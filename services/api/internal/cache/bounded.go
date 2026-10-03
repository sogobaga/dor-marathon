package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// bounded.go：「真的」會逾時的 Redis 往返。
//
// ## 問題（2026-10-02，團練同步跑的真實 Redis 故障測試抓到）
//
// go-redis v9 對 socket 讀寫**不看 context 的期限**，除非 Options.ContextTimeoutEnabled=true：
// baseClient.context(ctx) 在該選項為 false 時回傳 context.Background()（見 go-redis redis.go），
// 所以 context.WithTimeout(ctx, 1*time.Second) 只管得到「撥號」「等連線池」「重試之間的 sleep」，
// 管不到「已經送出指令、等 Redis 回話」的那一段。Redis 卡死（網路分區、被暫停、failover 中）時，
// 一個指令實測（go-redis v9.22 預設選項）：已建立的連線卡死 ≈ 10 秒（指令讀取逾時 5 秒 + 重試在新連線的
// 握手上再等 5 秒）、全新連線 ≈ 5 秒；全站每條限流路由都在
// 請求路徑上多次碰 Redis，兩三個疊起來就吃光 chi 的 30 秒 Timeout，後面的 DB 查詢拿到已取消的
// context 而 500。連 auth.checkDenylist 那個「300ms 獨立上限」也是同一個機制，同樣不是真的
// （卡死時實際會等一整個 ReadTimeout）。
//
// ## 做法：每個呼叫點用「共用連線池、讀寫逾時縮短」的 client 副本 + context 期限
//
//	b := cache.NewBounded(rdb, 1*time.Second)
//	ctx, cancel := b.Ctx(r.Context())          // 期限 = 1 s（或呼叫端更早的期限）
//	defer cancel()
//	err := script.Run(ctx, b.C, keys, args...)  // 讀寫逾時 = 1 s：socket 層真的會斷
//
//   - b.C 是 rdb.WithTimeout(d)：go-redis 官方 API，**與原 client 共用同一個連線池**（沒有多開連線、
//     沒有 goroutine），只是 ReadTimeout／WriteTimeout 改成 d。卡死時讀取在 d 後逾時；
//   - 同時給 context 期限 d，讓 go-redis 的重試迴圈在第一次失敗後就停（重試前的 internal.Sleep(ctx)
//     與下一輪 getConn(ctx) 都會看到期限已過），整個往返 ≈ d，而不是兩倍。
//
// 兩者缺一不可：只有 context 期限 → 卡死時還是等 ReadTimeout（實測 150ms 期限 + 1.2s ReadTimeout → 1.21s）；
// 只有 WithTimeout → 已建立的連線卡死時，指令讀取逾時後重試又在新連線的握手上等一次（實測 300ms → 650ms ≈ 2d）。
//
// ## 為什麼不直接開 ContextTimeoutEnabled=true（全域選項）
//
// 它會讓所有帶期限的 context 在 socket 層生效，等於把 auth 的 300ms（admin 失敗即關閉＝503）、
// race／integrations 的請求 context 等「從來沒真的生效過」的上限一次全部啟用，行為改變面是整個
// 後端而且無法在這裡逐一驗證；Pub/Sub、worker 的長連線讀取也要逐一確認不受影響。局部副本只改
// 「明確要求快速失敗」的呼叫點，爆炸半徑是零。要全域啟用，請另案評估後再做。
//
// ## 不要做的事
//
//   - **不要對 b.C 呼叫 Close()**：它與原 client 共用連線池，關掉會把整個應用的 Redis 連線池一起關掉。
//   - 逾時只代表「client 不等了」：指令可能已在 Redis 端執行。呼叫端的操作必須是冪等的
//     （本專案的限流 INCR、同步跑的 start／pos／leave／revoke 都是）。
//   - 不要用 goroutine + select 包住呼叫來「模擬」逾時：呼叫者提早返回後，goroutine 仍會占著連線
//     直到 ReadTimeout，卡死期間會堆積；本做法沒有任何額外 goroutine。

// Bounded 一個「每次往返都有硬期限」的 Redis 存取點。零值不可用，請用 NewBounded。
type Bounded struct {
	// C 與原 client 共用連線池；ReadTimeout／WriteTimeout = D。**不得 Close。**
	C *redis.Client
	// D 每次往返的硬期限。
	D time.Duration
}

// NewBounded 以 rdb 建立期限為 d 的存取點。rdb 為 nil 回 nil（呼叫端視為「Redis 不可用」）。
func NewBounded(rdb *redis.Client, d time.Duration) *Bounded {
	if rdb == nil {
		return nil
	}
	return &Bounded{C: rdb.WithTimeout(d), D: d}
}

// Ctx 回傳期限為 min(parent 的期限, now+D) 的子 context；呼叫端必須呼叫回傳的 cancel。
// 用 parent 的值（trace id 等），但**不要**把這個子 context 傳給後續處理——它只屬於這一次 Redis 往返
// （例如限流中介層在 Redis 逾時後要用「原始」請求 context 繼續往下游走）。
func (b *Bounded) Ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, b.D)
}
