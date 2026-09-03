package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// terraSampleActivityJSON：手工組出的一筆跑步活動樣本，欄位路徑依官方文件核對
// （https://docs.tryterra.co/reference/health-and-fitness-api/data-models.md）：
// distance_meters（非 distance_metres）、gain_actual_meters（非 gain_actual_metres）——
// 這兩處是 Phase 0 骨架的拼字錯誤，本檔已修正（見 terra.go terraActivity 定義處註解）。
// samples.md 文件頁本身未附完整活動 JSON（只列個別 sample type），故依 data-models.md 證實的
// 欄位路徑手工組出，而非逐字複製文件範例。
const terraSampleActivityJSON = `{
	"metadata": {
		"start_time": "2026-09-01T06:00:00Z",
		"end_time": "2026-09-01T06:30:00Z",
		"summary_id": "terra-sum-001",
		"type": 8,
		"name": "Morning Run"
	},
	"distance_data": {
		"summary": {
			"distance_meters": 5000,
			"elevation": {
				"gain_actual_meters": 42.5
			}
		}
	},
	"active_durations_data": {
		"activity_seconds": 1800
	},
	"heart_rate_data": {
		"summary": {
			"avg_hr_bpm": 152.3
		}
	},
	"movement_data": {
		"avg_pace_minutes_per_kilometer": 6.0
	}
}`

func mustDecodeTerraActivity(t *testing.T, raw string) *terraActivity {
	t.Helper()
	var a terraActivity
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatalf("decode terraActivity: %v", err)
	}
	return &a
}

func TestMapTerraActivity_RealisticSample(t *testing.T) {
	a := mustDecodeTerraActivity(t, terraSampleActivityJSON)
	na, ok := mapTerraActivity("user-1", "garmin", time.Time{}, a)
	if !ok {
		t.Fatalf("mapTerraActivity() ok = false, want true")
	}
	if na.DistanceKm != 5 {
		t.Errorf("DistanceKm = %v, want 5 (distance_meters=5000 must map to 5km, not distance_metres typo)", na.DistanceKm)
	}
	if na.DurationS != 1800 {
		t.Errorf("DurationS = %v, want 1800", na.DurationS)
	}
	wantPace := 1800 / 5 // duration/km 自算，不採 movement_data.avg_pace_minutes_per_kilometer
	if na.AvgPaceS != wantPace {
		t.Errorf("AvgPaceS = %v, want %v (duration/km)", na.AvgPaceS, wantPace)
	}
	if na.AvgHR == nil || *na.AvgHR != 152 {
		t.Errorf("AvgHR = %v, want 152 (rounded)", na.AvgHR)
	}
	if na.AscentM == nil || *na.AscentM != 42.5 {
		t.Errorf("AscentM = %v, want 42.5 (gain_actual_meters, not gain_actual_metres typo)", na.AscentM)
	}
	if na.ExternalID != "terra-sum-001" {
		t.Errorf("ExternalID = %q, want summary_id value", na.ExternalID)
	}
	if na.Source != "garmin" {
		t.Errorf("Source = %q, want garmin", na.Source)
	}
}

func TestMapTerraActivity_FallbackExternalID(t *testing.T) {
	raw := `{"metadata":{"start_time":"2026-09-01T06:00:00Z","type":8},
		"distance_data":{"summary":{"distance_meters":3000}},
		"active_durations_data":{"activity_seconds":900}}`
	a := mustDecodeTerraActivity(t, raw)
	na, ok := mapTerraActivity("user-1", "coros", time.Time{}, a)
	if !ok {
		t.Fatalf("mapTerraActivity() ok = false, want true")
	}
	if na.ExternalID == "" {
		t.Fatalf("ExternalID should not be empty when summary_id is missing")
	}
	recordedAt, _ := time.Parse(time.RFC3339, "2026-09-01T06:00:00Z")
	want := "coros:" + strconv.FormatInt(recordedAt.Unix(), 10)
	if na.ExternalID != want {
		t.Errorf("ExternalID = %q, want %q", na.ExternalID, want)
	}
	if na.AscentM != nil {
		t.Errorf("AscentM = %v, want nil when elevation missing/zero", na.AscentM)
	}
	if na.AvgHR != nil {
		t.Errorf("AvgHR = %v, want nil when heart rate missing", na.AvgHR)
	}
}

func TestMapTerraActivity_NonRunningTypeSkipped(t *testing.T) {
	raw := `{"metadata":{"start_time":"2026-09-01T06:00:00Z","type":9,"summary_id":"x"},
		"distance_data":{"summary":{"distance_meters":10000}},
		"active_durations_data":{"activity_seconds":1800}}`
	a := mustDecodeTerraActivity(t, raw)
	if _, ok := mapTerraActivity("u", "garmin", time.Time{}, a); ok {
		t.Fatalf("mapTerraActivity() ok = true, want false (type 9 = cycling, not running)")
	}
}

func TestMapTerraActivity_MissingDistanceOrDuration(t *testing.T) {
	cases := []string{
		`{"metadata":{"start_time":"2026-09-01T06:00:00Z","type":8},"distance_data":{"summary":{"distance_meters":0}},"active_durations_data":{"activity_seconds":600}}`,
		`{"metadata":{"start_time":"2026-09-01T06:00:00Z","type":8},"distance_data":{"summary":{"distance_meters":1000}},"active_durations_data":{"activity_seconds":0}}`,
	}
	for _, raw := range cases {
		a := mustDecodeTerraActivity(t, raw)
		if _, ok := mapTerraActivity("u", "garmin", time.Time{}, a); ok {
			t.Fatalf("mapTerraActivity() ok = true, want false for %s", raw)
		}
	}
}

func TestMapTerraActivity_FloorSkipsOldActivity(t *testing.T) {
	a := mustDecodeTerraActivity(t, terraSampleActivityJSON) // start_time 2026-09-01T06:00:00Z
	floor, _ := time.Parse(time.RFC3339, "2026-09-01T12:00:00Z")
	if _, ok := mapTerraActivity("u", "garmin", floor, a); ok {
		t.Fatalf("mapTerraActivity() ok = true, want false (recorded before floor/connected_at)")
	}
	floorBefore, _ := time.Parse(time.RFC3339, "2026-09-01T00:00:00Z")
	if _, ok := mapTerraActivity("u", "garmin", floorBefore, a); !ok {
		t.Fatalf("mapTerraActivity() ok = false, want true (recorded after floor)")
	}
}

func TestIsTerraRunningActivity(t *testing.T) {
	cases := []struct {
		name string
		code int
		want bool
	}{
		{"RUNNING", 8, true},
		{"JOGGING", 56, true},
		{"RUNNING_ON_SAND", 57, true},
		{"TREADMILL_RUNNING", 58, true},
		{"INDOOR_RUNNING", 133, true},
		{"TRAIL_RUNNING", 149, true},
		{"WALKING 不算跑步", 7, false},
		{"HIKING 不算跑步", 35, false},
		{"code 0 不算跑步", 0, false},
		{"騎車不算跑步", 9, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isTerraRunningActivity(c.code); got != c.want {
				t.Errorf("isTerraRunningActivity(%d) = %v, want %v", c.code, got, c.want)
			}
		})
	}
}

func TestProviderToSource(t *testing.T) {
	cases := []struct{ in, want string }{
		{"GARMIN", "garmin"},
		{"garmin", "garmin"},
		{" Garmin ", "garmin"},
		{"COROS", "coros"},
		{"POLAR", "polar"},
		{"SUUNTO", "suunto"},
		{"WAHOO", "wahoo"},
		{"STRAVA", providerStrava},
		{"UNKNOWNBRAND", "unknownbrand"},
	}
	for _, c := range cases {
		if got := providerToSource(c.in); got != c.want {
			t.Errorf("providerToSource(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseTerraProviders(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"預設清單以外自訂", "garmin,coros", []string{"GARMIN", "COROS"}},
		{"大小寫與空白不拘", " Garmin , COROS ,polar", []string{"GARMIN", "COROS", "POLAR"}},
		{"恆濾掉 STRAVA", "garmin,strava,STRAVA,coros", []string{"GARMIN", "COROS"}},
		{"空字串回 nil", "", nil},
		{"只有 strava 回 nil", "strava", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseTerraProviders(c.raw)
			if len(got) != len(c.want) {
				t.Fatalf("ParseTerraProviders(%q) = %v, want %v", c.raw, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("ParseTerraProviders(%q) = %v, want %v", c.raw, got, c.want)
				}
			}
		})
	}
}

func TestTerraScopesUnmarshal(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"陣列格式", `["workout","daily"]`, "workout,daily"},
		{"逗號字串格式", `"workout,daily"`, "workout,daily"},
		{"空陣列", `[]`, ""},
		{"null 容忍為空字串", `null`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s terraScopes
			if err := json.Unmarshal([]byte(c.raw), &s); err != nil {
				t.Fatalf("unmarshal %s: %v", c.raw, err)
			}
			if string(s) != c.want {
				t.Errorf("terraScopes(%s) = %q, want %q", c.raw, string(s), c.want)
			}
		})
	}
}

func TestVerifySignature(t *testing.T) {
	h := &TerraHandler{cfg: TerraConfig{SigningSecret: "test-secret"}}
	body := []byte(`{"type":"activity"}`)
	now := time.Now().Unix()

	valid := signTerraBody(t, "test-secret", now, body)
	if !h.verifySignature(valid, body) {
		t.Errorf("verifySignature() = false for a validly signed, fresh header, want true")
	}

	wrongSecret := signTerraBody(t, "wrong-secret", now, body)
	if h.verifySignature(wrongSecret, body) {
		t.Errorf("verifySignature() = true with wrong secret, want false")
	}

	stale := signTerraBody(t, "test-secret", now-10*60, body) // 10 分鐘前，超過 5 分鐘容忍窗
	if h.verifySignature(stale, body) {
		t.Errorf("verifySignature() = true for a stale (10min old) timestamp, want false")
	}

	future := signTerraBody(t, "test-secret", now+10*60, body) // 10 分鐘後
	if h.verifySignature(future, body) {
		t.Errorf("verifySignature() = true for a future (10min ahead) timestamp, want false")
	}

	if h.verifySignature("garbage-header-no-equals-signs", body) {
		t.Errorf("verifySignature() = true for a malformed header, want false")
	}
	if h.verifySignature("t=,v1=", body) {
		t.Errorf("verifySignature() = true for an empty t/v1 header, want false")
	}

	hDisabled := &TerraHandler{cfg: TerraConfig{SigningSecret: ""}}
	if hDisabled.verifySignature(valid, body) {
		t.Errorf("verifySignature() = true when SigningSecret unset, want false")
	}
}

// signTerraBody 依 terra.go verifySignature 的格式（t=<ts>,v1=<hmac_sha256(ts.body)>）產生一個
// 簽章標頭，供測試組出各種情境（正確/錯誤密鑰/過期時間戳）。獨立算一次 HMAC，不呼叫 verifySignature
// 本身，避免測試與被測程式碼共用同一段簽章邏輯而測不出真正的錯誤。
func signTerraBody(t *testing.T, secret string, ts int64, body []byte) string {
	t.Helper()
	tsStr := strconv.FormatInt(ts, 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(tsStr + "." + string(body)))
	return "t=" + tsStr + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// --- POST /import：fetchTerraActivities ---

func TestFetchTerraActivities_TwoActivitiesDecoded(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if got := r.Header.Get("dev-id"); got != "dev-1" {
			t.Errorf("dev-id header = %q, want dev-1", got)
		}
		if got := r.Header.Get("x-api-key"); got != "key-1" {
			t.Errorf("x-api-key header = %q, want key-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "success",
			"type": "activity",
			"user": {"user_id": "terra-user-1", "provider": "COROS"},
			"data": [` + terraSampleActivityJSON + `, ` + terraSampleActivityJSON + `]
		}`))
	}))
	defer srv.Close()

	h := &TerraHandler{
		cfg: TerraConfig{APIBase: srv.URL, DevID: "dev-1", APIKey: "key-1"},
		hc:  &http.Client{},
	}
	from, _ := time.Parse("2006-01-02", "2026-08-01")
	to, _ := time.Parse("2006-01-02", "2026-08-28")
	data, async, err := h.fetchTerraActivities(context.Background(), "terra-user-1", from, to)
	if err != nil {
		t.Fatalf("fetchTerraActivities() err = %v, want nil", err)
	}
	if async {
		t.Fatalf("fetchTerraActivities() async = true, want false")
	}
	if len(data) != 2 {
		t.Fatalf("fetchTerraActivities() len(data) = %d, want 2", len(data))
	}
	if data[0].Metadata.SummaryID != "terra-sum-001" {
		t.Errorf("data[0].Metadata.SummaryID = %q, want terra-sum-001", data[0].Metadata.SummaryID)
	}
	if !strings.Contains(gotQuery, "start_date=2026-08-01") || !strings.Contains(gotQuery, "end_date=2026-08-28") {
		t.Errorf("query = %q, want start_date=2026-08-01 & end_date=2026-08-28", gotQuery)
	}
	if !strings.Contains(gotQuery, "to_webhook=false") || !strings.Contains(gotQuery, "with_samples=false") {
		t.Errorf("query = %q, want to_webhook=false & with_samples=false", gotQuery)
	}
	if !strings.Contains(gotQuery, "user_id=terra-user-1") {
		t.Errorf("query = %q, want user_id=terra-user-1", gotQuery)
	}
}

func TestFetchTerraActivities_NoDataMeansAsync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","message":"request is being processed and will be sent to your webhook"}`))
	}))
	defer srv.Close()

	h := &TerraHandler{cfg: TerraConfig{APIBase: srv.URL, DevID: "d", APIKey: "k"}, hc: &http.Client{}}
	data, async, err := h.fetchTerraActivities(context.Background(), "terra-user-1", time.Now(), time.Now())
	if err != nil {
		t.Fatalf("fetchTerraActivities() err = %v, want nil", err)
	}
	if !async {
		t.Fatalf("fetchTerraActivities() async = false, want true (no data array in response)")
	}
	if len(data) != 0 {
		t.Fatalf("fetchTerraActivities() len(data) = %d, want 0", len(data))
	}
}

func TestFetchTerraActivities_ServerErrorReturnsErr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	h := &TerraHandler{cfg: TerraConfig{APIBase: srv.URL, DevID: "d", APIKey: "k"}, hc: &http.Client{}}
	_, _, err := h.fetchTerraActivities(context.Background(), "terra-user-1", time.Now(), time.Now())
	if err == nil {
		t.Fatalf("fetchTerraActivities() err = nil, want error on HTTP 500")
	}
}

// --- POST /import：terraSplitWindows ---

func TestTerraSplitWindows(t *testing.T) {
	now, err := time.Parse(time.RFC3339, "2026-09-03T12:00:00Z")
	if err != nil {
		t.Fatalf("parse now: %v", err)
	}
	wantEnd := now.UTC().Truncate(24 * time.Hour)

	cases := []struct {
		name        string
		days        int
		wantWindows int
	}{
		{"28天=1段", 28, 1},
		{"30天=2段", 30, 2},
		{"90天=4段", 90, 4},
		{"1天=1段", 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := terraSplitWindows(now, c.days)
			if len(got) != c.wantWindows {
				t.Fatalf("terraSplitWindows(days=%d) = %d windows, want %d (%+v)", c.days, len(got), c.wantWindows, got)
			}
			// 連續、無縫隙、無重疊：每段 From 恰為前一段 To+1 天。
			for i := 1; i < len(got); i++ {
				wantFrom := got[i-1].To.AddDate(0, 0, 1)
				if !got[i].From.Equal(wantFrom) {
					t.Errorf("window[%d].From = %v, want %v (前一段 To+1 天，無縫隙無重疊)", i, got[i].From, wantFrom)
				}
			}
			// 每段長度 ≤28 天。
			for i, w := range got {
				span := int(w.To.Sub(w.From).Hours()/24) + 1
				if span > terraImportMaxWindowDays {
					t.Errorf("window[%d] span = %d days, want <= %d", i, span, terraImportMaxWindowDays)
				}
				if span <= 0 {
					t.Errorf("window[%d] span = %d days, want > 0", i, span)
				}
			}
			// 最後一段 To == now 的 UTC 日期。
			last := got[len(got)-1]
			if !last.To.Equal(wantEnd) {
				t.Errorf("last window.To = %v, want %v (now's UTC date)", last.To, wantEnd)
			}
			// 總天數涵蓋恰好 days 天（第一段 From 到最後一段 To）。
			first := got[0]
			totalSpan := int(last.To.Sub(first.From).Hours()/24) + 1
			if totalSpan != c.days {
				t.Errorf("total span = %d days, want %d", totalSpan, c.days)
			}
		})
	}
}

// --- POST /import：terraSkipReason ---

func TestTerraSkipReason(t *testing.T) {
	okActivity := mustDecodeTerraActivity(t, terraSampleActivityJSON) // type 8=RUNNING, 2026-09-01T06:00:00Z

	nonRunning := mustDecodeTerraActivity(t, `{"metadata":{"start_time":"2026-09-01T06:00:00Z","type":9,"summary_id":"x"},
		"distance_data":{"summary":{"distance_meters":10000}},"active_durations_data":{"activity_seconds":1800}}`)

	invalidDistance := mustDecodeTerraActivity(t, `{"metadata":{"start_time":"2026-09-01T06:00:00Z","type":8},
		"distance_data":{"summary":{"distance_meters":0}},"active_durations_data":{"activity_seconds":600}}`)

	invalidStartTime := mustDecodeTerraActivity(t, `{"metadata":{"start_time":"not-a-time","type":8},
		"distance_data":{"summary":{"distance_meters":1000}},"active_durations_data":{"activity_seconds":600}}`)

	cases := []struct {
		name  string
		floor time.Time
		a     *terraActivity
		want  string
	}{
		{"非跑步類型", time.Time{}, nonRunning, "non_running"},
		{"距離無效", time.Time{}, invalidDistance, "invalid"},
		{"開始時間無法解析", time.Time{}, invalidStartTime, "invalid"},
		{"早於連接時間", mustParseRFC3339(t, "2026-09-01T12:00:00Z"), okActivity, "before_connect"},
		{"通過（floor為零值）", time.Time{}, okActivity, ""},
		{"通過（floor早於活動時間）", mustParseRFC3339(t, "2026-09-01T00:00:00Z"), okActivity, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := terraSkipReason(c.floor, c.a); got != c.want {
				t.Errorf("terraSkipReason() = %q, want %q", got, c.want)
			}
		})
	}
}

func mustParseRFC3339(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tm
}
