package middleware

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/dor/api/internal/notify"
)

// FiveXXWindow 5xx 回應在滑動窗口內的計數器：用於偵測「短時間內大量 5xx」並觸發聚合告警，
// 避免每一次 5xx 都各自發一則 Telegram（那樣正式環境稍微不穩就洗版）。純邏輯，不含 HTTP，方便單測。
type FiveXXWindow struct {
	mu          sync.Mutex
	windowStart time.Time
	count       int
	threshold   int
	window      time.Duration
	sample      string
	now         func() time.Time // 可注入以利測試；NewFiveXXWindow 預設用 time.Now
}

// NewFiveXXWindow 建立一個 5xx 滑動窗口計數器：window 時間內累積達 threshold 次即觸發。
func NewFiveXXWindow(threshold int, window time.Duration) *FiveXXWindow {
	return &FiveXXWindow{
		threshold: threshold,
		window:    window,
		now:       time.Now,
	}
}

// Record 記錄一次 5xx。若目前時間已超出窗口，先重置窗口再計數。累積達到 threshold 時 triggered=true，
// 並立即重置窗口歸零（避免同一波錯誤持續觸發告警——聚合後只送一次，等下一個窗口重新累積才會再送）。
// sample 是這一次（觸發時的最後一筆）"METHOD PATH STATUS" 字串，供告警內容定位用。
// 呼叫端（FiveXXAlert）負責只在狀態碼落在 5xx 範圍才呼叫本函式，Record 本身不做狀態碼過濾。
func (w *FiveXXWindow) Record(method, path string, status int) (triggered bool, count int, sample string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := w.now()
	s := fmt.Sprintf("%s %s %d", method, path, status)

	if w.windowStart.IsZero() || now.Sub(w.windowStart) >= w.window {
		// 超出窗口（或第一次呼叫）：重置窗口起點，從頭計數。
		w.windowStart = now
		w.count = 0
	}

	w.count++
	w.sample = s

	if w.count >= w.threshold {
		triggered = true
		count = w.count
		sample = w.sample
		// 觸發後立即重置窗口，避免同一波錯誤持續觸發（下一個窗口重新累積才會再送）。
		w.windowStart = time.Time{}
		w.count = 0
		w.sample = ""
		return triggered, count, sample
	}

	return false, w.count, w.sample
}

// defaultFiveXXWindow 全站共用的 5xx 滑動窗口：5 分鐘內累積 5 次 5xx 觸發一次聚合告警。
var defaultFiveXXWindow = NewFiveXXWindow(5, 5*time.Minute)

// FiveXXAlert 包住 route handler，量測本服務自己 handler 寫出的狀態碼（不是 Railway/CDN edge 產生的
// 502/504，那些請求根本不會進到這個 Go process）。5 分鐘窗口內累積 5 次 5xx（500-599）時觸發一次
// Alert，觸發後立即重置窗口。掛在中介層鏈最內層（貼近實際 route handler），確保量到的是我方 handler
// 真正寫出的狀態碼。
func FiveXXAlert(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		status := ww.Status()
		if status >= 500 && status <= 599 {
			if triggered, count, sample := defaultFiveXXWindow.Record(r.Method, r.URL.Path, status); triggered {
				notify.Alert("http5xx", "API 5xx 激增",
					fmt.Sprintf("近5分鐘 %d 次 5xx，最近一筆: %s", count, sample))
			}
		}
	})
}

// PanicAlert 補在既有 chimiddleware.Recoverer「之後」（在中介層鏈中更內層、離實際 handler 更近）：
// 攔截到 panic 時先送一則 Telegram 告警，再原樣 re-panic，交由外層既有的 chimiddleware.Recoverer
// 接手寫 500 回應＋既有 log 行為——不改動、不取代既有 Recoverer 的職責，最小侵入。
func PanicAlert(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				notify.Alert("panic", "API 發生 Panic", fmt.Sprintf("%s %s: %v", r.Method, r.URL.Path, rec))
				panic(rec)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
