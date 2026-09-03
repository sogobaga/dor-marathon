// Package ttlcache 提供一個泛型、行程內（in-process、per-instance，不跨機器/不進 Redis）記憶體
// TTL 快取，供 SSR／公開端點在 Postgres 前面加一層短快取，避免「每次 request 都查一次 DB」——
// 背景見 activity-data-source-gate 同類手法：夜間爬蟲掃過多筆公開頁（app-settings/site-settings/
// 賽事簡章 metadata）會不斷讓 Neon serverless compute 醒著（Neon 5 分鐘沒查詢才休眠）。
//
// 語意（三種情況）：
//  1. 命中且未過期：直接回傳，完全不碰 DB。
//  2. 過期但「曾經」成功載入過：立刻回傳舊值（stale-while-revalidate），同時在背景起「唯一」一個
//     goroutine 重新整理——並發的其他呼叫者看到 refreshing=true 就不會再各自觸發一次查詢
//     （singleflight-ish）。
//  3. 從未成功載入過（行程剛啟動，或至今每次刷新都失敗）：沒有舊值可退，只能同步查一次。
//
// 查詢失敗一律「保留舊值不覆蓋」（優雅降級），下一輪 TTL 到期才會再試——比對外回傳一份「查詢失敗
// 就變空」的資料安全。
package ttlcache

import (
	"context"
	"sync"
	"time"
)

// Cache 是一個型別安全的單一值 TTL 快取；T 通常是「一次查完、on-demand 供多個 key 查表用」的整包
// 資料（例如 map[string]string、map[string]RaceMeta），也可以只是單一結構（例如 SiteSettings）。
type Cache[T any] struct {
	ttl  time.Duration
	load func(ctx context.Context) (T, error)

	mu         sync.Mutex
	value      T
	valid      bool      // 是否曾經成功載入過至少一次
	at         time.Time // 最後一次成功載入時間（valid=false 時無意義）
	refreshing bool
	done       chan struct{} // 目前這輪刷新完成時會被 close；沒有刷新中則為 nil
}

// New 建立一個新的 Cache。load 是唯一的資料來源（通常包一層 DB 查詢），必須是併發安全的——它可能
// 同時被背景刷新 goroutine 與呼叫端的同步刷新路徑呼叫到（但兩者互斥，見 Refresh 的 singleflight）。
// ttl<=0 視為「永不過期」（只用於測試固定值，正式程式一律帶正值 TTL）。
func New[T any](ttl time.Duration, load func(ctx context.Context) (T, error)) *Cache[T] {
	return &Cache[T]{ttl: ttl, load: load}
}

// expired 純邏輯判斷（呼叫端須持有 c.mu）：從未成功載入過恆視為過期；否則看距上次成功載入是否
// 已達 TTL。抽成獨立方法方便單元測試直接灌 at/now/ttl 組合（見 ttlcache_test.go）。
func expired(valid bool, at, now time.Time, ttl time.Duration) bool {
	if !valid {
		return true
	}
	if ttl <= 0 {
		return false
	}
	return now.Sub(at) >= ttl
}

// Get 讀取目前值；規則見上方套件註解三種情況。ctx 只在「必須同步查詢」時（情況 2 的背景刷新除外）
// 真正派上用場——背景刷新固定用 context.Background()（見 startRefreshLocked 註解，避免呼叫端的
// request context 提早取消害刷新半途而廢）。
func (c *Cache[T]) Get(ctx context.Context) T {
	c.mu.Lock()
	if !expired(c.valid, c.at, time.Now(), c.ttl) {
		v := c.value
		c.mu.Unlock()
		return v
	}
	if !c.valid {
		// 從未成功載入過：沒有舊值可退，必須同步等這一次。
		c.mu.Unlock()
		return c.Refresh(ctx)
	}
	// 有舊值可用：背景刷新（若目前沒有人在刷新中），立刻回舊值。
	stale := c.value
	c.startRefreshLocked()
	c.mu.Unlock()
	return stale
}

// Peek 只讀目前值＋最後一次成功載入時間，完全不觸發任何刷新——供呼叫端自訂「要不要多做一次同步
// 刷新」的邏輯使用（例如賽事 meta 表：查無某 slug 時，只有在表格已經一段時間沒刷新過才值得多花
// 一次查詢去確認，見 internal/race GetRaceMeta）。
func (c *Cache[T]) Peek() (value T, at time.Time, valid bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value, c.at, c.valid
}

// Invalidate 標記目前值已過期（不清空既有值）——下一次 Get 會用舊值先回應、背景刷新；下一次
// Refresh 會同步重新查詢。保留舊值是刻意的：若剛好那一輪刷新查詢失敗（DB 短暫不可用），呼叫端仍
// 能拿到「稍舊但至少存在」的資料，不會突然變空。供寫入端（admin 改設定/賽事）呼叫。
func (c *Cache[T]) Invalidate() {
	c.mu.Lock()
	c.at = time.Time{}
	c.mu.Unlock()
}

// startRefreshLocked 若目前沒有刷新在進行中，起一個背景 goroutine 執行刷新。呼叫端須持有 c.mu
// （方法內不解鎖，回到 Get 才解鎖）。
func (c *Cache[T]) startRefreshLocked() {
	if c.refreshing {
		return
	}
	c.refreshing = true
	c.done = make(chan struct{})
	// 背景刷新固定用獨立的 context.Background()：原本觸發這次刷新的 request context 可能在這個
	// goroutine 跑完前就被取消（例如 HTTP handler 已經回應完畢），若沿用它會讓刷新常態性提早失敗。
	go c.runRefresh(context.Background())
}

// loadTimeout：單次 load 的硬上限。DB 若是「卡住」而非回錯（Neon 冷啟動停滯、半開連線、連線池耗盡），
// 沒有逾時的話 refreshing 會永遠為 true、done 永不關閉——快取從此只吐舊值、同步 Refresh 的呼叫端也會卡死
// （對抗式審查發現）。給每次 load 一個獨立期限，卡住就變成 DeadlineExceeded 走「保留舊值、下個 TTL 再試」。
const loadTimeout = 10 * time.Second

// loadBounded 以 loadTimeout 包住 load；ctx 若本身更短則以較短者為準。
func (c *Cache[T]) loadBounded(ctx context.Context) (T, error) {
	lctx, cancel := context.WithTimeout(ctx, loadTimeout)
	defer cancel()
	return c.load(lctx)
}

func (c *Cache[T]) runRefresh(ctx context.Context) {
	v, err := c.loadBounded(ctx)
	c.mu.Lock()
	if err == nil {
		c.value = v
		c.valid = true
		c.at = time.Now()
	}
	// err != nil：保留舊值（優雅降級）；c.at 沒被更新，下次 Get/Refresh 仍視為過期，之後會再試。
	c.refreshing = false
	done := c.done
	c.done = nil
	c.mu.Unlock()
	if done != nil {
		close(done)
	}
}

// Refresh 強制同步刷新一次並回傳結果（若刷新失敗則回舊值——從未成功載入過時就是 T 的 zero
// value）。並發呼叫全部收斂到同一輪刷新（singleflight）：後到的呼叫者不會各自再觸發一次查詢，
// 而是等同一輪刷新做完直接拿結果。
func (c *Cache[T]) Refresh(ctx context.Context) T {
	c.mu.Lock()
	if c.refreshing {
		done := c.done
		c.mu.Unlock()
		<-done
		c.mu.Lock()
		v := c.value
		c.mu.Unlock()
		return v
	}
	c.refreshing = true
	c.done = make(chan struct{})
	c.mu.Unlock()

	v, err := c.loadBounded(ctx)

	c.mu.Lock()
	if err == nil {
		c.value = v
		c.valid = true
		c.at = time.Now()
	}
	result := c.value
	done := c.done
	c.refreshing = false
	c.done = nil
	c.mu.Unlock()
	close(done)
	return result
}
