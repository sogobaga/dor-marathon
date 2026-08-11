package activityreward

// validate.go 獎勵項目/設定的參數驗證，模板 CRUD 與 race 套件建立/更新賽事(reward_config)共用
// （race.normalizeRequest 對 personal 模式的賽事呼叫 RewardConfig.Validate）。

import (
	"fmt"
	"strings"
)

var validRewardTypes = map[string]bool{"exp": true, "dp": true, "gp": true, "vip": true, "serial": true}

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
		if strings.TrimSpace(it.SerialGroupID) == "" {
			return fmt.Errorf("reward item serial: requires serial_group_id")
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
