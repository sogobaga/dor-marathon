package notify

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// alertThrottle 同一 kind 的告警節流窗口：窗口內只送第一次，避免同一種錯誤（例如某個 handler 持續
// panic/DB 持續逾時）在短時間內把 Telegram 洗版，導致真正重要的訊息被埋沒。
const alertThrottle = 30 * time.Minute

var (
	alertMu   sync.Mutex
	alertSeen = map[string]time.Time{} // kind -> 最近一次通過節流檢查的時間；package 級記憶體狀態，
	// 多執行個體（如 Railway 水平擴展）各自獨立節流，可能偶爾重複發送，但不會因此漏報，符合設計取捨
	// （比照 internal/race/personal_progress.go 的 lowStockShouldNotify）。
)

// alertShouldSend 節流的 atomic check-and-set：同一 kind 在 alertThrottle 窗口內只有第一個呼叫者會拿到
// true（呼叫當下立即佔位 lastNotified=now，避免併發多個呼叫同時通過檢查而洗頻）。
func alertShouldSend(kind string) bool {
	alertMu.Lock()
	defer alertMu.Unlock()
	if last, ok := alertSeen[kind]; ok && time.Since(last) < alertThrottle {
		return false
	}
	alertSeen[kind] = time.Now()
	return true
}

// taiwanTimestamp 回傳目前時間的台灣時間（UTC+8）字串，格式 MM/DD HH:mm。刻意手算 UTC+8、不用
// time.LoadLocation("Asia/Taipei")，避免額外依賴系統/容器內的 IANA tzdata（比照 telegram.go 一貫的
// 「不要為了通知功能本身引入額外依賴」取捨）。
func taiwanTimestamp() string {
	return time.Now().UTC().Add(8 * time.Hour).Format("01/02 15:04")
}

// Alert 關鍵事件告警：per-kind 30 分鐘節流，避免同一種錯誤洗版 Telegram。
// 底層走 Telegram()，TELEGRAM_BOT_TOKEN/TELEGRAM_CHAT_ID 未設時 Telegram() 本身是 no-op，
// 呼叫端不需要另外判斷是否已設定。非阻塞：內部開 goroutine 送出，不拖慢呼叫端（多為 HTTP handler 錯誤路徑/背景流程）。
func Alert(kind, title, detail string) {
	if !alertShouldSend(kind) {
		return
	}
	msg := fmt.Sprintf("🚨 [DOR] %s\n%s\n%s", title, detail, taiwanTimestamp())
	go func() {
		// 用 context.Background 另包 timeout，不用呼叫端的 request context：Alert 常在 HTTP
		// handler 的錯誤路徑或背景流程呼叫，該 goroutine 可能在呼叫端已經返回（甚至 request 已結束、
		// ctx 已被取消）之後才真正送出，若沿用呼叫端 ctx 會導致通知打不出去（比照 telegram.go 文件註解）。
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := Telegram(ctx, msg); err != nil {
			log.Warn().Err(err).Str("kind", kind).Msg("alert: telegram send failed")
		}
	}()
}
