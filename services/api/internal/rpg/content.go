// content.go：DORPG P2 內容資料型別（技能/道具/場景/遭遇/戰鬥紀錄）與欄位白名單驗證。
// MonsterRow/CompanionRow/ActorStats 定義在 scaling.go（縮放公式需要用到，見該檔檔頭說明）；
// 其餘內容表的型別集中在這裡。json tag 一律 snake_case、逐欄對齊 migration 176 的 DDL 欄位名
// （BattleSample 相關的 camelCase wire 型別在 battle.go，兩邊刻意分檔避免混淆）。
package rpg

import "fmt"

// --- 列舉白名單（migration 176 DDL 註解的合法值集合）。admin CRUD 寫入與 /rpg/battle/report
// 遙測回報共用同一份，值域改了只需要動這裡。---

// validSkillKinds P5（CONTRACT §5）新增 buff/debuff/passive/special 四種 kind——damage/heal/
// shield 三種是 P2 既有實作，其餘四種是本輪新增的效果詞彙（passive 不進技能欄、special 本輪
// 未實裝，皆由呼叫端依 kind 分流處理，見 skills.go ExpandEffect）。taunt 是 DORPG P10（CONTRACT
// §2/§3）新增：重騎士守護路線的挑釁／守護姿態，展開規則另見 skills.go ExpandEffect 的 taunt 分支。
var validSkillKinds = map[string]bool{
	"damage": true, "heal": true, "shield": true,
	"buff": true, "debuff": true, "passive": true, "special": true,
	"taunt": true,
}

// validSkillPaths DORPG P10：技能樹路線白名單——既有 5 個無職業技能 path="" 留空字串合法，六職業
// 一律 "a"/"b"，只有 heavy_knight 多一條 "c"（守護，CONTRACT §1/§2）。之前沒有這個白名單（P5～P9
// 都靠職業本身只定義 a/b 兩條路線間接保證），P10 起路線數量不再固定兩條，值得補一個明確的值域檢查。
var validSkillPaths = map[string]bool{"": true, "a": true, "b": true, "c": true}

// validSkillTargets 新增 allEnemies（P5：damage 可以 target=allEnemies 打全體、debuff 同理）。
var validSkillTargets = map[string]bool{"enemy": true, "ally": true, "self": true, "allAllies": true, "allEnemies": true}

// validDmgTypes P5：damage kind 用來決定扣哪一邊防禦（physical→ATK 扣 DEF、magic→MATK 扣
// MDEF，見 CONTRACT §6）；heal/shield/buff/debuff/passive/special 不看這個欄位，欄位仍會有值
// （DB NOT NULL DEFAULT 'physical'）只是沒有實際效果。
var validDmgTypes = map[string]bool{"physical": true, "magic": true}
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
	// 審查#4【低・CONFIRMED】根因修復：weak_elements 完全沒有值域檢查，後台存進一個打錯字的
	// 屬性代碼（例如 "fier"）不會有任何錯誤——這隻怪的弱點就會在 engine 的 elementMultiplier()
	// 用 includes() 比對時安靜地永遠比對不到（見 formulas.ts 該函式），效果悄悄失效，很難排查。
	// 比照 SkillRow.Validate() 對 element 欄位的既有作法，套用同一張 validElementKinds 值域表。
	for _, el := range m.WeakElements {
		if !validElementKinds[el] {
			return fmt.Errorf("weak_elements 內含不合法的屬性：%s", el)
		}
	}
	return nil
}

// JobPathRow 職業的其中一條路線（WIRE JobDTO.path_a/path_b/path_c）。
type JobPathRow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc"`
}

// JobPathKeyRow DORPG P10（WIRE JobDTO.paths）：路線陣列的單一元素，比 PathA/PathB/PathC 三個
// 各自獨立的欄位多帶一個 Key，讓前端（酒館腳本技能區／角色頁技能頁／後台技能表）可以用同一段
// 迴圈通用渲染「這個職業有幾條路線」，不必寫死「一定是兩條」。只有 heavy_knight 的切片含第三個
// 元素（key="c"），見 buildJobPaths（jobs.go）。
type JobPathKeyRow struct {
	ID   string `json:"id"`
	Key  string `json:"key"` // "a" | "b" | "c"
	Name string `json:"name"`
	Desc string `json:"desc"`
}

// JobTraits DORPG P10（CONTRACT §1/§4）：職業天生特性，目前只定義 damage_taken_pct 一項——用
// 指標分辨「這個職業沒有這項特性」（omitempty，整個物件序列化成 {}）與「值剛好是 0」。未來若要
// 新增別的特性鍵，在這裡加欄位＋Validate() 加一段即可，仍然共用同一份 rpg_jobs.traits JSONB。
type JobTraits struct {
	DamageTakenPct *float64 `json:"damage_taken_pct,omitempty"`
}

// Validate 後台 PUT /admin/rpg/jobs 用：目前唯一允許的特性鍵 damage_taken_pct 必須落在
// [-60,0]——正值等於懲罰自己不合理；-60 是 P8 裝備彙總既有的下限（armor.go AggregateEquipment，
// 不在本輪 BACKEND 所有權內，見下方 JobRow.PathC 註解），traits 疊加進 equipmentEffects 後還會
// 被 battle.go 的 clampDamageTakenPct 再夾一次，這裡先擋離譜輸入。
func (t JobTraits) Validate() error {
	if t.DamageTakenPct != nil {
		v := *t.DamageTakenPct
		if v < -60 || v > 0 {
			return fmt.Errorf("traits.damage_taken_pct 必須介於 -60..0")
		}
	}
	return nil
}

// JobRow rpg_jobs 資料列，json tag 直接對齊 WIRE 的 JobDTO——不需要另外轉一層 DTO。P10 起有
// 後台 CRUD（PUT /admin/rpg/jobs，見 battle_admin.go／jobs.go），推翻 P5 時代「本輪沒有後台
// CRUD」的舊決策，但六職業本身仍是固定 6 筆（migration 180 seed），只開放編輯既有列。
type JobRow struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Tagline          string     `json:"tagline"`
	Description      string     `json:"description"`
	PathA            JobPathRow `json:"path_a"`
	PathB            JobPathRow `json:"path_b"`
	Weapon           string     `json:"weapon"`
	AtkBranch        string     `json:"atk_branch"`
	RecommendedStats string     `json:"recommended_stats"`
	SortOrder        int        `json:"sort_order"`

	// PathC/Traits/Paths DORPG P10（CONTRACT §1/§2、WIRE）：第三條技能路線（只有 heavy_knight
	// 有值，nil＝這個職業沒有）與職業天生特性。兩者都不在 content_repo.go 既有的 jobCols/scanJob
	// 查詢欄位內——content_repo.go 屬於另一個角色的所有權（本輪 BACKEND 只能改 content.go/
	// skills.go/jobs.go/handler.go/battle.go/tavern.go/presets.go/presets_test.go/
	// battle_admin.go，見 migrations/187_rpg_p10_guardian.sql 檔頭），所以另外用 jobs.go 的
	// loadJobExtras()/getJobByIDFull()/listJobsFull() 查這四個新欄位、合併進一個「完整」JobRow，
	// 而不是去改那個檔案的 SELECT 欄位清單。凡是要把 JobDTO 回給外部（/rpg/me、/rpg/jobs、
	// /rpg/tavern、/rpg/skills、後台）的呼叫端都必須改用 *Full 版本，否則 Paths/Traits/PathC
	// 會靜靜地維持零值——這不是 500，但會讓某些端點「看起來」缺第三條路線／沒有特性，容易誤判成
	// bug，見各呼叫端的修改註解。
	PathC  *JobPathRow     `json:"path_c,omitempty"`
	Traits JobTraits       `json:"traits"`
	Paths  []JobPathKeyRow `json:"paths"`
}

// SkillEffect P5（CONTRACT §5）技能效果詞彙——所有 kind 共用同一個彈性結構存進 rpg_skills.effect
// JSONB，欄位依 kind 取用哪些有意義（例如 damage/heal/shield 用 CoefBase/FlatBase 那組，
// buff/debuff/passive 用 Stat/ValueBase 那組），未用到的欄位留零值即可。展開成戰鬥/顯示用的
// 即時數值見 skills.go 的 EffectAtLevel/ExpandEffect。
type SkillEffect struct {
	CoefBase      float64 `json:"coef_base,omitempty"`
	CoefPerLevel  float64 `json:"coef_per_level,omitempty"`
	Hits          int     `json:"hits,omitempty"`
	FlatBase      float64 `json:"flat_base,omitempty"`
	FlatPerLevel  float64 `json:"flat_per_level,omitempty"`
	Target        string  `json:"target,omitempty"`
	DurationMs    int     `json:"duration_ms,omitempty"`
	Stat          string  `json:"stat,omitempty"`
	ValueBase     float64 `json:"value_base,omitempty"`
	ValuePerLevel float64 `json:"value_per_level,omitempty"`
	Text          string  `json:"text,omitempty"` // kind=special：尚未實裝，純顯示文字

	// --- DORPG P10（CONTRACT §2/§3）新增：kind=taunt 專屬欄位，與 passive 的 guard_taunt。
	// duration_ms（既有欄位）用於「持續時間不隨等級變化」的 buff/debuff；taunt 的持續時間
	// 會隨等級延長，所以另外開 base/per_level 這組，跟 damage/heal 的 coef_base/coef_per_level
	// 是同一種命名慣例。---
	DurationBaseMs         int     `json:"duration_base_ms,omitempty"`
	DurationPerLevelMs     int     `json:"duration_per_level_ms,omitempty"`
	Retarget               bool    `json:"retarget,omitempty"`                   // kind=taunt：是否讓場上敵人立刻改鎖施放者
	DamageTakenPctBase     float64 `json:"damage_taken_pct_base,omitempty"`      // kind=taunt：守護姿態的減傷（負值）
	DamageTakenPctPerLevel float64 `json:"damage_taken_pct_per_level,omitempty"` // kind=taunt
	GuardTaunt             bool    `json:"guard_taunt,omitempty"`                // kind=passive：學到後按防禦即進入守護狀態
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

	// --- P5 新欄位（migration 180）：職業技能樹。既有 5 個技能列這批欄位維持零值
	// （job_id=NULL、path=""、tier=0、max_level=1、effect={}、dmg_type="physical"），
	// 純粹是「舊資料，玩家技能欄不會吃到它們」，不代表遊戲設計上的意義。---
	JobID          *string     `json:"job_id"`
	Path           string      `json:"path"` // "a" | "b"（既有列為空字串）
	Tier           int         `json:"tier"`
	MaxLevel       int         `json:"max_level"`
	Effect         SkillEffect `json:"effect"`
	DmgType        string      `json:"dmg_type"` // physical | magic
	PrereqSkillID  *string     `json:"prereq_skill_id"`
	PrereqLevel    int         `json:"prereq_level"`
	MPCostPerLevel float64     `json:"mp_cost_per_level"`
	DisplayText    string      `json:"display_text"`
	Implemented    bool        `json:"implemented"`
}

// Validate 沿用既有寬鬆風格：只擋會讓 scanSkill/前端渲染壞掉的欄位，P5 新欄位大多沒有嚴格值域
// （dmg_type 允許空字串，容忍既有後台編輯表單本輪尚未更新、送出時沒有這個欄位的情況）。
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
	// DORPG P10：path 值域檢查（見 validSkillPaths 註解）——後台編輯技能表打錯字（例如 "C" 大寫、
	// "d"）會被這裡擋下，而不是安靜地存進一個 listSkillsByJob(jobID) 永遠篩不到、ValidatePreset
	// 也永遠比對不到前置鏈的孤兒列。
	if !validSkillPaths[s.Path] {
		return fmt.Errorf("path 不合法")
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
	if s.DmgType != "" && !validDmgTypes[s.DmgType] {
		return fmt.Errorf("dmg_type 不合法")
	}
	if s.MaxLevel < 0 {
		return fmt.Errorf("max_level 必須 >= 0")
	}
	if s.Tier < 0 || s.PrereqLevel < 0 || s.MPCostPerLevel < 0 {
		return fmt.Errorf("tier/prereq_level/mp_cost_per_level 必須 >= 0")
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
	id         string
	Code       string  `json:"code"`
	Title      string  `json:"title"`
	Subtitle   string  `json:"subtitle"`
	SceneID    string  `json:"scene_id"`
	SceneKind  string  `json:"scene_kind"`
	Difficulty int     `json:"difficulty"`
	PowerScale float64 `json:"power_scale"`
	// MonsterLevel DORPG P6（CONTRACT §2）：battle_scale_mode="level" 時，這場遭遇的怪物一律
	// 顯示並套用這個等級（RefPlayerStats(cfg,MonsterLevel) 當基準，見 scaling.go
	// ScaleMonsterByLevel）。"power" 模式完全不讀這個欄位（怪物等級借用玩家 Base Lv）。
	MonsterLevel int                   `json:"monster_level"`
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
	if e.MonsterLevel < 1 || e.MonsterLevel > 99 {
		return fmt.Errorf("monster_level 必須介於 1..99")
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
