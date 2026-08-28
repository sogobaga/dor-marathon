// 前台活動資訊頁「活動獎勵」頁籤：完成活動有機會獲得的獎勵預覽（公開，不需登入）。
// 消保法機率揭露（2026-08）：改為回傳 prob_bp（中獎機率，萬分位），非 100% 必中的項目前台會標
// 「(中獎機率 xx%)」。仍不回傳 weight/min/max/序號面額庫存數——這些是「獎勵大小/供給量」而非「機率」，
// 揭露義務只及於中獎機率本身（見 memory activity-reward-system）。serial 類的 prob_bp 已經是「該面額
// 實際被抽中的機率」＝ 商家層機率(item.ProbBP) × 面額權重佔比（見下方 buildRewardPreviewItems 計算），
// 不是「商家給不給獎」的第一層機率——直接顯示第一層機率會誤導玩家高估單一面額的中獎率。
// 與 GetPublicDetail/ListPublic 的關係：那兩處回傳的 race JSON 已改為一律清空 reward_config
// （見 service.go ListPublic/GetPublicDetail 的 SEC 註解），本檔案是取代方案——公開需要的展示欄位改
// 走這支專用端點的白名單欄位（kind/name/icon_url/description/prob_bp）。
package race

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/dor/api/internal/activityreward"
)

// largestNumberIn 取字串中最大的連續數字（如「LINE POINTS 1000」→1000），無數字回 ok=false。
// 供面額卡排序用——與前台 RaceForm parsePointValue／後台序號管理 denomValueOf 同一種解析口徑。
func largestNumberIn(s string) (int, bool) {
	best, cur, has, curHas := 0, 0, false, false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			cur = cur*10 + int(r-'0')
			curHas = true
			continue
		}
		if curHas && (!has || cur > best) {
			best, has = cur, true
		}
		cur, curHas = 0, false
	}
	if curHas && (!has || cur > best) {
		best, has = cur, true
	}
	return best, has
}

// RewardPreviewItem 前台「活動獎勵」頁籤單筆卡片：可讀展示欄位＋中獎機率（prob_bp），絕不含權重/面額
// 庫存等抽獎引擎內部設定。
type RewardPreviewItem struct {
	Kind        string `json:"kind"` // economy|serial|coupon
	Name        string `json:"name"`
	Amount      string `json:"amount"` // economy 類的數量/區間顯示（如 100~500 / 7 天）；serial 類為空
	IconURL     string `json:"icon_url"`
	Description string `json:"description"`
	// ProbBP 中獎機率，萬分位（10000=100% 必中）。serial 類已換算成「該面額實際被抽中的機率」（商家層
	// 機率 × 面額權重佔比，見 buildRewardPreviewItems），不是商家層第一層機率。前台對 <10000 的項目標示
	// 「(中獎機率 xx%)」，=10000 不特別標示（必得，見 apps/web RaceDetailScreen.tsx formatProbLabel）。
	ProbBP int `json:"prob_bp,omitempty"`
	// refID 內部關聯用（serial 的序號組 id／coupon 的券種 id），只在 buildRewardPreviewItems 內用來把
	// GetRewardSerialGroupPreview/GetCouponDefPreview 查回的列與各自算好的 ProbBP 對上；json:"-" 絕不
	// 外洩原始 DB id 給前台。
	refID string `json:"-"`
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
	seenBundles := map[string]bool{}
	var groupIDs []string
	var couponDefIDs []string
	var bundleItems []*activityreward.RewardItem
	// groupProbBP/couponProbBP：group/coupon-def id → 算好的展示用機率（萬分位）。用「第一次遇到該
	// id 時所屬的 item」算出來的值，跟既有 seenGroups/seenCouponDefs 去重邏輯（name/description 也是
	// 取第一次遇到的值）保持一致；同一序號組/券種被多個 item 重複引用、且各 item 機率不同時，只呈現
	// 第一個 item 的機率——這與既有「跨 item 重複引用只列一次卡片」的展示語意本來就一致（一張卡只能
	// 標一個機率），不是本次新增的限制。
	groupProbBP := map[string]int{}
	couponProbBP := map[string]int{}
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
			if len(item.Bundle) > 0 {
				// 固定組合包（migration 149，見 activityreward.RewardItem.Bundle）：中獎後「全發」，不是
				// 「加權抽一個」——item.ProbBP 本身就是玩家實際拿到「整包」的機率，不需要再乘任何權重
				// 換算（不同於下面兩層抽獎分支）。all-or-nothing 之下拆開顯示各面額各自機率沒有意義
				// （玩家要嘛全拿要嘛全沒有），故不比照兩層抽獎逐面額各自一張卡，改成合併成一張卡片。
				// 去重 key 用「所有 entry 的 group_id:count」组成，避免多個 item 重複設定同一組合包時
				// 卡片重複；不同 item 若組合內容不同（不同 group/count 組合）視為不同卡片各自顯示。
				key := bundleDedupKey(item.Bundle)
				if !seenBundles[key] {
					seenBundles[key] = true
					bundleItems = append(bundleItems, item)
				}
				continue
			}
			// 兩層抽獎的第二層面額（見 activityreward.RewardItem 註解）：取 group id 去查展示欄位，
			// 並把 Weight 換算成「該面額實際被抽中的機率」＝ item.ProbBP（商家層機率）× 該面額權重 /
			// 該 item 所有有效面額權重總和——這是玩家拿到「這張卡片」的真實機率，不是商家層機率（直接
			// 秀商家層機率會誤導玩家以為每個面額都有那麼高的中獎率）。跨 item 重複引用同一序號組時只
			// 列一次（沿用既有 seenGroups 去重）。
			denoms := item.ValidDenominations()
			var totalWeight int
			for _, d := range denoms {
				totalWeight += d.Weight
			}
			for _, d := range denoms {
				if !seenGroups[d.GroupID] {
					seenGroups[d.GroupID] = true
					groupIDs = append(groupIDs, d.GroupID)
					if totalWeight > 0 {
						// 四捨五入到萬分位整數，比直接捨去更貼近實際權重比例。
						groupProbBP[d.GroupID] = int((int64(item.ProbBP)*int64(d.Weight) + int64(totalWeight)/2) / int64(totalWeight))
					}
				}
			}
		case "coupon":
			// 活動優惠券（migration 138）：只取 coupon_def_id 去查展示欄位（名稱/面額）；機率就是
			// item.ProbBP 本身（coupon 是單層抽獎，不像 serial 要再乘面額權重）。跨 item 重複引用同一
			// 券種時只列一次。
			if item.CouponDefID != "" && !seenCouponDefs[item.CouponDefID] {
				seenCouponDefs[item.CouponDefID] = true
				couponDefIDs = append(couponDefIDs, item.CouponDefID)
				couponProbBP[item.CouponDefID] = item.ProbBP
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
				out = append(out, RewardPreviewItem{Kind: "economy", Name: label, Amount: amount, ProbBP: item.ProbBP})
			}
		}
	}

	if len(groupIDs) > 0 {
		// 面額卡排序＝面額數字小→大（20→50→100→1000），解析不到數字者殿後依名稱——與後台
		// 序號/獎勵管理、賽事表單面額權重列表同一套順序（2026-08-28 使用者要求前後台同步）。
		// 查詢用 WHERE id=ANY($1) 回傳順序不定，必須顯式排序。
		serialItems, err := s.repo.GetRewardSerialGroupPreview(ctx, groupIDs)
		if err != nil {
			return nil, err
		}
		for i := range serialItems {
			serialItems[i].ProbBP = groupProbBP[serialItems[i].refID]
		}
		sort.SliceStable(serialItems, func(a, b int) bool {
			va, oka := largestNumberIn(serialItems[a].Name)
			vb, okb := largestNumberIn(serialItems[b].Name)
			if oka && okb && va != vb {
				return va < vb
			}
			if oka != okb {
				return oka // 有數字者在前
			}
			return serialItems[a].Name < serialItems[b].Name
		})
		out = append(out, serialItems...)
	}
	if len(couponDefIDs) > 0 {
		couponItems, err := s.repo.GetCouponDefPreview(ctx, couponDefIDs)
		if err != nil {
			return nil, err
		}
		for i := range couponItems {
			couponItems[i].ProbBP = couponProbBP[couponItems[i].refID]
		}
		out = append(out, couponItems...)
	}
	if len(bundleItems) > 0 {
		// 每個 bundleItem 各自查它涉及的 group 顯示欄位＋面額，合併成一張卡（見上方 case "serial" 分支
		// 註解：all-or-nothing 之下不比照兩層抽獎逐面額拆卡）。逐 item 各自查詢（而非像上面 groupIDs/
		// couponDefIDs 那樣攢成一次查詢）：組合包數量在賽事設定中通常很少（P1 用例僅 LINE POINTS 少數
		// 幾組），且每個 bundleItem 的 group 組合彼此不同，攢在一起查完還要拆回各自 item 反而更複雜，
		// 不值得為此優化。name/icon/description 的合併演算法刻意與 roll.go grantSerialBundle 算
		// bundle_label 的邏輯一致（商家名+總額，商家未指定則用「LINE POINTS」前綴）——玩家事前看到的
		// 卡片名稱應該跟實際中獎後拿到的一致，不要兩邊各算一套。
		for _, item := range bundleItems {
			bundleGroupIDs := make([]string, len(item.Bundle))
			countByGroup := map[string]int{}
			for i, e := range item.Bundle {
				bundleGroupIDs[i] = e.GroupID
				countByGroup[e.GroupID] = e.Count
			}
			meta, err := s.repo.GetRewardBundlePreviewMeta(ctx, bundleGroupIDs)
			if err != nil {
				return nil, err
			}
			total := 0
			merchantName, desc, icon := "", "", ""
			for _, gid := range bundleGroupIDs {
				m := meta[gid]
				total += m.FaceValue * countByGroup[gid]
				if merchantName == "" {
					merchantName = m.MerchantName
				}
				if desc == "" {
					desc = m.Description
				}
				if icon == "" {
					icon = m.IconURL
				}
			}
			out = append(out, RewardPreviewItem{
				Kind: "serial", Name: activityreward.FormatBundleLabel(merchantName, total),
				IconURL: icon, Description: desc, ProbBP: item.ProbBP,
			})
		}
	}
	return out, nil
}

// bundleDedupKey 把一個組合包 item 的 entries（group_id+count）序列化成穩定字串，供
// buildRewardPreviewItems 去重「同一組合設定被多個 item 重複引用」時卡片重複顯示（見該函式 case
// "serial" 分支）。逐 entry 依原始設定順序串接、不重排序——同一組合包在後台設定的 entries 順序理論上
// 固定，不需要額外排序也能保證同一組合每次算出同一個 key。
func bundleDedupKey(entries []activityreward.BundleEntry) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.GroupID)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(e.Count))
		b.WriteByte('|')
	}
	return b.String()
}

// bundlePreviewRow GetRewardBundlePreviewMeta 單筆序號組展示欄位，供 buildRewardPreviewItems 組合包分支
// 合併計算用。
type bundlePreviewRow struct {
	MerchantName, Description, IconURL string
	FaceValue                          int
}

// GetRewardBundlePreviewMeta 依組合包 entries 的 group_id 清單查前台展示需要的欄位（商家名稱/圖示/說明/
// 結構化面額），供 buildRewardPreviewItems 組合包分支合併成單張卡片。與 GetRewardSerialGroupPreview
// 的差異：多查 face_value（migration 149 結構化面額）——組合包合併總額屬於「獎勵大小」而非「中獎機率」，
// 比照 coupon 已揭露面額（NT$）的既有先例可對外，不算揭露義務範圍外的抽獎內部設定（庫存/權重才不可揭露）。
func (r *Repository) GetRewardBundlePreviewMeta(ctx context.Context, groupIDs []string) (map[string]bundlePreviewRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT g.id, COALESCE(m.name,''), COALESCE(g.description,''), COALESCE(g.icon_url,''), g.face_value
		FROM reward_serial_groups g
		LEFT JOIN reward_merchants m ON m.id = g.merchant_id
		WHERE g.id = ANY($1::uuid[])`, groupIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]bundlePreviewRow{}
	for rows.Next() {
		var id string
		var row bundlePreviewRow
		if err := rows.Scan(&id, &row.MerchantName, &row.Description, &row.IconURL, &row.FaceValue); err != nil {
			return nil, err
		}
		out[id] = row
	}
	return out, rows.Err()
}

// GetCouponDefPreview 依券種 id 清單查前台展示欄位（名稱＋面額），刻意不查 enabled/期限等後台管理欄位
// （機敏／與展示無關）。amount 格式化為「NT$ 100」比照 economy 類「100~500」的可讀字串慣例。refID 回填
// 券種 id（json:"-"，不外洩），供呼叫端 buildRewardPreviewItems 對回 couponProbBP 算好的機率。
func (r *Repository) GetCouponDefPreview(ctx context.Context, defIDs []string) ([]RewardPreviewItem, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, COALESCE(name,''), amount_cents FROM event_coupon_defs WHERE id = ANY($1::uuid[])`, defIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []RewardPreviewItem{}
	for rows.Next() {
		var id, name string
		var amountCents int
		if err := rows.Scan(&id, &name, &amountCents); err != nil {
			return nil, err
		}
		out = append(out, RewardPreviewItem{Kind: "coupon", Name: name, Amount: fmt.Sprintf("NT$ %d", amountCents/100), refID: id})
	}
	return out, rows.Err()
}

// GetRewardSerialGroupPreview 依序號組 id 清單查前台展示欄位（品項名稱/圖示/說明/商家名稱降階），
// 刻意不查庫存數與抽獎權重本身（機敏，不對外；換算過的「該面額實際機率」由呼叫端 buildRewardPreviewItems
// 算好後透過 refID 對回填入，見該函式）。name 優先用 item_label（面額/品項名稱），空則退回商家名稱。
func (r *Repository) GetRewardSerialGroupPreview(ctx context.Context, groupIDs []string) ([]RewardPreviewItem, error) {
	rows, err := r.db.Query(ctx, `
		SELECT g.id, COALESCE(g.item_label,''), COALESCE(g.icon_url,''), COALESCE(g.description,''), COALESCE(m.name,'')
		FROM reward_serial_groups g
		LEFT JOIN reward_merchants m ON m.id = g.merchant_id
		WHERE g.id = ANY($1::uuid[])`, groupIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []RewardPreviewItem{}
	for rows.Next() {
		var id, itemLabel, iconURL, description, merchantName string
		if err := rows.Scan(&id, &itemLabel, &iconURL, &description, &merchantName); err != nil {
			return nil, err
		}
		name := itemLabel
		if name == "" {
			name = merchantName
		}
		out = append(out, RewardPreviewItem{Kind: "serial", Name: name, IconURL: iconURL, Description: description, refID: id})
	}
	return out, rows.Err()
}
