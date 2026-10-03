package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mkEv(id, typ, uid string, attempts int) garminEvent {
	return garminEvent{ID: id, EventType: typ, ProviderUserID: uid, Payload: []byte(`{}`), Attempts: attempts, ReceivedAt: time.Now()}
}

// 失敗退避：第 1～4 次失敗各自排下一次嘗試（1m／5m／30m／2h），第 5 次失敗轉 dead 並告警（告警內容不含使用者資料）。
func TestEvents_RetryBackoffThenDeadWithCleanAlert(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	var calls int32
	tg.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "", errors.New("synthetic failure with user " + tUser1) // 錯誤文字含 userId：不得外洩到告警
	}
	tg.postJSON("activities", synthPush(synthActivity(tUser1, "R-1", 1790020000)))
	tg.store.waitStatus(t, "ev-0001", garminStError, 2*time.Second)

	wantDelays := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour}
	for i, want := range wantDelays {
		row := tg.store.rows()[0]
		if row.Attempts != i+1 || row.Status != garminStError {
			t.Fatalf("after failure %d: attempts=%d status=%s", i+1, row.Attempts, row.Status)
		}
		got := time.Until(row.Next)
		if got < want-5*time.Second || got > want+5*time.Second {
			t.Fatalf("failure %d: next attempt in %s, want ~%s", i+1, got.Round(time.Second), want)
		}
		// 讓事件到期並由掃描器領回處理
		tg.store.mu.Lock()
		tg.store.events["ev-0001"].Next = time.Now().Add(-time.Second)
		tg.store.mu.Unlock()
		if n := tg.h.SweepPending(context.Background()); n != 1 {
			t.Fatalf("sweep %d processed %d events, want 1", i+1, n)
		}
	}
	row := tg.store.rows()[0]
	if row.Status != garminStDead || row.Attempts != garminMaxAttempts {
		t.Fatalf("after 5 failures: status=%s attempts=%d, want dead/5", row.Status, row.Attempts)
	}
	if atomic.LoadInt32(&calls) != int32(garminMaxAttempts) {
		t.Fatalf("handler called %d times, want %d", calls, garminMaxAttempts)
	}
	var dead string
	for _, a := range tg.alertList() {
		if strings.HasPrefix(a, "garmin_dead_event|") {
			dead = a
		}
	}
	if dead == "" {
		t.Fatalf("expected garmin_dead_event alert, got %v", tg.alertList())
	}
	if strings.Contains(dead, tUser1) || strings.Contains(dead, "synthetic failure") {
		t.Fatalf("dead alert leaked user data / error text: %s", dead)
	}
	if !strings.Contains(dead, "event_id=ev-0001") {
		t.Fatalf("dead alert should carry the event id: %s", dead)
	}
	// dead 之後不會再被領取
	tg.store.mu.Lock()
	tg.store.events["ev-0001"].Next = time.Now().Add(-time.Hour)
	tg.store.mu.Unlock()
	if n := tg.h.SweepPending(context.Background()); n != 0 {
		t.Fatalf("dead event must not be swept, processed %d", n)
	}
}

func TestEvents_PanicBecomesFailureNotCrash(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	tg.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) { panic("boom") }
	tg.postJSON("activities", synthPush(synthActivity(tUser1, "P-1", 1790021000)))
	tg.store.waitStatus(t, "ev-0001", garminStError, 2*time.Second)
	if r := tg.store.rows()[0]; r.Attempts != 1 || r.LastError != "panic" {
		t.Fatalf("panic should count as one failure with code panic: %+v", r)
	}
}

func TestEvents_NotReadyKeepsPendingWithoutAttempts(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	tg.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) { return "", errGarminNotReady }
	tg.postJSON("activities", synthPush(synthActivity(tUser1, "N-1", 1790022000)))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r := tg.store.rows()[0]
		if r.LastError == "processor_not_ready" {
			if r.Status != garminStPending || r.Attempts != 0 || time.Until(r.Next) < 50*time.Minute {
				t.Fatalf("not-ready must stay pending, 0 attempts, deferred ~1h: %+v", r)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("event was never deferred")
}

func TestEvents_UserLockBusyDefersWithoutFailure(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	tg.store.lockErr = errGarminBusy
	tg.postJSON("activities", synthPush(synthActivity(tUser1, "B-1", 1790023000)))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r := tg.store.rows()[0]
		if r.LastError == "busy" {
			if r.Status != garminStPending || r.Attempts != 0 {
				t.Fatalf("busy must not count as a failure: %+v", r)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("event was never deferred as busy")
}

// 兩個並發掃描不會領到同一筆事件（SKIP LOCKED 語意；fake 以 mutex 內的租約更新模擬，真 PG 版在整合測試）。
func TestEvents_ConcurrentSweepsDoNotDoubleProcess(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	var mu sync.Mutex
	seen := map[string]int{}
	tg.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		mu.Lock()
		seen[ev.ID]++
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		return garminResInserted, nil
	}
	// 直接放 30 筆「已到期」事件（不經 webhook，避免就地處理搶先）
	for i := 0; i < 30; i++ {
		k := "k" + string(rune('a'+i%26)) + string(rune('A'+i/26))
		tg.store.InsertGarminEvents(context.Background(), []garminEventIn{{EventType: garminEvActivity, ProviderUserID: tUser1, DedupeKey: &k, Payload: []byte(`{"startTimeInSeconds":1790030000}`)}})
	}
	tg.store.mu.Lock()
	for _, r := range tg.store.events {
		r.Next = time.Now().Add(-time.Second)
	}
	tg.store.mu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); tg.h.SweepPending(context.Background()) }()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 30 {
		t.Fatalf("processed %d distinct events, want 30", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("event %s processed %d times", id, n)
		}
	}
}

func TestEvents_SweepIgnoresLeasedEvents(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	k := "lease-1"
	tg.store.InsertGarminEvents(context.Background(), []garminEventIn{{EventType: garminEvActivity, ProviderUserID: tUser1, DedupeKey: &k, Payload: []byte(`{}`)}})
	// 剛落地的事件有 2 分鐘租約：掃描器不得搶走（就地處理正在進行）
	if n := tg.h.SweepPending(context.Background()); n != 0 {
		t.Fatalf("leased event was swept (%d)", n)
	}
	tg.store.mu.Lock()
	tg.store.events["ev-0001"].Next = time.Now().Add(-time.Second) // 租約過期（行程被殺）
	tg.store.mu.Unlock()
	if n := tg.h.SweepPending(context.Background()); n != 1 {
		t.Fatalf("expired lease must be re-processed, swept %d", n)
	}
	if tg.store.rows()[0].Status != garminStDone {
		t.Fatalf("status=%s want done", tg.store.rows()[0].Status)
	}
}

// 崩潰復原：回 200 後行程被殺（以 draining 模擬：事件已落地、但沒有任何處理啟動）→ 新行程的啟動掃描處理，且只處理一次。
func TestEvents_StartupSweepRecoversUnprocessedAfterAck(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	var oldRuns int32
	tg.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		atomic.AddInt32(&oldRuns, 1)
		return garminResInserted, nil
	}
	tg.h.draining.Store(true) // 舊行程收到 SIGTERM：仍回 200（事件已落地），但不再啟動處理
	if rec := tg.postJSON("activities", synthPush(synthActivity(tUser1, "CR-1", 1790040000))); rec.Code != 200 {
		t.Fatalf("push: %d", rec.Code)
	}
	time.Sleep(50 * time.Millisecond)
	if r := tg.store.rows(); len(r) != 1 || r[0].Status != garminStPending || atomic.LoadInt32(&oldRuns) != 0 {
		t.Fatalf("setup: event must be persisted and unprocessed: %+v runs=%d", r, oldRuns)
	}
	// 關閉中收到的推送：落地後立刻釋放租約，新行程的啟動掃描不必等 2 分鐘租約過期
	tg.store.mu.Lock()
	released := append([]string(nil), tg.store.released...)
	nextAt := tg.store.events["ev-0001"].Next
	tg.store.mu.Unlock()
	if len(released) != 1 || released[0] != "ev-0001" || time.Until(nextAt) > time.Second {
		t.Fatalf("lease must be released right away when draining: released=%v next in %s", released, time.Until(nextAt))
	}
	// 新行程：租約已過期，啟動掃描處理
	tg.store.mu.Lock()
	tg.store.events["ev-0001"].Next = time.Now().Add(-time.Second)
	tg.store.mu.Unlock()
	var runs int32
	h2 := newGarminHandlerWithStore(GarminConfig{WebhookToken: testGarminToken}, GarminDeps{}, tg.store)
	h2.startupDelay = 5 * time.Millisecond
	h2.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		atomic.AddInt32(&runs, 1)
		return garminResInserted, nil
	}
	h2.StartupSweep(context.Background())
	if atomic.LoadInt32(&runs) != 1 {
		t.Fatalf("startup sweep ran the handler %d times, want 1", runs)
	}
	if r := tg.store.rows()[0]; r.Status != garminStDone {
		t.Fatalf("status = %s, want done", r.Status)
	}
	// 再掃一次不會重複處理
	h2.SweepPending(context.Background())
	if atomic.LoadInt32(&runs) != 1 {
		t.Fatalf("second sweep reprocessed the event (%d)", runs)
	}
}

func TestEvents_StartupSweepCancelledBeforeDelay(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.h.startupDelay = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tg.h.StartupSweep(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("StartupSweep did not return after ctx cancel")
	}
}

func TestEvents_DrainWaitsAndReleasesLeases(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	started := make(chan struct{})
	release := make(chan struct{})
	tg.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		close(started)
		<-release
		return garminResInserted, nil
	}
	tg.postJSON("activities", synthPush(synthActivity(tUser1, "D-1", 1790050000)))
	<-started
	// 1) 處理仍在進行時 Drain 會等；ctx 到期後返回並釋放尚未完成事件的租約
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	tg.h.Drain(ctx)
	if time.Since(start) < 100*time.Millisecond {
		t.Fatal("Drain returned before the in-flight event finished or ctx expired")
	}
	tg.store.mu.Lock()
	released := append([]string(nil), tg.store.released...)
	tg.store.mu.Unlock()
	if len(released) != 1 || released[0] != "ev-0001" {
		t.Fatalf("Drain must release the unfinished lease, released=%v", released)
	}
	// 2) Drain 之後新的推送仍落地，但不再啟動背景處理（交給新行程的啟動掃描）
	var ran int32
	tg.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		atomic.AddInt32(&ran, 1)
		return garminResInserted, nil
	}
	if rec := tg.postJSON("activities", synthPush(synthActivity(tUser1, "D-2", 1790050001))); rec.Code != 200 {
		t.Fatalf("push during drain: %d", rec.Code)
	}
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&ran) != 0 {
		t.Fatal("no processing may start after Drain")
	}
	close(release)
}

func TestEvents_DrainIdleReturnsImmediately(t *testing.T) {
	tg := newTestGarmin(t, nil)
	start := time.Now()
	tg.h.Drain(context.Background())
	if time.Since(start) > time.Second {
		t.Fatal("idle Drain should return immediately")
	}
}

func TestEvents_ProcessOrderActivitiesByStartThenPermissionThenDereg(t *testing.T) {
	evs := []garminEvent{
		mkEv("d", garminEvDeregister, tUser1, 0),
		{ID: "p", EventType: garminEvPermission, ProviderUserID: tUser1, Payload: []byte(`{}`), ReceivedAt: time.Unix(1790000000, 0)},
		{ID: "a3", EventType: garminEvActivity, ProviderUserID: tUser1, Payload: []byte(`{"startTimeInSeconds":300}`)},
		{ID: "a1", EventType: garminEvActivity, ProviderUserID: tUser1, Payload: []byte(`{"startTimeInSeconds":100}`)},
		{ID: "a2", EventType: garminEvActivity, ProviderUserID: tUser1, Payload: []byte(`{"startTimeInSeconds":200}`)},
	}
	sortGarminEvents(evs)
	var ids []string
	for _, e := range evs {
		ids = append(ids, e.ID)
	}
	if got := strings.Join(ids, ","); got != "a1,a2,a3,p,d" {
		t.Fatalf("order = %s, want a1,a2,a3,p,d", got)
	}
}

func TestEvents_ReleaseLeasesWhenBudgetExhausted(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	tg.store.addUser(tUser2, tDor2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 預算一開始就用完
	k1, k2 := "b-1", "b-2"
	ins, _ := tg.store.InsertGarminEvents(context.Background(), []garminEventIn{
		{EventType: garminEvActivity, ProviderUserID: tUser1, DedupeKey: &k1, Payload: []byte(`{}`)},
		{EventType: garminEvActivity, ProviderUserID: tUser2, DedupeKey: &k2, Payload: []byte(`{}`)},
	})
	tg.h.processEvents(ctx, ins)
	tg.store.mu.Lock()
	n := len(tg.store.released)
	tg.store.mu.Unlock()
	if n != 2 {
		t.Fatalf("exhausted budget must release the leases of unprocessed events, released=%d", n)
	}
}

// 驗收：garmin*.go 沒有 ticker／週期性迴圈（Neon 必須能睡）。
func TestNoTickersInGarminFiles(t *testing.T) {
	files, err := filepath.Glob("garmin*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no garmin files found: %v", err)
	}
	re := regexp.MustCompile(`time\.NewTicker|time\.Tick\(|\.Tick\(|for\s*\{\s*select`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		// 去掉註解行再比對（註解會提到「沒有 ticker」）
		var code []string
		for _, ln := range strings.Split(string(b), "\n") {
			if i := strings.Index(ln, "//"); i >= 0 {
				ln = ln[:i]
			}
			code = append(code, ln)
		}
		if loc := re.FindString(strings.Join(code, "\n")); loc != "" {
			t.Errorf("%s contains a periodic construct: %q", f, loc)
		}
	}
}

// 驗收：處理失敗不得在 log 外洩 userId 全文／路徑 token。
func TestEvents_LogsNeverContainTokenOrFullUserID(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	logs := captureLogs(t)
	tg.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		return "", errors.New("db error mentioning " + tUser1)
	}
	tg.postJSON("activities", synthPush(synthActivity(tUser1, "L-1", 1790060000)))
	tg.store.waitStatus(t, "ev-0001", garminStError, 2*time.Second)
	// 錯誤路徑／404／503 路徑
	tg.post("/webhook/nope/activities", strings.NewReader("{}"), nil)
	tg.store.insertErr = errors.New("simulated")
	tg.postJSON("activities", synthPush(synthActivity(tUser1, "L-2", 1790060001)))
	time.Sleep(50 * time.Millisecond)
	l := logs()
	for _, bad := range []string{testGarminToken, testGarminTokenPrev, tUser1, "webhook/" + testGarminToken} {
		if strings.Contains(l, bad) {
			t.Fatalf("log output contains %q", bad[:6])
		}
	}
	if r := tg.store.rows()[0]; strings.Contains(r.LastError, tUser1) {
		t.Fatalf("last_error stored raw error text with user id: %q", r.LastError)
	}
}
