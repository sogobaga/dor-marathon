package ops

import (
	"math"
	"testing"

	"github.com/dor/api/internal/activityreward"
)

func TestSerialShortageThreshold(t *testing.T) {
	cases := []struct {
		name   string
		target float64
		want   float64
	}{
		{"整除天數 30 天*0.8=24 天整", 30, 24},
		{"非整除天數 23 天*0.8=18.4 天", 23, 18.4},
		{"公里數門檻 10km*0.8=8km", 10, 8},
		{"零值", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := serialShortageThreshold(c.target)
			if math.Abs(got-c.want) > 1e-9 {
				t.Errorf("serialShortageThreshold(%v) = %v, want %v", c.target, got, c.want)
			}
		})
	}
}

func TestMeetsShortageThreshold(t *testing.T) {
	cases := []struct {
		name            string
		current, target float64
		want            bool
	}{
		{"剛好 80% 邊界（整除，24/30）達標", 24, 30, true},
		{"80% 邊界前一天（23/30）未達標", 23, 30, false},
		{"非整除門檻：19/23=82.6% 達標", 19, 23, true},
		{"非整除門檻：18/23=78.3% 未達標", 18, 23, false},
		{"已超過目標值仍算達標", 100, 10, true},
		{"target<=0 一律視為未達標（防除以零）", 5, 0, false},
		{"current=0 未達標", 0, 30, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := meetsShortageThreshold(c.current, c.target); got != c.want {
				t.Errorf("meetsShortageThreshold(%v,%v) = %v, want %v", c.current, c.target, got, c.want)
			}
		})
	}
}

func TestSerialGroupStockRemainingCapacity(t *testing.T) {
	cases := []struct {
		name       string
		available  int
		grantCount int
		want       int
	}{
		{"整除：5/1=5", 5, 1, 5},
		{"整除：6/2=3", 6, 2, 3},
		{"非整除無條件捨去：5/2=2", 5, 2, 2},
		{"grant_count=0 視為 1（防禦資料異常）", 5, 0, 5},
		{"grant_count 負值視為 1", 5, -1, 5},
		{"庫存為 0", 0, 3, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := serialGroupStock{Available: c.available, GrantCount: c.grantCount}
			if got := s.remainingCapacity(); got != c.want {
				t.Errorf("remainingCapacity(avail=%d,grant=%d) = %d, want %d", c.available, c.grantCount, got, c.want)
			}
		})
	}
}

func TestTotalCapacity(t *testing.T) {
	groups := []serialGroupStock{
		{Available: 5, GrantCount: 2}, // 2
		{Available: 3, GrantCount: 1}, // 3
		{Available: 0, GrantCount: 1}, // 0
	}
	if got := totalCapacity(groups); got != 5 {
		t.Errorf("totalCapacity() = %d, want 5", got)
	}
	if got := totalCapacity(nil); got != 0 {
		t.Errorf("totalCapacity(nil) = %d, want 0", got)
	}
}

func TestSerialItemsOf(t *testing.T) {
	cfg := &activityreward.RewardConfig{Items: []activityreward.RewardItem{
		{Type: "exp", ProbBP: 10000}, // 非 serial：排除
		{Type: "serial", ProbBP: 0, Denominations: []activityreward.RewardDenom{{GroupID: "g1", Weight: 1}}},    // ProbBP=0 永不中：排除
		{Type: "serial", ProbBP: 5000, Denominations: []activityreward.RewardDenom{{GroupID: "g2", Weight: 0}}}, // 權重<=0：無有效面額，排除
		{Type: "serial", ProbBP: 5000, Denominations: []activityreward.RewardDenom{{GroupID: "g3", Weight: 1}}}, // 保留
		{Type: "serial", ProbBP: 5000, SerialGroupID: "g4"},                                                     // 舊格式回退：保留
	}}
	got := serialItemsOf(cfg)
	if len(got) != 2 {
		t.Fatalf("expected 2 eligible serial items, got %d", len(got))
	}
	denomsG3 := got[0].ValidDenominations()
	if len(denomsG3) != 1 || denomsG3[0].GroupID != "g3" {
		t.Errorf("expected first eligible item to resolve to group g3, got %+v", denomsG3)
	}
	denomsG4 := got[1].ValidDenominations()
	if len(denomsG4) != 1 || denomsG4[0].GroupID != "g4" {
		t.Errorf("expected second eligible item to fall back to legacy SerialGroupID g4, got %+v", denomsG4)
	}

	if got := serialItemsOf(nil); got != nil {
		t.Errorf("serialItemsOf(nil) = %v, want nil", got)
	}
}
