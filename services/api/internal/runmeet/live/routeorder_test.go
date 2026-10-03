package live

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/middleware"
)

// routeorder_test.go：契約 §9-12——RequireAuth 必須先於 RateLimit 執行，限流維度才是 "u<uid>"。
//
// 為什麼重要：middleware.UserOrIP 在 ctx 沒有使用者時會退回 client IP。50 位跑者從同一個場地 WiFi／
// CGNAT 出口上網，若限流先於驗證，50 個人會共用一個「60 次/分」的桶，現場直接被限流。
//
// main.go 無法被單元測試（package main、依賴整個系統），所以：
//  1. 這裡以與 main.go 完全相同的鏈（r.Group(Use(auth)) → r.With(RateLimit(...UserOrIP)).Mount(...)）
//     驗證行為；
//  2. cmd/api/wiring_test.go 以 go/parser 檢查 main.go 的真實原始碼確實是這個結構。

// fakeRequireAuth 與 middleware.RequireAuth 對 ctx 的效果相同：把使用者 id 放進 context（auth.CtxKeyUserID）。
// 以 X-Test-User 標頭決定是誰（真正的 RequireAuth 從 JWT 取）。
func fakeRequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := r.Header.Get("X-Test-User")
		if uid == "" {
			http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), auth.CtxKeyUserID, uid)))
	})
}

const sharedIP = "203.0.113.5:4444" // 同一個場地 WiFi 出口

func (e *testEnv) request(h http.Handler, path, user, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.RemoteAddr = sharedIP
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// productionChain 與 cmd/api/main.go 相同的掛載順序。
func (e *testEnv) productionChain() http.Handler {
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(fakeRequireAuth) // main.go：r.Use(middleware.RequireAuth(authSvc))
		r.With(middleware.RateLimit(e.rdb, "runmeet_live_pos", 60, time.Minute, middleware.UserOrIP)).
			Mount("/run-meet-live", NewHandlerWithStore(e.store).Router())
	})
	return r
}

func TestRouteOrderRequireAuthBeforeRateLimitGivesPerUserDimension(t *testing.T) {
	e := newEnv(t)
	a, b := newPlayer("A"), newPlayer("B")
	e.start(a, 50, false)
	e.start(b, 50, false)
	h := e.productionChain()
	url := "/run-meet-live/" + testMeet + "/pos"

	if rec := e.request(h, url, a.uid, e.posJSON(a, fixA)); rec.Code != http.StatusOK {
		t.Fatalf("a: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.request(h, url, b.uid, e.posJSON(b, fixB)); rec.Code != http.StatusOK {
		t.Fatalf("b: %d %s", rec.Code, rec.Body.String())
	}

	// 限流維度必須是 "u<uid>"（RequireAuth 已把使用者放進 ctx），不是 client IP
	for _, uid := range []string{a.uid, b.uid} {
		key := "ratelimit:u" + uid + ":runmeet_live_pos"
		if got := e.get(key); got != "1" {
			t.Errorf("rate-limit counter %q = %q, want \"1\" (dimension must be u<uid>)", key, got)
		}
	}
	for _, k := range e.mr.Keys() {
		if strings.HasPrefix(k, "ratelimit:203.0.113.5") {
			t.Fatalf("limiter fell back to the IP dimension: %q — RequireAuth must run BEFORE RateLimit", k)
		}
	}
}

// 結果面：同一出口 IP 的兩個人各有各的 60/分，A 用完不影響 B。
func TestRouteOrderTwoUsersBehindOneIPHaveSeparateBuckets(t *testing.T) {
	e := newEnv(t)
	a, b := newPlayer("A"), newPlayer("B")
	e.start(a, 50, false)
	e.start(b, 50, false)
	h := e.productionChain()
	url := "/run-meet-live/" + testMeet + "/pos"

	limiterBody := "too many requests"
	for i := 1; i <= 60; i++ { // 時間不前進：多數會被 1.5 s 頻率地板擋下（429 too_fast），但都會計入限流桶
		rec := e.request(h, url, a.uid, e.posJSON(a, ""))
		if strings.Contains(rec.Body.String(), limiterBody) {
			t.Fatalf("request %d of 60 was rate limited by the per-minute bucket", i)
		}
	}
	rec := e.request(h, url, a.uid, e.posJSON(a, ""))
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), limiterBody) {
		t.Fatalf("request 61 must hit the limiter: %d %s", rec.Code, rec.Body.String())
	}
	if ra := rec.Header().Get("Retry-After"); ra != "60" {
		t.Errorf("limiter Retry-After = %q, want 60", ra)
	}
	// 同一個 IP 的 B 完全不受影響
	rec = e.request(h, url, b.uid, e.posJSON(b, fixB))
	if rec.Code != http.StatusOK {
		t.Fatalf("b (same IP) must not share a's bucket: %d %s", rec.Code, rec.Body.String())
	}
}

// 對照組：若限流先於驗證（錯誤順序），維度會退回 IP——證明上面的斷言真的抓得到這個錯。
func TestRouteOrderControlWrongOrderFallsBackToIP(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		// 錯誤：限流在驗證之前
		r.Use(middleware.RateLimit(e.rdb, "runmeet_live_pos", 60, time.Minute, middleware.UserOrIP))
		r.Use(fakeRequireAuth)
		r.Mount("/run-meet-live", NewHandlerWithStore(e.store).Router())
	})
	e.request(r, "/run-meet-live/"+testMeet+"/pos", a.uid, e.posJSON(a, fixA))

	if e.get("ratelimit:203.0.113.5:runmeet_live_pos") != "1" {
		t.Fatalf("control: wrong order must key the limiter by IP; keys = %v", e.mr.Keys())
	}
	if e.mr.Exists("ratelimit:u" + a.uid + ":runmeet_live_pos") {
		t.Fatal("control: wrong order must not produce a per-user bucket")
	}
}

// 縱深防禦：若有人把子路由掛到 RequireAuth 之外，所有請求都是 401（fail-closed），
// 而不是讓限流靜默退回 IP 維度。
func TestMountedOutsideAuthFailsClosed(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	r := chi.NewRouter()
	r.With(middleware.RateLimit(e.rdb, "runmeet_live_pos", 60, time.Minute, middleware.UserOrIP)).
		Mount("/run-meet-live", NewHandlerWithStore(e.store).Router())
	rec := e.request(r, "/run-meet-live/"+testMeet+"/pos", "", e.posJSON(a, fixA))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no auth in front of the router: %d %s, want 401", rec.Code, rec.Body.String())
	}
}
