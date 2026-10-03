package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/cache/redistest"
)

// bounded_test.go：證明 Bounded 的逾時是「真的」（socket 層），而且證明「只給 context 期限」不是。

// 前提測試：go-redis v9 的 context 期限**管不到** socket 讀取。這是整個 Bounded 存在的理由——
// 如果哪天 go-redis 改成預設就尊重期限，這個測試會失敗，提醒我們這層可以拿掉。
func TestPlainContextDeadlineDoesNotBoundSocketReads(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{
		Addr: redistest.Blackhole(t), ReadTimeout: 1200 * time.Millisecond, MaxRetries: -1,
	})
	defer rdb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := rdb.Ping(ctx).Err()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a blackholed Redis must not answer")
	}
	if elapsed < 800*time.Millisecond {
		t.Fatalf("plain client returned after %v with a 150 ms context deadline — go-redis now honours context deadlines "+
			"for socket reads by default; Bounded may no longer be necessary", elapsed)
	}
	t.Logf("plain client + 150ms ctx deadline against a blackhole: returned after %v (ReadTimeout 1.2s) — the deadline was NOT honoured", elapsed.Round(10*time.Millisecond))
}

// 新連線（撥號成功、握手 HELLO 永遠沒有回應）：以 go-redis 的**預設**選項（ReadTimeout 5 s、MaxRetries 3）
// 建 client，Bounded 仍在 ≈D 內失敗，而不是預設的 5 秒（新連線）／10 秒（已建立的連線卡死）。
func TestBoundedFailsFastOnBlackholeWithDefaultClientOptions(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: redistest.Blackhole(t)})
	defer rdb.Close()
	b := NewBounded(rdb, 300*time.Millisecond)

	ctx, cancel := b.Ctx(context.Background())
	defer cancel()
	start := time.Now()
	err := b.C.Ping(ctx).Err()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a blackholed Redis must not answer")
	}
	if elapsed < 200*time.Millisecond || elapsed > 1200*time.Millisecond {
		t.Fatalf("Bounded(300ms) took %v against a blackhole; want ≈300ms (and far below go-redis's default 5s)", elapsed)
	}
	t.Logf("Bounded(300ms) against a blackhole (default client options): failed after %v", elapsed.Round(10*time.Millisecond))
}

// 已建立的連線上 Redis 中途卡死（docker pause／網路分區的真實形狀）：連線在連線池裡、寫入成功、
// 讀取永遠沒有回應。這是 context-only 做法抓不到的情況。
func TestBoundedFailsFastWhenRedisHangsMidSession(t *testing.T) {
	mr := miniredis.RunT(t)
	proxy := redistest.NewProxy(t, mr.Addr())
	rdb := redis.NewClient(&redis.Options{Addr: proxy.Addr()})
	defer rdb.Close()
	b := NewBounded(rdb, 300*time.Millisecond)

	ctx, cancel := b.Ctx(context.Background())
	if err := b.C.Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatalf("healthy Set: %v", err)
	}
	if got, err := b.C.Get(ctx, "k").Result(); err != nil || got != "v" {
		t.Fatalf("healthy Get: %q %v", got, err)
	}
	cancel()
	// 與原 client 共用同一個連線池：兩個指令只開了一條連線，且原 client 看得到它
	if st := rdb.PoolStats(); st.TotalConns != 1 {
		t.Fatalf("pool TotalConns = %d, want 1 (the bounded view must share the original client's pool)", st.TotalConns)
	}

	proxy.Blackhole() // Redis 卡死：連線還在、不再回話

	ctx2, cancel2 := b.Ctx(context.Background())
	defer cancel2()
	start := time.Now()
	err := b.C.Get(ctx2, "k").Err()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a hung Redis must not answer")
	}
	if elapsed > 1200*time.Millisecond {
		t.Fatalf("Bounded(300ms) took %v on a hung established connection; want ≈300ms", elapsed)
	}
	t.Logf("Bounded(300ms) on an established connection to a hung Redis: failed after %v", elapsed.Round(10*time.Millisecond))
}

// 連線被拒：撥號立刻失敗，go-redis 自己的重試在期限內結束。
func TestBoundedRefusedFailsFast(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: redistest.Refused(t)})
	defer rdb.Close()
	b := NewBounded(rdb, time.Second)

	ctx, cancel := b.Ctx(context.Background())
	defer cancel()
	start := time.Now()
	err := b.C.Ping(ctx).Err()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("nothing is listening; Ping must fail")
	}
	if elapsed > time.Second+300*time.Millisecond {
		t.Fatalf("connection refused took %v, want ≤ ~1s", elapsed)
	}
	t.Logf("Bounded(1s) against a refused address: failed after %v", elapsed.Round(10*time.Millisecond))
}

func TestBoundedHealthyPathAndNil(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	b := NewBounded(rdb, time.Second)
	ctx, cancel := b.Ctx(context.Background())
	defer cancel()
	if err := b.C.Set(ctx, "a", "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if v, _ := mr.Get("a"); v != "1" {
		t.Fatalf("value written through the bounded view = %q", v)
	}
	if NewBounded(nil, time.Second) != nil {
		t.Fatal("NewBounded(nil) must be nil so callers can treat it as 'Redis unavailable'")
	}
}

func TestBoundedCtxHonoursAnEarlierParentDeadline(t *testing.T) {
	b := &Bounded{D: 2 * time.Second}
	parent, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx, cancel2 := b.Ctx(parent)
	defer cancel2()
	dl, ok := ctx.Deadline()
	if !ok || time.Until(dl) > 100*time.Millisecond {
		t.Fatalf("child deadline in %v, want ≤ the parent's 50ms", time.Until(dl))
	}
	// 沒有期限的 parent：期限就是 D
	ctx3, cancel3 := b.Ctx(context.Background())
	defer cancel3()
	if dl, ok := ctx3.Deadline(); !ok || time.Until(dl) > 2*time.Second || time.Until(dl) < time.Second {
		t.Fatalf("child deadline in %v, want ≈2s", time.Until(dl))
	}
}
