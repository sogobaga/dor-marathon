//go:build integration

// live_start_integration_test.go：團練同步跑 POST /run-meets/{id}/live/start 的 DB 面、撤銷掛鉤與
// AdminRestore 的 RETURNING 語句，在「真實 PostgreSQL」上跑一次（單元測試 live_start_test.go 全用 fake repo，
// 這些 SQL 從未對真庫執行過）。
//
// 需要 Neon 暫存分支（schema 已含 run_meets / run_meet_members / audit_logs / app_settings，即 migration
// 001/033/042/156/161/168…）。比照 internal/gpsrawlog/integration_test.go 的慣例：獨立 build tag，預設
// `go test ./...` 不會編譯；只在
//
//	DOR_TEST_DATABASE_URL=<暫存分支直連 URI> go test -tags integration -run TestIntegration ./internal/runmeet/
//
// 時執行，未設定則整支 Skip。
//
// ⚠️ 絕不可指向正式庫：本檔會 INSERT 假使用者／假團練／稽核列／app_settings 列（結束時全數刪除，並在最後
// 一個 cleanup 以 COUNT 對帳「殘留 = 0」）。若行程環境有 DATABASE_URL 且主機相同，直接拒絕。
// ⚠️ 輸出只含筆數／布林／本測試自己建立的列的 id；不印任何從庫裡讀回的 email／姓名／座標。
package runmeet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/runmeet/live"
)

const rmlItSID = "rmlitsid00000001" // 16 字元 [a-z0-9]，符合 live.ValidSID

// --- 連線 ---

var rmlItRedactRE = regexp.MustCompile(`(?i)(postgres(?:ql)?://\S+|host=\S+|password=\S+|user=\S+)`)

// rmlItRedact 連線錯誤訊息可能帶主機／使用者／DSN，輸出前一律遮掉。
func rmlItRedact(s string) string { return rmlItRedactRE.ReplaceAllString(s, "<redacted>") }

func rmlItHostOf(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	return strings.Replace(strings.ToLower(u.Hostname()), "-pooler", "", 1)
}

func rmlItPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("DOR_TEST_DATABASE_URL not set; skipping Neon integration test")
	}
	if prod := os.Getenv("DATABASE_URL"); prod != "" && rmlItHostOf(prod) != "" && rmlItHostOf(prod) == rmlItHostOf(dsn) {
		t.Fatal("refusing to run: DOR_TEST_DATABASE_URL has the same host as DATABASE_URL")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DOR_TEST_DATABASE_URL: %s", rmlItRedact(err.Error()))
	}
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %s", rmlItRedact(err.Error()))
	}
	t.Cleanup(pool.Close) // 最先註冊 → 最後執行（其他 cleanup 都還要用連線池）
	// Neon 暫存分支的 compute 可能剛醒，重試最多 90 秒。
	deadline := time.Now().Add(90 * time.Second)
	for {
		pctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err = pool.Ping(pctx)
		cancel()
		if err == nil {
			return pool
		}
		if time.Now().After(deadline) {
			t.Fatalf("branch database not reachable: %s", rmlItRedact(err.Error()))
		}
		time.Sleep(3 * time.Second)
	}
}

// --- fixture ---

type rmlItUser struct{ ID, Email, Handle, Name string }

// rmlItSettingKeys 本測試會碰的 app_settings 鍵：開始前先存起來（若分支上本來就有列）、刪掉，結束時還原。
var rmlItSettingKeys = []string{
	live.EntryStateKey, live.EntryWhitelistKey, live.MaxKey, live.PreMinutesKey, live.DefaultHoursKey, live.GraceMinutesKey,
	EntryStateKey, EntryWhitelistKey, // runmeet_entry_state / runmeet_entry_whitelist（Router() 子測試用）
}

type rmlItFx struct {
	t    *testing.T
	pool *pgxpool.Pool
	ctx  context.Context

	userIDs, meetIDs []string
	stash            map[string]string

	mr  *miniredis.Miniredis
	rdb *redis.Client
	h   *Handler // NewHandler(pool, rdb, nil)：真實接線（pgLiveRepo + live.Store + 撤銷掛鉤 + kill 回呼）
}

func newRmlItFx(t *testing.T, pool *pgxpool.Pool) *rmlItFx {
	t.Helper()
	f := &rmlItFx{t: t, pool: pool, ctx: context.Background()}
	// LIFO：先註冊的後執行。殘留對帳必須在所有清理之後，所以最先註冊。
	t.Cleanup(f.verifyNoResidue)
	t.Cleanup(f.deleteAll)
	f.stashSettings()

	f.mr = miniredis.RunT(t)
	f.rdb = redis.NewClient(&redis.Options{Addr: f.mr.Addr()})
	t.Cleanup(func() { _ = f.rdb.Close() })
	f.h = NewHandler(pool, f.rdb, nil)
	t.Cleanup(f.h.killSwitchOff)
	return f
}

func (f *rmlItFx) stashSettings() {
	t := f.t
	rows, err := f.pool.Query(f.ctx, `SELECT key, value FROM app_settings WHERE key = ANY($1::text[])`, rmlItSettingKeys)
	if err != nil {
		t.Fatalf("stash app_settings: %v", err)
	}
	f.stash = map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			t.Fatalf("stash app_settings scan: %v", err)
		}
		f.stash[k] = v
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("stash app_settings rows: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx, `DELETE FROM app_settings WHERE key = ANY($1::text[])`, rmlItSettingKeys); err != nil {
		t.Fatalf("clear app_settings: %v", err)
	}
	appsettings.InvalidateCache()
	t.Logf("app_settings: %d pre-existing runmeet live/entry rows stashed (values never logged); they are restored on cleanup", len(f.stash))
	t.Cleanup(func() {
		bg := context.Background()
		if _, err := f.pool.Exec(bg, `DELETE FROM app_settings WHERE key = ANY($1::text[])`, rmlItSettingKeys); err != nil {
			t.Errorf("cleanup app_settings: %v", err)
		}
		for k, v := range f.stash {
			if _, err := f.pool.Exec(bg, `
				INSERT INTO app_settings (key, value) VALUES ($1,$2)
				ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`, k, v); err != nil {
				t.Errorf("restore app_settings %s: %v", k, err)
			}
		}
		appsettings.InvalidateCache()
	})
}

func (f *rmlItFx) setSetting(t *testing.T, key, value string) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES ($1,$2,NOW())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`, key, value); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
	appsettings.InvalidateCache()
}

func (f *rmlItFx) clearSettings(t *testing.T, keys ...string) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `DELETE FROM app_settings WHERE key = ANY($1::text[])`, keys); err != nil {
		t.Errorf("clear settings: %v", err)
	}
	appsettings.InvalidateCache()
}

func (f *rmlItFx) deleteAll() {
	bg := context.Background()
	t := f.t
	if _, err := f.pool.Exec(bg, `
		DELETE FROM audit_logs
		 WHERE action=$1 AND (resource_id = ANY($2::uuid[]) OR user_id = ANY($3::uuid[]))`,
		consentAuditAction, f.meetIDs, f.userIDs); err != nil {
		t.Errorf("cleanup audit_logs: %v", err)
	}
	if _, err := f.pool.Exec(bg, `DELETE FROM run_meets WHERE id = ANY($1::uuid[])`, f.meetIDs); err != nil {
		t.Errorf("cleanup run_meets: %v", err)
	}
	if _, err := f.pool.Exec(bg, `DELETE FROM users WHERE id = ANY($1::uuid[])`, f.userIDs); err != nil {
		t.Errorf("cleanup users: %v", err)
	}
}

// verifyNoResidue 所有清理跑完後對帳：本測試建立的列一筆都不能留。
func (f *rmlItFx) verifyNoResidue() {
	bg := context.Background()
	t := f.t
	var users, meets, members, audit, fakeEmail, settings int
	count := func(dst *int, q string, args ...any) {
		if err := f.pool.QueryRow(bg, q, args...).Scan(dst); err != nil {
			t.Errorf("residue query: %v", err)
		}
	}
	count(&users, `SELECT COUNT(*) FROM users WHERE id = ANY($1::uuid[])`, f.userIDs)
	count(&meets, `SELECT COUNT(*) FROM run_meets WHERE id = ANY($1::uuid[])`, f.meetIDs)
	count(&members, `SELECT COUNT(*) FROM run_meet_members WHERE meet_id = ANY($1::uuid[])`, f.meetIDs)
	count(&audit, `SELECT COUNT(*) FROM audit_logs WHERE action=$1 AND (resource_id = ANY($2::uuid[]) OR user_id = ANY($3::uuid[]))`,
		consentAuditAction, f.meetIDs, f.userIDs)
	count(&fakeEmail, `SELECT COUNT(*) FROM users WHERE email LIKE 'rml-it-%@example.invalid'`)
	count(&settings, `SELECT COUNT(*) FROM app_settings WHERE key = ANY($1::text[])`, rmlItSettingKeys)
	t.Logf("RESIDUE after cleanup: users=%d meets=%d members=%d audit_rows=%d fake_email_users=%d runmeet_setting_rows=%d (stashed originals=%d)",
		users, meets, members, audit, fakeEmail, settings, len(f.stash))
	if users+meets+members+audit+fakeEmail != 0 {
		t.Errorf("rows created by this test were left behind")
	}
	if settings != len(f.stash) {
		t.Errorf("app_settings not restored: %d rows, want %d", settings, len(f.stash))
	}
}

func (f *rmlItFx) user(t *testing.T, tag, name string) rmlItUser {
	t.Helper()
	sfx := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	u := rmlItUser{
		Email:  fmt.Sprintf("rml-it-%s-%s@example.invalid", tag, sfx),
		Handle: fmt.Sprintf("rmlit_%s_%s", tag, sfx),
		Name:   name,
	}
	if err := f.pool.QueryRow(f.ctx, `
		INSERT INTO users (email, handle, name, password_hash) VALUES ($1,$2,$3,'x')
		RETURNING id::text`, u.Email, u.Handle, u.Name).Scan(&u.ID); err != nil {
		t.Fatalf("create user %q: %v", tag, err)
	}
	f.userIDs = append(f.userIDs, u.ID)
	return u
}

type rmlItMeetOpt struct {
	title       string
	meetAt      time.Time
	endsAt      *time.Time
	lat, lng    *float64
	noLocation  bool
	status      string
	hiddenAdmin bool
	deleted     bool
}

// meet 直接 INSERT 一個團練＋發起人的成員列（role=owner,status=joined，member_count=1）。
func (f *rmlItFx) meet(t *testing.T, owner rmlItUser, o rmlItMeetOpt) string {
	t.Helper()
	if o.title == "" {
		o.title = "RML-IT meet"
	}
	if o.meetAt.IsZero() {
		o.meetAt = time.Now().UTC().Truncate(time.Microsecond)
	}
	if o.status == "" {
		o.status = StatusOpen
	}
	var delAt *time.Time
	if o.deleted {
		n := time.Now().UTC()
		delAt = &n
	}
	reason := ""
	if o.hiddenAdmin {
		reason = "rml-it"
	}
	var id string
	if err := f.pool.QueryRow(f.ctx, `
		INSERT INTO run_meets (owner_id, title, meet_at, ends_at, region, place_label, lat, lng, no_location,
		                       capacity, member_count, pending_count, status, quota_month,
		                       hidden_by_admin, hidden_reason, deleted_at)
		VALUES ($1::uuid,$2,$3,$4,'RML-IT','RML-IT place',$5,$6,$7,10,1,0,$8,'2099-01',$9,$10,$11)
		RETURNING id::text`,
		owner.ID, o.title, o.meetAt, o.endsAt, o.lat, o.lng, o.noLocation, o.status, o.hiddenAdmin, reason, delAt).Scan(&id); err != nil {
		t.Fatalf("create meet: %v", err)
	}
	f.meetIDs = append(f.meetIDs, id)
	if _, err := f.pool.Exec(f.ctx, `
		INSERT INTO run_meet_members (meet_id, user_id, role, status, joined_at)
		VALUES ($1::uuid,$2::uuid,'owner','joined',NOW())`, id, owner.ID); err != nil {
		t.Fatalf("create owner member row: %v", err)
	}
	return id
}

// member 加一列成員並同步維護 member_count／pending_count（讓 Kick／Leave 的 GREATEST(count-1,0) 有意義）。
func (f *rmlItFx) member(t *testing.T, meetID string, u rmlItUser, status string) {
	t.Helper()
	var joinedAt *time.Time
	if status == MemberJoined {
		n := time.Now().UTC()
		joinedAt = &n
	}
	if _, err := f.pool.Exec(f.ctx, `
		INSERT INTO run_meet_members (meet_id, user_id, role, status, joined_at)
		VALUES ($1::uuid,$2::uuid,'member',$3,$4)`, meetID, u.ID, status, joinedAt); err != nil {
		t.Fatalf("add member (%s): %v", status, err)
	}
	col := ""
	switch status {
	case MemberJoined:
		col = "member_count"
	case MemberPending:
		col = "pending_count"
	}
	if col != "" {
		if _, err := f.pool.Exec(f.ctx, `UPDATE run_meets SET `+col+`=`+col+`+1 WHERE id=$1::uuid`, meetID); err != nil {
			t.Fatalf("bump %s: %v", col, err)
		}
	}
}

func (f *rmlItFx) setMeet(t *testing.T, meetID, assignment string) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `UPDATE run_meets SET `+assignment+` WHERE id=$1::uuid`, meetID); err != nil {
		t.Fatalf("UPDATE run_meets SET %s: %v", assignment, err)
	}
}

func (f *rmlItFx) dbMemberStatus(t *testing.T, meetID, uid string) string {
	t.Helper()
	var s string
	if err := f.pool.QueryRow(f.ctx, `
		SELECT COALESCE((SELECT status FROM run_meet_members WHERE meet_id=$1::uuid AND user_id=$2::uuid),'<none>')`,
		meetID, uid).Scan(&s); err != nil {
		t.Fatalf("read member status: %v", err)
	}
	return s
}

func (f *rmlItFx) dbCounts(t *testing.T, meetID string) (members, pending int) {
	t.Helper()
	if err := f.pool.QueryRow(f.ctx, `SELECT member_count::int, pending_count::int FROM run_meets WHERE id=$1::uuid`, meetID).
		Scan(&members, &pending); err != nil {
		t.Fatalf("read counts: %v", err)
	}
	return
}

type rmlItAudit struct {
	ID                                         int64
	UserID, Action, Resource, ResourceID, Meta string
	IP                                         string
	CreatedAtSet                               bool
}

func (f *rmlItFx) auditRows(t *testing.T, meetID, uid string) []rmlItAudit {
	t.Helper()
	rows, err := f.pool.Query(f.ctx, `
		SELECT id, COALESCE(user_id::text,''), action, COALESCE(resource,''), COALESCE(resource_id::text,''),
		       COALESCE(meta::text,''), COALESCE(ip,''), created_at IS NOT NULL
		  FROM audit_logs
		 WHERE resource_id=$1::uuid AND user_id=$2::uuid AND action=$3
		 ORDER BY id`, meetID, uid, consentAuditAction)
	if err != nil {
		t.Fatalf("read audit rows: %v", err)
	}
	defer rows.Close()
	var out []rmlItAudit
	for rows.Next() {
		var a rmlItAudit
		if err := rows.Scan(&a.ID, &a.UserID, &a.Action, &a.Resource, &a.ResourceID, &a.Meta, &a.IP, &a.CreatedAtSet); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("audit rows: %v", err)
	}
	return out
}

func rmlItAssertConsentMeta(t *testing.T, metaText string, wantV int, wantPO bool) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(metaText), &m); err != nil {
		t.Fatalf("audit meta is not JSON: %v", err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) != 2 || keys[0] != "consent_v" || keys[1] != "presence_only" {
		t.Errorf("audit meta keys = %v, want exactly [consent_v presence_only]", keys)
	}
	if v, _ := m["consent_v"].(float64); v != float64(wantV) {
		t.Errorf("meta.consent_v = %v, want %d", m["consent_v"], wantV)
	}
	if v, ok := m["presence_only"].(bool); !ok || v != wantPO {
		t.Errorf("meta.presence_only = %v, want %v", m["presence_only"], wantPO)
	}
}

// --- HTTP 小工具 ---

func rmlItUserCtx(uid string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if uid != "" {
				req = req.WithContext(context.WithValue(req.Context(), auth.CtxKeyUserID, uid))
			}
			next.ServeHTTP(w, req)
		})
	}
}

func rmlItBody(consent, reauth bool) string {
	c := ""
	if consent {
		c = `"consent_v":1,`
	}
	return fmt.Sprintf(`{"pv":1,"sid":%q,%s"reauth":%t}`, rmlItSID, c, reauth)
}

// postStart 直接打 Handler.LiveStart（NewHandler 接好的真實 pgLiveRepo + live.Store），使用者 ctx 比照單元測試注入。
func (f *rmlItFx) postStart(uid, meetID, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Use(rmlItUserCtx(uid))
	r.Post("/{id}/live/start", f.h.LiveStart)
	req := httptest.NewRequest(http.MethodPost, "/"+meetID+"/live/start", strings.NewReader(body))
	req.RemoteAddr = "198.51.100.9:5555"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

type rmlItStart struct {
	Error        string    `json:"error"`
	OpensAt      time.Time `json:"opens_at"`
	ClosesAt     time.Time `json:"closes_at"`
	PV           int       `json:"pv"`
	N            int       `json:"n"`
	SID          string    `json:"sid"`
	PresenceOnly bool      `json:"presence_only"`
	Meet         struct {
		ID    string   `json:"id"`
		Title string   `json:"title"`
		Lat   *float64 `json:"lat"`
		Lng   *float64 `json:"lng"`
	} `json:"meet"`
	Roster  [][]any `json:"roster"`
	MaxLive int     `json:"max_live"`
}

func rmlItDecode(t *testing.T, rec *httptest.ResponseRecorder) rmlItStart {
	t.Helper()
	var out rmlItStart
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON (status %d)", rec.Code)
	}
	return out
}

// expect 檢查狀態碼與錯誤碼（wantErr 為空＝不檢查）。
func rmlItExpect(t *testing.T, label string, rec *httptest.ResponseRecorder, wantCode int, wantErr string) rmlItStart {
	t.Helper()
	out := rmlItDecode(t, rec)
	if rec.Code != wantCode || (wantErr != "" && out.Error != wantErr) {
		t.Errorf("%s: status %d error %q, want %d %q", label, rec.Code, out.Error, wantCode, wantErr)
	} else {
		t.Logf("%s: HTTP %d error=%q", label, rec.Code, out.Error)
	}
	return out
}

// --- 撤銷掛鉤記錄器：呼叫當下用「另一條連線」探測 DB，證明掛鉤在 commit 之後才呼叫 ---

type rmlItCall struct{ op, meetID, uid, probe string }

type rmlItRec struct {
	pool  *pgxpool.Pool
	mu    sync.Mutex
	calls []rmlItCall
}

var _ liveHooks = (*rmlItRec)(nil)

func (r *rmlItRec) add(c rmlItCall) {
	r.mu.Lock()
	r.calls = append(r.calls, c)
	r.mu.Unlock()
}

func (r *rmlItRec) probeMeet(ctx context.Context, meetID string) string {
	var s string
	err := r.pool.QueryRow(ctx, `
		SELECT status || '|deleted=' || (deleted_at IS NOT NULL)::text || '|hidden=' || hidden_by_admin::text
		  FROM run_meets WHERE id=$1::uuid`, meetID).Scan(&s)
	if err != nil {
		return "probe_error:" + err.Error()
	}
	return s
}

func (r *rmlItRec) Revoke(ctx context.Context, meetID, uid string) error {
	var s string
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT status FROM run_meet_members WHERE meet_id=$1::uuid AND user_id=$2::uuid),'<none>')`,
		meetID, uid).Scan(&s)
	if err != nil {
		s = "probe_error:" + err.Error()
	}
	r.add(rmlItCall{"revoke", meetID, uid, s})
	return nil
}

func (r *rmlItRec) MarkDead(ctx context.Context, meetID string) error {
	r.add(rmlItCall{"mark_dead", meetID, "", r.probeMeet(ctx, meetID)})
	return nil
}

func (r *rmlItRec) ClearDead(ctx context.Context, meetID string) error {
	r.add(rmlItCall{"clear_dead", meetID, "", r.probeMeet(ctx, meetID)})
	return nil
}

func (r *rmlItRec) snapshot() []rmlItCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]rmlItCall(nil), r.calls...)
}

// want 比對呼叫序列；每個期望寫成 "op|meetID|uid|probe"。
func (r *rmlItRec) want(t *testing.T, label string, want ...string) {
	t.Helper()
	got := r.snapshot()
	gs := make([]string, 0, len(got))
	for _, c := range got {
		gs = append(gs, c.op+"|"+c.meetID+"|"+c.uid+"|"+c.probe)
	}
	if strings.Join(gs, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s: hook calls\n got  %q\n want %q", label, gs, want)
	} else {
		t.Logf("%s: hook calls = %d (as expected)", label, len(gs))
	}
}

// =====================================================================================

func TestIntegrationLiveStartDB(t *testing.T) {
	pool := rmlItPool(t)
	f := newRmlItFx(t, pool)
	ctx := f.ctx
	pg := &pgLiveRepo{db: pool, repo: NewRepository(pool)}

	// ---- 共用 fixture（唯讀居多；會被改狀態的子測試另建自己的團練）----
	A := f.user(t, "a", "RML IT Alpha")    // 發起人
	B := f.user(t, "b", "RML IT Bravo")    // joined
	C := f.user(t, "c", "RML IT Charlie")  // pending
	S := f.user(t, "s", "RML IT Stranger") // 沒有任何成員列
	E := f.user(t, "e", "")                // name 為空字串 → 顯示名稱退回 handle
	meetAt := time.Now().UTC().Truncate(time.Microsecond)
	lat, lng := 25.0330, 121.5654
	pub := f.meet(t, A, rmlItMeetOpt{title: "RML-IT public", meetAt: meetAt, lat: &lat, lng: &lng}) // ends_at NULL、有座標
	endsAt := meetAt.Add(2 * time.Hour)
	po := f.meet(t, A, rmlItMeetOpt{title: "RML-IT presence-only", meetAt: meetAt, endsAt: &endsAt, noLocation: true}) // 無座標、有 ends_at
	f.member(t, pub, B, MemberJoined)
	f.member(t, pub, C, MemberPending)
	f.member(t, po, B, MemberJoined)
	t.Logf("fixture: users=%d meets=%d (ids are this test's own rows)", len(f.userIDs), len(f.meetIDs))

	// -------------------------------------------------------------------------------
	t.Run("00_schema_version", func(t *testing.T) {
		var latest string
		var maxNum, n int
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(MAX(version),''),
			       COALESCE(MAX(version::int) FILTER (WHERE version ~ '^[0-9]{1,4}$'), 0),
			       COUNT(*)
			  FROM schema_migrations`).Scan(&latest, &maxNum, &n); err != nil {
			t.Fatalf("schema_migrations: %v", err)
		}
		t.Logf("SCHEMA VERSION: max_numeric=%d lexicographic_max=%s rows=%d", maxNum, latest, n)
		var cols int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM information_schema.columns
			 WHERE table_schema = current_schema()
			   AND ((table_name='run_meets' AND column_name IN ('ends_at','no_location','hidden_by_admin','deleted_at','hidden_by_owner','lat','lng'))
			     OR (table_name='audit_logs' AND column_name IN ('user_id','action','resource','resource_id','meta','ip')))`).Scan(&cols); err != nil {
			t.Fatalf("information_schema: %v", err)
		}
		if cols != 13 {
			t.Fatalf("expected 13 required columns present, found %d (schema too old for the live code)", cols)
		}
	})

	// -------------------------------------------------------------------------------
	t.Run("01_MeetForLive", func(t *testing.T) {
		t.Run("public_nonnull_coords_null_ends_at", func(t *testing.T) {
			m, err := pg.MeetForLive(ctx, pub)
			if err != nil {
				t.Fatalf("MeetForLive: %v", err)
			}
			if m.ID != pub || m.OwnerID != A.ID || m.Title != "RML-IT public" || m.Status != StatusOpen || m.NoLocation {
				t.Errorf("fields mismatch: id_ok=%v owner_ok=%v title_ok=%v status=%q noloc=%v",
					m.ID == pub, m.OwnerID == A.ID, m.Title == "RML-IT public", m.Status, m.NoLocation)
			}
			if m.Lat == nil || m.Lng == nil || *m.Lat != lat || *m.Lng != lng {
				t.Errorf("Lat/Lng scan: lat_nil=%v lng_nil=%v (want non-nil, equal to inserted)", m.Lat == nil, m.Lng == nil)
			}
			if m.EndsAt != nil {
				t.Errorf("EndsAt should be nil for ends_at NULL")
			}
			if !m.MeetAt.Equal(meetAt) {
				t.Errorf("MeetAt differs by %v", m.MeetAt.Sub(meetAt))
			}
		})
		t.Run("presence_only_null_coords_set_ends_at", func(t *testing.T) {
			m, err := pg.MeetForLive(ctx, po)
			if err != nil {
				t.Fatalf("MeetForLive: %v", err)
			}
			if !m.NoLocation || m.Lat != nil || m.Lng != nil {
				t.Errorf("NoLocation=%v lat_nil=%v lng_nil=%v, want true/true/true", m.NoLocation, m.Lat == nil, m.Lng == nil)
			}
			if m.EndsAt == nil || !m.EndsAt.Equal(endsAt.Truncate(time.Microsecond)) {
				t.Errorf("EndsAt scan: nil=%v", m.EndsAt == nil)
			}
		})
		t.Run("unknown_uuid_is_errNotFound", func(t *testing.T) {
			_, err := pg.MeetForLive(ctx, uuid.NewString())
			if !errors.Is(err, errNotFound) || !errIsNotFound(err) {
				t.Fatalf("err = %v, want errNotFound", err)
			}
		})
		t.Run("malformed_id_errors_but_is_not_errNotFound", func(t *testing.T) {
			// handler 在 live.ParseMeetID 就擋掉非 UUID；這裡只確認萬一漏進來是「錯誤」而不是 panic／誤判 404。
			_, err := pg.MeetForLive(ctx, "not-a-uuid")
			if err == nil || errIsNotFound(err) {
				t.Fatalf("err = %v, want a non-NotFound DB error (22P02)", err)
			}
			t.Logf("malformed id → %v", err)
		})
		t.Run("state_filters", func(t *testing.T) {
			tmp := f.meet(t, A, rmlItMeetOpt{title: "RML-IT tmp"})
			must := func(label string, wantFound bool, wantStatus string) {
				t.Helper()
				m, err := pg.MeetForLive(ctx, tmp)
				switch {
				case wantFound && err != nil:
					t.Errorf("%s: want found, got err %v", label, err)
				case wantFound && wantStatus != "" && m.Status != wantStatus:
					t.Errorf("%s: status %q, want %q", label, m.Status, wantStatus)
				case !wantFound && !errors.Is(err, errNotFound):
					t.Errorf("%s: want errNotFound, got %v", label, err)
				default:
					t.Logf("%s: ok (found=%v)", label, wantFound)
				}
			}
			must("baseline", true, StatusOpen)
			f.setMeet(t, tmp, `hidden_by_admin=TRUE`)
			must("hidden_by_admin=TRUE", false, "")
			f.setMeet(t, tmp, `hidden_by_admin=FALSE`)
			must("hidden_by_admin=FALSE again", true, StatusOpen)
			f.setMeet(t, tmp, `deleted_at=NOW()`)
			must("deleted_at=NOW()", false, "")
			f.setMeet(t, tmp, `deleted_at=NULL`)
			must("deleted_at=NULL again", true, StatusOpen)
			f.setMeet(t, tmp, `hidden_by_owner=TRUE`)
			must("hidden_by_owner=TRUE (must NOT hide it)", true, StatusOpen)
			f.setMeet(t, tmp, `hidden_by_owner=FALSE, status='cancelled'`)
			must("status=cancelled passes through", true, StatusCancelled)
			f.setMeet(t, tmp, `status='closed'`)
			must("status=closed passes through", true, StatusClosed)
		})
	})

	// -------------------------------------------------------------------------------
	t.Run("02_MemberStatus", func(t *testing.T) {
		check := func(label, meetID, uid, want string) {
			t.Helper()
			got, err := pg.MemberStatus(ctx, meetID, uid)
			if err != nil || got != want {
				t.Errorf("%s: got %q err=%v, want %q nil", label, got, err, want)
			} else {
				t.Logf("%s: %q err=nil", label, got)
			}
		}
		check("joined member", pub, B.ID, MemberJoined)
		check("pending applicant", pub, C.ID, MemberPending)
		check("stranger (real user, no row)", pub, S.ID, "")
		check("random uuid user (no row)", pub, uuid.NewString(), "")
		check("owner row", pub, A.ID, MemberJoined)
		check("member of another meet only", po, C.ID, "")
		// 五個合法 status 值都原樣讀回
		tmp := f.meet(t, A, rmlItMeetOpt{title: "RML-IT status"})
		f.member(t, tmp, E, MemberPending)
		for _, st := range []string{MemberRejected, MemberKicked, MemberLeft, MemberPending, MemberJoined} {
			if _, err := pool.Exec(ctx, `UPDATE run_meet_members SET status=$3 WHERE meet_id=$1::uuid AND user_id=$2::uuid`, tmp, E.ID, st); err != nil {
				t.Fatalf("set status %s: %v", st, err)
			}
			check("status="+st, tmp, E.ID, st)
		}
	})

	// -------------------------------------------------------------------------------
	t.Run("03_DisplayName_and_UserFlags", func(t *testing.T) {
		name, handle, err := pg.DisplayName(ctx, A.ID)
		if err != nil || name != A.Name || handle != A.Handle {
			t.Errorf("DisplayName(A): name_ok=%v handle_ok=%v err=%v", name == A.Name, handle == A.Handle, err)
		}
		if got := live.SanitizeDisplayName(name, handle); got != A.Name {
			t.Errorf("sanitized(A) mismatch")
		}
		name, handle, err = pg.DisplayName(ctx, E.ID)
		if err != nil || name != "" || handle != E.Handle {
			t.Errorf("DisplayName(E, empty name): name=%q handle_ok=%v err=%v", name, handle == E.Handle, err)
		}
		if got := live.SanitizeDisplayName(name, handle); got != E.Handle {
			t.Errorf("empty name must fall back to handle")
		}
		if _, _, err = pg.DisplayName(ctx, uuid.NewString()); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("unknown uid: err=%v, want pgx.ErrNoRows", err)
		}
		// UserFlags 是 /live/start 入口閘門實際會跑的 SQL（pgLiveRepo.UserFlags → Repository.UserFlags）
		email, code, isSuper, isVIP, err := pg.UserFlags(ctx, B.ID)
		if err != nil || email != B.Email || isSuper || isVIP {
			t.Errorf("UserFlags(B): email_ok=%v super=%v vip=%v err=%v (code_len=%d)", email == B.Email, isSuper, isVIP, err, len(code))
		}
	})

	// -------------------------------------------------------------------------------
	t.Run("04_InsertConsentAudit", func(t *testing.T) {
		if got := f.auditRows(t, pub, A.ID); len(got) != 0 {
			t.Fatalf("precondition: %d audit rows already exist for the fixture", len(got))
		}
		if err := pg.InsertConsentAudit(ctx, A.ID, pub, 1, false, "198.51.100.9"); err != nil {
			t.Fatalf("InsertConsentAudit: %v", err)
		}
		rows := f.auditRows(t, pub, A.ID)
		if len(rows) != 1 {
			t.Fatalf("audit rows = %d, want exactly 1", len(rows))
		}
		r := rows[0]
		if r.UserID != A.ID || r.Action != "runmeet_live_consent" || r.Resource != "run_meet" || r.ResourceID != pub ||
			r.IP != "198.51.100.9" || !r.CreatedAtSet {
			t.Errorf("row mismatch: user_ok=%v action=%q resource=%q resid_ok=%v ip=%q created_at_set=%v",
				r.UserID == A.ID, r.Action, r.Resource, r.ResourceID == pub, r.IP, r.CreatedAtSet)
		}
		rmlItAssertConsentMeta(t, r.Meta, 1, false)
		t.Logf("row 1: action/resource/ip/created_at ok; meta keys exactly [consent_v presence_only]")

		// presence-only 版本＋超長 ip（audit_logs.ip 是 VARCHAR(45)，函式內截斷）
		long := "2001:db8:aaaa:bbbb:cccc:dddd:eeee:ffff-0123456789-0123456789-xx" // 63 字元
		if err := pg.InsertConsentAudit(ctx, A.ID, po, 3, true, long); err != nil {
			t.Fatalf("InsertConsentAudit(long ip): %v", err)
		}
		rows = f.auditRows(t, po, A.ID)
		if len(rows) != 1 || len(rows[0].IP) != 45 || rows[0].IP != long[:45] {
			t.Fatalf("long ip: rows=%d stored_len=%d", len(rows), func() int {
				if len(rows) == 0 {
					return -1
				}
				return len(rows[0].IP)
			}())
		}
		rmlItAssertConsentMeta(t, rows[0].Meta, 3, true)
		t.Logf("row 2: 63-char ip stored truncated to 45; meta {consent_v:3, presence_only:true} exact keys")

		// 失敗路徑：非 UUID 的 uid → 回 error（handler 會轉 500，不得 panic／靜默成功）
		if err := pg.InsertConsentAudit(ctx, "not-a-uuid", pub, 1, false, "198.51.100.9"); err == nil {
			t.Errorf("invalid uuid must return an error")
		}
	})

	// -------------------------------------------------------------------------------
	// 去重視窗（2026-10-02 reviewer finding）：InsertConsentAudit 是單一 INSERT … SELECT … WHERE NOT EXISTS，
	// 同 user_id + action + resource_id 在 15 分鐘內只留一列。client 依契約會合法地反覆送 reauth:false 的 start
	// （頁面重新整理後自動接續、start 在證據寫入之後才失敗的重試），不去重稽核表會被灌爆。
	t.Run("04b_InsertConsentAudit_dedupe_window", func(t *testing.T) {
		D := f.user(t, "d", "RML IT Dedupe")
		m1 := f.meet(t, A, rmlItMeetOpt{title: "RML-IT dedupe 1"})
		m2 := f.meet(t, A, rmlItMeetOpt{title: "RML-IT dedupe 2"})
		insert := func(label, uid, meetID string) {
			t.Helper()
			if err := pg.InsertConsentAudit(ctx, uid, meetID, 1, false, "198.51.100.9"); err != nil {
				t.Fatalf("%s: InsertConsentAudit: %v", label, err)
			}
		}
		count := func(label, uid, meetID string, want int) []rmlItAudit {
			t.Helper()
			rows := f.auditRows(t, meetID, uid)
			if len(rows) != want {
				t.Fatalf("%s: %d audit rows, want %d", label, len(rows), want)
			}
			t.Logf("%s: %d row(s) as expected", label, len(rows))
			return rows
		}

		// 視窗內重複 → 仍只有一列，meta 鍵不變
		insert("first", D.ID, m1)
		insert("repeat #1", D.ID, m1)
		insert("repeat #2", D.ID, m1)
		rows := count("3 identical inserts inside the window", D.ID, m1, 1)
		rmlItAssertConsentMeta(t, rows[0].Meta, 1, false)

		// 不同團練、不同使用者各自獨立
		insert("other meet", D.ID, m2)
		count("same user, other meet", D.ID, m2, 1)
		insert("other user, same meet", A.ID, m1)
		count("other user, same meet", A.ID, m1, 1)
		count("the first user's rows are untouched", D.ID, m1, 1)

		// 視窗邊界：14 分鐘前的列仍擋住；16 分鐘前的列不再擋住（新寫一列，之後新列又擋住）
		age := func(id int64, interval string) {
			t.Helper()
			if _, err := pool.Exec(ctx, `UPDATE audit_logs SET created_at = NOW() - $2::interval WHERE id=$1`, id, interval); err != nil {
				t.Fatalf("age audit row: %v", err)
			}
		}
		age(rows[0].ID, "14 minutes")
		insert("existing row is 14 minutes old", D.ID, m1)
		count("a 14-minute-old row still suppresses", D.ID, m1, 1)
		age(rows[0].ID, "16 minutes")
		insert("existing row is 16 minutes old", D.ID, m1)
		count("a 16-minute-old row no longer suppresses", D.ID, m1, 2)
		insert("repeat after the new row", D.ID, m1)
		count("the new row suppresses again", D.ID, m1, 2)
	})

	// -------------------------------------------------------------------------------
	t.Run("05_loadLiveSettings", func(t *testing.T) {
		defaults := live.Settings{EntryState: "whitelist", Whitelist: "", Max: 50, PreMinutes: 30, DefaultHours: 3, GraceMinutes: 30}
		t.Cleanup(func() { f.clearSettings(t, rmlItSettingKeys...) })
		f.clearSettings(t, rmlItSettingKeys...)

		if got := loadLiveSettings(ctx, pool); got != defaults {
			t.Fatalf("no rows: got %+v, want %+v", got, defaults)
		}
		t.Logf("no rows → defaults {whitelist, \"\", 50, 30, 3, 30} OK")

		f.setSetting(t, live.MaxKey, "7")
		want := defaults
		want.Max = 7
		if got := loadLiveSettings(ctx, pool); got != want {
			t.Fatalf("runmeet_live_max=7: got %+v, want %+v", got, want)
		}
		t.Logf("runmeet_live_max=7 → Max=7 (other fields still defaults) OK")

		// 全部六個鍵一起設
		f.setSetting(t, live.EntryStateKey, "open")
		f.setSetting(t, live.EntryWhitelistKey, "a@example.invalid\nb@example.invalid")
		f.setSetting(t, live.MaxKey, " 12 ") // 前後空白 → GetInt trim
		f.setSetting(t, live.PreMinutesKey, "15")
		f.setSetting(t, live.DefaultHoursKey, "2")
		f.setSetting(t, live.GraceMinutesKey, "10")
		wantAll := live.Settings{EntryState: "open", Whitelist: "a@example.invalid\nb@example.invalid", Max: 12, PreMinutes: 15, DefaultHours: 2, GraceMinutes: 10}
		if got := loadLiveSettings(ctx, pool); got != wantAll {
			t.Fatalf("all keys set: got %+v, want %+v", got, wantAll)
		}
		t.Logf("all six keys set → read back exactly (incl. trimmed ' 12 ')")

		// 壞值：解析失敗 → 預設；超出上限 → 夾到 200；負的 pre → 預設
		f.setSetting(t, live.MaxKey, "abc")
		if got := loadLiveSettings(ctx, pool); got.Max != live.DefMax {
			t.Errorf("max=abc → %d, want default %d", got.Max, live.DefMax)
		}
		f.setSetting(t, live.MaxKey, "9999")
		if got := loadLiveSettings(ctx, pool); got.Max != live.MaxLiveCeiling {
			t.Errorf("max=9999 → %d, want ceiling %d", got.Max, live.MaxLiveCeiling)
		}
		f.setSetting(t, live.PreMinutesKey, "-5")
		if got := loadLiveSettings(ctx, pool); got.PreMinutes != live.DefPreMinutes {
			t.Errorf("pre=-5 → %d, want default %d", got.PreMinutes, live.DefPreMinutes)
		}
		t.Logf("bad values (abc / 9999 / -5) normalise to default / 200 / default")

		// ResolveLiveEntry 走同一條讀取路徑
		f.clearSettings(t, rmlItSettingKeys...)
		if g := ResolveLiveEntry(ctx, pool, B.Email, "", false); g != "hidden" {
			t.Errorf("default state, ordinary user → %q, want hidden", g)
		}
		if g := ResolveLiveEntry(ctx, pool, B.Email, "", true); g != "shown" {
			t.Errorf("default state, super admin → %q, want shown", g)
		}
		f.setSetting(t, live.EntryStateKey, "open")
		if g := ResolveLiveEntry(ctx, pool, B.Email, "", false); g != "shown" {
			t.Errorf("open → %q, want shown", g)
		}
		f.setSetting(t, live.EntryStateKey, "hidden")
		if g := ResolveLiveEntry(ctx, pool, B.Email, "", true); g != "hidden" {
			t.Errorf("hidden + super admin → %q, want hidden", g)
		}
		f.setSetting(t, live.EntryStateKey, "whitelist")
		f.setSetting(t, live.EntryWhitelistKey, B.Email)
		if g := ResolveLiveEntry(ctx, pool, B.Email, "", false); g != "shown" {
			t.Errorf("whitelisted email → %q, want shown", g)
		}
		if g := ResolveLiveEntry(ctx, pool, C.Email, "", false); g != "hidden" {
			t.Errorf("non-whitelisted email → %q, want hidden", g)
		}
		t.Logf("ResolveLiveEntry default/open/hidden/whitelist matrix OK")

		f.clearSettings(t, rmlItSettingKeys...)
		if got := loadLiveSettings(ctx, pool); got != defaults {
			t.Errorf("after delete: got %+v, want defaults", got)
		}
	})

	// -------------------------------------------------------------------------------
	t.Run("06_LiveStartHandlerE2E", func(t *testing.T) {
		t.Cleanup(func() { f.clearSettings(t, rmlItSettingKeys...) })
		f.clearSettings(t, rmlItSettingKeys...)
		f.mr.FlushAll()

		// --- 入口閘門：預設狀態 whitelist，白名單只放 A、B、C 的 email（走真實 UserFlags SQL）---
		f.setSetting(t, live.EntryWhitelistKey, strings.Join([]string{A.Email, B.Email, C.Email}, "\n"))

		rec := f.postStart(B.ID, pub, rmlItBody(true, false))
		out := rmlItExpect(t, "B (joined, whitelisted, in window) → start", rec, http.StatusOK, "")
		if rec.Code == http.StatusOK {
			if out.PV != 1 || out.N != 1 || out.SID != rmlItSID || out.PresenceOnly || out.Meet.ID != pub ||
				out.Meet.Title != "RML-IT public" || out.MaxLive != live.DefMax {
				t.Errorf("200 body mismatch: pv=%d n=%d sid_ok=%v po=%v meet_ok=%v title_ok=%v max_live=%d",
					out.PV, out.N, out.SID == rmlItSID, out.PresenceOnly, out.Meet.ID == pub, out.Meet.Title == "RML-IT public", out.MaxLive)
			}
			if out.Meet.Lat == nil || out.Meet.Lng == nil || *out.Meet.Lat != lat || *out.Meet.Lng != lng {
				t.Errorf("public meet must carry the DB coordinates (lat_nil=%v lng_nil=%v)", out.Meet.Lat == nil, out.Meet.Lng == nil)
			}
			if len(out.Roster) != 1 || len(out.Roster[0]) != 2 || out.Roster[0][1] != B.Name {
				t.Errorf("roster must hold exactly B with the DB display name (roster len=%d)", len(out.Roster))
			}
			body := rec.Body.String()
			for label, secret := range map[string]string{"B.id": B.ID, "A.id": A.ID, "B.email": B.Email, "A.email": A.Email} {
				if strings.Contains(body, secret) {
					t.Errorf("response leaks %s", label)
				}
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
			}
			if f.mr.HGet("rml:{"+pub+"}:grants", B.ID) == "" {
				t.Errorf("no grant written in Redis for B")
			}
		}
		rows := f.auditRows(t, pub, B.ID)
		if len(rows) != 1 {
			t.Fatalf("B's first start must write exactly 1 consent audit row, got %d", len(rows))
		}
		rmlItAssertConsentMeta(t, rows[0].Meta, 1, false)
		if rows[0].IP != "198.51.100.9" || rows[0].Resource != "run_meet" || rows[0].Action != "runmeet_live_consent" {
			t.Errorf("audit row via handler: ip=%q resource=%q action=%q", rows[0].IP, rows[0].Resource, rows[0].Action)
		}
		t.Logf("B start → 1 audit row, meta exact keys, ip recorded")

		rec = f.postStart(C.ID, pub, rmlItBody(true, false))
		rmlItExpect(t, "C (pending) → start", rec, http.StatusForbidden, "not_member")
		if len(f.auditRows(t, pub, C.ID)) != 0 || f.mr.HGet("rml:{"+pub+"}:grants", C.ID) != "" {
			t.Errorf("C must leave no audit row and no grant")
		}

		rec = f.postStart(S.ID, pub, rmlItBody(true, false))
		rmlItExpect(t, "S (stranger, NOT whitelisted) → start", rec, http.StatusForbidden, "entry_closed")

		rec = f.postStart(A.ID, pub, rmlItBody(true, false))
		rmlItExpect(t, "A (owner, whitelisted) → start", rec, http.StatusOK, "")

		// --- 入口改 open：非白名單者進得了閘門，但不是成員 → not_member ---
		f.setSetting(t, live.EntryStateKey, "open")
		rec = f.postStart(S.ID, pub, rmlItBody(true, false))
		rmlItExpect(t, "S (stranger) with entry=open → start", rec, http.StatusForbidden, "not_member")

		// --- presence-only 團：無座標、meta.po=1、稽核 presence_only=true ---
		rec = f.postStart(B.ID, po, rmlItBody(true, false))
		out = rmlItExpect(t, "B → start on presence-only meet", rec, http.StatusOK, "")
		if rec.Code == http.StatusOK {
			if !out.PresenceOnly || out.Meet.Lat != nil || out.Meet.Lng != nil || strings.Contains(rec.Body.String(), `"lat"`) || strings.Contains(rec.Body.String(), `"lng"`) {
				t.Errorf("presence-only meet must not carry coordinates (po=%v)", out.PresenceOnly)
			}
			if f.mr.HGet("rml:{"+po+"}:meta", "po") != "1" {
				t.Errorf("meta.po must be 1 for a presence-only meet")
			}
		}
		if rows := f.auditRows(t, po, B.ID); len(rows) != 1 {
			t.Errorf("presence-only start: %d audit rows, want 1", len(rows))
		} else {
			rmlItAssertConsentMeta(t, rows[0].Meta, 1, true)
		}

		// --- reauth：200，且不再寫稽核列 ---
		rec = f.postStart(B.ID, pub, rmlItBody(false, true))
		rmlItExpect(t, "B reauth (no consent_v) → start", rec, http.StatusOK, "")
		if n := len(f.auditRows(t, pub, B.ID)); n != 1 {
			t.Errorf("reauth must not add audit rows: now %d, want 1", n)
		}

		// --- 顯示名稱：name 為空字串 → 退回 handle（真實 users 讀取 + SanitizeDisplayName）---
		f.member(t, pub, E, MemberJoined)
		rec = f.postStart(E.ID, pub, rmlItBody(true, false))
		out = rmlItExpect(t, "E (empty users.name) → start", rec, http.StatusOK, "")
		if rec.Code == http.StatusOK && f.mr.HGet("rml:{"+pub+"}:names", fmt.Sprint(out.N)) != E.Handle {
			t.Errorf("empty name must fall back to the handle in the roster name map")
		}

		// --- 404 / 時窗 / 410 ---
		rec = f.postStart(B.ID, uuid.NewString(), rmlItBody(true, false))
		rmlItExpect(t, "unknown meet → start", rec, http.StatusNotFound, "not_found")

		future := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Microsecond)
		fut := f.meet(t, A, rmlItMeetOpt{title: "RML-IT future", meetAt: future})
		f.member(t, fut, B, MemberJoined)
		rec = f.postStart(B.ID, fut, rmlItBody(true, false))
		out = rmlItExpect(t, "B on a meet 48h ahead → start", rec, http.StatusConflict, "outside_window")
		if d := out.OpensAt.Sub(future.Add(-30 * time.Minute)); d < -time.Millisecond || d > time.Millisecond {
			t.Errorf("opens_at off by %v from meet_at-30m (DB timestamptz round trip)", d)
		}
		if d := out.ClosesAt.Sub(future.Add(3*time.Hour + 30*time.Minute)); d < -time.Millisecond || d > time.Millisecond {
			t.Errorf("closes_at off by %v from meet_at+3h30m (ends_at NULL path)", d)
		}
		if len(f.auditRows(t, fut, B.ID)) != 0 || f.mr.Exists("rml:{"+fut+"}:grants") {
			t.Errorf("outside the window nothing may be written")
		}

		tmp := f.meet(t, A, rmlItMeetOpt{title: "RML-IT e2e tmp"})
		f.member(t, tmp, B, MemberJoined)
		f.setMeet(t, tmp, `status='cancelled'`)
		rec = f.postStart(B.ID, tmp, rmlItBody(true, false))
		rmlItExpect(t, "cancelled meet → start", rec, http.StatusGone, "meet_over")
		f.setMeet(t, tmp, `status='open', hidden_by_admin=TRUE`)
		rec = f.postStart(B.ID, tmp, rmlItBody(true, false))
		rmlItExpect(t, "admin-hidden meet → start", rec, http.StatusNotFound, "not_found")
		f.setMeet(t, tmp, `hidden_by_admin=FALSE, deleted_at=NOW()`)
		rec = f.postStart(B.ID, tmp, rmlItBody(true, false))
		rmlItExpect(t, "soft-deleted meet → start", rec, http.StatusNotFound, "not_found")
	})

	// -------------------------------------------------------------------------------
	// 頁面重新整理後自動接續：client 每次都帶 reauth:false + consent_v——視窗內只留一列證據，grant 照建。
	t.Run("06b_repeated_first_start_keeps_one_audit_row", func(t *testing.T) {
		t.Cleanup(func() { f.clearSettings(t, rmlItSettingKeys...) })
		f.clearSettings(t, rmlItSettingKeys...)
		f.setSetting(t, live.EntryStateKey, "open")
		R := f.user(t, "r", "RML IT Reloader")
		rm := f.meet(t, A, rmlItMeetOpt{title: "RML-IT reload"})
		f.member(t, rm, R, MemberJoined)
		for i := 1; i <= 3; i++ {
			rmlItExpect(t, fmt.Sprintf("R first-start #%d (page reload)", i), f.postStart(R.ID, rm, rmlItBody(true, false)), http.StatusOK, "")
		}
		rows := f.auditRows(t, rm, R.ID)
		if len(rows) != 1 {
			t.Fatalf("3 non-reauth starts inside the 15-minute window wrote %d audit rows, want exactly 1", len(rows))
		}
		rmlItAssertConsentMeta(t, rows[0].Meta, 1, false)
		if f.mr.HGet("rml:{"+rm+"}:grants", R.ID) == "" {
			t.Errorf("the grant must exist after the starts")
		}
		t.Logf("3 non-reauth starts → 1 audit row (dedupe window), grant present")
	})

	// -------------------------------------------------------------------------------
	t.Run("07_RealRepositoryHooks", func(t *testing.T) {
		own := f.user(t, "ho", "RML IT HookOwner")
		kk := f.user(t, "hk", "RML IT Kicked")
		ll := f.user(t, "hl", "RML IT Leaver")
		ww := f.user(t, "hw", "RML IT Withdrawer")
		rr := f.user(t, "hr", "RML IT Rejected")
		m := f.meet(t, own, rmlItMeetOpt{title: "RML-IT hooks"})
		f.member(t, m, kk, MemberJoined)
		f.member(t, m, ll, MemberJoined)
		f.member(t, m, ww, MemberPending)
		f.member(t, m, rr, MemberPending)
		if mc, pc := f.dbCounts(t, m); mc != 3 || pc != 2 {
			t.Fatalf("fixture counts = %d/%d, want 3/2", mc, pc)
		}
		rec := &rmlItRec{pool: pool}
		repo := NewRepository(pool)
		repo.SetLiveHooks(rec)

		// Kick：commit 之後恰好呼叫一次 Revoke，且當下另一條連線看到的已是 kicked
		if err := repo.Kick(ctx, own.ID, m, kk.ID); err != nil {
			t.Fatalf("Kick: %v", err)
		}
		if st := f.dbMemberStatus(t, m, kk.ID); st != MemberKicked {
			t.Errorf("after Kick status=%q", st)
		}
		if mc, _ := f.dbCounts(t, m); mc != 2 {
			t.Errorf("member_count after Kick = %d, want 2", mc)
		}
		rec.want(t, "Kick", "revoke|"+m+"|"+kk.ID+"|kicked")

		if err := repo.Kick(ctx, own.ID, m, kk.ID); !errors.Is(err, errNoSuchMember) {
			t.Errorf("second Kick err=%v, want errNoSuchMember", err)
		}
		if err := repo.Kick(ctx, ll.ID, m, kk.ID); !errors.Is(err, errNotOwner) {
			t.Errorf("non-owner Kick err=%v, want errNotOwner", err)
		}
		rec.want(t, "failed Kicks must not fire hooks", "revoke|"+m+"|"+kk.ID+"|kicked")

		// LeaveOrWithdraw：joined → left（掛鉤）；pending → withdrawn（不掛）；owner → 拒絕
		res, err := repo.LeaveOrWithdraw(ctx, ll.ID, m)
		if err != nil || res != "left" {
			t.Fatalf("Leave: %q %v", res, err)
		}
		if st := f.dbMemberStatus(t, m, ll.ID); st != MemberLeft {
			t.Errorf("after Leave status=%q", st)
		}
		res, err = repo.LeaveOrWithdraw(ctx, ww.ID, m)
		if err != nil || res != "withdrawn" {
			t.Fatalf("Withdraw: %q %v", res, err)
		}
		if st := f.dbMemberStatus(t, m, ww.ID); st != "<none>" {
			t.Errorf("after Withdraw status=%q, want row deleted", st)
		}
		if _, err = repo.LeaveOrWithdraw(ctx, own.ID, m); !errors.Is(err, errOwnerCantLeave) {
			t.Errorf("owner Leave err=%v, want errOwnerCantLeave", err)
		}
		if mc, pc := f.dbCounts(t, m); mc != 1 || pc != 1 {
			t.Errorf("counts after Leave+Withdraw = %d/%d, want 1/1", mc, pc)
		}
		rec.want(t, "Leave fires, Withdraw/owner-leave do not",
			"revoke|"+m+"|"+kk.ID+"|kicked", "revoke|"+m+"|"+ll.ID+"|left")

		// Reject：pending → rejected（契約點名的防禦性掛鉤）
		if err := repo.Reject(ctx, own.ID, m, rr.ID); err != nil {
			t.Fatalf("Reject: %v", err)
		}
		if err := repo.Reject(ctx, own.ID, m, rr.ID); !errors.Is(err, errApplicationDone) {
			t.Errorf("second Reject err=%v, want errApplicationDone", err)
		}
		if mc, pc := f.dbCounts(t, m); mc != 1 || pc != 0 {
			t.Errorf("counts after Reject = %d/%d, want 1/0", mc, pc)
		}
		rec.want(t, "Reject",
			"revoke|"+m+"|"+kk.ID+"|kicked", "revoke|"+m+"|"+ll.ID+"|left", "revoke|"+m+"|"+rr.ID+"|rejected")

		// SetStatus / SoftDelete：另一個團（有一個待審申請者，中止時會被一併婉拒）
		pp := f.user(t, "hp", "RML IT Pending")
		m2 := f.meet(t, own, rmlItMeetOpt{title: "RML-IT status hooks"})
		f.member(t, m2, pp, MemberPending)
		rec2 := &rmlItRec{pool: pool}
		repo2 := NewRepository(pool)
		repo2.SetLiveHooks(rec2)

		for _, to := range []string{StatusClosed, StatusOpen} {
			if _, err := repo2.SetStatus(ctx, own.ID, m2, to); err != nil {
				t.Fatalf("SetStatus(%s): %v", to, err)
			}
		}
		rec2.want(t, "open→closed→open fires nothing")

		rejected, err := repo2.SetStatus(ctx, own.ID, m2, StatusCancelled)
		if err != nil || len(rejected) != 1 || rejected[0] != pp.ID {
			t.Fatalf("SetStatus(cancelled): rejected=%v err=%v", len(rejected), err)
		}
		if st := f.dbMemberStatus(t, m2, pp.ID); st != MemberRejected {
			t.Errorf("pending applicant after cancel = %q, want rejected", st)
		}
		if _, pc := f.dbCounts(t, m2); pc != 0 {
			t.Errorf("pending_count after cancel = %d, want 0", pc)
		}
		rec2.want(t, "cancel → MarkDead after commit", "mark_dead|"+m2+"||cancelled|deleted=false|hidden=false")

		if _, err := repo2.SetStatus(ctx, own.ID, m2, StatusCancelled); !errors.Is(err, errBadTransition) {
			t.Errorf("cancelled→cancelled err=%v, want errBadTransition", err)
		}
		if _, err := repo2.SetStatus(ctx, own.ID, m2, StatusOpen); err != nil {
			t.Fatalf("SetStatus(cancelled→open): %v", err)
		}
		rec2.want(t, "cancelled→open → ClearDead after commit",
			"mark_dead|"+m2+"||cancelled|deleted=false|hidden=false", "clear_dead|"+m2+"||open|deleted=false|hidden=false")

		if err := repo2.SoftDelete(ctx, own.ID, m2); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		if err := repo2.SoftDelete(ctx, own.ID, m2); !errors.Is(err, errNotFound) {
			t.Errorf("second SoftDelete err=%v, want errNotFound", err)
		}
		rec2.want(t, "SoftDelete → MarkDead after commit (second one fires nothing)",
			"mark_dead|"+m2+"||cancelled|deleted=false|hidden=false", "clear_dead|"+m2+"||open|deleted=false|hidden=false",
			"mark_dead|"+m2+"||open|deleted=true|hidden=false")
	})

	// -------------------------------------------------------------------------------
	t.Run("08_FullChain_start_kick_start", func(t *testing.T) {
		t.Cleanup(func() { f.clearSettings(t, rmlItSettingKeys...) })
		f.clearSettings(t, rmlItSettingKeys...)
		f.mr.FlushAll()
		f.setSetting(t, live.EntryStateKey, "open")

		own := f.user(t, "co", "RML IT ChainOwner")
		bb := f.user(t, "cb", "RML IT ChainBravo")
		mc := f.meet(t, own, rmlItMeetOpt{title: "RML-IT chain"})
		f.member(t, mc, bb, MemberJoined)

		rmlItExpect(t, "bb start", f.postStart(bb.ID, mc, rmlItBody(true, false)), http.StatusOK, "")
		if f.mr.HGet("rml:{"+mc+"}:grants", bb.ID) == "" {
			t.Fatalf("grant missing after start")
		}
		// 真實 hook（NewHandler 接的 live.Store）：Kick commit 之後撤銷 grant 並寫墓碑
		if err := f.h.repo.Kick(ctx, own.ID, mc, bb.ID); err != nil {
			t.Fatalf("Kick via handler repo: %v", err)
		}
		if f.mr.HGet("rml:{"+mc+"}:grants", bb.ID) != "" {
			t.Errorf("grant must be gone after Kick (real Store.Revoke)")
		}
		if !f.mr.Exists("rml:{" + mc + "}:rv:" + bb.ID) {
			t.Errorf("revocation tombstone missing after Kick")
		}
		rmlItExpect(t, "kicked bb start again (DB truth)", f.postStart(bb.ID, mc, rmlItBody(true, false)), http.StatusForbidden, "not_member")
		if got := f.dbMemberStatus(t, mc, bb.ID); got != MemberKicked {
			t.Errorf("DB status = %q, want kicked", got)
		}

		// 軟刪 → 真實 MarkDead；之後發起人再 start：MeetForLive 的 deleted_at 過濾 → 404
		rmlItExpect(t, "owner start before delete", f.postStart(own.ID, mc, rmlItBody(true, false)), http.StatusOK, "")
		if err := f.h.repo.SoftDelete(ctx, own.ID, mc); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		if f.mr.HGet("rml:{"+mc+"}:meta", "dead") != "1" {
			t.Errorf("meta.dead must be 1 after SoftDelete (real Store.MarkDead)")
		}
		rmlItExpect(t, "owner start after soft delete", f.postStart(own.ID, mc, rmlItBody(true, false)), http.StatusNotFound, "not_found")
	})

	// -------------------------------------------------------------------------------
	t.Run("09_AdminRestore_AdminTakedown", func(t *testing.T) {
		f.mr.FlushAll()
		own := f.user(t, "ao", "RML IT AdminOwner")
		rt := chi.NewRouter()
		rt.Post("/{id}/takedown", f.h.AdminTakedown)
		rt.Post("/{id}/restore", f.h.AdminRestore)
		do := func(id, action, body string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, "/"+id+"/"+action, strings.NewReader(body))
			rec := httptest.NewRecorder()
			rt.ServeHTTP(rec, req)
			return rec
		}
		type state struct {
			hidden  bool
			reason  string
			status  string
			deleted bool
		}
		read := func(id string) state {
			var s state
			if err := pool.QueryRow(ctx, `SELECT hidden_by_admin, hidden_reason, status, deleted_at IS NOT NULL FROM run_meets WHERE id=$1::uuid`, id).
				Scan(&s.hidden, &s.reason, &s.status, &s.deleted); err != nil {
				t.Fatalf("read meet: %v", err)
			}
			return s
		}
		dead := func(id string) string { return f.mr.HGet("rml:{"+id+"}:meta", "dead") }

		// (a) 一般 open 團：takedown → hidden + dead；restore → 取消下架 + dead 清除
		ma := f.meet(t, own, rmlItMeetOpt{title: "RML-IT restore open"})
		if rec := do(ma, "takedown", `{"reason":"rml-it"}`); rec.Code != http.StatusOK {
			t.Fatalf("AdminTakedown: %d %s", rec.Code, rec.Body.String())
		}
		if s := read(ma); !s.hidden || s.reason != "rml-it" || dead(ma) != "1" {
			t.Errorf("after takedown: %+v dead=%q, want hidden+reason+dead=1", s, dead(ma))
		}
		if _, err := pg.MeetForLive(ctx, ma); !errors.Is(err, errNotFound) {
			t.Errorf("a taken-down meet must be invisible to /live/start (err=%v)", err)
		}
		if rec := do(ma, "restore", ``); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
			t.Fatalf("AdminRestore: %d %s", rec.Code, rec.Body.String())
		}
		if s := read(ma); s.hidden || s.reason != "" || s.status != StatusOpen || s.deleted || dead(ma) != "" {
			t.Errorf("after restore: %+v dead=%q, want visible, reason empty, dead cleared", s, dead(ma))
		}
		if _, err := pg.MeetForLive(ctx, ma); err != nil {
			t.Errorf("a restored meet must be visible again: %v", err)
		}
		t.Logf("(a) open meet: takedown → hidden+dead=1; restore → RETURNING status/deleted scanned, hidden cleared, dead cleared")

		// (b) cancelled 且被下架：restore 只取消下架，dead 必須保留
		mb := f.meet(t, own, rmlItMeetOpt{title: "RML-IT restore cancelled", status: StatusCancelled, hiddenAdmin: true})
		f.h.repo.liveMarkDead(ctx, mb)
		if dead(mb) != "1" {
			t.Fatalf("precondition: dead flag not set")
		}
		if rec := do(mb, "restore", ``); rec.Code != http.StatusOK {
			t.Fatalf("AdminRestore(cancelled): %d %s", rec.Code, rec.Body.String())
		}
		if s := read(mb); s.hidden || s.status != StatusCancelled || dead(mb) != "1" {
			t.Errorf("cancelled meet: %+v dead=%q, want hidden cleared but dead kept", s, dead(mb))
		}
		t.Logf("(b) cancelled meet: hidden cleared, dead flag preserved")

		// (c) 已刪除且被下架：restore 也不得清 dead
		mcDel := f.meet(t, own, rmlItMeetOpt{title: "RML-IT restore deleted", hiddenAdmin: true, deleted: true})
		f.h.repo.liveMarkDead(ctx, mcDel)
		if rec := do(mcDel, "restore", ``); rec.Code != http.StatusOK {
			t.Fatalf("AdminRestore(deleted): %d %s", rec.Code, rec.Body.String())
		}
		if s := read(mcDel); s.hidden || !s.deleted || dead(mcDel) != "1" {
			t.Errorf("deleted meet: %+v dead=%q, want hidden cleared, deleted kept, dead kept", s, dead(mcDel))
		}
		t.Logf("(c) soft-deleted meet: RETURNING (deleted_at IS NOT NULL)=true honoured, dead flag preserved")

		// (d) 不存在 → 404（ErrNoRows 分支）；非 UUID → 400
		if rec := do(uuid.NewString(), "restore", ``); rec.Code != http.StatusNotFound {
			t.Errorf("unknown id: %d %s, want 404", rec.Code, rec.Body.String())
		}
		if rec := do("not-a-uuid", "restore", ``); rec.Code != http.StatusBadRequest {
			t.Errorf("bad id: %d, want 400", rec.Code)
		}
		t.Logf("(d) unknown id → 404, malformed id → 400")
	})

	// -------------------------------------------------------------------------------
	t.Run("10_RouterWiring_requireEntry_and_rate_limit", func(t *testing.T) {
		t.Cleanup(func() { f.clearSettings(t, rmlItSettingKeys...) })
		f.clearSettings(t, rmlItSettingKeys...)
		f.mr.FlushAll()
		own := f.user(t, "ro", "RML IT RouterOwner")
		bb := f.user(t, "rb", "RML IT RouterBravo")
		meetR := f.meet(t, own, rmlItMeetOpt{title: "RML-IT router"})
		f.member(t, meetR, bb, MemberJoined)

		root := chi.NewRouter()
		root.Use(rmlItUserCtx(bb.ID))
		root.Mount("/run-meets", f.h.Router())
		post := func(body string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, "/run-meets/"+meetR+"/live/start", strings.NewReader(body))
			req.RemoteAddr = "198.51.100.9:5555"
			rec := httptest.NewRecorder()
			root.ServeHTTP(rec, req)
			return rec
		}

		// runmeet_entry_state 預設 hidden（無列）→ requireEntry 擋在最前面（在限流之前，所以不計入 6 次/分）
		if rec := post(rmlItBody(true, false)); rec.Code != http.StatusForbidden {
			t.Errorf("requireEntry closed: %d %s, want 403", rec.Code, rec.Body.String())
		}
		f.setSetting(t, EntryStateKey, "open")
		// 限流計數從這裡開始：#1 runmeet 入口開了，但 live 入口預設 whitelist（B 不在白名單）→ 403 entry_closed
		rmlItExpect(t, "limit#1 runmeet=open, live=default(whitelist)", post(rmlItBody(true, false)), http.StatusForbidden, "entry_closed")
		f.setSetting(t, live.EntryStateKey, "open")
		rmlItExpect(t, "limit#2 (consent)", post(rmlItBody(true, false)), http.StatusOK, "")
		for i := 3; i <= 6; i++ {
			rmlItExpect(t, fmt.Sprintf("limit#%d (reauth)", i), post(rmlItBody(false, true)), http.StatusOK, "")
		}
		rec := post(rmlItBody(false, true))
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("limit#7 in a minute: %d, want 429 (runmeet_live_start 6/min/user)", rec.Code)
		} else {
			t.Logf("limit#7 → HTTP 429 (rate limit wired on /live/start)")
		}
		if n := len(f.auditRows(t, meetR, bb.ID)); n != 1 {
			t.Errorf("audit rows for the router run = %d, want exactly 1", n)
		}
	})
}
