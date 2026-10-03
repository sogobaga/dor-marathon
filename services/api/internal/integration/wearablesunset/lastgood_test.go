package wearablesunset

// DB 讀取「真的失敗」（不是缺鍵）時，Current 沿用上一次成功讀到的狀態——但最多 10 分鐘；沒有或太舊就掉回 off。
// 缺鍵不是故障：它是一次成功的讀取，結果是 off（而且會蓋掉先前記下的 announce）。
// 全部不連資料庫：用假的設定讀取與假的時鐘注入 settingsSource（真資料庫的版本見 integration_test.go）。

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/appsettings"
)

var errDBDown = errors.New("db unavailable")

// fakeSettings 假的設定讀取：vals 沒有的 key＝查無（found=false、err=nil）；err／errKeys 讓讀取失敗；記下被讀了哪些 key。
type fakeSettings struct {
	mu      sync.Mutex
	vals    map[string]string
	err     error
	errKeys map[string]error
	calls   []string
}

func (f *fakeSettings) lookup(_ context.Context, key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, key)
	if f.err != nil {
		return "", false, f.err
	}
	if e := f.errKeys[key]; e != nil {
		return "", false, e
	}
	v, ok := f.vals[key]
	return v, ok, nil
}

func (f *fakeSettings) set(err error)  { f.mu.Lock(); f.err = err; f.mu.Unlock() }
func (f *fakeSettings) callCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }
func (f *fakeSettings) resetCalls()    { f.mu.Lock(); f.calls = nil; f.mu.Unlock() }

// fakeClock 手動撥的時鐘。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) set(t time.Time) { c.mu.Lock(); c.now = t; c.mu.Unlock() }

var (
	t0           = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	announceInfo = Info{State: StateAnnounce, Date: "2026-11-15"}
	offInfo      = Info{State: StateOff}
	defaultAnn   = Info{State: StateAnnounce, Date: appsettings.WearableSunsetDefaultDate}
)

func announceStore() *fakeSettings {
	return &fakeSettings{vals: map[string]string{
		appsettings.WearableSunsetStateKey: "announce",
		appsettings.WearableSunsetDateKey:  "2026-11-15",
	}}
}

func newTestSource(f *fakeSettings, c *fakeClock) *settingsSource {
	return &settingsSource{lookup: f.lookup, now: c.Now}
}

func TestCurrent_ErrorWithinTenMinutesKeepsTheLastAnnounce(t *testing.T) {
	ctx := context.Background()
	f, c := announceStore(), &fakeClock{now: t0}
	s := newTestSource(f, c)
	if got := s.Current(ctx); got != announceInfo {
		t.Fatalf("precondition: %+v", got)
	}
	f.set(errDBDown)
	for _, age := range []time.Duration{0, time.Second, 5 * time.Minute, 9*time.Minute + 59*time.Second, 10 * time.Minute} {
		c.set(t0.Add(age))
		if got := s.Current(ctx); got != announceInfo {
			t.Errorf("DB error %s after the last good read: Current() = %+v, want the remembered %+v", age, got, announceInfo)
		}
	}
}

func TestCurrent_ErrorAfterTenMinutesFallsBackToOff(t *testing.T) {
	ctx := context.Background()
	f, c := announceStore(), &fakeClock{now: t0}
	s := newTestSource(f, c)
	s.Current(ctx)
	f.set(errDBDown)
	for _, age := range []time.Duration{10*time.Minute + time.Nanosecond, 10*time.Minute + time.Second, time.Hour, 24 * time.Hour} {
		c.set(t0.Add(age))
		if got := s.Current(ctx); got != offInfo {
			t.Errorf("DB error %s after the last good read: Current() = %+v, want off (the remembered state is too old)", age, got)
		}
	}
	// 之後資料庫恢復：立刻回到真實狀態
	f.set(nil)
	if got := s.Current(ctx); got != announceInfo {
		t.Errorf("after the database recovers: %+v, want %+v", got, announceInfo)
	}
}

func TestCurrent_ErrorWithNoPriorReadIsOff(t *testing.T) {
	f, c := announceStore(), &fakeClock{now: t0}
	f.set(errDBDown)
	if got := newTestSource(f, c).Current(context.Background()); got != offInfo {
		t.Fatalf("an error with nothing remembered must fall back to off (today's behaviour), got %+v", got)
	}
}

// 沿用不會把時效往後延：記下的狀態從「成功讀到的那一刻」起算 10 分鐘，中途一直失敗也不會越用越久。
func TestCurrent_ReusingDoesNotExtendTheWindow(t *testing.T) {
	ctx := context.Background()
	f, c := announceStore(), &fakeClock{now: t0}
	s := newTestSource(f, c)
	s.Current(ctx)
	f.set(errDBDown)
	for _, m := range []int{3, 6, 9} {
		c.set(t0.Add(time.Duration(m) * time.Minute))
		if got := s.Current(ctx); got != announceInfo {
			t.Fatalf("minute %d: %+v", m, got)
		}
	}
	c.set(t0.Add(11 * time.Minute))
	if got := s.Current(ctx); got != offInfo {
		t.Fatalf("11 minutes after the last good read (reuse in between must not refresh it): %+v, want off", got)
	}
}

// 缺鍵不是故障：它是一次成功的讀取（結果 off），會蓋掉先前記下的 announce；之後再失敗，沿用的是 off，不是更早的 announce。
func TestCurrent_AMissingKeyIsASuccessfulReadOfOff(t *testing.T) {
	ctx := context.Background()
	f, c := announceStore(), &fakeClock{now: t0}
	s := newTestSource(f, c)
	s.Current(ctx) // announce
	f.mu.Lock()
	delete(f.vals, appsettings.WearableSunsetStateKey) // 缺鍵（沒有錯誤）
	f.mu.Unlock()
	c.set(t0.Add(time.Minute))
	if got := s.Current(ctx); got != offInfo {
		t.Fatalf("a missing key is off, got %+v", got)
	}
	f.set(errDBDown)
	c.set(t0.Add(2 * time.Minute))
	if got := s.Current(ctx); got != offInfo {
		t.Fatalf("an error after a successful read of off must reuse off, not the older announce: %+v", got)
	}
}

// 管理者把狀態改回 off 後，之後的故障也沿用 off；改成 announce 同理。
func TestCurrent_ReusesWhicheverStateWasReadLast(t *testing.T) {
	ctx := context.Background()
	f, c := announceStore(), &fakeClock{now: t0}
	s := newTestSource(f, c)
	s.Current(ctx) // announce
	f.mu.Lock()
	f.vals[appsettings.WearableSunsetStateKey] = "off"
	f.mu.Unlock()
	c.set(t0.Add(time.Minute))
	if got := s.Current(ctx); got != offInfo {
		t.Fatalf("%+v", got)
	}
	f.set(errDBDown)
	c.set(t0.Add(2 * time.Minute))
	if got := s.Current(ctx); got != offInfo {
		t.Fatalf("an error after reading off must keep off, got %+v", got)
	}
	f.set(nil)
	f.mu.Lock()
	f.vals[appsettings.WearableSunsetStateKey] = "announce"
	f.vals[appsettings.WearableSunsetDateKey] = "2026-12-01"
	f.mu.Unlock()
	c.set(t0.Add(3 * time.Minute))
	if got := s.Current(ctx); got != (Info{State: StateAnnounce, Date: "2026-12-01"}) {
		t.Fatalf("%+v", got)
	}
	f.set(errDBDown)
	c.set(t0.Add(4 * time.Minute))
	if got := s.Current(ctx); got != (Info{State: StateAnnounce, Date: "2026-12-01"}) {
		t.Fatalf("an error after reading announce must keep that announce (with its date), got %+v", got)
	}
}

// 已經讀到 state=announce、只有結束日讀失敗：公告不能因此掉成 off。沿用上次的結束日（時效內）；沒有就用預設結束日
// （與改版前 GetString 讀失敗回預設值的行為相同）。
func TestCurrent_AnnounceKnownButTheDateReadFails(t *testing.T) {
	ctx := context.Background()

	// 沒有任何記錄：announce＋預設結束日
	f, c := announceStore(), &fakeClock{now: t0}
	f.errKeys = map[string]error{appsettings.WearableSunsetDateKey: errDBDown}
	if got := newTestSource(f, c).Current(ctx); got != defaultAnn {
		t.Errorf("no history: %+v, want %+v", got, defaultAnn)
	}

	// 時效內有上次的 announce（結束日 2026-11-15）：沿用它的結束日
	f, c = announceStore(), &fakeClock{now: t0}
	s := newTestSource(f, c)
	s.Current(ctx)
	f.mu.Lock()
	f.errKeys = map[string]error{appsettings.WearableSunsetDateKey: errDBDown}
	f.mu.Unlock()
	c.set(t0.Add(5 * time.Minute))
	if got := s.Current(ctx); got != announceInfo {
		t.Errorf("fresh history: %+v, want %+v", got, announceInfo)
	}

	// 上次的記錄太舊：不信任它的結束日，用預設結束日（仍是 announce）
	c.set(t0.Add(11 * time.Minute))
	if got := s.Current(ctx); got != defaultAnn {
		t.Errorf("stale history: %+v, want %+v", got, defaultAnn)
	}

	// 時效內的記錄是 off，但這次已經讀到 announce：不能沿用 off
	f, c = announceStore(), &fakeClock{now: t0}
	s = newTestSource(f, c)
	f.mu.Lock()
	f.vals[appsettings.WearableSunsetStateKey] = "off"
	f.mu.Unlock()
	s.Current(ctx) // off
	f.mu.Lock()
	f.vals[appsettings.WearableSunsetStateKey] = "announce"
	f.errKeys = map[string]error{appsettings.WearableSunsetDateKey: errDBDown}
	f.mu.Unlock()
	c.set(t0.Add(time.Minute))
	if got := s.Current(ctx); got != defaultAnn {
		t.Errorf("state now announce (read ok) while the remembered state is off: %+v, want %+v", got, defaultAnn)
	}
}

// 不增加任何資料庫呼叫：失敗時不重試、沿用記錄不再多讀；每次 Current 最多讀兩個鍵（狀態、announce 時再加結束日）。
func TestCurrent_NeverReadsMoreThanBefore(t *testing.T) {
	ctx := context.Background()
	f, c := announceStore(), &fakeClock{now: t0}
	s := newTestSource(f, c)

	s.Current(ctx)
	if n := f.callCount(); n != 2 {
		t.Errorf("announce read = %d lookups, want 2 (state + date)", n)
	}
	f.resetCalls()
	f.set(errDBDown)
	s.Current(ctx)
	if n := f.callCount(); n != 1 {
		t.Errorf("a failed read must stop after the first lookup (no retry, no extra reads), got %d", n)
	}
	f.resetCalls()
	f.set(nil)
	f.mu.Lock()
	f.vals[appsettings.WearableSunsetStateKey] = "off"
	f.mu.Unlock()
	s.Current(ctx)
	if n := f.callCount(); n != 1 {
		t.Errorf("an off read = %d lookups, want 1 (the date is only read while announcing)", n)
	}
}

// 並發呼叫（所有請求共用同一個 Source）：不 panic、不死鎖，結果永遠是合法的狀態。
func TestCurrent_ConcurrentCallsAreSafe(t *testing.T) {
	f, c := announceStore(), &fakeClock{now: t0}
	s := newTestSource(f, c)
	s.Current(context.Background())

	var failing atomic.Bool
	g := &fakeSettings{vals: f.vals}
	s.lookup = func(ctx context.Context, key string) (string, bool, error) {
		if failing.Load() {
			return "", false, errDBDown
		}
		return g.lookup(ctx, key)
	}

	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				failing.Store((i+w)%3 == 0)
				switch got := s.Current(context.Background()); got {
				case announceInfo, offInfo:
				default:
					t.Errorf("unexpected state %+v", got)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}

// 正式接線：FromSettings 讀設定走的是 appsettings.LookupString（分得出故障與缺鍵），不是 GetString（把故障吞成預設值）。
// 用已關閉的連線池當作「DB 故障」：不連網路，對它的任何查詢都立刻回錯。
func TestFromSettings_UsesTheErrorAwareLookup(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://nobody:nopass@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	appsettings.InvalidateCache()
	t.Cleanup(appsettings.InvalidateCache)

	src, ok := FromSettings(pool).(*settingsSource)
	if !ok {
		t.Fatalf("FromSettings(non-nil pool) must return *settingsSource, got %T", FromSettings(pool))
	}
	if _, _, err := src.lookup(context.Background(), appsettings.WearableSunsetStateKey); err == nil {
		t.Fatal("the production lookup must surface a database error instead of swallowing it")
	}
	if got := src.Current(context.Background()); got != offInfo {
		t.Fatalf("failing database with nothing remembered must be off, got %+v", got)
	}
}
