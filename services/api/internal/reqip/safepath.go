package reqip

import "strings"

// garminWebhookPrefix Garmin 直連 webhook 的路徑前綴；後面第一段是「秘密路徑 token」（Garmin 的 push 沒有
// 簽章，端點 URL 本身就是憑證），之後才是 summary 種類。
const garminWebhookPrefix = "/api/v1/integrations/garmin/webhook/"

// secretPathMask 取代秘密段的字面值。
const secretPathMask = "***"

// SafePath 回傳「可以安全寫進 log／Telegram 告警」的請求路徑：把 Garmin webhook 路徑上的秘密 token 段遮成
// ***，其餘路徑原樣回傳。
//
//	/api/v1/integrations/garmin/webhook/<token>/activities → /api/v1/integrations/garmin/webhook/***/activities
//	/api/v1/integrations/garmin/webhook/<token>            → /api/v1/integrations/garmin/webhook/***
//	/api/v1/integrations/garmin/webhook/                   → 原樣（沒有 token 段）
//	其他任何路徑                                              → 原樣
//
// 套用位置（S7）：internal/middleware/alert.go（5xx 激增與 panic 告警）、internal/dbwake（喚醒歸因 log）——
// 這兩處原本直接寫 r.URL.Path，一旦 webhook 處理出 5xx／panic，秘密 token 就會被寫進 Telegram 與日誌。
// 任何新增「把請求路徑寫進 log／告警」的地方都應該經過這支函式。
func SafePath(path string) string {
	if !strings.HasPrefix(path, garminWebhookPrefix) {
		return path
	}
	rest := path[len(garminWebhookPrefix):]
	if rest == "" {
		return path
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return garminWebhookPrefix + secretPathMask + rest[i:]
	}
	return garminWebhookPrefix + secretPathMask
}
