//go:build integration

// 需要真實 Postgres（app_settings 表）；獨立 build tag：
//
//	DOR_TEST_DATABASE_URL=postgres://…?sslmode=disable go test -tags=integration ./internal/integration/wearablesunset/
//
// 驗證 FromSettings 真的從 app_settings 讀、缺鍵＝off、並且走 60 秒進程內快取（只在請求時讀、後台寫入後立即清快取）。
//
// 會寫入並刪除全域的 wearable_sunset_* 兩個 app_settings 列（integration 與 appsettings 套件的整合測試也會寫同樣的列）。
// 連線一律經 internal/testdb.OpenSunsetSettings：只接受連到本機的資料庫、用 Postgres advisory lock 讓三個套件的測試
// 一次只跑一個（不必加 -p 1）、結束時把兩個列還原成測試前的樣子。
package wearablesunset

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/testdb"
)

func itPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.OpenSunsetSettings(t, appsettings.WearableSunsetStateKey, appsettings.WearableSunsetDateKey)
}

func itPut(t *testing.T, pool *pgxpool.Pool, key, val string, invalidate bool) {
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
	if invalidate {
		appsettings.InvalidateCache()
	}
}

func TestSunsetIT_FromSettings(t *testing.T) {
	pool := itPool(t)
	ctx := context.Background()
	t.Cleanup(func() {
		itPut(t, pool, appsettings.WearableSunsetStateKey, "", true)
		itPut(t, pool, appsettings.WearableSunsetDateKey, "", true)
	})
	src := FromSettings(pool)

	for _, c := range []struct {
		name        string
		state, date string
		want        Info
	}{
		{"both keys missing = off", "", "", Info{State: StateOff}},
		{"state off ignores the date", "off", "2026-11-15", Info{State: StateOff}},
		{"announce without a date = default end date", "announce", "", Info{State: StateAnnounce, Date: "2026-10-31"}},
		{"announce with a date", "announce", "2026-11-15", Info{State: StateAnnounce, Date: "2026-11-15"}},
		{"announce with a garbage date falls back to the default", "announce", "soon", Info{State: StateAnnounce, Date: "2026-10-31"}},
		{"unknown state (hand-written SQL) = off", "closed", "2026-11-15", Info{State: StateOff}},
		{"hand-written upper case / spaces still announce", " Announce ", "2026-11-15", Info{State: StateAnnounce, Date: "2026-11-15"}},
	} {
		itPut(t, pool, appsettings.WearableSunsetStateKey, c.state, false)
		itPut(t, pool, appsettings.WearableSunsetDateKey, c.date, true)
		if got := src.Current(ctx); got != c.want {
			t.Errorf("%s: Current() = %+v, want %+v", c.name, got, c.want)
		}
	}

	// 進程內快取：直接改資料庫（不清快取）讀到的還是舊值；清快取（後台 Set 成功後會做）後立刻看到新值。
	itPut(t, pool, appsettings.WearableSunsetStateKey, "announce", false)
	itPut(t, pool, appsettings.WearableSunsetDateKey, "2026-10-31", true)
	if got := src.Current(ctx); !got.Announcing() {
		t.Fatalf("precondition: %+v", got)
	}
	itPut(t, pool, appsettings.WearableSunsetStateKey, "off", false) // 沒有清快取
	if got := src.Current(ctx); !got.Announcing() {
		t.Fatalf("within the cache window the old value is expected (request-driven 60s cache), got %+v", got)
	}
	appsettings.InvalidateCache()
	if got := src.Current(ctx); got.Announcing() {
		t.Fatalf("after the cache is invalidated the new value must be visible, got %+v", got)
	}
}

// DB 讀取真的失敗時沿用上一次成功讀到的狀態（最多 10 分鐘），缺鍵則是「成功讀到 off」——兩者在真的 Postgres 上也要分得開
// （pgx.ErrNoRows 是缺鍵，其他錯誤才是故障）。「故障」用一條被關掉的連線池模擬：對它的查詢會立刻回錯。
// 被測的 Source 用自己的連線池（itPool 的連線池要留給鎖與結束時的還原）；時鐘用假的，不必真的等 10 分鐘。
func TestSunsetIT_DatabaseErrorReusesTheLastKnownStateForTenMinutes(t *testing.T) {
	pool := itPool(t)
	ctx := context.Background()
	t.Cleanup(func() {
		itPut(t, pool, appsettings.WearableSunsetStateKey, "", true)
		itPut(t, pool, appsettings.WearableSunsetDateKey, "", true)
	})

	newVictim := func() (*settingsSource, *fakeClock, *pgxpool.Pool) {
		victim, err := pgxpool.New(ctx, os.Getenv(testdb.EnvDSN))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(victim.Close) // 重複 Close 是安全的
		src := FromSettings(victim).(*settingsSource)
		clock := &fakeClock{now: t0}
		src.now = clock.Now
		return src, clock, victim
	}
	announce := Info{State: StateAnnounce, Date: "2026-11-15"}

	// ① 成功讀到 announce → 連線池壞掉：10 分鐘內沿用 announce，超過就 off
	itPut(t, pool, appsettings.WearableSunsetStateKey, "announce", false)
	itPut(t, pool, appsettings.WearableSunsetDateKey, "2026-11-15", true)
	src, clock, victim := newVictim()
	if got := src.Current(ctx); got != announce {
		t.Fatalf("precondition: %+v", got)
	}
	victim.Close()
	appsettings.InvalidateCache() // 進程內快取清掉，下一次讀取一定真的去查（而且會失敗）
	clock.set(t0.Add(9 * time.Minute))
	if got := src.Current(ctx); got != announce {
		t.Fatalf("database error 9 minutes after a good read: %+v, want the remembered %+v", got, announce)
	}
	clock.set(t0.Add(10*time.Minute + time.Second))
	if got := src.Current(ctx); got != (Info{State: StateOff}) {
		t.Fatalf("database error 10m01s after a good read: %+v, want off", got)
	}

	// ② 沒有任何成功讀取就故障：off
	src, _, victim = newVictim()
	victim.Close()
	appsettings.InvalidateCache()
	if got := src.Current(ctx); got != (Info{State: StateOff}) {
		t.Fatalf("database error with nothing remembered: %+v, want off", got)
	}

	// ③ 缺鍵（真的 pgx.ErrNoRows）是成功讀到 off，會蓋掉先前記下的 announce：之後故障沿用的是 off
	itPut(t, pool, appsettings.WearableSunsetStateKey, "announce", false)
	itPut(t, pool, appsettings.WearableSunsetDateKey, "2026-11-15", true)
	src, clock, victim = newVictim()
	if got := src.Current(ctx); got != announce {
		t.Fatalf("precondition: %+v", got)
	}
	itPut(t, pool, appsettings.WearableSunsetStateKey, "", false) // 刪除這一列＝缺鍵
	appsettings.InvalidateCache()
	clock.set(t0.Add(time.Minute))
	if got := src.Current(ctx); got != (Info{State: StateOff}) {
		t.Fatalf("a missing key is off: %+v", got)
	}
	victim.Close()
	appsettings.InvalidateCache()
	clock.set(t0.Add(2 * time.Minute))
	if got := src.Current(ctx); got != (Info{State: StateOff}) {
		t.Fatalf("an error after a successful read of a missing key must reuse off, not the earlier announce: %+v", got)
	}
}
