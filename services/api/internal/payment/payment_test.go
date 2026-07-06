package payment

import (
	"testing"
	"time"
)

func testCfg() *Config {
	return &Config{
		MerchantID:    "2000132",
		HashKey:       "5294y06JbISpM5x9",
		HashIV:        "v77hoKGq4kWxNNIS",
		Env:           "stage",
		ReturnURL:     "https://example.com/notify",
		ClientBackURL: "https://example.com/back",
	}
}

// CheckMacValue 為固定演算法 → 同輸入應得同輸出（回歸測試）
func TestCheckMacValueDeterministic(t *testing.T) {
	c := testCfg()
	params := map[string]string{
		"MerchantID":      "2000132",
		"MerchantTradeNo": "DOR1700000000ABCDEF",
		"TotalAmount":     "500",
		"ItemName":        "DOR 賽事報名",
	}
	a := c.CheckMacValue(params)
	b := c.CheckMacValue(params)
	if a != b {
		t.Fatalf("CheckMacValue not deterministic: %s vs %s", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("expected 64-char SHA256 hex, got %d (%s)", len(a), a)
	}
}

// BuildCheckout 產生的參數，VerifyCallback 應通過（自洽）
func TestBuildVerifyRoundTrip(t *testing.T) {
	c := testCfg()
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	params := c.BuildCheckout("DOR1700000000ABCDEF", 500, "DOR 賽事報名 - 測試賽事", "DOR 賽事報名", now, "")
	if params["CheckMacValue"] == "" {
		t.Fatal("missing CheckMacValue")
	}
	if !c.VerifyCallback(params) {
		t.Fatal("VerifyCallback failed for self-generated params")
	}
	// 竄改任一參數 → 驗章應失敗
	params["TotalAmount"] = "1"
	if c.VerifyCallback(params) {
		t.Fatal("VerifyCallback should fail after tampering")
	}
}

func TestDotNetURLEncode(t *testing.T) {
	cases := map[string]string{
		"a b":   "a+b",   // 空白 → +
		"a!b":   "a!b",   // ! 不編碼
		"a*(b)": "a*(b)", // *() 不編碼
		"a-_.b": "a-_.b", // - _ . 不編碼
	}
	for in, want := range cases {
		if got := dotNetURLEncode(in); got != want {
			t.Errorf("dotNetURLEncode(%q)=%q want %q", in, got, want)
		}
	}
}
