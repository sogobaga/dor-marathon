// p12_test.go：DORPG P12（scratchpad/dorpg_p12/CONTRACT.md）——武器類型 traits 新增四鍵
// （row_bonus_front_pct／row_bonus_rear_pct／pierce_chance_pct／pierce_dmg_pct）的純函式解析
// （weaponTypeRowBonus）、WeaponProfileWire 合併（withRowBonus／buildPlayerWeaponWire）、怪物
// 排位推導（enemyRowFromSlot），以及 migration 188 seed 的自我核對。不連 DB（比照 p10_test.go
// 既有慣例）；readMigrationFile 是 p10_test.go 已定義的套件層級 helper，這裡直接沿用。

package rpg

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// weaponTypeRowBonus：traits 四鍵解析（正常／缺省／型別不符／負值/超 100）。
// ---------------------------------------------------------------------------

func TestWeaponTypeRowBonus_AllFourKeysPresent(t *testing.T) {
	traits := map[string]any{
		"row_bonus_front_pct": 10.0,
		"row_bonus_rear_pct":  10.0,
		"pierce_chance_pct":   30.0,
		"pierce_dmg_pct":      50.0,
	}
	got := weaponTypeRowBonus(traits)
	want := WeaponTypeRowBonus{RowBonusFrontPct: 10, RowBonusRearPct: 10, PierceChancePct: 30, PierceDmgPct: 50}
	if got != want {
		t.Fatalf("四鍵齊全應原樣讀出，got %+v want %+v", got, want)
	}
}

func TestWeaponTypeRowBonus_MissingKeysDefaultToZero(t *testing.T) {
	// 其餘職業（例如 lk_sword）沒有這四個鍵，也可能還帶 P7 舊描述鍵（style/special/…）——
	// 這裡混一個舊鍵確認不受影響。
	traits := map[string]any{"style": "重擊型", "positioning": "物理攻擊力"}
	got := weaponTypeRowBonus(traits)
	if got != (WeaponTypeRowBonus{}) {
		t.Fatalf("缺省四鍵應全為 0，got %+v", got)
	}
}

func TestWeaponTypeRowBonus_NilTraitsDefaultToZero(t *testing.T) {
	if got := weaponTypeRowBonus(nil); got != (WeaponTypeRowBonus{}) {
		t.Fatalf("nil traits 不應 panic，應回全 0，got %+v", got)
	}
}

func TestWeaponTypeRowBonus_NonNumericValueTreatedAsZero(t *testing.T) {
	// 後台 traits JSON 可自由編輯，打錯型別（字串）不該讓戰鬥 bootstrap 500，只當缺省處理。
	traits := map[string]any{"row_bonus_rear_pct": "10", "pierce_chance_pct": true}
	got := weaponTypeRowBonus(traits)
	if got != (WeaponTypeRowBonus{}) {
		t.Fatalf("非數值型別應視為缺省 0，got %+v", got)
	}
}

func TestWeaponTypeRowBonus_NegativeValueClampedToZero(t *testing.T) {
	traits := map[string]any{"row_bonus_front_pct": -5.0, "pierce_dmg_pct": -50.0}
	got := weaponTypeRowBonus(traits)
	if got.RowBonusFrontPct != 0 || got.PierceDmgPct != 0 {
		t.Fatalf("負值應歸零，got %+v", got)
	}
}

func TestWeaponTypeRowBonus_OverHundredClampedToHundred(t *testing.T) {
	traits := map[string]any{"pierce_chance_pct": 150.0, "row_bonus_rear_pct": 999.0}
	got := weaponTypeRowBonus(traits)
	if got.PierceChancePct != 100 || got.RowBonusRearPct != 100 {
		t.Fatalf("pct 應夾在 0..100，got %+v", got)
	}
}

func TestWeaponTypeRowBonus_ExactlyHundredUnchanged(t *testing.T) {
	traits := map[string]any{"pierce_chance_pct": 100.0}
	if got := weaponTypeRowBonus(traits); got.PierceChancePct != 100 {
		t.Fatalf("恰好 100 不應被誤夾成其他值，got %v", got.PierceChancePct)
	}
}

// ---------------------------------------------------------------------------
// WeaponProfileWire.withRowBonus / buildPlayerWeaponWire：合併進 wire（玩家與傭兵共用同一條
// 組裝路徑，見 battle.go buildPlayerWeaponWire 註解）。
// ---------------------------------------------------------------------------

func TestWithRowBonus_MergesFourFieldsLeavesRestUnchanged(t *testing.T) {
	base := ToWeaponProfileWire(WeaponProfile{Atk: 42, Element: "fire"})
	merged := base.withRowBonus(WeaponTypeRowBonus{RowBonusRearPct: 10})
	if merged.Atk != 42 || merged.Element != "fire" {
		t.Fatalf("既有欄位不應被動到，got %+v", merged)
	}
	if merged.RowBonusRearPct != 10 || merged.RowBonusFrontPct != 0 || merged.PierceChancePct != 0 || merged.PierceDmgPct != 0 {
		t.Fatalf("只有 RowBonusRearPct 應非 0，got %+v", merged)
	}
}

func TestBuildPlayerWeaponWire_MergesTypeTraitsRowBonus(t *testing.T) {
	w := WeaponRow{ID: "ar_longbow_t3", Name: "路跑蓄力弓", TypeID: "ar_longbow", Profile: WeaponProfile{Atk: 30}}
	wt := WeaponTypeRow{ID: "ar_longbow", Visual: "bow", Traits: map[string]any{"row_bonus_rear_pct": 10.0}}
	got := buildPlayerWeaponWire(&w, &wt)
	if got == nil {
		t.Fatal("已裝備應回非 nil")
	}
	if got.Profile.RowBonusRearPct != 10 {
		t.Fatalf("弓對後排 +10%%，got %+v", got.Profile)
	}
	if got.Profile.RowBonusFrontPct != 0 || got.Profile.PierceChancePct != 0 || got.Profile.PierceDmgPct != 0 {
		t.Fatalf("弓不應帶前排加成／貫穿，got %+v", got.Profile)
	}
	// 既有欄位（Atk）不應因為合併 traits 而漂移。
	if got.Profile.Atk != 30 {
		t.Fatalf("Atk 應維持武器本身的 30，got %v", got.Profile.Atk)
	}
}

func TestBuildPlayerWeaponWire_SpearMergesPierceFields(t *testing.T) {
	w := WeaponRow{ID: "hk_spear_t1", Name: "測試槍", TypeID: "hk_spear", Profile: DefaultWeaponProfile()}
	wt := WeaponTypeRow{ID: "hk_spear", Visual: "greatsword", Traits: map[string]any{"pierce_chance_pct": 30.0, "pierce_dmg_pct": 50.0}}
	got := buildPlayerWeaponWire(&w, &wt)
	if got.Profile.PierceChancePct != 30 || got.Profile.PierceDmgPct != 50 {
		t.Fatalf("槍應帶 30%% 機率／50%% 貫穿傷害，got %+v", got.Profile)
	}
}

func TestBuildPlayerWeaponWire_NoTraitsKeepsZeroRowBonus(t *testing.T) {
	// 沒有這四個鍵的武器類型（例如既有 lk_sword）：合併後應維持 0，不應該因為呼叫了合併函式
	// 就意外生出非零值。
	w := WeaponRow{ID: "lk_sword_t1", Name: "測試劍", TypeID: "lk_sword", Profile: DefaultWeaponProfile()}
	wt := WeaponTypeRow{ID: "lk_sword", Visual: "sword", Traits: map[string]any{"style": "均衡型"}}
	got := buildPlayerWeaponWire(&w, &wt)
	if got.Profile.RowBonusFrontPct != 0 || got.Profile.RowBonusRearPct != 0 || got.Profile.PierceChancePct != 0 || got.Profile.PierceDmgPct != 0 {
		t.Fatalf("沒有排位加成鍵應維持全 0，got %+v", got.Profile)
	}
}

func TestBuildCompanionWeaponWire_FallbackVisualHasZeroRowBonus(t *testing.T) {
	// 傭兵未裝備武器的固定視覺退回路徑（buildCompanionWeaponWire，沒有 wtype 可合併）——
	// 中性 profile 本來就全 0，新欄位比照既有欄位維持中性。
	got := buildCompanionWeaponWire("staff")
	if got.Profile.RowBonusFrontPct != 0 || got.Profile.PierceChancePct != 0 {
		t.Fatalf("退回視覺應維持中性 0，got %+v", got.Profile)
	}
}

// ---------------------------------------------------------------------------
// enemyRowFromSlot：五個既有槽位（front_left/front_center/front_right/rear_left/rear_right）。
// ---------------------------------------------------------------------------

func TestEnemyRowFromSlot_FrontSlots(t *testing.T) {
	for _, slot := range []string{"front_left", "front_center", "front_right"} {
		if got := enemyRowFromSlot(slot); got != "front" {
			t.Errorf("%s 應推導為 front，got %s", slot, got)
		}
	}
}

func TestEnemyRowFromSlot_RearSlots(t *testing.T) {
	for _, slot := range []string{"rear_left", "rear_right"} {
		if got := enemyRowFromSlot(slot); got != "rear" {
			t.Errorf("%s 應推導為 rear，got %s", slot, got)
		}
	}
}

func TestEnemyRowFromSlot_UnknownSlotFallsBackToFront(t *testing.T) {
	if got := enemyRowFromSlot("unknown_slot"); got != "front" {
		t.Fatalf("未知槽位應保守回 front，got %s", got)
	}
}

// ---------------------------------------------------------------------------
// migration 188 seed 自我核對：七筆 type id 存在於 183 seed（單一真相防漂移，比照
// p10_test.go TestMigration187_HKGuardianSkillIDsMatchPresetSeedFixture 既有慣例）；188 本身
// 內容正確、可重複執行的判斳交給 SQL 的 WHERE NOT (traits @> …) 冪等寫法，這裡只核對字面內容。
// ---------------------------------------------------------------------------

func TestMigration188_SevenWeaponTypeIDsExistIn183Seed(t *testing.T) {
	seed183 := readMigrationFile(t, "183_rpg_weapons_elements.sql")
	ids := []string{"ar_longbow", "ar_shortbow", "ar_crossbow", "mc_hammer", "mc_mallet", "mc_club", "hk_spear"}
	for _, id := range ids {
		// rpg_weapon_types 的 INSERT 每列以 ('<id>', ... 開頭；用單引號包住的字面比對，避免跟
		// 其他表（例如 rpg_weapons 裡 'ar_longbow_t1' 這種以此為前綴的 id）誤判命中。
		if !strings.Contains(seed183, "('"+id+"',") {
			t.Errorf("183 的 rpg_weapon_types INSERT 應含 %s，188 才有東西可 UPDATE", id)
		}
	}
}

func TestMigration188_ContainsAllFourTraitKeysForCorrectTypes(t *testing.T) {
	content := readMigrationFile(t, "188_rpg_p12_row_bonus.sql")
	cases := map[string]string{
		"ar_longbow":  "row_bonus_rear_pct",
		"ar_shortbow": "row_bonus_rear_pct",
		"ar_crossbow": "row_bonus_rear_pct",
		"mc_hammer":   "row_bonus_front_pct",
		"mc_mallet":   "row_bonus_front_pct",
		"mc_club":     "row_bonus_front_pct",
	}
	for id, key := range cases {
		if !strings.Contains(content, id) {
			t.Errorf("188 應提及 %s", id)
		}
		if !strings.Contains(content, key) {
			t.Errorf("188 應含鍵 %s（來自 %s 那組 UPDATE）", key, id)
		}
	}
	if !strings.Contains(content, "hk_spear") || !strings.Contains(content, "pierce_chance_pct") || !strings.Contains(content, "pierce_dmg_pct") {
		t.Errorf("188 應含 hk_spear 的 pierce_chance_pct/pierce_dmg_pct")
	}
	if !strings.Contains(content, "'188'") {
		t.Errorf("188 應在檔尾寫入 schema_migrations")
	}
}
