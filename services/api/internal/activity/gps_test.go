package activity

import (
	"math"
	"strings"
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

// metersPerDegLng 這批測試共用的經度→公尺換算（緯度 25.033° 處，與 haversineM/perpDistM 同一套
// 等距投影公式：111320 × cos(lat)），供測試建構「約 N 公尺」的軌跡點，容許小誤差。
var metersPerDegLng = 111320 * math.Cos(25.0330*math.Pi/180)

// TestComputeRun_MRTGapExcluded 是 2026-09-03 事故的回歸測試：步行 2km（每 10 秒一點、時速約
// 2m/s）後搭捷運（地下無 GPS）跳到 10km 外、gap 長達 25 分鐘。舊邏輯只看 d/dt（10000/1500≈6.7m/s，
// 低於 8.33m/s 的超速門檻）會讓整段直線距離被誤計入；新的斷點規則（dt>60s 且 d>250m）必須把這段
// 排除、只保留步行的 2km，且不能被標記為異常（訊號斷點不是作弊）。
func TestComputeRun_MRTGapExcluded(t *testing.T) {
	const lat0 = 25.0330
	const lng0 = 121.5000
	stepLng := 20.0 / metersPerDegLng // 每步約 20m（2m/s × 10s）

	pts := make([]gpsPoint, 0, 102)
	for i := 0; i <= 100; i++ { // 101 個點、100 段，每段 20m，累積約 2000m
		pts = append(pts, gpsPoint{Lat: lat0, Lng: lng0 + float64(i)*stepLng, T: int64(i) * 10000, Acc: 10})
	}
	last := pts[len(pts)-1]
	farLng := last.Lng + 10000.0/metersPerDegLng // 再往東跳約 10km
	pts = append(pts, gpsPoint{Lat: lat0, Lng: farLng, T: last.T + 1500000, Acc: 10})

	calc, err := computeRun(pts, 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calc.Flagged {
		t.Errorf("expected Flagged=false (signal gap is not abuse), got true (reason=%s)", calc.FlagReason)
	}
	if math.Abs(calc.RawKm-2.0) > 0.05 {
		t.Errorf("RawKm = %f, want ≈2.0 (only the walked portion)", calc.RawKm)
	}
	excludedKm := calc.ExcludedM / 1000.0
	if math.Abs(excludedKm-10.0) > 0.1 {
		t.Errorf("ExcludedM/1000 = %f, want ≈10.0 (the straight-line MRT jump)", excludedKm)
	}
	if calc.ExcludedSegs != 1 {
		t.Errorf("ExcludedSegs = %d, want 1", calc.ExcludedSegs)
	}
	if calc.Anomalies != 0 {
		t.Errorf("Anomalies = %d, want 0 (gap rule must not feed the speed-based fraud signal)", calc.Anomalies)
	}

	poly := encodePolylineSegments(pts, calc.BreakBefore)
	if n := strings.Count(poly, "|"); n != 1 {
		t.Errorf("polyline has %d '|' separators, want exactly 1 (one break at the MRT jump): %q", n, poly)
	}
}

// TestComputeRun_ShortTunnelGapNotExcluded 40 秒的隧道斷點（dt 未過 60 秒門檻）即使位移達 180m，
// 仍是正常訊號差，不該被排除——dt 未過門檻時，gap 規則完全不生效（不看 d）。
func TestComputeRun_ShortTunnelGapNotExcluded(t *testing.T) {
	pts := []gpsPoint{
		{Lat: 25.0330, Lng: 121.5000, T: 0, Acc: 10},
		{Lat: 25.0330, Lng: 121.5000 + 180.0/metersPerDegLng, T: 40000, Acc: 10}, // 40s、約180m
	}
	calc, err := computeRun(pts, 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calc.ExcludedSegs != 0 || calc.ExcludedM != 0 {
		t.Errorf("expected nothing excluded for a 40s/180m gap, got ExcludedSegs=%d ExcludedM=%f", calc.ExcludedSegs, calc.ExcludedM)
	}
	if math.Abs(calc.RawKm-0.18) > 0.02 {
		t.Errorf("RawKm = %f, want ≈0.18 (the 180m must count as normal distance)", calc.RawKm)
	}
}

// TestComputeRun_GapDistanceThreshold 驗證斷點規則的「且」邏輯：dt 都是 90 秒（過門檻），
// d=200m（未過 250m 門檻）不排除；d=300m（過門檻）才排除。
func TestComputeRun_GapDistanceThreshold(t *testing.T) {
	mk := func(dMeters float64) []gpsPoint {
		return []gpsPoint{
			{Lat: 25.0330, Lng: 121.5000, T: 0, Acc: 10},
			{Lat: 25.0330, Lng: 121.5000 + dMeters/metersPerDegLng, T: 90000, Acc: 10},
		}
	}

	under, err := computeRun(mk(200), 1.0)
	if err != nil {
		t.Fatalf("d=200: unexpected error: %v", err)
	}
	if under.ExcludedSegs != 0 {
		t.Errorf("d=200,dt=90: expected not excluded (d under 250m threshold), got ExcludedSegs=%d", under.ExcludedSegs)
	}

	over, err := computeRun(mk(300), 1.0)
	if err != nil {
		t.Fatalf("d=300: unexpected error: %v", err)
	}
	if over.ExcludedSegs != 1 {
		t.Errorf("d=300,dt=90: expected excluded (d over 250m threshold), got ExcludedSegs=%d", over.ExcludedSegs)
	}
	if over.Anomalies != 0 {
		t.Errorf("d=300,dt=90: gap-only exclusion must not count as a speed anomaly, got Anomalies=%d", over.Anomalies)
	}
}

// TestEncodePolylineSegments_BreakInMiddle 直接測純函式：中間一個斷點應切成兩段、以 "|" 串接，
// 兩段各自都是非空、可還原的 encoded polyline（見函式註解——沒有斷點時等同單一 polyline，
// 與舊資料格式相容）。
func TestEncodePolylineSegments_BreakInMiddle(t *testing.T) {
	pts := []gpsPoint{
		{Lat: 25.0330, Lng: 121.5000, T: 0},
		{Lat: 25.0331, Lng: 121.5002, T: 10000},
		{Lat: 25.0332, Lng: 121.5004, T: 20000},
		{Lat: 25.0500, Lng: 121.5300, T: 30000}, // 斷點：這個點前另起一段
		{Lat: 25.0501, Lng: 121.5302, T: 40000},
		{Lat: 25.0502, Lng: 121.5304, T: 50000},
	}
	poly := encodePolylineSegments(pts, map[int]bool{3: true})
	parts := strings.Split(poly, "|")
	if len(parts) != 2 {
		t.Fatalf("expected 2 segments (1 break) in %q, got %d part(s)", poly, len(parts))
	}
	for i, part := range parts {
		if part == "" {
			t.Errorf("segment %d is empty, want non-empty decodable polyline", i)
		}
	}
}

// TestEncodePolylineSegments_NoBreaksMatchesPlainEncoding 沒有任何斷點時，
// encodePolylineSegments 必須退化成與 encodePolyline(simplifyPath(...)) 完全相同的單一字串
// （不含 "|"）——保證舊資料格式（無 "|"）相容，前端不用特判新舊格式。
func TestEncodePolylineSegments_NoBreaksMatchesPlainEncoding(t *testing.T) {
	pts := []gpsPoint{
		{Lat: 25.0330, Lng: 121.5000, T: 0},
		{Lat: 25.0331, Lng: 121.5002, T: 10000},
		{Lat: 25.0332, Lng: 121.5004, T: 20000},
	}
	got := encodePolylineSegments(pts, map[int]bool{})
	if strings.Contains(got, "|") {
		t.Errorf("expected no '|' when there are no breaks, got %q", got)
	}
	latlng := [][2]float64{{pts[0].Lat, pts[0].Lng}, {pts[1].Lat, pts[1].Lng}, {pts[2].Lat, pts[2].Lng}}
	want := encodePolyline(simplifyPath(latlng, 5))
	if got != want {
		t.Errorf("encodePolylineSegments (no breaks) = %q, want %q (identical to plain encodePolyline)", got, want)
	}
}
