//go:build integration

package integration

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 毒訊息防護（真 PG）：推送中的 NUL／控制字元不得讓整批 INSERT 失敗（會被 503 無限重送）。
func TestGarminIT_ControlCharsInPushDoNotPoisonTheBatch(t *testing.T) {
	pool := garminITPool(t)
	garminITSetKey(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	uid := garminITUser(t, pool)
	gid := "itwg-poison-" + garminITUnique()
	garminITCleanEvents(t, pool, gid)
	if _, err := repo.SaveGarmin(ctx, garminITSaveInput(uid, gid)); err != nil {
		t.Fatal(err)
	}
	h := NewGarminHandler(GarminConfig{WebhookToken: testGarminToken}, GarminDeps{DB: pool})
	h.alertFn = func(kind, title, detail string) {}
	h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) { return garminResInserted, nil }
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		h.Drain(c)
	})
	// userId 與 deviceName 夾帶 NUL（JSON 跳脫 \u0000）與其他控制字元
	raw := `{"activities":[
		{"userId":"` + gid + `\u0000","summaryId":"poison-1\u0007","activityType":"RUNNING","startTimeInSeconds":1790000000,"durationInSeconds":1800,"distanceInMeters":5000,"deviceName":"Fore\u0000runner"},
		{"userId":"` + gid + `","summaryId":"poison-2","activityType":"RUNNING","startTimeInSeconds":1790000100,"durationInSeconds":1800,"distanceInMeters":5000}]}`
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, newPostRequest("/webhook/"+testGarminToken+"/activities", raw))
	if rec.Code != 200 {
		t.Fatalf("a push with control characters must be accepted (got %d): the whole batch would otherwise be retried forever", rec.Code)
	}
	deadline := time.Now().Add(3 * time.Second)
	var n int
	for time.Now().Before(deadline) {
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM integration_events WHERE provider_user_id=$1 AND status='done'`, gid).Scan(&n)
		if n == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n != 2 {
		t.Fatalf("both activities must be persisted and processed, done=%d", n)
	}
	var payload string
	_ = pool.QueryRow(ctx, `SELECT payload::text FROM integration_events WHERE dedupe_key=$1`, "act:"+gid+":poison-1").Scan(&payload)
	if !strings.Contains(payload, "Forerunner") {
		t.Fatalf("sanitized payload: %s", payload)
	}
}
