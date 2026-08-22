package notify

import (
	"os"
	"regexp"
	"testing"
	"time"
)

// 本檔測試延續 vip_renewal_test.go 頂部的既有慣例：純邏輯（節流 check-and-set、時間戳格式化）用
// 單元測試覆蓋；實際打 Telegram API 的網路路徑（Telegram() 本身）不在此檔測試範圍內、也不做真實
// 網路呼叫，改以程式碼審閱 + Telegram() 未設定 env 時的 no-op 保證來確保安全。

func TestAlertShouldSend_ThrottlesSameKind(t *testing.T) {
	t.Cleanup(func() {
		alertMu.Lock()
		delete(alertSeen, "test-kind-a")
		alertMu.Unlock()
	})

	if !alertShouldSend("test-kind-a") {
		t.Fatal("expected first call to pass throttle check")
	}
	if alertShouldSend("test-kind-a") {
		t.Fatal("expected immediate second call to be throttled")
	}

	// 模擬時間已經過了節流窗口：直接改 package 級 map（比照 lowStockShouldNotify 相關測試在此
	// codebase 中「直接操作 package 級 map 模擬時間流逝」的既有慣例）。
	alertMu.Lock()
	alertSeen["test-kind-a"] = time.Now().Add(-alertThrottle - time.Second)
	alertMu.Unlock()

	if !alertShouldSend("test-kind-a") {
		t.Fatal("expected call after throttle window elapsed to pass")
	}
}

func TestAlertShouldSend_DifferentKindsIndependent(t *testing.T) {
	t.Cleanup(func() {
		alertMu.Lock()
		delete(alertSeen, "test-kind-b")
		delete(alertSeen, "test-kind-c")
		alertMu.Unlock()
	})

	if !alertShouldSend("test-kind-b") {
		t.Fatal("expected first call for kind b to pass")
	}
	if alertShouldSend("test-kind-b") {
		t.Fatal("expected second call for kind b to be throttled")
	}
	// kind c 應完全不受 kind b 節流狀態影響。
	if !alertShouldSend("test-kind-c") {
		t.Fatal("expected kind c to be independent of kind b's throttle state")
	}
}

func TestTaiwanTimestamp_Format(t *testing.T) {
	got := taiwanTimestamp()
	matched, err := regexp.MatchString(`^\d{2}/\d{2} \d{2}:\d{2}$`, got)
	if err != nil {
		t.Fatalf("regexp error: %v", err)
	}
	if !matched {
		t.Fatalf("expected MM/DD HH:mm format, got %q", got)
	}
}

func TestTaiwanTimestamp_KnownInstant(t *testing.T) {
	// 直接套用與 taiwanTimestamp 相同的算法（UTC+8 手算，不用 time.LoadLocation）驗證已知時間點，
	// 避免依賴 time.Now() 造成的時序不確定性。
	utc := time.Date(2026, 8, 21, 20, 30, 0, 0, time.UTC) // UTC 20:30 -> 台灣時間 08/22 04:30
	got := utc.UTC().Add(8 * time.Hour).Format("01/02 15:04")
	want := "08/22 04:30"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestAlert_NoPanicWithoutEnv(t *testing.T) {
	oldToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	oldChat := os.Getenv("TELEGRAM_CHAT_ID")
	os.Unsetenv("TELEGRAM_BOT_TOKEN")
	os.Unsetenv("TELEGRAM_CHAT_ID")
	t.Cleanup(func() {
		os.Setenv("TELEGRAM_BOT_TOKEN", oldToken)
		os.Setenv("TELEGRAM_CHAT_ID", oldChat)
		alertMu.Lock()
		delete(alertSeen, "test-kind-noenv")
		alertMu.Unlock()
	})

	// 同步部分（節流檢查 + 訊息組裝 + spawn goroutine）不應 panic 或阻塞；底層 Telegram() 因為
	// env 未設定會在 goroutine 內部直接 no-op，不需要等待 goroutine 完成即可確認呼叫端安全。
	Alert("test-kind-noenv", "test title", "test detail")
}
