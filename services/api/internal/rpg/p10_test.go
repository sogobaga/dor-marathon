// p10_test.go：DORPG P10（scratchpad/dorpg_p10/{CONTRACT.md,WIRE.md}）——重騎士守護路線
// （taunt 技能展開、guard_taunt 被動）、JobDTO 新增的 paths/traits、bootstrap 職業特性合併
// （夾限）、以及 migration 187 seed 的自我核對。純函式測試，不連 DB（比照 presets_test.go／
// skills_test.go 既有慣例）；migration 187 的 SQL 內容用 os.ReadFile 讀檔案本身做字串核對
// （比照 presets_equipment_test.go TestSystemPresetEquipment_ItemIDsExistInMigrationSeeds
// 的既有慣例）。
package rpg

import (
	"os"
	"strings"
	"testing"
)

func floatPtr(f float64) *float64 { return &f }

// ---------------------------------------------------------------------------
// ExpandEffect：kind=taunt 展開（CONTRACT §2/§3）。
// ---------------------------------------------------------------------------

// hkC1Fixture 逐欄對照 migration 187 的 hk_c1（挑釁）：duration_base_ms=4000、
// duration_per_level_ms=500、retarget=true，無 prereq，max_level=10。
func hkC1Fixture() SkillRow {
	return SkillRow{
		ID: "hk_c1", Kind: "taunt", Target: "self", MaxLevel: 10,
		Effect: SkillEffect{DurationBaseMs: 4000, DurationPerLevelMs: 500, Retarget: true},
	}
}

// hkC3Fixture 逐欄對照 migration 187 的 hk_c3（守護姿態）：duration_base_ms=10000、
// duration_per_level_ms=1000、damage_taken_pct_base=-10、damage_taken_pct_per_level=-2、
// retarget=false，max_level=5。
func hkC3Fixture() SkillRow {
	return SkillRow{
		ID: "hk_c3", Kind: "taunt", Target: "self", MaxLevel: 5,
		Effect: SkillEffect{
			DurationBaseMs: 10000, DurationPerLevelMs: 1000,
			DamageTakenPctBase: -10, DamageTakenPctPerLevel: -2, Retarget: false,
		},
	}
}

func TestExpandEffect_TauntLevel1UsesBaseValues(t *testing.T) {
	e := ExpandEffect(hkC1Fixture(), 1)
	if e.Kind != "taunt" {
		t.Fatalf("Kind 應為 taunt，got %s", e.Kind)
	}
	if e.DurationMs != 4000 {
		t.Fatalf("Lv1 duration_ms 應為 base 值 4000，got %d", e.DurationMs)
	}
	if !e.Retarget {
		t.Fatalf("hk_c1 retarget=true，展開後應維持 true")
	}
	if e.DamageTakenPct != 0 {
		t.Fatalf("hk_c1 沒有 damage_taken_pct_base，展開後應為 0，got %v", e.DamageTakenPct)
	}
}

func TestExpandEffect_TauntMaxLevelScalesDuration(t *testing.T) {
	e := ExpandEffect(hkC1Fixture(), 10) // max_level=10 → steps=9
	want := 4000 + 500*9
	if e.DurationMs != want {
		t.Fatalf("Lv10 duration_ms 應為 %d，got %d", want, e.DurationMs)
	}
}

func TestExpandEffect_TauntDamageTakenPctScalesNegatively(t *testing.T) {
	e := ExpandEffect(hkC3Fixture(), 5) // steps=4
	want := -10.0 + -2.0*4
	if e.DamageTakenPct != want {
		t.Fatalf("Lv5 damage_taken_pct 應為 %v，got %v", want, e.DamageTakenPct)
	}
	if e.Retarget {
		t.Fatalf("hk_c3 retarget=false，展開後不該變成 true")
	}
}

func TestExpandEffect_TauntLevelZeroPreviewsAsLevel1(t *testing.T) {
	// ExpandEffect 契約：level<=0 比照 lv=1 預覽（WIRE），taunt 分支沿用同一條規則，不應該是
	// 唯一漏接這條既有約定的 kind。
	e := ExpandEffect(hkC1Fixture(), 0)
	if e.DurationMs != 4000 {
		t.Fatalf("level=0 應比照 Lv1 預覽，duration_ms 應為 4000，got %d", e.DurationMs)
	}
}

func TestExpandEffect_PassiveGuardTauntFlagSurfaces(t *testing.T) {
	// hk_c2（守護本能）：passive，effect 帶 guard_taunt:true，同時仍要展開既有的 stat/value。
	s := SkillRow{
		ID: "hk_c2", Kind: "passive", MaxLevel: 10,
		Effect: SkillEffect{Stat: "def_pct", ValueBase: 3, ValuePerLevel: 0.7, GuardTaunt: true},
	}
	e := ExpandEffect(s, 4) // steps=3 → 3 + 0.7*3 = 5.1
	if !e.GuardTaunt {
		t.Fatalf("passive 展開應帶出 guard_taunt=true")
	}
	if e.Stat != "def_pct" {
		t.Fatalf("passive 的既有 stat 展開不該被 guard_taunt 影響，got %q", e.Stat)
	}
	want := 3 + 0.7*3
	if e.Value != want {
		t.Fatalf("value 應為 %v，got %v", want, e.Value)
	}
}

func TestExpandEffect_PassiveWithoutGuardTauntStaysFalse(t *testing.T) {
	// 既有 60 個職業技能沒有一個帶 guard_taunt（P5～P9 時代不存在這個欄位），確保零值行為不變。
	s := SkillRow{ID: "hk_b2", Kind: "passive", MaxLevel: 10, Effect: SkillEffect{Stat: "def_pct", ValueBase: 2, ValuePerLevel: 0.4}}
	e := ExpandEffect(s, 5)
	if e.GuardTaunt {
		t.Fatalf("hk_b2 沒有 guard_taunt 效果，展開後不該是 true")
	}
}

// ---------------------------------------------------------------------------
// content.go：validSkillKinds/validSkillPaths、SkillRow.Validate()。
// ---------------------------------------------------------------------------

func TestSkillRowValidate_TauntKindAndPathCAreLegal(t *testing.T) {
	s := SkillRow{
		ID: "hk_c1", Name: "挑釁", Kind: "taunt", Target: "self", Weapon: "greatsword",
		Path: "c", DmgType: "physical",
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("kind=taunt/path=c 應合法：%v", err)
	}
}

func TestSkillRowValidate_UnknownKindRejected(t *testing.T) {
	s := SkillRow{ID: "x", Name: "x", Kind: "not_a_kind", Target: "self", Weapon: "sword"}
	if err := s.Validate(); err == nil {
		t.Fatalf("未知 kind 應被拒絕")
	}
}

func TestSkillRowValidate_UnknownPathRejected(t *testing.T) {
	s := SkillRow{ID: "x", Name: "x", Kind: "damage", Target: "self", Weapon: "sword", Path: "d"}
	if err := s.Validate(); err == nil {
		t.Fatalf("path=\"d\" 不在白名單內，應被拒絕")
	}
}

func TestSkillRowValidate_EmptyPathStillLegal(t *testing.T) {
	// 既有 5 個無職業技能 path="" 不該因為新加的白名單被誤擋。
	s := SkillRow{ID: "slash", Name: "斬擊", Kind: "damage", Target: "enemy", Weapon: "sword", Path: ""}
	if err := s.Validate(); err != nil {
		t.Fatalf("path=\"\" 應合法（既有無職業技能）：%v", err)
	}
}

// ---------------------------------------------------------------------------
// JobTraits.Validate()。
// ---------------------------------------------------------------------------

func TestJobTraitsValidate_NilIsLegal(t *testing.T) {
	if err := (JobTraits{}).Validate(); err != nil {
		t.Fatalf("沒有設定 damage_taken_pct 應合法：%v", err)
	}
}

func TestJobTraitsValidate_WithinRangeIsLegal(t *testing.T) {
	if err := (JobTraits{DamageTakenPct: floatPtr(-15)}).Validate(); err != nil {
		t.Fatalf("-15 落在 [-60,0] 內應合法：%v", err)
	}
	if err := (JobTraits{DamageTakenPct: floatPtr(0)}).Validate(); err != nil {
		t.Fatalf("邊界值 0 應合法：%v", err)
	}
	if err := (JobTraits{DamageTakenPct: floatPtr(-60)}).Validate(); err != nil {
		t.Fatalf("邊界值 -60 應合法：%v", err)
	}
}

func TestJobTraitsValidate_OutOfRangeRejected(t *testing.T) {
	if err := (JobTraits{DamageTakenPct: floatPtr(-61)}).Validate(); err == nil {
		t.Fatalf("-61 超過下限，應被拒絕")
	}
	if err := (JobTraits{DamageTakenPct: floatPtr(5)}).Validate(); err == nil {
		t.Fatalf("正值沒有意義，應被拒絕")
	}
}

// ---------------------------------------------------------------------------
// jobs.go：buildJobPaths／mergeJobExtras（WIRE JobDTO.paths，依 key 排序 a→b→c）。
// ---------------------------------------------------------------------------

func TestBuildJobPaths_TwoPathsWhenNoPathC(t *testing.T) {
	j := JobRow{
		PathA: JobPathRow{ID: "x_a", Name: "路線A", Desc: "descA"},
		PathB: JobPathRow{ID: "x_b", Name: "路線B", Desc: "descB"},
	}
	paths := buildJobPaths(j)
	if len(paths) != 2 {
		t.Fatalf("沒有 PathC 時應只有 2 條路線，got %d", len(paths))
	}
	if paths[0].Key != "a" || paths[1].Key != "b" {
		t.Fatalf("順序應為 a,b，got %+v", paths)
	}
}

func TestBuildJobPaths_ThreePathsWhenPathCPresent(t *testing.T) {
	j := JobRow{
		PathA: JobPathRow{ID: "hk_a", Name: "狂戰士", Desc: "descA"},
		PathB: JobPathRow{ID: "hk_b", Name: "重裝騎士", Desc: "descB"},
		PathC: &JobPathRow{ID: "heavy_knight_guardian_wall", Name: "守護", Desc: "descC"},
	}
	paths := buildJobPaths(j)
	if len(paths) != 3 {
		t.Fatalf("有 PathC 時應有 3 條路線，got %d", len(paths))
	}
	if paths[2].Key != "c" || paths[2].ID != "heavy_knight_guardian_wall" || paths[2].Name != "守護" {
		t.Fatalf("第三條路線內容不對：%+v", paths[2])
	}
}

func TestMergeJobExtras_SetsPathCTraitsAndPaths(t *testing.T) {
	base := JobRow{
		ID:    "heavy_knight",
		PathA: JobPathRow{ID: "heavy_knight_berserker", Name: "狂戰士"},
		PathB: JobPathRow{ID: "heavy_knight_guardian", Name: "重裝騎士"},
	}
	extras := jobExtras{
		PathC:  &JobPathRow{ID: "heavy_knight_guardian_wall", Name: "守護", Desc: "守護說明"},
		Traits: JobTraits{DamageTakenPct: floatPtr(-15)},
	}
	got := mergeJobExtras(base, extras)
	if got.PathC == nil || got.PathC.ID != "heavy_knight_guardian_wall" {
		t.Fatalf("PathC 應合併進來，got %+v", got.PathC)
	}
	if got.Traits.DamageTakenPct == nil || *got.Traits.DamageTakenPct != -15 {
		t.Fatalf("Traits 應合併進來，got %+v", got.Traits)
	}
	if len(got.Paths) != 3 {
		t.Fatalf("合併後 Paths 應有 3 條，got %+v", got.Paths)
	}
}

func TestMergeJobExtras_OtherJobsHaveNilPathCAndEmptyTraits(t *testing.T) {
	base := JobRow{ID: "mage", PathA: JobPathRow{ID: "mage_single"}, PathB: JobPathRow{ID: "mage_aoe"}}
	got := mergeJobExtras(base, jobExtras{}) // 沒有 path_c/traits 的職業
	if got.PathC != nil {
		t.Fatalf("mage 沒有第三條路線，PathC 應維持 nil，got %+v", got.PathC)
	}
	if got.Traits.DamageTakenPct != nil {
		t.Fatalf("mage 沒有職業特性，Traits 應是零值，got %+v", got.Traits)
	}
	if len(got.Paths) != 2 {
		t.Fatalf("mage 只有兩條路線，got %d", len(got.Paths))
	}
}

// ---------------------------------------------------------------------------
// scanJobExtras：DB 掃描結果 → jobExtras（nil path_c_id ＝沒有第三條路線）。
// ---------------------------------------------------------------------------

func TestScanJobExtras_NilPathCIDMeansNoPathC(t *testing.T) {
	e := scanJobExtras(nil, nil, nil, []byte(`{}`))
	if e.PathC != nil {
		t.Fatalf("path_c_id 為 nil 時 PathC 應為 nil，got %+v", e.PathC)
	}
}

func TestScanJobExtras_NonNilPathCIDBuildsPathC(t *testing.T) {
	id, name, desc := "heavy_knight_guardian_wall", "守護", "說明"
	e := scanJobExtras(&id, &name, &desc, []byte(`{"damage_taken_pct":-15}`))
	if e.PathC == nil || e.PathC.ID != id || e.PathC.Name != name || e.PathC.Desc != desc {
		t.Fatalf("PathC 應完整帶出三個欄位，got %+v", e.PathC)
	}
	if e.Traits.DamageTakenPct == nil || *e.Traits.DamageTakenPct != -15 {
		t.Fatalf("traits JSON 應正確解析，got %+v", e.Traits)
	}
}

func TestScanJobExtras_MalformedTraitsJSONFallsBackToZeroValue(t *testing.T) {
	// 壞掉的 JSON 不該讓呼叫端 panic 或回錯誤——比照 presets.go scanPreset 對 stats/skill_levels
	// 的既有容錯慣例。
	e := scanJobExtras(nil, nil, nil, []byte(`not json`))
	if e.Traits.DamageTakenPct != nil {
		t.Fatalf("壞掉的 traits JSON 應視為沒有特性，got %+v", e.Traits)
	}
}

// ---------------------------------------------------------------------------
// battle.go：jobTraitsDamageTakenPct／clampDamageTakenPct（bootstrap 合併職業特性用的純函式）。
// ---------------------------------------------------------------------------

func TestJobTraitsDamageTakenPct_NilReturnsZero(t *testing.T) {
	if got := jobTraitsDamageTakenPct(JobTraits{}); got != 0 {
		t.Fatalf("沒有設定時應回 0，got %v", got)
	}
}

func TestJobTraitsDamageTakenPct_ReturnsPointerValue(t *testing.T) {
	if got := jobTraitsDamageTakenPct(JobTraits{DamageTakenPct: floatPtr(-15)}); got != -15 {
		t.Fatalf("應回傳指標指到的值 -15，got %v", got)
	}
}

func TestClampDamageTakenPct_ClampsBelowNegative60(t *testing.T) {
	if got := clampDamageTakenPct(-70); got != -60 {
		t.Fatalf("-70 應被夾到 -60，got %v", got)
	}
}

func TestClampDamageTakenPct_WithinRangeUnchanged(t *testing.T) {
	if got := clampDamageTakenPct(-30); got != -30 {
		t.Fatalf("-30 在範圍內不該被改動，got %v", got)
	}
}

func TestClampDamageTakenPct_ExactlyNegative60Unchanged(t *testing.T) {
	if got := clampDamageTakenPct(-60); got != -60 {
		t.Fatalf("邊界值 -60 不該再被改動，got %v", got)
	}
}

// ---------------------------------------------------------------------------
// skills.go：hasGuardTauntPassive（守護狀態來源之一：CONTRACT §3）。
// ---------------------------------------------------------------------------

func guardTauntPassiveFixture() SkillRow {
	return SkillRow{
		ID: "hk_c2", Kind: "passive", MaxLevel: 10,
		Effect: SkillEffect{Stat: "def_pct", ValueBase: 3, ValuePerLevel: 0.7, GuardTaunt: true},
	}
}

func TestHasGuardTauntPassive_TrueWhenInvested(t *testing.T) {
	jobSkills := []SkillRow{hkC1Fixture(), guardTauntPassiveFixture()}
	levels := map[string]int{"hk_c1": 3, "hk_c2": 1}
	if !hasGuardTauntPassive(jobSkills, levels) {
		t.Fatalf("hk_c2 已投資 1 級，應成立")
	}
}

func TestHasGuardTauntPassive_FalseWhenZeroLevel(t *testing.T) {
	jobSkills := []SkillRow{guardTauntPassiveFixture()}
	levels := map[string]int{"hk_c2": 0}
	if hasGuardTauntPassive(jobSkills, levels) {
		t.Fatalf("hk_c2 未投資（0 級），不該成立")
	}
}

func TestHasGuardTauntPassive_FalseWhenMissingFromLevels(t *testing.T) {
	jobSkills := []SkillRow{guardTauntPassiveFixture()}
	if hasGuardTauntPassive(jobSkills, map[string]int{}) {
		t.Fatalf("等級 map 裡完全沒有這個技能（視為 0 級），不該成立")
	}
}

func TestHasGuardTauntPassive_FalseForOtherJobsWithoutTheSkill(t *testing.T) {
	// 其餘五個職業的技能樹裡沒有任何一個帶 guard_taunt——用既有 60 技能其中幾個當代表。
	jobSkills := []SkillRow{
		{ID: "mg_a4", Kind: "passive", Effect: SkillEffect{Stat: "matk_pct", ValueBase: 4, ValuePerLevel: 0.6}},
		{ID: "cl_a4", Kind: "passive", Effect: SkillEffect{Stat: "hp_max_pct", ValueBase: 3, ValuePerLevel: 0.6}},
	}
	levels := map[string]int{"mg_a4": 10, "cl_a4": 10}
	if hasGuardTauntPassive(jobSkills, levels) {
		t.Fatalf("這兩個被動都沒有 guard_taunt 效果，不該成立")
	}
}

func TestHasGuardTauntPassive_IgnoresNonPassiveKindEvenIfFlagSet(t *testing.T) {
	// 資料異常防呆：guard_taunt 只該出現在 passive 技能上，即使不小心塞到別的 kind 也不該生效
	// （避免例如 taunt 技能自己被錯誤標記時被誤判成「守護本能」來源）。
	jobSkills := []SkillRow{
		{ID: "weird", Kind: "buff", Effect: SkillEffect{GuardTaunt: true}},
	}
	levels := map[string]int{"weird": 5}
	if hasGuardTauntPassive(jobSkills, levels) {
		t.Fatalf("kind!=passive 時即使 GuardTaunt=true 也不該成立")
	}
}

// ---------------------------------------------------------------------------
// skills.go：SkillDTO/wireSkill 的 taunt 子物件（僅 kind=taunt 才非 nil）。
// ---------------------------------------------------------------------------

func TestSkillDTOsFromLevels_TauntSkillGetsTauntObject(t *testing.T) {
	skills := []SkillRow{hkC1Fixture()}
	dtos := skillDTOsFromLevels(skills, map[string]int{"hk_c1": 3}, 10)
	if len(dtos) != 1 {
		t.Fatalf("應有 1 筆 DTO，got %d", len(dtos))
	}
	if dtos[0].Taunt == nil {
		t.Fatalf("kind=taunt 的技能 SkillDTO.Taunt 不該是 nil")
	}
	want := 4000 + 500*2 // Lv3 → steps=2
	if dtos[0].Taunt.DurationMs != want {
		t.Fatalf("Taunt.DurationMs 應為 %d，got %d", want, dtos[0].Taunt.DurationMs)
	}
	if !dtos[0].Taunt.Retarget {
		t.Fatalf("hk_c1 retarget=true，Taunt.Retarget 應為 true")
	}
}

func TestSkillDTOsFromLevels_NonTauntSkillHasNilTaunt(t *testing.T) {
	skills := []SkillRow{{ID: "hk_b1", Kind: "damage", MaxLevel: 10}}
	dtos := skillDTOsFromLevels(skills, map[string]int{"hk_b1": 5}, 10)
	if dtos[0].Taunt != nil {
		t.Fatalf("非 taunt 技能的 Taunt 應為 nil，got %+v", dtos[0].Taunt)
	}
}

func TestToWireSkillLeveled_TauntSkillGetsWireTaunt(t *testing.T) {
	w := toWireSkillLeveled(hkC3Fixture(), 3) // steps=2
	if w.Taunt == nil {
		t.Fatalf("kind=taunt 的 wireSkill.Taunt 不該是 nil")
	}
	wantDuration := 10000 + 1000*2
	wantDmgPct := -10.0 + -2.0*2
	if w.Taunt.DurationMs != wantDuration {
		t.Fatalf("durationMs 應為 %d，got %d", wantDuration, w.Taunt.DurationMs)
	}
	if w.Taunt.DamageTakenPct != wantDmgPct {
		t.Fatalf("damageTakenPct 應為 %v，got %v", wantDmgPct, w.Taunt.DamageTakenPct)
	}
	if w.Taunt.Retarget {
		t.Fatalf("hk_c3 retarget=false")
	}
}

func TestToWireSkillLeveled_NonTauntSkillHasNilWireTaunt(t *testing.T) {
	w := toWireSkillLeveled(SkillRow{ID: "hk_b1", Kind: "damage", MaxLevel: 10}, 5)
	if w.Taunt != nil {
		t.Fatalf("非 taunt 技能的 wireSkill.Taunt 應為 nil，got %+v", w.Taunt)
	}
}

// ---------------------------------------------------------------------------
// presets.go：ValidatePreset 對 path=c／kind=taunt 技能一視同仁（CONTRACT §5「ValidatePreset
// 對 path c 技能與 taunt kind 正常處理」）——這支函式本來就不看 kind/path，這裡的測試是確認
// 「不看」這件事在加入 taunt/c 之後仍然成立，而不是新增分支邏輯。
// ---------------------------------------------------------------------------

func TestValidatePreset_HandlesGuardianPathSkillsLikeAnyOther(t *testing.T) {
	cfg := DefaultConfig()
	jobSkills := heavyKnightSkillsFixture()
	stats := Stats{Str: 25, Agi: 1, Vit: 25, Dex: 1, Int: 1, Luk: 1}
	skillLevels := map[string]int{"hk_b1": 5, "hk_b2": 6, "hk_b3": 3, "hk_c1": 3, "hk_c2": 3, "hk_c3": 4}
	if errs := ValidatePreset(cfg, jobSkills, 25, stats, skillLevels); len(errs) != 0 {
		t.Fatalf("合法的守護路線配置不應有錯誤：%+v", errs)
	}
}

func TestValidatePreset_GuardianSkillPrereqStillEnforced(t *testing.T) {
	cfg := DefaultConfig()
	jobSkills := heavyKnightSkillsFixture()
	stats := Stats{Str: 25, Agi: 1, Vit: 25, Dex: 1, Int: 1, Luk: 1}
	// hk_c2 需要 hk_c1>=3，這裡只給 1 級。
	errs := ValidatePreset(cfg, jobSkills, 25, stats, map[string]int{"hk_c1": 1, "hk_c2": 1})
	if !hasCode(errs, "skill_prereq") {
		t.Fatalf("hk_c2 前置不足應回報 skill_prereq：%+v", errs)
	}
}

func TestValidatePreset_GuardianSkillMaxLevelStillEnforced(t *testing.T) {
	cfg := DefaultConfig()
	jobSkills := heavyKnightSkillsFixture()
	stats := Stats{Str: 25, Agi: 1, Vit: 25, Dex: 1, Int: 1, Luk: 1}
	errs := ValidatePreset(cfg, jobSkills, 25, stats, map[string]int{"hk_c3": 6}) // hk_c3 max_level=5
	if !hasCode(errs, "skill_max_level") {
		t.Fatalf("hk_c3 超過 max_level 應回報 skill_max_level：%+v", errs)
	}
}

// ---------------------------------------------------------------------------
// migration 187 seed 自我核對：item id / skill id 存在於 180/183/184（單一真相防漂移，比照
// presets_equipment_test.go TestSystemPresetEquipment_ItemIDsExistInMigrationSeeds 既有慣例）。
// ---------------------------------------------------------------------------

func readMigrationFile(t *testing.T, name string) string {
	t.Helper()
	path := "../../migrations/" + name
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("讀取 %s 失敗：%v", path, err)
	}
	return string(raw)
}

func TestMigration187_HKGuardianSkillIDsMatchPresetSeedFixture(t *testing.T) {
	content := readMigrationFile(t, "187_rpg_p10_guardian.sql")
	for _, id := range []string{"hk_c1", "hk_c2", "hk_c3", "hk_c4"} {
		if !strings.Contains(content, "'"+id+"'") {
			t.Errorf("187 的 rpg_skills INSERT 應含 %s", id)
		}
	}
	// 阿深新配置的技能 id 應同時出現在 187 的系統預設 UPDATE 裡（單一真相：跟
	// presets_test.go systemPresetSeeds() 的 char_ashen 那筆逐值相同）。
	if !strings.Contains(content, `{"hk_b1":5,"hk_b2":6,"hk_b3":3,"hk_c1":3,"hk_c2":3,"hk_c3":4}`) {
		t.Errorf("187 的阿深 skill_levels 應與 presets_test.go systemPresetSeeds() 逐值相同")
	}
}

func TestMigration187_CompanionEquipmentIDsExistInMigration183And184(t *testing.T) {
	weaponIDs := []string{"mg_staff_t3", "cl_staff_t3", "ar_longbow_t3"}
	armorIDs := []string{
		"mg_helmet_t3", "mg_gloves_t3", "mg_armor_t3", "mg_legs_t3", "mg_boots_t3", "acc_mp_t2", "acc_mpregen_t2",
		"cl_helmet_t3", "cl_gloves_t3", "cl_armor_t3", "cl_legs_t3", "cl_boots_t3", "acc_hp_t2",
		"ar_helmet_t3", "ar_gloves_t3", "ar_armor_t3", "ar_legs_t3", "ar_boots_t3", "acc_crit_t2", "acc_agi_t2",
	}
	weapons183 := readMigrationFile(t, "183_rpg_weapons_elements.sql")
	for _, id := range weaponIDs {
		if !strings.Contains(weapons183, "'"+id+"',") {
			t.Errorf("183 應含武器 %s（187 的系統預設裝備引用了它）", id)
		}
	}
	armor184 := readMigrationFile(t, "184_rpg_armor.sql")
	for _, id := range armorIDs {
		if !strings.Contains(armor184, "'"+id+"'") {
			t.Errorf("184 應含防具/飾品 %s（187 的系統預設裝備引用了它）", id)
		}
	}
}

// TestMagePathASkillsFixture_MatchesMigration180 對照 magePathASkills()（presets_test.go）逐欄
// 核對 180_rpg_jobs_skills.sql 第 158~161 行 mg_a1~mg_a4 的 tier/max_level/prereq_skill_id/
// prereq_level（單一真相：改一邊要記得改另一邊，比照既有 heavyKnightSkillsFixture() 檔頭同款
// 保險）。mg_a5 刻意不核對，理由同 fixture 檔頭註解（系統預設沒有投資它）。
func TestMagePathASkillsFixture_MatchesMigration180(t *testing.T) {
	content := readMigrationFile(t, "180_rpg_jobs_skills.sql")
	for _, needle := range []string{
		"'mage', 'a', 1, 5,",      // mg_a1：tier=1 max_level=5，無前置
		"'mage', 'a', 2, 5,",      // mg_a2：tier=2 max_level=5
		"'mage', 'a', 3, 10,",     // mg_a3：tier=3 max_level=10
		"'mage', 'a', 4, 10,",     // mg_a4：tier=4 max_level=10
		"'magic', 'mg_a1', 3, 1,", // mg_a2 前置 mg_a1>=3
		"'magic', 'mg_a2', 3, 1,", // mg_a3 前置 mg_a2>=3
		"'magic', 'mg_a3', 5, 0,", // mg_a4 前置 mg_a3>=5
	} {
		if !strings.Contains(content, needle) {
			t.Errorf("180 應含 %q（magePathASkills() fixture 依此逐欄核對）", needle)
		}
	}
}

func TestMigration187_CompanionJobReassignmentsPresent(t *testing.T) {
	content := readMigrationFile(t, "187_rpg_p10_guardian.sql")
	for _, needle := range []string{
		"job_id = 'mage'", "job_id = 'cleric'", "job_id = 'archer'",
		"heavy_knight_guardian_wall",
		`'{"damage_taken_pct":-15}'`,
	} {
		if !strings.Contains(content, needle) {
			t.Errorf("187 應含 %q", needle)
		}
	}
}
