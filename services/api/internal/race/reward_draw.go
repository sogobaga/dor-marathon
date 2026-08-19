// 獎勵管理一般化（migration 135）：賽後抽獎從個人挑戰模式(personal)專屬的獎勵管理
// （personal_reward.go／registrations.reward_status 舊制）擴大到所有其餘賽事模式。兩套完全平行、
// 互不影響——personal 模式賽事打本檔案的 API 一律回 ErrRewardDrawNotPersonalCompatible（400），
// 請改用 personal_reward.go 那組端點；本檔案的 Service.DrawRewardWinners 等函式也拒絕 personal 賽事。
//
// 抽獎資格底線＝完賽：完成該賽事「基本目標」（各分組 target_distance_km）的報名者，判定邏輯直接重用
// leaderboard.go 的 computeFinishers（GetLeaderboard 完成榜、AdminGetCertificate 完賽證明用的同一條線）。
// 全體/分組/個人額外挑戰(race_tasks) 都是疊加的額外目標，這裡的資格線不看有沒有另外完成 race_tasks。
//
// 兩種抽籤範圍(scope)：
//   all_finishers — 全體完賽會員隨機抽籤
//   winning_group — 獲勝分組內的完賽成員隨機抽籤（獲勝組判定見下方兩個純函數）
//
// 抽獎為後台手動觸發、中獎人數後台輸入；可對同一賽事建立多次批次（如頭獎抽 1 名、普獎抽 10 名）。
// 池本身以 user 為單位（computeFinishers 每人至多一筆），天生同批次不重複中獎；跨批次是否排除先前
// 批次中獎者由 DrawRewardRequest.ExcludePrior 控制。
package race

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"
)

// --- 常數 ---

const (
	RewardScopeAllFinishers = "all_finishers"
	RewardScopeWinningGroup = "winning_group"
	WinRuleHighestMetric    = "highest_metric"
	WinRuleFirstToTarget    = "first_to_target"
)

// --- 錯誤 ---

var (
	// ErrRewardDrawNotApplicable personal 模式賽事打新版一般抽獎 API（應改用 personal_reward.go 舊制）
	ErrRewardDrawNotApplicable = errors.New("personal 模式請改用個人挑戰獎勵管理（reward-completions/reward-draw）")
	// ErrInvalidScope scope 不是 all_finishers 或 winning_group
	ErrInvalidScope = errors.New("scope 必須是 all_finishers 或 winning_group")
	// ErrInvalidWinRule winning_group 抽籤未指定合法的 win_rule
	ErrInvalidWinRule = errors.New("winning_group 抽籤需指定 win_rule（highest_metric 或 first_to_target）")
	// ErrInvalidWinnerCount winner_count 非正整數
	ErrInvalidWinnerCount = errors.New("winner_count 須為正整數")
	// ErrNoGroupTask 賽事沒有可用的 group_team 任務可供獲勝組判定
	ErrNoGroupTask = errors.New("此賽事無分組任務，無法用獲勝組抽獎")
	// ErrWinTaskNotFound 指定的 win_task_id 不存在、不屬於此賽事，或不是 group_team scope
	ErrWinTaskNotFound = errors.New("指定的分組任務不存在，或不是分組(團體)任務")
	// ErrGroupTaskUnsupported 指定的分組任務指標型態不支援用於獲勝組判定（打卡點/區間類無「越高越好」
	// 或「跨越時間點」的自然定義）
	ErrGroupTaskUnsupported = errors.New("此分組任務的指標型態不支援用於獲勝組判定，請改選其他分組任務")
	// ErrNoGroupReachedTarget 沒有任何分組達到判定任務目標，無法判定獲勝組（不可抽）
	ErrNoGroupReachedTarget = errors.New("沒有任何分組達成判定任務目標，目前無法判定獲勝組，暫不可抽獎")
	// ErrEmptyRewardPool 抽獎資格池為 0（無完賽者、或 winning_group 過濾後為空、或 exclude_prior 排除後歸零）
	ErrEmptyRewardPool = errors.New("目前沒有符合資格的完賽者可供抽獎")
	// ErrRewardWinnerNotFound 找不到指定的中獎紀錄
	ErrRewardWinnerNotFound = errors.New("reward winner not found")
)

// --- 型別 ---

// DrawRewardRequest 後台建立一次抽獎批次的參數
type DrawRewardRequest struct {
	Title        string
	Scope        string // all_finishers | winning_group
	WinRule      string // winning_group 用：highest_metric | first_to_target
	WinTaskID    string // winning_group 用：空＝取該賽事第一個非打卡點的 group_team 任務
	WinnerCount  int
	ExcludePrior bool
}

// RewardWinnerRow 單筆中獎紀錄（含使用者/分組顯示資訊，供後台名單直接渲染）
type RewardWinnerRow struct {
	ID                string     `json:"id"`
	DrawID            string     `json:"draw_id"`
	UserID            string     `json:"user_id"`
	UserName          string     `json:"user_name"`
	UserEmail         string     `json:"user_email"`
	GroupID           string     `json:"group_id,omitempty"`
	GroupName         string     `json:"group_name,omitempty"`
	RewardStatus      string     `json:"reward_status"` // ''=中獎待發｜fulfilled=已發放
	RewardNote        string     `json:"reward_note,omitempty"`
	RewardFulfilledAt *time.Time `json:"reward_fulfilled_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

// RewardDraw 一次抽獎批次（含中獎名單、抽獎當下的稽核資訊）
type RewardDraw struct {
	ID              string            `json:"id"`
	RaceID          string            `json:"race_id"`
	Title           string            `json:"title"`
	Scope           string            `json:"scope"`
	WinRule         string            `json:"win_rule,omitempty"`
	WinTaskID       string            `json:"win_task_id,omitempty"`
	WinnerCount     int               `json:"winner_count"` // 原訂中獎人數（池不足時實際中獎數見 len(Winners)）
	ExcludePrior    bool              `json:"exclude_prior"`
	WinningGroupIDs []string          `json:"winning_group_ids,omitempty"`
	PoolSize        int               `json:"pool_size"` // 抽獎當下（排除先前批次中獎者後）的資格池人數
	DrawnBy         string            `json:"drawn_by,omitempty"`
	DrawnAt         time.Time         `json:"drawn_at"`
	Winners         []RewardWinnerRow `json:"winners"`
}

// --- 純函數：獲勝分組判定（不碰 DB，供單元測試）---

// groupAgg 單一分組供獲勝判定用的活動聚合輸入
type groupAgg struct {
	groupID string
	acts    []progAct
}

// groupRankValue 依任務指標型態計算某分組的排名數值：threshold 類＝metricValue 累計值（分組總里程等，
// 越高越好）；range 類（配速/心率區間）沒有「越大越好」的單一數值可比，改用落在區間內的活動筆數。
func groupRankValue(acts []progAct, t RaceTask) float64 {
	if spec, ok := MetricCatalog[t.MetricType]; ok && spec.Kind == MetricRange {
		lo, hi := 0.0, 0.0
		if t.RangeLo != nil {
			lo = *t.RangeLo
		}
		if t.RangeHi != nil {
			hi = *t.RangeHi
		}
		return float64(rangeQualify(acts, t.MetricType, lo, hi))
	}
	return metricValue(acts, t.MetricType)
}

// winningGroupsHighestMetric win_rule=highest_metric：只在「已達成判定任務目標」的分組中比較指標值，
// 取最高值者（平手全數並列進池，多組成員都算獲勝組）。沒有任何分組達標 → ErrNoGroupReachedTarget。
// aggs 已由呼叫端依任務適用範圍（task.GroupID）過濾好，這裡不重複過濾。
func winningGroupsHighestMetric(aggs []groupAgg, t RaceTask) ([]string, error) {
	type cand struct {
		groupID string
		value   float64
	}
	var cands []cand
	for _, a := range aggs {
		if !taskDone(a.acts, t) {
			continue
		}
		cands = append(cands, cand{a.groupID, groupRankValue(a.acts, t)})
	}
	if len(cands) == 0 {
		return nil, ErrNoGroupReachedTarget
	}
	best := cands[0].value
	for _, c := range cands[1:] {
		if c.value > best {
			best = c.value
		}
	}
	var winners []string
	for _, c := range cands {
		if c.value == best {
			winners = append(winners, c.groupID)
		}
	}
	sort.Strings(winners) // 固定順序，方便測試/稽核比對
	return winners, nil
}

// winningGroupsFirstToTarget win_rule=first_to_target：每個分組的成員活動依時間排序、逐筆累加判定任務
// 指標，記錄「首次跨過 target_value」那一刻的活動時間戳；最早跨越者獲勝（平手全數並列，理論上因時間戳
// 精度極罕見發生）。只支援有 target_value 的 threshold 類指標（range/checkpoint 無自然定義的「跨越
// 時間點」，呼叫端應在解析 win_task_id 階段就擋掉，這裡對 target<=0 的分組直接略過、不視為候選）。
// 沒有任何分組達標 → ErrNoGroupReachedTarget。
func winningGroupsFirstToTarget(aggs []groupAgg, t RaceTask) ([]string, error) {
	target := 0.0
	if t.TargetValue != nil {
		target = *t.TargetValue
	}
	type cand struct {
		groupID string
		at      time.Time
	}
	var cands []cand
	if target > 0 {
		for _, a := range aggs {
			acts := append([]progAct(nil), a.acts...)
			sort.Slice(acts, func(i, j int) bool { return acts[i].At.Before(acts[j].At) })
			for i := range acts {
				if metricValue(acts[:i+1], t.MetricType) >= target {
					cands = append(cands, cand{a.groupID, acts[i].At})
					break
				}
			}
		}
	}
	if len(cands) == 0 {
		return nil, ErrNoGroupReachedTarget
	}
	best := cands[0].at
	for _, c := range cands[1:] {
		if c.at.Before(best) {
			best = c.at
		}
	}
	var winners []string
	for _, c := range cands {
		if c.at.Equal(best) {
			winners = append(winners, c.groupID)
		}
	}
	sort.Strings(winners)
	return winners, nil
}

// resolveWinTask 依 winTaskID 決定判定任務：明確指定則驗證屬於此賽事、為 group_team scope、且非打卡點
// 指標；未指定（""）則取該賽事第一個非打卡點的 group_team 任務（依 GetRaceTasks 的
// scope,display_order,created_at 排序）。賽事完全沒有 group_team 任務、或全部都是打卡點類型 → ErrNoGroupTask。
func resolveWinTask(tasks []RaceTask, winTaskID string) (RaceTask, error) {
	if winTaskID != "" {
		for _, t := range tasks {
			if t.ID != winTaskID {
				continue
			}
			if t.Scope != ScopeGroupTeam {
				return RaceTask{}, ErrWinTaskNotFound
			}
			if t.MetricType == MetricCheckpoint {
				return RaceTask{}, ErrGroupTaskUnsupported
			}
			return t, nil
		}
		return RaceTask{}, ErrWinTaskNotFound
	}
	for _, t := range tasks {
		if t.Scope == ScopeGroupTeam && t.MetricType != MetricCheckpoint {
			return t, nil
		}
	}
	return RaceTask{}, ErrNoGroupTask
}

// groupsForTask 該任務適用的分組：GroupID==""＝所有分組共同（比照 progress.go/settlement.go 的
// group_team scope 語意）；否則僅該分組。
func groupsForTask(groups []RaceGroup, t RaceTask) []RaceGroup {
	if t.GroupID == "" {
		return groups
	}
	for _, g := range groups {
		if g.ID == t.GroupID {
			return []RaceGroup{g}
		}
	}
	return nil
}

// determineWinningGroups 依 win_rule 對指定任務算出獲勝分組 ID 清單。acts 為賽事全場活動（未依分組拆分，
// 這裡自行依 GroupID 建索引），純計算、不碰 DB。
func determineWinningGroups(groups []RaceGroup, acts []progAct, t RaceTask, winRule string) ([]string, error) {
	applicable := groupsForTask(groups, t)
	if len(applicable) == 0 {
		return nil, ErrNoGroupTask
	}
	byGroup := map[string][]progAct{}
	for _, a := range acts {
		if a.GroupID != "" {
			byGroup[a.GroupID] = append(byGroup[a.GroupID], a)
		}
	}
	aggs := make([]groupAgg, 0, len(applicable))
	for _, g := range applicable {
		aggs = append(aggs, groupAgg{groupID: g.ID, acts: byGroup[g.ID]})
	}
	if winRule == WinRuleFirstToTarget {
		if t.TargetValue == nil || *t.TargetValue <= 0 {
			return nil, ErrGroupTaskUnsupported
		}
		return winningGroupsFirstToTarget(aggs, t)
	}
	return winningGroupsHighestMetric(aggs, t)
}

// shuffleSelect 用 crypto/rand 做 Fisher-Yates 洗牌後取前 n 筆（n 大於等於池大小時取全部）。抽獎資格池
// 是應用層算好的複雜結果（完賽判定＋分組過濾＋排除先前中獎者，SQL 端做不到），因此隨機挑選在 Go 端做；
// 用 crypto/rand 而非 math/rand，避免可預測種子被猜測（隨機性等級比照 personal_reward.go 用 SQL
// random() 的用意，只是換了層級）。
func shuffleSelect[T any](pool []T, n int) []T {
	cp := append([]T(nil), pool...)
	for i := len(cp) - 1; i > 0; i-- {
		j := i
		if jBig, err := rand.Int(rand.Reader, big.NewInt(int64(i+1))); err == nil {
			j = int(jBig.Int64())
		}
		cp[i], cp[j] = cp[j], cp[i]
	}
	if n > len(cp) {
		n = len(cp)
	}
	if n < 0 {
		n = 0
	}
	return cp[:n]
}

// --- Service 層：組裝完賽者池 + 獲勝組判定 ---

// CreateRewardDraw 建立一次抽獎批次：完賽者池（computeFinishers）→ scope 過濾（winning_group 需先
// 判定獲勝組）→ 交給 repo 層在交易＋advisory lock 內排除先前批次中獎者、洗牌抽 n、落地。
// personal 模式賽事一律拒絕（回 ErrRewardDrawNotApplicable），請改用 personal_reward.go 的
// Service.DrawRewardWinners（同名但作用於 registrations.reward_status 舊制，函式簽章不同、不衝突）。
func (s *Service) CreateRewardDraw(ctx context.Context, raceID string, req DrawRewardRequest, drawnBy string) (*RewardDraw, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil {
		return nil, ErrRaceNotFound
	}
	if race.EventMode == "personal" {
		return nil, ErrRewardDrawNotApplicable
	}
	if req.Scope != RewardScopeAllFinishers && req.Scope != RewardScopeWinningGroup {
		return nil, ErrInvalidScope
	}
	if req.Scope == RewardScopeWinningGroup && req.WinRule != WinRuleHighestMetric && req.WinRule != WinRuleFirstToTarget {
		return nil, ErrInvalidWinRule
	}
	if req.WinnerCount <= 0 {
		return nil, ErrInvalidWinnerCount
	}

	// 完賽者池：抽獎不需要毫秒級一致性（人工觸發、低頻操作），這裡用未快取版本讀取最新完賽者，不透過
	// getFinishersCached 的短 TTL 快取（見 leaderboard.go 對該快取的說明——快取只給前台排行榜輪詢用）。
	finishers, _, err := s.repo.computeFinishers(ctx, raceID)
	if err != nil {
		return nil, err
	}

	var winTaskID string
	var winningGroupIDs []string
	if req.Scope == RewardScopeWinningGroup {
		tasks, err := s.repo.GetRaceTasks(ctx, raceID)
		if err != nil {
			return nil, err
		}
		task, err := resolveWinTask(tasks, req.WinTaskID)
		if err != nil {
			return nil, err
		}
		winTaskID = task.ID
		groups, err := s.repo.GetGroups(ctx, raceID)
		if err != nil {
			return nil, err
		}
		acts, err := s.repo.LoadRaceActivities(ctx, raceID)
		if err != nil {
			return nil, err
		}
		winningGroupIDs, err = determineWinningGroups(groups, acts, task, req.WinRule)
		if err != nil {
			return nil, err
		}
	}

	winGroupSet := make(map[string]bool, len(winningGroupIDs))
	for _, g := range winningGroupIDs {
		winGroupSet[g] = true
	}
	pool := make([]finisher, 0, len(finishers))
	for _, f := range finishers {
		if req.Scope == RewardScopeWinningGroup && !winGroupSet[f.groupID] {
			continue
		}
		pool = append(pool, f)
	}

	return s.repo.DrawRaceRewardWinners(ctx, raceID, req, winTaskID, winningGroupIDs, pool, drawnBy)
}

// ListRewardDraws 該賽事所有抽獎批次（含各自中獎名單），依 drawn_at DESC（最新批次在前）。
func (s *Service) ListRewardDraws(ctx context.Context, raceID string) ([]RewardDraw, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil {
		return nil, ErrRaceNotFound
	}
	if race.EventMode == "personal" {
		return nil, ErrRewardDrawNotApplicable
	}
	return s.repo.ListRaceRewardDraws(ctx, raceID)
}

// UpdateRewardWinner 設定單筆中獎紀錄的發放狀態/備註（比照 personal 模式 UpdateRewardCompletion 的欄位
// 語意，''=中獎待發｜fulfilled=已發放；中獎本身已定案，沒有舊制那個「未中選」的中間態）。
func (s *Service) UpdateRewardWinner(ctx context.Context, winnerID, status, note string) error {
	return s.repo.UpdateRewardWinner(ctx, winnerID, status, note)
}

// --- Repository 層 ---

// existingWinnerUserIDs 該賽事先前所有批次已中獎的 user_id 集合（exclude_prior 用）
func (r *Repository) existingWinnerUserIDs(ctx context.Context, db pgQueryer, raceID string) (map[string]bool, error) {
	rows, err := db.Query(ctx, `SELECT DISTINCT user_id::text FROM race_reward_winners WHERE race_id=$1`, raceID)
	if err != nil {
		return nil, fmt.Errorf("existing reward winners: %w", err)
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		set[uid] = true
	}
	return set, rows.Err()
}

// DrawRaceRewardWinners 在交易內完成排除＋抽選＋落地。pool 已由 Service.DrawRewardWinners 依
// scope/winning_group 算好，這裡只管「（可選）排除先前批次中獎者 → 洗牌抽 n → 寫入 draw+winners」。
//
// 併發防護：交易一開始就對 race 取 advisory lock（pg_advisory_xact_lock，交易結束自動釋放），把同一賽事
// 的併發抽獎請求序列化——第二個請求會等第一個 COMMIT 之後才繼續，確保「讀先前批次中獎者(exclude_prior)」
// 與「寫入這次批次」在同一個邏輯窗口內是原子的，不會出現兩個幾乎同時送出的批次各自讀到舊快照、
// 同時把同一人算進兩個批次的情形。選 advisory lock 而非比照 personal_reward.go 對候選列
// FOR UPDATE SKIP LOCKED 的理由：這裡的資格池是應用層先算好的複雜結果（完賽判定＋分組過濾），不是單一
// 資料表的候選列集合，沒有「一批 row 可以直接鎖」的對象，改用鎖 race 這個邏輯單位序列化整個決策過程。
func (r *Repository) DrawRaceRewardWinners(ctx context.Context, raceID string, req DrawRewardRequest, winTaskID string, winningGroupIDs []string, pool []finisher, drawnBy string) (*RewardDraw, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) // Commit 後為 no-op

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "reward_draw:"+raceID); err != nil {
		return nil, fmt.Errorf("advisory lock: %w", err)
	}

	finalPool := pool
	if req.ExcludePrior {
		excluded, err := r.existingWinnerUserIDs(ctx, tx, raceID)
		if err != nil {
			return nil, err
		}
		filtered := make([]finisher, 0, len(pool))
		for _, f := range pool {
			if !excluded[f.userID] {
				filtered = append(filtered, f)
			}
		}
		finalPool = filtered
	}
	if len(finalPool) == 0 {
		return nil, ErrEmptyRewardPool
	}

	picked := shuffleSelect(finalPool, req.WinnerCount)

	var drawID string
	var drawnAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO race_reward_draws
			(race_id, title, scope, win_rule, win_task_id, winner_count, exclude_prior, winning_group_ids, pool_size, drawn_by)
		VALUES ($1,$2,$3,$4,NULLIF($5,'')::uuid,$6,$7,$8,$9,NULLIF($10,'')::uuid)
		RETURNING id, drawn_at`,
		raceID, req.Title, req.Scope, req.WinRule, winTaskID, req.WinnerCount, req.ExcludePrior,
		winningGroupIDs, len(finalPool), drawnBy,
	).Scan(&drawID, &drawnAt)
	if err != nil {
		return nil, fmt.Errorf("insert reward draw: %w", err)
	}

	winners := make([]RewardWinnerRow, 0, len(picked))
	for _, f := range picked {
		var wid string
		var createdAt time.Time
		if err := tx.QueryRow(ctx, `
			INSERT INTO race_reward_winners (draw_id, race_id, user_id, group_id)
			VALUES ($1,$2,$3,NULLIF($4,'')::uuid)
			RETURNING id, created_at`,
			drawID, raceID, f.userID, f.groupID,
		).Scan(&wid, &createdAt); err != nil {
			return nil, fmt.Errorf("insert reward winner: %w", err)
		}
		winners = append(winners, RewardWinnerRow{
			ID: wid, DrawID: drawID, UserID: f.userID, UserName: f.nickname, UserEmail: f.email,
			GroupID: f.groupID, GroupName: f.groupName, RewardStatus: "", CreatedAt: createdAt,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit reward draw: %w", err)
	}

	return &RewardDraw{
		ID: drawID, RaceID: raceID, Title: req.Title, Scope: req.Scope, WinRule: req.WinRule,
		WinTaskID: winTaskID, WinnerCount: req.WinnerCount, ExcludePrior: req.ExcludePrior,
		WinningGroupIDs: winningGroupIDs, PoolSize: len(finalPool), DrawnBy: drawnBy, DrawnAt: drawnAt,
		Winners: winners,
	}, nil
}

// ListRaceRewardDraws 該賽事所有抽獎批次＋各自中獎名單（兩次查詢：批次列表 + 依 draw_id IN 撈全部
// 中獎名單再分組掛回，避免 N+1）。
func (r *Repository) ListRaceRewardDraws(ctx context.Context, raceID string) ([]RewardDraw, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, title, scope, win_rule, COALESCE(win_task_id::text,''), winner_count, exclude_prior,
		       COALESCE(winning_group_ids, '{}'), pool_size, COALESCE(drawn_by::text,''), drawn_at
		FROM race_reward_draws WHERE race_id=$1 ORDER BY drawn_at DESC`, raceID)
	if err != nil {
		return nil, fmt.Errorf("list reward draws: %w", err)
	}
	draws := []RewardDraw{}
	ids := []string{}
	for rows.Next() {
		var d RewardDraw
		if err := rows.Scan(&d.ID, &d.Title, &d.Scope, &d.WinRule, &d.WinTaskID, &d.WinnerCount,
			&d.ExcludePrior, &d.WinningGroupIDs, &d.PoolSize, &d.DrawnBy, &d.DrawnAt); err != nil {
			rows.Close()
			return nil, err
		}
		d.RaceID = raceID
		d.Winners = []RewardWinnerRow{}
		draws = append(draws, d)
		ids = append(ids, d.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return draws, nil
	}

	byID := make(map[string]*RewardDraw, len(draws))
	for i := range draws {
		byID[draws[i].ID] = &draws[i]
	}

	wrows, err := r.db.Query(ctx, `
		SELECT w.id, w.draw_id, w.user_id, u.name, u.email, COALESCE(w.group_id::text,''), COALESCE(g.name,''),
		       w.reward_status, w.reward_note, w.reward_fulfilled_at, w.created_at
		FROM race_reward_winners w
		JOIN users u ON u.id = w.user_id
		LEFT JOIN race_groups g ON g.id = w.group_id
		WHERE w.draw_id = ANY($1)
		ORDER BY w.created_at`, ids)
	if err != nil {
		return nil, fmt.Errorf("list reward winners: %w", err)
	}
	defer wrows.Close()
	for wrows.Next() {
		var w RewardWinnerRow
		if err := wrows.Scan(&w.ID, &w.DrawID, &w.UserID, &w.UserName, &w.UserEmail, &w.GroupID, &w.GroupName,
			&w.RewardStatus, &w.RewardNote, &w.RewardFulfilledAt, &w.CreatedAt); err != nil {
			return nil, err
		}
		if d, ok := byID[w.DrawID]; ok {
			d.Winners = append(d.Winners, w)
		}
	}
	return draws, wrows.Err()
}

// UpdateRewardWinner 設定單筆中獎紀錄的 reward_status/reward_note；status='fulfilled' 時一併寫入
// reward_fulfilled_at=NOW()，改為非 fulfilled 時清 NULL。找不到列回 ErrRewardWinnerNotFound。
func (r *Repository) UpdateRewardWinner(ctx context.Context, id, status, note string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE race_reward_winners
		SET reward_status = $2,
		    reward_note = $3,
		    reward_fulfilled_at = CASE WHEN $2 = 'fulfilled' THEN NOW() ELSE NULL END
		WHERE id = $1`, id, status, note)
	if err != nil {
		return fmt.Errorf("update reward winner: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRewardWinnerNotFound
	}
	return nil
}
