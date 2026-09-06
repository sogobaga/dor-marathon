// Package einvoice 綠界 B2C 電子發票 API 整合（migration 169）：訂單付款成功後非同步自動開立、
// 退款成功後開立折讓、後台手動開立/作廢/查詢同步。
//
// 與既有兩條綠界產品線各自獨立、不共用任何邏輯：
//   - internal/payment 的 AIO 結帳（CheckMacValue SHA256 簽章）。
//   - internal/payment 的站內付2.0（BindClient，信用卡定期定額/幕後綁卡請款）。
//
// 電子發票走的是第三種傳輸層封包（MerchantID+RqHeader{Timestamp,Revision}+Data 三層 AES-JSON
// envelope），但底層 AES-128-CBC/urlencode 加解密與站內付2.0 完全相同規格，直接重用
// payment.AESEncrypt/AESDecrypt（見 ecpay_aes.go），不重新實作一份。
//
// 本檔（Core 階段）只提供資料模型/純函式/repository/背景發票引擎，不含任何 HTTP handler——
// 對外端點（GET/POST /admin/orders/{id}/invoice 等）由後續 Wire 階段加上。
package einvoice

// Config 電子發票 API 憑證與環境（獨立於 payment.MultiConfig／payment.BindClient 的憑證）。
type Config struct {
	Env        string // stage | prod
	MerchantID string
	HashKey    string
	HashIV     string
}

const (
	stageBaseURL = "https://einvoice-stage.ecpay.com.tw"
	prodBaseURL  = "https://einvoice.ecpay.com.tw"
)

// BaseURL 依 Env 回傳電子發票 API 網域；Env 非 "prod" 一律視為 stage（故障安全：設定寫錯/漏帶時
// 不會誤打正式站）。
func (c Config) BaseURL() string {
	if c.Env == "prod" {
		return prodBaseURL
	}
	return stageBaseURL
}

// NewConfigFromEnv 由 internal/config.Config 的 ECPayInvoice* 欄位建構。prod 缺憑證時的降級 guard
// （invoiceEnvGuard）已在 internal/config 那一層做過，這裡直接信任傳入的 env 值。
func NewConfigFromEnv(env, merchantID, hashKey, hashIV string) Config {
	return Config{Env: env, MerchantID: merchantID, HashKey: hashKey, HashIV: hashIV}
}
