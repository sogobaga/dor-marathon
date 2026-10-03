package integration

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestParseGarminPush_Basics(t *testing.T) {
	raw := `{
		"unknownTop": {"nested": [1, {"a": "b"}, "x"]},
		"activities": [
			{"userId":"u1","summaryId":"s1","activityType":"RUNNING","startTimeInSeconds":1790000000,"durationInSeconds":"1800","distanceInMeters":"5000.5",
			 "manual":"true","isWebUpload":false,"isParent":1,"userAccessToken":"drop-me","startingLatitudeInDegree":25.1,"deviceName":"Fenix 7"},
			{"userId":12345,"summaryId":9876543210123456789,"activityId":"a-1","activityType":"WALKING","startTimeInSeconds":1790000100.0}
		],
		"deregistrations": [{"userId":"should-not-be-read"}]
	}`
	res, err := parseGarminPush(strings.NewReader(raw), garminPushActivities)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Activities) != 2 || len(res.Deregs) != 0 {
		t.Fatalf("activities=%d deregs=%d, want 2/0 (only the requested kind is read)", len(res.Activities), len(res.Deregs))
	}
	a, b := res.Activities[0], res.Activities[1]
	if string(a.UserID) != "u1" || !a.Duration.OK || a.Duration.V != 1800 || a.Distance.V != 5000.5 {
		t.Errorf("string numbers should parse: %+v", a)
	}
	if !bool(a.Manual) || bool(a.IsWebUpload) || !bool(a.IsParent) {
		t.Errorf("flags: manual=%v web=%v parent=%v", a.Manual, a.IsWebUpload, a.IsParent)
	}
	if string(b.UserID) != "12345" || string(b.SummaryID) != "9876543210123456789" {
		t.Errorf("numeric ids must keep their literal text (no float rounding): %q %q", b.UserID, b.SummaryID)
	}
}

func TestParseGarminPush_ErrorClassification(t *testing.T) {
	full := `{"activities":[{"userId":"u1","summaryId":"s1"},{"userId":"u2","summaryId":"s2"}]}`
	for cut := 1; cut < len(full); cut++ {
		_, err := parseGarminPush(strings.NewReader(full[:cut]), garminPushActivities)
		if err == nil {
			t.Fatalf("prefix of %d bytes parsed without error", cut)
		}
		if errors.Is(err, errGarminBadJSON) {
			t.Fatalf("prefix of %d bytes classified as bad json (would be acked 200 and lost): %v", cut, err)
		}
		if !errors.Is(err, errGarminTruncated) {
			t.Fatalf("prefix of %d bytes: err=%v, want truncated", cut, err)
		}
	}
	for name, raw := range map[string]string{
		"garbage":      `not json`,
		"array root":   `[]`,
		"bad inner":    `{"activities":[{"userId":}]}`,
		"unquoted key": `{activities:[]}`,
	} {
		_, err := parseGarminPush(strings.NewReader(raw), garminPushActivities)
		if !errors.Is(err, errGarminBadJSON) {
			t.Errorf("%s: err=%v, want bad json", name, err)
		}
	}
	// 讀取層錯誤（連線中斷）→ truncated
	_, err := parseGarminPush(io.MultiReader(strings.NewReader(`{"activities":[{"userId":"u1"}`), errReader{}), garminPushActivities)
	if !errors.Is(err, errGarminTruncated) {
		t.Errorf("read error: err=%v, want truncated", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset by peer") }

func TestParseGarminPush_MalformedElementsDropped(t *testing.T) {
	res, err := parseGarminPush(strings.NewReader(`{"activities":["str", 5, [1], null, {"userId":"u1","summaryId":"s1","activityType":"RUNNING","startTimeInSeconds":1790000000}]}`), garminPushActivities)
	if err != nil {
		t.Fatal(err)
	}
	if res.Malformed != 3 {
		t.Errorf("malformed = %d, want 3", res.Malformed)
	}
	// null 元素變成零值（之後因缺 userId 被丟棄），另一筆正常
	var st garminBuildStats
	evs := buildGarminActivityEvents(res.Activities, &st)
	if len(evs) != 1 || st.BadPayload != 1 {
		t.Errorf("events=%d badPayload=%d, want 1/1", len(evs), st.BadPayload)
	}
}

func TestParseGarminPush_DeregAndPermission(t *testing.T) {
	res, err := parseGarminPush(strings.NewReader(`{"deregistrations":[{"userId":"u1"},{"userId":"u2"}]}`), garminPushDeregs)
	if err != nil || len(res.Deregs) != 2 {
		t.Fatalf("deregs: %v %+v", err, res)
	}
	res, err = parseGarminPush(strings.NewReader(`{"userPermissionsChange":[{"userId":"u1","summaryId":"x","permissions":["ACTIVITY_EXPORT","bad perm!","HEALTH_EXPORT"],"changeTimeInSeconds":1790000000}]}`), garminPushPerms)
	if err != nil || len(res.Perms) != 1 {
		t.Fatalf("perms: %v %+v", err, res)
	}
	var st garminBuildStats
	evs := buildGarminPermissionEvents(res.Perms, &st)
	if len(evs) != 1 {
		t.Fatalf("events=%d", len(evs))
	}
	var sp garminStoredPermission
	if err := json.Unmarshal(evs[0].Payload, &sp); err != nil {
		t.Fatal(err)
	}
	if len(sp.Permissions) != 2 || sp.Permissions[0] != "ACTIVITY_EXPORT" || sp.ChangeTime != 1790000000 {
		t.Errorf("stored permission payload: %+v (invalid permission strings must be dropped)", sp)
	}
}

func TestGarminDedupeKeyLengthAndUserScoping(t *testing.T) {
	long := strings.Repeat("9", 300)
	k := garminDedupeKey("act", tUser1, long)
	if len(k) > garminDedupeKeyMax {
		t.Fatalf("dedupe key too long: %d", len(k))
	}
	if k2 := garminDedupeKey("act", tUser1, long); k2 != k {
		t.Fatal("dedupe key must be deterministic")
	}
	if garminDedupeKey("act", tUser1, "S-1") == garminDedupeKey("act", tUser2, "S-1") {
		t.Fatal("the same summaryId for two users must not collide (dedupe poisoning)")
	}
	if garminDedupeKey("act", tUser1, "S-1") != "act:"+tUser1+":S-1" {
		t.Fatal("short keys keep the readable form")
	}
	// 64 字元 userId＋長 id 仍不超過 160
	uid64 := strings.Repeat("u", 64)
	if k := garminDedupeKey("perm", uid64, long); len(k) > garminDedupeKeyMax {
		t.Fatalf("len=%d", len(k))
	}
}

func TestBuildActivityEvents_Validation(t *testing.T) {
	good := garminActivityIn{UserID: "u1", SummaryID: "s1", ActivityType: "RUNNING", StartTime: garminNum{V: 1790000000, OK: true}}
	cases := map[string]struct {
		mut  func(a *garminActivityIn)
		want int // 1＝保留
	}{
		"ok":                {func(a *garminActivityIn) {}, 1},
		"no user":           {func(a *garminActivityIn) { a.UserID = "" }, 0},
		"user too long":     {func(a *garminActivityIn) { a.UserID = garminStr(strings.Repeat("x", 65)) }, 0},
		"no ids":            {func(a *garminActivityIn) { a.SummaryID = ""; a.ActivityID = "" }, 0},
		"activityId only":   {func(a *garminActivityIn) { a.SummaryID = ""; a.ActivityID = "A9" }, 1},
		"no start":          {func(a *garminActivityIn) { a.StartTime = garminNum{} }, 0},
		"start in ms":       {func(a *garminActivityIn) { a.StartTime = garminNum{V: 1.79e12, OK: true} }, 0},
		"negative start":    {func(a *garminActivityIn) { a.StartTime = garminNum{V: -5, OK: true} }, 0},
		"cycling":           {func(a *garminActivityIn) { a.ActivityType = "CYCLING" }, 0},
		"callback (ping)":   {func(a *garminActivityIn) { a.CallbackURL = "https://apis.garmin.com/x" }, 0},
		"lowercase running": {func(a *garminActivityIn) { a.ActivityType = "running" }, 1},
	}
	for name, c := range cases {
		a := good
		c.mut(&a)
		var st garminBuildStats
		got := len(buildGarminActivityEvents([]garminActivityIn{a}, &st))
		if got != c.want {
			t.Errorf("%s: kept %d, want %d (stats %+v)", name, got, c.want, st)
		}
	}
}

func TestGarminActivityKind(t *testing.T) {
	for in, want := range map[string]string{
		"RUNNING": "run", "running": "run", " Trail_Running ": "run", "VIRTUAL_RUN": "run", "ULTRA_RUN": "run",
		"WALKING": "walk", "HIKING": "walk", "RUCKING": "walk", "SPEED_WALKING": "walk",
		"CYCLING": "", "WHEELCHAIR_PUSH_RUN": "", "MULTI_SPORT": "", "INDOOR_WALKING": "", "": "",
	} {
		if got := garminActivityKind(in); got != want {
			t.Errorf("garminActivityKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGarminConfigFromEnv(t *testing.T) {
	for _, k := range []string{"GARMIN_CLIENT_ID", "GARMIN_CLIENT_SECRET", "GARMIN_REDIRECT_URI", "GARMIN_TOKEN_AUTH", "GARMIN_AUTH_BASE",
		"GARMIN_TOKEN_URL", "GARMIN_API_BASE", "GARMIN_WEBHOOK_TOKEN", "GARMIN_WEBHOOK_TOKEN_PREV", "GARMIN_WEBHOOK_MAX_BYTES", "GARMIN_CONSENT_VERSIONS"} {
		t.Setenv(k, "")
	}
	c := GarminConfigFromEnv()
	if c.RedirectURI != "https://www.dor.tw/api/v1/integrations/garmin/callback" || c.TokenAuth != "basic" ||
		c.AuthBase != "https://connect.garmin.com" || c.TokenURL != "https://diauth.garmin.com/di-oauth2-service/oauth/token" ||
		c.APIBase != "https://apis.garmin.com" || c.WebhookMaxBytes != 16<<20 || len(c.ConsentVersions) != 1 || c.ConsentVersions[0] != "v1" {
		t.Fatalf("defaults wrong: %+v", c)
	}
	t.Setenv("GARMIN_TOKEN_AUTH", "BODY")
	t.Setenv("GARMIN_WEBHOOK_MAX_BYTES", "1048576") // 低於 10 MiB：拉回下限，避免審核失敗
	t.Setenv("GARMIN_CONSENT_VERSIONS", " v2 , v3 ,,")
	t.Setenv("GARMIN_WEBHOOK_TOKEN", "  abc  ")
	c = GarminConfigFromEnv()
	if c.TokenAuth != "body" || c.WebhookMaxBytes != 10<<20 || len(c.ConsentVersions) != 2 || c.WebhookToken != "abc" {
		t.Fatalf("env parse wrong: %+v", c)
	}
	t.Setenv("GARMIN_TOKEN_AUTH", "weird")
	if c = GarminConfigFromEnv(); c.TokenAuth != "basic" {
		t.Fatalf("unknown token auth mode must fall back to basic, got %q", c.TokenAuth)
	}
}

func TestGarminTokenOK(t *testing.T) {
	tg := newTestGarmin(t, nil)
	for tok, want := range map[string]bool{
		testGarminToken: true, testGarminTokenPrev: true, "": false, "x": false,
		testGarminToken + "x": false, strings.ToUpper(testGarminToken): false,
	} {
		if got := tg.h.tokenOK(tok); got != want {
			t.Errorf("tokenOK(%.6q) = %v, want %v", tok, got, want)
		}
	}
	// 沒有 PREV 時，空 PREV 的雜湊不能讓空字串通過
	tg2 := newTestGarmin(t, func(c *GarminConfig) { c.WebhookTokenPrev = "" })
	if tg2.h.tokenOK("") || tg2.h.tokenOK(testGarminTokenPrev) {
		t.Error("with no PREV configured, neither empty nor old token may pass")
	}
}

// 控制字元（含 NUL）與非法 UTF-8 必須在解析時就被去掉：PostgreSQL 的 TEXT／jsonb 不接受 NUL，
// 一個壞字元會讓整批 INSERT 失敗而被 503 無限重送（毒訊息）。
func TestGarminStr_StripsControlCharsAndInvalidUTF8(t *testing.T) {
	raw := "{\"activities\":[{\"userId\":\"u\\u0000x\\u0007y\",\"summaryId\":\" s\\n1 \",\"activityType\":\"RUNNING\",\"startTimeInSeconds\":1790000000,\"deviceName\":\"Fore\\u0000runner\xff\"}]}"
	res, err := parseGarminPush(strings.NewReader(raw), garminPushActivities)
	if err != nil || len(res.Activities) != 1 {
		t.Fatalf("parse: %v %+v", err, res)
	}
	a := res.Activities[0]
	if string(a.UserID) != "uxy" || string(a.SummaryID) != "s1" || string(a.DeviceName) != "Forerunner" {
		t.Fatalf("control characters / invalid utf-8 must be stripped: %q %q %q", a.UserID, a.SummaryID, a.DeviceName)
	}
	var st garminBuildStats
	evs := buildGarminActivityEvents(res.Activities, &st)
	if len(evs) != 1 || strings.ContainsRune(string(evs[0].Payload), 0) || strings.Contains(string(evs[0].Payload), "\\u0000") {
		t.Fatalf("payload must be safe for jsonb: %q", evs[0].Payload)
	}
}
