//go:build integration

package gpscalib

// candidateSQL 排除直連手錶列（COROS GA 契約 §1／§3.4、計畫 S4）的真實 Postgres 驗證：
// COROS MCP（source='coros'＋external_id 'mcp:%'）與 Garmin 直連（source='garmin'＋'gc:%'）一律不進 GPS 距離校正候選；
// 其餘來源（Strava、Terra／Partner 的 coros／garmin、polar…）照舊。需要 DOR_TEST_DATABASE_URL。

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCandidateSQLExcludesDirectWatchRows(t *testing.T) {
	dsn := os.Getenv("DOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("DOR_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	// 先註冊＝最後才執行（t.Cleanup 後進先出）：下面的資料清理還要用 pool。⚠️ 不能寫 defer pool.Close()——
	// defer 在測試函式返回時就執行，早於 t.Cleanup，清理的 DELETE 會全部對已關閉的連線池失敗並被靜默吞掉。
	t.Cleanup(pool.Close)

	suffix := time.Now().Format("150405.000000000")
	var uid string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, handle, name, password_hash) VALUES ($1,$2,'Test','x') RETURNING id::text`,
		"gpscalib-cand-"+suffix+"@example.com", "gcand_"+suffix).Scan(&uid); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM gps_calib_pairs WHERE user_id=$1`, uid)
		_, _ = pool.Exec(bg, `DELETE FROM activities WHERE user_id=$1`, uid)
		_, _ = pool.Exec(bg, `DELETE FROM gps_runs WHERE user_id=$1`, uid)
		_, _ = pool.Exec(bg, `DELETE FROM users WHERE id=$1::uuid`, uid)
	})

	// GPS 趟：結束於 2 小時前、長 40 分鐘 → 起點 = ended − 2400s
	ended := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	gpsStart := ended.Add(-40 * time.Minute)
	if _, err := pool.Exec(ctx, `INSERT INTO gps_runs (user_id, started_at, ended_at, distance_km, duration_s, avg_pace_s) VALUES ($1,$2,$3,5.5,2400,436)`,
		uid, gpsStart, ended); err != nil {
		t.Fatalf("seed gps run: %v", err)
	}
	type seed struct {
		source, ext string
		offset      time.Duration
		want        bool // 是否應該成為候選
	}
	seeds := []seed{
		{"strava", "str-1", 60 * time.Second, true},
		{"coros", "terra-summary-1", 90 * time.Second, true},    // Terra
		{"coros", "480000000000000001", 30 * time.Second, true}, // Partner API（純 labelId）
		{"garmin", "terra-summary-2", 45 * time.Second, true},   // Terra
		{"polar", "pol-1", 10 * time.Second, true},
		{"coros", "mcp:480000000000000002", 60 * time.Second, false}, // COROS MCP 直連：排除
		{"garmin", "gc:abc123", 60 * time.Second, false},             // Garmin 直連：排除
	}
	// external_id 加上每次執行唯一的後綴（activities 的 (source, external_id) 全域唯一；避免與同一個測試庫裡其他
	// 測試或殘留資料撞號）。後綴加在尾端，mcp:／gc: 前綴語意不變。
	for i := range seeds {
		seeds[i].ext += "-" + suffix
	}
	ids := map[string]seed{}
	for i, s := range seeds {
		var id string
		start := gpsStart.Add(s.offset)
		if err := pool.QueryRow(ctx, `
			INSERT INTO activities (user_id, distance_km, duration_s, avg_pace_s, recorded_at, processed, source, external_id)
			VALUES ($1,$2,2400,436,$3,TRUE,$4,$5) RETURNING id::text`, uid, 5.3+float64(i)/100, start, s.source, s.ext).Scan(&id); err != nil {
			t.Fatalf("seed %s %s: %v", s.source, s.ext, err)
		}
		ids[id] = s
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	pairs, err := loadCandidatePairs(ctx, tx, uid, nil)
	if err != nil {
		t.Fatalf("loadCandidatePairs: %v", err)
	}
	var got []string
	for _, p := range pairs {
		s, ok := ids[p.ExtActivityID]
		if !ok {
			t.Fatalf("unexpected candidate %s", p.ExtActivityID)
		}
		if !s.want {
			t.Fatalf("direct-watch row %s %s must NOT be a calibration candidate", s.source, s.ext)
		}
		got = append(got, s.source+"/"+s.ext)
	}
	sort.Strings(got)
	var want []string
	for _, s := range seeds {
		if s.want {
			want = append(want, s.source+"/"+s.ext)
		}
	}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}
