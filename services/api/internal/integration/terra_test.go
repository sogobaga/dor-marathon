package integration

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
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
