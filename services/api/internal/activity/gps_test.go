package activity

import (
	"math"
	"testing"
)

// syntheticTrack 產生一段固定往正東移動、平均配速 6:00/km 的軌跡（10 個點，每點間隔 60 秒、
// 位移約 166.7m，總距離約 1.5km，遠低於超速門檻，不觸發防弊），供 computeRun 的 k 不變量測試用。
func syntheticTrack(n int) []gpsPoint {
	const lat0 = 25.0330
	const stepLng = 0.0015 // 約每點 166.7m（赤道附近粗略換算，實際值不影響測試——只需與 k 無關）
	pts := make([]gpsPoint, n)
	for i := 0; i < n; i++ {
		pts[i] = gpsPoint{Lat: lat0, Lng: 121.5 + float64(i)*stepLng, T: int64(i) * 60000, Acc: 10}
	}
	return pts
}

func TestComputeRun_CalibrationDoesNotAffectFraudDetection(t *testing.T) {
	pts := syntheticTrack(20)
	calc1, err := computeRun(pts, 1.0)
	if err != nil {
		t.Fatalf("k=1.0: %v", err)
	}
	calcK, err := computeRun(pts, 0.9781)
	if err != nil {
		t.Fatalf("k=0.9781: %v", err)
	}

	if calc1.Flagged != calcK.Flagged {
		t.Errorf("Flagged differs by k: k=1.0 -> %v, k=0.9781 -> %v", calc1.Flagged, calcK.Flagged)
	}
	if calc1.FlagReason != calcK.FlagReason {
		t.Errorf("FlagReason differs by k: %q vs %q", calc1.FlagReason, calcK.FlagReason)
	}
	if calc1.Anomalies != calcK.Anomalies {
		t.Errorf("Anomalies differs by k: %d vs %d", calc1.Anomalies, calcK.Anomalies)
	}
	if calc1.RawKm != calcK.RawKm {
		t.Errorf("RawKm differs by k: %f vs %f (must be identical — raw distance never touched by calibration)", calc1.RawKm, calcK.RawKm)
	}
	if calc1.RawAvgPaceS != calcK.RawAvgPaceS {
		t.Errorf("RawAvgPaceS differs by k: %d vs %d", calc1.RawAvgPaceS, calcK.RawAvgPaceS)
	}
	if calc1.UsedPointCount != calcK.UsedPointCount {
		t.Errorf("UsedPointCount differs by k: %d vs %d", calc1.UsedPointCount, calcK.UsedPointCount)
	}

	// 校正值：distanceKm = round2(rawKm*k)（這裡不用 round2 比較，容忍浮點誤差）
	wantDistanceKm := calc1.RawKm * 0.9781
	if math.Abs(calcK.DistanceKm-wantDistanceKm) > 1e-9 {
		t.Errorf("DistanceKm = %f, want %f (rawKm*k)", calcK.DistanceKm, wantDistanceKm)
	}
	if calc1.DistanceKm != calc1.RawKm { // k=1.0 時校正後應與原始相同
		t.Errorf("k=1.0: DistanceKm(%f) should equal RawKm(%f)", calc1.DistanceKm, calc1.RawKm)
	}

	// kmSplits 段數 = floor(rawM*k/1000)（rawM 就是這條無超速軌跡的 distM，等於 RawKm*1000）
	wantSplits := int(math.Floor(calc1.RawKm * 1000 * 0.9781 / 1000))
	if len(calcK.KmSplits) != wantSplits {
		t.Errorf("k=0.9781: len(KmSplits) = %d, want %d (floor(rawM*k/1000))", len(calcK.KmSplits), wantSplits)
	}
	wantSplits1 := int(math.Floor(calc1.RawKm * 1000 / 1000))
	if len(calc1.KmSplits) != wantSplits1 {
		t.Errorf("k=1.0: len(KmSplits) = %d, want %d", len(calc1.KmSplits), wantSplits1)
	}
}

// syntheticVehicleTrack 產生一段遠超人類極限速度的軌跡（疑似載具），驗證 flagged 判定與 k 無關
// ——即使距離被校正打折，仍然不能讓一趟本該被標記的軌跡「校正後」看起來正常。
func syntheticVehicleTrack(n int) []gpsPoint {
	const lat0 = 25.0330
	const stepLng = 0.01 // 每點約 1000+ 公尺、間隔僅 5 秒 -> 遠超 2:00/km 對應速度
	pts := make([]gpsPoint, n)
	for i := 0; i < n; i++ {
		pts[i] = gpsPoint{Lat: lat0, Lng: 121.5 + float64(i)*stepLng, T: int64(i) * 5000, Acc: 10}
	}
	return pts
}

func TestComputeRun_VehicleFlaggedRegardlessOfCalibration(t *testing.T) {
	pts := syntheticVehicleTrack(30)
	for _, k := range []float64{1.0, 0.92, 0.9781} {
		calc, err := computeRun(pts, k)
		if err != nil {
			t.Fatalf("k=%v: %v", k, err)
		}
		if !calc.Flagged {
			t.Errorf("k=%v: expected Flagged=true for vehicle-speed track, got false", k)
		}
	}
}

func TestComputeRun_TooFewPointsHasNoAnomalies(t *testing.T) {
	// 兩點、正常步行速度：不該有任何超速段，flagged=false。
	pts := []gpsPoint{
		{Lat: 25.0330, Lng: 121.5000, T: 0, Acc: 10},
		{Lat: 25.0335, Lng: 121.5005, T: 300000, Acc: 10}, // 5 分鐘、約 700m，配速遠慢於 2:00/km
	}
	calc, err := computeRun(pts, 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calc.Flagged {
		t.Errorf("expected Flagged=false for slow walking pace, got true (reason=%s)", calc.FlagReason)
	}
	if calc.Anomalies != 0 {
		t.Errorf("Anomalies = %d, want 0", calc.Anomalies)
	}
}

func TestComputeRun_ZeroDurationErrors(t *testing.T) {
	pts := []gpsPoint{
		{Lat: 25.0330, Lng: 121.5000, T: 1000, Acc: 10},
		{Lat: 25.0330, Lng: 121.5000, T: 1000, Acc: 10}, // 同一時間戳 -> durationS=0
	}
	if _, err := computeRun(pts, 1.0); err == nil {
		t.Error("expected error for zero-duration track, got nil")
	}
}
