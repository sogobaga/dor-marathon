package integration

// COROS MCP 協定用戶端：無狀態 JSON-RPC over HTTP（契約第 7 點）。
// 規格依據：A_protocol_spec.md §4、COROS_MCP_SPEC_DRAFT.md §3（皆為交叉比對
// repo/skill/coros_mcp_login_gateway/scripts/coros_mcp_login.py 的推論，非官方文件）。
//
// 關鍵行為：
//   - 每次邏輯操作（tools/list、tools/call）前都重送一次 initialize（無狀態流程，沒有可延續的 session）。
//   - 回應可能是純 JSON，也可能是 SSE 框起來的 data: 事件——取「最後一個」事件（login.py 的既有行為）。
//   - 401 代表 access token 失效：换 refresh token 重試一次（不無限重試），由 withMCP 統一處理。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// corosMcpMaxToolPages tools/list 分頁最多讀幾頁（實測 34 個工具單頁就夠；上限只是防呆）。
const corosMcpMaxToolPages = 50

// errMCPUnauthorized：mcpCall 偵測到 HTTP 401 時回傳的哨兵錯誤，供 withMCP 判斷是否該刷新重試。
var errMCPUnauthorized = errors.New("coros mcp: unauthorized")

// corosMcpHTTPError：MCP 端點回非 200／401 的 HTTP 狀態（訊息只帶方法與狀態碼，不帶回應原文）。
// 型別化是為了讓同步流程能辨識 429／5xx → 該使用者冷卻 30 分鐘（見 isCorosThrottleErr）。
type corosMcpHTTPError struct {
	Method string
	Status int
}

func (e *corosMcpHTTPError) Error() string {
	return fmt.Sprintf("coros mcp %s http %d", e.Method, e.Status)
}

type mcpJSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type mcpJSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpJSONRPCResponse struct {
	Result json.RawMessage  `json:"result"`
	Error  *mcpJSONRPCError `json:"error"`
}

// mcpTool 對應 tools/list 單筆（inputSchema 整包保留，第一階段只用來猜 querySportRecords 的日期欄位名）。
type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type mcpToolCallResult struct {
	IsError bool `json:"isError"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// corosMcpParseSSEData 純函式：解析 SSE 框起來的回應，取「最後一個」data 事件原文（比照
// login.py SimpleHttpClient.read_json 的既有行為——較早的事件若存在會被忽略）。
func corosMcpParseSSEData(body []byte) ([]byte, error) {
	lines := strings.Split(string(body), "\n")
	var events [][]string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			events = append(events, cur)
			cur = nil
		}
	}
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			cur = append(cur, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	flush()
	if len(events) == 0 {
		return nil, fmt.Errorf("SSE 回應沒有任何 data 事件")
	}
	return []byte(strings.Join(events[len(events)-1], "\n")), nil
}

// corosMcpParseResponseBody 依 Content-Type 分流：text/event-stream 走 SSE 解析，其餘當純 JSON。
func corosMcpParseResponseBody(contentType string, body []byte) ([]byte, error) {
	if strings.Contains(contentType, "text/event-stream") {
		return corosMcpParseSSEData(body)
	}
	return body, nil
}

// mcpCall 送一次 JSON-RPC 請求；401 回傳 errMCPUnauthorized（不在這裡重試，由 withMCP 處理），
// 其餘非 200 或 JSON-RPC error 都包成一般 error（訊息不帶回應原文，避免意外洩漏 token/使用者資料到 log）。
func (h *CorosMcpHandler) mcpCall(ctx context.Context, mcpURL, accessToken, method string, params any, id int) (*mcpJSONRPCResponse, error) {
	reqBody, err := json.Marshal(mcpJSONRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errMCPUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &corosMcpHTTPError{Method: method, Status: resp.StatusCode}
	}
	data, err := corosMcpParseResponseBody(resp.Header.Get("Content-Type"), body)
	if err != nil {
		return nil, err
	}
	var out mcpJSONRPCResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode mcp %s response: %w", method, err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("mcp %s error %d: %s", method, out.Error.Code, out.Error.Message)
	}
	return &out, nil
}

func mcpInitializeParams() map[string]any {
	return map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "DOR", "version": "1.0.0"},
	}
}

// withMCP 跑一次「initialize → fn」的邏輯操作；401 時刷新 token 一次並整個重跑一次（不無限重試，
// 契約第 7 點：「401 → 用 refresh token 換新一次再重試」）。fn 失敗（非 401）直接回傳，不重試。
func (h *CorosMcpHandler) withMCP(ctx context.Context, conn *corosMcpConnection, fn func(mcpURL, accessToken string) error) error {
	mcpURL := strings.TrimRight(conn.Issuer, "/") + "/mcp"
	attempt := func() error {
		if _, err := h.mcpCall(ctx, mcpURL, conn.AccessToken, "initialize", mcpInitializeParams(), 1); err != nil {
			return err
		}
		return fn(mcpURL, conn.AccessToken)
	}
	err := attempt()
	if errors.Is(err, errMCPUnauthorized) {
		if rerr := h.refreshToken(ctx, conn); rerr != nil {
			return fmt.Errorf("mcp 401 後刷新 token 失敗: %w", rerr)
		}
		return attempt()
	}
	return err
}

// toolsList 呼叫 tools/list，依 nextCursor 分頁直到抓完（契約第 8 點：「存完整清單含 inputSchema」）。
func (h *CorosMcpHandler) toolsList(ctx context.Context, conn *corosMcpConnection) ([]mcpTool, error) {
	var tools []mcpTool
	err := h.withMCP(ctx, conn, func(mcpURL, accessToken string) error {
		cursor := ""
		id := 2
		// 頁數上限：對端若一直回 nextCursor（故障或惡意）也不會無限迴圈／無限打 COROS（稽核 low）。
		for page := 0; ; page++ {
			if page >= corosMcpMaxToolPages {
				return fmt.Errorf("tools/list pagination exceeded %d pages", corosMcpMaxToolPages)
			}
			params := map[string]any{}
			if cursor != "" {
				params["cursor"] = cursor
			}
			resp, err := h.mcpCall(ctx, mcpURL, accessToken, "tools/list", params, id)
			if err != nil {
				return err
			}
			var r struct {
				Tools      []mcpTool `json:"tools"`
				NextCursor string    `json:"nextCursor"`
			}
			if err := json.Unmarshal(resp.Result, &r); err != nil {
				return fmt.Errorf("decode tools/list result: %w", err)
			}
			tools = append(tools, r.Tools...)
			if r.NextCursor == "" {
				return nil
			}
			cursor = r.NextCursor
			id++
		}
	})
	return tools, err
}

// toolsCall 呼叫單一 tool（契約只允許 corosMcpProbeTools 這份清單；呼叫端負責不傳入寫入類工具名）。
func (h *CorosMcpHandler) toolsCall(ctx context.Context, conn *corosMcpConnection, name string, args map[string]any) (*mcpToolCallResult, error) {
	var out *mcpToolCallResult
	err := h.withMCP(ctx, conn, func(mcpURL, accessToken string) error {
		resp, err := h.mcpCall(ctx, mcpURL, accessToken, "tools/call", map[string]any{"name": name, "arguments": args}, 3)
		if err != nil {
			return err
		}
		var r mcpToolCallResult
		if err := json.Unmarshal(resp.Result, &r); err != nil {
			return fmt.Errorf("decode tools/call result: %w", err)
		}
		if r.IsError {
			msg := ""
			if len(r.Content) > 0 {
				msg = r.Content[0].Text
			}
			out = &r
			return fmt.Errorf("tool %s returned isError: %s", name, msg)
		}
		out = &r
		return nil
	})
	return out, err
}
