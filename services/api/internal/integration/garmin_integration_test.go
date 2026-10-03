//go:build integration

// 需要真實 Postgres（已套用 migrations 001–197＋199）才能跑；獨立 build tag，預設 `go test ./...` 不會編譯：
//
//	DOR_TEST_DATABASE_URL=postgres://…/dor_it_wg?sslmode=disable go test -tags=integration ./internal/integration/ -run GarminIT
//
// 全部使用合成資料與假 Garmin／Terra（in-process RoundTripper，不開本機 TCP）。每個測試自建使用者並在結束時清掉。
package integration

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const garminITKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff" // 合成 32 bytes hex 金鑰

func garminITPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("DOR_TEST_DATABASE_URL not set; skipping Garmin SQL integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

var garminITSeq int
var garminITMu sync.Mutex

func garminITUnique() string {
	garminITMu.Lock()
	defer garminITMu.Unlock()
	garminITSeq++
	return fmt.Sprintf("%d%03d", time.Now().UnixNano()%1_000_000_000_000, garminITSeq)
}

func garminITUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	u := garminITUnique()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, handle, name, password_hash) VALUES ($1,$2,'GarminIT','x') RETURNING id::text`,
		"garminit-"+u+"@example.invalid", "garminit_"+u).Scan(&id)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM activities WHERE user_id=$1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
	})
	return id
}

func garminITSetKey(t *testing.T) {
	t.Helper()
	t.Setenv("STRAVA_TOKEN_KEY", garminITKey)
}

func garminITSaveInput(userID, garminID string) garminSaveInput {
	return garminSaveInput{
		UserID: userID, GarminUserID: garminID,
		AccessToken: "access-" + garminID, RefreshToken: "refresh-" + garminID,
		ExpiresAt: time.Now().Add(24 * time.Hour), RefreshExpiresAt: time.Now().Add(90 * 24 * time.Hour),
		Scope: "ACTIVITY_EXPORT,HISTORICAL_DATA_EXPORT", Issuer: "garmin-app:abcd1234",
		ConsentAt: time.Now().Add(-time.Minute), ConsentVersion: "v1",
	}
}

func garminITCleanEvents(t *testing.T, pool *pgxpool.Pool, uidPrefix string) {
	t.Helper()
	clean := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM integration_events WHERE provider='garmin' AND provider_user_id LIKE $1`, uidPrefix+"%")
	}
	clean()
	t.Cleanup(clean)
}

// --- migration ---

func TestGarminIT_Migration199IdempotentWithAndWithout198(t *testing.T) {
	pool := garminITPool(t)
	ctx := context.Background()
	sqlBytes, err := os.ReadFile("../../migrations/199_garmin_direct.sql")
	if err != nil {
		t.Fatal(err)
	}
	// 198（COROS GA）的 DDL（照契約 §6）：全供應商唯一索引＋reauth_required_at
	const sql198 = `
ALTER TABLE user_integrations ADD COLUMN IF NOT EXISTS reauth_required_at TIMESTAMPTZ NULL;
CREATE UNIQUE INDEX IF NOT EXISTS user_integrations_provider_account_uniq ON user_integrations (provider, provider_user_id) WHERE provider_user_id <> '';
INSERT INTO schema_migrations (version) VALUES ('198') ON CONFLICT DO NOTHING;`
	for name, pre := range map[string]string{"without 198": "", "with 198 applied first": sql198} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if pre != "" {
			if _, err := tx.Exec(ctx, pre); err != nil {
				t.Fatalf("%s: 198: %v", name, err)
			}
		}
		for i := 1; i <= 2; i++ {
			if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
				t.Fatalf("%s: apply #%d: %v", name, i, err)
			}
		}
		if pre != "" { // 198 在 199 之後再套一次也無妨
			if _, err := tx.Exec(ctx, pre); err != nil {
				t.Fatalf("%s: 198 again: %v", name, err)
			}
		}
		var cnt int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname IN ('uq_integration_events_dedupe','uq_user_integrations_garmin_uid','uq_garmin_blocklist_user','uq_garmin_blocklist_gid')`).Scan(&cnt); err != nil || cnt != 4 {
			t.Fatalf("%s: indexes present=%d err=%v", name, cnt, err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name='user_integrations' AND column_name IN
			('issuer','last_synced_at','refresh_expires_at','reauth_required_at','consent_at','consent_version','connected_at')`).Scan(&cnt); err != nil || cnt != 7 {
			t.Fatalf("%s: user_integrations columns present=%d err=%v", name, cnt, err)
		}
		_ = tx.Rollback(ctx) // 不留下 198 的測試痕跡
	}
	// 去重索引必須是非部分索引（R-C1）
	var def string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE indexname='uq_integration_events_dedupe'`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToUpper(def), "WHERE") {
		t.Fatalf("dedupe index must be NON-partial for ON CONFLICT inference: %s", def)
	}
}

// --- integration_events ---

func TestGarminIT_EventsInsertOnConflictWithFinalSQL(t *testing.T) {
	pool := garminITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	p := "itwg-ins-" + garminITUnique() + "-"
	garminITCleanEvents(t, pool, p)
	key := func(s string) *string { k := p + s; return &k }
	ev := func(uid, k string) garminEventIn {
		return garminEventIn{EventType: garminEvActivity, ProviderUserID: p + uid, DedupeKey: key(k), Payload: []byte(`{"startTimeInSeconds":1790000000}`)}
	}

	got, err := repo.InsertGarminEvents(ctx, []garminEventIn{ev("u1", "a"), ev("u1", "b"), ev("u2", "c")})
	if err != nil {
		t.Fatalf("insert: %v (ON CONFLICT must infer the non-partial unique index)", err)
	}
	if len(got) != 3 {
		t.Fatalf("inserted %d, want 3", len(got))
	}
	for _, e := range got {
		if e.ID == "" || e.Attempts != 0 || e.EventType != garminEvActivity || len(e.Payload) == 0 {
			t.Fatalf("bad returned row: %+v", e)
		}
	}
	// 重送：RETURNING 只回新增的列，不新增列
	got, err = repo.InsertGarminEvents(ctx, []garminEventIn{ev("u1", "a"), ev("u1", "b")})
	if err != nil || len(got) != 0 {
		t.Fatalf("re-insert: got %d rows err=%v, want 0", len(got), err)
	}
	// 混合：1 新 1 舊 → 只回新的
	got, err = repo.InsertGarminEvents(ctx, []garminEventIn{ev("u1", "a"), ev("u3", "d")})
	if err != nil || len(got) != 1 || got[0].ProviderUserID != p+"u3" {
		t.Fatalf("mixed batch: %+v err=%v", got, err)
	}
	// 同一個 statement 內重複的鍵只落地一次
	got, err = repo.InsertGarminEvents(ctx, []garminEventIn{ev("u4", "e"), ev("u4", "e"), ev("u4", "e")})
	if err != nil || len(got) != 1 {
		t.Fatalf("intra-statement duplicates: %d rows err=%v, want 1", len(got), err)
	}
	// dedupe_key 為 NULL（deregistration）：兩筆都要入庫
	nullEv := garminEventIn{EventType: garminEvDeregister, ProviderUserID: p + "u5", Payload: []byte(`{}`)}
	got, err = repo.InsertGarminEvents(ctx, []garminEventIn{nullEv, nullEv})
	if err != nil || len(got) != 2 {
		t.Fatalf("NULL dedupe keys: %d rows err=%v, want 2", len(got), err)
	}
	got, err = repo.InsertGarminEvents(ctx, []garminEventIn{nullEv})
	if err != nil || len(got) != 1 {
		t.Fatalf("NULL dedupe key again: %d rows err=%v, want 1", len(got), err)
	}
	// 預設值：pending、租約約 2 分鐘
	var status string
	var attempts int
	var lease time.Duration
	if err := pool.QueryRow(ctx, `SELECT status, attempts, next_attempt_at - now() FROM integration_events WHERE dedupe_key=$1`, p+"a").Scan(&status, &attempts, &lease); err != nil {
		t.Fatal(err)
	}
	if status != garminStPending || attempts != 0 || lease < 90*time.Second || lease > 125*time.Second {
		t.Fatalf("defaults: status=%s attempts=%d lease=%s", status, attempts, lease)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM integration_events WHERE provider_user_id LIKE $1`, p+"%").Scan(&n)
	if n != 3+1+1+2+1 {
		t.Fatalf("row count = %d, want 8", n)
	}
	// payload 是 jsonb、可被查詢
	var st int64
	if err := pool.QueryRow(ctx, `SELECT (payload->>'startTimeInSeconds')::bigint FROM integration_events WHERE dedupe_key=$1`, p+"a").Scan(&st); err != nil || st != 1790000000 {
		t.Fatalf("payload jsonb: %d %v", st, err)
	}
}

func TestGarminIT_EventsLargeBatchChunked(t *testing.T) {
	pool := garminITPool(t)
	repo := NewRepository(pool)
	p := "itwg-big-" + garminITUnique() + "-"
	garminITCleanEvents(t, pool, p)
	var evs []garminEventIn
	for i := 0; i < 1203; i++ { // > garminInsertChunk(500)×2
		k := fmt.Sprintf("%sk%d", p, i)
		evs = append(evs, garminEventIn{EventType: garminEvActivity, ProviderUserID: p + "u", DedupeKey: &k, Payload: []byte(`{}`)})
	}
	got, err := repo.InsertGarminEvents(context.Background(), evs)
	if err != nil || len(got) != 1203 {
		t.Fatalf("large batch: %d rows err=%v", len(got), err)
	}
	got, err = repo.InsertGarminEvents(context.Background(), evs)
	if err != nil || len(got) != 0 {
		t.Fatalf("large batch replay: %d rows err=%v, want 0", len(got), err)
	}
}

func TestGarminIT_ClaimLeaseConcurrencyAndLifecycle(t *testing.T) {
	pool := garminITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	p := "itwg-claim-" + garminITUnique() + "-"
	garminITCleanEvents(t, pool, p)
	var evs []garminEventIn
	for i := 0; i < 60; i++ {
		k := fmt.Sprintf("%sk%d", p, i)
		evs = append(evs, garminEventIn{EventType: garminEvActivity, ProviderUserID: p + "u", DedupeKey: &k, Payload: []byte(`{}`)})
	}
	if _, err := repo.InsertGarminEvents(ctx, evs); err != nil {
		t.Fatal(err)
	}
	// 剛落地的事件有租約：不可被領取
	if got, err := repo.ClaimDueGarminEvents(ctx, 1000); err != nil {
		t.Fatal(err)
	} else {
		for _, e := range got {
			if strings.HasPrefix(e.ProviderUserID, p) {
				t.Fatalf("leased event was claimed: %s", e.ID)
			}
		}
	}
	// 讓這 60 筆到期
	if _, err := pool.Exec(ctx, `UPDATE integration_events SET next_attempt_at = now() - interval '1 second' WHERE provider_user_id LIKE $1`, p+"%"); err != nil {
		t.Fatal(err)
	}
	// 兩個並發領取：互不重疊、聯集完整（FOR UPDATE SKIP LOCKED）
	var wg sync.WaitGroup
	results := make([][]garminEvent, 4)
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			got, err := repo.ClaimDueGarminEvents(ctx, 25)
			if err != nil {
				t.Errorf("claim %d: %v", i, err)
			}
			for _, e := range got {
				if strings.HasPrefix(e.ProviderUserID, p) {
					results[i] = append(results[i], e)
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	seen := map[string]int{}
	for _, r := range results {
		for _, e := range r {
			seen[e.ID]++
		}
	}
	if len(seen) != 60 {
		// 其他測試不會碰這個前綴；若不是 60，代表有遺漏（limit 25×4=100≥60，所以必須全領到）
		t.Fatalf("claimed %d distinct events, want 60", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("event %s claimed %d times (SKIP LOCKED violated)", id, n)
		}
	}
	// 領取後租約約 3 分鐘，再領 0 筆
	var lease time.Duration
	_ = pool.QueryRow(ctx, `SELECT min(next_attempt_at) - now() FROM integration_events WHERE provider_user_id LIKE $1`, p+"%").Scan(&lease)
	if lease < 150*time.Second || lease > 190*time.Second {
		t.Fatalf("claim lease = %s, want ~3m", lease)
	}

	// 單筆：ClaimByID 只領到期者
	var id1 string
	_ = pool.QueryRow(ctx, `SELECT id::text FROM integration_events WHERE provider_user_id LIKE $1 LIMIT 1`, p+"%").Scan(&id1)
	if e, err := repo.ClaimGarminEventByID(ctx, id1); err != nil || e != nil {
		t.Fatalf("leased event must not be claimable by id: %v %v", e, err)
	}
	if err := repo.ReleaseGarminLeases(ctx, []string{id1}); err != nil {
		t.Fatal(err)
	}
	if e, err := repo.ClaimGarminEventByID(ctx, id1); err != nil || e == nil || e.ID != id1 {
		t.Fatalf("released event must be claimable: %v %v", e, err)
	}

	// 失敗→dead 的單向狀態機
	for i := 1; i <= 5; i++ {
		att, dead, err := repo.MarkGarminEventError(ctx, id1, "db:XX000", time.Now().Add(time.Minute), garminMaxAttempts)
		if err != nil || att != i || dead != (i == 5) {
			t.Fatalf("failure %d: attempts=%d dead=%v err=%v", i, att, dead, err)
		}
	}
	if err := repo.MarkGarminEventDone(ctx, id1, "inserted"); err != nil {
		t.Fatal(err)
	}
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM integration_events WHERE id=$1::uuid`, id1).Scan(&status)
	if status != garminStDead {
		t.Fatalf("dead must not be overwritten by a late done: %s", status)
	}
	if att, dead, err := repo.MarkGarminEventError(ctx, id1, "late", time.Now(), garminMaxAttempts); err != nil || att != 0 || dead {
		t.Fatalf("late failure on dead event must be a no-op: %d %v %v", att, dead, err)
	}

	// done、defer
	var id2 string
	_ = pool.QueryRow(ctx, `SELECT id::text FROM integration_events WHERE provider_user_id LIKE $1 AND id<>$2::uuid LIMIT 1`, p+"%", id1).Scan(&id2)
	if err := repo.DeferGarminEvent(ctx, id2, "processor_not_ready", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var note string
	var attempts int
	_ = pool.QueryRow(ctx, `SELECT last_error, attempts FROM integration_events WHERE id=$1::uuid`, id2).Scan(&note, &attempts)
	if note != "processor_not_ready" || attempts != 0 {
		t.Fatalf("defer: note=%q attempts=%d", note, attempts)
	}
	if err := repo.MarkGarminEventDone(ctx, id2, "skipped_non_running"); err != nil {
		t.Fatal(err)
	}
	var result string
	_ = pool.QueryRow(ctx, `SELECT status, result FROM integration_events WHERE id=$1::uuid`, id2).Scan(&status, &result)
	if status != garminStDone || result != "skipped_non_running" {
		t.Fatalf("done: %s %s", status, result)
	}
}

func TestGarminIT_PurgeRetention(t *testing.T) {
	pool := garminITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	p := "itwg-purge-" + garminITUnique() + "-"
	garminITCleanEvents(t, pool, p)
	mk := func(name, status string, recvAgo, procAgo string) {
		_, err := pool.Exec(ctx, `INSERT INTO integration_events (provider, event_type, provider_user_id, dedupe_key, payload, status, received_at, processed_at)
			VALUES ('garmin','activity',$1,$2,'{}',$3, now() - $4::interval, CASE WHEN $5 = '' THEN NULL ELSE now() - $5::interval END)`,
			p+name, p+name, status, recvAgo, procAgo)
		if err != nil {
			t.Fatalf("mk %s: %v", name, err)
		}
	}
	mk("done15", "done", "16 days", "15 days") // 清
	mk("done13", "done", "14 days", "13 days") // 留
	mk("dead31", "dead", "31 days", "31 days") // 清
	mk("dead29", "dead", "29 days", "29 days") // 留
	mk("err31", "error", "31 days", "")        // 清
	mk("pend31", "pending", "31 days", "")     // 清（久未處理）
	mk("pend2", "pending", "2 days", "")       // 留
	if _, err := repo.PurgeGarminEvents(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT provider_user_id FROM integration_events WHERE provider_user_id LIKE $1 ORDER BY 1`, p+"%")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var left []string
	for rows.Next() {
		var u string
		_ = rows.Scan(&u)
		left = append(left, strings.TrimPrefix(u, p))
	}
	if strings.Join(left, ",") != "dead29,done13,pend2" {
		t.Fatalf("after purge: %v, want [dead29 done13 pend2]", left)
	}
}

func TestGarminIT_EventStats(t *testing.T) {
	pool := garminITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	p := "itwg-stat-" + garminITUnique() + "-"
	garminITCleanEvents(t, pool, p)
	before, err := repo.GarminEventStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ins := func(name, status, result string, nextAgo string) {
		_, err := pool.Exec(ctx, `INSERT INTO integration_events (provider, event_type, provider_user_id, dedupe_key, payload, status, result, processed_at, next_attempt_at)
			VALUES ('garmin','activity',$1,$1,'{}',$2,NULLIF($3,''), CASE WHEN $4::boolean THEN now() END, now() - $5::interval)`, p+name, status, result, status == "done", nextAgo)
		if err != nil {
			t.Fatal(err)
		}
	}
	ins("a", "done", "inserted", "0 seconds")
	ins("b", "done", "skipped_non_running", "0 seconds")
	ins("c", "pending", "", "1 hour") // 逾期未處理
	ins("d", "dead", "", "0 seconds")
	after, err := repo.GarminEventStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d := after.Received24h - before.Received24h; d != 4 {
		t.Errorf("received delta %d, want 4", d)
	}
	if d := after.Imported24h - before.Imported24h; d != 1 {
		t.Errorf("imported delta %d, want 1", d)
	}
	if d := after.Skipped24h - before.Skipped24h; d != 1 {
		t.Errorf("skipped delta %d, want 1", d)
	}
	if d := after.PendingStale - before.PendingStale; d != 1 {
		t.Errorf("pending-stale delta %d, want 1", d)
	}
	if d := after.Dead - before.Dead; d != 1 {
		t.Errorf("dead delta %d, want 1", d)
	}
}

// --- user_integrations ---

func TestGarminIT_SaveGarminPreservesCreatedAtOverTerraRow(t *testing.T) {
	pool := garminITPool(t)
	garminITSetKey(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	uid := garminITUser(t, pool)
	gid := "itwg-g-" + garminITUnique()

	// 舊 Terra 列（11 位使用者的現況），created_at（匯入 floor）回推 10 天
	if err := repo.SaveTerra(ctx, &Connection{UserID: uid, Provider: "garmin", ProviderUserID: "11111111-1111-4111-8111-111111111111", Scope: "x", ExpiresAt: time.Now().AddDate(100, 0, 0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE user_integrations SET created_at = now() - interval '10 days' WHERE user_id=$1 AND provider='garmin'`, uid); err != nil {
		t.Fatal(err)
	}
	var floor time.Time
	_ = pool.QueryRow(ctx, `SELECT created_at FROM user_integrations WHERE user_id=$1 AND provider='garmin'`, uid).Scan(&floor)

	in := garminITSaveInput(uid, gid)
	res, err := repo.SaveGarmin(ctx, in)
	if err != nil {
		t.Fatalf("SaveGarmin: %v", err)
	}
	if res.Inserted || res.PrevVia != "terra" {
		t.Fatalf("overwrite of terra row: %+v", res)
	}
	if !res.ConnectedAt.Equal(floor) {
		t.Fatalf("created_at (import floor) changed: %s -> %s", floor, res.ConnectedAt)
	}
	c, err := repo.GetGarminByUser(ctx, uid, true)
	if err != nil || c == nil {
		t.Fatalf("read back: %v %v", c, err)
	}
	if c.Via != "direct" || c.ProviderUserID != gid || !c.ConnectedAt.Equal(floor) {
		t.Fatalf("row: %+v", c)
	}
	if c.AccessToken != in.AccessToken || c.RefreshToken != in.RefreshToken {
		t.Fatalf("tokens must round-trip through encryption")
	}
	var rawA, rawR string
	_ = pool.QueryRow(ctx, `SELECT access_token, refresh_token FROM user_integrations WHERE user_id=$1 AND provider='garmin'`, uid).Scan(&rawA, &rawR)
	if !strings.HasPrefix(rawA, "enc:") || !strings.HasPrefix(rawR, "enc:") || strings.Contains(rawA, in.AccessToken) {
		t.Fatalf("tokens must be stored encrypted: %q %q", rawA, rawR)
	}
	if c.Scope != in.Scope || c.Issuer != in.Issuer || c.ConsentVersion != "v1" || c.ConsentAt == nil || c.RefreshExpiresAt == nil || c.ReauthRequiredAt != nil {
		t.Fatalf("garmin columns: %+v", c)
	}
	if c.AuthorizedAt == nil || time.Since(*c.AuthorizedAt) > time.Minute {
		t.Fatalf("connected_at must be set to now: %v", c.AuthorizedAt)
	}
	if c.paused() || !c.hasPermission("ACTIVITY_EXPORT") {
		t.Fatalf("permission helpers: paused=%v", c.paused())
	}

	// 重新授權（非破壞性）：floor 不動、token 換新、reauth 旗標清除
	if err := repo.MarkGarminReauth(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	in2 := garminITSaveInput(uid, gid)
	in2.AccessToken, in2.RefreshToken = "access-v2", "refresh-v2"
	res2, err := repo.SaveGarmin(ctx, in2)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Inserted || res2.PrevVia != "direct" || !res2.ConnectedAt.Equal(floor) {
		t.Fatalf("re-auth: %+v floor=%s", res2, floor)
	}
	c2, _ := repo.GetGarminByUser(ctx, uid, true)
	if c2.AccessToken != "access-v2" || c2.RefreshToken != "refresh-v2" || c2.ReauthRequiredAt != nil {
		t.Fatalf("re-auth row: %+v", c2)
	}

	// 帳號變更：既有直連列的 Garmin userId 不同 → 拒絕，列不動
	_, err = repo.SaveGarmin(ctx, garminITSaveInput(uid, gid+"-other"))
	if !errors.Is(err, ErrGarminAccountChanged) {
		t.Fatalf("different garmin account on a direct row: err=%v, want ErrGarminAccountChanged", err)
	}
	c3, _ := repo.GetGarminByUser(ctx, uid, false)
	if c3.ProviderUserID != gid {
		t.Fatalf("row must be untouched after account_changed")
	}

	// 連線→中斷→全新連接：created_at 全新
	if err := repo.DeleteGarminConnection(ctx, uid); err != nil {
		t.Fatal(err)
	}
	res3, err := repo.SaveGarmin(ctx, garminITSaveInput(uid, gid))
	if err != nil || !res3.Inserted || res3.PrevVia != "" || !res3.ConnectedAt.After(floor.Add(24*time.Hour)) {
		t.Fatalf("fresh connect after disconnect: %+v err=%v", res3, err)
	}
}

func TestGarminIT_SaveGarminFailClosedWithoutKey(t *testing.T) {
	pool := garminITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	uid := garminITUser(t, pool)
	if err := repo.SaveTerra(ctx, &Connection{UserID: uid, Provider: "garmin", ProviderUserID: "22222222-2222-4222-8222-222222222222", ExpiresAt: time.Now().AddDate(100, 0, 0)}); err != nil {
		t.Fatal(err)
	}
	for name, key := range map[string]string{"missing": "", "invalid": "not-a-valid-key", "too short": hex.EncodeToString([]byte("short"))} {
		t.Setenv("STRAVA_TOKEN_KEY", key)
		_, err := repo.SaveGarmin(ctx, garminITSaveInput(uid, "itwg-nokey-"+garminITUnique()))
		if !errors.Is(err, ErrGarminNoTokenKey) {
			t.Fatalf("%s key: err=%v, want ErrGarminNoTokenKey", name, err)
		}
		if err := repo.UpdateGarminTokens(ctx, "00000000-0000-4000-8000-0000000000aa", "a", "r", time.Now(), time.Now()); !errors.Is(err, ErrGarminNoTokenKey) {
			t.Fatalf("%s key: UpdateGarminTokens err=%v", name, err)
		}
	}
	// 列沒被動到、也沒有明碼
	var via, access string
	_ = pool.QueryRow(ctx, `SELECT via, access_token FROM user_integrations WHERE user_id=$1 AND provider='garmin'`, uid).Scan(&via, &access)
	if via != "terra" || access != "" {
		t.Fatalf("fail-closed must leave the row untouched: via=%s access=%q", via, access)
	}
}

func TestGarminIT_UniqueBindingConflict(t *testing.T) {
	pool := garminITPool(t)
	garminITSetKey(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	a, b := garminITUser(t, pool), garminITUser(t, pool)
	gid := "itwg-bind-" + garminITUnique()
	if _, err := repo.SaveGarmin(ctx, garminITSaveInput(a, gid)); err != nil {
		t.Fatal(err)
	}
	// B 先有 Terra 舊列；嘗試綁同一個 Garmin 帳號 → ErrGarminAlreadyLinked，B 的舊列完好、A 不受影響
	if err := repo.SaveTerra(ctx, &Connection{UserID: b, Provider: "garmin", ProviderUserID: "33333333-3333-4333-8333-333333333333", ExpiresAt: time.Now().AddDate(100, 0, 0)}); err != nil {
		t.Fatal(err)
	}
	_, err := repo.SaveGarmin(ctx, garminITSaveInput(b, gid))
	if !errors.Is(err, ErrGarminAlreadyLinked) {
		t.Fatalf("err=%v, want ErrGarminAlreadyLinked", err)
	}
	cb, _ := repo.GetGarminByUser(ctx, b, false)
	if cb == nil || cb.Via != "terra" || cb.ProviderUserID != "33333333-3333-4333-8333-333333333333" {
		t.Fatalf("B's terra row must be intact: %+v", cb)
	}
	ca, _ := repo.GetGarminByUser(ctx, a, false)
	if ca == nil || ca.Via != "direct" || ca.ProviderUserID != gid {
		t.Fatalf("A must be unaffected: %+v", ca)
	}
	// 以 Garmin userId 反查只會找到 A
	if c, _ := repo.GetGarminDirectByUserID(ctx, gid, false); c == nil || c.UserID != a {
		t.Fatalf("GetGarminDirectByUserID: %+v", c)
	}
	// Terra 的 UUID 不會被 GetGarminDirectByUserID 找到（刻意不看 via='terra'）
	if c, _ := repo.GetGarminDirectByUserID(ctx, "33333333-3333-4333-8333-333333333333", false); c != nil {
		t.Fatalf("terra row must not be returned as a direct connection")
	}
}

func TestGarminIT_SaveTerraUnlessDirect(t *testing.T) {
	pool := garminITPool(t)
	garminITSetKey(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	uid := garminITUser(t, pool)
	far := time.Now().AddDate(100, 0, 0)

	saved, err := repo.SaveTerraUnlessDirect(ctx, &Connection{UserID: uid, Provider: "garmin", ProviderUserID: "t-1", Scope: "s1", ExpiresAt: far})
	if err != nil || !saved {
		t.Fatalf("insert: saved=%v err=%v", saved, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE user_integrations SET created_at = now() - interval '5 days' WHERE user_id=$1 AND provider='garmin'`, uid); err != nil {
		t.Fatal(err)
	}
	var floor time.Time
	_ = pool.QueryRow(ctx, `SELECT created_at FROM user_integrations WHERE user_id=$1 AND provider='garmin'`, uid).Scan(&floor)
	saved, err = repo.SaveTerraUnlessDirect(ctx, &Connection{UserID: uid, Provider: "garmin", ProviderUserID: "t-2", Scope: "s2", ExpiresAt: far})
	if err != nil || !saved {
		t.Fatalf("terra→terra update: saved=%v err=%v", saved, err)
	}
	c, _ := repo.GetGarminByUser(ctx, uid, false)
	if c.ProviderUserID != "t-2" || c.Via != "terra" || !c.ConnectedAt.Equal(floor) {
		t.Fatalf("terra update must keep created_at: %+v", c)
	}

	// 變成直連後：Terra 寫入一律被擋
	if _, err := repo.SaveGarmin(ctx, garminITSaveInput(uid, "itwg-direct-"+garminITUnique())); err != nil {
		t.Fatal(err)
	}
	before, _ := repo.GetGarminByUser(ctx, uid, true)
	saved, err = repo.SaveTerraUnlessDirect(ctx, &Connection{UserID: uid, Provider: "garmin", ProviderUserID: "t-3", Scope: "s3", ExpiresAt: far})
	if err != nil || saved {
		t.Fatalf("direct row must block terra overwrite: saved=%v err=%v", saved, err)
	}
	after, _ := repo.GetGarminByUser(ctx, uid, true)
	if after.Via != "direct" || after.ProviderUserID != before.ProviderUserID || after.AccessToken != before.AccessToken ||
		after.RefreshToken != before.RefreshToken || !after.ConnectedAt.Equal(before.ConnectedAt) || after.Scope != before.Scope {
		t.Fatalf("direct row changed by terra write:\nbefore=%+v\nafter=%+v", before, after)
	}

	// 通則：任何供應商的 via='direct' 列都不被 Terra 覆蓋（例：Strava 直連列的 token 不可被清空）
	if err := repo.Save(ctx, &Connection{UserID: uid, Provider: "strava", ProviderUserID: "athlete-1", AccessToken: "sa", RefreshToken: "sr", ExpiresAt: far}); err != nil {
		t.Fatal(err)
	}
	saved, err = repo.SaveTerraUnlessDirect(ctx, &Connection{UserID: uid, Provider: "strava", ProviderUserID: "x", ExpiresAt: far})
	if err != nil || saved {
		t.Fatalf("strava direct row: saved=%v err=%v", saved, err)
	}
	sc, _ := repo.GetByUser(ctx, uid, "strava")
	if sc.AccessToken != "sa" || sc.Via != "direct" {
		t.Fatalf("strava row damaged: %+v", sc)
	}
}

func TestGarminIT_TerraHandlersNeverOverwriteDirectRow(t *testing.T) {
	pool := garminITPool(t)
	garminITSetKey(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	uid := garminITUser(t, pool)
	gid := "itwg-th-" + garminITUnique()
	if _, err := repo.SaveGarmin(ctx, garminITSaveInput(uid, gid)); err != nil {
		t.Fatal(err)
	}
	snap := func() *garminConn { c, _ := repo.GetGarminByUser(ctx, uid, true); return c }
	before := snap()
	same := func(step string) {
		t.Helper()
		a := snap()
		if a == nil || a.Via != "direct" || a.ProviderUserID != before.ProviderUserID || a.AccessToken != before.AccessToken ||
			a.RefreshToken != before.RefreshToken || !a.ConnectedAt.Equal(before.ConnectedAt) || a.Scope != before.Scope || !a.ExpiresAt.Equal(before.ExpiresAt) {
			t.Fatalf("%s: direct row changed:\nbefore=%+v\nafter=%+v", step, before, a)
		}
	}
	h := NewTerraHandler(repo, TerraConfig{DevID: "d", APIKey: "k", SigningSecret: "s", FrontendURL: "https://app.test/"}, nil)
	terraUID := "44444444-4444-4444-8444-444444444444"

	// 1) auth 事件（Terra 端授權成功）
	h.handleAuthEvent(ctx, []byte(fmt.Sprintf(`{"type":"auth","status":"success","user":{"user_id":"%s","provider":"GARMIN","reference_id":"%s"}}`, terraUID, uid)))
	same("auth event")

	// 2) activity 事件走「查無連線→用 reference_id 保底建連線」分支：不得覆蓋，且活動不匯入
	h.handleActivityEvent(ctx, []byte(fmt.Sprintf(`{"type":"activity","user":{"user_id":"%s","provider":"GARMIN","reference_id":"%s"},
		"data":[{"metadata":{"start_time":"2026-10-01T06:00:00Z","summary_id":"itwg-terra-act","type":8},"distance_data":{"summary":{"distance_meters":5000}},"active_durations_data":{"activity_seconds":1800}}]}`, terraUID, uid)))
	same("activity fallback")
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM activities WHERE user_id=$1`, uid).Scan(&n)
	if n != 0 {
		t.Fatalf("terra activity must not be imported for a direct connection, got %d", n)
	}

	// 3) callback（Terra widget 導回）：以假 userInfo 回應
	h.hc = &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResp(200, fmt.Sprintf(`{"user":{"user_id":"%s","provider":"GARMIN","reference_id":"%s"}}`, terraUID, uid)), nil
	})}
	rec := httptestRecorder()
	h.Callback(rec, newGetRequest("/callback?user_id="+terraUID+"&reference_id="+uid+"&resource=GARMIN"))
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || !strings.Contains(loc, "terra=failed") || !strings.Contains(loc, "reason=already_direct") {
		t.Fatalf("callback: %d %q", rec.Code, loc)
	}
	same("callback")

	// 4) deauth／reauth 事件帶著直連列的 Garmin userId 或 Terra id：都不可動到直連列
	h.handleDeauthEvent(ctx, []byte(fmt.Sprintf(`{"type":"deauth","user":{"user_id":"%s","provider":"GARMIN"}}`, gid)))
	same("deauth with garmin id")
	h.handleReauthEvent(ctx, []byte(fmt.Sprintf(`{"type":"user_reauth","old_user":{"user_id":"%s","provider":"GARMIN"},"new_user":{"user_id":"%s","provider":"GARMIN","reference_id":"%s"}}`, gid, terraUID, uid)))
	same("reauth with garmin id")

	// 5) 對照：沒有直連列的使用者，同樣的 auth 事件照常建立 Terra 連線（守衛不影響既有 Terra 流程）
	uid2 := garminITUser(t, pool)
	h.handleAuthEvent(ctx, []byte(fmt.Sprintf(`{"type":"auth","status":"success","user":{"user_id":"55555555-5555-4555-8555-555555555555","provider":"GARMIN","reference_id":"%s"}}`, uid2)))
	c2, _ := repo.GetGarminByUser(ctx, uid2, false)
	if c2 == nil || c2.Via != "terra" {
		t.Fatalf("terra auth for a user without direct row must still create the terra row: %+v", c2)
	}
}

// --- activities 刪除 ---

func garminITInsertActivity(t *testing.T, pool *pgxpool.Pool, uid string, source, extID *string, start time.Time, flagged bool, flagReason string, dupOf *string) string {
	t.Helper()
	var id string
	var fr any
	if flagReason != "" {
		fr = flagReason
	}
	err := pool.QueryRow(context.Background(), `INSERT INTO activities (user_id, distance_km, duration_s, avg_pace_s, recorded_at, processed, source, external_id, flagged, flag_reason, dup_of)
		VALUES ($1, 5, 1800, 360, $2, TRUE, $3, $4, $5, $6, $7::uuid) RETURNING id::text`, uid, start, source, extID, flagged, fr, dupOf).Scan(&id)
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}
	return id
}

func TestGarminIT_DeleteGarminActivitiesRemovesAllGarminSourceRows(t *testing.T) {
	pool := garminITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	uid := garminITUser(t, pool)
	other := garminITUser(t, pool)
	u := garminITUnique()
	str := func(s string) *string { return &s }
	now := time.Now().Add(-48 * time.Hour)
	g1 := garminITInsertActivity(t, pool, uid, str("garmin"), str("gc:"+u+"1"), now, false, "", nil)
	garminITInsertActivity(t, pool, uid, str("garmin"), str("bare-terra-"+u), now.Add(time.Hour), false, "", nil) // 舊 Terra 列（裸 id）
	garminITInsertActivity(t, pool, uid, str("garmin"), str("garmin:"+u), now.Add(2*time.Hour), false, "", nil)   // 舊 Terra 列（保底 id）
	keep1 := garminITInsertActivity(t, pool, uid, str("strava"), str("s-"+u), now.Add(3*time.Hour), false, "", nil)
	keep2 := garminITInsertActivity(t, pool, uid, str("coros"), str("mcp:"+u), now.Add(4*time.Hour), false, "", nil)
	keep3 := garminITInsertActivity(t, pool, uid, nil, nil, now.Add(5*time.Hour), false, "", nil) // App GPS
	// GPS 列因與 garmin 列重疊被標記良性重複（dup_of 指向 g1）：刪除後必須解除標記
	dupGPS := garminITInsertActivity(t, pool, uid, nil, nil, now.Add(6*time.Hour), true, "multi_device_duplicate", &g1)
	// 其他使用者的 garmin 列不受影響
	otherG := garminITInsertActivity(t, pool, other, str("garmin"), str("gc:"+u+"9"), now, false, "", nil)

	n, err := repo.DeleteGarminActivities(ctx, uid)
	if err != nil || n != 3 {
		t.Fatalf("deleted %d rows err=%v, want 3 (all source='garmin' rows of the user)", n, err)
	}
	var cnt int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM activities WHERE user_id=$1 AND source='garmin'`, uid).Scan(&cnt)
	if cnt != 0 {
		t.Fatalf("garmin rows left: %d", cnt)
	}
	for _, id := range []string{keep1, keep2, keep3, dupGPS, otherG} {
		var ex bool
		_ = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM activities WHERE id=$1::uuid)`, id).Scan(&ex)
		if !ex {
			t.Fatalf("activity %s must not be deleted", id)
		}
	}
	var flagged bool
	var reason, dup *string
	_ = pool.QueryRow(ctx, `SELECT flagged, flag_reason, dup_of::text FROM activities WHERE id=$1::uuid`, dupGPS).Scan(&flagged, &reason, &dup)
	if flagged || reason != nil || dup != nil {
		t.Fatalf("benign duplicate marker must be cleared: flagged=%v reason=%v dup=%v", flagged, reason, dup)
	}
	// 冪等
	if n, err := repo.DeleteGarminActivities(ctx, uid); err != nil || n != 0 {
		t.Fatalf("second delete: %d %v", n, err)
	}
}

func TestGarminIT_LegacyTwinExists(t *testing.T) {
	pool := garminITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	uid := garminITUser(t, pool)
	u := garminITUnique()
	str := func(s string) *string { return &s }
	start := time.Unix(1790000000, 0)
	garminITInsertActivity(t, pool, uid, str("garmin"), str("bare-"+u), start, false, "", nil)
	garminITInsertActivity(t, pool, uid, str("garmin"), str("garmin:1790000500"), start.Add(500*time.Second), false, "", nil)
	for name, tc := range map[string]struct {
		bare  string
		start int64
		want  bool
	}{
		"bare id":            {"bare-" + u, 1790009999, true},
		"fallback start id":  {"nomatch-" + u, 1790000500, true},
		"neither":            {"nomatch-" + u, 1790001234, false},
		"empty bare, no hit": {"", 1790001234, false},
	} {
		got, err := repo.LegacyTwinExists(ctx, uid, tc.bare, tc.start)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %v err=%v, want %v", name, got, err, tc.want)
		}
	}
	// 其他使用者不受影響
	if got, _ := repo.LegacyTwinExists(ctx, garminITUser(t, pool), "bare-"+u, 1790000500); got {
		t.Error("legacy twin lookup must be scoped to the user")
	}
}

// --- 鎖、封鎖清單、已知使用者 ---

func TestGarminIT_WithUserLock(t *testing.T) {
	pool := garminITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	key := "itwg-lock-" + garminITUnique()
	holding := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- repo.WithGarminUserLock(ctx, key, func(ctx context.Context) error {
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding
	// 同 key：第二個拿不到鎖
	if err := repo.WithGarminUserLock(ctx, key, func(ctx context.Context) error { t.Error("must not run"); return nil }); !errors.Is(err, errGarminBusy) {
		t.Fatalf("same key while held: err=%v, want errGarminBusy", err)
	}
	// 不同 key：照常
	ran := false
	if err := repo.WithGarminUserLock(ctx, key+"-other", func(ctx context.Context) error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("different key: ran=%v err=%v", ran, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// 釋放後可再取得；fn 回錯時也會釋放
	boom := errors.New("boom")
	if err := repo.WithGarminUserLock(ctx, key, func(ctx context.Context) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("fn error must propagate: %v", err)
	}
	if err := repo.WithGarminUserLock(ctx, key, func(ctx context.Context) error { return nil }); err != nil {
		t.Fatalf("lock must be released after fn error: %v", err)
	}
	// ctx 取消：不會卡住
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := repo.WithGarminUserLock(cctx, key, func(ctx context.Context) error { return nil }); err == nil {
		t.Fatal("cancelled ctx must fail")
	}
}

func TestGarminIT_KnownUsersAndBlocklist(t *testing.T) {
	pool := garminITPool(t)
	garminITSetKey(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	a, b, c := garminITUser(t, pool), garminITUser(t, pool), garminITUser(t, pool)
	ga, gb, gc := "itwg-k-a-"+garminITUnique(), "itwg-k-b-"+garminITUnique(), "itwg-k-c-"+garminITUnique()
	for u, g := range map[string]string{a: ga, b: gb, c: gc} {
		if _, err := repo.SaveGarmin(ctx, garminITSaveInput(u, g)); err != nil {
			t.Fatal(err)
		}
	}
	// 一筆 Terra 列（不算已知的直連使用者）
	d := garminITUser(t, pool)
	if err := repo.SaveTerra(ctx, &Connection{UserID: d, Provider: "garmin", ProviderUserID: "66666666-6666-4666-8666-666666666666", ExpiresAt: time.Now().AddDate(100, 0, 0)}); err != nil {
		t.Fatal(err)
	}
	known, err := repo.KnownGarminUsers(ctx, []string{ga, gb, gc, "nobody", "66666666-6666-4666-8666-666666666666"})
	if err != nil || len(known) != 3 || known[ga] != a || known[gb] != b || known[gc] != c {
		t.Fatalf("known = %v err=%v", known, err)
	}
	if k, _ := repo.KnownGarminUsers(ctx, nil); len(k) != 0 {
		t.Fatal("empty input")
	}
	// 封鎖：以 DOR user_id、以 Garmin userId 各一
	if _, err := pool.Exec(ctx, `INSERT INTO garmin_blocklist (user_id, reason) VALUES ($1::uuid, 'test')`, b); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO garmin_blocklist (garmin_user_id, reason) VALUES ($1, 'test')`, gc); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM garmin_blocklist WHERE reason='test' AND (user_id=$1::uuid OR garmin_user_id=$2)`, b, gc)
	})
	known, err = repo.KnownGarminUsers(ctx, []string{ga, gb, gc})
	if err != nil || len(known) != 1 || known[ga] != a {
		t.Fatalf("blocked users must be filtered: %v err=%v", known, err)
	}
	for name, tc := range map[string]struct {
		uid, gid string
		want     bool
	}{
		"by user id": {b, "", true}, "by garmin id": {"", gc, true}, "clean": {a, ga, false}, "both empty": {"", "", false},
	} {
		got, err := repo.GarminBlocked(ctx, tc.uid, tc.gid)
		if err != nil || got != tc.want {
			t.Errorf("GarminBlocked %s: %v err=%v, want %v", name, got, err, tc.want)
		}
	}
	// 封鎖清單限制：兩個目標欄位都空不可寫入
	if _, err := pool.Exec(ctx, `INSERT INTO garmin_blocklist (reason) VALUES ('x')`); err == nil {
		t.Fatal("blocklist row without target must violate the check constraint")
	}
}

// --- EXPLAIN：關鍵查詢走索引 ---

func TestGarminIT_QueriesUseIndexes(t *testing.T) {
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
	explain := func(q string, args ...any) string {
		rows, err := tx.Query(ctx, "EXPLAIN "+q, args...)
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
		return sb.String()
	}
	plan := explain(`SELECT via, provider_user_id FROM user_integrations WHERE user_id='00000000-0000-4000-8000-000000000001' AND provider='garmin' FOR UPDATE`)
	if !strings.Contains(plan, "Index") || strings.Contains(plan, "Seq Scan") {
		t.Fatalf("SaveGarmin lookup should use the (user_id, provider) unique index:\n%s", plan)
	}
	plan = explain(`SELECT id FROM user_integrations WHERE provider='garmin' AND via='direct' AND provider_user_id='abc'`)
	if !strings.Contains(plan, "Index") || strings.Contains(plan, "Seq Scan") {
		t.Fatalf("GetGarminDirectByUserID should use an index:\n%s", plan)
	}
	// 掃描查詢：用「真實比例」的資料（大量 done、少量到期的 pending，已 ANALYZE）驗證會走部分索引——
	// 空表沒有統計資訊時規劃器的選擇沒有意義（整個交易最後回滾）。
	if _, err := tx.Exec(ctx, `
		INSERT INTO integration_events (provider, event_type, provider_user_id, dedupe_key, payload, status, processed_at)
		SELECT 'garmin','activity','itwg-plan-'||(g%50),'itwg-plan-'||g,'{}','done', now() FROM generate_series(1,5000) g`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO integration_events (provider, event_type, provider_user_id, dedupe_key, payload, status, next_attempt_at)
		SELECT 'garmin','activity','itwg-plan-1','itwg-plan-p'||g,'{}','pending', now() - interval '1 minute' FROM generate_series(1,3) g`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `ANALYZE integration_events`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = on`); err != nil {
		t.Fatal(err)
	}
	plan = explain(`SELECT id FROM integration_events WHERE provider='garmin' AND status IN ('pending','error') AND next_attempt_at <= now() ORDER BY next_attempt_at LIMIT 200`)
	if !strings.Contains(plan, "idx_integration_events_due") {
		t.Fatalf("event sweep should use idx_integration_events_due:\n%s", plan)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	plan = explain(`SELECT id FROM integration_events WHERE provider='garmin' AND dedupe_key='x'`)
	if !strings.Contains(plan, "Index") || strings.Contains(plan, "Seq Scan") {
		t.Fatalf("dedupe lookup should use an index (the ON CONFLICT arbiter itself is exercised by the insert tests):\n%s", plan)
	}
}

// --- 端到端（真 PG）：推送 → 落地 → 就地處理 → done；Drain／掃描 ---

func TestGarminIT_EndToEndWebhookWithRealStore(t *testing.T) {
	pool := garminITPool(t)
	garminITSetKey(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	uid := garminITUser(t, pool)
	gid := "itwg-e2e-" + garminITUnique()
	garminITCleanEvents(t, pool, gid)
	if _, err := repo.SaveGarmin(ctx, garminITSaveInput(uid, gid)); err != nil {
		t.Fatal(err)
	}
	h := NewGarminHandler(GarminConfig{WebhookToken: testGarminToken}, GarminDeps{DB: pool})
	h.alertFn = func(kind, title, detail string) {}
	var mu sync.Mutex
	handled := map[string]int{}
	h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		mu.Lock()
		handled[ev.ID]++
		mu.Unlock()
		return garminResInserted, nil
	}
	router := h.Router()
	send := func(raw string) int {
		rec := httptestRecorder()
		router.ServeHTTP(rec, newPostRequest("/webhook/"+testGarminToken+"/activities", raw))
		return rec.Code
	}
	push := func(summary string, start int64) string {
		return fmt.Sprintf(`{"activities":[{"userId":"%s","summaryId":"%s","activityType":"RUNNING","startTimeInSeconds":%d,"durationInSeconds":1800,"distanceInMeters":5000,"activityName":"secret name","startingLatitudeInDegree":25.1}]}`, gid, summary, start)
	}
	if code := send(push("e2e-1", 1790000000)); code != 200 {
		t.Fatalf("push: %d", code)
	}
	// 重送：不產生新事件
	if code := send(push("e2e-1", 1790000000)); code != 200 {
		t.Fatalf("resend: %d", code)
	}
	// 未知使用者與非白名單類型：不落地
	if code := send(`{"activities":[{"userId":"nobody","summaryId":"x","activityType":"RUNNING","startTimeInSeconds":1790000001},
		{"userId":"` + gid + `","summaryId":"cyc","activityType":"CYCLING","startTimeInSeconds":1790000002}]}`); code != 200 {
		t.Fatalf("unknown: %d", code)
	}
	deadline := time.Now().Add(3 * time.Second)
	var status, result string
	for time.Now().Before(deadline) {
		_ = pool.QueryRow(ctx, `SELECT status, COALESCE(result,'') FROM integration_events WHERE provider_user_id=$1`, gid).Scan(&status, &result)
		if status == garminStDone {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status != garminStDone || result != garminResInserted {
		t.Fatalf("event status=%s result=%s, want done/inserted", status, result)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM integration_events WHERE provider_user_id=$1`, gid).Scan(&n)
	if n != 1 {
		t.Fatalf("events for user = %d, want exactly 1 (resend deduped, unknown/cycling dropped)", n)
	}
	// payload 最小化
	var payload string
	_ = pool.QueryRow(ctx, `SELECT payload::text FROM integration_events WHERE provider_user_id=$1`, gid).Scan(&payload)
	for _, banned := range []string{"secret name", "25.1", "startingLatitude", "activityName"} {
		if strings.Contains(payload, banned) {
			t.Fatalf("stored payload leaks %q: %s", banned, payload)
		}
	}
	mu.Lock()
	if len(handled) != 1 {
		t.Fatalf("handler ran for %d events, want 1", len(handled))
	}
	mu.Unlock()

	// 崩潰復原：事件已落地但沒處理（draining 的行程）→ 新處理器透過掃描接手，只處理一次
	h.Drain(ctx)
	h2 := NewGarminHandler(GarminConfig{WebhookToken: testGarminToken}, GarminDeps{DB: pool})
	h2.alertFn = func(kind, title, detail string) {}
	h2.handlers.activity = h.handlers.activity
	if code := func() int {
		rec := httptestRecorder()
		h.Router().ServeHTTP(rec, newPostRequest("/webhook/"+testGarminToken+"/activities", push("e2e-2", 1790000100)))
		return rec.Code
	}(); code != 200 { // h 已 Drain：仍落地、不處理
		t.Fatalf("push during drain: %d", code)
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM integration_events WHERE provider_user_id=$1 AND status='pending'`, gid).Scan(&n)
	if n != 1 {
		t.Fatalf("pending after drain = %d, want 1", n)
	}
	// 關閉中落地的事件已立刻釋放租約：新行程的掃描可以馬上接手（不必等 2 分鐘租約過期）
	var lease time.Duration
	_ = pool.QueryRow(ctx, `SELECT next_attempt_at - now() FROM integration_events WHERE provider_user_id=$1 AND status='pending'`, gid).Scan(&lease)
	if lease > time.Second {
		t.Fatalf("lease should have been released on drain, still %s", lease)
	}
	if got := h2.SweepPending(ctx); got != 1 {
		t.Fatalf("sweep right after drain processed %d, want 1", got)
	}
	if got := h2.SweepPending(ctx); got != 0 { // 再掃一次：已處理完，不會重複
		t.Fatalf("second sweep processed %d", got)
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM integration_events WHERE provider_user_id=$1 AND status='done'`, gid).Scan(&n)
	if n != 2 {
		t.Fatalf("done events = %d, want 2", n)
	}
}
