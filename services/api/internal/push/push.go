// Package push 實作 Web Push（VAPID）：使用者訂閱管理 + 後台廣播推播。
// 契約：訂閱物件＝瀏覽器 PushSubscription JSON { endpoint, keys:{p256dh,auth} }。
// 未設齊 VAPID_PUBLIC_KEY / VAPID_PRIVATE_KEY / VAPID_SUBJECT 時 enabled()=false，
// /push/vapid 回 enabled:false，發送一律 no-op（不報錯）。
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/mailer"
)

// M6 資安修補：push_subscriptions.endpoint 是使用者瀏覽器 PushManager.subscribe() 回傳的
// URL，Subscribe 原本完全不驗證就存進 DB；h.send() 之後會直接對這個 URL 發 HTTP 請求
// （帶 VAPID 簽章 body）—— 等於任何登入使用者都能把後端變成一台可控 SSRF 代理，打內網
// 位址、雲端 metadata 服務、或任何第三方站台。
//
// 修法：只放行各大瀏覽器/OS 廠商已公開文件的推播閘道網域，訂閱當下（Subscribe）與
// 每次真正送出前（send）都各驗一次；沒有 "*." 前綴的三個是要求「完全相符」，其餘允許
// 該網域本身或任一層子網域（用 isAllowedPushHost 判斷，而非裸 strings.HasSuffix，避免
// "evilpush.apple.com"（沒有前置的點）之類的尾碼混淆誤判為合法）。
var allowedPushHostSuffixes = []string{
	"fcm.googleapis.com",                // 完全相符：Chrome/Edge/Android (FCM)
	"android.googleapis.com",            // 完全相符：Android (FCM 舊端點)
	"updates.push.services.mozilla.com", // 完全相符：Firefox（雖然下面的萬用字元也涵蓋，明列求一致）
	".push.services.mozilla.com",        // 萬用字元：Firefox 其餘推播節點
	".notify.windows.com",               // 萬用字元：Windows (WNS)
	".push.apple.com",                   // 萬用字元：Safari/iOS (APNs Web Push)，涵蓋 web.push.apple.com
}

// errInvalidPushEndpoint：訂閱端點格式不合法或主機名稱不在白名單內。
var errInvalidPushEndpoint = errors.New("不支援的推播服務端點")

// noRedirectHTTPClient：webpush-go 沒帶 Options.HTTPClient 時會退回零值 *http.Client{}，
// 而零值 client 預設會自動跟隨最多 10 次 HTTP 轉址——上面 send() 對 host 白名單/DNS 私有位址
// 的檢查都只驗證了「請求當下這個 URL」，若白名單網域（或其 DNS）被騙／被攻破而回一個 3xx，
// 轉址目標完全沒有經過同樣的檢查就被直接跟去，等於繞過整套 M6 SSRF 防護。這裡改用
// CheckRedirect 回傳 http.ErrUseLastResponse，讓轉址回應本身被當成最終回應處理（webpush-go
// 只看狀態碼決定刪除/重試，3xx 落入下面 send() 的 `resp.StatusCode >= 300` 分支回錯，
// 不會真的去 fetch Location）。
var noRedirectHTTPClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// isAllowedPushHost 判斷 host（已含 port 會先被 url.URL.Hostname() 去除）是否落在白名單內。
func isAllowedPushHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return false
	}
	for _, suffix := range allowedPushHostSuffixes {
		if strings.HasPrefix(suffix, ".") {
			if host == suffix[1:] || strings.HasSuffix(host, suffix) {
				return true
			}
		} else if host == suffix {
			return true
		}
	}
	return false
}

// validatePushEndpoint 只做「格式＋主機名稱白名單」的靜態檢查：必須是 https，且 host
// 通過 isAllowedPushHost。DNS 解析＋私有位址檢查留到真正要送出時（send()）才做——訂閱
// 當下就查 DNS 一來沒必要拖慢使用者操作，二來 send() 本來就會查一次，沒理由查兩次。
func validatePushEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" {
		return errInvalidPushEndpoint
	}
	if !isAllowedPushHost(u.Hostname()) {
		return errInvalidPushEndpoint
	}
	return nil
}

// isSafePushHost 額外解析 DNS，確認**所有**回傳的 IP 都不是私有(RFC1918)／迴圈／連結本地
// (link-local)／CGNAT(100.64.0.0/10) 位址（M6 深度防禦）：即使主機名稱通過白名單，也不該
// 讓後端對內部位址發起請求——例如白名單網域的 DNS 遭劫持/快取污染，或部署環境的 DNS 解析異常。
// 只在 send() 真正要發送前呼叫（訂閱當下不查，見 validatePushEndpoint 註解）。
func isSafePushHost(ctx context.Context, host string) bool {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !isPublicIP(ip.IP) {
			return false
		}
	}
	return true
}

// isPublicIP 排除私有網段／迴圈／連結本地／未指定／多播位址；net.IP 的 IsPrivate 已涵蓋
// RFC1918（10/8、172.16/12、192.168/16）與 fc00::/7，這裡額外手動排除 CGNAT
// (100.64.0.0/10)——Go 標準庫沒有內建判斷。
func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
		return false // 100.64.0.0/10 CGNAT（Carrier-Grade NAT，供應商內部位址）
	}
	return true
}

// Config VAPID 設定，全來自環境變數（由 cmd/api/main.go 傳入）。
type Config struct {
	PublicKey  string
	PrivateKey string
	Subject    string // mailto: 或 https 開頭
}

func (c Config) enabled() bool {
	return c.PublicKey != "" && c.PrivateKey != "" && c.Subject != ""
}

// Subscription 訂閱物件（對應瀏覽器 PushSubscription JSON）。
type Subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// PushMessage 送到 Service Worker 的推播內容 payload。
type PushMessage struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url,omitempty"`
	Icon  string `json:"icon,omitempty"`
}

// MailInserter 站內信寫入介面（由 internal/mail.Handler 實作）。
// 用小介面而非直接 import internal/mail，避免 push ↔ mail 循環相依。
type MailInserter interface {
	InsertForUsers(ctx context.Context, userIDs []string, level, title, body, url string) (int, error)
}

// Handler 掛載 /push（需登入）、/admin/push（需 settings 權限）與
// /admin/push-groups（帳號群組 CRUD，見 groups.go）路由。
type Handler struct {
	db     *pgxpool.Pool
	cfg    Config
	mailer *mailer.Mailer
	mail   MailInserter
}

// NewHandler 建立 push Handler。mailerInst 未設 SMTP env 時內部自動 no-op。
// mailInserter 可為 nil（此時 broadcast 的 mail 頻道略過，MailSent=0）。
func NewHandler(db *pgxpool.Pool, cfg Config, mailerInst *mailer.Mailer, mailInserter MailInserter) *Handler {
	return &Handler{db: db, cfg: cfg, mailer: mailerInst, mail: mailInserter}
}

// Router 需登入子路由：掛在 /api/v1/push。
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/vapid", h.GetVAPID)
	r.Post("/subscribe", h.Subscribe)
	r.Post("/unsubscribe", h.Unsubscribe)
	return r
}

// AdminRouter 後台子路由：掛在 /api/v1/admin/push（外層已檢查 settings 權限）。
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Post("/broadcast", h.Broadcast)
	return r
}

// GET /api/v1/push/vapid
func (h *Handler) GetVAPID(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{
		"public_key": h.cfg.PublicKey,
		"enabled":    h.cfg.enabled(),
	})
}

// POST /api/v1/push/subscribe
func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var sub Subscription
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if sub.Endpoint == "" || sub.Keys.P256dh == "" || sub.Keys.Auth == "" {
		respondErr(w, http.StatusBadRequest, "missing endpoint or keys")
		return
	}
	// M6 資安修補：見檔頭 validatePushEndpoint 註解。
	if err := validatePushEndpoint(sub.Endpoint); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}

	_, err := h.db.Exec(r.Context(), `
		INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (endpoint) DO UPDATE
		SET user_id = EXCLUDED.user_id, p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth
	`, userID, sub.Endpoint, sub.Keys.P256dh, sub.Keys.Auth)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save subscription")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/push/unsubscribe
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}

	_, err := h.db.Exec(r.Context(), `
		DELETE FROM push_subscriptions WHERE user_id = $1 AND endpoint = $2
	`, userID, body.Endpoint)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to remove subscription")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/admin/push/broadcast
// body: { title, body, url?, channels:["push","email","mail"], target_type:"all|user|race|group",
//         identifier?, race_id?, group_id?, level?(mail only) }
func (h *Handler) Broadcast(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title      string   `json:"title"`
		Body       string   `json:"body"`
		URL        string   `json:"url,omitempty"`
		Channels   []string `json:"channels"`
		TargetType string   `json:"target_type"`
		Identifier string   `json:"identifier,omitempty"`
		RaceID     string   `json:"race_id,omitempty"`
		GroupID    string   `json:"group_id,omitempty"`
		Level      string   `json:"level"` // 僅 mail 頻道用：normal(預設)/important/urgent
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}

	userIDs, isAll, err := h.resolveBroadcastTargets(r.Context(), body.TargetType, body.Identifier, body.RaceID, body.GroupID)
	if err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}

	recipients := len(userIDs)
	if isAll {
		// 虛擬選手(is_virtual)不參與站內信/推播/Email 全體廣播
		if err := h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM users WHERE NOT is_virtual`).Scan(&recipients); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to count recipients")
			return
		}
	}

	pushSent, pushFailed := 0, 0
	if slices.Contains(body.Channels, "push") && h.cfg.enabled() {
		targetIDs := userIDs
		if isAll {
			targetIDs = nil // listSubscriptions 空陣列＝全部
		}
		subs, err := h.listSubscriptions(r.Context(), targetIDs)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to load subscriptions")
			return
		}

		msg := PushMessage{Title: body.Title, Body: body.Body, URL: body.URL}
		payload, err := json.Marshal(msg)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to encode payload")
			return
		}

		for _, s := range subs {
			if err := h.send(r.Context(), s, payload); err != nil {
				pushFailed++
			} else {
				pushSent++
			}
		}
	}

	emailSent, emailFailed := 0, 0
	if slices.Contains(body.Channels, "email") {
		emails, err := h.listEmails(r.Context(), userIDs, isAll)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to load emails")
			return
		}
		htmlBody := buildBroadcastEmailHTML(body.Title, body.Body, body.URL)
		emailSent, emailFailed = h.mailer.Send(r.Context(), emails, body.Title, htmlBody)
	}

	mailSent := 0
	if slices.Contains(body.Channels, "mail") && h.mail != nil {
		ids, err := h.listUserIDs(r.Context(), userIDs, isAll)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to load recipients")
			return
		}
		n, err := h.mail.InsertForUsers(r.Context(), ids, body.Level, body.Title, body.Body, body.URL)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to send mail")
			return
		}
		mailSent = n
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"recipients":   recipients,
		"push_sent":    pushSent,
		"push_failed":  pushFailed,
		"email_sent":   emailSent,
		"email_failed": emailFailed,
		"mail_sent":    mailSent,
	})
}

// listUserIDs 目標對象的去重 user_id 清單。isAll=true 時查全部 users
// （虛擬選手(is_virtual)不參與站內信/推播全體廣播，排除之）。
func (h *Handler) listUserIDs(ctx context.Context, userIDs []string, isAll bool) ([]string, error) {
	if !isAll {
		return userIDs, nil
	}
	rows, err := h.db.Query(ctx, `SELECT id::text FROM users WHERE NOT is_virtual`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// resolveBroadcastTargets 依 target_type 解析出目標 user_id 清單。
// target_type=all 時 userIDs 恆為空、isAll=true（呼叫端各自決定「空＝全部」的查法）。
// 目標無效或必填參數缺漏回 error（呼叫端轉 400）。
func (h *Handler) resolveBroadcastTargets(ctx context.Context, targetType, identifier, raceID, groupID string) (userIDs []string, isAll bool, err error) {
	switch targetType {
	case "all":
		return nil, true, nil

	case "user":
		if strings.TrimSpace(identifier) == "" {
			return nil, false, errors.New("identifier required")
		}
		userID, found, err := h.resolveIdentifier(ctx, identifier)
		if err != nil {
			return nil, false, err
		}
		if !found {
			return nil, false, errors.New("user not found")
		}
		return []string{userID}, false, nil

	case "race":
		if strings.TrimSpace(raceID) == "" {
			return nil, false, errors.New("race_id required")
		}
		rows, err := h.db.Query(ctx, `
			SELECT DISTINCT user_id::text FROM registrations
			WHERE race_id = $1 AND status <> 'cancelled'
		`, raceID)
		if err != nil {
			return nil, false, err
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, false, err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
		return ids, false, nil

	case "group":
		if strings.TrimSpace(groupID) == "" {
			return nil, false, errors.New("group_id required")
		}
		rows, err := h.db.Query(ctx, `SELECT user_id::text FROM account_group_members WHERE group_id = $1`, groupID)
		if err != nil {
			return nil, false, err
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, false, err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
		return ids, false, nil

	default:
		return nil, false, errors.New("invalid target_type")
	}
}

// listEmails 撈目標對象的 email（非空者）。isAll=true 時查全部 users
// （虛擬選手(is_virtual)不參與 Email 全體廣播，排除之）。
func (h *Handler) listEmails(ctx context.Context, userIDs []string, isAll bool) ([]string, error) {
	var rows pgx.Rows
	var err error
	if isAll {
		rows, err = h.db.Query(ctx, `SELECT email FROM users WHERE COALESCE(email,'') <> '' AND NOT is_virtual`)
	} else {
		if len(userIDs) == 0 {
			return nil, nil
		}
		rows, err = h.db.Query(ctx, `SELECT email FROM users WHERE id = ANY($1) AND COALESCE(email,'') <> ''`, userIDs)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var emails []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		emails = append(emails, e)
	}
	return emails, rows.Err()
}

// buildBroadcastEmailHTML 組簡單 HTML 郵件內容（品牌 + 標題 + 內文 + 選填連結按鈕）。
func buildBroadcastEmailHTML(title, bodyText, url string) string {
	var linkHTML string
	if url != "" {
		linkHTML = fmt.Sprintf(
			`<p style="margin-top:24px;"><a href="%s" style="display:inline-block;padding:10px 20px;background:#c9a227;color:#fff;text-decoration:none;border-radius:6px;">查看詳情</a></p>`,
			html.EscapeString(url),
		)
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html><body style="font-family:sans-serif;color:#222;">
<p style="color:#888;font-size:12px;letter-spacing:1px;">DOR 城市探索</p>
<h2 style="margin:8px 0;">%s</h2>
<p style="white-space:pre-wrap;line-height:1.6;">%s</p>
%s
</body></html>`, html.EscapeString(title), html.EscapeString(bodyText), linkHTML)
}

// storedSubscription 內部查詢結果（含 id/user_id 供刪除失效訂閱用）。
type storedSubscription struct {
	ID       string
	UserID   string
	Endpoint string
	P256dh   string
	Auth     string
}

func (h *Handler) listSubscriptions(ctx context.Context, targetUserIDs []string) ([]storedSubscription, error) {
	var rows pgx.Rows
	var err error
	if len(targetUserIDs) == 0 {
		rows, err = h.db.Query(ctx, `SELECT id, user_id, endpoint, p256dh, auth FROM push_subscriptions`)
	} else {
		rows, err = h.db.Query(ctx, `
			SELECT id, user_id, endpoint, p256dh, auth FROM push_subscriptions
			WHERE user_id = ANY($1)
		`, targetUserIDs)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []storedSubscription
	for rows.Next() {
		var s storedSubscription
		if err := rows.Scan(&s.ID, &s.UserID, &s.Endpoint, &s.P256dh, &s.Auth); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SendToUser 推播給單一使用者的所有訂閱裝置（給其他模組呼叫，例如任務完成通知）。
// enabled()=false 時直接 no-op。
func (h *Handler) SendToUser(ctx context.Context, userID string, msg PushMessage) error {
	if !h.cfg.enabled() {
		return nil
	}

	subs, err := h.listSubscriptions(ctx, []string{userID})
	if err != nil {
		return err
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	var firstErr error
	for _, s := range subs {
		if err := h.send(ctx, s, payload); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// send 送出單一訂閱的推播；端點回 404/410 表示訂閱已失效，從 DB 刪除。
func (h *Handler) send(ctx context.Context, s storedSubscription, payload []byte) error {
	if !h.cfg.enabled() {
		return nil
	}

	// M6 資安修補：即使訂閱當下已通過 validatePushEndpoint，DB 裡也可能還有
	// migration 170 上線前留下的舊資料（或未來繞過驗證寫入的資料）；送出前一律再驗一次
	// 主機名稱白名單，格式/白名單不過直接視為失效訂閱一併刪除（不可能靠重試變合法）。
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Scheme != "https" || !isAllowedPushHost(u.Hostname()) {
		_, _ = h.db.Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1`, s.ID)
		return errInvalidPushEndpoint
	}
	// 再解析 DNS 排除私有/迴圈/連結本地/CGNAT 位址（深度防禦，見 isSafePushHost 註解）。
	// 這裡失敗**不刪**訂閱：可能只是暫時性 DNS 問題，白名單網域本身沒有理由長期解析成
	// 內網位址，留著讓下次送出時重新判斷即可。
	if !isSafePushHost(ctx, u.Hostname()) {
		return errInvalidPushEndpoint
	}

	resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: s.Endpoint,
		Keys: webpush.Keys{
			P256dh: s.P256dh,
			Auth:   s.Auth,
		},
	}, &webpush.Options{
		HTTPClient:      noRedirectHTTPClient,
		Subscriber:      h.cfg.Subject,
		VAPIDPublicKey:  h.cfg.PublicKey,
		VAPIDPrivateKey: h.cfg.PrivateKey,
		TTL:             86400,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		_, _ = h.db.Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1`, s.ID)
		return errors.New("subscription expired")
	}

	if resp.StatusCode >= 300 {
		var buf bytes.Buffer
		buf.ReadFrom(resp.Body)
		return errors.New(strings.TrimSpace("push send failed: " + resp.Status + " " + buf.String()))
	}

	return nil
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
