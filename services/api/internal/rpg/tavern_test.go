// tavern_test.go：DORPG P6（CONTRACT §3.1/§3.2）不連 DB 的純函式部分——預設隊伍常數、
// buildCompanionSkillsWire 的技能欄篩選規則（level>=1、非 passive、implemented=true）。
// 連 DB 的部分（GET /rpg/tavern、PUT /rpg/party、resolvePartyForBattle）留給 Neon 分支整合
// 測試（見任務回報），這裡只測不需要 pgxpool 就能驗證的邏輯。
package rpg

import "testing"

func TestDefaultPartySlots_XiaomiSlotOne(t *testing.T) {
	slots := defaultPartySlots()
	if len(slots) != 1 {
		t.Fatalf("契約 §3.1：沒有任何列時的預設隊伍應只有 1 筆（小咪），got %d 筆", len(slots))
	}
	if slots[0].Slot != 1 || slots[0].CompanionID != "char_xiaomi" || slots[0].PresetID != nil {
		t.Fatalf("預設隊伍應是「小咪佔第 1 格、preset_id=nil（退回系統預設）」，got %+v", slots[0])
	}
}

func TestBuildCompanionSkillsWire_FiltersUnleveledPassiveAndUnimplemented(t *testing.T) {
	jobSkills := []SkillRow{
		{ID: "a1", Kind: "damage", Implemented: true, MaxLevel: 10},  // level>=1 且 implemented → 應出現
		{ID: "a2", Kind: "passive", Implemented: true, MaxLevel: 10}, // passive → 即使 level>=1 也不應出現
		{ID: "a3", Kind: "damage", Implemented: false, MaxLevel: 5},  // 未實裝 → 不應出現
		{ID: "a4", Kind: "damage", Implemented: true, MaxLevel: 5},   // level=0（未配點）→ 不應出現
	}
	levels := map[string]int{"a1": 5, "a2": 5, "a3": 3}
	out := buildCompanionSkillsWire(jobSkills, levels)

	if len(out) != 1 {
		t.Fatalf("應只有 a1 通過篩選，got %d 個：%+v", len(out), out)
	}
	if out[0].ID != "a1" {
		t.Fatalf("唯一通過篩選的應是 a1，got %s", out[0].ID)
	}
}

func TestBuildCompanionSkillsWire_EmptyWhenNoSkillLevels(t *testing.T) {
	jobSkills := []SkillRow{{ID: "a1", Kind: "damage", Implemented: true, MaxLevel: 10}}
	out := buildCompanionSkillsWire(jobSkills, map[string]int{})
	if len(out) != 0 {
		t.Fatalf("完全沒有配點時技能欄應為空陣列，got %+v", out)
	}
}

// TestClampSkillLevelsToMax_ClampsOverMaxAndNegative 執行期防線的單元測試：重現 182 要修的
// 那類事故（skill_levels 裡有一筆超過 max_level 的髒資料，例如 migration 181 的 hk_b3=6 但
// max_level=5）——resolvePartyForBattle 組隊友資料時不該讓超額等級流進 Compute()/技能欄。
func TestClampSkillLevelsToMax_ClampsOverMaxAndNegative(t *testing.T) {
	jobSkills := []SkillRow{
		{ID: "hk_b3", Kind: "buff", MaxLevel: 5},
		{ID: "hk_b4", Kind: "shield", MaxLevel: 5},
	}
	in := map[string]int{"hk_b3": 6, "hk_b4": 3, "unknown_skill": -1}
	out := clampSkillLevelsToMax("preset-x", jobSkills, in)

	if out["hk_b3"] != 5 {
		t.Fatalf("hk_b3 超過 max_level=5 應被夾到 5，got %d", out["hk_b3"])
	}
	if out["hk_b4"] != 3 {
		t.Fatalf("hk_b4 在合法範圍內不應被更動，got %d", out["hk_b4"])
	}
	if out["unknown_skill"] != 0 {
		t.Fatalf("負值應被夾到 0（即使查無對應職業技能），got %d", out["unknown_skill"])
	}
}

func TestClampSkillLevelsToMax_EmptyInputReturnsEmpty(t *testing.T) {
	out := clampSkillLevelsToMax("preset-x", []SkillRow{{ID: "a1", MaxLevel: 5}}, map[string]int{})
	if len(out) != 0 {
		t.Fatalf("空輸入應回傳空 map，got %+v", out)
	}
}
