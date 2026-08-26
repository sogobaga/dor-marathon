// 每日自檢第 9 項：序號獎勵庫存需求預警。
//
// 與既有「序號低庫存告警」(race/personal_progress.go checkAndNotifyLowStock，門檻 available/total<20%)
// 是兩套刻意並存、不合併的獨立信號來源：
//   - checkAndNotifyLowStock 是「事件驅動」：只在真的發出過序號之後才回頭檢查剩餘庫存占比，抓的是
//     「已經在消耗的庫存正在變薄」。
//   - 本檢查是「排程巡檢」：不等任何一次中獎事件發生，主動比較「已接近達標但尚未觸發中獎的人數」與
//     「目前剩餘庫存」，抓的是「還沒發生任何一次中獎、但可預見即將發生的一批中獎會讓庫存不夠發」——例：
//     連續 30 天挑戰，某跑者已連續 24 天（80%），此時可能一枚序號都還沒發出去，
//     checkAndNotifyLowStock 完全不會被觸發，只有本檢查會抓到。
//
// 覆蓋範圍（依各模式進度口徑差異，見 memory event-schema-round1／activity-reward-system 與下方各函式）：
//   - personal 個人挑戰模式：races.reward_config 含 serial 項目者，逐一評估「進行中」attempt
//     （registrations.status='paid' AND challenge_started_at IS NOT NULL）依 challenge_rule 三種
//     completion_type 各自的達標比例，全程純 SQL 批次算完（streak_days 用 gaps-and-islands 技巧一次
//     算出整場所有使用者的最長連續天數，不需要逐 attempt 迴圈查詢）。
//   - 一般/per_group/分組對抗等非 personal 模式的「個人額外挑戰」(race_tasks scope=group_individual)：
//     僅覆蓋 threshold 類指標中「有清楚單一目標值」的四種——cumulative_distance/single_distance/
//     cumulative_ascent/single_ascent（皆可用「累積或最大值 vs target_value」單一比例判定）。
//   - 刻意跳過（並非遺漏）：
//   - checkpoint 類任務：進度是集點而非連續數值，「差多少算 80%」語意不明確（例如 5 選 4 算不算
//     80%？集到哪幾點意義也不同），跳過。
//   - daily_distance/weekly_distance/streak_days（race_tasks 版本，非 personal 的 streak_days）：
//     採「最佳分桶」，同一份活動資料被拿去跟很多天/週分別比較，沒有穩定的「80% 進度」概念（今天
//     達標 80%、明天可能因分母換成新的一週而歸零），跳過。
//   - avg_pace_range/avg_hr_range（range 類）：「落在區間」是布林命中，不是連續數值，沒有 0-100%
//     的進度可言，跳過。
//   - entry_reward_config（參賽虛擬獎勵）：人人有獎、無「進度」概念（開賽即對所有 paid 報名者發放），
//     不適用本檢查的「80% 進度」模型，不覆蓋。
//   - reward_draw.go（賽後手動抽獎）：非序號庫存自動消耗路徑，不覆蓋。
//
// 已知簡化（寧可多告警不漏報，見各函式內註解）：
//   - 不篩 ProbBP<100% 是否「達標＝必中」（達標 ≠ 必中，見 activityreward.RewardItem.ProbBP 註解）——
//     達標人數本來就是「潛在」得主數，不是「確定」得主數，用詞已表明是保守估計。
//   - window_cumulative 規則若同時設有 SingleKm 副條件，本檢查只看 CumKm 這一項的進度比例，不疊加
//     SingleKm 子條件（近似值，避免邏輯過度複雜；CumKm 通常是主要瓶頸）。
//   - 同一賽事若有多個 group_individual 任務共用同一份 race.RewardConfig，本檢查對每個任務的「近達標
//     人數」各自獨立跟同一份庫存比較，不加總跨任務同時達標的疊加需求（同一時刻多個任務一起被推爆的
//     機率遠低於單一任務被推爆，這裡取捨為「每任務獨立示警」而非窮舉組合，保持查詢與訊息單純）。
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/activityreward"
	"github.com/dor/api/internal/race"
)

const (
	// serialShortageProgressRatio 「潛在得主」的達標門檻：進度 >= 80%。
	serialShortageProgressRatio = 0.8

	// serialShortageRaceLimit 單次自檢最多掃描的候選賽事數（personal / 非 personal 各自的上限，防禦性
	// 上限，比照 entry_reward_schedule.go entryRewardCandidateRaceLimit 的設計取捨：正常情境「進行中且
	// 設定了序號類獎勵」的賽事數遠小於此值）。
	serialShortageRaceLimit = 100
)

// serialShortageTaskMetrics 本檢查覆蓋的 race_tasks threshold 類指標（見檔頭「覆蓋範圍」說明，
// checkpoint/range/bucket 類皆不在其中）。
var serialShortageTaskMetrics = []string{"cumulative_distance", "single_distance", "cumulative_ascent", "single_ascent"}

// serialShortageThreshold 依 80% 進度門檻換算成目標值的門檻。這是「SQL 邏輯鏡射成純函式供單測」的慣例
// （比照 entry_reward_schedule.go isEntryRewardWindowActive）：整除天數（如 30 天×0.8=24 天，剛好達標）
// 與非整除天數（如 23 天×0.8=18.4 天，需 19 天才算達標——SQL 端直接用浮點數比較
// `longest::float8 >= threshold`，不額外四捨五入）兩種邊界都要能離線驗證，見 selfcheck_serial_reward_test.go。
func serialShortageThreshold(target float64) float64 {
	return target * serialShortageProgressRatio
}

// meetsShortageThreshold current 是否達到 target 的 80% 進度（target<=0 視為未設定門檻，一律回 false，
// 避免除以零）。正式查詢的比較都在 SQL 端完成（見 personalAchieverCount／taskAchieverCount），這裡是同一
// 條算式的純 Go 鏡射版本，供單元測試直接驗證 0.80 邊界（含整除/非整除）行為與 SQL 端保持一致。
func meetsShortageThreshold(current, target float64) bool {
	if target <= 0 {
		return false
	}
	return current >= serialShortageThreshold(target)
}

// serialGroupStock 單一序號組的庫存快照（供 capacity 估算純函式使用）。
type serialGroupStock struct {
	GroupID      string
	Name         string
	MerchantName string
	Available    int
	GrantCount   int
}

// remainingCapacity 序號組「大約還能配發給幾人」＝ available／grant_count（grant_count<1 視為 1，防禦
// 資料異常；理論上 NOT NULL DEFAULT 1，見 migration 126）。整數除法無條件捨去——比照
// activityreward.claimSerialsFromGroup「庫存中途不足即跳過」的保守精神，寧可低估可服務人數（多告警）
// 也不要高估（漏告警）。
func (s serialGroupStock) remainingCapacity() int {
	gc := s.GrantCount
	if gc < 1 {
		gc = 1
	}
	return s.Available / gc
}

// totalCapacity 一個 serial 獎勵項目底下所有候選面額（denominations）合計可服務人數——依
// activityreward.grantSerialTwoLayer 的實際行為，某面額中途缺貨會被移出加權池、改由其餘有庫存面額
// 續發，因此「這個項目總共還能服務幾人」＝各面額 remainingCapacity 之和，直到全數面額皆缺貨為止。
func totalCapacity(groups []serialGroupStock) int {
	total := 0
	for _, g := range groups {
		total += g.remainingCapacity()
	}
	return total
}

// serialItemsOf 篩出 cfg 中「當下有機會中獎」的 serial 項目：type=serial、ProbBP>0（0 機率永不中，見
// activityreward.rollHit）、且至少有一個有效面額（ValidDenominations 已內含舊格式 SerialGroupID 的向後
// 相容回退，見 activityreward/model.go）。不限定 LINE POINTS 或任何特定字樣——所有序號類獎勵一體適用。
func serialItemsOf(cfg *activityreward.RewardConfig) []activityreward.RewardItem {
	if cfg == nil {
		return nil
	}
	var out []activityreward.RewardItem
	for _, it := range cfg.Items {
		if it.Type == "serial" && it.ProbBP > 0 && len(it.ValidDenominations()) > 0 {
			out = append(out, it)
		}
	}
	return out
}

// checkSerialRewardStockPressure 第 9 項自檢：見檔頭說明。SQL 全唯讀、容忍 0 列；單一賽事/任務資料異常
// （如 challenge_rule/reward_config JSON 解析失敗）只記警告並跳過該筆，不影響其餘賽事的檢查結果——只有
// 查詢本身出錯（DB 連線/SQL 錯誤）才會讓整項檢查回報失敗（與 runChecks 對「單項檢查失敗視為異常落地」
// 的既有慣例一致）。
func (h *Handler) checkSerialRewardStockPressure(ctx context.Context) (bool, string, error) {
	var lines []string

	personalLines, err := h.serialShortagePersonalLines(ctx)
	if err != nil {
		return false, "", fmt.Errorf("personal: %w", err)
	}
	lines = append(lines, personalLines...)

	taskLines, err := h.serialShortageGroupIndividualLines(ctx)
	if err != nil {
		return false, "", fmt.Errorf("group_individual: %w", err)
	}
	lines = append(lines, taskLines...)

	if len(lines) == 0 {
		return true, "", nil
	}
	if len(lines) > sampleLimit {
		extra := len(lines) - sampleLimit
		lines = append(lines[:sampleLimit], fmt.Sprintf("……其餘 %d 項省略", extra))
	}
	return false, strings.Join(lines, "\n"), nil
}

// --- personal 個人挑戰模式 ---

// personalSerialCandidate 一場候選 personal 賽事（已設定 reward_config 與 challenge_rule、仍在進行中）。
type personalSerialCandidate struct {
	id, title    string
	ruleBytes    []byte
	cfgBytes     []byte
	externalData bool
}

// serialShortagePersonalLines 掃描所有仍在進行中、設定了序號類即時獎勵的 personal 賽事，逐場計算「進度
// >=80% 的進行中 attempt 數」並與序號庫存比較。
func (h *Handler) serialShortagePersonalLines(ctx context.Context) ([]string, error) {
	rows, err := h.db.Query(ctx, `
		SELECT id::text, title, challenge_rule, reward_config, external_data
		FROM races
		WHERE event_mode='personal' AND review_status='approved'
		  AND challenge_rule IS NOT NULL AND reward_config IS NOT NULL
		  AND end_date > NOW()
		ORDER BY end_date
		LIMIT $1`, serialShortageRaceLimit)
	if err != nil {
		return nil, fmt.Errorf("query personal candidate races: %w", err)
	}
	var cands []personalSerialCandidate
	for rows.Next() {
		var c personalSerialCandidate
		if err := rows.Scan(&c.id, &c.title, &c.ruleBytes, &c.cfgBytes, &c.externalData); err != nil {
			rows.Close()
			return nil, err
		}
		cands = append(cands, c)
	}
	scanErr := rows.Err()
	rows.Close()
	if scanErr != nil {
		return nil, scanErr
	}

	var out []string
	for _, c := range cands {
		var rule race.ChallengeRule
		if err := json.Unmarshal(c.ruleBytes, &rule); err != nil {
			log.Warn().Err(err).Str("race_id", c.id).Msg("selfcheck serial shortage: parse challenge_rule failed")
			continue
		}
		var cfg activityreward.RewardConfig
		if err := json.Unmarshal(c.cfgBytes, &cfg); err != nil {
			log.Warn().Err(err).Str("race_id", c.id).Msg("selfcheck serial shortage: parse reward_config failed")
			continue
		}
		items := serialItemsOf(&cfg)
		if len(items) == 0 {
			continue
		}
		population, err := h.personalAchieverCount(ctx, c.id, &rule, c.externalData)
		if err != nil {
			log.Warn().Err(err).Str("race_id", c.id).Msg("selfcheck serial shortage: compute personal achiever count failed")
			continue
		}
		if population <= 0 {
			continue
		}
		label := fmt.Sprintf("賽事「%s」個人挑戰", c.title)
		lines, err := h.shortageLinesForItems(ctx, label, population, items)
		if err != nil {
			log.Warn().Err(err).Str("race_id", c.id).Msg("selfcheck serial shortage: stock lookup failed")
			continue
		}
		out = append(out, lines...)
	}
	return out, nil
}

// personalAchieverCount 依 rule.CompletionType 批次算出「這場賽事目前進行中、進度已達 80% 的 attempt
// 數」。三種規則皆為純 SQL 一次算完整場（不逐 attempt 查詢）：
//   - window_cumulative／single_distance：GROUP BY user_id 累加/取最大即可，直接用 HAVING 篩門檻。
//   - streak_days：用「gaps-and-islands」技巧——每位使用者依日期排序後的 row_number 與日期相減，
//     連續日期會落在同一個 grp_key，GROUP BY (user_id, grp_key) 取各段長度、再取每人最長一段——單一
//     查詢即可算出全場每個人的最長連續天數，不需要比照 race/personal_progress.go
//     streakQualifyingDays／longestConsecutiveRun 逐 attempt 查詢再拉回 Go 迴圈。
func (h *Handler) personalAchieverCount(ctx context.Context, raceID string, rule *race.ChallengeRule, externalData bool) (int, error) {
	switch rule.CompletionType {
	case race.CompletionWindowCumulative:
		if rule.CumKm <= 0 || rule.WindowDays <= 0 {
			return 0, nil
		}
		threshold := serialShortageThreshold(rule.CumKm)
		var n int
		err := h.db.QueryRow(ctx, `
			SELECT COUNT(*) FROM (
				SELECT reg.user_id
				FROM registrations reg
				JOIN activities a ON a.user_id = reg.user_id AND NOT a.flagged
				   AND a.recorded_at >= reg.challenge_started_at
				   AND a.recorded_at < reg.challenge_started_at + make_interval(days => $2)
				   AND (a.source IS NULL OR ($3 AND a.source <> 'strava'))
				WHERE reg.race_id = $1 AND reg.status = 'paid' AND reg.challenge_started_at IS NOT NULL
				  AND NOW() < reg.challenge_started_at + make_interval(days => $2)
				GROUP BY reg.user_id
				HAVING COALESCE(SUM(a.distance_km),0) >= $4
			) t`, raceID, rule.WindowDays, externalData, threshold).Scan(&n)
		if err != nil {
			return 0, fmt.Errorf("window_cumulative achiever count: %w", err)
		}
		return n, nil

	case race.CompletionSingleDistance:
		if rule.SingleKm <= 0 {
			return 0, nil
		}
		threshold := serialShortageThreshold(rule.SingleKm)
		var n int
		err := h.db.QueryRow(ctx, `
			SELECT COUNT(*) FROM (
				SELECT reg.user_id
				FROM registrations reg
				JOIN activities a ON a.user_id = reg.user_id AND NOT a.flagged
				   AND a.recorded_at >= reg.challenge_started_at
				   AND (a.source IS NULL OR ($2 AND a.source <> 'strava'))
				WHERE reg.race_id = $1 AND reg.status = 'paid' AND reg.challenge_started_at IS NOT NULL
				GROUP BY reg.user_id
				HAVING COALESCE(MAX(a.distance_km),0) >= $3
			) t`, raceID, externalData, threshold).Scan(&n)
		if err != nil {
			return 0, fmt.Errorf("single_distance achiever count: %w", err)
		}
		return n, nil

	case race.CompletionStreakDays:
		if rule.Days <= 0 || rule.MinKmPerDay <= 0 {
			return 0, nil
		}
		// agg 為受控字面值（SUM/MAX），非使用者輸入，無 SQL 注入風險——比照
		// race/personal_progress.go streakQualifyingDays 同款寫法。
		agg := "SUM"
		if rule.DailyMode == "single" {
			agg = "MAX"
		}
		threshold := serialShortageThreshold(float64(rule.Days))
		q := fmt.Sprintf(`
			WITH daily AS (
				SELECT reg.user_id AS user_id, (a.recorded_at AT TIME ZONE 'Asia/Taipei')::date AS d
				FROM registrations reg
				JOIN activities a ON a.user_id = reg.user_id AND NOT a.flagged
				   AND a.recorded_at >= reg.challenge_started_at
				   AND (a.source IS NULL OR ($3 AND a.source <> 'strava'))
				WHERE reg.race_id = $1 AND reg.status = 'paid' AND reg.challenge_started_at IS NOT NULL
				GROUP BY reg.user_id, d
				HAVING %s(a.distance_km) >= $2
			), grp AS (
				SELECT user_id, d - (ROW_NUMBER() OVER (PARTITION BY user_id ORDER BY d))::int AS grp_key
				FROM daily
			), streaks AS (
				SELECT user_id, COUNT(*) AS len FROM grp GROUP BY user_id, grp_key
			)
			SELECT COUNT(*) FROM (
				SELECT user_id, MAX(len) AS longest FROM streaks GROUP BY user_id
			) t WHERE longest >= $4`, agg)
		var n int
		if err := h.db.QueryRow(ctx, q, raceID, rule.MinKmPerDay, externalData, threshold).Scan(&n); err != nil {
			return 0, fmt.Errorf("streak_days achiever count: %w", err)
		}
		return n, nil
	}
	return 0, nil
}

// --- 非 personal 模式：group_individual 個人額外挑戰任務 ---

// taskRaceCandidate 一場候選非 personal 賽事（已設定 reward_config、仍在進行中）。
type taskRaceCandidate struct {
	id, title    string
	cfgBytes     []byte
	externalData bool
	startDate    time.Time
	endDate      time.Time
}

// shortageTask 一個候選 group_individual threshold 任務。
type shortageTask struct {
	id, title, groupID, metricType string
	targetValue                    float64
}

// serialShortageGroupIndividualLines 掃描所有仍在進行中、設定了序號類即時獎勵的非 personal 賽事，
// 對每場賽事底下每個 group_individual threshold 任務各自計算「進度 >=80% 且尚未觸發過該任務獎勵的
// 報名者數」並與序號庫存比較（見檔頭「已知簡化」：多任務各自獨立示警，不加總跨任務需求）。
func (h *Handler) serialShortageGroupIndividualLines(ctx context.Context) ([]string, error) {
	rows, err := h.db.Query(ctx, `
		SELECT id::text, title, reward_config, external_data, start_date, end_date
		FROM races
		WHERE event_mode <> 'personal' AND review_status='approved'
		  AND reward_config IS NOT NULL AND end_date > NOW()
		ORDER BY end_date
		LIMIT $1`, serialShortageRaceLimit)
	if err != nil {
		return nil, fmt.Errorf("query group_individual candidate races: %w", err)
	}
	var cands []taskRaceCandidate
	for rows.Next() {
		var c taskRaceCandidate
		if err := rows.Scan(&c.id, &c.title, &c.cfgBytes, &c.externalData, &c.startDate, &c.endDate); err != nil {
			rows.Close()
			return nil, err
		}
		cands = append(cands, c)
	}
	scanErr := rows.Err()
	rows.Close()
	if scanErr != nil {
		return nil, scanErr
	}

	var out []string
	for _, c := range cands {
		var cfg activityreward.RewardConfig
		if err := json.Unmarshal(c.cfgBytes, &cfg); err != nil {
			log.Warn().Err(err).Str("race_id", c.id).Msg("selfcheck serial shortage: parse reward_config failed")
			continue
		}
		items := serialItemsOf(&cfg)
		if len(items) == 0 {
			continue
		}
		tasks, err := h.groupIndividualThresholdTasks(ctx, c.id)
		if err != nil {
			log.Warn().Err(err).Str("race_id", c.id).Msg("selfcheck serial shortage: load race_tasks failed")
			continue
		}
		for _, t := range tasks {
			population, err := h.taskAchieverCount(ctx, c.id, t, c.startDate, c.endDate, c.externalData)
			if err != nil {
				log.Warn().Err(err).Str("race_id", c.id).Str("task_id", t.id).
					Msg("selfcheck serial shortage: compute task achiever count failed")
				continue
			}
			if population <= 0 {
				continue
			}
			label := fmt.Sprintf("賽事「%s」任務「%s」", c.title, t.title)
			lines, err := h.shortageLinesForItems(ctx, label, population, items)
			if err != nil {
				log.Warn().Err(err).Str("race_id", c.id).Str("task_id", t.id).
					Msg("selfcheck serial shortage: stock lookup failed")
				continue
			}
			out = append(out, lines...)
		}
	}
	return out, nil
}

// groupIndividualThresholdTasks 撈某賽事底下「scope=group_individual 且指標屬於本檢查覆蓋範圍
// （serialShortageTaskMetrics）」的任務。
func (h *Handler) groupIndividualThresholdTasks(ctx context.Context, raceID string) ([]shortageTask, error) {
	rows, err := h.db.Query(ctx, `
		SELECT id::text, title, COALESCE(group_id::text,''), metric_type, target_value
		FROM race_tasks
		WHERE race_id = $1 AND scope = $2 AND metric_type = ANY($3::text[])
		  AND target_value IS NOT NULL AND target_value > 0`,
		raceID, race.ScopeGroupIndividual, serialShortageTaskMetrics)
	if err != nil {
		return nil, fmt.Errorf("query group_individual threshold tasks: %w", err)
	}
	var out []shortageTask
	for rows.Next() {
		var t shortageTask
		if err := rows.Scan(&t.id, &t.title, &t.groupID, &t.metricType, &t.targetValue); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, t)
	}
	scanErr := rows.Err()
	rows.Close()
	if scanErr != nil {
		return nil, scanErr
	}
	return out, nil
}

// taskAchieverCount 算出「這個 group_individual 任務目前進度已達 80%、且尚未觸發過此任務即時獎勵
// （race_task_completions 無紀錄）的報名者數」。population 鏡射 race/progress.go 的 metricValue／`mine`
// 集合邏輯：同賽事、同分組（若任務綁定特定分組）、未取消報名者，各自累加/取最大其活動期間內的
// distance_km／ascent_m。
func (h *Handler) taskAchieverCount(ctx context.Context, raceID string, t shortageTask, startDate, endDate time.Time, externalData bool) (int, error) {
	var col, agg string
	switch t.metricType {
	case "cumulative_distance":
		col, agg = "distance_km", "SUM"
	case "single_distance":
		col, agg = "distance_km", "MAX"
	case "cumulative_ascent":
		col, agg = "ascent_m", "SUM"
	case "single_ascent":
		col, agg = "ascent_m", "MAX"
	default:
		return 0, nil // 理論上不會發生：groupIndividualThresholdTasks 已用 serialShortageTaskMetrics 篩過
	}
	threshold := serialShortageThreshold(t.targetValue)
	// col/agg 為受控字面值（上面 switch 的固定四選一），非使用者輸入，無 SQL 注入風險。
	q := fmt.Sprintf(`
		SELECT COUNT(*) FROM (
			SELECT reg.user_id
			FROM registrations reg
			JOIN activities a ON a.user_id = reg.user_id AND NOT a.flagged
			   AND a.recorded_at >= $2 AND a.recorded_at <= $3
			   AND (a.source IS NULL OR ($4 AND a.source <> 'strava'))
			WHERE reg.race_id = $1 AND reg.status <> 'cancelled'
			  AND ($5 = '' OR reg.group_id::text = $5)
			  AND NOT EXISTS (
			      SELECT 1 FROM race_task_completions rtc
			      WHERE rtc.task_id = $6 AND rtc.user_id = reg.user_id)
			GROUP BY reg.user_id
			HAVING COALESCE(%s(COALESCE(a.%s,0)),0) >= $7
		) t`, agg, col)
	var n int
	if err := h.db.QueryRow(ctx, q, raceID, startDate, endDate, externalData, t.groupID, t.id, threshold).Scan(&n); err != nil {
		return 0, fmt.Errorf("task achiever count (%s): %w", t.metricType, err)
	}
	return n, nil
}

// --- 共用：庫存 vs 潛在得主數比較 ---

// shortageLinesForItems 對一組「同一觸發事件會一起 roll」的 serial 獎勵項目，逐項比較 population（潛在
// 得主數）與該項目底下所有候選面額的合計庫存（totalCapacity），庫存不足才產生一行 detail。
func (h *Handler) shortageLinesForItems(ctx context.Context, label string, population int, items []activityreward.RewardItem) ([]string, error) {
	groupIDSet := map[string]bool{}
	for _, it := range items {
		for _, d := range it.ValidDenominations() {
			groupIDSet[d.GroupID] = true
		}
	}
	if len(groupIDSet) == 0 {
		return nil, nil
	}
	groupIDs := make([]string, 0, len(groupIDSet))
	for id := range groupIDSet {
		groupIDs = append(groupIDs, id)
	}
	stock, err := h.serialGroupStocks(ctx, groupIDs)
	if err != nil {
		return nil, err
	}

	var lines []string
	for _, it := range items {
		var groups []serialGroupStock
		var parts []string
		for _, d := range it.ValidDenominations() {
			g, ok := stock[d.GroupID]
			if !ok {
				continue // 面額指向的序號組查無資料（如已被刪除）：跳過此面額，不誤報也不硬湊資料
			}
			groups = append(groups, g)
			groupLabel := g.Name
			if g.MerchantName != "" {
				groupLabel = g.MerchantName + "/" + g.Name
			}
			parts = append(parts, fmt.Sprintf("%s(剩%d，每中1次發%d枚)", groupLabel, g.Available, max(g.GrantCount, 1)))
		}
		if len(groups) == 0 {
			continue
		}
		capacity := totalCapacity(groups)
		if capacity >= population {
			continue
		}
		lines = append(lines, fmt.Sprintf(
			"%s：達標80%%潛在得主約 %d 人，序號獎勵剩餘僅約可再發 %d 人份（缺口 %d 人）— %s",
			label, population, capacity, population-capacity, strings.Join(parts, "、")))
	}
	return lines, nil
}

// serialGroupStocks 批次查一批序號組的庫存快照（名稱/商家/每次配發枚數/目前可發數）。
func (h *Handler) serialGroupStocks(ctx context.Context, groupIDs []string) (map[string]serialGroupStock, error) {
	rows, err := h.db.Query(ctx, `
		SELECT g.id::text, COALESCE(g.name,''), COALESCE(m.name,''), g.grant_count,
		       COUNT(s.id) FILTER (WHERE s.status='available')
		FROM reward_serial_groups g
		LEFT JOIN reward_merchants m ON m.id = g.merchant_id
		LEFT JOIN reward_serials s ON s.group_id = g.id
		WHERE g.id = ANY($1::uuid[])
		GROUP BY g.id, g.name, m.name, g.grant_count`, groupIDs)
	if err != nil {
		return nil, fmt.Errorf("load serial group stocks: %w", err)
	}
	defer rows.Close()
	out := map[string]serialGroupStock{}
	for rows.Next() {
		var s serialGroupStock
		if err := rows.Scan(&s.GroupID, &s.Name, &s.MerchantName, &s.GrantCount, &s.Available); err != nil {
			return nil, err
		}
		out[s.GroupID] = s
	}
	return out, rows.Err()
}
