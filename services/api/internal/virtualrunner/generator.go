// 虛擬選手「數據生成引擎」（Phase 2）：對齊台灣整點，替 enabled 的虛擬選手自動產生「這個活躍時段
// 跑了沒／跑多遠/多快」的一筆活動，讓後台建立的機器人跑者在賽事排行榜/個人頁上看起來像真人在跑步。
//
// 排程：每小時檢查一次目前是否落在台灣整點 H ∈ {5,6,7,20,21,22,23}——這 7 個觸發時對應 model.go
// windowHourList 的 7 個活躍時段起始時（H-1 ∈ {4,5,6,19,20,21,22}）：例如台灣 06:00 觸發時，
// 處理「清晨 05:00 出門跑」這批 window_hour=5 的選手（在該時段結束後一小時內完成生成，模擬「起床
// 看數據」的合理延遲，也讓 Open-Meteo 查得到當下天氣）。
//
// 寫入正確性是本檔重點，三個關鍵決策（詳見對應函式註解）：
//  1. activities.exp_awarded 恆寫 TRUE——繞過 Redis Stream/worker 管線直接寫 DB，若不搶先標記，
//     services/worker/main.go reconcileMileageExp 會誤判漏發、對虛擬帳號補發 EXP/DP。
//  2. recorded_at 填「起跑時刻+duration_s」（結束時刻），語意比照 internal/activity/gps.go
//     SaveGPSRun 的 ended.Format(...)——賽事窗口查詢（LoadRaceActivities/aggregateStandings）
//     一律用 recorded_at BETWEEN start_date AND end_date 判斷落點，必須同語意才不會系統性偏移。
//  3. race_group_standings 是 services/worker/main.go aggregateStandings 維護的預聚合表，本引擎
//     繞過 worker 直接寫 activities 不會被該表自動感知——批次結束後對「本批實際有跑步的選手所報名
//     的競賽模式賽事」重跑一份同款聚合 SQL（recomputeStandingsSQL，見下方查證結論與同步維護提醒）。
package virtualrunner

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// --- 純函式核心（生成規格⑥：給定選手參數＋天氣＋隨機源 → 決定跑不跑＋距離＋配速＋起跑時刻）---

// RunnerParams 生成引擎輸入：某位虛擬選手在能力值面的靜態參數（來自 virtual_runners 表）。
type RunnerParams struct {
	AvgKm     float64
	MonthlyKm float64
	PaceFastS int
	PaceSlowS int
	Diligence int // 1-5
}

// GeneratedActivity 這個時段的生成結果。Ran=false 時其餘欄位皆為零值。
type GeneratedActivity struct {
	Ran        bool
	DistanceKm float64
	PaceS      int
	DurationS  int
	StartHour  int // = 呼叫端傳入的 windowHour，原樣帶出方便測試/呼叫端不用額外傳一次
	StartMin   int
	StartSec   int
	KmPaces    []int
}

// kmPaceJitterS 每公里分段配速的抖動幅度（±8 秒），見 buildKmPaces。
const kmPaceJitterS = 8

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func uniform(rng *rand.Rand, lo, hi float64) float64 {
	return lo + rng.Float64()*(hi-lo)
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// decideRun 純函式：依 p_run 機率＋天氣＋積極度決定「這個時段跑不跑」。
// p_run = clamp(monthly_km/avg_km/30, 0.15, 0.95)：月跑量/單次均距≈這個月預期跑幾次，除以 30 天
// 估成「今天有跑」的機率，夾在 [0.15,0.95] 避免極端值（月跑量趨近 0、或遠超單次均距×30）失真。
// 壞天氣：機率再乘上 (1-(5-diligence)*0.18)——積極度 5 完全不受影響（factor=1），積極度 1 打
// 對折以上（factor=0.28，幾乎必翹）。好天氣不受積極度影響（生成規格③定案：只在壞天氣才看積極度）。
func decideRun(p RunnerParams, bad bool, rng *rand.Rand) bool {
	pRun := clampFloat(p.MonthlyKm/p.AvgKm/30, 0.15, 0.95)
	if bad {
		factor := 1 - float64(5-p.Diligence)*0.18
		if factor < 0 {
			factor = 0
		}
		pRun *= factor
	}
	return rng.Float64() < pRun
}

// generateDistanceKm 純函式：這次跑量 = avg_km × U(0.90,1.10)，壞天氣再 × U(0.70,0.80)（雨天/
// 高溫縮短跑量），下限 1.0 公里、四捨五入 2 位。
func generateDistanceKm(avgKm float64, bad bool, rng *rand.Rand) float64 {
	d := avgKm * uniform(rng, 0.90, 1.10)
	if bad {
		d *= uniform(rng, 0.70, 0.80)
	}
	if d < 1.0 {
		d = 1.0
	}
	return round2(d)
}

// generatePaceS 純函式：這次配速 = 整數秒，均勻分布於 [fastS, slowS]。fastS>=slowS（不合法輸入，
// 正常情境下 jitterAbility 保證恆 fast<slow）時保守回傳 fastS，不 panic。
func generatePaceS(fastS, slowS int, rng *rand.Rand) int {
	if slowS <= fastS {
		return fastS
	}
	return fastS + rng.Intn(slowS-fastS+1)
}

// generateStartOffset 純函式：起跑時刻的分/秒部分（時已由呼叫端的 windowHour 決定），均勻分布
// 0-59。
func generateStartOffset(rng *rand.Rand) (minute, second int) {
	return rng.Intn(60), rng.Intn(60)
}

// buildKmPaces 純函式：每公里分段配速——整公里段落以「均值 paceS ±8 秒」均勻抖動；尾段（不足 1km
// 的殘餘距離）依比例灌入同樣抖動後的秒/公里換算（生成規格④）。加總後應約等於 distanceKm×paceS
// （即 duration_s），供套件測試驗證（TestBuildKmPaces_SumNearDuration）。
func buildKmPaces(distanceKm float64, paceS int, rng *rand.Rand) []int {
	full := int(math.Floor(distanceKm))
	remainder := distanceKm - float64(full)
	out := make([]int, 0, full+1)
	for i := 0; i < full; i++ {
		s := paceS + rng.Intn(2*kmPaceJitterS+1) - kmPaceJitterS
		if s < 1 {
			s = 1
		}
		out = append(out, s)
	}
	if remainder > 1e-9 {
		jitteredPace := paceS + rng.Intn(2*kmPaceJitterS+1) - kmPaceJitterS
		if jitteredPace < 1 {
			jitteredPace = 1
		}
		s := int(math.Round(remainder * float64(jitteredPace)))
		if s < 1 {
			s = 1
		}
		out = append(out, s)
	}
	return out
}

// GenerateActivity 純函式核心：給定選手參數＋天氣＋窗口起始時＋隨機源 → 決定這個時段跑不跑，
// 跑的話一併算出距離/配速/總秒數/起跑時刻/分段配速。所有隨機性皆由呼叫端注入的 rng 決定，供單元
// 測試以固定種子驗證分布邊界（見 generator_test.go）。
func GenerateActivity(p RunnerParams, w Weather, windowHour int, rng *rand.Rand) GeneratedActivity {
	bad := w.IsBad()
	if !decideRun(p, bad, rng) {
		return GeneratedActivity{Ran: false}
	}
	distanceKm := generateDistanceKm(p.AvgKm, bad, rng)
	paceS := generatePaceS(p.PaceFastS, p.PaceSlowS, rng)
	durationS := int(math.Round(distanceKm * float64(paceS)))
	minute, second := generateStartOffset(rng)
	return GeneratedActivity{
		Ran: true, DistanceKm: distanceKm, PaceS: paceS, DurationS: durationS,
		StartHour: windowHour, StartMin: minute, StartSec: second,
		KmPaces: buildKmPaces(distanceKm, paceS, rng),
	}
}

// --- 排程 ＋ DB 存取 ＋ 標準重算 ---

// generatorTriggerHours 對齊台灣整點觸發的小時集合（H），各自處理 window_hour=H-1 的選手
// （生成規格①）。與 model.go windowHourList 為同一份設計的兩種表示（H 集合 vs H-1 集合），見
// generator_test.go TestGeneratorTriggerHours_MapToValidWindowHours 交叉驗證不會漂移。
var generatorTriggerHours = map[int]bool{5: true, 6: true, 7: true, 20: true, 21: true, 22: true, 23: true}

// generatorAdvisoryLockName pg_try_advisory_lock 用的鎖名（獨立 key，經 hashtext 轉成 lock id）。
const generatorAdvisoryLockName = "virtual_runner_generate"

// taiwanNow 目前的台灣時間（UTC+8 固定 offset 手算，禁用 time.LoadLocation("Asia/Taipei")——
// distroless 執行環境無 tzdata，理由同 internal/ops/selfcheck.go taiwanNow()、
// internal/payment/vip_renewal.go formatTaipei() 等既有慣例）。回傳值底層仍標記 UTC，只是牆上
// 時刻已加 8 小時（「移位表示」），下方 windowStartUTC 換算回真正 UTC 時刻時要記得減回來。
func taiwanNow() time.Time {
	return time.Now().UTC().Add(8 * time.Hour)
}

// windowStartUTC 把「台灣時區某日期 + windowHour:minute:second」換算回真正的 UTC 時刻。twNow 為
// taiwanNow() 回傳的移位表示（用來決定年月日，避免例如 UTC 已跨日但台灣時間才剛好落在目標時的錯位）。
func windowStartUTC(twNow time.Time, windowHour, minute, second int) time.Time {
	twInstant := time.Date(twNow.Year(), twNow.Month(), twNow.Day(), windowHour, minute, second, 0, time.UTC)
	return twInstant.Add(-8 * time.Hour)
}

// genRunner EligibleForWindow 撈出的候選（只含生成邏輯需要的欄位）。
type genRunner struct {
	UserID    string
	City      string
	Diligence int
	AvgKm     float64
	MonthlyKm float64
	PaceFastS int
	PaceSlowS int
}

// EligibleForWindow 撈這次批次要處理的候選：window_hour 命中且 enabled，且尚未處理過這個時段——
// last_generated_at 為 NULL 或早於 periodStart（該時段起點，台灣 windowHour:00:00 換算的 UTC
// 時刻）才算候選，這是逐選手冪等防線（生成規格①）：同一時段重複被 tick 命中（例如批次跑到一半服務
// 重啟）不會對已處理過的選手重複生成。
func (r *Repository) EligibleForWindow(ctx context.Context, windowHour int, periodStart time.Time) ([]genRunner, error) {
	rows, err := r.db.Query(ctx, `
		SELECT user_id::text, COALESCE(city,''), diligence, avg_km, monthly_km, pace_fast_s, pace_slow_s
		FROM virtual_runners
		WHERE enabled AND window_hour = $1 AND (last_generated_at IS NULL OR last_generated_at < $2)`,
		windowHour, periodStart)
	if err != nil {
		return nil, fmt.Errorf("list eligible virtual runners: %w", err)
	}
	defer rows.Close()
	out := []genRunner{}
	for rows.Next() {
		var g genRunner
		if err := rows.Scan(&g.UserID, &g.City, &g.Diligence, &g.AvgKm, &g.MonthlyKm, &g.PaceFastS, &g.PaceSlowS); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ApplyGeneratedActivity 套用某位虛擬選手這次生成批次的結果，交易內完成、確保三個寫入
// （activities 插入／users.total_km 累加／virtual_runners.last_generated_at 更新）要嘛全成功要嘛
// 全不生效。act.Ran=false 時只更新 last_generated_at（仍要標記，見 EligibleForWindow 的冪等防線）。
//
// 三個欄位填法決策（查證結論，見套件檔頭）：
//   - exp_awarded 恆寫 TRUE：本引擎繞過既有 Redis Stream → worker 管線直接寫 activities，
//     若不搶先標記，services/worker/main.go reconcileMileageExp（「已寫入 2 分鐘仍未發獎」全表
//     掃描）會誤判為漏發，對虛擬帳號補發 EXP/DP/total_km（awardMileageDedup 甚至可能因總里程
//     跨過門檻誤觸發推薦連動）——鐵律，不可省略。
//   - processed 恆寫 TRUE：純粹比照全站所有既有寫入路徑的一致慣例（internal/activity/repository.go
//     Insert()／services/worker/main.go processOne()／internal/integration/repository.go
//     ImportActivity() 皆固定寫 TRUE）。該欄位目前查無任何背景管線讀取 processed=FALSE
//     （idx_activities_unprocessed 這個 partial index 建立已久卻查無讀者，形同死欄位/死索引）——
//     寫 TRUE 不影響任何現有行為，只是避免留下一筆「看起來像沒被處理過」的資料觀感混淆未來維護者。
//   - recorded_at 語意＝結束時刻（起跑時刻 + duration_s），比照 internal/activity/gps.go
//     SaveGPSRun 的 ended.Format(...)——賽事窗口查詢（LoadRaceActivities/aggregateStandings）皆用
//     recorded_at BETWEEN start_date AND end_date 判斷落點，必須同語意才不會有系統性偏移。
//   - source 恆留 NULL：語意等同「App GPS 來源」，這樣才會被既有查詢的 `a.source IS NULL` 分支
//     直接吃到（LoadRaceActivities/aggregateStandings/resolveCrossSourceDups 的 rank 0 皆是），
//     不需要額外放行 races.external_data。
func (r *Repository) ApplyGeneratedActivity(ctx context.Context, userID string, recordedAt time.Time, generatedAt time.Time, act GeneratedActivity) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if act.Ran {
		if _, err = tx.Exec(ctx, `
			INSERT INTO activities (user_id, distance_km, duration_s, avg_pace_s, recorded_at, km_paces, source, processed, exp_awarded)
			VALUES ($1,$2,$3,$4,$5,$6,NULL,TRUE,TRUE)`,
			userID, act.DistanceKm, act.DurationS, act.PaceS, recordedAt, act.KmPaces); err != nil {
			return fmt.Errorf("insert virtual runner activity: %w", err)
		}
		// exp：比照真人「每滿 1km +1 EXP」口徑就地累加（2026-08-28 使用者要求：虛擬選手也要有
		// 對應等級，否則累積數百公里卻 Lv.1 一看就是假人）。刻意「不走」真人發獎管線
		// （worker awardMileageDedup／integration mileage_exp）——exp_awarded=TRUE 鐵律不變，
		// 推薦獎勵/任務/體力/每日上限等真人機制對虛擬帳號一律不觸發；也刻意不發 DP
		// （DP 僅本人登入可見，虛擬帳號永不登入，發了只污染經濟統計）。
		if _, err = tx.Exec(ctx, `
			UPDATE users SET total_km = total_km + $1, exp = exp + $2, updated_at = NOW() WHERE id = $3`,
			act.DistanceKm, int(act.DistanceKm), userID); err != nil {
			return fmt.Errorf("update total_km/exp: %w", err)
		}
	}

	if _, err = tx.Exec(ctx, `
		UPDATE virtual_runners SET last_generated_at = $2, updated_at = NOW() WHERE user_id = $1`,
		userID, generatedAt); err != nil {
		return fmt.Errorf("update last_generated_at: %w", err)
	}

	return tx.Commit(ctx)
}

// recomputeStandingsSQL 查證結論：race_group_standings 是 services/worker/main.go
// aggregateStandings 維護的預聚合表，internal/race/repository.go GetStandings() 直接讀這張表
// 供競賽模式賽事（event_mode='competition'）的分組排名/成績顯示——不會自動感知本引擎繞過
// worker、直接寫入 activities 的資料。反之，個人進度/任務達成度（internal/race/progress.go
// LoadRaceActivities）與其餘賽事窗口查詢全部直接 JOIN activities 現算，不經這張表，不受影響。
// 因此每批次生成完成後，對「本批實際有跑步的虛擬選手所報名的賽事」重跑一次這段聚合 SQL。
//
// 這段 SQL 與 services/worker/main.go aggregateStandings 的查詢語句必須逐字同步——worker 是
// 獨立 go.mod module（github.com/dor/worker vs 本模組 github.com/dor/api），無法直接呼叫，只能
// 複製一份維護；修改任一邊的聚合邏輯時務必同步修改另一邊。
const recomputeStandingsSQL = `
	INSERT INTO race_group_standings
		(race_id, group_id, total_km, member_count, avg_km, avg_pace_s, finish_total_s, updated_at)
	SELECT
		rg.race_id,
		rg.id,
		COALESCE(SUM(a.distance_km), 0),
		COUNT(DISTINCT reg.user_id),
		CASE WHEN COUNT(DISTINCT reg.user_id) > 0
		     THEN COALESCE(SUM(a.distance_km), 0) / COUNT(DISTINCT reg.user_id) ELSE 0 END,
		CASE WHEN COALESCE(SUM(a.distance_km), 0) > 0
		     THEN (SUM(a.duration_s) / SUM(a.distance_km))::int ELSE 0 END,
		COALESCE(SUM(a.duration_s), 0),
		NOW()
	FROM race_groups rg
	JOIN races r ON r.id = rg.race_id AND r.event_mode = 'competition'
	             AND r.control_status NOT IN ('suspended','closed')
	             AND EXISTS (SELECT 1 FROM registrations bu WHERE bu.race_id = r.id AND bu.user_id = ANY($1::uuid[]))
	LEFT JOIN registrations reg ON reg.group_id = rg.id AND reg.status = 'paid'
	LEFT JOIN activities a ON a.user_id = reg.user_id AND NOT a.flagged
	                       AND a.recorded_at BETWEEN r.start_date AND r.end_date
	                       AND (a.source IS NULL OR (r.external_data AND a.source <> 'strava'))
	GROUP BY rg.race_id, rg.id
	ON CONFLICT (race_id, group_id) DO UPDATE SET
		total_km       = EXCLUDED.total_km,
		member_count   = EXCLUDED.member_count,
		avg_km         = EXCLUDED.avg_km,
		avg_pace_s     = EXCLUDED.avg_pace_s,
		finish_total_s = EXCLUDED.finish_total_s,
		updated_at     = NOW()
`

// recomputeStandingsForUsers 對「這批 userIDs 有報名」的競賽模式賽事重算 race_group_standings。
func (r *Repository) recomputeStandingsForUsers(ctx context.Context, userIDs []string) error {
	if len(userIDs) == 0 {
		return nil
	}
	_, err := r.db.Exec(ctx, recomputeStandingsSQL, userIDs)
	return err
}

// Generator 虛擬選手數據生成引擎：排程 + 天氣 + DB 寫入的組裝層。
type Generator struct {
	db      *pgxpool.Pool
	repo    *Repository
	weather *WeatherClient
}

// NewGenerator 建構子，供 cmd/api/main.go 啟動背景迴圈用。
func NewGenerator(db *pgxpool.Pool) *Generator {
	return &Generator{db: db, repo: NewRepository(db), weather: NewWeatherClient()}
}

// RunGenerateLoop 背景排程：先算距下一個整點的秒數用 time.NewTimer 對齊，之後改
// time.NewTicker(time.Hour) 每個整點各跑一次；ctx 取消即結束。刻意不比照
// internal/payment/vip_renewal.go RunRenewalLoop「啟動時先跑一次」的寫法——那裡的候選查詢本身有
// 「是否在到期視窗內」的天然過濾，啟動立即跑一次無害；本引擎的觸發時機定義是「台灣整點 H」，服務在
// 非整點時刻啟動（幾乎必然如此）若立即執行一次，等於用「當下的分鐘/秒」去比對一個本應對齊整點的
// 判斷，語意不乾淨，故改為精確對齊下一個整點才開始第一次執行。
func (g *Generator) RunGenerateLoop(ctx context.Context) {
	now := time.Now()
	next := now.Truncate(time.Hour).Add(time.Hour)
	timer := time.NewTimer(next.Sub(now))
	select {
	case <-ctx.Done():
		timer.Stop()
		return
	case <-timer.C:
	}
	g.runBatch(ctx)

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.runBatch(ctx)
		}
	}
}

// runBatch 單一整點觸發的一輪批次：先判斷是否命中觸發時（非命中直接 return，不佔用 advisory
// lock）→ 取批次互斥鎖（比照 internal/payment/vip_renewal.go runRenewalBatch：db.Acquire 拿專屬
// 連線、defer 內明確 unlock 之後才 Release，避免 session-level advisory lock 跟著連線被其他無關
// 查詢複用的已知陷阱）→ 撈候選 → 逐選手處理（panic 隔離，見 processRunnerSafe）→ 對本批實際有跑步
// 的選手重算受影響賽事的 race_group_standings。
func (g *Generator) runBatch(ctx context.Context) {
	now := taiwanNow()
	hour := now.Hour()
	if !generatorTriggerHours[hour] {
		return
	}
	windowHour := hour - 1
	periodStart := windowStartUTC(now, windowHour, 0, 0)

	conn, err := g.db.Acquire(ctx)
	if err != nil {
		log.Error().Err(err).Msg("virtual runner generator: acquire dedicated connection for advisory lock failed")
		return
	}
	defer conn.Release()

	var gotLock bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, generatorAdvisoryLockName).Scan(&gotLock); err != nil {
		log.Error().Err(err).Msg("virtual runner generator: try advisory lock failed")
		return
	}
	if !gotLock {
		log.Debug().Msg("virtual runner generator: another instance already running this hour's batch, skip")
		return
	}
	defer func() {
		var unlocked bool
		if err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, generatorAdvisoryLockName).Scan(&unlocked); err != nil {
			log.Warn().Err(err).Msg("virtual runner generator: advisory unlock failed (will auto-release once this connection closes)")
		}
	}()

	runners, err := g.repo.EligibleForWindow(ctx, windowHour, periodStart)
	if err != nil {
		log.Error().Err(err).Msg("virtual runner generator: load eligible runners failed")
		return
	}
	if len(runners) == 0 {
		return
	}

	cache := NewWeatherCache(g.weather)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	generatedAt := time.Now()

	var ranUserIDs []string
	generated, skipped, failed := 0, 0, 0
	for _, rn := range runners {
		ran, ok := g.processRunnerSafe(ctx, now, windowHour, cache, rng, generatedAt, rn)
		if !ok {
			failed++
			continue
		}
		if ran {
			generated++
			ranUserIDs = append(ranUserIDs, rn.UserID)
		} else {
			skipped++
		}
	}

	if len(ranUserIDs) > 0 {
		if err := g.repo.recomputeStandingsForUsers(ctx, ranUserIDs); err != nil {
			log.Error().Err(err).Msg("virtual runner generator: recompute standings failed")
		}
	}

	// 稱號同步：對「本輪實際有產生活動」的選手（ranUserIDs，act.Ran=true 才進這份清單）逐一評估是否
	// 解鎖新稱號/換展示稱號（見 titles.go SyncTitles）。單一選手失敗只記警告，不影響其餘選手或中斷
	// 批次——比照本函式其餘 best-effort 錯誤處理慣例（recomputeStandingsForUsers 亦僅 log 不 return）。
	for _, uid := range ranUserIDs {
		if _, err := SyncTitles(ctx, g.db, uid); err != nil {
			log.Warn().Err(err).Str("user_id", uid).Msg("virtual runner generator: sync titles failed")
		}
	}

	log.Info().Int("window_hour", windowHour).Int("candidates", len(runners)).
		Int("generated", generated).Int("skipped", skipped).Int("failed", failed).
		Msg("virtual runner generator: batch done")
}

// processRunnerSafe 處理單一選手，panic recover 隔離（比照 internal/payment/vip_renewal.go
// processRenewalCandidateSafe：單筆候選出狀況不應拖垮整批）。回傳 ok=false 代表這位選手處理失敗
// （error 或 panic），呼叫端只計入 failed 統計、不影響其餘選手；ran 僅在 ok=true 時有意義。
func (g *Generator) processRunnerSafe(ctx context.Context, now time.Time, windowHour int, cache *WeatherCache, rng *rand.Rand, generatedAt time.Time, rn genRunner) (ran bool, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Str("user_id", rn.UserID).
				Msg("virtual runner generator: panic recovered while processing runner")
			ok = false
		}
	}()

	weather := cache.Get(ctx, rn.City)
	params := RunnerParams{
		AvgKm: rn.AvgKm, MonthlyKm: rn.MonthlyKm,
		PaceFastS: rn.PaceFastS, PaceSlowS: rn.PaceSlowS, Diligence: rn.Diligence,
	}
	act := GenerateActivity(params, weather, windowHour, rng)

	var recordedAt time.Time
	if act.Ran {
		startAt := windowStartUTC(now, windowHour, act.StartMin, act.StartSec)
		recordedAt = startAt.Add(time.Duration(act.DurationS) * time.Second)
	}

	if err := g.repo.ApplyGeneratedActivity(ctx, rn.UserID, recordedAt, generatedAt, act); err != nil {
		log.Error().Err(err).Str("user_id", rn.UserID).Msg("virtual runner generator: apply generated activity failed")
		return false, false
	}
	return act.Ran, true
}
