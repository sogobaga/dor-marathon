package emailbroadcast

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnsubToken_DeterministicAndSecretBound(t *testing.T) {
	tok1 := unsubToken("secret-a", "user-123")
	tok2 := unsubToken("secret-a", "user-123")
	if tok1 != tok2 {
		t.Fatalf("token should be deterministic for same secret+userID, got %q vs %q", tok1, tok2)
	}
	if len(tok1) != unsubTokenLen {
		t.Fatalf("expected token length %d, got %d (%q)", unsubTokenLen, len(tok1), tok1)
	}

	tokOtherUser := unsubToken("secret-a", "user-456")
	if tok1 == tokOtherUser {
		t.Fatal("token must differ across user IDs")
	}

	tokOtherSecret := unsubToken("secret-b", "user-123")
	if tok1 == tokOtherSecret {
		t.Fatal("token must differ across secrets (cannot be forged without JWTSecret)")
	}
}

func TestVerifyUnsubToken(t *testing.T) {
	secret := "test-jwt-secret"
	userID := "11111111-1111-1111-1111-111111111111"
	valid := unsubToken(secret, userID)

	if !verifyUnsubToken(secret, userID, valid) {
		t.Fatal("expected valid token to verify")
	}
	if verifyUnsubToken(secret, userID, "deadbeefdeadbeef") {
		t.Fatal("forged/wrong token must not verify")
	}
	if verifyUnsubToken(secret, "other-user", valid) {
		t.Fatal("token for a different userID must not verify")
	}
	if verifyUnsubToken("wrong-secret", userID, valid) {
		t.Fatal("token signed with a different secret must not verify")
	}
	if verifyUnsubToken(secret, "", valid) {
		t.Fatal("empty userID must not verify")
	}
	if verifyUnsubToken(secret, userID, "") {
		t.Fatal("empty token must not verify")
	}
}

func TestUnsubscribe_InvalidTokenReturns400WithoutDB(t *testing.T) {
	// h.db 刻意留 nil：驗證失敗必須在碰觸 DB 之前就回應，否則這個測試會直接 panic（nil pgxpool）。
	h := &Handler{jwtSecret: "secret", unsubBase: "https://www.dor.tw/api/v1/email/unsubscribe"}

	req := httptest.NewRequest(http.MethodGet, "/unsubscribe?u=some-user&t=bogus-token", nil)
	w := httptest.NewRecorder()
	h.Unsubscribe(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid token, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "連結無效") {
		t.Fatalf("expected error page content, got: %s", w.Body.String())
	}
}

func TestUnsubscribe_MissingParamsReturns400WithoutDB(t *testing.T) {
	h := &Handler{jwtSecret: "secret", unsubBase: "https://www.dor.tw/api/v1/email/unsubscribe"}

	req := httptest.NewRequest(http.MethodGet, "/unsubscribe", nil)
	w := httptest.NewRecorder()
	h.Unsubscribe(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing params, got %d", w.Code)
	}
}

func TestUnsubscribeURL_ContainsValidToken(t *testing.T) {
	h := &Handler{jwtSecret: "secret", unsubBase: "https://www.dor.tw/api/v1/email/unsubscribe"}
	userID := "22222222-2222-2222-2222-222222222222"
	u := h.unsubscribeURL(userID)

	if !strings.HasPrefix(u, "https://www.dor.tw/api/v1/email/unsubscribe?u="+userID+"&t=") {
		t.Fatalf("unexpected unsubscribe URL shape: %s", u)
	}
	// 確認 URL 裡的 token 真的能通過驗證（不是隨便組字串）。
	idx := strings.Index(u, "&t=")
	token := u[idx+3:]
	if !verifyUnsubToken(h.jwtSecret, userID, token) {
		t.Fatalf("token embedded in unsubscribeURL failed verification: %s", u)
	}
}

func TestNewHandler_TrimsTrailingSlashInFrontendURL(t *testing.T) {
	h := NewHandler(nil, "secret", "https://www.dor.tw/")
	if h.unsubBase != "https://www.dor.tw/api/v1/email/unsubscribe" {
		t.Fatalf("expected trailing slash trimmed, got %q", h.unsubBase)
	}
}

func TestBuildEmailHTML_EscapesSubjectKeepsBodyHTMLRawIncludesUnsubLink(t *testing.T) {
	html := buildEmailHTML(`<script>alert(1)</script>`, `<p>hello <b>world</b></p>`, "https://www.dor.tw/api/v1/email/unsubscribe?u=1&t=abc")

	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Fatal("subject must be HTML-escaped, found raw <script> tag")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatal("expected escaped subject to appear in output")
	}
	if !strings.Contains(html, "<p>hello <b>world</b></p>") {
		t.Fatal("body_html should be embedded as raw HTML (admin-authored, trusted, settings-perm gated)")
	}
	if !strings.Contains(html, "https://www.dor.tw/api/v1/email/unsubscribe?u=1&amp;t=abc") {
		t.Fatal("expected unsubscribe link to be embedded in the email HTML")
	}
	if !strings.Contains(html, "service@dor.tw") || !strings.Contains(html, "83005678") {
		t.Fatal("expected footer contact policy: Email + 統一編號, no address/phone")
	}
}

func TestBroadcastStatus(t *testing.T) {
	cases := []struct {
		name         string
		sent, failed int
		want         string
	}{
		{"all sent", 10, 0, "done"},
		{"all failed", 0, 10, "failed"},
		{"mixed", 7, 3, "partial"},
		{"nothing attempted (0/0)", 0, 0, "done"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := broadcastStatus(c.sent, c.failed); got != c.want {
				t.Fatalf("broadcastStatus(%d,%d) = %q, want %q", c.sent, c.failed, got, c.want)
			}
		})
	}
}
