package dbwake

import "testing"

// ---- decide 純函式：距上次查詢是否已達喚醒門檻 ----

func TestDecide(t *testing.T) {
	const threshold = int64(WakeThreshold)
	cases := []struct {
		name string
		last int64
		now  int64
		want bool
	}{
		{"間隔為 0＝連續查詢，不算喚醒", 1_000, 1_000, false},
		{"剛好未達門檻前一奈秒＝不算喚醒", 0, threshold - 1, false},
		{"剛好達到門檻邊界＝算喚醒", 0, threshold, true},
		{"遠超過門檻＝算喚醒", 0, threshold * 100, true},
		{"last 晚於 now（理論上不會發生，但差值為負不應誤判為喚醒）", 1_000_000, 500_000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decide(tc.last, tc.now, threshold); got != tc.want {
				t.Errorf("decide(last=%d, now=%d, threshold=%d) = %v, want %v",
					tc.last, tc.now, threshold, got, tc.want)
			}
		})
	}
}
