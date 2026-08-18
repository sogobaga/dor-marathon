package integration

import "testing"

func TestIsCorosRunningActivity(t *testing.T) {
	cases := []struct {
		name          string
		mode, subMode int
		want          bool
	}{
		{"戶外跑", 8, 1, true},
		{"室內跑", 8, 2, true},
		{"越野跑", 15, 1, true},
		{"田徑場跑", 20, 1, true},
		{"騎車不算跑步", 9, 0, false},
		{"游泳不算跑步", 23, 0, false},
		{"code 0 不算跑步", 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isCorosRunningActivity(c.mode, c.subMode)
			if got != c.want {
				t.Fatalf("isCorosRunningActivity(%d,%d) = %v, want %v", c.mode, c.subMode, got, c.want)
			}
		})
	}
}

func TestCorosAvgPaceS(t *testing.T) {
	cases := []struct {
		name       string
		durationS  int
		distanceKm float64
		want       int
	}{
		{"5km 25分鐘 = 300秒/km", 1500, 5, 300},
		{"10km 50分鐘 = 300秒/km", 3000, 10, 300},
		{"非整除四捨五入", 1000, 3, 333}, // 333.33... → 333
		{"距離為 0 回傳 0（防除以零）", 1000, 0, 0},
		{"距離為負回傳 0", 1000, -1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := corosAvgPaceS(c.durationS, c.distanceKm)
			if got != c.want {
				t.Fatalf("corosAvgPaceS(%d,%v) = %d, want %d", c.durationS, c.distanceKm, got, c.want)
			}
		})
	}
}

func TestCorosHTTPErrorMessage(t *testing.T) {
	err := &corosHTTPError{Status: 401}
	if err.Error() != "coros http 401" {
		t.Fatalf("unexpected error message: %q", err.Error())
	}
}
