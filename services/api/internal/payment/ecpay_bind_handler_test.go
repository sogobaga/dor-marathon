package payment

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// 本檔測試涵蓋「不需要真的 DB」的部分——比照 race 套件既有慣例（見
// internal/race/vip_order_test.go 註解：「本套件本來就沒有 DB 整合測試的地基」），本套件也沒有。
// settleVipBindPayment 的 CAS 冪等閘門（同一訂單結算兩次→第二次 no-op）因此無法在這裡跑真的
// round-trip；但 Notify/Result 兩個 webhook 入口在「觸碰 DB 之前」的所有步驟——AES 解密、時間戳
// 重放防護、TransCode/ResultData 兩種外層格式解析、回應格式（恰為 1|OK）——都是純函式邏輯，
// 可以且應該測。這裡刻意用「data.OrderInfo 缺漏」的合法封包造出一條會在 handleBindWebhookData
// 最前面 return（不觸碰 h.repo/h.db）的路徑，讓 Notify/Result handler 本身能被完整跑過而不需要
// 真的資料庫連線；settleVipBindPayment 的 CAS SQL 本身則對照 race.Repository.MarkOrderPaid
// 同一套「WHERE status='pending'」CAS 寫法（該函式已在正式環境驗證過），以程式碼審閱取代單測。

func testBindHandlerNoDB() *BindHandler {
	return &BindHandler{
		client:      &BindClient{MerchantID: "3002607", HashKey: testBindHashKey, HashIV: testBindHashIV},
		frontendURL: "https://www.dor.tw",
	}
}

// buildBindEnvelope 組出一個合法的站內付2.0 webhook 外層 envelope（AES 加密 data，Timestamp 可指定
// 供測試時間戳重放防護）。
func buildBindEnvelope(t *testing.T, data any, timestamp int64, transCode int) []byte {
	t.Helper()
	plainJSON, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	encData, err := ecpayAESEncrypt(testBindHashKey, testBindHashIV, string(plainJSON))
	if err != nil {
		t.Fatalf("encrypt data: %v", err)
	}
	env := bindRespEnvelope{
		MerchantID: "3002607",
		RpHeader:   bindRpHeader{Timestamp: timestamp},
		TransCode:  transCode,
		TransMsg:   "Success",
		Data:       encData,
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return b
}

// TestBindWebhookDataParsing 驗證獨立 DTO（bindWebhookData）能正確解出 guides/21 line439+ 列出的
// 全部欄位，包含巢狀 OrderInfo/CardInfo 與頂層 MerchantMemberID/BindCardID/IsSameCard/CustomField。
func TestBindWebhookDataParsing(t *testing.T) {
	const raw = `{
		"RtnCode": 1,
		"RtnMsg": "Succeeded",
		"MerchantID": "3002607",
		"MerchantMemberID": "abc123",
		"BindCardID": "BINDCARDID1234567890",
		"IsSameCard": true,
		"OrderInfo": {
			"MerchantTradeNo": "DORTEST0001",
			"TradeNo": "2026081912345678",
			"TradeAmt": 399,
			"TradeDate": "2026/08/19 12:00:00",
			"PaymentType": "Credit_CreditCard",
			"PaymentDate": "2026/08/19 12:00:01",
			"TradeStatus": "1"
		},
		"CardInfo": {
			"AuthCode": "777777",
			"Gwsr": 123456,
			"Card6No": "422222",
			"Card4No": "3333",
			"CardValidYY": "28",
			"CardValidMM": "12"
		},
		"CustomField": "custom-value"
	}`
	var data bindWebhookData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if data.RtnCode != 1 || data.RtnMsg != "Succeeded" {
		t.Errorf("unexpected RtnCode/RtnMsg: %d %q", data.RtnCode, data.RtnMsg)
	}
	if data.MerchantMemberID != "abc123" || data.BindCardID != "BINDCARDID1234567890" || !data.IsSameCard {
		t.Errorf("unexpected top-level fields: %+v", data)
	}
	if data.CustomField != "custom-value" {
		t.Errorf("unexpected CustomField: %q", data.CustomField)
	}
	if data.OrderInfo == nil || data.OrderInfo.MerchantTradeNo != "DORTEST0001" || data.OrderInfo.TradeNo != "2026081912345678" ||
		data.OrderInfo.TradeAmt != 399 || data.OrderInfo.TradeStatus != "1" || data.OrderInfo.PaymentType != "Credit_CreditCard" {
		t.Errorf("unexpected OrderInfo: %+v", data.OrderInfo)
	}
	if data.CardInfo == nil || data.CardInfo.Gwsr != 123456 || data.CardInfo.Card4No != "3333" ||
		data.CardInfo.CardValidYY != "28" || data.CardInfo.CardValidMM != "12" {
		t.Errorf("unexpected CardInfo: %+v", data.CardInfo)
	}
}

// TestNotify_ValidPacketAlwaysRespondsOK 驗證「封包合法（解密+時間戳都通過）」時，不論
// handleBindWebhookData 內部業務結果如何，一律回應恰為 "1|OK"（4 字元、無換行/空白）。
// 用 OrderInfo 缺漏（不合法的業務資料，但外層封包本身合法）觸發 handleBindWebhookData 最前面的
// return，藉此在不需要 DB 的情況下跑過整個 Notify handler。
func TestNotify_ValidPacketAlwaysRespondsOK(t *testing.T) {
	h := testBindHandlerNoDB()
	body := buildBindEnvelope(t, map[string]any{"RtnCode": 1, "RtnMsg": "Succeeded"}, time.Now().Unix(), 1)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/ecpay/bind/notify", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	h.Notify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Body.String(); got != "1|OK" {
		t.Fatalf("expected response body exactly %q, got %q", "1|OK", got)
	}
}

// TestNotify_TransCodeNotOne 驗證傳輸層失敗（TransCode!=1）不視為「封包合法」，不回 1|OK
// （依官方參考實作回應 0|Fail，見 guides/21 雙 Callback Go 範例）。
func TestNotify_TransCodeNotOne(t *testing.T) {
	h := testBindHandlerNoDB()
	body := buildBindEnvelope(t, map[string]any{"RtnCode": 1}, time.Now().Unix(), 0)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/ecpay/bind/notify", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	h.Notify(rec, req)

	if got := rec.Body.String(); got != "0|Fail" {
		t.Fatalf("expected %q, got %q", "0|Fail", got)
	}
}

// TestNotify_InvalidDecryptRejects400 驗證非法/無法解密的 Data（偽造來源）直接回 400，不視為
// 「封包合法」，不回 1|OK。
func TestNotify_InvalidDecryptRejects400(t *testing.T) {
	h := testBindHandlerNoDB()
	env := bindRespEnvelope{
		MerchantID: "3002607",
		RpHeader:   bindRpHeader{Timestamp: time.Now().Unix()},
		TransCode:  1,
		TransMsg:   "Success",
		Data:       "not-valid-base64-aes-ciphertext!!",
	}
	body, _ := json.Marshal(env)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/ecpay/bind/notify", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	h.Notify(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for undecryptable Data, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() == "1|OK" {
		t.Fatal("must not respond 1|OK for a packet that failed to decrypt (untrusted source)")
	}
}

// TestNotify_TimestampOutsideSkewRejects400 驗證重放防護：RpHeader.Timestamp 與現在時間差
// 超過 10 分鐘即拒收（400，不回 1|OK），即使 Data 本身能正確解密。
func TestNotify_TimestampOutsideSkewRejects400(t *testing.T) {
	h := testBindHandlerNoDB()
	staleTimestamp := time.Now().Add(-20 * time.Minute).Unix()
	body := buildBindEnvelope(t, map[string]any{"RtnCode": 1}, staleTimestamp, 1)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/ecpay/bind/notify", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	h.Notify(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for stale timestamp, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() == "1|OK" {
		t.Fatal("must not respond 1|OK for a replayed/stale-timestamp packet")
	}
}

// TestNotify_TimestampWithinSkewAccepted 驗證合理範圍內（如 5 分鐘前）的時間戳不會被誤拒。
func TestNotify_TimestampWithinSkewAccepted(t *testing.T) {
	h := testBindHandlerNoDB()
	recentTimestamp := time.Now().Add(-5 * time.Minute).Unix()
	body := buildBindEnvelope(t, map[string]any{"RtnCode": 1}, recentTimestamp, 1)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/ecpay/bind/notify", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	h.Notify(rec, req)

	if got := rec.Body.String(); got != "1|OK" {
		t.Fatalf("expected %q for timestamp within skew, got %q (code=%d)", "1|OK", got, rec.Code)
	}
}

// TestResult_ResultDataFormFormat 驗證 OrderResultURL 的表單欄位格式：ResultData 是 JSON 字串
// （非直接 AES 加密），需先 JSON 解析取外層 {TransCode, Data}，再對 Data 解密——這是官方文件點名
// 最常見的串接錯誤（直接對 ResultData 本身解密）。用 OrderInfo 缺漏的合法封包觸發安全的失敗導向，
// 驗證整條解析路徑在不需要 DB 的情況下能跑通並正確導向 fail（因為 handleBindWebhookData 在
// OrderInfo 缺漏時回 false）。
func TestResult_ResultDataFormFormat(t *testing.T) {
	h := testBindHandlerNoDB()
	envBytes := buildBindEnvelope(t, map[string]any{"RtnCode": 1, "RtnMsg": "Succeeded"}, time.Now().Unix(), 1)

	form := url.Values{"ResultData": {string(envBytes)}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/ecpay/bind/result", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.Result(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://www.dor.tw/?vip_bind=fail" {
		t.Fatalf("expected redirect to fail page (OrderInfo missing → handleBindWebhookData returns false), got %q", loc)
	}
}

// TestResult_DirectAESOnResultDataFails 反向驗證「常見陷阱」：如果誤把 ResultData 整包直接當
// AES 密文解密（跳過 JSON 解析外層），一定會失敗——證明本實作的兩步驟（先 JSON 解外層，再對
// Data 欄位解密）是必要的，不是隨意選一種寫法都行。
func TestResult_DirectAESOnResultDataFails(t *testing.T) {
	envBytes := buildBindEnvelope(t, map[string]any{"RtnCode": 1}, time.Now().Unix(), 1)
	if _, err := ecpayAESDecrypt(testBindHashKey, testBindHashIV, string(envBytes)); err == nil {
		t.Fatal("expected direct AES-decrypt of the full ResultData JSON envelope to fail (it is JSON, not raw ciphertext) — " +
			"if this ever succeeds, the two-step parse in Result() is not actually necessary and should be revisited")
	}
}

// TestResult_MalformedResultDataRedirectsFail 驗證非 JSON 的 ResultData 安全導向失敗頁，不會 500。
func TestResult_MalformedResultDataRedirectsFail(t *testing.T) {
	h := testBindHandlerNoDB()
	form := url.Values{"ResultData": {"not-json-at-all"}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/ecpay/bind/result", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.Result(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect even for malformed ResultData, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://www.dor.tw/?vip_bind=fail" {
		t.Fatalf("expected fail redirect, got %q", loc)
	}
}

// TestMerchantMemberID 驗證 UUID → 綠界會員識別碼轉換：去除連字號、其餘原樣保留（只含 0-9a-f）。
func TestMerchantMemberID(t *testing.T) {
	got := MerchantMemberID("550e8400-e29b-41d4-a716-446655440000")
	want := "550e8400e29b41d4a716446655440000"
	if got != want {
		t.Fatalf("MerchantMemberID mismatch: want %q got %q", want, got)
	}
	if strings.Contains(got, "-") {
		t.Fatalf("MerchantMemberID must not contain hyphens, got %q", got)
	}
}
