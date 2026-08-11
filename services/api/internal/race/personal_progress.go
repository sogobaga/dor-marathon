// 個人挑戰模式（event_mode=personal）P3：完成判定引擎。
// 三種規則(streak_days/window_cumulative/single_distance)即時評估 + CAS 完成/逾期標記。
// 觸發點：GET /races/{id}/personal-progress（開個人賽事詳情頁即打，見前端 RaceDetailScreen）。
// 只查/標記呼叫者自己「進行中」的 attempt，範圍有界、不掃全體，不碰集體賽事管線
// （race/progress.go、leaderboard.go、settlement.go 完全不受影響）。詳見設計 memory personal-challenge-mode。
package race

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/activityreward"
)

// ChallengeProgress 個人挑戰進行中 attempt 的即時進度（依 rule.CompletionType 只填相關欄位）。
type ChallengeProgress struct {
	CompletionType string `json:"completion_type"`
	// streak_days
	StreakDays int `json:"streak_days,omitempty"` // 目前最長連續達標天數
	TargetDays int `json:"target_days,omitempty"`
	// window_cumulative（CumKm/TargetCumKm）＋ window_cumulative/single_distance 共用（BestSingleKm/TargetSingleKm）
	CumKm          float64    `json:"cum_km,omitempty"`
	TargetCumKm    float64    `json:"target_cum_km,omitempty"`
	BestSingleKm   float64    `json:"best_single_km,omitempty"`
	TargetSingleKm float64    `json:"target_single_km,omitempty"`
	WindowEndsAt   *time.Time `json:"window_ends_at,omitempty"` // window_cumulative 專用：挑戰起算 + window_days
}

// PersonalProgress GET /races/{id}/personal-progress 回應。
type PersonalProgress struct {
	HasAttempt     bool                           `json:"has_attempt"`
	Status         string                         `json:"status,omitempty"` // pending|paid|completed|expired（has_attempt=true 才有意義）
	Rule           *ChallengeRule                 `json:"rule,omitempty"`
	Progress       *ChallengeProgress             `json:"progress,omitempty"`
	CompletedCount int                            `json:"completed_count"`         // 此使用者在此賽事已完成的挑戰次數
	NewlyGranted   []activityreward.GrantedReward `json:"newly_granted,omitempty"` // 本次呼叫剛觸發完成時發放的即時獎勵（活動獎勵系統 P2；P3 前端彈窗才用）
}

// personalAttempt 使用者在此賽事「進行中」(pending/paid未完成) 的 attempt 精簡欄位。
// 同一使用者同一賽事至多一筆進行中（唯一索引 uq_registrations_active_user_race 保證，見 migration 124）。
type personalAttempt struct {
	ID                 string
	Status             string
	ChallengeStartedAt *time.Time // 一般走金流的報名在 pending 階段為 NULL，付款成功才由 MarkOrderPaid 設定
}

// FindActivePersonalAttempt 查詢使用者在此賽事「進行中」的挑戰 attempt。
func (r *Repository) FindActivePersonalAttempt(ctx context.Context, userID, raceID string) (*personalAttempt, error) {
	a := &personalAttempt{}
	err := r.db.QueryRow(ctx, `
		SELECT id, status, challenge_started_at
		FROM registrations
		WHERE user_id=$1 AND race_id=$2 AND status IN ('pending','paid')`,
		userID, raceID).Scan(&a.ID, &a.Status, &a.ChallengeStartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find active personal attempt: %w", err)
	}
	return a, nil
}

// CompletedChallengeCount 使用者在此賽事已完成的挑戰次數（前台「已完成 N 次」／未來 P4 排行榜聚合基礎）。
func (r *Repository) CompletedChallengeCount(ctx context.Context, userID, raceID string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM registrations WHERE user_id=$1 AND race_id=$2 AND status='completed'`,
		userID, raceID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("completed challenge count: %w", err)
	}
	return n, nil
}

// streakQualifyingDays 回傳 since 起、依台灣日曆日「當日總里程 >= minKmPerDay」的日期清單（升冪）。
// 台灣日曆日換算在 SQL 端做（AT TIME ZONE 'Asia/Taipei'，比照 titles.go/achievements.go 慣例，
// Go 端不用 time.LoadLocation——production distroless 映像沒有 tzdata）。一律 NOT flagged 防弊。
func (r *Repository) streakQualifyingDays(ctx context.Context, userID string, since time.Time, minKmPerDay float64, externalData bool) ([]time.Time, error) {
	rows, err := r.db.Query(ctx, `
		SELECT (recorded_at AT TIME ZONE 'Asia/Taipei')::date AS d
		FROM activities
		WHERE user_id=$1 AND NOT flagged AND recorded_at >= $2 AND ($4 OR source IS NULL)
		GROUP BY d
		HAVING SUM(distance_km) >= $3
		ORDER BY d`, userID, since, minKmPerDay, externalData)
	if err != nil {
		return nil, fmt.Errorf("streak qualifying days: %w", err)
	}
	defer rows.Close()
	var days []time.Time
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		days = append(days, d)
	}
	return days, rows.Err()
}

// windowAgg [from, to) 區間內未 flagged 活動的累積里程與最長單趟（window_cumulative 規則用）。
func (r *Repository) windowAgg(ctx context.Context, userID string, from, to time.Time, externalData bool) (sumKm, maxKm float64, err error) {
	err = r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(distance_km),0), COALESCE(MAX(distance_km),0)
		FROM activities
		WHERE user_id=$1 AND NOT flagged AND recorded_at >= $2 AND recorded_at < $3 AND ($4 OR source IS NULL)`,
		userID, from, to, externalData).Scan(&sumKm, &maxKm)
	if err != nil {
		return 0, 0, fmt.Errorf("window agg: %w", err)
	}
	return sumKm, maxKm, nil
}

// maxDistanceSince since 起未 flagged 活動的最長單趟里程（single_distance 規則用）。
func (r *Repository) maxDistanceSince(ctx context.Context, userID string, since time.Time, externalData bool) (float64, error) {
	var m float64
	err := r.db.QueryRow(ctx,
		`SELECT COALESCE(MAX(distance_km),0) FROM activities WHERE user_id=$1 AND NOT flagged AND recorded_at >= $2 AND ($3 OR source IS NULL)`,
		userID, since, externalData).Scan(&m)
	if err != nil {
		return 0, fmt.Errorf("max distance since: %w", err)
	}
	return m, nil
}

// MarkAttemptCompletedAndGrant CAS 標記個人挑戰 attempt 完成：搶鎖式 UPDATE，只有 status='paid' 且
// completed_at IS NULL 才會更新（抄 event_race_goal.go 的達標搶鎖式樣）。冪等：對已完成/非 paid 的
// attempt 重複呼叫不會出錯，只是 claimed=false（沒搶到，代表已經被標記過或狀態不符）。
//
// 活動獎勵系統 P2：在同一交易內原子地把「判定完成」與「依賽事 reward_config 逐項獨立機率 roll 發放
// 即時獎勵」綁在一起（見套件 activityreward.RollAndGrant）——只有 claimed=true（本次真的搶到完成權）
// 才會查 reward_config、才會 roll；claimed=false 時直接跳過，不重複發獎。任一步失敗，交易整個
// Rollback，「完成」與「發獎」保證同進退，不會出現「標完成了但獎沒發」或「發了獎但沒標完成」的半成功。
func (r *Repository) MarkAttemptCompletedAndGrant(ctx context.Context, regID string) (claimed bool, granted []activityreward.GrantedReward, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // Commit 後為 no-op

	tag, err := tx.Exec(ctx, `
		UPDATE registrations SET status='completed', completed_at=NOW()
		WHERE id=$1 AND status='paid' AND completed_at IS NULL`, regID)
	if err != nil {
		return false, nil, fmt.Errorf("mark attempt completed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil, nil // 沒搶到（已完成過/狀態不符）：no-op，交易靠 defer Rollback 收尾（反正沒有異動）
	}

	var userID, raceID string
	var cfgBytes []byte
	if err := tx.QueryRow(ctx, `
		SELECT reg.user_id, reg.race_id, rc.reward_config
		FROM registrations reg JOIN races rc ON rc.id = reg.race_id
		WHERE reg.id=$1`, regID).Scan(&userID, &raceID, &cfgBytes); err != nil {
		return false, nil, fmt.Errorf("load reward config: %w", err)
	}

	cfg, err := bytesToRewardConfig(cfgBytes)
	if err != nil {
		return false, nil, fmt.Errorf("parse reward_config: %w", err)
	}
	if cfg != nil && len(cfg.Items) > 0 {
		if granted, err = activityreward.RollAndGrant(ctx, tx, userID, "personal_challenge", raceID, regID, cfg); err != nil {
			return false, nil, fmt.Errorf("roll and grant: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, nil, fmt.Errorf("commit: %w", err)
	}
	return true, granted, nil
}

// MarkAttemptExpired CAS 標記個人挑戰 attempt 逾期：只有 status='paid' 才會更新（冪等；已 completed
// 的 attempt 不會被誤改成 expired，因為此時 status 已不是 'paid'）。逾期釋放唯一約束，可再報名。
func (r *Repository) MarkAttemptExpired(ctx context.Context, regID string) error {
	if _, err := r.db.Exec(ctx,
		`UPDATE registrations SET status='expired' WHERE id=$1 AND status='paid'`, regID); err != nil {
		return fmt.Errorf("mark attempt expired: %w", err)
	}
	return nil
}

// longestConsecutiveRun 升冪日期清單中「最長連續(每日間隔剛好 24h)」的長度
// （抄 profile/achievements.go computeStreak：漏一天即中斷重算，回傳歷史最長段，不綁「今天」錨點）。
func longestConsecutiveRun(days []time.Time) int {
	longest, cur := 0, 0
	for i := range days {
		if i > 0 && days[i].Sub(days[i-1]) == 24*time.Hour {
			cur++
		} else {
			cur = 1
		}
		if cur > longest {
			longest = cur
		}
	}
	return longest
}

// evaluateChallengeRule 依規則型別即時查詢使用者自 startedAt 起的活動、回傳是否達標 + 進度。
func (s *Service) evaluateChallengeRule(ctx context.Context, rule *ChallengeRule, userID string, startedAt time.Time, externalData bool) (completed bool, progress *ChallengeProgress, err error) {
	switch rule.CompletionType {
	case CompletionStreakDays:
		days, err := s.repo.streakQualifyingDays(ctx, userID, startedAt, rule.MinKmPerDay, externalData)
		if err != nil {
			return false, nil, err
		}
		longest := longestConsecutiveRun(days)
		progress = &ChallengeProgress{CompletionType: rule.CompletionType, StreakDays: longest, TargetDays: rule.Days}
		return longest >= rule.Days, progress, nil

	case CompletionWindowCumulative:
		end := startedAt.AddDate(0, 0, rule.WindowDays)
		sumKm, maxKm, err := s.repo.windowAgg(ctx, userID, startedAt, end, externalData)
		if err != nil {
			return false, nil, err
		}
		progress = &ChallengeProgress{
			CompletionType: rule.CompletionType,
			CumKm:          round2(sumKm),
			TargetCumKm:    rule.CumKm,
			BestSingleKm:   round2(maxKm),
			TargetSingleKm: rule.SingleKm,
			WindowEndsAt:   &end,
		}
		completed = sumKm >= rule.CumKm && (rule.SingleKm <= 0 || maxKm >= rule.SingleKm)
		return completed, progress, nil

	case CompletionSingleDistance:
		best, err := s.repo.maxDistanceSince(ctx, userID, startedAt, externalData)
		if err != nil {
			return false, nil, err
		}
		progress = &ChallengeProgress{
			CompletionType: rule.CompletionType,
			BestSingleKm:   round2(best),
			TargetSingleKm: rule.SingleKm,
		}
		return best >= rule.SingleKm, progress, nil

	default:
		return false, nil, fmt.Errorf("invalid challenge_rule completion_type: %s", rule.CompletionType)
	}
}

// GetPersonalProgress GET /races/{id}/personal-progress：個人挑戰模式(personal)完成判定引擎的觸發點。
// 查詢登入者在此賽事「進行中」的 attempt，即時評估規則是否達標，達標/逾期則 CAS 標記
// completed/expired（冪等，重複呼叫不會重複標記、不會把已完成的又改掉）。只評估呼叫者自己，
// 範圍有界（單一使用者 O(1)），不掃全體參賽者，不碰集體賽事管線。
func (s *Service) GetPersonalProgress(ctx context.Context, raceID, userID string) (*PersonalProgress, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}
	// 非 personal 賽事無此端點行為（不應被呼叫；防禦性擋下，避免對非 personal 的報名誤判/誤標記）。
	if race.EventMode != "personal" || race.ChallengeRule == nil {
		return nil, ErrRaceNotFound
	}

	completedCount, err := s.repo.CompletedChallengeCount(ctx, userID, raceID)
	if err != nil {
		return nil, err
	}

	attempt, err := s.repo.FindActivePersonalAttempt(ctx, userID, raceID)
	if err != nil {
		return nil, err
	}
	if attempt == nil {
		return &PersonalProgress{HasAttempt: false, CompletedCount: completedCount}, nil
	}

	rule := race.ChallengeRule
	// pending（尚未付款）：challenge_started_at 還沒設，尚無「起算點」可評估，原樣回傳待繳費狀態。
	if attempt.Status != "paid" || attempt.ChallengeStartedAt == nil {
		return &PersonalProgress{
			HasAttempt:     true,
			Status:         attempt.Status,
			Rule:           rule,
			CompletedCount: completedCount,
		}, nil
	}

	startedAt := *attempt.ChallengeStartedAt
	completed, progress, err := s.evaluateChallengeRule(ctx, rule, userID, startedAt, race.ExternalData)
	if err != nil {
		return nil, err
	}

	status := attempt.Status
	var newlyGranted []activityreward.GrantedReward
	if completed {
		claimed, grantedNow, err := s.repo.MarkAttemptCompletedAndGrant(ctx, attempt.ID)
		if err != nil {
			return nil, err
		}
		status = "completed"
		if claimed {
			completedCount++ // 這次呼叫剛完成，立即反映在回應（下次呼叫會改由 CompletedChallengeCount 算入）
			newlyGranted = grantedNow
		}
	} else {
		now := time.Now()
		expired := now.After(race.EndDate)
		if rule.CompletionType == CompletionWindowCumulative && rule.WindowDays > 0 &&
			now.After(startedAt.AddDate(0, 0, rule.WindowDays)) {
			expired = true
		}
		if expired {
			if err := s.repo.MarkAttemptExpired(ctx, attempt.ID); err != nil {
				return nil, err
			}
			status = "expired"
		}
	}

	return &PersonalProgress{
		HasAttempt:     true,
		Status:         status,
		Rule:           rule,
		Progress:       progress,
		CompletedCount: completedCount,
		NewlyGranted:   newlyGranted,
	}, nil
}
