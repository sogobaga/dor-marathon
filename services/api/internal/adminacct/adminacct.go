// Package adminacct 後台管理者帳號管理 + 各模組權限。
// 管理者＝users(role='admin')；權限存 users.is_super_admin / users.admin_permissions。
// 超級管理員跳過所有權限檢查，且唯一能管理其他管理者。
package adminacct

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"strconv"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/dor/api/internal/auth"
)

// Scope 權限模組（key 為儲存值，label 給前台顯示）
type Scope struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// Scopes 全部可勾選的功能模組權限（前台鏡像此清單）。管理者管理本身不在此，靠 is_super。
var Scopes = []Scope{
	{"races", "賽事管理"},
	{"members", "會員管理"},
	{"signups", "報名管理"},
	{"orders", "訂單管理"},
	{"promo", "序號管理"},
	{"gps_review", "GPS 審核"},
	{"tasks", "賽事任務"},
	{"event_tasks", "事件任務"},
	{"settings", "等級／系統設定"},
	{"organizer", "主辦審核"},
	{"titles", "稱號管理"},
}

func validScope(k string) bool {
	for _, s := range Scopes {
		if s.Key == k {
			return true
		}
	}
	return false
}

func contains(list []string, k string) bool {
	for _, v := range list {
		if v == k {
			return true
		}
	}
	return false
}

// Admin 管理者帳號
type Admin struct {
	ID          string    `json:"id"`
	Login       string    `json:"login"` // = users.email（登入帳號）
	Name        string    `json:"name"`
	IsSuper     bool      `json:"is_super"`
	Permissions []string  `json:"permissions"`
	CreatedAt   time.Time `json:"created_at"`
}

type Handler struct {
	db *pgxpool.Pool
}

func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db}
}

// loadPerms 讀取某管理者的 is_super 與權限鍵；非 admin 回 (false,nil,nil)
func (h *Handler) loadPerms(ctx context.Context, userID string) (bool, []string, error) {
	var isSuper bool
	var perms []string
	err := h.db.QueryRow(ctx,
		`SELECT is_super_admin, admin_permissions FROM users WHERE id=$1 AND role='admin'`, userID).
		Scan(&isSuper, &perms)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return isSuper, perms, nil
}

// --- 權限 middleware（掛在 /admin/* 各群組；須在 RequireAuth+RequireAdmin 之後）---

// RequirePerm 需要指定模組權限（超級管理員一律放行）
func (h *Handler) RequirePerm(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
			isSuper, perms, err := h.loadPerms(r.Context(), uid)
			if err != nil {
				respondErr(w, http.StatusInternalServerError, "failed")
				return
			}
			if isSuper || contains(perms, scope) {
				next.ServeHTTP(w, r)
				return
			}
			respondErr(w, http.StatusForbidden, "無此功能的操作權限")
		})
	}
}

// RequireSuper 僅超級管理員
func (h *Handler) RequireSuper(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
		isSuper, _, err := h.loadPerms(r.Context(), uid)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		if !isSuper {
			respondErr(w, http.StatusForbidden, "僅超級管理員可管理管理者")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- /admin/me（任何 admin 皆可，讓前台知道自己的權限以決定選單）---

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var a Admin
	err := h.db.QueryRow(r.Context(),
		`SELECT id, email, name, is_super_admin, admin_permissions, created_at
		 FROM users WHERE id=$1 AND role='admin'`, uid).
		Scan(&a.ID, &a.Login, &a.Name, &a.IsSuper, &a.Permissions, &a.CreatedAt)
	if err != nil {
		respondErr(w, http.StatusForbidden, "admin required")
		return
	}
	if a.Permissions == nil {
		a.Permissions = []string{}
	}
	respondJSON(w, http.StatusOK, map[string]any{"admin": a, "scopes": Scopes})
}

// --- 管理者 CRUD（掛 /admin/admins，僅超級管理員）---

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/", h.Create)
	r.Put("/{id}", h.Update)
	r.Delete("/{id}", h.Delete)
	return r
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(),
		`SELECT id, email, name, is_super_admin, admin_permissions, created_at
		 FROM users WHERE role='admin' ORDER BY is_super_admin DESC, created_at`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	out := []Admin{}
	for rows.Next() {
		var a Admin
		if err := rows.Scan(&a.ID, &a.Login, &a.Name, &a.IsSuper, &a.Permissions, &a.CreatedAt); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		if a.Permissions == nil {
			a.Permissions = []string{}
		}
		out = append(out, a)
	}
	respondJSON(w, http.StatusOK, map[string]any{"admins": out})
}

type adminReq struct {
	Login       string   `json:"login"`
	Password    string   `json:"password"`
	Name        string   `json:"name"`
	IsSuper     bool     `json:"is_super"`
	Permissions []string `json:"permissions"`
}

// cleanScopes 去除非法/重複權限鍵
func cleanScopes(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, k := range in {
		if validScope(k) && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req adminReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Login = strings.TrimSpace(req.Login)
	req.Name = strings.TrimSpace(req.Name)
	if req.Login == "" || len(req.Password) < 4 {
		respondErr(w, http.StatusBadRequest, "帳號必填、密碼至少 4 碼")
		return
	}
	if req.Name == "" {
		req.Name = req.Login
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	perms := cleanScopes(req.Permissions)
	var a Admin
	err = h.db.QueryRow(r.Context(), `
		INSERT INTO users (email, handle, name, password_hash, role, is_super_admin, admin_permissions)
		VALUES ($1,$1,$2,$3,'admin',$4,$5)
		RETURNING id, email, name, is_super_admin, admin_permissions, created_at`,
		req.Login, req.Name, string(hash), req.IsSuper, perms).
		Scan(&a.ID, &a.Login, &a.Name, &a.IsSuper, &a.Permissions, &a.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			respondErr(w, http.StatusConflict, "此帳號已存在，請換一個")
			return
		}
		respondErr(w, http.StatusInternalServerError, "建立失敗")
		return
	}
	if a.Permissions == nil {
		a.Permissions = []string{}
	}
	respondJSON(w, http.StatusOK, map[string]any{"admin": a})
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	me, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var req adminReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	// 確認目標是 admin
	var curSuper bool
	if err := h.db.QueryRow(r.Context(),
		`SELECT is_super_admin FROM users WHERE id=$1 AND role='admin'`, id).Scan(&curSuper); err != nil {
		respondErr(w, http.StatusNotFound, "找不到此管理者")
		return
	}
	// 防呆：不能移除自己的超級權限、也不能移除最後一位超級管理員
	if curSuper && !req.IsSuper {
		if id == me {
			respondErr(w, http.StatusBadRequest, "不能移除自己的超級管理員權限")
			return
		}
		var superCount int
		h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM users WHERE role='admin' AND is_super_admin`).Scan(&superCount)
		if superCount <= 1 {
			respondErr(w, http.StatusBadRequest, "至少需保留一位超級管理員")
			return
		}
	}
	perms := cleanScopes(req.Permissions)
	name := strings.TrimSpace(req.Name)

	if strings.TrimSpace(req.Password) != "" {
		if len(req.Password) < 4 {
			respondErr(w, http.StatusBadRequest, "密碼至少 4 碼")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		if _, err := h.db.Exec(r.Context(),
			`UPDATE users SET password_hash=$1 WHERE id=$2`, string(hash), id); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
	}

	var a Admin
	err := h.db.QueryRow(r.Context(), `
		UPDATE users SET name=COALESCE(NULLIF($2,''), name), is_super_admin=$3, admin_permissions=$4
		WHERE id=$1 AND role='admin'
		RETURNING id, email, name, is_super_admin, admin_permissions, created_at`,
		id, name, req.IsSuper, perms).
		Scan(&a.ID, &a.Login, &a.Name, &a.IsSuper, &a.Permissions, &a.CreatedAt)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "更新失敗")
		return
	}
	if a.Permissions == nil {
		a.Permissions = []string{}
	}
	respondJSON(w, http.StatusOK, map[string]any{"admin": a})
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	me, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if id == me {
		respondErr(w, http.StatusBadRequest, "不能刪除自己")
		return
	}
	// 若目標是最後一位超級管理員則擋下
	var isSuper bool
	if err := h.db.QueryRow(r.Context(),
		`SELECT is_super_admin FROM users WHERE id=$1 AND role='admin'`, id).Scan(&isSuper); err != nil {
		respondErr(w, http.StatusNotFound, "找不到此管理者")
		return
	}
	if isSuper {
		var superCount int
		h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM users WHERE role='admin' AND is_super_admin`).Scan(&superCount)
		if superCount <= 1 {
			respondErr(w, http.StatusBadRequest, "至少需保留一位超級管理員")
			return
		}
	}
	if _, err := h.db.Exec(r.Context(), `DELETE FROM users WHERE id=$1 AND role='admin'`, id); err != nil {
		respondErr(w, http.StatusBadRequest, "刪除失敗（此帳號可能有關聯資料）")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}

// --- 操作紀錄（audit log）---

var auditResourceLabel = map[string]string{
	"races": "賽事", "admins": "管理者", "promo-codes": "序號", "members": "會員",
	"orders": "訂單", "signups": "報名", "task-modules": "賽事任務", "membership": "等級設定",
	"settings": "系統設定", "group-presets": "分組範本", "test-whitelist": "測試白名單",
	"gps-runs": "GPS 軌跡", "images": "圖片", "organizer": "主辦", "activities": "活動",
}
var auditVerb = map[string]string{"POST": "新增", "PUT": "更新", "PATCH": "更新", "DELETE": "刪除"}

func auditResource(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, p := range parts {
		if p == "admin" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func auditAction(method, resource string) string {
	verb := auditVerb[method]
	if verb == "" {
		verb = method
	}
	label := auditResourceLabel[resource]
	if label == "" {
		label = resource
	}
	return verb + label
}

func clientIP(r *http.Request) string {
	ip := r.RemoteAddr
	if i := strings.LastIndex(ip, ":"); i > 0 {
		ip = ip[:i]
	}
	return strings.Trim(ip, "[]")
}

func isUUID(s string) bool {
	return len(s) == 36 && s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-'
}

// auditTargetID 從 path 取 resource 之後、看起來是 UUID 的那段當目標 id（否則 nil）
func auditTargetID(path, resource string) interface{} {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, p := range parts {
		if p == resource && i+1 < len(parts) && isUUID(parts[i+1]) {
			return parts[i+1]
		}
	}
	return nil
}

// Audit 中介層：自動記錄異動類 (POST/PUT/PATCH/DELETE) 的 admin 請求（掛在 RequireAdmin 之後）。
func (h *Handler) Audit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodOptions || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
		if uid == "" {
			return
		}
		resource := auditResource(r.URL.Path)
		action := auditAction(r.Method, resource)
		targetID := auditTargetID(r.URL.Path, resource)
		ip := clientIP(r)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// 沿用 001 既有 audit_logs：meta 存 method/path/status + 操作者快照
		_, _ = h.db.Exec(ctx, `
			INSERT INTO audit_logs (user_id, action, resource, resource_id, meta, ip)
			SELECT id, $2, $3, $4,
			       jsonb_build_object('method',$5::text,'path',$6::text,'status',$7::int,'login',email,'name',name),
			       $8
			FROM users WHERE id=$1`,
			uid, action, resource, targetID, r.Method, r.URL.Path, ww.Status(), ip)
	})
}

// AuditLog 一筆操作紀錄
type AuditLog struct {
	ID         string    `json:"id"`
	ActorID    string    `json:"actor_id"`
	ActorLogin string    `json:"actor_login"`
	ActorName  string    `json:"actor_name"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Resource   string    `json:"resource"`
	Action     string    `json:"action"`
	Status     int       `json:"status"`
	IP         string    `json:"ip"`
	CreatedAt  time.Time `json:"created_at"`
}

// AuditList GET /admin/audit?limit=&offset=&resource=（僅超級管理員）
func (h *Handler) AuditList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 50
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	offset := 0
	if v, err := strconv.Atoi(q.Get("offset")); err == nil && v > 0 {
		offset = v
	}
	resource := q.Get("resource")

	var total int
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM audit_logs WHERE ($1='' OR resource=$1)`, resource).Scan(&total)

	rows, err := h.db.Query(r.Context(), `
		SELECT id::text, COALESCE(user_id::text,''), COALESCE(action,''), COALESCE(resource,''),
		       COALESCE(meta->>'method',''), COALESCE(meta->>'path',''), COALESCE((meta->>'status')::int,0),
		       COALESCE(meta->>'login',''), COALESCE(meta->>'name',''), COALESCE(ip,''), created_at
		FROM audit_logs
		WHERE ($1='' OR resource=$1)
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`, resource, limit, offset)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	out := []AuditLog{}
	for rows.Next() {
		var a AuditLog
		if err := rows.Scan(&a.ID, &a.ActorID, &a.Action, &a.Resource,
			&a.Method, &a.Path, &a.Status, &a.ActorLogin, &a.ActorName, &a.IP, &a.CreatedAt); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		out = append(out, a)
	}
	respondJSON(w, http.StatusOK, map[string]any{"logs": out, "count": total})
}
