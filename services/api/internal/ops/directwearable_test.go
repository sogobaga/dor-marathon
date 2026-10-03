package ops

// 日報「直連手錶」段（S5，directwearable.go）的單元測試：假 provider、單一供應商失敗不拖垮報告、
// 格式快照不含任何顯示名稱／email／帳號編碼。

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

type fakeDirectProvider struct {
	name     string
	stats    DirectWearableStats
	statsErr error
	note     string
	maintErr error
	maint    int
	statsN   int
}

func (f *fakeDirectProvider) Name() string { return f.name }
func (f *fakeDirectProvider) DirectStats(ctx context.Context) (DirectWearableStats, error) {
	f.statsN++
	return f.stats, f.statsErr
}
func (f *fakeDirectProvider) MaintainDaily(ctx context.Context) (string, error) {
	f.maint++
	return f.note, f.maintErr
}

func TestBuildDirectWearableSection_FailingProviderDoesNotSinkTheReport(t *testing.T) {
	ok := &fakeDirectProvider{name: "coros", stats: DirectWearableStats{Provider: "coros", Connected: 12, Active24h: 9, NeedsReauth: 2, Stale3d: 1}}
	bad := &fakeDirectProvider{name: "garmin", statsErr: errors.New("db: connection refused 10.0.0.1")}
	sec := buildDirectWearableSection(context.Background(), []DirectWearableProvider{ok, bad})
	if len(sec.Stats) != 1 || sec.Stats[0].Provider != "coros" || len(sec.Failed) != 1 || sec.Failed[0] != "garmin" {
		t.Fatalf("unexpected section: %+v", sec)
	}
	if ok.maint != 1 || bad.maint != 1 {
		t.Fatalf("MaintainDaily must run once per provider even when stats fail: %d %d", ok.maint, bad.maint)
	}
	text := formatDirectWearableSection(sec)
	if strings.Contains(text, "connection refused") || strings.Contains(text, "10.0.0.1") {
		t.Fatalf("error text must never reach Telegram: %s", text)
	}
	if !strings.Contains(text, "⚠️ GARMIN：統計失敗") {
		t.Fatalf("failed provider line missing: %s", text)
	}
}

func TestFormatDirectWearableLine_Snapshot(t *testing.T) {
	cases := []struct {
		name string
		s    DirectWearableStats
		want string
	}{
		{"coros healthy", DirectWearableStats{Provider: "coros", Connected: 12, Active24h: 9, NeedsReauth: 2},
			"COROS：連線 12 人，24h 內有同步 9 人，需重新授權 2 人，超過 3 天未同步 0 人"},
		{"coros with imports", DirectWearableStats{Provider: "coros", Connected: 12, Active24h: 9, Imported24h: 7},
			"COROS：連線 12 人，24h 內有同步 9 人，需重新授權 0 人，超過 3 天未同步 0 人；24h 匯入 7 筆"},
		{"stale warns", DirectWearableStats{Provider: "coros", Connected: 5, Stale3d: 2},
			"⚠️ COROS：連線 5 人，24h 內有同步 0 人，需重新授權 0 人，超過 3 天未同步 2 人"},
		{"garmin full", DirectWearableStats{Provider: "garmin", Connected: 40, Active24h: 31, NeedsReauth: 3, Paused: 1, Stale3d: 0,
			Events24h: 120, Imported24h: 30, PendingStale: 2, Dead: 1, UnknownUser24h: 4},
			"⚠️ GARMIN：連線 40 人，24h 內有同步 31 人，需重新授權 3 人，超過 3 天未同步 0 人，暫停 1 人；24h 事件 120 筆、匯入 30 筆；待處理逾時 2 筆；死信 1 筆；24h 未知使用者事件 4 筆"},
	}
	for _, c := range cases {
		if got := formatDirectWearableLine(c.s); got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, c.want)
		}
	}
}

// 格式快照：整份日報訊息帶直連手錶段時，只有人數與供應商代碼；顯示名稱／email／帳號編碼絕不出現。
func TestDailyReportMessage_DirectWearableSectionHasNoNames(t *testing.T) {
	d := baseReportData()
	d.DirectWearable = directWearableSection{
		Stats: []DirectWearableStats{
			{Provider: "coros", Connected: 3, Active24h: 2, NeedsReauth: 1, Notes: []string{"尚未綁定 COROS 帳號識別的連線 1 人（無法防止同一個 COROS 帳號連到多個 DOR 帳號）"}},
			{Provider: "garmin", Connected: 2, Active24h: 2},
		},
		Maintenance: []string{"Garmin：已清除 5 筆過期事件"},
	}
	msg := buildDailyReportMessage(d)
	if !strings.Contains(msg, "🔌 直連手錶：") {
		t.Fatalf("section header missing:\n%s", msg)
	}
	for _, want := range []string{"COROS：連線 3 人", "GARMIN：連線 2 人", "· 尚未綁定 COROS 帳號識別的連線 1 人", "· Garmin：已清除 5 筆過期事件"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in:\n%s", want, msg)
		}
	}
	section := msg[strings.Index(msg, "🔌 直連手錶："):]
	for _, bad := range []string{"@", "#", "account", "handle", "email", "user_id"} {
		if strings.Contains(strings.ToLower(section), strings.ToLower(bad)) {
			t.Errorf("direct wearable section must not contain %q:\n%s", bad, section)
		}
	}
}

func TestDailyReportMessage_NoDirectWearableSectionWhenNothingRegistered(t *testing.T) {
	msg := buildDailyReportMessage(baseReportData())
	if strings.Contains(msg, "直連手錶") {
		t.Fatalf("no provider registered → no section:\n%s", msg)
	}
}

func TestAddDirectWearable_ConcurrentAndNilSafe(t *testing.T) {
	h := &Handler{}
	h.AddDirectWearable(nil)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.AddDirectWearable(&fakeDirectProvider{name: "x"})
		}()
	}
	wg.Wait()
	if got := len(h.directWearableProviders()); got != 20 {
		t.Fatalf("want 20 providers, got %d", got)
	}
}

// 備註與維護摘要空白行不顯示；維護失敗不影響統計。
func TestBuildDirectWearableSection_MaintenanceNotes(t *testing.T) {
	p := &fakeDirectProvider{name: "garmin", stats: DirectWearableStats{Provider: "garmin", Connected: 1}, note: "  已清除 3 筆  "}
	sec := buildDirectWearableSection(context.Background(), []DirectWearableProvider{p})
	if len(sec.Maintenance) != 1 || sec.Maintenance[0] != "已清除 3 筆" {
		t.Fatalf("maintenance note should be trimmed: %+v", sec.Maintenance)
	}
	p2 := &fakeDirectProvider{name: "coros", stats: DirectWearableStats{Provider: "coros"}, note: "   ", maintErr: errors.New("x")}
	sec2 := buildDirectWearableSection(context.Background(), []DirectWearableProvider{p2})
	if len(sec2.Maintenance) != 0 || len(sec2.Stats) != 1 {
		t.Fatalf("blank note / maintenance error: %+v", sec2)
	}
}
