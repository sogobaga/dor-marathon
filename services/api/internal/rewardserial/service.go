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

// validateGroupInput 驗證並正規化輸入（就地修改 in），回傳解析後的 valid_from/valid_until。selfID 是
// UpdateGroup 時「正在編輯的這個序號組自己的 id」（CreateGroup 傳空字串）——用來擋 BundleItems 自我引用
// （見 validateBundleItems）。
func (s *Service) validateGroupInput(ctx context.Context, in *GroupInput, selfID string) (*time.Time, *time.Time, error) {
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

	// 組合能力（migration 150）：is_bundle=true 才需要 bundle_items（子面額組×數量），且 face_value 不
	// 存靜態快照——一律強制歸零，實際值由 Repository 查詢時動態算 Σ(子面額組 face_value × count)（見
	// Group.FaceValue 註解），避免子項改動後這裡的舊快照跟真值脫鉤。is_bundle=false 則反過來要求
	// bundle_items 必須是空——不允許一般序號組偷偷帶組合定義造成前後端認知不一致。
	if in.IsBundle {
		if err := s.validateBundleItems(ctx, in, selfID); err != nil {
			return nil, nil, err
		}
		in.FaceValue = 0
	} else if len(in.BundleItems) > 0 {
		return nil, nil, fmt.Errorf("%w: 非組合型序號組不可帶 bundle_items", ErrInvalidInput)
	} else {
		in.BundleItems = []GroupBundleItem{}
	}

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
	validFrom, validUntil, err := s.validateGroupInput(ctx, &in, "")
	if err != nil {
		return nil, err
	}
	return s.repo.CreateGroup(ctx, in, validFrom, validUntil)
}

func (s *Service) UpdateGroup(ctx context.Context, id string, in GroupInput) (*Group, error) {
	validFrom, validUntil, err := s.validateGroupInput(ctx, &in, id)
	if err != nil {
		return nil, err
	}
	return s.repo.UpdateGroup(ctx, id, in, validFrom, validUntil)
}

// validateBundleItems 驗證組合型序號組（is_bundle=true，migration 150）的 bundle_items 結構＋跨表規則：
// 至少 1 個子項、子項 id 格式合法、count>=1、不可重複引用同一子面額組、不可引用自己（selfID，UpdateGroup
// 才有意義）。結構驗證過關後查 DB 取得每個子項當下的 is_bundle/merchant_id，交給純函式
// validateBundleChildMeta 判斷「非組合型（防巢狀）＋ 同一商家」。就地正規化 in.BundleItems[i].ChildGroupID
// （trim 後的值）。
func (s *Service) validateBundleItems(ctx context.Context, in *GroupInput, selfID string) error {
	if len(in.BundleItems) == 0 {
		return fmt.Errorf("%w: 組合型序號組需至少 1 個子項", ErrInvalidInput)
	}
	if len(in.BundleItems) > 20 {
		return fmt.Errorf("%w: 組合型序號組子項最多 20 個", ErrInvalidInput)
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(in.BundleItems))
	for i := range in.BundleItems {
		cid := strings.TrimSpace(in.BundleItems[i].ChildGroupID)
		if cid == "" || !isValidUUID(cid) {
			return fmt.Errorf("%w: 組合子項 %d 的子面額組 id 不合法", ErrInvalidInput, i)
		}
		if in.BundleItems[i].Count < 1 {
			return fmt.Errorf("%w: 組合子項 %d 數量需 ≥1", ErrInvalidInput, i)
		}
		if selfID != "" && cid == selfID {
			return fmt.Errorf("%w: 組合子項不可引用自己", ErrInvalidInput)
		}
		if seen[cid] {
			return fmt.Errorf("%w: 組合子項不可重複引用同一子面額組（%s）", ErrInvalidInput, cid)
		}
		seen[cid] = true
		in.BundleItems[i].ChildGroupID = cid
		ids = append(ids, cid)
	}
	metas, err := s.repo.loadGroupMetaByIDs(ctx, ids)
	if err != nil {
		return err
	}
	return validateBundleChildMeta(in.BundleItems, metas)
}

// validateBundleChildMeta 純函式：依序檢查每個子項是否符合「子面額組存在＋非組合型（防巢狀）＋與其餘
// 子項同一商家」，第一個違規即回傳描述性錯誤；全部合法回 nil。metas 是預先查好的 child id → 中繼資料
// （見 loadGroupMetaByIDs），本函式本身不碰 DB，方便單元測試涵蓋巢狀／跨商家等邊界情況。
func validateBundleChildMeta(items []GroupBundleItem, metas map[string]childGroupMeta) error {
	var commonMerchant *string
	first := true
	for i, it := range items {
		m, ok := metas[it.ChildGroupID]
		if !ok {
			return fmt.Errorf("%w: 組合子項 %d 指定的子面額組不存在", ErrInvalidInput, i)
		}
		if m.IsBundle {
			return fmt.Errorf("%w: 組合子項 %d 的子面額組本身也是組合型，不可巢狀", ErrInvalidInput, i)
		}
		if first {
			commonMerchant = m.MerchantID
			first = false
			continue
		}
		if !samePtrString(commonMerchant, m.MerchantID) {
			return fmt.Errorf("%w: 組合子項須為同一商家", ErrInvalidInput)
		}
	}
	return nil
}

// samePtrString 比較兩個可能為 nil 的字串指標是否代表同一值：皆 nil 視為相同（都未指定商家）；一 nil
// 一非 nil 視為不同；皆非 nil 則比較實際值。供 validateBundleChildMeta 判斷組合子項的 merchant_id 是否
// 一致。
func samePtrString(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
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
