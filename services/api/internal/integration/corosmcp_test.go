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
	"net/url"
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
	state := corosMcpSignState(secret, "user-1", "https://mcpus.coros.com", "verifier-abc", "nonce-xyz", 10*time.Minute)
	userID, issuer, verifier, nonce, ok := corosMcpVerifyState(secret, state)
	if !ok {
		t.Fatal("expected ok=true for valid state")
	}
	if userID != "user-1" || issuer != "https://mcpus.coros.com" || verifier != "verifier-abc" || nonce != "nonce-xyz" {
		t.Fatalf("unexpected round-trip values: %q %q %q %q", userID, issuer, verifier, nonce)
	}
}

func TestCorosMcpState_Expired(t *testing.T) {
	secret := "test-secret"
	state := corosMcpSignState(secret, "user-1", "https://mcpus.coros.com", "v", "n", -1*time.Second)
	if _, _, _, _, ok := corosMcpVerifyState(secret, state); ok {
		t.Fatal("expected ok=false for expired state")
	}
}

func TestCorosMcpState_TamperedSignatureRejected(t *testing.T) {
	secret := "test-secret"
	state := corosMcpSignState(secret, "user-1", "https://mcpus.coros.com", "v", "n", 10*time.Minute)
	i := strings.LastIndex(state, ".")
	tampered := state[:i] + "." + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, _, _, _, ok := corosMcpVerifyState(secret, tampered); ok {
		t.Fatal("expected ok=false for tampered signature")
	}
}

func TestCorosMcpState_TamperedPayloadRejected(t *testing.T) {
	secret := "test-secret"
	state := corosMcpSignState(secret, "user-1", "https://mcpus.coros.com", "v", "n", 10*time.Minute)
	i := strings.LastIndex(state, ".")
	raw, sig := state[:i], state[i+1:]
	msgBytes, _ := base64.RawURLEncoding.DecodeString(raw)
	tamperedMsg := strings.Replace(string(msgBytes), "user-1", "user-2", 1)
	tampered := base64.RawURLEncoding.EncodeToString([]byte(tamperedMsg)) + "." + sig
	if _, _, _, _, ok := corosMcpVerifyState(secret, tampered); ok {
		t.Fatal("expected ok=false for tampered payload (userID swapped without re-signing)")
	}
}

func TestCorosMcpState_WrongSecretRejected(t *testing.T) {
	state := corosMcpSignState("secret-a", "user-1", "https://mcpus.coros.com", "v", "n", 10*time.Minute)
	if _, _, _, _, ok := corosMcpVerifyState("secret-b", state); ok {
		t.Fatal("expected ok=false when verifying with a different secret")
	}
}

func TestCorosMcpState_MalformedRejected(t *testing.T) {
	for _, s := range []string{"", "no-dot-here", ".sig-only", "garbage.sig"} {
		if _, _, _, _, ok := corosMcpVerifyState("secret", s); ok {
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

// --- querySportRecords 參數（照 2026-10-01 tools/list 實測 schema）---

// corosRealSportRecordsSchema：正式站 tools/list 回傳的 querySportRecords inputSchema（節錄 required／properties 名稱）。
var corosRealSportRecordsSchema = map[string]any{
	"type": "object",
	"required": []any{"startDate", "endDate", "sportTypeCodes", "minDistanceKm", "maxDistanceKm",
		"minDurationMinutes", "maxDurationMinutes", "maxAveragePace", "locationKeyword", "limit"},
	"properties": map[string]any{
		"limit": map[string]any{"type": "integer"}, "endDate": map[string]any{"type": "string"},
		"startDate": map[string]any{"type": "string"}, "maxDistanceKm": map[string]any{"type": "number"},
		"minDistanceKm": map[string]any{"type": "number"}, "maxAveragePace": map[string]any{"type": "string"},
		"sportTypeCodes": map[string]any{"type": "array"}, "locationKeyword": map[string]any{"type": "string"},
		"maxDurationMinutes": map[string]any{"type": "integer"}, "minDurationMinutes": map[string]any{"type": "integer"},
	},
	"additionalProperties": false,
}

func TestCorosMcpSportRecordsArgs_RealSchema(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	args := corosMcpSportRecordsArgs(corosRealSportRecordsSchema, now)
	// v869 送 "2026-09-17" 被 COROS 判 anomaly；必須 yyyyMMdd
	if args["startDate"] != "20260917" || args["endDate"] != "20261001" {
		t.Fatalf("dates must be yyyyMMdd 14-day window, got %v / %v", args["startDate"], args["endDate"])
	}
	// required 的 10 個欄位都要出現（選填不用就給 null），且不得多出 schema 沒有的欄位（additionalProperties=false）
	if len(args) != 10 {
		t.Fatalf("expected exactly the 10 required keys, got %d: %v", len(args), args)
	}
	for _, k := range []string{"minDistanceKm", "maxDistanceKm", "minDurationMinutes", "maxDurationMinutes", "maxAveragePace", "locationKeyword"} {
		if v, ok := args[k]; !ok || v != nil {
			t.Fatalf("optional filter %s must be present as null, got %v (present=%v)", k, v, ok)
		}
	}
	codes, ok := args["sportTypeCodes"].([]int)
	if !ok || len(codes) != 6 || codes[0] != 100 || codes[5] != 900 {
		t.Fatalf("sportTypeCodes should be DOR run+hike+walk codes, got %v", args["sportTypeCodes"])
	}
	if args["limit"] != 10 {
		t.Fatalf("limit = %v, want 10", args["limit"])
	}
	// 序列化後 null 欄位要真的是 null（不是被 omitempty 吃掉）
	b, _ := json.Marshal(args)
	if !strings.Contains(string(b), `"locationKeyword":null`) {
		t.Fatalf("null filters must serialize as null: %s", b)
	}
}

func TestCorosMcpSportRecordsArgs_NoSchemaFallsBackToKnownKeys(t *testing.T) {
	args := corosMcpSportRecordsArgs(nil, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if len(args) != 10 || args["startDate"] != "20260917" {
		t.Fatalf("nil schema should fall back to the known 10 keys, got %v", args)
	}
}

// --- 文字解包／anomaly／活動 labelId＋sportType ---

func TestCorosMcpUnwrapTextAndAnomaly(t *testing.T) {
	// 正式站實測：content[0].text 是 JSON 字串字面值（外層多一層引號）
	quoted := `"Tool call anomalies detected. High risk of session context pollution or request exceeds the LLM capability boundary."`
	if got := corosMcpUnwrapText(quoted); !strings.HasPrefix(got, "Tool call anomalies detected") {
		t.Fatalf("unwrap failed: %q", got)
	}
	if !corosMcpIsAnomaly(corosMcpUnwrapText(quoted)) {
		t.Fatal("anomaly text must be detected")
	}
	if corosMcpIsAnomaly("Bound Devices (1)") {
		t.Fatal("normal text must not be flagged as anomaly")
	}
	if got := corosMcpUnwrapText(`[{"a":1}]`); got != `[{"a":1}]` {
		t.Fatalf("non-string JSON must be returned unchanged, got %q", got)
	}
}

func TestCorosMcpExtractFirstActivity(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		wantID    string
		wantSport int
		wantOK    bool
	}{
		{"json array", `[{"labelId":"476A","sportType":100,"distance":5},{"labelId":"B2","sportType":900}]`, "476A", 100, true},
		{"json wrapped, numeric labelId", `{"data":{"records":[{"labelId":4761234567890123456,"sportType":102}]}}`, "4761234567890123456", 102, true},
		{"json string literal wrapping text list", `"Workout Records (2)\n\n1. 2026-09-30 Outdoor Run\n   Distance: 10.02 km\n   labelId: 4761111\n   sportType: 100\n\n2. 2026-09-29 Walk\n   labelId: 4762222\n   sportType: 900"`, "4761111", 100, true},
		{"text with Label ID spelling", "1. Outdoor Run\n   Label ID: ABC123\n   Sport Type: 101\n2. Walk\n   Label ID: DEF456\n   Sport Type: 900", "ABC123", 101, true},
		{"anomaly text", `"Tool call anomalies detected. High risk..."`, "", 0, false},
		{"no records", `"No workout records found in the given period."`, "", 0, false},
		{"labelId without sportType", "labelId: 4761111", "", 0, false},
		{"empty", "", "", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, st, ok := corosMcpExtractFirstActivity(c.text)
			if ok != c.wantOK || id != c.wantID || st != c.wantSport {
				t.Fatalf("got (%q,%d,%v), want (%q,%d,%v)", id, st, ok, c.wantID, c.wantSport, c.wantOK)
			}
		})
	}
}

// --- Step 筆數摘要 ---

func corosTextResult(text string) *mcpToolCallResult {
	return &mcpToolCallResult{Content: []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: text}}}
}

func TestCorosMcpCountOf(t *testing.T) {
	tools := []mcpTool{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	if n, ok := corosMcpCountOf("tools/list", tools); !ok || n != 3 {
		t.Fatalf("tools/list count = %d,%v, want 3,true", n, ok)
	}
	if n, ok := corosMcpCountOf("queryDevices", corosTextResult(`[{"id":1},{"id":2}]`)); !ok || n != 2 {
		t.Fatalf("array content count = %d,%v, want 2,true", n, ok)
	}
	// 正式站 queryDevices 實際格式：JSON 字串字面值包著人看的清單，標題帶 (N)
	devices := `"Bound Devices (1)\n========================\n\n1. COROS PACE 4\n   Model Name: COROS R4"`
	if n, ok := corosMcpCountOf("queryDevices", corosTextResult(devices)); !ok || n != 1 {
		t.Fatalf("Bound Devices (1) count = %d,%v, want 1,true", n, ok)
	}
	records := "1. Run\n labelId: 4761111\n sportType: 100\n2. Walk\n labelId: 4762222\n sportType: 900"
	if n, ok := corosMcpCountOf("querySportRecords", corosTextResult(records)); !ok || n != 2 {
		t.Fatalf("labelId occurrences count = %d,%v, want 2,true", n, ok)
	}
	if _, ok := corosMcpCountOf("querySportRecords", corosTextResult(`"Tool call anomalies detected."`)); ok {
		t.Fatal("anomaly text must not yield a count")
	}
	if _, ok := corosMcpCountOf("queryDevices", corosTextResult("not an array")); ok {
		t.Fatal("prose without (N) or labelId must not yield a count")
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

// 對端一直回 nextCursor：頁數上限擋住無限迴圈（稽核 low），且不會把半份清單當成功。
func TestToolsList_PaginationIsCapped(t *testing.T) {
	calls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Method == "initialize" {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
			return
		}
		calls++
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"x"}],"nextCursor":"again"}}`))
	})
	h := newTestCorosMcpHandlerWithHandler(handler)
	conn := &corosMcpConnection{Issuer: fakeCorosBase, AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := h.toolsList(context.Background(), conn); err == nil || !strings.Contains(err.Error(), "pagination exceeded") {
		t.Fatalf("an endless nextCursor must be cut off with an error, got %v", err)
	}
	if calls != corosMcpMaxToolPages {
		t.Fatalf("exactly %d pages may be fetched, got %d", corosMcpMaxToolPages, calls)
	}
}

// MCP 回非 200／401 時是型別化錯誤（同步流程據此辨識 429／5xx → 冷卻），訊息不含回應原文。
func TestMCPCall_HTTPErrorIsTypedAndHasNoBody(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("secret-ish upstream text 王小明"))
	})
	h := newTestCorosMcpHandlerWithHandler(handler)
	_, err := h.mcpCall(context.Background(), fakeCorosBase+"/mcp", "tok", "tools/call", map[string]any{}, 3)
	var he *corosMcpHTTPError
	if !errors.As(err, &he) || he.Status != http.StatusTooManyRequests || he.Method != "tools/call" {
		t.Fatalf("want typed *corosMcpHTTPError(429), got %T %v", err, err)
	}
	if strings.Contains(err.Error(), "secret-ish") || strings.Contains(err.Error(), "王小明") {
		t.Fatalf("error text must not echo the upstream body: %q", err.Error())
	}
	if !isCorosThrottleErr(err) {
		t.Fatal("429 must be recognised as a throttling error")
	}
}

// --- DCR／token：照 COROS 正式站實際行為（2026-10-01 實測）---
//
// 實測：DCR 要求 client_secret_basic，COROS 仍回 200、token_endpoint_auth_method="none"、不發 client_secret。
// COROS 是 Spring Authorization Server：token 端點收到「空密碼的 Basic 標頭」→ 400 invalid_request（v868
// 正式站第一次連線失敗的根因）；public client 沒帶 client_id → 401 invalid_client；PKCE 缺 verifier → 400。
// fakeCorosSpringAS 盡量逐條模擬這些規則，讓測試抓得到同一類錯。

// corosRealDCRResponse：正式站 coros_mcp_clients.raw_response 去掉 client_id 後的實際內容（2026-10-01）。
const corosRealDCRResponse = `{"client_id":"11111111-2222-3333-4444-555555555555","scope":"offline_access openid mcp.tools","client_name":"DOR","grant_types":["authorization_code","refresh_token"],"redirect_uris":["https://www.dor.tw/api/v1/integrations/coros-mcp/callback"],"client_id_issued_at":1790838960,"token_endpoint_auth_method":"none"}`

type fakeTokenCall struct {
	authHeader string
	form       url.Values
}

// fakeCorosSpringAS：/connect/register 一律登記成 public；/oauth2/token 依 Spring AS 規則驗證；
// issueRefresh=false 模擬「不發 refresh token 給 public client」。
func fakeCorosSpringAS(t *testing.T, issueRefresh bool, calls *[]fakeTokenCall) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/connect/register", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(corosRealDCRResponse))
	})
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		*calls = append(*calls, fakeTokenCall{authHeader: r.Header.Get("Authorization"), form: r.PostForm})
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "" {
			// public client 根本沒有 secret：任何 Basic 標頭（含空密碼）都不合法
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_request","error_description":"OAuth 2.0 Parameter: client_secret"}`))
			return
		}
		if r.PostForm.Get("client_id") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		if r.PostForm.Get("grant_type") == "authorization_code" && r.PostForm.Get("code_verifier") == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		if issueRefresh {
			_, _ = w.Write([]byte(`{"access_token":"at-1","refresh_token":"rt-1","expires_in":3600,"token_type":"Bearer"}`))
		} else {
			_, _ = w.Write([]byte(`{"access_token":"at-1","expires_in":3600,"token_type":"Bearer"}`))
		}
	})
	return mux
}

func TestCorosMcpRegister_RequestsPublicAndReadsRegisteredMethod(t *testing.T) {
	var gotMethod string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotMethod, _ = body["token_endpoint_auth_method"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(corosRealDCRResponse))
	})
	h := newTestCorosMcpHandlerWithHandler(handler)
	clientID, clientSecret, raw, err := h.corosMcpRegister(context.Background(), fakeCorosBase+"/connect/register", "none")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != "none" {
		t.Fatalf("DCR 應直接要求 none（與 COROS 官方 skill 相同），got %q", gotMethod)
	}
	if clientID == "" || clientSecret != "" {
		t.Fatalf("unexpected registration result: id=%q secret=%q", clientID, clientSecret)
	}
	registered, _ := raw["token_endpoint_auth_method"].(string)
	if got := corosMcpEffectiveAuthMethod(registered, clientSecret); got != "none" {
		t.Fatalf("effective auth method = %q, want none", got)
	}
}

func TestCorosMcpEffectiveAuthMethod(t *testing.T) {
	cases := []struct{ method, secret, want string }{
		{"client_secret_basic", "", "none"}, // v868 正式站誤存的列：要求 basic 但 COROS 沒發 secret
		{"client_secret_basic", "s3cr3t", "client_secret_basic"},
		{"none", "", "none"},
		{"none", "s3cr3t", "none"},
		{"", "", "none"},
		{"client_secret_post", "s3cr3t", "none"}, // 未支援的方法一律退回 public
	}
	for _, c := range cases {
		if got := corosMcpEffectiveAuthMethod(c.method, c.secret); got != c.want {
			t.Errorf("corosMcpEffectiveAuthMethod(%q, secret=%v) = %q, want %q", c.method, c.secret != "", got, c.want)
		}
	}
}

// TestExchangeCode_LegacyRowNoBasicHeader：重現 v868 根因——DB 存 client_secret_basic＋無 secret。
// 修正後不得送 Authorization 標頭，body 必須帶 client_id＋code_verifier＋同一個 redirect_uri。
func TestExchangeCode_LegacyRowNoBasicHeader(t *testing.T) {
	var calls []fakeTokenCall
	h := newTestCorosMcpHandlerWithHandler(fakeCorosSpringAS(t, true, &calls))
	legacy := &corosMcpClient{ClientID: "cid-123", AuthMethod: "client_secret_basic", ClientSecret: ""}
	tok, err := h.exchangeCode(context.Background(), fakeCorosBase+"/oauth2/token", legacy, "code-abc", "verifier-xyz")
	if err != nil {
		t.Fatalf("exchangeCode should succeed for a public client, got %v", err)
	}
	if tok.AccessToken != "at-1" || tok.RefreshToken != "rt-1" {
		t.Fatalf("unexpected tokens: %+v", tok)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 token call, got %d", len(calls))
	}
	c := calls[0]
	if c.authHeader != "" {
		t.Fatalf("public client 不得送 Authorization 標頭，got %q", c.authHeader)
	}
	if c.form.Get("client_id") != "cid-123" || c.form.Get("code_verifier") != "verifier-xyz" || c.form.Get("code") != "code-abc" {
		t.Fatalf("token form missing fields: %v", c.form)
	}
	if c.form.Get("redirect_uri") != h.cfg.RedirectURI || c.form.Get("grant_type") != "authorization_code" {
		t.Fatalf("token form redirect_uri/grant_type wrong: %v", c.form)
	}
}

// TestExchangeCode_NoRefreshTokenStillConnects：COROS 不發 refresh token 給 public client 時，連線仍要成功。
func TestExchangeCode_NoRefreshTokenStillConnects(t *testing.T) {
	var calls []fakeTokenCall
	h := newTestCorosMcpHandlerWithHandler(fakeCorosSpringAS(t, false, &calls))
	client := &corosMcpClient{ClientID: "cid-123", AuthMethod: "none"}
	tok, err := h.exchangeCode(context.Background(), fakeCorosBase+"/oauth2/token", client, "code-abc", "verifier-xyz")
	if err != nil {
		t.Fatalf("missing refresh_token must not fail the connection: %v", err)
	}
	if tok.AccessToken == "" || tok.RefreshToken != "" {
		t.Fatalf("unexpected tokens: %+v", tok)
	}
}

// TestRefreshToken_NoRefreshTokenNeedsReconnect：沒有 refresh token 時不打 COROS，直接回 errCorosMcpReconnect。
func TestRefreshToken_NoRefreshTokenNeedsReconnect(t *testing.T) {
	h := newTestCorosMcpHandler() // repo=nil：若真的往下走去查 DB 會 panic，等於斷言「沒有往下走」
	conn := &corosMcpConnection{Issuer: "https://mcpus.coros.com", AccessToken: "old", RefreshToken: "", ExpiresAt: time.Now().Add(-time.Minute)}
	if err := h.refreshToken(context.Background(), conn); !errors.Is(err, errCorosMcpReconnect) {
		t.Fatalf("expected errCorosMcpReconnect, got %v", err)
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
	// 但要帶出 OAuth 錯誤碼，log 才看得出 COROS 拒絕的原因
	var te *corosMcpTokenError
	if !errors.As(err, &te) || te.Code != "invalid_grant" || te.Status != http.StatusBadRequest {
		t.Fatalf("expected *corosMcpTokenError{400 invalid_grant}, got %#v", err)
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("error string should include the OAuth error code: %v", err)
	}
}

func TestCorosMcpParseOAuthError_SanitizesCodeAndDescription(t *testing.T) {
	e := corosMcpParseOAuthError(400, []byte(`{"error":"Invalid Grant<script>","error_description":"line1\nline2 `+strings.Repeat("x", 300)+`"}`))
	if e.Code != "" {
		t.Fatalf("non-[a-z_] error code must be dropped, got %q", e.Code)
	}
	if strings.Contains(e.Description, "\n") || len([]rune(e.Description)) > 161 {
		t.Fatalf("description must be single-line and truncated, got %d runes", len([]rune(e.Description)))
	}
	if e2 := corosMcpParseOAuthError(502, []byte("<html>bad gateway</html>")); e2.Code != "" || e2.Status != 502 {
		t.Fatalf("non-JSON body should yield status only, got %#v", e2)
	}
}
