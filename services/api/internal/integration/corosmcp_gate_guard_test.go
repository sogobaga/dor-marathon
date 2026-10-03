package integration

// 「Dashboard 入口與 API 閘門必須呼叫同一個判斷函式」的原始碼守門（GA 契約 §2.1／§2.2，計畫 S1 驗收）：
// 以 go/ast 檢查函式本體裡實際呼叫了哪些函式——connect／callback／import 要過入口閘門，
// status／disconnect 不得過（只要有連線列就能看、能中斷），probe 只認超管；Dashboard 與 API 閘門最後都落到
// entrygate.Resolve；profile 不得自己再寫一份 COROS／Garmin 白名單比對。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// callsIn 回傳函式 recv.name 本體內所有被呼叫的「選擇器末段名稱」（h.requireEntry → "requireEntry"，
// entrygate.Load → "Load"）與「裸識別字呼叫」（Resolve → "Resolve"）。
func callsIn(t *testing.T, file, funcName string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.FromSlash(file), nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	out := map[string]bool{}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Name.Name != funcName || fd.Body == nil {
			return true
		}
		found = true
		ast.Inspect(fd.Body, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				out[fn.Sel.Name] = true
			case *ast.Ident:
				out[fn.Name] = true
			}
			return true
		})
		return false
	})
	if !found {
		t.Fatalf("function %s not found in %s", funcName, file)
	}
	return out
}

func TestCorosMcpGateCoverage(t *testing.T) {
	// 需要入口閘門：connect／import（requireEntry）、callback（entryForUser）。
	for _, fn := range []string{"Connect", "Import"} {
		file := "corosmcp.go"
		if fn == "Import" {
			file = "corosmcp_sync.go" // Import 在同步檔
		}
		c := callsIn(t, file, fn)
		if !c["requireEntry"] {
			t.Errorf("%s must go through requireEntry (entry gate)", fn)
		}
	}
	if !callsIn(t, "corosmcp.go", "Callback")["entryForUser"] {
		t.Error("Callback must re-check the entry gate (entry may be closed while the user is on the COROS consent page)")
	}
	// 自動同步（含重新授權補同步）受入口限制：autoSyncOnce／reauthCatchUp 都要查緊急關閉與 kill switch。
	for _, fn := range []string{"autoSyncOnce", "reauthCatchUp"} {
		c := callsIn(t, "corosmcp_sync.go", fn)
		if !c["entryEmergency"] || !c["autoSyncEnabled"] {
			t.Errorf("%s must honour the emergency shutdown and the autosync kill switch", fn)
		}
	}
	// 不得過入口閘門：status、disconnect（只要登入）。
	for _, fn := range []string{"Status", "Disconnect"} {
		c := callsIn(t, "corosmcp.go", fn)
		if c["requireEntry"] || c["entryForUser"] || c["EntryForUser"] {
			t.Errorf("%s must NOT be gated by the entry state (users with a connection must always be able to see and remove it)", fn)
		}
		if !c["requireUser"] {
			t.Errorf("%s must still require a logged-in user", fn)
		}
	}
	// probe：只有超管（loadUser→IsSuper），且不走一般入口閘門（緊急關閉由 Emergency 另外擋）。
	probe := callsIn(t, "corosmcp.go", "Probe")
	if !probe["requireUser"] || !probe["loadUser"] || !probe["Emergency"] {
		t.Error("Probe must require login, load the user (super-admin check) and honour the emergency shutdown")
	}
	// 進入點註冊沒有被繞過：Router 仍把五個端點掛上
	src, _ := os.ReadFile("corosmcp.go")
	for _, route := range []string{`r.Post("/connect", h.Connect)`, `r.Get("/status", h.Status)`, `r.Post("/probe", h.Probe)`, `r.Post("/import", h.Import)`, `r.Post("/disconnect", h.Disconnect)`, `r.Get("/callback", h.Callback)`} {
		if !strings.Contains(string(src), route) {
			t.Errorf("router lost %s", route)
		}
	}
}

func TestDashboardAndAPIGateShareOneResolver(t *testing.T) {
	// Dashboard：profile.membership 只經 integration.DashboardEntry，不再自己比對 COROS／Garmin 白名單。
	mem, err := os.ReadFile(filepath.FromSlash("../profile/membership.go"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(mem)
	if n := strings.Count(s, "integration.DashboardEntry("); n != 2 {
		t.Errorf("membership.go must call integration.DashboardEntry exactly twice (coros_mcp + garmin), got %d", n)
	}
	for _, banned := range []string{"CorosMcpDashboardEntry", "corosMcpWhitelisted", "coros_mcp_whitelist", "garmin_whitelist", "coros_mcp_entry_state", "garmin_entry_state"} {
		if strings.Contains(s, banned) {
			t.Errorf("membership.go must not carry its own gate logic/keys (%q)", banned)
		}
	}
	// DashboardEntry／EntryForUser 兩條路徑最後都落到 entrygate.Resolve。
	if c := callsIn(t, "directentry.go", "DashboardEntry"); !c["ResolveFromSettings"] {
		t.Error("DashboardEntry must delegate to entrygate.ResolveFromSettings")
	}
	if c := callsIn(t, "directentry.go", "EntryForUser"); !c["Load"] {
		t.Error("EntryForUser must delegate to entrygate.Load")
	}
	for _, fn := range []string{"ResolveFromSettings", "Load"} {
		if !callsIn(t, "entrygate/entrygate.go", fn)["Resolve"] {
			t.Errorf("entrygate.%s must call Resolve (single decision function)", fn)
		}
	}
	// COROS handler 自己不得再有白名單比對函式
	cm, _ := os.ReadFile("corosmcp.go")
	if strings.Contains(string(cm), "func corosMcpWhitelisted") || strings.Contains(string(cm), "func CorosMcpDashboardEntry") {
		t.Error("corosmcp.go still defines a private whitelist gate")
	}
	// 兩個供應商的鍵都經 EntryKeys 單點對照
	if s, w, d, ok := EntryKeys(EntryProviderCoros); !ok || s != "coros_mcp_entry_state" || w != "coros_mcp_whitelist" || d != corosMcpDefaultWhitelist {
		t.Errorf("coros keys: %q %q %q %v", s, w, d, ok)
	}
	if s, w, d, ok := EntryKeys(EntryProviderGarmin); !ok || s != "garmin_entry_state" || w != "garmin_whitelist" || d != "" {
		t.Errorf("garmin keys: %q %q %q %v", s, w, d, ok)
	}
	if _, _, _, ok := EntryKeys("strava"); ok {
		t.Error("unknown provider must not resolve")
	}
}
