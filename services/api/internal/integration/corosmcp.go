package integration

// COROS MCP 第一階段（連接＋讀取測試，不寫入任何活動）。
// 契約：docs/integration/COROS_MCP_STAGE1_CONTRACT.md；研究/設計草稿見 scratchpad coros_mcp/
// （A_protocol_spec.md、COROS_MCP_SPEC_DRAFT.md、C_dor_design.md）。
//
// 與既有 coros.go（provider='coros'，Partner API webhook）完全獨立、互不影響：
//   - provider 用獨立值 'coros_mcp'，(user_id, provider) 與既有 'coros' 列互不干涉。
//   - OAuth 是 public client + PKCE + 動態用戶端註冊(DCR)，不是 coros.go 的固定 client_id/secret。
//     2026-10-01 正式站實測：DCR 要求 client_secret_basic，COROS 仍登記成 token_endpoint_auth_method
//     "none"、不發 client_secret（與官方 skill coros_mcp_login.py 一致：public＋PKCE）。舊版存了「要求的」
//     方法，換 token 時送出「空密碼的 Basic 標頭」→ COROS（Spring Authorization Server）回 400
//     invalid_request → 擁有者看到 token_exchange_failed。現在一律以 COROS 實際登記的方法為準
//     （corosMcpEffectiveAuthMethod），註冊也直接要求 none。
//   - 第一階段刻意不寫入活動；第二階段（v871，見 corosmcp_sync.go、COROS_MCP_STAGE2_CONTRACT.md）才經
//     Repository.ImportActivity 匯入（source='coros'、external_id='mcp:'+labelId），仍只開放白名單。
//
// GA（全員開放）修正見 docs/integration/COROS_MCP_GA_CONTRACT.md：入口三態（entrygate，status／disconnect
// 不受限）、reauth_required_at／非破壞性重新授權、供應商帳號綁定（id_token sub）、節流改 Redis（corosmcp_throttle.go）、
// 讀取測試僅超管且只存摘要、Disconnect 同交易刪紀錄與 probe logs。
//
// 安全设计重點（見契約驗收段）：
//   - （Stage 1 的「白名單無 super_admin 旁路」已被 GA 契約 §2.1 取代：超管在 whitelist／open 狀態恆可，
//     hidden＝緊急關閉含超管，全部由 internal/integration/entrygate 判斷。）
//   - discovery 回傳的每一個端點 URL 都驗證 https + host 落在 coros.com/*.coros.com，防 SSRF／被導去他處。
//   - state 用既有 HMAC 簽章手法（比照 coros.go signState/verifyState），PKCE verifier 隨 state 一併簽入，
//     不需要 Redis/DB 往返（Neon 可以繼續睡）。
//   - secret／token 絕不進 log：DCR 回應存檔前先用 sanitizeRegistrationResponse 移除 client_secret；
//     postToken 類函式的錯誤訊息只帶 HTTP 狀態碼與 OAuth error／error_description，不帶其餘回應內文。

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/gpscalib"
	"github.com/dor/api/internal/integration/entrygate"
)

const (
	providerCorosMcp     = "coros_mcp"
	corosMcpScope        = "openid offline_access mcp.tools"
	corosMcpWhitelistKey = "coros_mcp_whitelist"
	// corosMcpEntryStateKey 入口三態（hidden|whitelist|open；缺鍵＝whitelist），見 entrygate。
	corosMcpEntryStateKey = "coros_mcp_entry_state"
	// corosMcpAutoSyncKey 自動同步總開關（kill switch）：缺鍵／空值＝開，0＝停（手動匯入仍可用）。
	corosMcpAutoSyncKey = "coros_mcp_autosync_enabled"
	// corosMcpDefaultWhitelist 缺鍵時的預設（migration 196 已插入同值這一列，這裡只是程式面兜底，
	// 比照 internal/gpsrawlog.defaultWhitelist 的雙重保險寫法）。
	corosMcpDefaultWhitelist = "sogobaga@gmail.com"
	corosMcpStateTTL         = 10 * time.Minute
	corosMcpDiscoveryTTL     = 6 * time.Hour
	corosMcpProbeWindow      = time.Minute // 讀取測試每人每分鐘最多 1 次
	corosMcpMCPTimeout       = 20 * time.Second
	corosMcpRefreshSkew      = 60 * time.Second // token 到期前 60 秒主動換新

	// OAuth 登入 CSRF 綁定（契約第 6 點）：/connect 種 nonce cookie，/callback 比對。Path 限縮在 callback 端點，
	// 其他請求不會夾帶；HttpOnly（JS 讀不到）、Secure、SameSite=Lax（COROS 導回是頂層 GET，Lax 會帶）、10 分鐘。
	corosMcpNonceCookie  = "dor_cmcp_n"
	corosMcpCallbackPath = "/api/v1/integrations/coros-mcp/callback"
	// state HMAC 金鑰用途隔離的 domain separation 字串：JWT_SECRET 不直接當 state 簽章金鑰。
	corosMcpStateKeyLabel = "dor/coros-mcp/state/v1"
)

// errCorosMcpReconnect：access token 已過期且無法換新（COROS 沒發 refresh token，或 refresh 被拒
// invalid_grant）——只能請使用者重新連接。Probe 回 409 {"error":"reconnect_required"}。
var errCorosMcpReconnect = errors.New("coros mcp: reconnect required")

// corosMcpTokenError：token／revoke／DCR 端點非 2xx 的錯誤。只保留 OAuth 標準欄位 error／
// error_description（不含任何 token），讓 Railway log 看得出 COROS 拒絕的真正原因——2026-10-01 第一次
// 正式連線失敗時 log 只有「http 400」，要另外查 DB 才找到根因。
type corosMcpTokenError struct {
	Status      int
	Code        string // OAuth error code，已驗證只含 [a-z_]
	Description string // 已去換行、截斷
}

func (e *corosMcpTokenError) Error() string {
	s := fmt.Sprintf("coros mcp oauth http %d", e.Status)
	if e.Code != "" {
		s += " " + e.Code
	}
	if e.Description != "" {
		s += ": " + e.Description
	}
	return s
}

// corosMcpParseOAuthError 只從回應抽 error／error_description 兩個欄位（其餘一律丟棄，避免帶出 token）。
func corosMcpParseOAuthError(status int, body []byte) *corosMcpTokenError {
	e := &corosMcpTokenError{Status: status}
	var m struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if json.Unmarshal(body, &m) != nil {
		return e
	}
	if code := strings.TrimSpace(m.Error); code != "" && len(code) <= 40 && strings.Trim(code, "abcdefghijklmnopqrstuvwxyz_") == "" {
		e.Code = code
	}
	desc := strings.Join(strings.Fields(m.ErrorDescription), " ")
	if r := []rune(desc); len(r) > 160 {
		desc = string(r[:160]) + "…"
	}
	e.Description = desc
	return e
}

// corosMcpEffectiveAuthMethod：實際可用的 token 端點驗證方式。只有「COROS 登記為 client_secret_basic
// 且真的發了 secret」才用 Basic；其餘（含舊版誤存的 client_secret_basic＋空 secret）一律 public(none)。
func corosMcpEffectiveAuthMethod(method, secret string) string {
	if method == "client_secret_basic" && secret != "" {
		return "client_secret_basic"
	}
	return "none"
}

// corosMcpProbeTools 讀取測試固定順序（契約第 8 點），queryUserInfo／FIT／任何寫入類工具一律不呼叫。
var corosMcpProbeTools = []string{"queryDevices", "querySportRecords", "getActivityDetail", "queryActivityLapData"}

// CorosMcpConfig 從 config.Config 注入。
type CorosMcpConfig struct {
	GatewayURL  string // discovery 入口，預設 https://mcp.coros.com
	RedirectURI string // 須與 DCR 註冊時送出的 redirect_uris 一致
	FrontendURL string
	JWTSecret   string
}

// CorosMcpHandler COROS MCP handler（連接／同步／狀態／讀取測試／中斷）。
type CorosMcpHandler struct {
	repo        *Repository
	cfg         CorosMcpConfig
	requireAuth func(http.Handler) http.Handler
	hc          *http.Client
	// rdb：節流／in-flight 鎖／冷卻的跨副本儲存（見 corosmcp_throttle.go）。nil（本機／測試）或 Redis 出錯時
	// 退回 mem（記憶體，單進程）——一律嚴格節流，不 fail-open。
	rdb *redis.Client
	mem corosMcpMem
	// now：可注入的時鐘（只影響記憶體節流表；Redis 的 TTL 是 Redis 自己的時間）。nil＝time.Now。
	now func() time.Time
	// autoSem：自動同步全域並發上限（容量 corosMcpAutoConcurrency）；滿了略過、不扣名額。經 sem() 延遲初始化
	// （測試直接以結構字面值建 handler 時也安全）。
	autoSem chan struct{}
	semOnce sync.Once
	// autoJitterMax：自動同步觸發前的隨機延遲上限（0＝不延遲，測試用）。
	autoJitterMax time.Duration
}

func NewCorosMcpHandler(repo *Repository, cfg CorosMcpConfig, requireAuth func(http.Handler) http.Handler, rdb *redis.Client) *CorosMcpHandler {
	if cfg.GatewayURL == "" {
		cfg.GatewayURL = "https://mcp.coros.com"
	}
	return &CorosMcpHandler{
		repo: repo, cfg: cfg, requireAuth: requireAuth,
		hc:            &http.Client{Timeout: corosMcpMCPTimeout},
		rdb:           rdb,
		autoJitterMax: corosMcpAutoJitterMax,
	}
}

// sem 自動同步並發信號量（延遲初始化）。
func (h *CorosMcpHandler) sem() chan struct{} {
	h.semOnce.Do(func() {
		if h.autoSem == nil {
			h.autoSem = make(chan struct{}, corosMcpAutoConcurrency)
		}
	})
	return h.autoSem
}

// Router 掛在 /api/v1/integrations/coros-mcp。/callback 公開，其餘需登入。
// 入口閘門（entrygate）：connect／callback／import／自動同步受限；status 與 disconnect 不受限（契約 §2.2：
// 只要有連線列就能看、能中斷）。probe 僅超管。
func (h *CorosMcpHandler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/callback", h.Callback)
	r.Group(func(r chi.Router) {
		r.Use(h.requireAuth)
		r.Post("/connect", h.Connect)
		r.Get("/status", h.Status)
		r.Post("/probe", h.Probe)
		r.Post("/import", h.Import)
		r.Post("/disconnect", h.Disconnect)
	})
	return r
}

// --- 入口閘門（entrygate，GA 契約 §2.1/§2.2）---

// corosMcpUser 閘門與 /status 需要的使用者欄位。
type corosMcpUser struct {
	Email   string
	Code    string
	IsSuper bool
}

func (h *CorosMcpHandler) loadUser(ctx context.Context, userID string) (corosMcpUser, error) {
	var u corosMcpUser
	err := h.repo.db.QueryRow(ctx,
		`SELECT COALESCE(email,''), COALESCE(account_code,''), COALESCE(is_super_admin,FALSE) FROM users WHERE id=$1`,
		userID).Scan(&u.Email, &u.Code, &u.IsSuper)
	return u, err
}

// requireEntry 「需登入＋入口開放」的共用前置檢查（connect／import；callback 與自動同步另行檢查）。
// ok=false 時已寫完回應。與 profile Dashboard 的 coros_mcp_entry 走同一個判斷（entrygate.Resolve）。
func (h *CorosMcpHandler) requireEntry(w http.ResponseWriter, r *http.Request) (userID string, ok bool) {
	userID, _ = r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return "", false
	}
	entry, err := h.entryForUser(r.Context(), userID)
	if err != nil {
		log.Error().Err(err).Str("user", userID).Msg("coros mcp: entry gate lookup failed")
		respondErr(w, http.StatusInternalServerError, "failed")
		return "", false
	}
	if entry != entrygate.Shown {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return "", false
	}
	return userID, true
}

// entryForUser 只有 userID 時的入口判斷（API 閘門與 callback 用）。
func (h *CorosMcpHandler) entryForUser(ctx context.Context, userID string) (string, error) {
	return EntryForUser(ctx, h.repo.db, EntryProviderCoros, userID)
}

// requireUser 只要求登入（status／disconnect：不受入口狀態限制）。
func (h *CorosMcpHandler) requireUser(w http.ResponseWriter, r *http.Request) (userID string, ok bool) {
	userID, _ = r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return "", false
	}
	return userID, true
}

// --- PKCE（純函式，供單元測試）---

func corosMcpGeneratePKCEVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func corosMcpPKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// --- state 簽章（HMAC，比照 coros.go signState/verifyState，額外帶 issuer／PKCE verifier）---

// corosMcpStateKey 用途隔離金鑰＝HMAC-SHA256(JWT_SECRET, "dor/coros-mcp/state/v1")：state 的簽章金鑰不等於
// JWT_SECRET 本身，萬一 state 簽章被拿來當 oracle，也不會洩漏或重用到 JWT 簽章金鑰（契約第 6 點）。
func corosMcpStateKey(jwtSecret string) string {
	m := hmac.New(sha256.New, []byte(jwtSecret))
	m.Write([]byte(corosMcpStateKeyLabel))
	return string(m.Sum(nil))
}

// corosMcpMAC 以「已衍生」的 key 簽（呼叫端用 corosMcpStateKey 衍生）。
func corosMcpMAC(secret, msg string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// corosMcpSignState 把 userID/issuer/verifier/nonce/到期 五個欄位簽進 state，callback 端不需 DB/Redis
// 往返即可還原整個 PKCE 交換所需的資訊（契約第 4 點）。nonce 同時種在使用者瀏覽器的 cookie（見 Connect），
// callback 要求兩者相符，擋「攻擊者把自己的授權連結（state）塞給受害者點」的登入 CSRF（契約第 6 點）。
// secret 傳 JWT_SECRET 原值，內部以 corosMcpStateKey 衍生專用金鑰。
func corosMcpSignState(secret, userID, issuer, verifier, nonce string, ttl time.Duration) string {
	msg := strings.Join([]string{userID, issuer, verifier, nonce, strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)}, "\n")
	return base64.RawURLEncoding.EncodeToString([]byte(msg)) + "." + corosMcpMAC(corosMcpStateKey(secret), msg)
}

func corosMcpVerifyState(secret, state string) (userID, issuer, verifier, nonce string, ok bool) {
	i := strings.LastIndex(state, ".")
	if i < 0 {
		return "", "", "", "", false
	}
	raw, sig := state[:i], state[i+1:]
	msgBytes, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", "", "", "", false
	}
	msg := string(msgBytes)
	if !hmac.Equal([]byte(sig), []byte(corosMcpMAC(corosMcpStateKey(secret), msg))) {
		return "", "", "", "", false
	}
	parts := strings.Split(msg, "\n")
	if len(parts) != 5 {
		return "", "", "", "", false
	}
	exp, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", "", "", "", false
	}
	return parts[0], parts[1], parts[2], parts[3], true
}

// corosMcpNonceMatches 常數時間比對 cookie 值與 state 內的 nonce；任一為空一律不符。
func corosMcpNonceMatches(cookieVal, stateNonce string) bool {
	if cookieVal == "" || stateNonce == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookieVal), []byte(stateNonce)) == 1
}

// corosMcpNewNonceCookie 組 nonce cookie；maxAge<0 代表清除（http.Cookie 的 MaxAge<0 會輸出 Max-Age=0）。
func corosMcpNewNonceCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: corosMcpNonceCookie, Value: value, Path: corosMcpCallbackPath,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: maxAge,
	}
}

// --- host 驗證（防 SSRF，契約第 2 點：discovery 回傳的每個端點都要驗）---

// corosMcpValidateHost：必須是 https、無 userinfo、host 不是 IP 字面量、port 只能是空或 443、
// host 必須等於 coros.com 或是其子網域（*.coros.com）。
func corosMcpValidateHost(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("無效 URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("必須是 https")
	}
	if u.User != nil {
		return fmt.Errorf("不允許 URL 內嵌帳密")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("缺少 host")
	}
	if net.ParseIP(host) != nil {
		return fmt.Errorf("不允許 IP 字面量 host")
	}
	if port := u.Port(); port != "" && port != "443" {
		return fmt.Errorf("port 只能是 443")
	}
	if host != "coros.com" && !strings.HasSuffix(host, ".coros.com") {
		return fmt.Errorf("host 必須是 coros.com 或其子網域")
	}
	return nil
}

// --- discovery（記憶體快取 6 小時，見契約第 2 點）---

type corosMcpDiscoveryDoc struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RevocationEndpoint    string `json:"revocation_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint"`
	// JWKSURI：OIDC 公鑰集（有的話 id_token 驗簽，見 corosmcp_identity.go）；同樣要通過 host 驗證。
	JWKSURI string `json:"jwks_uri"`
}

type corosMcpDiscoveryCacheEntry struct {
	doc *corosMcpDiscoveryDoc
	at  time.Time
}

var corosMcpDiscoveryCache = struct {
	mu sync.Mutex
	m  map[string]corosMcpDiscoveryCacheEntry
}{m: map[string]corosMcpDiscoveryCacheEntry{}}

func corosMcpDiscoveryCacheGet(key string) (*corosMcpDiscoveryDoc, bool) {
	corosMcpDiscoveryCache.mu.Lock()
	defer corosMcpDiscoveryCache.mu.Unlock()
	e, ok := corosMcpDiscoveryCache.m[key]
	if !ok || time.Since(e.at) >= corosMcpDiscoveryTTL {
		return nil, false
	}
	return e.doc, true
}

func corosMcpDiscoveryCacheSet(key string, doc *corosMcpDiscoveryDoc) {
	corosMcpDiscoveryCache.mu.Lock()
	defer corosMcpDiscoveryCache.mu.Unlock()
	corosMcpDiscoveryCache.m[key] = corosMcpDiscoveryCacheEntry{doc: doc, at: time.Now()}
}

// discover 對 base（gateway 或已知 issuer）取得 discovery 文件；每個回傳的端點 URL 都經
// corosMcpValidateHost 驗證，任一不合格就整體拒絕（契約：防 SSRF／被導去他處）。
// 同時以 base 與（驗證通過後的）doc.Issuer 兩個 key 快取，/connect 用 gateway 查、/callback 用
// 已知 issuer 查都能命中同一份快取。
func (h *CorosMcpHandler) discover(ctx context.Context, base string) (*corosMcpDiscoveryDoc, error) {
	if doc, ok := corosMcpDiscoveryCacheGet(base); ok {
		return doc, nil
	}
	if err := corosMcpValidateHost(base); err != nil {
		return nil, fmt.Errorf("discovery base 被拒絕: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/.well-known/openid-configuration", nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var doc corosMcpDiscoveryDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("decode discovery: %w", err)
	}
	for _, u := range []string{doc.Issuer, doc.AuthorizationEndpoint, doc.TokenEndpoint, doc.RevocationEndpoint, doc.RegistrationEndpoint, doc.JWKSURI} {
		if u == "" {
			continue
		}
		if err := corosMcpValidateHost(u); err != nil {
			return nil, fmt.Errorf("discovery 端點被拒絕: %w", err)
		}
	}
	if doc.Issuer == "" {
		return nil, fmt.Errorf("discovery 回應缺少 issuer")
	}
	corosMcpDiscoveryCacheSet(base, &doc)
	corosMcpDiscoveryCacheSet(doc.Issuer, &doc)
	return &doc, nil
}

// --- DCR（動態用戶端註冊，每個 issuer 一次，pg advisory lock 防併發重複註冊）---

type corosMcpClient struct {
	Issuer       string
	ClientID     string
	ClientSecret string // 已解密；AuthMethod="none" 時為空字串
	AuthMethod   string
}

type corosMcpDCRRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	Scope                   string   `json:"scope"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// corosMcpSanitizeRegistration 移除 client_secret 相關鍵，供存檔用的 raw_response（契約第 3 點：
// 「任何 log／存檔不得出現 secret」）。
func corosMcpSanitizeRegistration(raw map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range raw {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "secret") {
			continue
		}
		out[k] = v
	}
	return out
}

// corosMcpRegister 送一次 DCR 請求；authMethod 失敗（非 2xx 或缺 client_id）時由呼叫端決定是否
// 換 authMethod 重試一次（契約：confidential 優先，被拒才退回 public+PKCE）。
func (h *CorosMcpHandler) corosMcpRegister(ctx context.Context, endpoint, authMethod string) (clientID, clientSecret string, raw map[string]any, err error) {
	body, _ := json.Marshal(corosMcpDCRRequest{
		ClientName:              "DOR",
		RedirectURIs:            []string{h.cfg.RedirectURI},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		Scope:                   corosMcpScope,
		TokenEndpointAuthMethod: authMethod,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.hc.Do(req)
	if err != nil {
		return "", "", nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// 只帶 OAuth 標準 error／error_description，不帶其餘回應內文，避免洩漏。
		return "", "", nil, fmt.Errorf("dcr: %w", corosMcpParseOAuthError(resp.StatusCode, respBody))
	}
	var m map[string]any
	if err := json.Unmarshal(respBody, &m); err != nil {
		return "", "", nil, fmt.Errorf("decode dcr response: %w", err)
	}
	cid, _ := m["client_id"].(string)
	if cid == "" {
		return "", "", nil, fmt.Errorf("dcr 回應缺少 client_id")
	}
	csec, _ := m["client_secret"].(string)
	return cid, csec, m, nil
}

// ensureClient 查既有 coros_mcp_clients；查無才在 advisory lock 保護下註冊一次（契約第 3 點：
// 「併發以 pg advisory lock 保證只註冊一次」）。
func (h *CorosMcpHandler) ensureClient(ctx context.Context, issuer string, disc *corosMcpDiscoveryDoc) (*corosMcpClient, error) {
	if c, err := h.getClient(ctx, issuer); err == nil && c != nil {
		return c, nil
	} else if err != nil {
		return nil, err
	}

	tx, err := h.repo.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// hashtext 把 issuer 字串轉成 int4 做 lock key；同一 issuer 併發註冊會在這裡排隊，
	// 第二個進來的 goroutine 等到鎖後，下面的 SELECT 會查到第一個已寫入的列，直接回傳不重複註冊。
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, issuer); err != nil {
		return nil, err
	}

	var existing corosMcpClient
	var encSecret *string
	err = tx.QueryRow(ctx, `SELECT client_id, client_secret, token_endpoint_auth_method FROM coros_mcp_clients WHERE issuer=$1`, issuer).
		Scan(&existing.ClientID, &encSecret, &existing.AuthMethod)
	if err == nil {
		existing.Issuer = issuer
		if encSecret != nil {
			existing.ClientSecret, err = decryptToken(*encSecret)
			if err != nil {
				return nil, fmt.Errorf("decrypt coros_mcp client secret: %w", err)
			}
		}
		existing.AuthMethod = corosMcpEffectiveAuthMethod(existing.AuthMethod, existing.ClientSecret)
		return &existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	// 查無：直接以 public(none)＋PKCE 註冊（與 COROS 官方 skill 相同；COROS 即使收到 client_secret_basic
	// 也只登記 none、不發 secret，見檔頭 2026-10-01 說明）。存「COROS 回應裡實際登記的方法」，不存要求的。
	clientID, clientSecret, raw, regErr := h.corosMcpRegister(ctx, disc.RegistrationEndpoint, "none")
	if regErr != nil {
		return nil, fmt.Errorf("coros mcp DCR 註冊失敗: %w", regErr)
	}
	registered, _ := raw["token_endpoint_auth_method"].(string)
	authMethod := corosMcpEffectiveAuthMethod(registered, clientSecret)
	var encSecretVal any
	if clientSecret != "" {
		encSecretVal = encryptToken(clientSecret)
	}
	rawJSON, _ := json.Marshal(corosMcpSanitizeRegistration(raw))
	if _, err := tx.Exec(ctx, `
		INSERT INTO coros_mcp_clients (issuer, client_id, client_secret, token_endpoint_auth_method, raw_response)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (issuer) DO NOTHING`,
		issuer, clientID, encSecretVal, authMethod, rawJSON); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &corosMcpClient{Issuer: issuer, ClientID: clientID, ClientSecret: clientSecret, AuthMethod: authMethod}, nil
}

func (h *CorosMcpHandler) getClient(ctx context.Context, issuer string) (*corosMcpClient, error) {
	var c corosMcpClient
	var encSecret *string
	err := h.repo.db.QueryRow(ctx, `SELECT client_id, client_secret, token_endpoint_auth_method FROM coros_mcp_clients WHERE issuer=$1`, issuer).
		Scan(&c.ClientID, &encSecret, &c.AuthMethod)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Issuer = issuer
	if encSecret != nil {
		if c.ClientSecret, err = decryptToken(*encSecret); err != nil {
			return nil, fmt.Errorf("decrypt coros_mcp client secret: %w", err)
		}
	}
	// 舊版（v868）誤存 client_secret_basic＋無 secret 的列在這裡就地校正成 none，不必改資料庫。
	c.AuthMethod = corosMcpEffectiveAuthMethod(c.AuthMethod, c.ClientSecret)
	return &c, nil
}

// --- 連線儲存（user_integrations，provider='coros_mcp'；見 migration 196）---

type corosMcpConnection struct {
	ID           string
	UserID       string
	Issuer       string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Scope        string
	ConnectedAt  time.Time
	LastProbeAt  *time.Time
	// LastSyncedAt：最近一次成功同步時間（migration 197）；從未同步＝nil。
	LastSyncedAt *time.Time
	// ReauthRequiredAt：refresh 失敗（invalid_grant／401 後 refresh 失敗／無 refresh token 且已過期）時寫入，
	// 成功換 token 或重新授權時清空（migration 198，GA 契約 §2.3）。nil＝目前沒有已知的授權問題。
	ReauthRequiredAt *time.Time
	// ProviderUserID：綁定的 COROS 帳號識別（id_token sub 衍生，見 corosmcp_identity.go；migration 198 的
	// 部分唯一索引保證同一個 COROS 帳號只能連到一個 DOR 帳號）。空字串＝尚未綁定（舊連線或 COROS 沒發 id_token）。
	ProviderUserID string
}

// NeedsReauth 這條連線現在是否需要使用者重新授權：已被標記，或 access token 已過期且沒有 refresh token
// （refresh 根本無從嘗試——不必等到下一次同步失敗才知道）。
func (c *corosMcpConnection) NeedsReauth(now time.Time) bool {
	if c.ReauthRequiredAt != nil {
		return true
	}
	return c.RefreshToken == "" && !c.ExpiresAt.After(now)
}

// getConnectionMeta 讀連線列但「不解密」token（RefreshToken 欄位是資料庫存的原字串，空字串＝沒有 refresh token）。
// /status 與 /disconnect 用它：金鑰遺失或密文損毀時這兩支仍然要能用。查無回 (nil, nil)。
func (h *CorosMcpHandler) getConnectionMeta(ctx context.Context, userID string) (*corosMcpConnection, error) {
	var c corosMcpConnection
	err := h.repo.db.QueryRow(ctx, `
		SELECT id::text, access_token, refresh_token, expires_at, COALESCE(scope,''), created_at,
		       COALESCE(issuer,''), last_probe_at, last_synced_at, reauth_required_at, COALESCE(provider_user_id,'')
		FROM user_integrations WHERE user_id=$1 AND provider=$2`, userID, providerCorosMcp).
		Scan(&c.ID, &c.AccessToken, &c.RefreshToken, &c.ExpiresAt, &c.Scope, &c.ConnectedAt, &c.Issuer, &c.LastProbeAt, &c.LastSyncedAt,
			&c.ReauthRequiredAt, &c.ProviderUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.UserID = userID
	return &c, nil
}

// getConnection 讀連線列並解密 access／refresh token（同步、讀取測試、撤銷需要明文）。
func (h *CorosMcpHandler) getConnection(ctx context.Context, userID string) (*corosMcpConnection, error) {
	c, err := h.getConnectionMeta(ctx, userID)
	if err != nil || c == nil {
		return c, err
	}
	if c.AccessToken, err = decryptToken(c.AccessToken); err != nil {
		return nil, fmt.Errorf("decrypt coros_mcp access token: %w", err)
	}
	if c.RefreshToken, err = decryptToken(c.RefreshToken); err != nil {
		return nil, fmt.Errorf("decrypt coros_mcp refresh token: %w", err)
	}
	return c, nil
}

// saveConnection 連接成功（callback）後寫入／更新連線列。
//   - 全新使用者：INSERT，created_at 取預設 NOW()＝匯入 floor（只匯入連接當下之後的活動）。
//   - 已連接者重新授權（GA 契約 §2.4，非破壞性）：只更新 token／expires_at／scope／issuer 並清掉
//     reauth_required_at；⚠️ 不動 created_at（floor）——舊版 ON CONFLICT 會 created_at=NOW()，授權到期後
//     重新連接就把 floor 推到現在，到期～重連之間的跑步永久被當成「連接前」略過（稽核 HIGH）。
//   - provider_user_id 只在新值非空時才覆蓋（accountID 空＝這次沒拿到帳號識別，保留舊值）。
//   - token 一律 EncryptTokenStrict：沒有有效的加密金鑰就拒絕寫入（fail-closed，ErrTokenKeyMissing）。
//   - 撞 (provider, provider_user_id) 部分唯一索引（migration 198）→ ErrProviderAccountLinked。
func (h *CorosMcpHandler) saveConnection(ctx context.Context, userID, issuer, access, refresh string, expiresAt time.Time, scope, accountID string) error {
	encAccess, err := EncryptTokenStrict(access)
	if err != nil {
		return err
	}
	encRefresh, err := EncryptTokenStrict(refresh)
	if err != nil {
		return err
	}
	_, err = h.repo.db.Exec(ctx, `
		INSERT INTO user_integrations (user_id, provider, provider_user_id, access_token, refresh_token, expires_at, scope, athlete_name, issuer)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'',$8)
		ON CONFLICT (user_id, provider) DO UPDATE SET
			provider_user_id   = CASE WHEN EXCLUDED.provider_user_id <> '' THEN EXCLUDED.provider_user_id ELSE user_integrations.provider_user_id END,
			access_token       = EXCLUDED.access_token,
			refresh_token      = EXCLUDED.refresh_token,
			expires_at         = EXCLUDED.expires_at,
			scope              = EXCLUDED.scope,
			issuer             = EXCLUDED.issuer,
			reauth_required_at = NULL,
			updated_at         = NOW()`,
		userID, providerCorosMcp, accountID, encAccess, encRefresh, expiresAt, scope, issuer)
	if err != nil {
		if IsProviderAccountConflict(err) {
			return ErrProviderAccountLinked
		}
		return err
	}
	return nil
}

// updateTokens refresh 成功後寫回新 token，並清掉 reauth_required_at（授權已恢復）。fail-closed 加密同上。
func (h *CorosMcpHandler) updateTokens(ctx context.Context, id, access, refresh string, expiresAt time.Time) error {
	encAccess, err := EncryptTokenStrict(access)
	if err != nil {
		return err
	}
	encRefresh, err := EncryptTokenStrict(refresh)
	if err != nil {
		return err
	}
	_, err = h.repo.db.Exec(ctx,
		`UPDATE user_integrations SET access_token=$1, refresh_token=$2, expires_at=$3, reauth_required_at=NULL, updated_at=NOW() WHERE id=$4`,
		encAccess, encRefresh, expiresAt, id)
	return err
}

// markReauthRequired 標記「這條連線需要使用者重新授權」（只在尚未標記時寫入，保留第一次發生的時間）。
// 刻意不動 updated_at：日報「疑似靜默中斷」的保護期看 updated_at，標記本身不該把它往後延。
// 呼叫端只記 log、不擋流程（標記失敗時 /status 仍會因「已過期且無 refresh token」動態判定需要重新授權）。
func (h *CorosMcpHandler) markReauthRequired(ctx context.Context, connID string) {
	if connID == "" || h.repo == nil || h.repo.db == nil {
		return
	}
	if _, err := h.repo.db.Exec(ctx,
		`UPDATE user_integrations SET reauth_required_at = NOW() WHERE id=$1 AND reauth_required_at IS NULL`, connID); err != nil {
		log.Warn().Err(err).Msg("coros mcp: mark reauth_required_at failed")
	}
}

func (h *CorosMcpHandler) touchLastProbe(ctx context.Context, userID string, at time.Time) error {
	_, err := h.repo.db.Exec(ctx,
		`UPDATE user_integrations SET last_probe_at=$1 WHERE user_id=$2 AND provider=$3`, at, userID, providerCorosMcp)
	return err
}

// --- probe_logs（GA 契約 §3.2：只存摘要，不存任何 COROS 原始回應）---

// logProbe 寫一筆讀取測試摘要（step／ok／count／錯誤代碼）。request 欄位固定 NULL、response 只含 summary——
// 不保存 tools/list 全文、活動明細、逐公里分段或 COROS 的錯誤字串（資料最小化；前台也不顯示 COROS 原始錯誤）。
func (h *CorosMcpHandler) logProbe(ctx context.Context, userID string, summary corosMcpStepSummary, at time.Time) error {
	status := "ok"
	if !summary.OK {
		status = "error"
	}
	respJSON, _ := json.Marshal(probeLogResponse{Summary: summary})
	_, err := h.repo.db.Exec(ctx, `
		INSERT INTO coros_mcp_probe_logs (user_id, tool, request, response, status, created_at)
		VALUES ($1,$2,NULL,$3,$4,$5)`, userID, summary.Step, respJSON, status, at)
	if err != nil {
		log.Error().Err(err).Str("user", userID).Str("tool", summary.Step).Msg("coros mcp: write probe log failed")
	}
	return err
}

// corosMcpProbeLogRetentionDays：coros_mcp_probe_logs 保存期限（契約第 8 點、migration 196 註解：
// 「30 天保存期限，併入既有每日報告清理排程，不新增排程」）。比照 internal/gpsrawlog.retentionDays
// 的做法，獨立成常數方便測試與日後調整。
const corosMcpProbeLogRetentionDays = 30

// corosMcpProbeLogPurgeSQL 是 PurgeExpiredProbeLogs 的純函式核心：組出 DELETE 語句與保存天數參數，
// 方便單元測試斷言 SQL 文字與天數本身，不必真的連 DB（比照 gpsrawlog.retentionDeleteSQL）。用
// make_interval(days => $1) 而非字串拼接組 interval 字面值，避免 SQL injection 疑慮。
func corosMcpProbeLogPurgeSQL() (string, int) {
	return `DELETE FROM coros_mcp_probe_logs WHERE created_at < now() - make_interval(days => $1)`, corosMcpProbeLogRetentionDays
}

// PurgeExpired 刪除超過保存期限（見 corosMcpProbeLogRetentionDays）的讀取測試紀錄，回傳刪除筆數。
// 供 internal/ops 每日報告排程呼叫（契約第 8 點：「不另開週期性 DB 查詢」，掛在既有排程，
// 比照 gpsrawlog.PurgeExpired／internal/ops RunDailyReportLoop／buildDailyReportData 的既有慣例）。
// 表尚未建立（migration 196 未套用）或查詢本身失敗時由呼叫端 warn 後略過即可，不影響報告其餘段落。
//
// 做成 *CorosMcpHandler 的方法（而非自由函式吃 *pgxpool.Pool）是因為 internal/integration（本檔
// 所在套件，見 terra.go）已經 import internal/ops（供 TerraHandler 實作 ops.WearableReporter 用），
// 若 internal/ops 反過來 import internal/integration 會形成 import cycle。因此比照既有 WearableReporter
// 的依賴反轉寫法：ops 套件只定義一個小介面（ops.ProbeLogPurger），main.go 把 corosMcpHandler 注入
// 進去，ops 完全不需要知道 internal/integration 的存在。方法名 PurgeExpired（而非
// PurgeExpiredProbeLogs）是為了滿足 ops.ProbeLogPurger 介面簽章。
func (h *CorosMcpHandler) PurgeExpired(ctx context.Context) (int, error) {
	sql, days := corosMcpProbeLogPurgeSQL()
	ct, err := h.repo.db.Exec(ctx, sql, days)
	if err != nil {
		return 0, err
	}
	return int(ct.RowsAffected()), nil
}

// corosMcpStepSummary 回傳給前台的單步摘要（契約 Step 型別），不含原始回應內文。
// Error 是穩定的「錯誤代碼」（corosMcpErrorCode：reauth_required／anomaly／rate_limited／upstream_5xx／
// tool_error／no_activity…），不是 COROS 的原始錯誤字串。
type corosMcpStepSummary struct {
	Step  string `json:"step"`
	OK    bool   `json:"ok"`
	Count *int   `json:"count"`
	Error string `json:"error,omitempty"`
}

// probeLogResponse DB 裡 response 欄位的形狀：只有 summary（供 /status 重建摘要）。舊版（Stage 1/2）存的資料
// 還有 raw 欄位，讀取時直接忽略；不再寫入。
type probeLogResponse struct {
	Summary corosMcpStepSummary `json:"summary"`
}

// --- HTTP handlers ---

// POST /connect → { "url": authorize URL }
// 入口閘門：entrygate（hidden＝緊急關閉含超管；whitelist＝超管＋白名單；open＝全員）。已連接者也可以走——
// 那就是「重新授權」（GA 契約 §2.4）：callback 對既有列只更新 token 並清掉 reauth_required_at，保留 created_at（floor）。
func (h *CorosMcpHandler) Connect(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.requireEntry(w, r)
	if !ok {
		return
	}
	// 沒有有效的 token 加密金鑰就別讓使用者走完 COROS 授權才在 callback 失敗（直連 token 一律 fail-closed 拒存，
	// 見 EncryptTokenStrict；啟動時已 log.Error＋Telegram 告警，這裡讓前台立刻得到「暫時無法使用」）。
	if !TokenKeyConfigured() {
		log.Error().Str("user", userID).Msg("coros mcp: connect refused, token encryption key missing")
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	disc, err := h.discover(r.Context(), h.cfg.GatewayURL)
	if err != nil {
		log.Error().Err(err).Msg("coros mcp: discovery failed")
		respondErr(w, http.StatusBadGateway, "discovery failed")
		return
	}
	client, err := h.ensureClient(r.Context(), disc.Issuer, disc)
	if err != nil {
		log.Error().Err(err).Msg("coros mcp: ensure client failed")
		respondErr(w, http.StatusBadGateway, "client registration failed")
		return
	}
	verifier, err := corosMcpGeneratePKCEVerifier()
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	state := corosMcpSignState(h.cfg.JWTSecret, userID, disc.Issuer, verifier, nonce, corosMcpStateTTL)
	http.SetCookie(w, corosMcpNewNonceCookie(nonce, int(corosMcpStateTTL.Seconds())))
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", client.ClientID)
	q.Set("redirect_uri", h.cfg.RedirectURI)
	q.Set("scope", corosMcpScope)
	q.Set("code_challenge", corosMcpPKCEChallenge(verifier))
	q.Set("code_challenge_method", "S256")
	q.Set("resource", disc.Issuer+"/mcp")
	q.Set("state", state)
	respondJSON(w, http.StatusOK, map[string]string{"url": disc.AuthorizationEndpoint + "?" + q.Encode()})
}

// corosMcpFrontendRedirect 組回前台固定路徑（契約第 5 點：只導回自家固定路徑，不接受任意 return URL）。
func (h *CorosMcpHandler) corosMcpFrontendRedirect(status, reason string) string {
	u := strings.TrimRight(h.cfg.FrontendURL, "/") + "/?coros_mcp=" + status
	if reason != "" {
		u += "&reason=" + url.QueryEscape(reason)
	}
	return u
}

// getBinding 不解密 token 的輕量讀取：這位使用者目前有沒有連線列、已綁定的 COROS 帳號識別（可為空）。
func (h *CorosMcpHandler) getBinding(ctx context.Context, userID string) (exists bool, accountID string, err error) {
	err = h.repo.db.QueryRow(ctx,
		`SELECT TRUE, COALESCE(provider_user_id,'') FROM user_integrations WHERE user_id=$1 AND provider=$2`,
		userID, providerCorosMcp).Scan(&exists, &accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	return exists, accountID, err
}

// GET /callback?code&state（公開）→ 302 固定路徑。
func (h *CorosMcpHandler) Callback(w http.ResponseWriter, r *http.Request) {
	redirectErr := func(reason string) {
		http.Redirect(w, r, h.corosMcpFrontendRedirect("error", reason), http.StatusFound)
	}
	// 登入 CSRF 綁定：必須帶同一瀏覽器在 /connect 種下的 nonce cookie 才繼續。不論後面任何結果（含 state
	// 無效）都先清 cookie，一個 nonce 只用一次。不符 → state_mismatch，不換 token、不寫連線。
	cookieVal := ""
	if c, err := r.Cookie(corosMcpNonceCookie); err == nil {
		cookieVal = c.Value
	}
	http.SetCookie(w, corosMcpNewNonceCookie("", -1))
	userID, issuer, verifier, nonce, ok := corosMcpVerifyState(h.cfg.JWTSecret, r.URL.Query().Get("state"))
	if !ok {
		redirectErr("invalid_state")
		return
	}
	if !corosMcpNonceMatches(cookieVal, nonce) {
		redirectErr("state_mismatch")
		return
	}
	if r.URL.Query().Get("error") != "" {
		redirectErr("denied")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		redirectErr("missing_code")
		return
	}
	// 入口閘門（GA 契約 §2.1）：使用者在授權頁停留期間入口可能被關掉（緊急關閉）——callback 也要再判斷一次，
	// 不換 token、不寫連線。state 已證明 userID 是發起 /connect 的那位。
	if entry, err := h.entryForUser(r.Context(), userID); err != nil {
		log.Error().Err(err).Msg("coros mcp callback: entry gate lookup failed")
		redirectErr("save_failed")
		return
	} else if entry != entrygate.Shown {
		redirectErr("entry_closed")
		return
	}
	disc, err := h.discover(r.Context(), issuer)
	if err != nil {
		log.Error().Err(err).Msg("coros mcp callback: discovery failed")
		redirectErr("discovery_failed")
		return
	}
	client, err := h.getClient(r.Context(), issuer)
	if err != nil || client == nil {
		log.Error().Err(err).Msg("coros mcp callback: client not found")
		redirectErr("client_missing")
		return
	}
	tok, err := h.exchangeCode(r.Context(), disc.TokenEndpoint, client, code, verifier)
	if err != nil {
		log.Error().Err(err).Str("auth_method", client.AuthMethod).Msg("coros mcp callback: token exchange failed")
		reason := "token_exchange_failed"
		var te *corosMcpTokenError
		if errors.As(err, &te) && te.Code != "" {
			reason += ":" + te.Code // 例：token_exchange_failed:invalid_grant，擁有者截圖就看得出原因
		}
		redirectErr(reason)
		return
	}
	// 帳號綁定（GA 契約 §2.6）：id_token 的 sub。沒有 id_token＝無法綁定（連線仍建立，日報備註未綁定人數）；
	// 有 id_token 但驗證失敗＝拒絕（快速暴露實作／設定問題，也避免用不可信的識別綁定）。
	accountID, idErr := h.corosMcpIdentity(r.Context(), disc, client.ClientID, tok.IDToken, h.timeNow())
	if idErr != nil {
		log.Error().Err(idErr).Msg("coros mcp callback: id_token verification failed")
		redirectErr("identity_invalid")
		return
	}
	if accountID == "" {
		log.Warn().Str("user", userID).Msg("coros mcp callback: token response has no id_token — connecting without account binding")
	}
	existed, boundID, err := h.getBinding(r.Context(), userID)
	if err != nil {
		log.Error().Err(err).Msg("coros mcp callback: read existing connection failed")
		redirectErr("save_failed")
		return
	}
	// 重新授權時換了另一個 COROS 帳號（已綁定的識別與這次不同）：拒絕——請先中斷連線（會刪匯入紀錄）再用新帳號連接，
	// 否則同一個 DOR 帳號可以不經中斷就輪流掛不同 COROS 帳號、把舊帳號的綁定釋出去給別人再領一次。
	if existed && boundID != "" && accountID != "" && boundID != accountID {
		redirectErr("account_changed")
		return
	}
	expiresAt := time.Now().Add(1 * time.Hour)
	if tok.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	if err := h.saveConnection(r.Context(), userID, issuer, tok.AccessToken, tok.RefreshToken, expiresAt, tok.Scope, accountID); err != nil {
		if errors.Is(err, ErrProviderAccountLinked) {
			http.Redirect(w, r, h.corosMcpFrontendRedirect("already_linked", ""), http.StatusFound)
			return
		}
		log.Error().Err(err).Msg("coros mcp callback: save connection failed")
		redirectErr("save_failed")
		return
	}
	if existed {
		// 重新授權成功：立即補同步（起點 max(floor, last_synced_at − 1 天)，上限 30 天；背景執行，不擋導回）。
		h.reauthCatchUp(userID)
	}
	http.Redirect(w, r, h.corosMcpFrontendRedirect("connected", ""), http.StatusFound)
}

// corosMcpTokenResp 標準 OAuth2 token 回應（access/refresh token exchange、refresh 共用）。
// IDToken：scope 含 openid 時 token 端點會一併回 OIDC id_token（只用來取帳號識別 sub，不落地）。
type corosMcpTokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
}

func (h *CorosMcpHandler) postTokenForm(ctx context.Context, tokenURL string, form url.Values, client *corosMcpClient) (*corosMcpTokenResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// 只有真的有 secret 才送 Basic：空密碼的 Basic 標頭會被 COROS 直接以 400 invalid_request 拒絕
	// （2026-10-01 正式站第一次連線失敗的根因）。public client 只靠 body 裡的 client_id＋PKCE。
	if corosMcpEffectiveAuthMethod(client.AuthMethod, client.ClientSecret) == "client_secret_basic" {
		req.SetBasicAuth(client.ClientID, client.ClientSecret)
	}
	resp, err := h.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, corosMcpParseOAuthError(resp.StatusCode, body) // 只帶 error／error_description，不帶 token
	}
	var t corosMcpTokenResp
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	// refresh_token 可能不發：COROS 是 Spring Authorization Server，預設不發 refresh token 給 public
	// client。沒有就只用 access token，到期後請使用者重新連接（errCorosMcpReconnect），不能因此判連線失敗。
	if t.AccessToken == "" {
		return nil, fmt.Errorf("token response missing access_token")
	}
	return &t, nil
}

func (h *CorosMcpHandler) exchangeCode(ctx context.Context, tokenURL string, client *corosMcpClient, code, verifier string) (*corosMcpTokenResp, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {client.ClientID},
		"code":          {code},
		"redirect_uri":  {h.cfg.RedirectURI},
		"code_verifier": {verifier},
	}
	return h.postTokenForm(ctx, tokenURL, form, client)
}

// refreshToken 刷新並持久化（契約第 7 點：refresh 回傳的新 refresh token 一律存回）。
func (h *CorosMcpHandler) refreshToken(ctx context.Context, conn *corosMcpConnection) error {
	if conn.RefreshToken == "" {
		// COROS 沒發 refresh token：access token 到期就只能重新授權——寫入 reauth_required_at，/status 與日報據此顯示。
		h.markReauthRequired(ctx, conn.ID)
		return errCorosMcpReconnect
	}
	client, err := h.getClient(ctx, conn.Issuer)
	if err != nil || client == nil {
		return fmt.Errorf("coros mcp client not found for issuer %s", conn.Issuer)
	}
	disc, err := h.discover(ctx, conn.Issuer)
	if err != nil {
		return err
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {client.ClientID},
		"refresh_token": {conn.RefreshToken},
	}
	t, err := h.postTokenForm(ctx, disc.TokenEndpoint, form, client)
	if err != nil {
		var te *corosMcpTokenError
		if errors.As(err, &te) && te.Code == "invalid_grant" {
			h.markReauthRequired(ctx, conn.ID) // refresh token 已失效／被撤銷：需要使用者重新授權
			return fmt.Errorf("%w: %v", errCorosMcpReconnect, err)
		}
		return err
	}
	expiresAt := time.Now().Add(1 * time.Hour)
	if t.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	}
	// 沒輪替就沿用舊的 refresh token；有發新的一律存新的（契約第 7 點）。
	newRefresh := t.RefreshToken
	if newRefresh == "" {
		newRefresh = conn.RefreshToken
	}
	if err := h.updateTokens(ctx, conn.ID, t.AccessToken, newRefresh, expiresAt); err != nil {
		return err
	}
	conn.AccessToken, conn.RefreshToken, conn.ExpiresAt = t.AccessToken, newRefresh, expiresAt
	return nil
}

// ensureFreshToken 到期前 60 秒主動換新（契約第 7 點）。
func (h *CorosMcpHandler) ensureFreshToken(ctx context.Context, conn *corosMcpConnection) error {
	if time.Now().Add(corosMcpRefreshSkew).Before(conn.ExpiresAt) {
		return nil
	}
	return h.refreshToken(ctx, conn)
}

// corosMcpStatusExpiresAt /status 的 expires_at：只有「沒有可用的 refresh token」時才回 access token 到期時間
// （RFC3339），否則回 nil（JSON null）。有可用的 refresh token 時 access token 會在到期前自動換新，它約 30 天的
// 到期時間不是使用者需要處理的事——前台不該拿它顯示「即將到期」；是否需要使用者動作一律看 needs_reauth。
// 「可用」＝存有 refresh token 且尚未被標記 reauth_required_at（refresh 已失敗／被撤銷就不算可用）。
func corosMcpStatusExpiresAt(c *corosMcpConnection) any {
	if c.RefreshToken != "" && c.ReauthRequiredAt == nil {
		return nil
	}
	return c.ExpiresAt.Format(time.RFC3339)
}

// GET /status
// 不受入口狀態限制（GA 契約 §2.2）：只要登入就能查——有連線列的人隨時看得到狀態、也能中斷。回應多了：
//   - entry：目前入口對「這位使用者」的結果（"shown"|"hidden"），前台據此決定要不要顯示「連接」鈕；
//   - needs_reauth：是否需要使用者重新授權（前台顯示橫幅與「重新授權」鈕）——唯一權威旗標；
//   - expires_at：access token 到期時間，**只有沒有可用 refresh token 時才給**，否則 null（見 corosMcpStatusExpiresAt）；
//   - can_probe：是否顯示「讀取測試」鈕（只有超管，見 Probe）。
//
// 只讀不解密 token（getConnectionMeta）：金鑰遺失或密文損毀時 /status 與 /disconnect 仍要能用。
func (h *CorosMcpHandler) Status(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	u, err := h.loadUser(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	entry := DashboardEntry(r.Context(), h.repo.db, EntryProviderCoros, u.Email, u.Code, u.IsSuper)
	// can_probe：前台是否顯示「讀取測試」鈕——只有超管（且入口沒被緊急關閉）才是 true，與 POST /probe 的閘門一致；
	// 前台沒有其他超管訊號可用，所以由後端明講。
	canProbe := u.IsSuper && !h.entryEmergency(r.Context())
	conn, err := h.getConnectionMeta(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if conn == nil {
		respondJSON(w, http.StatusOK, map[string]any{
			"connected": false, "entry": entry, "issuer": nil, "connected_at": nil, "last_probe_at": nil, "last_probe": nil,
			"last_synced_at": nil, "device_name": nil, "expires_at": nil, "needs_reauth": false, "can_probe": canProbe,
		})
		return
	}
	resp := map[string]any{
		"connected": true, "entry": entry, "issuer": conn.Issuer, "connected_at": conn.ConnectedAt.Format(time.RFC3339),
		"last_probe_at": nil, "last_probe": nil,
		"last_synced_at": nil, "device_name": nil,
		"expires_at":   corosMcpStatusExpiresAt(conn),
		"needs_reauth": conn.NeedsReauth(h.timeNow()),
		"can_probe":    canProbe,
	}
	if conn.LastSyncedAt != nil {
		resp["last_synced_at"] = conn.LastSyncedAt.Format(time.RFC3339)
	}
	// device_name：該使用者最新一筆 MCP 匯入活動的型號（不即時打 COROS；尚無活動則為 null）。
	if dn := h.latestDeviceName(r.Context(), userID); dn != nil {
		resp["device_name"] = *dn
	}
	// 讀取測試摘要只給超管（GA 契約 §3.2：probe 是除錯工具，一般使用者看不到也沒有按鈕）。
	if u.IsSuper && conn.LastProbeAt != nil {
		resp["last_probe_at"] = conn.LastProbeAt.Format(time.RFC3339)
		if steps, err := h.loadProbeSummary(r.Context(), userID, *conn.LastProbeAt); err == nil && len(steps) > 0 {
			resp["last_probe"] = map[string]any{"at": conn.LastProbeAt.Format(time.RFC3339), "steps": steps}
		}
	}
	respondJSON(w, http.StatusOK, resp)
}

// loadProbeSummary 讀回某一批（created_at 完全相同，見 Probe 的 probeAt 注入寫法）probe_logs 的摘要。
func (h *CorosMcpHandler) loadProbeSummary(ctx context.Context, userID string, at time.Time) ([]corosMcpStepSummary, error) {
	rows, err := h.repo.db.Query(ctx, `SELECT response FROM coros_mcp_probe_logs WHERE user_id=$1 AND created_at=$2 ORDER BY tool`, userID, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []corosMcpStepSummary
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var pr probeLogResponse
		if err := json.Unmarshal(raw, &pr); err != nil {
			continue
		}
		out = append(out, pr.Summary)
	}
	return out, rows.Err()
}

// POST /probe（僅超管，GA 契約 §3.2）
// 讀取測試是除錯工具：一般使用者沒有按鈕、打這支也是 403；入口緊急關閉（hidden）時連超管也不行。
// 紀錄只存每一步的摘要（step／ok／count／錯誤代碼），不存任何 COROS 原始回應（tools/list 全文、活動明細、
// 逐公里分段…一律不落地）。
func (h *CorosMcpHandler) Probe(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	u, err := h.loadUser(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if !u.IsSuper || entrygate.Emergency(r.Context(), h.repo.db, corosMcpEntryStateKey) {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	conn, err := h.getConnection(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if conn == nil {
		respondJSON(w, http.StatusConflict, map[string]string{"error": "not_connected"})
		return
	}
	if allow, retryAfter := h.allowProbe(r.Context(), userID); !allow {
		respondJSON(w, http.StatusTooManyRequests, map[string]any{"error": "rate_limited", "retry_after_s": retryAfter})
		return
	}
	if err := h.ensureFreshToken(r.Context(), conn); err != nil {
		log.Error().Err(err).Str("user", userID).Msg("coros mcp probe: token refresh failed")
		if errors.Is(err, errCorosMcpReconnect) {
			respondJSON(w, http.StatusConflict, map[string]string{"error": "reconnect_required"})
			return
		}
		respondErr(w, http.StatusBadGateway, "token refresh failed")
		return
	}

	probeAt := time.Now()
	var steps []corosMcpStepSummary

	// Step 1: tools/list（只記工具數量）
	tools, err := h.toolsList(r.Context(), conn)
	listSummary := h.recordStep(r.Context(), userID, "tools/list", tools, err, probeAt)
	steps = append(steps, listSummary)

	// Step 2-5：queryDevices / querySportRecords / getActivityDetail / queryActivityLapData，
	// 依契約固定順序，前一步失敗不中止後面——每步獨立回報成功/失敗，供使用者看出「連得上多少」。
	var sportRecordsText string
	toolSchemas := corosMcpToolSchemaIndex(tools)
	for _, tool := range corosMcpProbeTools {
		args := map[string]any{} // 無參數工具也送 {}（不送 null）
		switch tool {
		case "querySportRecords":
			args = corosMcpSportRecordsArgs(toolSchemas["querySportRecords"], time.Now())
		case "getActivityDetail", "queryActivityLapData":
			labelID, sportType, ok := corosMcpExtractFirstActivity(sportRecordsText)
			if !ok {
				s := corosMcpStepSummary{Step: tool, OK: false, Error: "no_activity"}
				steps = append(steps, s)
				_ = h.logProbe(r.Context(), userID, s, probeAt)
				continue
			}
			// 2026-10-01 tools/list 實測：兩個工具都 required ["labelId","sportType"]（labelId 字串、sportType 整數）
			args = map[string]any{"labelId": labelID, "sportType": sportType}
		}
		result, callErr := h.toolsCall(r.Context(), conn, tool, args)
		text := corosMcpResultText(result)
		if callErr == nil && corosMcpIsAnomaly(text) {
			// COROS 對不合規格的呼叫回 isError=false＋一段「Tool call anomalies detected…」文字（給 AI 助理看的），
			// v869 第一次讀取測試把它誤判成 ✓。改判失敗，前台才看得出參數有問題。
			callErr = errCorosMcpAnomaly
		}
		if tool == "querySportRecords" && callErr == nil {
			sportRecordsText = text
		}
		summary := h.recordStep(r.Context(), userID, tool, result, callErr, probeAt)
		steps = append(steps, summary)
	}

	if err := h.touchLastProbe(r.Context(), userID, probeAt); err != nil {
		log.Warn().Err(err).Str("user", userID).Msg("coros mcp probe: touch last_probe_at failed")
	}
	respondJSON(w, http.StatusOK, map[string]any{"at": probeAt.Format(time.RFC3339), "steps": steps})
}

// recordStep 組單步摘要並寫 probe_logs。只留 step／ok／count／錯誤代碼（corosMcpErrorCode）——
// 不保存 COROS 回應原文，也不保存 COROS 的錯誤字串（GA 契約 §3.2）。
func (h *CorosMcpHandler) recordStep(ctx context.Context, userID, tool string, result any, callErr error, at time.Time) corosMcpStepSummary {
	summary := corosMcpStepSummary{Step: tool}
	if callErr != nil {
		summary.OK = false
		summary.Error = corosMcpErrorCode(callErr)
	} else {
		summary.OK = true
		if n, ok := corosMcpCountOf(tool, result); ok {
			summary.Count = &n
		}
	}
	_ = h.logProbe(ctx, userID, summary, at)
	return summary
}

// corosMcpUnwrapText：COROS 的 tools/call 結果 content[0].text 本身常是「JSON 字串字面值」（外層多一層引號，
// 2026-10-01 實測 queryDevices／querySportRecords 皆是）。能解成字串就回內文，否則原樣回傳。
func corosMcpUnwrapText(text string) string {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, `"`) {
		var s string
		if json.Unmarshal([]byte(t), &s) == nil {
			return s
		}
	}
	return text
}

// corosMcpResultText：取 tools/call 結果第一段文字並解開外層引號；nil／無內容回空字串。
func corosMcpResultText(r *mcpToolCallResult) string {
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	return corosMcpUnwrapText(r.Content[0].Text)
}

// corosMcpIsAnomaly：COROS 對不合規格的呼叫不回 isError，而是回一段給 AI 助理看的固定文字
// （"Tool call anomalies detected. High risk of session context pollution…"，2026-10-01 實測）。
func corosMcpIsAnomaly(text string) bool {
	return strings.Contains(strings.ToLower(text), "tool call anomalies detected")
}

var (
	corosMcpLabelIDRe   = regexp.MustCompile(`(?i)label\s*_?id["']?\s*[:：=]\s*["']?([0-9A-Za-z_-]{4,})`)
	corosMcpSportTypeRe = regexp.MustCompile(`(?i)sport\s*_?type["']?\s*[:：=]\s*["']?(\d{1,6})`)
	corosMcpHeaderNRe   = regexp.MustCompile(`\((\d{1,4})\)`)
	// 活動清單分段：空行，或換行後的「1. 」「2) 」編號
	corosMcpBlockSplitRe = regexp.MustCompile(`\n\s*\n|\n\s*\d{1,3}[.)]\s`)
)

// corosMcpCountOf 嘗試從回應推出筆數摘要：tools/list 用自己的 slice 長度；tools/call 依序嘗試
// JSON array 長度 → 文字裡 labelId 出現次數（活動清單）→ 第一行「標題 (N)」（例：Bound Devices (1)）。
func corosMcpCountOf(tool string, result any) (int, bool) {
	if tool == "tools/list" {
		if tools, ok := result.([]mcpTool); ok {
			return len(tools), true
		}
		return 0, false
	}
	r, ok := result.(*mcpToolCallResult)
	if !ok || r == nil || len(r.Content) == 0 {
		return 0, false
	}
	text := corosMcpResultText(r)
	if corosMcpIsAnomaly(text) {
		return 0, false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(text), &arr); err == nil {
		return len(arr), true
	}
	if n := len(corosMcpLabelIDRe.FindAllString(text, -1)); n > 0 {
		return n, true
	}
	firstLine := strings.SplitN(strings.TrimSpace(text), "\n", 2)[0]
	if m := corosMcpHeaderNRe.FindStringSubmatch(firstLine); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	return 0, false
}

// corosMcpToolSchemaIndex 把 tools/list 結果轉成 name→inputSchema 索引，供組 querySportRecords
// 參數時查「這個 schema 認得哪些日期欄位」。
func corosMcpToolSchemaIndex(tools []mcpTool) map[string]map[string]any {
	idx := map[string]map[string]any{}
	for _, t := range tools {
		idx[t.Name] = t.InputSchema
	}
	return idx
}

// corosMcpDORSportTypes：DOR 計入的 COROS 運動代碼（querySportRecords 說明的 dubbo 清單，2026-10-01 實測）：
// 100 戶外跑、101 室內跑、102 越野跑、103 田徑場跑、104 健行、900 走路——與 Terra／Strava 收「跑步全類＋走路／健行」一致。
var corosMcpDORSportTypes = []int{100, 101, 102, 103, 104, 900}

// corosMcpSportRecordsArgs 純函式：照 tools/list 的真實 schema 組 querySportRecords 參數（2026-10-01 實測）。
// schema 的 required 列了全部 10 個欄位（OpenAI strict 模式慣例：選填欄位也要出現、不用就給 null）；日期必須
// yyyyMMdd（v869 送 2006-01-02 格式＋只帶日期，被 COROS 以「Tool call anomalies detected」拒絕）。
// schema 拿不到時退回這份已知欄位清單。
func corosMcpSportRecordsArgs(schema map[string]any, now time.Time) map[string]any {
	keys := []string{"startDate", "endDate", "sportTypeCodes", "minDistanceKm", "maxDistanceKm",
		"minDurationMinutes", "maxDurationMinutes", "maxAveragePace", "locationKeyword", "limit"}
	if schema != nil {
		if req, ok := schema["required"].([]any); ok && len(req) > 0 {
			keys = keys[:0]
			for _, k := range req {
				if s, ok := k.(string); ok {
					keys = append(keys, s)
				}
			}
		}
	}
	args := map[string]any{}
	for _, k := range keys {
		args[k] = nil // 選填篩選：不用就明確給 null
	}
	set := func(k string, v any) {
		if _, ok := args[k]; ok || schema == nil {
			args[k] = v
		} else if props, _ := schema["properties"].(map[string]any); props != nil {
			if _, ok := props[k]; ok {
				args[k] = v
			}
		}
	}
	set("startDate", now.AddDate(0, 0, -14).Format("20060102"))
	set("endDate", now.Format("20060102"))
	set("sportTypeCodes", corosMcpDORSportTypes)
	set("limit", 10)
	return args
}

// corosMcpExtractFirstActivity 純函式：從 querySportRecords 回應取第一筆活動的 labelId＋sportType
// （getActivityDetail／queryActivityLapData 都 required 這兩個）。COROS 說明「Returns … labelId, sportType」
// 但回應可能是 JSON 也可能是人看的文字（queryDevices 就是文字），兩種都試：
//  1. JSON：遞迴找第一個同時有 labelId 與 sportType 的物件；
//  2. 文字：逐段（空行或編號分段）找同一段內的 labelId 與 sportType；都找不到就取全文第一個 labelId＋第一個 sportType。
func corosMcpExtractFirstActivity(text string) (labelID string, sportType int, ok bool) {
	text = corosMcpUnwrapText(text)
	if strings.TrimSpace(text) == "" || corosMcpIsAnomaly(text) {
		return "", 0, false
	}
	// UseNumber：labelId 若是超過 2^53 的長數字，用 float64 解會被四捨五入成錯的 id
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) == nil {
		if id, st, found := corosMcpFindActivityInJSON(v); found {
			return id, st, true
		}
	}
	blocks := corosMcpBlockSplitRe.Split(text, -1)
	for _, b := range blocks {
		m1 := corosMcpLabelIDRe.FindStringSubmatch(b)
		m2 := corosMcpSportTypeRe.FindStringSubmatch(b)
		if m1 != nil && m2 != nil {
			n, _ := strconv.Atoi(m2[1])
			return m1[1], n, true
		}
	}
	m1 := corosMcpLabelIDRe.FindStringSubmatch(text)
	m2 := corosMcpSportTypeRe.FindStringSubmatch(text)
	if m1 != nil && m2 != nil {
		n, _ := strconv.Atoi(m2[1])
		return m1[1], n, true
	}
	return "", 0, false
}

func corosMcpFindActivityInJSON(v any) (string, int, bool) {
	switch x := v.(type) {
	case map[string]any:
		idv, okID := x["labelId"]
		stv, okST := x["sportType"]
		if okID && okST {
			var id string
			switch t := idv.(type) {
			case string:
				id = t
			case json.Number:
				id = t.String() // 原樣保留全部位數
			}
			var st int64
			var stErr error = errors.New("not a number")
			switch t := stv.(type) {
			case json.Number:
				st, stErr = t.Int64()
			case string:
				st, stErr = strconv.ParseInt(t, 10, 64)
			}
			if id != "" && stErr == nil {
				return id, int(st), true
			}
		}
		for _, child := range x {
			if id, st, ok := corosMcpFindActivityInJSON(child); ok {
				return id, st, true
			}
		}
	case []any:
		for _, child := range x {
			if id, st, ok := corosMcpFindActivityInJSON(child); ok {
				return id, st, true
			}
		}
	}
	return "", 0, false
}

// POST /disconnect
// 不受入口狀態限制（GA 契約 §2.2／§3.3）：只要登入就能中斷——入口從 open 退回 whitelist（或被緊急關閉）後，
// 已連線的人仍然可以自己刪掉授權與已匯入的資料。
//
// 刪除內容（Repository.PurgeCorosMcpUser，單一交易）：source='coros'＋external_id 'mcp:%' 的活動、
// 連線列、這位使用者的 probe logs（Terra／Partner 的 coros 列不動）；之後重設偏好來源（偏好是 coros 且已沒有
// 其他 COROS 連線）與 GPS 距離校正（若其配對曾含這批 mcp 列）。已發放的 EXP／DP／里程不收回；賽事成績因為
// 是即時由活動加總，會跟著重算（前台確認視窗要寫明，並在完成後顯示 deleted_activities 筆數）。
func (h *CorosMcpHandler) Disconnect(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	// 盡力撤銷（public client 也試一次：COROS metadata 的 revocation 驗證方式不含 none，多半會被拒，
	// 但 RFC 7009 允許 public client 帶 client_id 撤銷，試了無害）；成敗都不擋本機刪除。撤銷需要明文 token——
	// 加密金鑰遺失或密文損毀（解密失敗）時略過撤銷，本機資料照刪（使用者一定要能中斷）。
	revoked := false
	if conn, err := h.getConnection(r.Context(), userID); err != nil {
		log.Warn().Err(err).Str("user", userID).Msg("coros mcp disconnect: cannot read tokens for revoke, deleting locally anyway")
	} else if conn != nil {
		if client, cerr := h.getClient(r.Context(), conn.Issuer); cerr == nil && client != nil {
			if disc, derr := h.discover(r.Context(), conn.Issuer); derr == nil && disc.RevocationEndpoint != "" {
				token, hint := conn.RefreshToken, "refresh_token"
				if token == "" {
					token, hint = conn.AccessToken, "access_token"
				}
				revoked = h.revoke(r.Context(), disc.RevocationEndpoint, client, token, hint)
			}
		}
	}
	// 單一交易：活動＋連線列＋probe logs 要嘛全刪要嘛都留（失敗回 500、使用者可再按一次）。
	// 即使目前沒有連線列（上次只刪到一半的舊版殘留）也照跑，確保資料一定清乾淨。
	deleted, hadCalib, err := h.repo.PurgeCorosMcpUser(r.Context(), userID)
	if err != nil {
		log.Error().Err(err).Str("user", userID).Msg("coros mcp disconnect: purge failed")
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	// 偏好來源是 'coros' 且已沒有任何 provider='coros'（Terra／Partner）連線 → 重設 'gps'（只 log、不擋中斷）。
	if other, oerr := h.repo.GetByUser(r.Context(), userID, providerCoros); oerr != nil {
		log.Warn().Err(oerr).Str("user", userID).Msg("coros mcp disconnect: check remaining coros connection failed")
	} else if other == nil {
		if rerr := h.repo.ResetPreferredSource(r.Context(), userID, "coros"); rerr != nil {
			log.Warn().Err(rerr).Str("user", userID).Msg("coros mcp disconnect: reset preferred source failed")
		}
	}
	// GPS 校正：配對是直接由 mcp 列算出來的（GA 前白名單期間的影子模式）→ 配對隨活動 CASCADE 消失，但係數還留著，
	// 重設回 1.0／warming（只向前生效）。GA 之後 candidateSQL 已排除 mcp 列，新使用者不會有這種係數。
	if hadCalib {
		if cerr := gpscalib.AdminReset(r.Context(), h.repo.db, userID, "coros_mcp_disconnect"); cerr != nil {
			log.Warn().Err(cerr).Str("user", userID).Msg("coros mcp disconnect: reset gps calibration failed")
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "revoked": revoked, "deleted_activities": deleted})
}

// revoke 盡力而為：失敗只記錄、不擋中斷本身（契約第 10 點）。refresh token 優先撤銷（撤銷它通常連帶
// 讓同一授權的 access token 失效）；只有真的有 secret 才送 Basic，public client 只帶 client_id。
func (h *CorosMcpHandler) revoke(ctx context.Context, endpoint string, client *corosMcpClient, token, hint string) bool {
	if token == "" {
		return false
	}
	form := url.Values{"token": {token}, "token_type_hint": {hint}, "client_id": {client.ClientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if corosMcpEffectiveAuthMethod(client.AuthMethod, client.ClientSecret) == "client_secret_basic" {
		req.SetBasicAuth(client.ClientID, client.ClientSecret)
	}
	resp, err := h.hc.Do(req)
	if err != nil {
		log.Warn().Err(err).Msg("coros mcp revoke failed")
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		log.Warn().Err(corosMcpParseOAuthError(resp.StatusCode, body)).Msg("coros mcp revoke not accepted (local tokens still deleted)")
		return false
	}
	return true
}
