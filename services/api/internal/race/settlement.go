package race

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dor/api/internal/vip"
)

// ErrRaceNotEnded 賽事尚未結束、不能（非強制）結算
var ErrRaceNotEnded = errors.New("race has not ended")

// SettleResult 結算結果摘要
type SettleResult struct {
	RaceID         string `json:"race_id"`
	Participants   int    `json:"participants"`
	AwardedUsers   int    `json:"awarded_users"`
	TotalExp       int    `json:"total_exp"`
	TotalDp        int    `json:"total_dp"`
	TotalVipDays   int    `json:"total_vip_days"`
	AlreadySettled bool   `json:"already_settled"`
}

type expRulesVals struct {
	collective int
	group      int
	individual int
	perKm      int
	// DP 平行費率
	dpCollective int
	dpGroup      int
	dpIndividual int
	dpPerKm      int
	// VIP 天數（僅任務完成三種 scope，里程無 VIP 天數）
	vipCollective int
	vipGroup      int
	vipIndividual int
}

type participant struct {
	userID  string
	groupID string
}

// award 單一來源同時記 EXP、DP 與 VIP 天數
type award struct {
	exp     int
	dp      int
	vipDays int
}

// SettleRaceEXP 結算某賽事的 EXP（idempotent；force=true 可提前/重跑補發）
func (s *Service) SettleRaceEXP(ctx context.Context, raceID string, force bool) (*SettleResult, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}
	if !force && time.Now().Before(race.EndDate) {
		return nil, ErrRaceNotEnded
	}
	return s.repo.settleRaceEXP(ctx, race, force)
}

// taskDone 判斷一組活動是否達成某任務（與 progress.go 的 Done 判定一致）
func taskDone(set []progAct, t RaceTask) bool {
	if spec, ok := MetricCatalog[t.MetricType]; ok && spec.Kind == MetricRange {
		lo, hi := 0.0, 0.0
		if t.RangeLo != nil {
			lo = *t.RangeLo
		}
		if t.RangeHi != nil {
			hi = *t.RangeHi
		}
		return rangeQualify(set, t.MetricType, lo, hi) > 0
	}
	target := 0.0
	if t.TargetValue != nil {
		target = *t.TargetValue
	}
	return target > 0 && metricValue(set, t.MetricType) >= target
}

// expRules 讀取 EXP 規則（單例）
func (r *Repository) expRules(ctx context.Context) (expRulesVals, error) {
	var v expRulesVals
	err := r.db.QueryRow(ctx,
		`SELECT per_collective_task, per_group_task, per_individual_task, per_km,
		        dp_per_collective_task, dp_per_group_task, dp_per_individual_task, dp_per_km,
		        vip_days_collective_task, vip_days_group_task, vip_days_individual_task
		 FROM exp_rules WHERE id=TRUE`).
		Scan(&v.collective, &v.group, &v.individual, &v.perKm,
			&v.dpCollective, &v.dpGroup, &v.dpIndividual, &v.dpPerKm,
			&v.vipCollective, &v.vipGroup, &v.vipIndividual)
	return v, err
}

// raceParticipants 取得未取消的報名者（user + 分組）
func (r *Repository) raceParticipants(ctx context.Context, raceID string) ([]participant, error) {
	rows, err := r.db.Query(ctx,
		`SELECT user_id::text, COALESCE(group_id::text,'') FROM registrations WHERE race_id=$1 AND status <> 'cancelled'`, raceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []participant
	for rows.Next() {
		var p participant
		if err := rows.Scan(&p.userID, &p.groupID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// settleRaceEXP 計算並發放 EXP（核心）
func (r *Repository) settleRaceEXP(ctx context.Context, race *Race, force bool) (*SettleResult, error) {
	// 便宜的前置判斷：非強制下，已結算就直接跳過（避免重複讀取/計算）
	if !force {
		var due bool
		if err := r.db.QueryRow(ctx,
			`SELECT exp_settled_at IS NULL AND end_date < NOW() FROM races WHERE id=$1`, race.ID).Scan(&due); err != nil {
			return nil, err
		}
		if !due {
			return &SettleResult{RaceID: race.ID, AlreadySettled: true}, nil
		}
	}

	rules, err := r.expRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("exp rules: %w", err)
	}
	tasks, err := r.GetRaceTasks(ctx, race.ID)
	if err != nil {
		return nil, err
	}
	acts, err := r.LoadRaceActivities(ctx, race.ID)
	if err != nil {
		return nil, err
	}
	groups, err := r.GetGroups(ctx, race.ID)
	if err != nil {
		return nil, err
	}
	parts, err := r.raceParticipants(ctx, race.ID)
	if err != nil {
		return nil, err
	}
	// checkpoint 任務完成度：map[taskID]map[userID]bool（集滿全部點且未標記）
	cpDone, err := r.checkpointCompletion(ctx, race.ID)
	if err != nil {
		return nil, err
	}

	// 活動索引
	byUser := map[string][]progAct{}
	byGroup := map[string][]progAct{}
	for _, a := range acts {
		byUser[a.UserID] = append(byUser[a.UserID], a)
		if a.GroupID != "" {
			byGroup[a.GroupID] = append(byGroup[a.GroupID], a)
		}
	}
	groupTarget := map[string]float64{}
	groupReward := map[string]int{}
	groupDpReward := map[string]int{}
	for i := range groups {
		if groups[i].TargetDistanceKm != nil {
			groupTarget[groups[i].ID] = *groups[i].TargetDistanceKm
		}
		groupReward[groups[i].ID] = groups[i].ExpReward
		groupDpReward[groups[i].ID] = groups[i].DpReward
	}

	// awards[userID][source] = {exp, dp, vipDays}（同一 source 同列記 EXP、DP 與 VIP 天數）
	awards := map[string]map[string]award{}
	add := func(uid, source string, exp, dp, vipDays int) {
		if exp <= 0 && dp <= 0 && vipDays <= 0 {
			return
		}
		if awards[uid] == nil {
			awards[uid] = map[string]award{}
		}
		awards[uid][source] = award{exp: exp, dp: dp, vipDays: vipDays}
	}

	// 完成任務 EXP（依層級）
	for ti := range tasks {
		t := tasks[ti]
		// 打卡點任務：依集滿與否、依 scope 給對應額度（集滿全部點才發）
		if t.MetricType == MetricCheckpoint {
			exp, dp, vipDays := rules.individual, rules.dpIndividual, rules.vipIndividual
			switch t.Scope {
			case ScopeRaceCollective:
				exp, dp, vipDays = rules.collective, rules.dpCollective, rules.vipCollective
			case ScopeGroupTeam:
				exp, dp, vipDays = rules.group, rules.dpGroup, rules.vipGroup
			}
			for _, p := range parts {
				if t.Scope != ScopeRaceCollective && t.GroupID != "" && t.GroupID != p.groupID {
					continue
				}
				if cpDone[t.ID][p.userID] {
					add(p.userID, "task:"+t.ID, exp, dp, vipDays)
				}
			}
			continue
		}
		switch t.Scope {
		case ScopeRaceCollective:
			if taskDone(acts, t) {
				for _, p := range parts {
					add(p.userID, "task:"+t.ID, rules.collective, rules.dpCollective, rules.vipCollective)
				}
			}
		case ScopeGroupTeam:
			for gi := range groups {
				g := &groups[gi]
				if t.GroupID != "" && t.GroupID != g.ID {
					continue
				}
				if taskDone(byGroup[g.ID], t) {
					for _, p := range parts {
						if p.groupID == g.ID {
							add(p.userID, "task:"+t.ID, rules.group, rules.dpGroup, rules.vipGroup)
						}
					}
				}
			}
		case ScopeGroupIndividual:
			for _, p := range parts {
				if t.GroupID != "" && t.GroupID != p.groupID {
					continue
				}
				if taskDone(byUser[p.userID], t) {
					add(p.userID, "task:"+t.ID, rules.individual, rules.dpIndividual, rules.vipIndividual)
				}
			}
		}
	}

	// 完成賽事 EXP（達分組目標里程）。里程 EXP 改為日常發放（worker），不在此結算；
	// 分組完賽獎勵（race_groups.exp_reward/dp_reward）不發 VIP 天數，僅「任務」三種 scope 才發。
	for _, p := range parts {
		totalKm := 0.0
		for _, a := range byUser[p.userID] {
			totalKm += a.Dist
		}
		if tgt := groupTarget[p.groupID]; tgt > 0 && totalKm >= tgt {
			add(p.userID, "completion", groupReward[p.groupID], groupDpReward[p.groupID], 0)
		}
	}

	// 寫入（交易內：claim 結算旗標 + ledger idempotent + 加 EXP）
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if force {
		if _, err := tx.Exec(ctx, `UPDATE races SET exp_settled_at=NOW() WHERE id=$1`, race.ID); err != nil {
			return nil, err
		}
	} else {
		tag, err := tx.Exec(ctx, `UPDATE races SET exp_settled_at=NOW() WHERE id=$1 AND exp_settled_at IS NULL`, race.ID)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			// 已被其他結算搶先 → 不重複發
			return &SettleResult{RaceID: race.ID, AlreadySettled: true}, nil
		}
	}

	res := &SettleResult{RaceID: race.ID, Participants: len(parts)}
	for uid, sources := range awards {
		expDelta, dpDelta, vipDelta := 0, 0, 0
		for source, a := range sources {
			tag, err := tx.Exec(ctx,
				`INSERT INTO exp_ledger (user_id, race_id, source, amount, dp_amount) VALUES ($1,$2,$3,$4,$5)
				 ON CONFLICT (user_id, race_id, source) DO NOTHING`, uid, race.ID, source, a.exp, a.dp)
			if err != nil {
				return nil, err
			}
			if tag.RowsAffected() == 1 {
				expDelta += a.exp
				dpDelta += a.dp
				vipDelta += a.vipDays
			}
		}
		if expDelta > 0 || dpDelta > 0 {
			if _, err := tx.Exec(ctx, `UPDATE users SET exp = exp + $1, dp = dp + $2 WHERE id=$3`, expDelta, dpDelta, uid); err != nil {
				return nil, err
			}
			res.TotalExp += expDelta
			res.TotalDp += dpDelta
		}
		if vipDelta > 0 {
			// 只對本次新寫入 exp_ledger 的 source 累加天數，故重跑結算不會重複延長 VIP。
			if err := vip.Extend(ctx, tx, uid, vipDelta); err != nil {
				return nil, err
			}
			res.TotalVipDays += vipDelta
		}
		if expDelta > 0 || dpDelta > 0 || vipDelta > 0 {
			res.AwardedUsers++
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return res, nil
}
