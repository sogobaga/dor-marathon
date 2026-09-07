package auth

import (
	"context"
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

func TestIsAcceptableAccessTyp(t *testing.T) {
	before := legacyRefreshCutoff.Add(-time.Hour)
	after := legacyRefreshCutoff.Add(time.Hour)

	cases := []struct {
		name string
		typ  string
		now  time.Time
		want bool
	}{
		{"access typ always accepted before cutoff", "access", before, true},
		{"access typ always accepted after cutoff", "access", after, true},
		{"legacy no-typ accepted before cutoff", "", before, true},
		{"legacy no-typ rejected after cutoff", "", after, false},
		{"refresh typ rejected before cutoff", "refresh", before, false},
		{"refresh typ rejected after cutoff", "refresh", after, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isAcceptableAccessTyp(c.typ, c.now); got != c.want {
				t.Errorf("isAcceptableAccessTyp(%q, %v) = %v, want %v", c.typ, c.now, got, c.want)
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
