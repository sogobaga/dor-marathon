package live

import (
	"testing"
	"time"
)

// 擁有者 2026-10-02 拍板：≤20 人 5 s、21–35 人 7 s、36–50 人 10 s。
func TestIntervalLadder(t *testing.T) {
	cases := []struct{ live, want int }{
		{0, 5000}, {1, 5000}, {20, 5000},
		{21, 7000}, {35, 7000},
		{36, 10000}, {50, 10000}, {200, 10000},
	}
	for _, c := range cases {
		if got := IntervalMs(c.live); got != c.want {
			t.Errorf("IntervalMs(%d) = %d, want %d", c.live, got, c.want)
		}
	}
}

// 審查 M3：fade = max(3·iv, 30s)、gray = max(8·iv, 75s)、drop = 180s。
func TestStaleThresholds(t *testing.T) {
	cases := []struct {
		iv   int
		want Stale
	}{
		{5000, Stale{FadeS: 30, GrayS: 75, DropS: 180}},  // 3·5=15→30、8·5=40→75
		{7000, Stale{FadeS: 30, GrayS: 75, DropS: 180}},  // 3·7=21→30、8·7=56→75
		{10000, Stale{FadeS: 30, GrayS: 80, DropS: 180}}, // 3·10=30、8·10=80
		{20000, Stale{FadeS: 60, GrayS: 160, DropS: 180}},
	}
	for _, c := range cases {
		if got := StaleFor(c.iv); got != c.want {
			t.Errorf("StaleFor(%d) = %+v, want %+v", c.iv, got, c.want)
		}
	}
}

func TestWindow(t *testing.T) {
	meetAt := time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC)
	s := DefaultSettings()

	// ends_at 為 NULL：closes = meet_at + 3h + 30m；opens = meet_at − 30m
	opens, closes := s.Window(meetAt, nil)
	if want := meetAt.Add(-30 * time.Minute); !opens.Equal(want) {
		t.Errorf("opens = %v, want %v", opens, want)
	}
	if want := meetAt.Add(3*time.Hour + 30*time.Minute); !closes.Equal(want) {
		t.Errorf("closes(ends_at=NULL) = %v, want %v", closes, want)
	}

	// 有 ends_at：closes = ends_at + 30m（不再用 default_hours）
	ends := meetAt.Add(90 * time.Minute)
	_, closes = s.Window(meetAt, &ends)
	if want := ends.Add(30 * time.Minute); !closes.Equal(want) {
		t.Errorf("closes(ends_at) = %v, want %v", closes, want)
	}

	// 自訂設定
	s = Settings{PreMinutes: 10, DefaultHours: 2, GraceMinutes: 5, Max: 50, EntryState: "open"}
	opens, closes = s.Window(meetAt, nil)
	if !opens.Equal(meetAt.Add(-10*time.Minute)) || !closes.Equal(meetAt.Add(2*time.Hour+5*time.Minute)) {
		t.Errorf("custom window = [%v, %v)", opens, closes)
	}
}

// 起點含、終點不含。
func TestInWindowBoundaries(t *testing.T) {
	opens := time.Date(2026, 10, 10, 5, 30, 0, 0, time.UTC)
	closes := time.Date(2026, 10, 10, 9, 30, 0, 0, time.UTC)
	cases := []struct {
		now  time.Time
		want bool
	}{
		{opens.Add(-time.Millisecond), false},
		{opens, true},
		{opens.Add(time.Hour), true},
		{closes.Add(-time.Millisecond), true},
		{closes, false},
		{closes.Add(time.Hour), false},
	}
	for _, c := range cases {
		if got := InWindow(c.now, opens, closes); got != c.want {
			t.Errorf("InWindow(%v) = %v, want %v", c.now, got, c.want)
		}
	}
}

// 讀取端第二道保險：壞值夾回安全範圍。
func TestSettingsNormalize(t *testing.T) {
	bad := Settings{EntryState: "", Max: 0, PreMinutes: -5, DefaultHours: 0, GraceMinutes: -1}.Normalize()
	d := DefaultSettings()
	if bad != d {
		t.Errorf("Normalize(bad) = %+v, want defaults %+v", bad, d)
	}
	if got := (Settings{Max: 100000}).Normalize().Max; got != MaxLiveCeiling {
		t.Errorf("Max above ceiling = %d, want %d", got, MaxLiveCeiling)
	}
	// 合法值原樣保留（含 pre/grace = 0）
	ok := Settings{EntryState: "open", Max: 20, PreMinutes: 0, DefaultHours: 5, GraceMinutes: 0}.Normalize()
	if ok.Max != 20 || ok.PreMinutes != 0 || ok.DefaultHours != 5 || ok.GraceMinutes != 0 || ok.EntryState != "open" {
		t.Errorf("Normalize(valid) changed values: %+v", ok)
	}
}

// 契約 §4 的常數：grant 15 分鐘、墓碑 15 分鐘、pos／hb 4 分鐘、地板 1.5 s、重驗提前 5 分鐘。
func TestProtocolConstants(t *testing.T) {
	if GrantTTL != 15*time.Minute || TombstoneTTL != 15*time.Minute || PosTTL != 4*time.Minute ||
		KeyTTL != 24*time.Hour || FloorMs != 1500 || ReauthMargin != 5*time.Minute || DropS != 180 ||
		ProtocolVersion != 1 || MaxBodyBytes != 512 {
		t.Fatal("protocol constants drifted from the contract")
	}
	if got := ReauthInS(1_000_000+900_000, 1_000_000); got != 600 {
		t.Errorf("ReauthInS(fresh grant) = %d, want 600", got)
	}
	if got := ReauthInS(1_000_000+200_000, 1_000_000); got != 0 {
		t.Errorf("ReauthInS(<5min left) = %d, want 0", got)
	}
	if got := ReauthInS(1_000_000-1, 1_000_000); got != 0 {
		t.Errorf("ReauthInS(expired) = %d, want 0", got)
	}
}

func TestValidSID(t *testing.T) {
	good := []string{"abcdefghijklmnop", "ABCDEFGHIJKLMNOP0123456789abcdef", "0123456789ABCDEF"}
	bad := []string{
		"", "short", "abcdefghijklmno", // 15 字元
		"abcdefghijklmnopqrstuvwxyz0123456", // 33 字元
		"abcdefghijklmn|p", "abcdefghijklmn p", "abcdefghijklmn-p", "abcdefghijklmn_p",
		"abcdefghijklmn\n\np", "abcdefghijklmn跑p",
	}
	for _, s := range good {
		if !ValidSID(s) {
			t.Errorf("ValidSID(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if ValidSID(s) {
			t.Errorf("ValidSID(%q) = true, want false", s)
		}
	}
}
