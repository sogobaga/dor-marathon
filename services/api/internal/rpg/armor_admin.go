// armor_admin.go：DORPG P8 後台「遊戲化」新分頁的防具/飾品 CRUD，比照 weapons_admin.go 既有結構
// ——main.go 把這個 Router 掛在 "/admin/rpg/armor-items" 靜態子路徑，跟既有 "/admin/rpg" mount
// 共存，沿用同一個 perm("rpg")。
package rpg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// AdminArmorItemsRouter GET "/"（可 ?job_id=／?slot= 篩選）、PUT "/" 單筆 upsert、DELETE "/{id}"。
func (h *Handler) AdminArmorItemsRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListArmorItems)
	r.Put("/", h.AdminPutArmorItem)
	r.Delete("/{id}", h.AdminDeleteArmorItem)
	return r
}

// AdminListArmorItems ?job_id=<職業id>|none（none＝只要通用飾品）、?slot=<helmet|gloves|armor|legs|boots|accessory>，
// 兩者都可省略（不篩選）或同時給（AND）。
func (h *Handler) AdminListArmorItems(w http.ResponseWriter, r *http.Request) {
	jobFilter := r.URL.Query().Get("job_id")
	slotFilter := r.URL.Query().Get("slot")
	list, err := h.listAllArmorItems(r.Context(), jobFilter, slotFilter)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errArmorNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to list armor items")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"armor_items": list})
}

// armorPutBody PUT /admin/rpg/armor-items 的請求 body——profile 先收成 json.RawMessage 再交給
// ParseArmorProfile（缺欄位＝中性值＋詞彙/範圍驗證），跟 scanArmor 從 DB jsonb 讀出來的路徑共用
// 同一份 defaulting/validation 邏輯，比照 weapons_admin.go AdminPutWeapon 的既有慣例。
type armorPutBody struct {
	ID          string          `json:"id"`
	JobID       *string         `json:"job_id"`
	Slot        string          `json:"slot"`
	Tier        int             `json:"tier"`
	Name        string          `json:"name"`
	Rarity      string          `json:"rarity"`
	LevelReq    int             `json:"level_req"`
	Profile     json.RawMessage `json:"profile"`
	Description string          `json:"description"`
	IsActive    bool            `json:"is_active"`
	SortOrder   int             `json:"sort_order"`
}

// ParseArmorProfileStrict 只給後台 PUT 這一處用（下面 AdminPutArmorItem）：多一層
// DisallowUnknownFields，把「打錯欄位名（例如少一個字母）」擋成 400，而不是像 armor.go
// ParseArmorProfile（玩家端 GET /rpg/equipment、scanArmor 讀 DB 都在用）那樣靜默補中性值——
// 後台是唯一的寫入路徑，打錯字不該無聲無息地存進一筆「什麼都不生效」的防具，事後很難查。讀取端
// 刻意保持寬鬆不動：舊資料如果曾經存過已經改名/移除的欄位，不能因為這樣就讓玩家端 500（見
// armor.go scanArmor 對壞掉 profile 的既有處理）。
func ParseArmorProfileStrict(raw []byte) (ArmorProfile, error) {
	p := DefaultArmorProfile()
	if len(raw) == 0 {
		return p, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return ArmorProfile{}, fmt.Errorf("invalid armor profile json: %w", err)
	}
	if err := p.Validate(); err != nil {
		return ArmorProfile{}, err
	}
	return p, nil
}

func (h *Handler) AdminPutArmorItem(w http.ResponseWriter, r *http.Request) {
	var body armorPutBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	profile, err := ParseArmorProfileStrict(body.Profile)
	if err != nil {
		respondErr(w, http.StatusBadRequest, "invalid profile: "+err.Error())
		return
	}
	item := ArmorRow{
		ID: body.ID, JobID: body.JobID, Slot: body.Slot, Tier: body.Tier, Name: body.Name, Rarity: body.Rarity,
		LevelReq: body.LevelReq, Profile: profile, Description: body.Description,
		IsActive: body.IsActive, SortOrder: body.SortOrder,
	}
	if err := item.Validate(); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.upsertArmorItem(r.Context(), item); err != nil {
		if respondIfMissingRelationMsg(w, err, errArmorNotReady) {
			return
		}
		if isForeignKeyViolation(err) {
			respondErr(w, http.StatusBadRequest, "job_id 不存在")
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	respondJSON(w, http.StatusOK, item)
}

// AdminDeleteArmorItem CONTRACT §1：player_equipment.item_id 沒有 FK 到 rpg_armor_items（同一
// 欄位可能指向 rpg_weapons，無法用單一 FK 表達，見 migration 184 註解），刪除一件被玩家裝備中的
// 防具不會被資料庫擋下——遺留的 item_id 由讀取端「查無 item → 視為未裝備」的既有慣例吸收（見
// equipment.go getEquippedArmorMap），不是這裡要處理的錯誤，因此不像 AdminDeleteWeapon 那樣檢查
// isForeignKeyViolation。
func (h *Handler) AdminDeleteArmorItem(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ok, err := h.deleteArmorItem(r.Context(), id)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errArmorNotReady) {
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
