package main

import (
	"testing"
	"time"
)

// TestShouldRecompute 驗證 recomputeThrottle 節流判斷的純函式邏輯（見 main.go 的 shouldRecompute）。
func TestShouldRecompute(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		last        time.Time
		now         time.Time
		minInterval time.Duration
		want        bool
	}{
		{
			name:        "zero-value last (尚未執行過) 一律應該執行",
			last:        time.Time{},
			now:         now,
			minInterval: 60 * time.Second,
			want:        true,
		},
		{
			name:        "距離上次執行剛好等於門檻 → 應該執行",
			last:        now.Add(-60 * time.Second),
			now:         now,
			minInterval: 60 * time.Second,
			want:        true,
		},
		{
			name:        "距離上次執行超過門檻 → 應該執行",
			last:        now.Add(-90 * time.Second),
			now:         now,
			minInterval: 60 * time.Second,
			want:        true,
		},
		{
			name:        "距離上次執行不到門檻 → 應該跳過",
			last:        now.Add(-30 * time.Second),
			now:         now,
			minInterval: 60 * time.Second,
			want:        false,
		},
		{
			name:        "距離上次執行只差 1 毫秒不到門檻 → 應該跳過",
			last:        now.Add(-60*time.Second + time.Millisecond),
			now:         now,
			minInterval: 60 * time.Second,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRecompute(tt.last, tt.now, tt.minInterval)
			if got != tt.want {
				t.Errorf("shouldRecompute(%v, %v, %v) = %v, want %v", tt.last, tt.now, tt.minInterval, got, tt.want)
			}
		})
	}
}
