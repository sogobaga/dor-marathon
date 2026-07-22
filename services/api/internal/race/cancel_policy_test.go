package race

import (
	"testing"
	"time"
)

func TestComputeCancellation_TierBoundaryExact(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// 剛好等於 30 天級距邊界 → 90%
	start30 := now.Add(30 * 24 * time.Hour)
	c := ComputeCancellation(start30, now, 100000, defaultCancellationPolicy)
	if !c.CanCancel || c.DaysBefore != 30 || c.Ratio != 90 || c.RefundAmountCents != 90000 {
		t.Fatalf("30-day boundary: got %+v", c)
	}

	// 剛好等於 14 天級距邊界（也剛好是截止日）→ 50%，仍可取消
	start14 := now.Add(14 * 24 * time.Hour)
	c = ComputeCancellation(start14, now, 100000, defaultCancellationPolicy)
	if !c.CanCancel || c.DaysBefore != 14 || c.Ratio != 50 || c.RefundAmountCents != 50000 {
		t.Fatalf("14-day boundary: got %+v", c)
	}

	// 差一天跌落到 13 天 → 已過截止，不可取消
	start13 := now.Add(13 * 24 * time.Hour)
	c = ComputeCancellation(start13, now, 100000, defaultCancellationPolicy)
	if c.CanCancel || c.DaysBefore != 13 {
		t.Fatalf("13-day (past deadline): got %+v", c)
	}

	// 29 天（不到 30 天門檻，但仍在 14 天門檻之上）→ 50%
	start29 := now.Add(29*24*time.Hour + 12*time.Hour) // 29.5 天 → floor 29
	c = ComputeCancellation(start29, now, 100000, defaultCancellationPolicy)
	if !c.CanCancel || c.DaysBefore != 29 || c.Ratio != 50 {
		t.Fatalf("29-day (between tiers): got %+v", c)
	}
}

func TestComputeCancellation_RaceDay(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	start := now.Add(6 * time.Hour) // 賽事今天稍晚開始，daysBefore=0
	c := ComputeCancellation(start, now, 100000, defaultCancellationPolicy)
	if c.CanCancel {
		t.Fatalf("race day should be blocked (deadline), got %+v", c)
	}
	if c.DaysBefore != 0 {
		t.Fatalf("expected daysBefore=0, got %d", c.DaysBefore)
	}
}

func TestComputeCancellation_PastRace(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) // 已開始一整天
	c := ComputeCancellation(start, now, 100000, defaultCancellationPolicy)
	if c.CanCancel || c.DaysBefore != -1 {
		t.Fatalf("past race: got %+v", c)
	}

	// 賽事已開始但不滿一天（負的小數天）→ floor 仍要落到 -1，不能因為浮點/截斷誤差變成 0
	now2 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c2 := ComputeCancellation(start, now2, 100000, defaultCancellationPolicy)
	if c2.CanCancel || c2.DaysBefore != -1 {
		t.Fatalf("past race by half a day: got %+v", c2)
	}
}

func TestComputeCancellation_NoTiers(t *testing.T) {
	policy := CancellationPolicy{DeadlineDays: 0, Tiers: nil}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	start := now.Add(100 * 24 * time.Hour)
	c := ComputeCancellation(start, now, 100000, policy)
	if !c.CanCancel {
		t.Fatalf("expected cancellable with deadline=0, got %+v", c)
	}
	if c.Ratio != 0 || c.RefundAmountCents != 0 {
		t.Fatalf("expected ratio=0/amount=0 with no tiers, got %+v", c)
	}
}

func TestComputeCancellation_RatioZero(t *testing.T) {
	// deadline 較寬鬆（5 天），但唯一的 tier 門檻是 14 天；daysBefore=10 允許取消但吃不到任何 tier。
	policy := CancellationPolicy{
		DeadlineDays: 5,
		Tiers:        []CancellationTier{{DaysBefore: 14, Ratio: 50}},
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	start := now.Add(10 * 24 * time.Hour)
	c := ComputeCancellation(start, now, 100000, policy)
	if !c.CanCancel {
		t.Fatalf("expected cancellable, got %+v", c)
	}
	if c.Ratio != 0 || c.RefundAmountCents != 0 {
		t.Fatalf("expected ratio=0, got %+v", c)
	}
}

func TestComputeCancellation_AmountDivisionFloors(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	start := now.Add(30 * 24 * time.Hour) // ratio 90%
	c := ComputeCancellation(start, now, 333, defaultCancellationPolicy)
	if c.Ratio != 90 {
		t.Fatalf("expected ratio 90, got %d", c.Ratio)
	}
	// 333 * 90 / 100 = 299.7 → 無條件捨去 299
	if c.RefundAmountCents != 299 {
		t.Fatalf("expected floored 299, got %d", c.RefundAmountCents)
	}

	// 50% 情境下除不盡：333*50/100 = 166.5 → 166
	start14 := now.Add(14 * 24 * time.Hour)
	c2 := ComputeCancellation(start14, now, 333, defaultCancellationPolicy)
	if c2.Ratio != 50 || c2.RefundAmountCents != 166 {
		t.Fatalf("expected ratio 50 / amount 166, got %+v", c2)
	}
}

func TestComputeCancellation_UnpaidOrderZeroAmount(t *testing.T) {
	// 呼叫端對未付款訂單傳入 0 元：即使 ratio>0，退費金額仍應為 0。
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	start := now.Add(30 * 24 * time.Hour)
	c := ComputeCancellation(start, now, 0, defaultCancellationPolicy)
	if !c.CanCancel || c.Ratio != 90 || c.RefundAmountCents != 0 {
		t.Fatalf("unpaid order: got %+v", c)
	}
}

func TestResolveCancellationPolicy(t *testing.T) {
	override := &CancellationPolicy{DeadlineDays: 7, Tiers: []CancellationTier{{DaysBefore: 7, Ratio: 100}}}
	if got := ResolveCancellationPolicy(override, `{"deadline_days":1,"tiers":[]}`); got.DeadlineDays != 7 {
		t.Fatalf("expected override to win, got %+v", got)
	}

	sysJSON := `{"deadline_days":21,"tiers":[{"days_before":21,"ratio":80}]}`
	got := ResolveCancellationPolicy(nil, sysJSON)
	if got.DeadlineDays != 21 || len(got.Tiers) != 1 || got.Tiers[0].Ratio != 80 {
		t.Fatalf("expected system default to be used, got %+v", got)
	}

	// 沒有覆寫、系統設定也查無資料（空字串）或壞掉的 JSON → 回退程式內建預設
	got = ResolveCancellationPolicy(nil, "")
	if got.DeadlineDays != defaultCancellationPolicy.DeadlineDays {
		t.Fatalf("expected builtin default for empty system json, got %+v", got)
	}
	got = ResolveCancellationPolicy(nil, "{not valid json")
	if got.DeadlineDays != defaultCancellationPolicy.DeadlineDays {
		t.Fatalf("expected builtin default for invalid system json, got %+v", got)
	}
}
