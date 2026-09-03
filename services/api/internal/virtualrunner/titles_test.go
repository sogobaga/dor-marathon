package virtualrunner

import (
	"testing"

	"github.com/dor/api/internal/profile"
)

// TestPickRandomTitle_Empty 沒有已解鎖稱號時回空字串，且不呼叫 intn（避免呼叫端對空 slice 求
// index 觸發任何潛在的除零/越界問題——這裡用會 Fatal 的 intn 佐證真的沒被呼叫到）。
func TestPickRandomTitle_Empty(t *testing.T) {
	called := false
	intn := func(n int) int { called = true; return 0 }
	got := pickRandomTitle(nil, intn)
	if got != "" {
		t.Fatalf("pickRandomTitle(nil) = %q，應為空字串", got)
	}
	if called {
		t.Fatal("unlocked 為空時不應呼叫 intn")
	}
}

// TestPickRandomTitle_UsesInjectedIndex 挑選結果＝unlocked[intn(len(unlocked))]，用固定 intn
// 驗證每個索引都能被選中（可決定性測試，不依賴實際隨機分布）。
func TestPickRandomTitle_UsesInjectedIndex(t *testing.T) {
	unlocked := []profile.UnlockedTitle{
		{Code: "a", Category: "cum_dist", Tier: 1, SortOrder: 1},
		{Code: "b", Category: "single_dist", Tier: 5, SortOrder: 9}, // 刻意最高 tier/sort_order，
		{Code: "c", Category: "cum_time", Tier: 2, SortOrder: 3},    // 驗證「全隨機」不偏好它
	}
	for i, want := range []string{"a", "b", "c"} {
		got := pickRandomTitle(unlocked, func(n int) int {
			if n != len(unlocked) {
				t.Fatalf("intn 收到的 n = %d，應為 %d", n, len(unlocked))
			}
			return i
		})
		if got != want {
			t.Fatalf("pickRandomTitle 索引 %d 選到 %q，應為 %q", i, got, want)
		}
	}
}

// TestPickRandomTitle_UniformDistribution 用固定種子的簡易 LCG 跑多輪，驗證每個選項大致被均勻
// 選中（不偏好任何 tier/category），佐證「全隨機」規則（2026-09-03 拍板②）沒有被實作成變相的
// 加權挑選。
func TestPickRandomTitle_UniformDistribution(t *testing.T) {
	unlocked := []profile.UnlockedTitle{
		{Code: "a", Category: "cum_dist", Tier: 1, SortOrder: 1},
		{Code: "b", Category: "single_dist", Tier: 9, SortOrder: 9},
		{Code: "c", Category: "cum_time", Tier: 5, SortOrder: 5},
		{Code: "d", Category: "cum_dist", Tier: 2, SortOrder: 2},
	}
	const trials = 40000
	counts := map[string]int{}
	state := uint64(12345)
	intn := func(n int) int {
		// xorshift64*，純函式、不依賴套件外部隨機源，方便測試自成一體且可重現。
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return int(state % uint64(n))
	}
	for i := 0; i < trials; i++ {
		counts[pickRandomTitle(unlocked, intn)]++
	}
	wantEach := float64(trials) / float64(len(unlocked))
	for _, code := range []string{"a", "b", "c", "d"} {
		got := float64(counts[code])
		if got < wantEach*0.9 || got > wantEach*1.1 {
			t.Fatalf("code=%s 被選中 %d 次，期望貼近均勻分布 %.0f 次（±10%%）", code, counts[code], wantEach)
		}
	}
}

// TestClampRerollEvery every<1 一律退回 defaultRerollEvery，其餘原樣採用。
func TestClampRerollEvery(t *testing.T) {
	cases := []struct {
		name  string
		every int
		want  int
	}{
		{"zero", 0, defaultRerollEvery},
		{"negative", -5, defaultRerollEvery},
		{"positive_unchanged", 7, 7},
		{"one_unchanged", 1, 1},
		{"large_unchanged", 10000, 10000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampRerollEvery(tc.every); got != tc.want {
				t.Fatalf("clampRerollEvery(%d) = %d，應為 %d", tc.every, got, tc.want)
			}
		})
	}
}

// TestRerollDue 表格測試涵蓋 2026-09-03 拍板規則②③：displayedEmpty 恆為 true；否則 runs 需為
// （clamp 過的）every 的正倍數才 true。
func TestRerollDue(t *testing.T) {
	cases := []struct {
		name           string
		runs           int
		every          int
		displayedEmpty bool
		want           bool
	}{
		{"displayed_empty_overrides_everything", 3, 10, true, true},
		{"displayed_empty_even_when_runs_zero", 0, 10, true, true},
		{"runs_below_every_not_due", 5, 10, false, false},
		{"runs_exact_multiple_due", 10, 10, false, true},
		{"runs_multiple_of_every_20_due", 20, 10, false, true},
		{"runs_zero_not_due_even_if_would_mod_zero", 0, 10, false, false},
		{"every_zero_clamped_to_default_10_due_at_10", 10, 0, false, true},
		{"every_zero_clamped_to_default_10_not_due_at_5", 5, 0, false, false},
		{"every_negative_clamped_to_default_10_due_at_10", 10, -3, false, true},
		{"every_one_reroll_every_run", 7, 1, false, true},
		{"runs_just_past_multiple_not_due", 11, 10, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rerollDue(tc.runs, tc.every, tc.displayedEmpty); got != tc.want {
				t.Fatalf("rerollDue(runs=%d, every=%d, displayedEmpty=%v) = %v，應為 %v",
					tc.runs, tc.every, tc.displayedEmpty, got, tc.want)
			}
		})
	}
}
