// Package appsettings 通用系統設定（key-value 單表），供後台「系統設定」頁調教。
// 值一律以字串儲存；讀取端用 GetInt 等 typed helper 解析並帶預設值。
package appsettings

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var keyRe = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

type Handler struct{ db *pgxpool.Pool }

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

// AdminRouter 掛 /admin/app-settings（需 settings 權限）
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Put("/{key}", h.Set)
	return r
}

func (h *Handler) respond(w http.ResponseWriter, ctx context.Context) {
	rows, err := h.db.Query(ctx, `SELECT key, value FROM app_settings`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			m[k] = v
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{"settings": m})
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) { h.respond(w, r.Context()) }

func (h *Handler) Set(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if !keyRe.MatchString(key) {
		respondErr(w, http.StatusBadRequest, "invalid key")
		return
	}
	var b struct {
		Value string `json:"value"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	val := strings.TrimSpace(b.Value)
	// 目前所有設定皆為數值；非空必須可解析為數字，避免存了卻被 GetInt 靜默忽略（存了等於沒設）
	if val != "" {
		if _, err := strconv.ParseFloat(val, 64); err != nil {
			respondErr(w, http.StatusBadRequest, "value must be numeric")
			return
		}
	}
	if _, err := h.db.Exec(r.Context(),
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1,$2,NOW())
		 ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`,
		key, val); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	h.respond(w, r.Context())
}

// GetInt 讀整數設定；查無/解析失敗回 def。
func GetInt(ctx context.Context, db *pgxpool.Pool, key string, def int) int {
	var v string
	if err := db.QueryRow(ctx, `SELECT value FROM app_settings WHERE key=$1`, key).Scan(&v); err != nil {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func respondErr(w http.ResponseWriter, code int, msg string) {
	respondJSON(w, code, map[string]any{"error": msg})
}
