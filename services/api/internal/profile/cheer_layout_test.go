package profile

import "testing"

// TestNormalizeCheerLayout 涵蓋合法輸入（含四捨五入）、缺 key、多餘 key、超範圍（dx/dy/scale 各自）
// 四種情況。純函式、不碰 http/db，可直接單元測試。
func TestNormalizeCheerLayout(t *testing.T) {
	valid := func() CheerLayout {
		return CheerLayout{
			"01": {DX: 0, DY: 0, Scale: 1},
			"02": {DX: 12.345, DY: -50, Scale: 1.5},
			"03": {DX: -300, DY: 300, Scale: 4},
		}
	}

	t.Run("合法輸入通過並四捨五入到小數2位", func(t *testing.T) {
		out, err := normalizeCheerLayout(valid())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 3 {
			t.Fatalf("expected 3 keys, got %d", len(out))
		}
		if out["02"].DX != 12.35 { // 12.345 四捨五入到小數2位
			t.Errorf("expected 02.dx=12.35, got %v", out["02"].DX)
		}
		if out["01"].DX != 0 || out["01"].DY != 0 || out["01"].Scale != 1 {
			t.Errorf("expected 01 unchanged, got %+v", out["01"])
		}
		if out["03"].DX != -300 || out["03"].DY != 300 || out["03"].Scale != 4 {
			t.Errorf("expected 03 boundary values preserved, got %+v", out["03"])
		}
	})

	t.Run("缺key(只有01/02)回錯誤", func(t *testing.T) {
		in := valid()
		delete(in, "03")
		if _, err := normalizeCheerLayout(in); err == nil {
			t.Fatalf("expected error for missing key 03")
		}
	})

	t.Run("多餘key(01/02/03/04)回錯誤", func(t *testing.T) {
		in := valid()
		in["04"] = CheerLayoutItem{DX: 0, DY: 0, Scale: 1}
		if _, err := normalizeCheerLayout(in); err == nil {
			t.Fatalf("expected error for extra key 04")
		}
	})

	t.Run("空map回錯誤", func(t *testing.T) {
		if _, err := normalizeCheerLayout(CheerLayout{}); err == nil {
			t.Fatalf("expected error for empty layout")
		}
	})

	dxRangeCases := []struct {
		name string
		dx   float64
	}{
		{"dx超過上限300.01", 300.01},
		{"dx超過下限-300.01", -300.01},
	}
	for _, c := range dxRangeCases {
		t.Run(c.name, func(t *testing.T) {
			in := valid()
			in["01"] = CheerLayoutItem{DX: c.dx, DY: 0, Scale: 1}
			if _, err := normalizeCheerLayout(in); err == nil {
				t.Fatalf("expected error for dx=%v", c.dx)
			}
		})
	}

	dyRangeCases := []struct {
		name string
		dy   float64
	}{
		{"dy超過上限300.01", 300.01},
		{"dy超過下限-300.01", -300.01},
	}
	for _, c := range dyRangeCases {
		t.Run(c.name, func(t *testing.T) {
			in := valid()
			in["01"] = CheerLayoutItem{DX: 0, DY: c.dy, Scale: 1}
			if _, err := normalizeCheerLayout(in); err == nil {
				t.Fatalf("expected error for dy=%v", c.dy)
			}
		})
	}

	scaleRangeCases := []struct {
		name  string
		scale float64
	}{
		{"scale低於下限0.19", 0.19},
		{"scale高於上限4.01", 4.01},
		{"scale為0", 0},
		{"scale為負", -1},
	}
	for _, c := range scaleRangeCases {
		t.Run(c.name, func(t *testing.T) {
			in := valid()
			in["01"] = CheerLayoutItem{DX: 0, DY: 0, Scale: c.scale}
			if _, err := normalizeCheerLayout(in); err == nil {
				t.Fatalf("expected error for scale=%v", c.scale)
			}
		})
	}

	t.Run("scale邊界值0.2與4合法", func(t *testing.T) {
		in := valid()
		in["01"] = CheerLayoutItem{DX: 0, DY: 0, Scale: 0.2}
		if _, err := normalizeCheerLayout(in); err != nil {
			t.Fatalf("unexpected error for scale=0.2: %v", err)
		}
		in["01"] = CheerLayoutItem{DX: 0, DY: 0, Scale: 4}
		if _, err := normalizeCheerLayout(in); err != nil {
			t.Fatalf("unexpected error for scale=4: %v", err)
		}
	})
}

func TestIsFiniteInRange(t *testing.T) {
	if !isFiniteInRange(0, -300, 300) {
		t.Errorf("expected 0 in range [-300,300]")
	}
	if isFiniteInRange(301, -300, 300) {
		t.Errorf("expected 301 out of range [-300,300]")
	}
	if isFiniteInRange(-301, -300, 300) {
		t.Errorf("expected -301 out of range [-300,300]")
	}
}

func TestRound2(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{12.345, 12.35},
		{12.344, 12.34},
		{-0.005, -0.01}, // math.Round 對 -0.5 邊界往遠離0捨入
		{1, 1},
		{0, 0},
	}
	for _, c := range cases {
		if got := round2(c.in); got != c.want {
			t.Errorf("round2(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
