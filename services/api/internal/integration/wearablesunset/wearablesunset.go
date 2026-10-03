// Package wearablesunset Terra／Strava 串接結束公告（announce）的狀態讀取。
//
// 擁有者決定：Terra 與 Strava 的串接於 2026-10-31 結束。後台把 wearable_sunset_state 切到 announce 的那天起，
// 不得再建立任何新的 Terra／Strava 連接（後端與前台都擋），既有連線照常同步到結束日。本套件只負責「讀狀態」，
// 守門與前台文案各自在 internal/integration（terra.go／strava.go）與 apps/web/src/lib/wearableSunset.ts。
//
// 設計重點：
//   - 刻意不用 entrygate：entrygate 的語意是「誰能用」，缺鍵＝whitelist，套在這裡部署當下就會把既有串接收成
//     僅超管；這個開關的缺鍵必須是 off（完全不改變現狀）。
//   - leaf 套件：只依賴 appsettings（以及 pgxpool 型別），所以 integration／profile／ops… 都能 import 而不循環。
//   - 只在請求或 webhook 事件進來時才讀設定（走 appsettings 的 60 秒進程內快取，含 negative cache）；
//     沒有 ticker、沒有背景迴圈，也不在任何週期性路徑碰 DB（Neon 睡眠規則）。後台改設定時 appsettings 會立即清掉
//     本機快取，多副本最慢 60 秒內生效。
//   - DB 讀取「真的失敗」（逾時、連不上；不是缺鍵）時，沿用上一次成功讀到的狀態，但最多 10 分鐘（lastGoodMaxAge）；
//     沒有記錄或太舊就掉回 off（fail-open，與改版前相同）。這樣一次短暫的資料庫抖動不會讓公告悄悄關掉、放行新連接；
//     DB 長時間不可用時，建立連線列本身也會失敗，所以掉回 off 是可接受的取捨（把故障當成 announce 擋人才是更糟的結果）。
//     缺鍵不是故障：它是一次成功的讀取，結果是 off。沿用記錄不增加任何資料庫呼叫、goroutine 或 timer。
package wearablesunset

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/appsettings"
)

// 狀態值。
const (
	StateOff      = "off"      // 缺鍵、空字串與任何未知值：完全維持現狀
	StateAnnounce = "announce" // 不再開放新的連接；既有連線照常到結束日
)

const (
	// ErrorCode 被擋下的 API 回應體 code 欄位；前台 apps/web/src/lib/wearableSunset.ts 的 SUNSET_ERROR_CODE 必須相同
	// （scripts/verify-wearable-sunset.mjs 會與本檔對拍）。
	ErrorCode = "wearable_sunset"
	// ResultValue 瀏覽器導回前台時 ?strava=／?terra= 帶的結果值；前台 SUNSET_RESULT 必須相同（同上對拍）。
	ResultValue = "sunset"
	// RefusalStatus 被擋下的 API 回應狀態碼；前台 SUNSET_ERROR_STATUS 必須相同（同上對拍）。
	// 刻意用 409 而不是 403：middleware/ipdaily.go 把每個 401／403 計成該 IP 的登入失敗（auth_fails），每日 08:00 營運
	// 報告會把 ≥20 次的 IP 列為「異常登入失敗」——共用 NAT 的 IP 點了被擋的連接鈕就可能出現誤報；404 同理會被算成
	// not_found。也不能是 5xx：會進「API 5xx 激增」告警。請維持 4xx 且不是 401／403／404（單元測試會檢查）。
	RefusalStatus = http.StatusConflict
)

// Info 目前的公告狀態；同時是 /status 回應裡 sunset 欄位的 JSON 形狀。
// off 時只有 {"state":"off"}（date 省略），announce 時帶結束日。
type Info struct {
	State string `json:"state"`
	Date  string `json:"date,omitempty"` // YYYY-MM-DD（台北曆日、含當天）；只在 announce 帶出
}

// Announcing 是否處於公告期間（不再開放新連接）。
func (i Info) Announcing() bool { return i.State == StateAnnounce }

// EndLabel 結束日的中文標示，例如 "10 月 31 日"；日期不合法回空字串（呼叫端改用不含日期的句子）。
func (i Info) EndLabel() string { return DateLabel(i.Date) }

// Message 被擋下時給人看的中文句子（舊版前台會直接顯示 API 的 error 字串，所以必須是可讀中文）。
// 不含任何品牌或第三方背書字樣。
func (i Info) Message() string {
	if l := i.EndLabel(); l != "" {
		return "Terra 與 Strava 串接將於 " + l + "結束，目前不再開放新的連接。"
	}
	return "Terra 與 Strava 串接即將結束，目前不再開放新的連接。"
}

// ErrorBody 被擋下時的 JSON 回應體：error 是可讀中文，code／state／date 供新版前台判斷。
func (i Info) ErrorBody() map[string]string {
	return map[string]string{
		"error": i.Message(),
		"code":  ErrorCode,
		"state": i.State,
		"date":  i.Date,
	}
}

// Parse 把設定值正規化成 StateOff 或 StateAnnounce：去頭尾空白、轉小寫；空值與任何未知值
// （例如直接下 SQL 寫進去的 vip、closed、打錯字）一律視為 off——這個開關的預設必須是「不改變現狀」。
func Parse(raw string) string {
	if strings.ToLower(strings.TrimSpace(raw)) == StateAnnounce {
		return StateAnnounce
	}
	return StateOff
}

// ParseDate 把設定值正規化成合法的 YYYY-MM-DD；空字串或格式不合法一律回預設結束日。
func ParseDate(raw string) string {
	d := strings.TrimSpace(raw)
	if d == "" {
		return appsettings.WearableSunsetDefaultDate
	}
	if _, err := time.Parse("2006-01-02", d); err != nil {
		return appsettings.WearableSunsetDefaultDate
	}
	return d
}

// DateLabel "2026-10-31" → "10 月 31 日"（月日不補零）；不合法回空字串。
func DateLabel(date string) string {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(date))
	if err != nil {
		return ""
	}
	return strconv.Itoa(int(t.Month())) + " 月 " + strconv.Itoa(t.Day()) + " 日"
}

// Source 讀取目前的公告狀態。正式環境用 FromSettings；測試與未注入的 handler 用 Fixed。
type Source interface {
	Current(ctx context.Context) Info
}

const (
	// lastGoodMaxAge DB 讀取失敗時，最多沿用「上一次成功讀到的狀態」多久（含剛好 10 分鐘）。
	lastGoodMaxAge = 10 * time.Minute
	// degradedWarnEvery 降級警告日誌的最小間隔：故障期間每個請求都會走到降級路徑，不能每次都寫。
	degradedWarnEvery = time.Minute
)

// settingsSource 從 app_settings 讀公告狀態，並記住上一次「完整成功」讀到的結果（只在記憶體裡，隨行程）。
// lookup／now 可注入（測試用）；正式環境是 appsettings.LookupString 與 time.Now。
// 記憶是每個 source 各自一份（Terra 與 Strava 的 handler 各建各的）：兩者都是請求驅動，穩態下各自都剛讀過。
type settingsSource struct {
	lookup func(ctx context.Context, key string) (value string, found bool, err error)
	now    func() time.Time

	mu       sync.Mutex
	last     Info      // 上一次完整成功讀到的狀態
	lastAt   time.Time // 讀到的時間
	hasLast  bool
	warnedAt time.Time // 上一次寫降級警告日誌的時間
}

// FromSettings 從 app_settings 讀狀態（60 秒進程內快取；只在被呼叫時才讀）。db 為 nil 回恆 off 的 Source，
// 所以不帶資料庫的單元測試不會因為 nil pool 而 panic。
func FromSettings(db *pgxpool.Pool) Source {
	if db == nil {
		return Fixed(Info{State: StateOff})
	}
	return &settingsSource{
		lookup: func(ctx context.Context, key string) (string, bool, error) {
			return appsettings.LookupString(ctx, db, key) // 分得出「故障」與「缺鍵」；GetString 會把兩者都吞成預設值
		},
		now: time.Now,
	}
}

// Current off 時只讀一個鍵（狀態）；announce 才多讀結束日。讀取失敗的處理見 degraded。
func (s *settingsSource) Current(ctx context.Context) Info {
	state, _, err := s.lookup(ctx, appsettings.WearableSunsetStateKey)
	if err != nil {
		return s.degraded(err, false)
	}
	if Parse(state) != StateAnnounce {
		return s.remember(Info{State: StateOff}) // 缺鍵、空值、未知值都是「成功讀到 off」
	}
	date, _, err := s.lookup(ctx, appsettings.WearableSunsetDateKey)
	if err != nil {
		return s.degraded(err, true)
	}
	return s.remember(Info{State: StateAnnounce, Date: ParseDate(date)})
}

// remember 記下這次成功讀到的狀態（供之後 DB 讀取失敗時沿用）並原樣回傳。
func (s *settingsSource) remember(i Info) Info {
	at := s.now()
	s.mu.Lock()
	s.last, s.lastAt, s.hasLast = i, at, true
	s.mu.Unlock()
	return i
}

// degraded DB 讀取真的失敗時的決定（不做任何新的 DB 呼叫、不重試）。knownAnnounce＝這次已經成功讀到 state=announce，
// 只是讀不到結束日。
//   - 記錄存在且不超過 lastGoodMaxAge：沿用它（knownAnnounce 時，記錄必須也是 announce 才能沿用它的結束日）。
//     沿用不會更新記錄的時間，所以時效從「成功讀到的那一刻」起算，不會越用越久。
//   - 否則 knownAnnounce：announce＋預設結束日（與改版前 GetString 讀失敗回預設值的行為相同；公告不因為讀不到日期而關掉）。
//   - 否則：off（改版前的行為）。
func (s *settingsSource) degraded(err error, knownAnnounce bool) Info {
	now := s.now()
	s.mu.Lock()
	last, hadLast, lastAt := s.last, s.hasLast, s.lastAt
	warn := now.Sub(s.warnedAt) >= degradedWarnEvery
	if warn {
		s.warnedAt = now
	}
	s.mu.Unlock()

	age := now.Sub(lastAt)
	var out Info
	var decision string
	switch {
	case hadLast && age <= lastGoodMaxAge && (!knownAnnounce || last.Announcing()):
		out, decision = last, "reused the last known state"
	case knownAnnounce:
		out, decision = Info{State: StateAnnounce, Date: ParseDate("")}, "announce with the default end date"
	default:
		out, decision = Info{State: StateOff}, "off (no recent known state)"
	}
	if warn {
		log.Warn().Err(err).Str("decision", decision).Str("state", out.State).Dur("last_good_age", age).
			Msg("wearablesunset: settings read failed (not a missing key)")
	}
	return out
}

type fixedSource struct{ info Info }

// Fixed 回固定狀態的 Source（測試注入用）。State 會經過 Parse 正規化、announce 的 Date 經過 ParseDate，
// 與 FromSettings 對同樣輸入的結果一致。
func Fixed(i Info) Source {
	if Parse(i.State) != StateAnnounce {
		return fixedSource{info: Info{State: StateOff}}
	}
	return fixedSource{info: Info{State: StateAnnounce, Date: ParseDate(i.Date)}}
}

func (f fixedSource) Current(context.Context) Info { return f.info }
