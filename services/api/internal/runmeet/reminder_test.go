package runmeet

// ⚠️ 這是獨立新檔，不動既有 runmeet_test.go（另一位工程師同時在改那支）。
// 涵蓋：合併邏輯（一人多場→一封）、CAS 冪等（第二次不發）、Email 收件三條件各自跳過、
// 時間格式化六時段與 0 點/中午邊界、已開跑的團練不發（含時間視窗上下界）。
// 全部脫離真實 DB——processReminderCandidates 的 DB 依賴用 func 參數注入假實作
// （見 reminder.go 檔頭），不需要連線也能測 CAS 冪等這種「本來要碰 DB」的行為。

import (
	"context"
	"strings"
	"testing"
	"time"
)

// --- 時間格式化：六時段 + 0 點／中午邊界（釘住「凌晨 0:30」而非「上午 12:30」、
//     「下午 12:30」而非「下午 0:30」這兩個口語最容易讀錯的邊界）---

func TestFmtMeetAtSixPeriodsAndBoundaries(t *testing.T) {
	// mk 回傳「台北 hh:mm」對應的時刻（taipeiTime = UTC + 8h，所以反推 UTC = 台北 - 8h）。
	mk := func(taipeiHour, taipeiMin int) time.Time {
		return time.Date(2026, 8, 30, taipeiHour, taipeiMin, 0, 0, time.UTC).Add(-8 * time.Hour)
	}
	now := mk(12, 0) // 台北 8/30 12:00，當「今天」基準

	cases := []struct {
		name   string
		meetAt time.Time
		want   string
	}{
		{"0 點是凌晨 0:xx，不是上午 12:xx", mk(0, 30), "今天 凌晨 0:30"},
		{"清晨 [4,6)", mk(5, 0), "今天 清晨 5:00"},
		{"上午 [6,12)", mk(9, 15), "今天 上午 9:15"},
		{"12 點是下午 12:xx，不是下午 0:xx", mk(12, 30), "今天 下午 12:30"},
		{"下午 [12,17) 一般值", mk(15, 0), "今天 下午 3:00"},
		{"傍晚 [17,19)", mk(18, 0), "今天 傍晚 6:00"},
		{"晚上 [19,24)", mk(21, 0), "今天 晚上 9:00"},
		{"凌晨上界前一刻 [0,4)", mk(3, 59), "今天 凌晨 3:59"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fmtMeetAt(c.meetAt, now); got != c.want {
				t.Fatalf("%s: want %q got %q", c.name, c.want, got)
			}
		})
	}
}

func TestFmtMeetAtDifferentDayShowsWeekday(t *testing.T) {
	now := time.Date(2026, 8, 31, 4, 0, 0, 0, time.UTC)    // 台北 8/31 12:00（一）
	meetAt := time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC) // 台北 9/1 21:00（二）
	if got, want := fmtMeetAt(meetAt, now), "9/1（二）晚上 9:00"; got != want {
		t.Fatalf("跨日應帶月/日（星期），want %q got %q", want, got)
	}
}

// --- 已開跑的團練不發：isReminderCandidate 的時間視窗邊界 ---

func TestIsReminderCandidateBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		meetAt time.Time
		hours  int
		want   bool
	}{
		{"恰好正在開跑那一刻不發", now, 3, false},
		{"已開跑（過去）不發", now.Add(-time.Second), 3, false},
		{"視窗內", now.Add(2 * time.Hour), 3, true},
		{"恰好等於上界仍算", now.Add(3 * time.Hour), 3, true},
		{"超出視窗不發", now.Add(3*time.Hour + time.Second), 3, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isReminderCandidate(c.meetAt, now, c.hours); got != c.want {
				t.Fatalf("%s: want %v got %v", c.name, c.want, got)
			}
		})
	}
}

// --- CAS 冪等：processReminderCandidates 第二次呼叫（同一批候選、同一份「已標記」狀態）不該再發 ---

func TestProcessReminderCandidatesCASIdempotent(t *testing.T) {
	sent := map[string]bool{} // 模擬 run_meets.reminder_sent_at 是否已非 NULL
	mark := func(ctx context.Context, id string) (bool, error) {
		if sent[id] {
			return false, nil // RowsAffected=0：已發過或被搶走
		}
		sent[id] = true
		return true, nil
	}
	memberCalls := 0
	members := func(ctx context.Context, id string) ([]string, error) {
		memberCalls++
		return []string{"user-1"}, nil
	}

	now := time.Now()
	candidates := []reminderCandidate{{ID: "meet-1", Title: "晨跑", MeetAt: now.Add(time.Hour)}}

	first := processReminderCandidates(context.Background(), candidates, now, 3, mark, members)
	if len(first) != 1 {
		t.Fatalf("第一次應產生 1 筆 pair，得 %d", len(first))
	}
	if memberCalls != 1 {
		t.Fatalf("第一次應查一次成員，得 %d 次", memberCalls)
	}

	second := processReminderCandidates(context.Background(), candidates, now, 3, mark, members)
	if len(second) != 0 {
		t.Fatalf("第二次應冪等跳過（已標記），得 %d 筆", len(second))
	}
	if memberCalls != 1 {
		t.Fatalf("第二次不該再查成員（CAS 沒標記成功就不該往下走），累計得 %d 次", memberCalls)
	}
}

// 已開跑的候選（就算混在同一批裡）不該被標記、也不該查成員。
func TestProcessReminderCandidatesSkipsStarted(t *testing.T) {
	now := time.Now()
	var markedIDs []string
	mark := func(ctx context.Context, id string) (bool, error) {
		markedIDs = append(markedIDs, id)
		return true, nil
	}
	members := func(ctx context.Context, id string) ([]string, error) { return []string{"u1"}, nil }

	candidates := []reminderCandidate{
		{ID: "started", MeetAt: now.Add(-time.Minute)}, // 已開跑
		{ID: "upcoming", MeetAt: now.Add(time.Hour)},   // 仍未開跑
	}
	pairs := processReminderCandidates(context.Background(), candidates, now, 3, mark, members)
	if len(pairs) != 1 || pairs[0].Meet.ID != "upcoming" {
		t.Fatalf("只有仍未開跑的團練該產生提醒，得 %+v", pairs)
	}
	if len(markedIDs) != 1 || markedIDs[0] != "upcoming" {
		t.Fatalf("已開跑的團練不該呼叫 CAS 標記，得 %v", markedIDs)
	}
}

// --- 合併邏輯：一人多場只出現一次、依 meet_at 升冪排序，url 依單場/多場切換 ---

func TestBuildReminderContentsMerge(t *testing.T) {
	now := time.Now()
	meetA := reminderMeet{ID: "a", Title: "晨跑A", MeetAt: now.Add(2 * time.Hour)}
	meetB := reminderMeet{ID: "b", Title: "晨跑B", MeetAt: now.Add(1 * time.Hour)} // 較早開始
	pairs := []userMeetPair{
		{UserID: "u1", Meet: meetA},
		{UserID: "u2", Meet: meetA},
		{UserID: "u1", Meet: meetB},
	}
	contents := buildReminderContents(pairs, now)
	if len(contents) != 2 {
		t.Fatalf("應分成 2 位使用者各一份，得 %d", len(contents))
	}
	if contents[0].UserID != "u1" {
		t.Fatalf("應保留輸入中首次出現的使用者順序，得 %q", contents[0].UserID)
	}
	u1 := contents[0]
	if len(u1.Meets) != 2 {
		t.Fatalf("u1 應合併成 2 場（同一人多場只發一封的前提），得 %d 場", len(u1.Meets))
	}
	if u1.Meets[0].ID != "b" || u1.Meets[1].ID != "a" {
		t.Fatalf("多場應依 meet_at 升冪排序（b 較早），得 %s, %s", u1.Meets[0].ID, u1.Meets[1].ID)
	}
	if u1.MailURL != "/?runmeet=list" {
		t.Fatalf("多場 url 應為 list，得 %q", u1.MailURL)
	}
	if !strings.Contains(u1.MailBody, "你有 2 場") || !strings.Contains(u1.MailBody, "晨跑A") || !strings.Contains(u1.MailBody, "晨跑B") {
		t.Fatalf("多場內文應標明場數且列出每場名稱，得 %q", u1.MailBody)
	}

	u2 := contents[1]
	if len(u2.Meets) != 1 || u2.MailURL != "/?runmeet=a" {
		t.Fatalf("u2 只有 1 場，url 應直接指向該場，得 meets=%+v url=%q", u2.Meets, u2.MailURL)
	}
	if u1.MailTitle != reminderMailTitle || u2.MailTitle != reminderMailTitle {
		t.Fatal("站內信標題單場/多場皆應為同一固定標題")
	}
}

// --- Email 收件資格：偏好關閉／在退訂表／無 email 各自跳過（兩個條件都要通過才寄） ---

func TestEligibleForReminderEmail(t *testing.T) {
	cases := []struct {
		name string
		rec  emailRecipient
		want bool
	}{
		{"全部符合才寄", emailRecipient{OptIn: true, Unsub: false, Email: "a@b.com"}, true},
		{"偏好關閉跳過", emailRecipient{OptIn: false, Unsub: false, Email: "a@b.com"}, false},
		{"在退訂表跳過（即使偏好開著）", emailRecipient{OptIn: true, Unsub: true, Email: "a@b.com"}, false},
		{"無 email 跳過", emailRecipient{OptIn: true, Unsub: false, Email: ""}, false},
		{"email 只有空白視同無", emailRecipient{OptIn: true, Unsub: false, Email: "   "}, false},
		{"偏好關閉且在退訂表", emailRecipient{OptIn: false, Unsub: true, Email: "a@b.com"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := eligibleForReminderEmail(c.rec); got != c.want {
				t.Fatalf("%s: want %v got %v", c.name, c.want, got)
			}
		})
	}
}

// --- 地點文字：不限地點固定文案；一般地點 region・place_label（僅公開層，見檔頭） ---

func TestReminderLocationText(t *testing.T) {
	if got, want := reminderLocationText(reminderMeet{NoLocation: true}), "🌏 不限地點"; got != want {
		t.Fatalf("不限地點應顯示固定文案，want %q got %q", want, got)
	}
	m := reminderMeet{Region: "臺北市・大安區", PlaceLabel: "大安森林公園"}
	if got, want := reminderLocationText(m), "臺北市・大安區・大安森林公園"; got != want {
		t.Fatalf("一般地點應為 region・place_label，want %q got %q", want, got)
	}
}

// Email 內文不得出現座標／集合細節等成員層字樣（reminderMeet 結構上就沒有 lat/lng/meeting_detail
// 三個欄位，這裡額外釘住文案本身有提示語，雙重把關）。
func TestBuildReminderEmailBodyHTMLNoPreciseLocationLeak(t *testing.T) {
	now := time.Now()
	m := reminderMeet{ID: "m1", Title: "晨跑", MeetAt: now.Add(time.Hour), Region: "臺北市・大安區", PlaceLabel: "大安森林公園", MemberCount: 5, Capacity: 20}
	html := buildReminderEmailBodyHTML([]reminderMeet{m}, now)
	if !strings.Contains(html, "詳細集合地點請點連結在 App 查看") {
		t.Fatal("內文應提示詳細地點請至 App 查看")
	}
	if !strings.Contains(html, "5/20 人") {
		t.Fatalf("內文應含人數，got %q", html)
	}
	if strings.Contains(html, "meeting_detail") || strings.Contains(html, "lat") || strings.Contains(html, "lng") {
		t.Fatal("Email 內文不得出現成員層座標／集合細節相關字樣")
	}
}
