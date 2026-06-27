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
	r.Get("/{raceID}/standings", h.Standings)
	r.Get("/{raceID}/status", h.LiveStatus)
	return r
}

// GET /api/v1/races/:raceID/standings — 競賽分組排行榜（公開；帶 token 則附自己分組名次）
func (h *Handler) Standings(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)

	ranking, err := h.svc.GetCompetitionRanking(r.Context(), raceID, userID)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get standings")
		return
	}
	respondJSON(w, http.StatusOK, ranking)
}

// AdminRouter 回傳管理員路由
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListRaces)
	r.Post("/", h.AdminCreateRace)
	r.Get("/{raceID}", h.AdminGetRace)
	r.Put("/{raceID}", h.AdminUpdateRace)
	r.Delete("/{raceID}", h.AdminDeleteRace)
	r.Patch("/{raceID}/status", h.AdminUpdateStatus)
	r.Get("/{raceID}/signups", h.AdminListSignups)
	return r
}

// PresetRouter 分組預設選單路由（掛載在 /api/v1/admin/group-presets）
func (h *Handler) PresetRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListPresets)
	r.Post("/", h.AdminCreatePreset)
	return r
}

// GET /api/v1/admin/group-presets
func (h *Handler) AdminListPresets(w http.ResponseWriter, r *http.Request) {
	presets, err := h.svc.ListPresets(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list presets")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"presets": presets})
}

// POST /api/v1/admin/group-presets
func (h *Handler) AdminCreatePreset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name              string   `json:"name"`
		DefaultDistanceKm *float64 `json:"default_distance_km"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		respondErr(w, http.StatusBadRequest, "name is required")
		return
	}
	preset, err := h.svc.CreatePreset(r.Context(), req.Name, req.DefaultDistanceKm)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to create preset")
		return
	}
	respondJSON(w, http.StatusCreated, map[string]any{"preset": preset})
}

// GET /api/v1/races?status=open
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status") // open|live|soon|done|（空 = 全部）
	races, err := h.svc.List(r.Context(), status)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list races")
		return
	}

	resp := map[string]any{"races": races}
	// 登入時附帶使用者各賽事報名狀態（race_id → {status, group_revealed}）
	if userID, _ := r.Context().Value(auth.CtxKeyUserID).(string); userID != "" {
		if regs, err := h.svc.GetUserRegistrations(r.Context(), userID); err == nil {
			resp["registrations"] = regs
		}
	}
	respondJSON(w, http.StatusOK, resp)
}

// GET /api/v1/races/:raceID — 公開賽事詳情（含分組/加購/物資）+ 報名狀態
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)

	detail, reg, err := h.svc.GetPublicDetail(r.Context(), raceID, userID)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get race")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"race":         detail,
		"registration": reg, // nil if not registered
	})
}

// POST /api/v1/races/:raceID/register — 前台報名（分組 + 加購 + 訂單）
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.RaceID = raceID
	req.UserID = userID

	result, err := h.svc.Register(r.Context(), &req)
	switch {
	case err == nil:
		respondJSON(w, http.StatusCreated, result)
	case errors.Is(err, ErrRaceNotFound):
		respondErr(w, http.StatusNotFound, "race not found")
	case errors.Is(err, ErrRegistrationClosed):
		respondErr(w, http.StatusConflict, "報名未開放")
	case errors.Is(err, ErrAlreadyRegistered):
		respondErr(w, http.StatusConflict, "您已報名此賽事")
	case errors.Is(err, ErrGroupFull):
		respondErr(w, http.StatusConflict, "該分組名額已滿")
	case errors.Is(err, ErrAddonSoldOut):
		respondErr(w, http.StatusConflict, "加購商品已售完")
	case errors.Is(err, ErrGroupRequired), errors.Is(err, ErrGroupNotFound),
		errors.Is(err, ErrNoGroups), errors.Is(err, ErrMissingRequiredField),
		errors.Is(err, ErrGroupRestriction), errors.Is(err, ErrAddonNotFound),
		errors.Is(err, ErrAddonLimit):
		respondErr(w, http.StatusBadRequest, err.Error())
	default:
		respondErr(w, http.StatusInternalServerError, "registration failed")
	}
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

// POST /api/v1/admin/races — 建立賽事（含巢狀分組/加購/物資）
func (h *Handler) AdminCreateRace(w http.ResponseWriter, r *http.Request) {
	var req CreateRaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Slug == "" || req.Title == "" {
		respondErr(w, http.StatusBadRequest, "slug, title are required")
		return
	}

	detail, err := h.svc.CreateRaceFull(r.Context(), &req)
	if err != nil {
		// 驗證類錯誤回 400，其餘 500
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]any{"race": detail})
}

// GET /api/v1/admin/races/:raceID — 管理員取得單一賽事（含巢狀子資料），供編輯表單載入
func (h *Handler) AdminGetRace(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	detail, err := h.svc.GetRaceDetail(r.Context(), raceID)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get race")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"race": detail})
}

// PUT /api/v1/admin/races/:raceID — 更新賽事（含巢狀分組/加購/物資）
func (h *Handler) AdminUpdateRace(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	var req CreateRaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Slug == "" || req.Title == "" {
		respondErr(w, http.StatusBadRequest, "slug, title are required")
		return
	}
	validStatuses := map[string]bool{"soon": true, "open": true, "live": true, "done": true}
	if req.Status != "" && !validStatuses[req.Status] {
		respondErr(w, http.StatusBadRequest, "invalid status")
		return
	}
	detail, err := h.svc.UpdateRaceFull(r.Context(), raceID, &req)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"race": detail})
}

// DELETE /api/v1/admin/races/:raceID — 刪除賽事（有報名則擋下）
func (h *Handler) AdminDeleteRace(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	err := h.svc.DeleteRace(r.Context(), raceID)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}
	if errors.Is(err, ErrRaceHasRegistrations) {
		respondErr(w, http.StatusConflict, "賽事已有報名，無法刪除")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to delete race")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
