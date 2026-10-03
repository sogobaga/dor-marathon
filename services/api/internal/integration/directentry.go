package integration

// 直連手錶入口的「鍵對照」與 Dashboard 入口判斷（GA 契約 §2.1）。
//
// 契約要求：Dashboard 的入口欄位與 API 閘門必須呼叫「同一個判斷函式」。判斷本體是
// entrygate.Resolve；這個檔案只負責把「哪個供應商用哪組 app_settings 鍵」集中成一處，
// profile.Dashboard 與各 handler 都從這裡取鍵，避免鍵名散落、改一邊漏一邊。

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/integration/entrygate"
)

// 供應商代碼（DashboardEntry／EntryKeys 的 provider 參數）。
const (
	EntryProviderCoros  = "coros_mcp"
	EntryProviderGarmin = "garmin"
)

// Garmin 直連入口的設定鍵（白名單預設空字串＝只有超管；COROS 的鍵見 corosmcp.go）。
const (
	GarminEntryStateKey = "garmin_entry_state"
	GarminWhitelistKey  = "garmin_whitelist"
)

// EntryKeys 某供應商入口的設定鍵：狀態鍵、白名單鍵、白名單缺鍵預設值。provider 未知回 ok=false。
func EntryKeys(provider string) (stateKey, wlKey, defaultWL string, ok bool) {
	switch provider {
	case EntryProviderCoros:
		return corosMcpEntryStateKey, corosMcpWhitelistKey, corosMcpDefaultWhitelist, true
	case EntryProviderGarmin:
		return GarminEntryStateKey, GarminWhitelistKey, "", true
	}
	return "", "", "", false
}

// DashboardEntry profile.Dashboard 用：已載入使用者 email／帳號編碼／是否超管，直接解出 "shown"｜"hidden"。
// 與 API 閘門（EntryForUser → entrygate.Load）最後都落在 entrygate.Resolve。provider 未知一律 hidden。
func DashboardEntry(ctx context.Context, db *pgxpool.Pool, provider, email, code string, isSuper bool) string {
	stateKey, wlKey, defaultWL, ok := EntryKeys(provider)
	if !ok {
		return entrygate.Hidden
	}
	return entrygate.ResolveFromSettings(ctx, db, stateKey, wlKey, defaultWL, email, code, isSuper)
}

// EntryForUser API 閘門用：只有 userID。provider 未知回 (hidden, nil)。
func EntryForUser(ctx context.Context, db *pgxpool.Pool, provider, userID string) (string, error) {
	stateKey, wlKey, defaultWL, ok := EntryKeys(provider)
	if !ok {
		return entrygate.Hidden, nil
	}
	return entrygate.Load(ctx, db, stateKey, wlKey, defaultWL, userID)
}
