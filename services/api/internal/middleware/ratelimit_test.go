package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func newJSONRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// newUnreachableRedis 建一個「一定連不上、且很快失敗」的 redis client，供不需要真的驗證 Redis
// 內容、只需要驗證 rdb != nil 分支邏輯的測試使用。127.0.0.1:1 是保留低位埠，正常環境不會有任何
// 服務在聽，TCP 會直接回 RST，不必等到 DialTimeout 逾時才失敗。
func newUnreachableRedis() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  200 * time.Millisecond,
		ReadTimeout:  200 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond,
	})
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

// 2026-09-08 第二次稽核修法：body 同時出現多個大小寫變體命中同一欄位（例如 "email" 跟
// "Email" 並存）時，跟 handler 端 encoding/json 解到 struct 可能選到不同的值——不能再像
// 舊邏輯那樣「任選一個繼續限流」，必須整個請求判定無效。
func TestAccountField_DuplicateCaseKeys_Invalid(t *testing.T) {
	dim := AccountField("email")

	cases := []string{
		`{"email":"a@x.com","Email":"b@x.com"}`,
		`{"Email":"a@x.com","EMAIL":"b@x.com"}`,
		`{"email":"a@x.com","email":"b@x.com","EMAIL":"c@x.com"}`,
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			req := newJSONRequest(t, body)
			if got := dim(req); got != accountFieldInvalidBody {
				t.Errorf("dim(%s) = %q, want sentinel accountFieldInvalidBody", body, got)
			}
		})
	}
}

// 2026-09-08 第二次稽核修法：body 在第一個 JSON 物件後面還有多餘內容（例如攻擊者串接第二個
// JSON 物件，或加上垃圾字元），舊邏輯用 json.Unmarshal 解整個 body 會直接報錯回傳空字串
// （不限流），但 handler 用 json.NewDecoder(r.Body).Decode() 只讀第一個值就成功處理——
// 兩邊認知分岔，等於攻擊者只要加垃圾字元就能讓帳號級限流失效。現在必須判定整個請求無效。
func TestAccountField_TrailingContent_Invalid(t *testing.T) {
	dim := AccountField("email")

	cases := []string{
		`{"email":"a@x.com"}{"email":"b@x.com"}`,
		`{"email":"a@x.com"} garbage`,
		`{"email":"a@x.com"}{}`,
		`{"email":"a@x.com"}"trailing string"`,
		`{"email":"a@x.com"}1`,
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			req := newJSONRequest(t, body)
			if got := dim(req); got != accountFieldInvalidBody {
				t.Errorf("dim(%s) = %q, want sentinel accountFieldInvalidBody", body, got)
			}
		})
	}
}

// 結尾的空白字元（換行、空格）是合法客戶端也可能送出的形狀（例如某些 HTTP 客戶端在 body
// 後面加一個換行），不應該被判定為「多餘內容」。
func TestAccountField_TrailingWhitespace_Allowed(t *testing.T) {
	dim := AccountField("email")

	cases := []string{
		"{\"email\":\"a@x.com\"}\n",
		"{\"email\":\"a@x.com\"}   ",
		"{\"email\":\"a@x.com\"}\t\n  ",
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			req := newJSONRequest(t, body)
			if got := dim(req); got != "acct:a@x.com" {
				t.Errorf("dim(%q) = %q, want %q", body, got, "acct:a@x.com")
			}
		})
	}
}

// 完全解析不了的 JSON（不是「多餘內容」，是本來就不合法）維持既有行為：不擋，交給 handler
// 自己的 decode 回應錯誤。
func TestAccountField_MalformedJSON_NotInvalidSentinel(t *testing.T) {
	dim := AccountField("email")
	req := newJSONRequest(t, `{"email":`)
	if got := dim(req); got != "" {
		t.Errorf("dim() = %q, want empty string (not the invalid sentinel)", got)
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

// 即使判定 body 無效，body 也要維持可被還原讀取（雖然 RateLimit 會直接回 400 不呼叫 handler，
// 但這裡驗證 peekAndRestoreBody 的契約在這條新分支上仍然成立，避免未來改動時默默破壞它）。
func TestAccountField_RestoresBodyEvenWhenInvalid(t *testing.T) {
	dim := AccountField("email")
	body := `{"email":"a@x.com"}{"email":"b@x.com"}`
	req := newJSONRequest(t, body)

	if got := dim(req); got != accountFieldInvalidBody {
		t.Fatalf("dim() = %q, want sentinel", got)
	}

	restored := make([]byte, len(body))
	n, _ := req.Body.Read(restored)
	if string(restored[:n]) != body {
		t.Errorf("body not restored correctly: got %q, want %q", restored[:n], body)
	}
}

// RateLimit 收到 accountFieldInvalidBody 哨兵值時必須直接回 400，且完全不呼叫下一個 handler
// ——這是本次修法的核心行為：畸形 body 不能讓帳號級限流被繞過之餘、登入請求還被正常處理。
// 用打不通的 redis client 也能測這條路徑，因為 400 判斷在碰 Redis 之前就發生。
func TestRateLimit_InvalidAccountBody_Returns400AndSkipsHandler(t *testing.T) {
	rdb := newUnreachableRedis()
	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	mw := RateLimit(rdb, "auth_login_acct", 10, time.Minute, AccountField("email"))
	wrapped := mw(next)

	req := newJSONRequest(t, `{"email":"a@x.com"}{"email":"b@x.com"}`)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if handlerCalled {
		t.Error("handler must not be called for an invalid account body")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if got := rec.Body.String(); got != `{"error":"invalid request body"}` {
		t.Errorf("body = %q", got)
	}
}

// 正常 body（含大小寫不同但只出現一次的欄位）不受影響：Redis 打不通時整條限流鏈路 fail-open，
// 照常呼叫 handler（既有行為，這裡確認新增的 400 分支沒有誤傷正常請求）。
func TestRateLimit_NormalBody_StillCallsHandlerWhenRedisDown(t *testing.T) {
	rdb := newUnreachableRedis()
	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	mw := RateLimit(rdb, "auth_login_acct", 10, time.Minute, AccountField("email"))
	wrapped := mw(next)

	req := newJSONRequest(t, `{"Email":"a@x.com","password":"x"}`)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if !handlerCalled {
		t.Error("handler should be called (fail-open) when Redis is unreachable for a valid body")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
