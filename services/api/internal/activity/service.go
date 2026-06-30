package activity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/race"
	"github.com/dor/api/internal/realtime"
)

const (
	minPaceSecPerKm = 120  // 2:00/km（最快合理速度）
	maxPaceSecPerKm = 1200 // 20:00/km（最慢合理速度）
	streamKey       = "activity_queue"
)

var (
	ErrInvalidPace     = errors.New("pace out of acceptable range (2:00–20:00 /km)")
	ErrInvalidDistance = errors.New("distance must be greater than 0.1 km")
	ErrFutureDate      = errors.New("recorded_at cannot be in the future")
	ErrNotRegistered   = errors.New("not registered for this race")
)

type Service struct {
	repo     *Repository
	raceSvc  *race.Service
	rdb      *redis.Client
	wsManager *realtime.Manager
}

func NewService(repo *Repository, raceSvc *race.Service, rdb *redis.Client, wsm *realtime.Manager) *Service {
	return &Service{
		repo:      repo,
		raceSvc:   raceSvc,
		rdb:       rdb,
		wsManager: wsm,
	}
}

// AdminAddMileage 後台模擬一筆里程活動（無賽事）：推入 stream，worker 寫入並發日常里程 EXP
func (s *Service) AdminAddMileage(ctx context.Context, userID string, distanceKm float64) error {
	if distanceKm <= 0 {
		return ErrInvalidDistance
	}
	paceS := 360 // 預設 6:00/km
	evt := ActivityEvent{
		UserID:     userID,
		DistanceKm: distanceKm,
		DurationS:  int(distanceKm * float64(paceS)),
		AvgPaceS:   paceS,
		RecordedAt: time.Now().Format(time.RFC3339),
	}
	b, _ := json.Marshal(evt)
	return s.rdb.XAdd(ctx, &redis.XAddArgs{Stream: streamKey, Values: map[string]any{"data": string(b)}}).Err()
}

// Upload 處理跑步資料上傳的完整流程：
//  1. 驗證資料
//  2. 立即更新 Redis 排行榜 + 陣營分數
//  3. 推送 Redis Streams（非同步 DB 寫入）
//  4. 廣播 WebSocket 推播
//  5. 評估任務完成
func (s *Service) Upload(ctx context.Context, userID string, req *UploadRequest) (*UploadResult, error) {
	// --- Step 1: 基本驗證 ---
	if req.DistanceKm < 0.1 {
		return nil, ErrInvalidDistance
	}

	recordedAt, err := time.Parse(time.RFC3339, req.RecordedAt)
	if err != nil {
		return nil, fmt.Errorf("invalid recorded_at format, use ISO8601: %w", err)
	}
	if recordedAt.After(time.Now().Add(5 * time.Minute)) { // 5 分鐘容許誤差
		return nil, ErrFutureDate
	}

	// 計算並驗證配速
	avgPaceS := int(float64(req.DurationS) / req.DistanceKm)
	if avgPaceS < minPaceSecPerKm || avgPaceS > maxPaceSecPerKm {
		return nil, ErrInvalidPace
	}

	// --- Step 2: 若指定賽事，確認報名狀態 ---
	var faction string
	var raceDetail *race.Race
	if req.RaceID != "" {
		var regInfo *race.Registration
		raceDetail, regInfo, err = s.raceSvc.GetDetail(ctx, req.RaceID, userID)
		if err != nil {
			return nil, err
		}
		if raceDetail == nil {
			return nil, race.ErrRaceNotFound
		}
		if regInfo == nil || regInfo.Status != "paid" {
			return nil, ErrNotRegistered
		}
		faction = regInfo.Faction
	}

	act := &Activity{
		UserID:     userID,
		RaceID:     req.RaceID,
		MissionDay: req.MissionDay,
		DistanceKm: req.DistanceKm,
		DurationS:  req.DurationS,
		AvgPaceS:   avgPaceS,
		RecordedAt: recordedAt,
	}

	// --- Step 3: 立即更新 Redis（高優先，排行榜即時性）---
	var rankUpdate *RankingUpdate
	if req.RaceID != "" {
		// 取舊排名
		oldRank := s.getRank(ctx, req.RaceID, userID)

		// 更新排行榜 ZSET
		if err := s.raceSvc.UpdateRanking(ctx, req.RaceID, userID, req.DistanceKm); err != nil {
			log.Error().Err(err).Msg("update ranking failed")
		}

		// 更新陣營累積里程
		if faction != "" {
			s.raceSvc.UpdateFactionKm(ctx, req.RaceID, faction, req.DistanceKm)
		}

		// 取新排名 + 累積里程
		newRank := s.getRank(ctx, req.RaceID, userID)
		totalKm := s.getTotalKm(ctx, req.RaceID, userID)
		rankUpdate = &RankingUpdate{
			OldRank: oldRank,
			NewRank: newRank,
			TotalKm: totalKm,
			AddedKm: req.DistanceKm,
		}
	}

	// --- Step 4: 推入 Redis Streams（非同步 DB 寫入，由 Worker 處理）---
	evt := ActivityEvent{
		UserID:     userID,
		RaceID:     req.RaceID,
		MissionDay: req.MissionDay,
		DistanceKm: req.DistanceKm,
		DurationS:  req.DurationS,
		AvgPaceS:   avgPaceS,
		RecordedAt: recordedAt.Format(time.RFC3339),
	}
	evtBytes, _ := json.Marshal(evt)
	s.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: map[string]any{"data": string(evtBytes)},
	})

	// --- Step 5: WebSocket 廣播（最新排行榜快照）---
	if req.RaceID != "" {
		go s.broadcastRankingUpdate(context.Background(), req.RaceID, userID, req.DistanceKm, faction)
	}

	// --- Step 6: 任務完成評估 ---
	var missionResult *MissionResult
	if req.RaceID != "" && req.MissionDay > 0 && raceDetail != nil {
		missionResult = s.evaluateMission(ctx, userID, req.RaceID, req.MissionDay, act, raceDetail)
	}

	result := &UploadResult{
		Activity:      act,
		MissionResult: missionResult,
		RankingUpdate: rankUpdate,
	}
	return result, nil
}

// ListByUser 取得使用者的活動記錄
func (s *Service) ListByUser(ctx context.Context, userID string, limit int) ([]*Activity, error) {
	return s.repo.ListByUser(ctx, userID, limit)
}

// ListByRace 取得使用者在某賽事的所有活動
func (s *Service) ListByRace(ctx context.Context, userID, raceID string) ([]*Activity, error) {
	return s.repo.ListByRace(ctx, userID, raceID)
}

// GetMissionStatus 取得使用者在某賽事的任務完成狀態
func (s *Service) GetMissionStatus(ctx context.Context, userID, raceID string) (map[int]int, error) {
	return s.repo.GetMissionCompletions(ctx, userID, raceID)
}

// --- 內部 helpers ---

// evaluateMission 根據活動資料評估任務是否完成，並記錄結果
func (s *Service) evaluateMission(ctx context.Context, userID, raceID string, day int, act *Activity, raceDetail *race.Race) *MissionResult {
	// 找任務定義
	var mission *race.MissionDef
	for i := range raceDetail.Config.Missions {
		if raceDetail.Config.Missions[i].Day == day {
			mission = &raceDetail.Config.Missions[i]
			break
		}
	}
	if mission == nil {
		return nil
	}

	result := &MissionResult{Day: day}

	// 是否已完成過（防重複）
	done, _ := s.repo.IsMissionDone(ctx, userID, raceID, day)
	if done {
		result.Completed = true
		return result
	}

	// 檢查基礎里程
	if act.DistanceKm < mission.BaseKm {
		return result // 未達基礎里程，任務未完成
	}

	// 配速驗證（若有配速要求）
	result.PaceValid = true
	if mission.PaceLo != "" && mission.PaceHi != "" {
		loS := parsePaceStr(mission.PaceLo)
		hiS := parsePaceStr(mission.PaceHi)
		if loS > 0 && hiS > 0 {
			// 注意：配速「低」表示速度較快（秒數少），「高」表示速度較慢（秒數多）
			// PaceLo = 最快（如 4:30/km = 270s）, PaceHi = 最慢（如 5:30/km = 330s）
			if act.AvgPaceS < loS || act.AvgPaceS > hiS {
				result.PaceValid = false
				return result // 配速不符合，任務未完成
			}
		}
	}

	// 計算超出里程（救援/額外功能）
	extraKm := act.DistanceKm - mission.BaseKm
	rescueCount := 0
	if mission.Type == "rescue" {
		rescueCount = int(math.Floor(extraKm))
		if day == 5 { // 救援日加倍（根據原型設計）
			rescueCount *= 2
		}
	}

	result.Completed = true
	result.ExtraKm = extraKm
	result.RescueCount = rescueCount

	// 記錄完成
	s.repo.RecordMissionCompletion(ctx, userID, raceID, day, "", rescueCount)

	return result
}

// broadcastRankingUpdate 廣播最新排行榜給 WebSocket 客戶端
func (s *Service) broadcastRankingUpdate(ctx context.Context, raceID, userID string, addedKm float64, faction string) {
	// 取最新 Top 10
	entries, err := s.raceSvc.GetRanking(ctx, raceID, 10)
	if err != nil {
		log.Error().Err(err).Msg("broadcast: get ranking failed")
		return
	}

	// 取最新陣營分數
	liveStatus, err := s.raceSvc.GetLiveStatus(ctx, raceID)
	if err != nil {
		log.Error().Err(err).Msg("broadcast: get live status failed")
		return
	}

	hub := s.wsManager.GetOrCreateHub(raceID)

	// 廣播排行榜更新
	hub.Publish(ctx, &realtime.Message{
		Type: "ranking_update",
		Payload: map[string]any{
			"top10":      entries,
			"updated_by": userID,
			"added_km":   addedKm,
		},
	})

	// 廣播陣營分數（若有陣營）
	if len(liveStatus.Factions) > 0 {
		hub.Publish(ctx, &realtime.Message{
			Type:    "faction_score",
			Payload: liveStatus.Factions,
		})
	}
}

func (s *Service) getRank(ctx context.Context, raceID, userID string) int {
	rank, err := s.rdb.ZRevRank(ctx, "race:"+raceID+":ranking", userID).Result()
	if err != nil {
		return -1
	}
	return int(rank) + 1 // 1-indexed
}

func (s *Service) getTotalKm(ctx context.Context, raceID, userID string) float64 {
	score, err := s.rdb.ZScore(ctx, "race:"+raceID+":ranking", userID).Result()
	if err != nil {
		return 0
	}
	return score / 1000
}

// parsePaceStr 將 "4:30" 格式轉為秒數（270）
func parsePaceStr(pace string) int {
	parts := strings.SplitN(pace, ":", 2)
	if len(parts) != 2 {
		return 0
	}
	min, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	sec, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return 0
	}
	return min*60 + sec
}
