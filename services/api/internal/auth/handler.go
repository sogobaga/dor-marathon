package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-playground/validator/v10"

	"github.com/dor/api/internal/realtime"
	"github.com/dor/api/internal/reqip"
)

var validate = validator.New()

type Handler struct {
	svc      *Service
	realtime *realtime.Manager // 單一登入：登入成功後推 session_revoked 踢舊裝置；main.go 用 SetRealtime 注入（見下）
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// SetRealtime 注入 WebSocket 管理器。main.go 因初始化順序（authHandler 建於 wsManager 之前）
// 無法在 NewHandler 建構時就傳入，故另開此 setter；未呼叫時 h.realtime 維持 nil，
// 登入推播會安全略過（見 pushSessionRevoked），不影響測試或未接線場景。
func (h *Handler) SetRealtime(m *realtime.Manager) {
	h.realtime = m
}

// pushSessionRevoked 登入成功後推播單一登入踢除通知；h.realtime 為 nil 時靜默略過。
func (h *Handler) pushSessionRevoked(r *http.Request, userID string, epoch int) {
	if h.realtime != nil {
		h.realtime.PublishSessionRevoked(r.Context(), userID, epoch)
	}
}

// recordLogin fire-and-forget 寫入登入紀錄（user_login_logs + users.last_login_at）。
// 用獨立 context（3 秒逾時，不綁定原本 request context——handler 回應後 r.Context() 就會被
// net/http 取消）+ 獨立 goroutine，絕不可拖慢或阻斷登入回應；失敗只記 log，不影響已回應的登入結果。
// method 只會是 password/google/register，呼叫端刻意不含 refresh（見各呼叫點註解）。
func (h *Handler) recordLogin(userID, method, ip string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := h.svc.RecordLogin(ctx, userID, method, ip); err != nil {
			log.Printf("auth.recordLogin: user=%s method=%s err=%v", userID, method, err)
		}
	}()
}

// POST /api/v1/auth/register
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string  `json:"email"    validate:"required,email"`
		Handle   string  `json:"handle"   validate:"required,min=3,max=30,alphanum"`
		Name     string  `json:"name"     validate:"required,min=1,max=50"`
		Password string  `json:"password" validate:"required,min=8"`
		Role     string  `json:"role"`     // 可選：留空 = "user"，填 "organizer" = 合作方申請
		RefCode  string  `json:"ref_code"` // 可選：?ref=<code> 推廣連結帶入的推薦碼
		Acq      *acqReq `json:"acq"`      // 可選：來源歸因（見 lib/acquisition.ts），只在新帳號才會被用到
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validate.Struct(req); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}

	user, pair, err := h.svc.Register(r.Context(), req.Email, req.Handle, req.Name, req.Password, req.Role, req.RefCode, req.Acq.toAcq())
	if errors.Is(err, ErrEmailTaken) {
		respondErr(w, http.StatusConflict, "email already registered")
		return
	}
	if errors.Is(err, ErrHandleTaken) {
		respondErr(w, http.StatusConflict, "handle already taken")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "registration failed")
		return
	}

	h.pushSessionRevoked(r, user.ID, pair.SessionEpoch)
	h.recordLogin(user.ID, "register", reqip.ClientIP(r))
	respondJSON(w, http.StatusCreated, map[string]any{
		"user":          toPublicUser(user),
		"tokens":        pair,
		"session_epoch": pair.SessionEpoch,
	})
}

// POST /api/v1/auth/login
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		// 登入識別：可為 email 或純帳號（如 admin），故不限定 email 格式
		Email    string `json:"email"    validate:"required"`
		Password string `json:"password" validate:"required"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validate.Struct(req); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}

	user, pair, err := h.svc.Login(r.Context(), req.Email, req.Password)
	if errors.Is(err, ErrInvalidCredentials) {
		respondErr(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "login failed")
		return
	}

	h.pushSessionRevoked(r, user.ID, pair.SessionEpoch)
	h.recordLogin(user.ID, "password", reqip.ClientIP(r))
	respondJSON(w, http.StatusOK, map[string]any{
		"user":          toPublicUser(user),
		"tokens":        pair,
		"session_epoch": pair.SessionEpoch,
	})
}

// POST /api/v1/auth/google — Google ID token 登入（GIS 流程）
func (h *Handler) Google(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDToken string  `json:"id_token" validate:"required"`
		RefCode string  `json:"ref_code"` // 可選：?ref=<code> 推廣連結帶入的推薦碼（只在新帳號分支使用）
		Acq     *acqReq `json:"acq"`      // 可選：來源歸因（只在新帳號分支使用）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validate.Struct(req); err != nil {
		respondErr(w, http.StatusBadRequest, "id_token is required")
		return
	}

	user, pair, err := h.svc.LoginWithGoogle(r.Context(), req.IDToken, req.RefCode, req.Acq.toAcq())
	if errors.Is(err, ErrGoogleNotConfigured) {
		respondErr(w, http.StatusServiceUnavailable, "google login not configured")
		return
	}
	if errors.Is(err, ErrGoogleTokenInvalid) {
		respondErr(w, http.StatusUnauthorized, "invalid google token")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "google login failed")
		return
	}

	h.pushSessionRevoked(r, user.ID, pair.SessionEpoch)
	h.recordLogin(user.ID, "google", reqip.ClientIP(r))
	respondJSON(w, http.StatusOK, map[string]any{
		"user":          toPublicUser(user),
		"tokens":        pair,
		"session_epoch": pair.SessionEpoch,
	})
}

// POST /api/v1/auth/refresh
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token" validate:"required"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}

	pair, err := h.svc.Refresh(r.Context(), req.RefreshToken)
	if errors.Is(err, ErrTokenInvalid) {
		respondErr(w, http.StatusUnauthorized, "invalid or expired token")
		return
	}
	// 單一登入：session 已被別的登入取代（epoch 不符）→ 401，前端走既有登出流程。
	if errors.Is(err, ErrSessionSuperseded) {
		respondErr(w, http.StatusUnauthorized, "session superseded by a newer login")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "refresh failed")
		return
	}

	respondJSON(w, http.StatusOK, pair)
}

// DELETE /api/v1/auth/logout
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	// userID は JWT middleware が context に入れる
	userID, _ := r.Context().Value(CtxKeyUserID).(string)
	if userID != "" && req.RefreshToken != "" {
		h.svc.Logout(r.Context(), userID, req.RefreshToken)
	}

	w.WriteHeader(http.StatusNoContent)
}

// GET /admin/login-logs?q=&limit=&offset=（perm: members）— 後台查最近登入流水，
// 供檢查「有沒有人天天登入」；JOIN users 帶 email，依 created_at DESC 分頁。
// q 為選填的 email 模糊篩選。刻意不含 refresh（高頻自動行為，見 recordLogin 註解）。
func (h *Handler) AdminLoginLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	logs, total, err := h.svc.ListLoginLogs(r.Context(), q, limit, offset)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list login logs")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"logs": logs, "count": total})
}

// GET /api/v1/auth/me
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxKeyUserID).(string)
	user, err := h.svc.GetUserByID(r.Context(), userID)
	if err != nil || user == nil {
		respondErr(w, http.StatusUnauthorized, "user not found")
		return
	}
	respondJSON(w, http.StatusOK, toPublicUser(user))
}

// ---

// acqReq 是 Register/Google request body 裡 "acq" 欄位的形狀，對應前端 lib/acquisition.ts 的
// getAcquisition()——App 首次進站（first-touch）擷取的 landing URL 與 document.referrer，皆選填。
// 獨立成具名 struct（而非沿用既有 auth.Acq）是因為 JSON 欄位命名（snake_case）與內部 Acq 的欄位
// 命名習慣不同，用 toAcq() 轉換，避免把 JSON tag 摻進 service 層的內部型別。
type acqReq struct {
	LandingURL  string `json:"landing_url"`
	ReferrerURL string `json:"referrer_url"`
}

// toAcq 轉為 service 層的 Acq；req 為 nil（前端未帶 acq，或非 Register/Google 端點）時回傳零值，
// 呼叫端（Service.Register/LoginWithGoogle）對零值 Acq 的行為等同「無歸因資訊」→ classify 落回 direct。
func (req *acqReq) toAcq() Acq {
	if req == nil {
		return Acq{}
	}
	return Acq{LandingURL: req.LandingURL, ReferrerURL: req.ReferrerURL}
}

// CtxKeyUserID is the context key for the authenticated user's ID.
// Exported so middleware can set it using the same type.
type ContextKey string

const CtxKeyUserID ContextKey = "userID"

// CtxKeySessionEpoch is the context key for the authenticated token's session epoch (單一登入用).
// Exported so middleware can set it and downstream handlers（如 GPS 上傳）可比對是否為 stale session。
const CtxKeySessionEpoch ContextKey = "sessionEpoch"

func toPublicUser(u *User) map[string]any {
	return map[string]any{
		"id":         u.ID,
		"email":      u.Email,
		"handle":     u.Handle,
		"name":       u.Name,
		"avatar_url": u.AvatarURL,
		"total_km":   u.TotalKm,
		"role":       u.Role,
	}
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
