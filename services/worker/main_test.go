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

// --- H5（2026-09-07 audit）：readAndProcessRound 只 XReadGroup '>' + XAck，處理失敗的訊息永遠
// 卡在 PEL、從不重試，stream 也從未 XTrim。下面兩個測試涵蓋新增的「死信判斷」與「payload 截斷」
// 純函式邏輯——reclaimStaleMessages/fetchRetryCounts/trimStream 本身要打真正的 Redis
// （*redis.Client 是具體型別，Worker.rdb 未介面化），無法在不牽動真正連線的情況下單元測試，
// 這點與 recomputeStandings 等既有函式的處境相同（見上面 TestShouldRecompute 只測抽出的純函式）。

// TestDeadLetterDecision 涵蓋 reclaimStaleMessages 的死信判斷邏輯：
//   - 處理成功 → 一律 ACK、非死信，不管投遞次數多少。
//   - 處理失敗但投遞次數未達門檻 → 不 ACK（留在 PEL 等下一輪重試）。
//   - 處理失敗且投遞次數已達門檻（>=）→ ACK 並標記死信。
//   - 門檻是「大於等於」不是「大於」：deliveryCount 剛好等於 threshold 那一次就要死信化，不必再多等一輪。
func TestDeadLetterDecision(t *testing.T) {
	cases := []struct {
		name           string
		processOK      bool
		deliveryCount  int64
		threshold      int64
		wantAck        bool
		wantDeadLetter bool
	}{
		{"success with high delivery count still acks non-dead-letter", true, 999, 5, true, false},
		{"success on first delivery acks non-dead-letter", true, 1, 5, true, false},
		{"first failure below threshold does not ack", false, 1, 5, false, false},
		{"failure just below threshold does not ack", false, 4, 5, false, false},
		{"failure exactly at threshold acks as dead letter", false, 5, 5, true, true},
		{"failure above threshold acks as dead letter", false, 9, 5, true, true},
		{"missing retry count (zero value) treated as not-yet-dead-letter", false, 0, 5, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ack, dead := deadLetterDecision(c.processOK, c.deliveryCount, c.threshold)
			if ack != c.wantAck {
				t.Errorf("shouldAck = %v, want %v", ack, c.wantAck)
			}
			if dead != c.wantDeadLetter {
				t.Errorf("isDeadLetter = %v, want %v", dead, c.wantDeadLetter)
			}
		})
	}
}

// TestTruncatePayload 確認死信告警不會把完整（可能很長的）payload 塞進 Telegram 訊息，
// 但短 payload 應原封不動保留（利於診斷）。
func TestTruncatePayload(t *testing.T) {
	short := `{"user_id":"u1","distance_km":5.2}`
	if got := truncatePayload(short); got != short {
		t.Errorf("short payload should be unchanged, got %q", got)
	}

	long := make([]byte, 1000)
	for i := range long {
		long[i] = 'a'
	}
	got := truncatePayload(string(long))
	if len(got) <= 300 {
		t.Errorf("expected truncated output longer than raw 300-char cutoff (includes suffix marker), got len=%d", len(got))
	}
	if got[:300] != string(long[:300]) {
		t.Error("truncated payload should keep the original prefix intact")
	}
}
