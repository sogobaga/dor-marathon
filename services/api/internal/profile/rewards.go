// 活動獎勵系統 P3：玩家活動獎勵錢包。只回/只改登入者自己的 user_rewards（見 migration 127；只存序號類
// 獎勵，經濟類 EXP/DP/GP/VIP 中獎直接入帳不進此表，見 internal/activityreward）。排序（近到期置頂＋外框、
// 其餘新到舊）交給前端依全量資料自行分組排序，這裡固定用 obtained_at DESC 回。
package profile

import (
	"errors"
	"net/http"
	"time"

	"github.com/dor/api/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// UserReward 玩家活動獎勵錢包單筆（欄位見 activityreward.grantSerial denormalize 寫入 user_rewards 時的顯示欄位）。
// Kind 區分序號類('serial'，migration 127 既有)／活動優惠券類('coupon'，migration 138 新增)：coupon 類
// 只有 CouponDefID/AmountCents 有意義，Code/Link/MerchantName 等序號類欄位皆為空。
type UserReward struct {
	ID           string     `json:"id"`
	SourceType   string     `json:"source_type"`
	SourceRaceID string     `json:"source_race_id,omitempty"`
	SourceRegID  string     `json:"source_reg_id,omitempty"`
	Kind         string     `json:"kind"` // serial | coupon
	Code         string     `json:"code"`
	Link         string     `json:"link,omitempty"`
	ItemLabel    string     `json:"item_label"`
	MerchantName string     `json:"merchant_name,omitempty"`
	UsageNote    string     `json:"usage_note,omitempty"`
	IconURL      string     `json:"icon_url,omitempty"`
	Description  string     `json:"description,omitempty"`
	CouponDefID  string     `json:"coupon_def_id,omitempty"` // kind=coupon 專用
	AmountCents  *int       `json:"amount_cents,omitempty"`  // kind=coupon 專用：面額
	// 組合包（migration 149，activityreward.grantSerialBundle）：同一次組合包發放的多張序號共用同一
	// BundleID；前端 group by bundle_id 併成一張卡（BundleLabel 顯示名如「LINE POINTS 3500」、
	// BundleTotal 為組合總面額）。非組合包（既有兩層抽獎單張序號獎勵）三欄皆為空，維持一列一卡不受影響。
	BundleID     string     `json:"bundle_id,omitempty"`
	BundleLabel  string     `json:"bundle_label,omitempty"`
	BundleTotal  *int       `json:"bundle_total,omitempty"`
	ValidFrom    *time.Time `json:"valid_from,omitempty"`
	ValidUntil   *time.Time `json:"valid_until,omitempty"`
	Used         bool       `json:"used"`
	UsedAt       *time.Time `json:"used_at,omitempty"`
	ObtainedAt   time.Time  `json:"obtained_at"`
}

// GET /api/v1/profile/rewards — 玩家活動獎勵錢包：只回登入者自己的序號類/活動優惠券類獎勵。序號本身
// 視同帳號機敏資訊（比照 account-code-privacy 通則），僅本人可見，故一律 WHERE user_id=$me、不提供查他人的路徑。
func (h *Handler) Rewards(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	rows, err := h.db.Query(r.Context(), `
		SELECT id, source_type, COALESCE(source_race_id::text,''), COALESCE(source_reg_id::text,''), kind,
		       COALESCE(code,''), COALESCE(link,''), COALESCE(item_label,''), COALESCE(merchant_name,''),
		       COALESCE(usage_note,''), COALESCE(icon_url,''), COALESCE(description,''),
		       COALESCE(coupon_def_id::text,''), amount_cents, valid_from, valid_until,
		       used, used_at, obtained_at,
		       COALESCE(bundle_id::text,''), COALESCE(bundle_label,''), bundle_total
		FROM user_rewards
		WHERE user_id=$1
		ORDER BY obtained_at DESC`, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	out := []UserReward{}
	for rows.Next() {
		var rr UserReward
		if err := rows.Scan(&rr.ID, &rr.SourceType, &rr.SourceRaceID, &rr.SourceRegID, &rr.Kind,
			&rr.Code, &rr.Link, &rr.ItemLabel, &rr.MerchantName, &rr.UsageNote, &rr.IconURL, &rr.Description,
			&rr.CouponDefID, &rr.AmountCents, &rr.ValidFrom, &rr.ValidUntil, &rr.Used, &rr.UsedAt, &rr.ObtainedAt,
			&rr.BundleID, &rr.BundleLabel, &rr.BundleTotal); err != nil {
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, rr)
	}
	if err := rows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "scan failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"rewards": out})
}

// POST /api/v1/profile/rewards/{id}/use — 標記某筆活動獎勵已使用。是否兌換發生在商家端（線下/序號網站），
// 系統無法自動偵測，故信任玩家自行回報（比照多數序號/兌換碼系統的作法），非系統判定。
// 只能改自己的（WHERE user_id=$me）；冪等（used=FALSE 才會真的更新，重複呼叫不報錯，回目前狀態）。
func (h *Handler) MarkRewardUsed(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	id := chi.URLParam(r, "id")

	// 先查現況：分辨「已使用」（冪等成功回現況，不受開始時間擋）vs「尚未開始」（400）vs「可以標記」，
	// 才不會讓「序號組事後被改成有開始時間」誤傷已標記過已使用的舊資料。
	var validFrom *time.Time
	var usedNow bool
	if err := h.db.QueryRow(r.Context(),
		`SELECT valid_from, used FROM user_rewards WHERE id=$1 AND user_id=$2`, id, uid).Scan(&validFrom, &usedNow); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondErr(w, http.StatusNotFound, "reward not found")
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if !usedNow && validFrom != nil && time.Now().Before(*validFrom) {
		respondErr(w, http.StatusBadRequest, "尚未開始，尚不可標記已使用")
		return
	}

	var used bool
	var usedAt *time.Time
	err := h.db.QueryRow(r.Context(), `
		UPDATE user_rewards SET used=TRUE, used_at=NOW()
		WHERE id=$1 AND user_id=$2 AND used=FALSE
		RETURNING used, used_at`, id, uid).Scan(&used, &usedAt)
	if err == nil {
		respondJSON(w, http.StatusOK, map[string]any{"ok": true, "used": used, "used_at": usedAt})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	// UPDATE 0 筆被改到：可能「本來就已使用過」（冪等、非錯誤）或「不存在/非本人」，查現況分辨兩者。
	err = h.db.QueryRow(r.Context(),
		`SELECT used, used_at FROM user_rewards WHERE id=$1 AND user_id=$2`, id, uid).Scan(&used, &usedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusNotFound, "reward not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "used": used, "used_at": usedAt})
}
