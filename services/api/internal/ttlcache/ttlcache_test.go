package ttlcache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---- expired() 純函式：TTL 判斷邏輯 ----

func TestExpired(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		valid bool
		at    time.Time
		ttl   time.Duration
		want  bool
	}{
		{"從未成功載入過恆過期", false, time.Time{}, time.Minute, true},
		{"從未成功載入過即使ttl<=0也過期", false, time.Time{}, 0, true},
		{"剛載入未過期", true, now, time.Minute, false},
		{"剛好達到ttl邊界視為過期", true, now.Add(-time.Minute), time.Minute, true},
		{"未達ttl邊界前一秒不過期", true, now.Add(-time.Minute + time.Second), time.Minute, false},
		{"ttl<=0且曾成功載入過永不過期", true, now.Add(-999 * time.Hour), 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := expired(tc.valid, tc.at, now, tc.ttl); got != tc.want {
				t.Errorf("expired(valid=%v, at=%v, now=%v, ttl=%v) = %v, want %v",
					tc.valid, tc.at, now, tc.ttl, got, tc.want)
			}
		})
	}
}

// ---- Cache[T].Get：首次同步載入 ----

func TestCache_Get_FirstLoadIsSynchronous(t *testing.T) {
	var calls int32
	c := New(time.Minute, func(ctx context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 7, nil
	})
	v := c.Get(context.Background())
	if v != 7 {
		t.Fatalf("Get() = %d, want 7", v)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("load called %d times, want 1", got)
	}
	// 命中快取：不應再觸發 load。
	v2 := c.Get(context.Background())
	if v2 != 7 {
		t.Fatalf("second Get() = %d, want 7", v2)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("load called %d times after cache hit, want still 1", got)
	}
}

// ---- Cache[T].Get：過期後回舊值＋背景刷新（stale-while-revalidate） ----

func TestCache_Get_StaleWhileRevalidate(t *testing.T) {
	var calls int32
	refreshed := make(chan struct{}, 1)
	c := New(time.Nanosecond, func(ctx context.Context) (int32, error) {
		v := atomic.AddInt32(&calls, 1)
		if v > 1 {
			refreshed <- struct{}{}
		}
		return v, nil
	})

	first := c.Get(context.Background())
	if first != 1 {
		t.Fatalf("first Get() = %d, want 1", first)
	}
	time.Sleep(2 * time.Millisecond) // 確保已超過 1ns 的 TTL

	second := c.Get(context.Background())
	if second != 1 {
		t.Fatalf("second Get() = %d, want stale value 1", second)
	}

	select {
	case <-refreshed:
	case <-time.After(2 * time.Second):
		t.Fatal("背景刷新逾時未發生")
	}

	third := c.Get(context.Background())
	if third != 2 {
		t.Fatalf("third Get() = %d, want refreshed value 2", third)
	}
}

// ---- Cache[T].Refresh：singleflight，並發呼叫只觸發一次 load ----

func TestCache_Refresh_Singleflight(t *testing.T) {
	var calls int32
	block := make(chan struct{})
	c := New(time.Minute, func(ctx context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		<-block
		return 42, nil
	})

	const n = 5
	results := make([]int, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i] = c.Refresh(context.Background())
		}(i)
	}
	time.Sleep(50 * time.Millisecond) // 讓 n 個 goroutine 都進入 Refresh 並卡在 <-block
	close(block)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("load called %d times, want exactly 1 (singleflight)", got)
	}
	for i, r := range results {
		if r != 42 {
			t.Errorf("results[%d] = %d, want 42", i, r)
		}
	}
}

// ---- Cache[T].Refresh：查詢失敗保留舊值（優雅降級） ----

func TestCache_Refresh_ErrorKeepsStaleValue(t *testing.T) {
	ok := true
	c := New(time.Minute, func(ctx context.Context) (int, error) {
		if ok {
			return 7, nil
		}
		return 0, errors.New("boom")
	})
	if v := c.Refresh(context.Background()); v != 7 {
		t.Fatalf("first Refresh() = %d, want 7", v)
	}
	ok = false
	if v := c.Refresh(context.Background()); v != 7 {
		t.Fatalf("Refresh() after load error = %d, want stale 7", v)
	}
}

// ---- Cache[T].Refresh：從未成功載入過且查詢失敗，回 zero value ----

func TestCache_Refresh_NeverLoadedAndErrorReturnsZeroValue(t *testing.T) {
	c := New(time.Minute, func(ctx context.Context) (int, error) {
		return 0, errors.New("boom")
	})
	if v := c.Refresh(context.Background()); v != 0 {
		t.Fatalf("Refresh() = %d, want zero value 0", v)
	}
	if _, _, valid := c.Peek(); valid {
		t.Fatal("Peek().valid = true after a failed first load, want false")
	}
}

// ---- Cache[T].Invalidate：不清空舊值，但標記過期 ----

func TestCache_Invalidate_KeepsStaleValueButMarksExpired(t *testing.T) {
	c := New(time.Hour, func(ctx context.Context) (int, error) {
		return 9, nil
	})
	c.Get(context.Background()) // 首次同步載入

	c.Invalidate()

	v, at, valid := c.Peek()
	if v != 9 {
		t.Fatalf("Peek() value = %d, want stale value 9 preserved", v)
	}
	if !valid {
		t.Fatal("Peek().valid = false after Invalidate, want true (舊值應保留)")
	}
	if !at.IsZero() {
		t.Fatalf("Peek() at = %v, want zero time after Invalidate", at)
	}
}

func TestCache_Invalidate_NextGetTriggersBackgroundRefresh(t *testing.T) {
	var calls int32
	refreshed := make(chan struct{}, 1)
	c := New(time.Hour, func(ctx context.Context) (int32, error) {
		v := atomic.AddInt32(&calls, 1)
		if v > 1 {
			refreshed <- struct{}{}
		}
		return v, nil
	})
	c.Get(context.Background()) // calls=1

	c.Invalidate()

	stale := c.Get(context.Background()) // 應立刻回舊值 1，背景觸發第 2 次 load
	if stale != 1 {
		t.Fatalf("Get() right after Invalidate = %d, want stale 1", stale)
	}

	select {
	case <-refreshed:
	case <-time.After(2 * time.Second):
		t.Fatal("Invalidate 後背景刷新逾時未發生")
	}
	if v, _, _ := c.Peek(); v != 2 {
		t.Fatalf("value after background refresh = %d, want 2", v)
	}
}

// ---- Cache[T].Peek：不觸發任何刷新 ----

func TestCache_Peek_NeverTriggersLoad(t *testing.T) {
	var calls int32
	c := New(time.Nanosecond, func(ctx context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 1, nil
	})
	// 從未 Get 過：Peek 前 calls 必須是 0。
	if _, _, valid := c.Peek(); valid {
		t.Fatal("Peek().valid = true before any Get, want false")
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("load called %d times via bare Peek(), want 0", got)
	}
}
