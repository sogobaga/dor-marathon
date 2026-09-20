// weapons.go：DORPG P7（CONTRACT §2/§3）武器系統的資料模型——WeaponProfile（引擎詞彙，存進
// rpg_weapons.profile JSONB）＋ rpg_weapon_types/rpg_weapons 兩張表的 Row 型別與 pgx 讀寫。
// player_equipment（裝備狀態）與 REST 端點在 equipment.go；戰鬥 wire 轉換在 battle.go；
// 屬性相剋在 elements.go；後台 CRUD 在 weapons_admin.go。
package rpg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// WeaponProfile：CONTRACT §3——每件武器最終數值的引擎詞彙，缺欄位＝中性值（不改變任何既有
// 行為）。json tag 全部 snake_case，逐欄對齊 WIRE.md 的 WeaponProfile 形狀，勿自行改名。
// ---------------------------------------------------------------------------

// WeaponSizeBonus 對小/中/大型怪物的額外傷害百分比（CONTRACT §3 size_bonus）。
type WeaponSizeBonus struct {
	Small  float64 `json:"small"`
	Medium float64 `json:"medium"`
	Large  float64 `json:"large"`
}

// WeaponProfile 見檔頭註解。Compute()（compute.go）套用 int_bonus/atk/matk/atk_pct/matk_pct/
// def_pct/mdef_pct/mp_pct/crit_pct/flee_bonus/crit_dmg_pct 這些「素質衍生值」欄位；其餘
// （hits/hit_mul/extra_hit_chance_pct/interval_pct/charge_*/splash_pct/size_bonus/
// element_resist_pct/magic_skill_pct/element）只在戰鬥中由前端引擎套用，Compute 不碰。
type WeaponProfile struct {
	Atk      float64 `json:"atk"`
	Matk     float64 `json:"matk"`
	IntBonus int     `json:"int_bonus"`
	MpPct    float64 `json:"mp_pct"`
	AtkPct   float64 `json:"atk_pct"`
	MatkPct  float64 `json:"matk_pct"`
	DefPct   float64 `json:"def_pct"`
	MdefPct  float64 `json:"mdef_pct"`

	Hits              int     `json:"hits"`
	HitMul            float64 `json:"hit_mul"`
	ExtraHitChancePct float64 `json:"extra_hit_chance_pct"`

	IntervalPct float64 `json:"interval_pct"` // 負＝更快（細劍/短弓/弩），正＝更慢（長弓/斧）

	ChargeTimeMul float64 `json:"charge_time_mul"`
	ChargeDmgMul  float64 `json:"charge_dmg_mul"`

	SplashPct float64 `json:"splash_pct"`

	SizeBonus WeaponSizeBonus `json:"size_bonus"`

	CritPct    float64 `json:"crit_pct"`
	CritDmgPct float64 `json:"crit_dmg_pct"`
	FleeBonus  float64 `json:"flee_bonus"`

	ElementResistPct float64 `json:"element_resist_pct"`
	MagicSkillPct    float64 `json:"magic_skill_pct"`

	Element string `json:"element"`
}

// DefaultWeaponProfile 中性值（CONTRACT §3 JSON 範例）：沒有武器、或武器 JSON 缺欄位時，該欄位
// 等同「完全不改變」既有戰鬥手感——hits=1/hit_mul=1/charge_time_mul=1/charge_dmg_mul=1 是唯一
// 不能用零值代表中性的三組欄位（0 會讓攻擊次數/傷害倍率/蓄氣時間變成 0，不是「沒有加成」）。
func DefaultWeaponProfile() WeaponProfile {
	return WeaponProfile{
		Hits:          1,
		HitMul:        1.0,
		ChargeTimeMul: 1.0,
		ChargeDmgMul:  1.0,
		Element:       "neutral",
	}
}

// Validate 只擋會讓 Compute()/戰鬥引擎壞掉（除零、負值攻擊次數、不合法詞彙）的邊界，比照
// config.go/content.go 一貫的寬鬆風格，不擋設計上合理的極端數值（例如 atk_pct 想給到很誇張）。
func (p WeaponProfile) Validate() error {
	if p.Element != "" && !validElementKinds[p.Element] {
		return fmt.Errorf("element 不合法：%s", p.Element)
	}
	if p.Hits < 1 {
		return fmt.Errorf("hits 必須 >= 1")
	}
	if p.HitMul <= 0 {
		return fmt.Errorf("hit_mul 必須 > 0")
	}
	if p.ChargeTimeMul <= 0 {
		return fmt.Errorf("charge_time_mul 必須 > 0")
	}
	if p.ChargeDmgMul <= 0 {
		return fmt.Errorf("charge_dmg_mul 必須 > 0")
	}
	// interval_pct <= -100 會讓引擎的攻擊冷卻換算出 0 或負值（除零/倒轉時間軸），只擋這個邊界。
	if p.IntervalPct <= -100 {
		return fmt.Errorf("interval_pct 必須 > -100")
	}
	if p.ExtraHitChancePct < 0 || p.ExtraHitChancePct > 100 {
		return fmt.Errorf("extra_hit_chance_pct 必須介於 0..100")
	}
	if p.SplashPct < 0 {
		return fmt.Errorf("splash_pct 必須 >= 0")
	}
	return nil
}

// ParseWeaponProfile 解析 rpg_weapons.profile JSONB（DB 掃描出的原始 bytes，或後台 PUT body 裡的
// 巢狀 json.RawMessage）：缺欄位＝DefaultWeaponProfile() 中性值，比照 config.go ParseConfig 的
// 「先套預設值再 Unmarshal 覆蓋」寫法。raw 為空（NULL/"" /"{}" 皆可安全處理）時直接回中性值。
func ParseWeaponProfile(raw []byte) (WeaponProfile, error) {
	p := DefaultWeaponProfile()
	if len(raw) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return WeaponProfile{}, fmt.Errorf("invalid weapon profile json: %w", err)
	}
	if err := p.Validate(); err != nil {
		return WeaponProfile{}, err
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// rpg_weapon_types
// ---------------------------------------------------------------------------

// WeaponTypeRow rpg_weapon_types 資料列，json tag 對齊 WIRE WeaponTypeDTO——不需要另外轉一層。
type WeaponTypeRow struct {
	ID               string         `json:"id"`
	JobID            string         `json:"job_id"`
	Name             string         `json:"name"`
	Visual           string         `json:"visual"`
	ElementalCapable bool           `json:"elemental_capable"`
	Description      string         `json:"description"`
	Traits           map[string]any `json:"traits"`
	SortOrder        int            `json:"sort_order"`
}

// Validate 比照 content.go 其餘 Row.Validate() 的寬鬆風格：只擋會讓前端渲染壞掉的欄位。
func (t WeaponTypeRow) Validate() error {
	if t.ID == "" || t.Name == "" || t.JobID == "" {
		return fmt.Errorf("id/name/job_id 不可為空")
	}
	if !validWeaponKinds[t.Visual] {
		return fmt.Errorf("visual 不合法")
	}
	return nil
}

const weaponTypeCols = `id, job_id, name, visual, elemental_capable, description, traits, sort_order`

func scanWeaponType(row pgx.Row) (WeaponTypeRow, error) {
	var t WeaponTypeRow
	var raw []byte
	if err := row.Scan(&t.ID, &t.JobID, &t.Name, &t.Visual, &t.ElementalCapable, &t.Description, &raw, &t.SortOrder); err != nil {
		return t, err
	}
	t.Traits = map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &t.Traits); err != nil {
			// 壞掉的 JSON 不該讓整支 API 500（比照 scanScene 對 slots 的既有慣例）——回空物件，
			// 純顯示用途，後台看得到可以馬上重新存檔修正。
			t.Traits = map[string]any{}
		}
	}
	return t, nil
}

func (h *Handler) listWeaponTypes(ctx context.Context) ([]WeaponTypeRow, error) {
	rows, err := h.db.Query(ctx, `SELECT `+weaponTypeCols+` FROM rpg_weapon_types ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WeaponTypeRow{}
	for rows.Next() {
		t, err := scanWeaponType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// listWeaponTypesByJob GET /rpg/equipment 用：某職業可裝備的全部武器類型。
func (h *Handler) listWeaponTypesByJob(ctx context.Context, jobID string) ([]WeaponTypeRow, error) {
	rows, err := h.db.Query(ctx, `SELECT `+weaponTypeCols+` FROM rpg_weapon_types WHERE job_id=$1 ORDER BY sort_order, id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WeaponTypeRow{}
	for rows.Next() {
		t, err := scanWeaponType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (h *Handler) getWeaponTypeByID(ctx context.Context, id string) (WeaponTypeRow, error) {
	row := h.db.QueryRow(ctx, `SELECT `+weaponTypeCols+` FROM rpg_weapon_types WHERE id=$1`, id)
	return scanWeaponType(row)
}

func (h *Handler) upsertWeaponType(ctx context.Context, t WeaponTypeRow) error {
	traits := t.Traits
	if traits == nil {
		traits = map[string]any{}
	}
	raw, err := json.Marshal(traits)
	if err != nil {
		return err
	}
	_, err = h.db.Exec(ctx, `
		INSERT INTO rpg_weapon_types (id, job_id, name, visual, elemental_capable, description, traits, sort_order, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW())
		ON CONFLICT (id) DO UPDATE SET
			job_id=$2, name=$3, visual=$4, elemental_capable=$5, description=$6, traits=$7, sort_order=$8, updated_at=NOW()`,
		t.ID, t.JobID, t.Name, t.Visual, t.ElementalCapable, t.Description, raw, t.SortOrder)
	return err
}

func (h *Handler) deleteWeaponType(ctx context.Context, id string) (bool, error) {
	ct, err := h.db.Exec(ctx, `DELETE FROM rpg_weapon_types WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// ---------------------------------------------------------------------------
// rpg_weapons
// ---------------------------------------------------------------------------

// validRarities CONTRACT §2：common(t1-3)|rare(t4-6)|epic(t7-9)|legendary(t10)。
var validRarities = map[string]bool{"common": true, "rare": true, "epic": true, "legendary": true}

// WeaponRow rpg_weapons 資料列。Profile 直接是解析好的 WeaponProfile（缺欄位已補中性值），API
// 回應與 DB 讀寫共用同一個型別，比照 SkillRow.Effect 的既有慣例。
type WeaponRow struct {
	ID          string        `json:"id"`
	TypeID      string        `json:"type_id"`
	Tier        int           `json:"tier"`
	Name        string        `json:"name"`
	Rarity      string        `json:"rarity"`
	LevelReq    int           `json:"level_req"`
	Element     string        `json:"element"`
	Profile     WeaponProfile `json:"profile"`
	Description string        `json:"description"`
	IsActive    bool          `json:"is_active"`
	SortOrder   int           `json:"sort_order"`
}

// Validate 只檢查外層欄位；Profile 的詞彙/範圍已由 ParseWeaponProfile 在建構 WeaponRow 時驗證過
// （見 weapons_admin.go AdminPutWeapon），這裡不重複。
func (w WeaponRow) Validate() error {
	if w.ID == "" || w.TypeID == "" || w.Name == "" {
		return fmt.Errorf("id/type_id/name 不可為空")
	}
	if w.Tier < 1 || w.Tier > 10 {
		return fmt.Errorf("tier 必須介於 1..10")
	}
	if !validRarities[w.Rarity] {
		return fmt.Errorf("rarity 不合法")
	}
	if w.LevelReq < 1 || w.LevelReq > 99 {
		return fmt.Errorf("level_req 必須介於 1..99")
	}
	if w.Element != "" && !validElementKinds[w.Element] {
		return fmt.Errorf("element 不合法")
	}
	return nil
}

const weaponCols = `id, type_id, tier, name, rarity, level_req, element, profile, description, is_active, sort_order`

func scanWeapon(row pgx.Row) (WeaponRow, error) {
	var w WeaponRow
	var raw []byte
	if err := row.Scan(&w.ID, &w.TypeID, &w.Tier, &w.Name, &w.Rarity, &w.LevelReq, &w.Element, &raw, &w.Description, &w.IsActive, &w.SortOrder); err != nil {
		return w, err
	}
	profile, err := ParseWeaponProfile(raw)
	if err != nil {
		// 壞掉/不合法的 profile JSON 不該讓整支 API 500（比照 scanSkill 對 effect 的既有慣例）——
		// 回中性值，後台看得到這筆資料，可以馬上重新存檔修正。
		profile = DefaultWeaponProfile()
	}
	w.Profile = profile
	return w, nil
}

func (h *Handler) listWeapons(ctx context.Context, activeOnly bool) ([]WeaponRow, error) {
	q := `SELECT ` + weaponCols + ` FROM rpg_weapons`
	if activeOnly {
		q += ` WHERE is_active`
	}
	q += ` ORDER BY type_id, tier, id`
	rows, err := h.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WeaponRow{}
	for rows.Next() {
		w, err := scanWeapon(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// weaponColsPrefixed 同 weaponCols，逐欄加上 "w." 別名前綴——JOIN rpg_weapon_types 時兩張表都有
// id 欄位，SELECT 清單必須指名屬於哪張表，避免歧義。純字面常數，跟 weaponCols 手動同步維護。
const weaponColsPrefixed = `w.id, w.type_id, w.tier, w.name, w.rarity, w.level_req, w.element, w.profile, w.description, w.is_active, w.sort_order`

// listWeaponsByJob GET /rpg/equipment 用：某職業目前全部可裝備的武器（依類型 sort_order、tier
// 排序），JOIN weapon_types 取得 job_id 對應關係。
func (h *Handler) listWeaponsByJob(ctx context.Context, jobID string) ([]WeaponRow, error) {
	rows, err := h.db.Query(ctx, `
		SELECT `+weaponColsPrefixed+` FROM rpg_weapons w
		JOIN rpg_weapon_types t ON t.id = w.type_id
		WHERE t.job_id=$1 AND w.is_active
		ORDER BY t.sort_order, w.tier, w.id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WeaponRow{}
	for rows.Next() {
		w, err := scanWeapon(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (h *Handler) getWeaponByID(ctx context.Context, id string) (WeaponRow, error) {
	row := h.db.QueryRow(ctx, `SELECT `+weaponCols+` FROM rpg_weapons WHERE id=$1`, id)
	return scanWeapon(row)
}

// getWeaponWithType 裝備端點需要同時知道武器本身與其類型（job_id 做「wrong_job」判斷、visual 做
// 戰鬥視覺）。查無武器或查無類型皆回 pgx.ErrNoRows（呼叫端統一當 not_found 處理）。
func (h *Handler) getWeaponWithType(ctx context.Context, id string) (WeaponRow, WeaponTypeRow, error) {
	w, err := h.getWeaponByID(ctx, id)
	if err != nil {
		return WeaponRow{}, WeaponTypeRow{}, err
	}
	t, err := h.getWeaponTypeByID(ctx, w.TypeID)
	if err != nil {
		return WeaponRow{}, WeaponTypeRow{}, err
	}
	return w, t, nil
}

// WeaponWithType DORPG P9 N+1 修復（見 presets.go loadGearCatalog 檔頭說明）：批次查詢的武器＋
// 類型合併結果，取代 resolvePresetEquipmentSlots 原本逐格呼叫 getWeaponWithType 的個別查詢。
type WeaponWithType struct {
	Weapon WeaponRow
	Type   WeaponTypeRow
}

// loadWeaponsWithTypesByIDs 一次撈出多把武器＋各自的類型（JOIN rpg_weapon_types，
// `WHERE w.id = ANY($1)`），取代呼叫端對每個 id 各發一次 getWeaponWithType 的做法。ids 為空時
// 略過查詢直接回空 map（避免對 Postgres 傳空陣列的邊界行為疑慮，且呼叫端本來就不需要查）。
// 查無資料的 id 純粹不會出現在回傳 map 裡（不是錯誤）——呼叫端（resolvePresetEquipmentSlots）
// 用 map 的 ok 判斷是否查到，查不到視為「該格未裝備」。
func (h *Handler) loadWeaponsWithTypesByIDs(ctx context.Context, ids []string) (map[string]WeaponWithType, error) {
	out := map[string]WeaponWithType{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := h.db.Query(ctx, `
		SELECT `+weaponColsPrefixed+`, t.id, t.job_id, t.name, t.visual, t.elemental_capable, t.description, t.traits, t.sort_order
		FROM rpg_weapons w
		JOIN rpg_weapon_types t ON t.id = w.type_id
		WHERE w.id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var wr WeaponRow
		var wraw []byte
		var tr WeaponTypeRow
		var traw []byte
		if err := rows.Scan(&wr.ID, &wr.TypeID, &wr.Tier, &wr.Name, &wr.Rarity, &wr.LevelReq, &wr.Element, &wraw, &wr.Description, &wr.IsActive, &wr.SortOrder,
			&tr.ID, &tr.JobID, &tr.Name, &tr.Visual, &tr.ElementalCapable, &tr.Description, &traw, &tr.SortOrder); err != nil {
			return nil, err
		}
		profile, perr := ParseWeaponProfile(wraw)
		if perr != nil {
			// 壞掉/不合法的 profile JSON 不該讓整支 API 500，比照 scanWeapon 的既有慣例回中性值。
			profile = DefaultWeaponProfile()
		}
		wr.Profile = profile
		tr.Traits = map[string]any{}
		if len(traw) > 0 {
			if jerr := json.Unmarshal(traw, &tr.Traits); jerr != nil {
				tr.Traits = map[string]any{}
			}
		}
		out[wr.ID] = WeaponWithType{Weapon: wr, Type: tr}
	}
	return out, rows.Err()
}

func (h *Handler) upsertWeapon(ctx context.Context, w WeaponRow) error {
	profileRaw, err := json.Marshal(w.Profile)
	if err != nil {
		return err
	}
	_, err = h.db.Exec(ctx, `
		INSERT INTO rpg_weapons (id, type_id, tier, name, rarity, level_req, element, profile, description, is_active, sort_order, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NOW())
		ON CONFLICT (id) DO UPDATE SET
			type_id=$2, tier=$3, name=$4, rarity=$5, level_req=$6, element=$7, profile=$8, description=$9,
			is_active=$10, sort_order=$11, updated_at=NOW()`,
		w.ID, w.TypeID, w.Tier, w.Name, w.Rarity, w.LevelReq, w.Element, profileRaw, w.Description, w.IsActive, w.SortOrder)
	return err
}

func (h *Handler) deleteWeapon(ctx context.Context, id string) (bool, error) {
	ct, err := h.db.Exec(ctx, `DELETE FROM rpg_weapons WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// ---------------------------------------------------------------------------
// WeaponProfileWire：戰鬥 bootstrap 用的 camelCase 鏡射（WIRE §「戰鬥 bootstrap」）。只包含
// Compute() 沒有吃掉的欄位——int_bonus/mp_pct/atk_pct/matk_pct/def_pct/mdef_pct/flee_bonus 已經
// 反映在玩家的 stats/derived 裡，不需要再送一次；crit_pct 例外，WIRE 明講「仍送供除錯顯示」。
// ---------------------------------------------------------------------------

// WeaponSizeBonusWire WeaponSizeBonus 的 camelCase 鏡射。
type WeaponSizeBonusWire struct {
	Small  float64 `json:"small"`
	Medium float64 `json:"medium"`
	Large  float64 `json:"large"`
}

type WeaponProfileWire struct {
	Atk               float64             `json:"atk"`
	Matk              float64             `json:"matk"`
	Hits              int                 `json:"hits"`
	HitMul            float64             `json:"hitMul"`
	ExtraHitChancePct float64             `json:"extraHitChancePct"`
	IntervalPct       float64             `json:"intervalPct"`
	ChargeTimeMul     float64             `json:"chargeTimeMul"`
	ChargeDmgMul      float64             `json:"chargeDmgMul"`
	SplashPct         float64             `json:"splashPct"`
	SizeBonus         WeaponSizeBonusWire `json:"sizeBonus"`
	CritPct           float64             `json:"critPct"`
	CritDmgPct        float64             `json:"critDmgPct"`
	ElementResistPct  float64             `json:"elementResistPct"`
	MagicSkillPct     float64             `json:"magicSkillPct"`
	Element           string              `json:"element"`

	// --- DORPG P12（CONTRACT §3「weapon.profile 新增 rowBonusFrontPct／rowBonusRearPct／
	// pierceChancePct／pierceDmgPct（由 type.traits 合併，缺省 0）」）：不是 WeaponProfile
	// 的欄位（沒有對應 rpg_weapons.profile JSON 鍵），而是從 rpg_weapon_types.traits 合併進來
	// ——withRowBonus() 負責合併，battle.go buildPlayerWeaponWire 呼叫（玩家與傭兵共用同一條
	// 路徑）。沒有武器類型（未裝備／傭兵固定視覺）時維持零值，跟其餘欄位「缺省＝中性值」一致。
	RowBonusFrontPct float64 `json:"rowBonusFrontPct"`
	RowBonusRearPct  float64 `json:"rowBonusRearPct"`
	PierceChancePct  float64 `json:"pierceChancePct"`
	PierceDmgPct     float64 `json:"pierceDmgPct"`
}

// ToWeaponProfileWire 轉換成戰鬥 bootstrap 要送給引擎的形狀（battle.go 組 party member 用）。
func ToWeaponProfileWire(p WeaponProfile) WeaponProfileWire {
	return WeaponProfileWire{
		Atk: p.Atk, Matk: p.Matk, Hits: p.Hits, HitMul: p.HitMul,
		ExtraHitChancePct: p.ExtraHitChancePct, IntervalPct: p.IntervalPct,
		ChargeTimeMul: p.ChargeTimeMul, ChargeDmgMul: p.ChargeDmgMul, SplashPct: p.SplashPct,
		SizeBonus:        WeaponSizeBonusWire{Small: p.SizeBonus.Small, Medium: p.SizeBonus.Medium, Large: p.SizeBonus.Large},
		CritPct:          p.CritPct,
		CritDmgPct:       p.CritDmgPct,
		ElementResistPct: p.ElementResistPct,
		MagicSkillPct:    p.MagicSkillPct,
		Element:          p.Element,
	}
}

// ---------------------------------------------------------------------------
// DORPG P12（CONTRACT §1/§2/§3）：怪物前排／後排 × 武器排位加成——弓對後排 +10%、鈍器對前排
// +10%、槍機率貫穿波及後排。加成掛在 rpg_weapon_types.traits（migration 188 新增四個鍵，見
// 該檔），這裡只提供讀 traits 的純函式；套用到傷害公式／貫穿判定是 ENGINE 前端的事
// （engine/combat.ts resolveWeaponAttack，CONTRACT §3）。
// ---------------------------------------------------------------------------

// WeaponTypeRowBonus migration 188 四個新鍵解析後的結果，中性值全為 0（沒有這些鍵＝武器類型
// 沒有排位加成／貫穿能力，不改變既有戰鬥手感，比照 WeaponProfile 缺欄位＝中性值的既有慣例）。
type WeaponTypeRowBonus struct {
	RowBonusFrontPct float64
	RowBonusRearPct  float64
	PierceChancePct  float64
	PierceDmgPct     float64
}

// weaponTypeRowBonus 從 WeaponTypeRow.Traits（scanWeaponType 已經是解析好的 map[string]any，壞
// JSON 已在那裡退回空物件）讀四個新鍵。刻意寬鬆：鍵不存在／型別不是數字一律當 0（traits 是後台
// 可自由編輯的 JSONB，不能讓打錯型別的值把戰鬥 bootstrap 500 掉，比照 scanWeaponType 對整個
// JSON 壞掉的既有退讓風格）；負值歸零（沒有「負加成」的設計意圖，之後真要做負加成再另開鍵）；
// pct 系列額外夾在 0..100（pierce_dmg_pct 是「波及傷害佔比」，>100% 沒有意義；跟 Validate() 對
// extra_hit_chance_pct 的既有夾限風格一致）。
func weaponTypeRowBonus(traits map[string]any) WeaponTypeRowBonus {
	get := func(key string) float64 {
		v, ok := traits[key]
		if !ok {
			return 0
		}
		// encoding/json 把 JSON 數字 Unmarshal 進 map[string]any 一律是 float64（json.Number 只
		// 有搭配 Decoder.UseNumber() 才會出現，scanWeaponType 用的是 json.Unmarshal，沒有那個
		// 選項）；其餘型別（字串/布林/物件/陣列）視為打錯型別，當缺省處理。
		f, ok := v.(float64)
		if !ok {
			return 0
		}
		if f < 0 {
			return 0
		}
		if f > 100 {
			return 100
		}
		return f
	}
	return WeaponTypeRowBonus{
		RowBonusFrontPct: get("row_bonus_front_pct"),
		RowBonusRearPct:  get("row_bonus_rear_pct"),
		PierceChancePct:  get("pierce_chance_pct"),
		PierceDmgPct:     get("pierce_dmg_pct"),
	}
}

// withRowBonus 把 WeaponTypeRowBonus 併入既有 WeaponProfileWire（其餘欄位不動），battle.go
// buildPlayerWeaponWire 呼叫——玩家與傭兵（傭兵裝備武器時走同一條 buildPlayerWeaponWire）共用
// 這個合併點，不重複實作兩次。
func (w WeaponProfileWire) withRowBonus(b WeaponTypeRowBonus) WeaponProfileWire {
	w.RowBonusFrontPct = b.RowBonusFrontPct
	w.RowBonusRearPct = b.RowBonusRearPct
	w.PierceChancePct = b.PierceChancePct
	w.PierceDmgPct = b.PierceDmgPct
	return w
}
