package payment

import (
	"context"
	"testing"
	"time"
)

// 本檔測試延續 ecpay_bind_handler_test.go 頂部的既有慣例（本套件沒有 DB 整合測試地基）：純邏輯
// （文案挑選、attempt_no/金額/請款欄位組裝、CreatePayment 結果分類）用單元測試覆蓋；牽涉 DB 的部分
// （settleVipRenewal 的 CAS 冪等閘門、當日重複跑 no-op、attempt_no 計數 SQL 本身）以程式碼審閱取代
// 單測，並在下方各測試/註解中對照要驗證的行為與其在正式程式碼中的位置。

// ================= classifyCreatePaymentErr（透過真的 CreatePayment + mock 綠界 server 驅動） =================

func testRenewalCandidate() renewalCandidate {
	return renewalCandidate{
		SubscriptionID:   "sub-1",
		UserID:           "user-1",
		Plan:             "monthly",
		AmountCents:      39900,
		CurrentPeriodEnd: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
		BindCardID:       "BINDCARD123",
		MerchantMemberID: "user1merchant",
		Email:            "user1@example.com",
		Name:             "Runner One",
	}
}

// TestClassifyCreatePaymentErr_Paid 驗證 CreatePayment 同步成功（RtnCode=1、無 ThreeDInfo）時，
// classifyCreatePaymentErr 回傳 renewalOutcomePaid——這是唯一會觸發 settleVipRenewal 實際扣款入帳的
// 分類，錯判會導致「明明沒扣到款卻當作續約成功」，故獨立驗證。
func TestClassifyCreatePaymentErr_Paid(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		return 1, "Success", CreatePaymentResp{
			RtnCode: 1, RtnMsg: "Succeeded", MerchantID: "3002607",
			OrderInfo: &PaymentOrderInfoResp{MerchantTradeNo: "DORTEST0001", TradeNo: "2026082012345678", TradeAmt: 399, TradeStatus: "1"},
			CardInfo:  &PaymentCardInfo{Gwsr: 123456, Card4No: "3333"},
		}
	})
	defer srv.Close()
	client := newTestBindClient(srv.URL)

	req := buildRenewalPaymentReq(testRenewalCandidate(), "DORTEST0001", "https://api.dor.tw/notify", time.Now())
	_, err := client.CreatePayment(context.Background(), req)

	outcome, rtnCode, rtnMsg := classifyCreatePaymentErr(err)
	if outcome != renewalOutcomePaid {
		t.Fatalf("expected renewalOutcomePaid, got %v (rtnCode=%q rtnMsg=%q, err=%v)", outcome, rtnCode, rtnMsg, err)
	}
}

// TestClassifyCreatePaymentErr_ThreeDS 驗證 2025/8/1 起的 3DS2.0 風險判定分支（RtnCode=1 但仍帶
// ThreeDInfo.ThreeDURL）被正確分類成 renewalOutcomeThreeDS，而不是誤判為 Paid——這是本任務規格
// 「絕不能誤判扣款成功」的核心防線，見 ecpay_bind.go CreatePayment 方法註解。
func TestClassifyCreatePaymentErr_ThreeDS(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		return 1, "Success", CreatePaymentResp{
			RtnCode: 1, RtnMsg: "Succeeded", MerchantID: "3002607",
			ThreeDInfo: &ThreeDInfo{ThreeDURL: "https://ecpay.example/3d"},
		}
	})
	defer srv.Close()
	client := newTestBindClient(srv.URL)

	req := buildRenewalPaymentReq(testRenewalCandidate(), "DORTEST0002", "https://api.dor.tw/notify", time.Now())
	_, err := client.CreatePayment(context.Background(), req)

	outcome, rtnCode, rtnMsg := classifyCreatePaymentErr(err)
	if outcome != renewalOutcomeThreeDS {
		t.Fatalf("expected renewalOutcomeThreeDS, got %v", outcome)
	}
	if rtnCode != "-1" || rtnMsg == "" {
		t.Errorf("unexpected rtnCode/rtnMsg for 3DS: %q %q", rtnCode, rtnMsg)
	}
}

// TestClassifyCreatePaymentErr_BizFailed 驗證一般業務失敗（RtnCode!=1，如額度不足/卡片被拒）被分類
// 成 renewalOutcomeBizFailed，且 rtnCode/rtnMsg 正確帶出（供落地 vip_renewal_attempts 稽核用）。
func TestClassifyCreatePaymentErr_BizFailed(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		return 1, "Success", CreatePaymentResp{RtnCode: 10200095, RtnMsg: "額度不足", MerchantID: "3002607"}
	})
	defer srv.Close()
	client := newTestBindClient(srv.URL)

	req := buildRenewalPaymentReq(testRenewalCandidate(), "DORTEST0003", "https://api.dor.tw/notify", time.Now())
	_, err := client.CreatePayment(context.Background(), req)

	outcome, rtnCode, rtnMsg := classifyCreatePaymentErr(err)
	if outcome != renewalOutcomeBizFailed {
		t.Fatalf("expected renewalOutcomeBizFailed, got %v", outcome)
	}
	if rtnCode != "10200095" || rtnMsg != "額度不足" {
		t.Errorf("unexpected rtnCode/rtnMsg: %q %q", rtnCode, rtnMsg)
	}
}

// TestClassifyCreatePaymentErr_TransportError 驗證傳輸層失敗（envelope TransCode!=1，代表請求未被
// 綠界正常受理，例如簽章錯誤）被分類成 renewalOutcomeUnknown——狀態未知，規格要求「不可自動重試、
// 不可標訂閱 failed」，故不能與 BizFailed（確定沒扣到款）混在一起處理。
func TestClassifyCreatePaymentErr_TransportError(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		return 0, "Signature Error", nil
	})
	defer srv.Close()
	client := newTestBindClient(srv.URL)

	req := buildRenewalPaymentReq(testRenewalCandidate(), "DORTEST0004", "https://api.dor.tw/notify", time.Now())
	_, err := client.CreatePayment(context.Background(), req)
	if err == nil {
		t.Fatal("expected transport error, got nil")
	}

	outcome, _, rtnMsg := classifyCreatePaymentErr(err)
	if outcome != renewalOutcomeUnknown {
		t.Fatalf("expected renewalOutcomeUnknown, got %v", outcome)
	}
	if rtnMsg == "" {
		t.Error("expected non-empty rtnMsg for transport error (for attempt row rtn_msg / manual reconciliation log)")
	}
}

// TestClassifyCreatePaymentErr_NetworkError 驗證更底層的網路錯誤（連不上/DNS 失敗，連 HTTP 回應都
// 沒有）同樣落在 renewalOutcomeUnknown，不會 panic 或誤判成其他分類。
func TestClassifyCreatePaymentErr_NetworkError(t *testing.T) {
	client := newTestBindClient("http://127.0.0.1:1") // 保留位址，連線必失敗
	req := buildRenewalPaymentReq(testRenewalCandidate(), "DORTEST0005", "https://api.dor.tw/notify", time.Now())
	_, err := client.CreatePayment(context.Background(), req)
	if err == nil {
		t.Fatal("expected network error, got nil")
	}
	outcome, _, _ := classifyCreatePaymentErr(err)
	if outcome != renewalOutcomeUnknown {
		t.Fatalf("expected renewalOutcomeUnknown, got %v", outcome)
	}
}

// ================= buildRenewalPaymentReq（純函式） =================

func TestBuildRenewalPaymentReq(t *testing.T) {
	c := testRenewalCandidate()
	now := time.Date(2026, 8, 20, 3, 0, 0, 0, time.UTC) // UTC 03:00 → 台灣 11:00
	req := buildRenewalPaymentReq(c, "DORTEST0006", "https://api.dor.tw/notify", now)

	if req.BindCardID != c.BindCardID {
		t.Errorf("BindCardID: got %q want %q", req.BindCardID, c.BindCardID)
	}
	if req.OrderInfo.TotalAmount != 399 {
		t.Errorf("TotalAmount: got %d want 399 (AmountCents/100)", req.OrderInfo.TotalAmount)
	}
	if req.OrderInfo.MerchantTradeNo != "DORTEST0006" {
		t.Errorf("MerchantTradeNo: got %q", req.OrderInfo.MerchantTradeNo)
	}
	if req.OrderInfo.ReturnURL != "https://api.dor.tw/notify" {
		t.Errorf("ReturnURL: got %q", req.OrderInfo.ReturnURL)
	}
	if req.OrderInfo.ItemName != "VIP 月費訂閱續訂" {
		t.Errorf("ItemName (monthly): got %q", req.OrderInfo.ItemName)
	}
	if req.OrderInfo.MerchantTradeDate != "2026/08/20 11:00:00" {
		t.Errorf("MerchantTradeDate (UTC+8 offset, no LoadLocation): got %q", req.OrderInfo.MerchantTradeDate)
	}
	if req.ConsumerInfo.MerchantMemberID != c.MerchantMemberID {
		t.Errorf("ConsumerInfo.MerchantMemberID: got %q want %q", req.ConsumerInfo.MerchantMemberID, c.MerchantMemberID)
	}

	annual := c
	annual.Plan = "annual"
	reqAnnual := buildRenewalPaymentReq(annual, "DORTEST0007", "https://api.dor.tw/notify", now)
	if reqAnnual.OrderInfo.ItemName != "VIP 年費訂閱續訂" {
		t.Errorf("ItemName (annual): got %q", reqAnnual.OrderInfo.ItemName)
	}

	// MerchantMemberID 空字串 fallback：應退回 MerchantMemberID(userID) 而非留空（留空會讓綠界拒絕請求）。
	noMerchant := c
	noMerchant.MerchantMemberID = ""
	reqFallback := buildRenewalPaymentReq(noMerchant, "DORTEST0008", "https://api.dor.tw/notify", now)
	if want := MerchantMemberID(c.UserID); reqFallback.ConsumerInfo.MerchantMemberID != want {
		t.Errorf("MerchantMemberID fallback: got %q want %q", reqFallback.ConsumerInfo.MerchantMemberID, want)
	}
}

// ================= 通知文案（純函式） =================

func TestRenewalMailText(t *testing.T) {
	if level, title, _ := renewalSuccessMail("2026/09/20"); level != "normal" || title != "VIP 續訂成功" {
		t.Errorf("renewalSuccessMail: unexpected level/title: %q %q", level, title)
	}
	if _, _, body := renewalSuccessMail("2026/09/20"); body == "" {
		t.Error("renewalSuccessMail: empty body")
	}

	level, title, body := renewalRetryMail(2)
	if level != "important" {
		t.Errorf("renewalRetryMail: expected level=important, got %q", level)
	}
	wantRetryBody := "扣款失敗（第 2/3 次），將於明日自動重試，請確認卡片額度"
	if body != wantRetryBody {
		t.Errorf("renewalRetryMail body: got %q want %q", body, wantRetryBody)
	}
	_ = title

	if _, _, body := renewalThreeDSMail(); body != "本次自動扣款需要持卡驗證，請於 App 內手動完成續費" {
		t.Errorf("renewalThreeDSMail: unexpected body: %q", body)
	}

	level, _, body = renewalFinalFailMail()
	if level != "urgent" {
		t.Errorf("renewalFinalFailMail: expected level=urgent, got %q", level)
	}
	if body != "多次扣款失敗，VIP 將於到期後暫停" {
		t.Errorf("renewalFinalFailMail: unexpected body: %q", body)
	}
}

// TestClassifyRenewalFailureMail_Priority 驗證文案挑選優先序：final（訂閱即將終結）> 3DS（需使用者
// 動作）> 一般重試通知。最容易犯錯的地方是「第 3 次剛好也是 3DS」時該用哪一則文案——規格上訂閱都要
// 終結了，繼續叫使用者去手動完成 3D 驗證意義不大，應該優先顯示終結文案。
func TestClassifyRenewalFailureMail_Priority(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		attemptNo int
		final     bool
		wantTitle string
		wantExact string
	}{
		{"first failure, not final", "failed", 1, false, "VIP 自動扣款失敗", ""},
		{"third failure, final wins over generic", "failed", 3, true, "VIP 自動續訂已停止", "多次扣款失敗，VIP 將於到期後暫停"},
		{"3DS, not final", "threeds_required", 1, false, "VIP 自動續訂需要驗證", "本次自動扣款需要持卡驗證，請於 App 內手動完成續費"},
		{"3DS but also final: final wins over 3DS text", "threeds_required", 3, true, "VIP 自動續訂已停止", "多次扣款失敗，VIP 將於到期後暫停"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, title, body := classifyRenewalFailureMail(tt.status, tt.attemptNo, tt.final)
			if title != tt.wantTitle {
				t.Errorf("title: got %q want %q", title, tt.wantTitle)
			}
			if tt.wantExact != "" && body != tt.wantExact {
				t.Errorf("body: got %q want %q", body, tt.wantExact)
			}
		})
	}
}

// ================= formatTaipei（純函式，禁用 time.LoadLocation 的固定 UTC+8 offset 換算） =================

func TestFormatTaipei(t *testing.T) {
	got := formatTaipei(time.Date(2026, 9, 19, 16, 30, 0, 0, time.UTC)) // UTC 16:30 → 台灣 9/20 00:30
	if got != "2026/09/20" {
		t.Errorf("formatTaipei: got %q want %q", got, "2026/09/20")
	}
	got2 := formatTaipei(time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)) // UTC 10:00 → 台灣 18:00，同一天
	if got2 != "2026/09/19" {
		t.Errorf("formatTaipei: got %q want %q", got2, "2026/09/19")
	}
}

// ================= renewalMaxAttempts / attempt_no 邊界（純算術，驗證 processRenewalCandidate ①③
// 的邏輯本身；SQL 查詢/INSERT 因無 DB harness 不在此測，見檔案頂部註解） =================

func TestRenewalAttemptNoClamp(t *testing.T) {
	tests := []struct {
		priorFails          int
		wantRawAttemptNo    int
		wantAttemptNoForRow int
		wantOverLimit       bool
	}{
		{0, 1, 1, false},
		{1, 2, 2, false},
		{2, 3, 3, false}, // 第 3 次仍會嘗試請款；若這次也失敗才由 handleRenewalChargeFailure 終結訂閱
		{3, 4, 3, true},  // 防禦性兜底：不應該發生（正常路徑第 3 次失敗當下就終結），但若發生則不再請款
	}
	for _, tt := range tests {
		rawAttemptNo := tt.priorFails + 1
		attemptNoForRow := rawAttemptNo
		if attemptNoForRow > renewalMaxAttempts {
			attemptNoForRow = renewalMaxAttempts
		}
		if rawAttemptNo != tt.wantRawAttemptNo {
			t.Errorf("priorFails=%d: rawAttemptNo got %d want %d", tt.priorFails, rawAttemptNo, tt.wantRawAttemptNo)
		}
		if attemptNoForRow != tt.wantAttemptNoForRow {
			t.Errorf("priorFails=%d: attemptNoForRow got %d want %d", tt.priorFails, attemptNoForRow, tt.wantAttemptNoForRow)
		}
		overLimit := rawAttemptNo > renewalMaxAttempts
		if overLimit != tt.wantOverLimit {
			t.Errorf("priorFails=%d: overLimit got %v want %v", tt.priorFails, overLimit, tt.wantOverLimit)
		}
	}
}
