package race

import (
	"testing"
	"time"
)

// 2026-09-03 事故重現：8/29 16:01 台北 3.31km（08:01Z）＋ 8/30 06:52 台北 12.41km（8/29 22:52Z）。
// 以 UTC 分桶會把兩筆併成同一天（15.72），台北日曆日應分成兩天、最佳單日 12.41。
func TestMetricValue_DailyDistanceUsesTaipeiDay(t *testing.T) {
	acts := []progAct{
		{At: time.Date(2026, 8, 29, 8, 1, 42, 0, time.UTC), Dist: 3.31},
		{At: time.Date(2026, 8, 29, 22, 52, 32, 0, time.UTC), Dist: 12.41},
		{At: time.Date(2026, 8, 30, 22, 1, 33, 0, time.UTC), Dist: 4.12},  // 8/31 06:01 台北
		{At: time.Date(2026, 8, 31, 14, 13, 59, 0, time.UTC), Dist: 0.01}, // 8/31 22:13 台北
	}
	if got := metricValue(acts, "daily_distance"); got != 12.41 {
		t.Fatalf("daily_distance = %v, want 12.41 (台北單日最佳)", got)
	}
	// 8/29、8/30、8/31 台北三天連續
	if got := metricValue(acts, "streak_days"); got != 3 {
		t.Fatalf("streak_days = %v, want 3", got)
	}
}

// 週分桶同樣用台北：8/30（日）22:52Z 其實是 8/31（一）？——不是：8/30 22:52Z = 8/31 06:52 台北，屬 ISO 週一新的一週。
func TestMetricValue_WeeklyDistanceUsesTaipeiWeek(t *testing.T) {
	acts := []progAct{
		{At: time.Date(2026, 8, 30, 8, 0, 0, 0, time.UTC), Dist: 5},   // 8/30 16:00 台北（週日，W35）
		{At: time.Date(2026, 8, 30, 22, 52, 0, 0, time.UTC), Dist: 7}, // 8/31 06:52 台北（週一，W36）
		{At: time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC), Dist: 2},    // 9/1 09:00 台北（W36）
	}
	if got := metricValue(acts, "weekly_distance"); got != 9 {
		t.Fatalf("weekly_distance = %v, want 9 (W36 台北 7+2)", got)
	}
}
