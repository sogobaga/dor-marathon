package partner

import "testing"

// TestApplyCtaGate 純函式驗證 CTA 三態：未登入→須登入（清連結）、VIP 不合格→鎖定（清連結）、其餘→原樣。
func TestApplyCtaGate(t *testing.T) {
	const minKm = 10
	cases := []struct {
		name      string
		audience  string
		ctaURL    string
		loggedIn  bool
		qualifies bool
		wantURL   string
		wantLogin bool
		wantLock  bool
	}{
		{"guest, all, has url → login required", "all", "https://x", false, false, "", true, false},
		{"guest, vip_featured, has url → login required (not vip lock)", "vip_featured", "https://x", false, false, "", true, false},
		{"guest, no url → nothing", "all", "", false, false, "", false, false},
		{"member, all → untouched", "all", "https://x", true, false, "https://x", false, false},
		{"member, vip_featured, unqualified → locked", "vip_featured", "https://x", true, false, "", false, true},
		{"member, vip_featured, qualified → untouched", "vip_featured", "https://x", true, true, "https://x", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 預先塞髒值，確認函式會把三個旗標都重設而不是只在符合條件時才寫。
			shop := &PartnerShop{Audience: c.audience, CTAURL: c.ctaURL, CtaLocked: true, CtaLockReason: "stale", CtaLoginRequired: true}
			applyCtaGate(shop, c.loggedIn, c.qualifies, minKm)
			if shop.CTAURL != c.wantURL {
				t.Errorf("CTAURL = %q, want %q", shop.CTAURL, c.wantURL)
			}
			if shop.CtaLoginRequired != c.wantLogin {
				t.Errorf("CtaLoginRequired = %v, want %v", shop.CtaLoginRequired, c.wantLogin)
			}
			if shop.CtaLocked != c.wantLock {
				t.Errorf("CtaLocked = %v, want %v", shop.CtaLocked, c.wantLock)
			}
			if c.wantLock && shop.CtaLockReason != ctaLockReason(minKm) {
				t.Errorf("CtaLockReason = %q, want %q", shop.CtaLockReason, ctaLockReason(minKm))
			}
			if !c.wantLock && shop.CtaLockReason != "" {
				t.Errorf("CtaLockReason = %q, want empty", shop.CtaLockReason)
			}
		})
	}
}
