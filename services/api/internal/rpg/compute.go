// compute.go：純函式——六圍 + Base/Job Lv → 衍生數值。不碰 DB，方便單元測試逐條對照
// 站長提供的 RO 素質對照表（見 compute_test.go）。呼叫端（handler.go/admin.go）負責把
// DB 讀出的 Config/角色列組成 ComputeInput 餵進來。
//
// ⚠️ Derived 的 json tag 對齊前端 apps/web/src/lib/api.ts 的 RpgDerived type——那裡只有
// atk/def/matk/mdef/hit/flee/perfect_dodge/crit_pct/crit_shield/aspd/weight/hp_regen/mp_regen/
// cast_reduction_pct/resists 這組欄位；MaxHP/MaxMP/NextCost/TotalSpent 屬於 RpgCharacter 外層
// （max_hp/max_mp/next_cost），不在 "derived" 物件裡，因此這裡標 json:"-"，由 handler 組外層
// response 時另外從 Derived 讀出這幾個 Go 欄位塞進正確位置。
package rpg

import "math"

// Stats 六圍目前值（非「加點數」——初始值 InitialStat 也算在內）。
type Stats struct {
	Str int `json:"str"`
	Agi int `json:"agi"`
	Vit int `json:"vit"`
	Dex int `json:"dex"`
	Int int `json:"int"`
	Luk int `json:"luk"`
}

// ComputeInput Compute 的輸入。
type ComputeInput struct {
	BaseLevel int
	JobLevel  int
	Stats     Stats
}

// Derived 六圍算出的所有衍生數值。
type Derived struct {
	MaxHP int `json:"-"` // 外層 response 用（RpgCharacter.max_hp），見檔頭註解
	MaxMP int `json:"-"` // 外層 response 用（RpgCharacter.max_mp）

	Atk  float64 `json:"atk"`  // 物理攻擊力＝素質+裝備（現階段裝備恆0）
	Def  float64 `json:"def"`  // 物理防禦力
	Matk float64 `json:"matk"` // 魔法攻擊力＝素質+裝備
	Mdef float64 `json:"mdef"` // 魔法防禦力

	Hit          float64 `json:"hit"`           // 命中率
	Flee         float64 `json:"flee"`          // 迴避率（已套用 flee_cap_pct 上限，不含完全迴避）
	PerfectDodge float64 `json:"perfect_dodge"` // 完全迴避（無上限）
	CritPct      float64 `json:"crit_pct"`      // 暴擊率（%）
	CritShield   float64 `json:"crit_shield"`   // 暴擊迴避率
	Aspd         float64 `json:"aspd"`          // 攻擊速度（已套用 aspd_cap 上限）
	Weight       float64 `json:"weight"`        // 負重量

	HPRegen          float64 `json:"hp_regen"`           // HP自然恢復量（每次恢復點數，非秒數）
	MPRegen          float64 `json:"mp_regen"`           // MP自然恢復量
	CastReductionPct float64 `json:"cast_reduction_pct"` // 變動詠唱時間縮減 %（已套用上限）

	Resists map[string]float64 `json:"resists"` // 狀態抗性 %，key 為英文狀態代碼（見下方 addResist 呼叫、前端 lib/rpgMeta.ts RESIST_LABEL 轉中文）

	NextCost   map[string]int `json:"-"` // 外層 response 用（RpgCharacter.next_cost，maxed 的素質不出現在 map 裡）
	TotalSpent int            `json:"-"` // 從 initial_stat 加到目前六圍總共花掉的點數（後台重置退點用）
}

// pointCost 從 n 加到 n+1 所需點數：floor((n-1)/step)+base（n<1 視為 1，避免負值輸入時公式跑飛）。
func pointCost(cfg Config, n int) int {
	if n < 1 {
		n = 1
	}
	return (n-1)/cfg.CostStepEvery + cfg.CostBase
}

// spentBetween 從 from 加到 to（to>=from）總共花掉的點數；to<=from 回 0。
func spentBetween(cfg Config, from, to int) int {
	sum := 0
	for n := from; n < to; n++ {
		sum += pointCost(cfg, n)
	}
	return sum
}

// floorDiv 「每 N 點才 +1」門檻規則的共用寫法：floor(value/divisor)。divisor<=0 一律回 0（防呆，
// Validate() 已擋掉這種設定，這裡是最後一道防線避免除零 panic）。
func floorDiv(value int, divisor float64) float64 {
	if divisor <= 0 {
		return 0
	}
	return math.Floor(float64(value) / divisor)
}

// floorDivF 同 floorDiv，但被除數本身是已算出的 float64（用於「每多少最大HP/MP」這類以衍生值
// 為分子的規則）。
func floorDivF(value float64, divisor float64) float64 {
	if divisor <= 0 {
		return 0
	}
	return math.Floor(value / divisor)
}

// Compute 核心純函式：Config + 目前六圍/等級 → 全部衍生數值。
func Compute(cfg Config, in ComputeInput) Derived {
	s := in.Stats
	baseLv := float64(in.BaseLevel)

	// --- HP / MP：先算未套加成的基底，再乘上 VIT/INT 的 % 加成 ---
	hpBase := cfg.BaseHP + cfg.HPPerBaseLevel*baseLv
	maxHP := hpBase * (1 + float64(s.Vit)*cfg.VitHPPct/100)
	mpBase := cfg.BaseMP + cfg.MPPerBaseLevel*baseLv
	maxMP := mpBase * (1 + float64(s.Int)*cfg.IntMPPct/100)

	// --- 物理攻擊力（素質）：依武器類型走近戰/遠程分支 + LUK 通用加成 + 基本等級加成 ---
	var statusAtk float64
	if cfg.DefaultWeaponType == "ranged" {
		statusAtk = floorDiv(s.Str, cfg.StrRangedAtkPer) + float64(s.Dex)*cfg.DexRangedAtk
	} else {
		statusAtk = float64(s.Str)*cfg.StrMeleeAtk + floorDiv(s.Dex, cfg.DexMeleeAtkPer)
	}
	statusAtk += floorDiv(s.Luk, cfg.LukAtkPer)
	statusAtk += floorDiv(in.BaseLevel, cfg.LvAtkPer)
	// 現階段沒有裝備系統：EquipAtk 恆 0，StrEquipAtkPct/DexEquipAtkPct 乘在這裡，之後接裝備直接生效。
	const equipAtk = 0.0
	atk := statusAtk + equipAtk*(1+(float64(s.Str)*cfg.StrEquipAtkPct+float64(s.Dex)*cfg.DexEquipAtkPct)/100)

	// --- 魔法攻擊力 ---
	matk := float64(s.Int)*cfg.IntMatk + floorDiv(s.Dex, cfg.DexMatkPer) + floorDiv(s.Luk, cfg.LukMatkPer)
	matk += floorDiv(in.BaseLevel, cfg.LvMatkPer)
	const equipMatk = 0.0
	matk += equipMatk

	// --- 物理防禦力 / 魔法防禦力 ---
	def := floorDiv(s.Agi, cfg.AgiDefPer) + floorDiv(s.Vit, cfg.VitDefPer) + floorDiv(in.BaseLevel, cfg.LvDefPer)
	mdef := floorDiv(s.Vit, cfg.VitMdefPer) + floorDiv(s.Dex, cfg.DexMdefPer) + float64(s.Int)*cfg.IntMdef
	mdef += floorDiv(in.BaseLevel, cfg.LvMdefPer)

	// --- 命中 / 迴避 / 完全迴避 ---
	hit := baseLv*cfg.LvHit + float64(s.Dex)*cfg.DexHit + floorDiv(s.Luk, cfg.LukHitPer)
	flee := baseLv*cfg.LvFlee + float64(s.Agi)*cfg.AgiFlee + floorDiv(s.Luk, cfg.LukFleePer)
	if flee > cfg.FleeCapPct {
		flee = cfg.FleeCapPct
	}
	perfectDodge := floorDiv(s.Luk, cfg.LukPerfectDodgePer)

	// --- 暴擊率 / 暴擊迴避率 ---
	critPct := float64(s.Luk) * cfg.LukCrit
	critShield := floorDiv(s.Luk, cfg.LukCritShieldPer)

	// --- 攻擊速度（Aspd） ---
	aspd := cfg.AspdBase + float64(s.Agi)*cfg.AspdPerAgi + float64(s.Dex)*cfg.AspdPerDex
	if aspd > cfg.AspdCap {
		aspd = cfg.AspdCap
	}

	// --- 負重 ---
	weight := cfg.WeightBase + float64(s.Str)*cfg.WeightPerStr

	// --- HP/MP 自然恢復：先算「每 N 點」貢獻，再疊加「每 X 最大HP／每 Y 最大MP」貢獻 ---
	hpRegen := floorDiv(s.Vit, cfg.VitHPRegenPer) + floorDivF(maxHP, cfg.HPRegenPerMaxHP)
	var mpFromInt float64
	if s.Int >= intMPRegenThreshold {
		// 門檻前固定段（floor(120/per)）+ 達門檻獎勵 + 門檻後每 M 點再 +1
		mpFromInt = floorDiv(intMPRegenThreshold, cfg.IntMPRegenPer) + cfg.IntMPRegenAt120 + floorDiv(s.Int-intMPRegenThreshold, cfg.IntMPRegenPerAfter120)
	} else {
		mpFromInt = floorDiv(s.Int, cfg.IntMPRegenPer)
	}
	mpRegen := mpFromInt + floorDivF(maxMP, cfg.MPRegenPerMaxMP)

	// --- 變動詠唱時間縮減 ---
	castUnits := float64(s.Dex)*cfg.CastUnitDex + float64(s.Int)*cfg.CastUnitInt
	castReduction := castUnits * cfg.CastPctPerUnit
	if castReduction > cfg.CastCapPct {
		castReduction = cfg.CastCapPct
	}

	// --- 狀態抗性：AGI/VIT/INT/LUK 共用同一係數（ResistPctPerPoint），同一狀態可能被多個素質
	// 同時加成（例：著火＝AGI+LUK，恐怖＝INT+LUK），逐項累加。key 用英文代碼，比照前端
	// lib/rpgMeta.ts RESIST_LABEL 的對照表。
	resists := map[string]float64{}
	addResist := func(key string, points int) {
		v := float64(points) * cfg.ResistPctPerPoint
		if v == 0 {
			return
		}
		resists[key] += v
	}
	addResist("bleed", s.Agi)     // 出血
	addResist("sleep", s.Agi)     // 睡眠
	addResist("burn", s.Agi)      // 著火（AGI）
	addResist("stun", s.Vit)      // 暈眩
	addResist("poison", s.Vit)    // 中毒
	addResist("dark", s.Int)      // 黑暗
	addResist("silence", s.Int)   // 沉默
	addResist("fear", s.Int)      // 恐怖（INT）
	addResist("curse", s.Luk)     // 詛咒
	addResist("confusion", s.Luk) // 混亂
	addResist("fear", s.Luk)      // 恐怖（LUK，與 INT 疊加）
	addResist("burn", s.Luk)      // 著火（LUK，與 AGI 疊加）

	// --- 加點成本：下一點成本（已達上限的素質不出現在 map 裡，比照前端 Partial<RpgStats>）+
	// 從初始值累計至今總花費 ---
	nextCost := map[string]int{}
	addNextCost := func(key string, val int) {
		if val < cfg.MaxStat {
			nextCost[key] = pointCost(cfg, val)
		}
	}
	addNextCost("str", s.Str)
	addNextCost("agi", s.Agi)
	addNextCost("vit", s.Vit)
	addNextCost("dex", s.Dex)
	addNextCost("int", s.Int)
	addNextCost("luk", s.Luk)
	totalSpent := spentBetween(cfg, cfg.InitialStat, s.Str) +
		spentBetween(cfg, cfg.InitialStat, s.Agi) +
		spentBetween(cfg, cfg.InitialStat, s.Vit) +
		spentBetween(cfg, cfg.InitialStat, s.Dex) +
		spentBetween(cfg, cfg.InitialStat, s.Int) +
		spentBetween(cfg, cfg.InitialStat, s.Luk)

	return Derived{
		MaxHP: int(math.Round(maxHP)),
		MaxMP: int(math.Round(maxMP)),

		Atk:  atk,
		Def:  def,
		Matk: matk,
		Mdef: mdef,

		Hit:          hit,
		Flee:         flee,
		PerfectDodge: perfectDodge,
		CritPct:      critPct,
		CritShield:   critShield,
		Aspd:         aspd,
		Weight:       weight,

		HPRegen:          hpRegen,
		MPRegen:          mpRegen,
		CastReductionPct: castReduction,

		Resists: resists,

		NextCost:   nextCost,
		TotalSpent: totalSpent,
	}
}
