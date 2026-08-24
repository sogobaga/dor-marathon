package training

import "testing"

// TestValidateStrategy_Segments segments 規則：首段須從 0 開始、段間必須連續銜接（無縫隙/無重疊）、
// to_km 須大於 from_km、pace_s 須落在合理配速區間內。
func TestValidateStrategy_Segments(t *testing.T) {
	cases := []struct {
		name     string
		segments []StrategySegment
		wantErr  string
	}{
		{
			"合法：兩段連續銜接",
			[]StrategySegment{{FromKm: 0, ToKm: 21, PaceS: 300}, {FromKm: 21, ToKm: 42.195, PaceS: 330}},
			"",
		},
		{"空 segments → invalid_segments", []StrategySegment{}, "invalid_segments"},
		{
			"首段 from_km 非 0 → segments_must_start_at_zero",
			[]StrategySegment{{FromKm: 1, ToKm: 10, PaceS: 300}},
			"segments_must_start_at_zero",
		},
		{
			"段間有缺口（第二段 from_km 跳號）→ segments_not_contiguous",
			[]StrategySegment{{FromKm: 0, ToKm: 10, PaceS: 300}, {FromKm: 12, ToKm: 20, PaceS: 300}},
			"segments_not_contiguous",
		},
		{
			"段間重疊 → segments_not_contiguous",
			[]StrategySegment{{FromKm: 0, ToKm: 10, PaceS: 300}, {FromKm: 8, ToKm: 20, PaceS: 300}},
			"segments_not_contiguous",
		},
		{
			"to_km 等於 from_km（零長度）→ invalid_segment_range",
			[]StrategySegment{{FromKm: 0, ToKm: 0, PaceS: 300}},
			"invalid_segment_range",
		},
		{
			"to_km 小於 from_km（倒退）→ invalid_segment_range",
			[]StrategySegment{{FromKm: 0, ToKm: 10, PaceS: 300}, {FromKm: 10, ToKm: 5, PaceS: 300}},
			"invalid_segment_range",
		},
		{
			"pace_s 低於下限（比世界紀錄還快）→ invalid_pace",
			[]StrategySegment{{FromKm: 0, ToKm: 10, PaceS: 100}},
			"invalid_pace",
		},
		{
			"pace_s 高於上限 → invalid_pace",
			[]StrategySegment{{FromKm: 0, ToKm: 10, PaceS: 2000}},
			"invalid_pace",
		},
		{
			"pace_s 剛好等於邊界（120/1800）→ 合法",
			[]StrategySegment{{FromKm: 0, ToKm: 10, PaceS: 120}, {FromKm: 10, ToKm: 20, PaceS: 1800}},
			"",
		},
		{
			"段數超過上限（31 段）→ invalid_segments",
			manySegments(strategySegMax + 1),
			"invalid_segments",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, errCode := validateStrategy(strategyRequest{Name: "測試策略", Segments: c.segments})
			if errCode != c.wantErr {
				t.Fatalf("got errCode=%q, want %q", errCode, c.wantErr)
			}
		})
	}
}

// TestValidateStrategy_TotalKm total_km 由後端計算＝segments 最後一段 to_km（不信前端傳入值）。
func TestValidateStrategy_TotalKm(t *testing.T) {
	segments := []StrategySegment{
		{FromKm: 0, ToKm: 21, PaceS: 300},
		{FromKm: 21, ToKm: 42.195, PaceS: 330},
	}
	_, totalKm, errCode := validateStrategy(strategyRequest{Name: "全馬配速", Segments: segments})
	if errCode != "" {
		t.Fatalf("unexpected errCode=%q", errCode)
	}
	if totalKm != 42.195 {
		t.Fatalf("got total_km=%v, want 42.195", totalKm)
	}
}

// TestValidateStrategy_Name name 去空白後須 1~50 字。
func TestValidateStrategy_Name(t *testing.T) {
	validSegs := []StrategySegment{{FromKm: 0, ToKm: 10, PaceS: 300}}
	cases := []struct {
		name    string
		input   string
		wantErr string
	}{
		{"合法名稱", "波馬 BQ 配速", ""},
		{"空字串 → invalid_name", "", "invalid_name"},
		{"純空白（trim 後為空）→ invalid_name", "   ", "invalid_name"},
		{"前後帶空白 → trim 後合法", "  全馬策略A  ", ""},
		{"超過 50 字 → invalid_name", stringOfLen(51), "invalid_name"},
		{"剛好 50 字 → 合法", stringOfLen(50), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotName, _, errCode := validateStrategy(strategyRequest{Name: c.input, Segments: validSegs})
			if errCode != c.wantErr {
				t.Fatalf("got errCode=%q, want %q", errCode, c.wantErr)
			}
			if errCode == "" && gotName == "" {
				t.Fatalf("expected trimmed name to be non-empty")
			}
		})
	}
}

// TestValidateStrategy_Fuel fuel 規則：kind/mode 白名單、at 必須為正值、依 mode 各自有上限。
func TestValidateStrategy_Fuel(t *testing.T) {
	validSegs := []StrategySegment{{FromKm: 0, ToKm: 42.195, PaceS: 300}}
	cases := []struct {
		name    string
		fuel    []FuelPoint
		wantErr string
	}{
		{"0 筆合法（選填）", []FuelPoint{}, ""},
		{"合法：time 模式", []FuelPoint{{Kind: "gel", Mode: "time", At: 1800}}, ""},
		{"合法：distance 模式", []FuelPoint{{Kind: "salt", Mode: "distance", At: 10000}}, ""},
		{"不明 kind → invalid_fuel_kind", []FuelPoint{{Kind: "vitamin", Mode: "time", At: 100}}, "invalid_fuel_kind"},
		{"不明 mode → invalid_fuel_mode", []FuelPoint{{Kind: "gel", Mode: "km", At: 100}}, "invalid_fuel_mode"},
		{"at=0 → invalid_fuel_at", []FuelPoint{{Kind: "gel", Mode: "time", At: 0}}, "invalid_fuel_at"},
		{"at 負數 → invalid_fuel_at", []FuelPoint{{Kind: "gel", Mode: "time", At: -5}}, "invalid_fuel_at"},
		{"time 模式超過 86400 秒 → invalid_fuel_at", []FuelPoint{{Kind: "gel", Mode: "time", At: 86401}}, "invalid_fuel_at"},
		{"time 模式剛好等於上限 → 合法", []FuelPoint{{Kind: "gel", Mode: "time", At: 86400}}, ""},
		{"distance 模式超過 200000 公尺 → invalid_fuel_at", []FuelPoint{{Kind: "caffeine", Mode: "distance", At: 200001}}, "invalid_fuel_at"},
		{"distance 模式剛好等於上限 → 合法", []FuelPoint{{Kind: "caffeine", Mode: "distance", At: 200000}}, ""},
		{"超過上限點數（31 點）→ invalid_fuel", manyFuelPoints(strategyFuelMax + 1), "invalid_fuel"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, errCode := validateStrategy(strategyRequest{Name: "測試策略", Segments: validSegs, Fuel: c.fuel})
			if errCode != c.wantErr {
				t.Fatalf("got errCode=%q, want %q", errCode, c.wantErr)
			}
		})
	}
}

func manySegments(n int) []StrategySegment {
	segs := make([]StrategySegment, n)
	for i := 0; i < n; i++ {
		segs[i] = StrategySegment{FromKm: float64(i), ToKm: float64(i + 1), PaceS: 300}
	}
	return segs
}

func manyFuelPoints(n int) []FuelPoint {
	pts := make([]FuelPoint, n)
	for i := 0; i < n; i++ {
		pts[i] = FuelPoint{Kind: "gel", Mode: "time", At: float64(600 * (i + 1))}
	}
	return pts
}

func stringOfLen(n int) string {
	r := make([]rune, n)
	for i := range r {
		r[i] = 'A'
	}
	return string(r)
}
