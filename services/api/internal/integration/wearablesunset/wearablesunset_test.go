package wearablesunset

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := map[string]string{
		"":                 StateOff,
		"off":              StateOff,
		"announce":         StateAnnounce,
		"  announce  ":     StateAnnounce, // 前後空白
		"ANNOUNCE":         StateAnnounce, // 大小寫
		"Announce":         StateAnnounce,
		"closed":           StateOff, // 本版沒有 closed：未知值＝off，不改變現狀
		"vip":              StateOff,
		"whitelist":        StateOff,
		"hidden":           StateOff,
		"announce,closed":  StateOff,
		"announced":        StateOff,
		"1":                StateOff,
		"true":             StateOff,
		"on":               StateOff,
		"announce\nclosed": StateOff,
	}
	for in, want := range cases {
		if got := Parse(in); got != want {
			t.Errorf("Parse(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDate(t *testing.T) {
	cases := map[string]string{
		"":                    "2026-10-31", // 缺鍵＝預設
		"   ":                 "2026-10-31",
		"2026-10-31":          "2026-10-31",
		" 2026-12-01 ":        "2026-12-01",
		"2026-02-30":          "2026-10-31", // 不存在的曆日
		"2026-10-32":          "2026-10-31",
		"2026/10/31":          "2026-10-31",
		"20261031":            "2026-10-31",
		"2026-1-5":            "2026-10-31", // 沒補零不是正規形式
		"tomorrow":            "2026-10-31",
		"2026-10-31T00:00:00": "2026-10-31",
	}
	for in, want := range cases {
		if got := ParseDate(in); got != want {
			t.Errorf("ParseDate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDateLabel(t *testing.T) {
	cases := map[string]string{
		"2026-10-31": "10 月 31 日",
		"2026-01-05": "1 月 5 日", // 月日不補零
		"2026-12-01": "12 月 1 日",
		"":           "",
		"nope":       "",
		"2026-13-01": "",
	}
	for in, want := range cases {
		if got := DateLabel(in); got != want {
			t.Errorf("DateLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInfoJSONShape(t *testing.T) {
	off, _ := json.Marshal(Info{State: StateOff})
	if string(off) != `{"state":"off"}` {
		t.Errorf("off JSON = %s, want only the state (date omitted)", off)
	}
	ann, _ := json.Marshal(Info{State: StateAnnounce, Date: "2026-10-31"})
	if string(ann) != `{"state":"announce","date":"2026-10-31"}` {
		t.Errorf("announce JSON = %s", ann)
	}
}

func TestInfoMessageAndErrorBody(t *testing.T) {
	i := Info{State: StateAnnounce, Date: "2026-10-31"}
	if !i.Announcing() || (Info{State: StateOff}).Announcing() || (Info{}).Announcing() {
		t.Fatal("Announcing must be true only for the announce state")
	}
	if !strings.Contains(i.Message(), "10 月 31 日結束") || !strings.Contains(i.Message(), "不再開放新的連接") {
		t.Errorf("message = %q", i.Message())
	}
	// 日期不合法（理論上不會發生）也要給得出一句通順的話
	if m := (Info{State: StateAnnounce}).Message(); !strings.Contains(m, "即將結束") || strings.Contains(m, "  ") {
		t.Errorf("message without a date = %q", m)
	}
	b := i.ErrorBody()
	if b["code"] != ErrorCode || b["state"] != "announce" || b["date"] != "2026-10-31" || b["error"] != i.Message() {
		t.Errorf("error body = %#v", b)
	}
	// 這兩個字面值是與前台（lib/wearableSunset.ts）的契約，不得隨手改；verify-wearable-sunset.mjs 會與本檔對拍。
	if ErrorCode != "wearable_sunset" {
		t.Errorf("ErrorCode = %q", ErrorCode)
	}
	if ResultValue != "sunset" {
		t.Errorf("ResultValue = %q (the browser redirect value the frontend maps to a fixed message)", ResultValue)
	}
}

// 被擋下的 API 回應狀態碼是與前台 SUNSET_ERROR_STATUS 的契約（scripts/verify-wearable-sunset.mjs 會對拍），而且必須是
// 4xx 但不是 401／403／404：middleware/ipdaily.go 把 401／403 計成該 IP 的登入失敗（每日 08:00 報告會把 ≥20 次的 IP 列為
// 「異常登入失敗」，共用 NAT 的 IP 會誤報）、404 計成 not_found；5xx 則會進「API 5xx 激增」告警。
// （middleware 套件另有一支測試用真的聚合器確認這個狀態碼不被計入。）
func TestRefusalStatusContract(t *testing.T) {
	if RefusalStatus != http.StatusConflict {
		t.Errorf("RefusalStatus = %d, want 409 (the frontend SUNSET_ERROR_STATUS must change with it)", RefusalStatus)
	}
	for _, bad := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		if RefusalStatus == bad {
			t.Errorf("RefusalStatus must not be %d: the daily security report counts it as a login failure / not-found", bad)
		}
	}
	if RefusalStatus < 400 || RefusalStatus >= 500 {
		t.Errorf("RefusalStatus = %d must be a 4xx (5xx trips the API 5xx surge alert)", RefusalStatus)
	}
}

// 對外文案守則（擁有者規定）：繁體中文；絕不提第三方裝置品牌 Garmin／佳明；不用「跑團」二字；不暗示任何品牌背書。
func TestUserVisibleCopyRules(t *testing.T) {
	banned := regexp.MustCompile(`(?i)garmin|佳明|跑團|官方合作|認證|贊助|背書`)
	for _, info := range []Info{{State: StateAnnounce, Date: "2026-10-31"}, {State: StateAnnounce}, {State: StateAnnounce, Date: "bad"}} {
		for _, s := range append([]string{info.Message()}, info.ErrorBody()["error"]) {
			if banned.MatchString(s) {
				t.Errorf("user-visible copy %q contains a banned word", s)
			}
		}
	}
}

func TestFixedNormalizes(t *testing.T) {
	ctx := context.Background()
	if got := Fixed(Info{State: "ANNOUNCE", Date: "2026-11-02"}).Current(ctx); got != (Info{State: StateAnnounce, Date: "2026-11-02"}) {
		t.Errorf("Fixed(announce) = %+v", got)
	}
	if got := Fixed(Info{State: StateAnnounce}).Current(ctx); got.Date != "2026-10-31" {
		t.Errorf("announce without date must get the default end date, got %+v", got)
	}
	// off 一律不帶日期；未知狀態＝off
	for _, in := range []Info{{}, {State: "off", Date: "2026-11-02"}, {State: "closed", Date: "2026-11-02"}} {
		if got := Fixed(in).Current(ctx); got != (Info{State: StateOff}) {
			t.Errorf("Fixed(%+v) = %+v, want plain off", in, got)
		}
	}
}

// 不帶資料庫（單元測試的 repo=nil）時必須恆 off 且不得 panic。
func TestFromSettingsNilDBIsOff(t *testing.T) {
	if got := FromSettings(nil).Current(context.Background()); got != (Info{State: StateOff}) {
		t.Fatalf("FromSettings(nil) = %+v, want off", got)
	}
}

// 只在請求進來時才讀設定：這個套件不得有 goroutine、ticker、timer 或 init 裡的背景工作（Neon 睡眠規則：
// 週期性／長連線邏輯不可碰 DB）。用 AST 掃描套件內所有非測試原始碼。
func TestNoBackgroundWork(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources found: %v", err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.GoStmt:
				t.Errorf("%s: goroutine at %s — this package must be request-driven only", f, fset.Position(x.Pos()))
			case *ast.FuncDecl:
				if x.Name.Name == "init" && x.Recv == nil {
					t.Errorf("%s: init() — no background setup allowed", f)
				}
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "time" {
					switch x.Sel.Name {
					case "NewTicker", "Tick", "AfterFunc", "NewTimer", "Sleep":
						t.Errorf("%s: time.%s at %s — no timers or tickers allowed", f, x.Sel.Name, fset.Position(x.Pos()))
					}
				}
			}
			return true
		})
	}
}

// 套件不得 import integration 或其他業務套件（leaf：只依賴 appsettings），否則 ops／profile 等就不能安全 import 它。
func TestLeafImports(t *testing.T) {
	src, err := os.ReadFile("wearablesunset.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `"github.com/dor/api/internal/`) && line != `"github.com/dor/api/internal/appsettings"` {
			t.Errorf("leaf package imports %s", line)
		}
	}
}
