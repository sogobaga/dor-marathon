// 綠界站內付2.0 VIP 訂閱綁卡流程的 HTTP handler（VIP 訂閱 Phase C2）：
//   - Subscribe        POST /profile/vip/subscribe           發起訂閱：建立 pending 訂單 + 取得綁卡 Token
//   - CompleteBindCard POST /profile/vip/bind-card/complete  前端完成綠界綁卡頁後，建立綁定卡片（可能落 3D 分支）
//   - Notify           POST /payments/ecpay/bind/notify      綠界 ReturnURL（server-to-server webhook，公開）
//   - Result           POST /payments/ecpay/bind/result      綠界 OrderResultURL（3D 驗證完成後瀏覽器導回，公開）
//
// 與 ecpay_bind.go（Phase B，只有 outbound BindClient）、payment.go（既有 AIO Checkout/Notify）的分工：
// 本檔是「這些 outbound 呼叫要怎麼被觸發、觸發後怎麼把結果落地到我方 DB」——三個入口
// （CompleteBindCard 非3D成功分支／Notify／Result）最終都匯聚到同一個 settleVipBindPayment，
// 用 CAS（orders.status: pending→paid）保證「絕不重複入帳/重複延長 VIP」，見該函式註解。
package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/vip"
)

// MerchantMemberID 把使用者 UUID 轉成綠界站內付2.0 要求的英數字會員識別碼：去除連字號後的 32 碼
// 小寫十六進位字串（UUID 標準格式本身只含 0-9a-f 與連字號，去除連字號後天然滿足英數限制，不需要
// 額外做 base62/base36 轉換）。集中一處，避免 GetToken／CreateBindCard 等各呼叫點各自轉換寫法不一致。
func MerchantMemberID(userID string) string {
	return strings.ReplaceAll(userID, "-", "")
}

// VipOrderCreator 由 race.Repository 實作（CreateVipOrder 簽章與此完全相同，Go 結構化型別自動滿足，
// race.Repository 不需要特別宣告實作這個介面）。BindHandler 需要建立 VIP 訂閱的 pending 訂單，但
// payment 套件不能 import race（race 已 import payment，兩者互相 import 會循環），故用依賴反轉，
// 比照既有 OrderMarker（payment.Handler 依賴 race.Service 標記訂單已付）的作法。
type VipOrderCreator interface {
	CreateVipOrder(ctx context.Context, userID, plan string, amountCents int, desc string) (orderID, merchantTradeNo string, err error)
}

// BindHandler 綠界站內付2.0 VIP 訂閱流程的 HTTP handler。
type BindHandler struct {
	client *BindClient
	// repo 重用既有 AIO 的 Repository：CheckoutCreateTx/GetPayableOrder/GetTxForNotify/MarkTxFailed
	// 對「訂單/交易」的操作與付款方式（AIO 或站內付2.0）無關，可直接共用，不需要另開一份。
	repo        *Repository
	db          *pgxpool.Pool
	orders      VipOrderCreator
	bindEnv     string // ECPayBindEnv，落地進 payment_transactions.ecpay_env 供稽核（Bind 本身只有單一組憑證，不像 AIO MultiConfig 需要靠它切換憑證/驗章）
	returnURL   string // ReturnURL：server-to-server webhook（見 Notify）
	resultURL   string // OrderResultURL：3D 驗證完成後瀏覽器導回（見 Result）
	frontendURL string // Result 3D 導回結果頁用（沿用 config.FrontendURL，Strava OAuth 已用同一欄位）
}

func NewBindHandler(client *BindClient, repo *Repository, db *pgxpool.Pool, orders VipOrderCreator, bindEnv, returnURL, resultURL, frontendURL string) *BindHandler {
	return &BindHandler{
		client: client, repo: repo, db: db, orders: orders,
		bindEnv: bindEnv, returnURL: returnURL, resultURL: resultURL, frontendURL: frontendURL,
	}
}

// ================= §1 訂閱發起 =================

// Subscribe POST /api/v1/profile/vip/subscribe（登入態）。Body {plan: "monthly"|"annual"}。
func (h *BindHandler) Subscribe(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var req struct {
		Plan string `json:"plan"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Plan != "monthly" && req.Plan != "annual" {
		respondErr(w, http.StatusBadRequest, "plan must be monthly or annual")
		return
	}
	ctx := r.Context()

	// 1) 已有 active 訂閱 → 409（不可重複訂閱）
	var hasActive bool
	if err := h.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vip_subscriptions WHERE user_id=$1 AND status='active')`, userID).
		Scan(&hasActive); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to check existing subscription")
		return
	}
	if hasActive {
		respondErr(w, http.StatusConflict, "已有進行中的 VIP 訂閱，如需變更方案請先取消現有訂閱")
		return
	}

	// 2) 算首期價：與 GET /profile/vip/pricing 同一套邏輯（見 vip.ComputeQuote），確保訂閱發起
	// 算出的首期價恰好等於玩家在定價頁面看到的數字。
	quote, err := vip.ComputeQuote(ctx, h.db, userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to compute pricing")
		return
	}
	amountCents := quote.PriceCentsForPlan(req.Plan)
	if amountCents <= 0 {
		respondErr(w, http.StatusInternalServerError, "invalid computed price")
		return
	}

	// 3) 建立 pending 訂單（無賽事，見 race.Repository.CreateVipOrder）
	orderID, tradeNo, err := h.orders.CreateVipOrder(ctx, userID, req.Plan, amountCents, "")
	if err != nil {
		log.Error().Err(err).Str("user_id", userID).Str("plan", req.Plan).Msg("vip subscribe: CreateVipOrder failed")
		respondErr(w, http.StatusInternalServerError, "failed to create order")
		return
	}

	// 4) 建 payment_transactions pending 列：重用既有 AIO Checkout 的同一套流程（鎖 order 列 →
	// 作廢舊 pending（剛建的新訂單不會有）→ insert 新 pending，見 Repository.CheckoutCreateTx）——
	// 之後 Notify/Result webhook 都靠這裡落地的 merchant_trade_no 對回訂單。
	if err := h.repo.CheckoutCreateTx(ctx, orderID, tradeNo, h.bindEnv, h.client.MerchantID, amountCents); err != nil {
		log.Error().Err(err).Str("order_id", orderID).Msg("vip subscribe: CheckoutCreateTx failed")
		respondErr(w, http.StatusInternalServerError, "failed to create transaction")
		return
	}

	// 5) 使用者資訊（GetToken 的 ConsumerInfo 需要 Email 或 Phone 擇一必填，本站一律有 Email）
	var email, name string
	if err := h.db.QueryRow(ctx, `SELECT email, COALESCE(name,'') FROM users WHERE id=$1`, userID).Scan(&email, &name); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load user")
		return
	}

	itemName := "VIP 月費訂閱"
	if req.Plan == "annual" {
		itemName = "VIP 年費訂閱"
	}
	// MerchantTradeDate 用固定 UTC+8 offset 換算台灣時間，不用 time.LoadLocation("Asia/Taipei")——
	// distroless 部署映像沒有 tzdata，LoadLocation 會在啟動或執行期直接出錯（比照 payment.go 既有
	// BuildCheckout／race 等模組已建立的慣例）。
	taipeiNow := time.Now().UTC().Add(8 * time.Hour)

	tokenResp, err := h.client.GetToken(ctx, GetTokenReq{
		ConsumerInfo: ConsumerInfo{
			MerchantMemberID: MerchantMemberID(userID),
			Email:            email,
			Name:             name,
		},
		OrderInfo: GetTokenOrderInfo{
			MerchantTradeDate: taipeiNow.Format("2006/01/02 15:04:05"),
			MerchantTradeNo:   tradeNo,
			TotalAmount:       amountCents / 100,
			TradeDesc:         "DOR VIP 訂閱",
			ItemName:          itemName,
			ReturnURL:         h.returnURL,
		},
		OrderResultURL: h.resultURL,
	})
	if err != nil {
		var bizErr *BindBizError
		if errors.As(err, &bizErr) {
			log.Warn().Str("order_id", orderID).Int("rtn_code", bizErr.RtnCode).Str("rtn_msg", bizErr.RtnMsg).
				Msg("vip subscribe: GetToken biz error")
			respondErr(w, http.StatusBadRequest, bizErr.RtnMsg)
			return
		}
		log.Error().Err(err).Str("order_id", orderID).Msg("vip subscribe: GetToken failed")
		respondErr(w, http.StatusInternalServerError, "failed to start bind-card flow")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"token":             tokenResp.Token,
		"token_expire_date": tokenResp.TokenExpireDate,
		"order_id":          orderID,
		"merchant_trade_no": tradeNo,
		"amount_cents":      amountCents,
	})
}

// ================= §2 綁卡完成 =================

// CompleteBindCard POST /api/v1/profile/vip/bind-card/complete（登入態）。
// Body {bind_card_pay_token, order_id}。
func (h *BindHandler) CompleteBindCard(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var req struct {
		BindCardPayToken string `json:"bind_card_pay_token"`
		OrderID          string `json:"order_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.BindCardPayToken == "" || req.OrderID == "" {
		respondErr(w, http.StatusBadRequest, "bind_card_pay_token and order_id are required")
		return
	}
	ctx := r.Context()

	// 1) 驗訂單屬於本人且 pending（GetPayableOrder 重用既有 AIO Checkout 的同一支查詢，與付款
	// 方式無關，見 payment.go）。
	order, err := h.repo.GetPayableOrder(ctx, req.OrderID, userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load order")
		return
	}
	if order == nil {
		respondErr(w, http.StatusNotFound, "order not found")
		return
	}
	if order.Status != "pending" {
		respondErr(w, http.StatusConflict, "訂單目前無法綁卡付款")
		return
	}
	plan, err := h.planForOrder(ctx, req.OrderID)
	if err != nil {
		respondErr(w, http.StatusBadRequest, "not a vip subscription order")
		return
	}

	// 2) CreateBindCard
	resp, err := h.client.CreateBindCard(ctx, CreateBindCardReq{
		BindCardPayToken: req.BindCardPayToken,
		MerchantMemberID: MerchantMemberID(userID),
	})
	if err != nil {
		var bizErr *BindBizError
		if errors.As(err, &bizErr) {
			// 5) BindBizError → 4xx 帶 RtnMsg（訂單保持 pending，可重試）
			log.Warn().Str("order_id", req.OrderID).Int("rtn_code", bizErr.RtnCode).Str("rtn_msg", bizErr.RtnMsg).
				Msg("vip bind-card complete: CreateBindCard biz error")
			respondErr(w, http.StatusBadRequest, bizErr.RtnMsg)
			return
		}
		log.Error().Err(err).Str("order_id", req.OrderID).Msg("vip bind-card complete: CreateBindCard failed")
		respondErr(w, http.StatusInternalServerError, "failed to bind card")
		return
	}

	// 4) 3D 分支 → 回 three_d_url，前端導轉；最終結果走 webhook（Notify/Result）結算，本次不落地任何 DB 變更。
	if resp.Is3D() {
		threeDURL := ""
		if resp.ThreeDInfo != nil {
			threeDURL = resp.ThreeDInfo.ThreeDURL
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"status":      "3d_required",
			"three_d_url": threeDURL,
		})
		return
	}

	// 3) 非 3D 成功 → 結算。金額一律用「我方自己這筆訂單當初算好的金額」（order.TotalCents），
	// 不用綠界回應帶的數字，理由同 settleVipBindPayment 呼叫端的一般原則（見 handleBindWebhookData 註解）。
	var card4, card6, mm, yy, ecpayTradeNo, gwsr string
	if resp.CardInfo != nil {
		card4, card6, mm, yy = resp.CardInfo.Card4No, resp.CardInfo.Card6No, resp.CardInfo.CardValidMM, resp.CardInfo.CardValidYY
		gwsr = strconv.Itoa(resp.CardInfo.Gwsr)
	}
	if resp.OrderInfo != nil {
		ecpayTradeNo = resp.OrderInfo.TradeNo
	}
	rawJSON, _ := json.Marshal(resp)

	if err := h.settleVipBindPayment(ctx, req.OrderID, settleVipBindParams{
		Plan:             plan,
		AmountCents:      order.TotalCents,
		MerchantMemberID: resp.MerchantMemberID,
		BindCardID:       resp.BindCardID,
		Card6No:          card6,
		Card4No:          card4,
		CardValidMM:      mm,
		CardValidYY:      yy,
		EcpayTradeNo:     ecpayTradeNo,
		Gwsr:             gwsr,
		Raw:              rawJSON,
	}); err != nil {
		log.Error().Err(err).Str("order_id", req.OrderID).Msg("vip bind-card complete: settle failed")
		respondErr(w, http.StatusInternalServerError, "付款已受理但結算失敗，請稍後查詢訂單狀態或聯繫客服")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"status": "paid", "card_last4": card4})
}

// planForOrder 由 order_items.item_type 反推這筆訂單是月繳還是年繳（VIP 訂閱訂單見
// race.Repository.CreateVipOrder：item_type 固定是 vip_month/vip_year 其中之一，qty=1，
// 每筆 VIP 訂單只有一個 item）。非 VIP 訂閱訂單（如賽事報名單）會落 default 分支回錯，避免
// 誤把一般報名訂單 ID 傳進來時被當成 VIP 訂單結算。
func (h *BindHandler) planForOrder(ctx context.Context, orderID string) (string, error) {
	var itemType string
	if err := h.db.QueryRow(ctx, `SELECT item_type FROM order_items WHERE order_id=$1 LIMIT 1`, orderID).Scan(&itemType); err != nil {
		return "", err
	}
	switch itemType {
	case "vip_month":
		return "monthly", nil
	case "vip_year":
		return "annual", nil
	default:
		return "", fmt.Errorf("order %s item_type %q is not a vip subscription item", orderID, itemType)
	}
}

// ================= §3 Webhook（ReturnURL，公開） =================

// bindWebhookTimestampSkew Notify 的重放防護窗：RpHeader.Timestamp 與現在時間差超過此值即拒收。
const bindWebhookTimestampSkew = 10 * time.Minute

// bindWebhookData ReturnURL/OrderResultURL 解密後 Data 的獨立 DTO——刻意不重用 GetTokenResp／
// CreateBindCardResp／CreatePaymentResp（那些是各同步呼叫端點各自的回應形狀），webhook 是
// 「這筆綁卡+扣款交易」的完整回報，且未來若同步回應 struct 改動不應牽動 webhook 解析。
// 欄位對照 guides/21-webhook-events-reference.md（line 439+）。
type bindWebhookData struct {
	RtnCode          int                   `json:"RtnCode"`
	RtnMsg           string                `json:"RtnMsg"`
	MerchantID       string                `json:"MerchantID,omitempty"`
	MerchantMemberID string                `json:"MerchantMemberID,omitempty"`
	BindCardID       string                `json:"BindCardID,omitempty"`
	IsSameCard       bool                  `json:"IsSameCard,omitempty"`
	OrderInfo        *bindWebhookOrderInfo `json:"OrderInfo,omitempty"`
	CardInfo         *bindWebhookCardInfo  `json:"CardInfo,omitempty"`
	CustomField      string                `json:"CustomField,omitempty"`
}

type bindWebhookOrderInfo struct {
	MerchantTradeNo string `json:"MerchantTradeNo"`
	TradeNo         string `json:"TradeNo,omitempty"`
	TradeAmt        int    `json:"TradeAmt,omitempty"`
	TradeDate       string `json:"TradeDate,omitempty"`
	PaymentType     string `json:"PaymentType,omitempty"`
	PaymentDate     string `json:"PaymentDate,omitempty"`
	TradeStatus     string `json:"TradeStatus,omitempty"` // "0"=未付款 "1"=已付款
}

type bindWebhookCardInfo struct {
	AuthCode    string `json:"AuthCode,omitempty"`
	Gwsr        int    `json:"Gwsr,omitempty"`
	Card6No     string `json:"Card6No,omitempty"`
	Card4No     string `json:"Card4No,omitempty"`
	CardValidYY string `json:"CardValidYY,omitempty"`
	CardValidMM string `json:"CardValidMM,omitempty"`
}

// Notify POST /api/v1/payments/ecpay/bind/notify（公開，綠界 server-to-server，ReturnURL）。
//
// 回應規則（綠界硬性規定）：只要收到的封包合法（解密成功、時間戳在容許範圍），不論後續業務結算
// 成不成功，一律回應純文字 1|OK（text/plain，恰好 4 字元、無換行/空白）；否則綠界每 2 小時重發。
// 業務失敗（RtnCode!=1、找不到對應訂單、結算 DB 出錯）都只記 log，不改變回應——這是刻意設計，
// 不是遺漏 error handling。
func (h *BindHandler) Notify(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var env bindRespEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		log.Warn().Err(err).Msg("ecpay bind notify: malformed envelope json")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if env.TransCode != 1 {
		// 傳輸層本身失敗：Data 不可信（可能為空/無法解密），依官方參考實作回應非 1|OK
		// （見 guides/21-webhook-events-reference.md 雙 Callback Go 範例），不當作「封包合法」處理。
		log.Warn().Int("trans_code", env.TransCode).Str("trans_msg", env.TransMsg).
			Msg("ecpay bind notify: TransCode != 1")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("0|Fail"))
		return
	}

	decJSON, err := ecpayAESDecrypt(h.client.HashKey, h.client.HashIV, env.Data)
	if err != nil {
		// 解密失敗＝不可信來源（沒有正確 HashKey/HashIV 不可能產生解得開的 Data）：直接 400，
		// 不視為「封包合法」，不回 1|OK。
		log.Warn().Err(err).Msg("ecpay bind notify: decrypt failed, refusing (untrusted source)")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// 重放防護：RpHeader.Timestamp 與現在時間差 > 10 分鐘即拒收（同樣視為「封包不合法」，不回 1|OK）。
	skew := time.Since(time.Unix(env.RpHeader.Timestamp, 0))
	if skew < 0 {
		skew = -skew
	}
	if skew > bindWebhookTimestampSkew {
		log.Warn().Int64("timestamp", env.RpHeader.Timestamp).Dur("skew", skew).
			Msg("ecpay bind notify: timestamp outside allowed skew, refusing (possible replay)")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var data bindWebhookData
	if err := json.Unmarshal([]byte(decJSON), &data); err != nil {
		log.Warn().Err(err).Msg("ecpay bind notify: malformed decrypted data json")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// 封包合法（解密＋時間戳都通過）：從這裡開始，不論後面業務結算成不成功，都回 1|OK。
	h.handleBindWebhookData(r.Context(), data)

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("1|OK"))
}

// ================= §4 3D 導回（OrderResultURL，公開） =================

// Result POST /api/v1/payments/ecpay/bind/result（公開，綠界瀏覽器 Form POST，OrderResultURL）。
//
// ⚠️ ResultData 是 JSON 字串（非直接 AES 加密——最常見的串接錯誤就是對 ResultData 本身解密），
// 需先 json.Unmarshal 取外層 {TransCode, Data}，再對 Data 欄位做 AES 解密（見官方
// guides/02-payment-ecpg.md「常見陷阱」與 guides/21-webhook-events-reference.md 雙 Callback 範例）。
func (h *BindHandler) Result(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.redirectBindResult(w, r, false)
		return
	}
	resultData := r.FormValue("ResultData")
	if resultData == "" {
		h.redirectBindResult(w, r, false)
		return
	}

	var outer bindRespEnvelope
	if err := json.Unmarshal([]byte(resultData), &outer); err != nil {
		log.Warn().Err(err).Msg("ecpay bind result: malformed ResultData json")
		h.redirectBindResult(w, r, false)
		return
	}
	if outer.TransCode != 1 {
		log.Warn().Int("trans_code", outer.TransCode).Msg("ecpay bind result: TransCode != 1")
		h.redirectBindResult(w, r, false)
		return
	}
	decJSON, err := ecpayAESDecrypt(h.client.HashKey, h.client.HashIV, outer.Data)
	if err != nil {
		log.Warn().Err(err).Msg("ecpay bind result: decrypt failed")
		h.redirectBindResult(w, r, false)
		return
	}
	var data bindWebhookData
	if err := json.Unmarshal([]byte(decJSON), &data); err != nil {
		log.Warn().Err(err).Msg("ecpay bind result: malformed decrypted data json")
		h.redirectBindResult(w, r, false)
		return
	}

	ok := h.handleBindWebhookData(r.Context(), data)
	h.redirectBindResult(w, r, ok)
}

// redirectBindResult 302 導回前端：成功 /?vip_bind=success、失敗 /?vip_bind=fail（Phase E 前端讀
// query 顯示結果）。
func (h *BindHandler) redirectBindResult(w http.ResponseWriter, r *http.Request, success bool) {
	target := h.frontendURL + "/?vip_bind=fail"
	if success {
		target = h.frontendURL + "/?vip_bind=success"
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// handleBindWebhookData 是 Notify／Result 共用的「已解密資料 → 找回訂單 → 結算」邏輯。
// 回傳這次是否成功結算（RtnCode==1 且已付款且結算無誤）——Notify 不使用回傳值（規則見 Notify
// 註解：封包合法一律回 1|OK，與業務結果無關）；Result 用它決定要導向成功還是失敗頁。
func (h *BindHandler) handleBindWebhookData(ctx context.Context, data bindWebhookData) bool {
	if data.OrderInfo == nil || data.OrderInfo.MerchantTradeNo == "" {
		log.Warn().Msg("ecpay bind webhook: missing OrderInfo.MerchantTradeNo, cannot reconcile order")
		return false
	}
	tradeNo := data.OrderInfo.MerchantTradeNo

	if data.RtnCode != 1 || data.OrderInfo.TradeStatus != "1" {
		// 業務失敗/未付款通知：只標記交易本身，訂單維持 pending 讓玩家可重新綁卡付款
		// （比照既有 AIO Notify 對失敗通知的處理，見 payment.go Notify，直接重用 MarkTxFailed）。
		raw, _ := json.Marshal(data)
		if err := h.repo.MarkTxFailed(ctx, tradeNo, strconv.Itoa(data.RtnCode), data.RtnMsg, raw); err != nil {
			log.Warn().Err(err).Str("merchant_trade_no", tradeNo).Msg("ecpay bind webhook: mark tx failed error")
		}
		return false
	}

	tx, err := h.repo.GetTxForNotify(ctx, tradeNo)
	if err != nil {
		log.Error().Err(err).Str("merchant_trade_no", tradeNo).Msg("ecpay bind webhook: lookup tx failed")
		return false
	}
	if tx == nil {
		log.Warn().Str("merchant_trade_no", tradeNo).Msg("ecpay bind webhook: no matching payment_transactions row (unknown trade)")
		return false
	}

	plan, err := h.planForOrder(ctx, tx.OrderID)
	if err != nil {
		log.Error().Err(err).Str("order_id", tx.OrderID).Msg("ecpay bind webhook: cannot determine plan for order")
		return false
	}

	var card4, card6, mm, yy, gwsr string
	if data.CardInfo != nil {
		card4, card6, mm, yy = data.CardInfo.Card4No, data.CardInfo.Card6No, data.CardInfo.CardValidMM, data.CardInfo.CardValidYY
		gwsr = strconv.Itoa(data.CardInfo.Gwsr)
	}
	raw, _ := json.Marshal(data)

	// 金額一律用「我方自己這筆交易當初建立時的金額」（tx.AmountCents，Subscribe 當下用
	// vip.ComputeQuote 算出、寫進 payment_transactions），不使用 webhook 回傳的 OrderInfo.TradeAmt——
	// 避免任何竄改/誤植的回傳金額被拿去當作實際入帳/延長 VIP 的依據，這條規則對 CompleteBindCard
	// 同步路徑也一樣（見該函式：用 order.TotalCents，不用 resp 帶回的數字）。
	if err := h.settleVipBindPayment(ctx, tx.OrderID, settleVipBindParams{
		Plan:             plan,
		AmountCents:      tx.AmountCents,
		MerchantMemberID: data.MerchantMemberID,
		BindCardID:       data.BindCardID,
		Card6No:          card6,
		Card4No:          card4,
		CardValidMM:      mm,
		CardValidYY:      yy,
		EcpayTradeNo:     data.OrderInfo.TradeNo,
		Gwsr:             gwsr,
		Raw:              raw,
	}); err != nil {
		log.Error().Err(err).Str("order_id", tx.OrderID).Str("merchant_trade_no", tradeNo).
			Msg("ecpay bind webhook: settle failed — payment received but not fully applied, needs manual reconciliation")
		return false
	}
	return true
}

// ================= §5 結算函式（單一入口，冪等核心） =================

// settleVipBindParams 結算所需的全部上下文。UserID 刻意不在這裡——一律由 CAS 閘門的
// `RETURNING user_id` 取得（見下方函式），不接受呼叫端傳入，避免任何參數誤植/竄改的 userID
// 被拿去延長「別人的」VIP 或建立「別人的」綁卡紀錄。
type settleVipBindParams struct {
	Plan             string // monthly | annual
	AmountCents      int
	MerchantMemberID string
	BindCardID       string
	Card6No          string
	Card4No          string
	CardValidMM      string
	CardValidYY      string
	EcpayTradeNo     string // 綠界 TradeNo（存進 orders.payment_ref / payment_transactions.ecpay_trade_no）
	Gwsr             string
	Raw              []byte // 完整回應/webhook 資料（稽核用，存進 payment_transactions.raw）
}

// settleVipBindPayment 是 VIP 綁卡訂閱結算的單一入口——CompleteBindCard（非3D成功分支）、
// Notify（webhook）、Result（3D 導回）三處都呼叫這裡；任何一處先到都會完整結算，晚到的
// （不論是三者中哪一種先到、或同一種被 ECPay 重送）在 CAS 閘門處直接冪等 no-op，「絕不重複
// 入帳/重複延長 VIP」的保證全部收斂在這一個函式的第一步。
//
// 單一 DB 交易，全部同一 tx commit；任何一步失敗整包 rollback（訂單留 pending，webhook 重試會再來）。
//
// ⚠️ 刻意不呼叫 race.Repository.MarkOrderPaid：那支方法自己開一個獨立交易並在裡面 commit，若拿來
// 當作本函式的「步驟 1」，後面任何一步（綁卡 upsert/VIP 延長/vip_subscriptions/paytx）失敗時，訂單
// 已經先被那個獨立交易 commit 成 paid，會造成「訂單 paid 但 VIP 沒延長」的資料不一致——必須整包在
// 同一個 tx 內才安全。CAS 條件本身（WHERE status='pending'）與 MarkOrderPaid 完全一致，只是直接
// 內聯在本函式的 tx 裡執行；VIP 訂單 registration_id 恆為 NULL，也不需要 MarkOrderPaid 那段
// registration 連動邏輯（不會誤觸、不會炸）。
func (h *BindHandler) settleVipBindPayment(ctx context.Context, orderID string, p settleVipBindParams) error {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("settle vip bind: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1) CAS 閘門：0 列＝已被結算過（冪等出口，直接 return nil）。
	var userID string
	err = tx.QueryRow(ctx, `
		UPDATE orders SET status='paid', paid_at=NOW(), payment_ref=$2
		WHERE id=$1 AND status='pending'
		RETURNING user_id`, orderID, "ECPay:"+p.EcpayTradeNo).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("settle vip bind: cas order paid: %w", err)
	}

	// MerchantMemberID 防禦性 fallback：webhook/回應理論上都會帶回這個欄位，但若任何情境下缺漏，
	// 用「我方自己對 userID 的確定性轉換」補上（見 MerchantMemberID helper），而不是存空字串。
	merchantMemberID := p.MerchantMemberID
	if merchantMemberID == "" {
		merchantMemberID = MerchantMemberID(userID)
	}

	// 2) 綁卡 upsert：既有 active 列 → deleted；insert 新列（每人每 provider 至多一張 active，
	// 見 migration 132 的 uq_card_binding_active 部分唯一索引）。
	if _, err := tx.Exec(ctx, `
		UPDATE payment_card_bindings SET status='deleted', updated_at=NOW()
		WHERE user_id=$1 AND provider='ecpay' AND status='active'`, userID); err != nil {
		return fmt.Errorf("settle vip bind: supersede old card binding: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_card_bindings
			(user_id, provider, merchant_member_id, bind_card_id, card_last4, card_expiry_mm, card_expiry_yy, status)
		VALUES ($1, 'ecpay', $2, $3, NULLIF($4,''), NULLIF($5,''), NULLIF($6,''), 'active')`,
		userID, merchantMemberID, p.BindCardID, p.Card4No, p.CardValidMM, p.CardValidYY); err != nil {
		return fmt.Errorf("settle vip bind: insert card binding: %w", err)
	}

	// 3) VIP 延長一期（日曆期別，interval 加法自帶月底 clamp，見 vip.ExtendPeriod）。
	if err := vip.ExtendPeriod(ctx, tx, userID, p.Plan); err != nil {
		return fmt.Errorf("settle vip bind: extend vip period: %w", err)
	}

	// 4) 讀回延長後的到期日，供 vip_subscriptions.current_period_end（定義上＝users.vip_expires_at）。
	var periodEnd time.Time
	if err := tx.QueryRow(ctx, `SELECT vip_expires_at FROM users WHERE id=$1`, userID).Scan(&periodEnd); err != nil {
		return fmt.Errorf("settle vip bind: read back vip_expires_at: %w", err)
	}

	// 5) vip_subscriptions：新開一列（若該 user 已有 cancelled/expired 舊列不動它，單純新增一列）。
	if _, err := tx.Exec(ctx, `
		INSERT INTO vip_subscriptions
			(user_id, plan, amount_cents, status, started_at, current_period_end, exec_times, provider, merchant_member_id, gwsr)
		VALUES ($1, $2, $3, 'active', NOW(), $4, 1, 'ecpay', $5, NULLIF($6,''))`,
		userID, p.Plan, p.AmountCents, periodEnd, merchantMemberID, p.Gwsr); err != nil {
		return fmt.Errorf("settle vip bind: insert vip_subscriptions: %w", err)
	}

	// 6) payment_transactions 標記已付（CAS 條件比照既有 MarkTxPaid：僅 pending/failed 才覆寫；
	// 但這裡要跟其餘步驟同一個 tx，不能呼叫 Repository.MarkTxPaid——那支方法用的是 r.db 直連，不吃外部 tx）。
	if _, err := tx.Exec(ctx, `
		UPDATE payment_transactions
		SET status='paid', rtn_code='1', rtn_msg='VIP bind card paid', ecpay_trade_no=NULLIF($2,''),
		    payment_type='Credit', trade_amt_cents=$3, raw=$4, paid_at=NOW()
		WHERE order_id=$1 AND status IN ('pending','failed')`,
		orderID, p.EcpayTradeNo, p.AmountCents, p.Raw); err != nil {
		return fmt.Errorf("settle vip bind: mark payment_transactions paid: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("settle vip bind: commit: %w", err)
	}
	return nil
}
