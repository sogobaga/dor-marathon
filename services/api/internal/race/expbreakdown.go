package race

import (
	"context"
	"time"
)

// ExpBreakdownItem 單筆 EXP 來源明細（給結算彈窗逐條呈現）
type ExpBreakdownItem struct {
	Label  string `json:"label"`
	Amount int    `json:"amount"`
	Dp     int    `json:"dp"`   // 同來源同時獲得的 DP
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
	Gained        int                `json:"gained"`
	ExpBefore     int                `json:"exp_before"`
	ExpAfter      int                `json:"exp_after"`
	DpGained      int                `json:"dp_gained"`      // 本場總獲得 DP
	DpAfter       int                `json:"dp_after"`       // 結算後 DP 餘額
	CompletionPct float64            `json:"completion_pct"` // 完成度（累積里程/分組目標，0~100；無目標則 100）
	Items         []ExpBreakdownItem `json:"items"`
	Levels        []ExpLevelRow      `json:"levels"`
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
		SELECT l.source, l.amount, l.dp_amount, COALESCE(t.title,''), COALESCE(t.scope,'')
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
		var amount, dp int
		if err := rows.Scan(&source, &amount, &dp, &title, &scope); err != nil {
			return nil, err
		}
		item := ExpBreakdownItem{Amount: amount, Dp: dp}
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
		out.DpGained += dp
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 目前 EXP/DP（= 結算後），回推結算前
	if userID != "" {
		_ = r.db.QueryRow(ctx, `SELECT COALESCE(exp,0), COALESCE(dp,0) FROM users WHERE id=$1`, userID).Scan(&out.ExpAfter, &out.DpAfter)
	}
	out.ExpBefore = out.ExpAfter - out.Gained
	if out.ExpBefore < 0 {
		out.ExpBefore = 0
	}

	// 完成度：本場累積里程 / 分組目標里程（無目標則視為 100%）
	var totalKm, targetKm float64
	_ = r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(a.distance_km),0), COALESCE(MAX(g.target_distance_km),0)
		FROM registrations reg
		JOIN races r ON r.id = reg.race_id
		LEFT JOIN race_groups g ON g.id = reg.group_id
		LEFT JOIN activities a ON a.user_id = reg.user_id AND NOT a.flagged
		                      AND a.recorded_at BETWEEN r.start_date AND r.end_date
		WHERE reg.user_id=$1 AND reg.race_id=$2 AND reg.status<>'cancelled'`,
		userID, raceID).Scan(&totalKm, &targetKm)
	if targetKm > 0 {
		out.CompletionPct = totalKm / targetKm * 100
		if out.CompletionPct > 100 {
			out.CompletionPct = 100
		}
	} else {
		out.CompletionPct = 100
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
