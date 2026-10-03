package profile

// 跨來源去重排序（crossSourceRankSQL）：api 端（reResolveUser）與 worker（resolveCrossSourceDups）必須逐字一致
// （兩個 Go module，無法共用程式碼；改一邊漏一邊會讓兩邊對同一對重疊活動做出相反裁決）。
// 這裡讀 worker 原始碼比對，並以 Go 模型驗證優先序語意（直連手錶勝過 Strava、即使使用者偏好 Strava）。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dor/api/internal/integration"
)

var sqlLineComment = regexp.MustCompile(`--[^\n]*`)

// normSQL 去 SQL 行註解、壓縮空白，方便逐字比較。
func normSQL(s string) string {
	s = sqlLineComment.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

func TestCrossSourceRankSQLMatchesWorker(t *testing.T) {
	b, err := os.ReadFile(filepath.FromSlash("../../../worker/main.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	start := strings.Index(src, "CASE\n\t\t\t\t\tWHEN a.source IS NULL THEN 0")
	if start < 0 {
		t.Fatal("cannot find the rank CASE in services/worker/main.go resolveCrossSourceDups")
	}
	end := strings.Index(src[start:], "ELSE 10 END")
	if end < 0 {
		t.Fatal("worker rank CASE must end with ELSE 10 END")
	}
	workerCase := src[start : start+end+len("ELSE 10 END")]
	apiCase := crossSourceRankSQL("COALESCE(p.src,'')")
	if normSQL(workerCase) != normSQL(apiCase) {
		t.Fatalf("worker and api rank CASE diverged:\nworker: %s\napi:    %s", normSQL(workerCase), normSQL(apiCase))
	}
	// reResolveUser 真的用這個函式產生的 CASE
	d, _ := os.ReadFile("dedup.go")
	if !strings.Contains(string(d), `crossSourceRankSQL("$2")`) {
		t.Error("reResolveUser must build its rank from crossSourceRankSQL")
	}
	// 直連片語＝integration.DirectWatchSQL("a")
	if !strings.Contains(apiCase, integration.DirectWatchSQL("a")) {
		t.Error("rank CASE must embed integration.DirectWatchSQL(\"a\")")
	}
}

// rankOf 以 Go 重現 SQL CASE 的語意（只用來驗證「優先序設計」，SQL 本身由整合測試在真實 Postgres 驗證）。
func rankOf(source, externalID, pref string) int {
	switch {
	case source == "":
		return 0
	case source == pref && source != "strava":
		return 1
	case integration.IsDirectWatch(source, externalID):
		return 2
	case source == pref:
		return 3
	}
	switch source {
	case "garmin":
		return 4
	case "coros":
		return 5
	case "polar":
		return 6
	case "suunto":
		return 7
	case "wahoo":
		return 8
	case "strava":
		return 9
	}
	return 10
}

func TestCrossSourceRankSemantics(t *testing.T) {
	type row struct{ src, ext string }
	gps, mcp, stravaRow := row{"", ""}, row{"coros", "mcp:1"}, row{"strava", "9"}
	terraCoros, terraGarmin, gc := row{"coros", "terra-1"}, row{"garmin", "terra-2"}, row{"garmin", "gc:7"}
	r := func(x row, pref string) int { return rankOf(x.src, x.ext, pref) }

	for _, pref := range []string{"gps", "", "strava", "coros", "garmin", "polar"} {
		if r(gps, pref) != 0 {
			t.Errorf("pref=%q: App GPS must always be rank 0", pref)
		}
		// 直連手錶勝過 Strava——即使使用者偏好 Strava（GA 契約 §2.7）
		if !(r(mcp, pref) < r(stravaRow, pref)) {
			t.Errorf("pref=%q: COROS MCP (%d) must outrank Strava (%d)", pref, r(mcp, pref), r(stravaRow, pref))
		}
		if !(r(gc, pref) < r(stravaRow, pref)) {
			t.Errorf("pref=%q: Garmin direct (%d) must outrank Strava (%d)", pref, r(gc, pref), r(stravaRow, pref))
		}
		// Terra／Partner 的 coros／garmin 仍低於直連（同品牌直連較權威），高於 Strava（除非偏好）
		if r(terraCoros, pref) <= r(mcp, pref) && pref != "coros" {
			t.Errorf("pref=%q: a Terra coros row must not outrank the direct COROS row", pref)
		}
	}
	// 使用者偏好 Strava：Strava 仍勝過其餘 Terra 來源（舊行為不變），但輸給直連
	if !(r(stravaRow, "strava") < r(terraGarmin, "strava")) {
		t.Error("preferred Strava still beats Terra-sourced rows")
	}
	if !(r(mcp, "strava") < r(stravaRow, "strava")) {
		t.Error("…but loses to a direct watch row")
	}
	// 偏好 garmin（Terra）：偏好來源仍最優先（僅次於 GPS）
	if r(terraGarmin, "garmin") != 1 || !(r(terraGarmin, "garmin") < r(mcp, "garmin")) {
		t.Error("preferred non-Strava source keeps rank 1, above direct rows")
	}
	// 沒有偏好：garmin > coros > polar > suunto > wahoo > strava
	order := []string{"garmin", "coros", "polar", "suunto", "wahoo", "strava"}
	for i := 0; i+1 < len(order); i++ {
		if !(rankOf(order[i], "x", "gps") < rankOf(order[i+1], "x", "gps")) {
			t.Errorf("%s must outrank %s", order[i], order[i+1])
		}
	}
	if rankOf("mystery", "x", "gps") != 10 {
		t.Error("unknown source → rank 10")
	}
}
