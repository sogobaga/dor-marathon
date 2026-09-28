package race

import (
	"testing"
	"time"
)

func taipeiDate(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestPercentFromRatio(t *testing.T) {
	cases := []struct {
		name          string
		value, target float64
		want          int
	}{
		{"zero progress", 0, 30, 0},
		{"half", 15, 30, 50},
		{"rounds up", 20, 30, 67}, // 66.67% -> 67
		{"exact complete", 30, 30, 100},
		{"over target clamps to 100", 45, 30, 100},
		{"target zero guards divide-by-zero", 10, 0, 0},
		{"target negative guards", 10, -5, 0},
		{"rounds down", 10, 30, 33}, // 33.33% -> 33
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := percentFromRatio(c.value, c.target); got != c.want {
				t.Fatalf("percentFromRatio(%v,%v) = %d, want %d", c.value, c.target, got, c.want)
			}
		})
	}
}

func TestWindowCumulativePercent(t *testing.T) {
	cases := []struct {
		name                                       string
		cumKm, targetCumKm, bestSingleKm, targetKm float64
		want                                       int
	}{
		{"no single_km sub-target: pure cum percent", 15, 30, 0, 0, 50},
		{"both targets fully met", 30, 30, 10, 5, 100},
		{"cum met but single sub-target not met caps percent", 30, 30, 2, 5, 40},
		{"single met but cum not met takes cum", 10, 30, 5, 5, 33},
		{"neither met takes the lower one", 10, 30, 1, 5, 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := windowCumulativePercent(c.cumKm, c.targetCumKm, c.bestSingleKm, c.targetKm); got != c.want {
				t.Fatalf("windowCumulativePercent(%v,%v,%v,%v) = %d, want %d",
					c.cumKm, c.targetCumKm, c.bestSingleKm, c.targetKm, got, c.want)
			}
		})
	}
}

func TestExpectedEndAt(t *testing.T) {
	started := taipeiDate(2026, 9, 3)
	raceEnd := taipeiDate(2026, 12, 31)

	t.Run("streak_days has no window, uses race end", func(t *testing.T) {
		rule := &ChallengeRule{CompletionType: CompletionStreakDays, Days: 30}
		if got := expectedEndAt(rule, started, raceEnd); !got.Equal(raceEnd) {
			t.Fatalf("expectedEndAt = %v, want race end %v", got, raceEnd)
		}
	})

	t.Run("single_distance has no window, uses race end", func(t *testing.T) {
		rule := &ChallengeRule{CompletionType: CompletionSingleDistance, SingleKm: 42}
		if got := expectedEndAt(rule, started, raceEnd); !got.Equal(raceEnd) {
			t.Fatalf("expectedEndAt = %v, want race end %v", got, raceEnd)
		}
	})

	t.Run("window_cumulative window ends before race end: window wins", func(t *testing.T) {
		rule := &ChallengeRule{CompletionType: CompletionWindowCumulative, WindowDays: 30, CumKm: 100}
		want := started.AddDate(0, 0, 30)
		if got := expectedEndAt(rule, started, raceEnd); !got.Equal(want) {
			t.Fatalf("expectedEndAt = %v, want window end %v", got, want)
		}
	})

	t.Run("window_cumulative window ends after race end: race end wins", func(t *testing.T) {
		rule := &ChallengeRule{CompletionType: CompletionWindowCumulative, WindowDays: 365, CumKm: 100}
		if got := expectedEndAt(rule, started, raceEnd); !got.Equal(raceEnd) {
			t.Fatalf("expectedEndAt = %v, want race end %v", got, raceEnd)
		}
	})

	t.Run("nil rule falls back to race end", func(t *testing.T) {
		if got := expectedEndAt(nil, started, raceEnd); !got.Equal(raceEnd) {
			t.Fatalf("expectedEndAt(nil) = %v, want race end %v", got, raceEnd)
		}
	})
}

func TestCurrentStreakLen(t *testing.T) {
	today := taipeiDate(2026, 9, 27)

	t.Run("no qualifying day ever: zero", func(t *testing.T) {
		if got := currentStreakLen(nil, 5, today); got != 0 {
			t.Fatalf("currentStreakLen(nil,...) = %d, want 0", got)
		}
	})

	t.Run("last qualifying day is today: streak alive", func(t *testing.T) {
		d := today
		if got := currentStreakLen(&d, 7, today); got != 7 {
			t.Fatalf("currentStreakLen = %d, want 7", got)
		}
	})

	t.Run("last qualifying day is yesterday: streak still alive (today not over yet)", func(t *testing.T) {
		d := today.AddDate(0, 0, -1)
		if got := currentStreakLen(&d, 7, today); got != 7 {
			t.Fatalf("currentStreakLen = %d, want 7", got)
		}
	})

	t.Run("last qualifying day is two days ago: streak broken", func(t *testing.T) {
		d := today.AddDate(0, 0, -2)
		if got := currentStreakLen(&d, 7, today); got != 0 {
			t.Fatalf("currentStreakLen = %d, want 0", got)
		}
	})

	t.Run("last qualifying day long ago: streak broken", func(t *testing.T) {
		d := today.AddDate(0, 0, -20)
		if got := currentStreakLen(&d, 3, today); got != 0 {
			t.Fatalf("currentStreakLen = %d, want 0", got)
		}
	})
}

func TestAttemptAtRisk(t *testing.T) {
	cases := []struct {
		name          string
		currentStreak int
		todayDone     bool
		want          bool
	}{
		{"alive & today done: not at risk", 7, true, false},
		{"alive via yesterday & today not done: at risk", 7, false, true},
		{"broken (current_streak=0), today not done: not at risk", 0, false, false},
		{"broken (current_streak=0), today somehow done: not at risk", 0, true, false},
		{"day-1 attempt with nothing yet (current_streak=0, today not done): not at risk", 0, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := attemptAtRisk(c.currentStreak, c.todayDone); got != c.want {
				t.Fatalf("attemptAtRisk(%v,%v) = %v, want %v", c.currentStreak, c.todayDone, got, c.want)
			}
		})
	}
}

func TestEarliestCompleteDate(t *testing.T) {
	today := taipeiDate(2026, 9, 28)

	t.Run("already met target via longest streak: not applicable (nil)", func(t *testing.T) {
		if got := earliestCompleteDate(30, 30, 30, false, today); got != nil {
			t.Fatalf("earliestCompleteDate = %v, want nil", got)
		}
	})

	t.Run("longest streak already exceeds target: not applicable (nil)", func(t *testing.T) {
		if got := earliestCompleteDate(35, 5, 30, false, today); got != nil {
			t.Fatalf("earliestCompleteDate = %v, want nil", got)
		}
	})

	t.Run("today done: today + (target-current_streak) days", func(t *testing.T) {
		want := today.AddDate(0, 0, 5) // target 30, current 25 -> 5
		if got := earliestCompleteDate(25, 25, 30, true, today); got == nil || !got.Equal(want) {
			t.Fatalf("earliestCompleteDate = %v, want %v", got, want)
		}
	})

	t.Run("spec sanity check: started 2026-09-03, streak 25, today 2026-09-28 not done, target 30 -> 2026-10-02", func(t *testing.T) {
		want := taipeiDate(2026, 10, 2)
		if got := earliestCompleteDate(25, 25, 30, false, today); got == nil || !got.Equal(want) {
			t.Fatalf("earliestCompleteDate = %v, want %v", got, want)
		}
	})

	t.Run("streak broken (current_streak=0), today not done: today + target - 1", func(t *testing.T) {
		want := today.AddDate(0, 0, 29) // target 30 -> +29
		if got := earliestCompleteDate(10, 0, 30, false, today); got == nil || !got.Equal(want) {
			t.Fatalf("earliestCompleteDate = %v, want %v", got, want)
		}
	})

	t.Run("day-1 attempt with nothing yet (streak/current 0, today not done): today + target - 1", func(t *testing.T) {
		want := today.AddDate(0, 0, 29)
		if got := earliestCompleteDate(0, 0, 30, false, today); got == nil || !got.Equal(want) {
			t.Fatalf("earliestCompleteDate = %v, want %v", got, want)
		}
	})
}

func TestCanFinishBeforeDeadline(t *testing.T) {
	today := taipeiDate(2026, 9, 28)
	deadline := taipeiDate(2026, 12, 31)

	t.Run("streak_days: earliest before deadline -> true", func(t *testing.T) {
		earliest := taipeiDate(2026, 10, 2)
		if !canFinishBeforeDeadline(&earliest, deadline, today) {
			t.Fatalf("want true")
		}
	})

	t.Run("streak_days: earliest exactly on deadline -> true", func(t *testing.T) {
		earliest := deadline
		if !canFinishBeforeDeadline(&earliest, deadline, today) {
			t.Fatalf("want true")
		}
	})

	t.Run("streak_days: earliest after deadline -> false", func(t *testing.T) {
		earliest := deadline.AddDate(0, 0, 1)
		if canFinishBeforeDeadline(&earliest, deadline, today) {
			t.Fatalf("want false")
		}
	})

	t.Run("non-streak types (earliest nil): not yet expired -> true", func(t *testing.T) {
		if !canFinishBeforeDeadline(nil, deadline, today) {
			t.Fatalf("want true")
		}
	})

	t.Run("non-streak types (earliest nil): already past deadline -> false", func(t *testing.T) {
		if canFinishBeforeDeadline(nil, today, deadline) {
			t.Fatalf("want false")
		}
	})
}

func TestFormatTaipeiDate(t *testing.T) {
	if got := formatTaipeiDate(nil); got != nil {
		t.Fatalf("formatTaipeiDate(nil) = %v, want nil", got)
	}
	d := taipeiDate(2026, 9, 27)
	got := formatTaipeiDate(&d)
	if got == nil || *got != "2026-09-27" {
		t.Fatalf("formatTaipeiDate(%v) = %v, want \"2026-09-27\"", d, got)
	}
}
