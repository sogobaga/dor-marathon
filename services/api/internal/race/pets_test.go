package race

import (
	"strings"
	"testing"
)

// TestValidatePets_NonPetRaceIgnoresInput race.PetKind==""（非寵物賽事）：D5 選擇「忽略」而非 400——
// 即使呼叫端多帶了 pets（甚至是完全非法的資料），一律回傳 nil, nil，不影響報名成功。
func TestValidatePets_NonPetRaceIgnoresInput(t *testing.T) {
	r := &Race{PetKind: "", PetBaseSlots: 1, PetMaxPerReg: 1}
	cases := [][]PetEntry{
		nil,
		{},
		{{Name: "Rex"}},
		{{Name: ""}},                 // 名稱不合法也不擋——非寵物賽事根本不應該驗證寵物欄位
		{{Name: "x", ChipID: "!!!"}}, // chip 格式不合法也不擋
	}
	for i, pets := range cases {
		out, err := ValidatePets(r, pets, 0)
		if err != nil {
			t.Errorf("case %d: ValidatePets error = %v, want nil (非寵物賽事應忽略)", i, err)
		}
		if out != nil {
			t.Errorf("case %d: ValidatePets pets = %+v, want nil", i, out)
		}
	}
}

// TestValidatePets_NilRace 防禦性：race 為 nil 時不得 panic，行為比照 PetKind=="" 的略過語意。
func TestValidatePets_NilRace(t *testing.T) {
	out, err := ValidatePets(nil, []PetEntry{{Name: "Rex"}}, 0)
	if err != nil || out != nil {
		t.Errorf("ValidatePets(nil, ...) = %+v, %v, want nil, nil", out, err)
	}
}

// TestValidatePets_ZeroPetsRequiresPets 寵物賽事一筆寵物都沒填 → ErrPetsRequired（D5「請填寫寵物資料」），
// 比純粹的「數量不符」更精確，讓前端能顯示對應文案。
func TestValidatePets_ZeroPetsRequiresPets(t *testing.T) {
	r := &Race{PetKind: "dog", PetBaseSlots: 1, PetMaxPerReg: 5}
	_, err := ValidatePets(r, nil, 0)
	if err != ErrPetsRequired {
		t.Errorf("ValidatePets error = %v, want ErrPetsRequired", err)
	}
	_, err = ValidatePets(r, []PetEntry{}, 0)
	if err != ErrPetsRequired {
		t.Errorf("ValidatePets(empty slice) error = %v, want ErrPetsRequired", err)
	}
}

// TestValidatePets_CountMustMatchBaseSlotsPlusAddonQty D5：寵物筆數須恰好等於
// race.PetBaseSlots + petSlotAddonQty，多一筆或少一筆都要擋。
func TestValidatePets_CountMustMatchBaseSlotsPlusAddonQty(t *testing.T) {
	r := &Race{PetKind: "cat", PetBaseSlots: 1, PetMaxPerReg: 10}

	// 沒加購（petSlotAddonQty=0）：應有 1 筆。
	if _, err := ValidatePets(r, []PetEntry{{Name: "Mimi"}}, 0); err != nil {
		t.Errorf("want 1 pet ok, got err=%v", err)
	}
	if _, err := ValidatePets(r, []PetEntry{{Name: "Mimi"}, {Name: "Momo"}}, 0); err != ErrPetCountMismatch {
		t.Errorf("2 pets with petSlotAddonQty=0: err=%v, want ErrPetCountMismatch", err)
	}

	// 加購 2 份寵物名額（petSlotAddonQty=2）：應有 1+2=3 筆。
	threePets := []PetEntry{{Name: "A"}, {Name: "B"}, {Name: "C"}}
	if _, err := ValidatePets(r, threePets, 2); err != nil {
		t.Errorf("want 3 pets ok (base 1 + addon 2), got err=%v", err)
	}
	if _, err := ValidatePets(r, threePets[:2], 2); err != ErrPetCountMismatch {
		t.Errorf("2 pets but want 3: err=%v, want ErrPetCountMismatch", err)
	}
}

// TestValidatePets_ExceedsMaxPerReg 即使加購數量算出來的應有寵物數也不得超過賽事寵物上限
// （PetMaxPerReg，後台設定），這是前端 UI 已 cap 過加購數量選擇器之外的伺服器端最後防線。
func TestValidatePets_ExceedsMaxPerReg(t *testing.T) {
	r := &Race{PetKind: "dog", PetBaseSlots: 1, PetMaxPerReg: 3}
	// base(1) + addonQty(5) = 6 > max(3)：無論 pets 帶了幾筆都該被擋下，因為「應有寵物數」本身就不合法。
	pets := make([]PetEntry, 6)
	for i := range pets {
		pets[i] = PetEntry{Name: "P"}
	}
	if _, err := ValidatePets(r, pets, 5); err != ErrPetCountMismatch {
		t.Errorf("want>max: err=%v, want ErrPetCountMismatch", err)
	}
}

// TestValidatePets_WantFloorsToOne race.PetBaseSlots 若因舊資料或未經 normalizeRequest 的呼叫路徑
// 意外是 0（DB CHECK 正常情況下不會發生，見 repository.defaultPetBaseSlots），ValidatePets 仍要保底
// 要求至少 1 筆，不能讓 want<=0 導致 0 筆寵物也算合法。
func TestValidatePets_WantFloorsToOne(t *testing.T) {
	r := &Race{PetKind: "dog", PetBaseSlots: 0, PetMaxPerReg: 5}
	if _, err := ValidatePets(r, nil, 0); err != ErrPetsRequired {
		t.Errorf("err=%v, want ErrPetsRequired (want 至少為 1)", err)
	}
	if _, err := ValidatePets(r, []PetEntry{{Name: "Rex"}}, 0); err != nil {
		t.Errorf("1 pet with floored want=1 should be ok, got err=%v", err)
	}
}

// TestValidatePets_NameValidation 名稱去頭尾空白後需 1..40 個 rune，否則 ErrPetNameRequired；
// 回傳值也要是已 TrimSpace 過的正規化名稱。
func TestValidatePets_NameValidation(t *testing.T) {
	r := &Race{PetKind: "dog", PetBaseSlots: 1, PetMaxPerReg: 5}

	if _, err := ValidatePets(r, []PetEntry{{Name: "   "}}, 0); err != ErrPetNameRequired {
		t.Errorf("blank name: err=%v, want ErrPetNameRequired", err)
	}
	longName := strings.Repeat("狗", 41) // 41 個 rune，超過上限
	if _, err := ValidatePets(r, []PetEntry{{Name: longName}}, 0); err != ErrPetNameRequired {
		t.Errorf("41-rune name: err=%v, want ErrPetNameRequired", err)
	}
	exactly40 := strings.Repeat("狗", 40)
	if _, err := ValidatePets(r, []PetEntry{{Name: exactly40}}, 0); err != nil {
		t.Errorf("exactly-40-rune name should be ok, got err=%v", err)
	}
	out, err := ValidatePets(r, []PetEntry{{Name: "  Rex  "}}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out[0].Name != "Rex" {
		t.Errorf("Name = %q, want trimmed %q", out[0].Name, "Rex")
	}
}

// TestValidatePets_ChipIDValidation ChipID 選填，符合 ^[A-Za-z0-9-]{0,32}$ 才合法；去頭尾空白後檢查。
func TestValidatePets_ChipIDValidation(t *testing.T) {
	r := &Race{PetKind: "dog", PetBaseSlots: 1, PetMaxPerReg: 5}

	cases := []struct {
		name    string
		chipID  string
		wantErr bool
	}{
		{"empty is ok (optional)", "", false},
		{"alnum and dash ok", "ABC-123-xyz", false},
		{"exactly 32 chars ok", strings.Repeat("A", 32), false},
		{"33 chars too long", strings.Repeat("A", 33), true},
		{"contains space", "ABC 123", true},
		{"contains chinese", "晶片123", true},
		{"contains underscore", "ABC_123", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ValidatePets(r, []PetEntry{{Name: "Rex", ChipID: c.chipID}}, 0)
			if (err != nil) != c.wantErr {
				t.Errorf("ChipID=%q: err=%v, wantErr=%v", c.chipID, err, c.wantErr)
			}
		})
	}
}

// TestValidatePets_TrimsChipIDWhitespace ChipID 去頭尾空白後才驗證/儲存（比照 Name）。
func TestValidatePets_TrimsChipIDWhitespace(t *testing.T) {
	r := &Race{PetKind: "dog", PetBaseSlots: 1, PetMaxPerReg: 5}
	out, err := ValidatePets(r, []PetEntry{{Name: "Rex", ChipID: "  ABC123  "}}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out[0].ChipID != "ABC123" {
		t.Errorf("ChipID = %q, want trimmed %q", out[0].ChipID, "ABC123")
	}
}

// --- normalizeRequest：D3 同一賽事至多一個 pet_slot 加購 ---

// TestNormalizeRequest_AtMostOnePetSlotAddon 兩個以上 kind=pet_slot 的加購應被擋下，錯誤訊息須為
// owner 指定文案「每場賽事只能有一個寵物名額加購」（handler 層 err.Error() 原樣回給後台前端顯示，見 D3）。
func TestNormalizeRequest_AtMostOnePetSlotAddon(t *testing.T) {
	req := &CreateRaceRequest{
		Race: Race{PetKind: "dog", PetMaxPerReg: 5}, // pet_slot 加購只允許在寵物賽事（審查修正後的規則）
		Addons: []RaceAddon{
			{Name: "寵物名額 A", Kind: "pet_slot"},
			{Name: "寵物名額 B", Kind: "pet_slot"},
		},
	}
	err := normalizeRequest(req)
	if err == nil {
		t.Fatal("normalizeRequest with two pet_slot addons should error")
	}
	if err.Error() != "每場賽事只能有一個寵物名額加購" {
		t.Errorf("error message = %q, want %q", err.Error(), "每場賽事只能有一個寵物名額加購")
	}
}

// TestNormalizeRequest_OnePetSlotAddonOK 恰好一個 pet_slot 加購（其餘為一般 item）應通過，且各自
// Kind 維持/正規化正確（未帶 kind 的舊資料一律預設 item，向下相容）。
func TestNormalizeRequest_OnePetSlotAddonOK(t *testing.T) {
	req := &CreateRaceRequest{
		Race: Race{PetKind: "dog", PetMaxPerReg: 5}, // pet_slot 加購只允許在寵物賽事（審查修正後的規則）
		Addons: []RaceAddon{
			{Name: "紀念衣", Kind: ""}, // 未帶 kind，應正規化為 item
			{Name: "寵物名額", Kind: "pet_slot"},
		},
	}
	if err := normalizeRequest(req); err != nil {
		t.Fatalf("normalizeRequest with one pet_slot addon should not error, got: %v", err)
	}
	if req.Addons[0].Kind != "item" {
		t.Errorf("Addons[0].Kind = %q, want defaulted %q", req.Addons[0].Kind, "item")
	}
	if req.Addons[1].Kind != "pet_slot" {
		t.Errorf("Addons[1].Kind = %q, want %q", req.Addons[1].Kind, "pet_slot")
	}
}

// TestNormalizeRequest_InvalidAddonKind 非白名單的 kind 值應被擋下。
func TestNormalizeRequest_InvalidAddonKind(t *testing.T) {
	req := &CreateRaceRequest{
		Addons: []RaceAddon{{Name: "x", Kind: "bogus"}},
	}
	if err := normalizeRequest(req); err == nil {
		t.Error("normalizeRequest with invalid addon kind should error")
	}
}

// TestNormalizeRequest_PetKindValidation pet_kind 只能是 ''｜dog｜cat；pet_max_per_reg/pet_base_slots
// 缺省或超界一律夾回合法範圍（D1；DB CHECK BETWEEN 1 AND 20）。
func TestNormalizeRequest_PetKindValidation(t *testing.T) {
	if err := normalizeRequest(&CreateRaceRequest{Race: Race{PetKind: "bird"}}); err == nil {
		t.Error("invalid pet_kind should error")
	}

	req := &CreateRaceRequest{Race: Race{PetKind: "dog", PetMaxPerReg: 0, PetBaseSlots: 0}}
	if err := normalizeRequest(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.PetMaxPerReg != 1 {
		t.Errorf("PetMaxPerReg = %d, want defaulted to 1", req.PetMaxPerReg)
	}
	if req.PetBaseSlots != 1 {
		t.Errorf("PetBaseSlots = %d, want defaulted to 1", req.PetBaseSlots)
	}

	req2 := &CreateRaceRequest{Race: Race{PetKind: "cat", PetMaxPerReg: 999}}
	if err := normalizeRequest(req2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req2.PetMaxPerReg != 20 {
		t.Errorf("PetMaxPerReg = %d, want clamped to 20", req2.PetMaxPerReg)
	}
}

// TestNormalizeRequest_GroupForOwnerForPetDefaultTrue race_groups.for_owner/for_pet（D2）：payload
// 未帶（nil，例如舊版後台表單尚未加上這兩個勾選框）一律預設 TRUE，比照 DB 欄位預設值——不可誤判成
// 「明確帶 false」而把既有分組整批關閉飼主/寵物成績計算資格。
func TestNormalizeRequest_GroupForOwnerForPetDefaultTrue(t *testing.T) {
	req := &CreateRaceRequest{
		Groups: []RaceGroup{{Name: "組1"}}, // 未帶 ForOwner/ForPet
	}
	if err := normalizeRequest(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g := req.Groups[0]
	if g.ForOwner == nil || !*g.ForOwner {
		t.Errorf("ForOwner = %v, want *true", g.ForOwner)
	}
	if g.ForPet == nil || !*g.ForPet {
		t.Errorf("ForPet = %v, want *true", g.ForPet)
	}
}

// TestNormalizeRequest_GroupForOwnerForPetExplicitFalsePreserved 明確帶 false 時必須保留，不能被
// 誤判成「沒帶」而覆寫回 true（*bool 用指標的唯一理由）。
func TestNormalizeRequest_GroupForOwnerForPetExplicitFalsePreserved(t *testing.T) {
	f := false
	req := &CreateRaceRequest{
		Groups: []RaceGroup{{Name: "組1", ForOwner: &f, ForPet: &f}},
	}
	if err := normalizeRequest(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g := req.Groups[0]
	if g.ForOwner == nil || *g.ForOwner {
		t.Errorf("ForOwner = %v, want *false preserved", g.ForOwner)
	}
	if g.ForPet == nil || *g.ForPet {
		t.Errorf("ForPet = %v, want *false preserved", g.ForPet)
	}
}
