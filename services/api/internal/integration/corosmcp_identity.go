package integration

// COROS 帳號識別（GA 契約 §2.6）：連接成功時從 token 回應的 OIDC id_token 取 sub，綁定「這個 COROS 帳號」。
// 綁定的目的：同一個 COROS 帳號不能同時連到兩個 DOR 帳號（否則同一趟活動可在兩個 DOR 帳號各領一次獎勵；
// migration 198 的部分唯一索引 user_integrations_provider_account_uniq 擋住第二個）。
//
// 為什麼是 id_token、不是 MCP queryUserInfo（契約允許「擇一並寫明」，這裡選 id_token）：
//   - tools/list 實測（2026-10-01）queryUserInfo 的說明是「Returns height, weight, birthday, and gender」——
//     沒有帳號識別碼，而且是身體資料，不在 COROS 書面核准的讀取範圍；拿它的雜湊當識別既不穩定（改體重就變）
//     也不唯一。所以 queryUserInfo 不用於綁定。
//   - scope 含 openid，COROS（Spring Authorization Server）的 token 端點會一併回 id_token；sub 只在連接當下
//     讀一次，不落地 id_token 本身，只存衍生的帳號識別（見 corosMcpAccountID）。
//
// ⚠️ 未驗證（無法呼叫 COROS，依 OIDC／Spring AS 慣例撰寫）：COROS 是否真的回 id_token、sub 的內容與穩定性。
// 因此行為設計成「拿不到不阻擋」：沒有 id_token → 連線照建、不綁定（日報備註「未綁定人數」，擁有者可據此判斷
// 是否需要請 COROS 說明）；有 id_token 但驗證失敗 → 拒絕連接（reason=identity_invalid，beta 期間第一個測試者
// 就會看到，快速暴露實作與實際回應的落差）。
//
// 驗證：
//   - discovery 有 jwks_uri → 驗簽（RS256／ES256，依 kid 對應）＋ iss／aud／exp；
//   - 沒有 jwks_uri → 只驗 iss／aud／exp。OIDC Core §3.1.3.7(6)：id_token 若是從 token 端點經 TLS 直接取得，
//     可用 TLS 伺服器驗證取代簽章驗證（token 端點已通過 corosMcpValidateHost 限定在 coros.com 子網域）。

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// corosMcpIDTokenLeeway 時鐘誤差容忍。
	corosMcpIDTokenLeeway = 2 * time.Minute
	// corosMcpAccountIDMax user_integrations.provider_user_id 是 VARCHAR(64)。
	corosMcpAccountIDMax = 64
)

// corosMcpIdentity 從 token 回應的 id_token 取出綁定用帳號識別。idToken 空字串回 ("", nil)（無法綁定）。
func (h *CorosMcpHandler) corosMcpIdentity(ctx context.Context, disc *corosMcpDiscoveryDoc, clientID, idToken string, now time.Time) (string, error) {
	idToken = strings.TrimSpace(idToken)
	if idToken == "" {
		return "", nil
	}
	var keyfunc jwt.Keyfunc
	if disc.JWKSURI != "" {
		keys, err := h.fetchJWKS(ctx, disc.JWKSURI)
		if err != nil {
			return "", fmt.Errorf("fetch jwks: %w", err)
		}
		keyfunc = jwksKeyfunc(keys)
	}
	sub, err := verifyCorosIDToken(idToken, disc.Issuer, clientID, now, keyfunc)
	if err != nil {
		return "", err
	}
	return corosMcpAccountID(disc.Issuer, sub), nil
}

func normalizeIssuer(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }

// verifyCorosIDToken 純函式（時間與金鑰由呼叫端注入，單元測試涵蓋）：驗證 id_token 並回傳 sub。
// keyfunc 非 nil → 驗簽（只接受 RS256／ES256）；nil → 只解析並驗 claims（見檔頭說明）。
// 兩條路徑都要求：iss 與 discovery issuer 相符（忽略結尾 /）、aud 含我們的 client_id、exp 存在且未過期（容忍 2 分鐘）、
// sub 非空。
func verifyCorosIDToken(idToken, issuer, clientID string, now time.Time, keyfunc jwt.Keyfunc) (string, error) {
	var claims jwt.RegisteredClaims
	timeFn := jwt.WithTimeFunc(func() time.Time { return now })
	if keyfunc != nil {
		parser := jwt.NewParser(
			jwt.WithValidMethods([]string{"RS256", "ES256"}),
			jwt.WithExpirationRequired(),
			jwt.WithLeeway(corosMcpIDTokenLeeway),
			timeFn,
		)
		if _, err := parser.ParseWithClaims(idToken, &claims, keyfunc); err != nil {
			return "", fmt.Errorf("id_token rejected: %w", err)
		}
	} else {
		if _, _, err := jwt.NewParser(timeFn).ParseUnverified(idToken, &claims); err != nil {
			return "", fmt.Errorf("id_token malformed: %w", err)
		}
		if claims.ExpiresAt == nil {
			return "", errors.New("id_token has no exp")
		}
		if !now.Before(claims.ExpiresAt.Add(corosMcpIDTokenLeeway)) {
			return "", errors.New("id_token expired")
		}
	}
	if normalizeIssuer(claims.Issuer) != normalizeIssuer(issuer) {
		return "", errors.New("id_token issuer mismatch")
	}
	audOK := false
	for _, a := range claims.Audience {
		if a == clientID {
			audOK = true
			break
		}
	}
	if !audOK {
		return "", errors.New("id_token audience mismatch")
	}
	sub := strings.TrimSpace(claims.Subject)
	if sub == "" {
		return "", errors.New("id_token has no sub")
	}
	return sub, nil
}

// corosMcpIssuerTag issuer 的區域標籤（https://mcpus.coros.com → "mcpus"）：COROS 有 us／eu／cn 多個 issuer，
// 各自的使用者資料庫，sub 可能撞號，所以綁定識別要帶區域前綴。無法判斷時用 "coros"。
func corosMcpIssuerTag(issuer string) string {
	u, err := url.Parse(strings.TrimSpace(issuer))
	if err != nil || u.Hostname() == "" {
		return "coros"
	}
	host := u.Hostname()
	if i := strings.IndexByte(host, '.'); i > 0 && strings.HasSuffix(host, ".coros.com") {
		return host[:i]
	}
	return "coros"
}

// corosMcpAccountID 衍生「綁定用帳號識別」：<區域標籤>:<sub>；超過 provider_user_id 的 64 字元上限就改存
// "h:" + sha256 十六進位前 62 字元（同一個 sub 永遠得到同一個值）。
func corosMcpAccountID(issuer, sub string) string {
	id := corosMcpIssuerTag(issuer) + ":" + sub
	if len(id) <= corosMcpAccountIDMax {
		return id
	}
	sum := sha256.Sum256([]byte(id))
	return "h:" + hex.EncodeToString(sum[:])[:corosMcpAccountIDMax-2]
}

// --- JWKS ---

type corosJWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func b64urlBig(s string) (*big.Int, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(b), nil
}

// parseJWKS 解析 JWKS（只收 RSA 與 P-256 EC 簽章金鑰；use=enc 的略過）。回傳 kid→公鑰。
func parseJWKS(body []byte) (map[string]crypto.PublicKey, error) {
	var set struct {
		Keys []corosJWK `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("decode jwks: %w", err)
	}
	out := map[string]crypto.PublicKey{}
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		switch k.Kty {
		case "RSA":
			n, err1 := b64urlBig(k.N)
			e, err2 := b64urlBig(k.E)
			if err1 != nil || err2 != nil || n.Sign() <= 0 || !e.IsInt64() || e.Int64() < 3 || e.Int64() > 1<<31-1 {
				continue
			}
			out[k.Kid] = &rsa.PublicKey{N: n, E: int(e.Int64())}
		case "EC":
			if k.Crv != "P-256" {
				continue
			}
			x, err1 := b64urlBig(k.X)
			y, err2 := b64urlBig(k.Y)
			if err1 != nil || err2 != nil {
				continue
			}
			curve := elliptic.P256()
			if !curve.IsOnCurve(x, y) { //nolint:staticcheck // 只用來驗證外部提供的公鑰座標在曲線上
				continue
			}
			out[k.Kid] = &ecdsa.PublicKey{Curve: curve, X: x, Y: y}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("jwks has no usable signing keys")
	}
	return out, nil
}

// jwksKeyfunc 依 id_token header 的 kid 選公鑰；沒有 kid 且 JWKS 只有一把時用那一把。
func jwksKeyfunc(keys map[string]crypto.PublicKey) jwt.Keyfunc {
	return func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if k, ok := keys[kid]; ok {
			return k, nil
		}
		if kid == "" && len(keys) == 1 {
			for _, k := range keys {
				return k, nil
			}
		}
		return nil, fmt.Errorf("no jwks key for kid %q", kid)
	}
}

// fetchJWKS 取公鑰集。uri 已在 discovery 時通過 host 驗證，這裡再驗一次（防被快取汙染的 doc）。
func (h *CorosMcpHandler) fetchJWKS(ctx context.Context, uri string) (map[string]crypto.PublicKey, error) {
	if err := corosMcpValidateHost(uri); err != nil {
		return nil, fmt.Errorf("jwks uri rejected: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseJWKS(body)
}
