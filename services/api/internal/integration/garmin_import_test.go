package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func baseStored() garminStoredActivity {
	g := 3600.0
	_ = g
	return garminStoredActivity{
		SummaryID: "s-100", ActivityID: "a-100", ActivityType: "RUNNING", StartTime: 1790000000,
		Duration: 1800, Distance: 5000, DeviceName: "Forerunner 265",
	}
}

func fptr(v float64) *float64 { return &v }

func TestMapGarminActivity_Table(t *testing.T) {
	floor := time.Unix(1789999000, 0)
	cases := map[string]struct {
		mut  func(a *garminStoredActivity)
		skip string
	}{
		"ok":                              {func(a *garminStoredActivity) {}, ""},
		"manual":                          {func(a *garminStoredActivity) { a.Manual = true }, garminSkipManual},
		"web upload":                      {func(a *garminStoredActivity) { a.IsWebUpload = true }, garminSkipManual},
		"parent activity":                 {func(a *garminStoredActivity) { a.IsParent = true }, garminSkipParent},
		"child activity ok":               {func(a *garminStoredActivity) { a.ParentSummaryID = "p-1" }, ""},
		"cycling":                         {func(a *garminStoredActivity) { a.ActivityType = "CYCLING" }, garminSkipNonRunning},
		"wheelchair":                      {func(a *garminStoredActivity) { a.ActivityType = "WHEELCHAIR_PUSH_RUN" }, garminSkipNonRunning},
		"multi sport":                     {func(a *garminStoredActivity) { a.ActivityType = "MULTI_SPORT" }, garminSkipNonRunning},
		"no id at all":                    {func(a *garminStoredActivity) { a.SummaryID, a.ActivityID = "", "" }, garminSkipBadPayload},
		"activityId only":                 {func(a *garminStoredActivity) { a.SummaryID = "" }, ""},
		"no start":                        {func(a *garminStoredActivity) { a.StartTime = 0 }, garminSkipBadPayload},
		"before connect":                  {func(a *garminStoredActivity) { a.StartTime = floor.Unix() - 1 }, garminSkipBefore},
		"exactly at floor":                {func(a *garminStoredActivity) { a.StartTime = floor.Unix() }, ""},
		"duration 59":                     {func(a *garminStoredActivity) { a.Duration = 59 }, garminSkipInvalid},
		"duration 60":                     {func(a *garminStoredActivity) { a.Duration = 60; a.Distance = 200 }, ""},
		"duration absurd":                 {func(a *garminStoredActivity) { a.Duration = 2e7 }, garminSkipInvalid},
		"distance 99m":                    {func(a *garminStoredActivity) { a.Distance = 99 }, garminSkipInvalid},
		"distance 100m":                   {func(a *garminStoredActivity) { a.Distance = 100 }, ""},
		"distance zero":                   {func(a *garminStoredActivity) { a.Distance = 0 }, garminSkipInvalid},
		"999.9 km":                        {func(a *garminStoredActivity) { a.Distance = 999900 }, ""},
		"1000 km (DECIMAL(6,3) overflow)": {func(a *garminStoredActivity) { a.Distance = 1_000_000 }, garminSkipInvalid},
		"1e9 m":                           {func(a *garminStoredActivity) { a.Distance = 1e9 }, garminSkipInvalid},
		"walking":                         {func(a *garminStoredActivity) { a.ActivityType = "WALKING" }, ""},
		"hiking":                          {func(a *garminStoredActivity) { a.ActivityType = "HIKING" }, ""},
		"treadmill":                       {func(a *garminStoredActivity) { a.ActivityType = "TREADMILL_RUNNING" }, ""},
		"virtual run":                     {func(a *garminStoredActivity) { a.ActivityType = "VIRTUAL_RUN" }, ""},
		"capitalised":                     {func(a *garminStoredActivity) { a.ActivityType = "Running" }, ""},
	}
	for name, c := range cases {
		a := baseStored()
		c.mut(&a)
		na, skip := mapGarminActivity("user-1", floor, a)
		if skip != c.skip {
			t.Errorf("%s: skip=%q, want %q", name, skip, c.skip)
			continue
		}
		if (na == nil) != (c.skip != "") {
			t.Errorf("%s: activity nil=%v with skip=%q", name, na == nil, skip)
		}
	}
	// 連接時間未知（零值 floor）：不做 floor 檢查
	if na, skip := mapGarminActivity("u", time.Time{}, baseStored()); na == nil || skip != "" {
		t.Errorf("zero floor: %v %q", na, skip)
	}
}

func TestMapGarminActivity_Fields(t *testing.T) {
	a := baseStored()
	a.Distance, a.Duration = 10_000, 3000
	a.ElevationGain, a.AvgHeartRate = fptr(123.4), fptr(151.6)
	na, _ := mapGarminActivity("user-1", time.Time{}, a)
	if na == nil {
		t.Fatal("nil")
	}
	if na.UserID != "user-1" || na.Source != "garmin" || na.ExternalID != "gc:s-100" {
		t.Errorf("identity: %+v", na)
	}
	if na.DistanceKm != 10 || na.DurationS != 3000 || na.AvgPaceS != 300 {
		t.Errorf("numbers: km=%v dur=%d pace=%d", na.DistanceKm, na.DurationS, na.AvgPaceS)
	}
	if na.AscentM == nil || *na.AscentM != 123.4 || na.AvgHR == nil || *na.AvgHR != 152 {
		t.Errorf("ascent/hr: %v %v", na.AscentM, na.AvgHR)
	}
	if na.DeviceName == nil || *na.DeviceName != "Forerunner 265" || na.ElapsedS != nil || na.Manual || na.Kind != KindRun {
		t.Errorf("device/elapsed/manual/kind: %v %v %v %q", na.DeviceName, na.ElapsedS, na.Manual, na.Kind)
	}
	if na.Fingerprint != fingerprintOf(1790000000, 10_000, 3000) || na.Fingerprint == "" {
		t.Errorf("fingerprint")
	}
	// recorded_at＝開始時間（開始與結束相差 1 小時的案例）
	a2 := baseStored()
	a2.Duration, a2.Distance = 3600, 12000
	na2, _ := mapGarminActivity("u", time.Time{}, a2)
	if !na2.RecordedAt.Equal(time.Unix(1790000000, 0).UTC()) {
		t.Errorf("RecordedAt must be the START time, got %s (end would be %s)", na2.RecordedAt, time.Unix(1790003600, 0).UTC())
	}
	// 走路類 → Kind=walk
	a3 := baseStored()
	a3.ActivityType = "HIKING"
	if n3, _ := mapGarminActivity("u", time.Time{}, a3); n3.Kind != KindWalk {
		t.Errorf("hiking kind=%q", n3.Kind)
	}
	// 心率範圍／爬升／裝置名稱規則
	for name, tc := range map[string]struct {
		hr     *float64
		wantHR bool
	}{"29": {fptr(29), false}, "30": {fptr(30), true}, "250": {fptr(250), true}, "251": {fptr(251), false}, "nil": {nil, false}} {
		x := baseStored()
		x.AvgHeartRate = tc.hr
		n, _ := mapGarminActivity("u", time.Time{}, x)
		if (n.AvgHR != nil) != tc.wantHR {
			t.Errorf("hr %s: stored=%v want %v", name, n.AvgHR != nil, tc.wantHR)
		}
	}
	for name, g := range map[string]*float64{"zero": fptr(0), "negative": fptr(-5), "nil": nil} {
		x := baseStored()
		x.ElevationGain = g
		if n, _ := mapGarminActivity("u", time.Time{}, x); n.AscentM != nil {
			t.Errorf("ascent %s must be omitted", name)
		}
	}
	for name, d := range map[string]string{"empty": "", "unknown": "unknown", "UNKNOWN": "UNKNOWN", "spaces": "   "} {
		x := baseStored()
		x.DeviceName = d
		if n, _ := mapGarminActivity("u", time.Time{}, x); n.DeviceName != nil {
			t.Errorf("device %q must map to nil", name)
		}
	}
	long := baseStored()
	long.DeviceName = strings.Repeat("好", 100)
	if n, _ := mapGarminActivity("u", time.Time{}, long); n.DeviceName == nil || len([]rune(*n.DeviceName)) != garminDeviceMax {
		t.Errorf("device name must be truncated to %d runes", garminDeviceMax)
	}
}

func TestGarminExternalID(t *testing.T) {
	if got := garminExternalID("12345"); got != "gc:12345" {
		t.Fatalf("short id: %q", got)
	}
	exact := strings.Repeat("9", 61) // gc: + 61 = 64
	if got := garminExternalID(exact); got != "gc:"+exact || len(got) != 64 {
		t.Fatalf("64 chars exactly stays readable: %q", got)
	}
	long := strings.Repeat("9", 62)
	got := garminExternalID(long)
	if !strings.HasPrefix(got, "gc:h:") || len(got) > 64 || len(got) != len("gc:h:")+40 {
		t.Fatalf("long id must be hashed: %q (%d)", got, len(got))
	}
	if garminExternalID(long) != got || garminExternalID(long+"1") == got {
		t.Fatal("hash must be deterministic and id-specific")
	}
	if !strings.HasPrefix(got, garminExtIDPrefix) || !IsDirectWatch("garmin", got) {
		t.Fatal("hashed ids must still be recognised as direct watch rows")
	}
}

func storedEvent(id string, a garminStoredActivity) garminEvent {
	b, _ := json.Marshal(a)
	return garminEvent{ID: id, EventType: garminEvActivity, ProviderUserID: "", Payload: b, ReceivedAt: time.Now()}
}

func importEnv(t *testing.T) (*connectEnv, *garminConn) {
	t.Helper()
	e := newConnectEnv(t, nil)
	c := seedConn(t, e, tUserA, time.Hour, 80*24*time.Hour)
	return e, c
}

func (e *connectEnv) process(t *testing.T, c *garminConn, a garminStoredActivity) (string, error) {
	t.Helper()
	ev := storedEvent("ev-x", a)
	ev.ProviderUserID = c.ProviderUserID
	return e.h.processActivityEvent(context.Background(), ev)
}

func TestProcessActivity_UnknownUserDropped(t *testing.T) {
	e := newConnectEnv(t, nil)
	ev := storedEvent("x", baseStored())
	ev.ProviderUserID = "nobody"
	res, err := e.h.processActivityEvent(context.Background(), ev)
	if err != nil || res != "unknown_user" || e.store.importCount() != 0 {
		t.Fatalf("unknown user: %q %v imports=%d", res, err, e.store.importCount())
	}
}

func TestProcessActivity_InsertedRunsTailWithGarminOptions(t *testing.T) {
	e, c := importEnv(t)
	a := baseStored()
	a.StartTime = time.Now().Add(-2 * time.Hour).Unix()
	res, err := e.process(t, c, a)
	if err != nil || res != "inserted" {
		t.Fatalf("res=%q err=%v", res, err)
	}
	if len(e.store.cs.imports) != 1 {
		t.Fatalf("imports=%d", len(e.store.cs.imports))
	}
	na := e.store.cs.imports[0]
	if na.UserID != tUserA || na.ExternalID != "gc:s-100" || na.Source != "garmin" {
		t.Fatalf("imported: %+v", na)
	}
	if len(e.store.cs.tails) != 1 {
		t.Fatalf("tails=%d", len(e.store.cs.tails))
	}
	opt := e.store.cs.tails[0].Opt
	if !opt.SkipGPSCalib || opt.MaxSPAge != 3*time.Hour {
		t.Fatalf("tail options: %+v (want SkipGPSCalib + MaxSPAge 3h)", opt)
	}
	if len(e.store.cs.touched) != 1 || e.store.cs.touched[0] != c.ID {
		t.Fatalf("last_synced_at must be touched: %v", e.store.cs.touched)
	}
}

func TestProcessActivity_ImportStatuses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		res      ImportResult
		want     string
		wantTail bool
	}{
		{"exists", ImportResult{Status: "exists"}, "exists", false},
		{"duplicate (multi device)", ImportResult{Status: "duplicate", Reason: "multi_device_duplicate", ID: "a1"}, "duplicate", true},
		{"flagged implausible", ImportResult{Status: "duplicate", Reason: "implausible_pace", ID: "a2"}, "duplicate", true},
		{"skipped by plausibility", ImportResult{Status: "skipped", Reason: "future_start"}, "skipped_implausible", false},
		{"inserted", ImportResult{Status: "inserted", ID: "a3"}, "inserted", true},
	} {
		e, c := importEnv(t)
		e.store.cs.importFn = func(a *NormalizedActivity) (ImportResult, error) { return tc.res, nil }
		got, err := e.process(t, c, baseStored())
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q err=%v, want %q", tc.name, got, err, tc.want)
		}
		if tails := len(e.store.cs.tails); (tails == 1) != tc.wantTail {
			t.Errorf("%s: tail calls=%d, wantTail=%v", tc.name, tails, tc.wantTail)
		}
	}
	// ImportActivity 失敗 → 暫時性錯誤（由框架退避重試），不吞掉
	e, c := importEnv(t)
	e.store.cs.importFn = func(a *NormalizedActivity) (ImportResult, error) { return ImportResult{}, errors.New("db down") }
	if res, err := e.process(t, c, baseStored()); err == nil || res != "" {
		t.Fatalf("import error must surface for retry: %q %v", res, err)
	}
}

func TestProcessActivity_RejectionsStillTouchSyncedAndNeverImport(t *testing.T) {
	for name, mut := range map[string]func(a *garminStoredActivity){
		"manual": func(a *garminStoredActivity) { a.Manual = true }, "web upload": func(a *garminStoredActivity) { a.IsWebUpload = true },
		"parent": func(a *garminStoredActivity) { a.IsParent = true }, "swimming": func(a *garminStoredActivity) { a.ActivityType = "LAP_SWIMMING" },
		"too short": func(a *garminStoredActivity) { a.Duration = 10 }, "huge distance": func(a *garminStoredActivity) { a.Distance = 5e7 },
	} {
		e, c := importEnv(t)
		a := baseStored()
		mut(&a)
		res, err := e.process(t, c, a)
		if err != nil || !strings.HasPrefix(res, "skipped_") {
			t.Errorf("%s: %q %v", name, res, err)
		}
		if e.store.importCount() != 0 || len(e.store.cs.tails) != 0 {
			t.Errorf("%s: must not import / run the tail", name)
		}
		if len(e.store.cs.touched) != 1 {
			t.Errorf("%s: the pipe is alive, last_synced_at must still be touched", name)
		}
	}
	// 連接之前的活動
	e, c := importEnv(t)
	a := baseStored()
	a.StartTime = c.ConnectedAt.Unix() - 3600
	if res, _ := e.process(t, c, a); res != "skipped_before_connect" || e.store.importCount() != 0 {
		t.Fatalf("before connect: %q", res)
	}
}

// R-C2：暫停旗標絕不可成為丟棄活動的條件。
func TestProcessActivity_PausedConnectionStillImports(t *testing.T) {
	e, c := importEnv(t)
	e.store.mu.Lock()
	e.store.cs.conns[tUserA].Scope = "HEALTH_EXPORT" // paused()
	e.store.mu.Unlock()
	if !e.readConn(tUserA).paused() {
		t.Fatal("setup: connection should be paused")
	}
	res, err := e.process(t, c, baseStored())
	if err != nil || res != "inserted" || e.store.importCount() != 1 {
		t.Fatalf("paused connection must still import real pushes: %q %v imports=%d", res, err, e.store.importCount())
	}
}

func TestProcessActivity_LegacyTwinIsExistsWithoutReward(t *testing.T) {
	e, c := importEnv(t)
	a := baseStored()
	e.store.cs.twins[fmt.Sprintf("%s|bare|%s", tUserA, a.SummaryID)] = true
	res, err := e.process(t, c, a)
	if err != nil || res != "exists" {
		t.Fatalf("legacy twin by bare id: %q %v", res, err)
	}
	if e.store.importCount() != 0 || len(e.store.cs.tails) != 0 {
		t.Fatal("a legacy twin must not be imported nor rewarded")
	}
	// 以「garmin:<開始秒>」保底 id 命中
	e2, c2 := importEnv(t)
	b := baseStored()
	e2.store.cs.twins[fmt.Sprintf("%s|garmin:%d", tUserA, b.StartTime)] = true
	if res, _ := e2.process(t, c2, b); res != "exists" || e2.store.importCount() != 0 {
		t.Fatalf("legacy twin by fallback id: %q", res)
	}
}

func TestProcessActivity_BadPayload(t *testing.T) {
	e, c := importEnv(t)
	ev := garminEvent{ID: "x", EventType: garminEvActivity, ProviderUserID: c.ProviderUserID, Payload: []byte(`{not json`)}
	if res, err := e.h.processActivityEvent(context.Background(), ev); err != nil || res != "skipped_bad_payload" {
		t.Fatalf("bad payload: %q %v", res, err)
	}
}

// 同一批多筆依開始時間由舊到新處理（框架排序＋實際匯入順序）。
func TestPipeline_BatchProcessedOldestFirst(t *testing.T) {
	e := newConnectEnv(t, nil)
	seedConn(t, e, tUserA, time.Hour, 80*24*time.Hour)
	var mu sync.Mutex
	var order []int64
	e.store.cs.importFn = func(a *NormalizedActivity) (ImportResult, error) {
		mu.Lock()
		order = append(order, a.RecordedAt.Unix())
		mu.Unlock()
		return ImportResult{Status: "inserted", ID: "x"}, nil
	}
	base := time.Now().Add(-5 * time.Hour).Unix()
	var acts []map[string]any
	for _, off := range []int64{300, 100, 200} {
		a := synthActivity(e.api.userID, fmt.Sprintf("ORD-%d", off), base+off)
		acts = append(acts, a)
	}
	if rec := e.postJSON("activities", synthPush(acts...)); rec.Code != 200 {
		t.Fatalf("push: %d", rec.Code)
	}
	e.store.waitSettled(t, 3*time.Second)
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != base+100 || order[1] != base+200 || order[2] != base+300 {
		t.Fatalf("import order = %v, want oldest first", order)
	}
}

// 端到端（單元層）：推送 → 落地 → 處理 → 匯入，paused 也照入庫，結果寫回 done/inserted。
func TestPipeline_PushToImport(t *testing.T) {
	e := newConnectEnv(t, nil)
	c := seedConn(t, e, tUserA, time.Hour, 80*24*time.Hour)
	_ = c
	start := time.Now().Add(-90 * time.Minute).Unix()
	if rec := e.postJSON("activities", synthPush(synthActivity(e.api.userID, "E2E-1", start))); rec.Code != 200 {
		t.Fatalf("push: %d", rec.Code)
	}
	e.store.waitSettled(t, 3*time.Second)
	rows := e.store.rows()
	if len(rows) != 1 || rows[0].Status != garminStDone || rows[0].Result != "inserted" {
		t.Fatalf("event row: %+v", rows)
	}
	if e.store.importCount() != 1 || e.store.cs.imports[0].RecordedAt.Unix() != start {
		t.Fatalf("imports: %+v", e.store.cs.imports)
	}
}
