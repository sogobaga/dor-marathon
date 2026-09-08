// Package reqip 判斷 HTTP 請求的真實 client IP（Cloudflare 之後的部署）。
// 邏輯搬自 internal/middleware（原本唯一定義處），獨立成不依賴任何其他 internal 套件的
// leaf package：internal/middleware 已依賴 internal/auth（RequireAuth 等），若 internal/auth
// 想直接沿用同一套 ClientIP 邏輯卻 import internal/middleware，會形成 auth → middleware → auth
// 的 import cycle（用戶登入紀錄 log 功能需要在 auth/handler.go 裡取得登入者 IP 才新增本套件）。
// internal/middleware.ClientIP 仍是原本呼叫點，改為委派到這裡，行為完全不變。
package reqip

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
)

// originVerifySecret（env ORIGIN_VERIFY_SECRET，D-2 加固）：Cloudflare Transform Rule 對每個「經過
// Cloudflare」的請求注入 X-Origin-Verify=<此密鑰>；後端只在標頭帶對密鑰時才採信 CF-Connecting-IP，
// 藉此封死「繞過 Cloudflare 直打 Railway 源站 + 自帶假 CF-Connecting-IP」的偽造。
// 未設密鑰時 originVerified 一律回 true＝維持既有現況（信任 CF-Connecting-IP）、零回歸——見
// Middleware 的 warnOriginVerifyOnce：這個「維持現況」是一個部署拓樸假設，啟動時會記一次 log
// 讓它是可見的，不是靜默生效。
var originVerifySecret = os.Getenv("ORIGIN_VERIFY_SECRET")

func originVerified(r *http.Request) bool {
	if originVerifySecret == "" {
		return true // 未設密鑰：不強制（零回歸）
	}
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Origin-Verify")), []byte(originVerifySecret)) == 1
}

// ctxKey 是本套件塞進 request context 的 client IP key 型別（未匯出，避免跟其他套件的 context key
// 意外相撞，比照 internal/middleware 既有 roleKey{}／internal/dbwake 既有 ctxKey 的做法）。
type ctxKey int

const ctxKeyIP ctxKey = iota

// warnOriginVerifyOnce 見 Middleware 內的使用說明：只在伺服器啟動、Middleware 被建構時記一次，
// 不是每個請求都記。
var warnOriginVerifyOnce sync.Once

// Middleware 計算請求的真實 client IP，寫進 context（供 ClientIP 讀取）並覆寫 r.RemoteAddr
// （讓既有沿用 r.RemoteAddr／chi RealIP 慣例的下游程式碼不必改，例如 internal/dbwake.Middleware、
// internal/adminacct 的稽核 log）——2026-09-08 audit finding 5：取代 cmd/api/main.go 原本掛的
// chimiddleware.RealIP（那個只認 True-Client-IP/X-Real-IP/X-Forwarded-For 最左側一段，三者皆可被
// 客戶端任意偽造，等同完全不設防；且掛在這裡等於本套件的 originVerifySecret／CF-Connecting-IP
// 判斷從未真正生效過，因為 r.RemoteAddr 早被 RealIP 用可偽造的標頭蓋掉了）。
//
// 判斷順序：
//  1. CF-Connecting-IP，當 originVerified(r)（帶對 X-Origin-Verify 共享密鑰）。未設密鑰時
//     originVerified 恆為 true，維持現況信任 CF-Connecting-IP（假設 Cloudflare 已前置於正式環境，
//     見 warnOriginVerifyOnce 的啟動 log）。
//  2. 否則取 X-Forwarded-For 「最右側」一段——這是 Railway 代理層自己附加、看到的來源，客戶端
//     自帶的假 XFF 值只能出現在更左側，附加動作本身不可被客戶端偽造覆蓋。
//  3. 都沒有則退回 TCP peer（r.RemoteAddr 原始值的 host 部分）。
//
// 必須掛在路由最前面（比照原本 chimiddleware.RealIP 的位置，見 cmd/api/main.go）。
func Middleware(next http.Handler) http.Handler {
	if originVerifySecret == "" {
		warnOriginVerifyOnce.Do(func() {
			log.Warn().Msg("ORIGIN_VERIFY_SECRET 未設定：仍信任 CF-Connecting-IP（假設 Cloudflare 已前置於正式環境）——繞過 Cloudflare 直打源站可偽造來源 IP，建議盡快設定此密鑰以強制驗證")
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := computeClientIP(r)
		r.RemoteAddr = ip
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyIP, ip)))
	})
}

// computeClientIP 見 Middleware 上方的三層判斷說明；獨立成函式供 Middleware 與 ClientIP
// （沒經過 Middleware 時的退回邏輯）共用同一套規則。
func computeClientIP(r *http.Request) string {
	if cf := r.Header.Get("CF-Connecting-IP"); cf != "" && originVerified(r) {
		return cf
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
			return last
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ClientIP 取得請求的真實 client IP。優先讀 Middleware 已經算好、存進 context 的值（全站路由都
// 掛了 Middleware，見 cmd/api/main.go）；context 沒有這個值時（例如未經過本 middleware 的測試
// 請求）退回直接重算一次（computeClientIP，與 Middleware 用同一套判斷邏輯），確保呼叫端不論有
// 沒有掛 Middleware 都能拿到合理的值。
// 帳號類端點（/auth/login 等）仍須疊加不可偽造的 AccountField 當主防線（見 internal/middleware.RateLimit）。
func ClientIP(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKeyIP).(string); ok && v != "" {
		return v
	}
	return computeClientIP(r)
}
