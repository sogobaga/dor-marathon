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
	batchSize         = 100
	batchInterval     = 5 * time.Second
	standingsInterval = 30 * time.Second // 競賽分組成績重算間隔
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
	go w.standingsLoop(ctx) // 背景定期重算競賽分組成績
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
			w.processBatch(ctx)
		}
	}
}

// standingsLoop 背景定期重算競賽分組成績（與 activity stream 分開，避免被 XReadGroup 阻塞）
func (w *Worker) standingsLoop(ctx context.Context) {
	ticker := time.NewTicker(standingsInterval)
	defer ticker.Stop()

	w.aggregateStandings(ctx) // 啟動時先算一次
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.aggregateStandings(ctx)
		}
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
		LEFT JOIN activities a ON a.user_id = reg.user_id AND a.race_id = rg.race_id
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

func (w *Worker) processBatch(ctx context.Context) {
	// 讀取 Redis Streams pending + new messages
	streams, err := w.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    consumerGroup,
		Consumer: w.consumerName,
		Streams:  []string{streamKey, ">"},
		Count:    batchSize,
		Block:    0,
	}).Result()
	if err != nil || len(streams) == 0 {
		return
	}

	msgs := streams[0].Messages
	if len(msgs) == 0 {
		return
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
		INSERT INTO activities (user_id, race_id, mission_day, distance_km, duration_s, avg_pace_s, recorded_at, processed)
		VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)
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

	// 日常里程 EXP：每跨一整公里 × per_km，並記事件供前台彈窗
	w.awardMileageExp(ctx, evt.UserID, evt.DistanceKm, evt.RecordedAt)

	return nil
}

// awardMileageExp 依使用者累積里程的「整公里跨越」發 EXP（idempotent：只發 exp_rewarded_km 之後的差額）
func (w *Worker) awardMileageExp(ctx context.Context, userID string, distanceKm float64, recordedAt string) {
	tx, err := w.db.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)

	var oldKm, newKm, perKm int
	if err := tx.QueryRow(ctx, `
		SELECT exp_rewarded_km, floor(total_km)::int, COALESCE((SELECT per_km FROM exp_rules WHERE id=TRUE),0)
		FROM users WHERE id=$1`, userID).Scan(&oldKm, &newKm, &perKm); err != nil {
		return
	}
	delta := newKm - oldKm
	if delta <= 0 || perKm <= 0 {
		return
	}
	expAmt := delta * perKm
	if _, err := tx.Exec(ctx,
		`UPDATE users SET exp = exp + $1, exp_rewarded_km = $2 WHERE id=$3 AND exp_rewarded_km = $4`,
		expAmt, newKm, userID, oldKm); err != nil {
		return
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO mileage_exp_events (user_id, exp_amount, km_added, distance_km, recorded_at)
		 VALUES ($1,$2,$3,$4,$5)`, userID, expAmt, delta, distanceKm, recordedAt); err != nil {
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
