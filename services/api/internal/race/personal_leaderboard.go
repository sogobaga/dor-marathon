// 個人挑戰模式（event_mode=personal）P4：排行榜。
// 依「已完成次數 desc、同次數最早完成時間 asc」排序，讀 P3 寫入的 registrations(status='completed')，
// 不做任何寫入、不碰集體賽事管線（leaderboard.go/progress.go/settlement.go）。
// 觸發點：GET /races/{id}/personal-leaderboard（登入 optional）。詳見設計 memory personal-challenge-mode。
package race

import (
	"context"
	"fmt"
	"time"
)

// PersonalLeaderRow 個人挑戰排行榜單列。只回暱稱/頭像/次數，不含 account_code/email（隱私規則）。
type PersonalLeaderRow struct {
	Rank             int       `json:"rank"`
	UserID           string    `json:"user_id"`
	Name             string    `json:"name"`
	Avatar           string    `json:"avatar"`
	CompletedCount   int       `json:"completed_count"`
	FirstCompletedAt time.Time `json:"first_completed_at"`
	IsFollowing      bool      `json:"is_following"`
	IsMe             bool      `json:"is_me"`
}

// PersonalLeaderboard GET /races/{id}/personal-leaderboard 回應。
type PersonalLeaderboard struct {
	Leaderboard []PersonalLeaderRow `json:"leaderboard"`
	MyRank      int                 `json:"my_rank"`  // 登入者在榜上的名次；未完成過或未登入 = 0
	MyCount     int                 `json:"my_count"`
}

// personalLeaderboardRows 依完成次數 desc、最早完成時間 asc 聚合此賽事所有「已完成」挑戰的使用者（前 100）。
// userID 可為空字串（未登入）：is_following 一律 false（比照 explore.go Ranking 的
// `$1<>'' AND ...::uuid` 寫法，避免空字串對 uuid 欄位做比較噴錯）。
func (r *Repository) personalLeaderboardRows(ctx context.Context, raceID, userID string) ([]PersonalLeaderRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT r.user_id::text, COUNT(*) AS cnt, MIN(r.completed_at) AS first_at,
		       COALESCE(NULLIF(p.nickname,''), u.handle) AS name,
		       COALESCE(u.avatar_url,'') AS avatar,
		       ($2 <> '' AND EXISTS(SELECT 1 FROM follows f WHERE f.follower_id = NULLIF($2,'')::uuid AND f.followee_id = r.user_id)) AS is_following
		FROM registrations r
		JOIN users u ON u.id = r.user_id
		LEFT JOIN user_profiles p ON p.user_id = r.user_id
		WHERE r.race_id = $1 AND r.status = 'completed'
		GROUP BY r.user_id, p.nickname, u.handle, u.avatar_url
		ORDER BY cnt DESC, first_at ASC
		LIMIT 100`, raceID, userID)
	if err != nil {
		return nil, fmt.Errorf("personal leaderboard rows: %w", err)
	}
	defer rows.Close()

	out := []PersonalLeaderRow{}
	for rows.Next() {
		var row PersonalLeaderRow
		if err := rows.Scan(&row.UserID, &row.CompletedCount, &row.FirstCompletedAt, &row.Name, &row.Avatar, &row.IsFollowing); err != nil {
			return nil, err
		}
		row.IsMe = row.UserID == userID
		out = append(out, row)
	}
	return out, rows.Err()
}

// personalMyRank 登入者在此排行榜上的名次：依同一組排序鍵（cnt desc, first_at asc）算「排在我前面的人數+1」。
// 只在使用者已完成過至少一次時有意義；未完成/未登入回 0（呼叫端另行判斷）。
func (r *Repository) personalMyRank(ctx context.Context, raceID, userID string) (int, error) {
	if userID == "" {
		return 0, nil
	}
	var rank int
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE((
			SELECT t.rnk FROM (
				SELECT o.user_id,
				       ROW_NUMBER() OVER (ORDER BY COUNT(*) DESC, MIN(o.completed_at) ASC) AS rnk
				FROM registrations o
				WHERE o.race_id=$1 AND o.status='completed'
				GROUP BY o.user_id
			) t WHERE t.user_id = $2::uuid
		), 0)`, raceID, userID).Scan(&rank)
	if err != nil {
		return 0, fmt.Errorf("personal my rank: %w", err)
	}
	return rank, nil
}

// GetPersonalLeaderboard GET /races/{id}/personal-leaderboard：個人挑戰模式(personal)排行榜。
// 非 personal 賽事回 ErrRaceNotFound（防呆，不誤用此管線）。登入 optional：未登入時 is_me/is_following
// 皆 false、my_rank/my_count 皆 0。
func (s *Service) GetPersonalLeaderboard(ctx context.Context, raceID, userID string) (*PersonalLeaderboard, error) {
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

	rows, err := s.repo.personalLeaderboardRows(ctx, raceID, userID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Rank = i + 1
	}

	myRank, err := s.repo.personalMyRank(ctx, raceID, userID)
	if err != nil {
		return nil, err
	}
	var myCount int
	if userID != "" {
		myCount, err = s.repo.CompletedChallengeCount(ctx, userID, raceID)
		if err != nil {
			return nil, err
		}
	}

	return &PersonalLeaderboard{Leaderboard: rows, MyRank: myRank, MyCount: myCount}, nil
}
