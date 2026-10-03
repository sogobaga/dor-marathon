package integration

// 呼叫 Garmin 的 HTTP 用戶端：換／刷新 token、取 userId、取權限、撤銷註冊。
//
// 只打 token 端點與 user 端點，**不打任何資料端點**（Garmin 不允許隨選取資料）。所有請求走注入的 http.Client
// （測試用以 host 分流的 in-process RoundTripper），逾時 15 秒、不跟隨導向。日誌紀律：只記 HTTP 狀態與 OAuth
// 標準錯誤碼，不記 token、不記回應內文、不記完整 userId。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// garminAPIError：呼叫 Garmin 失敗（HTTP 非 2xx，或回應不合預期）。Status=0 代表網路層錯誤／逾時。
type garminAPIError struct {
	Op     string // token／user_id／permissions／registration
	Status int
	Code   string // OAuth error code（已驗證只含 [a-z_]）
	Err    error  // 底層錯誤（網路層）
}

func (e *garminAPIError) Error() string {
	s := fmt.Sprintf("garmin %s: http %d", e.Op, e.Status)
	if e.Code != "" {
		s += " " + e.Code
	}
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
}

func (e *garminAPIError) Unwrap() error { return e.Err }

func init() {
	garminAPIStatusOf = func(err error) int {
		var ae *garminAPIError
		if errors.As(err, &ae) {
			return ae.Status
		}
		return 0
	}
}

// 錯誤分類（供 ensureFresh、dereg 實測、斷線使用）。
var (
	// errGarminReauth：授權已失效，只能請使用者重新授權（refresh 被拒 invalid_grant、refresh 過期、已標記）。
	errGarminReauth = errors.New("garmin: reauthorization required")
	// errGarminGeneration：連線屬於舊的 App 世代（換 client id 之後），不打 token 端點；需使用者重新授權。
	errGarminGeneration = errors.New("garmin: connection belongs to a previous app generation")
	// errGarminConfig：設定錯誤（invalid_client 等），不是使用者的問題，不標記使用者。
	errGarminConfig = errors.New("garmin: client configuration error")
	// errGarminTransient：暫時性失敗（429／5xx／逾時／拿不到刷新鎖），不改任何狀態，稍後重試。
	errGarminTransient = errors.New("garmin: transient failure")
)

func garminIsTransientStatus(status int) bool {
	return status == 0 || status == http.StatusTooManyRequests || status >= 500
}

// garminTokenResponse：token 端點回應（只取需要的欄位）。
type garminTokenResponse struct {
	AccessToken      string
	RefreshToken     string
	ExpiresIn        int64
	RefreshExpiresIn int64
	Scope            string
}

var oauthCodeRe = regexp.MustCompile(`^[a-z_]{1,40}$`)

// garminReadErrorCode 從錯誤回應抽 OAuth error 欄位（驗證格式後才採用；其餘一律丟棄）。
func garminReadErrorCode(body []byte) string {
	var m struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &m) != nil {
		return ""
	}
	if c := strings.TrimSpace(m.Error); oauthCodeRe.MatchString(c) {
		return c
	}
	return ""
}

// garminHostOK 驗證 Garmin 端點網址：https，且 host 為 garmin.com 或其子網域；非 production（ENV!=production）
// 才允許覆寫成其他主機（僅供測試／沙盒），production 一律拒絕，避免設定被改成導向他處。
func garminHostOK(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	official := u.Scheme == "https" && (host == "garmin.com" || strings.HasSuffix(host, ".garmin.com"))
	if official {
		return true
	}
	return !strings.EqualFold(os.Getenv("ENV"), "production")
}

func (h *GarminHandler) doJSON(ctx context.Context, op string, req *http.Request, maxBody int64) (int, []byte, error) {
	req = req.WithContext(ctx)
	req.Header.Set("Accept", "application/json")
	resp, err := h.hc.Do(req)
	if err != nil {
		return 0, nil, &garminAPIError{Op: op, Err: sanitizeNetErr(err)}
	}
	defer resp.Body.Close()
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if rerr != nil {
		return resp.StatusCode, nil, &garminAPIError{Op: op, Status: 0, Err: sanitizeNetErr(rerr)}
	}
	return resp.StatusCode, body, nil
}

// sanitizeNetErr：網路層錯誤只留型別資訊（url.Error 會帶完整網址，網址可能含查詢參數）。
func sanitizeNetErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if isTimeoutErr(ue.Err) {
			return context.DeadlineExceeded
		}
		return errors.New("network error")
	}
	return err
}

// tokenRequest POST token 端點。憑證送法依 GARMIN_TOKEN_AUTH（basic：Authorization 標頭；body：form 欄位）。
func (h *GarminHandler) tokenRequest(ctx context.Context, form url.Values) (*garminTokenResponse, error) {
	if h.cfg.TokenAuth == "body" {
		form.Set("client_id", h.cfg.ClientID)
		form.Set("client_secret", h.cfg.ClientSecret)
	}
	req, err := http.NewRequest(http.MethodPost, h.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, &garminAPIError{Op: "token", Err: errors.New("bad request")}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if h.cfg.TokenAuth != "body" {
		req.SetBasicAuth(h.cfg.ClientID, h.cfg.ClientSecret)
	}
	status, body, err := h.doJSON(ctx, "token", req, 1<<20)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &garminAPIError{Op: "token", Status: status, Code: garminReadErrorCode(body)}
	}
	var m struct {
		AccessToken      string    `json:"access_token"`
		RefreshToken     string    `json:"refresh_token"`
		ExpiresIn        garminNum `json:"expires_in"`
		RefreshExpiresIn garminNum `json:"refresh_token_expires_in"`
		Scope            garminStr `json:"scope"`
	}
	if err := json.Unmarshal(body, &m); err != nil || strings.TrimSpace(m.AccessToken) == "" {
		return nil, &garminAPIError{Op: "token", Status: status, Code: "bad_response"}
	}
	out := &garminTokenResponse{AccessToken: strings.TrimSpace(m.AccessToken), RefreshToken: strings.TrimSpace(m.RefreshToken), Scope: string(m.Scope)}
	if m.ExpiresIn.OK && m.ExpiresIn.V > 0 {
		out.ExpiresIn = int64(m.ExpiresIn.V)
	}
	if m.RefreshExpiresIn.OK && m.RefreshExpiresIn.V > 0 {
		out.RefreshExpiresIn = int64(m.RefreshExpiresIn.V)
	}
	return out, nil
}

// exchangeCode 以授權碼換 token（PKCE：帶 code_verifier；redirect_uri 與授權請求相同）。
func (h *GarminHandler) exchangeCode(ctx context.Context, code, verifier string) (*garminTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", h.cfg.RedirectURI)
	return h.tokenRequest(ctx, form)
}

// refreshToken 以 refresh token 換新（Garmin 每次都回新的 refresh token）。
func (h *GarminHandler) refreshToken(ctx context.Context, refresh string) (*garminTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refresh)
	return h.tokenRequest(ctx, form)
}

func (h *GarminHandler) userGET(ctx context.Context, op, path, access string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(h.cfg.APIBase, "/")+path, nil)
	if err != nil {
		return 0, nil, &garminAPIError{Op: op, Err: errors.New("bad request")}
	}
	req.Header.Set("Authorization", "Bearer "+access)
	return h.doJSON(ctx, op, req, 1<<20)
}

// getUserID GET /partner-gateway/rest/user/id → userId（當不透明字串，≤64 字元）。
func (h *GarminHandler) getUserID(ctx context.Context, access string) (string, error) {
	status, body, err := h.userGET(ctx, "user_id", "/partner-gateway/rest/user/id", access)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", &garminAPIError{Op: "user_id", Status: status, Code: garminReadErrorCode(body)}
	}
	var m struct {
		UserID garminStr `json:"userId"`
	}
	id := ""
	if json.Unmarshal(body, &m) == nil {
		id = string(m.UserID)
	}
	if id == "" || len(id) > garminUserIDMax {
		return "", &garminAPIError{Op: "user_id", Status: status, Code: "bad_response"}
	}
	return id, nil
}

var permTokenRe = regexp.MustCompile(`"([A-Z][A-Z_]{2,39})"`)

// getPermissions GET /partner-gateway/rest/user/permissions → 權限字串清單。容忍怪異的 JSON：先試字串陣列，
// 失敗再以正則抽出所有大寫底線字串。最多 16 個。
func (h *GarminHandler) getPermissions(ctx context.Context, access string) ([]string, error) {
	status, body, err := h.userGET(ctx, "permissions", "/partner-gateway/rest/user/permissions", access)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &garminAPIError{Op: "permissions", Status: status, Code: garminReadErrorCode(body)}
	}
	return garminParsePermissionList(body), nil
}

func garminParsePermissionList(body []byte) []string {
	var arr []string
	if err := json.Unmarshal(body, &arr); err == nil {
		return garminParsePermissions(mustRaw(arr))
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range permTokenRe.FindAllSubmatch(body, -1) {
		p := string(m[1])
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
			if len(out) >= 16 {
				break
			}
		}
	}
	return out
}

func mustRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// deleteRegistration DELETE /partner-gateway/rest/user/registration。成功（2xx）或「已經沒有註冊／授權已失效」
// （401／403／404）都回 nil——目的（Garmin 端不再推送）已達成；其餘錯誤回 garminAPIError。
func (h *GarminHandler) deleteRegistration(ctx context.Context, access string) error {
	req, err := http.NewRequest(http.MethodDelete, strings.TrimRight(h.cfg.APIBase, "/")+"/partner-gateway/rest/user/registration", nil)
	if err != nil {
		return &garminAPIError{Op: "registration", Err: errors.New("bad request")}
	}
	req.Header.Set("Authorization", "Bearer "+access)
	status, _, derr := h.doJSON(ctx, "registration", req, 64<<10)
	if derr != nil {
		return derr
	}
	switch {
	case status >= 200 && status < 300, status == http.StatusUnauthorized, status == http.StatusForbidden, status == http.StatusNotFound:
		return nil
	}
	return &garminAPIError{Op: "registration", Status: status}
}

// garminIsRevokedStatus：user 端點回這些狀態代表 token 已失效或使用者已撤回同意（dereg 實測的「確認撤銷」）。
func garminIsRevokedErr(err error) bool {
	var ae *garminAPIError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.Status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		return true
	}
	return false
}

func garminAPITimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 15*time.Second)
}
