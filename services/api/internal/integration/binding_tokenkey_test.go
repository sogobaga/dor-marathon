package integration

// 帳號綁定衝突偵測（binding.go）與 token 金鑰（tokenkey.go）單元測試。

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsProviderAccountConflict(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("boom"), false},
		{"unique violation on the account index", &pgconn.PgError{Code: "23505", ConstraintName: "user_integrations_provider_account_uniq", TableName: "user_integrations"}, true},
		{"unique violation without constraint name but on user_integrations", &pgconn.PgError{Code: "23505", TableName: "user_integrations"}, true},
		{"wrapped", fmt.Errorf("save: %w", &pgconn.PgError{Code: "23505", ConstraintName: "user_integrations_provider_account_uniq"}), true},
		{"(user_id, provider) primary uniqueness is NOT an account conflict", &pgconn.PgError{Code: "23505", ConstraintName: "user_integrations_user_id_provider_key", TableName: "user_integrations"}, false},
		{"unique violation on another table", &pgconn.PgError{Code: "23505", ConstraintName: "users_email_key", TableName: "users"}, false},
		{"other SQLSTATE (FK violation) on the same table", &pgconn.PgError{Code: "23503", TableName: "user_integrations"}, false},
		{"serialization failure", &pgconn.PgError{Code: "40001"}, false},
	}
	for _, c := range cases {
		if got := IsProviderAccountConflict(c.err); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	if ErrProviderAccountLinked == nil || !strings.Contains(ErrProviderAccountLinked.Error(), "already linked") {
		t.Fatal("ErrProviderAccountLinked must be a descriptive sentinel")
	}
}

func TestTokenKeyConfigured_MissingInvalidValid(t *testing.T) {
	t.Setenv("STRAVA_TOKEN_KEY", "")
	if TokenKeyConfigured() {
		t.Fatal("empty key must not count as configured")
	}
	t.Setenv("STRAVA_TOKEN_KEY", "not-a-valid-key")
	if TokenKeyConfigured() {
		t.Fatal("malformed key must not count as configured")
	}
	t.Setenv("STRAVA_TOKEN_KEY", strings.Repeat("ab", 16)) // 32 hex chars = 16 bytes → too short
	if TokenKeyConfigured() {
		t.Fatal("a 16-byte key is invalid (need 32 bytes)")
	}
	t.Setenv("STRAVA_TOKEN_KEY", validKeyHex)
	if !TokenKeyConfigured() {
		t.Fatal("a 32-byte hex key must count as configured")
	}
}

func TestEncryptTokenStrict_FailClosedAndRoundTrip(t *testing.T) {
	for _, bad := range []string{"", "not-a-valid-key", strings.Repeat("ab", 16)} {
		t.Setenv("STRAVA_TOKEN_KEY", bad)
		if enc, err := EncryptTokenStrict("secret-token"); !errors.Is(err, ErrTokenKeyMissing) || enc != "" {
			t.Fatalf("key %q: want ErrTokenKeyMissing and no plaintext fallback, got (%q, %v)", bad, enc, err)
		}
		// 空字串也先檢查金鑰（不因為欄位空就悄悄放過缺金鑰）
		if _, err := EncryptTokenStrict(""); !errors.Is(err, ErrTokenKeyMissing) {
			t.Fatalf("key %q: empty plain with missing key must still error, got %v", bad, err)
		}
		// 既有 encryptToken 行為不變：缺金鑰仍明碼 fallback（Strava／Terra 路徑不受影響）
		if got := encryptToken("secret-token"); got != "secret-token" {
			t.Fatalf("legacy encryptToken must keep the plaintext fallback, got %q", got)
		}
	}
	t.Setenv("STRAVA_TOKEN_KEY", validKeyHex)
	enc, err := EncryptTokenStrict("secret-token")
	if err != nil || !strings.HasPrefix(enc, encPrefix) || strings.Contains(enc, "secret-token") {
		t.Fatalf("strict encrypt: (%q, %v)", enc, err)
	}
	// 與 decryptToken 互通（同一格式）
	if dec, err := decryptToken(enc); err != nil || dec != "secret-token" {
		t.Fatalf("decrypt of strict ciphertext: (%q, %v)", dec, err)
	}
	// 每次加密 nonce 不同
	enc2, _ := EncryptTokenStrict("secret-token")
	if enc == enc2 {
		t.Fatal("two encryptions of the same plaintext must differ (random nonce)")
	}
	// 有金鑰時空字串回空字串（沒有 refresh token 的連線）
	if got, err := EncryptTokenStrict(""); err != nil || got != "" {
		t.Fatalf("empty plain with a valid key = (%q, %v), want (\"\", nil)", got, err)
	}
	// 嚴格版與舊版在有金鑰時可互相解密
	legacy := encryptToken("legacy-token")
	if dec, err := decryptToken(legacy); err != nil || dec != "legacy-token" {
		t.Fatalf("legacy ciphertext: (%q, %v)", dec, err)
	}
}

func TestCheckTokenKeyAtStartup(t *testing.T) {
	t.Setenv("STRAVA_TOKEN_KEY", validKeyHex)
	if !CheckTokenKeyAtStartup() {
		t.Fatal("valid key → true")
	}
	// 缺金鑰：回 false（log.Error＋notify.Alert；未設 Telegram 環境時 Alert 是 no-op，不會對外送出）
	t.Setenv("STRAVA_TOKEN_KEY", "")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	if CheckTokenKeyAtStartup() {
		t.Fatal("missing key → false")
	}
	t.Setenv("STRAVA_TOKEN_KEY", "garbage")
	if CheckTokenKeyAtStartup() {
		t.Fatal("malformed key → false")
	}
}

func TestParseTokenKey(t *testing.T) {
	if k, invalid := parseTokenKey(""); k != nil || invalid {
		t.Fatalf("unset: (%v,%v)", k, invalid)
	}
	if k, invalid := parseTokenKey("zz"); k != nil || !invalid {
		t.Fatalf("garbage: (%v,%v)", k, invalid)
	}
	if k, invalid := parseTokenKey(validKeyHex); len(k) != 32 || invalid {
		t.Fatalf("valid hex: (%d,%v)", len(k), invalid)
	}
}
