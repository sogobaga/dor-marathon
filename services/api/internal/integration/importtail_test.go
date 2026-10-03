package integration

// 匯入尾巴（importtail.go，計畫 S6）：
//   - planTail 與 terra.go importTerra 的行內條件逐條對拍（所有 Status×Reason 組合）；
//   - SP 舊活動跳過、被直連取代的 Strava 那趟不重複扣血；
//   - 競賽分組重算 callback：注入、debounce、nil 安全。

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// terraTail 照 terra.go:1309-1324 的行內條件寫成的對拍基準（Terra 沒有 SP 年齡上限、沒有競賽分組重算）。
func terraTail(na *NormalizedActivity, res ImportResult) (gpscalib, chargeSP, award bool) {
	gpscalib = res.Status == "inserted" || res.Status == "duplicate"
	chargeSP = res.Status == "inserted" && na.DistanceKm > 0
	award = (res.Status == "inserted" || (res.Status == "duplicate" && res.Reason == "multi_device_duplicate")) && na.DistanceKm > 0
	return
}

func TestPlanTail_MatchesTerraInlineTail(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	statuses := []string{"inserted", "exists", "duplicate", "skipped", "error", ""}
	reasons := []string{"", "multi_device_duplicate", "duplicate", "cross_account_duplicate", "cross_source_duplicate", "implausible_pace", "implausible_distance"}
	for _, st := range statuses {
		for _, rs := range reasons {
			for _, km := range []float64{0, 5.2} {
				na := &NormalizedActivity{UserID: "u1", Source: "garmin", ExternalID: "gc:1", DistanceKm: km, DurationS: 1800, AvgPaceS: 340,
					RecordedAt: now.Add(-time.Hour)}
				res := ImportResult{Status: st, Reason: rs, ID: "act-1"}
				got := planTail(na, res, TailOptions{}, now) // MaxSPAge=0（不限）＝Terra 行為；SkipGPSCalib=false
				wantCalib, wantSP, wantAward := terraTail(na, res)
				if got.GPSCalib != wantCalib || got.ChargeSP != wantSP || got.Award != wantAward {
					t.Errorf("status=%q reason=%q km=%v: got %+v, terra-inline calib=%v sp=%v award=%v", st, rs, km, got, wantCalib, wantSP, wantAward)
				}
				if got.Standings != (st == "inserted") {
					t.Errorf("status=%q: standings recompute only for inserted, got %v", st, got.Standings)
				}
			}
		}
	}
}

func TestPlanTail_SkipGPSCalibAndMissingID(t *testing.T) {
	now := time.Now()
	na := &NormalizedActivity{UserID: "u1", DistanceKm: 5, DurationS: 1500, RecordedAt: now.Add(-time.Hour)}
	if p := planTail(na, ImportResult{Status: "inserted", ID: "a"}, TailOptions{SkipGPSCalib: true}, now); p.GPSCalib {
		t.Fatal("SkipGPSCalib must suppress the recompute")
	}
	// 沒有 ID（理論上不會發生）：不呼叫 AwardMileageExp
	if p := planTail(na, ImportResult{Status: "inserted"}, TailOptions{}, now); p.Award {
		t.Fatal("no activity id → nothing to award")
	}
	if p := planTail(nil, ImportResult{Status: "inserted", ID: "a"}, TailOptions{}, now); p != (tailActions{}) {
		t.Fatalf("nil activity → no actions, got %+v", p)
	}
}

// SP 舊活動跳過（Garmin：MaxSPAge=3h；COROS：24h）：以「結束時間」（開始＋時長）距今計。
func TestPlanTail_SPSkippedForOldActivities(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	mk := func(start time.Time, dur int) *NormalizedActivity {
		return &NormalizedActivity{UserID: "u1", DistanceKm: 5, DurationS: dur, AvgPaceS: 300, RecordedAt: start}
	}
	res := ImportResult{Status: "inserted", ID: "a"}
	garmin := TailOptions{SkipGPSCalib: true, MaxSPAge: 3 * time.Hour}
	cases := []struct {
		name string
		na   *NormalizedActivity
		opt  TailOptions
		want bool
	}{
		{"ended 10 minutes ago", mk(now.Add(-40*time.Minute), 1800), garmin, true},
		{"ended exactly 3h ago", mk(now.Add(-3*time.Hour-30*time.Minute), 1800), garmin, true},
		{"ended just over 3h ago", mk(now.Add(-3*time.Hour-31*time.Minute), 1800), garmin, false},
		{"started long ago but ended recently (long run)", mk(now.Add(-4*time.Hour), 3*3600+1800), garmin, true}, // 結束於 30 分鐘前
		{"yesterday's run", mk(now.Add(-26*time.Hour), 3000), garmin, false},
		{"coros 24h: 20h ago is fresh", mk(now.Add(-20*time.Hour), 3000), TailOptions{MaxSPAge: corosMcpSPMaxAge}, true},
		{"coros 24h: 30h ago is stale", mk(now.Add(-30*time.Hour), 3000), TailOptions{MaxSPAge: corosMcpSPMaxAge}, false},
		{"unlimited (Terra/Strava behaviour): 30 days ago still charges", mk(now.Add(-30*24*time.Hour), 3000), TailOptions{}, true},
	}
	for _, c := range cases {
		if got := planTail(c.na, res, c.opt, now).ChargeSP; got != c.want {
			t.Errorf("%s: ChargeSP=%v want %v", c.name, got, c.want)
		}
	}
}

// 被直連列取代掉 Strava 的那趟：SP 在 Strava 匯入時已經扣過，不能再扣一次。
func TestPlanTail_NoDoubleSPWhenSupersedingStrava(t *testing.T) {
	now := time.Now()
	na := &NormalizedActivity{UserID: "u1", DistanceKm: 5, DurationS: 1500, RecordedAt: now.Add(-time.Hour)}
	if !planTail(na, ImportResult{Status: "inserted", ID: "a"}, TailOptions{}, now).ChargeSP {
		t.Fatal("a plain insert charges SP")
	}
	p := planTail(na, ImportResult{Status: "inserted", ID: "a", Superseded: 1}, TailOptions{}, now)
	if p.ChargeSP {
		t.Fatal("superseding a Strava row must not charge SP a second time")
	}
	if !p.Award || !p.Standings {
		t.Fatalf("EXP delta compensation and standings still apply, got %+v", p)
	}
}

func TestSpFresh(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	na := &NormalizedActivity{RecordedAt: now.Add(-2 * time.Hour), DurationS: 3600} // 結束於 1 小時前
	if !spFresh(na, 0, now) || !spFresh(na, time.Hour, now) || spFresh(na, 59*time.Minute, now) {
		t.Fatal("spFresh boundaries wrong")
	}
}

// --- 競賽分組成績重算 callback ---

func TestSetCompetitionRecompute_DebouncedPerUser(t *testing.T) {
	old := standingsDebounce
	standingsDebounce = 30 * time.Millisecond
	t.Cleanup(func() { standingsDebounce = old; SetCompetitionRecompute(nil) })

	var mu sync.Mutex
	calls := map[string]int{}
	SetCompetitionRecompute(func(ctx context.Context, userID string) {
		mu.Lock()
		calls[userID]++
		mu.Unlock()
	})
	// 同一位使用者連續匯入 5 筆：debounce 合併成 1 次；另一位獨立 1 次。
	for i := 0; i < 5; i++ {
		recomputeStandingsAsync("u1")
	}
	recomputeStandingsAsync("u2")
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls["u1"] != 1 || calls["u2"] != 1 {
		t.Fatalf("want one coalesced recompute per user, got %v", calls)
	}
}

func TestSetCompetitionRecompute_NilAndEmptyAreNoOps(t *testing.T) {
	SetCompetitionRecompute(nil)
	recomputeStandingsAsync("u1") // 沒注入：不 panic、不動作
	var n atomic.Int32
	SetCompetitionRecompute(func(ctx context.Context, userID string) { n.Add(1) })
	t.Cleanup(func() { SetCompetitionRecompute(nil) })
	recomputeStandingsAsync("") // 空 userID 不觸發
	time.Sleep(50 * time.Millisecond)
	if n.Load() != 0 {
		t.Fatal("empty user id must not trigger a recompute")
	}
}

func TestSetCompetitionRecompute_PanicIsContained(t *testing.T) {
	old := standingsDebounce
	standingsDebounce = 10 * time.Millisecond
	t.Cleanup(func() { standingsDebounce = old; SetCompetitionRecompute(nil) })
	done := make(chan struct{})
	SetCompetitionRecompute(func(ctx context.Context, userID string) {
		defer close(done)
		panic(fmt.Sprintf("boom for %s", userID))
	})
	recomputeStandingsAsync("u1")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("callback never ran")
	}
	time.Sleep(30 * time.Millisecond) // 若 panic 沒被攔下，測試行程已經崩潰
}
