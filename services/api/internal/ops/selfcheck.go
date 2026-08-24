// Package ops 提供正式上線後的無人巡檢：每日資料一致性自檢排程。
//
// 背景：金流/報名相關的多筆表格（orders/payment_transactions/vip_subscriptions/
// vip_renewal_attempts/registrations）之間有隱含的一致性約束（例如「已付訂單必有對應訂閱」、
// 「取消的報名其訂單不該還停在 paid」），這些約束平常靠各自流程的交易/CAS 保證，但外部金流
// callback 逾時、程序中斷、人工介入等邊角情況仍可能造成短暫或永久的不一致。本套件每天固定時間
// 跑一輪唯讀健檢 SQL，異常直接 Telegram 告警給人工複查，取代「等使用者投訴才發現」。
//
// 排程骨架比照 internal/payment/vip_renewal.go RunRenewalLoop：pg_try_advisory_lock 防多實例
// （Railway 水平擴展）重複執行；額外疊加 in-memory「今天是否已跑過」標記，把執行窗口收斂在台灣
// 時間 08:00-08:59 這一小時內（避免服務重啟時的「啟動立即跑一次」把巡檢跑到深夜/離峰之外的時段）。
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/notify"
)

const (
	// selfCheckTickInterval 每小時檢查一次「現在是否落在今天的執行窗口內」。窗口本身只有一小時寬
	// （見 inSelfCheckWindow），每小時 tick 一次足夠準確命中，不需要更密集。
	selfCheckTickInterval = time.Hour

	// selfCheckAdvisoryLockName pg_try_advisory_lock 用的鎖名（經 hashtext 轉成 lock id）。
	selfCheckAdvisoryLockName = "ops_daily_selfcheck"

	// selfCheckWindowHour 執行窗口：台灣時間 08:00-08:59（該小時內第一次 tick 命中即執行，
	// 手算 now.Hour()==8，UTC+8 換算見 taiwanNow）。
	selfCheckWindowHour = 8

	// sampleLimit 每項檢查列出的異常樣本上限（避免單則告警訊息過長）。
	sampleLimit = 5

	// activityHeartbeatCheck 第 8 項「活動流異常（營運心跳）」的檢查名稱，供 summarizeSelfCheck
	// 認出並與其他 7 項分開處理（獨立 kind 告警，不彙整進同一則）。
	activityHeartbeatCheck = "activity_heartbeat"
)

// checkLabel 各檢查項目的中文說明，供告警訊息與 log 使用（Name 本身維持穩定的英文 slug，
// 供 JSON API／未來程式化比對使用，比照 adminacct.go auditResourceLabel 的做法分開兩者）。
var checkLabel = map[string]string{
	"pending_vip_orders_stuck":          "卡住的pending VIP訂單(>24h)",
	"paytx_long_pending":                "payment_transactions長期pending(>48h)",
	"renewal_attempts_stale_processing": "VIP續約attempts跨日殘留processing",
	"paid_order_without_subscription":   "VIP訂單已付但無訂閱紀錄",
	"active_subscription_without_card":  "active VIP訂閱但無active綁卡",
	"webhook_failure_rate":              "近24h付款webhook失敗率",
	"cancel_refund_mismatch":            "取消報名但訂單未同步(仍為paid)",
	activityHeartbeatCheck:              "近24h活動上傳心跳",
}

// CheckResult 單一檢查項目的結果。
type CheckResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Handler 每日自檢排程 + 手動觸發端點。
type Handler struct {
	db *pgxpool.Pool

	mu          sync.Mutex
	lastRunDate string // 台灣日期 YYYY-MM-DD：最近一次「已認領要執行」自檢的日期（in-memory 標記，見檔頭）

	// lastReportDate：每日營運報告（dailyreport.go）獨立的當日冪等標記，刻意與上面的 lastRunDate
	// 分開——兩個排程共用同一個 Handler／同一小時執行窗口，但各自認領各自的「今天跑過了嗎」，
	// 不能共用同一個欄位，否則其中一個先跑就會讓另一個誤判「今天已跑過」而被跳過。
	lastReportDate string
}

// NewHandler 建構子。
func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db}
}

// taiwanNow 目前的台灣時間（UTC+8 固定 offset 手算，禁用 time.LoadLocation("Asia/Taipei")——
// distroless 執行環境無 tzdata，理由同 internal/notify/alert.go taiwanTimestamp()、
// internal/payment/vip_renewal.go formatTaipei() 等既有慣例）。
func taiwanNow() time.Time {
	return time.Now().UTC().Add(8 * time.Hour)
}

// inSelfCheckWindow 是否落在今天的執行窗口（台灣時間 08:00-08:59）內。純函式，方便單元測試。
func inSelfCheckWindow(t time.Time) bool {
	return t.Hour() == selfCheckWindowHour
}

// RunSelfCheckLoop 背景每日自檢排程。啟動時先檢查一次（若服務剛好在窗口內重啟，補跑當天），
// 之後每小時檢查一次；ctx 取消即結束。比照 payment.BindHandler.RunRenewalLoop 的迴圈骨架。
func (h *Handler) RunSelfCheckLoop(ctx context.Context) {
	h.maybeRunDaily(ctx)
	t := time.NewTicker(selfCheckTickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.maybeRunDaily(ctx)
		}
	}
}

// maybeRunDaily 窗口 + 當日冪等閘門判斷；命中才真的執行巡檢。雙層防重複：
//  1. in-memory lastRunDate：同一實例同一天只認領一次，避免同一小時內因故被呼叫多次而重跑。
//  2. pg_try_advisory_lock：多實例（Railway 水平擴展）情境下，同一時刻只有一個實例真的執行本輪
//     查詢；沒搶到鎖的實例直接跳過（不影響正確性——本檢查全程唯讀，就算真的重複執行兩次，頂多是
//     Telegram 告警因 notify.Alert 的 30 分節流被吃掉一次，不會有資料被誤改的風險，這裡用鎖純粹是
//     避免重複查詢的效能考量，比照 vip_renewal.go 的取捨）。
func (h *Handler) maybeRunDaily(ctx context.Context) {
	now := taiwanNow()
	if !inSelfCheckWindow(now) {
		return
	}
	today := now.Format("2006-01-02")

	h.mu.Lock()
	alreadyRan := h.lastRunDate == today
	h.mu.Unlock()
	if alreadyRan {
		return
	}

	conn, err := h.db.Acquire(ctx)
	if err != nil {
		log.Error().Err(err).Msg("ops selfcheck: acquire dedicated connection for advisory lock failed")
		return
	}
	defer conn.Release()

	var gotLock bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, selfCheckAdvisoryLockName).Scan(&gotLock); err != nil {
		log.Error().Err(err).Msg("ops selfcheck: try advisory lock failed")
		return
	}
	if !gotLock {
		log.Debug().Msg("ops selfcheck: another instance is already running/ran today's check, skip")
		return
	}
	defer func() {
		var unlocked bool
		if err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, selfCheckAdvisoryLockName).Scan(&unlocked); err != nil {
			log.Warn().Err(err).Msg("ops selfcheck: advisory unlock failed (will auto-release once this connection closes)")
		}
	}()

	// 搶到鎖即視為「今天由本實例認領」，不論後面實際執行結果如何都不重試（下一次落在窗口內的
	// tick 已是明天）——比照 vip_renewal.go 冪等閘門「先佔位再執行」的順序。
	h.mu.Lock()
	h.lastRunDate = today
	h.mu.Unlock()

	results := h.runChecks(ctx)
	h.reportSelfCheck(results)
}

// runChecks 依序執行全部 8 項檢查。單項查詢出錯（例如短暫 DB 逾時）視為該項「異常」落地，而非整批
// 中止——避免一項查詢失敗就讓其餘 7 項的健檢結果也一併遺失。
func (h *Handler) runChecks(ctx context.Context) []CheckResult {
	checks := []struct {
		name string
		fn   func(context.Context) (bool, string, error)
	}{
		{"pending_vip_orders_stuck", h.checkStuckPendingVipOrders},
		{"paytx_long_pending", h.checkPaytxLongPending},
		{"renewal_attempts_stale_processing", h.checkStaleRenewalAttempts},
		{"paid_order_without_subscription", h.checkPaidOrderNoSubscription},
		{"active_subscription_without_card", h.checkActiveSubscriptionNoCard},
		{"webhook_failure_rate", h.checkWebhookFailureRate},
		{"cancel_refund_mismatch", h.checkCancelRefundMismatch},
		{activityHeartbeatCheck, h.checkActivityHeartbeat},
	}
	out := make([]CheckResult, 0, len(checks))
	for _, c := range checks {
		ok, detail, err := c.fn(ctx)
		if err != nil {
			log.Error().Err(err).Str("check", c.name).Msg("ops selfcheck: check query failed")
			out = append(out, CheckResult{Name: c.name, OK: false, Detail: "查詢失敗：" + err.Error()})
			continue
		}
		out = append(out, CheckResult{Name: c.name, OK: ok, Detail: detail})
	}
	return out
}

// selfCheckSummary summarizeSelfCheck 的彙整結果（純邏輯、不碰 DB/notify，方便單元測試；
// reportSelfCheck 依此結果決定要 log.Info 還是送 Telegram）。
type selfCheckSummary struct {
	AllOK           bool
	FailedCount     int    // 不含 activityHeartbeatCheck 的異常數（彙整成單則 "selfcheck" kind）
	AggregateDetail string // 彙整訊息內文（逐項列出，換行分隔）
	HeartbeatFailed bool
	HeartbeatDetail string
}

// summarizeSelfCheck 把 8 項檢查結果彙整成「1-7 合併一則、8 獨立一則」的兩軌告警素材（純函式）。
func summarizeSelfCheck(results []CheckResult) selfCheckSummary {
	var sum selfCheckSummary
	sum.AllOK = true
	var lines []string
	for _, r := range results {
		if r.Name == activityHeartbeatCheck {
			if !r.OK {
				sum.AllOK = false
				sum.HeartbeatFailed = true
				sum.HeartbeatDetail = r.Detail
			}
			continue
		}
		if !r.OK {
			sum.AllOK = false
			sum.FailedCount++
			label := checkLabel[r.Name]
			if label == "" {
				label = r.Name
			}
			lines = append(lines, fmt.Sprintf("• %s：%s", label, r.Detail))
		}
	}
	sum.AggregateDetail = strings.Join(lines, "\n")
	return sum
}

// reportSelfCheck 依彙整結果決定告警行為：全部正常只記 log（避免每日噪音）；1-7 有異常彙整成單一則
// kind="selfcheck" 的 Telegram；第 8 項（活動心跳）異常另外用獨立 kind 送出、訊息註明可能為正常低谷。
func (h *Handler) reportSelfCheck(results []CheckResult) {
	sum := summarizeSelfCheck(results)
	if sum.AllOK {
		log.Info().Msg("selfcheck ok (8 checks)")
		return
	}
	if sum.FailedCount > 0 {
		notify.Alert("selfcheck", fmt.Sprintf("每日自檢發現 %d 項異常", sum.FailedCount), sum.AggregateDetail)
	}
	if sum.HeartbeatFailed {
		notify.Alert("selfcheck_activity_heartbeat", "近24h平台活動上傳數為0",
			sum.HeartbeatDetail+"（可能為正常低谷，請留意）")
	}
}

// SelfCheckNow POST /admin/ops/selfcheck：手動立即執行全部 8 項檢查並回傳完整 JSON 報告，
// 不受「今日是否已跑」的窗口/冪等限制、也不佔用每日 in-memory 標記（不影響排程本身當天是否還會
// 自動執行一次）；純粹回報現況供人工查驗，不送 Telegram（送不送告警的決策留給排程本身）。
func (h *Handler) SelfCheckNow(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	results := h.runChecks(ctx)
	okCount := 0
	for _, c := range results {
		if c.OK {
			okCount++
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"generated_at": time.Now().UTC(),
		"ok_count":     okCount,
		"total":        len(results),
		"checks":       results,
	})
}

// --- helpers ---

// formatTaipei 純函式：把 UTC 時刻格式化成台灣日期時間字串（UTC+8 固定 offset，理由同 taiwanNow）。
func formatTaipei(t time.Time) string {
	return t.UTC().Add(8 * time.Hour).Format("2006/01/02 15:04")
}

// countAndSamples 共用查詢骨架：先查總數，0 筆時直接短路（不再查樣本）；否則撈樣本 SQL 已格式化好的
// 文字列（每項檢查各自決定要秀哪些欄位），供 detail 訊息組字串用。兩段 SQL 皆不帶 bind 參數（門檻皆為
// SQL 常數 INTERVAL），僅供本檔內部使用。
func (h *Handler) countAndSamples(ctx context.Context, countSQL, sampleSQL string) (total int, samples []string, err error) {
	if err = h.db.QueryRow(ctx, countSQL).Scan(&total); err != nil {
		return 0, nil, err
	}
	if total == 0 {
		return 0, nil, nil
	}
	rows, err := h.db.Query(ctx, sampleSQL)
	if err != nil {
		return total, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			return total, nil, err
		}
		samples = append(samples, s)
	}
	if err = rows.Err(); err != nil {
		return total, nil, err
	}
	return total, samples, nil
}

// --- 檢查項目 1：卡住的 pending VIP 訂單 ---
//
// VIP 訂閱訂單＝race_id IS NULL（migration 132：VIP 訂單為獨立訂單，無賽事）。status='pending' 超過
// 24h 仍未變化，代表這筆訂單的金流 callback 從未到達、或使用者中途放棄付款——正常結帳流程通常幾分鐘
// 內就會收斂成 paid/failed，24h 是遠超正常流程的寬鬆門檻（連同「排除今天正常流程中的」的用意就是這道
// 24h 門檻本身：不到 24h 的一律當作還在正常流程中，不觸發告警）。
func (h *Handler) checkStuckPendingVipOrders(ctx context.Context) (bool, string, error) {
	total, samples, err := h.countAndSamples(ctx,
		`SELECT COUNT(*) FROM orders
		 WHERE race_id IS NULL AND status='pending' AND created_at < NOW() - INTERVAL '24 hours'`,
		`SELECT id::text FROM orders
		 WHERE race_id IS NULL AND status='pending' AND created_at < NOW() - INTERVAL '24 hours'
		 ORDER BY created_at ASC LIMIT 5`)
	if err != nil {
		return false, "", err
	}
	if total == 0 {
		return true, "", nil
	}
	return false, fmt.Sprintf("共 %d 筆，前 %d 筆 order id：%s", total, len(samples), strings.Join(samples, ", ")), nil
}

// --- 檢查項目 2：payment_transactions 長期 pending ---
//
// 涵蓋所有付款方式（不限 VIP），48h 未收斂為更寬的門檻（比照第 1 項但拉長，因為一般賽事報名結帳
// 也走這張表，不宜用跟 VIP 續約一樣緊的 24h）。
func (h *Handler) checkPaytxLongPending(ctx context.Context) (bool, string, error) {
	var total int
	if err := h.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM payment_transactions
		WHERE status='pending' AND created_at < NOW() - INTERVAL '48 hours'`).Scan(&total); err != nil {
		return false, "", err
	}
	if total == 0 {
		return true, "", nil
	}
	var oldestID string
	var oldestAt time.Time
	if err := h.db.QueryRow(ctx, `
		SELECT id::text, created_at FROM payment_transactions
		WHERE status='pending' AND created_at < NOW() - INTERVAL '48 hours'
		ORDER BY created_at ASC LIMIT 1`).Scan(&oldestID, &oldestAt); err != nil {
		return false, "", err
	}
	return false, fmt.Sprintf("共 %d 筆，最舊一筆 id=%s created_at=%s", total, oldestID, formatTaipei(oldestAt)), nil
}

// --- 檢查項目 3：VIP 續約 attempts 跨日殘留 processing ---
//
// 正常情況下這種殘留列會被 payment.BindHandler.cleanupStaleRenewalAttempts 在每小時的續約批次一開始
// 就清成 failed（見 internal/payment/vip_renewal.go）；此處是獨立於續約排程之外的第二道確認——若這裡
// 抓到非 0，代表續約排程本身可能沒有正常運作（例如 RunRenewalLoop 掛掉、或程序層級的問題），不能只
// 依賴續約排程自己的 cleanup 邏輯。created_at::date < CURRENT_DATE 的寫法比照
// cleanupStaleRenewalAttempts 原文，維持同一份「跨日」定義。
func (h *Handler) checkStaleRenewalAttempts(ctx context.Context) (bool, string, error) {
	var total int
	if err := h.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM vip_renewal_attempts
		WHERE status='processing' AND created_at::date < CURRENT_DATE`).Scan(&total); err != nil {
		return false, "", err
	}
	if total == 0 {
		return true, "", nil
	}
	return false, fmt.Sprintf("共 %d 筆跨日殘留在 processing（VIP 續約排程可能未正常運作）", total), nil
}

// --- 檢查項目 4：已付 VIP 訂單但該 user 無任何訂閱紀錄 ---
//
// 首購結算（settleVipRenewal／首次 Subscribe 結算）必寫入 vip_subscriptions 一列；只查近 7 天內
// paid（以 paid_at 為準）的訂單，避免撈到系統上線初期或資料遷移期間的歷史資料造成誤報（見任務規格）。
func (h *Handler) checkPaidOrderNoSubscription(ctx context.Context) (bool, string, error) {
	total, samples, err := h.countAndSamples(ctx,
		`SELECT COUNT(*) FROM orders o
		 WHERE o.race_id IS NULL AND o.status='paid' AND o.paid_at IS NOT NULL
		   AND o.paid_at > NOW() - INTERVAL '7 days'
		   AND NOT EXISTS (SELECT 1 FROM vip_subscriptions s WHERE s.user_id = o.user_id)`,
		`SELECT o.id::text || ' (user=' || o.user_id::text || ')' FROM orders o
		 WHERE o.race_id IS NULL AND o.status='paid' AND o.paid_at IS NOT NULL
		   AND o.paid_at > NOW() - INTERVAL '7 days'
		   AND NOT EXISTS (SELECT 1 FROM vip_subscriptions s WHERE s.user_id = o.user_id)
		 ORDER BY o.paid_at ASC LIMIT 5`)
	if err != nil {
		return false, "", err
	}
	if total == 0 {
		return true, "", nil
	}
	return false, fmt.Sprintf("近7天已付VIP訂單但user無任何vip_subscriptions紀錄，共 %d 筆，前 %d 筆：%s",
		total, len(samples), strings.Join(samples, "; ")), nil
}

// --- 檢查項目 5：active 訂閱但該 user 無 active 綁卡 ---
//
// 綁卡與訂閱在資料模型上各自獨立（payment_card_bindings 可被使用者主動刪除，或被綠界停用），一旦
// 出現「active 訂閱卻查無 active 綁卡」，代表下一輪續約排程 loadRenewalCandidates 的
// INNER JOIN payment_card_bindings 會直接把這筆訂閱排除在候選之外——訂閱表面上仍是 active，實際上
// 續約永遠不會被嘗試，直到自然到期後才會被使用者發現「怎麼突然不是 VIP 了」，是本項要提前抓出的
// 前兆情境。
func (h *Handler) checkActiveSubscriptionNoCard(ctx context.Context) (bool, string, error) {
	total, samples, err := h.countAndSamples(ctx,
		`SELECT COUNT(*) FROM vip_subscriptions s
		 WHERE s.status='active'
		   AND NOT EXISTS (
		     SELECT 1 FROM payment_card_bindings b
		     WHERE b.user_id = s.user_id AND b.provider='ecpay' AND b.status='active')`,
		`SELECT s.id::text || ' (user=' || s.user_id::text || ')' FROM vip_subscriptions s
		 WHERE s.status='active'
		   AND NOT EXISTS (
		     SELECT 1 FROM payment_card_bindings b
		     WHERE b.user_id = s.user_id AND b.provider='ecpay' AND b.status='active')
		 ORDER BY s.current_period_end ASC NULLS LAST LIMIT 5`)
	if err != nil {
		return false, "", err
	}
	if total == 0 {
		return true, "", nil
	}
	return false, fmt.Sprintf("共 %d 筆（續扣必失敗的前兆），前 %d 筆：%s", total, len(samples), strings.Join(samples, "; ")), nil
}

// --- 檢查項目 6：近 24h 付款 webhook 失敗率 ---
//
// 用 payment_transactions.status 分佈近似 webhook/幕後請款的成功率；failed 絕對數 ≥5 或
// failed/(paid+failed) 比例 >50% 任一命中即告警（分母為 0，即近 24h 完全沒有 paid 也沒有 failed，
// 視為無法計算比例，只看絕對數門檻，避免除以零）。
func (h *Handler) checkWebhookFailureRate(ctx context.Context) (bool, string, error) {
	var failedN, paidN int
	if err := h.db.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE status='failed'), COUNT(*) FILTER (WHERE status='paid')
		FROM payment_transactions WHERE created_at > NOW() - INTERVAL '24 hours'`).
		Scan(&failedN, &paidN); err != nil {
		return false, "", err
	}
	total := failedN + paidN
	var rate float64
	if total > 0 {
		rate = float64(failedN) / float64(total)
	}
	if failedN >= 5 || (total > 0 && rate > 0.5) {
		return false, fmt.Sprintf("近24h payment_transactions：failed=%d paid=%d（失敗率 %.1f%%）", failedN, paidN, rate*100), nil
	}
	return true, "", nil
}

// --- 檢查項目 7：取消退費矛盾 ---
//
// 終態驗證（依 internal/race/repository.go SettleCancellation + cancelreq.go approveCancelRequest
// 讀碼確認）：取消審核核准後，orders.status 一律從 paid/pending CAS 轉成 targetOrderStatus，其值只會
// 是 "cancelled"（含退 0 元/無需退款/退款尚未成功的情況——見 cancelreq.go targetOrderStatus 預設值
// 註解）或 "refunded"（僅退款「真正成功」時）。換句話說，一筆 registrations.status='cancelled' 的
// 報名，其對應訂單絕不應該仍停在 "paid"——若還停在 paid，代表 SettleCancellation 沒有被正確呼叫到
// （例如取消流程中途失敗、或有繞過 approveCancelRequest 的例外路徑直接改了 registrations 狀態），
// 對帳上會呈現「已取消卻仍算作已收款」的錯誤金額。24h 緩衝排除「核准流程正在進行中尚未寫完」的瞬時
// 不一致（SettleCancellation 走單一 DB 交易，理論上不會有這種瞬時窗口，24h 純粹是防禦性寬限）。
func (h *Handler) checkCancelRefundMismatch(ctx context.Context) (bool, string, error) {
	total, samples, err := h.countAndSamples(ctx,
		`SELECT COUNT(*) FROM registrations r
		 JOIN orders o ON o.registration_id = r.id
		 WHERE r.status='cancelled' AND o.status='paid' AND o.created_at < NOW() - INTERVAL '24 hours'`,
		`SELECT r.id::text || ' (order=' || o.id::text || ')' FROM registrations r
		 JOIN orders o ON o.registration_id = r.id
		 WHERE r.status='cancelled' AND o.status='paid' AND o.created_at < NOW() - INTERVAL '24 hours'
		 ORDER BY o.created_at ASC LIMIT 5`)
	if err != nil {
		return false, "", err
	}
	if total == 0 {
		return true, "", nil
	}
	return false, fmt.Sprintf("已取消報名但訂單仍為paid（應為cancelled/refunded），共 %d 筆，前 %d 筆：%s",
		total, len(samples), strings.Join(samples, "; ")), nil
}

// --- 檢查項目 8：活動流異常（營運心跳） ---
//
// 近 24h 完全沒有任何 activities 新增，可能代表 GPS 上傳管線（Redis Streams → Worker）整條斷了，
// 也可能只是自然的使用低谷（例如凌晨離峰時段剛好卡在窗口內、或極端天氣沒人跑步）——不像其他 7 項
// 那樣有明確的「一定是錯」判準，因此獨立 kind 告警並在訊息中誠實註明兩種可能性，交給人工判斷。
func (h *Handler) checkActivityHeartbeat(ctx context.Context) (bool, string, error) {
	var total int
	if err := h.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM activities WHERE created_at > NOW() - INTERVAL '24 hours'`).Scan(&total); err != nil {
		return false, "", err
	}
	if total > 0 {
		return true, "", nil
	}
	return false, "近24h activities 新增數為 0", nil
}
