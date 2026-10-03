package integration

// Garmin 測試共用輔助：記憶體假 store（語意對齊 PG 實作：ON CONFLICT DO NOTHING／租約／退避／dead）、
// 以 httptest.ResponseRecorder 打 Router 的輔助函式、zerolog 輸出擷取。全部使用合成資料。

import (
	"bytes"
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

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const testGarminToken = "tok_0123456789abcdef0123456789abcdef0123456789" // 44 字元，合成值
const testGarminTokenPrev = "prev_0123456789abcdef0123456789abcdef012345"

type fakeEvRow struct {
	garminEvent
	Status    string
	Result    string
	LastError string
	Next      time.Time
}

type fakeGarminStore struct {
	mu         sync.Mutex
	now        func() time.Time
	known      map[string]string // garmin user id -> dor user id
	events     map[string]*fakeEvRow
	order      []string
	keys       map[string]string // dedupe key -> id
	seq        int
	insertErr  error
	knownErr   error
	lockErr    error // WithGarminUserLock 回傳的錯誤（例如 errGarminBusy）
	insertDone bool
	onInsert   func()
	released   []string
	purged     int64

	lockMu   sync.Mutex
	lockHeld map[string]bool

	cs fakeConnState // 連線列／匯入相關的假資料（見 garmin_fakeconn_test.go）
}

func newFakeGarminStore() *fakeGarminStore {
	return &fakeGarminStore{
		now:      time.Now,
		known:    map[string]string{},
		events:   map[string]*fakeEvRow{},
		keys:     map[string]string{},
		lockHeld: map[string]bool{},
		cs:       newFakeConnState(),
	}
}

func (s *fakeGarminStore) addUser(garminID, dorID string) {
	s.mu.Lock()
	s.known[garminID] = dorID
	s.mu.Unlock()
}

func (s *fakeGarminStore) KnownGarminUsers(ctx context.Context, ids []string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.knownErr != nil {
		return nil, s.knownErr
	}
	out := map[string]string{}
	for _, id := range ids {
		if d, ok := s.known[id]; ok {
			out[id] = d
		}
	}
	for _, c := range s.cs.conns {
		for _, id := range ids {
			if c.Via == garminViaDirect && c.ProviderUserID == id && !s.cs.blockedUsers[c.UserID] && !s.cs.blockedGIDs[id] {
				out[id] = c.UserID
			}
		}
	}
	return out, nil
}

func (s *fakeGarminStore) InsertGarminEvents(ctx context.Context, evs []garminEventIn) ([]garminEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.insertErr != nil {
		return nil, s.insertErr
	}
	var out []garminEvent
	for _, e := range evs {
		if e.DedupeKey != nil {
			if _, dup := s.keys["garmin:"+*e.DedupeKey]; dup {
				continue
			}
		}
		s.seq++
		id := fmt.Sprintf("ev-%04d", s.seq)
		ge := garminEvent{ID: id, EventType: e.EventType, ProviderUserID: e.ProviderUserID, DedupeKey: e.DedupeKey,
			Payload: append([]byte(nil), e.Payload...), ReceivedAt: s.now()}
		s.events[id] = &fakeEvRow{garminEvent: ge, Status: garminStPending, Next: s.now().Add(garminEventLease)}
		s.order = append(s.order, id)
		if e.DedupeKey != nil {
			s.keys["garmin:"+*e.DedupeKey] = id
		}
		out = append(out, ge)
	}
	s.insertDone = true
	if s.onInsert != nil {
		s.onInsert()
	}
	return out, nil
}

func (s *fakeGarminStore) ClaimDueGarminEvents(ctx context.Context, limit int) ([]garminEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []garminEvent
	for _, id := range s.order {
		r := s.events[id]
		if (r.Status == garminStPending || r.Status == garminStError) && !r.Next.After(s.now()) {
			r.Next = s.now().Add(garminSweepLease)
			out = append(out, r.garminEvent)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (s *fakeGarminStore) ClaimGarminEventByID(ctx context.Context, id string) (*garminEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.events[id]
	if r == nil || !(r.Status == garminStPending || r.Status == garminStError) || r.Next.After(s.now().Add(2*time.Second)) {
		return nil, nil
	}
	r.Next = s.now().Add(garminSweepLease)
	ev := r.garminEvent
	return &ev, nil
}

func (s *fakeGarminStore) MarkGarminEventDone(ctx context.Context, id, result string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.events[id]; r != nil && (r.Status == garminStPending || r.Status == garminStError) {
		r.Status, r.Result = garminStDone, result
	}
	return nil
}

func (s *fakeGarminStore) MarkGarminEventError(ctx context.Context, id, errCode string, next time.Time, maxAttempts int) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.events[id]
	if r == nil {
		return 0, false, errors.New("no such event")
	}
	if r.Status != garminStPending && r.Status != garminStError {
		return 0, false, nil
	}
	r.Attempts++
	r.LastError, r.Next = errCode, next
	r.Status = garminStError
	if r.Attempts >= maxAttempts {
		r.Status = garminStDead
	}
	return r.Attempts, r.Status == garminStDead, nil
}

func (s *fakeGarminStore) MarkGarminEventDead(ctx context.Context, id, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.events[id]; r != nil {
		r.Status, r.LastError = garminStDead, reason
	}
	return nil
}

func (s *fakeGarminStore) DeferGarminEvent(ctx context.Context, id, note string, next time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.events[id]; r != nil && (r.Status == garminStPending || r.Status == garminStError) {
		r.Next, r.LastError = next, note
	}
	return nil
}

func (s *fakeGarminStore) ReleaseGarminLeases(ctx context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if r := s.events[id]; r != nil && (r.Status == garminStPending || r.Status == garminStError) {
			r.Next = s.now()
		}
		s.released = append(s.released, id)
	}
	return nil
}

func (s *fakeGarminStore) PurgeGarminEvents(ctx context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purged++
	return 0, nil
}

func (s *fakeGarminStore) WithGarminUserLock(ctx context.Context, key string, fn func(ctx context.Context) error) error {
	if s.lockErr != nil {
		return s.lockErr
	}
	s.lockMu.Lock()
	if s.lockHeld[key] {
		s.lockMu.Unlock()
		return errGarminBusy
	}
	s.lockHeld[key] = true
	s.lockMu.Unlock()
	defer func() { s.lockMu.Lock(); delete(s.lockHeld, key); s.lockMu.Unlock() }()
	return fn(ctx)
}

// --- 查詢輔助 ---

func (s *fakeGarminStore) rows() []*fakeEvRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*fakeEvRow, 0, len(s.order))
	for _, id := range s.order {
		c := *s.events[id]
		out = append(out, &c)
	}
	return out
}

func (s *fakeGarminStore) count() int { return len(s.rows()) }

func (s *fakeGarminStore) waitStatus(t *testing.T, id, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		r := s.events[id]
		ok := r != nil && r.Status == want
		s.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("event %s did not reach status %q within %s", id, want, within)
}

// --- handler 組裝 ---

type testGarmin struct {
	h      *GarminHandler
	store  *fakeGarminStore
	router http.Handler
	mu     sync.Mutex
	alerts []string
}

func newTestGarmin(t *testing.T, mutate func(*GarminConfig)) *testGarmin {
	t.Helper()
	cfg := GarminConfig{ClientID: "client-test", WebhookToken: testGarminToken, WebhookTokenPrev: testGarminTokenPrev}
	if mutate != nil {
		mutate(&cfg)
	}
	st := newFakeGarminStore()
	tg := &testGarmin{store: st}
	h := newGarminHandlerWithStore(cfg, GarminDeps{}, st)
	h.alertFn = func(kind, title, detail string) {
		tg.mu.Lock()
		tg.alerts = append(tg.alerts, kind+"|"+title+"|"+detail)
		tg.mu.Unlock()
	}
	h.startupDelay = 10 * time.Millisecond
	tg.h = h
	tg.router = h.Router()
	// 預設處理器：直接成功（個別測試會覆寫）
	h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) { return garminResInserted, nil }
	h.handlers.deregister = func(ctx context.Context, ev garminEvent) (string, error) { return garminResPurged, nil }
	h.handlers.permission = func(ctx context.Context, ev garminEvent) (string, error) { return garminResOK, nil }
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		h.Drain(ctx)
	})
	return tg
}

func (tg *testGarmin) alertList() []string {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return append([]string(nil), tg.alerts...)
}

func (tg *testGarmin) post(path string, body io.Reader, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, body)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	tg.router.ServeHTTP(rec, req)
	return rec
}

func (tg *testGarmin) postJSON(kind string, v any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(v)
	return tg.post("/webhook/"+testGarminToken+"/"+kind, bytes.NewReader(b), nil)
}

func (tg *testGarmin) postRaw(kind, raw string) *httptest.ResponseRecorder {
	return tg.post("/webhook/"+testGarminToken+"/"+kind, strings.NewReader(raw), nil)
}

// --- 合成資料 ---

func synthActivity(uid, summaryID string, start int64) map[string]any {
	return map[string]any{
		"userId": uid, "summaryId": summaryID, "activityId": 90000 + start%1000, "activityType": "RUNNING",
		"startTimeInSeconds": start, "startTimeOffsetInSeconds": 28800, "durationInSeconds": 1800,
		"distanceInMeters": 5000.5, "averageSpeedInMetersPerSecond": 2.78, "deviceName": "Forerunner 265",
		"totalElevationGainInMeters": 41.5, "averageHeartRateInBeatsPerMinute": 151,
		// 下列欄位不得落地
		"activityName": "Morning Run (synthetic)", "startingLatitudeInDegree": 25.0330, "startingLongitudeInDegree": 121.5654,
		"activeKilocalories": 321, "steps": 5400, "userAccessToken": "synthetic-user-access-token",
	}
}

func synthPush(acts ...map[string]any) map[string]any {
	return map[string]any{"activities": acts}
}

// captureLogs 把 zerolog 全域輸出導到 buffer，回傳讀取函式與還原函式。
func captureLogs(t *testing.T) func() string {
	t.Helper()
	var mu sync.Mutex
	var buf bytes.Buffer
	old := log.Logger
	level := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	log.Logger = zerolog.New(&lockedWriter{mu: &mu, w: &buf}).With().Timestamp().Logger()
	t.Cleanup(func() {
		log.Logger = old
		zerolog.SetGlobalLevel(level)
	})
	return func() string { mu.Lock(); defer mu.Unlock(); return buf.String() }
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// waitNoPending 等到 fake store 中所有事件都不是 pending／error（或逾時失敗）。
func (s *fakeGarminStore) waitSettled(t *testing.T, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		busy := false
		for _, r := range s.rows() {
			if r.Status == garminStPending || r.Status == garminStError {
				busy = true
			}
		}
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("events not settled within %s: %+v", within, s.rows())
}

// rtFunc：以函式實作的 http.RoundTripper（in-process 假 Garmin／Terra，不開本機 TCP；本機沙盒會改寫 loopback HTTP）。
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(body))}
}

func httptestRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }

func newGetRequest(path string) *http.Request { return httptest.NewRequest(http.MethodGet, path, nil) }

func newPostRequest(path, body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
}
