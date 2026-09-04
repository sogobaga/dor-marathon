// Package dbwake 追蹤「是誰把睡著的 Neon compute 喚醒」（2026-09-04，owner 要求「請持續查清楚原因」）。
//
// 背景：Neon serverless compute 閒置 5 分鐘會自動休眠（scale-to-zero），下一個查詢進來才會冷啟動
// 喚醒。v779 之後我們在多處加了行程內 TTL 快取（appsettings 公開設定 map／profile
// site_settings／race meta 表，見 internal/ttlcache），照理應該讓穩態下 DB 查詢頻率大幅降低；但
// 觀察到 compute 晚上仍然每 10-16 分鐘醒一次——只要有「任何」request 打進來（爬蟲/監控探針），
// 該次查詢會落在快取過期後、直接觸發一次 DB 查詢，快取本身反而變成「每 TTL 週期至少查一次」的
// 固定喚醒源（背景排程 ticker 同理）。
//
// 問題是「看不到是誰喚醒的」：Railway httpLogs 只保留最新一次部署，事後對不回時間點。這個套件掛在
// pgx 的 QueryTracer 上，只要偵測到「距上次查詢已經超過 Neon 的休眠門檻」，就代表這次查詢極可能是
// 剛把 compute 從睡眠中喚醒的那一下——記一行 Info log，帶上是哪個 HTTP request（method/path/ua/ip，
// 由 Middleware 存進 context）或哪個背景排程（job name，由 WithJob 存進 context）觸發的，供之後
// 從 log 直接反推喚醒來源與週期，不必再靠猜。
//
// 成本刻意壓到最低：這支 tracer 掛在「每一條 SQL 查詢」的路徑上（TraceQueryStart 對 Query/
// QueryRow/Exec 都會呼叫到），任何額外開銷都會被請求量放大——沒喚醒時只有一次 atomic swap，不配置
// 任何東西；只有真的判定為喚醒才走 log 分支（此時本來就要付一次 DB 冷啟動的代價，log 開銷可忽略）。
//
// ⚠️ 絕不 log SQL 文字本身（data.SQL）——這支只做「喚醒歸因」，不是查詢審計/效能追蹤。
package dbwake

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/reqip"
)

// WakeThreshold 判斷「這次查詢很可能剛把 Neon compute 從睡眠中喚醒」的閒置門檻。刻意設在略低於
// Neon 實際休眠門檻（5 分鐘）的 4 分鐘：抓「很可能剛醒」而不是「保證剛醒」——量測時間點（上一次
// TraceQueryStart 到這一次之間）本身就有幾秒到幾十秒的抖動空間，門檻抓太貼近 5 分鐘容易因為抖動
// 漏記真正的喚醒；抓 4 分鐘的代價只是極少數「其實還沒真的休眠」的查詢也被記一行 log，對除錯喚醒
// 來源這個目的來說寧可多記不要漏記。
const WakeThreshold = 4 * time.Minute

// maxUALen 記進 log 的 User-Agent 長度上限（decisions 明訂 ≤80 字元）——喚醒歸因用得到「大致是
// 什麼來源」（爬蟲/監控探針/瀏覽器）就夠，不需要完整 UA 字串塞爆 log。
const maxUALen = 80

// ctxKey 是本套件 context value 的 key 型別（未匯出，避免與其他套件的 context key 意外相撞；比照
// internal/middleware 既有 roleKey{} 的做法）。
type ctxKey int

const (
	ctxKeyReqAttr ctxKey = iota // HTTP 請求歸因（見 reqAttr），由 Middleware 存入
	ctxKeyJob                   // 背景排程歸因（job name 字串），由 WithJob 存入
)

// reqAttr 是塞進 request context 的 HTTP 請求端喚醒歸因資訊。
type reqAttr struct {
	method string
	path   string
	ua     string
	ip     string
}

// Middleware 把目前請求的 method/path/UA/IP 存進 context，供 Tracer 在偵測到喚醒查詢時取用來
// 歸因。掛法：緊接在 chi 的 RealIP 之後（見 cmd/api/main.go）——reqip.ClientIP 在沒有
// CF-Connecting-IP 時會退回 r.RemoteAddr，需要 RealIP 先把它正規化過。
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.UserAgent()
		if len(ua) > maxUALen {
			ua = ua[:maxUALen]
		}
		attr := reqAttr{method: r.Method, path: r.URL.Path, ua: ua, ip: reqip.ClientIP(r)}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyReqAttr, attr)))
	})
}

// WithJob 標記這個 ctx 底下發出的查詢屬於哪個背景排程（供 Tracer 在偵測到喚醒查詢時歸因為具名
// job，而非籠統的「background」）。供 cmd/api/main.go 包住各 ticker 的 ctx 使用，例如：
//
//	go raceSvc.RunEntryRewardLoop(dbwake.WithJob(bgCtx, "entry_reward"))
func WithJob(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, ctxKeyJob, name)
}

// Detach 把請求／排程的歸因值搬到一個「不會隨呼叫端取消」的新 context 上，給 ttlcache 的背景
// stale-while-revalidate 刷新用：刷新刻意脫離觸發它的 request context（避免 request 結束就把刷新砍掉），
// 但若直接用 context.Background()，最需要歸因的三個快取（公開設定／站台設定／賽事 meta）的 DB 喚醒
// 就會全部變成匿名 background（審查抓到）。只複製本套件的兩個 value、不帶 deadline/cancel。
func Detach(ctx context.Context) context.Context {
	out := context.Background()
	if v := ctx.Value(ctxKeyReqAttr); v != nil {
		out = context.WithValue(out, ctxKeyReqAttr, v)
	}
	if v := ctx.Value(ctxKeyJob); v != nil {
		out = context.WithValue(out, ctxKeyJob, v)
	}
	return out
}

// Tracer 實作 pgx.QueryTracer，掛在 pgxpool 的 ConnConfig.Tracer（見
// internal/db/postgres.go）。單一欄位、單一 atomic：沒有喚醒判定為真時，整個 TraceQueryStart
// 只有一次 atomic swap，不配置任何東西。
type Tracer struct {
	// lastNano 最近一次查詢開始的 UnixNano 時間戳（atomic，跨 goroutine 併發存取）。
	lastNano atomic.Int64
}

// NewTracer 建構一個新的 Tracer。lastNano 初始化為「建構當下」而非零值：呼叫端（db.Connect）在
// 建構完 Tracer 之後才會真的建池並 Ping 通 DB，也就是說 DB 在這個時間點附近本來就是醒著的——若
// 初始值留零值（Unix 元年），第一支被追蹤到的查詢會算出「閒置了幾十年」這種沒有意義的離群值，把它
// 誤判成一次喚醒並記一行語意不對的 log。
func NewTracer() *Tracer {
	t := &Tracer{}
	t.lastNano.Store(time.Now().UnixNano())
	return t
}

// decide 純函式：距上次查詢是否已達喚醒門檻。獨立成純函式方便單元測試涵蓋邊界值（見
// dbwake_test.go），不必真的等 4 分鐘或動用 atomic/time.Now()。
func decide(lastNano, nowNano, thresholdNano int64) (wake bool) {
	return nowNano-lastNano >= thresholdNano
}

// TraceQueryStart 見 pgx.QueryTracer。每次 Query/QueryRow/Exec 呼叫的第一步都會經過這裡。
func (t *Tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	nowNano := time.Now().UnixNano()
	last := t.lastNano.Swap(nowNano)
	if decide(last, nowNano, int64(WakeThreshold)) {
		logWake(ctx, time.Duration(nowNano-last))
	}
	return ctx
}

// TraceQueryEnd 見 pgx.QueryTracer。喚醒歸因只需要「查詢開始」這一刻，查詢結束後不需要額外動作。
func (t *Tracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {}

// logWake 記一行 Info log：閒置秒數（idle_s，machine-readable）＋人類可讀的 since_last_query
// duration 字串（兩者算的是同一段閒置時間，分開放是方便有人直接掃 log 肉眼看週期，不用心算秒數），
// 外加來源歸因——HTTP 請求（method/path/ua/ip）或背景排程（job name，缺 WithJob 時只標
// background）。刻意不帶任何 SQL 相關欄位。
func logWake(ctx context.Context, idle time.Duration) {
	ev := log.Info().
		Int64("idle_s", int64(idle.Seconds())).
		Str("since_last_query", idle.Round(time.Second).String())
	if attr, ok := ctx.Value(ctxKeyReqAttr).(reqAttr); ok {
		ev = ev.Str("source", "request").
			Str("method", attr.method).
			Str("path", attr.path).
			Str("ua", attr.ua).
			Str("ip", attr.ip)
	} else if job, ok := ctx.Value(ctxKeyJob).(string); ok {
		ev = ev.Str("source", "background").Str("job", job)
	} else {
		ev = ev.Str("source", "background")
	}
	ev.Msg("db wake after idle")
}
