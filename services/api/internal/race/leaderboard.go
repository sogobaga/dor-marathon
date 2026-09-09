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
	DistanceKm   float64    `json:"distance_km"` // 完賽判定/排序依據的「score」（見下方 Score 欄位）；非寵物賽事＝OwnerKm，維持既有語意
	IsFollowing  bool       `json:"is_following"`
	IsMe         bool       `json:"is_me"`
	// OwnerKm/PetKm/Score/PetScoreMode：寵物雲端馬拉松成績規則（migration 174，D4）。非寵物賽事
	// （race.pet_kind==""）PetScoreMode 恆為 ""（wirePetScoreMode 轉換過，見 pet_scoring.go；內部
	// sentinel "owner" 不外洩）、PetKm 恆為 0、Score==OwnerKm==DistanceKm，前端可直接忽略這三個
	// 新欄位、行為與今天完全相同。
	OwnerKm      float64 `json:"owner_km"`
	PetKm        float64 `json:"pet_km"`
	Score        float64 `json:"score"`
	PetScoreMode string  `json:"pet_score_mode"`
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
	distanceKm   float64 // = score（見下方），保留欄位名相容既有呼叫端（certificate.go／reward_draw.go 直接讀這個欄位當完成依據）
	// ownerKm/petKm/score/petScoreMode：寵物雲端馬拉松成績規則（migration 174，D4）。非寵物賽事
	// petScoreMode 恆為 PetScoreModeOwner、petKm 恆為 0、score==ownerKm==distanceKm。
	ownerKm      float64
	petKm        float64
	score        float64
	petScoreMode string
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
//
// 寵物雲端馬拉松成績規則（migration 174，D4）：完成判定改比較「score」而非單純飼主里程——
// score 依每筆報名的「有效規則」（EffectivePetScoreMode，見 pet_scoring.go）由飼主里程／狗狗
// 累積里程／兩者加總組成（ComposeScore）。為了讓 completion_at（影響「完成時間榜」排名與抽獎
// 池排序）精確落在「score 真正跨過目標」的那一刻，飼主活動事件與狗狗歸戶事件先合併依時間排序，
// 再逐筆累加判定——而不是先各自算出兩個總量才比較（那樣只能知道「有沒有跨過」，不知道「何時跨過」）。
// 非寵物賽事（race.pet_kind==""）：EffectivePetScoreMode 恆回 PetScoreModeOwner、跳過
// loadPetEventsByUser（省一次查詢），score===ownerKm，行為與 migration 174 之前完全相同。
func computeFinishersWith(ctx context.Context, db pgQueryer, raceID string) ([]finisher, int, error) {
	var petKind, petScoreMode string
	if err := db.QueryRow(ctx, `SELECT pet_kind, pet_score_mode FROM races WHERE id=$1`, raceID).
		Scan(&petKind, &petScoreMode); err != nil {
		return nil, 0, fmt.Errorf("load race pet mode: %w", err)
	}

	// 跨賽事歸戶：依「報名中 + recorded_at 落在賽事期間」計入，不看 activity.race_id
	rows, err := db.Query(ctx, `
		SELECT a.user_id::text, COALESCE(g.id::text,''), u.email,
		       COALESCE(NULLIF(u.name,''), u.handle), COALESCE(td.name,''), COALESCE(g.name,''),
		       COALESCE(g.target_distance_km, 0), a.distance_km, a.duration_s, a.recorded_at,
		       COALESCE(g.for_owner,TRUE), COALESCE(g.for_pet,TRUE)
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

	type userAgg struct {
		groupID, email, nickname, title, groupName string
		target                                     float64
		mode                                       string
		events                                     []scoreEvent
	}
	users := map[string]*userAgg{}
	for rows.Next() {
		var uid, gid, em, nick, ttl, gname string
		var tgt, dist float64
		var dur int
		var at time.Time
		var forOwner, forPet bool
		if err := rows.Scan(&uid, &gid, &em, &nick, &ttl, &gname, &tgt, &dist, &dur, &at, &forOwner, &forPet); err != nil {
			return nil, 0, err
		}
		ua, ok := users[uid]
		if !ok {
			ua = &userAgg{
				groupID: gid, email: em, nickname: nick, title: ttl, groupName: gname, target: tgt,
				mode: EffectivePetScoreMode(petKind, petScoreMode, forOwner, forPet),
			}
			users[uid] = ua
		}
		ua.events = append(ua.events, scoreEvent{kind: "owner", dist: dist, dur: dur, at: at})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	if petKind != "" && len(users) > 0 {
		petEvents, err := loadPetEventsByUser(ctx, db, raceID)
		if err != nil {
			return nil, 0, err
		}
		for uid, evs := range petEvents {
			ua, ok := users[uid]
			if !ok {
				// 理論上不會發生：每一筆通過 gate 的 pet_activities(owner_run) 都有對應且同樣通過
				// gate 的 activities 列（同一趟活動），必定已經出現在上面的 owner 查詢裡；防禦性略過。
				continue
			}
			ua.events = append(ua.events, evs...)
		}
	}

	var finishers []finisher
	for uid, ua := range users {
		sort.Slice(ua.events, func(i, j int) bool { return ua.events[i].at.Before(ua.events[j].at) })
		var ownerAcc, petAcc float64
		var accTime int
		for _, ev := range ua.events {
			if ev.kind == "pet" {
				petAcc += ev.dist
			} else {
				ownerAcc += ev.dist
				accTime += ev.dur // 總時間只計飼主自己的跑步時長（見檔頭註解），不因寵物加總模式而重複計入同一趟時長
			}
			score := ComposeScore(ua.mode, ownerAcc, petAcc)
			if ua.target > 0 && score >= ua.target {
				finishers = append(finishers, finisher{
					userID: uid, groupID: ua.groupID, email: ua.email, nickname: ua.nickname, title: ua.title, groupName: ua.groupName,
					completionAt: ev.at, totalTimeS: accTime, distanceKm: score,
					ownerKm: round2(ownerAcc), petKm: round2(petAcc), score: round2(score), petScoreMode: ua.mode,
				})
				break
			}
		}
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
			OwnerKm: f.ownerKm, PetKm: f.petKm, Score: f.score, PetScoreMode: wirePetScoreMode(f.petScoreMode),
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
