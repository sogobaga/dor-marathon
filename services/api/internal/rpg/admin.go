// admin.go：後台「遊戲化」頁端點（perm scope "rpg"，main.go 掛 RequirePerm("rpg")，本檔不再
// 重複判權限）。三個分頁對應：參數設定(config)、入口與VVIP(users/vvip/reset/job-level，
// entry_state/whitelist 走既有 /admin/app-settings 通用機制，見 appsettings.go specs 註冊)、
// 預覽計算(preview)。
package rpg

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/dor/api/internal/appsettings"
)

// AdminRouter 掛 /admin/rpg（main.go 外層已套 RequirePerm("rpg")）。
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/config", h.AdminGetConfig)
	r.Put("/config", h.AdminPutConfig)
	r.Get("/entry", h.AdminGetEntry)
	r.Put("/entry", h.AdminPutEntry)
	r.Get("/users", h.AdminListUsers)
	r.Post("/users/{id}/vvip", h.AdminSetVVIP)
	r.Post("/users/{id}/reset", h.AdminResetCharacter)
	r.Post("/users/{id}/job-level", h.AdminSetJobLevel)
	r.Get("/preview", h.AdminPreview)
	return r
}

type configResponse struct {
	Config   Config `json:"config"`
	Defaults Config `json:"defaults"`
}

// GET /admin/rpg/config
func (h *Handler) AdminGetConfig(w http.ResponseWriter, r *http.Request) {
	raw := appsettings.GetString(r.Context(), h.db, "rpg_config", "")
	cfg, err := ParseConfig(raw)
	if err != nil {
		// 系統設定裡存的值理論上都經過本檔 AdminPutConfig 的 Validate 才寫入，這裡查無效值只可能是
		// 資料被外力改壞——保守退回預設值,不讓後台整頁因此打不開。
		cfg = DefaultConfig()
	}
	respondJSON(w, http.StatusOK, configResponse{Config: cfg, Defaults: DefaultConfig()})
}

// PUT /admin/rpg/config {"config": {...}}：直接 upsert app_settings（比照 profile.PutCheerLayout
// 的寫法，不透過 /admin/app-settings 通用 Set handler——這裡是專屬頁面，逐欄位驗證邏輯在
// rpg.Config.Validate，不需要再過一層 appsettings specs 泛用字串驗證器）。
func (h *Handler) AdminPutConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Config Config `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := body.Config.Validate(); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid config: "+err.Error())
		return
	}
	raw, err := json.Marshal(body.Config)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if _, err := h.db.Exec(r.Context(), `
		INSERT INTO app_settings (key, value, updated_at) VALUES ('rpg_config', $1, NOW())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`,
		string(raw)); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	// 清掉 60 秒記憶體快取：後台儲存後緊接著切到「預覽計算」分頁或會員端 /rpg/me 若還吃到舊值，
	// 會讓管理者以為存檔沒生效（比照 profile.PutCheerLayout 同一慣例）。
	appsettings.InvalidateCache()
	respondJSON(w, http.StatusOK, configResponse{Config: body.Config, Defaults: DefaultConfig()})
}

// entryResponse GET/PUT /admin/rpg/entry 回傳形狀。
type entryResponse struct {
	State     string `json:"state"`
	Whitelist string `json:"whitelist"`
}

// GET/PUT /admin/rpg/entry：入口狀態＋白名單，獨立於通用 /admin/app-settings 之外。
//
// 修 code review finding（critical + major）：EntryTab 原本借用 adminAppSettingsApi 打
// /admin/app-settings，那條路徑掛 perm("settings")，跟這整頁其餘端點的 perm("rpg") 不同組
// ——只勾「遊戲化」權限的管理者會在「入口與 VVIP」分頁直接 403，等於拿不到這頁存在的核心目的
// （開關入口）。且通用 /admin/app-settings 的 isEntryState 驗證器只收 hidden/locked/whitelist/
// open/off，並不接受字面 "shown"（appsettings.go specs 註冊處的註解其實已經寫明 rpg 該存
// hidden/whitelist/shown 三態，只是先前沒有专属端點落實），導致前端「全部開放」選項送出
// state="shown" 一律被 400 擋下、永遠存不進去。
//
// 這裡直接讀寫 app_settings 的 rpg_entry_state/rpg_entry_whitelist 兩個 key（跟 AdminGetConfig/
// AdminPutConfig 對 rpg_config 的做法一樣繞過泛用驗證器），值域收斂成 entry.go ResolveEntry
// 真正認得的三態，兩邊語意保證一致。
func (h *Handler) AdminGetEntry(w http.ResponseWriter, r *http.Request) {
	state := appsettings.GetString(r.Context(), h.db, EntryStateKey, "hidden")
	wl := appsettings.GetString(r.Context(), h.db, EntryWhitelistKey, "")
	respondJSON(w, http.StatusOK, entryResponse{State: state, Whitelist: wl})
}

func (h *Handler) AdminPutEntry(w http.ResponseWriter, r *http.Request) {
	var body struct {
		State     string `json:"state"`
		Whitelist string `json:"whitelist"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	state := strings.TrimSpace(body.State)
	if state != "hidden" && state != "whitelist" && state != "shown" {
		respondErr(w, http.StatusBadRequest, "state must be hidden, whitelist or shown")
		return
	}
	if len(body.Whitelist) > 20000 { // 比照 appsettings.go isWhitelist 的長度上限
		respondErr(w, http.StatusBadRequest, "whitelist too long")
		return
	}
	if err := h.setAppSetting(r.Context(), EntryStateKey, state); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	if err := h.setAppSetting(r.Context(), EntryWhitelistKey, body.Whitelist); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	// 清記憶體快取：緊接著切到會員端 /rpg/me 或另一位管理者的分頁若還吃到舊值，會誤以為沒存到
	// （比照 AdminPutConfig 同一慣例）。
	appsettings.InvalidateCache()
	respondJSON(w, http.StatusOK, entryResponse{State: state, Whitelist: body.Whitelist})
}

// setAppSetting upsert 單一 app_settings key（AdminPutEntry 兩個 key 共用，避免重複 SQL）。
func (h *Handler) setAppSetting(ctx context.Context, key, value string) error {
	_, err := h.db.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES ($1,$2,NOW())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`, key, value)
	return err
}

// adminUserRow 後台會員搜尋列一筆（json tag 對齊前端 apps/web/src/lib/api.ts AdminRpgUser）。
type adminUserRow struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	IsVVIP       bool   `json:"is_vvip"`
	HasCharacter bool   `json:"has_character"`
	FreePoints   int    `json:"free_points"`
	JobLevel     int    `json:"job_level"`
	Stats        *Stats `json:"stats,omitempty"`
}

// GET /admin/rpg/users?q=（依 email/顯示名稱/帳號 handle 搜尋，最多 50 筆）
func (h *Handler) AdminListUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	like := "%" + q + "%"
	rows, err := h.db.Query(r.Context(), `
		SELECT u.id, COALESCE(u.name, u.handle) AS display_name, u.email, u.is_vvip,
		       pc.user_id IS NOT NULL AS has_character,
		       COALESCE(pc.free_points, 0), COALESCE(pc.job_level, 1),
		       COALESCE(pc.str_pt, 0), COALESCE(pc.agi_pt, 0), COALESCE(pc.vit_pt, 0),
		       COALESCE(pc.dex_pt, 0), COALESCE(pc.int_pt, 0), COALESCE(pc.luk_pt, 0)
		FROM users u
		LEFT JOIN player_characters pc ON pc.user_id = u.id
		WHERE $1 = '' OR u.email ILIKE $2 OR u.name ILIKE $2 OR u.handle ILIKE $2
		ORDER BY u.created_at DESC
		LIMIT 50`, q, like)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	defer rows.Close()

	users := []adminUserRow{}
	for rows.Next() {
		var u adminUserRow
		var s Stats
		if err := rows.Scan(&u.ID, &u.Name, &u.Email, &u.IsVVIP, &u.HasCharacter, &u.FreePoints, &u.JobLevel,
			&s.Str, &s.Agi, &s.Vit, &s.Dex, &s.Int, &s.Luk); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to scan users")
			return
		}
		if u.HasCharacter {
			u.Stats = &s
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"users": users})
}

// POST /admin/rpg/users/{id}/vvip {"on": true|false}
func (h *Handler) AdminSetVVIP(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ct, err := h.db.Exec(r.Context(), `UPDATE users SET is_vvip=$1 WHERE id=$2`, body.On, id)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if ct.RowsAffected() == 0 {
		respondErr(w, http.StatusNotFound, "user not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /admin/rpg/users/{id}/reset：六圍重置為初始值、free_points 退回「初始值 + 目前總花費」
// （即使目前套用的 Config 已改過係數，退款金額仍以「當初實際扣掉多少」為準——重算 TotalSpent
// 用的是現在的 Config，這是刻意的簡化：本階段唯一花費來源就是加點，退回「照目前公式重算的花費」
// 與「歷史實際花費」在係數未變動時完全一致，只有管理者剛好在同一批操作內先改公式才會有出入，
// 影響範圍小且可由 player_stat_log 追溯）。寫 player_stat_log 稽核列（cost 為負值＝退點）。
func (h *Handler) AdminResetCharacter(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cfg, err := h.loadConfig(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	ch, err := h.getOrCreateCharacter(r.Context(), id, cfg)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}
	totalSpent := spentBetween(cfg, cfg.InitialStat, ch.Str) +
		spentBetween(cfg, cfg.InitialStat, ch.Agi) +
		spentBetween(cfg, cfg.InitialStat, ch.Vit) +
		spentBetween(cfg, cfg.InitialStat, ch.Dex) +
		spentBetween(cfg, cfg.InitialStat, ch.Int) +
		spentBetween(cfg, cfg.InitialStat, ch.Luk)
	newFreePoints := cfg.InitialFreePoints + totalSpent

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	if _, err := tx.Exec(r.Context(), `
		UPDATE player_characters
		SET str_pt=$1, agi_pt=$1, vit_pt=$1, dex_pt=$1, int_pt=$1, luk_pt=$1, free_points=$2, updated_at=NOW()
		WHERE user_id=$3`, cfg.InitialStat, newFreePoints, id); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to reset")
		return
	}
	stats := []struct {
		key  string
		from int
	}{{"str", ch.Str}, {"agi", ch.Agi}, {"vit", ch.Vit}, {"dex", ch.Dex}, {"int", ch.Int}, {"luk", ch.Luk}}
	for _, s := range stats {
		if s.from == cfg.InitialStat {
			continue // 本來就在初始值，沒有變化不必留稽核列
		}
		cost := -spentBetween(cfg, cfg.InitialStat, s.from) // 負值＝退點
		if _, err := tx.Exec(r.Context(), `INSERT INTO player_stat_log (user_id, stat, from_value, to_value, cost) VALUES ($1,$2,$3,$4,$5)`,
			id, s.key, s.from, cfg.InitialStat, cost); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to log")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to commit")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /admin/rpg/users/{id}/job-level {"job_level": N}
func (h *Handler) AdminSetJobLevel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		JobLevel int `json:"job_level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.JobLevel < 1 || body.JobLevel > 999 {
		respondErr(w, http.StatusBadRequest, "job_level must be within 1..999")
		return
	}
	cfg, err := h.loadConfig(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	// 確保角色列存在（管理者可能在會員本人從未打開過角色頁前就先設定職業等級）。
	if _, err := h.getOrCreateCharacter(r.Context(), id, cfg); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}
	ct, err := h.db.Exec(r.Context(), `UPDATE player_characters SET job_level=$1, updated_at=NOW() WHERE user_id=$2`, body.JobLevel, id)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if ct.RowsAffected() == 0 {
		respondErr(w, http.StatusNotFound, "character not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GET /admin/rpg/preview?base_level&job_level&str&agi&vit&dex&int&luk → 用目前生效的 Config
// 純算一次，不碰任何使用者列（後台調參數時即時看效果用）。
func (h *Handler) AdminPreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := ComputeInput{
		BaseLevel: queryInt(q, "base_level"),
		JobLevel:  queryInt(q, "job_level"),
		Stats: Stats{
			Str: queryInt(q, "str"),
			Agi: queryInt(q, "agi"),
			Vit: queryInt(q, "vit"),
			Dex: queryInt(q, "dex"),
			Int: queryInt(q, "int"),
			Luk: queryInt(q, "luk"),
		},
	}
	cfg, err := h.loadConfig(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	d := Compute(cfg, in)
	respondJSON(w, http.StatusOK, map[string]any{
		"derived":   d,
		"max_hp":    d.MaxHP,
		"max_mp":    d.MaxMP,
		"next_cost": d.NextCost,
	})
}

// maxPreviewInt queryInt 的上限防呆（code review finding：原本只擋負值、不擋上限，Compute()
// 裡 TotalSpent 靠 spentBetween 做 O(n) 迴圈——GET /admin/rpg/preview?str=2000000000 這種輸入
// 會讓單一請求跑約 20 億次迴圈卡住一個 goroutine。這支端點雖然要 perm("rpg") 才能打，仍是
// 合法管理者輸入錯誤/帳號外洩時的放大器，不該無上限。10 萬對 O(n) 迴圈仍是毫秒等級，也遠超過
// max_stat（預設 99）方便管理者試算「假設上限開更高」的極端係數。）
const maxPreviewInt = 100000

func queryInt(q map[string][]string, key string) int {
	v := ""
	if vs, ok := q[key]; ok && len(vs) > 0 {
		v = vs[0]
	}
	n, _ := strconv.Atoi(v)
	if n < 0 {
		n = 0
	}
	if n > maxPreviewInt {
		n = maxPreviewInt
	}
	return n
}
