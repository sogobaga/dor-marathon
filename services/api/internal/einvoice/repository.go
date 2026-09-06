package einvoice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrOrderNotFound 找不到該訂單（orders 表無此列）。
var ErrOrderNotFound = errors.New("einvoice: order not found")

// Repository 電子發票資料存取層：order_invoices／order_invoice_allowances 兩張表的全部讀寫，
// 以及組 OrderSnapshot 所需的跨表查詢（orders/users/user_profiles/races/order_items/
// payment_transactions）。不含任何 ECPay API 呼叫（見 client.go）或決策邏輯（見 issuer.go）。
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository 建構子。
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// AdminInvoiceRow GET /admin/invoices 列表單筆（Wire 階段的 handler 轉成 JSON 前先過一層格式化，
// 見 handler.go toAdminInvoiceRowJSON，不直接序列化這個型別——InvoiceDate 需要轉成純日期字串）。
type AdminInvoiceRow struct {
	OrderID         string
	UserDisplayName string
	AmountNTD       int
	Status          string
	InvoiceNumber   string
	InvoiceDate     *time.Time
	LastError       string
	SkipReason      string
	UpdatedAt       time.Time
	BuyerType       string
}

// AllowanceRow order_invoice_allowances 單筆。
type AllowanceRow struct {
	ID            string
	RefundID      *string
	AmountNTD     int
	Reason        string
	Status        string
	AllowanceNo   string
	AllowanceDate *time.Time
	LastError     string
	CreatedAt     time.Time
}

// InvoiceDetail GET /admin/orders/{id}/invoice 回應（發票本體 + 折讓列表 + 最近錯誤）。Wire 階段的
// handler 轉成 JSON 前先過一層格式化（見 handler.go toEInvoiceDetailJSON），不直接序列化這個型別。
type InvoiceDetail struct {
	OrderID string
	// OrderInvoiceID order_invoices.id，供 Issuer.Allowance 建折讓列的 FK 用，不對外曝露。
	OrderInvoiceID string
	// 買受人快照（見 race.InvoiceInfo／race.ValidateInvoice；本套件不重複驗證格式）：VIP 訂單懶建立後
	// BuyerType 恆為 'personal'、其餘欄位皆空，與 mapping.go OrderInvoiceInput 同一組欄位。
	BuyerType          string
	TaxID              string
	Title              string
	CarrierType        string
	CarrierID          string
	LoveCode           string
	Status             string
	InvoiceNumber      string
	InvoiceDate        *time.Time
	InvoiceDateTime    *time.Time // 開立完整時刻（後台顯示「issued_at」；invoice_date 只到日期）
	RandomNumber       string
	RelateNumber       string
	EcpayEnv           string // 開立當下用的電子發票環境 stage|prod（尚未開立過為空字串）
	Attempts           int
	LastError          string
	SkipReason         string
	VoidedAt           *time.Time
	VoidReason         string
	RemainAllowanceNTD *int
	SalesAmountNTD     int
	Allowances         []AllowanceRow
}

// EnsureInvoiceRow 為訂單懶建立一筆 order_invoices（若尚無）。賽事報名流程本來就會在報名當下建立
// 這一列（race/repository.go RegisterWithOrder），但 VIP 訂閱訂單（CreateVipOrder）沒有——此處統一
// 補上，buyer_type 預設 'personal'（雲端發票存證，未填任何買受人資訊），與 migration 169 規格一致。
func (r *Repository) EnsureInvoiceRow(ctx context.Context, orderID string) error {
	if _, err := r.db.Exec(ctx, `
		INSERT INTO order_invoices (order_id, buyer_type)
		VALUES ($1, 'personal')
		ON CONFLICT (order_id) DO NOTHING`, orderID); err != nil {
		return fmt.Errorf("einvoice: ensure invoice row: %w", err)
	}
	return nil
}

// LoadOrderSnapshot 組出開立/折讓一張發票所需的完整訂單快照。呼叫前自動 EnsureInvoiceRow，
// 因此回傳的 snap.Invoice 恆有值（VIP 訂單懶建立後為全空的 personal 預設）。
func (r *Repository) LoadOrderSnapshot(ctx context.Context, orderID string) (*OrderSnapshot, error) {
	if err := r.EnsureInvoiceRow(ctx, orderID); err != nil {
		return nil, err
	}

	var snap OrderSnapshot
	snap.OrderID = orderID
	var raceTitle, userPhone, paidTxEnv *string
	var vipItemType string
	err := r.db.QueryRow(ctx, `
		SELECT o.total_cents, u.is_virtual, u.email, COALESCE(u.name, u.handle),
		       up.phone, rc.title,
		       EXISTS(SELECT 1 FROM order_items oi WHERE oi.order_id = o.id AND oi.item_type = 'addon'),
		       COALESCE((SELECT oi2.item_type FROM order_items oi2
		                 WHERE oi2.order_id = o.id AND oi2.item_type IN ('vip_month','vip_year')
		                 LIMIT 1), ''),
		       (SELECT pt.ecpay_env FROM payment_transactions pt
		        WHERE pt.order_id = o.id AND pt.status = 'paid'
		        ORDER BY pt.paid_at DESC NULLS LAST, pt.created_at DESC LIMIT 1),
		       inv.buyer_type, inv.tax_id, inv.title, inv.carrier_type, inv.carrier_id, inv.love_code
		FROM orders o
		JOIN users u ON u.id = o.user_id
		LEFT JOIN user_profiles up ON up.user_id = o.user_id
		LEFT JOIN races rc ON rc.id = o.race_id
		JOIN order_invoices inv ON inv.order_id = o.id
		WHERE o.id = $1`, orderID).Scan(
		&snap.TotalCents, &snap.IsVirtual, &snap.UserEmail, &snap.DisplayName,
		&userPhone, &raceTitle, &snap.HasAddon, &vipItemType, &paidTxEnv,
		&snap.Invoice.BuyerType, &snap.Invoice.TaxID, &snap.Invoice.Title,
		&snap.Invoice.CarrierType, &snap.Invoice.CarrierID, &snap.Invoice.LoveCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("einvoice: load order snapshot: %w", err)
	}
	if userPhone != nil {
		snap.UserPhone = *userPhone
	}
	if raceTitle != nil {
		snap.RaceTitle = *raceTitle
	}
	if paidTxEnv != nil {
		snap.PaidTxEnv = *paidTxEnv
	}
	snap.VipItemType = vipItemType
	return &snap, nil
}

// OrderStatus 讀出訂單目前的 orders.status。供後台手動開立端點（Wire 階段 handler.go
// AdminHandler.Issue）在呼叫 Issuer.IssueNow 前檢查訂單是否真的已付款——不像自動路徑（race.Service.
// MarkOrderPaid／BindHandler 的兩個 settle 函式）天生只在付款 CAS 真正翻轉時才觸發，後台手動開立
// 若不額外檢查，admin 對一筆還在 pending 的訂單點「開立」會真的對 ECPay 開出一張沒收到錢的發票
// （decideSkip 本身不檢查 orders.status，因為 Core 設計上假設呼叫端只在付款成功後才會呼叫）。
func (r *Repository) OrderStatus(ctx context.Context, orderID string) (string, error) {
	var s string
	err := r.db.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1`, orderID).Scan(&s)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrOrderNotFound
	}
	if err != nil {
		return "", fmt.Errorf("einvoice: read order status: %w", err)
	}
	return s, nil
}

// PaidAt 讀出訂單的「已付款時刻」（COALESCE(paid_at, created_at) 防禦性 fallback：理論上呼叫端只會
// 對已付款訂單走發票流程，paid_at 應恆有值）。獨立於 LoadOrderSnapshot 之外——只有 Issuer 的略過
// 判斷需要這個值，其餘欄位（mapping 用）不需要，避免 OrderSnapshot 背負與開立內容無關的欄位。
func (r *Repository) PaidAt(ctx context.Context, orderID string) (time.Time, error) {
	var t time.Time
	err := r.db.QueryRow(ctx, `SELECT COALESCE(paid_at, created_at) FROM orders WHERE id = $1`, orderID).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrOrderNotFound
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("einvoice: load paid_at: %w", err)
	}
	return t, nil
}

// CurrentStatus 讀出 order_invoices.invoice_status；查無列（理論上不會發生，LoadOrderSnapshot 已懶
// 建立過）回傳空字串、不視為錯誤。
func (r *Repository) CurrentStatus(ctx context.Context, orderID string) (string, error) {
	var s string
	err := r.db.QueryRow(ctx, `SELECT invoice_status FROM order_invoices WHERE order_id = $1`, orderID).Scan(&s)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("einvoice: read invoice status: %w", err)
	}
	return s, nil
}

// BeginIssuing CAS：pending/failed → issuing，attempts+1。0 列受影響＝已被別的呼叫搶先處理
// （例如排程 sweep 與付款 webhook 同時觸發、或已是 issued/void/skipped 終態），回傳 ok=false，
// 呼叫端應安靜放棄，不視為錯誤。
func (r *Repository) BeginIssuing(ctx context.Context, orderID string) (ok bool, attempts int, err error) {
	// 'skipped' 也可進入：呼叫端（Issuer.attempt）在這之前一定先跑過 decide() 重新套用全部略過規則，
	// 走到這裡代表這筆現在已符合開立條件（軟略過的原因消失：自動開立打開／起算日調早／正式憑證設好，
	// 或後台手動開立）。硬略過（0 元／虛擬／測試錢）在 decide() 就會被重新標回 skipped，不會到這裡。
	err = r.db.QueryRow(ctx, `
		UPDATE order_invoices SET invoice_status = 'issuing', attempts = attempts + 1, updated_at = NOW()
		WHERE order_id = $1 AND invoice_status IN ('pending','failed','skipped')
		RETURNING attempts`, orderID).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("einvoice: begin issuing: %w", err)
	}
	return true, attempts, nil
}

// MarkIssued 開立成功：寫入發票號碼/日期/隨機碼並轉為終態 issued。
func (r *Repository) MarkIssued(ctx context.Context, orderID string, req IssueReq, resp IssueResp, env, merchantID string) error {
	invDate, invDT := parseECPayDateTime(resp.InvoiceDate)
	if _, err := r.db.Exec(ctx, `
		UPDATE order_invoices SET
			invoice_status = 'issued', invoice_number = $2, invoice_date = $3, invoice_datetime = $4,
			random_number = $5, relate_number = $6, ecpay_env = $7, merchant_id = $8,
			sales_amount_ntd = $9, last_error = '', next_attempt_at = NULL, updated_at = NOW()
		WHERE order_id = $1`,
		orderID, resp.InvoiceNo, invDate, invDT, resp.RandomNumber, req.RelateNumber, env, merchantID, req.SalesAmount,
	); err != nil {
		return fmt.Errorf("einvoice: mark issued: %w", err)
	}
	return nil
}

// MarkFailed 本次嘗試失敗：記錄錯誤訊息與下次重試時間（nextAttemptAt 為 nil 代表已達重試上限，
// 不會再被 SweepPending 撿回——見 issuer.go maxAttempts）。invoice_status 停在 'failed'。
func (r *Repository) MarkFailed(ctx context.Context, orderID, lastError string, nextAttemptAt *time.Time) error {
	if _, err := r.db.Exec(ctx, `
		UPDATE order_invoices SET invoice_status = 'failed', last_error = $2, next_attempt_at = $3, updated_at = NOW()
		WHERE order_id = $1`, orderID, truncateRunes(lastError, 2000), nextAttemptAt,
	); err != nil {
		return fmt.Errorf("einvoice: mark failed: %w", err)
	}
	return nil
}

// MarkSkipped 依規則判定不需要開立（零元/虛擬會員/測試金流/自動開立未開/曆日早於門檻），寫入
// skip_reason 供後台顯示與稽核；終態，不會被 SweepPending 撿回。
func (r *Repository) MarkSkipped(ctx context.Context, orderID, reason string) error {
	if _, err := r.db.Exec(ctx, `
		UPDATE order_invoices SET invoice_status = 'skipped', skip_reason = $2, updated_at = NOW()
		WHERE order_id = $1`, orderID, truncateRunes(reason, 40),
	); err != nil {
		return fmt.Errorf("einvoice: mark skipped: %w", err)
	}
	return nil
}

// MarkVoided 作廢成功：終態 void，記錄作廢時間與原因。
func (r *Repository) MarkVoided(ctx context.Context, orderID, reason string) error {
	if _, err := r.db.Exec(ctx, `
		UPDATE order_invoices SET invoice_status = 'void', voided_at = NOW(), void_reason = $2, updated_at = NOW()
		WHERE order_id = $1`, orderID, truncateRunes(reason, 20),
	); err != nil {
		return fmt.Errorf("einvoice: mark voided: %w", err)
	}
	return nil
}

// ApplySync 依 GetIssue 回應刷新本地狀態（後台「同步」端點，以及 Issuer.RecoverByRelateNumber
// 重試前的復原查詢共用這支）。IIS_Invalid_Status='1' 優先於 IIS_Issue_Status——已作廢的發票，
// 其 Issue_Status 依然是 '1'（已開立過，只是後來被作廢），void 才是正確的終態判讀。
func (r *Repository) ApplySync(ctx context.Context, orderID string, resp GetIssueResp) error {
	status := "issued"
	switch {
	case resp.IISInvalidStatus == "1":
		status = "void"
	case resp.IISIssueStatus == "1":
		status = "issued"
	default:
		// 查無已開立紀錄／Issue_Status='0'（已取消）：不當作我方的 failed 覆寫既有 attempts/
		// last_error 語意，只更新其餘唯讀欄位；呼叫端（Sync 手動觸發）另行決定要不要提示人工判斷。
		status = "failed"
	}
	remain := int(resp.IISRemainAllowanceAmt)
	if _, err := r.db.Exec(ctx, `
		UPDATE order_invoices SET
			invoice_status = $2, invoice_number = $3, random_number = $4,
			remain_allowance_ntd = $5, sales_amount_ntd = $6, updated_at = NOW()
		WHERE order_id = $1`,
		orderID, status, resp.IISNumber, resp.IISRandomNumber, remain, int(resp.IISSalesAmount),
	); err != nil {
		return fmt.Errorf("einvoice: apply sync: %w", err)
	}
	return nil
}

// ReapStuckIssuing 回收卡在 'issuing' 狀態太久的訂單（見 review finding #2/#5）：正常情況下
// BeginIssuing 轉成 issuing 後幾秒內就會被 attempt() 轉成 issued/failed 終態；若中途發生 process
// crash/redeploy，或 MarkIssued/MarkFailed 那筆終態寫入本身失敗（見 issuer.go attempt()/fail()
// 已另外用獨立短逾時 context 降低機率，但仍可能發生），行就會永遠卡在 issuing——BeginIssuing 的
// CAS 只認 pending/failed，DuePending 也只選 pending/failed，兩者都撿不回一筆 issuing 的列。
// 這裡把逾時的 issuing 列轉回 failed 並讓 next_attempt_at 立即到期，交回正常的 DuePending／
// attempt() 流程；attempts 在 BeginIssuing 時已經加過，重試時 attempts>1 會先觸發
// RecoverByRelateNumber，避免中斷前其實已經呼叫 ECPay 成功時被重複開立。回傳被回收的 order_id
// 清單供呼叫端（Issuer.SweepPending）告警。
func (r *Repository) ReapStuckIssuing(ctx context.Context, staleAfter time.Duration) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		UPDATE order_invoices SET
			invoice_status = 'failed',
			last_error = 'stuck in issuing status (process interrupted); reclaimed by sweep',
			next_attempt_at = NOW(), updated_at = NOW()
		WHERE invoice_status = 'issuing' AND updated_at < NOW() - make_interval(secs => $1)
		RETURNING order_id`, staleAfter.Seconds())
	if err != nil {
		return nil, fmt.Errorf("einvoice: reap stuck issuing: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// softSkipReasons 可被 sweep 自動重新評估的略過原因（設定/環境性，非這筆訂單本身不該開）。
// 對照 issuer.go decideSkip：zero_amount／virtual_user／test_money／order_status_changed 為硬略過，不在此列。
var softSkipReasons = []string{"auto_issue_off", "before_issue_since", "stage_env_real_money"}

// DuePending 撈出需要被排程 sweep 撿回重試的訂單 id（pending/failed 且未達重試上限、且
// next_attempt_at 已到期或從未設過）。供 Issuer.SweepPending 使用，本身不做任何狀態轉移。
func (r *Repository) DuePending(ctx context.Context, maxAttempts int) ([]string, error) {
	// 軟略過（softSkipReasons）的列也一併撿回重新評估：這些原因是「設定/環境當下不允許」而非「這筆
	// 永遠不該開」，設定改了（自動開立打開、起算日調早、正式憑證設齊）就該自動補開，不必人工逐筆按。
	// 仍不符合的會被 decide() 再標回 skipped（只是 updated_at 每小時動一次，成本可忽略）。
	rows, err := r.db.Query(ctx, `
		SELECT order_id FROM order_invoices
		WHERE (
		        (invoice_status IN ('pending','failed') AND attempts < $1
		         AND (next_attempt_at IS NULL OR next_attempt_at <= NOW()))
		     OR (invoice_status = 'skipped' AND skip_reason = ANY($2))
		      )
		ORDER BY updated_at ASC LIMIT 200`, maxAttempts, softSkipReasons)
	if err != nil {
		return nil, fmt.Errorf("einvoice: due pending query: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetInvoiceDetail 供後台單筆查詢（發票本體 + 折讓列表）。查無 order_invoices 列回傳 (nil, nil)
// （代表這筆訂單從未被本套件處理過，例如尚未付款）。
func (r *Repository) GetInvoiceDetail(ctx context.Context, orderID string) (*InvoiceDetail, error) {
	var d InvoiceDetail
	d.OrderID = orderID
	err := r.db.QueryRow(ctx, `
		SELECT id, buyer_type, tax_id, title, carrier_type, carrier_id, love_code,
		       invoice_status, invoice_number, invoice_date, invoice_datetime, random_number, relate_number,
		       ecpay_env, attempts, last_error, skip_reason, voided_at, void_reason,
		       remain_allowance_ntd, sales_amount_ntd
		FROM order_invoices WHERE order_id = $1`, orderID).Scan(
		&d.OrderInvoiceID, &d.BuyerType, &d.TaxID, &d.Title, &d.CarrierType, &d.CarrierID, &d.LoveCode,
		&d.Status, &d.InvoiceNumber, &d.InvoiceDate, &d.InvoiceDateTime, &d.RandomNumber, &d.RelateNumber,
		&d.EcpayEnv, &d.Attempts, &d.LastError, &d.SkipReason, &d.VoidedAt, &d.VoidReason,
		&d.RemainAllowanceNTD, &d.SalesAmountNTD)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("einvoice: get invoice detail: %w", err)
	}

	rows, err := r.db.Query(ctx, `
		SELECT id, refund_id, amount_ntd, COALESCE(reason,''), status,
		       allowance_no, allowance_date, last_error, created_at
		FROM order_invoice_allowances WHERE order_invoice_id = $1 ORDER BY created_at ASC`, d.OrderInvoiceID)
	if err != nil {
		return nil, fmt.Errorf("einvoice: list allowances: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a AllowanceRow
		if err := rows.Scan(&a.ID, &a.RefundID, &a.AmountNTD, &a.Reason, &a.Status,
			&a.AllowanceNo, &a.AllowanceDate, &a.LastError, &a.CreatedAt); err != nil {
			return nil, err
		}
		d.Allowances = append(d.Allowances, a)
	}
	return &d, rows.Err()
}

// ListInvoices GET /admin/invoices：status 為空字串代表不篩選。
func (r *Repository) ListInvoices(ctx context.Context, status string, limit int) ([]AdminInvoiceRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.Query(ctx, `
		SELECT o.id, COALESCE(u.name, u.handle), o.total_cents / 100,
		       inv.invoice_status, inv.invoice_number, inv.invoice_date, inv.last_error, inv.skip_reason,
		       inv.updated_at, inv.buyer_type
		FROM order_invoices inv
		JOIN orders o ON o.id = inv.order_id
		JOIN users u ON u.id = o.user_id
		WHERE ($1 = '' OR inv.invoice_status = $1)
		ORDER BY inv.updated_at DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("einvoice: list invoices: %w", err)
	}
	defer rows.Close()
	out := []AdminInvoiceRow{}
	for rows.Next() {
		var row AdminInvoiceRow
		if err := rows.Scan(&row.OrderID, &row.UserDisplayName, &row.AmountNTD,
			&row.Status, &row.InvoiceNumber, &row.InvoiceDate, &row.LastError, &row.SkipReason,
			&row.UpdatedAt, &row.BuyerType); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// InsertAllowancePending 建立一筆待處理折讓。refundID 非 nil 時受 UNIQUE(refund_id) 保護
// （migration 169）：同一筆退款只能有一筆折讓，衝突時（existed=true）直接查回既有列 id，
// 呼叫端應視為冪等成功、不重複打折讓 API（避免同一筆退款被重複觸發時折讓兩次）。
func (r *Repository) InsertAllowancePending(ctx context.Context, orderInvoiceID string, refundID *string, amountNTD int, reason string) (allowanceID string, existed bool, err error) {
	err = r.db.QueryRow(ctx, `
		INSERT INTO order_invoice_allowances (order_invoice_id, refund_id, amount_ntd, reason, status)
		VALUES ($1, $2, $3, $4, 'pending')
		ON CONFLICT (refund_id) WHERE refund_id IS NOT NULL DO NOTHING
		RETURNING id`, orderInvoiceID, refundID, amountNTD, reason).Scan(&allowanceID)
	if errors.Is(err, pgx.ErrNoRows) {
		if refundID == nil {
			// 理論上不會發生：refund_id 為 NULL 的 INSERT 不受該部分唯一索引約束，不可能衝突
			// 導致 0 列——若真的發生代表 DB 端有其他非預期約束，明確報錯而非靜默吞掉。
			return "", false, fmt.Errorf("einvoice: insert allowance returned no rows with nil refund_id (unexpected)")
		}
		if qErr := r.db.QueryRow(ctx,
			`SELECT id FROM order_invoice_allowances WHERE refund_id = $1`, *refundID,
		).Scan(&allowanceID); qErr != nil {
			return "", false, fmt.Errorf("einvoice: load existing allowance: %w", qErr)
		}
		return allowanceID, true, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("einvoice: insert allowance: %w", err)
	}
	return allowanceID, false, nil
}

// MarkAllowanceSuccess 折讓 API 呼叫成功。
func (r *Repository) MarkAllowanceSuccess(ctx context.Context, allowanceID, allowanceNo string, allowanceDate time.Time, raw []byte) error {
	if _, err := r.db.Exec(ctx, `
		UPDATE order_invoice_allowances SET
			status = 'success', allowance_no = $2, allowance_date = $3, raw = $4, last_error = '', updated_at = NOW()
		WHERE id = $1`, allowanceID, allowanceNo, allowanceDate, raw,
	); err != nil {
		return fmt.Errorf("einvoice: mark allowance success: %w", err)
	}
	return nil
}

// MarkAllowanceFailed 折讓 API 呼叫失敗（不自動重試——見 issuer.go Allowance 註解，失敗由後台人工
// 判斷是否重試）。
func (r *Repository) MarkAllowanceFailed(ctx context.Context, allowanceID, lastError string) error {
	if _, err := r.db.Exec(ctx, `
		UPDATE order_invoice_allowances SET status = 'failed', last_error = $2, updated_at = NOW()
		WHERE id = $1`, allowanceID, truncateRunes(lastError, 2000),
	); err != nil {
		return fmt.Errorf("einvoice: mark allowance failed: %w", err)
	}
	return nil
}

// DailyCounts 供每日營運報告使用：[dayStart, dayEnd) 區間內（UTC 時刻，呼叫端自行換算台北曆日的
// 邊界）開立成功/失敗數，以及目前仍待處理（pending/failed 且未達重試上限）的總數。
func (r *Repository) DailyCounts(ctx context.Context, dayStart, dayEnd time.Time, maxAttempts int) (issued, failed, pending int, err error) {
	err = r.db.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE invoice_status = 'issued' AND updated_at >= $1 AND updated_at < $2),
			COUNT(*) FILTER (WHERE invoice_status = 'failed' AND updated_at >= $1 AND updated_at < $2),
			(SELECT COUNT(*) FROM order_invoices WHERE invoice_status IN ('pending','failed') AND attempts < $3)
		FROM order_invoices`, dayStart, dayEnd, maxAttempts).Scan(&issued, &failed, &pending)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("einvoice: daily counts: %w", err)
	}
	return issued, failed, pending, nil
}

// AnyInvoiceRows 是否曾經有過任何 order_invoices 列進入過發票流程以外的終態（用於每日報告：
// auto-issue 關閉但過去曾手動開立過時仍要顯示這行摘要）。這裡用「存在任一非 pending 列」判斷，
// pending 是懶建立時的初始值，訂單一旦付款就會有大量 pending 列，不能拿來當「曾經處理過」的依據。
func (r *Repository) AnyInvoiceRows(ctx context.Context) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM order_invoices WHERE invoice_status <> 'pending')`).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("einvoice: any invoice rows: %w", err)
	}
	return exists, nil
}

// taipeiZone 固定 UTC+8（不用 time.LoadLocation("Asia/Taipei")：distroless 執行環境無 tzdata，
// 理由同 internal/notify/alert.go taiwanTimestamp()、internal/ops/selfcheck.go taiwanNow() 等既有慣例）。
var taipeiZone = time.FixedZone("Asia/Taipei", 8*3600)

// parseECPayDateTime 解析綠界 Issue 回應的 InvoiceDate（文件格式 'yyyy-MM-dd HH:mm:ss'，官方文件
// 亦提及可能帶 '/' 分隔）。date 只取日期部分（供 invoice_date DATE 欄位），datetime 為完整時刻
// （供 invoice_datetime TIMESTAMPTZ 欄位）；解析失敗兩者皆回 nil——這兩欄只是輔助顯示/後續
// Invalid/Allowance 組 InvoiceDate 用，解析失敗不應該讓整個開立流程失敗（發票已經開出去了）。
func parseECPayDateTime(s string) (date *time.Time, datetime *time.Time) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006/01/02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, taipeiZone); err == nil {
			d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
			dt := t
			return &d, &dt
		}
	}
	if len(s) >= 10 {
		if t, err := time.Parse("2006-01-02", s[:10]); err == nil {
			return &t, nil
		}
	}
	return nil, nil
}

// formatECPayDateOnly 把 invoice_date（DATE 欄位）格式化成 Invalid/Allowance 請求要求的
// 'yyyy-MM-dd'；nil 回空字串（呼叫端應在呼叫前確認發票已開立、必有此值）。
func formatECPayDateOnly(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}
