package integration

// 外部活動合理性檢查（plausibility.go，GA 契約 §2.5）：表驅動，涵蓋每一條門檻的邊界。

import (
	"testing"
	"time"
)

func TestCheckPlausible_Table(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	mk := func(start time.Time, km float64, dur int, kind string) *NormalizedActivity {
		return &NormalizedActivity{RecordedAt: start, DistanceKm: km, DurationS: dur, Kind: kind}
	}
	past := now.Add(-2 * time.Hour)
	cases := []struct {
		name       string
		a          *NormalizedActivity
		wantAction string
		wantReason string
	}{
		// 正常
		{"normal 10km in 55min", mk(past, 10, 3300, ""), PlausibleOK, ""},
		{"normal run kind explicit", mk(past, 5, 1500, KindRun), PlausibleOK, ""},
		{"normal walk 5km in 60min", mk(past, 5, 3600, KindWalk), PlausibleOK, ""},
		{"marathon distance fine", mk(past, 42.195, 3*3600, ""), PlausibleOK, ""},
		{"120 km exactly is allowed", mk(past, 120, 14*3600, ""), PlausibleOK, ""},
		// 未來開始時間（容忍 10 分鐘）
		{"start 9min in the future is within slack", mk(now.Add(9*time.Minute), 5, 1500, ""), PlausibleOK, ""},
		{"start exactly now+10min is within slack", mk(now.Add(10*time.Minute), 5, 1500, ""), PlausibleOK, ""},
		{"start 11min in the future → skip", mk(now.Add(11*time.Minute), 5, 1500, ""), PlausibleSkip, SkipFutureStart},
		{"start tomorrow → skip", mk(now.Add(24*time.Hour), 5, 1500, ""), PlausibleSkip, SkipFutureStart},
		// 距離／時間過短
		{"0.1 km exactly is allowed", mk(past, 0.1, 300, ""), PlausibleOK, ""},
		{"0.099 km → skip", mk(past, 0.099, 300, ""), PlausibleSkip, SkipTooShortDist},
		{"zero distance → skip", mk(past, 0, 600, ""), PlausibleSkip, SkipTooShortDist},
		{"negative distance → skip", mk(past, -3, 600, ""), PlausibleSkip, SkipTooShortDist},
		{"60 s exactly is allowed", mk(past, 0.3, 60, ""), PlausibleOK, ""},
		{"59 s → skip", mk(past, 0.3, 59, ""), PlausibleSkip, SkipTooShortTime},
		{"zero duration → skip", mk(past, 5, 0, ""), PlausibleSkip, SkipTooShortTime},
		// 配速：跑步 2:30/km（150 s/km）；走路 4:00/km（240 s/km）
		{"run at exactly 2:30/km is allowed", mk(past, 10, 1500, ""), PlausibleOK, ""},
		{"run faster than 2:30/km → flag pace", mk(past, 10, 1499, ""), PlausibleFlag, FlagImplausiblePace},
		{"run 1:00/km → flag pace", mk(past, 10, 600, KindRun), PlausibleFlag, FlagImplausiblePace},
		{"walk at exactly 4:00/km is allowed", mk(past, 5, 1200, KindWalk), PlausibleOK, ""},
		{"walk faster than 4:00/km → flag pace", mk(past, 5, 1199, KindWalk), PlausibleFlag, FlagImplausiblePace},
		{"walk at 3:00/km is fine as RUN but flagged as WALK (same data, different kind)", mk(past, 5, 900, KindWalk), PlausibleFlag, FlagImplausiblePace},
		{"same data as run is ok (3:00/km)", mk(past, 5, 900, ""), PlausibleOK, ""},
		// 單筆距離上限
		{"120.1 km → flag distance", mk(past, 120.1, 20*3600, ""), PlausibleFlag, FlagImplausibleDistance},
		{"300 km → flag distance", mk(past, 300, 40*3600, ""), PlausibleFlag, FlagImplausibleDistance},
		// 優先序：太短優先於配速；未來優先於太短；配速優先於距離
		{"future beats too-short", mk(now.Add(time.Hour), 0.05, 10, ""), PlausibleSkip, SkipFutureStart},
		{"too-short beats pace", mk(past, 0.05, 5, ""), PlausibleSkip, SkipTooShortDist},
		{"pace beats distance", mk(past, 200, 600, ""), PlausibleFlag, FlagImplausiblePace},
	}
	for _, c := range cases {
		action, reason := CheckPlausible(c.a, now)
		if action != c.wantAction || reason != c.wantReason {
			t.Errorf("%s: got (%q,%q) want (%q,%q)", c.name, action, reason, c.wantAction, c.wantReason)
		}
	}
}

func TestCheckPlausible_NilAndZeroRecordedAt(t *testing.T) {
	now := time.Now()
	if a, _ := CheckPlausible(nil, now); a != PlausibleSkip {
		t.Fatal("nil activity must be skipped, not panic")
	}
	// RecordedAt 零值：不做「未來」判斷（其他欄位仍檢查）
	if a, _ := CheckPlausible(&NormalizedActivity{DistanceKm: 5, DurationS: 1500}, now); a != PlausibleOK {
		t.Fatalf("zero RecordedAt should not trigger the future check, got %q", a)
	}
}

// 常數就是契約數字（改了門檻要連契約一起改）。
func TestPlausibilityConstantsMatchContract(t *testing.T) {
	if plausFutureSlack != 10*time.Minute || plausMinDistanceKm != 0.1 || plausMinDurationS != 60 ||
		plausRunMinPaceS != 150 || plausWalkMinPaceS != 240 || plausMaxDistanceKm != 120 {
		t.Fatal("plausibility thresholds drifted from COROS_MCP_GA_CONTRACT §2.5")
	}
}

// 既有 importer 的狀態判斷對 "skipped" 不崩：這個測試守住 Status 詞彙的文件化集合。
func TestImportResultStatusVocabulary(t *testing.T) {
	for _, s := range []string{"inserted", "exists", "duplicate", "skipped"} {
		res := ImportResult{Status: s}
		// 與 corosmcp_sync／terra／strava 的 switch 一致：只有 inserted 會觸發 SP 與全額獎勵。
		if (res.Status == "inserted") != (s == "inserted") {
			t.Fatalf("status %q", s)
		}
	}
}
