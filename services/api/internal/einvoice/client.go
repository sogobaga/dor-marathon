package einvoice

import (
	"math"
	"sort"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/payment"
)

// apiRevision 綠界電子發票 API RqHeader.Revision 固定值（官方文件現行版本）。
const apiRevision = "3.0.0"

// Client 綠界 B2C 電子發票 API 用戶端（單一特店憑證）。
//
// 只負責「組 Request Data struct → AES 加密 → 組傳輸層 envelope → HTTP 呼叫 → 解傳輸層 envelope →
// AES 解密 → 解析 Response Data struct」，加解密直接沿用 payment.AESEncrypt/AESDecrypt——比照
// internal/payment/ecpay_bind.go BindClient 的分工方式，本檔不重寫任何加解密邏輯。
//
// ⚠️ 本 client 不知道呼叫端的 order_id/業務上下文（純 HTTP client），因此只在這裡記錄
// path/TransCode/TransMsg/RtnCode/RtnMsg 這些協定層資訊；order_id 層級的關聯 log 由呼叫端
// （issuer.go）在取得結果後自行記錄，兩層 log 合起來才是完整的稽核軌跡。絕不記錄 HashKey/HashIV
// 或解密後的完整客戶資料。
type Client struct {
	MerchantID string
	HashKey    string
	HashIV     string
	BaseURL    string
	HTTP       *http.Client
}

// NewClient 依 Config 建立 Client。HTTP timeout 20 秒（見 PRODUCT DECISIONS #7）。
func NewClient(cfg Config) *Client {
	return &Client{
		MerchantID: cfg.MerchantID,
		HashKey:    cfg.HashKey,
		HashIV:     cfg.HashIV,
		BaseURL:    cfg.BaseURL(),
		HTTP:       &http.Client{Timeout: 20 * time.Second},
	}
}

// --- 共用傳輸層 envelope ---

type reqEnvelope struct {
	MerchantID string   `json:"MerchantID"`
	RqHeader   rqHeader `json:"RqHeader"`
	Data       string   `json:"Data"`
}

type rqHeader struct {
	// Timestamp 用 time.Now().Unix()：Unix 秒本身即為絕對時間、與時區無關，避免無 tzdata 的
	// distroless 執行環境誤用 time.LoadLocation 導致出錯（同 payment.BindClient 慣例）。必須落在
	// 綠界伺服器時間 ±10 分鐘內，見官方規格。
	Timestamp int64  `json:"Timestamp"`
	Revision  string `json:"Revision"`
}

type respEnvelope struct {
	// MerchantID／TransCode／RpHeader.Timestamp 一律用容錯型別：正式環境實測（2026-09-06 第一張手動開立）
	// 綠界回的外層 MerchantID 是 JSON 數字 3504994 而非字串（測試環境文件寫 String(10)），嚴格型別會在
	// http 200 時整包 unmarshal 失敗、把已送出的 Issue 誤判成失敗。
	MerchantID FlexString `json:"MerchantID"`
	RpHeader   rpHeader   `json:"RpHeader"`
	TransCode  FlexInt    `json:"TransCode"`
	TransMsg   string   `json:"TransMsg"`
	Data       string   `json:"Data"`
}

type rpHeader struct {
	Timestamp FlexInt `json:"Timestamp"`
}

// FlexString 容錯解碼「文件說是字串、實際可能是 JSON 數字」的欄位（如回應外層 MerchantID）。
type FlexString string

func (f *FlexString) UnmarshalJSON(b []byte) error {
	t := strings.TrimSpace(string(b))
	if t == "null" || t == "" {
		*f = ""
		return nil
	}
	if strings.HasPrefix(t, `"`) {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*f = FlexString(v)
		return nil
	}
	*f = FlexString(t) // 數字／布林：原文字面值
	return nil
}

// jsonKind 回傳 JSON 值的粗略型別名（string/number/bool/null/object/array），供 decode 失敗時記 log 用。
func jsonKind(v json.RawMessage) string {
	t := strings.TrimSpace(string(v))
	switch {
	case t == "":
		return "empty"
	case strings.HasPrefix(t, `"`):
		return "string"
	case strings.HasPrefix(t, "{"):
		return "object"
	case strings.HasPrefix(t, "["):
		return "array"
	case t == "true" || t == "false":
		return "bool"
	case t == "null":
		return "null"
	default:
		return "number"
	}
}

// FlexInt 容錯解碼綠界回應中「有時是 JSON 數字、有時是加引號字串」的整數欄位（RtnCode 等）——官方
// 文件定義為 int，但已知會回傳字串型別，若直接用 int 解碼會整包 Unmarshal 失敗，把「其實已成功」的
// 回應誤判成解析錯誤。空字串/null 視為 0。
type FlexInt int

func (n *FlexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		// 綠界金額類欄位偶爾帶小數（如 50.0）：整數解析失敗再試浮點並四捨五入
		f, ferr := strconv.ParseFloat(s, 64)
		if ferr != nil {
			return fmt.Errorf("einvoice: expected integer RtnCode-like field, got %q: %w", s, err)
		}
		v = int(math.Round(f))
	}
	*n = FlexInt(v)
	return nil
}

// FlexNum 容錯解碼數字欄位（數量／單價／小計可含小數；也可能被加引號）。
type FlexNum float64

func (n *FlexNum) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("einvoice: expected numeric field, got %q: %w", s, err)
	}
	*n = FlexNum(f)
	return nil
}

// APIError 電子發票 API 呼叫的錯誤。
//
// TransCode!=1：傳輸層錯誤——這次 HTTP 呼叫本身未被綠界正常受理/解密（簽章錯誤、封包格式錯誤、
// RqHeader.Timestamp 超出±10分鐘等），Data 不可信，RtnCode/RtnMsg 恆為零值。
// TransCode==1 但 RtnCode!=1：業務層錯誤——請求已送達並執行，但這筆發票/作廢/折讓/查詢本身失敗
// （例如統編格式錯誤、發票已作廢、查無此筆）。
//
// 兩層合併成一個型別（不像 payment.BindTransportError/BindBizError 分成兩型別）：呼叫端
// （issuer.go）幾乎都是「兩層資訊一起記 log/組告警訊息」，用同一個型別更精簡；仍可用 TransCode==1
// 分辨究竟是哪一層失敗。
type APIError struct {
	TransCode int
	RtnCode   int
	RtnMsg    string
}

func (e *APIError) Error() string {
	if e.TransCode != 1 {
		return fmt.Sprintf("ecpay einvoice: transport error TransCode=%d", e.TransCode)
	}
	return fmt.Sprintf("ecpay einvoice: RtnCode=%d RtnMsg=%q", e.RtnCode, e.RtnMsg)
}

// call 共用的「加密 → 組傳輸層封包 → HTTP POST → 解傳輸層封包 → 解密」流程。
//
// ⚠️ 兩層狀態，順序不可顛倒：先檢查傳輸層 TransCode，確認傳輸層本身有效才進行 AES 解密；
// RtnCode（業務層）定義在各端點自己的 respData struct 內，此處是泛用流程拿不到，由各方法呼叫完
// call() 之後自行檢查。
func (c *Client) call(ctx context.Context, path string, reqData, respData any) error {
	plainJSON, err := json.Marshal(reqData)
	if err != nil {
		return fmt.Errorf("einvoice: marshal request data: %w", err)
	}
	encData, err := payment.AESEncrypt(c.HashKey, c.HashIV, string(plainJSON))
	if err != nil {
		return fmt.Errorf("einvoice: encrypt request data: %w", err)
	}

	envelope := reqEnvelope{
		MerchantID: c.MerchantID,
		RqHeader:   rqHeader{Timestamp: time.Now().Unix(), Revision: apiRevision},
		Data:       encData,
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("einvoice: marshal request envelope: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("einvoice: new http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return fmt.Errorf("einvoice: http request failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("einvoice: read response body (http status %d): %w", httpResp.StatusCode, err)
	}

	var respEnv respEnvelope
	if err := json.Unmarshal(respBody, &respEnv); err != nil {
		return fmt.Errorf("einvoice: unmarshal response envelope (http status %d): %w", httpResp.StatusCode, err)
	}

	if int(respEnv.TransCode) != 1 {
		log.Info().Str("path", path).Int("trans_code", int(respEnv.TransCode)).Str("trans_msg", respEnv.TransMsg).
			Msg("einvoice: ecpay call transport error")
		return &APIError{TransCode: int(respEnv.TransCode), RtnMsg: respEnv.TransMsg}
	}

	decJSON, err := payment.AESDecrypt(c.HashKey, c.HashIV, respEnv.Data)
	if err != nil {
		return fmt.Errorf("einvoice: decrypt response data: %w", err)
	}
	if err := json.Unmarshal([]byte(decJSON), respData); err != nil {
		// 只記欄位名不記值（Data 含客戶姓名/Email）；欄位名足以判斷是哪個欄位型別對不上
		var probe map[string]json.RawMessage
		_ = json.Unmarshal([]byte(decJSON), &probe)
		keys := make([]string, 0, len(probe))
		for k, v := range probe {
			keys = append(keys, k+":"+jsonKind(v))
		}
		sort.Strings(keys)
		log.Warn().Str("path", path).Strs("fields", keys).Err(err).Msg("einvoice: response data decode failed")
		return fmt.Errorf("einvoice: unmarshal response data: %w", err)
	}
	return nil
}

// ================= Issue =================

// IssueItem /B2CInvoice/Issue Data.Items 單筆明細。本系統一律只送一行（見 mapping.go）。
type IssueItem struct {
	ItemSeq     int    `json:"ItemSeq"`
	ItemName    string `json:"ItemName"`
	ItemCount   int    `json:"ItemCount"`
	ItemWord    string `json:"ItemWord"`
	ItemPrice   int    `json:"ItemPrice"`
	ItemTaxType string `json:"ItemTaxType"`
	ItemAmount  int    `json:"ItemAmount"`
}

// IssueReq /B2CInvoice/Issue 請求 Data。欄位/規則見官方文件（fetched 2026-09-06，見任務規格
// EXISTING STATE 區塊）；由 mapping.go BuildIssueRequest 組出，此處只是傳輸層形狀。
type IssueReq struct {
	MerchantID         string      `json:"MerchantID"`
	RelateNumber       string      `json:"RelateNumber"`
	CustomerID         string      `json:"CustomerID,omitempty"`
	CustomerIdentifier string      `json:"CustomerIdentifier,omitempty"`
	CustomerName       string      `json:"CustomerName,omitempty"`
	CustomerAddr       string      `json:"CustomerAddr,omitempty"`
	CustomerPhone      string      `json:"CustomerPhone,omitempty"`
	CustomerEmail      string      `json:"CustomerEmail,omitempty"`
	Print              string      `json:"Print"`
	Donation           string      `json:"Donation"`
	LoveCode           string      `json:"LoveCode,omitempty"`
	CarrierType        string      `json:"CarrierType"`
	CarrierNum         string      `json:"CarrierNum,omitempty"`
	TaxType            string      `json:"TaxType"`
	SalesAmount        int         `json:"SalesAmount"`
	InvoiceRemark      string      `json:"InvoiceRemark,omitempty"`
	Vat                string      `json:"vat,omitempty"`
	Items              []IssueItem `json:"Items"`
	InvType            string      `json:"InvType"`
}

// IssueResp /B2CInvoice/Issue 回應 Data。
type IssueResp struct {
	RtnCode      FlexInt `json:"RtnCode"`
	RtnMsg       string  `json:"RtnMsg"`
	InvoiceNo    string  `json:"InvoiceNo"`
	InvoiceDate  string  `json:"InvoiceDate"`
	RandomNumber string  `json:"RandomNumber"`
}

// Issue 呼叫 /B2CInvoice/Issue 開立發票。RtnCode!=1 時仍回傳已解析出的 resp（呼叫端可能需要
// RtnMsg 以外的欄位），同時回傳 *APIError。
func (c *Client) Issue(ctx context.Context, req IssueReq) (*IssueResp, error) {
	req.MerchantID = c.MerchantID
	var resp IssueResp
	if err := c.call(ctx, "/B2CInvoice/Issue", req, &resp); err != nil {
		return nil, err
	}
	log.Info().Str("path", "/B2CInvoice/Issue").Str("relate_number", req.RelateNumber).
		Int("rtn_code", int(resp.RtnCode)).Str("rtn_msg", resp.RtnMsg).Msg("einvoice: ecpay call")
	if int(resp.RtnCode) != 1 {
		return &resp, &APIError{TransCode: 1, RtnCode: int(resp.RtnCode), RtnMsg: resp.RtnMsg}
	}
	return &resp, nil
}

// ================= Invalid（作廢） =================

// InvalidReq /B2CInvoice/Invalid 請求 Data。
type InvalidReq struct {
	MerchantID  string `json:"MerchantID"`
	InvoiceNo   string `json:"InvoiceNo"`
	InvoiceDate string `json:"InvoiceDate"` // yyyy-MM-dd
	Reason      string `json:"Reason"`
}

// InvalidResp /B2CInvoice/Invalid 回應 Data。
type InvalidResp struct {
	RtnCode   FlexInt `json:"RtnCode"`
	RtnMsg    string  `json:"RtnMsg"`
	InvoiceNo string  `json:"InvoiceNo"`
}

// Invalid 呼叫 /B2CInvoice/Invalid 作廢發票。
func (c *Client) Invalid(ctx context.Context, req InvalidReq) (*InvalidResp, error) {
	req.MerchantID = c.MerchantID
	var resp InvalidResp
	if err := c.call(ctx, "/B2CInvoice/Invalid", req, &resp); err != nil {
		return nil, err
	}
	log.Info().Str("path", "/B2CInvoice/Invalid").Str("invoice_no", req.InvoiceNo).
		Int("rtn_code", int(resp.RtnCode)).Str("rtn_msg", resp.RtnMsg).Msg("einvoice: ecpay call")
	if int(resp.RtnCode) != 1 {
		return &resp, &APIError{TransCode: 1, RtnCode: int(resp.RtnCode), RtnMsg: resp.RtnMsg}
	}
	return &resp, nil
}

// ================= Allowance（折讓） =================

// AllowanceItem /B2CInvoice/Allowance 請求 Data.Items 單筆明細。
type AllowanceItem struct {
	ItemSeq     int    `json:"ItemSeq"`
	ItemName    string `json:"ItemName"`
	ItemCount   int    `json:"ItemCount"`
	ItemWord    string `json:"ItemWord"`
	ItemPrice   int    `json:"ItemPrice"`
	ItemTaxType string `json:"ItemTaxType"`
	ItemAmount  int    `json:"ItemAmount"`
}

// AllowanceReq /B2CInvoice/Allowance 請求 Data。
type AllowanceReq struct {
	MerchantID      string          `json:"MerchantID"`
	InvoiceNo       string          `json:"InvoiceNo"`
	InvoiceDate     string          `json:"InvoiceDate"` // yyyy-MM-dd
	AllowanceNotify string          `json:"AllowanceNotify"`
	CustomerName    string          `json:"CustomerName,omitempty"`
	NotifyMail      string          `json:"NotifyMail,omitempty"`
	NotifyPhone     string          `json:"NotifyPhone,omitempty"`
	AllowanceAmount int             `json:"AllowanceAmount"`
	Reason          string          `json:"Reason,omitempty"`
	Items           []AllowanceItem `json:"Items"`
}

// AllowanceResp /B2CInvoice/Allowance 回應 Data。
type AllowanceResp struct {
	RtnCode              FlexInt `json:"RtnCode"`
	RtnMsg               string  `json:"RtnMsg"`
	IAAllowNo            string  `json:"IA_Allow_No"`
	IAInvoiceNo          string  `json:"IA_Invoice_No"`
	IADate               string  `json:"IA_Date"`
	IARemainAllowanceAmt FlexInt `json:"IA_Remain_Allowance_Amt"`
}

// Allowance 呼叫 /B2CInvoice/Allowance 開立折讓（退款用）。
func (c *Client) Allowance(ctx context.Context, req AllowanceReq) (*AllowanceResp, error) {
	req.MerchantID = c.MerchantID
	var resp AllowanceResp
	if err := c.call(ctx, "/B2CInvoice/Allowance", req, &resp); err != nil {
		return nil, err
	}
	log.Info().Str("path", "/B2CInvoice/Allowance").Str("invoice_no", req.InvoiceNo).
		Int("rtn_code", int(resp.RtnCode)).Str("rtn_msg", resp.RtnMsg).Msg("einvoice: ecpay call")
	if int(resp.RtnCode) != 1 {
		return &resp, &APIError{TransCode: 1, RtnCode: int(resp.RtnCode), RtnMsg: resp.RtnMsg}
	}
	return &resp, nil
}

// ================= GetIssue（查詢） =================

// GetIssueReq /B2CInvoice/GetIssue 請求 Data：RelateNumber 或 (InvoiceNo+InvoiceDate) 擇一。
type GetIssueReq struct {
	MerchantID   string `json:"MerchantID"`
	RelateNumber string `json:"RelateNumber,omitempty"`
	InvoiceNo    string `json:"InvoiceNo,omitempty"`
	InvoiceDate  string `json:"InvoiceDate,omitempty"`
}

// GetIssueItem /B2CInvoice/GetIssue 回應 Data.Items 單筆明細。
type GetIssueItem struct {
	ItemSeq    FlexInt    `json:"ItemSeq"`
	ItemName   string     `json:"ItemName"`
	ItemCount  FlexNum    `json:"ItemCount"`
	ItemWord   string     `json:"ItemWord"`
	ItemPrice  FlexNum    `json:"ItemPrice"`
	ItemAmount FlexNum    `json:"ItemAmount"`
}

// GetIssueResp /B2CInvoice/GetIssue 回應 Data。
type GetIssueResp struct {
	RtnCode               FlexInt        `json:"RtnCode"`
	RtnMsg                string         `json:"RtnMsg"`
	// 全部用 FlexString：正式環境已實測外層 MerchantID 回數字，Data 內的狀態旗標／隨機碼／載具類型
	// 同樣可能是數字（文件皆寫 String）。嚴格字串型別會讓整包解析失敗，重試前的 GetIssue 回收就
	// 靜默失敗，接著 Issue 撞「自訂編號重覆」（2026-09-06 正式環境第一張實測）。
	IISNumber             FlexString     `json:"IIS_Number"`
	IISRelateNumber       FlexString     `json:"IIS_Relate_Number"`
	IISSalesAmount        FlexInt        `json:"IIS_Sales_Amount"`
	IISIssueStatus        FlexString     `json:"IIS_Issue_Status"`   // '1' 已開立／'0' 已取消
	IISInvalidStatus      FlexString     `json:"IIS_Invalid_Status"` // '1' 已作廢
	IISUploadStatus       FlexString     `json:"IIS_Upload_Status"`
	IISCreateDate         FlexString     `json:"IIS_Create_Date"`
	IISRandomNumber       FlexString     `json:"IIS_Random_Number"`
	IISCarrierType        FlexString     `json:"IIS_Carrier_Type"`
	IISLoveCode           FlexString     `json:"IIS_Love_Code"`
	IISRemainAllowanceAmt FlexInt        `json:"IIS_Remain_Allowance_Amt"`
	Items                 []GetIssueItem `json:"Items"`
}

// GetIssue 呼叫 /B2CInvoice/GetIssue 查詢一筆發票目前狀態。RtnCode!=1（例如查無此筆 RelateNumber，
// 代表尚未開立過）一律回傳 *APIError，呼叫端（issuer.go RecoverByRelateNumber）依此判斷「查無 ≠
// 傳輸失敗」，繼續走正常開立流程。
func (c *Client) GetIssue(ctx context.Context, req GetIssueReq) (*GetIssueResp, error) {
	req.MerchantID = c.MerchantID
	var resp GetIssueResp
	if err := c.call(ctx, "/B2CInvoice/GetIssue", req, &resp); err != nil {
		return nil, err
	}
	log.Info().Str("path", "/B2CInvoice/GetIssue").Str("relate_number", req.RelateNumber).
		Int("rtn_code", int(resp.RtnCode)).Str("rtn_msg", resp.RtnMsg).Msg("einvoice: ecpay call")
	if int(resp.RtnCode) != 1 {
		return &resp, &APIError{TransCode: 1, RtnCode: int(resp.RtnCode), RtnMsg: resp.RtnMsg}
	}
	return &resp, nil
}

// ================= CheckBarcode（手機條碼查驗） =================
//
// 官方規格（2026-09-08 web_fetch https://developers.ecpay.com.tw/7886.md 確認）：
// 請求 Data：MerchantID String(10) 必填、BarCode String(8) 必填（"/"開頭＋7碼，數字/大寫英文/+-.）。
// 回應 Data：RtnCode Int（1=成功；其餘失敗，9000001＝呼叫財政部 API 失敗/財政部系統維護中）、
// RtnMsg String(200)、IsExist String(1)（Y/N，⚠️只有 RtnCode==1 時才可信）。

// CheckBarcodeReq /B2CInvoice/CheckBarcode 請求 Data。
type CheckBarcodeReq struct {
	MerchantID string `json:"MerchantID"`
	BarCode    string `json:"BarCode"`
}

// CheckBarcodeResp /B2CInvoice/CheckBarcode 回應 Data。
type CheckBarcodeResp struct {
	RtnCode FlexInt    `json:"RtnCode"`
	RtnMsg  string     `json:"RtnMsg"`
	IsExist FlexString `json:"IsExist"`
}

// CheckBarcode 查驗手機條碼是否存在於財政部載具歸戶資料庫。RtnCode!=1（含 9000001 財政部 API
// 暫時失敗）一律回傳 *APIError——呼叫端（verify.go）必須把這種情況當「暫時無法查驗」而非
// 「條碼不存在」，不可誤判擋下合法輸入。⚠️ 不記錄 barcode 本身（同檔頭註解：客戶資料不進 log）。
func (c *Client) CheckBarcode(ctx context.Context, barcode string) (exists bool, err error) {
	req := CheckBarcodeReq{MerchantID: c.MerchantID, BarCode: barcode}
	var resp CheckBarcodeResp
	if err := c.call(ctx, "/B2CInvoice/CheckBarcode", req, &resp); err != nil {
		return false, err
	}
	log.Info().Str("path", "/B2CInvoice/CheckBarcode").
		Int("rtn_code", int(resp.RtnCode)).Str("rtn_msg", resp.RtnMsg).Msg("einvoice: ecpay call")
	if int(resp.RtnCode) != 1 {
		return false, &APIError{TransCode: 1, RtnCode: int(resp.RtnCode), RtnMsg: resp.RtnMsg}
	}
	return string(resp.IsExist) == "Y", nil
}

// ================= CheckLoveCode（愛心碼查驗） =================
//
// 官方規格（2026-09-08 web_fetch https://developers.ecpay.com.tw/7891.md 確認）：
// 請求 Data：MerchantID String(10) 必填、LoveCode String(7) 必填（純數字 3-7 碼，可含前導 0）。
// 回應 Data：RtnCode Int（1=成功取得查詢結果，其餘失敗）、RtnMsg String(200)、
// IsExist String(1)（Y=存在/N=不存在）、OrganName String(100)（僅 IsExist=='Y' 時有值，本檔不使用）。

// CheckLoveCodeReq /B2CInvoice/CheckLoveCode 請求 Data。
type CheckLoveCodeReq struct {
	MerchantID string `json:"MerchantID"`
	LoveCode   string `json:"LoveCode"`
}

// CheckLoveCodeResp /B2CInvoice/CheckLoveCode 回應 Data。
type CheckLoveCodeResp struct {
	RtnCode   FlexInt    `json:"RtnCode"`
	RtnMsg    string     `json:"RtnMsg"`
	IsExist   FlexString `json:"IsExist"`
	OrganName string     `json:"OrganName"`
}

// CheckLoveCode 查驗捐贈碼（愛心碼）是否存在。行為（RtnCode!=1 一律 *APIError）與 CheckBarcode 對稱，
// 見上方註解。
func (c *Client) CheckLoveCode(ctx context.Context, code string) (exists bool, err error) {
	req := CheckLoveCodeReq{MerchantID: c.MerchantID, LoveCode: code}
	var resp CheckLoveCodeResp
	if err := c.call(ctx, "/B2CInvoice/CheckLoveCode", req, &resp); err != nil {
		return false, err
	}
	log.Info().Str("path", "/B2CInvoice/CheckLoveCode").
		Int("rtn_code", int(resp.RtnCode)).Str("rtn_msg", resp.RtnMsg).Msg("einvoice: ecpay call")
	if int(resp.RtnCode) != 1 {
		return false, &APIError{TransCode: 1, RtnCode: int(resp.RtnCode), RtnMsg: resp.RtnMsg}
	}
	return string(resp.IsExist) == "Y", nil
}
