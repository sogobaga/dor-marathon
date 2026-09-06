package einvoice

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/notify"
)

// maxAttempts 最多重試次數（含首次），達此上限即停止重試並告警。
const maxAttempts = 4

// backoffSchedule 第 1/2/3 次失敗後的重試等待（見 PRODUCT DECISIONS #7）：1m, 5m, 30m。
// 索引 = attempts-1（attempts 由 Repository.BeginIssuing 回傳，第一次嘗試後為 1）；
// 超出陣列長度（不應發生，maxAttempts=4 只需要 3 個間隔）一律取最後一個值防呆。
var backoffSchedule = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute}

// defaultIssueSince einvoice_issue_since 設定為空時的內建預設（見 PRODUCT DECISIONS #6）。
const defaultIssueSince = "2026-09-07"

// Issuer 電子發票背景引擎：決策（是否要開立）→ 呼叫 ECPay → 落地結果 → 失敗重試（in-process
// 計時器＋SweepPending 兜底）。是本套件對外的主要入口，Wire 階段的 HTTP handler／付款結算路徑
// 只需要持有一個 *Issuer 呼叫這裡的方法，不需要直接碰 Repository/Client。
type Issuer struct {
	cfg    Config
	client *Client
	repo   *Repository
	db     *pgxpool.Pool // 供 appsettings.GetString 讀取 einvoice_auto_issue/einvoice_issue_since
}

// NewIssuer 建構子。
func NewIssuer(cfg Config, db *pgxpool.Pool) *Issuer {
	return &Issuer{cfg: cfg, client: NewClient(cfg), repo: NewRepository(db), db: db}
}

// Enqueue 非同步排入一次開立嘗試，立即返回（供付款結算路徑呼叫，不可阻塞金流結算的 DB 交易）。
// 內部另開 goroutine + 獨立 context（呼叫端的 request context 常在 goroutine 執行完前就已取消）。
func (h *Issuer) Enqueue(orderID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h.attempt(ctx, orderID, false); err != nil {
			log.Warn().Err(err).Str("order_id", orderID).Msg("einvoice: enqueued attempt failed")
		}
	}()
}

// IssueNow 同步執行一次開立嘗試（供後台手動開立/重試使用）；manual=true 時略過
// einvoice_auto_issue／einvoice_issue_since 兩項設定門檻，但零元/虛擬會員/測試金流三項硬規則一律
// 適用（見 decideSkip）。
func (h *Issuer) IssueNow(ctx context.Context, orderID string, manual bool) error {
	return h.attempt(ctx, orderID, manual)
}

// attempt 是 Enqueue／IssueNow／SweepPending 共用的單次嘗試流程。
func (h *Issuer) attempt(ctx context.Context, orderID string, manual bool) error {
	snap, err := h.repo.LoadOrderSnapshot(ctx, orderID)
	if err != nil {
		return err
	}

	status, err := h.repo.CurrentStatus(ctx, orderID)
	if err != nil {
		return err
	}
	if status == "issued" || status == "void" {
		// 已是終態：manual 觸發若想「查最新狀態」應呼叫 Sync，若想「作廢」應呼叫 Void，不應該落到
		// 這裡重新開立一次（會直接被下面的略過判斷擋到 skipped 也不對，故在此提早返回）。
		return nil
	}

	paidAt, err := h.repo.PaidAt(ctx, orderID)
	if err != nil {
		return err
	}
	if skip, reason := h.decide(ctx, snap, paidAt, manual); skip {
		return h.repo.MarkSkipped(ctx, orderID, reason)
	}

	// 訂單狀態自付款那一刻起可能已經改變（例如在重試 backoff 等待的 1~30 分鐘內被全額退款/連動取消
	// ——finalizeIfFullyRefunded 會把 orders.status 改成 refunded）：decideSkip 本身完全不知道
	// orders.status（Core 純函式設計上假設呼叫端只在真的付款成功當下才會呼叫一次），所以每次真正
	// 嘗試開立前都要在這裡重新確認訂單目前確實還是「已付款」，否則會對一筆已經退款的訂單開出一張
	// 真的 ECPay 發票、且沒有任何後續機制會為它補開折讓（見 review finding #3）。與後台手動開立
	// 端點（handler.go AdminHandler.Issue）的 OrderStatus 檢查是同一顆方法，行為一致。
	orderStatus, err := h.repo.OrderStatus(ctx, orderID)
	if err != nil {
		return err
	}
	if orderStatus != "paid" {
		return h.repo.MarkSkipped(ctx, orderID, "order_status_changed")
	}

	ok, attempts, err := h.repo.BeginIssuing(ctx, orderID)
	if err != nil {
		return err
	}
	if !ok {
		// 已被另一個呼叫搶先處理（sweep 與 webhook 同時觸發等）：安靜放棄，不是錯誤。
		return nil
	}

	relateNumber := RelateNumberFor(orderID)
	if attempts > 1 {
		// 重試前一律先查是否其實已經開立成功過（見 §7：官方文件沒有明確的重複 RelateNumber
		// 錯誤碼，保守起見用 GetIssue 復原，避免對同一張發票重送 Issue）。GetIssue 本身失敗
		// （查無此筆／傳輸錯誤）都當作「尚未開立，繼續往下走正常開立流程」，不中止本次嘗試。
		if recovered, _ := h.RecoverByRelateNumber(ctx, orderID, relateNumber); recovered {
			return nil
		}
	}

	req, err := BuildIssueRequest(*snap, h.cfg.Env)
	if err != nil {
		return h.fail(ctx, orderID, attempts, manual, err.Error())
	}
	resp, err := h.client.Issue(ctx, req)
	if err != nil {
		return h.fail(ctx, orderID, attempts, manual, err.Error())
	}
	// 落地寫入用獨立的短逾時 context（WithoutCancel 脫離呼叫端 ctx 的取消/逾時，另外套一個
	// 自己的 5s 上限）：ECPay 這通 Issue 已經回應成功，代表一張真實發票已經存在，即使呼叫端的
	// context 這時候剛好逾時（例如 Enqueue 的 30s 總預算被 ECPay 慢回應／Neon 冷啟動吃光），
	// 這筆「已開立」的結果仍然必須想辦法落地，不能因為 ctx 過期就直接 return err——那樣會讓
	// order_invoices 卡在 issuing、relate_number 仍是空字串，變成一張本地完全查無紀錄、任何
	// 既有工具（重試/Sync/DuePending）都撿不回來的孤兒發票（見 review finding #1）。
	markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := h.repo.MarkIssued(markCtx, orderID, req, *resp, h.cfg.Env, h.cfg.MerchantID); err != nil {
		// 寫入本身失敗：改走 fail()（而不是直接 return err）把這次嘗試落地為 failed＋排入下一次
		// 重試，讓 attempts 保持遞增。下一次重試（attempts>1）一開始就會呼叫 RecoverByRelateNumber，
		// 用純算出來的 RelateNumberFor(orderID)（不依賴本地是否存過 relate_number）呼叫 GetIssue
		// 找回這張已經真實存在的發票並收斂為 issued，不會對 ECPay 重複送出 Issue。
		log.Error().Err(err).Str("order_id", orderID).Str("invoice_no", resp.InvoiceNo).
			Msg("einvoice: ECPay issued but local MarkIssued write failed; will recover via GetIssue on retry")
		return h.fail(ctx, orderID, attempts, manual,
			fmt.Sprintf("ECPay issued (invoice_no=%s) but local write failed: %s", resp.InvoiceNo, err.Error()))
	}
	log.Info().Str("order_id", orderID).Str("invoice_no", resp.InvoiceNo).Msg("einvoice: issued")
	return nil
}

// decide 包一層 appsettings 讀取，把純邏輯（decideSkip）與 DB/manual 參數接起來。
func (h *Issuer) decide(ctx context.Context, snap *OrderSnapshot, paidAt time.Time, manual bool) (skip bool, reason string) {
	autoIssueOn := appsettings.GetString(ctx, h.db, "einvoice_auto_issue", "off") == "on"
	issueSince := appsettings.GetString(ctx, h.db, "einvoice_issue_since", defaultIssueSince)
	return decideSkip(decideSkipInput{
		TotalCents:   snap.TotalCents,
		IsVirtual:    snap.IsVirtual,
		CfgEnv:       h.cfg.Env,
		PaidTxEnv:    snap.PaidTxEnv,
		Manual:       manual,
		AutoIssueOn:  autoIssueOn,
		IssueSince:   issueSince,
		PaidAtTaipei: paidAt.UTC().Add(8 * time.Hour),
	})
}

// decideSkipInput decideSkip 的輸入（純函式，見下）。
type decideSkipInput struct {
	TotalCents   int
	IsVirtual    bool
	CfgEnv       string    // 系統電子發票環境設定：stage|prod
	PaidTxEnv    string    // 該筆訂單付款交易的 ecpay_env：stage|prod|""（查無，如後台人工標記付款）
	Manual       bool      // 後台手動觸發：略過 AutoIssueOn/IssueSince，但不略過前三項硬規則
	AutoIssueOn  bool      // 設定 einvoice_auto_issue == "on"
	IssueSince   string    // 設定 einvoice_issue_since（"YYYY-MM-DD"，台北曆日；空字串已由呼叫端代入預設值）
	PaidAtTaipei time.Time // 已換算成台北時間的訂單付款時刻
}

// decideSkip 純函式：PRODUCT DECISIONS #1 的完整略過判斷。不觸網、不觸 DB，是本套件單元測試覆蓋
// 的核心分支。回傳 skip=true 時 reason 對應 order_invoices.skip_reason（≤40 字，皆為英文 slug）。
func decideSkip(in decideSkipInput) (skip bool, reason string) {
	if in.TotalCents <= 0 {
		return true, "zero_amount"
	}
	if in.IsVirtual {
		return true, "virtual_user"
	}
	// 測試金流：發票環境為 prod，但這筆訂單的付款交易走的是非 prod（測試假錢）——不論手動與否
	// 一律擋下，避免正式發票環境對外開出一張「其實沒真的收到錢」的發票。PaidTxEnv 為空字串
	// （查無任何 paid 交易紀錄，例如後台人工標記付款）在 CfgEnv=prod 時同樣視為不可信、一併擋下。
	if in.CfgEnv == "prod" && in.PaidTxEnv != "prod" {
		return true, "test_money"
	}
	// 反向也擋：發票環境還在 stage（正式憑證未設齊）時，真錢（prod 付款）的訂單不開測試發票——
	// 否則會被標成 issued(stage)，之後切到正式環境時不會再被開一次，這筆真實交易就永遠沒有正式發票。
	// 這類訂單留在 skipped(stage_env_real_money)，屬「軟略過」：sweep 會在正式憑證設好後自動重新評估
	// （見 Repository.DuePending 的 softSkipReasons）。stage 環境只對 stage 付款（測試單）開立。
	if in.CfgEnv != "prod" && in.PaidTxEnv == "prod" {
		return true, "stage_env_real_money"
	}
	if in.Manual {
		return false, ""
	}
	if !in.AutoIssueOn {
		return true, "auto_issue_off"
	}
	since := in.IssueSince
	if since == "" {
		since = defaultIssueSince
	}
	sinceDate, err := time.Parse("2006-01-02", since)
	if err != nil {
		sinceDate, _ = time.Parse("2006-01-02", defaultIssueSince)
	}
	paidDate, _ := time.Parse("2006-01-02", in.PaidAtTaipei.Format("2006-01-02"))
	if paidDate.Before(sinceDate) {
		return true, "before_issue_since"
	}
	return false, ""
}

// fail 記錄本次失敗、視 attempts 決定要不要排下一次重試或直接告警放棄。
func (h *Issuer) fail(ctx context.Context, orderID string, attempts int, manual bool, errMsg string) error {
	var next *time.Time
	switch {
	case manual:
		// 後台手動開立失敗：不排自動重試、不發告警——錯誤已直接顯示在後台面板，由管理者看過訊息再按一次。
		// next_attempt_at 留 NULL，DuePending 對 failed 列要求 next_attempt_at 非 NULL 才撿回，故整點掃描
		// 也不會用自動路徑（manual=false → 自動開立關閉時會被改標 skipped，把錯誤訊息蓋掉）去碰它。
	case attempts < maxAttempts:
		idx := attempts - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(backoffSchedule) {
			idx = len(backoffSchedule) - 1
		}
		t := time.Now().Add(backoffSchedule[idx])
		next = &t
		time.AfterFunc(backoffSchedule[idx], func() { h.Enqueue(orderID) })
	default:
		notify.Alert("einvoice_failed", "電子發票開立失敗（已達重試上限）",
			fmt.Sprintf("order_id=%s attempts=%d error=%s", orderID, attempts, errMsg))
	}
	// 同理（見 attempt() 內 MarkIssued 前的說明）：落地「這次失敗＋下次何時重試」用獨立短逾時
	// context，不依賴呼叫端 ctx 是否仍有效——否則這筆記錄寫不進去，order_invoices 會卡在
	// 'issuing'（BeginIssuing 已經先轉過去了），且已經排入的 time.AfterFunc 重試計時器也救不了它
	// （CAS 條件只認 pending/failed，見 repository.go BeginIssuing），只能靠 SweepPending 的
	// stale-issuing 回收機制兜底（見 review finding #2/#5）。
	markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if mErr := h.repo.MarkFailed(markCtx, orderID, errMsg, next); mErr != nil {
		log.Error().Err(mErr).Str("order_id", orderID).
			Msg("einvoice: mark failed write itself failed; row may be stuck at issuing until the stale-issuing reaper reclaims it")
		return mErr
	}
	return fmt.Errorf("einvoice: issue failed (attempt %d): %s", attempts, errMsg)
}

// RecoverByRelateNumber 用 GetIssue 查詢這個 RelateNumber 是否其實已經開立成功，若是則直接
// ApplySync 把本地狀態收斂為 issued，回傳 recovered=true。查無此筆／傳輸錯誤都回傳
// recovered=false, err=nil——呼叫端（attempt）應把這種情況當作「尚未開立過」，繼續走正常開立
// 流程，而不是把 GetIssue 本身的失敗當成整個發票流程失敗。
func (h *Issuer) RecoverByRelateNumber(ctx context.Context, orderID, relateNumber string) (recovered bool, err error) {
	resp, err := h.client.GetIssue(ctx, GetIssueReq{RelateNumber: relateNumber})
	if err != nil {
		// 查無此筆（RtnCode!=1）＝尚未開立過，屬正常；傳輸／解析失敗也不中止流程，但一定要留 log——
		// 靜默吞掉會讓「其實已開成、只是讀不回」的情況直接撞 Issue 的自訂編號重覆錯誤（正式環境實測）。
		log.Warn().Err(err).Str("order_id", orderID).Str("relate_number", relateNumber).Msg("einvoice: GetIssue recovery lookup failed; proceeding as not issued")
		return false, nil
	}
	if string(resp.IISIssueStatus) != "1" {
		return false, nil
	}
	if err := h.repo.ApplySync(ctx, orderID, *resp); err != nil {
		return false, err
	}
	log.Info().Str("order_id", orderID).Str("relate_number", relateNumber).
		Msg("einvoice: recovered already-issued invoice before retry")
	return true, nil
}

// Sync 供後台「同步」端點：用 GetIssue 刷新本地發票狀態/號碼/剩餘可折讓金額。
func (h *Issuer) Sync(ctx context.Context, orderID string) error {
	resp, err := h.client.GetIssue(ctx, GetIssueReq{RelateNumber: RelateNumberFor(orderID)})
	if err != nil {
		return err
	}
	return h.repo.ApplySync(ctx, orderID, *resp)
}

// Void 作廢一張已開立的發票（後台觸發；捐贈發票不可作廢——由呼叫端在 UI 層擋下，這裡仍會照樣把
// ECPay 回傳的錯誤如實傳回，不額外重複擋一次規則，單一事實來源交給 ECPay 自己的業務驗證）。
func (h *Issuer) Void(ctx context.Context, orderID, reason string) error {
	inv, err := h.repo.GetInvoiceDetail(ctx, orderID)
	if err != nil {
		return err
	}
	if inv == nil || inv.Status != "issued" {
		return fmt.Errorf("einvoice: void requires an issued invoice (order=%s)", orderID)
	}
	reason = truncateRunes(reason, 20)
	if _, err := h.client.Invalid(ctx, InvalidReq{
		InvoiceNo:   inv.InvoiceNumber,
		InvoiceDate: formatECPayDateOnly(inv.InvoiceDate),
		Reason:      reason,
	}); err != nil {
		notify.Alert("einvoice_void_failed", "電子發票作廢失敗",
			fmt.Sprintf("order_id=%s invoice_no=%s error=%s", orderID, inv.InvoiceNumber, err.Error()))
		return err
	}
	return h.repo.MarkVoided(ctx, orderID, reason)
}

// Allowance 對一筆已開立的發票開立折讓（退款成功時呼叫；見 PRODUCT DECISIONS #3）。refundID 非
// nil 時受 DB 的 UNIQUE(refund_id) 保護，同一筆退款重複呼叫會冪等短路，不會折讓兩次。訂單尚未
// 開立過發票（零元/虛擬/測試金流/自動開立關閉等被略過的訂單）安靜略過——這些訂單本來就不會有
// 發票可折讓，不是錯誤。
func (h *Issuer) Allowance(ctx context.Context, orderID string, amountNTD int, reason string, refundID *string) error {
	inv, err := h.repo.GetInvoiceDetail(ctx, orderID)
	if err != nil {
		return err
	}
	if inv == nil || inv.Status != "issued" {
		return nil
	}

	allowanceID, existed, err := h.repo.InsertAllowancePending(ctx, inv.OrderInvoiceID, refundID, amountNTD, reason)
	if err != nil {
		return err
	}
	if existed {
		return nil
	}

	snap, err := h.repo.LoadOrderSnapshot(ctx, orderID)
	if err != nil {
		return err
	}
	email := snap.UserEmail
	if h.cfg.Env != "prod" {
		email = stageDummyEmail
	}

	itemName := truncateRunes("退款："+reason, 500)
	resp, err := h.client.Allowance(ctx, AllowanceReq{
		InvoiceNo:       inv.InvoiceNumber,
		InvoiceDate:     formatECPayDateOnly(inv.InvoiceDate),
		AllowanceNotify: "E",
		NotifyMail:      email,
		AllowanceAmount: amountNTD,
		Reason:          truncateRunes(reason, 50),
		Items: []AllowanceItem{{
			ItemSeq: 1, ItemName: itemName, ItemCount: 1, ItemWord: "式",
			ItemPrice: amountNTD, ItemTaxType: "1", ItemAmount: amountNTD,
		}},
	})
	if err != nil {
		_ = h.repo.MarkAllowanceFailed(ctx, allowanceID, err.Error())
		notify.Alert("einvoice_allowance_failed", "電子發票折讓失敗",
			fmt.Sprintf("order_id=%s invoice_no=%s amount=%d error=%s", orderID, inv.InvoiceNumber, amountNTD, err.Error()))
		return err
	}
	raw, _ := json.Marshal(resp)
	return h.repo.MarkAllowanceSuccess(ctx, allowanceID, resp.IAAllowNo, time.Now(), raw)
}

// ================= Wire 階段：小介面卡榫（見 race.InvoiceHook／payment.InvoiceOrderPaidHook／
// payment.InvoiceRefundHook）=================
//
// 這三個呼叫端各自定義了獨立的小介面（避免直接 import internal/einvoice、降低耦合），方法名彼此一致
// （OrderPaid／RefundSettled），因此 *Issuer 只需要各提供一個同名方法即可同時滿足三邊，不需要另外
// 在 main.go 寫轉接層。

// OrderPaid 供 race.InvoiceHook／payment.InvoiceOrderPaidHook 卡榫：委派給 Enqueue（本身已是非同步、
// 立即返回）。
func (h *Issuer) OrderPaid(orderID string) {
	h.Enqueue(orderID)
}

// RefundSettled 供 payment.InvoiceRefundHook 卡榫：內部另開 goroutine + 獨立 timeout context 呼叫
// Allowance，呼叫端（退款流程）不必自己管 goroutine/context。折讓失敗不影響退款本身——Allowance
// 內部已經處理落地 failed 狀態＋notify.Alert，這裡只再補一層呼叫層級的 log，方便從這一側追蹤。
func (h *Issuer) RefundSettled(orderID, refundID string, amountCents int, reason string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rid := refundID
		amountNTD := amountCents / 100
		if amountCents%100 != 0 {
			// 退款金額不是 100 的倍數（理論上不應發生——訂單金額本身恆為整數 NTD，見專案既有筆記；
			// 這裡防的是退款金額本身填了非整數，例如後台手動退款輸入框沒擋住小數，見 review
			// finding #4）：改用四捨五入而不是無聲整除捨去，並告警讓人工核對折讓金額是否與實際退款
			// 金額一致。
			amountNTD = int(math.Round(float64(amountCents) / 100))
			log.Warn().Str("order_id", orderID).Str("refund_id", refundID).Int("amount_cents", amountCents).
				Int("rounded_ntd", amountNTD).Msg("einvoice: refund amount is not a whole NTD amount; rounding for allowance")
			notify.Alert("einvoice_refund_amount_not_whole_ntd", "退款金額非整數 NTD，折讓金額已四捨五入",
				fmt.Sprintf("order_id=%s refund_id=%s amount_cents=%d rounded_ntd=%d", orderID, refundID, amountCents, amountNTD))
		}
		if err := h.Allowance(ctx, orderID, amountNTD, reason, &rid); err != nil {
			log.Warn().Err(err).Str("order_id", orderID).Str("refund_id", refundID).
				Msg("einvoice: refund settled but allowance failed (see einvoice_allowance_failed alert for detail)")
		}
	}()
}

// ShouldReportDaily 供每日營運報告使用（見 internal/ops dailyreport.go）：判斷「電子發票」這行要不要
// 顯示——auto-issue 開啟中，或曾經有過任何發票紀錄（哪怕現在關閉，避免歷史資料看起來像消失）。
func (h *Issuer) ShouldReportDaily(ctx context.Context) (bool, error) {
	if appsettings.GetString(ctx, h.db, "einvoice_auto_issue", "off") == "on" {
		return true, nil
	}
	return h.repo.AnyInvoiceRows(ctx)
}

// DailyCounts 透傳 Repository.DailyCounts（供 internal/ops dailyreport.go 使用，見 ShouldReportDaily）。
func (h *Issuer) DailyCounts(ctx context.Context, dayStart, dayEnd time.Time) (issued, failed, pending int, err error) {
	return h.repo.DailyCounts(ctx, dayStart, dayEnd, maxAttempts)
}

// staleIssuingThreshold ReapStuckIssuing 的逾時門檻：正常一次 attempt() 從 BeginIssuing 到終態
// 只需要一次 ECPay HTTP 呼叫（20s 逾時）加兩次 DB 寫入，Enqueue 本身的總預算也只有 30s；10 分鐘
// 留了充分餘裕給 Neon 冷啟動（見專案 memory ops-monitoring 的已知延遲），同時仍遠短於下一次 hourly
// sweep 週期，確保卡住的訂單最多一次 sweep 週期內就會被發現。
const staleIssuingThreshold = 10 * time.Minute

// SweepPending 兜底掃描：先回收卡住的 issuing 列（見 ReapStuckIssuing／review finding #2/#5），
// 再把逾期未重試（next_attempt_at 已到期，或因服務重啟遺失 in-process 計時器）且未達重試上限的
// pending/failed 訂單重新排入。供 internal/ops selfcheck 每小時排程呼叫，不另開週期性 DB 輪詢
// （見任務規格 ⚠️ Neon 必須允許休眠的限制）。
func (h *Issuer) SweepPending(ctx context.Context) {
	if reaped, err := h.repo.ReapStuckIssuing(ctx, staleIssuingThreshold); err != nil {
		log.Error().Err(err).Msg("einvoice: reap stuck issuing query failed")
	} else if len(reaped) > 0 {
		log.Warn().Strs("order_ids", reaped).Msg("einvoice: reclaimed orders stuck in issuing status")
		notify.Alert("einvoice_stuck_issuing_reaped", "電子發票有訂單卡在「開立中」被自動回收重試",
			fmt.Sprintf("count=%d order_ids=%v", len(reaped), reaped))
	}

	ids, err := h.repo.DuePending(ctx, maxAttempts)
	if err != nil {
		log.Error().Err(err).Msg("einvoice: sweep pending query failed")
		return
	}
	for _, id := range ids {
		h.Enqueue(id)
	}
}
