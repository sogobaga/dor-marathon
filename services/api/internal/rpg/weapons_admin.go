// weapons_admin.go：DORPG P7 後台「遊戲化」新分頁的武器類型／武器 CRUD，比照 battle_admin.go
// 既有 CRUD 結構（AdminMonstersRouter 等）——main.go 把這兩個 Router 掛在
// "/admin/rpg/weapon-types"／"/admin/rpg/weapons" 靜態子路徑，跟既有 "/admin/rpg" mount 共存，
// 沿用同一個 perm("rpg")。
package rpg

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// AdminWeaponTypesRouter GET "/" 列表、PUT "/" 單筆 upsert、DELETE "/{id}"。
func (h *Handler) AdminWeaponTypesRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListWeaponTypes)
	r.Put("/", h.AdminPutWeaponType)
	r.Delete("/{id}", h.AdminDeleteWeaponType)
	return r
}

func (h *Handler) AdminListWeaponTypes(w http.ResponseWriter, r *http.Request) {
	list, err := h.listWeaponTypes(r.Context())
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errWeaponsNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to list weapon types")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"weapon_types": list})
}

func (h *Handler) AdminPutWeaponType(w http.ResponseWriter, r *http.Request) {
	var t WeaponTypeRow
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := t.Validate(); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.upsertWeaponType(r.Context(), t); err != nil {
		if respondIfMissingRelationMsg(w, err, errWeaponsNotReady) {
			return
		}
		if isForeignKeyViolation(err) {
			respondErr(w, http.StatusBadRequest, "job_id 不存在")
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	respondJSON(w, http.StatusOK, t)
}

func (h *Handler) AdminDeleteWeaponType(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ok, err := h.deleteWeaponType(r.Context(), id)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errWeaponsNotReady) {
			return
		}
		if isForeignKeyViolation(err) {
			respondErr(w, http.StatusBadRequest, "這個武器類型仍有武器使用中，請先刪除該類型下的所有武器")
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

// AdminWeaponsRouter 同上結構。
func (h *Handler) AdminWeaponsRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListWeapons)
	r.Put("/", h.AdminPutWeapon)
	r.Delete("/{id}", h.AdminDeleteWeapon)
	return r
}

func (h *Handler) AdminListWeapons(w http.ResponseWriter, r *http.Request) {
	list, err := h.listWeapons(r.Context(), false)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errWeaponsNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to list weapons")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"weapons": list})
}

// weaponPutBody PUT /admin/rpg/weapons 的請求 body——profile 先收成 json.RawMessage 再交給
// ParseWeaponProfile（缺欄位＝中性值＋詞彙/範圍驗證），跟 scanWeapon 從 DB jsonb 讀出來的路徑
// 共用同一份 defaulting/validation 邏輯，不再各自維護一份。
type weaponPutBody struct {
	ID          string          `json:"id"`
	TypeID      string          `json:"type_id"`
	Tier        int             `json:"tier"`
	Name        string          `json:"name"`
	Rarity      string          `json:"rarity"`
	LevelReq    int             `json:"level_req"`
	Element     string          `json:"element"`
	Profile     json.RawMessage `json:"profile"`
	Description string          `json:"description"`
	IsActive    bool            `json:"is_active"`
	SortOrder   int             `json:"sort_order"`
}

func (h *Handler) AdminPutWeapon(w http.ResponseWriter, r *http.Request) {
	var body weaponPutBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	profile, err := ParseWeaponProfile(body.Profile)
	if err != nil {
		respondErr(w, http.StatusBadRequest, "invalid profile: "+err.Error())
		return
	}
	weapon := WeaponRow{
		ID: body.ID, TypeID: body.TypeID, Tier: body.Tier, Name: body.Name, Rarity: body.Rarity,
		LevelReq: body.LevelReq, Element: body.Element, Profile: profile, Description: body.Description,
		IsActive: body.IsActive, SortOrder: body.SortOrder,
	}
	if err := weapon.Validate(); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.upsertWeapon(r.Context(), weapon); err != nil {
		if respondIfMissingRelationMsg(w, err, errWeaponsNotReady) {
			return
		}
		if isForeignKeyViolation(err) {
			respondErr(w, http.StatusBadRequest, "type_id 不存在")
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	respondJSON(w, http.StatusOK, weapon)
}

func (h *Handler) AdminDeleteWeapon(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ok, err := h.deleteWeapon(r.Context(), id)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errWeaponsNotReady) {
			return
		}
		if isForeignKeyViolation(err) {
			respondErr(w, http.StatusBadRequest, "這把武器仍被玩家裝備中，請先確認沒有人裝備後再刪除")
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
