package integration

// 直連手錶判定（directwatch.go）：函式、SQL 片語，以及「同一份定義被各處逐字使用」的守門測試——
// gpscalib、worker 的去重排序、profile 的去重排序、DeleteProviderActivities 都不能各寫各的。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsDirectWatch(t *testing.T) {
	cases := []struct {
		source, ext string
		want        bool
	}{
		{"coros", "mcp:480000000000000009", true},
		{"garmin", "gc:abc123", true},
		{"coros", "480000000000000009", false}, // COROS Partner：純 labelId
		{"coros", "terra-summary-1", false},    // Terra
		{"garmin", "terra-summary-2", false},
		{"coros", "", false},
		{"coros", "MCP:123", false}, // 前綴區分大小寫（程式只會寫小寫）
		{"strava", "mcp:123", false},
		{"strava", "gc:123", false},
		{"polar", "mcp:1", false},
		{"", "mcp:1", false},
		{"garmin", "mcp:1", false}, // 前綴必須與品牌配對
		{"coros", "gc:1", false},
	}
	for _, c := range cases {
		if got := IsDirectWatch(c.source, c.ext); got != c.want {
			t.Errorf("IsDirectWatch(%q,%q) = %v, want %v", c.source, c.ext, got, c.want)
		}
	}
}

func TestDirectWatchSQL(t *testing.T) {
	want := `((a.source='coros' AND COALESCE(a.external_id,'') LIKE 'mcp:%') OR (a.source='garmin' AND COALESCE(a.external_id,'') LIKE 'gc:%'))`
	if got := DirectWatchSQL("a"); got != want {
		t.Fatalf("DirectWatchSQL(\"a\") =\n%s\nwant\n%s", got, want)
	}
	if got := DirectWatchSQL(""); !strings.HasPrefix(got, "((source='coros'") || strings.Contains(got, "a.") {
		t.Fatalf("empty alias must not add a prefix: %s", got)
	}
	// 整段已加括號：接在 NOT 後面不會因優先序改變語意
	if got := DirectWatchSQL("x"); !strings.HasPrefix(got, "(") || !strings.HasSuffix(got, ")") {
		t.Fatalf("must be fully parenthesised: %s", got)
	}
	for _, bad := range []string{"a b", "a;drop", "1a", "a.b", "a'"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("alias %q must panic (programmer error, never silently produce SQL)", bad)
				}
			}()
			DirectWatchSQL(bad)
		}()
	}
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// 同一個片語出現在：gpscalib 候選、worker 去重排序、api 去重排序（crossSourceRankSQL 由 profile 的測試守）。
func TestDirectWatchPredicateIsTheSameEverywhere(t *testing.T) {
	pred := DirectWatchSQL("a")
	gpscalib := readRepoFile(t, "../gpscalib/service.go")
	if !strings.Contains(gpscalib, "AND NOT "+pred) {
		t.Errorf("gpscalib candidateSQL must exclude direct-watch rows with exactly %q", pred)
	}
	worker := readRepoFile(t, "../../../worker/main.go")
	if !strings.Contains(worker, "WHEN "+pred+" THEN 2") {
		t.Errorf("worker resolveCrossSourceDups must rank direct-watch rows with exactly %q", pred)
	}
	// DeleteProviderActivities 兩個語句都走 DirectWatchSQL("")（不得再有手寫的 coros mcp 排除）
	repo := readRepoFile(t, "repository.go")
	if strings.Count(repo, `DirectWatchSQL("")`) < 2 {
		t.Error("DeleteProviderActivities must use DirectWatchSQL(\"\") in both statements")
	}
	if strings.Contains(repo, "AND NOT (source = 'coros' AND COALESCE(external_id,'') LIKE 'mcp:%')") {
		t.Error("repository.go still has a hand-written coros-only exclusion")
	}
}
