package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/cache/redistest"
)

// ratelimit_failfast_test.go：Redis 卡死／連不上時，限流中介層 ≤ ~1 秒就 fail-open，而且下游 handler
// 拿到的是**原始**請求 context（沒被取消、沒被縮短）。
//
// 一律用 go-redis 的**預設**選項建 client（ReadTimeout 5 s、MaxRetries 3、DialerRetries 5）——這正是
// 正式環境的形狀；若逾時只是靠測試把 client 調成很短，就證明不了 RateLimit 自己有逾時。

type ctxMarker struct{}

// passThroughProbe 記錄 handler 被呼叫時看到的 context。
type passThroughProbe struct {
	calls       int
	ctx         context.Context
	ctxErr      error
	hasDeadline bool
}

func (p *passThroughProbe) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.calls++
		p.ctx = r.Context()
		p.ctxErr = r.Context().Err()
		_, p.hasDeadline = r.Context().Deadline()
		w.WriteHeader(http.StatusNoContent)
	})
}

func fixedDim(r *http.Request) string { return "ff-dim" }

// serve 以帶標記的原始 context 打一次請求，回傳 (記錄器, 耗時, 原始 context)。
func serve(h http.Handler) (*httptest.ResponseRecorder, time.Duration, context.Context) {
	orig := context.WithValue(context.Background(), ctxMarker{}, "original")
	req := httptest.NewRequest(http.MethodPost, "/x", nil).WithContext(orig)
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, req)
	return rec, time.Since(start), orig
}

func assertFailedOpenQuickly(t *testing.T, label string, probe *passThroughProbe, rec *httptest.ResponseRecorder, elapsed time.Duration, orig context.Context) {
	t.Helper()
	if probe.calls != 1 || rec.Code != http.StatusNoContent {
		t.Fatalf("%s: handler calls=%d status=%d, want the request to fail open (1 call, 204)", label, probe.calls, rec.Code)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("%s: RateLimit took %v to fail open, want ≤ ~1.5s (its own 1s Redis timeout)", label, elapsed)
	}
	if probe.ctx != orig {
		t.Errorf("%s: the downstream handler must receive the ORIGINAL request context, got a derived one", label)
	}
	if probe.ctxErr != nil {
		t.Errorf("%s: the downstream context is already dead: %v", label, probe.ctxErr)
	}
	if probe.hasDeadline {
		t.Errorf("%s: the downstream context carries a deadline — the Redis timeout leaked past the middleware", label)
	}
	t.Logf("%s: failed open after %v; handler saw the original, live, deadline-free context", label, elapsed.Round(10*time.Millisecond))
}

func TestRateLimitFailsOpenQuicklyWhenRedisIsBlackholed(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: redistest.Blackhole(t)})
	defer rdb.Close()
	probe := &passThroughProbe{}
	rec, elapsed, orig := serve(RateLimit(rdb, "ff_black", 5, time.Minute, fixedDim)(probe.handler()))
	assertFailedOpenQuickly(t, "blackhole (accepts, never answers)", probe, rec, elapsed, orig)
	if elapsed < 800*time.Millisecond {
		t.Errorf("returned after %v — a blackhole must cost the full 1s timeout (else this test is not exercising the read-timeout path)", elapsed)
	}
}

func TestRateLimitFailsOpenQuicklyWhenRedisRefusesConnections(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: redistest.Refused(t)})
	defer rdb.Close()
	probe := &passThroughProbe{}
	rec, elapsed, orig := serve(RateLimit(rdb, "ff_refused", 5, time.Minute, fixedDim)(probe.handler()))
	assertFailedOpenQuickly(t, "connection refused", probe, rec, elapsed, orig)
}

// 先正常、後來才卡死（連線已在連線池裡）：docker pause／網路分區的真實形狀。
func TestRateLimitFailsOpenQuicklyWhenRedisHangsMidSession(t *testing.T) {
	mr := miniredis.RunT(t)
	proxy := redistest.NewProxy(t, mr.Addr())
	rdb := redis.NewClient(&redis.Options{Addr: proxy.Addr()})
	defer rdb.Close()
	probe := &passThroughProbe{}
	h := RateLimit(rdb, "ff_hang", 1, time.Minute, fixedDim)(probe.handler())

	// 健康時：第 1 次放行，第 2 次 429（限流照常運作）
	if rec, _, _ := serve(h); rec.Code != http.StatusNoContent {
		t.Fatalf("healthy #1: %d", rec.Code)
	}
	if rec, _, _ := serve(h); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("healthy #2: %d, want 429", rec.Code)
	}
	if probe.calls != 1 {
		t.Fatalf("handler calls = %d, want 1", probe.calls)
	}

	proxy.Blackhole() // Redis 卡死：連線還在連線池裡、不再回話
	*probe = passThroughProbe{}
	rec, elapsed, orig := serve(h)
	// 這一次本來會是 429（超限），但限流壞了就 fail-open：放行
	assertFailedOpenQuickly(t, "hung mid-session", probe, rec, elapsed, orig)
}

// 沒有退化：Redis 健康時，限流語意（key、上限、Retry-After）完全照舊。
func TestRateLimitUnchangedWithHealthyRedis(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	probe := &passThroughProbe{}
	h := RateLimit(rdb, "ff_ok", 2, time.Minute, fixedDim)(probe.handler())
	for i := 1; i <= 2; i++ {
		if rec, _, _ := serve(h); rec.Code != http.StatusNoContent {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	rec, _, _ := serve(h)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("request 3: %d Retry-After=%q, want 429 / 60", rec.Code, rec.Header().Get("Retry-After"))
	}
	if probe.calls != 2 {
		t.Fatalf("handler calls = %d, want 2", probe.calls)
	}
	if v, err := mr.Get("ratelimit:ff-dim:ff_ok"); err != nil || v != "3" {
		t.Fatalf("counter key ratelimit:ff-dim:ff_ok = %q (%v), want \"3\"", v, err)
	}
	if ttl := mr.TTL("ratelimit:ff-dim:ff_ok"); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("ttl = %v", ttl)
	}
	// rdb 為 nil：照舊整個放行
	nilProbe := &passThroughProbe{}
	if rec, _, _ := serve(RateLimit(nil, "x", 1, time.Minute, fixedDim)(nilProbe.handler())); rec.Code != http.StatusNoContent || nilProbe.calls != 1 {
		t.Fatalf("nil rdb must pass through: %d calls=%d", rec.Code, nilProbe.calls)
	}
}
