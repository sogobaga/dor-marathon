package activityreward

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
)

// uuidRE 驗證路徑帶入的 id 是否為合法 UUID 格式（比照 internal/rewardserial 慣例），避免把非 UUID
// 字串丟給 Postgres 的 uuid 欄位比較。
var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isValidUUID(s string) bool { return uuidRE.MatchString(s) }

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// TemplateRouter 全域即時獎勵模板 CRUD。掛載在 /api/v1/admin/reward-templates（perm("rewards")）。
func (h *Handler) TemplateRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.ListTemplates)
	r.Post("/", h.CreateTemplate)
	r.Put("/{id}", h.UpdateTemplate)
	r.Delete("/{id}", h.DeleteTemplate)
	return r
}

func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := h.svc.ListTemplates(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list templates")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"templates": templates})
}

type templateReq struct {
	Name  string       `json:"name"`
	Items []RewardItem `json:"items"`
}

func (h *Handler) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	var req templateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	t, err := h.svc.CreateTemplate(r.Context(), req.Name, req.Items)
	if err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"template": t})
}

func (h *Handler) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondErr(w, http.StatusBadRequest, "id is invalid")
		return
	}
	var req templateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	t, err := h.svc.UpdateTemplate(r.Context(), id, req.Name, req.Items)
	if errors.Is(err, ErrNotFound) {
		respondErr(w, http.StatusNotFound, "template not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"template": t})
}

func (h *Handler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondErr(w, http.StatusBadRequest, "id is invalid")
		return
	}
	err := h.svc.DeleteTemplate(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		respondErr(w, http.StatusNotFound, "template not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to delete template")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- 活動優惠券券種（migration 138）---

// CouponDefRouter 活動優惠券券種 CRUD。掛載在 /api/v1/admin/event-coupons（perm("rewards")）。
func (h *Handler) CouponDefRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.ListCouponDefs)
	r.Post("/", h.CreateCouponDef)
	r.Put("/{id}", h.UpdateCouponDef)
	r.Delete("/{id}", h.DeleteCouponDef)
	return r
}

func (h *Handler) ListCouponDefs(w http.ResponseWriter, r *http.Request) {
	defs, err := h.svc.ListCouponDefs(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list coupon defs")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"defs": defs})
}

func (h *Handler) CreateCouponDef(w http.ResponseWriter, r *http.Request) {
	var in CouponDefInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	d, err := h.svc.CreateCouponDef(r.Context(), in)
	if err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"def": d})
}

func (h *Handler) UpdateCouponDef(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondErr(w, http.StatusBadRequest, "id is invalid")
		return
	}
	var in CouponDefInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	d, err := h.svc.UpdateCouponDef(r.Context(), id, in)
	if errors.Is(err, ErrNotFound) {
		respondErr(w, http.StatusNotFound, "coupon def not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"def": d})
}

func (h *Handler) DeleteCouponDef(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondErr(w, http.StatusBadRequest, "id is invalid")
		return
	}
	err := h.svc.DeleteCouponDef(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		respondErr(w, http.StatusNotFound, "coupon def not found")
		return
	}
	if errors.Is(err, ErrCouponDefInUse) {
		respondErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to delete coupon def")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
