// armor_test.go：DORPG P8（CONTRACT §1/§2）ArmorProfile／AggregateEquipment／ArmorRow 的純函式
// 測試，不連 DB（比照 weapons_test.go 檔頭「連 DB 的部分留給 Neon 分支整合測試」的既有慣例）。
package rpg

import (
	"strings"
	"testing"
)

// --- ParseArmorProfile ---

func TestParseArmorProfile_EmptyIsNeutral(t *testing.T) {
	p, err := ParseArmorProfile(nil)
	if err != nil {
		t.Fatalf("空 raw 不應該報錯，got %v", err)
	}
	if p != (ArmorProfile{}) {
		t.Fatalf("空 raw 應該回全零中性值，got %+v", p)
	}
}

func TestParseArmorProfile_EmptyObjectIsNeutral(t *testing.T) {
	p, err := ParseArmorProfile([]byte(`{}`))
	if err != nil {
		t.Fatalf("空物件不應該報錯，got %v", err)
	}
	if p != (ArmorProfile{}) {
		t.Fatalf("空物件應該回全零中性值，got %+v", p)
	}
}

func TestParseArmorProfile_PartialFieldsDefaultRest(t *testing.T) {
	p, err := ParseArmorProfile([]byte(`{"def":12,"vit":3,"hp_pct":8}`))
	if err != nil {
		t.Fatalf("合法 JSON 不應該報錯，got %v", err)
	}
	if p.Def != 12 || p.Vit != 3 || p.HpPct != 8 {
		t.Fatalf("缺欄位應該補中性值，其餘欄位照給，got %+v", p)
	}
	if p.Str != 0 || p.CritPct != 0 || p.ElementResistPct != 0 {
		t.Fatalf("沒填的欄位應該維持零值，got %+v", p)
	}
}

// 錯誤案例一：JSON 語法本身壞掉。
func TestParseArmorProfile_InvalidJSONErrors(t *testing.T) {
	if _, err := ParseArmorProfile([]byte(`{not json`)); err == nil {
		t.Fatalf("壞掉的 JSON 應該報錯")
	}
}

// 錯誤案例二：interval_pct <= -100 會讓引擎除零/倒轉時間軸（比照 WeaponProfile.Validate 同款
// 邊界），ParseArmorProfile 必須擋下。
func TestParseArmorProfile_IntervalPctTooNegativeErrors(t *testing.T) {
	if _, err := ParseArmorProfile([]byte(`{"interval_pct":-100}`)); err == nil {
		t.Fatalf("interval_pct=-100 應該報錯（必須 > -100）")
	}
	if _, err := ParseArmorProfile([]byte(`{"interval_pct":-150}`)); err == nil {
		t.Fatalf("interval_pct=-150 應該報錯（必須 > -100）")
	}
}

// -100 以內（不含 -100）仍然合法，避免邊界檢查誤傷正常設計值。
func TestParseArmorProfile_IntervalPctJustAboveBoundaryOK(t *testing.T) {
	p, err := ParseArmorProfile([]byte(`{"interval_pct":-99}`))
	if err != nil {
		t.Fatalf("interval_pct=-99 不應該報錯，got %v", err)
	}
	if p.IntervalPct != -99 {
		t.Fatalf("interval_pct 應該原樣保留，got %v", p.IntervalPct)
	}
}

// --- ParseArmorProfileStrict（armor_admin.go 後台 PUT 專用） ---

// TestParseArmorProfileStrict_UnknownFieldErrors 根因修復回歸測試：後台 PUT 打錯欄位名（例如
// "dfe" 少一個字母）過去會被 ParseArmorProfile 靜默吃掉、回 200，存進一筆「什麼都不生效」的
// 防具。改用 ParseArmorProfileStrict 後應該直接報錯（400），訊息含欄位名方便後台立刻抓到打錯字。
func TestParseArmorProfileStrict_UnknownFieldErrors(t *testing.T) {
	_, err := ParseArmorProfileStrict([]byte(`{"def":5,"dfe":3}`))
	if err == nil {
		t.Fatalf("未知欄位 dfe 應該報錯（後台打錯字不該靜默失效）")
	}
	if !strings.Contains(err.Error(), "dfe") {
		t.Fatalf("錯誤訊息應該含欄位名 dfe，got %v", err)
	}
}

func TestParseArmorProfileStrict_KnownFieldsOK(t *testing.T) {
	p, err := ParseArmorProfileStrict([]byte(`{"def":12,"vit":3,"hp_pct":8}`))
	if err != nil {
		t.Fatalf("合法欄位不應該報錯，got %v", err)
	}
	if p.Def != 12 || p.Vit != 3 || p.HpPct != 8 {
		t.Fatalf("欄位應該正確解析，got %+v", p)
	}
}

// 空 raw／缺欄位補中性值的行為跟 ParseArmorProfile 一致（後台建立新防具時 profile 可以先留空）。
func TestParseArmorProfileStrict_EmptyIsNeutral(t *testing.T) {
	p, err := ParseArmorProfileStrict(nil)
	if err != nil {
		t.Fatalf("空 raw 不應該報錯，got %v", err)
	}
	if p != (ArmorProfile{}) {
		t.Fatalf("空 raw 應該回全零中性值，got %+v", p)
	}
}

// --- armorItemSlotFor ---

func TestArmorItemSlotFor_AccessorySlotsMapToAccessory(t *testing.T) {
	if got := armorItemSlotFor("accessory1"); got != "accessory" {
		t.Fatalf("accessory1 應對應 rpg_armor_items.slot=accessory，got %q", got)
	}
	if got := armorItemSlotFor("accessory2"); got != "accessory" {
		t.Fatalf("accessory2 應對應 rpg_armor_items.slot=accessory，got %q", got)
	}
}

func TestArmorItemSlotFor_ArmorSlotsPassThrough(t *testing.T) {
	for _, slot := range []string{"helmet", "gloves", "armor", "legs", "boots"} {
		if got := armorItemSlotFor(slot); got != slot {
			t.Fatalf("%s 應該原樣對應，got %q", slot, got)
		}
	}
}

// --- ArmorRow.Validate ---

func TestArmorRowValidate_JobArmorTierUpTo10(t *testing.T) {
	jobID := "light_knight"
	a := ArmorRow{ID: "lk_helmet_t10", JobID: &jobID, Slot: "helmet", Tier: 10, Name: "x", Rarity: "legendary", LevelReq: 90}
	if err := a.Validate(); err != nil {
		t.Fatalf("防具 tier=10 應該合法，got %v", err)
	}
}

func TestArmorRowValidate_JobArmorTier11Rejected(t *testing.T) {
	jobID := "light_knight"
	a := ArmorRow{ID: "x", JobID: &jobID, Slot: "helmet", Tier: 11, Name: "x", Rarity: "legendary", LevelReq: 90}
	if err := a.Validate(); err == nil {
		t.Fatalf("防具 tier=11 應該報錯（超過 1..10）")
	}
}

func TestArmorRowValidate_AccessoryTierUpTo5(t *testing.T) {
	a := ArmorRow{ID: "acc_x_t5", JobID: nil, Slot: "accessory", Tier: 5, Name: "x", Rarity: "legendary", LevelReq: 80}
	if err := a.Validate(); err != nil {
		t.Fatalf("飾品 tier=5 應該合法，got %v", err)
	}
}

// 飾品（JobID==nil）tier 上限是 5，不是 10——跟防具共用同一張表但規則更嚴，回歸測試防止誤用
// 防具的 1..10 檢查。
func TestArmorRowValidate_AccessoryTier6Rejected(t *testing.T) {
	a := ArmorRow{ID: "x", JobID: nil, Slot: "accessory", Tier: 6, Name: "x", Rarity: "legendary", LevelReq: 80}
	if err := a.Validate(); err == nil {
		t.Fatalf("飾品 tier=6 應該報錯（超過 1..5）")
	}
}

func TestArmorRowValidate_InvalidSlotRejected(t *testing.T) {
	jobID := "light_knight"
	a := ArmorRow{ID: "x", JobID: &jobID, Slot: "ring", Tier: 1, Name: "x", Rarity: "common", LevelReq: 1}
	if err := a.Validate(); err == nil {
		t.Fatalf("不合法的 slot 應該報錯")
	}
}

func TestArmorRowValidate_MissingNameRejected(t *testing.T) {
	jobID := "light_knight"
	a := ArmorRow{ID: "x", JobID: &jobID, Slot: "helmet", Tier: 1, Name: "", Rarity: "common", LevelReq: 1}
	if err := a.Validate(); err == nil {
		t.Fatalf("name 為空應該報錯")
	}
}

// --- AggregateEquipment：相加與 clamp（CONTRACT §2「彙總規則」）---

func TestAggregateEquipment_NilWeaponNilArmorsIsZero(t *testing.T) {
	b := AggregateEquipment(nil, nil)
	if b != (EquipBonus{}) {
		t.Fatalf("沒有武器也沒有防具時應該全零，got %+v", b)
	}
}

// 只有武器、armors 為 nil：驗證 CONTRACT「只有武器時結果與 P7 逐位元一致」的彙總來源正確
// （逐位元一致的完整驗證在 compute_weapon_test.go，這裡只驗證 AggregateEquipment 本身的映射）。
func TestAggregateEquipment_WeaponOnlyMapsWeaponFields(t *testing.T) {
	wp := WeaponProfile{
		IntBonus: 10, Atk: 20, Matk: 8, AtkPct: 5, MatkPct: 6, DefPct: 7, MdefPct: 9,
		FleeBonus: 12, MpPct: 3, CritPct: 4, CritDmgPct: 11,
		IntervalPct: -20, ElementResistPct: 15, // 這兩項武器貢獻，但 EquipBonus 刻意不吸收（見下方測試）。
	}
	b := AggregateEquipment(&wp, nil)
	if b.Int != 10 || b.Str != 0 || b.Agi != 0 || b.Vit != 0 || b.Dex != 0 || b.Luk != 0 {
		t.Fatalf("只有武器時六素質應該只有 Int（int_bonus），got %+v", b)
	}
	if b.Atk != 20 || b.Matk != 8 || b.DefPct != 7 || b.MdefPct != 9 || b.FleeBonus != 12 {
		t.Fatalf("Atk/Matk/DefPct/MdefPct/FleeBonus 應該直接取自武器，got %+v", b)
	}
	if b.AtkPct != 5 || b.MatkPct != 6 || b.MpPct != 3 || b.CritPct != 4 || b.CritDmgPct != 11 {
		t.Fatalf("Atk/MatkPct/MpPct/CritPct/CritDmgPct 應該吃到武器貢獻，got %+v", b)
	}
	if b.Def != 0 {
		t.Fatalf("武器沒有 flat def 欄位，Def 應該維持 0，got %v", b.Def)
	}
	// WIRE：equipmentEffects「只彙總防具與飾品」，武器的 interval_pct/element_resist_pct 刻意不
	// 疊進 EquipBonus（引擎自己拿 weapon.profile 的值另外相加），避免重複計入。
	if b.IntervalPct != 0 || b.ElementResistPct != 0 {
		t.Fatalf("武器的 interval_pct/element_resist_pct 不應該疊進 EquipBonus，got %+v", b)
	}
}

func TestAggregateEquipment_ArmorsSumSixStatsAndDef(t *testing.T) {
	armors := []ArmorProfile{
		{Str: 2, Vit: 3, Def: 10},
		{Str: 1, Agi: 4, Def: 5},
	}
	b := AggregateEquipment(nil, armors)
	if b.Str != 3 || b.Agi != 4 || b.Vit != 3 || b.Def != 15 {
		t.Fatalf("多件防具的六素質/flat def 應該逐項相加，got %+v", b)
	}
}

func TestAggregateEquipment_WeaponAndArmorCombine(t *testing.T) {
	wp := WeaponProfile{IntBonus: 5, Atk: 10, AtkPct: 10}
	armors := []ArmorProfile{{Str: 8, Def: 20, AtkPct: 5}}
	b := AggregateEquipment(&wp, armors)
	if b.Int != 5 || b.Str != 8 {
		t.Fatalf("六素質應該同時吃到武器 int_bonus 與防具 flat，got %+v", b)
	}
	if b.Def != 20 {
		t.Fatalf("Def 應該只吃防具，got %v", b.Def)
	}
	if b.AtkPct != 15 {
		t.Fatalf("atk_pct 應該武器＋防具相加＝15，got %v", b.AtkPct)
	}
}

func TestAggregateEquipment_IntervalPctClampsAtMinus50(t *testing.T) {
	armors := []ArmorProfile{{IntervalPct: -30}, {IntervalPct: -30}}
	b := AggregateEquipment(nil, armors)
	if b.IntervalPct != -50 {
		t.Fatalf("interval_pct 相加 -60 應該被 clamp 到 -50，got %v", b.IntervalPct)
	}
}

func TestAggregateEquipment_IntervalPctBelowCapNotClamped(t *testing.T) {
	armors := []ArmorProfile{{IntervalPct: -10}}
	b := AggregateEquipment(nil, armors)
	if b.IntervalPct != -10 {
		t.Fatalf("未超過 clamp 時應該原樣保留，got %v", b.IntervalPct)
	}
}

func TestAggregateEquipment_ElementResistPctClampsAt60(t *testing.T) {
	armors := []ArmorProfile{{ElementResistPct: 40}, {ElementResistPct: 40}}
	b := AggregateEquipment(nil, armors)
	if b.ElementResistPct != 60 {
		t.Fatalf("element_resist_pct 相加 80 應該被 clamp 到 60，got %v", b.ElementResistPct)
	}
}

func TestAggregateEquipment_DamageTakenPctClampsAtMinus60(t *testing.T) {
	armors := []ArmorProfile{{DamageTakenPct: -40}, {DamageTakenPct: -40}}
	b := AggregateEquipment(nil, armors)
	if b.DamageTakenPct != -60 {
		t.Fatalf("damage_taken_pct 相加 -80 應該被 clamp 到 -60，got %v", b.DamageTakenPct)
	}
}

func TestAggregateEquipment_MpCostReducePctClampsAt50(t *testing.T) {
	armors := []ArmorProfile{{MpCostReducePct: 30}, {MpCostReducePct: 30}}
	b := AggregateEquipment(nil, armors)
	if b.MpCostReducePct != 50 {
		t.Fatalf("mp_cost_reduce_pct 相加 60 應該被 clamp 到 50，got %v", b.MpCostReducePct)
	}
}

func TestAggregateEquipment_RegenFieldsSumWithoutClamp(t *testing.T) {
	armors := []ArmorProfile{{HpRegenPctPer5s: 2, MpRegenPctPer5s: 1}, {HpRegenPctPer5s: 3, MpRegenPctPer5s: 2}}
	b := AggregateEquipment(nil, armors)
	if b.HpRegenPctPer5s != 5 || b.MpRegenPctPer5s != 3 {
		t.Fatalf("regen 欄位應該單純相加、不 clamp，got %+v", b)
	}
}

// --- ToEquipBonusDTO ---

func TestToEquipBonusDTO_CopiesWireFields(t *testing.T) {
	b := EquipBonus{Str: 1, Def: 5, HpPct: 2, IntervalPct: -10, MpCostReducePct: 20, ElementResistPct: 15}
	dto := ToEquipBonusDTO(b)
	if dto.Str != 1 || dto.Def != 5 || dto.HpPct != 2 || dto.IntervalPct != -10 || dto.MpCostReducePct != 20 || dto.ElementResistPct != 15 {
		t.Fatalf("ToEquipBonusDTO 應該原樣搬 WIRE 欄位，got %+v", dto)
	}
}

// --- PlayerEquipmentSnapshot ---

func TestPlayerEquipmentSnapshot_EquipNilWhenNothingEquipped(t *testing.T) {
	snap := PlayerEquipmentSnapshot{}
	if snap.Equip() != nil {
		t.Fatalf("完全沒有裝備時 Equip() 應該回 nil（維持 eq==nil 不 floor 的既有行為）")
	}
}

func TestPlayerEquipmentSnapshot_EquipNonNilWhenArmorOnly(t *testing.T) {
	snap := PlayerEquipmentSnapshot{Armor: map[string]*ArmorRow{
		"helmet": {ID: "h1", Profile: ArmorProfile{Def: 5}},
	}}
	eq := snap.Equip()
	if eq == nil {
		t.Fatalf("只有防具、沒有武器時 Equip() 也應該回非 nil")
	}
	if eq.Def != 5 {
		t.Fatalf("應該吃到防具的 flat def，got %v", eq.Def)
	}
}

func TestPlayerEquipmentSnapshot_EquipmentEffectsExcludesWeapon(t *testing.T) {
	wp := WeaponProfile{IntervalPct: -30, ElementResistPct: 20}
	snap := PlayerEquipmentSnapshot{
		Weapon: &WeaponRow{ID: "w1", Profile: wp},
		Armor: map[string]*ArmorRow{
			"helmet": {ID: "h1", Profile: ArmorProfile{IntervalPct: -5, ElementResistPct: 10}},
		},
	}
	eff := snap.EquipmentEffects()
	if eff.IntervalPct != -5 || eff.ElementResistPct != 10 {
		t.Fatalf("equipmentEffects 只應該彙總防具/飾品，不含武器，got %+v", eff)
	}
}
