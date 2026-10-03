package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// 突波（審查缺漏測試 #8）：On Hold 解除後 Garmin 會短時間湧入多個大 POST。8 個並發的 >1MB／2,000 筆推送，
// 全部在 30 秒內回精確 200、事件全數落地並處理完（解析並發受 parseSem 限制）。
func TestWebhook_SurgeAllAckedWithinDeadline(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	const posts, perPost = 8, 2000
	start := time.Now().Add(-48 * time.Hour).Unix()
	var wg sync.WaitGroup
	codes := make([]int, posts)
	t0 := time.Now()
	for p := 0; p < posts; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			acts := make([]map[string]any, 0, perPost)
			for i := 0; i < perPost; i++ {
				acts = append(acts, synthActivity(tUser1, fmt.Sprintf("SURGE-%d-%d", p, i), start+int64(p*perPost+i)))
			}
			b, _ := json.Marshal(synthPush(acts...))
			if len(b) < 1<<20 {
				t.Errorf("synthetic body only %d bytes", len(b))
			}
			rec := tg.post("/webhook/"+testGarminToken+"/activities", bytes.NewReader(b), nil)
			codes[p] = rec.Code
		}(p)
	}
	wg.Wait()
	if el := time.Since(t0); el > 30*time.Second {
		t.Fatalf("surge took %s (>30s)", el)
	}
	for p, c := range codes {
		if c != http.StatusOK {
			t.Fatalf("post %d: %d", p, c)
		}
	}
	if got := tg.store.count(); got != posts*perPost {
		t.Fatalf("persisted %d events, want %d", got, posts*perPost)
	}
	tg.store.waitSettled(t, 60*time.Second)
}

// 時間邊界（缺漏測試 #9／F-9）：recorded_at 永遠是開始時間的 UTC 瞬間，不受 offset／台北午夜影響。
func TestMapGarminActivity_TaipeiMidnightBoundaryKeepsInstant(t *testing.T) {
	// 2026-10-03 23:59:30 +08:00 與 2026-10-04 00:00:30 +08:00（台北午夜前後）
	before := time.Date(2026, 10, 3, 23, 59, 30, 0, time.FixedZone("TPE", 8*3600)).Unix()
	after := time.Date(2026, 10, 4, 0, 0, 30, 0, time.FixedZone("TPE", 8*3600)).Unix()
	for name, start := range map[string]int64{"before midnight": before, "after midnight": after} {
		a := baseStored()
		a.StartTime = start
		na, skip := mapGarminActivity("u", time.Time{}, a)
		if skip != "" || na.RecordedAt.Unix() != start || na.RecordedAt.Location() != time.UTC {
			t.Fatalf("%s: %v %q", name, na, skip)
		}
	}
	if after-before != 60 {
		t.Fatal("sanity")
	}
	// 賽事 end_date 邊界：開始時間在賽末之前、結束時間跨過賽末的活動，recorded_at 仍是開始時間（晚到同步也計入）
	a := baseStored()
	a.StartTime = before
	a.Duration, a.Distance = 3600, 10000
	na, _ := mapGarminActivity("u", time.Time{}, a)
	if !na.RecordedAt.Before(time.Unix(after, 0)) {
		t.Fatal("an activity that started before midnight must be recorded before midnight even if it ended after")
	}
}

func TestWebhookDoesNotTouchUnrelatedKeysOfOtherKinds(t *testing.T) {
	// 同一個 body 同時含 activities 與 deregistrations：每個端點只讀自己的鍵（避免用錯端點就誤觸發撤銷）
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	body := map[string]any{
		"activities":      []map[string]any{synthActivity(tUser1, "MIX-1", time.Now().Add(-time.Hour).Unix())},
		"deregistrations": []map[string]any{{"userId": tUser1}},
	}
	if rec := tg.postJSON("activities", body); rec.Code != 200 {
		t.Fatalf("activities endpoint: %d", rec.Code)
	}
	rows := tg.store.rows()
	if len(rows) != 1 || rows[0].EventType != garminEvActivity {
		t.Fatalf("activities endpoint must only read 'activities': %+v", rows)
	}
	if rec := tg.postJSON("deregistrations", body); rec.Code != 200 {
		t.Fatalf("dereg endpoint: %d", rec.Code)
	}
	if got := tg.store.count(); got != 2 {
		t.Fatalf("dereg endpoint must only read 'deregistrations': total events %d, want 2", got)
	}
	_ = httptest.NewRecorder
}
