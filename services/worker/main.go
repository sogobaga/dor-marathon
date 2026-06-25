package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	// 寫入 PostgreSQL
	_, err := w.db.Exec(ctx, `
		INSERT INTO activities (user_id, race_id, mission_day, distance_km, duration_s, avg_pace_s, recorded_at, processed)
		VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)
		ON CONFLICT DO NOTHING
	`,
		evt.UserID,
		nullableString(evt.RaceID),
		nullableInt(evt.MissionDay),
		evt.DistanceKm,
		evt.DurationS,
		evt.AvgPaceS,
		evt.RecordedAt,
	)
	if err != nil {
		return fmt.Errorf("insert activity: %w", err)
	}

	// 更新使用者累積里程
	w.db.Exec(ctx, `
		UPDATE users SET total_km = total_km + $1, updated_at = NOW() WHERE id = $2
	`, evt.DistanceKm, evt.UserID)

	return nil
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
