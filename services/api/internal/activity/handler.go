package activity

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/dor/api/internal/auth"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Router 回傳活動相關路由（掛載在 /api/v1/activities）
//
// 2026-09-07 audit：舊有 POST "/"（self-report 上傳，見已移除的 Handler.Upload/Service.Upload）
// 已移除——grep 全前端確認零呼叫端，卻仍會餵給 worker 一筆 source=NULL、完全不設防（無 GPS 重算/
// 無防弊）的「無旗標」活動，等同讓任何人繞過 GPS 軌跡直接自報里程。真正的上傳路徑只剩
// POST /gps（伺服器端重算+防弊，見 gps.go SaveGPSRun）與後台 AdminAddMileage（測試/補償用，
// 有明確操作者與稽核軌跡）。
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Post("/gps", h.UploadGPS)
	r.Get("/gps/history", h.GPSHistory)
	r.Get("/gps/{id}", h.GPSDetail)
	r.Get("/me", h.MyActivities)
	r.Get("/me/race/{raceID}", h.MyRaceActivities)
	r.Get("/missions/{raceID}", h.MissionStatus)
	return r
}

// POST /api/v1/admin/activities/add-mileage — 後台模擬加里程（測試用）
func (h *Handler) AdminAddMileage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID     string  `json:"user_id"`
		DistanceKm float64 `json:"distance_km"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if body.UserID == "" || body.DistanceKm <= 0 {
		http.Error(w, `{"error":"user_id 與 distance_km 必填"}`, http.StatusBadRequest)
		return
	}
	if err := h.svc.AdminAddMileage(r.Context(), body.UserID, body.DistanceKm); err != nil {
		http.Error(w, `{"error":"failed"}`, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// GET /api/v1/activities/me?limit=20
func (h *Handler) MyActivities(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	acts, err := h.svc.ListByUser(r.Context(), userID, limit)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list activities")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"activities": acts, "count": len(acts)})
}

// GET /api/v1/activities/me/race/:raceID
func (h *Handler) MyRaceActivities(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	raceID := chi.URLParam(r, "raceID")

	acts, err := h.svc.ListByRace(r.Context(), userID, raceID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list race activities")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"activities": acts, "count": len(acts)})
}

// GET /api/v1/activities/missions/:raceID
// 取得使用者在某賽事的任務完成狀態（day → rescue_count）
func (h *Handler) MissionStatus(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	raceID := chi.URLParam(r, "raceID")

	completions, err := h.svc.GetMissionStatus(r.Context(), userID, raceID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get mission status")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"race_id":     raceID,
		"completions": completions,
	})
}

// ---

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
