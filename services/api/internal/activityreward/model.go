// Package activityreward 活動獎勵系統 P2：即時獎勵設定（全域模板 + 每場挑戰 config）與完成觸發機率 roll。
// 刻意獨立成 leaf 套件（比照 internal/wallet、internal/vip 的模式）：只依賴 wallet/vip 兩個更底層的 leaf
// 套件與資料庫驅動，完全不 import internal/race，讓 race 套件可以反向 import 本套件使用其型別、呼叫
// RollAndGrant，不會造成 import cycle。
//
// 兩種即得獎勵形式（設計見 memory activity-reward-system）：
//   - 經濟類（exp/dp/gp/vip）：中獎直接入帳，不進 user_rewards。
//   - 序號類（serial）：從指定序號組取一枚可用序號配發，寫入 user_rewards（活動獎勵錢包，P3 待上線）。
//
// 冪等由呼叫端保證：RollAndGrant 只應該在「這次呼叫確定是首次判定完成」時被呼叫一次（見 race 套件
// personal_progress.go 的 CAS 完成點），本套件不自建冪等機制。
package activityreward

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Execer 是本套件需要的資料庫操作子集：*pgxpool.Pool 與 pgx.Tx 皆滿足。比照 internal/wallet、
// internal/vip 的 Execer 慣例，但額外需要 QueryRow——序號類獎勵需要 SELECT/UPDATE...RETURNING 取得可用
// 序號與序號組的顯示欄位（面額/商家/使用說明…），不能只用 Exec。呼叫端傳入既有 pgx.Tx 時，本套件與
// wallet.AwardGP/AwardDP、vip.Extend 的所有 SQL 都會在同一交易內執行，確保「判定完成」與「發獎」同進退
// （呼叫端傳入的 Execer 值可直接傳給 wallet.AwardGP 等要求 wallet.Execer 的函式——Go 介面依方法集合結
// 構相容，本介面方法集是其超集）。
type Execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// RewardItem 即時獎勵設定的一個項目，逐項獨立機率 roll（見 RollAndGrant）。
type RewardItem struct {
	Type          string `json:"type"`                      // exp|dp|gp|vip|serial
	Min           int    `json:"min,omitempty"`             // exp/dp/gp：均勻隨機區間下界（含）
	Max           int    `json:"max,omitempty"`             // exp/dp/gp：均勻隨機區間上界（含）
	Days          int    `json:"days,omitempty"`            // vip：固定天數
	ProbBP        int    `json:"prob_bp"`                   // 中獎機率，萬分位（10000=100%）
	SerialGroupID string `json:"serial_group_id,omitempty"` // serial：指定序號組
}

// RewardConfig 一組即時獎勵設定（全域模板的 items、或每場賽事 races.reward_config 皆用此形狀）。
type RewardConfig struct {
	Items []RewardItem `json:"items"`
}

// GrantedReward 一次 RollAndGrant 呼叫中「實際中獎並成功發放」的單筆結果，供呼叫端回傳給前端顯示
// （P2 先把資料回出來，P3 彈窗才用）。
type GrantedReward struct {
	Type      string `json:"type"`
	Amount    int    `json:"amount,omitempty"`     // exp/dp/gp
	Days      int    `json:"days,omitempty"`       // vip
	ItemLabel string `json:"item_label,omitempty"` // serial
	Code      string `json:"code,omitempty"`       // serial
}

// Template 全域即時獎勵模板：建立/編輯挑戰賽事時可套用進 reward_config 再微調（套用後即與模板脫鉤，
// 之後修改模板不會回溯影響已套用過的賽事）。
type Template struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Items     []RewardItem `json:"items"`
	CreatedAt time.Time    `json:"created_at"`
}
