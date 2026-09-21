// p13_test.go：DORPG P13（docs/dorpg/P13_CONTRACT.md）——武器類型 traits 新增 reach 鍵
// （melee／ranged）的純函式解析（weaponTypeRowBonus 併入 Reach）、WeaponProfileWire 合併
// （withRowBonus／buildPlayerWeaponWire／buildCompanionWeaponWire），以及 migration 190 seed
// 的 18 筆 id 與分類自我核對（比照 183 seed）。不連 DB（比照 p10_test.go／p12_test.go 既有
// 慣例）；readMigrationFile 是 p10_test.go 已定義的套件層級 helper，這裡直接沿用。
//
// 「前排阻擋」的判定本身（isTargetBlocked／frontAlive）是 ENGINE（TypeScript）的事，不在本檔
// 範圍——這裡只驗證 BACKEND 負責的那一段：traits.reach 怎麼解析、怎麼合併進 wire、seed 對不對。

package rpg

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// weaponTypeRowBonus 的 Reach 解析：這一層刻意只「原樣讀出」，不做缺省收斂（缺省＝melee的
// 收斂點在下一節的 withRowBonus——理由見 weapons.go WeaponTypeRowBonus.Reach 欄位註解：P12
// 既有測試把 WeaponTypeRowBonus{} 零值當「完全沒有這些鍵」的判斷基準，Reach 的零值就是空
// 字串，若在這層就把缺省收斂成 "melee" 會讓那些非本輪所有權的既有測試改變語意而失敗）。
// ---------------------------------------------------------------------------

func TestWeaponTypeRowBonus_ReachRangedPassesThrough(t *testing.T) {
	traits := map[string]any{"reach": "ranged"}
	got := weaponTypeRowBonus(traits)
	if got.Reach != weaponReachRanged {
		t.Fatalf("reach=ranged 應原樣讀出，got %q", got.Reach)
	}
}

func TestWeaponTypeRowBonus_ReachMeleeExplicitPassesThrough(t *testing.T) {
	traits := map[string]any{"reach": "melee"}
	got := weaponTypeRowBonus(traits)
	if got.Reach != weaponReachMelee {
		t.Fatalf("reach=melee 應原樣讀出，got %q", got.Reach)
	}
}

func TestWeaponTypeRowBonus_ReachMissingIsRawEmpty(t *testing.T) {
	// 沒有 reach 鍵：這一層回傳空字串（零值），不在這裡就收斂成 melee——跟 P12 既有測試
	// 「四鍵缺省時整個 struct 等於 WeaponTypeRowBonus{}」的判斷基準一致。
	traits := map[string]any{"style": "均衡型"}
	got := weaponTypeRowBonus(traits)
	if got.Reach != "" {
		t.Fatalf("缺省這一層應為原始空字串，got %q", got.Reach)
	}
}

func TestWeaponTypeRowBonus_ReachNilTraitsIsRawEmpty(t *testing.T) {
	got := weaponTypeRowBonus(nil)
	if got.Reach != "" {
		t.Fatalf("nil traits 不應 panic，這一層應回原始空字串，got %q", got.Reach)
	}
}

func TestWeaponTypeRowBonus_ReachInvalidValueIsRawEmpty(t *testing.T) {
	// 後台 traits JSON 可自由編輯，打錯字（typo）、大小寫不符、非字串型別，這一層一律視為
	// 沒有這個鍵（空字串），保守收斂成 melee 交給 withRowBonus。
	cases := []any{"Ranged", "RANGED", "flying", "", 1.0, true, map[string]any{}, []any{"ranged"}}
	for _, v := range cases {
		got := weaponTypeRowBonus(map[string]any{"reach": v})
		if got.Reach != "" {
			t.Errorf("非法值 %#v 這一層應為空字串，got %q", v, got.Reach)
		}
	}
}

func TestWeaponTypeRowBonus_ReachDoesNotAffectOtherFields(t *testing.T) {
	// Reach 跟 P12 四個數字鍵同一次解析、同一個 struct，互不干擾。
	traits := map[string]any{"reach": "ranged", "row_bonus_rear_pct": 10.0}
	got := weaponTypeRowBonus(traits)
	if got.Reach != weaponReachRanged || got.RowBonusRearPct != 10 {
		t.Fatalf("reach 與 row_bonus 應互不影響，got %+v", got)
	}
}

// ---------------------------------------------------------------------------
// withRowBonus：真正「缺省＝melee」的收斂點——把 weaponTypeRowBonus 的原始值（含空字串／
// 任何非 "ranged" 的值）收斂成 WeaponProfileWire.Reach 承諾的 "melee"|"ranged" 兩個字面值。
// ---------------------------------------------------------------------------

func TestWithRowBonus_ResolvesRangedReach(t *testing.T) {
	base := ToWeaponProfileWire(DefaultWeaponProfile())
	got := base.withRowBonus(WeaponTypeRowBonus{Reach: weaponReachRanged})
	if got.Reach != weaponReachRanged {
		t.Fatalf("Reach=ranged 應原樣送出，got %q", got.Reach)
	}
}

func TestWithRowBonus_ResolvesEmptyReachToMelee(t *testing.T) {
	// 對應「未設定」的情境：weaponTypeRowBonus 回傳空字串，withRowBonus 收斂成 melee。
	base := ToWeaponProfileWire(DefaultWeaponProfile())
	got := base.withRowBonus(WeaponTypeRowBonus{Reach: ""})
	if got.Reach != weaponReachMelee {
		t.Fatalf("空字串應收斂成 melee，got %q", got.Reach)
	}
}

func TestWithRowBonus_ResolvesExplicitMeleeToMelee(t *testing.T) {
	base := ToWeaponProfileWire(DefaultWeaponProfile())
	got := base.withRowBonus(WeaponTypeRowBonus{Reach: weaponReachMelee})
	if got.Reach != weaponReachMelee {
		t.Fatalf("Reach=melee 應原樣送出，got %q", got.Reach)
	}
}

// ---------------------------------------------------------------------------
// WeaponProfileWire.withRowBonus / buildPlayerWeaponWire / buildCompanionWeaponWire：Reach
// 合併進 wire（玩家與傭兵裝備武器共用 buildPlayerWeaponWire；傭兵固定視覺走
// buildCompanionWeaponWire，沒有武器類型可查，明講填 melee）。
// ---------------------------------------------------------------------------

func TestWithRowBonus_MergesReachLeavesRestUnchanged(t *testing.T) {
	base := ToWeaponProfileWire(WeaponProfile{Atk: 42, Element: "fire"})
	merged := base.withRowBonus(WeaponTypeRowBonus{Reach: weaponReachRanged, RowBonusRearPct: 10})
	if merged.Atk != 42 || merged.Element != "fire" {
		t.Fatalf("既有欄位不應被動到，got %+v", merged)
	}
	if merged.Reach != weaponReachRanged || merged.RowBonusRearPct != 10 {
		t.Fatalf("Reach 應併入且不影響既有 RowBonus 合併，got %+v", merged)
	}
}

func TestBuildPlayerWeaponWire_RangedTypeMergesReach(t *testing.T) {
	w := WeaponRow{ID: "ar_longbow_t3", Name: "路跑蓄力弓", TypeID: "ar_longbow", Profile: WeaponProfile{Atk: 30}}
	wt := WeaponTypeRow{ID: "ar_longbow", Visual: "bow", Traits: map[string]any{"reach": "ranged", "row_bonus_rear_pct": 10.0}}
	got := buildPlayerWeaponWire(&w, &wt)
	if got == nil {
		t.Fatal("已裝備應回非 nil")
	}
	if got.Profile.Reach != weaponReachRanged {
		t.Fatalf("弓應為 ranged，got %q", got.Profile.Reach)
	}
}

func TestBuildPlayerWeaponWire_MeleeTypeMergesReach(t *testing.T) {
	w := WeaponRow{ID: "hk_greatsword_t1", Name: "測試巨劍", TypeID: "hk_greatsword", Profile: DefaultWeaponProfile()}
	wt := WeaponTypeRow{ID: "hk_greatsword", Visual: "greatsword", Traits: map[string]any{"reach": "melee"}}
	got := buildPlayerWeaponWire(&w, &wt)
	if got.Profile.Reach != weaponReachMelee {
		t.Fatalf("巨劍應為 melee，got %q", got.Profile.Reach)
	}
}

func TestBuildPlayerWeaponWire_NoReachTraitDefaultsToMelee(t *testing.T) {
	// 190 套用前（或後台漏標）：沒有 reach 鍵的既有武器類型，合併後應保守回 melee，不是空字串。
	w := WeaponRow{ID: "lk_sword_t1", Name: "測試劍", TypeID: "lk_sword", Profile: DefaultWeaponProfile()}
	wt := WeaponTypeRow{ID: "lk_sword", Visual: "sword", Traits: map[string]any{"style": "均衡型"}}
	got := buildPlayerWeaponWire(&w, &wt)
	if got.Profile.Reach != weaponReachMelee {
		t.Fatalf("沒有 reach 鍵應保守回 melee，got %q", got.Profile.Reach)
	}
}

func TestBuildCompanionWeaponWire_FallbackVisualReachIsMelee(t *testing.T) {
	// 傭兵未裝備武器的固定視覺退回路徑（沒有 wtype 可合併）——CONTRACT §2「未裝備武器（徒手）
	// ＝melee」，且 wire 型別承諾只有 melee|ranged 兩種字面值，不應是空字串零值。
	got := buildCompanionWeaponWire("staff")
	if got.Profile.Reach != weaponReachMelee {
		t.Fatalf("徒手退回視覺應為 melee，got %q", got.Profile.Reach)
	}
}

// ---------------------------------------------------------------------------
// migration 190 seed 自我核對：18 筆 type id 存在於 183 seed（單一真相防漂移，比照
// p12_test.go TestMigration188_SevenWeaponTypeIDsExistIn183Seed 既有慣例），且分類（melee／
// ranged）與 CONTRACT §2 一致。
// ---------------------------------------------------------------------------

var (
	p13RangedTypeIDs = []string{
		"ar_longbow", "ar_shortbow", "ar_crossbow",
		"mg_staff", "mg_rod", "mg_book",
		"cl_staff", "cl_book",
	}
	p13MeleeTypeIDs = []string{
		"lk_sword", "lk_dual", "lk_rapier",
		"hk_greatsword", "hk_spear", "hk_axe",
		"mc_hammer", "mc_mallet", "mc_club",
		"cl_chain",
	}
)

func TestP13_EighteenTypeIDsExistIn183Seed(t *testing.T) {
	seed183 := readMigrationFile(t, "183_rpg_weapons_elements.sql")
	all := append(append([]string{}, p13RangedTypeIDs...), p13MeleeTypeIDs...)
	if len(all) != 18 {
		t.Fatalf("CONTRACT §2 講的是 18 種武器類型，本檔清單卻有 %d 筆——先修測試清單", len(all))
	}
	for _, id := range all {
		// 比照 p12_test.go：用單引號包住的字面比對，避免跟 'ar_longbow_t1' 這種以此為前綴的
		// rpg_weapons id 誤判命中。
		if !strings.Contains(seed183, "('"+id+"',") {
			t.Errorf("183 的 rpg_weapon_types INSERT 應含 %s，190 才有東西可 UPDATE", id)
		}
	}
}

func TestMigration190_RangedIDsGetRangedReach(t *testing.T) {
	content := readMigrationFile(t, "190_rpg_p13_reach.sql")
	// ranged 那個 UPDATE 區塊：SET 到下一個分號之間應含 "reach": "ranged" 且列出全部 8 個 id。
	rangedBlockStart := strings.Index(content, `SET traits = traits || '{"reach": "ranged"}'`)
	if rangedBlockStart < 0 {
		t.Fatal("190 應有一段 SET traits ... reach: ranged 的 UPDATE")
	}
	rangedBlockEnd := strings.Index(content[rangedBlockStart:], ";")
	if rangedBlockEnd < 0 {
		t.Fatal("190 的 ranged UPDATE 區塊應以分號結尾")
	}
	rangedBlock := content[rangedBlockStart : rangedBlockStart+rangedBlockEnd]
	for _, id := range p13RangedTypeIDs {
		if !strings.Contains(rangedBlock, "'"+id+"'") {
			t.Errorf("190 的 ranged UPDATE 區塊應含 %s", id)
		}
	}
	for _, id := range p13MeleeTypeIDs {
		if strings.Contains(rangedBlock, "'"+id+"'") {
			t.Errorf("190 的 ranged UPDATE 區塊不應誤含 melee id %s", id)
		}
	}
}

func TestMigration190_MeleeIDsGetMeleeReach(t *testing.T) {
	content := readMigrationFile(t, "190_rpg_p13_reach.sql")
	meleeBlockStart := strings.Index(content, `SET traits = traits || '{"reach": "melee"}'`)
	if meleeBlockStart < 0 {
		t.Fatal("190 應有一段 SET traits ... reach: melee 的 UPDATE")
	}
	meleeBlockEnd := strings.Index(content[meleeBlockStart:], ";")
	if meleeBlockEnd < 0 {
		t.Fatal("190 的 melee UPDATE 區塊應以分號結尾")
	}
	meleeBlock := content[meleeBlockStart : meleeBlockStart+meleeBlockEnd]
	for _, id := range p13MeleeTypeIDs {
		if !strings.Contains(meleeBlock, "'"+id+"'") {
			t.Errorf("190 的 melee UPDATE 區塊應含 %s", id)
		}
	}
	for _, id := range p13RangedTypeIDs {
		if strings.Contains(meleeBlock, "'"+id+"'") {
			t.Errorf("190 的 melee UPDATE 區塊不應誤含 ranged id %s", id)
		}
	}
}

func TestMigration190_IsIdempotentGuardedAndRecordsVersion(t *testing.T) {
	content := readMigrationFile(t, "190_rpg_p13_reach.sql")
	if strings.Count(content, "NOT (traits @>") != 2 {
		t.Errorf("190 應有兩段（ranged／melee）用 NOT (traits @> …) 擋重複套用，got %d 段", strings.Count(content, "NOT (traits @>"))
	}
	if !strings.Contains(content, "'190'") {
		t.Errorf("190 應在檔尾寫入 schema_migrations")
	}
}
