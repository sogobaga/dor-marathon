package virtualrunner

import (
	"math/rand"
	"testing"
)

// TestJitterFloat_WithinBounds 抖動後的值必須落在 [v*(1-pct), v*(1+pct)] 區間內；用多個種子跑
// 多輪覆蓋隨機分布的上下界。
func TestJitterFloat_WithinBounds(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	v, pct := 90.0, 0.05
	lo, hi := v*0.95, v*1.05
	for i := 0; i < 1000; i++ {
		got := jitterFloat(rng, v, pct)
		if got < lo || got > hi {
			t.Fatalf("jitterFloat(%v,%v) = %v，超出 [%v,%v]", v, pct, got, lo, hi)
		}
	}
}

// TestJitterFloat_ZeroStaysZero v=0 時抖動應恆回 0（避免 0*(1±pct) 的除零/NaN 邊界疑慮）。
func TestJitterFloat_ZeroStaysZero(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	if got := jitterFloat(rng, 0, 0.05); got != 0 {
		t.Fatalf("jitterFloat(0,...) = %v，want 0", got)
	}
}

// TestJitterAbility_PaceOrderingAlwaysHolds 契約鐵律「fast<slow」：即使對差距很小的等級
// （beginner 480-510，差距僅 30 秒、±5% 抖動理論上可達 ±25.5 秒，接近反轉邊界）跑大量隨機種子，
// 抖動後也必須保證 fast<slow 恆成立。
func TestJitterAbility_PaceOrderingAlwaysHolds(t *testing.T) {
	preset := LevelPreset{Level: "beginner", AvgKm: 3, MonthlyKm: 60, PaceFastS: 480, PaceSlowS: 510}
	for seed := int64(0); seed < 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		a := jitterAbility(preset, rng)
		if a.PaceFastS >= a.PaceSlowS {
			t.Fatalf("seed=%d: PaceFastS(%d) >= PaceSlowS(%d)，違反 fast<slow", seed, a.PaceFastS, a.PaceSlowS)
		}
		if a.PaceFastS < 1 {
			t.Fatalf("seed=%d: PaceFastS(%d) < 1，未夾限保底", seed, a.PaceFastS)
		}
	}
}

// TestJitterAbility_KmWithinBounds avg_km/monthly_km 抖動後應落在 ±5% 區間內。
func TestJitterAbility_KmWithinBounds(t *testing.T) {
	preset := LevelPreset{Level: "elite", AvgKm: 19, MonthlyKm: 340, PaceFastS: 240, PaceSlowS: 280}
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 500; i++ {
		a := jitterAbility(preset, rng)
		if a.AvgKm < 19*0.95 || a.AvgKm > 19*1.05 {
			t.Fatalf("AvgKm=%v 超出 ±5%% 區間", a.AvgKm)
		}
		if a.MonthlyKm < 340*0.95 || a.MonthlyKm > 340*1.05 {
			t.Fatalf("MonthlyKm=%v 超出 ±5%% 區間", a.MonthlyKm)
		}
	}
}

// TestPickRandomGroup_OnlyPicksEligible 只會挑到「尚有名額」的組；滿組永遠不會被選中。
func TestPickRandomGroup_OnlyPicksEligible(t *testing.T) {
	full := 5
	open := 5
	groups := []GroupSlot{
		{ID: "full-group", SlotLimit: &full, SlotsTaken: 5},    // 已滿
		{ID: "open-group", SlotLimit: &open, SlotsTaken: 3},    // 有名額
		{ID: "unlimited-group", SlotLimit: nil, SlotsTaken: 0}, // 不限
	}
	rng := rand.New(rand.NewSource(3))
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		id, ok := pickRandomGroup(groups, rng)
		if !ok {
			t.Fatal("有合格候選時 ok 應為 true")
		}
		if id == "full-group" {
			t.Fatal("不應挑到已滿的組")
		}
		seen[id] = true
	}
	if !seen["open-group"] || !seen["unlimited-group"] {
		t.Fatalf("多輪抽樣應覆蓋所有合格候選，實際只見過 %v", seen)
	}
}

// TestPickRandomGroup_AllFullReturnsFalse 全部候選皆滿（或分組為空）時回 ok=false。
func TestPickRandomGroup_AllFullReturnsFalse(t *testing.T) {
	limit := 2
	groups := []GroupSlot{
		{ID: "a", SlotLimit: &limit, SlotsTaken: 2},
		{ID: "b", SlotLimit: &limit, SlotsTaken: 2},
	}
	rng := rand.New(rand.NewSource(4))
	if _, ok := pickRandomGroup(groups, rng); ok {
		t.Fatal("全滿時應回 ok=false")
	}
	if _, ok := pickRandomGroup(nil, rng); ok {
		t.Fatal("空分組列表應回 ok=false")
	}
}
