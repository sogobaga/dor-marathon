package appsettings

import (
	"testing"
	"time"
)

// TestIsSettingsCacheExpired 涵蓋 isSettingsCacheExpired 的三種情況：從未快取過（零值）、
// 未過期（在 TTL 內）、已過期（超過或恰好等於 TTL）。純函式、不依賴 DB，可直接單元測試。
func TestIsSettingsCacheExpired(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	const ttl = 60 * time.Second

	cases := []struct {
		name string
		at   time.Time
		now  time.Time
		want bool
	}{
		{"從未快取過(零值)視為過期", time.Time{}, now, true},
		{"剛快取(0秒前)未過期", now, now, false},
		{"快取29秒未過期", now.Add(-29 * time.Second), now, false},
		{"快取59秒未過期", now.Add(-59 * time.Second), now, false},
		{"快取恰好60秒視為過期", now.Add(-60 * time.Second), now, true},
		{"快取超過60秒過期", now.Add(-90 * time.Second), now, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isSettingsCacheExpired(c.at, c.now, ttl)
			if got != c.want {
				t.Errorf("isSettingsCacheExpired(%v, %v, %v) = %v, want %v", c.at, c.now, ttl, got, c.want)
			}
		})
	}
}

// TestSettingsCacheSetGetInvalidate 驗證快取讀寫/negative cache/invalidate 的基本行為（不碰 DB）。
func TestSettingsCacheSetGetInvalidate(t *testing.T) {
	invalidateSettingsCache() // 確保測試互不干擾
	defer invalidateSettingsCache()

	if _, ok := getCachedSetting("missing_key"); ok {
		t.Fatalf("expected cache miss for key never set")
	}

	setCachedSetting("foo", "bar", true)
	e, ok := getCachedSetting("foo")
	if !ok || !e.found || e.value != "bar" {
		t.Fatalf("expected cached hit foo=bar found=true, got %+v ok=%v", e, ok)
	}

	// negative cache：查無此 key 的結果也應被快取，且與「快取到空字串」的 zero value 有區別。
	setCachedSetting("does_not_exist", "", false)
	e, ok = getCachedSetting("does_not_exist")
	if !ok {
		t.Fatalf("expected negative-cache hit for does_not_exist")
	}
	if e.found {
		t.Fatalf("expected found=false for negative-cached key, got found=true")
	}

	invalidateSettingsCache()
	if _, ok := getCachedSetting("foo"); ok {
		t.Fatalf("expected cache empty after invalidate")
	}
	if _, ok := getCachedSetting("does_not_exist"); ok {
		t.Fatalf("expected negative cache cleared after invalidate")
	}
}
