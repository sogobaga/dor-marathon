// armor.go：DORPG P8（CONTRACT §1/§2）防具系統的資料模型——ArmorProfile（引擎詞彙，存進
// rpg_armor_items.profile JSONB）＋ AggregateEquipment（武器＋防具/飾品彙總成 EquipBonus，
// Compute() 與 GET /rpg/equipment 共用同一份彙總規則）＋ rpg_armor_items 表的 Row 型別與 pgx
// 讀寫。玩家 player_equipment 八格的讀寫、REST 端點在 equipment.go；後台 CRUD 在 armor_admin.go；
// 換職業卸下防具在 jobs.go；戰鬥 wire 的 equipmentEffects 轉換在 battle.go。
package rpg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// ArmorProfile：CONTRACT §2——每件防具/飾品最終數值的引擎詞彙，缺欄位＝中性值（不改變任何既有
// 行為）。json tag 全部 snake_case，逐欄對齊 WIRE.md 的 ArmorProfile 形狀，勿自行改名。跟
// WeaponProfile 不同，防具沒有 hits/hit_mul/charge_* 這類「零不代表中性」的例外欄位——全零
// ArmorProfile 就是「完全不改變」的中性值。
// ---------------------------------------------------------------------------

// ArmorProfile 見檔頭註解。六素質與 def 是 flat 加成（AggregateEquipment 直接相加，Compute()
// 在算衍生值前套用，見 compute.go）；其餘 pct/regen 類由 AggregateEquipment 相加後依 CONTRACT §2
// 的規則 clamp。interval_pct／element_resist_pct／damage_taken_pct／mp_cost_reduce_pct 只在
// 「防具/飾品」彙總範圍內有意義（不含武器，見 EquipBonus 檔頭註解與 WIRE 戰鬥 bootstrap
// equipmentEffects「只彙總防具與飾品」）。
type ArmorProfile struct {
	Def int `json:"def"`
	Str int `json:"str"`
	Agi int `json:"agi"`
	Vit int `json:"vit"`
	Dex int `json:"dex"`
	Int int `json:"int"`
	Luk int `json:"luk"`

	HpPct   float64 `json:"hp_pct"`
	MpPct   float64 `json:"mp_pct"`
	AtkPct  float64 `json:"atk_pct"`
	MatkPct float64 `json:"matk_pct"`

	IntervalPct float64 `json:"interval_pct"` // 負＝攻擊間隔縮短，與武器 interval_pct 相加（引擎）

	CritPct    float64 `json:"crit_pct"`
	CritDmgPct float64 `json:"crit_dmg_pct"`

	MpCostReducePct float64 `json:"mp_cost_reduce_pct"` // 技能 MP 消耗 ×(1−pct/100)，floor，下限 1（引擎）

	HpRegenPctPer5s float64 `json:"hp_regen_pct_per_5s"` // 每 5 秒回復 HPMax 的 n%（引擎，floor，戰鬥中持續）
	MpRegenPctPer5s float64 `json:"mp_regen_pct_per_5s"`

	DamageTakenPct   float64 `json:"damage_taken_pct"`   // 負＝少受傷，與 buff 的 damage_taken_pct 相加（引擎）
	ElementResistPct float64 `json:"element_resist_pct"` // 與武器（鍊）相加（引擎）
}

// DefaultArmorProfile 中性值：全零（跟 WeaponProfile 不同，防具沒有「零不代表中性」的例外欄位）。
func DefaultArmorProfile() ArmorProfile {
	return ArmorProfile{}
}

// Validate 只擋會讓引擎壞掉的邊界（比照 WeaponProfile.Validate 的寬鬆風格）：interval_pct 是
// 唯一「單一欄位就可能除零/倒轉時間軸」的欄位，其餘 pct 類即使是誇張的正負值也只是數值極端，
// 交給 AggregateEquipment 的彙總 clamp 把關，不在單一防具層級擋。
func (p ArmorProfile) Validate() error {
	if p.IntervalPct <= -100 {
		return fmt.Errorf("interval_pct 必須 > -100")
	}
	return nil
}

// ParseArmorProfile 解析 rpg_armor_items.profile JSONB（DB 掃描出的原始 bytes，或後台 PUT body
// 裡的巢狀 json.RawMessage）：缺欄位＝DefaultArmorProfile() 中性值，比照 ParseWeaponProfile 的
// 「先套預設值再 Unmarshal 覆蓋」寫法。raw 為空（NULL/""/"{}" 皆可安全處理）時直接回中性值。
func ParseArmorProfile(raw []byte) (ArmorProfile, error) {
	p := DefaultArmorProfile()
	if len(raw) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return ArmorProfile{}, fmt.Errorf("invalid armor profile json: %w", err)
	}
	if err := p.Validate(); err != nil {
		return ArmorProfile{}, err
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// EquipBonus：Compute()／GET /rpg/equipment／戰鬥 bootstrap 共用的裝備彙總結果（CONTRACT §2
// 「彙總規則」）。
//
// Atk/Matk/DefPct/MdefPct/FleeBonus 這五個欄位只有武器會提供（防具/飾品沒有對應欄位）——是
// Compute() 沿用 P7 既有公式（equipAtk 乘 Str/Dex%、equipMatk 直接加總、DefPct/MdefPct 各自的
// 乘法位置、FleeBonus 直接加進迴避率）所需的中繼值，不對外顯示，見 ToEquipBonusDTO 只挑
// WIRE.md 列出的欄位。
//
// IntervalPct／MpCostReducePct／HpRegenPctPer5s／MpRegenPctPer5s／DamageTakenPct／
// ElementResistPct 這六個欄位只彙總「防具與飾品」，刻意不含武器（WIRE：戰鬥 bootstrap
// equipmentEffects「只彙總防具與飾品；武器仍走 weapon.profile」，引擎把 weapon.profile 的
// intervalPct／elementResistPct 與 equipmentEffects 分開相加）——AggregateEquipment 因此故意
// 不把 weapon.IntervalPct/ElementResistPct 疊進這兩個欄位，避免引擎端重複計入。
type EquipBonus struct {
	Str, Agi, Vit, Dex, Int, Luk int
	Def                          int // 防具 flat def 總和（武器沒有這個欄位）

	Atk, Matk, DefPct, MdefPct, FleeBonus float64 // 只有武器貢獻，見檔頭註解

	HpPct, MpPct, AtkPct, MatkPct float64
	CritPct, CritDmgPct           float64

	IntervalPct      float64 // 只彙總防具＋飾品
	MpCostReducePct  float64
	HpRegenPctPer5s  float64
	MpRegenPctPer5s  float64
	DamageTakenPct   float64
	ElementResistPct float64 // 只彙總防具＋飾品
}

// AggregateEquipment CONTRACT §2 彙總規則：武器（P7 既有欄位，nil＝未裝備）＋任意數量防具/飾品
// （ArmorProfile，五部位防具與兩格飾品共用同一個型別，呼叫端把已裝備的全部 profile 攤平成一個
// slice 傳入；未裝備的格子由呼叫端直接不放進 slice，不是傳零值元素）→ 單一 EquipBonus。
//
// weapon==nil 時 Atk/Matk/DefPct/MdefPct/FleeBonus 維持零值（等同 P7 之前「沒有武器」的行為）；
// armors 為空切片或 nil 時防具/飾品部分全零——兩者互不影響，方便「只有武器」「只有防具」分開
// 測試（CONTRACT「只有武器時結果與 P7 逐位元一致」，見 compute_weapon_test.go）。
func AggregateEquipment(weapon *WeaponProfile, armors []ArmorProfile) EquipBonus {
	var b EquipBonus
	if weapon != nil {
		b.Int += weapon.IntBonus
		b.Atk = weapon.Atk
		b.Matk = weapon.Matk
		b.DefPct = weapon.DefPct
		b.MdefPct = weapon.MdefPct
		b.FleeBonus = weapon.FleeBonus
		b.MpPct += weapon.MpPct
		b.AtkPct += weapon.AtkPct
		b.MatkPct += weapon.MatkPct
		b.CritPct += weapon.CritPct
		b.CritDmgPct += weapon.CritDmgPct
	}
	for _, a := range armors {
		b.Str += a.Str
		b.Agi += a.Agi
		b.Vit += a.Vit
		b.Dex += a.Dex
		b.Int += a.Int
		b.Luk += a.Luk
		b.Def += a.Def
		b.HpPct += a.HpPct
		b.MpPct += a.MpPct
		b.AtkPct += a.AtkPct
		b.MatkPct += a.MatkPct
		b.CritPct += a.CritPct
		b.CritDmgPct += a.CritDmgPct
		b.IntervalPct += a.IntervalPct
		b.MpCostReducePct += a.MpCostReducePct
		b.HpRegenPctPer5s += a.HpRegenPctPer5s
		b.MpRegenPctPer5s += a.MpRegenPctPer5s
		b.DamageTakenPct += a.DamageTakenPct
		b.ElementResistPct += a.ElementResistPct
	}

	// CONTRACT §2 clamp：四項疊加太誇張會讓引擎壞掉（除零/傷害歸零/回魔過量），彙總完成後夾一次，
	// 不管疊加來源是幾件裝備。
	if b.IntervalPct < -50 {
		b.IntervalPct = -50
	}
	if b.ElementResistPct > 60 {
		b.ElementResistPct = 60
	}
	if b.DamageTakenPct < -60 {
		b.DamageTakenPct = -60
	}
	if b.MpCostReducePct > 50 {
		b.MpCostReducePct = 50
	}
	return b
}

// EquipBonusDTO WIRE EquipBonusDTO：GET /rpg/equipment、/rpg/me 回應用的精簡形狀，只列 WIRE.md
// 點名的欄位（Atk/Matk/DefPct/MdefPct/FleeBonus 是 Compute 內部中繼值，不對外顯示，見 EquipBonus
// 檔頭註解）。
type EquipBonusDTO struct {
	Str int `json:"str"`
	Agi int `json:"agi"`
	Vit int `json:"vit"`
	Dex int `json:"dex"`
	Int int `json:"int"`
	Luk int `json:"luk"`
	Def int `json:"def"`

	HpPct   float64 `json:"hp_pct"`
	MpPct   float64 `json:"mp_pct"`
	AtkPct  float64 `json:"atk_pct"`
	MatkPct float64 `json:"matk_pct"`

	IntervalPct float64 `json:"interval_pct"`

	CritPct    float64 `json:"crit_pct"`
	CritDmgPct float64 `json:"crit_dmg_pct"`

	MpCostReducePct  float64 `json:"mp_cost_reduce_pct"`
	HpRegenPctPer5s  float64 `json:"hp_regen_pct_per_5s"`
	MpRegenPctPer5s  float64 `json:"mp_regen_pct_per_5s"`
	DamageTakenPct   float64 `json:"damage_taken_pct"`
	ElementResistPct float64 `json:"element_resist_pct"`
}

// ToEquipBonusDTO 轉換成 GET /rpg/equipment、/rpg/me 要送給前端的形狀。
func ToEquipBonusDTO(b EquipBonus) EquipBonusDTO {
	return EquipBonusDTO{
		Str: b.Str, Agi: b.Agi, Vit: b.Vit, Dex: b.Dex, Int: b.Int, Luk: b.Luk, Def: b.Def,
		HpPct: b.HpPct, MpPct: b.MpPct, AtkPct: b.AtkPct, MatkPct: b.MatkPct,
		IntervalPct:      b.IntervalPct,
		CritPct:          b.CritPct,
		CritDmgPct:       b.CritDmgPct,
		MpCostReducePct:  b.MpCostReducePct,
		HpRegenPctPer5s:  b.HpRegenPctPer5s,
		MpRegenPctPer5s:  b.MpRegenPctPer5s,
		DamageTakenPct:   b.DamageTakenPct,
		ElementResistPct: b.ElementResistPct,
	}
}

// ---------------------------------------------------------------------------
// rpg_armor_items
// ---------------------------------------------------------------------------

// validArmorSlots CONTRACT §1：五部位防具＋通用飾品（accessory1/accessory2 是 player_equipment
// 的格子名，這裡的 slot 是 rpg_armor_items 自己的分類，飾品只有一種 "accessory"）。
var validArmorSlots = map[string]bool{
	"helmet": true, "gloves": true, "armor": true, "legs": true, "boots": true, "accessory": true,
}

// armorItemSlotFor player_equipment 的格子名（含 weapon 以外七格）→ rpg_armor_items.slot 應該
// 符合的值；accessory1/accessory2 兩格都對應同一個 "accessory" 分類（WIRE PUT /rpg/equipment/
// {slot} 錯誤碼 wrong_slot 判斷用）。
func armorItemSlotFor(equipSlot string) string {
	if equipSlot == "accessory1" || equipSlot == "accessory2" {
		return "accessory"
	}
	return equipSlot
}

// ArmorRow rpg_armor_items 資料列。Profile 直接是解析好的 ArmorProfile（缺欄位已補中性值），
// API 回應與 DB 讀寫共用同一個型別，比照 WeaponRow 的既有慣例。
type ArmorRow struct {
	ID          string       `json:"id"`
	JobID       *string      `json:"job_id"` // nil＝通用飾品
	Slot        string       `json:"slot"`
	Tier        int          `json:"tier"`
	Name        string       `json:"name"`
	Rarity      string       `json:"rarity"`
	LevelReq    int          `json:"level_req"`
	Profile     ArmorProfile `json:"profile"`
	Description string       `json:"description"`
	IsActive    bool         `json:"is_active"`
	SortOrder   int          `json:"sort_order"`
}

// Validate 只檢查外層欄位；Profile 的詞彙/範圍已由 ParseArmorProfile 在建構 ArmorRow 時驗證過
// （見 armor_admin.go AdminPutArmorItem），這裡不重複。防具（JobID!=nil）tier 1..10、飾品
// （JobID==nil）tier 1..5（CONTRACT §1），DB 的 CHECK 只收斂到 1..10（見 migration
// 184_rpg_armor.sql），飾品的上限 5 由這裡的應用層規則把關。
func (a ArmorRow) Validate() error {
	if a.ID == "" || a.Name == "" {
		return fmt.Errorf("id/name 不可為空")
	}
	if !validArmorSlots[a.Slot] {
		return fmt.Errorf("slot 不合法")
	}
	if !validRarities[a.Rarity] {
		return fmt.Errorf("rarity 不合法")
	}
	if a.LevelReq < 1 || a.LevelReq > 99 {
		return fmt.Errorf("level_req 必須介於 1..99")
	}
	if a.JobID != nil {
		if a.Tier < 1 || a.Tier > 10 {
			return fmt.Errorf("tier 必須介於 1..10")
		}
	} else {
		if a.Tier < 1 || a.Tier > 5 {
			return fmt.Errorf("tier 必須介於 1..5（通用飾品）")
		}
	}
	return nil
}

const armorCols = `id, job_id, slot, tier, name, rarity, level_req, profile, description, is_active, sort_order`

func scanArmor(row pgx.Row) (ArmorRow, error) {
	var a ArmorRow
	var raw []byte
	if err := row.Scan(&a.ID, &a.JobID, &a.Slot, &a.Tier, &a.Name, &a.Rarity, &a.LevelReq, &raw, &a.Description, &a.IsActive, &a.SortOrder); err != nil {
		return a, err
	}
	profile, err := ParseArmorProfile(raw)
	if err != nil {
		// 壞掉/不合法的 profile JSON 不該讓整支 API 500（比照 scanWeapon 對 profile 的既有慣例）——
		// 回中性值，後台看得到這筆資料，可以馬上重新存檔修正。
		profile = DefaultArmorProfile()
	}
	a.Profile = profile
	return a, nil
}

// listArmorItemsByJobOrGeneric GET /rpg/equipment 用（WIRE：armor_items＝目前職業的 50 件防具＋
// 90 件通用飾品）：job_id=jobID（該職業的五部位防具）或 job_id IS NULL（通用飾品）。
func (h *Handler) listArmorItemsByJobOrGeneric(ctx context.Context, jobID string) ([]ArmorRow, error) {
	rows, err := h.db.Query(ctx, `
		SELECT `+armorCols+` FROM rpg_armor_items
		WHERE is_active AND (job_id=$1 OR job_id IS NULL)
		ORDER BY job_id NULLS LAST, slot, tier, id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArmorRow{}
	for rows.Next() {
		a, err := scanArmor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// listAccessoryItems GET /rpg/equipment 用：未選職業時只有飾品（CONTRACT「未選職業→只有飾品」）。
func (h *Handler) listAccessoryItems(ctx context.Context) ([]ArmorRow, error) {
	rows, err := h.db.Query(ctx, `SELECT `+armorCols+` FROM rpg_armor_items WHERE is_active AND job_id IS NULL ORDER BY tier, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArmorRow{}
	for rows.Next() {
		a, err := scanArmor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (h *Handler) getArmorByID(ctx context.Context, id string) (ArmorRow, error) {
	row := h.db.QueryRow(ctx, `SELECT `+armorCols+` FROM rpg_armor_items WHERE id=$1`, id)
	return scanArmor(row)
}

// listAllArmorItems 後台 GET /admin/rpg/armor-items（可選 job_id/slot 篩選，空字串＝不篩選）。
// jobFilter 特殊值 "none" 代表只要通用飾品（job_id IS NULL）——後台篩選 UI 用固定字面值，不是
// 真正的職業 id（六職業 id 皆為小寫英文單字，不會撞到 "none"）。
func (h *Handler) listAllArmorItems(ctx context.Context, jobFilter, slotFilter string) ([]ArmorRow, error) {
	q := `SELECT ` + armorCols + ` FROM rpg_armor_items`
	var args []any
	var conds []string
	switch jobFilter {
	case "":
		// 不篩選。
	case "none":
		conds = append(conds, `job_id IS NULL`)
	default:
		args = append(args, jobFilter)
		conds = append(conds, fmt.Sprintf(`job_id=$%d`, len(args)))
	}
	if slotFilter != "" {
		args = append(args, slotFilter)
		conds = append(conds, fmt.Sprintf(`slot=$%d`, len(args)))
	}
	for i, c := range conds {
		if i == 0 {
			q += ` WHERE ` + c
		} else {
			q += ` AND ` + c
		}
	}
	q += ` ORDER BY job_id NULLS LAST, slot, tier, id`
	rows, err := h.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArmorRow{}
	for rows.Next() {
		a, err := scanArmor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (h *Handler) upsertArmorItem(ctx context.Context, a ArmorRow) error {
	profileRaw, err := json.Marshal(a.Profile)
	if err != nil {
		return err
	}
	_, err = h.db.Exec(ctx, `
		INSERT INTO rpg_armor_items (id, job_id, slot, tier, name, rarity, level_req, profile, description, is_active, sort_order, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NOW())
		ON CONFLICT (id) DO UPDATE SET
			job_id=$2, slot=$3, tier=$4, name=$5, rarity=$6, level_req=$7, profile=$8, description=$9,
			is_active=$10, sort_order=$11, updated_at=NOW()`,
		a.ID, a.JobID, a.Slot, a.Tier, a.Name, a.Rarity, a.LevelReq, profileRaw, a.Description, a.IsActive, a.SortOrder)
	return err
}

func (h *Handler) deleteArmorItem(ctx context.Context, id string) (bool, error) {
	ct, err := h.db.Exec(ctx, `DELETE FROM rpg_armor_items WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// errArmorNotReady migration 184 尚未套用時（rpg_armor_items 表不存在、或 player_equipment 的
// CHECK 約束還沒擴充）的固定 503 訊息，比照 errWeaponsNotReady 既有慣例。
const errArmorNotReady = "防具系統尚未初始化（migration 184 未套用）"

// ---------------------------------------------------------------------------
// PlayerEquipmentSnapshot：玩家目前完整裝備狀態（武器＋七格防具/飾品）一次查出，Compute() 用的
// EquipBonus、GET /rpg/equipment 的 equipped/armor_items/equip_bonus、/rpg/me 的
// equipment/equip_bonus、戰鬥 bootstrap 的武器 wire／equipmentEffects，共用同一份查詢結果組裝，
// 不必各自重複讀 player_equipment。armor_slots.go/equipment.go 負責組裝，這裡只放形狀與純函式。
// ---------------------------------------------------------------------------

// armorEquipSlots player_equipment 除了 weapon 以外的七格固定順序，供需要逐格列舉的呼叫端使用。
var armorEquipSlots = []string{"helmet", "gloves", "armor", "legs", "boots", "accessory1", "accessory2"}

// PlayerEquipmentSnapshot 見上方檔頭註解。
type PlayerEquipmentSnapshot struct {
	Weapon     *WeaponRow
	WeaponType *WeaponTypeRow
	Armor      map[string]*ArmorRow // key: armorEquipSlots 七格之一；查無/未裝備的格子不進 map
}

// ArmorProfiles 全部已裝備防具/飾品的 profile（不含武器），Compute()／equipmentEffects 共用。
func (s PlayerEquipmentSnapshot) ArmorProfiles() []ArmorProfile {
	out := make([]ArmorProfile, 0, len(s.Armor))
	for _, row := range s.Armor {
		if row != nil {
			out = append(out, row.Profile)
		}
	}
	return out
}

// WeaponProfilePtr 裝備的武器 profile（nil＝未裝備），AggregateEquipment 用。
func (s PlayerEquipmentSnapshot) WeaponProfilePtr() *WeaponProfile {
	if s.Weapon == nil {
		return nil
	}
	p := s.Weapon.Profile
	return &p
}

// Equip 組出 Compute() 要用的 *EquipBonus；完全沒有任何裝備時回 nil——維持 P5/P6/P7 既有的
// 「wp==nil 時素質衍生值不 floor」行為（見 compute.go Compute() 對 eq==nil 的既有分支），不是
// 一律回傳全零的 EquipBonus。
func (s PlayerEquipmentSnapshot) Equip() *EquipBonus {
	wp := s.WeaponProfilePtr()
	armors := s.ArmorProfiles()
	if wp == nil && len(armors) == 0 {
		return nil
	}
	b := AggregateEquipment(wp, armors)
	return &b
}

// EquipmentEffects 戰鬥 bootstrap party member 用（WIRE：equipmentEffects「只彙總防具與飾品」，
// 不含武器——武器仍走 weapon.profile，由引擎自己跟這裡的結果相加）。
func (s PlayerEquipmentSnapshot) EquipmentEffects() EquipBonus {
	return AggregateEquipment(nil, s.ArmorProfiles())
}
