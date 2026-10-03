package live

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/cache/redistest"
	"github.com/dor/api/internal/middleware"
)

// failfast_test.go：Redis 卡死／連不上時，團練同步跑的 Redis 存取在 ≤ ~2 秒內回 ErrUnavailable，
// 端點立刻答 503 redis_unavailable（不是等 20～30 秒再 500）。
//
// 一律用 go-redis 的**預設**選項建 client（正式環境的形狀：ReadTimeout 5 s、MaxRetries 3）。

const storeBudget = 2500 * time.Millisecond // RedisTimeout(2 s) + 排程餘裕

func newDefaultClient(t *testing.T, addr string) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// 八個 Store 方法各自在卡死／被拒的 Redis 上 ≤ 2.5 秒回 ErrUnavailable（平行跑，總時間 ≈ 2 秒）。
func TestStoreMethodsFailFastWhenRedisIsDown(t *testing.T) {
	uid, meet := newPlayer("A").uid, testMeet
	ops := map[string]func(ctx context.Context, s *Store) error{
		"NowMs": func(ctx context.Context, s *Store) error { _, err := s.NowMs(ctx); return err },
		"Start": func(ctx context.Context, s *Store) error {
			_, err := s.Start(ctx, meet, StartParams{UID: uid, SID: "abcdefghijklmnop", Name: "A", MaxLive: 50, CheckedAtMs: 1})
			return err
		},
		"Pos": func(ctx context.Context, s *Store) error {
			_, err := s.Pos(ctx, meet, PosParams{UID: uid, SID: "abcdefghijklmnop"})
			return err
		},
		"Leave": func(ctx context.Context, s *Store) error {
			_, err := s.Leave(ctx, meet, uid, "abcdefghijklmnop")
			return err
		},
		"Revoke":    func(ctx context.Context, s *Store) error { return s.Revoke(ctx, meet, uid) },
		"MarkDead":  func(ctx context.Context, s *Store) error { return s.MarkDead(ctx, meet) },
		"ClearDead": func(ctx context.Context, s *Store) error { return s.ClearDead(ctx, meet) },
		"SetKill":   func(ctx context.Context, s *Store) error { return s.SetKill(ctx, true) },
	}
	for shape, addr := range map[string]string{
		"blackhole": redistest.Blackhole(t),
		"refused":   redistest.Refused(t),
	} {
		for name, op := range ops {
			shape, addr, name, op := shape, addr, name, op
			t.Run(shape+"/"+name, func(t *testing.T) {
				t.Parallel()
				s := NewStore(newDefaultClient(t, addr))
				start := time.Now()
				err := op(context.Background(), s)
				elapsed := time.Since(start)
				if !errors.Is(err, ErrUnavailable) {
					t.Fatalf("err = %v, want ErrUnavailable", err)
				}
				if elapsed > storeBudget {
					t.Fatalf("%s took %v on a %s Redis, want ≤ %v (RedisTimeout %v)", name, elapsed, shape, storeBudget, RedisTimeout)
				}
				t.Logf("%s on %s Redis: ErrUnavailable after %v", name, shape, elapsed.Round(10*time.Millisecond))
			})
		}
	}
}

// 先正常、後來才卡死：連線已在連線池裡，Redis 中途停止回應。
func TestStoreFailsFastWhenRedisHangsMidSession(t *testing.T) {
	mr := miniredis.RunT(t)
	proxy := redistest.NewProxy(t, mr.Addr())
	s := NewStore(newDefaultClient(t, proxy.Addr()))
	a := newPlayer("A")
	ctx := context.Background()

	now, err := s.NowMs(ctx)
	if err != nil {
		t.Fatalf("healthy NowMs: %v", err)
	}
	if res, err := s.Start(ctx, testMeet, StartParams{UID: a.uid, SID: a.sid, Name: a.name, MaxLive: 50, CheckedAtMs: now}); err != nil || res.Outcome != OutcomeOK {
		t.Fatalf("healthy Start: %+v %v", res, err)
	}

	proxy.Blackhole()
	for name, op := range map[string]func() error{
		"Pos": func() error { _, err := s.Pos(ctx, testMeet, PosParams{UID: a.uid, SID: a.sid}); return err },
		"Start": func() error {
			_, err := s.Start(ctx, testMeet, StartParams{UID: a.uid, SID: a.sid, Name: a.name, MaxLive: 50, CheckedAtMs: now})
			return err
		},
		"Leave": func() error { _, err := s.Leave(ctx, testMeet, a.uid, a.sid); return err },
	} {
		start := time.Now()
		err := op()
		elapsed := time.Since(start)
		if !errors.Is(err, ErrUnavailable) || elapsed > storeBudget {
			t.Fatalf("%s on a hung Redis: err=%v after %v, want ErrUnavailable within %v", name, err, elapsed, storeBudget)
		}
		t.Logf("%s on a hung established connection: ErrUnavailable after %v", name, elapsed.Round(10*time.Millisecond))
	}
}

// 端到端（與 cmd/api/main.go 相同的掛載鏈：RequireAuth → RateLimit → 熱路徑 handler）：
// Redis 卡死時 /pos 與 /leave 在 ≈ 1 s（限流 fail-open）+ ≈ 2 s（Store 逾時）內答 503 redis_unavailable。
func TestPosAndLeaveAnswer503QuicklyThroughRateLimitWhenRedisIsDown(t *testing.T) {
	const budget = 3500 * time.Millisecond // 1 s + 2 s + 餘裕
	a := newPlayer("A")
	body := `{"pv":1,"sid":"` + a.sid + `","p":{"la":25.03321,"ln":121.56543,"ac":8,"fa":2}}`

	for shape, addr := range map[string]string{
		"blackhole": redistest.Blackhole(t),
		"refused":   redistest.Refused(t),
	} {
		shape, addr := shape, addr
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			rdb := newDefaultClient(t, addr) // 限流與 Store 共用同一個 client，與 main.go 一致
			root := chi.NewRouter()
			root.Group(func(r chi.Router) {
				r.Use(fakeRequireAuth)
				r.With(middleware.RateLimit(rdb, "runmeet_live_pos", 60, time.Minute, middleware.UserOrIP)).
					Mount("/run-meet-live", NewHandler(rdb).Router())
			})
			e := &testEnv{} // 只借用 request()：與 routeorder_test 相同的送請求方式

			for _, op := range []string{"pos", "leave"} {
				start := time.Now()
				rec := e.request(root, "/run-meet-live/"+testMeet+"/"+op, a.uid, body)
				elapsed := time.Since(start)
				if rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "redis_unavailable" {
					t.Fatalf("%s/%s: %d %s, want 503 redis_unavailable", shape, op, rec.Code, rec.Body.String())
				}
				if elapsed > budget {
					t.Fatalf("%s/%s answered after %v, want ≤ %v (the old behaviour was ~30 s)", shape, op, elapsed, budget)
				}
				t.Logf("%s Redis, POST /run-meet-live/{id}/%s through RequireAuth→RateLimit→handler: 503 redis_unavailable after %v",
					shape, op, elapsed.Round(10*time.Millisecond))
			}
		})
	}
}
