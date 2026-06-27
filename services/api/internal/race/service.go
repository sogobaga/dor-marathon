package race

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrRaceNotFound     = errors.New("race not found")
	ErrAlreadyRegistered = errors.New("already registered for this race")
	ErrRegistrationClosed = errors.New("registration is not open")
	ErrSoldOut          = errors.New("race is sold out")
	ErrInvalidDistance  = errors.New("invalid distance for this race")
	ErrRaceHasRegistrations = errors.New("race has registrations and cannot be deleted")
)

type Service struct {
	repo *Repository
	rdb  *redis.Client
}

func NewService(repo *Repository, rdb *redis.Client) *Service {
	return &Service{repo: repo, rdb: rdb}
}

// List 回傳賽事列表，附帶 Redis 剩餘名額
func (s *Service) List(ctx context.Context, status string) ([]*Race, error) {
	return s.repo.List(ctx, status)
}

// GetDetail 回傳賽事詳情 + 使用者的報名狀態
func (s *Service) GetDetail(ctx context.Context, raceID, userID string) (*Race, *Registration, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, nil, err
	}
	if race == nil {
		return nil, nil, ErrRaceNotFound
	}

	var reg *Registration
	if userID != "" {
		reg, err = s.repo.GetRegistration(ctx, userID, raceID)
		if err != nil {
			return nil, nil, err
		}
	}

	return race, reg, nil
}

// Register 處理報名邏輯
func (s *Service) Register(ctx context.Context, userID, raceID string, distance int) (*Registration, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil {
		return nil, ErrRaceNotFound
	}
	if race.Status != "open" {
		return nil, ErrRegistrationClosed
	}

	// 確認距離合法
	validDist := false
	for _, d := range race.Distances {
		if d == distance {
			validDist = true
			break
		}
	}
	if !validDist {
		return nil, ErrInvalidDistance
	}

	// 確認未重複報名
	existing, err := s.repo.GetRegistration(ctx, userID, raceID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrAlreadyRegistered
	}

	// 名額控制：Redis DECR 原子操作（slots_total > 0 才做）
	if race.SlotsTotal > 0 {
		slotsKey := "race:" + raceID + ":slots"
		// 若 Redis 尚未初始化此 key，先設定初始值
		if s.rdb.Exists(ctx, slotsKey).Val() == 0 {
			s.rdb.Set(ctx, slotsKey, race.SlotsTotal, 0)
		}
		remaining := s.rdb.Decr(ctx, slotsKey).Val()
		if remaining < 0 {
			s.rdb.Incr(ctx, slotsKey) // 歸還
			return nil, ErrSoldOut
		}
	}

	// 分配陣營（faction-type + random 模式）
	faction := ""
	if race.GroupType == "faction" && race.GroupMode == "random" && len(race.Config.Factions) > 0 {
		faction = s.assignFactionBalanced(ctx, raceID, race.Config.Factions)
	}

	// 寫入 DB
	reg := &Registration{
		UserID:   userID,
		RaceID:   raceID,
		Distance: distance,
		Faction:  faction,
		Status:   "pending",
		Amount:   race.EntryFee,
	}
	reg, err = s.repo.CreateRegistration(ctx, reg)
	if err != nil {
		return nil, fmt.Errorf("create registration: %w", err)
	}

	// Mock 付款：立即確認（真實整合時改為送金流後 webhook 回呼）
	if err := s.repo.ConfirmPayment(ctx, reg.ID); err != nil {
		return nil, fmt.Errorf("confirm payment: %w", err)
	}
	reg.Status = "paid"
	now := time.Now()
	reg.PaidAt = &now

	// 初始化 Redis 排行榜分數（0km 起跑）
	rankKey := "race:" + raceID + ":ranking"
	s.rdb.ZAddNX(ctx, rankKey, redis.Z{Score: 0, Member: userID})

	// 初始化陣營貢獻（若有陣營）
	if faction != "" {
		factionKmKey := "race:" + raceID + ":faction_km"
		s.rdb.HSetNX(ctx, factionKmKey, faction, 0)
	}

	return reg, nil
}

// GetLiveStatus 取得即時陣營分數
func (s *Service) GetLiveStatus(ctx context.Context, raceID string) (*LiveStatus, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil {
		return nil, ErrRaceNotFound
	}

	status := &LiveStatus{
		RaceID: raceID,
		Status: race.Status,
		DayNow: s.computeDayNow(race),
	}

	// 陣營分數（從 Redis）
	if race.GroupType == "faction" && len(race.Config.Factions) > 0 {
		factionKmKey := "race:" + raceID + ":faction_km"
		kmMap := s.rdb.HGetAll(ctx, factionKmKey).Val()

		// 計算總 km
		totalKm := 0.0
		factionKms := make(map[string]float64, len(race.Config.Factions))
		for _, f := range race.Config.Factions {
			if v, ok := kmMap[f.ID]; ok {
				km, _ := strconv.ParseFloat(v, 64)
				factionKms[f.ID] = km
				totalKm += km
			}
		}

		for _, f := range race.Config.Factions {
			km := factionKms[f.ID]
			pct := 0.0
			if totalKm > 0 {
				pct = km / totalKm * 100
			}
			status.Factions = append(status.Factions, FactionStatus{
				ID:       f.ID,
				Name:     f.Name,
				Color:    f.Color,
				TotalKm:  km,
				ScorePct: pct,
			})
		}
	}

	return status, nil
}

// GetRanking 取得排行榜（Redis ZSET，Top N）
func (s *Service) GetRanking(ctx context.Context, raceID string, limit int64) ([]*RankEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	rankKey := "race:" + raceID + ":ranking"
	// ZREVRANGE: 高分優先
	zs, err := s.rdb.ZRevRangeWithScores(ctx, rankKey, 0, limit-1).Result()
	if err != nil {
		return nil, fmt.Errorf("get ranking from redis: %w", err)
	}

	if len(zs) == 0 {
		return []*RankEntry{}, nil
	}

	// 批次取使用者資訊
	userIDs := make([]string, len(zs))
	for i, z := range zs {
		userIDs[i] = fmt.Sprint(z.Member)
	}
	handles, err := s.repo.GetUserHandles(ctx, userIDs)
	if err != nil {
		return nil, err
	}

	entries := make([]*RankEntry, len(zs))
	for i, z := range zs {
		uid := fmt.Sprint(z.Member)
		info := handles[uid]
		entries[i] = &RankEntry{
			Rank:       i + 1,
			UserID:     uid,
			Handle:     info[0],
			Name:       info[1],
			DistanceKm: z.Score / 1000, // 儲存時乘以 1000，還原
		}
	}

	return entries, nil
}

// UpdateRanking 更新 Redis 排行榜分數（activity upload 後呼叫）
func (s *Service) UpdateRanking(ctx context.Context, raceID, userID string, addKm float64) error {
	rankKey := "race:" + raceID + ":ranking"
	// ZINCRBY 原子增加分數
	return s.rdb.ZIncrBy(ctx, rankKey, addKm*1000, userID).Err()
}

// UpdateRaceStatus 更新賽事狀態（admin 用）
func (s *Service) UpdateRaceStatus(ctx context.Context, raceID, status string) error {
	return s.repo.UpdateStatus(ctx, raceID, status)
}

// UpdateRace 更新賽事可編輯欄位（admin 用）。status 留空則沿用原值。
func (s *Service) UpdateRace(ctx context.Context, raceID string, race *Race) (*Race, error) {
	existing, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrRaceNotFound
	}
	race.ID = raceID
	if race.Status == "" {
		race.Status = existing.Status
	}
	return s.repo.Update(ctx, race)
}

// CreateRace 建立新賽事（admin 用，直接 approved）
func (s *Service) CreateRace(ctx context.Context, race *Race) (*Race, error) {
	race.ReviewStatus = "approved"
	return s.repo.Create(ctx, race)
}

var (
	validEventModes  = map[string]bool{"general": true, "competition": true, "faction_battle": true}
	validGoalTypes   = map[string]bool{"cumulative": true, "distance": true}
	validGenderLimit = map[string]bool{"any": true, "male": true, "female": true}
	validSupplyKinds = map[string]bool{"race_pack": true, "finisher": true}
)

// normalizeRequest 套用預設值並驗證巢狀 payload（建立與更新共用）。
func normalizeRequest(req *CreateRaceRequest) error {
	if req.EventMode == "" {
		req.EventMode = "general"
	}
	if !validEventModes[req.EventMode] {
		return fmt.Errorf("invalid event_mode: %s", req.EventMode)
	}
	// 分組對抗 = 隨機分配；其餘 = 選手自選
	if req.EventMode == "faction_battle" {
		req.GroupMode = "random"
	} else if req.GroupMode == "" {
		req.GroupMode = "self"
	}
	if req.GroupType == "" {
		req.GroupType = "distance"
	}
	// goal_type 只在競賽模式有意義，其餘固定 distance
	if req.EventMode == "competition" {
		if req.GoalType == "" {
			req.GoalType = "distance"
		}
		if !validGoalTypes[req.GoalType] {
			return fmt.Errorf("invalid goal_type: %s", req.GoalType)
		}
	} else {
		req.GoalType = "distance"
	}

	for i := range req.Groups {
		g := &req.Groups[i]
		if g.Name == "" {
			return fmt.Errorf("group %d: name is required", i)
		}
		if g.GenderLimit == "" {
			g.GenderLimit = "any"
		}
		if !validGenderLimit[g.GenderLimit] {
			return fmt.Errorf("group %d: invalid gender_limit", i)
		}
	}
	for i := range req.Supplies {
		su := &req.Supplies[i]
		if su.Name == "" {
			return fmt.Errorf("supply %d: name is required", i)
		}
		if !validSupplyKinds[su.Kind] {
			return fmt.Errorf("supply %d: invalid kind", i)
		}
	}

	if req.Status == "" {
		req.Status = "soon"
	}
	// 同步 distances（沿用既有欄位；用各分組目標里程推導，至少給一筆避免 NOT NULL 空陣列）
	if len(req.Distances) == 0 {
		seen := map[int]bool{}
		for _, g := range req.Groups {
			if g.TargetDistanceKm != nil {
				d := int(*g.TargetDistanceKm)
				if d > 0 && !seen[d] {
					seen[d] = true
					req.Distances = append(req.Distances, d)
				}
			}
		}
		if len(req.Distances) == 0 {
			req.Distances = []int{0}
		}
	}
	return nil
}

// CreateRaceFull 建立含巢狀分組/加購/物資的賽事（後台新增賽事用）。
func (s *Service) CreateRaceFull(ctx context.Context, req *CreateRaceRequest) (*RaceDetail, error) {
	if err := normalizeRequest(req); err != nil {
		return nil, err
	}
	req.ReviewStatus = "approved"
	return s.repo.CreateWithChildren(ctx, req)
}

// UpdateRaceFull 更新含巢狀分組/加購/物資的賽事（後台編輯賽事用）。
func (s *Service) UpdateRaceFull(ctx context.Context, raceID string, req *CreateRaceRequest) (*RaceDetail, error) {
	existing, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrRaceNotFound
	}
	// 編輯時若未指定 status，沿用原值（須在 normalize 預設成 soon 之前判斷）
	statusSpecified := req.Status != ""
	if err := normalizeRequest(req); err != nil {
		return nil, err
	}
	if !statusSpecified {
		req.Status = existing.Status
	}
	return s.repo.UpdateWithChildren(ctx, raceID, req)
}

// GetRaceDetail 取得賽事 + 巢狀子資料（後台編輯載入用）
func (s *Service) GetRaceDetail(ctx context.Context, raceID string) (*RaceDetail, error) {
	detail, err := s.repo.GetDetail(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, ErrRaceNotFound
	}
	return detail, nil
}

// GetCompetitionRanking 取得競賽分組排行榜（兩個榜 + 使用者所屬分組名次）。
// 累積榜：總里程 DESC；完成時間榜：finish_total_s ASC（0=尚無紀錄，排最後）。
func (s *Service) GetCompetitionRanking(ctx context.Context, raceID, userID string) (*CompetitionRanking, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil {
		return nil, ErrRaceNotFound
	}

	standings, err := s.repo.GetStandings(ctx, raceID)
	if err != nil {
		return nil, err
	}

	// 累積里程榜：里程多者在前
	cumulative := make([]GroupStanding, len(standings))
	copy(cumulative, standings)
	sort.SliceStable(cumulative, func(i, j int) bool {
		return cumulative[i].TotalKm > cumulative[j].TotalKm
	})

	// 完成時間榜：時間少者在前，但 0（尚無紀錄）排到最後
	finish := make([]GroupStanding, len(standings))
	copy(finish, standings)
	sort.SliceStable(finish, func(i, j int) bool {
		a, b := finish[i].FinishTotalS, finish[j].FinishTotalS
		if a == 0 {
			return false
		}
		if b == 0 {
			return true
		}
		return a < b
	})

	// 記下每個分組在兩榜的名次（給 my_group 用）
	cumRankByGroup := make(map[string]int, len(cumulative))
	for i, g := range cumulative {
		cumRankByGroup[g.GroupID] = i + 1
	}
	finRankByGroup := make(map[string]int, len(finish))
	for i, g := range finish {
		finRankByGroup[g.GroupID] = i + 1
	}

	result := &CompetitionRanking{
		RaceID:       raceID,
		EventMode:    race.EventMode,
		GoalType:     race.GoalType,
		ByCumulative: toRanked(cumulative, 20),
		ByFinishTime: toRanked(finish, 20),
	}

	// 使用者所屬分組名次
	if userID != "" {
		gid, err := s.repo.GetUserGroupID(ctx, userID, raceID)
		if err != nil {
			return nil, err
		}
		if gid != "" {
			for _, g := range standings {
				if g.GroupID == gid {
					result.MyGroup = &MyGroupRank{
						GroupID:        g.GroupID,
						GroupName:      g.GroupName,
						CumulativeRank: cumRankByGroup[gid],
						FinishRank:     finRankByGroup[gid],
						TotalKm:        g.TotalKm,
					}
					break
				}
			}
		}
	}

	return result, nil
}

// toRanked 將排序後的成績轉成含名次、最多 limit 筆的榜單
func toRanked(sorted []GroupStanding, limit int) []StandingRank {
	if limit > len(sorted) {
		limit = len(sorted)
	}
	out := make([]StandingRank, limit)
	for i := 0; i < limit; i++ {
		out[i] = StandingRank{Rank: i + 1, GroupStanding: sorted[i]}
	}
	return out
}

// ListPresets 取得分組預設選單
func (s *Service) ListPresets(ctx context.Context) ([]GroupPreset, error) {
	return s.repo.ListPresets(ctx)
}

// CreatePreset 新增分組預設（後台擴充選單）
func (s *Service) CreatePreset(ctx context.Context, name string, distanceKm *float64) (*GroupPreset, error) {
	return s.repo.CreatePreset(ctx, name, distanceKm)
}

// CreateRaceWithReview 合作方提交賽事，指定審核狀態（pending）
func (s *Service) CreateRaceWithReview(ctx context.Context, race *Race, reviewStatus string) (*Race, error) {
	race.ReviewStatus = reviewStatus
	return s.repo.Create(ctx, race)
}

// DeleteRace 刪除賽事（admin 用）。有報名的賽事不可刪，其餘連同子資料一併移除。
func (s *Service) DeleteRace(ctx context.Context, raceID string) error {
	existing, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrRaceNotFound
	}
	n, err := s.repo.CountRegistrations(ctx, raceID)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrRaceHasRegistrations
	}
	return s.repo.Delete(ctx, raceID)
}

// UpdateFactionKm 更新陣營累積里程（activity upload 後呼叫）
func (s *Service) UpdateFactionKm(ctx context.Context, raceID, faction string, addKm float64) error {
	if faction == "" {
		return nil
	}
	factionKmKey := "race:" + raceID + ":faction_km"
	return s.rdb.HIncrByFloat(ctx, factionKmKey, faction, addKm).Err()
}

// --- helpers ---

// assignFactionBalanced 隨機分配但儘量維持各陣營人數平衡
func (s *Service) assignFactionBalanced(ctx context.Context, raceID string, factions []FactionDef) string {
	if len(factions) == 0 {
		return ""
	}
	// 取各陣營人數（Redis 記錄）
	countKey := "race:" + raceID + ":faction_count"
	counts := s.rdb.HGetAll(ctx, countKey).Val()

	minCount := int64(^uint64(0) >> 1) // MaxInt64
	minFaction := factions[0].ID

	for _, f := range factions {
		c := int64(0)
		if v, ok := counts[f.ID]; ok {
			c, _ = strconv.ParseInt(v, 10, 64)
		}
		if c < minCount {
			minCount = c
			minFaction = f.ID
		}
	}

	// 若各陣營人數相同，純隨機
	allEqual := true
	for _, f := range factions {
		c := int64(0)
		if v, ok := counts[f.ID]; ok {
			c, _ = strconv.ParseInt(v, 10, 64)
		}
		if c != minCount {
			allEqual = false
			break
		}
	}
	if allEqual {
		minFaction = factions[rand.Intn(len(factions))].ID
	}

	// 更新計數
	s.rdb.HIncrBy(ctx, countKey, minFaction, 1)
	return minFaction
}

// GetRegistrationForUser 取得使用者在某賽事的報名記錄（供其他模組呼叫）
func (s *Service) GetRegistrationForUser(ctx context.Context, userID, raceID string) (*Registration, error) {
	return s.repo.GetRegistration(ctx, userID, raceID)
}

// AdminListSignups 列出某賽事報名（admin 用）
func (s *Service) AdminListSignups(ctx context.Context, raceID string) ([]*Registration, error) {
	return s.repo.ListRegistrations(ctx, raceID)
}

// computeDayNow 計算目前是賽事第幾天（1-indexed，賽前為 0）
func (s *Service) computeDayNow(race *Race) int {
	if race.Status == "soon" || race.Status == "open" {
		return 0
	}
	if race.Status == "done" {
		return int(race.EndDate.Sub(race.StartDate).Hours()/24) + 1
	}
	day := int(time.Since(race.StartDate).Hours()/24) + 1
	maxDay := int(race.EndDate.Sub(race.StartDate).Hours()/24) + 1
	if day > maxDay {
		return maxDay
	}
	return day
}
