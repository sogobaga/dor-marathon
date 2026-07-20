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
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

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
	ClientBackURL string   // 預設付款後返回網址（前端未帶或不在白名單時的 fallback）
	AllowedBacks  []string // 允許的返回來源（origin 白名單，支援 www.dor.tw / dor.hero-mi.com 雙網域）
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
// amountNTD 為整數新台幣；itemName/tradeDesc 為顯示文字；clientBack 為付款後返回網址（空→用預設）。
func (c *Config) BuildCheckout(tradeNo string, amountNTD int, itemName, tradeDesc string, now time.Time, clientBack string) map[string]string {
	if clientBack == "" {
		clientBack = c.ClientBackURL
	}
	params := map[string]string{
		"MerchantID":        c.MerchantID,
		"MerchantTradeNo":   tradeNo,
		"MerchantTradeDate": now.Format("2006/01/02 15:04:05"),
		"PaymentType":       "aio",
		"TotalAmount":       fmt.Sprintf("%d", amountNTD),
		"TradeDesc":         tradeDesc,
		"ItemName":          itemName,
		"ReturnURL":         c.ReturnURL,
		"ClientBackURL":     clientBack,
		"ChoosePayment":     "ALL",
		"EncryptType":       "1",
	}
	params["CheckMacValue"] = c.CheckMacValue(params)
	return params
}

// resolveClientBack 驗證前端帶來的返回網址：origin 在白名單內就用它（讓玩家付款後回到「原本網域」，
// 支援雙網域），否則用預設 ClientBackURL。避免開放式轉址（任意 URL 注入）。
func (c *Config) resolveClientBack(candidate string) string {
	candidate = strings.TrimRight(strings.TrimSpace(candidate), "/")
	co := originOf(candidate)
	if co == "" {
		return c.ClientBackURL
	}
	for _, a := range append([]string{c.ClientBackURL}, c.AllowedBacks...) {
		if o := originOf(a); o != "" && strings.EqualFold(o, co) {
			return candidate
		}
	}
	return c.ClientBackURL
}

// originOf 取 scheme://host（小寫），用於 origin 白名單比對。
func originOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// VerifyCallback 重算 CheckMacValue 與回傳的比對。
func (c *Config) VerifyCallback(params map[string]string) bool {
	got := params["CheckMacValue"]
	if got == "" {
		return false
	}
	return strings.EqualFold(got, c.CheckMacValue(params))
}

// MultiConfig 依請求來源網域在正式／測試特店間切換。
//
// 故障安全設計：UAT(dor.hero-mi.com) 與正式(www.dor.tw) 目前共用同一後端 process、同一組全域設定，
// 只有「全域 ECPayEnv=prod」且「來源網域明確列在 ProdHosts」同時成立，才會使用正式特店；
// 其他任何情況（UAT、localhost、未知網域、ECPayEnv≠prod）一律回退測試特店——寧可誤用測試也不可誤刷真錢。
type MultiConfig struct {
	Prod      *Config
	Stage     *Config
	GlobalEnv string   // 全域 ECPAY_ENV（config.Config.ECPayEnv）
	ProdHosts []string // 允許使用正式特店的網域（已切好，比對忽略大小寫與 port）
}

// ResolveByHost 依請求來源網域決定這筆交易要用哪組特店，回傳環境代號（連同 Config 一併存進
// payment_transactions.ecpay_env，供 Notify 驗章／退款時查回同一組憑證）。
func (m *MultiConfig) ResolveByHost(host string) (env string, cfg *Config) {
	h := normalizeHost(host)
	if m.GlobalEnv == "prod" && h != "" {
		for _, ph := range m.ProdHosts {
			if strings.EqualFold(strings.TrimSpace(ph), h) {
				return "prod", m.Prod
			}
		}
	}
	if m.GlobalEnv == "prod" {
		// 正式環境卻解析不到正式網域：這是故障安全的預期後備路徑（見上方 struct 註解），但也可能代表
		// 反向代理沒有正確帶 X-Forwarded-Host、或有人直接打 API 網域略過前端代理。記一筆 log 供上線後稽核，
		// 不改變「一律退回測試特店」的行為。
		log.Warn().Str("resolved_host", h).Strs("prod_hosts", m.ProdHosts).
			Msg("ecpay: prod env but host did not match ProdHosts, falling back to stage (fail-safe)")
	}
	return "stage", m.Stage
}

// ByEnv 依已存的環境代號（payment_transactions.ecpay_env）取回對應憑證，Notify 驗章／退款用。
// 未知值一律視為 stage（故障安全）。
func (m *MultiConfig) ByEnv(env string) *Config {
	if env == "prod" {
		return m.Prod
	}
	return m.Stage
}

func normalizeHost(raw string) string {
	h := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	return h
}

// requestHost 取請求來源網域：優先 X-Forwarded-Host（前端經反向代理/Next.js rewrites 轉送 API 請求時，
// 這是唯一能反映瀏覽器實際網域的欄位；本專案的 API 對外只有單一 Host，r.Host 無法區分 UAT/正式），
// 沒有才退回 r.Host。
func requestHost(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		if i := strings.IndexByte(fwd, ','); i >= 0 {
			fwd = fwd[:i]
		}
		return strings.TrimSpace(fwd)
	}
	return r.Host
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

// CreateTx 建立一筆待付款交易，記下這筆交易當下用的是哪組特店（ecpayEnv/ecpayMerchantID），
// 之後 Notify 驗章與退款都要用同一組憑證，不可事後改用全域設定推斷。
func (r *Repository) CreateTx(ctx context.Context, orderID, tradeNo, ecpayEnv, ecpayMerchantID string, amountCents int) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO payment_transactions (order_id, merchant_trade_no, amount_cents, ecpay_env, ecpay_merchant_id)
		VALUES ($1, $2, $3, $4, NULLIF($5,''))`, orderID, tradeNo, amountCents, ecpayEnv, ecpayMerchantID)
	return err
}

// TxForNotify Notify 處理需要的交易資訊
type TxForNotify struct {
	ID          string
	OrderID     string
	AmountCents int
	EcpayEnv    string
	Status      string
}

// GetTxForNotify 由 merchant_trade_no 找回交易資訊（含當初用哪組特店環境，供驗章用）
func (r *Repository) GetTxForNotify(ctx context.Context, tradeNo string) (*TxForNotify, error) {
	t := &TxForNotify{}
	err := r.db.QueryRow(ctx, `
		SELECT id::text, order_id::text, amount_cents, ecpay_env, status
		FROM payment_transactions WHERE merchant_trade_no=$1`, tradeNo).
		Scan(&t.ID, &t.OrderID, &t.AmountCents, &t.EcpayEnv, &t.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// MarkTxPaid 原子標記交易已付款（CAS：僅在目前狀態是 pending 或 failed 時才更新，避免重複通知/重放重複入帳；
// 明確排除 refunded——已退款的交易不可被晚到/重放的付款通知「復活」成 paid）。
// 回傳這次呼叫是否真的觸發了入帳（true＝首次入帳）。呼叫端不應以此值決定要不要連動 MarkOrderPaid：
// MarkOrderPaid 本身有自己的 CAS（WHERE status='pending'），對重送/重放天生冪等，永遠呼叫它才能讓
// 「MarkTxPaid 成功但 MarkOrderPaid 當下失敗」的交易在下次重送時自我修復。
func (r *Repository) MarkTxPaid(ctx context.Context, tradeNo, rtnCode, rtnMsg, ecpayTradeNo, paymentType string, tradeAmtCents int, raw []byte) (bool, error) {
	ct, err := r.db.Exec(ctx, `
		UPDATE payment_transactions
		SET status='paid', rtn_code=$2, rtn_msg=$3, ecpay_trade_no=NULLIF($4,''),
		    payment_type=NULLIF($5,''), trade_amt_cents=$6, raw=$7, paid_at=NOW()
		WHERE merchant_trade_no=$1 AND status IN ('pending','failed')`,
		tradeNo, rtnCode, rtnMsg, ecpayTradeNo, paymentType, tradeAmtCents, raw)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// MarkTxFailed 標記付款失敗/異常（RtnCode != 1，或金額比對不符）；只更新 payment_transactions，
// 訂單(orders)維持 pending 讓玩家仍可重新付款。僅在交易目前是 pending 或 failed 時才覆寫——避免晚到的失敗
// 通知把已成功入帳(paid)或已退款(refunded)的交易洗掉狀態。
func (r *Repository) MarkTxFailed(ctx context.Context, tradeNo, rtnCode, rtnMsg string, raw []byte) error {
	_, err := r.db.Exec(ctx, `
		UPDATE payment_transactions
		SET status='failed', rtn_code=$2, rtn_msg=$3, raw=$4
		WHERE merchant_trade_no=$1 AND status IN ('pending','failed')`, tradeNo, rtnCode, rtnMsg, raw)
	return err
}

// --- Handler ---

// OrderMarker 由 race.Service 實作（標記訂單+報名已付 / 已退款）
type OrderMarker interface {
	MarkOrderPaid(ctx context.Context, orderID, paymentRef string) error
	// MarkOrderRefunded 標記訂單已退款，並連動處理報名狀態（反向於 MarkOrderPaid：比照付款成功時的
	// 連動邏輯，取消對應報名、釋放分組名額）。只在訂單目前為 paid 時生效，其餘狀態安靜視為已處理（冪等）。
	MarkOrderRefunded(ctx context.Context, orderID string) error
}

type Handler struct {
	multi  *MultiConfig
	repo   *Repository
	marker OrderMarker
}

func NewHandler(multi *MultiConfig, repo *Repository, marker OrderMarker) *Handler {
	return &Handler{multi: multi, repo: repo, marker: marker}
}

// Checkout POST /api/v1/payments/ecpay/checkout（需登入）
func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var req struct {
		OrderID       string `json:"order_id"`
		ClientBackURL string `json:"client_back_url"` // 前端帶自身 origin，付款後回到原網域（雙網域用）
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
	// 白名單：只有 pending 才可結帳。paid 之外，refunded/cancelled 的訂單也一律擋下——
	// 這些訂單已經走完（或退出）生命週期，不應該再被結帳一次而繞過 MarkOrderPaid 的 CAS 靜默 no-op。
	if order.Status != "pending" {
		msg := "訂單目前無法付款"
		switch order.Status {
		case "paid":
			msg = "訂單已付款"
		case "refunded":
			msg = "訂單已退款，無法再次付款；如需重新報名請洽客服"
		case "cancelled":
			msg = "訂單已取消，無法付款"
		}
		respondErr(w, http.StatusConflict, msg)
		return
	}
	if order.TotalCents <= 0 {
		respondErr(w, http.StatusBadRequest, "訂單金額為 0，無需付款")
		return
	}

	env, cfg := h.multi.ResolveByHost(requestHost(r))

	tradeNo := genTradeNo()
	if err := h.repo.CreateTx(r.Context(), req.OrderID, tradeNo, env, cfg.MerchantID, order.TotalCents); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to create transaction")
		return
	}

	itemName := truncate("DOR Race Registration", 100)
	clientBack := cfg.resolveClientBack(req.ClientBackURL)
	params := cfg.BuildCheckout(tradeNo, order.TotalCents/100, itemName, "DOR Registration", time.Now(), clientBack)
	respondJSON(w, http.StatusOK, map[string]any{
		"action_url": cfg.ActionURL(),
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

	tradeNo := params["MerchantTradeNo"]
	if tradeNo == "" {
		w.Write([]byte("0|NoTradeNo"))
		return
	}

	tx, err := h.repo.GetTxForNotify(r.Context(), tradeNo)
	if err != nil || tx == nil {
		w.Write([]byte("0|TradeNotFound"))
		return
	}

	// 正式環境硬性拒絕「測試特店」交易入帳：測試特店（stage）的 HashKey/HashIV 是綠界官方公開的沙箱憑證，
	// 任何人都能自行算出合法的 CheckMacValue。ResolveByHost 對「網域不明」的故障安全預設是退回 stage
	// （見 MultiConfig 註解），這在 Checkout 端是刻意的（寧可誤用測試特店也不可誤刷真錢）；但若放任 stage
	// 交易在正式環境也能把訂單標為 paid，等於任何人都能繞過付款、用公開金鑰偽造 Notify 完成免費報名。
	// 因此：只要全域環境是 prod，就只接受 ecpay_env='prod' 的交易入帳，stage 交易一律拒絕（不影響
	// GlobalEnv != prod 時的既有測試/UAT 流程）。
	if h.multi.GlobalEnv == "prod" && tx.EcpayEnv != "prod" {
		log.Warn().
			Str("merchant_trade_no", tradeNo).
			Str("order_id", tx.OrderID).
			Str("tx_env", tx.EcpayEnv).
			Msg("ecpay notify: refusing non-prod transaction while running in prod (possible forged/downgraded notify)")
		w.Write([]byte("0|EnvNotAllowed"))
		return
	}

	// 用這筆交易當初實際使用的那組特店憑證驗章（不可一律用全域設定，否則 UAT 回調驗章會失敗，
	// 或更糟——用錯憑證誤判為驗章通過）。
	cfg := h.multi.ByEnv(tx.EcpayEnv)
	if !cfg.VerifyCallback(params) {
		w.Write([]byte("0|CheckMacValueError"))
		return
	}

	rtnCode := params["RtnCode"]
	rtnMsg := params["RtnMsg"]
	raw, _ := json.Marshal(params)

	if rtnCode == "1" {
		// 金額竄改防護：比對綠界回傳的 TradeAmt 與「當初實際送給綠界的金額」——Checkout 送出的
		// TotalAmount 是 order.TotalCents/100（整數元，綠界只收整元），因此這裡也要用同一個換算方式
		// 比對，否則任何非整百分的訂單總額（如百分比折扣序號折出來的零頭）永遠會被誤判為金額竄改。
		tradeAmtNTD, convErr := strconv.Atoi(params["TradeAmt"])
		if convErr != nil || tradeAmtNTD != tx.AmountCents/100 {
			log.Warn().
				Str("merchant_trade_no", tradeNo).
				Str("order_id", tx.OrderID).
				Int("expected_ntd", tx.AmountCents/100).
				Str("trade_amt", params["TradeAmt"]).
				Msg("ecpay notify: TradeAmt mismatch, refusing to mark paid")
			mismatchMsg := fmt.Sprintf("AMOUNT_MISMATCH expected_ntd=%d got_trade_amt=%s; %s", tx.AmountCents/100, params["TradeAmt"], rtnMsg)
			if err := h.repo.MarkTxFailed(r.Context(), tradeNo, rtnCode, mismatchMsg, raw); err != nil {
				log.Warn().Err(err).Str("merchant_trade_no", tradeNo).Msg("ecpay notify: mark amount-mismatch failed")
			}
			w.Write([]byte("1|OK")) // 驗章有效，回應綠界停止重送；本筆不入帳
			return
		}

		if _, err := h.repo.MarkTxPaid(r.Context(), tradeNo, rtnCode, rtnMsg, params["TradeNo"], params["PaymentType"], tradeAmtNTD*100, raw); err != nil {
			w.Write([]byte("0|MarkTxPaidFailed"))
			return
		}
		// 無論這次是否真的觸發了 tx 從 pending 轉成 paid（applied），都要呼叫 MarkOrderPaid：
		// 它本身是 CAS（WHERE status='pending'），對重送/重放天生冪等。若只在 applied=true 時才呼叫，
		// 一旦上次呼叫 MarkTxPaid 已成功但 MarkOrderPaid 當下失敗（DB 短暫異常），tx 會停在 paid、
		// 但訂單永遠停在 pending——因為下次重送時 applied 必為 false，MarkOrderPaid 再也不會被呼叫。
		if err := h.marker.MarkOrderPaid(r.Context(), tx.OrderID, "ECPay:"+tradeNo); err != nil {
			log.Error().Err(err).
				Str("merchant_trade_no", tradeNo).
				Str("order_id", tx.OrderID).
				Msg("ecpay notify: mark order paid failed — payment received but order not updated, needs manual reconciliation")
			w.Write([]byte("0|MarkPaidFailed"))
			return
		}
	} else {
		// 付款失敗/取消：只標記交易本身，訂單維持 pending 讓玩家可重新付款。
		if err := h.repo.MarkTxFailed(r.Context(), tradeNo, rtnCode, rtnMsg, raw); err != nil {
			log.Warn().Err(err).Str("merchant_trade_no", tradeNo).Msg("ecpay notify: mark failed error")
		}
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
