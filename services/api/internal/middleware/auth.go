package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/dor/api/internal/auth"
)

// RequireAuth extracts and validates the JWT Bearer token from the Authorization header.
// Injects userID into request context using auth.CtxKeyUserID.
func RequireAuth(authSvc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
				return
			}

			tokenStr := strings.TrimPrefix(header, "Bearer ")
			claims, err := authSvc.ValidateAccessToken(r.Context(), tokenStr)
			if err != nil {
				http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), auth.CtxKeyUserID, claims.UserID)
			ctx = context.WithValue(ctx, roleKey{}, claims.Role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// OptionalAuth same as RequireAuth but doesn't reject unauthenticated requests.
func OptionalAuth(authSvc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if strings.HasPrefix(header, "Bearer ") {
				tokenStr := strings.TrimPrefix(header, "Bearer ")
				if claims, err := authSvc.ValidateAccessToken(r.Context(), tokenStr); err == nil {
					ctx := context.WithValue(r.Context(), auth.CtxKeyUserID, claims.UserID)
					ctx = context.WithValue(ctx, roleKey{}, claims.Role)
					r = r.WithContext(ctx)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// GetUserID extracts userID from context (set by RequireAuth middleware).
func GetUserID(ctx context.Context) string {
	id, _ := ctx.Value(auth.CtxKeyUserID).(string)
	return id
}

