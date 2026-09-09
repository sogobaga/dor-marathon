// Package rpg 遊戲化系統第一輪：玩家角色數值（參考 RO 仙境傳說 stat/attr 素質系統，
// https://ro.ntome.com/stat/attr 由站長截圖提供對照表）。本檔為所有係數的「設定檔」——
// 每一條 RO 素質規則都對應一個可由後台調整的欄位，DefaultConfig() 是把 owner 提供的對照表
// 原封不動編碼成程式預設值；後台可整包覆寫（見 appsettings key rpg_config，JSON 存放）。
//
// ⚠️ 欄位名稱（json tag）已與前端 apps/web/src/lib/api.ts 的 RpgConfig type 對齊，勿自行改名
// ——前端「參數設定」分頁（apps/web/src/lib/rpgMeta.ts CONFIG_GROUPS）逐欄輸入框直接對應這裡
// 的 json tag，改名會讓後台頁面欄位全部失聯。
//
// 大部分「每幾點 +1」的規則拆成兩種欄位：
//   - 每 1 點的直接係數（例：str_melee_atk、agi_flee、int_matk）：value = stat * coef。
//   - 每 N 點才 +1 的門檻（例：agi_def_per=5、luk_atk_per=3）：grant 固定為 1，只有 N 可調，
//     value = floor(stat / N) * 1；因此這類欄位語意是「除數」而非「每點係數」。
//
// 現階段沒有裝備系統：EquipAtk/EquipMatk 恆為 0（Compute 直接吃 0，這裡只保留「每點裝備攻擊力
// +0.5%」的乘數係數，供之後接裝備時直接套用，不必再動 Config 形狀）。
package rpg

import (
	"encoding/json"
	"fmt"
)

// intMPRegenThreshold INT 決定 MP 自然恢復量的門檻值（RO 對照表固定為 120，前端未把這個門檻
// 本身開放成設定欄位——只有門檻前的除數 IntMPRegenPer、門檻獎勵 IntMPRegenAt120、門檻後的除數
// IntMPRegenPerAfter120 可調），因此在程式內寫死。
const intMPRegenThreshold = 120

// Config 遊戲化數值參數集合。全部欄位皆可由後台 /admin/rpg/config 覆寫（JSON 整包 PUT）。
type Config struct {
	// --- 初始值與加點規則 ---
	InitialStat       int    `json:"initial_stat"`        // 六圍初始值（RO 預設 1）
	InitialFreePoints int    `json:"initial_free_points"` // 初始可配點數（owner 指定 40）
	MaxStat           int    `json:"max_stat"`            // 單一素質上限
	CostBase          int    `json:"cost_base"`           // 加點成本公式基底：cost(n)=floor((n-1)/step)+base
	CostStepEvery     int    `json:"cost_step_every"`     // 加點成本公式的級距（每幾點成本+1）
	DefaultWeaponType string `json:"default_weapon_type"` // "melee" | "ranged"（現階段沒有裝備欄位，用系統設定決定素質物攻走哪個分支）

	// --- HP / MP ---
	BaseHP         float64 `json:"base_hp"`           // HP 基礎值（Base Lv=0 時）
	HPPerBaseLevel float64 `json:"hp_per_base_level"` // 每 Base Lv 增加的 HP（乘 VIT 加成前）
	BaseMP         float64 `json:"base_mp"`           // MP 基礎值
	MPPerBaseLevel float64 `json:"mp_per_base_level"` // 每 Base Lv 增加的 MP（乘 INT 加成前）

	// --- 攻速 Aspd（0~aspd_cap，RO 上限 193） / 負重 ---
	AspdBase     float64 `json:"aspd_base"`      // 攻速基礎值
	AspdPerAgi   float64 `json:"aspd_per_agi"`   // 每 1 點 AGI 增加的攻速
	AspdPerDex   float64 `json:"aspd_per_dex"`   // 每 1 點 DEX 增加的攻速（"稍為增加"，係數小於 AGI）
	AspdCap      float64 `json:"aspd_cap"`       // 攻速上限
	WeightBase   float64 `json:"weight_base"`    // 基礎負重量（STR 加成前）
	WeightPerStr float64 `json:"weight_per_str"` // 每 1 點 STR 增加的負重量

	// --- STR：近戰武器每1點素質物攻+1、裝備物攻+0.5%；遠程武器每N點素質物攻+1 ---
	StrMeleeAtk     float64 `json:"str_melee_atk"`      // 每 1 點：近戰素質物攻
	StrEquipAtkPct  float64 `json:"str_equip_atk_pct"`  // 每 1 點：裝備物攻 %（乘在 EquipAtk 上，現階段 EquipAtk=0 故無感）
	StrRangedAtkPer float64 `json:"str_ranged_atk_per"` // 每幾點：遠程素質物攻+1（除數，預設5）

	// --- AGI：每1點迴避+1、攻速加成(見上)、狀態抗性([出血][睡眠][著火])；每N點物防+1 ---
	AgiFlee   float64 `json:"agi_flee"`    // 每 1 點：迴避率
	AgiDefPer float64 `json:"agi_def_per"` // 每幾點：物理防禦力+1（除數，預設5）

	// --- VIT：每1點最大HP+1%、道具HP恢復+2%、狀態抗性([暈眩][中毒])；每N點物防/魔防/HP自然恢復+1；
	//     每 hp_regen_per_max_hp 點最大HP：HP自然恢復+1 ---
	VitHPPct        float64 `json:"vit_hp_pct"`          // 每 1 點：最大HP %
	VitItemHPPct    float64 `json:"vit_item_hp_pct"`     // 每 1 點：道具HP恢復量 %
	VitDefPer       float64 `json:"vit_def_per"`         // 每幾點：物理防禦力+1（除數，預設2）
	VitMdefPer      float64 `json:"vit_mdef_per"`        // 每幾點：魔法防禦力+1（除數，預設5）
	VitHPRegenPer   float64 `json:"vit_hp_regen_per"`    // 每幾點：HP自然恢復量+1（除數，預設5）
	HPRegenPerMaxHP float64 `json:"hp_regen_per_max_hp"` // 每多少最大HP：HP自然恢復量+1（除數，預設200）

	// --- DEX：每1點遠程素質物攻+1、裝備物攻+0.5%、命中+1、變動詠唱縮減、攻速稍增；
	//     每N點近戰素質物攻/魔攻/魔防+1 ---
	DexRangedAtk   float64 `json:"dex_ranged_atk"`    // 每 1 點：遠程素質物攻
	DexEquipAtkPct float64 `json:"dex_equip_atk_pct"` // 每 1 點：裝備物攻 %
	DexHit         float64 `json:"dex_hit"`           // 每 1 點：命中率
	DexMeleeAtkPer float64 `json:"dex_melee_atk_per"` // 每幾點：近戰素質物攻+1（除數，預設5）
	DexMatkPer     float64 `json:"dex_matk_per"`      // 每幾點：魔法攻擊力+1（除數，預設5）
	DexMdefPer     float64 `json:"dex_mdef_per"`      // 每幾點：魔法防禦力+1（除數，預設5）

	// --- INT：每1點魔攻+1.5、魔防+1、最大MP+1%、道具MP恢復+1%、變動詠唱縮減(DEX一半)、
	//     狀態抗性([黑暗][沉默][恐怖])；達 intMPRegenThreshold(120) 前每N點MP自然恢復+1，
	//     達門檻額外+X，門檻後每M點再+1；每多少最大MP：MP自然恢復+1 ---
	IntMatk               float64 `json:"int_matk"`                   // 每 1 點：魔法攻擊力（1.5）
	IntMdef               float64 `json:"int_mdef"`                   // 每 1 點：魔法防禦力
	IntMPPct              float64 `json:"int_mp_pct"`                 // 每 1 點：最大MP %
	IntItemMPPct          float64 `json:"int_item_mp_pct"`            // 每 1 點：道具MP恢復量 %
	IntMPRegenPer         float64 `json:"int_mp_regen_per"`           // 門檻前每幾點：MP自然恢復+1（除數，預設6）
	IntMPRegenAt120       float64 `json:"int_mp_regen_at_120"`        // 達到門檻(120)時：MP自然恢復額外值（預設4）
	IntMPRegenPerAfter120 float64 `json:"int_mp_regen_per_after_120"` // 門檻後每幾點：MP自然恢復+1（除數，預設2）
	MPRegenPerMaxMP       float64 `json:"mp_regen_per_max_mp"`        // 每多少最大MP：MP自然恢復+1（除數，預設100）

	// --- LUK：每1點暴擊率+0.3、狀態抗性([詛咒][混亂][恐怖][著火])；每N點物攻/魔攻/命中/迴避/
	//     暴擊迴避/完全迴避+1 ---
	LukCrit            float64 `json:"luk_crit"`              // 每 1 點：暴擊率（0.3）
	LukAtkPer          float64 `json:"luk_atk_per"`           // 每幾點：物理攻擊力+1（除數，預設3）
	LukMatkPer         float64 `json:"luk_matk_per"`          // 每幾點：魔法攻擊力+1（除數，預設3）
	LukHitPer          float64 `json:"luk_hit_per"`           // 每幾點：命中率+1（除數，預設3）
	LukFleePer         float64 `json:"luk_flee_per"`          // 每幾點：迴避率+1（除數，預設5）
	LukCritShieldPer   float64 `json:"luk_crit_shield_per"`   // 每幾點：暴擊迴避率+1（除數，預設5）
	LukPerfectDodgePer float64 `json:"luk_perfect_dodge_per"` // 每幾點：完全迴避+1（除數，預設10）

	// --- 基本等級（Base Level，取自 users.exp 換算的既有 DOR 等級）：
	//     每1級命中+1、迴避+1；每N級物防/物攻/魔攻/魔防+1 ---
	LvHit     float64 `json:"lv_hit"`      // 每 1 級：命中率
	LvFlee    float64 `json:"lv_flee"`     // 每 1 級：迴避率
	LvDefPer  float64 `json:"lv_def_per"`  // 每幾級：物理防禦力+1（除數，預設2）
	LvAtkPer  float64 `json:"lv_atk_per"`  // 每幾級：物理攻擊力+1（除數，預設4）
	LvMatkPer float64 `json:"lv_matk_per"` // 每幾級：魔法攻擊力+1（除數，預設4）
	LvMdefPer float64 `json:"lv_mdef_per"` // 每幾級：魔法防禦力+1（除數，預設4）

	// --- 變動詠唱時間縮減：DEX 1點=cast_unit_dex單位，INT 1點=cast_unit_int單位，
	//     每單位縮減 cast_pct_per_unit%，總和封頂 cast_cap_pct% ---
	CastUnitDex    float64 `json:"cast_unit_dex"`     // DEX 每點對應的「單位」數（預設1）
	CastUnitInt    float64 `json:"cast_unit_int"`     // INT 每點對應的「單位」數（預設0.5＝DEX一半）
	CastPctPerUnit float64 `json:"cast_pct_per_unit"` // 每單位縮減百分比（預設0.5）
	CastCapPct     float64 `json:"cast_cap_pct"`      // 縮減上限百分比（預設50）

	// --- 狀態抗性（AGI/VIT/INT/LUK 共用同一係數，見 compute.go 各狀態對應表）／迴避率上限 ---
	ResistPctPerPoint float64 `json:"resist_pct_per_point"` // 每點狀態抗性 %（預設0.1）
	FleeCapPct        float64 `json:"flee_cap_pct"`         // 迴避率上限（完全迴避不受此限，預設95）
}

// DefaultConfig 站長截圖對照表原封不動編碼成的預設值（所有數字皆可由後台覆寫）。
func DefaultConfig() Config {
	return Config{
		InitialStat:       1,
		InitialFreePoints: 40,
		MaxStat:           99,
		CostBase:          2,
		CostStepEvery:     10,
		DefaultWeaponType: "melee",

		BaseHP:         100,
		HPPerBaseLevel: 30,
		BaseMP:         50,
		MPPerBaseLevel: 5,

		AspdBase:     150,
		AspdPerAgi:   0.25,
		AspdPerDex:   0.1,
		AspdCap:      193,
		WeightBase:   2000,
		WeightPerStr: 30,

		StrMeleeAtk:     1,
		StrEquipAtkPct:  0.5,
		StrRangedAtkPer: 5,

		AgiFlee:   1,
		AgiDefPer: 5,

		VitHPPct:        1,
		VitItemHPPct:    2,
		VitDefPer:       2,
		VitMdefPer:      5,
		VitHPRegenPer:   5,
		HPRegenPerMaxHP: 200,

		DexRangedAtk:   1,
		DexEquipAtkPct: 0.5,
		DexHit:         1,
		DexMeleeAtkPer: 5,
		DexMatkPer:     5,
		DexMdefPer:     5,

		IntMatk:               1.5,
		IntMdef:               1,
		IntMPPct:              1,
		IntItemMPPct:          1,
		IntMPRegenPer:         6,
		IntMPRegenAt120:       4,
		IntMPRegenPerAfter120: 2,
		MPRegenPerMaxMP:       100,

		LukCrit:            0.3,
		LukAtkPer:          3,
		LukMatkPer:         3,
		LukHitPer:          3,
		LukFleePer:         5,
		LukCritShieldPer:   5,
		LukPerfectDodgePer: 10,

		LvHit:     1,
		LvFlee:    1,
		LvDefPer:  2,
		LvAtkPer:  4,
		LvMatkPer: 4,
		LvMdefPer: 4,

		CastUnitDex:    1,
		CastUnitInt:    0.5,
		CastPctPerUnit: 0.5,
		CastCapPct:     50,

		ResistPctPerPoint: 0.1,
		FleeCapPct:        95,
	}
}

// requirePositive 收集「必須 > 0」的除數/上限欄位檢查（這些欄位當分母用，0 或負值會讓 Compute
// 除零/算出負值可利用漏洞）。
func requirePositive(errs *[]string, name string, v float64) {
	if v <= 0 {
		*errs = append(*errs, name)
	}
}

// Validate 檢查參數是否落在合理範圍（後台 PUT /admin/rpg/config 前置檢查）。刻意只擋「會讓
// 系統壞掉/算出負值可利用漏洞」的邊界，不擋設計上合理的極端值（例如某係數想暫時設 0 停用）。
func (c Config) Validate() error {
	if c.InitialStat < 0 {
		return fmt.Errorf("initial_stat must be >= 0")
	}
	if c.InitialFreePoints < 0 {
		return fmt.Errorf("initial_free_points must be >= 0")
	}
	if c.MaxStat <= c.InitialStat {
		return fmt.Errorf("max_stat must be > initial_stat")
	}
	if c.CostBase <= 0 {
		return fmt.Errorf("cost_base must be > 0")
	}
	if c.CostStepEvery <= 0 {
		return fmt.Errorf("cost_step_every must be > 0")
	}
	if c.DefaultWeaponType != "melee" && c.DefaultWeaponType != "ranged" {
		return fmt.Errorf("default_weapon_type must be melee or ranged")
	}
	if c.BaseHP < 0 || c.BaseMP < 0 || c.HPPerBaseLevel < 0 || c.MPPerBaseLevel < 0 {
		return fmt.Errorf("base_hp/base_mp/hp_per_base_level/mp_per_base_level must be >= 0")
	}
	if c.AspdCap <= 0 || c.AspdCap > 1000 {
		return fmt.Errorf("aspd_cap must be within (0,1000]")
	}
	if c.WeightBase < 0 {
		return fmt.Errorf("weight_base must be >= 0")
	}
	if c.FleeCapPct < 0 || c.FleeCapPct > 100 {
		return fmt.Errorf("flee_cap_pct must be within [0,100]")
	}
	if c.CastCapPct < 0 || c.CastCapPct > 100 {
		return fmt.Errorf("cast_cap_pct must be within [0,100]")
	}
	var bad []string
	requirePositive(&bad, "str_ranged_atk_per", c.StrRangedAtkPer)
	requirePositive(&bad, "agi_def_per", c.AgiDefPer)
	requirePositive(&bad, "vit_def_per", c.VitDefPer)
	requirePositive(&bad, "vit_mdef_per", c.VitMdefPer)
	requirePositive(&bad, "vit_hp_regen_per", c.VitHPRegenPer)
	requirePositive(&bad, "hp_regen_per_max_hp", c.HPRegenPerMaxHP)
	requirePositive(&bad, "dex_melee_atk_per", c.DexMeleeAtkPer)
	requirePositive(&bad, "dex_matk_per", c.DexMatkPer)
	requirePositive(&bad, "dex_mdef_per", c.DexMdefPer)
	requirePositive(&bad, "int_mp_regen_per", c.IntMPRegenPer)
	requirePositive(&bad, "int_mp_regen_per_after_120", c.IntMPRegenPerAfter120)
	requirePositive(&bad, "mp_regen_per_max_mp", c.MPRegenPerMaxMP)
	requirePositive(&bad, "luk_atk_per", c.LukAtkPer)
	requirePositive(&bad, "luk_matk_per", c.LukMatkPer)
	requirePositive(&bad, "luk_hit_per", c.LukHitPer)
	requirePositive(&bad, "luk_flee_per", c.LukFleePer)
	requirePositive(&bad, "luk_crit_shield_per", c.LukCritShieldPer)
	requirePositive(&bad, "luk_perfect_dodge_per", c.LukPerfectDodgePer)
	requirePositive(&bad, "lv_def_per", c.LvDefPer)
	requirePositive(&bad, "lv_atk_per", c.LvAtkPer)
	requirePositive(&bad, "lv_matk_per", c.LvMatkPer)
	requirePositive(&bad, "lv_mdef_per", c.LvMdefPer)
	if len(bad) > 0 {
		return fmt.Errorf("these fields must be > 0 (used as divisors): %v", bad)
	}
	return nil
}

// ParseConfig 解析後台 JSON 設定（appsettings key "rpg_config"）；空字串回預設值。未出現在 JSON
// 裡的欄位維持 DefaultConfig() 的值（先套預設值再 Unmarshal 覆蓋，避免後台只想改一兩個係數時
// 其餘欄位被歸零）。
func ParseConfig(raw string) (Config, error) {
	cfg := DefaultConfig()
	if raw == "" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return Config{}, fmt.Errorf("invalid rpg_config json: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
