package runmeet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/auth"
)

// 後台管理（規格 1.7：P1 必做，不是選配——全站第一個 UGC，沒有下架能力就不該上線）。
// 掛在 /api/v1/admin/run-meets，外層已有 RequireAuth → RequireAdmin → Audit → perm("run_meets")。
// ⚠️ Audit middleware 自動記錄所有 POST/PUT/PATCH/DELETE（main.go），
// 所以「人工返還配額」不需另建 ledger 表，留痕靠 Audit。

// --- 站內信（1-method 介面 + 晚繫結，比照 race/gpscalib 既有慣例）---
//
// 本套件**不 import internal/mail**：自行宣告最小介面，由 main.go 在啟動時注入 mail.Handler。
// 未注入時（測試環境／尚未 wiring）靜默跳過，不影響主流程；失敗只當作沒發。

type MailInserter interface {
	InsertForUsers(ctx context.Context, userIDs []string, level, title, body, url string) (int, error)
}

var mailer MailInserter

// SetMailInserter main.go 於 NewHandler 之後呼叫一次即可。
func SetMailInserter(m MailInserter) { mailer = m }

func sendMail(ctx context.Context, userIDs []string, level, title, body string) {
	if mailer == nil || len(userIDs) == 0 {
		return
	}
	_, _ = mailer.InsertForUsers(ctx, userIDs, level, title, body, "")
}

// AdminRouter 後台路由。
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminList)
	// 靜態段必須在 /{id} 之前宣告（可讀性；chi radix tree 本身已保證靜態優先）
	r.Get("/reports", h.AdminReports)
	r.Put("/reports/{rid}", h.AdminReviewReport)
	r.Get("/quota/{userID}", h.AdminGetQuota)
	r.Post("/quota/{userID}/adjust", h.AdminAdjustQuota)
	r.Post("/images/gc", h.AdminImageGC)

	r.Get("/{id}", h.AdminDetail)
	r.Post("/{id}/takedown", h.AdminTakedown)
	r.Post("/{id}/restore", h.AdminRestore)
	r.Delete("/{id}/comments/{cid}", h.AdminDeleteComment)
	return r
}

// AdminMeetRow 後台列表項。含 is_private 布林，**不含 hash**。
type AdminMeetRow struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	MeetAt        time.Time `json:"meet_at"`
	Region        string    `json:"region"`
	PlaceLabel    string    `json:"place_label"`
	Capacity      int       `json:"capacity"`
	MemberCount   int       `json:"member_count"`
	PendingCount  int       `json:"pending_count"`
	IsPrivate     bool      `json:"is_private"`
	Status        string    `json:"status"`
	HiddenByAdmin bool      `json:"hidden_by_admin"`
	HiddenReason  string    `json:"hidden_reason"`
	Deleted       bool      `json:"deleted"`
	CommentCount  int       `json:"comment_count"`
	ReactionCount int       `json:"reaction_count"`
	QuotaMonth    string    `json:"quota_month"`
	CreatedAt     time.Time `json:"created_at"`
	Owner         OwnerView `json:"owner"`
}

// GET /admin/run-meets?q=&status=&owner=&include_deleted=1&limit=&offset=
func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := pageParams(r, 30, 100)

	args := []any{}
	where := []string{"TRUE"}
	if q.Get("include_deleted") != "1" {
		where = append(where, "m.deleted_at IS NULL")
	}
	if s := strings.TrimSpace(q.Get("q")); s != "" {
		args = append(args, "%"+s+"%")
		i := len(args)
		where = append(where, "(m.title ILIKE $"+strconv.Itoa(i)+" OR m.place_label ILIKE $"+strconv.Itoa(i)+
			" OR m.region ILIKE $"+strconv.Itoa(i)+")")
	}
	if s := q.Get("status"); s == StatusOpen || s == StatusClosed || s == StatusCancelled {
		args = append(args, s)
		where = append(where, "m.status = $"+strconv.Itoa(len(args)))
	}
	if s := q.Get("owner"); s != "" && isValidUUID(s) {
		args = append(args, s)
		where = append(where, "m.owner_id = $"+strconv.Itoa(len(args)))
	}
	if q.Get("hidden") == "1" {
		where = append(where, "m.hidden_by_admin = TRUE")
	}
	whereSQL := " WHERE " + strings.Join(where, " AND ")

	var total int
	if err := h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM run_meets m`+whereSQL, args...).Scan(&total); err != nil {
		respondAPIErr(w, err)
		return
	}

	args = append(args, limit, offset)
	rows, err := h.db.Query(r.Context(), `
		SELECT m.id, m.title, m.meet_at, m.region, m.place_label, m.capacity, m.member_count,
		       m.pending_count, (m.join_password_hash IS NOT NULL), m.status, m.hidden_by_admin,
		       m.hidden_reason, (m.deleted_at IS NOT NULL), m.comment_count, m.reaction_count,
		       m.quota_month, m.created_at,
		       m.owner_id, COALESCE(NULLIF(u.name,''), u.handle), COALESCE(u.avatar_url,'')
		  FROM run_meets m JOIN users u ON u.id = m.owner_id`+whereSQL+`
		 ORDER BY m.created_at DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	defer rows.Close()
	items := []AdminMeetRow{}
	for rows.Next() {
		var a AdminMeetRow
		if err := rows.Scan(&a.ID, &a.Title, &a.MeetAt, &a.Region, &a.PlaceLabel, &a.Capacity,
			&a.MemberCount, &a.PendingCount, &a.IsPrivate, &a.Status, &a.HiddenByAdmin,
			&a.HiddenReason, &a.Deleted, &a.CommentCount, &a.ReactionCount, &a.QuotaMonth,
			&a.CreatedAt, &a.Owner.ID, &a.Owner.Name, &a.Owner.AvatarURL); err != nil {
			respondAPIErr(w, err)
			return
		}
		items = append(items, a)
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

// GET /admin/run-meets/{id}（含成員、留言；is_private 布林，不含 hash）
//
// 後台視角看得到成員層地點（lat/lng/meeting_detail）——這是刻意的：處理檢舉/糾紛時需要
// 完整資訊。MemberDetailView 的產出走 buildDetail(isAdmin=true)。
func (h *Handler) AdminDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondAPIErr(w, errBadID)
		return
	}
	viewer, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	m, err := h.repo.GetMeetAdmin(r.Context(), viewer, id)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	members, err := h.repo.ListMembers(r.Context(), id, MemberJoined)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	pending, err := h.repo.ListMembers(r.Context(), id, MemberPending)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	comments, _, err := h.repo.ListComments(r.Context(), id, viewer, m.OwnerID, 200, 0)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"meet": h.buildDetail(&m, viewer, true), "members": members,
		"pending": pending, "comments": comments,
		"hidden_by_admin": m.HiddenByAdmin, "hidden_reason": m.HiddenReason,
	})
}

// POST /admin/run-meets/{id}/takedown  {"reason":"..."}
// 強制下架 → hidden_by_admin=TRUE，前台任何端點對該團一律 404；同時發 urgent 站內信給發起人。
func (h *Handler) AdminTakedown(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondAPIErr(w, errBadID)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	reason, err := normalizeText(body.Reason, 100, false)
	if err != nil {
		respondAPIErr(w, errTooLong)
		return
	}
	var ownerID, title string
	err = h.db.QueryRow(r.Context(), `
		UPDATE run_meets SET hidden_by_admin=TRUE, hidden_reason=$2, updated_at=NOW()
		 WHERE id=$1 RETURNING owner_id, title`, id, reason).Scan(&ownerID, &title)
	if errors.Is(err, pgx.ErrNoRows) {
		respondAPIErr(w, errNotFound)
		return
	}
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	msg := "你發起的團練「" + title + "」已被管理員下架。"
	if reason != "" {
		msg += "原因：" + reason
	}
	msg += "若有疑問請與客服聯繫。"
	sendMail(r.Context(), []string{ownerID}, "urgent", "團練已被下架", msg)
	h.notify(r.Context(), ownerID)
	h.notifyMembers(r.Context(), id)
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// POST /admin/run-meets/{id}/restore（取消下架）
func (h *Handler) AdminRestore(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondAPIErr(w, errBadID)
		return
	}
	tag, err := h.db.Exec(r.Context(), `
		UPDATE run_meets SET hidden_by_admin=FALSE, hidden_reason='', updated_at=NOW() WHERE id=$1`, id)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		respondAPIErr(w, errNotFound)
		return
	}
	h.notifyMembers(r.Context(), id)
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DELETE /admin/run-meets/{id}/comments/{cid}（刪除違規留言，軟刪，deleted_by 記管理員）
func (h *Handler) AdminDeleteComment(w http.ResponseWriter, r *http.Request) {
	id, cid := chi.URLParam(r, "id"), chi.URLParam(r, "cid")
	if !isValidUUID(id) || !isValidUUID(cid) {
		respondAPIErr(w, errBadID)
		return
	}
	adminID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if err := h.repo.DeleteComment(r.Context(), id, cid, adminID, true); err != nil {
		respondAPIErr(w, err)
		return
	}
	h.notifyMembers(r.Context(), id)
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// AdminReportRow 檢舉清單項。
type AdminReportRow struct {
	ID           string    `json:"id"`
	MeetID       string    `json:"meet_id"`
	MeetTitle    string    `json:"meet_title"`
	CommentID    *string   `json:"comment_id"`
	CommentBody  string    `json:"comment_body"`
	ReporterID   string    `json:"reporter_id"`
	ReporterName string    `json:"reporter_name"`
	Reason       string    `json:"reason"`
	Status       string    `json:"status"`
	ReviewNote   string    `json:"review_note"`
	CreatedAt    time.Time `json:"created_at"`
}

// GET /admin/run-meets/reports?status=pending
func (h *Handler) AdminReports(w http.ResponseWriter, r *http.Request) {
	limit, offset := pageParams(r, 30, 100)
	status := r.URL.Query().Get("status")
	args := []any{}
	where := "TRUE"
	if status == "pending" || status == "handled" || status == "dismissed" {
		args = append(args, status)
		where = "rp.status = $1"
	}
	var total int
	if err := h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM run_meet_reports rp WHERE `+where, args...).Scan(&total); err != nil {
		respondAPIErr(w, err)
		return
	}
	args = append(args, limit, offset)
	rows, err := h.db.Query(r.Context(), `
		SELECT rp.id, rp.meet_id, m.title, rp.comment_id, COALESCE(c.body,''),
		       rp.reporter_id, COALESCE(NULLIF(u.name,''), u.handle),
		       rp.reason, rp.status, rp.review_note, rp.created_at
		  FROM run_meet_reports rp
		  JOIN run_meets m ON m.id = rp.meet_id
		  JOIN users u ON u.id = rp.reporter_id
		  LEFT JOIN run_meet_comments c ON c.id = rp.comment_id
		 WHERE `+where+`
		 ORDER BY rp.created_at DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	defer rows.Close()
	items := []AdminReportRow{}
	for rows.Next() {
		var a AdminReportRow
		if err := rows.Scan(&a.ID, &a.MeetID, &a.MeetTitle, &a.CommentID, &a.CommentBody,
			&a.ReporterID, &a.ReporterName, &a.Reason, &a.Status, &a.ReviewNote, &a.CreatedAt); err != nil {
			respondAPIErr(w, err)
			return
		}
		items = append(items, a)
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

// PUT /admin/run-meets/reports/{rid}  {"status":"handled","review_note":"..."}
func (h *Handler) AdminReviewReport(w http.ResponseWriter, r *http.Request) {
	rid := chi.URLParam(r, "rid")
	if !isValidUUID(rid) {
		respondAPIErr(w, errBadID)
		return
	}
	var body struct {
		Status     string `json:"status"`
		ReviewNote string `json:"review_note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAPIErr(w, errBadJSON)
		return
	}
	if body.Status != "handled" && body.Status != "dismissed" && body.Status != "pending" {
		respondAPIErr(w, errBadJSON)
		return
	}
	note, err := normalizeText(body.ReviewNote, 200, false)
	if err != nil {
		respondAPIErr(w, errTooLong)
		return
	}
	adminID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	tag, err := h.db.Exec(r.Context(), `
		UPDATE run_meet_reports SET status=$2, review_note=$3, reviewed_by=$4, reviewed_at=NOW()
		 WHERE id=$1`, rid, body.Status, note, adminID)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		respondAPIErr(w, errNotFound)
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /admin/run-meets/quota/{userID}
func (h *Handler) AdminGetQuota(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	if !isValidUUID(userID) {
		respondAPIErr(w, errBadID)
		return
	}
	month, used, err := h.repo.QuotaOf(r.Context(), userID)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	var isVIP bool
	_ = h.db.QueryRow(r.Context(), `SELECT COALESCE(vip_expires_at > NOW(), FALSE) FROM users WHERE id=$1`,
		userID).Scan(&isVIP)
	s := loadSettings(r.Context(), h.db)
	cur := QuotaMonth(time.Now())
	if month != cur {
		used = 0
	}
	cap := QuotaCap(isVIP, s.QuotaNormal, s.QuotaVIP)
	respondJSON(w, http.StatusOK, map[string]any{
		"user_id": userID, "month": cur, "cap": cap, "used": used,
		"remaining": maxInt(cap-used, 0), "is_vip": isVIP})
}

// POST /admin/run-meets/quota/{userID}/adjust  {"delta":-1,"reason":"..."}
//
// ⚠️ 這是**唯一**的配額返還管道（見 quota.go 檔頭：close/cancel/delete 一律不回補）。
// delta 為負＝返還次數（把 used 往下調）；正＝扣次數。夾在 [0, 999] 避免調成負值或溢位 SMALLINT。
//
// ⚠️ 留痕：Audit middleware（adminacct.go）的 meta 只有 method/path/status/操作者，**不含 request
// body**，所以光靠它事後完全無法對帳「返還了幾次、為什麼」。規格 1.7 說「不需另建 ledger 表」
// 的前提是留痕成立，所以這裡自行補寫一筆 audit_logs（沿用既有表，不新增 schema），
// meta 帶 delta / reason / before / after。後台 UI 的「調整原因（Audit 留痕）」才不是空頭承諾。
func (h *Handler) AdminAdjustQuota(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	if !isValidUUID(userID) {
		respondAPIErr(w, errBadID)
		return
	}
	var body struct {
		Delta  int    `json:"delta"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAPIErr(w, errBadJSON)
		return
	}
	if body.Delta == 0 || body.Delta < -50 || body.Delta > 50 {
		respondAPIErr(w, newErr(http.StatusBadRequest, "調整幅度請填 -50 到 50 之間的非零整數。"))
		return
	}
	reason, err := normalizeText(body.Reason, 200, false)
	if err != nil {
		respondAPIErr(w, errTooLong)
		return
	}

	month := QuotaMonth(time.Now())
	// 調整前的用量（供留痕；跨月時視同 0，與下面 SQL 的 CASE 一致）
	beforeMonth, before, err := h.repo.QuotaOf(r.Context(), userID)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	if beforeMonth != month {
		before = 0
	}

	var used int
	err = h.db.QueryRow(r.Context(), `
		UPDATE users
		   SET run_meet_month = $2,
		       run_meet_used  = LEAST(999, GREATEST(0,
		           CASE WHEN COALESCE(run_meet_month,'') = $2 THEN run_meet_used ELSE 0 END + $3))
		 WHERE id = $1
		RETURNING run_meet_used`, userID, month, body.Delta).Scan(&used)
	if errors.Is(err, pgx.ErrNoRows) {
		respondAPIErr(w, errNotFound)
		return
	}
	if err != nil {
		respondAPIErr(w, err)
		return
	}

	h.auditQuotaAdjust(r, userID, month, body.Delta, reason, before, used)
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "month": month, "used": used})
}

// auditQuotaAdjust 補一筆帶 delta/reason 的 audit_logs（沿用 001_init 既有表）。
// 失敗只當作沒留痕、不影響已完成的調整（與 adminacct.Audit 的 fire-and-forget 語意一致）。
func (h *Handler) auditQuotaAdjust(r *http.Request, targetUserID, month string, delta int, reason string, before, after int) {
	adminID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if adminID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = h.db.Exec(ctx, `
		INSERT INTO audit_logs (user_id, action, resource, resource_id, meta)
		SELECT id, 'adjust_quota', 'run-meets', $2,
		       jsonb_build_object('month',$3::text,'delta',$4::int,'reason',$5::text,
		                          'before',$6::int,'after',$7::int,'login',email,'name',name)
		  FROM users WHERE id=$1`,
		adminID, targetUserID, month, delta, reason, before, after)
}

// POST /admin/run-meets/images/gc
//
// 孤兒圖片清理（規格 4.5）。只刪 purpose='runmeet'、建立超過 24 小時、且沒有任何 run_meets
// 引用的圖。24 小時緩衝＝「已上傳但還沒按下建立」的暫存圖。
//
// ⚠️ 這是專案第一個會 DELETE images 列的機制（cmd/compressimages/main.go 檔頭明寫「永不刪除
// 資料」）。刻意走後台按鈕、不開排程（Neon compute 喚醒成本）。
func (h *Handler) AdminImageGC(w http.ResponseWriter, r *http.Request) {
	tag, err := h.db.Exec(r.Context(), `
		DELETE FROM images
		 WHERE purpose = 'runmeet'
		   AND created_at < NOW() - INTERVAL '24 hours'
		   AND NOT EXISTS (
		       SELECT 1 FROM run_meets m
		        WHERE ('/api/v1/images/' || images.id::text) = ANY(m.image_urls))`)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": tag.RowsAffected()})
}
