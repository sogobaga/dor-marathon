// Package payment 綠界 ECPay 全方位金流（AIO）串接。
package payment

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/auth"
)

const (
	stageURL = "https://payment-stage.ecpay.com.tw/Cashier/AioCheckOut/V5"
	prodURL  = "https://payment.ecpay.com.tw/Cashier/AioCheckOut/V5"
)

// Config 綠界設定
type Config struct {
	MerchantID    string
	HashKey       string
	HashIV        string
	Env           string // stage | prod
	ReturnURL     string
	ClientBackURL string
}

// ActionURL 結帳表單要 POST 的綠界端點
func (c *Config) ActionURL() string {
	if c.Env == "prod" {
		return prodURL
	}
	return stageURL
}

// CheckMacValue 依綠界規則計算驗證碼（SHA256 / EncryptType=1）。
func (c *Config) CheckMacValue(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "CheckMacValue" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return strings.ToLower(keys[i]) < strings.ToLower(keys[j])
	})

	var sb strings.Builder
	sb.WriteString("HashKey=")
	sb.WriteString(c.HashKey)
	for _, k := range keys {
		sb.WriteString("&")
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(params[k])
	}
	sb.WriteString("&HashIV=")
	sb.WriteString(c.HashIV)

	encoded := dotNetURLEncode(sb.String())
	encoded = strings.ToLower(encoded)
	sum := sha256.Sum256([]byte(encoded))
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// dotNetURLEncode 對齊 .NET HttpUtility.UrlEncode：space→'+'，且 - _ . ! * ( ) 不編碼。
func dotNetURLEncode(s string) string {
	e := url.QueryEscape(s) // space→'+', !*() → %21 %2A %28 %29，- _ . 不編碼
	r := strings.NewReplacer(
		"%21", "!",
		"%2A", "*",
		"%28", "(",
		"%29", ")",
	)
	return r.Replace(e)
}

// BuildCheckout 產生送綠界的 AIO 參數（含 CheckMacValue）。
// amountNTD 為整數新台幣；itemName/tradeDesc 為顯示文字。
func (c *Config) BuildCheckout(tradeNo string, amountNTD int, itemName, tradeDesc string, now time.Time) map[string]string {
	params := map[string]string{
		"MerchantID":        c.MerchantID,
		"MerchantTradeNo":   tradeNo,
		"MerchantTradeDate": now.Format("2006/01/02 15:04:05"),
		"PaymentType":       "aio",
		"TotalAmount":       fmt.Sprintf("%d", amountNTD),
		"TradeDesc":         tradeDesc,
		"ItemName":          itemName,
		"ReturnURL":         c.ReturnURL,
		"ClientBackURL":     c.ClientBackURL,
		"ChoosePayment":     "ALL",
		"EncryptType":       "1",
	}
	params["CheckMacValue"] = c.CheckMacValue(params)
	return params
}

// VerifyCallback 重算 CheckMacValue 與回傳的比對。
func (c *Config) VerifyCallback(params map[string]string) bool {
	got := params["CheckMacValue"]
	if got == "" {
		return false
	}
	return strings.EqualFold(got, c.CheckMacValue(params))
}

// --- Repository ---

type Repository struct{ db *pgxpool.Pool }

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// PayableOrder 結帳前查到的訂單
type PayableOrder struct {
	TotalCents int
	Status     string
	RaceTitle  string
}

// GetPayableOrder 取得屬於該使用者的訂單（結帳用）
func (r *Repository) GetPayableOrder(ctx context.Context, orderID, userID string) (*PayableOrder, error) {
	o := &PayableOrder{}
	err := r.db.QueryRow(ctx, `
		SELECT o.total_cents, o.status, rc.title
		FROM orders o JOIN races rc ON rc.id = o.race_id
		WHERE o.id=$1 AND o.user_id=$2`, orderID, userID).
		Scan(&o.TotalCents, &o.Status, &o.RaceTitle)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return o, err
}

func (r *Repository) CreateTx(ctx context.Context, orderID, tradeNo string, amountCents int) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO payment_transactions (order_id, merchant_trade_no, amount_cents)
		VALUES ($1, $2, $3)`, orderID, tradeNo, amountCents)
	return err
}

// FindOrderByTradeNo 由 merchant_trade_no 找回 order_id 與目前 tx 狀態
func (r *Repository) FindOrderByTradeNo(ctx context.Context, tradeNo string) (orderID, txStatus string, err error) {
	err = r.db.QueryRow(ctx,
		`SELECT order_id::text, status FROM payment_transactions WHERE merchant_trade_no=$1`,
		tradeNo).Scan(&orderID, &txStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	return orderID, txStatus, err
}

func (r *Repository) MarkTxPaid(ctx context.Context, tradeNo, rtnCode string, raw []byte) error {
	_, err := r.db.Exec(ctx, `
		UPDATE payment_transactions
		SET status='paid', rtn_code=$2, raw=$3, paid_at=NOW()
		WHERE merchant_trade_no=$1`, tradeNo, rtnCode, raw)
	return err
}

// --- Handler ---

// OrderMarker 由 race.Service 實作（標記訂單+報名已付）
type OrderMarker interface {
	MarkOrderPaid(ctx context.Context, orderID, paymentRef string) error
}

type Handler struct {
	cfg    *Config
	repo   *Repository
	marker OrderMarker
}

func NewHandler(cfg *Config, repo *Repository, marker OrderMarker) *Handler {
	return &Handler{cfg: cfg, repo: repo, marker: marker}
}

// Checkout POST /api/v1/payments/ecpay/checkout（需登入）
func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var req struct {
		OrderID string `json:"order_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrderID == "" {
		respondErr(w, http.StatusBadRequest, "order_id is required")
		return
	}

	order, err := h.repo.GetPayableOrder(r.Context(), req.OrderID, userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load order")
		return
	}
	if order == nil {
		respondErr(w, http.StatusNotFound, "order not found")
		return
	}
	if order.Status == "paid" {
		respondErr(w, http.StatusConflict, "訂單已付款")
		return
	}
	if order.TotalCents <= 0 {
		respondErr(w, http.StatusBadRequest, "訂單金額為 0，無需付款")
		return
	}

	tradeNo := genTradeNo()
	if err := h.repo.CreateTx(r.Context(), req.OrderID, tradeNo, order.TotalCents); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to create transaction")
		return
	}

	itemName := truncate("DOR Race Registration", 100)
	params := h.cfg.BuildCheckout(tradeNo, order.TotalCents/100, itemName, "DOR Registration", time.Now())
	respondJSON(w, http.StatusOK, map[string]any{
		"action_url": h.cfg.ActionURL(),
		"params":     params,
	})
}

// Notify POST /api/v1/payments/ecpay/notify（公開，綠界 server 對 server）
func (h *Handler) Notify(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.Write([]byte("0|ParseError"))
		return
	}
	params := map[string]string{}
	for k := range r.PostForm {
		params[k] = r.PostForm.Get(k)
	}

	if !h.cfg.VerifyCallback(params) {
		w.Write([]byte("0|CheckMacValueError"))
		return
	}

	tradeNo := params["MerchantTradeNo"]
	rtnCode := params["RtnCode"]
	if tradeNo == "" {
		w.Write([]byte("0|NoTradeNo"))
		return
	}

	orderID, txStatus, err := h.repo.FindOrderByTradeNo(r.Context(), tradeNo)
	if err != nil || orderID == "" {
		w.Write([]byte("0|TradeNotFound"))
		return
	}

	// RtnCode=1 表示付款成功
	if rtnCode == "1" && txStatus != "paid" {
		raw, _ := json.Marshal(params)
		if err := h.marker.MarkOrderPaid(r.Context(), orderID, "ECPay:"+tradeNo); err != nil {
			w.Write([]byte("0|MarkPaidFailed"))
			return
		}
		h.repo.MarkTxPaid(r.Context(), tradeNo, rtnCode, raw)
	}

	// 一律回 1|OK 讓綠界停止重送（驗章已過）
	w.Write([]byte("1|OK"))
}

// --- helpers ---

const tradeChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func genTradeNo() string {
	b := make([]byte, 6)
	rand.Read(b)
	suffix := make([]byte, 6)
	for i := range b {
		suffix[i] = tradeChars[int(b[i])%len(tradeChars)]
	}
	// DOR + 10 位 unix 秒 + 6 位亂數 = 19 字（≤20）
	return fmt.Sprintf("DOR%d%s", time.Now().Unix(), string(suffix))
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
