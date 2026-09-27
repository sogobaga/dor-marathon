package profile

import "testing"

// TestIsValidUiSkin 涵蓋 PUT /me/ui-skin 的值驗證分支（契約 §2.1：值不在 skin_options → 400
// invalid_skin）。400/403/200 三個 HTTP 分支中，403（resolveSkinSelectEntry）與 200（DB 寫入）
// 兩段需要真實 DB 連線，走 §6 的「本機 api」手動驗證；這裡只覆蓋不依賴 DB 的純函式部分。
func TestIsValidUiSkin(t *testing.T) {
	cases := []struct {
		skin string
		want bool
	}{
		{"default", true},
		{"scifi", true},
		{"retro", true},
		{"", false},
		{"warm", false},
		{"Scifi", false}, // 大小寫敏感，值必須完全相符 migration 193 的 CHECK 約束
		{"retro ", false},
	}
	for _, c := range cases {
		if got := isValidUiSkin(c.skin); got != c.want {
			t.Errorf("isValidUiSkin(%q) = %v, want %v", c.skin, got, c.want)
		}
	}
}
