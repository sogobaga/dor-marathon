// compressimages — 一次性回填工具：對 images 表中已存在的大圖（>150KB）套用
// 與上傳流程相同的 image.CompressImage 規則，逐筆處理、失敗只跳過該筆、永不刪除資料。
// 用法：cd services/api && go run ./cmd/compressimages
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dor/api/internal/image"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const sizeThreshold = 150 * 1024 // 150KB

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

	rows, err := pool.Query(ctx,
		`SELECT id, mime, data, size FROM images WHERE size > $1 ORDER BY size DESC`, sizeThreshold)
	if err != nil {
		log.Fatal().Err(err).Msg("query images failed")
	}

	type row struct {
		id   string
		mime string
		data []byte
		size int
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.mime, &r.data, &r.size); err != nil {
			rows.Close()
			log.Fatal().Err(err).Msg("scan row failed")
		}
		list = append(list, r)
	}
	rows.Close()

	fmt.Printf("found %d image(s) > %dKB\n", len(list), sizeThreshold/1024)

	compressed := 0
	skipped := 0
	var savedBytes int64

	for _, r := range list {
		out, outMime, changed := image.CompressImage(r.data, r.mime)
		if !changed {
			fmt.Printf("skip(未變更或不安全壓縮) id=%s %dKB mime=%s\n", r.id, r.size/1024, r.mime)
			skipped++
			continue
		}

		newSize := len(out)
		_, err := pool.Exec(ctx,
			`UPDATE images SET data=$1, mime=$2, size=$3 WHERE id=$4`,
			out, outMime, newSize, r.id)
		if err != nil {
			fmt.Printf("skip(更新失敗: %v) id=%s %dKB\n", err, r.id, r.size/1024)
			skipped++
			continue
		}

		fmt.Printf("%s %dKB→%dKB\n", r.id, r.size/1024, newSize/1024)
		compressed++
		savedBytes += int64(r.size - newSize)
	}

	fmt.Printf("\n完成：壓縮 %d 張，跳過 %d 張，共省 %.1f MB\n",
		compressed, skipped, float64(savedBytes)/1024/1024)
}
