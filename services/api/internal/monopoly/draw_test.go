package monopoly

import "testing"

// pickWeighted 是抽卡引擎的核心（reward_type 加權挑選、稀有度加權挑卡皆靠它），且全程 crypto/rand。
// 這裡只驗證邊界情況與統計分佈是否合理；DB 相依的 dup 轉換/兌換碼領用邏輯需要真正的 pgx.Tx，
// 本專案既有測試（internal/race 等）也只覆蓋純函式，不另外搭建 DB mock。

func TestPickWeighted_EmptyReturnsNil(t *testing.T) {
	got, err := pickWeighted([]int{}, func(i int) int { return i })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for empty input, got %v", *got)
	}
}

func TestPickWeighted_AllZeroWeightReturnsNil(t *testing.T) {
	items := []int{1, 2, 3}
	got, err := pickWeighted(items, func(i int) int { return 0 })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil when every weight is 0, got %v", *got)
	}
}

// 只有一個候選權重 > 0、其餘皆 0：必須每次都選中那一個（涵蓋「池內大部分項目權重被後台調成 0」的常見情境）。
func TestPickWeighted_SingleNonZeroAmongZerosIsDeterministic(t *testing.T) {
	type entry struct {
		label  string
		weight int
	}
	items := []entry{{"a", 0}, {"b", 5}, {"c", 0}, {"d", 0}}
	for i := 0; i < 200; i++ {
		got, err := pickWeighted(items, func(e entry) int { return e.weight })
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || got.label != "b" {
			t.Fatalf("expected always picking %q, got %+v", "b", got)
		}
	}
}

// 統計分佈測試：權重 90/10 跑夠多次，命中率應大致落在合理區間內（容忍統計誤差，避免 flaky）。
func TestPickWeighted_DistributionRoughlyMatchesWeights(t *testing.T) {
	type entry struct {
		label  string
		weight int
	}
	items := []entry{{"common", 90}, {"rare", 10}}
	const trials = 5000
	counts := map[string]int{}
	for i := 0; i < trials; i++ {
		got, err := pickWeighted(items, func(e entry) int { return e.weight })
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Fatalf("unexpected nil pick")
		}
		counts[got.label]++
	}
	rareRatio := float64(counts["rare"]) / float64(trials)
	// 期望值 10%，容忍 6%~14% 的統計誤差帶（trials=5000 下極不易誤判失敗）。
	if rareRatio < 0.06 || rareRatio > 0.14 {
		t.Fatalf("rare ratio out of expected range: got %.4f (counts=%+v)", rareRatio, counts)
	}
}

func TestRarityWeight_KnownAndUnknown(t *testing.T) {
	if w := rarityWeight("common"); w != 10 {
		t.Fatalf("common weight: got %d, want 10", w)
	}
	if w := rarityWeight("rare"); w != 3 {
		t.Fatalf("rare weight: got %d, want 3", w)
	}
	// 未知/髒資料稀有度：保底權重 1，不可讓整批候選被排除（見 rarityWeight 註解）。
	if w := rarityWeight("typo_rarity"); w != 1 {
		t.Fatalf("unknown rarity weight: got %d, want 1", w)
	}
	if w := rarityWeight(""); w != 1 {
		t.Fatalf("empty rarity weight: got %d, want 1", w)
	}
}
