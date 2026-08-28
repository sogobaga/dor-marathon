package activityreward

import "testing"

// TestComputeBundleTotal bundle_total = Σ(faceValueByGroup[entry.GroupID] × entry.Count)（migration 149
// 契約）。涵蓋多 entry 加總、單一 entry、查不到面額（缺該 group_id）視為 0 的防呆。
func TestComputeBundleTotal(t *testing.T) {
	cases := []struct {
		name    string
		entries []BundleEntry
		faceVal map[string]int
		want    int
	}{
		{
			name:    "多 entry：1000×3+500×1=3500",
			entries: []BundleEntry{{GroupID: "g1000", Count: 3}, {GroupID: "g500", Count: 1}},
			faceVal: map[string]int{"g1000": 1000, "g500": 500},
			want:    3500,
		},
		{
			name:    "單一 entry",
			entries: []BundleEntry{{GroupID: "g100", Count: 5}},
			faceVal: map[string]int{"g100": 100},
			want:    500,
		},
		{
			name:    "查不到面額（缺該 group_id）視為 0，不 panic",
			entries: []BundleEntry{{GroupID: "unknown", Count: 3}},
			faceVal: map[string]int{"other": 100},
			want:    0,
		},
		{
			name:    "空 entries → 0",
			entries: nil,
			faceVal: map[string]int{"g1": 100},
			want:    0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeBundleTotal(c.entries, c.faceVal)
			if got != c.want {
				t.Fatalf("computeBundleTotal() = %d, want %d", got, c.want)
			}
		})
	}
}

// TestFirstInsufficientBundleEntry all-or-nothing 核心判斷：need[i] 是需求張數、locked[i] 是階段 1
// 實際鎖到的張數；找出第一個不足的索引，全部足夠回 -1。涵蓋「全部剛好足夠」「第一個就短缺」「最後一個
// 才短缺」「locked 比 need 短（防呆）」的邊界。
func TestFirstInsufficientBundleEntry(t *testing.T) {
	cases := []struct {
		name         string
		need, locked []int
		want         int
	}{
		{"全部剛好足夠 → -1", []int{3, 1}, []int{3, 1}, -1},
		{"全部超額足夠（理論上不會發生，但不該誤判不足）→ -1", []int{3, 1}, []int{5, 2}, -1},
		{"第一個就短缺 → 0", []int{3, 1}, []int{2, 1}, 0},
		{"最後一個才短缺 → 最後索引", []int{3, 1}, []int{3, 0}, 1},
		{"locked 比 need 短（防呆，視同該筆為 0）→ 該索引", []int{3, 1}, []int{3}, 1},
		{"皆為空 → -1", nil, nil, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := firstInsufficientBundleEntry(c.need, c.locked)
			if got != c.want {
				t.Fatalf("firstInsufficientBundleEntry() = %d, want %d", got, c.want)
			}
		})
	}
}

// TestFormatBundleLabel "{商家名} {總額}"；商家名稱為空時退回固定前綴「LINE POINTS」，避免顯示空白標籤。
func TestFormatBundleLabel(t *testing.T) {
	cases := []struct {
		name, merchant string
		total          int
		want           string
	}{
		{"有商家名稱", "LINE POINTS", 3500, "LINE POINTS 3500"},
		{"商家名稱為空 → 退回固定前綴", "", 1000, "LINE POINTS 1000"},
		{"其他商家名稱照用", "7-ELEVEN", 300, "7-ELEVEN 300"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FormatBundleLabel(c.merchant, c.total)
			if got != c.want {
				t.Fatalf("FormatBundleLabel() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestSamePtrString 供 grantSerialBundle 判斷 bundle 內各 entry 的 merchant_id 是否一致：皆 nil 視為
// 相同（都未指定商家）；一 nil 一非 nil 視為不同；皆非 nil 則比較實際值。
func TestSamePtrString(t *testing.T) {
	s := func(v string) *string { return &v }
	cases := []struct {
		name string
		a, b *string
		want bool
	}{
		{"皆 nil → 相同", nil, nil, true},
		{"一 nil 一非 nil → 不同", nil, s("m1"), false},
		{"非 nil 且值相同 → 相同", s("m1"), s("m1"), true},
		{"非 nil 但值不同 → 不同", s("m1"), s("m2"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := samePtrString(c.a, c.b); got != c.want {
				t.Fatalf("samePtrString(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

// TestBundleEntryValidate serial 類 Bundle 分支的結構驗證：與 Denominations 互斥、entry 數 1-20、
// group_id/count 合法、同一 bundle 內不可重複 group_id。
func TestBundleEntryValidate(t *testing.T) {
	base := func() RewardItem {
		return RewardItem{Type: "serial", ProbBP: 5000, Bundle: []BundleEntry{
			{GroupID: "g1", Count: 3}, {GroupID: "g2", Count: 1},
		}}
	}

	t.Run("合法組合包", func(t *testing.T) {
		it := base()
		if err := it.Validate(); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
	t.Run("與 denominations 同時非空 → 錯誤", func(t *testing.T) {
		it := base()
		it.Denominations = []RewardDenom{{GroupID: "g3", Weight: 1}}
		if err := it.Validate(); err == nil {
			t.Fatalf("expected error for bundle+denominations mutual exclusivity")
		}
	})
	t.Run("entry group_id 空白 → 錯誤", func(t *testing.T) {
		it := base()
		it.Bundle[0].GroupID = "   "
		if err := it.Validate(); err == nil {
			t.Fatalf("expected error for blank group_id")
		}
	})
	t.Run("entry count<1 → 錯誤", func(t *testing.T) {
		it := base()
		it.Bundle[1].Count = 0
		if err := it.Validate(); err == nil {
			t.Fatalf("expected error for count<1")
		}
	})
	t.Run("超過 20 個 entry → 錯誤", func(t *testing.T) {
		it := base()
		entries := make([]BundleEntry, 21)
		for i := range entries {
			entries[i] = BundleEntry{GroupID: "g", Count: 1}
		}
		it.Bundle = entries
		if err := it.Validate(); err == nil {
			t.Fatalf("expected error for >20 entries")
		}
	})
	t.Run("同一 bundle 內重複 group_id → 錯誤", func(t *testing.T) {
		it := base()
		it.Bundle = []BundleEntry{{GroupID: "g1", Count: 1}, {GroupID: "g1", Count: 2}}
		if err := it.Validate(); err == nil {
			t.Fatalf("expected error for duplicate group_id within bundle")
		}
	})
}
