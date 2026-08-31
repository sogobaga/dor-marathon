package runmeet

// 留言討論串（migration 159）：Threads 式「頂層留言 + 一層回覆」＋單則留言表情反應＋游標分頁。
//
// ⚠️ 只允許一層：本檔的 validateReplyParent 是唯一的權威判定（handler/repository 都呼叫這裡），
// 見 migrations/159_runmeet_comment_thread.sql 檔頭的設計理由。
//
// ⚠️ 討論串分頁查詢刻意不濾 deleted_at：軟刪的留言仍要以佔位形式留在串裡（見 CommentView
// 檔頭），由 maskDeleted 負責遮蔽 Body/Reactions 等欄位，不是靠 WHERE 排除。migration 159 原本
// 建的 idx_rmc_thread 是局部索引（WHERE deleted_at IS NULL），套不上這個查詢，被迫拆成
// UNION ALL 兩支（未刪那支吃得到局部索引；已刪那支吃不到，Seq Scan+Sort，在 16 萬筆留言的
// 環境實測要 11.37 ms、佔整條查詢 98% 時間）。migration 160 已把索引換成不帶謂詞的
// idx_rmc_thread_all (meet_id, parent_id, created_at DESC, id DESC)，兩種留言現在共用同一個
// 索引，查詢改回單一查詢（見 runThreadPage），同一環境實測降到 1.95 ms。

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// --- 純函式（cursor 編解碼／一層限制／反應排序／軟刪遮蔽／canReact）：全部可脫離 DB 單元測試 ---

// encodeCursor 把 (created_at, id) 編碼成不透明字串：base64(unixNano|id)。
// 用 UnixNano 而不是 RFC3339 字串，是為了避免時區／秒以下精度在序列化來回時失真，
// 導致游標邊界跟 SQL 的 (created_at,id) 比較對不齊（多回或漏回一筆）。
func encodeCursor(createdAt time.Time, id string) string {
	raw := fmt.Sprintf("%d|%s", createdAt.UnixNano(), id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor 解碼；任何格式錯誤（含空字串、非法 base64、缺分隔、id 不是合法 UUID）一律
// ok=false——規格明定「無效 cursor 一律當第一頁處理、不報錯」，呼叫端看到 ok=false 就不加
// 游標條件，不回錯誤。
func decodeCursor(s string) (createdAt time.Time, id string, ok bool) {
	if s == "" {
		return time.Time{}, "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", false
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 || parts[1] == "" {
		return time.Time{}, "", false
	}
	var nanos int64
	if _, err := fmt.Sscanf(parts[0], "%d", &nanos); err != nil {
		return time.Time{}, "", false
	}
	if !isValidUUID(parts[1]) {
		return time.Time{}, "", false
	}
	return time.Unix(0, nanos).UTC(), parts[1], true
}

// parentCommentState 建回覆前，parent_id 指向的留言查出來的狀態（純資料，不含 DB handle），
// 供 validateReplyParent 判定。
type parentCommentState struct {
	// Exists：查無列，或列存在但不屬於同一團練，一律 false——不區分「不存在」與「跨團」，
	// 兩者對外都是同一種「找不到要回覆的留言」，不必讓使用者分辨（比照套件既有的 404 哲學，
	// 見 repository.go GetMeet 附近註解）。
	Exists   bool
	Deleted  bool    // deleted_at IS NOT NULL
	ParentID *string // 這則留言自己的 parent_id；非 nil 代表它本身已經是回覆（第二層）
}

// validateReplyParent 只允許一層回覆＋不可回覆已刪留言的核心判定（純函式，供單元測試；規格 3）。
func validateReplyParent(s parentCommentState) error {
	if !s.Exists {
		return errCommentParentNotFound
	}
	if s.Deleted {
		return errCommentDeleted
	}
	if s.ParentID != nil {
		return errCommentNestedReply
	}
	return nil
}

// sortReactions 依 count desc、kind asc 排序（規格固定順序：反應數多的在前，同票數按代碼字母序，
// 避免每次重新整理順序跳動）。純函式，供單元測試。
func sortReactions(rs []ReactionCountView) {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Count != rs[j].Count {
			return rs[i].Count > rs[j].Count
		}
		return rs[i].Kind < rs[j].Kind
	})
}

// maskDeleted 套用軟刪留言的欄位遮蔽規則（規格「其他要求」）：deleted_at 非空一律
// Deleted=true、Body=""、CanDelete=false、Reactions=[]、MyReaction=nil。純函式，供單元測試。
func maskDeleted(v *CommentView, deletedAt *time.Time) {
	if deletedAt == nil {
		return
	}
	v.Deleted = true
	v.Body = ""
	v.CanDelete = false
	v.Reactions = []ReactionCountView{}
	v.MyReaction = nil
}

// canReact 留言表情反應權限：與 canComment 同樣要求 joined/owner、團未中止，但**不**擋
// 「結束後 7 天」的唯讀期——規格 4：「不見得我們都想要留言，但是表達心情是可以的」，只讀期只
// 擋新留言，不擋表情。純函式，供單元測試。
func canReact(isOwner bool, myStatus *string, status string) bool {
	if !isOwner && (myStatus == nil || *myStatus != MemberJoined) {
		return false
	}
	return status != StatusCancelled
}

// --- DB 掃描列與 DTO 組裝 ---

// threadCommentRow 一則留言（頂層或回覆）的原始欄位，供 ListComments/ListReplies/
// batchEarliestReplies 共用掃描；欄位順序須與 threadCommentCols 一致。
type threadCommentRow struct {
	ID         string
	UserID     string
	Name       string
	AvatarURL  string
	Body       string
	CreatedAt  time.Time
	ParentID   *string
	ReplyCount int
	DeletedAt  *time.Time
}

const threadCommentCols = `
	c.id, c.user_id, COALESCE(NULLIF(u.name,''), u.handle), COALESCE(u.avatar_url,''),
	c.body, c.created_at, c.parent_id, c.reply_count, c.deleted_at`

func scanThreadComment(row interface{ Scan(...any) error }) (threadCommentRow, error) {
	var c threadCommentRow
	err := row.Scan(&c.ID, &c.UserID, &c.Name, &c.AvatarURL, &c.Body, &c.CreatedAt,
		&c.ParentID, &c.ReplyCount, &c.DeletedAt)
	return c, err
}

// toCommentView threadCommentRow → CommentView，套用軟刪遮蔽；Reactions/Replies 先給空陣列
// （前端契約恆為陣列，不是 null），呼叫端稍後用批次查詢結果覆蓋 Reactions/MyReaction。
func toCommentView(row threadCommentRow, canDelete bool) CommentView {
	v := CommentView{
		ID: row.ID, UserID: row.UserID, Name: row.Name, AvatarURL: row.AvatarURL,
		Body: row.Body, CreatedAt: row.CreatedAt, CanDelete: canDelete,
		ParentID: row.ParentID, ReplyCount: row.ReplyCount,
		Reactions: []ReactionCountView{}, Replies: []CommentView{},
	}
	maskDeleted(&v, row.DeletedAt)
	return v
}

// applyReactions 把批次查出來的反應統計／我的反應套進單則留言；deleted 留言就算 DB 裡還留著
// 歷史反應（刪除前按過的），也一律遮成 []／nil（雙重保險，maskDeleted 在 toCommentView 已做過
// 一次，這裡是因為 Reactions/MyReaction 是在那之後才被批次結果覆蓋，才需要再擋一次）。
func applyReactions(v *CommentView, byComment map[string][]ReactionCountView, myByComment map[string]string) {
	if v.Deleted {
		v.Reactions = []ReactionCountView{}
		v.MyReaction = nil
		return
	}
	if rs, ok := byComment[v.ID]; ok {
		v.Reactions = rs
	}
	if k, ok := myByComment[v.ID]; ok {
		kk := k
		v.MyReaction = &kk
	}
}

// --- 游標分頁核心查詢（單一查詢，取代舊版 UNION ALL 未刪／已刪兩支；見檔頭說明）---

// runThreadPage 撈一頁留言：parentEq==nil 時撈頂層（parent_id IS NULL），否則撈 *parentEq
// 底下的回覆（parent_id = *parentEq）。desc=true 用於頂層（created_at DESC, id DESC，最新在
// 前）；desc=false 用於回覆（ASC，對話由舊到新才讀得順）。多撈一筆（limit+1）判斷還有沒有下一頁，
// 回傳時已裁回 limit 筆。
//
// SQL 結構（由內而外）：
//  1. 最內層只碰 run_meet_comments、只用得到 idx_rmc_thread_all（migration 160）欄位的條件
//     （meet_id 等值 → parent_id 等值 → (created_at,id) 游標範圍），排序＋LIMIT limit+1 都在
//     這層做——規劃器因此會用 Index Scan 直接取 N 筆結束，不必先把整個討論串撈出來再排序
//     （這正是舊版 UNION ALL「已刪」分支 Seq Scan+Sort 的病灶）。已刪留言不再被濾掉：軟刪的
//     留言本來就該一起回來，由 maskDeleted 負責遮蔽 Body/Reactions 等欄位、以佔位形式呈現。
//  2. 中間層在這個已經 LIMIT 過的小結果集（最多 limit+1 筆）上用 COUNT(*) OVER() 算出
//     fetched_n——即「子查詢實際撈回幾筆」，供最外層判斷 hasMore（見下方 JOIN 的註解）。
//  3. 最外層才 JOIN users 取顯示用的 name/avatar_url，並重新 ORDER BY 一次（子查詢內的排序
//     不保證外層一定保留）。
//
// 已在 16 萬筆留言的環境用 EXPLAIN (ANALYZE) 實測（2026-08-31，同一團練 600 則留言，詳見
// migrations/160_runmeet_thread_index_fix.sql 檔頭）：
//
//	舊版 UNION ALL 兩支（未刪 0.18ms + 已刪 11.37ms）   11.56 ms
//	單一查詢、JOIN users 寫在外層                        3.26 ms
//	單一查詢、子查詢先 LIMIT+1 再 JOIN users（本版）      1.95 ms
//
// 這條 SQL 已用上述實測驗證過，不是紙上談兵。
func (r *Repository) runThreadPage(ctx context.Context, meetID string, parentEq *string, desc bool,
	afterAt *time.Time, afterID *string, limit int) ([]threadCommentRow, bool, error) {

	op, dir := ">", "ASC"
	if desc {
		op, dir = "<", "DESC"
	}

	// parentCond／atPh／idPh／limPh 用具名字串組出各自要用的 $N，不靠 fmt 動詞位置對應——
	// 兩個分支（頂層 vs 回覆）各自使用的參數數量不同（少一個 parentID），用具名變數組字串
	// 比用 fmt.Sprintf 數第幾個 %d 更不容易出錯。
	var parentCond, atPh, idPh, limPh string
	args := []any{meetID}
	if parentEq != nil {
		parentCond = "parent_id = $2"
		args = append(args, *parentEq)
		atPh, idPh, limPh = "$3", "$4", "$5"
	} else {
		parentCond = "parent_id IS NULL"
		atPh, idPh, limPh = "$2", "$3", "$4"
	}
	args = append(args, afterAt, afterID, limit+1)

	// threadCommentCols 沿用（見上方常數）：中間層的欄位名與 run_meet_comments 同名
	// （id/user_id/body/created_at/parent_id/reply_count/deleted_at），所以 `c.xxx` 在這裡
	// 一樣解析得到；c.fetched_n 是中間層另外算出來、只給這支查詢用的欄位。
	q := `
		SELECT ` + threadCommentCols + `, c.fetched_n
		  FROM (
		    SELECT id, user_id, body, created_at, parent_id, reply_count, deleted_at,
		           COUNT(*) OVER () AS fetched_n
		      FROM (
		        SELECT id, user_id, body, created_at, parent_id, reply_count, deleted_at
		          FROM run_meet_comments
		         WHERE meet_id=$1 AND ` + parentCond + `
		           AND (` + atPh + `::timestamptz IS NULL OR (created_at, id) ` + op + ` (` +
		atPh + `::timestamptz, ` + idPh + `::uuid))
		         ORDER BY created_at ` + dir + `, id ` + dir + `
		         LIMIT ` + limPh + `
		      ) page
		  ) c
		  -- is_virtual=FALSE 是防禦性條件（虛擬選手見 migration 146，只有系統生成的跑步紀錄，
		  -- 不會留言），但過濾發生在上面 LIMIT 之後：理論上如果虛擬選手哪天真的能留言，這裡
		  -- 可能讓這一頁少回幾筆（少的是虛擬選手那幾筆，不影響真人留言的完整性）。hasMore 用
		  -- fetched_n（LIMIT 當下、JOIN 之前的筆數）判斷、不是用 JOIN 後掃到的列數，所以不會
		  -- 因為 JOIN 篩掉幾筆就誤判「沒有下一頁」而讓 next_cursor 提早消失。
		  JOIN users u ON u.id = c.user_id AND u.is_virtual = FALSE
		 ORDER BY c.created_at ` + dir + `, c.id ` + dir

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var out []threadCommentRow
	fetchedN := 0
	for rows.Next() {
		var c threadCommentRow
		var n int
		if err := rows.Scan(&c.ID, &c.UserID, &c.Name, &c.AvatarURL, &c.Body, &c.CreatedAt,
			&c.ParentID, &c.ReplyCount, &c.DeletedAt, &n); err != nil {
			return nil, false, err
		}
		fetchedN = n // COUNT(*) OVER() 沒有 PARTITION BY，同一批列裡每筆都是同一個值
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := fetchedN > limit
	if len(out) > limit {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// --- 批次查詢（N+1 對策）---

// batchEarliestReplies 撈這一頁每則頂層留言「最早 n 則回覆」（含軟刪佔位），用 window function
// 一次查完整批（WHERE parent_id = ANY($2)），不對每則頂層留言各查一次。
func (r *Repository) batchEarliestReplies(ctx context.Context, meetID string, parentIDs []string, n int,
	viewerID, ownerID string) (map[string][]CommentView, error) {
	out := map[string][]CommentView{}
	if len(parentIDs) == 0 || n <= 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, name, avatar_url, body, created_at, parent_id, reply_count, deleted_at
		  FROM (
		    SELECT c.id, c.user_id, COALESCE(NULLIF(u.name,''), u.handle) AS name,
		           COALESCE(u.avatar_url,'') AS avatar_url, c.body, c.created_at, c.parent_id,
		           c.reply_count, c.deleted_at,
		           ROW_NUMBER() OVER (PARTITION BY c.parent_id ORDER BY c.created_at ASC, c.id ASC) AS rn
		      FROM run_meet_comments c
		      JOIN users u ON u.id = c.user_id AND u.is_virtual = FALSE
		     WHERE c.meet_id=$1 AND c.parent_id = ANY($2::uuid[])
		  ) ranked
		 WHERE rn <= $3
		 ORDER BY parent_id, created_at ASC, id ASC`, meetID, parentIDs, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanThreadComment(rows)
		if err != nil {
			return nil, err
		}
		v := toCommentView(c, c.UserID == viewerID || (viewerID != "" && viewerID == ownerID))
		out[*c.ParentID] = append(out[*c.ParentID], v)
	}
	return out, rows.Err()
}

// batchReactionCounts 批次撈一批留言的反應統計，依 count desc/kind asc 排序後回傳；
// 一次 WHERE comment_id = ANY($1) 查完（走 idx_rmcr_comment），不逐筆查。
func (r *Repository) batchReactionCounts(ctx context.Context, commentIDs []string) (map[string][]ReactionCountView, error) {
	out := map[string][]ReactionCountView{}
	if len(commentIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT comment_id, kind, COUNT(*) FROM run_meet_comment_reactions
		 WHERE comment_id = ANY($1::uuid[])
		 GROUP BY comment_id, kind`, commentIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, kind string
		var n int
		if err := rows.Scan(&cid, &kind, &n); err != nil {
			return nil, err
		}
		out[cid] = append(out[cid], ReactionCountView{Kind: kind, Count: n})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for cid := range out {
		sortReactions(out[cid])
	}
	return out, nil
}

// batchMyReactions 撈觀看者在這個團練「所有留言」按過的表情，一次查完（走 idx_rmcr_meet_user：
// WHERE meet_id=$1 AND user_id=$2）。同一人同一團的反應筆數天生就少，不必再依這一頁的
// comment_id 清單重查一次。
func (r *Repository) batchMyReactions(ctx context.Context, meetID, viewerID string) (map[string]string, error) {
	out := map[string]string{}
	if viewerID == "" {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT comment_id, kind FROM run_meet_comment_reactions
		 WHERE meet_id=$1 AND user_id=$2`, meetID, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, kind string
		if err := rows.Scan(&cid, &kind); err != nil {
			return nil, err
		}
		out[cid] = kind
	}
	return out, rows.Err()
}

// --- 對外端點用的 repository 方法 ---

// ListComments 頂層留言（parent_id IS NULL）游標分頁，依 created_at DESC, id DESC。
// 每則頂層留言隨附最早 replyPreview 則回覆與 reply_count（詳情頁不必再打一次 API）；
// N+1 對策：回覆／反應／我的反應三者都用「一次批次查詢」撈回，不對每則留言各查一次。
// total 為該團**未刪**頂層留言數（供「查看全部留言(N)」用）；migration 160 後 idx_rmc_thread_all
// 不再帶 WHERE deleted_at IS NULL 謂詞，這條 COUNT 查詢改成靠索引前綴(meet_id, parent_id)
// 縮小掃描範圍到「這個團練的頂層留言」，deleted_at IS NULL 變成 Filter（而非索引內建謂詞）；
// 掃描範圍仍侷限在單一團練、不是全站，成本不受影響。
func (r *Repository) ListComments(ctx context.Context, meetID, viewerID, ownerID string, limit int,
	cursorStr string, replyPreview int) ([]CommentView, *string, int, error) {

	var total int
	if err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM run_meet_comments
		 WHERE meet_id=$1 AND parent_id IS NULL AND deleted_at IS NULL`, meetID).Scan(&total); err != nil {
		return nil, nil, 0, err
	}

	afterAt, afterID, hasCursor := decodeCursor(cursorStr)
	var atPtr *time.Time
	var idPtr *string
	if hasCursor {
		atPtr, idPtr = &afterAt, &afterID
	}

	rows, hasMore, err := r.runThreadPage(ctx, meetID, nil, true, atPtr, idPtr, limit)
	if err != nil {
		return nil, nil, 0, err
	}

	items := make([]CommentView, len(rows))
	ids := make([]string, len(rows))
	for i, row := range rows {
		items[i] = toCommentView(row, row.UserID == viewerID || (viewerID != "" && viewerID == ownerID))
		ids[i] = row.ID
	}

	repliesByParent, err := r.batchEarliestReplies(ctx, meetID, ids, replyPreview, viewerID, ownerID)
	if err != nil {
		return nil, nil, 0, err
	}

	allIDs := append([]string{}, ids...)
	for _, rs := range repliesByParent {
		for _, rep := range rs {
			allIDs = append(allIDs, rep.ID)
		}
	}

	reactionMap, err := r.batchReactionCounts(ctx, allIDs)
	if err != nil {
		return nil, nil, 0, err
	}
	myMap, err := r.batchMyReactions(ctx, meetID, viewerID)
	if err != nil {
		return nil, nil, 0, err
	}

	for i := range items {
		applyReactions(&items[i], reactionMap, myMap)
		if rs, ok := repliesByParent[items[i].ID]; ok {
			for j := range rs {
				applyReactions(&rs[j], reactionMap, myMap)
			}
			items[i].Replies = rs
		}
	}

	var next *string
	if hasMore && len(rows) > 0 {
		s := encodeCursor(rows[len(rows)-1].CreatedAt, rows[len(rows)-1].ID)
		next = &s
	}
	return items, next, total, nil
}

// ListReplies 某頂層留言的回覆，依 created_at ASC, id ASC 游標分頁（正序：對話由舊到新才讀得
// 順）。parentID 必須是同一團練、且自身為頂層留言（parent_id IS NULL），否則 404——不區分
// 「不存在」與「其實是回覆／跨團」，比照套件既有的 404 哲學。
func (r *Repository) ListReplies(ctx context.Context, meetID, parentID, viewerID, ownerID string, limit int,
	cursorStr string) ([]CommentView, *string, error) {

	var pmMeet string
	var pParent *string
	err := r.db.QueryRow(ctx, `SELECT meet_id, parent_id FROM run_meet_comments WHERE id=$1`, parentID).
		Scan(&pmMeet, &pParent)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, err
	}
	if errors.Is(err, pgx.ErrNoRows) || pmMeet != meetID || pParent != nil {
		return nil, nil, errNotFound
	}

	afterAt, afterID, hasCursor := decodeCursor(cursorStr)
	var atPtr *time.Time
	var idPtr *string
	if hasCursor {
		atPtr, idPtr = &afterAt, &afterID
	}

	pid := parentID
	rows, hasMore, err := r.runThreadPage(ctx, meetID, &pid, false, atPtr, idPtr, limit)
	if err != nil {
		return nil, nil, err
	}

	items := make([]CommentView, len(rows))
	ids := make([]string, len(rows))
	for i, row := range rows {
		items[i] = toCommentView(row, row.UserID == viewerID || (viewerID != "" && viewerID == ownerID))
		ids[i] = row.ID
	}

	reactionMap, err := r.batchReactionCounts(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	myMap, err := r.batchMyReactions(ctx, meetID, viewerID)
	if err != nil {
		return nil, nil, err
	}
	for i := range items {
		applyReactions(&items[i], reactionMap, myMap)
	}

	var next *string
	if hasMore && len(rows) > 0 {
		s := encodeCursor(rows[len(rows)-1].CreatedAt, rows[len(rows)-1].ID)
		next = &s
	}
	return items, next, nil
}

// --- 單則留言表情反應（migration 159；與既有團練層級 SetReaction/RemoveReaction 是兩張表、
// 兩套端點，互不影響）---

// reactionsForComment 單則留言目前的完整反應統計（已排序）。呼叫端在同一交易內呼叫，
// 讀到的是這次異動之後的最新狀態。
func reactionsForComment(ctx context.Context, tx pgx.Tx, commentID string) ([]ReactionCountView, error) {
	rows, err := tx.Query(ctx, `
		SELECT kind, COUNT(*) FROM run_meet_comment_reactions
		 WHERE comment_id=$1 GROUP BY kind`, commentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReactionCountView{}
	for rows.Next() {
		var rc ReactionCountView
		if err := rows.Scan(&rc.Kind, &rc.Count); err != nil {
			return nil, err
		}
		out = append(out, rc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortReactions(out)
	return out, nil
}

// lockCommentForReaction 鎖列＋確認留言屬於這個團練且未刪（不可對已刪留言按表情——規格「其他
// 要求」：回 409）。FOR UPDATE 避免同時刪留言與按表情的競態。
func lockCommentForReaction(ctx context.Context, tx pgx.Tx, meetID, commentID string) error {
	var deleted bool
	err := tx.QueryRow(ctx, `
		SELECT deleted_at IS NOT NULL FROM run_meet_comments
		 WHERE id=$1 AND meet_id=$2 FOR UPDATE`, commentID, meetID).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	if deleted {
		return errCommentDeleted
	}
	return nil
}

// SetCommentReaction 設定／更換某則留言的表情反應（UPSERT，PK(comment_id,user_id) 保證一人
// 一則一種）。同一交易內維護 run_meet_comments.reaction_count，回傳更新後的完整反應狀態。
func (r *Repository) SetCommentReaction(ctx context.Context, meetID, commentID, uid, kind string) (
	[]ReactionCountView, *string, error) {
	if !ReactionKinds[kind] {
		return nil, nil, errBadReaction
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	if err := lockCommentForReaction(ctx, tx, meetID, commentID); err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO run_meet_comment_reactions (comment_id, user_id, meet_id, kind) VALUES ($1,$2,$3,$4)
		ON CONFLICT (comment_id, user_id) DO UPDATE SET kind=EXCLUDED.kind, created_at=NOW()`,
		commentID, uid, meetID, kind); err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE run_meet_comments
		   SET reaction_count = (SELECT COUNT(*) FROM run_meet_comment_reactions WHERE comment_id=$1)
		 WHERE id=$1`, commentID); err != nil {
		return nil, nil, err
	}
	reactions, err := reactionsForComment(ctx, tx, commentID)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	my := kind
	return reactions, &my, nil
}

// RemoveCommentReaction 取消某則留言的表情反應（PUT body {"kind":null} 或 DELETE 同路徑）。
func (r *Repository) RemoveCommentReaction(ctx context.Context, meetID, commentID, uid string) (
	[]ReactionCountView, *string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	if err := lockCommentForReaction(ctx, tx, meetID, commentID); err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec(ctx, `
		DELETE FROM run_meet_comment_reactions WHERE comment_id=$1 AND user_id=$2`, commentID, uid); err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE run_meet_comments
		   SET reaction_count = (SELECT COUNT(*) FROM run_meet_comment_reactions WHERE comment_id=$1)
		 WHERE id=$1`, commentID); err != nil {
		return nil, nil, err
	}
	reactions, err := reactionsForComment(ctx, tx, commentID)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return reactions, nil, nil
}
