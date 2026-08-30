package gpscalib

import (
	"testing"
	"time"
)

// TestEffectiveState 釘住「後台顯示的係數＝實際入帳的係數」這條不變式（對抗式審查 high finding）。
// 重點是 EffectiveFactor 的六道閘門裡，除了「懶判 stale」以外那兩道最容易被讀取端漏掉：
// 入口非 shown（Recompute 影子模式：非白名單會員一樣會被寫成 active/0.97xx）與 enabled=false。
func TestEffectiveState(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-3 * 24 * time.Hour)
	expired := now.Add(-(StaleDays + 1) * 24 * time.Hour)

	cases := []struct {
		name       string
		applyEntry string
		hasRow     bool
		enabled    bool
		status     string
		factor     float64
		lastPairAt *time.Time
		wantFactor float64
		wantApply  bool
		wantReason string
	}{
		{"白名單內 + active + 有新配對 → 生效", "shown", true, true, "active", 0.9778, &fresh, 0.9778, true, ""},
		{"影子模式（非白名單）→ 一律 1.0", "hidden", true, true, "active", 0.9778, &fresh, 1.0, false, "entry"},
		{"入口 locked（緊急關閉）→ 一律 1.0", "locked", true, true, "active", 0.9778, &fresh, 1.0, false, "entry"},
		{"使用者自己關掉 → 一律 1.0", "shown", true, false, "active", 0.9778, &fresh, 1.0, false, "disabled"},
		{"尚無 user_gps_calib 列 → 1.0", "shown", false, true, "warming", 1.0, nil, 1.0, false, "no_data"},
		{"warming → 1.0", "shown", true, true, "warming", 1.0, &fresh, 1.0, false, "status"},
		{"unstable → 1.0", "shown", true, true, "unstable", 0.95, &fresh, 1.0, false, "status"},
		{"active 但超過 StaleDays → 1.0（懶判 stale）", "shown", true, true, "active", 0.9778, &expired, 1.0, false, "stale"},
		{"DB/CTE 已標成 stale → 1.0", "shown", true, true, "stale", 1.0, &expired, 1.0, false, "stale"},
		{"frozen → 用後台釘住的係數", "shown", true, true, "frozen", 0.95, &expired, 0.95, true, ""},
		{"frozen 但使用者關掉 → 1.0", "shown", true, false, "frozen", 0.95, &fresh, 1.0, false, "disabled"},
		{"frozen 但非白名單 → 1.0", "hidden", true, true, "frozen", 0.95, &fresh, 1.0, false, "entry"},
	}
	for _, c := range cases {
		got := effectiveState(c.applyEntry, c.hasRow, c.enabled, c.status, c.factor, c.lastPairAt, now)
		if got.Factor != c.wantFactor || got.Applied != c.wantApply || got.Reason != c.wantReason {
			t.Errorf("%s: effectiveState = {%.4f %v %q}, want {%.4f %v %q}",
				c.name, got.Factor, got.Applied, got.Reason, c.wantFactor, c.wantApply, c.wantReason)
		}
	}
}

// TestApplyEntryFrom 純函式版入口判定（後台列表一次判一整頁用）必須與 resolveApplyEntry 同語意：
// 不含 super_admin 旁路、hidden/未設定一律 hidden。
func TestApplyEntryFrom(t *testing.T) {
	const list = "sogobaga@gmail.com\n#8U2TGUWE"
	cases := []struct {
		state, whitelist, email, code, want string
	}{
		{"open", "", "anyone@example.com", "ZZZZ1111", "shown"},
		{"locked", list, "sogobaga@gmail.com", "", "locked"},
		{"whitelist", list, "sogobaga@gmail.com", "", "shown"},
		{"whitelist", list, "other@example.com", "8u2tguwe", "shown"},
		{"whitelist", list, "other@example.com", "ZZZZ1111", "hidden"},
		{"whitelist", "", "sogobaga@gmail.com", "", "hidden"},
		{"hidden", list, "sogobaga@gmail.com", "", "hidden"},
		{"", list, "sogobaga@gmail.com", "", "hidden"},
	}
	for _, c := range cases {
		if got := applyEntryFrom(c.state, c.whitelist, c.email, c.code); got != c.want {
			t.Errorf("applyEntryFrom(%q, %q, %q, %q) = %q, want %q", c.state, c.whitelist, c.email, c.code, got, c.want)
		}
	}
}
