package race

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// --- 一般模式個人排名（累積里程達分組目標即「完成」）---

type LeaderRow struct {
	Rank         int        `json:"rank"`
	UserID       string     `json:"user_id"`
	Nickname     string     `json:"nickname"`
	GroupName    string     `json:"group_name,omitempty"`
	CompletionAt *time.Time `json:"completion_at,omitempty"`
	TotalTimeS   int        `json:"total_time_s"`
	DistanceKm   float64    `json:"distance_km"`
	IsFollowing  bool       `json:"is_following"`
	IsMe         bool       `json:"is_me"`
}

type Leaderboard struct {
	FinishedCount int         `json:"finished_count"`
	TotalCount    int         `json:"total_count"`
	ByCompletion  []LeaderRow `json:"by_completion"`
	ByTotalTime   []LeaderRow `json:"by_total_time"`
}

type finisher struct {
	userID       string
	nickname     string
	groupName    string
	completionAt time.Time
	totalTimeS   int
	distanceKm   float64
}

// computeFinishers 逐使用者累積里程，達分組目標即記為完成
func (r *Repository) computeFinishers(ctx context.Context, raceID string) ([]finisher, int, error) {
	// 跨賽事歸戶：依「報名中 + recorded_at 落在賽事期間」計入，不看 activity.race_id
	rows, err := r.db.Query(ctx, `
		SELECT a.user_id::text, COALESCE(NULLIF(p.nickname,''), u.handle), COALESCE(g.name,''),
		       COALESCE(g.target_distance_km, 0), a.distance_km, a.duration_s, a.recorded_at
		FROM races rc
		JOIN registrations reg ON reg.race_id = rc.id AND reg.status <> 'cancelled'
		JOIN activities a ON a.user_id = reg.user_id AND NOT a.flagged
		                  AND a.recorded_at BETWEEN rc.start_date AND rc.end_date
		LEFT JOIN race_groups g ON g.id = reg.group_id
		JOIN users u ON u.id = reg.user_id
		LEFT JOIN user_profiles p ON p.user_id = reg.user_id
		WHERE rc.id = $1
		ORDER BY a.user_id, a.recorded_at`, raceID)
	if err != nil {
		return nil, 0, fmt.Errorf("leaderboard query: %w", err)
	}
	defer rows.Close()

	var finishers []finisher
	var cur string
	var accDist, target float64
	var accTime int
	var nickname, groupName string
	var done bool

	for rows.Next() {
		var uid, nick, gname string
		var tgt, dist float64
		var dur int
		var at time.Time
		if err := rows.Scan(&uid, &nick, &gname, &tgt, &dist, &dur, &at); err != nil {
			return nil, 0, err
		}
		if uid != cur {
			cur, accDist, accTime, target, nickname, groupName, done = uid, 0, 0, tgt, nick, gname, false
		}
		if done {
			continue
		}
		accDist += dist
		accTime += dur
		if target > 0 && accDist >= target {
			finishers = append(finishers, finisher{
				userID: uid, nickname: nickname, groupName: groupName,
				completionAt: at, totalTimeS: accTime, distanceKm: accDist,
			})
			done = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM registrations WHERE race_id=$1 AND status <> 'cancelled'`, raceID).Scan(&total); err != nil {
		return nil, 0, err
	}
	return finishers, total, nil
}

// FollowingSet 取得使用者追蹤的 followee 集合
func (r *Repository) FollowingSet(ctx context.Context, userID string) (map[string]bool, error) {
	set := map[string]bool{}
	if userID == "" {
		return set, nil
	}
	rows, err := r.db.Query(ctx, `SELECT followee_id::text FROM follows WHERE follower_id=$1`, userID)
	if err != nil {
		return set, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return set, err
		}
		set[id] = true
	}
	return set, rows.Err()
}

// GetLeaderboard 一般模式個人排名（完成時間榜 + 累計時間榜）
func (s *Service) GetLeaderboard(ctx context.Context, raceID, userID string) (*Leaderboard, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}
	finishers, total, err := s.repo.computeFinishers(ctx, raceID)
	if err != nil {
		return nil, err
	}
	following, err := s.repo.FollowingSet(ctx, userID)
	if err != nil {
		return nil, err
	}

	toRow := func(f finisher) LeaderRow {
		c := f.completionAt
		return LeaderRow{
			UserID: f.userID, Nickname: f.nickname, GroupName: f.groupName,
			CompletionAt: &c, TotalTimeS: f.totalTimeS, DistanceKm: round2(f.distanceKm),
			IsFollowing: following[f.userID], IsMe: f.userID == userID,
		}
	}

	byComp := make([]LeaderRow, len(finishers))
	for i, f := range finishers {
		byComp[i] = toRow(f)
	}
	byTime := make([]LeaderRow, len(byComp))
	copy(byTime, byComp)

	sort.Slice(byComp, func(i, j int) bool { return byComp[i].CompletionAt.Before(*byComp[j].CompletionAt) })
	sort.Slice(byTime, func(i, j int) bool { return byTime[i].TotalTimeS < byTime[j].TotalTimeS })
	for i := range byComp {
		byComp[i].Rank = i + 1
	}
	for i := range byTime {
		byTime[i].Rank = i + 1
	}

	return &Leaderboard{
		FinishedCount: len(finishers), TotalCount: total,
		ByCompletion: byComp, ByTotalTime: byTime,
	}, nil
}

func round2(v float64) float64 { return float64(int(v*100+0.5)) / 100 }
