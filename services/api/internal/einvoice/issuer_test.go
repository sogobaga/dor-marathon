package einvoice

import (
	"testing"
	"time"
)

func mustParseTaipei(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse date %q: %v", s, err)
	}
	return tm
}

func TestDecideSkip_ZeroAmount(t *testing.T) {
	in := decideSkipInput{
		TotalCents: 0, CfgEnv: "stage", AutoIssueOn: true, IssueSince: "2026-01-01",
		PaidAtTaipei: mustParseTaipei(t, "2026-09-10"),
	}
	skip, reason := decideSkip(in)
	if !skip || reason != "zero_amount" {
		t.Errorf("expected skip=zero_amount, got skip=%v reason=%q", skip, reason)
	}
}

func TestDecideSkip_VirtualUser(t *testing.T) {
	in := decideSkipInput{
		TotalCents: 10000, IsVirtual: true, CfgEnv: "stage", AutoIssueOn: true, IssueSince: "2026-01-01",
		PaidAtTaipei: mustParseTaipei(t, "2026-09-10"),
	}
	skip, reason := decideSkip(in)
	if !skip || reason != "virtual_user" {
		t.Errorf("expected skip=virtual_user, got skip=%v reason=%q", skip, reason)
	}
}

func TestDecideSkip_TestMoney_ProdCfgStagePaid(t *testing.T) {
	in := decideSkipInput{
		TotalCents: 10000, CfgEnv: "prod", PaidTxEnv: "stage", AutoIssueOn: true, IssueSince: "2026-01-01",
		PaidAtTaipei: mustParseTaipei(t, "2026-09-10"),
	}
	skip, reason := decideSkip(in)
	if !skip || reason != "test_money" {
		t.Errorf("expected skip=test_money, got skip=%v reason=%q", skip, reason)
	}
}

func TestDecideSkip_TestMoney_ProdCfgEmptyPaidTxEnv(t *testing.T) {
	// 查無任何 paid 交易紀錄（例如後台人工標記付款）在正式發票環境下同樣視為不可信。
	in := decideSkipInput{
		TotalCents: 10000, CfgEnv: "prod", PaidTxEnv: "", AutoIssueOn: true, IssueSince: "2026-01-01",
		PaidAtTaipei: mustParseTaipei(t, "2026-09-10"),
	}
	skip, reason := decideSkip(in)
	if !skip || reason != "test_money" {
		t.Errorf("expected skip=test_money, got skip=%v reason=%q", skip, reason)
	}
}

func TestDecideSkip_TestMoney_StageCfgAllowsAnyPaidEnv(t *testing.T) {
	// 發票環境本身是 stage 時，不檢查 PaidTxEnv（測試環境本來就都是測試金流）。
	in := decideSkipInput{
		TotalCents: 10000, CfgEnv: "stage", PaidTxEnv: "", AutoIssueOn: true, IssueSince: "2026-01-01",
		PaidAtTaipei: mustParseTaipei(t, "2026-09-10"),
	}
	skip, _ := decideSkip(in)
	if skip {
		t.Errorf("expected no skip when CfgEnv=stage regardless of PaidTxEnv")
	}
}

func TestDecideSkip_ManualBypassesAutoIssueAndSince(t *testing.T) {
	in := decideSkipInput{
		TotalCents: 10000, CfgEnv: "stage", Manual: true,
		AutoIssueOn: false, IssueSince: "2099-01-01", // 這兩項若非 manual 皆會導致略過
		PaidAtTaipei: mustParseTaipei(t, "2020-01-01"),
	}
	skip, reason := decideSkip(in)
	if skip {
		t.Errorf("expected manual to bypass auto_issue/since checks, got skip=%v reason=%q", skip, reason)
	}
}

func TestDecideSkip_ManualStillBlocksHardRules(t *testing.T) {
	cases := []struct {
		name string
		in   decideSkipInput
		want string
	}{
		{"zero_amount", decideSkipInput{TotalCents: 0, Manual: true, CfgEnv: "stage"}, "zero_amount"},
		{"virtual_user", decideSkipInput{TotalCents: 10000, IsVirtual: true, Manual: true, CfgEnv: "stage"}, "virtual_user"},
		{"test_money", decideSkipInput{TotalCents: 10000, Manual: true, CfgEnv: "prod", PaidTxEnv: "stage"}, "test_money"},
	}
	for _, c := range cases {
		skip, reason := decideSkip(c.in)
		if !skip || reason != c.want {
			t.Errorf("%s: expected skip=%s even when manual, got skip=%v reason=%q", c.name, c.want, skip, reason)
		}
	}
}

func TestDecideSkip_AutoIssueOff(t *testing.T) {
	in := decideSkipInput{
		TotalCents: 10000, CfgEnv: "stage", AutoIssueOn: false, IssueSince: "2020-01-01",
		PaidAtTaipei: mustParseTaipei(t, "2026-09-10"),
	}
	skip, reason := decideSkip(in)
	if !skip || reason != "auto_issue_off" {
		t.Errorf("expected skip=auto_issue_off, got skip=%v reason=%q", skip, reason)
	}
}

func TestDecideSkip_BeforeIssueSince(t *testing.T) {
	in := decideSkipInput{
		TotalCents: 10000, CfgEnv: "stage", AutoIssueOn: true, IssueSince: "2026-09-07",
		PaidAtTaipei: mustParseTaipei(t, "2026-09-06"),
	}
	skip, reason := decideSkip(in)
	if !skip || reason != "before_issue_since" {
		t.Errorf("expected skip=before_issue_since, got skip=%v reason=%q", skip, reason)
	}
}

func TestDecideSkip_OnOrAfterIssueSinceProceeds(t *testing.T) {
	in := decideSkipInput{
		TotalCents: 10000, CfgEnv: "stage", AutoIssueOn: true, IssueSince: "2026-09-07",
		PaidAtTaipei: mustParseTaipei(t, "2026-09-07"),
	}
	skip, reason := decideSkip(in)
	if skip {
		t.Errorf("expected no skip when paid date == issue_since, got reason=%q", reason)
	}
}

func TestDecideSkip_EmptyIssueSinceFallsBackToDefault(t *testing.T) {
	// IssueSince 空字串（呼叫端理論上已代入預設值，但純函式自身也要 defensive）：用 defaultIssueSince。
	before := decideSkipInput{
		TotalCents: 10000, CfgEnv: "stage", AutoIssueOn: true, IssueSince: "",
		PaidAtTaipei: mustParseTaipei(t, defaultIssueSince).AddDate(0, 0, -1),
	}
	skip, reason := decideSkip(before)
	if !skip || reason != "before_issue_since" {
		t.Errorf("expected before_issue_since using default, got skip=%v reason=%q", skip, reason)
	}

	onDefault := decideSkipInput{
		TotalCents: 10000, CfgEnv: "stage", AutoIssueOn: true, IssueSince: "",
		PaidAtTaipei: mustParseTaipei(t, defaultIssueSince),
	}
	skip2, _ := decideSkip(onDefault)
	if skip2 {
		t.Errorf("expected no skip when paid date == default issue_since")
	}
}

func TestDecideSkip_MalformedIssueSinceFallsBackToDefault(t *testing.T) {
	in := decideSkipInput{
		TotalCents: 10000, CfgEnv: "stage", AutoIssueOn: true, IssueSince: "not-a-date",
		PaidAtTaipei: mustParseTaipei(t, defaultIssueSince),
	}
	skip, reason := decideSkip(in)
	if skip {
		t.Errorf("expected malformed IssueSince to fall back to default and not skip, got reason=%q", reason)
	}
}

func TestBackoffSchedule_HasEnoughEntriesForMaxAttempts(t *testing.T) {
	if len(backoffSchedule) != maxAttempts-1 {
		t.Errorf("expected %d backoff entries for maxAttempts=%d (no wait needed after final failure), got %d",
			maxAttempts-1, maxAttempts, len(backoffSchedule))
	}
}

// TestDecideSkip_StageEnvRealMoney 發票環境 stage 時，真錢（prod 付款）訂單一律不開測試發票（手動也不行），
// 原因為 stage_env_real_money；stage 付款的測試單則照常可開。
func TestDecideSkip_StageEnvRealMoney(t *testing.T) {
	for _, manual := range []bool{false, true} {
		skip, reason := decideSkip(decideSkipInput{TotalCents: 10000, CfgEnv: "stage", PaidTxEnv: "prod", Manual: manual, AutoIssueOn: true, IssueSince: "2020-01-01", PaidAtTaipei: time.Now()})
		if !skip || reason != "stage_env_real_money" {
			t.Errorf("manual=%v: expected skip=stage_env_real_money, got skip=%v reason=%q", manual, skip, reason)
		}
	}
	skip, reason := decideSkip(decideSkipInput{TotalCents: 10000, CfgEnv: "stage", PaidTxEnv: "stage", Manual: false, AutoIssueOn: true, IssueSince: "2020-01-01", PaidAtTaipei: time.Now()})
	if skip {
		t.Errorf("stage env + stage money should issue, got skip reason=%q", reason)
	}
}
