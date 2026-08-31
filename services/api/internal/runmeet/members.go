package runmeet

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// 名額併發的核心不變式（規格 1.5）：
//
//	member_count == COUNT(*) WHERE status='joined'（含發起人）
//	member_count <= capacity 由應用層在鎖內保證（DB 刻意不加 CHECK）
//	pending 不占名額
//
// ⚠️ 唯一序列化點：SELECT ... FOR UPDATE on run_meets。所有會改動 member_count / pending_count
// 的路徑都必須先鎖同一列（照抄既有防超賣範本 race/repository.go 的「鎖分組、檢查名額」）。
// GREATEST(x-1, 0) 是安全網不是正確性依賴——正確性靠每條路徑的 CAS 閘門（RowsAffected=0 → 409），
// 同 SettleCancellation 的思路（那支曾被審查抓到「重試退兩次錢」）。

// lockedMeet FOR UPDATE 鎖出來的團練狀態。
type lockedMeet struct {
	OwnerID      string
	Capacity     int
	MemberCount  int
	PendingCount int
	Status       string
	MeetAt       time.Time
	IsPrivate    bool
	Approval     bool
}

func lockMeet(ctx context.Context, tx pgx.Tx, meetID string) (lockedMeet, error) {
	var m lockedMeet
	err := tx.QueryRow(ctx, `
		SELECT owner_id, capacity, member_count, pending_count, status, meet_at,
		       (join_password_hash IS NOT NULL), approval_required
		  FROM run_meets
		 WHERE id=$1 AND deleted_at IS NULL AND hidden_by_admin = FALSE
		 FOR UPDATE`, meetID).
		Scan(&m.OwnerID, &m.Capacity, &m.MemberCount, &m.PendingCount, &m.Status, &m.MeetAt,
			&m.IsPrivate, &m.Approval)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, errNotFound
	}
	return m, err
}

// JoinResult 加入/申請的結果（前端據此顯示 toast 與切換 CTA）。
type JoinResult struct {
	State string `json:"state"` // joined | pending
}

// Join 自由加入（規格 1.5(a)）與申請加入（(b)）走同一支——由團的 approval_required 決定分支，
// 客戶端無法指定（避免「自稱自由加入」繞過審核）。
func (r *Repository) Join(ctx context.Context, uid, meetID, note string, s Settings) (JoinResult, error) {
	var res JoinResult
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)

	m, err := lockMeet(ctx, tx, meetID)
	if err != nil {
		return res, err
	}
	if m.OwnerID == uid {
		return res, errAlreadyJoined
	}
	if e := meetStatusError(m.Status, !m.MeetAt.After(time.Now())); e != nil {
		return res, e
	}

	// 現有成員列（決定能不能加入／是否在冷卻中）
	var cur, role string
	var decidedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT status, role, decided_at FROM run_meet_members WHERE meet_id=$1 AND user_id=$2`,
		meetID, uid).Scan(&cur, &role, &decidedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return res, err
	}
	switch cur {
	case MemberJoined:
		return res, errAlreadyJoined
	case MemberPending:
		return res, errAlreadyPending
	case MemberKicked:
		// 被剔除者不得自行回鍋（需發起人在成員管理解除封鎖）
		return res, errKicked
	case MemberRejected:
		if decidedAt != nil && s.RejectCooldownHours > 0 &&
			time.Since(*decidedAt) < time.Duration(s.RejectCooldownHours)*time.Hour {
			return res, newErrRejectCooldown(s.RejectCooldownHours)
		}
	}

	// 額滿：自由加入與申請加入都擋（額滿還讓人白申請是壞體驗）
	if m.MemberCount >= m.Capacity {
		return res, errMeetFull
	}

	// ⚠️ UPSERT 的 RowsAffected 必須檢查：ON CONFLICT DO UPDATE ... WHERE 若不成立會影響 0 列，
	// 這時若還是無條件把計數 +1，member_count 就會與「實際 joined 列數」永久脫鉤（不變式破了，
	// 而且沒有任何地方會修回來）。上面的 lockMeet + 狀態預檢已讓這條路走不到，這裡是第二道
	// CAS 閘門——同 SettleCancellation「只結算一次」的思路：寧可回 409 也不要靜默寫壞計數。
	if m.Approval {
		if m.PendingCount >= s.PendingMax {
			return res, errPendingFull
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO run_meet_members (meet_id, user_id, role, status, apply_note, applied_at)
			VALUES ($1,$2,'member','pending',$3,NOW())
			ON CONFLICT (meet_id, user_id) DO UPDATE
			   SET status='pending', apply_note=EXCLUDED.apply_note, applied_at=NOW(),
			       decided_at=NULL, decided_by=NULL
			 WHERE run_meet_members.status IN ('rejected','left')`, meetID, uid, note)
		if err != nil {
			return res, err
		}
		if tag.RowsAffected() == 0 {
			return res, errAlreadyPending
		}
		if _, err = tx.Exec(ctx, `UPDATE run_meets SET pending_count=pending_count+1, updated_at=NOW() WHERE id=$1`,
			meetID); err != nil {
			return res, err
		}
		res.State = MemberPending
	} else {
		tag, err := tx.Exec(ctx, `
			INSERT INTO run_meet_members (meet_id, user_id, role, status, applied_at, joined_at)
			VALUES ($1,$2,'member','joined',NOW(),NOW())
			ON CONFLICT (meet_id, user_id) DO UPDATE
			   SET status='joined', joined_at=NOW(), decided_at=NULL, decided_by=NULL
			 WHERE run_meet_members.status IN ('rejected','left')`, meetID, uid)
		if err != nil {
			return res, err
		}
		if tag.RowsAffected() == 0 {
			return res, errAlreadyJoined
		}
		if _, err = tx.Exec(ctx, `UPDATE run_meets SET member_count=member_count+1, updated_at=NOW() WHERE id=$1`,
			meetID); err != nil {
			return res, err
		}
		res.State = MemberJoined
	}
	return res, tx.Commit(ctx)
}

func newErrRejectCooldown(hours int) *apiErr {
	return newErr(429, "發起人剛婉拒了你的申請，請 "+itoa(hours)+" 小時後再試。")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// LeaveOrWithdraw 撤回申請（pending）／自行退出（joined）。發起人不得退出自己的團。
func (r *Repository) LeaveOrWithdraw(ctx context.Context, uid, meetID string) (string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	m, err := lockMeet(ctx, tx, meetID)
	if err != nil {
		return "", err
	}
	if m.OwnerID == uid {
		return "", errOwnerCantLeave
	}

	// 先試「撤回申請」：pending → 直接刪列（讓對方之後還能重新申請，不留 rejected 冷卻）
	tag, err := tx.Exec(ctx, `DELETE FROM run_meet_members WHERE meet_id=$1 AND user_id=$2 AND status='pending'`,
		meetID, uid)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() > 0 {
		if _, err = tx.Exec(ctx, `
			UPDATE run_meets SET pending_count=GREATEST(pending_count-1,0), updated_at=NOW() WHERE id=$1`,
			meetID); err != nil {
			return "", err
		}
		return "withdrawn", tx.Commit(ctx)
	}

	// 再試「退出」：CAS joined → left，RowsAffected=0 表示本來就不在團裡（守住「只減一次」）
	tag, err = tx.Exec(ctx, `
		UPDATE run_meet_members SET status='left', decided_at=NOW(), decided_by=$2
		 WHERE meet_id=$1 AND user_id=$2 AND status='joined' AND role <> 'owner'`, meetID, uid)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", errNoSuchMember
	}
	if _, err = tx.Exec(ctx, `
		UPDATE run_meets SET member_count=GREATEST(member_count-1,0), updated_at=NOW() WHERE id=$1`,
		meetID); err != nil {
		return "", err
	}
	return "left", tx.Commit(ctx)
}

// Approve 發起人同意一筆申請（規格 1.5(c)：這就是「同時同意多人超過上限」的競態點）。
//
// 兩個並行的同意會在 lockMeet 排隊；第二個在鎖釋放後讀到已更新的 member_count → 回 409，不會超收。
func (r *Repository) Approve(ctx context.Context, ownerID, meetID, targetID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	m, err := lockMeet(ctx, tx, meetID)
	if err != nil {
		return err
	}
	if m.OwnerID != ownerID {
		return errNotOwner
	}
	if m.Status != StatusOpen {
		return meetStatusError(m.Status, false)
	}
	if m.MemberCount >= m.Capacity {
		return errApproveFull
	}

	// CAS 核銷這筆申請：RowsAffected=0 → 已被處理過，冪等回 409，絕不重複加名額
	tag, err := tx.Exec(ctx, `
		UPDATE run_meet_members
		   SET status='joined', joined_at=NOW(), decided_at=NOW(), decided_by=$3
		 WHERE meet_id=$1 AND user_id=$2 AND status='pending'`, meetID, targetID, ownerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errApplicationDone
	}
	if _, err = tx.Exec(ctx, `
		UPDATE run_meets SET member_count=member_count+1,
		                     pending_count=GREATEST(pending_count-1,0), updated_at=NOW()
		 WHERE id=$1`, meetID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Reject 發起人婉拒一筆申請。
func (r *Repository) Reject(ctx context.Context, ownerID, meetID, targetID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	m, err := lockMeet(ctx, tx, meetID)
	if err != nil {
		return err
	}
	if m.OwnerID != ownerID {
		return errNotOwner
	}
	tag, err := tx.Exec(ctx, `
		UPDATE run_meet_members
		   SET status='rejected', decided_at=NOW(), decided_by=$3
		 WHERE meet_id=$1 AND user_id=$2 AND status='pending'`, meetID, targetID, ownerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errApplicationDone
	}
	if _, err = tx.Exec(ctx, `
		UPDATE run_meets SET pending_count=GREATEST(pending_count-1,0), updated_at=NOW() WHERE id=$1`,
		meetID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Kick 發起人剔除成員（joined → kicked，被剔除者無法自行回鍋）。
// 對同一成員送兩次 kick：第 2 次 CAS 命中 0 列 → 409，member_count 只減一次。
func (r *Repository) Kick(ctx context.Context, ownerID, meetID, targetID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	m, err := lockMeet(ctx, tx, meetID)
	if err != nil {
		return err
	}
	if m.OwnerID != ownerID {
		return errNotOwner
	}
	if targetID == ownerID {
		return errNotOwner // 不能剔除自己
	}
	tag, err := tx.Exec(ctx, `
		UPDATE run_meet_members
		   SET status='kicked', decided_at=NOW(), decided_by=$3
		 WHERE meet_id=$1 AND user_id=$2 AND status='joined' AND role <> 'owner'`, meetID, targetID, ownerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNoSuchMember
	}
	if _, err = tx.Exec(ctx, `
		UPDATE run_meets SET member_count=GREATEST(member_count-1,0), updated_at=NOW() WHERE id=$1`,
		meetID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Unban 解除封鎖（刪掉 kicked 那列，對方即可重新加入）。
// 規格裁決 5：誤踢不該逼人重開團浪費配額，所以發起人可以解除。
func (r *Repository) Unban(ctx context.Context, ownerID, meetID, targetID string) error {
	var owner string
	if err := r.db.QueryRow(ctx, `SELECT owner_id FROM run_meets WHERE id=$1 AND deleted_at IS NULL`, meetID).
		Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		return err
	}
	if owner != ownerID {
		return errNotOwner
	}
	tag, err := r.db.Exec(ctx, `DELETE FROM run_meet_members WHERE meet_id=$1 AND user_id=$2 AND status='kicked'`,
		meetID, targetID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNoSuchMember
	}
	return nil
}

// ListMembers 成員名單／待審清單。
// ⚠️ 欄位白名單見 MemberView；一律 AND u.is_virtual = FALSE（虛擬選手不出現在任何名單）。
func (r *Repository) ListMembers(ctx context.Context, meetID, status string) ([]MemberView, error) {
	rows, err := r.db.Query(ctx, `
		SELECT mm.user_id, COALESCE(NULLIF(u.name,''), u.handle), COALESCE(u.avatar_url,''),
		       (mm.role = 'owner'), mm.status, mm.apply_note, mm.joined_at, mm.applied_at
		  FROM run_meet_members mm
		  JOIN users u ON u.id = mm.user_id AND u.is_virtual = FALSE
		 WHERE mm.meet_id=$1 AND mm.status=$2
		 ORDER BY (mm.role='owner') DESC, mm.applied_at ASC
		 LIMIT 500`, meetID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemberView{}
	for rows.Next() {
		var v MemberView
		var joinedAt *time.Time
		var appliedAt time.Time
		if err := rows.Scan(&v.UserID, &v.Name, &v.AvatarURL, &v.IsOwner, &v.Status, &v.ApplyNote,
			&joinedAt, &appliedAt); err != nil {
			return nil, err
		}
		if joinedAt != nil {
			s := joinedAt.Format(time.RFC3339)
			v.JoinedAt = &s
		}
		v.AppliedAt = appliedAt.Format(time.RFC3339)
		if v.Status != MemberPending {
			v.ApplyNote = "" // 附言只在待審清單有意義，加入後不再保留展示
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
