package auth

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-playground/validator/v10"

	"github.com/dor/api/internal/realtime"
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

// POST /api/v1/auth/register
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"    validate:"required,email"`
		Handle   string `json:"handle"   validate:"required,min=3,max=30,alphanum"`
		Name     string `json:"name"     validate:"required,min=1,max=50"`
		Password string `json:"password" validate:"required,min=8"`
		Role     string `json:"role"`     // 可選：留空 = "user"，填 "organizer" = 合作方申請
		RefCode  string `json:"ref_code"` // 可選：?ref=<code> 推廣連結帶入的推薦碼
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validate.Struct(req); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}

	user, pair, err := h.svc.Register(r.Context(), req.Email, req.Handle, req.Name, req.Password, req.Role, req.RefCode)
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
	respondJSON(w, http.StatusOK, map[string]any{
		"user":          toPublicUser(user),
		"tokens":        pair,
		"session_epoch": pair.SessionEpoch,
	})
}

// POST /api/v1/auth/google — Google ID token 登入（GIS 流程）
func (h *Handler) Google(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDToken string `json:"id_token" validate:"required"`
		RefCode string `json:"ref_code"` // 可選：?ref=<code> 推廣連結帶入的推薦碼（只在新帳號分支使用）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validate.Struct(req); err != nil {
		respondErr(w, http.StatusBadRequest, "id_token is required")
		return
	}

	user, pair, err := h.svc.LoginWithGoogle(r.Context(), req.IDToken, req.RefCode)
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
