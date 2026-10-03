package integration

// 後台 Garmin 管理 API（garmin_admin.go）的單元測試：httptest recorder＋記憶體假 store＋假 Garmin（in-process），
// 沒有任何 listener、網路或外部呼叫。全部使用合成資料。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dor/api/internal/auth"
)

const (
	tAdmSuper = "5a5a5a5a-0000-4000-8000-0000000000aa" // 超級管理員
	tAdmPlain = "5a5a5a5a-0000-4000-8000-0000000000bb" // 一般管理者（有 admin 角色但不是超管）
	tAdmGhost = "dddddddd-0000-4000-8000-0000000000dd" // 不存在的 DOR 帳號

	admEv1 = "e0000000-0000-4000-8000-000000000001"
	admEv2 = "e0000000-0000-4000-8000-000000000002"
	admEv3 = "e0000000-0000-4000-8000-000000000003"
	admEv4 = "e0000000-0000-4000-8000-000000000004"
	admEv5 = "e0000000-0000-4000-8000-000000000005"

	admPayloadMarker = "SECRET-PAYLOAD-MARKER-7731"
)

// --- 假的 admin store：包住既有的 fakeGarminStore，補上 garminAdminStore 的方法 ---

type admBlockRec struct {
	UserID, GID, Reason, By string
	CtxErr                  error
}

type fakeAdminStore struct {
	*fakeGarminStore
	panicOnClaim atomic.Bool
	am           sync.Mutex
	supers       map[string]bool
	users        map[string]bool
	superErr     error
	existsErr    error
	listErr      error
	getErr       error
	countsErr    error
	requeueErr   error
	requeues     int
	blocks       []admBlockRec
}

func (s *fakeAdminStore) IsGarminSuperAdmin(ctx context.Context, uid string) (bool, error) {
	s.am.Lock()
	defer s.am.Unlock()
	if s.superErr != nil {
		return false, s.superErr
	}
	return s.supers[uid], nil
}

func (s *fakeAdminStore) GarminUserExists(ctx context.Context, uid string) (bool, error) {
	s.am.Lock()
	defer s.am.Unlock()
	if s.existsErr != nil {
		return false, s.existsErr
	}
	return s.users[uid], nil
}

// adminRow 呼叫端須持有 s.mu。
func (s *fakeAdminStore) adminRow(r *fakeEvRow) garminAdminEvent {
	e := garminAdminEvent{ID: r.ID, EventType: r.EventType, Status: r.Status, Attempts: r.Attempts, Result: r.Result,
		LastError: r.LastError, ReceivedAt: r.ReceivedAt, NextAttemptAt: r.Next, ProviderUserID: r.ProviderUserID}
	if r.Status == garminStDone || r.Status == garminStDead {
		t := r.ReceivedAt.Add(time.Second)
		e.ProcessedAt = &t
	}
	for _, c := range s.cs.conns {
		if c.Via == garminViaDirect && c.ProviderUserID == r.ProviderUserID {
			e.UserID = c.UserID
		}
	}
	return e
}

func (s *fakeAdminStore) ListGarminEventsAdmin(ctx context.Context, status string, limit int) ([]garminAdminEvent, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var rows []*fakeEvRow
	for _, id := range s.order {
		if r := s.events[id]; r != nil && (status == "" || r.Status == status) {
			rows = append(rows, r)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ReceivedAt.After(rows[j].ReceivedAt) })
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	out := []garminAdminEvent{}
	for _, r := range rows {
		out = append(out, s.adminRow(r))
	}
	return out, nil
}

func (s *fakeAdminStore) GetGarminEventAdmin(ctx context.Context, id string) (*garminAdminEvent, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.events[id]
	if r == nil {
		return nil, nil
	}
	e := s.adminRow(r)
	return &e, nil
}

func (s *fakeAdminStore) RequeueGarminEvent(ctx context.Context, id string) (garminRequeue, error) {
	if s.requeueErr != nil {
		return garminRequeue{}, s.requeueErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.events[id]
	if r == nil {
		return garminRequeue{}, nil
	}
	if r.Status != garminStError && r.Status != garminStDead {
		return garminRequeue{Found: true, Status: r.Status}, nil
	}
	prev := r.Status
	r.Status, r.Attempts, r.Next, r.LastError, r.Result = garminStPending, 0, s.now(), "", ""
	s.requeues++
	return garminRequeue{Found: true, Requeued: true, Status: prev}, nil
}

func (s *fakeAdminStore) GarminAdminCounts(ctx context.Context) (garminAdminCounts, error) {
	if s.countsErr != nil {
		return garminAdminCounts{}, s.countsErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var c garminAdminCounts
	for _, r := range s.events {
		switch r.Status {
		case garminStPending:
			c.Pending++
		case garminStError:
			c.Error++
		case garminStDone:
			c.Done++
		case garminStDead:
			c.Dead++
		}
		if c.LastReceivedAt == nil || r.ReceivedAt.After(*c.LastReceivedAt) {
			t := r.ReceivedAt
			c.LastReceivedAt = &t
		}
	}
	c.Blocked = len(s.cs.blockedUsers)
	return c, nil
}

// ClaimGarminEventByID 覆寫：可設定成 panic，用來驗證就地處理的 goroutine 出事時請求不會卡住也不會讓行程崩潰。
func (s *fakeAdminStore) ClaimGarminEventByID(ctx context.Context, id string) (*garminEvent, error) {
	if s.panicOnClaim.Load() {
		panic("synthetic claim panic")
	}
	return s.fakeGarminStore.ClaimGarminEventByID(ctx, id)
}

// AddGarminBlock 覆寫：記下參數（含呼叫當下 ctx 是否已取消），再交給原本的假實作。
func (s *fakeAdminStore) AddGarminBlock(ctx context.Context, userID, gid, reason, by string) error {
	s.am.Lock()
	s.blocks = append(s.blocks, admBlockRec{userID, gid, reason, by, ctx.Err()})
	s.am.Unlock()
	return s.fakeGarminStore.AddGarminBlock(ctx, userID, gid, reason, by)
}

func (s *fakeAdminStore) blockRecs() []admBlockRec {
	s.am.Lock()
	defer s.am.Unlock()
	return append([]admBlockRec(nil), s.blocks...)
}

func (s *fakeAdminStore) requeueCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requeues
}

// --- 環境 ---

// admAuthMW：有 X-Test-User 就把它放進 context（模擬 RequireAuth）；沒有也放行——讓路由內的守衛自己回 401。
func admAuthMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if uid := r.Header.Get("X-Test-User"); uid != "" {
			r = r.WithContext(context.WithValue(r.Context(), auth.CtxKeyUserID, uid))
		}
		next.ServeHTTP(w, r)
	})
}

type admEnv struct {
	*connectEnv
	as      *fakeAdminStore
	handler http.Handler
}

func newAdmEnv(t *testing.T) *admEnv {
	t.Helper()
	e := newConnectEnv(t, nil)
	as := &fakeAdminStore{
		fakeGarminStore: e.store,
		supers:          map[string]bool{tAdmSuper: true},
		users:           map[string]bool{tUserA: true, tUserB: true, tAdmSuper: true, tAdmPlain: true},
	}
	e.h.store = as
	e.h.purgeWait = 50 * time.Millisecond
	return &admEnv{connectEnv: e, as: as, handler: admAuthMW(e.h.AdminRouter())}
}

func (e *admEnv) do(method, path, uid, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if uid != "" {
		r.Header.Set("X-Test-User", uid)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, r)
	return rec
}

func (e *admEnv) get(path string) *httptest.ResponseRecorder { return e.do("GET", path, tAdmSuper, "") }
func (e *admEnv) post(path string) *httptest.ResponseRecorder {
	return e.do("POST", path, tAdmSuper, "")
}

// seedEvent 直接寫入假 store 的事件列（payload 內含標記字串與 Garmin userId 全碼，用來驗證不會外洩）。
func (e *admEnv) seedEvent(id, typ, status, gid string, attempts int, lastErr, result string, received, next time.Time) {
	e.store.mu.Lock()
	defer e.store.mu.Unlock()
	payload := fmt.Sprintf(`{"summaryId":%q,"startTimeInSeconds":1790000000,"deviceName":"Forerunner 265","raw_user":%q}`, admPayloadMarker, gid)
	e.store.events[id] = &fakeEvRow{
		garminEvent: garminEvent{ID: id, EventType: typ, ProviderUserID: gid, Payload: []byte(payload), Attempts: attempts, ReceivedAt: received},
		Status:      status, Result: result, LastError: lastErr, Next: next,
	}
	e.store.order = append(e.store.order, id)
}

func (e *admEnv) eventRow(id string) fakeEvRow {
	e.store.mu.Lock()
	defer e.store.mu.Unlock()
	return *e.store.events[id]
}

func admJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("response is not a JSON object (%d): %q", rec.Code, rec.Body.String())
	}
	return m
}

func admWantErr(t *testing.T, rec *httptest.ResponseRecorder, code int, errCode string) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, code, rec.Body.String())
	}
	if got := admJSON(t, rec)["error"]; got != errCode {
		t.Fatalf("error = %v, want %q (body %q)", got, errCode, rec.Body.String())
	}
}

// --- 遮罩 ---

func TestAdmin_MaskGarminUserID(t *testing.T) {
	for in, want := range map[string]string{
		"":                                     "",
		"   ":                                  "",
		"abc":                                  "****",
		"1234567":                              "****",
		"12345678":                             "****5678",
		"  12345678  ":                         "****5678",
		"d3c6a5b9-1111-2222-3333-44445555ffff": "****ffff",
		"測試測試測試測試x":                            "****試測試x",
	} {
		if got := garminMaskUserID(in); got != want {
			t.Errorf("mask(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAdmin_RedactID(t *testing.T) {
	id := "garmin-user-1234567890abcdef"
	if got := garminRedactID("api:401 for "+id+" twice "+id, id); strings.Contains(got, id) || !strings.Contains(got, "****cdef") {
		t.Fatalf("redact: %q", got)
	}
	if got := garminRedactID("busy", id); got != "busy" {
		t.Fatalf("untouched: %q", got)
	}
	if got := garminRedactID("abc short abc", "abc"); got != "abc short abc" {
		t.Fatalf("a short id must not be substituted (it would mangle ordinary text): %q", got)
	}
	if got := garminRedactID("", id); got != "" {
		t.Fatalf("empty: %q", got)
	}
}

// 事件輸出的欄位白名單：沒有 payload、dedupe_key、Garmin userId 原值；SQL 也不選 payload／dedupe_key。
func TestAdmin_EventOutputNeverCarriesPayloadOrRawGarminID(t *testing.T) {
	low := strings.ToLower(garminAdminEventSelect)
	for _, bad := range []string{"payload", "dedupe_key", "access_token", "refresh_token"} {
		if strings.Contains(low, bad) {
			t.Fatalf("admin event SELECT must not read %q", bad)
		}
	}
	want := map[string]bool{"id": true, "event_type": true, "status": true, "attempts": true, "result": true, "last_error": true,
		"received_at": true, "processed_at": true, "next_attempt_at": true, "user_id": true, "garmin_user_ref": true}
	typ := reflect.TypeOf(garminAdminEventDTO{})
	got := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		got[strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event DTO json fields = %v, want exactly %v", got, want)
	}
}

func TestAdmin_EventViewTruncatesAndRedacts(t *testing.T) {
	gid := "garmin-user-abcdefgh7e21"
	v := garminAdminEventView(garminAdminEvent{ID: admEv1, ProviderUserID: gid,
		LastError: strings.Repeat("測", 400) + gid, Result: "inserted " + gid})
	if n := len([]rune(v.LastError)); n > garminAdminErrMaxRunes {
		t.Fatalf("last_error not truncated: %d runes", n)
	}
	if strings.Contains(v.Result, gid) || !strings.Contains(v.Result, "****7e21") {
		t.Fatalf("result not redacted: %q", v.Result)
	}
	if v.GarminUserRef != "****7e21" {
		t.Fatalf("ref: %q", v.GarminUserRef)
	}
	v = garminAdminEventView(garminAdminEvent{ID: admEv1, LastError: "bad\x00\x07 code"})
	if strings.ContainsAny(v.LastError, "\x00\x07") {
		t.Fatalf("control characters must be stripped: %q", v.LastError)
	}
}

// --- 守衛 ---

var admRoutes = []struct{ method, path, body string }{
	{"GET", "/summary", ""},
	{"GET", "/events", ""},
	{"POST", "/events/" + admEv1 + "/replay", ""},
	{"POST", "/users/" + tUserA + "/block", `{"reason":"abuse"}`},
	{"POST", "/users/" + tUserA + "/unblock", ""},
}

func TestAdmin_GuardFailsClosedOnEveryRoute(t *testing.T) {
	for _, rt := range admRoutes {
		rt := rt
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			e := newAdmEnv(t)
			now := time.Now()
			e.seedEvent(admEv1, garminEvActivity, garminStDead, "g-guard-0000000000000001", 5, "api:500", "", now, now.Add(-time.Hour))
			e.putConnFor(tUserA)

			// 未登入／身分不是 UUID：401（路由內自己擋，不依賴外層）
			admWantErr(t, e.do(rt.method, rt.path, "", rt.body), 401, "login_required")
			admWantErr(t, e.do(rt.method, rt.path, "not-a-uuid", rt.body), 401, "login_required")
			// 一般管理者（非超管）：403，沒有任何副作用
			admWantErr(t, e.do(rt.method, rt.path, tAdmPlain, rt.body), 403, "forbidden")
			// 登入但不是 admin（例如一般會員）：403
			admWantErr(t, e.do(rt.method, rt.path, tUserB, rt.body), 403, "forbidden")
			if e.as.requeueCount() != 0 || len(e.store.cs.blockedUsers) != 0 || len(e.as.blockRecs()) != 0 {
				t.Fatalf("a rejected request must not change anything: requeues=%d blocked=%v", e.as.requeueCount(), e.store.cs.blockedUsers)
			}
			if row := e.eventRow(admEv1); row.Status != garminStDead {
				t.Fatalf("event touched by a rejected request: %+v", row)
			}
			// 超管查詢失敗：fail-closed，且不洩漏內部錯誤
			e.as.superErr = errors.New("db exploded: secret-detail")
			rec := e.do(rt.method, rt.path, tAdmSuper, rt.body)
			admWantErr(t, rec, 500, "failed")
			if strings.Contains(rec.Body.String(), "secret-detail") {
				t.Fatalf("internal error leaked: %s", rec.Body.String())
			}
			e.as.superErr = nil
			// store 不支援 admin 介面：503（fail-closed，不是放行）
			e.h.store = e.store
			admWantErr(t, e.do(rt.method, rt.path, tAdmSuper, rt.body), 503, "unavailable")
		})
	}
}

func (e *admEnv) putConnFor(userID string) *garminConn {
	return e.store.putConn(garminConn{UserID: userID, ProviderUserID: "g-conn-" + userID[:8] + "-0000000000", Via: garminViaDirect,
		Issuer: garminAppGeneration("client-test"), Scope: "ACTIVITY_EXPORT", ConnectedAt: time.Now().Add(-90 * 24 * time.Hour)})
}

func TestAdmin_ResponsesAreNotCacheable(t *testing.T) {
	e := newAdmEnv(t)
	if cc := e.get("/events").Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if cc := e.do("GET", "/events", tAdmPlain, "").Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("even rejections must not be cached: %q", cc)
	}
}

func TestAdmin_UnknownRoutesAndMethods(t *testing.T) {
	e := newAdmEnv(t)
	if rec := e.get("/nope"); rec.Code != 404 {
		t.Fatalf("unknown route: %d", rec.Code)
	}
	if rec := e.do("PUT", "/summary", tAdmSuper, ""); rec.Code != 405 {
		t.Fatalf("wrong method on /summary: %d", rec.Code)
	}
	if rec := e.get("/events/" + admEv1 + "/replay"); rec.Code != 405 {
		t.Fatalf("GET on a POST route: %d", rec.Code)
	}
	if rec := e.get("/users/" + tUserA + "/block"); rec.Code != 405 {
		t.Fatalf("GET on block: %d", rec.Code)
	}
}

// main.go 的掛法（admin 群組內 r.With(RequireSuper).Mount）：路徑參數穿過 Mount 仍可取得；內層守衛在外層漏擋時仍然生效。
func TestAdmin_MountedUnderAdminGroupPattern(t *testing.T) {
	e := newAdmEnv(t)
	passAll := func(next http.Handler) http.Handler { return next } // 模擬「外層 RequireSuper 漏掛／放行一切」
	parent := chi.NewRouter()
	parent.Group(func(r chi.Router) {
		r.Use(admAuthMW)
		r.With(passAll).Mount("/admin/garmin", e.h.AdminRouter())
	})
	now := time.Now()
	e.seedEvent(admEv1, garminEvActivity, garminStDead, "g-mount-0000000000000001", 5, "api:500", "", now, now.Add(-time.Hour))
	call := func(method, path, uid, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-Test-User", uid)
		rec := httptest.NewRecorder()
		parent.ServeHTTP(rec, r)
		return rec
	}
	if rec := call("GET", "/admin/garmin/summary", tAdmSuper, ""); rec.Code != 200 {
		t.Fatalf("summary under mount: %d %s", rec.Code, rec.Body.String())
	}
	rec := call("POST", "/admin/garmin/events/"+admEv1+"/replay", tAdmSuper, "")
	if rec.Code != 200 || admJSON(t, rec)["outcome"] != "requeued" {
		t.Fatalf("replay under mount: %d %s", rec.Code, rec.Body.String())
	}
	rec = call("POST", "/admin/garmin/users/"+tUserB+"/block", tAdmSuper, `{"reason":"mounted"}`)
	if rec.Code != 200 || !e.store.cs.blockedUsers[tUserB] {
		t.Fatalf("block under mount: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call("POST", "/admin/garmin/users/"+tUserA+"/block", tAdmPlain, `{"reason":"x"}`); rec.Code != 403 || e.store.cs.blockedUsers[tUserA] {
		t.Fatalf("the in-router guard must still stop a non-super admin: %d", rec.Code)
	}
}

// 限流（main.go 注入 RateLimit 時）掛在守衛之後：被拒絕的請求不會消耗限流額度。
func TestAdmin_RateLimitAppliedAfterGuard(t *testing.T) {
	e := newAdmEnv(t)
	var mu sync.Mutex
	var actions []string
	e.h.rateLimit = func(action string, limit int, window time.Duration) func(http.Handler) http.Handler {
		mu.Lock()
		actions = append(actions, action)
		mu.Unlock()
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("X-Limited", action)
				next.ServeHTTP(w, r)
			})
		}
	}
	e.handler = admAuthMW(e.h.AdminRouter())
	mu.Lock()
	got := append([]string(nil), actions...)
	mu.Unlock()
	for _, want := range []string{"garmin_admin_block", "garmin_admin_read", "garmin_admin_replay"} {
		if !contains(got, want) {
			t.Fatalf("limit actions = %v, missing %q", got, want)
		}
	}
	if rec := e.do("GET", "/summary", tAdmPlain, ""); rec.Code != 403 || rec.Header().Get("X-Limited") != "" {
		t.Fatalf("rejected request reached the limiter: code=%d header=%q", rec.Code, rec.Header().Get("X-Limited"))
	}
	if rec := e.get("/summary"); rec.Code != 200 || rec.Header().Get("X-Limited") != "garmin_admin_read" {
		t.Fatalf("allowed request: code=%d header=%q", rec.Code, rec.Header().Get("X-Limited"))
	}
}

// --- GET /events ---

func TestAdmin_EventsListMetadataOnly(t *testing.T) {
	e := newAdmEnv(t)
	gidA := "garmin-user-aaaaaaaaaaaaaaaa7e21"
	gidUnknown := "garmin-user-zzzzzzzzzzzzzzzz0042"
	e.store.putConn(garminConn{UserID: tUserA, ProviderUserID: gidA, Via: garminViaDirect, Issuer: garminAppGeneration("client-test")})
	now := time.Now().UTC().Truncate(time.Second)
	e.seedEvent(admEv1, garminEvActivity, garminStDead, gidA, 5, "api:500", "", now.Add(-5*time.Minute), now.Add(-time.Hour))
	e.seedEvent(admEv2, garminEvActivity, garminStDone, gidA, 0, "", "inserted", now.Add(-4*time.Minute), now.Add(-time.Hour))
	e.seedEvent(admEv3, garminEvPermission, garminStError, gidA, 2, strings.Repeat("x", 500)+gidA, "", now.Add(-3*time.Minute), now.Add(time.Hour))
	e.seedEvent(admEv4, garminEvDeregister, garminStPending, gidUnknown, 0, "", "", now.Add(-2*time.Minute), now.Add(time.Minute))

	rec := e.get("/events")
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, leak := range []string{admPayloadMarker, gidA, gidUnknown, "Forerunner", "raw_user", "payload"} {
		if strings.Contains(body, leak) {
			t.Fatalf("response leaks %q: %s", leak, body)
		}
	}
	for _, want := range []string{"****7e21", "****0042"} {
		if !strings.Contains(body, want) {
			t.Fatalf("response lacks masked ref %q: %s", want, body)
		}
	}
	m := admJSON(t, rec)
	evs, _ := m["events"].([]any)
	if len(evs) != 4 || m["count"].(float64) != 4 || m["limit"].(float64) != garminAdminDefaultLimit || m["status"] != "" {
		t.Fatalf("envelope: %s", body)
	}
	ids := make([]string, 0, 4)
	allowed := map[string]bool{"id": true, "event_type": true, "status": true, "attempts": true, "result": true, "last_error": true,
		"received_at": true, "processed_at": true, "next_attempt_at": true, "user_id": true, "garmin_user_ref": true}
	byID := map[string]map[string]any{}
	for _, raw := range evs {
		o := raw.(map[string]any)
		for k := range o {
			if !allowed[k] {
				t.Fatalf("unexpected field %q in %v", k, o)
			}
		}
		if len(o) != len(allowed) {
			t.Fatalf("every event must carry all %d fields, got %v", len(allowed), o)
		}
		ids = append(ids, o["id"].(string))
		byID[o["id"].(string)] = o
	}
	if want := []string{admEv4, admEv3, admEv2, admEv1}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("order (newest first) = %v, want %v", ids, want)
	}
	if byID[admEv1]["user_id"] != tUserA || byID[admEv4]["user_id"] != "" {
		t.Fatalf("user_id: connected=%v unknown=%v", byID[admEv1]["user_id"], byID[admEv4]["user_id"])
	}
	if byID[admEv1]["last_error"] != "api:500" || byID[admEv1]["status"] != "dead" || byID[admEv1]["attempts"].(float64) != 5 {
		t.Fatalf("dead row: %v", byID[admEv1])
	}
	if le := byID[admEv3]["last_error"].(string); len([]rune(le)) > garminAdminErrMaxRunes || strings.Contains(le, gidA) {
		t.Fatalf("last_error must be truncated and redacted: %q", le)
	}
	if byID[admEv4]["processed_at"] != nil || byID[admEv2]["processed_at"] == nil {
		t.Fatalf("processed_at: pending=%v done=%v", byID[admEv4]["processed_at"], byID[admEv2]["processed_at"])
	}
	if byID[admEv2]["result"] != "inserted" || byID[admEv2]["garmin_user_ref"] != "****7e21" {
		t.Fatalf("done row: %v", byID[admEv2])
	}

	// status 篩選與 limit
	m = admJSON(t, e.get("/events?status=dead"))
	if evs, _ := m["events"].([]any); len(evs) != 1 || m["status"] != "dead" || evs[0].(map[string]any)["id"] != admEv1 {
		t.Fatalf("status=dead: %v", m)
	}
	m = admJSON(t, e.get("/events?limit=2"))
	if evs, _ := m["events"].([]any); len(evs) != 2 || evs[0].(map[string]any)["id"] != admEv4 || m["limit"].(float64) != 2 {
		t.Fatalf("limit=2: %v", m)
	}
	m = admJSON(t, e.get("/events?limit=999999"))
	if m["limit"].(float64) != garminAdminMaxLimit {
		t.Fatalf("an oversized limit must be clamped to %d: %v", garminAdminMaxLimit, m["limit"])
	}
}

func TestAdmin_EventsListValidationAndEmpty(t *testing.T) {
	e := newAdmEnv(t)
	for _, q := range []string{"limit=0", "limit=-3", "limit=abc", "limit=1.5", "limit=99999999999999999999"} {
		admWantErr(t, e.get("/events?"+q), 400, "invalid_limit")
	}
	for _, q := range []string{"status=weird", "status=DEAD", "status=dead%20", "status=pending,dead", "status=%27%3B--"} {
		rec := e.get("/events?" + q)
		// "dead%20" 會被 trim 成合法值；其餘必須是 400
		if q == "status=dead%20" {
			if rec.Code != 200 {
				t.Fatalf("%s: %d", q, rec.Code)
			}
			continue
		}
		admWantErr(t, rec, 400, "invalid_status")
	}
	rec := e.get("/events")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"events":[]`) {
		t.Fatalf("an empty list must be [] (not null): %d %s", rec.Code, rec.Body.String())
	}
	e.as.listErr = errors.New("db exploded: secret-detail")
	rec = e.get("/events")
	admWantErr(t, rec, 500, "failed")
	if strings.Contains(rec.Body.String(), "secret-detail") {
		t.Fatalf("internal error leaked: %s", rec.Body.String())
	}
}

// --- POST /events/{id}/replay ---

func admDeadActivity(e *admEnv, id, gid string) {
	now := time.Now()
	e.seedEvent(id, garminEvActivity, garminStDead, gid, 5, "api:500", "", now.Add(-time.Hour), now.Add(-30*time.Minute))
}

func TestAdmin_ReplayDeadEventRequeuesProcessesAndIsIdempotent(t *testing.T) {
	e := newAdmEnv(t)
	var calls atomic.Int32
	e.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		calls.Add(1)
		return garminResInserted, nil
	}
	gid := "g-replay-0000000000000001-9f3a"
	admDeadActivity(e, admEv1, gid)

	rec := e.post("/events/" + admEv1 + "/replay")
	if rec.Code != 200 {
		t.Fatalf("replay: %d %s", rec.Code, rec.Body.String())
	}
	m := admJSON(t, rec)
	ev, _ := m["event"].(map[string]any)
	if m["outcome"] != "requeued" || m["previous_status"] != "dead" || m["processing_started"] != true || ev == nil {
		t.Fatalf("replay response: %s", rec.Body.String())
	}
	if ev["status"] != "done" || ev["result"] != "inserted" || ev["attempts"].(float64) != 0 || ev["last_error"] != "" {
		t.Fatalf("event after replay: %v", ev)
	}
	for _, leak := range []string{admPayloadMarker, gid, "Forerunner"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("replay response leaks %q: %s", leak, rec.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("activity handler calls = %d, want 1", calls.Load())
	}
	row := e.eventRow(admEv1)
	if row.Status != garminStDone || row.Result != garminResInserted {
		t.Fatalf("stored row: %+v", row)
	}

	// 冪等：再放一次什麼都不做
	rec = e.post("/events/" + admEv1 + "/replay")
	m = admJSON(t, rec)
	if rec.Code != 200 || m["outcome"] != "already_done" || m["processing_started"] != false || m["previous_status"] != nil {
		t.Fatalf("second replay: %d %s", rec.Code, rec.Body.String())
	}
	if calls.Load() != 1 || e.as.requeueCount() != 1 {
		t.Fatalf("second replay must be a no-op: handler calls=%d requeues=%d", calls.Load(), e.as.requeueCount())
	}
}

// 重放前 attempts 先歸零：再次失敗只會是第 1 次（error），不會因為舊的 5 次直接又變 dead。
func TestAdmin_ReplayResetsAttemptsBeforeProcessing(t *testing.T) {
	e := newAdmEnv(t)
	fail := atomic.Bool{}
	fail.Store(true)
	e.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		if fail.Load() {
			return "", errors.New("transient")
		}
		return garminResInserted, nil
	}
	admDeadActivity(e, admEv1, "g-reset-00000000000000001")

	m := admJSON(t, e.post("/events/"+admEv1+"/replay"))
	ev, _ := m["event"].(map[string]any)
	if m["outcome"] != "requeued" || ev["status"] != "error" || ev["attempts"].(float64) != 1 {
		t.Fatalf("failing replay: attempts must restart from 0 (so 1 now) and stay replayable: %v", m)
	}
	if ev["last_error"] == "" {
		t.Fatalf("a failed replay records its own error code: %v", ev)
	}
	// error 狀態也可重放；這次成功
	fail.Store(false)
	// 退避中的 error 事件：重放要立刻處理（next_attempt_at 被重設成 now）
	m = admJSON(t, e.post("/events/"+admEv1+"/replay"))
	ev, _ = m["event"].(map[string]any)
	if m["outcome"] != "requeued" || m["previous_status"] != "error" || ev["status"] != "done" {
		t.Fatalf("replaying an error event: %v", m)
	}
}

func TestAdmin_ReplayPendingAndDoneAreNoOpsExceptOverduePending(t *testing.T) {
	e := newAdmEnv(t)
	var calls atomic.Int32
	e.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		calls.Add(1)
		return garminResInserted, nil
	}
	now := time.Now()
	// done：什麼都不做
	e.seedEvent(admEv1, garminEvActivity, garminStDone, "g-noop-000000000000000001", 0, "", "inserted", now.Add(-time.Hour), now.Add(-time.Hour))
	// pending 且租約中（別的處理者持有）：不能重複處理
	e.seedEvent(admEv2, garminEvActivity, garminStPending, "g-noop-000000000000000002", 0, "", "", now.Add(-time.Minute), now.Add(2*time.Minute))
	// pending 且已逾期（例如行程中途死掉後遺留）：順手處理掉
	e.seedEvent(admEv3, garminEvActivity, garminStPending, "g-noop-000000000000000003", 0, "", "", now.Add(-time.Hour), now.Add(-10*time.Minute))

	m := admJSON(t, e.post("/events/"+admEv1+"/replay"))
	if m["outcome"] != "already_done" || m["processing_started"] != false {
		t.Fatalf("done: %v", m)
	}
	m = admJSON(t, e.post("/events/"+admEv2+"/replay"))
	if m["outcome"] != "already_pending" || m["processing_started"] != false {
		t.Fatalf("leased pending must not be claimed: %v", m)
	}
	if ev, _ := m["event"].(map[string]any); ev["status"] != "pending" {
		t.Fatalf("leased pending changed: %v", ev)
	}
	if calls.Load() != 0 {
		t.Fatalf("handler ran for a done/leased event: %d", calls.Load())
	}
	m = admJSON(t, e.post("/events/"+admEv3+"/replay"))
	ev, _ := m["event"].(map[string]any)
	if m["outcome"] != "already_pending" || m["processing_started"] != true || ev["status"] != "done" || calls.Load() != 1 {
		t.Fatalf("overdue pending should be processed once: %v (calls=%d)", m, calls.Load())
	}
	if e.as.requeueCount() != 0 {
		t.Fatalf("nothing should have been requeued: %d", e.as.requeueCount())
	}
}

func TestAdmin_ReplayNotFoundBadIDAndErrors(t *testing.T) {
	e := newAdmEnv(t)
	admWantErr(t, e.post("/events/e0000000-0000-4000-8000-0000000000ff/replay"), 404, "event_not_found")
	for _, bad := range []string{"nope", "e0000000-0000-4000-8000-00000000000", "e0000000-0000-4000-8000-0000000000001", "%27%3B%20DROP%20TABLE%20x", "e0000000000040008000000000000001"} {
		admWantErr(t, e.post("/events/"+bad+"/replay"), 400, "invalid_id")
	}
	e.as.requeueErr = errors.New("db exploded: secret-detail")
	rec := e.post("/events/" + admEv1 + "/replay")
	admWantErr(t, rec, 500, "failed")
	if strings.Contains(rec.Body.String(), "secret-detail") {
		t.Fatalf("internal error leaked: %s", rec.Body.String())
	}
}

func TestAdmin_ReplayUpperCaseIDIsNormalized(t *testing.T) {
	e := newAdmEnv(t)
	admDeadActivity(e, admEv1, "g-upper-0000000000000001")
	rec := e.post("/events/" + strings.ToUpper(admEv1) + "/replay")
	if rec.Code != 200 || admJSON(t, rec)["outcome"] != "requeued" {
		t.Fatalf("upper-case uuid: %d %s", rec.Code, rec.Body.String())
	}
}

// 並發重放同一筆：只會有一個真的重排，處理器只跑一次。
func TestAdmin_ConcurrentReplaysProcessOnce(t *testing.T) {
	e := newAdmEnv(t)
	var calls atomic.Int32
	e.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return garminResInserted, nil
	}
	admDeadActivity(e, admEv1, "g-conc-00000000000000001")
	const n = 12
	outcomes := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := e.post("/events/" + admEv1 + "/replay")
			if rec.Code != 200 {
				outcomes <- fmt.Sprintf("HTTP %d", rec.Code)
				return
			}
			var m map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &m)
			o, _ := m["outcome"].(string)
			outcomes <- o
		}()
	}
	wg.Wait()
	close(outcomes)
	counts := map[string]int{}
	for o := range outcomes {
		counts[o]++
	}
	if counts["requeued"] != 1 || counts["requeued"]+counts["already_pending"]+counts["already_done"] != n {
		t.Fatalf("outcomes: %v (exactly one request may requeue; the rest are idempotent no-ops)", counts)
	}
	if calls.Load() != 1 || e.as.requeueCount() != 1 {
		t.Fatalf("handler calls=%d requeues=%d, want 1/1", calls.Load(), e.as.requeueCount())
	}
	if row := e.eventRow(admEv1); row.Status != garminStDone {
		t.Fatalf("final row: %+v", row)
	}
}

// 關閉中（Drain 已開始）：事件仍會被重排成 pending，但不開新的處理——交給新行程的啟動掃描。
func TestAdmin_ReplayWhileDrainingRequeuesButDoesNotProcess(t *testing.T) {
	e := newAdmEnv(t)
	var calls atomic.Int32
	e.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		calls.Add(1)
		return garminResInserted, nil
	}
	admDeadActivity(e, admEv1, "g-drain-0000000000000001")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	e.h.Drain(ctx)
	m := admJSON(t, e.post("/events/"+admEv1+"/replay"))
	if m["outcome"] != "requeued" || m["processing_started"] != false {
		t.Fatalf("draining replay: %v", m)
	}
	if ev, _ := m["event"].(map[string]any); ev["status"] != "pending" || calls.Load() != 0 {
		t.Fatalf("draining: event=%v calls=%d", ev, calls.Load())
	}
	// 之後（新行程）的啟動掃描可以領到它
	if row := e.eventRow(admEv1); row.Status != garminStPending || row.Next.After(time.Now().Add(time.Second)) {
		t.Fatalf("event must be left due and pending for the next sweep: %+v", row)
	}
}

// 處理比等待時間久：回應不會被拖住，event 顯示處理中的狀態；處理完成後事件才變 done。
func TestAdmin_ReplayBoundedWaitWhenProcessingIsSlow(t *testing.T) {
	old := garminAdminReplayWait
	garminAdminReplayWait = 80 * time.Millisecond
	t.Cleanup(func() { garminAdminReplayWait = old })
	e := newAdmEnv(t)
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	e.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) {
		<-release
		return garminResInserted, nil
	}
	admDeadActivity(e, admEv1, "g-slow-00000000000000001")
	start := time.Now()
	rec := e.post("/events/" + admEv1 + "/replay")
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the response must not wait for slow processing (took %s)", d)
	}
	m := admJSON(t, rec)
	ev, _ := m["event"].(map[string]any)
	if rec.Code != 200 || m["processing_started"] != true || ev["status"] != "pending" {
		t.Fatalf("slow replay: %d %s", rec.Code, rec.Body.String())
	}
	once.Do(func() { close(release) })
	e.store.waitStatus(t, admEv1, garminStDone, 3*time.Second)
}

// 就地處理的 goroutine 在領取階段 panic：請求立刻回（不空等逾時）、行程不崩潰，事件維持已重排的 pending 交給下次掃描。
func TestAdmin_ReplayPanicInClaimDoesNotHangOrCrash(t *testing.T) {
	old := garminAdminReplayWait
	garminAdminReplayWait = 5 * time.Second
	t.Cleanup(func() { garminAdminReplayWait = old })
	e := newAdmEnv(t)
	e.as.panicOnClaim.Store(true)
	admDeadActivity(e, admEv1, "g-panic-00000000000000001")
	start := time.Now()
	rec := e.post("/events/" + admEv1 + "/replay")
	if d := time.Since(start); d > time.Second {
		t.Fatalf("the response waited %s although the processing goroutine had already panicked", d)
	}
	m := admJSON(t, rec)
	ev, _ := m["event"].(map[string]any)
	if rec.Code != 200 || m["outcome"] != "requeued" || m["processing_started"] != false || ev["status"] != "pending" {
		t.Fatalf("replay with a panicking claim: %d %s", rec.Code, rec.Body.String())
	}
	// 之後恢復正常：同一筆事件可被一般掃描領走處理
	e.as.panicOnClaim.Store(false)
	if n := e.h.SweepPending(context.Background()); n != 1 {
		t.Fatalf("the requeued event must be picked up by the normal sweep: %d", n)
	}
	e.store.waitStatus(t, admEv1, garminStDone, 2*time.Second)
}

// --- POST /users/{id}/block｜unblock ---

func TestAdmin_BlockValidation(t *testing.T) {
	e := newAdmEnv(t)
	block := func(uid, body string) *httptest.ResponseRecorder {
		return e.do("POST", "/users/"+uid+"/block", tAdmSuper, body)
	}
	admWantErr(t, block("not-a-uuid", `{"reason":"x"}`), 400, "invalid_id")
	admWantErr(t, block(tUserA, ``), 400, "reason_required")
	admWantErr(t, block(tUserA, `{}`), 400, "reason_required")
	admWantErr(t, block(tUserA, `{"reason":""}`), 400, "reason_required")
	admWantErr(t, block(tUserA, `{"reason":"   \t  "}`), 400, "reason_required")
	admWantErr(t, block(tUserA, `{"reason":"\u0000\u0007\u001b"}`), 400, "reason_required")
	admWantErr(t, block(tUserA, `{"reason":5}`), 400, "invalid_body")
	admWantErr(t, block(tUserA, `{"reason":`), 400, "invalid_body")
	admWantErr(t, block(tUserA, `[1,2]`), 400, "invalid_body")
	admWantErr(t, block(tUserA, `{"reason":"`+strings.Repeat("a", 121)+`"}`), 400, "reason_too_long")
	admWantErr(t, block(tUserA, `{"reason":"`+strings.Repeat("測", 121)+`"}`), 400, "reason_too_long")
	admWantErr(t, block(tUserA, `{"reason":"`+strings.Repeat("a", 8000)+`"}`), 400, "invalid_body") // 超過 4KB 的 body
	admWantErr(t, block(tAdmGhost, `{"reason":"typo"}`), 404, "user_not_found")
	if len(e.as.blockRecs()) != 0 || len(e.store.cs.blockedUsers) != 0 {
		t.Fatalf("an invalid request must not write the block list: %+v", e.as.blockRecs())
	}
	e.as.existsErr = errors.New("db exploded: secret-detail")
	rec := block(tUserA, `{"reason":"x"}`)
	admWantErr(t, rec, 500, "failed")
	if strings.Contains(rec.Body.String(), "secret-detail") || len(e.as.blockRecs()) != 0 {
		t.Fatalf("lookup failure: %s", rec.Body.String())
	}
	e.as.existsErr = nil
	// 剛好 120 字（多位元組字元）可以
	rec = block(tUserB, `{"reason":"`+strings.Repeat("測", 120)+`"}`)
	if rec.Code != 200 {
		t.Fatalf("120-rune reason: %d %s", rec.Code, rec.Body.String())
	}
	if recs := e.as.blockRecs(); len(recs) != 1 || len([]rune(recs[0].Reason)) != 120 {
		t.Fatalf("block rec: %+v", recs)
	}
}

func TestAdmin_BlockRevokesRegistrationPurgesAndIsIdempotent(t *testing.T) {
	e := newAdmEnv(t)
	c := seedConn(t, e.connectEnv, tUserA, time.Hour, 80*24*time.Hour)
	e.store.cs.purgeActivities = 3
	now := time.Now()
	e.seedEvent(admEv1, garminEvActivity, garminStDone, c.ProviderUserID, 0, "", "inserted", now, now)

	rec := e.do("POST", "/users/"+tUserA+"/block", tAdmSuper, "{\"reason\":\"  abuse report\\u0000 (synthetic)  \"}")
	if rec.Code != 200 {
		t.Fatalf("block: %d %s", rec.Code, rec.Body.String())
	}
	m := admJSON(t, rec)
	if m["ok"] != true || m["blocked"] != true || m["already_blocked"] != false || m["registration_deleted"] != true ||
		m["had_connection"] != true || m["deleted_activities"].(float64) != 3 || m["deleted_events"].(float64) != 1 {
		t.Fatalf("block response: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), c.ProviderUserID) || strings.Contains(rec.Body.String(), tUserA) {
		t.Fatalf("block response leaks identifiers: %s", rec.Body.String())
	}
	recs := e.as.blockRecs()
	if len(recs) != 1 || recs[0].UserID != tUserA || recs[0].GID != c.ProviderUserID || recs[0].By != tAdmSuper ||
		recs[0].Reason != "abuse report (synthetic)" {
		t.Fatalf("block row: %+v (the reason is cleaned, the actor is the logged-in super admin)", recs)
	}
	if _, _, _, del := e.api.calls(); del != 1 {
		t.Fatalf("DELETE registration calls = %d, want 1", del)
	}
	if e.readConn(tUserA) != nil || !e.store.cs.blockedUsers[tUserA] {
		t.Fatal("connection must be purged and the user blocked")
	}
	if len(e.mailer.list()) != 0 {
		t.Fatalf("a blocked user must not get the 'connection ended' mail: %v", e.mailer.list())
	}
	// 之後 /connect 回 403
	if rc := e.req("POST", "/connect", tUserA, `{"consent":true,"consent_v":"v1"}`); rc.Code != 403 {
		t.Fatalf("blocked user connect: %d", rc.Code)
	}

	// 冪等：第二次回 already_blocked，仍 200
	rec = e.do("POST", "/users/"+tUserA+"/block", tAdmSuper, `{"reason":"again"}`)
	m = admJSON(t, rec)
	if rec.Code != 200 || m["already_blocked"] != true || m["had_connection"] != false {
		t.Fatalf("second block: %d %s", rec.Code, rec.Body.String())
	}

	// 解除封鎖：第一次 true、第二次 false（冪等）；不要求使用者仍存在
	rec = e.do("POST", "/users/"+tUserA+"/unblock", tAdmSuper, "")
	if rec.Code != 200 || admJSON(t, rec)["unblocked"] != true || e.store.cs.blockedUsers[tUserA] {
		t.Fatalf("unblock: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do("POST", "/users/"+tUserA+"/unblock", tAdmSuper, "")
	if rec.Code != 200 || admJSON(t, rec)["unblocked"] != false {
		t.Fatalf("second unblock: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do("POST", "/users/"+tAdmGhost+"/unblock", tAdmSuper, "")
	if rec.Code != 200 || admJSON(t, rec)["unblocked"] != false {
		t.Fatalf("unblock of a deleted user must still work: %d %s", rec.Code, rec.Body.String())
	}
	admWantErr(t, e.do("POST", "/users/not-a-uuid/unblock", tAdmSuper, ""), 400, "invalid_id")
	if rc := e.req("POST", "/connect", tUserA, `{"consent":true,"consent_v":"v1"}`); rc.Code != 200 {
		t.Fatalf("after unblock the user can connect again: %d", rc.Code)
	}
}

// 清除失敗：封鎖清單已先寫（封鎖已生效），回 500 固定代碼、不含內部細節；修復後重送即補完（冪等）。
func TestAdmin_BlockPurgeFailureKeepsBlockAndIsRetryable(t *testing.T) {
	e := newAdmEnv(t)
	seedConn(t, e.connectEnv, tUserA, time.Hour, 80*24*time.Hour)
	e.store.cs.purgeErr = errors.New("db exploded: secret-detail")
	rec := e.do("POST", "/users/"+tUserA+"/block", tAdmSuper, `{"reason":"abuse"}`)
	admWantErr(t, rec, 500, "block_failed")
	if strings.Contains(rec.Body.String(), "secret-detail") {
		t.Fatalf("internal error leaked: %s", rec.Body.String())
	}
	if !e.store.cs.blockedUsers[tUserA] {
		t.Fatal("the block list is written first, so the block is already effective")
	}
	e.store.mu.Lock()
	e.store.cs.purgeErr = nil
	e.store.mu.Unlock()
	rec = e.do("POST", "/users/"+tUserA+"/block", tAdmSuper, `{"reason":"abuse"}`)
	if rec.Code != 200 || admJSON(t, rec)["already_blocked"] != true || e.readConn(tUserA) != nil {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAdmin_BlockWhenUserLockIsBusyIs409(t *testing.T) {
	e := newAdmEnv(t)
	seedConn(t, e.connectEnv, tUserA, time.Hour, 80*24*time.Hour)
	e.store.lockErr = errGarminBusy
	admWantErr(t, e.do("POST", "/users/"+tUserA+"/block", tAdmSuper, `{"reason":"abuse"}`), 409, "user_busy")
	if !e.store.cs.blockedUsers[tUserA] {
		t.Fatal("the block list is written before the (busy) purge")
	}
}

// 封鎖是合規動作：請求被取消（管理者關掉頁面）時仍要做完——傳給 BlockUser 的 context 不能是已取消的。
func TestAdmin_BlockIsDetachedFromRequestCancellation(t *testing.T) {
	e := newAdmEnv(t)
	seedConn(t, e.connectEnv, tUserA, time.Hour, 80*24*time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "/users/"+tUserA+"/block", strings.NewReader(`{"reason":"abuse"}`)).WithContext(ctx)
	r.Header.Set("X-Test-User", tAdmSuper)
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, r)
	// 守衛本身用請求 ctx 查超管（假 store 不看 ctx），所以會通過；重點是下面的封鎖
	recs := e.as.blockRecs()
	if rec.Code != 200 || len(recs) != 1 || recs[0].CtxErr != nil {
		t.Fatalf("block with a cancelled request: code=%d recs=%+v body=%s", rec.Code, recs, rec.Body.String())
	}
}

// --- GET /summary ---

func TestAdmin_SummaryHasOnlyCountsAndMatchesDirectStats(t *testing.T) {
	e := newAdmEnv(t)
	c := seedConn(t, e.connectEnv, tUserA, time.Hour, 80*24*time.Hour)
	now := time.Now().UTC().Truncate(time.Second)
	gid := c.ProviderUserID
	e.seedEvent(admEv1, garminEvActivity, garminStPending, gid, 0, "", "", now.Add(-time.Hour), now.Add(time.Minute))
	e.seedEvent(admEv2, garminEvActivity, garminStPending, gid, 0, "", "", now.Add(-50*time.Minute), now.Add(time.Minute))
	e.seedEvent(admEv3, garminEvActivity, garminStError, gid, 2, "api:500", "", now.Add(-40*time.Minute), now.Add(time.Hour))
	e.seedEvent(admEv4, garminEvActivity, garminStDone, gid, 0, "", garminResInserted, now.Add(-30*time.Minute), now)
	e.seedEvent(admEv5, garminEvDeregister, garminStDead, gid, 5, "api:500", "", now.Add(-5*time.Minute), now)
	e.store.cs.blockedUsers[tUserB] = true
	e.h.count(context.Background(), "garmin:cnt:unknown:"+e.h.hourBucket(), 7, time.Hour)

	rec := e.get("/summary")
	if rec.Code != 200 {
		t.Fatalf("summary: %d %s", rec.Code, rec.Body.String())
	}
	for _, leak := range []string{gid, tUserA, tUserB, admPayloadMarker, "Forerunner"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("summary must be counts only, found %q in %s", leak, rec.Body.String())
		}
	}
	var got struct {
		Provider    string `json:"provider"`
		Connections struct {
			Connected, Active24h, NeedsReauth, Paused, Stale3d int
		} `json:"connections"`
		Events struct {
			Received24h  int `json:"received_24h"`
			Imported24h  int `json:"imported_24h"`
			Pending      int `json:"pending"`
			PendingStale int `json:"pending_stale"`
			Error        int `json:"error"`
			Done         int `json:"done"`
			Dead         int `json:"dead"`
		} `json:"events"`
		UnknownUser24h     int      `json:"unknown_user_24h"`
		Blocked            int      `json:"blocked"`
		LastPushReceivedAt string   `json:"last_push_received_at"`
		Notes              []string `json:"notes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Provider != "garmin" || got.Connections.Connected != 1 {
		t.Fatalf("connections: %+v", got)
	}
	if got.Events.Pending != 2 || got.Events.Error != 1 || got.Events.Done != 1 || got.Events.Dead != 1 || got.Blocked != 1 || got.UnknownUser24h != 7 {
		t.Fatalf("counts: %+v", got)
	}
	if want := now.Add(-5 * time.Minute).Format(time.RFC3339); got.LastPushReceivedAt != want {
		t.Fatalf("last_push_received_at = %q, want %q", got.LastPushReceivedAt, want)
	}
	if got.Notes == nil {
		t.Fatalf("notes must be [] not null: %s", rec.Body.String())
	}
	// 與日報同源：DirectStats 的數字一致
	ds, err := e.h.DirectStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Connections.Connected != ds.Connected || got.Connections.Active24h != ds.Active24h || got.Connections.NeedsReauth != ds.NeedsReauth ||
		got.Connections.Paused != ds.Paused || got.Connections.Stale3d != ds.Stale3d || got.Events.Received24h != ds.Events24h ||
		got.Events.Imported24h != ds.Imported24h || got.Events.PendingStale != ds.PendingStale || got.Events.Dead != ds.Dead || got.UnknownUser24h != ds.UnknownUser24h {
		t.Fatalf("summary %+v differs from DirectStats %+v", got, ds)
	}
}

func TestAdmin_SummaryEmptyAndErrors(t *testing.T) {
	e := newAdmEnv(t)
	rec := e.get("/summary")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"last_push_received_at":null`) {
		t.Fatalf("empty summary: %d %s", rec.Code, rec.Body.String())
	}
	e.as.countsErr = errors.New("db exploded: secret-detail")
	rec = e.get("/summary")
	admWantErr(t, rec, 500, "failed")
	if strings.Contains(rec.Body.String(), "secret-detail") {
		t.Fatalf("internal error leaked: %s", rec.Body.String())
	}
}

// 讀 payload 的欄位名稱不會出現在 admin 輸出的任何地方（整體掃描：回應原文逐一檢查）。
func TestAdmin_NoRouteEverEchoesPayloadMarker(t *testing.T) {
	e := newAdmEnv(t)
	gid := "g-echo-000000000000000001-zz99"
	e.store.putConn(garminConn{UserID: tUserA, ProviderUserID: gid, Via: garminViaDirect, Issuer: garminAppGeneration("client-test")})
	admDeadActivity(e, admEv1, gid)
	for _, rt := range admRoutes {
		rec := e.do(rt.method, rt.path, tAdmSuper, rt.body)
		for _, leak := range []string{admPayloadMarker, gid, "raw_user", "Forerunner"} {
			if strings.Contains(rec.Body.String(), leak) {
				t.Fatalf("%s %s echoes %q: %s", rt.method, rt.path, leak, rec.Body.String())
			}
		}
	}
}
