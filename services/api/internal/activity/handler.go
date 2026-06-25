package activity

import (
	"encoding/json"
	"errors"
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
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Post("/", h.Upload)
	r.Get("/me", h.MyActivities)
	r.Get("/me/race/{raceID}", h.MyRaceActivities)
	r.Get("/missions/{raceID}", h.MissionStatus)
	return r
}

// POST /api/v1/activities
// 上傳跑步資料 — 核心業務端點
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}

	var req UploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}

	// 基本欄位驗證
	if req.DistanceKm <= 0 || req.DurationS <= 0 || req.RecordedAt == "" {
		respondErr(w, http.StatusBadRequest, "distance_km, duration_s, recorded_at are required")
		return
	}

	result, err := h.svc.Upload(r.Context(), userID, &req)
	if errors.Is(err, ErrInvalidDistance) {
		respondErr(w, http.StatusBadRequest, "distance must be at least 0.1 km")
		return
	}
	if errors.Is(err, ErrInvalidPace) {
		respondErr(w, http.StatusBadRequest, "pace must be between 2:00–20:00 /km")
		return
	}
	if errors.Is(err, ErrFutureDate) {
		respondErr(w, http.StatusBadRequest, "recorded_at cannot be in the future")
		return
	}
	if errors.Is(err, ErrNotRegistered) {
		respondErr(w, http.StatusForbidden, "not registered for this race")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "upload failed: "+err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, result)
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
