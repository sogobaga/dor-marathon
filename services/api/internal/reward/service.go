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
//
// 併發修補（SEC M5）：原本「查剩餘次數 → 判斷 → 插入抽獎紀錄」是三個各自 autocommit 的步驟，
// 中間沒有鎖——同一使用者兩個併發請求（雙擊/兩個分頁）可能都在「剩餘 1 次」時通過檢查，各自插入
// 一筆 wheel_spins，實際多發一次抽獎機會/獎項。改成用 BeginSpinTx 開一筆交易並立刻對
// (userID, raceID) 取 pg_advisory_xact_lock，讓第二個請求必須等第一個交易 COMMIT 後才能繼續，
// 此時它重新查到的剩餘次數已經反映第一個請求剛插入的那筆，才不會被多算（比照
// internal/race/reward_draw.go DrawRaceRewardWinners 同一模式）。
func (s *Service) Spin(ctx context.Context, userID, raceID string) (*SpinResult, error) {
	tx, err := s.repo.BeginSpinTx(ctx, userID, raceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) // Commit 後為 no-op

	completedMissions, err := s.repo.CountCompletedMissionsTx(ctx, tx, userID, raceID)
	if err != nil {
		return nil, err
	}
	usedSpins, err := s.repo.CountSpinsAllTx(ctx, tx, userID, raceID)
	if err != nil {
		return nil, err
	}
	remaining := completedMissions - usedSpins
	if remaining <= 0 {
		return nil, ErrNoSpinsLeft
	}

	// 加權隨機抽獎
	item := weightedRandom(defaultWheelPool)

	// 記錄抽獎結果（同一交易內，advisory lock 保護下不會與其他併發請求交錯）
	if err := s.repo.RecordSpinTx(ctx, tx, userID, raceID, item.ID, item.Kind, item.Amount); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
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
