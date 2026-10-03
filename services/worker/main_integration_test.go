//go:build integration

package main

// resolveCrossSourceDups 的優先序 SQL（與 services/api/internal/profile/dedup.go crossSourceRankSQL 逐字同步，
// COROS GA 契約 §2.7）在真實 Postgres 上的行為：直連手錶勝過 Strava（即使使用者偏好 Strava）、
// 先前缺的 polar／suunto／wahoo 現在排在 strava 之前。需要 DOR_TEST_DATABASE_URL（本機拋棄式容器）。

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestResolveCrossSourceDups_RealSQL(t *testing.T) {
	dsn := os.Getenv("DOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("DOR_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	// 先註冊＝最後才執行（t.Cleanup 後進先出）：下面 user() 登記的資料清理還要用 pool。⚠️ 不能寫 defer pool.Close()
	// ——defer 在測試函式返回時就執行，早於 t.Cleanup，清理的 DELETE 會對已關閉的連線池失敗並被靜默吞掉。
	t.Cleanup(pool.Close)
	w := &Worker{db: pool}
	suffix := time.Now().Format("150405.000000000")

	user := func(label string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO users (email, handle, name, password_hash) VALUES ($1,$2,'T','x') RETURNING id::text`,
			"wk-"+label+"-"+suffix+"@example.com", "wk_"+label+"_"+suffix).Scan(&id); err != nil {
			t.Fatalf("user: %v", err)
		}
		t.Cleanup(func() {
			bg := context.Background()
			_, _ = pool.Exec(bg, `DELETE FROM activities WHERE user_id=$1`, id)
			_, _ = pool.Exec(bg, `DELETE FROM user_profiles WHERE user_id=$1`, id)
			_, _ = pool.Exec(bg, `DELETE FROM users WHERE id=$1::uuid`, id)
		})
		return id
	}
	act := func(uid, source, ext string, start time.Time, durS int) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO activities (user_id, distance_km, duration_s, avg_pace_s, recorded_at, processed, source, external_id)
			VALUES ($1,5,$2,300,$3,TRUE,$4,$5) RETURNING id::text`, uid, durS, start.UTC(), source, ext).Scan(&id); err != nil {
			t.Fatalf("activity: %v", err)
		}
		return id
	}
	state := func(id string) (bool, string, string) {
		var f bool
		var reason, dup *string
		if err := pool.QueryRow(ctx, `SELECT flagged, flag_reason, dup_of::text FROM activities WHERE id=$1::uuid`, id).Scan(&f, &reason, &dup); err != nil {
			t.Fatal(err)
		}
		r, d := "", ""
		if reason != nil {
			r = *reason
		}
		if dup != nil {
			d = *dup
		}
		return f, r, d
	}
	setPref := func(uid, pref string) {
		if _, err := pool.Exec(ctx, `INSERT INTO user_profiles (user_id, preferred_data_source) VALUES ($1,$2)
			ON CONFLICT (user_id) DO UPDATE SET preferred_data_source=EXCLUDED.preferred_data_source`, uid, pref); err != nil {
			t.Fatalf("pref: %v", err)
		}
	}
	base := time.Now().UTC().Add(-7 * time.Hour).Truncate(time.Second)

	// 1) 偏好 Strava 的使用者：Strava 與 COROS MCP（直連）重疊 → 直連勝（Strava 被標重複）
	u1 := user("pref-strava")
	setPref(u1, "strava")
	mcp := act(u1, "coros", "mcp:wk-"+suffix, base.Add(30*time.Second), 2400)
	str := act(u1, "strava", "str-wk-"+suffix, base, 2433)

	// 2) 沒有偏好：polar（Terra）與 strava 重疊 → polar 勝（以前 polar 落在 ELSE＝比 strava 低）
	u2 := user("polar-vs-strava")
	pol := act(u2, "polar", "pol-wk-"+suffix, base.Add(-3*time.Hour), 2400)
	str2 := act(u2, "strava", "str2-wk-"+suffix, base.Add(-3*time.Hour).Add(10*time.Second), 2433)

	// 3) 偏好 garmin（Terra）：偏好來源（rank 1）勝過直連 COROS
	u3 := user("pref-garmin")
	setPref(u3, "garmin")
	mcp3 := act(u3, "coros", "mcp:wk3-"+suffix, base.Add(-5*time.Hour), 2400)
	gar3 := act(u3, "garmin", "terra-wk3-"+suffix, base.Add(-5*time.Hour).Add(15*time.Second), 2433)

	// 4) 直連 Garmin（gc:）同樣勝過 Strava
	u4 := user("gc-vs-strava")
	gc := act(u4, "garmin", "gc:wk4-"+suffix, base.Add(-1*time.Hour), 2400)
	str4 := act(u4, "strava", "str4-wk-"+suffix, base.Add(-1*time.Hour).Add(5*time.Second), 2433)

	w.resolveCrossSourceDups(ctx)

	if f, _, _ := state(mcp); f {
		t.Fatal("case1: direct watch row must win over Strava even though the user prefers Strava")
	}
	if f, r, d := state(str); !f || r != "cross_source_duplicate" || d != mcp {
		t.Fatalf("case1: Strava must be flagged → direct row: %v %q %q", f, r, d)
	}
	if f, _, _ := state(pol); f {
		t.Fatal("case2: polar must outrank strava (worker previously ranked polar/suunto/wahoo below strava)")
	}
	if f, _, d := state(str2); !f || d != pol {
		t.Fatalf("case2: strava flagged → polar: %v %q", f, d)
	}
	if f, _, _ := state(gar3); f {
		t.Fatal("case3: the preferred external source keeps rank 1")
	}
	if f, _, d := state(mcp3); !f || d != gar3 {
		t.Fatalf("case3: direct row loses to the preferred source: %v %q", f, d)
	}
	if f, _, _ := state(gc); f {
		t.Fatal("case4: Garmin direct (gc:) wins over Strava")
	}
	if f, _, d := state(str4); !f || d != gc {
		t.Fatalf("case4: strava flagged → garmin direct: %v %q", f, d)
	}
}
