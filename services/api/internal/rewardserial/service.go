package rewardserial

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidInput 輸入驗證失敗（回 400）
var ErrInvalidInput = errors.New("invalid input")

type Service struct{ repo *Repository }

func NewService(repo *Repository) *Service { return &Service{repo: repo} }

// --- 合作商家 ---

func (s *Service) ListMerchants(ctx context.Context) ([]Merchant, error) {
	return s.repo.ListMerchants(ctx)
}

func (s *Service) CreateMerchant(ctx context.Context, name, note string) (*Merchant, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: 商家名稱必填", ErrInvalidInput)
	}
	return s.repo.CreateMerchant(ctx, name, strings.TrimSpace(note))
}

func (s *Service) UpdateMerchant(ctx context.Context, id, name, note string) (*Merchant, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: 商家名稱必填", ErrInvalidInput)
	}
	return s.repo.UpdateMerchant(ctx, id, name, strings.TrimSpace(note))
}

func (s *Service) DeleteMerchant(ctx context.Context, id string) error {
	return s.repo.DeleteMerchant(ctx, id)
}

// --- 序號組 ---

var validUseLimitTypes = map[string]bool{"single": true, "repeat": true, "unlimited": true}

func (s *Service) ListGroups(ctx context.Context) ([]Group, error) {
	return s.repo.ListGroups(ctx)
}

// validateGroupInput 驗證並正規化輸入（就地修改 in），回傳解析後的 valid_from/valid_until。
func (s *Service) validateGroupInput(in *GroupInput) (*time.Time, *time.Time, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, nil, fmt.Errorf("%w: 名稱必填", ErrInvalidInput)
	}
	if !validUseLimitTypes[in.UseLimitType] {
		return nil, nil, fmt.Errorf("%w: use_limit_type 需為 single/repeat/unlimited", ErrInvalidInput)
	}
	if in.UseLimitType == "repeat" && (in.UseLimitCount == nil || *in.UseLimitCount <= 0) {
		return nil, nil, fmt.Errorf("%w: 選擇「可重複使用」需填使用次數（正整數）", ErrInvalidInput)
	}
	if in.UseLimitType != "repeat" {
		in.UseLimitCount = nil // single/unlimited 不需要次數，避免殘留舊值
	}
	if in.GrantCount <= 0 {
		in.GrantCount = 1
	}
	if in.FaceValue < 0 {
		in.FaceValue = 0 // 面額（migration 149）：負值視同未設，避免帶壞組合包 bundle_total 計算
	}
	in.UsageNote = strings.TrimSpace(in.UsageNote)
	in.IconURL = strings.TrimSpace(in.IconURL)
	in.Description = strings.TrimSpace(in.Description)

	if in.MerchantID != nil {
		mid := strings.TrimSpace(*in.MerchantID)
		if mid == "" {
			in.MerchantID = nil
		} else if !isValidUUID(mid) {
			return nil, nil, fmt.Errorf("%w: merchant_id 格式錯誤", ErrInvalidInput)
		} else {
			in.MerchantID = &mid
		}
	}

	if in.AppliesAllRaces {
		in.RaceIDs = nil
	} else {
		if len(in.RaceIDs) == 0 {
			return nil, nil, fmt.Errorf("%w: 未勾選「全部活動」時需至少指定一場活動", ErrInvalidInput)
		}
		for _, rid := range in.RaceIDs {
			if !isValidUUID(rid) {
				return nil, nil, fmt.Errorf("%w: race_ids 含不合法的活動 ID", ErrInvalidInput)
			}
		}
	}

	var validFrom *time.Time
	if in.ValidFrom != nil && strings.TrimSpace(*in.ValidFrom) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(*in.ValidFrom))
		if err != nil {
			return nil, nil, fmt.Errorf("%w: valid_from 格式錯誤（需 RFC3339）", ErrInvalidInput)
		}
		validFrom = &t
	}
	var validUntil *time.Time
	if in.ValidUntil != nil && strings.TrimSpace(*in.ValidUntil) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(*in.ValidUntil))
		if err != nil {
			return nil, nil, fmt.Errorf("%w: valid_until 格式錯誤（需 RFC3339）", ErrInvalidInput)
		}
		validUntil = &t
	}
	if validFrom != nil && validUntil != nil && !validFrom.Before(*validUntil) {
		return nil, nil, fmt.Errorf("%w: 開始時間需早於使用期限", ErrInvalidInput)
	}
	return validFrom, validUntil, nil
}

func (s *Service) CreateGroup(ctx context.Context, in GroupInput) (*Group, error) {
	validFrom, validUntil, err := s.validateGroupInput(&in)
	if err != nil {
		return nil, err
	}
	return s.repo.CreateGroup(ctx, in, validFrom, validUntil)
}

func (s *Service) UpdateGroup(ctx context.Context, id string, in GroupInput) (*Group, error) {
	validFrom, validUntil, err := s.validateGroupInput(&in)
	if err != nil {
		return nil, err
	}
	return s.repo.UpdateGroup(ctx, id, in, validFrom, validUntil)
}

func (s *Service) DeleteGroup(ctx context.Context, id string) error {
	return s.repo.DeleteGroup(ctx, id)
}

// --- 序號 ---

func (s *Service) ListSerials(ctx context.Context, groupID, status string, limit, offset int) ([]Serial, int, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListSerials(ctx, groupID, status, limit, offset)
}

func (s *Service) ImportSerials(ctx context.Context, groupID string, items []ImportInput) (*ImportResult, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("%w: 沒有可匯入的序號", ErrInvalidInput)
	}
	if len(items) > 5000 {
		return nil, fmt.Errorf("%w: 一次最多匯入 5000 筆", ErrInvalidInput)
	}
	return s.repo.ImportSerials(ctx, groupID, items)
}

func (s *Service) VoidSerial(ctx context.Context, groupID, serialID string) error {
	return s.repo.VoidSerial(ctx, groupID, serialID)
}
