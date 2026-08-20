// VIP 訂閱制 Phase D：每日續約排程。到期前 1 天起，用玩家綁定的卡（payment_card_bindings）幕後
// 呼叫 BindClient.CreatePayment 扣一期，成功則 vip.ExtendPeriod 延長 VIP；失敗則 3 天寬限、最多重試
// 3 次（含首次），逾期或用滿次數即把訂閱標記 failed（VIP 到期後自然降級，不需額外動作）。
//
// 動真錢，最高原則是「絕不重複扣款」。雙層防線：
//  1. migrations/137_vip_renewal_attempts.sql 的 uq_vip_renewal_daily 唯一索引——同一訂閱同一天最多
//     一次嘗試，是真正擋住重複扣款的第一道、也是最終防線：INSERT 這列的動作發生在呼叫 CreatePayment
//     之前，就算有兩個 API 實例同時跑今天的批次、同時選中同一筆候選，只有一個能搶到這列 INSERT，
//     另一個撞 23505 直接跳過，實際只會有一次 CreatePayment 呼叫。
//  2. runRenewalBatch 額外用 pg_try_advisory_lock 讓同一時刻只有一個實例真的去跑候選查詢＋處理迴圈
//     （純粹是效率考量、減少多實例情境下的重複查詢/重複打 ECPay 429；即使沒搶到鎖、兩個實例都跑，
//     靠第 1 點也不會真的重複扣款）。本檔採用「pg_try_advisory_lock + 專屬連線持有到批次結束才手動
//     unlock+release」的寫法，而不是既有 event_race_schedule.go/event_race.go 兩個背景排程慣用的
//     「單筆 UPDATE...WHERE...IS NULL RETURNING id」CAS 寫法——那套寫法對於「整個批次要不要跑」這種
//     跨多筆、跨多次外部 HTTP 呼叫的粗粒度互斥並不合適（沒有一個天然的『單筆』可以拿來 CAS）；用
//     pg_advisory_xact_lock（單一交易內自動釋放）也不合適，因為本批次的關鍵段跨越多個獨立交易與
//     慢速的外部 HTTP 呼叫，把這些包在同一個 DB 交易裡持有鎖等於長時間佔用一條連線+交易，不安全。
//     pgxpool 對 session-level advisory lock 的已知陷阱是：若不透過專屬取得的連線、且忘記 unlock 就
//     Release 回池，鎖會跟著那條實體連線一直留著、被其他無關查詢複用。這裡用 db.Acquire 拿一條專屬
//     連線、defer 內明確呼叫 pg_advisory_unlock 之後才 Release，避免這個陷阱。
package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/vip"
)

const (
	// renewalTickInterval 每次檢查間隔。與其他背景排程（RunExpiryLoop 每分鐘、RunScheduleLoop 每
	// 30 秒）不同，這裡刻意不做「近期有人活動才查 DB」的閒置省睡眠優化——續約排程必須每天可靠執行，
	// 不能因為系統當下沒人在跑步就被跳過；候選查詢本身有索引、結果集小（僅「即將到期+active+有綁卡」
	// 的訂閱），每小時查一次對 Neon 負擔可忽略。同一訂閱同一天只會真的呼叫一次 CreatePayment（見檔頭
	// 冪等設計），故縮短間隔只會提升「候選一進入到期前 1 天視窗就儘快被處理」的時效性，不會重複扣款。
	renewalTickInterval = time.Hour

	// renewalMaxAttempts 3 天寬限、最多重試 3 次（含首次）。ECPay 規則是同卡連續失敗 4 次會被停卡，
	// 3 次留有安全邊界，絕不能超過。
	renewalMaxAttempts = 3

	// renewalAdvisoryLockName pg_try_advisory_lock 用的鎖名（經 hashtext 轉成 lock id）。字串常數本身
	// 即具唯一性，不需要另外維護一份不會撞號的整數表。
	renewalAdvisoryLockName = "vip_renewal_daily_batch"

	// renewalCandidateLimit 單次批次最多處理的候選數，避免極端情況下單一 tick 處理過久。
	// 一般情境候選數遠小於此值（僅到期前 1 天內的 active 訂閱）。
	renewalCandidateLimit = 500
)

// RunRenewalLoop 背景每日續約排程（VIP 訂閱 Phase D）。比照既有 event.Handler.RunExpiryLoop／
// RunScheduleLoop 的迴圈骨架：啟動時先跑一次（補停機期間應處理的候選），之後每 renewalTickInterval
// 跑一次；ctx 取消即結束。
func (h *BindHandler) RunRenewalLoop(ctx context.Context) {
	h.runRenewalBatch(ctx)
	t := time.NewTicker(renewalTickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.runRenewalBatch(ctx)
		}
	}
}

// runRenewalBatch 取得批次互斥鎖（見檔頭註解）→ 撈候選 → 逐筆處理（單筆失敗以 recover+log 隔離，
// 不影響其餘候選）。
func (h *BindHandler) runRenewalBatch(ctx context.Context) {
	conn, err := h.db.Acquire(ctx)
	if err != nil {
		log.Error().Err(err).Msg("vip renewal: acquire dedicated connection for advisory lock failed")
		return
	}
	defer conn.Release()

	var gotLock bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, renewalAdvisoryLockName).Scan(&gotLock); err != nil {
		log.Error().Err(err).Msg("vip renewal: try advisory lock failed")
		return
	}
	if !gotLock {
		log.Debug().Msg("vip renewal: another instance is already running today's batch, skip")
		return
	}
	defer func() {
		var unlocked bool
		if err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, renewalAdvisoryLockName).Scan(&unlocked); err != nil {
			log.Warn().Err(err).Msg("vip renewal: advisory unlock failed (will auto-release once this connection closes)")
		}
	}()

	candidates, err := h.loadRenewalCandidates(ctx)
	if err != nil {
		log.Error().Err(err).Msg("vip renewal: load candidates failed")
		return
	}
	if len(candidates) == 0 {
		return
	}
	log.Info().Int("count", len(candidates)).Msg("vip renewal: processing daily batch")
	for _, c := range candidates {
		h.processRenewalCandidateSafe(ctx, c)
	}
}

// renewalCandidate 一筆待續約候選：到期前 1 天內、訂閱仍 active、且有一張 active 綁卡。
type renewalCandidate struct {
	SubscriptionID   string
	UserID           string
	Plan             string // monthly | annual
	AmountCents      int    // 續約當下原訂閱的金額（不重算促銷價，見檔頭）
	CurrentPeriodEnd time.Time
	BindCardID       string
	MerchantMemberID string
	Email            string
	Name             string
}

// loadRenewalCandidates 撈候選：active 訂閱 × 到期日在 1 天內（含已過期，補寬限期重試用）× 有 active
// 綁卡（INNER JOIN payment_card_bindings：沒有可用綁卡的訂閱本階段不處理，直接不出現在候選中，
// 不產生任何 attempt 紀錄——比照使用者手冊 Phase D 範圍界定）。
func (h *BindHandler) loadRenewalCandidates(ctx context.Context) ([]renewalCandidate, error) {
	rows, err := h.db.Query(ctx, `
		SELECT s.id::text, s.user_id::text, s.plan, s.amount_cents, s.current_period_end,
		       b.bind_card_id, b.merchant_member_id, u.email, COALESCE(u.name,'')
		FROM vip_subscriptions s
		JOIN users u ON u.id = s.user_id
		JOIN payment_card_bindings b ON b.user_id = s.user_id AND b.provider='ecpay' AND b.status='active'
		WHERE s.status = 'active'
		  AND u.vip_expires_at <= NOW() + INTERVAL '1 day'
		ORDER BY s.current_period_end
		LIMIT $1`, renewalCandidateLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []renewalCandidate
	for rows.Next() {
		var c renewalCandidate
		if err := rows.Scan(&c.SubscriptionID, &c.UserID, &c.Plan, &c.AmountCents, &c.CurrentPeriodEnd,
			&c.BindCardID, &c.MerchantMemberID, &c.Email, &c.Name); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// processRenewalCandidateSafe 包 panic recover + 逾時保護，確保單筆候選出狀況不會拖垮整批（比照
// worker 端各處理迴圈的單筆隔離慣例）。
func (h *BindHandler) processRenewalCandidateSafe(ctx context.Context, c renewalCandidate) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Str("subscription_id", c.SubscriptionID).
				Msg("vip renewal: panic recovered while processing candidate")
		}
	}()
	// CreatePayment 本身逾時 35s（見 BindClient.HTTP.Timeout），這裡多留緩衝涵蓋前後的 DB 操作。
	cctx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	if err := h.processRenewalCandidate(cctx, c); err != nil {
		log.Error().Err(err).Str("subscription_id", c.SubscriptionID).Str("user_id", c.UserID).
			Msg("vip renewal: process candidate failed")
	}
}

// processRenewalCandidate 單一候選的完整流程：算 attempt_no → 當日冪等閘門 → （超過重試上限則直接
// 終結，不重複嘗試）→ 建訂單 → 幕後請款 → 依結果分流（成功/失敗/3DS/傳輸層未知）。
func (h *BindHandler) processRenewalCandidate(ctx context.Context, c renewalCandidate) error {
	// ① attempt_no：以本期到期日往前推 1 天（＝候選視窗開始的那一刻）當作本輪重試週期的起點，統計
	// 這之後已經產生過的失敗/需 3D 驗證嘗試數。current_period_end 只有續約成功時才會前進；失敗重試
	// 期間這個起點不動，attempt_no 逐日遞增；一旦續約成功，current_period_end 跳到下一期（一個月/
	// 一年後），起點隨之遠遠推移，計數自然歸零，不需要另外的欄位或重置邏輯。
	cycleStart := c.CurrentPeriodEnd.Add(-24 * time.Hour)
	var priorFails int
	if err := h.db.QueryRow(ctx, `
		SELECT count(*) FROM vip_renewal_attempts
		WHERE subscription_id=$1 AND status IN ('failed','threeds_required') AND created_at > $2`,
		c.SubscriptionID, cycleStart).Scan(&priorFails); err != nil {
		return fmt.Errorf("count prior attempts: %w", err)
	}
	rawAttemptNo := priorFails + 1
	attemptNoForRow := rawAttemptNo
	if attemptNoForRow > renewalMaxAttempts {
		attemptNoForRow = renewalMaxAttempts // 落地值維持在 1..3 的定義域內（見 migration 137 欄位註解）
	}

	// ② 當日冪等閘門：INSERT 一列 status='processing'。撞 uq_vip_renewal_daily（同訂閱同一天已有
	// 嘗試紀錄）代表今天已經處理過（不論最終成功/失敗/還在跑中），直接跳過——這是「絕不重複扣款」的
	// 核心保證，發生在呼叫 CreatePayment 之前。
	var attemptID string
	err := h.db.QueryRow(ctx, `
		INSERT INTO vip_renewal_attempts (subscription_id, user_id, attempt_no, status)
		VALUES ($1,$2,$3,'processing') RETURNING id::text`,
		c.SubscriptionID, c.UserID, attemptNoForRow).Scan(&attemptID)
	if err != nil {
		if isUniqueViolation(err) {
			return nil // 今天已經試過（daily gate），冪等 no-op
		}
		return fmt.Errorf("insert attempt gate: %w", err)
	}

	// ③ 超過重試上限：正常情況下第 3 次真正請款失敗時就會直接終結訂閱（見 handleRenewalChargeFailure），
	// 不會再被撈進候選；這裡是防禦性兜底（例如訂閱因故沒被標成 failed、隔天又被撈進候選名單）。
	if rawAttemptNo > renewalMaxAttempts {
		h.finalizeRenewalOverLimit(ctx, attemptID, c)
		return nil
	}

	// ④ 建續約訂單（金額固定用候選當下的原訂閱金額，不重算促銷價，見檔頭）。
	orderID, tradeNo, err := h.orders.CreateVipOrder(ctx, c.UserID, c.Plan, c.AmountCents, "VIP 續訂")
	if err != nil {
		if isUniqueViolation(err) {
			// 撞 uq_orders_pending_vip：使用者剛好有另一筆 pending VIP 訂單在途（例如正在手動走
			// Subscribe 改方案、或前一次續約的傳輸層錯誤訂單還沒被 webhook 結算掉）。不可硬塞第二筆
			// pending 訂單——當日這次嘗試標失敗，明天再試。
			h.markRenewalAttemptFailed(ctx, attemptID, "", "order create conflict: another pending VIP order exists for this user")
			log.Warn().Str("user_id", c.UserID).Str("subscription_id", c.SubscriptionID).
				Msg("vip renewal: pending VIP order already exists, skip today")
			return nil
		}
		h.markRenewalAttemptFailed(ctx, attemptID, "", "create order failed: "+err.Error())
		return fmt.Errorf("create vip order: %w", err)
	}
	if err := h.repo.CheckoutCreateTx(ctx, orderID, tradeNo, h.bindEnv, h.client.MerchantID, c.AmountCents); err != nil {
		h.markRenewalAttemptFailed(ctx, attemptID, "", "checkout create tx failed: "+err.Error())
		return fmt.Errorf("checkout create tx: %w", err)
	}
	if _, err := h.db.Exec(ctx, `UPDATE vip_renewal_attempts SET order_id=$2 WHERE id=$1`, attemptID, orderID); err != nil {
		// 非致命：order_id 主要供 webhook 反查續約分流用（見 handleBindWebhookData）；這裡萬一寫入
		// 失敗，晚到的 webhook 會判斷成「非續約訂單」而走 settleVipBindPayment 誤入帳——記警告供人工
		// 複查，但不中止本次請款流程（正常同步成功路徑完全不受影響）。
		log.Warn().Err(err).Str("attempt_id", attemptID).Str("order_id", orderID).
			Msg("vip renewal: link order_id to attempt failed (webhook renewal routing may misfire if this order later needs webhook rescue)")
	}

	req := buildRenewalPaymentReq(c, tradeNo, h.returnURL, time.Now())
	resp, err := h.client.CreatePayment(ctx, req)

	outcome, rtnCode, rtnMsg := classifyCreatePaymentErr(err)
	switch outcome {
	case renewalOutcomePaid:
		return h.handleRenewalChargeSuccess(ctx, orderID, attemptID, c, tradeNo, resp)
	case renewalOutcomeThreeDS:
		return h.handleRenewalChargeFailure(ctx, orderID, attemptID, c, rawAttemptNo, "threeds_required", rtnCode, rtnMsg)
	case renewalOutcomeBizFailed:
		return h.handleRenewalChargeFailure(ctx, orderID, attemptID, c, rawAttemptNo, "failed", rtnCode, rtnMsg)
	default: // renewalOutcomeUnknown：傳輸層/網路錯誤
		return h.handleRenewalChargeUnknown(ctx, attemptID, c, rtnMsg)
	}
}

// buildRenewalPaymentReq 組出幕後續約請款的 CreatePaymentReq。抽成純函式（不碰 DB/HTTP，only 依賴
// 傳入的 now 而非 time.Now()，可測）方便單元測試 ItemName 分支／MerchantMemberID 空字串 fallback／
// 金額換算等邏輯，不需要真的呼叫 CreatePayment。MerchantTradeDate 用固定 UTC+8 offset 換算台灣時間，
// 理由同 Subscribe（distroless 無 tzdata，不可用 time.LoadLocation）。
func buildRenewalPaymentReq(c renewalCandidate, tradeNo, returnURL string, now time.Time) CreatePaymentReq {
	itemName := "VIP 月費訂閱續訂"
	if c.Plan == "annual" {
		itemName = "VIP 年費訂閱續訂"
	}
	merchantMemberID := c.MerchantMemberID
	if merchantMemberID == "" {
		merchantMemberID = MerchantMemberID(c.UserID)
	}
	taipeiNow := now.UTC().Add(8 * time.Hour)
	return CreatePaymentReq{
		BindCardID: c.BindCardID,
		OrderInfo: PaymentOrderInfoReq{
			MerchantTradeDate: taipeiNow.Format("2006/01/02 15:04:05"),
			MerchantTradeNo:   tradeNo,
			TotalAmount:       c.AmountCents / 100,
			ReturnURL:         returnURL, // 重用綁卡流程同一個 ReturnURL，webhook 進來後靠 order_id 分流（見 handleBindWebhookData）
			TradeDesc:         "DOR VIP 自動續訂",
			ItemName:          itemName,
		},
		ConsumerInfo: ConsumerInfo{
			MerchantMemberID: merchantMemberID,
			Email:            c.Email,
			Name:             c.Name,
		},
	}
}

// renewalOutcome CreatePayment 呼叫結果的分類，供 processRenewalCandidate 分流；抽成獨立、不碰
// DB/HTTP 的純函式（classifyCreatePaymentErr）方便單元測試。
type renewalOutcome int

const (
	renewalOutcomePaid renewalOutcome = iota
	renewalOutcomeThreeDS
	renewalOutcomeBizFailed
	renewalOutcomeUnknown
)

// classifyCreatePaymentErr 把 BindClient.CreatePayment 的回傳錯誤分類成四種結果之一，回傳分類結果
// 與供落地 vip_renewal_attempts.rtn_code/rtn_msg 用的字串。err==nil（含仍非 nil 但業務上等同成功的
// 情況——目前沒有此例外）視為成功；ErrCreatePayment3DRequired 判斷需在一般 *BindBizError 判斷之前，
// 因為前者本身就是用同一個型別實作的哨兵值（見 ecpay_bind.go）。
func classifyCreatePaymentErr(err error) (outcome renewalOutcome, rtnCode, rtnMsg string) {
	if err == nil {
		return renewalOutcomePaid, "", ""
	}
	if errors.Is(err, ErrCreatePayment3DRequired) {
		return renewalOutcomeThreeDS, strconv.Itoa(ErrCreatePayment3DRequired.RtnCode), ErrCreatePayment3DRequired.RtnMsg
	}
	var bizErr *BindBizError
	if errors.As(err, &bizErr) {
		return renewalOutcomeBizFailed, strconv.Itoa(bizErr.RtnCode), bizErr.RtnMsg
	}
	// BindTransportError 或其他底層錯誤（逾時/DNS/連線失敗…）：狀態未知，見 handleRenewalChargeUnknown。
	return renewalOutcomeUnknown, "", err.Error()
}

// handleRenewalChargeSuccess CreatePayment 同步成功：結算 + 站內信通知。
func (h *BindHandler) handleRenewalChargeSuccess(ctx context.Context, orderID, attemptID string, c renewalCandidate, tradeNo string, resp *CreatePaymentResp) error {
	var ecpayTradeNo, gwsr string
	if resp != nil {
		if resp.CardInfo != nil {
			gwsr = strconv.Itoa(resp.CardInfo.Gwsr)
		}
		if resp.OrderInfo != nil {
			ecpayTradeNo = resp.OrderInfo.TradeNo
		}
	}
	raw, _ := json.Marshal(resp)

	userID, periodEnd, err := h.settleVipRenewal(ctx, orderID, attemptID, settleVipRenewalParams{
		Plan:            c.Plan,
		AmountCents:     c.AmountCents,
		MerchantTradeNo: tradeNo,
		EcpayTradeNo:    ecpayTradeNo,
		Gwsr:            gwsr,
		Raw:             raw,
	})
	if err != nil {
		return fmt.Errorf("settle vip renewal: %w", err)
	}
	if periodEnd.IsZero() || userID == "" {
		return nil // CAS 冪等 no-op（理論上不該發生在這條同步成功路徑，防禦性處理，不重複發信）
	}
	level, title, body := renewalSuccessMail(formatTaipei(periodEnd))
	h.sendRenewalMail(ctx, userID, level, title, body)
	return nil
}

// handleRenewalChargeFailure BindBizError／3DS 兩種「確定沒扣到款」的失敗：標記 attempt、作廢訂單
// （不留垃圾 pending——這筆請款已經確定不會有晚到的成功 webhook）、視 attemptNo 是否已達上限決定
// 終結訂閱或留待明天重試，並發對應文案的站內信。
func (h *BindHandler) handleRenewalChargeFailure(ctx context.Context, orderID, attemptID string, c renewalCandidate, attemptNo int, status, rtnCode, rtnMsg string) error {
	if _, err := h.db.Exec(ctx, `
		UPDATE vip_renewal_attempts SET status=$2, rtn_code=$3, rtn_msg=$4
		WHERE id=$1 AND status='processing'`, attemptID, status, rtnCode, rtnMsg); err != nil {
		log.Error().Err(err).Str("attempt_id", attemptID).Msg("vip renewal: mark attempt failure status failed")
	}
	if _, err := h.db.Exec(ctx, `UPDATE orders SET status='cancelled' WHERE id=$1 AND status='pending'`, orderID); err != nil {
		log.Error().Err(err).Str("order_id", orderID).Msg("vip renewal: cancel failed charge order failed")
	}

	final := attemptNo >= renewalMaxAttempts
	if final {
		h.finalizeRenewalSubscription(ctx, c.SubscriptionID)
	}

	level, title, body := classifyRenewalFailureMail(status, attemptNo, final)
	h.sendRenewalMail(ctx, c.UserID, level, title, body)
	return nil
}

// handleRenewalChargeUnknown 傳輸層/未知錯誤（逾時、斷線…）：狀態未知，錢可能已經扣了。絕不可自動
// 重試（下一輪 tick 仍會被同一天的 uq_vip_renewal_daily 擋下，明天才會再嘗試——這正是我們要的：當日
// 不再對這張卡動作）、也絕不可標訂閱 failed（那樣會在錢已經扣了的情況下把訂閱關掉）。訂單刻意保持
// pending：若 ECPay 端其實已受理，稍後 Notify/Result webhook 送達時會由 settleVipRenewal 補上結算
// （見 handleBindWebhookData 的續約分流判斷）。這裡只記 log 供人工對帳、並在 attempt 列留痕。
func (h *BindHandler) handleRenewalChargeUnknown(ctx context.Context, attemptID string, c renewalCandidate, errMsg string) error {
	msg := "transport/unknown error, charge status unknown, needs manual reconciliation: " + errMsg
	if _, err := h.db.Exec(ctx, `
		UPDATE vip_renewal_attempts SET status='failed', rtn_msg=$2
		WHERE id=$1 AND status='processing'`, attemptID, msg); err != nil {
		log.Error().Err(err).Str("attempt_id", attemptID).Msg("vip renewal: mark transport-error attempt failed")
	}
	log.Error().Str("user_id", c.UserID).Str("subscription_id", c.SubscriptionID).Str("reason", errMsg).
		Msg("vip renewal: CreatePayment transport/unknown error — charge status unknown, needs manual reconciliation (order left pending for webhook rescue)")
	return nil
}

// finalizeRenewalOverLimit attemptNo 超過上限的防禦性兜底分支（見 processRenewalCandidate ③）：
// 直接終結訂閱，不嘗試請款。
func (h *BindHandler) finalizeRenewalOverLimit(ctx context.Context, attemptID string, c renewalCandidate) {
	h.markRenewalAttemptFailed(ctx, attemptID, "", "exceeded max retry attempts")
	h.finalizeRenewalSubscription(ctx, c.SubscriptionID)
	level, title, body := renewalFinalFailMail()
	h.sendRenewalMail(ctx, c.UserID, level, title, body)
}

// finalizeRenewalSubscription 把訂閱標記 failed（CAS：僅在目前仍是 active 時才覆寫，避免覆蓋掉
// 使用者/管理員同時間手動取消所產生的 cancelled 狀態）。VIP 到期後自然降級，不需要另外的動作。
func (h *BindHandler) finalizeRenewalSubscription(ctx context.Context, subscriptionID string) {
	if _, err := h.db.Exec(ctx, `UPDATE vip_subscriptions SET status='failed', updated_at=NOW() WHERE id=$1 AND status='active'`, subscriptionID); err != nil {
		log.Error().Err(err).Str("subscription_id", subscriptionID).Msg("vip renewal: finalize failed subscription failed")
	}
}

// markRenewalAttemptFailed 標記 attempt 列失敗（僅在仍是 processing 時覆寫）；用於「還沒真的呼叫
// CreatePayment 就中止」的分支（建訂單失敗等）。
func (h *BindHandler) markRenewalAttemptFailed(ctx context.Context, attemptID, rtnCode, rtnMsg string) {
	if _, err := h.db.Exec(ctx, `
		UPDATE vip_renewal_attempts SET status='failed', rtn_code=$2, rtn_msg=$3
		WHERE id=$1 AND status='processing'`, attemptID, rtnCode, rtnMsg); err != nil {
		log.Error().Err(err).Str("attempt_id", attemptID).Msg("vip renewal: mark attempt failed error")
	}
}

// sendRenewalMail 站內信通知的共用出口：h.mail 為 nil（未注入/測試）時直接跳過，不影響金流本身
// （站內信是錦上添花，不是金流正確性的一部分）。
func (h *BindHandler) sendRenewalMail(ctx context.Context, userID, level, title, body string) {
	if h.mail == nil || userID == "" {
		return
	}
	if _, err := h.mail.InsertForUsers(ctx, []string{userID}, level, title, body, ""); err != nil {
		log.Warn().Err(err).Str("user_id", userID).Str("title", title).Msg("vip renewal: send mail failed")
	}
}

// --- 通知文案（純函式，方便單元測試） ---

// formatTaipei 純函式：把 UTC 時刻格式化成台灣日期（UTC+8 固定 offset 換算，理由同 Subscribe：
// distroless 無 tzdata，禁止 time.LoadLocation）。
func formatTaipei(t time.Time) string {
	return t.UTC().Add(8 * time.Hour).Format("2006/01/02")
}

// renewalSuccessMail 續約成功通知文案。
func renewalSuccessMail(periodEndStr string) (level, title, body string) {
	return "normal", "VIP 續訂成功", fmt.Sprintf("您的 DOR VIP 已成功自動續訂，效期已延長至 %s。", periodEndStr)
}

// renewalRetryMail 非最終失敗的重試通知文案（產品拍板原文字，見任務規格）。
func renewalRetryMail(attemptNo int) (level, title, body string) {
	return "important", "VIP 自動扣款失敗",
		fmt.Sprintf("扣款失敗（第 %d/%d 次），將於明日自動重試，請確認卡片額度", attemptNo, renewalMaxAttempts)
}

// renewalThreeDSMail 3DS 風險分流通知文案（產品拍板原文字，見任務規格）：幕後扣款被風控要求 3D，
// 無人值守做不到，需使用者手動到前景完成。
func renewalThreeDSMail() (level, title, body string) {
	return "important", "VIP 自動續訂需要驗證", "本次自動扣款需要持卡驗證，請於 App 內手動完成續費"
}

// renewalFinalFailMail 最終失敗（用滿重試次數或超過上限）通知文案（產品拍板原文字，見任務規格）。
func renewalFinalFailMail() (level, title, body string) {
	return "urgent", "VIP 自動續訂已停止", "多次扣款失敗，VIP 將於到期後暫停"
}

// classifyRenewalFailureMail 依「是否為最終失敗」與「是否為 3DS 分流」決定要用哪一種失敗文案，
// 優先序：final（訂閱即將終結，最急迫）> threeds_required（需要使用者動作）> 一般重試通知。
// 抽成純函式方便單元測試三種文案的挑選邏輯，不需要真的觸發 DB/HTTP 呼叫。
func classifyRenewalFailureMail(status string, attemptNo int, final bool) (level, title, body string) {
	switch {
	case final:
		return renewalFinalFailMail()
	case status == "threeds_required":
		return renewalThreeDSMail()
	default:
		return renewalRetryMail(attemptNo)
	}
}

// --- 結算（Phase D 核心：與 settleVipBindPayment 同一保證，但延長既有訂閱而非新建） ---

// settleVipRenewalParams settleVipRenewal 所需的全部上下文。UserID 刻意不在這裡——一律由 CAS 閘門的
// `RETURNING user_id` 取得（同 settleVipBindParams 的設計原則），不接受呼叫端傳入。
type settleVipRenewalParams struct {
	Plan            string // monthly | annual
	AmountCents     int
	MerchantTradeNo string // 我方交易編號（orders.payment_ref 統一存 "ECPay:"+此值，與既有慣例一致）
	EcpayTradeNo    string // 綠界 TradeNo
	Gwsr            string
	Raw             []byte // 完整回應/webhook 資料（稽核用）
}

// settleVipRenewal 續約扣款結算的單一入口——processRenewalCandidate（CreatePayment 同步成功）與
// handleBindWebhookData（Notify/Result webhook，涵蓋同步呼叫因逾時/斷線而「其實已扣款成功」但我方
// 誤判為未知結果的情形，見 handleRenewalChargeUnknown）都呼叫這裡；先到者結算，晚到者在 CAS 閘門
// 冪等 no-op——與 settleVipBindPayment 完全同一保證。差異只在於：續約是「延長既有 active 訂閱」而非
// 「建立新訂閱」，因此：
//   - 不動 payment_card_bindings（卡片本來就已綁定，續約不重新綁卡、也不該覆寫卡片顯示資訊——
//     CreatePayment 的回應本來就不一定回帶完整卡片資訊）。
//   - 不 INSERT vip_subscriptions 新列（否則會撞 uq_vip_subs_active——這正是 Phase D 最初設計必須
//     解決的問題：若直接重用 settleVipBindPayment 結算續約訂單，webhook 路徑會在使用者已有一筆
//     active 訂閱時嘗試再插入第二筆，違反唯一索引、整包 rollback，訂單卡在 pending。改走本函式後，
//     webhook 才能正確處理「同步呼叫失敗但 ECPay 端其實已扣款成功」這種續約專屬的補救情境）。
//
// 單一 DB 交易，全部同一 tx commit；任何一步失敗整包 rollback（訂單留 pending，webhook 重試會再來）。
// 回傳 (userID, periodEnd, err)：periodEnd 為零值代表 CAS no-op（已被結算過），呼叫端應以此判斷是否
// 需要發送成功通知（no-op 不重複發信）。
func (h *BindHandler) settleVipRenewal(ctx context.Context, orderID, attemptID string, p settleVipRenewalParams) (string, time.Time, error) {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("settle vip renewal: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1) CAS 閘門：0 列＝已被結算過（冪等出口）。邏輯與 settleVipBindPayment 完全一致，見該函式註解。
	var userID string
	err = tx.QueryRow(ctx, `
		UPDATE orders SET status='paid', paid_at=NOW(), payment_ref=$2
		WHERE id=$1 AND status='pending'
		RETURNING user_id`, orderID, "ECPay:"+p.MerchantTradeNo).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		var st string
		if qErr := h.db.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1`, orderID).Scan(&st); qErr == nil && st != "paid" {
			log.Error().Str("order_id", orderID).Str("order_status", st).Str("merchant_trade_no", p.MerchantTradeNo).
				Msg("settle vip renewal: charge received but order is not settleable — needs manual reconciliation/refund")
		}
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("settle vip renewal: cas order paid: %w", err)
	}

	// 2) 續約一期（同 settleVipBindPayment：從 max(現有到期, now) 起算，interval 加法自帶月底 clamp）。
	if err := vip.ExtendPeriod(ctx, tx, userID, p.Plan); err != nil {
		return "", time.Time{}, fmt.Errorf("settle vip renewal: extend vip period: %w", err)
	}
	var periodEnd time.Time
	if err := tx.QueryRow(ctx, `SELECT vip_expires_at FROM users WHERE id=$1`, userID).Scan(&periodEnd); err != nil {
		return "", time.Time{}, fmt.Errorf("settle vip renewal: read back vip_expires_at: %w", err)
	}

	// 3) vip_subscriptions：更新既有 active 列（不 INSERT 新列，見函式註解）。CAS 條件 status='active'
	// 是防禦性的（候選查詢已篩 status='active'，但扣款這段期間可能被使用者/管理員取消）；若 0 列受
	// 影響，代表訂閱在扣款當下已不是 active——錢已經確實收了（否則不會走到這裡），VIP 天數仍照上面
	// ExtendPeriod 給足，只記警告供人工複查，不因此讓整包交易失敗（不能已收錢卻不給服務）。
	tag, err := tx.Exec(ctx, `
		UPDATE vip_subscriptions
		SET exec_times = exec_times + 1, current_period_end = $2, updated_at = NOW()
		WHERE id = (SELECT subscription_id FROM vip_renewal_attempts WHERE id=$1) AND status='active'`,
		attemptID, periodEnd)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("settle vip renewal: update vip_subscriptions: %w", err)
	}
	if tag.RowsAffected() == 0 {
		log.Warn().Str("order_id", orderID).Str("attempt_id", attemptID).Str("user_id", userID).
			Msg("settle vip renewal: subscription no longer active at settle time (VIP days still granted; needs review)")
	}

	// 4) payment_transactions 標記已付（同 settleVipBindPayment 的欄位/CAS 條件）。
	if _, err := tx.Exec(ctx, `
		UPDATE payment_transactions
		SET status='paid', rtn_code='1', rtn_msg='VIP renewal paid', ecpay_trade_no=NULLIF($2,''),
		    payment_type='Credit', trade_amt_cents=$3, raw=$4, paid_at=NOW()
		WHERE order_id=$1 AND status IN ('pending','failed')`,
		orderID, p.EcpayTradeNo, p.AmountCents, p.Raw); err != nil {
		return "", time.Time{}, fmt.Errorf("settle vip renewal: mark payment_transactions paid: %w", err)
	}

	// 5) attempt 列標記 paid（CAS：僅 processing→paid，避免覆蓋掉已經被其他路徑 finalize 過的狀態）。
	if _, err := tx.Exec(ctx, `
		UPDATE vip_renewal_attempts SET status='paid', rtn_code='1', rtn_msg='paid'
		WHERE id=$1 AND status='processing'`, attemptID); err != nil {
		return "", time.Time{}, fmt.Errorf("settle vip renewal: mark attempt paid: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", time.Time{}, fmt.Errorf("settle vip renewal: commit: %w", err)
	}
	return userID, periodEnd, nil
}
