// compute_weapon_test.go：DORPG P7（CONTRACT §3）Compute() 套用 WeaponProfile 的逐欄位測試——
// 每個欄位分開驗證（其餘欄位/素質留 0，孤立變數），比照 compute_test.go 既有風格。wp=nil 的既有
// 行為由 compute_test.go 全部既有測試在不改動下繼續覆蓋（沒有任何一個既有測試設定
// ComputeInput.Weapon，等同持續驗證「nil＝零改動」）。
package rpg

import "testing"

func TestCompute_Weapon_IntBonus(t *testing.T) {
	cfg := DefaultConfig()
	wp := WeaponProfile{IntBonus: 10}
	d := Compute(cfg, ComputeInput{Weapon: &wp})
	if d.Matk != 16 { // IntMatk(1.5)*10=15 + P5 整十階梯 floor(10/10)^2*1=1
		t.Fatalf("int_bonus 應在算衍生值前加進 INT，MATK 應為 16（含 P5 整十階梯），got %v", d.Matk)
	}
	if d.Mdef != 10 { // IntMdef(1)*10
		t.Fatalf("int_bonus 應影響 MDEF，應為 10，got %v", d.Mdef)
	}
	if d.MaxMP != 55 { // base 50 * (1+10*1/100)
		t.Fatalf("int_bonus 應影響 MaxMP（透過 IntMPPct），應為 55，got %v", d.MaxMP)
	}
}

func TestCompute_Weapon_Atk(t *testing.T) {
	cfg := DefaultConfig()
	wp := WeaponProfile{Atk: 20}
	d := Compute(cfg, ComputeInput{Weapon: &wp})
	if d.Atk != 20 {
		t.Fatalf("裝備 atk=20、其餘素質為 0 時 ATK 應為 20，got %v", d.Atk)
	}
}

func TestCompute_Weapon_Matk(t *testing.T) {
	cfg := DefaultConfig()
	wp := WeaponProfile{Matk: 8}
	d := Compute(cfg, ComputeInput{Weapon: &wp})
	if d.Matk != 8 {
		t.Fatalf("裝備 matk=8、其餘素質為 0 時 MATK 應為 8，got %v", d.Matk)
	}
}

func TestCompute_Weapon_AtkPct(t *testing.T) {
	cfg := DefaultConfig()
	wp := WeaponProfile{AtkPct: 50}
	// STR=10 melee：statusAtk=11（含 P5 整十階梯，見 TestCompute_STR10Melee），乘 1.5 後 floor。
	d := Compute(cfg, ComputeInput{Stats: Stats{Str: 10}, Weapon: &wp})
	if d.Atk != 16 { // floor(11*1.5)=floor(16.5)=16
		t.Fatalf("atk_pct=50%% 應把 ATK 從 11 乘到 floor(16.5)=16，got %v", d.Atk)
	}
}

func TestCompute_Weapon_MatkPct(t *testing.T) {
	cfg := DefaultConfig()
	wp := WeaponProfile{MatkPct: 50}
	// INT=10：matk=15(IntMatk)+1(整十階梯)=16，乘 1.5 後 floor。
	d := Compute(cfg, ComputeInput{Stats: Stats{Int: 10}, Weapon: &wp})
	if d.Matk != 24 { // floor(16*1.5)=24
		t.Fatalf("matk_pct=50%% 應把 MATK 從 16 乘到 24，got %v", d.Matk)
	}
}

func TestCompute_Weapon_DefPct(t *testing.T) {
	cfg := DefaultConfig()
	wp := WeaponProfile{DefPct: 50}
	// AGI=10：def=floor(10/5)=2。
	d := Compute(cfg, ComputeInput{Stats: Stats{Agi: 10}, Weapon: &wp})
	if d.Def != 3 { // floor(2*1.5)=3
		t.Fatalf("def_pct=50%% 應把 DEF 從 2 乘到 3，got %v", d.Def)
	}
}

func TestCompute_Weapon_MdefPct(t *testing.T) {
	cfg := DefaultConfig()
	wp := WeaponProfile{MdefPct: 50}
	// VIT=10：mdef=floor(10/5)=2（VitMdefPer=5）。
	d := Compute(cfg, ComputeInput{Stats: Stats{Vit: 10}, Weapon: &wp})
	if d.Mdef != 3 { // floor(2*1.5)=3
		t.Fatalf("mdef_pct=50%% 應把 MDEF 從 2 乘到 3，got %v", d.Mdef)
	}
}

func TestCompute_Weapon_MpPct(t *testing.T) {
	cfg := DefaultConfig()
	wp := WeaponProfile{MpPct: 20}
	d := Compute(cfg, ComputeInput{Weapon: &wp})
	if d.MaxMP != 60 { // base_mp(50) * 1.2
		t.Fatalf("mp_pct=20%% 應把 MaxMP 從 50 乘到 60，got %v", d.MaxMP)
	}
}

func TestCompute_Weapon_CritPct(t *testing.T) {
	cfg := DefaultConfig()
	wp := WeaponProfile{CritPct: 7}
	d := Compute(cfg, ComputeInput{Weapon: &wp})
	if d.CritPct != 7 {
		t.Fatalf("crit_pct=7 應直接加進 CombatRating 的暴擊率，got %v", d.CritPct)
	}
}

func TestCompute_Weapon_FleeBonus(t *testing.T) {
	cfg := DefaultConfig()
	wp := WeaponProfile{FleeBonus: 12}
	d := Compute(cfg, ComputeInput{Weapon: &wp})
	if d.Flee != 12 {
		t.Fatalf("flee_bonus=12 應直接加進迴避率，got %v", d.Flee)
	}
}

func TestCompute_Weapon_CritDmgPct(t *testing.T) {
	cfg := DefaultConfig()
	wp := WeaponProfile{CritDmgPct: 9}
	d := Compute(cfg, ComputeInput{Weapon: &wp})
	if d.CritDmgPct != 9 {
		t.Fatalf("crit_dmg_pct=9 應直接加進 CritDmgPct，got %v", d.CritDmgPct)
	}
}

// TestCompute_Weapon_FloorsOnlyWhenEquipped 驗證「全部 floor 成整數」只在裝備武器時發生——
// 不裝備武器時既有測試（compute_test.go 全部案例）從未設定 Weapon，等同持續驗證 wp==nil 時
// ATK/MATK/DEF/MDEF/CritPct/Flee/CritDmgPct 完全不被本輪改動（不會被意外攔截去 floor）。這裡
// 額外用一個「本來就整數」的案例交叉確認 floor 不會把裝備武器時的整數值改壞。
func TestCompute_Weapon_FloorsOnlyWhenEquipped(t *testing.T) {
	cfg := DefaultConfig()
	withWeapon := Compute(cfg, ComputeInput{Stats: Stats{Str: 10}, Weapon: &WeaponProfile{}})
	withoutWeapon := Compute(cfg, ComputeInput{Stats: Stats{Str: 10}})
	if withWeapon.Atk != withoutWeapon.Atk {
		t.Fatalf("中性武器（全零 profile）不應改變 ATK 數值本身，with=%v without=%v", withWeapon.Atk, withoutWeapon.Atk)
	}
}
