package runmeet

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// 留言與心情。
// ⚠️ 只提供刪除、**不提供編輯**（規格 4.4 硬規則）：可編輯等於「先發正常內容取得曝光，
// 事後改成惡意內容」，UGC 情境下這條路一定會被走。
// ⚠️ 留言是純文字，不走 htmlsafe（見 sanitize.go 檔頭）。

// ListComments／ListReplies（游標分頁、頂層留言＋一層回覆＋表情反應）已搬到 thread.go
// （migration 159：留言升級為討論串）。

// CreateComment 發留言／回覆。三道節流都在 DB 判（Redis 只擋粗粒度的 20/min），回覆同樣適用：
//
//	① 3 秒間隔      —— 擋連點/腳本刷版
//	② 每日 N 則     —— runmeet_comment_daily_cap（跨團加總，預設 100）
//	③ 重複內容      —— 同一團同一人 10 分鐘內同樣內容視為重複
//
// parentID 非 nil 時是回覆：只允許一層（見 thread.go validateReplyParent），成功時同一交易內
// 一併把父留言的 reply_count 與團練的 comment_count 都加 1。
func (r *Repository) CreateComment(ctx context.Context, meetID, uid, body string, parentID *string, dailyCap int) (CommentView, error) {
	var v CommentView

	var lastAt *time.Time
	if err := r.db.QueryRow(ctx, `
		SELECT MAX(created_at) FROM run_meet_comments WHERE user_id=$1`, uid).Scan(&lastAt); err != nil {
		return v, err
	}
	if lastAt != nil && time.Since(*lastAt) < commentMinIntervalSec*time.Second {
		return v, errCommentFast
	}

	var todayN int
	if err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM run_meet_comments
		 WHERE user_id=$1
		   AND created_at >= date_trunc('day', NOW() AT TIME ZONE 'Asia/Taipei') AT TIME ZONE 'Asia/Taipei'`,
		uid).Scan(&todayN); err != nil {
		return v, err
	}
	if dailyCap > 0 && todayN >= dailyCap {
		return v, errCommentCap
	}

	var dup bool
	if err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM run_meet_comments
		               WHERE meet_id=$1 AND user_id=$2 AND body=$3 AND deleted_at IS NULL
		                 AND created_at > NOW() - INTERVAL '10 minutes')`,
		meetID, uid, body).Scan(&dup); err != nil {
		return v, err
	}
	if dup {
		return v, errCommentDup
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return v, err
	}
	defer tx.Rollback(ctx)

	// 只允許一層：parent_id 指向的留言必須是「同一團練的頂層留言」且未刪除（規格 3；
	// 純函式判定見 thread.go validateReplyParent）。FOR UPDATE 鎖住父留言列，避免同時刪除
	// 父留言與建立回覆的競態（刪除那邊也在交易內，兩者互斥）。
	if parentID != nil {
		var st parentCommentState
		var pmMeetID string
		err := tx.QueryRow(ctx, `
			SELECT meet_id, parent_id, (deleted_at IS NOT NULL) FROM run_meet_comments
			 WHERE id=$1 FOR UPDATE`, *parentID).Scan(&pmMeetID, &st.ParentID, &st.Deleted)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return v, err
		}
		st.Exists = err == nil && pmMeetID == meetID
		if verr := validateReplyParent(st); verr != nil {
			return v, verr
		}
	}

	if err = tx.QueryRow(ctx, `
		INSERT INTO run_meet_comments (meet_id, user_id, body, parent_id) VALUES ($1,$2,$3,$4)
		RETURNING id, created_at`, meetID, uid, body, parentID).Scan(&v.ID, &v.CreatedAt); err != nil {
		return v, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE run_meets SET comment_count=comment_count+1, updated_at=NOW() WHERE id=$1`, meetID); err != nil {
		return v, err
	}
	if parentID != nil {
		if _, err = tx.Exec(ctx, `
			UPDATE run_meet_comments SET reply_count = reply_count + 1 WHERE id=$1`, *parentID); err != nil {
			return v, err
		}
	}
	if err = tx.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(name,''), handle), COALESCE(avatar_url,'') FROM users WHERE id=$1`, uid).
		Scan(&v.Name, &v.AvatarURL); err != nil {
		return v, err
	}
	v.UserID, v.Body, v.CanDelete = uid, body, true
	v.ParentID = parentID
	v.Reactions = []ReactionCountView{}
	v.Replies = []CommentView{}
	return v, tx.Commit(ctx)
}

// DeleteComment 軟刪（保留內容供爭議追溯）。
// byUID 必須是作者本人或該團發起人；後台另走 admin.go 的專屬端點（deleted_by 記管理員）。
func (r *Repository) DeleteComment(ctx context.Context, meetID, commentID, byUID string, isOwner bool) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	cond := ` AND user_id=$3`
	args := []any{commentID, meetID, byUID}
	if isOwner {
		cond = "" // 發起人可刪任何人的留言（規格 4.4 授權矩陣）
		args = []any{commentID, meetID, byUID}
	}
	tag, err := tx.Exec(ctx, `
		UPDATE run_meet_comments SET deleted_at=NOW(), deleted_by=$3
		 WHERE id=$1 AND meet_id=$2 AND deleted_at IS NULL`+cond, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	if _, err = tx.Exec(ctx, `
		UPDATE run_meets SET comment_count=GREATEST(comment_count-1,0), updated_at=NOW() WHERE id=$1`,
		meetID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetReaction 心情 upsert（PK(meet_id,user_id) 保證一人一團只有一種 → 天然防洗榜）。
// 換心情不改 reaction_count（換一種不是多一票）。
func (r *Repository) SetReaction(ctx context.Context, meetID, uid, kind string) error {
	if !ReactionKinds[kind] {
		return errBadReaction
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		INSERT INTO run_meet_reactions (meet_id, user_id, kind) VALUES ($1,$2,$3)
		ON CONFLICT (meet_id, user_id) DO UPDATE SET kind=EXCLUDED.kind, created_at=NOW()`,
		meetID, uid, kind)
	if err != nil {
		return err
	}
	// pgx 對 ON CONFLICT DO UPDATE 一律回 1 列，無法區分新增/更新 → 改以「這人先前有沒有列」判定。
	// 這裡用 xmax=0 的技巧太隱晦，改成事後對齊計數（一次 UPDATE，成本可接受且恆正確）。
	_ = tag
	if _, err = tx.Exec(ctx, `
		UPDATE run_meets SET reaction_count = (SELECT COUNT(*) FROM run_meet_reactions WHERE meet_id=$1),
		                     updated_at=NOW()
		 WHERE id=$1`, meetID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RemoveReaction 取消心情。
func (r *Repository) RemoveReaction(ctx context.Context, meetID, uid string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx, `DELETE FROM run_meet_reactions WHERE meet_id=$1 AND user_id=$2`, meetID, uid); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE run_meets SET reaction_count = (SELECT COUNT(*) FROM run_meet_reactions WHERE meet_id=$1),
		                     updated_at=NOW()
		 WHERE id=$1`, meetID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CommentMeetID 查某則留言屬於哪個團（檢舉留言時驗證 meet_id 一致性用）。
func (r *Repository) CommentMeetID(ctx context.Context, commentID string) (string, error) {
	var meetID string
	err := r.db.QueryRow(ctx, `SELECT meet_id FROM run_meet_comments WHERE id=$1 AND deleted_at IS NULL`,
		commentID).Scan(&meetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errNotFound
	}
	return meetID, err
}
