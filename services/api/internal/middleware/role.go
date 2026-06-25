package middleware

import (
	"context"
	"net/http"

	"github.com/dor/api/internal/auth"
)

// roleKey 用於在 context 存放 role（與 userID 共存）
type roleKey struct{}

// RequireAdmin 限制只有 role=admin 的 JWT 才能通過
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, _ := r.Context().Value(roleKey{}).(string)
		if role != "admin" {
			http.Error(w, `{"error":"admin required"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireOrganizer 限制 role=organizer 或 role=admin
func RequireOrganizer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, _ := r.Context().Value(roleKey{}).(string)
		if role != "organizer" && role != "admin" {
			http.Error(w, `{"error":"organizer role required"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GetRole 從 context 取出 role（須先通過 RequireAuth middleware）
func GetRole(ctx context.Context) string {
	role, _ := ctx.Value(roleKey{}).(string)
	return role
}

// IsAdmin 檢查 context 中的 role 是否為 admin
func IsAdmin(ctx context.Context) bool { return GetRole(ctx) == "admin" }

// IsOrganizer 檢查是否為 organizer 或 admin
func IsOrganizer(ctx context.Context) bool {
	r := GetRole(ctx)
	return r == "organizer" || r == "admin"
}
