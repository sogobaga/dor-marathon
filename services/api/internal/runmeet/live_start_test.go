package runmeet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/runmeet/live"
)

// live_start_test.go：POST /run-meets/{id}/live/start（契約 §3.1、§9 P1-1／6／7／9／10／11）。
//
// 沒有 PostgreSQL：DB 面（成員／團練／設定／顯示名稱／同意稽核）全部走 liveStartRepo 的 fake，
// Redis 用 miniredis（Lua 由 gopher-lua 執行）。HTTP 一律 httptest.NewRecorder 直接打 chi router，
// 不開真的 TCP 監聽（本機沙盒會改寫 loopback HTTP 回應）。

// --- fake repo ---

type consentRow struct {
	uid, meetID  string
	consentV     int
	presenceOnly bool
	ip           string
	at           time.Time // 寫入時間（fake 用來模擬 pgLiveRepo 的去重視窗）
}

type fakeLiveRepo struct {
	mu sync.Mutex

	settings live.Settings
	flags    map[string]fakeFlags // uid → 旗標；缺省＝一般會員（無 email／編碼、非超管）
	flagsErr error
	meet     liveMeet
	meetErr  error
	members  map[string]string    // uid → run_meet_members.status（缺＝沒有這一列）
	names    map[string][2]string // uid → (name, handle)
	auditErr error

	audit []consentRow
	calls []string // 呼叫順序（"UserFlags"／"MeetForLive"／…）

	// auditAttempts InsertConsentAudit 被呼叫的次數（含被去重掉的）；auditNow 控制去重視窗用的時鐘（nil＝time.Now）。
	auditAttempts int
	auditNow      func() time.Time

	// onMemberStatus 在 MemberStatus 被呼叫時執行：用來模擬「handler 讀 DB 的同時，另一個請求 Kick 並 Revoke」。
	onMemberStatus func()
}

type fakeFlags struct {
	email, code string
	isSuper     bool
}

func (f *fakeLiveRepo) note(name string) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
}

func (f *fakeLiveRepo) called(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == name {
			return true
		}
	}
	return false
}

func (f *fakeLiveRepo) UserFlags(_ context.Context, uid string) (string, string, bool, bool, error) {
	f.note("UserFlags")
	if f.flagsErr != nil {
		return "", "", false, false, f.flagsErr
	}
	fl := f.flags[uid]
	return fl.email, fl.code, fl.isSuper, false, nil
}

func (f *fakeLiveRepo) LiveSettings(context.Context) live.Settings {
	f.note("LiveSettings")
	return f.settings.Normalize()
}

func (f *fakeLiveRepo) MeetForLive(context.Context, string) (liveMeet, error) {
	f.note("MeetForLive")
	return f.meet, f.meetErr
}

func (f *fakeLiveRepo) MemberStatus(_ context.Context, _, uid string) (string, error) {
	f.note("MemberStatus")
	if f.onMemberStatus != nil {
		f.onMemberStatus()
	}
	return f.members[uid], nil
}

func (f *fakeLiveRepo) DisplayName(_ context.Context, uid string) (string, string, error) {
	f.note("DisplayName")
	if n, ok := f.names[uid]; ok {
		return n[0], n[1], nil
	}
	return "小明", "xm", nil
}

// InsertConsentAudit 與 pgLiveRepo.InsertConsentAudit 同一個語意：視窗（consentDedupeWindowMinutes）內已有同
// user_id + meet 的列就不再寫（pgLiveRepo 用單一 INSERT … SELECT … WHERE NOT EXISTS 做到；那條 SQL 本身的
// 行為見 live_start_integration_test.go，需要真實 PostgreSQL）。被去重掉的呼叫仍回 nil（證據已存在）。
func (f *fakeLiveRepo) InsertConsentAudit(_ context.Context, uid, meetID string, consentV int, presenceOnly bool, ip string) error {
	f.note("InsertConsentAudit")
	if f.auditErr != nil {
		return f.auditErr
	}
	now := time.Now()
	if f.auditNow != nil {
		now = f.auditNow()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auditAttempts++
	for _, r := range f.audit {
		if r.uid == uid && r.meetID == meetID && now.Sub(r.at) < consentDedupeWindowMinutes*time.Minute {
			return nil
		}
	}
	f.audit = append(f.audit, consentRow{uid, meetID, consentV, presenceOnly, ip, now})
	return nil
}

// --- 測試環境 ---

var (
	// 團練 2026-10-10 12:00（台北）；預設時窗 11:30 ～ 15:30（ends_at 為 NULL → meet_at + 3h + 30m）
	taipei       = time.FixedZone("Asia/Taipei", 8*3600)
	liveMeetAt   = time.Date(2026, 10, 10, 12, 0, 0, 0, taipei)
	liveMeetUUID = "6f259465-0f7d-431c-a122-bf21a4e9a8eb"
)

type startEnv struct {
	t     *testing.T
	mr    *miniredis.Miniredis
	rdb   *redis.Client
	store *live.Store
	repo  *fakeLiveRepo
	h     *liveStartHandler
	now   time.Time
	owner string
}

func newStartEnv(t *testing.T) *startEnv {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	e := &startEnv{t: t, mr: mr, rdb: rdb, store: live.NewStore(rdb), owner: uuid.NewString()}
	e.now = liveMeetAt.Add(-10 * time.Minute) // 窗內（開放起點 11:30）
	mr.SetTime(e.now)
	e.repo = &fakeLiveRepo{
		settings: live.DefaultSettings().Normalize(),
		flags:    map[string]fakeFlags{},
		members:  map[string]string{},
		names:    map[string][2]string{},
		meet: liveMeet{
			ID: liveMeetUUID, OwnerID: e.owner, Title: "週六河濱團練", Status: StatusOpen,
			MeetAt: liveMeetAt,
		},
	}
	lat, lng := 25.03, 121.56
	e.repo.meet.Lat, e.repo.meet.Lng = &lat, &lng
	e.repo.settings.EntryState = "open" // 預設全開；入口閘門另有專屬測試
	e.h = &liveStartHandler{repo: e.repo, store: e.store}
	return e
}

func (e *startEnv) advance(d time.Duration) {
	e.now = e.now.Add(d)
	e.mr.SetTime(e.now)
	e.mr.FastForward(d)
}

func (e *startEnv) router(uid string) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if uid != "" {
				req = req.WithContext(context.WithValue(req.Context(), auth.CtxKeyUserID, uid))
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Post("/{id}/live/start", e.h.handle)
	return r
}

// member 把一個 uid 設成指定的成員狀態。
func (e *startEnv) member(status string) string {
	uid := uuid.NewString()
	if status != "" {
		e.repo.members[uid] = status
	}
	return uid
}

const startSID = "startsid00000001"

func startBody(consent string, reauth bool) string {
	return fmt.Sprintf(`{"pv":1,"sid":%q,%s"reauth":%t}`, startSID, consent, reauth)
}

const consentV1 = `"consent_v":1,`

func (e *startEnv) post(uid, id, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/"+id+"/live/start", strings.NewReader(body))
	req.RemoteAddr = "198.51.100.9:5555"
	rec := httptest.NewRecorder()
	e.router(uid).ServeHTTP(rec, req)
	return rec
}

func (e *startEnv) start(uid string) *httptest.ResponseRecorder {
	return e.post(uid, liveMeetUUID, startBody(consentV1, false))
}

func codeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("not JSON: %q", rec.Body.String())
	}
	s, _ := out["error"].(string)
	return s
}

// --- 成員資格 ---

// 契約 §9-1：非成員／pending／kicked／left／rejected 的 /live/start → 403；joined 與發起人 → 200。
func TestLiveStartMembershipMatrix(t *testing.T) {
	cases := []struct {
		status string
		want   int
	}{
		{"", http.StatusForbidden},
		{MemberPending, http.StatusForbidden},
		{MemberRejected, http.StatusForbidden},
		{MemberKicked, http.StatusForbidden},
		{MemberLeft, http.StatusForbidden},
		{MemberJoined, http.StatusOK},
	}
	for _, c := range cases {
		t.Run("status="+c.status, func(t *testing.T) {
			e := newStartEnv(t)
			uid := e.member(c.status)
			rec := e.start(uid)
			if rec.Code != c.want {
				t.Fatalf("%d %s, want %d", rec.Code, rec.Body.String(), c.want)
			}
			if c.want == http.StatusForbidden {
				if codeOf(t, rec) != "not_member" {
					t.Fatalf("error = %q, want not_member", codeOf(t, rec))
				}
				if len(e.repo.audit) != 0 {
					t.Fatal("a rejected start must not write a consent audit row")
				}
				if len(e.mr.Keys()) != 0 {
					t.Fatalf("a rejected start must not write anything to Redis: %v", e.mr.Keys())
				}
			}
		})
	}

	t.Run("owner without a member row", func(t *testing.T) {
		e := newStartEnv(t)
		if rec := e.start(e.owner); rec.Code != http.StatusOK {
			t.Fatalf("owner: %d %s", rec.Code, rec.Body.String())
		}
	})
}

// --- 時窗 ---

// 契約 §2：now ∈ [meet_at−30m, COALESCE(ends_at, meet_at+3h)+30m)；起點含、終點不含；ends_at NULL 用 meet_at+3h。
func TestLiveStartWindow(t *testing.T) {
	opens := liveMeetAt.Add(-30 * time.Minute)
	endsAt := liveMeetAt.Add(2 * time.Hour)
	cases := []struct {
		name   string
		endsAt *time.Time
		at     time.Time
		want   int
	}{
		{"1ms before opens", nil, opens.Add(-time.Millisecond), http.StatusConflict},
		{"exactly opens", nil, opens, http.StatusOK},
		{"at meet_at", nil, liveMeetAt, http.StatusOK},
		{"meet_at+3h exactly, ends_at NULL", nil, liveMeetAt.Add(3 * time.Hour), http.StatusOK},
		{"3h29m59s after meet_at, ends_at NULL", nil, liveMeetAt.Add(3*time.Hour + 30*time.Minute - time.Second), http.StatusOK},
		{"exactly closes (ends_at NULL: meet_at+3h+30m)", nil, liveMeetAt.Add(3*time.Hour + 30*time.Minute), http.StatusConflict},
		{"after closes, ends_at NULL", nil, liveMeetAt.Add(5 * time.Hour), http.StatusConflict},
		{"ends_at set: just before ends_at+30m", &endsAt, endsAt.Add(30*time.Minute - time.Millisecond), http.StatusOK},
		{"ends_at set: exactly ends_at+30m", &endsAt, endsAt.Add(30 * time.Minute), http.StatusConflict},
		{"ends_at set: meet_at+3h would still be open but ends_at+30m has passed", &endsAt, liveMeetAt.Add(2*time.Hour + 45*time.Minute), http.StatusConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newStartEnv(t)
			e.repo.meet.EndsAt = c.endsAt
			e.now = c.at
			e.mr.SetTime(c.at)
			uid := e.member(MemberJoined)
			rec := e.start(uid)
			if rec.Code != c.want {
				t.Fatalf("%d %s, want %d", rec.Code, rec.Body.String(), c.want)
			}
			if c.want == http.StatusConflict {
				var out struct {
					Error    string    `json:"error"`
					OpensAt  time.Time `json:"opens_at"`
					ClosesAt time.Time `json:"closes_at"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Error != "outside_window" {
					t.Fatalf("body: %s", rec.Body.String())
				}
				if !out.OpensAt.Equal(opens) {
					t.Fatalf("opens_at = %v, want %v", out.OpensAt, opens)
				}
				if len(e.repo.audit) != 0 || len(e.mr.Keys()) != 0 {
					t.Fatal("outside the window nothing may be written (no audit row, no Redis state)")
				}
			}
		})
	}
}

func TestLiveStartWindowUsesConfiguredSettings(t *testing.T) {
	e := newStartEnv(t)
	e.repo.settings.PreMinutes = 5
	e.repo.settings.GraceMinutes = 0
	e.repo.settings.DefaultHours = 1
	uid := e.member(MemberJoined)
	e.now = liveMeetAt.Add(-6 * time.Minute)
	e.mr.SetTime(e.now)
	if rec := e.start(uid); rec.Code != http.StatusConflict {
		t.Fatalf("6 min before with pre=5: %d", rec.Code)
	}
	e.now = liveMeetAt.Add(-5 * time.Minute)
	e.mr.SetTime(e.now)
	if rec := e.start(uid); rec.Code != http.StatusOK {
		t.Fatalf("5 min before with pre=5: %d %s", rec.Code, rec.Body.String())
	}
	e.now = liveMeetAt.Add(time.Hour) // closes = meet_at + 1h + 0
	e.mr.SetTime(e.now)
	if rec := e.start(uid); rec.Code != http.StatusConflict {
		t.Fatalf("at meet_at+1h with default_hours=1, grace=0: %d", rec.Code)
	}
}

// --- 團練狀態 ---

func TestLiveStartMeetStates(t *testing.T) {
	t.Run("cancelled -> 410 meet_over", func(t *testing.T) {
		e := newStartEnv(t)
		e.repo.meet.Status = StatusCancelled
		rec := e.start(e.member(MemberJoined))
		if rec.Code != http.StatusGone || codeOf(t, rec) != "meet_over" {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("closed (not accepting new members) still allowed", func(t *testing.T) {
		e := newStartEnv(t)
		e.repo.meet.Status = StatusClosed
		if rec := e.start(e.member(MemberJoined)); rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("not found / deleted / admin-hidden -> 404", func(t *testing.T) {
		e := newStartEnv(t)
		e.repo.meetErr = errNotFound
		rec := e.start(e.member(MemberJoined))
		if rec.Code != http.StatusNotFound || codeOf(t, rec) != "not_found" {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("db error -> 500 without leaking details", func(t *testing.T) {
		e := newStartEnv(t)
		e.repo.meetErr = errors.New("pq: connection refused to 10.0.0.1")
		rec := e.start(e.member(MemberJoined))
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "10.0.0.1") {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})
}

// --- 入口閘門 ---

func TestLiveStartEntryGate(t *testing.T) {
	type tc struct {
		name      string
		state     string
		whitelist string
		flags     fakeFlags
		want      int
	}
	cases := []tc{
		{"hidden: nobody", "hidden", "", fakeFlags{email: "a@x.com"}, 403},
		{"hidden: even a whitelisted user", "hidden", "a@x.com", fakeFlags{email: "a@x.com"}, 403},
		{"hidden: even the super admin (kill-switch consistency)", "hidden", "", fakeFlags{isSuper: true}, 403},
		{"locked: shown-but-disabled for members", "locked", "a@x.com", fakeFlags{email: "a@x.com"}, 403},
		{"locked: super admin always allowed", "locked", "", fakeFlags{isSuper: true}, 200},
		{"whitelist: email hit", "whitelist", "a@x.com\nb@x.com", fakeFlags{email: "B@X.com"}, 200},
		{"whitelist: account code hit (# optional)", "whitelist", "#abc123", fakeFlags{code: "ABC123"}, 200},
		{"whitelist: miss", "whitelist", "a@x.com", fakeFlags{email: "z@x.com", code: "zz"}, 403},
		{"whitelist: empty list", "whitelist", "", fakeFlags{email: "z@x.com"}, 403},
		{"whitelist: super admin always allowed", "whitelist", "", fakeFlags{isSuper: true}, 200},
		{"open: everyone", "open", "", fakeFlags{email: "z@x.com"}, 200},
		{"default state (no row) is whitelist: stranger blocked", "", "", fakeFlags{email: "z@x.com"}, 403},
		{"default state (no row): super admin allowed", "", "", fakeFlags{isSuper: true}, 200},
		{"unknown state fails closed", "bogus", "z@x.com", fakeFlags{email: "z@x.com"}, 403},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newStartEnv(t)
			e.repo.settings.EntryState = c.state
			if c.state == "" {
				e.repo.settings.EntryState = "" // Normalize 會補成預設 whitelist，與 loadLiveSettings 一致
			}
			e.repo.settings.Whitelist = c.whitelist
			uid := e.member(MemberJoined)
			e.repo.flags[uid] = c.flags
			rec := e.start(uid)
			if rec.Code != c.want {
				t.Fatalf("%d %s, want %d", rec.Code, rec.Body.String(), c.want)
			}
			if c.want == 403 {
				if codeOf(t, rec) != "entry_closed" {
					t.Fatalf("error = %q, want entry_closed", codeOf(t, rec))
				}
				// 入口閘門在讀團練之前：被擋的人不會碰到團練／成員查詢，也不會寫任何東西
				if e.repo.called("MeetForLive") || e.repo.called("MemberStatus") {
					t.Fatalf("the entry gate must run before any meet/member read: %v", e.repo.calls)
				}
				if len(e.repo.audit) != 0 || len(e.mr.Keys()) != 0 {
					t.Fatal("entry_closed must not write audit rows or Redis state")
				}
			}
		})
	}
}

// requireEntry 已查過旗標並放進 ctx 時，/live/start 不重查 users。
func TestLiveStartReusesEntryFlagsFromContext(t *testing.T) {
	e := newStartEnv(t)
	e.repo.settings.EntryState = "whitelist"
	e.repo.settings.Whitelist = "ctx@x.com"
	uid := e.member(MemberJoined)
	// 假 repo 對這個 uid 回「不在白名單」，但 ctx 旗標說在白名單——若結果是 200，代表用的是 ctx 的旗標
	e.repo.flags[uid] = fakeFlags{email: "nope@x.com"}

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := context.WithValue(req.Context(), auth.CtxKeyUserID, uid)
			ctx = withEntryFlags(ctx, "ctx@x.com", "", false, false)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Post("/{id}/live/start", e.h.handle)
	req := httptest.NewRequest(http.MethodPost, "/"+liveMeetUUID+"/live/start", strings.NewReader(startBody(consentV1, false)))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if e.repo.called("UserFlags") {
		t.Fatal("UserFlags must not be queried again when requireEntry already put the flags in ctx")
	}
}

func TestLiveStartFlagsLookupErrorFailsClosed(t *testing.T) {
	e := newStartEnv(t)
	e.repo.flagsErr = errors.New("db down")
	rec := e.start(e.member(MemberJoined))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(e.mr.Keys()) != 0 {
		t.Fatal("must not grant when the entry gate could not be evaluated")
	}
}

// --- 同意（M8）---

func TestLiveStartConsent(t *testing.T) {
	t.Run("missing / invalid consent_v -> 400 consent_required, nothing written", func(t *testing.T) {
		for name, consent := range map[string]string{
			"absent": ``, "zero": `"consent_v":0,`, "negative": `"consent_v":-1,`, "null": `"consent_v":null,`,
		} {
			e := newStartEnv(t)
			rec := e.post(e.member(MemberJoined), liveMeetUUID, startBody(consent, false))
			if rec.Code != http.StatusBadRequest || codeOf(t, rec) != "consent_required" {
				t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
			}
			if len(e.repo.audit) != 0 || len(e.mr.Keys()) != 0 || e.repo.called("MeetForLive") {
				t.Errorf("%s: nothing may be read or written before consent is validated", name)
			}
		}
	})

	t.Run("first start writes exactly one audit row without coordinates", func(t *testing.T) {
		e := newStartEnv(t)
		uid := e.member(MemberJoined)
		rec := e.post(uid, liveMeetUUID, startBody(`"consent_v":2,`, false))
		if rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		if len(e.repo.audit) != 1 {
			t.Fatalf("audit rows = %d, want 1", len(e.repo.audit))
		}
		row := e.repo.audit[0]
		if row.uid != uid || row.meetID != liveMeetUUID || row.consentV != 2 || row.presenceOnly || row.ip != "198.51.100.9" {
			t.Fatalf("audit row = %+v", row)
		}
	})

	t.Run("presence-only meet is recorded in the audit meta", func(t *testing.T) {
		e := newStartEnv(t)
		e.repo.meet.NoLocation, e.repo.meet.Lat, e.repo.meet.Lng = true, nil, nil
		e.post(e.member(MemberJoined), liveMeetUUID, startBody(consentV1, false))
		if len(e.repo.audit) != 1 || !e.repo.audit[0].presenceOnly {
			t.Fatalf("audit = %+v", e.repo.audit)
		}
	})

	t.Run("reauth skips the audit row and needs no consent_v", func(t *testing.T) {
		e := newStartEnv(t)
		uid := e.member(MemberJoined)
		if rec := e.post(uid, liveMeetUUID, startBody(``, true)); rec.Code != http.StatusOK {
			t.Fatalf("reauth without consent_v: %d %s", rec.Code, rec.Body.String())
		}
		if rec := e.post(uid, liveMeetUUID, startBody(consentV1, true)); rec.Code != http.StatusOK {
			t.Fatalf("reauth with consent_v: %d", rec.Code)
		}
		if len(e.repo.audit) != 0 {
			t.Fatalf("reauth must not write audit rows: %+v", e.repo.audit)
		}
	})

	t.Run("audit failure -> 500 and no grant (no consent evidence, no sharing)", func(t *testing.T) {
		e := newStartEnv(t)
		e.repo.auditErr = errors.New("insert failed")
		rec := e.start(e.member(MemberJoined))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		if len(e.mr.Keys()) != 0 {
			t.Fatalf("no grant may exist without a consent record: %v", e.mr.Keys())
		}
	})
}

// meta 只有 consent_v 與 presence_only，不含座標（契約 §3.1）。
func TestConsentAuditMetaHasNoCoordinates(t *testing.T) {
	meta, err := consentAuditMeta(1, true)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m["consent_v"] != float64(1) || m["presence_only"] != true {
		t.Fatalf("meta = %s, want exactly {consent_v, presence_only}", meta)
	}
	for _, k := range []string{"lat", "lng", "la", "ln", "latitude", "longitude", "p", "sid", "name"} {
		if _, has := m[k]; has {
			t.Fatalf("meta must not contain %q: %s", k, meta)
		}
	}
	if consentAuditAction != "runmeet_live_consent" || consentAuditResource != "run_meet" {
		t.Fatal("audit action/resource drifted from the contract")
	}
}

// --- 回應 ---

func TestLiveStartResponseShape(t *testing.T) {
	e := newStartEnv(t)
	uid := e.member(MemberJoined)
	e.repo.names[uid] = [2]string{"小明", "xm"}
	rec := e.start(uid)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	num := func(k string) float64 { v, _ := out[k].(float64); return v }
	if num("pv") != 1 || num("n") != 1 || out["sid"] != startSID || num("iv") != 5000 || out["presence_only"] != false ||
		num("grant_ttl_s") != 900 || num("reauth_in_s") != 600 || num("rv") != 1 || num("live") != 0 || num("max_live") != 50 ||
		int64(num("server_now_ms")) != e.now.UnixMilli() {
		t.Fatalf("envelope: %s", rec.Body.String())
	}
	meet, _ := out["meet"].(map[string]any)
	if meet["id"] != liveMeetUUID || meet["title"] != "週六河濱團練" || meet["lat"] != 25.03 || meet["lng"] != 121.56 {
		t.Fatalf("meet: %v", meet)
	}
	roster, _ := out["roster"].([]any)
	if len(roster) != 1 || roster[0].([]any)[0].(float64) != 1 || roster[0].([]any)[1] != "小明" {
		t.Fatalf("roster: %v", out["roster"])
	}
	stale, _ := out["stale"].(map[string]any)
	if stale["fade_s"] != float64(30) || stale["gray_s"] != float64(75) || stale["drop_s"] != float64(180) {
		t.Fatalf("stale: %v", stale)
	}
	// 只允許契約列出的欄位
	allowed := map[string]bool{"pv": true, "n": true, "sid": true, "iv": true, "presence_only": true, "grant_ttl_s": true,
		"reauth_in_s": true, "meet": true, "rv": true, "roster": true, "live": true, "max_live": true, "stale": true, "server_now_ms": true}
	for k := range out {
		if !allowed[k] {
			t.Errorf("unexpected response field %q", k)
		}
	}
	// 不得出現 user id／email／帳號編碼
	body := rec.Body.String()
	for _, secret := range []string{uid, e.owner, "email", "account_code", "user_id"} {
		if strings.Contains(body, secret) {
			t.Fatalf("response leaks %q: %s", secret, body)
		}
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
}

func TestLiveStartPresenceOnlyMeetHasNoCoordinatesAnywhere(t *testing.T) {
	e := newStartEnv(t)
	e.repo.meet.NoLocation = true
	e.repo.meet.Lat, e.repo.meet.Lng = nil, nil
	uid := e.member(MemberJoined)
	rec := e.start(uid)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	meet, _ := out["meet"].(map[string]any)
	if _, has := meet["lat"]; has {
		t.Fatalf("presence-only meet must not carry lat: %v", meet)
	}
	if _, has := meet["lng"]; has {
		t.Fatalf("presence-only meet must not carry lng: %v", meet)
	}
	if out["presence_only"] != true {
		t.Fatalf("presence_only = %v", out["presence_only"])
	}
	if e.mr.HGet("rml:{"+liveMeetUUID+"}:meta", "po") != "1" {
		t.Fatal("start must record meta.po=1 for a presence-only meet")
	}
	// 接著走熱路徑：/pos 永不存座標、永不回 p
	api := live.NewHandlerWithStore(e.store)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), auth.CtxKeyUserID, uid)))
		})
	})
	r.Mount("/run-meet-live", api.Router())
	e.advance(2 * time.Second)
	req := httptest.NewRequest(http.MethodPost, "/run-meet-live/"+liveMeetUUID+"/pos",
		strings.NewReader(fmt.Sprintf(`{"pv":1,"sid":%q,"p":{"la":25.03321,"ln":121.56543,"ac":8,"fa":1}}`, startSID)))
	prec := httptest.NewRecorder()
	r.ServeHTTP(prec, req)
	if prec.Code != http.StatusOK || strings.Contains(prec.Body.String(), `"p":`) || !strings.Contains(prec.Body.String(), "presence_only") {
		t.Fatalf("pos: %d %s", prec.Code, prec.Body.String())
	}
	if e.mr.Exists("rml:{" + liveMeetUUID + "}:pos") {
		t.Fatal("presence-only: pos must never be stored")
	}
}

// 契約 §5：顯示名稱消毒（整合）。
func TestLiveStartSanitizesDisplayName(t *testing.T) {
	long := strings.Repeat("跑", 30)
	cases := []struct {
		name, handle, want string
	}{
		{"<script>alert(1)</script>", "h", "<script>alert(1)</sc…"},
		{"團練" + string(rune(0x202e)) + "gnp.exe", "h", "團練gnp.exe"},
		{long, "h", strings.Repeat("跑", 20) + "…"},
		{"🏃小明", "h", "🏃小明"},
		{"", "runner_01", "runner_01"},
		{string(rune(0x200b)), "runner_01", "runner_01"},
		{"", "", "跑者"},
	}
	for _, c := range cases {
		e := newStartEnv(t)
		uid := e.member(MemberJoined)
		e.repo.names[uid] = [2]string{c.name, c.handle}
		rec := e.start(uid)
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: %d %s", c.name, rec.Code, rec.Body.String())
		}
		var out struct {
			Roster [][]any `json:"roster"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if len(out.Roster) != 1 || out.Roster[0][1] != c.want {
			t.Errorf("name %q handle %q → roster %v, want %q", c.name, c.handle, out.Roster, c.want)
		}
		if got := e.mr.HGet("rml:{"+liveMeetUUID+"}:names", "1"); got != c.want {
			t.Errorf("name stored in Redis = %q, want %q", got, c.want)
		}
	}
}

// --- 名額、Lua 結果映射 ---

func TestLiveStartCapacityAndMaxFromSettings(t *testing.T) {
	e := newStartEnv(t)
	e.repo.settings.Max = 2
	a, b, c := e.member(MemberJoined), e.member(MemberJoined), e.member(MemberJoined)
	if e.start(a).Code != http.StatusOK || e.start(b).Code != http.StatusOK {
		t.Fatal("first two must fit")
	}
	rec := e.start(c)
	if rec.Code != http.StatusTooManyRequests || codeOf(t, rec) != "live_full" {
		t.Fatalf("third: %d %s, want 429 live_full", rec.Code, rec.Body.String())
	}
	// 滿額時已持有 grant 者的重驗不受影響（max_live 回報設定值）
	rec = e.post(a, liveMeetUUID, startBody(``, true))
	if rec.Code != http.StatusOK {
		t.Fatalf("holder reauth at full: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		MaxLive int `json:"max_live"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.MaxLive != 2 {
		t.Fatalf("max_live = %d, want 2", out.MaxLive)
	}
}

// 契約 §3.1：killed → 410 killed；dead → 410 meet_over。
func TestLiveStartKilledAndDead(t *testing.T) {
	e := newStartEnv(t)
	uid := e.member(MemberJoined)
	e.mr.Set(live.KillKey, "1")
	rec := e.start(uid)
	if rec.Code != http.StatusGone || codeOf(t, rec) != "killed" {
		t.Fatalf("kill: %d %s", rec.Code, rec.Body.String())
	}
	e.mr.Del(live.KillKey)

	if err := e.store.MarkDead(context.Background(), liveMeetUUID); err != nil {
		t.Fatal(err)
	}
	rec = e.start(uid)
	if rec.Code != http.StatusGone || codeOf(t, rec) != "meet_over" {
		t.Fatalf("dead: %d %s", rec.Code, rec.Body.String())
	}
}

// --- 撤銷競態（M1）---

// 契約 §9-9：Revoke 的時間 ≥ checkedAtMs → start 被拒。
// checkedAtMs 必須在讀 DB「之前」取得：這裡讓 fake 的 MemberStatus（＝DB 讀取）執行到一半時，
// 另一個請求 Kick 並 Revoke（Redis 時間前進 20 ms）——handler 讀到的還是舊的 joined，
// 但 start 的 Lua 必須因為墓碑時間晚於 checkedAtMs 而拒絕。若 checkedAtMs 是讀完 DB 才取的，
// 這個測試會得到 200（被踢者拿到 grant）。
func TestLiveStartRevokeDuringDBReadIsRejected(t *testing.T) {
	e := newStartEnv(t)
	uid := e.member(MemberJoined)
	e.repo.onMemberStatus = func() {
		e.advance(20 * time.Millisecond) // Kick 的 commit 與 Revoke 發生在 handler 取得 checkedAtMs 之後
		if err := e.store.Revoke(context.Background(), liveMeetUUID, uid); err != nil {
			t.Error(err)
		}
		// 之後 handler 才繼續讀 DB 並進 Lua：時間必須再往前走，否則「checkedAtMs 晚取」的錯誤實作會因為
		// 與墓碑同一毫秒的保守 tie 規則而僥倖被擋下，測試就抓不到它（已用變異測試驗證）。
		e.advance(20 * time.Millisecond)
	}
	rec := e.start(uid)
	if rec.Code != http.StatusForbidden || codeOf(t, rec) != "revoked" {
		t.Fatalf("%d %s, want 403 revoked", rec.Code, rec.Body.String())
	}
	if e.mr.HGet("rml:{"+liveMeetUUID+"}:grants", uid) != "" {
		t.Fatal("no grant may be written for a user revoked during the start")
	}
}

// 對照：撤銷發生在 start 開始「之前」（之後又重新加入）→ 正常通過，且墓碑被清掉。
func TestLiveStartAfterRevokeAndRejoinSucceeds(t *testing.T) {
	e := newStartEnv(t)
	uid := e.member(MemberJoined)
	if err := e.store.Revoke(context.Background(), liveMeetUUID, uid); err != nil {
		t.Fatal(err)
	}
	e.advance(3 * time.Second) // 之後使用者重新加入、再按開始
	rec := e.start(uid)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if e.mr.Exists("rml:{" + liveMeetUUID + "}:rv:" + uid) {
		t.Fatal("tombstone should be cleared by the successful start")
	}
}

// --- 輸入驗證 ---

func TestLiveStartInputValidation(t *testing.T) {
	e := newStartEnv(t)
	uid := e.member(MemberJoined)

	for _, id := range []string{"not-a-uuid", "a%7D:g:x%7B", "123"} {
		rec := e.post(uid, id, startBody(consentV1, false))
		if rec.Code != http.StatusNotFound {
			t.Errorf("id %q: %d, want 404", id, rec.Code)
		}
	}
	if rec := e.post(uid, liveMeetUUID, `{"pv":2,"sid":"startsid00000001","consent_v":1}`); rec.Code != http.StatusUpgradeRequired {
		t.Errorf("pv: %d", rec.Code)
	}
	if rec := e.post(uid, liveMeetUUID, `{"sid":"startsid00000001","consent_v":1}`); rec.Code != http.StatusUpgradeRequired {
		t.Errorf("pv missing: %d", rec.Code)
	}
	if rec := e.post(uid, liveMeetUUID, `{"pv":1,"sid":"short","consent_v":1}`); rec.Code != http.StatusBadRequest || codeOf(t, rec) != "bad_sid" {
		t.Errorf("sid: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.post(uid, liveMeetUUID, `not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json: %d", rec.Code)
	}
	big := fmt.Sprintf(`{"pv":1,"sid":"startsid00000001","consent_v":1,"junk":%q}`, strings.Repeat("x", 600))
	if rec := e.post(uid, liveMeetUUID, big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("512 B limit: %d", rec.Code)
	}
	if e.repo.called("MeetForLive") {
		t.Error("malformed requests must be rejected before any DB read")
	}
	// 沒有使用者 → 401
	if rec := e.post("", liveMeetUUID, startBody(consentV1, false)); rec.Code != http.StatusUnauthorized {
		t.Errorf("no user: %d", rec.Code)
	}
	// Redis 不可用 → 503（fail-closed；取 checkedAtMs 就失敗，連 DB 都不碰）
	e.mr.Close()
	e.repo.calls = nil
	rec := e.post(uid, liveMeetUUID, startBody(consentV1, false))
	if rec.Code != http.StatusServiceUnavailable || codeOf(t, rec) != "redis_unavailable" {
		t.Errorf("redis down: %d %s", rec.Code, rec.Body.String())
	}
	if e.repo.called("MeetForLive") || e.repo.called("UserFlags") {
		t.Errorf("with Redis down the handler must fail before touching the DB: %v", e.repo.calls)
	}
}

func TestLiveStartWithoutRedisIs503(t *testing.T) {
	h := &Handler{} // liveStart 未接線
	req := httptest.NewRequest(http.MethodPost, "/x/live/start", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.LiveStart(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d", rec.Code)
	}
	s := &liveStartHandler{repo: &fakeLiveRepo{}, store: nil} // rdb 為 nil 時 NewHandler 傳進來的 store
	r := chi.NewRouter()
	uid := uuid.NewString()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), auth.CtxKeyUserID, uid)))
		})
	})
	r.Post("/{id}/live/start", s.handle)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/"+liveMeetUUID+"/live/start", strings.NewReader(startBody(consentV1, false))))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil store: %d %s", rec.Code, rec.Body.String())
	}
}

// --- 端到端（沒有 DB）：start → /pos → 被踢 → /pos revoked ---

func TestLiveStartThenHotPathThenKick(t *testing.T) {
	e := newStartEnv(t)
	a, b := e.member(MemberJoined), e.member(MemberJoined)
	e.repo.names[a] = [2]string{"小明", "xm"}
	e.repo.names[b] = [2]string{"阿華", "ah"}
	if rec := e.start(a); rec.Code != http.StatusOK {
		t.Fatalf("a: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.start(b); rec.Code != http.StatusOK {
		t.Fatalf("b: %d %s", rec.Code, rec.Body.String())
	}

	hot := func(uid, fix string) *httptest.ResponseRecorder {
		r := chi.NewRouter()
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), auth.CtxKeyUserID, uid)))
			})
		})
		r.Mount("/run-meet-live", live.NewHandlerWithStore(e.store).Router())
		body := fmt.Sprintf(`{"pv":1,"sid":%q,"p":%s}`, startSID, fix)
		req := httptest.NewRequest(http.MethodPost, "/run-meet-live/"+liveMeetUUID+"/pos", strings.NewReader(body))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	const fa = `{"la":25.03321,"ln":121.56543,"ac":8,"fa":1}`
	const fb = `{"la":25.03401,"ln":121.56612,"ac":6,"fa":1}`
	e.advance(2 * time.Second)
	hot(a, fa)
	e.advance(2 * time.Second)
	if rec := hot(b, fb); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "25.03321") {
		t.Fatalf("b must see a: %d %s", rec.Code, rec.Body.String())
	}
	e.advance(2 * time.Second)

	// 發起人踢 b：repository 在 commit 之後呼叫 Revoke
	if err := e.store.Revoke(context.Background(), liveMeetUUID, b); err != nil {
		t.Fatal(err)
	}
	if rec := hot(b, fb); rec.Code != http.StatusForbidden || codeOf(t, rec) != "revoked" {
		t.Fatalf("kicked b's next /pos: %d %s, want 403 revoked", rec.Code, rec.Body.String())
	}
	if rec := hot(a, fa); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "25.03401") {
		t.Fatalf("b's dot must be gone from a's snapshot: %d %s", rec.Code, rec.Body.String())
	}
	// 被踢後再按開始：DB 已不是 joined → 403 not_member
	e.repo.members[b] = MemberKicked
	if rec := e.start(b); rec.Code != http.StatusForbidden || codeOf(t, rec) != "not_member" {
		t.Fatalf("kicked b's start: %d %s", rec.Code, rec.Body.String())
	}
}
