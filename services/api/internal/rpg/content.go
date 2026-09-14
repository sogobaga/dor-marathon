// content.go：DORPG P2 內容資料型別（技能/道具/場景/遭遇/戰鬥紀錄）與欄位白名單驗證。
// MonsterRow/CompanionRow/ActorStats 定義在 scaling.go（縮放公式需要用到，見該檔檔頭說明）；
// 其餘內容表的型別集中在這裡。json tag 一律 snake_case、逐欄對齊 migration 176 的 DDL 欄位名
// （BattleSample 相關的 camelCase wire 型別在 battle.go，兩邊刻意分檔避免混淆）。
package rpg

import "fmt"

// --- 列舉白名單（migration 176 DDL 註解的合法值集合）。admin CRUD 寫入與 /rpg/battle/report
// 遙測回報共用同一份，值域改了只需要動這裡。---

var validSkillKinds = map[string]bool{"damage": true, "heal": true, "shield": true}
var validSkillTargets = map[string]bool{"enemy": true, "ally": true, "self": true, "allAllies": true}
var validWeaponKinds = map[string]bool{"sword": true, "staff": true, "bow": true, "greatsword": true}
var validItemKinds = map[string]bool{"hp": true, "mp": true, "revive": true}
var validEncounterSlots = map[string]bool{
	"rear_left": true, "rear_right": true, "front_left": true, "front_center": true, "front_right": true,
}
var validSceneKinds = map[string]bool{"normal": true, "boss": true}
var validBattleOutcomes = map[string]bool{"victory": true, "defeat": true, "draw": true, "escaped": true, "abandoned": true}

// validElementKinds 技能 element 的八屬性值域，對齊前端 types.ts ElementKind（英文代碼：
// metal/wood/water/fire/earth/light/dark/neutral）。
//
// ⚠️ 這個值域刻意不拿來檢查 rpg_monsters.attribute/size：migration 176 實際 seed 的是中文原文
// （'闇'/'金'/'土'/'無'/'木'、'大型'/'中型'/'小型'，逐字取自內容包 monster.json），跟這裡的
// DDL 欄位註解列的英文代碼只是「語意上對應哪些值」的參考、不是實際儲存格式（見該 migration
// 檔頭大段說明：現有前端只把這兩欄當純顯示文字印出，不做屬性克制運算）。若在這裡對
// attribute/size 套用英文白名單，後台編輯既有怪物直接存檔就會被 400 擋下——見下面
// MonsterRow.Validate() 只檢查非空，不檢查值域，比照 rank/race 這類純顯示文字欄位。
var validElementKinds = map[string]bool{
	"metal": true, "wood": true, "water": true, "fire": true, "earth": true, "light": true, "dark": true, "neutral": true,
}

// Validate 逐欄檢查（admin PUT /admin/rpg/monsters 用）。只檢查會讓前端渲染/scaling 算式壞掉的
// 欄位，不檢查 name/rank/race/attribute/size 這類純顯示文字的內容（見上方 validElementKinds
// 註解：attribute/size 實際存的是中文原文，沒有封閉值域）。
func (m MonsterRow) Validate() error {
	if m.ID == "" {
		return fmt.Errorf("id 不可為空")
	}
	if m.Name == "" {
		return fmt.Errorf("name 不可為空")
	}
	if m.HPMult <= 0 || m.AtkMult <= 0 || m.DefMult <= 0 || m.SpeedMult <= 0 {
		return fmt.Errorf("hp_mult/atk_mult/def_mult/speed_mult 必須 > 0")
	}
	return nil
}

// SkillRow rpg_skills 資料列。
type SkillRow struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	IconID      string  `json:"icon_id"`
	Kind        string  `json:"kind"`
	Target      string  `json:"target"`
	Weapon      string  `json:"weapon"`
	Element     string  `json:"element"`
	MPCost      int     `json:"mp_cost"`
	CooldownMs  int     `json:"cooldown_ms"`
	Coefficient float64 `json:"coefficient"`
	Flat        int     `json:"flat"`
	CastMs      int     `json:"cast_ms"`
	IsDefault   bool    `json:"is_default"`
	IsActive    bool    `json:"is_active"`
	SortOrder   int     `json:"sort_order"`
}

func (s SkillRow) Validate() error {
	if s.ID == "" || s.Name == "" {
		return fmt.Errorf("id/name 不可為空")
	}
	if !validSkillKinds[s.Kind] {
		return fmt.Errorf("kind 不合法")
	}
	if !validSkillTargets[s.Target] {
		return fmt.Errorf("target 不合法")
	}
	if !validWeaponKinds[s.Weapon] {
		return fmt.Errorf("weapon 不合法")
	}
	if s.Element != "" && !validElementKinds[s.Element] {
		return fmt.Errorf("element 不合法")
	}
	if s.MPCost < 0 || s.CooldownMs < 0 || s.CastMs < 0 {
		return fmt.Errorf("mp_cost/cooldown_ms/cast_ms 必須 >= 0")
	}
	return nil
}

// ItemRow rpg_items 資料列。
type ItemRow struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	IconID          string `json:"icon_id"`
	Kind            string `json:"kind"`
	Amount          int    `json:"amount"`
	DefaultQuantity int    `json:"default_quantity"`
	IsActive        bool   `json:"is_active"`
	SortOrder       int    `json:"sort_order"`
}

func (it ItemRow) Validate() error {
	if it.ID == "" || it.Name == "" {
		return fmt.Errorf("id/name 不可為空")
	}
	if !validItemKinds[it.Kind] {
		return fmt.Errorf("kind 不合法")
	}
	if it.Amount < 0 || it.DefaultQuantity < 0 {
		return fmt.Errorf("amount/default_quantity 必須 >= 0")
	}
	if it.Kind == "revive" && it.Amount > 100 {
		return fmt.Errorf("revive 的 amount 是百分比，必須 <= 100")
	}
	return nil
}

// SceneSlotRow rpg_scenes.slots 陣列元素——直接對齊前端 apps/web/src/lib/dorpg/types.ts 的
// SceneSlot（camelCase）。這個型別最終會被 battle.go 原封不動塞進 BattleSample.scene.slots，
// MIGRATION 的 seed JSON 可以逐字複製前端規格，不必再轉換大小寫（契約 §3 檔頭「刻意混用」）。
type SceneSlotRow struct {
	ID    string  `json:"id"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Scale float64 `json:"scale"`
	Row   string  `json:"row"`
}

// SceneRow rpg_scenes 資料列。
type SceneRow struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	ImageURL     string         `json:"image_url"`
	Slots        []SceneSlotRow `json:"slots"`
	LocationNote string         `json:"location_note"`
	IsActive     bool           `json:"is_active"`
	SortOrder    int            `json:"sort_order"`
}

// 審查6 PLAUSIBLE（刻意不修，理由同 EncounterRow.Validate）：不要求 Slots 涵蓋全部 5 個槽位
// id——後台建立場景時可以先存一部分，之後在遭遇分頁指到缺的槽位才會在前端渲染時看到問題，
// 只限受信任管理者觸發，風險低。
func (s SceneRow) Validate() error {
	if s.ID == "" || s.Name == "" {
		return fmt.Errorf("id/name 不可為空")
	}
	for _, slot := range s.Slots {
		if !validEncounterSlots[slot.ID] {
			return fmt.Errorf("slots 內含不合法的槽位 id：%s", slot.ID)
		}
		if slot.Row != "rear" && slot.Row != "front" {
			return fmt.Errorf("slots[%s].row 必須是 rear 或 front", slot.ID)
		}
	}
	return nil
}

func (c CompanionRow) Validate() error {
	if c.ID == "" || c.Name == "" {
		return fmt.Errorf("id/name 不可為空")
	}
	if !validWeaponKinds[c.Weapon] {
		return fmt.Errorf("weapon 不合法")
	}
	if c.HPMult <= 0 || c.MPMult <= 0 || c.AtkMult <= 0 || c.MatkMult <= 0 || c.DefMult <= 0 || c.MdefMult <= 0 || c.ActIntervalMult <= 0 {
		return fmt.Errorf("各項 *_mult 必須 > 0")
	}
	return nil
}

// EncounterMonsterRow rpg_encounter_monsters 一列（遭遇編組的其中一個槽位）。
type EncounterMonsterRow struct {
	Slot       string  `json:"slot"`
	MonsterID  string  `json:"monster_id"`
	PowerScale float64 `json:"power_scale"`
}

// EncounterRow rpg_encounters 資料列（含編組）。id 是 DB 內部 UUID，只用來 JOIN
// rpg_encounter_monsters，刻意不匯出成 JSON 欄位——契約：「對外識別（API 用 code，不用 UUID）」。
type EncounterRow struct {
	id           string
	Code         string                `json:"code"`
	Title        string                `json:"title"`
	Subtitle     string                `json:"subtitle"`
	SceneID      string                `json:"scene_id"`
	SceneKind    string                `json:"scene_kind"`
	Difficulty   int                   `json:"difficulty"`
	PowerScale   float64               `json:"power_scale"`
	EscapeChance float64               `json:"escape_chance"`
	CanEscape    bool                  `json:"can_escape"`
	IsActive     bool                  `json:"is_active"`
	SortOrder    int                   `json:"sort_order"`
	Monsters     []EncounterMonsterRow `json:"monsters"`
}

// 審查6 PLAUSIBLE（刻意不修）：這裡只檢查欄位格式，不檢查 SceneID/monster_id 是否真的存在於
// rpg_scenes/rpg_monsters（也不要求 Monsters 至少 1 筆）——後台填錯會在 DB 層 FK 違反時才炸
// （upsertEncounter 沒有 FK，但 rpg_encounter_monsters.monster_id/rpg_encounters.scene_id 有），
// 或前端引擎收到不完整資料時才出問題。只有受信任的後台管理者能觸發（perm("rpg")），風險低，
// 不值得在這層額外查兩次表換取更早的錯誤訊息；FE_ADMIN 若要在表單層防呆（例如下拉選單只給
// 選存在的 id）可以完全避免這個情境發生。
func (e EncounterRow) Validate() error {
	if e.Code == "" || e.Title == "" || e.SceneID == "" {
		return fmt.Errorf("code/title/scene_id 不可為空")
	}
	if !validSceneKinds[e.SceneKind] {
		return fmt.Errorf("scene_kind 不合法")
	}
	if e.Difficulty < 1 || e.Difficulty > 5 {
		return fmt.Errorf("difficulty 必須介於 1..5")
	}
	if e.PowerScale <= 0 {
		return fmt.Errorf("power_scale 必須 > 0")
	}
	if e.EscapeChance < 0 || e.EscapeChance > 1 {
		return fmt.Errorf("escape_chance 必須介於 0..1")
	}
	seen := map[string]bool{}
	for _, m := range e.Monsters {
		if !validEncounterSlots[m.Slot] {
			return fmt.Errorf("monsters 內含不合法的槽位：%s", m.Slot)
		}
		if seen[m.Slot] {
			return fmt.Errorf("monsters 槽位重複：%s", m.Slot)
		}
		seen[m.Slot] = true
		if m.MonsterID == "" {
			return fmt.Errorf("monsters[%s].monster_id 不可為空", m.Slot)
		}
		if m.PowerScale <= 0 {
			return fmt.Errorf("monsters[%s].power_scale 必須 > 0", m.Slot)
		}
	}
	return nil
}

// --- 戰鬥遙測（POST /rpg/battle/report）---

// battleLogSanityLimits 契約 §3.4 的健全性檢查邊界。
const (
	minBattleDurationMs = 1000
	maxBattleDurationMs = 1800000
	maxBattleCounter    = 10000
	maxDailyBattleLogs  = 300
)

// BattleLogInput POST /rpg/battle/report 的請求 body。純遙測，不觸碰任何獎勵/帳本（D5）。
type BattleLogInput struct {
	EncounterCode   string `json:"encounter_code"`
	Outcome         string `json:"outcome"`
	DurationMs      int    `json:"duration_ms"`
	DamageDealt     int    `json:"damage_dealt"`
	DamageTaken     int    `json:"damage_taken"`
	EnemiesDefeated int    `json:"enemies_defeated"`
	Attacks         int    `json:"attacks"`
	ChargedAttacks  int    `json:"charged_attacks"`
	SkillsUsed      int    `json:"skills_used"`
	ItemsUsed       int    `json:"items_used"`
	GuardMs         int    `json:"guard_ms"`
	PlayerLevel     int    `json:"player_level"`
	PlayerPower     int    `json:"player_power"`
	ClientVersion   string `json:"client_version"`
}

// clampInt 夾在 [0, max] 之間（負值/超大值都視為異常輸入而非拒絕整筆——遙測資料，容錯優先）。
func clampInt(v, max int) int {
	if v < 0 {
		return 0
	}
	if v > max {
		return max
	}
	return v
}

// sanitize 契約 §3.4 健全性檢查：duration_ms/outcome 不合法直接拒絕（400）；其餘計數只 clamp
// 不拒絕（遙測濫用防護，不是逐欄嚴格驗證——這裡只是後台看手感統計，不值得為了幾個離譜數字
// 就把整筆使用者遙測丟掉）。
func (b *BattleLogInput) sanitize() error {
	if b.EncounterCode == "" {
		return fmt.Errorf("encounter_code 不可為空")
	}
	if !validBattleOutcomes[b.Outcome] {
		return fmt.Errorf("outcome 不合法")
	}
	if b.DurationMs < minBattleDurationMs || b.DurationMs > maxBattleDurationMs {
		return fmt.Errorf("duration_ms 必須介於 %d..%d", minBattleDurationMs, maxBattleDurationMs)
	}
	b.DamageDealt = clampInt(b.DamageDealt, maxBattleCounter*1000) // 傷害量級比次數大，給寬一點的上限
	b.DamageTaken = clampInt(b.DamageTaken, maxBattleCounter*1000)
	b.EnemiesDefeated = clampInt(b.EnemiesDefeated, maxBattleCounter)
	b.Attacks = clampInt(b.Attacks, maxBattleCounter)
	b.ChargedAttacks = clampInt(b.ChargedAttacks, maxBattleCounter)
	b.SkillsUsed = clampInt(b.SkillsUsed, maxBattleCounter)
	b.ItemsUsed = clampInt(b.ItemsUsed, maxBattleCounter)
	b.GuardMs = clampInt(b.GuardMs, maxBattleDurationMs)
	b.PlayerLevel = clampInt(b.PlayerLevel, 999)
	b.PlayerPower = clampInt(b.PlayerPower, maxBattleCounter*1000)
	if len(b.ClientVersion) > 64 {
		b.ClientVersion = b.ClientVersion[:64]
	}
	return nil
}
