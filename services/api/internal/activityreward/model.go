// Package activityreward 活動獎勵系統 P2：即時獎勵設定（全域模板 + 每場挑戰 config）與完成觸發機率 roll。
// 刻意獨立成 leaf 套件（比照 internal/wallet、internal/vip 的模式）：只依賴 wallet/vip 兩個更底層的 leaf
// 套件與資料庫驅動，完全不 import internal/race，讓 race 套件可以反向 import 本套件使用其型別、呼叫
// RollAndGrant，不會造成 import cycle。
//
// 兩種即得獎勵形式（設計見 memory activity-reward-system）：
//   - 經濟類（exp/dp/gp/vip）：中獎直接入帳，不進 user_rewards。
//   - 序號類（serial）：以商家為單位的兩層抽獎——先判定該商家中不中獎，中了才在商家旗下「有庫存」的
//     序號組（面額）中依權重加權抽一組，取一枚可用序號配發，寫入 user_rewards（活動獎勵錢包）。
//     RollAndGrant 只回傳「這次呼叫實際發過序號」的 group id 清單，供呼叫端在交易 Commit 成功後另行
//     用最新真值查庫存、判斷要不要發低庫存通知——本套件刻意不在 tx 內做「跌破門檻」判斷：READ
//     COMMITTED 下同一交易看不到其他併發交易「已 UPDATE 但未 commit」的扣減，在 tx 內反推會導致同一次
//     真正跨越門檻被永久漏報，或多筆併發交易各自誤判成「剛跨越」而洗頻（見 race.MarkAttemptCompletedAndGrant）。
//
// 冪等由呼叫端保證：RollAndGrant 只應該在「這次呼叫確定是首次判定完成」時被呼叫一次（見 race 套件
// personal_progress.go 的 CAS 完成點），本套件不自建冪等機制。
package activityreward

import (
	"context"
	"strings"
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
//
// serial 類是「以商家為單位的兩層抽獎」（見 memory activity-reward-system 設計）：ProbBP 是該商家
// 「給不給獎」的機率；中了之後才在 Denominations（商家旗下序號組清單，各自帶抽獎權重）中，只在
// 「目前有庫存」的面額裡依權重抽一組實際配發（見 roll.go grantSerialTwoLayer）。
type RewardItem struct {
	Type   string `json:"type"`            // exp|dp|gp|vip|serial
	Min    int    `json:"min,omitempty"`   // exp/dp/gp：均勻隨機區間下界（含）
	Max    int    `json:"max,omitempty"`   // exp/dp/gp：均勻隨機區間上界（含）
	Days   int    `json:"days,omitempty"`  // vip：固定天數
	ProbBP int    `json:"prob_bp"`         // 中獎機率，萬分位（10000=100%）；serial：該商家「給不給獎」的機率

	// SerialGroupID 【已過時，僅供向後相容】serial 類舊格式的單一序號組。新設定一律用 MerchantID +
	// Denominations；只有舊資料 Denominations 為空時，validDenominations() 才會把這個欄位當成
	// 「單一面額、權重固定 1」帶入抽獎池，讓改版前已設定好的賽事不會壞掉。
	SerialGroupID string `json:"serial_group_id,omitempty"`
	// MerchantID serial 類：指定商家（兩層抽獎第一層 ProbBP 判定的對象；純標示用途，實際抽獎池由
	// Denominations 決定，不會另外拿 MerchantID 去查序號組）。
	MerchantID string `json:"merchant_id,omitempty"`
	// Denominations serial 類：該商家旗下可能中獎的序號組與各自的抽獎權重（兩層抽獎第二層，見
	// grantSerialTwoLayer）；Weight<=0 視為不列入抽獎池。
	Denominations []RewardDenom `json:"denominations,omitempty"`
}

// RewardDenom 商家旗下一個序號組（面額）在兩層抽獎第二層的加權設定。
type RewardDenom struct {
	GroupID string `json:"group_id"`
	Weight  int    `json:"weight"`
}

// validDenominations 回傳 it.Denominations 中「有效」的面額（GroupID 非空且 Weight>0）；若因此為空、
// 但舊格式 SerialGroupID 非空 → 回退成單一面額（權重固定 1），讓改版前只設定過 SerialGroupID 的賽事
// 抽獎行為維持不變（等同「該商家只有一種面額、100% 抽中」）。皆空時回 nil（呼叫端視為此項目未設定
// 任何可抽的面額）。
func (it *RewardItem) validDenominations() []RewardDenom {
	var out []RewardDenom
	for _, d := range it.Denominations {
		if strings.TrimSpace(d.GroupID) != "" && d.Weight > 0 {
			out = append(out, d)
		}
	}
	if len(out) == 0 && strings.TrimSpace(it.SerialGroupID) != "" {
		out = append(out, RewardDenom{GroupID: it.SerialGroupID, Weight: 1})
	}
	return out
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
