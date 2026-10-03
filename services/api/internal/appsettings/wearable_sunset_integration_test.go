//go:build integration

// 需要真實 Postgres（已套用 app_settings 所在的 migrations）；獨立 build tag：
//
//	DOR_TEST_DATABASE_URL=postgres://…?sslmode=disable go test -tags=integration ./internal/appsettings/ -run SunsetIT
//
// 驗證 Set 的跨鍵檢查真的「現查資料庫」讀另一個鍵（validateWrite 預設的讀取路徑），而不是 60 秒快取。
//
// 會寫入並刪除全域的 wearable_sunset_* 兩個 app_settings 列（integration 與 integration/wearablesunset 套件的整合測試
// 也會寫同樣的列）。連線一律經 internal/testdb.OpenSunsetSettings：只接受連到本機的資料庫、用 Postgres advisory lock
// 讓三個套件的測試一次只跑一個（不必加 -p 1）、結束時把兩個列還原成測試前的樣子。
package appsettings

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/testdb"
)

func sunsetITPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.OpenSunsetSettings(t, WearableSunsetStateKey, WearableSunsetDateKey)
}

func sunsetITPut(t *testing.T, pool *pgxpool.Pool, key, val string) {
	t.Helper()
	ctx := context.Background()
	var err error
	if val == "" {
		_, err = pool.Exec(ctx, `DELETE FROM app_settings WHERE key=$1`, key)
	} else {
		_, err = pool.Exec(ctx, `INSERT INTO app_settings (key, value, updated_at) VALUES ($1,$2,NOW())
			ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`, key, val)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestSunsetIT_ValidateWriteReadsTheOtherKeyFreshFromDB(t *testing.T) {
	pool := sunsetITPool(t)
	ctx := context.Background()
	t.Cleanup(func() {
		sunsetITPut(t, pool, WearableSunsetStateKey, "")
		sunsetITPut(t, pool, WearableSunsetDateKey, "")
		InvalidateCache()
	})
	h := &Handler{db: pool, now: func() time.Time { return time.Date(2026, 10, 3, 12, 0, 0, 0, taipei) }}

	// 兩個鍵都沒有：announce＋預設結束日（2026-10-31）還在未來 → 可寫；結束日改成過去且狀態缺鍵 → 可寫（不是 announce）
	sunsetITPut(t, pool, WearableSunsetStateKey, "")
	sunsetITPut(t, pool, WearableSunsetDateKey, "")
	if status, msg := h.validateWrite(ctx, WearableSunsetStateKey, "announce"); status != 0 {
		t.Fatalf("announce with the default date: %d %s", status, msg)
	}
	if status, msg := h.validateWrite(ctx, WearableSunsetDateKey, "2026-10-01"); status != 0 {
		t.Fatalf("past date while the state key is missing must be allowed: %d %s", status, msg)
	}

	// 資料庫裡已有過去的結束日：不能啟用公告
	sunsetITPut(t, pool, WearableSunsetDateKey, "2026-10-01")
	if status, _ := h.validateWrite(ctx, WearableSunsetStateKey, "announce"); status != http.StatusBadRequest {
		t.Fatalf("announce with a stored past date must be rejected, got %d", status)
	}
	// 修成未來日期後（不 InvalidateCache——證明讀的是現查而不是 60 秒快取）就可以
	sunsetITPut(t, pool, WearableSunsetDateKey, "2026-10-31")
	if status, msg := h.validateWrite(ctx, WearableSunsetStateKey, "announce"); status != 0 {
		t.Fatalf("announce after fixing the date: %d %s (cross-check must read the DB fresh)", status, msg)
	}

	// 公告進行中：把結束日改到過去被擋，改到未來通過
	sunsetITPut(t, pool, WearableSunsetStateKey, "announce")
	if status, _ := h.validateWrite(ctx, WearableSunsetDateKey, "2026-10-02"); status != http.StatusBadRequest {
		t.Fatalf("past date while announcing must be rejected, got %d", status)
	}
	if status, msg := h.validateWrite(ctx, WearableSunsetDateKey, "2026-11-15"); status != 0 {
		t.Fatalf("future date while announcing: %d %s", status, msg)
	}
	// 關掉公告（off）：不管結束日
	sunsetITPut(t, pool, WearableSunsetDateKey, "2026-10-01")
	if status, msg := h.validateWrite(ctx, WearableSunsetStateKey, "off"); status != 0 {
		t.Fatalf("turning the announcement off must always be allowed: %d %s", status, msg)
	}
}

// LookupString 在真的 Postgres 上：缺鍵（pgx.ErrNoRows）、有值（含前後空白）、以及「連線池壞掉」的故障要分得開，錯誤不快取。
// 故障用一條被關掉的連線池模擬（對它的查詢會立刻回錯）。
func TestSunsetIT_LookupStringSeparatesAnErrorFromAMissingKey(t *testing.T) {
	pool := sunsetITPool(t)
	ctx := context.Background()
	t.Cleanup(func() {
		sunsetITPut(t, pool, WearableSunsetStateKey, "")
		sunsetITPut(t, pool, WearableSunsetDateKey, "")
		InvalidateCache()
	})

	// 缺鍵：回 (空字串, false, nil)——這是真的 pgx.ErrNoRows，不是故障
	sunsetITPut(t, pool, WearableSunsetStateKey, "")
	InvalidateCache()
	if v, found, err := LookupString(ctx, pool, WearableSunsetStateKey); err != nil || found || v != "" {
		t.Fatalf("a missing row must be (\"\", false, nil): got (%q, %v, %v)", v, found, err)
	}
	// 有值（含前後空白）：trim 後回傳。上一步把「查無」快取起來了，所以要先清快取（後台 Set 成功後也是這樣做）。
	sunsetITPut(t, pool, WearableSunsetStateKey, "  announce ")
	InvalidateCache()
	if v, found, err := LookupString(ctx, pool, WearableSunsetStateKey); err != nil || !found || v != "announce" {
		t.Fatalf("a stored value must come back trimmed: got (%q, %v, %v)", v, found, err)
	}
	// GetString 讀同一份快取、行為不變
	if got := GetString(ctx, pool, WearableSunsetStateKey, "dflt"); got != "announce" {
		t.Fatalf("GetString = %q, want announce", got)
	}

	// 故障：被關掉的連線池。回 err、不快取；之後用健康的連線池讀同一個 key 拿得到真值（錯誤沒有被固定成「查無」）
	broken, err := pgxpool.New(ctx, os.Getenv(testdb.EnvDSN))
	if err != nil {
		t.Fatal(err)
	}
	broken.Close()
	sunsetITPut(t, pool, WearableSunsetDateKey, "2026-11-15")
	InvalidateCache()
	if v, found, err := LookupString(ctx, broken, WearableSunsetDateKey); err == nil || found || v != "" {
		t.Fatalf("a closed pool must be an error: got (%q, %v, %v)", v, found, err)
	}
	if got := GetString(ctx, broken, WearableSunsetDateKey, "dflt"); got != "dflt" {
		t.Fatalf("GetString on a failed read must keep returning the default, got %q", got)
	}
	if v, found, err := LookupString(ctx, pool, WearableSunsetDateKey); err != nil || !found || v != "2026-11-15" {
		t.Fatalf("after a failed read the next read on a healthy pool must see the real value (errors are never cached): got (%q, %v, %v)", v, found, err)
	}
}
