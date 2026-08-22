package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/notify"
	"github.com/dor/api/internal/stamina"
)

// COROS 手錶直連 adapter：OAuth2 連接 + webhook 收活動，餵進既有的 NormalizedActivity 管線。
// 結構幾乎照抄 strava.go（OAuth2 流程/state 簽章/tokenForUser 刷新/Disconnect）與
// terra.go（NormalizedActivity 落地/ImportActivity→ChargeSP→AwardMileageExp 流程）。
// 規格依據：5 個獨立開源實作交叉驗證（無官方公開文件），信心度高但仍有若干未證實細節，
// 已在對應位置以 ⚠️ TODO(待官方文件確認) 標記，行為皆採「失敗不炸、保守 fallback」。
// 未設定 COROS_CLIENT_ID/SECRET → enabled()=false，webhook 只回 ack、不處理（安全可部署）。

const (
	corosBaseURL        = "https://open.coros.com" // 沙盒為 https://opentest.coros.com；先寫死正式站，用常數方便日後切換
	corosAuthURL        = corosBaseURL + "/oauth2/authorize"
	corosTokenURL       = corosBaseURL + "/oauth2/accesstoken"
	corosRefreshURL     = corosBaseURL + "/oauth2/refresh-token"
	corosDeauthorizeURL = corosBaseURL + "/oauth2/deauthorize"
	providerCoros       = "coros"
	corosScope          = "workout"
)

// corosRunningModes：COROS sportDataList 每筆的運動類型是 mode(int)*10+subMode(int) 組成的代碼。
// 只處理跑步：81=戶外跑、82=室內跑、151=越野跑、201=田徑場跑（來自交叉驗證的開源實作，非官方文件）。
var corosRunningModes = map[int]bool{
	81:  true, // 戶外跑
	82:  true, // 室內跑
	151: true, // 越野跑
	201: true, // 田徑場跑
}

// isCorosRunningActivity 純函式：依 mode/subMode 組出的代碼判斷是否為跑步類活動（供單元測試）。
func isCorosRunningActivity(mode, subMode int) bool {
	return corosRunningModes[mode*10+subMode]
}

// corosAvgPaceS 純函式：COROS webhook payload 沒有配速欄位，秒/公里需自己算（供單元測試）。
// distanceKm<=0 時回傳 0，避免除以零（呼叫端在此之前已擋 distance<=0，這裡防禦性保留）。
func corosAvgPaceS(durationS int, distanceKm float64) int {
	if distanceKm <= 0 {
		return 0
	}
	return int(math.Round(float64(durationS) / distanceKm))
}

// CorosConfig 從 config.Config 注入
type CorosConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	FrontendURL  string
	JWTSecret    string
}

type CorosHandler struct {
	repo        *Repository
	cfg         CorosConfig
	requireAuth func(http.Handler) http.Handler
	hc          *http.Client
	rdb         *redis.Client // webhook 基本節流用；nil 時 allowRate 一律放行（fail-open）
}

func NewCorosHandler(repo *Repository, cfg CorosConfig, requireAuth func(http.Handler) http.Handler, rdb *redis.Client) *CorosHandler {
	return &CorosHandler{repo: repo, cfg: cfg, requireAuth: requireAuth, hc: &http.Client{Timeout: 15 * time.Second}, rdb: rdb}
}

func (h *CorosHandler) enabled() bool { return h.cfg.ClientID != "" && h.cfg.ClientSecret != "" }

// allowRate 簡單固定窗口節流（Redis INCR + 首次 SET EXPIRE），照抄 strava.go 的 allowRate。
// webhook 驗證雖已用 client/secret header 擋掉大部分濫用，但仍是公開端點，保留一層基本防線。
func (h *CorosHandler) allowRate(ctx context.Context, key string, limit int, window time.Duration) bool {
	if h.rdb == nil {
		return true
	}
	n, err := h.rdb.Incr(ctx, key).Result()
	if err != nil {
		return true
	}
	if n == 1 {
		h.rdb.Expire(ctx, key, window)
	}
	return n <= int64(limit)
}

// Router 掛在 /api/v1/integrations/coros（自行處理需登入的子路由）
func (h *CorosHandler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/callback", h.Callback)  // COROS OAuth 導回（公開）
	r.Post("/webhook", h.Webhook)   // COROS webhook 事件（公開）
	r.Get("/health", h.HealthCheck) // COROS 要求的 Service Status URL（公開，須回 200）
	r.Group(func(r chi.Router) {
		r.Use(h.requireAuth)
		r.Get("/connect", h.Connect) // 取得授權 URL
		r.Get("/status", h.Status)   // 連線狀態
		r.Post("/disconnect", h.Disconnect)
	})
	return r
}

// GET /health → COROS 開發者後台要求填一個「回 200 的 Service Status URL」才能通過審核。
func (h *CorosHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- 需登入端點 ---

// GET /connect → { "url": "<coros authorize url>" }
func (h *CorosHandler) Connect(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	if !h.enabled() {
		respondErr(w, http.StatusServiceUnavailable, "COROS 整合尚未設定")
		return
	}
	ret := r.URL.Query().Get("return")
	if !strings.HasPrefix(ret, "https://") && !strings.HasPrefix(ret, "http://") {
		ret = h.cfg.FrontendURL
	}
	q := url.Values{}
	q.Set("client_id", h.cfg.ClientID)
	q.Set("redirect_uri", h.cfg.RedirectURI)
	q.Set("response_type", "code")
	q.Set("state", h.signState(userID, ret))
	respondJSON(w, http.StatusOK, map[string]string{"url": corosAuthURL + "?" + q.Encode()})
}

// GET /status → { enabled, connected }
func (h *CorosHandler) Status(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	conn, err := h.repo.GetByUser(r.Context(), userID, providerCoros)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"enabled": h.enabled(), "connected": conn != nil})
}

// POST /disconnect
func (h *CorosHandler) Disconnect(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if conn, err := h.repo.GetByUser(r.Context(), userID, providerCoros); err == nil && conn != nil {
		if access, err := h.tokenForUser(r.Context(), conn); err == nil && access != "" {
			h.deauthorize(r.Context(), access)
		}
	}
	if err := h.repo.Delete(r.Context(), userID, providerCoros); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if err := h.repo.DeleteProviderActivities(r.Context(), userID, providerCoros); err != nil {
		log.Error().Err(err).Str("user", userID).Msg("coros disconnect: delete imported activities failed")
		// 不因刪除失敗擋斷線本身，錯誤已記錄供事後補償清理（比照 strava.go Disconnect）。
	}
	w.WriteHeader(http.StatusNoContent)
}

// deauthorize 向 COROS 撤銷該存取權杖。
// ⚠️ TODO(待官方文件確認)：撤權端點 method 未證實，開源實作有用 GET 的，這裡先用 POST；
// 失敗只記錄、不擋本地中斷（本地連線/活動仍會刪除，使用者達成中斷目的）。
func (h *CorosHandler) deauthorize(ctx context.Context, accessToken string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, corosDeauthorizeURL+"?"+url.Values{"token": {accessToken}}.Encode(), nil)
	if err != nil {
		return
	}
	resp, err := h.hc.Do(req)
	if err != nil {
		log.Warn().Err(err).Msg("coros deauthorize failed")
		return
	}
	_ = resp.Body.Close()
}

// --- 公開端點 ---

// GET /callback?code=&state= → 交換 token、存檔、導回原頁面。
// 不 backfill：我方政策「只從連接當下起算」，COROS webhook 本就只推連接後的新活動，
// 也沒有需要主動拉取的歷史活動列表端點可用（未做歷史回填）。
func (h *CorosHandler) Callback(w http.ResponseWriter, r *http.Request) {
	userID, ret, ok := h.verifyState(r.URL.Query().Get("state"))
	if !ok {
		http.Redirect(w, r, appendQuery(h.cfg.FrontendURL, "coros", "invalid"), http.StatusFound)
		return
	}
	redirectFront := func(status string) {
		http.Redirect(w, r, appendQuery(ret, "coros", status), http.StatusFound)
	}
	if r.URL.Query().Get("error") != "" {
		redirectFront("denied")
		return
	}
	code := r.URL.Query().Get("code")
	tok, err := h.exchangeCode(r.Context(), code)
	if err != nil {
		log.Error().Err(err).Msg("coros token exchange failed")
		redirectFront("error")
		return
	}
	if tok.AccessToken == "" || tok.OpenID == "" {
		log.Error().Msg("coros token exchange: missing access_token/openId")
		redirectFront("error")
		return
	}
	expiresAt := time.Now().Add(30 * 24 * time.Hour) // 保守預設；正常情況下方 ExpiresIn>0 會覆蓋
	if tok.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	conn := &Connection{
		UserID: userID, Provider: providerCoros,
		ProviderUserID: tok.OpenID,
		AccessToken:    tok.AccessToken, RefreshToken: tok.RefreshToken,
		ExpiresAt: expiresAt,
		Scope:     corosScope,
	}
	if err := h.repo.Save(r.Context(), conn); err != nil {
		log.Error().Err(err).Msg("coros save connection failed")
		redirectFront("error")
		return
	}
	// 讀回帶 ConnectedAt 的連線（比照 strava.go Callback）。COROS 不 backfill，理論上用不到，
	// 但保持與 strava/terra 一致的落地保護（若日後改政策要 backfill，floor 已經備好）。
	if _, err := h.repo.GetByUser(r.Context(), userID, providerCoros); err != nil {
		log.Warn().Err(err).Msg("coros callback: re-read connection failed (non-fatal)")
	}
	redirectFront("connected")
}

// corosWebhookPayload COROS → 我方的活動推播 payload。
type corosWebhookPayload struct {
	SportDataList []corosSportData `json:"sportDataList"`
}

type corosSportData struct {
	OpenID            string  `json:"openId"`
	LabelID           string  `json:"labelId"`
	Mode              int     `json:"mode"`
	SubMode           int     `json:"subMode"`
	Distance          float64 `json:"distance"` // 公尺，可能缺
	Duration          int     `json:"duration"` // 秒，可能缺
	StartTime         int64   `json:"startTime"`
	EndTime           int64   `json:"endTime"`
	StartTimezone     int     `json:"startTimezone"`
	EndTimezone       int     `json:"endTimezone"`
	FitURL            string  `json:"fitUrl"`
	TriathlonItemList []any   `json:"triathlonItemList"` // TODO：先不處理鐵人三項分段，只取頂層跑步
}

// Webhook POST /webhook — COROS 活動事件。必須回 200 + {"message":"ok","result":"0000"}，
// 否則 COROS 會停用這個 webhook 訂閱；驗證失敗（header 不符）也一律回 ack，不處理、只 log，
// 避免對外洩漏「驗證失敗」與「處理失敗」的差異（比照 strava/terra webhook 的先 ack 後處理模式）。
func (h *CorosHandler) Webhook(w http.ResponseWriter, r *http.Request) {
	ack := func() { respondJSON(w, http.StatusOK, map[string]string{"message": "ok", "result": "0000"}) }

	if !h.enabled() {
		ack() // 未設定 client_id/secret：只 ack、不處理（安全可部署）
		return
	}
	if !h.verifyWebhookHeaders(r) {
		log.Warn().Msg("coros webhook: client/secret header 驗證失敗，拒絕處理")
		ack()
		return
	}
	if !h.allowRate(r.Context(), "coros:webhook", 600, time.Minute) {
		log.Warn().Msg("coros webhook rate limited")
		ack()
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		ack()
		return
	}
	var p corosWebhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		log.Warn().Err(err).Msg("coros webhook: bad json")
		ack()
		return
	}
	ack()
	if len(p.SportDataList) > 0 {
		go h.importCoros(p.SportDataList)
	}
}

// verifyWebhookHeaders：COROS webhook 不是 HMAC 簽章，而是明文 header `client`(=我方 client_id)
// 與 `secret`(=我方 client_secret)。用 constant-time 比對避免時序側錄，逐一比對兩個 header。
func (h *CorosHandler) verifyWebhookHeaders(r *http.Request) bool {
	clientOK := subtle.ConstantTimeCompare([]byte(r.Header.Get("client")), []byte(h.cfg.ClientID)) == 1
	secretOK := subtle.ConstantTimeCompare([]byte(r.Header.Get("secret")), []byte(h.cfg.ClientSecret)) == 1
	return clientOK && secretOK
}

// importCoros 背景處理一批活動（webhook 已先回 ack，這裡不再阻塞 HTTP 回應）。
// at-least-once 重送：靠 ImportActivity 的 UNIQUE(source,external_id) 天然冪等，不用額外去重。
func (h *CorosHandler) importCoros(items []corosSportData) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := range items {
		h.importOne(ctx, &items[i])
	}
}

// importOne 正規化單筆 COROS 活動並寫入（比照 terra.go importTerra 的落地流程）。
// 不解析 fitUrl（FIT 檔）：AscentM/AvgHR 這兩個選填欄位留空，只影響前端顯示，不影響里程/EXP 發放。
func (h *CorosHandler) importOne(ctx context.Context, a *corosSportData) ImportResult {
	if !isCorosRunningActivity(a.Mode, a.SubMode) {
		return ImportResult{Status: "skipped"}
	}
	if a.Distance <= 0 || a.Duration <= 0 {
		return ImportResult{Status: "skipped"}
	}
	conn, err := h.repo.GetByProviderUser(ctx, providerCoros, a.OpenID)
	if err != nil || conn == nil {
		return ImportResult{Status: "skipped"} // 查無對應連線（未連接/已中斷），略過
	}
	recordedAt := time.Unix(a.StartTime, 0)
	// 連接當下以前的資料一律不抓（比照 strava.go importOne 的 floor 保護；COROS webhook 本就只推
	// 連接後的新活動，這裡仍加此保護以防萬一）。
	if !conn.ConnectedAt.IsZero() && recordedAt.Before(conn.ConnectedAt) {
		return ImportResult{Status: "skipped"}
	}
	distanceKm := a.Distance / 1000.0
	na := &NormalizedActivity{
		UserID:      conn.UserID,
		Source:      providerCoros,
		ExternalID:  a.LabelID,
		Fingerprint: fingerprintOf(recordedAt.Unix(), a.Distance, a.Duration),
		DistanceKm:  distanceKm,
		DurationS:   a.Duration,
		AvgPaceS:    corosAvgPaceS(a.Duration, distanceKm), // COROS JSON 沒有配速欄位，自己算
		RecordedAt:  recordedAt,
		// AscentM/AvgHR 刻意留空：需解析 fitUrl 的 FIT 檔才拿得到，目前不取。
	}
	res, err := h.repo.ImportActivity(ctx, na)
	if err != nil {
		log.Error().Err(err).Msg("coros import activity failed")
		notify.Alert("coros_webhook_err", "COROS Webhook 處理錯誤", fmt.Sprintf("import activity failed: %v", err))
		return ImportResult{Status: "error"}
	}
	// stamina.ChargeSP 維持「僅新匯入」才扣血：SP 是扣血動作，同一趟不能被扣兩次（比照 strava/terra）。
	if res.Status == "inserted" && na.DistanceKm > 0 {
		stamina.ChargeSP(ctx, h.repo.db, na.UserID, na.DistanceKm, na.AvgPaceS)
	}
	// AwardMileageExp 呼叫條件比照 strava.go/terra.go：新匯入，或同帳號跨裝置的良性重複
	// （multi_device_duplicate，會走差額補償流程）；其他 duplicate 原因交給函式內部的 flagged 政策擋。
	if (res.Status == "inserted" || (res.Status == "duplicate" && res.Reason == "multi_device_duplicate")) && na.DistanceKm > 0 {
		if err := h.repo.AwardMileageExp(ctx, res.ID, na.UserID); err != nil {
			log.Error().Err(err).Str("activity", res.ID).Msg("coros award mileage exp failed")
			notify.Alert("coros_webhook_err", "COROS Webhook 處理錯誤", fmt.Sprintf("activity=%s award mileage exp failed: %v", res.ID, err))
		}
	}
	return res
}

// --- COROS OAuth2 token 端點 ---

// corosHTTPError 包裝 COROS OAuth 端點回傳的非 2xx 狀態碼（比照 strava.go stravaHTTPError，
// 各自獨立型別避免跨 provider 錯誤語意混淆）。
type corosHTTPError struct{ Status int }

func (e *corosHTTPError) Error() string { return fmt.Sprintf("coros http %d", e.Status) }

// corosTokenResp 涵蓋 accesstoken 交換與 refresh-token 兩種回應（欄位皆選填以容忍兩者 schema 差異）。
type corosTokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	OpenID       string `json:"openId"`
	ExpiresIn    int64  `json:"expires_in"` // 秒
	Result       string `json:"result"`     // refresh 端點觀察到的回應可能只有 result/message，無新 token
	Message      string `json:"message"`
}

func (h *CorosHandler) exchangeCode(ctx context.Context, code string) (*corosTokenResp, error) {
	return h.postToken(ctx, corosTokenURL, url.Values{
		"client_id":     {h.cfg.ClientID},
		"client_secret": {h.cfg.ClientSecret},
		"redirect_uri":  {h.cfg.RedirectURI},
		"code":          {code},
		"grant_type":    {"authorization_code"},
	})
}

func (h *CorosHandler) postToken(ctx context.Context, tokenURL string, form url.Values) (*corosTokenResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &corosHTTPError{Status: resp.StatusCode}
	}
	var t corosTokenResp
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// tokenForUser 回傳有效的 access token；COROS access token 效期約 30 天，剩 <10 天才刷新
// （比照 strava.go tokenForUser 的過期判斷模式，門檻依 COROS 較長效期調整）。
//
// ⚠️ TODO(待 COROS 官方文件確認 refresh 回應 schema)：/oauth2/refresh-token 是否回傳新
// access_token 未證實——已知有實作呼叫後只拿到 {"result":"0000","message":"OK"}、沒有新 token。
// 這裡採保守 fallback：解析不到新 access_token 就沿用舊 token、只延長本地 expiry 25 天
// （不報錯、不擋使用者），避免因回應 schema 誤判而錯誤登出使用者的整合。
func (h *CorosHandler) tokenForUser(ctx context.Context, conn *Connection) (string, error) {
	if time.Now().Before(conn.ExpiresAt.Add(-10 * 24 * time.Hour)) {
		return conn.AccessToken, nil
	}
	t, err := h.postToken(ctx, corosRefreshURL, url.Values{
		"client_id":     {h.cfg.ClientID},
		"client_secret": {h.cfg.ClientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {conn.RefreshToken},
	})
	if err != nil {
		return "", err
	}
	access, refresh, expiresAt := conn.AccessToken, conn.RefreshToken, conn.ExpiresAt
	if t.AccessToken != "" {
		access = t.AccessToken
		if t.RefreshToken != "" {
			refresh = t.RefreshToken
		}
		if t.ExpiresIn > 0 {
			expiresAt = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
		} else {
			expiresAt = time.Now().Add(25 * 24 * time.Hour)
		}
	} else {
		// 回應沒有新 access_token（見上方 TODO）：沿用舊 token，只保守延長本地 expiry，
		// 避免下次呼叫又立刻觸發 refresh（COROS 端可能已經在內部續期，只是不回傳給我們）。
		expiresAt = time.Now().Add(25 * 24 * time.Hour)
	}
	_ = h.repo.UpdateTokens(ctx, conn.ID, access, refresh, expiresAt)
	return access, nil
}

// --- state 簽章（callback 無登入，用 HMAC 綁定發起者）---
// 與 strava.go signState/verifyState 完全相同的機制，各自獨立實作避免跨檔耦合。

func (h *CorosHandler) signState(userID, returnURL string) string {
	msg := strings.Join([]string{userID, strconv.FormatInt(time.Now().Add(15*time.Minute).Unix(), 10), returnURL}, "\n")
	return base64.RawURLEncoding.EncodeToString([]byte(msg)) + "." + h.mac(msg)
}

func (h *CorosHandler) verifyState(state string) (userID, returnURL string, ok bool) {
	i := strings.LastIndex(state, ".")
	if i < 0 {
		return "", "", false
	}
	raw, sig := state[:i], state[i+1:]
	msgBytes, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", "", false
	}
	msg := string(msgBytes)
	if !hmac.Equal([]byte(sig), []byte(h.mac(msg))) {
		return "", "", false
	}
	parts := strings.Split(msg, "\n")
	if len(parts) != 3 {
		return "", "", false
	}
	exp, _ := strconv.ParseInt(parts[1], 10, 64)
	if time.Now().Unix() > exp {
		return "", "", false
	}
	return parts[0], parts[2], true
}

func (h *CorosHandler) mac(msg string) string {
	m := hmac.New(sha256.New, []byte(h.cfg.JWTSecret))
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
