package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// withResendEnv 暫時設定 RESEND_API_KEY 並把 resendAPIBase 指向 mock server，測完自動還原
// （比照 payment.ecpay_bind_test.go newTestBindClient 的「覆寫 BaseURL 指向 httptest」慣例）。
func withResendEnv(t *testing.T, srv *httptest.Server) {
	t.Helper()
	oldKey := os.Getenv("RESEND_API_KEY")
	oldBase := resendAPIBase
	os.Setenv("RESEND_API_KEY", "re_test_key")
	resendAPIBase = srv.URL
	t.Cleanup(func() {
		os.Setenv("RESEND_API_KEY", oldKey)
		resendAPIBase = oldBase
	})
}

func TestSendEmailBatch_NotConfigured(t *testing.T) {
	oldKey := os.Getenv("RESEND_API_KEY")
	os.Unsetenv("RESEND_API_KEY")
	defer os.Setenv("RESEND_API_KEY", oldKey)

	err := SendEmailBatch(context.Background(), "a@b.com", []EmailMsg{{To: []string{"x@y.com"}, Subject: "s", HTML: "h"}})
	if !errors.Is(err, ErrEmailNotConfigured) {
		t.Fatalf("expected ErrEmailNotConfigured, got %v", err)
	}
}

func TestSendEmailBatch_EmptyMsgsNoop(t *testing.T) {
	// 未設定 RESEND_API_KEY 也應該直接回 nil（不打 API），因為根本沒有東西要寄。
	oldKey := os.Getenv("RESEND_API_KEY")
	os.Unsetenv("RESEND_API_KEY")
	defer os.Setenv("RESEND_API_KEY", oldKey)

	if err := SendEmailBatch(context.Background(), "a@b.com", nil); err != nil {
		t.Fatalf("expected nil error for empty batch, got %v", err)
	}
}

func TestSendEmailBatch_TooLarge(t *testing.T) {
	msgs := make([]EmailMsg, 101)
	for i := range msgs {
		msgs[i] = EmailMsg{To: []string{"x@y.com"}, Subject: "s", HTML: "h"}
	}
	err := SendEmailBatch(context.Background(), "a@b.com", msgs)
	if err == nil {
		t.Fatal("expected error for batch >100, got nil")
	}
}

func TestSendEmailBatch_Success(t *testing.T) {
	var gotAuth, gotMethod, gotPath string
	var gotBody []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"id":"11111111-1111-1111-1111-111111111111"}]}`))
	}))
	defer srv.Close()
	withResendEnv(t, srv)

	err := SendEmailBatch(context.Background(), "DOR <noreply@dor.tw>", []EmailMsg{
		{To: []string{"user@example.com"}, Subject: "hello", HTML: "<p>hi</p>"},
	})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/emails/batch" {
		t.Fatalf("unexpected request: %s %s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer re_test_key" {
		t.Fatalf("expected Bearer auth header, got %q", gotAuth)
	}
	if len(gotBody) != 1 || gotBody[0]["subject"] != "hello" {
		t.Fatalf("unexpected payload: %+v", gotBody)
	}
}

func TestSendEmailBatch_QuotaExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"statusCode":429,"name":"daily_quota_exceeded","message":"You have reached your daily quota."}`))
	}))
	defer srv.Close()
	withResendEnv(t, srv)

	err := SendEmailBatch(context.Background(), "a@b.com", []EmailMsg{{To: []string{"x@y.com"}, Subject: "s", HTML: "h"}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !IsQuotaExceeded(err) {
		t.Fatalf("expected IsQuotaExceeded=true, got err=%v", err)
	}
	var rerr *ResendAPIError
	if !errors.As(err, &rerr) || rerr.StatusCode != 429 || rerr.Name != "daily_quota_exceeded" {
		t.Fatalf("expected ResendAPIError with daily_quota_exceeded, got %+v", err)
	}
}

func TestSendEmailBatch_RateLimitIsNotQuota(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"statusCode":429,"name":"rate_limit_exceeded","message":"Too many requests."}`))
	}))
	defer srv.Close()
	withResendEnv(t, srv)

	err := SendEmailBatch(context.Background(), "a@b.com", []EmailMsg{{To: []string{"x@y.com"}, Subject: "s", HTML: "h"}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if IsQuotaExceeded(err) {
		t.Fatalf("rate_limit_exceeded should NOT be treated as quota exceeded, got IsQuotaExceeded=true")
	}
}

func TestSendEmailBatch_ServerErrorFallbackRawBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	withResendEnv(t, srv)

	err := SendEmailBatch(context.Background(), "a@b.com", []EmailMsg{{To: []string{"x@y.com"}, Subject: "s", HTML: "h"}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if IsQuotaExceeded(err) {
		t.Fatalf("non-JSON 500 should not be classified as quota exceeded")
	}
}

func TestDefaultFrom(t *testing.T) {
	old := os.Getenv("EMAIL_FROM")
	defer os.Setenv("EMAIL_FROM", old)

	os.Unsetenv("EMAIL_FROM")
	if got := DefaultFrom(); got != "DOR 跑步平台 <noreply@dor.tw>" {
		t.Fatalf("expected default from, got %q", got)
	}

	os.Setenv("EMAIL_FROM", "Custom <custom@dor.tw>")
	if got := DefaultFrom(); got != "Custom <custom@dor.tw>" {
		t.Fatalf("expected env override, got %q", got)
	}
}
