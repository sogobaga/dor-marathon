package integration

// 外部活動合理性檢查（COROS GA 契約 §2.5；所有外部來源共用，掛在 Repository.ImportActivity 開頭，
// Strava／Terra／COROS／Garmin 自動受益，各 importer 不必自己再寫一份）。
//
// 規則（數值全部放常數，單元測試逐條覆蓋邊界）：
//   - 開始時間晚於 now＋10 分鐘            → skip（時鐘錯亂／偽造）
//   - 距離 < 0.1 km 或時間 < 60 s          → skip（GPS 抖動、誤觸記錄）
//   - 跑步類平均配速快於 2:30/km（走路類 4:00/km）→ flag implausible_pace（寫入但 flagged=TRUE，
//     不計賽事、不發獎勵、不加 total_km——AwardMileageExp 對非良性 flag_reason 一律不處理）
//   - 單筆距離 > 120 km                    → flag implausible_distance
//
// 「flag」的列仍寫進 activities（使用者在自己的活動清單看得到、後台可複查），但 flagged=TRUE 讓所有
// NOT flagged 的統計（賽事、稱號、EXP）都略過它。

import "time"

// CheckPlausible 的 action 值。
const (
	PlausibleOK   = "ok"
	PlausibleSkip = "skip"
	PlausibleFlag = "flag"
)

// 寫入 activities.flag_reason 的原因字串（skip 的原因只回傳給呼叫端／log，不寫入資料庫）。
const (
	FlagImplausiblePace     = "implausible_pace"
	FlagImplausibleDistance = "implausible_distance"

	SkipFutureStart  = "future_start"
	SkipTooShortDist = "too_short_distance"
	SkipTooShortTime = "too_short_duration"
)

// NormalizedActivity.Kind 的值：空字串視為 run（既有 importer 都沒設）。
const (
	KindRun  = "run"
	KindWalk = "walk"
)

// 門檻（契約 §2.5）。
const (
	// plausFutureSlack：容許的時鐘誤差；開始時間超過 now+這個值就視為未來活動。
	plausFutureSlack = 10 * time.Minute
	// plausMinDistanceKm／plausMinDurationS：低於任一者就略過。
	plausMinDistanceKm = 0.1
	plausMinDurationS  = 60
	// plausRunMinPaceS／plausWalkMinPaceS：平均配速（秒／公里）快於（小於）這個值＝不合理。
	// 跑步 2:30/km（世界紀錄級 ~2:30 是百公尺衝刺等級，持續 0.1 km 以上的平均配速不可能更快）；
	// 走路 4:00/km（競走世界紀錄約 3:50/km，一般走路／健行遠慢於此）。
	plausRunMinPaceS  = 150
	plausWalkMinPaceS = 240
	// plausMaxDistanceKm：單筆上限；超過一律 flag（超馬以外不會有；真有的由後台人工放行）。
	plausMaxDistanceKm = 120.0
)

// CheckPlausible 判斷一筆正規化後的外部活動能不能匯入。純函式（now 由呼叫端注入，方便測試）。
//
// 回傳 action∈{"ok","skip","flag"} 與原因：
//
//	skip → 呼叫端（ImportActivity）回 ImportResult{Status:"skipped", Reason:reason}，不寫 DB
//	flag → 照常寫入但 flagged=TRUE、flag_reason=reason（implausible_pace／implausible_distance）
//
// 檢查順序固定：未來開始時間 → 太短 → 配速 → 距離（先擋掉無意義的資料，再判斷「看起來像作弊」的）。
// 配速以 DurationS／DistanceKm 自行計算，不信任 provider 給的 AvgPaceS。
func CheckPlausible(a *NormalizedActivity, now time.Time) (action string, reason string) {
	if a == nil {
		return PlausibleSkip, SkipTooShortDist
	}
	if !a.RecordedAt.IsZero() && a.RecordedAt.After(now.Add(plausFutureSlack)) {
		return PlausibleSkip, SkipFutureStart
	}
	if a.DistanceKm < plausMinDistanceKm {
		return PlausibleSkip, SkipTooShortDist
	}
	if a.DurationS < plausMinDurationS {
		return PlausibleSkip, SkipTooShortTime
	}
	minPace := float64(plausRunMinPaceS)
	if a.Kind == KindWalk {
		minPace = float64(plausWalkMinPaceS)
	}
	if float64(a.DurationS)/a.DistanceKm < minPace {
		return PlausibleFlag, FlagImplausiblePace
	}
	if a.DistanceKm > plausMaxDistanceKm {
		return PlausibleFlag, FlagImplausibleDistance
	}
	return PlausibleOK, ""
}
