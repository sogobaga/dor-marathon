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

func testMulti(globalEnv string) *MultiConfig {
	stage := &Config{
		MerchantID:    "2000132",
		Env:           "stage",
		ClientBackURL: "https://dor.hero-mi.com",
		AllowedBacks:  []string{"https://dor.hero-mi.com", "https://www.dor.tw", "https://dor.tw"},
	}
	prod := &Config{
		MerchantID:    "PRODMID",
		Env:           "prod",
		ClientBackURL: "https://dor.hero-mi.com",
		AllowedBacks:  []string{"https://dor.hero-mi.com", "https://www.dor.tw", "https://dor.tw"},
	}
	return &MultiConfig{
		Prod:        prod,
		Stage:       stage,
		GlobalEnv:   globalEnv,
		ProdOrigins: []string{"https://www.dor.tw", "https://dor.tw"},
	}
}

// 對應驗收四題：ResolveByOrigin 依 GlobalEnv/origin 決定要用哪組特店、是否 fail closed。
func TestResolveByOriginScenarios(t *testing.T) {
	// 1. 現況 ECPAY_ENV=stage、origin=www.dor.tw → 用 stage、不會被擋（GlobalEnv!=prod 時 ok 恆為 true）。
	m := testMulti("stage")
	if env, cfg, ok := m.ResolveByOrigin("https://www.dor.tw"); env != "stage" || !ok || cfg != m.Stage {
		t.Fatalf("case1: got env=%s ok=%v cfg=%p want stage/true/m.Stage", env, ok, cfg)
	}

	// 2. 切正式後 ECPAY_ENV=prod、origin=www.dor.tw → 用 prod。
	m = testMulti("prod")
	if env, cfg, ok := m.ResolveByOrigin("https://www.dor.tw"); env != "prod" || !ok || cfg != m.Prod {
		t.Fatalf("case2: got env=%s ok=%v cfg=%p want prod/true/m.Prod", env, ok, cfg)
	}

	// 3. 切正式後、origin=dor.hero-mi.com（UAT，在既有返回網址白名單內但不在 ProdOrigins）→ 用 stage。
	if env, cfg, ok := m.ResolveByOrigin("https://dor.hero-mi.com"); env != "stage" || !ok || cfg != m.Stage {
		t.Fatalf("case3: got env=%s ok=%v cfg=%p want stage/true/m.Stage", env, ok, cfg)
	}

	// 4. 切正式後、origin 為空或陌生網域 → fail closed（ok=false）。
	if _, _, ok := m.ResolveByOrigin(""); ok {
		t.Fatal("case4a: empty origin should be ok=false (fail closed)")
	}
	if _, _, ok := m.ResolveByOrigin("https://evil.example.com"); ok {
		t.Fatal("case4b: unknown origin should be ok=false (fail closed)")
	}

	// 回歸：GlobalEnv=stage 時任何 origin（含空字串／陌生網域）一律 ok=true、用 stage —— 不可造成任何回歸。
	m = testMulti("stage")
	for _, o := range []string{"", "https://evil.example.com", "https://www.dor.tw"} {
		if env, cfg, ok := m.ResolveByOrigin(o); env != "stage" || !ok || cfg != m.Stage {
			t.Fatalf("regression: origin=%q got env=%s ok=%v want stage/true", o, env, ok)
		}
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
