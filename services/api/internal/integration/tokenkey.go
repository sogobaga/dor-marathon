package integration

// Token 靜態加密金鑰檢查（COROS GA 契約附件／Garmin 計畫 S3，政策稿前提 P8「憑證加密保存、缺金鑰拒存」）。
//
// 既有 encryptToken（repository.go）在 STRAVA_TOKEN_KEY 未設／無效時「靜默回傳明碼」——Strava／Terra 路徑
// 維持這個行為不變（零風險 fallback）。直連手錶（COROS MCP、Garmin）的 token 權限遠大於 DOR 所需，
// 因此這兩條路徑改用 EncryptTokenStrict：無有效金鑰就回錯、拒絕寫入（fail-closed），
// 並在啟動時 CheckTokenKeyAtStartup 告警，讓維運在使用者連接失敗之前就知道。

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/notify"
)

// ErrTokenKeyMissing 沒有有效的 token 加密金鑰（STRAVA_TOKEN_KEY 未設或不是 32 bytes hex／base64）。
var ErrTokenKeyMissing = errors.New("integration: token encryption key (STRAVA_TOKEN_KEY) missing or invalid")

// TokenKeyConfigured 目前是否有有效的 32 bytes 加密金鑰。不記 log（stravaTokenKey 無效時每次呼叫都會
// 記一行 warn，這支只問「有沒有」，會被連線建立／啟動檢查頻繁呼叫）。
func TokenKeyConfigured() bool {
	k, _ := parseTokenKey(os.Getenv("STRAVA_TOKEN_KEY"))
	return len(k) == 32
}

// EncryptTokenStrict 與 encryptToken 同一套 AES-256-GCM／"enc:" 格式（decryptToken 可直接解），
// 差別是 fail-closed：沒有有效金鑰、或加密過程任何一步失敗都回錯，絕不退回明碼。
// plain 為空字串時回 ("", nil)（沒有 refresh token 的連線，空字串不需要保護）——但金鑰缺失仍先回錯，
// 避免「某些欄位空就悄悄放過缺金鑰」造成測試漏抓。
func EncryptTokenStrict(plain string) (string, error) {
	key, _ := parseTokenKey(os.Getenv("STRAVA_TOKEN_KEY"))
	if len(key) != 32 {
		return "", ErrTokenKeyMissing
	}
	if plain == "" {
		return "", nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("token cipher init: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("token gcm init: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("token nonce: %w", err)
	}
	return encPrefix + base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// CheckTokenKeyAtStartup 啟動時檢查加密金鑰：缺失／無效 → log.Error ＋ notify.Alert("token_key_missing")。
// 不中止啟動（Strava／Terra 沿用明碼 fallback，且金鑰缺失時直連手錶的連接本身會 fail-closed 拒絕）。
// 回傳是否有效，方便呼叫端與測試使用。
func CheckTokenKeyAtStartup() bool {
	if TokenKeyConfigured() {
		return true
	}
	raw := os.Getenv("STRAVA_TOKEN_KEY")
	detail := "STRAVA_TOKEN_KEY 未設定"
	if raw != "" {
		detail = "STRAVA_TOKEN_KEY 已設定但不是有效的 32 bytes hex／base64 金鑰"
	}
	log.Error().Msg("token encryption key missing or invalid: " + detail + "；直連手錶（COROS MCP／Garmin）將拒絕儲存 token，Strava／Terra 仍以明碼儲存")
	notify.Alert("token_key_missing", "Token 加密金鑰缺失",
		detail+"。直連手錶（COROS MCP／Garmin）無法儲存連線（fail-closed）；Strava／Terra 的 token 目前以明碼儲存。請在 Railway 設定有效的 32 bytes 金鑰（勿貼出金鑰值）。")
	return false
}
