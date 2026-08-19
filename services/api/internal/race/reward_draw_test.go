package race

import (
	"errors"
	"testing"
	"time"
)

func f64(v float64) *float64 { return &v }

func mustAt(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse("2006-01-02T15:04:05", s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return tm
}

// --- winningGroupsHighestMetric ---

func TestWinningGroupsHighestMetric_TieAllWin(t *testing.T) {
	task := RaceTask{MetricType: "cumulative_distance", TargetValue: f64(10)}
	aggs := []groupAgg{
		{groupID: "A", acts: []progAct{{Dist: 7}, {Dist: 5}}},  // 12km, done, tie for highest
		{groupID: "B", acts: []progAct{{Dist: 12}}},            // 12km, done, tie for highest
		{groupID: "C", acts: []progAct{{Dist: 3}, {Dist: 2}}},  // 5km, not done
	}
	winners, err := winningGroupsHighestMetric(aggs, task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(winners) != 2 || winners[0] != "A" || winners[1] != "B" {
		t.Fatalf("expected tie [A B], got %v", winners)
	}
}

func TestWinningGroupsHighestMetric_SingleWinner(t *testing.T) {
	task := RaceTask{MetricType: "cumulative_distance", TargetValue: f64(10)}
	aggs := []groupAgg{
		{groupID: "A", acts: []progAct{{Dist: 15}}},
		{groupID: "B", acts: []progAct{{Dist: 11}}},
	}
	winners, err := winningGroupsHighestMetric(aggs, task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(winners) != 1 || winners[0] != "A" {
		t.Fatalf("expected [A], got %v", winners)
	}
}

func TestWinningGroupsHighestMetric_NoGroupReached(t *testing.T) {
	task := RaceTask{MetricType: "cumulative_distance", TargetValue: f64(100)}
	aggs := []groupAgg{
		{groupID: "A", acts: []progAct{{Dist: 5}}},
		{groupID: "B", acts: []progAct{{Dist: 3}}},
	}
	_, err := winningGroupsHighestMetric(aggs, task)
	if !errors.Is(err, ErrNoGroupReachedTarget) {
		t.Fatalf("expected ErrNoGroupReachedTarget, got %v", err)
	}
}

// --- winningGroupsFirstToTarget ---

func TestWinningGroupsFirstToTarget_EarliestWins(t *testing.T) {
	task := RaceTask{MetricType: "cumulative_distance", TargetValue: f64(10)}
	aggs := []groupAgg{
		{groupID: "A", acts: []progAct{
			{Dist: 5, At: mustAt(t, "2026-01-01T10:00:00")},
			{Dist: 6, At: mustAt(t, "2026-01-01T11:00:00")}, // crosses 10 here (11:00)
		}},
		{groupID: "B", acts: []progAct{
			{Dist: 12, At: mustAt(t, "2026-01-01T10:30:00")}, // crosses 10 here (10:30) — earlier than A
		}},
	}
	winners, err := winningGroupsFirstToTarget(aggs, task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(winners) != 1 || winners[0] != "B" {
		t.Fatalf("expected [B] (earliest crossing), got %v", winners)
	}
}

func TestWinningGroupsFirstToTarget_TieAllWin(t *testing.T) {
	task := RaceTask{MetricType: "cumulative_distance", TargetValue: f64(10)}
	at := mustAt(t, "2026-01-01T10:00:00")
	aggs := []groupAgg{
		{groupID: "A", acts: []progAct{{Dist: 12, At: at}}},
		{groupID: "B", acts: []progAct{{Dist: 15, At: at}}},
	}
	winners, err := winningGroupsFirstToTarget(aggs, task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(winners) != 2 || winners[0] != "A" || winners[1] != "B" {
		t.Fatalf("expected tie [A B], got %v", winners)
	}
}

func TestWinningGroupsFirstToTarget_NoGroupReached(t *testing.T) {
	task := RaceTask{MetricType: "cumulative_distance", TargetValue: f64(100)}
	aggs := []groupAgg{
		{groupID: "A", acts: []progAct{{Dist: 5, At: mustAt(t, "2026-01-01T10:00:00")}}},
	}
	_, err := winningGroupsFirstToTarget(aggs, task)
	if !errors.Is(err, ErrNoGroupReachedTarget) {
		t.Fatalf("expected ErrNoGroupReachedTarget, got %v", err)
	}
}

// --- resolveWinTask ---

func TestResolveWinTask_DefaultSkipsCheckpoint(t *testing.T) {
	tasks := []RaceTask{
		{ID: "t1", Scope: ScopeGroupTeam, MetricType: MetricCheckpoint},
		{ID: "t2", Scope: ScopeGroupTeam, MetricType: "cumulative_distance"},
	}
	got, err := resolveWinTask(tasks, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != "t2" {
		t.Fatalf("expected default to pick t2 (first non-checkpoint group_team task), got %q", got.ID)
	}
}

func TestResolveWinTask_ExplicitCheckpointRejected(t *testing.T) {
	tasks := []RaceTask{
		{ID: "t1", Scope: ScopeGroupTeam, MetricType: MetricCheckpoint},
	}
	_, err := resolveWinTask(tasks, "t1")
	if !errors.Is(err, ErrGroupTaskUnsupported) {
		t.Fatalf("expected ErrGroupTaskUnsupported, got %v", err)
	}
}

func TestResolveWinTask_WrongScopeRejected(t *testing.T) {
	tasks := []RaceTask{
		{ID: "t1", Scope: ScopeRaceCollective, MetricType: "cumulative_distance"},
	}
	_, err := resolveWinTask(tasks, "t1")
	if !errors.Is(err, ErrWinTaskNotFound) {
		t.Fatalf("expected ErrWinTaskNotFound, got %v", err)
	}
}

func TestResolveWinTask_UnknownIDRejected(t *testing.T) {
	tasks := []RaceTask{
		{ID: "t1", Scope: ScopeGroupTeam, MetricType: "cumulative_distance"},
	}
	_, err := resolveWinTask(tasks, "does-not-exist")
	if !errors.Is(err, ErrWinTaskNotFound) {
		t.Fatalf("expected ErrWinTaskNotFound, got %v", err)
	}
}

func TestResolveWinTask_NoEligibleGroupTeamTask(t *testing.T) {
	tasks := []RaceTask{
		{ID: "t1", Scope: ScopeRaceCollective, MetricType: "cumulative_distance"},
		{ID: "t2", Scope: ScopeGroupTeam, MetricType: MetricCheckpoint}, // only a checkpoint one — still no usable default
	}
	_, err := resolveWinTask(tasks, "")
	if !errors.Is(err, ErrNoGroupTask) {
		t.Fatalf("expected ErrNoGroupTask, got %v", err)
	}
}

// --- groupsForTask ---

func TestGroupsForTask_AllGroupsWhenUnscoped(t *testing.T) {
	groups := []RaceGroup{{ID: "A"}, {ID: "B"}}
	got := groupsForTask(groups, RaceTask{GroupID: ""})
	if len(got) != 2 {
		t.Fatalf("expected all 2 groups, got %d", len(got))
	}
}

func TestGroupsForTask_SingleGroupWhenScoped(t *testing.T) {
	groups := []RaceGroup{{ID: "A"}, {ID: "B"}}
	got := groupsForTask(groups, RaceTask{GroupID: "B"})
	if len(got) != 1 || got[0].ID != "B" {
		t.Fatalf("expected only [B], got %v", got)
	}
}

// --- shuffleSelect ---

func TestShuffleSelect_CapsAtPoolSize(t *testing.T) {
	pool := []string{"a", "b", "c"}
	got := shuffleSelect(pool, 10)
	if len(got) != 3 {
		t.Fatalf("expected all 3 elements when n > pool size, got %d", len(got))
	}
}

func TestShuffleSelect_ReturnsRequestedCountAndUniqueElements(t *testing.T) {
	pool := []string{"a", "b", "c", "d", "e"}
	got := shuffleSelect(pool, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(got))
	}
	seen := map[string]bool{}
	for _, v := range got {
		if seen[v] {
			t.Fatalf("shuffleSelect returned duplicate element %q", v)
		}
		seen[v] = true
	}
}

func TestShuffleSelect_DoesNotMutateOriginalPool(t *testing.T) {
	pool := []string{"a", "b", "c"}
	orig := append([]string(nil), pool...)
	_ = shuffleSelect(pool, 2)
	for i := range pool {
		if pool[i] != orig[i] {
			t.Fatalf("shuffleSelect mutated input pool: got %v, want %v", pool, orig)
		}
	}
}
