package integration

// 去重決策（decideDuplicate，純函式）的三情境單元測試——GA 契約 §2.7／§2.8：
//   ① Strava 先到、直連手錶（COROS MCP）後到 → Strava 列被取代（改標 cross_source_duplicate），新列計入；
//   ② 直連先到、Strava 後到               → Strava 新列標重複，直連列保留；
//   ③ 手機 GPS 比手錶晚停／GPS 後 N 分鐘的獨立跑步 → SQL 時間基準，見 repository_dedup_integration_test.go（真實 Postgres）。
// 另含：跨帳號指紋、App GPS/Terra/其他直連一律先到先贏、混合候選不翻盤。

import (
	"strings"
	"testing"
)

func mcpNew(user string) *NormalizedActivity {
	return &NormalizedActivity{UserID: user, Source: "coros", ExternalID: "mcp:1001", DistanceKm: 10, DurationS: 3000}
}

func stravaNew(user string) *NormalizedActivity {
	return &NormalizedActivity{UserID: user, Source: "strava", ExternalID: "999", DistanceKm: 10, DurationS: 3000}
}

func TestDecideDuplicate_NoCandidates(t *testing.T) {
	if d := decideDuplicate(mcpNew("u1"), nil, nil); d.Flagged || len(d.SupersedeIDs) != 0 {
		t.Fatalf("no candidates → plain insert, got %+v", d)
	}
}

// 情境 ①：Strava 先到，直連後到（時間重疊）→ 新列計入、Strava 列被取代。
func TestDecideDuplicate_StravaFirstDirectLater_SupersedesStrava(t *testing.T) {
	overlap := []dupCandidate{{ID: "strava-row", UserID: "u1", Source: "strava", ExternalID: "999"}}
	d := decideDuplicate(mcpNew("u1"), nil, overlap)
	if d.Flagged || len(d.SupersedeIDs) != 1 || d.SupersedeIDs[0] != "strava-row" {
		t.Fatalf("direct watch must supersede the Strava row, got %+v", d)
	}
	// 指紋完全相同（COROS→Strava 自動同步通常起始秒／距離／移動時間都一樣）也要走同一條路，而不是被標 duplicate
	fp := []dupCandidate{{ID: "strava-row", UserID: "u1", Source: "strava", ExternalID: "999"}}
	d = decideDuplicate(mcpNew("u1"), fp, overlap)
	if d.Flagged || len(d.SupersedeIDs) != 1 || d.SupersedeIDs[0] != "strava-row" {
		t.Fatalf("same fingerprint + same Strava row: still supersede (deduplicated ids), got %+v", d)
	}
	// 多筆 Strava 重疊（分段上傳）→ 全部取代
	two := []dupCandidate{{ID: "s1", UserID: "u1", Source: "strava"}, {ID: "s2", UserID: "u1", Source: "strava"}}
	d = decideDuplicate(mcpNew("u1"), nil, two)
	if d.Flagged || len(d.SupersedeIDs) != 2 {
		t.Fatalf("all overlapping Strava rows are superseded, got %+v", d)
	}
	// Garmin 直連同理
	g := &NormalizedActivity{UserID: "u1", Source: "garmin", ExternalID: "gc:77", DistanceKm: 5, DurationS: 1500}
	d = decideDuplicate(g, nil, overlap)
	if d.Flagged || len(d.SupersedeIDs) != 1 {
		t.Fatalf("garmin direct supersedes Strava too, got %+v", d)
	}
}

// 情境 ②：直連先到、Strava 後到 → Strava 新列標 multi_device_duplicate（dup_of＝直連列），直連列保留。
func TestDecideDuplicate_DirectFirstStravaLater_StravaIsFlagged(t *testing.T) {
	overlap := []dupCandidate{{ID: "mcp-row", UserID: "u1", Source: "coros", ExternalID: "mcp:1001"}}
	d := decideDuplicate(stravaNew("u1"), nil, overlap)
	if !d.Flagged || d.Reason != "multi_device_duplicate" || d.DupOf != "mcp-row" || len(d.SupersedeIDs) != 0 {
		t.Fatalf("Strava arriving after a direct row must be the flagged one, got %+v", d)
	}
	d = decideDuplicate(stravaNew("u1"), []dupCandidate{{ID: "mcp-row", UserID: "u1", Source: "coros", ExternalID: "mcp:1001"}}, nil)
	if !d.Flagged || d.Reason != "duplicate" || d.DupOf != "mcp-row" {
		t.Fatalf("same fingerprint → duplicate, got %+v", d)
	}
}

// 不翻盤的情況：只要命中任一筆「非 Strava」既有列（App GPS、Terra、另一支直連），新直連列照舊標重複。
func TestDecideDuplicate_NoFlipWhenAnyNonStravaCandidate(t *testing.T) {
	for _, other := range []dupCandidate{
		{ID: "gps-row", UserID: "u1", Source: ""},                         // App GPS
		{ID: "terra-row", UserID: "u1", Source: "coros", ExternalID: "t"}, // Terra／Partner 的 coros
		{ID: "garmin-row", UserID: "u1", Source: "garmin", ExternalID: "gc:1"},
	} {
		overlap := []dupCandidate{{ID: "strava-row", UserID: "u1", Source: "strava"}, other}
		d := decideDuplicate(mcpNew("u1"), nil, overlap)
		if !d.Flagged || d.Reason != "multi_device_duplicate" || d.DupOf != other.ID || len(d.SupersedeIDs) != 0 {
			t.Fatalf("non-Strava candidate %q must win (first come first served), got %+v", other.ID, d)
		}
	}
	// 非直連的外部列（Terra 的 coros／Strava／Polar…）永遠先到先贏，不會翻盤 Strava
	terra := &NormalizedActivity{UserID: "u1", Source: "coros", ExternalID: "terra-summary-9"}
	d := decideDuplicate(terra, nil, []dupCandidate{{ID: "strava-row", UserID: "u1", Source: "strava"}})
	if !d.Flagged || len(d.SupersedeIDs) != 0 {
		t.Fatalf("a Terra row is not a direct watch row: it must not supersede Strava, got %+v", d)
	}
	polar := &NormalizedActivity{UserID: "u1", Source: "polar", ExternalID: "p1"}
	if d := decideDuplicate(polar, nil, []dupCandidate{{ID: "strava-row", UserID: "u1", Source: "strava"}}); !d.Flagged {
		t.Fatalf("Polar must not supersede Strava, got %+v", d)
	}
}

// 跨帳號精確指紋：永遠 cross_account_duplicate（洗資料），優先於其他判斷；直連列也不例外。
func TestDecideDuplicate_CrossAccountFingerprintAlwaysWins(t *testing.T) {
	fp := []dupCandidate{
		{ID: "mine-strava", UserID: "u1", Source: "strava"},
		{ID: "theirs", UserID: "u2", Source: "coros", ExternalID: "mcp:5"},
	}
	d := decideDuplicate(mcpNew("u1"), fp, nil)
	if !d.Flagged || d.Reason != "cross_account_duplicate" || d.DupOf != "theirs" || len(d.SupersedeIDs) != 0 {
		t.Fatalf("cross-account copy must be flagged and never supersede anything, got %+v", d)
	}
	// 重疊候選若（防禦性）屬於別人：忽略
	d = decideDuplicate(mcpNew("u1"), nil, []dupCandidate{{ID: "x", UserID: "u2", Source: "strava"}})
	if d.Flagged || len(d.SupersedeIDs) != 0 {
		t.Fatalf("other users' rows are never overlap candidates, got %+v", d)
	}
}

func TestDecideDuplicate_NonDirectAllStravaOverlapStaysFirstComeFirstServed(t *testing.T) {
	// Strava 新列 vs 既有 Strava 列（同一趟重複 webhook 以外的重疊）：標重複
	d := decideDuplicate(stravaNew("u1"), nil, []dupCandidate{{ID: "old-strava", UserID: "u1", Source: "strava"}})
	if !d.Flagged || d.Reason != "multi_device_duplicate" || d.DupOf != "old-strava" {
		t.Fatalf("got %+v", d)
	}
}

// §2.8：時間基準修正的 SQL 文字守門（真實 Postgres 行為見 integration 測試）。
func TestOverlapCandidatesSQL_UsesStartTimeBasisForGpsRows(t *testing.T) {
	if !strings.Contains(overlapCandidatesSQL, "CASE WHEN source IS NULL THEN recorded_at - make_interval(secs => duration_s) ELSE recorded_at END") {
		t.Error("candidate start must be recorded_at - duration for App GPS rows (recorded_at is the END time there)")
	}
	if !strings.Contains(overlapCandidatesSQL, "CASE WHEN source IS NULL THEN recorded_at ELSE recorded_at + make_interval(secs => duration_s) END") {
		t.Error("candidate end must be recorded_at for App GPS rows")
	}
	if strings.Contains(overlapCandidatesSQL, "duration_s || ' seconds'") {
		t.Error("the old start-time-only overlap predicate must be gone")
	}
}
