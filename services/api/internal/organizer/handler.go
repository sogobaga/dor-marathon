package organizer

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/middleware"
	"github.com/dor/api/internal/race"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// OrganizerRouter 合作方專屬路由（需登入 + organizer 角色）
// 掛載在 /api/v1/organizer
func (h *Handler) OrganizerRouter() http.Handler {
	r := chi.NewRouter()

	// Profile 管理
	r.Get("/profile", h.GetProfile)
	r.Put("/profile", h.UpsertProfile)

	// 儀錶板
	r.Get("/dashboard", h.Dashboard)

	// 賽事管理（合作方視角）
	r.Get("/races", h.ListRaces)
	r.Post("/races", h.SubmitRace)
	r.Get("/races/{raceID}", h.GetRaceDetail)

	return r
}

// AdminOrganizerRouter admin 管理合作方與審核
// 掛載在 /api/v1/admin/organizer
func (h *Handler) AdminOrganizerRouter() http.Handler {
	r := chi.NewRouter()

	// 合作方管理
	r.Get("/", h.AdminListOrganizers)
	r.Post("/{userID}/verify", h.AdminVerifyOrganizer)

	// 賽事審核
	r.Get("/races/pending", h.AdminListPending)
	r.Post("/races/{raceID}/review", h.AdminReviewRace)

	return r
}

// --- 合作方端點 ---

// GET /api/v1/organizer/profile
func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	p, err := h.svc.GetProfile(r.Context(), userID)
	if errors.Is(err, ErrProfileNotFound) {
		respondErr(w, http.StatusNotFound, "profile not found, please complete your organizer profile first")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get profile")
		return
	}
	respondJSON(w, http.StatusOK, p)
}

// PUT /api/v1/organizer/profile
func (h *Handler) UpsertProfile(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var p Profile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if p.CompanyName == "" {
		respondErr(w, http.StatusBadRequest, "company_name is required")
		return
	}
	if err := h.svc.UpsertProfile(r.Context(), userID, &p); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save profile")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GET /api/v1/organizer/dashboard
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	d, err := h.svc.GetDashboard(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get dashboard")
		return
	}
	respondJSON(w, http.StatusOK, d)
}

// GET /api/v1/organizer/races
func (h *Handler) ListRaces(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	races, err := h.svc.ListMyRaces(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list races")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"races": races, "count": len(races)})
}

// POST /api/v1/organizer/races
func (h *Handler) SubmitRace(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)

	var raceData race.Race
	if err := json.NewDecoder(r.Body).Decode(&raceData); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if raceData.Slug == "" || raceData.Title == "" || len(raceData.Distances) == 0 {
		respondErr(w, http.StatusBadRequest, "slug, title, distances are required")
		return
	}
	raceData.CreatedBy = userID

	created, err := h.svc.SubmitRace(r.Context(), userID, &raceData)
	if errors.Is(err, ErrNotVerified) {
		respondErr(w, http.StatusForbidden, "your organizer account has not been verified by the platform yet")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to submit race")
		return
	}

	respondJSON(w, http.StatusCreated, map[string]any{
		"race":    created,
		"message": "Race submitted for review. You will be notified once approved.",
	})
}

// GET /api/v1/organizer/races/:raceID
func (h *Handler) GetRaceDetail(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	raceID := chi.URLParam(r, "raceID")

	raceDetail, signups, err := h.svc.GetMyRaceDetail(r.Context(), userID, raceID)
	if errors.Is(err, ErrNotOwner) {
		respondErr(w, http.StatusForbidden, "this race does not belong to you")
		return
	}
	if errors.Is(err, race.ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get race detail")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"race":    raceDetail,
		"signups": signups,
		"count":   len(signups),
	})
}

// --- Admin 端點 ---

// GET /api/v1/admin/organizer
func (h *Handler) AdminListOrganizers(w http.ResponseWriter, r *http.Request) {
	orgs, err := h.svc.ListOrganizers(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list organizers")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"organizers": orgs, "count": len(orgs)})
}

// POST /api/v1/admin/organizer/:userID/verify
// Body: { "verified": true }
func (h *Handler) AdminVerifyOrganizer(w http.ResponseWriter, r *http.Request) {
	targetUserID := chi.URLParam(r, "userID")
	reviewerID := middleware.GetUserID(r.Context())

	var req struct {
		Verified bool `json:"verified"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}

	if err := h.svc.VerifyOrganizer(r.Context(), targetUserID, req.Verified); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to update verification")
		return
	}

	_ = reviewerID // 可記錄到 audit_logs（Phase 4）
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/admin/organizer/races/pending
func (h *Handler) AdminListPending(w http.ResponseWriter, r *http.Request) {
	races, err := h.svc.ListPendingRaces(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list pending races")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"races": races, "count": len(races)})
}

// POST /api/v1/admin/organizer/races/:raceID/review
// Body: { "action": "approve" | "reject", "note": "..." }
func (h *Handler) AdminReviewRace(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	reviewerID := middleware.GetUserID(r.Context())

	var req struct {
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Action == "reject" && req.Note == "" {
		respondErr(w, http.StatusBadRequest, "note is required when rejecting")
		return
	}

	if err := h.svc.ReviewRace(r.Context(), raceID, req.Action, req.Note, reviewerID); err != nil {
		if err.Error() == "action must be 'approve' or 'reject'" {
			respondErr(w, http.StatusBadRequest, err.Error())
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to review race")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"action":  req.Action,
		"race_id": raceID,
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
