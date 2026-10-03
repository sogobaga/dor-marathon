package integration

// Terra／Strava 串接結束公告（announce）的守門單元測試：不連資料庫、不連任何外部服務。
// handler 一律以 NewTerraHandler／NewStravaHandler(nil repo…) 建出，任何資料庫存取都會 nil pointer panic，
// 所以「announce 擋下」的案例同時證明了「沒有碰資料庫」；對外 HTTP 用記錄呼叫的 in-process RoundTripper，
// 證明「沒有呼叫 Terra／Strava」。需要真資料庫的情境（webhook 只更新既有列、Strava 回呼不寫列、/status）
// 見 wearable_sunset_integration_test.go（-tags=integration）。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/integration/wearablesunset"
)

const (
	sunsetFront  = "https://app.test/"
	sunsetUser   = "11111111-1111-4111-8111-111111111111"
	sunsetTerraU = "22222222-2222-4222-8222-222222222222"
	sunsetOther  = "33333333-3333-4333-8333-333333333333"
)

func sunsetAnnounceSrc() wearablesunset.Source {
	return wearablesunset.Fixed(wearablesunset.Info{State: wearablesunset.StateAnnounce, Date: "2026-10-31"})
}
func sunsetOffSrc() wearablesunset.Source {
	return wearablesunset.Fixed(wearablesunset.Info{State: wearablesunset.StateOff})
}

// outbound 記錄所有對外 HTTP 呼叫（url），並用 respond 產生回應。
type outbound struct {
	mu      sync.Mutex
	urls    []string
	respond func(*http.Request) (*http.Response, error)
}

func (o *outbound) client() *http.Client {
	return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		o.mu.Lock()
		o.urls = append(o.urls, r.URL.String())
		o.mu.Unlock()
		if o.respond != nil {
			return o.respond(r)
		}
		return nil, errors.New("unexpected outbound call: " + r.URL.String())
	})}
}
func (o *outbound) count() int { o.mu.Lock(); defer o.mu.Unlock(); return len(o.urls) }

func sunsetTerraHandler(src wearablesunset.Source, withCreds bool, out *outbound) *TerraHandler {
	cfg := TerraConfig{FrontendURL: sunsetFront, Providers: []string{"POLAR"}}
	if withCreds {
		cfg.DevID, cfg.APIKey, cfg.SigningSecret = "d", "k", "s"
	}
	h := NewTerraHandler(nil, cfg, nil)
	h.SetSunset(src)
	if out != nil {
		h.hc = out.client()
	}
	return h
}

func sunsetWithUser(r *http.Request, id string) *http.Request {
	if id == "" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), auth.CtxKeyUserID, id))
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("response is not a flat JSON object: %v: %s", err, rec.Body.String())
	}
	return m
}

// 被擋下的回應契約：狀態碼＝wearablesunset.RefusalStatus（409）；body 有 error（可讀中文）、code、state、date。
// 不是 401／403／404（每日資安報告會算成登入失敗／不存在）也不是 5xx（會進「API 5xx 激增」告警）。
func assertSunsetDenied(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if rec.Code != wearablesunset.RefusalStatus {
		t.Fatalf("%s: status = %d (%s), want %d — 401/403 count as login failures in the daily security report, a 5xx would trip the 'API 5xx surge' alert",
			what, rec.Code, rec.Body.String(), wearablesunset.RefusalStatus)
	}
	b := decodeBody(t, rec)
	if b["code"] != wearablesunset.ErrorCode || b["state"] != "announce" || b["date"] != "2026-10-31" {
		t.Fatalf("%s: body = %#v", what, b)
	}
	if !strings.Contains(b["error"], "10 月 31 日") || !strings.Contains(b["error"], "不再開放新的連接") {
		t.Fatalf("%s: error text = %q", what, b["error"])
	}
	if banned := regexp.MustCompile(`(?i)garmin|佳明|跑團`); banned.MatchString(rec.Body.String()) {
		t.Fatalf("%s: response contains a banned word: %s", what, rec.Body.String())
	}
}

// ---------- Terra：connect（T1）----------

func TestSunsetTerraConnect_AnnounceRefusesBeforeAnythingElse(t *testing.T) {
	for _, withCreds := range []bool{true, false} { // 沒憑證也要先 409，而不是 503 terra_disabled
		out := &outbound{}
		h := sunsetTerraHandler(sunsetAnnounceSrc(), withCreds, out)
		rec := httptest.NewRecorder()
		h.Connect(rec, sunsetWithUser(httptest.NewRequest(http.MethodGet, "/connect", nil), sunsetUser))
		assertSunsetDenied(t, rec, "terra connect")
		if out.count() != 0 {
			t.Fatalf("withCreds=%v: announce must not call Terra, got %v", withCreds, out.urls)
		}
	}
}

func TestSunsetTerraConnect_LoginStillCheckedFirst(t *testing.T) {
	h := sunsetTerraHandler(sunsetAnnounceSrc(), true, &outbound{})
	rec := httptest.NewRecorder()
	h.Connect(rec, httptest.NewRequest(http.MethodGet, "/connect", nil)) // 沒有登入
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous caller must still get 401, got %d", rec.Code)
	}
}

func TestSunsetTerraConnect_OffBehavesAsBefore(t *testing.T) {
	// off、沒憑證：照舊 503 terra_disabled（守門不得改變 off 的行為）
	h := sunsetTerraHandler(sunsetOffSrc(), false, nil)
	rec := httptest.NewRecorder()
	h.Connect(rec, sunsetWithUser(httptest.NewRequest(http.MethodGet, "/connect", nil), sunsetUser))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "terra_disabled") {
		t.Fatalf("off + no credentials: %d %s", rec.Code, rec.Body.String())
	}
	// off、有憑證：照舊去跟 Terra 要 widget session
	out := &outbound{respond: func(r *http.Request) (*http.Response, error) {
		return jsonResp(201, `{"url":"https://widget.test/session/abc"}`), nil
	}}
	h = sunsetTerraHandler(sunsetOffSrc(), true, out)
	rec = httptest.NewRecorder()
	h.Connect(rec, sunsetWithUser(httptest.NewRequest(http.MethodGet, "/connect", nil), sunsetUser))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "https://widget.test/session/abc") {
		t.Fatalf("off + credentials must proceed to Terra: %d %s", rec.Code, rec.Body.String())
	}
	if out.count() != 1 || !strings.Contains(out.urls[0], "/v2/auth/generateWidgetSession") {
		t.Fatalf("expected exactly one generateWidgetSession call, got %v", out.urls)
	}
}

// 未注入 Source 的 handler（以 struct 字面值建出）視為 off，不 panic。
func TestSunsetNilSourceIsOff(t *testing.T) {
	h := &TerraHandler{cfg: TerraConfig{}}
	if h.newConnectionsPaused(context.Background()) {
		t.Fatal("nil source must mean off")
	}
	s := &StravaHandler{}
	if s.newConnectionsPaused(context.Background()) {
		t.Fatal("nil source must mean off")
	}
}

// ---------- Terra：callback（T2）----------

func TestSunsetTerraCallback_AnnounceRedirectsWithoutCallingTerra(t *testing.T) {
	for _, withCreds := range []bool{true, false} {
		out := &outbound{}
		h := sunsetTerraHandler(sunsetAnnounceSrc(), withCreds, out)
		rec := httptest.NewRecorder()
		h.Callback(rec, httptest.NewRequest(http.MethodGet,
			"/callback?user_id="+sunsetTerraU+"&reference_id="+sunsetUser+"&resource=POLAR", nil))
		if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || loc != sunsetFront+"?terra=sunset" {
			t.Fatalf("withCreds=%v: %d %q, want 302 → %s?terra=sunset", withCreds, rec.Code, loc, sunsetFront)
		}
		if out.count() != 0 {
			t.Fatalf("withCreds=%v: the block must happen before the Terra userInfo round trip, got %v", withCreds, out.urls)
		}
	}
}

func TestSunsetTerraCallback_OffStillVerifiesWithTerra(t *testing.T) {
	// off：照舊先向 Terra 反查；這裡讓 reference_id 對不上 → 導回 failed（不會走到資料庫）。
	out := &outbound{respond: func(r *http.Request) (*http.Response, error) {
		return jsonResp(200, `{"user":{"user_id":"`+sunsetTerraU+`","provider":"POLAR","reference_id":"`+sunsetOther+`"}}`), nil
	}}
	h := sunsetTerraHandler(sunsetOffSrc(), true, out)
	rec := httptest.NewRecorder()
	h.Callback(rec, httptest.NewRequest(http.MethodGet,
		"/callback?user_id="+sunsetTerraU+"&reference_id="+sunsetUser+"&resource=POLAR", nil))
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || !strings.Contains(loc, "terra=failed") {
		t.Fatalf("off: %d %q", rec.Code, loc)
	}
	if out.count() != 1 || !strings.Contains(out.urls[0], "/v2/userInfo") {
		t.Fatalf("off must still verify with Terra userInfo exactly once, got %v", out.urls)
	}
}

// ---------- Terra：落地分流（persistTerraConn）----------

type fakeTerraStore struct {
	saveCalls, updateCalls int
	saveRet, updateRet     bool
	err                    error
	last                   *Connection
}

func (f *fakeTerraStore) SaveTerraUnlessDirect(_ context.Context, c *Connection) (bool, error) {
	f.saveCalls++
	f.last = c
	return f.saveRet, f.err
}
func (f *fakeTerraStore) UpdateTerraConnIfExists(_ context.Context, c *Connection) (bool, error) {
	f.updateCalls++
	f.last = c
	return f.updateRet, f.err
}

func TestSunsetPersistTerraConn(t *testing.T) {
	ctx := context.Background()
	conn := &Connection{UserID: sunsetUser, Provider: "polar", ProviderUserID: sunsetTerraU}

	// announce：只准更新既有列，絕不呼叫會 INSERT 的 upsert
	st := &fakeTerraStore{saveRet: true, updateRet: true}
	h := sunsetTerraHandler(sunsetAnnounceSrc(), true, nil)
	h.store = st
	if saved, err := h.persistTerraConn(ctx, conn); err != nil || !saved {
		t.Fatalf("announce + existing row: saved=%v err=%v", saved, err)
	}
	if st.updateCalls != 1 || st.saveCalls != 0 {
		t.Fatalf("announce must use update-only (update=%d save=%d)", st.updateCalls, st.saveCalls)
	}

	// announce、沒有既有列：saved=false（呼叫端略過），仍然沒有走 upsert
	st = &fakeTerraStore{saveRet: true, updateRet: false}
	h.store = st
	if saved, err := h.persistTerraConn(ctx, conn); err != nil || saved {
		t.Fatalf("announce + no row: saved=%v err=%v, want false/nil", saved, err)
	}
	if st.saveCalls != 0 {
		t.Fatal("announce must never reach the upsert, even when there is no existing row")
	}

	// off：與改版前完全相同，走 upsert
	st = &fakeTerraStore{saveRet: true, updateRet: true}
	h = sunsetTerraHandler(sunsetOffSrc(), true, nil)
	h.store = st
	if saved, err := h.persistTerraConn(ctx, conn); err != nil || !saved {
		t.Fatalf("off: saved=%v err=%v", saved, err)
	}
	if st.saveCalls != 1 || st.updateCalls != 0 {
		t.Fatalf("off must use the unchanged upsert (save=%d update=%d)", st.saveCalls, st.updateCalls)
	}
	// off 時被直連列擋下：saved=false 照舊傳回
	st = &fakeTerraStore{saveRet: false}
	h.store = st
	if saved, _ := h.persistTerraConn(ctx, conn); saved {
		t.Fatal("off: a blocked-by-direct save must still report saved=false")
	}

	// 錯誤原樣往上傳（兩種狀態）
	boom := errors.New("db down")
	for _, src := range []wearablesunset.Source{sunsetAnnounceSrc(), sunsetOffSrc()} {
		h := sunsetTerraHandler(src, true, nil)
		h.store = &fakeTerraStore{err: boom}
		if _, err := h.persistTerraConn(ctx, conn); !errors.Is(err, boom) {
			t.Fatalf("error must propagate, got %v", err)
		}
	}
}

// ---------- Strava：connect（S1）與 callback（S2）----------

func sunsetStravaHandler(src wearablesunset.Source, configured bool, out *outbound) *StravaHandler {
	cfg := StravaConfig{FrontendURL: sunsetFront, JWTSecret: "unit-test-secret", RedirectURI: "https://api.test/cb", WebhookVerifyToken: "v"}
	if configured {
		cfg.ClientID, cfg.ClientSecret = "cid", "csecret"
	}
	h := NewStravaHandler(nil, cfg, nil, nil)
	h.SetSunset(src)
	if out != nil {
		h.hc = out.client()
	}
	return h
}

func TestSunsetStravaConnect(t *testing.T) {
	// announce：409，而且在 enabled() 之前（沒設憑證也是 409 而不是 503）
	for _, configured := range []bool{true, false} {
		h := sunsetStravaHandler(sunsetAnnounceSrc(), configured, nil)
		rec := httptest.NewRecorder()
		h.Connect(rec, sunsetWithUser(httptest.NewRequest(http.MethodGet, "/connect", nil), sunsetUser))
		assertSunsetDenied(t, rec, "strava connect")
	}
	// 沒登入照舊 401
	h := sunsetStravaHandler(sunsetAnnounceSrc(), true, nil)
	rec := httptest.NewRecorder()
	h.Connect(rec, httptest.NewRequest(http.MethodGet, "/connect", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous caller must still get 401, got %d", rec.Code)
	}
	// off：照舊（沒設定 503；設定好了回授權網址）
	h = sunsetStravaHandler(sunsetOffSrc(), false, nil)
	rec = httptest.NewRecorder()
	h.Connect(rec, sunsetWithUser(httptest.NewRequest(http.MethodGet, "/connect", nil), sunsetUser))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("off + not configured: %d %s", rec.Code, rec.Body.String())
	}
	h = sunsetStravaHandler(sunsetOffSrc(), true, nil)
	rec = httptest.NewRecorder()
	h.Connect(rec, sunsetWithUser(httptest.NewRequest(http.MethodGet, "/connect?return="+sunsetFront+"x", nil), sunsetUser))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "https://www.strava.com/oauth/authorize") {
		t.Fatalf("off + configured must return the authorize URL: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSunsetStravaCallback(t *testing.T) {
	ret := "https://app.test/profile"

	// announce、state 有效：導回 ?strava=sunset，沒有換 token（沒有任何對外呼叫）；repo=nil 若有寫入會 panic
	out := &outbound{}
	h := sunsetStravaHandler(sunsetAnnounceSrc(), true, out)
	state := h.signState(sunsetUser, ret)
	rec := httptest.NewRecorder()
	h.Callback(rec, httptest.NewRequest(http.MethodGet, "/callback?code=abc&state="+state, nil))
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || loc != ret+"?strava=sunset" {
		t.Fatalf("announce: %d %q, want 302 → %s?strava=sunset", rec.Code, loc, ret)
	}
	if out.count() != 0 {
		t.Fatalf("announce must not exchange the code with Strava, got %v", out.urls)
	}

	// announce、使用者在 Strava 按了「拒絕」：公告訊息優先（不是「已取消授權」）
	rec = httptest.NewRecorder()
	h.Callback(rec, httptest.NewRequest(http.MethodGet, "/callback?error=access_denied&state="+state, nil))
	if loc := rec.Header().Get("Location"); loc != ret+"?strava=sunset" {
		t.Fatalf("announce + denied: %q", loc)
	}

	// announce、state 不合法：維持原本的 invalid 導回（ret 不可信，所以回固定的前台網址）
	rec = httptest.NewRecorder()
	h.Callback(rec, httptest.NewRequest(http.MethodGet, "/callback?code=abc&state=garbage", nil))
	if loc := rec.Header().Get("Location"); loc != sunsetFront+"?strava=invalid" {
		t.Fatalf("announce + invalid state: %q", loc)
	}

	// off：守門不介入——使用者拒絕授權照舊導回 denied；有 code 就會去換 token
	h = sunsetStravaHandler(sunsetOffSrc(), true, out)
	state = h.signState(sunsetUser, ret)
	rec = httptest.NewRecorder()
	h.Callback(rec, httptest.NewRequest(http.MethodGet, "/callback?error=access_denied&state="+state, nil))
	if loc := rec.Header().Get("Location"); loc != ret+"?strava=denied" {
		t.Fatalf("off + denied: %q", loc)
	}
	out2 := &outbound{respond: func(r *http.Request) (*http.Response, error) { return jsonResp(500, `{}`), nil }}
	h = sunsetStravaHandler(sunsetOffSrc(), true, out2)
	state = h.signState(sunsetUser, ret)
	rec = httptest.NewRecorder()
	h.Callback(rec, httptest.NewRequest(http.MethodGet, "/callback?code=abc&state="+state, nil))
	if loc := rec.Header().Get("Location"); loc != ret+"?strava=error" {
		t.Fatalf("off + failing token exchange: %q", loc)
	}
	if out2.count() != 1 || out2.urls[0] != stravaTokenURL {
		t.Fatalf("off must still exchange the code with Strava once, got %v", out2.urls)
	}
}

// 兩條「結果」導回值要與前台約定一致（apps/web/src/lib/wearableSunset.ts 的 SUNSET_RESULT）。
func TestSunsetResultValueMatchesFrontendContract(t *testing.T) {
	if wearablesunset.ResultValue != "sunset" {
		t.Fatalf("ResultValue = %q", wearablesunset.ResultValue)
	}
}

// ---------- Terra：auth 事件（T3）----------

// announce 期間 auth 事件一律不落地（連既有列都不改寫）：repo=nil，所以只要 handleAuthEvent 沒有在碰資料庫之前就略過，
// 這裡就會 nil pointer panic；假的 store 也證明 persistTerraConn 一次都沒被呼叫（update／upsert 都是 0）。
// 兩種進入方式都測：直接呼叫處理函式、以及經 processWebhook 的事件類型分派。
func TestSunsetTerraAuthEvent_AnnounceIsNotLanded(t *testing.T) {
	body := []byte(`{"type":"auth","status":"success","user":{"user_id":"` + sunsetTerraU + `","provider":"POLAR","reference_id":"` + sunsetUser + `"}}`)
	for name, deliver := range map[string]func(h *TerraHandler){
		"handleAuthEvent": func(h *TerraHandler) { h.handleAuthEvent(context.Background(), body) },
		"processWebhook":  func(h *TerraHandler) { h.processWebhook(body) },
	} {
		st := &fakeTerraStore{saveRet: true, updateRet: true}
		h := sunsetTerraHandler(sunsetAnnounceSrc(), true, &outbound{})
		h.store = st
		deliver(h)
		if st.saveCalls != 0 || st.updateCalls != 0 {
			t.Errorf("%s: announce must not land an auth event at all (save=%d update=%d)", name, st.saveCalls, st.updateCalls)
		}
	}
}

// ---------- Strava：換 token 的往返期間剛好切到 announce ----------

// flipSource 第一次 Current 回 off、之後都回 announce——模擬「Callback 開頭檢查通過、換 token 的往返期間管理者把設定切到 announce」。
type flipSource struct {
	mu    sync.Mutex
	calls int
}

func (f *flipSource) Current(context.Context) wearablesunset.Info {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls == 1 {
		return wearablesunset.Info{State: wearablesunset.StateOff}
	}
	return wearablesunset.Info{State: wearablesunset.StateAnnounce, Date: "2026-10-31"}
}

// 落地前再確認一次：不存任何東西（repo=nil，若走到 repo.Save 會 panic）、導回公告訊息。而且刻意不撤銷剛換到的權杖：
// 撤銷（POST /oauth/deauthorize）對整個 athlete 生效，這位使用者若其實已有既有連線（重新授權的人），撤銷會把他的既有連線
// 一併弄壞——所以這個測試的對外呼叫清單只能有「換 token」這一筆。
func TestSunsetStravaCallback_FlipToAnnounceDuringTokenExchange(t *testing.T) {
	ret := "https://app.test/profile"
	out := &outbound{respond: func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == stravaTokenURL {
			return jsonResp(200, `{"access_token":"acc-flip","refresh_token":"ref-flip","expires_at":4102444800,"athlete":{"id":4242,"firstname":"Flip","lastname":"Test"}}`), nil
		}
		return nil, errors.New("unexpected outbound call: " + r.URL.String())
	}}
	src := &flipSource{}
	h := sunsetStravaHandler(src, true, out)
	state := h.signState(sunsetUser, ret)
	rec := httptest.NewRecorder()
	h.Callback(rec, httptest.NewRequest(http.MethodGet, "/callback?code=abc&state="+state, nil))
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || loc != ret+"?strava=sunset" {
		t.Fatalf("flip during exchange: %d %q, want 302 → %s?strava=sunset", rec.Code, loc, ret)
	}
	if len(out.urls) != 1 || out.urls[0] != stravaTokenURL {
		t.Fatalf("expected exactly the token exchange (no deauthorize: it would break an existing connection), got %v", out.urls)
	}
	if src.calls < 2 {
		t.Fatalf("the gate must be re-checked after the token exchange (Current called %d times)", src.calls)
	}
}
