package vip

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/appsettings"
)

// PlanPrice 單一方案的定價（NTD 元）。Price=折後、Save=現省、Promo=是否套用折扣。
// 欄位形狀刻意與 profile.planPrice 完全一致（名稱/型別/順序皆同，只差 struct tag）——
// 呼叫端可用一般的型別轉換（如 profilePlanPriceType(q.Monthly)）互轉，不需逐欄搬移。
type PlanPrice struct {
	Original int
	Price    int
	Save     int
	Promo    bool
}

// Quote 目前生效的月/年方案定價快照，供「定價頁面」與「訂閱發起時算首期價」共用同一套計算結果——
// 訂閱發起（POST /profile/vip/subscribe）算出的首期價，必須恰好等於玩家在定價頁面看到的數字，
// 兩處各自維護同一段邏輯很容易走鐘（drift），故抽成本函式讓兩處呼叫同一份實作。
type Quote struct {
	Monthly       PlanPrice
	Annual        PlanPrice
	InPromoWindow bool // 目前是否有任何促銷生效
	PromoEndsAt   *time.Time
	TrialDays     int
	IsVIP         bool
	VipPlan       string
	VIPExpiresAt  *time.Time
	// VIP 權益文案用（v0.1.566：升級彈窗的「每月 N 張 X 元活動優惠券」須跟著系統設定變動，
	// 不可寫死——與補券/扣抵端同源 app_settings vip_coupon_*，見 v0.1.540）
	CouponValueNTD int // 活動優惠券面額（元）
	CouponPerMonth int // 每月補發張數
}

// ComputeQuote 依此使用者的促銷資格計算月/年方案定價（NTD 元，非分）。
// 首購促銷窗：仍為 trial 且 now <= 到期 + first_promo_days；此外套用後台 vip_promos 生效檔期
// （取更優，即 pay_pct 更低者）。與原本 profile.VipPricing 內聯的算法完全相同，只是搬出來共用。
func ComputeQuote(ctx context.Context, db *pgxpool.Pool, userID string) (Quote, error) {
	mp := appsettings.GetInt(ctx, db, "vip_price_monthly", 399)
	ap := appsettings.GetInt(ctx, db, "vip_price_annual", 4788)
	mpct := appsettings.GetInt(ctx, db, "vip_first_promo_monthly_pct", 70)
	apct := appsettings.GetInt(ctx, db, "vip_first_promo_annual_pct", 55)
	trialDays := appsettings.GetInt(ctx, db, "vip_trial_days", 14)
	promoDays := appsettings.GetInt(ctx, db, "vip_first_promo_days", 14)

	var vipPlan string
	var vipExp *time.Time
	if err := db.QueryRow(ctx, `SELECT COALESCE(vip_plan,''), vip_expires_at FROM users WHERE id=$1`, userID).
		Scan(&vipPlan, &vipExp); err != nil {
		return Quote{}, err
	}
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
	// 後台其他促銷檔期（active 且在期間內）→ 取更優折扣（pay_pct 更低）。
	// PromoEndsAt 修正（v0.1.567）：舊版只有首購窗會寫 promoEnds，檔期促銷生效時前端顯示的
	// 「X 後恢復原價」日期缺失或殘留首購窗日期（使用者回報顯示 9/1、實際檔期到 12/31）——
	// 檔期被「採用」（提供了目前最優折扣）時，一併記下其 ends_at，最後取「最早的恢復時點」顯示。
	if rows, err := db.Query(ctx, `
		SELECT plan, pay_pct, ends_at FROM vip_promos
		WHERE active AND (starts_at IS NULL OR starts_at<=NOW()) AND (ends_at IS NULL OR ends_at>=NOW())`); err == nil {
		var mEnds, aEnds *time.Time
		for rows.Next() {
			var pl string
			var pct int
			var ends *time.Time
			if rows.Scan(&pl, &pct, &ends) == nil && pct >= 1 && pct <= 100 {
				if (pl == "monthly" || pl == "both") && pct < mEffPct {
					mEffPct, mEnds = pct, ends
				}
				if (pl == "annual" || pl == "both") && pct < aEffPct {
					aEffPct, aEnds = pct, ends
				}
			}
		}
		rows.Close()
		// 檔期促銷被採用時更新促銷結束時點：取「最早結束」者（該時點後開始有方案恢復原價，
		// 對玩家的「把握期限」語意最誠實）；與首購窗並存時同樣取較早者。
		for _, e := range []*time.Time{mEnds, aEnds} {
			if e != nil && (promoEnds == nil || e.Before(*promoEnds)) {
				promoEnds = e
			}
		}
	}

	mPrice := (mp*mEffPct + 50) / 100 // 四捨五入
	aPrice := (ap*aEffPct + 50) / 100

	return Quote{
		Monthly:       PlanPrice{Original: mp, Price: mPrice, Save: mp - mPrice, Promo: mEffPct < 100},
		Annual:        PlanPrice{Original: ap, Price: aPrice, Save: ap - aPrice, Promo: aEffPct < 100},
		InPromoWindow: mEffPct < 100 || aEffPct < 100,
		PromoEndsAt:   promoEnds,
		TrialDays:     trialDays,
		IsVIP:         isVIP,
		VipPlan:       vipPlan,
		VIPExpiresAt:  vipExp,
		CouponValueNTD: appsettings.GetInt(ctx, db, "vip_coupon_value_cents", 10000) / 100,
		CouponPerMonth: appsettings.GetInt(ctx, db, "vip_coupon_per_month", 3),
	}, nil
}

// PriceCentsForPlan 回傳指定方案（monthly|annual）目前折後價格的「分」（NTD*100，與 orders/
// payment_transactions/vip_subscriptions 的 amount_cents 欄位單位一致）。plan 非 monthly/annual
// 回傳 0——呼叫端應在此之前已驗證過 plan 合法性（見 race.ErrInvalidVipPlan 的驗證時機）。
func (q Quote) PriceCentsForPlan(plan string) int {
	switch plan {
	case "monthly":
		return q.Monthly.Price * 100
	case "annual":
		return q.Annual.Price * 100
	default:
		return 0
	}
}
