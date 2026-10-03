package reqip

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafePath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/api/v1/integrations/garmin/webhook/SECRETtoken123/activities", "/api/v1/integrations/garmin/webhook/***/activities"},
		{"/api/v1/integrations/garmin/webhook/SECRETtoken123/deregistrations", "/api/v1/integrations/garmin/webhook/***/deregistrations"},
		{"/api/v1/integrations/garmin/webhook/SECRETtoken123/a/b/c", "/api/v1/integrations/garmin/webhook/***/a/b/c"},
		{"/api/v1/integrations/garmin/webhook/SECRETtoken123", "/api/v1/integrations/garmin/webhook/***"},
		{"/api/v1/integrations/garmin/webhook/SECRETtoken123/", "/api/v1/integrations/garmin/webhook/***/"},
		{"/api/v1/integrations/garmin/webhook/", "/api/v1/integrations/garmin/webhook/"},
		// 其他路徑完全不變
		{"/api/v1/integrations/garmin/status", "/api/v1/integrations/garmin/status"},
		{"/api/v1/integrations/garmin/connect", "/api/v1/integrations/garmin/connect"},
		{"/api/v1/integrations/garmin/webhook", "/api/v1/integrations/garmin/webhook"},
		{"/api/v1/integrations/coros-mcp/callback", "/api/v1/integrations/coros-mcp/callback"},
		{"/api/v1/integrations/terra/webhook", "/api/v1/integrations/terra/webhook"},
		{"/api/v1/profile/dashboard", "/api/v1/profile/dashboard"},
		{"/health", "/health"},
		{"", ""},
		{"/", "/"},
		// 前綴相似但不是 webhook 前綴
		{"/api/v1/integrations/garmin/webhookX/abc", "/api/v1/integrations/garmin/webhookX/abc"},
		{"/x/api/v1/integrations/garmin/webhook/abc", "/x/api/v1/integrations/garmin/webhook/abc"},
	}
	for _, c := range cases {
		if got := SafePath(c.in); got != c.want {
			t.Errorf("SafePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// token 本身無論長什麼樣（含特殊字元）都不會出現在輸出
	for _, tok := range []string{"a", "A1b2C3d4E5f6G7h8I9j0", "tok%2Fwith%20stuff", "中文token", strings.Repeat("x", 500)} {
		out := SafePath("/api/v1/integrations/garmin/webhook/" + tok + "/activities")
		if strings.Contains(out, tok) && tok != "a" { // 單字元 "a" 會出現在 "activities" 的 a，不算
			t.Errorf("token %q leaked: %s", tok, out)
		}
		if !strings.HasPrefix(out, "/api/v1/integrations/garmin/webhook/***") {
			t.Errorf("unexpected output %q", out)
		}
	}
}

// 守門：把請求路徑寫進 log／告警的三個點（5xx 激增、panic 告警、喚醒歸因）都必須經 SafePath，
// 不得直接用 r.URL.Path——否則 webhook 路徑上的秘密 token 會被寫進 Telegram 與日誌。
func TestAlertAndWakeAttributionUseSafePath(t *testing.T) {
	for _, f := range []string{"../middleware/alert.go", "../dbwake/dbwake.go"} {
		b, err := os.ReadFile(filepath.FromSlash(f))
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if !strings.Contains(src, "reqip.SafePath(r.URL.Path)") {
			t.Errorf("%s must mask the request path with reqip.SafePath", f)
		}
		// 每一次出現 r.URL.Path 都必須是 SafePath 的參數
		rest := src
		for {
			i := strings.Index(rest, "r.URL.Path")
			if i < 0 {
				break
			}
			if !strings.HasSuffix(rest[:i], "reqip.SafePath(") {
				t.Errorf("%s uses a raw r.URL.Path near %q", f, rest[max(0, i-40):min(len(rest), i+20)])
			}
			rest = rest[i+len("r.URL.Path"):]
		}
	}
}
