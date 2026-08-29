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

// TestIsCheerLayoutJSON 涵蓋合法(空字串/完整三key)、缺key、多餘key、壞JSON、非物件、各數值超範圍
// 的情況。對應啦啦隊角色位置校正值（見 internal/profile.normalizeCheerLayout 的儲存端正規化邏輯，
// 兩邊各自獨立實作以避免循環依賴，範圍常數需保持一致：dx/dy -300~300、scale 0.2~4）。
func TestIsCheerLayoutJSON(t *testing.T) {
	const valid = `{"01":{"dx":0,"dy":0,"scale":1},"02":{"dx":12.3,"dy":-45.6,"scale":1.5},"03":{"dx":-300,"dy":300,"scale":4}}`

	cases := []struct {
		name string
		v    string
		want bool
	}{
		{"空字串合法(清空用預設)", "", true},
		{"完整三key合法", valid, true},
		{"壞JSON", `{"01":{"dx":0`, false},
		{"非物件(陣列)", `[1,2,3]`, false},
		{"缺key(只有01/02)", `{"01":{"dx":0,"dy":0,"scale":1},"02":{"dx":0,"dy":0,"scale":1}}`, false},
		{"多餘key(01/02/03/04)", `{"01":{"dx":0,"dy":0,"scale":1},"02":{"dx":0,"dy":0,"scale":1},"03":{"dx":0,"dy":0,"scale":1},"04":{"dx":0,"dy":0,"scale":1}}`, false},
		{"dx超過上限301", `{"01":{"dx":301,"dy":0,"scale":1},"02":{"dx":0,"dy":0,"scale":1},"03":{"dx":0,"dy":0,"scale":1}}`, false},
		{"dy超過下限-301", `{"01":{"dx":0,"dy":-301,"scale":1},"02":{"dx":0,"dy":0,"scale":1},"03":{"dx":0,"dy":0,"scale":1}}`, false},
		{"scale低於下限0.1", `{"01":{"dx":0,"dy":0,"scale":0.1},"02":{"dx":0,"dy":0,"scale":1},"03":{"dx":0,"dy":0,"scale":1}}`, false},
		{"scale高於上限4.1", `{"01":{"dx":0,"dy":0,"scale":4.1},"02":{"dx":0,"dy":0,"scale":1},"03":{"dx":0,"dy":0,"scale":1}}`, false},
		{"scale為0不合法", `{"01":{"dx":0,"dy":0,"scale":0},"02":{"dx":0,"dy":0,"scale":1},"03":{"dx":0,"dy":0,"scale":1}}`, false},
		{"scale為字串型別不合法", `{"01":{"dx":0,"dy":0,"scale":"1"},"02":{"dx":0,"dy":0,"scale":1},"03":{"dx":0,"dy":0,"scale":1}}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isCheerLayoutJSON(c.v); got != c.want {
				t.Errorf("isCheerLayoutJSON(%q) = %v, want %v", c.v, got, c.want)
			}
		})
	}
}
