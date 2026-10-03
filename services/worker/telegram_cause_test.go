package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// errRoundTripper 模擬傳輸層失敗（不開任何真實連線）；fail 的文字可以刻意夾帶網址，驗證縱深防禦。
type errRoundTripper struct{ fail error }

func (e errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, e.fail }

// TestTelegramErrCauseNeverLeaksToken：http.Client.Do 失敗時回的 *url.Error 會把完整網址（含 bot token）印進 Error()；
// notifyAlert 只能記 telegramErrCause 的結果，而它不得帶出 token 或 api.telegram.org/bot 網址。
func TestTelegramErrCauseNeverLeaksToken(t *testing.T) {
	const token = "123456:SECRETTOKEN"
	do := func(fail error) error {
		c := &http.Client{Transport: errRoundTripper{fail: fail}, Timeout: time.Second}
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
			"https://api.telegram.org/bot"+token+"/sendMessage", strings.NewReader("x=1"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Do(req)
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), token) {
			t.Fatalf("precondition: raw *url.Error should contain the token, got %q", err.Error())
		}
		return err
	}
	cases := []struct {
		name string
		fail error
		want string
	}{
		{"plain network failure keeps the cause", errors.New("connection refused"), "connection refused"},
		{"cause that embeds the URL is replaced", errors.New("proxy said: https://api.telegram.org/bot" + token + "/sendMessage unreachable"), "request failed"},
		{"cause that embeds the bare token is replaced", errors.New("bad credential " + token), "request failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := telegramErrCause(do(tc.fail), token)
			if strings.Contains(got, "SECRETTOKEN") || strings.Contains(got, "api.telegram.org/bot") {
				t.Fatalf("leaked secret: %q", got)
			}
			if got != tc.want {
				t.Fatalf("telegramErrCause = %q, want %q", got, tc.want)
			}
		})
	}
	if got := telegramErrCause(errors.New("not a url error "+token), token); got != "request failed" {
		t.Fatalf("non-*url.Error must collapse to a fixed string, got %q", got)
	}
}
