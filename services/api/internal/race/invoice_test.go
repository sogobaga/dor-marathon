package race

import "testing"

// TestIsValidTaxID_RealCompanies 正確統編：兩間真實已登記公司的統一編號（皆經多方公開來源交叉查證）。
//   - 臺灣證券交易所股份有限公司：03559508（findcompany.com.tw、twincn.com 皆登記為此統編）
//   - 台灣大哥大股份有限公司：97176270（政府開放資料鏡站 opengovtw.com、mygov.tw、twincn.com 皆登記為此統編；
//     這組剛好第 7 碼＝7，但本身用一般規則 sum%5==0 就會過，不是靠特例通過——第 7 碼特例的單獨驗證見下方）
func TestIsValidTaxID_RealCompanies(t *testing.T) {
	cases := []string{"03559508", "97176270"}
	for _, tid := range cases {
		if !IsValidTaxID(tid) {
			t.Errorf("IsValidTaxID(%q) = false, want true (真實已登記公司統編)", tid)
		}
	}
}

// TestIsValidTaxID_OneDigitWrong 錯一碼：把真實有效統編 03559508 的最後一碼 8→9，檢查碼應該失敗。
func TestIsValidTaxID_OneDigitWrong(t *testing.T) {
	if IsValidTaxID("03559509") {
		t.Error(`IsValidTaxID("03559509") = true, want false（改錯一碼後檢查碼應該失敗）`)
	}
}

// TestIsValidTaxID_Digit7ExceptionOnly 第 7 位數字為 7 的特例：97176274 是刻意構造的測試向量
// （非真實公司，純粹用來單獨驗證特例規則），一般規則 sum%5==0 在這組數字上會失敗（sum=44，44%5=4），
// 必須靠特例 (sum+1)%5==0（45%5=0）才能通過——用來確認特例分支真的有被執行到，而不是只是巧合通過一般規則。
func TestIsValidTaxID_Digit7ExceptionOnly(t *testing.T) {
	if !IsValidTaxID("97176274") {
		t.Error(`IsValidTaxID("97176274") = false, want true（應靠第7碼=7的特例規則通過）`)
	}
	// 對照組：把第 7 碼從 7 換成 0（其餘不變會變成別的數字，這裡改用第 7 碼非 7 但同樣 sum%5!=0 的組合，
	// 確認「沒有特例資格」時同樣的失敗 sum 不會被誤判通過。
	if IsValidTaxID("97176204") {
		t.Error(`IsValidTaxID("97176204") = true, want false（第7碼不是7，不該套用特例）`)
	}
}

// TestIsValidTaxID_WrongLength 長度不符：非 8 位一律無效。
func TestIsValidTaxID_WrongLength(t *testing.T) {
	cases := []string{"", "1234567", "123456789", "5312539"}
	for _, tid := range cases {
		if IsValidTaxID(tid) {
			t.Errorf("IsValidTaxID(%q) = true, want false（長度不是8位）", tid)
		}
	}
}

// TestIsValidTaxID_NonDigit 含非數字：一律無效。
func TestIsValidTaxID_NonDigit(t *testing.T) {
	cases := []string{"0355950A", "abcdefgh", "0355-508", "０３５５９５０８"}
	for _, tid := range cases {
		if IsValidTaxID(tid) {
			t.Errorf("IsValidTaxID(%q) = true, want false（含非數字字元）", tid)
		}
	}
}

// TestValidateInvoice_NilDefaultsToPersonalBlank 未帶 invoice 物件（nil）→ 正規化為 personal 全空，
// 且不可回傳錯誤（既有報名流程不能因此被破壞）。
func TestValidateInvoice_NilDefaultsToPersonalBlank(t *testing.T) {
	v, err := ValidateInvoice(nil)
	if err != nil {
		t.Fatalf("ValidateInvoice(nil) error = %v, want nil", err)
	}
	want := InvoiceInfo{BuyerType: "personal"}
	if v != want {
		t.Errorf("ValidateInvoice(nil) = %+v, want %+v", v, want)
	}
}

func TestValidateInvoice_Company(t *testing.T) {
	cases := []struct {
		name    string
		in      InvoiceInfo
		wantErr bool
	}{
		{"valid", InvoiceInfo{BuyerType: "company", TaxID: "03559508", Title: "臺灣證券交易所股份有限公司"}, false},
		{"bad checksum", InvoiceInfo{BuyerType: "company", TaxID: "03559509", Title: "測試公司"}, true},
		{"missing tax_id", InvoiceInfo{BuyerType: "company", Title: "測試公司"}, true},
		{"missing title", InvoiceInfo{BuyerType: "company", TaxID: "03559508"}, true},
		{"carrier not allowed", InvoiceInfo{BuyerType: "company", TaxID: "03559508", Title: "測試公司", CarrierType: "mobile", CarrierID: "/ABCD123"}, true},
		{"love_code not allowed", InvoiceInfo{BuyerType: "company", TaxID: "03559508", Title: "測試公司", LoveCode: "1234"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := c.in
			_, err := ValidateInvoice(&in)
			if (err != nil) != c.wantErr {
				t.Errorf("ValidateInvoice(%+v) error = %v, wantErr %v", c.in, err, c.wantErr)
			}
		})
	}
}

func TestValidateInvoice_Personal(t *testing.T) {
	cases := []struct {
		name    string
		in      InvoiceInfo
		wantErr bool
	}{
		{"blank cloud invoice", InvoiceInfo{BuyerType: "personal"}, false},
		{"valid mobile carrier", InvoiceInfo{BuyerType: "personal", CarrierType: "mobile", CarrierID: "/AB12345"}, false},
		{"mobile carrier missing id", InvoiceInfo{BuyerType: "personal", CarrierType: "mobile"}, true},
		{"mobile carrier bad format (no slash)", InvoiceInfo{BuyerType: "personal", CarrierType: "mobile", CarrierID: "AB123456"}, true},
		{"mobile carrier bad format (lowercase)", InvoiceInfo{BuyerType: "personal", CarrierType: "mobile", CarrierID: "/ab12345"}, true},
		{"mobile carrier bad format (too short)", InvoiceInfo{BuyerType: "personal", CarrierType: "mobile", CarrierID: "/AB1234"}, true},
		{"unknown carrier_type", InvoiceInfo{BuyerType: "personal", CarrierType: "citizen_cert"}, true},
		{"carrier_id without carrier_type", InvoiceInfo{BuyerType: "personal", CarrierID: "/AB12345"}, true},
		{"tax_id not allowed", InvoiceInfo{BuyerType: "personal", TaxID: "03559508"}, true},
		{"title not allowed", InvoiceInfo{BuyerType: "personal", Title: "x"}, true},
		{"love_code not allowed", InvoiceInfo{BuyerType: "personal", LoveCode: "123"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := c.in
			_, err := ValidateInvoice(&in)
			if (err != nil) != c.wantErr {
				t.Errorf("ValidateInvoice(%+v) error = %v, wantErr %v", c.in, err, c.wantErr)
			}
		})
	}
}

func TestValidateInvoice_Donation(t *testing.T) {
	cases := []struct {
		name    string
		in      InvoiceInfo
		wantErr bool
	}{
		{"valid 3-digit", InvoiceInfo{BuyerType: "donation", LoveCode: "123"}, false},
		{"valid 7-digit", InvoiceInfo{BuyerType: "donation", LoveCode: "1234567"}, false},
		{"missing love_code", InvoiceInfo{BuyerType: "donation"}, true},
		{"too short", InvoiceInfo{BuyerType: "donation", LoveCode: "12"}, true},
		{"too long", InvoiceInfo{BuyerType: "donation", LoveCode: "12345678"}, true},
		{"non digit", InvoiceInfo{BuyerType: "donation", LoveCode: "12A"}, true},
		{"tax_id not allowed", InvoiceInfo{BuyerType: "donation", LoveCode: "123", TaxID: "03559508"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := c.in
			_, err := ValidateInvoice(&in)
			if (err != nil) != c.wantErr {
				t.Errorf("ValidateInvoice(%+v) error = %v, wantErr %v", c.in, err, c.wantErr)
			}
		})
	}
}

func TestValidateInvoice_UnknownBuyerType(t *testing.T) {
	in := InvoiceInfo{BuyerType: "bitcoin"}
	if _, err := ValidateInvoice(&in); err == nil {
		t.Error("ValidateInvoice with unknown buyer_type should error")
	}
}
