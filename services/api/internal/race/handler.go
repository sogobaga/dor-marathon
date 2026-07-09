package race

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/promo"
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
	r.Post("/{raceID}/groups", h.CreateTeamGroup)
	r.Get("/{raceID}/ranking", h.Ranking)
	r.Get("/{raceID}/standings", h.Standings)
	r.Get("/{raceID}/progress", h.Progress)
	r.Get("/{raceID}/tasks/{taskID}/contributors", h.TaskContributors)
	r.Get("/{raceID}/tasks/{taskID}/range-detail", h.TaskRangeDetail)
	r.Get("/{raceID}/leaderboard", h.Leaderboard)
	r.Get("/{raceID}/certificate", h.Certificate)
	r.Get("/{raceID}/exp-breakdown", h.ExpBreakdown)
	r.Get("/{raceID}/status", h.LiveStatus)
	r.Post("/{raceID}/promo/check", h.PromoCheck)
	return r
}

// POST /api/v1/races/:raceID/promo/check — 報名前試算優惠序號（需登入）
func (h *Handler) PromoCheck(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var req struct {
		Code   string           `json:"code"`
		Addons []AddonSelection `json:"addons"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	quote, err := h.svc.QuotePromo(r.Context(), raceID, userID, req.Code, req.Addons)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to check promo")
		return
	}
	respondJSON(w, http.StatusOK, quote)
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
	r.Put("/{raceID}/certificate-bg", h.AdminSetCertificateBg)
	r.Put("/{raceID}/rank-display", h.AdminSetRankDisplay)
	r.Post("/{raceID}/settle-exp", h.AdminSettleEXP)
	r.Get("/{raceID}/signups", h.AdminListSignups)
	return r
}

// SignupRouter 後台報名管理路由（掛載在 /api/v1/admin/signups）
func (h *Handler) SignupRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListSignupRows)
	r.Patch("/{regID}/pay", h.AdminMarkRegistrationPaid)
	r.Patch("/{regID}/group", h.AdminChangeSignupGroup)
	return r
}

// OrderRouter 後台訂單管理路由（掛載在 /api/v1/admin/orders）
func (h *Handler) OrderRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListOrders)
	r.Get("/{orderID}", h.AdminGetOrder)
	r.Patch("/{orderID}/pay", h.AdminMarkOrderPaid)
	return r
}

// GET /api/v1/admin/signups?race_id=&q=
func (h *Handler) AdminListSignupRows(w http.ResponseWriter, r *http.Request) {
	raceID := r.URL.Query().Get("race_id")
	if raceID == "" {
		respondErr(w, http.StatusBadRequest, "race_id is required")
		return
	}
	rows, err := h.svc.ListSignups(r.Context(), raceID, r.URL.Query().Get("q"))
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list signups")
		return
	}
	groups, err := h.svc.ListRaceGroups(r.Context(), raceID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list groups")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"signups": rows, "count": len(rows), "groups": groups})
}

// PATCH /api/v1/admin/signups/{regID}/group  {group_id}
func (h *Handler) AdminChangeSignupGroup(w http.ResponseWriter, r *http.Request) {
	regID := chi.URLParam(r, "regID")
	var body struct {
		GroupID string `json:"group_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	err := h.svc.ChangeSignupGroup(r.Context(), regID, body.GroupID)
	switch {
	case errors.Is(err, ErrRegistrationNotFound):
		respondErr(w, http.StatusNotFound, "registration not found")
	case errors.Is(err, ErrGroupNotFound):
		respondErr(w, http.StatusBadRequest, "分組不存在或不屬於此賽事")
	case errors.Is(err, ErrGroupFull):
		respondErr(w, http.StatusConflict, "該分組已額滿，無法調整進入")
	case err != nil:
		respondErr(w, http.StatusInternalServerError, "failed to change group")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// PATCH /api/v1/admin/signups/{regID}/pay
func (h *Handler) AdminMarkRegistrationPaid(w http.ResponseWriter, r *http.Request) {
	regID := chi.URLParam(r, "regID")
	err := h.svc.MarkRegistrationPaid(r.Context(), regID)
	if errors.Is(err, ErrRegistrationNotFound) {
		respondErr(w, http.StatusNotFound, "registration not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to mark paid")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/admin/orders?race_id=&status=
func (h *Handler) AdminListOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := h.svc.ListOrders(r.Context(),
		r.URL.Query().Get("race_id"), r.URL.Query().Get("status"), 100, 0)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list orders")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"orders": orders, "count": len(orders)})
}

// GET /api/v1/admin/orders/{orderID}
func (h *Handler) AdminGetOrder(w http.ResponseWriter, r *http.Request) {
	detail, err := h.svc.GetOrderDetail(r.Context(), chi.URLParam(r, "orderID"))
	if errors.Is(err, ErrOrderNotFound) {
		respondErr(w, http.StatusNotFound, "order not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get order")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"order": detail})
}

// PATCH /api/v1/admin/orders/{orderID}/pay
func (h *Handler) AdminMarkOrderPaid(w http.ResponseWriter, r *http.Request) {
	orderID := chi.URLParam(r, "orderID")
	var req struct {
		PaymentRef string `json:"payment_ref"`
	}
	json.NewDecoder(r.Body).Decode(&req) // body 可空
	err := h.svc.MarkOrderPaid(r.Context(), orderID, req.PaymentRef)
	if errors.Is(err, ErrOrderNotFound) {
		respondErr(w, http.StatusNotFound, "order not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to mark paid")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TestWhitelistRouter 全域預設測試白名單路由（掛載在 /api/v1/admin/test-whitelist）
func (h *Handler) TestWhitelistRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListDefaultWhitelist)
	r.Post("/", h.AdminAddDefaultWhitelist)
	r.Delete("/", h.AdminRemoveDefaultWhitelist)
	return r
}

func (h *Handler) AdminListDefaultWhitelist(w http.ResponseWriter, r *http.Request) {
	emails, err := h.svc.ListDefaultWhitelist(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list whitelist")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"emails": emails})
}

func (h *Handler) AdminAddDefaultWhitelist(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		respondErr(w, http.StatusBadRequest, "email is required")
		return
	}
	if err := h.svc.AddDefaultWhitelist(r.Context(), req.Email); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to add")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) AdminRemoveDefaultWhitelist(w http.ResponseWriter, r *http.Request) {
	email := r.URL.Query().Get("email")
	if email == "" {
		respondErr(w, http.StatusBadRequest, "email is required")
		return
	}
	if err := h.svc.RemoveDefaultWhitelist(r.Context(), email); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to remove")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

// TaskModuleRouter 任務模組路由（掛載在 /api/v1/admin/task-modules）
func (h *Handler) TaskModuleRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListTaskModules)
	r.Post("/", h.AdminCreateTaskModule)
	r.Get("/{id}", h.AdminGetTaskModule)
	r.Put("/{id}", h.AdminUpdateTaskModule)
	r.Delete("/{id}", h.AdminDeleteTaskModule)
	return r
}

// GET /api/v1/admin/task-modules — 含 metric catalog 供前端鏡像
func (h *Handler) AdminListTaskModules(w http.ResponseWriter, r *http.Request) {
	mods, err := h.svc.ListTaskModules(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list task modules")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"modules": mods, "metrics": MetricCatalogList()})
}

// GET /api/v1/admin/task-modules/{id}
func (h *Handler) AdminGetTaskModule(w http.ResponseWriter, r *http.Request) {
	m, err := h.svc.GetTaskModule(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get task module")
		return
	}
	if m == nil {
		respondErr(w, http.StatusNotFound, "task module not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"module": m})
}

// POST /api/v1/admin/task-modules
func (h *Handler) AdminCreateTaskModule(w http.ResponseWriter, r *http.Request) {
	var m TaskModule
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	created, err := h.svc.CreateTaskModule(r.Context(), &m)
	h.respondTaskModule(w, http.StatusCreated, created, err)
}

// PUT /api/v1/admin/task-modules/{id}
func (h *Handler) AdminUpdateTaskModule(w http.ResponseWriter, r *http.Request) {
	var m TaskModule
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	updated, err := h.svc.UpdateTaskModule(r.Context(), chi.URLParam(r, "id"), &m)
	h.respondTaskModule(w, http.StatusOK, updated, err)
}

// DELETE /api/v1/admin/task-modules/{id}
func (h *Handler) AdminDeleteTaskModule(w http.ResponseWriter, r *http.Request) {
	err := h.svc.DeleteTaskModule(r.Context(), chi.URLParam(r, "id"))
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrTaskModuleNotFound):
		respondErr(w, http.StatusNotFound, "task module not found")
	default:
		respondErr(w, http.StatusInternalServerError, "failed to delete task module")
	}
}

func (h *Handler) respondTaskModule(w http.ResponseWriter, okStatus int, m *TaskModule, err error) {
	switch {
	case err == nil:
		respondJSON(w, okStatus, map[string]any{"module": m})
	case errors.Is(err, ErrTaskModuleName):
		respondErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrTaskModuleNotFound):
		respondErr(w, http.StatusNotFound, "task module not found")
	default:
		// 驗證錯誤（invalid metric / 缺值）回 400，其餘 500
		if strings.Contains(err.Error(), "metric") || strings.Contains(err.Error(), "range") || strings.Contains(err.Error(), "item") {
			respondErr(w, http.StatusBadRequest, err.Error())
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to save task module")
	}
}

// GET /api/v1/races — 前台賽事列表（依 control_status 過濾可見性，display_status 自動推導）
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	races, err := h.svc.ListPublic(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list races")
		return
	}

	resp := map[string]any{"races": races}
	// 登入時附帶使用者各賽事報名狀態（race_id → {status, group_revealed}）
	if userID != "" {
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
		"race":                  detail,
		"registration":          reg, // nil if not registered
		"can_create_team_group": h.svc.CanUserCreateTeamGroup(r.Context(), userID, &detail.Race),
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
	case errors.Is(err, ErrRegistrationPaused):
		respondErr(w, http.StatusConflict, "此賽事目前暫停報名")
	case errors.Is(err, ErrVIPOnly):
		respondErr(w, http.StatusForbidden, "此賽事僅限 VIP 會員報名")
	case errors.Is(err, ErrNoCoupon):
		respondErr(w, http.StatusConflict, "沒有可用的活動優惠券")
	case errors.Is(err, ErrCouponPromoConflict):
		respondErr(w, http.StatusBadRequest, "優惠券與優惠序號不可同時使用")
	case errors.Is(err, ErrGroupFull):
		respondErr(w, http.StatusConflict, "該分組名額已滿")
	case errors.Is(err, ErrGroupKeyWrong):
		respondErr(w, http.StatusForbidden, "跑團鑰匙錯誤")
	case errors.Is(err, ErrAddonSoldOut):
		respondErr(w, http.StatusConflict, "加購商品已售完")
	case errors.Is(err, promo.ErrUsedUp), errors.Is(err, promo.ErrUserUsed):
		respondErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrGroupRequired), errors.Is(err, ErrGroupNotFound),
		errors.Is(err, ErrNoGroups), errors.Is(err, ErrMissingRequiredField),
		errors.Is(err, ErrGroupRestriction), errors.Is(err, ErrAddonNotFound),
		errors.Is(err, ErrAddonLimit),
		errors.Is(err, promo.ErrNotFound), errors.Is(err, promo.ErrInactive),
		errors.Is(err, promo.ErrNotStarted), errors.Is(err, promo.ErrExpired),
		errors.Is(err, promo.ErrWrongRace), errors.Is(err, promo.ErrWrongUser):
		respondErr(w, http.StatusBadRequest, err.Error())
	default:
		respondErr(w, http.StatusInternalServerError, "registration failed")
	}
}

// POST /api/v1/races/:raceID/groups — 前台跑團成員自建分組（competition + allow_team_groups）
func (h *Handler) CreateTeamGroup(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}

	var req CreateTeamGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.RaceID = raceID
	req.UserID = userID

	group, err := h.svc.CreateTeamGroup(r.Context(), &req)
	switch {
	case err == nil:
		respondJSON(w, http.StatusCreated, group)
	case errors.Is(err, ErrRaceNotFound):
		respondErr(w, http.StatusNotFound, "race not found")
	case errors.Is(err, ErrTeamGroupsDisabled), errors.Is(err, ErrTeamGroupNotAllowed):
		respondErr(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrTeamGroupName):
		respondErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrRegistrationClosed):
		respondErr(w, http.StatusConflict, "非報名期間，無法建立跑團分組")
	default:
		respondErr(w, http.StatusInternalServerError, "create team group failed")
	}
}

// GET /api/v1/races/:raceID/progress — 使用者相關任務的達成度 + 個人統計（公開，登入後含個人）
func (h *Handler) Progress(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	prog, err := h.svc.GetRaceProgress(r.Context(), raceID, userID)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get progress")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"progress": prog})
}

// GET /api/v1/races/:raceID/leaderboard — 一般模式個人完成排名（公開，登入後含追蹤狀態）
func (h *Handler) Leaderboard(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	lb, err := h.svc.GetLeaderboard(r.Context(), raceID, userID)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get leaderboard")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"leaderboard": lb})
}

// GET /api/v1/races/:raceID/certificate — 登入者完賽證明資料（需登入）
func (h *Handler) Certificate(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	cert, err := h.svc.GetMyCertificate(r.Context(), raceID, userID)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get certificate")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"certificate": cert})
}

// GET /api/v1/races/:raceID/exp-breakdown — 登入者本場 EXP 結算明細（需登入）
func (h *Handler) ExpBreakdown(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	bd, err := h.svc.GetExpBreakdown(r.Context(), raceID, userID)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get exp breakdown")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"breakdown": bd})
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

// POST /api/v1/admin/races/:raceID/settle-exp?force=1 — 結算該賽事 EXP（idempotent）
func (h *Handler) AdminSettleEXP(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	force := r.URL.Query().Get("force") == "1"
	res, err := h.svc.SettleRaceEXP(r.Context(), raceID, force)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}
	if errors.Is(err, ErrRaceNotEnded) {
		respondErr(w, http.StatusBadRequest, "賽事尚未結束，如需提前結算請加 force")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "settle failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"result": res})
}

// PUT /api/v1/admin/races/:raceID/rank-display — 設定兩種排行榜是否顯示
func (h *Handler) AdminSetRankDisplay(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	var req struct {
		ShowDistanceRank bool `json:"show_distance_rank"`
		ShowTimeRank     bool `json:"show_time_rank"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := h.svc.SetRankDisplay(r.Context(), raceID, req.ShowDistanceRank, req.ShowTimeRank); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to set rank display")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PUT /api/v1/admin/races/:raceID/certificate-bg — 設定完賽證明底圖（空=用預設）
func (h *Handler) AdminSetCertificateBg(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := h.svc.SetCertificateBg(r.Context(), raceID, strings.TrimSpace(req.URL)); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to set certificate bg")
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
