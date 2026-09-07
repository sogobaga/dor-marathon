// verify.go：手機條碼／愛心碼「輸入時查驗」共用核心邏輯（不含 HTTP handler 外殼）。
//
// 動機（2026-09-08 owner 需求）：會員輸入手機條碼載具時常打錯字但格式仍合法（最常見 0/O、1/I
// 混淆），race.ValidateInvoice 的正規表達式抓不到這種「格式對但號碼根本不存在」的輸入，要等到
// 之後這筆訂單的發票送交政府歸戶失敗才會發現——這時會員早已報名完成、難以聯繫更正。這裡在
// 輸入當下就即時打 ECPay CheckBarcode／CheckLoveCode（背後就是財政部歸戶資料庫）查一次。
//
// 刻意獨立一個檔案、不放進 issuer.go：issuer.go 是「已確定要開立/作廢/折讓」的動作，會寫入
// order_invoices；這裡純粹是唯讀查詢，不碰任何 DB，失敗一律不擋（fail-open，見 buildResult）。
// 供 member_handler.go（會員 /invoice/verify）與 handler.go（後台 verify-carrier）共用。
package einvoice

import (
	"context"
	"regexp"
	"strings"
)

// verifyMobileCarrierRe／verifyLoveCodeRe 格式驗證規則，鏡射 internal/race/invoice.go 的
// mobileCarrierRe／loveCodeRe（同一份 ECPay 規格）。刻意不跨套件 import：race 與 einvoice
// 之間全靠各自定義的小介面卡榫（見 issuer.go 檔尾「Wire 階段」註解），維持解耦，避免 import
// 循環風險；代價是這兩條規則要手動保持同步，未來若格式規則變動需一併修改兩處。
var (
	verifyMobileCarrierRe = regexp.MustCompile(`^/[0-9A-Z.+-]{7}$`)
	verifyLoveCodeRe      = regexp.MustCompile(`^[0-9]{3,7}$`)
)

// VerifyResult /invoice/verify（會員）／verify-carrier（後台）共用回應形狀。
type VerifyResult struct {
	FormatOK bool   `json:"format_ok"`
	Exists   *bool  `json:"exists"` // nil＝未查驗（格式不合法，或 ECPay/財政部暫時無法查）
	Checked  bool   `json:"checked"`
	Message  string `json:"message"`
	Env      string `json:"env"` // stage｜prod：前端在 env != prod 時顯示「（測試環境）」提示
}

// NormalizeCarrierInput 正規化手機條碼輸入：去頭尾空白、轉大寫、移除中間空白。
// 供本檔查驗與 race.ValidateInvoice 的正規化共用同一套規則（見該檔案 2026-09-08 更新）。
func NormalizeCarrierInput(raw string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(raw), " ", ""))
}

// verifyMobileOrLoveCode 是 /invoice/verify、verify-carrier 共用核心：正規化 → 格式驗證 →
// （格式合法才）呼叫 ECPay CheckBarcode／CheckLoveCode。
//
// client 呼叫失敗（含 *APIError，例如 RtnCode=9000001 財政部 API 暫時無法查詢）一律回傳
// checked=false, exists=nil——這是唯讀輸入提示，任何不確定的情況都不能誤判成「不存在」而擋住
// 使用者送出報名（見任務規格：never block on transport failure）。
func verifyMobileOrLoveCode(ctx context.Context, client *Client, cfgEnv, kind, rawValue string) VerifyResult {
	switch kind {
	case "mobile":
		v := NormalizeCarrierInput(rawValue)
		if !verifyMobileCarrierRe.MatchString(v) {
			return VerifyResult{FormatOK: false, Checked: false,
				Message: "格式須為「/」開頭＋7碼大寫英數與 . + -（例如 /AB12+CD）", Env: cfgEnv}
		}
		exists, err := client.CheckBarcode(ctx, v)
		return buildVerifyResult(exists, err, cfgEnv,
			"已確認此手機條碼存在",
			"查無此手機條碼，請確認是否打錯（常見：0 與 O、1 與 I、l）")
	case "love_code":
		v := strings.TrimSpace(rawValue)
		if !verifyLoveCodeRe.MatchString(v) {
			return VerifyResult{FormatOK: false, Checked: false,
				Message: "愛心碼須為 3-7 位數字", Env: cfgEnv}
		}
		exists, err := client.CheckLoveCode(ctx, v)
		return buildVerifyResult(exists, err, cfgEnv,
			"已確認此愛心碼存在",
			"查無此愛心碼，請確認是否打錯")
	default:
		return VerifyResult{FormatOK: false, Checked: false, Message: "type 僅限 mobile 或 love_code", Env: cfgEnv}
	}
}

// buildVerifyResult 是 mobile／love_code 兩分支共用的「呼叫結果 → VerifyResult」收斂邏輯。
func buildVerifyResult(exists bool, err error, cfgEnv, existsMsg, missingMsg string) VerifyResult {
	if err != nil {
		return VerifyResult{FormatOK: true, Checked: false,
			Message: "暫時無法向財政部查驗，請再試一次或稍後再填", Env: cfgEnv}
	}
	e := exists
	msg := missingMsg
	if exists {
		msg = existsMsg
	}
	return VerifyResult{FormatOK: true, Exists: &e, Checked: true, Message: msg, Env: cfgEnv}
}
