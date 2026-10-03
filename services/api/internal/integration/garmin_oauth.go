package integration

// Garmin OAuth2 PKCE 的純函式部分：verifier／challenge、state 簽章、登入 CSRF nonce cookie、授權網址。
//
// 與 COROS MCP 版（corosmcp.go）的差異：PKCE verifier **不放進 state**（state 會經過瀏覽器前通道可見），
// 改存 Redis（garmin:pkce:<sid>，TTL 10 分鐘，callback 用 GETDEL 一次性取走）。state 只帶使用者、nonce、
// sid、同意版本與時間，簽章金鑰＝HMAC(JWT_SECRET, "dor/garmin/state/v1")（用途隔離）。

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	garminNonceCookie   = "dor_garmin_n"
	garminCallbackPath  = "/api/v1/integrations/garmin/callback"
	garminStateKeyLabel = "dor/garmin/state/v1"
	garminStateTTL      = 10 * time.Minute
	garminPKCETTL       = 10 * time.Minute
	garminPKCEKeyPrefix = "garmin:pkce:"
)

// garminRandB64 回傳 n 個隨機位元組的 base64url（無 padding）。
func garminRandB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// garminNewVerifier：32 bytes → base64url＝43 字元（符合官方 43–128 字元、字元集 A-Za-z0-9-_）。
func garminNewVerifier() (string, error) { return garminRandB64(32) }

// garminChallenge：base64url(sha256(verifier))（S256）。
func garminChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// garminStateSecret 衍生 state 專用簽章金鑰（JWT_SECRET 不直接當 state 簽章金鑰）。
func garminStateSecret(jwtSecret string) []byte {
	m := hmac.New(sha256.New, []byte(jwtSecret))
	m.Write([]byte(garminStateKeyLabel))
	return m.Sum(nil)
}

func garminMAC(key []byte, msg string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// garminState：state 內容。
type garminState struct {
	UserID    string
	Nonce     string
	SID       string // PKCE verifier 在 Redis 的鍵 id
	ConsentV  string // 使用者同意的文案版本
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// garminSignState 組 state：base64url(uid\nnonce\nsid\nconsent_v\niat\nexp).HMAC。
func garminSignState(jwtSecret string, st garminState) string {
	msg := strings.Join([]string{st.UserID, st.Nonce, st.SID, st.ConsentV,
		strconv.FormatInt(st.IssuedAt.Unix(), 10), strconv.FormatInt(st.ExpiresAt.Unix(), 10)}, "\n")
	return base64.RawURLEncoding.EncodeToString([]byte(msg)) + "." + garminMAC(garminStateSecret(jwtSecret), msg)
}

// garminVerifyState 驗簽章與期限（常數時間比對）。
func garminVerifyState(jwtSecret, state string, now time.Time) (garminState, bool) {
	var st garminState
	i := strings.LastIndex(state, ".")
	if i < 0 {
		return st, false
	}
	raw, sig := state[:i], state[i+1:]
	msgBytes, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return st, false
	}
	msg := string(msgBytes)
	if !hmac.Equal([]byte(sig), []byte(garminMAC(garminStateSecret(jwtSecret), msg))) {
		return st, false
	}
	parts := strings.Split(msg, "\n")
	if len(parts) != 6 {
		return st, false
	}
	iat, err1 := strconv.ParseInt(parts[4], 10, 64)
	exp, err2 := strconv.ParseInt(parts[5], 10, 64)
	if err1 != nil || err2 != nil || now.Unix() > exp {
		return st, false
	}
	return garminState{UserID: parts[0], Nonce: parts[1], SID: parts[2], ConsentV: parts[3],
		IssuedAt: time.Unix(iat, 0).UTC(), ExpiresAt: time.Unix(exp, 0).UTC()}, true
}

// garminNonceMatches 常數時間比對 cookie 與 state 內的 nonce；任一為空一律不符。
func garminNonceMatches(cookieVal, stateNonce string) bool {
	if cookieVal == "" || stateNonce == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookieVal), []byte(stateNonce)) == 1
}

// garminNonceCookieFor 組 nonce cookie；maxAge<0 代表清除。
func garminNonceCookieFor(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: garminNonceCookie, Value: value, Path: garminCallbackPath,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: maxAge,
	}
}

// garminAuthURL 組授權網址（GET /oauth2Confirm）。redirect_uri 與換 token 時送的值完全相同。
func (h *GarminHandler) garminAuthURL(challenge, state string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", h.cfg.ClientID)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("redirect_uri", h.cfg.RedirectURI)
	q.Set("state", state)
	return strings.TrimRight(h.cfg.AuthBase, "/") + "/oauth2Confirm?" + q.Encode()
}

// garminAppGeneration：App 世代標記＝garmin-app:<sha256(client_id) 前 8 碼>。換 client id（Evaluation→Production）
// 後，舊連線的 issuer 與當前不符 → 視為需要重新授權（不打 token 端點）。
func garminAppGeneration(clientID string) string {
	sum := sha256.Sum256([]byte(clientID))
	return "garmin-app:" + hex.EncodeToString(sum[:])[:8]
}
