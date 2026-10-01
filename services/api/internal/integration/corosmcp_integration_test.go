//go:build integration

// 需要真實 Postgres（Neon 暫存分支，已套用 migrations/196）才能跑——比照
// internal/gpsrawlog/integration_test.go 的既有慣例：獨立 build tag，預設
// `go test ./...` 不會編譯/執行這個檔案，只在 `go test -tags=integration ./internal/integration/...`
// 且設定 DOR_TEST_DATABASE_URL 時才跑。全程只打 httptest 假伺服器（127.0.0.1），絕不連真正 coros.com——
// discovery 文件直接塞進套件內的記憶體快取（corosMcpDiscoveryCacheSet），繞過 corosMcpValidateHost
// 的「host 必須是 coros.com 子網域」檢查（那條規則本身已在 corosmcp_test.go 用字串案例單獨驗證），
// 原理等同「正式環境已經對真正的 coros.com 做過一次合法 discovery、快取 6 小時」的中段狀態。
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/auth"
)

func corosMcpSetupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("DOR_TEST_DATABASE_URL not set; skipping Neon 整合測試")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func corosMcpRandSuffix() string { return time.Now().Format("150405.000000000") }

func corosMcpCreateUser(t *testing.T, ctx context.Context, db *pgxpool.Pool, email string) string {
	t.Helper()
	var id string
	handle := "corosmcp_" + corosMcpRandSuffix()
	err := db.QueryRow(ctx, `INSERT INTO users (email, handle, name, password_hash) VALUES ($1,$2,'Test','x') RETURNING id::text`, email, handle).Scan(&id)
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM users WHERE id=$1::uuid`, id)
	})
	return id
}

func corosMcpSetWhitelist(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES ($1,$2,NOW())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`, corosMcpWhitelistKey, email)
	if err != nil {
		t.Fatalf("set whitelist: %v", err)
	}
	appsettings.InvalidateCache()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_settings WHERE key=$1`, corosMcpWhitelistKey)
		appsettings.InvalidateCache()
	})
}

func passthroughAuth(next http.Handler) http.Handler { return next }

func withUser(r *http.Request, userID string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), auth.CtxKeyUserID, userID))
}

// fakeCorosOpts：假 COROS 的可調行為。issueRefresh=false 模擬 Spring Authorization Server 預設「不發
// refresh token 給 public client」；firstExpiresIn 是 authorization_code 換到的 access token 秒數。
type fakeCorosOpts struct {
	issueRefresh   bool
	firstExpiresIn int
}

// fakeCoros：測試可讀的假伺服器狀態（皆以 mu 保護）。
type fakeCoros struct {
	srv          *httptest.Server
	mux          *http.ServeMux // corosMcpInProcessHTTP 直接呼叫它，不走本機 TCP（原因見該函式）
	mu           sync.Mutex
	lastMCPAuth  string
	regCount     int
	dcrMethods   []string // 每次 DCR 請求要求的 token_endpoint_auth_method
	tokenAuthHdr []string // 每次 token 請求的 Authorization 標頭（public client 必須為空）
	tokenCalls   int
	// 第二階段：querySportRecords 的假資料（nil＝沿用第一階段的舊文字）、各工具被呼叫次數、anomaly 模式。
	records   []corosFixtureRec
	toolCalls map[string]int
	anomaly   bool
}

// setRecords／calls／setAnomaly：測試與假伺服器 handler 共用，皆以 mu 保護。
func (f *fakeCoros) setRecords(r []corosFixtureRec) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = r
}

func (f *fakeCoros) calls(tool string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.toolCalls[tool]
}

func (f *fakeCoros) setAnomaly(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.anomaly = on
}

// recordsInRange 依 startDate／endDate（yyyyMMdd，台灣日期，含頭尾）過濾，模擬 COROS 的日期篩選。
func (f *fakeCoros) recordsInRange(sd, ed string) []corosFixtureRec {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err1 := time.ParseInLocation("20060102", sd, corosMcpTZ)
	e, err2 := time.ParseInLocation("20060102", ed, corosMcpTZ)
	if err1 != nil || err2 != nil {
		return nil
	}
	var out []corosFixtureRec
	for _, r := range f.records {
		if r.Start >= s.Unix() && r.Start < e.AddDate(0, 0, 1).Unix() {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeCoros) snapshot() (lastMCPAuth string, regCount int, dcrMethods, tokenAuthHdr []string, tokenCalls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastMCPAuth, f.regCount, append([]string(nil), f.dcrMethods...), append([]string(nil), f.tokenAuthHdr...), f.tokenCalls
}

// newFakeCorosMCPServer 起一個「照 COROS 正式站實際行為」的假伺服器（2026-10-01 實測，見 corosmcp.go 檔頭）：
//   - DCR：不論要求什麼方法，一律登記成 token_endpoint_auth_method="none"、不發 client_secret。
//   - token（Spring Authorization Server 規則）：帶任何 Authorization 標頭 → 400 invalid_request（v868 正式站
//     第一次連線失敗的根因：空密碼 Basic）；缺 client_id → 401 invalid_client；authorization_code 缺
//     code_verifier → 400 invalid_grant；refresh token 不對 → 400 invalid_grant。
//   - revoke：metadata 的 revocation 驗證方式不含 none → public client 一律 401 invalid_client。
//   - MCP JSON-RPC（initialize/tools/list/tools/call），記下最後一次 Authorization 供斷言。
func newFakeCorosMCPServer(t *testing.T, opts ...fakeCorosOpts) *fakeCoros {
	t.Helper()
	o := fakeCorosOpts{issueRefresh: true, firstExpiresIn: 30}
	if len(opts) > 0 {
		o = opts[0]
	}
	f := &fakeCoros{}
	mux := http.NewServeMux()
	mux.HandleFunc("/connect/register", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		method, _ := body["token_endpoint_auth_method"].(string)
		f.mu.Lock()
		f.regCount++
		f.dcrMethods = append(f.dcrMethods, method)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"client_id":"fake-client-id","scope":"offline_access openid mcp.tools","client_name":"DOR","grant_types":["authorization_code","refresh_token"],"redirect_uris":["https://www.dor.tw/api/v1/integrations/coros-mcp/callback"],"client_id_issued_at":1790838960,"token_endpoint_auth_method":"none"}`))
	})
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.tokenCalls++
		f.tokenAuthHdr = append(f.tokenAuthHdr, r.Header.Get("Authorization"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_request","error_description":"OAuth 2.0 Parameter: client_secret"}`))
			return
		}
		if r.PostForm.Get("client_id") != "fake-client-id" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		refresh := func(tok string) string {
			if !o.issueRefresh {
				return ""
			}
			return fmt.Sprintf(`,"refresh_token":%q`, tok)
		}
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			if r.PostForm.Get("code_verifier") == "" || r.PostForm.Get("code") != "test-code" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"access_token":"tok-1"%s,"expires_in":%d,"token_type":"Bearer","scope":"openid offline_access mcp.tools"}`, refresh("rtok-1"), o.firstExpiresIn)
		case "refresh_token":
			if r.PostForm.Get("refresh_token") != "rtok-1" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"access_token":"tok-2"%s,"expires_in":3600,"token_type":"Bearer","scope":"openid offline_access mcp.tools"}`, refresh("rtok-2"))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unsupported_grant_type"}`))
		}
	})
	mux.HandleFunc("/oauth2/revoke", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.lastMCPAuth = r.Header.Get("Authorization")
		f.mu.Unlock()
		var body struct {
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.Method {
		case "initialize":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
		case "tools/list":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[
				{"name":"queryDevices","inputSchema":{}},
				{"name":"querySportRecords","inputSchema":{"properties":{"startDate":{},"endDate":{}}}},
				{"name":"getActivityDetail","inputSchema":{}},
				{"name":"queryActivityLapData","inputSchema":{}}
			]}}`))
		case "tools/call":
			// 照正式站實際行為（2026-10-01）：參數不合規格時 isError=false＋固定的 anomaly 文字；正常回應是
			// 「JSON 字串字面值」包著人看的文字（外層多一層引號）。
			const anomaly = "Tool call anomalies detected. High risk of session context pollution or request exceeds the LLM capability boundary."
			args := body.Params.Arguments
			var text string
			f.mu.Lock()
			if f.toolCalls == nil {
				f.toolCalls = map[string]int{}
			}
			f.toolCalls[body.Params.Name]++
			hasRecords, anomalyOn := f.records != nil, f.anomaly
			f.mu.Unlock()
			switch body.Params.Name {
			case "queryDevices":
				text = "Bound Devices (1)\n========================\n\n1. COROS PACE 4\n   Model Name: COROS R4"
			case "querySportRecords":
				sd, _ := args["startDate"].(string)
				_, hasLoc := args["locationKeyword"]
				ed, _ := args["endDate"].(string)
				if len(sd) != 8 || strings.Contains(sd, "-") || !hasLoc || anomalyOn {
					text = anomaly // 日期非 yyyyMMdd 或缺 required 欄位（或測試要求的 anomaly 模式）
				} else if hasRecords {
					text = corosFixtureText(f.recordsInRange(sd, ed)) // 第二階段：照日期範圍回假活動
				} else {
					text = "Workout Records (2)\n\n1. 2026-09-30 Outdoor Run\n   Distance: 10.02 km\n   labelId: 4761111\n   sportType: 100\n\n2. 2026-09-29 Walk\n   labelId: 4762222\n   sportType: 900"
				}
			case "getActivityDetail", "queryActivityLapData":
				st, isNum := args["sportType"].(float64)
				if args["labelId"] != "4761111" || !isNum || st != 100 {
					text = anomaly
				} else if body.Params.Name == "getActivityDetail" {
					text = "Activity Detail\nDistance: 10.02 km\nDuration: 00:52:10"
				} else {
					text = "Laps (2)\n1. 1.00 km 5:10\n2. 1.00 km 5:05"
				}
			default:
				text = anomaly
			}
			quoted, _ := json.Marshal(text)
			resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"result":{"isError":false,"content":[{"type":"text","text":%q}]}}`, string(quoted))
			_, _ = w.Write([]byte(resp))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	f.mux = mux
	f.srv = httptest.NewServer(mux) // 只用來產生 URL；實際請求一律經 corosMcpInProcessHTTP 直接進 mux
	t.Cleanup(f.srv.Close)
	return f
}

// corosMcpInProcessHTTP：把 handler 的 HTTP client 改成直接呼叫假 COROS 的 mux（inProcessRoundTripper，見
// corosmcp_test.go），不走本機 TCP。2026-10-01 實測：這台開發機的執行沙盒會改寫 127.0.0.1 上的 HTTP 回應——
// 伺服器實際寫出「Content-Length: 36」的正確回應，客戶端收到的卻變成「transfer-encoding: chunked、內容沒分段」，
// 讀 body 卡到逾時（scratchpad keepalive_repro 以伺服器端 tee 對照證實，與 DOR 程式無關）。正式站與 COROS
// 走 HTTPS、沒有這層改寫。改走 in-process 後，資料庫部分（Neon 暫時分支）仍是真實驗證。
func corosMcpInProcessHTTP(h *CorosMcpHandler, fc *fakeCoros) {
	h.hc = &http.Client{Timeout: corosMcpMCPTimeout, Transport: inProcessRoundTripper{handler: fc.mux}}
}

func seedDiscovery(base string) *corosMcpDiscoveryDoc {
	doc := &corosMcpDiscoveryDoc{
		Issuer:                base,
		AuthorizationEndpoint: base + "/oauth2/authorize",
		TokenEndpoint:         base + "/oauth2/token",
		RevocationEndpoint:    base + "/oauth2/revoke",
		RegistrationEndpoint:  base + "/connect/register",
	}
	corosMcpDiscoveryCacheSet(base, doc)
	return doc
}

// TestIntegration_FullFlow：connect → callback → probe（含 429 節流）→ status → disconnect，
// 全程走假 COROS 伺服器，驗證 token 刷新、secret 不落地、provider='coros' 既有資料不受影響。
func TestIntegration_FullFlow(t *testing.T) {
	pool := corosMcpSetupPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	ownerEmail := "corosmcp-owner-" + corosMcpRandSuffix() + "@example.com"
	otherEmail := "corosmcp-other-" + corosMcpRandSuffix() + "@example.com"
	ownerID := corosMcpCreateUser(t, ctx, pool, ownerEmail)
	otherID := corosMcpCreateUser(t, ctx, pool, otherEmail)
	corosMcpSetWhitelist(t, ctx, pool, ownerEmail)

	// 既有 provider='coros' 連線（模擬 Partner API 直連使用者）：全程不該被 coros_mcp 的任何動作碰到。
	if err := repo.Save(ctx, &Connection{UserID: ownerID, Provider: providerCoros, ProviderUserID: "coros-open-id", AccessToken: "a", RefreshToken: "r", ExpiresAt: time.Now().Add(24 * time.Hour)}); err != nil {
		t.Fatalf("seed existing coros connection: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_integrations WHERE user_id=$1 AND provider=$2`, ownerID, providerCoros)
	})

	fc := newFakeCorosMCPServer(t)
	doc := seedDiscovery(fc.srv.URL)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM coros_mcp_clients WHERE issuer=$1`, doc.Issuer)
	})

	h := NewCorosMcpHandler(repo, CorosMcpConfig{
		GatewayURL: fc.srv.URL, RedirectURI: "https://www.dor.tw/api/v1/integrations/coros-mcp/callback",
		FrontendURL: "https://www.dor.tw", JWTSecret: "test-secret",
	}, passthroughAuth, nil)
	corosMcpInProcessHTTP(h, fc)

	// --- 不在白名單 → 403 forbidden ---
	{
		req := withUser(httptest.NewRequest(http.MethodPost, "/connect", nil), otherID)
		rw := httptest.NewRecorder()
		h.Router().ServeHTTP(rw, req)
		if rw.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for non-whitelisted user, got %d: %s", rw.Code, rw.Body.String())
		}
	}

	// --- probe 未連接 → 409 not_connected ---
	{
		req := withUser(httptest.NewRequest(http.MethodPost, "/probe", nil), ownerID)
		rw := httptest.NewRecorder()
		h.Router().ServeHTTP(rw, req)
		if rw.Code != http.StatusConflict || !strings.Contains(rw.Body.String(), "not_connected") {
			t.Fatalf("expected 409 not_connected, got %d: %s", rw.Code, rw.Body.String())
		}
	}

	// --- POST /connect ---
	var authorizeURL string
	var nonceCookies []*http.Cookie
	{
		req := withUser(httptest.NewRequest(http.MethodPost, "/connect", nil), ownerID)
		rw := httptest.NewRecorder()
		h.Router().ServeHTTP(rw, req)
		if rw.Code != http.StatusOK {
			t.Fatalf("connect failed: %d %s", rw.Code, rw.Body.String())
		}
		var body struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode connect response: %v", err)
		}
		authorizeURL = body.URL
		nonceCookies = rw.Result().Cookies()
	}
	if _, reg, methods, _, _ := fc.snapshot(); reg != 1 || len(methods) != 1 || methods[0] != "none" {
		t.Fatalf("expected exactly 1 DCR registration requesting none, got %d %v", reg, methods)
	}

	parsed, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatal("authorize url missing state")
	}
	if !strings.HasPrefix(authorizeURL, doc.AuthorizationEndpoint) {
		t.Fatalf("authorize url should start with authorization_endpoint, got %s", authorizeURL)
	}

	// --- GET /callback（公開，不需白名單中介層）---
	{
		req := httptest.NewRequest(http.MethodGet, "/callback?code=test-code&state="+url.QueryEscape(state), nil)
		for _, c := range nonceCookies { // 瀏覽器會自動帶回 /connect 種的 nonce cookie
			req.AddCookie(c)
		}
		rw := httptest.NewRecorder()
		h.Router().ServeHTTP(rw, req)
		if rw.Code != http.StatusFound {
			t.Fatalf("expected 302 from callback, got %d: %s", rw.Code, rw.Body.String())
		}
		loc := rw.Header().Get("Location")
		if !strings.Contains(loc, "coros_mcp=connected") {
			t.Fatalf("expected redirect to coros_mcp=connected, got %s", loc)
		}
	}
	if _, _, _, hdrs, calls := fc.snapshot(); calls != 1 || hdrs[0] != "" {
		t.Fatalf("token exchange must be a public-client call without Authorization header, got calls=%d hdrs=%q", calls, hdrs)
	}

	// --- GET /status：已連接 ---
	{
		req := withUser(httptest.NewRequest(http.MethodGet, "/status", nil), ownerID)
		rw := httptest.NewRecorder()
		h.Router().ServeHTTP(rw, req)
		var body map[string]any
		_ = json.Unmarshal(rw.Body.Bytes(), &body)
		if body["connected"] != true {
			t.Fatalf("expected connected=true, got %v", body)
		}
		if body["issuer"] != doc.Issuer {
			t.Fatalf("expected issuer=%s, got %v", doc.Issuer, body["issuer"])
		}
	}

	// --- POST /probe：access token (expires_in=30) 應觸發 ensureFreshToken 主動刷新 ---
	var probeSteps []corosMcpStepSummary
	{
		req := withUser(httptest.NewRequest(http.MethodPost, "/probe", nil), ownerID)
		rw := httptest.NewRecorder()
		h.Router().ServeHTTP(rw, req)
		if rw.Code != http.StatusOK {
			t.Fatalf("probe failed: %d %s", rw.Code, rw.Body.String())
		}
		var body struct {
			At    string                `json:"at"`
			Steps []corosMcpStepSummary `json:"steps"`
		}
		if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode probe response: %v", err)
		}
		probeSteps = body.Steps
		if len(probeSteps) != 5 {
			t.Fatalf("expected 5 steps (tools/list + 4 tools), got %d: %+v", len(probeSteps), probeSteps)
		}
		wantCount := map[string]int{"queryDevices": 1, "querySportRecords": 2}
		for _, s := range probeSteps {
			if !s.OK {
				t.Fatalf("expected step %s to succeed, got %+v", s.Step, s)
			}
			if want, ok := wantCount[s.Step]; ok && (s.Count == nil || *s.Count != want) {
				t.Fatalf("step %s count = %v, want %d", s.Step, s.Count, want)
			}
		}
	}
	if last, _, _, hdrs, _ := fc.snapshot(); last != "Bearer tok-2" {
		t.Fatalf("expected probe's MCP calls to use refreshed token tok-2, got %q", last)
	} else {
		for i, hd := range hdrs {
			if hd != "" {
				t.Fatalf("token call %d must not send Authorization header, got %q", i, hd)
			}
		}
	}

	// --- 立刻再 probe 一次 → 429 rate_limited（記憶體節流，1 分鐘 1 次）---
	{
		req := withUser(httptest.NewRequest(http.MethodPost, "/probe", nil), ownerID)
		rw := httptest.NewRecorder()
		h.Router().ServeHTTP(rw, req)
		if rw.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 rate_limited, got %d: %s", rw.Code, rw.Body.String())
		}
		if !strings.Contains(rw.Body.String(), "rate_limited") {
			t.Fatalf("expected body to mention rate_limited, got %s", rw.Body.String())
		}
	}

	// --- GET /status：last_probe 摘要可讀回 ---
	{
		req := withUser(httptest.NewRequest(http.MethodGet, "/status", nil), ownerID)
		rw := httptest.NewRecorder()
		h.Router().ServeHTTP(rw, req)
		var body map[string]any
		_ = json.Unmarshal(rw.Body.Bytes(), &body)
		if body["last_probe_at"] == nil {
			t.Fatal("expected last_probe_at to be set after a successful probe")
		}
		lp, ok := body["last_probe"].(map[string]any)
		if !ok {
			t.Fatalf("expected last_probe object, got %v", body["last_probe"])
		}
		steps, ok := lp["steps"].([]any)
		if !ok || len(steps) != 5 {
			t.Fatalf("expected 5 steps in last_probe, got %v", lp["steps"])
		}
	}

	// --- secret／token 絕不落地（DB 層檢查）---
	{
		var rawResponse []byte
		if err := pool.QueryRow(ctx, `SELECT raw_response FROM coros_mcp_clients WHERE issuer=$1`, doc.Issuer).Scan(&rawResponse); err != nil {
			t.Fatalf("read coros_mcp_clients: %v", err)
		}
		if strings.Contains(strings.ToLower(string(rawResponse)), "secret") {
			t.Fatal("coros_mcp_clients.raw_response 不應包含任何 secret 欄位")
		}
		var secretCol *string
		var method string
		if err := pool.QueryRow(ctx, `SELECT client_secret, token_endpoint_auth_method FROM coros_mcp_clients WHERE issuer=$1`, doc.Issuer).Scan(&secretCol, &method); err != nil {
			t.Fatalf("read client row: %v", err)
		}
		// COROS 只登記 public client：不發 secret；存的方法必須是 COROS 實際登記的 none（不是我們要求的）
		if secretCol != nil || method != "none" {
			t.Fatalf("expected public client row (secret NULL, method none), got secret=%v method=%q", secretCol != nil, method)
		}

		rows, err := pool.Query(ctx, `SELECT response FROM coros_mcp_probe_logs WHERE user_id=$1`, ownerID)
		if err != nil {
			t.Fatalf("read probe_logs: %v", err)
		}
		defer rows.Close()
		n := 0
		for rows.Next() {
			n++
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				t.Fatalf("scan probe log: %v", err)
			}
			for _, secret := range []string{"tok-1", "tok-2", "rtok-1", "rtok-2"} {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("probe_logs.response 不應包含 token/secret 原文 (%s): %s", secret, raw)
				}
			}
		}
		if n != 5 {
			t.Fatalf("expected 5 probe_logs rows, got %d", n)
		}
	}

	// --- POST /disconnect：public client 的撤銷被 COROS 拒（revoked=false），本機連線照樣刪除、provider='coros' 不受影響 ---
	{
		req := withUser(httptest.NewRequest(http.MethodPost, "/disconnect", nil), ownerID)
		rw := httptest.NewRecorder()
		h.Router().ServeHTTP(rw, req)
		if rw.Code != http.StatusOK {
			t.Fatalf("disconnect failed: %d %s", rw.Code, rw.Body.String())
		}
		var body struct {
			OK      bool `json:"ok"`
			Revoked bool `json:"revoked"`
		}
		_ = json.Unmarshal(rw.Body.Bytes(), &body)
		if !body.OK || body.Revoked {
			t.Fatalf("expected ok=true revoked=false (COROS rejects public-client revoke), got %+v", body)
		}
	}
	conn, err := h.getConnection(ctx, ownerID)
	if err != nil {
		t.Fatalf("getConnection after disconnect: %v", err)
	}
	if conn != nil {
		t.Fatal("expected coros_mcp connection to be deleted after disconnect")
	}
	otherConn, err := repo.GetByUser(ctx, ownerID, providerCoros)
	if err != nil || otherConn == nil {
		t.Fatalf("existing provider='coros' connection must survive coros_mcp disconnect, got conn=%v err=%v", otherConn, err)
	}

	// --- GET /status：已中斷 ---
	{
		req := withUser(httptest.NewRequest(http.MethodGet, "/status", nil), ownerID)
		rw := httptest.NewRecorder()
		h.Router().ServeHTTP(rw, req)
		var body map[string]any
		_ = json.Unmarshal(rw.Body.Bytes(), &body)
		if body["connected"] != false {
			t.Fatalf("expected connected=false after disconnect, got %v", body)
		}
	}
}

// TestIntegration_EnsureClient_AdvisoryLockDedupesConcurrentDCR：同一 issuer 併發呼叫 ensureClient，
// advisory lock 應確保 DCR 只真正送出一次請求、coros_mcp_clients 只留一列（契約第 3 點）。
func TestIntegration_EnsureClient_AdvisoryLockDedupesConcurrentDCR(t *testing.T) {
	pool := corosMcpSetupPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	fc := newFakeCorosMCPServer(t)
	doc := seedDiscovery(fc.srv.URL + "/lock-test") // 獨立 issuer 字串，避免撞到其他測試的 cache/DB 列
	doc.RegistrationEndpoint = fc.srv.URL + "/connect/register"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM coros_mcp_clients WHERE issuer=$1`, doc.Issuer)
	})

	h := NewCorosMcpHandler(repo, CorosMcpConfig{GatewayURL: fc.srv.URL, RedirectURI: "https://www.dor.tw/x", FrontendURL: "https://www.dor.tw", JWTSecret: "s"}, passthroughAuth, nil)
	corosMcpInProcessHTTP(h, fc)

	const n = 5
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := h.ensureClient(ctx, doc.Issuer, doc)
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("ensureClient[%d] failed: %v", i, err)
		}
	}
	if _, reg, _, _, _ := fc.snapshot(); reg != 1 {
		t.Fatalf("expected exactly 1 DCR registration despite %d concurrent callers, got %d", n, reg)
	}
	var rowCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM coros_mcp_clients WHERE issuer=$1`, doc.Issuer).Scan(&rowCount); err != nil {
		t.Fatalf("count coros_mcp_clients: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("expected exactly 1 coros_mcp_clients row, got %d", rowCount)
	}
}

// corosMcpConnectAndCallback：POST /connect 拿 authorize URL → 取 state → 模擬 COROS 帶 code 導回 /callback，
// 回傳 callback 的 302 Location。
func corosMcpConnectAndCallback(t *testing.T, h *CorosMcpHandler, userID string) string {
	t.Helper()
	req := withUser(httptest.NewRequest(http.MethodPost, "/connect", nil), userID)
	rw := httptest.NewRecorder()
	h.Router().ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("connect failed: %d %s", rw.Code, rw.Body.String())
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode connect response: %v", err)
	}
	parsed, err := url.Parse(body.URL)
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	state := parsed.Query().Get("state")
	creq := httptest.NewRequest(http.MethodGet, "/callback?code=test-code&state="+url.QueryEscape(state), nil)
	for _, c := range rw.Result().Cookies() { // 瀏覽器自動帶回 /connect 種的 nonce cookie
		creq.AddCookie(c)
	}
	crw := httptest.NewRecorder()
	h.Router().ServeHTTP(crw, creq)
	if crw.Code != http.StatusFound {
		t.Fatalf("expected 302 from callback, got %d: %s", crw.Code, crw.Body.String())
	}
	return crw.Header().Get("Location")
}

// TestIntegration_LegacyClientRow_ConnectsWithPublicAuth：重現 2026-10-01 正式站狀態——coros_mcp_clients 已有一列
// token_endpoint_auth_method='client_secret_basic'、client_secret=NULL（v868 誤存「要求的」方法）。修正後同一列不需改
// 資料庫：不再重新註冊、換 token 不送 Authorization 標頭 → 連線成功（v868 在這裡得到 token_exchange_failed）。
func TestIntegration_LegacyClientRow_ConnectsWithPublicAuth(t *testing.T) {
	pool := corosMcpSetupPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	ownerEmail := "corosmcp-legacy-" + corosMcpRandSuffix() + "@example.com"
	ownerID := corosMcpCreateUser(t, ctx, pool, ownerEmail)
	corosMcpSetWhitelist(t, ctx, pool, ownerEmail)

	fc := newFakeCorosMCPServer(t)
	issuer := fc.srv.URL + "/legacy" // 獨立 issuer，避免撞到其他測試；端點仍指向假伺服器根路徑
	doc := &corosMcpDiscoveryDoc{
		Issuer:                issuer,
		AuthorizationEndpoint: fc.srv.URL + "/oauth2/authorize",
		TokenEndpoint:         fc.srv.URL + "/oauth2/token",
		RevocationEndpoint:    fc.srv.URL + "/oauth2/revoke",
		RegistrationEndpoint:  fc.srv.URL + "/connect/register",
	}
	corosMcpDiscoveryCacheSet(issuer, doc)
	if _, err := pool.Exec(ctx, `
		INSERT INTO coros_mcp_clients (issuer, client_id, client_secret, token_endpoint_auth_method, raw_response)
		VALUES ($1, 'fake-client-id', NULL, 'client_secret_basic', '{"token_endpoint_auth_method":"none"}'::jsonb)`, issuer); err != nil {
		t.Fatalf("seed legacy client row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM coros_mcp_clients WHERE issuer=$1`, issuer)
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_integrations WHERE user_id=$1 AND provider=$2`, ownerID, providerCorosMcp)
	})

	h := NewCorosMcpHandler(repo, CorosMcpConfig{
		GatewayURL: issuer, RedirectURI: "https://www.dor.tw/api/v1/integrations/coros-mcp/callback",
		FrontendURL: "https://www.dor.tw", JWTSecret: "test-secret",
	}, passthroughAuth, nil)
	corosMcpInProcessHTTP(h, fc)

	loc := corosMcpConnectAndCallback(t, h, ownerID)
	if !strings.Contains(loc, "coros_mcp=connected") {
		t.Fatalf("legacy client row must now connect, got redirect %s", loc)
	}
	_, reg, _, hdrs, calls := fc.snapshot()
	if reg != 0 {
		t.Fatalf("existing client row must be reused (no new DCR), got %d registrations", reg)
	}
	if calls != 1 || hdrs[0] != "" {
		t.Fatalf("token exchange must not send Authorization header, got calls=%d hdrs=%q", calls, hdrs)
	}
	conn, err := h.getConnection(ctx, ownerID)
	if err != nil || conn == nil || conn.AccessToken != "tok-1" || conn.Issuer != issuer {
		t.Fatalf("expected stored coros_mcp connection with tok-1, got conn=%+v err=%v", conn, err)
	}
}

// TestIntegration_NoRefreshToken_ReconnectRequired：COROS 不發 refresh token（Spring AS 對 public client 的預設）時，
// 連線仍成功、讀取測試可用；access token 過期後讀取測試回 409 reconnect_required（不打 COROS token 端點），
// 中斷連線仍會刪除本機連線。
func TestIntegration_NoRefreshToken_ReconnectRequired(t *testing.T) {
	pool := corosMcpSetupPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	ownerEmail := "corosmcp-norefresh-" + corosMcpRandSuffix() + "@example.com"
	ownerID := corosMcpCreateUser(t, ctx, pool, ownerEmail)
	corosMcpSetWhitelist(t, ctx, pool, ownerEmail)

	fc := newFakeCorosMCPServer(t, fakeCorosOpts{issueRefresh: false, firstExpiresIn: 3600})
	doc := seedDiscovery(fc.srv.URL)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM coros_mcp_clients WHERE issuer=$1`, doc.Issuer)
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_integrations WHERE user_id=$1 AND provider=$2`, ownerID, providerCorosMcp)
	})
	cfg := CorosMcpConfig{
		GatewayURL: fc.srv.URL, RedirectURI: "https://www.dor.tw/api/v1/integrations/coros-mcp/callback",
		FrontendURL: "https://www.dor.tw", JWTSecret: "test-secret",
	}
	h := NewCorosMcpHandler(repo, cfg, passthroughAuth, nil)
	corosMcpInProcessHTTP(h, fc)

	if loc := corosMcpConnectAndCallback(t, h, ownerID); !strings.Contains(loc, "coros_mcp=connected") {
		t.Fatalf("missing refresh_token must not fail the connection, got %s", loc)
	}

	// 讀取測試：token 仍有效 → 成功，MCP 用 tok-1
	{
		req := withUser(httptest.NewRequest(http.MethodPost, "/probe", nil), ownerID)
		rw := httptest.NewRecorder()
		h.Router().ServeHTTP(rw, req)
		if rw.Code != http.StatusOK {
			t.Fatalf("probe failed: %d %s", rw.Code, rw.Body.String())
		}
		if last, _, _, _, _ := fc.snapshot(); last != "Bearer tok-1" {
			t.Fatalf("expected MCP calls with tok-1, got %q", last)
		}
	}

	// access token 過期 → 新的 handler（重置每分鐘節流）再讀取測試 → 409 reconnect_required，且不打 token 端點
	if _, err := pool.Exec(ctx, `UPDATE user_integrations SET expires_at = NOW() - INTERVAL '1 minute' WHERE user_id=$1 AND provider=$2`, ownerID, providerCorosMcp); err != nil {
		t.Fatalf("expire token: %v", err)
	}
	_, _, _, _, callsBefore := fc.snapshot()
	h2 := NewCorosMcpHandler(repo, cfg, passthroughAuth, nil)
	corosMcpInProcessHTTP(h2, fc)
	{
		req := withUser(httptest.NewRequest(http.MethodPost, "/probe", nil), ownerID)
		rw := httptest.NewRecorder()
		h2.Router().ServeHTTP(rw, req)
		if rw.Code != http.StatusConflict || !strings.Contains(rw.Body.String(), "reconnect_required") {
			t.Fatalf("expected 409 reconnect_required, got %d: %s", rw.Code, rw.Body.String())
		}
	}
	if _, _, _, _, callsAfter := fc.snapshot(); callsAfter != callsBefore {
		t.Fatalf("no refresh token → must not call the token endpoint, calls %d → %d", callsBefore, callsAfter)
	}

	// 中斷連線：revoke 被拒（public client）→ revoked=false，但本機連線仍刪除
	{
		req := withUser(httptest.NewRequest(http.MethodPost, "/disconnect", nil), ownerID)
		rw := httptest.NewRecorder()
		h2.Router().ServeHTTP(rw, req)
		if rw.Code != http.StatusOK || !strings.Contains(rw.Body.String(), `"revoked":false`) {
			t.Fatalf("expected 200 revoked=false, got %d: %s", rw.Code, rw.Body.String())
		}
	}
	if conn, err := h2.getConnection(ctx, ownerID); err != nil || conn != nil {
		t.Fatalf("connection must be deleted after disconnect, got conn=%v err=%v", conn, err)
	}
}

// ===== 第二階段（v871）：匯入／去重／EXP／自動同步／中斷／CSRF =====
//
// 全程假 COROS（corosMcpInProcessHTTP，不走本機 TCP）＋真實 Postgres（Neon 暫時分支，需已套用 migration 197）。
// labelId 每次執行都產生新的 18 位數，避免 external_award_ledger（不隨使用者刪除而清）擋掉第二次執行的 EXP 斷言。

type mcpImportEnv struct {
	t     *testing.T
	ctx   context.Context
	pool  *pgxpool.Pool
	repo  *Repository
	fc    *fakeCoros
	h     *CorosMcpHandler
	users []string
}

func newMcpImportEnv(t *testing.T, nUsers int) *mcpImportEnv {
	t.Helper()
	pool := corosMcpSetupPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	var emails, ids []string
	for i := 0; i < nUsers; i++ {
		email := fmt.Sprintf("corosmcp-imp%d-%s@example.com", i, corosMcpRandSuffix())
		emails = append(emails, email)
		ids = append(ids, corosMcpCreateUser(t, ctx, pool, email))
	}
	corosMcpSetWhitelist(t, ctx, pool, strings.Join(emails, ","))

	fc := newFakeCorosMCPServer(t, fakeCorosOpts{issueRefresh: true, firstExpiresIn: 3600})
	doc := seedDiscovery(fc.srv.URL)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM coros_mcp_clients WHERE issuer=$1`, doc.Issuer)
		for _, id := range ids {
			_, _ = pool.Exec(bg, `DELETE FROM activities WHERE user_id=$1`, id)
			_, _ = pool.Exec(bg, `DELETE FROM external_award_ledger WHERE user_id=$1`, id)
			_, _ = pool.Exec(bg, `DELETE FROM user_integrations WHERE user_id=$1`, id)
		}
	})
	h := NewCorosMcpHandler(repo, CorosMcpConfig{
		GatewayURL: fc.srv.URL, RedirectURI: "https://www.dor.tw/api/v1/integrations/coros-mcp/callback",
		FrontendURL: "https://www.dor.tw", JWTSecret: "test-secret",
	}, passthroughAuth, nil)
	corosMcpInProcessHTTP(h, fc)
	return &mcpImportEnv{t: t, ctx: ctx, pool: pool, repo: repo, fc: fc, h: h, users: ids}
}

// connectUser 走完整 connect→callback（含 CSRF cookie），再把連線 floor（created_at）改成指定時間。
func (e *mcpImportEnv) connectUser(idx int, floor time.Time) string {
	e.t.Helper()
	uid := e.users[idx]
	if loc := corosMcpConnectAndCallback(e.t, e.h, uid); !strings.Contains(loc, "coros_mcp=connected") {
		e.t.Fatalf("connect failed: %s", loc)
	}
	if _, err := e.pool.Exec(e.ctx, `UPDATE user_integrations SET created_at=$1 WHERE user_id=$2 AND provider=$3`, floor, uid, providerCorosMcp); err != nil {
		e.t.Fatalf("set floor: %v", err)
	}
	return uid
}

func (e *mcpImportEnv) do(method, path, uid string) *httptest.ResponseRecorder {
	rw := httptest.NewRecorder()
	e.h.Router().ServeHTTP(rw, withUser(httptest.NewRequest(method, path, nil), uid))
	return rw
}

func (e *mcpImportEnv) resetThrottle() {
	e.h.importMu.Lock()
	e.h.importLast = nil
	e.h.importMu.Unlock()
}

type mcpActRow struct {
	Source, ExtID string
	Device        *string
	Km            float64
	DurS, Pace    int
	HR, Elapsed   *int
	Fingerprint   string
	Flagged       bool
	Reason        *string
	ExpAwarded    bool
	DupOf         *string
	ID            string
}

func (e *mcpImportEnv) activities(uid string) map[string]mcpActRow {
	e.t.Helper()
	rows, err := e.pool.Query(e.ctx, `
		SELECT id::text, COALESCE(source,''), COALESCE(external_id,''), device_name, distance_km::float8, duration_s, avg_pace_s,
		       avg_hr, elapsed_s, COALESCE(fingerprint,''), flagged, flag_reason, exp_awarded, dup_of::text
		FROM activities WHERE user_id=$1`, uid)
	if err != nil {
		e.t.Fatalf("query activities: %v", err)
	}
	defer rows.Close()
	out := map[string]mcpActRow{}
	for rows.Next() {
		var r mcpActRow
		if err := rows.Scan(&r.ID, &r.Source, &r.ExtID, &r.Device, &r.Km, &r.DurS, &r.Pace, &r.HR, &r.Elapsed, &r.Fingerprint, &r.Flagged, &r.Reason, &r.ExpAwarded, &r.DupOf); err != nil {
			e.t.Fatalf("scan: %v", err)
		}
		out[r.ExtID] = r
	}
	return out
}

func (e *mcpImportEnv) totalKm(uid string) float64 {
	e.t.Helper()
	var v float64
	if err := e.pool.QueryRow(e.ctx, `SELECT total_km::float8 FROM users WHERE id=$1::uuid`, uid).Scan(&v); err != nil {
		e.t.Fatalf("total_km: %v", err)
	}
	return v
}

func decodeJSONMap(t *testing.T, rw *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rw.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode json %q: %v", rw.Body.String(), err)
	}
	return m
}

func jnum(m map[string]any, k string) int { f, _ := m[k].(float64); return int(f) }

// mcpTestLabel：每次執行唯一的 18 位數 labelId（"48"＋16 位）。
func mcpTestLabel(i int) string {
	return fmt.Sprintf("48%016d", time.Now().UnixNano()%1_000_000_000_000_000+int64(i))
}

// mcpFloorNoon：台灣 N 天前正午——選正午讓「floor 前一小時」一定落在同一個日期、會被分段查詢抓到。
func mcpFloorNoon(daysAgo int) time.Time {
	d := time.Now().In(corosMcpTZ).AddDate(0, 0, -daysAgo)
	return time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, corosMcpTZ)
}

func TestIntegration_Stage2_ManualImport(t *testing.T) {
	e := newMcpImportEnv(t, 2)
	floor := mcpFloorNoon(20)
	now := time.Now().Truncate(time.Second)
	lb := func(i int) string { return mcpTestLabel(i) }
	run1L, run2L, walkL, cycL, oldL := lb(1), lb(2), lb(3), lb(4), lb(5)
	_ = run2L
	e.fc.setRecords([]corosFixtureRec{
		{Title: "Outdoor Run", Start: now.AddDate(0, 0, -5).Unix(), ElapsedS: 2494, DurationS: 2433, Distance: "5.31 km", HR: 133, LabelID: run1L, Sport: 100},
		{Title: "Treadmill Run", Start: now.AddDate(0, 0, -2).Unix(), ElapsedS: 3735, DurationS: 3735, Distance: "10.00 km", HR: 150, LabelID: run2L, Sport: 101},
		{Title: "Walk", Start: now.AddDate(0, 0, -3).Unix(), ElapsedS: 800, DurationS: 720, Distance: "850 m", LabelID: walkL, Sport: 900},
		{Title: "Road Bike", Start: now.AddDate(0, 0, -4).Unix(), ElapsedS: 3600, DurationS: 3600, Distance: "20.00 km", HR: 120, LabelID: cycL, Sport: 200},
		{Title: "Outdoor Run (before connect)", Start: floor.Add(-time.Hour).Unix(), ElapsedS: 1800, DurationS: 1800, Distance: "4.00 km", HR: 140, LabelID: oldL, Sport: 100},
	})
	uid := e.connectUser(0, floor)

	// 未連接的使用者（白名單內但沒有 coros_mcp 連線）→ 409 not_connected
	if rw := e.do(http.MethodPost, "/import", e.users[1]); rw.Code != http.StatusConflict || !strings.Contains(rw.Body.String(), "not_connected") {
		t.Fatalf("not connected → 409 not_connected, got %d %s", rw.Code, rw.Body.String())
	}

	rw := e.do(http.MethodPost, "/import", uid)
	if rw.Code != http.StatusOK {
		t.Fatalf("import failed: %d %s", rw.Code, rw.Body.String())
	}
	m := decodeJSONMap(t, rw)
	want := map[string]int{"days": 30, "fetched": 5, "imported": 3, "duplicate": 0, "exists": 0,
		"skipped_before_connect": 1, "skipped_non_running": 1, "skipped_invalid": 0, "errors": 0}
	for k, v := range want {
		if jnum(m, k) != v {
			t.Fatalf("response %s = %d, want %d (full: %v)", k, jnum(m, k), v, m)
		}
	}
	if m["device_name"] != "COROS PACE 4" {
		t.Fatalf("device_name = %v", m["device_name"])
	}
	// from 夾到 floor：floor 在 20 天前 → 21 個日曆日 → 3 段（每段 ≤10 天）
	if n := e.fc.calls("querySportRecords"); n != 3 {
		t.Fatalf("expected 3 chunked querySportRecords calls, got %d", n)
	}
	if n := e.fc.calls("queryDevices"); n != 1 {
		t.Fatalf("expected queryDevices once per sync, got %d", n)
	}

	acts := e.activities(uid)
	if len(acts) != 3 {
		t.Fatalf("expected 3 imported rows (2 runs + walk), got %d: %+v", len(acts), acts)
	}
	r1, ok := acts["mcp:"+run1L]
	if !ok {
		t.Fatalf("run1 missing, rows=%v", acts)
	}
	if r1.Source != "coros" || r1.Device == nil || *r1.Device != "COROS PACE 4" || r1.Km != 5.31 || r1.DurS != 2433 || r1.Pace != 458 {
		t.Fatalf("run1 wrong: %+v", r1)
	}
	if r1.HR == nil || *r1.HR != 133 || r1.Elapsed == nil || *r1.Elapsed != 2494 {
		t.Fatalf("run1 hr/elapsed wrong: %+v", r1)
	}
	if want := fingerprintOf(now.AddDate(0, 0, -5).Unix(), 5310, 2433); r1.Fingerprint != want {
		t.Fatalf("run1 fingerprint mismatch")
	}
	if w := acts["mcp:"+walkL]; w.Km != 0.85 || w.HR != nil {
		t.Fatalf("walk wrong: %+v", w)
	}
	if _, ok := acts["mcp:"+cycL]; ok {
		t.Fatal("cycling must not be imported")
	}
	if _, ok := acts["mcp:"+oldL]; ok {
		t.Fatal("record before the connect floor must not be imported")
	}

	// AwardMileageExp 生效：三筆都標 exp_awarded，users.total_km = 5.31 + 10.00 + 0.85
	for k, r := range acts {
		if !r.ExpAwarded || r.Flagged {
			t.Fatalf("%s must be exp_awarded and unflagged: %+v", k, r)
		}
	}
	if got := e.totalKm(uid); got < 16.159 || got > 16.161 {
		t.Fatalf("total_km = %v, want ≈16.16", got)
	}

	// /status：last_synced_at 已寫入、device_name 來自最新匯入活動
	st := decodeJSONMap(t, e.do(http.MethodGet, "/status", uid))
	if st["last_synced_at"] == nil || st["device_name"] != "COROS PACE 4" {
		t.Fatalf("status after sync wrong: %v", st)
	}
	// ListActivities 回傳 device_name
	list, err := e.repo.ListActivities(e.ctx, uid, 50)
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	withDev := 0
	for _, a := range list {
		if a.DeviceName != nil && *a.DeviceName == "COROS PACE 4" {
			withDev++
		}
	}
	if withDev != 3 {
		t.Fatalf("ListActivities should expose device_name on 3 rows, got %d", withDev)
	}

	// 立刻再匯入 → 429 rate_limited（60 秒節流）
	rw = e.do(http.MethodPost, "/import", uid)
	if rw.Code != http.StatusTooManyRequests || !strings.Contains(rw.Body.String(), "rate_limited") {
		t.Fatalf("expected 429 rate_limited, got %d %s", rw.Code, rw.Body.String())
	}
	// 解除節流後重跑 → 全部 exists、不重複計 km
	e.resetThrottle()
	m = decodeJSONMap(t, e.do(http.MethodPost, "/import?days=7", uid))
	if jnum(m, "imported") != 0 || jnum(m, "exists") != 3 || jnum(m, "days") != 7 {
		t.Fatalf("re-import must be idempotent (days=7), got %v", m)
	}
	if got := e.totalKm(uid); got < 16.159 || got > 16.161 {
		t.Fatalf("total_km must not change on re-import, got %v", got)
	}
}

func TestIntegration_Stage2_ImportErrorStatuses(t *testing.T) {
	e := newMcpImportEnv(t, 1)
	e.fc.setRecords([]corosFixtureRec{})
	uid := e.connectUser(0, mcpFloorNoon(20))

	// anomaly → 502，且不寫任何活動、不更新 last_synced_at
	e.fc.setAnomaly(true)
	rw := e.do(http.MethodPost, "/import", uid)
	if rw.Code != http.StatusBadGateway {
		t.Fatalf("anomaly → 502, got %d %s", rw.Code, rw.Body.String())
	}
	if n := len(e.activities(uid)); n != 0 {
		t.Fatalf("anomaly sync must write nothing, got %d rows", n)
	}
	if st := decodeJSONMap(t, e.do(http.MethodGet, "/status", uid)); st["last_synced_at"] != nil {
		t.Fatalf("failed sync must not set last_synced_at: %v", st)
	}

	// token 過期且無 refresh token → 409 reconnect_required
	e.fc.setAnomaly(false)
	e.resetThrottle()
	if _, err := e.pool.Exec(e.ctx, `UPDATE user_integrations SET expires_at=NOW()-INTERVAL '1 minute', refresh_token=$1 WHERE user_id=$2 AND provider=$3`, encryptToken(""), uid, providerCorosMcp); err != nil {
		t.Fatalf("expire token: %v", err)
	}
	if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusConflict || !strings.Contains(rw.Body.String(), "reconnect_required") {
		t.Fatalf("expired token without refresh → 409 reconnect_required, got %d %s", rw.Code, rw.Body.String())
	}

	// 非白名單 → 403
	nonWL := corosMcpCreateUser(t, e.ctx, e.pool, "corosmcp-nonwl-"+corosMcpRandSuffix()+"@example.com")
	if rw := e.do(http.MethodPost, "/import", nonWL); rw.Code != http.StatusForbidden {
		t.Fatalf("non-whitelisted → 403, got %d", rw.Code)
	}
}

func TestIntegration_Stage2_OverlappingTerraRowIsMultiDeviceDuplicate(t *testing.T) {
	e := newMcpImportEnv(t, 1)
	now := time.Now().Truncate(time.Second)
	start := now.AddDate(0, 0, -2)
	label := mcpTestLabel(1)
	e.fc.setRecords([]corosFixtureRec{
		{Title: "Outdoor Run", Start: start.Unix(), ElapsedS: 2494, DurationS: 2433, Distance: "5.31 km", HR: 133, LabelID: label, Sport: 100},
	})
	uid := e.connectUser(0, mcpFloorNoon(20))

	// 先種一筆重疊的 Terra COROS 列（source='coros'、external_id=Terra summary_id 風格），並讓它先拿到 EXP（5.30 km）
	terraNA := &NormalizedActivity{UserID: uid, Source: "coros", ExternalID: "terra-" + corosMcpRandSuffix(), Fingerprint: fingerprintOf(start.Unix()-10, 5300, 2430),
		DistanceKm: 5.30, DurationS: 2430, AvgPaceS: 459, RecordedAt: start.Add(-10 * time.Second).UTC()}
	res, err := e.repo.ImportActivity(e.ctx, terraNA)
	if err != nil || res.Status != "inserted" {
		t.Fatalf("seed terra row: %+v %v", res, err)
	}
	if err := e.repo.AwardMileageExp(e.ctx, res.ID, uid); err != nil {
		t.Fatalf("award terra row: %v", err)
	}
	if got := e.totalKm(uid); got < 5.299 || got > 5.301 {
		t.Fatalf("setup total_km = %v, want 5.30", got)
	}

	m := decodeJSONMap(t, e.do(http.MethodPost, "/import", uid))
	if jnum(m, "duplicate") != 1 || jnum(m, "imported") != 0 || jnum(m, "errors") != 0 {
		t.Fatalf("overlap with Terra row must be a duplicate, got %v", m)
	}
	acts := e.activities(uid)
	mcp, ok := acts["mcp:"+label]
	if !ok {
		t.Fatalf("MCP row must still be written (flagged), rows=%v", acts)
	}
	if !mcp.Flagged || mcp.Reason == nil || *mcp.Reason != "multi_device_duplicate" || mcp.DupOf == nil || *mcp.DupOf != res.ID {
		t.Fatalf("MCP row must be multi_device_duplicate pointing at the Terra row: %+v", mcp)
	}
	if mcp.Device == nil || *mcp.Device != "COROS PACE 4" {
		t.Fatalf("device_name should still be stored on the flagged row: %+v", mcp)
	}
	// 重疊只補差額：5.31 − 5.30 = 0.01，不是再加 5.31
	if !mcp.ExpAwarded {
		t.Fatal("overlap row must be marked exp_awarded after delta compensation")
	}
	if got := e.totalKm(uid); got < 5.309 || got > 5.311 {
		t.Fatalf("total_km = %v, want ≈5.31 (delta only)", got)
	}
}

func TestIntegration_Stage2_AutoSyncOncePerWindow(t *testing.T) {
	e := newMcpImportEnv(t, 2)
	now := time.Now().Truncate(time.Second)
	label := mcpTestLabel(1)
	e.fc.setRecords([]corosFixtureRec{
		{Title: "Outdoor Run", Start: now.AddDate(0, 0, -1).Unix(), ElapsedS: 2494, DurationS: 2433, Distance: "5.31 km", HR: 133, LabelID: label, Sport: 100},
	})
	uid := e.connectUser(0, mcpFloorNoon(20))

	ran, res, err := e.h.autoSyncOnce(e.ctx, uid)
	if err != nil || !ran || res.Imported != 1 {
		t.Fatalf("first autosync must import, got ran=%v res=%+v err=%v", ran, res, err)
	}
	// 從沒同步過：起點＝now−30 天、被連線 floor（20 天前）夾住 → 依 10 天分段查（≥1 段）
	if e.fc.calls("querySportRecords") < 1 || e.fc.calls("queryDevices") != 1 {
		t.Fatalf("expected 1 devices + ≥1 records call, got %d/%d", e.fc.calls("queryDevices"), e.fc.calls("querySportRecords"))
	}
	before := e.fc.calls("querySportRecords") + e.fc.calls("queryDevices")
	ran, _, err = e.h.autoSyncOnce(e.ctx, uid)
	if err != nil || ran {
		t.Fatalf("second autosync within the window must not run, got ran=%v err=%v", ran, err)
	}
	if after := e.fc.calls("querySportRecords") + e.fc.calls("queryDevices"); after != before {
		t.Fatalf("second autosync must not hit COROS (calls %d → %d)", before, after)
	}
	if _, ok := e.activities(uid)["mcp:"+label]; !ok {
		t.Fatal("autosync row missing")
	}

	// 名額過期後再同步：已有 last_synced_at（剛成功同步）→ 只回看 3 天＝1 段 querySportRecords
	e.h.autoMu.Lock()
	e.h.autoLast = nil
	e.h.autoMu.Unlock()
	recBefore := e.fc.calls("querySportRecords")
	if ran, _, err = e.h.autoSyncOnce(e.ctx, uid); err != nil || !ran {
		t.Fatalf("autosync after window must run, got ran=%v err=%v", ran, err)
	}
	if got := e.fc.calls("querySportRecords") - recBefore; got != 1 {
		t.Fatalf("after a successful sync the window is 3 days → exactly 1 records call, got %d", got)
	}
	before = e.fc.calls("querySportRecords") + e.fc.calls("queryDevices")

	// 未連接的使用者：搶到名額但不打 COROS
	ran, _, err = e.h.autoSyncOnce(e.ctx, e.users[1])
	if err != nil || ran {
		t.Fatalf("not-connected user: ran=%v err=%v", ran, err)
	}
	if after := e.fc.calls("querySportRecords") + e.fc.calls("queryDevices"); after != before {
		t.Fatal("not-connected autosync must not call COROS")
	}
}

func TestIntegration_Stage2_DisconnectDeletesOnlyMcpRows(t *testing.T) {
	e := newMcpImportEnv(t, 2)
	now := time.Now().Truncate(time.Second)
	startA := now.AddDate(0, 0, -2)
	labelA := mcpTestLabel(1)
	e.fc.setRecords([]corosFixtureRec{
		{Title: "Outdoor Run", Start: startA.Unix(), ElapsedS: 2494, DurationS: 2433, Distance: "5.31 km", HR: 133, LabelID: labelA, Sport: 100},
	})
	uidA := e.connectUser(0, mcpFloorNoon(20))
	uidB := e.connectUser(1, mcpFloorNoon(20))
	for _, uid := range []string{uidA, uidB} {
		e.resetThrottle()
		if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusOK {
			t.Fatalf("import: %d %s", rw.Code, rw.Body.String())
		}
	}

	// 使用者 A 另有 Terra 直連（provider='coros'）：一筆與 MCP 列重疊的 Terra 活動
	// （先到先贏→Terra 列被標重複、dup_of 指向 MCP 列；MCP 列刪除後必須還原成正常）
	if err := e.repo.Save(e.ctx, &Connection{UserID: uidA, Provider: providerCoros, ProviderUserID: "coros-open-id", AccessToken: "a", RefreshToken: "r", ExpiresAt: time.Now().Add(24 * time.Hour)}); err != nil {
		t.Fatalf("seed terra connection: %v", err)
	}
	terraExt := "terra-" + corosMcpRandSuffix()
	tres, err := e.repo.ImportActivity(e.ctx, &NormalizedActivity{UserID: uidA, Source: "coros", ExternalID: terraExt,
		Fingerprint: fingerprintOf(startA.Unix()-5, 5300, 2430), DistanceKm: 5.30, DurationS: 2430, AvgPaceS: 459, RecordedAt: startA.Add(-5 * time.Second).UTC()})
	if err != nil || tres.Status != "duplicate" || tres.Reason != "multi_device_duplicate" {
		t.Fatalf("seed overlapping terra row should be a multi_device_duplicate of the MCP row, got %+v %v", tres, err)
	}
	// 偏好來源都設成 coros
	for _, uid := range []string{uidA, uidB} {
		if _, err := e.pool.Exec(e.ctx, `INSERT INTO user_profiles (user_id, preferred_data_source, updated_at) VALUES ($1,'coros',NOW())
			ON CONFLICT (user_id) DO UPDATE SET preferred_data_source='coros'`, uid); err != nil {
			t.Fatalf("set preferred: %v", err)
		}
	}
	pref := func(uid string) string {
		var p string
		_ = e.pool.QueryRow(e.ctx, `SELECT COALESCE(preferred_data_source,'') FROM user_profiles WHERE user_id=$1`, uid).Scan(&p)
		return p
	}

	// --- A 中斷：只刪 mcp: 列；Terra 列保留且被還原（不再是重複）；仍有 provider='coros' 連線 → 偏好來源不動 ---
	rw := e.do(http.MethodPost, "/disconnect", uidA)
	if rw.Code != http.StatusOK {
		t.Fatalf("disconnect A: %d %s", rw.Code, rw.Body.String())
	}
	if jnum(decodeJSONMap(t, rw), "deleted_activities") != 1 {
		t.Fatalf("expected 1 deleted MCP activity, got %s", rw.Body.String())
	}
	actsA := e.activities(uidA)
	if _, ok := actsA["mcp:"+labelA]; ok {
		t.Fatal("MCP row must be deleted on disconnect")
	}
	tr, ok := actsA[terraExt]
	if !ok {
		t.Fatalf("Terra row must survive, rows=%v", actsA)
	}
	if tr.Flagged || tr.Reason != nil || tr.DupOf != nil {
		t.Fatalf("Terra row duplicate flag pointing at the deleted MCP row must be cleared: %+v", tr)
	}
	if conn, _ := e.h.getConnection(e.ctx, uidA); conn != nil {
		t.Fatal("MCP connection must be deleted")
	}
	if c, _ := e.repo.GetByUser(e.ctx, uidA, providerCoros); c == nil {
		t.Fatal("Terra/Partner provider='coros' connection must survive")
	}
	if p := pref(uidA); p != "coros" {
		t.Fatalf("A still has a coros connection → preferred stays coros, got %q", p)
	}

	// --- B 中斷：沒有其他 coros 連線 → 偏好來源重設 gps，MCP 列全刪 ---
	if rw := e.do(http.MethodPost, "/disconnect", uidB); rw.Code != http.StatusOK {
		t.Fatalf("disconnect B: %d %s", rw.Code, rw.Body.String())
	}
	if n := len(e.activities(uidB)); n != 0 {
		t.Fatalf("B MCP rows must be gone, got %d", n)
	}
	if p := pref(uidB); p != "gps" {
		t.Fatalf("B has no coros connection left → preferred reset to gps, got %q", p)
	}
}

func TestIntegration_Stage2_ConnectCallbackCSRFCookie(t *testing.T) {
	e := newMcpImportEnv(t, 1)
	uid := e.users[0]

	// /connect 種 nonce cookie（屬性符合契約）
	rw := e.do(http.MethodPost, "/connect", uid)
	if rw.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", rw.Code, rw.Body.String())
	}
	var nonce *http.Cookie
	for _, c := range rw.Result().Cookies() {
		if c.Name == "dor_cmcp_n" {
			nonce = c
		}
	}
	if nonce == nil {
		t.Fatal("/connect must set dor_cmcp_n cookie")
	}
	if nonce.Path != "/api/v1/integrations/coros-mcp/callback" || !nonce.HttpOnly || !nonce.Secure || nonce.SameSite != http.SameSiteLaxMode || nonce.MaxAge != 600 {
		t.Fatalf("nonce cookie attributes wrong: %+v", nonce)
	}
	if len(nonce.Value) < 40 { // 32 bytes base64url ≈ 43 字元
		t.Fatalf("nonce should be 32 random bytes base64url, got %d chars", len(nonce.Value))
	}
	var body struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(rw.Body.Bytes(), &body)
	parsed, _ := url.Parse(body.URL)
	state := parsed.Query().Get("state")

	callback := func(c *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/callback?code=test-code&state="+url.QueryEscape(state), nil)
		if c != nil {
			req.AddCookie(c)
		}
		w := httptest.NewRecorder()
		e.h.Router().ServeHTTP(w, req)
		return w
	}
	// 負向：沒有 cookie（攻擊者把自己的授權連結塞給受害者）→ state_mismatch，不換 token、不建連線
	w := callback(nil)
	if loc := w.Header().Get("Location"); w.Code != http.StatusFound || !strings.Contains(loc, "coros_mcp=error") || !strings.Contains(loc, "reason=state_mismatch") {
		t.Fatalf("missing cookie must be state_mismatch, got %d %q", w.Code, loc)
	}
	if _, _, _, _, calls := e.fc.snapshot(); calls != 0 {
		t.Fatalf("CSRF failure must not reach the token endpoint, token calls=%d", calls)
	}
	if conn, _ := e.h.getConnection(e.ctx, uid); conn != nil {
		t.Fatal("CSRF failure must not create a connection")
	}
	// 負向：cookie 值不符
	if w := callback(&http.Cookie{Name: "dor_cmcp_n", Value: nonce.Value + "x"}); !strings.Contains(w.Header().Get("Location"), "reason=state_mismatch") {
		t.Fatalf("wrong cookie must be state_mismatch, got %q", w.Header().Get("Location"))
	}
	// 正向：正確 cookie → 連線成功，且回應清掉 cookie
	w = callback(&http.Cookie{Name: "dor_cmcp_n", Value: nonce.Value})
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "coros_mcp=connected") {
		t.Fatalf("correct cookie must connect, got %q", loc)
	}
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == "dor_cmcp_n" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("callback must clear the nonce cookie, Set-Cookie=%v", w.Header()["Set-Cookie"])
	}
	if conn, _ := e.h.getConnection(e.ctx, uid); conn == nil {
		t.Fatal("connection must exist after a valid callback")
	}
}

// TestIntegration_Stage2_TerraDisconnectKeepsMcpRows：中斷 Terra／Partner 的 COROS（DeleteProviderActivities(user,'coros')）
// 只刪它們自己的列，COROS MCP 直連匯入的 'mcp:' 列必須保留（Stage 2 審查；從 Terra 切到直連時一定會走到）。
func TestIntegration_Stage2_TerraDisconnectKeepsMcpRows(t *testing.T) {
	e := newMcpImportEnv(t, 1)
	now := time.Now().Truncate(time.Second)
	labelA := mcpTestLabel(7)
	e.fc.setRecords([]corosFixtureRec{
		{Title: "Outdoor Run", Start: now.AddDate(0, 0, -2).Unix(), ElapsedS: 2494, DurationS: 2433, Distance: "5.31 km", HR: 133, LabelID: labelA, Sport: 100},
	})
	uid := e.connectUser(0, mcpFloorNoon(20))
	e.resetThrottle()
	if rw := e.do(http.MethodPost, "/import", uid); rw.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rw.Code, rw.Body.String())
	}
	// 另一筆 Terra 來的 coros 列（不重疊）
	terraExt := "terra-" + corosMcpRandSuffix()
	start := now.AddDate(0, 0, -5)
	if res, err := e.repo.ImportActivity(e.ctx, &NormalizedActivity{UserID: uid, Source: "coros", ExternalID: terraExt,
		Fingerprint: fingerprintOf(start.Unix(), 3000, 1200), DistanceKm: 3, DurationS: 1200, AvgPaceS: 400, RecordedAt: start.UTC()}); err != nil || res.Status != "inserted" {
		t.Fatalf("seed terra row: %+v %v", res, err)
	}
	if err := e.repo.DeleteProviderActivities(e.ctx, uid, "coros"); err != nil {
		t.Fatalf("DeleteProviderActivities: %v", err)
	}
	var mcpRows, terraRows int
	_ = e.pool.QueryRow(e.ctx, `SELECT COUNT(*) FROM activities WHERE user_id=$1 AND external_id=$2`, uid, "mcp:"+labelA).Scan(&mcpRows)
	_ = e.pool.QueryRow(e.ctx, `SELECT COUNT(*) FROM activities WHERE user_id=$1 AND external_id=$2`, uid, terraExt).Scan(&terraRows)
	if mcpRows != 1 || terraRows != 0 {
		t.Fatalf("Terra/Partner disconnect must delete only its own coros rows: mcp=%d (want 1) terra=%d (want 0)", mcpRows, terraRows)
	}
}
