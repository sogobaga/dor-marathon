package integration

// 不需要 DB／不打真正 COROS 的純函式單元測試。需要 Postgres 的端對端流程見
// corosmcp_integration_test.go（build tag integration，比照 internal/gpsrawlog/integration_test.go 既有慣例）。

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- PKCE ---

func TestCorosMcpPKCE_VerifierAndChallenge(t *testing.T) {
	v1, err := corosMcpGeneratePKCEVerifier()
	if err != nil {
		t.Fatalf("generate verifier: %v", err)
	}
	v2, err := corosMcpGeneratePKCEVerifier()
	if err != nil {
		t.Fatalf("generate verifier: %v", err)
	}
	if v1 == v2 {
		t.Fatal("兩次產生的 verifier 不應相同")
	}
	if len(v1) < 32 {
		t.Fatalf("verifier 長度太短: %d", len(v1))
	}
	sum := sha256.Sum256([]byte(v1))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if got := corosMcpPKCEChallenge(v1); got != want {
		t.Fatalf("challenge = %q, want %q", got, want)
	}
}

// --- state 簽章 ---

func TestCorosMcpState_SignVerifyRoundTrip(t *testing.T) {
	secret := "test-secret"
	state := corosMcpSignState(secret, "user-1", "https://mcpus.coros.com", "verifier-abc", 10*time.Minute)
	userID, issuer, verifier, ok := corosMcpVerifyState(secret, state)
	if !ok {
		t.Fatal("expected ok=true for valid state")
	}
	if userID != "user-1" || issuer != "https://mcpus.coros.com" || verifier != "verifier-abc" {
		t.Fatalf("unexpected round-trip values: %q %q %q", userID, issuer, verifier)
	}
}

func TestCorosMcpState_Expired(t *testing.T) {
	secret := "test-secret"
	state := corosMcpSignState(secret, "user-1", "https://mcpus.coros.com", "v", -1*time.Second)
	if _, _, _, ok := corosMcpVerifyState(secret, state); ok {
		t.Fatal("expected ok=false for expired state")
	}
}

func TestCorosMcpState_TamperedSignatureRejected(t *testing.T) {
	secret := "test-secret"
	state := corosMcpSignState(secret, "user-1", "https://mcpus.coros.com", "v", 10*time.Minute)
	i := strings.LastIndex(state, ".")
	tampered := state[:i] + "." + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, _, _, ok := corosMcpVerifyState(secret, tampered); ok {
		t.Fatal("expected ok=false for tampered signature")
	}
}

func TestCorosMcpState_TamperedPayloadRejected(t *testing.T) {
	secret := "test-secret"
	state := corosMcpSignState(secret, "user-1", "https://mcpus.coros.com", "v", 10*time.Minute)
	i := strings.LastIndex(state, ".")
	raw, sig := state[:i], state[i+1:]
	msgBytes, _ := base64.RawURLEncoding.DecodeString(raw)
	tamperedMsg := strings.Replace(string(msgBytes), "user-1", "user-2", 1)
	tampered := base64.RawURLEncoding.EncodeToString([]byte(tamperedMsg)) + "." + sig
	if _, _, _, ok := corosMcpVerifyState(secret, tampered); ok {
		t.Fatal("expected ok=false for tampered payload (userID swapped without re-signing)")
	}
}

func TestCorosMcpState_WrongSecretRejected(t *testing.T) {
	state := corosMcpSignState("secret-a", "user-1", "https://mcpus.coros.com", "v", 10*time.Minute)
	if _, _, _, ok := corosMcpVerifyState("secret-b", state); ok {
		t.Fatal("expected ok=false when verifying with a different secret")
	}
}

func TestCorosMcpState_MalformedRejected(t *testing.T) {
	for _, s := range []string{"", "no-dot-here", ".sig-only", "garbage.sig"} {
		if _, _, _, ok := corosMcpVerifyState("secret", s); ok {
			t.Fatalf("expected ok=false for malformed state %q", s)
		}
	}
}

// --- host 驗證（SSRF 防護）---

func TestCorosMcpValidateHost(t *testing.T) {
	cases := []struct {
		url     string
		wantErr bool
	}{
		{"https://coros.com/.well-known/openid-configuration", false},
		{"https://mcpus.coros.com/oauth2/authorize", false},
		{"https://mcpus.coros.com:443/oauth2/authorize", false},
		{"http://coros.com/x", true},           // 非 https
		{"https://coros.com.evil.com/x", true}, // 偽裝子網域（字首相同、host 不同）
		{"https://evilcoros.com/x", true},      // 字尾相似但非子網域（缺分隔的點）
		{"https://user@coros.com/x", true},     // URL 內嵌帳密
		{"https://coros.com:8443/x", true},     // 非 443 的 port
		{"https://93.184.216.34/x", true},      // IP 字面量 host
		{"https://[::1]/x", true},              // IPv6 字面量 host
		{"not a url", true},
		{"", true},
	}
	for _, c := range cases {
		t.Run(c.url, func(t *testing.T) {
			err := corosMcpValidateHost(c.url)
			if c.wantErr && err == nil {
				t.Fatalf("expected error for %q, got nil", c.url)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("expected no error for %q, got %v", c.url, err)
			}
		})
	}
}

// --- 白名單 ---

func TestCorosMcpWhitelisted(t *testing.T) {
	list := "Owner@Example.com, second@example.com\nthird@example.com;fourth@example.com"
	cases := []struct {
		email string
		want  bool
	}{
		{"owner@example.com", true}, // 大小寫不敏感
		{"OWNER@EXAMPLE.COM", true},
		{"second@example.com", true},
		{"third@example.com", true},
		{"fourth@example.com", true},
		{"nobody@example.com", false},
		{"", false},
	}
	for _, c := range cases {
		if got := corosMcpWhitelisted(list, c.email); got != c.want {
			t.Fatalf("corosMcpWhitelisted(%q) = %v, want %v", c.email, got, c.want)
		}
	}
}

// --- SSE / JSON 回應解析 ---

func TestCorosMcpParseSSEData_SingleEvent(t *testing.T) {
	body := []byte("data: {\"result\":{\"ok\":true}}\n\n")
	got, err := corosMcpParseSSEData(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != `{"result":{"ok":true}}` {
		t.Fatalf("unexpected parsed data: %s", got)
	}
}

func TestCorosMcpParseSSEData_TakesLastEvent(t *testing.T) {
	body := []byte("data: {\"n\":1}\n\ndata: {\"n\":2}\n\ndata: {\"n\":3}\n\n")
	got, err := corosMcpParseSSEData(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]int
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["n"] != 3 {
		t.Fatalf("expected last event (n=3), got %v", m)
	}
}

func TestCorosMcpParseSSEData_NoEvents(t *testing.T) {
	if _, err := corosMcpParseSSEData([]byte("\n\n")); err == nil {
		t.Fatal("expected error for SSE body with no data events")
	}
}

func TestCorosMcpParseResponseBody_Dispatch(t *testing.T) {
	jsonBody := []byte(`{"result":1}`)
	if got, err := corosMcpParseResponseBody("application/json", jsonBody); err != nil || string(got) != string(jsonBody) {
		t.Fatalf("plain json passthrough failed: %s, %v", got, err)
	}
	sseBody := []byte("data: {\"result\":2}\n\n")
	got, err := corosMcpParseResponseBody("text/event-stream; charset=utf-8", sseBody)
	if err != nil {
		t.Fatalf("sse dispatch failed: %v", err)
	}
	if string(got) != `{"result":2}` {
		t.Fatalf("unexpected sse-dispatched body: %s", got)
	}
}

// --- DCR 回應消毒（secret 絕不落地）---

func TestCorosMcpSanitizeRegistration(t *testing.T) {
	raw := map[string]any{
		"client_id":                  "abc123",
		"client_secret":              "super-secret-value",
		"client_secret_expires_at":   float64(0),
		"token_endpoint_auth_method": "client_secret_basic",
	}
	out := corosMcpSanitizeRegistration(raw)
	if _, ok := out["client_secret"]; ok {
		t.Fatal("client_secret 不應出現在消毒後的結果")
	}
	if _, ok := out["client_secret_expires_at"]; ok {
		t.Fatal("任何含 secret 字樣的鍵都應被移除")
	}
	if out["client_id"] != "abc123" {
		t.Fatal("非 secret 欄位應保留")
	}
	marshaled, _ := json.Marshal(out)
	if strings.Contains(string(marshaled), "super-secret-value") {
		t.Fatal("消毒後的 JSON 不應包含原始 secret 字串")
	}
}

// --- querySportRecords 參數猜測 ---

func TestCorosMcpSportRecordsArgs(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	if args := corosMcpSportRecordsArgs(nil, now); len(args) != 0 {
		t.Fatalf("nil schema 應回空 map，得到 %v", args)
	}

	schemaNoMatch := map[string]any{"properties": map[string]any{"sportType": map[string]any{}}}
	if args := corosMcpSportRecordsArgs(schemaNoMatch, now); len(args) != 0 {
		t.Fatalf("無已知日期欄位時應回空 map，得到 %v", args)
	}

	schemaWithDates := map[string]any{"properties": map[string]any{
		"startDate": map[string]any{"type": "string"},
		"endDate":   map[string]any{"type": "string"},
	}}
	args := corosMcpSportRecordsArgs(schemaWithDates, now)
	if args["startDate"] != "2026-09-17" || args["endDate"] != "2026-10-01" {
		t.Fatalf("unexpected 14 天日期範圍: %v", args)
	}
}

// --- 從 querySportRecords 回應猜活動 id ---

func TestCorosMcpExtractFirstActivityID(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"array of objects, id field", `[{"id":"act-1"},{"id":"act-2"}]`, "act-1"},
		{"array of objects, labelId field", `[{"labelId":"L-9"}]`, "L-9"},
		{"wrapped records key", `{"records":[{"activityId":"A-7"}]}`, "A-7"},
		{"wrapped data key, numeric id", `{"data":[{"id":42}]}`, "42"},
		{"empty array", `[]`, ""},
		{"no recognizable id field", `[{"foo":"bar"}]`, ""},
		{"garbage", `not json`, ""},
		{"empty bytes", ``, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := corosMcpExtractFirstActivityID([]byte(c.raw))
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// --- Step 筆數摘要 ---

func TestCorosMcpCountOf(t *testing.T) {
	tools := []mcpTool{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	if n, ok := corosMcpCountOf("tools/list", tools); !ok || n != 3 {
		t.Fatalf("tools/list count = %d,%v, want 3,true", n, ok)
	}

	arrResult := &mcpToolCallResult{Content: []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: `[{"id":1},{"id":2}]`}}}
	if n, ok := corosMcpCountOf("queryDevices", arrResult); !ok || n != 2 {
		t.Fatalf("array content count = %d,%v, want 2,true", n, ok)
	}

	proseResult := &mcpToolCallResult{Content: []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: "not an array"}}}
	if _, ok := corosMcpCountOf("queryDevices", proseResult); ok {
		t.Fatal("非 JSON array 的文字內容不應給出 count")
	}

	if _, ok := corosMcpCountOf("queryDevices", (*mcpToolCallResult)(nil)); ok {
		t.Fatal("nil result 不應給出 count")
	}
}

// --- MCP JSON-RPC 用戶端：401／isError／JSON-RPC error ---
//
// 不用 httptest.NewServer（真實 TCP/127.0.0.1）：這個沙盒環境對 httptest 的連續請求會隨機出現
// "unexpected EOF"（已實測重現，與我們的程式邏輯無關，純粹是這個網路沙盒的 artifact——即使完全
// 不相干的最小重現案例也會發生），改用 inProcessRoundTripper 把 http.Handler 包成
// http.RoundTripper，完全不經過作業系統 socket，於本測試行程內直接呼叫 handler——同時也更貼合
// 「絕不對 coros.com 發出任何網路請求」的鐵律（根本沒有實際網路層，不只是沒打到 coros.com host）。

type inProcessRoundTripper struct{ handler http.Handler }

func (rt inProcessRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	rt.handler.ServeHTTP(rec, req)
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

// fakeCorosBase：假 COROS 伺服器的門面 URL——inProcessRoundTripper 不會真的對外撥號，host/scheme
// 本身無意義，純粹讓 corosMcpRegister/postTokenForm/mcpCall 組得出合法的 *http.Request。
const fakeCorosBase = "http://coros-mcp-fake.test"

func newTestCorosMcpHandler() *CorosMcpHandler {
	// repo 刻意留 nil：下面測的函式（mcpCall／corosMcpRegister／discover）都不觸碰 h.repo，
	// 只有牽涉 DB 的流程（ensureClient／getConnection／saveConnection 等）才需要真正的 Repository，
	// 那些留給 corosmcp_integration_test.go。
	return &CorosMcpHandler{
		cfg: CorosMcpConfig{RedirectURI: "https://www.dor.tw/api/v1/integrations/coros-mcp/callback", GatewayURL: "https://mcp.coros.com", JWTSecret: "test"},
		hc:  &http.Client{Timeout: 5 * time.Second},
	}
}

// newTestCorosMcpHandlerWithHandler 比照 newTestCorosMcpHandler，額外把 h.hc 接上
// inProcessRoundTripper，讓它對 fakeCorosBase 的任何請求都直接呼叫 handler（見上方註解）。
func newTestCorosMcpHandlerWithHandler(handler http.Handler) *CorosMcpHandler {
	h := newTestCorosMcpHandler()
	h.hc = &http.Client{Timeout: 5 * time.Second, Transport: inProcessRoundTripper{handler: handler}}
	return h
}

func TestMCPCall_401ReturnsSentinel(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://mcpus.coros.com/.well-known/oauth-protected-resource/mcp"`)
		w.WriteHeader(http.StatusUnauthorized)
	})
	h := newTestCorosMcpHandlerWithHandler(handler)
	_, err := h.mcpCall(context.Background(), fakeCorosBase, "bad-token", "initialize", mcpInitializeParams(), 1)
	if !errors.Is(err, errMCPUnauthorized) {
		t.Fatalf("expected errMCPUnauthorized, got %v", err)
	}
}

func TestMCPCall_JSONRPCError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}`))
	})
	h := newTestCorosMcpHandlerWithHandler(handler)
	_, err := h.mcpCall(context.Background(), fakeCorosBase, "tok", "tools/call", nil, 1)
	if err == nil || !strings.Contains(err.Error(), "method not found") {
		t.Fatalf("expected error mentioning JSON-RPC message, got %v", err)
	}
}

func TestMCPCall_SSEResponse(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"tools\":[]}}\n\n"))
	})
	h := newTestCorosMcpHandlerWithHandler(handler)
	resp, err := h.mcpCall(context.Background(), fakeCorosBase, "tok", "tools/list", nil, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(resp.Result) != `{"tools":[]}` {
		t.Fatalf("unexpected result from SSE body: %s", resp.Result)
	}
}

func TestToolsCall_IsErrorPropagates(t *testing.T) {
	callCount := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Method == "initialize" {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"isError":true,"content":[{"type":"text","text":"not permitted"}]}}`))
	})
	h := newTestCorosMcpHandlerWithHandler(handler)
	conn := &corosMcpConnection{Issuer: fakeCorosBase, AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour)}
	result, err := h.toolsCall(context.Background(), conn, "queryDevices", nil)
	if err == nil || !strings.Contains(err.Error(), "not permitted") {
		t.Fatalf("expected isError to surface as error, got result=%v err=%v", result, err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected result to still be populated with IsError=true, got %v", result)
	}
	if callCount != 2 {
		t.Fatalf("expected exactly initialize+tools/call (2 requests), got %d", callCount)
	}
}

// TestToolsList_Pagination：驗證 nextCursor 分頁會一路抓到底（契約第 8 點：「tools/list…存完整清單」）。
func TestToolsList_Pagination(t *testing.T) {
	page := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Method string `json:"method"`
			Params struct {
				Cursor string `json:"cursor"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Method == "initialize" {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
			return
		}
		page++
		if page == 1 {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"a"}],"nextCursor":"p2"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":3,"result":{"tools":[{"name":"b"}]}}`))
	})
	h := newTestCorosMcpHandlerWithHandler(handler)
	conn := &corosMcpConnection{Issuer: fakeCorosBase, AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour)}
	tools, err := h.toolsList(context.Background(), conn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "a" || tools[1].Name != "b" {
		t.Fatalf("expected both pages concatenated, got %+v", tools)
	}
}

// --- DCR：confidential 優先、被拒才退回 public（不碰 DB，直接測 corosMcpRegister）---

func TestCorosMcpRegister_ConfidentialThenFallbackToPublic(t *testing.T) {
	var gotMethods []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		method, _ := body["token_endpoint_auth_method"].(string)
		gotMethods = append(gotMethods, method)
		if method == "client_secret_basic" {
			w.WriteHeader(http.StatusBadRequest) // 模擬 COROS 拒絕 confidential 註冊
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"client_id":"public-client-id"}`))
	})
	h := newTestCorosMcpHandlerWithHandler(handler)
	// 模擬 ensureClient 內部的「confidential 先、被拒才 fallback」邏輯（不經過 DB 層）。
	clientID, _, _, err := h.corosMcpRegister(context.Background(), fakeCorosBase, "client_secret_basic")
	if err == nil {
		t.Fatalf("expected confidential registration to fail in this test server, got clientID=%s", clientID)
	}
	clientID, clientSecret, raw, err := h.corosMcpRegister(context.Background(), fakeCorosBase, "none")
	if err != nil {
		t.Fatalf("public fallback registration should succeed: %v", err)
	}
	if clientID != "public-client-id" || clientSecret != "" {
		t.Fatalf("unexpected public client result: id=%q secret=%q", clientID, clientSecret)
	}
	if _, ok := raw["client_secret"]; ok {
		t.Fatal("public client 回應不應含 client_secret")
	}
	if len(gotMethods) != 2 || gotMethods[0] != "client_secret_basic" || gotMethods[1] != "none" {
		t.Fatalf("expected confidential attempt then public fallback, got %v", gotMethods)
	}
}

func TestCorosMcpRegister_ConfidentialSucceeds(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"client_id":"conf-id","client_secret":"shh-secret","token_endpoint_auth_method":"client_secret_basic"}`))
	})
	h := newTestCorosMcpHandlerWithHandler(handler)
	clientID, clientSecret, raw, err := h.corosMcpRegister(context.Background(), fakeCorosBase, "client_secret_basic")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clientID != "conf-id" || clientSecret != "shh-secret" {
		t.Fatalf("unexpected registration result: %q %q", clientID, clientSecret)
	}
	sanitized := corosMcpSanitizeRegistration(raw)
	if _, ok := sanitized["client_secret"]; ok {
		t.Fatal("sanitized raw response 不應含 client_secret")
	}
}

// --- discover：host 驗證先於網路呼叫（惡意/格式錯誤的 base 永遠不會真的發出請求）---

func TestDiscover_RejectsInvalidBaseWithoutDialing(t *testing.T) {
	h := newTestCorosMcpHandler()
	for _, base := range []string{"http://coros.com", "https://evilcoros.com", "not-a-url"} {
		if _, err := h.discover(context.Background(), base); err == nil {
			t.Fatalf("expected discover(%q) to be rejected by host validation", base)
		}
	}
}

// --- 錯誤訊息絕不帶回應原文（可能含 token/secret）---

func TestPostTokenForm_ErrorNeverLeaksBody(t *testing.T) {
	leaked := "leak-me-access-token-12345"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"error":"invalid_grant","access_token":%q}`, leaked)
	})
	h := newTestCorosMcpHandlerWithHandler(handler)
	client := &corosMcpClient{ClientID: "cid", AuthMethod: "none"}
	_, err := h.postTokenForm(context.Background(), fakeCorosBase, map[string][]string{"grant_type": {"authorization_code"}}, client)
	if err == nil {
		t.Fatal("expected error for non-200 token response")
	}
	if strings.Contains(err.Error(), leaked) {
		t.Fatalf("error message leaked response body: %v", err)
	}
}
