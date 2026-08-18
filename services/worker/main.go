package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	streamKey     = "activity_queue"
	consumerGroup = "activity_workers"
	batchSize     = 100
	batchInterval = 5 * time.Second

	// maxDrainRounds：processBatch 排空迴圈每次 tick 最多讀取的輪數上限（每輪最多 batchSize 筆）。
	// 防止 Redis Stream 持續湧入新訊息時無窮迴圈/饑餓其他 select 分支（尤其 ctx.Done()）；
	// 10 輪 * 100 筆 = 單次 tick 最多處理 1000 筆，遠高於舊版每 tick 固定 100 筆的人為吞吐上限。
	maxDrainRounds = 10

	// firstRoundBlockDur：readAndProcessRound 第一輪阻塞讀取的上限（對抗式審查修正：原本用
	// BLOCK 0 無限阻塞，閒置時 worker 會整個卡在 Redis 讀取裡，連 ticker 都不會被檢查，導致節流
	// 到期後累積的 pendingUserIDs 無上限延遲）。改成有界阻塞後，閒置期最多這麼久就會返回一次，
	// 讓 run() 的 ticker 迴圈能週期性醒來 flush pending。
	firstRoundBlockDur = 5 * time.Second

	// recomputeThrottle：recomputeStandings 牽涉全表級的跨來源去重 + 對帳補發 + 排行榜重算，
	// 尖峰時期（例如每 5 秒一批都非空）若每批都整套重跑，等於每 5 秒一次全表級重算，對 DB 負擔過大。
	// 距離上次執行不到此間隔就跳過本次重算；跳過期間累積的 userID 會併入 pendingUserIDs（見
	// Worker.maybeRecomputeStandings），下次真正執行時一併帶入，不會丟失。節流後排行榜/去重
	// 最多延遲 recomputeThrottle（60 秒），可接受。
	recomputeThrottle = 60 * time.Second
)

// ActivityEvent is the message pushed to Redis Streams when a user uploads a run.
type ActivityEvent struct {
	UserID     string  `json:"user_id"`
	RaceID     string  `json:"race_id"`
	MissionDay int     `json:"mission_day"`
	DistanceKm float64 `json:"distance_km"`
	DurationS  int     `json:"duration_s"`
	AvgPaceS   int     `json:"avg_pace_s"`
	RecordedAt string  `json:"recorded_at"`
	KmPaces    []int   `json:"km_paces,omitempty"` // 每公里分段配速(秒/km)
}

func main() {
	godotenv.Load()

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	if os.Getenv("ENV") == "development" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// DB：比照 services/api/internal/db/postgres.go 的寫法（ParseConfig + 顯式連線池設定）。
	dbCfg, err := pgxpool.ParseConfig(mustEnv("DATABASE_URL"))
	if err != nil {
		log.Fatal().Err(err).Msg("parse db config failed")
	}
	// MaxConns=3：worker 是單一 goroutine 依序處理批次的序列/批次工作負載，不像 api 端要應付大量
	// 併發 HTTP 請求，不需要大連線池；小池也降低對 Neon pooler 常駐連線數的壓力。
	dbCfg.MaxConns = 3
	// MinConns=0：閒置時不保留熱連線 → Neon compute 可完全休眠(scale-to-zero)，有請求再懶惰重連（比照 api 端）。
	dbCfg.MinConns = 0

	pool, err := pgxpool.NewWithConfig(ctx, dbCfg)
	if err != nil {
		log.Fatal().Err(err).Msg("db connect failed")
	}
	defer pool.Close()

	// Redis
	opt, _ := redis.ParseURL(mustEnv("REDIS_URL"))
	rdb := redis.NewClient(opt)
	defer rdb.Close()

	// 建立 Consumer Group（若不存在）
	rdb.XGroupCreateMkStream(ctx, streamKey, consumerGroup, "0").Err()

	hostname, _ := os.Hostname()
	consumerName := "worker-" + hostname

	log.Info().Str("consumer", consumerName).Msg("DOR Activity Worker started")

	w := &Worker{db: pool, rdb: rdb, consumerName: consumerName, pendingUserIDs: make(map[string]struct{})}
	w.recomputeStandings(ctx, nil) // 啟動時先算一次(補齊停機期間累積，不限縮 userID)；之後改「有新活動才重算」，閒置不打 DB → 讓 Neon 休眠
	w.run(ctx)
}

type Worker struct {
	db           *pgxpool.Pool
	rdb          *redis.Client
	consumerName string

	// lastRecompute / pendingUserIDs：recomputeStandings 節流狀態。只由 run() 所在的單一 goroutine
	// 循序讀寫（processBatch/recomputeStandings 皆同步呼叫，無並行存取），故不需要額外鎖。
	lastRecompute  time.Time           // 上次真正執行 recomputeStandings 的時間，zero value 代表尚未執行過
	pendingUserIDs map[string]struct{} // 節流跳過時累積的 userID，下次真正執行時要與當批 userID 一併帶入，不可丟失
}

func (w *Worker) run(ctx context.Context) {
	ticker := time.NewTicker(batchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("worker shutting down")
			return
		case <-ticker.C:
			processed, userIDs := w.processBatch(ctx)
			// 對抗式審查修正：節流跳過期間累積的 pendingUserIDs 必須「不依賴新活動」也能被 flush，
			// 否則若節流到期後恰好進入閒置空窗（夜間/離峰、完全沒有新活動），pendingUserIDs 會卡在
			// 記憶體裡無上限延遲，直到系統中任何人下一筆活動出現才重算——與「最多延遲 60 秒」的承諾矛盾。
			// 因此即使本次 tick 沒有新活動（processed==0），只要還有 pending 待處理，也要呼叫
			// maybeRecomputeStandings（userIDs 可為空 map，該函式本就是先併集合再判節流，空批安全）；
			// 搭配下面 readAndProcessRound 的有界阻塞（5s），worker 迴圈在閒置期仍會週期性醒來檢查。
			if processed > 0 || len(w.pendingUserIDs) > 0 {
				w.maybeRecomputeStandings(ctx, userIDs)
			}
		}
	}
}

// shouldRecompute 是 recomputeThrottle 節流判斷的純函式（抽出以利單元測試）：距離上次執行
// （last）到現在（now）是否已經過了至少 minInterval。last 為 zero value（尚未執行過，例如
// worker 剛啟動）一律視為「應該執行」。
func shouldRecompute(last, now time.Time, minInterval time.Duration) bool {
	if last.IsZero() {
		return true
	}
	return now.Sub(last) >= minInterval
}

// maybeRecomputeStandings 依 recomputeThrottle 節流規則決定是否執行 recomputeStandings：
//   - 一律先把本批 userIDs 併入 w.pendingUserIDs（無論本次是否真的執行）。
//   - 若距離上次執行不到 recomputeThrottle，本次跳過，pendingUserIDs 保留給下次使用，函式直接返回。
//   - 若可以執行，把累積的 pendingUserIDs（含本批 + 之前跳過時累積未消化的）整包傳入
//     recomputeStandings，執行後清空 pendingUserIDs、更新 lastRecompute。
func (w *Worker) maybeRecomputeStandings(ctx context.Context, userIDs map[string]struct{}) {
	for uid := range userIDs {
		w.pendingUserIDs[uid] = struct{}{}
	}

	now := time.Now()
	if !shouldRecompute(w.lastRecompute, now, recomputeThrottle) {
		return // 節流跳過；pendingUserIDs 已併入，留待下次真正執行時一起帶入，不會丟失
	}

	toProcess := w.pendingUserIDs
	w.pendingUserIDs = make(map[string]struct{})
	w.lastRecompute = now
	w.recomputeStandings(ctx, toProcess)
}

// recomputeStandings 跨來源去重 + 里程 EXP/DP 對帳補發 + 重算競賽分組成績。只在「剛處理完新活動」
// 或啟動時呼叫，閒置時完全不打 DB → 讓 Neon compute 休眠(scale-to-zero)。
//
// userIDs：本輪（含節流跳過期間累積）涉及的使用者集合，僅用來限縮 aggregateStandings 的重算範圍
// （見該函式註解）；resolveCrossSourceDups／reconcileMileageExp 仍是全表級操作，不受此限縮——
// 去重需要看到所有使用者間的重疊、對帳是依 exp_awarded=false 全表掃描，兩者皆非「本批 userID」
// 可局部判斷。userIDs 為 nil/空集合時（例如啟動時的全量重算）aggregateStandings 不加範圍限制。
func (w *Worker) recomputeStandings(ctx context.Context, userIDs map[string]struct{}) {
	w.resolveCrossSourceDups(ctx)          // 先跨來源去重，再算成績
	w.sweepOrphanedProviderActivities(ctx) // 自癒：inline Strava 刪除失敗留下的孤兒活動列（見該函式註解）
	w.reconcileMileageExp(ctx)             // 對帳補發：抓漏發的里程 EXP/DP（inline 發放失敗的補救網）
	ids := make([]string, 0, len(userIDs))
	for uid := range userIDs {
		ids = append(ids, uid)
	}
	w.aggregateStandings(ctx, ids)
}

// sweepOrphanedProviderActivities 自癒（對抗式審查 LOW-4）：Disconnect／撤權 webhook 觸發的
// DeleteProviderActivities（services/api/internal/integration/repository.go）是 inline 呼叫，失敗
// 時只 log、沒有重試機制——這裡週期性掃描「source='strava' 但該使用者已經沒有對應 user_integrations
// 連線列」的孤兒活動（代表連線已刪但活動刪除失敗，或連線刪除與活動刪除之間發生了其他狀況），
// 比照 DeleteProviderActivities 同樣的清除流程（先解除因這批孤兒活動而生的 stale
// cross_source_duplicate／multi_device_duplicate 標記，再刪除），讓失敗的 inline 刪除最終仍會被
// 清乾淨，滿足 Strava 30 天刪除義務。低頻即可：搭 recomputeStandings 節流（最多每 60 秒一次、且僅
// 在有新活動時才觸發，見 recomputeThrottle）順帶執行，不另開 ticker。
func (w *Worker) sweepOrphanedProviderActivities(ctx context.Context) {
	tx, err := w.db.Begin(ctx)
	if err != nil {
		log.Error().Err(err).Msg("sweepOrphanedProviderActivities: begin tx failed")
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE activities
		SET dup_of = NULL,
		    flagged = CASE WHEN flag_reason IN ('cross_source_duplicate','multi_device_duplicate') THEN FALSE ELSE flagged END,
		    flag_reason = CASE WHEN flag_reason IN ('cross_source_duplicate','multi_device_duplicate') THEN NULL ELSE flag_reason END
		WHERE dup_of IN (
			SELECT s.id FROM activities s
			WHERE s.source = 'strava'
			  AND NOT EXISTS (SELECT 1 FROM user_integrations ui WHERE ui.user_id = s.user_id AND ui.provider = 'strava')
		)`); err != nil {
		log.Error().Err(err).Msg("sweepOrphanedProviderActivities: clear stale dup_of failed")
		return
	}
	tag, err := tx.Exec(ctx, `
		DELETE FROM activities a
		WHERE a.source = 'strava'
		  AND NOT EXISTS (SELECT 1 FROM user_integrations ui WHERE ui.user_id = a.user_id AND ui.provider = 'strava')`)
	if err != nil {
		log.Error().Err(err).Msg("sweepOrphanedProviderActivities: delete failed")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		log.Error().Err(err).Msg("sweepOrphanedProviderActivities: commit failed")
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		log.Info().Int64("deleted", n).Msg("sweepOrphanedProviderActivities: healed orphaned strava activities")
	}
}

// reconcileMileageExp 對帳補發：inline 發放（本 worker processOne / api 端 Strava·Terra 匯入）
// 若因暫時性錯誤（DB 抖動、連線問題等）失敗，該筆活動會停在 exp_awarded=false 永久漏發
// （worker 目前讀 Redis Stream 只讀 ">"（新訊息），不會重讀 PEL 裡的 pending 訊息；api 端失敗則只有 log，
// 完全沒有重試）。這裡定期掃描「已寫入一段時間、卻仍未標記 exp_awarded」的活動，逐筆重新呼叫
// awardMileageDedup —— 該函式本身冪等 + 去重安全 + flagged 安全：真正已被別的來源計過的會走差額
// 補償正確處理，純粹漏發的則會補上，non-benign flagged（如 cross_account_duplicate 跨帳號作弊）
// 則永遠 no-op 不會被誤發，三來源（GPS/Strava/Terra）皆涵蓋。此掃描本身刻意不排除 flagged 活動
// （交給 awardMileageDedup 內部政策判斷），因為 benign-flagged 仍需要被掃到才能參與補償流程。
//
// 時間視窗：> 2 分鐘給 inline 發放先跑完（避免跟 inline 搶著發同一筆造成不必要的 lock 等待）；
// < 24 小時避免無限期重掃太舊的資料（真正的重複活動早已被跳過，不需要一直重查）。
func (w *Worker) reconcileMileageExp(ctx context.Context) {
	rows, err := w.db.Query(ctx, `
		SELECT id::text, user_id::text FROM activities
		WHERE exp_awarded = false
		  AND created_at < NOW() - INTERVAL '2 minutes'
		  AND created_at > NOW() - INTERVAL '24 hours'
		ORDER BY created_at
		LIMIT 500`)
	if err != nil {
		log.Error().Err(err).Msg("reconcileMileageExp: query pending failed")
		return
	}
	type pendingActivity struct{ id, userID string }
	var pending []pendingActivity
	for rows.Next() {
		var p pendingActivity
		if err := rows.Scan(&p.id, &p.userID); err != nil {
			rows.Close()
			log.Error().Err(err).Msg("reconcileMileageExp: scan failed")
			return
		}
		pending = append(pending, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("reconcileMileageExp: rows iteration failed")
		return
	}

	swept := 0
	for _, p := range pending {
		if err := w.awardMileageDedup(ctx, p.id, p.userID); err != nil {
			log.Error().Err(err).Str("activity", p.id).Msg("reconcileMileageExp: award failed")
			continue
		}
		swept++
	}
	if swept > 0 {
		log.Info().Int("count", swept).Msg("mileage exp reconciliation swept")
	}
}

// resolveCrossSourceDups 跨來源去重：同一趟跑步同時有 App GPS（source IS NULL）與 Strava（source='strava'）
// 兩筆、且時間重疊時（GPS 存結束時間、Strava 存開始時間，故用各自區間判重疊），保留一筆、另一筆標
// flagged=cross_source_duplicate、dup_of 指向保留的那筆 → 賽事排名/完賽 SUM(NOT flagged) 只算一筆。
// 只處理「雙方都尚未 flagged」的新配對。
//
// 優先序（2026-08-16 定案，正式紀錄一律 App GPS 優先）：App GPS 恆為最高優先（rank 0）；使用者偏好
// 的外部來源（user_profiles.preferred_data_source）僅在「沒有 App GPS 記錄、多個外部來源互相重疊」
// 時才用來取捨（次高，rank 1）；其餘外部來源依 garmin > coros > strava 排序。
//
// 自癒 stale 標記（2026-08-16 新增，對抗式審查發現的漏洞修補）：上面新排序只對「雙方都尚未 flagged
// 的新配對」生效——部署新排序前，若使用者偏好是外部來源（舊排序下該外部來源 rank 0、GPS rank 4），
// GPS 那筆會被標成 cross_source_duplicate。新政策下「GPS 筆被標 cross_source_duplicate」在定義上
// 必錯（GPS 恆 rank 0，不可能是輸家），但舊資料已寫入的 flagged 狀態不會自動消失，也沒有其他排程
// 會重解它（唯一會重置的 reResolveUser 只有使用者手動觸發）——若不處理，這批歷史 GPS 筆會永久卡在
// flagged，被所有 NOT flagged 統計（排行榜/成就/稱號/訓練/體力）排除，違反新政策且不會自癒。
// 修法：同一交易內，先解除所有「source IS NULL 且 flag_reason='cross_source_duplicate'」的 stale
// 標記，再執行既有的新排序 UPDATE（同交易內的下一步會依新排序重新配對——原本的外部孿生筆會被改標
// 成輸家）。首輪即治癒全部歷史資料；之後每輪這條 UPDATE 匹配 0 筆，近乎零成本。兩條 UPDATE 在同一
// 交易內執行，保證不存在「兩筆同時未標記、被統計雙算」的窗口。
func (w *Worker) resolveCrossSourceDups(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	tx, err := w.db.Begin(cctx)
	if err != nil {
		log.Error().Err(err).Msg("resolveCrossSourceDups: begin tx failed")
		return
	}
	defer tx.Rollback(cctx) // 已 Commit 後為 no-op

	// 自癒：GPS 恆 rank 0，「GPS 筆被標 cross_source_duplicate」在新排序下必為部署前舊排序留下的
	// stale 標記，先無條件解除，讓下一步的新排序 UPDATE 重新裁決這個時間叢集。
	healTag, err := tx.Exec(cctx, `
		UPDATE activities SET flagged = FALSE, flag_reason = NULL, dup_of = NULL
		WHERE source IS NULL AND flag_reason = 'cross_source_duplicate'`)
	if err != nil {
		log.Error().Err(err).Msg("resolveCrossSourceDups: heal stale gps flags failed")
		return
	}
	if n := healTag.RowsAffected(); n > 0 {
		log.Info().Int64("healed", n).Msg("stale cross_source_duplicate GPS flags healed")
	}

	// N 來源優先序去重：每筆算出「優先序 rank」（App GPS 恆 0；其餘外部來源中，使用者偏好者=1，
	// 再 garmin>coros>strava）；對每筆活動，若有時間重疊、且優先序更高（rank 更小）的另一筆存在 →
	// 標記為 cross_source_duplicate、dup_of 指向重疊中優先序最高那筆。每個時間叢集只保留優先序最高的一筆。
	// 起始時間統一：GPS(source NULL) 存結束時間 → 起=recorded_at-dur；其餘來源存起始時間 → 起=recorded_at。
	tag, err := tx.Exec(cctx, `
		WITH pref AS (SELECT user_id, COALESCE(preferred_data_source,'gps') AS src FROM user_profiles),
		ranked AS (
			SELECT a.id, a.user_id, a.duration_s AS dur,
				CASE WHEN a.source IS NULL THEN a.recorded_at - make_interval(secs=>a.duration_s) ELSE a.recorded_at END AS st,
				CASE
					WHEN a.source IS NULL THEN 0                      -- App GPS 一律最高：正式紀錄一律 GPS 優先（使用者 2026-08-16 定案）
					WHEN a.source = COALESCE(p.src,'') THEN 1          -- 使用者偏好的外部來源次之（僅在多個外部來源間取捨）
					WHEN a.source = 'garmin' THEN 2
					WHEN a.source = 'coros'  THEN 3
					WHEN a.source = 'strava' THEN 4
					ELSE 5 END AS rk
			FROM activities a
			LEFT JOIN pref p ON p.user_id = a.user_id
			WHERE a.duration_s > 0 AND NOT a.flagged
		)
		UPDATE activities a SET flagged = TRUE, flag_reason = 'cross_source_duplicate', dup_of = w.id
		FROM ranked lo
		CROSS JOIN LATERAL (
			SELECT hi.id FROM ranked hi
			WHERE hi.user_id = lo.user_id AND hi.id <> lo.id AND hi.rk < lo.rk
			  AND lo.st < hi.st + make_interval(secs=>hi.dur)
			  AND hi.st < lo.st + make_interval(secs=>lo.dur)
			ORDER BY hi.rk, hi.st DESC LIMIT 1
		) w
		WHERE a.id = lo.id AND NOT a.flagged`)
	if err != nil {
		log.Error().Err(err).Msg("resolveCrossSourceDups failed")
		return
	}
	if err := tx.Commit(cctx); err != nil {
		log.Error().Err(err).Msg("resolveCrossSourceDups: commit failed")
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		log.Info().Int64("flagged", n).Msg("cross-source duplicates resolved")
	}
}

// aggregateStandings 以單一查詢重算競賽模式賽事的 race_group_standings（預聚合，前台直接讀）。
// 各分組：總累積里程、成員數、平均里程、平均配速（總時間/總里程）、完成累計總時間（成員總移動時間）。
//
// userIDs：非空時，只重算「本批使用者有報名」的賽事（EXISTS ... registrations bu WHERE bu.race_id=r.id
// AND bu.user_id = ANY(userIDs)）——用 registrations（報名關係表）反查，而非 activities.race_id，因為
// 一筆活動會計入該使用者「所有報名中的賽事」，不是活動本身綁定單一賽事；用報名表才能抓全本批使用者
// 涉及的所有賽事。userIDs 為空（例如啟動時的全量重算）則不加此限制，比照原本全表級行為。
func (w *Worker) aggregateStandings(ctx context.Context, userIDs []string) {
	query := `
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
		             AND r.control_status NOT IN ('suspended','closed')`

	var args []interface{}
	if len(userIDs) > 0 {
		// 範圍限縮：只重算本批使用者有報名的賽事，避免每批都全表級重算所有進行中賽事。
		query += `
		AND EXISTS (SELECT 1 FROM registrations bu WHERE bu.race_id = r.id AND bu.user_id = ANY($1::uuid[]))`
		args = append(args, userIDs)
	}

	query += `
		LEFT JOIN registrations reg ON reg.group_id = rg.id AND reg.status = 'paid'
		LEFT JOIN activities a ON a.user_id = reg.user_id AND NOT a.flagged
		                       AND a.recorded_at BETWEEN r.start_date AND r.end_date
		                       AND (r.external_data OR a.source IS NULL)
		GROUP BY rg.race_id, rg.id
		ON CONFLICT (race_id, group_id) DO UPDATE SET
			total_km       = EXCLUDED.total_km,
			member_count   = EXCLUDED.member_count,
			avg_km         = EXCLUDED.avg_km,
			avg_pace_s     = EXCLUDED.avg_pace_s,
			finish_total_s = EXCLUDED.finish_total_s,
			updated_at     = NOW()
	`

	tag, err := w.db.Exec(ctx, query, args...)
	if err != nil {
		log.Error().Err(err).Msg("aggregate standings failed")
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		log.Debug().Int64("groups", n).Msg("standings aggregated")
	}
}

// processBatch 排空 Redis Stream：迴圈呼叫 XReadGroup 讀取並處理訊息，直到某一輪讀不到新訊息、
// 或最多讀滿 maxDrainRounds 輪為止（防止 stream 持續湧入新訊息時無窮迴圈/饑餓 ctx.Done()）。
// 第一輪為有界阻塞讀取（block=true，見 readAndProcessRound：最多阻塞 5 秒，不是無限阻塞）——
// 閒置時仍會在 5 秒內醒來一次，讓 run() 的 ticker 迴圈有機會檢查/flush pendingUserIDs
// （對抗式審查修正：若這裡無限阻塞，閒置空窗期節流到期後的 pending 會無上限延遲，見 run() 註解）；
// 讀到訊息後改用非阻塞讀取（block=false）持續嘗試撈下一批，藉此排空真正的 backlog，
// 不再被「一次最多 batchSize 筆、下一批要等下個 ticker」的人為節奏卡住吞吐量。
//
// 回傳：整個排空流程處理成功的總筆數，以及排空期間涉及的 userID 集合（去重，取自本批所有訊息的
// user_id）——recomputeStandings（受節流限制，見 maybeRecomputeStandings）應在排空流程「整個跑完
// 之後」才呼叫一次，不要每一輪 XReadGroup 各自呼叫一次。
func (w *Worker) processBatch(ctx context.Context) (int, map[string]struct{}) {
	totalProcessed := 0
	userIDs := make(map[string]struct{})

	block := true
	for round := 0; round < maxDrainRounds; round++ {
		n, roundUserIDs, hasMore := w.readAndProcessRound(ctx, block)
		totalProcessed += n
		for uid := range roundUserIDs {
			userIDs[uid] = struct{}{}
		}
		if !hasMore {
			break // 本輪讀不到新訊息，stream 已排空，提前結束
		}
		block = false // 後續輪次改非阻塞：快速判斷「還有沒有 backlog」，不無謂等待
	}
	return totalProcessed, userIDs
}

// readAndProcessRound 讀取並處理單一輪（最多 batchSize 筆）活動訊息。block 控制本輪 XReadGroup
// 是否阻塞等待（true=有界阻塞最多 firstRoundBlockDur(5秒)，逾時未讀到訊息就返回空結果；
// false=非阻塞，立即回傳現有訊息，沒有就回傳空結果）。回傳成功處理筆數、本輪涉及的 userID 集合、
// 以及本輪是否讀到訊息（hasMore=false 時，processBatch 的排空迴圈應停止）。
//
// ⚠️ block=true 刻意用「有界」阻塞（非 BLOCK 0 無限阻塞）：對抗式審查發現，若第一輪無限阻塞，
// worker 在完全閒置（無新活動）時會整個卡在這次 Redis 讀取裡，連 run() 的 ticker 都不會被檢查，
// 導致節流到期後累積的 pendingUserIDs 無上限延遲、直到系統中任何人下一筆活動出現才會被 flush——
// 與「最多延遲 recomputeThrottle(60秒)」的承諾矛盾。改成有界阻塞後，閒置期最多 5 秒就會返回一次
// （回傳 hasMore=false，processBatch 提前結束、回到 run() 的下一輪 ticker 檢查），恢復週期性喚醒。
func (w *Worker) readAndProcessRound(ctx context.Context, block bool) (int, map[string]struct{}, bool) {
	blockDur := time.Duration(-1) // 負值 → 不傳送 BLOCK 參數，非阻塞立即回傳
	if block {
		blockDur = firstRoundBlockDur // 有界阻塞：閒置時最多等這麼久就返回，讓迴圈能週期性醒來
	}

	streams, err := w.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    consumerGroup,
		Consumer: w.consumerName,
		Streams:  []string{streamKey, ">"},
		Count:    batchSize,
		Block:    blockDur,
	}).Result()
	if err != nil || len(streams) == 0 {
		return 0, nil, false
	}

	msgs := streams[0].Messages
	if len(msgs) == 0 {
		return 0, nil, false
	}

	log.Debug().Int("count", len(msgs)).Msg("processing activity batch")

	userIDs := make(map[string]struct{})
	var ids []string
	for _, msg := range msgs {
		ids = append(ids, msg.ID)
		uid, err := w.processOne(ctx, msg)
		if err != nil {
			log.Error().Err(err).Str("msg_id", msg.ID).Msg("failed to process activity")
			// 保留在 pending list，稍後重試
			ids = ids[:len(ids)-1]
			continue
		}
		if uid != "" {
			userIDs[uid] = struct{}{}
		}
	}

	// ACK 成功處理的訊息
	if len(ids) > 0 {
		w.rdb.XAck(ctx, streamKey, consumerGroup, ids...)
	}
	return len(ids), userIDs, true
}

// processOne 處理單筆活動訊息，回傳該訊息的 user_id（供 processBatch 收集本批涉及的使用者集合）
// 與錯誤。訊息格式錯誤或處理失敗時 userID 回傳空字串。
func (w *Worker) processOne(ctx context.Context, msg redis.XMessage) (string, error) {
	raw, ok := msg.Values["data"].(string)
	if !ok {
		return "", fmt.Errorf("missing data field")
	}

	var evt ActivityEvent
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		return "", fmt.Errorf("unmarshal event: %w", err)
	}

	// 寫入 PostgreSQL（RETURNING 偵測是否真的新插入，避免重複事件灌爆里程）
	var newID string
	err := w.db.QueryRow(ctx, `
		INSERT INTO activities (user_id, race_id, mission_day, distance_km, duration_s, avg_pace_s, recorded_at, km_paces, processed)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, TRUE)
		ON CONFLICT DO NOTHING
		RETURNING id
	`,
		evt.UserID,
		nullableString(evt.RaceID),
		nullableInt(evt.MissionDay),
		evt.DistanceKm,
		evt.DurationS,
		evt.AvgPaceS,
		evt.RecordedAt,
		evt.KmPaces,
	).Scan(&newID)
	if err == pgx.ErrNoRows {
		return evt.UserID, nil // 重複活動，略過（不再累加里程），但 userID 仍有效供標準重算範圍使用
	}
	if err != nil {
		return "", fmt.Errorf("insert activity: %w", err)
	}

	// 去重感知、冪等地發放里程 EXP/DP/total_km（取代舊的「無條件 UPDATE total_km」+ awardMileageExp）：
	// 若同一使用者存在時間重疊、已由其他來源（Strava/Terra）發放過的活動，這裡就不重發，
	// 避免同一趟跑步被 GPS 與第三方來源各發一次。
	if err := w.awardMileageDedup(ctx, newID, evt.UserID); err != nil {
		return "", fmt.Errorf("award mileage dedup: %w", err)
	}

	return evt.UserID, nil
}

// benignFlagReasons：flagged=true 但屬於「同帳號跨裝置/跨來源/同源重複」的良性標記——不是作弊，
// 只是同一趟跑步被記了兩筆。這類活動仍可參與差額補償流程（見 awardMileageDedup）。
// 不在此集合中的 flag_reason（目前只有 cross_account_duplicate＝跨帳號洗數據作弊）一律不發放、
// 不補償、也不標記 exp_awarded——見 awardMileageDedup 開頭的 flagged 政策。
//
// ⚠️ 與 services/api/internal/integration/mileage_exp.go 的同名變數是同一語意的獨立實作，理由同下方
// awardMileageDedup 註解；修改時請同步修改另一份。
var benignFlagReasons = map[string]bool{
	"multi_device_duplicate": true, // 同帳號多裝置重複上傳同一趟
	"cross_source_duplicate": true, // resolveCrossSourceDups：同帳號 GPS/Strava/Terra 互相重複
	"duplicate":              true, // 同帳號、精確指紋相同的重複匯入
}

// computeRewardKm 算出單趟活動可折算的獎勵公里數：floor(distance) → 套單趟上限 capKm → 套配速防造假
// minPaceS。distanceKm<1 或 durationS<=0 或 perKm 與 dpPerKm 皆為 0 時直接回 0（不具備發放資格）。
// 供 awardMileageDedup 全額發放與差額補償共用（差額補償需要對本筆與每筆重疊已發放筆各自計算一次）。
//
// ⚠️ 與 services/api/internal/integration/mileage_exp.go 的同名函式是同一語意的獨立實作，理由同下方
// awardMileageDedup 註解；修改演算法時請同步修改另一份。
func computeRewardKm(distanceKm float64, durationS, perKm, dpPerKm, capKm, minPaceS int) int {
	rewardKm := 0
	if distanceKm >= 1 && durationS > 0 && (perKm > 0 || dpPerKm > 0) {
		rewardKm = int(distanceKm) // floor(單趟距離)
		if capKm > 0 && rewardKm > capKm {
			rewardKm = capKm // 單趟上限
		}
		if minPaceS > 0 {
			if maxByTime := durationS / minPaceS; rewardKm > maxByTime { // 配速防造假
				rewardKm = maxByTime
			}
		}
		if rewardKm < 0 {
			rewardKm = 0
		}
	}
	return rewardKm
}

// clampDeltaInt 回傳 max(a-b, 0)：差額補償「只補不扣」的整數版 clamp。
func clampDeltaInt(a, b int) int {
	if a > b {
		return a - b
	}
	return 0
}

// clampDeltaFloat 回傳 max(a-b, 0)：差額補償「只補不扣」的浮點版 clamp（total_km 用）。
func clampDeltaFloat(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return 0
}

// externalAwardHash 算出 (source, external_id) 的一次性、不可還原雜湊，供 external_award_ledger
// （migrations/131_external_award_ledger.sql）記錄「這筆外部活動的獎勵是否已處理過」。只存雜湊、
// 不存可識別的 Strava/Terra 活動內容本身。純 sha256、無需密鑰。
//
// ⚠️ 與 services/api/internal/integration/mileage_exp.go 的同名函式是同一語意的獨立實作（worker
// 為獨立 Go module，理由同下方 awardMileageDedup 註解）；修改時請同步修改另一份。
func externalAwardHash(source, externalID string) string {
	sum := sha256.Sum256([]byte(source + ":" + externalID))
	return hex.EncodeToString(sum[:])
}

// awardMileageDedup 去重感知、冪等地發放里程 EXP/DP/total_km。GPS 活動寫入 activities 後呼叫，
// activityID 為剛插入的 activities.id。
//
// ⚠️ 與 services/api/internal/integration/mileage_exp.go 的 Repository.AwardMileageExp 是同一語意
// 的獨立實作——worker 是獨立的 Go module、不能 import api 的 internal package，故兩邊「各自維護一份」。
// 修改本函式的判斷邏輯（去重規則／exp_rules 算法／交易語意）時，請同步修改另一份，保持完全一致的行為。
//
// 語意：
//  1. per-user 序列化：SELECT pg_advisory_xact_lock(hashtext(userID))（交易內），避免本 worker
//     與 Strava/Terra（api）同時對同一使用者發放而雙發。
//  2. 冪等：FOR UPDATE 鎖定該筆 activity 並讀出 exp_awarded；已為 true 代表發過了，直接 return。
//  3. flagged 政策（2026-08-16 新增，修補既有安全漏洞）：flagged=true 且 flag_reason 不在
//     benignFlagReasons（目前僅 cross_account_duplicate＝跨帳號洗數據作弊）→ 直接 return，
//     完全不發放、不補償、也不標記 exp_awarded。⚠️ 這同時是安全漏洞修補點：舊版
//     reconcileMileageExp 的 sweep 掃描與本函式都不檢查 flagged，導致 cross_account_duplicate
//     活動會在 2 分鐘後被 sweep 誤發 EXP/DP；此後 sweep 對這類活動永遠是 no-op。
//     flag_reason 屬於 benignFlagReasons（同帳號跨裝置/跨來源/同源重複，非作弊）者則與
//     unflagged 活動走同一套統一流程（見 4-5）。
//  4. 去重比對：查該使用者「時間重疊且已發放(exp_awarded=true)」的其他活動（比照
//     resolveCrossSourceDups 的重疊判定——⚠️ GPS(source IS NULL) 的 recorded_at 存的是「結束時間」，
//     Strava/Terra(source 非 NULL) 存的是「開始時間」，兩者語意不同，比對前都要正規化成 [start,end)
//     區間再判重疊，否則會誤判/漏判），撈出全部重疊已發放筆的 (distance_km, duration_s)（實務 0~2 筆）。
//     5a. 無重疊已發放筆 → 全額發放（floor(distance) → 套單趟上限 → 套配速防造假 →
//     UPDATE users.total_km/exp/dp → awardReferralReward → INSERT mileage_exp_events →
//     UPDATE activities.exp_awarded=true）。unflagged 與 benign-flagged 皆可能走此路——
//     benign-flagged 走此路時即成為「這趟以此筆入帳」，之後原本重疊的那筆進來會因重疊而走
//     5b 差額比較，「以配對中最大值計」的語意自動成立。
//     5b. 有重疊已發放筆 → 差額補償（使用者 2026-08-16 拍板：以配對中最大值計、只補不扣）：
//     deltaReward = max(0, 本筆 computeRewardKm − 各重疊已發放筆 computeRewardKm 的最大值)；
//     deltaKm = max(0, 本筆 distance − 各重疊已發放筆 distance 的最大值)。deltaReward>0 或
//     deltaKm>0 時才 UPDATE users（加 deltaKm/deltaReward*perKm/deltaReward*dpPerKm）→
//     awardReferralReward → （deltaReward>0 時）INSERT mileage_exp_events。
//     無論 delta 是否為 0，一律 UPDATE activities.exp_awarded=TRUE：語意為「此筆價值已入帳
//     完畢（直接發放或已與重疊筆比較補償過）」——讓 reconcile sweep 不再重複掃它，也讓它可作為
//     之後其他重疊筆比較時的 MAX 基準。
//     （舊版註解：「刻意保留 exp_awarded=false」僅適用於「整筆不發」的舊語意，本次已改為差額
//     補償，此筆一律標記為已處理，見上。）
//     全部在同一交易內；exp_rules 讀取在統一流程之前（全額發放與差額補償都需要 perKm/dpPerKm/
//     capKm/minPaceS）。
func (w *Worker) awardMileageDedup(ctx context.Context, activityID, userID string) error {
	tx, err := w.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // 已 Commit 後為 no-op

	// ① per-user 序列化
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, userID); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}

	// ② 冪等：鎖定本筆活動；一併撈 source/external_id（正規化 [start,end) 用、外部帳本用）與
	// flagged/flag_reason（政策判斷用）
	var awarded, flagged bool
	var distanceKm float64
	var durationS int
	var recordedAt time.Time
	var source *string
	var flagReason *string
	var externalID string
	if err := tx.QueryRow(ctx, `
		SELECT exp_awarded, distance_km, duration_s, recorded_at, source, flagged, flag_reason, COALESCE(external_id,'')
		FROM activities WHERE id=$1 AND user_id=$2 FOR UPDATE`, activityID, userID).
		Scan(&awarded, &distanceKm, &durationS, &recordedAt, &source, &flagged, &flagReason, &externalID); err != nil {
		return fmt.Errorf("load activity: %w", err)
	}
	if awarded {
		return nil // 已發過，不重發
	}

	// ③ flagged 政策：非良性標記（如 cross_account_duplicate＝跨帳號作弊）一律不處理，見上方函式註解。
	if flagged && !(flagReason != nil && benignFlagReasons[*flagReason]) {
		return tx.Commit(ctx) // 尚無任何寫入，commit 等同 no-op，僅釋放鎖
	}

	// ③.5 durable 帳本去重（對抗式審查 CRITICAL-2 修補）：與 services/api/internal/integration/
	// mileage_exp.go 的 Repository.AwardMileageExp 同一語意（見該函式 ③.5 註解）。GPS 活動理論上
	// 不會有 source/external_id（不會被撤權刪除重匯），這裡只是與 api 端保持完全一致的防線，
	// 供未來若有其他外部來源改走此函式時也能受益。
	var isExternal bool
	var extHash string
	if source != nil && externalID != "" {
		isExternal = true
		extHash = externalAwardHash(*source, externalID)
		var alreadyAwarded bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM external_award_ledger WHERE user_id=$1 AND source=$2 AND ext_hash=$3)`,
			userID, *source, extHash).Scan(&alreadyAwarded); err != nil {
			return fmt.Errorf("ledger check: %w", err)
		}
		if alreadyAwarded {
			if _, err := tx.Exec(ctx, `UPDATE activities SET exp_awarded = TRUE WHERE id=$1`, activityID); err != nil {
				return fmt.Errorf("mark awarded (ledger dedup): %w", err)
			}
			return tx.Commit(ctx)
		}
	}

	// 正規化本筆時間為 [thisStart, thisEnd)：GPS(source IS NULL) 的 recorded_at 是結束時間，
	// 其餘來源(strava/garmin/coros)的 recorded_at 是開始時間（比照 resolveCrossSourceDups）。
	thisStart := recordedAt
	if source == nil {
		thisStart = recordedAt.Add(-time.Duration(durationS) * time.Second)
	}
	thisEnd := thisStart.Add(time.Duration(durationS) * time.Second)

	// ④ 讀 exp_rules：全額發放與差額補償都需要
	var perKm, dpPerKm, capKm, minPaceS int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(per_km,0), COALESCE(dp_per_km,0),
		       COALESCE(mileage_cap_km,21), COALESCE(mileage_min_pace_s,120)
		FROM exp_rules WHERE id=TRUE`).Scan(&perKm, &dpPerKm, &capKm, &minPaceS); err != nil {
		return fmt.Errorf("load exp_rules: %w", err)
	}

	// ⑤ 撈出「時間重疊且已發放」的其他活動之 (distance_km, duration_s)（實務 0~2 筆）。候選列同樣
	// 依 source 正規化成 [candStart, candEnd) 再判重疊（candStart < thisEnd AND candEnd > thisStart）。
	rows, err := tx.Query(ctx, `
		SELECT a.distance_km, a.duration_s FROM activities a
		WHERE a.user_id=$1 AND a.exp_awarded=true AND a.id<>$2
		  AND (CASE WHEN a.source IS NULL THEN a.recorded_at - make_interval(secs=>a.duration_s) ELSE a.recorded_at END) < $3
		  AND (CASE WHEN a.source IS NULL THEN a.recorded_at - make_interval(secs=>a.duration_s) ELSE a.recorded_at END)
		      + make_interval(secs=>a.duration_s) > $4`,
		userID, activityID, thisEnd, thisStart)
	if err != nil {
		return fmt.Errorf("overlap query: %w", err)
	}
	var overlapKm []float64
	var overlapDur []int
	for rows.Next() {
		var km float64
		var dur int
		if err := rows.Scan(&km, &dur); err != nil {
			rows.Close()
			return fmt.Errorf("scan overlap: %w", err)
		}
		overlapKm = append(overlapKm, km)
		overlapDur = append(overlapDur, dur)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("overlap rows: %w", err)
	}

	thisReward := computeRewardKm(distanceKm, durationS, perKm, dpPerKm, capKm, minPaceS)

	if len(overlapKm) == 0 {
		// 無重疊已發放筆 → 全額發放（見函式註解 5a）
		expAmt := thisReward * perKm
		dpAmt := thisReward * dpPerKm

		// total_km 一趟只加一次（走到這裡即代表本趟首次被計入，不論最終 thisReward 是否 > 0）
		// RETURNING 新 total_km，供下面推薦達標判斷（awardReferralReward 只在 >=10km 時才可能發獎）用。
		var newTotalKm float64
		if err := tx.QueryRow(ctx,
			`UPDATE users SET total_km = total_km + $1, exp = exp + $2, dp = dp + $3, updated_at = NOW() WHERE id=$4 RETURNING total_km`,
			distanceKm, expAmt, dpAmt, userID).Scan(&newTotalKm); err != nil {
			return fmt.Errorf("update user: %w", err)
		}
		// 推薦/推廣連結系統：同一交易內判斷這位使用者是否因此跨過 10km 門檻，若是且他是被推薦來的
		// 新朋友、且尚未發過獎 → 對推薦人/被推薦人雙向 +VIP 天數。
		// ⚠️ 此段為 internal/referral.Reward + internal/vip.Extend 的 worker 複製版（worker 是獨立 Go
		// module、不能 import services/api 的 internal package，故整段內聯複製一份）——改動需與
		// services/api/internal/{vip,referral} 同步，見下方 awardReferralReward。
		if err := awardReferralReward(ctx, tx, userID, newTotalKm); err != nil {
			return fmt.Errorf("referral reward: %w", err)
		}
		if thisReward > 0 {
			if _, err := tx.Exec(ctx,
				`INSERT INTO mileage_exp_events (user_id, exp_amount, dp_amount, km_added, distance_km, recorded_at)
				 VALUES ($1,$2,$3,$4,$5,$6)`, userID, expAmt, dpAmt, thisReward, distanceKm, recordedAt); err != nil {
				return fmt.Errorf("insert event: %w", err)
			}
		}
		if isExternal {
			if _, err := tx.Exec(ctx,
				`INSERT INTO external_award_ledger (user_id, source, ext_hash) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
				userID, *source, extHash); err != nil {
				return fmt.Errorf("ledger insert: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE activities SET exp_awarded = TRUE WHERE id=$1`, activityID); err != nil {
			return fmt.Errorf("mark awarded: %w", err)
		}
		return tx.Commit(ctx)
	}

	// 有重疊已發放筆 → 差額補償（見函式註解 5b）：以配對中最大值計、只補不扣。
	maxOverlapReward := 0
	maxOverlapKm := 0.0
	for i, km := range overlapKm {
		if rk := computeRewardKm(km, overlapDur[i], perKm, dpPerKm, capKm, minPaceS); rk > maxOverlapReward {
			maxOverlapReward = rk
		}
		if km > maxOverlapKm {
			maxOverlapKm = km
		}
	}
	deltaReward := clampDeltaInt(thisReward, maxOverlapReward)
	deltaKm := clampDeltaFloat(distanceKm, maxOverlapKm)
	deltaExpAmt := deltaReward * perKm
	deltaDpAmt := deltaReward * dpPerKm

	if deltaReward > 0 || deltaKm > 0 {
		var newTotalKm float64
		if err := tx.QueryRow(ctx,
			`UPDATE users SET total_km = total_km + $1, exp = exp + $2, dp = dp + $3, updated_at = NOW() WHERE id=$4 RETURNING total_km`,
			deltaKm, deltaExpAmt, deltaDpAmt, userID).Scan(&newTotalKm); err != nil {
			return fmt.Errorf("update user (delta): %w", err)
		}
		if err := awardReferralReward(ctx, tx, userID, newTotalKm); err != nil {
			return fmt.Errorf("referral reward (delta): %w", err)
		}
		if deltaReward > 0 {
			if _, err := tx.Exec(ctx,
				`INSERT INTO mileage_exp_events (user_id, exp_amount, dp_amount, km_added, distance_km, recorded_at)
				 VALUES ($1,$2,$3,$4,$5,$6)`, userID, deltaExpAmt, deltaDpAmt, deltaReward, distanceKm, recordedAt); err != nil {
				return fmt.Errorf("insert event (delta): %w", err)
			}
		}
	}
	if isExternal {
		if _, err := tx.Exec(ctx,
			`INSERT INTO external_award_ledger (user_id, source, ext_hash) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
			userID, *source, extHash); err != nil {
			return fmt.Errorf("ledger insert (delta): %w", err)
		}
	}
	// 無論 delta 是否為 0，一律標記 exp_awarded=TRUE（見函式註解 5b）。
	if _, err := tx.Exec(ctx, `UPDATE activities SET exp_awarded = TRUE WHERE id=$1`, activityID); err != nil {
		return fmt.Errorf("mark awarded (delta): %w", err)
	}
	return tx.Commit(ctx)
}

// awardReferralReward —— 此段為 services/api/internal/referral.Reward + internal/vip.Extend 的
// worker 複製版（worker 是獨立 Go module、不能 import services/api 的 internal package，故整段
// 內聯複製一份）。改動需與 services/api/internal/{vip,referral} 同步，保持完全一致的行為。
//
// 語意（與 referral.Reward 完全一致）：只在 newTotalKm >= 10 時才可能發獎。CAS 鎖定 referrals
// 該筆（rewarded_at IS NULL → 設值並 RETURNING referrer_user_id）：拿得到 row 才代表「這是本筆
// 推薦關係第一次達標」，據此對推薦人／被推薦人各自疊加 VIP 天數（extendVIP，SQL 逐字抄自
// internal/vip.Extend）。非推薦來的人（referrals 無此筆）或已發過獎（rewarded_at 已非 NULL）
// → CAS 拿不到 row（pgx.ErrNoRows），直接結束（no-op），確保一次性、冪等。
func awardReferralReward(ctx context.Context, tx pgx.Tx, userID string, newTotalKm float64) error {
	if newTotalKm < 10 {
		return nil
	}
	var referrerID string
	err := tx.QueryRow(ctx, `
		UPDATE referrals SET rewarded_at = now()
		WHERE referred_user_id = $1 AND rewarded_at IS NULL
		RETURNING referrer_user_id`, userID).Scan(&referrerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // 非推薦來的人，或這筆推薦關係已經發過獎
	}
	if err != nil {
		return fmt.Errorf("cas referrals: %w", err)
	}
	referrerDays := getAppSettingInt(ctx, tx, "referral_reward_referrer_days", 1)
	referredDays := getAppSettingInt(ctx, tx, "referral_reward_referred_days", 3)
	if err := extendVIP(ctx, tx, referrerID, referrerDays); err != nil {
		return fmt.Errorf("extend referrer vip: %w", err)
	}
	if err := extendVIP(ctx, tx, userID, referredDays); err != nil {
		return fmt.Errorf("extend referred vip: %w", err)
	}
	return nil
}

// getAppSettingInt —— 複製自 internal/appsettings.GetInt 的最小邏輯：查無/解析失敗回 def。
func getAppSettingInt(ctx context.Context, tx pgx.Tx, key string, def int) int {
	var v string
	if err := tx.QueryRow(ctx, `SELECT value FROM app_settings WHERE key=$1`, key).Scan(&v); err != nil {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

// extendVIP —— 逐字抄自 internal/vip.Extend 的 UPDATE SQL：延長使用者 VIP 到期日 days 天
// （從 max(現有到期, now()) 起算，不縮短既有 VIP）。days<=0 為 no-op。付費會員的 vip_plan 不覆蓋；
// 只有從未當過 VIP（空字串）才標記為 'bonus'。
func extendVIP(ctx context.Context, tx pgx.Tx, userID string, days int) error {
	if days <= 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE users SET
		  vip_expires_at = GREATEST(COALESCE(vip_expires_at, now()), now()) + make_interval(days => $2),
		  vip_since      = COALESCE(vip_since, now()),
		  vip_plan       = CASE WHEN COALESCE(vip_plan,'')='' THEN 'bonus' ELSE vip_plan END
		WHERE id = $1`, userID, days)
	return err
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt(i int) interface{} {
	if i == 0 {
		return nil
	}
	return i
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatal().Str("key", key).Msg("required env var not set")
	}
	return v
}
