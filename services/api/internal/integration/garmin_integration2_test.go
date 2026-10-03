//go:build integration

// WP-G3/G4/G5 的真 PG 測試（假 Garmin）：清理單一交易、統計與保活候選、端到端（Terra 舊列覆蓋→授權→推送→撤銷→清理）。
// 執行方式見 garmin_integration_test.go 檔頭。
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGarminIT_PurgeGarminUser_SingleTransaction(t *testing.T) {
	pool := garminITPool(t)
	garminITSetKey(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	uid, other := garminITUser(t, pool), garminITUser(t, pool)
	gid := "itwg-purge-" + garminITUnique()
	p := gid
	garminITCleanEvents(t, pool, p)
	if _, err := repo.SaveGarmin(ctx, garminITSaveInput(uid, gid)); err != nil {
		t.Fatal(err)
	}
	str := func(s string) *string { return &s }
	now := time.Now().Add(-48 * time.Hour)
	u := garminITUnique()
	g1 := garminITInsertActivity(t, pool, uid, str("garmin"), str("gc:"+u+"1"), now, false, "", nil)
	g2 := garminITInsertActivity(t, pool, uid, str("garmin"), str("legacy-terra-"+u), now.Add(time.Hour), false, "", nil)
	keep := garminITInsertActivity(t, pool, uid, str("strava"), str("s-"+u), now.Add(2*time.Hour), false, "", nil)
	dupGPS := garminITInsertActivity(t, pool, uid, nil, nil, now.Add(3*time.Hour), true, "multi_device_duplicate", &g1)
	otherG := garminITInsertActivity(t, pool, other, str("garmin"), str("gc:"+u+"9"), now, false, "", nil)
	// 里程發放紀錄：兩筆屬於被刪的 garmin 活動、一筆屬於 Strava 活動、一筆無關聯
	for _, r := range []struct {
		act  *string
		dist float64
	}{{&g1, 5.25}, {&g2, 3.5}, {&keep, 7.75}, {nil, 1.5}} {
		if _, err := pool.Exec(ctx, `INSERT INTO mileage_exp_events (user_id, exp_amount, km_added, distance_km, recorded_at, activity_id, dp_amount)
			VALUES ($1, 10, 5, $2, now(), $3::uuid, 2)`, uid, r.dist, r.act); err != nil {
			t.Fatal(err)
		}
	}
	// 事件列（各種狀態）＋別人的事件
	for i, st := range []string{"pending", "done", "dead", "error"} {
		if _, err := pool.Exec(ctx, `INSERT INTO integration_events (provider, event_type, provider_user_id, dedupe_key, payload, status) VALUES ('garmin','activity',$1,$2,'{}',$3)`,
			gid, fmt.Sprintf("%s-ev%d", p, i), st); err != nil {
			t.Fatal(err)
		}
	}
	otherEv := p + "-other"
	if _, err := pool.Exec(ctx, `INSERT INTO integration_events (provider, event_type, provider_user_id, dedupe_key, payload) VALUES ('garmin','activity',$1,$2,'{}')`, otherEv, otherEv+"-k"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM integration_events WHERE provider_user_id=$1`, otherEv)
	})
	// 偏好來源與已發獎勵帳本
	if _, err := pool.Exec(ctx, `INSERT INTO user_profiles (user_id, preferred_data_source) VALUES ($1,'garmin') ON CONFLICT (user_id) DO UPDATE SET preferred_data_source='garmin'`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO external_award_ledger (user_id, source, ext_hash) VALUES ($1,'garmin','hash-1')`, uid); err != nil {
		t.Fatal(err)
	}

	res, err := repo.PurgeGarminUser(ctx, uid, gid)
	if err != nil {
		t.Fatal(err)
	}
	if !res.HadConnection || res.DeletedActivities != 2 || res.DeletedEvents != 4 || res.AnonymizedEvents != 2 {
		t.Fatalf("result: %+v", res)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM user_integrations WHERE user_id=$1 AND provider='garmin'`, uid).Scan(&n)
	if n != 0 {
		t.Fatal("connection must be deleted")
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM activities WHERE user_id=$1 AND source='garmin'`, uid).Scan(&n)
	if n != 0 {
		t.Fatal("garmin activities (including legacy rows) must be deleted")
	}
	for _, id := range []string{keep, dupGPS, otherG} {
		var ex bool
		_ = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM activities WHERE id=$1::uuid)`, id).Scan(&ex)
		if !ex {
			t.Fatalf("activity %s must survive", id)
		}
	}
	var flagged bool
	_ = pool.QueryRow(ctx, `SELECT flagged FROM activities WHERE id=$1::uuid`, dupGPS).Scan(&flagged)
	if flagged {
		t.Fatal("the duplicate marker caused by the deleted garmin row must be cleared")
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM integration_events WHERE provider_user_id=$1`, gid).Scan(&n)
	if n != 0 {
		t.Fatalf("events of any status must be deleted, %d left", n)
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM integration_events WHERE provider_user_id=$1`, otherEv).Scan(&n)
	if n != 1 {
		t.Fatal("another user's events must not be touched")
	}
	// mileage_exp_events 匿名化：garmin 的兩筆去掉開始時間、活動連結、距離明細（distance_km=km_added），帳務欄位保留；其他不動
	rows, err := pool.Query(ctx, `SELECT activity_id IS NULL, recorded_at IS NULL, distance_km::float8, km_added, exp_amount, dp_amount FROM mileage_exp_events WHERE user_id=$1 ORDER BY distance_km`, uid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type ev struct {
		actNull, recNull bool
		dist             float64
		km, exp, dp      int
	}
	var got []ev
	for rows.Next() {
		var e ev
		_ = rows.Scan(&e.actNull, &e.recNull, &e.dist, &e.km, &e.exp, &e.dp)
		got = append(got, e)
	}
	if len(got) != 4 {
		t.Fatalf("mileage events: %+v", got)
	}
	anon := 0
	for _, e := range got {
		if e.actNull && e.recNull && e.dist == 5 && e.km == 5 && e.exp == 10 && e.dp == 2 {
			anon++
		}
	}
	if anon != 2 {
		t.Fatalf("exactly the two garmin-linked events must be anonymized (distance replaced by km_added, ledger columns kept): %+v", got)
	}
	var stravaKept, plainKept bool
	for _, e := range got {
		if e.dist == 7.75 && !e.actNull && !e.recNull {
			stravaKept = true
		}
		if e.dist == 1.5 && e.actNull && !e.recNull {
			plainKept = true
		}
	}
	if !stravaKept || !plainKept {
		t.Fatalf("non-garmin mileage events must be untouched: %+v", got)
	}
	// 偏好來源重設、已發獎勵帳本不動
	var pref string
	_ = pool.QueryRow(ctx, `SELECT preferred_data_source FROM user_profiles WHERE user_id=$1`, uid).Scan(&pref)
	if pref != "gps" {
		t.Fatalf("preferred source must reset to gps, got %q", pref)
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM external_award_ledger WHERE user_id=$1 AND source='garmin'`, uid).Scan(&n)
	if n != 1 {
		t.Fatal("external_award_ledger must not be touched")
	}
	// 冪等
	if res2, err := repo.PurgeGarminUser(ctx, uid, gid); err != nil || res2.HadConnection || res2.DeletedActivities != 0 {
		t.Fatalf("second purge: %+v %v", res2, err)
	}
}

func TestGarminIT_ConnStatsAndKeepaliveCandidates(t *testing.T) {
	pool := garminITPool(t)
	garminITSetKey(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	gen := garminAppGeneration("itwg-client-" + garminITUnique())
	before, err := repo.GarminConnStats(ctx, gen)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(scope, issuer string, refreshIn time.Duration) (string, string) {
		uid := garminITUser(t, pool)
		gid := "itwg-st-" + garminITUnique()
		in := garminITSaveInput(uid, gid)
		in.Scope, in.Issuer, in.RefreshExpiresAt = scope, issuer, time.Now().Add(refreshIn)
		if _, err := repo.SaveGarmin(ctx, in); err != nil {
			t.Fatal(err)
		}
		return uid, gid
	}
	okUID, _ := mk("ACTIVITY_EXPORT", gen, 5*24*time.Hour)              // 健康、5 天後到期（保活候選）
	_, _ = mk("ACTIVITY_EXPORT", gen, 60*24*time.Hour)                  // 健康、不需保活
	pausedUID, _ := mk("HEALTH_EXPORT", gen, 60*24*time.Hour)           // 暫停
	_, _ = mk("ACTIVITY_EXPORT", "garmin-app:oldgen00", 5*24*time.Hour) // 舊世代：需重新授權、不是保活候選
	reUID, _ := mk("ACTIVITY_EXPORT", gen, 5*24*time.Hour)
	if _, err := pool.Exec(ctx, `UPDATE user_integrations SET reauth_required_at=now() WHERE user_id=$1 AND provider='garmin'`, reUID); err != nil {
		t.Fatal(err)
	}
	// 暫停但 24h 內仍有推送（異常）、一般有同步
	if _, err := pool.Exec(ctx, `UPDATE user_integrations SET last_synced_at=now() WHERE user_id IN ($1,$2) AND provider='garmin'`, pausedUID, okUID); err != nil {
		t.Fatal(err)
	}
	after, err := repo.GarminConnStats(ctx, gen)
	if err != nil {
		t.Fatal(err)
	}
	if d := after.Connected - before.Connected; d != 5 {
		t.Errorf("connected delta %d, want 5", d)
	}
	if d := after.Active24h - before.Active24h; d != 2 {
		t.Errorf("active24h delta %d, want 2", d)
	}
	if d := after.NeedsReauth - before.NeedsReauth; d != 2 { // 舊世代＋已標記
		t.Errorf("needs_reauth delta %d, want 2", d)
	}
	if d := after.Paused - before.Paused; d != 1 {
		t.Errorf("paused delta %d, want 1", d)
	}
	if d := after.PausedButActive - before.PausedButActive; d != 1 {
		t.Errorf("paused-but-active delta %d, want 1", d)
	}
	cands, err := repo.GarminKeepaliveCandidates(ctx, gen, 14*24*time.Hour, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range cands {
		if c.UserID == okUID {
			found = true
		}
		if c.UserID == reUID {
			t.Fatal("connections flagged for re-authorization must not be keepalive candidates")
		}
		if c.Issuer != gen {
			t.Fatalf("old-generation connection returned as candidate: %s", c.Issuer)
		}
		if c.AccessToken != "" || c.RefreshToken != "" {
			t.Fatal("candidates must not carry tokens")
		}
	}
	if !found {
		t.Fatal("the connection whose refresh token expires in 5 days must be a keepalive candidate")
	}
}

// 端到端（真 PG＋假 Garmin）：舊 Terra 列覆蓋（保留 floor）→ 授權 → 推送入庫 → 舊孿生探測 → 撤銷實測 → 清理。
func TestGarminIT_EndToEndConnectPushDeregPurge(t *testing.T) {
	pool := garminITPool(t)
	garminITSetKey(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	uid := garminITUser(t, pool)
	api := newFakeGarmin()
	api.userID = "itwg-e2e-" + garminITUnique()
	garminITCleanEvents(t, pool, api.userID)

	// 11 位 Terra-Garmin 使用者的現況：舊 Terra 列（floor＝10 天前）＋兩筆舊 Terra 活動
	if err := repo.SaveTerra(ctx, &Connection{UserID: uid, Provider: "garmin", ProviderUserID: "55555555-5555-4555-8555-555555555555", ExpiresAt: time.Now().AddDate(100, 0, 0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE user_integrations SET created_at = now() - interval '10 days' WHERE user_id=$1 AND provider='garmin'`, uid); err != nil {
		t.Fatal(err)
	}
	var floor time.Time
	_ = pool.QueryRow(ctx, `SELECT created_at FROM user_integrations WHERE user_id=$1 AND provider='garmin'`, uid).Scan(&floor)
	str := func(s string) *string { return &s }
	terraStart := time.Now().Add(-5 * 24 * time.Hour)
	garminITInsertActivity(t, pool, uid, str("garmin"), str("terra-bare-sum-1"), terraStart, false, "", nil)
	garminITInsertActivity(t, pool, uid, str("garmin"), str("garmin:"+fmt.Sprint(terraStart.Add(24*time.Hour).Unix())), terraStart.Add(24*time.Hour), false, "", nil)

	mailer := &fakeMailer{}
	h := NewGarminHandler(GarminConfig{ClientID: "client-test", ClientSecret: "secret-test", WebhookToken: testGarminToken},
		GarminDeps{DB: pool, JWTSecret: "jwt-secret-test", FrontendURL: "https://app.test", RequireAuth: testAuthMW, Mailer: mailer,
			HTTPClient: &http.Client{Transport: api}})
	h.entryFor = func(ctx context.Context, userID string) (string, error) { return "shown", nil }
	h.alertFn = func(kind, title, detail string) {}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		h.Drain(c)
	})
	router := h.Router()
	do := func(method, path, user, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if user != "" {
			r.Header.Set("X-Test-User", user)
		}
		for _, c := range cookies {
			r.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, r)
		return rec
	}

	// 連接：覆蓋 Terra 列，保留 floor
	rec := do(http.MethodPost, "/connect", uid, `{"consent":true,"consent_v":"v1"}`)
	if rec.Code != 200 {
		t.Fatalf("connect: %d %s", rec.Code, rec.Body.String())
	}
	var cn struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &cn)
	u, _ := url.Parse(cn.URL)
	var nonce *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == garminNonceCookie {
			nonce = c
		}
	}
	code := api.newCode(u.Query().Get("code_challenge"))
	rec = do(http.MethodGet, "/callback?code="+code+"&state="+url.QueryEscape(u.Query().Get("state")), "", "", nonce)
	if st, reason := redirectReason(rec); st != "connected" {
		t.Fatalf("callback: %q %q", st, reason)
	}
	c, err := repo.GetGarminByUser(ctx, uid, true)
	if err != nil || c == nil {
		t.Fatalf("connection: %v %v", c, err)
	}
	if c.Via != "direct" || c.ProviderUserID != api.userID || !c.ConnectedAt.Equal(floor) || c.ConsentVersion != "v1" ||
		c.ConsentAt == nil || c.AccessToken != "acc-1" || c.Issuer != garminAppGeneration("client-test") {
		t.Fatalf("connection after overwrite: %+v", c)
	}
	// /status：已連接、legacy_terra=false
	rec = do(http.MethodGet, "/status", uid, "")
	var stt map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &stt)
	if stt["connected"] != true || stt["legacy_terra"] != false || stt["needs_reauth"] != false || stt["paused"] != false {
		t.Fatalf("status: %v", stt)
	}

	// 推送：①連接前（floor 之前）的活動略過 ②舊孿生命中 ③正常活動入庫（開始時間＝recorded_at）
	push := func(acts ...map[string]any) {
		b, _ := json.Marshal(synthPush(acts...))
		if rec := do(http.MethodPost, "/webhook/"+testGarminToken+"/activities", "", string(b)); rec.Code != 200 {
			t.Fatalf("push: %d", rec.Code)
		}
	}
	waitDone := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var n int
			_ = pool.QueryRow(ctx, `SELECT count(*) FROM integration_events WHERE provider_user_id=$1 AND status='done'`, api.userID).Scan(&n)
			if n >= want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("events not done (%d expected)", want)
	}
	good := time.Now().Add(-3 * time.Hour).Unix()
	pre := synthActivity(api.userID, "pre-connect", floor.Add(-2*time.Hour).Unix())
	twin := synthActivity(api.userID, "terra-bare-sum-1", terraStart.Unix()) // 與舊 Terra 列同 summaryId
	live := synthActivity(api.userID, "real-1", good)
	live["deviceName"] = "Forerunner 265"
	push(pre, twin, live)
	waitDone(3)
	rows, _ := pool.Query(ctx, `SELECT external_id, recorded_at, COALESCE(device_name,''), distance_km::float8 FROM activities WHERE user_id=$1 AND source='garmin' AND external_id LIKE 'gc:%'`, uid)
	var gc []string
	for rows.Next() {
		var ext, dev string
		var at time.Time
		var km float64
		_ = rows.Scan(&ext, &at, &dev, &km)
		gc = append(gc, ext)
		if ext != "gc:real-1" || at.Unix() != good || dev != "Forerunner 265" || km != 5.001 {
			t.Errorf("direct activity: ext=%s recorded_at=%d (want start %d) device=%q km=%v", ext, at.Unix(), good, dev, km)
		}
	}
	rows.Close()
	if len(gc) != 1 {
		t.Fatalf("only the live activity may be imported as gc:*, got %v", gc)
	}
	resMap := map[string]string{}
	rr, _ := pool.Query(ctx, `SELECT dedupe_key, COALESCE(result,'') FROM integration_events WHERE provider_user_id=$1`, api.userID)
	for rr.Next() {
		var k, r string
		_ = rr.Scan(&k, &r)
		resMap[k] = r
	}
	rr.Close()
	if resMap["act:"+api.userID+":pre-connect"] != "skipped_before_connect" || resMap["act:"+api.userID+":terra-bare-sum-1"] != "exists" || resMap["act:"+api.userID+":real-1"] != "inserted" {
		t.Fatalf("event results: %v", resMap)
	}
	c, _ = repo.GetGarminByUser(ctx, uid, false)
	if c.LastSyncedAt == nil {
		t.Fatal("last_synced_at must be updated by pushes")
	}

	// 撤銷：Garmin 端已撤銷 → 實測 401 → 清理（全部 source='garmin' 活動，含舊 Terra 列）＋站內信
	api.mu.Lock()
	api.revoked = true
	api.mu.Unlock()
	b, _ := json.Marshal(map[string]any{"deregistrations": []map[string]any{{"userId": api.userID}}})
	if rec := do(http.MethodPost, "/webhook/"+testGarminToken+"/deregistrations", "", string(b)); rec.Code != 200 {
		t.Fatalf("dereg push: %d", rec.Code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, _ := repo.GetGarminByUser(ctx, uid, false); c == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if c, _ := repo.GetGarminByUser(ctx, uid, false); c != nil {
		t.Fatalf("connection must be purged after a confirmed revocation: %+v", c)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM activities WHERE user_id=$1 AND source='garmin'`, uid).Scan(&n)
	if n != 0 {
		t.Fatalf("all garmin activities (including legacy Terra rows) must be deleted, %d left", n)
	}
	if ms := mailer.list(); len(ms) != 1 || !strings.Contains(ms[0], garminMailDeregTitle) {
		t.Fatalf("in-app mail: %v", ms)
	}
	// 之後同一個 userId 的推送：未知使用者，丟棄不落地
	push(synthActivity(api.userID, "after-purge", time.Now().Add(-time.Hour).Unix()))
	time.Sleep(100 * time.Millisecond)
	var after int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM integration_events WHERE provider_user_id=$1`, api.userID).Scan(&after)
	if after != 0 {
		t.Fatalf("events after purge: %d, want 0 (cleared with the user, later pushes dropped as unknown)", after)
	}
}
