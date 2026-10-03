package integration

// COROS MCP 第二階段的純函式／不需 DB 的單元測試（解析、對應、型號、CSRF、金鑰隔離、節流、分段）。
// 需要 Postgres 的匯入／去重／EXP／中斷流程見 corosmcp_integration_test.go（build tag integration）。
// 範本一律去除 Location 與起點座標（契約：不保存），只留解析器會讀的欄位。

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// corosFixtureRec 組一筆 querySportRecords 文字分段（格式照 INVENTORY A 節的真實樣本，但不含 Location／座標）。
type corosFixtureRec struct {
	Title     string // 「N. 」後的標題
	Start     int64  // unix 秒
	ElapsedS  int    // end = start + ElapsedS
	DurationS int    // Duration 顯示用（<3600 → mm:ss，否則 h:mm:ss）
	Distance  string // 例 "5.31 km"、"850 m"、"12 sets"
	HR        int    // 0＝整段 Avg HR 省略
	LabelID   string
	Sport     int
}

func corosFixtureDuration(sec int) string {
	if sec >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", sec/3600, (sec%3600)/60, sec%60)
	}
	return fmt.Sprintf("%02d:%02d", sec/60, sec%60)
}

func (r corosFixtureRec) block(n int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d. %s\n", n, r.Title)
	fmt.Fprintf(&sb, "   Time Window: startTimestamp=%d | endTimestamp=%d\n", r.Start, r.Start+int64(r.ElapsedS))
	fmt.Fprintf(&sb, "   Duration: %s | Distance: %s\n", corosFixtureDuration(r.DurationS), r.Distance)
	if r.HR > 0 {
		fmt.Fprintf(&sb, "   Average Pace: 7:38 /km | Avg HR: %d bpm | Calories: 334 kcal\n", r.HR)
	} else {
		sb.WriteString("   Average Pace: 14:07 /km | Calories: 40 kcal\n")
	}
	fmt.Fprintf(&sb, "   LabelId: %s | SportType: %d\n", r.LabelID, r.Sport)
	return sb.String()
}

func corosFixtureText(recs []corosFixtureRec) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Workout Records (%d)\n\n", len(recs))
	for i, r := range recs {
		sb.WriteString(r.block(i + 1))
		sb.WriteString("\n")
	}
	return sb.String()
}

// corosStaticRecs：涵蓋 mm:ss／h:mm:ss、km／m、缺 HR、非距離型、非跑步運動、18 位 labelId。
var corosStaticRecs = []corosFixtureRec{
	{Title: "Outdoor Run — 2026-09-29", Start: 1790630201, ElapsedS: 2433 + 61, DurationS: 2433, Distance: "5.31 km", HR: 133, LabelID: "480669290658299907", Sport: 100},
	{Title: "Treadmill Run — 2026-09-28", Start: 1790540000, ElapsedS: 3735, DurationS: 3735, Distance: "10.00 km", HR: 150, LabelID: "480669290658299908", Sport: 101},
	{Title: "Walk — 2026-09-27", Start: 1790450000, ElapsedS: 800, DurationS: 720, Distance: "850 m", HR: 0, LabelID: "480669290658299909", Sport: 900},
	{Title: "Strength — 2026-09-26", Start: 1790360000, ElapsedS: 2700, DurationS: 2700, Distance: "12 sets", HR: 110, LabelID: "480669290658299910", Sport: 402},
	{Title: "Road Bike — 2026-09-25", Start: 1790270000, ElapsedS: 3600, DurationS: 3600, Distance: "20.00 km", HR: 120, LabelID: "480669290658299911", Sport: 200},
}

func corosQuoted(text string) string {
	b, _ := json.Marshal(text)
	return string(b)
}

func TestParseCorosMcpSportRecords_Fixture(t *testing.T) {
	recs := parseCorosMcpSportRecords(corosFixtureText(corosStaticRecs))
	if len(recs) != 5 {
		t.Fatalf("expected 5 records, got %d: %+v", len(recs), recs)
	}
	r0 := recs[0]
	if r0.LabelID != "480669290658299907" || r0.SportType != 100 || r0.StartUnix != 1790630201 || r0.EndUnix != 1790630201+2433+61 {
		t.Fatalf("unexpected r0: %+v", r0)
	}
	if r0.DurationS != 2433 || r0.DistanceKm != 5.31 || r0.AvgHR == nil || *r0.AvgHR != 133 {
		t.Fatalf("unexpected r0 metrics: %+v", r0)
	}
	if !strings.Contains(r0.Title, "Outdoor Run") {
		t.Fatalf("title should be kept for debugging, got %q", r0.Title)
	}
	// h:mm:ss + km
	if recs[1].DurationS != 3735 || recs[1].DistanceKm != 10.0 || recs[1].SportType != 101 {
		t.Fatalf("h:mm:ss record wrong: %+v", recs[1])
	}
	// m 單位 + 缺 Avg HR
	if recs[2].DistanceKm != 0.85 || recs[2].AvgHR != nil || recs[2].DurationS != 720 {
		t.Fatalf("meter/no-HR record wrong: %+v", recs[2])
	}
	// 非距離單位（sets）→ distance 0（之後歸 invalid／non_running）
	if recs[3].DistanceKm != 0 || recs[3].SportType != 402 {
		t.Fatalf("sets record wrong: %+v", recs[3])
	}
	if recs[4].SportType != 200 || recs[4].DistanceKm != 20.0 {
		t.Fatalf("cycling record wrong: %+v", recs[4])
	}
}

func TestParseCorosMcpSportRecords_LabelIDKeptExact18Digits(t *testing.T) {
	recs := parseCorosMcpSportRecords(corosFixtureText(corosStaticRecs[:1]))
	if len(recs) != 1 || recs[0].LabelID != "480669290658299907" {
		t.Fatalf("18-digit labelId must be kept verbatim (no float rounding), got %+v", recs)
	}
	if len(recs[0].LabelID) != 18 {
		t.Fatalf("labelId length changed: %q", recs[0].LabelID)
	}
}

func TestParseCorosMcpSportRecords_QuotedWrapper(t *testing.T) {
	text := corosFixtureText(corosStaticRecs[:2])
	recs := parseCorosMcpSportRecords(corosQuoted(text)) // 外層是「JSON 字串字面值」
	if len(recs) != 2 {
		t.Fatalf("quoted wrapper should be unwrapped, got %d records", len(recs))
	}
}

func TestParseCorosMcpSportRecords_IgnoresLocationAndCoordinates(t *testing.T) {
	// 真實回應含 Location 與起點座標：解析器必須照樣解析，且結果型別根本沒有可放這兩項的欄位。
	text := "Workout Records (1)\n\n1. Outdoor Run — 2026-09-29\n   Location: Test City, Test District\n" +
		"   Start Coordinates: 12.345678, 98.765432\n   Time Window: startTimestamp=1790630201 | endTimestamp=1790632634\n" +
		"   Duration: 40:33 | Distance: 5.31 km\n   Average Pace: 7:38 /km | Avg HR: 133 bpm | Calories: 334 kcal\n" +
		"   LabelId: 480669290658299907 | SportType: 100\n"
	recs := parseCorosMcpSportRecords(text)
	if len(recs) != 1 || recs[0].DistanceKm != 5.31 {
		t.Fatalf("records with location lines should still parse, got %+v", recs)
	}
	dump := fmt.Sprintf("%+v", recs[0])
	for _, bad := range []string{"Test City", "12.345678", "98.765432"} {
		if strings.Contains(dump, bad) {
			t.Fatalf("parsed record must not retain location/coordinates, found %q in %s", bad, dump)
		}
	}
}

func TestParseCorosMcpSportRecords_EmptyAndAnomaly(t *testing.T) {
	if got := parseCorosMcpSportRecords("No workout records found."); len(got) != 0 {
		t.Fatalf("no records → empty, got %+v", got)
	}
	if got := parseCorosMcpSportRecords(""); len(got) != 0 {
		t.Fatalf("empty text → empty, got %+v", got)
	}
	anomaly := "Tool call anomalies detected. High risk of session context pollution or request exceeds the LLM capability boundary."
	if got := parseCorosMcpSportRecords(anomaly); len(got) != 0 {
		t.Fatalf("anomaly text must parse to nothing, got %+v", got)
	}
	if !corosMcpIsAnomaly(corosMcpUnwrapText(corosQuoted(anomaly))) {
		t.Fatal("quoted anomaly text must still be detected as anomaly")
	}
}

func TestParseCorosMcpSportRecords_ExactBoundaryNumberedLinesOnly(t *testing.T) {
	// 標題行「Workout Records (2)」不是分段；LabelId 之外的行裡的數字不能被誤認成編號行。
	text := "Workout Records (2)\n\n1. Outdoor Run — 2026-09-29\n   Duration: 40:33 | Distance: 5.31 km\n   LabelId: 1111 | SportType: 100\n\n" +
		"2. Outdoor Run — 2026-09-30\n   Duration: 30:00 | Distance: 4.00 km\n   LabelId: 2222 | SportType: 100\n"
	recs := parseCorosMcpSportRecords(text)
	if len(recs) != 2 || recs[0].LabelID != "1111" || recs[1].LabelID != "2222" {
		t.Fatalf("expected 2 separate records, got %+v", recs)
	}
	if recs[0].DurationS != 2433 || recs[1].DurationS != 1800 {
		t.Fatalf("durations must not bleed across blocks: %+v", recs)
	}
}

// --- 對應 ---

func TestMapCorosMcpRecord_FullMapping(t *testing.T) {
	recs := parseCorosMcpSportRecords(corosFixtureText(corosStaticRecs))
	dev := "COROS PACE 4"
	na, skip := mapCorosMcpRecord("u1", time.Time{}, &dev, recs[0])
	if skip != corosMcpSkipNone || na == nil {
		t.Fatalf("expected mapped, skip=%q", skip)
	}
	if na.UserID != "u1" || na.Source != "coros" || na.ExternalID != "mcp:480669290658299907" {
		t.Fatalf("identity wrong: %+v", na)
	}
	if na.DistanceKm != 5.31 || na.DurationS != 2433 {
		t.Fatalf("metrics wrong: %+v", na)
	}
	if want := 458; na.AvgPaceS != want { // round(2433/5.31)=458
		t.Fatalf("AvgPaceS = %d, want %d (round(duration/distance), not COROS pace)", na.AvgPaceS, want)
	}
	if na.ElapsedS == nil || *na.ElapsedS != 2433+61 {
		t.Fatalf("ElapsedS = %v, want end-start=%d", na.ElapsedS, 2433+61)
	}
	if na.AvgHR == nil || *na.AvgHR != 133 {
		t.Fatalf("AvgHR wrong: %v", na.AvgHR)
	}
	if !na.RecordedAt.Equal(time.Unix(1790630201, 0)) || na.RecordedAt.Location() != time.UTC {
		t.Fatalf("RecordedAt must be start time in UTC, got %v", na.RecordedAt)
	}
	if want := fingerprintOf(1790630201, 5310, 2433); na.Fingerprint != want {
		t.Fatalf("fingerprint = %q, want %q (fingerprintOf start|meters|sec)", na.Fingerprint, want)
	}
	if na.DeviceName == nil || *na.DeviceName != "COROS PACE 4" {
		t.Fatalf("DeviceName wrong: %v", na.DeviceName)
	}
	if na.AscentM != nil || na.Manual {
		t.Fatalf("unexpected extras: %+v", na)
	}
}

// 運動代碼 → 活動大類：跑步類（100–103）Kind 留空（＝run，門檻 2:30/km）；健行 104、走路 900 → walk（門檻 4:00/km）。
func TestMapCorosMcpRecord_KindFromSportType(t *testing.T) {
	cases := []struct {
		sport int
		want  string
	}{{100, ""}, {101, ""}, {102, ""}, {103, ""}, {104, KindWalk}, {900, KindWalk}}
	for _, c := range cases {
		na, skip := mapCorosMcpRecord("u1", time.Time{}, nil, corosMcpRecord{
			LabelID: "480000000000000001", SportType: c.sport, StartUnix: 1790630201, EndUnix: 1790632700, DurationS: 2400, DistanceKm: 5,
		})
		if skip != corosMcpSkipNone || na == nil || na.Kind != c.want {
			t.Errorf("sport %d: kind=%q skip=%q, want kind=%q", c.sport, func() string {
				if na == nil {
					return "<nil>"
				}
				return na.Kind
			}(), skip, c.want)
		}
	}
	// 同樣 3:00/km 的資料：跑步不算異常、走路算異常（見 plausibility.go）
	run, _ := mapCorosMcpRecord("u1", time.Time{}, nil, corosMcpRecord{LabelID: "480000000000000002", SportType: 100, StartUnix: 1790630201, DurationS: 900, DistanceKm: 5})
	walk, _ := mapCorosMcpRecord("u1", time.Time{}, nil, corosMcpRecord{LabelID: "480000000000000003", SportType: 900, StartUnix: 1790630201, DurationS: 900, DistanceKm: 5})
	now := time.Unix(1790640000, 0)
	if a, _ := CheckPlausible(run, now); a != PlausibleOK {
		t.Fatalf("run at 3:00/km is plausible, got %q", a)
	}
	if a, r := CheckPlausible(walk, now); a != PlausibleFlag || r != FlagImplausiblePace {
		t.Fatalf("walk at 3:00/km is implausible, got %q %q", a, r)
	}
}

func TestMapCorosMcpRecord_SkipReasons(t *testing.T) {
	recs := parseCorosMcpSportRecords(corosFixtureText(corosStaticRecs))
	// 走路 m 單位：收（900 在白名單）、無 HR
	if na, skip := mapCorosMcpRecord("u", time.Time{}, nil, recs[2]); skip != "" || na.AvgHR != nil || na.DistanceKm != 0.85 || na.DeviceName != nil {
		t.Fatalf("walk should map without HR/device, skip=%q na=%+v", skip, na)
	}
	// 重訓、單車 → non_running（即使 distance 有值）
	if _, skip := mapCorosMcpRecord("u", time.Time{}, nil, recs[3]); skip != corosMcpSkipNonRunning {
		t.Fatalf("strength skip = %q", skip)
	}
	if _, skip := mapCorosMcpRecord("u", time.Time{}, nil, recs[4]); skip != corosMcpSkipNonRunning {
		t.Fatalf("cycling skip = %q", skip)
	}
	// 白名單全部放行
	for _, st := range []int{100, 101, 102, 103, 104, 900} {
		rec := corosMcpRecord{LabelID: "9999", SportType: st, StartUnix: 1790630201, DurationS: 600, DistanceKm: 2}
		if _, skip := mapCorosMcpRecord("u", time.Time{}, nil, rec); skip != "" {
			t.Fatalf("sportType %d must be accepted, skip=%q", st, skip)
		}
	}
	// invalid：distance／duration／start／labelId
	bad := []corosMcpRecord{
		{LabelID: "1", SportType: 100, StartUnix: 1790630201, DurationS: 600, DistanceKm: 0},
		{LabelID: "1", SportType: 100, StartUnix: 1790630201, DurationS: 0, DistanceKm: 2},
		{LabelID: "1", SportType: 100, StartUnix: 0, DurationS: 600, DistanceKm: 2},
		{LabelID: "", SportType: 100, StartUnix: 1790630201, DurationS: 600, DistanceKm: 2},
	}
	for i, r := range bad {
		if _, skip := mapCorosMcpRecord("u", time.Time{}, nil, r); skip != corosMcpSkipInvalid {
			t.Fatalf("bad[%d] skip = %q, want invalid", i, skip)
		}
	}
	// 非距離型（sets）的跑步代碼也只會是 invalid（distance 解析成 0）
	rec := parseCorosMcpSportRecords(corosFixtureText([]corosFixtureRec{{Title: "x", Start: 1790630201, ElapsedS: 600, DurationS: 600, Distance: "12 sets", LabelID: "5555", Sport: 100}}))[0]
	if _, skip := mapCorosMcpRecord("u", time.Time{}, nil, rec); skip != corosMcpSkipInvalid {
		t.Fatalf("sets under a running code → invalid, got %q", skip)
	}
}

func TestMapCorosMcpRecord_Floor(t *testing.T) {
	rec := corosMcpRecord{LabelID: "7777", SportType: 100, StartUnix: 1790630201, DurationS: 600, DistanceKm: 2}
	start := time.Unix(rec.StartUnix, 0)
	if _, skip := mapCorosMcpRecord("u", start.Add(time.Second), nil, rec); skip != corosMcpSkipBeforeConnect {
		t.Fatalf("start < floor must be skipped_before_connect, got %q", skip)
	}
	if _, skip := mapCorosMcpRecord("u", start, nil, rec); skip != "" {
		t.Fatalf("start == floor is not before connect, got %q", skip)
	}
	if _, skip := mapCorosMcpRecord("u", start.Add(-time.Hour), nil, rec); skip != "" {
		t.Fatalf("start > floor must import, got %q", skip)
	}
}

func TestMapCorosMcpRecord_ElapsedOnlyWhenPositive(t *testing.T) {
	rec := corosMcpRecord{LabelID: "7777", SportType: 100, StartUnix: 1790630201, EndUnix: 1790630201, DurationS: 600, DistanceKm: 2}
	if na, _ := mapCorosMcpRecord("u", time.Time{}, nil, rec); na.ElapsedS != nil {
		t.Fatalf("end==start → ElapsedS nil, got %v", *na.ElapsedS)
	}
	rec.EndUnix = 0
	if na, _ := mapCorosMcpRecord("u", time.Time{}, nil, rec); na.ElapsedS != nil {
		t.Fatalf("missing end → ElapsedS nil")
	}
}

// --- 型號 ---

func TestParseCorosMcpDeviceName(t *testing.T) {
	const real = "Bound Devices (1)\n========================\n\n1. COROS PACE 4\n   Model Name: COROS R4\n"
	cases := []struct {
		in   string
		want string // 空＝nil
	}{
		{real, "COROS PACE 4"},
		{corosQuoted(real), "COROS PACE 4"},
		{"Bound Devices (2)\n\n1.   COROS APEX 2 Pro  \n   Model Name: X\n\n2. COROS PACE 4\n", "COROS APEX 2 Pro"}, // 取第一支、去空白
		{"Bound Devices (0)\nNo devices.", ""},
		{"", ""},
		{"Tool call anomalies detected. High risk of session context pollution.", ""},
	}
	for i, c := range cases {
		got := parseCorosMcpDeviceName(c.in)
		if c.want == "" {
			if got != nil {
				t.Fatalf("case %d: want nil, got %q", i, *got)
			}
			continue
		}
		if got == nil || *got != c.want {
			t.Fatalf("case %d: want %q, got %v", i, c.want, got)
		}
	}
	long := "Bound Devices (1)\n\n1. " + strings.Repeat("A", 100) + "\n"
	if got := parseCorosMcpDeviceName(long); got == nil || len([]rune(*got)) != 60 {
		t.Fatalf("device name must be truncated to 60 runes, got %v", got)
	}
}

// --- 分段／參數 ---

func TestCorosMcpDayChunks(t *testing.T) {
	to := time.Date(2026, 10, 1, 12, 0, 0, 0, corosMcpTZ)
	from := to.AddDate(0, 0, -30)
	chunks := corosMcpDayChunks(from, to)
	if len(chunks) != 4 { // 31 個日曆日（含頭尾）→ 10+10+10+1
		t.Fatalf("expected 4 chunks for 31 calendar days, got %d: %v", len(chunks), chunks)
	}
	for i, c := range chunks {
		days := int(c[1].Sub(c[0]).Hours()/24) + 1
		if days > corosMcpChunkDays || days < 1 {
			t.Fatalf("chunk %d spans %d days (limit %d)", i, days, corosMcpChunkDays)
		}
		if i > 0 && !c[0].Equal(chunks[i-1][1].AddDate(0, 0, 1)) {
			t.Fatalf("chunks must be contiguous and non-overlapping: %v then %v", chunks[i-1], c)
		}
	}
	if !chunks[len(chunks)-1][1].Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, corosMcpTZ)) {
		t.Fatalf("last chunk must end on `to` date, got %v", chunks[len(chunks)-1][1])
	}
	if got := corosMcpDayChunks(to.AddDate(0, 0, -3), to); len(got) != 1 {
		t.Fatalf("3-day autosync window → 1 chunk, got %d", len(got))
	}
}

func TestCorosMcpSportRecordsRangeArgs(t *testing.T) {
	from := time.Date(2026, 9, 21, 0, 0, 0, 0, corosMcpTZ)
	to := time.Date(2026, 9, 30, 0, 0, 0, 0, corosMcpTZ)
	args := corosMcpSportRecordsRangeArgs(from, to, 50)
	if args["startDate"] != "20260921" || args["endDate"] != "20260930" || args["limit"] != 50 {
		t.Fatalf("unexpected args: %v", args)
	}
	if len(args) != 10 { // schema 要求 10 個欄位全部出現（不用就 null）
		t.Fatalf("expected all 10 keys, got %d: %v", len(args), args)
	}
	if v, ok := args["locationKeyword"]; !ok || v != nil {
		t.Fatalf("locationKeyword must be present and null, got %v (present=%v)", v, ok)
	}
	if fmt.Sprint(args["sportTypeCodes"]) != fmt.Sprint(corosMcpDORSportTypes) {
		t.Fatalf("sportTypeCodes wrong: %v", args["sportTypeCodes"])
	}
}

// --- CSRF（OAuth 登入 CSRF cookie 綁定）---

func newCallbackReq(state string, cookie *http.Cookie, extra string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/callback?state="+url.QueryEscape(state)+extra, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	return req
}

func TestCorosMcpCallback_CSRF(t *testing.T) {
	h := newTestCorosMcpHandler() // repo=nil：CSRF 不符時在碰 DB／網路之前就導回，不會用到
	h.cfg.FrontendURL = "https://www.dor.tw"
	good := corosMcpSignState(h.cfg.JWTSecret, "user-1", "https://mcpus.coros.com", "verifier", "nonce-abc", 10*time.Minute)
	expired := corosMcpSignState(h.cfg.JWTSecret, "user-1", "https://mcpus.coros.com", "verifier", "nonce-abc", -time.Second)

	run := func(name string, req *http.Request, wantReason string) *httptest.ResponseRecorder {
		t.Helper()
		rw := httptest.NewRecorder()
		h.Callback(rw, req)
		loc := rw.Header().Get("Location")
		if rw.Code != http.StatusFound || !strings.Contains(loc, "reason="+wantReason) {
			t.Fatalf("%s: want 302 reason=%s, got %d %q", name, wantReason, rw.Code, loc)
		}
		return rw
	}

	// 缺 cookie → state_mismatch
	run("missing cookie", newCallbackReq(good, nil, "&code=c"), "state_mismatch")
	// cookie 不符 → state_mismatch
	rw := run("mismatched cookie", newCallbackReq(good, &http.Cookie{Name: corosMcpNonceCookie, Value: "nonce-OTHER"}, "&code=c"), "state_mismatch")
	// 比對後立即清 cookie（Max-Age=0、Path 限縮在 callback）
	sc := rw.Header().Get("Set-Cookie")
	if !strings.Contains(sc, corosMcpNonceCookie+"=") || !strings.Contains(sc, "Max-Age=0") || !strings.Contains(sc, "Path="+corosMcpCallbackPath) {
		t.Fatalf("callback must clear the nonce cookie, got %q", sc)
	}
	// cookie 空值 → state_mismatch
	run("empty cookie", newCallbackReq(good, &http.Cookie{Name: corosMcpNonceCookie, Value: ""}, "&code=c"), "state_mismatch")
	// state 過期 → invalid_state（在 CSRF 比對之前就被擋）
	run("expired state", newCallbackReq(expired, &http.Cookie{Name: corosMcpNonceCookie, Value: "nonce-abc"}, "&code=c"), "invalid_state")
	// state 被竄改 → invalid_state
	run("tampered state", newCallbackReq(good+"x", &http.Cookie{Name: corosMcpNonceCookie, Value: "nonce-abc"}, "&code=c"), "invalid_state")
	// 正確 cookie → 通過 CSRF（用 ?error=access_denied 讓流程在 CSRF 之後、碰網路之前結束，證明沒被 CSRF 擋）
	rw = run("correct cookie", newCallbackReq(good, &http.Cookie{Name: corosMcpNonceCookie, Value: "nonce-abc"}, "&error=access_denied"), "denied")
	if sc := rw.Header().Get("Set-Cookie"); !strings.Contains(sc, "Max-Age=0") {
		t.Fatalf("nonce cookie must be cleared even on success path, got %q", sc)
	}
}

func TestCorosMcpNonceCookieAttributes(t *testing.T) {
	c := corosMcpNewNonceCookie("abc", 600)
	s := c.String()
	for _, want := range []string{"dor_cmcp_n=abc", "Path=/api/v1/integrations/coros-mcp/callback", "Max-Age=600", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(s, want) {
			t.Fatalf("cookie %q missing %q", s, want)
		}
	}
	if !strings.Contains(corosMcpNewNonceCookie("", -1).String(), "Max-Age=0") {
		t.Fatal("clear cookie must emit Max-Age=0")
	}
}

func TestCorosMcpNonceMatches(t *testing.T) {
	if !corosMcpNonceMatches("abc", "abc") {
		t.Fatal("equal nonce must match")
	}
	for _, c := range [][2]string{{"", ""}, {"abc", ""}, {"", "abc"}, {"abc", "abd"}, {"abc", "abcd"}} {
		if corosMcpNonceMatches(c[0], c[1]) {
			t.Fatalf("%q vs %q must not match", c[0], c[1])
		}
	}
}

func TestCorosMcpStateKeySeparation(t *testing.T) {
	secret := "jwt-secret-value"
	key := corosMcpStateKey(secret)
	if key == secret || key == "" {
		t.Fatal("state key must differ from JWT_SECRET")
	}
	if len(key) != 32 {
		t.Fatalf("derived key should be a 32-byte HMAC-SHA256 output, got %d", len(key))
	}
	if corosMcpStateKey(secret) != key {
		t.Fatal("derivation must be deterministic")
	}
	if corosMcpStateKey("other-secret") == key {
		t.Fatal("different JWT secrets must derive different keys")
	}
	// 用 JWT_SECRET 原值當 key 簽出的 state（舊作法）不得被驗過；直接以衍生 key 簽的才算數。
	msg := "u\nhttps://mcpus.coros.com\nv\nn\n" + fmt.Sprint(time.Now().Add(time.Minute).Unix())
	rawSigned := mustStateWithKey(secret, msg)
	if _, _, _, _, ok := corosMcpVerifyState(secret, rawSigned); ok {
		t.Fatal("state signed directly with JWT_SECRET must be rejected (key separation)")
	}
	derivedSigned := mustStateWithKey(key, msg)
	if _, _, _, _, ok := corosMcpVerifyState(secret, derivedSigned); !ok {
		t.Fatal("state signed with the derived key must verify")
	}
}

func mustStateWithKey(key, msg string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(msg)) + "." + corosMcpMAC(key, msg)
}

// --- 手動匯入節流／自動同步名額 ---

func TestCorosMcpAutoSyncOnce_SecondCallDoesNotRun(t *testing.T) {
	// repo=nil 的 handler：第一次搶到名額後會去查連線列——這裡不想碰 DB，所以先把名額占掉，確認「搶不到就完全不動作」。
	h := newTestCorosMcpHandler()
	if !h.claimAutoSync(t.Context(), "u1") {
		t.Fatal("setup claim")
	}
	ran, res, err := h.autoSyncOnce(t.Context(), "u1")
	if ran || err != nil || res.Fetched != 0 {
		t.Fatalf("lost claim must be a complete no-op, got ran=%v err=%v res=%+v", ran, err, res)
	}
}

func TestCorosMcpSyncResultJSONShape(t *testing.T) {
	dev := "COROS PACE 4"
	b, _ := json.Marshal(corosMcpImportResponse{Days: 30, CorosMcpSyncResult: CorosMcpSyncResult{Fetched: 5, Imported: 2, DeviceName: &dev}})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"days", "fetched", "imported", "duplicate", "exists", "skipped_before_connect", "skipped_non_running", "skipped_invalid", "errors", "device_name"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("response missing key %q: %s", k, b)
		}
	}
	if m["device_name"] != "COROS PACE 4" || m["days"] != float64(30) {
		t.Fatalf("unexpected values: %s", b)
	}
}

// --- Stage 2 審查後修正 ---

func TestCorosMcpAutoFrom(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	ptr := func(t time.Time) *time.Time { return &t }
	cases := []struct {
		name string
		last *time.Time
		want time.Time
	}{
		{"never synced → 30 days (floor clamps later)", nil, now.AddDate(0, 0, -30)},
		{"synced 1h ago → 3-day window", ptr(now.Add(-time.Hour)), now.AddDate(0, 0, -3)},
		{"synced 10 days ago → last−1 day", ptr(now.AddDate(0, 0, -10)), now.AddDate(0, 0, -11)},
		{"synced 60 days ago → capped at 30 days", ptr(now.AddDate(0, 0, -60)), now.AddDate(0, 0, -30)},
	}
	for _, c := range cases {
		if got := corosMcpAutoFrom(now, c.last); !got.Equal(c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestCorosMcpCallback_InvalidStateStillClearsCookie(t *testing.T) {
	h := newTestCorosMcpHandler()
	h.cfg.FrontendURL = "https://www.dor.tw"
	rw := httptest.NewRecorder()
	h.Callback(rw, newCallbackReq("garbage-state", &http.Cookie{Name: corosMcpNonceCookie, Value: "nonce-abc"}, "&code=c"))
	if loc := rw.Header().Get("Location"); !strings.Contains(loc, "reason=invalid_state") {
		t.Fatalf("want invalid_state, got %q", loc)
	}
	if sc := rw.Header().Get("Set-Cookie"); !strings.Contains(sc, corosMcpNonceCookie+"=") || !strings.Contains(sc, "Max-Age=0") {
		t.Fatalf("nonce cookie must be cleared even when state is invalid, got %q", sc)
	}
}
