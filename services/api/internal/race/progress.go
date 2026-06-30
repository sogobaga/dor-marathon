package race

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
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
}

// TaskProgress 單一任務 + 達成度
type TaskProgress struct {
	RaceTask
	GroupName    string  `json:"group_name,omitempty"`
	ScopeLabel   string  `json:"scope_label"`   // 集體 / 本組團體 / 本組個人（前台分區）
	Current      float64 `json:"current"`       // threshold 累計值
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
	My        MyRaceStats    `json:"my"`
	HasGroup  bool           `json:"has_group"`
	GroupName string         `json:"group_name,omitempty"`
	Started   bool           `json:"started"` // 賽事是否已開始（未開始 → 前台顯示提示）
	Tasks     []TaskProgress `json:"tasks"`
}

// LoadRaceActivities 取得某賽事所有未標記活動（含活動者目前分組）
func (r *Repository) LoadRaceActivities(ctx context.Context, raceID string) ([]progAct, error) {
	// 跨賽事歸戶：一筆跑步計入「該會員所有報名中、且 recorded_at 落在賽事期間內」的賽事，
	// 不再看 activity.race_id（同一筆可同時計入多場賽事/挑戰）。
	rows, err := r.db.Query(ctx, `
		SELECT a.user_id::text, COALESCE(reg.group_id::text,''),
		       a.distance_km, COALESCE(a.ascent_m,0), COALESCE(a.avg_hr,0), a.avg_pace_s, a.recorded_at
		FROM races rc
		JOIN registrations reg ON reg.race_id = rc.id AND reg.status <> 'cancelled'
		JOIN activities a ON a.user_id = reg.user_id AND NOT a.flagged
		                  AND a.recorded_at BETWEEN rc.start_date AND rc.end_date
		WHERE rc.id = $1`, raceID)
	if err != nil {
		return nil, fmt.Errorf("load race activities: %w", err)
	}
	defer rows.Close()
	out := []progAct{}
	for rows.Next() {
		var a progAct
		if err := rows.Scan(&a.UserID, &a.GroupID, &a.Dist, &a.Ascent, &a.HR, &a.PaceS, &a.At); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

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
		return bestBucket(acts, func(a progAct) string { return a.At.Format("2006-01-02") })
	case "weekly_distance":
		return bestBucket(acts, func(a progAct) string {
			y, w := a.At.ISOWeek()
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
		daySet[a.At.Format("2006-01-02")] = true
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

// rangeQualify 計算落在區間內的活動筆數（配速/心率區間）
func rangeQualify(acts []progAct, metric string, lo, hi float64) int {
	n := 0
	for _, a := range acts {
		var v float64
		if metric == "avg_pace_range" {
			v = float64(a.PaceS)
		} else { // avg_hr_range
			v = float64(a.HR)
		}
		if v > 0 && v >= lo && v <= hi {
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
	acts, err := s.repo.LoadRaceActivities(ctx, raceID)
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
	if userID != "" {
		myGroup, _ = s.repo.GetUserGroupID(ctx, userID, raceID)
		myCheckins, _ = s.repo.userRaceCheckins(ctx, userID, raceID)
	}

	// 個人統計
	var mine []progAct
	for _, a := range acts {
		if a.UserID == userID && userID != "" {
			mine = append(mine, a)
		}
	}
	prog := &RaceProgress{HasGroup: myGroup != "", GroupName: groupName[myGroup], Tasks: []TaskProgress{}}
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
		prog.Tasks = append(prog.Tasks, tp)
	}
	return prog, nil
}
