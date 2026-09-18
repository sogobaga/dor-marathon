// jobs_test.go：DORPG P7 換職業自動卸下的純函式部分。DB 讀寫本身（PutJob 端點）留給 Neon 分支
// 整合測試，比照 tavern_test.go 檔頭的既有慣例。
package rpg

import "testing"

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
