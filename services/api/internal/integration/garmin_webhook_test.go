package integration

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	tUser1 = "a1b2c3d4e5f60718293a4b5c6d7e8f90" // 合成 Garmin userId（32 hex）
	tUser2 = "9f8e7d6c-5b4a-4392-8171-6f5e4d3c2b1a"
	tDor1  = "00000000-0000-4000-8000-000000000001"
	tDor2  = "00000000-0000-4000-8000-000000000002"
)

func TestWebhook_TokenChecks(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)

	// 錯誤 token：404、不落地
	rec := tg.post("/webhook/wrong-token/activities", strings.NewReader(`{"activities":[]}`), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("wrong token: got %d, want 404", rec.Code)
	}
	// 前綴相同但較短的 token 也不行
	rec = tg.post("/webhook/"+testGarminToken[:len(testGarminToken)-1]+"/activities", strings.NewReader(`{}`), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("short token: got %d, want 404", rec.Code)
	}
	// 目前 token 與 PREV token 都接受（輪替重疊期）
	for _, tok := range []string{testGarminToken, testGarminTokenPrev} {
		rec = tg.post("/webhook/"+tok+"/activities", strings.NewReader(`{"activities":[]}`), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("token %q: got %d, want 200", tok[:4], rec.Code)
		}
	}
	if tg.store.count() != 0 {
		t.Fatalf("no events expected, got %d", tg.store.count())
	}
}

func TestWebhook_TokenUnsetOrTooShort_503(t *testing.T) {
	for name, tok := range map[string]string{"unset": "", "short": "short-token"} {
		tg := newTestGarmin(t, func(c *GarminConfig) { c.WebhookToken, c.WebhookTokenPrev = tok, "" })
		rec := tg.post("/webhook/"+tok+"/activities", strings.NewReader(`{}`), nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: got %d, want 503", name, rec.Code)
		}
	}
}

func TestWebhook_BadTokenBurstAlertsOnceWithoutToken(t *testing.T) {
	tg := newTestGarmin(t, nil)
	logs := captureLogs(t)
	const secretTry = "guessed-secret-token-xyz"
	for i := 0; i < 25; i++ {
		tg.post("/webhook/"+secretTry+"/activities", strings.NewReader(`{}`), nil)
	}
	al := tg.alertList()
	if len(al) != 1 || !strings.HasPrefix(al[0], "garmin_bad_token|") {
		t.Fatalf("want exactly one garmin_bad_token alert, got %v", al)
	}
	if strings.Contains(al[0], secretTry) || strings.Contains(al[0], testGarminToken) {
		t.Fatalf("alert leaked a token: %s", al[0])
	}
	if l := logs(); strings.Contains(l, secretTry) || strings.Contains(l, testGarminToken) {
		t.Fatalf("log leaked a token")
	}
}

// 落地先於回應：WriteHeader(200) 被呼叫時事件必須已經 INSERT。
type orderCheckWriter struct {
	*httptest.ResponseRecorder
	store      *fakeGarminStore
	violated   bool
	headerSeen bool
}

func (w *orderCheckWriter) WriteHeader(code int) {
	if code == http.StatusOK && !w.headerSeen {
		w.headerSeen = true
		w.store.mu.Lock()
		done := w.store.insertDone
		w.store.mu.Unlock()
		if !done {
			w.violated = true
		}
	}
	w.ResponseRecorder.WriteHeader(code)
}

func TestWebhook_PersistThenAck_Exact200EmptyBody(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	body, _ := json.Marshal(synthPush(synthActivity(tUser1, "S-1", 1790000000)))
	req := httptest.NewRequest(http.MethodPost, "/webhook/"+testGarminToken+"/activities", bytes.NewReader(body))
	w := &orderCheckWriter{ResponseRecorder: httptest.NewRecorder(), store: tg.store}
	tg.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want exactly 200", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("body must be empty, got %q", w.Body.String())
	}
	if w.violated {
		t.Fatal("200 was written before the event was persisted (persist-then-ack violated)")
	}
	if tg.store.count() != 1 {
		t.Fatalf("want 1 persisted event, got %d", tg.store.count())
	}
}

func TestWebhook_PayloadMinimizedAndWhitelisted(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	// 處理器先卡住，避免事件在斷言前被標 done（payload 不受影響，但這樣最單純）
	rec := tg.postJSON("activities", synthPush(synthActivity(tUser1, "S-2", 1790000100)))
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	rows := tg.store.rows()
	if len(rows) != 1 {
		t.Fatalf("want 1 event, got %d", len(rows))
	}
	var m map[string]any
	if err := json.Unmarshal(rows[0].Payload, &m); err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"startingLatitudeInDegree", "startingLongitudeInDegree", "activityName", "activeKilocalories",
		"steps", "userAccessToken", "userId", "startTimeOffsetInSeconds", "averageSpeedInMetersPerSecond"} {
		if _, ok := m[banned]; ok {
			t.Errorf("payload must not contain %q: %s", banned, rows[0].Payload)
		}
	}
	raw := string(rows[0].Payload)
	for _, banned := range []string{"Morning Run", "25.033", "121.565", "synthetic-user-access-token"} {
		if strings.Contains(raw, banned) {
			t.Errorf("payload leaks %q", banned)
		}
	}
	for _, want := range []string{"summaryId", "activityType", "startTimeInSeconds", "durationInSeconds", "distanceInMeters", "deviceName"} {
		if _, ok := m[want]; !ok {
			t.Errorf("payload missing %q: %s", want, raw)
		}
	}
	if rows[0].DedupeKey == nil || *rows[0].DedupeKey != "act:"+tUser1+":S-2" {
		t.Errorf("dedupe key = %v, want act:<uid>:S-2", rows[0].DedupeKey)
	}
}

func TestWebhook_DuplicatePushIsIdempotent(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	var calls int
	var mu sync.Mutex
	tg.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return garminResInserted, nil
	}
	push := synthPush(synthActivity(tUser1, "S-3", 1790000200))
	for i := 0; i < 3; i++ {
		if rec := tg.postJSON("activities", push); rec.Code != 200 {
			t.Fatalf("push %d: %d", i, rec.Code)
		}
	}
	tg.store.waitSettled(t, 2*time.Second)
	if tg.store.count() != 1 {
		t.Fatalf("duplicate pushes must persist one event, got %d", tg.store.count())
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("handler ran %d times, want 1", calls)
	}
}

func TestWebhook_TypeWhitelist(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	types := map[string]bool{
		"RUNNING": true, "TRAIL_RUNNING": true, "TREADMILL_RUNNING": true, "INDOOR_RUNNING": true, "VIRTUAL_RUNNING": false,
		"VIRTUAL_RUN": true, "ULTRA_RUN": true, "OBSTACLE_RUN": true, "STREET_RUNNING": true, "TRACK_RUNNING": true,
		"WALKING": true, "CASUAL_WALKING": true, "SPEED_WALKING": true, "HIKING": true, "RUCKING": true,
		"Running": true, // 首字大寫也容忍
		"CYCLING": false, "LAP_SWIMMING": false, "WHEELCHAIR_PUSH_RUN": false, "WHEELCHAIR_PUSH_WALK": false,
		"MULTI_SPORT": false, "INDOOR_WALKING": false, "": false,
	}
	var acts []map[string]any
	i := 0
	want := 0
	for ty, ok := range types {
		a := synthActivity(tUser1, fmt.Sprintf("T-%d", i), 1790001000+int64(i))
		a["activityType"] = ty
		acts = append(acts, a)
		if ok {
			want++
		}
		i++
	}
	if rec := tg.postJSON("activities", synthPush(acts...)); rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	if got := tg.store.count(); got != want {
		t.Fatalf("persisted %d events, want %d (whitelist)", got, want)
	}
}

func TestWebhook_FlagsPreservedForMapping(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	a := synthActivity(tUser1, "F-1", 1790002000)
	a["manual"], a["isWebUpload"], a["isParent"], a["parentSummaryId"] = true, true, true, "P-9"
	tg.postJSON("activities", synthPush(a))
	rows := tg.store.rows()
	if len(rows) != 1 {
		t.Fatalf("want 1, got %d", len(rows))
	}
	var s garminStoredActivity
	if err := json.Unmarshal(rows[0].Payload, &s); err != nil {
		t.Fatal(err)
	}
	if !s.Manual || !s.IsWebUpload || !s.IsParent || s.ParentSummaryID != "P-9" {
		t.Fatalf("flags must be preserved for the mapper: %+v", s)
	}
}

func TestWebhook_UnknownUserCountedNotPersisted(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	rec := tg.postJSON("activities", synthPush(
		synthActivity(tUser1, "K-1", 1790003000),
		synthActivity("unknown-user-id", "K-2", 1790003001),
		synthActivity("unknown-user-id-2", "K-3", 1790003002)))
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	if tg.store.count() != 1 {
		t.Fatalf("only the known user's activity may be persisted, got %d", tg.store.count())
	}
	tg.store.waitSettled(t, 2*time.Second)
	// 計數器（記憶體 KV）
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if v, ok, _ := tg.h.kv.Get(context.Background(), "garmin:cnt:unknown:"+tg.h.hourBucket()); ok && v == "2" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	v, ok, _ := tg.h.kv.Get(context.Background(), "garmin:cnt:unknown:"+tg.h.hourBucket())
	t.Fatalf("unknown counter = %q (found=%v), want 2", v, ok)
}

func TestWebhook_UnknownUserBurstAlert(t *testing.T) {
	tg := newTestGarmin(t, nil)
	var acts []map[string]any
	for i := 0; i < 60; i++ {
		acts = append(acts, synthActivity(fmt.Sprintf("ghost-%d", i), fmt.Sprintf("G-%d", i), 1790004000+int64(i)))
	}
	if rec := tg.postJSON("activities", synthPush(acts...)); rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, a := range tg.alertList() {
			if strings.HasPrefix(a, "garmin_unknown_user_burst|") {
				if strings.Contains(a, "ghost") {
					t.Fatalf("alert leaked user ids: %s", a)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected garmin_unknown_user_burst alert, got %v", tg.alertList())
}

func TestWebhook_DeregistrationNullDedupeBothPersist(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	push := map[string]any{"deregistrations": []map[string]any{{"userId": tUser1}}}
	for i := 0; i < 2; i++ {
		if rec := tg.postJSON("deregistrations", push); rec.Code != 200 {
			t.Fatalf("got %d", rec.Code)
		}
	}
	rows := tg.store.rows()
	if len(rows) != 2 {
		t.Fatalf("two deregistration events (NULL dedupe key) must both persist, got %d", len(rows))
	}
	for _, r := range rows {
		if r.DedupeKey != nil || r.EventType != garminEvDeregister {
			t.Fatalf("unexpected row: %+v", r)
		}
	}
}

func TestWebhook_PermissionDedupeByChangeTime(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	mk := func(summary string, ct int64, perms ...string) map[string]any {
		return map[string]any{"userPermissionsChange": []map[string]any{{"userId": tUser1, "summaryId": summary, "changeTimeInSeconds": ct, "permissions": perms}}}
	}
	// 「關→開」兩則事件即使 summaryId 相同，changeTime 不同就必須各自落地（R-C2）
	tg.postJSON("permissions", mk("same-summary", 1790005000))
	tg.postJSON("permissions", mk("same-summary", 1790005060, "ACTIVITY_EXPORT"))
	// 完全相同的重送只算一次
	tg.postJSON("permissions", mk("same-summary", 1790005060, "ACTIVITY_EXPORT"))
	rows := tg.store.rows()
	if len(rows) != 2 {
		t.Fatalf("want 2 permission events, got %d", len(rows))
	}
	if *rows[0].DedupeKey != "perm:"+tUser1+":1790005000" || *rows[1].DedupeKey != "perm:"+tUser1+":1790005060" {
		t.Fatalf("dedupe keys: %q %q", *rows[0].DedupeKey, *rows[1].DedupeKey)
	}
}

func TestWebhook_HiddenBlocksActivitiesOnly(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	tg.h.emergency = func(ctx context.Context) bool { return true }
	if rec := tg.postJSON("activities", synthPush(synthActivity(tUser1, "H-1", 1790006000))); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("hidden: activities want 503, got %d", rec.Code)
	}
	if tg.store.count() != 0 {
		t.Fatal("hidden: nothing may be persisted for activities")
	}
	if rec := tg.postJSON("deregistrations", map[string]any{"deregistrations": []map[string]any{{"userId": tUser1}}}); rec.Code != 200 {
		t.Fatalf("hidden: deregistrations must still be processed, got %d", rec.Code)
	}
	if rec := tg.postJSON("permissions", map[string]any{"userPermissionsChange": []map[string]any{{"userId": tUser1, "changeTimeInSeconds": 1790006100}}}); rec.Code != 200 {
		t.Fatalf("hidden: permissions must still be processed, got %d", rec.Code)
	}
	if tg.store.count() != 2 {
		t.Fatalf("want 2 events (dereg+permission), got %d", tg.store.count())
	}
}

func TestWebhook_BodyLimit413_AndGzip(t *testing.T) {
	tg := newTestGarmin(t, func(c *GarminConfig) { c.WebhookMaxBytes = 64 << 10 })
	tg.store.addUser(tUser1, tDor1)

	// 超過上限（未壓縮）→ 413
	big := `{"activities":[` + strings.Repeat(`{"userId":"x","activityType":"RUNNING"},`, 4000) + `{"userId":"x"}]}`
	if rec := tg.postRaw("activities", big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize: got %d, want 413", rec.Code)
	}

	// gzip 正常：可解、可落地
	raw, _ := json.Marshal(synthPush(synthActivity(tUser1, "Z-1", 1790007000)))
	var zb bytes.Buffer
	gz := gzip.NewWriter(&zb)
	gz.Write(raw)
	gz.Close()
	rec := tg.post("/webhook/"+testGarminToken+"/activities", bytes.NewReader(zb.Bytes()), map[string]string{"Content-Encoding": "gzip"})
	if rec.Code != 200 {
		t.Fatalf("gzip: got %d", rec.Code)
	}
	if tg.store.count() != 1 {
		t.Fatalf("gzip body must be decoded and persisted, got %d", tg.store.count())
	}

	// 壞的 gzip 串流 → 非 200（讓 Garmin 重送）
	rec = tg.post("/webhook/"+testGarminToken+"/activities", strings.NewReader("this is not gzip"), map[string]string{"Content-Encoding": "gzip"})
	if rec.Code == 200 {
		t.Fatalf("corrupt gzip must not be acked with 200")
	}
}

func TestWebhook_InflateLimit(t *testing.T) {
	// 解壓後超過上限（zip bomb）→ 413；這裡暫時把上限調低以免產生數十 MB 資料
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	r := &garminInflateReader{r: strings.NewReader(strings.Repeat("a", 1000)), max: 100}
	if _, err := io.Copy(io.Discard, r); !errors.Is(err, errGarminTooLarge) {
		t.Fatalf("inflate over limit: err=%v, want errGarminTooLarge", err)
	}
	r2 := &garminInflateReader{r: strings.NewReader(strings.Repeat("a", 100)), max: 100}
	if n, err := io.Copy(io.Discard, r2); err != nil || n != 100 {
		t.Fatalf("inflate exactly at limit: n=%d err=%v", n, err)
	}
	// 以真正的 gzip：極度可壓縮的 40 MiB 字串值 > 32 MiB 解壓上限 → 413
	var zb bytes.Buffer
	gz := gzip.NewWriter(&zb)
	chunk := bytes.Repeat([]byte("a"), 1<<20)
	gz.Write([]byte(`{"note":"`))
	for i := 0; i < 40; i++ {
		gz.Write(chunk)
	}
	gz.Write([]byte(`"}`))
	gz.Close()
	if zb.Len() > 1<<20 {
		t.Fatalf("test bomb unexpectedly large: %d", zb.Len())
	}
	start := time.Now()
	rec := tg.post("/webhook/"+testGarminToken+"/activities", bytes.NewReader(zb.Bytes()), map[string]string{"Content-Encoding": "gzip"})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("zip bomb: got %d, want 413", rec.Code)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("zip bomb took %s", el)
	}
}

// 超長空白串（CPU 消耗攻擊）：快速丟棄，回 200（完整但不合法），不落地。
func TestWebhook_WhitespaceBombRejectedFast(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	body := `{"activities":[` + strings.Repeat(" ", 16<<20-100) + `]}`
	start := time.Now()
	rec := tg.postRaw("activities", body)
	if rec.Code != 200 {
		t.Fatalf("whitespace bomb: got %d, want 200 (complete but invalid)", rec.Code)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("whitespace bomb took %s (quadratic whitespace scan not guarded)", el)
	}
	if tg.store.count() != 0 {
		t.Fatal("nothing may be persisted")
	}
	// 正常排版（縮排）的 JSON 不受影響
	pretty, _ := json.MarshalIndent(synthPush(synthActivity(tUser1, "WS-1", 1790070000)), "", "        ")
	if rec := tg.post("/webhook/"+testGarminToken+"/activities", bytes.NewReader(pretty), nil); rec.Code != 200 || tg.store.count() != 1 {
		t.Fatalf("pretty-printed json: code=%d events=%d", rec.Code, tg.store.count())
	}
}

func TestWebhook_TruncatedJSONRejected_BadJSONAcked(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	full, _ := json.Marshal(synthPush(synthActivity(tUser1, "X-1", 1790008000), synthActivity(tUser1, "X-2", 1790008001)))

	// 被截斷（疑似代理砍掉尾巴）→ 非 200，且不得落地任何事件（Garmin 會整批重送）
	for _, cut := range []int{len(full) - 1, len(full) / 2, len(full) - 20, 5} {
		rec := tg.post("/webhook/"+testGarminToken+"/activities", bytes.NewReader(full[:cut]), nil)
		if rec.Code == http.StatusOK {
			t.Fatalf("truncated at %d/%d bytes acked with 200 (would lose data)", cut, len(full))
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("truncated at %d: got %d, want 400", cut, rec.Code)
		}
	}
	if tg.store.count() != 0 {
		t.Fatal("truncated bodies must not persist anything")
	}

	// Content-Length 與實讀不符（宣稱 N 但實際較短）→ 非 200
	req := httptest.NewRequest(http.MethodPost, "/webhook/"+testGarminToken+"/activities", bytes.NewReader(full))
	req.ContentLength = int64(len(full) + 100)
	rec := httptest.NewRecorder()
	tg.router.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatal("Content-Length mismatch must not be acked")
	}

	// 完整讀完但語意不合法 → 200（毒訊息不重送），不落地
	for name, raw := range map[string]string{
		"not json":      `this is not json at all`,
		"array root":    `[1,2,3]`,
		"garbage mid":   `{"activities":[{"userId":"x"},,]}`,
		"empty body":    ``,
		"scalar":        `42`,
		"wrong element": `{"activities":["a","b"]}`,
	} {
		rec := tg.postRaw("activities", raw)
		if rec.Code != 200 {
			t.Errorf("%s: complete-but-invalid body must be acked 200, got %d", name, rec.Code)
		}
	}
	if tg.store.count() != 0 {
		t.Fatal("invalid bodies must not persist anything")
	}
}

func TestWebhook_PersistFailureIs503(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	tg.store.insertErr = errors.New("simulated db outage")
	rec := tg.postJSON("activities", synthPush(synthActivity(tUser1, "E-1", 1790009000)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("persist failure: got %d, want 503", rec.Code)
	}
	if al := tg.alertList(); len(al) != 1 || !strings.HasPrefix(al[0], "garmin_webhook_err|") || strings.Contains(al[0], tUser1) {
		t.Fatalf("expected one clean garmin_webhook_err alert, got %v", al)
	}
	tg.store.insertErr = nil
	tg.store.knownErr = errors.New("simulated lookup failure")
	if rec := tg.postJSON("activities", synthPush(synthActivity(tUser1, "E-2", 1790009001))); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("lookup failure: got %d, want 503", rec.Code)
	}
}

func TestWebhook_ProcessingDoesNotUseRequestContext(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	started := make(chan struct{})
	release := make(chan struct{})
	var ctxErr error
	tg.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		close(started)
		<-release
		ctxErr = ctx.Err()
		return garminResInserted, nil
	}
	body, _ := json.Marshal(synthPush(synthActivity(tUser1, "C-1", 1790010000)))
	reqCtx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/webhook/"+testGarminToken+"/activities", bytes.NewReader(body)).WithContext(reqCtx)
	rec := httptest.NewRecorder()
	tg.router.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("processing did not start after ack")
	}
	cancel() // 請求結束（ctx 取消）後，處理仍須能完成
	close(release)
	tg.store.waitSettled(t, 2*time.Second)
	if ctxErr != nil {
		t.Fatalf("processing ctx was cancelled with the request: %v", ctxErr)
	}
	rows := tg.store.rows()
	if rows[0].Status != garminStDone {
		t.Fatalf("event status = %s, want done", rows[0].Status)
	}
}

func TestWebhook_SameUserEventsSerialized(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	tg.store.addUser(tUser2, tDor2)
	var mu sync.Mutex
	cur, maxCur := map[string]int{}, map[string]int{}
	tg.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		mu.Lock()
		cur[ev.ProviderUserID]++
		if cur[ev.ProviderUserID] > maxCur[ev.ProviderUserID] {
			maxCur[ev.ProviderUserID] = cur[ev.ProviderUserID]
		}
		mu.Unlock()
		time.Sleep(15 * time.Millisecond)
		mu.Lock()
		cur[ev.ProviderUserID]--
		mu.Unlock()
		return garminResInserted, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			uid := tUser1
			if i%2 == 1 {
				uid = tUser2
			}
			tg.postJSON("activities", synthPush(synthActivity(uid, fmt.Sprintf("SER-%d", i), 1790011000+int64(i))))
		}(i)
	}
	wg.Wait()
	tg.store.waitSettled(t, 5*time.Second)
	mu.Lock()
	defer mu.Unlock()
	for uid, m := range maxCur {
		if m != 1 {
			t.Errorf("user %s had %d concurrent handlers, want 1 (per-user serialization)", uid[:6], m)
		}
	}
}

func TestWebhook_ObservesClientIDWithoutStoringValue(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	tg.post("/webhook/"+testGarminToken+"/activities", strings.NewReader(`{"activities":[]}`), map[string]string{"garmin-client-id": "client-test"})
	tg.post("/webhook/"+testGarminToken+"/activities", strings.NewReader(`{"activities":[]}`), map[string]string{"garmin-client-id": "someone-else"})
	tg.post("/webhook/"+testGarminToken+"/activities", strings.NewReader(`{"activities":[]}`), nil)
	deadline := time.Now().Add(2 * time.Second)
	day := tg.h.dayBucket()
	for time.Now().Before(deadline) {
		a, _, _ := tg.h.kv.Get(context.Background(), "garmin:cnt:cid:"+day+":match")
		b, _, _ := tg.h.kv.Get(context.Background(), "garmin:cnt:cid:"+day+":mismatch")
		c, _, _ := tg.h.kv.Get(context.Background(), "garmin:cnt:cid:"+day+":none")
		if a == "1" && b == "1" && c == "1" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("client-id observation counters (match/mismatch/none) not recorded")
}

func TestWebhook_NoRateLimitOnWebhook(t *testing.T) {
	// 被 429 會觸發 Garmin 退避重送：webhook 路由不得掛限流。以 deps.RateLimit 注入一個「一律 429」的中介層驗證。
	called := 0
	tg := newTestGarmin(t, nil)
	tg.h.rateLimit = func(action string, limit int, window time.Duration) func(http.Handler) http.Handler {
		called++
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
		}
	}
	tg.router = tg.h.Router()
	rec := tg.postJSON("activities", synthPush())
	if rec.Code != 200 {
		t.Fatalf("webhook must not be rate limited, got %d", rec.Code)
	}
}

// 10 MiB 與 16 MiB 邊界：經過 handler（自帶上限），不經網路。Production 審核要求最小可收 10MB。
func synthBigPush(t testing.TB, targetBytes int, uid string) []byte {
	t.Helper()
	const head = `{"activities":[`
	const padPrefix = `,{"userId":"pad","note":"`
	const padSuffix = `"}]}`
	tail := len(padPrefix) + len(padSuffix)
	var b bytes.Buffer
	b.WriteString(head)
	first := true
	for i := 0; ; i++ {
		a := synthActivity(uid, fmt.Sprintf("BIG-%d", i), 1700000000+int64(i))
		a["activityType"] = "CYCLING" // 非白名單：驗證大 body 解析而不必落地數萬筆
		raw, _ := json.Marshal(a)
		if b.Len()+len(raw)+1+tail > targetBytes {
			break
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.Write(raw)
	}
	pad := targetBytes - b.Len() - tail
	if pad < 0 {
		t.Fatalf("cannot synthesize %d bytes", targetBytes)
	}
	b.WriteString(padPrefix + strings.Repeat("p", pad) + padSuffix)
	return b.Bytes()
}

func TestWebhook_LargeBodies_10MiB_16MiB(t *testing.T) {
	tg := newTestGarmin(t, nil) // 預設上限 16 MiB
	tg.store.addUser(tUser1, tDor1)

	for _, tc := range []struct {
		name string
		size int
		want int
	}{
		{"10MiB", 10 << 20, 200},
		{"16MiB exactly", 16 << 20, 200},
		{"16MiB+1", 16<<20 + 1, 413},
	} {
		body := synthBigPush(t, tc.size, tUser1)
		if len(body) != tc.size {
			t.Fatalf("%s: synthesized %d bytes, want %d", tc.name, len(body), tc.size)
		}
		start := time.Now()
		rec := tg.post("/webhook/"+testGarminToken+"/activities", bytes.NewReader(body), nil)
		el := time.Since(start)
		if rec.Code != tc.want {
			t.Fatalf("%s: got %d, want %d", tc.name, rec.Code, tc.want)
		}
		if el > 30*time.Second {
			t.Fatalf("%s: took %s (>30s)", tc.name, el)
		}
		t.Logf("%s: status %d in %s", tc.name, rec.Code, el.Round(time.Millisecond))
	}
	if tg.store.count() != 0 {
		t.Fatalf("non-whitelisted synthetic activities must not persist, got %d", tg.store.count())
	}
}

func TestWebhook_ParseConcurrencyBounded(t *testing.T) {
	tg := newTestGarmin(t, nil)
	// 占滿 parseSem，下一個請求在等待上限（5 秒）內拿不到就 503；這裡縮短等待，改以 ctx 取消驗證不會卡死
	for i := 0; i < cap(tg.h.parseSem); i++ {
		tg.h.parseSem <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/webhook/"+testGarminToken+"/activities", strings.NewReader(`{}`)).WithContext(ctx)
	rec := httptest.NewRecorder()
	tg.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("saturated parse slots: got %d, want 503", rec.Code)
	}
}
