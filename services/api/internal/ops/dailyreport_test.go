package ops

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFormatCents(t *testing.T) {
	cases := []struct {
		cents int
		want  string
	}{
		{0, "0"},
		{100, "1"},
		{150, "1"}, // 分沒有湊滿一元的部分無條件捨去（金流實務上皆為整數分，不會出現這種輸入）
		{123456, "1234"},
	}
	for _, c := range cases {
		if got := formatCents(c.cents); got != c.want {
			t.Errorf("formatCents(%d) = %q, want %q", c.cents, got, c.want)
		}
	}
}

func TestCheckFailureLines_AllOK(t *testing.T) {
	checks := []CheckResult{
		{Name: "pending_vip_orders_stuck", OK: true},
		{Name: activityHeartbeatCheck, OK: true},
	}
	if lines := checkFailureLines(checks); len(lines) != 0 {
		t.Fatalf("expected no failure lines when all OK, got %v", lines)
	}
}

func TestCheckFailureLines_SomeFailedUsesLabel(t *testing.T) {
	checks := []CheckResult{
		{Name: "pending_vip_orders_stuck", OK: false, Detail: "共 3 筆"},
		{Name: "paytx_long_pending", OK: true},
	}
	lines := checkFailureLines(checks)
	if len(lines) != 1 {
		t.Fatalf("expected 1 failure line, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "卡住的pending VIP訂單") || !strings.Contains(lines[0], "共 3 筆") {
		t.Errorf("unexpected line content: %q", lines[0])
	}
}

func TestCheckFailureLines_UnknownNameFallsBackAndEmptyDetail(t *testing.T) {
	checks := []CheckResult{{Name: "some_future_check", OK: false}}
	lines := checkFailureLines(checks)
	if len(lines) != 1 || !strings.Contains(lines[0], "some_future_check") || !strings.Contains(lines[0], "無詳情") {
		t.Fatalf("unexpected fallback line: %v", lines)
	}
}

func baseReportData() dailyReportData {
	return dailyReportData{
		ReportDate:       "2026-08-23",
		NewMembers:       12,
		TotalMembers:     3456,
		RaceRevenueCents: 1234500,
		RaceOrderCount:   30,
		VipRevenueCents:  56000,
		VipOrderCount:    5,
		Checks: []CheckResult{
			{Name: "pending_vip_orders_stuck", OK: true},
			{Name: activityHeartbeatCheck, OK: true},
		},
	}
}

func TestBuildDailyReportMessage_NoSignupsShowsPlaceholder(t *testing.T) {
	d := baseReportData()
	msg := buildDailyReportMessage(d)
	if !strings.Contains(msg, "昨日無新增報名") {
		t.Errorf("expected placeholder line for no signups, got:\n%s", msg)
	}
	// baseReportData() 的 Checks 固定給 2 筆 mock 結果（見該函式），訊息文字動態取自 len(d.Checks)，
	// 不是寫死的正式檢查總數（見 assembleDailyReportMessage 註解）。
	if !strings.Contains(msg, "2 項全數正常 ✅") {
		t.Errorf("expected all-ok selfcheck line, got:\n%s", msg)
	}
	if !strings.Contains(msg, "資料收集中（今日啟用）") {
		t.Errorf("expected traffic no-data placeholder, got:\n%s", msg)
	}
}

func TestBuildDailyReportMessage_RaceLinesAndTotals(t *testing.T) {
	d := baseReportData()
	d.RaceSignups = []raceSignupLine{
		{Title: "台北馬拉松", N: 42, M: 40},
		{Title: "夜跑挑戰賽", N: 5, M: 0},
	}
	msg := buildDailyReportMessage(d)
	if !strings.Contains(msg, "《台北馬拉松》+42（已付 40）") {
		t.Errorf("missing first race line, got:\n%s", msg)
	}
	if !strings.Contains(msg, "《夜跑挑戰賽》+5（已付 0）") {
		t.Errorf("missing second race line, got:\n%s", msg)
	}
	// 12345 + 560 = 12905 元合計
	if !strings.Contains(msg, "合計 NT$12905") {
		t.Errorf("expected total NT$12905, got:\n%s", msg)
	}
	if strings.Contains(msg, "退費") {
		t.Errorf("RefundCount=0 時不應出現退費行，got:\n%s", msg)
	}
}

// 虛擬選手報名標註：V>0 加「，虛擬 V」後綴、V=0 維持原格式——防止「已付 N」被誤讀成真實金流
// 去綠界對帳（2026-08-28 實際發生過，見 buildDailyReportData 報名查詢的註解）。
func TestBuildDailyReportMessage_VirtualSignupAnnotation(t *testing.T) {
	d := baseReportData()
	d.RaceSignups = []raceSignupLine{
		{Title: "健康啟動", N: 50, M: 50, V: 50},
		{Title: "台北馬拉松", N: 3, M: 2, V: 0},
	}
	msg := buildDailyReportMessage(d)
	if !strings.Contains(msg, "《健康啟動》+50（已付 50，虛擬 50）") {
		t.Errorf("virtual annotation missing, got:\n%s", msg)
	}
	if !strings.Contains(msg, "《台北馬拉松》+3（已付 2）") {
		t.Errorf("V=0 line should keep original format, got:\n%s", msg)
	}
}

func TestBuildDailyReportMessage_ShowsRefundLineOnlyWhenPresent(t *testing.T) {
	d := baseReportData()
	d.RefundCents = 9900
	d.RefundCount = 2
	msg := buildDailyReportMessage(d)
	if !strings.Contains(msg, "退費 NT$99（2 筆）") {
		t.Errorf("expected refund line, got:\n%s", msg)
	}
}

func TestBuildDailyReportMessage_SelfcheckFailuresListed(t *testing.T) {
	d := baseReportData()
	d.Checks = []CheckResult{
		{Name: "webhook_failure_rate", OK: false, Detail: "failed=6 paid=2"},
		{Name: activityHeartbeatCheck, OK: true},
	}
	msg := buildDailyReportMessage(d)
	if strings.Contains(msg, "項全數正常") {
		t.Errorf("should not show all-ok line when a check failed, got:\n%s", msg)
	}
	if !strings.Contains(msg, "failed=6 paid=2") {
		t.Errorf("expected failure detail in message, got:\n%s", msg)
	}
}

func TestBuildDailyReportMessage_TrafficSuspiciousList(t *testing.T) {
	d := baseReportData()
	d.Traffic = trafficSummary{
		HasData:       true,
		TotalRequests: 100000,
		DistinctIPs:   3000,
		CountryTop:    []countryShare{{Country: "TW", Requests: 60000}, {Country: "US", Requests: 40000}},
		AuthFailIPs:   []ipFlagLine{{IP: "1.2.3.4", Country: "CN", Count: 55}},
		HighVolumeIPs: []ipFlagLine{{IP: "5.6.7.8", Country: "US", Count: 6000}},
		NonTWFlag:     true,
		NonTWPct:      0.4,
	}
	msg := buildDailyReportMessage(d)
	for _, want := range []string{
		"總請求 100000、獨立 IP 3000",
		"TW 60%", "US 40%",
		"登入失敗異常 IP 1.2.3.4(CN) 55 次",
		"單一IP高流量 5.6.7.8(US) 6000 次請求",
		"非TW流量佔比 40%",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected message to contain %q, got:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "無異常事件") {
		t.Errorf("should not show '無異常事件' when suspicious entries exist, got:\n%s", msg)
	}
}

func TestBuildDailyReportMessage_TrafficNoAnomalyShowsClean(t *testing.T) {
	d := baseReportData()
	d.Traffic = trafficSummary{HasData: true, TotalRequests: 500, DistinctIPs: 200}
	msg := buildDailyReportMessage(d)
	if !strings.Contains(msg, "⚠️ 可疑清單：無異常事件") {
		t.Errorf("expected clean traffic line, got:\n%s", msg)
	}
}

// TestBuildDailyReportMessage_TruncatesLongRaceList 賽事筆數多到讓整則訊息超過 telegramMaxLen 時，
// 應該只裁「報名」區塊、保留其餘固定段落完整，並附註省略場數；輸出仍必須符合上限。
func TestBuildDailyReportMessage_TruncatesLongRaceList(t *testing.T) {
	d := baseReportData()
	for i := 0; i < 500; i++ {
		d.RaceSignups = append(d.RaceSignups, raceSignupLine{
			Title: fmt.Sprintf("測試賽事編號第%03d場超長標題撐爆長度用", i),
			N:     500 - i,
			M:     0,
		})
	}
	full := assembleDailyReportMessage(d, len(d.RaceSignups))
	if utf8.RuneCountInString(full) <= telegramMaxLen {
		t.Fatal("test setup invalid: full message should exceed telegramMaxLen to exercise truncation")
	}

	msg := buildDailyReportMessage(d)
	if n := utf8.RuneCountInString(msg); n > telegramMaxLen {
		t.Fatalf("truncated message still exceeds telegramMaxLen: %d runes", n)
	}
	if !strings.Contains(msg, "其餘") || !strings.Contains(msg, "場") {
		t.Errorf("expected truncation note '…其餘 N 場', got tail:\n%s", msg[len(msg)-200:])
	}
	// 保留的賽事應是排序在前面的（N 較大的），驗證第一筆仍在
	if !strings.Contains(msg, "測試賽事編號第000場") {
		t.Errorf("expected highest-N race to survive truncation, got:\n%s", msg)
	}
	// 其餘固定段落（會員/營收/自檢/流量標題）必須完整保留，不能被誤裁
	for _, want := range []string{"👥 會員", "💰 營收", "🔍 資料自檢", "🌐 流量安全"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected fixed section %q to survive truncation, got:\n%s", want, msg)
		}
	}
}

func TestInDailyReportWindow_MatchesSelfCheckWindow(t *testing.T) {
	// inDailyReportWindow 直接沿用 inSelfCheckWindow，兩者對同一輸入必須永遠一致。
	for hour := 0; hour < 24; hour++ {
		tm := time.Date(2026, 8, 21, hour, 30, 0, 0, time.UTC)
		if inDailyReportWindow(tm) != inSelfCheckWindow(tm) {
			t.Errorf("hour=%d: inDailyReportWindow and inSelfCheckWindow disagree", hour)
		}
	}
}
