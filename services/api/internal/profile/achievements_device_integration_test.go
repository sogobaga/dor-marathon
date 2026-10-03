//go:build integration

package profile

// 成就月曆「單日明細」（GET /profile/achievements/day，本人視圖）對真實 Postgres：Garmin 直連列要帶 source 與
// device_name（供前台顯示「Garmin 〈型號〉」歸屬）；型號為 NULL 時不輸出該欄位。需要 DOR_TEST_DATABASE_URL。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/auth"
)

func TestAchievementsDayIT_GarminRowsCarryDeviceName(t *testing.T) {
	dsn := os.Getenv("DOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("DOR_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	suffix := time.Now().Format("150405.000000000")
	var uid string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, handle, name, password_hash) VALUES ($1,$2,'AD','x') RETURNING id::text`,
		"ad-"+suffix+"@example.invalid", "ad_"+suffix).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM activities WHERE user_id=$1`, uid)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1::uuid`, uid)
	})

	// 台北時間中午（避免換日邊界）：用固定日期，兩筆 Garmin（一筆有型號、一筆沒有）＋一筆 App GPS
	loc := time.FixedZone("TPE", 8*3600)
	noon := time.Date(2026, 9, 15, 12, 0, 0, 0, loc).UTC()
	ins := func(source, ext, device any, at time.Time) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO activities (user_id, distance_km, duration_s, avg_pace_s, recorded_at, processed, source, external_id, device_name)
			VALUES ($1, 5, 1800, 360, $2, TRUE, $3, $4, $5)`, uid, at, source, ext, device); err != nil {
			t.Fatalf("insert activity: %v", err)
		}
	}
	ins("garmin", "gc:ad-1", "Forerunner 265", noon)
	ins("garmin", "gc:ad-2", nil, noon.Add(time.Hour))
	ins(nil, nil, nil, noon.Add(2*time.Hour))

	h := NewHandler(pool, nil)
	req := httptest.NewRequest("GET", "/api/v1/profile/achievements/day?date=2026-09-15", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.CtxKeyUserID, uid))
	rec := httptest.NewRecorder()
	h.AchievementsDay(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("achievements day: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"source":"garmin"`) || !strings.Contains(body, `"device_name":"Forerunner 265"`) {
		t.Fatalf("garmin row must carry source and device_name: %s", body)
	}
	if n := strings.Count(body, `"device_name"`); n != 1 {
		t.Fatalf("device_name must appear only on the row that has a model (got %d): %s", n, body)
	}
}
