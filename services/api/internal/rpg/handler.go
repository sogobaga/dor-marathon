// handler.go：遊戲化角色數值 HTTP 端點——會員自助 GET /rpg/me（查詢/lazy-create 角色列）、
// POST /rpg/allocate（加點）。整個 Router() 掛套件私有 requireEntry：非白名單（且非 VVIP/
// super_admin）一律 403（SEC-H5 同款：前端 UI 隱藏不等於後端有擋，比照 monopoly/gpscalib 前例）。
package rpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

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

// requireEntry 後端強制入口白名單（SEC-H5）：非 shown 一律 403，不論前端有沒有把按鈕藏起來。
func (h *Handler) requireEntry(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
		if uid == "" {
			respondErr(w, http.StatusUnauthorized, "login required")
			return
		}
		id, err := resolveIdentity(r.Context(), h.db, uid)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to resolve access")
			return
		}
		state := appsettings.GetString(r.Context(), h.db, EntryStateKey, "hidden")
		wl := appsettings.GetString(r.Context(), h.db, EntryWhitelistKey, "")
		if ResolveEntry(state, wl, id.IsVVIP, id.Email, id.Code, id.IsSuperAdmin) != "shown" {
			respondErr(w, http.StatusForbidden, "not available")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Router 掛 /rpg（main.go 內 Mount 路徑）：GET /me、POST /allocate，兩者皆吃 requireEntry。
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(h.requireEntry)
	r.Get("/me", h.Me)
	r.Post("/allocate", h.Allocate)
	return r
}

func (h *Handler) loadConfig(ctx context.Context) (Config, error) {
	raw := appsettings.GetString(ctx, h.db, "rpg_config", "")
	return ParseConfig(raw)
}

// character 角色列（player_characters）的 DB 對應。
type character struct {
	Str, Agi, Vit, Dex, Int, Luk int
	FreePoints                   int
	JobLevel                     int
	JobExp                       int
}

const characterCols = `str_pt, agi_pt, vit_pt, dex_pt, int_pt, luk_pt, free_points, job_level, job_exp`

func scanCharacter(row pgx.Row) (character, error) {
	var c character
	err := row.Scan(&c.Str, &c.Agi, &c.Vit, &c.Dex, &c.Int, &c.Luk, &c.FreePoints, &c.JobLevel, &c.JobExp)
	return c, err
}

// getOrCreateCharacter 查角色列；查無則依 cfg 的初始值 lazy-create（D5：「Lazy-create the
// character row on first /me for allowed users」）。ON CONFLICT DO NOTHING + 重讀一次應付併發
// （兩個分頁同時第一次打開角色頁）。
func (h *Handler) getOrCreateCharacter(ctx context.Context, userID string, cfg Config) (character, error) {
	row := h.db.QueryRow(ctx, `SELECT `+characterCols+` FROM player_characters WHERE user_id=$1`, userID)
	c, err := scanCharacter(row)
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return character{}, err
	}
	_, err = h.db.Exec(ctx, `
		INSERT INTO player_characters (user_id, str_pt, agi_pt, vit_pt, dex_pt, int_pt, luk_pt, free_points, job_level, job_exp)
		VALUES ($1,$2,$2,$2,$2,$2,$2,$3,1,0)
		ON CONFLICT (user_id) DO NOTHING`, userID, cfg.InitialStat, cfg.InitialFreePoints)
	if err != nil {
		return character{}, err
	}
	row = h.db.QueryRow(ctx, `SELECT `+characterCols+` FROM player_characters WHERE user_id=$1`, userID)
	return scanCharacter(row)
}

// --- Wire 型別（json tag 對齊前端 apps/web/src/lib/api.ts 的 RpgMe/RpgCharacter，勿自行改名）---

type meResponse struct {
	Enabled   bool           `json:"enabled"`
	Character *characterView `json:"character,omitempty"` // enabled=false 時省略
}

type characterView struct {
	BaseLevel  int            `json:"base_level"`
	JobLevel   int            `json:"job_level"`
	JobExp     int            `json:"job_exp"`
	Stats      Stats          `json:"stats"`
	FreePoints int            `json:"free_points"`
	NextCost   map[string]int `json:"next_cost"` // 已達上限的素質不出現在這裡（Partial<RpgStats>）
	MaxHP      int            `json:"max_hp"`
	MaxMP      int            `json:"max_mp"`
	Derived    Derived        `json:"derived"`
}

// buildCharacterView 角色列 + Config → 完整衍生數值（Me/Allocate 共用）。
func buildCharacterView(cfg Config, baseLevel int, ch character) characterView {
	stats := Stats{Str: ch.Str, Agi: ch.Agi, Vit: ch.Vit, Dex: ch.Dex, Int: ch.Int, Luk: ch.Luk}
	d := Compute(cfg, ComputeInput{BaseLevel: baseLevel, JobLevel: ch.JobLevel, Stats: stats})
	return characterView{
		BaseLevel:  baseLevel,
		JobLevel:   ch.JobLevel,
		JobExp:     ch.JobExp,
		Stats:      stats,
		FreePoints: ch.FreePoints,
		NextCost:   d.NextCost,
		MaxHP:      d.MaxHP,
		MaxMP:      d.MaxMP,
		Derived:    d,
	}
}

// GET /rpg/me
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	h.respondMe(w, r, uid)
}

// respondMe 查角色列（lazy-create）+ Base Level（依 users.exp 換算）→ 回完整 /me payload。
// Allocate 加點成功後也呼叫這支，回傳格式與 GET /me 完全一致（D5）。
func (h *Handler) respondMe(w http.ResponseWriter, r *http.Request, userID string) {
	ctx := r.Context()
	cfg, err := h.loadConfig(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	ch, err := h.getOrCreateCharacter(ctx, userID, cfg)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}
	var exp int
	if err := h.db.QueryRow(ctx, `SELECT COALESCE(exp,0) FROM users WHERE id=$1`, userID).Scan(&exp); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load user")
		return
	}
	baseLevel, err := baseLevelFromExp(ctx, h.db, exp)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load level config")
		return
	}
	view := buildCharacterView(cfg, baseLevel, ch)
	respondJSON(w, http.StatusOK, meResponse{Enabled: true, Character: &view})
}

type allocateRequest struct {
	Stat   string `json:"stat"`
	Points int    `json:"points"`
}

// statColumns 白名單：req.Stat 只會查出這 6 個固定欄位名之一，絕不會把使用者輸入直接拼進 SQL。
var statColumns = map[string]string{
	"str": "str_pt", "agi": "agi_pt", "vit": "vit_pt", "dex": "dex_pt", "int": "int_pt", "luk": "luk_pt",
}

// POST /rpg/allocate {"stat":"str","points":1..10}：依序計費（第 n 點成本可能不同，見
// compute.go pointCost）、SELECT...FOR UPDATE 鎖角色列防同一使用者連點造成的併發超花，
// free_points 不足或會超過 max_stat 一律拒絕，成功寫 player_stat_log 稽核列。
func (h *Handler) Allocate(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var req allocateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	col, ok := statColumns[req.Stat]
	if !ok {
		respondErr(w, http.StatusBadRequest, "invalid stat")
		return
	}
	if req.Points < 1 || req.Points > 10 {
		respondErr(w, http.StatusBadRequest, "points must be within 1..10")
		return
	}
	ctx := r.Context()
	cfg, err := h.loadConfig(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	// 防禦性 lazy-create：正常流程使用者一定先呼叫過 /me 才看得到配點按鈕，這裡防角色列不存在時
	// 下面的 SELECT...FOR UPDATE 直接落空。
	if _, err := h.getOrCreateCharacter(ctx, uid, cfg); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 成功路徑會先 Commit，Rollback 在那之後為 no-op

	var current, freePoints int
	selectQuery := fmt.Sprintf(`SELECT %s, free_points FROM player_characters WHERE user_id=$1 FOR UPDATE`, col)
	if err := tx.QueryRow(ctx, selectQuery, uid).Scan(&current, &freePoints); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}

	cost := 0
	newVal := current
	for i := 0; i < req.Points; i++ {
		if newVal >= cfg.MaxStat {
			respondErr(w, http.StatusBadRequest, "已達素質上限")
			return
		}
		cost += pointCost(cfg, newVal)
		newVal++
	}
	if cost > freePoints {
		respondErr(w, http.StatusBadRequest, "可配點數不足")
		return
	}

	updateQuery := fmt.Sprintf(`UPDATE player_characters SET %s=$1, free_points=free_points-$2, updated_at=NOW() WHERE user_id=$3`, col)
	if _, err := tx.Exec(ctx, updateQuery, newVal, cost, uid); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO player_stat_log (user_id, stat, from_value, to_value, cost) VALUES ($1,$2,$3,$4,$5)`,
		uid, req.Stat, current, newVal, cost); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to log")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to commit")
		return
	}
	h.respondMe(w, r, uid)
}
