package race

import (
	"context"
	"errors"
	"math"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/dor/api/internal/auth"
)

// 任務貢獻明細：點擊某任務的進度面板 → 看該任務相關成員「里程貢獻」前 20 名 + 自己的里程與排名。

// Contributor 單一成員的里程貢獻
type Contributor struct {
	Rank       int     `json:"rank"`
	UserID     string  `json:"user_id"`
	Name       string  `json:"name"`
	GroupName  string  `json:"group_name,omitempty"`
	DistanceKm float64 `json:"distance_km"`
	Activities int     `json:"activities"`
	IsMe       bool    `json:"is_me"`
}

// TaskContributors 某任務的貢獻榜
type TaskContributors struct {
	TaskID      string        `json:"task_id"`
	TaskTitle   string        `json:"task_title"`
	Scope       string        `json:"scope"`
	PoolLabel   string        `json:"pool_label"`  // 計算範圍：全體參賽者 / 本組：XXX
	Total       int           `json:"total"`       // 範圍內總人數
	Contributed int           `json:"contributed"` // 已有里程（>0）人數
	Top         []Contributor `json:"top"`         // 前 20 名
	Me          *Contributor  `json:"me,omitempty"` // 自己（即使不在前 20 名也回傳）
}

type contribRow struct {
	UserID    string
	Name      string
	GroupName string
	Dist      float64
	Acts      int
}

// LoadTaskContributors 依範圍（groupID 空＝全體）彙總每位報名者在賽事期間的里程與活動數，里程高到低排序。
func (r *Repository) LoadTaskContributors(ctx context.Context, raceID, groupID string) ([]contribRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT reg.user_id::text,
		       COALESCE(NULLIF(p.nickname,''), u.handle, '跑者') AS name,
		       COALESCE(g.name,'') AS group_name,
		       COALESCE(SUM(a.distance_km),0)::float8 AS dist,
		       COUNT(a.id) AS acts
		FROM races rc
		JOIN registrations reg ON reg.race_id = rc.id AND reg.status <> 'cancelled'
		JOIN users u ON u.id = reg.user_id
		LEFT JOIN user_profiles p ON p.user_id = reg.user_id
		LEFT JOIN race_groups g ON g.id = reg.group_id
		LEFT JOIN activities a ON a.user_id = reg.user_id AND NOT a.flagged
		                       AND a.recorded_at BETWEEN rc.start_date AND rc.end_date
		WHERE rc.id = $1 AND ($2 = '' OR reg.group_id::text = $2)
		GROUP BY reg.user_id, p.nickname, u.handle, g.name
		ORDER BY dist DESC, acts DESC`, raceID, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []contribRow{}
	for rows.Next() {
		var c contribRow
		if err := rows.Scan(&c.UserID, &c.Name, &c.GroupName, &c.Dist, &c.Acts); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetTaskContributors 計算某任務的里程貢獻榜（範圍依任務 scope：集體/所有分組個人＝全體；本組團體/個人＝自己所屬分組）。
func (s *Service) GetTaskContributors(ctx context.Context, raceID, taskID, userID string) (*TaskContributors, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}
	tasks, err := s.repo.GetRaceTasks(ctx, raceID)
	if err != nil {
		return nil, err
	}
	var task *RaceTask
	for i := range tasks {
		if tasks[i].ID == taskID {
			task = &tasks[i]
			break
		}
	}
	if task == nil {
		return nil, ErrRaceNotFound
	}

	myGroup := ""
	if userID != "" {
		myGroup, _ = s.repo.GetUserGroupID(ctx, userID, raceID)
	}
	// 分組限定的任務（本組團體/本組個人）→ 只看自己所屬分組；否則看全體
	groupScoped := task.Scope == ScopeGroupTeam || (task.Scope == ScopeGroupIndividual && task.GroupID != "")
	poolGroup := ""
	if groupScoped && myGroup != "" {
		poolGroup = myGroup
	}

	rows, err := s.repo.LoadTaskContributors(ctx, raceID, poolGroup)
	if err != nil {
		return nil, err
	}

	res := &TaskContributors{TaskID: task.ID, TaskTitle: task.Title, Scope: task.Scope, Total: len(rows), PoolLabel: "全體參賽者", Top: []Contributor{}}
	for i, c := range rows {
		if c.Dist > 0 {
			res.Contributed++
		}
		if poolGroup != "" && c.GroupName != "" {
			res.PoolLabel = "本組：" + c.GroupName
		}
		row := Contributor{
			Rank: i + 1, UserID: c.UserID, Name: c.Name, GroupName: c.GroupName,
			DistanceKm: math.Round(c.Dist*100) / 100, Activities: c.Acts,
			IsMe: userID != "" && c.UserID == userID,
		}
		if i < 20 {
			res.Top = append(res.Top, row)
		}
		if row.IsMe {
			me := row
			res.Me = &me
		}
	}
	return res, nil
}

// GET /api/v1/races/:raceID/tasks/:taskID/contributors — 某任務的里程貢獻榜（公開，登入後含自己排名）
func (h *Handler) TaskContributors(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	taskID := chi.URLParam(r, "taskID")
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	c, err := h.svc.GetTaskContributors(r.Context(), raceID, taskID, userID)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get contributors")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"contributors": c})
}
