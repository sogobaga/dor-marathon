package rpg

import "testing"

// 逐條對照站長提供的 RO stat/attr 截圖對照表（見 config.go 開頭註解的來源），每個測試把其餘
// 素質/等級留在 0（Compute 直接吃「目前值」而非「加點數」，孤立變數即可還原表格數字）。

func TestCompute_STR10Melee(t *testing.T) {
	cfg := DefaultConfig() // DefaultWeaponType 預設 "melee"
	d := Compute(cfg, ComputeInput{Stats: Stats{Str: 10}})
	if d.Atk != 10 {
		t.Fatalf("STR 10 melee: 素質物攻應為 +10，got %v", d.Atk)
	}
	if d.Weight != cfg.WeightBase+300 {
		t.Fatalf("STR 10: 負重應為基礎值+300，got %v (base=%v)", d.Weight, cfg.WeightBase)
	}
}

func TestCompute_STR10Ranged(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DefaultWeaponType = "ranged"
	// STR 每 5 點才 +1（遠程分支），STR=10 → +2；DEX=0 不額外貢獻。
	d := Compute(cfg, ComputeInput{Stats: Stats{Str: 10}})
	if d.Atk != 2 {
		t.Fatalf("STR 10 ranged: 素質物攻應為 +2（每5點+1），got %v", d.Atk)
	}
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
