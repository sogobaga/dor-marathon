// Migration runner — reads ./migrations/*.sql in order and applies unapplied ones.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	godotenv.Load()
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal().Msg("DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal().Err(err).Msg("connect db failed")
	}
	defer pool.Close()

	// 確保 schema_migrations 表存在
	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    VARCHAR(14) PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		log.Fatal().Err(err).Msg("create schema_migrations failed")
	}

	// 讀取已套用的版本
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		log.Fatal().Err(err).Msg("query migrations failed")
	}
	applied := map[string]bool{}
	for rows.Next() {
		var v string
		rows.Scan(&v)
		applied[v] = true
	}
	rows.Close()

	// 讀取 migrations/ 目錄下的 .sql 檔案
	migrDir := filepath.Join("migrations")
	files, err := filepath.Glob(filepath.Join(migrDir, "*.sql"))
	if err != nil || len(files) == 0 {
		log.Fatal().Str("dir", migrDir).Msg("no migration files found")
	}
	sort.Strings(files) // 按檔名排序（001, 002, ...）

	applied_count := 0
	for _, f := range files {
		base := filepath.Base(f)
		version := strings.Split(base, "_")[0] // e.g. "001" from "001_init.sql"

		if applied[version] {
			log.Info().Str("file", base).Msg("skip (already applied)")
			continue
		}

		sql, err := os.ReadFile(f)
		if err != nil {
			log.Fatal().Err(err).Str("file", f).Msg("read file failed")
		}

		// 在 transaction 中執行
		tx, err := pool.Begin(ctx)
		if err != nil {
			log.Fatal().Err(err).Msg("begin tx failed")
		}

		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			tx.Rollback(ctx)
			log.Fatal().Err(err).Str("file", base).Msg("migration failed")
		}

		// 記錄版本（若 SQL 裡沒有 INSERT INTO schema_migrations，這裡補一筆）
		tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`, version)

		if err := tx.Commit(ctx); err != nil {
			log.Fatal().Err(err).Str("file", base).Msg("commit failed")
		}

		log.Info().Str("file", base).Msg("applied ✓")
		applied_count++
	}

	if applied_count == 0 {
		fmt.Println("All migrations already applied.")
	} else {
		fmt.Printf("Applied %d migration(s).\n", applied_count)
	}

	_ = pgx.ErrNoRows // suppress unused import
}
