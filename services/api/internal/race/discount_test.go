package race

import "testing"

// TestDiscountMethodCount_MutexMatrix 報名折抵三擇一（VIP活動優惠券／活動優惠券／優惠序號，migration 138
// 加入活動優惠券後一般化為三方互斥）：覆蓋全部單一/兩兩組合/三者同帶情形，只有 count>1 時 Register()
// 才會回 ErrDiscountConflict（見 service.go 呼叫端）。
func TestDiscountMethodCount_MutexMatrix(t *testing.T) {
	cases := []struct {
		name           string
		useCoupon      bool
		promoCode      string
		couponRewardID string
		want           int
	}{
		{"三者皆未帶 → 0（合法，不折抵）", false, "", "", 0},
		{"只帶 VIP 券", true, "", "", 1},
		{"只帶優惠序號", false, "PROMO1", "", 1},
		{"只帶活動優惠券", false, "", "reward-1", 1},
		{"VIP 券 + 優惠序號 → 2（應擋）", true, "PROMO1", "", 2},
		{"VIP 券 + 活動優惠券 → 2（應擋）", true, "", "reward-1", 2},
		{"優惠序號 + 活動優惠券 → 2（應擋）", false, "PROMO1", "reward-1", 2},
		{"三者同帶 → 3（應擋）", true, "PROMO1", "reward-1", 3},
		{"優惠序號前後空白 trim 後仍算有帶", false, "  PROMO1  ", "", 1},
		{"活動優惠券前後空白 trim 後仍算有帶", false, "", "  reward-1  ", 1},
		{"優惠序號純空白視為未帶", false, "   ", "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := discountMethodCount(c.useCoupon, c.promoCode, c.couponRewardID)
			if got != c.want {
				t.Fatalf("discountMethodCount(%v,%q,%q) = %d, want %d", c.useCoupon, c.promoCode, c.couponRewardID, got, c.want)
			}
		})
	}
}

// TestShouldRedeemCouponReward 交易層最後防線（RegisterWithOrder 步驟 2d 的資格判斷）：
// 需指定 CouponRewardID、與 VIP 券/優惠序號互斥、且報名費 >0（報名費 0 的免費賽事不消耗券）。
func TestShouldRedeemCouponReward(t *testing.T) {
	cases := []struct {
		name           string
		couponRewardID string
		promoCode      string
		useCoupon      bool
		entryFee       int
		want           bool
	}{
		{"正常情形：只帶活動優惠券 + 報名費>0 → 應嘗試核銷", "reward-1", "", false, 10000, true},
		{"報名費為 0 → 不應消耗券（比照 VIP 券）", "reward-1", "", false, 0, false},
		{"同時帶優惠序號 → 不應核銷（互斥）", "reward-1", "PROMO1", false, 10000, false},
		{"同時開 VIP 券 → 不應核銷（互斥）", "reward-1", "", true, 10000, false},
		{"未指定 CouponRewardID → 不應核銷", "", "", false, 10000, false},
		{"CouponRewardID 純空白 → 視為未指定", "   ", "", false, 10000, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shouldRedeemCouponReward(c.couponRewardID, c.promoCode, c.useCoupon, c.entryFee)
			if got != c.want {
				t.Fatalf("shouldRedeemCouponReward(%q,%q,%v,%d) = %v, want %v",
					c.couponRewardID, c.promoCode, c.useCoupon, c.entryFee, got, c.want)
			}
		})
	}
}

// TestCouponRewardDiscountCents 折抵金額＝券面額，但不超過報名費本身（避免折抵出負的應付金額；
// 真正的 floor 0 由呼叫端 payable=max(0,entryFee-discount) 保證，這裡只驗證「不超額折抵」這一段）。
func TestCouponRewardDiscountCents(t *testing.T) {
	cases := []struct {
		name        string
		amountCents int
		entryFee    int
		want        int
	}{
		{"面額 < 報名費 → 全額折抵", 10000, 20000, 10000},
		{"面額 = 報名費 → 全額折抵", 15000, 15000, 15000},
		{"面額 > 報名費 → 折抵封頂在報名費", 20000, 15000, 15000},
		{"報名費為 0 → 封頂為 0（呼叫端理論上不會在報名費 0 時走到這裡，但函式本身仍需正確）", 10000, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := couponRewardDiscountCents(c.amountCents, c.entryFee)
			if got != c.want {
				t.Fatalf("couponRewardDiscountCents(%d,%d) = %d, want %d", c.amountCents, c.entryFee, got, c.want)
			}
		})
	}
}
