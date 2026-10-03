package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// wiring_garmin_test.go（計畫 S7）：main() 無法被單元測試，以 go/parser 檢查 main.go 的真實原始碼——
//  1. MaxBodyBytes 的略過前綴包含 Garmin webhook 路徑 "/api/v1/integrations/garmin/webhook/"：
//     Garmin 單次 push 可達 10MB，少了這行會被 1MB 全域上限先擋成 413，Garmin 判定端點失效而停推；
//  2. 啟動時呼叫 integration.CheckTokenKeyAtStartup()；
//  3. 競賽分組重算 callback（integration.SetCompetitionRecompute）與日報直連供應商（AddDirectWearable）已接線；
//  4. 一旦 main.go 引用了 Garmin handler，就必須 Mount("/integrations/garmin", …)——join 前（還沒有任何 garmin
//     識別字）這一項明確 skip，不是靜默通過。
//
// 刻意不改團練的 wiring_test.go（它守的是同步跑熱路徑的掛載順序）。

const garminWebhookPrefix = "/api/v1/integrations/garmin/webhook/"

func parseMain(t *testing.T) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

// selectorCalls 收集 main.go 中所有 X.Sel(...) 呼叫，key＝"X.Sel"（X 為單一識別字時）或 "Sel"。
func selectorCalls(file *ast.File) map[string][]*ast.CallExpr {
	out := map[string][]*ast.CallExpr{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			key := sel.Sel.Name
			if id, ok := sel.X.(*ast.Ident); ok {
				key = id.Name + "." + sel.Sel.Name
			}
			out[key] = append(out[key], call)
		}
		return true
	})
	return out
}

func TestMaxBodyBytesSkipsGarminWebhookPrefix(t *testing.T) {
	calls := selectorCalls(parseMain(t))["middleware.MaxBodyBytes"]
	if len(calls) != 1 {
		t.Fatalf("main.go must call middleware.MaxBodyBytes exactly once, got %d", len(calls))
	}
	skips := map[string]bool{}
	for _, a := range calls[0].Args[1:] { // 第一個參數是 1<<20 上限
		if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			s, _ := strconv.Unquote(lit.Value)
			skips[s] = true
		}
	}
	if !skips[garminWebhookPrefix] {
		t.Fatalf("MaxBodyBytes skip list must include %q (Garmin pushes up to 10MB; the 1MB global cap would 413 it and Garmin would disable the endpoint), got %v", garminWebhookPrefix, skips)
	}
	// 既有的略過前綴沒有被誤刪
	for _, keep := range []string{"/api/v1/admin/images", "/api/v1/profile/avatar", "/api/v1/run-meets/images"} {
		if !skips[keep] {
			t.Errorf("existing skip prefix %q was dropped", keep)
		}
	}
	// 前綴比對語意：skip 用 strings.HasPrefix，webhook 路徑（含秘密 token 段）必須落在前綴下
	if !strings.HasPrefix(garminWebhookPrefix+"SECRET/activities", garminWebhookPrefix) {
		t.Fatal("sanity")
	}
}

func TestMainChecksTokenKeyAtStartupAndWiresS7Pieces(t *testing.T) {
	calls := selectorCalls(parseMain(t))
	if len(calls["integration.CheckTokenKeyAtStartup"]) != 1 {
		t.Error("main() must call integration.CheckTokenKeyAtStartup() once at startup (missing key → log.Error + Telegram alert)")
	}
	if len(calls["integration.SetCompetitionRecompute"]) != 1 {
		t.Error("main() must inject the competition standings recompute callback (integration.SetCompetitionRecompute)")
	}
	if len(calls["opsHandler.AddDirectWearable"]) < 1 {
		t.Error("main() must register the direct-wearable providers with the daily report (opsHandler.AddDirectWearable)")
	}
	if len(calls["virtualrunner.RecomputeStandingsForUsers"]) != 1 {
		t.Error("the competition recompute callback must reuse the virtualrunner aggregation SQL (single copy in the api module)")
	}
}

func TestGarminRoutesMountedOnceWiredIntoMain(t *testing.T) {
	file := parseMain(t)
	garminIdent := false
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && strings.HasPrefix(strings.ToLower(id.Name), "garmin") {
			garminIdent = true
		}
		return true
	})
	if !garminIdent {
		t.Skip("Garmin join pending owner approval")
	}
	mounted := false
	for _, call := range selectorCalls(file)["Mount"] {
		if len(call.Args) == 2 {
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Value == `"/integrations/garmin"` {
				mounted = true
			}
		}
	}
	if !mounted {
		t.Fatal(`main.go references the Garmin handler but never mounts "/integrations/garmin" (public group)`)
	}
}
