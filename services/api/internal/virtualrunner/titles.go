// 虛擬選手稱號同步：套用既有稱號引擎（internal/profile，見 titles_export.go 匯出面）解鎖稱號，
// 並依人設（等級/勤勞度）從已解鎖稱號中挑一個設為展示稱號（users.displayed_title）。
//
// 「只有在該選手解鎖到新稱號時才重新評估展示稱號」是使用者明確拍板的規則（2026-09-01）：機器人
// 沒有「主動想換稱號展示」的語境，避免每輪生成都翻動展示稱號、造成排行榜稱號無意義地跳動。
// displayed_title 目前為空（剛回填/首次同步）視為需要評估的特例，否則新選手永遠沒有稱號可展示。
package virtualrunner

import (
	"context"
	"fmt"

	"github.com/dor/api/internal/profile"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// fallbackCategoryOrder 偏好類別沒有解鎖任何稱號時，依序退而求其次的類別順序。
var fallbackCategoryOrder = []string{"cum_dist", "single_dist", "cum_time"}

// preferredCategory 依人設決定偏好類別（三條規則互斥、由上到下取第一個命中者，見套件檔頭規格）：
//  1. 等級屬於後段（sort_order 在 vr_level_presets 全部等級的後半，偏菁英）→ single_dist（單次
//     距離型稱號較「強者」）
//  2. diligence >= 4 → cum_time（勤奮＝累積時間型）
//  3. 其餘 → cum_dist
//
// "後半" 用 sortOrder > maxSortOrder/2（整數除法）動態判斷，不寫死「8 級」——presets 表未來若
// 增減等級，這裡不必跟著改。
func preferredCategory(sortOrder, maxSortOrder, diligence int) string {
	if maxSortOrder > 0 && sortOrder > maxSortOrder/2 {
		return "single_dist"
	}
	if diligence >= 4 {
		return "cum_time"
	}
	return "cum_dist"
}

// bestInCategory 在 unlocked 中挑 category 的最高 tier 一筆（同 tier 以 sort_order 較大者勝出，
// 較後面＝較強）；category 傳空字串代表不篩類別、對全體挑選（供 pickDisplayedTitle 的保底分支用）。
// 找不到符合的稱號回 ok=false。
func bestInCategory(unlocked []profile.UnlockedTitle, category string) (code string, ok bool) {
	var bestTier, bestSort int
	for _, t := range unlocked {
		if category != "" && t.Category != category {
			continue
		}
		if !ok || t.Tier > bestTier || (t.Tier == bestTier && t.SortOrder > bestSort) {
			code, bestTier, bestSort, ok = t.Code, t.Tier, t.SortOrder, true
		}
	}
	return code, ok
}

// pickDisplayedTitle 從已解鎖稱號中挑「最符合人設」的一個：偏好類別內 tier 最高者優先；偏好類別
// 未解鎖任何稱號則依序退 cum_dist → single_dist → cum_time 找全體最高 tier；三個類別都槓龜（理論上
// 不會發生——checkin/boss/personal/level/card 至少有一個已解鎖時才會呼叫本函式）則對全體挑最高
// tier 保底，避免回傳空字串。
func pickDisplayedTitle(unlocked []profile.UnlockedTitle, preferred string) string {
	if code, ok := bestInCategory(unlocked, preferred); ok {
		return code
	}
	for _, cat := range fallbackCategoryOrder {
		if cat == preferred {
			continue // 已在上面試過，不重複
		}
		if code, ok := bestInCategory(unlocked, cat); ok {
			return code
		}
	}
	code, _ := bestInCategory(unlocked, "") // 保底：全體最高 tier
	return code
}

// SyncTitles 對單一虛擬選手：呼叫既有稱號引擎解鎖新稱號 → 依規則決定是否要換展示稱號。
//
//   - 沒有新解鎖 且 displayed_title 已非空 → 不動（使用者拍板規則），回 changed=false。
//   - 有新解鎖，或 displayed_title 目前為空（首次回填）→ 依人設規則計算「最符合人設」的稱號，
//     與現值不同才 UPDATE（WHERE is_virtual=TRUE 防呆，永遠不會誤改到真人帳號）。
func SyncTitles(ctx context.Context, db *pgxpool.Pool, userID string) (changed bool, err error) {
	newCodes, err := profile.AwardTitles(ctx, db, userID)
	if err != nil {
		return false, fmt.Errorf("award titles: %w", err)
	}

	var displayedTitle string
	if err := db.QueryRow(ctx, `SELECT displayed_title FROM users WHERE id=$1`, userID).Scan(&displayedTitle); err != nil {
		return false, fmt.Errorf("load displayed title: %w", err)
	}
	if len(newCodes) == 0 && displayedTitle != "" {
		return false, nil
	}

	var level string
	var diligence, sortOrder, maxSortOrder int
	err = db.QueryRow(ctx, `
		SELECT vr.level, vr.diligence, lp.sort_order, (SELECT MAX(sort_order) FROM vr_level_presets)
		FROM virtual_runners vr JOIN vr_level_presets lp ON lp.level = vr.level
		WHERE vr.user_id=$1`, userID).Scan(&level, &diligence, &sortOrder, &maxSortOrder)
	if err != nil {
		return false, fmt.Errorf("load runner persona: %w", err)
	}

	unlocked, err := profile.UnlockedTitles(ctx, db, userID)
	if err != nil {
		return false, fmt.Errorf("load unlocked titles: %w", err)
	}
	if len(unlocked) == 0 {
		return false, nil // 尚未解鎖任何稱號，無從挑選
	}

	best := pickDisplayedTitle(unlocked, preferredCategory(sortOrder, maxSortOrder, diligence))
	if best == "" || best == displayedTitle {
		return false, nil
	}
	if _, err := db.Exec(ctx, `
		UPDATE users SET displayed_title=$1, updated_at=NOW() WHERE id=$2 AND is_virtual=TRUE`,
		best, userID); err != nil {
		return false, fmt.Errorf("update displayed title: %w", err)
	}
	return true, nil
}

// SyncAllEnabledTitles 對所有 enabled 虛擬選手逐一呼叫 SyncTitles，供後台 POST /sync-titles
// 端點使用（見 admin.go SyncTitlesAll）——同時也是「初次回填」入口：既有選手在本功能上線前
// 累積的活動從未被稱號引擎掃過，第一次跑這支就會補上稱號解鎖與展示稱號。
//
// 單一選手失敗只記警告、不中斷整批（比照 generator.go runBatch 對 SyncTitles 的呼叫慣例）。
// synced＝實際嘗試處理的選手數（不含失敗者，失敗者不計入 synced 也不計入 changed），
// changed＝展示稱號有變動的選手數。
// SyncEnabledTitlesBatch 分批同步（offset/limit 以 user_id 穩定排序分頁）。
// 全量 179 位一次跑會超過閘道逾時（每位 4-6 條查詢 × Neon RTT，實測 request failed），
// 由前端逐批呼叫（一批 20）串完整趟。total 供前端顯示進度與判斷是否還有下一批。
func SyncEnabledTitlesBatch(ctx context.Context, db *pgxpool.Pool, offset, limit int) (synced, changed, total int, err error) {
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM virtual_runners WHERE enabled`).Scan(&total); err != nil {
		return 0, 0, 0, fmt.Errorf("count enabled virtual runners: %w", err)
	}
	rows, err := db.Query(ctx, `SELECT user_id::text FROM virtual_runners WHERE enabled ORDER BY user_id OFFSET $1 LIMIT $2`, offset, limit)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("list enabled virtual runners: %w", err)
	}
	var userIDs []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			rows.Close()
			return 0, 0, 0, err
		}
		userIDs = append(userIDs, uid)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, 0, err
	}
	rows.Close()

	for _, uid := range userIDs {
		ch, err := SyncTitles(ctx, db, uid)
		if err != nil {
			log.Warn().Err(err).Str("user_id", uid).Msg("virtual runner sync-titles: sync failed")
			continue
		}
		synced++
		if ch {
			changed++
		}
	}
	return synced, changed, total, nil
}
