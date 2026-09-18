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

// --- canEquipArmor：DORPG P8（CONTRACT §1「取得與裝備規則」、WIRE 五種錯誤碼）純函式測試，
// 不連 DB，比照上面 canEquipWeapon 的既有慣例。---

func TestCanEquipArmor_InactiveRejected(t *testing.T) {
	jobID := "light_knight"
	item := ArmorRow{ID: "a1", JobID: &jobID, Slot: "helmet", LevelReq: 1, IsActive: false}
	if code := canEquipArmor(item, "helmet", &jobID, 99, ""); code != "not_found" {
		t.Fatalf("下架防具即使 job/等級都符合，也必須回 not_found，got %q", code)
	}
}

func TestCanEquipArmor_ActiveArmorAllowed(t *testing.T) {
	jobID := "light_knight"
	item := ArmorRow{ID: "a1", JobID: &jobID, Slot: "helmet", LevelReq: 1, IsActive: true}
	if code := canEquipArmor(item, "helmet", &jobID, 99, ""); code != "" {
		t.Fatalf("上架防具、job/等級都符合，應可裝備（空字串），got %q", code)
	}
}

func TestCanEquipArmor_WrongSlotRejected(t *testing.T) {
	jobID := "light_knight"
	item := ArmorRow{ID: "a1", JobID: &jobID, Slot: "helmet", LevelReq: 1, IsActive: true}
	if code := canEquipArmor(item, "gloves", &jobID, 99, ""); code != "wrong_slot" {
		t.Fatalf("item.slot=helmet 裝去 gloves 格應回 wrong_slot，got %q", code)
	}
}

func TestCanEquipArmor_AccessorySlotMapsToAccessoryItemSlot(t *testing.T) {
	item := ArmorRow{ID: "acc1", JobID: nil, Slot: "accessory", LevelReq: 1, IsActive: true}
	if code := canEquipArmor(item, "accessory1", nil, 99, ""); code != "" {
		t.Fatalf("item.slot=accessory 應該可以裝進 accessory1/accessory2 任一格，got %q", code)
	}
	if code := canEquipArmor(item, "accessory2", nil, 99, ""); code != "" {
		t.Fatalf("item.slot=accessory 應該可以裝進 accessory1/accessory2 任一格，got %q", code)
	}
}

func TestCanEquipArmor_WrongJobRejected(t *testing.T) {
	jobID := "light_knight"
	otherJobID := "mage"
	item := ArmorRow{ID: "a1", JobID: &otherJobID, Slot: "helmet", LevelReq: 1, IsActive: true}
	if code := canEquipArmor(item, "helmet", &jobID, 99, ""); code != "wrong_job" {
		t.Fatalf("防具 job_id 跟角色目前職業不符，應回 wrong_job，got %q", code)
	}
}

func TestCanEquipArmor_NoJobRejectedForJobArmor(t *testing.T) {
	jobID := "light_knight"
	item := ArmorRow{ID: "a1", JobID: &jobID, Slot: "helmet", LevelReq: 1, IsActive: true}
	if code := canEquipArmor(item, "helmet", nil, 99, ""); code != "wrong_job" {
		t.Fatalf("角色尚未選職業（jobID=nil）裝防具，應回 wrong_job，got %q", code)
	}
}

// 飾品 job_id 恆為 nil，不受「角色尚未選職業」規則限制（CONTRACT「飾品不限」）。
func TestCanEquipArmor_AccessoryIgnoresJobCheck(t *testing.T) {
	item := ArmorRow{ID: "acc1", JobID: nil, Slot: "accessory", LevelReq: 1, IsActive: true}
	if code := canEquipArmor(item, "accessory1", nil, 99, ""); code != "" {
		t.Fatalf("飾品不受職業限制，角色尚未選職業也應可裝備，got %q", code)
	}
}

func TestCanEquipArmor_LevelTooLowRejected(t *testing.T) {
	jobID := "light_knight"
	item := ArmorRow{ID: "a1", JobID: &jobID, Slot: "helmet", LevelReq: 50, IsActive: true}
	if code := canEquipArmor(item, "helmet", &jobID, 10, ""); code != "level_too_low" {
		t.Fatalf("有效等級低於 level_req，應回 level_too_low，got %q", code)
	}
}

func TestCanEquipArmor_DuplicateAccessoryRejected(t *testing.T) {
	item := ArmorRow{ID: "acc1", JobID: nil, Slot: "accessory", LevelReq: 1, IsActive: true}
	if code := canEquipArmor(item, "accessory2", nil, 99, "acc1"); code != "duplicate_accessory" {
		t.Fatalf("另一格（accessory1）已經裝著同一件飾品，應回 duplicate_accessory，got %q", code)
	}
}

// 兩格飾品裝不同件，或同一件飾品的 id 只是恰好跟另一格「不同」item 相符時，都不該誤判重複。
func TestCanEquipArmor_DifferentAccessoriesAllowed(t *testing.T) {
	item := ArmorRow{ID: "acc2", JobID: nil, Slot: "accessory", LevelReq: 1, IsActive: true}
	if code := canEquipArmor(item, "accessory2", nil, 99, "acc1"); code != "" {
		t.Fatalf("另一格裝的是不同件飾品，不應回 duplicate_accessory，got %q", code)
	}
}

// duplicate_accessory 只在「本次要裝的格子」是飾品格時才有意義——otherAccessoryItemID 傳非空值
// 但目前操作的是防具格（helmet 等）時不該誤判（呼叫端只在 slot 為 accessory1/2 時才會帶非空值，
// 這裡額外驗證函式本身邏輯的防呆）。
func TestCanEquipArmor_DuplicateCheckIgnoredForNonAccessorySlot(t *testing.T) {
	jobID := "light_knight"
	item := ArmorRow{ID: "a1", JobID: &jobID, Slot: "helmet", LevelReq: 1, IsActive: true}
	if code := canEquipArmor(item, "helmet", &jobID, 99, "a1"); code != "" {
		t.Fatalf("非飾品格不應套用 duplicate_accessory 規則，got %q", code)
	}
}

// 優先序：is_active 檢查放最前面——即使 slot/job/等級同時不符，下架這件事最優先回報。
func TestCanEquipArmor_InactiveTakesPriorityOverOtherFailures(t *testing.T) {
	jobID := "light_knight"
	item := ArmorRow{ID: "a1", JobID: &jobID, Slot: "gloves", LevelReq: 50, IsActive: false}
	if code := canEquipArmor(item, "helmet", nil, 1, ""); code != "not_found" {
		t.Fatalf("is_active 檢查應優先於 slot/job/等級檢查，got %q", code)
	}
}
