package profile

import "testing"

// TestResolveSkinSelectEntryState 涵蓋契約 retro_skin/CONTRACT.md §2 的規則：缺鍵/未知值一律
// hidden、open 全開、whitelist 命中/未命中、vip（VIP 有效期內／過期）、且刻意驗證「不接受
// isSuperAdmin 參數」這件事本身（函式簽章沒有該參數，所以不可能被誤接上旁路——這裡改用行為驗證：
// 即使白名單刻意留空，也不會因為某種隱藏旁路而放行）。
func TestResolveSkinSelectEntryState(t *testing.T) {
	const wl = "sogobaga@gmail.com"

	cases := []struct {
		name      string
		state     string
		whitelist string
		email     string
		code      string
		isVIP     bool
		want      string
	}{
		{"缺鍵(空字串state)視為hidden", "", wl, "someone@example.com", "", false, "hidden"},
		{"未知值視為hidden", "weird", wl, "someone@example.com", "", false, "hidden"},
		{"off非本入口合法值,視為hidden", "off", wl, "sogobaga@gmail.com", "", false, "hidden"},
		{"locked非本入口合法值,視為hidden", "locked", wl, "sogobaga@gmail.com", "", false, "hidden"},
		{"open全開(非白名單也shown)", "open", wl, "someone@example.com", "", false, "shown"},
		{"whitelist命中email", "whitelist", wl, "sogobaga@gmail.com", "", false, "shown"},
		{"whitelist命中email大小寫不敏感", "whitelist", wl, "SoGoBaga@Gmail.com", "", false, "shown"},
		{"whitelist未命中email", "whitelist", wl, "other@example.com", "", false, "hidden"},
		{"whitelist命中帳號編碼", "whitelist", "ABCD1234", "other@example.com", "ABCD1234", false, "shown"},
		{"whitelist白名單為空一律hidden", "whitelist", "", "sogobaga@gmail.com", "", false, "hidden"},
		{"vip且VIP有效期內shown(非白名單)", "vip", wl, "other@example.com", "", true, "shown"},
		{"vip但VIP已過期且非白名單hidden", "vip", wl, "other@example.com", "", false, "hidden"},
		{"vip狀態下白名單命中即使非VIP也shown", "vip", wl, "sogobaga@gmail.com", "", false, "shown"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveSkinSelectEntryState(c.state, c.whitelist, c.email, c.code, c.isVIP)
			if got != c.want {
				t.Errorf("resolveSkinSelectEntryState(%q,%q,%q,%q,%v) = %q, want %q",
					c.state, c.whitelist, c.email, c.code, c.isVIP, got, c.want)
			}
		})
	}
}
