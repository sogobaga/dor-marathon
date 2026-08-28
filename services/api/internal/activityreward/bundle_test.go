package activityreward

import "testing"

// TestComputeBundleTotal bundle_total = Σ(faceValues[i] × counts[i])（migration 149/150 契約）。涵蓋多
// 子項加總、單一子項、slice 長度不一致（防呆）。
func TestComputeBundleTotal(t *testing.T) {
	cases := []struct {
		name               string
		faceValues, counts []int
		want               int
	}{
		{
			name:       "多子項：1000×3+500×1=3500",
			faceValues: []int{1000, 500},
			counts:     []int{3, 1},
			want:       3500,
		},
		{
			name:       "單一子項",
			faceValues: []int{100},
			counts:     []int{5},
			want:       500,
		},
		{
			name:       "counts 比 faceValues 短（防呆）：只加總到較短者",
			faceValues: []int{100, 200, 300},
			counts:     []int{1, 2},
			want:       500, // 100*1 + 200*2；第三筆缺 count，不計入
		},
		{
			name:       "皆空 → 0",
			faceValues: nil,
			counts:     nil,
			want:       0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeBundleTotal(c.faceValues, c.counts)
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

// TestBundlePacksFromStock 純函式：min(floor(avail[i]/count[i])) over i，即組合型序號組（migration 150
// is_bundle=true）目前能湊滿幾包。涵蓋「各子項餘量不同取最小」「除不盡無條件捨去」「count<=0 防呆視為
// 1」「slice 長度不一致（防呆）」「無子項回 0」。
func TestBundlePacksFromStock(t *testing.T) {
	cases := []struct {
		name         string
		avail, count []int
		want         int
	}{
		{"單一子項剛好整除", []int{10}, []int{2}, 5},
		{"多子項取最小（瓶頸在第二項）", []int{100, 7}, []int{10, 3}, 2}, // 100/10=10, 7/3=2 → min=2
		{"除不盡無條件捨去", []int{7}, []int{3}, 2},
		{"某子項庫存 0 → 整體 0", []int{50, 0}, []int{5, 1}, 0},
		{"count<=0 防呆視為 1（理論不會發生）", []int{5}, []int{0}, 5},
		{"avail 比 count 短（防呆）：只算到較短者", []int{10, 20}, []int{2}, 5},
		{"無子項 → 0", nil, nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := bundlePacksFromStock(c.avail, c.count)
			if got != c.want {
				t.Fatalf("bundlePacksFromStock() = %d, want %d", got, c.want)
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
