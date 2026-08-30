// handler.go：GPS 距離校正 HTTP 端點——使用者自助（GET/PUT/POST /me/gps-calib）+ 後台管理
// （/admin/gps-calib/{user_id}）。使用者三個端點皆掛 requireEntry：非白名單（且非 super_admin）
// 一律 403（SEC-H5 同款：前端 UI 隱藏不等於後端有擋，比照 monopoly/cheer_layout 前例）。
package gpscalib

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/auth"
)

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func respondErr(w http.ResponseWriter, code int, msg string) {
	respondJSON(w, code, map[string]any{"error": msg})
}

type Handler struct {
	db *pgxpool.Pool
}

func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db}
}

// requireEntry 後端強制 GPS 校正入口白名單（SEC-H5）。獨立實作（比照 monopoly/cheer_layout 前例）：
// 讀 gps_calib_entry_state/whitelist + 該使用者 email/account_code/is_super_admin，super_admin
// 恆放行；state=open 放行；state=whitelist 命中放行；其餘（含 hidden/locked/未設定）一律 403——
// 「locked」在前端是顯示但不可按，對應到後端一樣不放行。
func (h *Handler) requireEntry(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
		if uid == "" {
			respondErr(w, http.StatusUnauthorized, "login required")
			return
		}
		email, code, isSuperAdmin, err := resolveUserIdentity(r.Context(), h.db, uid)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to resolve access")
			return
		}
		if ResolveEntry(r.Context(), h.db, email, code, isSuperAdmin) != "shown" {
			respondErr(w, http.StatusForbidden, "not available")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Router 掛 /me/gps-calib（GET 查詢 / PUT 開關 / POST recompute 手動重算）。
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(h.requireEntry)
	r.Get("/", h.Get)
	r.Put("/", h.Put)
	r.Post("/recompute", h.PostRecompute)
	return r
}

// AdminRouter 掛 /admin/gps-calib（外層由 main.go 套 RequirePerm("members")）。
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/{user_id}", h.AdminGet)
	r.Post("/{user_id}/freeze", h.AdminFreezeHandler)
	r.Post("/{user_id}/unfreeze", h.AdminUnfreezeHandler)
	r.Post("/{user_id}/reset", h.AdminResetHandler)
	return r
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	st, err := GetStatus(r.Context(), h.db, uid, 20, 30)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, st)
}

func (h *Handler) Put(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := SetEnabled(r.Context(), h.db, uid, body.Enabled); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	st, err := GetStatus(r.Context(), h.db, uid, 20, 30)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, st)
}

// manualRecomputeAt：手動重算限流用的套件級記憶體時戳（userID -> 上次「手動」呼叫時間），與
// user_gps_calib.computed_at 分開維護。對抗式審查修正（low-3 finding）：computed_at 會被任何來源
// 的 Recompute 更新（GPS 上傳/Strava webhook 等背景觸發，見 RecomputeAsync），若拿它判斷手動限
// 流，使用者會被自己根本沒按過的自動重算連帶擋下 429（例如剛上傳完一趟跑步、5 秒後
// RecomputeAsync 自動觸發把 computed_at 更新，使用者接著切到個人資料頁按「重新計算」就被誤擋）。
// 只記手動呼叫本身的時間，才是這支端點真正該限流的對象。
var manualRecomputeAt sync.Map

// PostRecompute 手動重算，每人 60 秒限流（見上方 manualRecomputeAt 註解）。
func (h *Handler) PostRecompute(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if v, ok := manualRecomputeAt.Load(uid); ok {
		if last, ok := v.(time.Time); ok && time.Since(last) < 60*time.Second {
			respondErr(w, http.StatusTooManyRequests, "請稍候再試")
			return
		}
	}
	manualRecomputeAt.Store(uid, time.Now())
	if err := Recompute(r.Context(), h.db, uid, "recompute", "user"); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	st, err := GetStatus(r.Context(), h.db, uid, 20, 30)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, st)
}

func (h *Handler) AdminGet(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	st, err := GetStatus(r.Context(), h.db, userID, 200, 200)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, st)
}

func (h *Handler) AdminFreezeHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	adminID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var body struct {
		Factor float64 `json:"factor"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := AdminFreeze(r.Context(), h.db, userID, body.Factor, "admin:"+adminID); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) AdminUnfreezeHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	adminID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if err := AdminUnfreeze(r.Context(), h.db, userID, "admin:"+adminID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) AdminResetHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	adminID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if err := AdminReset(r.Context(), h.db, userID, "admin:"+adminID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- 讀模型：GET /me/gps-calib 與後台詳情共用 ---

type PairView struct {
	ActivityAt   time.Time `json:"activity_at"`
	ExtSource    string    `json:"ext_source"`
	GpsKm        float64   `json:"gps_km"`
	ExtKm        float64   `json:"ext_km"`
	Ratio        float64   `json:"ratio"`
	Accepted     bool      `json:"accepted"`
	RejectReason string    `json:"reject_reason,omitempty"`
	InlierW      *float64  `json:"inlier_w,omitempty"`
}

type LogEntry struct {
	Version      int       `json:"version"`
	FactorBefore *float64  `json:"factor_before,omitempty"`
	FactorAfter  *float64  `json:"factor_after,omitempty"`
	Status       string    `json:"status,omitempty"`
	Reason       string    `json:"reason"`
	Actor        string    `json:"actor"`
	CreatedAt    time.Time `json:"created_at"`
}

// Status GET /me/gps-calib 與 GET /admin/gps-calib/{user_id} 的回應形狀（前端契約，見
// apps/web/src/lib/api.ts gpsCalibApi）。entry=hidden/locked 時其餘欄位仍可能有值（後端不因此
// 清空，前端本就不該在非 shown 時渲染這個區塊；locked 卡片需要 factor/status 顯示鎖定前的狀態）。
type Status struct {
	Entry      string     `json:"entry"`
	Enabled    bool       `json:"enabled"`
	Factor     float64    `json:"factor"`
	Status     string     `json:"status"`
	RefSource  string     `json:"ref_source"`
	NPairs     int        `json:"n_pairs"`
	NEff       float64    `json:"n_eff"`
	Sigma      float64    `json:"sigma"`
	LastPairAt *time.Time `json:"last_pair_at,omitempty"`
	ComputedAt *time.Time `json:"computed_at,omitempty"`
	Version    int        `json:"version"`
	Pairs      []PairView `json:"pairs"`
	Log        []LogEntry `json:"log"`
}

// GetStatus 組出 GET /me/gps-calib（或後台詳情）的完整回應：user_gps_calib 現況 + 最近
// pairLimit 筆配對（依 activity_at 新到舊）+ 最近 logLimit 筆係數異動紀錄。查無 user_gps_calib
// 列（從未有過候選配對）不視為錯誤，回傳預設值（factor=1.0/status=warming/enabled=true）。
func GetStatus(ctx context.Context, db *pgxpool.Pool, userID string, pairLimit, logLimit int) (*Status, error) {
	email, code, isSuperAdmin, err := resolveUserIdentity(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	st := &Status{
		Entry:   ResolveEntry(ctx, db, email, code, isSuperAdmin),
		Enabled: true, Factor: 1.0, Status: "warming",
		Pairs: []PairView{}, Log: []LogEntry{},
	}
	var sigma *float64
	err = db.QueryRow(ctx, `
		SELECT enabled, factor, status, ref_source, n_pairs, n_eff, sigma, last_pair_at, computed_at, version
		FROM user_gps_calib WHERE user_id=$1`, userID).
		Scan(&st.Enabled, &st.Factor, &st.Status, &st.RefSource, &st.NPairs, &st.NEff, &sigma, &st.LastPairAt, &st.ComputedAt, &st.Version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if sigma != nil {
		st.Sigma = *sigma
	}
	// 對抗式審查修正（low-2 finding）：EffectiveFactor/DashboardSummary 都會在讀取端懶判「status='active'
	// 但 last_pair_at 已超過 StaleDays 未更新」→ 視為 stale/1.0（不寫 DB，等下次真的有新配對觸發
	// Recompute 才落地）；GetStatus 之前是直接回傳 DB 原值，使用者停用手錶、Recompute 不再被觸發後，
	// 這裡會永遠顯示「校正中 ×0.97xx」，跟實際上 GPS 上傳早已不套用（EffectiveFactor 回 1.0）矛盾。
	if st.Status == "active" && st.LastPairAt != nil && time.Since(*st.LastPairAt) > StaleDays*24*time.Hour {
		st.Status = "stale"
		st.Factor = 1.0
	}

	rows, err := db.Query(ctx, `
		SELECT activity_at, ext_source, gps_km, ext_km, accepted, COALESCE(reject_reason,''), inlier_w
		FROM gps_calib_pairs WHERE user_id=$1 ORDER BY activity_at DESC LIMIT $2`, userID, pairLimit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var pv PairView
		if err := rows.Scan(&pv.ActivityAt, &pv.ExtSource, &pv.GpsKm, &pv.ExtKm, &pv.Accepted, &pv.RejectReason, &pv.InlierW); err != nil {
			rows.Close()
			return nil, err
		}
		if pv.GpsKm > 0 {
			pv.Ratio = pv.ExtKm / pv.GpsKm
		}
		st.Pairs = append(st.Pairs, pv)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	logRows, err := db.Query(ctx, `
		SELECT version, factor_before, factor_after, COALESCE(status,''), reason, actor, created_at
		FROM user_gps_calib_log WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2`, userID, logLimit)
	if err != nil {
		return nil, err
	}
	for logRows.Next() {
		var le LogEntry
		if err := logRows.Scan(&le.Version, &le.FactorBefore, &le.FactorAfter, &le.Status, &le.Reason, &le.Actor, &le.CreatedAt); err != nil {
			logRows.Close()
			return nil, err
		}
		st.Log = append(st.Log, le)
	}
	logRows.Close()
	if err := logRows.Err(); err != nil {
		return nil, err
	}

	return st, nil
}
