package race

import (
	"strings"
	"testing"
	"time"
)

// --- isEntryRewardWindowActive（鏡射 ListEntryRewardActiveRaces 的 SQL WHERE 條件，見該函式註解） ---

func TestIsEntryRewardWindowActive_BeforeStart(t *testing.T) {
	now := mustAt(t, "2026-08-01T00:00:00")
	start := mustAt(t, "2026-08-10T00:00:00")
	end := mustAt(t, "2026-08-20T00:00:00")
	if isEntryRewardWindowActive(now, start, end, 7) {
		t.Fatal("expected false before start_date (race hasn't started yet)")
	}
}

func TestIsEntryRewardWindowActive_ExactlyAtStart(t *testing.T) {
	start := mustAt(t, "2026-08-10T00:00:00")
	end := mustAt(t, "2026-08-20T00:00:00")
	// now == start_date：SQL 用 start_date<=NOW()，含端點
	if !isEntryRewardWindowActive(start, start, end, 7) {
		t.Fatal("expected true at exactly start_date (SQL condition is inclusive: start_date<=NOW())")
	}
}

func TestIsEntryRewardWindowActive_DuringRace(t *testing.T) {
	now := mustAt(t, "2026-08-15T00:00:00")
	start := mustAt(t, "2026-08-10T00:00:00")
	end := mustAt(t, "2026-08-20T00:00:00")
	if !isEntryRewardWindowActive(now, start, end, 7) {
		t.Fatal("expected true during race window")
	}
}

func TestIsEntryRewardWindowActive_WithinGraceWindow(t *testing.T) {
	start := mustAt(t, "2026-08-10T00:00:00")
	end := mustAt(t, "2026-08-20T00:00:00")
	// end + 3 天，寬限期 7 天內
	now := end.AddDate(0, 0, 3)
	if !isEntryRewardWindowActive(now, start, end, 7) {
		t.Fatal("expected true within grace window (end_date+3d, grace=7d)")
	}
}

func TestIsEntryRewardWindowActive_ExactlyAtGraceCutoff(t *testing.T) {
	start := mustAt(t, "2026-08-10T00:00:00")
	end := mustAt(t, "2026-08-20T00:00:00")
	// now == end_date + graceDays：SQL 用 end_date>=NOW()-graceDays天，即 NOW()<=end_date+graceDays天，含端點
	now := end.AddDate(0, 0, 7)
	if !isEntryRewardWindowActive(now, start, end, 7) {
		t.Fatal("expected true exactly at grace cutoff (inclusive boundary)")
	}
}

func TestIsEntryRewardWindowActive_PastGraceWindow(t *testing.T) {
	start := mustAt(t, "2026-08-10T00:00:00")
	end := mustAt(t, "2026-08-20T00:00:00")
	// end + 8 天，超過寬限期 7 天
	now := end.AddDate(0, 0, 8)
	if isEntryRewardWindowActive(now, start, end, 7) {
		t.Fatal("expected false past grace window (end_date+8d, grace=7d)")
	}
}

func TestIsEntryRewardWindowActive_ZeroGraceDays(t *testing.T) {
	start := mustAt(t, "2026-08-10T00:00:00")
	end := mustAt(t, "2026-08-20T00:00:00")
	if !isEntryRewardWindowActive(end, start, end, 0) {
		t.Fatal("expected true exactly at end_date with 0 grace days")
	}
	if isEntryRewardWindowActive(end.Add(time.Second), start, end, 0) {
		t.Fatal("expected false 1s after end_date with 0 grace days")
	}
}

// --- entryRewardMail（純文案函式） ---

func TestEntryRewardMail_ContainsRaceTitle(t *testing.T) {
	title, body := entryRewardMail("英雄馬拉松 2026")
	if !strings.Contains(title, "參賽獎勵") {
		t.Fatalf("expected title to mention 參賽獎勵, got %q", title)
	}
	if !strings.Contains(body, "英雄馬拉松 2026") {
		t.Fatalf("expected body to contain race title, got %q", body)
	}
}
