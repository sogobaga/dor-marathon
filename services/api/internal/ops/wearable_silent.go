package ops

// 日報「疑似靜默中斷」的查詢與格式（原本在 dailyreport.go 的 buildWearableSilentWarnings，2026-10-03 為 COROS GA 契約
// §2.9／計畫 S5 調整，獨立成檔方便單元測試）：
//
//  1. COROS 改以 source='coros' AND external_id LIKE 'mcp:%' 參與偵測（provider 'coros_mcp' 的連線，只認 mcp: 活動）；
//     先前這類連線整個被排除（Stage 1 不匯入活動），GA 後才有意義。
//  2. garmin／coros／coros_mcp（直連手錶）只印「人數」，不印顯示名稱（政策稿前提 P5：日報 Telegram 不印顯示名稱）；
//     SQL 端就把這幾個 provider 的 display_name 清成空字串，名稱根本不會離開資料庫。其餘 provider（strava、polar、
//     suunto、wahoo）維持原本的具名清單。
//  3. 「保護期」（連線至少存在 3 天才可能被判靜默，避免剛連上、第一筆資料還沒同步的新連結被誤判）：直連手錶改用
//     updated_at（重新授權不再重設 created_at＝匯入 floor，若仍看 created_at，重新授權成功的連線會立刻被判
//     「靜默」）；其餘 provider 照舊用 created_at。

import (
	"fmt"
	"strings"
)

// silentCountOnlyProviders 只印人數、不印顯示名稱的 provider。
var silentCountOnlyProviders = map[string]bool{"garmin": true, "coros": true, "coros_mcp": true}

// silentProviderMatch 「alias 這張 activities 是 ui 這條連線的資料來源」的 SQL 布林片語（null-safe：App GPS 的
// source IS NULL 不會讓整句變 NULL）。coros_mcp 連線只認 COROS MCP 匯入的活動（source='coros' 且 external_id 'mcp:%'），
// 其餘 provider 照 source = provider。
func silentProviderMatch(alias string) string {
	return "(CASE WHEN ui.provider = 'coros_mcp' THEN (" + alias + ".source IS NOT DISTINCT FROM 'coros' AND COALESCE(" + alias + ".external_id,'') LIKE 'mcp:%')" +
		" ELSE " + alias + ".source IS NOT DISTINCT FROM ui.provider END)"
}

// wearableSilentSQL 查「疑似靜默中斷」的連線（參數 $1＝cutoff＝now−3 天）：近 3 天完全沒有該連線來源的活動，但同一使用者同期間
// 卻有其他來源（App GPS 或別的 provider）的活動。刻意不排除 flagged：跨來源重複等被標記的活動照樣證明「這條管線有送資料進來」。
// JOIN 加 NOT u.is_virtual＝防禦性（虛擬選手不走 OAuth／Terra）。
var wearableSilentSQL = `
	SELECT
		ui.provider,
		CASE WHEN ui.provider IN ('garmin','coros','coros_mcp') THEN '' ELSE COALESCE(u.name, u.handle) END AS display_name,
		(
			SELECT MAX(a.recorded_at)
			FROM activities a
			WHERE a.user_id = ui.user_id AND ` + silentProviderMatch("a") + `
		) AS last_provider_activity
	FROM user_integrations ui
	JOIN users u ON u.id = ui.user_id AND NOT u.is_virtual
	WHERE (CASE WHEN ui.provider IN ('garmin','coros','coros_mcp') THEN ui.updated_at ELSE ui.created_at END) < $1
	  AND NOT EXISTS (
		SELECT 1 FROM activities a2
		WHERE a2.user_id = ui.user_id
		  AND ` + silentProviderMatch("a2") + `
		  AND a2.recorded_at >= $1
	  )
	  AND EXISTS (
		SELECT 1 FROM activities a3
		WHERE a3.user_id = ui.user_id
		  AND NOT ` + silentProviderMatch("a3") + `
		  AND a3.recorded_at >= $1
	  )
	ORDER BY ui.provider, display_name
`

// silentProviderLabel 日報上的 provider 顯示名稱。
func silentProviderLabel(provider string) string {
	if provider == "coros_mcp" {
		return "COROS 直連"
	}
	return strings.ToUpper(provider)
}

// formatWearableSilentCountLine 單一直連 provider 的「疑似靜默中斷」人數行（只有人數，不含任何個人資訊）。
func formatWearableSilentCountLine(provider string, n int) string {
	return fmt.Sprintf("⚠️ %s：%d 人疑似已停止同步（近 3 天沒有該來源的活動，同期間其他來源仍有跑步紀錄）", silentProviderLabel(provider), n)
}

// formatWearableSilentLines 把查詢結果轉成日報行：直連 provider（garmin／coros／coros_mcp）彙總成人數行，
// 其餘 provider 沿用原本的具名行（formatWearableSilentLine）。即使呼叫端傳進直連 provider 的 DisplayName 也不會印出。
func formatWearableSilentLines(list []WearableSilentConnection) []string {
	var out []string
	counts := map[string]int{}
	var order []string
	for _, s := range list {
		if silentCountOnlyProviders[s.Provider] {
			if counts[s.Provider] == 0 {
				order = append(order, s.Provider)
			}
			counts[s.Provider]++
			continue
		}
		out = append(out, formatWearableSilentLine(s))
	}
	for _, p := range order {
		out = append(out, formatWearableSilentCountLine(p, counts[p]))
	}
	return out
}
