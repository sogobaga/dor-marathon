package virtualrunner

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

// TestDecideRun_GoodWeather_ProbabilityMatchesPRun 好天氣命中率應貼近理論 p_run（未觸頂夾限時）。
func TestDecideRun_GoodWeather_ProbabilityMatchesPRun(t *testing.T) {
	// avg_km=10, monthly_km=90 → p_run = 90/10/30 = 0.30（不會被夾限，方便驗證機率貼近理論值）
	p := RunnerParams{AvgKm: 10, MonthlyKm: 90, Diligence: 3, PaceFastS: 300, PaceSlowS: 330}
	rng := rand.New(rand.NewSource(42))
	const trials = 50000
	ran := 0
	for i := 0; i < trials; i++ {
		if decideRun(p, false, rng) {
			ran++
		}
	}
	got := float64(ran) / trials
	if math.Abs(got-0.30) > 0.01 {
		t.Fatalf("好天氣命中率 = %.4f，理論值 0.30，誤差超出容許範圍", got)
	}
}

// TestDecideRun_PRunClampedToBounds p_run 應夾在 [0.15,0.95]，即使理論值遠超出此區間。
func TestDecideRun_PRunClampedToBounds(t *testing.T) {
	const trials = 50000

	// 月跑量遠低於單次均距 → 理論機率趨近 0，應被夾在下限 0.15
	lowP := RunnerParams{AvgKm: 20, MonthlyKm: 1, Diligence: 3, PaceFastS: 300, PaceSlowS: 330}
	rng := rand.New(rand.NewSource(1))
	ran := 0
	for i := 0; i < trials; i++ {
		if decideRun(lowP, false, rng) {
			ran++
		}
	}
	got := float64(ran) / trials
	if math.Abs(got-0.15) > 0.01 {
		t.Fatalf("下限夾限命中率 = %.4f，應貼近 0.15", got)
	}

	// 月跑量遠高於單次均距 → 理論機率趨近 1，應被夾在上限 0.95
	highP := RunnerParams{AvgKm: 1, MonthlyKm: 999, Diligence: 3, PaceFastS: 300, PaceSlowS: 330}
	rng2 := rand.New(rand.NewSource(2))
	ran = 0
	for i := 0; i < trials; i++ {
		if decideRun(highP, false, rng2) {
			ran++
		}
	}
	got = float64(ran) / trials
	if math.Abs(got-0.95) > 0.01 {
		t.Fatalf("上限夾限命中率 = %.4f，應貼近 0.95", got)
	}
}

// TestDecideRun_BadWeatherDiligenceEffect 積極度效應：diligence=5 壞天氣不受影響，diligence=1
// 幾乎必翹；且 diligence=1 命中率必須明顯低於 diligence=5。
func TestDecideRun_BadWeatherDiligenceEffect(t *testing.T) {
	base := RunnerParams{AvgKm: 10, MonthlyKm: 90, PaceFastS: 300, PaceSlowS: 330} // p_run=0.30
	const trials = 50000

	p5 := base
	p5.Diligence = 5
	rng5 := rand.New(rand.NewSource(10))
	ran5 := 0
	for i := 0; i < trials; i++ {
		if decideRun(p5, true, rng5) {
			ran5++
		}
	}
	got5 := float64(ran5) / trials
	if math.Abs(got5-0.30) > 0.01 {
		t.Fatalf("diligence=5 壞天氣命中率 = %.4f，應貼近好天氣理論值 0.30（不受影響）", got5)
	}

	p1 := base
	p1.Diligence = 1
	rng1 := rand.New(rand.NewSource(11))
	ran1 := 0
	for i := 0; i < trials; i++ {
		if decideRun(p1, true, rng1) {
			ran1++
		}
	}
	got1 := float64(ran1) / trials
	want1 := 0.30 * (1 - 4*0.18) // factor = 0.28
	if math.Abs(got1-want1) > 0.01 {
		t.Fatalf("diligence=1 壞天氣命中率 = %.4f，理論值 %.4f", got1, want1)
	}
	if got1 >= got5 {
		t.Fatalf("積極度效應應為 diligence=1 命中率(%.4f) < diligence=5 命中率(%.4f)", got1, got5)
	}
}

// TestGenerateDistanceKm_WithinRange 好天氣/壞天氣距離皆應落在對應理論區間、且恆 >= 下限 1.0。
func TestGenerateDistanceKm_WithinRange(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	avgKm := 10.0
	for i := 0; i < 20000; i++ {
		d := generateDistanceKm(avgKm, false, rng)
		if d < 1.0 {
			t.Fatalf("好天氣距離 %.2f 低於下限 1.0", d)
		}
		if d < avgKm*0.90-0.01 || d > avgKm*1.10+0.01 {
			t.Fatalf("好天氣距離 %.2f 超出 [%.2f,%.2f]", d, avgKm*0.90, avgKm*1.10)
		}
	}
	for i := 0; i < 20000; i++ {
		d := generateDistanceKm(avgKm, true, rng)
		if d < 1.0 {
			t.Fatalf("壞天氣距離 %.2f 低於下限 1.0", d)
		}
		lo := avgKm * 0.90 * 0.70
		hi := avgKm * 1.10 * 0.80
		if d < lo-0.01 || d > hi+0.01 {
			t.Fatalf("壞天氣距離 %.2f 超出 [%.2f,%.2f]", d, lo, hi)
		}
	}
}

// TestGenerateDistanceKm_FloorAppliesForTinyAvgKm 均距很小＋壞天氣縮水時，仍應被下限夾到 1.0。
func TestGenerateDistanceKm_FloorAppliesForTinyAvgKm(t *testing.T) {
	rng := rand.New(rand.NewSource(8))
	for i := 0; i < 2000; i++ {
		d := generateDistanceKm(0.5, true, rng)
		if d < 1.0 {
			t.Fatalf("距離 %.2f 低於下限 1.0", d)
		}
	}
}

// TestGeneratePaceS_WithinRange 配速應恆落在 [fastS, slowS]。
func TestGeneratePaceS_WithinRange(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	for i := 0; i < 5000; i++ {
		p := generatePaceS(300, 330, rng)
		if p < 300 || p > 330 {
			t.Fatalf("配速 %d 超出 [300,330]", p)
		}
	}
}

// TestGeneratePaceS_GuardsInvertedRange fastS>=slowS（不合法輸入）時保守回傳 fastS，不 panic。
func TestGeneratePaceS_GuardsInvertedRange(t *testing.T) {
	rng := rand.New(rand.NewSource(0))
	if p := generatePaceS(300, 300, rng); p != 300 {
		t.Fatalf("fastS==slowS 應回傳 300，實際 %d", p)
	}
	if p := generatePaceS(300, 250, rng); p != 300 {
		t.Fatalf("fastS>slowS 應保守回傳 fastS=300，實際 %d", p)
	}
}

// TestBuildKmPaces_SumNearDuration km_paces 總和應約等於 distanceKm×paceS（即 duration_s），
// 誤差不超過「每段最多 ±8 秒抖動」所能造成的最大偏差。
func TestBuildKmPaces_SumNearDuration(t *testing.T) {
	rng := rand.New(rand.NewSource(12))
	for _, tc := range []struct {
		distanceKm float64
		paceS      int
	}{
		{5.37, 330}, {10.0, 300}, {1.2, 480}, {21.1, 260}, {1.0, 500},
	} {
		paces := buildKmPaces(tc.distanceKm, tc.paceS, rng)
		if len(paces) == 0 {
			t.Fatalf("distanceKm=%.2f: km_paces 不應為空", tc.distanceKm)
		}
		sum := 0
		for _, s := range paces {
			sum += s
		}
		want := int(math.Round(tc.distanceKm * float64(tc.paceS)))
		tolerance := kmPaceJitterS*len(paces) + len(paces) + 5
		if diff := sum - want; diff > tolerance || diff < -tolerance {
			t.Fatalf("distanceKm=%.2f paceS=%d: km_paces 總和=%d，duration=%d，偏差 %d 超出容許 ±%d",
				tc.distanceKm, tc.paceS, sum, want, diff, tolerance)
		}
	}
}

// TestGenerateActivity_NotRanReturnsZeroValue Ran=false 分支的回傳值形狀正確（不夾帶殘留數值）。
func TestGenerateActivity_NotRanReturnsZeroValue(t *testing.T) {
	rng := rand.New(rand.NewSource(0))
	// p_run 夾在 0.15 下限，但仍有 15% 機率跑，多輪嘗試必能抽到「不跑」。
	p := RunnerParams{AvgKm: 20, MonthlyKm: 0.001, Diligence: 3, PaceFastS: 300, PaceSlowS: 330}
	var gotNotRan bool
	for i := 0; i < 2000 && !gotNotRan; i++ {
		act := GenerateActivity(p, Weather{}, 5, rng)
		if !act.Ran {
			gotNotRan = true
			if act.DistanceKm != 0 || act.PaceS != 0 || act.DurationS != 0 || len(act.KmPaces) != 0 {
				t.Fatalf("Ran=false 時欄位應為零值，實際 %+v", act)
			}
		}
	}
	if !gotNotRan {
		t.Fatal("2000 次嘗試都命中『有跑』，測試情境機率設計有誤")
	}
}

// TestGenerateActivity_RanFieldsConsistent Ran=true 分支各欄位彼此一致（duration=distance*pace、
// 起跑時分秒落在合法範圍等）。
func TestGenerateActivity_RanFieldsConsistent(t *testing.T) {
	rng := rand.New(rand.NewSource(0))
	// p_run 夾上限 0.95，幾乎必跑。
	p := RunnerParams{AvgKm: 15, MonthlyKm: 999, Diligence: 5, PaceFastS: 300, PaceSlowS: 330}
	var gotRan bool
	for i := 0; i < 2000 && !gotRan; i++ {
		act := GenerateActivity(p, Weather{}, 20, rng)
		if act.Ran {
			gotRan = true
			if act.DistanceKm < 1.0 {
				t.Fatalf("DistanceKm=%.2f 低於下限", act.DistanceKm)
			}
			if act.PaceS < 300 || act.PaceS > 330 {
				t.Fatalf("PaceS=%d 超出範圍", act.PaceS)
			}
			wantDuration := int(math.Round(act.DistanceKm * float64(act.PaceS)))
			if act.DurationS != wantDuration {
				t.Fatalf("DurationS=%d，want %d", act.DurationS, wantDuration)
			}
			if act.StartHour != 20 {
				t.Fatalf("StartHour=%d，want 20", act.StartHour)
			}
			if act.StartMin < 0 || act.StartMin > 59 || act.StartSec < 0 || act.StartSec > 59 {
				t.Fatalf("起跑時刻 %d:%d 超出 0-59", act.StartMin, act.StartSec)
			}
			if len(act.KmPaces) == 0 {
				t.Fatal("KmPaces 不應為空")
			}
		}
	}
	if !gotRan {
		t.Fatal("2000 次嘗試都沒命中『有跑』，測試情境機率設計有誤")
	}
}

// TestGeneratorTriggerHours_MapToValidWindowHours 觸發時集合（H）與 model.go 的 window_hour
// 白名單（H-1）必須一一對應，不會漂移。
func TestGeneratorTriggerHours_MapToValidWindowHours(t *testing.T) {
	for h := range generatorTriggerHours {
		wh := h - 1
		if !ValidWindowHour(wh) {
			t.Errorf("觸發時 H=%d 對應 window_hour=%d 不在白名單內", h, wh)
		}
	}
	for _, wh := range windowHourList {
		if !generatorTriggerHours[wh+1] {
			t.Errorf("window_hour=%d 沒有對應的觸發時 H=%d", wh, wh+1)
		}
	}
}

// TestWindowStartUTC 台灣時區換算回 UTC 需正確跨日（台灣時刻早於 8 點時，對應 UTC 是前一天）。
func TestWindowStartUTC(t *testing.T) {
	tw := time.Date(2026, 8, 21, 7, 30, 0, 0, time.UTC) // taiwanNow() 的「移位表示」：台灣 07:30
	got := windowStartUTC(tw, 6, 15, 40)                // window_hour=6 → 台灣 06:15:40
	want := time.Date(2026, 8, 20, 22, 15, 40, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("windowStartUTC = %v, want %v", got, want)
	}
}

// TestWindowStartUTC_SameDayNoRollback 台灣時刻晚於 8 點時，UTC 換算仍在同一天。
func TestWindowStartUTC_SameDayNoRollback(t *testing.T) {
	tw := time.Date(2026, 8, 21, 21, 0, 0, 0, time.UTC) // 台灣 21:00
	got := windowStartUTC(tw, 20, 5, 9)                 // window_hour=20 → 台灣 20:05:09
	want := time.Date(2026, 8, 21, 12, 5, 9, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("windowStartUTC = %v, want %v", got, want)
	}
}
