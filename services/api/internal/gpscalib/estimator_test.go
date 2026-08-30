package gpscalib

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"testing"
	"time"
)

// --- fixture 載入（24 組真實配對，見 testdata/calib_pairs_fixture.json；與
// scratchpad/calib_final.py 讀的是同一份資料，回測輸出即本檔案期望值的來源）---

type fixtureRow struct {
	GpsT   string  `json:"gps_t"`
	ExtT   string  `json:"ext_t"`
	GpsKm  float64 `json:"gps_km"`
	ExtKm  float64 `json:"ext_km"`
	GpsDur int     `json:"gps_dur"`
	ExtDur int     `json:"ext_dur"`
}

func parseFixtureTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse("2006/01/02 15:04:05", "2026/"+s)
	if err != nil {
		t.Fatalf("parse fixture time %q: %v", s, err)
	}
	return tm.UTC()
}

// loadFixturePairs 回傳依 gps_t 字面值分組的 GpsRunID（同一趟 GPS 若配對到多個外部候選，
// GpsRunID 相同 —— 對應 idx14/15 共用同一趟 08/01 22:24:35 GPS 紀錄的情境）、每列各自唯一的
// ExtActivityID；ExtSource 一律 "strava"（樣本資料透過 Strava 匯入的 COROS 錶）。
func loadFixturePairs(t *testing.T) []Pair {
	t.Helper()
	b, err := os.ReadFile("testdata/calib_pairs_fixture.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var rows []fixtureRow
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	pairs := make([]Pair, len(rows))
	for i, r := range rows {
		gpsEnd := parseFixtureTime(t, r.GpsT)
		gpsStart := gpsEnd.Add(-time.Duration(r.GpsDur) * time.Second)
		extStart := parseFixtureTime(t, r.ExtT)
		pairs[i] = Pair{
			GpsRunID:      r.GpsT,
			ExtActivityID: idxLabel(i + 1),
			ExtSource:     "strava",
			RawGpsKm:      r.GpsKm,
			ExtKm:         r.ExtKm,
			GpsDurS:       r.GpsDur,
			ExtDurS:       r.ExtDur,
			GpsStart:      gpsStart,
			ExtStart:      extStart,
		}
	}
	return pairs
}

func idxLabel(i int) string {
	return "ext" + strconv.Itoa(i)
}

// expectedRejects：1-based fixture 位置 -> 期望的拒絕原因分類（Gate 回傳的 Reason 字串前綴）。
// 未列出的位置（3,10,17,18,19,21,22,23,24）期望 Accepted=true。
var expectedRejects = map[int]string{
	1: "partial", 2: "edge", 4: "partial", 5: "partial", 6: "edge", 7: "partial",
	8: "short", 9: "partial", 11: "edge", 12: "partial", 13: "partial", 14: "partial",
	15: "ambiguous", 16: "edge", 20: "partial",
}

var expectedAccepted = map[int]bool{3: true, 10: true, 17: true, 18: true, 19: true, 21: true, 22: true, 23: true, 24: true}

func TestGate_FixtureAcceptedAndReasons(t *testing.T) {
	pairs := loadFixturePairs(t)
	gated := Gate(pairs, "strava")
	if len(gated) != 24 {
		t.Fatalf("expected 24 gated pairs, got %d", len(gated))
	}
	for i, g := range gated {
		idx := i + 1
		if expectedAccepted[idx] {
			if !g.Accepted {
				t.Errorf("idx %d: expected accepted, got rejected (reason=%s)", idx, g.Reason)
			}
			continue
		}
		wantPrefix, ok := expectedRejects[idx]
		if !ok {
			t.Fatalf("test bug: idx %d has no expectation", idx)
		}
		if g.Accepted {
			t.Errorf("idx %d: expected rejected(%s), got accepted", idx, wantPrefix)
			continue
		}
		if g.Reason != wantPrefix && !hasPrefix(g.Reason, wantPrefix) {
			t.Errorf("idx %d: expected reason %q, got %q", idx, wantPrefix, g.Reason)
		}
	}
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// TestWalkForward_FixtureTrajectory 逐筆重播 24 組（依時間序，與正式系統「每次上傳/匯入都
// RecomputeAsync」的行為一致）：每次新配對進來就用「目前累積的 accepted 視窗」重新
// EstimateWindow + Publish，前次 Publish 的結果餵回當下一次的 prevFactor（遲滯步幅需要）。
// 只在配對被 accept 時記錄軌跡點（拒絕的配對不改變視窗，重算後 factor/status 不變）。
func TestWalkForward_FixtureTrajectory(t *testing.T) {
	pairs := loadFixturePairs(t)
	gated := Gate(pairs, "strava")

	wantTrajectory := []float64{1.0000, 1.0000, 1.0000, 1.0000, 0.9823, 0.9842, 0.9828, 0.9806, 0.9781}
	wantStatus := []string{"warming", "warming", "warming", "warming", "active", "active", "active", "active", "active"}

	var accepted []Gated
	var trajectory []float64
	var statuses []string
	factor := 1.0
	for _, g := range gated {
		if !g.Accepted {
			continue
		}
		accepted = append(accepted, g)
		now := g.GpsStart
		est := EstimateWindow(accepted, now)
		pub := Publish(est, factor, nil, now)
		factor = pub.Factor
		trajectory = append(trajectory, factor)
		statuses = append(statuses, pub.Status)
	}

	if len(trajectory) != len(wantTrajectory) {
		t.Fatalf("trajectory length = %d, want %d (%v)", len(trajectory), len(wantTrajectory), trajectory)
	}
	const tol = 0.001
	for i, want := range wantTrajectory {
		if math.Abs(trajectory[i]-want) > tol {
			t.Errorf("step %d: factor = %.4f, want %.4f (±%.4f)", i, trajectory[i], want, tol)
		}
		if statuses[i] != wantStatus[i] {
			t.Errorf("step %d: status = %s, want %s", i, statuses[i], wantStatus[i])
		}
	}

	// 最終視窗（9 組 accepted）：n_eff≈8.13、sigma=0.015（MAD 值低於下限被 floor 撐住）、
	// 07/20（idx=10，離群配對）在最終視窗裡的 inlier 權重 ≤0.15（Huber 穩健回歸正確把它降權）。
	finalEst := EstimateWindow(accepted, accepted[len(accepted)-1].GpsStart)
	if math.Abs(finalEst.NEff-8.13) > 0.05 {
		t.Errorf("final NEff = %.2f, want ≈8.13", finalEst.NEff)
	}
	if math.Abs(finalEst.Sigma-0.015) > 1e-6 {
		t.Errorf("final Sigma = %.6f, want 0.015000", finalEst.Sigma)
	}
	outlierIdx := -1
	for i, g := range finalEst.Window {
		if g.ExtActivityID == idxLabel(10) {
			outlierIdx = i
		}
	}
	if outlierIdx < 0 {
		t.Fatal("outlier pair (idx 10) not found in final window")
	}
	if finalEst.InlierW[outlierIdx] > 0.15 {
		t.Errorf("outlier InlierW = %.4f, want ≤0.15", finalEst.InlierW[outlierIdx])
	}
}

// --- Compute 便利函式的一次性回歸（僅驗證 Gate+EstimateWindow+Publish 串接無誤，細節已由
// 上面兩個測試覆蓋）---
func TestCompute_FixtureFinalFactor(t *testing.T) {
	pairs := loadFixturePairs(t)
	res := Compute(pairs, "strava", 1.0, nil, pairs[len(pairs)-1].GpsStart)
	if res.Pub.Status != "active" {
		t.Errorf("status = %s, want active", res.Pub.Status)
	}
	// Compute 是「一次性」估計（不是逐筆 walk-forward 步幅），所以係數不必等於軌跡最後一步的
	// 0.9781——這裡只驗證落在合理範圍（先驗+資料都指向偏低），並確認 Gate 部分回傳的
	// accepted 數與逐筆測試一致。
	if res.Pub.Factor <= ClampLo || res.Pub.Factor > ClampHi {
		t.Errorf("factor %.4f out of clamp range [%.2f,%.2f]", res.Pub.Factor, ClampLo, ClampHi)
	}
	accCount := 0
	for _, g := range res.Gated {
		if g.Accepted {
			accCount++
		}
	}
	if accCount != 9 {
		t.Errorf("accepted count = %d, want 9", accCount)
	}
}

// --- 邊界條件（規格 §6 測試 3，數值來自 scratchpad/calib_final.py 實際執行輸出）---

func syntheticWindow(ratios []float64, gpsKm float64, now time.Time) []Gated {
	out := make([]Gated, len(ratios))
	for i, r := range ratios {
		out[i] = Gated{
			Pair: Pair{
				RawGpsKm: gpsKm,
				GpsStart: now.Add(-time.Duration(i) * 24 * time.Hour),
			},
			LogRatio: math.Log(r),
			DistW:    clamp(gpsKm/DistRefKm, DistWMin, DistWMax),
			Accepted: true,
		}
	}
	return out
}

func TestPublish_ZeroPairs(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	est := EstimateWindow(nil, now)
	pub := Publish(est, 1.0, nil, now)
	if pub.Status != "warming" || pub.Factor != 1.0 {
		t.Errorf("got %+v, want {1.0 warming}", pub)
	}
}

func TestPublish_BelowMinEff_Warming(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	win := syntheticWindow([]float64{0.97, 0.97, 0.97}, 5, now)
	est := EstimateWindow(win, now)
	if math.Abs(est.NEff-3.0) > 0.01 {
		t.Errorf("NEff = %.2f, want 3.00", est.NEff)
	}
	pub := Publish(est, 1.0, nil, now)
	if pub.Status != "warming" || pub.Factor != 1.0 {
		t.Errorf("got %+v, want {1.0 warming}", pub)
	}
}

func TestPublish_SameDirectionHitsCeiling(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	win := syntheticWindow([]float64{1.20, 1.20, 1.20, 1.20, 1.20, 1.20}, 5, now)
	est := EstimateWindow(win, now)
	pub := Publish(est, 1.0, nil, now)
	if pub.Status != "active" || pub.Factor != 1.0 {
		t.Errorf("got %+v, want {1.0 active} (ceiling engaged, k>1 clamped to 1.00)", pub)
	}
}

func TestPublish_MixedOutliers_Unstable(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	win := syntheticWindow([]float64{0.97, 1.16, 0.85, 0.97, 1.20, 0.80}, 5, now)
	est := EstimateWindow(win, now)
	if est.Sigma <= SigmaMaxActive {
		t.Errorf("Sigma = %.4f, want > %.2f (mixed outliers should be unstable)", est.Sigma, SigmaMaxActive)
	}
	pub := Publish(est, 1.0, nil, now)
	if pub.Status != "unstable" || pub.Factor != 1.0 {
		t.Errorf("got %+v, want {1.0 unstable}", pub)
	}
}

func TestPublish_LowRatioClampsAtFloor(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	win := syntheticWindow([]float64{0.85, 0.85, 0.85, 0.85, 0.85, 0.85, 0.85, 0.85}, 5, now)
	est := EstimateWindow(win, now)
	factor := 1.0
	want := []float64{0.9802, 0.9608, 0.9418, 0.9232, 0.9200, 0.9200}
	for i, w := range want {
		pub := Publish(est, factor, nil, now)
		if pub.Status != "active" {
			t.Fatalf("step %d: status = %s, want active", i, pub.Status)
		}
		if math.Abs(pub.Factor-w) > 0.0015 {
			t.Errorf("step %d: factor = %.4f, want ≈%.4f", i, pub.Factor, w)
		}
		factor = pub.Factor
	}
	if factor != ClampLo {
		t.Errorf("final factor = %.4f, want floor %.2f", factor, ClampLo)
	}
}

func TestPublish_HighRatioClampsAtCeiling(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	win := syntheticWindow([]float64{1.03, 1.03, 1.03, 1.03, 1.03, 1.03, 1.03, 1.03}, 5, now)
	est := EstimateWindow(win, now)
	pub := Publish(est, 1.0, nil, now)
	if pub.Status != "active" || pub.Factor != 1.0 {
		t.Errorf("got %+v, want {1.0 active}", pub)
	}
}

func TestPublish_DeadBand(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	win := syntheticWindow([]float64{0.994, 0.994, 0.994, 0.994, 0.994, 0.994, 0.994, 0.994}, 5, now)
	est := EstimateWindow(win, now)
	pub := Publish(est, 1.0, nil, now)
	if pub.Status != "active" || pub.Factor != 1.0 {
		t.Errorf("got %+v, want {1.0 active} (within dead band)", pub)
	}
}

func TestPublish_Hysteresis_WithinStepNoClamp(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	est := Estimate{LogMu: math.Log(0.9760), Sigma: 0.015, NEff: 8, N: 8}
	pub := Publish(est, 0.9781, nil, now)
	if pub.Status != "active" || math.Abs(pub.Factor-0.976) > 0.0005 {
		t.Errorf("got %+v, want ≈{0.976 active}", pub)
	}
}

func TestPublish_Hysteresis_StepClamped(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	est := Estimate{LogMu: math.Log(0.90), Sigma: 0.015, NEff: 8, N: 8}
	pub := Publish(est, 0.9781, nil, now)
	if pub.Status != "active" || math.Abs(pub.Factor-0.9587) > 0.0005 {
		t.Errorf("got %+v, want ≈{0.9587 active} (2%% step clamp engaged)", pub)
	}
}

func TestPublish_StaleAfter120DayGap(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	old := now.Add(-200 * 24 * time.Hour)
	win := syntheticWindow([]float64{0.97, 0.97, 0.97, 0.97, 0.97, 0.97, 0.97, 0.97}, 5, old)
	// 全部落在 200 天前，超出 120 天視窗 -> EstimateWindow 過濾後應為空。
	est := EstimateWindow(win, now)
	if est.N != 0 {
		t.Fatalf("expected empty window after 120d cutoff, got N=%d", est.N)
	}
	pub := Publish(est, 0.95, &old, now)
	if pub.Status != "stale" || pub.Factor != 1.0 {
		t.Errorf("got %+v, want {1.0 stale}", pub)
	}
}

func TestPublish_NeverHadPairs_Warming(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	est := EstimateWindow(nil, now)
	pub := Publish(est, 1.0, nil, now) // lastPairAt=nil：從未有過配對
	if pub.Status != "warming" {
		t.Errorf("status = %s, want warming (never had any pair)", pub.Status)
	}
}

// --- Gate 閘門個別條件 ---

func TestGate_CrossAccountDuplicateFlagged(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	p := Pair{
		GpsRunID: "g1", ExtActivityID: "e1", ExtSource: "strava",
		RawGpsKm: 5, ExtKm: 5, GpsDurS: 1800, ExtDurS: 1800,
		GpsStart: now, ExtStart: now, ExtFlagReason: "cross_account_duplicate",
	}
	g := Gate([]Pair{p}, "strava")
	if g[0].Accepted || g[0].Reason != "flagged" {
		t.Errorf("got accepted=%v reason=%s, want rejected:flagged", g[0].Accepted, g[0].Reason)
	}
}

func TestGate_OtherSourceExcluded(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	p := Pair{
		GpsRunID: "g1", ExtActivityID: "e1", ExtSource: "garmin",
		RawGpsKm: 5, ExtKm: 5, GpsDurS: 1800, ExtDurS: 1800,
		GpsStart: now, ExtStart: now,
	}
	g := Gate([]Pair{p}, "strava")
	if g[0].Accepted || g[0].Reason != "other_source" {
		t.Errorf("got accepted=%v reason=%s, want rejected:other_source", g[0].Accepted, g[0].Reason)
	}
	// 但仍應計算 LogRatio/DistW 供顯示（gps_calib_pairs 為 NOT NULL 欄位）。
	if g[0].LogRatio != 0 {
		t.Errorf("LogRatio = %f, want 0 (ratio 1.0)", g[0].LogRatio)
	}
}

func TestGate_RangeViolation(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	for _, ratio := range []float64{0.79, 1.26} {
		p := Pair{
			GpsRunID: "g1", ExtActivityID: "e1", ExtSource: "strava",
			RawGpsKm: 5, ExtKm: 5 * ratio, GpsDurS: 1800, ExtDurS: 1800, // 時長比維持 1:1，單獨違反距離比值閘門
			GpsStart: now, ExtStart: now,
		}
		g := Gate([]Pair{p}, "strava")
		if g[0].Accepted || g[0].Reason != "range" {
			t.Errorf("ratio %.2f: got accepted=%v reason=%s, want rejected:range", ratio, g[0].Accepted, g[0].Reason)
		}
	}
}

// TestGate_Bidirectional1to1 驗證雙向 1:1：一個外部活動被兩趟不同 GPS 候選匹配（反向於 fixture
// 裡「一趟 GPS 匹配兩個外部活動」的 idx14/15 案例），也只能留下距離最近的一組。
func TestGate_Bidirectional1to1(t *testing.T) {
	now := time.Date(2026, 8, 1, 22, 0, 0, 0, time.UTC)
	nearPair := Pair{ // 起訖幾乎完全對齊
		GpsRunID: "gpsA", ExtActivityID: "extShared", ExtSource: "strava",
		RawGpsKm: 5, ExtKm: 5, GpsDurS: 1800, ExtDurS: 1800,
		GpsStart: now, ExtStart: now,
	}
	farPair := Pair{ // 同一個外部活動，但另一趟 GPS 起訖差很多
		GpsRunID: "gpsB", ExtActivityID: "extShared", ExtSource: "strava",
		RawGpsKm: 5, ExtKm: 5, GpsDurS: 1800, ExtDurS: 1800,
		GpsStart: now.Add(20 * time.Minute), ExtStart: now,
	}
	g := Gate([]Pair{nearPair, farPair}, "strava")
	if !g[0].Accepted {
		t.Errorf("closer pair should be accepted, got reason=%s", g[0].Reason)
	}
	if g[1].Accepted || g[1].Reason != "ambiguous" {
		t.Errorf("farther pair sharing the same ext activity should be rejected:ambiguous, got accepted=%v reason=%s", g[1].Accepted, g[1].Reason)
	}
}

// TestGate_MultiSourceDoesNotStealAmbiguous 對抗式審查修正回歸測試（low-1 finding）：使用者同時
// 連了 Strava（自動同步）與 COROS（直連）——同一趟 GPS 有兩個外部候選，refSource="strava"。
// COROS 候選的起訖 gap 比 Strava 候選小（時間戳更接近），修正前的 1:1 最佳比對把全部來源混在一起
// 比較，會讓 COROS 候選「贏走」best-match 資格，導致 Strava 候選被誤標 ambiguous（而不是正確的
// other_source）、COROS 候選也因為不是 refSource 被拒絕——整趟 0 筆 accepted。修正後 Strava 候選
// 應該正常被 accepted，COROS 候選標 other_source（不是 ambiguous）。
func TestGate_MultiSourceDoesNotStealAmbiguous(t *testing.T) {
	now := time.Date(2026, 8, 1, 22, 0, 0, 0, time.UTC)
	// edge 容差 = max(20s, 1.5%×1800s) = 27s——兩筆的 gap 都落在容差內，差別只在誰比較接近。
	stravaPair := Pair{ // 起訖差 25 秒（在容差內，但比 coros 差）
		GpsRunID: "g1", ExtActivityID: "ext-strava", ExtSource: "strava",
		RawGpsKm: 6, ExtKm: 6, GpsDurS: 1800, ExtDurS: 1800,
		GpsStart: now, ExtStart: now.Add(-25 * time.Second),
	}
	corosPair := Pair{ // 同一趟 GPS，但外部來源是 coros，起訖幾乎完全對齊（gap 比 strava 小很多）
		GpsRunID: "g1", ExtActivityID: "ext-coros", ExtSource: "coros",
		RawGpsKm: 6, ExtKm: 6, GpsDurS: 1800, ExtDurS: 1800,
		GpsStart: now, ExtStart: now.Add(-2 * time.Second),
	}
	g := Gate([]Pair{stravaPair, corosPair}, "strava")
	if !g[0].Accepted {
		t.Errorf("strava candidate (the ref source) should be accepted, got reason=%s", g[0].Reason)
	}
	if g[1].Accepted || g[1].Reason != "other_source" {
		t.Errorf("coros candidate should be rejected:other_source (not ambiguous), got accepted=%v reason=%s", g[1].Accepted, g[1].Reason)
	}
}

// TestPickRefSource_PreferredOverridesEvenWithoutCandidates 對抗式審查修正回歸測試（medium-1
// finding 的重點修正）：user_profiles.preferred_data_source 一旦設為合法值，就算這次候選為空也
// 立刻採用——這是「使用者切換手錶後，即使新配對還沒進來，下次 Recompute 也該立刻改用新來源」
// 這個修正的核心保證（真正的「不再永久卡死在第一次挑選結果」由 service.go 的 Recompute 每次都
// 呼叫 pickRefSource 而非只在 refSource=="" 時呼叫來保證，這裡驗證 pickRefSource 本身的行為）。
func TestPickRefSource_PreferredOverridesEvenWithoutCandidates(t *testing.T) {
	if got := pickRefSource("garmin", nil); got != "garmin" {
		t.Errorf("pickRefSource(garmin, nil) = %q, want garmin", got)
	}
	if got := pickRefSource("garmin", []Pair{{ExtSource: "strava"}}); got != "garmin" {
		t.Errorf("preferred source should win even when candidates are all a different source, got %q", got)
	}
}

// TestPickRefSource_PicksSourceWithMostAccepted 對抗式審查修正回歸測試（medium-1 finding 附帶的
// 次要修正）：沒有 preferred_data_source 時，改用「實際會被 accepted 的筆數」挑選，而不是舊版本
// 的「候選出現次數」——這裡 strava 出現 2 次但兩筆都會被 partial 閘門擋下（0 筆 accepted），
// garmin 只出現 1 次但會被 accepted，應該選 garmin。
func TestPickRefSource_PicksSourceWithMostAccepted(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	pairs := []Pair{
		{GpsRunID: "g1", ExtActivityID: "e1", ExtSource: "strava", RawGpsKm: 5, ExtKm: 8, GpsDurS: 1800, ExtDurS: 2880, GpsStart: now, ExtStart: now}, // partial：時長比超過 5%
		{GpsRunID: "g2", ExtActivityID: "e2", ExtSource: "strava", RawGpsKm: 5, ExtKm: 8, GpsDurS: 1800, ExtDurS: 2880, GpsStart: now, ExtStart: now}, // partial
		{GpsRunID: "g3", ExtActivityID: "e3", ExtSource: "garmin", RawGpsKm: 5, ExtKm: 5, GpsDurS: 1800, ExtDurS: 1800, GpsStart: now, ExtStart: now}, // 乾淨配對，會 accepted
	}
	if got := pickRefSource("", pairs); got != "garmin" {
		t.Errorf("pickRefSource(\"\", pairs) = %q, want garmin (only source with any accepted pair)", got)
	}
}

// TestPickRefSource_FallsBackToMostCandidatesWhenNoneAccepted 對抗式審查修正回歸測試（low-1
// finding）：三個來源都 0 筆 accepted（例如全部距離太短被 short 閘門擋下）時，pickRefSource 不該
// 回空字串——refSource=="" 會讓 Gate() 的 G2 把每一筆候選都誤標成 other_source，蓋掉真正的拒絕
// 原因（這裡應該是 short）。改用「候選筆數最多」的來源（strava 3 筆 > coros 1 筆）。
func TestPickRefSource_FallsBackToMostCandidatesWhenNoneAccepted(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	pairs := []Pair{
		{GpsRunID: "g1", ExtActivityID: "e1", ExtSource: "strava", RawGpsKm: 1, ExtKm: 1, GpsDurS: 600, ExtDurS: 600, GpsStart: now, ExtStart: now},
		{GpsRunID: "g2", ExtActivityID: "e2", ExtSource: "strava", RawGpsKm: 1, ExtKm: 1, GpsDurS: 600, ExtDurS: 600, GpsStart: now, ExtStart: now},
		{GpsRunID: "g3", ExtActivityID: "e3", ExtSource: "strava", RawGpsKm: 1, ExtKm: 1, GpsDurS: 600, ExtDurS: 600, GpsStart: now, ExtStart: now},
		{GpsRunID: "g4", ExtActivityID: "e4", ExtSource: "coros", RawGpsKm: 1, ExtKm: 1, GpsDurS: 600, ExtDurS: 600, GpsStart: now, ExtStart: now},
	}
	got := pickRefSource("", pairs)
	if got != "strava" {
		t.Errorf("pickRefSource(\"\", pairs) = %q, want strava (most candidates, even though none accepted)", got)
	}
	// 驗證修正真正解決的症狀：refSource 非空時，短程配對被標為 short 而非 other_source。
	g := Gate(pairs, got)
	for i, gg := range g {
		if gg.ExtSource != got {
			continue
		}
		if gg.Reason != "short" {
			t.Errorf("pair %d: reason = %q, want short (not masked as other_source)", i, gg.Reason)
		}
	}
	// 全部候選都是 0 筆才回空字串。
	if got := pickRefSource("", nil); got != "" {
		t.Errorf("pickRefSource(\"\", nil) = %q, want empty string", got)
	}
}

// TestWindowFingerprint_DeterministicAndOrderIndependent medium-2 finding 修正回歸測試：同一組
// accepted 配對（不論陣列順序）指紋必須相同；配對集合不同則指紋必須不同——service.go 的 Recompute
// 拿它判斷「這批視窗跟上次 publish 時是否完全相同」，若對排序敏感或對集合變化不敏感就會失效。
func TestWindowFingerprint_DeterministicAndOrderIndependent(t *testing.T) {
	a := Gated{Pair: Pair{GpsRunID: "g1", ExtActivityID: "e1"}}
	b := Gated{Pair: Pair{GpsRunID: "g2", ExtActivityID: "e2"}}
	c := Gated{Pair: Pair{GpsRunID: "g3", ExtActivityID: "e3"}}

	f1 := WindowFingerprint([]Gated{a, b})
	f2 := WindowFingerprint([]Gated{b, a})
	if f1 == "" || f1 != f2 {
		t.Errorf("fingerprint should be order-independent: %q vs %q", f1, f2)
	}
	f3 := WindowFingerprint([]Gated{a, c})
	if f3 == f1 {
		t.Error("different pair sets should produce different fingerprints")
	}
	if WindowFingerprint(nil) != "" {
		t.Error("empty window should fingerprint to empty string")
	}
}

// TestGate_PureNoHiddenState：Gate 是 SQL 候選查詢之後的純函式，不該有任何跨呼叫的隱藏狀態
// ——同一組輸入呼叫兩次必須得到完全相同的結果；且如果呼叫端不慎把「已校正過」的距離當作
// RawGpsKm 重新餵入（本該傳 gps_runs.distance_km 原始值），LogRatio 必須確實改變（證明函式
// 沒有內部再乘回係數把這個上游錯誤悄悄抵銷掉）。
func TestGate_PureNoHiddenState(t *testing.T) {
	pairs := loadFixturePairs(t)
	g1 := Gate(pairs, "strava")
	g2 := Gate(pairs, "strava")
	for i := range g1 {
		if g1[i].Accepted != g2[i].Accepted || g1[i].Reason != g2[i].Reason || g1[i].LogRatio != g2[i].LogRatio {
			t.Fatalf("Gate not deterministic at idx %d", i)
		}
	}

	const k = 0.9781
	miscalibrated := make([]Pair, len(pairs))
	for i, p := range pairs {
		p.RawGpsKm = p.RawGpsKm * k
		miscalibrated[i] = p
	}
	g3 := Gate(miscalibrated, "strava")
	diff := 0
	for i := range g1 {
		if g1[i].LogRatio != g3[i].LogRatio {
			diff++
		}
	}
	if diff == 0 {
		t.Fatal("expected feeding already-calibrated distances to change LogRatio (no hidden re-correction)")
	}
}
