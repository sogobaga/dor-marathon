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
	streamKey         = "activity_queue"
	consumerGroup     = "activity_workers"
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

// recomputeStandings 跨來源去重 + 重算競賽分組成績。只在「剛處理完新活動」或啟動時呼叫，
// 閒置時完全不打 DB → 讓 Neon compute 休眠(scale-to-zero)。
func (w *Worker) recomputeStandings(ctx context.Context) {
	w.resolveCrossSourceDups(ctx) // 先跨來源去重，再算成績
	w.aggregateStandings(ctx)
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

	// 更新使用者累積里程
	if _, err := w.db.Exec(ctx, `
		UPDATE users SET total_km = total_km + $1, updated_at = NOW() WHERE id = $2
	`, evt.DistanceKm, evt.UserID); err != nil {
		return fmt.Errorf("update total_km: %w", err)
	}

	// 日常里程 EXP：單趟每滿一整公里 × per_km（含上限與配速防造假），並記事件供前台彈窗
	w.awardMileageExp(ctx, evt.UserID, evt.DistanceKm, evt.DurationS, evt.RecordedAt)

	return nil
}

// awardMileageExp 單趟里程獎勵：這一趟跑步每滿一整公里發一份（floor(單趟距離)），並套用：
//   ① 單趟上限 mileage_cap_km（避免一趟灌爆）
//   ② 防造假配速下限 mileage_min_pace_s：此趟時間內、以最快合理配速最多能跑 duration/min_pace 公里，
//      超過的距離不列入發獎（擋「短時間灌大距離」的假 GPS）。
// 每個 activity 只處理一次（HandleActivity 於重複時已提早 return）→ 天然冪等，不需累積計數器。
func (w *Worker) awardMileageExp(ctx context.Context, userID string, distanceKm float64, durationS int, recordedAt string) {
	if distanceKm < 1 || durationS <= 0 {
		return // 不足 1km 或無時間 → 不發
	}
	var perKm, dpPerKm, capKm, minPaceS int
	if err := w.db.QueryRow(ctx, `
		SELECT COALESCE(per_km,0), COALESCE(dp_per_km,0),
		       COALESCE(mileage_cap_km,21), COALESCE(mileage_min_pace_s,120)
		FROM exp_rules WHERE id=TRUE`).Scan(&perKm, &dpPerKm, &capKm, &minPaceS); err != nil {
		return
	}
	if perKm <= 0 && dpPerKm <= 0 {
		return
	}
	rewardKm := int(distanceKm) // 單趟整公里數（floor）
	if capKm > 0 && rewardKm > capKm {
		rewardKm = capKm // ① 單趟上限
	}
	if minPaceS > 0 {
		if maxByTime := durationS / minPaceS; rewardKm > maxByTime { // ② 配速防造假（整數除＝floor）
			rewardKm = maxByTime
		}
	}
	if rewardKm <= 0 {
		return
	}
	expAmt := rewardKm * perKm
	dpAmt := rewardKm * dpPerKm

	tx, err := w.db.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`UPDATE users SET exp = exp + $1, dp = dp + $2 WHERE id=$3`, expAmt, dpAmt, userID); err != nil {
		return
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO mileage_exp_events (user_id, exp_amount, dp_amount, km_added, distance_km, recorded_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`, userID, expAmt, dpAmt, rewardKm, distanceKm, recordedAt); err != nil {
		return
	}
	tx.Commit(ctx)
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
