// Package testdb 需要真實 Postgres 的整合測試（go test -tags=integration）共用的小工具。
//
// 只被 *_test.go 引用（不會進正式二進位）。刻意是 leaf 套件、不 import 任何業務套件：appsettings 自己的整合測試
// 也要用它，若它 import appsettings 就會循環——需要的設定鍵名由呼叫端傳入。
//
// 解決的問題（Terra／Strava 串接結束公告的三個套件整合測試）：integration、appsettings、integration/wearablesunset 的
// 整合測試都會寫同樣兩個全域 app_settings 列（wearable_sunset_state／wearable_sunset_date）並在結束時刪掉。
// go test 預設把不同套件當成不同行程平行跑（-p），所以 A 套件的測試寫進 announce 的當下，B 套件的 cleanup 可能剛好把它
// 刪掉（或反過來被 B 寫的值污染），結果是看時機的隨機失敗。這裡用一把 Postgres advisory lock 讓這些測試一次只跑一個
// （跨行程有效，不必記得加 -p 1），並在結束時把兩個列還原成測試前的樣子。
package testdb

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// EnvDSN 整合測試連線字串的環境變數（各套件既有慣例）。
	EnvDSN = "DOR_TEST_DATABASE_URL"
	// EnvAllowRemote 設為 1 才允許連到非本機的資料庫（例如 CI 以服務名稱連 Postgres 容器）。預設拒絕：
	// 這些測試會寫入並刪除 app_settings 的列，絕不能指到正式庫。
	EnvAllowRemote = "DOR_TEST_DATABASE_ALLOW_REMOTE"
	// EnvProdDSN 正式環境（API 本身）用的連線字串環境變數。測試行程若碰巧帶著它（例如 shell 裡載入過 .env），
	// 測試連線目標的主機絕不可與它相同——即使設了 EnvAllowRemote 也一樣（見 CheckDSN）。
	EnvProdDSN = "DATABASE_URL"

	// SunsetSettingsLock 三個套件的整合測試共用的 advisory lock 名稱（hashtext 成 key）。
	SunsetSettingsLock = "dor.test.wearable_sunset_settings"

	sunsetLockWait = 90 * time.Second
)

// OpenSunsetSettings 開一個連到 DOR_TEST_DATABASE_URL 的連線池（未設定就 Skip），並讓「這個測試」獨占 keys 指定的
// app_settings 列（呼叫端傳 wearable_sunset_state／wearable_sunset_date 的鍵名）：
//   - 連線目標必須是本機資料庫（loopback、localhost、unix socket），否則 t.Fatalf——設 DOR_TEST_DATABASE_ALLOW_REMOTE=1 才放行。
//   - 不論有沒有設 ALLOW_REMOTE，連線目標的主機若與 DATABASE_URL（正式庫）相同（去掉 "-pooler" 後比對）一律 t.Fatalf。
//   - 取得 SunsetSettingsLock（blocking，最久等 90 秒；行程結束連線關閉時自動釋放，不會卡死後面的測試）。
//   - 取得鎖之後記下 keys 目前的值；測試結束時（在測試自己的 cleanup 之後）還原成原樣，再釋放鎖。
//
// 一個測試只能呼叫一次（同一個行程內第二次取同一把鎖會等自己而死鎖）。
func OpenSunsetSettings(t testing.TB, keys ...string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(EnvDSN)
	if dsn == "" {
		t.Skipf("%s not set; skipping SQL integration tests", EnvDSN)
	}
	if err := CheckDSN(dsn, os.Getenv(EnvProdDSN), os.Getenv(EnvAllowRemote) == "1"); err != nil {
		t.Fatalf("%s: %v", EnvDSN, err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil { // CheckDSN 已經解析成功過，實際上不會走到這裡；訊息不帶 err（pgx 的巢狀錯誤會回顯含密碼的整串連線字串）
		t.Fatalf("%s is not a valid connection string", EnvDSN)
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close) // 最先註冊＝最後執行：鎖與還原都做完才關池

	ctx, cancel := context.WithTimeout(context.Background(), sunsetLockWait)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext($1))`, SunsetSettingsLock); err != nil {
		conn.Release()
		t.Fatalf("could not take the wearable-sunset test lock within %s (another test process is holding it?): %v", sunsetLockWait, err)
	}

	// 取得鎖之後才記原值：這時沒有別的測試行程會改這些列。
	orig := map[string]string{}
	rows, err := conn.Query(ctx, `SELECT key, value FROM app_settings WHERE key = ANY($1)`, keys)
	if err != nil {
		unlockAndRelease(conn)
		t.Fatalf("snapshot app_settings: %v", err)
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			unlockAndRelease(conn)
			t.Fatalf("snapshot app_settings: %v", err)
		}
		orig[k] = v
	}
	if err := rows.Err(); err != nil {
		unlockAndRelease(conn)
		t.Fatalf("snapshot app_settings: %v", err)
	}

	t.Cleanup(func() {
		defer unlockAndRelease(conn)
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		for _, k := range keys {
			var err error
			if v, ok := orig[k]; ok {
				_, err = conn.Exec(cctx, `INSERT INTO app_settings (key, value, updated_at) VALUES ($1,$2,NOW())
					ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`, k, v)
			} else {
				_, err = conn.Exec(cctx, `DELETE FROM app_settings WHERE key=$1`, k)
			}
			if err != nil {
				t.Errorf("restore app_settings %q: %v", k, err)
			}
		}
	})
	return pool
}

// unlockAndRelease 釋放 advisory lock 並把連線還給池；解鎖失敗就直接關掉這條連線（連線結束鎖一定會釋放）。
func unlockAndRelease(conn *pgxpool.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, SunsetSettingsLock); err != nil {
		_ = conn.Hijack().Close(ctx)
		return
	}
	conn.Release()
}

// CheckDSN 整合測試連線字串的安全檢查（純函式，不連線、不讀環境變數）。回傳 nil 才可以連：
//  1. testDSN 必須能解析（pgx 的解析器，URL 與 key=value 兩種寫法、多主機都認得）。
//  2. 不論 allowRemote：testDSN 的任何主機與 prodDSN（DATABASE_URL，正式庫）的任何主機相同（小寫、去掉 Neon 的 "-pooler"
//     後比對；pooler 與直連是同一個資料庫）一律拒絕。比照 runmeet/live_start_integration_test.go 的同主機守門，
//     只比主機名稱：prodDSN 是空字串（行程沒有 DATABASE_URL）或解析不出主機就沒有東西可比、略過這一條。
//  3. 連線目標（含所有備援主機）必須看起來是本機（RequireThrowawayHost）——除非 allowRemote。
//
// 同主機規則排在本機規則之前：正式庫的主機名稱絕不能被後面那條錯誤訊息印出來。同主機的錯誤訊息不含主機名稱，
// 解析失敗的錯誤也不帶 pgx 的原文（它巢狀的 url.Parse 錯誤會原樣回顯整串連線字串，包含密碼）。
// 注意：loopback 主機只比主機名稱、不比連接埠——若行程的 DATABASE_URL 也指向同一個 loopback 主機（例如 SSH 隧道連正式庫），
// 即使連接埠不同也會被拒絕（寧可誤擋）；跑這些測試時請不要帶著指向本機的 DATABASE_URL。
func CheckDSN(testDSN, prodDSN string, allowRemote bool) error {
	if strings.TrimSpace(testDSN) == "" {
		// pgx 把空字串當成「全部用預設值」（連 PGHOST／localhost），不能讓空的連線字串悄悄走這條路
		return errors.New("is empty")
	}
	cfg, err := pgxpool.ParseConfig(testDSN)
	if err != nil {
		return errors.New("is not a valid connection string (details omitted: the parser's message can echo the password)")
	}
	testHosts := []string{cfg.ConnConfig.Host}
	for _, fb := range cfg.ConnConfig.Fallbacks {
		testHosts = append(testHosts, fb.Host)
	}

	prodHosts := dsnHosts(prodDSN)
	for _, th := range testHosts {
		nth := normalizeHost(th)
		for _, ph := range prodHosts {
			if nth != "" && nth == ph {
				return fmt.Errorf("refusing to run: the database host is the same as %s's (the production database); "+
					"these tests write and delete rows, so they must never touch it, even with %s=1", EnvProdDSN, EnvAllowRemote)
			}
		}
	}

	if !allowRemote {
		for _, h := range testHosts {
			if err := RequireThrowawayHost(h); err != nil {
				return fmt.Errorf("%w (set %s=1 only if this really is a throwaway database)", err, EnvAllowRemote)
			}
		}
	}
	return nil
}

// normalizeHost 主機名稱的比對形式：去空白、小寫、去掉 Neon 的 "-pooler"（連線池端點與直連端點是同一個資料庫）。
func normalizeHost(h string) string {
	return strings.Replace(strings.ToLower(strings.TrimSpace(h)), "-pooler", "", 1)
}

// dsnHosts 連線字串涵蓋的所有主機（主要＋備援），已正規化；空字串或解析不出任何主機回 nil。
// 先用 pgx 的解析器（與真正連線同一套規則）；pgx 不認得（例如 sslmode 值不合法）但仍是 URL 時，退而求其次從 URL 取主機，
// 寧可多擋也不要因為解析失敗就略過同主機守門。
func dsnHosts(dsn string) []string {
	if strings.TrimSpace(dsn) == "" {
		return nil
	}
	var raw []string
	if cfg, err := pgxpool.ParseConfig(dsn); err == nil {
		raw = append(raw, cfg.ConnConfig.Host)
		for _, fb := range cfg.ConnConfig.Fallbacks {
			raw = append(raw, fb.Host)
		}
	} else if u, err := url.Parse(dsn); err == nil {
		for _, hp := range strings.Split(u.Host, ",") {
			if h, _, err := net.SplitHostPort(hp); err == nil {
				hp = h
			}
			raw = append(raw, strings.Trim(hp, "[]"))
		}
	}
	var hosts []string
	for _, h := range raw {
		if n := normalizeHost(h); n != "" {
			hosts = append(hosts, n)
		}
	}
	return hosts
}

// RequireThrowawayHost 資料庫主機看起來是不是本機（拋棄式容器）：空字串（預設 localhost）、localhost、loopback IP、
// unix socket 目錄算；其他（雲端主機、內網 IP、docker 服務名稱）一律拒絕並回說明。
func RequireThrowawayHost(host string) error {
	h := strings.TrimSpace(host)
	if h == "" || strings.HasPrefix(h, "/") || strings.EqualFold(h, "localhost") || strings.HasSuffix(strings.ToLower(h), ".localhost") {
		return nil
	}
	if ip := net.ParseIP(strings.Trim(h, "[]")); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("refusing to run: database host %q does not look like a local throwaway database (these tests write and delete app_settings rows)", h)
}
