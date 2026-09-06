package einvoice

import (
	"fmt"
	"strings"
)

// OrderInvoiceInput 買受人資訊快照，直接對應 order_invoices 既有欄位（見 race.InvoiceInfo／
// race.ValidateInvoice；本套件不重複驗證格式，信任報名端已驗證過的既有資料）。
type OrderInvoiceInput struct {
	BuyerType   string // personal|company|donation
	TaxID       string // company 專用：統一編號
	Title       string // company 專用：發票抬頭
	CarrierType string // personal 專用：''(雲端發票存證)｜mobile
	CarrierID   string // personal 專用：手機條碼載具號碼
	LoveCode    string // donation 專用：愛心碼
}

// OrderSnapshot 開立/折讓一張發票所需的完整訂單快照，由 Repository.LoadOrderSnapshot 組出。
// 刻意與 DB 查詢分離：BuildIssueRequest 是純函式，可用 table-driven test 覆蓋全部買受人組合，
// 不需要假 DB。
type OrderSnapshot struct {
	OrderID     string
	TotalCents  int
	IsVirtual   bool
	UserEmail   string
	DisplayName string // COALESCE(users.name, users.handle)，見 memory display-name-convention
	UserPhone   string // user_profiles.phone，可能為空
	RaceTitle   string // 空字串＝VIP 訂單（orders.race_id IS NULL）
	HasAddon    bool   // order_items 是否含 item_type='addon' 的列
	VipItemType string // "vip_month" | "vip_year" | ""（非 VIP 訂單，或未知）
	// PaidTxEnv 這筆訂單「已付款」的 payment_transactions.ecpay_env（stage|prod）；查無任何 paid
	// 交易（例如後台人工標記付款、無金流紀錄）時為空字串。只供 Issuer 的略過判斷使用，不影響
	// BuildIssueRequest 本身。
	PaidTxEnv string
	Invoice   OrderInvoiceInput
}

// stageDummyEmail 測試環境一律用固定假信箱，不寄真實客戶信箱（見 PRODUCT DECISIONS：
// 「Testing env must not use real customer emails」）。
const stageDummyEmail = "test@dor.tw"

// BuildIssueRequest 依 PRODUCT DECISIONS #2 的對照規則，把訂單快照組成一支 /B2CInvoice/Issue 的
// Data 欄位。純函式：不觸網、不觸 DB。cfgEnv 是本次呼叫要用的電子發票環境設定（"stage"|"prod"，
// 來自 Config.Env，非訂單欄位——訂單本身的付款環境是 snap.PaidTxEnv，兩者用途不同，不可混用：
// cfgEnv 只決定「這次要不要用假信箱寄送」，PaidTxEnv 只給 Issuer 的略過判斷用）。
func BuildIssueRequest(snap OrderSnapshot, cfgEnv string) (IssueReq, error) {
	if snap.OrderID == "" {
		return IssueReq{}, fmt.Errorf("einvoice: order id is required")
	}
	if snap.TotalCents <= 0 || snap.TotalCents%100 != 0 {
		return IssueReq{}, fmt.Errorf("einvoice: total_cents must be a positive multiple of 100 (got %d)", snap.TotalCents)
	}

	salesAmount := snap.TotalCents / 100
	displayName := truncateRunes(snap.DisplayName, 60)

	req := IssueReq{
		TaxType:     "1",
		Vat:         "1",
		InvType:     "07",
		SalesAmount: salesAmount,
		Print:       "0", // 四種買受人組合皆不需要紙本，全走電子發票（見規則註解）
		Donation:    "0",
	}

	switch snap.Invoice.BuyerType {
	case "company":
		// 三聯式：CarrierType 明確填 '1'（不是空字串），刻意避開「UBN 存在且 CarrierType 為空
		// 時 Print 必須為 1」這條規則——本系統一律走電子發票，不開紙本。
		req.CustomerIdentifier = snap.Invoice.TaxID
		req.CustomerName = truncateRunes(snap.Invoice.Title, 60)
		req.CarrierType = "1"
	case "donation":
		req.Donation = "1"
		req.LoveCode = snap.Invoice.LoveCode
		req.CarrierType = ""
		req.CustomerName = displayName
	default:
		// "personal" 或未知一律當 personal 處理（防禦性 fallback；報名端 race.ValidateInvoice 已
		// 擋過格式，這裡不重複驗證，只需要一個安全的預設分支）。
		req.CustomerName = displayName
		if snap.Invoice.CarrierType == "mobile" {
			req.CarrierType = "3"
			req.CarrierNum = snap.Invoice.CarrierID
		} else {
			req.CarrierType = "1" // 綠界載具（雲端發票存證，依 Email/手機自動歸戶）
		}
	}

	req.CustomerEmail = truncateRunes(snap.UserEmail, 80)
	if cfgEnv != "prod" {
		req.CustomerEmail = stageDummyEmail
	}
	if isDigitsOnlyLen(snap.UserPhone, 8, 20) {
		req.CustomerPhone = snap.UserPhone
	}

	itemName := buildItemName(snap)
	req.Items = []IssueItem{{
		ItemSeq: 1, ItemName: itemName, ItemCount: 1, ItemWord: "式",
		ItemPrice: salesAmount, ItemTaxType: "1", ItemAmount: salesAmount,
	}}

	req.RelateNumber = RelateNumberFor(snap.OrderID)
	req.InvoiceRemark = truncateRunes("DOR 訂單 "+snap.OrderID, 200)
	return req, nil
}

// buildItemName 依訂單種類組出唯一一行明細品名（見 PRODUCT DECISIONS #2）。
func buildItemName(snap OrderSnapshot) string {
	var name string
	switch snap.VipItemType {
	case "vip_month":
		name = "DOR VIP 月費訂閱"
	case "vip_year":
		name = "DOR VIP 年費訂閱"
	default:
		if snap.RaceTitle != "" {
			name = fmt.Sprintf("「%s」報名費", snap.RaceTitle)
			if snap.HasAddon {
				name += "（含加購）"
			}
		} else {
			name = "DOR VIP 訂閱" // 無賽事但 VipItemType 未知的防禦性 fallback
		}
	}
	return truncateRunes(name, 500)
}

// RelateNumberFor 依訂單 id 組出 ECPay RelateNumber：限半形英數、≤50 字、不分大小寫，
// 同一訂單每次重試都得出同一個值——Issuer 靠這個確定性在重試前用 GetIssue 查回「已開立但我方
// 沒記到」的發票（見 issuer.go RecoverByRelateNumber）。"DOR"(3) + UUID 去掉連字號(32 hex) = 35 字。
func RelateNumberFor(orderID string) string {
	return "DOR" + strings.ReplaceAll(orderID, "-", "")
}

// truncateRunes 依 rune 數截斷（欄位長度限制皆以字元數計，非 byte 數，含中文時很重要）。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// isDigitsOnlyLen 是否為純數字字串，且長度落在 [min,max] 區間（CustomerPhone 規則：純數字 8-20 碼）。
func isDigitsOnlyLen(s string, min, max int) bool {
	if len(s) < min || len(s) > max {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
