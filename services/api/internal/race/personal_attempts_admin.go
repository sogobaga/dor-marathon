// 個人挑戰模式（event_mode=personal）後台唯讀監控：GET /admin/races/:raceID/personal-attempts。
// 列出該賽事「進行中」的挑戰 attempt 即時進度，供客服/營運排查用（例如每日自檢誤報序號庫存吃緊時，
// 快速看到「進行中只有這幾個人、各自進度多少」，見 memory event-schema-round1 / go-live-todos 的
// 每日自檢脈絡）。
//
// ⚠️ 全程只讀：本檔（含 Service.ListActivePersonalAttempts 與其呼叫的所有 repo 方法）只執行 SELECT，
// 絕不呼叫 MarkAttemptCompletedAndGrant／MarkAttemptExpired／GetPersonalProgress 或任何具副作用的
// 函式——後台打開這支列表頁純粹依當下資料庫狀態即時算給人看，不會意外把任何 attempt 標記完成/逾期、
// 觸發發獎或發送 Telegram 通知。與 personal_progress.go 的完成判定引擎完全獨立、互不影響。
//
// 進度語意務必與玩家自己看到的 GET /races/:id/personal-progress（personal_progress.go
// evaluateChallengeRule）完全一致：同一組活動篩選條件（NOT flagged／recorded_at >= challenge_started_at／
// 依 race.external_data 排除 Strava 的來源 gate，見 activity-data-source-gate）、同一 daily_mode
// （cumulative 用 SUM／single 用 MAX）、同一個「達標＝這個數字」定義；差別只在這裡批次算全場（比照
// ops/selfcheck_serial_reward.go personalAchieverCount 的 gaps-and-islands 寫法），玩家端是逐 attempt
// 即時算單一使用者。
package race

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// PersonalAttemptRow 後台「進行中挑戰即時進度」單筆列。三種 completion_type 各自只有對應欄位有意義
// （其餘欄位固定為零值/false），刻意不用 json:",omitempty"——零值本身就是有意義的資料（例如
// 「這個 attempt 完全還沒有任何活動」＝ progress 全部為 0，不應該因為 omitempty 而在 JSON 裡整組消失，
// 這正是 LEFT JOIN 把零進度 attempt 也列出來的意義所在），只有 LastActivityDate（從未有活動＝無日期
// 可談）用 *string＋omitempty 讓「沒有」明確等於 null 而非誤讀成某個日期字串。
type PersonalAttemptRow struct {
	RegistrationID     string    `json:"registration_id"`
	UserID             string    `json:"user_id"`
	UserName           string    `json:"user_name"` // COALESCE(u.name,u.handle)，見 display-name-convention
	UserEmail          string    `json:"user_email"`
	AttemptNo          int       `json:"attempt_no"`
	ChallengeStartedAt time.Time `json:"challenge_started_at"`
	ExpectedEndAt      time.Time `json:"expected_end_at"` // 見 expectedEndAt() 文件
	CompletionType     string    `json:"completion_type"`

	// --- streak_days 專用 ---
	TargetDays     int  `json:"target_days"`
	StreakDays     int  `json:"streak_days"`     // 歷史最長連續達標天數＝玩家端達標判定用的同一個數字（longestConsecutiveRun）
	CurrentStreak  int  `json:"current_streak"`  // 目前仍「活著」的連續天數；斷了就是 0，見 currentStreakLen()
	QualifyingDays int  `json:"qualifying_days"` // 累計達標天數（不要求連續）
	TodayDone      bool `json:"today_done"`      // 今天是否已達當日門檻
	AtRisk         bool `json:"at_risk"`         // 見 attemptAtRisk() 文件

	// EarliestCompleteDate streak_days 專用（其餘型別固定為 nil）：見 earliestCompleteDate() 文件。
	// 用 *string＋omitempty，比照 LastActivityDate——「不適用」明確等於缺席而非某個日期字串。
	EarliestCompleteDate *string `json:"earliest_complete_date,omitempty"`
	// CanFinishBeforeDeadline 見 canFinishBeforeDeadline() 文件：streak_days 看 EarliestCompleteDate
	// 是否不晚於截止日；其餘兩種型別隨時可能一次達標，只看 attempt 本身是否已過期。
	CanFinishBeforeDeadline bool `json:"can_finish_before_deadline"`

	// --- window_cumulative／single_distance 專用 ---
	TargetCumKm    float64 `json:"target_cum_km"`
	CumKm          float64 `json:"cum_km"`
	TargetSingleKm float64 `json:"target_single_km"`
	BestSingleKm   float64 `json:"best_single_km"`

	// --- 三種規則共用 ---
	Percent          int     `json:"percent"`                      // 0-100，見 percentFromRatio/windowCumulativePercent
	LastActivityDate *string `json:"last_activity_date,omitempty"` // Taipei 日期 YYYY-MM-DD；從未有活動則為 null
	TodayKm          float64 `json:"today_km"`                     // Taipei 今日（此刻）累積里程（僅採計 gate 通過來源）
}

// PersonalAttemptsSummary 該賽事報名狀態統計（全量，未經任何篩選）。
type PersonalAttemptsSummary struct {
	InProgress     int `json:"in_progress"`     // status='paid' AND challenge_started_at IS NOT NULL
	Completed      int `json:"completed"`       // status='completed'
	Expired        int `json:"expired"`         // status='expired'
	PendingPayment int `json:"pending_payment"` // status='pending'（尚未付款，還沒起算）
}

// PersonalAttemptsResponse GET .../personal-attempts 回應。
type PersonalAttemptsResponse struct {
	Attempts []PersonalAttemptRow    `json:"attempts"`
	Count    int                     `json:"count"` // len(Attempts)，即進行中筆數（本端點無分頁，一次回全部）
	Summary  PersonalAttemptsSummary `json:"summary"`
}

// percentFromRatio value/target 的百分比，四捨五入取整並夾在 [0,100]（target<=0 防呆回 0，理論上不會發生
// ——ChallengeRule.Validate 已保證各 completion_type 對應的 target 欄位 > 0）。
func percentFromRatio(value, target float64) int {
	if target <= 0 {
		return 0
	}
	p := int(math.Round(value / target * 100))
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	return p
}

// windowCumulativePercent window_cumulative 規則的完成條件是「累積里程達標 AND（若有設單趟門檻）單趟也
// 達標」（見 personal_progress.go evaluateChallengeRule 的 completed 判斷式），兩個條件都要滿足才算
// 100%，故取兩者百分比中較小的一個——只設了累積門檻（targetSingleKm<=0）時單趟這關形同不存在，直接視為
// 100% 不拖累總百分比。
func windowCumulativePercent(cumKm, targetCumKm, bestSingleKm, targetSingleKm float64) int {
	p := percentFromRatio(cumKm, targetCumKm)
	if targetSingleKm > 0 {
		if sp := percentFromRatio(bestSingleKm, targetSingleKm); sp < p {
			p = sp
		}
	}
	return p
}

// expectedEndAt 這次 attempt「實際」到期時間點：完全比照 personal_progress.go GetPersonalProgress
// 判斷 attempt 是否逾期(expired)的同一套規則（差別只在這裡回傳時間點而非布林值）——只有 window_cumulative
// 才有「起算時間 + window_days」這個可能比賽事 end_date 更早的截止點；streak_days／single_distance
// 沒有獨立時窗，到期只看賽事本身的 end_date。
func expectedEndAt(rule *ChallengeRule, startedAt, raceEnd time.Time) time.Time {
	if rule != nil && rule.CompletionType == CompletionWindowCumulative && rule.WindowDays > 0 {
		if windowEnd := startedAt.AddDate(0, 0, rule.WindowDays); windowEnd.Before(raceEnd) {
			return windowEnd
		}
	}
	return raceEnd
}

// currentStreakLen 「目前仍活著」的連續達標天數：只有當「最近一次達標的日曆日」是今天或昨天(Taipei)，
// 這段連續紀錄才算仍在延續中（今天還沒達標很正常——一天還沒過完，只要昨天有達標，連續紀錄就還沒斷）；
// 一旦最近一次達標日是前天以前，代表這段連續紀錄已經斷了，回傳 0——即使歷史上曾經連續很多天（見
// StreakDays 最長紀錄欄位，兩者刻意分開呈現：一個是「現在還在走的連續紀錄」，一個是「這個 attempt
// 期間內曾經達到過的最長連續紀錄」）。lastQualifyingDay/todayTaipei 皆為 SQL 端算好的 Taipei 日曆日
// （避免 Go 端需要 time.LoadLocation——production distroless 映像沒有 tzdata）。
func currentStreakLen(lastQualifyingDay *time.Time, lastSegLen int, todayTaipei time.Time) int {
	if lastQualifyingDay == nil {
		return 0
	}
	gapDays := int(math.Round(todayTaipei.Sub(*lastQualifyingDay).Hours() / 24))
	if gapDays <= 1 { // 0=今天也達標；1=昨天達標、今天還沒
		return lastSegLen
	}
	return 0
}

// attemptAtRisk streak_days 規則的「即將流失」信號，可執行意義：目前這段連續紀錄仍活著
// （current_streak>0，見 currentStreakLen()——換句話說最近一次達標是今天或昨天），但今天(Taipei)截至
// 目前還沒達標；如果今天就這樣結束、沒有補上，這段「還在走」的連續紀錄就會斷掉。這是給後台「還來得及
// 提醒玩家去跑」的旗標，範圍刻意限縮在「有東西可救」的 attempt：
//   - 已經斷掉的紀錄（current_streak==0，不論是从未開始過還是很久以前斷過）不算 at_risk——沒有正在
//     走的連續紀錄可言，提醒「快去跑不然要斷了」沒有意義，這種情況後台另有「已中斷」/「尚未開始」
//     狀態可看（見前端 today 欄位邏輯），不需要 at_risk 重複涵蓋。
//   - 連續紀錄仍活著、且今天已經達標，自然也不是 at_risk（今天不會結束後才斷）。
//
// 這與舊版定義（不分現況是否仍活著，只要昨天+今天都掛零就算風險）不同：新定義只看「現在還在走、
// 今天結束前會不會斷」這個可執行信號，不再把「已經斷過、持續掛零」也算進來（那種情況已經沒有「即將」
// 可言，重新起算即可，不需要後台特別去救）。
func attemptAtRisk(currentStreak int, todayDone bool) bool {
	return currentStreak > 0 && !todayDone
}

// taipeiDateOf 把任意時間點換算成 Taipei 日曆日（UTC 午夜錨定的 time.Time，格式與 SQL DATE 欄位掃描
// 進 Go 的慣例一致，方便與 SQL 算好的 todayDate/lastQualifyingDay 等直接比較），複用 progress.go 的
// taipeiTZ（固定 +8 offset，不依賴容器 tzdata，見該處事故記錄）。
func taipeiDateOf(t time.Time) time.Time {
	tp := t.In(taipeiTZ)
	return time.Date(tp.Year(), tp.Month(), tp.Day(), 0, 0, 0, 0, time.UTC)
}

// earliestCompleteDate streak_days 規則「最快什麼時候可能完成」的推算（Taipei 日曆日）：
//   - 若歷史最長連續（streakDays，即完成判定用的同一個數字）已經達標，代表理論上這個 attempt 應該
//     已經被完成判定引擎標記完成、不該再出現在「進行中」——防禦性回傳 nil（不適用/無意義）。
//   - 今天已達標：還缺 (targetDays-currentStreak) 天，若這幾天都連上，最快就在今天起算第
//     (targetDays-currentStreak) 天後的那天完成（含今天本身已算 1 天）。
//   - 尚未斷、今天還沒達標：現有的 currentStreak 天全部有效，只要接下來每天都達標，還需要
//     (targetDays-currentStreak) 天，但其中今天這天尚未結束、還算數，故再往後推 1 天。
//   - 已經斷掉(currentStreak=0)：等同於明天重新起算的最佳情況，需要 targetDays 整天，今天也還沒結束
//     可能補救（今天達標即等同 currentStreak=1 的情況），故用 targetDays-1。
//
// 三支公式在 currentStreak 跨越邊界時彼此連續（例如 currentStreak 從 0 變 1、today_done 從 false 變
// true），不會有跳號或推算矛盾。
func earliestCompleteDate(streakDays, currentStreak, targetDays int, todayDone bool, todayTaipei time.Time) *time.Time {
	if streakDays >= targetDays {
		return nil
	}
	var daysFromToday int
	switch {
	case todayDone:
		daysFromToday = targetDays - currentStreak
	case currentStreak > 0:
		daysFromToday = targetDays - currentStreak - 1
	default:
		daysFromToday = targetDays - 1
	}
	if daysFromToday < 0 {
		daysFromToday = 0 // 防禦性：依上方不變量理論上不會發生（streakDays>=currentStreak 且已被 guard 排除已達標）
	}
	d := todayTaipei.AddDate(0, 0, daysFromToday)
	return &d
}

// canFinishBeforeDeadline 這個 attempt 是否「來得及在截止日前完成」：
//   - streak_days（earliest 非 nil）：看推算出的最早完成日是否不晚於 expectedEndDate；兩邊呼叫端都
//     傳入 Taipei 日曆日（taipeiDateOf() 截斷），因為 earliestCompleteDate() 本身就是「還需要幾個完整
//     日曆日」的推算，日曆日granularity 是這個數字的定義所在，不是可以消除的誤差。這代表在
//     race.EndDate 當天，若引擎的精確時間點已過（見下方 window_cumulative 說明），這裡仍可能顯示
//     「來得及」，最多落差到 race.EndDate 當天的日曆日結束——這是 streak_days 這個欄位定義下無法避免
//     的日曆日／時間點granularity落差，不是 bug，僅提醒判讀時留意。
//   - window_cumulative／single_distance（earliest 恆為 nil，這兩種規則隨時可能一次活動就達標，沒有
//     「最早完成日」可推算）：呼叫端改傳未截斷的精確時間點（row.ExpectedEndAt／time.Now()），與
//     personal_progress.go 完成判定引擎 `now.After(race.EndDate)` 用同一個時間粒度比較——舊版兩邊都先
//     taipeiDateOf() 截斷成日曆日再比較，會在 race.EndDate 當天造成最多將近 24 小時「後台顯示來得及、
//     引擎其實已判定逾期」的落差（並非原本註解講的「批次執行正常暫時落後」那種毫秒級誤差；且引擎本身
//     是每次 GetPersonalProgress 呼叫時即時判定，不是排程批次），故改成精確時間點比較把這個落差歸零。
func canFinishBeforeDeadline(earliest *time.Time, expectedEndDate, now time.Time) bool {
	if earliest != nil {
		return !earliest.After(expectedEndDate)
	}
	return !now.After(expectedEndDate)
}

// formatTaipeiDate 把 SQL 算好的 Taipei 日曆日（可能為 nil＝這個 attempt 期間從未有任何活動）格式化成
// YYYY-MM-DD 字串指標；純字串格式化，不涉及時區轉換，不需要 tzdata。
func formatTaipeiDate(d *time.Time) *string {
	if d == nil {
		return nil
	}
	s := d.Format("2006-01-02")
	return &s
}

// listStreakAttempts streak_days 規則：批次算出該賽事所有「進行中」attempt 的連續達標天數等進度。
// 寫法比照 ops/selfcheck_serial_reward.go personalAchieverCount 的 gaps-and-islands 技巧（同一段連續
// 日期的 row_number 與日期相減會落在同一個 grp_key），但這裡要保留每個 attempt（不只是全場是否達標的
// 布林），所以额外用 DISTINCT ON 取出「最近一段連續紀錄」（seg_end 最大者）＋窗函數一次取得「歷史最長
// 一段」，避免逐 attempt 查詢再拉回 Go 迴圈算。attempts CTE 起手用 JOIN（非 LEFT JOIN）到 users 只是為了
// 排除虛擬選手，daily 用 JOIN activities 也沒關係——完全沒有任何活動的 attempt 会在 daily/qual 沒有任何
// 列，但最外層仍是從 attempts LEFT JOIN today_calc/streak_agg，保證零活動的 attempt 也會出現在結果裡
// （COALESCE 補零）。
func (r *Repository) listStreakAttempts(ctx context.Context, raceID string, rule *ChallengeRule, externalData bool, raceEnd time.Time) ([]PersonalAttemptRow, error) {
	single := rule.DailyMode == "single"
	rows, err := r.db.Query(ctx, `
		WITH attempts AS (
			SELECT reg.id AS reg_id, reg.user_id, COALESCE(u.name,u.handle) AS user_name, u.email AS user_email,
			       reg.attempt_no, reg.challenge_started_at
			FROM registrations reg
			JOIN users u ON u.id = reg.user_id AND NOT u.is_virtual
			WHERE reg.race_id = $1 AND reg.status = 'paid' AND reg.challenge_started_at IS NOT NULL
		),
		daily AS (
			SELECT at.reg_id, (a.recorded_at AT TIME ZONE 'Asia/Taipei')::date AS d,
			       SUM(a.distance_km) AS sum_km, MAX(a.distance_km) AS max_km
			FROM attempts at
			JOIN activities a ON a.user_id = at.user_id AND NOT a.flagged
			   AND a.recorded_at >= at.challenge_started_at
			   AND (a.source IS NULL OR ($3 AND a.source <> 'strava'))
			GROUP BY at.reg_id, d
		),
		qual AS (
			SELECT reg_id, d, sum_km,
			       (CASE WHEN $2 THEN max_km ELSE sum_km END) >= $4 AS ok
			FROM daily
		),
		today_calc AS (
			SELECT reg_id,
			       COUNT(*) FILTER (WHERE ok) AS qualifying_days,
			       MAX(d) AS last_activity_date,
			       COALESCE(SUM(sum_km) FILTER (WHERE d = (NOW() AT TIME ZONE 'Asia/Taipei')::date), 0) AS today_km,
			       bool_or(ok AND d = (NOW() AT TIME ZONE 'Asia/Taipei')::date) AS today_done
			FROM qual
			GROUP BY reg_id
		),
		seg AS (
			SELECT reg_id, d, d - (ROW_NUMBER() OVER (PARTITION BY reg_id ORDER BY d))::int AS grp
			FROM qual WHERE ok
		),
		streaks AS (
			SELECT reg_id, grp, COUNT(*) AS len, MAX(d) AS seg_end
			FROM seg GROUP BY reg_id, grp
		),
		streak_agg AS (
			SELECT DISTINCT ON (reg_id) reg_id, seg_end AS last_day, len AS last_len,
			       MAX(len) OVER (PARTITION BY reg_id) AS longest
			FROM streaks
			ORDER BY reg_id, seg_end DESC
		)
		SELECT at.reg_id, at.user_id, at.user_name, at.user_email, at.attempt_no, at.challenge_started_at,
		       COALESCE(sa.longest, 0), COALESCE(sa.last_len, 0), sa.last_day,
		       COALESCE(tc.qualifying_days, 0), tc.last_activity_date, COALESCE(tc.today_km, 0),
		       COALESCE(tc.today_done, false),
		       (NOW() AT TIME ZONE 'Asia/Taipei')::date
		FROM attempts at
		LEFT JOIN today_calc tc ON tc.reg_id = at.reg_id
		LEFT JOIN streak_agg sa ON sa.reg_id = at.reg_id`,
		raceID, single, externalData, rule.MinKmPerDay)
	if err != nil {
		return nil, fmt.Errorf("list streak attempts: %w", err)
	}
	defer rows.Close()

	out := []PersonalAttemptRow{}
	for rows.Next() {
		var row PersonalAttemptRow
		var lastDay, lastActivityDate *time.Time
		var lastLen int
		var todayDate time.Time
		if err := rows.Scan(&row.RegistrationID, &row.UserID, &row.UserName, &row.UserEmail, &row.AttemptNo,
			&row.ChallengeStartedAt, &row.StreakDays, &lastLen, &lastDay, &row.QualifyingDays,
			&lastActivityDate, &row.TodayKm, &row.TodayDone, &todayDate); err != nil {
			return nil, err
		}
		row.CompletionType = CompletionStreakDays
		row.TargetDays = rule.Days
		row.CurrentStreak = currentStreakLen(lastDay, lastLen, todayDate)
		row.AtRisk = attemptAtRisk(row.CurrentStreak, row.TodayDone)
		row.Percent = percentFromRatio(float64(row.StreakDays), float64(rule.Days))
		row.ExpectedEndAt = expectedEndAt(rule, row.ChallengeStartedAt, raceEnd)
		earliest := earliestCompleteDate(row.StreakDays, row.CurrentStreak, rule.Days, row.TodayDone, todayDate)
		row.EarliestCompleteDate = formatTaipeiDate(earliest)
		row.CanFinishBeforeDeadline = canFinishBeforeDeadline(earliest, taipeiDateOf(row.ExpectedEndAt), todayDate)
		row.TodayKm = round2(row.TodayKm)
		row.LastActivityDate = formatTaipeiDate(lastActivityDate)
		out = append(out, row)
	}
	return out, rows.Err()
}

// listWindowCumulativeAttempts window_cumulative 規則：批次算出該賽事所有「進行中」attempt 在
// [challenge_started_at, challenge_started_at+window_days) 區間內的累積里程／最長單趟，寫法對照
// personal_progress.go windowAgg，只是這裡一次 GROUP BY 全場而非單一使用者。LEFT JOIN activities
// （非 INNER JOIN）確保完全沒有活動的 attempt 也會保留（COALESCE 補零），不會被吃掉。
func (r *Repository) listWindowCumulativeAttempts(ctx context.Context, raceID string, rule *ChallengeRule, externalData bool, raceEnd time.Time) ([]PersonalAttemptRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT reg.id, reg.user_id, COALESCE(u.name,u.handle), u.email, reg.attempt_no, reg.challenge_started_at,
		       COALESCE(SUM(a.distance_km),0) AS cum_km,
		       COALESCE(MAX(a.distance_km),0) AS best_single_km,
		       MAX((a.recorded_at AT TIME ZONE 'Asia/Taipei')::date) AS last_activity_date,
		       COALESCE(SUM(a.distance_km) FILTER (WHERE (a.recorded_at AT TIME ZONE 'Asia/Taipei')::date = (NOW() AT TIME ZONE 'Asia/Taipei')::date), 0) AS today_km
		FROM registrations reg
		JOIN users u ON u.id = reg.user_id AND NOT u.is_virtual
		LEFT JOIN activities a ON a.user_id = reg.user_id AND NOT a.flagged
		   AND a.recorded_at >= reg.challenge_started_at
		   AND a.recorded_at < reg.challenge_started_at + make_interval(days => $2)
		   AND (a.source IS NULL OR ($3 AND a.source <> 'strava'))
		WHERE reg.race_id = $1 AND reg.status = 'paid' AND reg.challenge_started_at IS NOT NULL
		GROUP BY reg.id, reg.user_id, u.name, u.handle, u.email`,
		raceID, rule.WindowDays, externalData)
	if err != nil {
		return nil, fmt.Errorf("list window_cumulative attempts: %w", err)
	}
	defer rows.Close()

	out := []PersonalAttemptRow{}
	for rows.Next() {
		var row PersonalAttemptRow
		var lastActivityDate *time.Time
		if err := rows.Scan(&row.RegistrationID, &row.UserID, &row.UserName, &row.UserEmail, &row.AttemptNo,
			&row.ChallengeStartedAt, &row.CumKm, &row.BestSingleKm, &lastActivityDate, &row.TodayKm); err != nil {
			return nil, err
		}
		row.CompletionType = CompletionWindowCumulative
		row.TargetCumKm = rule.CumKm
		row.TargetSingleKm = rule.SingleKm
		row.CumKm = round2(row.CumKm)
		row.BestSingleKm = round2(row.BestSingleKm)
		row.TodayKm = round2(row.TodayKm)
		row.Percent = windowCumulativePercent(row.CumKm, rule.CumKm, row.BestSingleKm, rule.SingleKm)
		row.ExpectedEndAt = expectedEndAt(rule, row.ChallengeStartedAt, raceEnd)
		// 精確時間點比較（非日曆日截斷），見 canFinishBeforeDeadline() 文件——跟引擎 personal_progress.go
		// 的 now.After(race.EndDate) 同一個時間粒度。
		row.CanFinishBeforeDeadline = canFinishBeforeDeadline(nil, row.ExpectedEndAt, time.Now())
		row.LastActivityDate = formatTaipeiDate(lastActivityDate)
		out = append(out, row)
	}
	return out, rows.Err()
}

// listSingleDistanceAttempts single_distance 規則：批次算出該賽事所有「進行中」attempt 自
// challenge_started_at 起的最長單趟里程，寫法對照 personal_progress.go maxDistanceSince。
func (r *Repository) listSingleDistanceAttempts(ctx context.Context, raceID string, rule *ChallengeRule, externalData bool, raceEnd time.Time) ([]PersonalAttemptRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT reg.id, reg.user_id, COALESCE(u.name,u.handle), u.email, reg.attempt_no, reg.challenge_started_at,
		       COALESCE(MAX(a.distance_km),0) AS best_single_km,
		       MAX((a.recorded_at AT TIME ZONE 'Asia/Taipei')::date) AS last_activity_date,
		       COALESCE(SUM(a.distance_km) FILTER (WHERE (a.recorded_at AT TIME ZONE 'Asia/Taipei')::date = (NOW() AT TIME ZONE 'Asia/Taipei')::date), 0) AS today_km
		FROM registrations reg
		JOIN users u ON u.id = reg.user_id AND NOT u.is_virtual
		LEFT JOIN activities a ON a.user_id = reg.user_id AND NOT a.flagged
		   AND a.recorded_at >= reg.challenge_started_at
		   AND (a.source IS NULL OR ($2 AND a.source <> 'strava'))
		WHERE reg.race_id = $1 AND reg.status = 'paid' AND reg.challenge_started_at IS NOT NULL
		GROUP BY reg.id, reg.user_id, u.name, u.handle, u.email`,
		raceID, externalData)
	if err != nil {
		return nil, fmt.Errorf("list single_distance attempts: %w", err)
	}
	defer rows.Close()

	out := []PersonalAttemptRow{}
	for rows.Next() {
		var row PersonalAttemptRow
		var lastActivityDate *time.Time
		if err := rows.Scan(&row.RegistrationID, &row.UserID, &row.UserName, &row.UserEmail, &row.AttemptNo,
			&row.ChallengeStartedAt, &row.BestSingleKm, &lastActivityDate, &row.TodayKm); err != nil {
			return nil, err
		}
		row.CompletionType = CompletionSingleDistance
		row.TargetSingleKm = rule.SingleKm
		row.BestSingleKm = round2(row.BestSingleKm)
		row.TodayKm = round2(row.TodayKm)
		row.Percent = percentFromRatio(row.BestSingleKm, rule.SingleKm)
		row.ExpectedEndAt = expectedEndAt(rule, row.ChallengeStartedAt, raceEnd)
		// 精確時間點比較（非日曆日截斷），見 canFinishBeforeDeadline() 文件——跟引擎 personal_progress.go
		// 的 now.After(race.EndDate) 同一個時間粒度。
		row.CanFinishBeforeDeadline = canFinishBeforeDeadline(nil, row.ExpectedEndAt, time.Now())
		row.LastActivityDate = formatTaipeiDate(lastActivityDate)
		out = append(out, row)
	}
	return out, rows.Err()
}

// personalAttemptsSummary 該賽事報名狀態統計（全量，不受任何篩選影響）。虛擬選手一律排除，比照
// DrawRewardWinners／ops/selfcheck_serial_reward.go 的一貫作法。
func (r *Repository) personalAttemptsSummary(ctx context.Context, raceID string) (PersonalAttemptsSummary, error) {
	var s PersonalAttemptsSummary
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE reg.status='paid' AND reg.challenge_started_at IS NOT NULL),
		       COUNT(*) FILTER (WHERE reg.status='completed'),
		       COUNT(*) FILTER (WHERE reg.status='expired'),
		       COUNT(*) FILTER (WHERE reg.status='pending')
		FROM registrations reg
		JOIN users u ON u.id = reg.user_id AND NOT u.is_virtual
		WHERE reg.race_id = $1`, raceID).
		Scan(&s.InProgress, &s.Completed, &s.Expired, &s.PendingPayment)
	if err != nil {
		return s, fmt.Errorf("personal attempts summary: %w", err)
	}
	return s, nil
}

// ListActivePersonalAttempts 個人挑戰模式後台唯讀監控：批次列出該賽事「進行中」attempt 的即時進度＋
// 統計摘要。⚠️ 全程只讀（見檔頭說明）。非 personal 賽事（或 challenge_rule 未設定的資料異常情形）回
// ErrRaceNotFound，比照 ListRewardCompletions 的防呆寫法。
func (s *Service) ListActivePersonalAttempts(ctx context.Context, raceID string) (*PersonalAttemptsResponse, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.EventMode != "personal" || race.ChallengeRule == nil {
		return nil, ErrRaceNotFound
	}

	rule := race.ChallengeRule
	var attempts []PersonalAttemptRow
	switch rule.CompletionType {
	case CompletionStreakDays:
		attempts, err = s.repo.listStreakAttempts(ctx, raceID, rule, race.ExternalData, race.EndDate)
	case CompletionWindowCumulative:
		attempts, err = s.repo.listWindowCumulativeAttempts(ctx, raceID, rule, race.ExternalData, race.EndDate)
	case CompletionSingleDistance:
		attempts, err = s.repo.listSingleDistanceAttempts(ctx, raceID, rule, race.ExternalData, race.EndDate)
	default:
		return nil, fmt.Errorf("invalid challenge_rule completion_type: %s", rule.CompletionType)
	}
	if err != nil {
		return nil, err
	}

	// Order: percent desc, then started asc（見規格；百分比在 Go 端算好，排序也在 Go 端做，不勞煩 SQL）。
	sort.SliceStable(attempts, func(i, j int) bool {
		if attempts[i].Percent != attempts[j].Percent {
			return attempts[i].Percent > attempts[j].Percent
		}
		return attempts[i].ChallengeStartedAt.Before(attempts[j].ChallengeStartedAt)
	})

	summary, err := s.repo.personalAttemptsSummary(ctx, raceID)
	if err != nil {
		return nil, err
	}

	return &PersonalAttemptsResponse{Attempts: attempts, Count: len(attempts), Summary: summary}, nil
}
