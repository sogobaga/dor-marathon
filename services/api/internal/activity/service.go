package activity

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/race"
	"github.com/dor/api/internal/realtime"
)

const (
	minPaceSecPerKm = 120 // 2:00/km（最快合理速度）；仍供 gps.go computeRun 的防弊判定使用
	streamKey       = "activity_queue"
)

var (
	ErrInvalidDistance = errors.New("distance must be greater than 0.1 km")
)

// activityStreamXAdder 是本套件對 Redis 的唯一依賴面——只用 XAdd 把活動事件推進 Stream，交給
// worker 非同步消費落地 DB（見 services/worker/main.go）。用最小介面而非具體 *redis.Client，
// 讓 SaveGPSRun 的「XAdd 失敗→補償刪除」路徑可以在單元測試注入假的失敗客戶端（見 gps_test.go），
// 不必牽動真正的 Redis 連線；*redis.Client 天生滿足此介面，NewService 簽章不受影響。
type activityStreamXAdder interface {
	XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd
}

type Service struct {
	repo      *Repository
	raceSvc   *race.Service
	rdb       activityStreamXAdder
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
