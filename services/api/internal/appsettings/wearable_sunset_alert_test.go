package appsettings

// Terra／Strava 串接結束公告兩個鍵被後台改動時的 Telegram 通知（wearable_sunset_alert.go）：不連 DB、不連 Telegram
// （sunsetAlertSender 換成記錄用的假實作）。通知掛在 OnChange（Set 成功寫入之後才會觸發），這裡直接用 NotifyChange 觸發回呼。

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// resetSunsetAlerts 把「已註冊」的狀態清回行程剛啟動的樣子（取消回呼、重置 sync.Once），讓測試能分辨是誰註冊的。
func resetSunsetAlerts() {
	for _, unregister := range sunsetAlertUnregister {
		unregister()
	}
	sunsetAlertUnregister = nil
	sunsetAlertOnce = sync.Once{}
}

// captureSunsetAlerts 把通知的送出換成記錄用的假實作並清掉註冊狀態（不自動註冊——由呼叫端決定誰來註冊）；測試結束還原。
func captureSunsetAlerts(t *testing.T) *[]string {
	t.Helper()
	var mu sync.Mutex
	var got []string
	orig := sunsetAlertSender
	sunsetAlertSender = func(msg string) {
		mu.Lock()
		got = append(got, msg)
		mu.Unlock()
	}
	resetSunsetAlerts()
	t.Cleanup(func() {
		sunsetAlertSender = orig
		resetSunsetAlerts()
	})
	return &got
}

func TestSunsetChangeAlertsFireForBothKeys(t *testing.T) {
	got := captureSunsetAlerts(t)
	registerSunsetAlerts()
	ctx := context.Background()

	// 訊息只陳述「已儲存成什麼」，不宣稱「改成了什麼」：後台把同一個值再存一次也會觸發通知（見下面的重複儲存斷言）。
	NotifyChange(ctx, WearableSunsetStateKey, "announce")
	if len(*got) != 1 || !strings.Contains((*got)[0], "已儲存為「啟用」") || !strings.Contains((*got)[0], "改回「關閉」") {
		t.Fatalf("announce alert = %q", *got)
	}
	NotifyChange(ctx, WearableSunsetStateKey, "off")
	if len(*got) != 2 || !strings.Contains((*got)[1], "已儲存為「關閉」") {
		t.Fatalf("off alert = %q", *got)
	}
	// 防禦：Set 的驗證器已不再接受空字串，但直接下 SQL 寫進去或舊資料仍可能是空值；語意＝缺鍵＝off
	NotifyChange(ctx, WearableSunsetStateKey, "")
	if len(*got) != 3 || !strings.Contains((*got)[2], "已儲存為「關閉」") {
		t.Fatalf("empty (= off) alert = %q", *got)
	}
	NotifyChange(ctx, WearableSunsetDateKey, "2026-11-15")
	if len(*got) != 4 || !strings.Contains((*got)[3], "結束日已儲存為 2026-11-15") {
		t.Fatalf("date alert = %q", *got)
	}
	NotifyChange(ctx, WearableSunsetDateKey, "")
	if len(*got) != 5 || !strings.Contains((*got)[4], "結束日已儲存為 "+WearableSunsetDefaultDate) {
		t.Fatalf("date cleared alert must say it was saved as the default date: %q", *got)
	}
	// 連續把同一個值再存一次也要通知（沒有節流：操作者要能看到每一次寫入）
	NotifyChange(ctx, WearableSunsetStateKey, "announce")
	NotifyChange(ctx, WearableSunsetStateKey, "announce")
	if len(*got) != 7 {
		t.Fatalf("repeated saves must each alert (no throttling), got %d alerts", len(*got))
	}
	// 其他設定鍵不通知
	before := len(*got)
	NotifyChange(ctx, "vip_trial_days", "7")
	NotifyChange(ctx, "active_skin", "warm")
	if len(*got) != before {
		t.Fatalf("unrelated keys must not alert: %q", (*got)[before:])
	}
}

// 後台 Handler 建構時就會註冊（整個行程只註冊一次，重複建構不會重複通知）。這裡不自己呼叫 registerSunsetAlerts：
// 註冊只能來自 NewHandler——拿掉那一行就是 0 則通知，註冊兩次就是 2 則，兩種退化都會失敗。
func TestNewHandlerRegistersTheSunsetAlertsOnce(t *testing.T) {
	got := captureSunsetAlerts(t)
	NewHandler(nil, nil)
	NewHandler(nil, nil)
	NotifyChange(context.Background(), WearableSunsetStateKey, "announce")
	NotifyChange(context.Background(), WearableSunsetDateKey, "2026-11-15")
	if len(*got) != 2 {
		t.Fatalf("exactly one alert per change expected even after two NewHandler calls (got %d alerts for 2 changes): %q", len(*got), *got)
	}
}

// 通知在「每一次後台成功寫入」都會送（沒有節流、也不比對舊值），所以同一個值再存一次，訊息也必須是真的：
// 只能說「已儲存為…」，不能說「已改為／已變更／狀態已『啟用』」這類暗示狀態有變動的話。
func TestSunsetChangeMessagesOnlyStateWhatWasSaved(t *testing.T) {
	claimsAChange := regexp.MustCompile(`已改為|已改成|已變更|已更新|已切換|已被改|已調整|已「啟用」|已「關閉」|改成了|變成了`)
	for _, c := range []struct{ key, val, want string }{
		{WearableSunsetStateKey, "announce", "已儲存為「啟用」"},
		{WearableSunsetStateKey, "off", "已儲存為「關閉」"},
		{WearableSunsetStateKey, "", "已儲存為「關閉」"},
		{WearableSunsetDateKey, "2026-10-31", "結束日已儲存為 2026-10-31"},
		{WearableSunsetDateKey, "2026-12-01", "結束日已儲存為 2026-12-01"},
		{WearableSunsetDateKey, "", "結束日已儲存為 " + WearableSunsetDefaultDate},
	} {
		msg := sunsetChangeMessage(c.key, c.val)
		if !strings.Contains(msg, c.want) {
			t.Errorf("message for %s=%q must say %q: %q", c.key, c.val, c.want, msg)
		}
		if claimsAChange.MatchString(msg) {
			t.Errorf("message for %s=%q claims a change although the same value may have been saved again: %q", c.key, c.val, msg)
		}
		// 同樣的輸入永遠是同樣的訊息（沒有「上次是什麼」的暗示）
		if again := sunsetChangeMessage(c.key, c.val); again != msg {
			t.Errorf("message must depend only on the saved value: %q vs %q", msg, again)
		}
	}
}

// 對外文案守則（擁有者規定）：繁體中文；絕不提第三方裝置品牌；不用「跑團」二字；不暗示任何品牌背書。
func TestSunsetChangeMessagesFollowTheCopyRules(t *testing.T) {
	banned := regexp.MustCompile(`(?i)garmin|佳明|跑團|官方合作|認證|贊助|背書`)
	for _, c := range []struct{ key, val string }{
		{WearableSunsetStateKey, "announce"}, {WearableSunsetStateKey, "off"}, {WearableSunsetStateKey, ""},
		{WearableSunsetDateKey, "2026-10-31"}, {WearableSunsetDateKey, ""},
	} {
		msg := sunsetChangeMessage(c.key, c.val)
		if msg == "" || banned.MatchString(msg) {
			t.Errorf("message for %s=%q is empty or contains a banned word: %q", c.key, c.val, msg)
		}
	}
	if sunsetChangeMessage("something_else", "x") != "" {
		t.Error("only the two sunset keys have a message")
	}
}
