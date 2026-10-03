// Package notify 系統對外通知（目前只有 Telegram）。刻意獨立成 leaf 套件（比照 internal/wallet、
// internal/vip 的模式）：不 import 任何業務套件（race/activityreward…），任何套件都可以安全 import
// 本套件而不會造成 import cycle。
package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// httpClient 比照 internal/integration/strava.go 的 Client Timeout 慣例（外部服務呼叫一律設短逾時，
// 避免卡住呼叫端的 goroutine）。
var httpClient = &http.Client{Timeout: 10 * time.Second}

// telegramAPIHost Telegram Bot API 主機；請求網址是 https://api.telegram.org/bot<TOKEN>/sendMessage，token 就在路徑裡。
const telegramAPIHost = "api.telegram.org"

// Telegram 發送一則 Telegram 訊息（parse_mode=HTML）。憑證讀 env TELEGRAM_BOT_TOKEN/TELEGRAM_CHAT_ID
// （目前僅在部署平台的 Secrets 設定，見 memory ops-monitoring）；任一為空 → 直接 no-op 靜默略過，不視為
// 錯誤，避免通知功能本身變成業務流程的單點故障。呼叫端若要在 HTTP 請求處理完之後、於背景 goroutine
// 呼叫本函式，務必傳入獨立於該請求生命週期的 context（例如 context.Background 另包 timeout），否則
// 請求結束連帶取消 ctx 會導致通知打不出去。
//
// 回傳的錯誤文字保證不含 bot token 與請求網址：呼叫端（alert.go、後台公告通知…）會直接把 err 寫進日誌，
// 而 http.Client.Do／url.Parse 回傳的 *url.Error，其 Error() 會把完整網址（含路徑裡的 token）原樣印出——
// 逾時、DNS 失敗這類再普通不過的錯誤就足以讓憑證進日誌。
func Telegram(ctx context.Context, text string) error {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")
	if token == "" || chatID == "" {
		return nil
	}

	form := url.Values{"chat_id": {chatID}, "text": {text}, "parse_mode": {"HTML"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://"+telegramAPIHost+"/bot"+token+"/sendMessage", strings.NewReader(form.Encode()))
	if err != nil {
		// 建不出請求幾乎一定是 url.Parse 失敗（token 含控制字元、非法的 % 跳脫…）：它的錯誤文字附整個網址，
		// 連 %zz 這種「無效跳脫」的片段都來自 token 本身，所以整個丟掉、只回固定訊息。
		return errors.New("telegram sendMessage: build request failed")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return sanitizeTransportErr(err, token)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 讀一小段 body 供 log 診斷（常見如 token 錯誤/chat_id 錯誤/bot 被踢出群組），限制長度避免異常
		// 回應把記憶體吃爆；讀取失敗不影響回傳錯誤本身。內文若（經代理等）回顯了 token 一併遮掉。
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telegram sendMessage failed: status %d: %s", resp.StatusCode, redactToken(string(body), token))
	}
	return nil
}

// sanitizeTransportErr 把 httpClient.Do 的錯誤整理成「不帶網址、不帶 token」的版本。
//   - http.Client.Do 的錯誤一律是 *url.Error（文件保證）：只取底層的 ue.Err（逾時、DNS 失敗、連線被拒…），
//     用 %w 保留錯誤鏈，呼叫端仍可 errors.Is(err, context.DeadlineExceeded) 之類判斷。
//   - 不是 *url.Error 就無從確定文字裡有沒有網址，只回固定訊息。
//   - 縱深防禦：底層錯誤文字本身若仍帶著 token 或網址（自訂傳輸層、代理的錯誤訊息），同樣只回固定訊息並放棄錯誤鏈。
func sanitizeTransportErr(err error, token string) error {
	var ue *url.Error
	if !errors.As(err, &ue) || ue.Err == nil {
		return errors.New("telegram sendMessage: request failed")
	}
	if leaksSecret(ue.Err.Error(), token) {
		return errors.New("telegram sendMessage: request failed")
	}
	return fmt.Errorf("telegram sendMessage: %w", ue.Err)
}

// leaksSecret s 是否帶著 token（原樣或 URL 跳脫後的形式）或請求網址的特徵（主機＋/bot 前綴，路徑裡的 token 緊接其後）。
func leaksSecret(s, token string) bool {
	if strings.Contains(s, telegramAPIHost+"/bot") {
		return true
	}
	for _, secret := range tokenForms(token) {
		if strings.Contains(s, secret) {
			return true
		}
	}
	return false
}

// redactToken 把 s 裡的 token（原樣或 URL 跳脫後的形式）換成 <redacted>。
func redactToken(s, token string) string {
	for _, secret := range tokenForms(token) {
		s = strings.ReplaceAll(s, secret, "<redacted>")
	}
	return s
}

// tokenForms token 可能出現在文字裡的寫法：原樣、路徑跳脫、查詢字串跳脫。空字串不算（否則 Contains／ReplaceAll 會對任何文字命中）。
func tokenForms(token string) []string {
	if token == "" {
		return nil
	}
	return []string{token, url.PathEscape(token), url.QueryEscape(token)}
}
