// 個人挑戰模式（event_mode=personal）完賽證明細分：personal 賽事可重複報名挑戰，一般完賽證明
// （GetMyCertificate／computeFinishers 依「分組目標里程」判定單次完成）不適用，改以「完賽歷程」彙總卡
// 呈現：挑戰次數(總 attempts)、完成次數、最佳成績（依 challenge_rule 完成條件類型呈現時間或距離維度）、
// 最近一次完成時間。觸發點：GET /races/{id}/personal-history（登入態，只查呼叫者自己）。詳見設計 memory
// personal-challenge-mode。與完賽證明共用同一顯示開關（config.certificate_disabled，見 certificate.go）。
package race

import (
	"context"
	"fmt"
	"time"
)

// PersonalHistory GET /races/{id}/personal-history 回應。
type PersonalHistory struct {
	TotalAttempts  int `json:"total_attempts"`  // 總報名次數（不論結果：pending/paid/completed/expired/cancelled 皆計入）
	CompletedCount int `json:"completed_count"` // 已完成挑戰次數
	// BestMetric 依 challenge_rule.completion_type 決定「最佳成績」呈現哪個維度：
	//   duration：streak_days 專用——目標天數固定、比不出「更好的天數」，改比「最快達標所花用時」
	//   distance：window_cumulative/single_distance 專用——核心本就是距離，直接比「最佳距離」最直觀
	// 空字串＝尚無完成紀錄（此時 BestDurationS/BestDistanceKm 皆為零值，前端不顯示此列）。
	BestMetric      string     `json:"best_metric,omitempty"`
	BestDurationS   int        `json:"best_duration_s,omitempty"`   // best_metric=duration 時有效：最短完成用時（秒）
	BestDistanceKm  float64    `json:"best_distance_km,omitempty"`  // best_metric=distance 時有效：最佳距離（km）
	LastCompletedAt *time.Time `json:"last_completed_at,omitempty"` // 最近一次完成時間；從未完成過為 nil
}

// personalHistoryBestMetric 純函式：依規則型別決定最佳成績呈現維度，見上方 PersonalHistory.BestMetric 註解。
// rule 為 nil（理論上不會發生，personal 賽事建立時已強制驗證 ChallengeRule 必填）時回空字串防呆。
func personalHistoryBestMetric(rule *ChallengeRule) string {
	if rule == nil {
		return ""
	}
	switch rule.CompletionType {
	case CompletionWindowCumulative, CompletionSingleDistance:
		return "distance"
	default: // streak_days 及任何未知型別一律回 duration（用時永遠是可比較的通用維度）
		return "duration"
	}
}

// personalTotalAttempts 使用者在此賽事的總報名次數（挑戰次數），不論最終狀態。
func (r *Repository) personalTotalAttempts(ctx context.Context, userID, raceID string) (int, error) {
	var n int
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM registrations WHERE user_id=$1 AND race_id=$2`,
		userID, raceID).Scan(&n); err != nil {
		return 0, fmt.Errorf("personal total attempts: %w", err)
	}
	return n, nil
}

// personalLastCompletedAt 使用者在此賽事最近一次完成挑戰的時間；從未完成過回 nil。
func (r *Repository) personalLastCompletedAt(ctx context.Context, userID, raceID string) (*time.Time, error) {
	var t *time.Time
	if err := r.db.QueryRow(ctx,
		`SELECT MAX(completed_at) FROM registrations WHERE user_id=$1 AND race_id=$2 AND status='completed'`,
		userID, raceID).Scan(&t); err != nil {
		return nil, fmt.Errorf("personal last completed at: %w", err)
	}
	return t, nil
}

// personalBestDurationS 已完成 attempts 中「從挑戰起算到完成」耗時最短的秒數（streak_days 用：目標天數
// 固定，比的是誰達標得快）。無完成紀錄或缺起訖時間回 0,false。
func (r *Repository) personalBestDurationS(ctx context.Context, userID, raceID string) (int, bool, error) {
	var sec *float64
	if err := r.db.QueryRow(ctx, `
		SELECT MIN(EXTRACT(EPOCH FROM (completed_at - challenge_started_at)))
		FROM registrations
		WHERE user_id=$1 AND race_id=$2 AND status='completed'
		  AND challenge_started_at IS NOT NULL AND completed_at IS NOT NULL`,
		userID, raceID).Scan(&sec); err != nil {
		return 0, false, fmt.Errorf("personal best duration: %w", err)
	}
	if sec == nil {
		return 0, false, nil
	}
	return int(*sec), true, nil
}

// personalBestDistanceKm 已完成 attempts 中「完成當下達成的距離」最佳值。攻略完成時的實際數值未落地
// 儲存（P3 完成判定引擎只即時評估、CAS 標記，不寫回進度快照），改用各 attempt 的
// [challenge_started_at, completed_at] 區間回頭查 activities 重算——與 evaluateChallengeRule 的
// windowAgg/maxDistanceSince 同一套邏輯，只是上界從「now/起算+window_days」換成「該筆已知的實際完成時間」。
// single=true 用 MAX(單趟)（single_distance）；false 用 SUM(累積)（window_cumulative）。多筆完成 attempt
// 取整體最佳（MAX）。無完成紀錄回 0,false。agg 為受控字面值（SUM/MAX），非使用者輸入，無 SQL 注入風險
// （比照 personal_progress.go streakQualifyingDays 的既有寫法）。
func (r *Repository) personalBestDistanceKm(ctx context.Context, userID, raceID string, single, externalData bool) (float64, bool, error) {
	agg := "SUM"
	if single {
		agg = "MAX"
	}
	var km *float64
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT MAX(t.agg) FROM (
			SELECT %s(a.distance_km) AS agg
			FROM registrations r
			JOIN activities a ON a.user_id = r.user_id AND NOT a.flagged
			  AND a.recorded_at >= r.challenge_started_at AND a.recorded_at <= r.completed_at
			  AND (a.source IS NULL OR ($3 AND a.source <> 'strava'))
			WHERE r.user_id=$1 AND r.race_id=$2 AND r.status='completed'
			  AND r.challenge_started_at IS NOT NULL AND r.completed_at IS NOT NULL
			GROUP BY r.id
		) t`, agg), userID, raceID, externalData).Scan(&km)
	if err != nil {
		return 0, false, fmt.Errorf("personal best distance: %w", err)
	}
	if km == nil {
		return 0, false, nil
	}
	return round2(*km), true, nil
}

// GetPersonalHistory GET /races/{id}/personal-history：個人挑戰模式(personal)完賽歷程彙總，取代一般模式
// 的完賽證明（GetMyCertificate 對 personal 賽事一律擋下，見該檔註解）。非 personal 賽事回 ErrRaceNotFound
// （防禦性擋下，同 GetPersonalProgress／GetPersonalLeaderboard 慣例）；certificate_disabled 開啟時回
// ErrCertificateDisabled（與完賽證明共用同一顯示開關語意）。
func (s *Service) GetPersonalHistory(ctx context.Context, raceID, userID string) (*PersonalHistory, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}
	if race.EventMode != "personal" {
		return nil, ErrRaceNotFound
	}
	if race.Config.CertificateDisabled {
		return nil, ErrCertificateDisabled
	}

	total, err := s.repo.personalTotalAttempts(ctx, userID, raceID)
	if err != nil {
		return nil, err
	}
	completed, err := s.repo.CompletedChallengeCount(ctx, userID, raceID)
	if err != nil {
		return nil, err
	}
	lastAt, err := s.repo.personalLastCompletedAt(ctx, userID, raceID)
	if err != nil {
		return nil, err
	}

	hist := &PersonalHistory{TotalAttempts: total, CompletedCount: completed, LastCompletedAt: lastAt}
	if completed > 0 {
		hist.BestMetric = personalHistoryBestMetric(race.ChallengeRule)
		switch hist.BestMetric {
		case "duration":
			if sec, ok, err := s.repo.personalBestDurationS(ctx, userID, raceID); err != nil {
				return nil, err
			} else if ok {
				hist.BestDurationS = sec
			}
		case "distance":
			single := race.ChallengeRule != nil && race.ChallengeRule.CompletionType == CompletionSingleDistance
			if km, ok, err := s.repo.personalBestDistanceKm(ctx, userID, raceID, single, race.ExternalData); err != nil {
				return nil, err
			} else if ok {
				hist.BestDistanceKm = km
			}
		}
	}
	return hist, nil
}
