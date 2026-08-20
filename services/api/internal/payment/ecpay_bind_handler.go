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
	"github.com/jackc/pgx/v5/pgconn"
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

// MailInserter 站內信最小介面（VIP 訂閱 Phase D 續約通知用）：由 mail.Handler 實作。用小介面而非直接
// import internal/mail，比照 push 套件既有的 MailInserter 介面同一慣例（見 internal/push/push.go），
// 雖然 payment↔mail 目前並無循環依賴風險，仍維持這個慣例讓依賴方向單純、好測試。
type MailInserter interface {
	InsertForUsers(ctx context.Context, userIDs []string, level, title, body, url string) (int, error)
}

// BindHandler 綠界站內付2.0 VIP 訂閱流程的 HTTP handler；同時也是 VIP 訂閱 Phase D 每日續約排程
// （RunRenewalLoop，見 vip_renewal.go）的宿主——續約排程需要的 client/repo/db/orders/bindEnv/
// returnURL 這裡全部已有，不另外開一個平行結構重複持有同一組依賴。
type BindHandler struct {
	client *BindClient
	// repo 重用既有 AIO 的 Repository：CheckoutCreateTx/GetPayableOrder/GetTxForNotify/MarkTxFailed
	// 對「訂單/交易」的操作與付款方式（AIO 或站內付2.0）無關，可直接共用，不需要另開一份。
	repo        *Repository
	db          *pgxpool.Pool
	orders      VipOrderCreator
	mail        MailInserter // 續約排程通知用（Phase D）；可為 nil（測試/未注入時通知直接跳過，不影響金流本身）
	bindEnv     string       // ECPayBindEnv，落地進 payment_transactions.ecpay_env 供稽核（Bind 本身只有單一組憑證，不像 AIO MultiConfig 需要靠它切換憑證/驗章）
	returnURL   string       // ReturnURL：server-to-server webhook（見 Notify）；Phase D 續約請款也重用同一個 ReturnURL，共用同一入口做結算分流（見 handleBindWebhookData）
	resultURL   string       // OrderResultURL：3D 驗證完成後瀏覽器導回（見 Result）
	frontendURL string       // Result 3D 導回結果頁用（沿用 config.FrontendURL，Strava OAuth 已用同一欄位）
}

func NewBindHandler(client *BindClient, repo *Repository, db *pgxpool.Pool, orders VipOrderCreator, mail MailInserter, bindEnv, returnURL, resultURL, frontendURL string) *BindHandler {
	return &BindHandler{
		client: client, repo: repo, db: db, orders: orders, mail: mail,
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

	// 1.5) 併發/重複防線（對抗式審查修正）：hasActive 檢查無鎖，擋不住「連按兩次訂閱→兩筆 pending
	// 訂單→各自完成綁卡→重複扣款」。雙層防護：
	//   a. 先作廢使用者既有的 pending VIP 訂單（每次發起＝一份新報價新訂單；舊訂單即使已在綠界拿過
	//      綁卡 Token、事後被完成，settle 的 CAS 也會因訂單非 pending 而 no-op，並由 settle 端記
	//      「已扣款但訂單不可結算」的人工對帳 log，見 settleVipBindPayment）。
	//   b. DB 唯一索引 uq_orders_pending_vip（migration 133：每人至多一筆 pending VIP 訂單）兜底真併發
	//      ——兩個請求同時通過這裡時，後到的 CreateVipOrder INSERT 撞索引，回 409。
	if _, err := h.db.Exec(ctx, `
		UPDATE orders SET status='cancelled'
		WHERE user_id=$1 AND status='pending' AND race_id IS NULL`, userID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to void previous pending order")
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
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// 撞 uq_orders_pending_vip（migration 133）：另一個並發的 Subscribe 剛建了 pending 訂單
			respondErr(w, http.StatusConflict, "訂閱流程正在進行中，請勿重複發起")
			return
		}
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

	// 3) 非 3D 成功 → 先驗「這個 token 實際完成的交易」與 client 宣稱的訂單一致，再結算
	// （對抗式審查修正）：CreateBindCard 實際扣款對象由 BindCardPayToken 決定（token 綁著 GetToken
	// 當下的 MerchantTradeNo／金額），client 傳入的 order_id 只是「宣稱」——若不驗證，攻擊者可拿
	// 「月繳訂單的 token」配「年繳訂單的 order_id」低付高得。以綠界回報的 MerchantTradeNo 反查我方
	// payment_transactions 對回真正的訂單，必須與 req.OrderID 一致才結算；驗不了（缺欄位/查無）
	// 一律拒絕落地——寧可留給人工對帳，也不能把無法對應的扣款拿去標訂單。
	// （3D 分支無此問題：其結算走 Notify/Result webhook，orderID 一律由綠界回報的 MerchantTradeNo
	// 反查，不吃 client 輸入。）
	mtn := ""
	if resp.OrderInfo != nil {
		mtn = resp.OrderInfo.MerchantTradeNo
	}
	if mtn == "" {
		log.Error().Str("order_id", req.OrderID).Msg("vip bind-card complete: response missing MerchantTradeNo, refusing to settle (needs manual reconciliation)")
		respondErr(w, http.StatusConflict, "付款結果無法核對訂單，請聯繫客服")
		return
	}
	btx, err := h.repo.GetTxForNotify(ctx, mtn)
	if err != nil || btx == nil || btx.OrderID != req.OrderID {
		log.Error().Str("claimed_order_id", req.OrderID).Str("merchant_trade_no", mtn).
			Msg("vip bind-card complete: token/order mismatch, refusing to settle (possible tampering)")
		respondErr(w, http.StatusConflict, "付款結果與訂單不符，請聯繫客服")
		return
	}

	// 金額一律用「我方自己這筆訂單當初算好的金額」（order.TotalCents），
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
		MerchantTradeNo:  mtn,
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

	tx, err := h.repo.GetTxForNotify(ctx, tradeNo)
	if err != nil {
		log.Error().Err(err).Str("merchant_trade_no", tradeNo).Msg("ecpay bind webhook: lookup tx failed")
		return false
	}
	if tx == nil {
		log.Warn().Str("merchant_trade_no", tradeNo).Msg("ecpay bind webhook: no matching payment_transactions row (unknown trade)")
		return false
	}

	// Phase D 續約分流：這筆訂單若能在 vip_renewal_attempts 找到對應列，代表這是續約幕後請款
	// （CreatePaymentWithCardID／RunRenewalLoop 建立），不是首次綁卡訂閱（CreateBindCard／Subscribe）——
	// 兩者共用同一個 Notify/Result 入口（續約請款的 OrderInfo.ReturnURL 刻意沿用同一個 h.returnURL，
	// 見 processRenewalCandidate），但結算方式完全不同：settleVipBindPayment 會 INSERT 新
	// vip_subscriptions 列，若對續約訂單呼叫會撞 uq_vip_subs_active（該 user 已有一筆 active 訂閱）；
	// 續約必須改呼叫 settleVipRenewal（更新既有 active 列，不新增、不動綁卡）。用 order_id 反查
	// vip_renewal_attempts 是否命中，比「該 user 是否已有 active 訂閱」這種間接判斷更精確——不受
	// 「使用者剛好在此刻取消訂閱/正在走另一次手動 Subscribe」等時序影響（那樣間接判斷可能誤判）。
	var attemptID string
	isRenewal := h.db.QueryRow(ctx, `SELECT id::text FROM vip_renewal_attempts WHERE order_id=$1`, tx.OrderID).Scan(&attemptID) == nil

	// BindCardID 缺漏 guard（對抗式審查修正）只適用於「首次綁卡」流程：成功通知理應帶 BindCardID；
	// 若缺（異常封包），絕不能讓空字串落進 payment_card_bindings 當 active 綁卡——Phase D 續扣排程
	// 拿空 token 請款必失敗，而且是靜默壞掉。續約請款（CreatePaymentWithCardID）用的是既有 BindCardID，
	// 這支端點的成功回應本來就不保證回帶 BindCardID 欄位（bindWebhookData 是兩種端點共用的解析
	// DTO），不能套用同一條防線，否則會讓所有續約成功的 webhook 都被誤判成「異常缺欄位」而拒絕結算。
	if !isRenewal && data.RtnCode == 1 && data.OrderInfo.TradeStatus == "1" && data.BindCardID == "" {
		log.Error().Str("merchant_trade_no", data.OrderInfo.MerchantTradeNo).
			Msg("ecpay bind webhook: paid notification missing BindCardID, refusing to settle (needs manual reconciliation)")
		return false
	}

	if data.RtnCode != 1 || data.OrderInfo.TradeStatus != "1" {
		// 業務失敗/未付款通知：只標記交易本身（比照既有 AIO Notify 對失敗通知的處理，見 payment.go
		// Notify，直接重用 MarkTxFailed）。
		raw, _ := json.Marshal(data)
		if err := h.repo.MarkTxFailed(ctx, tradeNo, strconv.Itoa(data.RtnCode), data.RtnMsg, raw); err != nil {
			log.Warn().Err(err).Str("merchant_trade_no", tradeNo).Msg("ecpay bind webhook: mark tx failed error")
		}
		if isRenewal {
			// 續約訂單維持 pending 會卡住 uq_orders_pending_vip（明天的重試建不了新訂單）——首次綁卡
			// 訂單則刻意維持 pending 讓玩家可在同一筆訂單上重新綁卡付款，兩者語意不同，故續約這裡額外
			// 標記 attempt 列失敗＋作廢訂單；是否終結整個訂閱交給下一輪排程的 attempt_no 判斷
			// （見 RenewalScheduler 同名邏輯 processRenewalCandidate），這裡只負責讓流程不被卡住。
			if _, err := h.db.Exec(ctx, `
				UPDATE vip_renewal_attempts SET status='failed', rtn_code=$2, rtn_msg=$3
				WHERE id=$1 AND status='processing'`,
				attemptID, strconv.Itoa(data.RtnCode), data.RtnMsg); err != nil {
				log.Warn().Err(err).Str("attempt_id", attemptID).Msg("ecpay bind webhook: mark renewal attempt failed error")
			}
			if _, err := h.db.Exec(ctx, `UPDATE orders SET status='cancelled' WHERE id=$1 AND status='pending'`, tx.OrderID); err != nil {
				log.Warn().Err(err).Str("order_id", tx.OrderID).Msg("ecpay bind webhook: cancel failed renewal order error")
			}
		}
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

	// 金額一律用「我方自己這筆交易當初建立時的金額」（tx.AmountCents，Subscribe/續約排程當下寫進
	// payment_transactions 的金額），不使用 webhook 回傳的 OrderInfo.TradeAmt——避免任何竄改/誤植的
	// 回傳金額被拿去當作實際入帳/延長 VIP 的依據，這條規則對 CompleteBindCard 同步路徑也一樣。
	if isRenewal {
		userID, periodEnd, err := h.settleVipRenewal(ctx, tx.OrderID, attemptID, settleVipRenewalParams{
			Plan:            plan,
			AmountCents:     tx.AmountCents,
			MerchantTradeNo: tradeNo,
			EcpayTradeNo:    data.OrderInfo.TradeNo,
			Gwsr:            gwsr,
			Raw:             raw,
		})
		if err != nil {
			log.Error().Err(err).Str("order_id", tx.OrderID).Str("merchant_trade_no", tradeNo).
				Msg("ecpay bind webhook: settle vip renewal failed — payment received but not fully applied, needs manual reconciliation")
			return false
		}
		// periodEnd 為零值代表 CAS 冪等 no-op（這筆已經被結算過，通常是排程本身的同步呼叫已先結算，
		// webhook 才晚到）——不重複發信。
		if !periodEnd.IsZero() && userID != "" && h.mail != nil {
			level, title, body := renewalSuccessMail(formatTaipei(periodEnd))
			if _, mErr := h.mail.InsertForUsers(ctx, []string{userID}, level, title, body, ""); mErr != nil {
				log.Warn().Err(mErr).Str("order_id", tx.OrderID).Msg("ecpay bind webhook: send renewal success mail failed")
			}
		}
		return true
	}

	if err := h.settleVipBindPayment(ctx, tx.OrderID, settleVipBindParams{
		Plan:             plan,
		AmountCents:      tx.AmountCents,
		MerchantTradeNo:  tradeNo,
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
	MerchantTradeNo  string // 我方交易編號（orders.payment_ref 統一存 "ECPay:"+此值，與既有 AIO 慣例一致）
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
	// payment_ref 統一存 "ECPay:"+我方 MerchantTradeNo（與既有 AIO Notify 慣例一致，見 payment.go；
	// 綠界端 TradeNo 另存 payment_transactions.ecpay_trade_no，客服對帳兩邊都查得到）。
	var userID string
	err = tx.QueryRow(ctx, `
		UPDATE orders SET status='paid', paid_at=NOW(), payment_ref=$2
		WHERE id=$1 AND status='pending'
		RETURNING user_id`, orderID, "ECPay:"+p.MerchantTradeNo).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		// 冪等 no-op 的兩種情況要分開對待（對抗式審查修正）：訂單已是 paid＝正常的重複通知，靜默
		// 返回即可；訂單處於 pending/paid 以外的狀態（如被新一次 Subscribe 作廢成 cancelled）卻收到
		// 成功扣款＝「錢已收、單不可結」，必須大聲記 log 供人工對帳退款，不能靜默吞掉。
		var st string
		if qErr := h.db.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1`, orderID).Scan(&st); qErr == nil && st != "paid" {
			log.Error().Str("order_id", orderID).Str("order_status", st).Str("merchant_trade_no", p.MerchantTradeNo).
				Msg("settle vip bind: charge received but order is not settleable — needs manual reconciliation/refund")
		}
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
	// ON CONFLICT 對應 uq_card_binding_active（partial unique：user_id+provider WHERE active）：
	// 正常路徑上面的 UPDATE 已把舊 active 標 deleted、這裡不會衝突；衝突只發生在「另一筆訂單的結算
	// 交易在本交易的快照之後才 commit 了它的 active 綁卡」的跨交易併發——此時直接把那筆 active 列
	// 原地更新成本次的新卡資訊（等價於 supersede，只是不留歷史列），避免 unique_violation 拖垮整包
	// 結算交易造成「已扣款卻 rollback 回 pending」（對抗式審查修正）。
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_card_bindings
			(user_id, provider, merchant_member_id, bind_card_id, card_last4, card_expiry_mm, card_expiry_yy, status)
		VALUES ($1, 'ecpay', $2, $3, NULLIF($4,''), NULLIF($5,''), NULLIF($6,''), 'active')
		ON CONFLICT (user_id, provider) WHERE status='active' DO UPDATE SET
			merchant_member_id = EXCLUDED.merchant_member_id,
			bind_card_id       = EXCLUDED.bind_card_id,
			card_last4         = EXCLUDED.card_last4,
			card_expiry_mm     = EXCLUDED.card_expiry_mm,
			card_expiry_yy     = EXCLUDED.card_expiry_yy,
			updated_at         = NOW()`,
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
