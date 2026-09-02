package integration

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/gpscalib"
	"github.com/dor/api/internal/stamina"
)

// Terra 聚合器接入（Phase 1）：一次串接 Garmin/COROS/Polar/Suunto/Wahoo（明確排除 Strava——
// Strava 走我方既有、有申請額度上限的官方 App，見 strava.go，不透過 Terra 再接一次）。
// 落地策略：source 存「底層品牌」(garmin/coros/polar/suunto/wahoo)、external_id 存該品牌活動 id →
// 直接沿用既有 ImportActivity 的 UNIQUE(source,external_id) 精準去重 + 跨來源優先序去重
// （見 profile/dedup.go、gpscalib/service.go 的優先序清單，本次一併擴充）。
// user_integrations 一列同時可能是 direct（COROS 官方直連 OAuth）或 terra（本檔）連線，
// 由新增的 via 欄位（migrations/165）區分；(user_id, provider) 唯一鍵兩者共用，見 repository.go
// Save/SaveTerra 的對稱處理註解。
//
// 端點文件依據（2026-09-03 查證，pretty 網址 404，須用對應 .md 網址）：
//   - widget session：https://docs.tryterra.co/unified-api/user-authentication/implementation-terra-widget.md
//   - redirect 帶回參數：https://docs.tryterra.co/unified-api/user-authentication/customising-authentication-redirects.md
//     （成功：附加 user_id / resource / reference_id；範例顯示以 Terra 自帶頁面路徑示範，未證實對「已帶
//     query string 的自訂網址」是用 ? 還是 & 附加——本檔一律不在 auth_success/failure_redirect_url 上
//     自己帶 query，規避這個未證實的細節，見 Connect 註解）
//   - webhook 事件：https://docs.tryterra.co/reference/health-and-fitness-api/event-types.md
//   - 活動欄位：https://docs.tryterra.co/reference/health-and-fitness-api/data-models.md
//     （對照 Phase 0 骨架，修正兩個拼字：distance_meters 不是 distance_metres；
//     gain_actual_meters 不是 gain_actual_metres——骨架當時是先行對映、從未真的收過 Terra
//     payload 校對過，這兩個欄位過去等同一直讀不到值）
//   - userInfo 驗證：https://docs.tryterra.co/faq/help-topics/data-api-sdk/authentication-users-and-connection-state/userinfo-verification-flow.md
//   - deauthenticateUser：https://docs.tryterra.co/faq/help-topics/data-api-sdk/authentication-users-and-connection-state/deauthenticate-users.md
//   - 活動類型 enum：https://docs.tryterra.co/unified-api/activity-types.md
//
// 未設定 TERRA_DEV_ID/TERRA_API_KEY/TERRA_SIGNING_SECRET 任一 → enabled()=false：
// webhook 只回 200 ack、不處理；/connect 回 503；/status 回 enabled:false（不影響現有流程）。

// TerraConfig 由環境變數注入（TERRA_DEV_ID / TERRA_API_KEY / TERRA_SIGNING_SECRET /
// TERRA_REDIRECT_URI / TERRA_API_BASE / TERRA_PROVIDERS），FrontendURL 沿用 config.Config
// 既有的 FRONTEND_URL（與 Strava/COROS 共用同一顆設定）。
type TerraConfig struct {
	DevID         string
	APIKey        string
	SigningSecret string
	RedirectURI   string   // Terra widget 的 auth_success/auth_failure_redirect_url；空字串用預設
	APIBase       string   // Terra API base；空字串用預設 https://api.tryterra.co
	FrontendURL   string   // callback 完成後導回前台（Terra widget 無法攜帶自訂 state，一律導回此固定路徑）
	Providers     []string // 允許透過 Terra 連接的品牌（大寫，如 GARMIN）；空切片用預設
}

const (
	terraDefaultAPIBase     = "https://api.tryterra.co"
	terraDefaultRedirectURI = "https://www.dor.tw/api/v1/integrations/terra/callback"
)

// terraDefaultProviders：預設可透過 Terra 連接的品牌。刻意不含 STRAVA——見上方檔案註解。
var terraDefaultProviders = []string{"GARMIN", "COROS", "POLAR", "SUUNTO", "WAHOO"}

// ParseTerraProviders 解析 TERRA_PROVIDERS 環境變數（逗號分隔、大小寫不拘）。
// 恆濾掉 STRAVA：無論環境變數怎麼設，都不允許透過 Terra 重複串接 Strava（見上方檔案註解）。
// 結果為空（含未設定環境變數）時回 nil，由 NewTerraHandler 套用預設清單。
func ParseTerraProviders(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.ToUpper(strings.TrimSpace(p))
		if p == "" || p == providerStrava2 {
			continue
		}
		out = append(out, p)
	}
	return out
}

// providerStrava2：大寫版 "STRAVA"，只在 ParseTerraProviders 的排除檢查用；避免與 strava.go 既有的
// 小寫 providerStrava 常數（=活動來源代碼）混用造成誤解。
const providerStrava2 = "STRAVA"

type TerraHandler struct {
	repo        *Repository
	cfg         TerraConfig
	requireAuth func(http.Handler) http.Handler
	hc          *http.Client
}

func NewTerraHandler(repo *Repository, cfg TerraConfig, requireAuth func(http.Handler) http.Handler) *TerraHandler {
	if len(cfg.Providers) == 0 {
		cfg.Providers = terraDefaultProviders
	}
	if cfg.APIBase == "" {
		cfg.APIBase = terraDefaultAPIBase
	}
	if cfg.RedirectURI == "" {
		cfg.RedirectURI = terraDefaultRedirectURI
	}
	return &TerraHandler{repo: repo, cfg: cfg, requireAuth: requireAuth, hc: &http.Client{Timeout: 15 * time.Second}}
}

// enabled 三個都要有值才算啟用：DevID/APIKey 用於 REST 呼叫（widget session／userInfo／
// deauthenticateUser），SigningSecret 用於 webhook 簽章驗證。三者任一缺，Terra 整合就無法完整運作
// （例如只有 SigningSecret 沒有 APIKey：webhook activity 事件雖能驗簽，但 callback 端無法用
// userInfo 二次驗證使用者，安全上不該讓這種半殘狀態上線），故合併成單一開關，不像 Phase 0 骨架
// 只看 SigningSecret。
func (h *TerraHandler) enabled() bool {
	return h.cfg.DevID != "" && h.cfg.APIKey != "" && h.cfg.SigningSecret != ""
}

// isValidUUID 驗證字串是否為合法 UUID 格式（比照 internal/activityreward 等套件慣例，本套件獨立
// 維護一份，避免跨 package import 內部工具函式）。Terra 的 reference_id 是任何人都能在 webhook/
// callback 帶入的裸字串，寫入 DB 前必須先擋掉非 UUID 格式，並搭配 UserExists 確認真的是既有使用者。
var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isValidUUID(s string) bool { return uuidRE.MatchString(s) }

// Router 掛在 /api/v1/integrations/terra。webhook/callback 公開；connect/status/disconnect 需登入。
func (h *TerraHandler) Router() http.Handler {
	r := chi.NewRouter()
	r.Post("/webhook", h.WebhookEvent) // Terra 事件推播（公開）
	r.Get("/callback", h.Callback)     // Terra widget 授權完成導回（公開）
	r.Group(func(r chi.Router) {
		r.Use(h.requireAuth)
		r.Get("/connect", h.Connect) // 取得 widget session url
		r.Get("/status", h.Status)   // 連線狀態
		r.Post("/disconnect", h.Disconnect)
	})
	return r
}

// --- 需登入端點 ---

// GET /connect?return=<path> → { "url": "<terra widget url>" }
func (h *TerraHandler) Connect(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	if !h.enabled() {
		respondErr(w, http.StatusServiceUnavailable, "terra_disabled")
		return
	}
	// return= 目前只接收、不使用：Terra widget session 沒有可攜帶自訂 state 的欄位，
	// auth_success/failure_redirect_url 只能是固定網址；且未證實 Terra 附加 user_id/resource/
	// reference_id 時是用 "?" 還是 "&"（見檔案頂端註解），若我方也在這個網址帶查詢字串，
	// 遇到 Terra 用 "?" 附加就會整個被蓋掉、甚至產生不合法的雙 "?" 網址。因此一律不帶查詢字串，
	// Callback 統一導回 FrontendURL（比照 strava.go/coros.go 在沒有合法 return 時的 fallback）。
	_ = r.URL.Query().Get("return")

	body, err := json.Marshal(map[string]any{
		"reference_id":              userID,
		"providers":                 strings.Join(h.cfg.Providers, ","),
		"language":                  "en",
		"auth_success_redirect_url": h.cfg.RedirectURI,
		"auth_failure_redirect_url": h.cfg.RedirectURI,
	})
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.cfg.APIBase+"/v2/auth/generateWidgetSession", bytes.NewReader(body))
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	req.Header.Set("dev-id", h.cfg.DevID)
	req.Header.Set("x-api-key", h.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.hc.Do(req)
	if err != nil {
		log.Error().Err(err).Msg("terra generateWidgetSession request failed")
		respondErr(w, http.StatusBadGateway, "failed")
		return
	}
	defer resp.Body.Close()
	// Terra 實測回 201 Created（不是文件示意的 200）——任何 2xx 都算成功，別再硬比 200。
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		log.Error().Int("status", resp.StatusCode).Str("body", string(b)).
			Msg("terra generateWidgetSession non-2xx") // 絕不記 dev-id/x-api-key
		respondErr(w, http.StatusBadGateway, "failed")
		return
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.URL == "" {
		log.Error().Err(err).Msg("terra generateWidgetSession: bad response body")
		respondErr(w, http.StatusBadGateway, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"url": out.URL})
}

// GET /status → { enabled, providers:[小寫品牌...], connections:[{provider,connected_at,via}...] }
// connections 只含 via='terra' 的列——同品牌若是 direct 連線（如 COROS 官方直連），
// 屬於各自 provider 專用的 /status（如 /integrations/coros/status），不重複出現在這裡。
func (h *TerraHandler) Status(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	conns, err := h.repo.ListTerraConnections(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	out := make([]map[string]any, 0, len(conns))
	for _, c := range conns {
		out = append(out, map[string]any{
			"provider":     c.Provider,
			"connected_at": c.ConnectedAt.Format(time.RFC3339),
			"via":          "terra",
		})
	}
	providers := make([]string, 0, len(h.cfg.Providers))
	for _, p := range h.cfg.Providers {
		providers = append(providers, strings.ToLower(p))
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"enabled":     h.enabled(),
		"providers":   providers,
		"connections": out,
	})
}

// POST /disconnect?provider=<brand>
func (h *TerraHandler) Disconnect(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	brand := providerToSource(r.URL.Query().Get("provider"))
	if brand == "" {
		respondErr(w, http.StatusBadRequest, "provider required")
		return
	}
	conn, err := h.repo.GetByUser(r.Context(), userID, brand)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if conn == nil || conn.Via != "terra" {
		respondErr(w, http.StatusNotFound, "not connected via terra")
		return
	}
	// 向 Terra 撤權；404/410（帳號早已不存在）視為「本來就已經斷了」不算錯誤，其餘錯誤只記錄、
	// 不擋本地中斷（比照 strava.go/coros.go 的 deauthorize：本地連線/活動仍會刪除，使用者達成
	// 中斷目的；失敗留給事後補償清理）。
	h.deauthenticateTerraUser(r.Context(), conn.ProviderUserID)
	if err := h.repo.Delete(r.Context(), userID, brand); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	// 若使用者偏好資料來源正好是這個品牌，斷線後改回預設 gps（見 repository.go ResetPreferredSource）；
	// 失敗只記錄、不擋斷線本身。
	if err := h.repo.ResetPreferredSource(r.Context(), userID, brand); err != nil {
		log.Warn().Err(err).Str("user", userID).Str("provider", brand).Msg("terra disconnect: reset preferred data source failed")
	}
	// external_award_ledger（見 mileage_exp.go）不受影響：DeleteProviderActivities 只刪 activities
	// 列本身，帳本記錄留存，防止「中斷再重連」重複請領 EXP/DP（比照 strava.go Disconnect 的刪除義務）。
	if err := h.repo.DeleteProviderActivities(r.Context(), userID, brand); err != nil {
		log.Error().Err(err).Str("user", userID).Str("provider", brand).
			Msg("terra disconnect: delete imported activities failed")
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// deauthenticateTerraUser DELETE /v2/auth/deauthenticateUser?user_id=<terra_user_id>
func (h *TerraHandler) deauthenticateTerraUser(ctx context.Context, terraUserID string) {
	if terraUserID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		h.cfg.APIBase+"/v2/auth/deauthenticateUser?user_id="+url.QueryEscape(terraUserID), nil)
	if err != nil {
		return
	}
	req.Header.Set("dev-id", h.cfg.DevID)
	req.Header.Set("x-api-key", h.cfg.APIKey)
	resp, err := h.hc.Do(req)
	if err != nil {
		log.Warn().Err(err).Msg("terra deauthenticateUser request failed")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return // 成功（任何 2xx），或帳號本來就已經不存在——都視為「已達成撤權目的」
	}
	log.Warn().Int("status", resp.StatusCode).Msg("terra deauthenticateUser non-2xx")
}

// --- 公開端點 ---

// GET /callback — Terra widget 授權完成導回。成功時 Terra 附加 user_id/resource/reference_id
// （見檔案頂端註解）；query params 絕不可直接信任（任何人都能造這個 GET 請求），一律再用
// userInfo(user_id) 向 Terra 反查、核對 reference_id 一致才落地寫入連線。
func (h *TerraHandler) Callback(w http.ResponseWriter, r *http.Request) {
	redirectFront := func(status, reason string) string {
		target := appendQuery(h.cfg.FrontendURL, "terra", status)
		if reason != "" {
			target = appendQuery(target, "reason", reason)
		}
		return target
	}
	if !h.enabled() {
		http.Redirect(w, r, redirectFront("disabled", ""), http.StatusFound)
		return
	}
	q := r.URL.Query()
	terraUserID := strings.TrimSpace(q.Get("user_id"))
	refID := strings.TrimSpace(q.Get("reference_id"))
	brand := strings.TrimSpace(q.Get("resource"))
	source := providerToSource(brand)
	// Terra user_id 本身是 UUID；非 UUID 形狀的值不值得再花一趟 userInfo 往返。
	if !isValidUUID(terraUserID) || !isValidUUID(refID) || source == "" {
		// 失敗案例的參數名文件未證實明確列出；常見會帶 reason，容忍其存在或缺席一併導回失敗頁。
		http.Redirect(w, r, redirectFront("failed", strings.TrimSpace(q.Get("reason"))), http.StatusFound)
		return
	}
	verified, err := h.fetchTerraUserInfo(r.Context(), terraUserID)
	if err != nil || verified == nil || strings.TrimSpace(verified.ReferenceID) != refID {
		log.Warn().Str("terra_user", terraUserID).Msg("terra callback: userInfo verification failed/mismatch")
		http.Redirect(w, r, redirectFront("failed", ""), http.StatusFound)
		return
	}
	// 品牌以 Terra 端 userInfo 回報的 provider 為準（query 的 resource 只是提示）：連線列的 provider
	// 決定後續 activity 事件用哪個 source 去重／發獎，不能讓瀏覽器端可改的參數決定。
	if vs := providerToSource(strings.TrimSpace(verified.Provider)); vs != "" {
		source = vs
	}
	if ok, err := h.repo.UserExists(r.Context(), refID); err != nil || !ok {
		log.Warn().Str("reference_id", refID).Msg("terra callback: reference_id is not a known user")
		http.Redirect(w, r, redirectFront("failed", ""), http.StatusFound)
		return
	}
	if err := h.repo.SaveTerra(r.Context(), &Connection{
		UserID: refID, Provider: source, ProviderUserID: terraUserID,
		Scope: string(verified.Scopes), ExpiresAt: terraFarFutureExpiry(),
	}); err != nil {
		log.Error().Err(err).Msg("terra callback: save connection failed")
		http.Redirect(w, r, redirectFront("error", ""), http.StatusFound)
		return
	}
	target := appendQuery(h.cfg.FrontendURL, "terra", "connected")
	target = appendQuery(target, "provider", source)
	http.Redirect(w, r, target, http.StatusFound)
}

// fetchTerraUserInfo GET /v2/userInfo?user_id=<terra_user_id> → {"user": {...}}
func (h *TerraHandler) fetchTerraUserInfo(ctx context.Context, terraUserID string) (*terraUser, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		h.cfg.APIBase+"/v2/userInfo?user_id="+url.QueryEscape(terraUserID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("dev-id", h.cfg.DevID)
	req.Header.Set("x-api-key", h.cfg.APIKey)
	resp, err := h.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		log.Warn().Int("status", resp.StatusCode).Str("body", string(b)).Msg("terra userInfo non-2xx")
		return nil, fmt.Errorf("terra userInfo http %d", resp.StatusCode)
	}
	var out struct {
		User terraUser `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out.User, nil
}

// terraFarFutureExpiry：Terra 連線沒有我方需要刷新的 access token（見 SaveTerra 註解），
// expires_at 欄位是 NOT NULL，這裡填一個不具意義、遠期的值，純粹滿足欄位約束。
func terraFarFutureExpiry() time.Time { return time.Now().AddDate(100, 0, 0) }

// --- webhook payload ---

// terraScopes 容忍 scopes 欄位是 JSON 陣列或逗號分隔字串兩種格式（文件未明確保證是哪一種），
// 正規化後統一存成逗號字串。無法辨識的格式（如 null）容忍為空字串，不擋整個 payload 解析。
type terraScopes string

func (s *terraScopes) UnmarshalJSON(b []byte) error {
	var arr []string
	if err := json.Unmarshal(b, &arr); err == nil {
		*s = terraScopes(strings.Join(arr, ","))
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		*s = terraScopes(str)
		return nil
	}
	*s = ""
	return nil
}

// terraUser webhook/userInfo 共用的 user 物件（只取落地所需欄位）。
type terraUser struct {
	UserID      string      `json:"user_id"`
	Provider    string      `json:"provider"`
	ReferenceID string      `json:"reference_id"`
	Scopes      terraScopes `json:"scopes"`
}

// terraUserEventPayload：auth / deauth / connection_error 共用的頂層形狀。
type terraUserEventPayload struct {
	Type        string    `json:"type"`
	Status      string    `json:"status"`
	User        terraUser `json:"user"`
	ReferenceID string    `json:"reference_id"`
}

// terraReauthPayload：user_reauth 事件——同一 reference_id+provider 重新授權，Terra 端 user_id 換新，
// 舊 user_id 已被 Terra 刪除。
type terraReauthPayload struct {
	Type    string    `json:"type"`
	NewUser terraUser `json:"new_user"`
	OldUser terraUser `json:"old_user"`
}

// terraActivityPayload：activity 事件。
type terraActivityPayload struct {
	Type string          `json:"type"`
	User terraUser       `json:"user"`
	Data []terraActivity `json:"data"`
}

type terraActivity struct {
	Metadata struct {
		StartTime string `json:"start_time"`
		SummaryID string `json:"summary_id"`
		Type      int    `json:"type"` // Terra 運動類型 enum，見 isTerraRunningActivity
	} `json:"metadata"`
	DistanceData struct {
		Summary struct {
			// ⚠️ 修正 Phase 0 骨架的拼字錯誤：文件是 distance_meters（meters），不是 distance_metres。
			DistanceMeters float64 `json:"distance_meters"`
			Elevation      struct {
				// ⚠️ 同上：gain_actual_meters，不是 gain_actual_metres。
				GainActualMeters *float64 `json:"gain_actual_meters"`
			} `json:"elevation"`
		} `json:"summary"`
	} `json:"distance_data"`
	ActiveDurationsData struct {
		ActivitySeconds float64 `json:"activity_seconds"`
	} `json:"active_durations_data"`
	HeartRateData struct {
		Summary struct {
			AvgHrBpm *float64 `json:"avg_hr_bpm"`
		} `json:"summary"`
	} `json:"heart_rate_data"`
	// MovementData 僅解析、不用於 AvgPaceS 計算：本方比照 strava.go/coros.go 的作法，配速一律由
	// 「本方採用的 duration/distance」自算，維持三個來源口徑一致，不受各品牌自己算法差異影響。
	MovementData struct {
		AvgPaceMinutesPerKm *float64 `json:"avg_pace_minutes_per_kilometer"`
	} `json:"movement_data"`
}

// terraRunningTypes：Terra 活動類型 enum 中屬於「跑步」的代碼（見 unified-api/activity-types.md）。
// 8=RUNNING 56=JOGGING 57=RUNNING_ON_SAND 58=TREADMILL_RUNNING 133=INDOOR_RUNNING 149=TRAIL_RUNNING。
// 刻意排除健走/健行類代碼（7/35/93/95/116），比照 coros.go corosRunningModes 只認跑步。
var terraRunningTypes = map[int]bool{
	8: true, 56: true, 57: true, 58: true, 133: true, 149: true,
}

func isTerraRunningActivity(t int) bool { return terraRunningTypes[t] }

// providerToSource 把 Terra 的 provider（大小寫不拘）轉成我方 activities.source（小寫品牌）。
func providerToSource(p string) string {
	switch strings.ToUpper(strings.TrimSpace(p)) {
	case "GARMIN":
		return "garmin"
	case "COROS":
		return "coros"
	case "POLAR":
		return "polar"
	case "SUUNTO":
		return "suunto"
	case "WAHOO":
		return "wahoo"
	case "STRAVA":
		return providerStrava
	default:
		return strings.ToLower(strings.TrimSpace(p))
	}
}

// WebhookEvent POST /webhook — 先驗簽、立即 ack，重活背景處理（比照 coros.go：Terra 8 秒逾時、
// 最多重試 10 次共 ~8 小時，逾時或非 2xx 會重送；at-least-once 靠 ImportActivity 的
// UNIQUE(source,external_id) 與各事件處理函式自身的查找-覆蓋語意天然冪等）。
// ⚠️ 絕不在 info 等級記錄原始 body（可能含健康資料）；只記事件類型/筆數等中繼資訊。
func (h *TerraHandler) WebhookEvent(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !h.enabled() {
		w.WriteHeader(http.StatusOK) // 尚未啟用：只 ack、不處理
		return
	}
	if !h.verifySignature(r.Header.Get("terra-signature"), body) {
		log.Warn().Msg("terra webhook: signature verify failed")
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK) // 先 ack，重活背景處理
	go h.processWebhook(body)
}

func (h *TerraHandler) processWebhook(body []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		log.Warn().Err(err).Msg("terra webhook: bad json")
		return
	}
	switch env.Type {
	case "auth":
		h.handleAuthEvent(ctx, body)
	case "user_reauth":
		h.handleReauthEvent(ctx, body)
	case "deauth":
		h.handleDeauthEvent(ctx, body)
	case "connection_error":
		var p terraUserEventPayload
		_ = json.Unmarshal(body, &p)
		log.Warn().Str("provider", p.User.Provider).Msg("terra webhook: connection_error event")
	case "activity":
		h.handleActivityEvent(ctx, body)
	default:
		// s3_payload / large_request_sending / large_request_processing / daily / sleep 等：ack+忽略。
		log.Debug().Str("type", env.Type).Msg("terra webhook: unhandled event type, ack+ignore")
	}
}

// handleAuthEvent auth 事件：Terra 端授權成功，落地（或覆蓋）一條連線。
func (h *TerraHandler) handleAuthEvent(ctx context.Context, body []byte) {
	var p terraUserEventPayload
	if err := json.Unmarshal(body, &p); err != nil {
		log.Warn().Err(err).Msg("terra webhook: auth payload decode failed")
		return
	}
	refID := strings.TrimSpace(p.User.ReferenceID)
	if refID == "" {
		refID = strings.TrimSpace(p.ReferenceID)
	}
	source := providerToSource(p.User.Provider)
	terraUserID := strings.TrimSpace(p.User.UserID)
	if !isValidUUID(refID) || terraUserID == "" || source == "" {
		log.Warn().Str("provider", p.User.Provider).Msg("terra webhook: auth event missing/invalid reference_id")
		return
	}
	if ok, err := h.repo.UserExists(ctx, refID); err != nil || !ok {
		log.Warn().Msg("terra webhook: auth event reference_id is not a known user")
		return
	}
	if err := h.repo.SaveTerra(ctx, &Connection{
		UserID: refID, Provider: source, ProviderUserID: terraUserID,
		Scope: string(p.User.Scopes), ExpiresAt: terraFarFutureExpiry(),
	}); err != nil {
		log.Error().Err(err).Msg("terra webhook: auth event save connection failed")
	}
}

// handleReauthEvent user_reauth 事件：同一 reference_id+provider 重新授權，Terra 端 user_id 換新、
// 舊 user_id 已被 Terra 刪除——把連線列的 provider_user_id 換成新的，connected_at 維持不變
// （SaveTerra 的 ON CONFLICT 不覆寫 created_at，見該函式註解）。
func (h *TerraHandler) handleReauthEvent(ctx context.Context, body []byte) {
	var p terraReauthPayload
	if err := json.Unmarshal(body, &p); err != nil {
		log.Warn().Err(err).Msg("terra webhook: user_reauth payload decode failed")
		return
	}
	source := providerToSource(p.OldUser.Provider)
	if source == "" {
		source = providerToSource(p.NewUser.Provider)
	}
	oldTerraID := strings.TrimSpace(p.OldUser.UserID)
	newTerraID := strings.TrimSpace(p.NewUser.UserID)
	if oldTerraID == "" || newTerraID == "" || source == "" {
		return
	}
	conn, err := h.repo.GetByProviderUser(ctx, source, oldTerraID)
	if err != nil || conn == nil || conn.Via != "terra" {
		log.Warn().Str("provider", source).Msg("terra webhook: user_reauth for unknown/non-terra connection")
		return
	}
	if err := h.repo.SaveTerra(ctx, &Connection{
		UserID: conn.UserID, Provider: source, ProviderUserID: newTerraID,
		Scope: string(p.NewUser.Scopes), ExpiresAt: terraFarFutureExpiry(),
	}); err != nil {
		log.Error().Err(err).Msg("terra webhook: user_reauth save connection failed")
	}
}

// handleDeauthEvent deauth 事件：使用者在品牌端或 Terra 端撤權。刪本地連線 + 已匯入活動
// （比照 strava.go handleDeauthorizeEvent 的刪除義務；external_award_ledger 不受影響，見
// mileage_exp.go，防止刪除重匯冒領 EXP/DP）。
func (h *TerraHandler) handleDeauthEvent(ctx context.Context, body []byte) {
	var p terraUserEventPayload
	if err := json.Unmarshal(body, &p); err != nil {
		log.Warn().Err(err).Msg("terra webhook: deauth payload decode failed")
		return
	}
	source := providerToSource(p.User.Provider)
	terraUserID := strings.TrimSpace(p.User.UserID)
	if source == "" || terraUserID == "" {
		return
	}
	conn, err := h.repo.GetByProviderUser(ctx, source, terraUserID)
	if err != nil || conn == nil || conn.Via != "terra" {
		return // 查無對應 Terra 連線（可能已經斷過、或這個品牌本來是 direct 連線）：無需處理
	}
	if err := h.repo.Delete(ctx, conn.UserID, source); err != nil {
		log.Error().Err(err).Msg("terra webhook: deauth delete connection failed")
		return
	}
	// 若使用者偏好資料來源正好是這個品牌，撤權後改回預設 gps（見 repository.go ResetPreferredSource）；
	// 失敗只記錄、不擋撤權處理本身。
	if err := h.repo.ResetPreferredSource(ctx, conn.UserID, source); err != nil {
		log.Warn().Err(err).Str("user", conn.UserID).Str("provider", source).Msg("terra webhook: deauth reset preferred data source failed")
	}
	if err := h.repo.DeleteProviderActivities(ctx, conn.UserID, source); err != nil {
		log.Error().Err(err).Str("user", conn.UserID).Msg("terra webhook: deauth delete activities failed")
	}
}

// handleActivityEvent activity 事件：依 provider_user_id 找回對應的 DOR 使用者；查無連線時
// 保底用 reference_id 建一條（connected_at=now，理論上不該發生——auth 事件應該早於 activity 送達，
// 這裡只是防呆，不讓漏收 auth 事件變成整批活動被丟棄）。
func (h *TerraHandler) handleActivityEvent(ctx context.Context, body []byte) {
	var p terraActivityPayload
	if err := json.Unmarshal(body, &p); err != nil {
		log.Warn().Err(err).Msg("terra webhook: activity payload decode failed")
		return
	}
	source := providerToSource(p.User.Provider)
	terraUserID := strings.TrimSpace(p.User.UserID)
	if source == "" || terraUserID == "" || len(p.Data) == 0 {
		return
	}

	conn, err := h.repo.GetByProviderUser(ctx, source, terraUserID)
	if err != nil {
		log.Error().Err(err).Msg("terra webhook: activity lookup connection failed")
		return
	}
	if conn == nil {
		refID := strings.TrimSpace(p.User.ReferenceID)
		if !isValidUUID(refID) {
			log.Warn().Str("provider", source).Msg("terra webhook: activity event has no known connection and no valid reference_id")
			return
		}
		if ok, uerr := h.repo.UserExists(ctx, refID); uerr != nil || !ok {
			log.Warn().Msg("terra webhook: activity fallback reference_id is not a known user")
			return
		}
		if err := h.repo.SaveTerra(ctx, &Connection{
			UserID: refID, Provider: source, ProviderUserID: terraUserID,
			Scope: string(p.User.Scopes), ExpiresAt: terraFarFutureExpiry(),
		}); err != nil {
			log.Error().Err(err).Msg("terra webhook: activity fallback save connection failed")
			return
		}
		conn, err = h.repo.GetByUser(ctx, refID, source)
		if err != nil || conn == nil {
			log.Error().Err(err).Msg("terra webhook: activity fallback re-read connection failed")
			return
		}
	} else if conn.Via != "terra" {
		// 理論上不會發生：Terra user_id 命名空間與 direct 連線（如 COROS openId）不同，
		// 這裡防呆保留、不匯入，避免任何 ID 命名巧合污染一條直連的連線。
		log.Warn().Str("provider", source).Msg("terra webhook: activity event resolved to a non-terra connection, skipping")
		return
	}

	imported, dup, skipped := 0, 0, 0
	for i := range p.Data {
		switch h.importTerra(ctx, conn.UserID, source, conn.ConnectedAt, &p.Data[i]).Status {
		case "inserted":
			imported++
		case "duplicate":
			dup++
		default:
			skipped++
		}
	}
	log.Debug().Str("provider", source).Int("imported", imported).Int("duplicate", dup).Int("skipped", skipped).
		Msg("terra webhook: activity batch processed")
}

// mapTerraActivity 純函式：把單筆 Terra activity 正規化成 NormalizedActivity（不碰 DB，供單元測試）。
// ok=false 代表這筆不該匯入：非跑步類型／缺距離或時間／開始時間解析失敗／早於 floor（連接當下）。
func mapTerraActivity(userID, source string, floor time.Time, a *terraActivity) (*NormalizedActivity, bool) {
	if !isTerraRunningActivity(a.Metadata.Type) {
		return nil, false
	}
	distM := a.DistanceData.Summary.DistanceMeters
	durS := int(math.Round(a.ActiveDurationsData.ActivitySeconds))
	if distM <= 0 || durS <= 0 {
		return nil, false
	}
	recordedAt, err := time.Parse(time.RFC3339, a.Metadata.StartTime)
	if err != nil {
		return nil, false
	}
	// 連接當下以前的資料一律不抓（比照 strava.go/coros.go 的 floor 保護，避免一連接就把整段歷史
	// 里程灌入導致 EXP/DP 暴衝；使用者定案：從串接當下起計算）。
	if !floor.IsZero() && recordedAt.Before(floor) {
		return nil, false
	}
	distanceKm := distM / 1000.0
	extID := a.Metadata.SummaryID
	if extID == "" {
		extID = source + ":" + strconv.FormatInt(recordedAt.Unix(), 10) // 保底外部 id
	}
	na := &NormalizedActivity{
		UserID:      userID,
		Source:      source, // 底層品牌，非 'terra' → 可跨「直連/Terra」精準去重
		ExternalID:  extID,
		Fingerprint: fingerprintOf(recordedAt.Unix(), distM, durS),
		DistanceKm:  distanceKm,
		DurationS:   durS,
		AvgPaceS:    int(math.Round(float64(durS) / distanceKm)),
		RecordedAt:  recordedAt,
	}
	if g := a.DistanceData.Summary.Elevation.GainActualMeters; g != nil && *g > 0 {
		na.AscentM = g
	}
	if hr := a.HeartRateData.Summary.AvgHrBpm; hr != nil && *hr > 0 {
		v := int(math.Round(*hr))
		na.AvgHR = &v
	}
	return na, true
}

// importTerra 正規化＋落地單筆 Terra 活動（比照 strava.go importOne / coros.go importOne 的
// ImportActivity → GPS 校正 → ChargeSP → AwardMileageExp 流程）。
func (h *TerraHandler) importTerra(ctx context.Context, userID, source string, floor time.Time, a *terraActivity) ImportResult {
	na, ok := mapTerraActivity(userID, source, floor, a)
	if !ok {
		return ImportResult{Status: "skipped"}
	}
	res, err := h.repo.ImportActivity(ctx, na)
	if err != nil {
		log.Error().Err(err).Str("source", source).Msg("terra import activity failed")
		return ImportResult{Status: "error"}
	}
	// GPS 距離校正 T1 觸發點（比照 strava.go）：非同步重算（debounce），不阻塞這次匯入。
	if res.Status == "inserted" || res.Status == "duplicate" {
		gpscalib.RecomputeAsync(h.repo.db, na.UserID)
	}
	// stamina.ChargeSP 維持「僅新匯入」才扣血：SP 是扣血動作，同一趟不能被扣兩次。
	if res.Status == "inserted" && na.DistanceKm > 0 {
		stamina.ChargeSP(ctx, h.repo.db, na.UserID, na.DistanceKm, na.AvgPaceS)
	}
	// AwardMileageExp 的呼叫條件比照 strava.go/coros.go：新匯入，或同帳號跨裝置的良性重複
	// （multi_device_duplicate，會走差額補償流程）；其他 duplicate 原因交給函式內部的 flagged 政策擋。
	if (res.Status == "inserted" || (res.Status == "duplicate" && res.Reason == "multi_device_duplicate")) && na.DistanceKm > 0 {
		if err := h.repo.AwardMileageExp(ctx, res.ID, na.UserID); err != nil {
			log.Error().Err(err).Str("activity", res.ID).Msg("terra award mileage exp failed")
		}
	}
	return res
}

// verifySignature 驗證 Terra 的 terra-signature 標頭：格式 "t=<ts>,v1=<hmac_sha256(ts.body)>"，
// 另加 5 分鐘時間戳容忍窗（Phase 0 骨架沒有，防重送/重放的舊簽章被無限期接受）。
func (h *TerraHandler) verifySignature(header string, body []byte) bool {
	if h.cfg.SigningSecret == "" {
		return false
	}
	var ts, v1 string
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			ts = kv[1]
		case "v1":
			v1 = kv[1]
		}
	}
	if ts == "" || v1 == "" {
		return false
	}
	tsUnix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if d := time.Since(time.Unix(tsUnix, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(h.cfg.SigningSecret))
	mac.Write([]byte(ts + "." + string(body)))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(strings.ToLower(v1)))
}
