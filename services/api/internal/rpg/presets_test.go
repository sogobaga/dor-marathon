// presets_test.go：DORPG P6（CONTRACT §3.2）ValidatePreset() 純函式測試——合法／超預算／
// 超 cap／前置／非本職技能，以及 migration 181 要 seed 的四筆系統預設腳本本身的合法性
// （單一真相：這裡的 Stats/SkillLevels 常數必須跟 migration 181 SQL 裡貼的 JSON 逐值相同，
// 改一邊要記得改另一邊，這個測試就是防止兩邊漂移的唯一保險）。
package rpg

import (
	"encoding/json"
	"testing"
)

// jobSkillsFixture 造一份最小可用的技能樹：a1(無前置,max10)→a2(prereq a1>=3,max10,passive)→
// a3(prereq a2>=3,max5)，跟 migration 180 六職業的路線 a 結構同一種模式，足夠測前置鏈/上限/
// 超本職技能三種情境，不需要真的連 DB 撈 60 筆技能。
func jobSkillsFixture() []SkillRow {
	return []SkillRow{
		{ID: "a1", Kind: "damage", MaxLevel: 10},
		{ID: "a2", Kind: "passive", MaxLevel: 10, PrereqSkillID: strPtr("a1"), PrereqLevel: 3},
		{ID: "a3", Kind: "damage", MaxLevel: 5, PrereqSkillID: strPtr("a2"), PrereqLevel: 3},
	}
}

func hasCode(errs []PresetError, code string) bool {
	for _, e := range errs {
		if e.Code == code {
			return true
		}
	}
	return false
}

func TestValidatePreset_LegalConfigHasNoErrors(t *testing.T) {
	cfg := DefaultConfig()
	stats := Stats{Str: 1, Agi: 1, Vit: 10, Dex: 20, Int: 25, Luk: 5} // 同 migration 181 小咪 seed
	skills := map[string]int{"a1": 5, "a2": 5}
	if errs := ValidatePreset(cfg, jobSkillsFixture(), 25, stats, skills); len(errs) != 0 {
		t.Fatalf("合法配置不應有錯誤：%+v", errs)
	}
}

// TestValidatePreset_LegalDraftJSONHasEmptyErrorsArray 審查【CRITICAL】回歸測試：合法草稿的
// errs 若是 nil slice（Go 的零值），json.Marshal 會編碼成 null 而非 []——PresetsValidate 一律回
// `"errors": errs`，前端 TavernScreen.tsx 的 errorFor() 對 null 呼叫 .find(...) 會直接 TypeError，
// 導致任何合法草稿都無法編輯／儲存。這裡直接斷言 JSON 輸出字面值，防止之後有人把 errs 又改回
// `var errs []PresetError`（那樣寫在 Go 語意上「看起來」等價，但序列化結果不同，光靠 len(errs)==0
// 的斷言測不出這個差異，必須真的 Marshal 才驗證得到）。
func TestValidatePreset_LegalDraftJSONHasEmptyErrorsArray(t *testing.T) {
	cfg := DefaultConfig()
	stats := Stats{Str: 1, Agi: 1, Vit: 10, Dex: 20, Int: 25, Luk: 5} // 同 migration 181 小咪 seed，合法配置
	skills := map[string]int{"a1": 5, "a2": 5}
	errs := ValidatePreset(cfg, jobSkillsFixture(), 25, stats, skills)
	if errs == nil {
		t.Fatalf("合法配置的 errs 不可為 nil slice（會被 json.Marshal 編碼成 null）")
	}
	b, err := json.Marshal(map[string]any{"errors": errs})
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if string(b) != `{"errors":[]}` {
		t.Fatalf(`合法草稿的 errors 應序列化成 []，got %s`, string(b))
	}
}

func TestValidatePreset_StatOverBudget(t *testing.T) {
	cfg := DefaultConfig()
	stats := Stats{Str: 99, Agi: 99, Vit: 99, Dex: 99, Int: 99, Luk: 99} // 遠超 Lv25 的 170 點預算
	errs := ValidatePreset(cfg, jobSkillsFixture(), 25, stats, nil)
	if !hasCode(errs, "stat_over_budget") {
		t.Fatalf("超預算應回報 stat_over_budget：%+v", errs)
	}
	if !hasCode(errs, "stat_over_cap") {
		t.Fatalf("99 也超過 Lv25 的 cap=25，應同時回報 stat_over_cap：%+v", errs)
	}
}

func TestValidatePreset_StatOverCap(t *testing.T) {
	cfg := DefaultConfig()
	stats := Stats{Str: 26, Agi: 1, Vit: 1, Dex: 1, Int: 1, Luk: 1} // Lv25 cap=25，26 超過
	errs := ValidatePreset(cfg, jobSkillsFixture(), 25, stats, nil)
	if !hasCode(errs, "stat_over_cap") {
		t.Fatalf("超過 cap 應回報 stat_over_cap：%+v", errs)
	}
}

func TestValidatePreset_StatBelowInitial(t *testing.T) {
	cfg := DefaultConfig()
	stats := Stats{Str: 0, Agi: 1, Vit: 1, Dex: 1, Int: 1, Luk: 1} // InitialStat=1，0 不合法
	errs := ValidatePreset(cfg, jobSkillsFixture(), 25, stats, nil)
	if !hasCode(errs, "stat_below_initial") {
		t.Fatalf("低於初始值應回報 stat_below_initial：%+v", errs)
	}
}

func TestValidatePreset_SkillPrereqNotMet(t *testing.T) {
	cfg := DefaultConfig()
	stats := Stats{Str: 1, Agi: 1, Vit: 1, Dex: 1, Int: 1, Luk: 1}
	// a2 需要 a1>=3，這裡 a1 只有 1 級。
	errs := ValidatePreset(cfg, jobSkillsFixture(), 25, stats, map[string]int{"a1": 1, "a2": 1})
	if !hasCode(errs, "skill_prereq") {
		t.Fatalf("前置不足應回報 skill_prereq：%+v", errs)
	}
}

func TestValidatePreset_SkillMaxLevelExceeded(t *testing.T) {
	cfg := DefaultConfig()
	stats := Stats{Str: 1, Agi: 1, Vit: 1, Dex: 1, Int: 1, Luk: 1}
	errs := ValidatePreset(cfg, jobSkillsFixture(), 25, stats, map[string]int{"a3": 6}) // a3 max_level=5
	if !hasCode(errs, "skill_max_level") {
		t.Fatalf("超過 max_level 應回報 skill_max_level：%+v", errs)
	}
}

func TestValidatePreset_SkillNotInJob(t *testing.T) {
	cfg := DefaultConfig()
	stats := Stats{Str: 1, Agi: 1, Vit: 1, Dex: 1, Int: 1, Luk: 1}
	errs := ValidatePreset(cfg, jobSkillsFixture(), 25, stats, map[string]int{"not_this_job": 3})
	if !hasCode(errs, "skill_not_in_job") {
		t.Fatalf("非本職技能應回報 skill_not_in_job：%+v", errs)
	}
}

func TestValidatePreset_SkillOverBudget(t *testing.T) {
	cfg := DefaultConfig()
	stats := Stats{Str: 1, Agi: 1, Vit: 1, Dex: 1, Int: 1, Luk: 1}
	// Lv25 技能點預算=24；a1 灌到 10、a2 灌到 10、a3 灌到 5 共 25 點，超過 1 點。
	errs := ValidatePreset(cfg, jobSkillsFixture(), 25, stats, map[string]int{"a1": 10, "a2": 10, "a3": 5})
	if !hasCode(errs, "skill_over_budget") {
		t.Fatalf("超過技能點預算應回報 skill_over_budget：%+v", errs)
	}
}

func TestValidatePreset_LevelRangeClampsButStillChecksRest(t *testing.T) {
	cfg := DefaultConfig()
	stats := Stats{Str: 1, Agi: 1, Vit: 1, Dex: 1, Int: 1, Luk: 1}
	errs := ValidatePreset(cfg, jobSkillsFixture(), 150, stats, nil) // 超出 1..99
	if !hasCode(errs, "level_range") {
		t.Fatalf("超出 1..99 應回報 level_range：%+v", errs)
	}
}

// ---------------------------------------------------------------------------
// migration 181 四筆系統預設腳本——單一真相：這裡的常數必須跟 SQL seed 逐值相同。
// ---------------------------------------------------------------------------

// systemPresetSeed 對照 migration 181 SQL 的一筆系統預設腳本（companionID 只用來標註測試失敗
// 訊息，ValidatePreset 本身不需要它）。
type systemPresetSeed struct {
	companionID string
	jobSkills   []SkillRow // 該職業路線相關的技能子集（只需要涵蓋 skillLevels 用到的 id 與其前置鏈）
	stats       Stats
	skillLevels map[string]int
}

// cleric/archer/light_knight/heavy_knight 路線技能子集，逐欄對照 migration 180 seed 的
// tier/max_level/prereq（見該檔 §3 SKILLS SEED 區塊）——只挑本測試會用到的欄位，其餘（icon_id/
// mp_cost 等）留零值不影響 ValidatePreset。
func clericPathASkills() []SkillRow {
	return []SkillRow{
		{ID: "cl_a1", Kind: "heal", MaxLevel: 5},
		{ID: "cl_a2", Kind: "buff", MaxLevel: 5, PrereqSkillID: strPtr("cl_a1"), PrereqLevel: 3},
		{ID: "cl_a3", Kind: "heal", MaxLevel: 10, PrereqSkillID: strPtr("cl_a2"), PrereqLevel: 3},
		{ID: "cl_a4", Kind: "passive", MaxLevel: 10, PrereqSkillID: strPtr("cl_a3"), PrereqLevel: 5},
	}
}

func archerPathASkills() []SkillRow {
	return []SkillRow{
		{ID: "ar_a1", Kind: "damage", MaxLevel: 10},
		{ID: "ar_a2", Kind: "passive", MaxLevel: 10, PrereqSkillID: strPtr("ar_a1"), PrereqLevel: 3},
		{ID: "ar_a3", Kind: "buff", MaxLevel: 5, PrereqSkillID: strPtr("ar_a2"), PrereqLevel: 3},
	}
}

func lightKnightPathASkills() []SkillRow {
	return []SkillRow{
		{ID: "lk_a1", Kind: "damage", MaxLevel: 10},
		{ID: "lk_a2", Kind: "passive", MaxLevel: 10, PrereqSkillID: strPtr("lk_a1"), PrereqLevel: 3},
		{ID: "lk_a3", Kind: "buff", MaxLevel: 5, PrereqSkillID: strPtr("lk_a2"), PrereqLevel: 3},
	}
}

// heavyKnightPathBSkills 逐欄對照 migration 180 seed（180_rpg_jobs_skills.sql 第 133~136
// 行）：hk_b1/hk_b2 max_level=10，hk_b3/hk_b4 max_level=5——hk_b3 之前誤抄成 10（跟
// hk_b1/hk_b2 一樣），讓 182 要修的 hk_b3=6 違規配置在這裡「通過」了驗證，是本次資料事故的
// 根因；改回真實值 5 後，這個 fixture 才真的能攔住超過上限的 seed。
func heavyKnightPathBSkills() []SkillRow {
	return []SkillRow{
		{ID: "hk_b1", Kind: "damage", MaxLevel: 10},
		{ID: "hk_b2", Kind: "passive", MaxLevel: 10, PrereqSkillID: strPtr("hk_b1"), PrereqLevel: 3},
		{ID: "hk_b3", Kind: "buff", MaxLevel: 5, PrereqSkillID: strPtr("hk_b2"), PrereqLevel: 3},
		{ID: "hk_b4", Kind: "shield", MaxLevel: 5, PrereqSkillID: strPtr("hk_b3"), PrereqLevel: 3},
	}
}

func systemPresetSeeds() []systemPresetSeed {
	return []systemPresetSeed{
		{
			companionID: "char_xiaomi (cleric)",
			jobSkills:   clericPathASkills(),
			stats:       Stats{Str: 1, Agi: 1, Vit: 10, Dex: 20, Int: 25, Luk: 5},
			skillLevels: map[string]int{"cl_a1": 5, "cl_a2": 5, "cl_a3": 10},
		},
		{
			companionID: "char_xiaoyou (archer)",
			jobSkills:   archerPathASkills(),
			stats:       Stats{Str: 1, Agi: 25, Vit: 1, Dex: 20, Int: 1, Luk: 15},
			skillLevels: map[string]int{"ar_a1": 10, "ar_a2": 3, "ar_a3": 5},
		},
		{
			companionID: "char_aguang (light_knight)",
			jobSkills:   lightKnightPathASkills(),
			stats:       Stats{Str: 22, Agi: 20, Vit: 1, Dex: 1, Int: 1, Luk: 15},
			skillLevels: map[string]int{"lk_a1": 10, "lk_a2": 9, "lk_a3": 5},
		},
		{
			// migration 182 修正值（原 181 的 {"hk_b1":7,"hk_b2":6,"hk_b3":6,"hk_b4":5} 讓
			// hk_b3 超過 max_level=5，見 182_rpg_preset_seed_fix.sql 檔頭根因說明）。
			companionID: "char_ashen (heavy_knight)",
			jobSkills:   heavyKnightPathBSkills(),
			stats:       Stats{Str: 25, Agi: 1, Vit: 25, Dex: 1, Int: 1, Luk: 1},
			skillLevels: map[string]int{"hk_b1": 8, "hk_b2": 6, "hk_b3": 5, "hk_b4": 5},
		},
	}
}

// TestSystemPresetSeeds_AreValidAtLevel25 契約 §3.1：「你必須用同一套驗證函式在 Go 測試裡驗證
// 這四筆 seed 合法：預算 170、cap 25、技能點 24、前置鏈、只含該職業技能」——這個測試就是那份
// 驗證；同時明確斷言預算/cap/技能點三個數字，改了 DefaultConfig() 的配點公式而沒有同步調整
// migration 181 的 seed 值時，這裡會先 FAIL 而不是等到套 DB 才發現。
func TestSystemPresetSeeds_AreValidAtLevel25(t *testing.T) {
	cfg := DefaultConfig()
	const level = 25

	if got := TotalStatPoints(cfg, level); got != 170 {
		t.Fatalf("契約：Lv25 配點預算應為 170，got %d（DefaultConfig 的配點公式跟契約算的不一致，"+
			"migration 181 的四筆 seed 是照 170 這個數字設計的）", got)
	}
	if got := StatCap(cfg, level); got != 25 {
		t.Fatalf("契約：Lv25 cap 應為 25，got %d", got)
	}
	if got := TotalSkillPoints(cfg, level); got != 24 {
		t.Fatalf("契約：Lv25 技能點應為 24，got %d", got)
	}

	for _, seed := range systemPresetSeeds() {
		t.Run(seed.companionID, func(t *testing.T) {
			errs := ValidatePreset(cfg, seed.jobSkills, level, seed.stats, seed.skillLevels)
			if len(errs) != 0 {
				t.Fatalf("%s 的系統預設腳本不合法：%+v（stats=%+v skills=%+v）",
					seed.companionID, errs, seed.stats, seed.skillLevels)
			}
			spent := TotalSpentStats(cfg, seed.stats)
			if spent > 170 {
				t.Fatalf("%s 配點花費 %d 超過 170", seed.companionID, spent)
			}
			skillSpent := 0
			for _, lvl := range seed.skillLevels {
				skillSpent += lvl
			}
			if skillSpent > 24 {
				t.Fatalf("%s 技能點花費 %d 超過 24", seed.companionID, skillSpent)
			}
		})
	}
}
