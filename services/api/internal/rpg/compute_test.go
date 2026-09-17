package rpg

import "testing"

// 逐條對照站長提供的 RO stat/attr 截圖對照表（見 config.go 開頭註解的來源），每個測試把其餘
// 素質/等級留在 0（Compute 直接吃「目前值」而非「加點數」，孤立變數即可還原表格數字）。

// P5 起 Compute() 多算了 STR 整十階梯（見 TestCompute_StrTierSteps）：STR=10 → floor(10/10)²×
// str_tier_coef(1)=+1，這裡的期望值一併加上這個新增的加成，不是這兩個既有測試本身的公式錯。
func TestCompute_STR10Melee(t *testing.T) {
	cfg := DefaultConfig() // DefaultWeaponType 預設 "melee"
	d := Compute(cfg, ComputeInput{Stats: Stats{Str: 10}})
	if d.Atk != 11 { // 10（每1點+1）+ 1（P5 整十階梯）
		t.Fatalf("STR 10 melee: 素質物攻應為 +11（含 P5 整十階梯 +1），got %v", d.Atk)
	}
	if d.Weight != cfg.WeightBase+300 {
		t.Fatalf("STR 10: 負重應為基礎值+300，got %v (base=%v)", d.Weight, cfg.WeightBase)
	}
}

func TestCompute_STR10Ranged(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DefaultWeaponType = "ranged"
	// STR 每 5 點才 +1（遠程分支），STR=10 → +2；+ P5 整十階梯 +1 → 3；DEX=0 不額外貢獻。
	d := Compute(cfg, ComputeInput{Stats: Stats{Str: 10}})
	if d.Atk != 3 {
		t.Fatalf("STR 10 ranged: 素質物攻應為 +3（每5點+1=2，含 P5 整十階梯 +1），got %v", d.Atk)
	}
	// Str=0 時 P5 整十階梯貢獻 0，這組既有斷言不受影響。
	d2 := Compute(cfg, ComputeInput{Stats: Stats{Dex: 10}})
	if d2.Atk != 10 {
		t.Fatalf("DEX 10 ranged: 素質物攻應為 +10（每1點+1），got %v", d2.Atk)
	}
}

func TestCompute_LUK30(t *testing.T) {
	cfg := DefaultConfig()
	d := Compute(cfg, ComputeInput{Stats: Stats{Luk: 30}})
	if d.CritPct != 9 {
		t.Fatalf("LUK 30: 暴擊率應為 +9（30*0.3），got %v", d.CritPct)
	}
	if d.Atk != 10 {
		t.Fatalf("LUK 30: 物理攻擊力應為 +10（每3點+1），got %v", d.Atk)
	}
	if d.Hit != 10 {
		t.Fatalf("LUK 30: 命中率應為 +10（每3點+1），got %v", d.Hit)
	}
	if d.Flee != 6 {
		t.Fatalf("LUK 30: 迴避率應為 +6（每5點+1），got %v", d.Flee)
	}
	if d.PerfectDodge != 3 {
		t.Fatalf("LUK 30: 完全迴避應為 +3（每10點+1），got %v", d.PerfectDodge)
	}
}

func TestCompute_VIT20(t *testing.T) {
	cfg := DefaultConfig()
	d := Compute(cfg, ComputeInput{Stats: Stats{Vit: 20}})
	wantMaxHP := int(cfg.BaseHP * 1.2) // +20%
	if d.MaxHP != wantMaxHP {
		t.Fatalf("VIT 20: 最大HP應為基礎值+20%%＝%v，got %v", wantMaxHP, d.MaxHP)
	}
	if d.Def != 10 {
		t.Fatalf("VIT 20: 物理防禦力應為 +10（每2點+1），got %v", d.Def)
	}
	if d.Mdef != 4 {
		t.Fatalf("VIT 20: 魔法防禦力應為 +4（每5點+1），got %v", d.Mdef)
	}
}

// INT 每 6 點 MP 自然恢復+1（達 120 前），達 120 額外 +4——這條規則獨立於「每100最大MP+1」
// （另一條規則，來源是不同的 MaxMP 換算，見 TestCompute_MPRegenPerMaxMP），這裡把
// mp_regen_per_max_mp 歸零以孤立驗證站長截圖給的「INT 120 → +20+4」這一句本身。
func TestCompute_INT120MPRegenThreshold(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MPRegenPerMaxMP = 0
	d := Compute(cfg, ComputeInput{Stats: Stats{Int: 120}})
	if d.MPRegen != 24 {
		t.Fatalf("INT 120: MP自然恢復應為 20+4=24（每6點直到120 + 達120門檻獎勵），got %v", d.MPRegen)
	}
}

func TestCompute_INTMPRegenBoundaries(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MPRegenPerMaxMP = 0
	// INT=119：尚未達 120，floor(119/6)=19，無門檻獎勵。
	if d := Compute(cfg, ComputeInput{Stats: Stats{Int: 119}}); d.MPRegen != 19 {
		t.Fatalf("INT 119: MP自然恢復應為 19，got %v", d.MPRegen)
	}
	// INT=122：120 固定段 20 + 門檻獎勵 4 + floor(2/2)*1=1 → 25。
	if d := Compute(cfg, ComputeInput{Stats: Stats{Int: 122}}); d.MPRegen != 25 {
		t.Fatalf("INT 122: MP自然恢復應為 25（20+4+1），got %v", d.MPRegen)
	}
}

// 「每多少最大MP：MP自然恢復+1」獨立驗證（INT=0 時前段規則自動貢獻0，不需額外歸零其他欄位）。
func TestCompute_MPRegenPerMaxMP(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BaseMP = 300 // MaxMP=300（INT=0，無 INT 加成）→ floor(300/100)=3
	d := Compute(cfg, ComputeInput{Stats: Stats{}})
	if d.MaxMP != 300 {
		t.Fatalf("MaxMP 應為 300，got %v", d.MaxMP)
	}
	if d.MPRegen != 3 {
		t.Fatalf("每100最大MP+1: MaxMP=300 應貢獻 MP自然恢復+3，got %v", d.MPRegen)
	}
}

// 「每200最大HP：HP自然恢復+1」比照上面 MP 版本獨立驗證。
func TestCompute_HPRegenPerMaxHP(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BaseHP = 600 // MaxHP=600（VIT=0，無 VIT 加成）→ floor(600/200)=3
	d := Compute(cfg, ComputeInput{Stats: Stats{}})
	if d.MaxHP != 600 {
		t.Fatalf("MaxHP 應為 600，got %v", d.MaxHP)
	}
	if d.HPRegen != 3 {
		t.Fatalf("每200最大HP+1: MaxHP=600 應貢獻 HP自然恢復+3，got %v", d.HPRegen)
	}
}

func TestCompute_AspdCap(t *testing.T) {
	cfg := DefaultConfig()
	// Compute 本身不檢查 max_stat（那是 handler/allocate 端的限制），這裡刻意餵超出上限的數值
	// 純粹為了確認封頂邏輯——150+0.25*1000+0.1*1000=500 遠超 193，應被夾在上限。
	d := Compute(cfg, ComputeInput{Stats: Stats{Agi: 1000, Dex: 1000}})
	if d.Aspd != cfg.AspdCap {
		t.Fatalf("高 AGI/DEX 應觸頂攻速上限 %v，got %v", cfg.AspdCap, d.Aspd)
	}
	// 低數值不觸頂：150 + 10*0.25 + 10*0.1 = 153.5
	d2 := Compute(cfg, ComputeInput{Stats: Stats{Agi: 10, Dex: 10}})
	if d2.Aspd != 153.5 {
		t.Fatalf("AGI/DEX 10: 攻速應為 153.5，got %v", d2.Aspd)
	}
}

func TestCompute_FleeCap(t *testing.T) {
	cfg := DefaultConfig()
	d := Compute(cfg, ComputeInput{Stats: Stats{Agi: 99, Luk: 99}})
	if d.Flee != cfg.FleeCapPct {
		t.Fatalf("高 AGI/LUK 應觸頂迴避率上限 %v，got %v", cfg.FleeCapPct, d.Flee)
	}
	// 完全迴避不受迴避率上限影響
	if d.PerfectDodge != 9 { // floor(99/10)=9
		t.Fatalf("完全迴避應為 9（不受上限限制），got %v", d.PerfectDodge)
	}
}

func TestCompute_CastReductionCap(t *testing.T) {
	cfg := DefaultConfig()
	// DEX=99,INT=99 → units=99*1+99*0.5=148.5 → 148.5*0.5%=74.25% → 應封頂於 50%
	d := Compute(cfg, ComputeInput{Stats: Stats{Dex: 99, Int: 99}})
	if d.CastReductionPct != cfg.CastCapPct {
		t.Fatalf("高 DEX/INT 應觸頂詠唱縮減上限 %v%%，got %v", cfg.CastCapPct, d.CastReductionPct)
	}
	// 低數值不觸頂：DEX=10,INT=10 → units=10+5=15 → 15*0.5=7.5%
	d2 := Compute(cfg, ComputeInput{Stats: Stats{Dex: 10, Int: 10}})
	if d2.CastReductionPct != 7.5 {
		t.Fatalf("DEX/INT 10: 詠唱縮減應為 7.5%%，got %v", d2.CastReductionPct)
	}
}

func TestCompute_Resists(t *testing.T) {
	cfg := DefaultConfig()
	// AGI=10,LUK=10：bleed/sleep 只受 AGI 影響＝1.0；burn 受 AGI+LUK 雙重影響＝2.0；fear 只受 LUK＝1.0。
	d := Compute(cfg, ComputeInput{Stats: Stats{Agi: 10, Luk: 10}})
	if got := d.Resists["bleed"]; got != 1.0 {
		t.Fatalf("出血(bleed)抗性應為 1.0（僅AGI），got %v", got)
	}
	if got := d.Resists["burn"]; got != 2.0 {
		t.Fatalf("著火(burn)抗性應為 2.0（AGI+LUK各1.0），got %v", got)
	}
	if got := d.Resists["fear"]; got != 1.0 {
		t.Fatalf("恐怖(fear)抗性應為 1.0（僅LUK，此案例INT=0），got %v", got)
	}
}

func TestCompute_BaseLevelBonuses(t *testing.T) {
	cfg := DefaultConfig()
	d := Compute(cfg, ComputeInput{BaseLevel: 40, Stats: Stats{}})
	if d.Hit != 40 {
		t.Fatalf("Base Lv 40: 命中率應為 +40（每級+1），got %v", d.Hit)
	}
	if d.Flee != 40 {
		t.Fatalf("Base Lv 40: 迴避率應為 +40（每級+1），got %v", d.Flee)
	}
	if d.Def != 20 {
		t.Fatalf("Base Lv 40: 物理防禦力應為 +20（每2級+1），got %v", d.Def)
	}
	if d.Atk != 10 || d.Matk != 10 || d.Mdef != 10 {
		t.Fatalf("Base Lv 40: 物攻/魔攻/魔防應皆為 +10（每4級+1），got atk=%v matk=%v mdef=%v", d.Atk, d.Matk, d.Mdef)
	}
}

// --- 加點成本 ---

func TestPointCost_DefaultFormula(t *testing.T) {
	cfg := DefaultConfig() // cost_base=2, cost_step_every=10
	cases := []struct {
		n    int
		want int
	}{
		{1, 2},  // floor(0/10)+2=2
		{9, 2},  // floor(8/10)+2=2
		{10, 2}, // floor(9/10)+2=2
		{11, 3}, // floor(10/10)+2=3
		{20, 3}, // floor(19/10)+2=3
		{21, 4}, // floor(20/10)+2=4
	}
	for _, c := range cases {
		if got := pointCost(cfg, c.n); got != c.want {
			t.Fatalf("pointCost(%d): want %d, got %d", c.n, c.want, got)
		}
	}
}

func TestCompute_TotalSpentAndNextCost(t *testing.T) {
	cfg := DefaultConfig() // initial_stat=1
	// STR 從 1 加到 11：花費 = cost(1)+cost(2)+...+cost(10) = 10 次每次 2 = 20
	d := Compute(cfg, ComputeInput{Stats: Stats{Str: 11, Agi: 1, Vit: 1, Dex: 1, Int: 1, Luk: 1}})
	if d.TotalSpent != 20 {
		t.Fatalf("STR 1→11 總花費應為 20，got %v", d.TotalSpent)
	}
	if d.NextCost["str"] != 3 { // cost(11)=floor(10/10)+2=3
		t.Fatalf("STR=11 下一點成本應為 3，got %v", d.NextCost["str"])
	}
	if d.NextCost["agi"] != 2 { // cost(1)=2
		t.Fatalf("AGI=1 下一點成本應為 2，got %v", d.NextCost["agi"])
	}
}

func TestCompute_TotalSpentAtInitial(t *testing.T) {
	cfg := DefaultConfig()
	d := Compute(cfg, ComputeInput{Stats: Stats{Str: 1, Agi: 1, Vit: 1, Dex: 1, Int: 1, Luk: 1}})
	if d.TotalSpent != 0 {
		t.Fatalf("六圍皆在初始值時總花費應為 0，got %v", d.TotalSpent)
	}
}

// 已達上限的素質不出現在 NextCost（比照前端 Partial<RpgStats>，前端用 key 存不存在判斷是否鎖住 +1/+5）。
func TestCompute_NextCostOmitsMaxedStat(t *testing.T) {
	cfg := DefaultConfig()
	d := Compute(cfg, ComputeInput{Stats: Stats{Str: cfg.MaxStat, Agi: 1, Vit: 1, Dex: 1, Int: 1, Luk: 1}})
	if _, ok := d.NextCost["str"]; ok {
		t.Fatalf("STR 已達上限，NextCost 不應包含 str 鍵，got %v", d.NextCost)
	}
	if _, ok := d.NextCost["agi"]; !ok {
		t.Fatalf("AGI 未達上限，NextCost 應包含 agi 鍵")
	}
}

// =============================================================================
// DORPG P5：職業／測試等級／配點規則／技能等級／戰鬥公式微調（CONTRACT §2/§3/§4/§6）
// =============================================================================

// --- lvDef 新曲線：Lv<=50 每級 0.5、Lv>50 每級改 0.35，取代舊版 floor(L/2) ---

func TestLvDef_Curve(t *testing.T) {
	cfg := DefaultConfig()
	cases := []struct {
		level int
		want  float64
	}{
		{1, 0},   // floor(1*0.5)=0
		{27, 13}, // floor(27*0.5)=floor(13.5)=13
		{50, 25}, // floor(50*0.5)=25（斷點本身仍用 per_low）
		{51, 25}, // floor(25 + 1*0.35)=floor(25.35)=25
		{99, 42}, // floor(25 + 49*0.35)=floor(25+17.15)=floor(42.15)=42
	}
	for _, c := range cases {
		if got := lvDef(cfg, c.level); got != c.want {
			t.Fatalf("lvDef(%d): want %v, got %v", c.level, c.want, got)
		}
	}
}

// Compute() 的 Def 欄位要吃到新曲線（取代舊版 floorDiv(BaseLevel,lv_def_per)），Mdef 的等級項
// 維持既有線性除數不變（契約明講只換 Def 這條）。
func TestCompute_DefUsesNewLvDefCurve(t *testing.T) {
	cfg := DefaultConfig()
	d := Compute(cfg, ComputeInput{BaseLevel: 51, Stats: Stats{}})
	if d.Def != 25 { // lvDef(51)=25（見 TestLvDef_Curve），AGI/VIT=0 無其餘貢獻
		t.Fatalf("Lv51 Def 應為 25（新曲線），got %v", d.Def)
	}
	if d.Mdef != 12 { // floor(51/4)=12，未變的線性除數
		t.Fatalf("Lv51 Mdef 應維持舊線性除數 12，got %v", d.Mdef)
	}
}

// --- STR/INT 整十階梯：atk += floor(STR/10)²×coef、matk += floor(INT/10)²×coef ---

func TestCompute_StrTierSteps(t *testing.T) {
	cfg := DefaultConfig() // melee 分支：statusAtk = STR*1
	cases := []struct {
		str      int
		wantStep float64 // floor(str/10)²
	}{
		{9, 0},
		{10, 1},
		{19, 1},
		{20, 4},
	}
	for _, c := range cases {
		d := Compute(cfg, ComputeInput{Stats: Stats{Str: c.str}})
		want := float64(c.str)*cfg.StrMeleeAtk + c.wantStep*cfg.StrTierCoef
		if d.Atk != want {
			t.Fatalf("STR=%d: Atk 應為 %v（含整十階梯 %v），got %v", c.str, want, c.wantStep, d.Atk)
		}
	}
}

func TestCompute_IntTierSteps(t *testing.T) {
	cfg := DefaultConfig()
	cases := []struct {
		intStat  int
		wantStep float64
	}{
		{9, 0},
		{10, 1},
		{19, 1},
		{20, 4},
	}
	for _, c := range cases {
		d := Compute(cfg, ComputeInput{Stats: Stats{Int: c.intStat}})
		want := float64(c.intStat)*cfg.IntMatk + c.wantStep*cfg.IntTierCoef
		if d.Matk != want {
			t.Fatalf("INT=%d: Matk 應為 %v（含整十階梯 %v），got %v", c.intStat, want, c.wantStep, d.Matk)
		}
	}
}

// --- WeaponType 覆寫：空字串退回 cfg.DefaultWeaponType（既有行為不變），有值時依角色職業覆寫 ---

func TestCompute_WeaponTypeOverridesDefault(t *testing.T) {
	cfg := DefaultConfig() // DefaultWeaponType=melee
	// Str=20/Dex=0（刻意不對稱，避免 melee/ranged 兩組係數剛好因為 Str=Dex 對稱互換而算出同一個值）。
	dRanged := Compute(cfg, ComputeInput{WeaponType: "ranged", Stats: Stats{Str: 20, Dex: 0}})
	dMeleeDefault := Compute(cfg, ComputeInput{Stats: Stats{Str: 20, Dex: 0}})
	if dRanged.Atk == dMeleeDefault.Atk {
		t.Fatalf("WeaponType=ranged 應與空字串（沿用 melee 預設）算出不同的 Atk，皆為 %v", dRanged.Atk)
	}
	dEmpty := Compute(cfg, ComputeInput{WeaponType: "", Stats: Stats{Str: 20, Dex: 0}})
	if dEmpty.Atk != dMeleeDefault.Atk {
		t.Fatalf("WeaponType 空字串應完全比照 cfg.DefaultWeaponType：want %v got %v", dMeleeDefault.Atk, dEmpty.Atk)
	}
}

// --- Passives：pct 類乘在最終值、flat 類直接加總 ---

func TestCompute_PassiveAtkPct(t *testing.T) {
	cfg := DefaultConfig()
	base := Compute(cfg, ComputeInput{Stats: Stats{Str: 10}})
	withPassive := Compute(cfg, ComputeInput{Stats: Stats{Str: 10}, Passives: []PassiveEffect{{Stat: "atk_pct", Value: 50}}})
	want := base.Atk * 1.5
	if withPassive.Atk != want {
		t.Fatalf("atk_pct=50 應讓 Atk 變為 1.5 倍：want %v got %v", want, withPassive.Atk)
	}
}

func TestCompute_PassiveFlatStats(t *testing.T) {
	cfg := DefaultConfig()
	base := Compute(cfg, ComputeInput{BaseLevel: 10, Stats: Stats{}})
	d := Compute(cfg, ComputeInput{BaseLevel: 10, Stats: Stats{}, Passives: []PassiveEffect{
		{Stat: "hit", Value: 5},
		{Stat: "flee", Value: 3},
		{Stat: "crit_pct", Value: 2},
		{Stat: "perfect_dodge", Value: 1},
		{Stat: "aspd", Value: 4},
		{Stat: "crit_dmg_pct", Value: 10},
	}})
	if d.Hit != base.Hit+5 {
		t.Fatalf("hit 被動應直接加總：want %v got %v", base.Hit+5, d.Hit)
	}
	if d.Flee != base.Flee+3 {
		t.Fatalf("flee 被動應直接加總：want %v got %v", base.Flee+3, d.Flee)
	}
	if d.CritPct != base.CritPct+2 {
		t.Fatalf("crit_pct 被動應直接加總：want %v got %v", base.CritPct+2, d.CritPct)
	}
	if d.PerfectDodge != base.PerfectDodge+1 {
		t.Fatalf("perfect_dodge 被動應直接加總：want %v got %v", base.PerfectDodge+1, d.PerfectDodge)
	}
	if d.Aspd != base.Aspd+4 {
		t.Fatalf("aspd 被動應直接加總：want %v got %v", base.Aspd+4, d.Aspd)
	}
	if d.CritDmgPct != 10 {
		t.Fatalf("crit_dmg_pct 應為被動加總（沒有底值）：want 10 got %v", d.CritDmgPct)
	}
	if base.CritDmgPct != 0 {
		t.Fatalf("沒有被動時 CritDmgPct 應為 0，got %v", base.CritDmgPct)
	}
}

func TestCompute_PassiveHPMaxPct(t *testing.T) {
	cfg := DefaultConfig()
	base := Compute(cfg, ComputeInput{Stats: Stats{Vit: 10}})
	d := Compute(cfg, ComputeInput{Stats: Stats{Vit: 10}, Passives: []PassiveEffect{{Stat: "hp_max_pct", Value: 20}}})
	want := int(float64(base.MaxHP) * 1.2)
	if d.MaxHP != want {
		t.Fatalf("hp_max_pct=20 應讓 MaxHP 變為 1.2 倍：want %v got %v", want, d.MaxHP)
	}
}

// --- TotalStatPoints：逐級核對 RO pre-renewal 官方 statpoint.yml（Lv1..99），來源：
// scratchpad/ro_classic/src/rathena/db/statpoint.yml（2026-09-17 讀取）。index 0 對應 Lv1。 ---

var roStatPointTable = [99]int{
	48, 51, 54, 57, 60, 64, 68, 72, 76, 80,
	85, 90, 95, 100, 105, 111, 117, 123, 129, 135,
	142, 149, 156, 163, 170, 178, 186, 194, 202, 210,
	219, 228, 237, 246, 255, 265, 275, 285, 295, 305,
	316, 327, 338, 349, 360, 372, 384, 396, 408, 420,
	433, 446, 459, 472, 485, 499, 513, 527, 541, 555,
	570, 585, 600, 615, 630, 646, 662, 678, 694, 710,
	727, 744, 761, 778, 795, 813, 831, 849, 867, 885,
	904, 923, 942, 961, 980, 1000, 1020, 1040, 1060, 1080,
	1101, 1122, 1143, 1164, 1185, 1207, 1229, 1251, 1273,
}

func TestTotalStatPoints_MatchesROStatpointTable(t *testing.T) {
	cfg := DefaultConfig() // stat_points_initial=48, per_level_base=3, step_levels=5
	for lv := 1; lv <= 99; lv++ {
		want := roStatPointTable[lv-1]
		if got := TotalStatPoints(cfg, lv); got != want {
			t.Fatalf("TotalStatPoints(Lv%d): want %d（RO statpoint.yml）, got %d", lv, want, got)
		}
	}
}

func TestTotalStatPoints_BelowLv1ClampsToLv1(t *testing.T) {
	cfg := DefaultConfig()
	if got := TotalStatPoints(cfg, 0); got != cfg.StatPointsInitial {
		t.Fatalf("TotalStatPoints(0) 應視為 Lv1，want %d got %d", cfg.StatPointsInitial, got)
	}
}

// --- TotalSkillPoints ---

func TestTotalSkillPoints_DefaultFormula(t *testing.T) {
	cfg := DefaultConfig() // initial=0, per_level=1
	if got := TotalSkillPoints(cfg, 1); got != 0 {
		t.Fatalf("Lv1 技能點應為 0，got %d", got)
	}
	if got := TotalSkillPoints(cfg, 27); got != 26 {
		t.Fatalf("Lv27 技能點應為 26，got %d", got)
	}
}

// --- StatCap ---

func TestStatCap_MinOfMaxStatAndLevel(t *testing.T) {
	cfg := DefaultConfig() // max_stat=99
	if got := StatCap(cfg, 1); got != 1 {
		t.Fatalf("Lv1 StatCap 應為 1（等同 InitialStat，Lv1 不能加點），got %d", got)
	}
	if got := StatCap(cfg, 27); got != 27 {
		t.Fatalf("Lv27 StatCap 應為 27，got %d", got)
	}
	if got := StatCap(cfg, 150); got != cfg.MaxStat {
		t.Fatalf("超過 MaxStat 的等級應夾在 MaxStat=%d，got %d", cfg.MaxStat, got)
	}
}

// --- EffectiveLevel ---

func TestEffectiveLevel_TestLevelOverridesRealLevel(t *testing.T) {
	tl := 42
	if got := EffectiveLevel(&tl, 5); got != 42 {
		t.Fatalf("有 test_level 時應完全採用，want 42 got %d", got)
	}
	if got := EffectiveLevel(nil, 5); got != 5 {
		t.Fatalf("test_level=nil 時應採用真實等級，want 5 got %d", got)
	}
}

// --- effectiveTestLevel（審查#2 CONFIRMED 根因回歸測試）---

func TestEffectiveTestLevel_EnabledPassesThroughExistingValue(t *testing.T) {
	tl := 42
	cfg := DefaultConfig() // TestLevelEnabled 預設 true
	got := effectiveTestLevel(cfg, &tl)
	if got == nil || *got != 42 {
		t.Fatalf("開關開啟時應照常採用既有 test_level，got %v", got)
	}
}

func TestEffectiveTestLevel_DisabledIgnoresExistingValue(t *testing.T) {
	tl := 42
	cfg := DefaultConfig()
	cfg.TestLevelEnabled = false
	got := effectiveTestLevel(cfg, &tl)
	if got != nil {
		t.Fatalf("開關關閉時應忽略既有 test_level（即使 DB 裡還留著舊值），got %v", *got)
	}
}

func TestEffectiveTestLevel_NilStaysNilRegardlessOfSwitch(t *testing.T) {
	cfg := DefaultConfig()
	if got := effectiveTestLevel(cfg, nil); got != nil {
		t.Fatalf("test_level 本來就是 nil 時，開關開啟也應該回 nil，got %v", *got)
	}
	cfg.TestLevelEnabled = false
	if got := effectiveTestLevel(cfg, nil); got != nil {
		t.Fatalf("test_level 本來就是 nil 時，開關關閉也應該回 nil，got %v", *got)
	}
}

func TestDefaultConfig_TestLevelEnabledDefaultsTrue(t *testing.T) {
	if !DefaultConfig().TestLevelEnabled {
		t.Fatalf("DefaultConfig().TestLevelEnabled 應預設為 true（維持現行行為，正式上線前才手動關閉）")
	}
}
