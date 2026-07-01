package race

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/promo"
)

var (
	ErrRaceNotFound     = errors.New("race not found")
	ErrAlreadyRegistered = errors.New("already registered for this race")
	ErrRegistrationClosed = errors.New("registration is not open")
	ErrSoldOut          = errors.New("race is sold out")
	ErrInvalidDistance  = errors.New("invalid distance for this race")
	ErrRaceHasRegistrations = errors.New("race has registrations and cannot be deleted")
	ErrGroupNotFound       = errors.New("group not found in this race")
	ErrGroupFull           = errors.New("group is full")
	ErrGroupRequired       = errors.New("group selection is required")
	ErrMissingRequiredField = errors.New("missing required participant field")
	ErrGroupRestriction    = errors.New("participant does not meet group restriction")
	ErrNoGroups            = errors.New("race has no groups")
	ErrAddonNotFound       = errors.New("addon not found")
	ErrAddonLimit          = errors.New("addon quantity exceeds per-user limit")
	ErrAddonSoldOut        = errors.New("addon sold out")
	ErrOrderNotFound        = errors.New("order not found")
	ErrRegistrationNotFound = errors.New("registration not found")
	ErrRegistrationPaused   = errors.New("此賽事目前暫停報名")
	ErrGroupKeyWrong        = errors.New("跑團鑰匙錯誤")
	ErrTeamGroupsDisabled   = errors.New("此賽事未開放跑團分組申請")
	ErrTeamGroupName        = errors.New("請輸入跑團分組名稱")
	ErrTeamGroupNotAllowed  = errors.New("您的帳號未開放建立跑團分組")
	ErrTaskModuleName       = errors.New("請輸入任務模組名稱")
	ErrTaskModuleNotFound   = errors.New("task module not found")
)

type Service struct {
	repo  *Repository
	rdb   *redis.Client
	promo *promo.Service
}

func NewService(repo *Repository, rdb *redis.Client, promoSvc *promo.Service) *Service {
	return &Service{repo: repo, rdb: rdb, promo: promoSvc}
}

// List 回傳賽事列表（admin 用，含全部 control_status，填入 display_status）
func (s *Service) List(ctx context.Context, status string) ([]*Race, error) {
	races, err := s.repo.List(ctx, status)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for _, r := range races {
		r.FillDisplay(now)
	}
	return races, nil
}

// ListPublic 前台賽事列表：排除 closed/hidden，testing 只給白名單 email，並填入 display_status。
func (s *Service) ListPublic(ctx context.Context, userID string) ([]*Race, error) {
	races, err := s.repo.List(ctx, "") // 不依舊 status 欄位過濾
	if err != nil {
		return nil, err
	}
	email, _ := s.repo.GetUserEmail(ctx, userID)
	now := time.Now()
	out := []*Race{}
	for _, r := range races {
		switch r.ControlStatus {
		case "closed", "hidden":
			continue
		case "testing":
			if email == "" {
				continue
			}
			ok, err := s.repo.IsEmailWhitelisted(ctx, r.ID, email)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		r.FillDisplay(now)
		out = append(out, r)
	}
	return out, nil
}

// GetUserRegistrations 取得使用者所有報名的精簡狀態（賽事列表用）
func (s *Service) GetUserRegistrations(ctx context.Context, userID string) (map[string]MyRegLite, error) {
	return s.repo.GetUserRegistrations(ctx, userID)
}

// --- 全域預設測試白名單 ---

func (s *Service) ListDefaultWhitelist(ctx context.Context) ([]string, error) {
	return s.repo.ListDefaultWhitelist(ctx)
}
func (s *Service) AddDefaultWhitelist(ctx context.Context, email string) error {
	return s.repo.AddDefaultWhitelist(ctx, email)
}
func (s *Service) RemoveDefaultWhitelist(ctx context.Context, email string) error {
	return s.repo.RemoveDefaultWhitelist(ctx, email)
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

// GetPublicDetail 取得公開賽事詳情（含分組/加購/物資）+ 使用者報名狀態。
func (s *Service) GetPublicDetail(ctx context.Context, raceID, userID string) (*RaceDetail, *Registration, error) {
	detail, err := s.repo.GetDetail(ctx, raceID)
	if err != nil {
		return nil, nil, err
	}
	if detail == nil || detail.ReviewStatus != "approved" {
		return nil, nil, ErrRaceNotFound
	}
	// 可見性：closed 全擋；testing 僅白名單 email（hidden 有連結可進）
	if detail.ControlStatus == "closed" {
		return nil, nil, ErrRaceNotFound
	}
	if detail.ControlStatus == "testing" {
		email, _ := s.repo.GetUserEmail(ctx, userID)
		if email == "" {
			return nil, nil, ErrRaceNotFound
		}
		ok, err := s.repo.IsEmailWhitelisted(ctx, raceID, email)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, ErrRaceNotFound
		}
	}
	detail.FillDisplay(time.Now())

	// 賽事已結束 → 背景自動結算 EXP（idempotent；已結算會便宜跳過、失敗則下次讀取重試）
	if detail.DisplayStatus == "ended" {
		raceID := detail.ID
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = s.SettleRaceEXP(bg, raceID, false)
		}()
	}

	// 安全：公開回傳一律不洩漏跑團鑰匙明碼（前台只需 requires_key 旗標）
	for i := range detail.Groups {
		detail.Groups[i].GroupKey = ""
	}

	var reg *Registration
	if userID != "" {
		reg, err = s.repo.GetRegistration(ctx, userID, raceID)
		if err != nil {
			return nil, nil, err
		}
	}
	return detail, reg, nil
}

// CreateTeamGroup 前台跑團成員自建分組（限 competition 且已開放 allow_team_groups、報名期間內）。
func (s *Service) CreateTeamGroup(ctx context.Context, req *CreateTeamGroupRequest) (*RaceGroup, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.GroupKey = strings.TrimSpace(req.GroupKey)
	if req.Name == "" {
		return nil, ErrTeamGroupName
	}
	race, err := s.repo.GetByID(ctx, req.RaceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}
	if race.EventMode != "competition" || !race.AllowTeamGroups {
		return nil, ErrTeamGroupsDisabled
	}
	// 權限：僅開放的會員可建立
	allowed, err := s.repo.UserCanCreateTeamGroup(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrTeamGroupNotAllowed
	}
	// testing 模式僅白名單可建立；closed 全擋
	switch race.ControlStatus {
	case "closed":
		return nil, ErrRaceNotFound
	case "testing":
		email, _ := s.repo.GetUserEmail(ctx, req.UserID)
		if email == "" {
			return nil, ErrRaceNotFound
		}
		ok, err := s.repo.IsEmailWhitelisted(ctx, req.RaceID, email)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrRaceNotFound
		}
	}
	// 僅報名期間可建立
	if _, canReg := race.ComputeDisplay(time.Now()); !canReg {
		return nil, ErrRegistrationClosed
	}
	return s.repo.CreateTeamGroup(ctx, *req)
}

// CanUserCreateTeamGroup 該使用者於此賽事是否可建立跑團分組（前台顯示按鈕用）
func (s *Service) CanUserCreateTeamGroup(ctx context.Context, userID string, race *Race) bool {
	if userID == "" || race == nil || race.EventMode != "competition" || !race.AllowTeamGroups {
		return false
	}
	ok, _ := s.repo.UserCanCreateTeamGroup(ctx, userID)
	return ok
}

// Register 處理前台報名（分組 + 加購 + 訂單 + 個資回填）。
func (s *Service) Register(ctx context.Context, req *RegisterRequest) (*RegisterResult, error) {
	race, err := s.repo.GetByID(ctx, req.RaceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}
	// control_status 守門
	switch race.ControlStatus {
	case "closed":
		return nil, ErrRaceNotFound
	case "paused":
		return nil, ErrRegistrationPaused
	case "suspended":
		return nil, ErrRegistrationClosed
	case "testing":
		email, _ := s.repo.GetUserEmail(ctx, req.UserID)
		if email == "" {
			return nil, ErrRaceNotFound
		}
		ok, err := s.repo.IsEmailWhitelisted(ctx, req.RaceID, email)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrRaceNotFound
		}
	}
	// 時間規則：非「報名中」不可報名
	if _, canReg := race.ComputeDisplay(time.Now()); !canReg {
		return nil, ErrRegistrationClosed
	}

	// 重複報名先擋（交易內 UNIQUE 仍是最終保證）
	if existing, err := s.repo.GetRegistration(ctx, req.UserID, req.RaceID); err != nil {
		return nil, err
	} else if existing != nil {
		return nil, ErrAlreadyRegistered
	}

	groups, err := s.repo.GetGroups(ctx, req.RaceID)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, ErrNoGroups
	}

	// 決定分組
	var chosen *RaceGroup
	revealed := true
	if race.EventMode == "faction_battle" {
		chosen = pickBalancedGroup(groups) // 隨機/平衡指派
		revealed = false                   // 賽事當天才公布
	} else {
		if req.GroupID == "" {
			return nil, ErrGroupRequired
		}
		for i := range groups {
			if groups[i].ID == req.GroupID {
				chosen = &groups[i]
				break
			}
		}
		if chosen == nil {
			return nil, ErrGroupNotFound
		}
	}

	// 必填欄位驗證
	if err := validateRequiredFields(race.RequiredFields, req.Participant); err != nil {
		return nil, err
	}
	// 分組性別/年齡限制
	if err := validateGroupRestriction(chosen, req.Participant); err != nil {
		return nil, err
	}

	distance := 0
	if chosen.TargetDistanceKm != nil {
		distance = int(*chosen.TargetDistanceKm)
	}

	return s.repo.RegisterWithOrder(ctx, RegisterTxInput{
		UserID:        req.UserID,
		RaceID:        req.RaceID,
		GroupID:       chosen.ID,
		GroupKey:      strings.TrimSpace(req.GroupKey),
		EntryFee:      race.EntryFee,
		GroupRevealed: revealed,
		Distance:      distance,
		Addons:        req.Addons,
		Participant:   req.Participant,
		PromoCode:     strings.TrimSpace(req.PromoCode),
	})
}

// QuotePromo 報名前試算優惠序號折抵（不寫入）。序號無效時回 Valid=false + Reason。
func (s *Service) QuotePromo(ctx context.Context, raceID, userID, code string, addons []AddonSelection) (*PromoQuote, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}

	p, err := s.promo.ValidateForRace(ctx, code, raceID, userID)
	if err != nil {
		return &PromoQuote{Valid: false, Reason: err.Error()}, nil
	}

	discount := promo.DiscountCents(p, race.EntryFee)
	addonsTotal, err := s.addonsTotal(ctx, raceID, addons)
	if err != nil {
		return nil, err
	}
	payable := race.EntryFee - discount
	if payable < 0 {
		payable = 0
	}
	payable += addonsTotal
	return &PromoQuote{
		Valid:         true,
		Code:          p.Code,
		DiscountCents: discount,
		PayableCents:  payable,
		Free:          payable < 50,
	}, nil
}

// addonsTotal 依選購計算加購總額（分）
func (s *Service) addonsTotal(ctx context.Context, raceID string, sel []AddonSelection) (int, error) {
	if len(sel) == 0 {
		return 0, nil
	}
	addons, err := s.repo.GetAddons(ctx, raceID)
	if err != nil {
		return 0, err
	}
	priceByID := map[string]int{}
	for _, a := range addons {
		priceByID[a.ID] = a.PriceCents
	}
	total := 0
	for _, a := range sel {
		if a.Qty > 0 {
			total += priceByID[a.AddonID] * a.Qty
		}
	}
	return total, nil
}

// pickBalancedGroup 從分組中挑人數最少者（同最少則取第一個），用於分組對抗隨機指派
func pickBalancedGroup(groups []RaceGroup) *RaceGroup {
	idx := 0
	for i := range groups {
		if groups[i].SlotsTaken < groups[idx].SlotsTaken {
			idx = i
		}
	}
	return &groups[idx]
}

func validateRequiredFields(required []string, p ParticipantInfo) error {
	get := map[string]string{
		"real_name": p.RealName, "nickname": p.Nickname, "phone": p.Phone,
		"address": p.Address, "birthday": p.Birthday, "gender": p.Gender,
	}
	for _, f := range required {
		if strings.TrimSpace(get[f]) == "" {
			return fmt.Errorf("%w: %s", ErrMissingRequiredField, f)
		}
	}
	return nil
}

func validateGroupRestriction(g *RaceGroup, p ParticipantInfo) error {
	if g.GenderLimit != "" && g.GenderLimit != "any" {
		if p.Gender != g.GenderLimit {
			return fmt.Errorf("%w: gender", ErrGroupRestriction)
		}
	}
	if g.AgeMin != nil || g.AgeMax != nil {
		age, ok := ageFromBirthday(p.Birthday)
		if !ok {
			return fmt.Errorf("%w: birthday required", ErrGroupRestriction)
		}
		if g.AgeMin != nil && age < *g.AgeMin {
			return fmt.Errorf("%w: age below min", ErrGroupRestriction)
		}
		if g.AgeMax != nil && age > *g.AgeMax {
			return fmt.Errorf("%w: age above max", ErrGroupRestriction)
		}
	}
	return nil
}

// ageFromBirthday 由 YYYY-MM-DD 算現在年齡
func ageFromBirthday(birthday string) (int, bool) {
	t, err := time.Parse("2006-01-02", birthday)
	if err != nil {
		return 0, false
	}
	now := time.Now()
	age := now.Year() - t.Year()
	if now.YearDay() < t.YearDay() {
		age--
	}
	return age, true
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

// SetCertificateBg 設定完賽證明底圖（admin 用）
func (s *Service) SetCertificateBg(ctx context.Context, raceID, url string) error {
	return s.repo.SetCertificateBg(ctx, raceID, url)
}

// SetRankDisplay 設定排行榜顯示（admin 用）
func (s *Service) SetRankDisplay(ctx context.Context, raceID string, dist, time bool) error {
	return s.repo.SetRankDisplay(ctx, raceID, dist, time)
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
	validTaskScope   = map[string]bool{
		ScopeRaceCollective: true, ScopeGroupTeam: true, ScopeGroupIndividual: true,
	}
)

// validateTaskMetric 驗證任務指標與其數值（threshold 需 target>0；range 需 lo/hi 且 lo<=hi）。
func validateTaskMetric(metric string, target, lo, hi *float64) error {
	spec, ok := MetricCatalog[metric]
	if !ok {
		return fmt.Errorf("invalid metric_type: %s", metric)
	}
	switch spec.Kind {
	case MetricThreshold:
		if target == nil || *target <= 0 {
			return fmt.Errorf("metric %s requires positive target_value", metric)
		}
	case MetricRange:
		if lo == nil || hi == nil {
			return fmt.Errorf("metric %s requires range_lo and range_hi", metric)
		}
		if *lo > *hi {
			return fmt.Errorf("metric %s: range_lo must be <= range_hi", metric)
		}
	}
	return nil
}

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
	for i := range req.Tasks {
		t := &req.Tasks[i]
		if !validTaskScope[t.Scope] {
			return fmt.Errorf("task %d: invalid scope", i)
		}
		if err := validateTaskMetric(t.MetricType, t.TargetValue, t.RangeLo, t.RangeHi); err != nil {
			return fmt.Errorf("task %d: %w", i, err)
		}
		if t.Title == "" {
			t.Title = MetricCatalog[t.MetricType].Label
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

// --- 任務模組（全站共用範本）---

func validateModule(m *TaskModule) error {
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" {
		return ErrTaskModuleName
	}
	for i := range m.Items {
		it := &m.Items[i]
		if err := validateTaskMetric(it.MetricType, it.TargetValue, it.RangeLo, it.RangeHi); err != nil {
			return fmt.Errorf("item %d: %w", i, err)
		}
		if it.Title == "" {
			it.Title = MetricCatalog[it.MetricType].Label
		}
	}
	return nil
}

func (s *Service) ListTaskModules(ctx context.Context) ([]TaskModule, error) {
	return s.repo.ListTaskModules(ctx)
}

func (s *Service) GetTaskModule(ctx context.Context, id string) (*TaskModule, error) {
	return s.repo.GetTaskModule(ctx, id)
}

func (s *Service) CreateTaskModule(ctx context.Context, m *TaskModule) (*TaskModule, error) {
	if err := validateModule(m); err != nil {
		return nil, err
	}
	return s.repo.CreateTaskModule(ctx, m)
}

func (s *Service) UpdateTaskModule(ctx context.Context, id string, m *TaskModule) (*TaskModule, error) {
	if err := validateModule(m); err != nil {
		return nil, err
	}
	updated, err := s.repo.UpdateTaskModule(ctx, id, m)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, ErrTaskModuleNotFound
	}
	return updated, nil
}

func (s *Service) DeleteTaskModule(ctx context.Context, id string) error {
	ok, err := s.repo.DeleteTaskModule(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrTaskModuleNotFound
	}
	return nil
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

// AdminListSignups 列出某賽事報名（admin 用，舊版相容）
func (s *Service) AdminListSignups(ctx context.Context, raceID string) ([]*Registration, error) {
	return s.repo.ListRegistrations(ctx, raceID)
}

// --- 後台報名 / 訂單管理 ---

func (s *Service) ListSignups(ctx context.Context, raceID, q string) ([]SignupRow, error) {
	return s.repo.ListSignups(ctx, raceID, q)
}

// ListRaceGroups 後台報名管理用：取分組（含名額上限/已用），並清掉鑰匙明碼。
func (s *Service) ListRaceGroups(ctx context.Context, raceID string) ([]RaceGroup, error) {
	gs, err := s.repo.GetGroups(ctx, raceID)
	if err != nil {
		return nil, err
	}
	for i := range gs {
		gs[i].GroupKey = ""
	}
	return gs, nil
}

// ChangeSignupGroup 後台調整某報名的分組（額滿擋下）。
func (s *Service) ChangeSignupGroup(ctx context.Context, regID, groupID string) error {
	return s.repo.ChangeSignupGroup(ctx, regID, groupID)
}

func (s *Service) ListOrders(ctx context.Context, raceID, status string, limit, offset int) ([]OrderRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListOrders(ctx, raceID, status, limit, offset)
}

func (s *Service) GetOrderDetail(ctx context.Context, orderID string) (*OrderDetail, error) {
	d, err := s.repo.GetOrderDetail(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, ErrOrderNotFound
	}
	return d, nil
}

func (s *Service) MarkOrderPaid(ctx context.Context, orderID, paymentRef string) error {
	return s.repo.MarkOrderPaid(ctx, orderID, paymentRef)
}

func (s *Service) MarkRegistrationPaid(ctx context.Context, regID string) error {
	return s.repo.MarkRegistrationPaid(ctx, regID)
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
