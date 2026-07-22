package race

import (
	"encoding/json"
	"fmt"
	"time"
)

// CancellationPolicy 取消退費政策：截止天數 + 依距賽事天數分級的退費比例。
// 可存在 races.config.cancellation_policy（見 RaceConfig）覆寫系統預設（見 app_settings key
// "cancellation_policy"）；兩者皆無則退回 defaultCancellationPolicy。
type CancellationPolicy struct {
	DeadlineDays int                `json:"deadline_days"` // 賽事開始前幾天截止申請取消
	Tiers        []CancellationTier `json:"tiers"`         // 依 days_before 由大到小排序後比對
}

// CancellationTier 一個退費級距：距賽事開始 >= DaysBefore 天申請 → 退 Ratio%。
type CancellationTier struct {
	DaysBefore int `json:"days_before"`
	Ratio      int `json:"ratio"` // 退費百分比 0–100
}

// defaultCancellationPolicy 程式內建預設（系統設定 app_settings 查無資料時的最後防線）：
// 截止 14 天；距賽事 ≥30 天退 90%、≥14 天退 50%。與 migrations/095 寫入 app_settings 的值一致。
var defaultCancellationPolicy = CancellationPolicy{
	DeadlineDays: 14,
	Tiers: []CancellationTier{
		{DaysBefore: 30, Ratio: 90},
		{DaysBefore: 14, Ratio: 50},
	},
}

// ResolveCancellationPolicy 解析取消政策，依序：該賽事覆寫（override 非 nil）→ 系統預設
// （systemDefaultJSON，來自 app_settings key "cancellation_policy" 的原始字串）→ 程式內建預設。
// 純函式，不觸碰 DB——呼叫端自行查好 override / systemDefaultJSON 再傳入。
func ResolveCancellationPolicy(override *CancellationPolicy, systemDefaultJSON string) CancellationPolicy {
	if override != nil {
		return *override
	}
	if systemDefaultJSON != "" {
		var p CancellationPolicy
		if err := json.Unmarshal([]byte(systemDefaultJSON), &p); err == nil {
			return p
		}
	}
	return defaultCancellationPolicy
}

// CancellationCalc ComputeCancellation 的計算結果。
type CancellationCalc struct {
	DaysBefore        int    `json:"days_before"`
	CanCancel         bool   `json:"can_cancel"`
	BlockedReason     string `json:"blocked_reason,omitempty"`
	Ratio             int    `json:"ratio"`
	RefundAmountCents int    `json:"refund_amount_cents"`
}

// floorDivDuration 對兩個 time.Duration 做「無條件捨去（floor）」整數除法，取代直接用 Go 的
// int64 截斷除法（截斷是向零捨去，對負數會得到錯誤結果，例如 -0.5 天應該 floor 成 -1 天而不是 0）。
// 全程使用整數（時間差的奈秒數），不經過浮點數，避免邊界值因浮點誤差算錯。
func floorDivDuration(a, b time.Duration) int {
	q := int64(a) / int64(b)
	if int64(a)%int64(b) != 0 && (a < 0) != (b < 0) {
		q--
	}
	return int(q)
}

// ComputeCancellation 純函式：依賽事開始時間、現在時間、（可退費用基準的）訂單金額、政策，
// 算出這筆取消申請當下的可取消性與退費結果。
//
//   - daysBefore = floor((start_date - now) / 24h)：無條件捨去，賽事當天為 0，已開始為負數。
//   - daysBefore < 0（賽事已開始）→ 不可取消。
//   - 否則 daysBefore < policy.DeadlineDays（已過取消申請截止）→ 不可取消。
//   - ratio：tiers 依 DaysBefore 由大到小排序後，取第一個 daysBefore >= tier.DaysBefore 的 ratio；
//     都不符合則 0（此時仍可能可取消，只是退費比例為 0）。
//   - refundAmountCents = orderTotalCents * ratio / 100，整數運算無條件捨去，不可用浮點。
//
// orderTotalCents 由呼叫端決定「可退費用基準」是什麼——例如訂單尚未付款（pending）時應傳入 0
// （沒有錢可退，即使 ratio > 0 也退 0 元），已付款則傳入訂單實付總額。
func ComputeCancellation(startDate, now time.Time, orderTotalCents int, policy CancellationPolicy) CancellationCalc {
	daysBefore := floorDivDuration(startDate.Sub(now), 24*time.Hour)

	if daysBefore < 0 {
		return CancellationCalc{
			DaysBefore:    daysBefore,
			CanCancel:     false,
			BlockedReason: "賽事已開始，無法申請取消",
		}
	}
	if daysBefore < policy.DeadlineDays {
		return CancellationCalc{
			DaysBefore:    daysBefore,
			CanCancel:     false,
			BlockedReason: fmt.Sprintf("已超過取消申請截止（須於賽事開始前 %d 天申請）", policy.DeadlineDays),
		}
	}

	ratio := 0
	tiers := make([]CancellationTier, len(policy.Tiers))
	copy(tiers, policy.Tiers)
	// 依 DaysBefore 由大到小排序（不修改呼叫端傳入的原始切片）；簡單插入排序即可，tiers 數量很小。
	for i := 1; i < len(tiers); i++ {
		for j := i; j > 0 && tiers[j].DaysBefore > tiers[j-1].DaysBefore; j-- {
			tiers[j], tiers[j-1] = tiers[j-1], tiers[j]
		}
	}
	for _, t := range tiers {
		if daysBefore >= t.DaysBefore {
			ratio = t.Ratio
			break
		}
	}
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 100 {
		ratio = 100
	}

	refundAmountCents := 0
	if orderTotalCents > 0 && ratio > 0 {
		refundAmountCents = orderTotalCents * ratio / 100
	}

	return CancellationCalc{
		DaysBefore:        daysBefore,
		CanCancel:         true,
		Ratio:             ratio,
		RefundAmountCents: refundAmountCents,
	}
}
