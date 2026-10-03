package appsettings

// LookupString：與 GetString 同一條快取路徑，但把「DB 讀取失敗」和「查無此 key」分開回報
// （internal/integration/wearablesunset 靠它在 DB 故障時沿用上一次成功讀到的狀態，而不是把故障當成缺鍵）。
//
// 不需要資料庫：用一個「已關閉的連線池」模擬讀取失敗（對它發查詢會立刻回 closed pool 錯誤，不連網路；
// pgxpool 本身是延遲連線的，建立時不會連出去）。真資料庫上的行為見 wearable_sunset_integration_test.go（-tags=integration）。

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// closedPool 回傳一個已關閉的連線池：任何查詢都會立刻失敗，不會連到任何地方。
func closedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://nobody:nopass@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("pgxpool.New must not connect eagerly: %v", err)
	}
	pool.Close()
	return pool
}

func TestLookupStringReportsADatabaseErrorSeparatelyFromAMissingKey(t *testing.T) {
	invalidateSettingsCache()
	t.Cleanup(invalidateSettingsCache)
	ctx := context.Background()
	pool := closedPool(t)

	// DB 讀取真的失敗：回 err（不是 pgx.ErrNoRows），value 為空、found=false
	v, found, err := LookupString(ctx, pool, "lookup_test_err")
	if err == nil || found || v != "" {
		t.Fatalf("a failed read must return (\"\", false, err): got (%q, %v, %v)", v, found, err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("a failed read must not look like a missing key: %v", err)
	}
	// 錯誤不得被快取（否則一次故障會被固定成「查無此 key」，要等整輪 TTL 才恢復）
	if _, ok := getCachedSetting("lookup_test_err"); ok {
		t.Fatal("an error must never be cached")
	}
	// 連續兩次都是真的去讀（第二次沒有被第一次的錯誤快取遮住）
	if _, _, err2 := LookupString(ctx, pool, "lookup_test_err"); err2 == nil {
		t.Fatal("the second read must hit the database again and fail again")
	}

	// 查無此 key（negative cache 命中）：err=nil、found=false —— 與上面的故障分得開，而且完全不碰資料庫
	setCachedSetting("lookup_test_missing", "", false)
	if v, found, err := LookupString(ctx, pool, "lookup_test_missing"); err != nil || found || v != "" {
		t.Fatalf("a missing key must be (\"\", false, nil): got (%q, %v, %v)", v, found, err)
	}

	// 有值：回 trim 後的原始值
	setCachedSetting("lookup_test_value", "  announce \n", true)
	if v, found, err := LookupString(ctx, pool, "lookup_test_value"); err != nil || !found || v != "announce" {
		t.Fatalf("a stored value must be returned trimmed: got (%q, %v, %v)", v, found, err)
	}

	// 有這個 key 但值是空字串：found=true、value=""（LookupString 不套任何預設）
	setCachedSetting("lookup_test_empty", "", true)
	if v, found, err := LookupString(ctx, pool, "lookup_test_empty"); err != nil || !found || v != "" {
		t.Fatalf("an empty stored value must be (\"\", true, nil): got (%q, %v, %v)", v, found, err)
	}
}

// 其他呼叫端（GetString／GetInt／GetFloat）的行為完全不變：仍然吞掉錯誤、回預設值，空值回預設。
func TestGettersKeepSwallowingErrorsAndApplyingDefaults(t *testing.T) {
	invalidateSettingsCache()
	t.Cleanup(invalidateSettingsCache)
	ctx := context.Background()
	pool := closedPool(t)

	if got := GetString(ctx, pool, "getter_test_err", "dflt"); got != "dflt" {
		t.Errorf("GetString on a failed read = %q, want the default", got)
	}
	if got := GetInt(ctx, pool, "getter_test_err", 42); got != 42 {
		t.Errorf("GetInt on a failed read = %d, want the default", got)
	}
	if got := GetFloat(ctx, pool, "getter_test_err", 1.5); got != 1.5 {
		t.Errorf("GetFloat on a failed read = %v, want the default", got)
	}

	setCachedSetting("getter_test_empty", "  ", true)
	if got := GetString(ctx, pool, "getter_test_empty", "dflt"); got != "dflt" {
		t.Errorf("GetString on a blank stored value = %q, want the default", got)
	}
	setCachedSetting("getter_test_value", " 7 ", true)
	if got := GetInt(ctx, pool, "getter_test_value", 0); got != 7 {
		t.Errorf("GetInt = %d, want 7", got)
	}
	if got := GetString(ctx, pool, "getter_test_value", "dflt"); got != "7" {
		t.Errorf("GetString = %q, want 7", got)
	}
	setCachedSetting("getter_test_missing", "", false)
	if got := GetString(ctx, pool, "getter_test_missing", "dflt"); got != "dflt" {
		t.Errorf("GetString on a missing key = %q, want the default", got)
	}
}
