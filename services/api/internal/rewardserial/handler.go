package rewardserial

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// uuidRE 驗證路徑帶入的 id 是否為合法 UUID 格式，避免把非 UUID 字串丟給 Postgres 的 uuid 欄位比較。
var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isValidUUID(s string) bool { return uuidRE.MatchString(s) }

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// MerchantRouter 合作商家 CRUD。掛載在 /api/v1/admin/reward-merchants（perm("rewards")）。
func (h *Handler) MerchantRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.ListMerchants)
	r.Post("/", h.CreateMerchant)
	r.Put("/{id}", h.UpdateMerchant)
	r.Delete("/{id}", h.DeleteMerchant)
	return r
}

// GroupRouter 序號組 CRUD + 序號匯入/清單/註銷。掛載在 /api/v1/admin/reward-groups（perm("rewards")）。
func (h *Handler) GroupRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.ListGroups)
	r.Post("/", h.CreateGroup)
	r.Put("/{id}", h.UpdateGroup)
	r.Delete("/{id}", h.DeleteGroup)
	r.Get("/{id}/serials", h.ListSerials)
	r.Post("/{id}/serials/import", h.ImportSerials)
	r.Put("/{id}/serials/{serialID}/void", h.VoidSerial)
	r.Post("/{id}/serials/void-batch", h.VoidSerialsBatch)
	r.Post("/{id}/serials/delete", h.DeleteSerials)
	return r
}

// --- 合作商家 ---

func (h *Handler) ListMerchants(w http.ResponseWriter, r *http.Request) {
	merchants, err := h.svc.ListMerchants(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list merchants")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"merchants": merchants})
}

type merchantReq struct {
	Name string `json:"name"`
	Note string `json:"note"`
}

func (h *Handler) CreateMerchant(w http.ResponseWriter, r *http.Request) {
	var req merchantReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	m, err := h.svc.CreateMerchant(r.Context(), req.Name, req.Note)
	if err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"merchant": m})
}

func (h *Handler) UpdateMerchant(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondErr(w, http.StatusBadRequest, "id is invalid")
		return
	}
	var req merchantReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	m, err := h.svc.UpdateMerchant(r.Context(), id, req.Name, req.Note)
	if errors.Is(err, ErrNotFound) {
		respondErr(w, http.StatusNotFound, "merchant not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"merchant": m})
}

func (h *Handler) DeleteMerchant(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondErr(w, http.StatusBadRequest, "id is invalid")
		return
	}
	err := h.svc.DeleteMerchant(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		respondErr(w, http.StatusNotFound, "merchant not found")
		return
	}
	if errors.Is(err, ErrMerchantInUse) {
		respondErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to delete merchant")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- 序號組 ---

func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.svc.ListGroups(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list groups")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var in GroupInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	g, err := h.svc.CreateGroup(r.Context(), in)
	if err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"group": g})
}

func (h *Handler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondErr(w, http.StatusBadRequest, "id is invalid")
		return
	}
	var in GroupInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	g, err := h.svc.UpdateGroup(r.Context(), id, in)
	if errors.Is(err, ErrNotFound) {
		respondErr(w, http.StatusNotFound, "group not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"group": g})
}

func (h *Handler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondErr(w, http.StatusBadRequest, "id is invalid")
		return
	}
	err := h.svc.DeleteGroup(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		respondErr(w, http.StatusNotFound, "group not found")
		return
	}
	if errors.Is(err, ErrGroupInUseAsBundleChild) {
		respondErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to delete group")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- 序號 ---

func (h *Handler) ListSerials(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "id")
	if !isValidUUID(groupID) {
		respondErr(w, http.StatusBadRequest, "id is invalid")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	serials, total, err := h.svc.ListSerials(r.Context(), groupID, r.URL.Query().Get("status"), limit, offset)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list serials")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"serials": serials, "count": total})
}

type importReq struct {
	Serials []ImportInput `json:"serials"`
}

func (h *Handler) ImportSerials(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "id")
	if !isValidUUID(groupID) {
		respondErr(w, http.StatusBadRequest, "id is invalid")
		return
	}
	var req importReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	res, err := h.svc.ImportSerials(r.Context(), groupID, req.Serials)
	if err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, res)
}

func (h *Handler) VoidSerial(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "id")
	serialID := chi.URLParam(r, "serialID")
	if !isValidUUID(groupID) || !isValidUUID(serialID) {
		respondErr(w, http.StatusBadRequest, "id is invalid")
		return
	}
	err := h.svc.VoidSerial(r.Context(), groupID, serialID)
	if errors.Is(err, ErrNotFound) {
		respondErr(w, http.StatusNotFound, "serial not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to void serial")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// VoidSerialsBatch 批次註銷（前端複選工具列用）；單筆註銷仍走既有 VoidSerial（PUT .../void）不變。
func (h *Handler) VoidSerialsBatch(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "id")
	if !isValidUUID(groupID) {
		respondErr(w, http.StatusBadRequest, "id is invalid")
		return
	}
	var req SerialIDsInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	res, err := h.svc.VoidSerialsBatch(r.Context(), groupID, req.IDs)
	if err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, res)
}

// DeleteSerials 真刪除序號（單筆亦透過此端點，ids 傳 1 個即可）。安全邊界（status IN
// ('available','void') 才可刪、issued 一律拒絕）在 Service/Repository 層強制，見 Service.DeleteSerials。
func (h *Handler) DeleteSerials(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "id")
	if !isValidUUID(groupID) {
		respondErr(w, http.StatusBadRequest, "id is invalid")
		return
	}
	var req SerialIDsInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	res, err := h.svc.DeleteSerials(r.Context(), groupID, req.IDs)
	if err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, res)
}

// --- helpers ---

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
