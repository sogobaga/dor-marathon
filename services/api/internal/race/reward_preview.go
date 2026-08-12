// 前台活動資訊頁「活動獎勵」頁籤：完成活動有機會獲得的獎勵預覽（公開，不需登入）。
// 刻意不回傳 prob_bp/weight/min/max/序號面額庫存數——這些是機率與數量等機敏設定（見 memory
// activity-reward-system），公開端點只列「有哪些獎勵」讓玩家有心理預期，不能讓玩家反推中獎率。
// 與 GetPublicDetail/ListPublic 的關係：那兩處回傳的 race JSON 已改為一律清空 reward_config
// （見 service.go ListPublic/GetPublicDetail 的 SEC 註解），本檔案是取代方案——公開需要的展示欄位改
// 走這支專用端點的白名單欄位（kind/name/icon_url/description）。
package race

import (
	"context"
)

// RewardPreviewItem 前台「活動獎勵」頁籤單筆卡片：只有可讀展示欄位，絕不含機率/數量/權重/面額庫存。
type RewardPreviewItem struct {
	Kind        string `json:"kind"`        // economy|serial
	Name        string `json:"name"`
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
	if r.RewardConfig == nil || len(r.RewardConfig.Items) == 0 {
		return []RewardPreviewItem{}, nil
	}

	out := []RewardPreviewItem{}
	seenGroups := map[string]bool{}
	var groupIDs []string
	for i := range r.RewardConfig.Items {
		item := &r.RewardConfig.Items[i]
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
		case "exp", "dp", "gp", "vip":
			if label, ok := economyRewardLabel[item.Type]; ok {
				out = append(out, RewardPreviewItem{Kind: "economy", Name: label})
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
	return out, nil
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
