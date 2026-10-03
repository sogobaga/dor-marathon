package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/auth"
)

const (
	tUserA = "aaaaaaaa-0000-4000-8000-00000000000a" // DOR 使用者 A
	tUserB = "bbbbbbbb-0000-4000-8000-00000000000b" // DOR 使用者 B
)

// connectEnv：連接流程測試環境（假 store＋假 Garmin＋可切換入口狀態）。
type connectEnv struct {
	*testGarmin
	api    *fakeGarmin
	mu     sync.Mutex
	entry  map[string]string // DOR user id → "shown"|"hidden"
	mails  []string
	mailer *fakeMailer
}

type fakeMailer struct {
	mu   sync.Mutex
	sent []string // "<userID>|<title>"
}

func (m *fakeMailer) InsertForUsers(ctx context.Context, userIDs []string, level, title, body, url string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range userIDs {
		m.sent = append(m.sent, u+"|"+title)
	}
	return len(userIDs), nil
}

func (m *fakeMailer) list() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.sent...)
}

func testAuthMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := r.Header.Get("X-Test-User")
		if uid == "" {
			respondErr(w, http.StatusUnauthorized, "login required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), auth.CtxKeyUserID, uid)))
	})
}

func newConnectEnv(t *testing.T, mutate func(*GarminConfig)) *connectEnv {
	t.Helper()
	return newConnectEnvRedis(t, mutate, nil)
}

// newConnectEnvRedis：與 newConnectEnv 相同，但 KV（PKCE verifier、計數器、節流旗標）走指定的 Redis（測試用 miniredis）。
func newConnectEnvRedis(t *testing.T, mutate func(*GarminConfig), rdb *redis.Client) *connectEnv {
	t.Helper()
	t.Setenv("STRAVA_TOKEN_KEY", garminTestKeyHex)
	api := newFakeGarmin()
	env := &connectEnv{api: api, entry: map[string]string{}, mailer: &fakeMailer{}}
	st := newFakeGarminStore()
	cfg := GarminConfig{ClientID: "client-test", ClientSecret: "secret-test", WebhookToken: testGarminToken}
	if mutate != nil {
		mutate(&cfg)
	}
	h := newGarminHandlerWithStore(cfg, GarminDeps{
		JWTSecret: "jwt-secret-test", FrontendURL: "https://app.test", RequireAuth: testAuthMW,
		HTTPClient: &http.Client{Transport: api, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		Mailer:     env.mailer, Redis: rdb,
	}, st)
	h.alertFn = func(kind, title, detail string) {
		env.mu.Lock()
		env.mails = append(env.mails, kind+"|"+title+"|"+detail)
		env.mu.Unlock()
	}
	h.entryFor = func(ctx context.Context, uid string) (string, error) {
		env.mu.Lock()
		defer env.mu.Unlock()
		if s, ok := env.entry[uid]; ok {
			return s, nil
		}
		return "shown", nil
	}
	h.emergency = func(ctx context.Context) bool { return false }
	env.testGarmin = &testGarmin{h: h, store: st, router: h.Router()}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		h.Drain(ctx)
	})
	return env
}

const garminTestKeyHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func (e *connectEnv) alertKinds() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, a := range e.mails {
		out = append(out, strings.SplitN(a, "|", 2)[0])
	}
	return out
}

func (e *connectEnv) setEntry(uid, state string) { e.mu.Lock(); e.entry[uid] = state; e.mu.Unlock() }

func (e *connectEnv) req(method, path, uid string, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	r := httptest.NewRequest(method, path, rd)
	if uid != "" {
		r.Header.Set("X-Test-User", uid)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, r)
	return rec
}

// connectResult：POST /connect 的解析結果。
type connectResult struct {
	URL       string
	Query     url.Values
	Cookie    *http.Cookie
	State     string
	Challenge string
}

func (e *connectEnv) connect(t *testing.T, uid, consentV string) connectResult {
	t.Helper()
	rec := e.req(http.MethodPost, "/connect", uid, `{"consent":true,"consent_v":"`+consentV+`"}`)
	if rec.Code != 200 {
		t.Fatalf("connect: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.URL == "" {
		t.Fatalf("connect body: %s", rec.Body.String())
	}
	u, err := url.Parse(out.URL)
	if err != nil {
		t.Fatal(err)
	}
	res := connectResult{URL: out.URL, Query: u.Query()}
	for _, c := range rec.Result().Cookies() {
		if c.Name == garminNonceCookie {
			res.Cookie = c
		}
	}
	res.State, res.Challenge = res.Query.Get("state"), res.Query.Get("code_challenge")
	return res
}

func (e *connectEnv) callback(t *testing.T, cr connectResult, code string, extra string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	q := "state=" + url.QueryEscape(cr.State)
	if code != "" {
		q += "&code=" + url.QueryEscape(code)
	}
	q += extra
	if cookie == nil {
		return e.req(http.MethodGet, "/callback?"+q, "", "")
	}
	return e.req(http.MethodGet, "/callback?"+q, "", "", cookie)
}

func redirectReason(rec *httptest.ResponseRecorder) (status, reason string) {
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		return "", ""
	}
	return u.Query().Get("garmin"), u.Query().Get("reason")
}

// --- /connect ---

func TestConnect_ValidationAndGates(t *testing.T) {
	e := newConnectEnv(t, nil)
	if rec := e.req(http.MethodPost, "/connect", "", `{"consent":true,"consent_v":"v1"}`); rec.Code != 401 {
		t.Fatalf("no login: %d", rec.Code)
	}
	for name, body := range map[string]string{
		"no body": "", "consent false": `{"consent":false,"consent_v":"v1"}`, "missing consent": `{"consent_v":"v1"}`, "garbage": `not-json`,
	} {
		rec := e.req(http.MethodPost, "/connect", tUserA, body)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "consent_required") {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	for name, v := range map[string]string{"unknown version": "v999", "too long": strings.Repeat("v", 30), "bad chars": "v1;drop", "empty": ""} {
		rec := e.req(http.MethodPost, "/connect", tUserA, `{"consent":true,"consent_v":"`+v+`"}`)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "invalid_consent_version") {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	// 入口 hidden → 403
	e.setEntry(tUserA, "hidden")
	if rec := e.req(http.MethodPost, "/connect", tUserA, `{"consent":true,"consent_v":"v1"}`); rec.Code != 403 {
		t.Fatalf("entry hidden: %d", rec.Code)
	}
	e.setEntry(tUserA, "shown")
	// 封鎖 → 403
	e.store.cs.blockedUsers[tUserA] = true
	if rec := e.req(http.MethodPost, "/connect", tUserA, `{"consent":true,"consent_v":"v1"}`); rec.Code != 403 {
		t.Fatalf("blocked: %d", rec.Code)
	}
	e.store.cs.blockedUsers[tUserA] = false
	// 缺金鑰 → 503 garmin_disabled
	t.Setenv("STRAVA_TOKEN_KEY", "")
	if rec := e.req(http.MethodPost, "/connect", tUserA, `{"consent":true,"consent_v":"v1"}`); rec.Code != 503 {
		t.Fatalf("no token key: %d", rec.Code)
	}
	t.Setenv("STRAVA_TOKEN_KEY", garminTestKeyHex)
	// 缺 client 設定 → 503
	for name, mut := range map[string]func(*GarminConfig){
		"no client id":     func(c *GarminConfig) { c.ClientID = "" },
		"no client secret": func(c *GarminConfig) { c.ClientSecret = "" },
		"no webhook token": func(c *GarminConfig) { c.WebhookToken = "" },
	} {
		e2 := newConnectEnv(t, mut)
		if rec := e2.req(http.MethodPost, "/connect", tUserA, `{"consent":true,"consent_v":"v1"}`); rec.Code != 503 || !strings.Contains(rec.Body.String(), "garmin_disabled") {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestConnect_BuildsPKCEURLCookieAndStoresVerifierInKV(t *testing.T) {
	e := newConnectEnv(t, nil)
	cr := e.connect(t, tUserA, "v1")
	q := cr.Query
	if !strings.HasPrefix(cr.URL, "https://connect.garmin.com/oauth2Confirm?") {
		t.Fatalf("auth url host/path: %s", cr.URL)
	}
	if q.Get("response_type") != "code" || q.Get("client_id") != "client-test" || q.Get("code_challenge_method") != "S256" ||
		q.Get("redirect_uri") != "https://www.dor.tw/api/v1/integrations/garmin/callback" || q.Get("state") == "" || q.Get("code_challenge") == "" {
		t.Fatalf("auth url params: %v", q)
	}
	// nonce cookie 屬性
	c := cr.Cookie
	if c == nil || c.Name != "dor_garmin_n" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode ||
		c.Path != "/api/v1/integrations/garmin/callback" || c.MaxAge != 600 || len(c.Value) < 16 {
		t.Fatalf("nonce cookie: %+v", c)
	}
	// state 內容：簽章可驗；不含 verifier
	st, ok := garminVerifyState("jwt-secret-test", cr.State, time.Now())
	if !ok || st.UserID != tUserA || st.ConsentV != "v1" || st.Nonce != c.Value || st.SID == "" {
		t.Fatalf("state: %+v ok=%v", st, ok)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(strings.SplitN(cr.State, ".", 2)[0])
	// verifier 在 KV（一次性），長度 43，challenge＝S256(verifier)；state 內看不到它
	v, found, _ := e.h.kv.Get(context.Background(), garminPKCEKeyPrefix+st.SID)
	if !found || len(v) != 43 || garminChallenge(v) != q.Get("code_challenge") {
		t.Fatalf("verifier in kv: found=%v len=%d", found, len(v))
	}
	if strings.Contains(string(raw), v) || strings.Contains(cr.URL, v) {
		t.Fatal("PKCE verifier must not appear in state or in the authorization url")
	}
	// 簽章用的是衍生金鑰，不是 JWT_SECRET 本身
	if _, ok := garminVerifyState("another-secret", cr.State, time.Now()); ok {
		t.Fatal("state must not verify under a different secret")
	}
	// 每次 connect 都是新的 verifier／nonce／sid
	cr2 := e.connect(t, tUserA, "v1")
	if cr2.Cookie.Value == c.Value || cr2.Challenge == cr.Challenge {
		t.Fatal("verifier/nonce must be fresh per connect")
	}
}

// --- /callback ---

func TestCallback_HappyPath_SavesConnectionAndRedirects(t *testing.T) {
	e := newConnectEnv(t, nil)
	cr := e.connect(t, tUserA, "v1")
	code := e.api.newCode(cr.Challenge)
	rec := e.callback(t, cr, code, "", cr.Cookie)
	if rec.Code != 302 {
		t.Fatalf("callback: %d", rec.Code)
	}
	if st, reason := redirectReason(rec); st != "connected" || reason != "" {
		t.Fatalf("redirect: %q %q (%s)", st, reason, rec.Header().Get("Location"))
	}
	if !strings.HasPrefix(rec.Header().Get("Location"), "https://app.test") {
		t.Fatalf("redirect base: %s", rec.Header().Get("Location"))
	}
	// 存下來的內容
	saves := e.store.cs.saves
	if len(saves) != 1 {
		t.Fatalf("saves=%d", len(saves))
	}
	s := saves[0]
	if s.UserID != tUserA || s.GarminUserID != e.api.userID || s.AccessToken != "acc-1" || s.RefreshToken != "ref-1" ||
		s.Scope != "ACTIVITY_EXPORT,HISTORICAL_DATA_EXPORT" || s.Issuer != garminAppGeneration("client-test") || s.ConsentVersion != "v1" {
		t.Fatalf("save input: %+v", s)
	}
	if d := time.Until(s.ExpiresAt); d < 23*time.Hour || d > 25*time.Hour {
		t.Fatalf("access expiry: %s", d)
	}
	if d := time.Until(s.RefreshExpiresAt); d < 89*24*time.Hour || d > 91*24*time.Hour {
		t.Fatalf("refresh expiry: %s", d)
	}
	if time.Since(s.ConsentAt) > time.Minute || time.Since(s.ConsentAt) < 0 {
		t.Fatalf("consent_at should be the connect time: %s", s.ConsentAt)
	}
	// nonce cookie 一律清除
	var cleared bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == garminNonceCookie && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("nonce cookie must be cleared")
	}
	// verifier 一次性：重放 callback → invalid_state，且不再打 token 端點
	tokenBefore, _, _, _ := e.api.calls()
	rec2 := e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)
	if st, reason := redirectReason(rec2); st != "error" || reason != "invalid_state" {
		t.Fatalf("replay: %q %q", st, reason)
	}
	if tokenAfter, _, _, _ := e.api.calls(); tokenAfter != tokenBefore {
		t.Fatal("replayed callback must not reach the token endpoint")
	}
	// 只呼叫了需要的端點：沒有 DELETE
	if _, _, _, del := e.api.calls(); del != 0 {
		t.Fatalf("successful connect must not delete registration (%d)", del)
	}
}

func TestCallback_StateAndNonceFailuresNeverExchangeToken(t *testing.T) {
	e := newConnectEnv(t, nil)
	cr := e.connect(t, tUserA, "v1")
	code := e.api.newCode(cr.Challenge)

	assertNoToken := func(name string) {
		t.Helper()
		if tk, _, _, _ := e.api.calls(); tk != 0 {
			t.Fatalf("%s: token endpoint was called", name)
		}
	}
	// 竄改 state
	bad := cr
	bad.State = cr.State[:len(cr.State)-3] + "xxx"
	if st, reason := redirectReason(e.callback(t, bad, code, "", cr.Cookie)); st != "error" || reason != "invalid_state" {
		t.Fatalf("tampered: %q %q", st, reason)
	}
	// 完全亂的 state
	bad.State = "garbage"
	if _, reason := redirectReason(e.callback(t, bad, code, "", cr.Cookie)); reason != "invalid_state" {
		t.Fatalf("garbage: %q", reason)
	}
	// 過期的 state（以過去時間簽出）
	expired := garminSignState("jwt-secret-test", garminState{UserID: tUserA, Nonce: cr.Cookie.Value, SID: "sid", ConsentV: "v1",
		IssuedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(-time.Minute)})
	if _, reason := redirectReason(e.callback(t, connectResult{State: expired}, code, "", cr.Cookie)); reason != "invalid_state" {
		t.Fatalf("expired: %q", reason)
	}
	assertNoToken("state failures")
	// 缺 nonce cookie／不符 → state_mismatch
	if _, reason := redirectReason(e.callback(t, cr, code, "", nil)); reason != "state_mismatch" {
		t.Fatalf("no cookie: %q", reason)
	}
	if _, reason := redirectReason(e.callback(t, cr, code, "", &http.Cookie{Name: garminNonceCookie, Value: "other-nonce-value"})); reason != "state_mismatch" {
		t.Fatalf("wrong cookie: %q", reason)
	}
	assertNoToken("nonce failures")
	// state 簽章正確但 verifier 已不在 KV（Redis 清空或過期）→ invalid_state
	st, _ := garminVerifyState("jwt-secret-test", cr.State, time.Now())
	e.h.kv.Del(context.Background(), garminPKCEKeyPrefix+st.SID)
	if _, reason := redirectReason(e.callback(t, cr, code, "", cr.Cookie)); reason != "invalid_state" {
		t.Fatalf("missing verifier: %q", reason)
	}
	assertNoToken("missing verifier")
}

func TestCallback_DeniedMissingCodeDisabledEntryClosed(t *testing.T) {
	e := newConnectEnv(t, nil)
	// 使用者拒絕
	cr := e.connect(t, tUserA, "v1")
	if _, reason := redirectReason(e.callback(t, cr, "", "&error=access_denied", cr.Cookie)); reason != "denied" {
		t.Fatalf("denied: %q", reason)
	}
	// 缺 code
	cr = e.connect(t, tUserA, "v1")
	if _, reason := redirectReason(e.callback(t, cr, "", "", cr.Cookie)); reason != "missing_code" {
		t.Fatalf("missing code: %q", reason)
	}
	// 入口在授權途中被關閉
	cr = e.connect(t, tUserA, "v1")
	e.setEntry(tUserA, "hidden")
	if _, reason := redirectReason(e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)); reason != "entry_closed" {
		t.Fatalf("entry closed: %q", reason)
	}
	e.setEntry(tUserA, "shown")
	// 缺金鑰
	cr = e.connect(t, tUserA, "v1")
	t.Setenv("STRAVA_TOKEN_KEY", "")
	if _, reason := redirectReason(e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)); reason != "server_config" {
		t.Fatalf("no token key: %q", reason)
	}
	t.Setenv("STRAVA_TOKEN_KEY", garminTestKeyHex)
	if tk, _, _, del := e.api.calls(); tk != 0 || del != 0 {
		t.Fatalf("none of these paths may call garmin: token=%d delete=%d", tk, del)
	}
	// 設定停用
	e2 := newConnectEnv(t, func(c *GarminConfig) { c.ClientSecret = "" })
	rec := e2.req(http.MethodGet, "/callback?state=x&code=y", "", "")
	if _, reason := redirectReason(rec); reason != "disabled" {
		t.Fatalf("disabled: %q", reason)
	}
}

func TestCallback_TokenAuthModes(t *testing.T) {
	// basic（預設）：Authorization 標頭；body 不帶 client 憑證
	e := newConnectEnv(t, nil)
	cr := e.connect(t, tUserA, "v1")
	e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)
	if len(e.api.tokenAuth) != 1 {
		t.Fatalf("token calls: %d", len(e.api.tokenAuth))
	}
	wantBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte("client-test:secret-test"))
	f := e.api.tokenForms[0]
	if e.api.tokenAuth[0] != wantBasic || f.Get("client_id") != "" || f.Get("client_secret") != "" {
		t.Fatalf("basic mode: auth=%q form=%v", e.api.tokenAuth[0], f)
	}
	if f.Get("grant_type") != "authorization_code" || f.Get("code_verifier") == "" || f.Get("redirect_uri") != "https://www.dor.tw/api/v1/integrations/garmin/callback" || f.Get("code") == "" {
		t.Fatalf("exchange form: %v", f)
	}

	// body 模式：form 帶 client_id／client_secret；沒有 Authorization
	e = newConnectEnv(t, func(c *GarminConfig) { c.TokenAuth = "body" })
	cr = e.connect(t, tUserA, "v1")
	e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)
	f = e.api.tokenForms[0]
	if e.api.tokenAuth[0] != "" || f.Get("client_id") != "client-test" || f.Get("client_secret") != "secret-test" {
		t.Fatalf("body mode: auth=%q form=%v", e.api.tokenAuth[0], f)
	}
}

func TestCallback_TokenExchangeAndAPIFailures(t *testing.T) {
	// 換 token 失敗：不落地、不撤銷（還沒有註冊／不知道 userId）
	e := newConnectEnv(t, nil)
	cr := e.connect(t, tUserA, "v1")
	if _, reason := redirectReason(e.callback(t, cr, "wrong-code", "", cr.Cookie)); reason != "token_exchange_failed" {
		t.Fatalf("bad code: %q", reason)
	}
	// Garmin 回 500
	e.api.tokenStatus = 500
	cr = e.connect(t, tUserA, "v1")
	if _, reason := redirectReason(e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)); reason != "token_exchange_failed" {
		t.Fatalf("500: %q", reason)
	}
	e.api.tokenStatus = 0
	// 換 token 回應沒有 refresh_token → 失敗（連線沒有 refresh 就活不過一天）
	e.api.noRefreshInResp = true
	cr = e.connect(t, tUserA, "v1")
	if _, reason := redirectReason(e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)); reason != "token_exchange_failed" {
		t.Fatalf("no refresh token: %q", reason)
	}
	e.api.noRefreshInResp = false
	if len(e.store.cs.saves) != 0 {
		t.Fatal("nothing may be saved on exchange failures")
	}
	// user/id 失敗：api_failed，且不撤銷（不知道這個註冊是否屬於別人）
	e.api.userIDStatus = 500
	cr = e.connect(t, tUserA, "v1")
	if _, reason := redirectReason(e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)); reason != "api_failed" {
		t.Fatalf("user id failure: %q", reason)
	}
	if _, _, _, del := e.api.calls(); del != 0 {
		t.Fatalf("user id failure must not delete the registration (%d)", del)
	}
	e.api.userIDStatus = 0
	// permissions 失敗：api_failed；userId 沒綁任何人 → 撤銷孤兒註冊
	e.api.permsStatus = 500
	cr = e.connect(t, tUserA, "v1")
	if _, reason := redirectReason(e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)); reason != "api_failed" {
		t.Fatalf("permissions failure: %q", reason)
	}
	if _, _, _, del := e.api.calls(); del != 1 {
		t.Fatalf("orphan registration (unbound user id) must be deleted best-effort: %d", del)
	}
}

func TestCallback_NoActivityPermission_OrphanRules(t *testing.T) {
	// 未綁定的 Garmin 帳號：撤銷孤兒註冊、不存連線
	e := newConnectEnv(t, nil)
	e.api.perms = []string{"HISTORICAL_DATA_EXPORT"}
	cr := e.connect(t, tUserA, "v1")
	if _, reason := redirectReason(e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)); reason != "no_activity_permission" {
		t.Fatalf("reason=%q", reason)
	}
	if _, _, _, del := e.api.calls(); del != 1 {
		t.Fatalf("unbound account: want 1 registration delete, got %d", del)
	}
	if len(e.store.cs.saves) != 0 || e.store.conn(tUserA) != nil {
		t.Fatal("connection must not be saved without ACTIVITY_EXPORT")
	}

	// 已綁定「同一個 DOR 使用者」（重新授權時縮減權限）：不撤銷（會毀掉既有連線的註冊），更新權限旗標
	e2 := newConnectEnv(t, nil)
	e2.store.putConn(garminConn{UserID: tUserA, ProviderUserID: e2.api.userID, Via: garminViaDirect, Scope: "ACTIVITY_EXPORT", Issuer: garminAppGeneration("client-test")})
	e2.api.perms = []string{"HEALTH_EXPORT"}
	cr = e2.connect(t, tUserA, "v1")
	if _, reason := redirectReason(e2.callback(t, cr, e2.api.newCode(cr.Challenge), "", cr.Cookie)); reason != "no_activity_permission" {
		t.Fatalf("reason=%q", reason)
	}
	if _, _, _, del := e2.api.calls(); del != 0 {
		t.Fatalf("bound to the same user: must NOT delete the registration (%d)", del)
	}
	if c := e2.store.conn(tUserA); c == nil || c.Scope != "HEALTH_EXPORT" || !c.paused() {
		t.Fatalf("scope must be updated so the card shows paused: %+v", c)
	}

	// 已綁定「另一個 DOR 使用者」：不撤銷
	e3 := newConnectEnv(t, nil)
	e3.store.putConn(garminConn{UserID: tUserB, ProviderUserID: e3.api.userID, Via: garminViaDirect, Issuer: garminAppGeneration("client-test")})
	e3.api.perms = []string{"HEALTH_EXPORT"}
	cr = e3.connect(t, tUserA, "v1")
	e3.callback(t, cr, e3.api.newCode(cr.Challenge), "", cr.Cookie)
	if _, _, _, del := e3.api.calls(); del != 0 {
		t.Fatalf("bound to another user: must NOT delete the registration (%d)", del)
	}
}

func TestCallback_AlreadyLinked_NeverDeletesRegistration(t *testing.T) {
	e := newConnectEnv(t, nil)
	// B 已綁定這個 Garmin 帳號
	e.store.putConn(garminConn{UserID: tUserB, ProviderUserID: e.api.userID, Via: garminViaDirect, Issuer: garminAppGeneration("client-test"), Scope: "ACTIVITY_EXPORT"})
	cr := e.connect(t, tUserA, "v1")
	rec := e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)
	if st, reason := redirectReason(rec); st != "error" || reason != "already_linked" {
		t.Fatalf("redirect: %q %q", st, reason)
	}
	if _, _, _, del := e.api.calls(); del != 0 {
		t.Fatalf("already_linked must NEVER call DELETE registration (that registration belongs to the legitimate account): %d", del)
	}
	if c := e.store.conn(tUserB); c == nil || c.Via != garminViaDirect {
		t.Fatalf("B's connection must be untouched: %+v", c)
	}
	if e.store.conn(tUserA) != nil {
		t.Fatal("A must not get a connection")
	}
}

func TestCallback_AccountChanged_NotOverwrittenDeleteOnlyIfUnbound(t *testing.T) {
	// A 已連 Garmin 帳號 G1；這次授權的是 G2（未綁任何 DOR 帳號）→ account_changed、不覆寫、撤銷孤兒 G2
	e := newConnectEnv(t, nil)
	e.store.putConn(garminConn{UserID: tUserA, ProviderUserID: "g-old-account", Via: garminViaDirect, Issuer: garminAppGeneration("client-test"), Scope: "ACTIVITY_EXPORT"})
	cr := e.connect(t, tUserA, "v1")
	rec := e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)
	if _, reason := redirectReason(rec); reason != "account_changed" {
		t.Fatalf("reason=%q", reason)
	}
	if c := e.store.conn(tUserA); c.ProviderUserID != "g-old-account" {
		t.Fatalf("existing connection must not be overwritten: %+v", c)
	}
	if _, _, _, del := e.api.calls(); del != 1 {
		t.Fatalf("unbound new account G2 is an orphan registration: want 1 delete, got %d", del)
	}

	// 新授權的 G2 已綁在 B：不撤銷
	e2 := newConnectEnv(t, nil)
	e2.store.putConn(garminConn{UserID: tUserA, ProviderUserID: "g-old-account", Via: garminViaDirect, Issuer: garminAppGeneration("client-test")})
	e2.store.putConn(garminConn{UserID: tUserB, ProviderUserID: e2.api.userID, Via: garminViaDirect, Issuer: garminAppGeneration("client-test")})
	cr = e2.connect(t, tUserA, "v1")
	e2.callback(t, cr, e2.api.newCode(cr.Challenge), "", cr.Cookie)
	if _, _, _, del := e2.api.calls(); del != 0 {
		t.Fatalf("G2 bound to B: must NOT delete (%d)", del)
	}
}

func TestCallback_SaveFailuresCleanOrphan(t *testing.T) {
	for name, tc := range map[string]struct {
		err        error
		wantReason string
	}{
		"save_failed":  {errTestSave, "save_failed"},
		"no token key": {ErrTokenKeyMissing, "server_config"},
	} {
		e := newConnectEnv(t, nil)
		e.store.cs.saveErr = tc.err
		cr := e.connect(t, tUserA, "v1")
		if _, reason := redirectReason(e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)); reason != tc.wantReason {
			t.Fatalf("%s: reason=%q", name, reason)
		}
		if _, _, _, del := e.api.calls(); del != 1 {
			t.Fatalf("%s: unbound user id → orphan delete expected, got %d", name, del)
		}
	}
}

var errTestSave = &testErr{"simulated save failure"}

type testErr struct{ s string }

func (e *testErr) Error() string { return e.s }

func TestCallback_BlockedByGarminUserIDIsRefused(t *testing.T) {
	e := newConnectEnv(t, nil)
	e.store.cs.blockedGIDs[e.api.userID] = true
	cr := e.connect(t, tUserA, "v1")
	rec := e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)
	if _, reason := redirectReason(rec); reason != "entry_closed" {
		t.Fatalf("blocked garmin id: %q", reason)
	}
	if len(e.store.cs.saves) != 0 {
		t.Fatal("blocked account must not be saved")
	}
	if _, _, _, del := e.api.calls(); del != 1 {
		t.Fatalf("blocked + unbound → registration deleted best-effort: %d", del)
	}
}

// --- /status ---

func TestStatus_FieldsAndNoGate(t *testing.T) {
	e := newConnectEnv(t, nil)
	e.h.draining.Store(true) // 關掉背景保活（本測試直接改假 store 的資料，避免與背景 goroutine 競態）
	get := func(uid string) map[string]any {
		rec := e.req(http.MethodGet, "/status", uid, "")
		if rec.Code != 200 {
			t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
		}
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if rec := e.req(http.MethodGet, "/status", "", ""); rec.Code != 401 {
		t.Fatalf("no login: %d", rec.Code)
	}
	m := get(tUserA)
	for _, k := range []string{"connected", "connected_at", "last_data_at", "device_name", "needs_reauth", "paused", "legacy_terra", "entry"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing status field %q", k)
		}
	}
	if m["connected"] != false || m["legacy_terra"] != false || m["entry"] != "shown" {
		t.Fatalf("not connected: %v", m)
	}
	// 舊 Terra 列
	e.store.putConn(garminConn{UserID: tUserA, ProviderUserID: "terra-uuid", Via: garminViaTerra, ConnectedAt: time.Now().Add(-48 * time.Hour)})
	if m := get(tUserA); m["legacy_terra"] != true || m["connected"] != false {
		t.Fatalf("legacy terra: %v", m)
	}
	// 直連：入口 hidden 也照常回傳（不受閘限制）
	now := time.Now()
	ls := now.Add(-time.Hour)
	e.store.putConn(garminConn{UserID: tUserA, ProviderUserID: e.api.userID, Via: garminViaDirect, Issuer: garminAppGeneration("client-test"),
		Scope: "ACTIVITY_EXPORT", ConnectedAt: now.Add(-72 * time.Hour), AuthorizedAt: &now, LastSyncedAt: &ls, ExpiresAt: now.Add(time.Hour)})
	e.store.cs.deviceName = "Forerunner 265"
	e.setEntry(tUserA, "hidden")
	m = get(tUserA)
	if m["connected"] != true || m["entry"] != "hidden" || m["device_name"] != "Forerunner 265" || m["needs_reauth"] != false || m["paused"] != false || m["last_data_at"] == nil {
		t.Fatalf("connected but entry hidden: %v", m)
	}
	// Status 不呼叫 Garmin（保活只在 refresh 快到期時才會背景觸發）
	if tk, ui, pm, del := e.api.calls(); tk+ui+pm+del != 0 {
		t.Fatalf("status must not call garmin: %d %d %d %d", tk, ui, pm, del)
	}
	// needs_reauth：旗標／App 世代不符／refresh 已過期
	e.store.cs.conns[tUserA].ReauthRequiredAt = &now
	if get(tUserA)["needs_reauth"] != true {
		t.Fatal("reauth flag")
	}
	e.store.cs.conns[tUserA].ReauthRequiredAt = nil
	e.store.cs.conns[tUserA].Issuer = "garmin-app:deadbeef"
	if get(tUserA)["needs_reauth"] != true {
		t.Fatal("app generation mismatch")
	}
	e.store.cs.conns[tUserA].Issuer = garminAppGeneration("client-test")
	past := now.Add(-time.Hour)
	e.store.cs.conns[tUserA].RefreshExpiresAt = &past
	if get(tUserA)["needs_reauth"] != true {
		t.Fatal("expired refresh")
	}
	// 暫停：權限不含 ACTIVITY_EXPORT
	e.store.cs.conns[tUserA].RefreshExpiresAt = nil
	e.store.cs.conns[tUserA].Scope = "HEALTH_EXPORT"
	if get(tUserA)["paused"] != true {
		t.Fatal("paused")
	}
}

// --- /disconnect ---

func connectedEnv(t *testing.T) (*connectEnv, *garminConn) {
	t.Helper()
	e := newConnectEnv(t, nil)
	cr := e.connect(t, tUserA, "v1")
	if _, reason := redirectReason(e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)); reason != "" {
		t.Fatalf("setup connect: %q", reason)
	}
	c := e.store.conn(tUserA)
	if c == nil {
		t.Fatal("setup: no connection")
	}
	return e, c
}

func TestDisconnect_NotConnectedIsIdempotent(t *testing.T) {
	e := newConnectEnv(t, nil)
	rec := e.req(http.MethodPost, "/disconnect", tUserA, "")
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if rec.Code != 200 || m["ok"] != true || m["was_connected"] != false || m["garmin_notified"] != false || m["deleted_activities"] != float64(0) {
		t.Fatalf("not connected: %d %v", rec.Code, m)
	}
	if rec := e.req(http.MethodPost, "/disconnect", "", ""); rec.Code != 401 {
		t.Fatalf("no login: %d", rec.Code)
	}
	// 只有舊 Terra 列：這個端點不處理（由 Terra 的 disconnect 處理）
	e.store.putConn(garminConn{UserID: tUserB, ProviderUserID: "terra-uuid", Via: garminViaTerra})
	rec = e.req(http.MethodPost, "/disconnect", tUserB, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if m["was_connected"] != false || e.store.conn(tUserB) == nil {
		t.Fatalf("legacy terra row must be left to terra's own disconnect: %v", m)
	}
}

func TestDisconnect_DeletesRegistrationThenPurges(t *testing.T) {
	for name, tc := range map[string]struct {
		deleteStatus int
		wantNotified bool
	}{
		"204 ok":        {0, true},
		"401 revoked":   {401, true},
		"403 forbidden": {403, true},
		"404 gone":      {404, true},
		"500 failure":   {500, false},
		"429 limited":   {429, false},
	} {
		e, _ := connectedEnv(t)
		e.store.cs.purgeActivities = 7
		e.api.deleteStatus = tc.deleteStatus
		var flagSeenBeforeDelete bool
		e.api.mu.Lock()
		e.api.deleteStatus = tc.deleteStatus
		e.api.mu.Unlock()
		// 撤銷前 selfdereg 標記必須已設定
		origRT := e.h.hc.Transport
		e.h.hc.Transport = rtFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodDelete {
				_, ok, _ := e.h.kv.Get(context.Background(), "garmin:selfdereg:"+tUserA)
				flagSeenBeforeDelete = ok
			}
			return origRT.RoundTrip(r)
		})
		rec := e.req(http.MethodPost, "/disconnect", tUserA, "")
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		if rec.Code != 200 || m["ok"] != true || m["was_connected"] != true || m["garmin_notified"] != tc.wantNotified || m["deleted_activities"] != float64(7) {
			t.Fatalf("%s: %d %v", name, rec.Code, m)
		}
		if !flagSeenBeforeDelete {
			t.Fatalf("%s: selfdereg flag must be set BEFORE the registration delete", name)
		}
		if e.store.conn(tUserA) != nil {
			t.Fatalf("%s: local cleanup must always happen", name)
		}
		if len(e.store.cs.purges) != 1 {
			t.Fatalf("%s: purges=%d", name, len(e.store.cs.purges))
		}
		// 冪等：第二次呼叫
		rec = e.req(http.MethodPost, "/disconnect", tUserA, "")
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		if rec.Code != 200 || m["was_connected"] != false {
			t.Fatalf("%s: second call: %v", name, m)
		}
	}
}

func TestDisconnect_NotGatedByEntry_AndNoUsableToken(t *testing.T) {
	e, c := connectedEnv(t)
	e.setEntry(tUserA, "hidden")
	// token 端點壞掉（暫時性）：無法取得可用 token → 不通知 Garmin，但本地清理照做
	e.store.cs.conns[tUserA].ExpiresAt = time.Now().Add(-time.Hour)
	e.api.tokenStatus = 503
	rec := e.req(http.MethodPost, "/disconnect", tUserA, "")
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if rec.Code != 200 || m["garmin_notified"] != false || m["was_connected"] != true {
		t.Fatalf("no usable token: %d %v", rec.Code, m)
	}
	if _, _, _, del := e.api.calls(); del != 0 {
		t.Fatalf("no registration delete without a usable token: %d", del)
	}
	if e.store.conn(tUserA) != nil {
		t.Fatalf("connection %s must be purged locally even when garmin cannot be told", c.ID)
	}
}

func TestDisconnect_PurgeFailureReturns500AndKeepsConnection(t *testing.T) {
	e, _ := connectedEnv(t)
	e.store.cs.purgeErr = errTestSave
	rec := e.req(http.MethodPost, "/disconnect", tUserA, "")
	if rec.Code != 500 {
		t.Fatalf("purge failure: %d", rec.Code)
	}
	if e.store.conn(tUserA) == nil {
		t.Fatal("connection must remain when the purge failed (so the user can retry)")
	}
	e.store.cs.purgeErr = nil
	rec = e.req(http.MethodPost, "/disconnect", tUserA, "")
	if rec.Code != 200 || e.store.conn(tUserA) != nil {
		t.Fatalf("retry: %d", rec.Code)
	}
}

func TestRouter_UserEndpointsRequireAuth_WhenNoMiddleware(t *testing.T) {
	// 沒注入 RequireAuth：fail-closed（一律 401），不可讓 connect／status／disconnect 無保護
	h := newGarminHandlerWithStore(GarminConfig{WebhookToken: testGarminToken}, GarminDeps{}, newFakeGarminStore())
	r := h.Router()
	for _, tc := range []struct{ method, path string }{{"POST", "/connect"}, {"GET", "/status"}, {"POST", "/disconnect"}} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"consent":true,"consent_v":"v1"}`)))
		if rec.Code != 401 {
			t.Errorf("%s %s: %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

func TestEndpointURLValidation(t *testing.T) {
	t.Setenv("ENV", "production")
	for u, want := range map[string]bool{
		"https://connect.garmin.com": true, "https://diauth.garmin.com/di-oauth2-service/oauth/token": true, "https://apis.garmin.com": true,
		"http://apis.garmin.com": false, "https://evil.example.com": false, "https://garmin.com.evil.com": false, "https://user@apis.garmin.com": false, "": false,
	} {
		if got := garminHostOK(u); got != want {
			t.Errorf("production garminHostOK(%q) = %v, want %v", u, got, want)
		}
	}
	t.Setenv("ENV", "development")
	if !garminHostOK("http://127.0.0.1:9") {
		t.Error("non-production may override endpoints for tests")
	}
	// production 下覆寫成非官方主機 → 連接功能停用
	t.Setenv("ENV", "production")
	h := newGarminHandlerWithStore(GarminConfig{ClientID: "c", ClientSecret: "s", WebhookToken: testGarminToken, APIBase: "https://evil.example.com"},
		GarminDeps{JWTSecret: "x"}, newFakeGarminStore())
	if h.configured() || h.disabledReason == "" {
		t.Fatalf("production with a non-garmin API base must disable connect: %q", h.disabledReason)
	}
}
