package notify

// Telegram() 的錯誤文字不得帶出 bot token 或請求網址（https://api.telegram.org/bot<TOKEN>/sendMessage）。
// 背景：http.Client.Do 回傳的 *url.Error 的 Error() 會把完整請求網址原樣寫進文字（`Post "https://…/bot<TOKEN>/…": …`），
// 逾時、DNS 失敗這類再普通不過的錯誤只要被呼叫端 log 起來，token 就進了日誌。
//
// 全部用 in-process 的 RoundTripper（換掉套件變數 httpClient 的 Transport）：不開任何 socket、不連 Telegram。

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testBotToken = "123456:SECRETTOKEN"
	testChatID   = "-1009988776655"
)

// rtFunc 讓函式充當 http.RoundTripper。
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// useTelegramTransport 把套件的 httpClient 換成 in-process 傳輸並設好憑證環境變數，測完還原；回傳傳輸被呼叫的次數。
func useTelegramTransport(t *testing.T, token string, rt func(*http.Request) (*http.Response, error)) *int32 {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", token)
	t.Setenv("TELEGRAM_CHAT_ID", testChatID)
	var calls int32
	old := httpClient.Transport
	httpClient.Transport = rtFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return rt(r)
	})
	t.Cleanup(func() { httpClient.Transport = old })
	return &calls
}

// assertNoSecret 錯誤文字不得含 token（含它的任何片段）、不得含請求網址的特徵（api.telegram.org/bot）。
func assertNoSecret(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected an error, got nil", what)
	}
	msg := err.Error()
	for _, bad := range []string{testBotToken, "SECRETTOKEN", "123456", "api.telegram.org/bot", "/sendMessage\""} {
		if strings.Contains(msg, bad) {
			t.Errorf("%s: error text leaks %q: %s", what, bad, msg)
		}
	}
}

// 最常見的失敗：傳輸層錯誤（DNS、連線被拒…）。*url.Error 的外殼會帶網址，必須被拆掉，底層原因仍要保留可 errors.Is。
func TestTelegram_TransportErrorDoesNotLeakTokenOrURL(t *testing.T) {
	errSim := errors.New("simulated network failure")
	calls := useTelegramTransport(t, testBotToken, func(*http.Request) (*http.Response, error) { return nil, errSim })

	err := Telegram(context.Background(), "hello")
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("transport calls = %d, want 1 (the request must actually go through the in-process transport)", atomic.LoadInt32(calls))
	}
	assertNoSecret(t, err, "transport error")
	if !errors.Is(err, errSim) {
		t.Errorf("the underlying cause must stay reachable through errors.Is: %v", err)
	}
	if !strings.Contains(err.Error(), "simulated network failure") {
		t.Errorf("the underlying reason should still be readable for diagnosis: %v", err)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		t.Errorf("the returned error chain must not contain a *url.Error (its Error() carries the full URL): %v", err)
	}
}

// 逾時（呼叫端 ctx 到期或 Client.Timeout）：外殼拆掉後仍能 errors.Is(context.DeadlineExceeded)。
func TestTelegram_TimeoutDoesNotLeakTokenAndKeepsDeadlineExceeded(t *testing.T) {
	useTelegramTransport(t, testBotToken, func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done() // 模擬卡住的連線：等到 ctx 到期
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := Telegram(ctx, "hello")
	assertNoSecret(t, err, "timeout")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a timeout must stay detectable with errors.Is(err, context.DeadlineExceeded): %v", err)
	}
}

// DNS 失敗：底層錯誤本來就會提到主機名稱（api.telegram.org），但不得連同路徑裡的 token 一起出現。
func TestTelegram_DNSErrorDoesNotLeakToken(t *testing.T) {
	useTelegramTransport(t, testBotToken, func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "api.telegram.org", IsNotFound: true}}
	})
	err := Telegram(context.Background(), "hello")
	assertNoSecret(t, err, "dns error")
	if !strings.Contains(err.Error(), "no such host") {
		t.Errorf("the DNS reason should remain readable: %v", err)
	}
}

// 縱深防禦：連底層（自訂傳輸層、代理）的錯誤文字都帶著完整網址或 token 時，也不得原樣回傳。
func TestTelegram_InnerErrorThatEmbedsTheURLIsStillRedacted(t *testing.T) {
	for name, build := range map[string]func(*http.Request) error{
		"full URL": func(r *http.Request) error { return errors.New("proxy refused " + r.URL.String()) },
		"token":    func(*http.Request) error { return errors.New("bad credentials " + testBotToken) },
		"escaped token": func(*http.Request) error {
			return errors.New("bad credentials " + url.PathEscape(testBotToken))
		},
	} {
		build := build
		t.Run(name, func(t *testing.T) {
			useTelegramTransport(t, testBotToken, func(r *http.Request) (*http.Response, error) { return nil, build(r) })
			assertNoSecret(t, Telegram(context.Background(), "hello"), name)
		})
	}
}

// NewRequestWithContext 失敗（token 含控制字元、非法的 % 跳脫…）：url.Parse 的錯誤文字會附上整個網址，必須換成固定訊息，
// 而且不得送出任何請求。
func TestTelegram_BuildRequestErrorIsGeneric(t *testing.T) {
	for name, token := range map[string]string{
		"control character":      "123456:SECRET\x7fTOKEN",
		"newline":                "123456:SECRET\nTOKEN",
		"invalid percent-escape": "123456:SECRET%zzTOKEN",
		"leading control byte":   "\x01123456:SECRETTOKEN",
	} {
		token := token
		t.Run(name, func(t *testing.T) {
			calls := useTelegramTransport(t, token, func(*http.Request) (*http.Response, error) {
				return nil, errors.New("must not be called")
			})
			err := Telegram(context.Background(), "hello")
			if err == nil {
				t.Fatal("a token that cannot form a URL must produce an error")
			}
			msg := err.Error()
			for _, bad := range []string{"SECRET", "TOKEN", "123456", "api.telegram.org", "parse", "%zz", "\x7f"} {
				if strings.Contains(msg, bad) {
					t.Errorf("build-request error leaks %q: %q", bad, msg)
				}
			}
			if atomic.LoadInt32(calls) != 0 {
				t.Errorf("no request may be sent when the request cannot be built, got %d", atomic.LoadInt32(calls))
			}
		})
	}
}

// 非 2xx：行為不變（狀態碼＋一小段回應內文供診斷），內文萬一回顯 token 也要遮掉。
func TestTelegram_NonSuccessStatusKeepsDiagnosticsButRedactsTheToken(t *testing.T) {
	respond := func(body string) func(*http.Request) (*http.Response, error) {
		return func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
		}
	}

	useTelegramTransport(t, testBotToken, respond(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
	err := Telegram(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "status 401") || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("status errors must keep the status code and the response body: %v", err)
	}
	assertNoSecret(t, err, "status error")

	useTelegramTransport(t, testBotToken, respond(`echo of the request path: /bot`+testBotToken+`/sendMessage`))
	assertNoSecret(t, Telegram(context.Background(), "hello"), "status error whose body echoes the token")
}

// 成功路徑不變：POST 到 /bot<token>/sendMessage、表單帶 chat_id／text／parse_mode=HTML。
func TestTelegram_SuccessSendsTheFormAndReturnsNil(t *testing.T) {
	var gotPath, gotCT, gotForm string
	useTelegramTransport(t, testBotToken, func(r *http.Request) (*http.Response, error) {
		gotPath, gotCT = r.URL.Host+r.URL.Path, r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotForm = string(b)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: http.Header{}}, nil
	})
	if err := Telegram(context.Background(), "hello <b>world</b>"); err != nil {
		t.Fatalf("success path returned %v", err)
	}
	if gotPath != "api.telegram.org/bot"+testBotToken+"/sendMessage" {
		t.Errorf("request target = %q", gotPath)
	}
	if gotCT != "application/x-www-form-urlencoded" {
		t.Errorf("content type = %q", gotCT)
	}
	form, _ := url.ParseQuery(gotForm)
	if form.Get("chat_id") != testChatID || form.Get("text") != "hello <b>world</b>" || form.Get("parse_mode") != "HTML" {
		t.Errorf("form = %v", form)
	}
}

// 憑證沒設定：直接 no-op、不送請求、不回錯誤。
func TestTelegram_WithoutCredentialsIsANoop(t *testing.T) {
	calls := useTelegramTransport(t, "", func(*http.Request) (*http.Response, error) { return nil, errors.New("must not be called") })
	if err := Telegram(context.Background(), "hello"); err != nil {
		t.Fatalf("no credentials must be a silent no-op, got %v", err)
	}
	t.Setenv("TELEGRAM_BOT_TOKEN", testBotToken)
	t.Setenv("TELEGRAM_CHAT_ID", "")
	if err := Telegram(context.Background(), "hello"); err != nil {
		t.Fatalf("missing chat id must be a silent no-op, got %v", err)
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Fatalf("no request may be sent without credentials, got %d", atomic.LoadInt32(calls))
	}
}
