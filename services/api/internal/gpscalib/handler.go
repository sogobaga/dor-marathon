// handler.go：GPS 距離校正 HTTP 端點——使用者自助（GET/PUT/POST /me/gps-calib）+ 後台管理
// （/admin/gps-calib/{user_id}）。使用者三個端點皆掛 requireEntry：非白名單（且非 super_admin）
// 一律 403（SEC-H5 同款：前端 UI 隱藏不等於後端有擋，比照 monopoly/cheer_layout 前例）。
package gpscalib

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/appsettings"
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
	r.Get("/", h.AdminList) // 全站校正概況（寫在 /{user_id} 之前，可讀性；chi 段數不同不會衝突）
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

// validCalibStatus 列表篩選允許的狀態值（與 migration 154 的 user_gps_calib.status 註解同步）。
var validCalibStatus = map[string]bool{"warming": true, "active": true, "unstable": true, "stale": true, "frozen": true}

// adminListBaseCTE 全站校正概況的共用 CTE（列表與總數兩支查詢共用同一份定義，避免筆數與內容對不上）。
// $1 = stale 判定的時間界線（now−StaleDays）。
//
// 兩個 CASE：把 GetStatus/EffectiveFactor 讀取端的「懶判 stale」（status='active' 但 last_pair_at 已超過
// 120 天未更新 → 視為 stale/1.0，不寫 DB）搬進 SQL，讓列表顯示的 factor/status 與點進詳情頁看到的完全
// 一致；也讓 ?status= 篩選比對的是同一個「有效狀態」，否則篩 active 會撈出一批詳情頁顯示 stale 的列。
// 顯示名稱走下方 SQL 的 display_name（COALESCE + NULLIF 取 users.name，空字串退回 handle）——專案顯示
// 名稱統一口徑，不讀個資暱稱；虛擬選手（is_virtual，見 migration 146）排除，比照後台會員列表慣例。
const adminListBaseCTE = `
	WITH base AS (
		SELECT c.user_id::text AS user_id,
		       COALESCE(NULLIF(u.name,''), u.handle) AS display_name,
		       COALESCE(u.email,'') AS email,
		       COALESCE(u.account_code,'') AS account_code,
		       CASE WHEN c.status='active' AND c.last_pair_at IS NOT NULL AND c.last_pair_at < $1
		            THEN 1.0 ELSE c.factor END AS factor,
		       CASE WHEN c.status='active' AND c.last_pair_at IS NOT NULL AND c.last_pair_at < $1
		            THEN 'stale' ELSE c.status END AS status,
		       c.enabled, c.ref_source, c.n_pairs, c.n_eff, c.sigma,
		       c.last_pair_at, c.computed_at, c.version
		FROM user_gps_calib c
		JOIN users u ON u.id = c.user_id
		WHERE NOT u.is_virtual
	)`

// AdminList GET /admin/gps-calib?limit=&offset=&status=（外層由 main.go 套 RequirePerm("members")，
// 與既有 /admin/gps-calib/{user_id} 同一道權限）。回全站「已經有校正資料」（user_gps_calib 有列）的
// 會員概況，依 computed_at 新到舊；total 為套用 status 篩選後的總筆數（供後台分頁）。
func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && !validCalibStatus[status] {
		respondErr(w, http.StatusBadRequest, "invalid status")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	// 與 GetStatus 的 time.Since(*st.LastPairAt) > StaleDays*24h 同一條界線，在 Go 端算好再送進 SQL
	// （避免 make_interval 的參數型別推導問題，也讓兩處只有一份常數來源 StaleDays）。
	staleBefore := time.Now().UTC().Add(-StaleDays * 24 * time.Hour)

	var total int
	if err := h.db.QueryRow(r.Context(),
		adminListBaseCTE+` SELECT count(*) FROM base WHERE ($2::text = '' OR status = $2::text)`,
		staleBefore, status).Scan(&total); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	rows, err := h.db.Query(r.Context(), adminListBaseCTE+`
		SELECT user_id, display_name, email, account_code, factor, status, enabled, ref_source,
		       n_pairs, n_eff, sigma, last_pair_at, computed_at, version
		FROM base
		WHERE ($2::text = '' OR status = $2::text)
		ORDER BY computed_at DESC NULLS LAST
		LIMIT $3 OFFSET $4`, staleBefore, status, limit, offset)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	// 入口設定只查一次，之後每列走純函式 applyEntryFrom（見 service.go）——列表一頁最多 200 列，
	// 逐列各查兩次 app_settings 沒有必要。
	entryState := appsettings.GetString(r.Context(), h.db, EntryStateKey, "hidden")
	entryWhitelist := appsettings.GetString(r.Context(), h.db, EntryWhitelistKey, "")
	now := time.Now()
	items := []AdminCalibRow{}
	for rows.Next() {
		var it AdminCalibRow
		var sigma *float64
		if err := rows.Scan(&it.UserID, &it.Name, &it.Email, &it.AccountCode, &it.Factor, &it.Status,
			&it.Enabled, &it.RefSource, &it.NPairs, &it.NEff, &sigma,
			&it.LastPairAt, &it.ComputedAt, &it.Version); err != nil {
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		if sigma != nil {
			it.Sigma = *sigma // DB 可為 NULL（尚未估過）→ 對前端一律回 0，比照 GetStatus
		}
		// 對抗式審查修正（high finding）：SQL 的兩個 CASE 只補了「懶判 stale」一種，EffectiveFactor
		// 另外兩道閘門（入口非 shown 的影子模式、使用者關閉）完全沒反映——不補的話，非白名單會員
		// 會被列成「校正中 ×0.97xx」，但他一公里都沒被校正過。
		it.ApplyEntry = applyEntryFrom(entryState, entryWhitelist, it.Email, it.AccountCode)
		es := effectiveState(it.ApplyEntry, true, it.Enabled, it.Status, it.Factor, it.LastPairAt, now)
		it.EffectiveFactor = es.Factor
		it.Applied = es.Applied
		it.NotApplyReason = es.Reason
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
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

// PairView 一組候選配對的顯示欄位。GpsKm 一律是 App GPS 原始距離（永不套校正，見 migration 154 的
// gps_runs.distance_km COMMENT）、ExtKm 是手錶/外部來源距離。
//
// 對抗式審查修正（high finding）：距離有「兩個」校正後的數字，語意完全不同，之前只回一個而且標錯：
//   - CreditedKm/CreditedFactor：這趟跑步**當時真正入帳**的距離與係數，逐趟凍結在
//     gps_runs.calib_distance_km / calib_factor（見 activity/gps.go 寫入點）。係數是隨時間演進的
//     （±2% 遲滯步幅、warming 期間恆為 1.0），所以歷史配對多半不等於今天的係數。
//   - CalibKm：用**目前生效係數**回推的假設值（back-test），只能用來評估「現在這組係數好不好」，
//     絕不是實際入帳的距離。
//
// InWindow：這筆是否真的進了估計視窗。Gate() 產生的 accepted 不含任何視窗條件，視窗過濾是
// EstimateWindow 才做（120 天內、最新 20 組），因此 DB 的 accepted=TRUE 會多於實際採用的組數；
// 超過 120 天的舊列更是永遠不會被 candidateSQL 重寫，accepted 會一直留著。判準＝accepted 且有
// inlier_w（只有進視窗的那批會被寫回權重）且 activity_at 仍在 WindowDays 內。
type PairView struct {
	ActivityAt     time.Time `json:"activity_at"`
	ExtSource      string    `json:"ext_source"`
	GpsKm          float64   `json:"gps_km"`
	ExtKm          float64   `json:"ext_km"`
	CreditedKm     float64   `json:"credited_km"`
	CreditedFactor float64   `json:"credited_factor"`
	CalibKm        float64   `json:"calib_km"`
	Ratio          float64   `json:"ratio"`
	Accepted       bool      `json:"accepted"`
	InWindow       bool      `json:"in_window"`
	RejectReason   string    `json:"reject_reason,omitempty"`
	InlierW        *float64  `json:"inlier_w,omitempty"`
	GpsRunID       string    `json:"gps_run_id"`
	ExtActivityID  string    `json:"ext_activity_id"`
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
	Entry string `json:"entry"`
	// ApplyEntry/EffectiveFactor/Applied/NotApplyReason：對抗式審查修正（high finding）——Factor 是
	// user_gps_calib 的「估計係數」，不等於實際入帳的係數。Recompute 是影子模式（對全體無條件執
	// 行），所以非白名單會員也會被寫成 active/0.97xx；使用者自己關掉開關（enabled=false）時也一
	// 樣。EffectiveFactor 走 effectiveState（與 GPS 上傳的 EffectiveFactor 同一份判定），才是「這
	// 一刻真的乘上去的那個數字」；NotApplyReason 說明為什麼沒生效（entry/no_data/disabled/status/stale）。
	ApplyEntry      string     `json:"apply_entry"` // hidden|locked|shown（不含 super_admin 旁路）
	EffectiveFactor float64    `json:"effective_factor"`
	Applied         bool       `json:"applied"`
	NotApplyReason  string     `json:"not_apply_reason,omitempty"`
	Enabled         bool       `json:"enabled"`
	Factor          float64    `json:"factor"`
	Status          string     `json:"status"`
	RefSource       string     `json:"ref_source"`
	NPairs          int        `json:"n_pairs"`
	NEff            float64    `json:"n_eff"`
	Sigma           float64    `json:"sigma"`
	LastPairAt      *time.Time `json:"last_pair_at,omitempty"`
	ComputedAt      *time.Time `json:"computed_at,omitempty"`
	Version         int        `json:"version"`
	Pairs           []PairView `json:"pairs"`
	Log             []LogEntry `json:"log"`
}

// AdminCalibRow GET /admin/gps-calib 列表的一列（後台「GPS 校正紀錄」頁，前端契約見
// apps/web/src/lib/api.ts adminGpsCalibApi.list）。Factor/Status 已套用懶判 stale（見
// adminListBaseCTE），與點進詳情頁的 Status 同一口徑。AccountCode 僅後台可見（面向玩家的 API
// 一律不得回傳他人帳號編碼）。
type AdminCalibRow struct {
	UserID      string `json:"user_id"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	AccountCode string `json:"account_code"`
	// ApplyEntry/EffectiveFactor/Applied/NotApplyReason 語意同 Status 的同名欄位（見那裡的註解）：
	// Factor 只是估計值，EffectiveFactor 才是實際入帳的係數。列表一定要同時給，否則影子模式下的
	// 會員（entry != shown）與自己關掉校正的會員（enabled=false）會跟真正在校正的人長得一模一樣。
	ApplyEntry      string     `json:"apply_entry"`
	EffectiveFactor float64    `json:"effective_factor"`
	Applied         bool       `json:"applied"`
	NotApplyReason  string     `json:"not_apply_reason,omitempty"`
	Factor          float64    `json:"factor"`
	Status          string     `json:"status"`
	Enabled         bool       `json:"enabled"`
	RefSource       string     `json:"ref_source"`
	NPairs          int        `json:"n_pairs"`
	NEff            float64    `json:"n_eff"`
	Sigma           float64    `json:"sigma"`
	LastPairAt      *time.Time `json:"last_pair_at,omitempty"`
	ComputedAt      *time.Time `json:"computed_at,omitempty"`
	Version         int        `json:"version"`
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
		Entry:      ResolveEntry(ctx, db, email, code, isSuperAdmin),
		ApplyEntry: resolveApplyEntry(ctx, db, email, code),
		Enabled:    true, Factor: 1.0, Status: "warming",
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
	hasRow := !errors.Is(err, pgx.ErrNoRows)
	if sigma != nil {
		st.Sigma = *sigma
	}

	now := time.Now()
	// 生效係數（對抗式審查修正，high finding）：走與 GPS 上傳同一份 effectiveState，把「入口非
	// shown 的影子模式」「使用者自己關掉」「非 active」「懶判 stale」四種都算進去。必須在下面的
	// 懶判 stale 覆寫 st.Status/st.Factor **之前**算，才拿得到 DB 原值、分得出 stale 與其他原因。
	es := effectiveState(st.ApplyEntry, hasRow, st.Enabled, st.Status, st.Factor, st.LastPairAt, now)
	st.EffectiveFactor = es.Factor
	st.Applied = es.Applied
	st.NotApplyReason = es.Reason
	// 對抗式審查修正（low-2 finding）：EffectiveFactor/DashboardSummary 都會在讀取端懶判「status='active'
	// 但 last_pair_at 已超過 StaleDays 未更新」→ 視為 stale/1.0（不寫 DB，等下次真的有新配對觸發
	// Recompute 才落地）；GetStatus 之前是直接回傳 DB 原值，使用者停用手錶、Recompute 不再被觸發後，
	// 這裡會永遠顯示「校正中 ×0.97xx」，跟實際上 GPS 上傳早已不套用（EffectiveFactor 回 1.0）矛盾。
	if st.Status == "active" && st.LastPairAt != nil && now.Sub(*st.LastPairAt) > StaleDays*24*time.Hour {
		st.Status = "stale"
		st.Factor = 1.0
	}

	// LEFT JOIN gps_runs 取「這趟當時真正入帳」的係數與距離（對抗式審查修正，high finding）：
	// 每趟的係數是上傳當下凍結的，跟今天的係數不是同一個數字，不能拿現在的係數回推當成事實。
	// FK 保證 gps_run 必存在，仍用 LEFT JOIN 保守處理（缺列時退回原始距離／1.0）。
	rows, err := db.Query(ctx, `
		SELECT p.activity_at, p.ext_source, p.gps_km, p.ext_km, p.accepted, COALESCE(p.reject_reason,''), p.inlier_w,
		       p.gps_run_id::text, p.ext_activity_id::text, r.calib_factor, r.calib_distance_km
		FROM gps_calib_pairs p
		LEFT JOIN gps_runs r ON r.id = p.gps_run_id
		WHERE p.user_id=$1 ORDER BY p.activity_at DESC LIMIT $2`, userID, pairLimit)
	if err != nil {
		return nil, err
	}
	windowCutoff := now.Add(-WindowDays * 24 * time.Hour)
	for rows.Next() {
		var pv PairView
		var creditedFactor, creditedKm *float64
		if err := rows.Scan(&pv.ActivityAt, &pv.ExtSource, &pv.GpsKm, &pv.ExtKm, &pv.Accepted, &pv.RejectReason, &pv.InlierW,
			&pv.GpsRunID, &pv.ExtActivityID, &creditedFactor, &creditedKm); err != nil {
			rows.Close()
			return nil, err
		}
		if pv.GpsKm > 0 {
			pv.Ratio = pv.ExtKm / pv.GpsKm
		}
		// 實際入帳：gps_runs.calib_distance_km 為 NULL＝當時沒套校正，入帳的就是原始距離。
		pv.CreditedFactor, pv.CreditedKm = 1.0, pv.GpsKm
		if creditedFactor != nil {
			pv.CreditedFactor = *creditedFactor
		}
		if creditedKm != nil {
			pv.CreditedKm = *creditedKm
		}
		// 回推（back-test）：用**目前生效**係數重算，供後台評估現在這組係數會不會更貼近手錶。
		// 用 EffectiveFactor 而非 st.Factor，否則影子模式/使用者關閉時會回推出一個根本不會發生的值。
		pv.CalibKm = round2(pv.GpsKm * st.EffectiveFactor)
		pv.InWindow = pv.Accepted && pv.InlierW != nil && !pv.ActivityAt.Before(windowCutoff)
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
