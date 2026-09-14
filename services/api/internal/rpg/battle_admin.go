// battle_admin.go：DORPG P2 後台「遊戲化」新分頁的內容 CRUD + 戰鬥數據（契約 §3.5）。
// 每張內容表各自一個 chi.Router，main.go 直接 Mount 在自己的靜態路徑（例如
// "/admin/rpg/monsters"）——不是塞進既有 admin.go 的 AdminRouter()（那支函式不在 BACKEND
// 可寫清單內），而是利用 chi 的路由樹「靜態子路徑優先於父層萬用字元」規則，讓這些新路徑
// 跟既有 "/admin/rpg" mount（config/entry/users/preview）在同一個父路由底下和平共存，
// 對外行為完全等同「掛在既有 AdminRouter() 之下」。main.go 掛載時一律套用同一個
// perm("rpg")，跟其餘 /admin/rpg/* 端點權限一致。
package rpg

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// ---------------------------------------------------------------------------
// 共用：缺表 → 503（見 content_repo.go errContentNotReady），已在 battle.go 定義
// respondIfMissingRelation，這裡直接沿用。
// ---------------------------------------------------------------------------

// AdminMonstersRouter GET "/" 列表、PUT "/" 單筆 upsert、DELETE "/{id}"。
func (h *Handler) AdminMonstersRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListMonsters)
	r.Put("/", h.AdminPutMonster)
	r.Delete("/{id}", h.AdminDeleteMonster)
	return r
}

func (h *Handler) AdminListMonsters(w http.ResponseWriter, r *http.Request) {
	list, err := h.listMonsters(r.Context(), false)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to list monsters")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"monsters": list})
}

func (h *Handler) AdminPutMonster(w http.ResponseWriter, r *http.Request) {
	var m MonsterRow
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := m.Validate(); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.upsertMonster(r.Context(), m); err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	respondJSON(w, http.StatusOK, m)
}

func (h *Handler) AdminDeleteMonster(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ok, err := h.deleteMonster(r.Context(), id)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		// 審查4 CONFIRMED：rpg_encounter_monsters.monster_id 參照這張表且沒有 ON DELETE 子句，
		// 刪除仍被某場遭遇使用中的怪物會撞 23503——原本一律回通用 500，管理者無法得知真正
		// 原因，這裡改回可讀的 400。
		if isForeignKeyViolation(err) {
			respondErr(w, http.StatusBadRequest, "這隻怪仍被遭遇使用中，請先從遭遇編組移除")
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to delete")
		return
	}
	if !ok {
		respondErr(w, http.StatusNotFound, "not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// AdminSkillsRouter 同上結構。
func (h *Handler) AdminSkillsRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListSkills)
	r.Put("/", h.AdminPutSkill)
	r.Delete("/{id}", h.AdminDeleteSkill)
	return r
}

func (h *Handler) AdminListSkills(w http.ResponseWriter, r *http.Request) {
	list, err := h.listSkills(r.Context(), false)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to list skills")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"skills": list})
}

func (h *Handler) AdminPutSkill(w http.ResponseWriter, r *http.Request) {
	var s SkillRow
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.Validate(); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.upsertSkill(r.Context(), s); err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	respondJSON(w, http.StatusOK, s)
}

func (h *Handler) AdminDeleteSkill(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ok, err := h.deleteSkill(r.Context(), id)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to delete")
		return
	}
	if !ok {
		respondErr(w, http.StatusNotFound, "not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// AdminItemsRouter 同上結構。
func (h *Handler) AdminItemsRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListItems)
	r.Put("/", h.AdminPutItem)
	r.Delete("/{id}", h.AdminDeleteItem)
	return r
}

func (h *Handler) AdminListItems(w http.ResponseWriter, r *http.Request) {
	list, err := h.listItems(r.Context(), false)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to list items")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (h *Handler) AdminPutItem(w http.ResponseWriter, r *http.Request) {
	var it ItemRow
	if err := json.NewDecoder(r.Body).Decode(&it); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := it.Validate(); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.upsertItem(r.Context(), it); err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	respondJSON(w, http.StatusOK, it)
}

func (h *Handler) AdminDeleteItem(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ok, err := h.deleteItem(r.Context(), id)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to delete")
		return
	}
	if !ok {
		respondErr(w, http.StatusNotFound, "not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// AdminScenesRouter 同上結構。
func (h *Handler) AdminScenesRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListScenes)
	r.Put("/", h.AdminPutScene)
	r.Delete("/{id}", h.AdminDeleteScene)
	return r
}

func (h *Handler) AdminListScenes(w http.ResponseWriter, r *http.Request) {
	list, err := h.listScenes(r.Context(), false)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to list scenes")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"scenes": list})
}

func (h *Handler) AdminPutScene(w http.ResponseWriter, r *http.Request) {
	var s SceneRow
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.Validate(); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.upsertScene(r.Context(), s); err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	respondJSON(w, http.StatusOK, s)
}

func (h *Handler) AdminDeleteScene(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ok, err := h.deleteScene(r.Context(), id)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		// 審查4 CONFIRMED：rpg_encounters.scene_id 參照這張表且沒有 ON DELETE 子句，理由同
		// AdminDeleteMonster。
		if isForeignKeyViolation(err) {
			respondErr(w, http.StatusBadRequest, "這個場景仍被遭遇使用中，請先改用其他場景或刪除該遭遇")
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to delete")
		return
	}
	if !ok {
		respondErr(w, http.StatusNotFound, "not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// AdminCompanionsRouter 同上結構，upsert 多一個「玩家頭像唯一」的衝突處理。
func (h *Handler) AdminCompanionsRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListCompanions)
	r.Put("/", h.AdminPutCompanion)
	r.Delete("/{id}", h.AdminDeleteCompanion)
	return r
}

func (h *Handler) AdminListCompanions(w http.ResponseWriter, r *http.Request) {
	list, err := h.listCompanions(r.Context(), false)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to list companions")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"companions": list})
}

func (h *Handler) AdminPutCompanion(w http.ResponseWriter, r *http.Request) {
	var c CompanionRow
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := c.Validate(); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.upsertCompanion(r.Context(), c); err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		if errors.Is(err, errPlayerPortraitTaken) {
			respondErr(w, http.StatusBadRequest, err.Error())
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	respondJSON(w, http.StatusOK, c)
}

func (h *Handler) AdminDeleteCompanion(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ok, err := h.deleteCompanion(r.Context(), id)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to delete")
		return
	}
	if !ok {
		respondErr(w, http.StatusNotFound, "not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// AdminEncountersRouter GET "/" 含編組、PUT "/" upsert（單一交易先刪後插編組）、DELETE "/{code}"。
func (h *Handler) AdminEncountersRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListEncounters)
	r.Put("/", h.AdminPutEncounter)
	r.Delete("/{code}", h.AdminDeleteEncounter)
	return r
}

func (h *Handler) AdminListEncounters(w http.ResponseWriter, r *http.Request) {
	list, err := h.listEncounters(r.Context(), false)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to list encounters")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"encounters": list})
}

func (h *Handler) AdminPutEncounter(w http.ResponseWriter, r *http.Request) {
	var e EncounterRow
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := e.Validate(); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.upsertEncounter(r.Context(), e); err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	respondJSON(w, http.StatusOK, e)
}

func (h *Handler) AdminDeleteEncounter(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	ok, err := h.deleteEncounter(r.Context(), code)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to delete")
		return
	}
	if !ok {
		respondErr(w, http.StatusNotFound, "not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- 戰鬥數據（單一 GET 端點，main.go 直接掛 perm("rpg").Get，不需要獨立 Router）---

// clampQueryInt 讀 query string 的整數參數並夾在 [min,max]（缺省/非法值一律回 def）。
func clampQueryInt(r *http.Request, key string, def, min, max int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

// AdminBattleLogs GET /admin/rpg/battle-logs?code=&days=7&limit=100。不得回傳 email/
// account_code（隱私規則），listBattleLogsAdmin 的 SQL 只 SELECT COALESCE(name,handle)。
func (h *Handler) AdminBattleLogs(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	days := clampQueryInt(r, "days", 7, 1, 90)
	limit := clampQueryInt(r, "limit", 100, 1, 1000)

	rows, summary, err := h.listBattleLogsAdmin(r.Context(), code, days, limit)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load battle logs")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"rows": rows, "summary": summary})
}
