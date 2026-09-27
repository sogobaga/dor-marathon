package profile

import "testing"

// TestResolveScifiEntryState 涵蓋契約 §2 的規則：缺鍵/未知值/off/locked 一律 hidden、open 全開、
// whitelist 命中/未命中、且刻意驗證「不接受 isSuperAdmin 參數」這件事本身（函式簽章沒有該參數，
// 所以不可能被誤接上旁路——這裡改用行為驗證：即使白名單刻意留空，也不會因為某種隱藏旁路而放行）。
func TestResolveScifiEntryState(t *testing.T) {
	const wl = "sogobaga@gmail.com"

	cases := []struct {
		name      string
		state     string
		whitelist string
		email     string
		code      string
		want      string
	}{
		{"缺鍵(空字串state)視為hidden", "", wl, "someone@example.com", "", "hidden"},
		{"未知值視為hidden", "weird", wl, "someone@example.com", "", "hidden"},
		{"off視為hidden(不放行任何人)", "off", wl, "sogobaga@gmail.com", "", "hidden"},
		{"locked視為hidden(此入口無locked語意)", "locked", wl, "sogobaga@gmail.com", "", "hidden"},
		{"open全開(非白名單也shown)", "open", wl, "someone@example.com", "", "shown"},
		{"whitelist命中email", "whitelist", wl, "sogobaga@gmail.com", "", "shown"},
		{"whitelist命中email大小寫不敏感", "whitelist", wl, "SoGoBaga@Gmail.com", "", "shown"},
		{"whitelist未命中email", "whitelist", wl, "other@example.com", "", "hidden"},
		{"whitelist命中帳號編碼", "whitelist", "ABCD1234", "other@example.com", "ABCD1234", "shown"},
		{"whitelist白名單為空一律hidden", "whitelist", "", "sogobaga@gmail.com", "", "hidden"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveScifiEntryState(c.state, c.whitelist, c.email, c.code)
			if got != c.want {
				t.Errorf("resolveScifiEntryState(%q,%q,%q,%q) = %q, want %q",
					c.state, c.whitelist, c.email, c.code, got, c.want)
			}
		})
	}
}
