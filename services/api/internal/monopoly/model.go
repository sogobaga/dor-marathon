// Package monopoly 環台大富翁 Phase 1：盤面遊戲（擲骰前進 + 繞圈）。
// 比照 internal/partner 的三層結構（model/repository/service/handler）。
package monopoly

// boardSize 盤面總格數：0=START，1..45 為格子，繞一圈=46 格（由前端 BOARD_COORDS 常數決定座標）。
const boardSize = 46

// chancePositions / destinyPositions 機會格／命運格（固定位置，由底圖決定）。
// Phase 1 停在這兩類格子只回一個 placeholder 旗標（draw_pending），真正抽卡是 Phase 3 才做。
var chancePositions = map[int]bool{6: true, 15: true, 25: true, 35: true}
var destinyPositions = map[int]bool{10: true, 18: true, 30: true, 39: true}

func landedOnFor(pos int) string {
	if chancePositions[pos] {
		return "chance"
	}
	if destinyPositions[pos] {
		return "destiny"
	}
	return "normal"
}

// PlayerState GET /monopoly/state 回應：目前棋子位置 + GP 餘額 + 目前擲骰成本（後台可調）。
type PlayerState struct {
	Position      int `json:"position"`
	LapsCompleted int `json:"laps_completed"`
	GPBalance     int `json:"gp_balance"`
	DiceGPCost    int `json:"dice_gp_cost"`
}

// RollResult POST /monopoly/roll 回應：一次擲骰的完整結果。
type RollResult struct {
	Roll        int    `json:"roll"` // 伺服器決定的點數 1..6（前端動畫最終須停在這個值）
	From        int    `json:"from"`
	To          int    `json:"to"`
	LapsGained  int    `json:"laps_gained"`   // 本次是否繞圈（可能一次擲骰跨圈，理論上恆為 0 或 1，但不假設上限）
	LandedOn    string `json:"landed_on"`     // normal | chance | destiny
	LapRewardGP int    `json:"lap_reward_gp"` // 本次因繞圈實際發放的 GP（0=沒發）
	GPBalance   int    `json:"gp_balance"`    // 扣款+發獎後的最終餘額
	DrawPending bool   `json:"draw_pending"`  // true=停在機會/命運格，抽卡功能 Phase 3 才開放，前端只顯示 placeholder
}
