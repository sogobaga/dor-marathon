package rewardserial

import "testing"

// TestBundlePacksFromStock 純函式：min(floor(avail[i]/count[i])) over i，即組合型序號組（migration 150
// is_bundle=true）目前／原始能湊滿幾包。涵蓋 0 庫存、剛好整除、除不盡有餘數、多子項取最小（瓶頸判定）、
// count<=0 防呆、slice 長度不一致防呆、無子項，以及「其中一子項視同 unlimited」的建模示範（見本函式文件
// 「unlimited 子項」防呆說明——不需要特別分支，餵一個遠大於其餘子項的哨兵值即可讓 min 自然略過它）。
func TestBundlePacksFromStock(t *testing.T) {
	cases := []struct {
		name         string
		avail, count []int
		want         int
	}{
		{"0 庫存 → 整體 0", []int{0}, []int{3}, 0},
		{"單一子項剛好整除（exact multiple）", []int{10}, []int{2}, 5},
		{"除不盡無條件捨去（remainder）", []int{7}, []int{3}, 2},
		{"多子項取最小（瓶頸在第二項）", []int{100, 7}, []int{10, 3}, 2}, // 100/10=10, 7/3=2 → min=2
		{"多子項其中一項庫存 0 → 整體 0", []int{50, 0}, []int{5, 1}, 0},
		{"其中一子項視同 unlimited（極大庫存，不該是瓶頸）", []int{10, 1_000_000_000}, []int{2, 1}, 5},
		{"per-pack count<=0 防呆視為 1", []int{5}, []int{0}, 5},
		{"avail 比 count 短（防呆）：只算到較短者", []int{10, 20}, []int{2}, 5},
		{"無子項 → 0", nil, nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BundlePacksFromStock(c.avail, c.count)
			if got != c.want {
				t.Fatalf("BundlePacksFromStock() = %d, want %d", got, c.want)
			}
		})
	}
}
