package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/auth"
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
}

func NewStravaHandler(repo *Repository, cfg StravaConfig, requireAuth func(http.Handler) http.Handler) *StravaHandler {
	return &StravaHandler{repo: repo, cfg: cfg, requireAuth: requireAuth, hc: &http.Client{Timeout: 15 * time.Second}}
}

func (h *StravaHandler) enabled() bool { return h.cfg.ClientID != "" && h.cfg.ClientSecret != "" }

// Router 掛在 /api/v1/integrations/strava（自行處理需登入的子路由）
func (h *StravaHandler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/callback", h.Callback)    // Strava OAuth 導回（公開）
	r.Get("/webhook", h.WebhookVerify) // Strava webhook 驗證（公開）
	r.Post("/webhook", h.WebhookEvent) // Strava webhook 事件（公開）
	r.Group(func(r chi.Router) {
		r.Use(h.requireAuth)
		r.Get("/connect", h.Connect)        // 取得授權 URL
		r.Get("/status", h.Status)          // 連線狀態
		r.Delete("/disconnect", h.Disconnect)
		r.Post("/sync", h.Sync)             // 手動匯入近期活動
		r.Get("/activities", h.Activities)  // 已同步活動清單
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
	// 背景回填近期活動（避免阻塞導回）
	go h.backfill(conn)
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

// POST /webhook — 活動事件（須 2 秒內回 200，處理放背景）
func (h *StravaHandler) WebhookEvent(w http.ResponseWriter, r *http.Request) {
	var ev struct {
		ObjectType string `json:"object_type"`
		AspectType string `json:"aspect_type"`
		ObjectID   int64  `json:"object_id"`
		OwnerID    int64  `json:"owner_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		w.WriteHeader(http.StatusOK) // 仍回 200，避免 Strava 重試風暴
		return
	}
	w.WriteHeader(http.StatusOK)
	if ev.ObjectType == "activity" && (ev.AspectType == "create" || ev.AspectType == "update") {
		go h.handleActivityEvent(ev.OwnerID, ev.ObjectID)
	}
}

func (h *StravaHandler) handleActivityEvent(ownerID, activityID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := h.repo.GetByProviderUser(ctx, providerStrava, strconv.FormatInt(ownerID, 10))
	if err != nil || conn == nil {
		return
	}
	access, err := h.tokenForUser(ctx, conn)
	if err != nil {
		log.Error().Err(err).Msg("strava token refresh failed")
		return
	}
	act, err := h.getActivity(ctx, access, activityID)
	if err != nil {
		log.Error().Err(err).Int64("activity", activityID).Msg("strava get activity failed")
		return
	}
	regAt, _ := h.repo.UserCreatedAt(ctx, conn.UserID)
	h.importOne(ctx, conn.UserID, regAt, act)
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

func (h *StravaHandler) postToken(ctx context.Context, form url.Values) (*stravaToken, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, stravaTokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("strava token http %d", resp.StatusCode)
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
	ID                 int64   `json:"id"`
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

func (h *StravaHandler) getJSON(ctx context.Context, access, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, stravaAPIBase+path, nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := h.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("strava api http %d", resp.StatusCode)
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

// syncRecent 拉「會員註冊時間之後」的活動並匯入，回傳統計。
// 用 Strava 的 after（epoch 秒）=會員 created_at 過濾；per_page=100（未來量大再分頁）。
func (h *StravaHandler) syncRecent(ctx context.Context, conn *Connection) (SyncResult, error) {
	var res SyncResult
	access, err := h.tokenForUser(ctx, conn)
	if err != nil {
		return res, err
	}
	// 只抓會員註冊後的活動（統一規則：註冊前不抓，避免影響 EXP/等級）
	regAt, err := h.repo.UserCreatedAt(ctx, conn.UserID)
	if err != nil {
		return res, err
	}
	after := regAt.Unix()
	var acts []stravaActivity
	if err := h.getJSON(ctx, access, fmt.Sprintf("/athlete/activities?after=%d&per_page=100", after), &acts); err != nil {
		return res, err
	}
	for i := range acts {
		r := h.importOne(ctx, conn.UserID, regAt, &acts[i])
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

// importOne 正規化單筆 Strava 活動並寫入。regAt=會員註冊時間，註冊前的活動一律不抓。
func (h *StravaHandler) importOne(ctx context.Context, userID string, regAt time.Time, a *stravaActivity) ImportResult {
	if !isRun(a) || a.Distance <= 0 || a.MovingTime <= 0 {
		return ImportResult{Status: "skipped"}
	}
	recordedAt, err := time.Parse(time.RFC3339, a.StartDate)
	if err != nil {
		return ImportResult{Status: "skipped"}
	}
	// 會員註冊時間以前的資料一律不抓（避免舊資料影響 EXP/等級）
	if !regAt.IsZero() && recordedAt.Before(regAt) {
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
		return ImportResult{Status: "error"}
	}
	// 體力值 SP：僅「新匯入」的活動扣血（已存在/重複不扣）
	if res.Status == "inserted" && na.DistanceKm > 0 {
		stamina.ChargeSP(ctx, h.repo.db, na.UserID, na.DistanceKm, na.AvgPaceS)
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
