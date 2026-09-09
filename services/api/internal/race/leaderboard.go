package race

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// --- 一般模式個人排名（累積里程達分組目標即「完成」）---

type LeaderRow struct {
	Rank         int        `json:"rank"`
	UserID       string     `json:"user_id"`
	Nickname     string     `json:"nickname"`
	Title        string     `json:"title"`
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
	groupID      string // 所屬分組 ID（獎勵管理一般化 migration 135：winning_group scope 依此過濾；既有呼叫端不使用）
	email        string // 獎勵管理一般化：中獎名單顯示用；既有呼叫端不使用
	nickname     string
	title        string
	groupName    string
	completionAt time.Time
	totalTimeS   int
	distanceKm   float64
}

// pgQueryer 是 *pgxpool.Pool 與 pgx.Tx 的共同子集（Query/QueryRow），讓完賽者池查詢可在既有連線池或
// 交易內執行——獎勵管理一般化（migration 135）的抽獎需要在「advisory lock 之後」的交易內重新取一次最新
// 完賽者池，因此把查詢邏輯抽成可注入 db 的版本，既有呼叫端（GetLeaderboard/GetCertificate 等）不受影響。
type pgQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// computeFinishers 逐使用者累積里程，達分組目標即記為完成
func (r *Repository) computeFinishers(ctx context.Context, raceID string) ([]finisher, int, error) {
	return computeFinishersWith(ctx, r.db, raceID)
}

// computeFinishersWith 是 computeFinishers 的核心實作，db 可傳 *pgxpool.Pool（一般讀取）或 pgx.Tx
// （獎勵抽獎需要在交易內查最新資料，見 reward_draw.go DrawRaceRewardWinners）。
func computeFinishersWith(ctx context.Context, db pgQueryer, raceID string) ([]finisher, int, error) {
	// 跨賽事歸戶：依「報名中 + recorded_at 落在賽事期間」計入，不看 activity.race_id
	rows, err := db.Query(ctx, `
		SELECT a.user_id::text, COALESCE(g.id::text,''), u.email,
		       COALESCE(NULLIF(u.name,''), u.handle), COALESCE(td.name,''), COALESCE(g.name,''),
		       COALESCE(g.target_distance_km, 0), a.distance_km, a.duration_s, a.recorded_at
		FROM races rc
		JOIN registrations reg ON reg.race_id = rc.id AND reg.status <> 'cancelled'
		JOIN activities a ON a.user_id = reg.user_id AND NOT a.flagged
		                  AND a.recorded_at BETWEEN rc.start_date AND rc.end_date
		                  AND (a.source IS NULL OR (rc.external_data AND a.source <> 'strava'))
		LEFT JOIN race_groups g ON g.id = reg.group_id
		JOIN users u ON u.id = reg.user_id
		LEFT JOIN user_profiles p ON p.user_id = reg.user_id
		LEFT JOIN title_defs td ON td.code = u.displayed_title
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
	var groupID, email, nickname, title, groupName string
	var done bool

	for rows.Next() {
		var uid, gid, em, nick, ttl, gname string
		var tgt, dist float64
		var dur int
		var at time.Time
		if err := rows.Scan(&uid, &gid, &em, &nick, &ttl, &gname, &tgt, &dist, &dur, &at); err != nil {
			return nil, 0, err
		}
		if uid != cur {
			cur, accDist, accTime, target = uid, 0, 0, tgt
			groupID, email, nickname, title, groupName, done = gid, em, nick, ttl, gname, false
		}
		if done {
			continue
		}
		accDist += dist
		accTime += dur
		if target > 0 && accDist >= target {
			finishers = append(finishers, finisher{
				userID: uid, groupID: groupID, email: email, nickname: nickname, title: title, groupName: groupName,
				completionAt: at, totalTimeS: accTime, distanceKm: accDist,
			})
			done = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	var total int
	if err := db.QueryRow(ctx,
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
	// 短 TTL 快取（見 cache.go）：基底完成榜與觀看者無關，同一窗口內的所有觀看者共用一次全場掃描；
	// is_following 等 per-viewer 欄位維持在下面用 FollowingSet 逐請求查詢，不進快取。
	finishers, total, err := s.repo.getFinishersCached(ctx, raceID)
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
			UserID: f.userID, Nickname: f.nickname, Title: f.title, GroupName: f.groupName,
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
