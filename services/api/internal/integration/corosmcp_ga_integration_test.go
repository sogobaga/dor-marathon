//go:build integration

// COROS MCP GA（全員開放）整合測試——真實 Postgres（本機拋棄式容器，已套用 migration 198）＋假 COROS（in-process，
// 不走本機 TCP、不打 coros.com）。涵蓋契約 §2.1–§2.6、§3.1–§3.5：入口三態與閘門範圍、reauth 旗標與非破壞性重新授權、
// 帳號綁定、節流／鎖／冷卻／kill switch、讀取測試僅超管且只存摘要、中斷（單一交易、不受閘門限制、GPS 校正重設）、
// 競賽分組重算 callback、直連優先去重的 SP 不重扣、合理性檢查。
package integration

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	gaKeyOnce sync.Once
	gaKey     *rsa.PrivateKey
)

func gaRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	gaKeyOnce.Do(func() { gaKey, _ = rsa.GenerateKey(rand.Reader, 2048) })
	return gaKey
}

// gaIDToken 假 COROS 的 id_token 產生器（RS256，kid=k1）。mod 可改 claims（測錯誤 aud 等）。
func gaIDToken(t *testing.T, sub string, mod func(jwt.MapClaims)) func(issuer, clientID string) string {
	key := gaRSAKey(t)
	return func(issuer, clientID string) string {
		c := jwt.MapClaims{"iss": issuer, "aud": []string{clientID}, "sub": sub,
			"exp": time.Now().Add(10 * time.Minute).Unix(), "iat": time.Now().Unix()}
		if mod != nil {
			mod(c)
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
		tok.Header["kid"] = "k1"
		s, err := tok.SignedString(key)
		if err != nil {
			panic(err)
		}
		return s
	}
}

func (e *mcpImportEnv) newUser(label string) string {
	return corosMcpCreateUser(e.t, e.ctx, e.pool, "corosmcp-ga-"+label+"-"+corosMcpRandSuffix()+"@example.com")
}

func (e *mcpImportEnv) status(uid string) map[string]any {
	e.t.Helper()
	return decodeJSONMap(e.t, e.do(http.MethodGet, "/status", uid))
}

func (e *mcpImportEnv) connRow(uid string) (createdAt time.Time, reauth *time.Time, lastSynced *time.Time, accountID string, ok bool) {
	e.t.Helper()
	err := e.pool.QueryRow(e.ctx, `SELECT created_at, reauth_required_at, last_synced_at, COALESCE(provider_user_id,'')
		FROM user_integrations WHERE user_id=$1 AND provider=$2`, uid, providerCorosMcp).Scan(&createdAt, &reauth, &lastSynced, &accountID)
	return createdAt, reauth, lastSynced, accountID, err == nil
}

// ---------- §2.1／§2.2 入口三態與閘門範圍 ----------

func TestGA_EntryGate_AcrossStatesAndEndpoints(t *testing.T) {
	e := newMcpImportEnv(t, 1)
	listed := e.users[0] // newMcpImportEnv 已把它放進 coros_mcp_whitelist
	super := e.newUser("super")
	corosMcpMakeSuper(t, e.ctx, e.pool, super)
	other := e.newUser("other")

	type tc struct {
		state                   string
		listed, super, other    int // /connect 預期狀態碼
		entryListed, entrySuper string
		entryOther              string
	}
	cases := []tc{
		{"hidden", 403, 403, 403, "hidden", "hidden", "hidden"}, // 緊急關閉：含超管與白名單
		{"whitelist", 200, 200, 403, "shown", "shown", "hidden"},
		{"", 200, 200, 403, "shown", "shown", "hidden"}, // 缺鍵＝whitelist
		{"open", 200, 200, 200, "shown", "shown", "shown"},
	}
	for _, c := range cases {
		corosMcpSetEntryState(t, e.ctx, e.pool, c.state)
		for _, u := range []struct {
			name, id string
			want     int
			entry    string
		}{{"listed", listed, c.listed, c.entryListed}, {"super", super, c.super, c.entrySuper}, {"other", other, c.other, c.entryOther}} {
			if rw := e.do(http.MethodPost, "/connect", u.id); rw.Code != u.want {
				t.Fatalf("state=%q user=%s: POST /connect = %d, want %d (%s)", c.state, u.name, rw.Code, u.want, rw.Body.String())
			}
			// status 永遠 200，並帶 entry；entry 必須與 Dashboard 用的判斷（DashboardEntry）及 API 閘門（EntryForUser）完全一致
			rw := e.do(http.MethodGet, "/status", u.id)
			if rw.Code != http.StatusOK {
				t.Fatalf("state=%q user=%s: /status must never be gated, got %d", c.state, u.name, rw.Code)
			}
			st := decodeJSONMap(t, rw)
			if st["entry"] != u.entry {
				t.Fatalf("state=%q user=%s: status.entry=%v want %s", c.state, u.name, st["entry"], u.entry)
			}
			var email, code string
			var isSuper bool
			if err := e.pool.QueryRow(e.ctx, `SELECT email, COALESCE(account_code,''), is_super_admin FROM users WHERE id=$1`, u.id).Scan(&email, &code, &isSuper); err != nil {
				t.Fatal(err)
			}
			if got := DashboardEntry(e.ctx, e.pool, EntryProviderCoros, email, code, isSuper); got != u.entry {
				t.Fatalf("state=%q user=%s: Dashboard entry %q != expected %q", c.state, u.name, got, u.entry)
			}
			if got, err := EntryForUser(e.ctx, e.pool, EntryProviderCoros, u.id); err != nil || got != u.entry {
				t.Fatalf("state=%q user=%s: API gate entry (%q,%v) != expected %q", c.state, u.name, got, err, u.entry)
			}
		}
	}

	// 緊急關閉時：import 擋、status／disconnect 不擋（已連線的人要能看與能中斷）
	corosMcpSetEntryState(t, e.ctx, e.pool, "open")
	uid := e.connectUser(0, mcpFloorNoon(20))
	corosMcpSetEntryState(t, e.ctx, e.pool, "hidden")
	if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusForbidden {
		t.Fatalf("emergency shutdown must block /import even for a connected, listed user, got %d", rw.Code)
	}
	st := e.status(uid)
	if st["connected"] != true || st["entry"] != "hidden" {
		t.Fatalf("a connected user must still see /status under emergency shutdown: %v", st)
	}
	if rw := e.do(http.MethodPost, "/disconnect", uid); rw.Code != http.StatusOK {
		t.Fatalf("disconnect must work under emergency shutdown, got %d %s", rw.Code, rw.Body.String())
	}
	if _, _, _, _, ok := e.connRow(uid); ok {
		t.Fatal("connection must be gone after disconnect under emergency shutdown")
	}
}

// callback 也要再判斷一次入口：使用者在 COROS 授權頁時入口被緊急關閉 → 不換 token、不建連線。
func TestGA_Callback_ReChecksEntryGate(t *testing.T) {
	e := newMcpImportEnv(t, 1)
	uid := e.users[0]
	rw := e.do(http.MethodPost, "/connect", uid)
	if rw.Code != http.StatusOK {
		t.Fatalf("connect: %d", rw.Code)
	}
	var body struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(rw.Body.Bytes(), &body)
	parsed, _ := url.Parse(body.URL)
	corosMcpSetEntryState(t, e.ctx, e.pool, "hidden") // 授權途中被緊急關閉
	req := httptest.NewRequest(http.MethodGet, "/callback?code=test-code&state="+url.QueryEscape(parsed.Query().Get("state")), nil)
	for _, c := range rw.Result().Cookies() {
		req.AddCookie(c)
	}
	crw := httptest.NewRecorder()
	e.h.Router().ServeHTTP(crw, req)
	if loc := crw.Header().Get("Location"); crw.Code != http.StatusFound || !strings.Contains(loc, "coros_mcp=error") || !strings.Contains(loc, "reason=entry_closed") {
		t.Fatalf("callback under emergency shutdown must be refused with entry_closed, got %d %q", crw.Code, loc)
	}
	if _, _, _, _, calls := e.fc.snapshot(); calls != 0 {
		t.Fatalf("no token exchange when the gate is closed, calls=%d", calls)
	}
	if _, _, _, _, ok := e.connRow(uid); ok {
		t.Fatal("no connection may be created")
	}
}

// ---------- §3.1 自動同步：kill switch／緊急關閉／全域並發／鎖 ----------

func TestGA_AutoSync_KillSwitchEmergencyAndLock(t *testing.T) {
	e := newMcpImportEnv(t, 1)
	now := time.Now().Truncate(time.Second)
	label := mcpTestLabel(1)
	e.fc.setRecords([]corosFixtureRec{
		{Title: "Outdoor Run", Start: now.AddDate(0, 0, -1).Unix(), ElapsedS: 2494, DurationS: 2433, Distance: "5.31 km", HR: 133, LabelID: label, Sport: 100},
	})
	uid := e.connectUser(0, mcpFloorNoon(20))

	// kill switch：coros_mcp_autosync_enabled=0 → 完全不動作（不打 COROS、不扣名額）；手動匯入照常可用
	corosMcpSetSetting(t, e.ctx, e.pool, corosMcpAutoSyncKey, "0")
	if ran, _, err := e.h.autoSyncOnce(e.ctx, uid); ran || err != nil {
		t.Fatalf("kill switch off: ran=%v err=%v", ran, err)
	}
	if e.fc.hits() != 0 {
		t.Fatalf("kill switch must prevent every COROS call, hits=%d", e.fc.hits())
	}
	if !e.h.claimAutoSync(e.ctx, uid) {
		t.Fatal("kill switch must not consume the user's 25-minute quota")
	}
	e.resetThrottle()
	if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusOK {
		t.Fatalf("manual import must still work with the kill switch off, got %d %s", rw.Code, rw.Body.String())
	}
	if _, ok := e.activities(uid)["mcp:"+label]; !ok {
		t.Fatal("manual import should have imported the run")
	}

	// 再開 kill switch：自動同步恢復（缺鍵＝開）
	corosMcpSetSetting(t, e.ctx, e.pool, corosMcpAutoSyncKey, "1")
	e.resetThrottle()
	hits := e.fc.hits()
	if ran, _, err := e.h.autoSyncOnce(e.ctx, uid); !ran || err != nil {
		t.Fatalf("kill switch on: ran=%v err=%v", ran, err)
	}
	if e.fc.hits() == hits {
		t.Fatal("autosync should have called COROS")
	}

	// 緊急關閉（entry=hidden）：自動同步也停
	corosMcpSetEntryState(t, e.ctx, e.pool, "hidden")
	e.resetThrottle()
	hits = e.fc.hits()
	if ran, _, err := e.h.autoSyncOnce(e.ctx, uid); ran || err != nil || e.fc.hits() != hits {
		t.Fatalf("emergency shutdown must stop autosync: ran=%v err=%v hits %d→%d", ran, err, hits, e.fc.hits())
	}
	corosMcpSetEntryState(t, e.ctx, e.pool, "open")

	// in-flight 鎖被占用（例如手動匯入進行中）：自動同步不跑、不打 COROS
	e.resetThrottle()
	release, ok := e.h.acquireSyncLock(e.ctx, uid)
	if !ok {
		t.Fatal("setup lock")
	}
	hits = e.fc.hits()
	if ran, _, err := e.h.autoSyncOnce(e.ctx, uid); ran || err != nil || e.fc.hits() != hits {
		t.Fatalf("lock held: autosync must not run: ran=%v err=%v", ran, err)
	}
	release()
}

// ---------- §2.3／§2.4 reauth 旗標與非破壞性重新授權 ----------

func TestGA_Reauth_PreservesFloorClearsFlagAndCatchesUp(t *testing.T) {
	e := newMcpImportEnv(t, 1)
	now := time.Now().Truncate(time.Second)
	label := mcpTestLabel(1)
	e.fc.setRecords([]corosFixtureRec{
		{Title: "Outdoor Run", Start: now.AddDate(0, 0, -1).Unix(), ElapsedS: 2494, DurationS: 2433, Distance: "5.31 km", HR: 133, LabelID: label, Sport: 100},
	})
	floor := mcpFloorNoon(10)
	uid := e.connectUser(0, floor)

	// 模擬「授權到期後」：旗標已寫、token 過期、上次同步在 3 天前、沒有 refresh token
	if _, err := e.pool.Exec(e.ctx, `UPDATE user_integrations SET reauth_required_at=NOW()-INTERVAL '1 hour',
		expires_at=NOW()-INTERVAL '1 day', refresh_token='', last_synced_at=NOW()-INTERVAL '3 days'
		WHERE user_id=$1 AND provider=$2`, uid, providerCorosMcp); err != nil {
		t.Fatal(err)
	}
	createdBefore, _, lastSyncedBefore, _, _ := e.connRow(uid)
	st := e.status(uid)
	if st["needs_reauth"] != true || st["expires_at"] == nil {
		t.Fatalf("expired + flagged: needs_reauth must be true and expires_at present: %v", st)
	}
	if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusConflict || !strings.Contains(rw.Body.String(), "reconnect_required") {
		t.Fatalf("flagged connection: /import → 409 reconnect_required, got %d %s", rw.Code, rw.Body.String())
	}
	hits := e.fc.hits()
	if ran, _, err := e.h.autoSyncOnce(e.ctx, uid); ran || err == nil {
		t.Fatalf("flagged connection: autosync must not call COROS (ran=%v err=%v)", ran, err)
	}
	if e.fc.hits() != hits {
		t.Fatal("a flagged connection must not generate COROS traffic")
	}

	// 重新授權（已連接者再走 /connect → callback）
	if loc := corosMcpConnectAndCallback(t, e.h, uid); !strings.Contains(loc, "coros_mcp=connected") {
		t.Fatalf("re-authorization must succeed for a connected user: %s", loc)
	}
	createdAfter, reauthAfter, _, _, ok := e.connRow(uid)
	if !ok || !createdAfter.Equal(createdBefore) {
		t.Fatalf("created_at (import floor) must be preserved on re-authorization: before=%v after=%v", createdBefore, createdAfter)
	}
	if !createdAfter.Equal(floor) {
		t.Fatalf("floor changed: %v vs %v", createdAfter, floor)
	}
	if reauthAfter != nil {
		t.Fatal("re-authorization must clear reauth_required_at")
	}
	conn, err := e.h.getConnection(e.ctx, uid)
	if err != nil || conn == nil || conn.AccessToken != "tok-1" || !conn.ExpiresAt.After(time.Now().Add(30*time.Minute)) {
		t.Fatalf("tokens must be refreshed by the re-authorization: %+v err=%v", conn, err)
	}
	st = e.status(uid)
	if st["needs_reauth"] != false {
		t.Fatalf("after re-authorization needs_reauth=false, got %v", st)
	}
	// 立即補同步（背景）：起點 max(floor, last_synced−1d)；跑步在 floor 之後 → 匯入
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := e.activities(uid)["mcp:"+label]; ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("re-authorization must trigger an immediate catch-up sync that imports the missed run")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// last_synced_at 在整輪同步結束時才寫（背景 goroutine，匯入完成後還有尾巴）：輪詢等它前進。
	deadline = time.Now().Add(5 * time.Second)
	for {
		_, _, lastSyncedAfter, _, _ := e.connRow(uid)
		if lastSyncedAfter != nil && (lastSyncedBefore == nil || lastSyncedAfter.After(*lastSyncedBefore)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("catch-up must advance last_synced_at: before=%v after=%v", lastSyncedBefore, lastSyncedAfter)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// refresh 流程：有 refresh token 時 expires_at 為 null；refresh 失敗(invalid_grant)→寫旗標、之後不再打 COROS；重新授權清旗標。
func TestGA_ReauthFlagLifecycle_RefreshToken(t *testing.T) {
	e := newMcpImportEnv(t, 1)
	e.fc.setRecords([]corosFixtureRec{})
	uid := e.connectUser(0, mcpFloorNoon(20))

	st := e.status(uid)
	if st["connected"] != true || st["needs_reauth"] != false || st["expires_at"] != nil || st["can_probe"] != false {
		t.Fatalf("usable refresh token: needs_reauth=false, expires_at=null, can_probe=false (non-super), got %v", st)
	}

	// access token 過期但 refresh token 有效 → 同步時自動換新，不需要使用者動作
	if _, err := e.pool.Exec(e.ctx, `UPDATE user_integrations SET expires_at=NOW()-INTERVAL '1 minute' WHERE user_id=$1 AND provider=$2`, uid, providerCorosMcp); err != nil {
		t.Fatal(err)
	}
	if st := e.status(uid); st["needs_reauth"] != false || st["expires_at"] != nil {
		t.Fatalf("expired access token with a usable refresh token is not a user action: %v", st)
	}
	if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusOK {
		t.Fatalf("import with auto-refresh: %d %s", rw.Code, rw.Body.String())
	}
	if last, _, _, _, _ := e.fc.snapshot(); last != "Bearer tok-2" {
		t.Fatalf("expected the refreshed token tok-2 to be used, got %q", last)
	}
	if _, reauth, _, _, _ := e.connRow(uid); reauth != nil {
		t.Fatal("successful refresh keeps reauth_required_at NULL")
	}

	// refresh 失敗（invalid_grant：DB 裡的 refresh token 被換成錯的）→ 409、寫旗標
	e.resetThrottle()
	bad, _ := EncryptTokenStrict("rtok-WRONG")
	if _, err := e.pool.Exec(e.ctx, `UPDATE user_integrations SET expires_at=NOW()-INTERVAL '1 minute', refresh_token=$3 WHERE user_id=$1 AND provider=$2`, uid, providerCorosMcp, bad); err != nil {
		t.Fatal(err)
	}
	if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusConflict || !strings.Contains(rw.Body.String(), "reconnect_required") {
		t.Fatalf("invalid_grant → 409 reconnect_required, got %d %s", rw.Code, rw.Body.String())
	}
	if _, reauth, _, _, _ := e.connRow(uid); reauth == nil {
		t.Fatal("invalid_grant must set reauth_required_at")
	}
	st = e.status(uid)
	if st["needs_reauth"] != true || st["expires_at"] == nil {
		t.Fatalf("flagged: needs_reauth=true, and the expiry is shown because the refresh token is no longer usable: %v", st)
	}
	hits := e.fc.hits()
	e.resetThrottle()
	if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusConflict {
		t.Fatalf("still 409 while flagged, got %d", rw.Code)
	}
	if e.fc.hits() != hits {
		t.Fatal("a flagged connection must not hit COROS again until re-authorized")
	}
	// 重新授權 → 旗標清除
	if loc := corosMcpConnectAndCallback(t, e.h, uid); !strings.Contains(loc, "coros_mcp=connected") {
		t.Fatalf("reauth: %s", loc)
	}
	if _, reauth, _, _, _ := e.connRow(uid); reauth != nil {
		t.Fatal("re-authorization clears the flag")
	}
	if st := e.status(uid); st["needs_reauth"] != false || st["expires_at"] != nil {
		t.Fatalf("after re-authorization: %v", st)
	}
}

// ---------- §2.6 帳號綁定 ----------

func TestGA_AccountBinding(t *testing.T) {
	e := newMcpImportEnv(t, 3, fakeCorosOpts{issueRefresh: true, firstExpiresIn: 3600, idToken: gaIDToken(t, "acct-A", nil)})
	e.fc.setRecords([]corosFixtureRec{})
	u1, u2 := e.users[0], e.users[1]

	if loc := corosMcpConnectAndCallback(t, e.h, u1); !strings.Contains(loc, "coros_mcp=connected") {
		t.Fatalf("first account: %s", loc)
	}
	if _, _, _, id, _ := e.connRow(u1); id != "coros:acct-A" {
		t.Fatalf("provider_user_id must hold the bound COROS identity (region tag + sub), got %q", id)
	}
	// 同一個 COROS 帳號被第二個 DOR 帳號連接 → 拒絕（already_linked），不建連線
	loc := corosMcpConnectAndCallback(t, e.h, u2)
	if !strings.Contains(loc, "coros_mcp=already_linked") || strings.Contains(loc, "coros_mcp=connected") {
		t.Fatalf("second DOR account with the same COROS account must be refused: %s", loc)
	}
	if _, _, _, _, ok := e.connRow(u2); ok {
		t.Fatal("no connection row for the second DOR account")
	}
	// 同一個 DOR 帳號重新授權同一個 COROS 帳號 → 成功（綁定不變）
	if loc := corosMcpConnectAndCallback(t, e.h, u1); !strings.Contains(loc, "coros_mcp=connected") {
		t.Fatalf("same account re-auth: %s", loc)
	}
	// 重新授權時換了另一個 COROS 帳號 → account_changed（要先中斷連線）
	// 另一台假 COROS：同一個 issuer（discovery 與 DCR client 沿用），但這次的 id_token 是另一個 COROS 帳號。
	e2 := newMcpImportEnvSharing(t, e, fakeCorosOpts{issueRefresh: true, firstExpiresIn: 3600,
		idToken: gaIDToken(t, "acct-B", func(c jwt.MapClaims) { c["iss"] = e.fc.srv.URL })})
	loc = corosMcpConnectAndCallback(t, e2.h, u1)
	if !strings.Contains(loc, "reason=account_changed") {
		t.Fatalf("switching COROS account without disconnecting must be refused: %s", loc)
	}
	if _, _, _, id, _ := e.connRow(u1); id != "coros:acct-A" {
		t.Fatalf("binding must not change, got %q", id)
	}
	// 中斷後可以改綁另一個帳號
	if rw := e.do(http.MethodPost, "/disconnect", u1); rw.Code != http.StatusOK {
		t.Fatalf("disconnect: %d", rw.Code)
	}
	if loc := corosMcpConnectAndCallback(t, e2.h, u1); !strings.Contains(loc, "coros_mcp=connected") {
		t.Fatalf("after disconnect another COROS account can be bound: %s", loc)
	}
	// 中斷也釋放綁定：原 COROS 帳號 A 現在可由第二個 DOR 帳號連接
	if loc := corosMcpConnectAndCallback(t, e.h, u2); !strings.Contains(loc, "coros_mcp=connected") {
		t.Fatalf("disconnect must release the binding of account A: %s", loc)
	}
}

// newMcpImportEnvSharing 以「同一個資料庫、同一批使用者、同一個 issuer／DCR client」另建一台假 COROS＋handler——
// 只為了讓第二次授權拿到不同內容的 id_token（呼叫端要把 id_token 的 iss 設成 base 的 issuer）。
func newMcpImportEnvSharing(t *testing.T, base *mcpImportEnv, opts fakeCorosOpts) *mcpImportEnv {
	t.Helper()
	fc := newFakeCorosMCPServer(t, opts)
	h := NewCorosMcpHandler(base.repo, CorosMcpConfig{
		GatewayURL: base.fc.srv.URL, RedirectURI: "https://www.dor.tw/api/v1/integrations/coros-mcp/callback",
		FrontendURL: "https://www.dor.tw", JWTSecret: "test-secret",
	}, passthroughAuth, nil)
	h.autoJitterMax = 0
	// discovery（快取）與 DCR client（資料庫）都是 base 的；所有對外請求改走新假伺服器的 mux
	h.hc = &http.Client{Timeout: corosMcpMCPTimeout, Transport: inProcessRoundTripper{handler: fc.mux}}
	return &mcpImportEnv{t: t, ctx: base.ctx, pool: base.pool, repo: base.repo, fc: fc, h: h, users: base.users}
}

func TestGA_AccountBinding_NoIDTokenConnectsUnbound_InvalidIDTokenRefused(t *testing.T) {
	// 沒有 id_token（COROS 沒發）：連線照建、provider_user_id 空字串；兩個 DOR 帳號都能連（部分唯一索引不管空字串）
	e := newMcpImportEnv(t, 2)
	e.fc.setRecords([]corosFixtureRec{})
	for i, uid := range e.users {
		if loc := corosMcpConnectAndCallback(t, e.h, uid); !strings.Contains(loc, "coros_mcp=connected") {
			t.Fatalf("user %d without id_token must still connect: %s", i, loc)
		}
		if _, _, _, id, ok := e.connRow(uid); !ok || id != "" {
			t.Fatalf("user %d: unbound connection expected, got ok=%v id=%q", i, ok, id)
		}
	}

	// id_token 有但 audience 不符 → 拒絕（identity_invalid），不建連線
	bad := newMcpImportEnv(t, 1, fakeCorosOpts{issueRefresh: true, firstExpiresIn: 3600,
		idToken: gaIDToken(t, "acct-X", func(c jwt.MapClaims) { c["aud"] = []string{"somebody-else"} })})
	if loc := corosMcpConnectAndCallback(t, bad.h, bad.users[0]); !strings.Contains(loc, "reason=identity_invalid") {
		t.Fatalf("an id_token for another audience must be refused: %s", loc)
	}
	if _, _, _, _, ok := bad.connRow(bad.users[0]); ok {
		t.Fatal("no connection for a failed identity check")
	}
	// id_token 過期 → 拒絕
	expired := newMcpImportEnv(t, 1, fakeCorosOpts{issueRefresh: true, firstExpiresIn: 3600,
		idToken: gaIDToken(t, "acct-Y", func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Hour).Unix() })})
	if loc := corosMcpConnectAndCallback(t, expired.h, expired.users[0]); !strings.Contains(loc, "reason=identity_invalid") {
		t.Fatalf("an expired id_token must be refused: %s", loc)
	}
}

// 有 jwks_uri 時走驗簽路徑（假 JWKS 端點；host 用 coros.com 風格網址，in-process 傳輸不會真的連線）。
func TestGA_AccountBinding_SignedIDTokenWithJWKS(t *testing.T) {
	e := newMcpImportEnv(t, 1, fakeCorosOpts{issueRefresh: true, firstExpiresIn: 3600, idToken: gaIDToken(t, "acct-S", nil)})
	e.fc.setRecords([]corosFixtureRec{})
	key := gaRSAKey(t)
	e.fc.mu.Lock()
	e.fc.jwks = rsaJWKS(t, "k1", &key.PublicKey)
	e.fc.mu.Unlock()
	doc, _ := corosMcpDiscoveryCacheGet(e.fc.srv.URL)
	cp := *doc
	cp.JWKSURI = "https://mcpus.coros.com/jwks"
	corosMcpDiscoveryCacheSet(e.fc.srv.URL, &cp)
	if loc := corosMcpConnectAndCallback(t, e.h, e.users[0]); !strings.Contains(loc, "coros_mcp=connected") {
		t.Fatalf("signed id_token with JWKS must connect: %s", loc)
	}
	if _, _, _, id, _ := e.connRow(e.users[0]); id != "coros:acct-S" {
		t.Fatalf("identity: %q", id)
	}
	// JWKS 對不上金鑰（換一把）→ 驗簽失敗 → 拒絕
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	e.fc.mu.Lock()
	e.fc.jwks = rsaJWKS(t, "k1", &other.PublicKey)
	e.fc.mu.Unlock()
	if rw := e.do(http.MethodPost, "/disconnect", e.users[0]); rw.Code != http.StatusOK {
		t.Fatal("disconnect")
	}
	if loc := corosMcpConnectAndCallback(t, e.h, e.users[0]); !strings.Contains(loc, "reason=identity_invalid") {
		t.Fatalf("an id_token that does not verify against the JWKS must be refused: %s", loc)
	}
}

// ---------- §3.1 手動匯入節流／鎖／冷卻 ----------

func TestGA_ManualImport_ThrottleLockCooldown(t *testing.T) {
	e := newMcpImportEnv(t, 1)
	now := time.Now().Truncate(time.Second)
	label := mcpTestLabel(1)
	e.fc.setRecords([]corosFixtureRec{
		{Title: "Outdoor Run", Start: now.AddDate(0, 0, -1).Unix(), ElapsedS: 2494, DurationS: 2433, Distance: "5.31 km", HR: 133, LabelID: label, Sport: 100},
	})
	uid := e.connectUser(0, mcpFloorNoon(20))

	// 1) 第一次成功
	if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusOK {
		t.Fatalf("first import: %d %s", rw.Code, rw.Body.String())
	}
	// 2) 5 分鐘內再按：429 rate_limited＋retry_after_s（約 300 秒）；不打 COROS
	hits := e.fc.hits()
	rw := e.do(http.MethodPost, "/import", uid)
	m := decodeJSONMap(t, rw)
	if rw.Code != http.StatusTooManyRequests || m["error"] != "rate_limited" || jnum(m, "retry_after_s") < 250 {
		t.Fatalf("second import within 5 minutes: %d %v", rw.Code, m)
	}
	if e.fc.hits() != hits {
		t.Fatal("a throttled import must not call COROS")
	}
	// 3) Redis／記憶體名額被清空（模擬 Redis flush），但 last_synced_at 還在 → 第二道節流仍擋（不連打）
	e.h.mem.mu.Lock()
	e.h.mem.m = nil
	e.h.mem.mu.Unlock()
	rw = e.do(http.MethodPost, "/import", uid)
	m = decodeJSONMap(t, rw)
	if rw.Code != http.StatusTooManyRequests || m["error"] != "rate_limited" {
		t.Fatalf("with the quota key gone, last_synced_at must still throttle: %d %v", rw.Code, m)
	}
	if e.fc.hits() != hits {
		t.Fatal("second guard must prevent COROS calls")
	}
	// 4) in-flight 鎖被占用：429 sync_in_progress，且不扣手動名額
	e.resetThrottle()
	release, ok := e.h.acquireSyncLock(e.ctx, uid)
	if !ok {
		t.Fatal("setup lock")
	}
	rw = e.do(http.MethodPost, "/import", uid)
	m = decodeJSONMap(t, rw)
	if rw.Code != http.StatusTooManyRequests || m["error"] != "sync_in_progress" {
		t.Fatalf("lock held: %d %v", rw.Code, m)
	}
	release()
	if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusOK {
		t.Fatalf("after the lock is released the manual quota must still be available (it was not consumed): %d %s", rw.Code, rw.Body.String())
	}
	// 5) COROS 回 500 → 502＋冷卻 30 分鐘；之後（名額到期也一樣）429 cooldown、不打 COROS；自動同步同樣被擋
	e.resetThrottle()
	e.fc.setMCPStatus(http.StatusInternalServerError)
	if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusBadGateway {
		t.Fatalf("COROS 5xx → 502, got %d %s", rw.Code, rw.Body.String())
	}
	if e.h.cooldownLeft(e.ctx, uid) < 29*time.Minute {
		t.Fatal("a COROS 5xx must start a ~30 minute cooldown for this user")
	}
	e.fc.setMCPStatus(0)
	e.resetThrottle() // 清名額與 last_synced_at；冷卻（記憶體雙寫）仍在
	e.h.setCooldown(e.ctx, uid)
	hits = e.fc.hits()
	rw = e.do(http.MethodPost, "/import", uid)
	m = decodeJSONMap(t, rw)
	if rw.Code != http.StatusTooManyRequests || m["error"] != "cooldown" || jnum(m, "retry_after_s") < 1700 {
		t.Fatalf("cooldown: %d %v", rw.Code, m)
	}
	if ran, _, err := e.h.autoSyncOnce(e.ctx, uid); ran || err != nil {
		t.Fatalf("cooldown also blocks autosync: ran=%v err=%v", ran, err)
	}
	if e.fc.hits() != hits {
		t.Fatal("cooldown must prevent any COROS traffic")
	}
	// 429 同樣觸發冷卻
	e.h.mem.mu.Lock()
	e.h.mem.m = nil
	e.h.mem.mu.Unlock()
	e.fc.setMCPStatus(http.StatusTooManyRequests)
	if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusBadGateway {
		t.Fatalf("COROS 429 → 502, got %d", rw.Code)
	}
	if e.h.cooldownLeft(e.ctx, uid) <= 0 {
		t.Fatal("a COROS 429 must start the cooldown")
	}
}

// 首次連接（從未同步）手動匯入回看 30 天；之後 3 天（請求更多也只給 3）。
func TestGA_ManualImport_LookbackDays(t *testing.T) {
	e := newMcpImportEnv(t, 1)
	e.fc.setRecords([]corosFixtureRec{})
	uid := e.connectUser(0, mcpFloorNoon(40)) // floor 夠早，不會夾住 30 天
	m := decodeJSONMap(t, e.do(http.MethodPost, "/import?days=30", uid))
	if jnum(m, "days") != 30 {
		t.Fatalf("first connect looks back 30 days: %v", m)
	}
	e.resetThrottle()
	m = decodeJSONMap(t, e.do(http.MethodPost, "/import?days=30", uid))
	if jnum(m, "days") != 3 {
		t.Fatalf("afterwards 3 days (30 requested): %v", m)
	}
	e.resetThrottle()
	m = decodeJSONMap(t, e.do(http.MethodPost, "/import", uid))
	if jnum(m, "days") != 3 {
		t.Fatalf("default is 3 days: %v", m)
	}
}

// ---------- §3.2 讀取測試僅超管、只存摘要；/status 的 can_probe ----------

func TestGA_Probe_SuperAdminOnly_CanProbeInStatus(t *testing.T) {
	e := newMcpImportEnv(t, 1)
	e.fc.setRecords([]corosFixtureRec{})
	normal := e.users[0]
	super := e.newUser("probe-super")
	corosMcpMakeSuper(t, e.ctx, e.pool, super)
	e.connectUser(0, mcpFloorNoon(20))
	if loc := corosMcpConnectAndCallback(t, e.h, super); !strings.Contains(loc, "coros_mcp=connected") {
		t.Fatalf("super connect: %s", loc)
	}

	// 一般使用者（已連線、在白名單）：/probe 403；status 的 can_probe=false 且看不到 last_probe
	if rw := e.do(http.MethodPost, "/probe", normal); rw.Code != http.StatusForbidden {
		t.Fatalf("non-super /probe must be 403, got %d %s", rw.Code, rw.Body.String())
	}
	if n := e.fc.hits(); n != 0 {
		t.Fatalf("a refused probe must not touch COROS, hits=%d", n)
	}
	if st := e.status(normal); st["can_probe"] != false || st["last_probe"] != nil {
		t.Fatalf("non-super status: %v", st)
	}
	// 超管：can_probe=true，probe 成功，摘要只含代碼、可從 status 讀回
	if st := e.status(super); st["can_probe"] != true {
		t.Fatalf("super status must advertise can_probe=true: %v", st)
	}
	rw := e.do(http.MethodPost, "/probe", super)
	if rw.Code != http.StatusOK {
		t.Fatalf("super probe: %d %s", rw.Code, rw.Body.String())
	}
	if rw2 := e.do(http.MethodPost, "/probe", super); rw2.Code != http.StatusTooManyRequests {
		t.Fatalf("probe is throttled to 1/min: %d", rw2.Code)
	}
	if st := e.status(super); st["last_probe"] == nil {
		t.Fatalf("super sees the last probe summary: %v", st)
	}
	// 不論 COROS 回什麼，回給前台的錯誤只有穩定代碼（不含 COROS 原始字串）
	e.h.mem.mu.Lock()
	e.h.mem.m = nil
	e.h.mem.mu.Unlock()
	e.fc.setAnomaly(true)
	rw = e.do(http.MethodPost, "/probe", super)
	if rw.Code != http.StatusOK {
		t.Fatalf("probe with anomaly still returns step list: %d %s", rw.Code, rw.Body.String())
	}
	body := rw.Body.String()
	if strings.Contains(body, "Tool call anomalies") || strings.Contains(body, "High risk") {
		t.Fatalf("probe response must not echo COROS error text: %s", body)
	}
	if !strings.Contains(body, `"error":"anomaly"`) {
		t.Fatalf("probe response must carry the stable error code: %s", body)
	}
	e.fc.setAnomaly(false)

	// 緊急關閉：連超管也不能 probe，can_probe=false
	corosMcpSetEntryState(t, e.ctx, e.pool, "hidden")
	if rw := e.do(http.MethodPost, "/probe", super); rw.Code != http.StatusForbidden {
		t.Fatalf("emergency shutdown blocks probe even for super admin, got %d", rw.Code)
	}
	if st := e.status(super); st["can_probe"] != false {
		t.Fatalf("can_probe must be false under emergency shutdown: %v", st)
	}
}

// ---------- §3.3 中斷：單一交易、GPS 校正重設 ----------

func (e *mcpImportEnv) seedCalibPair(uid, extActivityID, extSource string, factor float64) {
	e.t.Helper()
	var runID string
	if err := e.pool.QueryRow(e.ctx, `
		INSERT INTO gps_runs (user_id, started_at, ended_at, distance_km, duration_s, avg_pace_s)
		VALUES ($1, NOW()-INTERVAL '3 hours', NOW()-INTERVAL '2 hours 20 minutes', 5.5, 2400, 436) RETURNING id::text`, uid).Scan(&runID); err != nil {
		e.t.Fatalf("seed gps run: %v", err)
	}
	if _, err := e.pool.Exec(e.ctx, `
		INSERT INTO gps_calib_pairs (user_id, gps_run_id, ext_activity_id, ext_source, gps_km, ext_km, gps_dur_s, ext_dur_s,
		                             start_gap_s, end_gap_s, log_ratio, dist_w, accepted, activity_at)
		VALUES ($1,$2,$3,$4,5.5,5.31,2400,2433,5,5,-0.03,1,TRUE,NOW()-INTERVAL '3 hours')`, uid, runID, extActivityID, extSource); err != nil {
		e.t.Fatalf("seed calib pair: %v", err)
	}
	if _, err := e.pool.Exec(e.ctx, `
		INSERT INTO user_gps_calib (user_id, ref_source, factor, status, n_pairs, last_pair_at, computed_at, version)
		VALUES ($1,$2,$3,'active',1,NOW(),NOW(),1)
		ON CONFLICT (user_id) DO UPDATE SET factor=EXCLUDED.factor, status='active', n_pairs=1`, uid, extSource, factor); err != nil {
		e.t.Fatalf("seed user_gps_calib: %v", err)
	}
}

func (e *mcpImportEnv) calib(uid string) (factor float64, status string, pairs int) {
	e.t.Helper()
	if err := e.pool.QueryRow(e.ctx, `SELECT factor::float8, status FROM user_gps_calib WHERE user_id=$1`, uid).Scan(&factor, &status); err != nil {
		e.t.Fatalf("read calib: %v", err)
	}
	_ = e.pool.QueryRow(e.ctx, `SELECT COUNT(*) FROM gps_calib_pairs WHERE user_id=$1`, uid).Scan(&pairs)
	return
}

func TestGA_Disconnect_ResetsGpsCalibOnlyWhenDerivedFromMcpRows(t *testing.T) {
	e := newMcpImportEnv(t, 2)
	now := time.Now().Truncate(time.Second)
	labelA := mcpTestLabel(1)
	e.fc.setRecords([]corosFixtureRec{
		{Title: "Outdoor Run", Start: now.AddDate(0, 0, -1).Unix(), ElapsedS: 2494, DurationS: 2433, Distance: "5.31 km", HR: 133, LabelID: labelA, Sport: 100},
	})
	uidA := e.connectUser(0, mcpFloorNoon(20))
	uidB := e.connectUser(1, mcpFloorNoon(20))
	for _, uid := range []string{uidA, uidB} {
		e.resetThrottle()
		if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusOK {
			t.Fatalf("import: %d %s", rw.Code, rw.Body.String())
		}
	}
	// A：校正配對來自 mcp 列（GA 前白名單期間的影子模式）→ 中斷後重設
	actA := e.activities(uidA)["mcp:"+labelA]
	e.seedCalibPair(uidA, actA.ID, "coros", 0.9712)
	// B：校正配對來自 Strava 列 → 中斷 COROS 不得動它
	res, err := e.repo.ImportActivity(e.ctx, &NormalizedActivity{UserID: uidB, Source: "strava", ExternalID: "str-" + corosMcpRandSuffix(),
		Fingerprint: fingerprintOf(now.AddDate(0, 0, -9).Unix(), 5000, 1800), DistanceKm: 5, DurationS: 1800, AvgPaceS: 360, RecordedAt: now.AddDate(0, 0, -9).UTC()})
	if err != nil || res.Status != "inserted" {
		t.Fatalf("seed strava row: %+v %v", res, err)
	}
	e.seedCalibPair(uidB, res.ID, "strava", 0.9650)
	// 另外種一筆 probe log，確認同交易刪除
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO coros_mcp_probe_logs (user_id, tool, response, status) VALUES ($1,'queryDevices','{"summary":{"step":"queryDevices","ok":true,"count":null}}','ok')`, uidA); err != nil {
		t.Fatal(err)
	}

	rw := e.do(http.MethodPost, "/disconnect", uidA)
	if rw.Code != http.StatusOK || jnum(decodeJSONMap(t, rw), "deleted_activities") != 1 {
		t.Fatalf("disconnect A: %d %s", rw.Code, rw.Body.String())
	}
	if f, st, pairs := e.calib(uidA); f != 1.0 || st != "warming" || pairs != 0 {
		t.Fatalf("A's calibration derived from mcp rows must be reset: factor=%v status=%s pairs=%d", f, st, pairs)
	}
	var probeRows int
	_ = e.pool.QueryRow(e.ctx, `SELECT COUNT(*) FROM coros_mcp_probe_logs WHERE user_id=$1`, uidA).Scan(&probeRows)
	if probeRows != 0 {
		t.Fatalf("probe logs deleted with the connection: %d", probeRows)
	}

	rw = e.do(http.MethodPost, "/disconnect", uidB)
	if rw.Code != http.StatusOK {
		t.Fatalf("disconnect B: %d", rw.Code)
	}
	if f, st, pairs := e.calib(uidB); f != 0.9650 || st != "active" || pairs != 1 {
		t.Fatalf("B's calibration came from Strava and must be untouched: factor=%v status=%s pairs=%d", f, st, pairs)
	}
}

// 清除失敗（這裡以已取消的 context 讓交易無法開始）時什麼都不會被改動；成功時活動＋連線列一起消失。
func TestGA_PurgeCorosMcpUser_FailedPurgeChangesNothing(t *testing.T) {
	e := newMcpImportEnv(t, 1)
	now := time.Now().Truncate(time.Second)
	label := mcpTestLabel(1)
	e.fc.setRecords([]corosFixtureRec{
		{Title: "Outdoor Run", Start: now.AddDate(0, 0, -1).Unix(), ElapsedS: 2494, DurationS: 2433, Distance: "5.31 km", HR: 133, LabelID: label, Sport: 100},
	})
	uid := e.connectUser(0, mcpFloorNoon(20))
	if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusOK {
		t.Fatalf("import: %d", rw.Code)
	}
	// 用已取消的 context 呼叫：交易必然失敗 → 什麼都不能被改動
	cctx, cancel := context.WithCancel(e.ctx)
	cancel()
	if _, _, err := e.repo.PurgeCorosMcpUser(cctx, uid); err == nil {
		t.Fatal("a canceled context must fail the purge")
	}
	if _, ok := e.activities(uid)["mcp:"+label]; !ok {
		t.Fatal("failed purge must leave the activities")
	}
	if _, _, _, _, ok := e.connRow(uid); !ok {
		t.Fatal("failed purge must leave the connection")
	}
	n, _, err := e.repo.PurgeCorosMcpUser(e.ctx, uid)
	if err != nil || n != 1 {
		t.Fatalf("purge: n=%d err=%v", n, err)
	}
	if _, _, _, _, ok := e.connRow(uid); ok {
		t.Fatal("connection gone after a successful purge")
	}
}

// ---------- §3.5 競賽分組成績重算 callback ----------

func TestGA_ImportTriggersCompetitionRecompute(t *testing.T) {
	old := standingsDebounce
	// 正式值是 3 秒；測試用 400ms：同一次同步裡兩筆匯入（各含數個資料庫交易）之間的間隔要明顯小於去抖時間。
	standingsDebounce = 400 * time.Millisecond
	t.Cleanup(func() { standingsDebounce = old; SetCompetitionRecompute(nil) })
	got := make(chan string, 4)
	SetCompetitionRecompute(func(ctx context.Context, userID string) { got <- userID })

	e := newMcpImportEnv(t, 1)
	now := time.Now().Truncate(time.Second)
	e.fc.setRecords([]corosFixtureRec{
		{Title: "Outdoor Run", Start: now.AddDate(0, 0, -1).Unix(), ElapsedS: 2494, DurationS: 2433, Distance: "5.31 km", HR: 133, LabelID: mcpTestLabel(1), Sport: 100},
		{Title: "Outdoor Run 2", Start: now.AddDate(0, 0, -2).Unix(), ElapsedS: 2494, DurationS: 2433, Distance: "5.10 km", HR: 133, LabelID: mcpTestLabel(2), Sport: 100},
	})
	uid := e.connectUser(0, mcpFloorNoon(20))
	if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusOK {
		t.Fatalf("import: %d", rw.Code)
	}
	select {
	case u := <-got:
		if u != uid {
			t.Fatalf("recompute for the wrong user %s", u)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("an import with new unflagged rows must trigger the competition standings recompute")
	}
	select {
	case <-got:
		t.Fatal("two imports in one sync must be coalesced into one recompute (debounce)")
	case <-time.After(700 * time.Millisecond):
	}
	// 沒有新增（全部 exists）→ 不重算
	e.resetThrottle()
	if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusOK {
		t.Fatalf("re-import: %d", rw.Code)
	}
	select {
	case <-got:
		t.Fatal("re-import with nothing new must not trigger a recompute")
	case <-time.After(700 * time.Millisecond):
	}
}

// ---------- §2.7 直連優先去重（經 COROS 匯入流程）＋ SP 不重扣 ----------

func (e *mcpImportEnv) spUpdatedAt(uid string) time.Time {
	e.t.Helper()
	var ts time.Time
	if err := e.pool.QueryRow(e.ctx, `SELECT sp_updated_at FROM users WHERE id=$1::uuid`, uid).Scan(&ts); err != nil {
		e.t.Fatalf("sp_updated_at: %v", err)
	}
	return ts
}

func TestGA_StravaFirst_McpLater_SupersedesStravaWithoutDoubleSP(t *testing.T) {
	e := newMcpImportEnv(t, 2)
	now := time.Now().Truncate(time.Second)
	start := now.Add(-3 * time.Hour) // 結束在 SP 新鮮度（24h）內，對照組會扣血
	label, ctrlLabel := mcpTestLabel(1), mcpTestLabel(2)
	e.fc.setRecords([]corosFixtureRec{
		{Title: "Outdoor Run", Start: start.Unix(), ElapsedS: 2494, DurationS: 2433, Distance: "5.31 km", HR: 133, LabelID: label, Sport: 100},
	})
	uid := e.connectUser(0, mcpFloorNoon(20))
	// Strava 先到（同一趟：指紋完全相同，COROS→Strava 自動同步的典型情形）
	sres, err := e.repo.ImportActivity(e.ctx, &NormalizedActivity{UserID: uid, Source: "strava", ExternalID: "str-" + corosMcpRandSuffix(),
		Fingerprint: fingerprintOf(start.Unix(), 5310, 2433), DistanceKm: 5.31, DurationS: 2433, AvgPaceS: 458, RecordedAt: start.UTC()})
	if err != nil || sres.Status != "inserted" {
		t.Fatalf("seed strava row: %+v %v", sres, err)
	}
	if err := e.repo.AwardMileageExp(e.ctx, sres.ID, uid); err != nil { // Strava 那筆先拿到 EXP／里程
		t.Fatal(err)
	}
	kmBefore := e.totalKm(uid)
	spBefore := e.spUpdatedAt(uid)

	m := decodeJSONMap(t, e.do(http.MethodPost, "/import", uid))
	if jnum(m, "imported") != 1 || jnum(m, "duplicate") != 0 {
		t.Fatalf("the direct watch row must be counted (not a duplicate): %v", m)
	}
	acts := e.activities(uid)
	mcp := acts["mcp:"+label]
	if mcp.Flagged || mcp.Reason != nil {
		t.Fatalf("COROS MCP row must stay unflagged (counts in races): %+v", mcp)
	}
	var strava mcpActRow
	for _, a := range acts {
		if a.Source == "strava" {
			strava = a
		}
	}
	if !strava.Flagged || strava.Reason == nil || *strava.Reason != "cross_source_duplicate" || strava.DupOf == nil || *strava.DupOf != mcp.ID {
		t.Fatalf("Strava row must be marked cross_source_duplicate pointing at the COROS row: %+v", strava)
	}
	// EXP／里程只補差額（同一趟不雙算）
	if got := e.totalKm(uid); got < kmBefore-0.0001 || got > kmBefore+0.0001 {
		t.Fatalf("total_km must not double count the same run: before=%v after=%v", kmBefore, got)
	}
	// 體力 SP：Strava 那筆匯入時已扣過，取代之後不能再扣一次
	if after := e.spUpdatedAt(uid); !after.Equal(spBefore) {
		t.Fatalf("SP must not be charged a second time when superseding a Strava row (sp_updated_at %v → %v)", spBefore, after)
	}

	// 對照組：另一位使用者同樣的 COROS 匯入、沒有 Strava 重疊 → 會扣 SP（證明上面的「不扣」不是因為 SP 本來就不動）
	uid2 := e.connectUser(1, mcpFloorNoon(20))
	// 指紋必須與上面那位使用者的不同（起始秒／距離／時長完全相同會被當成跨帳號複製而標記）。
	e.fc.setRecords([]corosFixtureRec{
		{Title: "Outdoor Run", Start: start.Add(-17 * time.Minute).Unix(), ElapsedS: 2440, DurationS: 2400, Distance: "5.40 km", HR: 133, LabelID: ctrlLabel, Sport: 100},
	})
	sp2Before := e.spUpdatedAt(uid2)
	if rw := e.do(http.MethodPost, "/import", uid2); rw.Code != http.StatusOK {
		t.Fatalf("control import: %d", rw.Code)
	}
	if after := e.spUpdatedAt(uid2); !after.After(sp2Before) {
		t.Fatalf("control: a plain fresh import must charge SP (sp_updated_at %v → %v)", sp2Before, after)
	}
}

// ---------- §2.5 合理性檢查（經匯入流程）----------

func TestGA_Import_PlausibilityFlagsAndSkips(t *testing.T) {
	e := newMcpImportEnv(t, 1)
	now := time.Now().Truncate(time.Second)
	okL, fastL, futL := mcpTestLabel(1), mcpTestLabel(2), mcpTestLabel(3)
	recs := []corosFixtureRec{
		{Title: "Normal Run", Start: now.AddDate(0, 0, -2).Unix(), ElapsedS: 2494, DurationS: 2433, Distance: "5.31 km", HR: 133, LabelID: okL, Sport: 100},
		{Title: "Impossible Pace", Start: now.AddDate(0, 0, -1).Unix(), ElapsedS: 600, DurationS: 600, Distance: "10.00 km", HR: 150, LabelID: fastL, Sport: 100},
	}
	checkFuture := time.Now().In(corosMcpTZ).Hour() < 23 // 接近午夜時「30 分鐘後」可能落到明天，假 COROS 的日期篩選會把它濾掉
	if checkFuture {
		recs = append(recs, corosFixtureRec{Title: "Future Run", Start: now.Add(30 * time.Minute).Unix(), ElapsedS: 1800, DurationS: 1800, Distance: "5.00 km", HR: 140, LabelID: futL, Sport: 100})
	}
	e.fc.setRecords(recs)
	uid := e.connectUser(0, mcpFloorNoon(20))
	m := decodeJSONMap(t, e.do(http.MethodPost, "/import", uid))
	acts := e.activities(uid)
	if r, ok := acts["mcp:"+okL]; !ok || r.Flagged {
		t.Fatalf("the normal run must import cleanly: %+v", r)
	}
	fast, ok := acts["mcp:"+fastL]
	if !ok || !fast.Flagged || fast.Reason == nil || *fast.Reason != "implausible_pace" || fast.ExpAwarded {
		t.Fatalf("an impossible pace is stored but flagged implausible_pace and never rewarded: %+v (present=%v)", fast, ok)
	}
	if checkFuture {
		if _, ok := acts["mcp:"+futL]; ok {
			t.Fatal("a run starting in the future must be skipped, not stored")
		}
		if jnum(m, "skipped_invalid") < 1 {
			t.Fatalf("the skipped future run is reported as skipped_invalid: %v", m)
		}
	}
	// 只有正常那趟入帳（5.31 km）；10 km 的不可能配速不計入 total_km
	if got := e.totalKm(uid); got < 5.309 || got > 5.311 {
		t.Fatalf("total_km must only include the plausible run (5.31), got %v", got)
	}
}

// ---------- §2.9 日報「直連手錶」段：COROS DirectStats（純 DB、只有人數）----------

func TestGA_DirectStats_Counts(t *testing.T) {
	e := newMcpImportEnv(t, 5)
	ctx := e.ctx
	before, err := e.h.DirectStats(ctx)
	if err != nil {
		t.Fatalf("DirectStats: %v", err)
	}
	ins := func(uid, accountID, refresh string, expires, lastSynced, reauth, created string) {
		if _, err := e.pool.Exec(ctx, `
			INSERT INTO user_integrations (user_id, provider, provider_user_id, access_token, refresh_token, expires_at, last_synced_at, reauth_required_at, created_at, updated_at)
			VALUES ($1,'coros_mcp',$2,'a',$3, `+expires+`, `+lastSynced+`, `+reauth+`, `+created+`, NOW())`, uid, accountID, refresh); err != nil {
			t.Fatalf("seed connection: %v", err)
		}
	}
	// A：正常（綁定、1 小時前同步、有 refresh token）
	ins(e.users[0], "coros:a", "enc:r", "NOW()+INTERVAL '20 days'", "NOW()-INTERVAL '1 hour'", "NULL", "NOW()-INTERVAL '10 days'")
	// B：4 天沒同步（stale）
	ins(e.users[1], "", "enc:r", "NOW()+INTERVAL '20 days'", "NOW()-INTERVAL '4 days'", "NULL", "NOW()-INTERVAL '10 days'")
	// C：refresh 失敗被標記（需要重新授權），10 小時前同步過
	ins(e.users[2], "", "enc:r", "NOW()+INTERVAL '20 days'", "NOW()-INTERVAL '10 hours'", "NOW()-INTERVAL '1 hour'", "NOW()-INTERVAL '10 days'")
	// D：沒有 refresh token 且已過期（需要重新授權），連接 20 天前、從未同步（算 stale）
	ins(e.users[3], "", "", "NOW()-INTERVAL '1 day'", "NULL", "NULL", "NOW()-INTERVAL '20 days'")
	// E：剛連接、還沒同步（不算 stale）
	ins(e.users[4], "", "enc:r", "NOW()+INTERVAL '29 days'", "NULL", "NULL", "NOW()")
	// 24 小時內匯入 2 筆 mcp 活動（user A）
	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		st := now.Add(-time.Duration(3+i*4) * time.Hour)
		if res, err := e.repo.ImportActivity(ctx, &NormalizedActivity{UserID: e.users[0], Source: "coros", ExternalID: "mcp:" + mcpTestLabel(100+i), DistanceKm: 5, DurationS: 1500, AvgPaceS: 300, RecordedAt: st, Fingerprint: fingerprintOf(st.Unix(), 5000, 1500)}); err != nil || res.Status != "inserted" {
			t.Fatalf("seed activity: %+v %v", res, err)
		}
	}
	after, err := e.h.DirectStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Provider != "coros" || after.Connected-before.Connected != 5 || after.Active24h-before.Active24h != 2 ||
		after.NeedsReauth-before.NeedsReauth != 2 || after.Stale3d-before.Stale3d != 2 || after.Imported24h-before.Imported24h != 2 {
		t.Fatalf("stats deltas wrong: before=%+v after=%+v", before, after)
	}
	// 備註只講「尚未綁定」的人數（B、C、D、E 共 4 位）；不含任何個人資訊
	joined := strings.Join(after.Notes, "|")
	if !strings.Contains(joined, "尚未綁定 COROS 帳號識別") {
		t.Fatalf("notes should mention unbound connections: %v", after.Notes)
	}
	for _, bad := range []string{"@", "example.com", "corosmcp"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("direct wearable notes must not contain identifying text %q: %s", bad, joined)
		}
	}
	if note, err := e.h.MaintainDaily(ctx); err != nil || note != "" {
		t.Fatalf("COROS MaintainDaily has nothing to report: %q %v", note, err)
	}
	if e.h.Name() != "coros" {
		t.Fatalf("name = %q", e.h.Name())
	}
}

// 沒有有效的 token 加密金鑰：/connect 立刻回 503（不讓使用者走完 COROS 授權才在 callback 失敗）；
// 已存在的連線仍可看狀態、可中斷（status／disconnect 不依賴金鑰）。
func TestGA_Connect_RefusedWithoutTokenKey_StatusAndDisconnectStillWork(t *testing.T) {
	e := newMcpImportEnv(t, 1)
	e.fc.setRecords([]corosFixtureRec{})
	uid := e.connectUser(0, mcpFloorNoon(20)) // 金鑰有效時先連上
	t.Setenv("STRAVA_TOKEN_KEY", "")          // 之後金鑰遺失
	if rw := e.do(http.MethodPost, "/connect", uid); rw.Code != http.StatusServiceUnavailable {
		t.Fatalf("connect without a token key must be refused with 503, got %d %s", rw.Code, rw.Body.String())
	}
	if st := e.status(uid); st["connected"] != true {
		t.Fatalf("status must keep working when the key is gone (no token decryption needed): %v", st)
	}
	if rw := e.do(http.MethodPost, "/disconnect", uid); rw.Code != http.StatusOK {
		t.Fatalf("disconnect must keep working when the key is gone, got %d %s", rw.Code, rw.Body.String())
	}
	if _, _, _, _, ok := e.connRow(uid); ok {
		t.Fatal("connection deleted even though its tokens could not be decrypted")
	}
}
