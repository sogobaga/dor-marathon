// 個人挑戰模式（event_mode=personal）P5：後台獎勵管理。
// 獎勵＝「完成者中抽獎/限額」，LINE Point 由後台人工發放；系統只管「資格＋發放狀態」。
// 以「每一筆完成」為單位（同一人完成多次＝多筆完成＝多個抽獎資格；對應每次挑戰皆需付費 299 的經濟）。
// 三支 HTTP handler 見 handler.go（AdminListRewardCompletions/AdminUpdateRewardCompletion/
// AdminDrawRewardWinners），皆只作用於 registrations.status='completed'（僅個人挑戰模式會產生此狀態，
// 見 124 號 migration 註解），不影響其他模式/其他狀態。詳見設計 memory personal-challenge-mode。
package race

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// RewardCompletionRow 後台獎勵管理：單筆「完成挑戰」紀錄
type RewardCompletionRow struct {
	RegistrationID    string     `json:"registration_id"`
	UserID            string     `json:"user_id"`
	UserName          string     `json:"user_name"`
	UserEmail         string     `json:"user_email"`
	CompletedAt       time.Time  `json:"completed_at"`
	AttemptNo         int        `json:"attempt_no"`
	RewardStatus      string     `json:"reward_status"` // ''=未處理｜won=中獎待發｜fulfilled=已發放
	RewardNote        string     `json:"reward_note,omitempty"`
	RewardFulfilledAt *time.Time `json:"reward_fulfilled_at,omitempty"`
}

// RewardCompletionSummary 該賽事完成筆數統計（永遠是全量，不受列表篩選影響）
type RewardCompletionSummary struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"` // reward_status=''
	Won       int `json:"won"`
	Fulfilled int `json:"fulfilled"`
}

// RewardCompletionsResponse GET .../reward-completions 回應
type RewardCompletionsResponse struct {
	Completions []RewardCompletionRow   `json:"completions"`
	Count       int                     `json:"count"` // 符合篩選條件的總筆數（分頁用，非本頁筆數）
	Summary     RewardCompletionSummary `json:"summary"`
}

// ErrRewardCompletionNotFound PUT reward-completions/:regID 找不到符合條件的列（不存在／非 completed）
var ErrRewardCompletionNotFound = errors.New("reward completion not found")

// rewardCompletionRows 依 completed_at ASC 列出該賽事的完成紀錄；rewardStatus="all" 不篩選，
// 其他值（""｜"won"｜"fulfilled"）只列該狀態（""＝待處理，非「不篩選」——與 "all" 明確區分）。
func (r *Repository) rewardCompletionRows(ctx context.Context, raceID, rewardStatus string, limit, offset int) ([]RewardCompletionRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT reg.id, reg.user_id, u.name, u.email, reg.completed_at, reg.attempt_no,
		       reg.reward_status, COALESCE(reg.reward_note,''), reg.reward_fulfilled_at
		FROM registrations reg
		JOIN users u ON u.id = reg.user_id
		WHERE reg.race_id = $1 AND reg.status = 'completed'
		  AND ($2 = 'all' OR reg.reward_status = $2)
		ORDER BY reg.completed_at ASC
		LIMIT $3 OFFSET $4`, raceID, rewardStatus, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list reward completions: %w", err)
	}
	defer rows.Close()

	out := []RewardCompletionRow{}
	for rows.Next() {
		var row RewardCompletionRow
		if err := rows.Scan(&row.RegistrationID, &row.UserID, &row.UserName, &row.UserEmail,
			&row.CompletedAt, &row.AttemptNo, &row.RewardStatus, &row.RewardNote, &row.RewardFulfilledAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// rewardCompletionCount 符合篩選條件（含 reward_status="all"）的總筆數，分頁用。
func (r *Repository) rewardCompletionCount(ctx context.Context, raceID, rewardStatus string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM registrations
		WHERE race_id = $1 AND status = 'completed' AND ($2 = 'all' OR reward_status = $2)`,
		raceID, rewardStatus).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count reward completions: %w", err)
	}
	return n, nil
}

// rewardCompletionSummary 該賽事完成筆數統計（全量，不受篩選影響——供頁面頂部摘要用）。
func (r *Repository) rewardCompletionSummary(ctx context.Context, raceID string) (RewardCompletionSummary, error) {
	var s RewardCompletionSummary
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE reward_status=''),
		       COUNT(*) FILTER (WHERE reward_status='won'),
		       COUNT(*) FILTER (WHERE reward_status='fulfilled')
		FROM registrations WHERE race_id=$1 AND status='completed'`, raceID).
		Scan(&s.Total, &s.Pending, &s.Won, &s.Fulfilled)
	if err != nil {
		return s, fmt.Errorf("reward completion summary: %w", err)
	}
	return s, nil
}

// UpdateRewardCompletion 設定單筆完成紀錄的 reward_status/reward_note；status='fulfilled' 時一併寫入
// reward_fulfilled_at=NOW()，改為非 fulfilled（''｜'won'）時清 NULL。僅能作用於 status='completed'
// 的報名（防呆——不讓後台誤改到未完成/已取消的報名）。找不到符合條件的列回 ErrRewardCompletionNotFound。
func (r *Repository) UpdateRewardCompletion(ctx context.Context, regID, rewardStatus, note string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE registrations
		SET reward_status = $2,
		    reward_note = $3,
		    reward_fulfilled_at = CASE WHEN $2 = 'fulfilled' THEN NOW() ELSE NULL END
		WHERE id = $1 AND status = 'completed'`, regID, rewardStatus, note)
	if err != nil {
		return fmt.Errorf("update reward completion: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRewardCompletionNotFound
	}
	return nil
}

// DrawRewardWinners 抽獎：從該賽事 status='completed' AND reward_status='' 的完成紀錄隨機抽 n 筆設為
// 'won'。單一交易內以資料修改型 CTE 完成挑選＋鎖定＋更新＋取回使用者資訊，SKIP LOCKED 防併發重複抽中
// 同一筆（比照 internal/monopoly/draw.go 的原子搶鎖式抽獎寫法）。回傳這次抽中的清單。
func (r *Repository) DrawRewardWinners(ctx context.Context, raceID string, n int) ([]RewardCompletionRow, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) // Commit 後為 no-op

	rows, err := tx.Query(ctx, `
		WITH picked AS (
			UPDATE registrations SET reward_status = 'won'
			WHERE id IN (
				SELECT id FROM registrations
				WHERE race_id = $1 AND status = 'completed' AND reward_status = ''
				ORDER BY random() LIMIT $2
				FOR UPDATE SKIP LOCKED
			)
			RETURNING id, user_id, completed_at, attempt_no
		)
		SELECT p.id, p.user_id, u.name, u.email, p.completed_at, p.attempt_no
		FROM picked p JOIN users u ON u.id = p.user_id`, raceID, n)
	if err != nil {
		return nil, fmt.Errorf("draw reward winners: %w", err)
	}
	defer rows.Close()

	out := []RewardCompletionRow{}
	for rows.Next() {
		var row RewardCompletionRow
		if err := rows.Scan(&row.RegistrationID, &row.UserID, &row.UserName, &row.UserEmail,
			&row.CompletedAt, &row.AttemptNo); err != nil {
			return nil, err
		}
		row.RewardStatus = "won"
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit draw reward winners: %w", err)
	}
	return out, nil
}

// ListRewardCompletions 個人挑戰模式 P5：列出該賽事完成紀錄＋統計摘要。非 personal 賽事回 ErrRaceNotFound
// （比照 GetPersonalLeaderboard 的防呆寫法，避免誤用在其他模式的賽事上）。
func (s *Service) ListRewardCompletions(ctx context.Context, raceID, rewardStatus string, limit, offset int) (*RewardCompletionsResponse, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.EventMode != "personal" {
		return nil, ErrRaceNotFound
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := s.repo.rewardCompletionRows(ctx, raceID, rewardStatus, limit, offset)
	if err != nil {
		return nil, err
	}
	count, err := s.repo.rewardCompletionCount(ctx, raceID, rewardStatus)
	if err != nil {
		return nil, err
	}
	summary, err := s.repo.rewardCompletionSummary(ctx, raceID)
	if err != nil {
		return nil, err
	}
	return &RewardCompletionsResponse{Completions: rows, Count: count, Summary: summary}, nil
}

// UpdateRewardCompletion 個人挑戰模式 P5：設定單筆完成紀錄的中獎/發放狀態＋備註。
func (s *Service) UpdateRewardCompletion(ctx context.Context, regID, rewardStatus, note string) error {
	return s.repo.UpdateRewardCompletion(ctx, regID, rewardStatus, note)
}

// DrawRewardWinners 個人挑戰模式 P5：隨機抽 n 筆完成紀錄設為中獎。非 personal 賽事回 ErrRaceNotFound。
func (s *Service) DrawRewardWinners(ctx context.Context, raceID string, n int) ([]RewardCompletionRow, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.EventMode != "personal" {
		return nil, ErrRaceNotFound
	}
	return s.repo.DrawRewardWinners(ctx, raceID, n)
}
