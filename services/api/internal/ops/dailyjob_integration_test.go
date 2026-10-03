//go:build integration

package ops

// 每日排程（dailyjob.go）的 Postgres 實作與「疑似靜默中斷」SQL（wearable_silent.go）的真實資料庫測試。
// 需要 DOR_TEST_DATABASE_URL（本機拋棄式容器）；全程不碰 Telegram（sendTG 用假實作）。

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func itPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("DOR_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// advisory lock：同一把鎖兩條連線只有一條拿得到；釋放後可再取得；標記讀寫往返。
func TestPgJobStore_LockExclusiveAndMarkerRoundTrip(t *testing.T) {
	pool := itPool(t)
	ctx := context.Background()
	store := pgJobStore{pool}
	lock := "it_ops_lock_" + time.Now().Format("150405.000000000")

	rel1, ok, err := store.TryLock(ctx, lock)
	if err != nil || !ok {
		t.Fatalf("first TryLock: %v %v", ok, err)
	}
	if _, ok2, err := store.TryLock(ctx, lock); err != nil || ok2 {
		t.Fatalf("second holder must not get the lock while the first holds it (ok=%v err=%v)", ok2, err)
	}
	rel1()
	rel3, ok3, err := store.TryLock(ctx, lock)
	if err != nil || !ok3 {
		t.Fatalf("lock must be free after release: %v %v", ok3, err)
	}
	rel3()

	key := "it_ops_marker_" + time.Now().Format("150405.000000000")
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM app_settings WHERE key=$1`, key) })
	if v, err := store.ReadMarker(ctx, key); err != nil || v != "" {
		t.Fatalf("missing marker reads as empty: %q %v", v, err)
	}
	if err := store.WriteMarker(ctx, key, "2026-10-03"); err != nil {
		t.Fatal(err)
	}
	if v, _ := store.ReadMarker(ctx, key); v != "2026-10-03" {
		t.Fatalf("marker: %q", v)
	}
	if err := store.WriteMarker(ctx, key, "2026-10-04"); err != nil { // upsert
		t.Fatal(err)
	}
	if v, _ := store.ReadMarker(ctx, key); v != "2026-10-04" {
		t.Fatalf("marker upsert: %q", v)
	}
}

// 每日營運報告：真實 app_settings 標記——送失敗不寫、重試成功才寫；另一個實例持有鎖時完全不動；
// 標記已是今天則直接視為完成。
func TestRunDailyJob_RealStore_MarkerOnlyAfterSuccess(t *testing.T) {
	pool := itPool(t)
	ctx := context.Background()
	store := pgJobStore{pool}
	suffix := time.Now().Format("150405.000000000")
	key := "it_ops_job_marker_" + suffix
	lock := "it_ops_job_lock_" + suffix
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM app_settings WHERE key=$1`, key) })

	sends := 0
	failNext := true
	run := func(ctx context.Context) (string, error) {
		if failNext {
			failNext = false
			return "send", errors.New("telegram 500")
		}
		sends++
		return "", nil
	}
	st := &dailyJobState{}
	job := &dailyJob{name: "it", lockName: lock, persistKey: key, state: st, run: run}

	runDailyJob(ctx, store, job, at(8, 0))
	if v, _ := store.ReadMarker(ctx, key); v != "" || sends != 0 {
		t.Fatalf("a failed attempt must leave NO persistent marker: marker=%q sends=%d", v, sends)
	}
	runDailyJob(ctx, store, job, at(8, 5)) // 還沒到 10 分鐘
	if sends != 0 {
		t.Fatal("must wait 10 minutes")
	}
	runDailyJob(ctx, store, job, at(8, 10))
	if v, _ := store.ReadMarker(ctx, key); v != "2026-10-03" || sends != 1 {
		t.Fatalf("success writes the marker: marker=%q sends=%d", v, sends)
	}

	// 另一個進程（全新 state）看到標記已是今天 → 不再執行（重啟後不重送）
	st2 := &dailyJobState{}
	job2 := &dailyJob{name: "it", lockName: lock, persistKey: key, state: st2, run: func(ctx context.Context) (string, error) { sends += 100; return "", nil }}
	runDailyJob(ctx, store, job2, at(8, 30))
	if sends != 1 {
		t.Fatalf("a restarted instance must not re-send when the marker says today is done, sends=%d", sends)
	}

	// 別的實例持有鎖：不執行、不計嘗試
	otherKey := key + "_b"
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM app_settings WHERE key=$1`, otherKey) })
	hold, ok, err := store.TryLock(ctx, lock+"_b")
	if err != nil || !ok {
		t.Fatal("setup lock")
	}
	st3 := &dailyJobState{}
	ran := false
	job3 := &dailyJob{name: "it", lockName: lock + "_b", persistKey: otherKey, state: st3, run: func(ctx context.Context) (string, error) { ran = true; return "", nil }}
	runDailyJob(ctx, store, job3, at(8, 0))
	if ran || st3.attempts != 0 {
		t.Fatalf("lock held by another instance: no run, no attempt (ran=%v attempts=%d)", ran, st3.attempts)
	}
	hold()
	runDailyJob(ctx, store, job3, at(8, 1))
	if !ran {
		t.Fatal("after the lock is released the next tick runs")
	}
}

// ---------- 疑似靜默中斷 SQL（COROS 以 mcp: 活動參與；直連手錶不印名字、保護期看 updated_at）----------

func TestWearableSilentSQL_DirectWatchRules(t *testing.T) {
	pool := itPool(t)
	ctx := context.Background()
	h := &Handler{db: pool}
	suffix := time.Now().Format("150405.000000000")

	mk := func(label string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO users (email, handle, name, password_hash) VALUES ($1,$2,$3,'x') RETURNING id::text`,
			"silent-"+label+"-"+suffix+"@example.com", "sil_"+label+"_"+suffix, "名字"+label).Scan(&id); err != nil {
			t.Fatalf("user: %v", err)
		}
		t.Cleanup(func() {
			bg := context.Background()
			_, _ = pool.Exec(bg, `DELETE FROM activities WHERE user_id=$1`, id)
			_, _ = pool.Exec(bg, `DELETE FROM user_integrations WHERE user_id=$1`, id)
			_, _ = pool.Exec(bg, `DELETE FROM users WHERE id=$1::uuid`, id)
		})
		return id
	}
	conn := func(uid, provider, createdAgo, updatedAgo string) {
		if _, err := pool.Exec(ctx, `INSERT INTO user_integrations (user_id, provider, provider_user_id, access_token, refresh_token, expires_at, created_at, updated_at)
			VALUES ($1,$2,'',$3,'', NOW()+INTERVAL '10 days', NOW()-($4::text)::interval, NOW()-($5::text)::interval)`, uid, provider, "t", createdAgo, updatedAgo); err != nil {
			t.Fatalf("conn: %v", err)
		}
	}
	act := func(uid string, source *string, ext string, ago string) {
		if _, err := pool.Exec(ctx, `INSERT INTO activities (user_id, distance_km, duration_s, avg_pace_s, recorded_at, processed, source, external_id)
			VALUES ($1,5,1500,300,NOW()-($2::text)::interval,TRUE,$3,$4)`, uid, ago, source, nullIfEmpty(ext)); err != nil {
			t.Fatalf("activity: %v", err)
		}
	}
	str := func(s string) *string { return &s }

	// 1) coros_mcp：連線 10 天前、updated 5 天前；近 3 天只有 App GPS（無 mcp: 活動）→ 靜默，名字為空
	u1 := mk("mcp-silent")
	conn(u1, "coros_mcp", "10 days", "5 days")
	act(u1, nil, "", "1 day")
	// 2) coros_mcp：近 3 天有 mcp: 活動 → 不靜默
	u2 := mk("mcp-active")
	conn(u2, "coros_mcp", "10 days", "5 days")
	act(u2, nil, "", "1 day")
	act(u2, str("coros"), "mcp:100000000000000001", "1 day")
	// 3) coros_mcp：近 3 天只有「Terra 的 coros」活動（external_id 非 mcp:）→ 對 coros_mcp 連線來說仍是靜默
	u3 := mk("mcp-only-terra-rows")
	conn(u3, "coros_mcp", "10 days", "5 days")
	act(u3, str("coros"), "terra-xyz-"+suffix, "1 day")
	// 4) 保護期：coros_mcp 連線 10 天前建立、但 updated_at 才 1 天前（剛重新授權）→ 不判靜默
	u4 := mk("mcp-reauthed")
	conn(u4, "coros_mcp", "10 days", "1 day")
	act(u4, nil, "", "1 day")
	// 5) garmin（直連手錶）：靜默、名字為空
	u5 := mk("garmin-silent")
	conn(u5, "garmin", "10 days", "5 days")
	act(u5, nil, "", "1 day")
	// 6) strava：靜默，名字保留（保護期看 created_at）
	u6 := mk("strava-silent")
	conn(u6, "strava", "10 days", "1 hour") // updated_at 很新（token 每 6 小時刷新），但保護期看 created_at → 仍判靜默
	act(u6, nil, "", "1 day")
	// 7) strava 剛連 1 天（created_at 新）→ 受保護
	u7 := mk("strava-new")
	conn(u7, "strava", "1 day", "1 day")
	act(u7, nil, "", "1 day")

	list := h.buildWearableSilentWarnings(ctx)
	byProvider := map[string][]WearableSilentConnection{}
	for _, s := range list {
		byProvider[s.Provider] = append(byProvider[s.Provider], s)
	}
	// 只看這次測試建立的帳號（資料庫可能還有別的列）：以名字／計數交叉比對
	var named []string
	mcpSilent, garminSilent := 0, 0
	for _, s := range list {
		if s.DisplayName != "" {
			named = append(named, s.Provider+":"+s.DisplayName)
		}
		switch s.Provider {
		case "coros_mcp":
			mcpSilent++
			if s.DisplayName != "" {
				t.Fatalf("direct-watch silent rows must never carry a display name: %+v", s)
			}
		case "garmin", "coros":
			if s.DisplayName != "" {
				t.Fatalf("direct-watch silent rows must never carry a display name: %+v", s)
			}
			if s.Provider == "garmin" {
				garminSilent++
			}
		}
	}
	if mcpSilent != 2 { // u1、u3
		t.Fatalf("coros_mcp silent connections = %d, want 2 (u1: no mcp rows; u3: only Terra rows); active/reauthed excluded", mcpSilent)
	}
	if garminSilent != 1 {
		t.Fatalf("garmin silent = %d, want 1", garminSilent)
	}
	foundStrava := false
	for _, n := range named {
		if n == "strava:名字strava-silent" {
			foundStrava = true
		}
		if strings.Contains(n, "strava-new") {
			t.Fatalf("a freshly created strava connection is protected by created_at: %v", named)
		}
	}
	if !foundStrava {
		t.Fatalf("strava silent connection keeps its display name (named providers unchanged): %v", named)
	}
	// 格式：直連 provider 彙總成人數行
	lines := formatWearableSilentLines(list)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "COROS 直連：2 人疑似已停止同步") || !strings.Contains(joined, "GARMIN：1 人疑似已停止同步") {
		t.Fatalf("count-only lines missing: %s", joined)
	}
	for _, bad := range []string{"mcp-silent", "garmin-silent", "mcp-only-terra-rows"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("direct-watch lines must not contain names: %s", joined)
		}
	}
	_ = byProvider
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
