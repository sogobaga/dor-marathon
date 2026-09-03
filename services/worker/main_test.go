package main

import (
	"strings"
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

// --- 2026-09-03 owner 回收決策：overlap 查詢 NOT IN(...) 清單的純函式測試 ---
//
// benignReasonsSQLIn 把 benignFlagReasons map 轉成 SQL IN(...) 用的逗號分隔清單，供
// awardMileageDedup 的 overlap 查詢排除「非良性標記已發放列」（見 main.go 該函式與
// benignReasonsSQLIn 的註解）。這裡驗證：清單內容跟 benignFlagReasons 完全一致、每個 key 都正確
// 加上單引號、輸出結果穩定（sort 過，不受 map 疊代順序影響）——與
// internal/integration/mileage_exp_test.go 的同名測試對照，兩邊各自維護一份實作但行為必須一致。

func TestBenignReasonsSQLIn_ContainsExactlyMapKeys(t *testing.T) {
	got := benignReasonsSQLIn(benignFlagReasons)
	parts := strings.Split(got, ",")
	if len(parts) != len(benignFlagReasons) {
		t.Fatalf("benignReasonsSQLIn produced %d entries, want %d (from map): %q", len(parts), len(benignFlagReasons), got)
	}
	for _, p := range parts {
		if len(p) < 2 || p[0] != '\'' || p[len(p)-1] != '\'' {
			t.Fatalf("entry %q is not single-quoted", p)
		}
		key := p[1 : len(p)-1]
		if !benignFlagReasons[key] {
			t.Fatalf("entry %q (key=%q) is not a real benignFlagReasons key", p, key)
		}
	}
	for key := range benignFlagReasons {
		if !strings.Contains(got, "'"+key+"'") {
			t.Fatalf("benignReasonsSQLIn missing key %q, got %q", key, got)
		}
	}
}

func TestBenignReasonsSQLIn_StableOrder(t *testing.T) {
	a := benignReasonsSQLIn(benignFlagReasons)
	b := benignReasonsSQLIn(benignFlagReasons)
	if a != b {
		t.Fatalf("benignReasonsSQLIn is not stable across calls: %q vs %q", a, b)
	}
}

func TestBenignReasonsSQLIn_EmptyMap(t *testing.T) {
	if got := benignReasonsSQLIn(map[string]bool{}); got != "" {
		t.Fatalf("empty map should produce empty string, got %q", got)
	}
}
