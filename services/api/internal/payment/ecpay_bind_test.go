package payment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 測試用 HashKey/HashIV 常數 testBindHashKey/testBindHashIV 沿用 ecpay_aes_test.go 既有宣告
// （同 package，避免重複宣告——go vet 對此會報 redeclared）；其值等同 config.go 的
// ECPAY_BIND_HASH_KEY/ECPAY_BIND_HASH_IV 預設值（綠界測試特店公開憑證，非機敏資料）。

// bindTestHandler 由各測試提供：收到已解密的 request Data（決成 map[string]any 供斷言關鍵欄位），
// 回傳要模擬的 TransCode/TransMsg，以及要加密進 Data 的 response 內容（nil 代表 Data 留空，用於
// 模擬傳輸層失敗、Data 本來就不會有內容的情境）。
type bindTestHandler func(t *testing.T, path string, reqData map[string]any) (transCode int, transMsg string, respData any)

// newBindTestServer 起一個假的綠界站內付2.0 server：解出 request envelope 的 Data → 呼叫 handler
// 取得要回應的內容 → 加密組成 response envelope 回傳，讓測試完全不必打真的綠界端點。
func newBindTestServer(t *testing.T, handler bindTestHandler) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env bindReqEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			t.Fatalf("test server: decode request envelope: %v", err)
		}
		plainJSON, err := ecpayAESDecrypt(testBindHashKey, testBindHashIV, env.Data)
		if err != nil {
			t.Fatalf("test server: decrypt request data: %v", err)
		}
		var reqData map[string]any
		if err := json.Unmarshal([]byte(plainJSON), &reqData); err != nil {
			t.Fatalf("test server: unmarshal request data: %v", err)
		}

		transCode, transMsg, respData := handler(t, r.URL.Path, reqData)

		respEnv := bindRespEnvelope{
			MerchantID: env.MerchantID,
			RpHeader:   bindRpHeader{Timestamp: 1234567890},
			TransCode:  transCode,
			TransMsg:   transMsg,
		}
		if respData != nil {
			respJSON, err := json.Marshal(respData)
			if err != nil {
				t.Fatalf("test server: marshal response data: %v", err)
			}
			encData, err := ecpayAESEncrypt(testBindHashKey, testBindHashIV, string(respJSON))
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

func newTestBindClient(baseURL string) *BindClient {
	c := NewBindClient("stage", "3002607", testBindHashKey, testBindHashIV)
	c.BaseURL = baseURL
	// QueryTrade 走另一個 domain（見 BindClient.QueryURL 欄位註解），測試一律指到同一個假 server——
	// newBindTestServer 只依 r.URL.Path 分派，不管實際打的是哪個 domain。
	c.QueryURL = baseURL
	return c
}

func testCreatePaymentReq(tradeNo string) CreatePaymentReq {
	return CreatePaymentReq{
		BindCardID: "1234567890ABCDEF",
		OrderInfo: PaymentOrderInfoReq{
			MerchantTradeDate: "2026/08/19 12:00:00",
			MerchantTradeNo:   tradeNo,
			TotalAmount:       499,
			ReturnURL:         "https://api.dor.tw/api/v1/payments/ecpay/bind/notify",
			TradeDesc:         "DOR VIP",
			ItemName:          "VIP monthly subscription",
		},
		ConsumerInfo: ConsumerInfo{
			MerchantMemberID: "user_123",
			Email:            "user123@example.com",
		},
	}
}

// 情境 1：CreatePayment 成功——TransCode=1, RtnCode=1，有 CardInfo.Gwsr / OrderInfo.TradeNo。
func TestCreatePaymentSuccess(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		if path != "/Merchant/CreatePaymentWithCardID" {
			t.Errorf("unexpected path: %s", path)
		}
		if reqData["BindCardID"] != "1234567890ABCDEF" {
			t.Errorf("unexpected BindCardID: %v", reqData["BindCardID"])
		}
		orderInfo, _ := reqData["OrderInfo"].(map[string]any)
		if orderInfo["MerchantTradeNo"] != "DORTEST0001" {
			t.Errorf("unexpected OrderInfo.MerchantTradeNo: %v", orderInfo["MerchantTradeNo"])
		}
		if need3D, _ := reqData["Need3D"].(float64); need3D != 0 {
			t.Errorf("expected Need3D=0, got %v", reqData["Need3D"])
		}
		resp := CreatePaymentResp{
			RtnCode:    1,
			RtnMsg:     "Succeeded",
			MerchantID: "3002607",
			OrderInfo: &PaymentOrderInfoResp{
				MerchantTradeNo: "DORTEST0001",
				TradeNo:         "2026081912345678",
				TradeAmt:        499,
				TradeStatus:     "1",
			},
			CardInfo: &PaymentCardInfo{
				AuthCode: "777777",
				Gwsr:     123456,
				Card6No:  "422222",
				Card4No:  "3333",
			},
		}
		return 1, "", resp
	})
	defer srv.Close()

	c := newTestBindClient(srv.URL)
	resp, err := c.CreatePayment(context.Background(), testCreatePaymentReq("DORTEST0001"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil resp")
	}
	if resp.CardInfo == nil || resp.CardInfo.Gwsr != 123456 {
		t.Errorf("unexpected CardInfo: %+v", resp.CardInfo)
	}
	if resp.OrderInfo == nil || resp.OrderInfo.TradeNo != "2026081912345678" {
		t.Errorf("unexpected OrderInfo: %+v", resp.OrderInfo)
	}
}

// 情境 2：CreatePayment 業務失敗——TransCode=1, RtnCode=10100252（額度不足）。
func TestCreatePaymentBizFailure(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		resp := CreatePaymentResp{
			RtnCode:    10100252,
			RtnMsg:     "額度不足",
			MerchantID: "3002607",
		}
		return 1, "", resp
	})
	defer srv.Close()

	c := newTestBindClient(srv.URL)
	resp, err := c.CreatePayment(context.Background(), testCreatePaymentReq("DORTEST0002"))
	if resp == nil {
		t.Fatal("expected non-nil resp even on biz error")
	}
	var bizErr *BindBizError
	if !errors.As(err, &bizErr) {
		t.Fatalf("expected *BindBizError, got %T: %v", err, err)
	}
	if bizErr.RtnCode != 10100252 {
		t.Errorf("unexpected RtnCode: %d", bizErr.RtnCode)
	}
}

// 情境 3：CreatePayment 傳輸失敗——TransCode=0。
func TestCreatePaymentTransportFailure(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		return 0, "Signature Error", nil
	})
	defer srv.Close()

	c := newTestBindClient(srv.URL)
	resp, err := c.CreatePayment(context.Background(), testCreatePaymentReq("DORTEST0003"))
	if resp != nil {
		t.Errorf("expected nil resp on transport error, got %+v", resp)
	}
	var transErr *BindTransportError
	if !errors.As(err, &transErr) {
		t.Fatalf("expected *BindTransportError, got %T: %v", err, err)
	}
	if transErr.TransCode != 0 || transErr.TransMsg != "Signature Error" {
		t.Errorf("unexpected transport error fields: %+v", transErr)
	}
}

// 情境 4：CreatePayment 被要求 3D——RtnCode=1 但回 ThreeDInfo.ThreeDURL、無 CardInfo；
// 不可被當成請款成功。
func TestCreatePayment3DRequired(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		resp := CreatePaymentResp{
			RtnCode:    1,
			RtnMsg:     "Succeeded",
			MerchantID: "3002607",
			ThreeDInfo: &ThreeDInfo{ThreeDURL: "https://ecpg-stage.ecpay.com.tw/3D/xxxx"},
		}
		return 1, "", resp
	})
	defer srv.Close()

	c := newTestBindClient(srv.URL)
	resp, err := c.CreatePayment(context.Background(), testCreatePaymentReq("DORTEST0004"))
	if err == nil {
		t.Fatal("expected error when 3D is required, got nil (must not treat as success)")
	}
	if !errors.Is(err, ErrCreatePayment3DRequired) {
		t.Errorf("expected errors.Is(err, ErrCreatePayment3DRequired) to be true, got %T: %v", err, err)
	}
	if resp == nil || resp.ThreeDInfo == nil || resp.ThreeDInfo.ThreeDURL == "" {
		t.Fatalf("expected resp with ThreeDInfo populated so caller can redirect user, got %+v", resp)
	}
	if resp.CardInfo != nil {
		t.Errorf("expected no CardInfo in 3D-required response, got %+v", resp.CardInfo)
	}
}

// CreateBindCard 成功分支：解出 BindCardID。
func TestCreateBindCardSuccess(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		if path != "/Merchant/CreateBindCard" {
			t.Errorf("unexpected path: %s", path)
		}
		if reqData["BindCardPayToken"] != "TESTTOKEN1234567890" {
			t.Errorf("unexpected BindCardPayToken: %v", reqData["BindCardPayToken"])
		}
		resp := CreateBindCardResp{
			RtnCode:          1,
			RtnMsg:           "Succeeded",
			MerchantID:       "3002607",
			MerchantMemberID: "user_123",
			BindCardID:       "BINDCARDID1234567890",
			IsSameCard:       false,
			OrderInfo: &BindOrderInfo{
				MerchantTradeNo: "DORTEST0005",
				TradeNo:         "2026081912345679",
			},
			CardInfo: &BindCardInfo{
				Card6No:     "422222",
				Card4No:     "3333",
				CardValidYY: "28",
				CardValidMM: "12",
			},
		}
		return 1, "", resp
	})
	defer srv.Close()

	c := newTestBindClient(srv.URL)
	resp, err := c.CreateBindCard(context.Background(), CreateBindCardReq{
		BindCardPayToken: "TESTTOKEN1234567890",
		MerchantMemberID: "user_123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.BindCardID != "BINDCARDID1234567890" {
		t.Errorf("unexpected BindCardID: %s", resp.BindCardID)
	}
	if resp.Is3D() {
		t.Errorf("expected Is3D()==false for non-3D branch, resp=%+v", resp)
	}
}

// CreateBindCard 3D 分支：ThreeDInfo 有值、BindCardID 空，能被正確判別（Is3D()==true）。
func TestCreateBindCard3DBranch(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		resp := CreateBindCardResp{
			RtnCode:    1,
			RtnMsg:     "Succeeded",
			MerchantID: "3002607",
			OrderInfo: &BindOrderInfo{
				MerchantTradeNo: "DORTEST0006",
			},
			ThreeDInfo: &ThreeDInfo{ThreeDURL: "https://ecpg-stage.ecpay.com.tw/3D/yyyy"},
		}
		return 1, "", resp
	})
	defer srv.Close()

	c := newTestBindClient(srv.URL)
	resp, err := c.CreateBindCard(context.Background(), CreateBindCardReq{
		BindCardPayToken: "TESTTOKEN1234567890",
		MerchantMemberID: "user_123",
	})
	if err != nil {
		t.Fatalf("unexpected error (3D branch is still RtnCode=1, a normal flow step, not an error): %v", err)
	}
	if resp.BindCardID != "" {
		t.Errorf("expected empty BindCardID in 3D branch, got %q", resp.BindCardID)
	}
	if !resp.Is3D() {
		t.Errorf("expected Is3D()==true for 3D branch, resp=%+v", resp)
	}
}

// 其餘 3 支端點的基本煙霧測試：確保 JSON 欄位標籤沒寫錯，共用 call() 傳輸層對這 3 支也一樣走得通。

func TestGetTokenSuccess(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		if path != "/Merchant/GetTokenbyBindingCard" {
			t.Errorf("unexpected path: %s", path)
		}
		orderInfo, _ := reqData["OrderInfo"].(map[string]any)
		if orderInfo["MerchantTradeNo"] != "DORTEST0007" {
			t.Errorf("unexpected OrderInfo.MerchantTradeNo: %v", orderInfo["MerchantTradeNo"])
		}
		if _, hasURL := reqData["OrderResultURL"]; !hasURL {
			t.Errorf("expected OrderResultURL at Data top level, reqData=%+v", reqData)
		}
		resp := GetTokenResp{
			RtnCode:         1,
			RtnMsg:          "Succeeded",
			MerchantID:      "3002607",
			Token:           "TOKENABCDEF1234567890",
			TokenExpireDate: "2026/08/19 13:00:00",
		}
		return 1, "", resp
	})
	defer srv.Close()

	c := newTestBindClient(srv.URL)
	resp, err := c.GetToken(context.Background(), GetTokenReq{
		ConsumerInfo: ConsumerInfo{MerchantMemberID: "user_123", Email: "user123@example.com"},
		OrderInfo: GetTokenOrderInfo{
			MerchantTradeDate: "2026/08/19 12:00:00",
			MerchantTradeNo:   "DORTEST0007",
			TotalAmount:       0,
			TradeDesc:         "DOR VIP bind card",
			ItemName:          "Bind card",
			ReturnURL:         "https://api.dor.tw/api/v1/payments/ecpay/bind/return",
		},
		OrderResultURL: "https://www.dor.tw/vip/bind-result",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Token != "TOKENABCDEF1234567890" {
		t.Errorf("unexpected Token: %s", resp.Token)
	}
}

func TestGetTokenRequiresEmailOrPhone(t *testing.T) {
	c := newTestBindClient("http://unused.invalid")
	_, err := c.GetToken(context.Background(), GetTokenReq{
		ConsumerInfo: ConsumerInfo{MerchantMemberID: "user_123"},
		OrderInfo: GetTokenOrderInfo{
			MerchantTradeDate: "2026/08/19 12:00:00",
			MerchantTradeNo:   "DORTEST0008",
			TradeDesc:         "DOR VIP bind card",
			ItemName:          "Bind card",
			ReturnURL:         "https://api.dor.tw/api/v1/payments/ecpay/bind/return",
		},
		OrderResultURL: "https://www.dor.tw/vip/bind-result",
	})
	if err == nil {
		t.Fatal("expected error when both Email and Phone are empty")
	}
}

func TestGetMemberBindCardSuccess(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		if path != "/Merchant/GetMemberBindCard" {
			t.Errorf("unexpected path: %s", path)
		}
		if reqData["MerchantMemberID"] != "user_123" {
			t.Errorf("unexpected MerchantMemberID: %v", reqData["MerchantMemberID"])
		}
		resp := GetMemberBindCardResp{
			RtnCode:          1,
			RtnMsg:           "Succeeded",
			MerchantID:       "3002607",
			MerchantMemberID: "user_123",
			BindCardList: []BindCardListItem{
				{Card6No: "422222", Card4No: "3333", CardValidYY: "28", CardValidMM: "12", BindCardID: "BINDCARDID1234567890"},
			},
		}
		return 1, "", resp
	})
	defer srv.Close()

	c := newTestBindClient(srv.URL)
	resp, err := c.GetMemberBindCard(context.Background(), "user_123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.BindCardList) != 1 || resp.BindCardList[0].BindCardID != "BINDCARDID1234567890" {
		t.Errorf("unexpected BindCardList: %+v", resp.BindCardList)
	}
}

func TestDeleteMemberBindCardSuccess(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		if path != "/Merchant/DeleteMemberBindCard" {
			t.Errorf("unexpected path: %s", path)
		}
		if reqData["BindCardID"] != "BINDCARDID1234567890" {
			t.Errorf("unexpected BindCardID: %v", reqData["BindCardID"])
		}
		resp := DeleteMemberBindCardResp{RtnCode: 1, RtnMsg: "Succeeded"}
		return 1, "", resp
	})
	defer srv.Close()

	c := newTestBindClient(srv.URL)
	resp, err := c.DeleteMemberBindCard(context.Background(), "BINDCARDID1234567890")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.RtnCode != 1 {
		t.Errorf("unexpected RtnCode: %d", resp.RtnCode)
	}
}

// ================= QueryTrade（Phase D 對抗式審查修正：主動收斂 unknown 扣款狀態） =================

// 情境 1：已付款——RtnCode=1, OrderInfo.TradeStatus="1"。
func TestQueryTradePaid(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		if path != "/1.0.0/Cashier/QueryTrade" {
			t.Errorf("unexpected path: %s (QueryTrade must hit ecpayment domain path, not /Merchant/*)", path)
		}
		if reqData["MerchantTradeNo"] != "DORTEST0009" {
			t.Errorf("unexpected MerchantTradeNo: %v", reqData["MerchantTradeNo"])
		}
		resp := QueryTradeResp{
			RtnCode:    1,
			RtnMsg:     "交易成功",
			MerchantID: "3002607",
			OrderInfo: &QueryTradeOrderInfo{
				MerchantTradeNo: "DORTEST0009",
				TradeNo:         "2026082012345678",
				TradeAmt:        399,
				PaymentType:     "Credit",
				PaymentDate:     "2026/08/20 12:00:00",
				TradeStatus:     "1",
			},
		}
		return 1, "", resp
	})
	defer srv.Close()

	c := newTestBindClient(srv.URL)
	resp, err := c.QueryTrade(context.Background(), "DORTEST0009")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.OrderInfo == nil || resp.OrderInfo.TradeStatus != "1" {
		t.Fatalf("unexpected resp: %+v", resp)
	}
	if resp.OrderInfo.TradeNo != "2026082012345678" {
		t.Errorf("unexpected TradeNo: %q", resp.OrderInfo.TradeNo)
	}
}

// 情境 2：未付款——RtnCode=1, OrderInfo.TradeStatus="0"（訂單存在，但尚未完成付款）。
func TestQueryTradeUnpaid(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		resp := QueryTradeResp{
			RtnCode:    1,
			RtnMsg:     "交易查詢成功",
			MerchantID: "3002607",
			OrderInfo: &QueryTradeOrderInfo{
				MerchantTradeNo: "DORTEST0010",
				TradeStatus:     "0",
			},
		}
		return 1, "", resp
	})
	defer srv.Close()

	c := newTestBindClient(srv.URL)
	resp, err := c.QueryTrade(context.Background(), "DORTEST0010")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.OrderInfo == nil || resp.OrderInfo.TradeStatus != "0" {
		t.Fatalf("unexpected resp: %+v", resp)
	}
}

// 情境 3：查無此筆訂單——RtnCode!=1（業務層失敗），回 *BindBizError，同其餘 5 支端點慣例。
func TestQueryTradeNotFound(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		resp := QueryTradeResp{RtnCode: 10200047, RtnMsg: "查無此訂單", MerchantID: "3002607"}
		return 1, "", resp
	})
	defer srv.Close()

	c := newTestBindClient(srv.URL)
	resp, err := c.QueryTrade(context.Background(), "DORTEST0011")
	var bizErr *BindBizError
	if !errors.As(err, &bizErr) {
		t.Fatalf("expected *BindBizError, got %T: %v", err, err)
	}
	if bizErr.RtnCode != 10200047 {
		t.Errorf("unexpected RtnCode: %d", bizErr.RtnCode)
	}
	if resp == nil {
		t.Fatal("expected non-nil resp even on biz error (same convention as other 5 endpoints)")
	}
}

// 情境 4：查詢本身失敗（傳輸層）——TransCode!=1，回 *BindTransportError，與「查無/未付」須被呼叫端
// 分開處理（見 ecpay_bind.go QueryTrade 方法註解／vip_renewal.go classifyQueryTradeResult）。
func TestQueryTradeTransportFailure(t *testing.T) {
	srv := newBindTestServer(t, func(t *testing.T, path string, reqData map[string]any) (int, string, any) {
		return 0, "Signature Error", nil
	})
	defer srv.Close()

	c := newTestBindClient(srv.URL)
	resp, err := c.QueryTrade(context.Background(), "DORTEST0012")
	var transErr *BindTransportError
	if !errors.As(err, &transErr) {
		t.Fatalf("expected *BindTransportError, got %T: %v", err, err)
	}
	if resp != nil {
		t.Errorf("expected nil resp on transport error, got %+v", resp)
	}
}

func TestQueryTradeRequiresMerchantTradeNo(t *testing.T) {
	c := newTestBindClient("http://unused.invalid")
	if _, err := c.QueryTrade(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty merchantTradeNo")
	}
}
