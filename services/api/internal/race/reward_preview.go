// 前台活動資訊頁「活動獎勵」頁籤：完成活動有機會獲得的獎勵預覽（公開，不需登入）。
// 刻意不回傳 prob_bp/weight/min/max/序號面額庫存數——這些是機率與數量等機敏設定（見 memory
// activity-reward-system），公開端點只列「有哪些獎勵」讓玩家有心理預期，不能讓玩家反推中獎率。
// 與 GetPublicDetail/ListPublic 的關係：那兩處回傳的 race JSON 已改為一律清空 reward_config
// （見 service.go ListPublic/GetPublicDetail 的 SEC 註解），本檔案是取代方案——公開需要的展示欄位改
// 走這支專用端點的白名單欄位（kind/name/icon_url/description）。
package race

import (
	"context"
	"fmt"
	"strconv"

	"github.com/dor/api/internal/activityreward"
)

// RewardPreviewItem 前台「活動獎勵」頁籤單筆卡片：只有可讀展示欄位，絕不含機率/數量/權重/面額庫存。
type RewardPreviewItem struct {
	Kind        string `json:"kind"` // economy|serial
	Name        string `json:"name"`
	Amount      string `json:"amount"` // economy 類的數量/區間顯示（如 100~500 / 7 天）；serial 類為空
	IconURL     string `json:"icon_url"`
	Description string `json:"description"`
}

// economyRewardLabel 經濟類獎勵型別 → 前台可讀名稱，沿用 admin RaceForm.tsx 既有的 REWARD_TYPE_LABEL
// 命名（apps/web/src/app/admin/races/RaceForm.tsx），避免前後台用語不一致。
var economyRewardLabel = map[string]string{
	"exp": "EXP 經驗值",
	"dp":  "DP",
	"gp":  "GP",
	"vip": "VIP 天數",
}

// GetRewardPreview 回傳 raceID 這場賽事「完成有機會獲得」的獎勵預覽（公開；不需登入）。
// race 需 approved 才可見（比照 GetRaceProgress 等其餘公開資訊端點的可見性判斷）；reward_config
// 未設定/無項目 → 回空陣列（呼叫端據此判斷要不要顯示「活動獎勵」頁籤）。
func (s *Service) GetRewardPreview(ctx context.Context, raceID string) ([]RewardPreviewItem, error) {
	r, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if r == nil || r.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}
	return s.buildRewardPreviewItems(ctx, r.RewardConfig)
}

// GetEntryRewardPreview 回傳 raceID 這場賽事的參賽虛擬獎勵預覽（migration 140；公開；不需登入）：
// 「賽事開始後自動發放給所有已報名者」的項目清單，與 GetRewardPreview 共用同一白名單欄位過濾邏輯
// （buildRewardPreviewItems）——不含機率/數量/權重，理由同上（見 memory activity-reward-system）。
// entry_reward_config 未設定/無項目 → 回空陣列（呼叫端據此判斷要不要顯示這段展示區塊）。
func (s *Service) GetEntryRewardPreview(ctx context.Context, raceID string) ([]RewardPreviewItem, error) {
	r, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if r == nil || r.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}
	return s.buildRewardPreviewItems(ctx, r.EntryRewardConfig)
}

// buildRewardPreviewItems 把一組 activityreward.RewardConfig 轉成公開展示用預覽項目清單，供
// GetRewardPreview／GetEntryRewardPreview 共用（兩者觸發條件不同，但公開展示的白名單欄位過濾規則完全
// 一致）。cfg 為 nil 或無項目 → 回空陣列。
func (s *Service) buildRewardPreviewItems(ctx context.Context, cfg *activityreward.RewardConfig) ([]RewardPreviewItem, error) {
	if cfg == nil || len(cfg.Items) == 0 {
		return []RewardPreviewItem{}, nil
	}

	out := []RewardPreviewItem{}
	seenGroups := map[string]bool{}
	seenCouponDefs := map[string]bool{}
	var groupIDs []string
	var couponDefIDs []string
	for i := range cfg.Items {
		item := &cfg.Items[i]
		// Hidden＝管理員設定的「驚喜獎勵」（見 activityreward.RewardItem.Hidden 註解）：只影響這支
		// 公開預覽端點的可見清單，不進 groupIDs/couponDefIDs/out；RollAndGrant 抽獎/發獎完全不看這個
		// 欄位、不受影響，玩家一樣會照常抽中拿到，只是事前在「活動獎勵」頁籤看不到會有這個項目。
		if item.Hidden {
			continue
		}
		switch item.Type {
		case "serial":
			// 兩層抽獎的第二層面額（見 activityreward.RewardItem 註解）：只取 group id 去查展示欄位，
			// 完全不帶 Weight（抽獎權重）。跨 item 重複引用同一序號組時只列一次。
			for _, d := range item.ValidDenominations() {
				if !seenGroups[d.GroupID] {
					seenGroups[d.GroupID] = true
					groupIDs = append(groupIDs, d.GroupID)
				}
			}
		case "coupon":
			// 活動優惠券（migration 138）：只取 coupon_def_id 去查展示欄位（名稱/面額），機率(prob_bp)
			// 仍不外洩。跨 item 重複引用同一券種時只列一次。
			if item.CouponDefID != "" && !seenCouponDefs[item.CouponDefID] {
				seenCouponDefs[item.CouponDefID] = true
				couponDefIDs = append(couponDefIDs, item.CouponDefID)
			}
		case "exp", "dp", "gp", "vip":
			if label, ok := economyRewardLabel[item.Type]; ok {
				// 顯示獎勵數字（金額區間/天數）——這是「獎勵大小」，非中獎機率，可對外；serial 面額/庫存/權重仍不外洩。
				amount := ""
				switch {
				case item.Type == "vip":
					if item.Days > 0 {
						amount = fmt.Sprintf("%d 天", item.Days)
					}
				case item.Min == item.Max:
					amount = strconv.Itoa(item.Max)
				default:
					amount = fmt.Sprintf("%d~%d", item.Min, item.Max)
				}
				out = append(out, RewardPreviewItem{Kind: "economy", Name: label, Amount: amount})
			}
		}
	}

	if len(groupIDs) > 0 {
		serialItems, err := s.repo.GetRewardSerialGroupPreview(ctx, groupIDs)
		if err != nil {
			return nil, err
		}
		out = append(out, serialItems...)
	}
	if len(couponDefIDs) > 0 {
		couponItems, err := s.repo.GetCouponDefPreview(ctx, couponDefIDs)
		if err != nil {
			return nil, err
		}
		out = append(out, couponItems...)
	}
	return out, nil
}

// GetCouponDefPreview 依券種 id 清單查前台展示欄位（名稱＋面額），刻意不查 enabled/期限等後台管理欄位
// （機敏／與展示無關）。amount 格式化為「NT$ 100」比照 economy 類「100~500」的可讀字串慣例。
func (r *Repository) GetCouponDefPreview(ctx context.Context, defIDs []string) ([]RewardPreviewItem, error) {
	rows, err := r.db.Query(ctx, `
		SELECT COALESCE(name,''), amount_cents FROM event_coupon_defs WHERE id = ANY($1::uuid[])`, defIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []RewardPreviewItem{}
	for rows.Next() {
		var name string
		var amountCents int
		if err := rows.Scan(&name, &amountCents); err != nil {
			return nil, err
		}
		out = append(out, RewardPreviewItem{Kind: "coupon", Name: name, Amount: fmt.Sprintf("NT$ %d", amountCents/100)})
	}
	return out, rows.Err()
}

// GetRewardSerialGroupPreview 依序號組 id 清單查前台展示欄位（品項名稱/圖示/說明/商家名稱降階），
// 刻意不查庫存數與抽獎權重（機敏，不對外）。name 優先用 item_label（面額/品項名稱），空則退回商家名稱。
func (r *Repository) GetRewardSerialGroupPreview(ctx context.Context, groupIDs []string) ([]RewardPreviewItem, error) {
	rows, err := r.db.Query(ctx, `
		SELECT COALESCE(g.item_label,''), COALESCE(g.icon_url,''), COALESCE(g.description,''), COALESCE(m.name,'')
		FROM reward_serial_groups g
		LEFT JOIN reward_merchants m ON m.id = g.merchant_id
		WHERE g.id = ANY($1::uuid[])`, groupIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []RewardPreviewItem{}
	for rows.Next() {
		var itemLabel, iconURL, description, merchantName string
		if err := rows.Scan(&itemLabel, &iconURL, &description, &merchantName); err != nil {
			return nil, err
		}
		name := itemLabel
		if name == "" {
			name = merchantName
		}
		out = append(out, RewardPreviewItem{Kind: "serial", Name: name, IconURL: iconURL, Description: description})
	}
	return out, rows.Err()
}
