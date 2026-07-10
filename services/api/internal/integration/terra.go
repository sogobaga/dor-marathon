package integration

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
)

// Terra 聚合器接入（Phase 0 骨架）：一條 webhook 收 Garmin / COROS / Strava… 正規化後的活動。
// 落地策略：source 存「底層品牌」(garmin/coros/strava)、external_id 存該品牌活動 id →
// 直接沿用既有 ImportActivity 的 UNIQUE(source,external_id) 精準去重 + 跨來源優先序去重。
// ⚠️ 本檔為骨架：payload 欄位路徑依 Terra 文件先行對映，接上正式帳號(TERRA_SIGNING_SECRET)後需以真實 payload 校對。
// 未設定 signing secret → enabled()=false，webhook 只回 200 ack、不處理（不影響現有流程）。

// TerraConfig 由環境變數注入（TERRA_DEV_ID / TERRA_API_KEY / TERRA_SIGNING_SECRET）。
type TerraConfig struct {
	DevID         string
	APIKey        string
	SigningSecret string
}

type TerraHandler struct {
	repo        *Repository
	cfg         TerraConfig
	requireAuth func(http.Handler) http.Handler
}

func NewTerraHandler(repo *Repository, cfg TerraConfig, requireAuth func(http.Handler) http.Handler) *TerraHandler {
	return &TerraHandler{repo: repo, cfg: cfg, requireAuth: requireAuth}
}

func (h *TerraHandler) enabled() bool { return h.cfg.SigningSecret != "" }

// Router 掛在 /api/v1/integrations/terra。webhook 公開；status 需登入（Phase 1 再補 connect widget）。
func (h *TerraHandler) Router() http.Handler {
	r := chi.NewRouter()
	r.Post("/webhook", h.WebhookEvent) // Terra 事件推播（公開）
	r.Group(func(r chi.Router) {
		r.Use(h.requireAuth)
		r.Get("/status", h.Status) // 連線狀態（Phase 1：實際連線 + widget）
	})
	return r
}

// Status Phase 0 佔位：回報是否已啟用（前台據此顯示「手錶直連」是可用或「即將開放」）。
func (h *TerraHandler) Status(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{"enabled": h.enabled(), "connected": false, "providers": []string{"garmin", "coros"}})
}

// --- Terra webhook payload（僅取落地所需欄位；其餘忽略）---

type terraPayload struct {
	Type string `json:"type"` // activity | auth | deauth | daily | sleep | ...
	User struct {
		UserID      string `json:"user_id"`      // Terra 端 user id
		ReferenceID string `json:"reference_id"` // 我方 user id（connect 時帶入）
		Provider    string `json:"provider"`     // GARMIN | COROS | STRAVA | ...
	} `json:"user"`
	Data []terraActivity `json:"data"`
}

type terraActivity struct {
	Metadata struct {
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
		SummaryID string `json:"summary_id"`
		Type      int    `json:"type"` // Terra 運動類型 enum（跑步等；正式接入時據此過濾）
		Name      string `json:"name"`
	} `json:"metadata"`
	DistanceData struct {
		Summary struct {
			DistanceMetres float64 `json:"distance_metres"`
			Elevation      struct {
				GainActualMetres *float64 `json:"gain_actual_metres"`
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
}

// providerToSource 把 Terra 的 provider 轉成我方 activities.source（小寫品牌）。
func providerToSource(p string) string {
	switch strings.ToUpper(strings.TrimSpace(p)) {
	case "GARMIN":
		return "garmin"
	case "COROS":
		return "coros"
	case "STRAVA":
		return providerStrava
	default:
		return strings.ToLower(strings.TrimSpace(p))
	}
}

func (h *TerraHandler) WebhookEvent(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	// 先 ack（Terra 期望 2xx，否則重送）
	defer func() { w.WriteHeader(http.StatusOK) }()

	if !h.enabled() {
		return // 尚未啟用：只 ack、不處理
	}
	if !h.verifySignature(r.Header.Get("terra-signature"), body) {
		log.Warn().Msg("terra webhook: signature verify failed")
		return
	}
	var p terraPayload
	if err := json.Unmarshal(body, &p); err != nil {
		log.Warn().Err(err).Msg("terra webhook: bad json")
		return
	}
	if p.Type != "activity" {
		return // 只處理活動事件（auth/daily/sleep 等忽略）
	}
	userID := strings.TrimSpace(p.User.ReferenceID)
	source := providerToSource(p.User.Provider)
	if userID == "" || source == "" {
		return
	}
	for i := range p.Data {
		h.importTerra(r, userID, source, &p.Data[i])
	}
}

func (h *TerraHandler) importTerra(r *http.Request, userID, source string, a *terraActivity) {
	distM := a.DistanceData.Summary.DistanceMetres
	durS := int(math.Round(a.ActiveDurationsData.ActivitySeconds))
	// TODO(P1)：依 metadata.type 過濾「跑步」類型；目前先以「有距離+有時間」粗篩，避免把騎車等計入。
	if distM <= 0 || durS <= 0 {
		return
	}
	recordedAt, err := time.Parse(time.RFC3339, a.Metadata.StartTime)
	if err != nil {
		return
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
	if g := a.DistanceData.Summary.Elevation.GainActualMetres; g != nil && *g > 0 {
		na.AscentM = g
	}
	if hr := a.HeartRateData.Summary.AvgHrBpm; hr != nil && *hr > 0 {
		v := int(math.Round(*hr))
		na.AvgHR = &v
	}
	if _, err := h.repo.ImportActivity(r.Context(), na); err != nil {
		log.Error().Err(err).Str("source", source).Msg("terra import activity failed")
	}
}

// _ = auth：Phase 1 connect widget 會用到 auth ctx；此檔 Phase 0 尚未使用。

// verifySignature 驗證 Terra 的 terra-signature 標頭：格式 "t=<ts>,v1=<hmac_sha256(ts.body)>"。
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
	mac := hmac.New(sha256.New, []byte(h.cfg.SigningSecret))
	mac.Write([]byte(ts + "." + string(body)))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(v1))
}
