package middleware

import (
	"context"
	"net/http"
	"sync"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/reqip"
)

const (
	// ipDailyFlushInterval：in-memory 累計多久批次 UPSERT 一次進 ops_ip_daily（migration 145）。
	ipDailyFlushInterval = 5 * time.Minute

	// ipDailyMaxUniqueIPs：單日相異 IP 上限。超過後「新」IP（今天沒見過的）不再計入，只累計已見過
	// 的既有 IP，避免惡意灑大量偽造 IP（例如帶假 X-Forwarded-For）把 in-memory map 撐爆記憶體。
	// internal/ops/dailyreport.go 有一份相同數值（未共用常數，比照全站「各檔各自複製一份」慣例），
	// 報告會用 DistinctIPs >= 此值反推「昨日可能已達上限」並在報告中註記。
	ipDailyMaxUniqueIPs = 50000

	// ipDailyRetentionDays：ops_ip_daily 保留天數，由背景迴圈每天順手 DELETE 過期資料。
	ipDailyRetentionDays = 30
)

// ipDailyExcludedPaths：高頻探測端點不計入聚合，避免統計失真、也避免健康檢查探針白白佔用單日 IP 額度。
var ipDailyExcludedPaths = map[string]bool{
	"/health":    true,
	"/health/db": true,
}

// ipDailyCounts 單一 IP 在目前累計視窗內的統計（flush 後歸零重算）。
type ipDailyCounts struct {
	country   string
	requests  int
	authFails int
	notFound  int
}

// IPDailyAggregate 每請求 in-memory 聚合 IP／國家／請求數／401/403/404 次數，背景每
// ipDailyFlushInterval 批次 UPSERT 進 ops_ip_daily，供 internal/ops/dailyreport.go 每日報告的
// 「流量安全」區塊與異常流量巡檢使用。純邏輯（Record）與 HTTP 包裝（Middleware）分離，方便單元測試。
type IPDailyAggregate struct {
	db *pgxpool.Pool

	mu   sync.Mutex
	day  string                    // 目前累計中的台灣日（YYYY-MM-DD）；換日時重置 knownIPs/capped
	data map[string]*ipDailyCounts // ip -> 尚未 flush 的累計（flush 後清空）

	knownIPs map[string]struct{} // 今天已見過的 IP 集合，跨 flush 持續累積（data 會被 flush 清空，
	// 但「今天見過哪些 IP」必須整天持續追蹤，否則上限每次 flush 後歸零形同虛設）；即使頂到
	// ipDailyMaxUniqueIPs 上限，這個 set 本身大小也就是上限值，不會無界成長。
	capped bool // 今天是否已達 ipDailyMaxUniqueIPs 上限（供報告註記）

	lastCleanupDay string // 最近一次執行「刪除過期資料」的台灣日，避免每次 flush 都重跑一次 DELETE
}

// NewIPDailyAggregate 建構子。
func NewIPDailyAggregate(db *pgxpool.Pool) *IPDailyAggregate {
	return &IPDailyAggregate{
		db:       db,
		data:     make(map[string]*ipDailyCounts),
		knownIPs: make(map[string]struct{}),
	}
}

// taiwanDay 台灣日期字串 YYYY-MM-DD（UTC+8 手算，理由同 internal/ops/selfcheck.go taiwanNow）。
func taiwanDay(t time.Time) string {
	return t.UTC().Add(8 * time.Hour).Format("2006-01-02")
}

// Record 記錄一次請求（純邏輯，Middleware 的實際呼叫點；獨立成方法方便不必真的發 HTTP 請求即可單測）。
func (a *IPDailyAggregate) Record(now time.Time, ip, country string, status int) {
	day := taiwanDay(now)

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.day != day {
		// 換日：重置每日 IP 追蹤與上限旗標。data 理論上已在前一天最後一次 flush 時清空，這裡保險
		// 再清一次，防止背景 flush 恰好還沒跑到、換日瞬間把新一天的資料錯記到舊 day 的極短暫窗口。
		a.day = day
		a.data = make(map[string]*ipDailyCounts)
		a.knownIPs = make(map[string]struct{})
		a.capped = false
	}

	if _, known := a.knownIPs[ip]; !known {
		if len(a.knownIPs) >= ipDailyMaxUniqueIPs {
			a.capped = true
			return // 新 IP 且今天已達上限：整筆不計入，避免 map 無界成長
		}
		a.knownIPs[ip] = struct{}{}
	}

	c, ok := a.data[ip]
	if !ok {
		c = &ipDailyCounts{country: country}
		a.data[ip] = c
	}
	c.requests++
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		c.authFails++
	case http.StatusNotFound:
		c.notFound++
	}
}

// Middleware 包住整個路由鏈，記錄每請求的真實 IP／CF-IPCountry／狀態碼。掛法比照 FiveXXAlert：用
// chimw.WrapResponseWriter 取得實際狀態碼，健康檢查等高頻探測路徑（ipDailyExcludedPaths）不計入。
func (a *IPDailyAggregate) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ipDailyExcludedPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		country := r.Header.Get("CF-IPCountry")
		if country == "" {
			country = "??"
		}
		a.Record(time.Now(), reqip.ClientIP(r), country, ww.Status())
	})
}

// flush 把目前累計的 in-memory 資料批次 UPSERT 進 ops_ip_daily，不論成功與否都清空 map（避免同一批
// 資料反覆重試造成 UPSERT 重複累加）。失敗只 log，不告警、不影響請求路徑——流量統計是輔助性的營運
// 可見度功能，不該讓它的故障牽連任何請求。
func (a *IPDailyAggregate) flush(ctx context.Context) {
	a.mu.Lock()
	day := a.day
	snapshot := a.data
	a.data = make(map[string]*ipDailyCounts)
	a.mu.Unlock()

	if day == "" || len(snapshot) == 0 {
		return
	}

	batch := &pgx.Batch{}
	for ip, c := range snapshot {
		batch.Queue(`
			INSERT INTO ops_ip_daily (day, ip, country, requests, auth_fails, not_found)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (day, ip) DO UPDATE SET
				requests   = ops_ip_daily.requests + EXCLUDED.requests,
				auth_fails = ops_ip_daily.auth_fails + EXCLUDED.auth_fails,
				not_found  = ops_ip_daily.not_found + EXCLUDED.not_found,
				country    = EXCLUDED.country
		`, day, ip, c.country, c.requests, c.authFails, c.notFound)
	}

	br := a.db.SendBatch(ctx, batch)
	for range snapshot {
		if _, err := br.Exec(); err != nil {
			log.Warn().Err(err).Msg("ops ip daily: flush upsert failed")
		}
	}
	if err := br.Close(); err != nil {
		log.Warn().Err(err).Msg("ops ip daily: flush batch close failed")
	}
}

// cleanupOldDays 刪除超過 ipDailyRetentionDays 保留期限的舊資料。用 lastCleanupDay 限制成每天只在
// 換日後第一次 flush 時真的執行一次 DELETE，避免每 5 分鐘都重跑一次全表掃描（多數時候沒東西可刪）。
func (a *IPDailyAggregate) cleanupOldDays(ctx context.Context, today string) {
	a.mu.Lock()
	if a.lastCleanupDay == today {
		a.mu.Unlock()
		return
	}
	a.lastCleanupDay = today
	a.mu.Unlock()

	if _, err := a.db.Exec(ctx,
		`DELETE FROM ops_ip_daily WHERE day < ($1::date - $2 * INTERVAL '1 day')`,
		today, ipDailyRetentionDays,
	); err != nil {
		log.Warn().Err(err).Msg("ops ip daily: cleanup old rows failed")
	}
}

// Run 背景排程：每 ipDailyFlushInterval flush 一次 in-memory 聚合結果，並在每天第一次 flush 時順手
// 清理過期資料。ctx 取消時另開一個沒有時限的 context 做最後一次 flush（盡量不遺漏 graceful shutdown
// 當下還沒寫進 DB 的資料）再結束。比照 internal/ops 排程 loop 的骨架（RunSelfCheckLoop 等）。
func (a *IPDailyAggregate) Run(ctx context.Context) {
	t := time.NewTicker(ipDailyFlushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			a.flush(context.Background())
			return
		case <-t.C:
			a.flush(ctx)
			a.cleanupOldDays(ctx, taiwanDay(time.Now()))
		}
	}
}
