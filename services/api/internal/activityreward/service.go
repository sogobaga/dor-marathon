package activityreward

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidInput 輸入驗證失敗（回 400），比照 internal/rewardserial 慣例。
var ErrInvalidInput = errors.New("invalid input")

var validExpiryModes = map[string]bool{"fixed": true, "days": true}

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

// --- 活動優惠券券種（migration 138）---

func (s *Service) ListCouponDefs(ctx context.Context) ([]CouponDef, error) {
	return s.repo.ListCouponDefs(ctx)
}

// validateCouponDefInput 正規化並驗證券種輸入，回傳解析後的 expires_at（僅 fixed 模式有值）。
func validateCouponDefInput(in *CouponDefInput) (*time.Time, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, fmt.Errorf("%w: 名稱必填", ErrInvalidInput)
	}
	if in.AmountCents <= 0 {
		return nil, fmt.Errorf("%w: 面額需大於 0", ErrInvalidInput)
	}
	if !validExpiryModes[in.ExpiryMode] {
		return nil, fmt.Errorf("%w: expiry_mode 需為 fixed/days", ErrInvalidInput)
	}
	var expiresAt *time.Time
	if in.ExpiryMode == "fixed" {
		if in.ExpiresAt == nil || strings.TrimSpace(*in.ExpiresAt) == "" {
			return nil, fmt.Errorf("%w: 指定到期日模式需填到期日", ErrInvalidInput)
		}
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(*in.ExpiresAt))
		if err != nil {
			return nil, fmt.Errorf("%w: expires_at 格式錯誤（需 RFC3339）", ErrInvalidInput)
		}
		expiresAt = &t
		in.ValidDays = nil // fixed 模式不需要 valid_days，避免殘留舊值
	} else {
		if in.ValidDays == nil || *in.ValidDays <= 0 {
			return nil, fmt.Errorf("%w: 獲得後 N 天模式需填正整數天數", ErrInvalidInput)
		}
	}
	return expiresAt, nil
}

func (s *Service) CreateCouponDef(ctx context.Context, in CouponDefInput) (*CouponDef, error) {
	expiresAt, err := validateCouponDefInput(&in)
	if err != nil {
		return nil, err
	}
	return s.repo.CreateCouponDef(ctx, in, expiresAt)
}

func (s *Service) UpdateCouponDef(ctx context.Context, id string, in CouponDefInput) (*CouponDef, error) {
	expiresAt, err := validateCouponDefInput(&in)
	if err != nil {
		return nil, err
	}
	return s.repo.UpdateCouponDef(ctx, id, in, expiresAt)
}

func (s *Service) DeleteCouponDef(ctx context.Context, id string) error {
	return s.repo.DeleteCouponDef(ctx, id)
}
