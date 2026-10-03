package integration

// 匯入「尾巴」（計畫 S6）：ImportActivity 成功之後，所有外部來源都要做的後續動作，抽成單一函式，
// Garmin 直連與 COROS MCP 共用（terra.go／strava.go／coros.go 的既有行內版本維持不動，行為對拍測試見
// importtail_test.go）。順序固定：
//
//  1. GPS 距離校正重算（gpscalib.RecomputeAsync，debounce）——除非 opt.SkipGPSCalib
//     （直連列已被 gpscalib 候選排除，Garmin 因此傳 SkipGPSCalib:true）。
//  2. 體力 SP 扣血（stamina.ChargeSP）——只有「新匯入」才扣（同一趟不能被扣兩次）；且
//     結束時間距今 ≤ opt.MaxSPAge（0＝不限）：補抓回來的舊活動不應再扣血（SP 恢復是從「現在」起算，
//     事後才扣等於重複懲罰）；被直連取代掉的 Strava 那趟（res.Superseded>0）SP 已在 Strava 匯入時扣過。
//  3. 里程 EXP／DP／total_km（AwardMileageExp，去重感知＋冪等）——新匯入，或同帳號跨裝置的良性重複
//     （multi_device_duplicate，走差額補償）。
//  4. 競賽分組成績重算（GA 契約 §3.5）：race_group_standings 是預聚合表，只有 worker（GPS 事件）與
//     虛擬選手生成器會寫，外部匯入不經 Redis stream——不重算的話全 COROS／Garmin 的分組成績會一直是 0。
//     以注入的 callback 執行（SetCompetitionRecompute，由 main.go 接 virtualrunner 的既有聚合 SQL），
//     避免 integration ↔ race／virtualrunner 的 import cycle；per-user 3 秒 debounce（一次同步匯入多筆只算一次）。

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/gpscalib"
	"github.com/dor/api/internal/stamina"
)

// TailOptions 控制 AfterImport 的差異行為。
type TailOptions struct {
	// SkipGPSCalib：不觸發 GPS 距離校正重算（直連列已被 gpscalib 候選排除，重算沒有意義）。
	SkipGPSCalib bool
	// MaxSPAge：只有「活動結束時間距今 ≤ MaxSPAge」才扣 SP；0＝不限（Strava／Terra 既有行為）。
	MaxSPAge time.Duration
}

// competitionRecomputeFn 重算「這位使用者有報名的競賽模式賽事」的 race_group_standings。
type competitionRecomputeFn func(ctx context.Context, userID string)

var competitionRecompute atomic.Pointer[competitionRecomputeFn]

// SetCompetitionRecompute 注入競賽分組成績重算 callback（main.go 啟動時呼叫一次；nil＝停用）。
func SetCompetitionRecompute(fn func(ctx context.Context, userID string)) {
	if fn == nil {
		competitionRecompute.Store(nil)
		return
	}
	f := competitionRecomputeFn(fn)
	competitionRecompute.Store(&f)
}

// standingsDebounce per-user 去抖時間（測試可縮短）。
var standingsDebounce = 3 * time.Second

var standingsTimers sync.Map // userID -> *time.Timer

// recomputeStandingsAsync per-user debounce：最後一次觸發後 standingsDebounce 才真正執行，逾時 15 秒，
// 錯誤只記 log（best-effort，worker 下次處理 GPS 事件時仍會整批重算，不是唯一防線）。
func recomputeStandingsAsync(userID string) {
	fn := competitionRecompute.Load()
	if fn == nil || userID == "" {
		return
	}
	if v, ok := standingsTimers.Load(userID); ok {
		if t, ok := v.(*time.Timer); ok {
			t.Stop()
		}
	}
	timer := time.AfterFunc(standingsDebounce, func() {
		standingsTimers.Delete(userID)
		defer func() {
			if rec := recover(); rec != nil {
				log.Error().Interface("panic", rec).Str("user", userID).Msg("integration: competition standings recompute panic recovered")
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		(*fn)(ctx, userID)
	})
	standingsTimers.Store(userID, timer)
}

// spFresh：這趟活動「結束時間」距 now 是否在 maxAge 以內（0＝不限）。外部來源的 recorded_at 是開始時間，
// 結束時間 = 開始 + 時長。
func spFresh(na *NormalizedActivity, maxAge time.Duration, now time.Time) bool {
	if maxAge <= 0 {
		return true
	}
	end := na.RecordedAt.Add(time.Duration(na.DurationS) * time.Second)
	return now.Sub(end) <= maxAge
}

// tailActions AfterImport 要做哪些動作（純決策，與 terra.go importTerra 的行內條件逐條對拍，見 importtail_test.go）。
type tailActions struct {
	GPSCalib  bool // gpscalib.RecomputeAsync
	ChargeSP  bool // stamina.ChargeSP
	Award     bool // AwardMileageExp
	Standings bool // 競賽分組成績重算
}

// planTail 依匯入結果與選項決定尾巴動作。now 供「SP 只扣新鮮活動」判斷（由呼叫端注入，方便測試）。
func planTail(na *NormalizedActivity, res ImportResult, opt TailOptions, now time.Time) tailActions {
	var a tailActions
	if na == nil {
		return a
	}
	// 1) GPS 距離校正 T1 觸發點：inserted／duplicate 皆重算（與 terra.go 同條件）。
	a.GPSCalib = !opt.SkipGPSCalib && (res.Status == "inserted" || res.Status == "duplicate")
	// 2) 體力 SP：僅新匯入才扣；被直連取代的 Strava 那趟已扣過；太舊的補抓活動不扣。
	a.ChargeSP = res.Status == "inserted" && na.DistanceKm > 0 && res.Superseded == 0 && spFresh(na, opt.MaxSPAge, now)
	// 3) 里程 EXP／DP／total_km：新匯入，或同帳號跨裝置的良性重複（差額補償）；其他 duplicate 原因由
	//    AwardMileageExp 內部的 flagged 政策擋。（res.ID 為空＝沒有可發放的列。）
	a.Award = (res.Status == "inserted" || (res.Status == "duplicate" && res.Reason == "multi_device_duplicate")) && na.DistanceKm > 0 && res.ID != ""
	// 4) 競賽分組成績：只有「新匯入且未標記」才可能改變成績。
	a.Standings = res.Status == "inserted"
	return a
}

// AfterImport 見檔頭說明。res 是 ImportActivity 的回傳；Status 為 exists／skipped 或 ID 為空時什麼都不做。
func (r *Repository) AfterImport(ctx context.Context, na *NormalizedActivity, res ImportResult, opt TailOptions) {
	plan := planTail(na, res, opt, time.Now())
	if plan.GPSCalib {
		gpscalib.RecomputeAsync(r.db, na.UserID)
	}
	if plan.ChargeSP {
		stamina.ChargeSP(ctx, r.db, na.UserID, na.DistanceKm, na.AvgPaceS)
	}
	if plan.Award {
		if err := r.AwardMileageExp(ctx, res.ID, na.UserID); err != nil {
			log.Error().Err(err).Str("activity", res.ID).Str("source", na.Source).Msg("integration: award mileage exp failed")
		}
	}
	if plan.Standings {
		recomputeStandingsAsync(na.UserID)
	}
}
