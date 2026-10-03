//go:build integration

package profile

// 跨來源去重排序（reResolveUser 的真實 SQL，GA 契約 §2.7）與 Dashboard 的 connected_sources 查詢——
// 真實 Postgres（本機拋棄式容器）。需要 DOR_TEST_DATABASE_URL。

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ddEnv struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
}

func newDDEnv(t *testing.T) *ddEnv {
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
	return &ddEnv{t: t, ctx: context.Background(), pool: pool}
}

func (e *ddEnv) user(label string) string {
	suffix := time.Now().Format("150405.000000000")
	var id string
	if err := e.pool.QueryRow(e.ctx, `INSERT INTO users (email, handle, name, password_hash) VALUES ($1,$2,'T','x') RETURNING id::text`,
		"dd-"+label+"-"+suffix+"@example.com", "dd_"+label+"_"+suffix).Scan(&id); err != nil {
		e.t.Fatalf("user: %v", err)
	}
	e.t.Cleanup(func() {
		bg := context.Background()
		_, _ = e.pool.Exec(bg, `DELETE FROM activities WHERE user_id=$1`, id)
		_, _ = e.pool.Exec(bg, `DELETE FROM user_integrations WHERE user_id=$1`, id)
		_, _ = e.pool.Exec(bg, `DELETE FROM user_profiles WHERE user_id=$1`, id)
		_, _ = e.pool.Exec(bg, `DELETE FROM users WHERE id=$1::uuid`, id)
	})
	return id
}

// act 插一筆活動；source=="" 代表 App GPS（source IS NULL，recorded_at 為結束時間）。
func (e *ddEnv) act(uid, source, ext string, start time.Time, durS int) string {
	e.t.Helper()
	var src, extID any
	recorded := start
	if source != "" {
		src, extID = source, ext
	} else {
		recorded = start.Add(time.Duration(durS) * time.Second)
	}
	var id string
	if err := e.pool.QueryRow(e.ctx, `INSERT INTO activities (user_id, distance_km, duration_s, avg_pace_s, recorded_at, processed, source, external_id)
		VALUES ($1,5,$2,300,$3,TRUE,$4,$5) RETURNING id::text`, uid, durS, recorded.UTC(), src, extID).Scan(&id); err != nil {
		e.t.Fatalf("insert activity %s %s: %v", source, ext, err)
	}
	return id
}

func (e *ddEnv) flagged(id string) (bool, string, string) {
	e.t.Helper()
	var f bool
	var reason, dup *string
	if err := e.pool.QueryRow(e.ctx, `SELECT flagged, flag_reason, dup_of::text FROM activities WHERE id=$1::uuid`, id).Scan(&f, &reason, &dup); err != nil {
		e.t.Fatal(err)
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

// 直連手錶勝過 Strava——即使使用者偏好 Strava；舊資料（先到先贏時被標 cross_source_duplicate 的直連列）也會被重新裁決回來。
func TestReResolveUser_DirectWatchBeatsStravaEvenWhenStravaIsPreferred(t *testing.T) {
	e := newDDEnv(t)
	uid := e.user("pref-strava")
	start := time.Now().UTC().Add(-5 * time.Hour).Truncate(time.Second)
	mcp := e.act(uid, "coros", "mcp:dd-"+start.Format("150405"), start.Add(30*time.Second), 2400)
	str := e.act(uid, "strava", "str-dd-"+start.Format("150405"), start, 2433)
	// 模擬舊行為：直連列被標成重複（指向 Strava）
	if _, err := e.pool.Exec(e.ctx, `UPDATE activities SET flagged=TRUE, flag_reason='cross_source_duplicate', dup_of=$2::uuid WHERE id=$1::uuid`, mcp, str); err != nil {
		t.Fatal(err)
	}
	reResolveUser(e.ctx, e.pool, uid, "strava") // 使用者偏好 Strava
	if f, _, _ := e.flagged(mcp); f {
		t.Fatal("the direct watch row must be restored and kept even when the user prefers Strava")
	}
	f, reason, dup := e.flagged(str)
	if !f || reason != "cross_source_duplicate" || dup != mcp {
		t.Fatalf("Strava row must be flagged as the duplicate of the direct row: flagged=%v reason=%q dup_of=%q", f, reason, dup)
	}
}

// 偏好的非 Strava 來源（Terra garmin）排在直連之前；App GPS 恆為最高。
func TestReResolveUser_PreferredNonStravaAndGpsOutrankDirect(t *testing.T) {
	e := newDDEnv(t)
	uid := e.user("pref-garmin")
	start := time.Now().UTC().Add(-8 * time.Hour).Truncate(time.Second)
	mcp := e.act(uid, "coros", "mcp:dd2-"+start.Format("150405"), start.Add(20*time.Second), 2400)
	terraGarmin := e.act(uid, "garmin", "terra-dd2-"+start.Format("150405"), start, 2433)
	reResolveUser(e.ctx, e.pool, uid, "garmin")
	if f, _, _ := e.flagged(terraGarmin); f {
		t.Fatal("the user's preferred external source (Terra garmin) keeps rank 1, above direct rows")
	}
	if f, reason, dup := e.flagged(mcp); !f || reason != "cross_source_duplicate" || dup != terraGarmin {
		t.Fatalf("direct row loses to the preferred source: %v %q %q", f, reason, dup)
	}

	uid2 := e.user("gps-top")
	st2 := time.Now().UTC().Add(-10 * time.Hour).Truncate(time.Second)
	gps := e.act(uid2, "", "", st2, 2500)
	m2 := e.act(uid2, "coros", "mcp:dd3-"+st2.Format("150405"), st2.Add(30*time.Second), 2400)
	s2 := e.act(uid2, "strava", "str-dd3-"+st2.Format("150405"), st2, 2433)
	reResolveUser(e.ctx, e.pool, uid2, "gps")
	if f, _, _ := e.flagged(gps); f {
		t.Fatal("App GPS always wins")
	}
	for name, id := range map[string]string{"mcp": m2, "strava": s2} {
		if f, reason, dup := e.flagged(id); !f || reason != "cross_source_duplicate" || dup != gps {
			t.Fatalf("%s must lose to App GPS: %v %q %q", name, f, reason, dup)
		}
	}
}

// 偏好 Strava 時，Strava 仍然勝過 Terra 的其他來源（舊行為不變）。
func TestReResolveUser_PreferredStravaStillBeatsTerraSources(t *testing.T) {
	e := newDDEnv(t)
	uid := e.user("strava-vs-terra")
	start := time.Now().UTC().Add(-6 * time.Hour).Truncate(time.Second)
	str := e.act(uid, "strava", "str-dd4-"+start.Format("150405"), start, 2433)
	terra := e.act(uid, "polar", "pol-dd4-"+start.Format("150405"), start.Add(10*time.Second), 2400)
	reResolveUser(e.ctx, e.pool, uid, "strava")
	if f, _, _ := e.flagged(str); f {
		t.Fatal("preferred Strava beats a Terra-sourced Polar row")
	}
	if f, _, dup := e.flagged(terra); !f || dup != str {
		t.Fatalf("polar must be flagged: %v %q", f, dup)
	}
}

func TestLoadConnectedSources_RealQuery(t *testing.T) {
	e := newDDEnv(t)
	h := &Handler{db: e.pool}
	uid := e.user("conn")
	ins := func(provider, via string) {
		if _, err := e.pool.Exec(e.ctx, `INSERT INTO user_integrations (user_id, provider, provider_user_id, access_token, refresh_token, expires_at, via)
			VALUES ($1,$2,'',$3,'', NOW()+INTERVAL '1 day',$4)`, uid, provider, "t", via); err != nil {
			t.Fatalf("insert %s: %v", provider, err)
		}
	}
	if srcs, mcp, gd := h.loadConnectedSources(e.ctx, uid); len(srcs) != 0 || mcp || gd {
		t.Fatalf("no connections: %v %v %v", srcs, mcp, gd)
	}
	ins("strava", "direct")
	ins("coros_mcp", "direct")
	ins("garmin", "direct")
	ins("polar", "terra")
	srcs, mcp, gd := h.loadConnectedSources(e.ctx, uid)
	if !reflect.DeepEqual(srcs, []string{"strava", "garmin", "coros", "polar"}) || !mcp || !gd {
		t.Fatalf("connected sources: %v mcp=%v garminDirect=%v", srcs, mcp, gd)
	}
}
