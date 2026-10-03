package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// wiring_test.go：main() 無法被單元測試（package main，依賴整個系統），但團練同步跑熱路徑的掛載順序是
// 契約 §9-12 的驗收項目：RequireAuth 必須先於 RateLimit 執行，限流維度（middleware.UserOrIP）才會是
// "u<uid>"；若 RateLimit 先跑，UserOrIP 退回 client IP，50 位同場地 WiFi 的跑者共用一個「60 次/分」的桶。
//
// 這裡以 go/parser 檢查 main.go 的真實原始碼：
//  1. Mount("/run-meet-live", live.NewHandler(rdb).Router()) 位於某個 r.Group(func…) 內；
//  2. 該 Group 在 Mount 之前（同一個函式本體、較早的陳述式）有 r.Use(middleware.RequireAuth(...))；
//  3. Mount 的接收者是 r.With(middleware.RateLimit(rdb, "runmeet_live_pos", 60, time.Minute, middleware.UserOrIP))。
//
// 行為面（同樣的鏈在 chi 上確實得到 u<uid>）由 internal/runmeet/live 的 TestRouteOrder* 驗證。

func TestRunMeetLiveMountIsInsideAuthGroupBeforeRateLimit(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var stack []ast.Node
	var mountStack []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Mount" && len(call.Args) == 2 {
				if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Value == `"/run-meet-live"` {
					if mountStack != nil {
						t.Fatal(`main.go mounts "/run-meet-live" more than once`)
					}
					mountStack = append([]ast.Node(nil), stack...)
				}
			}
		}
		return true
	})
	if mountStack == nil {
		t.Fatal(`main.go does not mount "/run-meet-live" (the live hot path)`)
	}
	mount := mountStack[len(mountStack)-1].(*ast.CallExpr)

	// (3) Mount 的第二個參數：live.NewHandler(rdb).Router()
	router, ok := mount.Args[1].(*ast.CallExpr)
	if !ok {
		t.Fatal("Mount's handler argument is not a call")
	}
	if sel, ok := router.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Router" {
		t.Fatal("Mount's handler must be live.NewHandler(rdb).Router()")
	}
	newH, ok := router.Fun.(*ast.SelectorExpr).X.(*ast.CallExpr)
	if !ok {
		t.Fatal("Router() receiver must be live.NewHandler(rdb)")
	}
	if sel, ok := newH.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "NewHandler" || identName(sel.X) != "live" {
		t.Fatal("Router() receiver must be live.NewHandler(rdb)")
	}

	// (3) 接收者：r.With(middleware.RateLimit(rdb, "runmeet_live_pos", 60, time.Minute, middleware.UserOrIP))
	with, ok := mount.Fun.(*ast.SelectorExpr).X.(*ast.CallExpr)
	if !ok {
		t.Fatal(`Mount's receiver must be r.With(middleware.RateLimit(...))`)
	}
	if sel, ok := with.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "With" || len(with.Args) != 1 {
		t.Fatal(`Mount's receiver must be r.With(middleware.RateLimit(...))`)
	}
	rl, ok := with.Args[0].(*ast.CallExpr)
	if !ok || len(rl.Args) != 5 {
		t.Fatal(`With's argument must be middleware.RateLimit(rdb, action, limit, window, dim)`)
	}
	if sel, ok := rl.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "RateLimit" || identName(sel.X) != "middleware" {
		t.Fatal(`With's argument must be middleware.RateLimit(...)`)
	}
	if lit, ok := rl.Args[1].(*ast.BasicLit); !ok || lit.Value != `"runmeet_live_pos"` {
		t.Error(`rate-limit action must be "runmeet_live_pos"`)
	}
	if lit, ok := rl.Args[2].(*ast.BasicLit); !ok || lit.Value != "60" {
		t.Error("rate limit must be 60")
	}
	if sel, ok := rl.Args[3].(*ast.SelectorExpr); !ok || sel.Sel.Name != "Minute" {
		t.Error("rate-limit window must be time.Minute")
	}
	// UserOrIP：維度吃 ctx 的使用者，所以必須排在 RequireAuth 之後
	if sel, ok := rl.Args[4].(*ast.SelectorExpr); !ok || sel.Sel.Name != "UserOrIP" || identName(sel.X) != "middleware" {
		t.Error("rate-limit dimension must be middleware.UserOrIP")
	}

	// (1) 最近的 FuncLit 必須是 r.Group(func(r chi.Router){…}) 的參數
	var lit *ast.FuncLit
	var litIdx int
	for i := len(mountStack) - 1; i >= 0; i-- {
		if fl, ok := mountStack[i].(*ast.FuncLit); ok {
			lit, litIdx = fl, i
			break
		}
	}
	if lit == nil {
		t.Fatal(`"/run-meet-live" is not mounted inside a func literal (expected r.Group(func(r chi.Router){…}))`)
	}
	parent, ok := mountStack[litIdx-1].(*ast.CallExpr)
	if !ok {
		t.Fatal("the func literal is not an argument of a call")
	}
	if sel, ok := parent.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Group" {
		t.Fatalf(`"/run-meet-live" must be mounted inside r.Group(...), found it inside another construct`)
	}

	// (2) Group 本體：Mount 所在陳述式之前，必須有 r.Use(middleware.RequireAuth(...))
	mountStmtIdx := -1
	for i, st := range lit.Body.List {
		if st.Pos() <= mount.Pos() && mount.End() <= st.End() {
			mountStmtIdx = i
		}
	}
	if mountStmtIdx < 0 {
		t.Fatal("cannot locate the statement containing the Mount call")
	}
	authIdx := -1
	for i := 0; i < mountStmtIdx; i++ {
		es, ok := lit.Body.List[i].(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Use" || len(call.Args) != 1 {
			continue
		}
		inner, ok := call.Args[0].(*ast.CallExpr)
		if !ok {
			continue
		}
		if isel, ok := inner.Fun.(*ast.SelectorExpr); ok && isel.Sel.Name == "RequireAuth" && identName(isel.X) == "middleware" {
			authIdx = i
			break
		}
	}
	if authIdx < 0 {
		t.Fatalf(`the r.Group containing "/run-meet-live" has no r.Use(middleware.RequireAuth(...)) before the mount ` +
			`— RequireAuth must run BEFORE RateLimit (otherwise UserOrIP falls back to the client IP)`)
	}

	// 同一個 Group 內也確認 import 沒被改成別的 live
	imported := false
	for _, imp := range file.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == "github.com/dor/api/internal/runmeet/live" {
			imported = true
		}
	}
	if !imported {
		t.Error(`main.go must import "github.com/dor/api/internal/runmeet/live" for the hot path`)
	}
}

func identName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}
