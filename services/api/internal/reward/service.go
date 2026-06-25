package reward

import (
	"context"
	"errors"
	"math/rand"
)

var (
	ErrNoSpinsLeft  = errors.New("no spins remaining (complete more missions to earn spins)")
	ErrNotInRace    = errors.New("not registered in this race")
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// SpinQuota 計算剩餘可抽獎次數
// 規則：完成 N 個任務 = N 次抽獎機會（總抽獎次數為上限）
func (s *Service) SpinQuota(ctx context.Context, userID, raceID string) (remaining int, total int, err error) {
	completedMissions, err := s.repo.CountCompletedMissions(ctx, userID, raceID)
	if err != nil {
		return 0, 0, err
	}
	usedSpins, err := s.repo.CountSpinsAll(ctx, userID, raceID)
	if err != nil {
		return 0, 0, err
	}
	total = completedMissions
	remaining = completedMissions - usedSpins
	if remaining < 0 {
		remaining = 0
	}
	return
}

// Spin 執行抽獎
func (s *Service) Spin(ctx context.Context, userID, raceID string) (*SpinResult, error) {
	remaining, _, err := s.SpinQuota(ctx, userID, raceID)
	if err != nil {
		return nil, err
	}
	if remaining <= 0 {
		return nil, ErrNoSpinsLeft
	}

	// 加權隨機抽獎
	item := weightedRandom(defaultWheelPool)

	// 記錄抽獎結果
	if err := s.repo.RecordSpin(ctx, userID, raceID, item.ID, item.Kind, item.Amount); err != nil {
		return nil, err
	}

	result := &SpinResult{
		Item:         item,
		CanSpinAgain: item.Kind == "again",
	}

	// 若抽到集點卡，隨機發放貼紙
	if item.Kind == "sticker" {
		for i := 0; i < item.Amount; i++ {
			no := s.grantRandomSticker(ctx, userID, raceID)
			if no > 0 {
				result.StickerNo = no
				result.StickerName = stickerName(no)
			}
		}
	}

	return result, nil
}

// GetStickerCard 取得九宮格狀態
func (s *Service) GetStickerCard(ctx context.Context, userID, raceID string) (*StickerCard, error) {
	owned, err := s.repo.GetStickers(ctx, userID, raceID)
	if err != nil {
		return nil, err
	}

	stickers := make([]Sticker, 9)
	for i := 1; i <= 9; i++ {
		stickers[i-1] = Sticker{
			No:    i,
			Name:  stickerName(i),
			Owned: owned[i],
		}
	}

	return &StickerCard{
		RaceID:   raceID,
		Stickers: stickers,
		Complete: len(owned) == 9,
	}, nil
}

// --- helpers ---

// weightedRandom 依權重隨機選取一個獎項
func weightedRandom(pool []WheelItem) WheelItem {
	totalWeight := 0
	for _, item := range pool {
		totalWeight += item.Weight
	}
	r := rand.Intn(totalWeight)
	cumulative := 0
	for _, item := range pool {
		cumulative += item.Weight
		if r < cumulative {
			return item
		}
	}
	return pool[len(pool)-1]
}

// grantRandomSticker 發放一張還沒有的貼紙，回傳貼紙編號（0 表示已集滿）
func (s *Service) grantRandomSticker(ctx context.Context, userID, raceID string) int {
	owned, err := s.repo.GetStickers(ctx, userID, raceID)
	if err != nil {
		return 0
	}

	// 找出還沒有的貼紙
	missing := []int{}
	for i := 1; i <= 9; i++ {
		if !owned[i] {
			missing = append(missing, i)
		}
	}
	if len(missing) == 0 {
		return 0 // 已集滿
	}

	no := missing[rand.Intn(len(missing))]
	s.repo.GrantSticker(ctx, userID, raceID, no)
	return no
}

func stickerName(no int) string {
	if no < 1 || no >= len(defaultStickerNames) {
		return ""
	}
	return defaultStickerNames[no]
}
