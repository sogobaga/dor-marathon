// 綠界站內付2.0（信用卡定期定額/幕後綁卡請款）伺服器對伺服器 API client。
//
// 涵蓋 5 支端點：
//   - GetTokenbyBindingCard   取得綁卡 Token（導客戶去綠界頁面完成綁卡）
//   - CreateBindCard          用 Token 建立綁定卡片，取得 BindCardID（可能落 3D 分支）
//   - CreatePaymentWithCardID 幕後用 BindCardID 請款（VIP 自動續扣款核心，本階段最關鍵的端點）
//   - GetMemberBindCard       查詢會員已綁定卡片列表
//   - DeleteMemberBindCard    解除綁定
//
// 與 ecpay_aes.go 的分工：本檔只負責「組 Request Data struct → AES 加密 → 組傳輸層 envelope →
// HTTP 呼叫 → 解傳輸層 envelope → AES 解密 → 解析 Response Data struct」，直接沿用 ecpayAESEncrypt/
// ecpayAESDecrypt，不重寫任何加解密邏輯。
//
// 本階段（Phase B）只做 outbound client，不含 ReturnURL webhook handler／DB 寫入／排程（Phase C/D）。
package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// BindClient 綠界站內付2.0 用戶端（單一特店憑證；若未來要多特店切換，比照 payment.go 的
// MultiConfig 模式在上層另包一層，本 client 本身保持單一憑證簡單）。
type BindClient struct {
	MerchantID string
	HashKey    string
	HashIV     string
	BaseURL    string // ecpg(-stage) domain：Token/建立交易/綁卡系列 API（見 ecpay_aes.go 常數註解）
	QueryURL   string // ecpayment(-stage) domain：查詢/請退款系列 API（QueryTrade 用這個，不是 BaseURL）
	HTTP       *http.Client
}

// NewBindClient 建立 BindClient；env=="prod" 用正式站，其餘（含空字串／stage／未知值）一律用測試站
// ——故障安全：設定寫錯或漏帶時不會誤打正式站真的扣到錢。BaseURL／QueryURL 依同一個 env 各自解析成
// 對應的兩個 domain（見 ecpay_aes.go 常數註解：站內付2.0 橫跨 ecpg／ecpayment 兩個不同 domain）。
func NewBindClient(env, merchantID, hashKey, hashIV string) *BindClient {
	base, query := ecpayBindStageURL, ecpayQueryStageURL
	if env == "prod" {
		base, query = ecpayBindProdURL, ecpayQueryProdURL
	}
	return &BindClient{
		MerchantID: merchantID,
		HashKey:    hashKey,
		HashIV:     hashIV,
		BaseURL:    base,
		QueryURL:   query,
		// 綠界建議站內付2.0 逾時設定 ≥30 秒（請款可能牽涉發卡行/收單風控判定），這裡抓 35s 留緩衝。
		HTTP: &http.Client{Timeout: 35 * time.Second},
	}
}

// --- 共用傳輸層 envelope ---

// bindReqEnvelope 站內付2.0 共用請求外層封包：{"MerchantID":..., "RqHeader":{"Timestamp":...}, "Data":"<AES base64>"}
type bindReqEnvelope struct {
	MerchantID string       `json:"MerchantID"`
	RqHeader   bindRqHeader `json:"RqHeader"`
	Data       string       `json:"Data"`
}

type bindRqHeader struct {
	// Timestamp 用 time.Now().Unix()：Unix 秒本身即為絕對時間、與時區無關，避免在無 tzdata 的
	// distroless 執行環境（本專案 API 服務的部署環境）誤用 time.LoadLocation 導致啟動或執行期出錯。
	Timestamp int64 `json:"Timestamp"`
}

// bindRespEnvelope 站內付2.0 共用回應外層封包：{"MerchantID":..., "RpHeader":{...}, "TransCode":int, "TransMsg":string, "Data":"<AES base64>"}
type bindRespEnvelope struct {
	MerchantID string       `json:"MerchantID"`
	RpHeader   bindRpHeader `json:"RpHeader"`
	TransCode  int          `json:"TransCode"`
	TransMsg   string       `json:"TransMsg"`
	Data       string       `json:"Data"`
}

type bindRpHeader struct {
	Timestamp int64 `json:"Timestamp"`
}

// BindTransportError 傳輸層錯誤（envelope 的 TransCode != 1）：代表這次 HTTP 呼叫本身在綠界端沒有
// 被正常受理／解密（例如簽章錯誤、封包格式錯誤），與「請求有確實送達並執行、但業務結果失敗」的
// BindBizError 是不同層級——呼叫端（Phase C/D）應分流處理：BindTransportError 通常代表串接本身
// 有問題（設定/簽章錯誤），BindBizError 才是「這張卡/這筆交易」本身的業務結果（如卡被拒、額度不足）。
type BindTransportError struct {
	TransCode int
	TransMsg  string
}

func (e *BindTransportError) Error() string {
	return fmt.Sprintf("ecpay bind: transport error TransCode=%d TransMsg=%q", e.TransCode, e.TransMsg)
}

// BindBizError 業務層錯誤（各端點 Data.RtnCode != 1）：代表請求有確實送達並執行，但業務結果失敗
// （如卡片被拒、額度不足、卡片已停用等）。各方法在回傳 BindBizError 的同時，也會回傳已解密/解析出的
// resp struct（而非 nil）——呼叫端仍可能需要 resp 內的其餘欄位（如已解出的 MerchantID 供記錄）。
type BindBizError struct {
	RtnCode int
	RtnMsg  string
}

func (e *BindBizError) Error() string {
	return fmt.Sprintf("ecpay bind: biz error RtnCode=%d RtnMsg=%q", e.RtnCode, e.RtnMsg)
}

// call 是共用的「加密 → 組傳輸層封包 → HTTP POST → 解傳輸層封包 → 解密」流程，打 c.BaseURL（ecpg
// domain）。絕大多數端點（Token/建立交易/綁卡系列）都用這個 domain，見 ecpay_aes.go 常數註解。
//
// ⚠️ 兩層狀態，順序不可顛倒：先檢查傳輸層 TransCode（步驟 4），確認傳輸層本身有效才進行 AES 解密；
// RtnCode（業務層）在 Data 解密、Unmarshal 進呼叫端傳入的 respData 之後，由各方法自行檢查——因為
// RtnCode 欄位定義在各端點自己的 respData struct 內，此處是泛用流程，拿不到那個欄位。
func (c *BindClient) call(ctx context.Context, path string, reqData, respData any) error {
	return c.callAt(ctx, c.BaseURL, path, reqData, respData)
}

// callAt 同 call，但可指定 baseURL——QueryTrade 等查詢系列端點在 ecpayment domain（與 c.BaseURL 的
// ecpg domain不同，見 BindClient.QueryURL 欄位註解），需要能覆寫 base，故把 call 的邏輯抽成本函式，
// call 本身變成 callAt(c.BaseURL, ...) 的薄包裝，不重複實作一份加解密/傳輸邏輯。
func (c *BindClient) callAt(ctx context.Context, baseURL, path string, reqData, respData any) error {
	plainJSON, err := json.Marshal(reqData)
	if err != nil {
		return fmt.Errorf("ecpay bind: marshal request data: %w", err)
	}
	encData, err := ecpayAESEncrypt(c.HashKey, c.HashIV, string(plainJSON))
	if err != nil {
		return fmt.Errorf("ecpay bind: encrypt request data: %w", err)
	}

	envelope := bindReqEnvelope{
		MerchantID: c.MerchantID,
		RqHeader:   bindRqHeader{Timestamp: time.Now().Unix()},
		Data:       encData,
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("ecpay bind: marshal request envelope: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ecpay bind: new http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return fmt.Errorf("ecpay bind: http request failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("ecpay bind: read response body (http status %d): %w", httpResp.StatusCode, err)
	}

	var respEnv bindRespEnvelope
	if err := json.Unmarshal(respBody, &respEnv); err != nil {
		return fmt.Errorf("ecpay bind: unmarshal response envelope (http status %d): %w", httpResp.StatusCode, err)
	}

	// 兩層狀態，第一層：傳輸層。TransCode != 1 代表這次呼叫在綠界端未被正常受理，此時 Data 欄位不可信
	// （可能為空或無法解密），直接回傳輸層錯誤，不嘗試往下解密。
	if respEnv.TransCode != 1 {
		return &BindTransportError{TransCode: respEnv.TransCode, TransMsg: respEnv.TransMsg}
	}

	decJSON, err := ecpayAESDecrypt(c.HashKey, c.HashIV, respEnv.Data)
	if err != nil {
		return fmt.Errorf("ecpay bind: decrypt response data: %w", err)
	}
	if err := json.Unmarshal([]byte(decJSON), respData); err != nil {
		return fmt.Errorf("ecpay bind: unmarshal response data: %w", err)
	}
	return nil
}

// isAlnumASCII 檢查字串是否僅由半形英數字元組成（CreatePayment 的 MerchantTradeNo 限制：
// 限半形英數 a-zA-Z0-9）。
func isAlnumASCII(s string) bool {
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// ================= (a) GetTokenbyBindingCard =================

// ConsumerInfo 消費者資訊，GetToken 與 CreatePayment 共用同一個形狀。Email/Phone 兩者在
// GetTokenbyBindingCard 規則裡「擇一必填」（見 GetToken 方法內的驗證）。
type ConsumerInfo struct {
	MerchantMemberID string `json:"MerchantMemberID"`
	Email            string `json:"Email,omitempty"`
	Phone            string `json:"Phone,omitempty"`
	Name             string `json:"Name,omitempty"`
	CountryCode      string `json:"CountryCode,omitempty"`
	Address          string `json:"Address,omitempty"`
}

// GetTokenOrderInfo GetToken 請求內的 OrderInfo（注意：CreditInstallment 在這裡是巢狀在 OrderInfo
// 內部，這點與 (c) CreatePayment 的 CreditInstallment 位於 Data 最外層不同，兩者不可混用）。
type GetTokenOrderInfo struct {
	MerchantTradeDate string `json:"MerchantTradeDate"`
	MerchantTradeNo   string `json:"MerchantTradeNo"`
	TotalAmount       int    `json:"TotalAmount"`
	TradeDesc         string `json:"TradeDesc"`
	ItemName          string `json:"ItemName"`
	ReturnURL         string `json:"ReturnURL"`
	CreditInstallment string `json:"CreditInstallment,omitempty"` // 綠界 String(20)，逗號分隔如 "3,6,12"；VIP 不分期一律留空
}

// GetTokenReq GetTokenbyBindingCard 請求 Data。
//
// ⚠️ OrderResultURL／CustomField 位於 Data 最外層，不在 OrderInfo 內——這是綠界文件裡最容易寫錯的
// 地方之一，務必保持這個扁平結構。
type GetTokenReq struct {
	PlatformID     string            `json:"PlatformID,omitempty"`
	MerchantID     string            `json:"MerchantID"`
	ConsumerInfo   ConsumerInfo      `json:"ConsumerInfo"`
	OrderInfo      GetTokenOrderInfo `json:"OrderInfo"`
	OrderResultURL string            `json:"OrderResultURL"`
	CustomField    string            `json:"CustomField,omitempty"`
}

// GetTokenResp GetTokenbyBindingCard 回應 Data。
type GetTokenResp struct {
	RtnCode         int    `json:"RtnCode"`
	RtnMsg          string `json:"RtnMsg"`
	PlatformID      string `json:"PlatformID,omitempty"`
	MerchantID      string `json:"MerchantID"`
	Token           string `json:"Token"`
	TokenExpireDate string `json:"TokenExpireDate"`
}

// GetToken 呼叫 GetTokenbyBindingCard：取得導客戶去綠界綁卡頁面用的一次性 Token。
// 綠界規定 ConsumerInfo.Email/Phone 擇一必填，這裡先做基本驗證，避免明知會被拒的請求還打出去。
func (c *BindClient) GetToken(ctx context.Context, req GetTokenReq) (*GetTokenResp, error) {
	if req.ConsumerInfo.Email == "" && req.ConsumerInfo.Phone == "" {
		return nil, fmt.Errorf("ecpay bind: GetToken requires ConsumerInfo.Email or ConsumerInfo.Phone (at least one)")
	}
	req.MerchantID = c.MerchantID

	var resp GetTokenResp
	if err := c.call(ctx, "/Merchant/GetTokenbyBindingCard", req, &resp); err != nil {
		return nil, err
	}
	if resp.RtnCode != 1 {
		return &resp, &BindBizError{RtnCode: resp.RtnCode, RtnMsg: resp.RtnMsg}
	}
	return &resp, nil
}

// ================= (b) CreateBindCard =================

// CreateBindCardReq CreateBindCard 請求 Data。
type CreateBindCardReq struct {
	PlatformID       string `json:"PlatformID,omitempty"`
	MerchantID       string `json:"MerchantID"`
	BindCardPayToken string `json:"BindCardPayToken"`
	MerchantMemberID string `json:"MerchantMemberID"`
}

// BindOrderInfo CreateBindCard 回應內的 OrderInfo（不走 3D 分支時欄位齊全；走 3D 分支時只有
// MerchantTradeNo 有值，其餘為零值——用 pointer 包整個 struct，讓呼叫端能用 nil 判斷「這個分支
// 有沒有回這個區塊」，但 OrderInfo 本身在兩分支都會出現，故不用 pointer 而是看內部欄位是否為空）。
type BindOrderInfo struct {
	MerchantTradeNo string  `json:"MerchantTradeNo"`
	TradeNo         string  `json:"TradeNo,omitempty"`
	TradeAmt        int     `json:"TradeAmt,omitempty"`
	TradeDate       string  `json:"TradeDate,omitempty"`
	PaymentType     string  `json:"PaymentType,omitempty"`
	PaymentDate     string  `json:"PaymentDate,omitempty"`
	TradeStatus     string  `json:"TradeStatus,omitempty"`
	ChargeFee       float64 `json:"ChargeFee,omitempty"`
	ProcessFee      float64 `json:"ProcessFee,omitempty"`
}

// BindCardInfo CreateBindCard 回應內的 CardInfo（只在不走 3D 分支時出現）。
type BindCardInfo struct {
	AuthCode    string `json:"AuthCode,omitempty"`
	Gwsr        int    `json:"Gwsr,omitempty"`
	Card6No     string `json:"Card6No,omitempty"`
	Card4No     string `json:"Card4No,omitempty"`
	CardValidYY string `json:"CardValidYY,omitempty"`
	CardValidMM string `json:"CardValidMM,omitempty"`
	Eci         int    `json:"Eci,omitempty"`
	IssuingBank string `json:"IssuingBank,omitempty"`
}

// ThreeDInfo 3D 驗證資訊，CreateBindCard／CreatePayment 回應共用同一個形狀。
type ThreeDInfo struct {
	ThreeDURL string `json:"ThreeDURL,omitempty"`
}

// CreateBindCardResp CreateBindCard 回應 Data（兩分支共用同一個 struct，用 pointer 欄位讓
// 「這個分支沒回這塊」能用 nil 表達；OrderInfo 兩分支都會出現故非 pointer，用 BindCardID=="" 或
// ThreeDInfo!=nil 判斷分支——見 Is3D）。
type CreateBindCardResp struct {
	RtnCode          int            `json:"RtnCode"`
	RtnMsg           string         `json:"RtnMsg"`
	MerchantID       string         `json:"MerchantID"`
	MerchantMemberID string         `json:"MerchantMemberID,omitempty"`
	BindCardID       string         `json:"BindCardID,omitempty"`
	IsSameCard       bool           `json:"IsSameCard,omitempty"`
	OrderInfo        *BindOrderInfo `json:"OrderInfo,omitempty"`
	CardInfo         *BindCardInfo  `json:"CardInfo,omitempty"`
	ThreeDInfo       *ThreeDInfo    `json:"ThreeDInfo,omitempty"`
}

// Is3D 回傳這次 CreateBindCard 回應是否落入 3D 驗證分支（尚未真正綁定成功，需另外導客戶去
// ThreeDInfo.ThreeDURL 完成驗證後才會有 BindCardID）。
//
// ⚠️ 綠界文件對這支端點沒有提供明確的 Need3D／布林欄位可判斷分支，這裡依文件給的兩分支欄位範例
// 判斷：ThreeDInfo 有值，或 BindCardID 為空，即視為落入 3D 分支（兩個條件邏輯上應等價，用 OR 是
// 為了在任一方向的欄位缺漏時仍能正確判斷，較為保守）。
func (r *CreateBindCardResp) Is3D() bool {
	return (r.ThreeDInfo != nil && r.ThreeDInfo.ThreeDURL != "") || r.BindCardID == ""
}

// CreateBindCard 呼叫 CreateBindCard：用 GetToken 拿到的 BindCardPayToken 建立綁定卡片。
// RtnCode==1 即視為呼叫成功（無論落哪個分支——3D 分支仍是「正常流程的一步」，不是錯誤；呼叫端應
// 用 resp.Is3D() 判斷是否需要再導轉使用者去完成 3D 驗證，這是 Phase C 前端流程的責任，本階段只負責
// 把兩分支都正確解析出來）。
func (c *BindClient) CreateBindCard(ctx context.Context, req CreateBindCardReq) (*CreateBindCardResp, error) {
	req.MerchantID = c.MerchantID

	var resp CreateBindCardResp
	if err := c.call(ctx, "/Merchant/CreateBindCard", req, &resp); err != nil {
		return nil, err
	}
	if resp.RtnCode != 1 {
		return &resp, &BindBizError{RtnCode: resp.RtnCode, RtnMsg: resp.RtnMsg}
	}
	return &resp, nil
}

// ================= (c) CreatePaymentWithCardID（幕後請款核心） =================

// PaymentOrderInfoReq CreatePayment 請求內的 OrderInfo。
type PaymentOrderInfoReq struct {
	MerchantTradeDate string `json:"MerchantTradeDate"`
	MerchantTradeNo   string `json:"MerchantTradeNo"` // 限半形英數 a-zA-Z0-9，≤20 字
	TotalAmount       int    `json:"TotalAmount"`     // ≤200000
	ReturnURL         string `json:"ReturnURL"`
	TradeDesc         string `json:"TradeDesc"`
	ItemName          string `json:"ItemName"`
}

// CreatePaymentReq CreatePaymentWithCardID 請求 Data。
//
// ⚠️ CreditInstallment 在這裡位於 Data 最外層（與 (a) GetToken 巢狀在 OrderInfo 內不同，見上）。
type CreatePaymentReq struct {
	PlatformID        string              `json:"PlatformID,omitempty"`
	MerchantID        string              `json:"MerchantID"`
	BindCardID        string              `json:"BindCardID"`
	OrderInfo         PaymentOrderInfoReq `json:"OrderInfo"`
	ConsumerInfo      ConsumerInfo        `json:"ConsumerInfo"`
	CreditInstallment string              `json:"CreditInstallment,omitempty"` // 綠界 String(20)，逗號分隔如 "3,6,12"；VIP 不分期一律留空
	// Need3D 幕後（無使用者互動）請款固定填 0——這支方法（CreatePayment）本身就是「幕後請款核心」，
	// 場景就是背景自動扣款（VIP 續訂），沒有使用者在場可以跳轉去完成 3D 驗證頁面；因此 CreatePayment
	// 方法內部固定把這個欄位覆寫成 0（見下方方法實作），不論呼叫端傳了什麼值——避免任何未來呼叫端
	// 誤用這個欄位觸發需要前景導轉的流程。若日後真的需要 Need3D=1 的前景流程，應該是另一支獨立方法，
	// 不應該共用這支已明確定位為「幕後」的方法。
	Need3D         int    `json:"Need3D"`
	OrderResultURL string `json:"OrderResultURL,omitempty"` // 僅 Need3D=1 才需要；本方法固定 Need3D=0，通常用不到
	CustomField    string `json:"CustomField,omitempty"`
}

// PaymentOrderInfoResp CreatePayment 回應內的 OrderInfo。
type PaymentOrderInfoResp struct {
	MerchantTradeNo string  `json:"MerchantTradeNo"`
	TradeNo         string  `json:"TradeNo,omitempty"`
	TradeAmt        int     `json:"TradeAmt,omitempty"`
	TradeDate       string  `json:"TradeDate,omitempty"`
	PaymentType     string  `json:"PaymentType,omitempty"`
	PaymentDate     string  `json:"PaymentDate,omitempty"`
	ChargeFee       float64 `json:"ChargeFee,omitempty"`
	ProcessFee      float64 `json:"ProcessFee,omitempty"`
	TradeStatus     string  `json:"TradeStatus,omitempty"`
}

// PaymentCardInfo CreatePayment 回應內的 CardInfo（3DS 風險判定要求 3D 驗證時不會出現，見
// CreatePaymentResp/CreatePayment 方法註解）。
type PaymentCardInfo struct {
	AuthCode        string `json:"AuthCode,omitempty"`
	Gwsr            int    `json:"Gwsr,omitempty"`
	ProcessDate     string `json:"ProcessDate,omitempty"`
	Amount          int    `json:"Amount,omitempty"`
	Card6No         string `json:"Card6No,omitempty"`
	Card4No         string `json:"Card4No,omitempty"`
	IssuingBank     string `json:"IssuingBank,omitempty"`
	IssuingBankCode string `json:"IssuingBankCode,omitempty"`
	Eci             int    `json:"Eci,omitempty"`
}

// CreatePaymentResp CreatePaymentWithCardID 回應 Data。
type CreatePaymentResp struct {
	RtnCode     int                   `json:"RtnCode"`
	RtnMsg      string                `json:"RtnMsg"`
	PlatformID  string                `json:"PlatformID,omitempty"`
	MerchantID  string                `json:"MerchantID"`
	OrderInfo   *PaymentOrderInfoResp `json:"OrderInfo,omitempty"`
	CardInfo    *PaymentCardInfo      `json:"CardInfo,omitempty"`
	ThreeDInfo  *ThreeDInfo           `json:"ThreeDInfo,omitempty"` // 僅風險判定要求 3D 時出現
	CustomField string                `json:"CustomField,omitempty"`
}

// CreatePayment 呼叫 CreatePaymentWithCardID：用已綁定的 BindCardID 幕後請款（VIP 自動續扣款核心）。
//
// ⚠️ 2025/8/1 起綠界 3DS2.0 風險判定：即使本方法固定送 Need3D=0，回應仍可能出現 ThreeDInfo.ThreeDURL
// （RtnCode 依然是 1）——此時 CardInfo 與 OrderInfo.TradeNo 會缺，代表這筆幕後請款「並未真正完成
// 扣款」，發卡行/收單風控判定這筆交易需要走 3D 驗證，但幕後場景沒有使用者在場可以完成。這種情況
// 絕不可當請款成功（否則 Phase C/D 若只看 RtnCode==1 就判定扣款成功，會產生「系統認為已收款、實際
// 未收款」的資料不一致，對 VIP 續訂這種金流核心是嚴重風險）。因此本方法在 RtnCode==1 之後，仍會
// 額外檢查 ThreeDInfo；若命中 3D 分支，回傳可用 errors.Is 辨識的 ErrCreatePayment3DRequired（連同已
// 解析出的 resp 一起回傳，讓呼叫端仍能取得 ThreeDInfo.ThreeDURL），讓 Phase D 能據此通知使用者
// 補刷／到前景手動完成 3D 驗證，而不是誤判為扣款成功。
func (c *BindClient) CreatePayment(ctx context.Context, req CreatePaymentReq) (*CreatePaymentResp, error) {
	if req.OrderInfo.MerchantTradeNo == "" || len(req.OrderInfo.MerchantTradeNo) > 20 || !isAlnumASCII(req.OrderInfo.MerchantTradeNo) {
		return nil, fmt.Errorf("ecpay bind: CreatePayment OrderInfo.MerchantTradeNo must be 1-20 alphanumeric ASCII characters (got %q)", req.OrderInfo.MerchantTradeNo)
	}
	if req.OrderInfo.TotalAmount <= 0 || req.OrderInfo.TotalAmount > 200000 {
		return nil, fmt.Errorf("ecpay bind: CreatePayment OrderInfo.TotalAmount must be in (0, 200000], got %d", req.OrderInfo.TotalAmount)
	}
	req.MerchantID = c.MerchantID
	req.Need3D = 0 // 固定 0：本方法場景就是幕後（無使用者互動）請款，見上方欄位註解

	var resp CreatePaymentResp
	if err := c.call(ctx, "/Merchant/CreatePaymentWithCardID", req, &resp); err != nil {
		return nil, err
	}
	if resp.RtnCode != 1 {
		return &resp, &BindBizError{RtnCode: resp.RtnCode, RtnMsg: resp.RtnMsg}
	}
	// RtnCode==1 但仍被要求 3D 驗證：不可當成功（見方法註解）。
	if resp.ThreeDInfo != nil && resp.ThreeDInfo.ThreeDURL != "" {
		return &resp, ErrCreatePayment3DRequired
	}
	return &resp, nil
}

// ErrCreatePayment3DRequired 是 CreatePayment 在遇到 3DS2.0 風險判定分支時回傳的哨兵錯誤
// （同一個 *BindBizError 實例，可用 errors.Is(err, ErrCreatePayment3DRequired) 辨識）。
// RtnCode 用 -1 表示「非綠界原生 RtnCode，是本 client 自行判定的複合結果」，避免與任何真實
// 綠界 RtnCode 數值撞號。
var ErrCreatePayment3DRequired = &BindBizError{
	RtnCode: -1,
	RtnMsg:  "3DS required (unattended charge cannot complete)",
}

// ================= (d) GetMemberBindCard =================

// GetMemberBindCardReq GetMemberBindCard 請求 Data。
type GetMemberBindCardReq struct {
	PlatformID       string `json:"PlatformID,omitempty"`
	MerchantID       string `json:"MerchantID"`
	MerchantMemberID string `json:"MerchantMemberID"`
	MerchantTradeNo  string `json:"MerchantTradeNo,omitempty"`
}

// BindCardListItem GetMemberBindCard 回應內單筆已綁定卡片。
type BindCardListItem struct {
	Card6No     string `json:"Card6No"`
	Card4No     string `json:"Card4No"`
	CardValidYY string `json:"CardValidYY"`
	CardValidMM string `json:"CardValidMM"`
	BindCardID  string `json:"BindCardID"`
}

// GetMemberBindCardResp GetMemberBindCard 回應 Data。
type GetMemberBindCardResp struct {
	RtnCode          int                `json:"RtnCode"`
	RtnMsg           string             `json:"RtnMsg"`
	MerchantID       string             `json:"MerchantID"`
	MerchantMemberID string             `json:"MerchantMemberID"`
	BindCardList     []BindCardListItem `json:"BindCardList"`
}

// GetMemberBindCard 呼叫 GetMemberBindCard：查詢會員已綁定的卡片列表。
func (c *BindClient) GetMemberBindCard(ctx context.Context, merchantMemberID string) (*GetMemberBindCardResp, error) {
	req := GetMemberBindCardReq{
		MerchantID:       c.MerchantID,
		MerchantMemberID: merchantMemberID,
	}
	var resp GetMemberBindCardResp
	if err := c.call(ctx, "/Merchant/GetMemberBindCard", req, &resp); err != nil {
		return nil, err
	}
	if resp.RtnCode != 1 {
		return &resp, &BindBizError{RtnCode: resp.RtnCode, RtnMsg: resp.RtnMsg}
	}
	return &resp, nil
}

// ================= (e) DeleteMemberBindCard =================

// DeleteMemberBindCardReq DeleteMemberBindCard 請求 Data。
type DeleteMemberBindCardReq struct {
	PlatformID string `json:"PlatformID,omitempty"`
	MerchantID string `json:"MerchantID"`
	BindCardID string `json:"BindCardID"`
}

// DeleteMemberBindCardResp DeleteMemberBindCard 回應 Data。
type DeleteMemberBindCardResp struct {
	RtnCode int    `json:"RtnCode"`
	RtnMsg  string `json:"RtnMsg"`
}

// DeleteMemberBindCard 呼叫 DeleteMemberBindCard：解除卡片綁定。
func (c *BindClient) DeleteMemberBindCard(ctx context.Context, bindCardID string) (*DeleteMemberBindCardResp, error) {
	req := DeleteMemberBindCardReq{
		MerchantID: c.MerchantID,
		BindCardID: bindCardID,
	}
	var resp DeleteMemberBindCardResp
	if err := c.call(ctx, "/Merchant/DeleteMemberBindCard", req, &resp); err != nil {
		return nil, err
	}
	if resp.RtnCode != 1 {
		return &resp, &BindBizError{RtnCode: resp.RtnCode, RtnMsg: resp.RtnMsg}
	}
	return &resp, nil
}

// ================= (f) QueryTrade（Phase D 對抗式審查修正：主動收斂 unknown 扣款狀態） =================
//
// 端點依據：guides/02-payment-ecpg.md 及官方 9083.md（web_fetch 2026-08 校準）——查詢訂單走
// ecpayment(-stage).ecpay.com.tw/1.0.0/Cashier/QueryTrade（不是 ecpg domain、也不是 /Merchant/
// 前綴；本任務原始規格猜測的端點路徑與 domain 有誤，這裡改以官方文件為準），封包格式仍是與其餘 5 支
// 端點相同的三層 AES-JSON envelope（MerchantID+RqHeader+Data），故直接複用 callAt，只是傳入
// c.QueryURL 而非 c.BaseURL。

// QueryTradeReq QueryTrade 請求 Data：MerchantID/MerchantTradeNo 必填，PlatformID 一般商店留空。
type QueryTradeReq struct {
	PlatformID      string `json:"PlatformID,omitempty"`
	MerchantID      string `json:"MerchantID"`
	MerchantTradeNo string `json:"MerchantTradeNo"`
}

// QueryTradeOrderInfo QueryTrade 回應內的 OrderInfo（欄位巢狀在 OrderInfo 內，同 CreatePayment 回應
// 的形狀慣例——見官方文件範例 JSON）。
type QueryTradeOrderInfo struct {
	MerchantTradeNo string `json:"MerchantTradeNo,omitempty"`
	TradeNo         string `json:"TradeNo,omitempty"`
	TradeAmt        int    `json:"TradeAmt,omitempty"`
	TradeDate       string `json:"TradeDate,omitempty"`
	PaymentType     string `json:"PaymentType,omitempty"`
	PaymentDate     string `json:"PaymentDate,omitempty"`
	// TradeStatus "0"=未付款 "1"=已付款（官方文件明確定義的關鍵欄位——QueryTrade 存在的唯一目的
	// 就是讀這個欄位，見 vip_renewal.go classifyQueryTradeResult）。
	TradeStatus string `json:"TradeStatus,omitempty"`
}

// QueryTradeResp QueryTrade 回應 Data。OrderInfo 用 pointer：文件未明確定義「MerchantTradeNo 查無
// 此筆交易」時的回應形狀，保守起見允許它整塊缺席（RtnCode!=1 時很可能沒有 OrderInfo）。
type QueryTradeResp struct {
	RtnCode    int                  `json:"RtnCode"`
	RtnMsg     string               `json:"RtnMsg"`
	MerchantID string               `json:"MerchantID,omitempty"`
	OrderInfo  *QueryTradeOrderInfo `json:"OrderInfo,omitempty"`
}

// QueryTrade 呼叫 QueryTrade：查詢一筆訂單目前的真實付款狀態。Phase D 續約排程用它主動收斂「傳輸層
// 逾時/斷線導致我方不確定是否已扣款成功」的舊訂單（見 vip_renewal.go 檔頭「unknown 收斂」設計），
// 取代舊版「無限 pending 等 webhook」——舊版一旦某次續約請款逾時，訂單會卡在 pending 永遠擋住
// uq_orders_pending_vip，隔天所有候選都無法真正重試扣款。
//
// RtnCode!=1（業務層失敗，例如查無此筆訂單、MerchantID 不符等）一律回傳 *BindBizError，與其餘 5 支
// 端點同一慣例；呼叫端（vip_renewal.go classifyQueryTradeResult）把它與「RtnCode==1 但
// TradeStatus=="0"（確定未付款）」同歸一類（未付/查無），因為兩者對「該不該作廢舊單重試」的決策
// 完全相同；只有傳輸層錯誤（BindTransportError／網路錯誤）才是真正「查詢本身失敗、狀態依然未知」，
// 需要與前兩者分開保守處理（不可清空重試，否則若舊單其實已扣款成功會變成雙扣）。
func (c *BindClient) QueryTrade(ctx context.Context, merchantTradeNo string) (*QueryTradeResp, error) {
	if merchantTradeNo == "" {
		return nil, fmt.Errorf("ecpay bind: QueryTrade requires a non-empty merchantTradeNo")
	}
	req := QueryTradeReq{
		MerchantID:      c.MerchantID,
		MerchantTradeNo: merchantTradeNo,
	}
	var resp QueryTradeResp
	if err := c.callAt(ctx, c.QueryURL, "/1.0.0/Cashier/QueryTrade", req, &resp); err != nil {
		return nil, err
	}
	if resp.RtnCode != 1 {
		return &resp, &BindBizError{RtnCode: resp.RtnCode, RtnMsg: resp.RtnMsg}
	}
	return &resp, nil
}
