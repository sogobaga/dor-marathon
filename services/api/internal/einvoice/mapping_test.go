package einvoice

import (
	"strings"
	"testing"
)

func baseSnapshot() OrderSnapshot {
	return OrderSnapshot{
		OrderID:     "11111111-2222-3333-4444-555555555555",
		TotalCents:  150000, // 1500 NTD
		UserEmail:   "runner@example.com",
		DisplayName: "小明",
		RaceTitle:   "2026 台北馬拉松",
		Invoice:     OrderInvoiceInput{BuyerType: "personal"},
	}
}

func TestBuildIssueRequest_PersonalCloudCarrier(t *testing.T) {
	snap := baseSnapshot()
	req, err := BuildIssueRequest(snap, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.CarrierType != "1" {
		t.Errorf("expected CarrierType '1' (ECPay carrier), got %q", req.CarrierType)
	}
	if req.Print != "0" {
		t.Errorf("expected Print '0', got %q", req.Print)
	}
	if req.Donation != "0" {
		t.Errorf("expected Donation '0', got %q", req.Donation)
	}
	if req.CustomerName != "小明" {
		t.Errorf("expected CustomerName '小明', got %q", req.CustomerName)
	}
	if req.CustomerEmail != "runner@example.com" {
		t.Errorf("expected real email in prod, got %q", req.CustomerEmail)
	}
	wantItemName := "「2026 台北馬拉松」報名費"
	if len(req.Items) != 1 || req.Items[0].ItemName != wantItemName {
		t.Errorf("expected single item %q, got %+v", wantItemName, req.Items)
	}
	if req.Items[0].ItemAmount != 1500 || req.SalesAmount != 1500 {
		t.Errorf("expected amount 1500, got item=%d sales=%d", req.Items[0].ItemAmount, req.SalesAmount)
	}
	if req.RelateNumber != "DOR"+strings.ReplaceAll(snap.OrderID, "-", "") {
		t.Errorf("unexpected RelateNumber %q", req.RelateNumber)
	}
}

func TestBuildIssueRequest_PersonalMobileCarrier(t *testing.T) {
	snap := baseSnapshot()
	snap.Invoice.CarrierType = "mobile"
	snap.Invoice.CarrierID = "/ABC123."
	req, err := BuildIssueRequest(snap, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.CarrierType != "3" {
		t.Errorf("expected CarrierType '3' (mobile), got %q", req.CarrierType)
	}
	if req.CarrierNum != "/ABC123." {
		t.Errorf("expected CarrierNum passthrough, got %q", req.CarrierNum)
	}
	if req.Print != "0" {
		t.Errorf("expected Print '0', got %q", req.Print)
	}
}

func TestBuildIssueRequest_Company(t *testing.T) {
	snap := baseSnapshot()
	snap.Invoice = OrderInvoiceInput{BuyerType: "company", TaxID: "12345675", Title: "測試股份有限公司"}
	req, err := BuildIssueRequest(snap, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.CustomerIdentifier != "12345675" {
		t.Errorf("expected CustomerIdentifier passthrough, got %q", req.CustomerIdentifier)
	}
	if req.CustomerName != "測試股份有限公司" {
		t.Errorf("expected CustomerName = title, got %q", req.CustomerName)
	}
	if req.CarrierType != "1" {
		t.Errorf("expected CarrierType '1' (avoids forced Print=1 rule), got %q", req.CarrierType)
	}
	if req.Print != "0" {
		t.Errorf("expected Print '0' (electronic invoice only), got %q", req.Print)
	}
	if req.Donation != "0" {
		t.Errorf("expected Donation '0', got %q", req.Donation)
	}
}

func TestBuildIssueRequest_Donation(t *testing.T) {
	snap := baseSnapshot()
	snap.Invoice = OrderInvoiceInput{BuyerType: "donation", LoveCode: "919191"}
	req, err := BuildIssueRequest(snap, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Donation != "1" {
		t.Errorf("expected Donation '1', got %q", req.Donation)
	}
	if req.LoveCode != "919191" {
		t.Errorf("expected LoveCode passthrough, got %q", req.LoveCode)
	}
	if req.CarrierType != "" {
		t.Errorf("expected empty CarrierType for donation, got %q", req.CarrierType)
	}
	if req.Print != "0" {
		t.Errorf("expected Print '0', got %q", req.Print)
	}
}

func TestBuildIssueRequest_AddonSuffix(t *testing.T) {
	snap := baseSnapshot()
	snap.HasAddon = true
	req, err := BuildIssueRequest(snap, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "「2026 台北馬拉松」報名費（含加購）"
	if req.Items[0].ItemName != want {
		t.Errorf("expected addon suffix %q, got %q", want, req.Items[0].ItemName)
	}
}

func TestBuildIssueRequest_VipMonthlyAndAnnual(t *testing.T) {
	cases := []struct {
		vipItemType string
		want        string
	}{
		{"vip_month", "DOR VIP 月費訂閱"},
		{"vip_year", "DOR VIP 年費訂閱"},
		{"", "DOR VIP 訂閱"}, // 無賽事、VipItemType 未知的防禦性 fallback
	}
	for _, c := range cases {
		snap := baseSnapshot()
		snap.RaceTitle = "" // VIP 訂單無賽事
		snap.VipItemType = c.vipItemType
		req, err := BuildIssueRequest(snap, "prod")
		if err != nil {
			t.Fatalf("vip_item_type=%q: unexpected error: %v", c.vipItemType, err)
		}
		if req.Items[0].ItemName != c.want {
			t.Errorf("vip_item_type=%q: expected item name %q, got %q", c.vipItemType, c.want, req.Items[0].ItemName)
		}
	}
}

func TestBuildIssueRequest_StageUsesDummyEmail(t *testing.T) {
	snap := baseSnapshot()
	req, err := BuildIssueRequest(snap, "stage")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.CustomerEmail != stageDummyEmail {
		t.Errorf("expected dummy email %q in stage, got %q", stageDummyEmail, req.CustomerEmail)
	}
}

func TestBuildIssueRequest_ProdUsesRealEmail(t *testing.T) {
	snap := baseSnapshot()
	req, err := BuildIssueRequest(snap, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.CustomerEmail != "runner@example.com" {
		t.Errorf("expected real email in prod, got %q", req.CustomerEmail)
	}
}

func TestBuildIssueRequest_PhoneOnlyWhenDigitsOnly(t *testing.T) {
	snap := baseSnapshot()
	snap.UserPhone = "0912345678"
	req, err := BuildIssueRequest(snap, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.CustomerPhone != "0912345678" {
		t.Errorf("expected phone passthrough, got %q", req.CustomerPhone)
	}

	snap2 := baseSnapshot()
	snap2.UserPhone = "0912-345-678" // 含非數字字元，應被丟棄
	req2, err := BuildIssueRequest(snap2, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req2.CustomerPhone != "" {
		t.Errorf("expected empty phone for non-digits-only input, got %q", req2.CustomerPhone)
	}
}

func TestBuildIssueRequest_ItemNameTruncatedTo500Runes(t *testing.T) {
	snap := baseSnapshot()
	snap.RaceTitle = strings.Repeat("賽", 600)
	req, err := BuildIssueRequest(snap, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len([]rune(req.Items[0].ItemName)); got > 500 {
		t.Errorf("expected ItemName truncated to <=500 runes, got %d", got)
	}
}

func TestBuildIssueRequest_CustomerNameTruncatedTo60Runes(t *testing.T) {
	snap := baseSnapshot()
	snap.DisplayName = strings.Repeat("名", 100)
	req, err := BuildIssueRequest(snap, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len([]rune(req.CustomerName)); got != 60 {
		t.Errorf("expected CustomerName truncated to exactly 60 runes, got %d", got)
	}
}

func TestBuildIssueRequest_RejectsZeroAmount(t *testing.T) {
	snap := baseSnapshot()
	snap.TotalCents = 0
	if _, err := BuildIssueRequest(snap, "prod"); err == nil {
		t.Fatal("expected error for zero total_cents")
	}
}

func TestBuildIssueRequest_RejectsNonWholeNTD(t *testing.T) {
	snap := baseSnapshot()
	snap.TotalCents = 150050 // 非整數 NTD
	if _, err := BuildIssueRequest(snap, "prod"); err == nil {
		t.Fatal("expected error for non-whole-NTD total_cents")
	}
}

func TestBuildIssueRequest_RejectsEmptyOrderID(t *testing.T) {
	snap := baseSnapshot()
	snap.OrderID = ""
	if _, err := BuildIssueRequest(snap, "prod"); err == nil {
		t.Fatal("expected error for empty order id")
	}
}

func TestRelateNumberFor_StripsHyphensAndPrefixesDOR(t *testing.T) {
	orderID := "11111111-2222-3333-4444-555555555555"
	got := RelateNumberFor(orderID)
	want := "DOR" + strings.ReplaceAll(orderID, "-", "")
	if got != want {
		t.Errorf("unexpected RelateNumber: got %q, want %q", got, want)
	}
	if len(got) > 50 {
		t.Errorf("RelateNumber exceeds ECPay 50-char limit: %d", len(got))
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("hello", 10); got != "hello" {
		t.Errorf("expected passthrough when under limit, got %q", got)
	}
	if got := truncateRunes("你好世界", 2); got != "你好" {
		t.Errorf("expected rune-based truncation, got %q", got)
	}
}

func TestIsDigitsOnlyLen(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"12345678", true},
		{"1234567", false},               // 7 碼，低於下限 8
		{"091234567890123456789", false}, // 過長
		{"0912a45678", false},            // 含非數字
		{"", false},
	}
	for _, c := range cases {
		if got := isDigitsOnlyLen(c.s, 8, 20); got != c.want {
			t.Errorf("isDigitsOnlyLen(%q)=%v, want %v", c.s, got, c.want)
		}
	}
}
