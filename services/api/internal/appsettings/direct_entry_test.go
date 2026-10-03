package appsettings

// 直連手錶入口設定鍵（COROS GA 契約 §2.1／§3.1）的後台驗證器：三態＋空字串（缺鍵）、kill switch 0|1、白名單長度上限。

import (
	"strings"
	"testing"
)

func TestDirectEntryStateSpecs(t *testing.T) {
	for _, key := range []string{"coros_mcp_entry_state", "garmin_entry_state"} {
		for _, ok := range []string{"", "hidden", "whitelist", "open"} {
			if known, valid := ValidateValue(key, ok); !known || !valid {
				t.Errorf("%s=%q must be accepted (known=%v valid=%v)", key, ok, known, valid)
			}
		}
		for _, bad := range []string{"shown", "locked", "off", "vip", "OPEN", "true", "1", "whitelist,open"} {
			if known, valid := ValidateValue(key, bad); !known || valid {
				t.Errorf("%s=%q must be rejected (known=%v valid=%v)", key, bad, known, valid)
			}
		}
	}
}

func TestAutoSyncKillSwitchSpec(t *testing.T) {
	for _, ok := range []string{"", "0", "1"} {
		if known, valid := ValidateValue("coros_mcp_autosync_enabled", ok); !known || !valid {
			t.Errorf("coros_mcp_autosync_enabled=%q must be accepted", ok)
		}
	}
	for _, bad := range []string{"2", "-1", "true", "on", "off", "yes", "01"} {
		if known, valid := ValidateValue("coros_mcp_autosync_enabled", bad); !known || valid {
			t.Errorf("coros_mcp_autosync_enabled=%q must be rejected", bad)
		}
	}
}

func TestDirectWhitelistSpecs(t *testing.T) {
	for _, key := range []string{"coros_mcp_whitelist", "garmin_whitelist"} {
		if known, valid := ValidateValue(key, "a@b.c, ABCD2345\nx@y.z"); !known || !valid {
			t.Errorf("%s must accept a normal whitelist", key)
		}
		if known, valid := ValidateValue(key, strings.Repeat("a", 20000)); !known || !valid {
			t.Errorf("%s must accept exactly 20000 chars", key)
		}
		if known, valid := ValidateValue(key, strings.Repeat("a", 20001)); !known || valid {
			t.Errorf("%s must reject 20001 chars", key)
		}
	}
}

// 這些鍵不屬於「公開設定」（publicKeys 只有 active_skin／favicon_url／google_login_ux_mode）：入口白名單不得外流給匿名訪客。
func TestDirectEntryKeysAreNotPublic(t *testing.T) {
	for _, key := range []string{"coros_mcp_entry_state", "coros_mcp_whitelist", "coros_mcp_autosync_enabled", "garmin_entry_state", "garmin_whitelist"} {
		if publicKeys[key] {
			t.Errorf("%s must never be exposed through /app-settings/public", key)
		}
	}
}
