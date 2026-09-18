// jobs_test.go：DORPG P7/P8 換職業自動卸下的純函式部分。DB 讀寫本身（PutJob 端點）留給 Neon 分支
// 整合測試，比照 tavern_test.go 檔頭的既有慣例。
package rpg

import "testing"

// TestArmorSlotsClearedOnJobChange_ExcludesAccessoriesAndWeapon CONTRACT §1「換職業時自動卸下
// 五部位防具，飾品保留」——這裡直接對照 armorSlotsClearedOnJobChange（PutJob 換職業時 DELETE
// 的 slot 清單）：必須剛好是五部位防具，accessory1/accessory2（飾品）與 weapon（武器另有自己
// 的 shouldUnequipOnJobChange 判斷邏輯，不该被這份清單重複卸下）都不可以出現在裡面。
func TestArmorSlotsClearedOnJobChange_ExcludesAccessoriesAndWeapon(t *testing.T) {
	want := map[string]bool{"helmet": true, "gloves": true, "armor": true, "legs": true, "boots": true}
	if len(armorSlotsClearedOnJobChange) != len(want) {
		t.Fatalf("armorSlotsClearedOnJobChange 應該剛好五格，got %v", armorSlotsClearedOnJobChange)
	}
	seen := map[string]bool{}
	for _, slot := range armorSlotsClearedOnJobChange {
		if !want[slot] {
			t.Fatalf("armorSlotsClearedOnJobChange 出現不該卸下的格子 %q（飾品應保留、武器不該重複卸）", slot)
		}
		seen[slot] = true
	}
	for slot := range want {
		if !seen[slot] {
			t.Fatalf("armorSlotsClearedOnJobChange 漏了防具格子 %q", slot)
		}
	}
}

func TestShouldUnequipOnJobChange_ClearingJobUnequips(t *testing.T) {
	if !shouldUnequipOnJobChange("light_knight", nil) {
		t.Fatalf("清除職業（newJobID=nil）應該卸下目前的武器")
	}
}

func TestShouldUnequipOnJobChange_MismatchedJobUnequips(t *testing.T) {
	newJob := "archer"
	if !shouldUnequipOnJobChange("light_knight", &newJob) {
		t.Fatalf("武器所屬職業與新職業不符應該卸下")
	}
}

func TestShouldUnequipOnJobChange_SameJobKeepsWeapon(t *testing.T) {
	newJob := "light_knight"
	if shouldUnequipOnJobChange("light_knight", &newJob) {
		t.Fatalf("換成同一個職業不應該卸下武器")
	}
}

// TestShouldClearArmorOnJobChange 根因修復回歸測試：PutJob 原本不管 job_id 是否真的改變都無條件
// 清五部位防具，玩家對「目前職業」重複送出同一個 PUT 就會誤清。這裡直接對 shouldClearArmorOnJobChange
// 表格測試涵蓋「同職業不清」與「其餘情況（含 nil/空↔有值）都要清」。
func TestShouldClearArmorOnJobChange(t *testing.T) {
	cases := []struct {
		name          string
		oldJob        string // PutJob 呼叫端把 *string 攤平成字串，""＝沒有職業（nil）
		newJob        string
		wantShouldClr bool
	}{
		{"同職業不清", "light_knight", "light_knight", false},
		{"不同職業要清", "light_knight", "archer", true},
		{"舊沒有職業_新有值要清", "", "light_knight", true},
		{"舊有值_新沒有職業要清", "light_knight", "", true},
		{"兩邊都沒有職業不清", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldClearArmorOnJobChange(tc.oldJob, tc.newJob); got != tc.wantShouldClr {
				t.Fatalf("shouldClearArmorOnJobChange(%q, %q) = %v，want %v", tc.oldJob, tc.newJob, got, tc.wantShouldClr)
			}
		})
	}
}
