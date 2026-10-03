//go:build integration

// 後台 Garmin 管理 API 對真實 Postgres 的測試（已套用 migrations 001–199）。執行方式見 garmin_integration_test.go 檔頭：
//
//	DOR_TEST_DATABASE_URL=postgres://…/dor_it_wg2?sslmode=disable go test -tags=integration ./internal/integration/ -run GarminAdminIT
//
// 全部使用合成資料與假 Garmin（in-process RoundTripper，不開本機 TCP）；每個測試自建資料並在結束時清掉。
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/adminacct"
)

func garminITAdminUser(t *testing.T, pool *pgxpool.Pool, role string, super bool) string {
	t.Helper()
	u := garminITUnique()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, handle, name, password_hash, role, is_super_admin) VALUES ($1,$2,'GarminAdminIT','x',$3,$4) RETURNING id::text`,
		"garminadminit-"+u+"@example.invalid", "garminadminit_"+u, role, super).Scan(&id)
	if err != nil {
		t.Fatalf("create %s user: %v", role, err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id) })
	return id
}

// garminITCleanUser 清掉測試使用者的 Garmin 相關資料（在 garminITUser 的清理之前執行：t.Cleanup 後進先出）。
func garminITCleanUser(t *testing.T, pool *pgxpool.Pool, uid string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM mileage_exp_events WHERE user_id=$1`, uid)
		_, _ = pool.Exec(ctx, `DELETE FROM external_award_ledger WHERE user_id=$1`, uid)
		_, _ = pool.Exec(ctx, `DELETE FROM garmin_blocklist WHERE user_id=$1`, uid)
		_, _ = pool.Exec(ctx, `DELETE FROM user_integrations WHERE user_id=$1`, uid)
		_, _ = pool.Exec(ctx, `DELETE FROM user_profiles WHERE user_id=$1`, uid)
	})
}

type garminITAdminEnv struct {
	pool    *pgxpool.Pool
	repo    *Repository
	h       *GarminHandler
	api     *fakeGarmin
	handler http.Handler
}

func newGarminITAdminEnv(t *testing.T) *garminITAdminEnv {
	t.Helper()
	pool := garminITPool(t)
	garminITSetKey(t)
	api := newFakeGarmin()
	h := NewGarminHandler(
		GarminConfig{ClientID: "client-test", ClientSecret: "secret-test", WebhookToken: testGarminToken},
		GarminDeps{DB: pool, JWTSecret: "jwt-secret-test", FrontendURL: "https://app.test",
			HTTPClient: &http.Client{Transport: api, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}})
	h.alertFn = func(kind, title, detail string) {}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.Drain(c)
	})
	return &garminITAdminEnv{pool: pool, repo: NewRepository(pool), h: h, api: api, handler: admAuthMW(h.AdminRouter())}
}

func (e *garminITAdminEnv) do(method, path, uid, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if uid != "" {
		r.Header.Set("X-Test-User", uid)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, r)
	return rec
}

// connect 以假 Garmin 發出的 token 建立一條直連連線（App 世代與 handler 一致，匯入起算點往前推 30 天）。
func (e *garminITAdminEnv) connect(t *testing.T, uid, gid string) {
	t.Helper()
	e.api.mu.Lock()
	a, r := e.api.issueTokens()
	e.api.mu.Unlock()
	in := garminITSaveInput(uid, gid)
	in.AccessToken, in.RefreshToken, in.Issuer = a, r, e.h.generation()
	in.ExpiresAt = time.Now().Add(time.Hour)
	if _, err := e.repo.SaveGarmin(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	garminITBackdate(t, e.pool, uid)
}

func (e *garminITAdminEnv) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

// insertEvent 以真實的 INSERT（InsertGarminEvents）落地一筆活動事件，再用 SQL 把它設成指定狀態。
func (e *garminITAdminEnv) insertEvent(t *testing.T, gid, summary string, start int64, status string, attempts int, lastErr string) string {
	t.Helper()
	payload, _ := json.Marshal(garminStoredActivity{SummaryID: summary, ActivityType: "RUNNING", StartTime: start, Duration: 1800, Distance: 5000, DeviceName: "Forerunner 265"})
	key := "act:" + summary
	got, err := e.repo.InsertGarminEvents(context.Background(), []garminEventIn{{EventType: garminEvActivity, ProviderUserID: gid, DedupeKey: &key, Payload: payload}})
	if err != nil || len(got) != 1 {
		t.Fatalf("insert event: %v (%d rows)", err, len(got))
	}
	id := got[0].ID
	if _, err := e.pool.Exec(context.Background(), `UPDATE integration_events
		SET status=$2::text, attempts=$3::int, last_error=NULLIF($4::text,''), next_attempt_at=now() - interval '1 hour',
		    processed_at = CASE WHEN $2::text IN ('done','dead') THEN now() ELSE NULL END,
		    result = CASE WHEN $2::text='done' THEN 'inserted' ELSE NULL END
		WHERE id=$1::uuid`, id, status, attempts, lastErr); err != nil {
		t.Fatal(err)
	}
	return id
}

func admITJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("not a JSON object (%d): %q", rec.Code, rec.Body.String())
	}
	return m
}

// --- 守衛對真實 users 表 ---

func TestGarminAdminIT_GuardUsesRealUsersTable(t *testing.T) {
	e := newGarminITAdminEnv(t)
	super := garminITAdminUser(t, e.pool, "admin", true)
	plainAdmin := garminITAdminUser(t, e.pool, "admin", false)
	member := garminITAdminUser(t, e.pool, "user", false)
	oddFlag := garminITAdminUser(t, e.pool, "user", true) // is_super_admin=true 但不是 admin 角色：不得放行
	for name, tc := range map[string]struct {
		uid  string
		want int
	}{
		"super admin":                       {super, 200},
		"admin without super":               {plainAdmin, 403},
		"ordinary member":                   {member, 403},
		"super flag on a non-admin account": {oddFlag, 403},
		"valid uuid that is not in the DB":  {"ffffffff-0000-4000-8000-ffffffffffff", 403},
		"no login at all":                   {"", 401},
		"malformed identity":                {"not-a-uuid", 401},
	} {
		if rec := e.do("GET", "/summary", tc.uid, ""); rec.Code != tc.want {
			t.Fatalf("%s: %d, want %d (%s)", name, rec.Code, tc.want, rec.Body.String())
		}
	}
}

// --- GET /events ---

func TestGarminAdminIT_EventsListRealSQL(t *testing.T) {
	e := newGarminITAdminEnv(t)
	super := garminITAdminUser(t, e.pool, "admin", true)
	uid := garminITUser(t, e.pool)
	garminITCleanUser(t, e.pool, uid)
	gid := "itwg2-list-" + garminITUnique() + "-7e21"
	garminITCleanEvents(t, e.pool, gid)
	e.connect(t, uid, gid)
	orphanGID := "itwg2-orphan-" + garminITUnique() + "-0042"
	garminITCleanEvents(t, e.pool, orphanGID)
	base := time.Now().Add(-6 * time.Hour).Unix()
	dead := e.insertEvent(t, gid, "dead-"+garminITUnique(), base, "dead", 5, "api:500")
	done := e.insertEvent(t, gid, "done-"+garminITUnique(), base+10, "done", 0, "")
	errEv := e.insertEvent(t, gid, "err-"+garminITUnique(), base+20, "error", 2, "timeout")
	pend := e.insertEvent(t, orphanGID, "pend-"+garminITUnique(), base+30, "pending", 0, "")
	// 把落地時間排好順序（避免同一毫秒），並放一筆「別的供應商」的事件（不得出現在清單）
	for i, id := range []string{dead, done, errEv, pend} {
		if _, err := e.pool.Exec(context.Background(), `UPDATE integration_events SET received_at = now() - make_interval(mins => $2) WHERE id=$1::uuid`, id, 10-i); err != nil {
			t.Fatal(err)
		}
	}
	var foreign string
	if err := e.pool.QueryRow(context.Background(), `INSERT INTO integration_events (provider, event_type, provider_user_id, payload, status)
		VALUES ('itwg2other','activity',$1,'{"secret":"x"}','dead') RETURNING id::text`, gid).Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM integration_events WHERE id=$1::uuid`, foreign)
	})

	rec := e.do("GET", "/events?limit=200", super, "")
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, leak := range []string{"Forerunner", "summaryId", "payload", "startTimeInSeconds", gid, orphanGID, foreign} {
		if strings.Contains(body, leak) {
			t.Fatalf("response leaks %q: %s", leak, body)
		}
	}
	byID := map[string]map[string]any{}
	var order []string
	for _, raw := range admITJSON(t, rec)["events"].([]any) {
		o := raw.(map[string]any)
		byID[o["id"].(string)] = o
		order = append(order, o["id"].(string))
	}
	if _, ok := byID[foreign]; ok {
		t.Fatal("events of other providers must not be listed")
	}
	// 新到舊：pend、errEv、done、dead 的相對順序
	pos := map[string]int{}
	for i, id := range order {
		pos[id] = i
	}
	if !(pos[pend] < pos[errEv] && pos[errEv] < pos[done] && pos[done] < pos[dead]) {
		t.Fatalf("order (newest first): %v", order)
	}
	if byID[dead]["user_id"] != uid || byID[dead]["garmin_user_ref"] != "****7e21" || byID[dead]["last_error"] != "api:500" ||
		byID[dead]["status"] != "dead" || byID[dead]["attempts"].(float64) != 5 || byID[dead]["processed_at"] == nil {
		t.Fatalf("dead row: %v", byID[dead])
	}
	if byID[pend]["user_id"] != "" || byID[pend]["garmin_user_ref"] != "****0042" || byID[pend]["processed_at"] != nil {
		t.Fatalf("pending row of an unknown user: %v", byID[pend])
	}
	if byID[done]["result"] != "inserted" {
		t.Fatalf("done row: %v", byID[done])
	}
	// 篩選
	m := admITJSON(t, e.do("GET", "/events?status=dead&limit=200", super, ""))
	for _, raw := range m["events"].([]any) {
		if raw.(map[string]any)["status"] != "dead" {
			t.Fatalf("status filter leaked other statuses: %v", raw)
		}
	}
	m = admITJSON(t, e.do("GET", "/events?limit=2", super, ""))
	if got := m["events"].([]any); len(got) != 2 || got[0].(map[string]any)["id"] != pend {
		t.Fatalf("limit=2: %v", m["events"])
	}
	// 事件類型與 SQL 注入式輸入
	if rec := e.do("GET", "/events?status=dead'%20OR%20'1'='1", super, ""); rec.Code != 400 {
		t.Fatalf("injection-shaped status: %d", rec.Code)
	}
}

// --- POST /events/{id}/replay ---

func TestGarminAdminIT_ReplayProcessesForRealAndIsIdempotent(t *testing.T) {
	e := newGarminITAdminEnv(t)
	super := garminITAdminUser(t, e.pool, "admin", true)
	uid := garminITUser(t, e.pool)
	garminITCleanUser(t, e.pool, uid)
	gid := "itwg2-replay-" + garminITUnique()
	garminITCleanEvents(t, e.pool, gid)
	e.connect(t, uid, gid)
	id := e.insertEvent(t, gid, "rp-"+garminITUnique(), time.Now().Add(-4*time.Hour).Unix(), "dead", 5, "api:500")

	rec := e.do("POST", "/events/"+id+"/replay", super, "")
	if rec.Code != 200 {
		t.Fatalf("replay: %d %s", rec.Code, rec.Body.String())
	}
	m := admITJSON(t, rec)
	ev, _ := m["event"].(map[string]any)
	if m["outcome"] != "requeued" || m["previous_status"] != "dead" || m["processing_started"] != true || ev["status"] != "done" || ev["result"] != "inserted" || ev["attempts"].(float64) != 0 {
		t.Fatalf("replay response: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), gid) || strings.Contains(rec.Body.String(), "Forerunner") {
		t.Fatalf("replay response leaks: %s", rec.Body.String())
	}
	if n := e.count(t, `SELECT count(*) FROM activities WHERE user_id=$1 AND source='garmin'`, uid); n != 1 {
		t.Fatalf("activities after replay: %d, want 1", n)
	}
	var status, result string
	var attempts int
	var lastErr *string
	if err := e.pool.QueryRow(context.Background(), `SELECT status, COALESCE(result,''), attempts, last_error FROM integration_events WHERE id=$1::uuid`, id).Scan(&status, &result, &attempts, &lastErr); err != nil {
		t.Fatal(err)
	}
	if status != "done" || result != "inserted" || attempts != 0 || lastErr != nil {
		t.Fatalf("stored event: %s %s %d %v", status, result, attempts, lastErr)
	}
	awards := e.count(t, `SELECT count(*) FROM mileage_exp_events WHERE user_id=$1`, uid)

	// 再放一次：冪等，不重複匯入、不重複發獎勵
	rec = e.do("POST", "/events/"+id+"/replay", super, "")
	m = admITJSON(t, rec)
	if rec.Code != 200 || m["outcome"] != "already_done" || m["processing_started"] != false {
		t.Fatalf("second replay: %d %s", rec.Code, rec.Body.String())
	}
	if n := e.count(t, `SELECT count(*) FROM activities WHERE user_id=$1 AND source='garmin'`, uid); n != 1 {
		t.Fatalf("activities after second replay: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM mileage_exp_events WHERE user_id=$1`, uid); n != awards {
		t.Fatalf("mileage award rows changed on an idempotent replay: %d -> %d", awards, n)
	}

	// 「已確認的歸屬欄位」：會員自己的活動清單帶 source=garmin 與 device_name
	acts, err := e.repo.ListActivities(context.Background(), uid, 10)
	if err != nil || len(acts) != 1 {
		t.Fatalf("list activities: %v (%d rows)", err, len(acts))
	}
	if acts[0].Source != "garmin" || acts[0].DeviceName == nil || *acts[0].DeviceName != "Forerunner 265" {
		t.Fatalf("attribution fields: source=%q device=%v", acts[0].Source, acts[0].DeviceName)
	}
	b, _ := json.Marshal(acts[0])
	if !strings.Contains(string(b), `"source":"garmin"`) || !strings.Contains(string(b), `"device_name":"Forerunner 265"`) {
		t.Fatalf("activity JSON lacks attribution fields: %s", b)
	}
}

func TestGarminAdminIT_ReplayUnknownAndNonReplayableEvents(t *testing.T) {
	e := newGarminITAdminEnv(t)
	super := garminITAdminUser(t, e.pool, "admin", true)
	gid := "itwg2-nonrep-" + garminITUnique()
	garminITCleanEvents(t, e.pool, gid)
	base := time.Now().Add(-5 * time.Hour).Unix()
	pendLeased := e.insertEvent(t, gid, "pl-"+garminITUnique(), base, "pending", 0, "")
	if _, err := e.pool.Exec(context.Background(), `UPDATE integration_events SET next_attempt_at = now() + interval '5 minutes' WHERE id=$1::uuid`, pendLeased); err != nil {
		t.Fatal(err)
	}
	doneEv := e.insertEvent(t, gid, "dn-"+garminITUnique(), base+10, "done", 0, "")
	var foreign string
	if err := e.pool.QueryRow(context.Background(), `INSERT INTO integration_events (provider, event_type, provider_user_id, payload, status, attempts)
		VALUES ('itwg2other','activity',$1,'{}','dead',5) RETURNING id::text`, gid).Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM integration_events WHERE id=$1::uuid`, foreign)
	})

	if rec := e.do("POST", "/events/ffffffff-0000-4000-8000-ffffffffffff/replay", super, ""); rec.Code != 404 {
		t.Fatalf("unknown id: %d", rec.Code)
	}
	if rec := e.do("POST", "/events/"+foreign+"/replay", super, ""); rec.Code != 404 {
		t.Fatalf("another provider's event must look like it does not exist: %d", rec.Code)
	}
	var st string
	_ = e.pool.QueryRow(context.Background(), `SELECT status FROM integration_events WHERE id=$1::uuid`, foreign).Scan(&st)
	if st != "dead" {
		t.Fatalf("another provider's event was modified: %s", st)
	}
	m := admITJSON(t, e.do("POST", "/events/"+pendLeased+"/replay", super, ""))
	if m["outcome"] != "already_pending" || m["processing_started"] != false {
		t.Fatalf("leased pending: %v", m)
	}
	m = admITJSON(t, e.do("POST", "/events/"+doneEv+"/replay", super, ""))
	if m["outcome"] != "already_done" {
		t.Fatalf("done: %v", m)
	}
	// 租約中的 pending 沒被動過
	var next time.Time
	_ = e.pool.QueryRow(context.Background(), `SELECT next_attempt_at FROM integration_events WHERE id=$1::uuid`, pendLeased).Scan(&next)
	if time.Until(next) < 4*time.Minute {
		t.Fatalf("a leased pending event must keep its lease: %s", next)
	}
}

func TestGarminAdminIT_ConcurrentReplaysRequeueOnce(t *testing.T) {
	e := newGarminITAdminEnv(t)
	super := garminITAdminUser(t, e.pool, "admin", true)
	uid := garminITUser(t, e.pool)
	garminITCleanUser(t, e.pool, uid)
	gid := "itwg2-conc-" + garminITUnique()
	garminITCleanEvents(t, e.pool, gid)
	e.connect(t, uid, gid)
	id := e.insertEvent(t, gid, "cc-"+garminITUnique(), time.Now().Add(-3*time.Hour).Unix(), "dead", 5, "api:500")

	const n = 8
	outcomes := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := e.do("POST", "/events/"+id+"/replay", super, "")
			if rec.Code != 200 {
				outcomes <- fmt.Sprintf("HTTP %d: %s", rec.Code, rec.Body.String())
				return
			}
			var m map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &m)
			o, _ := m["outcome"].(string)
			outcomes <- o
		}()
	}
	wg.Wait()
	close(outcomes)
	counts := map[string]int{}
	for o := range outcomes {
		counts[o]++
	}
	if counts["requeued"] != 1 || counts["requeued"]+counts["already_pending"]+counts["already_done"] != n {
		t.Fatalf("outcomes: %v (exactly one request may requeue)", counts)
	}
	garminITWaitEvents(t, e.pool, gid, "done", 1)
	if got := e.count(t, `SELECT count(*) FROM activities WHERE user_id=$1 AND source='garmin'`, uid); got != 1 {
		t.Fatalf("activities after %d concurrent replays: %d, want 1", n, got)
	}
	if got := e.count(t, `SELECT count(*) FROM mileage_exp_events WHERE user_id=$1`, uid); got > 1 {
		t.Fatalf("mileage award rows after concurrent replays: %d (must not double award)", got)
	}
}

// 重放時處理再次失敗：attempts 先歸零、這次失敗只算第 1 次（error，會依退避重試），不會因舊的 5 次直接變回 dead。
func TestGarminAdminIT_ReplayThatFailsAgainStartsFromZero(t *testing.T) {
	e := newGarminITAdminEnv(t)
	super := garminITAdminUser(t, e.pool, "admin", true)
	gid := "itwg2-fail-" + garminITUnique()
	garminITCleanEvents(t, e.pool, gid)
	id := e.insertEvent(t, gid, "fl-"+garminITUnique(), time.Now().Add(-3*time.Hour).Unix(), "dead", 5, "api:500")
	e.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) { return "", fmt.Errorf("transient") }
	m := admITJSON(t, e.do("POST", "/events/"+id+"/replay", super, ""))
	ev, _ := m["event"].(map[string]any)
	if m["outcome"] != "requeued" || ev["status"] != "error" || ev["attempts"].(float64) != 1 || ev["last_error"] != "error" {
		t.Fatalf("failing replay: %v", m)
	}
	var next time.Time
	_ = e.pool.QueryRow(context.Background(), `SELECT next_attempt_at FROM integration_events WHERE id=$1::uuid`, id).Scan(&next)
	if time.Until(next) < 30*time.Second {
		t.Fatalf("a failed replay must back off (next attempt in %s)", time.Until(next))
	}
}

// --- POST /users/{id}/block｜unblock ---

func TestGarminAdminIT_BlockAndUnblockRealDB(t *testing.T) {
	e := newGarminITAdminEnv(t)
	super := garminITAdminUser(t, e.pool, "admin", true)
	uid := garminITUser(t, e.pool)
	garminITCleanUser(t, e.pool, uid)
	gid := "itwg2-block-" + garminITUnique()
	garminITCleanEvents(t, e.pool, gid)
	e.connect(t, uid, gid)
	str := func(s string) *string { return &s }
	u := garminITUnique()
	start := time.Now().Add(-48 * time.Hour)
	garminITInsertActivity(t, e.pool, uid, str("garmin"), str("gc:"+u+"1"), start, false, "", nil)
	garminITInsertActivity(t, e.pool, uid, str("garmin"), str("legacy-"+u), start.Add(time.Hour), false, "", nil)
	garminITInsertActivity(t, e.pool, uid, str("strava"), str("s-"+u), start.Add(2*time.Hour), false, "", nil)
	e.insertEvent(t, gid, "bl-"+garminITUnique(), start.Unix(), "done", 0, "")
	e.insertEvent(t, gid, "bl2-"+garminITUnique(), start.Unix()+60, "dead", 5, "api:500")

	// 驗證失敗不得動到任何東西
	for name, tc := range map[string]struct{ uid, body string }{
		"missing reason":   {uid, `{}`},
		"reason too long":  {uid, `{"reason":"` + strings.Repeat("x", 121) + `"}`},
		"nonexistent user": {"ffffffff-0000-4000-8000-ffffffffffff", `{"reason":"typo"}`},
	} {
		if rec := e.do("POST", "/users/"+tc.uid+"/block", super, tc.body); rec.Code != 400 && rec.Code != 404 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if e.count(t, `SELECT count(*) FROM garmin_blocklist WHERE user_id=$1 OR user_id='ffffffff-0000-4000-8000-ffffffffffff'`, uid) != 0 ||
		e.count(t, `SELECT count(*) FROM user_integrations WHERE user_id=$1 AND provider='garmin'`, uid) != 1 {
		t.Fatal("a rejected block request changed the database")
	}

	rec := e.do("POST", "/users/"+strings.ToUpper(uid)+"/block", super, "{\"reason\":\"  濫用回報（合成） \"}")
	if rec.Code != 200 {
		t.Fatalf("block: %d %s", rec.Code, rec.Body.String())
	}
	m := admITJSON(t, rec)
	if m["blocked"] != true || m["already_blocked"] != false || m["registration_deleted"] != true || m["had_connection"] != true ||
		m["deleted_activities"].(float64) != 2 || m["deleted_events"].(float64) != 2 {
		t.Fatalf("block response: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), gid) || strings.Contains(rec.Body.String(), uid) {
		t.Fatalf("block response leaks identifiers: %s", rec.Body.String())
	}
	if _, _, _, del := e.api.calls(); del != 1 {
		t.Fatalf("DELETE registration calls: %d (blocking must still tell Garmin to remove the registration)", del)
	}
	var reason, createdBy, blockedGID string
	if err := e.pool.QueryRow(context.Background(), `SELECT reason, COALESCE(created_by::text,''), COALESCE(garmin_user_id,'') FROM garmin_blocklist WHERE user_id=$1`, uid).Scan(&reason, &createdBy, &blockedGID); err != nil {
		t.Fatalf("block row: %v", err)
	}
	if reason != "濫用回報（合成）" || createdBy != super || blockedGID != gid {
		t.Fatalf("block row: reason=%q created_by=%q garmin_user_id=%q", reason, createdBy, blockedGID)
	}
	if e.count(t, `SELECT count(*) FROM user_integrations WHERE user_id=$1 AND provider='garmin'`, uid) != 0 ||
		e.count(t, `SELECT count(*) FROM activities WHERE user_id=$1 AND source='garmin'`, uid) != 0 ||
		e.count(t, `SELECT count(*) FROM activities WHERE user_id=$1 AND source='strava'`, uid) != 1 ||
		e.count(t, `SELECT count(*) FROM integration_events WHERE provider_user_id=$1`, gid) != 0 {
		t.Fatal("block must purge the Garmin connection, Garmin activities and events (and nothing else)")
	}
	if blocked, err := e.repo.GarminBlocked(context.Background(), uid, ""); err != nil || !blocked {
		t.Fatalf("GarminBlocked: %v %v", blocked, err)
	}
	if known, _ := e.repo.KnownGarminUsers(context.Background(), []string{gid}); len(known) != 0 {
		t.Fatal("a blocked Garmin id must not be treated as a known user")
	}

	// 冪等
	m = admITJSON(t, e.do("POST", "/users/"+uid+"/block", super, `{"reason":"again"}`))
	if m["already_blocked"] != true || m["had_connection"] != false {
		t.Fatalf("second block: %v", m)
	}
	if e.count(t, `SELECT count(*) FROM garmin_blocklist WHERE user_id=$1`, uid) != 1 {
		t.Fatal("blocking twice must keep a single block row")
	}

	// 解除
	m = admITJSON(t, e.do("POST", "/users/"+uid+"/unblock", super, ""))
	if m["unblocked"] != true || e.count(t, `SELECT count(*) FROM garmin_blocklist WHERE user_id=$1`, uid) != 0 {
		t.Fatalf("unblock: %v", m)
	}
	m = admITJSON(t, e.do("POST", "/users/"+uid+"/unblock", super, ""))
	if m["unblocked"] != false {
		t.Fatalf("second unblock: %v", m)
	}
}

// --- GET /summary ---

func TestGarminAdminIT_SummaryCountsRealDB(t *testing.T) {
	e := newGarminITAdminEnv(t)
	super := garminITAdminUser(t, e.pool, "admin", true)
	read := func() map[string]any {
		rec := e.do("GET", "/summary", super, "")
		if rec.Code != 200 {
			t.Fatalf("summary: %d %s", rec.Code, rec.Body.String())
		}
		return admITJSON(t, rec)
	}
	num := func(m map[string]any, group, key string) int { return int(m[group].(map[string]any)[key].(float64)) }
	before := read()

	uid, blockedUID := garminITUser(t, e.pool), garminITUser(t, e.pool)
	garminITCleanUser(t, e.pool, uid)
	garminITCleanUser(t, e.pool, blockedUID)
	gid := "itwg2-sum-" + garminITUnique()
	garminITCleanEvents(t, e.pool, gid)
	e.connect(t, uid, gid)
	base := time.Now().Add(-2 * time.Hour).Unix()
	e.insertEvent(t, gid, "s1-"+garminITUnique(), base, "pending", 0, "")
	e.insertEvent(t, gid, "s2-"+garminITUnique(), base+1, "error", 2, "timeout")
	e.insertEvent(t, gid, "s3-"+garminITUnique(), base+2, "done", 0, "")
	e.insertEvent(t, gid, "s4-"+garminITUnique(), base+3, "dead", 5, "api:500")
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO garmin_blocklist (user_id, reason) VALUES ($1,'itwg2')`, blockedUID); err != nil {
		t.Fatal(err)
	}

	after := read()
	for _, d := range []struct {
		group, key string
		delta      int
	}{
		{"connections", "connected", 1}, {"events", "pending", 1}, {"events", "error", 1}, {"events", "done", 1}, {"events", "dead", 1},
		{"events", "received_24h", 4},
	} {
		if got := num(after, d.group, d.key) - num(before, d.group, d.key); got != d.delta {
			t.Fatalf("%s.%s changed by %d, want %d\nbefore=%v\nafter=%v", d.group, d.key, got, d.delta, before, after)
		}
	}
	if int(after["blocked"].(float64))-int(before["blocked"].(float64)) != 1 {
		t.Fatalf("blocked: %v -> %v", before["blocked"], after["blocked"])
	}
	// pending／error 的租約已被測試設成 1 小時前：兩筆都算「逾期」
	if got := num(after, "events", "pending_stale") - num(before, "events", "pending_stale"); got != 2 {
		t.Fatalf("pending_stale delta = %d, want 2", got)
	}
	if after["last_push_received_at"] == nil {
		t.Fatal("last_push_received_at must be set once events exist")
	}
	body, _ := json.Marshal(after)
	for _, leak := range []string{gid, uid, blockedUID} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("summary leaks %q: %s", leak, body)
		}
	}
}

// --- 資料層：RequeueGarminEvent 的 SQL 語意 ---

func TestGarminAdminIT_RequeueSQLSemantics(t *testing.T) {
	e := newGarminITAdminEnv(t)
	ctx := context.Background()
	gid := "itwg2-rq-" + garminITUnique()
	garminITCleanEvents(t, e.pool, gid)
	base := time.Now().Add(-2 * time.Hour).Unix()
	cases := map[string]string{}
	for _, st := range []string{"pending", "done", "error", "dead"} {
		cases[st] = e.insertEvent(t, gid, "rq-"+st+"-"+garminITUnique(), base, st, 3, "api:500")
	}
	for st, id := range cases {
		got, err := e.repo.RequeueGarminEvent(ctx, id)
		if err != nil {
			t.Fatalf("%s: %v", st, err)
		}
		wantRequeued := st == "error" || st == "dead"
		if !got.Found || got.Requeued != wantRequeued || got.Status != st {
			t.Fatalf("%s: %+v", st, got)
		}
	}
	// 重排後：pending、attempts=0、立即到期、舊錯誤與結果被清掉、processed_at 清空
	for _, st := range []string{"error", "dead"} {
		var status string
		var attempts int
		var lastErr, result *string
		var processed *time.Time
		var next time.Time
		if err := e.pool.QueryRow(ctx, `SELECT status, attempts, last_error, result, processed_at, next_attempt_at FROM integration_events WHERE id=$1::uuid`, cases[st]).
			Scan(&status, &attempts, &lastErr, &result, &processed, &next); err != nil {
			t.Fatal(err)
		}
		if status != "pending" || attempts != 0 || lastErr != nil || result != nil || processed != nil || next.After(time.Now().Add(2*time.Second)) {
			t.Fatalf("%s requeued row: %s %d %v %v %v %s", st, status, attempts, lastErr, result, processed, next)
		}
	}
	// 沒動到的：done 仍是 done、pending 仍是 pending 且 attempts 不變
	var status string
	var attempts int
	_ = e.pool.QueryRow(ctx, `SELECT status, attempts FROM integration_events WHERE id=$1::uuid`, cases["done"]).Scan(&status, &attempts)
	if status != "done" || attempts != 3 {
		t.Fatalf("done row changed: %s %d", status, attempts)
	}
	// 同一筆再 requeue：現在是 pending → 不重排
	if got, err := e.repo.RequeueGarminEvent(ctx, cases["dead"]); err != nil || !got.Found || got.Requeued || got.Status != "pending" {
		t.Fatalf("requeue again: %+v %v", got, err)
	}
	if got, err := e.repo.RequeueGarminEvent(ctx, "ffffffff-0000-4000-8000-ffffffffffff"); err != nil || got.Found {
		t.Fatalf("missing id: %+v %v", got, err)
	}
	if ev, err := e.repo.GetGarminEventAdmin(ctx, "ffffffff-0000-4000-8000-ffffffffffff"); err != nil || ev != nil {
		t.Fatalf("GetGarminEventAdmin(missing) = %v %v", ev, err)
	}
	if _, err := e.repo.RequeueGarminEvent(ctx, "not-a-uuid"); err == nil {
		t.Fatal("a malformed id is rejected by the database (the handler validates it first)")
	}
}

// --- EXPLAIN：後台事件清單的查詢不會對每一筆事件全表掃描 user_integrations ---

func TestGarminAdminIT_ListQueryUsesIndexes(t *testing.T) {
	pool := garminITPool(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, "EXPLAIN "+garminAdminEventSelect+`
		WHERE e.provider='garmin' AND ($1::text = '' OR e.status = $1::text)
		ORDER BY e.received_at DESC, e.id
		LIMIT $2`, "", 50)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		var l string
		_ = rows.Scan(&l)
		sb.WriteString(l + "\n")
	}
	plan := sb.String()
	if strings.Contains(plan, "Seq Scan on user_integrations") || !strings.Contains(plan, "Index Scan using") {
		t.Fatalf("the DOR user lookup per event row must use an index on user_integrations:\n%s", plan)
	}
}

// --- 與 main.go 建議掛法一致：admin 群組（Audit）內 r.With(RequireSuper).Mount("/admin/garmin", AdminRouter()) ---

func TestGarminAdminIT_MountWithRealAdminAcctMiddleware(t *testing.T) {
	e := newGarminITAdminEnv(t)
	super := garminITAdminUser(t, e.pool, "admin", true)
	plain := garminITAdminUser(t, e.pool, "admin", false)
	uid := garminITUser(t, e.pool)
	garminITCleanUser(t, e.pool, uid)
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM audit_logs WHERE user_id=$1::uuid OR user_id=$2::uuid`, super, plain)
	})
	gid := "itwg2-mount-" + garminITUnique()
	garminITCleanEvents(t, e.pool, gid)
	e.connect(t, uid, gid)
	evID := e.insertEvent(t, gid, "mt-"+garminITUnique(), time.Now().Add(-3*time.Hour).Unix(), "dead", 5, "api:500")

	admin := adminacct.NewHandler(e.pool)
	parent := chi.NewRouter()
	parent.Group(func(r chi.Router) {
		r.Use(admAuthMW) // 代替 RequireAuth／RequireAdmin
		r.Use(admin.Audit)
		r.With(admin.RequireSuper).Mount("/admin/garmin", e.h.AdminRouter())
	})
	call := func(method, path, who, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-Test-User", who)
		rec := httptest.NewRecorder()
		parent.ServeHTTP(rec, r)
		return rec
	}
	// 外層 RequireSuper 先擋一般管理者（它是第一道，內層守衛是第二道）
	if rec := call("POST", "/admin/garmin/users/"+uid+"/block", plain, `{"reason":"x"}`); rec.Code != 403 {
		t.Fatalf("non-super through the real RequireSuper: %d %s", rec.Code, rec.Body.String())
	}
	if e.count(t, `SELECT count(*) FROM garmin_blocklist WHERE user_id=$1`, uid) != 0 {
		t.Fatal("a rejected admin must not block anyone")
	}
	if rec := call("GET", "/admin/garmin/summary", super, ""); rec.Code != 200 {
		t.Fatalf("summary through the real middleware chain: %d %s", rec.Code, rec.Body.String())
	}
	rec := call("POST", "/admin/garmin/events/"+evID+"/replay", super, "")
	if rec.Code != 200 || admITJSON(t, rec)["outcome"] != "requeued" {
		t.Fatalf("replay through the real middleware chain: %d %s", rec.Code, rec.Body.String())
	}
	rec = call("POST", "/admin/garmin/users/"+uid+"/block", super, `{"reason":"mount test (synthetic)"}`)
	if rec.Code != 200 || admITJSON(t, rec)["blocked"] != true {
		t.Fatalf("block through the real middleware chain: %d %s", rec.Code, rec.Body.String())
	}
	// Audit 中介層把這兩個 POST 記進 audit_logs（resource＝路徑中 /admin/ 後的第一段）
	rows, err := e.pool.Query(context.Background(), `SELECT resource, COALESCE(meta->>'path',''), COALESCE((meta->>'status')::int,0)
		FROM audit_logs WHERE user_id=$1::uuid ORDER BY created_at`, super)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var res, path string
		var status int
		if err := rows.Scan(&res, &path, &status); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s|%s|%d", res, path, status))
	}
	want := []string{
		"garmin|/admin/garmin/events/" + evID + "/replay|200",
		"garmin|/admin/garmin/users/" + uid + "/block|200",
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("audit rows = %v, want %v", got, want)
	}
}
