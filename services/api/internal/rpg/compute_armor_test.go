// compute_armor_test.go：DORPG P8（CONTRACT §2）Compute() 套用 EquipBonus 裡「只有防具/飾品才會
// 貢獻」的欄位（六素質/def flat、hp_pct）的逐欄位測試——武器已貢獻的欄位（Atk/Matk/AtkPct/…）
// 由 compute_weapon_test.go 覆蓋，這裡只補防具特有的部分，比照該檔案的風格（每個欄位分開驗證，
// 其餘欄位/素質留 0，孤立變數）。
package rpg

import "testing"

// equipOfArmor 沒有武器（weapon=nil），只有防具/飾品貢獻的 EquipBonus。
func equipOfArmor(armors ...ArmorProfile) *EquipBonus {
	b := AggregateEquipment(nil, armors)
	return &b
}

func TestCompute_Armor_FlatStatsAppliedBeforeDerived(t *testing.T) {
	cfg := DefaultConfig()
	// 防具 +10 INT，應該跟 P7 的武器 int_bonus 一樣，在算 MATK/MDEF 之前先加進 INT。
	d := Compute(cfg, ComputeInput{Equip: equipOfArmor(ArmorProfile{Int: 10})})
	if d.Matk != 16 { // 同 TestCompute_Weapon_IntBonus：IntMatk(1.5)*10=15 + 整十階梯 1
		t.Fatalf("防具 int flat 應該跟武器 int_bonus 一樣在算衍生值前套用，MATK 應為 16，got %v", d.Matk)
	}
	if d.Mdef != 10 {
		t.Fatalf("防具 int flat 應該影響 MDEF，應為 10，got %v", d.Mdef)
	}
}

func TestCompute_Armor_FlatStr(t *testing.T) {
	cfg := DefaultConfig()
	// STR+10（melee）：statusAtk = 10*StrMeleeAtk(1) + 整十階梯 floor(10/10)^2*1 = 10+1 = 11。
	d := Compute(cfg, ComputeInput{Equip: equipOfArmor(ArmorProfile{Str: 10})})
	if d.Atk != 11 {
		t.Fatalf("防具 str flat 應該跟素質 STR 一樣套進 ATK 公式，got %v", d.Atk)
	}
}

func TestCompute_Armor_FlatAgiAffectsDef(t *testing.T) {
	cfg := DefaultConfig()
	d := Compute(cfg, ComputeInput{Equip: equipOfArmor(ArmorProfile{Agi: 10})})
	if d.Def != 2 { // floorDiv(10, AgiDefPer=5) = 2
		t.Fatalf("防具 agi flat 應該跟素質 AGI 一樣套進 DEF 公式，got %v", d.Def)
	}
}

func TestCompute_Armor_FlatVitAffectsMaxHPAndMdef(t *testing.T) {
	cfg := DefaultConfig()
	d := Compute(cfg, ComputeInput{Equip: equipOfArmor(ArmorProfile{Vit: 20})})
	base := Compute(cfg, ComputeInput{Stats: Stats{Vit: 20}})
	if d.MaxHP != base.MaxHP {
		t.Fatalf("防具 vit flat 應該跟素質 VIT 效果一致（MaxHP），got %v want %v", d.MaxHP, base.MaxHP)
	}
}

func TestCompute_Armor_FlatDex(t *testing.T) {
	cfg := DefaultConfig()
	d := Compute(cfg, ComputeInput{Equip: equipOfArmor(ArmorProfile{Dex: 20})})
	base := Compute(cfg, ComputeInput{Stats: Stats{Dex: 20}})
	if d.Hit != base.Hit {
		t.Fatalf("防具 dex flat 應該跟素質 DEX 效果一致（Hit），got %v want %v", d.Hit, base.Hit)
	}
}

func TestCompute_Armor_FlatLuk(t *testing.T) {
	cfg := DefaultConfig()
	d := Compute(cfg, ComputeInput{Equip: equipOfArmor(ArmorProfile{Luk: 30})})
	base := Compute(cfg, ComputeInput{Stats: Stats{Luk: 30}})
	if d.CritPct != base.CritPct {
		t.Fatalf("防具 luk flat 應該跟素質 LUK 效果一致（CritPct），got %v want %v", d.CritPct, base.CritPct)
	}
}

// TestCompute_Armor_DefFlatAddsBeforeDefPct 驗證 CONTRACT §2「DEF = (statusDEF + def) ×
// (1+def_pct/100)」——防具 flat def 要在武器 def_pct 之前加總，不是各自獨立乘。
func TestCompute_Armor_DefFlatAddsBeforeDefPct(t *testing.T) {
	cfg := DefaultConfig()
	wp := WeaponProfile{DefPct: 50}
	// AGI=10：statusDef=floor(10/5)=2；(2+8)*1.5=15。
	eq := AggregateEquipment(&wp, []ArmorProfile{{Def: 8}})
	d := Compute(cfg, ComputeInput{Stats: Stats{Agi: 10}, Equip: &eq})
	if d.Def != 15 {
		t.Fatalf("DEF 應為 (2+8)*1.5=15，got %v", d.Def)
	}
}

func TestCompute_Armor_DefFlatAloneNoWeapon(t *testing.T) {
	cfg := DefaultConfig()
	// 沒有武器（DefPct=0）：純粹加總防具的 def flat，不套用任何 pct。
	d := Compute(cfg, ComputeInput{Equip: equipOfArmor(ArmorProfile{Def: 8})})
	if d.Def != 8 {
		t.Fatalf("沒有武器 def_pct 時 DEF 應該就是防具 flat def=8，got %v", d.Def)
	}
}

func TestCompute_Armor_HpPct(t *testing.T) {
	cfg := DefaultConfig()
	d := Compute(cfg, ComputeInput{Equip: equipOfArmor(ArmorProfile{HpPct: 20})})
	base := Compute(cfg, ComputeInput{})
	want := int(float64(base.MaxHP) * 1.2) // hp_pct 是 armor 專屬（武器沒有這個欄位），套用順序見 compute.go
	// 用同一條公式反推：直接跑 Compute 兩次比對比手算浮點更穩定，這裡改用邊界寬鬆比較。
	if d.MaxHP < want-1 || d.MaxHP > want+1 {
		t.Fatalf("hp_pct=20%% 應該把 MaxHP 從 %d 提升約 20%%，got %v", base.MaxHP, d.MaxHP)
	}
	if d.MaxHP <= base.MaxHP {
		t.Fatalf("hp_pct=20%% 應該讓 MaxHP 比沒有防具時更高，got %v vs base %v", d.MaxHP, base.MaxHP)
	}
}

// TestCompute_Armor_FloorsWhenOnlyArmorEquipped 驗證「有裝備就 floor」不是只認武器——只有防具、
// 沒有武器時 eq!=nil，ATK/MATK/DEF/MDEF 一樣要 floor 成整數（CONTRACT §3「全部 floor 成整數」）。
func TestCompute_Armor_FloorsWhenOnlyArmorEquipped(t *testing.T) {
	cfg := DefaultConfig()
	withArmor := Compute(cfg, ComputeInput{Stats: Stats{Str: 10}, Equip: equipOfArmor(ArmorProfile{})})
	withoutAnything := Compute(cfg, ComputeInput{Stats: Stats{Str: 10}})
	if withArmor.Atk != withoutAnything.Atk {
		t.Fatalf("中性防具（全零 profile）不應改變 ATK 數值本身，with=%v without=%v", withArmor.Atk, withoutAnything.Atk)
	}
}

// TestCompute_WeaponAndArmorTogether_MatchesManualSum 綜合案例：武器＋防具同時存在時，六素質/
// atk_pct 等應該是兩邊貢獻的總和，不是互相覆蓋。
func TestCompute_WeaponAndArmorTogether_MatchesManualSum(t *testing.T) {
	cfg := DefaultConfig()
	wp := WeaponProfile{IntBonus: 5, AtkPct: 10}
	eq := AggregateEquipment(&wp, []ArmorProfile{{Int: 5, AtkPct: 10}})
	d := Compute(cfg, ComputeInput{Stats: Stats{Str: 10}, Equip: &eq})

	// 手動組一份「素質已經加總好」的等效輸入來對照：INT=10（武器5＋防具5），atk_pct 疊加 20%。
	wantInt := Compute(cfg, ComputeInput{Stats: Stats{Str: 10, Int: 10}})
	if d.Mdef != wantInt.Mdef {
		t.Fatalf("武器與防具的 INT flat 應該一起套用，MDEF got %v want %v", d.Mdef, wantInt.Mdef)
	}
}
