// Package appsettings 通用系統設定（key-value 單表），供後台「系統設定」頁調教。
// 值一律以字串儲存；讀取端用 GetInt/GetString 等 typed helper 解析並帶預設值。
package appsettings

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/realtime"
)

// specs 登記所有合法設定 key 及其值驗證器（後端權威；新增設定時在此加一列）。
var specs = map[string]func(string) bool{
	"event_wait_min_sec":        isNonNegInt,
	"event_wait_max_sec":        isNonNegInt,
	"event_first_wait_run1_sec": isNonNegInt, // 新手加速：第 1/2/3 趟跑步「第一個事件」的等待秒數
	"event_first_wait_run2_sec": isNonNegInt,
	"event_first_wait_run3_sec": isNonNegInt,
	"active_skin":               func(v string) bool { return v == "" || v == "default" || v == "warm" || v == "warm2" },
	"interstitial_enabled":      func(v string) bool { return v == "" || v == "0" || v == "1" }, // 蓋板廣告總開關
	"favicon_url": func(v string) bool {
		return v == "" || (len(v) <= 512 && (strings.HasPrefix(v, "/") || strings.HasPrefix(v, "http")))
	},
	// 入口可見性：hidden 前台隱藏 / locked 顯示但不能按 / whitelist 顯示且指定帳號可按 / open 顯示且全部開放
	"personal_entry_state":        isEntryState,
	"personal_entry_whitelist":    isWhitelist,  // 換行/逗號分隔的帳號編碼或 email
	"explore_entry_state":         isEntryState, // 城市探索入口
	"explore_entry_whitelist":     isWhitelist,
	"gallery_entry_state":         isEntryState, // 卡片圖鑑入口
	"gallery_entry_whitelist":     isWhitelist,
	"title_entry_state":           isEntryState, // 稱號系統入口
	"title_entry_whitelist":       isWhitelist,
	"achievement_entry_state":     isEntryState, // 成就統計入口
	"achievement_entry_whitelist": isWhitelist,
	// VIP 訂閱制（後台可調數值）
	"vip_trial_days":              isNonNegInt, // 新註冊自動 VIP 試用天數
	"vip_price_monthly":           isNonNegInt, // 月繳原價（元）
	"vip_price_annual":            isNonNegInt, // 年繳原價（元）
	"vip_first_promo_monthly_pct": isPct,       // 首購促銷・月繳實付%（70=付七成）
	"vip_first_promo_annual_pct":  isPct,       // 首購促銷・年繳實付%（55=付五五）
	"vip_first_promo_days":        isNonNegInt, // 首購促銷窗天數（試用到期後幾天內續訂享優惠）
}

func isEntryState(v string) bool {
	return v == "" || v == "hidden" || v == "locked" || v == "whitelist" || v == "open"
}
func isWhitelist(v string) bool { return len(v) <= 20000 }

// isPct 促銷實付百分比：空(用預設) 或 1..100。
func isPct(v string) bool {
	if v == "" {
		return true
	}
	n, err := strconv.Atoi(v)
	return err == nil && n >= 1 && n <= 100
}

// publicKeys 允許未登入前台讀取的 key（皆為非敏感外觀設定）。
var publicKeys = map[string]bool{"active_skin": true, "favicon_url": true}

func isNonNegInt(v string) bool {
	if v == "" {
		return true
	}
	n, err := strconv.Atoi(v)
	return err == nil && n >= 0
}

type Handler struct {
	db *pgxpool.Pool
	rt *realtime.Manager
}

func NewHandler(db *pgxpool.Pool, rt *realtime.Manager) *Handler { return &Handler{db: db, rt: rt} }

// AdminRouter 掛 /admin/app-settings（需 settings 權限）
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Put("/{key}", h.Set)
	return r
}

func (h *Handler) queryAll(ctx context.Context, publicOnly bool) map[string]string {
	m := map[string]string{}
	rows, err := h.db.Query(ctx, `SELECT key, value FROM app_settings`)
	if err != nil {
		return m
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil && (!publicOnly || publicKeys[k]) {
			m[k] = v
		}
	}
	return m
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{"settings": h.queryAll(r.Context(), false)})
}

// Public 前台（可未登入）讀取白名單設定，如 active_skin。
func (h *Handler) Public(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{"settings": h.queryAll(r.Context(), true)})
}

func (h *Handler) Set(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	validate, known := specs[key]
	if !known {
		respondErr(w, http.StatusBadRequest, "unknown setting")
		return
	}
	var b struct {
		Value string `json:"value"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	val := strings.TrimSpace(b.Value)
	if !validate(val) {
		respondErr(w, http.StatusBadRequest, "invalid value")
		return
	}
	if _, err := h.db.Exec(r.Context(),
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1,$2,NOW())
		 ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`,
		key, val); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	h.rt.PublishData(r.Context(), "settings", nil)
	respondJSON(w, http.StatusOK, map[string]any{"settings": h.queryAll(r.Context(), false)})
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

// GetString 讀字串設定；查無/空值回 def。
func GetString(ctx context.Context, db *pgxpool.Pool, key, def string) string {
	var v string
	if err := db.QueryRow(ctx, `SELECT value FROM app_settings WHERE key=$1`, key).Scan(&v); err != nil {
		return def
	}
	if v = strings.TrimSpace(v); v == "" {
		return def
	}
	return v
}

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func respondErr(w http.ResponseWriter, code int, msg string) {
	respondJSON(w, code, map[string]any{"error": msg})
}
