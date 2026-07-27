package monopoly

import (
	"context"

	"github.com/dor/api/internal/appsettings"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetState(ctx context.Context, userID string) (*PlayerState, error) {
	st, err := s.repo.GetState(ctx, userID)
	if err != nil {
		return nil, err
	}
	st.DiceGPCost = diceGPCost(ctx, s)
	return st, nil
}

func (s *Service) Roll(ctx context.Context, userID string) (*RollResult, error) {
	cost := diceGPCost(ctx, s)
	lapReward := appsettings.GetInt(ctx, s.repo.db, "monopoly_lap_reward_gp", 0)
	return s.repo.Roll(ctx, userID, cost, lapReward)
}

// diceGPCost 讀後台可調的擲骰成本（預設 3 GP），與 race package 存取 s.repo.db 的慣例一致
// （appsettings.GetInt 只吃 *pgxpool.Pool，同套件內直接讀 repo 的 db 欄位，不需另外分層）。
func diceGPCost(ctx context.Context, s *Service) int {
	return appsettings.GetInt(ctx, s.repo.db, "monopoly_dice_gp_cost", 3)
}
