package profile

import (
	"net/http"
	"time"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/auth"
)

// planPrice 單一方案的定價（元）。Price=折後、Save=現省、Promo=是否套用折扣。
type planPrice struct {
	Original int  `json:"original"`
	Price    int  `json:"price"`
	Save     int  `json:"save"`
	Promo    bool `json:"promo"`
}

type vipPricing struct {
	Monthly       planPrice  `json:"monthly"`
	Annual        planPrice  `json:"annual"`
	InPromoWindow bool       `json:"in_promo_window"` // 目前是否有任何促銷生效
	PromoEndsAt   *time.Time `json:"promo_ends_at,omitempty"`
	TrialDays     int        `json:"trial_days"`
	IsVIP         bool       `json:"is_vip"`
	VipPlan       string     `json:"vip_plan"`
	VIPExpiresAt  *time.Time `json:"vip_expires_at,omitempty"`
}

// VipPricing GET /profile/vip/pricing — 依此使用者的促銷資格計算月/年方案定價。
// 首購促銷窗：仍為 trial 且 now <= 到期 + first_promo_days；此外套用後台 vip_promos 生效檔期（取更優）。
func (h *Handler) VipPricing(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	ctx := r.Context()
	mp := appsettings.GetInt(ctx, h.db, "vip_price_monthly", 399)
	ap := appsettings.GetInt(ctx, h.db, "vip_price_annual", 4788)
	mpct := appsettings.GetInt(ctx, h.db, "vip_first_promo_monthly_pct", 70)
	apct := appsettings.GetInt(ctx, h.db, "vip_first_promo_annual_pct", 55)
	trialDays := appsettings.GetInt(ctx, h.db, "vip_trial_days", 14)
	promoDays := appsettings.GetInt(ctx, h.db, "vip_first_promo_days", 14)

	var vipPlan string
	var vipExp *time.Time
	_ = h.db.QueryRow(ctx, `SELECT COALESCE(vip_plan,''), vip_expires_at FROM users WHERE id=$1`, uid).Scan(&vipPlan, &vipExp)
	now := time.Now()
	isVIP := vipExp != nil && vipExp.After(now)

	// 首購促銷窗：仍是 trial 且 現在 <= 到期 + promoDays
	mEffPct, aEffPct := 100, 100
	var promoEnds *time.Time
	if vipPlan == "trial" && vipExp != nil {
		end := vipExp.Add(time.Duration(promoDays) * 24 * time.Hour)
		if !now.After(end) {
			mEffPct, aEffPct = mpct, apct
			promoEnds = &end
		}
	}
	// 後台其他促銷檔期（active 且在期間內）→ 取更優折扣（pay_pct 更低）
	if rows, err := h.db.Query(ctx, `
		SELECT plan, pay_pct FROM vip_promos
		WHERE active AND (starts_at IS NULL OR starts_at<=NOW()) AND (ends_at IS NULL OR ends_at>=NOW())`); err == nil {
		for rows.Next() {
			var pl string
			var pct int
			if rows.Scan(&pl, &pct) == nil && pct >= 1 && pct <= 100 {
				if (pl == "monthly" || pl == "both") && pct < mEffPct {
					mEffPct = pct
				}
				if (pl == "annual" || pl == "both") && pct < aEffPct {
					aEffPct = pct
				}
			}
		}
		rows.Close()
	}

	mPrice := (mp*mEffPct + 50) / 100 // 四捨五入
	aPrice := (ap*aEffPct + 50) / 100
	respondJSON(w, http.StatusOK, vipPricing{
		Monthly:       planPrice{Original: mp, Price: mPrice, Save: mp - mPrice, Promo: mEffPct < 100},
		Annual:        planPrice{Original: ap, Price: aPrice, Save: ap - aPrice, Promo: aEffPct < 100},
		InPromoWindow: mEffPct < 100 || aEffPct < 100,
		PromoEndsAt:   promoEnds,
		TrialDays:     trialDays,
		IsVIP:         isVIP,
		VipPlan:       vipPlan,
		VIPExpiresAt:  vipExp,
	})
}

// MarkTrialNoticeShown POST /profile/trial-notice-shown — 標記試用到期彈窗已顯示（只跳一次）。
func (h *Handler) MarkTrialNoticeShown(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	_, _ = h.db.Exec(r.Context(), `UPDATE users SET trial_notice_shown=TRUE WHERE id=$1`, uid)
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}
