package integration

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// seedConn 在假 store 放一條與假 Garmin 的 token 狀態一致的直連列。
func seedConn(t *testing.T, e *connectEnv, userID string, accessExpiresIn, refreshExpiresIn time.Duration) *garminConn {
	t.Helper()
	e.api.mu.Lock()
	a, r := e.api.issueTokens()
	e.api.mu.Unlock()
	rexp := time.Now().Add(refreshExpiresIn)
	gid := e.api.userID
	if userID != tUserA {
		gid += "-" + userID[:2]
	}
	return e.store.putConn(garminConn{
		UserID: userID, ProviderUserID: gid, Via: garminViaDirect, Issuer: garminAppGeneration("client-test"),
		AccessToken: a, RefreshToken: r, ExpiresAt: time.Now().Add(accessExpiresIn), RefreshExpiresAt: &rexp,
		Scope: "ACTIVITY_EXPORT", ConnectedAt: time.Now().Add(-90 * 24 * time.Hour),
	})
}

func (e *connectEnv) readConn(userID string) *garminConn { return e.store.conn(userID) }

func TestEnsureFresh_FreshTokenMakesNoCall(t *testing.T) {
	e := newConnectEnv(t, nil)
	c := seedConn(t, e, tUserA, time.Hour, 80*24*time.Hour)
	got, err := e.h.ensureFresh(context.Background(), c, false)
	if err != nil || got.AccessToken != c.AccessToken {
		t.Fatalf("fresh: %v %+v", err, got)
	}
	if tk, _, _, _ := e.api.calls(); tk != 0 {
		t.Fatalf("a fresh token must not call the token endpoint (%d)", tk)
	}
}

func TestEnsureFresh_RefreshesWithinSkewAndPersistsRotation(t *testing.T) {
	e := newConnectEnv(t, nil)
	c := seedConn(t, e, tUserA, 5*time.Minute, 80*24*time.Hour) // 在 10 分鐘 skew 內
	oldRefresh := c.RefreshToken
	got, err := e.h.ensureFresh(context.Background(), c, false)
	if err != nil {
		t.Fatal(err)
	}
	if tk, _, _, _ := e.api.calls(); tk != 1 {
		t.Fatalf("token calls = %d, want 1", tk)
	}
	f := e.api.tokenForms[0]
	if f.Get("grant_type") != "refresh_token" || f.Get("refresh_token") != oldRefresh {
		t.Fatalf("refresh form: %v", f)
	}
	if got.AccessToken == c.AccessToken || got.RefreshToken == oldRefresh || got.RefreshToken == "" {
		t.Fatalf("rotation: %+v", got)
	}
	// 新 token 已落盤（先落盤才回傳）
	if len(e.store.cs.updates) != 1 {
		t.Fatalf("updates=%d", len(e.store.cs.updates))
	}
	db := e.readConn(tUserA)
	if db.AccessToken != got.AccessToken || db.RefreshToken != got.RefreshToken {
		t.Fatalf("returned tokens must equal persisted tokens")
	}
	if d := time.Until(db.ExpiresAt); d < 23*time.Hour {
		t.Fatalf("expiry not extended: %s", d)
	}
	if d := time.Until(*db.RefreshExpiresAt); d < 89*24*time.Hour {
		t.Fatalf("refresh expiry not reset: %s", d)
	}
}

func TestEnsureFresh_SingleFlightConcurrent(t *testing.T) {
	e := newConnectEnv(t, nil)
	c := seedConn(t, e, tUserA, -time.Minute, 80*24*time.Hour)   // 已過期
	e.api.onToken = func() { time.Sleep(30 * time.Millisecond) } // 讓並發者有機會擠在一起
	var wg sync.WaitGroup
	results := make([]*garminConn, 12)
	errs := make([]error, 12)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cp := *c
			results[i], errs[i] = e.h.ensureFresh(context.Background(), &cp, false)
		}(i)
	}
	wg.Wait()
	if tk, _, _, _ := e.api.calls(); tk != 1 {
		t.Fatalf("12 concurrent callers must cause exactly 1 token call, got %d", tk)
	}
	for i := range results {
		if errs[i] != nil || results[i].AccessToken != "acc-2" {
			t.Fatalf("caller %d: %v %+v", i, errs[i], results[i])
		}
	}
}

func TestEnsureFresh_CrossProcessLockBusy_WaitsForOtherRefresher(t *testing.T) {
	e := newConnectEnv(t, nil)
	e.h.refreshWait = 2 * time.Second
	c := seedConn(t, e, tUserA, -time.Minute, 80*24*time.Hour)
	// 模擬另一個副本持有刷新鎖，過一會兒它刷新完成並釋放
	e.store.lockMu.Lock()
	e.store.lockHeld["rt:"+c.ProviderUserID] = true
	e.store.lockMu.Unlock()
	go func() {
		time.Sleep(350 * time.Millisecond)
		e.store.mu.Lock()
		cc := e.store.cs.conns[tUserA]
		cc.AccessToken, cc.RefreshToken, cc.ExpiresAt = "other-replica-access", "other-replica-refresh", time.Now().Add(24*time.Hour)
		e.store.mu.Unlock()
		e.store.lockMu.Lock()
		delete(e.store.lockHeld, "rt:"+c.ProviderUserID)
		e.store.lockMu.Unlock()
	}()
	cp := *c
	got, err := e.h.ensureFresh(context.Background(), &cp, false)
	if err != nil || got.AccessToken != "other-replica-access" {
		t.Fatalf("must use the other replica's refreshed token: %v %+v", err, got)
	}
	if tk, _, _, _ := e.api.calls(); tk != 0 {
		t.Fatalf("must not call the token endpoint itself while another process holds the lock (%d)", tk)
	}
}

func TestEnsureFresh_LockBusyForever_TransientAndNoSelfRefresh(t *testing.T) {
	e := newConnectEnv(t, nil)
	e.h.refreshWait = 400 * time.Millisecond
	c := seedConn(t, e, tUserA, -time.Minute, 80*24*time.Hour)
	e.store.lockMu.Lock()
	e.store.lockHeld["rt:"+c.ProviderUserID] = true
	e.store.lockMu.Unlock()
	cp := *c
	_, err := e.h.ensureFresh(context.Background(), &cp, false)
	if !errors.Is(err, errGarminTransient) {
		t.Fatalf("err=%v, want errGarminTransient", err)
	}
	if tk, _, _, _ := e.api.calls(); tk != 0 {
		t.Fatalf("must never refresh by itself when the lock cannot be taken (%d calls)", tk)
	}
	if e.readConn(tUserA).ReauthRequiredAt != nil {
		t.Fatal("a busy lock must not mark the user")
	}
}

func TestEnsureFresh_InvalidGrantMarksReauthAndStopsCalling(t *testing.T) {
	e := newConnectEnv(t, nil)
	c := seedConn(t, e, tUserA, -time.Minute, 80*24*time.Hour)
	e.api.mu.Lock()
	e.api.revoked = true // 使用者在 Garmin 端撤銷：refresh → invalid_grant
	e.api.mu.Unlock()
	if _, err := e.h.ensureFresh(context.Background(), c, false); !errors.Is(err, errGarminReauth) {
		t.Fatalf("err=%v, want errGarminReauth", err)
	}
	if e.readConn(tUserA).ReauthRequiredAt == nil {
		t.Fatal("invalid_grant must set reauth_required_at")
	}
	// 之後不再打 token 端點
	cur := e.readConn(tUserA)
	if _, err := e.h.ensureFresh(context.Background(), cur, false); !errors.Is(err, errGarminReauth) {
		t.Fatalf("second call: %v", err)
	}
	if tk, _, _, _ := e.api.calls(); tk != 1 {
		t.Fatalf("token calls=%d, want exactly 1 (no hammering after reauth is flagged)", tk)
	}
	// 重新授權（SaveGarmin）會清掉旗標、之後 ensureFresh 正常
	e.api.mu.Lock()
	e.api.revoked = false
	e.api.mu.Unlock()
	if _, err := e.store.SaveGarmin(context.Background(), garminSaveInput{UserID: tUserA, GarminUserID: e.api.userID, AccessToken: "n-a", RefreshToken: "n-r",
		ExpiresAt: time.Now().Add(time.Hour), RefreshExpiresAt: time.Now().Add(80 * 24 * time.Hour), Scope: "ACTIVITY_EXPORT", Issuer: garminAppGeneration("client-test")}); err != nil {
		t.Fatal(err)
	}
	if got, err := e.h.ensureFresh(context.Background(), e.readConn(tUserA), false); err != nil || got.AccessToken != "n-a" {
		t.Fatalf("after re-authorization: %v %+v", err, got)
	}
}

func TestEnsureFresh_InvalidGrantButRotatedByOtherProcess_RetriesWithNewToken(t *testing.T) {
	e := newConnectEnv(t, nil)
	c := seedConn(t, e, tUserA, -time.Minute, 80*24*time.Hour)
	first := true
	e.api.onToken = func() {
		if !first {
			return
		}
		first = false
		// 在我們送出請求的同時，另一個行程已輪替：Garmin 端目前的 refresh 變成 ref-9、DB 列也被更新
		e.api.mu.Lock()
		e.api.validRefresh = map[string]bool{"ref-9": true}
		e.api.mu.Unlock()
		e.store.mu.Lock()
		e.store.cs.conns[tUserA].RefreshToken = "ref-9"
		e.store.mu.Unlock()
	}
	got, err := e.h.ensureFresh(context.Background(), c, false)
	if err != nil {
		t.Fatalf("must retry with the rotated token and succeed: %v", err)
	}
	if tk, _, _, _ := e.api.calls(); tk != 2 {
		t.Fatalf("token calls=%d, want 2", tk)
	}
	if e.readConn(tUserA).ReauthRequiredAt != nil {
		t.Fatal("must NOT mark reauth when another process rotated the refresh token")
	}
	if got.AccessToken == "" {
		t.Fatal("no token")
	}
}

func TestEnsureFresh_InvalidClientIsConfigErrorNotUserFault(t *testing.T) {
	e := newConnectEnv(t, nil)
	c := seedConn(t, e, tUserA, -time.Minute, 80*24*time.Hour)
	e.api.tokenStatus, e.api.tokenErrCode = 401, "invalid_client"
	if _, err := e.h.ensureFresh(context.Background(), c, false); !errors.Is(err, errGarminConfig) {
		t.Fatalf("err=%v, want errGarminConfig", err)
	}
	if e.readConn(tUserA).ReauthRequiredAt != nil {
		t.Fatal("invalid_client must not mark the user")
	}
	if !contains(e.alertKinds(), "garmin_token_config") {
		t.Fatalf("expected garmin_token_config alert: %v", e.alertKinds())
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestEnsureFresh_TransientFailuresDoNotChangeState(t *testing.T) {
	for name, setup := range map[string]func(e *connectEnv){
		"500":         func(e *connectEnv) { e.api.tokenStatus = 500 },
		"429":         func(e *connectEnv) { e.api.tokenStatus = 429 },
		"network":     func(e *connectEnv) { e.api.netErr = &net.OpError{Op: "dial", Err: errors.New("connection refused")} },
		"unknown 4xx": func(e *connectEnv) { e.api.tokenStatus = 418 },
	} {
		e := newConnectEnv(t, nil)
		c := seedConn(t, e, tUserA, -time.Minute, 80*24*time.Hour)
		setup(e)
		_, err := e.h.ensureFresh(context.Background(), c, false)
		if !errors.Is(err, errGarminTransient) {
			t.Fatalf("%s: err=%v, want transient", name, err)
		}
		if e.readConn(tUserA).ReauthRequiredAt != nil || len(e.store.cs.updates) != 0 {
			t.Fatalf("%s: state must be unchanged", name)
		}
	}
	// 同一小時 ≥3 次失敗 → 告警一次
	e := newConnectEnv(t, nil)
	c := seedConn(t, e, tUserA, -time.Minute, 80*24*time.Hour)
	e.api.tokenStatus = 503
	for i := 0; i < 4; i++ {
		cp := *c
		_, _ = e.h.ensureFresh(context.Background(), &cp, false)
	}
	n := 0
	for _, k := range e.alertKinds() {
		if k == "garmin_token_refresh" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("garmin_token_refresh alerts = %d, want 1", n)
	}
}

func TestEnsureFresh_AppGenerationMismatch_NoTokenCall(t *testing.T) {
	e := newConnectEnv(t, nil)
	c := seedConn(t, e, tUserA, -time.Minute, 80*24*time.Hour)
	e.store.mu.Lock()
	e.store.cs.conns[tUserA].Issuer = "garmin-app:old00000" // Evaluation 世代
	e.store.mu.Unlock()
	cp := e.readConn(tUserA)
	if _, err := e.h.ensureFresh(context.Background(), cp, false); !errors.Is(err, errGarminGeneration) {
		t.Fatalf("err=%v, want errGarminGeneration", err)
	}
	if tk, _, _, _ := e.api.calls(); tk != 0 {
		t.Fatalf("generation mismatch must not call the token endpoint (%d)", tk)
	}
	if e.readConn(tUserA).ReauthRequiredAt == nil {
		t.Fatal("generation mismatch must flag the connection for re-authorization")
	}
	_ = c
}

func TestEnsureFresh_RefreshExpiredByClock(t *testing.T) {
	e := newConnectEnv(t, nil)
	c := seedConn(t, e, tUserA, -time.Minute, -time.Hour)
	if _, err := e.h.ensureFresh(context.Background(), c, false); !errors.Is(err, errGarminReauth) {
		t.Fatalf("err=%v", err)
	}
	if tk, _, _, _ := e.api.calls(); tk != 0 {
		t.Fatalf("no token call with an expired refresh token (%d)", tk)
	}
	if e.readConn(tUserA).ReauthRequiredAt == nil {
		t.Fatal("must flag reauth")
	}
}

func TestEnsureFresh_PersistFailureAlertsAndNeverReturnsToken(t *testing.T) {
	e := newConnectEnv(t, nil)
	c := seedConn(t, e, tUserA, -time.Minute, 80*24*time.Hour)
	e.store.cs.updateFailTimes = 3 // 三次重試都失敗
	got, err := e.h.ensureFresh(context.Background(), c, false)
	if !errors.Is(err, errGarminTransient) || got != nil {
		t.Fatalf("persist failure: %v %+v", err, got)
	}
	if !contains(e.alertKinds(), "garmin_token_persist") {
		t.Fatalf("expected garmin_token_persist alert: %v", e.alertKinds())
	}
	for _, a := range e.mails {
		if strings.Contains(a, "ref-") || strings.Contains(a, "acc-") {
			t.Fatalf("alert leaked a token: %s", a)
		}
	}
	// 前兩次失敗、第三次成功：重試成功
	e2 := newConnectEnv(t, nil)
	c2 := seedConn(t, e2, tUserA, -time.Minute, 80*24*time.Hour)
	e2.store.cs.updateFailTimes = 2
	if got, err := e2.h.ensureFresh(context.Background(), c2, false); err != nil || got == nil {
		t.Fatalf("retry should succeed: %v", err)
	}
}

func TestKeepAlive_WindowAndCooldown(t *testing.T) {
	e := newConnectEnv(t, nil)
	far := seedConn(t, e, tUserA, time.Hour, 60*24*time.Hour) // refresh 還有 60 天：不需保活
	e.h.maybeKeepAlive(context.Background(), far)
	if tk, _, _, _ := e.api.calls(); tk != 0 {
		t.Fatalf("no keepalive when refresh has >30 days left (%d)", tk)
	}
	near := seedConn(t, e, tUserB, time.Hour, 20*24*time.Hour) // 剩 20 天：強制刷新一次（即使 access 仍有效）
	e.h.maybeKeepAlive(context.Background(), near)
	if tk, _, _, _ := e.api.calls(); tk != 1 {
		t.Fatalf("keepalive should force one refresh (%d)", tk)
	}
	if d := time.Until(*e.readConn(tUserB).RefreshExpiresAt); d < 89*24*time.Hour {
		t.Fatalf("refresh expiry should be renewed: %s", d)
	}
	// 24 小時冷卻：即使 refresh 仍然很近（Garmin 若不延長壽命），也不會每次都打
	cur := e.readConn(tUserB)
	soon := time.Now().Add(10 * 24 * time.Hour)
	cur.RefreshExpiresAt = &soon
	e.h.maybeKeepAlive(context.Background(), cur)
	if tk, _, _, _ := e.api.calls(); tk != 1 {
		t.Fatalf("keepalive cooldown violated (%d token calls)", tk)
	}
}

func TestMaintainDaily_PurgesSweepsAndKeepsAlive(t *testing.T) {
	e := newConnectEnv(t, nil)
	seedConn(t, e, tUserA, time.Hour, 10*24*time.Hour) // 14 天內到期 → 保活
	seedConn(t, e, tUserB, time.Hour, 50*24*time.Hour) // 不需要
	summary, err := e.h.MaintainDaily(context.Background())
	if err != nil {
		t.Fatalf("MaintainDaily: %v", err)
	}
	if tk, _, _, _ := e.api.calls(); tk != 1 {
		t.Fatalf("keepalive token calls=%d, want 1", tk)
	}
	if e.store.purged != 1 {
		t.Fatalf("retention purge must run once (%d)", e.store.purged)
	}
	// 摘要只含數字（不含使用者識別）
	for _, bad := range []string{tUserA, tUserB, e.api.userID, "acc-", "ref-"} {
		if strings.Contains(summary, bad) {
			t.Fatalf("summary leaks %q: %s", bad, summary)
		}
	}
	// 同一天再跑：冷卻旗標擋住，不會再打 token 端點
	if _, err := e.h.MaintainDaily(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tk, _, _, _ := e.api.calls(); tk != 1 {
		t.Fatalf("second run the same day must not hit the token endpoint again (%d)", tk)
	}
	// 補掃：到期的事件被處理
	k := "maint-1"
	e.store.InsertGarminEvents(context.Background(), []garminEventIn{{EventType: garminEvActivity, ProviderUserID: e.api.userID, DedupeKey: &k, Payload: []byte(`{}`)}})
	e.store.mu.Lock()
	for _, r := range e.store.events {
		r.Next = time.Now().Add(-time.Second)
	}
	e.store.mu.Unlock()
	var ran int
	e.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) { ran++; return garminResInserted, nil }
	if _, err := e.h.MaintainDaily(context.Background()); err != nil || ran != 1 {
		t.Fatalf("daily sweep: ran=%d err=%v", ran, err)
	}
}

func TestDirectStats_OnlyCounts(t *testing.T) {
	e := newConnectEnv(t, nil)
	now := time.Now()
	seedConn(t, e, tUserA, time.Hour, 80*24*time.Hour)
	c := seedConn(t, e, tUserB, time.Hour, 80*24*time.Hour)
	e.store.mu.Lock()
	e.store.cs.conns[tUserB].Scope = "HEALTH_EXPORT" // 暫停
	e.store.cs.conns[tUserB].LastSyncedAt = &now     // 但 24h 內仍有推送（異常）
	e.store.cs.conns[tUserA].ReauthRequiredAt = &now
	e.store.mu.Unlock()
	_ = c
	e.h.count(context.Background(), "garmin:cnt:unknown:"+e.h.hourBucket(), 3, time.Hour)
	st, err := e.h.DirectStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Provider != "garmin" || st.Connected != 2 || st.NeedsReauth != 1 || st.Paused != 1 || st.UnknownUser24h != 3 {
		t.Fatalf("stats: %+v", st)
	}
	joined := strings.Join(st.Notes, "|")
	if !strings.Contains(joined, "已暫停但 24 小時內仍有推送：1") {
		t.Fatalf("expected the paused-but-active anomaly note: %v", st.Notes)
	}
	for _, n := range st.Notes {
		for _, bad := range []string{tUserA, tUserB, "@", e.api.userID} {
			if strings.Contains(n, bad) {
				t.Fatalf("note leaks identity: %s", n)
			}
		}
	}
	if e.h.Name() != "garmin" {
		t.Fatal("Name")
	}
}
