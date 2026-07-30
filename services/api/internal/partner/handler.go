package partner

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"github.com/dor/api/internal/auth"
)

// uuidRE 驗證路徑/body 帶入的 id 是否為合法 UUID 格式；不合法直接擋掉，
// 避免把非 UUID 字串丟給 Postgres 的 uuid 欄位比較（型別錯誤 → 500 + log 噪音）。
var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isValidUUID(s string) bool {
	return uuidRE.MatchString(s)
}

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// PublicRouter 前台商家目錄（OptionalAuth：未登入也能看，登入才有 is_favorited）。
// 掛載在 /api/v1/partner-shops。
func (h *Handler) PublicRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Get("/{id}", h.Detail)
	return r
}

// FavoriteRouter 收藏（需登入）。掛載在 /api/v1/profile/partner-favorites。
func (h *Handler) FavoriteRouter() http.Handler {
	r := chi.NewRouter()
	r.Post("/", h.AddFavorite)
	r.Delete("/{shopID}", h.RemoveFavorite)
	return r
}

// AdminRouter 後台管理（RequireAuth + RequireAdmin + RequirePerm("partners")）。
// 掛載在 /api/v1/admin/partner-shops。
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminList)
	r.Post("/", h.AdminCreate)
	r.Put("/{id}", h.AdminUpdate)
	r.Delete("/{id}", h.AdminDelete)
	// VIP 精選解鎖門檻（累積里程 km）；靜態路徑，chi 依 radix tree 比對，不會被上面的 {id} 攔截。
	r.Get("/vip-featured-min-km", h.AdminGetMinKm)
	r.Put("/vip-featured-min-km", h.AdminSetMinKm)
	return r
}

// --- 前台端點 ---

// GET /api/v1/partner-shops
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	shops, meta, err := h.svc.ListEnabled(r.Context(), uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list partner shops")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"shops": shops, "meta": meta})
}

// GET /api/v1/partner-shops/{id}
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondErr(w, http.StatusNotFound, "partner shop not found")
		return
	}
	shop, err := h.svc.GetDetail(r.Context(), id, uid)
	if errors.Is(err, ErrNotFound) {
		respondErr(w, http.StatusNotFound, "partner shop not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get partner shop")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"shop": shop})
}

// --- 收藏端點 ---

// POST /api/v1/profile/partner-favorites  body {"shop_id":"..."}
func (h *Handler) AddFavorite(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var body struct {
		ShopID string `json:"shop_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ShopID == "" {
		respondErr(w, http.StatusBadRequest, "shop_id is required")
		return
	}
	if !isValidUUID(body.ShopID) {
		respondErr(w, http.StatusBadRequest, "shop_id is invalid")
		return
	}
	if err := h.svc.AddFavorite(r.Context(), userID, body.ShopID); err != nil {
		if errors.Is(err, ErrNotFound) {
			respondErr(w, http.StatusNotFound, "partner shop not found")
			return
		}
		respondErr(w, http.StatusBadRequest, "收藏失敗（商家不存在？）")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DELETE /api/v1/profile/partner-favorites/{shopID}
func (h *Handler) RemoveFavorite(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	shopID := chi.URLParam(r, "shopID")
	if !isValidUUID(shopID) {
		respondErr(w, http.StatusBadRequest, "shopID is invalid")
		return
	}
	if err := h.svc.RemoveFavorite(r.Context(), userID, shopID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to remove favorite")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- 後台端點 ---

// GET /api/v1/admin/partner-shops
func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	shops, err := h.svc.AdminList(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list partner shops")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"shops": shops})
}

// POST /api/v1/admin/partner-shops
func (h *Handler) AdminCreate(w http.ResponseWriter, r *http.Request) {
	var req AdminPartnerShopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	shop, err := h.svc.AdminCreate(r.Context(), &req)
	if errors.Is(err, ErrNameRequired) || errors.Is(err, ErrInvalidURL) || errors.Is(err, ErrTooLong) || errors.Is(err, ErrInvalidAudience) {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to create partner shop")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"shop": shop})
}

// PUT /api/v1/admin/partner-shops/{id}
func (h *Handler) AdminUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req AdminPartnerShopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	shop, err := h.svc.AdminUpdate(r.Context(), id, &req)
	if errors.Is(err, ErrNotFound) {
		respondErr(w, http.StatusNotFound, "partner shop not found")
		return
	}
	if errors.Is(err, ErrNameRequired) || errors.Is(err, ErrInvalidURL) || errors.Is(err, ErrTooLong) || errors.Is(err, ErrInvalidAudience) {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to update partner shop")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"shop": shop})
}

// GET /api/v1/admin/partner-shops/vip-featured-min-km
func (h *Handler) AdminGetMinKm(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{"min_km": h.svc.MinVIPFeaturedKm(r.Context())})
}

// PUT /api/v1/admin/partner-shops/vip-featured-min-km  body {"min_km": 10}
func (h *Handler) AdminSetMinKm(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MinKm int `json:"min_km"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := h.svc.SetMinVIPFeaturedKm(r.Context(), body.MinKm); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"min_km": body.MinKm})
}

// DELETE /api/v1/admin/partner-shops/{id}
func (h *Handler) AdminDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.AdminDelete(r.Context(), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			respondErr(w, http.StatusNotFound, "partner shop not found")
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to delete partner shop")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
