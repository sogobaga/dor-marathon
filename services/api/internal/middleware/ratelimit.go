package middleware

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/cache"
)

// incrExpireScript 把「INCR + 首次設 TTL」包進單一 Lua script 原子執行（Redis 保證整支 script
// 不會被其他命令插隊），取代原本 Incr 後再 Expire 的兩次往返。修法動機：兩次往返之間若行程崩潰
// /連線斷掉，會留下一個「已 INCR 但沒有 TTL」的 key，之後永遠不會過期，等於把某個 bucket 永久
// 鎖死在限流狀態（誤擋合法流量，且沒有自動恢復手段，需要人工介入 DEL）。
var incrExpireScript = redis.NewScript(`
local n = redis.call("INCR", KEYS[1])
if n == 1 then
	redis.call("EXPIRE", KEYS[1], ARGV[1])
end
return n
`)

// RateLimit 用既有 Redis 做固定視窗計數限流，超限回 429（SEC-H1）。
// action 是限流動作名稱（如 "auth"、"monopoly_roll"），與 dim(r)（IP、userID 或帳號）組出
// cache.RateLimitKey。同一路由可疊加多個 RateLimit（不同 dim/action）做多維度防線，
// 例如 /auth/login 同時掛 ClientIP（縱深防禦）與 AccountField("email")（帳號維度，
// 見該函式註解——這維度不像 IP 可被偽造的標頭繞過）。
// dim(r) 回傳空字串時視為無法判斷維度，不限流（避免誤擋，交由其他把關機制處理）。
// Redis 為 nil 或連線出錯時 fail-open（不放行機制本身變成單點故障，擋掉所有正常流量）。
func RateLimit(rdb *redis.Client, action string, limit int, window time.Duration, dim func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rdb == nil {
				next.ServeHTTP(w, r)
				return
			}
			d := dim(r)
			if d == "" {
				next.ServeHTTP(w, r)
				return
			}

			key := cache.RateLimitKey(d, action)
			ctx := r.Context()
			n, err := incrExpireScript.Run(ctx, rdb, []string{key}, int(window.Seconds())).Int64()
			if err != nil {
				// Redis 掛掉時放行，避免限流機制本身造成全站不可用。
				next.ServeHTTP(w, r)
				return
			}
			if n > int64(limit) {
				w.Header().Set("Retry-After", strconv.Itoa(int(window.Seconds())))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"too many requests, please try again later"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP 取得限流用 client IP。
// SEC-M8 修復（v0.1.417，站台已置於 Cloudflare 之後）：優先信任 Cloudflare 的 CF-Connecting-IP。
// Cloudflare 代理（www.dor.tw 橘雲）會把「真實客戶端 IP」寫入 CF-Connecting-IP，並清除客戶端自帶的
// 同名標頭 → 不可偽造；API 流量走 www.dor.tw/api/*（Next.js rewrites 反向代理），此標頭會一路帶到後端。
// 沒有 CF-Connecting-IP 時（本地開發、或尚未經過 Cloudflare 的路徑）退回 chi RealIP 處理過的 RemoteAddr，
// 不影響現況、無回歸。
// ⚠️ 殘餘風險：若有人繞過 Cloudflare 直打 Railway 源站並自帶假 CF-Connecting-IP，仍可偽造此維度——
// 要完全封死需在 Cloudflare 加「共享密鑰標頭」且後端只信任帶對密鑰的請求（origin verify），為後續加固項。
// 因此帳號類端點（/auth/login 等）務必仍疊加 AccountField 這種不可偽造維度當主防線，不能只靠 IP。
func ClientIP(r *http.Request) string {
	if cf := r.Header.Get("CF-Connecting-IP"); cf != "" {
		return cf
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// UserOrIP 以登入使用者 ID 為限流維度；未登入（context 無 userID，如 RequireAuth 尚未執行
// 或用在 OptionalAuth 路由）時退回 client IP，避免匿名情境沒有維度可用。
func UserOrIP(r *http.Request) string {
	if uid := GetUserID(r.Context()); uid != "" {
		return "u" + uid
	}
	return ClientIP(r)
}

// bodyReadLimit 是 AccountField/GoogleIDTokenAccount 讀取限流用 body 的上限，與全域
// MaxBodyBytes（cmd/api/main.go）一致，避免在真正的大小檢查生效之前，這裡先被超大 body 撐爆記憶體。
const bodyReadLimit = 1 << 20

// peekAndRestoreBody 讀出整個 request body（上限 bodyReadLimit），並把內容還原回 r.Body
// （包成新的 NopCloser），確保呼叫這個函式之後，handler 自己的 json.Decode(r.Body) 仍能讀到
// 完整內容——這裡只是「偷看」body 來判斷限流維度，不能因此讓後續正常的解碼流程讀到空 body。
func peekAndRestoreBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, bodyReadLimit))
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body
}

// AccountField 以請求 JSON body 中的指定欄位（如 /auth/login、/auth/register 的 "email"）
// 當限流維度——CRITICAL 修法：帳號維度不像 ClientIP 能被客戶端偽造的標頭繞過
// （見 ClientIP 註解 SEC-M8），即使攻擊者對同一個目標帳號每次都換 IP/偽造標頭，
// 這個維度仍會把所有嘗試算進同一個 bucket，擋住針對特定帳號（尤其 admin，
// /auth/login 為 admin 與一般會員共用、且無帳號級鎖定）的暴力破解/憑證填充。
// 讀不到 body、JSON 解析失敗、或欄位為空 → 回傳空字串，RateLimit 對空維度直接放行
// （不因為格式錯誤的請求誤擋，交給 handler 自己的 decode/validate 回應錯誤）。
func AccountField(field string) func(*http.Request) string {
	return func(r *http.Request) string {
		body := peekAndRestoreBody(r)
		if len(body) == 0 {
			return ""
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(body, &payload); err != nil {
			return ""
		}
		raw, ok := payload[field]
		if !ok {
			return ""
		}
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return ""
		}
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" {
			return ""
		}
		return "acct:" + v
	}
}

// GoogleIDTokenAccount 是 /auth/google 專用的帳號維度：該端點 body 只有 id_token（無明文
// email 欄位），所以從 JWT 的中段 payload 解出（未驗簽）email/sub claim 當限流 key。
// 這裡只是拿來做流量分桶、擋同一個目標帳號被打爆，不是身分驗證——真正的簽章驗證仍在
// Service.LoginWithGoogle 用 Google 公鑰做（見 internal/auth/service.go 的 idtoken.Validate）；
// 就算攻擊者塞偽造/竄改過 payload 的假 token，這裡最多只是分到錯的 bucket 或不限流，
// 不會被用來偽造身分。格式不對/缺 email 與 sub → 回傳空字串，不限流（IP 維度仍在防禦）。
func GoogleIDTokenAccount(r *http.Request) string {
	body := peekAndRestoreBody(r)
	if len(body) == 0 {
		return ""
	}
	var req struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.IDToken == "" {
		return ""
	}
	parts := strings.Split(req.IDToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
		Sub   string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	if claims.Email != "" {
		return "acct:" + strings.ToLower(strings.TrimSpace(claims.Email))
	}
	if claims.Sub != "" {
		return "acct:googlesub:" + claims.Sub
	}
	return ""
}
