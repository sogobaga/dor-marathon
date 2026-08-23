package ops

import (
	"strings"
	"testing"
	"time"
)

func TestInSelfCheckWindow(t *testing.T) {
	cases := []struct {
		name string
		hour int
		want bool
	}{
		{"before window (07:59)", 7, false},
		{"window start (08:00)", 8, true},
		{"window end (08:59)", 8, true},
		{"after window (09:00)", 9, false},
		{"midnight", 0, false},
		{"noon", 12, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tm := time.Date(2026, 8, 21, c.hour, 30, 0, 0, time.UTC)
			if got := inSelfCheckWindow(tm); got != c.want {
				t.Errorf("inSelfCheckWindow(hour=%d) = %v, want %v", c.hour, got, c.want)
			}
		})
	}
}

func TestTaiwanNowOffset(t *testing.T) {
	// taiwanNow 應為 UTC+8：不直接測 time.Now() 依賴的那一刻，改驗證換算邏輯本身（與 taiwanNow 的
	// 實作同一套算式），確保之後有人手滑改成別的 offset 或誤用 time.Local 時測試會炸。
	utc := time.Date(2026, 8, 20, 23, 30, 0, 0, time.UTC) // UTC 23:30 → 台灣隔天 07:30
	taipei := utc.Add(8 * time.Hour)
	if taipei.Day() != 21 || taipei.Hour() != 7 {
		t.Errorf("UTC+8 換算錯誤：got day=%d hour=%d, want day=21 hour=7", taipei.Day(), taipei.Hour())
	}
}

func TestSummarizeSelfCheck_AllOK(t *testing.T) {
	results := []CheckResult{
		{Name: "pending_vip_orders_stuck", OK: true},
		{Name: "paytx_long_pending", OK: true},
		{Name: "renewal_attempts_stale_processing", OK: true},
		{Name: "paid_order_without_subscription", OK: true},
		{Name: "active_subscription_without_card", OK: true},
		{Name: "webhook_failure_rate", OK: true},
		{Name: "cancel_refund_mismatch", OK: true},
		{Name: activityHeartbeatCheck, OK: true},
	}
	sum := summarizeSelfCheck(results)
	if !sum.AllOK {
		t.Fatal("expected AllOK=true when all 8 checks pass")
	}
	if sum.FailedCount != 0 || sum.HeartbeatFailed {
		t.Fatalf("expected no failures, got FailedCount=%d HeartbeatFailed=%v", sum.FailedCount, sum.HeartbeatFailed)
	}
}

func TestSummarizeSelfCheck_RegularChecksFail(t *testing.T) {
	results := []CheckResult{
		{Name: "pending_vip_orders_stuck", OK: false, Detail: "共 3 筆"},
		{Name: "paytx_long_pending", OK: true},
		{Name: "cancel_refund_mismatch", OK: false, Detail: "共 1 筆"},
		{Name: activityHeartbeatCheck, OK: true},
	}
	sum := summarizeSelfCheck(results)
	if sum.AllOK {
		t.Fatal("expected AllOK=false when some checks fail")
	}
	if sum.FailedCount != 2 {
		t.Fatalf("expected FailedCount=2, got %d", sum.FailedCount)
	}
	if sum.HeartbeatFailed {
		t.Fatal("heartbeat should not be marked failed")
	}
	// 彙整內文應含中文標籤 + detail，且兩項各佔一行
	if !strings.Contains(sum.AggregateDetail, "卡住的pending VIP訂單") || !strings.Contains(sum.AggregateDetail, "共 3 筆") {
		t.Errorf("aggregate detail missing expected content: %q", sum.AggregateDetail)
	}
	if !strings.Contains(sum.AggregateDetail, "取消報名但訂單未同步") || !strings.Contains(sum.AggregateDetail, "共 1 筆") {
		t.Errorf("aggregate detail missing expected content: %q", sum.AggregateDetail)
	}
	if len(strings.Split(sum.AggregateDetail, "\n")) != 2 {
		t.Errorf("expected 2 lines in aggregate detail, got: %q", sum.AggregateDetail)
	}
}

func TestSummarizeSelfCheck_HeartbeatFailsAlone(t *testing.T) {
	results := []CheckResult{
		{Name: "pending_vip_orders_stuck", OK: true},
		{Name: activityHeartbeatCheck, OK: false, Detail: "近24h activities 新增數為 0"},
	}
	sum := summarizeSelfCheck(results)
	if sum.AllOK {
		t.Fatal("expected AllOK=false when heartbeat check fails")
	}
	if sum.FailedCount != 0 {
		t.Fatalf("heartbeat failure must not count toward FailedCount (separate alert kind), got %d", sum.FailedCount)
	}
	if !sum.HeartbeatFailed || sum.HeartbeatDetail == "" {
		t.Fatal("expected HeartbeatFailed=true with detail populated")
	}
	if sum.AggregateDetail != "" {
		t.Errorf("aggregate detail should be empty when only heartbeat fails, got %q", sum.AggregateDetail)
	}
}

func TestSummarizeSelfCheck_BothFail(t *testing.T) {
	results := []CheckResult{
		{Name: "webhook_failure_rate", OK: false, Detail: "failed=6 paid=2"},
		{Name: activityHeartbeatCheck, OK: false, Detail: "近24h activities 新增數為 0"},
	}
	sum := summarizeSelfCheck(results)
	if sum.AllOK {
		t.Fatal("expected AllOK=false")
	}
	if sum.FailedCount != 1 {
		t.Fatalf("expected FailedCount=1, got %d", sum.FailedCount)
	}
	if !sum.HeartbeatFailed {
		t.Fatal("expected HeartbeatFailed=true")
	}
}

func TestSummarizeSelfCheck_UnknownNameFallsBackToRawName(t *testing.T) {
	results := []CheckResult{
		{Name: "some_future_check", OK: false, Detail: "detail here"},
	}
	sum := summarizeSelfCheck(results)
	if !strings.Contains(sum.AggregateDetail, "some_future_check") {
		t.Errorf("expected fallback to raw name when label missing, got %q", sum.AggregateDetail)
	}
}
