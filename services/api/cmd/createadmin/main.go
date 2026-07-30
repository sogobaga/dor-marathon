// 建立 / 更新一個 admin 帳號（role=admin）。
// 用法（本機，連 postgres:5432）：
//
//	DATABASE_URL=postgres://dor:dor_dev_secret@localhost:5432/dor_db \
//	  ADMIN_LOGIN=<自訂帳號> ADMIN_PASS=<自訂強密碼> go run ./cmd/createadmin
//
// ADMIN_LOGIN / ADMIN_PASS 必須明確指定（不再有預設值）：本指令對既有帳號是
// ON CONFLICT DO UPDATE 靜默覆寫密碼＋強制 is_super_admin=TRUE，若沿用固定預設弱密碼，
// 誤對正式庫執行等同開後門／覆寫掉正式 super_admin 密碼。未設任一變數會直接拒絕執行。
//
// 登入帳號存於 users.email 欄位（系統以 email 為登入識別），可為純帳號字串。
package main

import (
	"context"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	godotenv.Load()
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal().Msg("DATABASE_URL not set")
	}
	login := mustEnv("ADMIN_LOGIN")
	pass := mustEnv("ADMIN_PASS")
	name := getenv("ADMIN_NAME", "管理員")

	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal().Err(err).Msg("hash password")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal().Err(err).Msg("connect db")
	}
	defer pool.Close()

	// 以 email 欄位作為登入帳號；存在則更新密碼並確保 role=admin。
	// CLI 建立的一律為超級管理員（bootstrap；可管理其他管理者與全部模組）。
	_, err = pool.Exec(ctx, `
		INSERT INTO users (email, handle, name, password_hash, role, is_super_admin)
		VALUES ($1, $1, $2, $3, 'admin', TRUE)
		ON CONFLICT (email) DO UPDATE
		  SET password_hash  = EXCLUDED.password_hash,
		      role           = 'admin',
		      name           = EXCLUDED.name,
		      is_super_admin = TRUE
	`, login, name, string(hash))
	if err != nil {
		log.Fatal().Err(err).Msg("upsert admin")
	}
	log.Info().Str("login", login).Str("role", "admin").Msg("admin account ready ✓")
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// mustEnv 強制要求環境變數必須明確設定，未設即印警告並拒絕執行（log.Fatal 會 os.Exit(1)）——
// 避免任何人省略 ADMIN_LOGIN/ADMIN_PASS 誤用固定預設值，對正式庫建立或覆寫成弱密碼 super_admin。
func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatal().Msgf("%s not set — refusing to run with a default admin credential; set %s explicitly", k, k)
	}
	return v
}
