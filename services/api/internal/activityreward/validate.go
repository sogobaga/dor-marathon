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
