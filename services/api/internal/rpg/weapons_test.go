// weapons_test.go：DORPG P7（CONTRACT §3）WeaponProfile 解析/驗證＋Row.Validate() 純函式測試。
// 連 DB 的部分（GET/PUT /rpg/equipment、後台武器 CRUD）留給 Neon 分支整合測試，比照
// tavern_test.go 檔頭的既有慣例。
package rpg

import "testing"

func TestDefaultWeaponProfile_IsNeutral(t *testing.T) {
	p := DefaultWeaponProfile()
	if p.Hits != 1 || p.HitMul != 1.0 || p.ChargeTimeMul != 1.0 || p.ChargeDmgMul != 1.0 {
		t.Fatalf("中性值：hits=1/hit_mul=1/charge_time_mul=1/charge_dmg_mul=1，got %+v", p)
	}
	if p.Element != "neutral" {
		t.Fatalf("中性值 element 應為 neutral，got %s", p.Element)
	}
	if p.Atk != 0 || p.Matk != 0 || p.AtkPct != 0 || p.CritPct != 0 {
		t.Fatalf("其餘欄位中性值應為 0，got %+v", p)
	}
}

func TestParseWeaponProfile_EmptyReturnsDefault(t *testing.T) {
	p, err := ParseWeaponProfile(nil)
	if err != nil {
		t.Fatalf("空輸入不應報錯：%v", err)
	}
	if p != DefaultWeaponProfile() {
		t.Fatalf("空輸入應等同 DefaultWeaponProfile()，got %+v", p)
	}
}

func TestParseWeaponProfile_MissingFieldsFillNeutral(t *testing.T) {
	// 只給 atk，其餘欄位（尤其 hits/hit_mul/charge_time_mul/charge_dmg_mul/element）缺欄位＝中性值。
	p, err := ParseWeaponProfile([]byte(`{"atk": 12}`))
	if err != nil {
		t.Fatalf("不應報錯：%v", err)
	}
	if p.Atk != 12 {
		t.Fatalf("atk 應為 12，got %v", p.Atk)
	}
	if p.Hits != 1 || p.HitMul != 1.0 || p.ChargeTimeMul != 1.0 || p.ChargeDmgMul != 1.0 || p.Element != "neutral" {
		t.Fatalf("缺欄位應補中性值，got %+v", p)
	}
}

func TestParseWeaponProfile_FullOverride(t *testing.T) {
	raw := []byte(`{
		"atk": 20, "matk": 5, "int_bonus": 3, "mp_pct": 10, "atk_pct": 5, "matk_pct": 0,
		"def_pct": 0, "mdef_pct": 0, "hits": 2, "hit_mul": 0.6, "extra_hit_chance_pct": 10,
		"interval_pct": -10, "charge_time_mul": 1.5, "charge_dmg_mul": 1.8, "splash_pct": 40,
		"size_bonus": {"small": 5, "medium": 0, "large": 5}, "crit_pct": 3, "crit_dmg_pct": 5,
		"flee_bonus": 3, "element_resist_pct": 5, "magic_skill_pct": 5, "element": "fire"
	}`)
	p, err := ParseWeaponProfile(raw)
	if err != nil {
		t.Fatalf("不應報錯：%v", err)
	}
	if p.Hits != 2 || p.HitMul != 0.6 || p.Element != "fire" || p.SizeBonus.Small != 5 || p.SizeBonus.Large != 5 {
		t.Fatalf("完整覆蓋應逐欄生效，got %+v", p)
	}
}

func TestParseWeaponProfile_InvalidElementRejected(t *testing.T) {
	if _, err := ParseWeaponProfile([]byte(`{"element": "poison"}`)); err == nil {
		t.Fatalf("不合法的 element 應報錯")
	}
}

func TestParseWeaponProfile_ZeroHitsRejected(t *testing.T) {
	if _, err := ParseWeaponProfile([]byte(`{"hits": 0}`)); err == nil {
		t.Fatalf("hits=0 應報錯（會讓攻擊次數變 0）")
	}
}

func TestParseWeaponProfile_ZeroHitMulRejected(t *testing.T) {
	if _, err := ParseWeaponProfile([]byte(`{"hit_mul": 0}`)); err == nil {
		t.Fatalf("hit_mul=0 應報錯（會讓傷害變 0）")
	}
}

func TestParseWeaponProfile_ZeroChargeTimeMulRejected(t *testing.T) {
	if _, err := ParseWeaponProfile([]byte(`{"charge_time_mul": 0}`)); err == nil {
		t.Fatalf("charge_time_mul=0 應報錯")
	}
}

func TestParseWeaponProfile_ZeroChargeDmgMulRejected(t *testing.T) {
	if _, err := ParseWeaponProfile([]byte(`{"charge_dmg_mul": 0}`)); err == nil {
		t.Fatalf("charge_dmg_mul=0 應報錯")
	}
}

func TestParseWeaponProfile_IntervalPctFloorRejected(t *testing.T) {
	if _, err := ParseWeaponProfile([]byte(`{"interval_pct": -100}`)); err == nil {
		t.Fatalf("interval_pct=-100 應報錯（攻擊冷卻會變 0/負值）")
	}
	if _, err := ParseWeaponProfile([]byte(`{"interval_pct": -99}`)); err != nil {
		t.Fatalf("interval_pct=-99 應合法：%v", err)
	}
}

func TestParseWeaponProfile_InvalidJSONRejected(t *testing.T) {
	if _, err := ParseWeaponProfile([]byte(`{not json`)); err == nil {
		t.Fatalf("壞掉的 JSON 應報錯")
	}
}

func TestWeaponTypeRow_Validate(t *testing.T) {
	valid := WeaponTypeRow{ID: "lk_sword", JobID: "light_knight", Name: "單手劍", Visual: "sword"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("合法的武器類型不應報錯：%v", err)
	}
	missing := WeaponTypeRow{ID: "", JobID: "light_knight", Name: "單手劍", Visual: "sword"}
	if err := missing.Validate(); err == nil {
		t.Fatalf("id 為空應報錯")
	}
	badVisual := WeaponTypeRow{ID: "lk_sword", JobID: "light_knight", Name: "單手劍", Visual: "laser"}
	if err := badVisual.Validate(); err == nil {
		t.Fatalf("不合法的 visual 應報錯")
	}
}

func TestWeaponRow_Validate(t *testing.T) {
	base := WeaponRow{
		ID: "lk_sword_t1", TypeID: "lk_sword", Tier: 1, Name: "初階單手劍",
		Rarity: "common", LevelReq: 1, Element: "neutral", Profile: DefaultWeaponProfile(),
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("合法武器不應報錯：%v", err)
	}
	badTier := base
	badTier.Tier = 11
	if err := badTier.Validate(); err == nil {
		t.Fatalf("tier=11 應報錯（超出 1..10）")
	}
	badRarity := base
	badRarity.Rarity = "mythic"
	if err := badRarity.Validate(); err == nil {
		t.Fatalf("不合法的 rarity 應報錯")
	}
	badLevel := base
	badLevel.LevelReq = 0
	if err := badLevel.Validate(); err == nil {
		t.Fatalf("level_req=0 應報錯")
	}
	badElement := base
	badElement.Element = "poison"
	if err := badElement.Validate(); err == nil {
		t.Fatalf("不合法的 element 應報錯")
	}
}

// --- toWeaponDTO：GET /rpg/equipment 的 can_equip/equipped 動態欄位是純函式，直接測。 ---

func TestToWeaponDTO_CanEquipByEffectiveLevel(t *testing.T) {
	w := WeaponRow{ID: "lk_sword_t3", LevelReq: 20}
	below := toWeaponDTO(w, 19, "")
	if below.CanEquip {
		t.Fatalf("等級不足應 can_equip=false")
	}
	exact := toWeaponDTO(w, 20, "")
	if !exact.CanEquip {
		t.Fatalf("等級恰好達門檻應 can_equip=true")
	}
	above := toWeaponDTO(w, 99, "")
	if !above.CanEquip {
		t.Fatalf("等級超過門檻應 can_equip=true")
	}
}

func TestToWeaponDTO_EquippedFlag(t *testing.T) {
	w := WeaponRow{ID: "lk_sword_t3", LevelReq: 1}
	eq := toWeaponDTO(w, 99, "lk_sword_t3")
	if !eq.Equipped {
		t.Fatalf("id 相符應 equipped=true")
	}
	notEq := toWeaponDTO(w, 99, "lk_dual_t3")
	if notEq.Equipped {
		t.Fatalf("id 不符應 equipped=false")
	}
	empty := toWeaponDTO(w, 99, "")
	if empty.Equipped {
		t.Fatalf("equippedID 為空（未裝備任何武器）應 equipped=false")
	}
}

// --- ToWeaponProfileWire / buildPlayerWeaponWire / buildCompanionWeaponWire：純函式轉換。 ---

func TestToWeaponProfileWire_OmitsComputeOnlyFields(t *testing.T) {
	p := WeaponProfile{
		Atk: 10, Matk: 5, Hits: 2, HitMul: 0.6, ExtraHitChancePct: 20, IntervalPct: -10,
		ChargeTimeMul: 1.5, ChargeDmgMul: 1.8, SplashPct: 40,
		SizeBonus: WeaponSizeBonus{Small: 5, Medium: 0, Large: 5},
		CritPct:   3, CritDmgPct: 5, ElementResistPct: 5, MagicSkillPct: 5, Element: "fire",
		// IntBonus/MpPct/AtkPct/MatkPct/DefPct/MdefPct/FleeBonus 刻意留 0：這些已經被 Compute()
		// 吃進玩家的 stats/derived，WeaponProfileWire 不應該再帶一份（見 WIRE.md 註解）。
		IntBonus: 99, MpPct: 99, AtkPct: 99, MatkPct: 99, DefPct: 99, MdefPct: 99, FleeBonus: 99,
	}
	wire := ToWeaponProfileWire(p)
	if wire.Atk != 10 || wire.Matk != 5 || wire.Hits != 2 || wire.HitMul != 0.6 {
		t.Fatalf("atk/matk/hits/hit_mul 應原樣帶過去，got %+v", wire)
	}
	if wire.SizeBonus.Small != 5 || wire.SizeBonus.Large != 5 {
		t.Fatalf("size_bonus 應原樣帶過去，got %+v", wire.SizeBonus)
	}
	if wire.CritPct != 3 {
		t.Fatalf("crit_pct 例外仍應送出（供除錯顯示），got %v", wire.CritPct)
	}
	// 用反射以外的方式確認 WeaponProfileWire 沒有 IntBonus/MpPct/AtkPct/MatkPct/DefPct/MdefPct/
	// FleeBonus 這幾個欄位——編譯期就會擋下任何不小心加回去的欄位存取，這裡不需要額外斷言。
}

func TestBuildPlayerWeaponWire_NilWhenUnequipped(t *testing.T) {
	if got := buildPlayerWeaponWire(nil, nil); got != nil {
		t.Fatalf("未裝備應回 nil，got %+v", got)
	}
	w := WeaponRow{ID: "lk_sword_t3", Name: "熟練單手劍", TypeID: "lk_sword", Profile: DefaultWeaponProfile()}
	wt := WeaponTypeRow{ID: "lk_sword", Visual: "sword"}
	got := buildPlayerWeaponWire(&w, &wt)
	if got == nil || got.Visual != "sword" || got.ID != "lk_sword_t3" || got.TypeID != "lk_sword" {
		t.Fatalf("已裝備應回完整物件，got %+v", got)
	}
}

func TestBuildCompanionWeaponWire_FixedVisualNeutralProfile(t *testing.T) {
	got := buildCompanionWeaponWire("staff")
	if got == nil || got.Visual != "staff" {
		t.Fatalf("應固定送 visual，got %+v", got)
	}
	if got.ID != "" || got.Name != "" || got.TypeID != "" {
		t.Fatalf("傭兵沒有真正的武器實體，id/name/typeId 應留空，got %+v", got)
	}
	if got.Profile.Hits != 1 || got.Profile.HitMul != 1.0 {
		t.Fatalf("Profile 應為中性值（不改變既有戰鬥手感），got %+v", got.Profile)
	}
}

// TestResolvePlayerWeaponWire_FallsBackToJobVisualWhenUnequipped INTEGRATOR 補：battle.go
// BattleBootstrap 用這支函式決定 wirePartyMember.Weapon，不能在玩家未裝備時整個送 nil——那會
// 讓 P7 之前既有的「職業預設武器視覺」（曾經是 wirePartyMember.Weapon 的純字串 job.Weapon）
// 消失，戰鬥畫面全部退化成引擎寫死的預設劍（見 resolvePlayerWeaponWire 函式註解）。
func TestResolvePlayerWeaponWire_FallsBackToJobVisualWhenUnequipped(t *testing.T) {
	job := &JobRow{ID: "mage", Weapon: "staff"}
	got := resolvePlayerWeaponWire(nil, nil, job)
	if got == nil || got.Visual != "staff" {
		t.Fatalf("未裝備應退回職業預設視覺 staff，got %+v", got)
	}
	if got.ID != "" || got.Name != "" || got.TypeID != "" {
		t.Fatalf("職業預設不是真正的武器實體，id/name/typeId 應留空，got %+v", got)
	}
	if got.Profile.Hits != 1 || got.Profile.HitMul != 1.0 || got.Profile.Element != "neutral" {
		t.Fatalf("職業預設應為中性 Profile（不改變既有戰鬥手感），got %+v", got.Profile)
	}
}

func TestResolvePlayerWeaponWire_EquippedOverridesJobDefault(t *testing.T) {
	job := &JobRow{ID: "mage", Weapon: "staff"}
	w := WeaponRow{ID: "mg_rod_t5", Name: "測試棍", TypeID: "mg_rod", Profile: WeaponProfile{MatkPct: 10}}
	wt := WeaponTypeRow{ID: "mg_rod", Visual: "staff"}
	got := resolvePlayerWeaponWire(&w, &wt, job)
	if got == nil || got.ID != "mg_rod_t5" || got.TypeID != "mg_rod" {
		t.Fatalf("已裝備應優先於職業預設，got %+v", got)
	}
}

func TestResolvePlayerWeaponWire_NilWhenNoJob(t *testing.T) {
	if got := resolvePlayerWeaponWire(nil, nil, nil); got != nil {
		t.Fatalf("未裝備且查無職業（理論上不會發生）應保守回 nil，got %+v", got)
	}
}
