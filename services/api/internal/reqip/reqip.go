// Package reqip 判斷 HTTP 請求的真實 client IP（Cloudflare 之後的部署）。
// 邏輯搬自 internal/middleware（原本唯一定義處），獨立成不依賴任何其他 internal 套件的
// leaf package：internal/middleware 已依賴 internal/auth（RequireAuth 等），若 internal/auth
// 想直接沿用同一套 ClientIP 邏輯卻 import internal/middleware，會形成 auth → middleware → auth
// 的 import cycle（用戶登入紀錄 log 功能需要在 auth/handler.go 裡取得登入者 IP 才新增本套件）。
// internal/middleware.ClientIP 仍是原本呼叫點，改為委派到這裡，行為完全不變。
package reqip

import (
	"crypto/subtle"
	"net"
	"net/http"
	"os"
)

// originVerifySecret（env ORIGIN_VERIFY_SECRET，D-2 加固）：Cloudflare Transform Rule 對每個「經過
// Cloudflare」的請求注入 X-Origin-Verify=<此密鑰>；後端只在標頭帶對密鑰時才採信 CF-Connecting-IP，
// 藉此封死「繞過 Cloudflare 直打 Railway 源站 + 自帶假 CF-Connecting-IP」的偽造。
// 未設密鑰時 originVerified 一律回 true＝維持既有現況（信任 CF-Connecting-IP）、零回歸。
var originVerifySecret = os.Getenv("ORIGIN_VERIFY_SECRET")

func originVerified(r *http.Request) bool {
	if originVerifySecret == "" {
		return true // 未設密鑰：不強制（零回歸）
	}
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Origin-Verify")), []byte(originVerifySecret)) == 1
}

// ClientIP 取得請求的真實 client IP。
// SEC-M8：站台已置於 Cloudflare 之後，優先信任 Cloudflare 的 CF-Connecting-IP（Cloudflare 設定並清除
// 客戶端自帶同名標頭＝不可偽造）；API 流量走 www.dor.tw/api/*（Next.js rewrites 反向代理），標頭一路帶到後端。
// D-2：僅在 originVerified（帶對共享密鑰）時才採信 CF-Connecting-IP——沒帶對就退回 RemoteAddr，
// 使「繞過 Cloudflare 直打源站 + 自帶假 CF-Connecting-IP」無法偽造此維度。未設密鑰時 originVerified=true。
// 帳號類端點（/auth/login 等）仍須疊加不可偽造的 AccountField 當主防線（見 internal/middleware.RateLimit）。
func ClientIP(r *http.Request) string {
	if cf := r.Header.Get("CF-Connecting-IP"); cf != "" && originVerified(r) {
		return cf
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
