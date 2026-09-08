package reqip

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 這幾個測試都假設 ORIGIN_VERIFY_SECRET 未設定（本套件的 originVerifySecret 在測試環境的預設
// 值，見 go.mod 沒有任何機制在 test 執行前設這個環境變數）——originVerifySecret 是套件變數，
// 在 package 初始化時就讀一次 os.Getenv，測試中途用 os.Setenv 改不了它，所以這裡不測試
// 「密鑰已設定且比對成功/失敗」那一半（那段判斷邏輯本身在這次修法之前就存在、沒有變動）。

func TestComputeClientIP_PrefersCFConnectingIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("CF-Connecting-IP", "203.0.113.9")
	r.Header.Set("X-Forwarded-For", "10.0.0.1, 10.0.0.2")
	r.RemoteAddr = "192.0.2.1:12345"

	if got := computeClientIP(r); got != "203.0.113.9" {
		t.Errorf("computeClientIP() = %q, want CF-Connecting-IP value", got)
	}
}

// 2026-09-08 audit finding 5：沒有 CF-Connecting-IP 時取 X-Forwarded-For 的「最右側」一段
// （Railway 代理層自己附加、看到的來源），不是最左側（客戶端可任意偽造塞好幾段）。
func TestComputeClientIP_FallsBackToRightmostXFF(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8, 203.0.113.9")
	r.RemoteAddr = "192.0.2.1:12345"

	if got := computeClientIP(r); got != "203.0.113.9" {
		t.Errorf("computeClientIP() = %q, want rightmost XFF entry", got)
	}
}

func TestComputeClientIP_TrimsWhitespaceInXFF(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-For", "1.2.3.4,  203.0.113.9  ")
	r.RemoteAddr = "192.0.2.1:12345"

	if got := computeClientIP(r); got != "203.0.113.9" {
		t.Errorf("computeClientIP() = %q, want trimmed rightmost XFF entry", got)
	}
}

func TestComputeClientIP_FallsBackToRemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.1:12345"

	if got := computeClientIP(r); got != "192.0.2.1" {
		t.Errorf("computeClientIP() = %q, want RemoteAddr host", got)
	}
}

// net.SplitHostPort 對沒有 port 的裸位址（bare IPv6 或已經被上游改寫過的值）會回傳 error，
// computeClientIP 此時應該原樣回傳整個 r.RemoteAddr（不裁切、不 panic）。
func TestComputeClientIP_BareRemoteAddrWithoutPort(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "::1"

	if got := computeClientIP(r); got != "::1" {
		t.Errorf("computeClientIP() = %q, want raw RemoteAddr when it has no port", got)
	}
}

func TestMiddleware_SetsContextAndRemoteAddr(t *testing.T) {
	var gotFromClientIP string
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFromClientIP = ClientIP(r)
		if r.RemoteAddr != "203.0.113.9" {
			t.Errorf("r.RemoteAddr = %q, want overwritten to the computed IP", r.RemoteAddr)
		}
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("CF-Connecting-IP", "203.0.113.9")
	r.RemoteAddr = "192.0.2.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if gotFromClientIP != "203.0.113.9" {
		t.Errorf("ClientIP(r) inside handler = %q, want value computed by Middleware via context", gotFromClientIP)
	}
}

// ClientIP 在沒有經過 Middleware 的請求上（context 沒有值）應該退回直接重算一次，而不是回傳
// 空字串或 panic——例如未掛這個 middleware 的測試請求、或本套件以外呼叫端直接建構的 request。
func TestClientIP_FallsBackWithoutMiddleware(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("CF-Connecting-IP", "203.0.113.9")

	if got := ClientIP(r); got != "203.0.113.9" {
		t.Errorf("ClientIP() without Middleware = %q, want direct computeClientIP result", got)
	}
}
