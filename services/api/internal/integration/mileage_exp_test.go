package integration

import "testing"

func TestComputeRewardKm(t *testing.T) {
	cases := []struct {
		name                                       string
		distanceKm                                 float64
		durationS, perKm, dpPerKm, capKm, minPaceS int
		want                                       int
	}{
		{"一般值_無條件捨去", 7.9, 3000, 10, 5, 21, 120, 7},
		{"floor_剛好整數", 8.0, 3000, 10, 5, 21, 120, 8},
		{"單趟上限_超過cap截斷", 25.5, 20000, 10, 5, 21, 120, 21},
		{"配速防造假_低於最小配速時間截斷", 10.0, 600, 10, 5, 21, 120, 5}, // 600s/120s每公里 = 5km 上限
		{"距離小於1_回0", 0.9, 600, 10, 5, 21, 120, 0},
		{"duration為0_回0", 5.0, 0, 10, 5, 21, 120, 0},
		{"perKm與dpPerKm皆為0_回0", 5.0, 3000, 0, 0, 21, 120, 0},
		{"只有dpPerKm有值仍發放", 5.0, 3000, 0, 5, 21, 120, 5},
		{"cap為0代表不設上限", 30.0, 30000, 10, 5, 0, 120, 30},
		{"minPaceS為0代表不設配速防造假", 30.0, 60, 10, 5, 0, 0, 30},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeRewardKm(c.distanceKm, c.durationS, c.perKm, c.dpPerKm, c.capKm, c.minPaceS)
			if got != c.want {
				t.Errorf("computeRewardKm(%v,%v,%v,%v,%v,%v) = %v, want %v",
					c.distanceKm, c.durationS, c.perKm, c.dpPerKm, c.capKm, c.minPaceS, got, c.want)
			}
		})
	}
}

func TestClampDeltaInt(t *testing.T) {
	cases := []struct {
		name string
		a, b int
		want int
	}{
		{"a大於b_回差值", 8, 7, 1},
		{"a小於b_只補不扣回0", 7, 8, 0},
		{"a等於b_回0", 7, 7, 0},
		{"皆為0", 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := clampDeltaInt(c.a, c.b); got != c.want {
				t.Errorf("clampDeltaInt(%v,%v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestClampDeltaFloat(t *testing.T) {
	cases := []struct {
		name string
		a, b float64
		want float64
	}{
		{"a大於b_回差值", 8.2, 7.1, 1.1},
		{"a小於b_只補不扣回0", 7.1, 8.2, 0},
		{"a等於b_回0", 7.5, 7.5, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := clampDeltaFloat(c.a, c.b)
			// 浮點誤差容許
			diff := got - c.want
			if diff < 0 {
				diff = -diff
			}
			if diff > 1e-9 {
				t.Errorf("clampDeltaFloat(%v,%v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

// TestDeltaCompensationScenario 對照背景描述的真實案例：GPS 7.1K 先發、Strava 8.2K 後到（時間重疊）
// → 應只補差額 1 點（floor(8.2)-floor(7.1) = 8-7 = 1），且方向反過來時「只補不扣」回 0。
func TestDeltaCompensationScenario(t *testing.T) {
	const perKm, dpPerKm, capKm, minPaceS = 10, 5, 21, 120

	gpsReward := computeRewardKm(7.1, 3000, perKm, dpPerKm, capKm, minPaceS)    // floor(7.1)=7
	stravaReward := computeRewardKm(8.2, 3200, perKm, dpPerKm, capKm, minPaceS) // floor(8.2)=8

	if gpsReward != 7 {
		t.Fatalf("gpsReward = %v, want 7", gpsReward)
	}
	if stravaReward != 8 {
		t.Fatalf("stravaReward = %v, want 8", stravaReward)
	}

	// 情境一：GPS(7.1) 先發 → Strava(8.2) 後到，重疊已發放筆是 GPS(7.1) → 應補 1 點差額
	if delta := clampDeltaInt(stravaReward, gpsReward); delta != 1 {
		t.Errorf("7.1 vs 8.2 情境（8.2 為本筆）delta = %v, want 1", delta)
	}
	if deltaKm := clampDeltaFloat(8.2, 7.1); deltaKm < 1.0999 || deltaKm > 1.1001 {
		t.Errorf("8.2 vs 7.1 deltaKm = %v, want ~1.1", deltaKm)
	}

	// 情境二：Strava(8.2) 先發 → GPS(7.1) 後到，重疊已發放筆是 Strava(8.2) → 只補不扣，回 0
	if delta := clampDeltaInt(gpsReward, stravaReward); delta != 0 {
		t.Errorf("8.2 vs 7.1 情境（7.1 為本筆）delta = %v, want 0", delta)
	}
	if deltaKm := clampDeltaFloat(7.1, 8.2); deltaKm != 0 {
		t.Errorf("7.1 vs 8.2 deltaKm = %v, want 0", deltaKm)
	}

	// 情境三：兩筆距離相等 → 差額為 0
	if delta := clampDeltaInt(gpsReward, gpsReward); delta != 0 {
		t.Errorf("相等情境 delta = %v, want 0", delta)
	}
}
