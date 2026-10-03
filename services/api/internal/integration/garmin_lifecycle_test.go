package integration

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func deregEv(gid string, attempts int) garminEvent {
	return garminEvent{ID: "ev-dereg", EventType: garminEvDeregister, ProviderUserID: gid, Payload: []byte(`{}`), Attempts: attempts, ReceivedAt: time.Now()}
}

func permEv(gid string) garminEvent {
	return garminEvent{ID: "ev-perm", EventType: garminEvPermission, ProviderUserID: gid, Payload: []byte(`{"permissions":[]}`), ReceivedAt: time.Now()}
}

func lifecycleEnv(t *testing.T) (*connectEnv, *garminConn) {
	t.Helper()
	e := newConnectEnv(t, nil)
	c := seedConn(t, e, tUserA, time.Hour, 80*24*time.Hour)
	return e, c
}

func assertPurged(t *testing.T, e *connectEnv, name string, want bool) {
	t.Helper()
	got := len(e.store.cs.purges) > 0
	if got != want {
		t.Fatalf("%s: purged=%v, want %v", name, got, want)
	}
}

// 撤銷實測矩陣（R-M1）：確認撤銷 → 清理。
func TestDereg_ConfirmedRevocationPurges(t *testing.T) {
	for name, setup := range map[string]func(e *connectEnv){
		"permissions 401 (token revoked)": func(e *connectEnv) { e.api.revoked = true },
		"permissions 403":                 func(e *connectEnv) { e.api.permsStatus = 403 },
		"permissions 404":                 func(e *connectEnv) { e.api.permsStatus = 404 },
		"permissions 410":                 func(e *connectEnv) { e.api.permsStatus = 410 },
		"refresh invalid_grant (expired access)": func(e *connectEnv) {
			e.api.revoked = true
			e.store.mu.Lock()
			e.store.cs.conns[tUserA].ExpiresAt = time.Now().Add(-time.Hour)
			e.store.mu.Unlock()
		},
		"already flagged for re-authorization": func(e *connectEnv) {
			now := time.Now()
			e.store.mu.Lock()
			e.store.cs.conns[tUserA].ReauthRequiredAt = &now
			e.store.mu.Unlock()
		},
	} {
		e, c := lifecycleEnv(t)
		setup(e)
		res, err := e.h.processDeregEvent(context.Background(), deregEv(c.ProviderUserID, 0))
		if err != nil || res != "purged" {
			t.Fatalf("%s: %q %v", name, res, err)
		}
		assertPurged(t, e, name, true)
		if got := e.store.cs.purges[0]; got.UserID != tUserA || got.GarminUID != c.ProviderUserID {
			t.Fatalf("%s: purge target %+v", name, got)
		}
	}
}

// token 仍有效（200）→ 未決：+10 分、+1 小時、+6 小時重驗；三次後判定偽造／過期並告警，不刪。
func TestDereg_StillValidIsRecheckedThenReportedNeverPurged(t *testing.T) {
	e, c := lifecycleEnv(t)
	wantDelays := []time.Duration{10 * time.Minute, time.Hour, 6 * time.Hour}
	for attempts, want := range wantDelays {
		_, err := e.h.processDeregEvent(context.Background(), deregEv(c.ProviderUserID, attempts))
		var re *garminRecheckError
		if !errors.As(err, &re) || re.Delay != want {
			t.Fatalf("attempt %d: err=%v, want recheck in %s", attempts, err, want)
		}
	}
	res, err := e.h.processDeregEvent(context.Background(), deregEv(c.ProviderUserID, 3))
	if err != nil || res != "unconfirmed" {
		t.Fatalf("after 3 rechecks: %q %v", res, err)
	}
	assertPurged(t, e, "alive token", false)
	if !contains(e.alertKinds(), "garmin_deauth_unconfirmed") {
		t.Fatalf("expected garmin_deauth_unconfirmed alert: %v", e.alertKinds())
	}
	for _, a := range e.mails {
		if strings.Contains(a, tUserA) || strings.Contains(a, c.ProviderUserID) {
			t.Fatalf("alert leaked user data: %s", a)
		}
	}
	if len(e.mailer.list()) != 0 {
		t.Fatal("no in-app mail for an unconfirmed event")
	}
}

// 「第一次 200、第二次 401 → 清理」：Garmin 側時差造成的暫時 200 不會讓真實撤銷永遠不被處理。
func TestDereg_FirstValidThenRevokedPurges(t *testing.T) {
	e, c := lifecycleEnv(t)
	if _, err := e.h.processDeregEvent(context.Background(), deregEv(c.ProviderUserID, 0)); err == nil {
		t.Fatal("first check should be inconclusive")
	}
	e.api.mu.Lock()
	e.api.revoked = true
	e.api.mu.Unlock()
	res, err := e.h.processDeregEvent(context.Background(), deregEv(c.ProviderUserID, 1))
	if err != nil || res != "purged" {
		t.Fatalf("second check: %q %v", res, err)
	}
	assertPurged(t, e, "401 on recheck", true)
}

func TestDereg_TransientErrorsNeverPurge(t *testing.T) {
	for name, setup := range map[string]func(e *connectEnv){
		"permissions 500": func(e *connectEnv) { e.api.permsStatus = 500 },
		"permissions 429": func(e *connectEnv) { e.api.permsStatus = 429 },
		"network error":   func(e *connectEnv) { e.api.netErr = &net.OpError{Op: "dial", Err: errors.New("refused")} },
		"token endpoint 503 (expired access)": func(e *connectEnv) {
			e.api.tokenStatus = 503
			e.store.mu.Lock()
			e.store.cs.conns[tUserA].ExpiresAt = time.Now().Add(-time.Hour)
			e.store.mu.Unlock()
		},
	} {
		e, c := lifecycleEnv(t)
		setup(e)
		res, err := e.h.processDeregEvent(context.Background(), deregEv(c.ProviderUserID, 0))
		var re *garminRecheckError
		if err == nil || errors.As(err, &re) || res != "" {
			t.Fatalf("%s: want a plain transient error (normal backoff), got %q %v", name, res, err)
		}
		assertPurged(t, e, name, false)
	}
	// 其他 4xx（例如 400）：未決（走重驗），不刪
	e, c := lifecycleEnv(t)
	e.api.permsStatus = 400
	_, err := e.h.processDeregEvent(context.Background(), deregEv(c.ProviderUserID, 0))
	var re *garminRecheckError
	if !errors.As(err, &re) {
		t.Fatalf("400: want recheck, got %v", err)
	}
	assertPurged(t, e, "400", false)
}

// Evaluation→Production 換 App：舊世代連線無法實測，資料不動；已用新 App 重新授權者，舊 App 的撤銷通知被實測擋下。
func TestDereg_AppGenerationSwitchSafety(t *testing.T) {
	e, c := lifecycleEnv(t)
	e.store.mu.Lock()
	e.store.cs.conns[tUserA].Issuer = "garmin-app:evalgen0"
	e.store.mu.Unlock()
	res, err := e.h.processDeregEvent(context.Background(), deregEv(c.ProviderUserID, 0))
	if err != nil || res != "skipped_generation" {
		t.Fatalf("old generation: %q %v", res, err)
	}
	assertPurged(t, e, "old generation", false)
	if tk, ui, pm, del := e.api.calls(); tk+ui+pm+del != 0 {
		t.Fatalf("old generation must not call garmin at all: %d %d %d %d", tk, ui, pm, del)
	}
	// 已重新授權（issuer＝目前世代）、token 有效：舊 App 的 dereg 事件 → 實測 200 → 不刪
	e2, c2 := lifecycleEnv(t)
	_, err = e2.h.processDeregEvent(context.Background(), deregEv(c2.ProviderUserID, 0))
	var re *garminRecheckError
	if !errors.As(err, &re) {
		t.Fatalf("re-authorized connection: stale dereg must be held back, got %v", err)
	}
	assertPurged(t, e2, "re-authorized", false)
}

func TestDereg_UnknownUserAndSelfEcho(t *testing.T) {
	e := newConnectEnv(t, nil)
	res, err := e.h.processDeregEvent(context.Background(), deregEv("nobody", 0))
	if err != nil || res != "unknown_user" {
		t.Fatalf("unknown: %q %v", res, err)
	}
	// 自己中斷後 Garmin 回送的最後一則通知：連線已被清掉 → 冪等忽略、不發信、不再清理
	e2, c := connectedEnv(t)
	rec := e2.req("POST", "/disconnect", tUserA, "")
	if rec.Code != 200 {
		t.Fatalf("disconnect: %d", rec.Code)
	}
	purges := len(e2.store.cs.purges)
	res, err = e2.h.processDeregEvent(context.Background(), deregEv(c.ProviderUserID, 0))
	if err != nil || res != "unknown_user" || len(e2.store.cs.purges) != purges || len(e2.mailer.list()) != 0 {
		t.Fatalf("echo of our own dereg: %q %v purges=%d mails=%v", res, err, len(e2.store.cs.purges), e2.mailer.list())
	}
}

func TestDereg_MailOnlyWhenNotSelfInitiated(t *testing.T) {
	// 使用者在 Garmin 端移除：發站內信
	e, c := lifecycleEnv(t)
	e.api.revoked = true
	if res, err := e.h.processDeregEvent(context.Background(), deregEv(c.ProviderUserID, 0)); err != nil || res != "purged" {
		t.Fatalf("%q %v", res, err)
	}
	mails := e.mailer.list()
	if len(mails) != 1 || !strings.HasPrefix(mails[0], tUserA+"|") || !strings.Contains(mails[0], garminMailDeregTitle) {
		t.Fatalf("mails: %v", mails)
	}
	// 自己剛按了中斷（selfdereg 標記存在）但清理尚未完成就收到 dereg：不發信
	e2, c2 := lifecycleEnv(t)
	e2.api.revoked = true
	_ = e2.h.kv.Set(context.Background(), "garmin:selfdereg:"+tUserA, "1", time.Minute)
	if res, err := e2.h.processDeregEvent(context.Background(), deregEv(c2.ProviderUserID, 0)); err != nil || res != "purged" {
		t.Fatalf("%q %v", res, err)
	}
	if len(e2.mailer.list()) != 0 {
		t.Fatalf("self-initiated disconnect must not mail the user: %v", e2.mailer.list())
	}
}

func TestDereg_DecryptFailureIsNotConfirmation(t *testing.T) {
	// 取連線時解密失敗（金鑰缺失）→ 暫時性錯誤，絕不當成撤銷確認
	e, c := lifecycleEnv(t)
	st := &failingConnStore{fakeGarminStore: e.store}
	e.h.store = st
	res, err := e.h.processDeregEvent(context.Background(), deregEv(c.ProviderUserID, 0))
	if err == nil || res != "" {
		t.Fatalf("decrypt failure: %q %v", res, err)
	}
	assertPurged(t, e, "decrypt failure", false)
}

type failingConnStore struct{ *fakeGarminStore }

func (f *failingConnStore) GetGarminDirectByUserID(ctx context.Context, gid string, withTokens bool) (*garminConn, error) {
	if withTokens {
		return nil, errors.New("decrypt token: key missing")
	}
	return f.fakeGarminStore.GetGarminDirectByUserID(ctx, gid, withTokens)
}

// 經由框架：dereg 推送 → 事件 → 確認撤銷 → 清理，事件 done/purged；token 仍有效則事件進入 recheck（error、約 10 分鐘後），不 dead。
func TestDereg_ThroughPipeline(t *testing.T) {
	e, c := lifecycleEnv(t)
	push := map[string]any{"deregistrations": []map[string]any{{"userId": c.ProviderUserID}}}
	if rec := e.postJSON("deregistrations", push); rec.Code != 200 {
		t.Fatalf("push: %d", rec.Code)
	}
	deadline := time.Now().Add(3 * time.Second)
	var row *fakeEvRow
	for time.Now().Before(deadline) {
		if rows := e.store.rows(); len(rows) == 1 && rows[0].Status == garminStError {
			row = rows[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if row == nil {
		t.Fatalf("event should be waiting for a recheck: %+v", e.store.rows())
	}
	if row.Attempts != 1 || row.LastError != "recheck" || time.Until(row.Next) < 9*time.Minute || time.Until(row.Next) > 11*time.Minute {
		t.Fatalf("recheck scheduling: %+v (next in %s)", row, time.Until(row.Next))
	}
	assertPurged(t, e, "alive", false)
	// 之後 Garmin 端確實撤銷：事件到期、被掃描器重驗 → 清理
	e.api.mu.Lock()
	e.api.revoked = true
	e.api.mu.Unlock()
	e.store.mu.Lock()
	e.store.events[row.ID].Next = time.Now().Add(-time.Second)
	e.store.mu.Unlock()
	if n := e.h.SweepPending(context.Background()); n != 1 {
		t.Fatalf("sweep: %d", n)
	}
	assertPurged(t, e, "after revoke", true)
}

// --- 權限異動（R-C2）---

func TestPermission_PauseResumeAndMail(t *testing.T) {
	e, c := lifecycleEnv(t)
	// 使用者在 Connect 關閉分享：API 當下權限不含 ACTIVITY_EXPORT
	e.api.perms = []string{"HISTORICAL_DATA_EXPORT"}
	res, err := e.h.processPermissionEvent(context.Background(), permEv(c.ProviderUserID))
	if err != nil || res != "paused" {
		t.Fatalf("pause: %q %v", res, err)
	}
	if got := e.readConn(tUserA); got.Scope != "HISTORICAL_DATA_EXPORT" || !got.paused() {
		t.Fatalf("scope must follow the API: %+v", got)
	}
	if mails := e.mailer.list(); len(mails) != 1 || !strings.Contains(mails[0], garminMailPauseTitle) {
		t.Fatalf("one pause mail expected: %v", mails)
	}
	// 同樣的事件再來一次：不重複發信
	if res, _ := e.h.processPermissionEvent(context.Background(), permEv(c.ProviderUserID)); res != "unchanged" || len(e.mailer.list()) != 1 {
		t.Fatalf("repeat: %q mails=%d", res, len(e.mailer.list()))
	}
	// 重新開啟：恢復
	e.api.perms = []string{"ACTIVITY_EXPORT", "HISTORICAL_DATA_EXPORT"}
	if res, err := e.h.processPermissionEvent(context.Background(), permEv(c.ProviderUserID)); err != nil || res != "resumed" {
		t.Fatalf("resume: %q %v", res, err)
	}
	if got := e.readConn(tUserA); got.paused() || !got.hasPermission("ACTIVITY_EXPORT") {
		t.Fatalf("resumed: %+v", got)
	}
	// 從頭到尾沒有刪任何資料
	assertPurged(t, e, "permission events", false)
}

func TestPermission_ForgedPayloadAndOutOfOrderUseAPITruth(t *testing.T) {
	e, c := lifecycleEnv(t)
	// 偽造事件：payload 宣稱權限是空的，但 API 說有 ACTIVITY_EXPORT → 不暫停
	forged := garminEvent{ID: "f", EventType: garminEvPermission, ProviderUserID: c.ProviderUserID, Payload: []byte(`{"permissions":[],"changeTimeInSeconds":1790000000}`)}
	res, err := e.h.processPermissionEvent(context.Background(), forged)
	if err != nil || res != "unchanged" || e.readConn(tUserA).paused() {
		t.Fatalf("forged event must not pause: %q %v", res, err)
	}
	// 亂序：先處理「開」再處理「關」——最終狀態一律等於 API 當下回傳，與事件到達順序無關
	older := garminEvent{ID: "o", EventType: garminEvPermission, ProviderUserID: c.ProviderUserID, Payload: []byte(`{"permissions":[],"changeTimeInSeconds":100}`)}
	newer := garminEvent{ID: "n", EventType: garminEvPermission, ProviderUserID: c.ProviderUserID, Payload: []byte(`{"permissions":["ACTIVITY_EXPORT"],"changeTimeInSeconds":200}`)}
	e.api.perms = []string{"ACTIVITY_EXPORT"} // 真實當下狀態：已開啟
	if _, err := e.h.processPermissionEvent(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
	if _, err := e.h.processPermissionEvent(context.Background(), older); err != nil { // 較舊的「關」事件晚到
		t.Fatal(err)
	}
	if got := e.readConn(tUserA); got.paused() || !got.hasPermission("ACTIVITY_EXPORT") {
		t.Fatalf("late 'off' event must not override the API truth: %+v", got)
	}
}

func TestPermission_ErrorPaths(t *testing.T) {
	// token 失效（401）→ 標記需重新授權，不刪資料
	e, c := lifecycleEnv(t)
	e.api.revoked = true
	res, err := e.h.processPermissionEvent(context.Background(), permEv(c.ProviderUserID))
	if err != nil || res != "skipped_unauthorized" || e.readConn(tUserA).ReauthRequiredAt == nil {
		t.Fatalf("401: %q %v", res, err)
	}
	assertPurged(t, e, "permission 401", false)
	// 暫時性
	e2, c2 := lifecycleEnv(t)
	e2.api.permsStatus = 503
	if res, err := e2.h.processPermissionEvent(context.Background(), permEv(c2.ProviderUserID)); err == nil || res != "" {
		t.Fatalf("503: %q %v", res, err)
	}
	// 需重新授權／舊世代：無法驗證
	e3, c3 := lifecycleEnv(t)
	now := time.Now()
	e3.store.mu.Lock()
	e3.store.cs.conns[tUserA].ReauthRequiredAt = &now
	e3.store.mu.Unlock()
	if res, err := e3.h.processPermissionEvent(context.Background(), permEv(c3.ProviderUserID)); err != nil || res != "skipped_unverifiable" {
		t.Fatalf("reauth: %q %v", res, err)
	}
	// 未知使用者
	if res, _ := e3.h.processPermissionEvent(context.Background(), permEv("nobody")); res != "unknown_user" {
		t.Fatalf("unknown: %q", res)
	}
}

func TestPermissionParsing_TolerantOfOddJSON(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"array":          {`["ACTIVITY_EXPORT","HEALTH_EXPORT"]`, "ACTIVITY_EXPORT,HEALTH_EXPORT"},
		"empty array":    {`[]`, ""},
		"object wrapper": {`{"permissions":["ACTIVITY_EXPORT","COURSE_IMPORT"]}`, "ACTIVITY_EXPORT,COURSE_IMPORT"},
		"odd example":    {`[ "HISTORICAL_DATA_EXPORT" "ACTIVITY_EXPORT" ]`, "HISTORICAL_DATA_EXPORT,ACTIVITY_EXPORT"},
		"garbage":        {`<html>nope</html>`, ""},
		"lowercase junk": {`["activity_export","x"]`, ""},
	} {
		got := strings.Join(garminParsePermissionList([]byte(tc.body)), ",")
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// --- PurgeUser ---

func TestPurgeUser_ResultAndLocalCleanup(t *testing.T) {
	e, c := lifecycleEnv(t)
	e.store.cs.purgeActivities = 5
	k := "purge-ev"
	e.store.InsertGarminEvents(context.Background(), []garminEventIn{{EventType: garminEvActivity, ProviderUserID: c.ProviderUserID, DedupeKey: &k, Payload: []byte(`{}`)}})
	res, err := e.h.PurgeUser(context.Background(), tUserA)
	if err != nil || !res.HadConnection || res.DeletedActivities != 5 || res.DeletedEvents != 1 {
		t.Fatalf("purge: %+v %v", res, err)
	}
	if e.readConn(tUserA) != nil || e.store.count() != 0 {
		t.Fatal("connection and all events must be gone")
	}
	// 冪等
	res, err = e.h.PurgeUser(context.Background(), tUserA)
	if err != nil || res.HadConnection || res.DeletedActivities != 5 /* fake 的固定值 */ {
		t.Fatalf("second purge: %+v %v", res, err)
	}
	// 沒有連線列（刪帳號 SOP）：仍會清（garminUID 空，無鎖）
	if got := e.store.cs.purges[len(e.store.cs.purges)-1]; got.GarminUID != "" || got.UserID != tUserA {
		t.Fatalf("no-connection purge target: %+v", got)
	}
}

func TestPurgeUser_TriggersCompetitionRecompute(t *testing.T) {
	var mu sync.Mutex
	var got []string
	old := standingsDebounce
	standingsDebounce = 20 * time.Millisecond
	SetCompetitionRecompute(func(ctx context.Context, userID string) { mu.Lock(); got = append(got, userID); mu.Unlock() })
	t.Cleanup(func() { standingsDebounce = old; SetCompetitionRecompute(nil) })
	e, _ := lifecycleEnv(t)
	e.store.cs.purgeActivities = 2
	if _, err := e.h.PurgeUser(context.Background(), tUserA); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 1 {
			if got[0] != tUserA {
				t.Fatalf("recompute for %q", got[0])
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("deleting activities must trigger the competition standings recompute")
}

// 清理與處理中的事件序列化（R-M8）：處理持有使用者鎖時，PurgeUser 必須等；清理後遲到的事件只會是 unknown_user。
func TestPurgeUser_SerializesWithInFlightProcessing(t *testing.T) {
	e, c := lifecycleEnv(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var finished atomic.Bool
	e.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		close(started)
		<-release
		finished.Store(true)
		return garminResInserted, nil
	}
	if rec := e.postJSON("activities", synthPush(synthActivity(c.ProviderUserID, "SER-1", time.Now().Add(-time.Hour).Unix()))); rec.Code != 200 {
		t.Fatalf("push: %d", rec.Code)
	}
	<-started
	purgeDone := make(chan error, 1)
	var purgedBeforeFinish atomic.Bool
	go func() {
		_, err := e.h.PurgeUser(context.Background(), tUserA)
		if !finished.Load() {
			purgedBeforeFinish.Store(true)
		}
		purgeDone <- err
	}()
	select {
	case <-purgeDone:
		t.Fatal("PurgeUser must wait while an event for the same user is being processed")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-purgeDone; err != nil {
		t.Fatal(err)
	}
	if purgedBeforeFinish.Load() {
		t.Fatal("purge ran before the in-flight handler finished")
	}
	// 清理後遲到的活動事件：連線已不存在 → unknown_user，不匯入
	ev := storedEvent("late", baseStored())
	ev.ProviderUserID = c.ProviderUserID
	imports := e.store.importCount()
	e.h.handlers.activity = e.h.processActivityEvent
	if res, err := e.h.processActivityEvent(context.Background(), ev); err != nil || res != "unknown_user" || e.store.importCount() != imports {
		t.Fatalf("late event after purge: %q %v", res, err)
	}
}

func TestPurgeUser_LockHeldByOtherReplica_TimesOut(t *testing.T) {
	e, c := lifecycleEnv(t)
	e.h.purgeWait = 300 * time.Millisecond
	e.store.lockMu.Lock()
	e.store.lockHeld[c.ProviderUserID] = true // 另一個副本正在處理這位使用者
	e.store.lockMu.Unlock()
	_, err := e.h.PurgeUser(context.Background(), tUserA)
	if !errors.Is(err, errGarminBusy) {
		t.Fatalf("err=%v, want errGarminBusy after waiting", err)
	}
	if e.readConn(tUserA) == nil {
		t.Fatal("nothing may be deleted when the lock could not be taken")
	}
	// 鎖釋放後可以清
	e.store.lockMu.Lock()
	delete(e.store.lockHeld, c.ProviderUserID)
	e.store.lockMu.Unlock()
	if _, err := e.h.PurgeUser(context.Background(), tUserA); err != nil {
		t.Fatal(err)
	}
}

// 後台封鎖（License §5.3）：封鎖者的推送被丟棄（落地前就擋）。
func TestBlockedUserPushDropped(t *testing.T) {
	e, c := lifecycleEnv(t)
	e.store.cs.blockedGIDs[c.ProviderUserID] = true
	rec := e.postJSON("activities", synthPush(synthActivity(c.ProviderUserID, "BLK-1", time.Now().Add(-time.Hour).Unix())))
	if rec.Code != 200 || e.store.count() != 0 {
		t.Fatalf("blocked user's push must be acked and dropped: code=%d events=%d", rec.Code, e.store.count())
	}
}

// 後台封鎖（License §5.3，審查 R-M6）：封鎖清單先寫、仍呼叫 DELETE registration、再清理；之後無法重連、推送丟棄；解除後可重連。
func TestBlockUser_BlocksDeletesRegistrationPurgesAndStopsReconnect(t *testing.T) {
	e, c := lifecycleEnv(t)
	e.store.cs.purgeActivities = 3
	res, err := e.h.BlockUser(context.Background(), tUserA, "abuse report", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.RegistrationDeleted || !res.Purge.HadConnection || res.Purge.DeletedActivities != 3 {
		t.Fatalf("result: %+v", res)
	}
	if _, _, _, del := e.api.calls(); del != 1 {
		t.Fatalf("DELETE registration must still be called when blocking: %d", del)
	}
	if got := e.store.cs.callLog; len(got) < 2 || got[0] != "block" {
		t.Fatalf("the block list must be written first (before purge): %v", got)
	}
	if e.readConn(tUserA) != nil {
		t.Fatal("connection must be purged")
	}
	if len(e.mailer.list()) != 0 {
		t.Fatalf("a blocked user must not get the 'connection ended' mail: %v", e.mailer.list())
	}
	// 無法重連
	if rec := e.req("POST", "/connect", tUserA, `{"consent":true,"consent_v":"v1"}`); rec.Code != 403 {
		t.Fatalf("blocked user connect: %d, want 403", rec.Code)
	}
	// 該 Garmin userId 的推送在落地前就被丟棄
	rec := e.postJSON("activities", synthPush(synthActivity(c.ProviderUserID, "BLK-2", time.Now().Add(-time.Hour).Unix())))
	if rec.Code != 200 || e.store.count() != 0 {
		t.Fatalf("blocked garmin id push: code=%d events=%d", rec.Code, e.store.count())
	}
	// 冪等
	if _, err := e.h.BlockUser(context.Background(), tUserA, "again", ""); err != nil {
		t.Fatalf("second block: %v", err)
	}
	// 解除封鎖後可以重新連接
	ok, err := e.h.UnblockUser(context.Background(), tUserA)
	if err != nil || !ok {
		t.Fatalf("unblock: %v %v", ok, err)
	}
	if rec := e.req("POST", "/connect", tUserA, `{"consent":true,"consent_v":"v1"}`); rec.Code != 200 {
		t.Fatalf("after unblock connect: %d", rec.Code)
	}
	if ok, _ := e.h.UnblockUser(context.Background(), tUserA); ok {
		t.Fatal("second unblock should report nothing removed")
	}
}

func TestBlockUser_UserWithoutConnectionStillBlocked(t *testing.T) {
	e := newConnectEnv(t, nil)
	res, err := e.h.BlockUser(context.Background(), tUserB, "preemptive", "")
	if err != nil || res.RegistrationDeleted || res.Purge.HadConnection {
		t.Fatalf("no connection: %+v %v", res, err)
	}
	if rec := e.req("POST", "/connect", tUserB, `{"consent":true,"consent_v":"v1"}`); rec.Code != 403 {
		t.Fatalf("connect: %d", rec.Code)
	}
}
