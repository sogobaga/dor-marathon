package profile

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

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

// CancelVipSub POST /profile/vip/cancel — 取消訂閱：不再續扣，VIP 權益維持至到期日。
// 目前標記 vip_subscriptions 為 cancelled；P4 綠界定期定額上線後，這裡同時呼叫綠界終止定期定額授權。
func (h *Handler) CancelVipSub(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	ctx := r.Context()
	_, _ = h.db.Exec(ctx, `UPDATE vip_subscriptions SET status='cancelled', updated_at=NOW() WHERE user_id=$1 AND status='active'`, uid)
	var exp *time.Time
	_ = h.db.QueryRow(ctx, `SELECT vip_expires_at FROM users WHERE id=$1`, uid).Scan(&exp)
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "vip_expires_at": exp})
}

// --- 後台：訂閱優惠管理（vip_promos） ---

// VipPromo 訂閱優惠檔期。pay_pct=實付%（70=付七成、即打七折）。
type VipPromo struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Plan      string     `json:"plan"` // monthly | annual | both
	PayPct    int        `json:"pay_pct"`
	StartsAt  *time.Time `json:"starts_at,omitempty"`
	EndsAt    *time.Time `json:"ends_at,omitempty"`
	Active    bool       `json:"active"`
	CreatedAt time.Time  `json:"created_at"`
}

// VipPromoAdminRouter 掛 /admin/vip-promos（需 settings 權限）
func (h *Handler) VipPromoAdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListPromos)
	r.Post("/", h.AdminSavePromo)
	r.Post("/{id}/delete", h.AdminDeletePromo)
	return r
}

func (h *Handler) AdminListPromos(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `
		SELECT id, name, COALESCE(plan,'both'), pay_pct, starts_at, ends_at, active, created_at
		FROM vip_promos ORDER BY COALESCE(starts_at, created_at) DESC, created_at DESC`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	list := []VipPromo{}
	for rows.Next() {
		var p VipPromo
		if rows.Scan(&p.ID, &p.Name, &p.Plan, &p.PayPct, &p.StartsAt, &p.EndsAt, &p.Active, &p.CreatedAt) == nil {
			list = append(list, p)
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{"promos": list})
}

func (h *Handler) AdminSavePromo(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Plan     string `json:"plan"`
		PayPct   int    `json:"pay_pct"`
		StartsAt string `json:"starts_at"` // ISO 或 ''
		EndsAt   string `json:"ends_at"`
		Active   bool   `json:"active"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		respondErr(w, http.StatusBadRequest, "請輸入優惠名稱")
		return
	}
	if b.Plan != "monthly" && b.Plan != "annual" && b.Plan != "both" {
		b.Plan = "both"
	}
	if b.PayPct < 1 || b.PayPct > 100 {
		respondErr(w, http.StatusBadRequest, "實付%需介於 1–100")
		return
	}
	ctx := r.Context()
	var id string
	if b.ID == "" {
		err := h.db.QueryRow(ctx, `
			INSERT INTO vip_promos (name, plan, pay_pct, starts_at, ends_at, active)
			VALUES ($1,$2,$3,NULLIF($4,'')::timestamptz,NULLIF($5,'')::timestamptz,$6)
			RETURNING id`, b.Name, b.Plan, b.PayPct, b.StartsAt, b.EndsAt, b.Active).Scan(&id)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
	} else {
		id = b.ID
		if _, err := h.db.Exec(ctx, `
			UPDATE vip_promos SET name=$2, plan=$3, pay_pct=$4,
				starts_at=NULLIF($5,'')::timestamptz, ends_at=NULLIF($6,'')::timestamptz, active=$7
			WHERE id=$1`, id, b.Name, b.Plan, b.PayPct, b.StartsAt, b.EndsAt, b.Active); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (h *Handler) AdminDeletePromo(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.db.Exec(r.Context(), `DELETE FROM vip_promos WHERE id=$1`, id); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}
