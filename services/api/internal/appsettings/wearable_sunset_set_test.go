package appsettings

// 把「後台寫入設定的 Set」綁到 validateWrite（含 Terra／Strava 公告的跨鍵規則：announce 時結束日不得早於今天）。
// validateWrite 本身另有表格測試（wearable_sunset_test.go），但那些測試都是直接呼叫 validateWrite——如果有人把 Set 裡的
// 呼叫換成只做單鍵驗證，這些測試仍然全綠、跨鍵保護卻靜默消失。這裡用兩種獨立的方式把 Set 釘住：
//  1. go/ast：Set 必須呼叫 validateWrite、而且早於任何資料庫寫入，也不能自己直接查 specs（那就是繞過）；
//     寫入成功之後還要呼叫 NotifyChange（後台改動通知、緊急關閉旗標等 OnChange 回呼都靠它，拿掉就靜默失效）；
//  2. 行為：真的經 AdminRouter 發 PUT——沒有資料庫（db=nil），所以只要驗證沒有擋下、請求走到寫入那一步，就會 nil pointer
//     panic，測試把 panic 當成「驗證被繞過」回報。不連 DB、不需要 Redis。

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSetBindsToValidateWrite_AST(t *testing.T) {
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "appsettings.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var set *ast.FuncDecl
	for _, d := range af.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Set" || fd.Recv == nil || len(fd.Recv.List) == 0 || fd.Body == nil {
			continue
		}
		if star, ok := fd.Recv.List[0].Type.(*ast.StarExpr); ok {
			if id, ok := star.X.(*ast.Ident); ok && id.Name == "Handler" {
				set = fd
			}
		}
	}
	if set == nil {
		t.Fatal("func (h *Handler) Set not found in appsettings.go")
	}

	validateAt, firstWrite, notifyAt := -1, -1, -1
	ast.Inspect(set.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "NotifyChange" {
				notifyAt = fset.Position(x.Pos()).Offset
			}
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				off := fset.Position(x.Pos()).Offset
				switch sel.Sel.Name {
				case "validateWrite":
					if validateAt < 0 || off < validateAt {
						validateAt = off
					}
				case "Exec", "Query", "QueryRow", "SendBatch", "Begin": // 任何碰資料庫的動作
					if firstWrite < 0 || off < firstWrite {
						firstWrite = off
					}
				}
			}
		case *ast.IndexExpr:
			// 自己直接 specs[key]＝繞過 validateWrite（只剩單鍵驗證器，跨鍵規則不會執行）
			if id, ok := x.X.(*ast.Ident); ok && id.Name == "specs" {
				t.Errorf("Set must not consult specs[...] itself at %s: go through h.validateWrite so the cross-key rule always runs", fset.Position(x.Pos()))
			}
		}
		return true
	})
	if validateAt < 0 {
		t.Fatal("Set must call h.validateWrite (single-key validator + cross-key rule) before writing")
	}
	if firstWrite >= 0 && validateAt > firstWrite {
		t.Fatal("Set must call h.validateWrite before any database access")
	}
	if notifyAt < 0 || firstWrite < 0 || notifyAt < firstWrite {
		t.Fatal("Set must call NotifyChange after the database write (OnChange callbacks such as the sunset change alert depend on it)")
	}
}

// 經 AdminRouter 的 PUT：被擋下的寫入一律是 400（帶原因）／500，而且絕不會走到寫入。
func TestAdminRouterPutIsGuardedByTheCrossKeyRule(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, taipei)
	put := func(h *Handler, key, value string) (code int, body string) {
		t.Helper()
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PUT %s=%q reached the database write (nil pool panic: %v) although validation must have rejected it", key, value, r)
			}
		}()
		req := httptest.NewRequest(http.MethodPut, "/"+key, strings.NewReader(`{"value":"`+value+`"}`))
		rec := httptest.NewRecorder()
		h.AdminRouter().ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	// 資料庫裡的結束日已經過期：啟用公告被擋
	f := &fakeSettings{vals: map[string]string{WearableSunsetDateKey: "2026-10-01"}}
	h := &Handler{readFresh: f.read, now: func() time.Time { return now }} // db／rt 刻意是 nil：走到寫入就 panic
	if code, body := put(h, WearableSunsetStateKey, "announce"); code != http.StatusBadRequest || !strings.Contains(body, "2026-10-01") || !strings.Contains(body, "2026-10-03") {
		t.Errorf("announce with a past stored date: %d %s, want 400 naming both dates", code, body)
	}

	// 公告進行中：把結束日改到過去被擋
	f = &fakeSettings{vals: map[string]string{WearableSunsetStateKey: "announce"}}
	h = &Handler{readFresh: f.read, now: func() time.Time { return now }}
	if code, body := put(h, WearableSunsetDateKey, "2026-10-02"); code != http.StatusBadRequest || !strings.Contains(body, "2026-10-02") {
		t.Errorf("past date while announcing: %d %s, want 400 with the reason", code, body)
	}

	// 單鍵驗證與未登記的 key 也在同一個入口被擋
	if code, body := put(h, WearableSunsetStateKey, "closed"); code != http.StatusBadRequest || !strings.Contains(body, "invalid value") {
		t.Errorf("invalid state value: %d %s, want 400 invalid value", code, body)
	}
	if code, body := put(h, "no_such_setting", "x"); code != http.StatusBadRequest || !strings.Contains(body, "unknown setting") {
		t.Errorf("unknown key: %d %s, want 400 unknown setting", code, body)
	}

	// 現查另一個鍵失敗：500，不得靜默放行
	h = &Handler{readFresh: (&fakeSettings{err: errors.New("db down")}).read, now: func() time.Time { return now }}
	if code, _ := put(h, WearableSunsetStateKey, "announce"); code != http.StatusInternalServerError {
		t.Errorf("failed fresh read must be a 500, got %d", code)
	}
}
