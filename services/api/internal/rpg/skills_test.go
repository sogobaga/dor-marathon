// skills_test.go：對抗式審查 dorpg_p5 修正輪 #3【中低・CONFIRMED】的根因回歸測試——降級
// 技能（delta=-1）前沒有檢查是否有其他「已投資」技能把它當前置，會出現「A 降到 0 後 B 仍停留在
// 需要 A>=3 才能點滿的 Lv5」這種前置條件事後失效、但既有配點沒有連動的不一致狀態。
// violatesPrereqDependents 抽成純函式（見 skills.go），這裡不連 DB 直接測。
package rpg

import "testing"

func strPtr(s string) *string { return &s }

func TestViolatesPrereqDependents_BlocksWhenDependentInvestedAboveNewLevel(t *testing.T) {
	skillA := SkillRow{ID: "a"}
	skillB := SkillRow{ID: "b", PrereqSkillID: strPtr("a"), PrereqLevel: 3}
	jobSkills := []SkillRow{skillA, skillB}
	levels := map[string]int{"a": 5, "b": 5} // B 已投資到 Lv5，前置要求 A>=3

	if !violatesPrereqDependents(jobSkills, levels, "a", 2) {
		t.Fatalf("B 已投資且 prereq_level=3 > newLevel=2，A 降級應被擋")
	}
}

func TestViolatesPrereqDependents_AllowsWhenNewLevelStillMeetsPrereq(t *testing.T) {
	skillA := SkillRow{ID: "a"}
	skillB := SkillRow{ID: "b", PrereqSkillID: strPtr("a"), PrereqLevel: 3}
	jobSkills := []SkillRow{skillA, skillB}
	levels := map[string]int{"a": 5, "b": 5}

	if violatesPrereqDependents(jobSkills, levels, "a", 3) {
		t.Fatalf("newLevel=3 仍滿足 B 的 prereq_level=3，不該被擋")
	}
}

func TestViolatesPrereqDependents_AllowsWhenDependentNotInvested(t *testing.T) {
	skillA := SkillRow{ID: "a"}
	skillB := SkillRow{ID: "b", PrereqSkillID: strPtr("a"), PrereqLevel: 3}
	jobSkills := []SkillRow{skillA, skillB}
	levels := map[string]int{"a": 5, "b": 0} // B 尚未投資

	if violatesPrereqDependents(jobSkills, levels, "a", 0) {
		t.Fatalf("B 尚未投資（level=0），A 降到 0 不該被擋")
	}
}

func TestViolatesPrereqDependents_UnrelatedSkillsDoNotInterfere(t *testing.T) {
	skillA := SkillRow{ID: "a"}
	skillC := SkillRow{ID: "c", PrereqSkillID: strPtr("x"), PrereqLevel: 1} // 前置是別的技能，跟 a 無關
	jobSkills := []SkillRow{skillA, skillC}
	levels := map[string]int{"a": 5, "c": 5}

	if violatesPrereqDependents(jobSkills, levels, "a", 0) {
		t.Fatalf("C 的前置技能不是 A，A 降級不該被 C 擋住")
	}
}

func TestViolatesPrereqDependents_NoPrereqSkillIDNeverBlocks(t *testing.T) {
	skillA := SkillRow{ID: "a"}
	skillB := SkillRow{ID: "b", PrereqSkillID: nil} // 沒有前置技能
	jobSkills := []SkillRow{skillA, skillB}
	levels := map[string]int{"a": 5, "b": 5}

	if violatesPrereqDependents(jobSkills, levels, "a", 0) {
		t.Fatalf("B 沒有 PrereqSkillID，不該把任何降級擋下")
	}
}
