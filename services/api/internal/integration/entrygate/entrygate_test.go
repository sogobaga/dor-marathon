package entrygate

// Resolve 真值表（state × 超管 × 白名單）與白名單格式；Load／Emergency 的資料庫路徑見
// entrygate_integration_test.go（build tag integration）。

import (
	"fmt"
	"testing"
)

func TestResolve_TruthTable(t *testing.T) {
	const wl = "Owner@Example.com, ABCD2345\nthird@example.com;#Zxy98765\tfifth@example.com"
	type user struct {
		name         string
		email, code  string
		isSuper      bool
		inWhitelist  bool
		wantWhitelst string // state=whitelist 的預期
	}
	users := []user{
		{"normal member (not listed)", "nobody@example.com", "NOPE1234", false, false, Hidden},
		{"listed by email", "owner@example.com", "", false, true, Shown},
		{"listed by email, case-insensitive", "OWNER@EXAMPLE.COM", "", false, true, Shown},
		{"listed by account code", "x@example.com", "abcd2345", false, true, Shown},
		{"listed by account code with # in list", "x@example.com", "#zxy98765", false, true, Shown},
		{"listed by second separator style", "third@example.com", "", false, true, Shown},
		{"super admin not listed", "admin@example.com", "ADMIN123", true, false, Shown},
		{"super admin listed", "owner@example.com", "", true, true, Shown},
	}
	states := []struct {
		state string
		want  func(u user) string
	}{
		{"hidden", func(u user) string { return Hidden }}, // 緊急關閉：連超管也 hidden
		{"open", func(u user) string { return Shown }},
		{"whitelist", func(u user) string { return u.wantWhitelst }},
		{"", func(u user) string { return u.wantWhitelst }},           // 缺鍵＝whitelist
		{"vip", func(u user) string { return u.wantWhitelst }},        // 未知值＝whitelist
		{"  OPEN  ", func(u user) string { return Shown }},            // 去空白、不分大小寫
		{"Hidden", func(u user) string { return Hidden }},             //
		{"locked", func(u user) string { return u.wantWhitelst }},     // 舊式狀態值：未知＝whitelist（不是 hidden）
		{"off", func(u user) string { return u.wantWhitelst }},        //
		{"whitelist ", func(u user) string { return u.wantWhitelst }}, //
	}
	for _, st := range states {
		for _, u := range users {
			name := fmt.Sprintf("state=%q/%s", st.state, u.name)
			t.Run(name, func(t *testing.T) {
				if got, want := Resolve(st.state, wl, u.email, u.code, u.isSuper), st.want(u); got != want {
					t.Fatalf("Resolve(%q, wl, %q, %q, super=%v) = %q, want %q", st.state, u.email, u.code, u.isSuper, got, want)
				}
			})
		}
	}
}

func TestResolve_EmptyWhitelistAndEmptyIdentity(t *testing.T) {
	// 空白名單：只有超管能過；不會因為 email／code 都是空字串而「空比空」命中。
	if got := Resolve("whitelist", "", "a@b.c", "CODE1234", false); got != Hidden {
		t.Fatalf("empty whitelist must hide normal members, got %q", got)
	}
	if got := Resolve("whitelist", "a@b.c", "", "", false); got != Hidden {
		t.Fatalf("a user with no email and no code must never match, got %q", got)
	}
	if got := Resolve("whitelist", "", "", "", true); got != Shown {
		t.Fatalf("super admin passes an empty whitelist, got %q", got)
	}
	if got := Resolve("whitelist", " , ;\n\t#", "x@y.z", "ABC", false); got != Hidden {
		t.Fatalf("separator/# only whitelist matches nobody, got %q", got)
	}
}

func TestResolve_HiddenIsEmergencyEvenForSuper(t *testing.T) {
	if got := Resolve("hidden", "owner@example.com", "owner@example.com", "", true); got != Hidden {
		t.Fatalf("hidden = emergency shutdown, includes super admins and listed users, got %q", got)
	}
}

func TestNormalizeState(t *testing.T) {
	cases := map[string]string{
		"": StateWhitelist, "whitelist": StateWhitelist, "WHITELIST": StateWhitelist, "open": StateOpen, " Open ": StateOpen,
		"hidden": StateHidden, "HIDDEN": StateHidden, "garbage": StateWhitelist, "vip": StateWhitelist, "off": StateWhitelist,
	}
	for in, want := range cases {
		if got := NormalizeState(in); got != want {
			t.Errorf("NormalizeState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWhitelisted_Formats(t *testing.T) {
	list := "Owner@Example.com, second@example.com\nthird@example.com;fourth@example.com\t#CODE0001 CODE0002"
	cases := []struct {
		email, code string
		want        bool
	}{
		{"owner@example.com", "", true},
		{"OWNER@EXAMPLE.COM", "", true},
		{"second@example.com", "", true},
		{"third@example.com", "", true},
		{"fourth@example.com", "", true},
		{"nobody@example.com", "", false},
		{"", "", false},
		{"", "code0001", true}, // list 內有 # 前綴
		{"", "#CODE0001", true},
		{"", "CODE0002", true},
		{"", "CODE0003", false},
		{"nobody@example.com", "code0002", true}, // email 不中但 code 中
	}
	for _, c := range cases {
		if got := Whitelisted(list, c.email, c.code); got != c.want {
			t.Errorf("Whitelisted(email=%q, code=%q) = %v, want %v", c.email, c.code, got, c.want)
		}
	}
}
