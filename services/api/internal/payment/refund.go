// 綠界信用卡退款流程。與 payment.go 同家族（payment.ecpay.com.tw／payment-stage.ecpay.com.tw），
// 端點與規則整理自官方文件 developers.ecpay.com.tw/2885（信用卡請退款功能）：
//   - Action=R（退刷）：適用【已關帳】訂單（本專案每日 23:59 自動關帳，隔日以後申請退款絕大多數落在此狀態）。
//   - 測試環境官方文件明載「無法提供實際授權，故無法使用此 API」——stage 端點僅供打通請求格式，不保證真能退成功，
//     上線前務必用小額真實交易在正式環境驗證一次。
package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/dor/api/internal/auth"
)

const (
	stageRefundURL = "https://payment-stage.ecpay.com.tw/CreditDetail/DoAction"
	prodRefundURL  = "https://payment.ecpay.com.tw/CreditDetail/DoAction"
)

// RefundActionURL 信用卡退刷／取消授權 API 端點。
func (c *Config) RefundActionURL() string {
	if c.Env == "prod" {
		return prodRefundURL
	}
	return stageRefundURL
}

// BuildRefundRequest 產生信用卡退刷（Action=R）請求參數（含 CheckMacValue）。
// merchantTradeNo/ecpayTradeNo 為原授權交易的商店訂單編號／綠界交易編號（兩者皆必填）；amountCents 為要退的金額（分）。
func (c *Config) BuildRefundRequest(merchantTradeNo, ecpayTradeNo string, amountCents int) map[string]string {
	params := map[string]string{
		"MerchantID":      c.MerchantID,
		"MerchantTradeNo": merchantTradeNo,
		"TradeNo":         ecpayTradeNo,
		"Action":          "R",
		"TotalAmount":     fmt.Sprintf("%d", amountCents/100),
		"EncryptType":     "1",
	}
	params["CheckMacValue"] = c.CheckMacValue(params)
	return params
}

// DoActionResult CreditDetail/DoAction 回應（querystring 格式，非 JSON）
type DoActionResult struct {
	RtnCode string
	RtnMsg  string
	Raw     map[string]string
}

// doRefundRequest 呼叫綠界 CreditDetail/DoAction。不記錄、不回傳任何金鑰。
func doRefundRequest(actionURL string, params map[string]string) (*DoActionResult, error) {
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.PostForm(actionURL, form)
	if err != nil {
		return nil, fmt.Errorf("ecpay refund request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read ecpay refund response: %w", err)
	}
	vals, err := url.ParseQuery(strings.TrimSpace(string(body)))
	if err != nil {
		return nil, fmt.Errorf("parse ecpay refund response: %w", err)
	}
	out := &DoActionResult{Raw: map[string]string{}}
	for k := range vals {
		out.Raw[k] = vals.Get(k)
	}
	out.RtnCode = vals.Get("RtnCode")
	out.RtnMsg = vals.Get("RtnMsg")
	if len(out.Raw) == 0 {
		// 非預期格式（例如 HTML 錯誤頁）：保留原始內容前段供稽核，不視為成功。
		out.Raw["_body"] = truncate(string(body), 500)
	}
	return out, nil
}

// --- Repository：退款相關 ---

var (
	// ErrRefundInProgress 同一筆交易已有退款處理中（unique index 擋下重複觸發）
	ErrRefundInProgress = errors.New("refund already in progress for this transaction")
)

// RefundableOrder 退款前查到的訂單狀態
type RefundableOrder struct {
	TotalCents int
	Status     string
}

// GetOrderForRefund 讀訂單目前狀態與金額（退款用）
func (r *Repository) GetOrderForRefund(ctx context.Context, orderID string) (*RefundableOrder, error) {
	o := &RefundableOrder{}
	err := r.db.QueryRow(ctx, `SELECT total_cents, status FROM orders WHERE id=$1`, orderID).
		Scan(&o.TotalCents, &o.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return o, nil
}

// PaidTx 該訂單目前已付款的那筆交易（退款要用的憑證來源：MerchantTradeNo/TradeNo/特店環境/付款方式）
type PaidTx struct {
	ID              string
	MerchantTradeNo string
	EcpayTradeNo    string
	EcpayEnv        string
	PaymentType     string
	AmountCents     int
	PaidAt          *time.Time
}

// GetPaidTxForOrder 取該訂單最近一筆已付款交易
func (r *Repository) GetPaidTxForOrder(ctx context.Context, orderID string) (*PaidTx, error) {
	t := &PaidTx{}
	err := r.db.QueryRow(ctx, `
		SELECT id::text, merchant_trade_no, COALESCE(ecpay_trade_no,''), ecpay_env, COALESCE(payment_type,''), amount_cents, paid_at
		FROM payment_transactions
		WHERE order_id=$1 AND status='paid'
		ORDER BY paid_at DESC NULLS LAST LIMIT 1`, orderID).
		Scan(&t.ID, &t.MerchantTradeNo, &t.EcpayTradeNo, &t.EcpayEnv, &t.PaymentType, &t.AmountCents, &t.PaidAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// SumRefundedForOrder 該訂單目前累計「已完成」退款金額（success + manual_done，不含 pending/manual_required/failed）
func (r *Repository) SumRefundedForOrder(ctx context.Context, orderID string) (int, error) {
	var sum int
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents),0) FROM payment_refunds
		WHERE order_id=$1 AND status IN ('success','manual_done')`, orderID).Scan(&sum)
	return sum, err
}

// SumReservedForOrder 該訂單目前「已佔用可退餘額」的退款金額：已完成(success/manual_done) +
// 尚未結案但已建立的退款(pending/manual_required/unknown)。用於計算「剩餘可退餘額」時，避免同一筆訂單在
// 前一筆退款尚未真正結案前又被重複建立一筆全額退款（人工退款 manual 路徑在 CreatePendingRefund 之後
// 會立刻轉成 manual_required，若只統計 success/manual_done，manual_required 期間的額度形同沒被佔用，
// 可被重複申請，導致實際匯出金額超過訂單總額）。刻意不含 failed——已確認失敗的嘗試不佔用額度，可重試。
func (r *Repository) SumReservedForOrder(ctx context.Context, orderID string) (int, error) {
	var sum int
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents),0) FROM payment_refunds
		WHERE order_id=$1 AND status IN ('pending','manual_required','unknown','success','manual_done')`, orderID).Scan(&sum)
	return sum, err
}

// RefundRow 退款紀錄
type RefundRow struct {
	ID              string    `json:"id"`
	TransactionID   string    `json:"transaction_id"`
	OrderID         string    `json:"order_id"`
	AmountCents     int       `json:"amount_cents"`
	Status          string    `json:"status"` // pending|success|failed|manual_required|manual_done
	Method          string    `json:"method"` // api|manual
	Reason          string    `json:"reason,omitempty"`
	OperatorAdminID string    `json:"operator_admin_id,omitempty"`
	EcpayRtnCode    string    `json:"ecpay_rtn_code,omitempty"`
	EcpayRtnMsg     string    `json:"ecpay_rtn_msg,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CreatePendingRefund 建立一筆待處理退款紀錄。同一筆交易若已有 pending 紀錄會被 unique index 擋下
// （回 ErrRefundInProgress），這是防止雙擊/併發重複觸發退款 API 的冪等防線。
func (r *Repository) CreatePendingRefund(ctx context.Context, txID, orderID string, amountCents int, method, reason, operatorAdminID string) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `
		INSERT INTO payment_refunds (transaction_id, order_id, amount_cents, status, method, reason, operator_admin_id)
		VALUES ($1, $2, $3, 'pending', $4, NULLIF($5,''), NULLIF($6,'')::uuid)
		RETURNING id::text`, txID, orderID, amountCents, method, reason, operatorAdminID).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return "", ErrRefundInProgress
		}
		return "", err
	}
	return id, nil
}

// MarkRefundResult 更新退款紀錄的處理結果
func (r *Repository) MarkRefundResult(ctx context.Context, refundID, status, rtnCode, rtnMsg string, raw []byte) error {
	_, err := r.db.Exec(ctx, `
		UPDATE payment_refunds
		SET status=$2, ecpay_rtn_code=NULLIF($3,''), ecpay_rtn_msg=NULLIF($4,''), raw=$5, updated_at=NOW()
		WHERE id=$1`, refundID, status, rtnCode, rtnMsg, raw)
	return err
}

// GetRefund 單筆退款紀錄
func (r *Repository) GetRefund(ctx context.Context, refundID string) (*RefundRow, error) {
	row := &RefundRow{}
	err := r.db.QueryRow(ctx, `
		SELECT id::text, transaction_id::text, order_id::text, amount_cents, status, method,
		       COALESCE(reason,''), COALESCE(operator_admin_id::text,''), COALESCE(ecpay_rtn_code,''), COALESCE(ecpay_rtn_msg,''),
		       created_at, updated_at
		FROM payment_refunds WHERE id=$1`, refundID).
		Scan(&row.ID, &row.TransactionID, &row.OrderID, &row.AmountCents, &row.Status, &row.Method,
			&row.Reason, &row.OperatorAdminID, &row.EcpayRtnCode, &row.EcpayRtnMsg, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

// ListRefundsByOrder 某訂單的退款紀錄（後台顯示用，新到舊）
func (r *Repository) ListRefundsByOrder(ctx context.Context, orderID string) ([]RefundRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id::text, transaction_id::text, order_id::text, amount_cents, status, method,
		       COALESCE(reason,''), COALESCE(operator_admin_id::text,''), COALESCE(ecpay_rtn_code,''), COALESCE(ecpay_rtn_msg,''),
		       created_at, updated_at
		FROM payment_refunds WHERE order_id=$1 ORDER BY created_at DESC`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RefundRow{}
	for rows.Next() {
		var row RefundRow
		if err := rows.Scan(&row.ID, &row.TransactionID, &row.OrderID, &row.AmountCents, &row.Status, &row.Method,
			&row.Reason, &row.OperatorAdminID, &row.EcpayRtnCode, &row.EcpayRtnMsg, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// MarkTxRefunded 標記交易已退款（僅在目前是 paid 時生效）
func (r *Repository) MarkTxRefunded(ctx context.Context, txID string) error {
	_, err := r.db.Exec(ctx, `UPDATE payment_transactions SET status='refunded' WHERE id=$1 AND status='paid'`, txID)
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// --- Handler：退款相關 ---

const (
	refundMinAgeAfterPaid = 24 * time.Hour       // 授權後建議隔日才退刷（避免尚未關帳）
	refundSelfServiceDays = 90 * 24 * time.Hour  // 90 天內可自行 API/後台操作退刷
	refundHardCutoffDays  = 360 * 24 * time.Hour // 超過 360 天無法退刷
)

// inEcpayDailyCloseBlackout 每日自動關帳時段（20:15–20:30，台北時間）不可呼叫退刷 API。
func inEcpayDailyCloseBlackout(now time.Time) bool {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	t := now.In(loc)
	mins := t.Hour()*60 + t.Minute()
	return mins >= 20*60+15 && mins <= 20*60+30
}

// AdminRouter 後台退款路由（掛載在 /api/v1/admin/payments，沿用既有 orders 權限，見 main.go）。
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/env-check", h.AdminEnvCheck)
	r.Post("/refunds", h.AdminCreateRefund)
	r.Get("/refunds", h.AdminListRefunds)
	r.Patch("/refunds/{refundID}/manual-done", h.AdminMarkRefundManualDone)
	return r
}

// EnvCheckLegacyHostHeaders 舊版 host-based 解析看到的原始請求 header，僅供除錯／確認反向代理
// （Railway／Next.js rewrites）有沒有把 X-Forwarded-Host 帶到 API 參考用。這兩個值已【不再】用於
// 決定要用哪組特店——前台是 Next.js 伺服器端代理，瀏覽器打 www.dor.tw 之後，Next.js 伺服器再對 API
// 發一個新請求，這兩個 header 在代理這一跳都會被換成 API 自己的 Railway 網域，反映不出瀏覽器實際來源
// （已用本端點證實）。現改用 ResolveByOrigin，見 EnvCheckResponse.ReceivedOrigin/ResolveOK。
type EnvCheckLegacyHostHeaders struct {
	Host           string `json:"host"`             // r.Host 原值（不用於解析特店）
	XForwardedHost string `json:"x_forwarded_host"` // X-Forwarded-Host header 原值（不用於解析特店，可能為空）
}

// EnvCheckCredentialsConfigured 正式特店三寶是否已設定。刻意只回布林，絕不回傳值本身
// （即便是遮罩片段），避免診斷端點變成金鑰外洩管道。
type EnvCheckCredentialsConfigured struct {
	MerchantID bool `json:"merchant_id"`
	HashKey    bool `json:"hash_key"`
	HashIV     bool `json:"hash_iv"`
}

// EnvCheckResponse GET /api/v1/admin/payments/env-check 回應。
type EnvCheckResponse struct {
	GlobalEcpayEnv            string                        `json:"global_ecpay_env"`
	ProdOrigins               []string                      `json:"prod_origins"`        // 設定值：ECPAY_PROD_ORIGINS
	ReceivedOrigin            string                        `json:"received_origin"`     // 呼叫時帶的 ?origin= 查詢參數原值
	ResolveOK                 bool                          `json:"resolve_ok"`          // ResolveByOrigin 的 ok；false＝這個 origin 結帳會被 Checkout 擋下（fail closed）
	LegacyHostHeaders         EnvCheckLegacyHostHeaders     `json:"legacy_host_headers"` // 除錯參考，不用於決定特店（見上方型別註解）
	ResolvedEnv               string                        `json:"resolved_env"`        // ResolveOK=false 時為空字串
	ResolvedMerchantID        string                        `json:"resolved_merchant_id"`
	ResolvedActionURL         string                        `json:"resolved_action_url"`
	WouldChargeRealMoney      bool                          `json:"would_charge_real_money"`
	ProdCredentialsConfigured EnvCheckCredentialsConfigured `json:"prod_credentials_configured"`
}

// AdminEnvCheck GET /api/v1/admin/payments/env-check?origin=https://www.dor.tw
// 讓管理員在真的刷卡之前，確認「從這個 origin 結帳，Checkout 會解析成哪一組特店、會不會被擋下」。
// 刻意呼叫與 Checkout（見 payment.go Handler.Checkout）完全相同的 h.multi.ResolveByOrigin(origin)，
// 不自行複製一份判斷——避免診斷結果與實際結帳行為漂移，讓這個端點失去意義。
//
// 安全：絕不在回應中輸出 HashKey/HashIV 的值（連遮罩片段也不要），正式三寶是否已設定只回布林。
// MerchantID 本身會出現在送綠界的結帳表單中、非機密，可直接顯示。
func (h *Handler) AdminEnvCheck(w http.ResponseWriter, r *http.Request) {
	origin := r.URL.Query().Get("origin")
	env, cfg, ok := h.multi.ResolveByOrigin(origin)

	resp := EnvCheckResponse{
		GlobalEcpayEnv: h.multi.GlobalEnv,
		ProdOrigins:    h.multi.ProdOrigins,
		ReceivedOrigin: origin,
		ResolveOK:      ok,
		LegacyHostHeaders: EnvCheckLegacyHostHeaders{
			Host:           r.Host,
			XForwardedHost: r.Header.Get("X-Forwarded-Host"),
		},
	}
	if ok {
		resp.ResolvedEnv = env
		resp.WouldChargeRealMoney = env == "prod"
		if cfg != nil {
			resp.ResolvedMerchantID = cfg.MerchantID
			resp.ResolvedActionURL = cfg.ActionURL()
		}
	}
	if h.multi.Prod != nil {
		resp.ProdCredentialsConfigured = EnvCheckCredentialsConfigured{
			MerchantID: h.multi.Prod.MerchantID != "",
			HashKey:    h.multi.Prod.HashKey != "",
			HashIV:     h.multi.Prod.HashIV != "",
		}
	}

	respondJSON(w, http.StatusOK, resp)
}

// AdminCreateRefund POST /api/v1/admin/payments/refunds  {order_id, amount_cents?, reason}
// amount_cents 省略或 0 = 對剩餘可退餘額全額退款。信用卡走綠界退刷 API；其餘付款方式（ATM/超商等）
// 建立 manual_required 紀錄，待後台人工匯款後呼叫 manual-done 完成。
func (h *Handler) AdminCreateRefund(w http.ResponseWriter, r *http.Request) {
	adminID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var req struct {
		OrderID     string `json:"order_id"`
		AmountCents int    `json:"amount_cents"`
		Reason      string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrderID == "" {
		respondErr(w, http.StatusBadRequest, "order_id is required")
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		respondErr(w, http.StatusBadRequest, "退款原因為必填")
		return
	}

	order, err := h.repo.GetOrderForRefund(r.Context(), req.OrderID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load order")
		return
	}
	if order == nil {
		respondErr(w, http.StatusNotFound, "order not found")
		return
	}
	if order.Status != "paid" {
		respondErr(w, http.StatusConflict, "訂單目前非「已付款」狀態，無法退款")
		return
	}

	tx, err := h.repo.GetPaidTxForOrder(r.Context(), req.OrderID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load payment transaction")
		return
	}
	if tx == nil {
		respondErr(w, http.StatusConflict, "查無已付款的金流交易紀錄，無法退款")
		return
	}

	already, err := h.repo.SumReservedForOrder(r.Context(), req.OrderID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load refund history")
		return
	}
	remaining := order.TotalCents - already
	if remaining <= 0 {
		respondErr(w, http.StatusConflict, "此訂單已全額退款")
		return
	}
	amountCents := req.AmountCents
	if amountCents <= 0 {
		amountCents = remaining
	}
	if amountCents > remaining {
		respondErr(w, http.StatusBadRequest, fmt.Sprintf("退款金額超過可退餘額（剩餘可退 NT$ %d）", remaining/100))
		return
	}

	// 依付款方式分流：僅信用卡支援 API 退款，其餘（ATM/超商代碼/條碼等）一律人工退款。
	isCredit := strings.HasPrefix(strings.ToLower(tx.PaymentType), "credit")
	method := "manual"
	manualNote := ""
	switch {
	case !isCredit:
		manualNote = "此付款方式（" + tx.PaymentType + "）不支援 API 退款，需人工處理"
	case tx.EcpayTradeNo == "":
		manualNote = "查無綠界交易編號（TradeNo），無法呼叫退刷 API，請改人工處理"
	case tx.PaidAt == nil || time.Since(*tx.PaidAt) > refundHardCutoffDays:
		manualNote = "已逾 360 天，綠界 API 與後台皆無法退刷，請洽客服"
	case time.Since(*tx.PaidAt) > refundSelfServiceDays:
		manualNote = "已逾 90 天，須至綠界廠商後台提出人工退刷申請"
	case time.Since(*tx.PaidAt) < refundMinAgeAfterPaid:
		manualNote = "訂單付款未滿 24 小時（可能尚未關帳），請隔日 06:00 後再試，或改人工處理"
	case inEcpayDailyCloseBlackout(time.Now()):
		respondErr(w, http.StatusConflict, "綠界每日 20:15–20:30 自動關帳時段無法呼叫退刷 API，請稍後再試")
		return
	default:
		method = "api"
	}
	reason := req.Reason
	if method == "manual" && manualNote != "" {
		reason = reason + "（系統判斷：" + manualNote + "）"
	}

	refundID, err := h.repo.CreatePendingRefund(r.Context(), tx.ID, req.OrderID, amountCents, method, reason, adminID)
	if errors.Is(err, ErrRefundInProgress) {
		respondErr(w, http.StatusConflict, "此交易已有退款處理中，請稍候")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to create refund record")
		return
	}

	if method == "manual" {
		if err := h.repo.MarkRefundResult(r.Context(), refundID, "manual_required", "", "", nil); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to update refund record")
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"refund_id": refundID, "status": "manual_required", "method": "manual", "note": manualNote,
		})
		return
	}

	// method == "api"：呼叫綠界信用卡退刷（Action=R）
	cfg := h.multi.ByEnv(tx.EcpayEnv)
	params := cfg.BuildRefundRequest(tx.MerchantTradeNo, tx.EcpayTradeNo, amountCents)
	result, callErr := doRefundRequest(cfg.RefundActionURL(), params)
	if callErr != nil {
		// 網路逾時/連線失敗：無法確認綠界是否已受理退刷（可能已經退了，只是回應沒送達）。
		// 不可標成 'failed'——那會釋放這筆額度讓下一次請求以為還沒退過，導致重複退款、真金重複流出。
		// 標成 'unknown'，讓它繼續佔用可退餘額並擋住後續請求，待人工到綠界後台核對後才手動處理。
		h.repo.MarkRefundResult(r.Context(), refundID, "unknown", "", "呼叫綠界退刷服務逾時或連線失敗，結果不明", nil)
		respondErr(w, http.StatusBadGateway, "退款結果不明（逾時/連線失敗），請務必先至綠界後台核對是否已退款成功，切勿直接重試")
		return
	}
	rawJSON, _ := json.Marshal(result.Raw)

	if result.RtnCode != "1" {
		if result.RtnCode == "" {
			// 綠界回應不是預期的 querystring 格式（例如中間閘道器回傳的 HTML 錯誤頁），同樣無法確認
			// 退刷是否已被受理，比照連線失敗處理，不可視為確定失敗。
			h.repo.MarkRefundResult(r.Context(), refundID, "unknown", result.RtnCode, "退刷回應格式非預期，結果不明", rawJSON)
			respondErr(w, http.StatusBadGateway, "退款結果不明（回應格式異常），請務必先至綠界後台核對是否已退款成功，切勿直接重試")
			return
		}
		h.repo.MarkRefundResult(r.Context(), refundID, "failed", result.RtnCode, result.RtnMsg, rawJSON)
		respondErr(w, http.StatusBadGateway, "退款失敗："+result.RtnMsg)
		return
	}

	if err := h.repo.MarkRefundResult(r.Context(), refundID, "success", result.RtnCode, result.RtnMsg, rawJSON); err != nil {
		respondErr(w, http.StatusInternalServerError, "退款已成功但記錄寫入失敗，請人工核對")
		return
	}
	if err := h.finalizeIfFullyRefunded(r.Context(), req.OrderID, tx.ID, order.TotalCents); err != nil {
		respondErr(w, http.StatusInternalServerError, "退款已成功但訂單狀態更新失敗，請人工核對")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"refund_id": refundID, "status": "success"})
}

// finalizeIfFullyRefunded 累計退款達訂單全額時，才把交易/訂單標為 refunded 並連動取消報名。
// orders.status 沒有「部分退款」狀態，refunded 僅代表已全額退清；未達全額前訂單維持 paid，
// 之後可再次呼叫本端點退剩餘金額（累計不可超過訂單總額，由呼叫端驗證）。
func (h *Handler) finalizeIfFullyRefunded(ctx context.Context, orderID, txID string, totalCents int) error {
	sum, err := h.repo.SumRefundedForOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if sum < totalCents {
		return nil
	}
	if err := h.repo.MarkTxRefunded(ctx, txID); err != nil {
		return err
	}
	return h.marker.MarkOrderRefunded(ctx, orderID)
}

// AdminMarkRefundManualDone PATCH /api/v1/admin/payments/refunds/{refundID}/manual-done
// 後台人員完成人工匯款後呼叫；若累計退款達全額，連動把交易/訂單標為 refunded。
func (h *Handler) AdminMarkRefundManualDone(w http.ResponseWriter, r *http.Request) {
	refundID := chi.URLParam(r, "refundID")
	refund, err := h.repo.GetRefund(r.Context(), refundID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load refund")
		return
	}
	if refund == nil {
		respondErr(w, http.StatusNotFound, "refund not found")
		return
	}
	if refund.Status != "manual_required" {
		respondErr(w, http.StatusConflict, "此退款紀錄非「待人工處理」狀態")
		return
	}

	order, err := h.repo.GetOrderForRefund(r.Context(), refund.OrderID)
	if err != nil || order == nil {
		respondErr(w, http.StatusInternalServerError, "failed to load order")
		return
	}
	// 最後一道防線：正式結案前重新核對「已完成退款總額 + 本筆金額」不可超過訂單總額。
	// 就算前面的冪等防線（unique index / SumReservedForOrder）都被繞過，這裡也要擋下超額結案。
	already, err := h.repo.SumRefundedForOrder(r.Context(), refund.OrderID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load refund history")
		return
	}
	if already+refund.AmountCents > order.TotalCents {
		respondErr(w, http.StatusConflict, "此筆退款金額加計已完成退款將超過訂單總額，請人工核對後再處理")
		return
	}
	if err := h.repo.MarkRefundResult(r.Context(), refundID, "manual_done", "", "", nil); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to update refund")
		return
	}
	if err := h.finalizeIfFullyRefunded(r.Context(), refund.OrderID, refund.TransactionID, order.TotalCents); err != nil {
		respondErr(w, http.StatusInternalServerError, "退款已標記完成但訂單狀態更新失敗，請人工核對")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AdminListRefunds GET /api/v1/admin/payments/refunds?order_id=
func (h *Handler) AdminListRefunds(w http.ResponseWriter, r *http.Request) {
	orderID := r.URL.Query().Get("order_id")
	if orderID == "" {
		respondErr(w, http.StatusBadRequest, "order_id is required")
		return
	}
	rows, err := h.repo.ListRefundsByOrder(r.Context(), orderID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list refunds")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"refunds": rows, "count": len(rows)})
}
