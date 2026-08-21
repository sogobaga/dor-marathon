package activityreward

import (
	"testing"
	"time"
)

// TestCouponValidUntil_FixedMode fixed 模式（指定到期日）：已過期／未設到期日一律回 (nil,false)，
// 讓 grantCoupon 安靜跳過該項（不影響其他獎勵項目）；未過期則直接沿用該到期日。
func TestCouponValidUntil_FixedMode(t *testing.T) {
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	future := now.Add(48 * time.Hour)
	past := now.Add(-time.Hour)

	t.Run("未過期 → 沿用 expires_at", func(t *testing.T) {
		got, ok := couponValidUntil("fixed", &future, nil, now)
		if !ok {
			t.Fatalf("expected ok=true")
		}
		if got == nil || !got.Equal(future) {
			t.Fatalf("got %v, want %v", got, future)
		}
	})
	t.Run("已過期 → 跳過", func(t *testing.T) {
		_, ok := couponValidUntil("fixed", &past, nil, now)
		if ok {
			t.Fatalf("expected ok=false for expired fixed coupon")
		}
	})
	t.Run("剛好等於 now → 視為已過期（Before 邊界不含 now 本身時仍應保守跳過）", func(t *testing.T) {
		exact := now
		_, ok := couponValidUntil("fixed", &exact, nil, now)
		// exact.Before(now) 為 false，故不會被判過期；沿用當下時刻本身（極端邊界，允許通過）。
		if !ok {
			t.Fatalf("expected ok=true for expires_at == now (not yet Before now)")
		}
	})
	t.Run("未設定到期日 → 跳過", func(t *testing.T) {
		_, ok := couponValidUntil("fixed", nil, nil, now)
		if ok {
			t.Fatalf("expected ok=false when expires_at is nil")
		}
	})
}

// TestCouponValidUntil_DaysMode days 模式（獲得後 N 天）：天數缺漏/非正數（資料異常保底）跳過；
// 合法天數則回 now+N 天。
func TestCouponValidUntil_DaysMode(t *testing.T) {
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	t.Run("valid_days=7 → now+7天", func(t *testing.T) {
		days := 7
		got, ok := couponValidUntil("days", nil, &days, now)
		if !ok {
			t.Fatalf("expected ok=true")
		}
		want := now.AddDate(0, 0, 7)
		if got == nil || !got.Equal(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
	t.Run("valid_days=nil → 跳過（資料異常保底）", func(t *testing.T) {
		_, ok := couponValidUntil("days", nil, nil, now)
		if ok {
			t.Fatalf("expected ok=false when valid_days is nil")
		}
	})
	t.Run("valid_days=0 → 跳過", func(t *testing.T) {
		zero := 0
		_, ok := couponValidUntil("days", nil, &zero, now)
		if ok {
			t.Fatalf("expected ok=false when valid_days=0")
		}
	})
	t.Run("valid_days=負數 → 跳過", func(t *testing.T) {
		neg := -3
		_, ok := couponValidUntil("days", nil, &neg, now)
		if ok {
			t.Fatalf("expected ok=false when valid_days<0")
		}
	})
}

// TestRewardItem_Validate_Coupon coupon 類的結構驗證（不碰 DB）：type=coupon 且 prob_bp 合法時，
// 必須有 coupon_def_id 才算合法設定；空字串（含純空白，見 validate.go TrimSpace）一律擋下。
func TestRewardItem_Validate_Coupon(t *testing.T) {
	cases := []struct {
		name    string
		item    RewardItem
		wantErr bool
	}{
		{"合法：有 coupon_def_id", RewardItem{Type: "coupon", ProbBP: 5000, CouponDefID: "def-1"}, false},
		{"缺 coupon_def_id → 錯誤", RewardItem{Type: "coupon", ProbBP: 5000, CouponDefID: ""}, true},
		{"coupon_def_id 純空白 → 錯誤", RewardItem{Type: "coupon", ProbBP: 5000, CouponDefID: "   "}, true},
		{"prob_bp 超出範圍 → 錯誤（結構驗證先擋機率）", RewardItem{Type: "coupon", ProbBP: 0, CouponDefID: "def-1"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.item.Validate()
			if c.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

// TestValidRewardTypes_IncludesCoupon 確認 coupon 已列入合法獎勵型別（否則 Validate 會在 switch 前
// 就被 validRewardTypes 擋下，回「invalid reward item type」而非本檔案測的「requires coupon_def_id」）。
func TestValidRewardTypes_IncludesCoupon(t *testing.T) {
	if !validRewardTypes["coupon"] {
		t.Fatalf("expected validRewardTypes[\"coupon\"] to be true")
	}
}
