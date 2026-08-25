package virtualrunner

import "math/rand"

// jitterPct 建立/等級變更重帶入時的個體能力值抖動幅度（±5%，見套件契約「能力值從 preset 帶入並
// ±5% 抖動」）。
const jitterPct = 0.05

// jitterFloat 在 [v*(1-pct), v*(1+pct)] 區間內均勻抖動一個值。純函式，呼叫端注入 rng 供測試。
func jitterFloat(rng *rand.Rand, v, pct float64) float64 {
	if v == 0 {
		return 0
	}
	lo := v * (1 - pct)
	span := v * 2 * pct
	return lo + rng.Float64()*span
}

// jitterAbility 由等級範本算出一組個體抖動後的能力值。pace_fast_s/pace_slow_s 各自獨立抖動後
// 仍需保底維持 fast < slow（範本本身的 fast/slow 差距在抖動幅度下理論上可能被抖到反轉——例如
// beginner 480-510 只差 30 秒、±5% 抖動最大可達 ±24-25.5 秒，雖機率低但非不可能——因此抖動後
// 顯式檢查，若反轉或相等就把 fast 夾回 slow-1，確保契約「fast<slow」恆成立）。
func jitterAbility(preset LevelPreset, rng *rand.Rand) Ability {
	avgKm := jitterFloat(rng, preset.AvgKm, jitterPct)
	monthlyKm := jitterFloat(rng, preset.MonthlyKm, jitterPct)
	fastS := int(jitterFloat(rng, float64(preset.PaceFastS), jitterPct))
	slowS := int(jitterFloat(rng, float64(preset.PaceSlowS), jitterPct))
	if fastS >= slowS {
		fastS = slowS - 1
	}
	if fastS < 1 {
		fastS = 1
	}
	return Ability{AvgKm: avgKm, MonthlyKm: monthlyKm, PaceFastS: fastS, PaceSlowS: slowS}
}
