// 每日營運報告：固定台灣時間 08:00 送出一則 Telegram 摘要（會員／報名／營收／資料自檢／流量安全），
// 不論當天有沒有異常都送（區別於 selfcheck.go 的「只在異常時才告警」）。排程骨架完全比照
// selfcheck.go（hourly tick + advisory lock + in-memory 當日冪等標記，同一 08:00-08:59 執行窗口），
// 兩者共用同一個 Handler（因此 lastReportDate 是獨立欄位、advisory lock 是獨立 key，避免互相誤判
// 「今天已跑過」——見各自的常數/欄位註解）。
//
// 統計視窗＝前一個台灣日 00:00–24:00，用 Postgres AT TIME ZONE 'Asia/Taipei' 在 SQL 端換算成正確的
// UTC 邊界（見 buildDailyReportData 開頭那個查詢）：Neon 是完整的 Postgres，內建 IANA tzdata，這與
// 「Go 的 distroless 執行環境沒有 tzdata、time.LoadLocation 會炸」是兩回事，不衝突（Go 側仍照全站
// 慣例手算 UTC+8，只有這裡刻意借 Postgres 的時區函式做 SQL 端的日界線換算）。
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/notify"
)

const (
	// dailyReportTickInterval／dailyReportWindowHour：與 selfcheck 共用同一顆「每小時 tick、
	// 台灣 08:00-08:59 執行」的邏輯（直接沿用 inSelfCheckWindow，見該函式註解），沒有另外定義
	// 獨立常數的必要——兩者本來就設計成同一小時視窗內各自跑各自的（各自的 advisory lock/
	// lastXxxDate 互不影響），差別只在於「要不要在視窗內執行」共用同一個判斷式。
	dailyReportTickInterval = time.Hour

	// dailyReportAdvisoryLockName：獨立於 selfCheckAdvisoryLockName 的鎖名，避免兩個排程互搶同一把鎖。
	dailyReportAdvisoryLockName = "ops_daily_report"

	// telegramMaxLen：Telegram sendMessage 文字上限（官方文件為 4096 characters，以 Unicode 字元數計，
	// 非 byte）。用 utf8.RuneCountInString 量測而非 len()（後者是 UTF-8 byte 長度，中文報告用 byte
	// 量測會在遠低於 4096 字元時就被誤判超長，過度犧牲能塞進去的賽事筆數）。
	telegramMaxLen = 4096

	// dailyReportIPCapHint：與 internal/middleware.ipDailyMaxUniqueIPs 相同數值（未共用常數，比照
	// 全站「各檔各自複製一份」慣例）。報告端只能從查詢結果反推「昨日 distinct IP 數是否頂到這個
	// 上限」，藉此在報告中註記「數據可能不完整」，本身不影響聚合邏輯。
	dailyReportIPCapHint = 50000
)

// raceSignupLine 單一賽事「昨日新增報名」摘要列。
type raceSignupLine struct {
	Title string
	N     int // 新增報名數（不含 cancelled）
	M     int // 其中已付款數
}

// countryShare 單一國家昨日請求數（供 top5 分佈使用）。
type countryShare struct {
	Country  string
	Requests int
}

// ipFlagLine 可疑 IP 一列（登入失敗異常 or 單一 IP 高流量）。
type ipFlagLine struct {
	IP      string
	Country string
	Count   int
}

// trafficSummary 「流量安全」區塊的彙整資料，來自 ops_ip_daily（migration 145）。
type trafficSummary struct {
	HasData       bool // false＝該日在 ops_ip_daily 完全沒有資料（migration 剛上線 or 尚未有 flush）
	TotalRequests int
	DistinctIPs   int
	CountryTop    []countryShare
	AuthFailIPs   []ipFlagLine // auth_fails >= 20
	HighVolumeIPs []ipFlagLine // requests >= 5000
	NonTWFlag     bool         // 非 TW 國家佔比 > 30%
	NonTWPct      float64
	CapHit        bool // DistinctIPs 已達 dailyReportIPCapHint（推測昨日聚合上限被打到，資料可能不完整）
}

// dailyReportData 一份完整每日報告所需的全部資料（buildDailyReportData 產出，buildDailyReportMessage
// 純函式格式化成 Telegram 文字——兩階段分離方便單元測試訊息格式，不必真的連 DB）。
type dailyReportData struct {
	ReportDate string // 統計的台灣日（昨日），YYYY-MM-DD

	NewMembers   int
	TotalMembers int // 累計至統計視窗結束當下（即今日台灣 00:00 前）的總會員數

	RaceSignups []raceSignupLine

	RaceRevenueCents int
	RaceOrderCount   int
	VipRevenueCents  int
	VipOrderCount    int
	RefundCents      int
	RefundCount      int

	Checks []CheckResult // 重用 selfcheck 8 項檢查（runChecks）

	Traffic trafficSummary
}

// inDailyReportWindow 是否落在今天的執行窗口。直接沿用 selfcheck 的 08:00-08:59 判斷（見檔頭註解），
// 獨立包一層函式只是語意上讓呼叫端讀起來清楚在講「報告」而不是「自檢」，行為完全相同。
func inDailyReportWindow(t time.Time) bool {
	return inSelfCheckWindow(t)
}

// RunDailyReportLoop 背景每日報告排程，骨架同 RunSelfCheckLoop（見該函式註解）。
func (h *Handler) RunDailyReportLoop(ctx context.Context) {
	h.maybeRunDailyReport(ctx)
	t := time.NewTicker(dailyReportTickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.maybeRunDailyReport(ctx)
		}
	}
}

// maybeRunDailyReport 窗口 + 當日冪等閘門判斷，命中才真的產生並送出報告。雙層防重複邏輯與
// maybeRunDaily 完全對稱，唯一差異是用獨立的 lastReportDate 欄位與獨立的 advisory lock key
// （dailyReportAdvisoryLockName），避免跟 selfcheck 排程互相誤判「今天已跑過」。
func (h *Handler) maybeRunDailyReport(ctx context.Context) {
	now := taiwanNow()
	if !inDailyReportWindow(now) {
		return
	}
	today := now.Format("2006-01-02")

	h.mu.Lock()
	alreadyRan := h.lastReportDate == today
	h.mu.Unlock()
	if alreadyRan {
		return
	}

	conn, err := h.db.Acquire(ctx)
	if err != nil {
		log.Error().Err(err).Msg("ops dailyreport: acquire dedicated connection for advisory lock failed")
		return
	}
	defer conn.Release()

	var gotLock bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, dailyReportAdvisoryLockName).Scan(&gotLock); err != nil {
		log.Error().Err(err).Msg("ops dailyreport: try advisory lock failed")
		return
	}
	if !gotLock {
		log.Debug().Msg("ops dailyreport: another instance is already running/ran today's report, skip")
		return
	}
	defer func() {
		var unlocked bool
		if err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, dailyReportAdvisoryLockName).Scan(&unlocked); err != nil {
			log.Warn().Err(err).Msg("ops dailyreport: advisory unlock failed (will auto-release once this connection closes)")
		}
	}()

	h.mu.Lock()
	h.lastReportDate = today
	h.mu.Unlock()

	h.runAndSendDailyReport(ctx)
}

// runAndSendDailyReport 產生報告並直接用 notify.Telegram 送出（不透過 notify.Alert：Alert 有 30 分鐘
// per-kind 節流，是設計給「同類錯誤重複發生」場景用的，每日報告是排程單次觸發、每天固定要送，語意上
// 該直接呼叫不節流的 Telegram()，見任務規格與 notify/telegram.go 的行為）。產生報告本身失敗（DB 查詢
// 出錯）改用 Alert(kind="daily_report", ...) 通知——這種情況本身節流 30 分鐘是合理的，避免 DB 持續
// 異常時，每小時 tick 都重覆送一樣的失敗告警（雖然本排程一天只認領一次，這裡的節流主要是防禦性的）。
func (h *Handler) runAndSendDailyReport(ctx context.Context) {
	data, err := h.buildDailyReportData(ctx)
	if err != nil {
		log.Error().Err(err).Msg("ops dailyreport: build report failed")
		notify.Alert("daily_report", "每日營運報告產生失敗", err.Error())
		return
	}
	msg := buildDailyReportMessage(data)
	if err := notify.Telegram(ctx, msg); err != nil {
		log.Warn().Err(err).Msg("ops dailyreport: telegram send failed")
	}
}

// DailyReportNow POST /admin/ops/dailyreport：手動立即產生報告，回傳完整報告文字 JSON，並實際發送
// Telegram（供驗收——手動端點刻意跟排程本身走一樣的發送路徑，而不是只回傳文字不發送，否則驗收時無法
// 確認 Telegram 那端真的收得到）。不受每日窗口/冪等限制、也不佔用 lastReportDate（不影響排程本身
// 當天是否還會自動跑一次），比照 SelfCheckNow 的定位。
func (h *Handler) DailyReportNow(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	data, err := h.buildDailyReportData(ctx)
	if err != nil {
		log.Error().Err(err).Msg("ops dailyreport: manual trigger build failed")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "產生報告失敗：" + err.Error()})
		return
	}

	msg := buildDailyReportMessage(data)
	sendErr := notify.Telegram(ctx, msg)

	resp := map[string]any{
		"report_date": data.ReportDate,
		"report":      msg,
		"sent":        sendErr == nil,
	}
	if sendErr != nil {
		resp["send_error"] = sendErr.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// --- 資料查詢 ---

// buildDailyReportData 唯一負責碰 DB 的入口，依序查完 5 個區塊的資料。任何一段查詢失敗就整份回傳
// error（不像 selfcheck 的「單項失敗不影響其餘項」——報告是要給人看的完整敘事，缺一段還硬送出去，
// 不如整份失敗、退回改送一則「產生失敗」的告警，等下一輪 tick 或人工用手動端點重試）。
func (h *Handler) buildDailyReportData(ctx context.Context) (dailyReportData, error) {
	// 統計視窗＝「昨天」的台灣日 00:00-24:00，換算成 UTC 邊界（timestamptz 比較不看時區標籤，
	// 只看絕對時刻，這裡借 Postgres AT TIME ZONE 在 SQL 端做這個換算，見檔頭註解）。
	var windowStart, windowEnd time.Time
	if err := h.db.QueryRow(ctx, `
		SELECT
			date_trunc('day', (now() AT TIME ZONE 'Asia/Taipei')) AT TIME ZONE 'Asia/Taipei' - INTERVAL '1 day',
			date_trunc('day', (now() AT TIME ZONE 'Asia/Taipei')) AT TIME ZONE 'Asia/Taipei'
	`).Scan(&windowStart, &windowEnd); err != nil {
		return dailyReportData{}, fmt.Errorf("window bounds: %w", err)
	}

	// windowStart 本身就是「台灣昨日 00:00」對應的 UTC 絕對時刻，直接手算 UTC+8 取日期部分即為
	// ReportDate（"-" 分隔格式，同時用在 ops_ip_daily.day 的 SQL 比對與訊息標題；不用 formatTaipei()
	// 是因為它輸出 "/" 分隔且含時分，這裡只要日期）。
	d := dailyReportData{
		ReportDate: windowStart.UTC().Add(8 * time.Hour).Format("2006-01-02"),
	}

	// 1) 會員
	if err := h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE created_at >= $1 AND created_at < $2`,
		windowStart, windowEnd).Scan(&d.NewMembers); err != nil {
		return dailyReportData{}, fmt.Errorf("new members: %w", err)
	}
	if err := h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE created_at < $1`,
		windowEnd).Scan(&d.TotalMembers); err != nil {
		return dailyReportData{}, fmt.Errorf("total members: %w", err)
	}

	// 2) 報名：每賽事新增報名數（不含 cancelled）與其中已付款數。HAVING 排除「昨天雖有新報名但
	// 全部當天又被取消」的賽事（N=0 沒有列出的意義）。
	rows, err := h.db.Query(ctx, `
		SELECT r.title,
		       COUNT(*) FILTER (WHERE reg.status <> 'cancelled') AS n,
		       COUNT(*) FILTER (WHERE reg.status = 'paid') AS m
		FROM registrations reg
		JOIN races r ON r.id = reg.race_id
		WHERE reg.created_at >= $1 AND reg.created_at < $2
		GROUP BY r.id, r.title
		HAVING COUNT(*) FILTER (WHERE reg.status <> 'cancelled') > 0
		ORDER BY n DESC, r.title
	`, windowStart, windowEnd)
	if err != nil {
		return dailyReportData{}, fmt.Errorf("race signups: %w", err)
	}
	for rows.Next() {
		var line raceSignupLine
		if err := rows.Scan(&line.Title, &line.N, &line.M); err != nil {
			rows.Close()
			return dailyReportData{}, fmt.Errorf("race signups scan: %w", err)
		}
		d.RaceSignups = append(d.RaceSignups, line)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return dailyReportData{}, fmt.Errorf("race signups rows: %w", err)
	}
	rows.Close()

	// 3) 營收：orders.race_id IS NOT NULL＝賽事報名收入；race_id IS NULL＝VIP 訂閱收款
	// （migration 132：VIP 訂單為獨立訂單，無賽事，見 selfcheck.go checkStuckPendingVipOrders 同款判準）。
	if err := h.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(total_cents),0), COUNT(*) FROM orders
		WHERE race_id IS NOT NULL AND status='paid' AND paid_at >= $1 AND paid_at < $2
	`, windowStart, windowEnd).Scan(&d.RaceRevenueCents, &d.RaceOrderCount); err != nil {
		return dailyReportData{}, fmt.Errorf("race revenue: %w", err)
	}
	if err := h.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(total_cents),0), COUNT(*) FROM orders
		WHERE race_id IS NULL AND status='paid' AND paid_at >= $1 AND paid_at < $2
	`, windowStart, windowEnd).Scan(&d.VipRevenueCents, &d.VipOrderCount); err != nil {
		return dailyReportData{}, fmt.Errorf("vip revenue: %w", err)
	}
	// 退費：success（API 退刷成功）與 manual_done（人工已退款完成）皆代表錢真的退了；
	// manual_required（待人工處理）/failed 不計入已退金額（比照探查結果的口徑）。
	if err := h.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents),0), COUNT(*) FROM payment_refunds
		WHERE status IN ('success','manual_done') AND created_at >= $1 AND created_at < $2
	`, windowStart, windowEnd).Scan(&d.RefundCents, &d.RefundCount); err != nil {
		return dailyReportData{}, fmt.Errorf("refunds: %w", err)
	}

	// 4) 資料自檢：重用 selfcheck 的 8 項檢查（同一個 Handler、同一份 runChecks，見 selfcheck.go）。
	d.Checks = h.runChecks(ctx)

	// 5) 流量安全
	traffic, err := h.buildTrafficSummary(ctx, d.ReportDate)
	if err != nil {
		return dailyReportData{}, fmt.Errorf("traffic: %w", err)
	}
	d.Traffic = traffic

	return d, nil
}

// buildTrafficSummary 查 ops_ip_daily 昨日（day）資料，彙整成報告用的 trafficSummary。
func (h *Handler) buildTrafficSummary(ctx context.Context, day string) (trafficSummary, error) {
	var t trafficSummary
	if err := h.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(requests),0), COUNT(*) FROM ops_ip_daily WHERE day = $1`,
		day).Scan(&t.TotalRequests, &t.DistinctIPs); err != nil {
		return t, fmt.Errorf("totals: %w", err)
	}
	if t.DistinctIPs == 0 {
		// 該日完全沒有資料——可能是 migration 145 才剛上線（尚未有一次 flush），HasData 保持 false，
		// 報告端顯示「資料收集中（今日啟用）」。
		return t, nil
	}
	t.HasData = true
	if t.DistinctIPs >= dailyReportIPCapHint {
		t.CapHit = true
	}

	rows, err := h.db.Query(ctx, `
		SELECT country, SUM(requests) FROM ops_ip_daily
		WHERE day = $1
		GROUP BY country ORDER BY SUM(requests) DESC LIMIT 5
	`, day)
	if err != nil {
		return t, fmt.Errorf("country top5: %w", err)
	}
	for rows.Next() {
		var c countryShare
		if err := rows.Scan(&c.Country, &c.Requests); err != nil {
			rows.Close()
			return t, fmt.Errorf("country top5 scan: %w", err)
		}
		t.CountryTop = append(t.CountryTop, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return t, fmt.Errorf("country top5 rows: %w", err)
	}
	rows.Close()

	// 非 TW 佔比：獨立查 TW 總量（不依賴上面的 top5——極端情況下 TW 可能被擠出前 5 名）。
	var twRequests int
	if err := h.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(requests),0) FROM ops_ip_daily WHERE day = $1 AND country = 'TW'`,
		day).Scan(&twRequests); err != nil {
		return t, fmt.Errorf("tw requests: %w", err)
	}
	if t.TotalRequests > 0 {
		t.NonTWPct = float64(t.TotalRequests-twRequests) / float64(t.TotalRequests)
		if t.NonTWPct > 0.3 {
			t.NonTWFlag = true
		}
	}

	authRows, err := h.db.Query(ctx, `
		SELECT ip, country, auth_fails FROM ops_ip_daily
		WHERE day = $1 AND auth_fails >= 20
		ORDER BY auth_fails DESC LIMIT $2
	`, day, sampleLimit)
	if err != nil {
		return t, fmt.Errorf("auth fail ips: %w", err)
	}
	for authRows.Next() {
		var f ipFlagLine
		if err := authRows.Scan(&f.IP, &f.Country, &f.Count); err != nil {
			authRows.Close()
			return t, fmt.Errorf("auth fail ips scan: %w", err)
		}
		t.AuthFailIPs = append(t.AuthFailIPs, f)
	}
	if err := authRows.Err(); err != nil {
		authRows.Close()
		return t, fmt.Errorf("auth fail ips rows: %w", err)
	}
	authRows.Close()

	volRows, err := h.db.Query(ctx, `
		SELECT ip, country, requests FROM ops_ip_daily
		WHERE day = $1 AND requests >= 5000
		ORDER BY requests DESC LIMIT $2
	`, day, sampleLimit)
	if err != nil {
		return t, fmt.Errorf("high volume ips: %w", err)
	}
	for volRows.Next() {
		var f ipFlagLine
		if err := volRows.Scan(&f.IP, &f.Country, &f.Count); err != nil {
			volRows.Close()
			return t, fmt.Errorf("high volume ips scan: %w", err)
		}
		t.HighVolumeIPs = append(t.HighVolumeIPs, f)
	}
	if err := volRows.Err(); err != nil {
		volRows.Close()
		return t, fmt.Errorf("high volume ips rows: %w", err)
	}
	volRows.Close()

	return t, nil
}

// --- 訊息格式化（純函式，不碰 DB，方便單元測試） ---

// checkFailureLines 把 selfcheck 8 項結果中「異常」的項目格式化成逐行文字，重用 selfcheck.go 既有的
// checkLabel 中文標籤 map（該 map 涵蓋全部 8 項，含 activityHeartbeatCheck）。
func checkFailureLines(checks []CheckResult) []string {
	var lines []string
	for _, c := range checks {
		if c.OK {
			continue
		}
		label := checkLabel[c.Name]
		if label == "" {
			label = c.Name
		}
		detail := c.Detail
		if detail == "" {
			detail = "（無詳情）"
		}
		lines = append(lines, fmt.Sprintf("• %s：%s", label, detail))
	}
	return lines
}

// formatCents 分 → NT$ 整數字串（無條件捨去小數，金流分毫至多以「分」計，實務上都是整數分）。
func formatCents(cents int) string {
	return fmt.Sprintf("%d", cents/100)
}

// formatTrafficSection 組「🌐 流量安全」區塊內文（不含標題行）。
func formatTrafficSection(t trafficSummary) string {
	if !t.HasData {
		return "資料收集中（今日啟用）\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "總請求 %d、獨立 IP %d\n", t.TotalRequests, t.DistinctIPs)

	if len(t.CountryTop) > 0 && t.TotalRequests > 0 {
		parts := make([]string, 0, len(t.CountryTop))
		for _, c := range t.CountryTop {
			pct := float64(c.Requests) / float64(t.TotalRequests) * 100
			parts = append(parts, fmt.Sprintf("%s %.0f%%", c.Country, pct))
		}
		b.WriteString("國家分佈 top5：" + strings.Join(parts, "、") + "\n")
	}

	var warn []string
	for _, f := range t.AuthFailIPs {
		warn = append(warn, fmt.Sprintf("登入失敗異常 IP %s(%s) %d 次", f.IP, f.Country, f.Count))
	}
	for _, f := range t.HighVolumeIPs {
		warn = append(warn, fmt.Sprintf("單一IP高流量 %s(%s) %d 次請求", f.IP, f.Country, f.Count))
	}
	if t.NonTWFlag {
		warn = append(warn, fmt.Sprintf("非TW流量佔比 %.0f%%", t.NonTWPct*100))
	}
	if t.CapHit {
		warn = append(warn, fmt.Sprintf("昨日IP去重疑似已達聚合上限（%d），部分流量可能未計入統計", dailyReportIPCapHint))
	}

	if len(warn) == 0 {
		b.WriteString("⚠️ 可疑清單：無異常事件\n")
	} else {
		b.WriteString("⚠️ 可疑清單：\n")
		for _, w := range warn {
			b.WriteString("• " + w + "\n")
		}
	}
	return b.String()
}

// assembleDailyReportMessage 組完整訊息文字。raceKeep 控制「報名」區塊實際列出的賽事筆數（供
// buildDailyReportMessage 在超長時逐步減少列出筆數用）；raceKeep >= len(d.RaceSignups) 時等同全列。
func assembleDailyReportMessage(d dailyReportData, raceKeep int) string {
	var b strings.Builder

	fmt.Fprintf(&b, "📊 DOR 每日營運報告（%s）\n\n", d.ReportDate)

	fmt.Fprintf(&b, "👥 會員：昨日新增 %d 位（累計 %d）\n\n", d.NewMembers, d.TotalMembers)

	b.WriteString("🏃 報名：\n")
	if len(d.RaceSignups) == 0 {
		b.WriteString("昨日無新增報名\n")
	} else {
		shown := d.RaceSignups
		hidden := 0
		if raceKeep < len(d.RaceSignups) {
			if raceKeep < 0 {
				raceKeep = 0
			}
			shown = d.RaceSignups[:raceKeep]
			hidden = len(d.RaceSignups) - raceKeep
		}
		for _, line := range shown {
			fmt.Fprintf(&b, "《%s》+%d（已付 %d）\n", line.Title, line.N, line.M)
		}
		if hidden > 0 {
			fmt.Fprintf(&b, "…其餘 %d 場\n", hidden)
		}
	}
	b.WriteString("\n")

	total := d.RaceRevenueCents + d.VipRevenueCents
	b.WriteString("💰 營收：\n")
	fmt.Fprintf(&b, "報名收入 NT$%s（%d 筆）\n", formatCents(d.RaceRevenueCents), d.RaceOrderCount)
	fmt.Fprintf(&b, "VIP 訂閱收款 NT$%s（%d 筆）\n", formatCents(d.VipRevenueCents), d.VipOrderCount)
	fmt.Fprintf(&b, "合計 NT$%s\n", formatCents(total))
	if d.RefundCount > 0 {
		fmt.Fprintf(&b, "退費 NT$%s（%d 筆）\n", formatCents(d.RefundCents), d.RefundCount)
	}
	b.WriteString("\n")

	b.WriteString("🔍 資料自檢：\n")
	if failed := checkFailureLines(d.Checks); len(failed) == 0 {
		b.WriteString("8 項全數正常 ✅\n")
	} else {
		for _, l := range failed {
			b.WriteString(l + "\n")
		}
	}
	b.WriteString("\n")

	b.WriteString("🌐 流量安全：\n")
	b.WriteString(formatTrafficSection(d.Traffic))

	return strings.TrimRight(b.String(), "\n")
}

// buildDailyReportMessage 組出最終要送出的 Telegram 文字，超過 telegramMaxLen 時逐步減少「報名」
// 區塊列出的賽事筆數（保留新增數較高的前面幾場，因 RaceSignups 已依 N DESC 排序）直到符合上限，
// 並附註「…其餘 N 場」——只裁這一段可變長度的部分，其餘固定段落（會員/營收/自檢/流量）維持完整。
func buildDailyReportMessage(d dailyReportData) string {
	full := assembleDailyReportMessage(d, len(d.RaceSignups))
	if utf8.RuneCountInString(full) <= telegramMaxLen {
		return full
	}
	for keep := len(d.RaceSignups) - 1; keep >= 0; keep-- {
		candidate := assembleDailyReportMessage(d, keep)
		if utf8.RuneCountInString(candidate) <= telegramMaxLen {
			return candidate
		}
	}
	// 理論上不會走到這裡（其餘固定段落遠小於 telegramMaxLen）；防禦性地按 rune 截斷，
	// 避免整則訊息因超長被 Telegram API 拒收而完全送不出去。
	runes := []rune(full)
	if len(runes) > telegramMaxLen {
		return string(runes[:telegramMaxLen])
	}
	return full
}
