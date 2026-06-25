package auth

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-playground/validator/v10"
)

var validate = validator.New()

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// POST /api/v1/auth/register
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"    validate:"required,email"`
		Handle   string `json:"handle"   validate:"required,min=3,max=30,alphanum"`
		Name     string `json:"name"     validate:"required,min=1,max=50"`
		Password string `json:"password" validate:"required,min=8"`
		Role     string `json:"role"`    // 可選：留空 = "user"，填 "organizer" = 合作方申請
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validate.Struct(req); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}

	user, pair, err := h.svc.Register(r.Context(), req.Email, req.Handle, req.Name, req.Password, req.Role)
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

	respondJSON(w, http.StatusCreated, map[string]any{
		"user":   toPublicUser(user),
		"tokens": pair,
	})
}

// POST /api/v1/auth/login
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"    validate:"required,email"`
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

	respondJSON(w, http.StatusOK, map[string]any{
		"user":   toPublicUser(user),
		"tokens": pair,
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
