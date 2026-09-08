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

const (
	ctxKeyIP ctxKey = iota
	ctxKeyVerified
	ctxKeySource
)

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
		ip, source, verified := computeClientIPDetail(r)
		r.RemoteAddr = ip
		ctx := context.WithValue(r.Context(), ctxKeyIP, ip)
		ctx = context.WithValue(ctx, ctxKeyVerified, verified)
		ctx = context.WithValue(ctx, ctxKeySource, source)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// computeClientIP 見 Middleware 上方的三層判斷說明；獨立成函式供 Middleware 與 ClientIP
// （沒經過 Middleware 時的退回邏輯）共用同一套規則。
func computeClientIP(r *http.Request) string {
	ip, _, _ := computeClientIPDetail(r)
	return ip
}

// computeClientIPDetail 回傳 (ip, 來源, origin 是否驗證通過)。
//
// 2026-09-08 正式環境實測修正：原本「X-Origin-Verify 驗證失敗就改信 X-Forwarded-For 最右側」在
// Cloudflare → Railway edge → Next.js rewrite → api 這條路徑上，最右側永遠是 Railway edge 的代理位址
// （DataPacket/Datacamp 機房 IP，且會在 5～10 個位址間輪替），結果一旦 Cloudflare 的標頭規則沒生效，
// 全站所有使用者都被歸到同一小撮代理 IP：IP 限流變成全站共用、登入紀錄與流量統計全錯。
// 這比「信任可能被偽造的 CF-Connecting-IP」嚴重得多——偽造只有繞過 Cloudflare 直打源站才辦得到，
// 而源站封鎖是另一層待做的防線。因此政策改為：
//   1. 有 CF-Connecting-IP → 一律採用（verified 依 X-Origin-Verify 是否吻合標記，供診斷／統計，不改變 IP）。
//   2. 沒有 CF-Connecting-IP（非經 Cloudflare 的請求）→ X-Forwarded-For 最右側（代理附加）→ TCP peer。
// /api/v1/version 會回報 origin_verified／ip_source，讓維運能直接看出 Cloudflare 標頭規則是否生效。
func computeClientIPDetail(r *http.Request) (ip, source string, verified bool) {
	if cf := r.Header.Get("CF-Connecting-IP"); cf != "" {
		return cf, "cf", originVerified(r)
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
			return last, "xff", false
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr, "peer", false
	}
	return host, "peer", false
}

// OriginVerified 這個請求的 X-Origin-Verify 是否與 ORIGIN_VERIFY_SECRET 吻合（未設密鑰＝視為通過）。
func OriginVerified(r *http.Request) bool {
	if v, ok := r.Context().Value(ctxKeyVerified).(bool); ok {
		return v
	}
	return originVerified(r)
}

// IPSource 這個請求的 client IP 取自哪裡：cf（CF-Connecting-IP）／xff（X-Forwarded-For 最右側）／peer（TCP）。
func IPSource(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKeySource).(string); ok && v != "" {
		return v
	}
	_, src, _ := computeClientIPDetail(r)
	return src
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
