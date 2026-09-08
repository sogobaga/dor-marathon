package auth

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
)

// newUnreachableRedis 建一個「一定連不上、且很快失敗」的 redis client，供不需要真的驗證撤銷名單
// 內容、只需要驗證「Redis 打不通時 fail-open」這條路徑的測試使用。127.0.0.1:1 是保留低位埠，
// 正常環境不會有任何服務在聽，TCP 會直接回 RST（拒絕連線），不必等到 DialTimeout 逾時才失敗。
func newUnreachableRedis() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  200 * time.Millisecond,
		ReadTimeout:  200 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond,
	})
}

func newTestService() *Service {
	// repo 刻意留 nil：本檔測試只涵蓋不觸碰 DB 的路徑（issueTokens／ValidateAccessToken／
	// isAcceptableRefreshTyp），FindByID 等需要 DB 的路徑不在本檔範圍內（需要真的 Postgres）。
	return NewService(nil, newUnreachableRedis(), "test-jwt-secret-please-ignore", time.Hour, 720*time.Hour, "")
}

// --- H1: token 用途區分（typ claim）---

func TestValidateAccessToken_AcceptsAccessTyp(t *testing.T) {
	s := newTestService()
	pair, err := s.issueTokens(context.Background(), "u1", "user", 3)
	if err != nil {
		t.Fatalf("issueTokens: %v", err)
	}
	claims, err := s.ValidateAccessToken(context.Background(), pair.AccessToken)
	if err != nil {
		t.Fatalf("ValidateAccessToken should accept a freshly-issued access token, got err=%v", err)
	}
	if claims.UserID != "u1" || claims.SessionEpoch != 3 {
		t.Errorf("unexpected claims: %+v", claims)
	}
}

func TestValidateAccessToken_RejectsRefreshTyp(t *testing.T) {
	s := newTestService()
	pair, err := s.issueTokens(context.Background(), "u1", "user", 0)
	if err != nil {
		t.Fatalf("issueTokens: %v", err)
	}
	// 用 refresh token 打 access 驗證：必須被拒絕，否則長效 refresh token 能冒充 access token
	// 一路用到過期為止（720 小時 vs. 60 分鐘），形同繞過短效 access token 的設計初衷。
	if _, err := s.ValidateAccessToken(context.Background(), pair.RefreshToken); err != ErrTokenInvalid {
		t.Errorf("expected ErrTokenInvalid for a refresh-typ token, got %v", err)
	}
}

// signLegacyToken 手刻一顆「本次 H1 修法上線前」簽出的 token：其餘 claim 都對，只是沒有
// typ 欄位（Typ 留零值 ""，JSON 標了 omitempty，序列化後跟舊版簽出的 token 一樣不帶這個欄位）。
func signLegacyToken(t *testing.T, s *Service) string {
	t.Helper()
	legacy := &Claims{
		UserID:       "u1",
		Role:         "user",
		SessionEpoch: 0,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, legacy).SignedString(s.jwtSecret)
	if err != nil {
		t.Fatalf("sign legacy token: %v", err)
	}
	return tok
}

// 2026-09-07 audit（major finding）：ValidateAccessToken 對 typ-less 舊 access token 原本沒有
// 寬限期，害後台「不勾選保持登入」（完全不存 refresh token，見 adminAuth.ts setSession）的
// session 在部署當下就被直接踢出、無法自動恢復。修法後兩者都在 legacyRefreshCutoff 前接受、
// 之後拒絕——這裡驗證「寬限期內接受」這一半（寬限期後的拒絕行為由下面
// TestIsAcceptableAccessTyp 的表格案例覆蓋，不需要真的把系統時間調到未來）。
func TestValidateAccessToken_AcceptsLegacyNoTypDuringGracePeriod(t *testing.T) {
	s := newTestService()
	tok := signLegacyToken(t, s)
	claims, err := s.ValidateAccessToken(context.Background(), tok)
	if err != nil {
		t.Fatalf("expected legacy (typ-less) access token to be accepted during the grace period, got err=%v", err)
	}
	if claims.UserID != "u1" {
		t.Errorf("unexpected claims: %+v", claims)
	}
}

// 2026-09-08 audit finding 2：光靠 typ=="" 判斷寬限期不夠——沒有 typ 的舊 refresh token
// （效期跨距最長 30 天）在寬限期內也會被誤判成「可接受的 access token」。新版
// isAcceptableAccessTyp 多兩個時間戳＋accessTTL 參數，額外檢查效期跨距（exp-iat）是否落在
// accessTTL+5 分鐘以內；30 天跨距的 legacy refresh token 必須被拒絕，即使在寬限期內。
func TestIsAcceptableAccessTyp(t *testing.T) {
	before := legacyRefreshCutoff.Add(-time.Hour)
	after := legacyRefreshCutoff.Add(time.Hour)
	accessTTL := time.Hour

	shortIat := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	shortExp := shortIat.Add(accessTTL) // 剛好等於 accessTTL 的跨距（典型 legacy access token）

	longIat := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	longExp := longIat.Add(30 * 24 * time.Hour) // legacy refresh token 的典型跨距（30 天）

	cases := []struct {
		name string
		typ  string
		now  time.Time
		iat  time.Time
		exp  time.Time
		want bool
	}{
		{"access typ always accepted before cutoff, span irrelevant", "access", before, longIat, longExp, true},
		{"access typ always accepted after cutoff, span irrelevant", "access", after, longIat, longExp, true},
		{"legacy no-typ short span accepted before cutoff", "", before, shortIat, shortExp, true},
		{"legacy no-typ short span rejected after cutoff", "", after, shortIat, shortExp, false},
		{"legacy no-typ long span (looks like a refresh token) rejected even before cutoff", "", before, longIat, longExp, false},
		{"legacy no-typ missing timestamps rejected", "", before, time.Time{}, time.Time{}, false},
		{"refresh typ rejected before cutoff", "refresh", before, shortIat, shortExp, false},
		{"refresh typ rejected after cutoff", "refresh", after, shortIat, shortExp, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isAcceptableAccessTyp(c.typ, c.now, c.iat, c.exp, accessTTL); got != c.want {
				t.Errorf("isAcceptableAccessTyp(%q, now=%v, iat=%v, exp=%v, ttl=%v) = %v, want %v",
					c.typ, c.now, c.iat, c.exp, accessTTL, got, c.want)
			}
		})
	}
}

func TestIsAcceptableRefreshTyp(t *testing.T) {
	before := legacyRefreshCutoff.Add(-time.Hour)
	after := legacyRefreshCutoff.Add(time.Hour)

	cases := []struct {
		name string
		typ  string
		now  time.Time
		want bool
	}{
		{"refresh typ always accepted before cutoff", "refresh", before, true},
		{"refresh typ always accepted after cutoff", "refresh", after, true},
		{"legacy no-typ accepted before cutoff", "", before, true},
		{"legacy no-typ rejected after cutoff", "", after, false},
		{"access typ rejected before cutoff", "access", before, false},
		{"access typ rejected after cutoff", "access", after, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isAcceptableRefreshTyp(c.typ, c.now); got != c.want {
				t.Errorf("isAcceptableRefreshTyp(%q, %v) = %v, want %v", c.typ, c.now, got, c.want)
			}
		})
	}
}

func TestRefresh_RejectsAccessTypToken(t *testing.T) {
	s := newTestService()
	pair, err := s.issueTokens(context.Background(), "u1", "user", 0)
	if err != nil {
		t.Fatalf("issueTokens: %v", err)
	}
	// 用 access token 打 refresh 端點：typ 檢查必須在碰到 DB（FindByID）之前就先擋下來
	// （repo 是 nil，若沒有提早擋下會直接 panic，而不是回傳 ErrTokenInvalid）。
	if _, err := s.Refresh(context.Background(), pair.AccessToken); err != ErrTokenInvalid {
		t.Errorf("expected ErrTokenInvalid for an access-typ token at /auth/refresh, got %v", err)
	}
}

// --- M1: refresh token jti 唯一性 ---

func TestIssueTokens_JTIUniqueAcrossSameSecondIssuances(t *testing.T) {
	s := newTestService()
	pair1, err := s.issueTokens(context.Background(), "u1", "user", 0)
	if err != nil {
		t.Fatalf("issueTokens #1: %v", err)
	}
	pair2, err := s.issueTokens(context.Background(), "u1", "user", 0)
	if err != nil {
		t.Fatalf("issueTokens #2: %v", err)
	}

	if pair1.RefreshToken == pair2.RefreshToken {
		t.Fatalf("two issuances for the same user/role/epoch produced byte-identical refresh tokens")
	}

	claims1, err := s.parseToken(pair1.RefreshToken)
	if err != nil {
		t.Fatalf("parse pair1 refresh: %v", err)
	}
	claims2, err := s.parseToken(pair2.RefreshToken)
	if err != nil {
		t.Fatalf("parse pair2 refresh: %v", err)
	}
	if claims1.ID == "" || claims2.ID == "" {
		t.Fatalf("expected non-empty jti on refresh claims, got %q and %q", claims1.ID, claims2.ID)
	}
	if claims1.ID == claims2.ID {
		t.Errorf("expected distinct jti across two issuances, both were %q", claims1.ID)
	}
}

// access token 現在也帶 jti（2026-09-08 audit finding 1(b)）——沒有這個，access token 的撤銷
// 只能退回整串字串的 sha256（revokeKey），在下面 canonicalJWTPattern 測試證實的編碼變體繞過
// 疑慮出現之前就已經是比較弱的撤銷鍵。
func TestIssueTokens_AccessTokenHasJTI(t *testing.T) {
	s := newTestService()
	pair, err := s.issueTokens(context.Background(), "u1", "user", 0)
	if err != nil {
		t.Fatalf("issueTokens: %v", err)
	}
	claims, err := s.parseToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	if claims.ID == "" {
		t.Error("expected non-empty jti on access token claims")
	}
}

// --- 2026-09-08 audit finding 1：token 編碼變體繞過撤銷名單 ---

// mutateSignatureInsertNewline 在簽章段落中間插入一個 \n。Go 的 base64.RawURLEncoding.
// DecodeString 對嵌入的 \n／\r 是「忽略、照樣解出正確位元組」（已用獨立小程式驗證），意味著在
// canonicalJWTPattern 這道檢查補上之前，這個變體會解出跟原本一模一樣的簽章、順利通過驗簽——
// 但整串字串已經不同，撤銷名單的舊版 key（sha256(整串 token)）也跟著不同，形同繞過撤銷。
func mutateSignatureInsertNewline(t *testing.T, token string) string {
	t.Helper()
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		t.Fatalf("not a 3-segment JWT: %q", token)
	}
	return parts[0] + "." + parts[1] + "." + parts[2][:5] + "\n" + parts[2][5:]
}

func TestParseToken_RejectsNewlineInSignatureSegment(t *testing.T) {
	s := newTestService()
	pair, err := s.issueTokens(context.Background(), "u1", "user", 0)
	if err != nil {
		t.Fatalf("issueTokens: %v", err)
	}
	// 先確認原始 token 本身是合法的（排除「這個 token 本來就有問題」的可能）。
	if _, err := s.parseToken(pair.AccessToken); err != nil {
		t.Fatalf("original token should parse fine, got err=%v", err)
	}
	mutated := mutateSignatureInsertNewline(t, pair.AccessToken)
	if _, err := s.parseToken(mutated); err != ErrTokenInvalid {
		t.Errorf("expected ErrTokenInvalid for a newline-mutated signature, got %v", err)
	}
}

// 附加驗證：即使沒有 canonicalJWTPattern，golang-jwt 本身也會因為插入的 \n 讓 base64 解碼出的
// 簽章跟原本完全相同（見上方註解），代表若只靠 jwt 套件本身的驗簽，這個變體不會被擋下——這裡
// 直接證實這個底層行為，佐證 canonicalJWTPattern 這道防線不是多餘的。
func TestBase64RawURLEncoding_SilentlyIgnoresEmbeddedNewline(t *testing.T) {
	orig := []byte("some signature bytes for the probe 0123456789")
	enc := base64.RawURLEncoding.EncodeToString(orig)
	withNL := enc[:5] + "\n" + enc[5:]
	dec, err := base64.RawURLEncoding.DecodeString(withNL)
	if err != nil {
		t.Fatalf("expected no error decoding newline-embedded base64, got %v", err)
	}
	if string(dec) != string(orig) {
		t.Fatalf("expected decoded bytes to match original despite embedded newline")
	}
}

func TestParseToken_RejectsPaddedSignatureSegment(t *testing.T) {
	s := newTestService()
	pair, err := s.issueTokens(context.Background(), "u1", "user", 0)
	if err != nil {
		t.Fatalf("issueTokens: %v", err)
	}
	mutated := pair.AccessToken + "=" // 附加一個標準 base64 的 padding 字元
	if _, err := s.parseToken(mutated); err != ErrTokenInvalid {
		t.Errorf("expected ErrTokenInvalid for a padded token, got %v", err)
	}
}

// base64Alphabet 是 RawURLEncoding 用的字元表（順序需與 encoding/base64 的定義一致），供下面
// 建構「同一顆簽章、不同字串」的變體用。
const base64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

// TestParseToken_RejectsNonCanonicalTrailingBits 驗證 jwt.WithStrictDecoding() 真的有作用，
// 不是裝飾用的選項——canonicalJWTPattern 只鎖字元集，擋不住「換成合法字元集內的另一個字元、
// 但解出完全相同位元組」這種 malleability（HMAC-SHA256 簽章固定 32 bytes，base64 編碼後最後
// 一個符號帶 2 個 padding bits，只要這 2 個 bit 非零就是非 canonical 寫法——非嚴格解碼會忽略它、
// 嚴格解碼會拒絕）。這裡直接構造這樣一個變體，證明沒有 WithStrictDecoding 的話它會被非嚴格
// 解碼判定為「跟原本相同的位元組」，而 parseToken（已加上 WithStrictDecoding）確實拒絕它。
func TestParseToken_RejectsNonCanonicalTrailingBits(t *testing.T) {
	s := newTestService()
	pair, err := s.issueTokens(context.Background(), "u1", "user", 0)
	if err != nil {
		t.Fatalf("issueTokens: %v", err)
	}
	parts := strings.SplitN(pair.AccessToken, ".", 3)
	if len(parts) != 3 {
		t.Fatalf("not a 3-segment JWT: %q", pair.AccessToken)
	}
	sig := parts[2]
	lastChar := sig[len(sig)-1]
	idx := strings.IndexByte(base64Alphabet, lastChar)
	if idx < 0 {
		t.Fatalf("signature last char %q not found in base64 alphabet", string(lastChar))
	}
	// HMAC-SHA256 固定 32 bytes → base64 編碼後最後一個符號恰有 2 個 padding bits，canonical
	// 寫法下這 2 個 bit 必為零，也就是 idx 必為 4 的倍數（見 group）；換到同一個 4-群組內的
	// 另一個字元，非嚴格解碼會截斷 padding bits、得到跟原本相同的資料位元組。
	group := idx - idx%4
	alt := group + (idx-group+1)%4
	if alt == idx {
		t.Fatalf("failed to compute a distinct alternate index (idx=%d)", idx)
	}
	mutatedSig := sig[:len(sig)-1] + string(base64Alphabet[alt])

	// 佐證：非嚴格解碼確實把這個變體解讀成跟原本相同的位元組（否則這個測試就沒有意義——代表
	// 隨便換掉最後一個字元本來就會產生不同簽章，jwt 驗簽自然就會拒絕，不需要 WithStrictDecoding）。
	origBytes, errOrig := base64.RawURLEncoding.DecodeString(sig)
	mutatedBytesNonStrict, errNS := base64.RawURLEncoding.DecodeString(mutatedSig)
	if errOrig != nil || errNS != nil || string(mutatedBytesNonStrict) != string(origBytes) {
		t.Fatalf("could not construct a same-bytes trailing-bit variant (idx=%d alt=%d): errOrig=%v errNS=%v", idx, alt, errOrig, errNS)
	}

	mutatedToken := parts[0] + "." + parts[1] + "." + mutatedSig
	if _, err := s.parseToken(mutatedToken); err != ErrTokenInvalid {
		t.Errorf("expected WithStrictDecoding to reject a non-canonical trailing-bit signature variant, got %v", err)
	}
}

// --- revocationKey：優先 jti，legacy（無 jti）退回 sha256 ---

func TestRevocationKey_PrefersJTI(t *testing.T) {
	claims := &Claims{UserID: "u1", RegisteredClaims: jwt.RegisteredClaims{ID: "abc123"}}
	got := revocationKey(claims, "raw-token-string-should-be-ignored-when-jti-present")
	want := revokeKeyJTI("u1", "abc123")
	if got != want {
		t.Errorf("revocationKey() = %q, want %q", got, want)
	}
}

func TestRevocationKey_FallsBackToSha256WhenNoJTI(t *testing.T) {
	claims := &Claims{UserID: "u1"} // 無 jti（legacy token）
	raw := "header.payload.signature"
	got := revocationKey(claims, raw)
	want := revokeKey("u1", raw)
	if got != want {
		t.Errorf("revocationKey() = %q, want %q", got, want)
	}
	// 兩顆 userID 不同的 legacy token（即使原始字串相同）不能撞到同一把鑰匙。
	if revocationKey(&Claims{UserID: "u2"}, raw) == want {
		t.Errorf("revocationKey should be scoped per user, got the same key for u1 and u2")
	}
}

// --- 2026-09-08 audit finding 1：撤銷真的生效（整合測試，用 fakeredis_test.go 的假 Redis）---

// TestValidateAccessToken_HonoursRevocation 驗證「同一顆 token 的撤銷是否真的生效」：Logout
// 撤銷一顆 access token 之後，ValidateAccessToken 必須拒絕它——這條路徑不需要 repo（DB），
// 用 newFakeRedisService 換掉原本 fail-open 用的 newUnreachableRedis，讓 Redis 互動真的落地。
func TestValidateAccessToken_HonoursRevocation(t *testing.T) {
	s := newFakeRedisService(t)
	ctx := context.Background()

	pair, err := s.issueTokens(ctx, "u1", "user", 0)
	if err != nil {
		t.Fatalf("issueTokens: %v", err)
	}
	if _, err := s.ValidateAccessToken(ctx, pair.AccessToken); err != nil {
		t.Fatalf("token should be valid before logout, got err=%v", err)
	}

	if err := s.Logout(ctx, "u1", pair.AccessToken, ""); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	if _, err := s.ValidateAccessToken(ctx, pair.AccessToken); err != ErrTokenInvalid {
		t.Errorf("expected ErrTokenInvalid for a revoked access token, got %v", err)
	}
}

// 同上，但用一顆沒有 jti 的 legacy access token（revocationKey 退回 revokeKey 的 sha256 路徑）——
// 確保 2026-09-08 audit 的修法沒有破壞本次修法上線前簽出、還在自然過期前繼續流通的 token 的
// 撤銷行為。
func TestValidateAccessToken_HonoursRevocation_LegacyNoJTI(t *testing.T) {
	s := newFakeRedisService(t)
	ctx := context.Background()

	tok := signLegacyToken(t, s) // typ=="", 無 jti，見本檔上方 signLegacyToken
	if _, err := s.ValidateAccessToken(ctx, tok); err != nil {
		t.Fatalf("legacy token should be valid before logout, got err=%v", err)
	}

	if err := s.Logout(ctx, "u1", tok, ""); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	if _, err := s.ValidateAccessToken(ctx, tok); err != ErrTokenInvalid {
		t.Errorf("expected ErrTokenInvalid for a revoked legacy (no-jti) access token, got %v", err)
	}
}

// --- 2026-09-08 audit finding 7：Google 自動連結信任判斷（純函式）---

func TestIsGoogleLinkTrusted(t *testing.T) {
	cases := []struct {
		name        string
		hadPassword bool
		email       string
		hd          string
		want        bool
	}{
		{"no existing password (pure Google account): always trusted regardless of domain", false, "anything@example.com", "", true},
		{"gmail.com domain with password: trusted", true, "user@gmail.com", "", true},
		{"googlemail.com domain with password: trusted", true, "user@googlemail.com", "", true},
		{"GMAIL.COM uppercase domain with password: trusted (case-insensitive)", true, "user@GMAIL.COM", "", true},
		{"workspace hd claim with password, non-gmail domain: trusted", true, "user@company.example", "company.example", true},
		{"arbitrary domain with password, no hd: not trusted", true, "user@evil-corp.example", "", false},
		{"arbitrary domain with password, empty email: not trusted", true, "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isGoogleLinkTrusted(c.hadPassword, c.email, c.hd); got != c.want {
				t.Errorf("isGoogleLinkTrusted(%v, %q, %q) = %v, want %v", c.hadPassword, c.email, c.hd, got, c.want)
			}
		})
	}
}
