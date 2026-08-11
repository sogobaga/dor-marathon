package activityreward

import (
	"context"
	"fmt"
	"strings"
)

type Service struct{ repo *Repository }

func NewService(repo *Repository) *Service { return &Service{repo: repo} }

func (s *Service) ListTemplates(ctx context.Context) ([]Template, error) {
	return s.repo.ListTemplates(ctx)
}

// validateTemplateInput 正規化並驗證模板輸入，回傳 trim 過的名稱（items 驗證與 race.reward_config 共用
// RewardConfig.Validate）。
func validateTemplateInput(name string, items []RewardItem) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("名稱必填")
	}
	cfg := RewardConfig{Items: items}
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	return name, nil
}

func (s *Service) CreateTemplate(ctx context.Context, name string, items []RewardItem) (*Template, error) {
	name, err := validateTemplateInput(name, items)
	if err != nil {
		return nil, err
	}
	return s.repo.CreateTemplate(ctx, name, items)
}

func (s *Service) UpdateTemplate(ctx context.Context, id, name string, items []RewardItem) (*Template, error) {
	name, err := validateTemplateInput(name, items)
	if err != nil {
		return nil, err
	}
	return s.repo.UpdateTemplate(ctx, id, name, items)
}

func (s *Service) DeleteTemplate(ctx context.Context, id string) error {
	return s.repo.DeleteTemplate(ctx, id)
}
