package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse db config: %w", err)
	}

	// 連線池設定：配合 PgBouncer transaction mode
	// MaxConns=20：原 10 在 1000 併發下是全站排隊瓶頸；20 仍遠低於 Neon pooler 預設 default_pool_size=64，安全上調。
	cfg.MaxConns = 20
	// MinConns=0：閒置時不保留熱連線 → Neon compute 可完全休眠(scale-to-zero)，有請求再懶惰重連。
	cfg.MinConns = 0

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}

	return pool, nil
}
