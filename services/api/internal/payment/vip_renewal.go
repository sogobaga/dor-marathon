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

	"github.com/dor/api/internal/notify"
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

	if err := h.cleanupStaleRenewalAttempts(ctx); err != nil {
		log.Error().Err(err).Msg("vip renewal: cleanup stale processing attempts failed")
	}

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

// cleanupStaleRenewalAttempts 清理「跨日仍卡在 processing」的殘留 attempt 列（對抗式審查修正 [7]）。
// 正常情況下 attempt 列會在同一次 processRenewalCandidate 呼叫內就被更新成終態（paid/failed/
// threeds_required）；唯一會留在 processing 的情境是處理過程中程序被中斷（部署重啟、panic 逃過
// recover、資料庫連線斷線等）。這種殘留列若不清理：
//  1. 稽核上看起來像「永遠處理中」，誤導人工排查。
//  2. processRenewalCandidate①的 priorFails 計數 SQL 只認 status IN ('failed','threeds_required')，
//     卡在 processing 的列不會被計入重試次數——等於讓那次中斷的嘗試被永遠忽略、變相多給使用者一次
//     重試機會，長期下來可能讓某些訂閱的實際重試次數超過 renewalMaxAttempts 的設計上限。
//
// 只清「今天以前」的（created_at::date < CURRENT_DATE）：今天仍在 processing 的列可能是本次批次正在
// 處理中的其他候選（尚未跑到終態的正常中間狀態），保守起見不動它，避免誤傷。
func (h *BindHandler) cleanupStaleRenewalAttempts(ctx context.Context) error {
	tag, err := h.db.Exec(ctx, `
		UPDATE vip_renewal_attempts
		SET status='failed', rtn_msg='stale processing (process interrupted)'
		WHERE status='processing' AND created_at::date < CURRENT_DATE`)
	if err != nil {
		return err
	}
	if n := tag.RowsAffected(); n > 0 {
		log.Warn().Int64("count", n).Msg("vip renewal: cleaned up stale processing attempts from previous day(s)")
	}
	return nil
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

	// ④ 建續約訂單（金額固定用候選當下的原訂閱金額，不重算促銷價，見檔頭）。撞 uq_orders_pending_vip
	// 時不再直接放棄當日重試——改用 QueryTrade 主動收斂那筆卡住的舊訂單（見下方「unknown 收斂」區塊
	// 的 convergeStuckPendingOrder），取代舊版「無限 pending 等 webhook」設計（對抗式審查
	// critical [0]／high [2][4][6]：舊版一旦某次續約請款因逾時等傳輸層錯誤留下無限 pending 的訂單，
	// 之後每天的候選都會撞這個唯一索引、永遠無法真正重試扣款，訂閱形同卡死）。
	orderID, tradeNo, err := h.orders.CreateVipOrder(ctx, c.UserID, c.Plan, c.AmountCents, "VIP 續訂")
	if err != nil {
		if !isUniqueViolation(err) {
			h.markRenewalAttemptFailed(ctx, attemptID, "", "create order failed: "+err.Error())
			return fmt.Errorf("create vip order: %w", err)
		}
		retry := h.convergeStuckPendingOrder(ctx, c, attemptID, rawAttemptNo)
		if !retry {
			return nil // 收斂流程已完整處理本輪（收斂成已付／非續約訂單／查詢失敗），不再往下建新單
		}
		// 舊單已確認未付款/查無並作廢，唯一索引已釋放，重試一次真正建立本次的新訂單。
		orderID, tradeNo, err = h.orders.CreateVipOrder(ctx, c.UserID, c.Plan, c.AmountCents, "VIP 續訂")
		if err != nil {
			// 理論上不該再撞（舊單已作廢）；此處是防禦性兜底，絕不無限重試——直接標失敗，留給明天。
			h.markRenewalAttemptFailed(ctx, attemptID, "", "create order failed after convergence retry: "+err.Error())
			return fmt.Errorf("create vip order after convergence retry: %w", err)
		}
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

	// 對抗式審查修正 high [4]：終結訂閱前先確認名下沒有其他尚未收斂的卡住續約訂單（見
	// finalizeRenewalSubscriptionGuarded），絕不能在錢可能已收的情況下對外發「訂閱已終止」。
	final := attemptNo >= renewalMaxAttempts
	outcome := renewalFinalizeDone
	if final {
		outcome = h.finalizeRenewalSubscriptionGuarded(ctx, c)
	}
	if outcome == renewalFinalizeRecoveredPaid {
		// 收斂發現另一筆卡住的舊訂單其實已付款，視同續約成功，不該再發本次失敗通知（矛盾）。理論上
		// 極少發生在這個呼叫點——這裡的 orderID 剛在上面被標成 cancelled，findPendingRenewalOrder
		// 通常查不到別的 pending 訂單；保留只是防禦性處理，不假設「不可能發生」。
		return nil
	}

	level, title, body := classifyRenewalFailureMail(status, attemptNo, final && outcome == renewalFinalizeDone)
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
		Msg("vip renewal: CreatePayment transport/unknown error — charge status unknown, needs manual reconciliation (order left pending; next unique-violation hit will attempt QueryTrade convergence, see convergeStuckPendingOrder)")
	// 對抗式審查修正 [1][6]：舊版這裡完全不通知使用者——使用者全程無感，直到某天訂閱被終結才發現。
	// 這個分支發生當下我方還不知道錢到底扣了沒，不能講「失敗」也不能講「成功」，只能誠實告知「確認中」。
	level, title, body := renewalUnknownMail()
	h.sendRenewalMail(ctx, c.UserID, level, title, body)
	return nil
}

// finalizeRenewalOverLimit attemptNo 超過上限的防禦性兜底分支（見 processRenewalCandidate ③）：
// 終結訂閱，不嘗試請款。這是「unknown 收斂」機制最主要的實戰觸發點——會走到這裡通常是因為前幾天有
// 某次 handleRenewalChargeUnknown 把訂單留成 pending（見該函式），之後每天的候選都在④撞
// uq_orders_pending_vip、被 markRenewalAttemptFailed 標成 failed，priorFails 逐日累積直到超過上限；
// 這條路徑終結訂閱前必須先確認那筆卡住的舊訂單是否其實已經扣款成功（見
// finalizeRenewalSubscriptionGuarded），不能不問青紅皂白直接終結。
func (h *BindHandler) finalizeRenewalOverLimit(ctx context.Context, attemptID string, c renewalCandidate) {
	h.markRenewalAttemptFailed(ctx, attemptID, "", "exceeded max retry attempts")
	if h.finalizeRenewalSubscriptionGuarded(ctx, c) != renewalFinalizeDone {
		return // 已收斂成已付（視同續約成功），或狀態仍未明（保留待下一輪）——都不發「已終止」通知
	}
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

// --- unknown 收斂（QueryTrade 主動查詢，對抗式審查修正 critical [0]／high [2][4][6]） ---
//
// 舊版設計：CreatePayment 逾時等傳輸層錯誤時（handleRenewalChargeUnknown），訂單刻意留 pending，
// 全指望晚到的 Notify/Result webhook 補結算。這個假設在綠界端「這次呼叫其實根本沒被受理」時會落空
// ——webhook 永遠不會來，訂單就永遠卡在 pending，撞死 uq_orders_pending_vip，之後每天的候選都會在
// processRenewalCandidate④建新單那步失敗，訂閱形同卡死，直到某天被 finalizeRenewalOverLimit 這個
// 防禦性兜底分支強制終結——而舊版那個分支完全沒有先確認「錢到底有沒有真的扣到」，等於可能在「其實
// 已經扣款成功」的情況下告知使用者「訂閱已終止」，是本次對抗式審查抓到的核心風險。
//
// 新機制：任何時候需要判斷一筆卡住的舊訂單真實狀態，一律用 QueryTrade 主動問綠界（取代被動等
// webhook），三種收斂結果：
//   - 已付（queryTradePaid）      → convergeRenewalPaid：結算舊單，視同本輪續約成功。
//   - 未付/查無（queryTradeUnpaid） → convergeRenewalVoidStale：作廢舊單，釋放唯一索引，可以正常
//     重試。RtnCode!=1（BindBizError，例如查無此訂單）與「RtnCode==1 但 TradeStatus=="0"」歸同一類
//     ——兩者對「該不該作廢重試」的決策完全相同（理由見 ecpay_bind.go QueryTrade 方法註解）。
//   - 查詢本身失敗（queryTradeFailed，傳輸層/網路錯誤） → 狀態依然未知，維持絕對保守：不作廢、
//     不重試、不終結訂閱，只記告警等待下一輪重新判斷。
//
// 兩個呼叫點：①processRenewalCandidate④撞 uq_orders_pending_vip 當下（convergeStuckPendingOrder）；
// ②finalizeRenewalSubscriptionGuarded——任何要終結訂閱的路徑統一改走這個前置檢查，是最後一道防線。

// queryTradeOutcome QueryTrade 收斂決策用的三分類（純邏輯，見 classifyQueryTradeResult）。
type queryTradeOutcome int

const (
	queryTradePaid queryTradeOutcome = iota
	queryTradeUnpaid
	queryTradeFailed
)

// classifyQueryTradeResult 把 BindClient.QueryTrade 的回傳結果分類成三種收斂決策之一，抽成不碰
// DB/HTTP 的純函式方便單元測試。err==nil 時看 resp.OrderInfo.TradeStatus；err 是 *BindBizError
// （業務層失敗，例如查無此訂單）視為未付/查無同一類（理由見 ecpay_bind.go QueryTrade 方法註解）；
// 其餘 error（*BindTransportError／網路錯誤）才是真正查詢本身失敗、狀態依然未知。
func classifyQueryTradeResult(resp *QueryTradeResp, err error) queryTradeOutcome {
	if err == nil {
		if resp != nil && resp.OrderInfo != nil && resp.OrderInfo.TradeStatus == "1" {
			return queryTradePaid
		}
		return queryTradeUnpaid // TradeStatus=="0"，或欄位缺漏/不完整：保守視為未付，絕不誤判成已付
	}
	var bizErr *BindBizError
	if errors.As(err, &bizErr) {
		return queryTradeUnpaid
	}
	return queryTradeFailed
}

// pendingRenewalOrder 一筆使用者目前卡住的 pending VIP 訂單（uq_orders_pending_vip 定義下「同使用者
// 至多一筆」），連同其目前 pending 的 payment_transactions.merchant_trade_no、以及是否有對應的
// vip_renewal_attempts 列（＝這筆訂單是續約排程建立的，而非使用者手動走 Subscribe 留下的）。
type pendingRenewalOrder struct {
	OrderID          string
	TradeNo          string
	AttemptID        string // IsRenewalAttempt=false 時為空字串
	IsRenewalAttempt bool
}

// findPendingRenewalOrder 找出該 user 目前卡住的 pending VIP 訂單，連同其 payment_transactions 的
// merchant_trade_no、以及是否有對應的 vip_renewal_attempts 列。查無回傳 (nil, nil)。
//
// 也供 ecpay_bind_handler.go Subscribe 的 1.5 段共用（判斷 23505 衝突是否為續約造成，決定回應文案，
// 對抗式審查修正 high [1]），不只是 vip_renewal.go 內部使用。
func (h *BindHandler) findPendingRenewalOrder(ctx context.Context, userID string) (*pendingRenewalOrder, error) {
	var p pendingRenewalOrder
	err := h.db.QueryRow(ctx, `
		SELECT o.id::text, pt.merchant_trade_no, COALESCE(a.id::text, '')
		FROM orders o
		JOIN payment_transactions pt ON pt.order_id = o.id AND pt.status = 'pending'
		LEFT JOIN vip_renewal_attempts a ON a.order_id = o.id
		WHERE o.user_id = $1 AND o.status = 'pending' AND o.race_id IS NULL`,
		userID).Scan(&p.OrderID, &p.TradeNo, &p.AttemptID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.IsRenewalAttempt = p.AttemptID != ""
	return &p, nil
}

// convergeRenewalPaid QueryTrade 確認舊訂單其實已付款後的收斂動作：呼叫 settleVipRenewal 結算
// （CAS 冪等，即使這筆早被其他路徑結算過也安全 no-op）、成功才發站內信。c 提供 Plan/AmountCents——
// 卡住的舊訂單必然是同一張訂閱先前某次續約嘗試留下的（uq_orders_pending_vip 是 per-user 唯一索引，
// CreateVipOrder 建立續約訂單時金額固定用候選當下的原訂閱金額，見檔頭②），與本輪候選 c 屬於同一張
// 訂閱、同一個方案/金額，可以直接沿用，不需要另外反查舊訂單當初的金額。
func (h *BindHandler) convergeRenewalPaid(ctx context.Context, p *pendingRenewalOrder, resp *QueryTradeResp, c renewalCandidate) {
	var ecpayTradeNo string
	if resp != nil && resp.OrderInfo != nil {
		ecpayTradeNo = resp.OrderInfo.TradeNo
	}
	raw, _ := json.Marshal(resp)
	userID, periodEnd, err := h.settleVipRenewal(ctx, p.OrderID, p.AttemptID, settleVipRenewalParams{
		Plan:            c.Plan,
		AmountCents:     c.AmountCents,
		MerchantTradeNo: p.TradeNo,
		EcpayTradeNo:    ecpayTradeNo,
		Raw:             raw,
	})
	if err != nil {
		log.Error().Err(err).Str("order_id", p.OrderID).Str("attempt_id", p.AttemptID).
			Msg("vip renewal: QueryTrade convergence found paid, but settleVipRenewal failed — needs manual reconciliation")
		return
	}
	if periodEnd.IsZero() || userID == "" {
		return // CAS no-op：這筆舊單已經被別的路徑結算過了（例如晚到的 webhook 搶先一步），不重複發信
	}
	level, title, body := renewalSuccessMail(formatTaipei(periodEnd))
	h.sendRenewalMail(ctx, userID, level, title, body)
}

// convergeRenewalVoidStale QueryTrade 確認舊訂單未付款/查無後的收斂動作：標記舊 attempt 列失敗、
// 作廢舊訂單＋交易，釋放 uq_orders_pending_vip，讓呼叫端可以正常建立新訂單重試。
func (h *BindHandler) convergeRenewalVoidStale(ctx context.Context, p *pendingRenewalOrder, reason string) {
	if p.IsRenewalAttempt {
		if _, err := h.db.Exec(ctx, `
			UPDATE vip_renewal_attempts SET status='failed', rtn_msg=$2
			WHERE id=$1 AND status='processing'`, p.AttemptID, reason); err != nil {
			log.Warn().Err(err).Str("attempt_id", p.AttemptID).Msg("vip renewal: mark stale attempt failed error")
		}
	}
	if err := h.repo.MarkTxFailed(ctx, p.TradeNo, "", reason, nil); err != nil {
		log.Warn().Err(err).Str("merchant_trade_no", p.TradeNo).Msg("vip renewal: mark stale tx failed error")
	}
	if _, err := h.db.Exec(ctx, `UPDATE orders SET status='cancelled' WHERE id=$1 AND status='pending'`, p.OrderID); err != nil {
		log.Warn().Err(err).Str("order_id", p.OrderID).Msg("vip renewal: cancel stale order error")
	}
}

// convergeStuckPendingOrder processRenewalCandidate④撞 uq_orders_pending_vip 時的收斂入口。
// 回傳 retry：true＝舊單已確認未付款/查無並作廢，呼叫端應該重新嘗試建立新訂單（本次真正重試）；
// false＝本輪已被完整處理（收斂成已付＝視同續約成功；或非續約訂單/查詢失敗＝保守中止待明天），
// 呼叫端不應該再嘗試建立新訂單。
func (h *BindHandler) convergeStuckPendingOrder(ctx context.Context, c renewalCandidate, attemptID string, rawAttemptNo int) (retry bool) {
	pending, err := h.findPendingRenewalOrder(ctx, c.UserID)
	if err != nil {
		log.Error().Err(err).Str("user_id", c.UserID).Msg("vip renewal: load stuck pending order for convergence failed")
		h.markRenewalAttemptFailed(ctx, attemptID, "", "order create conflict, and failed to load stuck order for convergence: "+err.Error())
		return false
	}
	if pending == nil {
		// 理論上不該發生（我們才剛撞到唯一索引）；極端時序下舊單可能剛好被別的路徑處理掉，安全起見
		// 這天不重試，明天再判斷。
		log.Warn().Str("user_id", c.UserID).Msg("vip renewal: unique violation but no stuck pending order found (race), skip today")
		h.markRenewalAttemptFailed(ctx, attemptID, "", "order create conflict but stuck order not found (transient), skip today")
		return false
	}
	if !pending.IsRenewalAttempt {
		// 非續約造成的 pending 訂單（例如使用者正在手動走 Subscribe 改方案）：維持既有保守行為，
		// 不動它、今天跳過。
		h.markRenewalAttemptFailed(ctx, attemptID, "", "order create conflict: another pending VIP order exists for this user (not a renewal order, left untouched)")
		log.Warn().Str("user_id", c.UserID).Str("subscription_id", c.SubscriptionID).Str("order_id", pending.OrderID).
			Msg("vip renewal: pending VIP order already exists and is not a renewal order, skip today")
		return false
	}

	resp, qErr := h.client.QueryTrade(ctx, pending.TradeNo)
	switch classifyQueryTradeResult(resp, qErr) {
	case queryTradePaid:
		h.convergeRenewalPaid(ctx, pending, resp, c)
		// 今天新插入的這筆 attempt 列（attemptID）也一併標成 paid：今天雖然沒有真的呼叫
		// CreatePayment，但本輪的結果就是「續約已確認成功」，不該讓這筆列停在 processing——那樣看起來
		// 像是還在跑、或被誤認成一次失敗的嘗試。CAS 條件維持 status='processing'（不需要用 D 修正
		// 那種放寬版 WHERE status<>'paid'：這筆是本次呼叫剛插入的新列，執行期間不會有其他路徑動它，
		// 此刻狀態保證仍是 processing；放寬版 CAS 是給 settleVipRenewal 內部處理「可能是好幾天前、
		// 狀態已經是 failed 的舊 attempt 列」這種情境用的，見該函式步驟 5 註解）。
		if _, err := h.db.Exec(ctx, `
			UPDATE vip_renewal_attempts SET status='paid', rtn_code='1', rtn_msg=$2
			WHERE id=$1 AND status='processing'`, attemptID, "recovered via QueryTrade"); err != nil {
			log.Warn().Err(err).Str("attempt_id", attemptID).Msg("vip renewal: mark today's attempt paid (recovered) failed")
		}
		return false
	case queryTradeUnpaid:
		h.convergeRenewalVoidStale(ctx, pending, "voided via QueryTrade convergence (unpaid/not found), superseded by retry")
		return true
	default: // queryTradeFailed：查詢本身失敗，狀態依然未知，維持保守
		msg := "order create conflict: stuck pending order status still unknown, QueryTrade query failed (transport error): "
		if qErr != nil {
			msg += qErr.Error()
		}
		h.markRenewalAttemptFailed(ctx, attemptID, "", msg)
		log.Error().Str("user_id", c.UserID).Str("subscription_id", c.SubscriptionID).Str("order_id", pending.OrderID).
			Msg("vip renewal: QueryTrade convergence query itself failed, stuck order left untouched, needs manual reconciliation")
		// 比照 handleRenewalChargeFailure 做同樣的 finalize 判斷與通知（修 [2][6]：舊版這個分支完全
		// 不終結也不通知，若剛好是第 3 次以上的嘗試，訂閱會無限期卡在「已達重試上限卻從未終結」）。
		final := rawAttemptNo >= renewalMaxAttempts
		outcome := renewalFinalizeDone
		if final {
			outcome = h.finalizeRenewalSubscriptionGuarded(ctx, c)
		}
		if outcome != renewalFinalizeRecoveredPaid {
			level, title, body := classifyRenewalFailureMail("failed", rawAttemptNo, final && outcome == renewalFinalizeDone)
			h.sendRenewalMail(ctx, c.UserID, level, title, body)
		}
		return false
	}
}

// renewalFinalizeOutcome finalizeRenewalSubscriptionGuarded 的收斂結果。
type renewalFinalizeOutcome int

const (
	renewalFinalizeDone          renewalFinalizeOutcome = iota // 實際終結了訂閱（原本行為）
	renewalFinalizeRecoveredPaid                                // 收斂成已付，視同續約成功，未終結
	renewalFinalizeBlocked                                      // 收斂結果仍未知（QueryTrade 查詢失敗），保留待下一輪，未終結
)

// finalizeRenewalSubscriptionGuarded 終結訂閱前的前置檢查（對抗式審查修正 high [4]）：若該訂閱的
// 使用者名下存在尚未收斂的 pending 續約訂單，絕不能直接終結——那筆訂單背後的錢可能其實已經收了，
// 若這時對外宣告「訂閱已終止」，就是「已扣款卻告知使用者服務中止」的嚴重風險。改為終結前先嘗試用
// QueryTrade 收斂：
//   - 收斂成已付 → 直接結算該筆訂單，不終結訂閱（訂閱視為續約成功）。
//   - 收斂成未付/查無 → 已無阻擋因素，作廢舊單後正常終結。
//   - 查詢本身仍失敗 → 兩者都不做，只記錄 log.Error + Telegram 營運告警，訂閱保持 active，留給下一輪
//     排程（明天，或本函式下次被呼叫時）重新判斷——絕不能在狀態不明時貿然終結。
//
// 呼叫端（finalizeRenewalOverLimit／handleRenewalChargeFailure／convergeStuckPendingOrder）一律呼叫
// 本函式取代直接呼叫 finalizeRenewalSubscription，並依回傳的 outcome 決定要不要發「已終止」通知。
func (h *BindHandler) finalizeRenewalSubscriptionGuarded(ctx context.Context, c renewalCandidate) renewalFinalizeOutcome {
	pending, err := h.findPendingRenewalOrder(ctx, c.UserID)
	if err != nil {
		// 前置檢查本身失敗：寧可維持舊行為直接終結，避免因為這裡出錯就永遠卡住不終結、使用者續約
		// 真的失敗了卻一直收不到通知。
		log.Error().Err(err).Str("subscription_id", c.SubscriptionID).Str("user_id", c.UserID).
			Msg("vip renewal: finalize pre-check load pending order failed, finalizing without convergence")
		h.finalizeRenewalSubscription(ctx, c.SubscriptionID)
		return renewalFinalizeDone
	}
	if pending == nil || !pending.IsRenewalAttempt {
		h.finalizeRenewalSubscription(ctx, c.SubscriptionID)
		return renewalFinalizeDone
	}

	resp, qErr := h.client.QueryTrade(ctx, pending.TradeNo)
	switch classifyQueryTradeResult(resp, qErr) {
	case queryTradePaid:
		h.convergeRenewalPaid(ctx, pending, resp, c)
		return renewalFinalizeRecoveredPaid
	case queryTradeUnpaid:
		h.convergeRenewalVoidStale(ctx, pending, "voided during finalize pre-check (unpaid/not found)")
		h.finalizeRenewalSubscription(ctx, c.SubscriptionID)
		return renewalFinalizeDone
	default: // queryTradeFailed
		log.Error().Str("subscription_id", c.SubscriptionID).Str("user_id", c.UserID).Str("order_id", pending.OrderID).
			Msg("vip renewal: finalize blocked — pending renewal order status still unknown after QueryTrade, subscription left active, needs manual reconciliation")
		alertCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if tgErr := notify.Telegram(alertCtx, fmt.Sprintf(
			"⚠️ <b>VIP 續約狀態未明</b>\n訂閱：%s\n使用者：%s\n卡住訂單：%s\n原因：終結前 QueryTrade 收斂查詢失敗，訂閱保留 active 待人工確認",
			c.SubscriptionID, c.UserID, pending.OrderID)); tgErr != nil {
			log.Warn().Err(tgErr).Str("subscription_id", c.SubscriptionID).Msg("vip renewal: telegram alert failed")
		}
		return renewalFinalizeBlocked
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

// renewalUnknownMail handleRenewalChargeUnknown 用的文案（對抗式審查修正 [1]）：CreatePayment
// 傳輸層/未知錯誤發生當下，我方還不知道錢到底扣了沒，不能講「失敗」也不能講「成功」，只能誠實告知
// 「確認中」——這正是舊版完全沒有通知使用者的分支，使用者原本全程無感。
func renewalUnknownMail() (level, title, body string) {
	return "normal", "VIP 自動續訂處理中", "本次自動扣款結果確認中，系統將自動處理，無需操作"
}

// renewalRetryMail 非最終失敗的重試通知文案（產品拍板原文字，見任務規格）。對抗式審查修正 [5]：
// 補上「建議更換卡片」的引導——扣款持續失敗最常見的根因就是這張卡本身有問題（額度不足/已過期/被
// 風控標記），單純提示「確認額度」對使用者幫助有限，讓使用者知道有「換卡」這個選項能更快解決問題。
// 不做卡片比對邏輯（判斷使用者是否真的換了卡需要另外追蹤 card fingerprint，且涉及綠界風控計數範圍
// 需另外找綠界確認，這裡先只做文案引導，不 investment 額外邏輯，見任務規格）。
func renewalRetryMail(attemptNo int) (level, title, body string) {
	return "important", "VIP 自動扣款失敗",
		fmt.Sprintf("扣款失敗（第 %d/%d 次），將於明日自動重試，請確認卡片額度；若持續失敗，建議更換其他卡片後重新訂閱", attemptNo, renewalMaxAttempts)
}

// renewalThreeDSMail 3DS 風險分流通知文案（產品拍板原文字，見任務規格）：幕後扣款被風控要求 3D，
// 無人值守做不到，需使用者手動到前景完成。
func renewalThreeDSMail() (level, title, body string) {
	return "important", "VIP 自動續訂需要驗證", "本次自動扣款需要持卡驗證，請於 App 內手動完成續費"
}

// renewalFinalFailMail 最終失敗（用滿重試次數或超過上限）通知文案（產品拍板原文字，見任務規格）。
// 對抗式審查修正 [5]：同 renewalRetryMail，補上換卡引導——此時使用者若要繼續訂閱 VIP，只能重新走一次
// Subscribe 流程，換張卡是最直接的解法，明確告知比讓使用者自己猜測更好。
func renewalFinalFailMail() (level, title, body string) {
	return "urgent", "VIP 自動續訂已停止", "多次扣款失敗，VIP 將於到期後暫停；建議更換其他卡片後重新訂閱"
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

	// 5) attempt 列標記 paid（CAS：僅在尚未是 paid 時才覆寫，放寬自舊版「僅 processing→paid」——
	// 對抗式審查修正 [3]：QueryTrade 收斂流程（見下方「unknown 收斂」區塊）可能對一筆早已被
	// handleRenewalChargeUnknown 標成 'failed' 的舊 attempt 列呼叫本函式（我方當初誤判扣款結果未知
	// /失敗，QueryTrade 一查才發現其實已經付款成功），若 WHERE 仍卡死 status='processing'，這種情況
	// 就會 0 列命中、稽核表（vip_renewal_attempts）永遠停在『failed』，與實際金流（orders 已 paid、
	// VIP 已延長）長期脫節，人工事後對帳會被誤導。'paid' 本身不再被覆寫（CAS 終態，見 WHERE 條件），
	// 其餘任何狀態（processing/failed/threeds_required）都允許收斂成 paid。
	attemptTag, err := tx.Exec(ctx, `
		UPDATE vip_renewal_attempts SET status='paid', rtn_code='1', rtn_msg='paid'
		WHERE id=$1 AND status<>'paid'`, attemptID)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("settle vip renewal: mark attempt paid: %w", err)
	}
	if attemptTag.RowsAffected() == 0 {
		log.Warn().Str("attempt_id", attemptID).Str("order_id", orderID).
			Msg("settle vip renewal: attempt row already paid or missing at settle time (order/VIP still settled normally; audit row itself was a no-op)")
	}

	if err := tx.Commit(ctx); err != nil {
		return "", time.Time{}, fmt.Errorf("settle vip renewal: commit: %w", err)
	}
	return userID, periodEnd, nil
}
