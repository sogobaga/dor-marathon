package race

import (
	"context"
	"sync"
	"time"
)

// --- per-race 短 TTL 快取（1000 人效能優化批次 項目 A/B）---
//
// 背景：進度頁／排行榜頁前端都是 30 秒 SWR 輪詢；1000 人同看同一場賽事時，若每次請求都各自
// 全場掃描 activities，就會變成每 30 秒近千次重複掃描同一份資料。這裡用 raceID 為 key 的短 TTL
// 記憶體快取，讓同一個 TTL 窗口內的所有觀看者共享同一次掃描結果。
//
// 兩個快取（LoadRaceActivities／computeFinishers）各自獨立的 map + RWMutex，避免互相拖累鎖競爭；
// TTL 常數共用，但這只是省一個數字，不代表兩者互相依賴。
//
// 刻意不做的事：沒有做 singleflight 防止 cache miss 當下的併發重複查詢（thundering herd）。
// TTL 到期瞬間仍可能有少量並發請求各自打一次 DB，但相較「完全不快取」已是數量級的改善，
// 且複雜度/風險換來的邊際效益在目前規模不划算，先不做。

// raceCacheTTL 短 TTL 快取存活時間。刻意略短於前端 30 秒 SWR 輪詢間隔：同一輪詢週期內的多個
// 觀看者幾乎必然落在同一個 20 秒窗口內、共用同一次掃描結果（省下重複查詢）；同時保證下一次
// 輪詢一定會看到新資料，不會讓使用者感覺資料卡住不動。
const raceCacheTTL = 20 * time.Second

// cacheFresh 純函式：TTL 是否仍有效，抽出來方便不必真的等待就能單元測試。
func cacheFresh(expiry, now time.Time) bool {
	return now.Before(expiry)
}

// --- 項目 A：LoadRaceActivities 快取（僅供 GetRaceProgress 使用）---

type raceActivitiesEntry struct {
	acts   []progAct
	expiry time.Time
}

var (
	raceActivitiesMu    sync.RWMutex
	raceActivitiesCache = map[string]raceActivitiesEntry{}
)

// getRaceActivitiesCached 是 LoadRaceActivities 的短 TTL 快取包裝。
//
// 重要：只有 GetRaceProgress（前端 30 秒輪詢的進度頁）走這裡。settlement.go 的 EXP 結算等其他
// 呼叫者一律必須維持呼叫原始 LoadRaceActivities，不可改用這個快取版本——結算是對正確性要求高、
// 非高頻輪詢的路徑，不能吃到 20 秒內的 stale 資料（見 settlement.go:152 呼叫點註解）。
func (r *Repository) getRaceActivitiesCached(ctx context.Context, raceID string) ([]progAct, error) {
	now := time.Now()
	raceActivitiesMu.RLock()
	entry, ok := raceActivitiesCache[raceID]
	raceActivitiesMu.RUnlock()
	if ok && cacheFresh(entry.expiry, now) {
		return entry.acts, nil
	}

	acts, err := r.LoadRaceActivities(ctx, raceID)
	if err != nil {
		return nil, err
	}
	raceActivitiesMu.Lock()
	raceActivitiesCache[raceID] = raceActivitiesEntry{acts: acts, expiry: now.Add(raceCacheTTL)}
	raceActivitiesMu.Unlock()
	return acts, nil
}

// --- 項目 B：computeFinishers 基底結果快取（僅供 GetLeaderboard 使用）---

type leaderboardBaseEntry struct {
	finishers []finisher
	total     int
	expiry    time.Time
}

var (
	leaderboardBaseMu    sync.RWMutex
	leaderboardBaseCache = map[string]leaderboardBaseEntry{}
)

// getFinishersCached 是 computeFinishers 的短 TTL 快取包裝。computeFinishers 回傳的完成榜（排名依據
// 的里程/完成時間/暱稱等）與觀看者是誰無關，可以安全地在同一場賽事的所有觀看者間共用；
// is_following 這種 per-viewer 欄位不在 computeFinishers 內部計算（已經是在 GetLeaderboard 呼叫層
// 用 FollowingSet 另外查、再套用到每一列），因此天生就與這裡的快取內容分離，不需要額外拆分。
func (r *Repository) getFinishersCached(ctx context.Context, raceID string) ([]finisher, int, error) {
	now := time.Now()
	leaderboardBaseMu.RLock()
	entry, ok := leaderboardBaseCache[raceID]
	leaderboardBaseMu.RUnlock()
	if ok && cacheFresh(entry.expiry, now) {
		return entry.finishers, entry.total, nil
	}

	finishers, total, err := r.computeFinishers(ctx, raceID)
	if err != nil {
		return nil, 0, err
	}
	leaderboardBaseMu.Lock()
	leaderboardBaseCache[raceID] = leaderboardBaseEntry{finishers: finishers, total: total, expiry: now.Add(raceCacheTTL)}
	leaderboardBaseMu.Unlock()
	return finishers, total, nil
}
