//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func garminITHandler(t *testing.T, pool *pgxpool.Pool) *GarminHandler {
	t.Helper()
	h := NewGarminHandler(GarminConfig{WebhookToken: testGarminToken}, GarminDeps{DB: pool})
	h.alertFn = func(kind, title, detail string) {}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		h.Drain(c)
	})
	return h
}

// garminITBackdate 把連線的 created_at（匯入 floor）往前推，讓測試用的「幾小時前」活動不會被當成連接之前的資料略過。
func garminITBackdate(t *testing.T, pool *pgxpool.Pool, userID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE user_integrations SET created_at = now() - interval '30 days' WHERE user_id=$1 AND provider='garmin'`, userID); err != nil {
		t.Fatal(err)
	}
}

func garminITWaitEvents(t *testing.T, pool *pgxpool.Pool, gid, status string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var n int
	for time.Now().Before(deadline) {
		_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM integration_events WHERE provider_user_id=$1 AND status=$2`, gid, status).Scan(&n)
		if n >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("events with status %s: %d, want %d", status, n, want)
}

func garminITPush(t *testing.T, h *GarminHandler, gid, summary string, start int64) {
	t.Helper()
	raw := fmt.Sprintf(`{"activities":[{"userId":%q,"summaryId":%q,"activityType":"RUNNING","startTimeInSeconds":%d,"durationInSeconds":1800,"distanceInMeters":5000,"deviceName":"Forerunner 265"}]}`, gid, summary, start)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, newPostRequest("/webhook/"+testGarminToken+"/activities", raw))
	if rec.Code != 200 {
		t.Fatalf("push: %d", rec.Code)
	}
}

// 重放不雙發獎勵（審查缺漏測試 #7）：事件被重新處理（例如租約到期後的掃描）時，匯入是冪等的，里程獎勵紀錄不會增加。
func TestGarminIT_ReprocessedEventDoesNotDoubleCountOrAward(t *testing.T) {
	pool := garminITPool(t)
	garminITSetKey(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	uid := garminITUser(t, pool)
	gid := "itwg-reproc-" + garminITUnique()
	garminITCleanEvents(t, pool, gid)
	if _, err := repo.SaveGarmin(ctx, garminITSaveInput(uid, gid)); err != nil {
		t.Fatal(err)
	}
	garminITBackdate(t, pool, uid)
	h := garminITHandler(t, pool)
	garminITPush(t, h, gid, "reproc-1", time.Now().Add(-4*time.Hour).Unix())
	garminITWaitEvents(t, pool, gid, "done", 1)
	count := func(q string) int {
		var n int
		_ = pool.QueryRow(ctx, q, uid).Scan(&n)
		return n
	}
	acts := count(`SELECT count(*) FROM activities WHERE user_id=$1 AND source='garmin'`)
	awards := count(`SELECT count(*) FROM mileage_exp_events WHERE user_id=$1`)
	var km string
	_ = pool.QueryRow(ctx, `SELECT total_km::text FROM users WHERE id=$1`, uid).Scan(&km)
	if acts != 1 {
		t.Fatalf("activities after first processing: %d", acts)
	}
	// 事件被「殺程序後重放」：改回 pending 且租約到期，由新的處理器掃描
	if _, err := pool.Exec(ctx, `UPDATE integration_events SET status='pending', result=NULL, processed_at=NULL, next_attempt_at=now() - interval '1 second' WHERE provider_user_id=$1`, gid); err != nil {
		t.Fatal(err)
	}
	h2 := garminITHandler(t, pool)
	if n := h2.SweepPending(ctx); n != 1 {
		t.Fatalf("sweep processed %d", n)
	}
	var result string
	_ = pool.QueryRow(ctx, `SELECT COALESCE(result,'') FROM integration_events WHERE provider_user_id=$1`, gid).Scan(&result)
	if result != "exists" {
		t.Fatalf("replayed event result = %q, want exists", result)
	}
	if got := count(`SELECT count(*) FROM activities WHERE user_id=$1 AND source='garmin'`); got != acts {
		t.Fatalf("activities after replay: %d, want %d", got, acts)
	}
	if got := count(`SELECT count(*) FROM mileage_exp_events WHERE user_id=$1`); got != awards {
		t.Fatalf("mileage award events after replay: %d, want %d (no double award)", got, awards)
	}
	var km2 string
	_ = pool.QueryRow(ctx, `SELECT total_km::text FROM users WHERE id=$1`, uid).Scan(&km2)
	if km2 != km {
		t.Fatalf("users.total_km changed on replay: %s -> %s", km, km2)
	}
}

// 舊孿生探測矩陣的另一半（缺漏測試 #10）：舊 Terra 列的 external_id 對不上，但指紋相同 → 直連列被標為重複、不發獎勵。
func TestGarminIT_FingerprintTwinOfTerraRowIsFlaggedNotRewarded(t *testing.T) {
	pool := garminITPool(t)
	garminITSetKey(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	uid := garminITUser(t, pool)
	gid := "itwg-fp-" + garminITUnique()
	garminITCleanEvents(t, pool, gid)
	if _, err := repo.SaveGarmin(ctx, garminITSaveInput(uid, gid)); err != nil {
		t.Fatal(err)
	}
	garminITBackdate(t, pool, uid)
	start := time.Now().Add(-5 * time.Hour).Unix()
	// 舊 Terra 列：external_id 與 Garmin summaryId 無關，但（開始秒、距離公尺、秒數）相同
	if _, err := pool.Exec(ctx, `INSERT INTO activities (user_id, distance_km, duration_s, avg_pace_s, recorded_at, processed, source, external_id, fingerprint)
		VALUES ($1, 5, 1800, 360, to_timestamp($2), TRUE, 'garmin', 'terra-unrelated-id', $3)`, uid, start, fingerprintOf(start, 5000, 1800)); err != nil {
		t.Fatal(err)
	}
	var awardsBefore int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM mileage_exp_events WHERE user_id=$1`, uid).Scan(&awardsBefore)
	h := garminITHandler(t, pool)
	garminITPush(t, h, gid, "fp-twin-1", start)
	garminITWaitEvents(t, pool, gid, "done", 1)
	var flagged bool
	var reason string
	if err := pool.QueryRow(ctx, `SELECT flagged, COALESCE(flag_reason,'') FROM activities WHERE user_id=$1 AND external_id='gc:fp-twin-1'`, uid).Scan(&flagged, &reason); err != nil {
		t.Fatal(err)
	}
	if !flagged || reason != "duplicate" {
		t.Fatalf("fingerprint twin must be flagged duplicate: flagged=%v reason=%q", flagged, reason)
	}
	var result string
	_ = pool.QueryRow(ctx, `SELECT COALESCE(result,'') FROM integration_events WHERE provider_user_id=$1`, gid).Scan(&result)
	if result != "duplicate" {
		t.Fatalf("event result = %q", result)
	}
	var awardsAfter int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM mileage_exp_events WHERE user_id=$1`, uid).Scan(&awardsAfter)
	if awardsAfter != awardsBefore {
		t.Fatalf("a flagged duplicate must not be rewarded: %d -> %d", awardsBefore, awardsAfter)
	}
}

// migration 199 沒套用時（審查缺漏測試 #12）：接收端回 503＋告警（Garmin 會排隊重送，不丟資料），不 panic、不 500。
func TestGarminIT_MissingSchemaGives503AndAlert(t *testing.T) {
	pool := garminITPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS itwg_empty`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS itwg_empty`) })
	cfg := pool.Config().Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = "itwg_empty" // 空 schema：所有資料表都不存在
	emptyPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(emptyPool.Close)

	var alerts []string
	h := NewGarminHandler(GarminConfig{WebhookToken: testGarminToken}, GarminDeps{DB: emptyPool})
	h.alertFn = func(kind, title, detail string) { alerts = append(alerts, kind+"|"+detail) }
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		h.Drain(c)
	})
	for _, kind := range []string{"activities", "deregistrations", "permissions"} {
		raw := `{"activities":[{"userId":"x","summaryId":"y","activityType":"RUNNING","startTimeInSeconds":1790000000}],"deregistrations":[{"userId":"x"}],"userPermissionsChange":[{"userId":"x","changeTimeInSeconds":1790000000}]}`
		rec := httptest.NewRecorder()
		h.Router().ServeHTTP(rec, newPostRequest("/webhook/"+testGarminToken+"/"+kind, raw))
		if rec.Code != 503 {
			t.Fatalf("%s with schema missing: %d, want 503", kind, rec.Code)
		}
	}
	if len(alerts) == 0 || !strings.HasPrefix(alerts[0], "garmin_webhook_err|") || !strings.Contains(alerts[0], "migration 199") {
		t.Fatalf("expected an alert that points at migration 199: %v", alerts)
	}
	// 啟動掃描與每日維護也不 panic（只回錯誤）
	h.startupDelay = time.Millisecond
	h.StartupSweep(ctx)
	if _, err := h.MaintainDaily(ctx); err == nil {
		t.Fatal("MaintainDaily should report an error when the schema is missing")
	}
	if _, err := h.DirectStats(ctx); err == nil {
		t.Fatal("DirectStats should report an error when the schema is missing")
	}
}

func TestGarminIT_BlocklistRepoMethodsIdempotent(t *testing.T) {
	pool := garminITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	uid := garminITUser(t, pool)
	gid := "itwg-blk-" + garminITUnique()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM garmin_blocklist WHERE garmin_user_id=$1 OR user_id=$2::uuid`, gid, uid)
	})
	for i := 0; i < 2; i++ { // 冪等
		if err := repo.AddGarminBlock(ctx, uid, gid, "abuse", ""); err != nil {
			t.Fatalf("add #%d: %v", i, err)
		}
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM garmin_blocklist WHERE user_id=$1::uuid`, uid).Scan(&n)
	if n != 1 {
		t.Fatalf("rows=%d, want 1", n)
	}
	if b, _ := repo.GarminBlocked(ctx, uid, ""); !b {
		t.Fatal("blocked by user id")
	}
	if b, _ := repo.GarminBlocked(ctx, "", gid); !b {
		t.Fatal("blocked by garmin id")
	}
	if err := repo.AddGarminBlock(ctx, "", "", "x", ""); err == nil {
		t.Fatal("a block without a target must be rejected")
	}
	if n, err := repo.RemoveGarminBlock(ctx, uid); err != nil || n != 1 {
		t.Fatalf("remove: %d %v", n, err)
	}
	if b, _ := repo.GarminBlocked(ctx, uid, gid); b {
		t.Fatal("unblocked")
	}
	if n, _ := repo.RemoveGarminBlock(ctx, uid); n != 0 {
		t.Fatal("second remove removes nothing")
	}
}
