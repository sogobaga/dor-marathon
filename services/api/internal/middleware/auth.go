package middleware

import (
	"context"
	"errors"
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
				// 2026-09-08 audit finding 3(e)：Redis/DB 撤銷檢查對 admin token fail-closed 時，
				// Service.ValidateAccessToken 回 auth.ErrAuthUnavailable——這裡映射成 503（服務
				// 暫時不可用，前端可重試），不能跟一般的「token 無效」401 混在一起（401 會讓
				// 前端誤判成需要重新登入，而不是稍後重試）。
				if errors.Is(err, auth.ErrAuthUnavailable) {
					http.Error(w, `{"error":"auth service temporarily unavailable"}`, http.StatusServiceUnavailable)
					return
				}
				http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), auth.CtxKeyUserID, claims.UserID)
			ctx = context.WithValue(ctx, roleKey{}, claims.Role)
			// 單一登入：把 token 的 session epoch 也帶進 context，供下游（如 GPS 上傳）
			// 比對是否已被更新的登入取代（stale session）。
			ctx = context.WithValue(ctx, auth.CtxKeySessionEpoch, claims.SessionEpoch)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// OptionalAuth same as RequireAuth but doesn't reject unauthenticated requests.
// 刻意不特別處理 auth.ErrAuthUnavailable（見 RequireAuth 的 503 映射）：這支中介層本來就把任何
// 驗證失敗都當作「當成匿名訪客繼續」，Redis/DB 暫時不可用時降級成匿名瀏覽比讓公開/半公開端點
// 跟著 503 更合理——這裡不是需要保護的動作型端點。
func OptionalAuth(authSvc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if strings.HasPrefix(header, "Bearer ") {
				tokenStr := strings.TrimPrefix(header, "Bearer ")
				if claims, err := authSvc.ValidateAccessToken(r.Context(), tokenStr); err == nil {
					ctx := context.WithValue(r.Context(), auth.CtxKeyUserID, claims.UserID)
					ctx = context.WithValue(ctx, roleKey{}, claims.Role)
					ctx = context.WithValue(ctx, auth.CtxKeySessionEpoch, claims.SessionEpoch)
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
