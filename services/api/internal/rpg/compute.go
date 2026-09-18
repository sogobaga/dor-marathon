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

// PassiveEffect P5：已配點的被動技能（kind=passive）展開後的單一加成輸入，見 skills.go
// ExpandEffect 與 handler.go loadPassives。Stat 值域為 CONTRACT §5 passive 的 stat 集合：
// atk_pct/matk_pct/def_pct/mdef_pct/aspd/crit_pct/crit_dmg_pct/flee/hit/hp_max_pct/mp_max_pct/
// perfect_dodge。同一 stat 可能被多個被動技能疊加，Compute() 逐項加總後套用。
type PassiveEffect struct {
	Stat  string
	Value float64
}

// ComputeInput Compute 的輸入。
type ComputeInput struct {
	BaseLevel int
	JobLevel  int
	Stats     Stats
	// WeaponType P5：素質物攻走近戰或遠程分支，空字串時 Compute 退回 cfg.DefaultWeaponType
	// （既有全域行為，未選職業或呼叫端不關心職業時維持不變）。有值時必須是 "melee"|"ranged"，
	// 由呼叫端依角色目前職業的 atk_branch 決定（見 handler.go buildComputeInputForCharacter）。
	WeaponType string
	// Passives P5：已配點的被動技能加成，見上方 PassiveEffect 註解。
	Passives []PassiveEffect
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

	// CritDmgPct P5 新增：被動技能 crit_dmg_pct 加成的總和（%）。本輪只進 Compute／角色頁顯示
	// （CONTRACT §5：passive 效果「進 Compute／戰鬥初始值，角色頁看得到」）；沒有被動技能提供
	// 這個 stat 時恆為 0，不影響任何既有戰鬥/顯示邏輯。是否要接進戰鬥的實際暴擊倍率計算
	// （wireBattleConfig 是全域 min/max，不是逐角色）留給之後的回合決定。
	CritDmgPct float64 `json:"crit_dmg_pct"`

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

// lvDef P5（CONTRACT §6）等級→防禦新曲線，取代舊版 floor(L/2)：Lv<=breakpoint 每級 per_low、
// 超過的部分每級改用 per_high，取 floor。cfg.LvDefPer 舊欄位保留讀取相容但不在這裡使用
// （見該欄位 config.go 註解）。
func lvDef(cfg Config, level int) float64 {
	l := float64(level)
	bp := float64(cfg.LvDefBreakpoint)
	if l <= bp {
		return math.Floor(l * cfg.LvDefPerLow)
	}
	return math.Floor(bp*cfg.LvDefPerLow + (l-bp)*cfg.LvDefPerHigh)
}

// statTierBonus P5（CONTRACT §6）STR/INT 整十階梯：floor(stat/10)²×coef。全職業統一套用
// （弓箭手也吃 STR 階梯，契約明講是刻意簡化）。
func statTierBonus(stat int, coef float64) float64 {
	tier := math.Floor(float64(stat) / 10)
	return tier * tier * coef
}

// sumPassivePct/sumPassiveFlat P5：從 Passives 裡取出指定 stat 的加總值（找不到回 0）。
// 同一個 stat 可能被多個已配點被動技能疊加。
func sumPassive(passives []PassiveEffect, stat string) float64 {
	sum := 0.0
	for _, p := range passives {
		if p.Stat == stat {
			sum += p.Value
		}
	}
	return sum
}

// Compute 核心純函式：Config + 目前六圍/等級 → 全部衍生數值。
func Compute(cfg Config, in ComputeInput) Derived {
	s := in.Stats
	baseLv := float64(in.BaseLevel)
	pv := in.Passives

	// --- 武器類型：P5 新增每個角色可依職業覆寫（見 ComputeInput.WeaponType 註解），
	// 空字串時完全比照既有行為退回全域設定。 ---
	weaponType := in.WeaponType
	if weaponType == "" {
		weaponType = cfg.DefaultWeaponType
	}

	// --- HP / MP：先算未套加成的基底，再乘上 VIT/INT 的 % 加成，最後套用被動 hp_max_pct/
	// mp_max_pct（P5，乘在最終值）---
	hpBase := cfg.BaseHP + cfg.HPPerBaseLevel*baseLv
	maxHP := hpBase * (1 + float64(s.Vit)*cfg.VitHPPct/100)
	maxHP *= 1 + sumPassive(pv, "hp_max_pct")/100
	mpBase := cfg.BaseMP + cfg.MPPerBaseLevel*baseLv
	maxMP := mpBase * (1 + float64(s.Int)*cfg.IntMPPct/100)
	maxMP *= 1 + sumPassive(pv, "mp_max_pct")/100

	// --- 物理攻擊力（素質）：依武器類型走近戰/遠程分支 + LUK 通用加成 + 基本等級加成 +
	// STR 整十階梯（P5）+ 被動 atk_pct（P5，乘在最終值）---
	var statusAtk float64
	if weaponType == "ranged" {
		statusAtk = floorDiv(s.Str, cfg.StrRangedAtkPer) + float64(s.Dex)*cfg.DexRangedAtk
	} else {
		statusAtk = float64(s.Str)*cfg.StrMeleeAtk + floorDiv(s.Dex, cfg.DexMeleeAtkPer)
	}
	statusAtk += floorDiv(s.Luk, cfg.LukAtkPer)
	statusAtk += floorDiv(in.BaseLevel, cfg.LvAtkPer)
	statusAtk += statTierBonus(s.Str, cfg.StrTierCoef)
	// 現階段沒有裝備系統：EquipAtk 恆 0，StrEquipAtkPct/DexEquipAtkPct 乘在這裡，之後接裝備直接生效。
	const equipAtk = 0.0
	atk := statusAtk + equipAtk*(1+(float64(s.Str)*cfg.StrEquipAtkPct+float64(s.Dex)*cfg.DexEquipAtkPct)/100)
	atk *= 1 + sumPassive(pv, "atk_pct")/100

	// --- 魔法攻擊力：+ INT 整十階梯（P5）+ 被動 matk_pct（P5，乘在最終值）---
	matk := float64(s.Int)*cfg.IntMatk + floorDiv(s.Dex, cfg.DexMatkPer) + floorDiv(s.Luk, cfg.LukMatkPer)
	matk += floorDiv(in.BaseLevel, cfg.LvMatkPer)
	matk += statTierBonus(s.Int, cfg.IntTierCoef)
	const equipMatk = 0.0
	matk += equipMatk
	matk *= 1 + sumPassive(pv, "matk_pct")/100

	// --- 物理防禦力：等級項改用 P5 新曲線 lvDef()（取代 floorDiv(BaseLevel,LvDefPer)）+
	// 被動 def_pct（P5，乘在最終值）。魔法防禦力的等級項維持既有線性除數，未變。 ---
	def := floorDiv(s.Agi, cfg.AgiDefPer) + floorDiv(s.Vit, cfg.VitDefPer) + lvDef(cfg, in.BaseLevel)
	def *= 1 + sumPassive(pv, "def_pct")/100
	mdef := floorDiv(s.Vit, cfg.VitMdefPer) + floorDiv(s.Dex, cfg.DexMdefPer) + float64(s.Int)*cfg.IntMdef
	mdef += floorDiv(in.BaseLevel, cfg.LvMdefPer)
	mdef *= 1 + sumPassive(pv, "mdef_pct")/100

	// --- 命中 / 迴避 / 完全迴避（P5：疊加被動 hit/flee/perfect_dodge，皆為 flat 加法——CONTRACT
	// §3 只點名 pct 類乘在最終值，這三項不是 _pct 命名，比照既有 AGI/LUK 直接加成的語意）---
	hit := baseLv*cfg.LvHit + float64(s.Dex)*cfg.DexHit + floorDiv(s.Luk, cfg.LukHitPer)
	hit += sumPassive(pv, "hit")
	flee := baseLv*cfg.LvFlee + float64(s.Agi)*cfg.AgiFlee + floorDiv(s.Luk, cfg.LukFleePer)
	flee += sumPassive(pv, "flee")
	if flee > cfg.FleeCapPct {
		flee = cfg.FleeCapPct
	}
	perfectDodge := floorDiv(s.Luk, cfg.LukPerfectDodgePer)
	perfectDodge += sumPassive(pv, "perfect_dodge")

	// --- 暴擊率 / 暴擊迴避率（P5：crit_pct 被動 flat 加成；crit_dmg_pct 沒有既有欄位可疊加，
	// 獨立進 Derived.CritDmgPct，見該欄位註解）---
	critPct := float64(s.Luk)*cfg.LukCrit + sumPassive(pv, "crit_pct")
	critShield := floorDiv(s.Luk, cfg.LukCritShieldPer)
	critDmgPct := sumPassive(pv, "crit_dmg_pct")

	// --- 攻擊速度（Aspd）：P5 被動 aspd 在封頂前加總（flat，語意同 AGI/DEX 既有加成）---
	aspd := cfg.AspdBase + float64(s.Agi)*cfg.AspdPerAgi + float64(s.Dex)*cfg.AspdPerDex
	aspd += sumPassive(pv, "aspd")
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
	totalSpent := TotalSpentStats(cfg, s)

	return Derived{
		// DORPG P6（CONTRACT §1）：HP/MP 一律整數，且改用 floor（不是 Round）——戰報／戰鬥中
		// 顯示絕不能因為四捨五入比實際能扣的血量「多」出零點幾點，floor 對玩家永遠保守。
		MaxHP: int(math.Floor(maxHP)),
		MaxMP: int(math.Floor(maxMP)),

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

		CritDmgPct: critDmgPct,

		NextCost:   nextCost,
		TotalSpent: totalSpent,
	}
}

// TotalSpentStats 六圍目前值相對 cfg.InitialStat 總共花費的點數（cost 語意，即
// Σ pointCost，不是原始配點數）。抽成獨立函式讓 Compute()／handler.go Allocate／
// admin.go AdminResetCharacter 共用同一段六行加總，避免各自重複、日後漂移。
func TotalSpentStats(cfg Config, s Stats) int {
	return spentBetween(cfg, cfg.InitialStat, s.Str) +
		spentBetween(cfg, cfg.InitialStat, s.Agi) +
		spentBetween(cfg, cfg.InitialStat, s.Vit) +
		spentBetween(cfg, cfg.InitialStat, s.Dex) +
		spentBetween(cfg, cfg.InitialStat, s.Int) +
		spentBetween(cfg, cfg.InitialStat, s.Luk)
}

// --- P5（CONTRACT §2/§3/§4）：有效等級 + 配點/技能點總量公式，純函式方便逐級比對測試。 ---

// EffectiveLevel 契約 §2：test_level 有值時完全採用（忽略真實等級），nil 時採用真實 Base Level。
// handler.go（/rpg/me 等）與 battle.go（bootstrap）共用同一份判斷，不得各自重算。
func EffectiveLevel(testLevel *int, baseLevel int) int {
	if testLevel != nil {
		return *testLevel
	}
	return baseLevel
}

// effectiveTestLevel 審查#2【中・CONFIRMED】根因修復：test_level_enabled 這個總開關關閉時，
// 完全忽略既有 test_level（回傳 nil），即使 DB 裡某個玩家還留著關閉前設定的舊值——PutTestLevel
// 本身的 403 只擋得住「新的 PUT 請求」，如果 EffectiveLevel() 還是照樣讀到舊的 test_level，
// 玩家角色的配點/技能點總量/戰鬥數值會繼續套用測試等級，「正式上線前關閉測試等級功能」這個
// 開關就形同虛設。抽成獨立純函式方便不連 DB 的單元測試（見 compute_test.go），也讓
// entry.go／handler.go／skills.go 三處呼叫點共用同一份判斷，不各自重複 if 敘述。
func effectiveTestLevel(cfg Config, testLevel *int) *int {
	if !cfg.TestLevelEnabled {
		return nil
	}
	return testLevel
}

// TotalStatPoints RO 給點公式（CONTRACT §3）：
//
//	Total(L) = stat_points_initial + Σ_{k=2..L} ( floor((k-1)/stat_points_step_levels) + stat_points_per_level_base )
//
// L<1 視為 1（Lv1 只有初始點數，沒有任何一級的成長量）。逐級迴圈而非封閉式公式：L 上限
// 99（測試等級上限）迴圈成本可忽略，逐級寫法比展開的等差/分段公式更不容易算錯，也方便跟
// RO 官方 statpoint.yml 逐級核對（見 compute_test.go）。
func TotalStatPoints(cfg Config, level int) int {
	if level < 1 {
		level = 1
	}
	total := cfg.StatPointsInitial
	for k := 2; k <= level; k++ {
		total += (k-1)/cfg.StatPointsStepLevels + cfg.StatPointsPerLevelBase
	}
	return total
}

// TotalSkillPoints 技能點總數（CONTRACT §4）：initial + max(0,L-1) × per_level。
func TotalSkillPoints(cfg Config, level int) int {
	if level < 1 {
		level = 1
	}
	return cfg.SkillPointsInitial + (level-1)*cfg.SkillPointsPerLevel
}

// StatCap 單一素質上限（CONTRACT §3）：min(MaxStat, 有效等級)。Lv1 時 cap=1＝InitialStat
// 預設值，六圍全部已經「達到上限」，等同「Lv1 不能加點」（契約明講這是刻意的）。
func StatCap(cfg Config, level int) int {
	if level < 1 {
		level = 1
	}
	if level > cfg.MaxStat {
		return cfg.MaxStat
	}
	return level
}
