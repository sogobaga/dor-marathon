package ops

// 每日視窗排程（dailyjob.go）與每日營運報告／自檢的可靠度單元測試（COROS GA 契約 §4）：
// 不連資料庫——跨實例協調抽成 jobStore 介面，這裡用假實作驗證「成功才標記、失敗重試、窗口外不碰 DB、
// 重試節奏、鎖互斥、標題」；Postgres 實作（pgJobStore）的整合測試見 dailyjob_integration_test.go。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeJobStore 記錄所有呼叫；lockHeld 模擬別的實例持有鎖。
type fakeJobStore struct {
	mu        sync.Mutex
	marker    map[string]string
	lockHeld  bool
	lockErr   error
	locks     int
	unlocks   int
	reads     int
	writes    []string
	writeErr  error
	readErr   error
	totalCall int
}

func newFakeJobStore() *fakeJobStore { return &fakeJobStore{marker: map[string]string{}} }

func (f *fakeJobStore) TryLock(ctx context.Context, name string) (func(), bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.totalCall++
	if f.lockErr != nil {
		return nil, false, f.lockErr
	}
	if f.lockHeld {
		return nil, false, nil
	}
	f.locks++
	return func() {
		f.mu.Lock()
		f.unlocks++
		f.mu.Unlock()
	}, true, nil
}

func (f *fakeJobStore) ReadMarker(ctx context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.totalCall++
	f.reads++
	return f.marker[key], f.readErr
}

func (f *fakeJobStore) WriteMarker(ctx context.Context, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.totalCall++
	if f.writeErr != nil {
		return f.writeErr
	}
	f.marker[key] = value
	f.writes = append(f.writes, key+"="+value)
	return nil
}

// at 台灣時間 2026-10-03 hh:mm（runDailyJob 吃的就是 taiwanNow() 這種「已加 8 小時」的時間值）。
func at(h, m int) time.Time { return time.Date(2026, 10, 3, h, m, 0, 0, time.UTC) }

func testJob(st *dailyJobState, run func(ctx context.Context) (string, error), onFail func(string, error, int, bool)) *dailyJob {
	return &dailyJob{name: "test", lockName: "test_lock", persistKey: "test_marker", state: st, run: run, onFail: onFail}
}

func TestRunDailyJob_OutsideWindowDoesNotTouchStore(t *testing.T) {
	st := &dailyJobState{}
	store := newFakeJobStore()
	ran := false
	job := testJob(st, func(ctx context.Context) (string, error) { ran = true; return "", nil }, nil)
	for _, hr := range []int{0, 7, 9, 12, 23} {
		runDailyJob(context.Background(), store, job, at(hr, 30))
	}
	if ran || store.totalCall != 0 {
		t.Fatalf("outside 08:00-08:59 nothing may run or touch the DB: ran=%v storeCalls=%d", ran, store.totalCall)
	}
}

// 成功才標記：run 成功之後才寫持久標記；同一天第二次 tick 不再碰 store（in-memory 已完成）。
func TestRunDailyJob_SuccessWritesMarkerThenStopsTouchingStore(t *testing.T) {
	st := &dailyJobState{}
	store := newFakeJobStore()
	runs := 0
	job := testJob(st, func(ctx context.Context) (string, error) {
		// run 執行當下，持久標記數量必須還是「之前成功的天數」——這一輪還沒成功，不能先寫。
		if len(store.writes) != runs {
			t.Errorf("persistent marker must NOT be written before run succeeded (writes=%d, completed runs=%d)", len(store.writes), runs)
		}
		runs++
		return "", nil
	}, nil)
	runDailyJob(context.Background(), store, job, at(8, 0))
	if runs != 1 || len(store.writes) != 1 || store.writes[0] != "test_marker=2026-10-03" {
		t.Fatalf("want 1 run and marker written after success, runs=%d writes=%v", runs, store.writes)
	}
	if store.locks != 1 || store.unlocks != 1 {
		t.Fatalf("lock must be taken and released exactly once: locks=%d unlocks=%d", store.locks, store.unlocks)
	}
	calls := store.totalCall
	for m := 1; m < 60; m++ {
		runDailyJob(context.Background(), store, job, at(8, m))
	}
	if runs != 1 || store.totalCall != calls {
		t.Fatalf("after success the rest of the window must not run or touch the store: runs=%d extraCalls=%d", runs, store.totalCall-calls)
	}
	// 隔天（新的台灣日）又可以跑
	next := at(8, 0).AddDate(0, 0, 1)
	runDailyJob(context.Background(), store, job, next)
	if runs != 2 || store.marker["test_marker"] != "2026-10-04" {
		t.Fatalf("next day must run again: runs=%d marker=%q", runs, store.marker["test_marker"])
	}
}

// 失敗：不寫持久標記、不設已完成；10 分鐘內不重試，到時間才重試；最多 5 次；第 5 次回報放棄。
func TestRunDailyJob_FailureRetriesEvery10MinUpTo5(t *testing.T) {
	st := &dailyJobState{}
	store := newFakeJobStore()
	runs := 0
	var fails []string
	job := testJob(st, func(ctx context.Context) (string, error) {
		runs++
		return "send", errors.New("telegram down")
	}, func(stage string, err error, attempt int, giveUp bool) {
		fails = append(fails, fmt.Sprintf("%s/%d/%v", stage, attempt, giveUp))
	})

	runDailyJob(context.Background(), store, job, at(8, 0))
	if runs != 1 || len(store.writes) != 0 {
		t.Fatalf("first failure: runs=%d writes=%v (marker must not be written)", runs, store.writes)
	}
	// 10 分鐘內每分鐘 tick：不重試、也不碰 store
	calls := store.totalCall
	for m := 1; m < 10; m++ {
		runDailyJob(context.Background(), store, job, at(8, m))
	}
	if runs != 1 || store.totalCall != calls {
		t.Fatalf("within the 10-minute backoff nothing may happen: runs=%d extraStoreCalls=%d", runs, store.totalCall-calls)
	}
	for _, m := range []int{10, 20, 30, 40} {
		runDailyJob(context.Background(), store, job, at(8, m))
	}
	if runs != 5 {
		t.Fatalf("want exactly 5 attempts (08:00, :10, :20, :30, :40), got %d", runs)
	}
	// 第 5 次之後不再重試
	for m := 41; m < 60; m++ {
		runDailyJob(context.Background(), store, job, at(8, m))
	}
	if runs != 5 {
		t.Fatalf("no 6th attempt allowed, got %d", runs)
	}
	if len(store.writes) != 0 {
		t.Fatalf("a day with only failures must never write the persistent marker: %v", store.writes)
	}
	want := []string{"send/1/false", "send/2/false", "send/3/false", "send/4/false", "send/5/true"}
	if strings.Join(fails, ",") != strings.Join(want, ",") {
		t.Fatalf("onFail sequence = %v, want %v (stage and attempt carried, give-up flagged on the 5th)", fails, want)
	}
	if store.locks != 5 || store.unlocks != 5 {
		t.Fatalf("lock released after every attempt: locks=%d unlocks=%d", store.locks, store.unlocks)
	}
}

// 失敗一次後重試成功：持久標記只在成功那次才寫。
func TestRunDailyJob_FailThenSucceedWritesMarkerOnlyOnSuccess(t *testing.T) {
	st := &dailyJobState{}
	store := newFakeJobStore()
	runs := 0
	job := testJob(st, func(ctx context.Context) (string, error) {
		runs++
		if runs == 1 {
			return "build", errors.New("db hiccup")
		}
		return "", nil
	}, nil)
	runDailyJob(context.Background(), store, job, at(8, 0))
	if len(store.writes) != 0 {
		t.Fatalf("marker must not exist after the failed attempt: %v", store.writes)
	}
	runDailyJob(context.Background(), store, job, at(8, 10))
	if runs != 2 || len(store.writes) != 1 || store.marker["test_marker"] != "2026-10-03" {
		t.Fatalf("retry success must write the marker: runs=%d writes=%v", runs, store.writes)
	}
	runDailyJob(context.Background(), store, job, at(8, 20))
	if runs != 2 {
		t.Fatal("must not run again after success")
	}
}

// 沒搶到鎖（別的實例在做）：不跑、不計嘗試；鎖釋放後下一分鐘立刻可以接手。
func TestRunDailyJob_LockHeldElsewhereDoesNotCountAsAttempt(t *testing.T) {
	st := &dailyJobState{}
	store := newFakeJobStore()
	store.lockHeld = true
	runs := 0
	job := testJob(st, func(ctx context.Context) (string, error) { runs++; return "", nil }, nil)
	for m := 0; m < 8; m++ {
		runDailyJob(context.Background(), store, job, at(8, m))
	}
	if runs != 0 || st.attempts != 0 {
		t.Fatalf("lock miss must not run nor burn attempts: runs=%d attempts=%d", runs, st.attempts)
	}
	store.lockHeld = false
	runDailyJob(context.Background(), store, job, at(8, 8))
	if runs != 1 {
		t.Fatalf("after the other instance released the lock we must take over, runs=%d", runs)
	}
}

// 持久標記已是今天（別的實例／重啟前已成功送過）：不跑，直接視為完成。
func TestRunDailyJob_PersistentMarkerSaysDone(t *testing.T) {
	st := &dailyJobState{}
	store := newFakeJobStore()
	store.marker["test_marker"] = "2026-10-03"
	runs := 0
	job := testJob(st, func(ctx context.Context) (string, error) { runs++; return "", nil }, nil)
	runDailyJob(context.Background(), store, job, at(8, 0))
	if runs != 0 {
		t.Fatal("marker already says today is done: must not run")
	}
	calls := store.totalCall
	runDailyJob(context.Background(), store, job, at(8, 1))
	if store.totalCall != calls {
		t.Fatal("in-memory must be synced to done so later ticks never touch the store")
	}
	// 標記是昨天：要跑
	st2 := &dailyJobState{}
	store.marker["test_marker"] = "2026-10-02"
	job2 := testJob(st2, func(ctx context.Context) (string, error) { runs++; return "", nil }, nil)
	runDailyJob(context.Background(), store, job2, at(8, 0))
	if runs != 1 {
		t.Fatal("yesterday's marker must not block today")
	}
}

// 讀標記失敗：視為今天還沒成功（寧可重送，不要漏整天）；取鎖出錯：算一次失敗（階段 lock）。
func TestRunDailyJob_StoreErrors(t *testing.T) {
	store := newFakeJobStore()
	store.readErr = errors.New("read boom")
	st := &dailyJobState{}
	runs := 0
	job := testJob(st, func(ctx context.Context) (string, error) { runs++; return "", nil }, nil)
	runDailyJob(context.Background(), store, job, at(8, 0))
	if runs != 1 {
		t.Fatalf("marker read failure must be treated as not-done and still run, runs=%d", runs)
	}

	store2 := newFakeJobStore()
	store2.lockErr = errors.New("conn pool exhausted")
	st2 := &dailyJobState{}
	var gotStage string
	job2 := testJob(st2, func(ctx context.Context) (string, error) {
		t.Fatal("must not run when the lock errored")
		return "", nil
	},
		func(stage string, err error, attempt int, giveUp bool) { gotStage = stage })
	runDailyJob(context.Background(), store2, job2, at(8, 0))
	if gotStage != "lock" || st2.attempts != 1 {
		t.Fatalf("lock error = one failed attempt with stage lock, stage=%q attempts=%d", gotStage, st2.attempts)
	}

	// 寫標記失敗：仍視為成功（in-memory 擋住本進程重送）
	store3 := newFakeJobStore()
	store3.writeErr = errors.New("write boom")
	st3 := &dailyJobState{}
	runs3 := 0
	job3 := testJob(st3, func(ctx context.Context) (string, error) { runs3++; return "", nil }, nil)
	runDailyJob(context.Background(), store3, job3, at(8, 0))
	runDailyJob(context.Background(), store3, job3, at(8, 1))
	if runs3 != 1 {
		t.Fatalf("marker write failure after success must not cause a re-send in this process, runs=%d", runs3)
	}
}

// 同進程併發 tick：只有一個能進場（in-flight 標記）。
func TestDailyJobState_BeginIsExclusive(t *testing.T) {
	st := &dailyJobState{}
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if st.begin("2026-10-03", at(8, 0)) {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("exactly one concurrent tick may start the job, got %d", winners)
	}
}

// --- 每日營運報告：run 的階段、成功才標記、標題 ---

func reportHandlerWithFakes(build func(ctx context.Context) (dailyReportData, error), send func(ctx context.Context, text string) error) *Handler {
	return &Handler{buildReport: build, sendTG: send}
}

func TestDailyReportRun_BuildFailureHasStageBuild(t *testing.T) {
	h := reportHandlerWithFakes(
		func(ctx context.Context) (dailyReportData, error) { return dailyReportData{}, errors.New("db down") },
		func(ctx context.Context, text string) error { t.Fatal("must not send when build failed"); return nil })
	stage, err := h.dailyReportRun(context.Background())
	if stage != "build" || err == nil {
		t.Fatalf("stage=%q err=%v, want build failure", stage, err)
	}
}

func TestDailyReportRun_SendFailureHasStageSend(t *testing.T) {
	h := reportHandlerWithFakes(
		func(ctx context.Context) (dailyReportData, error) { return baseReportData(), nil },
		func(ctx context.Context, text string) error {
			return errors.New("telegram sendMessage failed: status 502")
		})
	stage, err := h.dailyReportRun(context.Background())
	if stage != "send" || err == nil {
		t.Fatalf("stage=%q err=%v, want send failure", stage, err)
	}
}

// 端到端（假 store＋假 Telegram）：送失敗→不寫標記→10 分鐘後重試→成功才寫標記。
func TestDailyReportJob_TelegramFailsThenSucceeds_MarkerOnlyAfter2xx(t *testing.T) {
	var sent []string
	failNext := true
	h := reportHandlerWithFakes(
		func(ctx context.Context) (dailyReportData, error) { return baseReportData(), nil },
		func(ctx context.Context, text string) error {
			if failNext {
				failNext = false
				return errors.New("telegram sendMessage failed: status 500")
			}
			sent = append(sent, text)
			return nil
		})
	store := newFakeJobStore()
	job := h.dailyReportJob()

	runDailyJob(context.Background(), store, job, at(8, 0))
	if len(sent) != 0 || len(store.writes) != 0 {
		t.Fatalf("a failed Telegram send must leave no persistent marker and no 'sent': sent=%d writes=%v", len(sent), store.writes)
	}
	runDailyJob(context.Background(), store, job, at(8, 5)) // 還沒到 10 分鐘
	if len(sent) != 0 {
		t.Fatal("must wait 10 minutes before the retry")
	}
	runDailyJob(context.Background(), store, job, at(8, 10))
	if len(sent) != 1 || len(store.writes) != 1 || store.marker[dailyReportPersistentKey] != "2026-10-03" {
		t.Fatalf("retry succeeded: sent=%d writes=%v marker=%q", len(sent), store.writes, store.marker[dailyReportPersistentKey])
	}
	runDailyJob(context.Background(), store, job, at(8, 20))
	if len(sent) != 1 {
		t.Fatal("no duplicate send after success")
	}
}

// 標題明確標示「昨日統計」，日期是統計的昨日（擁有者曾誤以為標題日期是發送日）。
func TestDailyReportTitleStatesYesterdayStatistics(t *testing.T) {
	d := baseReportData()
	d.ReportDate = "2026-10-02"
	msg := buildDailyReportMessage(d)
	first := strings.SplitN(msg, "\n", 2)[0]
	if !strings.Contains(first, "DOR 每日營運報告｜2026-10-02（昨日統計）") {
		t.Fatalf("title must read 'DOR 每日營運報告｜2026-10-02（昨日統計）', got %q", first)
	}
}

// --- 自檢：同一套可靠度（同步送、失敗回階段、全正常不送）---

func TestSelfCheckRun_AllOKSendsNothing(t *testing.T) {
	h := &Handler{
		checks: func(ctx context.Context) []CheckResult {
			return []CheckResult{{Name: "paytx_long_pending", OK: true}, {Name: activityHeartbeatCheck, OK: true}}
		},
		sendTG: func(ctx context.Context, text string) error { t.Fatal("all-OK must not send"); return nil },
	}
	stage, err := h.selfCheckRun(context.Background())
	if stage != "" || err != nil {
		t.Fatalf("stage=%q err=%v", stage, err)
	}
}

func TestSelfCheckRun_AnomalySendsSynchronously_AndSendFailureHasStageSend(t *testing.T) {
	var texts []string
	h := &Handler{
		checks: func(ctx context.Context) []CheckResult {
			return []CheckResult{
				{Name: "webhook_failure_rate", OK: false, Detail: "failed=6 paid=2"},
				{Name: activityHeartbeatCheck, OK: false, Detail: "近24h activities 新增數為 0"},
			}
		},
		sendTG: func(ctx context.Context, text string) error { texts = append(texts, text); return nil },
	}
	if stage, err := h.selfCheckRun(context.Background()); stage != "" || err != nil {
		t.Fatalf("stage=%q err=%v", stage, err)
	}
	if len(texts) != 2 || !strings.Contains(texts[0], "每日自檢發現 1 項異常") || !strings.Contains(texts[0], "failed=6 paid=2") ||
		!strings.Contains(texts[1], "近24h平台活動上傳數為0") || !strings.HasPrefix(texts[0], "🚨 [DOR] ") {
		t.Fatalf("unexpected messages: %q", texts)
	}

	h.sendTG = func(ctx context.Context, text string) error {
		return errors.New("telegram sendMessage failed: status 500")
	}
	if stage, err := h.selfCheckRun(context.Background()); stage != "send" || err == nil {
		t.Fatalf("send failure must surface as stage=send so the scheduler retries, stage=%q err=%v", stage, err)
	}
}

// 自檢與營運報告共用同一個 Handler、同一個窗口：兩者的狀態、鎖名、持久標記 key 互相獨立。
func TestSelfCheckAndDailyReportJobsAreIndependent(t *testing.T) {
	h := &Handler{}
	a, b := h.selfCheckJob(), h.dailyReportJob()
	if a.state == b.state || a.lockName == b.lockName || a.persistKey == b.persistKey {
		t.Fatalf("self-check and daily-report jobs must not share state/lock/marker: %+v %+v", a, b)
	}
	if a.persistKey != "ops_selfcheck_last_date" || b.persistKey != "ops_daily_report_last_date" {
		t.Fatalf("persistent marker keys changed: %q %q", a.persistKey, b.persistKey)
	}
}
