package reward

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/dor/api/internal/auth"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Router 獎勵路由（掛載在 /api/v1/rewards）
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/quota/{raceID}", h.SpinQuota)
	r.Post("/spin/{raceID}", h.Spin)
	r.Get("/stickers/{raceID}", h.Stickers)
	return r
}

// GET /api/v1/rewards/quota/:raceID
func (h *Handler) SpinQuota(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	raceID := chi.URLParam(r, "raceID")

	remaining, total, err := h.svc.SpinQuota(r.Context(), userID, raceID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get spin quota")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"race_id":   raceID,
		"total":     total,
		"remaining": remaining,
		"used":      total - remaining,
	})
}

// POST /api/v1/rewards/spin/:raceID
func (h *Handler) Spin(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	raceID := chi.URLParam(r, "raceID")

	result, err := h.svc.Spin(r.Context(), userID, raceID)
	if errors.Is(err, ErrNoSpinsLeft) {
		respondErr(w, http.StatusForbidden, "no spins remaining, complete missions to earn more")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "spin failed")
		return
	}

	respondJSON(w, http.StatusOK, result)
}

// GET /api/v1/rewards/stickers/:raceID
func (h *Handler) Stickers(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	raceID := chi.URLParam(r, "raceID")

	card, err := h.svc.GetStickerCard(r.Context(), userID, raceID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get sticker card")
		return
	}
	respondJSON(w, http.StatusOK, card)
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
