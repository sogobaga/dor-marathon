package race

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/activityreward"
)

// --- 賽事進度（讀取時即時計算使用者相關任務的達成度）---

// progAct 進度計算用的精簡活動
type progAct struct {
	UserID  string
	GroupID string
	Dist    float64
	Ascent  float64
	HR      int
	PaceS   int
	At      time.Time
	KmPaces []int // 每公里分段配速(秒/km)；供 avg_pace_range 分段判定
}

// TaskProgress 單一任務 + 達成度
type TaskProgress struct {
	RaceTask
	GroupName    string  `json:"group_name,omitempty"`
	ScopeLabel   string  `json:"scope_label"` // 集體 / 本組團體 / 本組個人（前台分區）
	Current      float64 `json:"current"`     // threshold 累計值
	Done         bool    `json:"done"`
	QualifyCount int     `json:"qualify_count"` // range 符合筆數
}

// MyRaceStats 使用者在此賽事的個人統計
type MyRaceStats struct {
	TotalKm    float64 `json:"total_km"`
	Activities int     `json:"activities"`
	AscentM    float64 `json:"ascent_m"`
}

// RaceProgress 進度頁回應
type RaceProgress struct {
	My           MyRaceStats                    `json:"my"`
	HasGroup     bool                           `json:"has_group"`
	GroupName    string                         `json:"group_name,omitempty"`
	Started      bool                           `json:"started"`    // 賽事是否已開始（未開始 → 前台顯示提示）
	Registered   bool                           `json:"registered"` // 目前登入者是否已報名此賽事（未報名 → 打卡等需報名的操作先提示）
	Tasks        []TaskProgress                 `json:"tasks"`
	NewlyGranted []activityreward.GrantedReward `json:"newly_granted,omitempty"` // 本次呼叫剛觸發「個人額外挑戰」完成時發放的即時獎勵（活動獎勵系統一般化，migration 134；比照 personal 模式 PersonalProgress.NewlyGranted）
}

// LoadRaceActivities 取得某賽事所有未標記活動（含活動者目前分組）
func (r *Repository) LoadRaceActivities(ctx context.Context, raceID string) ([]progAct, error) {
	// 跨賽事歸戶：一筆跑步計入「該會員所有報名中、且 recorded_at 落在賽事期間內」的賽事，
	// 不再看 activity.race_id（同一筆可同時計入多場賽事/挑戰）。
	// 個人挑戰模式(personal)「隨報隨進行」：里程只算「該報名者付款起算(challenge_started_at)之後」的活動，
	// 不像一般賽事從 rc.start_date 起算（否則報名前產生的跑步會被算進活動進度）。personal 只認進行中(paid)
	// 的 attempt（唯一約束保證同時最多一筆，避免多次 attempt 重複計）；非 personal 維持原本 status<>cancelled
	// ＋賽事期間 [start_date, end_date] 窗（用 CASE/條件式一條查詢分流，一般賽事行為完全不變）。
	rows, err := r.db.Query(ctx, `
		SELECT a.user_id::text, COALESCE(reg.group_id::text,''),
		       a.distance_km, COALESCE(a.ascent_m,0), COALESCE(a.avg_hr,0), a.avg_pace_s, a.recorded_at,
		       COALESCE(a.km_paces, '{}')
		FROM races rc
		JOIN registrations reg ON reg.race_id = rc.id
		     AND ( (rc.event_mode <> 'personal' AND reg.status <> 'cancelled')
		        OR (rc.event_mode = 'personal' AND reg.status = 'paid' AND reg.challenge_started_at IS NOT NULL) )
		JOIN activities a ON a.user_id = reg.user_id AND NOT a.flagged
		                  AND a.recorded_at <= rc.end_date
		                  AND a.recorded_at >= CASE WHEN rc.event_mode = 'personal'
		                                            THEN reg.challenge_started_at ELSE rc.start_date END
		                  AND (a.source IS NULL OR (rc.external_data AND a.source <> 'strava'))
		WHERE rc.id = $1`, raceID)
	if err != nil {
		return nil, fmt.Errorf("load race activities: %w", err)
	}
	defer rows.Close()
	out := []progAct{}
	for rows.Next() {
		var a progAct
		if err := rows.Scan(&a.UserID, &a.GroupID, &a.Dist, &a.Ascent, &a.HR, &a.PaceS, &a.At, &a.KmPaces); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// userRaceTaskCompletions 該會員在此賽事「已觸發過即時獎勵」的任務 id 集合（migration 134
// race_task_completions）。一次查全部、放 map，供 GetRaceProgress 迴圈內判斷某 group_individual 任務
// 這次評估到 Done 時是否為第一次，避免每次輪詢（前端 30 秒 SWR）都對已完成的任務重複開交易嘗試 INSERT。
func (r *Repository) userRaceTaskCompletions(ctx context.Context, raceID, userID string) (map[string]bool, error) {
	rows, err := r.db.Query(ctx,
		`SELECT task_id::text FROM race_task_completions WHERE race_id=$1 AND user_id=$2`, raceID, userID)
	if err != nil {
		return nil, fmt.Errorf("user race task completions: %w", err)
	}
	defer rows.Close()
	m := map[string]bool{}
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			return nil, err
		}
		m[taskID] = true
	}
	return m, rows.Err()
}

// MarkRaceTaskCompletedAndGrant CAS 標記「個人額外挑戰」(race_tasks scope=group_individual) 完成觸發一次
// 即時獎勵：INSERT race_task_completions ON CONFLICT(task_id,user_id) DO NOTHING，只有真的插入新列
// （RowsAffected==1，代表這次呼叫是「第一個」把這個使用者這個任務標記完成的）才在同一交易內 roll 發放
// （比照 personal_progress.go MarkAttemptCompletedAndGrant 的「CAS 搶完成 + 同交易 roll」模式；這裡沒有
// 既有狀態欄位可 CAS UPDATE，改用 UNIQUE(task_id,user_id) + INSERT...ON CONFLICT 當唯一冪等防線——
// 呼叫端(progress.go)的 map 預檢只是效能優化、不是正確性保證，真正擋重複發獎的是這裡的 DB 唯一約束）。
// 任一步失敗，交易整個 Rollback——completion 不落地，下次評估到 Done 時會再次嘗試，不會出現
// 「completion 記了但獎沒發」的半成功。
func (r *Repository) MarkRaceTaskCompletedAndGrant(ctx context.Context, raceID, raceTitle, taskID, userID string, cfg *activityreward.RewardConfig) (claimed bool, granted []activityreward.GrantedReward, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // Commit 後為 no-op

	tag, err := tx.Exec(ctx, `
		INSERT INTO race_task_completions (race_id, task_id, user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (task_id, user_id) DO NOTHING`, raceID, taskID, userID)
	if err != nil {
		return false, nil, fmt.Errorf("insert race task completion: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil, nil // 沒搶到（此使用者此任務先前已觸發過）：no-op
	}

	var issuedGroupIDs []string
	if cfg != nil && len(cfg.Items) > 0 {
		if granted, issuedGroupIDs, err = activityreward.RollAndGrant(ctx, tx, userID, "race_task", raceID, taskID, cfg); err != nil {
			return false, nil, fmt.Errorf("roll and grant: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, nil, fmt.Errorf("commit: %w", err)
	}
	// commit 後才用非 tx 連線查真實庫存告警，理由與 MarkAttemptCompletedAndGrant 完全相同（見該函式註解）。
	if len(issuedGroupIDs) > 0 {
		go checkAndNotifyLowStock(r.db, raceTitle, issuedGroupIDs)
	}
	return true, granted, nil
}

// taipeiTZ：每日／每週／連續天數分桶一律用台灣日曆日（固定 +8，不依賴容器 tzdata）。
// ⚠️ 2026-09-03 事故：原本用 recorded_at 的 UTC 日期分桶，8/30 06:52（台北）的 12.41km 是 8/29 22:52Z，
// 被併進 8/29 那一桶（3.31+12.41=15.72），「每日里程」任務顯示 15.72 km，與每日列表／貢獻榜（皆已用台北日）對不上。
// daily_history.go（+8h）與 personal_progress.go（SQL AT TIME ZONE 'Asia/Taipei'）早已是台北口徑，這裡補齊。
var taipeiTZ = time.FixedZone("Asia/Taipei", 8*3600)

// metricValue 依指標計算累計/最佳值（threshold 類）
func metricValue(acts []progAct, metric string) float64 {
	switch metric {
	case "cumulative_distance":
		s := 0.0
		for _, a := range acts {
			s += a.Dist
		}
		return s
	case "single_distance":
		m := 0.0
		for _, a := range acts {
			if a.Dist > m {
				m = a.Dist
			}
		}
		return m
	case "cumulative_ascent":
		s := 0.0
		for _, a := range acts {
			s += a.Ascent
		}
		return s
	case "single_ascent":
		m := 0.0
		for _, a := range acts {
			if a.Ascent > m {
				m = a.Ascent
			}
		}
		return m
	case "daily_distance":
		return bestBucket(acts, func(a progAct) string { return a.At.In(taipeiTZ).Format("2006-01-02") })
	case "weekly_distance":
		return bestBucket(acts, func(a progAct) string {
			y, w := a.At.In(taipeiTZ).ISOWeek()
			return fmt.Sprintf("%d-%02d", y, w)
		})
	case "streak_days":
		return longestStreak(acts)
	}
	return 0
}

// bestBucket 將里程依 key 分桶後回傳最高桶總和（每日/每週里程）
func bestBucket(acts []progAct, key func(progAct) string) float64 {
	m := map[string]float64{}
	for _, a := range acts {
		m[key(a)] += a.Dist
	}
	best := 0.0
	for _, v := range m {
		if v > best {
			best = v
		}
	}
	return best
}

// longestStreak 最長連續有活動天數
func longestStreak(acts []progAct) float64 {
	if len(acts) == 0 {
		return 0
	}
	daySet := map[string]bool{}
	for _, a := range acts {
		daySet[a.At.In(taipeiTZ).Format("2006-01-02")] = true // 台北日曆日（見 taipeiTZ）
	}
	days := make([]time.Time, 0, len(daySet))
	for d := range daySet {
		t, _ := time.Parse("2006-01-02", d)
		days = append(days, t)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
	best, cur := 1, 1
	for i := 1; i < len(days); i++ {
		if days[i].Sub(days[i-1]) == 24*time.Hour {
			cur++
			if cur > best {
				best = cur
			}
		} else {
			cur = 1
		}
	}
	return float64(best)
}

// rangeQualify 計算落在區間內的活動筆數（配速/心率區間）。
// 配速：優先用「每公里分段」——任一公里落在區間即算（比整段均配速好達成）；無分段(Strava/手動/舊資料)退回整段均配速。
func rangeQualify(acts []progAct, metric string, lo, hi float64) int {
	inRange := func(v float64) bool { return v > 0 && v >= lo && v <= hi }
	n := 0
	for _, a := range acts {
		if metric == "avg_pace_range" {
			if len(a.KmPaces) > 0 {
				for _, ps := range a.KmPaces {
					if inRange(float64(ps)) {
						n++
						break // 任一公里符合即算這筆
					}
				}
			} else if inRange(float64(a.PaceS)) {
				n++
			}
			continue
		}
		if inRange(float64(a.HR)) { // avg_hr_range
			n++
		}
	}
	return n
}

// GetRaceProgress 計算使用者在此賽事的個人統計與各層任務達成度
func (s *Service) GetRaceProgress(ctx context.Context, raceID, userID string) (*RaceProgress, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}
	race.FillDisplay(time.Now())

	tasks, err := s.repo.GetRaceTasks(ctx, raceID)
	if err != nil {
		return nil, err
	}
	// 短 TTL 快取（見 cache.go）：進度頁前端 30 秒輪詢，讓同一窗口內的所有觀看者共用一次掃描。
	acts, err := s.repo.getRaceActivitiesCached(ctx, raceID)
	if err != nil {
		return nil, err
	}
	groups, err := s.repo.GetGroups(ctx, raceID)
	if err != nil {
		return nil, err
	}
	groupName := map[string]string{}
	for i := range groups {
		groupName[groups[i].ID] = groups[i].Name
	}

	myGroup := ""
	myCheckins := map[string]bool{} // checkpointID → flagged（待審）
	registered := false
	if userID != "" {
		myGroup, _ = s.repo.GetUserGroupID(ctx, userID, raceID)
		myCheckins, _ = s.repo.userRaceCheckins(ctx, userID, raceID)
		registered, _ = s.repo.userRegisteredInRace(ctx, userID, raceID)
	}

	// 即時獎勵一般化（migration 134）：personal 走既有 attempt 引擎(MarkAttemptCompletedAndGrant)，
	// 絕不能在這裡重複觸發；只有非 personal 且此賽事有設定 reward_config 才需要查「已觸發過」的任務集合
	// （多數賽事沒設定 reward_config，直接略過這次查詢，維持原本零開銷）。
	taskCompletions := map[string]bool{}
	rewardEligible := userID != "" && race.EventMode != "personal" && race.RewardConfig != nil && len(race.RewardConfig.Items) > 0
	if rewardEligible {
		taskCompletions, err = s.repo.userRaceTaskCompletions(ctx, raceID, userID)
		if err != nil {
			return nil, err
		}
	}
	var newlyGranted []activityreward.GrantedReward
	// triggerTaskReward 「個人額外挑戰」(group_individual scope) 完成時觸發一次即時獎勵 CAS
	// （見 MarkRaceTaskCompletedAndGrant）。taskCompletions 只是效能預檢（避免對已完成任務重複開交易），
	// 真正防重複發獎靠該函式內 race_task_completions 的 UNIQUE 約束。失敗時記錄警告但不中斷整個進度回應
	// ——reward 是附加效果，不該因單次 roll 失敗連累使用者看不到自己的任務進度。
	triggerTaskReward := func(t RaceTask, done bool) {
		if !rewardEligible || !done || t.Scope != ScopeGroupIndividual || taskCompletions[t.ID] {
			return
		}
		claimed, grantedNow, err := s.repo.MarkRaceTaskCompletedAndGrant(ctx, raceID, race.Title, t.ID, userID, race.RewardConfig)
		if err != nil {
			log.Warn().Err(err).Str("race_id", raceID).Str("task_id", t.ID).Str("user_id", userID).
				Msg("race task reward grant failed")
			return
		}
		if claimed {
			newlyGranted = append(newlyGranted, grantedNow...)
		}
	}

	// 個人統計
	var mine []progAct
	for _, a := range acts {
		if a.UserID == userID && userID != "" {
			mine = append(mine, a)
		}
	}
	prog := &RaceProgress{HasGroup: myGroup != "", GroupName: groupName[myGroup], Registered: registered, Tasks: []TaskProgress{}}
	prog.Started = !time.Now().Before(race.StartDate)
	for _, a := range mine {
		prog.My.TotalKm += a.Dist
		prog.My.AscentM += a.Ascent
		prog.My.Activities++
	}

	// 該分組成員活動
	var groupActs []progAct
	if myGroup != "" {
		for _, a := range acts {
			if a.GroupID == myGroup {
				groupActs = append(groupActs, a)
			}
		}
	}

	for i := range tasks {
		t := tasks[i]
		var set []progAct
		var label, gname string
		switch t.Scope {
		case ScopeRaceCollective:
			set, label = acts, "賽事集體"
		case ScopeGroupTeam:
			if myGroup == "" || (t.GroupID != "" && t.GroupID != myGroup) {
				continue // 與使用者所屬分組無關（其他分組的專屬任務）
			}
			gname = groupName[myGroup]
			if t.GroupID == "" {
				label = "所有分組共同（團體）"
			} else {
				label = "本組團體"
			}
			set = groupActs
		case ScopeGroupIndividual:
			if t.GroupID != "" && t.GroupID != myGroup {
				continue
			}
			gname = groupName[myGroup]
			if t.GroupID == "" {
				label = "所有分組共同（個人）"
			} else {
				label = "本組個人"
			}
			set = mine
		default:
			continue
		}

		tp := TaskProgress{RaceTask: t, ScopeLabel: label, GroupName: gname}
		// 打卡點任務：依當前會員的打卡紀錄計算集點進度（與 set/活動無關）
		if t.MetricType == MetricCheckpoint {
			collected := 0
			for ci := range tp.Checkpoints {
				st, ok := myCheckins[tp.Checkpoints[ci].ID]
				if !ok {
					continue
				}
				if st { // flagged → 待審
					tp.Checkpoints[ci].Pending = true
				} else {
					tp.Checkpoints[ci].Collected = true
					collected++
				}
			}
			tp.Current = float64(collected)
			tp.Done = len(tp.Checkpoints) > 0 && collected >= len(tp.Checkpoints)
			triggerTaskReward(t, tp.Done)
			prog.Tasks = append(prog.Tasks, tp)
			continue
		}
		spec, ok := MetricCatalog[t.MetricType]
		if ok && spec.Kind == MetricRange {
			lo, hi := 0.0, 0.0
			if t.RangeLo != nil {
				lo = *t.RangeLo
			}
			if t.RangeHi != nil {
				hi = *t.RangeHi
			}
			tp.QualifyCount = rangeQualify(set, t.MetricType, lo, hi)
			tp.Done = tp.QualifyCount > 0
		} else {
			tp.Current = math.Round(metricValue(set, t.MetricType)*100) / 100
			target := 0.0
			if t.TargetValue != nil {
				target = *t.TargetValue
			}
			tp.Done = target > 0 && tp.Current >= target
		}
		triggerTaskReward(t, tp.Done)
		prog.Tasks = append(prog.Tasks, tp)
	}
	prog.NewlyGranted = newlyGranted
	return prog, nil
}
