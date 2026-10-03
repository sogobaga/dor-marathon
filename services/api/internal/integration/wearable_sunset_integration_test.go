//go:build integration

// Terra／Strava 串接結束公告（announce）的資料庫層測試：證明「announce 期間不會新增任何連線列、既有列照常運作」。
// 需要真實 Postgres（已套用全部 migrations），獨立 build tag，預設 `go test ./...` 不會編譯：
//
//	DOR_TEST_DATABASE_URL=postgres://…?sslmode=disable go test -tags=integration ./internal/integration/ -run SunsetIT
//
// 這些測試會寫入並刪除全域的 wearable_sunset_* 兩個 app_settings 列，同樣的列 appsettings 與 integration/wearablesunset
// 套件的整合測試也會寫。連線一律經 internal/testdb.OpenSunsetSettings：只接受連到本機的資料庫（否則直接失敗）、
// 用 Postgres advisory lock 讓三個套件的測試一次只跑一個（所以不必加 -p 1，三個套件一起跑也不會互相踩）、
// 結束時把兩個列還原成測試前的樣子。
//
// 全部使用合成資料；Terra／Strava 一律用 in-process RoundTripper（不開本機 TCP、不連任何外部服務）。
// handler 一律以 NewTerraHandler／NewStravaHandler(repo…) 建出而不注入 Source——狀態從 app_settings 讀，
// 同時驗證「建構子預設接線」在真資料庫上確實生效。
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/testdb"
)

func sunsetITPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.OpenSunsetSettings(t, appsettings.WearableSunsetStateKey, appsettings.WearableSunsetDateKey)
}

// sunsetITSet 寫入（或刪除，空字串＝缺鍵）兩個公告設定並清掉設定快取；測試結束時刪除並再清一次，不污染其他測試。
func sunsetITSet(t *testing.T, pool *pgxpool.Pool, state, date string) {
	t.Helper()
	ctx := context.Background()
	apply := func(k, v string) {
		var err error
		if v == "" {
			_, err = pool.Exec(ctx, `DELETE FROM app_settings WHERE key=$1`, k)
		} else {
			_, err = pool.Exec(ctx, `INSERT INTO app_settings (key, value, updated_at) VALUES ($1,$2,NOW())
				ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`, k, v)
		}
		if err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
	apply(appsettings.WearableSunsetStateKey, state)
	apply(appsettings.WearableSunsetDateKey, date)
	appsettings.InvalidateCache()
	t.Cleanup(func() {
		apply(appsettings.WearableSunsetStateKey, "")
		apply(appsettings.WearableSunsetDateKey, "")
		appsettings.InvalidateCache()
	})
}

func sunsetITUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	u := strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, handle, name, password_hash) VALUES ($1,$2,'SunsetIT','x') RETURNING id::text`,
		"sunsetit-"+u+"@example.invalid", "sunsetit_"+u).Scan(&id); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM activities WHERE user_id=$1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM user_integrations WHERE user_id=$1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
	})
	return id
}

// sunsetITEventually 輪詢 cond 直到成立或逾時（取代固定 sleep：條件成立就立刻往下走，不成立才會等到期限並失敗）。
func sunsetITEventually(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for: %s", within, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func sunsetITRows(t *testing.T, pool *pgxpool.Pool, uid string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM user_integrations WHERE user_id=$1`, uid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sunsetITActs(t *testing.T, pool *pgxpool.Pool, uid string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM activities WHERE user_id=$1`, uid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// sunsetITConn 讀回某 (user,provider) 的連線列欄位。
type sunsetITConn struct {
	ProviderUserID, Via string
	CreatedAt           time.Time
	Found               bool
}

func sunsetITGet(t *testing.T, pool *pgxpool.Pool, uid, provider string) sunsetITConn {
	t.Helper()
	var c sunsetITConn
	err := pool.QueryRow(context.Background(),
		`SELECT provider_user_id, COALESCE(via,'direct'), created_at FROM user_integrations WHERE user_id=$1 AND provider=$2`,
		uid, provider).Scan(&c.ProviderUserID, &c.Via, &c.CreatedAt)
	if err == nil {
		c.Found = true
	}
	return c
}

func sunsetITTerra(repo *Repository, out *outbound) *TerraHandler {
	h := NewTerraHandler(repo, TerraConfig{DevID: "d", APIKey: "k", SigningSecret: "s", FrontendURL: sunsetFront, Providers: []string{"POLAR"}}, nil)
	if out != nil {
		h.hc = out.client()
	}
	return h
}

func sunsetITSeedTerraRow(t *testing.T, pool *pgxpool.Pool, repo *Repository, uid, provider, terraID string) {
	t.Helper()
	ctx := context.Background()
	if err := repo.SaveTerra(ctx, &Connection{UserID: uid, Provider: provider, ProviderUserID: terraID, ExpiresAt: time.Now().AddDate(100, 0, 0)}); err != nil {
		t.Fatal(err)
	}
	// 連接起算點（created_at）往前挪：活動只收「連接之後」開始的，測試活動用兩天前
	if _, err := pool.Exec(ctx, `UPDATE user_integrations SET created_at = NOW() - interval '30 days' WHERE user_id=$1 AND provider=$2`, uid, provider); err != nil {
		t.Fatal(err)
	}
}

func terraAuthEv(terraID, provider, ref string) []byte {
	return []byte(fmt.Sprintf(`{"type":"auth","status":"success","user":{"user_id":"%s","provider":"%s","reference_id":"%s"}}`, terraID, provider, ref))
}

func terraReauthEv(oldID, newID, provider, ref string) []byte {
	return []byte(fmt.Sprintf(`{"type":"user_reauth","old_user":{"user_id":"%s","provider":"%s"},"new_user":{"user_id":"%s","provider":"%s","reference_id":"%s"}}`, oldID, provider, newID, provider, ref))
}

func terraActivityEv(terraID, provider, ref, summaryID string) []byte {
	start := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	return []byte(fmt.Sprintf(`{"type":"activity","user":{"user_id":"%s","provider":"%s","reference_id":"%s"},
		"data":[{"metadata":{"start_time":"%s","summary_id":"%s","type":8},"distance_data":{"summary":{"distance_meters":5000}},"active_durations_data":{"activity_seconds":1800}}]}`,
		terraID, provider, ref, start, summaryID))
}

// ---------- Terra：announce ----------

func TestSunsetIT_TerraAnnounce_WebhooksNeverCreateRows(t *testing.T) {
	pool := sunsetITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	sunsetITSet(t, pool, "announce", "2026-10-31")
	h := sunsetITTerra(repo, nil)

	uid := sunsetITUser(t, pool)
	tid := uuid.NewString()

	// auth 事件（新連接）：不建列
	h.handleAuthEvent(ctx, terraAuthEv(tid, "POLAR", uid))
	if n := sunsetITRows(t, pool, uid); n != 0 {
		t.Fatalf("announce: auth event created %d row(s)", n)
	}
	// activity 保底建列：不建列、活動也不匯入
	h.handleActivityEvent(ctx, terraActivityEv(tid, "POLAR", uid, "sunset-it-"+uuid.NewString()))
	if n := sunsetITRows(t, pool, uid); n != 0 {
		t.Fatalf("announce: activity fallback created %d row(s)", n)
	}
	if n := sunsetITActs(t, pool, uid); n != 0 {
		t.Fatalf("announce: activities of an unconnected user must not be imported, got %d", n)
	}
	// user_reauth 對不到列：什麼都不發生
	h.handleReauthEvent(ctx, terraReauthEv(uuid.NewString(), uuid.NewString(), "POLAR", uid))
	if n := sunsetITRows(t, pool, uid); n != 0 {
		t.Fatalf("announce: reauth for an unknown connection created %d row(s)", n)
	}
	// 透過 webhook 入口（簽章正確）送進來：一定先 ack 200（Terra 看狀態碼決定要不要重送，announce 也不能回非 200）。
	// 背景處理是 goroutine、沒有可以 join 的訊號，所以「背景處理後仍然沒有列」這個否定斷言不能靠固定 sleep（goroutine 慢一點
	// 就會空過）：
	//   ① 同一份 body 再走同步的 processWebhook（WebhookEvent 背景 goroutine 呼叫的就是它，事件類型分派與處理邏輯完全相同）
	//      後才斷言——確定性的，不會因為時機而空過；
	//   ② 另用同一個入口送「既有連線的 user_reauth」當正向對照，輪詢（有期限）到列真的被改掉，證明簽章、ack、背景處理
	//      在 announce 期間確實有在運作（否則上面的「沒有列」可能只是因為事件根本沒被處理）。
	sendSigned := func(body []byte) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
		req.Header.Set("terra-signature", signTerraBody(t, "s", time.Now().Unix(), body))
		h.WebhookEvent(rec, req)
		return rec.Code
	}
	body := terraAuthEv(tid, "POLAR", uid)
	if code := sendSigned(body); code != http.StatusOK {
		t.Fatalf("webhook must still ack 200 (Terra retries otherwise): %d", code)
	}
	h.processWebhook(body)
	if n := sunsetITRows(t, pool, uid); n != 0 {
		t.Fatalf("announce: webhook-delivered auth event created %d row(s)", n)
	}

	uid2 := sunsetITUser(t, pool)
	oldTID, newTID := uuid.NewString(), uuid.NewString()
	sunsetITSeedTerraRow(t, pool, repo, uid2, "polar", oldTID)
	if code := sendSigned(terraReauthEv(oldTID, newTID, "POLAR", uid2)); code != http.StatusOK {
		t.Fatalf("webhook (existing connection's user_reauth) must ack 200: %d", code)
	}
	sunsetITEventually(t, 5*time.Second, "the signed user_reauth delivered through the webhook entry is applied in announce", func() bool {
		return sunsetITGet(t, pool, uid2, "polar").ProviderUserID == newTID
	})
	if n := sunsetITRows(t, pool, uid); n != 0 {
		t.Fatalf("announce: still no row for the unconnected user after the control event was processed, got %d", n)
	}
}

func TestSunsetIT_TerraAnnounce_ExistingRowKeepsWorking(t *testing.T) {
	pool := sunsetITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	sunsetITSet(t, pool, "announce", "2026-10-31")
	h := sunsetITTerra(repo, nil)

	uid := sunsetITUser(t, pool)
	oldTID := uuid.NewString()
	sunsetITSeedTerraRow(t, pool, repo, uid, "polar", oldTID)
	before := sunsetITGet(t, pool, uid, "polar")

	// 既有列＋活動事件：照常匯入
	h.handleActivityEvent(ctx, terraActivityEv(oldTID, "POLAR", uid, "sunset-it-"+uuid.NewString()))
	if n := sunsetITActs(t, pool, uid); n != 1 {
		t.Fatalf("announce: an existing connection must keep importing activities, got %d", n)
	}

	// user_reauth：Terra user id 換新，列照常更新（不會因為公告而斷線），created_at（匯入 floor）不變
	newTID := uuid.NewString()
	h.handleReauthEvent(ctx, terraReauthEv(oldTID, newTID, "POLAR", uid))
	c := sunsetITGet(t, pool, uid, "polar")
	if c.ProviderUserID != newTID || c.Via != "terra" || !c.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("announce: reauth must update the existing row in place: %+v (before %+v)", c, before)
	}
	if n := sunsetITRows(t, pool, uid); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}

	// 換新後的 id 送活動事件：同樣匯入（不是被當成新連接擋掉）
	h.handleActivityEvent(ctx, terraActivityEv(newTID, "POLAR", uid, "sunset-it-"+uuid.NewString()))
	if n := sunsetITActs(t, pool, uid); n != 2 {
		t.Fatalf("activities after reauth = %d, want 2", n)
	}

	// auth 事件（使用者在 Terra 又完成了一次連接／重新授權，帶著新的 Terra id）：announce 期間整個不落地——
	// 連既有列都不改寫（擁有者規定既有使用者也不能重新授權）；既有連線的 id 換新另由 user_reauth／activity 保底自癒。
	third := uuid.NewString()
	h.handleAuthEvent(ctx, terraAuthEv(third, "POLAR", uid))
	if c := sunsetITGet(t, pool, uid, "polar"); c.ProviderUserID != newTID || !c.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("announce: an auth event must not rewrite the existing row (provider_user_id still %s): %+v", newTID, c)
	}
	if n := sunsetITRows(t, pool, uid); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}

	// 保底自癒：活動事件帶著「對不到列的 Terra id」＋合法 reference_id，既有列的 id 被修正、活動照收
	fourth := uuid.NewString()
	h.handleActivityEvent(ctx, terraActivityEv(fourth, "POLAR", uid, "sunset-it-"+uuid.NewString()))
	if c := sunsetITGet(t, pool, uid, "polar"); c.ProviderUserID != fourth || !c.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("announce: fallback must heal the existing row: %+v", c)
	}
	if n := sunsetITActs(t, pool, uid); n != 3 {
		t.Fatalf("activities after healing fallback = %d, want 3", n)
	}
	if n := sunsetITRows(t, pool, uid); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

// 直連列（via='direct'）在 announce 期間同樣完全不受 Terra 事件影響。
func TestSunsetIT_TerraAnnounce_DirectRowUntouched(t *testing.T) {
	pool := sunsetITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	sunsetITSet(t, pool, "announce", "2026-10-31")
	h := sunsetITTerra(repo, nil)

	uid := sunsetITUser(t, pool)
	if err := repo.Save(ctx, &Connection{UserID: uid, Provider: "coros", ProviderUserID: "direct-" + uuid.NewString(), AccessToken: "a", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	before := sunsetITGet(t, pool, uid, "coros")
	tid := uuid.NewString()
	h.handleAuthEvent(ctx, terraAuthEv(tid, "COROS", uid))
	h.handleActivityEvent(ctx, terraActivityEv(tid, "COROS", uid, "sunset-it-"+uuid.NewString()))
	h.handleReauthEvent(ctx, terraReauthEv(before.ProviderUserID, tid, "COROS", uid))
	if c := sunsetITGet(t, pool, uid, "coros"); c != before {
		t.Fatalf("direct row changed: %+v → %+v", before, c)
	}
	if n := sunsetITActs(t, pool, uid); n != 0 {
		t.Fatalf("Terra activity must not be imported onto a direct connection, got %d", n)
	}
}

func TestSunsetIT_TerraAnnounce_CallbackRedirectsAndCreatesNothing(t *testing.T) {
	pool := sunsetITPool(t)
	repo := NewRepository(pool)
	sunsetITSet(t, pool, "announce", "2026-10-31")
	out := &outbound{} // 任何對外呼叫都會回錯並被記下
	h := sunsetITTerra(repo, out)

	uid := sunsetITUser(t, pool)
	tid := uuid.NewString()
	rec := httptest.NewRecorder()
	h.Callback(rec, httptest.NewRequest(http.MethodGet, "/callback?user_id="+tid+"&reference_id="+uid+"&resource=POLAR", nil))
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || loc != sunsetFront+"?terra=sunset" {
		t.Fatalf("callback: %d %q", rec.Code, loc)
	}
	if out.count() != 0 {
		t.Fatalf("callback must not call Terra in announce: %v", out.urls)
	}
	if n := sunsetITRows(t, pool, uid); n != 0 {
		t.Fatalf("callback created %d row(s)", n)
	}
}

// 設定剛好在「callback 開頭檢查」與「落地」之間切到 announce：落地走更新專用 SQL，不會建列。
func TestSunsetIT_PersistTerraConnIsUpdateOnlyInAnnounce(t *testing.T) {
	pool := sunsetITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	sunsetITSet(t, pool, "announce", "2026-10-31")
	h := sunsetITTerra(repo, nil)
	uid := sunsetITUser(t, pool)

	saved, err := h.persistTerraConn(ctx, &Connection{UserID: uid, Provider: "polar", ProviderUserID: uuid.NewString(), ExpiresAt: time.Now().AddDate(100, 0, 0)})
	if err != nil || saved {
		t.Fatalf("announce + no row: saved=%v err=%v", saved, err)
	}
	if n := sunsetITRows(t, pool, uid); n != 0 {
		t.Fatalf("announce persist created %d row(s)", n)
	}
}

// ---------- Terra：off（與改版前相同）----------

func TestSunsetIT_TerraOff_StillCreatesRows(t *testing.T) {
	pool := sunsetITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	sunsetITSet(t, pool, "", "") // 缺鍵＝off
	h := sunsetITTerra(repo, nil)

	// auth 事件建列
	uid := sunsetITUser(t, pool)
	tid := uuid.NewString()
	h.handleAuthEvent(ctx, terraAuthEv(tid, "POLAR", uid))
	if c := sunsetITGet(t, pool, uid, "polar"); !c.Found || c.ProviderUserID != tid || c.Via != "terra" {
		t.Fatalf("off: auth event must create the terra row: %+v", c)
	}
	// activity 保底建列＋匯入（連接起算點是現在，所以活動必須晚於它：這裡只驗證建列，不驗證匯入）
	uid2 := sunsetITUser(t, pool)
	tid2 := uuid.NewString()
	h.handleActivityEvent(ctx, terraActivityEv(tid2, "POLAR", uid2, "sunset-it-"+uuid.NewString()))
	if c := sunsetITGet(t, pool, uid2, "polar"); !c.Found || c.ProviderUserID != tid2 || c.Via != "terra" {
		t.Fatalf("off: activity fallback must create the terra row: %+v", c)
	}
	// 設定值為 off／未知值：同樣不擋
	sunsetITSet(t, pool, "off", "2026-10-31")
	uid3 := sunsetITUser(t, pool)
	h.handleAuthEvent(ctx, terraAuthEv(uuid.NewString(), "POLAR", uid3))
	if n := sunsetITRows(t, pool, uid3); n != 1 {
		t.Fatalf("state=off must not block, rows=%d", n)
	}
}

// ---------- Terra／Strava：/status 帶出 sunset ----------

func sunsetITUserCtx(r *http.Request, uid string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), auth.CtxKeyUserID, uid))
}

func decodeStatus(t *testing.T, rec *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSunsetIT_StatusEndpointsExposeSunset(t *testing.T) {
	pool := sunsetITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	uid := sunsetITUser(t, pool)
	failing := &outbound{respond: func(r *http.Request) (*http.Response, error) { return jsonResp(500, `{}`), nil }}
	terra := sunsetITTerra(repo, failing)
	strava := NewStravaHandler(repo, StravaConfig{ClientID: "c", ClientSecret: "s", FrontendURL: sunsetFront, JWTSecret: "x"}, nil, nil)

	for _, tc := range []struct{ state, date, want string }{
		{"announce", "2026-10-31", `{"state":"announce","date":"2026-10-31"}`},
		{"announce", "2026-11-15", `{"state":"announce","date":"2026-11-15"}`},
		{"announce", "", `{"state":"announce","date":"2026-10-31"}`}, // 缺日期＝預設結束日
		{"", "", `{"state":"off"}`},
		{"off", "2026-10-31", `{"state":"off"}`},
	} {
		sunsetITSet(t, pool, tc.state, tc.date)
		// Terra（沒有連線列）
		rec := httptest.NewRecorder()
		terra.Status(rec, sunsetITUserCtx(httptest.NewRequest(http.MethodGet, "/status", nil), uid))
		if got := string(decodeStatus(t, rec)["sunset"]); got != tc.want {
			t.Errorf("terra status sunset (%s/%s) = %s, want %s", tc.state, tc.date, got, tc.want)
		}
		// Strava（未連接）
		rec = httptest.NewRecorder()
		strava.Status(rec, sunsetITUserCtx(httptest.NewRequest(http.MethodGet, "/status", nil), uid))
		if got := string(decodeStatus(t, rec)["sunset"]); got != tc.want {
			t.Errorf("strava status (not connected) sunset (%s/%s) = %s, want %s", tc.state, tc.date, got, tc.want)
		}
	}

	// Strava 已連接：兩種回應形狀都帶 sunset，其餘欄位不變
	sunsetITSet(t, pool, "announce", "2026-10-31")
	if err := repo.Save(ctx, &Connection{UserID: uid, Provider: providerStrava, ProviderUserID: "424242", AccessToken: "a", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour), AthleteName: "Test Runner"}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	strava.Status(rec, sunsetITUserCtx(httptest.NewRequest(http.MethodGet, "/status", nil), uid))
	m := decodeStatus(t, rec)
	if string(m["sunset"]) != `{"state":"announce","date":"2026-10-31"}` || string(m["connected"]) != "true" || string(m["athlete_name"]) != `"Test Runner"` || string(m["enabled"]) != "true" {
		t.Errorf("strava connected status = %s", rec.Body.String())
	}
}

// ---------- Strava ----------

func stravaTokenJSON(athleteID int64) string {
	return fmt.Sprintf(`{"access_token":"acc-%d","refresh_token":"ref-%d","expires_at":%d,"athlete":{"id":%d,"firstname":"Sun","lastname":"Set"}}`,
		athleteID, athleteID, time.Now().Add(6*time.Hour).Unix(), athleteID)
}

func sunsetITStrava(repo *Repository, out *outbound) *StravaHandler {
	h := NewStravaHandler(repo, StravaConfig{ClientID: "cid", ClientSecret: "csecret", FrontendURL: sunsetFront, JWTSecret: "sunset-it-secret", RedirectURI: "https://api.test/cb"}, nil, nil)
	if out != nil {
		h.hc = out.client()
	}
	return h
}

func TestSunsetIT_StravaAnnounce_CallbackSavesNothing(t *testing.T) {
	pool := sunsetITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	sunsetITSet(t, pool, "announce", "2026-10-31")
	out := &outbound{} // 任何對外呼叫（換 token、回填）都會被記下
	h := sunsetITStrava(repo, out)
	ret := "https://app.test/profile"

	// 沒有連線的使用者：不建列、不換 token
	uid := sunsetITUser(t, pool)
	rec := httptest.NewRecorder()
	h.Callback(rec, httptest.NewRequest(http.MethodGet, "/callback?code=abc&state="+h.signState(uid, ret), nil))
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || loc != ret+"?strava=sunset" {
		t.Fatalf("callback: %d %q", rec.Code, loc)
	}
	if n := sunsetITRows(t, pool, uid); n != 0 {
		t.Fatalf("announce callback created %d row(s)", n)
	}

	// 已有連線的使用者「重新授權」：同樣整個擋下，既有列（athlete、token）完全不動
	uid2 := sunsetITUser(t, pool)
	if err := repo.Save(ctx, &Connection{UserID: uid2, Provider: providerStrava, ProviderUserID: "111", AccessToken: "old-a", RefreshToken: "old-r", ExpiresAt: time.Now().Add(time.Hour), AthleteName: "Old Name"}); err != nil {
		t.Fatal(err)
	}
	beforeRow := sunsetITGet(t, pool, uid2, providerStrava)
	rec = httptest.NewRecorder()
	h.Callback(rec, httptest.NewRequest(http.MethodGet, "/callback?code=abc&state="+h.signState(uid2, ret), nil))
	if loc := rec.Header().Get("Location"); loc != ret+"?strava=sunset" {
		t.Fatalf("re-authorization callback: %q", loc)
	}
	if after := sunsetITGet(t, pool, uid2, providerStrava); after != beforeRow {
		t.Fatalf("existing strava row changed: %+v → %+v", beforeRow, after)
	}
	if out.count() != 0 {
		t.Fatalf("announce must not talk to Strava from the callback: %v", out.urls)
	}
}

func TestSunsetIT_StravaOff_CallbackStillSaves(t *testing.T) {
	pool := sunsetITPool(t)
	repo := NewRepository(pool)
	sunsetITSet(t, pool, "", "")
	out := &outbound{respond: func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == stravaTokenURL {
			return jsonResp(200, stravaTokenJSON(777001)), nil
		}
		return jsonResp(200, `[]`), nil // 背景回填：沒有活動
	}}
	h := sunsetITStrava(repo, out)
	ret := "https://app.test/profile"
	uid := sunsetITUser(t, pool)
	rec := httptest.NewRecorder()
	h.Callback(rec, httptest.NewRequest(http.MethodGet, "/callback?code=abc&state="+h.signState(uid, ret), nil))
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || loc != ret+"?strava=connected" {
		t.Fatalf("off callback: %d %q", rec.Code, loc)
	}
	if c := sunsetITGet(t, pool, uid, providerStrava); !c.Found || c.ProviderUserID != "777001" || c.Via != "direct" {
		t.Fatalf("off: callback must save the strava connection: %+v", c)
	}
	// 背景回填 goroutine 沒有可以 join 的訊號：輪詢（有期限）到它真的打了活動列表那支請求，取代固定 sleep——
	// 條件成立就立刻往下走，不成立才會等到期限並失敗（機器慢時不會空過、快時不會白等；也順便證明 off 時連接成功後確實啟動了回填）。
	// 請求被記下之後，這個 goroutine 只剩解析空陣列與寫一行日誌，不會再碰資料庫，所以測試結束後的清理不會與它交錯。
	sunsetITEventually(t, 5*time.Second, "the background backfill fetched the activity list", func() bool {
		out.mu.Lock()
		defer out.mu.Unlock()
		for _, u := range out.urls {
			if strings.Contains(u, "/athlete/activities") {
				return true
			}
		}
		return false
	})
}

// 既有 Strava 連線在 announce 期間：手動同步、中斷都照常（只擋「連接」）。
func TestSunsetIT_StravaAnnounce_ExistingConnectionSyncsAndDisconnects(t *testing.T) {
	pool := sunsetITPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	sunsetITSet(t, pool, "announce", "2026-10-31")
	out := &outbound{respond: func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/athlete/activities") {
			return jsonResp(200, `[]`), nil
		}
		return jsonResp(200, `{}`), nil // deauthorize
	}}
	h := sunsetITStrava(repo, out)
	uid := sunsetITUser(t, pool)
	if err := repo.Save(ctx, &Connection{UserID: uid, Provider: providerStrava, ProviderUserID: "555", AccessToken: "a", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour), AthleteName: "Existing"}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.Sync(rec, sunsetITUserCtx(httptest.NewRequest(http.MethodPost, "/sync", nil), uid))
	if rec.Code != http.StatusOK {
		t.Fatalf("announce: sync of an existing connection must keep working: %d %s", rec.Code, rec.Body.String())
	}
	if out.count() != 1 || !strings.Contains(out.urls[0], "/athlete/activities") {
		t.Fatalf("sync should have fetched the activity list once: %v", out.urls)
	}

	rec = httptest.NewRecorder()
	h.Disconnect(rec, sunsetITUserCtx(httptest.NewRequest(http.MethodDelete, "/disconnect", nil), uid))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("announce: disconnect must keep working: %d %s", rec.Code, rec.Body.String())
	}
	if n := sunsetITRows(t, pool, uid); n != 0 {
		t.Fatalf("disconnect left %d row(s)", n)
	}
}

// Terra 的匯入與中斷在 announce 期間照常。
func TestSunsetIT_TerraAnnounce_ImportAndDisconnectStillWork(t *testing.T) {
	pool := sunsetITPool(t)
	repo := NewRepository(pool)
	sunsetITSet(t, pool, "announce", "2026-10-31")
	out := &outbound{respond: func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "/v2/activity"):
			return jsonResp(200, `{"data":[]}`), nil
		case strings.Contains(r.URL.Path, "/v2/auth/deauthenticateUser"):
			return jsonResp(200, `{}`), nil
		}
		return jsonResp(200, `{}`), nil
	}}
	h := sunsetITTerra(repo, out)
	uid := sunsetITUser(t, pool)
	sunsetITSeedTerraRow(t, pool, repo, uid, "polar", uuid.NewString())

	rec := httptest.NewRecorder()
	h.Import(rec, sunsetITUserCtx(httptest.NewRequest(http.MethodPost, "/import?provider=polar", nil), uid))
	if rec.Code != http.StatusOK {
		t.Fatalf("announce: import must keep working: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.Disconnect(rec, sunsetITUserCtx(httptest.NewRequest(http.MethodPost, "/disconnect?provider=polar", nil), uid))
	if rec.Code != http.StatusOK {
		t.Fatalf("announce: disconnect must keep working: %d %s", rec.Code, rec.Body.String())
	}
	if n := sunsetITRows(t, pool, uid); n != 0 {
		t.Fatalf("disconnect left %d row(s)", n)
	}
}
