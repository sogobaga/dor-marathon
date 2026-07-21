package race

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// mobileCarrierRe 手機條碼載具：斜線開頭＋7 碼大寫英數與 .+-
var mobileCarrierRe = regexp.MustCompile(`^/[0-9A-Z.+-]{7}$`)

// loveCodeRe 捐贈碼：3–7 位數字
var loveCodeRe = regexp.MustCompile(`^[0-9]{3,7}$`)

// taxIDWeights 統一編號檢查碼權重
var taxIDWeights = [8]int{1, 2, 1, 2, 1, 2, 4, 1}

// IsValidTaxID 驗證台灣營利事業統一編號檢查碼。
// 規則：每位數字乘上對應權重後，把乘積的各位數相加（如 7×2=14 → 1+4=5），全部加總為 sum；
// sum%5==0 即有效。特例：第 7 位數字（index 6）若為 7，(sum+1)%5==0 亦視為有效
// （財政部 2023/4/1 修正後的檢查邏輯，用來相容新舊統編）。非 8 位純數字一律無效。
func IsValidTaxID(taxID string) bool {
	if len(taxID) != 8 {
		return false
	}
	digits := make([]int, 8)
	for i := 0; i < 8; i++ {
		ch := taxID[i]
		if ch < '0' || ch > '9' {
			return false
		}
		digits[i] = int(ch - '0')
	}
	sum := 0
	for i, d := range digits {
		product := d * taxIDWeights[i]
		sum += product/10 + product%10
	}
	if sum%5 == 0 {
		return true
	}
	if digits[6] == 7 && (sum+1)%5 == 0 {
		return true
	}
	return false
}

// ValidateInvoice 驗證並正規化報名 request 帶入的發票資訊。
// inv 為 nil（前端未帶 invoice 物件）時，正規化為 personal 且全空（＝雲端發票存證的預設狀態），
// 不會因此讓報名失敗——這是既有報名流程的相容前提。
//
// 三種 buyer_type 互斥（組合錯了之後串綠界電子發票 API 會被退件，現在就要擋）：
//   - company（三聯式）：tax_id 必填且須通過統一編號檢查碼；title 必填（≤120 rune）；
//     carrier_type/carrier_id/love_code 必須為空。
//   - personal（二聯式）：tax_id/title/love_code 必須為空；carrier_type 可為空字串（雲端發票存證）或
//     mobile；為 mobile 時 carrier_id 必填且須符合手機條碼格式。
//   - donation：love_code 必填、3–7 位數字；其餘欄位必須為空。
func ValidateInvoice(inv *InvoiceInfo) (InvoiceInfo, error) {
	var v InvoiceInfo
	if inv != nil {
		v = *inv
	}
	v.BuyerType = strings.TrimSpace(v.BuyerType)
	if v.BuyerType == "" {
		v.BuyerType = "personal" // 未帶 invoice 物件、或帶了但沒選 buyer_type，一律預設個人
	}
	v.TaxID = strings.TrimSpace(v.TaxID)
	v.Title = strings.TrimSpace(v.Title)
	v.CarrierType = strings.TrimSpace(v.CarrierType)
	v.CarrierID = strings.TrimSpace(v.CarrierID)
	v.LoveCode = strings.TrimSpace(v.LoveCode)

	switch v.BuyerType {
	case "personal":
		if v.TaxID != "" || v.Title != "" || v.LoveCode != "" {
			return v, fmt.Errorf("%w: personal 發票不可填統編/抬頭/愛心碼", ErrInvalidInvoice)
		}
		switch v.CarrierType {
		case "":
			if v.CarrierID != "" {
				return v, fmt.Errorf("%w: carrier_type 為空時 carrier_id 須留空", ErrInvalidInvoice)
			}
		case "mobile":
			if !mobileCarrierRe.MatchString(v.CarrierID) {
				return v, fmt.Errorf("%w: carrier_id 格式錯誤（須為 / 開頭＋7碼大寫英數與.+-）", ErrInvalidInvoice)
			}
		default:
			return v, fmt.Errorf("%w: carrier_type 僅限空值或 mobile", ErrInvalidInvoice)
		}
	case "company":
		if v.CarrierType != "" || v.CarrierID != "" || v.LoveCode != "" {
			return v, fmt.Errorf("%w: company 發票不可填載具/愛心碼", ErrInvalidInvoice)
		}
		if v.TaxID == "" {
			return v, fmt.Errorf("%w: 統編為必填", ErrInvalidInvoice)
		}
		if !IsValidTaxID(v.TaxID) {
			return v, fmt.Errorf("%w: 統編檢查碼錯誤", ErrInvalidInvoice)
		}
		if v.Title == "" {
			return v, fmt.Errorf("%w: 發票抬頭為必填", ErrInvalidInvoice)
		}
		if utf8.RuneCountInString(v.Title) > 120 {
			return v, fmt.Errorf("%w: 發票抬頭過長（上限120字）", ErrInvalidInvoice)
		}
	case "donation":
		if v.TaxID != "" || v.Title != "" || v.CarrierType != "" || v.CarrierID != "" {
			return v, fmt.Errorf("%w: 捐贈發票不可填統編/抬頭/載具", ErrInvalidInvoice)
		}
		if !loveCodeRe.MatchString(v.LoveCode) {
			return v, fmt.Errorf("%w: 愛心碼須為3-7位數字", ErrInvalidInvoice)
		}
	default:
		return v, fmt.Errorf("%w: buyer_type 不合法（僅限 personal/company/donation）", ErrInvalidInvoice)
	}
	return v, nil
}
