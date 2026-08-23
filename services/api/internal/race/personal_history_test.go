package race

import "testing"

// TestPersonalHistoryBestMetric 驗證「最佳成績」呈現維度依 challenge_rule 完成條件類型正確分流：
// streak_days（目標天數固定）比用時；window_cumulative/single_distance（核心即距離）比距離。
func TestPersonalHistoryBestMetric(t *testing.T) {
	cases := []struct {
		name string
		rule *ChallengeRule
		want string
	}{
		{"nil rule", nil, ""},
		{"streak_days", &ChallengeRule{CompletionType: CompletionStreakDays}, "duration"},
		{"window_cumulative", &ChallengeRule{CompletionType: CompletionWindowCumulative}, "distance"},
		{"single_distance", &ChallengeRule{CompletionType: CompletionSingleDistance}, "distance"},
		{"unknown type falls back to duration", &ChallengeRule{CompletionType: "bogus"}, "duration"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := personalHistoryBestMetric(c.rule); got != c.want {
				t.Fatalf("personalHistoryBestMetric(%+v) = %q, want %q", c.rule, got, c.want)
			}
		})
	}
}
