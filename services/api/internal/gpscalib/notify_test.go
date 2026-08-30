package gpscalib

import "testing"

// TestNotifyAllowed 覆蓋「校正狀態變更站內信該不該發」的純函式閘門（見 service.go notifyAllowed）。
// 重點在使用者需求：即使入口開放給全站（applyEntry=="shown" 對所有人成立），沒列在
// gps_calib_notify_whitelist 的帳號一律不得收到信；白名單留空＝一封都不發（fail-closed）。
func TestNotifyAllowed(t *testing.T) {
	const list = "sogobaga@gmail.com\n#8U2TGUWE"
	cases := []struct {
		name       string
		applyEntry string
		list       string
		email      string
		code       string
		want       bool
	}{
		{"白名單內的 email + 校正生效 → 發", "shown", list, "sogobaga@gmail.com", "ABC123", true},
		{"白名單內的 email 大小寫不敏感", "shown", list, "SoGoBaGa@Gmail.com", "", true},
		{"白名單內的帳號編碼（帶#）", "shown", list, "other@example.com", "#8U2TGUWE", true},
		{"白名單內的帳號編碼（省略#）", "shown", list, "other@example.com", "8u2tguwe", true},
		{"入口 open 但不在通知白名單 → 不發", "shown", list, "someone@example.com", "ZZZZZZZZ", false},
		{"白名單留空 → 一封都不發（fail-closed）", "shown", "", "sogobaga@gmail.com", "8U2TGUWE", false},
		{"白名單只有空白/分隔符 → 不發", "shown", " , ;\n\t", "sogobaga@gmail.com", "", false},
		{"在白名單但校正對他沒生效（hidden）→ 不發", "hidden", list, "sogobaga@gmail.com", "", false},
		{"在白名單但入口 locked → 不發", "locked", list, "sogobaga@gmail.com", "", false},
		{"email 與編碼皆空 → 不發（不得被空 token 誤命中）", "shown", list, "", "", false},
	}
	for _, c := range cases {
		if got := notifyAllowed(c.applyEntry, c.list, c.email, c.code); got != c.want {
			t.Errorf("%s: notifyAllowed(%q, %q, %q, %q) = %v, want %v",
				c.name, c.applyEntry, c.list, c.email, c.code, got, c.want)
		}
	}
}

// TestNotifyAllowedEmptyListNeverSends 明確釘住「空白名單對任何帳號都不發」的安全預設——
// 這條是使用者需求的核心（正式開放後不得誤發給其他會員），若日後有人把空值改成「全放行」
// （比照 gps_calib_entry_whitelist 的語意）這個測試會直接失敗。
func TestNotifyAllowedEmptyListNeverSends(t *testing.T) {
	for _, email := range []string{"a@example.com", "b@example.com", "sogobaga@gmail.com"} {
		if notifyAllowed("shown", "", email, "CODE1234") {
			t.Fatalf("空白名單仍發信給 %s", email)
		}
	}
}
