// H7 資安修補：/ws/race/{raceID} 原本對任意 raceID（含不存在的）都會建立 Hub＋goroutine＋
// Redis 訂閱，且從不回收——攻擊者連續打不同的隨機 raceID 就能無限放大資源耗盡。這裡在建立
// Hub 之前，先確認 raceID 是真實存在的賽事，並用一個很輕量的 TTL 快取避免每次 WS 連線
// 都查一次 DB。
package realtime

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

const (
	raceExistPositiveTTL = 10 * time.Minute // 存在：賽事建立後不會消失，快取久一點沒差
	raceExistNegativeTTL = 1 * time.Minute  // 不存在：避免探測不同 raceID 時對 DB 灌爆，但也不會讓
	// 「剛建立的賽事」被誤快取成不存在太久

	// raceExistCacheCap 是粗糙但有效的防護：若不設上限，攻擊者對大量不同（甚至格式不合法的）
	// raceID 探測會讓這個 map 本身變成新的記憶體耗盡管道。超過上限就整個清空重來——
	// 正常流量下賽事數量遠小於這個數字，不會被誤傷；被清空後最多就是下一次查詢多打一次 DB。
	raceExistCacheCap = 5000
)

// pgxQueryer 只取用到的最小介面（QueryRow），避免這個套件直接依賴 *pgxpool.Pool 的完整介面，
// 方便未來需要時用假物件測試。main.go 直接傳 *pgxpool.Pool 進來即滿足此介面。
type pgxQueryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type raceExistCache struct {
	pool pgxQueryer

	mu      sync.Mutex
	entries map[string]raceExistEntry
}

type raceExistEntry struct {
	exists    bool
	expiresAt time.Time
}

func newRaceExistCache(pool pgxQueryer) *raceExistCache {
	return &raceExistCache{pool: pool, entries: make(map[string]raceExistEntry)}
}

func (c *raceExistCache) get(raceID string) (exists bool, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, found := c.entries[raceID]
	if !found || time.Now().After(e.expiresAt) {
		return false, false
	}
	return e.exists, true
}

func (c *raceExistCache) set(raceID string, exists bool) {
	ttl := raceExistPositiveTTL
	if !exists {
		ttl = raceExistNegativeTTL
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= raceExistCacheCap {
		c.entries = make(map[string]raceExistEntry) // 見 raceExistCacheCap 註解
	}
	c.entries[raceID] = raceExistEntry{exists: exists, expiresAt: time.Now().Add(ttl)}
}

// Exists 查詢 raceID 是否為真實存在的賽事（先查快取，未命中才打 DB）。
// 查詢本身失敗（含 raceID 不是合法 UUID 格式——Postgres 會回 invalid input syntax）一律
// 視為「不存在」但**不快取**：避免把暫時性的 DB 錯誤或攻擊者亂打的格式錯誤字串當成快取
// 對象；呼叫端因此永遠拿到明確的 bool，不需要另外處理 error（錯誤已在這裡記 log）。
func (c *raceExistCache) Exists(ctx context.Context, raceID string) bool {
	if exists, ok := c.get(raceID); ok {
		return exists
	}

	var exists bool
	err := c.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM races WHERE id=$1)`, raceID).Scan(&exists)
	if err != nil {
		if err != pgx.ErrNoRows {
			log.Warn().Err(err).Str("race", raceID).Msg("ws race existence check query failed")
		}
		return false
	}

	c.set(raceID, exists)
	return exists
}
