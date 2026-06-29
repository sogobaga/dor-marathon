package race

import (
	"context"
	"time"
)

// ExpBreakdownItem 單筆 EXP 來源明細（給結算彈窗逐條呈現）
type ExpBreakdownItem struct {
	Label  string `json:"label"`
	Amount int    `json:"amount"`
	Kind   string `json:"kind"` // completion | mileage | task
}

// ExpLevelRow 等級門檻（累積 EXP）
type ExpLevelRow struct {
	Level       int    `json:"level"`
	Title       string `json:"title"`
	ExpRequired int    `json:"exp_required"`
}

// ExpBreakdown 結算彈窗資料
type ExpBreakdown struct {
	Gained    int                `json:"gained"`
	ExpBefore int                `json:"exp_before"`
	ExpAfter  int                `json:"exp_after"`
	Items     []ExpBreakdownItem `json:"items"`
	Levels    []ExpLevelRow      `json:"levels"`
}

// GetExpBreakdown 取得登入者在某賽事的 EXP 結算明細（會先確保已結算）
func (s *Service) GetExpBreakdown(ctx context.Context, raceID, userID string) (*ExpBreakdown, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}
	// 賽事已結束 → 確保已結算（同步，讓首位開啟者也拿得到明細）
	if time.Now().After(race.EndDate) {
		_, _ = s.SettleRaceEXP(ctx, raceID, false)
	}
	return s.repo.expBreakdown(ctx, raceID, userID)
}

func scopeLabel(scope string) string {
	switch scope {
	case ScopeRaceCollective:
		return "集體"
	case ScopeGroupTeam:
		return "分組"
	case ScopeGroupIndividual:
		return "個人"
	}
	return ""
}

func (r *Repository) expBreakdown(ctx context.Context, raceID, userID string) (*ExpBreakdown, error) {
	out := &ExpBreakdown{Items: []ExpBreakdownItem{}, Levels: []ExpLevelRow{}}

	// 明細（task 來源 join 任務標題）
	rows, err := r.db.Query(ctx, `
		SELECT l.source, l.amount, COALESCE(t.title,''), COALESCE(t.scope,'')
		FROM exp_ledger l
		LEFT JOIN race_tasks t ON ('task:' || t.id::text) = l.source
		WHERE l.user_id=$1 AND l.race_id=$2
		ORDER BY
		  CASE WHEN l.source='completion' THEN 0 WHEN l.source LIKE 'task:%' THEN 1 ELSE 2 END,
		  l.amount DESC`, userID, raceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var source, title, scope string
		var amount int
		if err := rows.Scan(&source, &amount, &title, &scope); err != nil {
			return nil, err
		}
		item := ExpBreakdownItem{Amount: amount}
		switch {
		case source == "completion":
			item.Kind, item.Label = "completion", "完成賽事"
		case source == "mileage":
			item.Kind, item.Label = "mileage", "完成里程"
		default: // task:<id>
			item.Kind = "task"
			if title != "" {
				if sl := scopeLabel(scope); sl != "" {
					item.Label = "任務：" + title + "（" + sl + "）"
				} else {
					item.Label = "任務：" + title
				}
			} else {
				item.Label = "任務達成"
			}
		}
		out.Items = append(out.Items, item)
		out.Gained += amount
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 目前 EXP（= 結算後），回推結算前
	if userID != "" {
		_ = r.db.QueryRow(ctx, `SELECT COALESCE(exp,0) FROM users WHERE id=$1`, userID).Scan(&out.ExpAfter)
	}
	out.ExpBefore = out.ExpAfter - out.Gained
	if out.ExpBefore < 0 {
		out.ExpBefore = 0
	}

	// 等級門檻
	lrows, err := r.db.Query(ctx, `SELECT level, COALESCE(title,''), exp_required FROM level_config ORDER BY exp_required`)
	if err != nil {
		return nil, err
	}
	defer lrows.Close()
	for lrows.Next() {
		var lr ExpLevelRow
		if err := lrows.Scan(&lr.Level, &lr.Title, &lr.ExpRequired); err != nil {
			return nil, err
		}
		out.Levels = append(out.Levels, lr)
	}
	return out, lrows.Err()
}
