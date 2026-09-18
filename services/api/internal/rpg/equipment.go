// equipment.go：DORPG P7/P8（CONTRACT §2/§5、WIRE）玩家裝備——player_equipment 表讀寫 + GET
// /rpg/equipment、PUT /rpg/equipment/weapon、PUT /rpg/equipment/{slot}（P8 五部位防具＋兩格
// 飾品）三支會員端點。掛在既有 Handler.Router() 底下，套用既有 requireEntry 白名單（比照
// handler.go/jobs.go 慣例）。ArmorProfile／EquipBonus／rpg_armor_items 的資料模型在 armor.go；
// 後台 CRUD 在 armor_admin.go；換職業卸下防具在 jobs.go。
package rpg

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/auth"
)

// ---------------------------------------------------------------------------
// player_equipment 讀寫
// ---------------------------------------------------------------------------

// getEquippedWeapon 目前裝備的武器 id；未裝備回空字串（不是錯誤——沒有裝備是合法狀態，比照
// character.JobID 為 nil 的既有慣例，這裡用空字串代表「無」，因為 item_id 是 TEXT 不是指標型別
// 更方便的場合）。
func (h *Handler) getEquippedWeaponID(ctx context.Context, userID string) (string, error) {
	var itemID string
	err := h.db.QueryRow(ctx, `SELECT item_id FROM player_equipment WHERE user_id=$1 AND slot='weapon'`, userID).Scan(&itemID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return itemID, err
}

// getEquippedWeaponDetail 未裝備時回 (nil, nil, nil)——呼叫端（/rpg/me、bootstrap）不必逐一判斷
// pgx.ErrNoRows，直接檢查回傳的指標是否為 nil 即可，比照 getPlayerPortraitCompanion 的既有慣例。
func (h *Handler) getEquippedWeaponDetail(ctx context.Context, userID string) (*WeaponRow, *WeaponTypeRow, error) {
	itemID, err := h.getEquippedWeaponID(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if itemID == "" {
		return nil, nil, nil
	}
	w, t, err := h.getWeaponWithType(ctx, itemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 髒資料：裝備指到一把已被刪除的武器（本輪沒有刪除保護，見 CONTRACT）——保守視為
			// 未裝備，不讓 /rpg/me 因此 500。
			return nil, nil, nil
		}
		return nil, nil, err
	}
	return &w, &t, nil
}

// setEquippedWeapon upsert 裝備列（唯一 slot='weapon'，PRIMARY KEY(user_id,slot) 保證同時只有
// 一把）。
func (h *Handler) setEquippedWeapon(ctx context.Context, userID, itemID string) error {
	_, err := h.db.Exec(ctx, `
		INSERT INTO player_equipment (user_id, slot, item_id, updated_at) VALUES ($1,'weapon',$2,NOW())
		ON CONFLICT (user_id, slot) DO UPDATE SET item_id=$2, updated_at=NOW()`, userID, itemID)
	return err
}

// clearEquippedWeapon 卸下（刪除該列）；沒有裝備時刪 0 列，視為成功（冪等）。
func (h *Handler) clearEquippedWeapon(ctx context.Context, userID string) error {
	_, err := h.db.Exec(ctx, `DELETE FROM player_equipment WHERE user_id=$1 AND slot='weapon'`, userID)
	return err
}

// getEquippedArmorMap DORPG P8：一次查出玩家 player_equipment 除了 weapon 以外的七格（五部位
// 防具＋兩格飾品）目前裝備的 rpg_armor_items 資料。查無 item（髒資料——例如後台刪除了一件玩家
// 正裝備中的防具，見 armor_admin.go AdminDeleteArmorItem 檔頭註解）的格子直接跳過不放進 map，
// 呼叫端視為未裝備（CONTRACT §1「讀取時查無 item → 視為未裝備（不 500，log 一行）」），不是
// 錯誤。回傳的 map key 是 player_equipment.slot（armorEquipSlots 七格之一），不是
// rpg_armor_items.slot。
func (h *Handler) getEquippedArmorMap(ctx context.Context, userID string) (map[string]*ArmorRow, error) {
	rows, err := h.db.Query(ctx, `SELECT slot, item_id FROM player_equipment WHERE user_id=$1 AND slot != 'weapon'`, userID)
	if err != nil {
		return nil, err
	}
	itemIDBySlot := map[string]string{}
	for rows.Next() {
		var slot, itemID string
		if err := rows.Scan(&slot, &itemID); err != nil {
			rows.Close()
			return nil, err
		}
		itemIDBySlot[slot] = itemID
	}
	closeErr := rows.Err()
	rows.Close()
	if closeErr != nil {
		return nil, closeErr
	}

	out := map[string]*ArmorRow{}
	for slot, itemID := range itemIDBySlot {
		item, err := h.getArmorByID(ctx, itemID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				log.Printf("rpg: player_equipment 指到不存在的防具/飾品 item_id=%s slot=%s user_id=%s，視為未裝備", itemID, slot, userID)
				continue
			}
			return nil, err
		}
		row := item
		out[slot] = &row
	}
	return out, nil
}

// loadPlayerEquipment 一次查出玩家目前完整裝備狀態（武器＋七格防具/飾品），見
// PlayerEquipmentSnapshot（armor.go）檔頭註解——Compute()／GET /rpg/equipment／PUT
// /rpg/equipment/{weapon,slot}／/rpg/me／戰鬥 bootstrap 共用同一份查詢結果組裝。migration
// 183/184 尚未套用時（rpg_weapons/rpg_armor_items 表不存在）保守視為「該部分未裝備」，不讓呼叫端
// 因此整支既有 API 500（比照 getEquippedWeaponDetail 既有容錯慣例）。
func (h *Handler) loadPlayerEquipment(ctx context.Context, userID string) (PlayerEquipmentSnapshot, error) {
	var snap PlayerEquipmentSnapshot
	w, wt, err := h.getEquippedWeaponDetail(ctx, userID)
	if err != nil {
		if !isMissingRelation(err) {
			return snap, err
		}
	} else {
		snap.Weapon, snap.WeaponType = w, wt
	}
	armorMap, err := h.getEquippedArmorMap(ctx, userID)
	if err != nil {
		if !isMissingRelation(err) {
			return snap, err
		}
		armorMap = map[string]*ArmorRow{}
	}
	snap.Armor = armorMap
	return snap, nil
}

// ---------------------------------------------------------------------------
// Wire DTO（WIRE：GET /rpg/equipment、PUT /rpg/equipment/weapon、PUT /rpg/equipment/{slot}
// 共用回應形狀）
// ---------------------------------------------------------------------------

// weaponDTO WIRE WeaponDTO：WeaponRow 加上依「目前這位玩家」算出的 can_equip/equipped 兩個
// 動態欄位，不存進 DB，只在回應時組裝。
type weaponDTO struct {
	ID          string        `json:"id"`
	TypeID      string        `json:"type_id"`
	Tier        int           `json:"tier"`
	Name        string        `json:"name"`
	Rarity      string        `json:"rarity"`
	LevelReq    int           `json:"level_req"`
	Element     string        `json:"element"`
	Profile     WeaponProfile `json:"profile"`
	Description string        `json:"description"`
	CanEquip    bool          `json:"can_equip"`
	Equipped    bool          `json:"equipped"`
}

func toWeaponDTO(w WeaponRow, effectiveLevel int, equippedID string) weaponDTO {
	return weaponDTO{
		ID: w.ID, TypeID: w.TypeID, Tier: w.Tier, Name: w.Name, Rarity: w.Rarity,
		LevelReq: w.LevelReq, Element: w.Element, Profile: w.Profile, Description: w.Description,
		CanEquip: effectiveLevel >= w.LevelReq,
		Equipped: equippedID != "" && equippedID == w.ID,
	}
}

// armorDTO WIRE ArmorDTO：ArmorRow 加上依「目前這位玩家」算出的 can_equip/equipped_in 兩個動態
// 欄位，不存進 DB，只在回應時組裝（比照 weaponDTO 的既有慣例）。EquippedIn＝實際佔用的格子名
// （例如 "accessory2"），nil＝這件防具/飾品目前沒有被裝在任何格子。
type armorDTO struct {
	ID          string       `json:"id"`
	JobID       *string      `json:"job_id"`
	Slot        string       `json:"slot"`
	Tier        int          `json:"tier"`
	Name        string       `json:"name"`
	Rarity      string       `json:"rarity"`
	LevelReq    int          `json:"level_req"`
	Profile     ArmorProfile `json:"profile"`
	Description string       `json:"description"`
	CanEquip    bool         `json:"can_equip"`
	EquippedIn  *string      `json:"equipped_in"`
}

func toArmorDTO(a ArmorRow, effectiveLevel int, equippedIn *string) armorDTO {
	return armorDTO{
		ID: a.ID, JobID: a.JobID, Slot: a.Slot, Tier: a.Tier, Name: a.Name, Rarity: a.Rarity,
		LevelReq: a.LevelReq, Profile: a.Profile, Description: a.Description,
		CanEquip:   effectiveLevel >= a.LevelReq,
		EquippedIn: equippedIn,
	}
}

// toArmorDTOList 把一份防具/飾品目錄（listArmorItemsByJobOrGeneric/listAccessoryItems 的結果）
// 轉成 armorDTO 清單，equippedIn 依 equipped（getEquippedArmorMap 的結果）反查——一件 item 理論
// 上只會出現在最多一個格子（PUT /rpg/equipment/{slot} 沒有「同一件裝兩格」的路徑），用 item id
// 當 key 反查即可。
func toArmorDTOList(items []ArmorRow, effectiveLevel int, equipped map[string]*ArmorRow) []armorDTO {
	slotByItemID := map[string]string{}
	for slot, row := range equipped {
		if row != nil {
			slotByItemID[row.ID] = slot
		}
	}
	out := make([]armorDTO, 0, len(items))
	for _, it := range items {
		var equippedIn *string
		if slot, ok := slotByItemID[it.ID]; ok {
			v := slot
			equippedIn = &v
		}
		out = append(out, toArmorDTO(it, effectiveLevel, equippedIn))
	}
	return out
}

// equippedWire WIRE：GET /rpg/equipment 的 equipped 八格。armorEquipSlots 七格對應
// helmet/gloves/armor/legs/boots/accessory1/accessory2（見 armor.go）。
type equippedWire struct {
	Weapon     *weaponDTO `json:"weapon"`
	Helmet     *armorDTO  `json:"helmet"`
	Gloves     *armorDTO  `json:"gloves"`
	Armor      *armorDTO  `json:"armor"`
	Legs       *armorDTO  `json:"legs"`
	Boots      *armorDTO  `json:"boots"`
	Accessory1 *armorDTO  `json:"accessory1"`
	Accessory2 *armorDTO  `json:"accessory2"`
}

type equipmentResponse struct {
	Job            *JobRow         `json:"job"`
	EffectiveLevel int             `json:"effective_level"`
	Equipped       equippedWire    `json:"equipped"`
	WeaponTypes    []WeaponTypeDTO `json:"weapon_types"`
	Weapons        []weaponDTO     `json:"weapons"`
	ArmorItems     []armorDTO      `json:"armor_items"`
	EquipBonus     EquipBonusDTO   `json:"equip_bonus"`
}

// WeaponTypeDTO WIRE WeaponTypeDTO——WeaponTypeRow 形狀完全相同，取別名方便 handler 簽章與
// WIRE.md 文件用語一致（沒有額外欄位需要組裝，不像 weaponDTO 需要動態計算）。
type WeaponTypeDTO = WeaponTypeRow

// buildEquipmentResponse GET /rpg/equipment 與 PUT /rpg/equipment/weapon、PUT
// /rpg/equipment/{slot} 成功後共用。
func (h *Handler) buildEquipmentResponse(ctx context.Context, uid string, cfg Config) (equipmentResponse, error) {
	ch, err := h.getOrCreateCharacter(ctx, uid, cfg)
	if err != nil {
		return equipmentResponse{}, err
	}
	baseLevel, effLevel, err := h.loadEffectiveLevel(ctx, uid, cfg, ch.TestLevel)
	if err != nil {
		return equipmentResponse{}, err
	}
	_ = baseLevel

	var job *JobRow
	weaponTypes := []WeaponTypeDTO{}
	weapons := []weaponDTO{}
	armorItems := []armorDTO{}
	if ch.JobID != nil {
		j, err := h.getJobByID(ctx, *ch.JobID)
		switch {
		case err == nil:
			job = &j
		case errors.Is(err, pgx.ErrNoRows):
			// 職業被刪除／查無（本輪沒有職業 CRUD，理論上不會發生）——保守視為未選職業。
		default:
			return equipmentResponse{}, err
		}
	}

	snap, err := h.loadPlayerEquipment(ctx, uid)
	if err != nil {
		return equipmentResponse{}, err
	}
	equippedID := ""
	if snap.Weapon != nil {
		equippedID = snap.Weapon.ID
	}

	if job != nil {
		wts, err := h.listWeaponTypesByJob(ctx, job.ID)
		if err != nil {
			return equipmentResponse{}, err
		}
		weaponTypes = wts

		ws, err := h.listWeaponsByJob(ctx, job.ID)
		if err != nil {
			return equipmentResponse{}, err
		}
		for _, w := range ws {
			weapons = append(weapons, toWeaponDTO(w, effLevel, equippedID))
		}

		// CONTRACT/WIRE：armor_items＝目前職業的 50 件防具＋90 件通用飾品。migration 184 尚未
		// 套用時（rpg_armor_items 表不存在）armorItems 保持空清單，不讓整支 API 因此 503（武器
		// 部分仍要正常運作）。
		items, err := h.listArmorItemsByJobOrGeneric(ctx, job.ID)
		if err != nil {
			if !isMissingRelation(err) {
				return equipmentResponse{}, err
			}
		} else {
			armorItems = toArmorDTOList(items, effLevel, snap.Armor)
		}
	} else {
		// CONTRACT：「未選職業→只有飾品」。
		items, err := h.listAccessoryItems(ctx)
		if err != nil {
			if !isMissingRelation(err) {
				return equipmentResponse{}, err
			}
		} else {
			armorItems = toArmorDTOList(items, effLevel, snap.Armor)
		}
	}

	var equippedWeaponDTO *weaponDTO
	if equippedID != "" {
		// 目前裝備的武器可能不屬於「目前職業」的清單（例如武器被下架 is_active=false，或資料
		// 髒掉），仍然要在 equipped.weapon 誠實顯示裝備中的那一把，不能因為它不在 weapons
		// 清單裡就悄悄消失——查一次完整資料組成 DTO，can_equip 用同一套等級規則算。
		if w, err := h.getWeaponByID(ctx, equippedID); err == nil {
			dto := toWeaponDTO(w, effLevel, equippedID)
			equippedWeaponDTO = &dto
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return equipmentResponse{}, err
		}
	}

	toEquippedArmorDTO := func(slot string) *armorDTO {
		row := snap.Armor[slot]
		if row == nil {
			return nil
		}
		v := slot
		dto := toArmorDTO(*row, effLevel, &v)
		return &dto
	}

	equipBonus := ToEquipBonusDTO(AggregateEquipment(snap.WeaponProfilePtr(), snap.ArmorProfiles()))

	return equipmentResponse{
		Job:            job,
		EffectiveLevel: effLevel,
		Equipped: equippedWire{
			Weapon:     equippedWeaponDTO,
			Helmet:     toEquippedArmorDTO("helmet"),
			Gloves:     toEquippedArmorDTO("gloves"),
			Armor:      toEquippedArmorDTO("armor"),
			Legs:       toEquippedArmorDTO("legs"),
			Boots:      toEquippedArmorDTO("boots"),
			Accessory1: toEquippedArmorDTO("accessory1"),
			Accessory2: toEquippedArmorDTO("accessory2"),
		},
		WeaponTypes: weaponTypes,
		Weapons:     weapons,
		ArmorItems:  armorItems,
		EquipBonus:  equipBonus,
	}, nil
}

// GET /rpg/equipment
func (h *Handler) GetEquipment(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	ctx := r.Context()
	cfg, err := h.loadConfig(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	resp, err := h.buildEquipmentResponse(ctx, uid, cfg)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errWeaponsNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load equipment")
		return
	}
	respondJSON(w, http.StatusOK, resp)
}

// errWeaponsNotReady migration 183 尚未套用時（rpg_weapon_types/rpg_weapons/player_equipment
// 表不存在）的固定 503 訊息，比照 errJobsNotReady/errContentNotReady 既有慣例。
const errWeaponsNotReady = "武器系統尚未初始化（migration 183 未套用）"

type putEquipmentWeaponRequest struct {
	ItemID *string `json:"item_id"`
}

// canEquipWeapon 純函式抽出 PutEquipmentWeapon 的裝備資格判斷（is_active／職業／有效等級三項），
// 不碰 DB，方便寫不連 Neon 的單元測試（見 equipment_test.go，比照 weapons_test.go 檔頭「連 DB 的
// 部分留給 Neon 分支整合測試」的既有慣例）。回傳空字串代表可裝備，否則是要回給前端的錯誤碼字面值。
//
// 審查#2【中】根因修復：原本只檢查 job/等級，沒檢查 is_active——下架武器（後台停用但保留歷史列，
// 例如平衡調整前收回舊武器）玩家只要事先拿到過 item_id（舊的裝備清單快取、或直接打 API），下架後
// 仍能繞過「/rpg/equipment 的 weapons 清單已經濾掉它」這道 UI 層防線裝上去。is_active 檢查放最前面、
// 回同一個 "not_found" 錯誤碼——對呼叫端而言「查無這把可裝備的武器」跟「這把已下架」語意上是同一
// 件事，不需要額外的錯誤碼分岔（也對齊上面 pgx.ErrNoRows 分支已經在用的 "not_found" 字面值）。
func canEquipWeapon(weapon WeaponRow, wtype WeaponTypeRow, jobID *string, effLevel int) string {
	if !weapon.IsActive {
		return "not_found"
	}
	if jobID == nil || wtype.JobID != *jobID {
		return "wrong_job"
	}
	if effLevel < weapon.LevelReq {
		return "level_too_low"
	}
	return ""
}

// PUT /rpg/equipment/weapon {"item_id": string|null}：null 卸下；有值則檢查武器存在、屬於目前
// 職業、有效等級達到門檻，全部通過才寫入（CONTRACT §2「取得方式（測試階段）」：職業對應的全部
// 武器都可裝備，唯一條件＝有效等級 ≥ level_req，不做背包／掉落／商店）。
func (h *Handler) PutEquipmentWeapon(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var body putEquipmentWeaponRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	ctx := r.Context()
	cfg, err := h.loadConfig(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	ch, err := h.getOrCreateCharacter(ctx, uid, cfg)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}

	if body.ItemID == nil {
		if err := h.clearEquippedWeapon(ctx, uid); err != nil {
			if respondIfMissingRelationMsg(w, err, errWeaponsNotReady) {
				return
			}
			respondErr(w, http.StatusInternalServerError, "failed to save")
			return
		}
		h.respondEquipment(w, r, uid, cfg)
		return
	}

	weapon, wtype, err := h.getWeaponWithType(ctx, *body.ItemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondErr(w, http.StatusBadRequest, "not_found")
			return
		}
		if respondIfMissingRelationMsg(w, err, errWeaponsNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load weapon")
		return
	}
	_, effLevel, err := h.loadEffectiveLevel(ctx, uid, cfg, ch.TestLevel)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load level config")
		return
	}
	if code := canEquipWeapon(weapon, wtype, ch.JobID, effLevel); code != "" {
		respondErr(w, http.StatusBadRequest, code)
		return
	}
	if err := h.setEquippedWeapon(ctx, uid, weapon.ID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	h.respondEquipment(w, r, uid, cfg)
}

// validEquipSlots DORPG P8（WIRE PUT /rpg/equipment/{slot}）：七格非武器格子的合法值——"weapon"
// 刻意不在這裡（有自己的靜態路由 PUT /rpg/equipment/weapon，chi 的靜態路徑優先於 {slot} 萬用
// 字元，正常請求不會落到這支 handler，這裡仍白名單擋一次防呆）。
var validEquipSlots = map[string]bool{
	"helmet": true, "gloves": true, "armor": true, "legs": true, "boots": true,
	"accessory1": true, "accessory2": true,
}

// canEquipArmor 純函式抽出 PutEquipmentSlot 的裝備資格判斷，不碰 DB，方便單元測試（比照
// canEquipWeapon 的既有慣例）。otherAccessoryItemID＝另一個飾品格子目前裝備的 item id（空字串＝
// 該格未裝備或本次不是飾品格），用來判斷 duplicate_accessory。回傳空字串代表可裝備。
//
// CONTRACT §1「取得與裝備規則」：防具必須 job_id＝目前職業（否則 wrong_job），飾品不限；有效
// 等級 ≥ level_req；飾品兩格不可裝同一件。is_active 檢查放最前面，比照 canEquipWeapon 的既有
// 優先序慣例（下架的防具/飾品即使其餘條件都符合也一律 not_found）。
func canEquipArmor(item ArmorRow, equipSlot string, jobID *string, effLevel int, otherAccessoryItemID string) string {
	if !item.IsActive {
		return "not_found"
	}
	if item.Slot != armorItemSlotFor(equipSlot) {
		return "wrong_slot"
	}
	if item.JobID != nil { // 防具（非飾品）才需要比對職業；飾品 job_id 恆為 nil，不受這條規則限制。
		if jobID == nil || *item.JobID != *jobID {
			return "wrong_job"
		}
	}
	if effLevel < item.LevelReq {
		return "level_too_low"
	}
	if (equipSlot == "accessory1" || equipSlot == "accessory2") && otherAccessoryItemID != "" && otherAccessoryItemID == item.ID {
		return "duplicate_accessory"
	}
	return ""
}

// PUT /rpg/equipment/{slot}（helmet|gloves|armor|legs|boots|accessory1|accessory2）
// {"item_id": string|null}：null 卸下；有值則檢查防具/飾品存在、slot 相符、（防具才需要）屬於
// 目前職業、有效等級達到門檻、（飾品才需要）另一格沒有裝同一件，全部通過才寫入。
func (h *Handler) PutEquipmentSlot(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	slot := chi.URLParam(r, "slot")
	if !validEquipSlots[slot] {
		respondErr(w, http.StatusBadRequest, "wrong_slot")
		return
	}
	var body putEquipmentWeaponRequest // 重用同一個 {item_id: string|null} 形狀（WIRE 兩支端點共用）
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	ctx := r.Context()
	cfg, err := h.loadConfig(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	ch, err := h.getOrCreateCharacter(ctx, uid, cfg)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}

	if body.ItemID == nil {
		if _, err := h.db.Exec(ctx, `DELETE FROM player_equipment WHERE user_id=$1 AND slot=$2`, uid, slot); err != nil {
			if respondIfMissingRelationMsg(w, err, errArmorNotReady) {
				return
			}
			respondErr(w, http.StatusInternalServerError, "failed to save")
			return
		}
		h.respondEquipment(w, r, uid, cfg)
		return
	}

	item, err := h.getArmorByID(ctx, *body.ItemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondErr(w, http.StatusBadRequest, "not_found")
			return
		}
		if respondIfMissingRelationMsg(w, err, errArmorNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load armor item")
		return
	}
	_, effLevel, err := h.loadEffectiveLevel(ctx, uid, cfg, ch.TestLevel)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load level config")
		return
	}

	otherAccessoryItemID := ""
	if slot == "accessory1" || slot == "accessory2" {
		otherSlot := "accessory1"
		if slot == "accessory1" {
			otherSlot = "accessory2"
		}
		var otherID string
		scanErr := h.db.QueryRow(ctx, `SELECT item_id FROM player_equipment WHERE user_id=$1 AND slot=$2`, uid, otherSlot).Scan(&otherID)
		switch {
		case scanErr == nil:
			otherAccessoryItemID = otherID
		case errors.Is(scanErr, pgx.ErrNoRows):
			// 另一格沒有裝備，不需要比對。
		default:
			respondErr(w, http.StatusInternalServerError, "failed to load equipment")
			return
		}
	}

	if code := canEquipArmor(item, slot, ch.JobID, effLevel, otherAccessoryItemID); code != "" {
		respondErr(w, http.StatusBadRequest, code)
		return
	}
	if _, err := h.db.Exec(ctx, `
		INSERT INTO player_equipment (user_id, slot, item_id, updated_at) VALUES ($1,$2,$3,NOW())
		ON CONFLICT (user_id, slot) DO UPDATE SET item_id=$3, updated_at=NOW()`, uid, slot, item.ID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	h.respondEquipment(w, r, uid, cfg)
}

func (h *Handler) respondEquipment(w http.ResponseWriter, r *http.Request, uid string, cfg Config) {
	resp, err := h.buildEquipmentResponse(r.Context(), uid, cfg)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errWeaponsNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load equipment")
		return
	}
	respondJSON(w, http.StatusOK, resp)
}
