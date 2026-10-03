//go:build integration

package race

// 進度頁每日歷程（GET /races/{id}/my-daily-activities）對真實 Postgres：Garmin 直連列要帶 source 與 device_name
// （供前台顯示「Garmin 〈型號〉」歸屬），且仍遵守既有的里程窗與來源閘門（Strava 排除、flagged 排除、只回本人）。
// 需要 DOR_TEST_DATABASE_URL（已套用 migrations，含 197 的 activities.device_name）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/auth"
)

func dhPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("DOR_TEST_DATABASE_URL not set; skipping daily-history SQL integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func dhUser(t *testing.T, pool *pgxpool.Pool, label string) string {
	t.Helper()
	suffix := time.Now().Format("150405.000000000")
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO users (email, handle, name, password_hash) VALUES ($1,$2,'DH','x') RETURNING id::text`,
		"dh-"+label+"-"+suffix+"@example.invalid", "dh_"+label+"_"+suffix).Scan(&id); err != nil {
		t.Fatalf("user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM activities WHERE user_id=$1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1::uuid`, id)
	})
	return id
}

func TestDailyHistoryIT_GarminRowsCarrySourceAndDeviceName(t *testing.T) {
	pool := dhPool(t)
	ctx := context.Background()
	uid, other := dhUser(t, pool, "me"), dhUser(t, pool, "other")

	var raceID string
	if err := pool.QueryRow(ctx, `INSERT INTO races (slug, title, distances, start_date, end_date, external_data)
		VALUES ($1,'DH IT race',ARRAY[5],now() - interval '30 days', now() + interval '30 days', TRUE) RETURNING id::text`,
		"dh-it-"+time.Now().Format("150405.000000000")).Scan(&raceID); err != nil {
		t.Fatalf("race: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM races WHERE id=$1::uuid`, raceID) })

	now := time.Now().UTC()
	ins := func(u string, source, ext, device *string, recorded time.Time, flagged bool) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO activities (user_id, distance_km, duration_s, avg_pace_s, recorded_at, processed, source, external_id, device_name, flagged)
			VALUES ($1, 5, 1800, 360, $2, TRUE, $3, $4, $5, $6)`, u, recorded, source, ext, device, flagged); err != nil {
			t.Fatalf("insert activity: %v", err)
		}
	}
	str := func(s string) *string { return &s }
	ins(uid, nil, nil, nil, now.Add(-24*time.Hour), false)                                        // App GPS
	ins(uid, str("garmin"), str("gc:dh-1"), str("Forerunner 265"), now.Add(-48*time.Hour), false) // Garmin 有型號
	ins(uid, str("garmin"), str("gc:dh-2"), nil, now.Add(-72*time.Hour), false)                   // Garmin 型號不明
	ins(uid, str("strava"), str("s-dh-3"), str("StravaWatch"), now.Add(-96*time.Hour), false)     // Strava：永遠被閘門排除
	ins(uid, str("garmin"), str("gc:dh-4"), str("Forerunner 955"), now.Add(-120*time.Hour), true) // flagged：排除
	ins(other, str("garmin"), str("gc:dh-5"), str("Venu 3"), now.Add(-30*time.Hour), false)       // 別人的：不得出現

	days, err := NewService(NewRepository(pool), nil, nil).GetMyDailyActivities(ctx, raceID, uid)
	if err != nil {
		t.Fatalf("GetMyDailyActivities: %v", err)
	}
	var acts []DailyActivity
	for _, d := range days {
		acts = append(acts, d.Activities...)
	}
	if len(acts) != 3 {
		t.Fatalf("activities in window = %d, want 3 (GPS + 2 Garmin): %+v", len(acts), acts)
	}
	byExt := map[string]DailyActivity{}
	for _, a := range acts {
		byExt[a.ExternalID] = a
	}
	if gps, ok := byExt[""]; !ok || gps.Source != "" || gps.DeviceName != nil {
		t.Fatalf("App GPS row: %+v", gps)
	}
	if g := byExt["gc:dh-1"]; g.Source != "garmin" || g.DeviceName == nil || *g.DeviceName != "Forerunner 265" {
		t.Fatalf("Garmin row with a model: %+v", g)
	}
	if g := byExt["gc:dh-2"]; g.Source != "garmin" || g.DeviceName != nil {
		t.Fatalf("Garmin row without a model: %+v", g)
	}

	// HTTP 端點（本人視圖）：JSON 帶 source 與 device_name；不含別人的型號、Strava 型號、flagged 的型號
	h := NewHandler(NewService(NewRepository(pool), nil, nil), nil)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), auth.CtxKeyUserID, uid)))
		})
	})
	r.Get("/races/{raceID}/my-daily-activities", h.MyDailyActivities)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/races/"+raceID+"/my-daily-activities", nil))
	if rec.Code != 200 {
		t.Fatalf("endpoint: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"source":"garmin"`, `"device_name":"Forerunner 265"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("response lacks %s: %s", want, body)
		}
	}
	for _, leak := range []string{"Venu 3", "StravaWatch", "Forerunner 955"} {
		if strings.Contains(body, leak) {
			t.Fatalf("response must not contain %q: %s", leak, body)
		}
	}
	var parsed struct {
		Days []struct {
			Activities []map[string]any `json:"activities"`
		} `json:"days"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	for _, d := range parsed.Days {
		for _, a := range d.Activities {
			if a["source"] == "garmin" && a["external_id"] == "gc:dh-2" {
				if _, has := a["device_name"]; has {
					t.Fatalf("an unknown model must not emit device_name: %v", a)
				}
			}
		}
	}
}
