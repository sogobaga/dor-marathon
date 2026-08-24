package middleware

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestTaiwanDay(t *testing.T) {
	// UTC 23:30 → 台灣隔天 07:30 → 台灣日期為隔天
	utc := time.Date(2026, 8, 20, 23, 30, 0, 0, time.UTC)
	if got := taiwanDay(utc); got != "2026-08-21" {
		t.Errorf("taiwanDay = %q, want 2026-08-21", got)
	}
}

func TestIPDailyAggregate_Record_AccumulatesPerIP(t *testing.T) {
	a := NewIPDailyAggregate(nil)
	now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC) // 台灣 09:00

	a.Record(now, "1.1.1.1", "TW", http.StatusOK)
	a.Record(now, "1.1.1.1", "TW", http.StatusOK)
	a.Record(now, "2.2.2.2", "US", http.StatusOK)

	if len(a.data) != 2 {
		t.Fatalf("expected 2 distinct IPs in data, got %d", len(a.data))
	}
	if a.data["1.1.1.1"].requests != 2 {
		t.Errorf("expected 1.1.1.1 requests=2, got %d", a.data["1.1.1.1"].requests)
	}
	if a.data["2.2.2.2"].requests != 1 {
		t.Errorf("expected 2.2.2.2 requests=1, got %d", a.data["2.2.2.2"].requests)
	}
	if a.data["2.2.2.2"].country != "US" {
		t.Errorf("expected country US, got %q", a.data["2.2.2.2"].country)
	}
}

func TestIPDailyAggregate_Record_CountsAuthFailsAndNotFound(t *testing.T) {
	a := NewIPDailyAggregate(nil)
	now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)

	a.Record(now, "1.1.1.1", "TW", http.StatusUnauthorized)
	a.Record(now, "1.1.1.1", "TW", http.StatusForbidden)
	a.Record(now, "1.1.1.1", "TW", http.StatusNotFound)
	a.Record(now, "1.1.1.1", "TW", http.StatusOK)

	c := a.data["1.1.1.1"]
	if c.requests != 4 {
		t.Errorf("expected requests=4, got %d", c.requests)
	}
	if c.authFails != 2 {
		t.Errorf("expected authFails=2 (401+403), got %d", c.authFails)
	}
	if c.notFound != 1 {
		t.Errorf("expected notFound=1, got %d", c.notFound)
	}
}

func TestIPDailyAggregate_Record_CapsUniqueIPsPerDay(t *testing.T) {
	a := NewIPDailyAggregate(nil)
	now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)

	// 先塞滿到上限（用小上限測試不現實，這裡直接操弄 knownIPs 模擬「已達上限」狀態，
	// 避免測試真的迴圈 5 萬次拖慢測試）。
	a.mu.Lock()
	a.day = taiwanDay(now)
	for i := 0; i < ipDailyMaxUniqueIPs; i++ {
		a.knownIPs[padIP(i)] = struct{}{}
	}
	a.mu.Unlock()

	// 已知 IP：仍應被計入（不受上限影響）。
	known := padIP(0)
	a.Record(now, known, "TW", http.StatusOK)
	if a.data[known] == nil || a.data[known].requests != 1 {
		t.Fatalf("expected known IP to still be recorded after cap reached")
	}

	// 全新 IP：上限已滿，應被丟棄、不進入 data，且 capped 旗標應為 true。
	a.Record(now, "9.9.9.9", "TW", http.StatusOK)
	if _, ok := a.data["9.9.9.9"]; ok {
		t.Errorf("expected brand-new IP to be dropped once daily cap reached")
	}
	a.mu.Lock()
	capped := a.capped
	a.mu.Unlock()
	if !capped {
		t.Errorf("expected capped=true once ipDailyMaxUniqueIPs reached")
	}
}

func TestIPDailyAggregate_Record_DayRolloverResetsCapAndKnownIPs(t *testing.T) {
	a := NewIPDailyAggregate(nil)
	day1 := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC) // 台灣 08/21 09:00
	day2 := time.Date(2026, 8, 22, 1, 0, 0, 0, time.UTC) // 台灣 08/22 09:00

	a.Record(day1, "1.1.1.1", "TW", http.StatusOK)
	a.mu.Lock()
	a.capped = true // 模擬前一天已達上限
	a.mu.Unlock()

	a.Record(day2, "1.1.1.1", "TW", http.StatusOK)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.day != taiwanDay(day2) {
		t.Errorf("expected day to roll over to %q, got %q", taiwanDay(day2), a.day)
	}
	if a.capped {
		t.Error("expected capped flag reset to false on new day")
	}
	if len(a.knownIPs) != 1 {
		t.Errorf("expected knownIPs reset to contain only today's 1 IP, got %d", len(a.knownIPs))
	}
	if a.data["1.1.1.1"].requests != 1 {
		t.Errorf("expected fresh per-day count of 1 after rollover, got %d", a.data["1.1.1.1"].requests)
	}
}

// padIP 產生測試用的相異假 IP 字串（不需要是合法 IP 格式，Record 只把它當 map key 用）。
func padIP(i int) string {
	return "test-ip-" + strconv.Itoa(i)
}
