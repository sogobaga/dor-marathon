package middleware

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/cache"
	"github.com/dor/api/internal/reqip"
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
			// 2026-09-08 第二次稽核修法：dim 回傳 accountFieldInvalidBody 這個哨兵值，
			// 代表 AccountField 判定 body 本身形狀可疑（JSON 後面還有多餘內容、或同一欄位
			// 出現多個大小寫變體）——這種 body 對登入/註冊端點不可能合法，必須在這裡直接
			// 回 400 並中止，不能落到下面「d == "" → 放行」分支（那是給「看不出維度、正常
			// 放行交給 handler 判斷」用的，語意不同，見 AccountField 註解）。
			if d == accountFieldInvalidBody {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"invalid request body"}`))
				return
			}
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

// ClientIP 取得限流用 client IP。實作已搬到 internal/reqip（獨立 leaf package，讓 internal/auth
// 也能取用同一套邏輯而不會與本套件互相 import——見該套件註解）；這裡保留同名函式委派過去，
// 呼叫端（本檔內 UserOrIP、cmd/api/main.go 的 RateLimit 掛載）都不用改。
func ClientIP(r *http.Request) string {
	return reqip.ClientIP(r)
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

// accountFieldInvalidBody 是 AccountField 判定「body 形狀可疑」時回傳的哨兵值，只有 RateLimit
// 認得（見該函式）。不能用空字串代替——空字串在 RateLimit 裡的語意是「看不出維度，正常放行，
// 交給 handler 自己驗證」，而這裡要的是相反的效果：直接擋下整個請求。挑一個永遠不會是合法
// "acct:"-前綴 key 的字串（NUL 開頭，JSON 字串合法但不可能被拿來當帳號名稱送進來又剛好撞上），
// 避免跟真正的帳號 key 混淆。
const accountFieldInvalidBody = "\x00invalid-account-body"

// AccountField 以請求 JSON body 中的指定欄位（如 /auth/login、/auth/register 的 "email"）
// 當限流維度——CRITICAL 修法：帳號維度不像 ClientIP 能被客戶端偽造的標頭繞過
// （見 ClientIP 註解 SEC-M8），即使攻擊者對同一個目標帳號每次都換 IP/偽造標頭，
// 這個維度仍會把所有嘗試算進同一個 bucket，擋住針對特定帳號（尤其 admin，
// /auth/login 為 admin 與一般會員共用、且無帳號級鎖定）的暴力破解/憑證填充。
// 讀不到 body、JSON 解析失敗、或欄位為空 → 回傳空字串，RateLimit 對空維度直接放行
// （不因為格式錯誤的請求誤擋，交給 handler 自己的 decode/validate 回應錯誤）。
//
// 2026-09-08 第二次稽核修法：先前這裡跟 handler 對「body 該怎麼解」的認知會分岔，
// 兩種情況都讓帳號級限流可以被繞過（IP 也能偽造時，等於完全繞過限流）：
//
//  1. body 同時有 "email" 跟 "Email" 這種大小寫變體：舊邏輯「先找完全相符，找不到才
//     不分大小寫任選一個（map 迭代順序不定）」——跟 handler 端 encoding/json 解到 struct
//     的規則（完全相符優先，否則不分大小寫取「最後出現」那個）不一定選到同一個值，
//     限流 bucket 跟實際登入帳號對不上。
//  2. body 在第一個 JSON 物件後面還有多餘內容（例如 `{"email":"a"}{"email":"b"}` 或
//     `{"email":"a"} garbage`）：舊邏輯用 json.Unmarshal 解整個 body，遇到多餘內容直接
//     報錯 → 回傳空字串 → 不限流；但 handler 是用 json.NewDecoder(r.Body).Decode()，
//     只讀第一個 JSON 值就成功，照常把它當合法登入請求處理——攻擊者只要在 body 後面
//     加垃圾字元，就能讓帳號級限流失效，登入請求卻不受影響。
//
// 這兩種 body 對合法客戶端不可能出現，判定為整個請求無效（回傳 accountFieldInvalidBody，
// 由 RateLimit 直接回 400 並中止，不呼叫 handler）比「猜一個值繼續限流」更安全。
func AccountField(field string) func(*http.Request) string {
	return func(r *http.Request) string {
		body := peekAndRestoreBody(r)
		if len(body) == 0 {
			return ""
		}
		dec := json.NewDecoder(bytes.NewReader(body))
		var payload map[string]json.RawMessage
		if err := dec.Decode(&payload); err != nil {
			// 完全不是合法 JSON——交給 handler 自己的 decode 回應錯誤，這裡不擋。
			return ""
		}
		// 確認第一個 JSON 物件後面沒有多餘內容（允許結尾空白：json.Decoder 讀值前後都會
		// 跳過空白字元，io.EOF 代表跳過空白後真的沒有更多 token 了）。
		var trailing json.RawMessage
		if err := dec.Decode(&trailing); err != io.EOF {
			return accountFieldInvalidBody
		}
		// 收集所有跟 field 不分大小寫相符的 key；命中超過一個視為無效（見上方註解情境 1）。
		var raw json.RawMessage
		matches := 0
		for k, v := range payload {
			if strings.EqualFold(k, field) {
				raw = v
				matches++
			}
		}
		if matches > 1 {
			return accountFieldInvalidBody
		}
		if matches == 0 {
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
