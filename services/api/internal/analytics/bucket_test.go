package analytics

import (
	"reflect"
	"testing"
	"time"
)

func TestTaiwanDayBoundaryUTC(t *testing.T) {
	// taiwanNow() 回傳的是 real-UTC-time + 8h（欄位代表台灣本地時刻）。這裡固定一個 2026-03-15
	// 台灣時間 14:30 的情境（其底層時刻是 UTC 2026-03-15 06:30，但傳入函式的值已經是「加 8 後」的
	// 表示法，故直接用 UTC location 建構，符合 taiwanNow() 的實際回傳形狀）。
	taiwanLocal := time.Date(2026, 3, 15, 14, 30, 0, 0, time.UTC)
	got := taiwanDayBoundaryUTC(taiwanLocal)
	// 台灣 2026-03-15 00:00 對應的真實 UTC 絕對時刻 = 2026-03-14 16:00 UTC。
	want := time.Date(2026, 3, 14, 16, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("taiwanDayBoundaryUTC() = %v, want %v", got, want)
	}
}

func TestTaiwanDaySeries(t *testing.T) {
	today := time.Date(2026, 1, 3, 10, 0, 0, 0, time.UTC)

	got := taiwanDaySeries(today, 5)
	want := []string{"2025-12-30", "2025-12-31", "2026-01-01", "2026-01-02", "2026-01-03"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("taiwanDaySeries(days=5) = %v, want %v", got, want)
	}

	if got := taiwanDaySeries(today, 1); !reflect.DeepEqual(got, []string{"2026-01-03"}) {
		t.Errorf("taiwanDaySeries(days=1) = %v", got)
	}

	if got := taiwanDaySeries(today, 0); len(got) != 0 {
		t.Errorf("taiwanDaySeries(days=0) should be empty, got %v", got)
	}
	if got := taiwanDaySeries(today, -3); len(got) != 0 {
		t.Errorf("taiwanDaySeries(negative days) should be empty, got %v", got)
	}
}

func TestMergeDateCounts(t *testing.T) {
	series := []string{"2026-01-01", "2026-01-02", "2026-01-03"}
	counts := map[string]int{"2026-01-01": 5, "2026-01-03": 2, "2026-01-09": 999} // 09 不在序列內，應被忽略
	got := mergeDateCounts(series, counts)
	want := []DateCount{
		{Date: "2026-01-01", Count: 5},
		{Date: "2026-01-02", Count: 0},
		{Date: "2026-01-03", Count: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeDateCounts() = %+v, want %+v", got, want)
	}
}

func TestMergeDateKm(t *testing.T) {
	series := []string{"2026-01-01", "2026-01-02"}
	kms := map[string]float64{"2026-01-01": 12.5}
	got := mergeDateKm(series, kms)
	want := []DateKm{
		{Date: "2026-01-01", Km: 12.5},
		{Date: "2026-01-02", Km: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeDateKm() = %+v, want %+v", got, want)
	}
}

func TestBucketList(t *testing.T) {
	order := []string{"a", "b", "c"}
	counts := map[string]int{"b": 7}
	got := bucketList(order, counts)
	want := []BucketCount{{Bucket: "a", Count: 0}, {Bucket: "b", Count: 7}, {Bucket: "c", Count: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bucketList() = %+v, want %+v", got, want)
	}
}

func TestGroupAvgList(t *testing.T) {
	order := []string{"male", "female"}
	sums := map[string]float64{"male": 100, "female": 0}
	counts := map[string]int{"male": 4, "female": 0}
	got := groupAvgList(order, sums, counts)
	want := []GroupAvg{
		{Group: "male", AvgKm: 25, Users: 4},
		{Group: "female", AvgKm: 0, Users: 0}, // 0 人時不除以 0，avg_km 固定 0
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("groupAvgList() = %+v, want %+v", got, want)
	}
}

func TestRound2(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{1.005, 1.01}, // 四捨五入而非銀行家捨去
		{1.004, 1.0},
		{0, 0},
		{3.14159, 3.14},
	}
	for _, c := range cases {
		if got := round2(c.in); got != c.want {
			t.Errorf("round2(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRound1(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{3.45, 3.5}, // 四捨五入而非銀行家捨去（同 TestRound2 的 1.005 案例邏輯）
		{3.44, 3.4},
		{0, 0},
		{3.5, 3.5},
	}
	for _, c := range cases {
		if got := round1(c.in); got != c.want {
			t.Errorf("round1(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestAvgPaceSeconds(t *testing.T) {
	cases := []struct {
		durationS int
		km        float64
		want      int
	}{
		{3600, 10, 360}, // 10km/hr 配速 = 360 秒/km
		{0, 0, 0},       // totalKm<=0 防禦性回傳 0
		{100, 0, 0},     // totalKm=0 防禦性回傳 0
		{361, 1, 361},   // 整除
		{100, 3, 33},    // 100/3=33.33 → 四捨五入 33
		{101, 3, 34},    // 101/3=33.67 → 四捨五入 34
	}
	for _, c := range cases {
		if got := avgPaceSeconds(c.durationS, c.km); got != c.want {
			t.Errorf("avgPaceSeconds(%d, %v) = %d, want %d", c.durationS, c.km, got, c.want)
		}
	}
}

func TestAvgDaysPerWeek(t *testing.T) {
	today := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	// 邊界：首跑當天=週數1（first_day == today，天數差 0，weeks 下限 1 保護生效）。
	if got := avgDaysPerWeek(1, today, today); got != 1.0 {
		t.Errorf("avgDaysPerWeek(首跑當天) = %v, want 1.0", got)
	}

	// 邊界：整除。14 天前開始（剛好滿 2 週），6 個有跑步的日子 → 6/2 = 3.0。
	first14 := today.AddDate(0, 0, -14)
	if got := avgDaysPerWeek(6, first14, today); got != 3.0 {
		t.Errorf("avgDaysPerWeek(整除) = %v, want 3.0", got)
	}

	// 邊界：非整除。10 天前開始（10/7 週），5 個有跑步的日子 → 5/(10/7) = 3.5。
	first10 := today.AddDate(0, 0, -10)
	if got := avgDaysPerWeek(5, first10, today); got != 3.5 {
		t.Errorf("avgDaysPerWeek(非整除) = %v, want 3.5", got)
	}

	// 觀察窗口不足 1 週（3 天前開始）：weeks 下限 1 保護生效，不會膨脹成 2/(3/7)=4.67。
	first3 := today.AddDate(0, 0, -3)
	if got := avgDaysPerWeek(2, first3, today); got != 2.0 {
		t.Errorf("avgDaysPerWeek(不足1週) = %v, want 2.0", got)
	}

	// runDays<=0 防禦性回傳 0。
	if got := avgDaysPerWeek(0, first10, today); got != 0 {
		t.Errorf("avgDaysPerWeek(runDays=0) = %v, want 0", got)
	}
}

func TestLoginFreqBucket(t *testing.T) {
	cases := map[int]string{0: "0", 1: "1-2", 2: "1-2", 3: "3-9", 9: "3-9", 10: "10-29", 29: "10-29", 30: "30+", 100: "30+", -1: "0"}
	for in, want := range cases {
		if got := loginFreqBucket(in); got != want {
			t.Errorf("loginFreqBucket(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestPaceBucket(t *testing.T) {
	cases := map[float64]string{
		0:    "<5:00",
		-10:  "<5:00",
		299:  "<5:00",
		300:  "5-6",
		359:  "5-6",
		360:  "6-7",
		419:  "6-7",
		420:  "7-8",
		479:  "7-8",
		480:  ">8:00",
		1000: ">8:00",
	}
	for in, want := range cases {
		if got := paceBucket(in); got != want {
			t.Errorf("paceBucket(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestMonthlyVolumeBucket(t *testing.T) {
	cases := map[float64]string{
		0:     "0",
		0.5:   "1-20",
		20:    "1-20",
		20.1:  "21-50",
		50:    "21-50",
		50.1:  "51-100",
		100:   "51-100",
		100.1: "100+",
		500:   "100+",
	}
	for in, want := range cases {
		if got := monthlyVolumeBucket(in); got != want {
			t.Errorf("monthlyVolumeBucket(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestRepeatBucket(t *testing.T) {
	cases := map[int]string{0: "0", 1: "1", 2: "2-3", 3: "2-3", 4: "4+", 10: "4+"}
	for in, want := range cases {
		if got := repeatBucket(in); got != want {
			t.Errorf("repeatBucket(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestCardBucket(t *testing.T) {
	cases := map[int]string{0: "0", 1: "1-5", 5: "1-5", 6: "6-20", 20: "6-20", 21: "21-50", 50: "21-50", 51: "50+", 200: "50+"}
	for in, want := range cases {
		if got := cardBucket(in); got != want {
			t.Errorf("cardBucket(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestAgeBucket(t *testing.T) {
	asOf := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	if got := ageBucket(nil, asOf); got != "未填" {
		t.Errorf("ageBucket(nil) = %q, want 未填", got)
	}

	mk := func(y int, m time.Month, d int) *time.Time {
		v := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		return &v
	}

	// asOf = 2026-06-15。"上緣"案例刻意用生日月/日=6/16（asOf 尚未走到今年生日，還沒過生日，
	// 年齡=去年那次生日算起，比只看年份差少 1 歲）；"下緣"案例用生日當天或已過的日期，代表今年
	// 生日已到，年齡=年份差本身，剛好落在下一個級距的最小值。
	cases := []struct {
		name     string
		birthday *time.Time
		want     string
	}{
		{"17歲差1天過生日", mk(2008, 6, 16), "<18"}, // 明天才滿18
		{"剛滿18", mk(2008, 6, 15), "18-24"},
		{"24歲上緣(明天才過生日)", mk(2001, 6, 16), "18-24"},
		{"25歲下緣", mk(2001, 6, 14), "25-34"},
		{"34歲上緣(明天才過生日)", mk(1991, 6, 16), "25-34"},
		{"35歲下緣", mk(1991, 6, 14), "35-44"},
		{"44歲上緣(明天才過生日)", mk(1981, 6, 16), "35-44"},
		{"45歲下緣", mk(1981, 6, 14), "45-54"},
		{"54歲上緣(明天才過生日)", mk(1971, 6, 16), "45-54"},
		{"55歲下緣", mk(1971, 6, 14), "55+"},
		{"未來生日(防禦性)", mk(2030, 1, 1), "<18"},
	}
	for _, c := range cases {
		if got := ageBucket(c.birthday, asOf); got != c.want {
			t.Errorf("%s: ageBucket(%v) = %q, want %q", c.name, c.birthday.Format("2006-01-02"), got, c.want)
		}
	}
}

func TestLevelFromExp(t *testing.T) {
	// 門檻表比照 migrations/017_membership.sql 的種子資料（level_config 初始 5 級：
	// 1='新手'/0exp、2='入門'/100exp、3='進階'/250exp、4='資深'/500exp、5='菁英'/1000exp）——
	// 與 internal/profile/membership.go computeLevel 吃同一張表、同一種形狀，用真實種子值當對照點
	// 才有意義（此表後台可調，這裡固定值只是驗證換算邏輯本身，非斷言正式環境現在的門檻）。
	levels := []levelRow{
		{Level: 1, ExpRequired: 0},
		{Level: 2, ExpRequired: 100},
		{Level: 3, ExpRequired: 250},
		{Level: 4, ExpRequired: 500},
		{Level: 5, ExpRequired: 1000},
	}
	cases := []struct {
		name string
		exp  int
		want int
	}{
		{"exp=0 恰好門檻", 0, 1},
		{"未達下一級門檻", 99, 1},
		{"exp=100 恰好門檻", 100, 2},
		{"exp=249 未達下一級", 249, 2},
		{"exp=250 恰好門檻", 250, 3},
		{"exp=999 未達頂級", 999, 4},
		{"exp=1000 恰好頂級門檻", 1000, 5},
		{"超過頂級門檻仍為頂級", 999999, 5},
	}
	for _, c := range cases {
		if got := levelFromExp(c.exp, levels); got != c.want {
			t.Errorf("%s: levelFromExp(%d) = %d, want %d", c.name, c.exp, got, c.want)
		}
	}

	// 邊界：levels 為空（理論上不會發生，level_config 至少有種子資料）→ 防禦性回傳預設 1 級，
	// 比照來源函式 computeLevel 的預設值（internal/profile/membership.go:84）。
	if got := levelFromExp(500, []levelRow{}); got != 1 {
		t.Errorf("levelFromExp(空門檻表) = %d, want 1", got)
	}
}

func TestNormalizeGender(t *testing.T) {
	cases := map[string]string{
		"male":   "male",
		"female": "female",
		"other":  "other",
		"":       "unspecified",
		"weird":  "unspecified",
	}
	for in, want := range cases {
		if got := normalizeGender(in); got != want {
			t.Errorf("normalizeGender(%q) = %q, want %q", in, got, want)
		}
	}
}
