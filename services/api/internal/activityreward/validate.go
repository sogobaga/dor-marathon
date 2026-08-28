package activityreward

// validate.go 獎勵項目/設定的參數驗證，模板 CRUD 與 race 套件建立/更新賽事(reward_config)共用
// （race.normalizeRequest 對 personal 模式的賽事呼叫 RewardConfig.Validate）。

import (
	"fmt"
	"strings"
)

var validRewardTypes = map[string]bool{"exp": true, "dp": true, "gp": true, "vip": true, "serial": true, "coupon": true}

// Validate 驗證單一獎勵項目參數是否合理。
func (it *RewardItem) Validate() error {
	if !validRewardTypes[it.Type] {
		return fmt.Errorf("invalid reward item type: %q", it.Type)
	}
	if it.ProbBP <= 0 || it.ProbBP > 10000 {
		return fmt.Errorf("reward item %s: prob_bp must be within (0,10000]", it.Type)
	}
	switch it.Type {
	case "exp", "dp", "gp":
		if it.Min <= 0 || it.Max < it.Min {
			return fmt.Errorf("reward item %s: requires min>0 and max>=min", it.Type)
		}
	case "vip":
		if it.Days <= 0 {
			return fmt.Errorf("reward item vip: requires days>0")
		}
	case "serial":
		if len(it.Bundle) > 0 {
			// 固定組合包（migration 149）：與 Denominations（加權隨機抽一個）互斥——同一 item 只能走
			// 其中一種發放邏輯，兩者同時非空會讓 grantSerialBundle/grantSerialTwoLayer 該選誰失去定義。
			if len(it.Denominations) > 0 {
				return fmt.Errorf("reward item serial: bundle and denominations are mutually exclusive")
			}
			if len(it.Bundle) > 20 {
				return fmt.Errorf("reward item serial: bundle supports at most 20 entries")
			}
			seenBundleGroups := map[string]bool{}
			for i, e := range it.Bundle {
				gid := strings.TrimSpace(e.GroupID)
				if gid == "" {
					return fmt.Errorf("reward item serial: bundle entry %d requires group_id", i)
				}
				if e.Count < 1 {
					return fmt.Errorf("reward item serial: bundle entry %d requires count>=1", i)
				}
				if seenBundleGroups[gid] {
					// 同一 bundle 內不可重複引用同一序號組：grantSerialBundle 逐 entry 各自對該 group 做
					// 「UPDATE...RETURNING 搭配 FOR UPDATE SKIP LOCKED」搶碼，同一交易內重複搶同一組會
					// 讓兩個 entry 各自的 LIMIT 查詢在庫存邊界互相干擾（詳見 roll.go 函式註解），故禁止。
					return fmt.Errorf("reward item serial: bundle entry %d duplicates group_id %s", i, gid)
				}
				seenBundleGroups[gid] = true
			}
			// 「Bundle 內 group 需同一商家」需要查 DB 才能確認每個 group_id 實際隸屬哪個商家——本函式是
			// 純結構驗證（比照上面 coupon 的取捨：不在此處查 DB），這項規則因此挪到 roll.go
			// grantSerialBundle 執行期查出每個 entry 的 merchant_id 後跨 entry 比對，不一致視為設定
			// 錯誤直接 fail（非 all-or-nothing 的庫存不足情境，不算「商家庫存不足」，不觸發
			// serial_bundle_shortage 告警，而是回一般錯誤）。
			return nil
		}
		// 兩層抽獎：至少要有一個「有效」面額（GroupID 非空且 Weight>0）才有東西可抽，否則中獎機率
		// 判定過了卻永遠抽不出面額，等於這個項目形同虛設（見 model.go validDenominations，內含
		// 舊格式 SerialGroupID 的向後相容回退）。
		if len(it.validDenominations()) == 0 {
			return fmt.Errorf("reward item serial: requires at least one denomination (group_id + weight>0) or serial_group_id")
		}
	case "coupon":
		// 純結構驗證（比照 serial 只驗「有沒有指定面額/組」，不在此處查 DB）：實際「券種是否存在／
		// 啟用／未過期」由後台表單只列出可選券種把關（見 RaceForm.tsx 的可選清單過濾），中獎當下再由
		// roll.go grantCoupon 對 DB 現況做最終判斷（不合格則該項跳過，不影響其他項目）。
		if strings.TrimSpace(it.CouponDefID) == "" {
			return fmt.Errorf("reward item coupon: requires coupon_def_id")
		}
	}
	return nil
}

// Validate 驗證整組即時獎勵設定；c 為 nil 或 items 為空視為合法（＝此賽事/模板不設定即時獎勵，
// 比照 race.ChallengeRule 對其餘模式清空為 nil 的處理方式，reward_config 本身在 personal 模式下也非必填）。
func (c *RewardConfig) Validate() error {
	if c == nil {
		return nil
	}
	for i := range c.Items {
		if err := c.Items[i].Validate(); err != nil {
			return fmt.Errorf("item %d: %w", i, err)
		}
	}
	return nil
}
