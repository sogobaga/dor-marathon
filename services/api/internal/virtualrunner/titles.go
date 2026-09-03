// 虛擬選手稱號同步：套用既有稱號引擎（internal/profile，見 titles_export.go 匯出面）解鎖稱號，
// 並決定是否要換展示稱號（users.displayed_title）。
//
// 2026-09-03 拍板規則（取代 2026-09-01 舊版「只在解鎖到新稱號時才重新評估、且從人設偏好類別挑
// 最高 tier」的規則——當時的顧慮是「機器人沒有主動想換稱號展示的語境」，但實際上線後 179 位虛擬
// 選手的展示稱號因為都吃同一套「等級/勤勞度→偏好類別→最高 tier」決定論，看起來高度雷同、一眼假）：
//  1. 展示稱號從已解鎖稱號中「全隨機」均勻挑選，不分類別、不看人設。
//  2. 重新挑選的時機＝該選手累積跑步趟數（未被標記異常的 activities 筆數）每達到 N 的倍數就重抽
//     一次；N 為系統設定 virtual_title_reroll_every（appsettings，預設 10，後台可調）。
//  3. displayed_title 目前為空（剛回填/首次同步）永遠視為需要挑選，不管趟數是否命中倍數——否則
//     新選手會永遠沒有稱號可展示。
//
// 「累積 >=9 趟的選手要重抽一次」是上線當下的一次性資料修正，已由使用者直接下 SQL 補做，不在此
// 程式碼路徑重複——本檔只負責「往後」的節流重抽規則，以及後台 POST /reroll-titles 供隨時手動
// 重抽（見 admin.go RerollTitles／本檔 RerollTitlesBatch，該端點刻意忽略下面的節流規則）。
package virtualrunner

import (
	"context"
	"fmt"
	"math/rand/v2"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/profile"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// defaultRerollEvery virtual_title_reroll_every 讀不到值、或值不合理（<1，例如誤填 0 或負數）時
// 的退回預設：每累積 10 趟重抽一次。
const defaultRerollEvery = 10

// clampRerollEvery 純函式：every < 1 視為不合理，退回 defaultRerollEvery；否則原樣採用。獨立成
// 純函式方便單元測試，呼叫端（SyncTitles）另外對「有被 clamp 到」的情形記警告，本函式本身不做 log。
func clampRerollEvery(every int) int {
	if every < 1 {
		return defaultRerollEvery
	}
	return every
}

// rerollDue 判斷這次是否要重新挑選展示稱號（見套件檔頭 2026-09-03 規則②③）：
//   - displayedTitle 目前為空 → 一定要挑，回 true（不管 runs/every）。
//   - 否則 runs>0 且 runs 是 every 的倍數（every 先經 clampRerollEvery）才回 true。
//     runs<=0（尚未跑過任何一趟）恆不重抽，避免「0 % every == 0」誤觸發。
func rerollDue(runs, every int, displayedEmpty bool) bool {
	if displayedEmpty {
		return true
	}
	every = clampRerollEvery(every)
	return runs > 0 && runs%every == 0
}

// pickRandomTitle 從已解鎖稱號中「全隨機」均勻挑一個 code；unlocked 為空回空字串。intn 由呼叫端
// 注入隨機性（正式路徑用 math/rand/v2 頂層 IntN——全域來源、自動播種、並發安全；單元測試可注入
// 固定回傳值讓分布可預期），比照套件既有慣例把隨機性外部注入以利測試（見 generator.go
// GenerateActivity 對 *rand.Rand 的類似做法）。
func pickRandomTitle(unlocked []profile.UnlockedTitle, intn func(int) int) string {
	if len(unlocked) == 0 {
		return ""
	}
	return unlocked[intn(len(unlocked))].Code
}

// countRuns 該使用者目前未被標記異常的活動筆數（＝「累積跑步趟數」，供 rerollDue 判斷是否命中
// 重抽節流的倍數）。NOT flagged 排除掉已被後台「回收異常數據」標記的趟次，比照全站其餘統計查詢
// （race_group_standings 重算 SQL／個人進度查詢）一致口徑。
func countRuns(ctx context.Context, db *pgxpool.Pool, userID string) (int, error) {
	var runs int
	if err := db.QueryRow(ctx,
		`SELECT COUNT(*) FROM activities WHERE user_id=$1 AND NOT flagged`, userID).Scan(&runs); err != nil {
		return 0, fmt.Errorf("count runs: %w", err)
	}
	return runs, nil
}

// SyncTitles 對單一虛擬選手：呼叫既有稱號引擎解鎖新稱號 → 依節流規則決定這次是否要重新挑選展示
// 稱號 → 命中的話從已解鎖稱號全隨機挑一個，與現值不同才 UPDATE（WHERE is_virtual=TRUE 防呆，
// 永遠不會誤改到真人帳號）。
func SyncTitles(ctx context.Context, db *pgxpool.Pool, userID string) (changed bool, err error) {
	if _, err := profile.AwardTitles(ctx, db, userID); err != nil {
		return false, fmt.Errorf("award titles: %w", err)
	}

	var displayedTitle string
	if err := db.QueryRow(ctx, `SELECT displayed_title FROM users WHERE id=$1`, userID).Scan(&displayedTitle); err != nil {
		return false, fmt.Errorf("load displayed title: %w", err)
	}

	runs, err := countRuns(ctx, db, userID)
	if err != nil {
		return false, err
	}

	every := appsettings.GetInt(ctx, db, "virtual_title_reroll_every", defaultRerollEvery)
	if every < 1 {
		log.Warn().Int("virtual_title_reroll_every", every).
			Msg("virtual runner sync-titles: reroll-every setting invalid（<1），fallback to default")
	}
	if !rerollDue(runs, every, displayedTitle == "") {
		return false, nil
	}

	unlocked, err := profile.UnlockedTitles(ctx, db, userID)
	if err != nil {
		return false, fmt.Errorf("load unlocked titles: %w", err)
	}
	best := pickRandomTitle(unlocked, rand.IntN)
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
// changed＝展示稱號有變動的選手數——注意這裡「有變動」不等於「整批全部重抽」：SyncTitles 內部
// 仍套用 rerollDue 節流規則，一批呼叫下來只有「displayed_title 剛好為空」或「累積趟數剛好命中
// N 倍數」的選手才會真的重抽/寫入，其餘選手 SyncTitles 會直接回 changed=false（見上）。
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

// RerollTitlesBatch 後台「重抽稱號」明確動作（POST /admin/virtual-runners/reroll-titles，見
// admin.go RerollTitles）：刻意忽略 SyncTitles 的節流規則（rerollDue／每 N 趟才重抽），對候選
// 逐一從已解鎖稱號中隨機重挑一個直接寫入 displayed_title——維運明確要求「現在就重抽」時使用。
//
// 候選＝enabled 虛擬選手且累積跑步趟數（COUNT(*) activities WHERE NOT flagged）>= minRuns，依
// user_id 穩定排序分頁（offset/limit，比照 SyncEnabledTitlesBatch：全量一次跑會超過閘道逾時，
// 由前端分批呼叫）。沒有任何已解鎖稱號的選手跳過並計入 skipped（無從重抽一個不存在的稱號）；
// 單一選手查詢/寫入失敗只記警告、不計入 rerolled 或 skipped、不中斷整批（比照套件其餘 best-effort
// 慣例）。
//
// nextOffset 回傳「這批實際處理筆數」推出的下一批 offset：本批筆數為 0（offset 已超過 total）或
// offset+本批筆數 已達 total 時回 nil（前端據此判斷跑完，不必再比對 rerolled+skipped 是否等於
// 筆數——失敗筆數不計入 rerolled/skipped 但仍算「已處理」，用 rerolled+skipped 推算會少算、可能
// 造成前端誤判成沒跑完而重複打同一批）。
func RerollTitlesBatch(ctx context.Context, db *pgxpool.Pool, minRuns, offset, limit int) (rerolled, skipped, total int, nextOffset *int, err error) {
	const runsSubquery = `(SELECT COUNT(*) FROM activities a WHERE a.user_id = vr.user_id AND NOT a.flagged)`

	if err := db.QueryRow(ctx, `
		SELECT COUNT(*) FROM virtual_runners vr
		WHERE vr.enabled AND `+runsSubquery+` >= $1`, minRuns).Scan(&total); err != nil {
		return 0, 0, 0, nil, fmt.Errorf("count reroll candidates: %w", err)
	}

	rows, err := db.Query(ctx, `
		SELECT vr.user_id::text FROM virtual_runners vr
		WHERE vr.enabled AND `+runsSubquery+` >= $1
		ORDER BY vr.user_id OFFSET $2 LIMIT $3`, minRuns, offset, limit)
	if err != nil {
		return 0, 0, 0, nil, fmt.Errorf("list reroll candidates: %w", err)
	}
	var userIDs []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			rows.Close()
			return 0, 0, 0, nil, err
		}
		userIDs = append(userIDs, uid)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, 0, nil, err
	}
	rows.Close()

	for _, uid := range userIDs {
		unlocked, err := profile.UnlockedTitles(ctx, db, uid)
		if err != nil {
			log.Warn().Err(err).Str("user_id", uid).Msg("virtual runner reroll-titles: load unlocked titles failed")
			continue
		}
		code := pickRandomTitle(unlocked, rand.IntN)
		if code == "" {
			skipped++
			continue
		}
		if _, err := db.Exec(ctx, `
			UPDATE users SET displayed_title=$1, updated_at=NOW() WHERE id=$2 AND is_virtual=TRUE`,
			code, uid); err != nil {
			log.Warn().Err(err).Str("user_id", uid).Msg("virtual runner reroll-titles: update displayed title failed")
			continue
		}
		rerolled++
	}

	if processed := len(userIDs); processed > 0 && offset+processed < total {
		n := offset + processed
		nextOffset = &n
	}
	return rerolled, skipped, total, nextOffset, nil
}
