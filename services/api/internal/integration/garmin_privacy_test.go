package integration

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dor/api/internal/auth"
)

// 日誌紀律（R-M5／License §15.11）：連接、刷新、撤銷、清理整條流程的日誌不得含 token、授權碼、PKCE verifier、state、
// client secret、JWT secret、完整 Garmin userId、完整 DOR 使用者 id、路徑 token。
func TestLogs_NoSecretsAcrossConnectRefreshDisconnectFlow(t *testing.T) {
	logs := captureLogs(t)
	e := newConnectEnv(t, nil)
	cr := e.connect(t, tUserA, "v1")
	st, _ := garminVerifyState("jwt-secret-test", cr.State, time.Now())
	verifier, _, _ := e.h.kv.Get(context.Background(), garminPKCEKeyPrefix+st.SID)
	code := e.api.newCode(cr.Challenge)
	e.callback(t, cr, code, "", cr.Cookie)
	e.req(http.MethodGet, "/status", tUserA, "")

	// 刷新（含失敗路徑：invalid_grant、5xx）
	c := e.store.conn(tUserA)
	e.store.mu.Lock()
	e.store.cs.conns[tUserA].ExpiresAt = time.Now().Add(-time.Hour)
	e.store.mu.Unlock()
	e.api.tokenStatus = 503
	_, _ = e.h.ensureFresh(context.Background(), e.store.conn(tUserA), false)
	e.api.tokenStatus = 0
	_, _ = e.h.ensureFresh(context.Background(), e.store.conn(tUserA), false)
	// webhook：錯誤 token／落地失敗／壞 JSON
	e.post("/webhook/not-the-token/activities", strings.NewReader("{}"), nil)
	e.postRaw("activities", "not json")
	e.postJSON("activities", synthPush(synthActivity(c.ProviderUserID, "LOGP-1", time.Now().Add(-time.Hour).Unix())))
	e.store.waitSettled(t, 3*time.Second)
	// 撤銷／斷線
	_, _ = e.h.processDeregEvent(context.Background(), deregEv(c.ProviderUserID, 0))
	e.req(http.MethodPost, "/disconnect", tUserA, "")
	time.Sleep(50 * time.Millisecond)

	l := logs()
	secrets := map[string]string{
		"access token": "acc-", "refresh token": "ref-", "auth code": code, "pkce verifier": verifier, "state": cr.State,
		"nonce": cr.Cookie.Value, "client secret": "secret-test", "jwt secret": "jwt-secret-test", "webhook token": testGarminToken,
		"garmin user id": e.api.userID, "dor user id": tUserA,
	}
	for name, s := range secrets {
		if s != "" && strings.Contains(l, s) {
			t.Errorf("log output contains the %s", name)
		}
	}
	for _, a := range e.mails { // 告警同理
		for name, s := range secrets {
			if s != "" && strings.Contains(a, s) {
				t.Errorf("alert contains the %s: %s", name, a)
			}
		}
	}
	if l == "" {
		t.Fatal("expected some log output to inspect")
	}
}

// 限流掛在 RequireAuth 之後、只掛使用者端點；webhook 與 callback 不限流（被 429 會觸發 Garmin 退避重送）。
func TestRouter_RateLimitWiring(t *testing.T) {
	var mu sync.Mutex
	type spec struct {
		limit  int
		window time.Duration
	}
	actions := map[string]spec{}
	var order []string // 以 header 驗證執行順序：先認證、再限流
	e := newConnectEnv(t, nil)
	e.h.rateLimit = func(action string, limit int, window time.Duration) func(http.Handler) http.Handler {
		mu.Lock()
		actions[action] = spec{limit, window}
		mu.Unlock()
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				order = append(order, action+":"+r.Header.Get("X-Test-User")) // RequireAuth 沒有改 header；用 context 判斷認證已先行
				mu.Unlock()
				if r.Context().Value(auth.CtxKeyUserID) == nil {
					t.Errorf("rate limit for %s ran before authentication", action)
				}
				next.ServeHTTP(w, r)
			})
		}
	}
	e.router = e.h.Router()
	want := map[string]spec{"garmin_connect": {10, time.Minute}, "garmin_status": {60, time.Minute}, "garmin_disconnect": {10, time.Minute}}
	mu.Lock()
	for a, s := range want {
		if got, ok := actions[a]; !ok || got != s {
			t.Errorf("rate limit %s = %+v, want %+v", a, got, s)
		}
	}
	if len(actions) != len(want) {
		t.Errorf("unexpected rate limit actions: %v", actions)
	}
	mu.Unlock()
	// 實際走一遍：三個使用者端點都經過限流；webhook／callback 沒有
	e.req(http.MethodPost, "/connect", tUserA, `{"consent":true,"consent_v":"v1"}`)
	e.req(http.MethodGet, "/status", tUserA, "")
	e.req(http.MethodPost, "/disconnect", tUserA, "")
	e.postJSON("activities", synthPush())
	e.req(http.MethodGet, "/callback?state=x", "", "")
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 {
		t.Fatalf("rate limit middleware ran %d times, want 3 (connect/status/disconnect only): %v", len(order), order)
	}
}
