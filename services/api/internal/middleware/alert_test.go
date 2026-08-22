package middleware

import (
	"testing"
	"time"
)

// newTestWindow 建立一個可控時間的 FiveXXWindow：now 由呼叫端持有的 *time.Time 指標推進，
// 避免依賴真實 time.Sleep 造成測試時序不確定性。
func newTestWindow(threshold int, window time.Duration, clock *time.Time) *FiveXXWindow {
	w := NewFiveXXWindow(threshold, window)
	w.now = func() time.Time { return *clock }
	return w
}

func TestFiveXXWindow_BelowThreshold_NeverTriggers(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := newTestWindow(5, 5*time.Minute, &clock)

	for i := 0; i < 4; i++ {
		triggered, count, _ := w.Record("GET", "/api/v1/foo", 500)
		if triggered {
			t.Fatalf("call %d: expected no trigger below threshold, got triggered (count=%d)", i+1, count)
		}
	}
}

func TestFiveXXWindow_ReachesThreshold_TriggersOnceWithCountAndSample(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := newTestWindow(5, 5*time.Minute, &clock)

	for i := 0; i < 4; i++ {
		triggered, _, _ := w.Record("GET", "/api/v1/foo", 500)
		if triggered {
			t.Fatalf("call %d: unexpected early trigger", i+1)
		}
	}

	triggered, count, sample := w.Record("POST", "/api/v1/bar", 503)
	if !triggered {
		t.Fatal("expected 5th call to trigger")
	}
	if count != 5 {
		t.Fatalf("expected count=5 at trigger, got %d", count)
	}
	want := "POST /api/v1/bar 503"
	if sample != want {
		t.Fatalf("expected sample %q, got %q", want, sample)
	}
}

func TestFiveXXWindow_ResetsAfterTrigger_DoesNotImmediatelyRetrigger(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := newTestWindow(3, 5*time.Minute, &clock)

	// 累積到 threshold，觸發一次。
	for i := 0; i < 2; i++ {
		w.Record("GET", "/x", 500)
	}
	triggered, _, _ := w.Record("GET", "/x", 500)
	if !triggered {
		t.Fatal("expected trigger on 3rd call")
	}

	// 觸發後緊接著再呼叫一次（同一時刻，窗口內），不應該立即再次觸發——應該是重置後的第 1 次計數。
	triggered, count, _ := w.Record("GET", "/x", 500)
	if triggered {
		t.Fatal("expected no immediate re-trigger right after a trigger")
	}
	if count != 1 {
		t.Fatalf("expected count=1 (fresh window after trigger), got %d", count)
	}
}

func TestFiveXXWindow_CallsBeyondWindow_DoNotAccumulateAcrossBoundary(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := newTestWindow(5, 5*time.Minute, &clock)

	// t=0: 第一筆
	triggered, count, _ := w.Record("GET", "/x", 500)
	if triggered {
		t.Fatal("unexpected trigger on 1st call")
	}
	if count != 1 {
		t.Fatalf("expected count=1, got %d", count)
	}

	// t=6min（超出 5 分鐘窗口）：應該視為新窗口重新從 1 起算，而不是累積成 2。
	clock = clock.Add(6 * time.Minute)
	triggered, count, _ = w.Record("GET", "/x", 500)
	if triggered {
		t.Fatal("unexpected trigger after window reset (only 1 call in new window)")
	}
	if count != 1 {
		t.Fatalf("expected count reset to 1 after window elapsed, got %d", count)
	}
}

func TestFiveXXWindow_WithinWindow_Accumulates(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := newTestWindow(5, 5*time.Minute, &clock)

	w.Record("GET", "/x", 500)
	clock = clock.Add(1 * time.Minute)
	w.Record("GET", "/x", 500)
	clock = clock.Add(1 * time.Minute)
	triggered, count, _ := w.Record("GET", "/x", 500)

	if triggered {
		t.Fatal("unexpected trigger before threshold reached")
	}
	if count != 3 {
		t.Fatalf("expected count=3 (accumulated within window), got %d", count)
	}
}

func TestFiveXXWindow_ConcurrentRecord_NoRace(t *testing.T) {
	// 併發呼叫下不應該 panic 或 race（用真實時間，不需要精確時序斷言，主要靠 `go test -race` 驗證）。
	w := NewFiveXXWindow(1000000, time.Minute)

	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			for j := 0; j < 50; j++ {
				w.Record("GET", "/x", 500)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
}
