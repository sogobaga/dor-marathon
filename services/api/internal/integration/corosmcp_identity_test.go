package integration

// COROS 帳號識別（corosmcp_identity.go，GA 契約 §2.6）：id_token 驗證（驗簽路徑／無 JWKS 路徑）、
// 各種攻擊與壞資料、JWKS 解析、帳號識別字串衍生。全部用測試時現產的金鑰，不打 COROS。

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "https://mcpus.coros.com"
	testClientID = "client-abc"
)

var idNow = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func rsaJWKS(t *testing.T, kid string, pub *rsa.PublicKey) []byte {
	t.Helper()
	jwks := map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
		"n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes()),
	}}}
	out, _ := json.Marshal(jwks)
	return out
}

func signRS256(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func goodClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"iss": testIssuer, "aud": []string{testClientID}, "sub": "user-12345",
		"exp": idNow.Add(10 * time.Minute).Unix(), "iat": idNow.Add(-time.Minute).Unix(),
	}
}

func TestVerifyCorosIDToken_SignedPath(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	keys, err := parseJWKS(rsaJWKS(t, "k1", &key.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	kf := jwksKeyfunc(keys)

	// 正常
	tok := signRS256(t, key, "k1", goodClaims())
	if sub, err := verifyCorosIDToken(tok, testIssuer, testClientID, idNow, kf); err != nil || sub != "user-12345" {
		t.Fatalf("valid token: (%q, %v)", sub, err)
	}
	// aud 為字串（非陣列）也可
	c := goodClaims()
	c["aud"] = testClientID
	if sub, err := verifyCorosIDToken(signRS256(t, key, "k1", c), testIssuer, testClientID, idNow, kf); err != nil || sub == "" {
		t.Fatalf("string aud: (%q, %v)", sub, err)
	}
	// issuer 結尾斜線容忍
	c = goodClaims()
	c["iss"] = testIssuer + "/"
	if _, err := verifyCorosIDToken(signRS256(t, key, "k1", c), testIssuer, testClientID, idNow, kf); err != nil {
		t.Fatalf("issuer with trailing slash must be tolerated: %v", err)
	}

	bad := map[string]string{
		"wrong signing key": signRS256(t, other, "k1", goodClaims()),
		"unknown kid":       signRS256(t, key, "nope", goodClaims()),
		"wrong audience":    signRS256(t, key, "k1", func() jwt.MapClaims { c := goodClaims(); c["aud"] = []string{"someone-else"}; return c }()),
		"wrong issuer":      signRS256(t, key, "k1", func() jwt.MapClaims { c := goodClaims(); c["iss"] = "https://evil.example.com"; return c }()),
		"expired":           signRS256(t, key, "k1", func() jwt.MapClaims { c := goodClaims(); c["exp"] = idNow.Add(-10 * time.Minute).Unix(); return c }()),
		"no exp":            signRS256(t, key, "k1", func() jwt.MapClaims { c := goodClaims(); delete(c, "exp"); return c }()),
		"no sub":            signRS256(t, key, "k1", func() jwt.MapClaims { c := goodClaims(); delete(c, "sub"); return c }()),
		"empty sub":         signRS256(t, key, "k1", func() jwt.MapClaims { c := goodClaims(); c["sub"] = "  "; return c }()),
		"garbage":           "not.a.jwt",
		"empty segments":    "..",
		"alg none":          b64([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + b64([]byte(`{"iss":"`+testIssuer+`","aud":"`+testClientID+`","sub":"x","exp":9999999999}`)) + ".",
	}
	for name, tk := range bad {
		if sub, err := verifyCorosIDToken(tk, testIssuer, testClientID, idNow, kf); err == nil {
			t.Errorf("%s: must be rejected, got sub=%q", name, sub)
		}
	}
	// 演算法混淆攻擊：用公鑰位元組當 HMAC 密鑰簽 HS256——allowlist 只有 RS256／ES256，必須被拒絕。
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, goodClaims())
	hs.Header["kid"] = "k1"
	hsTok, _ := hs.SignedString(key.PublicKey.N.Bytes())
	if _, err := verifyCorosIDToken(hsTok, testIssuer, testClientID, idNow, kf); err == nil {
		t.Error("HS256 token must be rejected when verifying with a JWKS (algorithm confusion)")
	}
	// 未過期但在容忍範圍內（exp 剛過 1 分鐘，leeway 2 分鐘）
	c = goodClaims()
	c["exp"] = idNow.Add(-time.Minute).Unix()
	if _, err := verifyCorosIDToken(signRS256(t, key, "k1", c), testIssuer, testClientID, idNow, kf); err != nil {
		t.Errorf("1 minute past exp is within the 2-minute leeway: %v", err)
	}
}

func TestVerifyCorosIDToken_ES256(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "EC", "kid": "e1", "use": "sig", "crv": "P-256",
		"x": b64(key.X.FillBytes(make([]byte, 32))), "y": b64(key.Y.FillBytes(make([]byte, 32))),
	}}})
	keys, err := parseJWKS(jwks)
	if err != nil {
		t.Fatal(err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, goodClaims())
	tok.Header["kid"] = "e1"
	s, _ := tok.SignedString(key)
	if sub, err := verifyCorosIDToken(s, testIssuer, testClientID, idNow, jwksKeyfunc(keys)); err != nil || sub != "user-12345" {
		t.Fatalf("ES256: (%q, %v)", sub, err)
	}
}

// 沒有 jwks_uri：只驗 iss／aud／exp（OIDC Core §3.1.3.7(6)：直接從 token 端點經 TLS 取得）。
func TestVerifyCorosIDToken_UnverifiedPathChecksClaims(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	mk := func(mod func(jwt.MapClaims)) string {
		c := goodClaims()
		if mod != nil {
			mod(c)
		}
		return signRS256(t, key, "k1", c) // 簽章不會被驗（沒有 JWKS），但 claims 要驗
	}
	if sub, err := verifyCorosIDToken(mk(nil), testIssuer, testClientID, idNow, nil); err != nil || sub != "user-12345" {
		t.Fatalf("valid claims without jwks: (%q, %v)", sub, err)
	}
	bad := map[string]string{
		"wrong aud": mk(func(c jwt.MapClaims) { c["aud"] = "x" }),
		"wrong iss": mk(func(c jwt.MapClaims) { c["iss"] = "https://other.coros.com" }),
		"expired":   mk(func(c jwt.MapClaims) { c["exp"] = idNow.Add(-time.Hour).Unix() }),
		"no exp":    mk(func(c jwt.MapClaims) { delete(c, "exp") }),
		"no sub":    mk(func(c jwt.MapClaims) { delete(c, "sub") }),
		"garbage":   "xyz",
	}
	for name, tk := range bad {
		if _, err := verifyCorosIDToken(tk, testIssuer, testClientID, idNow, nil); err == nil {
			t.Errorf("%s must be rejected even without signature verification", name)
		}
	}
}

func TestParseJWKS(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	keys, err := parseJWKS(rsaJWKS(t, "k1", &key.PublicKey))
	if err != nil || len(keys) != 1 {
		t.Fatalf("rsa jwks: %v %v", keys, err)
	}
	if pk, ok := keys["k1"].(*rsa.PublicKey); !ok || pk.N.Cmp(key.N) != 0 || pk.E != key.E {
		t.Fatal("rsa public key mismatch")
	}
	for name, body := range map[string]string{
		"not json":       "nope",
		"empty set":      `{"keys":[]}`,
		"only enc keys":  `{"keys":[{"kty":"RSA","use":"enc","n":"AQAB","e":"AQAB"}]}`,
		"bad rsa":        `{"keys":[{"kty":"RSA","kid":"a","n":"!!!","e":"AQAB"}]}`,
		"tiny exponent":  `{"keys":[{"kty":"RSA","kid":"a","n":"AQAB","e":"AQ"}]}`,
		"ec wrong curve": `{"keys":[{"kty":"EC","kid":"a","crv":"P-384","x":"AQ","y":"AQ"}]}`,
		"ec off curve":   `{"keys":[{"kty":"EC","kid":"a","crv":"P-256","x":"AQ","y":"AQ"}]}`,
		"unknown kty":    `{"keys":[{"kty":"oct","kid":"a","k":"AQ"}]}`,
	} {
		if _, err := parseJWKS([]byte(body)); err == nil {
			t.Errorf("%s: must be rejected", name)
		}
	}
	// kid 空字串＋只有一把金鑰：id_token 沒有 kid 也能對應
	one, _ := parseJWKS(rsaJWKS(t, "", &key.PublicKey))
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, goodClaims()) // 沒設 kid header
	s, _ := tok.SignedString(key)
	if _, err := verifyCorosIDToken(s, testIssuer, testClientID, idNow, jwksKeyfunc(one)); err != nil {
		t.Fatalf("single-key JWKS with token lacking kid: %v", err)
	}
}

func TestCorosMcpAccountID(t *testing.T) {
	if got := corosMcpAccountID("https://mcpus.coros.com", "12345"); got != "mcpus:12345" {
		t.Fatalf("got %q", got)
	}
	if got := corosMcpAccountID("https://mcpeu.coros.com/", "12345"); got != "mcpeu:12345" {
		t.Fatalf("regions must not collide: got %q", got)
	}
	if corosMcpAccountID("https://mcpus.coros.com", "1") == corosMcpAccountID("https://mcpeu.coros.com", "1") {
		t.Fatal("the same sub in two regions must map to different identities")
	}
	if got := corosMcpAccountID("https://weird.example.com", "9"); got != "coros:9" {
		t.Fatalf("unknown issuer → generic tag, got %q", got)
	}
	long := strings.Repeat("x", 200)
	a := corosMcpAccountID("https://mcpus.coros.com", long)
	b := corosMcpAccountID("https://mcpus.coros.com", long)
	c := corosMcpAccountID("https://mcpus.coros.com", long+"y")
	if len(a) != corosMcpAccountIDMax || a != b || a == c || !strings.HasPrefix(a, "h:") {
		t.Fatalf("over-long sub → stable hashed identity of exactly %d chars: %q (len %d)", corosMcpAccountIDMax, a, len(a))
	}
	// 邊界：區域前綴 "mcpus:"（6）＋ sub 58 字元 ＝ 64，剛好不超過上限，不雜湊；多 1 個字元就雜湊。
	if got := corosMcpAccountID("https://mcpus.coros.com", strings.Repeat("y", 58)); len(got) != 64 || strings.HasPrefix(got, "h:") {
		t.Fatalf("exactly 64 chars is allowed unhashed, got %q (%d)", got, len(got))
	}
	if got := corosMcpAccountID("https://mcpus.coros.com", strings.Repeat("y", 59)); len(got) != 64 || !strings.HasPrefix(got, "h:") {
		t.Fatalf("65 chars must be hashed down to 64, got %q (%d)", got, len(got))
	}
}

func TestCorosMcpIdentity_EndToEndWithJWKSFetch(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	jwks := rsaJWKS(t, "k1", &key.PublicKey)
	hits := 0
	h := newTestCorosMcpHandlerWithHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwks)
	}))
	disc := &corosMcpDiscoveryDoc{Issuer: testIssuer, JWKSURI: testIssuer + "/.well-known/jwks.json"}
	ctx := context.Background()

	// 沒有 id_token → ("", nil)：呼叫端視為無法綁定（不阻擋連接）
	if id, err := h.corosMcpIdentity(ctx, disc, testClientID, "", idNow); id != "" || err != nil || hits != 0 {
		t.Fatalf("no id_token: (%q,%v) hits=%d", id, err, hits)
	}
	// 有 id_token＋JWKS：驗簽通過 → 區域前綴的帳號識別
	tok := signRS256(t, key, "k1", goodClaims())
	id, err := h.corosMcpIdentity(ctx, disc, testClientID, tok, idNow)
	if err != nil || id != "mcpus:user-12345" || hits != 1 {
		t.Fatalf("signed id_token: (%q,%v) hits=%d", id, err, hits)
	}
	// 簽章被竄改 → 錯誤（呼叫端拒絕連接 identity_invalid）
	tampered := tok[:len(tok)-4] + "AAAA"
	if _, err := h.corosMcpIdentity(ctx, disc, testClientID, tampered, idNow); err == nil {
		t.Fatal("tampered signature must be rejected")
	}
	// JWKS 端點非 coros.com host → 不撥號直接拒絕
	hits = 0
	badDisc := &corosMcpDiscoveryDoc{Issuer: testIssuer, JWKSURI: "https://evil.example.com/jwks"}
	if _, err := h.corosMcpIdentity(ctx, badDisc, testClientID, tok, idNow); err == nil || hits != 0 {
		t.Fatalf("jwks uri outside coros.com must be refused without any request, err=%v hits=%d", err, hits)
	}
	// 沒有 jwks_uri：只驗 claims
	noJWKS := &corosMcpDiscoveryDoc{Issuer: testIssuer}
	hits = 0
	if id, err := h.corosMcpIdentity(ctx, noJWKS, testClientID, tok, idNow); err != nil || id != "mcpus:user-12345" || hits != 0 {
		t.Fatalf("no jwks_uri: (%q,%v) hits=%d", id, err, hits)
	}
}
