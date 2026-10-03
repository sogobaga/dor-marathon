// Package entrygate 供應商無關的「直連手錶入口」閘門（COROS GA 契約 §2.1／§2.2，Garmin 直連沿用）。
//
// 一支判斷函式、兩個呼叫端（契約：「Dashboard 入口與 API 閘門必須呼叫同一個判斷函式」）：
//   - profile.Dashboard 組 coros_mcp_entry／garmin_entry 時：ResolveFromSettings（已載入 email／帳號編碼／
//     是否超管，不再多查一次 users）。
//   - API 端點（connect／callback／import／自動同步）的前置閘門：Load（只有 userID，內部查 users）。
//     兩者最終都落到 Resolve；grep 守門測試（entrygate_test.go）確認各呼叫端沒有自己再寫一份白名單比對。
//
// 狀態（後台 app_settings，鍵由呼叫端指定，例如 coros_mcp_entry_state／garmin_entry_state）：
//
//	hidden    ＝緊急關閉：所有人（含超管）都 hidden。
//	whitelist ＝超管恆可，其餘需命中白名單（email 或帳號編碼）。缺鍵／空值／未知值一律視為 whitelist。
//	open      ＝全部開放。
//
// 注意：status 與 disconnect 端點「不受限制」（只要有連線列就能看、能中斷，契約 §2.2）——那兩支不得呼叫本套件。
package entrygate

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/appsettings"
)

// 入口狀態值（app_settings 的合法值）。
const (
	StateHidden    = "hidden"
	StateWhitelist = "whitelist"
	StateOpen      = "open"
)

// Resolve 的回傳值（與 profile Dashboard 其他 *_entry 欄位同一組字面值）。
const (
	Shown  = "shown"
	Hidden = "hidden"
)

// NormalizeState 把設定值正規化成 hidden|whitelist|open 三者之一：去頭尾空白、轉小寫；空值與任何未知值
// （例如直接下 SQL 寫進去的 'vip'、打錯字）一律視為 whitelist——「缺鍵＝whitelist」，未知值落到限制較多
// 的一邊，但不會比 hidden 更嚴（hidden 必須是明確寫入的值）。
func NormalizeState(state string) string {
	switch s := strings.ToLower(strings.TrimSpace(state)); s {
	case StateHidden, StateOpen, StateWhitelist:
		return s
	default:
		return StateWhitelist
	}
}

// Resolve 純函式（真值表見 entrygate_test.go）：回 "shown" 或 "hidden"。
//
//	state=hidden    → hidden（含超管）
//	state=open      → shown
//	state=whitelist（或空／未知）→ isSuper 或白名單命中 → shown，否則 hidden
//
// 白名單格式：換行／逗號／分號／空白分隔，項目可為 email 或帳號編碼（開頭的 # 可省略），大小寫不敏感。
func Resolve(state, whitelist, email, code string, isSuper bool) string {
	switch NormalizeState(state) {
	case StateHidden:
		return Hidden
	case StateOpen:
		return Shown
	default: // whitelist
		if isSuper || Whitelisted(whitelist, email, code) {
			return Shown
		}
		return Hidden
	}
}

// Whitelisted 白名單命中判斷（email 或帳號編碼，# 可省，大小寫不敏感；空白名單恆為 false）。
func Whitelisted(list, email, code string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	code = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(code), "#"))
	if email == "" && code == "" {
		return false
	}
	for _, tok := range strings.FieldsFunc(list, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';' || r == ' ' || r == '\t'
	}) {
		t := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(tok), "#"))
		if t == "" {
			continue
		}
		if (email != "" && t == email) || (code != "" && t == code) {
			return true
		}
	}
	return false
}

// LoadSettings 讀入口狀態與白名單（走 appsettings 的 60 秒進程內快取）。state 缺鍵＝whitelist；
// 白名單缺鍵＝defaultWL（由呼叫端的 EntryKeys 決定：可為 email／#帳號編碼 清單，也可為空字串＝只有超管可見）。
func LoadSettings(ctx context.Context, db *pgxpool.Pool, stateKey, wlKey, defaultWL string) (state, whitelist string) {
	state = appsettings.GetString(ctx, db, stateKey, StateWhitelist)
	whitelist = appsettings.GetString(ctx, db, wlKey, defaultWL)
	return state, whitelist
}

// ResolveFromSettings 給「已經載入使用者資料」的呼叫端（Dashboard）：讀設定後交給 Resolve。
func ResolveFromSettings(ctx context.Context, db *pgxpool.Pool, stateKey, wlKey, defaultWL, email, code string, isSuper bool) string {
	state, wl := LoadSettings(ctx, db, stateKey, wlKey, defaultWL)
	return Resolve(state, wl, email, code, isSuper)
}

// Load 給只有 userID 的呼叫端（API 閘門）：讀設定；hidden／open 不必查使用者就能決定，只有 whitelist
// 才查一次 users（email／帳號編碼／is_super_admin）。查詢失敗回 (Hidden, err)——呼叫端應 fail-closed。
func Load(ctx context.Context, db *pgxpool.Pool, stateKey, wlKey, defaultWL, userID string) (string, error) {
	state, wl := LoadSettings(ctx, db, stateKey, wlKey, defaultWL)
	switch NormalizeState(state) {
	case StateHidden:
		return Hidden, nil
	case StateOpen:
		return Shown, nil
	}
	if userID == "" {
		return Hidden, errors.New("entrygate: empty user id")
	}
	var email, code string
	var isSuper bool
	if err := db.QueryRow(ctx,
		`SELECT COALESCE(email,''), COALESCE(account_code,''), COALESCE(is_super_admin,FALSE) FROM users WHERE id=$1`,
		userID).Scan(&email, &code, &isSuper); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Hidden, nil // 不存在的使用者一律 hidden（不是錯誤）
		}
		return Hidden, err
	}
	return Resolve(state, wl, email, code, isSuper), nil
}

// Emergency 是否處於「緊急關閉」（state 明確為 hidden）。背景工作（自動同步、批次回補、sweep）用它在
// 不需要特定使用者的情況下整批停手；缺鍵＝whitelist＝非緊急。
func Emergency(ctx context.Context, db *pgxpool.Pool, stateKey string) bool {
	return NormalizeState(appsettings.GetString(ctx, db, stateKey, StateWhitelist)) == StateHidden
}
