package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
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

	// DB
	pool, err := pgxpool.New(ctx, mustEnv("DATABASE_URL"))
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

	w := &Worker{db: pool, rdb: rdb, consumerName: consumerName}
	w.recomputeStandings(ctx) // 啟動時先算一次(補齊停機期間累積)；之後改「有新活動才重算」，閒置不打 DB → 讓 Neon 休眠
	w.run(ctx)
}

type Worker struct {
	db           *pgxpool.Pool
	rdb          *redis.Client
	consumerName string
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
			if w.processBatch(ctx) > 0 {
				w.recomputeStandings(ctx) // 有新活動才重算成績(事件驅動)；閒置(Redis 阻塞)時完全不打 DB
			}
		}
	}
}

// recomputeStandings 跨來源去重 + 里程 EXP/DP 對帳補發 + 重算競賽分組成績。只在「剛處理完新活動」
// 或啟動時呼叫，閒置時完全不打 DB → 讓 Neon compute 休眠(scale-to-zero)。
func (w *Worker) recomputeStandings(ctx context.Context) {
	w.resolveCrossSourceDups(ctx) // 先跨來源去重，再算成績
	w.reconcileMileageExp(ctx)    // 對帳補發：抓漏發的里程 EXP/DP（inline 發放失敗的補救網）
	w.aggregateStandings(ctx)
}

// reconcileMileageExp 對帳補發：inline 發放（本 worker processOne / api 端 Strava·Terra 匯入）
// 若因暫時性錯誤（DB 抖動、連線問題等）失敗，該筆活動會停在 exp_awarded=false 永久漏發
// （worker 目前讀 Redis Stream 只讀 ">"（新訊息），不會重讀 PEL 裡的 pending 訊息；api 端失敗則只有 log，
// 完全沒有重試）。這裡定期掃描「已寫入一段時間、卻仍未標記 exp_awarded」的活動，逐筆重新呼叫
// awardMileageDedup —— 該函式本身冪等 + 去重安全：真正已被別的來源計過的會再次被正確跳過，
// 純粹漏發的則會補上，三來源（GPS/Strava/Terra）皆涵蓋。
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
// 兩筆、且時間重疊時（GPS 存結束時間、Strava 存開始時間，故用各自區間判重疊），依使用者偏好來源
// （user_profiles.preferred_data_source，預設 gps）保留一筆、另一筆標 flagged=cross_source_duplicate、
// dup_of 指向保留的那筆 → 賽事排名/完賽 SUM(NOT flagged) 只算一筆。只處理「雙方都尚未 flagged」的新配對。
func (w *Worker) resolveCrossSourceDups(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// N 來源優先序去重：每筆算出「優先序 rank」（使用者偏好來源=0，其餘 garmin>coros>strava>gps）；
	// 對每筆活動，若有時間重疊、且優先序更高（rank 更小）的另一筆存在 → 標記為 cross_source_duplicate、
	// dup_of 指向重疊中優先序最高那筆。每個時間叢集只保留優先序最高的一筆。
	// 起始時間統一：GPS(source NULL) 存結束時間 → 起=recorded_at-dur；其餘來源存起始時間 → 起=recorded_at。
	tag, err := w.db.Exec(cctx, `
		WITH pref AS (SELECT user_id, COALESCE(preferred_data_source,'gps') AS src FROM user_profiles),
		ranked AS (
			SELECT a.id, a.user_id, a.duration_s AS dur,
				CASE WHEN a.source IS NULL THEN a.recorded_at - make_interval(secs=>a.duration_s) ELSE a.recorded_at END AS st,
				CASE
					WHEN COALESCE(a.source,'gps') = COALESCE(p.src,'gps') THEN 0
					WHEN COALESCE(a.source,'gps') = 'garmin' THEN 1
					WHEN COALESCE(a.source,'gps') = 'coros'  THEN 2
					WHEN COALESCE(a.source,'gps') = 'strava' THEN 3
					WHEN COALESCE(a.source,'gps') = 'gps'    THEN 4
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
	if n := tag.RowsAffected(); n > 0 {
		log.Info().Int64("flagged", n).Msg("cross-source duplicates resolved")
	}
}

// aggregateStandings 以單一查詢重算所有競賽模式賽事的 race_group_standings（預聚合，前台直接讀）。
// 各分組：總累積里程、成員數、平均里程、平均配速（總時間/總里程）、完成累計總時間（成員總移動時間）。
func (w *Worker) aggregateStandings(ctx context.Context) {
	tag, err := w.db.Exec(ctx, `
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
		LEFT JOIN registrations reg ON reg.group_id = rg.id AND reg.status = 'paid'
		LEFT JOIN activities a ON a.user_id = reg.user_id AND NOT a.flagged
		                       AND a.recorded_at BETWEEN r.start_date AND r.end_date
		GROUP BY rg.race_id, rg.id
		ON CONFLICT (race_id, group_id) DO UPDATE SET
			total_km       = EXCLUDED.total_km,
			member_count   = EXCLUDED.member_count,
			avg_km         = EXCLUDED.avg_km,
			avg_pace_s     = EXCLUDED.avg_pace_s,
			finish_total_s = EXCLUDED.finish_total_s,
			updated_at     = NOW()
	`)
	if err != nil {
		log.Error().Err(err).Msg("aggregate standings failed")
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		log.Debug().Int64("groups", n).Msg("standings aggregated")
	}
}

// processBatch 讀取並處理一批活動訊息；回傳成功處理的筆數（供 run 決定是否重算成績）。
func (w *Worker) processBatch(ctx context.Context) int {
	// 讀取 Redis Streams pending + new messages
	streams, err := w.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    consumerGroup,
		Consumer: w.consumerName,
		Streams:  []string{streamKey, ">"},
		Count:    batchSize,
		Block:    0,
	}).Result()
	if err != nil || len(streams) == 0 {
		return 0
	}

	msgs := streams[0].Messages
	if len(msgs) == 0 {
		return 0
	}

	log.Debug().Int("count", len(msgs)).Msg("processing activity batch")

	var ids []string
	for _, msg := range msgs {
		ids = append(ids, msg.ID)
		if err := w.processOne(ctx, msg); err != nil {
			log.Error().Err(err).Str("msg_id", msg.ID).Msg("failed to process activity")
			// 保留在 pending list，稍後重試
			ids = ids[:len(ids)-1]
		}
	}

	// ACK 成功處理的訊息
	if len(ids) > 0 {
		w.rdb.XAck(ctx, streamKey, consumerGroup, ids...)
	}
	return len(ids)
}

func (w *Worker) processOne(ctx context.Context, msg redis.XMessage) error {
	raw, ok := msg.Values["data"].(string)
	if !ok {
		return fmt.Errorf("missing data field")
	}

	var evt ActivityEvent
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		return fmt.Errorf("unmarshal event: %w", err)
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
		return nil // 重複活動，略過（不再累加里程）
	}
	if err != nil {
		return fmt.Errorf("insert activity: %w", err)
	}

	// 去重感知、冪等地發放里程 EXP/DP/total_km（取代舊的「無條件 UPDATE total_km」+ awardMileageExp）：
	// 若同一使用者存在時間重疊、已由其他來源（Strava/Terra）發放過的活動，這裡就不重發，
	// 避免同一趟跑步被 GPS 與第三方來源各發一次。
	if err := w.awardMileageDedup(ctx, newID, evt.UserID); err != nil {
		return fmt.Errorf("award mileage dedup: %w", err)
	}

	return nil
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
//  3. 去重：查該使用者是否存在「時間重疊且已發放(exp_awarded=true)」的其他活動（比照
//     resolveCrossSourceDups 的重疊判定——⚠️ GPS(source IS NULL) 的 recorded_at 存的是「結束時間」，
//     Strava/Terra(source 非 NULL) 存的是「開始時間」，兩者語意不同，比對前都要正規化成 [start,end)
//     區間再判重疊，否則會誤判/漏判）。若存在 → 這趟已被別的來源計過 → 本筆不發
//     EXP/DP/total_km，且刻意保留 exp_awarded=false（不標記，避免誤判為「已處理」）。
//  4. 發放（無重疊）：讀 exp_rules（per_km/dp_per_km/mileage_cap_km/mileage_min_pace_s：
//     floor(distance) → ①單趟上限 mileage_cap_km ②配速防造假 mileage_min_pace_s）→
//     UPDATE users.total_km/exp/dp → INSERT mileage_exp_events → UPDATE activities.exp_awarded=true。
//     全部在同一交易內；total_km 也只在「無重疊」時加，確保一趟只加一次。
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

	// ② 冪等：鎖定本筆活動；一併撈 source 才能正規化本筆的 [start,end) 區間
	var awarded bool
	var distanceKm float64
	var durationS int
	var recordedAt time.Time
	var source *string
	if err := tx.QueryRow(ctx, `
		SELECT exp_awarded, distance_km, duration_s, recorded_at, source
		FROM activities WHERE id=$1 AND user_id=$2 FOR UPDATE`, activityID, userID).
		Scan(&awarded, &distanceKm, &durationS, &recordedAt, &source); err != nil {
		return fmt.Errorf("load activity: %w", err)
	}
	if awarded {
		return nil // 已發過，不重發
	}

	// 正規化本筆時間為 [thisStart, thisEnd)：GPS(source IS NULL) 的 recorded_at 是結束時間，
	// 其餘來源(strava/garmin/coros)的 recorded_at 是開始時間（比照 resolveCrossSourceDups）。
	thisStart := recordedAt
	if source == nil {
		thisStart = recordedAt.Add(-time.Duration(durationS) * time.Second)
	}
	thisEnd := thisStart.Add(time.Duration(durationS) * time.Second)

	// ③ 去重：是否已有時間重疊、且已發放的其他活動（任何來源）。候選列同樣依 source 正規化成
	// [candStart, candEnd) 再判重疊（candStart < thisEnd AND candEnd > thisStart）。
	var dup bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM activities a
			WHERE a.user_id=$1 AND a.exp_awarded=true AND a.id<>$2
			  AND (CASE WHEN a.source IS NULL THEN a.recorded_at - make_interval(secs=>a.duration_s) ELSE a.recorded_at END) < $3
			  AND (CASE WHEN a.source IS NULL THEN a.recorded_at - make_interval(secs=>a.duration_s) ELSE a.recorded_at END)
			      + make_interval(secs=>a.duration_s) > $4
		)`, userID, activityID, thisEnd, thisStart).Scan(&dup); err != nil {
		return fmt.Errorf("overlap check: %w", err)
	}
	if dup {
		// 這趟已被時間重疊的另一筆活動計過（不論來源）；本筆不發，exp_awarded 維持 false。
		return tx.Commit(ctx)
	}

	// ④ 發放：讀 exp_rules
	var perKm, dpPerKm, capKm, minPaceS int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(per_km,0), COALESCE(dp_per_km,0),
		       COALESCE(mileage_cap_km,21), COALESCE(mileage_min_pace_s,120)
		FROM exp_rules WHERE id=TRUE`).Scan(&perKm, &dpPerKm, &capKm, &minPaceS); err != nil {
		return fmt.Errorf("load exp_rules: %w", err)
	}

	rewardKm := 0
	if distanceKm >= 1 && durationS > 0 && (perKm > 0 || dpPerKm > 0) {
		rewardKm = int(distanceKm) // floor(單趟距離)
		if capKm > 0 && rewardKm > capKm {
			rewardKm = capKm
		}
		if minPaceS > 0 {
			if maxByTime := durationS / minPaceS; rewardKm > maxByTime {
				rewardKm = maxByTime
			}
		}
		if rewardKm < 0 {
			rewardKm = 0
		}
	}
	expAmt := rewardKm * perKm
	dpAmt := rewardKm * dpPerKm

	// total_km 一趟只加一次（走到這裡即代表本趟首次被計入，不論最終 rewardKm 是否 > 0）
	if _, err := tx.Exec(ctx,
		`UPDATE users SET total_km = total_km + $1, exp = exp + $2, dp = dp + $3, updated_at = NOW() WHERE id=$4`,
		distanceKm, expAmt, dpAmt, userID); err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	if rewardKm > 0 {
		if _, err := tx.Exec(ctx,
			`INSERT INTO mileage_exp_events (user_id, exp_amount, dp_amount, km_added, distance_km, recorded_at)
			 VALUES ($1,$2,$3,$4,$5,$6)`, userID, expAmt, dpAmt, rewardKm, distanceKm, recordedAt); err != nil {
			return fmt.Errorf("insert event: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE activities SET exp_awarded = TRUE WHERE id=$1`, activityID); err != nil {
		return fmt.Errorf("mark awarded: %w", err)
	}
	return tx.Commit(ctx)
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
