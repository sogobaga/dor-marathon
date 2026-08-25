package virtualrunner

import "testing"

// TestValidDiligence 契約：diligence 1-5。
func TestValidDiligence(t *testing.T) {
	cases := []struct {
		v    int
		want bool
	}{
		{0, false}, {1, true}, {3, true}, {5, true}, {6, false}, {-1, false},
	}
	for _, c := range cases {
		if got := ValidDiligence(c.v); got != c.want {
			t.Errorf("ValidDiligence(%d) = %v, want %v", c.v, got, c.want)
		}
	}
}

// TestValidWindowHour 契約白名單：{4,5,6,19,20,21,22}。
func TestValidWindowHour(t *testing.T) {
	for _, h := range []int{4, 5, 6, 19, 20, 21, 22} {
		if !ValidWindowHour(h) {
			t.Errorf("ValidWindowHour(%d) 應為合法", h)
		}
	}
	for _, h := range []int{0, 3, 7, 12, 18, 23, -1} {
		if ValidWindowHour(h) {
			t.Errorf("ValidWindowHour(%d) 應為不合法", h)
		}
	}
}

// TestValidCity 契約白名單：七都。
func TestValidCity(t *testing.T) {
	for _, c := range []string{"taipei", "new_taipei", "taoyuan", "hsinchu", "taichung", "tainan", "kaohsiung"} {
		if !ValidCity(c) {
			t.Errorf("ValidCity(%q) 應為合法", c)
		}
	}
	for _, c := range []string{"", "Taipei", "keelung", "台北"} {
		if ValidCity(c) {
			t.Errorf("ValidCity(%q) 應為不合法", c)
		}
	}
}

// TestValidGender 僅 male/female。
func TestValidGender(t *testing.T) {
	for _, g := range []string{"male", "female"} {
		if !ValidGender(g) {
			t.Errorf("ValidGender(%q) 應為合法", g)
		}
	}
	for _, g := range []string{"", "other", "unknown", "Male"} {
		if ValidGender(g) {
			t.Errorf("ValidGender(%q) 應為不合法", g)
		}
	}
}

// TestValidLevel 8 級固定代碼。
func TestValidLevel(t *testing.T) {
	for _, lv := range []string{"beginner", "citizen", "advanced", "half_challenger", "half_finisher", "full_challenger", "full_finisher", "elite"} {
		if !ValidLevel(lv) {
			t.Errorf("ValidLevel(%q) 應為合法", lv)
		}
	}
	for _, lv := range []string{"", "master", "pro", "Beginner"} {
		if ValidLevel(lv) {
			t.Errorf("ValidLevel(%q) 應為不合法", lv)
		}
	}
}

// TestValidPaceRange fast 必須嚴格小於 slow，且兩者皆須為正值。
func TestValidPaceRange(t *testing.T) {
	cases := []struct {
		fast, slow int
		want       bool
	}{
		{480, 510, true},
		{300, 300, false}, // 相等不合法
		{510, 480, false}, // 反轉不合法
		{0, 100, false},   // fast=0 不合法
		{100, 0, false},   // slow=0 不合法
		{-10, 100, false},
	}
	for _, c := range cases {
		if got := ValidPaceRange(c.fast, c.slow); got != c.want {
			t.Errorf("ValidPaceRange(%d,%d) = %v, want %v", c.fast, c.slow, got, c.want)
		}
	}
}

// TestUpdateRunnerInput_AbilityGiven 只要任一能力值欄位非 nil 就算「有明給」。
func TestUpdateRunnerInput_AbilityGiven(t *testing.T) {
	f := 5.0
	i := 300
	cases := []struct {
		name string
		in   UpdateRunnerInput
		want bool
	}{
		{"全空", UpdateRunnerInput{}, false},
		{"只給 level", UpdateRunnerInput{Level: strPtr("elite")}, false},
		{"給 avg_km", UpdateRunnerInput{AvgKm: &f}, true},
		{"給 pace_fast_s", UpdateRunnerInput{PaceFastS: &i}, true},
	}
	for _, c := range cases {
		if got := c.in.AbilityGiven(); got != c.want {
			t.Errorf("%s: AbilityGiven() = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestGroupSlot_HasCapacity slot_limit=nil 視為不限；有限額時比較 slots_taken。
func TestGroupSlot_HasCapacity(t *testing.T) {
	unlimited := GroupSlot{SlotLimit: nil, SlotsTaken: 9999}
	if !unlimited.HasCapacity() {
		t.Error("slot_limit=nil 應永遠有名額")
	}
	limit := intPtr(10)
	full := GroupSlot{SlotLimit: limit, SlotsTaken: 10}
	if full.HasCapacity() {
		t.Error("slots_taken==slot_limit 應視為已滿")
	}
	open := GroupSlot{SlotLimit: limit, SlotsTaken: 9}
	if !open.HasCapacity() {
		t.Error("slots_taken<slot_limit 應視為有名額")
	}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
