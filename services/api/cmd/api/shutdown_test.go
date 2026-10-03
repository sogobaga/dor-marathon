package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// shutdown_test.go（2026-10-03，正式環境部署時 IP 流量統計遺失）：
//
// 事故：SIGTERM → main 呼叫 bgCancel() → ipdaily goroutine 立刻做「最後一次 flush」（Neon 休眠時要 DNS＋TLS＋喚醒，
// 要好幾秒）；但 main 不等它，srv.Shutdown／Drain 一結束就返回，defer pool.Close() 把這次 flush 的連線直接取消
// （log：ops ip daily: flush upsert failed … operation was canceled），上次 flush 之後累計的計數全部遺失。
//
// 修正（main.go）：ipdaily 包在 goroutine 裡、返回時關閉 ipDailyDone；關機序列在 srv.Shutdown → garminHandler.Drain
// 之後 `waitDone(shutdownCtx, ipDailyDone)`，等最後一次 flush 寫完（受 shutdownCtx 30 秒上限約束）才讓 defer 的
// pool.Close() 執行。main() 無法單元測試，所以：
//  1. waitDone 的行為直接單測；
//  2. 接線用 go/parser 檢查 main.go 的真實原始碼（checkShutdownWiring），並用合成的「壞版本」原始碼證明這個檢查
//     不是空轉——舊寫法（go ipDailyAgg.Run(...) 沒人等）、沒等、順序錯、無期限、提前 pool.Close() 都會被抓到。

func TestWaitDone(t *testing.T) {
	t.Run("done 已關閉 → true", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !waitDone(ctx, done) {
			t.Fatal("waitDone should return true when done is already closed")
		}
	})

	t.Run("等到較慢的最後一次 flush 做完（期限內）才返回", func(t *testing.T) {
		done := make(chan struct{})
		var finished atomic.Bool
		go func() {
			time.Sleep(80 * time.Millisecond)
			finished.Store(true)
			close(done)
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !waitDone(ctx, done) {
			t.Fatal("waitDone should return true when done closes before the deadline")
		}
		if !finished.Load() {
			t.Fatal("waitDone returned before the work finished — pool.Close() would race the final flush")
		}
	})

	t.Run("期限到了還沒 done → false，不會永遠卡住關機", func(t *testing.T) {
		done := make(chan struct{}) // 永不關閉
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
		defer cancel()
		start := time.Now()
		if waitDone(ctx, done) {
			t.Fatal("waitDone should return false when the deadline passes first")
		}
		if el := time.Since(start); el < 40*time.Millisecond || el > 3*time.Second {
			t.Fatalf("waitDone should give up right at the deadline (~60ms), took %s", el)
		}
	})

	t.Run("done 與 ctx 同時就緒 → 以 done 為準（不誤報逾時）", func(t *testing.T) {
		// select 在多個 case 同時就緒時隨機挑一個；跑多輪確保 waitDone 的「再看一眼 done」讓結果是確定的 true。
		for i := 0; i < 2000; i++ {
			done := make(chan struct{})
			close(done)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if !waitDone(ctx, done) {
				t.Fatalf("iteration %d: done was closed but waitDone reported a timeout", i)
			}
		}
	})
}

// --- 接線檢查（go/parser） ---

// shutdownCallName 回傳呼叫的名稱："f"（單一識別字）或 "X.Sel"（X 為單一識別字），其餘回 ""。
func shutdownCallName(call *ast.CallExpr) string {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		if id, ok := f.X.(*ast.Ident); ok {
			return id.Name + "." + f.Sel.Name
		}
	}
	return ""
}

// checkShutdownWiring 檢查 func main 的關機接線，回傳所有違規（空＝通過）：
//  1. 呼叫 ipDailyAgg.Run 的必須是 `go func(){ defer close(done); ipDailyAgg.Run(...) }()`（裸 go 沒東西可等）；
//  2. 頂層陳述式順序：該 goroutine → bgCancel() → srv.Shutdown → garminHandler.Drain → waitDone(shutdownCtx, done)；
//     waitDone 必須是頂層（不能藏在條件式的 body 裡被跳過）；
//  3. shutdownCtx 來自 context.WithTimeout（等待有期限）；
//  4. 等待之前不得直接呼叫 pool.Close()／rdb.Close()（defer 的會在 main 返回時才跑，沒問題）。
func checkShutdownWiring(file *ast.File) (problems []string) {
	fail := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	var body *ast.BlockStmt
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "main" {
			body = fd.Body
		}
	}
	if body == nil {
		return []string{"func main not found"}
	}

	// (1) ipdaily goroutine 與它的 done channel
	goIdx, doneName := -1, ""
	for i, st := range body.List {
		gs, ok := st.(*ast.GoStmt)
		if !ok {
			continue
		}
		lit, ok := gs.Call.Fun.(*ast.FuncLit)
		if !ok {
			continue
		}
		runs, closed := false, ""
		ast.Inspect(lit.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if shutdownCallName(x) == "ipDailyAgg.Run" {
					runs = true
				}
			case *ast.DeferStmt:
				if shutdownCallName(x.Call) == "close" && len(x.Call.Args) == 1 {
					closed = identName(x.Call.Args[0])
				}
			}
			return true
		})
		if runs {
			goIdx, doneName = i, closed
		}
	}
	if goIdx < 0 {
		fail("ipDailyAgg.Run must be started as `go func(){ defer close(done); ipDailyAgg.Run(...) }()` so main can wait for its final flush (a bare `go ipDailyAgg.Run(...)` cannot be waited on)")
		return problems
	}
	if doneName == "" {
		fail("the ipDailyAgg.Run goroutine must `defer close(<done channel>)` so the channel closes only after Run (incl. its final flush) returns")
		return problems
	}

	// (2) 頂層陳述式的相對順序
	topCall := func(name string) int {
		for i, st := range body.List {
			if es, ok := st.(*ast.ExprStmt); ok {
				if c, ok := es.X.(*ast.CallExpr); ok && shutdownCallName(c) == name {
					return i
				}
			}
		}
		return -1
	}
	waitIdx := -1
	for i, st := range body.List {
		var root ast.Node
		switch s := st.(type) {
		case *ast.ExprStmt:
			root = s.X
		case *ast.IfStmt:
			root = s.Cond // `if !waitDone(...) { log… }`：只看條件式，body 內的呼叫不算（可能被跳過）
		default:
			continue
		}
		ast.Inspect(root, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok && shutdownCallName(c) == "waitDone" && len(c.Args) == 2 &&
				identName(c.Args[0]) == "shutdownCtx" && identName(c.Args[1]) == doneName {
				waitIdx = i
			}
			return true
		})
	}
	steps := []struct {
		label string
		idx   int
	}{
		{"go func(){ …ipDailyAgg.Run… }()", goIdx},
		{"bgCancel()", topCall("bgCancel")},
		{"srv.Shutdown(shutdownCtx)", topCall("srv.Shutdown")},
		{"garminHandler.Drain(shutdownCtx)", topCall("garminHandler.Drain")},
		{"waitDone(shutdownCtx, " + doneName + ")", waitIdx},
	}
	missing := false
	for _, s := range steps {
		if s.idx < 0 {
			fail("main must contain a top-level %s", s.label)
			missing = true
		}
	}
	if missing {
		return problems
	}
	for i := 1; i < len(steps); i++ {
		if steps[i-1].idx >= steps[i].idx {
			fail("%s must come before %s", steps[i-1].label, steps[i].label)
		}
	}

	// (3) 等待有期限
	bounded := false
	for _, st := range body.List {
		as, ok := st.(*ast.AssignStmt)
		if !ok || len(as.Lhs) == 0 || identName(as.Lhs[0]) != "shutdownCtx" || len(as.Rhs) != 1 {
			continue
		}
		if c, ok := as.Rhs[0].(*ast.CallExpr); ok && shutdownCallName(c) == "context.WithTimeout" {
			bounded = true
		}
	}
	if !bounded {
		fail("shutdownCtx must be created with context.WithTimeout so the wait for the final flush is bounded")
	}

	// (4) 等待之前不得提前關閉 DB／Redis
	for i, st := range body.List {
		es, ok := st.(*ast.ExprStmt)
		if !ok {
			continue
		}
		if c, ok := es.X.(*ast.CallExpr); ok && i < waitIdx {
			if n := shutdownCallName(c); n == "pool.Close" || n == "rdb.Close" {
				fail("%s() is called directly before main waits for the ipdaily final flush — keep it deferred", n)
			}
		}
	}
	return problems
}

func TestMainWaitsForIPDailyFinalFlushBeforeClosingPool(t *testing.T) {
	if problems := checkShutdownWiring(parseMain(t)); len(problems) > 0 {
		t.Fatalf("main.go shutdown wiring is wrong (the ipdaily final flush would race the deferred pool.Close()):\n- %s",
			strings.Join(problems, "\n- "))
	}
}

// 合成的 main：goodMain 是正確接線；其餘每個變體各破壞一件事，檢查器必須指出對應的原因。
const goodMain = `package main
func main() {
	defer pool.Close()
	defer rdb.Close()
	ipDailyDone := make(chan struct{})
	go func() {
		defer close(ipDailyDone)
		ipDailyAgg.Run(ctx)
	}()
	<-quit
	bgCancel()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	garminHandler.Drain(shutdownCtx)
	if !waitDone(shutdownCtx, ipDailyDone) {
		log.Warn().Msg("x")
	}
}
`

func TestShutdownWiringCheckerCatchesRegressions(t *testing.T) {
	mutate := func(from, to string) string {
		if !strings.Contains(goodMain, from) {
			t.Fatalf("test bug: goodMain does not contain %q", from)
		}
		return strings.Replace(goodMain, from, to, 1)
	}
	waitBlock := "\tif !waitDone(shutdownCtx, ipDailyDone) {\n\t\tlog.Warn().Msg(\"x\")\n\t}\n"

	cases := []struct {
		name string
		src  string
		want string // 必須出現在某個違規訊息裡的片段；"" ＝ 必須完全通過
	}{
		{"正確接線", goodMain, ""},
		{"舊寫法：裸 go ipDailyAgg.Run 沒人等",
			strings.Replace(mutate("\tgo func() {\n\t\tdefer close(ipDailyDone)\n\t\tipDailyAgg.Run(ctx)\n\t}()\n", "\tgo ipDailyAgg.Run(ctx)\n"), waitBlock, "", 1),
			"bare `go ipDailyAgg.Run"},
		{"goroutine 沒有 defer close(done)",
			mutate("\t\tdefer close(ipDailyDone)\n", ""),
			"defer close"},
		{"main 沒有等", mutate(waitBlock, ""), "waitDone"},
		{"等錯 channel", mutate("waitDone(shutdownCtx, ipDailyDone)", "waitDone(shutdownCtx, otherDone)"), "waitDone"},
		{"等待藏在條件式 body 裡可能被跳過",
			mutate(waitBlock, "\tif drained {\n\t\twaitDone(shutdownCtx, ipDailyDone)\n\t}\n"), "waitDone"},
		{"等在 srv.Shutdown 之前", mutate("\tsrv.Shutdown(shutdownCtx)\n\tgarminHandler.Drain(shutdownCtx)\n"+waitBlock, waitBlock+"\tsrv.Shutdown(shutdownCtx)\n\tgarminHandler.Drain(shutdownCtx)\n"), "must come before"},
		{"等在 Drain 之前", mutate("\tgarminHandler.Drain(shutdownCtx)\n"+waitBlock, waitBlock+"\tgarminHandler.Drain(shutdownCtx)\n"), "must come before"},
		{"shutdownCtx 沒有期限", mutate("context.WithTimeout(context.Background(), 30*time.Second)", "context.WithCancel(context.Background())"), "context.WithTimeout"},
		{"等待前直接 pool.Close()", mutate(waitBlock, "\tpool.Close()\n"+waitBlock), "pool.Close()"},
		{"等待前直接 rdb.Close()", mutate(waitBlock, "\trdb.Close()\n"+waitBlock), "rdb.Close()"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "synthetic.go", c.src, 0)
			if err != nil {
				t.Fatalf("synthetic source does not parse: %v", err)
			}
			problems := checkShutdownWiring(file)
			if c.want == "" {
				if len(problems) > 0 {
					t.Fatalf("expected no problems, got:\n- %s", strings.Join(problems, "\n- "))
				}
				return
			}
			if len(problems) == 0 {
				t.Fatalf("checker did not catch this regression (wanted a problem mentioning %q)", c.want)
			}
			if !strings.Contains(strings.Join(problems, "\n"), c.want) {
				t.Fatalf("checker flagged the wrong thing: wanted a problem mentioning %q, got:\n- %s", c.want, strings.Join(problems, "\n- "))
			}
		})
	}
}
