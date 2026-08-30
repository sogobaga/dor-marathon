// Package gpscalib：App GPS 距離校正估計器（以穿戴裝置匯入的活動為參考，log 域 Huber 穩健回歸 +
// 往 k=1 收縮的先驗 + 距離權重 + 45 天半衰期時間衰減 + 邊緣對齊/時長比例閘門）。
//
// 本檔（estimator.go）刻意保持零 DB 依賴（純函式）：候選配對由呼叫端（service.go 的 Recompute）
// 查出後餵入 Gate → EstimateWindow → Publish（或用 Compute 一次跑完），方便單元測試覆蓋 24 組真實
// fixture 資料與各邊界條件；DB 讀寫/觸發時機/白名單套用等副作用一律在 service.go。
//
// 對應「GPS 距離校正係數 — 最終規格」§1-2；回測腳本 scratchpad/calib_final.py（Python 版一比一對照，
// 常數、閘門順序、Huber IRLS 迭代與本檔逐項相同，供交叉驗證）。修改任一常數或演算法步驟前，必須
// 同步更新該腳本重新回測 24 組 fixture，並更新本套件的 estimator_test.go 期望值。
package gpscalib

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strings"
	"time"
)

// --- 常數（規格 §2.1，估計器行為由程式碼常數決定，不開放 app_settings 調參——改參數等同改演算法，
// 必須連同回測腳本一起審視，不是「調一個數字」的事）---
const (
	CandStartGapMaxS = 600  // 候選 SQL：|GPS起−外部起| ≤ 10 分（撈候選用，不在 Gate 內）
	DurTol           = 0.05 // G3 partial：|ext_dur/gps_dur − 1| > 5%
	MinKm            = 3.0  // G4 short：兩側皆須 ≥ 3.0 km
	EdgeAbsS         = 20.0 // G5 edge：tol = max(20s, 1.5% × min(gps_dur, ext_dur))
	EdgeFrac         = 0.015
	HardLo           = 0.80 // G6 range：ext/gps 超出 [0.80,1.25] → 視為資料問題，不進估計
	HardHi           = 1.25
	DistRefKm        = 5.0 // 距離權重：w_d = clamp(gps_km/5, 0.5, 3.0)
	DistWMin         = 0.5
	DistWMax         = 3.0
	HalfLifeDays     = 45.0 // 時間衰減半衰期（以 activity_at 計）
	WindowDays       = 120  // 估計視窗：activity_at 在 now−120d 內
	WindowN          = 20   // 視窗內依 activity_at 取最新 20 組
	PriorMu          = 0.0  // 先驗：往 k=1 收縮，等同兩趟 5km 比值 1.0 的虛擬配對
	PriorW           = 2.0
	Sigma0           = 0.03  // n<5 時固定尺度
	SigmaMin         = 0.015 // n≥5：s = max(SigmaMin, 1.4826×MAD(y))
	SigmaMaxActive   = 0.06  // s > 6% → unstable（資料互相矛盾，不啟用）
	MADMinN          = 5
	HuberC           = 1.5
	IRLSIters        = 8
	NMinEff          = 4.0  // Σinlier_w ≥ 4 才 active
	StepMaxLn        = 0.02 // 每次 publish |Δln k| ≤ 2%（遲滯：避免單筆離群一次跳動過大）
	ClampLo          = 0.92 // 只准向下：k ∈ [0.92, 1.00]
	ClampHi          = 1.00
	DeadBandLn       = 0.01 // |ln k| < 1% → 直接視為 1.0（避免無意義的微幅係數）
	StaleDays        = 120
)

// Pair 一組候選配對（一趟 App GPS 原始紀錄 × 一筆外部來源活動），由 service.go 的 SQL 候選查詢
// 撈出後餵給 Gate。RawGpsKm 一律取自 gps_runs.distance_km（原始、未套校正）。
type Pair struct {
	GpsRunID, ExtActivityID string
	ExtSource               string // strava|garmin|coros
	RawGpsKm, ExtKm         float64
	// GpsDurS：GPS 端真實經過時間（含停等）。ExtDurS：外部端「經過時間」——service.go 的
	// candidateSQL 已 COALESCE(elapsed_s, duration_s)，只用於本檔的時間對齊（G3/G5），不是
	// activities.duration_s 的 moving_time 語意（那個口徑給其他讀取點用，這裡不碰）。
	GpsDurS, ExtDurS   int
	GpsStart, ExtStart time.Time // GPS 起 = gps_runs.ended_at − duration_s；外部起 = activities.recorded_at
	ExtFlagReason      string
}

// Gated 一組配對套完整個 G1-G7 閘門鏈後的結果。LogRatio/DistW 對所有配對都計算（即使被拒絕），
// 供「最近配對表」顯示比值；只有 Accepted=true 的才進入 EstimateWindow。
type Gated struct {
	Pair
	StartGapS, EndGapS int // GPS起−外部起、GPS迄−外部迄（秒）
	LogRatio, DistW    float64
	Accepted           bool
	Reason             string // ""(accepted) | flagged|ambiguous|partial|short|edge|range|other_source
}

// Estimate 視窗估計結果（Huber IRLS + 先驗收縮後的內部 log 域估計）。
type Estimate struct {
	LogMu, Sigma, NEff, EffWeight float64
	N                             int       // 視窗內筆數
	InlierW                       []float64 // 與 Window 同序
	Window                        []Gated
}

// Published 對外發佈的係數。
type Published struct {
	Factor float64
	Status string // warming|active|unstable|stale
}

// Result Compute 的整包輸出。
type Result struct {
	Gated []Gated
	Est   Estimate
	Pub   Published
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// median 回傳排序後中位數（偶數個取中間兩者平均）；不修改輸入切片。
func median(xs []float64) float64 {
	n := len(xs)
	if n == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// gapSeconds：StartGapS = GPS起 − 外部起；EndGapS = GPS迄 − 外部迄（GPS迄/外部迄由起+時長推算）。
func gapSeconds(p Pair) (startGap, endGap float64) {
	gpsEnd := p.GpsStart.Add(time.Duration(p.GpsDurS) * time.Second)
	extEnd := p.ExtStart.Add(time.Duration(p.ExtDurS) * time.Second)
	startGap = p.GpsStart.Sub(p.ExtStart).Seconds()
	endGap = gpsEnd.Sub(extEnd).Seconds()
	return
}

func edgeTol(p Pair) float64 {
	return math.Max(EdgeAbsS, EdgeFrac*float64(minInt(p.GpsDurS, p.ExtDurS)))
}

// Gate 依序套 G1-G7 閘門：
//
//	G1 flagged：ExtFlagReason=="cross_account_duplicate"（跨帳號洗數據作弊）→ 拒絕；其餘 benign
//	   的 duplicate/multi_device_duplicate/cross_source_duplicate 允許進入後續閘門。
//	G2 other_source：ExtSource != refSource——配對仍存表（LogRatio/DistW 已算好供顯示），但不進
//	   本次估計（校準只對單一參考來源估）。**必須排在 G3 ambiguous 之前**（對抗式審查修正，見
//	   low-1 finding）：1:1 最佳比對只在「參考來源」內部競爭才有意義——若把全部來源混在一起比
//	   |gap| 最小，使用者同時連 Strava(手錶自動同步)＋COROS(直連) 時，同一趟 GPS 可能被 gap 更
//	   小的非參考來源候選「贏走」best-match 資格，導致真正該被採用的參考來源候選被誤標
//	   ambiguous、而不是正確的 other_source；兩者都不會被 accepted，但誤標 ambiguous 的那筆即使
//	   使用者之後改變參考來源也無法被正確理解為「換來源後應該會過」。
//	G3 ambiguous：1:1 雙向最佳比對——僅在 ExtSource==refSource 的候選之間比較，一個配對必須同時
//	   是「其 GpsRunID 底下 |gap| 總和最小」且「其 ExtActivityID 底下 |gap| 總和最小」才留，任一
//	   方向不是最佳即拒絕（防止同一 GPS 趟被誤配到多筆外部活動、或反向同一外部活動被誤配到多趟
//	   GPS）。
//	G4 partial：|ext_dur/gps_dur − 1| > 5%（App 晚開/早停等部分紀錄，7 月離散資料的主因）。
//	G5 short：兩側距離皆須 ≥ 3.0 km（短程單一 GPS 跳點/雜訊比值不穩定）。
//	G6 edge：|StartGapS| 或 |EndGapS| 超過 max(20s, 1.5%×min(duration)) 容差——雙方起訖沒有真正
//	   對齊到同一趟（例如中途加入/提早結束的另一段路程恰好距離相近）。
//	G7 range：ext/gps 比值超出 [0.80, 1.25] 視為資料本身有問題（非單純裝置系統性誤差可解釋）。
//
// 每筆配對的 LogRatio/DistW 無論是否被任何閘門拒絕都會計算（gps_calib_pairs 表 NOT NULL 需要，
// 且「最近配對表」UI 需要對被拒絕的配對也顯示比值）。
func Gate(pairs []Pair, refSource string) []Gated {
	type best struct {
		tot float64
		id  string
	}
	bestByGps := map[string]best{}
	bestByExt := map[string]best{}
	for _, p := range pairs {
		if p.ExtSource != refSource {
			continue // 非參考來源不參與 1:1 最佳比對（見上方 G2/G3 順序註解）
		}
		sg, eg := gapSeconds(p)
		t := math.Abs(sg) + math.Abs(eg)
		if cur, ok := bestByGps[p.GpsRunID]; !ok || t < cur.tot {
			bestByGps[p.GpsRunID] = best{t, p.ExtActivityID}
		}
		if cur, ok := bestByExt[p.ExtActivityID]; !ok || t < cur.tot {
			bestByExt[p.ExtActivityID] = best{t, p.GpsRunID}
		}
	}

	out := make([]Gated, 0, len(pairs))
	for _, p := range pairs {
		sg, eg := gapSeconds(p)
		g := Gated{
			Pair:      p,
			StartGapS: int(math.Round(sg)),
			EndGapS:   int(math.Round(eg)),
			DistW:     clamp(p.RawGpsKm/DistRefKm, DistWMin, DistWMax),
		}
		if p.RawGpsKm > 0 && p.ExtKm > 0 {
			g.LogRatio = math.Log(p.ExtKm / p.RawGpsKm)
		}

		switch {
		case p.ExtFlagReason == "cross_account_duplicate":
			g.Reason = "flagged"
		case p.ExtSource != refSource:
			g.Reason = "other_source"
		case bestByGps[p.GpsRunID].id != p.ExtActivityID || bestByExt[p.ExtActivityID].id != p.GpsRunID:
			g.Reason = "ambiguous"
		case p.GpsDurS <= 0 || p.ExtDurS <= 0 || math.Abs(float64(p.ExtDurS)/float64(p.GpsDurS)-1) > DurTol:
			g.Reason = "partial"
		case p.RawGpsKm < MinKm || p.ExtKm < MinKm:
			g.Reason = "short"
		case math.Abs(sg) > edgeTol(p) || math.Abs(eg) > edgeTol(p):
			g.Reason = "edge"
		case p.RawGpsKm <= 0 || p.ExtKm/p.RawGpsKm < HardLo || p.ExtKm/p.RawGpsKm > HardHi:
			g.Reason = "range"
		default:
			g.Accepted = true
		}
		out = append(out, g)
	}
	return out
}

// EstimateWindow 對「已 accepted」的配對套 120 天/20 組視窗，回傳 Huber IRLS + 先驗收縮後的
// log 域估計。now 為估計基準時間（Recompute 呼叫當下）；視窗以 activity_at(=GpsStart) 篩選/排序。
//
// n_eff（判定是否啟用用）以「穩健中心（中位數）」為基準判 inlier，不是以 mu：mu 會被先驗往 0 拉，
// 若真實資料一致地偏離 1（例如全部 0.85），用 mu 判 inlier 會把這批一致的資料誤判成全體離群、
// n_eff 永遠不過門檻（永遠 warming）；中位數是資料本身的穩健中心，不受先驗影響。
func EstimateWindow(accepted []Gated, now time.Time) Estimate {
	cutoff := now.Add(-WindowDays * 24 * time.Hour)
	win := make([]Gated, 0, len(accepted))
	for _, g := range accepted {
		if !g.GpsStart.Before(cutoff) {
			win = append(win, g)
		}
	}
	sort.Slice(win, func(i, j int) bool { return win[i].GpsStart.Before(win[j].GpsStart) })
	if len(win) > WindowN {
		win = win[len(win)-WindowN:]
	}
	if len(win) == 0 {
		return Estimate{LogMu: PriorMu, Sigma: Sigma0, NEff: 0, EffWeight: 0, N: 0}
	}

	ys := make([]float64, len(win))
	ws := make([]float64, len(win))
	for i, g := range win {
		ys[i] = g.LogRatio
		ageDays := now.Sub(g.GpsStart).Hours() / 24
		ws[i] = g.DistW * math.Pow(0.5, ageDays/HalfLifeDays)
	}

	var sigma float64
	if len(ys) >= MADMinN {
		med := median(ys)
		devs := make([]float64, len(ys))
		for i, y := range ys {
			devs[i] = math.Abs(y - med)
		}
		sigma = math.Max(SigmaMin, 1.4826*median(devs))
	} else {
		sigma = Sigma0
	}

	med := median(ys)
	inl := make([]float64, len(ys))
	nEff := 0.0
	for i, y := range ys {
		d := math.Abs(y - med)
		if d <= HuberC*sigma {
			inl[i] = 1.0
		} else {
			inl[i] = HuberC * sigma / d
		}
		nEff += inl[i]
	}

	// Huber IRLS（起點=中位數，8 輪；凸問題，起點只影響收斂速度不影響收斂值）：
	// mu = (PriorW*PriorMu + Σ w_i h_i y_i) / (PriorW + Σ w_i h_i)，h_i 依目前 mu 的 Huber 權重。
	mu := med
	h := make([]float64, len(ys))
	for iter := 0; iter < IRLSIters; iter++ {
		for i, y := range ys {
			d := math.Abs(y - mu)
			if d <= HuberC*sigma {
				h[i] = 1.0
			} else if d > 0 {
				h[i] = HuberC * sigma / d
			} else {
				h[i] = 1.0
			}
		}
		num := PriorW*PriorMu + 0.0
		den := PriorW + 0.0
		for i, y := range ys {
			num += ws[i] * h[i] * y
			den += ws[i] * h[i]
		}
		mu = num / den
	}
	effWeight := 0.0
	for i := range ys {
		effWeight += ws[i] * h[i]
	}

	return Estimate{LogMu: mu, Sigma: sigma, NEff: nEff, EffWeight: effWeight, N: len(win), InlierW: inl, Window: win}
}

// Publish 依估計結果決定對外係數與狀態。
//
// 優先序：
//  1. 視窗為空（N==0）：若 lastPairAt 已知且距今超過 120 天 → stale；否則（含從未有過配對）→
//     warming。這一步必須在「NEff<4 → warming」之前判斷——視窗為空時 NEff 恆為 0，若先套用
//     NMinEff 規則會讓 stale 永遠不可達；stale 專指「曾經有效卻已過期」，warming 專指「還沒
//     累積到足夠資料」，兩者语意不同，用視窗是否曾經非空（lastPairAt 是否已知）來區分。
//  2. NEff < 4 → warming（資料量不足，含視窗非空但有效權重不足的情況）。
//  3. Sigma > 6% → unstable（資料互相矛盾，不可信，不啟用）。
//  4. 否則 active：lnK = ln(prevFactor) + clamp(mu−ln(prevFactor), ±2%)（遲滯步幅）；
//     k = clamp(exp(lnK), 0.92, 1.00)；|ln k| < 1% 死區內視為 1.0；四捨五入到小數 4 位。
//
// status="frozen" 不在本函式內產生——那是 service.go 層級的覆寫（frozen 時 Recompute 照常更新
// pairs/log_mu 但不呼叫本函式覆寫 factor/status，直接沿用後台釘住值）。
func Publish(e Estimate, prevFactor float64, lastPairAt *time.Time, now time.Time) Published {
	if e.N == 0 {
		if lastPairAt != nil && now.Sub(*lastPairAt) > StaleDays*24*time.Hour {
			return Published{Factor: 1.0, Status: "stale"}
		}
		return Published{Factor: 1.0, Status: "warming"}
	}
	if e.NEff < NMinEff {
		return Published{Factor: 1.0, Status: "warming"}
	}
	if e.Sigma > SigmaMaxActive {
		return Published{Factor: 1.0, Status: "unstable"}
	}
	if prevFactor <= 0 {
		prevFactor = 1.0
	}
	lnPrev := math.Log(prevFactor)
	delta := e.LogMu - lnPrev
	if delta > StepMaxLn {
		delta = StepMaxLn
	} else if delta < -StepMaxLn {
		delta = -StepMaxLn
	}
	k := math.Exp(lnPrev + delta)
	k = clamp(k, ClampLo, ClampHi)
	if math.Abs(math.Log(k)) < DeadBandLn {
		k = 1.0
	}
	k = math.Round(k*10000) / 10000
	return Published{Factor: k, Status: "active"}
}

// WindowFingerprint 回傳「這批估計視窗使用的配對集合」的決定性指紋（排序後 keys 的 sha256）。
// service.go 的 Recompute 用它判斷「這次重算的 accepted 視窗是否與上次 publish 時完全相同」——
// 完全相同代表沒有新證據，不該再讓 ±2% 步幅繼續往 mu 前進一步（對抗式審查修正，見 medium-2
// finding：修正前 Publish 的步幅限制是「每次 Recompute 呼叫」而非「每組新證據」，使用者連按
// 「重新計算」、或任何無關的 Strava/COROS webhook 觸發都會讓係數持續逼近同一個 mu，幾分鐘內就
// 能把遲滯設計繞過）。空視窗回傳空字串（Publish 對空視窗本來就走 warming/stale 分支，不涉及
// 步幅，呼叫端不會用到這個值來做「凍結係數」判斷）。
func WindowFingerprint(win []Gated) string {
	if len(win) == 0 {
		return ""
	}
	keys := make([]string, len(win))
	for i, g := range win {
		keys[i] = g.GpsRunID + "|" + g.ExtActivityID
	}
	sort.Strings(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, ",")))
	return hex.EncodeToString(sum[:])
}

// Compute 一次跑完 Gate → EstimateWindow → Publish，供 service.go 的 Recompute 呼叫。
func Compute(pairs []Pair, refSource string, prevFactor float64, lastPairAt *time.Time, now time.Time) Result {
	gated := Gate(pairs, refSource)
	accepted := make([]Gated, 0, len(gated))
	for _, g := range gated {
		if g.Accepted {
			accepted = append(accepted, g)
		}
	}
	est := EstimateWindow(accepted, now)
	pub := Publish(est, prevFactor, lastPairAt, now)
	return Result{Gated: gated, Est: est, Pub: pub}
}
