package appsettings

// 後台 PUT /admin/app-settings/{key} 的請求內容檢查（Set）：body 解析失敗、缺 value、value 是 null／非字串，一律 400，
// 而且絕不會走到資料庫寫入。舊版吞掉解碼錯誤並把缺漏的 value 當成 ""，一個壞掉的 body 或打錯字的欄位名稱（例如
// {"valu":"announce"}）就會靜默把設定清成空字串——對 wearable_sunset_state 來說就是公告悄悄關閉。
//
// 不連 DB：Handler 的 db 刻意是 nil，通過所有檢查、走到寫入那一步就會 nil pointer panic，測試把 panic 當成「已到達寫入」。
//
// 前端呼叫點盤點（apps/web，2026-10-03 逐一確認）：所有 PUT /admin/app-settings/{key} 都經 lib/api.ts adminAppSettingsApi.set
// → body 恆為 JSON.stringify({ value })，value 型別是 string（可以是空字串）：
//   - admin/system/page.tsx：cancellation_policy（JSON 字串）、通用 save(spec)（number→String、text→raw 可為空字串、
//     select→raw || spec.def 恆非空）、favicon_url（上傳的網址／清除時送 ''）；
//   - admin/interstitial/page.tsx：interstitial_enabled（'1'／'0'）。
//
// 其餘後台頁（rpg、monopoly…）走各自的專屬端點，不經過這支。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// putBody 經 AdminRouter 對 key 發 PUT。reachedWrite=true 代表請求通過了所有檢查、走到了資料庫寫入（nil pool 的 panic 被攔下）。
func putBody(h *Handler, key, body string) (code int, resp string, reachedWrite bool) {
	defer func() {
		if r := recover(); r != nil {
			code, resp, reachedWrite = 0, "", true
		}
	}()
	req := httptest.NewRequest(http.MethodPut, "/"+key, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.AdminRouter().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String(), false
}

func newBodyTestHandler() *Handler {
	// 跨鍵檢查用的假讀取（wearable_sunset_* 兩個鍵才會用到）：兩個鍵都沒有、時間固定在公告結束日之前。
	f := &fakeSettings{vals: map[string]string{}}
	return &Handler{readFresh: f.read, now: func() time.Time { return time.Date(2026, 10, 3, 12, 0, 0, 0, taipei) }}
}

func TestSetRejectsBodiesWithoutAStringValue(t *testing.T) {
	h := newBodyTestHandler()
	bad := map[string]string{
		"empty body":                     ``,
		"whitespace only":                "  \n ",
		"not JSON":                       `value=announce`,
		"truncated JSON":                 `{"value":`,
		"empty object (value missing)":   `{}`,
		"typo in the field name":         `{"valu":"announce"}`,
		"wrong field name":               `{"val":"announce"}`,
		"value is null":                  `{"value":null}`,
		"value is a number":              `{"value":5}`,
		"value is a boolean":             `{"value":true}`,
		"value is an array":              `{"value":["announce"]}`,
		"value is an object":             `{"value":{"state":"announce"}}`,
		"top-level array":                `["announce"]`,
		"top-level string":               `"announce"`,
		"top-level null":                 `null`,
		"top-level number":               `5`,
		"value is a string but then EOF": `{"value":"announce"`,
	}
	for _, key := range []string{WearableSunsetStateKey, "active_skin", "favicon_url", "vip_trial_days"} {
		for name, body := range bad {
			code, resp, reached := putBody(h, key, body)
			if reached {
				t.Errorf("%s / %s: reached the database write — a malformed body must be rejected before any write", key, name)
				continue
			}
			if code != http.StatusBadRequest {
				t.Errorf("%s / %s: status = %d (%s), want 400", key, name, code, resp)
			}
			if !strings.Contains(resp, "error") {
				t.Errorf("%s / %s: a rejection must carry an error message, got %q", key, name, resp)
			}
		}
	}
}

// 回歸（本次修正的實際傷害）：缺 value 的 body 絕不能被當成空字串寫進去。對 wearable_sunset_state，
// 舊版（Value string＋吞掉解碼錯誤＋驗證器收空字串）會把這支請求存成 ""，也就是公告悄悄關閉。
func TestSetNeverTreatsAMissingValueAsAnEmptyString(t *testing.T) {
	h := newBodyTestHandler()
	for _, body := range []string{`{}`, `{"valu":"announce"}`, `{"value":null}`, `garbage`, ``} {
		if code, resp, reached := putBody(h, WearableSunsetStateKey, body); reached || code != http.StatusBadRequest {
			t.Errorf("PUT wearable_sunset_state %q: code=%d reachedWrite=%v (%s), want 400 and no write", body, code, reached, resp)
		}
	}
}

// 狀態鍵的「寫入」驗證器不收空字串（含只有空白、trim 後為空）：後台下拉選單一律送 off／announce，空值只會來自壞請求。
func TestSetRejectsAnEmptyOrBlankSunsetState(t *testing.T) {
	h := newBodyTestHandler()
	for _, body := range []string{`{"value":""}`, `{"value":"   "}`, `{"value":"\n"}`, `{"value":"\t "}`} {
		code, resp, reached := putBody(h, WearableSunsetStateKey, body)
		if reached || code != http.StatusBadRequest || !strings.Contains(resp, "invalid value") {
			t.Errorf("PUT wearable_sunset_state %s: code=%d reachedWrite=%v (%s), want 400 invalid value and no write", body, code, reached, resp)
		}
	}
}

// 合法的 body 照舊通過（走到寫入）：value 是字串，包含空字串（許多設定用 "" 表示「用程式預設」，後台的清除鈕就是這樣送的）。
func TestSetStillAcceptsStringValuesIncludingTheEmptyString(t *testing.T) {
	h := newBodyTestHandler()
	good := []struct{ key, body string }{
		{"favicon_url", `{"value":""}`},                      // 後台「還原內建 favicon」
		{"favicon_url", `{"value":"/icon-192.png"}`},         // 上傳後的網址
		{"personal_entry_whitelist", `{"value":"   "}`},      // text 型清空（trim 後為空）
		{"personal_entry_whitelist", `{"value":"a@b.c\nx"}`}, // 多行文字
		{"active_skin", `{"value":"warm"}`},
		{"interstitial_enabled", `{"value":"1"}`},
		{"vip_trial_days", `{"value":"7"}`},
		{"favicon_url", `{"value":"/x.png","extra":"ignored"}`}, // 多餘欄位不影響
		{WearableSunsetStateKey, `{"value":"off"}`},
		{WearableSunsetStateKey, `{"value":"announce"}`},
		{WearableSunsetStateKey, `{"value":" announce "}`}, // 與舊版相同：前後空白會被 trim
		{WearableSunsetDateKey, `{"value":""}`},            // 結束日清空＝用預設日期
		{WearableSunsetDateKey, `{"value":"2026-11-15"}`},
	}
	for _, c := range good {
		code, resp, reached := putBody(h, c.key, c.body)
		if !reached {
			t.Errorf("PUT %s %s: code=%d (%s), want the request to pass validation and reach the write", c.key, c.body, code, resp)
		}
	}
}

// 既有的 400 不變：未登記的 key、單鍵驗證器、跨鍵規則。
func TestSetKeepsTheExistingRejections(t *testing.T) {
	h := newBodyTestHandler()
	if code, resp, reached := putBody(h, "no_such_setting", `{"value":"x"}`); reached || code != http.StatusBadRequest || !strings.Contains(resp, "unknown setting") {
		t.Errorf("unknown key: %d %s reached=%v", code, resp, reached)
	}
	if code, resp, reached := putBody(h, "active_skin", `{"value":"nope"}`); reached || code != http.StatusBadRequest || !strings.Contains(resp, "invalid value") {
		t.Errorf("invalid value: %d %s reached=%v", code, resp, reached)
	}
}
