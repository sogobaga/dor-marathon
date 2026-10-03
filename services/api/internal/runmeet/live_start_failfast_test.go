package runmeet

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/cache/redistest"
	"github.com/dor/api/internal/middleware"
	"github.com/dor/api/internal/runmeet/live"
)

// live_start_failfast_test.go：Redis 卡死／連不上時，POST /run-meets/{id}/live/start 立刻答 503
// redis_unavailable——不是等 30 秒後因為 context 已死而 500 "failed to resolve access"。
//
// 一律用 go-redis 的預設選項建 client（正式環境的形狀）。start 在「讀任何 DB 之前」就先取 Redis TIME
// （checkedAtMs，見 handle 的檔頭說明），所以 Redis 壞掉時連 DB 都不會碰。

func downRedisShapes(t *testing.T) map[string]string {
	return map[string]string{"blackhole": redistest.Blackhole(t), "refused": redistest.Refused(t)}
}

// startViaChain 與 handler.go Router() 相同的順序：限流（runmeet_live_start 6/分/人）→ handler。
func startViaChain(rdb *redis.Client, start http.Handler, uid string) (*httptest.ResponseRecorder, time.Duration) {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), auth.CtxKeyUserID, uid)))
		})
	})
	r.With(middleware.RateLimit(rdb, "runmeet_live_start", 6, time.Minute, middleware.UserOrIP)).
		Post("/{id}/live/start", start.ServeHTTP)
	req := httptest.NewRequest(http.MethodPost, "/"+liveMeetUUID+"/live/start", strings.NewReader(startBody(consentV1, false)))
	req.RemoteAddr = "198.51.100.9:5555"
	rec := httptest.NewRecorder()
	t0 := time.Now()
	r.ServeHTTP(rec, req)
	return rec, time.Since(t0)
}

func TestLiveStartAnswers503QuicklyWhenRedisIsDown(t *testing.T) {
	for shape, addr := range downRedisShapes(t) {
		shape, addr := shape, addr
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			rdb := redis.NewClient(&redis.Options{Addr: addr})
			defer rdb.Close()
			uid := uuid.NewString()
			repo := &fakeLiveRepo{
				settings: live.DefaultSettings(),
				flags:    map[string]fakeFlags{},
				members:  map[string]string{uid: MemberJoined},
				names:    map[string][2]string{},
				meet:     liveMeet{ID: liveMeetUUID, OwnerID: uuid.NewString(), Title: "t", Status: StatusOpen, MeetAt: time.Now()},
			}
			repo.settings.EntryState = "open"
			h := &liveStartHandler{repo: repo, store: live.NewStore(rdb)}

			// (a) handler 單獨：Store.NowMs 的 2 秒期限
			rec, elapsed := startViaChain(nil, http.HandlerFunc(h.handle), uid) // rdb=nil：不經限流
			if rec.Code != http.StatusServiceUnavailable || codeOf(t, rec) != "redis_unavailable" {
				t.Fatalf("handler alone: %d %s, want 503 redis_unavailable (never 500)", rec.Code, rec.Body.String())
			}
			if elapsed > 2500*time.Millisecond {
				t.Fatalf("handler alone answered after %v, want ≤ ~2.5s", elapsed)
			}
			t.Logf("%s Redis, /live/start handler alone: 503 redis_unavailable after %v", shape, elapsed.Round(10*time.Millisecond))

			// (b) 經限流中介層：≈1 s（限流 fail-open）+ ≈2 s（Store 逾時）
			rec, elapsed = startViaChain(rdb, http.HandlerFunc(h.handle), uid)
			if rec.Code != http.StatusServiceUnavailable || codeOf(t, rec) != "redis_unavailable" {
				t.Fatalf("through RateLimit: %d %s, want 503 redis_unavailable (never 500)", rec.Code, rec.Body.String())
			}
			if elapsed > 3500*time.Millisecond {
				t.Fatalf("through RateLimit answered after %v, want ≤ ~3.5s (old behaviour: ~30s then 500)", elapsed)
			}
			t.Logf("%s Redis, /live/start through RateLimit: 503 redis_unavailable after %v", shape, elapsed.Round(10*time.Millisecond))

			// Redis 壞掉時連 DB 都不該碰（fail-closed 在讀 DB 之前）：沒有任何 repo 呼叫
			if len(repo.calls) != 0 || len(repo.audit) != 0 {
				t.Fatalf("with Redis down the handler must fail before any DB access: calls=%v audit=%d", repo.calls, len(repo.audit))
			}
		})
	}
}

// 真實接線（NewHandler → pgLiveRepo＋live.Store，db 為 nil）：若 handler 在 Redis 壞掉時還去碰 DB，
// nil 連線池會直接 panic——這個測試同時證明「沒碰 DB」。
func TestNewHandlerLiveStartWithDeadRedisAnswers503WithoutTouchingDB(t *testing.T) {
	for shape, addr := range downRedisShapes(t) {
		shape, addr := shape, addr
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			rdb := redis.NewClient(&redis.Options{Addr: addr})
			defer rdb.Close()
			h := NewHandler(nil, rdb, nil)
			t.Cleanup(h.killSwitchOff)

			rec, elapsed := startViaChain(nil, http.HandlerFunc(h.LiveStart), uuid.NewString())
			if rec.Code != http.StatusServiceUnavailable || codeOf(t, rec) != "redis_unavailable" {
				t.Fatalf("%d %s, want 503 redis_unavailable", rec.Code, rec.Body.String())
			}
			if elapsed > 2500*time.Millisecond {
				t.Fatalf("answered after %v, want ≤ ~2.5s", elapsed)
			}
			t.Logf("%s Redis, real wiring (NewHandler, nil DB pool): 503 after %v", shape, elapsed.Round(10*time.Millisecond))
		})
	}
}
