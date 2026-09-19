// presets_equipment_test.go：DORPG P9（CONTRACT §2/§3、WIRE）傭兵裝備——
// validatePresetEquipmentErrors／presetEquip／presetEquipmentEffects 純函式測試（不連 DB，
// resolvedPresetEquipmentSlot 直接手造，比照 equipment_test.go「留給 Neon 分支整合測試」的
// 既有慣例），以及 migration 186 系統預設腳本的裝備 item id 全部存在於 183/184 seed 的回歸測試。
package rpg

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// --- validatePresetEquipmentErrors ---

func strp(s string) *string { return &s }

// allValidEquipmentSlots 造一份「全部合法」的八格裝備（light_knight，Lv25）：武器/五部位防具
// job_id 都對，等級都夠，兩個飾品不同件——validatePresetEquipmentErrors 對這份輸入必須回空陣列。
func allValidEquipmentSlots() []resolvedPresetEquipmentSlot {
	jobID := "light_knight"
	armor := func(slot, id string) *ArmorRow {
		return &ArmorRow{ID: id, JobID: &jobID, Slot: slot, LevelReq: 20, IsActive: true}
	}
	return []resolvedPresetEquipmentSlot{
		{Slot: "weapon", ItemID: "lk_sword_t3",
			Weapon:     &WeaponRow{ID: "lk_sword_t3", TypeID: "lk_sword", LevelReq: 20, IsActive: true},
			WeaponType: &WeaponTypeRow{ID: "lk_sword", JobID: jobID}},
		{Slot: "helmet", ItemID: "lk_helmet_t3", Armor: armor("helmet", "lk_helmet_t3")},
		{Slot: "gloves", ItemID: "lk_gloves_t3", Armor: armor("gloves", "lk_gloves_t3")},
		{Slot: "armor", ItemID: "lk_armor_t3", Armor: armor("armor", "lk_armor_t3")},
		{Slot: "legs", ItemID: "lk_legs_t3", Armor: armor("legs", "lk_legs_t3")},
		{Slot: "boots", ItemID: "lk_boots_t3", Armor: armor("boots", "lk_boots_t3")},
		{Slot: "accessory1", ItemID: "acc_a", Armor: &ArmorRow{ID: "acc_a", JobID: nil, Slot: "accessory", LevelReq: 1, IsActive: true}},
		{Slot: "accessory2", ItemID: "acc_b", Armor: &ArmorRow{ID: "acc_b", JobID: nil, Slot: "accessory", LevelReq: 1, IsActive: true}},
	}
}

func TestValidatePresetEquipmentErrors_AllValidNoErrors(t *testing.T) {
	errs := validatePresetEquipmentErrors(allValidEquipmentSlots(), "light_knight", 25)
	if len(errs) != 0 {
		t.Fatalf("全部合法的裝備不應有錯誤：%+v", errs)
	}
}

func TestValidatePresetEquipmentErrors_EmptySlotsNoErrors(t *testing.T) {
	slots := []resolvedPresetEquipmentSlot{
		{Slot: "weapon"}, {Slot: "helmet"}, {Slot: "gloves"}, {Slot: "armor"},
		{Slot: "legs"}, {Slot: "boots"}, {Slot: "accessory1"}, {Slot: "accessory2"},
	}
	errs := validatePresetEquipmentErrors(slots, "light_knight", 25)
	if len(errs) != 0 {
		t.Fatalf("完全沒有配置裝備不應有錯誤：%+v", errs)
	}
}

func TestValidatePresetEquipmentErrors_NotFoundWeapon(t *testing.T) {
	slots := allValidEquipmentSlots()
	slots[0] = resolvedPresetEquipmentSlot{Slot: "weapon", ItemID: "ghost_sword", NotFound: true}
	errs := validatePresetEquipmentErrors(slots, "light_knight", 25)
	e := findEquipError(errs, "equipment.weapon")
	if e == nil || e.Code != "not_found" {
		t.Fatalf("查無武器應回 equipment.weapon/not_found，got %+v", errs)
	}
}

func TestValidatePresetEquipmentErrors_NotFoundArmor(t *testing.T) {
	slots := allValidEquipmentSlots()
	slots[1] = resolvedPresetEquipmentSlot{Slot: "helmet", ItemID: "ghost_helmet", NotFound: true}
	errs := validatePresetEquipmentErrors(slots, "light_knight", 25)
	e := findEquipError(errs, "equipment.helmet")
	if e == nil || e.Code != "not_found" {
		t.Fatalf("查無防具應回 equipment.helmet/not_found，got %+v", errs)
	}
}

func TestValidatePresetEquipmentErrors_WrongJobWeapon(t *testing.T) {
	slots := allValidEquipmentSlots()
	slots[0].WeaponType = &WeaponTypeRow{ID: "lk_sword", JobID: "heavy_knight"} // 武器類型屬於別的職業
	errs := validatePresetEquipmentErrors(slots, "light_knight", 25)
	e := findEquipError(errs, "equipment.weapon")
	if e == nil || e.Code != "wrong_job" {
		t.Fatalf("武器類型 job 不符應回 wrong_job，got %+v", errs)
	}
}

func TestValidatePresetEquipmentErrors_WrongSlotArmor(t *testing.T) {
	slots := allValidEquipmentSlots()
	jobID := "light_knight"
	// 把一件 slot="gloves" 的防具塞進 "helmet" 格。
	slots[1] = resolvedPresetEquipmentSlot{Slot: "helmet", ItemID: "lk_gloves_t3",
		Armor: &ArmorRow{ID: "lk_gloves_t3", JobID: &jobID, Slot: "gloves", LevelReq: 20, IsActive: true}}
	errs := validatePresetEquipmentErrors(slots, "light_knight", 25)
	e := findEquipError(errs, "equipment.helmet")
	if e == nil || e.Code != "wrong_slot" {
		t.Fatalf("防具 slot 不符應回 wrong_slot，got %+v", errs)
	}
}

func TestValidatePresetEquipmentErrors_LevelTooLow(t *testing.T) {
	slots := allValidEquipmentSlots()
	slots[0].Weapon.LevelReq = 50 // 腳本等級 25 < 50
	errs := validatePresetEquipmentErrors(slots, "light_knight", 25)
	e := findEquipError(errs, "equipment.weapon")
	if e == nil || e.Code != "level_too_low" {
		t.Fatalf("等級不足應回 level_too_low，got %+v", errs)
	}
}

func TestValidatePresetEquipmentErrors_DuplicateAccessory(t *testing.T) {
	slots := allValidEquipmentSlots()
	dup := &ArmorRow{ID: "acc_a", JobID: nil, Slot: "accessory", LevelReq: 1, IsActive: true}
	slots[6] = resolvedPresetEquipmentSlot{Slot: "accessory1", ItemID: "acc_a", Armor: dup}
	slots[7] = resolvedPresetEquipmentSlot{Slot: "accessory2", ItemID: "acc_a", Armor: dup}
	errs := validatePresetEquipmentErrors(slots, "light_knight", 25)
	e := findEquipError(errs, "equipment.accessory2")
	if e == nil || e.Code != "duplicate_accessory" {
		t.Fatalf("兩格飾品裝同一件應回 duplicate_accessory，got %+v", errs)
	}
}

func findEquipError(errs []PresetError, field string) *PresetError {
	for i := range errs {
		if errs[i].Field == field {
			return &errs[i]
		}
	}
	return nil
}

// --- resolvePresetEquipmentSlots（2026-09-19 P9 N+1 修復：改吃 loadGearCatalog 查好的
// map[string]WeaponWithType／map[string]ArmorRow，不再逐格打 DB，見 presets.go 檔頭說明）---

// TestResolvePresetEquipmentSlots_CatalogVersionMatchesPerSlotVersion 用假 catalog 餵
// resolvePresetEquipmentSlots，驗證結果與 allValidEquipmentSlots()（原本逐格查詢版會產生的
// 手造結果，同一組 item id／資料）逐位元一致——確保「改批次查詢」這個純粹的效能重構沒有連帶
// 改變任何一格的解析結果。
func TestResolvePresetEquipmentSlots_CatalogVersionMatchesPerSlotVersion(t *testing.T) {
	jobID := "light_knight"
	eq := PresetEquipment{
		Weapon: strp("lk_sword_t3"), Helmet: strp("lk_helmet_t3"), Gloves: strp("lk_gloves_t3"),
		Armor: strp("lk_armor_t3"), Legs: strp("lk_legs_t3"), Boots: strp("lk_boots_t3"),
		Accessory1: strp("acc_a"), Accessory2: strp("acc_b"),
	}
	weapons := map[string]WeaponWithType{
		"lk_sword_t3": {
			Weapon: WeaponRow{ID: "lk_sword_t3", TypeID: "lk_sword", LevelReq: 20, IsActive: true},
			Type:   WeaponTypeRow{ID: "lk_sword", JobID: jobID},
		},
	}
	armorOf := func(slot, id string) ArmorRow {
		return ArmorRow{ID: id, JobID: &jobID, Slot: slot, LevelReq: 20, IsActive: true}
	}
	armor := map[string]ArmorRow{
		"lk_helmet_t3": armorOf("helmet", "lk_helmet_t3"),
		"lk_gloves_t3": armorOf("gloves", "lk_gloves_t3"),
		"lk_armor_t3":  armorOf("armor", "lk_armor_t3"),
		"lk_legs_t3":   armorOf("legs", "lk_legs_t3"),
		"lk_boots_t3":  armorOf("boots", "lk_boots_t3"),
		"acc_a":        {ID: "acc_a", JobID: nil, Slot: "accessory", LevelReq: 1, IsActive: true},
		"acc_b":        {ID: "acc_b", JobID: nil, Slot: "accessory", LevelReq: 1, IsActive: true},
	}

	got := resolvePresetEquipmentSlots(eq, weapons, armor)
	want := allValidEquipmentSlots()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog 版 resolvePresetEquipmentSlots 應與逐格查詢版一致：got %+v want %+v", got, want)
	}
}

// TestResolvePresetEquipmentSlots_MissingFromCatalogIsNotFound：item_id 有值但 catalog 裡沒有
// （物品被後台下架刪除，或呼叫端收集 id 時漏收）視為未裝備（NotFound=true），不是錯誤——呼叫端
// 不應因此 500，只記一行 log（見 resolvePresetEquipmentSlots 實作）。
func TestResolvePresetEquipmentSlots_MissingFromCatalogIsNotFound(t *testing.T) {
	eq := PresetEquipment{Weapon: strp("ghost_sword"), Helmet: strp("ghost_helmet")}
	got := resolvePresetEquipmentSlots(eq, map[string]WeaponWithType{}, map[string]ArmorRow{})
	w := got[0]
	if w.Slot != "weapon" || !w.NotFound || w.Weapon != nil {
		t.Fatalf("查無武器應標記 NotFound=true 且 Weapon=nil，got %+v", w)
	}
	helmet := got[1]
	if helmet.Slot != "helmet" || !helmet.NotFound || helmet.Armor != nil {
		t.Fatalf("查無防具應標記 NotFound=true 且 Armor=nil，got %+v", helmet)
	}
}

// TestResolvePresetEquipmentSlots_EmptyEquipmentSkipsCatalogLookup：八格全空時每格都應是零值，
// 完全不查 catalog（傳 nil map 也不能 panic）。
func TestResolvePresetEquipmentSlots_EmptyEquipmentSkipsCatalogLookup(t *testing.T) {
	got := resolvePresetEquipmentSlots(PresetEquipment{}, nil, nil)
	if len(got) != len(presetEquipmentSlotOrder) {
		t.Fatalf("應輸出全部八格，got %d 格", len(got))
	}
	for _, s := range got {
		if s.ItemID != "" || s.NotFound || s.Weapon != nil || s.Armor != nil {
			t.Fatalf("沒有配置裝備的格子應是全零值，got %+v", s)
		}
	}
}

// --- presetEquip／presetEquipmentEffects ---

func TestPresetEquip_EmptyReturnsNil(t *testing.T) {
	slots := []resolvedPresetEquipmentSlot{
		{Slot: "weapon"}, {Slot: "helmet"}, {Slot: "gloves"}, {Slot: "armor"},
		{Slot: "legs"}, {Slot: "boots"}, {Slot: "accessory1"}, {Slot: "accessory2"},
	}
	if b := presetEquip(slots); b != nil {
		t.Fatalf("完全沒有裝備應回 nil（CONTRACT「equipment 空時輸出逐位元同 P6」），got %+v", *b)
	}
}

func TestPresetEquip_NotFoundIgnoredLikeEmpty(t *testing.T) {
	slots := []resolvedPresetEquipmentSlot{
		{Slot: "weapon", ItemID: "ghost", NotFound: true},
		{Slot: "helmet"}, {Slot: "gloves"}, {Slot: "armor"},
		{Slot: "legs"}, {Slot: "boots"}, {Slot: "accessory1"}, {Slot: "accessory2"},
	}
	if b := presetEquip(slots); b != nil {
		t.Fatalf("查無資料的格子應視同沒有裝備，回 nil，got %+v", *b)
	}
}

func TestPresetEquip_AggregatesWeaponAndArmor(t *testing.T) {
	jobID := "cleric"
	slots := []resolvedPresetEquipmentSlot{
		{Slot: "weapon", ItemID: "w1", Weapon: &WeaponRow{ID: "w1", Profile: WeaponProfile{Atk: 10, IntBonus: 4}}},
		{Slot: "helmet", ItemID: "h1", Armor: &ArmorRow{ID: "h1", JobID: &jobID, Slot: "helmet", Profile: ArmorProfile{Def: 3, Int: 2}}},
		{Slot: "gloves"}, {Slot: "armor"}, {Slot: "legs"}, {Slot: "boots"}, {Slot: "accessory1"}, {Slot: "accessory2"},
	}
	b := presetEquip(slots)
	if b == nil {
		t.Fatalf("有裝備時不應回 nil")
	}
	want := AggregateEquipment(&WeaponProfile{Atk: 10, IntBonus: 4}, []ArmorProfile{{Def: 3, Int: 2}})
	if *b != want {
		t.Fatalf("彙總結果應等同直接呼叫 AggregateEquipment，got %+v want %+v", *b, want)
	}
}

func TestPresetEquipmentEffects_ExcludesWeapon(t *testing.T) {
	jobID := "cleric"
	slots := []resolvedPresetEquipmentSlot{
		{Slot: "weapon", ItemID: "w1", Weapon: &WeaponRow{ID: "w1", Profile: WeaponProfile{Atk: 999}}},
		{Slot: "helmet", ItemID: "h1", Armor: &ArmorRow{ID: "h1", JobID: &jobID, Slot: "helmet", Profile: ArmorProfile{IntervalPct: -5}}},
		{Slot: "gloves"}, {Slot: "armor"}, {Slot: "legs"}, {Slot: "boots"}, {Slot: "accessory1"}, {Slot: "accessory2"},
	}
	eff := presetEquipmentEffects(slots)
	if eff.Atk != 0 {
		t.Fatalf("equipmentEffects 不應包含武器貢獻（武器仍走 weapon.profile），got Atk=%v", eff.Atk)
	}
	if eff.IntervalPct != -5 {
		t.Fatalf("equipmentEffects 應包含防具貢獻，got IntervalPct=%v", eff.IntervalPct)
	}
}

// --- computeCompanionDerived：equipment 空時逐位元同 P6（CONTRACT §3/§6）---

func TestComputeCompanionDerived_NilEquipMatchesDirectComputeCall(t *testing.T) {
	cfg := DefaultConfig()
	job := JobRow{ID: "cleric", AtkBranch: "melee"}
	skills := clericPathASkills()
	stats := Stats{Str: 1, Agi: 1, Vit: 10, Dex: 20, Int: 25, Luk: 5}
	skillLevels := map[string]int{"cl_a1": 5, "cl_a2": 5}

	got := computeCompanionDerived(cfg, job, skills, 25, stats, skillLevels, nil)
	want := Compute(cfg, ComputeInput{
		BaseLevel: 25, Stats: stats, WeaponType: job.AtkBranch,
		Passives: passivesFromSkillLevels(skills, skillLevels),
	}) // 刻意不設 Equip（維持 P6 之前 nil 的既有行為）
	// Derived 內含 map（Resists/NextCost），不可比較，用 reflect.DeepEqual。
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("equip=nil 應與 P6（沒有裝備概念）逐位元一致：got %+v want %+v", got, want)
	}
}

func TestComputeCompanionDerived_EmptyEquipmentResolvesToNilAndMatchesP6(t *testing.T) {
	cfg := DefaultConfig()
	job := JobRow{ID: "cleric", AtkBranch: "melee"}
	skills := clericPathASkills()
	stats := Stats{Str: 1, Agi: 1, Vit: 10, Dex: 20, Int: 25, Luk: 5}
	skillLevels := map[string]int{"cl_a1": 5, "cl_a2": 5}

	emptySlots := []resolvedPresetEquipmentSlot{
		{Slot: "weapon"}, {Slot: "helmet"}, {Slot: "gloves"}, {Slot: "armor"},
		{Slot: "legs"}, {Slot: "boots"}, {Slot: "accessory1"}, {Slot: "accessory2"},
	}
	equip := presetEquip(emptySlots)
	if equip != nil {
		t.Fatalf("空 equipment 應解析成 nil，got %+v", *equip)
	}
	got := computeCompanionDerived(cfg, job, skills, 25, stats, skillLevels, equip)
	want := computeCompanionDerived(cfg, job, skills, 25, stats, skillLevels, nil)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("presetEquip() 對空 equipment 的結果餵進 Compute 應與直接傳 nil 逐位元一致：got %+v want %+v", got, want)
	}
}

// --- migration 186 系統預設腳本的裝備 item id 全部存在於 183/184 seed（CONTRACT §2）---

// migration186CompanionEquipmentIDs 對照 186_rpg_mercenary_gear_ai.sql 四筆 UPDATE 用到的
// item id（單一真相：改一邊要記得改另一邊，這個測試就是防止兩邊漂移的保險，比照
// presets_test.go 對 migration 181 seed 的既有慣例）。
func migration186CompanionEquipmentIDs() map[string][]string {
	return map[string][]string{
		"183_rpg_weapons_elements.sql": {
			"cl_staff_t3", "ar_longbow_t3", "lk_sword_t3", "hk_greatsword_t3",
		},
		"184_rpg_armor.sql": {
			"cl_helmet_t3", "cl_gloves_t3", "cl_armor_t3", "cl_legs_t3", "cl_boots_t3", "acc_mpregen_t2", "acc_mp_t2",
			"ar_helmet_t3", "ar_gloves_t3", "ar_armor_t3", "ar_legs_t3", "ar_boots_t3", "acc_crit_t2", "acc_agi_t2",
			"lk_helmet_t3", "lk_gloves_t3", "lk_armor_t3", "lk_legs_t3", "lk_boots_t3", "acc_dmgtaken_t2", "acc_hp_t2",
			"hk_helmet_t3", "hk_gloves_t3", "hk_armor_t3", "hk_legs_t3", "hk_boots_t3", "acc_vit_t2",
		},
	}
}

func TestSystemPresetEquipment_ItemIDsExistInMigrationSeeds(t *testing.T) {
	for file, ids := range migration186CompanionEquipmentIDs() {
		path := "../../migrations/" + file
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("讀取 %s 失敗：%v（migration 186 的裝備 id 必須對照這份既有 seed）", path, err)
		}
		content := string(raw)
		for _, id := range ids {
			t.Run(file+"/"+id, func(t *testing.T) {
				needle := "'" + id + "',"
				if !strings.Contains(content, needle) {
					t.Fatalf("%s 找不到 id=%s（migration 186 seed 用到的 item id 必須真的存在於 %s）", file, id, file)
				}
			})
		}
	}
}
