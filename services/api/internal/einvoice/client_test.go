package einvoice

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dor/api/internal/payment"
)

// 官方公開電子發票測試特店憑證（見任務規格 EXISTING STATE：這些是綠界公開的測試值，非機敏資料）。
const (
	testMerchantID = "2000132"
	testHashKey    = "ejCk326UnaZWKisg"
	testHashIV     = "q9jcZX8Ib9LM8wYk"
)

// einvoiceTestHandler 由各測試提供：收到已解密的 request Data（解成 map[string]any 供斷言關鍵欄位），
// 回傳要模擬的 TransCode/TransMsg，以及要加密進 Data 的 response 內容（nil＝Data 留空，模擬傳輸層
// 失敗、Data 本來就不會有內容的情境）。比照 internal/payment/ecpay_bind_test.go 的既有慣例。
type einvoiceTestHandler func(t *testing.T, path string, reqData map[string]any) (transCode int, transMsg string, respData any)

func newEinvoiceTestServer(t *testing.T, handler einvoiceTestHandler) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env reqEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			t.Fatalf("test server: decode request envelope: %v", err)
		}
		if env.RqHeader.Revision != apiRevision {
			t.Fatalf("test server: expected Revision %q, got %q", apiRevision, env.RqHeader.Revision)
		}
		if env.RqHeader.Timestamp == 0 {
			t.Fatalf("test server: expected non-zero Timestamp")
		}
		plainJSON, err := payment.AESDecrypt(testHashKey, testHashIV, env.Data)
		if err != nil {
			t.Fatalf("test server: decrypt request data: %v", err)
		}
		var reqData map[string]any
		if err := json.Unmarshal([]byte(plainJSON), &reqData); err != nil {
			t.Fatalf("test server: unmarshal request data: %v", err)
		}
		if reqData["MerchantID"] != testMerchantID {
			t.Fatalf("test server: expected MerchantID %q in decrypted Data, got %v", testMerchantID, reqData["MerchantID"])
		}

		transCode, transMsg, respData := handler(t, r.URL.Path, reqData)

		respEnv := respEnvelope{
			MerchantID: env.MerchantID,
			RpHeader:   rpHeader{Timestamp: 1234567890},
			TransCode:  transCode,
			TransMsg:   transMsg,
		}
		if respData != nil {
			respJSON, err := json.Marshal(respData)
			if err != nil {
				t.Fatalf("test server: marshal response data: %v", err)
			}
			encData, err := payment.AESEncrypt(testHashKey, testHashIV, string(respJSON))
			if err != nil {
				t.Fatalf("test server: encrypt response data: %v", err)
			}
			respEnv.Data = encData
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(respEnv); err != nil {
			t.Fatalf("test server: encode response envelope: %v", err)
		}
	}))
}

func newTestClient(baseURL string) *Client {
	c := NewClient(Config{Env: "stage", MerchantID: testMerchantID, HashKey: testHashKey, HashIV: testHashIV})
	c.BaseURL = baseURL
	return c
}

func TestClient_Issue_Success(t *testing.T) {
	srv := newEinvoiceTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		if path != "/B2CInvoice/Issue" {
			t.Fatalf("unexpected path: %s", path)
		}
		if reqData["RelateNumber"] != "DORabc123" {
			t.Fatalf("unexpected RelateNumber: %v", reqData["RelateNumber"])
		}
		return 1, "", map[string]any{
			"RtnCode": 1, "RtnMsg": "開立成功", "InvoiceNo": "AB12345678",
			"InvoiceDate": "2026-09-06 12:00:00", "RandomNumber": "1234",
		}
	})
	defer srv.Close()

	c := newTestClient(srv.URL)
	resp, err := c.Issue(t.Context(), IssueReq{
		RelateNumber: "DORabc123", Print: "0", Donation: "0", CarrierType: "1",
		TaxType: "1", SalesAmount: 100, InvType: "07",
		Items: []IssueItem{{ItemSeq: 1, ItemName: "test", ItemCount: 1, ItemWord: "式", ItemPrice: 100, ItemTaxType: "1", ItemAmount: 100}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.InvoiceNo != "AB12345678" {
		t.Errorf("expected InvoiceNo AB12345678, got %q", resp.InvoiceNo)
	}
}

// TestClient_Issue_RtnCodeAsString 驗證 FlexInt 容錯解碼：RtnCode 用加引號字串回傳仍能正確判斷成功。
func TestClient_Issue_RtnCodeAsString(t *testing.T) {
	srv := newEinvoiceTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		return 1, "", map[string]any{
			"RtnCode": "1", "RtnMsg": "開立成功", "InvoiceNo": "AB12345678",
			"InvoiceDate": "2026-09-06 12:00:00", "RandomNumber": "1234",
		}
	})
	defer srv.Close()

	c := newTestClient(srv.URL)
	resp, err := c.Issue(t.Context(), IssueReq{RelateNumber: "DORxyz"})
	if err != nil {
		t.Fatalf("expected success with string RtnCode, got error: %v", err)
	}
	if resp.InvoiceNo != "AB12345678" {
		t.Errorf("expected InvoiceNo AB12345678, got %q", resp.InvoiceNo)
	}
}

func TestClient_Issue_BizErrorRtnCodeNot1(t *testing.T) {
	srv := newEinvoiceTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		return 1, "", map[string]any{"RtnCode": 10100058, "RtnMsg": "統一編號錯誤"}
	})
	defer srv.Close()

	c := newTestClient(srv.URL)
	resp, err := c.Issue(t.Context(), IssueReq{RelateNumber: "DORbad"})
	if err == nil {
		t.Fatal("expected business error, got nil")
	}
	var apiErr *APIError
	if !isAPIError(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.TransCode != 1 || apiErr.RtnCode != 10100058 {
		t.Errorf("unexpected APIError fields: %+v", apiErr)
	}
	if resp == nil || resp.RtnMsg != "統一編號錯誤" {
		t.Errorf("expected resp still populated with RtnMsg, got %+v", resp)
	}
}

func TestClient_TransportError_TransCodeNot1(t *testing.T) {
	srv := newEinvoiceTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		return 0, "簽章錯誤", nil
	})
	defer srv.Close()

	c := newTestClient(srv.URL)
	resp, err := c.Issue(t.Context(), IssueReq{RelateNumber: "DORtransport"})
	if err == nil {
		t.Fatal("expected transport error, got nil")
	}
	if resp != nil {
		t.Errorf("expected nil resp on transport error, got %+v", resp)
	}
	var apiErr *APIError
	if !isAPIError(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.TransCode == 1 {
		t.Errorf("expected non-1 TransCode, got %d", apiErr.TransCode)
	}
}

func TestClient_GetIssue_NotFoundIsBizError(t *testing.T) {
	srv := newEinvoiceTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		if path != "/B2CInvoice/GetIssue" {
			t.Fatalf("unexpected path: %s", path)
		}
		return 1, "", map[string]any{"RtnCode": 0, "RtnMsg": "查無資料"}
	})
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.GetIssue(t.Context(), GetIssueReq{RelateNumber: "DORnotfound"})
	if err == nil {
		t.Fatal("expected error for RtnCode!=1")
	}
}

func TestClient_Invalid_Success(t *testing.T) {
	srv := newEinvoiceTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		if path != "/B2CInvoice/Invalid" {
			t.Fatalf("unexpected path: %s", path)
		}
		return 1, "", map[string]any{"RtnCode": 1, "RtnMsg": "作廢成功", "InvoiceNo": reqData["InvoiceNo"]}
	})
	defer srv.Close()

	c := newTestClient(srv.URL)
	resp, err := c.Invalid(t.Context(), InvalidReq{InvoiceNo: "AB12345678", InvoiceDate: "2026-09-06", Reason: "測試作廢"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.InvoiceNo != "AB12345678" {
		t.Errorf("unexpected InvoiceNo: %v", resp.InvoiceNo)
	}
}

func TestClient_Allowance_Success(t *testing.T) {
	srv := newEinvoiceTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		if path != "/B2CInvoice/Allowance" {
			t.Fatalf("unexpected path: %s", path)
		}
		return 1, "", map[string]any{
			"RtnCode": 1, "RtnMsg": "開立折讓成功", "IA_Allow_No": "IA20260906000001",
			"IA_Invoice_No": reqData["InvoiceNo"], "IA_Date": "2026-09-06 13:00:00", "IA_Remain_Allowance_Amt": "0",
		}
	})
	defer srv.Close()

	c := newTestClient(srv.URL)
	resp, err := c.Allowance(t.Context(), AllowanceReq{
		InvoiceNo: "AB12345678", InvoiceDate: "2026-09-06", AllowanceNotify: "E", NotifyMail: "test@dor.tw",
		AllowanceAmount: 100, Items: []AllowanceItem{{ItemSeq: 1, ItemName: "退款", ItemCount: 1, ItemWord: "式", ItemPrice: 100, ItemTaxType: "1", ItemAmount: 100}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.IAAllowNo != "IA20260906000001" {
		t.Errorf("unexpected IAAllowNo: %q", resp.IAAllowNo)
	}
	if int(resp.IARemainAllowanceAmt) != 0 {
		t.Errorf("expected remain allowance amount decoded from string '0', got %d", int(resp.IARemainAllowanceAmt))
	}
}

// TestFlexInt_UnmarshalJSON 直接測 FlexInt 容錯解碼（數字/字串/空字串/null）。
func TestFlexInt_UnmarshalJSON(t *testing.T) {
	cases := []struct {
		json string
		want int
		fail bool
	}{
		{`1`, 1, false},
		{`"1"`, 1, false},
		{`0`, 0, false},
		{`"10100058"`, 10100058, false},
		{`""`, 0, false},
		{`null`, 0, false},
		{`"abc"`, 0, true},
	}
	for _, c := range cases {
		var n FlexInt
		err := json.Unmarshal([]byte(c.json), &n)
		if c.fail {
			if err == nil {
				t.Errorf("json=%s: expected error, got nil", c.json)
			}
			continue
		}
		if err != nil {
			t.Errorf("json=%s: unexpected error: %v", c.json, err)
			continue
		}
		if int(n) != c.want {
			t.Errorf("json=%s: got %d, want %d", c.json, int(n), c.want)
		}
	}
}

// isAPIError 是 errors.As 的薄包裝，避免每個測試各自 import errors 只為了這一行。
func isAPIError(err error, target **APIError) bool {
	ae, ok := err.(*APIError)
	if !ok {
		return false
	}
	*target = ae
	return true
}
