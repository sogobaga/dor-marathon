package appsettings

// Terra／Strava 串接結束公告開關（wearable_sunset_state／wearable_sunset_date）被後台儲存時的即時通知。
//
// 這兩個鍵一改，最慢 60 秒內全站的 Terra／Strava 連接入口就會被擋下（或恢復），結束日也會顯示給所有已連接的會員看；
// 誤切換必須馬上被看到，所以每次「後台成功寫入」都送一則 Telegram（沒設 TELEGRAM_* 環境變數時 notify.Telegram 是 no-op）。
// 每一次寫入都送、不比對舊值，所以訊息只說「已儲存為…」、不說「改成…」（同一個值再存一次也是一次寫入）。
// 用 OnChange 掛在 Set 成功之後，只涵蓋後台這條路徑；直接下 SQL 改 app_settings 不會觸發（與其他 OnChange 使用端相同）。
//
// 刻意不用 notify.Alert：它對同一個 kind 有 30 分鐘節流，「公告中→關閉→公告中」的連續切換會漏報第二次；這則通知只有
// 手動改設定才會觸發（一天最多幾則），不需要節流。後台請求不能被通知拖慢：另開 goroutine 送、自己的逾時，與請求脫鉤。

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/notify"
)

var (
	sunsetAlertOnce       sync.Once
	sunsetAlertUnregister []func() // OnChange 的取消註冊函式（正式環境不取消；測試用來還原）
)

// sunsetAlertSender 送出通知的實作（參數是整則訊息）；預設非阻塞地呼叫 notify.Telegram，測試會換成假的。
var sunsetAlertSender = func(msg string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := notify.Telegram(ctx, msg); err != nil {
			log.Warn().Err(err).Msg("appsettings: wearable sunset change telegram failed")
		}
	}()
}

// registerSunsetAlerts 幫兩個公告鍵各掛一個 OnChange 回呼（整個行程只註冊一次；NewHandler 呼叫）。
func registerSunsetAlerts() {
	sunsetAlertOnce.Do(func() {
		for _, key := range []string{WearableSunsetStateKey, WearableSunsetDateKey} {
			key := key
			sunsetAlertUnregister = append(sunsetAlertUnregister, OnChange(key, func(_ context.Context, value string) {
				log.Warn().Str("key", key).Str("value", value).Msg("appsettings: wearable sunset setting saved from the admin console")
				if msg := sunsetChangeMessage(key, value); msg != "" {
					sunsetAlertSender(msg)
				}
			}))
		}
	})
}

// sunsetChangeMessage 一則「已儲存」通知的內容（繁體中文；不提任何第三方裝置品牌、不用「跑團」、不暗示品牌背書）。
//
// 措辭只陳述「已儲存成什麼」，不宣稱「改成了什麼」：OnChange 在每一次後台成功寫入都會觸發，不比對舊值——
// 管理者把同一個值再存一次也會送一則，所以「已改為／已變更／已啟用」這類暗示狀態有變動的說法可能是假的。
// 其餘只寫「什麼時候生效、誤切換怎麼救」。
//
// 為什麼值要寫在這則訊息裡：後台稽核紀錄（audit_logs）只記方法、路徑、狀態碼與操作者，不記寫入的值；
// 值本身只存在於這則通知與 app_settings。
func sunsetChangeMessage(key, value string) string {
	v := strings.TrimSpace(value)
	switch key {
	case WearableSunsetStateKey:
		if strings.ToLower(v) == sunsetAnnounce {
			return "📣 [DOR] Terra／Strava 串接結束公告已儲存為「啟用」：最慢 60 秒內，全站不再開放新的 Terra／Strava 連接（既有連線照常同步）。" +
				"若不是你操作的，請立刻到後台「系統設定」把「串接結束公告狀態」改回「關閉」。"
		}
		return "📣 [DOR] Terra／Strava 串接結束公告已儲存為「關閉」：最慢 60 秒內，Terra／Strava 連接維持開放（若先前是啟用則恢復開放），會員畫面不顯示結束公告。"
	case WearableSunsetDateKey:
		if v == "" {
			v = WearableSunsetDefaultDate + "（預設值）"
		}
		return "📣 [DOR] Terra／Strava 串接結束日已儲存為 " + v + "（台北日期，含當天）。公告期間顯示給會員看的結束日以此為準。"
	}
	return ""
}
