// equipment_test.go：canEquipWeapon 純函式測試（不連 DB，見該函式檔頭「留給 Neon 分支整合測試」
// 的既有慣例，比照 weapons_test.go）。
// 審查#2【中】回歸測試：下架武器（is_active=false）即使 job/等級都符合，仍必須擋下，不能靠 id
// 繞過裝備。
package rpg

import "testing"

func TestCanEquipWeapon_InactiveRejected(t *testing.T) {
	jobID := "warrior"
	weapon := WeaponRow{ID: "w1", TypeID: "t1", LevelReq: 1, IsActive: false}
	wtype := WeaponTypeRow{ID: "t1", JobID: jobID}
	if code := canEquipWeapon(weapon, wtype, &jobID, 99); code != "not_found" {
		t.Fatalf("下架武器（is_active=false）即使 job/等級都符合，也必須回 not_found，got %q", code)
	}
}

func TestCanEquipWeapon_ActiveAllowed(t *testing.T) {
	jobID := "warrior"
	weapon := WeaponRow{ID: "w1", TypeID: "t1", LevelReq: 1, IsActive: true}
	wtype := WeaponTypeRow{ID: "t1", JobID: jobID}
	if code := canEquipWeapon(weapon, wtype, &jobID, 99); code != "" {
		t.Fatalf("上架武器、job/等級都符合，應可裝備（空字串），got %q", code)
	}
}

func TestCanEquipWeapon_WrongJobRejected(t *testing.T) {
	jobID := "warrior"
	otherJobID := "mage"
	weapon := WeaponRow{ID: "w1", TypeID: "t1", LevelReq: 1, IsActive: true}
	wtype := WeaponTypeRow{ID: "t1", JobID: otherJobID}
	if code := canEquipWeapon(weapon, wtype, &jobID, 99); code != "wrong_job" {
		t.Fatalf("武器所屬武器類型的 job 跟角色目前職業不符，應回 wrong_job，got %q", code)
	}
}

func TestCanEquipWeapon_NoJobRejected(t *testing.T) {
	weapon := WeaponRow{ID: "w1", TypeID: "t1", LevelReq: 1, IsActive: true}
	wtype := WeaponTypeRow{ID: "t1", JobID: "warrior"}
	if code := canEquipWeapon(weapon, wtype, nil, 99); code != "wrong_job" {
		t.Fatalf("角色尚未選職業（jobID=nil），應回 wrong_job，got %q", code)
	}
}

func TestCanEquipWeapon_LevelTooLowRejected(t *testing.T) {
	jobID := "warrior"
	weapon := WeaponRow{ID: "w1", TypeID: "t1", LevelReq: 50, IsActive: true}
	wtype := WeaponTypeRow{ID: "t1", JobID: jobID}
	if code := canEquipWeapon(weapon, wtype, &jobID, 10); code != "level_too_low" {
		t.Fatalf("有效等級低於 level_req，應回 level_too_low，got %q", code)
	}
}

// 優先序：is_active 檢查放最前面——即使 job/等級同時不符，下架這件事最優先回報。
func TestCanEquipWeapon_InactiveTakesPriorityOverOtherFailures(t *testing.T) {
	jobID := "warrior"
	weapon := WeaponRow{ID: "w1", TypeID: "t1", LevelReq: 50, IsActive: false}
	wtype := WeaponTypeRow{ID: "t1", JobID: "mage"}
	if code := canEquipWeapon(weapon, wtype, &jobID, 1); code != "not_found" {
		t.Fatalf("is_active 檢查應優先於 job/等級檢查，got %q", code)
	}
}
