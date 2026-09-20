// content_test.go：對抗式審查 dorpg_p5 修正輪 #4【低・CONFIRMED】的根因回歸測試——
// MonsterRow.Validate() 過去完全不檢查 weak_elements 的值域，後台存進打錯字的屬性代碼
// （例如 "fier"）不會有任何錯誤，這隻怪的弱點只會在 engine 的 elementMultiplier() 比對時
// 安靜地永遠命中不到，難以排查。見 content.go MonsterRow.Validate() 的修復註解。
package rpg

import "testing"

func TestMonsterRow_ValidateRejectsUnknownWeakElement(t *testing.T) {
	m := MonsterRow{ID: "m1", Name: "測試怪", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1, WeakElements: []string{"fire", "fier"}}
	if err := m.Validate(); err == nil {
		t.Fatalf("weak_elements 內含未知屬性 'fier'（打錯字）應被拒絕")
	}
}

func TestMonsterRow_ValidateAcceptsKnownWeakElements(t *testing.T) {
	// DORPG P11：Rank 加了九值白名單檢查（見 content.go MonsterRow.Validate()），這裡補上合法值
	// 讓本測試繼續只測 weak_elements 本身，不被無關的 rank 檢查擋下。
	m := MonsterRow{ID: "m1", Name: "測試怪", Rank: "F", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1, WeakElements: []string{"fire", "water"}}
	if err := m.Validate(); err != nil {
		t.Fatalf("合法的 weak_elements 不應被拒絕：%v", err)
	}
}

func TestMonsterRow_ValidateAcceptsEmptyWeakElements(t *testing.T) {
	m := MonsterRow{ID: "m1", Name: "測試怪", Rank: "F", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1}
	if err := m.Validate(); err != nil {
		t.Fatalf("weak_elements 為空（無弱點）應合法：%v", err)
	}
}
