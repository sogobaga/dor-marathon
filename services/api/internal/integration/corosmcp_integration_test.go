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

// newFakeCorosMCPServer 起一個假的 COROS MCP 伺服器：DCR 註冊（固定回 confidential）、token
// 交換／刷新、revoke、MCP JSON-RPC（initialize/tools/list/tools/call）。lastMCPAuth 讓測試斷言
// 「token 刷新後，後續 MCP 呼叫確實帶著新的 access token」。
func newFakeCorosMCPServer(t *testing.T) (srv *httptest.Server, lastMCPAuth *string, registerCount *int) {
	t.Helper()
	var mu sync.Mutex
	var lastAuth string
	var regCount int
	mux := http.NewServeMux()
	mux.HandleFunc("/connect/register", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		regCount++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"client_id":"fake-client-id","client_secret":"regsecret-xyz","token_endpoint_auth_method":"client_secret_basic"}`))
	})
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			_, _ = w.Write([]byte(`{"access_token":"tok-1","refresh_token":"rtok-1","expires_in":30,"token_type":"Bearer","scope":"openid offline_access mcp.tools"}`))
		case "refresh_token":
			_, _ = w.Write([]byte(`{"access_token":"tok-2","refresh_token":"rtok-2","expires_in":3600,"token_type":"Bearer","scope":"openid offline_access mcp.tools"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unsupported_grant_type"}`))
		}
	})
	mux.HandleFunc("/oauth2/revoke", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		lastAuth = r.Header.Get("Authorization")
		mu.Unlock()
		var body struct {
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
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
			var text string
			switch body.Params.Name {
			case "queryDevices":
				text = `[{"deviceId":"d1","name":"COROS PACE 4"}]`
			case "querySportRecords":
				text = `[{"id":"act-123","startTime":1234567890}]`
			case "getActivityDetail":
				text = `{"id":"act-123","distance":5000,"duration":1800}`
			case "queryActivityLapData":
				text = `[{"lap":1,"distance":1000}]`
			default:
				text = `[]`
			}
			resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"result":{"isError":false,"content":[{"type":"text","text":%q}]}}`, text)
			_, _ = w.Write([]byte(resp))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &lastAuth, &regCount
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

	srv, lastMCPAuth, regCount := newFakeCorosMCPServer(t)
	doc := seedDiscovery(srv.URL)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM coros_mcp_clients WHERE issuer=$1`, doc.Issuer)
	})

	h := NewCorosMcpHandler(repo, CorosMcpConfig{
		GatewayURL: srv.URL, RedirectURI: "https://www.dor.tw/api/v1/integrations/coros-mcp/callback",
		FrontendURL: "https://www.dor.tw", JWTSecret: "test-secret",
	}, passthroughAuth, nil)

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
	}
	if *regCount != 1 {
		t.Fatalf("expected exactly 1 DCR registration, got %d", *regCount)
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
		for _, s := range probeSteps {
			if !s.OK {
				t.Fatalf("expected step %s to succeed, got %+v", s.Step, s)
			}
		}
	}
	if *lastMCPAuth != "Bearer tok-2" {
		t.Fatalf("expected probe's MCP calls to use refreshed token tok-2, got %q", *lastMCPAuth)
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
		if strings.Contains(string(rawResponse), "regsecret-xyz") {
			t.Fatal("coros_mcp_clients.raw_response 不應包含 client_secret 原文")
		}
		var secretCol *string
		if err := pool.QueryRow(ctx, `SELECT client_secret FROM coros_mcp_clients WHERE issuer=$1`, doc.Issuer).Scan(&secretCol); err != nil {
			t.Fatalf("read client_secret column: %v", err)
		}
		if secretCol == nil || !strings.HasPrefix(*secretCol, "enc:") {
			t.Fatalf("expected client_secret to be stored encrypted (enc: prefix), got %v", secretCol)
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
			for _, secret := range []string{"regsecret-xyz", "tok-1", "tok-2", "rtok-1", "rtok-2"} {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("probe_logs.response 不應包含 token/secret 原文 (%s): %s", secret, raw)
				}
			}
		}
		if n != 5 {
			t.Fatalf("expected 5 probe_logs rows, got %d", n)
		}
	}

	// --- POST /disconnect：revoke=true（confidential client）、連線被刪、provider='coros' 不受影響 ---
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
		if !body.OK || !body.Revoked {
			t.Fatalf("expected ok=true revoked=true, got %+v", body)
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

	srv, _, regCount := newFakeCorosMCPServer(t)
	doc := seedDiscovery(srv.URL + "/lock-test") // 獨立 issuer 字串，避免撞到其他測試的 cache/DB 列
	doc.RegistrationEndpoint = srv.URL + "/connect/register"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM coros_mcp_clients WHERE issuer=$1`, doc.Issuer)
	})

	h := NewCorosMcpHandler(repo, CorosMcpConfig{GatewayURL: srv.URL, RedirectURI: "https://www.dor.tw/x", FrontendURL: "https://www.dor.tw", JWTSecret: "s"}, passthroughAuth, nil)

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
	if *regCount != 1 {
		t.Fatalf("expected exactly 1 DCR registration despite %d concurrent callers, got %d", n, *regCount)
	}
	var rowCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM coros_mcp_clients WHERE issuer=$1`, doc.Issuer).Scan(&rowCount); err != nil {
		t.Fatalf("count coros_mcp_clients: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("expected exactly 1 coros_mcp_clients row, got %d", rowCount)
	}
}
