// equipment.go：DORPG P7（CONTRACT §2/§5、WIRE）玩家裝備——player_equipment 表讀寫 + GET
// /rpg/equipment、PUT /rpg/equipment/weapon 兩支會員端點。掛在既有 Handler.Router() 底下，套用
// 既有 requireEntry 白名單（比照 handler.go/jobs.go 慣例）。
package rpg

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

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

// ---------------------------------------------------------------------------
// Wire DTO（WIRE：GET /rpg/equipment、PUT /rpg/equipment/weapon 共用回應形狀）
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

type equippedWire struct {
	Weapon *weaponDTO `json:"weapon"`
}

type equipmentResponse struct {
	Job            *JobRow         `json:"job"`
	EffectiveLevel int             `json:"effective_level"`
	Equipped       equippedWire    `json:"equipped"`
	WeaponTypes    []WeaponTypeDTO `json:"weapon_types"`
	Weapons        []weaponDTO     `json:"weapons"`
}

// WeaponTypeDTO WIRE WeaponTypeDTO——WeaponTypeRow 形狀完全相同，取別名方便 handler 簽章與
// WIRE.md 文件用語一致（沒有額外欄位需要組裝，不像 weaponDTO 需要動態計算）。
type WeaponTypeDTO = WeaponTypeRow

// buildEquipmentResponse GET /rpg/equipment 與 PUT /rpg/equipment/weapon 成功後共用。
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

	equippedID, err := h.getEquippedWeaponID(ctx, uid)
	if err != nil {
		return equipmentResponse{}, err
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
	}

	var equippedDTO *weaponDTO
	if equippedID != "" {
		// 目前裝備的武器可能不屬於「目前職業」的清單（例如武器被下架 is_active=false，或資料
		// 髒掉），仍然要在 equipped.weapon 誠實顯示裝備中的那一把，不能因為它不在 weapons
		// 清單裡就悄悄消失——查一次完整資料組成 DTO，can_equip 用同一套等級規則算。
		if w, err := h.getWeaponByID(ctx, equippedID); err == nil {
			dto := toWeaponDTO(w, effLevel, equippedID)
			equippedDTO = &dto
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return equipmentResponse{}, err
		}
	}

	return equipmentResponse{
		Job:            job,
		EffectiveLevel: effLevel,
		Equipped:       equippedWire{Weapon: equippedDTO},
		WeaponTypes:    weaponTypes,
		Weapons:        weapons,
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
