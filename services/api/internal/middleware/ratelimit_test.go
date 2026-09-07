package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newJSONRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// M7 修法：AccountField 對 JSON 欄位名的比對必須大小寫不敏感，比照 handler 端
// encoding/json 解到 struct 時的既定行為（先找完全相同大小寫，找不到才退而求其次比對忽略大小寫）。
func TestAccountField_CaseInsensitiveKeyMatch(t *testing.T) {
	dim := AccountField("email")

	cases := []struct {
		name string
		body string
		want string
	}{
		{"exact lowercase key", `{"email":"User@Example.com","password":"x"}`, "acct:user@example.com"},
		{"capitalized key", `{"Email":"User@Example.com","password":"x"}`, "acct:user@example.com"},
		{"all-caps key", `{"EMAIL":"User@Example.com","password":"x"}`, "acct:user@example.com"},
		{"mixed-case key", `{"eMaIl":" User@Example.com ","password":"x"}`, "acct:user@example.com"},
		{"missing field", `{"password":"x"}`, ""},
		{"empty value", `{"Email":"","password":"x"}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := newJSONRequest(t, c.body)
			got := dim(req)
			if got != c.want {
				t.Errorf("dim(%s) = %q, want %q", c.body, got, c.want)
			}
		})
	}
}

// 偷看 body 判斷限流維度之後，handler 自己的 json.Decode(r.Body) 必須仍能讀到完整內容
// （peekAndRestoreBody 的契約）——這裡只驗證大小寫比對這條新分支不會破壞既有的「還原 body」行為。
func TestAccountField_RestoresBodyForHandler(t *testing.T) {
	dim := AccountField("email")
	body := `{"Email":"User@Example.com","password":"x"}`
	req := newJSONRequest(t, body)

	if got := dim(req); got != "acct:user@example.com" {
		t.Fatalf("dim() = %q", got)
	}

	restored := make([]byte, len(body))
	n, _ := req.Body.Read(restored)
	if string(restored[:n]) != body {
		t.Errorf("body not restored correctly: got %q, want %q", restored[:n], body)
	}
}
