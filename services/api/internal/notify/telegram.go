// Package notify 系統對外通知（目前只有 Telegram）。刻意獨立成 leaf 套件（比照 internal/wallet、
// internal/vip 的模式）：不 import 任何業務套件（race/activityreward…），任何套件都可以安全 import
// 本套件而不會造成 import cycle。
package notify

import (
	"context"
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

// Telegram 發送一則 Telegram 訊息（parse_mode=HTML）。憑證讀 env TELEGRAM_BOT_TOKEN/TELEGRAM_CHAT_ID
// （目前僅在部署平台的 Secrets 設定，見 memory ops-monitoring）；任一為空 → 直接 no-op 靜默略過，不視為
// 錯誤，避免通知功能本身變成業務流程的單點故障。呼叫端若要在 HTTP 請求處理完之後、於背景 goroutine
// 呼叫本函式，務必傳入獨立於該請求生命週期的 context（例如 context.Background 另包 timeout），否則
// 請求結束連帶取消 ctx 會導致通知打不出去。
func Telegram(ctx context.Context, text string) error {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")
	if token == "" || chatID == "" {
		return nil
	}

	form := url.Values{"chat_id": {chatID}, "text": {text}, "parse_mode": {"HTML"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.telegram.org/bot"+token+"/sendMessage", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 讀一小段 body 供 log 診斷（常見如 token 錯誤/chat_id 錯誤/bot 被踢出群組），限制長度避免異常
		// 回應把記憶體吃爆；讀取失敗不影響回傳錯誤本身。
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telegram sendMessage failed: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
