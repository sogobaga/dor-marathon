// handler.go：電子發票後台 HTTP 端點（Wire 階段）。掛載方式見 cmd/api/main.go：
//   - OrderInvoiceRouter() 掛在 /admin/orders/{orderID}/invoice（與 race.Handler.OrderRouter()
//     並列掛在同一個 /admin/orders 底下，orderID 由外層 chi.Route 的 mount pattern 捕捉）。
//   - ListInvoices 掛在 /admin/invoices。
//
// 權限（perm("orders")）與 admin/audit middleware 皆在 main.go 那層把關，這裡不重複檢查。
// 回應 JSON 欄位對應前端既有型別（apps/web/src/lib/api.ts 的 EInvoiceDetail／EInvoiceAllowance／
// AdminInvoiceRow——Wire 階段前端已就緒的既有契約，欄位名稱不可隨意更動）。
package einvoice

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]any{"error": msg})
}

// AdminHandler 電子發票後台端點。
type AdminHandler struct {
	issuer *Issuer
	repo   *Repository
}

// NewAdminHandler 建構子。issuer/repo 由 main.go 建構的同一組（einvoice.NewIssuer 內部持有的
// Repository 與這裡傳入的應是同一個 *pgxpool.Pool 建出來的獨立實例——兩者都是無狀態的薄資料層，
// 共用同一個 DB 連線池即可，不需要是同一個物件）。
func NewAdminHandler(issuer *Issuer, repo *Repository) *AdminHandler {
	return &AdminHandler{issuer: issuer, repo: repo}
}

// OrderInvoiceRouter 掛載於 /admin/orders/{orderID}/invoice。
func (h *AdminHandler) OrderInvoiceRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.Get)
	r.Post("/issue", h.Issue)
	r.Post("/void", h.Void)
	r.Post("/sync", h.Sync)
	r.Post("/allowance", h.CreateAllowance)
	return r
}

// GET /admin/orders/{orderID}/invoice
func (h *AdminHandler) Get(w http.ResponseWriter, r *http.Request) {
	orderID := chi.URLParam(r, "orderID")
	detail, err := h.repo.GetInvoiceDetail(r.Context(), orderID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load invoice")
		return
	}
	if detail == nil {
		// 查無列＝這筆訂單從未進入發票流程（尚未付款的 VIP 訂單懶建立列要等付款成功才會有；
		// 賽事訂單理論上報名當下就有列，這裡統一用 null 表達「沒有可顯示的發票資訊」）。
		respondJSON(w, http.StatusOK, map[string]any{"invoice": nil, "allowances": []allowanceJSON{}})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"invoice":    toInvoiceDetailJSON(detail),
		"allowances": toAllowanceJSONList(detail.Allowances),
	})
}

// Issue POST /admin/orders/{orderID}/invoice/issue（後台手動開立/重試；bypass auto_issue/
// issue_since，但零元/虛擬會員/測試金流三項硬規則一律適用，見 Issuer.decideSkip）。
//
// decideSkip 本身不檢查訂單是否已付款（Core 設計上假設呼叫端——三處付款結算 CAS——只在付款成功
// 時才呼叫），後台手動開立是唯一「不保證訂單已付款」的呼叫來源，這裡額外補上這道檢查（見
// Repository.OrderStatus 註解），避免對還在 pending 的訂單開出一張沒收到錢的發票。
func (h *AdminHandler) Issue(w http.ResponseWriter, r *http.Request) {
	orderID := chi.URLParam(r, "orderID")
	status, err := h.repo.OrderStatus(r.Context(), orderID)
	if errors.Is(err, ErrOrderNotFound) {
		respondErr(w, http.StatusNotFound, "order not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load order")
		return
	}
	if status != "paid" {
		respondErr(w, http.StatusConflict, "此訂單尚未付款，無法開立發票")
		return
	}
	if err := h.issuer.IssueNow(r.Context(), orderID, true); err != nil {
		respondErr(w, http.StatusBadGateway, err.Error())
		return
	}
	h.respondInvoice(w, r, orderID)
}

// Sync POST /admin/orders/{orderID}/invoice/sync（GetIssue 刷新本地狀態/號碼/剩餘可折讓金額）。
//
// 只要求 order_invoices 有列（detail != nil），不要求本地已經存過 relate_number：Issuer.Sync 本身是
// 用 RelateNumberFor(orderID) 純算出來的值去查 GetIssue，不依賴本地是否存過這個欄位，所以一筆卡在
// 'issuing'（relate_number 仍是空字串——見 review finding #1/#2，MarkIssued 是唯一會寫入
// relate_number 的地方）或本地寫入失敗的訂單，也能直接靠這顆按鈕手動查回 ECPay 是否其實已經開立
// 成功，不必乾等 SweepPending 的 stale-issuing 回收（最長 10 分鐘才會撿到）。查無此筆對 ECPay 而言
// 只是回一個「找不到」的業務錯誤，不會有副作用。
func (h *AdminHandler) Sync(w http.ResponseWriter, r *http.Request) {
	orderID := chi.URLParam(r, "orderID")
	detail, err := h.repo.GetInvoiceDetail(r.Context(), orderID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load invoice")
		return
	}
	if detail == nil {
		respondErr(w, http.StatusConflict, "尚未開立過發票，無法同步查詢")
		return
	}
	if err := h.issuer.Sync(r.Context(), orderID); err != nil {
		respondErr(w, http.StatusBadGateway, err.Error())
		return
	}
	h.respondInvoice(w, r, orderID)
}

// Void POST /admin/orders/{orderID}/invoice/void {"reason":"..."}（≤20 字，必填）。
// 捐贈發票不可作廢是 ECPay 自己的業務規則（見官方文件），刻意不在這裡重複擋一次——前端已於 UI
// 層擋下（見 apps/web 的 orders 頁），後端只把 ECPay 拒絕的錯誤原樣傳回（見任務規格「server passes
// ECPay error through」）。這裡唯一補的前置檢查是「必須是已開立狀態」，屬於操作本身的合理性檢查，
// 不是重複業務規則。
func (h *AdminHandler) Void(w http.ResponseWriter, r *http.Request) {
	orderID := chi.URLParam(r, "orderID")
	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		respondErr(w, http.StatusBadRequest, "作廢原因為必填")
		return
	}
	if utf8.RuneCountInString(reason) > 20 {
		respondErr(w, http.StatusBadRequest, "作廢原因請在 20 字以內")
		return
	}
	detail, err := h.repo.GetInvoiceDetail(r.Context(), orderID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load invoice")
		return
	}
	if detail == nil || detail.Status != "issued" {
		respondErr(w, http.StatusConflict, "此發票非「已開立」狀態，無法作廢")
		return
	}
	if err := h.issuer.Void(r.Context(), orderID, reason); err != nil {
		respondErr(w, http.StatusBadGateway, err.Error())
		return
	}
	h.respondInvoice(w, r, orderID)
}

// CreateAllowance POST /admin/orders/{orderID}/invoice/allowance {"amount_ntd":123,"reason":"..."}
// 後台手動折讓；不掛 refund_id（見 Issuer.Allowance／InsertAllowancePending 的冪等保證只鎖
// refund_id 非 nil 的情況，手動折讓本來就不對應特定一筆退款，允許同一發票被手動折讓多次）。
func (h *AdminHandler) CreateAllowance(w http.ResponseWriter, r *http.Request) {
	orderID := chi.URLParam(r, "orderID")
	var req struct {
		AmountNTD int    `json:"amount_ntd"`
		Reason    string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.AmountNTD <= 0 {
		respondErr(w, http.StatusBadRequest, "折讓金額必須為正整數")
		return
	}
	before, err := h.repo.GetInvoiceDetail(r.Context(), orderID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load invoice")
		return
	}
	if before == nil || before.Status != "issued" {
		respondErr(w, http.StatusConflict, "此訂單尚無已開立的發票，無法折讓")
		return
	}
	beforeCount := len(before.Allowances)

	if err := h.issuer.Allowance(r.Context(), orderID, req.AmountNTD, req.Reason, nil); err != nil {
		respondErr(w, http.StatusBadGateway, err.Error())
		return
	}

	after, err := h.repo.GetInvoiceDetail(r.Context(), orderID)
	if err != nil || after == nil || len(after.Allowances) <= beforeCount {
		respondErr(w, http.StatusInternalServerError, "折讓已送出但讀回結果失敗，請至發票明細確認")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"allowance": toAllowanceJSON(after.Allowances[len(after.Allowances)-1]),
	})
}

// respondInvoice Issue/Sync/Void 共用的「操作後讀回最新發票快照」尾段。
func (h *AdminHandler) respondInvoice(w http.ResponseWriter, r *http.Request, orderID string) {
	detail, err := h.repo.GetInvoiceDetail(r.Context(), orderID)
	if err != nil || detail == nil {
		respondErr(w, http.StatusInternalServerError, "操作成功但讀回發票失敗，請重新整理")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "invoice": toInvoiceDetailJSON(detail)})
}

// ListInvoices GET /admin/invoices?status=&limit=
func (h *AdminHandler) ListInvoices(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	rows, err := h.repo.ListInvoices(r.Context(), status, limit)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list invoices")
		return
	}
	out := make([]adminInvoiceRowJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAdminInvoiceRowJSON(row))
	}
	respondJSON(w, http.StatusOK, map[string]any{"invoices": out})
}

// ================= JSON DTO（對應 apps/web/src/lib/api.ts 既有型別，欄位名稱為固定契約） =================

type invoiceDetailJSON struct {
	OrderID            string  `json:"order_id"`
	BuyerType          string  `json:"buyer_type"`
	TaxID              string  `json:"tax_id,omitempty"`
	Title              string  `json:"title,omitempty"`
	CarrierType        string  `json:"carrier_type,omitempty"`
	CarrierID          string  `json:"carrier_id,omitempty"`
	LoveCode           string  `json:"love_code,omitempty"`
	InvoiceStatus      string  `json:"invoice_status"`
	InvoiceNumber      string  `json:"invoice_number,omitempty"`
	InvoiceDate        string  `json:"invoice_date,omitempty"`
	RandomNumber       string  `json:"random_number,omitempty"`
	RelateNumber       string  `json:"relate_number,omitempty"`
	EcpayEnv           string  `json:"ecpay_env,omitempty"`
	SalesAmountNTD     int     `json:"sales_amount_ntd"`
	Attempts           int     `json:"attempts"`
	LastError          string  `json:"last_error,omitempty"`
	SkipReason         string  `json:"skip_reason,omitempty"`
	IssuedAt           *string `json:"issued_at,omitempty"`
	VoidedAt           *string `json:"voided_at,omitempty"`
	VoidReason         string  `json:"void_reason,omitempty"`
	RemainAllowanceNTD *int    `json:"remain_allowance_ntd,omitempty"`
}

func toInvoiceDetailJSON(d *InvoiceDetail) invoiceDetailJSON {
	out := invoiceDetailJSON{
		OrderID: d.OrderID, BuyerType: d.BuyerType, TaxID: d.TaxID, Title: d.Title,
		CarrierType: d.CarrierType, CarrierID: d.CarrierID, LoveCode: d.LoveCode,
		InvoiceStatus: d.Status, InvoiceNumber: d.InvoiceNumber, RandomNumber: d.RandomNumber,
		RelateNumber: d.RelateNumber, EcpayEnv: d.EcpayEnv, SalesAmountNTD: d.SalesAmountNTD,
		Attempts: d.Attempts, LastError: d.LastError, SkipReason: d.SkipReason,
		VoidReason: d.VoidReason, RemainAllowanceNTD: d.RemainAllowanceNTD,
	}
	if d.InvoiceDate != nil {
		out.InvoiceDate = d.InvoiceDate.Format("2006-01-02")
	}
	if d.InvoiceDateTime != nil {
		s := d.InvoiceDateTime.Format(time.RFC3339)
		out.IssuedAt = &s
	}
	if d.VoidedAt != nil {
		s := d.VoidedAt.Format(time.RFC3339)
		out.VoidedAt = &s
	}
	return out
}

type allowanceJSON struct {
	ID            string  `json:"id"`
	RefundID      *string `json:"refund_id,omitempty"`
	AmountNTD     int     `json:"amount_ntd"`
	Reason        string  `json:"reason,omitempty"`
	Status        string  `json:"status"`
	AllowanceNo   string  `json:"allowance_no,omitempty"`
	AllowanceDate *string `json:"allowance_date,omitempty"`
	LastError     string  `json:"last_error,omitempty"`
	CreatedAt     string  `json:"created_at"`
}

func toAllowanceJSON(a AllowanceRow) allowanceJSON {
	out := allowanceJSON{
		ID: a.ID, RefundID: a.RefundID, AmountNTD: a.AmountNTD, Reason: a.Reason,
		Status: a.Status, AllowanceNo: a.AllowanceNo, LastError: a.LastError,
		CreatedAt: a.CreatedAt.Format(time.RFC3339),
	}
	if a.AllowanceDate != nil {
		s := a.AllowanceDate.Format(time.RFC3339)
		out.AllowanceDate = &s
	}
	return out
}

func toAllowanceJSONList(rows []AllowanceRow) []allowanceJSON {
	out := make([]allowanceJSON, 0, len(rows))
	for _, a := range rows {
		out = append(out, toAllowanceJSON(a))
	}
	return out
}

type adminInvoiceRowJSON struct {
	OrderID       string `json:"order_id"`
	UserName      string `json:"user_name"`
	AmountNTD     int    `json:"amount_ntd"`
	InvoiceStatus string `json:"invoice_status"`
	InvoiceNumber string `json:"invoice_number,omitempty"`
	InvoiceDate   string `json:"invoice_date,omitempty"`
	LastError     string `json:"last_error,omitempty"`
	SkipReason    string `json:"skip_reason,omitempty"`
	UpdatedAt     string `json:"updated_at"`
	BuyerType     string `json:"buyer_type"`
}

func toAdminInvoiceRowJSON(row AdminInvoiceRow) adminInvoiceRowJSON {
	out := adminInvoiceRowJSON{
		OrderID: row.OrderID, UserName: row.UserDisplayName, AmountNTD: row.AmountNTD,
		InvoiceStatus: row.Status, InvoiceNumber: row.InvoiceNumber, LastError: row.LastError,
		SkipReason: row.SkipReason, UpdatedAt: row.UpdatedAt.Format(time.RFC3339), BuyerType: row.BuyerType,
	}
	if row.InvoiceDate != nil {
		out.InvoiceDate = row.InvoiceDate.Format("2006-01-02")
	}
	return out
}
