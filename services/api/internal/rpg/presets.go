// presets.go：DORPG P6（CONTRACT §3、WIRE §3.3）傭兵腳本——rpg_companion_presets 的讀寫、
// 純函式驗證 ValidatePreset()、與 POST/PUT/DELETE /rpg/presets、POST /rpg/presets/validate 四支
// 端點。GET /rpg/tavern、PUT /rpg/party 在 tavern.go（同一套資料型別，兩檔互相呼叫彼此的
// 未匯出函式，屬於同一個 package，不需要另外匯出）。
package rpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/auth"
)

// ---------------------------------------------------------------------------
// 純函式驗證（契約 §3.2）：與玩家配點/技能點規則完全相同，只是套用在「腳本」這個獨立的
// (level, stats, skill_levels) 三元組上，不碰 DB、不看目前玩家是誰。
// ---------------------------------------------------------------------------

// PresetError WIRE：POST /rpg/presets/validate 的 errors[] 單一項目，也用於 POST/PUT
// /rpg/presets 存檔失敗時的 400 回應（契約 §3.2：「存檔時整份驗證，不合法 400 帶欄位錯誤」）。
type PresetError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func presetErr(field, code, message string) PresetError {
	return PresetError{Field: field, Code: code, Message: message}
}

// statFieldOrder 固定走訪順序（str→agi→vit→dex→int→luk）——ValidatePreset 的錯誤陣列在相同輸入
// 下必須每次順序一致，才方便前端/測試逐項比對，不能依賴 map 疊代順序（Go map 疊代順序不穩定）。
var statFieldOrder = []string{"str", "agi", "vit", "dex", "int", "luk"}

func statFieldMap(s Stats) map[string]int {
	return map[string]int{"str": s.Str, "agi": s.Agi, "vit": s.Vit, "dex": s.Dex, "int": s.Int, "luk": s.Luk}
}

// ValidatePreset 契約 §3.2：「腳本驗證＝與玩家相同：配點總數 TotalStatPoints(level)、成本
// floor((n−1)/10)+2、每項 ≤ min(MaxStat,level)、技能點 TotalSkillPoints(level)、前置鏈、
// max_level、只能配該傭兵職業的技能」。純函式，不碰 DB，方便單元測試逐條核對（合法／超預算／
// 超 cap／前置／非本職技能）——jobSkills 是這個傭兵目前職業「啟用中」的全部技能
// （listSkillsByJob 的結果），呼叫端負責依 companion_id 找出正確的職業技能集合。
//
// level 超出 1..99 時仍夾住繼續檢查其餘欄位（一次回傳所有能查到的錯誤，而不是查到第一個就
// 停手）——UI 可以一次把整份草案的問題都標紅，不必來回送出好幾次才看到下一個錯誤。
func ValidatePreset(cfg Config, jobSkills []SkillRow, level int, stats Stats, skillLevels map[string]int) []PresetError {
	// 審查【CRITICAL】：合法草稿必須回傳 []，不能是 nil slice——nil slice 經 encoding/json 編碼會
	// 變成 JSON null，PresetsValidate 一律回 "errors": errs，前端 result.errors.find(...) 對 null
	// 呼叫 .find 會直接 TypeError，整個腳本編輯器炸掉（見 TavernScreen.tsx errorFor()）。初始化成
	// 空 slice 讓合法草稿一律序列化成 "errors":[]（見 presets_test.go 的對應測試）。
	errs := []PresetError{}

	lv := level
	if lv < 1 || lv > 99 {
		errs = append(errs, presetErr("level", "level_range", "等級必須介於 1~99"))
		switch {
		case lv < 1:
			lv = 1
		case lv > 99:
			lv = 99
		}
	}

	statCap := StatCap(cfg, lv)
	vals := statFieldMap(stats)
	for _, key := range statFieldOrder {
		v := vals[key]
		if v < cfg.InitialStat {
			errs = append(errs, presetErr(key, "stat_below_initial", fmt.Sprintf("%s 不可低於初始值 %d", key, cfg.InitialStat)))
		}
		if v > statCap {
			errs = append(errs, presetErr(key, "stat_over_cap", fmt.Sprintf("%s 超過上限 %d", key, statCap)))
		}
	}
	statBudget := TotalStatPoints(cfg, lv)
	statSpent := TotalSpentStats(cfg, stats)
	if statSpent > statBudget {
		errs = append(errs, presetErr("stats", "stat_over_budget", fmt.Sprintf("配點總花費 %d 超過預算 %d", statSpent, statBudget)))
	}

	jobByID := make(map[string]SkillRow, len(jobSkills))
	for _, s := range jobSkills {
		jobByID[s.ID] = s
	}
	skillBudget := TotalSkillPoints(cfg, lv)
	skillSpent := 0
	skillIDs := make([]string, 0, len(skillLevels))
	for id := range skillLevels {
		skillIDs = append(skillIDs, id)
	}
	sort.Strings(skillIDs) // 固定順序，理由同 statFieldOrder
	for _, id := range skillIDs {
		lvl := skillLevels[id]
		if lvl <= 0 {
			continue // 0（或負值，理論上不該出現）＝未配點，不需要檢查前置/上限
		}
		s, ok := jobByID[id]
		if !ok {
			errs = append(errs, presetErr(id, "skill_not_in_job", "此技能不屬於這個傭兵的職業"))
			continue // 不屬於這個職業的技能不計入預算——已經是另一個獨立錯誤，不重複懲罰
		}
		if lvl > s.MaxLevel {
			errs = append(errs, presetErr(id, "skill_max_level", fmt.Sprintf("%s 超過最高等級 %d", id, s.MaxLevel)))
		}
		if s.PrereqSkillID != nil && skillLevels[*s.PrereqSkillID] < s.PrereqLevel {
			errs = append(errs, presetErr(id, "skill_prereq", "前置技能等級不足"))
		}
		skillSpent += lvl
	}
	if skillSpent > skillBudget {
		errs = append(errs, presetErr("skill_levels", "skill_over_budget", fmt.Sprintf("技能點花費 %d 超過預算 %d", skillSpent, skillBudget)))
	}
	return errs
}

// ---------------------------------------------------------------------------
// DerivedDTO（WIRE PresetDTO.derived / POST /rpg/presets/validate 的 derived）：整數，
// 已乘上該傭兵自己的 hp_mult 等倍率（Hit/Flee/Aspd/CritPct 沒有對應的傭兵倍率欄位，直接取
// Compute() 原始值，比照既有 CompanionRating() 的既定慣例）。
// ---------------------------------------------------------------------------

type DerivedDTO struct {
	HPMax   int `json:"hp_max"`
	MPMax   int `json:"mp_max"`
	Atk     int `json:"atk"`
	Matk    int `json:"matk"`
	Def     int `json:"def"`
	Mdef    int `json:"mdef"`
	Hit     int `json:"hit"`
	Flee    int `json:"flee"`
	Aspd    int `json:"aspd"`
	CritPct int `json:"crit_pct"`
}

// passivesFromSkillLevels 從一份「技能等級」草案（DB 已存的腳本，或請求 body 尚未落地的草稿皆可）
// 取出目前已配點（level>=1）的被動技能，展開成 Compute() 要吃的 []PassiveEffect——邏輯與
// content_repo.go loadPassives 相同，但那支是連 DB 查目前玩家的已配點技能，這裡吃呼叫端已經
// 準備好的 jobSkills+skillLevels（腳本驗證/預覽時還沒有、也不需要寫進 DB 就能算出來）。
func passivesFromSkillLevels(jobSkills []SkillRow, skillLevels map[string]int) []PassiveEffect {
	var out []PassiveEffect
	for _, s := range jobSkills {
		if s.Kind != "passive" {
			continue
		}
		lvl := skillLevels[s.ID]
		if lvl < 1 {
			continue
		}
		e := ExpandEffect(s, lvl)
		if e.Stat == "" {
			continue
		}
		out = append(out, PassiveEffect{Stat: e.Stat, Value: e.Value})
	}
	return out
}

// computeCompanionDerived 契約 §3.2：「每位＝Compute(cfg, level, stats, job, passives from
// skill_levels)」——傭兵自己的職業決定 WeaponType（atk_branch），被動技能來自傭兵自己的
// skill_levels（不是玩家的）。job.ID 為空字串時（理論上不會發生，見呼叫端保護）比照未選職業，
// WeaponType 留空讓 Compute() 退回 cfg.DefaultWeaponType。
//
// equip DORPG P9（CONTRACT §3）：這位傭兵目前腳本裝備（八格）彙總後的 *EquipBonus，nil＝完全
// 沒有裝備——呼叫端一律用 presetEquip() 產生這個值（該函式在「沒有任何有效裝備」時回傳 nil，
// 不是零值指標），才能保證「equipment 空時輸出逐位元同 P6」：Compute() 對 eq==nil 與
// eq=&EquipBonus{} 兩種輸入的計算路徑不同（後者會多套一次 math.Floor，見 compute.go），零值
// 指標不是安全的「沒有裝備」表示法。
func computeCompanionDerived(cfg Config, job JobRow, jobSkills []SkillRow, level int, stats Stats, skillLevels map[string]int, equip *EquipBonus) Derived {
	weaponType := ""
	if job.ID != "" {
		weaponType = job.AtkBranch
	}
	passives := passivesFromSkillLevels(jobSkills, skillLevels)
	return Compute(cfg, ComputeInput{BaseLevel: level, Stats: stats, WeaponType: weaponType, Passives: passives, Equip: equip})
}

// floorMul math.Floor(v*mult) 的簡短寫法——DerivedDTO/ActorStats 的每一項乘完傭兵倍率都要立刻
// floor 成整數（CONTRACT §1：HP/MP 整數規則不只適用戰鬥中，這裡的「衍生值預覽」同樣不能有
// 小數，見 WIRE DerivedDTO 註解「整數，已乘傭兵倍率」）。
func floorMul(v, mult float64) int { return int(math.Floor(v * mult)) }

// presetDerivedDTO Derived + 該傭兵的 *_mult → DerivedDTO（見上方兩個註解）。
func presetDerivedDTO(comp CompanionRow, d Derived) DerivedDTO {
	return DerivedDTO{
		HPMax:   floorMul(float64(d.MaxHP), comp.HPMult),
		MPMax:   floorMul(float64(d.MaxMP), comp.MPMult),
		Atk:     floorMul(d.Atk, comp.AtkMult),
		Matk:    floorMul(d.Matk, comp.MatkMult),
		Def:     floorMul(d.Def, comp.DefMult),
		Mdef:    floorMul(d.Mdef, comp.MdefMult),
		Hit:     int(math.Floor(d.Hit)),
		Flee:    int(math.Floor(d.Flee)),
		Aspd:    int(math.Floor(d.Aspd)),
		CritPct: int(math.Floor(d.CritPct)),
	}
}

// presetActorStats 戰鬥 bootstrap 用（battle.go）：同 presetDerivedDTO 的乘法，但輸出
// scaling.go 既有的 ActorStats 型別（float64，battle.go 自己會再 roundInt 一次組進 wire）。
func presetActorStats(comp CompanionRow, d Derived) ActorStats {
	return ActorStats{
		HPMax: math.Floor(float64(d.MaxHP) * comp.HPMult),
		MPMax: math.Floor(float64(d.MaxMP) * comp.MPMult),
		Atk:   math.Floor(d.Atk * comp.AtkMult),
		Matk:  math.Floor(d.Matk * comp.MatkMult),
		Def:   math.Floor(d.Def * comp.DefMult),
		Mdef:  math.Floor(d.Mdef * comp.MdefMult),
	}
}

// presetCombatRating 戰鬥 bootstrap 用：Hit/Flee/CritPct/CritShield/Aspd/CastReductionPct 沒有
// 對應的傭兵倍率欄位，直接沿用這位傭兵自己 Compute() 的原始評級（比照既有 CompanionRating()
// 對玩家評級「沒有的話沿用」的同一個精神——這裡「沒有的話」永遠成立，因為 rpg_companions
// 從未新增過 hit_mult 這類欄位，見 scaling.go CompanionRating 檔頭註解）。
func presetCombatRating(d Derived) CombatRating {
	return CombatRating{
		Hit: d.Hit, Flee: d.Flee, CritPct: d.CritPct, CritShield: d.CritShield,
		Aspd: d.Aspd, CastReductionPct: d.CastReductionPct, CritDmgPct: d.CritDmgPct,
	}
}

// ---------------------------------------------------------------------------
// PresetDTO（WIRE）
// ---------------------------------------------------------------------------

type PresetDTO struct {
	ID               string         `json:"id"`
	CompanionID      string         `json:"companion_id"`
	Name             string         `json:"name"`
	Level            int            `json:"level"`
	IsSystem         bool           `json:"is_system"`
	Stats            Stats          `json:"stats"`
	SkillLevels      map[string]int `json:"skill_levels"`
	Derived          DerivedDTO     `json:"derived"`
	StatPointsTotal  int            `json:"stat_points_total"`
	StatPointsFree   int            `json:"stat_points_free"`
	StatCap          int            `json:"stat_cap"`
	SkillPointsTotal int            `json:"skill_points_total"`
	SkillPointsFree  int            `json:"skill_points_free"`

	// --- DORPG P9（CONTRACT §2/§3、WIRE）：傭兵裝備比照玩家＋AI 戰鬥策略 ---
	Equipment  equippedWire  `json:"equipment"`   // 八格，同 P8 GET /rpg/equipment 的 equipped 形狀
	EquipBonus EquipBonusDTO `json:"equip_bonus"` // 彙總後的裝備加成（derived 已套用，這裡另外攤平顯示）
	StrategyID string        `json:"strategy_id"`
}

// skillPointsSpentForJob 只加總「屬於這個職業」的技能等級（不屬於的已經是 skill_not_in_job
// 錯誤，不重複計入預算——與 ValidatePreset 內的同一段邏輯保持一致，兩處都要這樣算才不會出現
// 「驗證說超預算」但「DTO 顯示的 skill_points_free 沒超」的不一致）。
func skillPointsSpentForJob(jobSkills []SkillRow, skillLevels map[string]int) int {
	jobByID := make(map[string]bool, len(jobSkills))
	for _, s := range jobSkills {
		jobByID[s.ID] = true
	}
	spent := 0
	for id, lvl := range skillLevels {
		if lvl > 0 && jobByID[id] {
			spent += lvl
		}
	}
	return spent
}

// buildPresetDTO PresetRow → PresetDTO：算好 derived 與四個預算/上限數字。equipWire/equip
// DORPG P9：呼叫端（tavern.go 的清單迴圈、presets.go 的 Create/Update）先用
// h.resolvePresetEquipmentSlots() 查出這份腳本 equipment 八格對應的資料列，再用
// presetEquipmentWire()/presetEquip() 轉成這裡要的兩種形狀——buildPresetDTO 本身維持純函式
// （不碰 DB），沿用既有慣例。
func buildPresetDTO(cfg Config, job JobRow, jobSkills []SkillRow, comp CompanionRow, p PresetRow, equipWire equippedWire, equip *EquipBonus) PresetDTO {
	d := computeCompanionDerived(cfg, job, jobSkills, p.Level, p.Stats, p.SkillLevels, equip)

	statTotal := TotalStatPoints(cfg, p.Level)
	statFree := statTotal - TotalSpentStats(cfg, p.Stats)
	if statFree < 0 {
		statFree = 0
	}
	skillTotal := TotalSkillPoints(cfg, p.Level)
	skillFree := skillTotal - skillPointsSpentForJob(jobSkills, p.SkillLevels)
	if skillFree < 0 {
		skillFree = 0
	}

	skillLevels := p.SkillLevels
	if skillLevels == nil {
		skillLevels = map[string]int{}
	}

	return PresetDTO{
		ID: p.ID, CompanionID: p.CompanionID, Name: p.Name, Level: p.Level, IsSystem: p.IsSystem(),
		Stats: p.Stats, SkillLevels: skillLevels, Derived: presetDerivedDTO(comp, d),
		StatPointsTotal: statTotal, StatPointsFree: statFree, StatCap: StatCap(cfg, p.Level),
		SkillPointsTotal: skillTotal, SkillPointsFree: skillFree,
		Equipment: equipWire, EquipBonus: ToEquipBonusDTO(equipBonusOrZero(equip)), StrategyID: p.StrategyID,
	}
}

// ---------------------------------------------------------------------------
// DB 層：rpg_companion_presets（migration 181）
// ---------------------------------------------------------------------------

// PresetEquipment DORPG P9（CONTRACT §2、WIRE）：一份腳本的八格裝備，值是 item_id
// （武器查 rpg_weapons，其餘七格查 rpg_armor_items）；缺鍵／nil＝那一格沒有配置。json tag 對齊
// WIRE PUT/POST /rpg/presets 的 `equipment` body 形狀，同一個型別直接拿來當 rpg_companion_
// presets.equipment 這欄 JSONB 的 Go 對應（存入/讀出都不必再轉一層）。
type PresetEquipment struct {
	Weapon     *string `json:"weapon,omitempty"`
	Helmet     *string `json:"helmet,omitempty"`
	Gloves     *string `json:"gloves,omitempty"`
	Armor      *string `json:"armor,omitempty"`
	Legs       *string `json:"legs,omitempty"`
	Boots      *string `json:"boots,omitempty"`
	Accessory1 *string `json:"accessory1,omitempty"`
	Accessory2 *string `json:"accessory2,omitempty"`
}

// presetEquipmentItemID 依格子名取出 PresetEquipment 對應欄位的值（nil→空字串＝沒有配置）。
func presetEquipmentItemID(eq PresetEquipment, slot string) string {
	var p *string
	switch slot {
	case "weapon":
		p = eq.Weapon
	case "helmet":
		p = eq.Helmet
	case "gloves":
		p = eq.Gloves
	case "armor":
		p = eq.Armor
	case "legs":
		p = eq.Legs
	case "boots":
		p = eq.Boots
	case "accessory1":
		p = eq.Accessory1
	case "accessory2":
		p = eq.Accessory2
	}
	if p == nil {
		return ""
	}
	return *p
}

// PresetRow rpg_companion_presets 資料列。UserID 為 nil＝系統預設（唯讀，見契約 §3.2）。
type PresetRow struct {
	ID          string
	UserID      *string
	CompanionID string
	Name        string
	Level       int
	Stats       Stats
	SkillLevels map[string]int
	Equipment   PresetEquipment // DORPG P9（CONTRACT §2）
	StrategyID  string          // DORPG P9（CONTRACT §2）：AI 戰鬥策略，見 strategies.go
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// IsSystem 系統預設腳本（user_id IS NULL）——唯讀，不能被 PUT/DELETE（契約 §3.2）。
func (p PresetRow) IsSystem() bool { return p.UserID == nil }

const presetCols = `id::text, user_id::text, companion_id, name, level, stats, skill_levels, equipment, strategy_id, created_at, updated_at`

func scanPreset(row pgx.Row) (PresetRow, error) {
	var p PresetRow
	var statsRaw, skillsRaw, equipRaw []byte
	if err := row.Scan(&p.ID, &p.UserID, &p.CompanionID, &p.Name, &p.Level, &statsRaw, &skillsRaw, &equipRaw, &p.StrategyID, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return p, err
	}
	if len(equipRaw) > 0 {
		if err := json.Unmarshal(equipRaw, &p.Equipment); err != nil {
			p.Equipment = PresetEquipment{} // 壞掉的 JSON 不該讓整支 API 500（比照下面 stats/skill_levels 既有慣例）
		}
	}
	if len(statsRaw) > 0 {
		if err := json.Unmarshal(statsRaw, &p.Stats); err != nil {
			p.Stats = Stats{} // 壞掉的 JSON 不該讓整支 API 500，比照 scanScene/scanSkill 既有慣例
		}
	}
	p.SkillLevels = map[string]int{}
	if len(skillsRaw) > 0 {
		if err := json.Unmarshal(skillsRaw, &p.SkillLevels); err != nil {
			p.SkillLevels = map[string]int{}
		}
	}
	return p, nil
}

// listPresetsForCompanion WIRE：「presets 含系統預設在前、使用者自訂在後」——
// `(user_id IS NULL) DESC` 在 Postgres 裡 TRUE 排在 FALSE 前面，正好對齊這個順序。
func (h *Handler) listPresetsForCompanion(ctx context.Context, userID, companionID string) ([]PresetRow, error) {
	rows, err := h.db.Query(ctx, `
		SELECT `+presetCols+` FROM rpg_companion_presets
		WHERE companion_id=$1 AND (user_id IS NULL OR user_id=$2)
		ORDER BY (user_id IS NULL) DESC, created_at`, companionID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PresetRow{}
	for rows.Next() {
		p, err := scanPreset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (h *Handler) getPreset(ctx context.Context, id string) (PresetRow, error) {
	row := h.db.QueryRow(ctx, `SELECT `+presetCols+` FROM rpg_companion_presets WHERE id=$1`, id)
	return scanPreset(row)
}

// getDefaultSystemPreset 契約 §3.1：「preset_id NULL＝用該傭兵的系統預設腳本」。同一個傭兵理論上
// 只有一筆系統預設（migration 181 seed），用 created_at 排序取第一筆是保守寫法，即使之後手動
// 多塞了一筆系統預設也有明確、穩定的挑選規則。查無資料回 pgx.ErrNoRows（呼叫端保守處理）。
func (h *Handler) getDefaultSystemPreset(ctx context.Context, companionID string) (PresetRow, error) {
	row := h.db.QueryRow(ctx, `
		SELECT `+presetCols+` FROM rpg_companion_presets
		WHERE companion_id=$1 AND user_id IS NULL
		ORDER BY created_at LIMIT 1`, companionID)
	return scanPreset(row)
}

// resolveCompanionPreset 契約 §3.1：presetID 為 nil 時退回該傭兵的系統預設。
func (h *Handler) resolveCompanionPreset(ctx context.Context, companionID string, presetID *string) (PresetRow, error) {
	if presetID != nil {
		return h.getPreset(ctx, *presetID)
	}
	return h.getDefaultSystemPreset(ctx, companionID)
}

func statsToJSON(s Stats) ([]byte, error) { return json.Marshal(s) }
func skillLevelsToJSON(m map[string]int) ([]byte, error) {
	if m == nil {
		m = map[string]int{}
	}
	return json.Marshal(m)
}

// insertPreset 使用者自訂腳本（user_id 一律是呼叫端自己，不可能新增系統預設——契約 §3.2
// 「系統預設腳本唯讀」，新增端點本來就不吃 user_id=NULL 這個選項）。equipment/strategyID
// DORPG P9：呼叫端（CreatePreset）已經跑完 validatePresetEquipmentErrors()/checkStrategyID()
// 才會走到這裡，strategyID 一律是「已標準化」的合法值（空字串已由呼叫端補成
// DefaultStrategyID），不會是使用者原始輸入的空字串（FK 會拒絕空字串）。
func (h *Handler) insertPreset(ctx context.Context, userID, companionID, name string, level int, stats Stats, skillLevels map[string]int, equipment PresetEquipment, strategyID string) (PresetRow, error) {
	statsRaw, err := statsToJSON(stats)
	if err != nil {
		return PresetRow{}, err
	}
	skillsRaw, err := skillLevelsToJSON(skillLevels)
	if err != nil {
		return PresetRow{}, err
	}
	equipRaw, err := json.Marshal(equipment)
	if err != nil {
		return PresetRow{}, err
	}
	row := h.db.QueryRow(ctx, `
		INSERT INTO rpg_companion_presets (user_id, companion_id, name, level, stats, skill_levels, equipment, strategy_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING `+presetCols, userID, companionID, name, level, statsRaw, skillsRaw, equipRaw, strategyID)
	return scanPreset(row)
}

// updatePresetRow 只更新「屬於這個 userID 的」腳本列（WHERE user_id=$8）——系統預設的 user_id
// 是 NULL，`NULL = $8` 在 SQL 裡恆為 UNKNOWN（非 TRUE），天然擋掉誤更新系統預設，呼叫端
// UpdatePreset 仍會先查一次 IsSystem() 提早回 403（見下方 handler），這裡是第二道防線。
// RowsAffected()==0 可能是「查無此列」或「這列不屬於這個使用者」，呼叫端一律視為 404。
func (h *Handler) updatePresetRow(ctx context.Context, id, userID, name string, level int, stats Stats, skillLevels map[string]int, equipment PresetEquipment, strategyID string) (PresetRow, bool, error) {
	statsRaw, err := statsToJSON(stats)
	if err != nil {
		return PresetRow{}, false, err
	}
	skillsRaw, err := skillLevelsToJSON(skillLevels)
	if err != nil {
		return PresetRow{}, false, err
	}
	equipRaw, err := json.Marshal(equipment)
	if err != nil {
		return PresetRow{}, false, err
	}
	ct, err := h.db.Exec(ctx, `
		UPDATE rpg_companion_presets SET name=$1, level=$2, stats=$3, skill_levels=$4, equipment=$5, strategy_id=$6, updated_at=NOW()
		WHERE id=$7 AND user_id=$8`, name, level, statsRaw, skillsRaw, equipRaw, strategyID, id, userID)
	if err != nil {
		return PresetRow{}, false, err
	}
	if ct.RowsAffected() == 0 {
		return PresetRow{}, false, nil
	}
	p, err := h.getPreset(ctx, id)
	return p, true, err
}

// deletePresetRow 同 updatePresetRow：WHERE user_id=$2 天然擋掉刪除系統預設或別人的腳本。
// player_party.preset_id 的 FK 是 ON DELETE SET NULL（migration 181），刪除後該格自動退回
// 「preset_id=NULL＝用系統預設」，不需要這裡另外處理應用層退回邏輯。
func (h *Handler) deletePresetRow(ctx context.Context, id, userID string) (bool, error) {
	ct, err := h.db.Exec(ctx, `DELETE FROM rpg_companion_presets WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// ---------------------------------------------------------------------------
// DORPG P9（CONTRACT §2/§3、WIRE）：傭兵裝備解析——一份腳本的 equipment 八格 item_id →
// rpg_weapons/rpg_armor_items 資料列（best effort，查無資料的格子視為「沒有配置」，不是
// 錯誤，比照 equipment.go getEquippedArmorMap 對玩家裝備的既有慣例），再由下面幾個純函式轉成
// 資格檢查錯誤／Compute() 用的 *EquipBonus／戰鬥 wire 的 equipmentEffects／顯示用 DTO 四種
// 不同形狀——四個呼叫端（presets.go 的 Create/Update/Validate、tavern.go 的清單、battle.go 的
// bootstrap）共用同一次查詢結果，各自只挑需要的形狀，不重複打 DB。
// ---------------------------------------------------------------------------

// presetEquipmentSlotOrder 固定走訪順序（同 statFieldOrder 既有慣例）——驗證/顯示的結果在相同
// 輸入下必須每次順序一致。
var presetEquipmentSlotOrder = []string{"weapon", "helmet", "gloves", "armor", "legs", "boots", "accessory1", "accessory2"}

// resolvedPresetEquipmentSlot 單一格子的查詢結果。ItemID 空字串＝這格沒有配置（NotFound/Weapon/
// Armor 皆為零值，呼叫端不需要另外判斷）；NotFound＝item_id 有值但查無資料（髒資料：物品被後台
// 下架刪除，或腳本等級調降後這件裝備已經 level_too_low——後者不算「查無」，仍會解析出 Weapon/
// Armor，只是 validatePresetEquipmentErrors 會標記 level_too_low）。
type resolvedPresetEquipmentSlot struct {
	Slot       string
	ItemID     string
	Weapon     *WeaponRow
	WeaponType *WeaponTypeRow
	Armor      *ArmorRow
	NotFound   bool
}

// resolvePresetEquipmentSlots 依 eq 的八格 item_id 從 catalog（loadGearCatalog 批次查好的武器/
// 防具表）逐一查出對應資料列，不做任何資格檢查（資格檢查在 validatePresetEquipmentErrors，這裡
// 只負責「查得到什麼」）。2026-09-19 P9 N+1 修復：改自逐格各打一次 getWeaponWithType/
// getArmorByID（GET /rpg/tavern 實測 N 份腳本×8 格造成 13–19 秒），現在純粹查記憶體 map、
// 不再碰 DB；查無資料的格子（item_id 有值但 catalog 沒收到——物品被後台下架刪除，屬於既有
// NotFound 語意）記一行 log 視為未裝備，不是錯誤，因此不再需要回傳 error。
func resolvePresetEquipmentSlots(eq PresetEquipment, weapons map[string]WeaponWithType, armor map[string]ArmorRow) []resolvedPresetEquipmentSlot {
	out := make([]resolvedPresetEquipmentSlot, 0, len(presetEquipmentSlotOrder))
	for _, slot := range presetEquipmentSlotOrder {
		id := presetEquipmentItemID(eq, slot)
		r := resolvedPresetEquipmentSlot{Slot: slot, ItemID: id}
		if id == "" {
			out = append(out, r)
			continue
		}
		if slot == "weapon" {
			if wt, ok := weapons[id]; ok {
				weaponRow, typeRow := wt.Weapon, wt.Type
				r.Weapon, r.WeaponType = &weaponRow, &typeRow
			} else {
				r.NotFound = true
				log.Printf("rpg: preset equipment 查無武器 item_id=%s slot=weapon，視為未裝備", id)
			}
		} else {
			if item, ok := armor[id]; ok {
				r.Armor = &item
			} else {
				r.NotFound = true
				log.Printf("rpg: preset equipment 查無防具/飾品 item_id=%s slot=%s，視為未裝備", id, slot)
			}
		}
		out = append(out, r)
	}
	return out
}

// presetEquipmentIDs 收集一份 equipment 八格裡「非空」的 item_id，依 weapon/armor 分成兩組
// （loadGearCatalog 的輸入）。單一腳本呼叫端（Validate/Create/Update）直接拿這份結果查詢；
// 多份腳本的呼叫端（tavern.go／battle.go）則對每份腳本各呼叫一次、把結果 append 起來，
// 最後統一去重（dedupeNonEmpty）再一次性查詢，讓 N 份腳本仍只打一次武器查詢＋一次防具查詢。
func presetEquipmentIDs(eq PresetEquipment) (weaponIDs, armorIDs []string) {
	for _, slot := range presetEquipmentSlotOrder {
		id := presetEquipmentItemID(eq, slot)
		if id == "" {
			continue
		}
		if slot == "weapon" {
			weaponIDs = append(weaponIDs, id)
		} else {
			armorIDs = append(armorIDs, id)
		}
	}
	return weaponIDs, armorIDs
}

// dedupeNonEmpty 去除空字串與重複值，保留原始出現順序——多份腳本可能共用同一把武器/防具，
// 去重後 loadGearCatalog 的 `WHERE id = ANY($1)` 參數陣列不會被灌爆。
func dedupeNonEmpty(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// loadGearCatalog 依 weaponIDs/armorIDs 各發一次批次查詢（`WHERE id = ANY($1)`），取代
// resolvePresetEquipmentSlots 原本逐格查 DB 的做法——GET /rpg/tavern 對 N 份腳本的 DB 往返
// 因此收斂為常數次（腳本查詢＋武器一次＋防具一次），見上方 resolvePresetEquipmentSlots 檔頭
// 說明。呼叫端負責先用 presetEquipmentIDs／dedupeNonEmpty 收集好 id 清單再呼叫這裡一次。
func (h *Handler) loadGearCatalog(ctx context.Context, weaponIDs, armorIDs []string) (map[string]WeaponWithType, map[string]ArmorRow, error) {
	weapons, err := h.loadWeaponsWithTypesByIDs(ctx, weaponIDs)
	if err != nil {
		return nil, nil, err
	}
	armor, err := h.loadArmorByIDs(ctx, armorIDs)
	if err != nil {
		return nil, nil, err
	}
	return weapons, armor, nil
}

// equipErrMessage validatePresetEquipmentErrors 用的錯誤碼→人類可讀訊息，跟 equipment.go
// canEquipWeapon/canEquipArmor 回傳的裸錯誤碼字面值一一對應（PresetError 需要一句 Message，
// equipment.go 兩支既有端點不需要，各自维持自己的既有慣例，不勉強共用）。
func equipErrMessage(code string) string {
	switch code {
	case "not_found":
		return "查無此裝備"
	case "wrong_slot":
		return "裝備格子不符"
	case "wrong_job":
		return "此裝備不屬於這個職業"
	case "level_too_low":
		return "等級不足"
	case "duplicate_accessory":
		return "兩個飾品格不可裝備同一件"
	}
	return code
}

// validatePresetEquipmentErrors CONTRACT §3「傭兵裝備規則與玩家完全相同」：重用 equipment.go
// 既有的 canEquipWeapon/canEquipArmor 純函式，jobID/level 一律是這位傭兵的職業與腳本等級
// （不是玩家自己的職業／有效等級）。
func validatePresetEquipmentErrors(slots []resolvedPresetEquipmentSlot, jobID string, level int) []PresetError {
	errs := []PresetError{}
	idBySlot := make(map[string]string, len(slots))
	for _, s := range slots {
		idBySlot[s.Slot] = s.ItemID
	}
	for _, s := range slots {
		if s.ItemID == "" {
			continue
		}
		field := "equipment." + s.Slot
		if s.Slot == "weapon" {
			if s.NotFound || s.Weapon == nil || s.WeaponType == nil {
				errs = append(errs, presetErr(field, "not_found", equipErrMessage("not_found")))
				continue
			}
			if code := canEquipWeapon(*s.Weapon, *s.WeaponType, &jobID, level); code != "" {
				errs = append(errs, presetErr(field, code, equipErrMessage(code)))
			}
			continue
		}
		if s.NotFound || s.Armor == nil {
			errs = append(errs, presetErr(field, "not_found", equipErrMessage("not_found")))
			continue
		}
		other := ""
		if s.Slot == "accessory1" {
			other = idBySlot["accessory2"]
		} else if s.Slot == "accessory2" {
			other = idBySlot["accessory1"]
		}
		if code := canEquipArmor(*s.Armor, s.Slot, &jobID, level, other); code != "" {
			errs = append(errs, presetErr(field, code, equipErrMessage(code)))
		}
	}
	return errs
}

// presetEquip Compute() 要用的 *EquipBonus；完全沒有任何有效裝備時回 nil——沿用
// PlayerEquipmentSnapshot.Equip() 同一個既有慣例（CONTRACT §3「equipment 空時輸出逐位元同
// P6」）：Compute() 對 eq==nil 與 eq=&EquipBonus{}（零值）兩種輸入的計算路徑不同（後者會多套
// 一次 math.Floor，見 compute.go），零值指標不是安全的「沒有裝備」表示法，見該函式檔頭註解。
// 資格是否通過（validatePresetEquipmentErrors）不影響這裡的彙總——查得到的裝備一律計入，跟
// equipment.go loadPlayerEquipment/PlayerEquipmentSnapshot.Equip() 對玩家裝備的既有慣例一致
// （裝備效果何時生效是「存檔時擋不合法裝備」把關，不是每次讀取都重新資格複查）。
func presetEquip(slots []resolvedPresetEquipmentSlot) *EquipBonus {
	var weaponProfile *WeaponProfile
	var armorProfiles []ArmorProfile
	for _, s := range slots {
		if s.ItemID == "" || s.NotFound {
			continue
		}
		if s.Slot == "weapon" {
			if s.Weapon != nil {
				p := s.Weapon.Profile
				weaponProfile = &p
			}
			continue
		}
		if s.Armor != nil {
			armorProfiles = append(armorProfiles, s.Armor.Profile)
		}
	}
	if weaponProfile == nil && len(armorProfiles) == 0 {
		return nil
	}
	b := AggregateEquipment(weaponProfile, armorProfiles)
	return &b
}

// presetEquipmentEffects battle.go 戰鬥 wire party member「equipmentEffects」用（WIRE：「只彙總
// 防具與飾品；武器仍走 weapon.profile」）——同 PlayerEquipmentSnapshot.EquipmentEffects() 對
// 玩家的既有慣例，這裡回傳值型別（非指標），零裝備時自然是全零 EquipBonus，跟前端「這個欄位
// 不必額外判斷存不存在」的既有設計一致（見 battle.go wireEquipmentEffects 檔頭註解）。
func presetEquipmentEffects(slots []resolvedPresetEquipmentSlot) EquipBonus {
	var armorProfiles []ArmorProfile
	for _, s := range slots {
		if s.Slot == "weapon" || s.ItemID == "" || s.NotFound || s.Armor == nil {
			continue
		}
		armorProfiles = append(armorProfiles, s.Armor.Profile)
	}
	return AggregateEquipment(nil, armorProfiles)
}

// presetWeaponRowAndType battle.go 用：取出 weapon 格子解析出的 WeaponRow/WeaponTypeRow
// （皆為 nil＝沒有裝備武器，或裝到查無資料的武器）。
func presetWeaponRowAndType(slots []resolvedPresetEquipmentSlot) (*WeaponRow, *WeaponTypeRow) {
	for _, s := range slots {
		if s.Slot == "weapon" {
			if s.ItemID == "" || s.NotFound {
				return nil, nil
			}
			return s.Weapon, s.WeaponType
		}
	}
	return nil, nil
}

// presetEquipmentWire PresetDTO.equipment（WIRE：同 P8 GET /rpg/equipment 的 equipped 形狀）—
// effLevel 一律是「腳本等級」，can_equip 依這個等級算（不是玩家的有效等級）。equipped/
// equipped_in 恆為 true/該格子名（這是這份腳本自己的裝備配置，不是「跟誰比對」的動態欄位）。
func presetEquipmentWire(slots []resolvedPresetEquipmentSlot, effLevel int) equippedWire {
	var out equippedWire
	for _, s := range slots {
		if s.ItemID == "" || s.NotFound {
			continue
		}
		if s.Slot == "weapon" {
			if s.Weapon != nil {
				dto := toWeaponDTO(*s.Weapon, effLevel, s.Weapon.ID)
				out.Weapon = &dto
			}
			continue
		}
		if s.Armor == nil {
			continue
		}
		slotName := s.Slot
		dto := toArmorDTO(*s.Armor, effLevel, &slotName)
		switch s.Slot {
		case "helmet":
			out.Helmet = &dto
		case "gloves":
			out.Gloves = &dto
		case "armor":
			out.Armor = &dto
		case "legs":
			out.Legs = &dto
		case "boots":
			out.Boots = &dto
		case "accessory1":
			out.Accessory1 = &dto
		case "accessory2":
			out.Accessory2 = &dto
		}
	}
	return out
}

// equipBonusOrZero PresetDTO/validate 回應的 equip_bonus：presetEquip() 回 nil（完全沒有裝備）
// 時顯示全零 DTO，而不是讓呼叫端另外判斷 nil。
func equipBonusOrZero(b *EquipBonus) EquipBonus {
	if b == nil {
		return EquipBonus{}
	}
	return *b
}

// clampLevel1to99 腳本等級的合法範圍（CONTRACT §3.2「等級必須介於 1~99」），計算裝備/衍生值
// 預覽時即使 body.Level 本身不合法（已經由 ValidatePreset 標記 level_range 錯誤）也要有個夾住
// 的數字才能繼續往下算，比照 PresetsValidate 既有的 clamp 寫法抽成共用函式。
func clampLevel1to99(level int) int {
	if level < 1 {
		return 1
	}
	if level > 99 {
		return 99
	}
	return level
}

// ---------------------------------------------------------------------------
// HTTP 端點
// ---------------------------------------------------------------------------

// errCompanionHasNoJob sentinel——comp.JobID 為 nil 時（理論上不會發生：小井以外的四位傭兵
// migration 181 都已補上 job_id；小井本人 is_player_portrait=TRUE，不會被 listPartyCompanions
// 選出來、也不該有人拿它的 id 呼叫這幾支端點）。respondPresetLoadError 用 errors.Is 認得這個
// sentinel，回 400（呼叫端輸入問題）而不是預設的 500（系統錯誤），跟 pgx.ErrNoRows 同一組待遇。
var errCompanionHasNoJob = errors.New("companion 沒有職業，不能建立腳本")

// jobAndSkillsForCompanion 依傭兵目前的 job_id 查出職業與該職業的技能樹——presets.go/tavern.go
// 每個端點都要這兩樣東西才能驗證/展開腳本，抽成共用函式。
func (h *Handler) jobAndSkillsForCompanion(ctx context.Context, comp CompanionRow) (JobRow, []SkillRow, error) {
	if comp.JobID == nil {
		return JobRow{}, nil, errCompanionHasNoJob
	}
	job, err := h.getJobByID(ctx, *comp.JobID)
	if err != nil {
		return JobRow{}, nil, err
	}
	jobSkills, err := h.listSkillsByJob(ctx, *comp.JobID)
	if err != nil {
		return JobRow{}, nil, err
	}
	return job, jobSkills, nil
}

// presetRequestBody POST/PUT /rpg/presets 與 POST /rpg/presets/validate 共用的 body 形狀
// （WIRE：後者沒有 name，前兩者有；用同一個結構體，name 空字串時 CreatePreset/UpdatePreset
// 另外擋 name_required，Validate 端點完全不看 name）。
type presetRequestBody struct {
	CompanionID string         `json:"companion_id"`
	Name        string         `json:"name"`
	Level       int            `json:"level"`
	Stats       Stats          `json:"stats"`
	SkillLevels map[string]int `json:"skill_levels"`

	// --- DORPG P9（CONTRACT §2/§3、WIRE）：傭兵裝備比照玩家＋AI 戰鬥策略 ---
	Equipment  PresetEquipment `json:"equipment"`
	StrategyID string          `json:"strategy_id"`
}

// loadCompanionAndJob 共用尾段：decode body → 查傭兵 → 查職業/技能樹。缺表/查無資料的錯誤處理
// 統一在這裡，四支端點（Create/Update/Delete 不需要、Validate 需要）呼叫同一份。
func (h *Handler) loadCompanionAndJob(ctx context.Context, companionID string) (CompanionRow, JobRow, []SkillRow, error) {
	comp, err := h.getCompanionByID(ctx, companionID)
	if err != nil {
		return CompanionRow{}, JobRow{}, nil, err
	}
	job, jobSkills, err := h.jobAndSkillsForCompanion(ctx, comp)
	return comp, job, jobSkills, err
}

// POST /rpg/presets/validate {companion_id, level, stats, skill_levels} → 草稿 → 預算/衍生值/
// 錯誤，不存檔（契約 §3.3）。
func (h *Handler) PresetsValidate(w http.ResponseWriter, r *http.Request) {
	var body presetRequestBody
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
	comp, job, jobSkills, err := h.loadCompanionAndJob(ctx, body.CompanionID)
	if err != nil {
		if respondPresetLoadError(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load companion")
		return
	}

	errs := ValidatePreset(cfg, jobSkills, body.Level, body.Stats, body.SkillLevels)
	lv := clampLevel1to99(body.Level)

	// DORPG P9（CONTRACT §3、WIRE）：equipment.<slot> 五種錯誤碼＋strategy_id unknown_strategy，
	// 併入同一份 errors[]；equip_bonus／derived 一律含裝備（即使有錯誤——查得到的裝備照樣計入
	// 彙總，見 presetEquip 檔頭註解，讓草稿在還沒完全合法時也能看到目前配置的效果預覽）。
	weaponIDs, armorIDs := presetEquipmentIDs(body.Equipment)
	weapons, armor, err := h.loadGearCatalog(ctx, weaponIDs, armorIDs)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load equipment")
		return
	}
	slots := resolvePresetEquipmentSlots(body.Equipment, weapons, armor)
	errs = append(errs, validatePresetEquipmentErrors(slots, job.ID, lv)...)

	strategyID := body.StrategyID
	if strategyID == "" {
		strategyID = DefaultStrategyID
	}
	strategyErr, err := h.checkStrategyID(ctx, strategyID)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errStrategiesNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load strategy")
		return
	}
	if strategyErr != nil {
		errs = append(errs, *strategyErr)
	}

	equip := presetEquip(slots)
	d := computeCompanionDerived(cfg, job, jobSkills, lv, body.Stats, body.SkillLevels, equip)
	statTotal := TotalStatPoints(cfg, lv)
	statFree := statTotal - TotalSpentStats(cfg, body.Stats)
	if statFree < 0 {
		statFree = 0
	}
	skillTotal := TotalSkillPoints(cfg, lv)
	skillFree := skillTotal - skillPointsSpentForJob(jobSkills, body.SkillLevels)
	if skillFree < 0 {
		skillFree = 0
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"ok":                 len(errs) == 0,
		"errors":             errs,
		"stat_points_total":  statTotal,
		"stat_points_free":   statFree,
		"stat_cap":           StatCap(cfg, lv),
		"skill_points_total": skillTotal,
		"skill_points_free":  skillFree,
		"derived":            presetDerivedDTO(comp, d),
		"skills":             skillDTOsFromLevels(jobSkills, body.SkillLevels, skillFree),
		"equip_bonus":        ToEquipBonusDTO(equipBonusOrZero(equip)),
	})
}

// respondPresetLoadError companion 查無資料或沒有職業時統一回 400（呼叫端輸入問題，不是系統
// 錯誤）；缺表（migration 181 未套用時 rpg_companion_presets 不存在，但這裡查的是既有的
// rpg_companions/rpg_jobs，理論上不會缺表——保留檢查純粹是跟其餘端點一致的防禦寫法）。
func respondPresetLoadError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusBadRequest, "companion not found")
		return true
	}
	if errors.Is(err, errCompanionHasNoJob) {
		respondErr(w, http.StatusBadRequest, "companion has no job")
		return true
	}
	if isMissingRelation(err) {
		respondErr(w, http.StatusServiceUnavailable, errJobsNotReady)
		return true
	}
	return false
}

// POST /rpg/presets
func (h *Handler) CreatePreset(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var body presetRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Name == "" {
		respondJSON(w, http.StatusBadRequest, map[string]any{"errors": []PresetError{presetErr("name", "name_required", "名稱不可為空")}})
		return
	}
	ctx := r.Context()
	cfg, err := h.loadConfig(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	comp, job, jobSkills, err := h.loadCompanionAndJob(ctx, body.CompanionID)
	if err != nil {
		if respondPresetLoadError(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load companion")
		return
	}
	errs := ValidatePreset(cfg, jobSkills, body.Level, body.Stats, body.SkillLevels)
	lv := clampLevel1to99(body.Level)

	// DORPG P9（CONTRACT §3、WIRE）：equipment.<slot> 五種錯誤碼＋strategy_id unknown_strategy，
	// 併入同一份 errs——任何一項不合法就整份 400，跟既有配點/技能點驗證同一套「存檔時整份驗證」
	// 慣例（presets.go 檔頭）。
	weaponIDs, armorIDs := presetEquipmentIDs(body.Equipment)
	weapons, armor, err := h.loadGearCatalog(ctx, weaponIDs, armorIDs)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load equipment")
		return
	}
	slots := resolvePresetEquipmentSlots(body.Equipment, weapons, armor)
	errs = append(errs, validatePresetEquipmentErrors(slots, job.ID, lv)...)

	strategyID := body.StrategyID
	if strategyID == "" {
		strategyID = DefaultStrategyID
	}
	strategyErr, err := h.checkStrategyID(ctx, strategyID)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errStrategiesNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load strategy")
		return
	}
	if strategyErr != nil {
		errs = append(errs, *strategyErr)
	}

	if len(errs) > 0 {
		respondJSON(w, http.StatusBadRequest, map[string]any{"errors": errs})
		return
	}
	created, err := h.insertPreset(ctx, uid, body.CompanionID, body.Name, body.Level, body.Stats, body.SkillLevels, body.Equipment, strategyID)
	if err != nil {
		if isMissingRelation(err) {
			respondErr(w, http.StatusServiceUnavailable, errJobsNotReady)
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	respondJSON(w, http.StatusCreated, buildPresetDTO(cfg, job, jobSkills, comp, created, presetEquipmentWire(slots, lv), presetEquip(slots)))
}

// PUT /rpg/presets/{id}：companion_id 不可變更（腳本天生屬於某個傭兵，換傭兵請另存新腳本）。
func (h *Handler) UpdatePreset(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	id := chi.URLParam(r, "id")
	var body presetRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Name == "" {
		respondJSON(w, http.StatusBadRequest, map[string]any{"errors": []PresetError{presetErr("name", "name_required", "名稱不可為空")}})
		return
	}
	ctx := r.Context()
	existing, err := h.getPreset(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		respondErr(w, http.StatusNotFound, "preset not found")
		return
	case err != nil:
		if isMissingRelation(err) {
			respondErr(w, http.StatusServiceUnavailable, errJobsNotReady)
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load preset")
		return
	}
	if existing.IsSystem() {
		respondErr(w, http.StatusForbidden, "preset_readonly")
		return
	}
	if existing.UserID == nil || *existing.UserID != uid {
		// 不屬於這個使用者：回 404 而不是 403，避免洩漏「這個 id 存在、只是不是你的」。
		respondErr(w, http.StatusNotFound, "preset not found")
		return
	}
	if body.CompanionID != "" && body.CompanionID != existing.CompanionID {
		respondErr(w, http.StatusBadRequest, "companion_id 不可變更")
		return
	}

	cfg, err := h.loadConfig(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	comp, job, jobSkills, err := h.loadCompanionAndJob(ctx, existing.CompanionID)
	if err != nil {
		if respondPresetLoadError(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load companion")
		return
	}
	errs := ValidatePreset(cfg, jobSkills, body.Level, body.Stats, body.SkillLevels)
	lv := clampLevel1to99(body.Level)

	// DORPG P9（CONTRACT §3、WIRE）：equipment.<slot> 五種錯誤碼＋strategy_id unknown_strategy，
	// 併入同一份 errs，理由同 CreatePreset。
	weaponIDs, armorIDs := presetEquipmentIDs(body.Equipment)
	weapons, armor, err := h.loadGearCatalog(ctx, weaponIDs, armorIDs)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load equipment")
		return
	}
	slots := resolvePresetEquipmentSlots(body.Equipment, weapons, armor)
	errs = append(errs, validatePresetEquipmentErrors(slots, job.ID, lv)...)

	strategyID := body.StrategyID
	if strategyID == "" {
		strategyID = DefaultStrategyID
	}
	strategyErr, err := h.checkStrategyID(ctx, strategyID)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errStrategiesNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load strategy")
		return
	}
	if strategyErr != nil {
		errs = append(errs, *strategyErr)
	}

	if len(errs) > 0 {
		respondJSON(w, http.StatusBadRequest, map[string]any{"errors": errs})
		return
	}
	updated, ok, err := h.updatePresetRow(ctx, id, uid, body.Name, body.Level, body.Stats, body.SkillLevels, body.Equipment, strategyID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	if !ok {
		respondErr(w, http.StatusNotFound, "preset not found")
		return
	}
	respondJSON(w, http.StatusOK, buildPresetDTO(cfg, job, jobSkills, comp, updated, presetEquipmentWire(slots, lv), presetEquip(slots)))
}

// DELETE /rpg/presets/{id}
func (h *Handler) DeletePreset(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	existing, err := h.getPreset(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		respondErr(w, http.StatusNotFound, "preset not found")
		return
	case err != nil:
		if isMissingRelation(err) {
			respondErr(w, http.StatusServiceUnavailable, errJobsNotReady)
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load preset")
		return
	}
	if existing.IsSystem() {
		respondErr(w, http.StatusForbidden, "preset_readonly")
		return
	}
	if existing.UserID == nil || *existing.UserID != uid {
		respondErr(w, http.StatusNotFound, "preset not found")
		return
	}
	ok, err := h.deletePresetRow(ctx, id, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to delete")
		return
	}
	if !ok {
		respondErr(w, http.StatusNotFound, "preset not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}
