// Package stamina 體力值(SP)系統的純數值計算（無 DB 相依，供 activity/profile 等套件共用）。
// 設計：SP 上限依等級成長；跑完依「距離×強度」扣 SP；依「跑步水準」以 30–120 分/點恢復；扣到 0 凍結 6 小時。
package stamina

import "time"

// FreezeDuration 過度訓練(扣到 0)後恢復凍結時長。
const FreezeDuration = 6 * time.Hour

// SPMax 依等級的體力上限：Lv1=24，每 5 級 +1。
func SPMax(level int) int {
	if level < 1 {
		level = 1
	}
	return 24 + (level-1)/5
}

// RecoveryMin 依跑步水準(0–100)的「每恢復 1 點所需分鐘數」：水準 0→120 分、100→30 分（clamp 30–120）。
func RecoveryMin(fitness int) int {
	m := 120 - fitness*9/10
	if m < 30 {
		m = 30
	}
	if m > 120 {
		m = 120
	}
	return m
}

// IntensityRate 依「均速相對閾值配速 T（秒/公里）」判定該趟強度係數(SP/km)。
// d = 均速 − T（越慢 d 越大）。慢→恢復(0.5)；快於閾值→間歇(2.2)。
func IntensityRate(avgPaceS, thresholdPaceS int) float64 {
	d := avgPaceS - thresholdPaceS
	switch {
	case d > 120:
		return 0.5 // 恢復跑
	case d > 75:
		return 0.7 // 輕鬆跑 / LSD
	case d > 30:
		return 1.0 // 有氧 / 漸速
	case d > 0:
		return 1.4 // 節奏跑
	case d > -20:
		return 1.7 // 乳酸閾值 / 變速
	default:
		return 2.2 // 間歇 / 亞索 / 挪威4x4 / 金字塔
	}
}

// WorkoutRate 結構化課表的強度係數(SP/km)，依 workout_kind（供課表挑戰用，比 pace 更準）。
func WorkoutRate(kind string) float64 {
	switch kind {
	case "recovery":
		return 0.5
	case "easy", "lsd":
		return 0.7
	case "aerobic", "progression":
		return 1.0
	case "tempo":
		return 1.4
	case "threshold", "fartlek", "variable":
		return 1.7
	case "interval", "yasso800", "norwegian4x4", "pyramid":
		return 2.2
	default:
		return 1.0
	}
}

// Cost 一趟跑步的 SP 扣除：距離(km)×強度係數，無條件進位，最低 1。
func Cost(distanceKm, rate float64) int {
	c := int(distanceKm*rate + 0.9999)
	if c < 1 {
		c = 1
	}
	return c
}

// Accrue 懶惰恢復：算從 updatedAt 到 now 累積恢復的 SP（凍結期間不恢復、不超過上限）。
// 回傳新的 sp 與新的錨點時間（滿了錨點=now；未滿則前進整數週期、保留餘數）。
func Accrue(sp, spMax int, updatedAt time.Time, freezeUntil *time.Time, now time.Time, recoveryMin int) (int, time.Time) {
	if sp >= spMax {
		return spMax, now
	}
	start := updatedAt
	if freezeUntil != nil && freezeUntil.After(start) {
		start = *freezeUntil // 凍結期間不累積，恢復從凍結結束才開始
	}
	if !now.After(start) {
		return sp, updatedAt
	}
	per := time.Duration(recoveryMin) * time.Minute
	gained := int(now.Sub(start) / per)
	if gained <= 0 {
		return sp, updatedAt
	}
	newSP := sp + gained
	if newSP >= spMax {
		return spMax, now
	}
	return newSP, start.Add(time.Duration(gained) * per)
}

// NextRecoverSeconds 距離下一點恢復還要幾秒（供前台倒數；已滿回 0）。
func NextRecoverSeconds(sp, spMax int, updatedAt time.Time, freezeUntil *time.Time, now time.Time, recoveryMin int) int {
	if sp >= spMax {
		return 0
	}
	start := updatedAt
	if freezeUntil != nil && freezeUntil.After(start) {
		start = *freezeUntil
	}
	per := time.Duration(recoveryMin) * time.Minute
	// 下一點的時間 = start + (已累積整數週期+1)×per
	elapsed := now.Sub(start)
	if elapsed < 0 {
		elapsed = 0
	}
	done := elapsed / per
	next := start.Add((done + 1) * per)
	secs := int(next.Sub(now).Seconds())
	if secs < 0 {
		secs = 0
	}
	return secs
}
