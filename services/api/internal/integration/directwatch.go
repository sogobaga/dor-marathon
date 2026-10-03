package integration

// 「直連手錶」活動的單一判定（COROS GA 契約 §2.7／§1、Garmin 直連沿用）。
//
// 直連＝使用者在 DOR 自己授權、由我們直接向手錶廠商（COROS MCP／Garmin Health API）取得的活動：
//   - COROS MCP： source='coros'  AND external_id LIKE 'mcp:%'
//   - Garmin 直連：source='garmin' AND external_id LIKE 'gc:%'
// （Terra 與 COROS Partner 的列也是 source='coros'／'garmin'，但 external_id 不帶這兩個前綴，不算直連。）
//
// 三個用途共用這一份定義，避免各處各寫各的而漂移：
//  1. detectDuplicate：直連列後到、既有重疊列是 Strava → Strava 列改標 cross_source_duplicate（直連優先）。
//  2. profile/dedup.go 與 services/worker 的跨來源裁決排序：直連列排在 Strava 之前。
//  3. gpscalib 候選排除、DeleteProviderActivities 排除——這兩處不能 import integration（會循環），
//     SQL 片語以字面複製，directwatch_test.go 以測試守住三處完全一致。

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	directWatchCorosPrefix  = "mcp:"
	directWatchGarminPrefix = "gc:"
)

// IsDirectWatch 這筆活動（source＋external_id）是否為直連手錶匯入。
func IsDirectWatch(source, externalID string) bool {
	switch source {
	case "coros":
		return strings.HasPrefix(externalID, directWatchCorosPrefix)
	case "garmin":
		return strings.HasPrefix(externalID, directWatchGarminPrefix)
	}
	return false
}

var sqlAliasRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// DirectWatchSQL 回傳判斷「alias 這張 activities 是直連手錶列」的 SQL 布林片語（整段已加括號，可直接
// 接在 AND／OR／NOT 後面）。alias 為空字串時不加表別名。alias 必須是合法識別字（程式碼常數，不是使用者
// 輸入；格式不符視為程式錯誤直接 panic，寧可在測試時炸掉，也不要靜默產生會漏排除的 SQL）。
// external_id 以 COALESCE 轉成空字串後才做 LIKE：App GPS 列的 external_id 是 NULL，LIKE 結果會是 NULL，
// 放進 NOT(...) 會讓整句變成 NULL 而錯殺列，COALESCE 保證整段恆為 TRUE／FALSE。
func DirectWatchSQL(alias string) string {
	p := ""
	if alias != "" {
		if !sqlAliasRe.MatchString(alias) {
			panic(fmt.Sprintf("integration.DirectWatchSQL: invalid alias %q", alias))
		}
		p = alias + "."
	}
	return fmt.Sprintf(`((%[1]ssource='coros' AND COALESCE(%[1]sexternal_id,'') LIKE 'mcp:%%') OR (%[1]ssource='garmin' AND COALESCE(%[1]sexternal_id,'') LIKE 'gc:%%'))`, p)
}
