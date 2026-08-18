package integration

import "testing"

// validKeyHex / altKeyHex：各為 32 bytes（64 hex chars）的測試用假金鑰，用字串重複組出避免手數字元出錯。
var validKeyHex = repeat4("0123456789abcdef")
var altKeyHex = repeat4("fedcba9876543210")

func repeat4(s string) string { return s + s + s + s }

func TestEncryptDecryptTokenRoundtrip(t *testing.T) {
	t.Setenv("STRAVA_TOKEN_KEY", validKeyHex)
	plain := "test-access-token-abc123"
	enc := encryptToken(plain)
	if enc == plain {
		t.Fatalf("expected token to be encrypted when key configured, got plaintext back")
	}
	if len(enc) < len(encPrefix) || enc[:len(encPrefix)] != encPrefix {
		t.Fatalf("expected %q prefix, got %q", encPrefix, enc)
	}
	dec, err := decryptToken(enc)
	if err != nil {
		t.Fatalf("decryptToken failed: %v", err)
	}
	if dec != plain {
		t.Fatalf("roundtrip mismatch: got %q want %q", dec, plain)
	}
}

func TestDecryptTokenLegacyPlaintextFallback(t *testing.T) {
	// legacy 明碼（無 "enc:" 前綴）：即使金鑰已設定，也應原樣通過，不嘗試解密。
	t.Setenv("STRAVA_TOKEN_KEY", validKeyHex)
	legacy := "legacy-plaintext-token-xyz"
	dec, err := decryptToken(legacy)
	if err != nil {
		t.Fatalf("legacy plaintext token should not error: %v", err)
	}
	if dec != legacy {
		t.Fatalf("legacy plaintext token should pass through unchanged, got %q", dec)
	}
}

func TestTokenKeyUnsetFallback(t *testing.T) {
	// STRAVA_TOKEN_KEY 未設定 → encrypt 為 no-op、decrypt 對明碼也原樣通過（零風險 fallback）。
	t.Setenv("STRAVA_TOKEN_KEY", "")
	plain := "plain-token-no-key-configured"
	enc := encryptToken(plain)
	if enc != plain {
		t.Fatalf("expected encryptToken to be a no-op when key unset, got %q", enc)
	}
	dec, err := decryptToken(plain)
	if err != nil || dec != plain {
		t.Fatalf("expected passthrough when key unset, got %q err=%v", dec, err)
	}
}

func TestTokenKeyInvalidFormatFallback(t *testing.T) {
	// 金鑰格式不對（非 32 bytes hex/base64）→ 視同未設定，明碼 fallback，不 panic。
	t.Setenv("STRAVA_TOKEN_KEY", "not-a-valid-key")
	plain := "plain-token-bad-key"
	enc := encryptToken(plain)
	if enc != plain {
		t.Fatalf("expected no-op fallback for invalid key format, got %q", enc)
	}
}

func TestDecryptTokenFailsClearlyWithoutPanicWhenKeyMissing(t *testing.T) {
	t.Setenv("STRAVA_TOKEN_KEY", validKeyHex)
	enc := encryptToken("another-token-value")
	// 金鑰事後被移除／未設定：解密應回傳清楚的 error，不可 panic、也不可把密文當明碼用。
	t.Setenv("STRAVA_TOKEN_KEY", "")
	dec, err := decryptToken(enc)
	if err == nil {
		t.Fatalf("expected error when key missing for an encrypted token, got nil (dec=%q)", dec)
	}
}

func TestDecryptTokenFailsClearlyWithWrongKey(t *testing.T) {
	t.Setenv("STRAVA_TOKEN_KEY", validKeyHex)
	enc := encryptToken("yet-another-token-value")
	// 換一把不同的合法金鑰：GCM 認證應該失敗，回傳 error 而非亂碼或 panic。
	t.Setenv("STRAVA_TOKEN_KEY", altKeyHex)
	if _, err := decryptToken(enc); err == nil {
		t.Fatalf("expected error when decrypting with the wrong key, got nil")
	}
}
