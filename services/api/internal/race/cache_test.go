package race

import (
	"testing"
	"time"
)

func TestCacheFresh(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	if !cacheFresh(now.Add(1*time.Second), now) {
		t.Fatalf("expiry 1s in the future should be fresh")
	}
	if !cacheFresh(now.Add(raceCacheTTL), now) {
		t.Fatalf("expiry exactly TTL in the future should be fresh")
	}
	if cacheFresh(now, now) {
		t.Fatalf("expiry == now should be stale (not before now)")
	}
	if cacheFresh(now.Add(-1*time.Second), now) {
		t.Fatalf("expiry in the past should be stale")
	}
}

func TestGetRaceActivitiesCached_ExpiresAfterTTL(t *testing.T) {
	raceActivitiesMu.Lock()
	raceActivitiesCache["test-race"] = raceActivitiesEntry{
		acts:   []progAct{{UserID: "u1", Dist: 5}},
		expiry: time.Now().Add(-time.Second), // 已過期
	}
	raceActivitiesMu.Unlock()
	defer func() {
		raceActivitiesMu.Lock()
		delete(raceActivitiesCache, "test-race")
		raceActivitiesMu.Unlock()
	}()

	raceActivitiesMu.RLock()
	entry, ok := raceActivitiesCache["test-race"]
	raceActivitiesMu.RUnlock()
	if !ok {
		t.Fatalf("expected entry to exist")
	}
	if cacheFresh(entry.expiry, time.Now()) {
		t.Fatalf("expected expired entry to be stale")
	}
}

func TestGetFinishersCached_FreshEntryReturnedWithoutQuery(t *testing.T) {
	leaderboardBaseMu.Lock()
	leaderboardBaseCache["test-race"] = leaderboardBaseEntry{
		finishers: []finisher{{userID: "u1", distanceKm: 42}},
		total:     10,
		expiry:    time.Now().Add(raceCacheTTL),
	}
	leaderboardBaseMu.Unlock()
	defer func() {
		leaderboardBaseMu.Lock()
		delete(leaderboardBaseCache, "test-race")
		leaderboardBaseMu.Unlock()
	}()

	leaderboardBaseMu.RLock()
	entry, ok := leaderboardBaseCache["test-race"]
	leaderboardBaseMu.RUnlock()
	if !ok || !cacheFresh(entry.expiry, time.Now()) {
		t.Fatalf("expected fresh cache entry")
	}
	if entry.total != 10 || len(entry.finishers) != 1 || entry.finishers[0].userID != "u1" {
		t.Fatalf("unexpected cached entry: %+v", entry)
	}
}
