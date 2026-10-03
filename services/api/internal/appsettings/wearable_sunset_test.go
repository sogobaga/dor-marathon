package appsettings

// Terra／Strava 串接結束公告（wearable_sunset_state／wearable_sunset_date）：單鍵驗證器、不公開、
// 以及「announce 時結束日不得早於今天」的跨鍵規則。全部純記憶體，不連 DB。
// （Set 一定會先經過這條跨鍵規則、後台改動會送通知，見 wearable_sunset_set_test.go／wearable_sunset_alert_test.go。）

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 「寫入」驗證器只收 off／announce——空字串不收：後台下拉選單一律送 off／announce（空值會被表單換成預設 off），
// 空字串只會來自壞掉或打錯字的請求，靜默存成空值＝公告悄悄關閉。「讀取」端不受影響（缺鍵、空值、未知值一律視為 off，
// 見 wearablesunset.Parse 的測試）。
func TestWearableSunsetStateSpec(t *testing.T) {
	for _, ok := range []string{"off", "announce", "  announce  ", " off "} { // ValidateValue 與 Set 一樣先 trim
		if known, valid := ValidateValue(WearableSunsetStateKey, ok); !known || !valid {
			t.Errorf("state=%q must be accepted (known=%v valid=%v)", ok, known, valid)
		}
	}
	// 本版只有 off／announce：closed 之類的值還不存在，打錯字或大小寫不對一律擋掉（寧可 400 也不要靜默存成 off）；
	// 空字串（含只有空白，trim 後為空）同樣擋掉。
	for _, bad := range []string{"", "   ", "\n", "closed", "Announce", "ANNOUNCE", "on", "true", "1", "hidden", "whitelist", "vip", "announce,off", "announced"} {
		if known, valid := ValidateValue(WearableSunsetStateKey, bad); !known || valid {
			t.Errorf("state=%q must be rejected (known=%v valid=%v)", bad, known, valid)
		}
	}
}

func TestWearableSunsetDateSpec(t *testing.T) {
	for _, ok := range []string{"", "2026-10-31", "2026-12-01", " 2027-01-05 "} {
		if known, valid := ValidateValue(WearableSunsetDateKey, ok); !known || !valid {
			t.Errorf("date=%q must be accepted (known=%v valid=%v)", ok, known, valid)
		}
	}
	for _, bad := range []string{"2026-10-32", "2026-02-30", "2026/10/31", "20261031", "2026-1-5", "tomorrow", "2026-10-31T00:00:00Z", "10-31", "NaN"} {
		if known, valid := ValidateValue(WearableSunsetDateKey, bad); !known || valid {
			t.Errorf("date=%q must be rejected (known=%v valid=%v)", bad, known, valid)
		}
	}
}

func TestWearableSunsetKeysAreNotPublic(t *testing.T) {
	for _, key := range []string{WearableSunsetStateKey, WearableSunsetDateKey} {
		if publicKeys[key] {
			t.Errorf("%s must never be exposed through /app-settings/public (the frontend reads it from the logged-in /status responses)", key)
		}
	}
	if WearableSunsetStateKey != "wearable_sunset_state" || WearableSunsetDateKey != "wearable_sunset_date" || WearableSunsetDefaultDate != "2026-10-31" {
		t.Errorf("keys/default changed: %q %q %q — the web admin catalog (lib/appSettings.ts) and wearablesunset must change with them",
			WearableSunsetStateKey, WearableSunsetDateKey, WearableSunsetDefaultDate)
	}
}

var taipei = time.FixedZone("Asia/Taipei", 8*3600)

func TestSunsetCombinationOK(t *testing.T) {
	cases := []struct {
		name  string
		state string
		date  string
		now   time.Time
		ok    bool
	}{
		{"off never checks (past date is fine, it only matters while announcing)", "off", "2020-01-01", time.Date(2026, 10, 3, 12, 0, 0, 0, taipei), true},
		{"empty state = off", "", "2020-01-01", time.Date(2026, 10, 3, 12, 0, 0, 0, taipei), true},
		{"unknown state = off", "closed", "2020-01-01", time.Date(2026, 10, 3, 12, 0, 0, 0, taipei), true},
		{"announce, future date", "announce", "2026-10-31", time.Date(2026, 10, 3, 12, 0, 0, 0, taipei), true},
		{"announce, date is today (last service day = today)", "announce", "2026-10-31", time.Date(2026, 10, 31, 0, 0, 0, 0, taipei), true},
		{"announce, date is today at 23:59:59 Taipei", "announce", "2026-10-31", time.Date(2026, 10, 31, 23, 59, 59, 0, taipei), true},
		{"announce, date was yesterday (00:00:00 next day Taipei)", "announce", "2026-10-31", time.Date(2026, 11, 1, 0, 0, 0, 0, taipei), false},
		{"announce, past date", "announce", "2026-10-02", time.Date(2026, 10, 3, 12, 0, 0, 0, taipei), false},
		// 用台北曆日，不是 UTC：UTC 還是 10/31 16:30，台北已經是 11/01 00:30
		{"Taipei day wins over the UTC day", "announce", "2026-10-31", time.Date(2026, 10, 31, 16, 30, 0, 0, time.UTC), false},
		{"UTC 10/30 20:00 is already 10/31 in Taipei", "announce", "2026-10-31", time.Date(2026, 10, 30, 20, 0, 0, 0, time.UTC), true},
		{"announce with empty date = default 2026-10-31 (still ahead)", "announce", "", time.Date(2026, 10, 3, 12, 0, 0, 0, taipei), true},
		{"announce with empty date after the default end date", "announce", "", time.Date(2026, 11, 2, 12, 0, 0, 0, taipei), false},
		{"announce with garbage date is rejected", "announce", "soon", time.Date(2026, 10, 3, 12, 0, 0, 0, taipei), false},
		{"state is trimmed and case-insensitive here (defensive: DB value may be hand-written)", "  ANNOUNCE ", "2026-10-02", time.Date(2026, 10, 3, 12, 0, 0, 0, taipei), false},
	}
	for _, c := range cases {
		ok, why := sunsetCombinationOK(c.state, c.date, c.now)
		if ok != c.ok {
			t.Errorf("%s: sunsetCombinationOK(%q,%q) = %v (%q), want %v", c.name, c.state, c.date, ok, why, c.ok)
		}
		if !ok && why == "" {
			t.Errorf("%s: a rejection must carry a readable reason", c.name)
		}
		if ok && why != "" {
			t.Errorf("%s: accepted but has a reason %q", c.name, why)
		}
	}
	// 拒絕原因要帶出日期，操作者才知道要改哪個欄位；也不得含 Garmin／跑團等對外禁用字樣
	_, why := sunsetCombinationOK("announce", "2026-10-02", time.Date(2026, 10, 3, 12, 0, 0, 0, taipei))
	if !strings.Contains(why, "2026-10-02") || !strings.Contains(why, "2026-10-03") {
		t.Errorf("reason should name both the configured date and today: %q", why)
	}
}

// fakeSettings 假的「現查 DB」：記下被查了哪些 key。
type fakeSettings struct {
	vals  map[string]string
	err   error
	calls []string
}

func (f *fakeSettings) read(_ context.Context, key string) (string, bool, error) {
	f.calls = append(f.calls, key)
	if f.err != nil {
		return "", false, f.err
	}
	v, ok := f.vals[key]
	return v, ok, nil
}

func newTestHandler(f *fakeSettings, now time.Time) *Handler {
	return &Handler{readFresh: f.read, now: func() time.Time { return now }}
}

func TestValidateWriteSunsetCrossKey(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, taipei)

	type tc struct {
		name     string
		key, val string
		stored   map[string]string
		want     int // 0 = 可寫
		reads    []string
	}
	cases := []tc{
		{"turn announce on, stored date in the future", WearableSunsetStateKey, "announce", map[string]string{WearableSunsetDateKey: "2026-10-31"}, 0, []string{WearableSunsetDateKey}},
		{"turn announce on, no stored date (default 10/31 still ahead)", WearableSunsetStateKey, "announce", map[string]string{}, 0, []string{WearableSunsetDateKey}},
		{"turn announce on, stored date already past", WearableSunsetStateKey, "announce", map[string]string{WearableSunsetDateKey: "2026-10-01"}, http.StatusBadRequest, []string{WearableSunsetDateKey}},
		{"turn announce on, stored date blank (spaces) = default", WearableSunsetStateKey, "announce", map[string]string{WearableSunsetDateKey: "  "}, 0, []string{WearableSunsetDateKey}},
		{"turn it off, past stored date is irrelevant", WearableSunsetStateKey, "off", map[string]string{WearableSunsetDateKey: "2026-10-01"}, 0, []string{WearableSunsetDateKey}},
		// 空字串不能再當成「清除」寫進去：單鍵驗證器在跨鍵檢查之前就擋下（所以完全不讀另一個鍵）
		{"the state cannot be written as an empty string", WearableSunsetStateKey, "", map[string]string{WearableSunsetDateKey: "2026-10-01"}, http.StatusBadRequest, nil},
		{"whitespace-only state is empty after trim and is rejected too", WearableSunsetStateKey, "   ", map[string]string{}, http.StatusBadRequest, nil},
		{"change the date to the future while announcing", WearableSunsetDateKey, "2026-11-15", map[string]string{WearableSunsetStateKey: "announce"}, 0, []string{WearableSunsetStateKey}},
		{"change the date to today while announcing", WearableSunsetDateKey, "2026-10-03", map[string]string{WearableSunsetStateKey: "announce"}, 0, []string{WearableSunsetStateKey}},
		{"change the date to the past while announcing", WearableSunsetDateKey, "2026-10-02", map[string]string{WearableSunsetStateKey: "announce"}, http.StatusBadRequest, []string{WearableSunsetStateKey}},
		{"clear the date while announcing (= default, still ahead)", WearableSunsetDateKey, "", map[string]string{WearableSunsetStateKey: "announce"}, 0, []string{WearableSunsetStateKey}},
		{"past date is fine while state=off", WearableSunsetDateKey, "2026-10-02", map[string]string{WearableSunsetStateKey: "off"}, 0, []string{WearableSunsetStateKey}},
		{"past date is fine while the state key is missing", WearableSunsetDateKey, "2026-10-02", map[string]string{}, 0, []string{WearableSunsetStateKey}},
		{"single-key validator still applies: bad state", WearableSunsetStateKey, "closed", map[string]string{}, http.StatusBadRequest, nil},
		{"single-key validator still applies: bad date format", WearableSunsetDateKey, "2026/10/31", map[string]string{WearableSunsetStateKey: "announce"}, http.StatusBadRequest, nil},
		{"unknown key", "no_such_key", "x", map[string]string{}, http.StatusBadRequest, nil},
		{"unrelated key never reads the other settings", "vip_trial_days", "7", map[string]string{WearableSunsetStateKey: "announce"}, 0, nil},
	}
	for _, c := range cases {
		f := &fakeSettings{vals: c.stored}
		h := newTestHandler(f, now)
		status, msg := h.validateWrite(ctx, c.key, c.val)
		if status != c.want {
			t.Errorf("%s: status = %d (%q), want %d", c.name, status, msg, c.want)
		}
		if status != 0 && msg == "" {
			t.Errorf("%s: a rejection must carry a message", c.name)
		}
		if strings.Join(f.calls, ",") != strings.Join(c.reads, ",") {
			t.Errorf("%s: fresh reads = %v, want %v", c.name, f.calls, c.reads)
		}
	}

	// 現查失敗不得靜默放行：回 500，Set 不會寫入
	f := &fakeSettings{err: errors.New("db down")}
	if status, _ := newTestHandler(f, now).validateWrite(ctx, WearableSunsetStateKey, "announce"); status != http.StatusInternalServerError {
		t.Errorf("a failed fresh read must be a 500, got %d", status)
	}
	// 但不需要跨鍵檢查的寫入完全不受影響（DB 現查壞了也不影響其他設定）
	if status, _ := newTestHandler(f, now).validateWrite(ctx, "vip_trial_days", "7"); status != 0 {
		t.Errorf("unrelated keys must not depend on the fresh read, got %d", status)
	}
}
