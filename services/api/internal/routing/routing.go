// Package routing：跑步路線建議代理。以 OpenRouteService 的 foot-walking（行人）設定檔規劃
// 「目前位置 → 打卡點」的建議路線——foot-walking 天生走人行道/步道/巷弄、避開高速/快速道路/
// 禁行人的車道（跑者友善的近似；無法保證 100% 避開所有主觀「危險」路段）。
// API key 放後端（env ORS_API_KEY），不外露；未設定時端點回 503 讓前端優雅隱藏此功能。
package routing

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dor/api/internal/auth"
)

type Handler struct {
	apiKey string
	client *http.Client
}

func NewHandler(apiKey string) *Handler {
	return &Handler{apiKey: strings.TrimSpace(apiKey), client: &http.Client{Timeout: 12 * time.Second}}
}

func (h *Handler) Router() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.Plan)
	return r
}

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func respondErr(w http.ResponseWriter, code int, msg string) {
	respondJSON(w, code, map[string]string{"error": msg})
}

// parseLatLng 解析 "lat,lng" 字串 → 經緯度（含基本範圍驗證）
func parseLatLng(s string) (lat, lng float64, ok bool) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return 0, 0, false
	}
	lat, e1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lng, e2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if e1 != nil || e2 != nil || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return 0, 0, false
	}
	return lat, lng, true
}

// Plan — GET /api/v1/route?from=lat,lng&to=lat,lng
// 回 { distance_m, duration_s, coords: [[lat,lng],...] }（coords 直接給 Leaflet polyline 畫線）。
func (h *Handler) Plan(w http.ResponseWriter, r *http.Request) {
	if uid, _ := r.Context().Value(auth.CtxKeyUserID).(string); uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	if h.apiKey == "" {
		respondErr(w, http.StatusServiceUnavailable, "路線規劃尚未設定（缺 ORS_API_KEY）")
		return
	}
	fromLat, fromLng, ok1 := parseLatLng(r.URL.Query().Get("from"))
	toLat, toLng, ok2 := parseLatLng(r.URL.Query().Get("to"))
	if !ok1 || !ok2 {
		respondErr(w, http.StatusBadRequest, "from/to 座標格式錯誤（需 lat,lng）")
		return
	}

	// ORS foot-walking（行人）：走人行道/步道/巷弄、避開高速/快速道路/禁行人車道。
	// radiuses -1 = 不限吸附距離（避免起訖點剛好不在路上就直接失敗）。ORS 用 [lng,lat] 順序。
	reqBody, _ := json.Marshal(map[string]any{
		"coordinates": [][]float64{{fromLng, fromLat}, {toLng, toLat}},
		"radiuses":    []float64{-1, -1},
	})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"https://api.openrouteservice.org/v2/directions/foot-walking/geojson", bytes.NewReader(reqBody))
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	req.Header.Set("Authorization", h.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		respondErr(w, http.StatusBadGateway, "路線服務暫時無法使用，請稍後再試")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// ORS 常見 4xx：起訖過遠、該處無步道資料、或超出免費配額
		respondErr(w, http.StatusBadGateway, "無法規劃路線（可能距離過遠、或該處缺步道資料）")
		return
	}

	var ors struct {
		Features []struct {
			Geometry struct {
				Coordinates [][]float64 `json:"coordinates"`
			} `json:"geometry"`
			Properties struct {
				Summary struct {
					Distance float64 `json:"distance"`
					Duration float64 `json:"duration"`
				} `json:"summary"`
			} `json:"properties"`
		} `json:"features"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ors); err != nil || len(ors.Features) == 0 {
		respondErr(w, http.StatusBadGateway, "路線解析失敗")
		return
	}
	src := ors.Features[0].Geometry.Coordinates
	coords := make([][2]float64, 0, len(src))
	for _, c := range src {
		if len(c) >= 2 {
			coords = append(coords, [2]float64{c[1], c[0]}) // ORS [lng,lat] → 前端/Leaflet [lat,lng]
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"distance_m": ors.Features[0].Properties.Summary.Distance,
		"duration_s": ors.Features[0].Properties.Summary.Duration,
		"coords":     coords,
	})
}
