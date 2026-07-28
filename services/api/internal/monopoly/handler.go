package monopoly

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

// Router 掛 /monopoly（需登入，由呼叫端的 RequireAuth 群組保證）。
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/state", h.State)
	r.Post("/roll", h.Roll)
	r.Get("/knowledge", h.Knowledge)
	return r
}

// GET /api/v1/monopoly/state
func (h *Handler) State(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	st, err := h.svc.GetState(r.Context(), uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get monopoly state")
		return
	}
	respondJSON(w, http.StatusOK, st)
}

// POST /api/v1/monopoly/roll —— 扣 GP、伺服器決定點數、移動棋子；GP 不足直接 409、絕不扣款。
func (h *Handler) Roll(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	res, err := h.svc.Roll(r.Context(), uid)
	if errors.Is(err, ErrInsufficientGP) {
		respondErr(w, http.StatusConflict, "GP 不足")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "擲骰失敗，請稍後再試")
		return
	}
	respondJSON(w, http.StatusOK, res)
}

// GET /api/v1/monopoly/knowledge —— 知識卡圖鑑（供 Phase 2a 圖鑑頁）；防劇透邏輯見
// Repository.GetKnowledgeCards：未擁有的卡片只回 id/theme/main_category/rarity/owned/obtained_count。
func (h *Handler) Knowledge(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	gallery, err := h.svc.GetKnowledgeCards(r.Context(), uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get knowledge cards")
		return
	}
	respondJSON(w, http.StatusOK, gallery)
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
