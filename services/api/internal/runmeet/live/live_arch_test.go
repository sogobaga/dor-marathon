package live

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// live_arch_test.go：架構鐵則的機器檢查（契約 §1、§9 P1-2）。
//
//   - 熱路徑（/pos、/leave）零 PostgreSQL：本套件不得 import 任何會碰 DB 的東西，Handler／Store 的型別
//     樹裡不得出現 pgx 的型別——「拿不到連線池」是型別層級的保證，不是紀律。
//   - 葉節點：不得 import internal/runmeet（runmeet 會 import 本套件，反向 import 就循環）。
//   - 不記 log：座標／request body 不得進任何 log；不 import 任何 logger，連 Redis 錯誤都不記。

func parseNonTestFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]*ast.File{}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", n, err)
		}
		out[n] = f
	}
	if len(out) < 4 {
		t.Fatalf("expected the live package sources, found %d files", len(out))
	}
	return out
}

func TestPackageImportsKeepHotPathDatabaseFreeAndLeafAndSilent(t *testing.T) {
	forbidden := []struct{ contains, why string }{
		{"jackc/pgx", "PostgreSQL 驅動：熱路徑零 DB 查詢必須由型別保證"},
		{"database/sql", "任何 SQL 存取"},
		{"lib/pq", "PostgreSQL 驅動"},
		{"internal/db", "專案的資料庫套件"},
		{"internal/appsettings", "appsettings 會讀 DB（設定由 runmeet 讀好後傳入）"},
		{"github.com/rs/zerolog", "不得記 log（座標／body 不得進 log）"},
		{"log/slog", "不得記 log"},
	}
	exact := map[string]string{
		"log":                                 "不得記 log",
		"github.com/dor/api/internal/runmeet": "runmeet 會 import 本套件，反向 import 會循環",
		"os":                                  "不得寫 stdout／stderr",
	}
	for name, f := range parseNonTestFiles(t) {
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			for _, fb := range forbidden {
				if strings.Contains(path, fb.contains) {
					t.Errorf("%s imports %q — forbidden: %s", name, path, fb.why)
				}
			}
			if why, bad := exact[path]; bad {
				t.Errorf("%s imports %q — forbidden: %s", name, path, why)
			}
		}
		// 不得 fmt.Print*／Fprint*（會寫到 stdout／stderr）
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "fmt" &&
					(strings.HasPrefix(sel.Sel.Name, "Print") || strings.HasPrefix(sel.Sel.Name, "Fprint")) {
					t.Errorf("%s calls fmt.%s — the live package must never print or log", name, sel.Sel.Name)
				}
			}
			return true
		})
	}
}

// Handler 與 Store 的型別樹（含巢狀欄位）裡不得出現 pgx／database/sql 的型別。
func TestHandlerAndStoreHoldNoDatabaseHandle(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(path string, ty reflect.Type)
	walk = func(path string, ty reflect.Type) {
		if seen[ty] {
			return
		}
		seen[ty] = true
		if pkg := ty.PkgPath(); strings.Contains(pkg, "pgx") || strings.Contains(pkg, "database/sql") {
			t.Errorf("%s has type %s from %s — the hot path must not hold a database handle", path, ty, pkg)
		}
		switch ty.Kind() {
		case reflect.Ptr, reflect.Slice, reflect.Array, reflect.Chan:
			walk(path+"[]", ty.Elem())
		case reflect.Map:
			walk(path+"[key]", ty.Key())
			walk(path+"[val]", ty.Elem())
		case reflect.Struct:
			// 只展開本專案與 go-redis 的結構；標準庫內部（sync、net…）不需要檢查
			if p := ty.PkgPath(); !strings.HasPrefix(p, "github.com/dor/api/") && !strings.Contains(p, "go-redis") && p != "" {
				return
			}
			for i := 0; i < ty.NumField(); i++ {
				f := ty.Field(i)
				walk(path+"."+f.Name, f.Type)
			}
		case reflect.Interface:
			// 介面無法靜態展開；本套件沒有以介面持有外部資源（Store 具體型別、Handler 具體型別）
		}
	}
	walk("Handler", reflect.TypeOf(Handler{}))
	walk("Store", reflect.TypeOf(Store{}))
	if len(seen) < 3 {
		t.Fatalf("type walk visited only %d types; the check is not exercising anything", len(seen))
	}
}
