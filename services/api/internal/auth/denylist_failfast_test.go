package auth

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/cache/redistest"
)

// denylist_failfast_test.go：checkDenylist 的「300ms 獨立上限」必須是真的。
//
// 2026-10-02 發現：這條查詢原本只用 context.WithTimeout(ctx, 300ms) 包住 rdb.Exists——但 go-redis v9 不會
// 因為 context 期限而中斷 socket 讀取，Redis 卡死時實際會等一整個 ReadTimeout（預設 5 秒）。而這條查詢在
// 「每一個帶 token 的請求」上（RequireAuth），所以 Redis 一卡死，全站每個已登入請求都多卡 5 秒。
// 現在改用 cache.Bounded（與連線池共用、讀寫逾時 = 300ms），見 cache/bounded.go 檔頭。
//
// 一律用 go-redis 的預設選項建 client（正式環境的形狀）。

func denylistTestService(rdb *redis.Client) *Service {
	return NewService(nil, rdb, "test-jwt-secret-please-ignore", time.Hour, 720*time.Hour, "")
}

func claimsFor(role string) *Claims {
	return &Claims{UserID: "u1", Role: role, RegisteredClaims: jwt.RegisteredClaims{ID: "jti-failfast"}}
}

func TestCheckDenylistIsBoundedAt300msWhenRedisIsDown(t *testing.T) {
	const budget = 800 * time.Millisecond // 300ms 期限 + 餘裕（修法前：卡死時 ≈ 5 s）
	for shape, addr := range map[string]string{
		"blackhole": redistest.Blackhole(t),
		"refused":   redistest.Refused(t),
	} {
		shape, addr := shape, addr
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			rdb := redis.NewClient(&redis.Options{Addr: addr})
			defer rdb.Close()
			s := denylistTestService(rdb)

			// 一般會員：fail-open（查不到／逾時一律放行）
			start := time.Now()
			err := s.checkDenylist(context.Background(), claimsFor("user"), "tok")
			elapsed := time.Since(start)
			if err != nil {
				t.Fatalf("user: err = %v, want nil (fail-open)", err)
			}
			if elapsed > budget {
				t.Fatalf("user: checkDenylist took %v on a %s Redis, want ≤ %v", elapsed, shape, budget)
			}
			t.Logf("%s Redis, user token: fail-open after %v", shape, elapsed.Round(10*time.Millisecond))

			// admin：fail-closed（ErrAuthUnavailable → RequireAuth 映射成 503）
			start = time.Now()
			err = s.checkDenylist(context.Background(), claimsFor("admin"), "tok")
			elapsed = time.Since(start)
			if err != ErrAuthUnavailable {
				t.Fatalf("admin: err = %v, want ErrAuthUnavailable (fail-closed)", err)
			}
			if elapsed > budget {
				t.Fatalf("admin: checkDenylist took %v on a %s Redis, want ≤ %v", elapsed, shape, budget)
			}
			t.Logf("%s Redis, admin token: fail-closed (ErrAuthUnavailable) after %v", shape, elapsed.Round(10*time.Millisecond))
		})
	}
}

// 沒有退化：Redis 健康時，撤銷名單命中仍回 ErrTokenInvalid、沒命中回 nil。
func TestCheckDenylistStillHonoursTheDenylistWithHealthyRedis(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	s := denylistTestService(rdb)
	c := claimsFor("user")

	if err := s.checkDenylist(context.Background(), c, "tok"); err != nil {
		t.Fatalf("not denylisted: %v", err)
	}
	if err := mr.Set(revocationKey(c, "tok"), "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.checkDenylist(context.Background(), c, "tok"); err != ErrTokenInvalid {
		t.Fatalf("denylisted: err = %v, want ErrTokenInvalid", err)
	}
}
