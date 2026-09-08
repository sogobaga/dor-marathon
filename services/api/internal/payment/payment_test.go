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

// TestMarkSupersededTxPaidCAS 涵蓋 M4 修補（Notify 對 superseded tx 補記 paid_superseded）：
// MarkSupersededTxPaid／GetPaidTxForOrder 的 paid_superseded fallback 都是純 SQL CAS，邏輯全在
// WHERE 子句裡（Repository.db 是具體的 *pgxpool.Pool，直接送原生 SQL，沒有像其他語言常見的
// query builder/interface 可注入假連線），本 repo 也沒有 sqlmock/pgxmock 一類的套件（go.mod 未見、
// 其他 _test.go 也一律只測不碰 DB 的純邏輯，如上面 CheckMacValue／ResolveByOrigin），因此無法在不接
// 真實 Postgres 的情況下驗證這兩條 UPDATE/SELECT 是否真的照預期 CAS。跳過、留下這則說明，而非硬做一個
// 測不到真正邏輯的假測試；真正的行為需求已寫在 payment.go MarkSupersededTxPaid 與 refund.go
// GetPaidTxForOrder 的函式註解，並經 go build/go vet 確認可編譯。
//
// finding 4（2026-09-08 第二次稽核，同一份 skip 說明追加）：Notify 對 MarkSupersededTxPaid 失敗
// 的處理也改了（見 payment.go Notify 該分支）——失敗時回 "0|MarkSupersededFailed" 並直接 return
// （不再往下呼叫 MarkOrderPaid），讓 ECPay 判讀為失敗而重送 Notify、下次重試同一筆 CAS；成功才
// 繼續原本流程。這段分支邏輯同樣綁死在 h.repo（具體 *Repository）與 http.ResponseWriter，非純函式，
// 一樣無法在不接真實 Postgres 的情況下單元測試，理由同上；已用 go build/go vet 確認可編譯。
func TestMarkSupersededTxPaidCAS(t *testing.T) {
	t.Skip("需要真實 Postgres 連線才能驗證 CAS 行為；repo 未附 sqlmock/pgxmock，見上方註解")
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
