// Package live 團練同步跑（Group Run Live）的即時位置同步層。
//
// 契約：docs/runmeet/GROUP_RUN_LIVE_CONTRACT.md（本套件是 P1「後端與協定」的實作）。
//
// ⚠️ 架構鐵則（改這個套件前先讀）：
//
//  1. **葉節點套件**：只 import go-redis／chi／uuid／標準庫／internal/middleware（取 ctx 使用者）。
//     **不得 import internal/runmeet**——runmeet 會 import 本套件（撤銷掛鉤、/live/start），反向
//     import 會形成循環。也**不得 import pgx／appsettings／任何會碰 PostgreSQL 的東西**：
//     熱路徑（/pos、/leave）「零 PostgreSQL 查詢」是用型別保證的——本套件根本拿不到 *pgxpool.Pool。
//     （live_arch_test.go 以 go/parser 檢查 import 清單，違反會直接紅燈。）
//  2. **他人位置只存在 Redis 記憶體、只保留最新一點**：無歷史、無軌跡、不寫 DB、不寫 log。
//     本套件**不得**記錄 request body 或座標（連 Redis 錯誤都不記，見 live_arch_test.go 的
//     「不得 import 任何 logger」檢查）；要觀測請看回應狀態碼（5xx 已由 FiveXXAlert 聚合告警）。
//  3. 不新增任何背景 goroutine／排程（Neon 要能睡）。
//  4. Redis 只用 ≥ 6.0 的指令（PEXPIRE／PX，不用 EXAT）；時間一律 Lua 內 redis.call('TIME')。
package live

import "time"

// --- app_settings 鍵（無列時用程式預設；不需 migration）---

const (
	// EntryStateKey 入口狀態 hidden／locked／whitelist／open（語意同 runmeet_entry_state）。
	// ⚠️ hidden 同時是「緊急關閉」：appsettings 後台寫入此鍵為 hidden 時 runmeet 會 SET rml:kill
	// （見 runmeet.registerLiveKillSwitch），所以 hidden 對所有人（含超管）一律關閉。
	EntryStateKey = "runmeet_live_entry_state"
	// EntryWhitelistKey 白名單（換行／逗號分隔的帳號編碼或 email，格式同 runmeet_entry_whitelist）。
	EntryWhitelistKey = "runmeet_live_whitelist"
	// MaxKey 同一團練同時持有 grant 的人數上限。
	MaxKey = "runmeet_live_max"
	// PreMinutesKey 開放起點 = meet_at − pre。
	PreMinutesKey = "runmeet_live_pre_minutes"
	// DefaultHoursKey ends_at 為 NULL 時的結束 = meet_at + 此值（小時）。
	DefaultHoursKey = "runmeet_live_default_hours"
	// GraceMinutesKey 開放終點 = 結束 + grace。
	GraceMinutesKey = "runmeet_live_grace_minutes"
)

// 各設定鍵的程式預設值（契約 §2）。
const (
	DefEntryState     = "whitelist"
	DefEntryWhitelist = ""
	DefMax            = 50
	DefPreMinutes     = 30
	DefDefaultHours   = 3
	DefGraceMinutes   = 30

	// MaxLiveCeiling 後台可調的同步上限天花板（appsettings 驗證器與 Settings.Normalize 共用的
	// 單一數字）。Lua 每個 /pos 都會線性掃描 pos／hb 雜湊，上限越大單次越貴、快照越大
	// （N 人 × N 筆，下行流量隨人數平方成長），所以不開放到團練本身的 500 人上限。
	MaxLiveCeiling = 200
)

// Settings 一次讀齊的同步跑設定（由 runmeet 端從 app_settings 載入後傳入）。
type Settings struct {
	EntryState   string
	Whitelist    string
	Max          int
	PreMinutes   int
	DefaultHours int
	GraceMinutes int
}

// DefaultSettings 回程式內建預設（無列時使用）。
func DefaultSettings() Settings {
	return Settings{
		EntryState:   DefEntryState,
		Whitelist:    DefEntryWhitelist,
		Max:          DefMax,
		PreMinutes:   DefPreMinutes,
		DefaultHours: DefDefaultHours,
		GraceMinutes: DefGraceMinutes,
	}
}

// Normalize 把不合理的值（<=0 的上限、負的時窗參數、超過天花板）夾回安全範圍。
// 後台驗證器已擋掉大部分，這裡是讀取端的第二道保險（例如有人直接用 SQL 寫壞值）。
func (s Settings) Normalize() Settings {
	d := DefaultSettings()
	if s.EntryState == "" {
		s.EntryState = d.EntryState
	}
	if s.Max <= 0 {
		s.Max = d.Max
	}
	if s.Max > MaxLiveCeiling {
		s.Max = MaxLiveCeiling
	}
	if s.PreMinutes < 0 {
		s.PreMinutes = d.PreMinutes
	}
	if s.DefaultHours <= 0 {
		s.DefaultHours = d.DefaultHours
	}
	if s.GraceMinutes < 0 {
		s.GraceMinutes = d.GraceMinutes
	}
	return s
}

// Window 開放時窗（契約 §2）：
//
//	opens_at  = meet_at − pre
//	closes_at = COALESCE(ends_at, meet_at + default_hours) + grace
//
// ⚠️ 不能直接用團練的 effectiveEnd/phase 當閘門：ends_at 為 NULL 時 effectiveEnd = meet_at，
// 團練在 meet_at 當下就算「已結束」，但同步時窗必須撐到 meet_at + default_hours + grace。
func (s Settings) Window(meetAt time.Time, endsAt *time.Time) (opens, closes time.Time) {
	s = s.Normalize()
	opens = meetAt.Add(-time.Duration(s.PreMinutes) * time.Minute)
	end := meetAt.Add(time.Duration(s.DefaultHours) * time.Hour)
	if endsAt != nil {
		end = *endsAt
	}
	closes = end.Add(time.Duration(s.GraceMinutes) * time.Minute)
	return opens, closes
}

// InWindow now ∈ [opens, closes)（起點含、終點不含）。
func InWindow(now, opens, closes time.Time) bool {
	return !now.Before(opens) && now.Before(closes)
}

// --- 協定常數 ---

const (
	// ProtocolVersion 請求／回應的 pv。部署期間舊 bundle 的頁面可能還在跑，不符就回 426。
	ProtocolVersion = 1

	// MaxBodyBytes 同步跑三個端點的 request body 上限（http.MaxBytesReader）。
	MaxBodyBytes = 512

	// GrantTTL grant 有界過期（M1）：任何漏掛的撤銷路徑最壞暴露這麼久；client 在剩 ReauthMargin
	// 時靜默重呼叫 /live/start（reauth=true）。
	GrantTTL     = 15 * time.Minute
	ReauthMargin = 5 * time.Minute

	// KeyTTL grants／idx／names／meta 的 TTL（每次寫入刷新）。
	KeyTTL = 24 * time.Hour
	// PosTTL pos／hb 雜湊的 TTL（每次成功寫入刷新）。
	PosTTL = 4 * time.Minute
	// FloorMs 頻率地板：同一人兩次 /pos 至少間隔（SET NX PX）。
	FloorMs = 1500
	// TombstoneTTL 撤銷墓碑 rv:<uid> 的存活時間。
	TombstoneTTL = 15 * time.Minute

	// FreshMs「有自己的位置」與「算在線」的新鮮度門檻（互惠規則 C1、live 計數共用）。
	FreshMs = 60_000

	// DropS 快照不含 age 超過此秒數者（Lua 讀取時順便 HDEL）。
	DropS = 180

	// RedisTimeout Store 每一次 Redis 往返的硬期限（含撥號、等連線池、寫入、等回應）。逾時 → ErrUnavailable →
	// handler 回 503 redis_unavailable；client 退避、跑步不受影響。見 Store 與 cache.Bounded 的說明。
	RedisTimeout = 2 * time.Second
)

// IntervalMs 伺服器下發的更新間隔（依同步人數自動調整，擁有者 2026-10-02 拍板）：
// ≤20 人 5 s、21–35 人 7 s、36 人以上 10 s。
func IntervalMs(live int) int {
	switch {
	case live <= 20:
		return 5000
	case live <= 35:
		return 7000
	default:
		return 10000
	}
}

// Stale 他人亮點的陳舊度門檻（秒），由 iv 計算（審查 M3：門檻必須隨間隔縮放，否則健康但
// 慢速上傳的跑者會被誤標陳舊）：fade = max(3·iv, 30s)、gray = max(8·iv, 75s)、drop = 180s。
type Stale struct {
	FadeS int `json:"fade_s"`
	GrayS int `json:"gray_s"`
	DropS int `json:"drop_s"`
}

// StaleFor 依 iv（毫秒）計算陳舊度門檻。
func StaleFor(ivMs int) Stale {
	fade := 3 * ivMs / 1000
	if fade < 30 {
		fade = 30
	}
	gray := 8 * ivMs / 1000
	if gray < 75 {
		gray = 75
	}
	return Stale{FadeS: fade, GrayS: gray, DropS: DropS}
}
