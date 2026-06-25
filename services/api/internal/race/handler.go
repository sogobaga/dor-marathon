package race

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

// Router 回傳賽事相關路由（掛載在 /api/v1/races）
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Get("/{raceID}", h.Detail)
	r.Post("/{raceID}/register", h.Register)
	r.Get("/{raceID}/ranking", h.Ranking)
	r.Get("/{raceID}/status", h.LiveStatus)
	return r
}

// AdminRouter 回傳管理員路由
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListRaces)
	r.Post("/", h.AdminCreateRace)
	r.Patch("/{raceID}/status", h.AdminUpdateStatus)
	r.Get("/{raceID}/signups", h.AdminListSignups)
	return r
}

// GET /api/v1/races?status=open
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status") // open|live|soon|done|（空 = 全部）
	races, err := h.svc.List(r.Context(), status)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list races")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"races": races})
}

// GET /api/v1/races/:raceID
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)

	race, reg, err := h.svc.GetDetail(r.Context(), raceID, userID)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get race")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"race":         race,
		"registration": reg, // nil if not registered
	})
}

// POST /api/v1/races/:raceID/register
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}

	var req struct {
		Distance int `json:"distance" validate:"required"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Distance == 0 {
		respondErr(w, http.StatusBadRequest, "distance is required")
		return
	}

	reg, err := h.svc.Register(r.Context(), userID, raceID, req.Distance)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}
	if errors.Is(err, ErrRegistrationClosed) {
		respondErr(w, http.StatusConflict, "registration is not open")
		return
	}
	if errors.Is(err, ErrAlreadyRegistered) {
		respondErr(w, http.StatusConflict, "already registered")
		return
	}
	if errors.Is(err, ErrSoldOut) {
		respondErr(w, http.StatusConflict, "sold out")
		return
	}
	if errors.Is(err, ErrInvalidDistance) {
		respondErr(w, http.StatusBadRequest, "invalid distance")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "registration failed")
		return
	}

	respondJSON(w, http.StatusCreated, map[string]any{"registration": reg})
}

// GET /api/v1/races/:raceID/ranking?limit=100
func (h *Handler) Ranking(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)

	entries, err := h.svc.GetRanking(r.Context(), raceID, limit)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get ranking")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"race_id": raceID,
		"ranking": entries,
		"count":   len(entries),
	})
}

// GET /api/v1/races/:raceID/status
func (h *Handler) LiveStatus(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")

	status, err := h.svc.GetLiveStatus(r.Context(), raceID)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get status")
		return
	}

	respondJSON(w, http.StatusOK, status)
}

// --- Admin handlers ---

// GET /api/v1/admin/races
func (h *Handler) AdminListRaces(w http.ResponseWriter, r *http.Request) {
	races, err := h.svc.List(r.Context(), "")
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list races")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"races": races})
}

// POST /api/v1/admin/races
func (h *Handler) AdminCreateRace(w http.ResponseWriter, r *http.Request) {
	var race Race
	if err := json.NewDecoder(r.Body).Decode(&race); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if race.Slug == "" || race.Title == "" || len(race.Distances) == 0 {
		respondErr(w, http.StatusBadRequest, "slug, title, distances are required")
		return
	}
	race.Status = "soon" // 新建賽事預設 soon

	created, err := h.svc.CreateRace(r.Context(), &race)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to create race")
		return
	}
	respondJSON(w, http.StatusCreated, map[string]any{"race": created})
}

// PATCH /api/v1/admin/races/:raceID/status
func (h *Handler) AdminUpdateStatus(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	validStatuses := map[string]bool{"soon": true, "open": true, "live": true, "done": true}
	if !validStatuses[req.Status] {
		respondErr(w, http.StatusBadRequest, "invalid status")
		return
	}
	if err := h.svc.UpdateRaceStatus(r.Context(), raceID, req.Status); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to update status")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/admin/races/:raceID/signups
func (h *Handler) AdminListSignups(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	regs, err := h.svc.AdminListSignups(r.Context(), raceID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list signups")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"registrations": regs, "count": len(regs)})
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
