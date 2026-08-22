package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

const (
	stravaAuthURL  = "https://www.strava.com/oauth/authorize"
	stravaTokenURL = "https://www.strava.com/oauth/token"
	stravaAPIBase  = "https://www.strava.com/api/v3"
	providerStrava = "strava"
	stravaScope    = "read,activity:read_all"
)

// StravaConfig 從 config.Config 注入
type StravaConfig struct {
	ClientID           string
	ClientSecret       string
	RedirectURI        string
	WebhookVerifyToken string
	FrontendURL        string
	JWTSecret          string
}

type StravaHandler struct {
	repo        *Repository
	cfg         StravaConfig
	requireAuth func(http.Handler) http.Handler
	hc          *http.Client
	rdb         *redis.Client // 節流用；nil 時 allowRate 一律放行（fail-open，不因 Redis 未注入而擋正常流量）
}

func NewStravaHandler(repo *Repository, cfg StravaConfig, requireAuth func(http.Handler) http.Handler, rdb *redis.Client) *StravaHandler {
	return &StravaHandler{repo: repo, cfg: cfg, requireAuth: requireAuth, hc: &http.Client{Timeout: 15 * time.Second}, rdb: rdb}
}

// allowRate 簡單固定窗口節流（Redis INCR + 首次 SET EXPIRE）。
// SEC-H4：公開 webhook 與 /sync 皆可能被重複打來耗盡全站共用 Strava API 額度，先擋在這裡。
// Redis 不可用或出錯 → fail-open（放行），避免節流機制本身變成單點故障擋掉正常同步。
func (h *StravaHandler) allowRate(ctx context.Context, key string, limit int, window time.Duration) bool {
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

func (h *StravaHandler) enabled() bool { return h.cfg.ClientID != "" && h.cfg.ClientSecret != "" }

// Router 掛在 /api/v1/integrations/strava（自行處理需登入的子路由）
func (h *StravaHandler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/callback", h.Callback)     // Strava OAuth 導回（公開）
	r.Get("/webhook", h.WebhookVerify) // Strava webhook 驗證（公開）
	r.Post("/webhook", h.WebhookEvent) // Strava webhook 事件（公開）
	r.Group(func(r chi.Router) {
		r.Use(h.requireAuth)
		r.Get("/connect", h.Connect) // 取得授權 URL
		r.Get("/status", h.Status)   // 連線狀態
		r.Delete("/disconnect", h.Disconnect)
		r.Post("/sync", h.Sync)            // 手動匯入近期活動
		r.Get("/activities", h.Activities) // 已同步活動清單
	})
	return r
}

// --- 需登入端點 ---

// GET /connect → { "url": "<strava authorize url>" }
func (h *StravaHandler) Connect(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	if !h.enabled() {
		respondErr(w, http.StatusServiceUnavailable, "Strava 整合尚未設定")
		return
	}
	// 回程網址：導回使用者原本所在頁面（同源→不會被登出）。前端傳 return；缺省用 FrontendURL。
	ret := r.URL.Query().Get("return")
	if !strings.HasPrefix(ret, "https://") && !strings.HasPrefix(ret, "http://") {
		ret = h.cfg.FrontendURL
	}
	q := url.Values{}
	q.Set("client_id", h.cfg.ClientID)
	q.Set("redirect_uri", h.cfg.RedirectURI)
	q.Set("response_type", "code")
	q.Set("approval_prompt", "force") // 每次都顯示授權頁，才能更換連結帳號
	q.Set("scope", stravaScope)
	q.Set("state", h.signState(userID, ret))
	respondJSON(w, http.StatusOK, map[string]string{"url": stravaAuthURL + "?" + q.Encode()})
}

// GET /status → { connected, athlete_name }
func (h *StravaHandler) Status(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	conn, err := h.repo.GetByUser(r.Context(), userID, providerStrava)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if conn == nil {
		respondJSON(w, http.StatusOK, map[string]any{"connected": false, "enabled": h.enabled()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"connected": true, "enabled": h.enabled(), "athlete_name": conn.AthleteName})
}

// POST /sync — 手動匯入近期活動
func (h *StravaHandler) Sync(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if !h.allowRate(r.Context(), "strava:sync:"+userID, 5, 5*time.Minute) {
		respondErr(w, http.StatusTooManyRequests, "同步太頻繁，請稍後再試")
		return
	}
	conn, err := h.repo.GetByUser(r.Context(), userID, providerStrava)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if conn == nil {
		respondErr(w, http.StatusBadRequest, "尚未連接 Strava")
		return
	}
	res, err := h.syncRecent(r.Context(), conn)
	if err != nil {
		respondErr(w, http.StatusBadGateway, "向 Strava 取得活動失敗")
		return
	}
	respondJSON(w, http.StatusOK, res)
}

// GET /activities — 已同步活動清單
func (h *StravaHandler) Activities(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	acts, err := h.repo.ListActivities(r.Context(), userID, 30)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"activities": acts})
}

// DELETE /disconnect
func (h *StravaHandler) Disconnect(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	// Strava API 條款：中斷連接時須向 Strava「撤銷授權」(deauthorize)，而非只刪本地 token。
	if conn, err := h.repo.GetByUser(r.Context(), userID, providerStrava); err == nil && conn != nil {
		if access, err := h.tokenForUser(r.Context(), conn); err == nil && access != "" {
			h.deauthorize(r.Context(), access)
		}
	}
	if err := h.repo.Delete(r.Context(), userID, providerStrava); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	// Strava API 條款 30 天刪除義務：中斷連接後刪除已匯入的活動（已發放的 EXP/DP/total_km 不追回，使用者拍板）。
	if err := h.repo.DeleteProviderActivities(r.Context(), userID, providerStrava); err != nil {
		log.Error().Err(err).Str("user", userID).Msg("strava disconnect: delete imported activities failed")
		// 不因刪除失敗擋斷線本身（連線已刪、使用者已達成中斷目的）；錯誤已記錄供事後補償清理。
	}
	w.WriteHeader(http.StatusNoContent)
}

// deauthorize 向 Strava 撤銷該存取權杖（POST /oauth/deauthorize）。失敗只記錄、不擋本地中斷。
func (h *StravaHandler) deauthorize(ctx context.Context, accessToken string) {
	form := url.Values{"access_token": {accessToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.strava.com/oauth/deauthorize", strings.NewReader(form.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.hc.Do(req)
	if err != nil {
		log.Warn().Err(err).Msg("strava deauthorize failed")
		return
	}
	_ = resp.Body.Close()
}

// --- 公開端點 ---

// GET /callback?code=&state= → 交換 token、存檔、回填近期活動、導回原頁面
func (h *StravaHandler) Callback(w http.ResponseWriter, r *http.Request) {
	userID, ret, ok := h.verifyState(r.URL.Query().Get("state"))
	if !ok {
		http.Redirect(w, r, appendQuery(h.cfg.FrontendURL, "strava", "invalid"), http.StatusFound)
		return
	}
	redirectFront := func(status string) {
		http.Redirect(w, r, appendQuery(ret, "strava", status), http.StatusFound)
	}
	if r.URL.Query().Get("error") != "" {
		redirectFront("denied")
		return
	}
	code := r.URL.Query().Get("code")
	tok, err := h.exchangeCode(r.Context(), code)
	if err != nil {
		log.Error().Err(err).Msg("strava token exchange failed")
		redirectFront("error")
		return
	}
	conn := &Connection{
		UserID: userID, Provider: providerStrava,
		ProviderUserID: strconv.FormatInt(tok.Athlete.ID, 10),
		AccessToken:    tok.AccessToken, RefreshToken: tok.RefreshToken,
		ExpiresAt:   time.Unix(tok.ExpiresAt, 0),
		Scope:       stravaScope,
		AthleteName: strings.TrimSpace(tok.Athlete.Firstname + " " + tok.Athlete.Lastname),
	}
	if err := h.repo.Save(r.Context(), conn); err != nil {
		log.Error().Err(err).Msg("strava save connection failed")
		redirectFront("error")
		return
	}
	// ⚠️ Callback 建的 conn 沒有 ConnectedAt（floor 為零值 → 會關掉「只抓連接後」的防護、倒灌整段歷史里程）。
	// Save 已把 created_at 寫成 NOW()，這裡重新讀回帶著 ConnectedAt 的連線再交給 backfill 當 floor。
	saved, err := h.repo.GetByUser(r.Context(), userID, providerStrava)
	if err != nil || saved == nil {
		conn.ConnectedAt = time.Now() // 讀回失敗的保底：仍以現在為 floor，寧可略嚴也不倒灌歷史
		saved = conn
	}
	// 背景回填「連接後」的活動（避免阻塞導回）
	go h.backfill(saved)
	redirectFront("connected")
}

// GET /webhook?hub.mode=subscribe&hub.challenge=&hub.verify_token=
func (h *StravaHandler) WebhookVerify(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("hub.mode") == "subscribe" && q.Get("hub.verify_token") == h.cfg.WebhookVerifyToken {
		respondJSON(w, http.StatusOK, map[string]string{"hub.challenge": q.Get("hub.challenge")})
		return
	}
	respondErr(w, http.StatusForbidden, "verify failed")
}

// POST /webhook — 活動事件 + 授權事件（須 2 秒內回 200，處理放背景）。
// updates 為 Strava webhook 事件通用的 map[string]string 欄位（依 event_type 不同內容不同）；
// 撤權事件（object_type=athlete, aspect_type=update）帶 updates={"authorized":"false"}
// （見 https://developers.strava.com/docs/webhooks/ 的 Example athlete deauthorization event）。
func (h *StravaHandler) WebhookEvent(w http.ResponseWriter, r *http.Request) {
	var ev struct {
		ObjectType string            `json:"object_type"`
		AspectType string            `json:"aspect_type"`
		ObjectID   int64             `json:"object_id"`
		OwnerID    int64             `json:"owner_id"`
		Updates    map[string]string `json:"updates"`
	}
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		w.WriteHeader(http.StatusOK) // 仍回 200，避免 Strava 重試風暴
		return
	}
	w.WriteHeader(http.StatusOK)
	if ev.ObjectType == "activity" && (ev.AspectType == "create" || ev.AspectType == "update") {
		go h.handleActivityEvent(ev.OwnerID, ev.ObjectID)
	}
	if ev.ObjectType == "athlete" && ev.Updates["authorized"] == "false" {
		go h.handleDeauthorizeEvent(ev.OwnerID)
	}
}

// handleDeauthorizeEvent 使用者「直接在 Strava 端」撤銷本站授權（未先來本站按「中斷」）。
// 與 Disconnect（使用者主動在本站中斷）對稱：都須刪本地連線 + 已匯入活動，滿足 Strava 30 天刪除義務。
// 不呼叫 h.deauthorize（該 API 是本站主動撤銷，使用者已經在 Strava 端撤銷過了，再打一次沒有意義）。
//
// ⚠️ SEC（對抗式審查 CRITICAL-1）：/webhook 是無簽章公開端點，任何人都能偽造這個事件、帶受害者的
// owner_id，若不驗證就直接刪，等同讓任何人都能清空任意使用者的 Strava 連線＋活動。比照隔壁
// handleActivityEvent 的「confirm-via-API」防線（見該函式上方 SEC-H4 註解），刪除前一律先用該連線
// 自己的 token 向 Strava 實測是否真的已撤權（見 verifyStravaDeauthorized）；另加 per-owner 節流，
// 讓這條破壞性路徑的防線不比讀取路徑弱。
//
// (provider, provider_user_id) 沒有唯一約束（見 migrations/014_integrations.sql）：同一個 Strava
// 帳號理論上可能被多個 DOR 帳號各自連接，撤權事件對應的是整個 Strava 帳號，故用 ListByProviderUser
// 找出「全部」符合的連線逐一處理，而非只處理第一筆。
func (h *StravaHandler) handleDeauthorizeEvent(ownerID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ownerIDStr := strconv.FormatInt(ownerID, 10)
	if !h.allowRate(ctx, "strava:webhook:deauth:"+ownerIDStr, 20, 5*time.Minute) {
		log.Warn().Str("owner", ownerIDStr).Msg("strava deauth webhook event rate limited")
		return
	}
	conns, err := h.repo.ListByProviderUser(ctx, providerStrava, ownerIDStr)
	if err != nil || len(conns) == 0 {
		return
	}
	for _, conn := range conns {
		if !h.verifyStravaDeauthorized(ctx, conn) {
			continue // 未確認撤權（可能是偽造事件、也可能只是暫時無法確認）→ 這條連線不動
		}
		if err := h.repo.Delete(ctx, conn.UserID, providerStrava); err != nil {
			log.Error().Err(err).Str("user", conn.UserID).Msg("strava deauth webhook: delete connection failed")
			notify.Alert("strava_webhook_err", "Strava Webhook 處理錯誤", fmt.Sprintf("delete connection failed: user_id=%s error=%v", conn.UserID, err))
			continue
		}
		if err := h.repo.DeleteProviderActivities(ctx, conn.UserID, providerStrava); err != nil {
			log.Error().Err(err).Str("user", conn.UserID).Msg("strava deauth webhook: delete imported activities failed")
			notify.Alert("strava_webhook_err", "Strava Webhook 處理錯誤", fmt.Sprintf("delete imported activities failed: user_id=%s error=%v", conn.UserID, err))
		}
	}
}

// verifyStravaDeauthorized 向 Strava 實際確認某連線是否「真的」已被撤權，而不是單憑 webhook 事件的
// 宣稱（見 handleDeauthorizeEvent 上方 SEC 註解）。
//
//   - token 刷新失敗、或 GET /athlete 遭拒（Strava 明確表示 token 已失效）→ 確認撤權，可以刪除。
//   - GET /athlete 回 200（token 仍然有效）→ 這個「撤權事件」是偽造的，不刪除。
//   - 其他錯誤（網路逾時／5xx／解碼失敗，狀態不確定）→ 保守不刪除：破壞性操作寧可暫時漏刪，也不可
//     誤刪無辜使用者的資料。真撤權的話 Strava 之後仍會重送 webhook，使用者自己也可能在本站按「中斷」。
func (h *StravaHandler) verifyStravaDeauthorized(ctx context.Context, conn *Connection) bool {
	access, err := h.tokenForUser(ctx, conn)
	if err != nil {
		// token 刷新（/oauth/token, grant_type=refresh_token）失敗：Strava 官方文件僅明確保證資源型
		// API（如 GET /athlete）對失效 token 回 401；OAuth token 端點對失效 refresh_token 未見官方
		// 明文狀態碼，但依 OAuth2 標準（RFC 6749 invalid_grant）與 Strava revoke 端點本身以 400
		// 表示 token 參數問題的慣例，400 在這個端點上實務上等同「token 已失效」，故與 401/403 一併
		// 視為確認信號（防禦性涵蓋）。
		if isStravaTokenInvalidGrant(err) {
			return true
		}
		log.Warn().Err(err).Str("user", conn.UserID).Msg("strava deauth verify: token refresh inconclusive, not deleting")
		return false
	}
	var athlete struct {
		ID int64 `json:"id"`
	}
	if err := h.getJSON(ctx, access, "/athlete", &athlete); err != nil {
		// 依 Strava 官方文件：「All requests made using invalidated tokens will receive a 401
		// Unauthorized response」——401/403 即確認撤權。
		if isStravaUnauthorized(err) {
			return true
		}
		log.Warn().Err(err).Str("user", conn.UserID).Msg("strava deauth verify: /athlete check inconclusive, not deleting")
		return false
	}
	log.Warn().Str("user", conn.UserID).Msg("strava deauth webhook: token still valid, event looks forged — refusing to delete")
	return false
}

// SEC-H4：/webhook 是公開端點，POST 完全不驗來源（Strava webhook 不支援 payload 簽章）——
// 任何人都能偽造事件 POST 任意 owner_id/object_id。防線分三層：
//  1. per-owner 節流：同一 owner_id 短時間灌大量事件視為異常，先擋住、避免耗盡全站共用 Strava API 額度。
//  2. 核對事件宣稱的 owner_id 確實對應到一條真實連線（GetByProviderUser 已用該值查詢，這裡再顯式核對一次防呆）。
//  3. 抓回活動後再核對「活動實際擁有者」＝該連線的 athlete id——防止攻擊者用自己合法連線的 token，
//     偽造事件指向他人的（公開）活動 object_id，把不屬於自己的里程灌進自己帳號刷 EXP/DP。
func (h *StravaHandler) handleActivityEvent(ownerID, activityID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ownerIDStr := strconv.FormatInt(ownerID, 10)
	if !h.allowRate(ctx, "strava:webhook:"+ownerIDStr, 20, 5*time.Minute) {
		log.Warn().Str("owner", ownerIDStr).Msg("strava webhook event rate limited")
		return
	}

	conn, err := h.repo.GetByProviderUser(ctx, providerStrava, ownerIDStr)
	if err != nil || conn == nil {
		return
	}
	if conn.ProviderUserID != ownerIDStr {
		// 理論上 GetByProviderUser 已用 ownerIDStr 查詢不會發生，防呆保留、不匯入。
		return
	}
	access, err := h.tokenForUser(ctx, conn)
	if err != nil {
		log.Error().Err(err).Msg("strava token refresh failed")
		notify.Alert("strava_webhook_err", "Strava Webhook 處理錯誤", fmt.Sprintf("token refresh failed: %v", err))
		return
	}
	act, err := h.getActivity(ctx, access, activityID)
	if err != nil {
		log.Error().Err(err).Int64("activity", activityID).Msg("strava get activity failed")
		notify.Alert("strava_webhook_err", "Strava Webhook 處理錯誤", fmt.Sprintf("activity_id=%d error=%v", activityID, err))
		return
	}
	if strconv.FormatInt(act.Athlete.ID, 10) != conn.ProviderUserID {
		log.Warn().Str("owner", ownerIDStr).Int64("activity", activityID).Int64("actual_athlete", act.Athlete.ID).
			Msg("strava activity athlete mismatch, refusing import")
		return
	}
	h.importOne(ctx, conn.UserID, conn.ConnectedAt, act)
}

// --- Strava API ---

type stravaToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	Athlete      struct {
		ID        int64  `json:"id"`
		Firstname string `json:"firstname"`
		Lastname  string `json:"lastname"`
	} `json:"athlete"`
}

func (h *StravaHandler) exchangeCode(ctx context.Context, code string) (*stravaToken, error) {
	return h.postToken(ctx, url.Values{
		"client_id":     {h.cfg.ClientID},
		"client_secret": {h.cfg.ClientSecret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
	})
}

// errRateLimited 標記 Strava 回 429（Too Many Requests）。呼叫端（syncRecent/backfill）看到此錯誤
// 應立即中止本輪剩餘處理，不要繼續打——Strava 限流窗口（15 分鐘 + 每日）過後，使用者下次手動
// sync 或下次 webhook 事件自然會再試。不做排程重試／退避佇列：目前呼叫量小，過度設計反而增加
// 複雜度，之後真的常撞限流再考慮。
var errRateLimited = errors.New("strava rate limited (429)")

func (h *StravaHandler) postToken(ctx context.Context, form url.Values) (*stravaToken, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, stravaTokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		log.Warn().Str("retry_after", resp.Header.Get("Retry-After")).Msg("strava token endpoint rate limited (429)")
		return nil, errRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &stravaHTTPError{Status: resp.StatusCode}
	}
	var t stravaToken
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// tokenForUser 回傳有效的 access token（過期前 60s 自動刷新並更新 DB）
func (h *StravaHandler) tokenForUser(ctx context.Context, conn *Connection) (string, error) {
	if time.Now().Before(conn.ExpiresAt.Add(-60 * time.Second)) {
		return conn.AccessToken, nil
	}
	t, err := h.postToken(ctx, url.Values{
		"client_id":     {h.cfg.ClientID},
		"client_secret": {h.cfg.ClientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {conn.RefreshToken},
	})
	if err != nil {
		return "", err
	}
	_ = h.repo.UpdateTokens(ctx, conn.ID, t.AccessToken, t.RefreshToken, time.Unix(t.ExpiresAt, 0))
	return t.AccessToken, nil
}

type stravaActivity struct {
	ID      int64 `json:"id"`
	Athlete struct {
		ID int64 `json:"id"` // SEC-H4：用於核對抓回的活動確實屬於連線中的 athlete，防「以自己 token 抓他人公開活動」
	} `json:"athlete"`
	Distance           float64 `json:"distance"`             // 公尺
	MovingTime         int     `json:"moving_time"`          // 秒
	TotalElevationGain float64 `json:"total_elevation_gain"` // 公尺
	AverageSpeed       float64 `json:"average_speed"`        // m/s
	AverageHeartrate   float64 `json:"average_heartrate"`
	HasHeartrate       bool    `json:"has_heartrate"`
	Type               string  `json:"type"`
	SportType          string  `json:"sport_type"`
	StartDate          string  `json:"start_date"` // RFC3339 UTC
}

// stravaHTTPError 包裝 Strava API/OAuth 端點回傳的非 2xx 狀態碼，讓呼叫端（如撤權驗證
// verifyStravaDeauthorized）能用 errors.As 取出確切狀態碼判斷語意，而不必字串比對錯誤訊息。
type stravaHTTPError struct{ Status int }

func (e *stravaHTTPError) Error() string { return fmt.Sprintf("strava http %d", e.Status) }

// isStravaUnauthorized 判斷錯誤是否代表 Strava 端回報 401/403（token 已失效/無權限）。
func isStravaUnauthorized(err error) bool {
	var he *stravaHTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusUnauthorized || he.Status == http.StatusForbidden
	}
	return false
}

// isStravaTokenInvalidGrant 判斷「/oauth/token 刷新」失敗是否代表 refresh_token 已失效
// （見 verifyStravaDeauthorized 呼叫處註解：400 與 401/403 皆視為確認信號）。
func isStravaTokenInvalidGrant(err error) bool {
	var he *stravaHTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusBadRequest || he.Status == http.StatusUnauthorized || he.Status == http.StatusForbidden
	}
	return false
}

func (h *StravaHandler) getJSON(ctx context.Context, access, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, stravaAPIBase+path, nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := h.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		log.Warn().Str("retry_after", resp.Header.Get("Retry-After")).Str("path", path).Msg("strava api rate limited (429)")
		return errRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		return &stravaHTTPError{Status: resp.StatusCode}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (h *StravaHandler) getActivity(ctx context.Context, access string, id int64) (*stravaActivity, error) {
	var a stravaActivity
	if err := h.getJSON(ctx, access, "/activities/"+strconv.FormatInt(id, 10), &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// SyncResult 同步統計
type SyncResult struct {
	Imported   int `json:"imported"`
	Duplicates int `json:"duplicates"`
	Existing   int `json:"existing"`
	Total      int `json:"total"`
}

// syncRecent 拉「連接時間之後」的活動並匯入，回傳統計。
// 用 Strava 的 after（epoch 秒）=連接時間(conn.ConnectedAt) 過濾；per_page=100（未來量大再分頁）。
func (h *StravaHandler) syncRecent(ctx context.Context, conn *Connection) (SyncResult, error) {
	var res SyncResult
	access, err := h.tokenForUser(ctx, conn)
	if err != nil {
		return res, err
	}
	// 只抓「連接當下之後」的活動（floor=連接時間，見 Connection.ConnectedAt）：一連接不倒灌整段歷史里程
	after := conn.ConnectedAt.Unix()
	var acts []stravaActivity
	// 429（errRateLimited）與其他錯誤同樣直接 return：整批列表抓取失敗，不進入下方逐筆匯入迴圈，
	// 即「中止本輪剩餘項目、不要繼續打」——這裡列表一次拉齊、匯入不再打 Strava API，故不需要在
	// for 迴圈中額外判斷。
	if err := h.getJSON(ctx, access, fmt.Sprintf("/athlete/activities?after=%d&per_page=100", after), &acts); err != nil {
		return res, err
	}
	for i := range acts {
		r := h.importOne(ctx, conn.UserID, conn.ConnectedAt, &acts[i])
		switch r.Status {
		case "inserted":
			res.Imported++
			res.Total++
		case "duplicate":
			res.Duplicates++
			res.Total++
		case "exists":
			res.Existing++
			res.Total++
		}
	}
	return res, nil
}

// backfill 連線後背景回填近期活動
func (h *StravaHandler) backfill(conn *Connection) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	res, err := h.syncRecent(ctx, conn)
	if err != nil {
		log.Error().Err(err).Msg("strava backfill failed")
		notify.Alert("strava_webhook_err", "Strava Webhook 處理錯誤", fmt.Sprintf("backfill failed: %v", err))
		return
	}
	log.Info().Int("imported", res.Imported).Int("dup", res.Duplicates).Str("user", conn.UserID).Msg("strava backfill done")
}

// fingerprintOf 精確指紋：起始秒|距離公尺|移動秒（同一筆檔案在不同帳號會一致）
func fingerprintOf(startUnix int64, distanceM float64, durationS int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d|%.0f|%d", startUnix, distanceM, durationS)))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func isRun(a *stravaActivity) bool {
	t := a.SportType
	if t == "" {
		t = a.Type
	}
	switch t {
	case "Run", "TrailRun", "VirtualRun":
		return true
	}
	return false
}

// importOne 正規化單筆 Strava 活動並寫入。floor=連接時間（見 Connection.ConnectedAt）：連接當下以前的活動
// 一律不抓（使用者定案：從串接當下起計算，避免一連接就把整段歷史里程灌入造成 EXP/DP 暴衝）。
func (h *StravaHandler) importOne(ctx context.Context, userID string, floor time.Time, a *stravaActivity) ImportResult {
	if !isRun(a) || a.Distance <= 0 || a.MovingTime <= 0 {
		return ImportResult{Status: "skipped"}
	}
	recordedAt, err := time.Parse(time.RFC3339, a.StartDate)
	if err != nil {
		return ImportResult{Status: "skipped"}
	}
	// 連接當下以前的資料一律不抓
	if !floor.IsZero() && recordedAt.Before(floor) {
		return ImportResult{Status: "skipped"}
	}
	distanceKm := a.Distance / 1000.0
	na := &NormalizedActivity{
		UserID:      userID,
		Source:      providerStrava,
		ExternalID:  strconv.FormatInt(a.ID, 10),
		Fingerprint: fingerprintOf(recordedAt.Unix(), a.Distance, a.MovingTime),
		DistanceKm:  distanceKm,
		DurationS:   a.MovingTime,
		AvgPaceS:    int(math.Round(float64(a.MovingTime) / distanceKm)),
		RecordedAt:  recordedAt,
	}
	if a.TotalElevationGain > 0 {
		v := a.TotalElevationGain
		na.AscentM = &v
	}
	if a.HasHeartrate && a.AverageHeartrate > 0 {
		v := int(math.Round(a.AverageHeartrate))
		na.AvgHR = &v
	}
	res, err := h.repo.ImportActivity(ctx, na)
	if err != nil {
		log.Error().Err(err).Msg("strava import activity failed")
		notify.Alert("strava_webhook_err", "Strava Webhook 處理錯誤", fmt.Sprintf("import activity failed: %v", err))
		return ImportResult{Status: "error"}
	}
	// stamina.ChargeSP 維持「僅新匯入」才扣血：SP 是扣血動作，同一趟不能被扣兩次。
	if res.Status == "inserted" && na.DistanceKm > 0 {
		stamina.ChargeSP(ctx, h.repo.db, na.UserID, na.DistanceKm, na.AvgPaceS)
	}
	// AwardMileageExp 的呼叫條件放寬到「新匯入」或「同帳號跨裝置的良性重複(multi_device_duplicate)」：
	// 後者進函式後會走差額補償流程（可能補上與既有那筆的里程/EXP/DP 差額）。其他 duplicate 原因
	// （同源精確重複 duplicate、跨帳號洗數據 cross_account_duplicate）不 inline 呼叫，交給函式內部
	// 的 flagged 政策擋（cross_account 永遠不發；純同源 duplicate 本來就已經是同一筆數值，sweep
	// 對帳掃到時一樣會被函式正確處理）。
	if (res.Status == "inserted" || (res.Status == "duplicate" && res.Reason == "multi_device_duplicate")) && na.DistanceKm > 0 {
		if err := h.repo.AwardMileageExp(ctx, res.ID, na.UserID); err != nil {
			log.Error().Err(err).Str("activity", res.ID).Msg("strava award mileage exp failed")
			notify.Alert("strava_webhook_err", "Strava Webhook 處理錯誤", fmt.Sprintf("activity=%s award mileage exp failed: %v", res.ID, err))
		}
	}
	return res
}

// --- state 簽章（callback 無登入，用 HMAC 綁定發起者）---

func (h *StravaHandler) signState(userID, returnURL string) string {
	// 以 \n 分隔（userID 為 UUID、無換行）：userID \n exp \n returnURL
	msg := strings.Join([]string{userID, strconv.FormatInt(time.Now().Add(15*time.Minute).Unix(), 10), returnURL}, "\n")
	return base64.RawURLEncoding.EncodeToString([]byte(msg)) + "." + h.mac(msg)
}

func (h *StravaHandler) verifyState(state string) (userID, returnURL string, ok bool) {
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

// appendQuery 在 URL 上加一個查詢參數（保留既有 query）
func appendQuery(base, key, val string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	q.Set(key, val)
	u.RawQuery = q.Encode()
	return u.String()
}

func (h *StravaHandler) mac(msg string) string {
	m := hmac.New(sha256.New, []byte(h.cfg.JWTSecret))
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// --- helpers ---

func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
