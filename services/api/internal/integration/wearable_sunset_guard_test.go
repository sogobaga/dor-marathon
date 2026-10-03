package integration

// 「Terra／Strava 串接結束公告（announce）守門沒有被繞過」的原始碼守門（go/parser + go/ast，不連資料庫）。
// 規則：announce 起不得再建立任何新的 Terra／Strava 連線列。單元測試只能證明「目前這些入口有擋」；這支測試證明
// 「日後新增或搬動建列路徑時，不可能不經過守門」——任何新的建列呼叫點都會讓它失敗，逼人決定要不要納入守門。
//
// 掃描範圍刻意收斂：Strava 只看 strava.go（StravaHandler）的 Save；COROS Partner（coros.go）共用同一個
// Repository.Save，但不屬於 Terra／Strava，列在白名單並說明理由，不能因為它誤抓或被迫放寬整體規則。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// callSite 一個函式呼叫點：在哪個檔案、哪個接收者型別（沒有＝"" 即一般函式）、哪個函式內被呼叫。
type callSite struct{ File, Recv, Func string }

func (c callSite) String() string {
	if c.Recv != "" {
		return c.File + ":" + c.Recv + "." + c.Func
	}
	return c.File + ":" + c.Func
}

// parseNonTestFiles 解析本套件所有非測試 .go 檔。
func parseNonTestFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources: %v", err)
	}
	fset := token.NewFileSet()
	out := map[string]*ast.File{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		out[f] = af
	}
	return out
}

func recvName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	switch x := fd.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.Ident:
		return x.Name
	}
	return ""
}

// callSitesOf 回傳所有「以任何接收者呼叫 selector 名稱 sel」的呼叫點（含 h.repo.Save、store.Save… 的 Save）。
func callSitesOf(files map[string]*ast.File, sel string) []string {
	var out []string
	for name, af := range files {
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			found := false
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if s, ok := call.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == sel {
						found = true
					}
				}
				return true
			})
			if found {
				out = append(out, callSite{File: name, Recv: recvName(fd), Func: fd.Name.Name}.String())
			}
		}
	}
	sort.Strings(out)
	return out
}

// funcBody 找 file 裡 recv.fn 的函式宣告（recv 為 "" 代表一般函式）。
func funcBody(t *testing.T, file, recv, fn string) (*ast.FuncDecl, *token.FileSet) {
	t.Helper()
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, filepath.FromSlash(file), nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	for _, d := range af.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == fn && recvName(fd) == recv && fd.Body != nil {
			return fd, fset
		}
	}
	t.Fatalf("function %s.%s not found in %s", recv, fn, file)
	return nil, nil
}

// firstCall 回傳函式本體內第一次呼叫 name 的原始碼位置（offset）；沒呼叫回 -1。
func firstCall(t *testing.T, file, recv, fn, name string) int {
	t.Helper()
	fd, fset := funcBody(t, file, recv, fn)
	best := -1
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		match := false
		switch f := call.Fun.(type) {
		case *ast.SelectorExpr:
			match = f.Sel.Name == name
		case *ast.Ident:
			match = f.Name == name
		}
		if match {
			if off := fset.Position(call.Pos()).Offset; best < 0 || off < best {
				best = off
			}
		}
		return true
	})
	return best
}

// callOffsets 回傳函式本體內所有呼叫 name 的原始碼位置（offset，由小到大）。
func callOffsets(t *testing.T, file, recv, fn, name string) []int {
	t.Helper()
	fd, fset := funcBody(t, file, recv, fn)
	var offs []int
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		match := false
		switch f := call.Fun.(type) {
		case *ast.SelectorExpr:
			match = f.Sel.Name == name
		case *ast.Ident:
			match = f.Name == name
		}
		if match {
			offs = append(offs, fset.Position(call.Pos()).Offset)
		}
		return true
	})
	sort.Ints(offs)
	return offs
}

func callsNothingFrom(t *testing.T, file, recv, fn string, forbidden ...string) {
	t.Helper()
	for _, name := range forbidden {
		if firstCall(t, file, recv, fn, name) >= 0 {
			t.Errorf("%s.%s must NOT call %s (existing connections must keep working through the announce period)", recv, fn, name)
		}
	}
}

// before 斷言 fn 內 first 的第一次呼叫早於 second 的第一次呼叫（second 沒有呼叫就不比）。
func before(t *testing.T, file, recv, fn, first, second string) {
	t.Helper()
	a := firstCall(t, file, recv, fn, first)
	if a < 0 {
		t.Errorf("%s.%s must call %s", recv, fn, first)
		return
	}
	if b := firstCall(t, file, recv, fn, second); b >= 0 && a > b {
		t.Errorf("%s.%s must call %s before %s", recv, fn, first, second)
	}
}

func TestSunsetGuard_TerraEntryPointsAreGated(t *testing.T) {
	// T1 connect：登入檢查之後、enabled()／任何對外呼叫之前就擋下；呼叫的是那支會回 409（wearablesunset.RefusalStatus）的 refuseNewConnection。
	before(t, "terra.go", "TerraHandler", "Connect", "refuseNewConnection", "enabled")
	before(t, "terra.go", "TerraHandler", "Connect", "refuseNewConnection", "Do")
	before(t, "terra.go", "TerraHandler", "Connect", "sunsetInfo", "enabled")
	// T2 callback：在 enabled()、userInfo 反查、任何寫入之前導回公告訊息；落地走 persistTerraConn。
	before(t, "terra.go", "TerraHandler", "Callback", "newConnectionsPaused", "enabled")
	before(t, "terra.go", "TerraHandler", "Callback", "newConnectionsPaused", "fetchTerraUserInfo")
	before(t, "terra.go", "TerraHandler", "Callback", "newConnectionsPaused", "persistTerraConn")
	// T3/T4/T5 webhook 事件：建列／更新列一律經 persistTerraConn（announce 只更新既有列）。
	for _, fn := range []string{"Callback", "handleAuthEvent", "handleReauthEvent", "handleActivityEvent"} {
		if firstCall(t, "terra.go", "TerraHandler", fn, "persistTerraConn") < 0 {
			t.Errorf("TerraHandler.%s must persist through persistTerraConn", fn)
		}
	}
	// T3 auth 事件（＝使用者在 Terra 完成連接／重新授權）：announce 期間在碰資料庫（UserExists）與落地之前就略過，
	// 連既有列都不改寫。
	before(t, "terra.go", "TerraHandler", "handleAuthEvent", "newConnectionsPaused", "UserExists")
	before(t, "terra.go", "TerraHandler", "handleAuthEvent", "newConnectionsPaused", "persistTerraConn")
	// 狀態端點要把 sunset 帶給前台。
	if firstCall(t, "terra.go", "TerraHandler", "Status", "sunsetInfo") < 0 {
		t.Error("TerraHandler.Status must expose the sunset state")
	}
	// 既有連線必須照常運作：這些入口不得被 announce 擋下。
	for _, fn := range []string{"Status", "Disconnect", "Import", "WebhookEvent", "processWebhook", "handleDeauthEvent"} {
		callsNothingFrom(t, "terra.go", "TerraHandler", fn, "refuseNewConnection", "newConnectionsPaused")
	}
}

func TestSunsetGuard_StravaEntryPointsAreGated(t *testing.T) {
	before(t, "strava.go", "StravaHandler", "Connect", "refuseNewConnection", "enabled")
	before(t, "strava.go", "StravaHandler", "Connect", "sunsetInfo", "enabled")
	// callback：state 驗證之後（不信任的 state 維持原本的 invalid 導回）、換 token 與寫入之前擋下。
	before(t, "strava.go", "StravaHandler", "Callback", "verifyState", "newConnectionsPaused")
	before(t, "strava.go", "StravaHandler", "Callback", "newConnectionsPaused", "exchangeCode")
	before(t, "strava.go", "StravaHandler", "Callback", "newConnectionsPaused", "Save")
	before(t, "strava.go", "StravaHandler", "Callback", "newConnectionsPaused", "backfill")
	// 換 token 要往返 Strava（逾時 15 秒），期間設定可能剛好切到 announce：落地（Save）前必須再確認一次，
	// 也就是 exchangeCode 之後、Save 之前還要有一次 newConnectionsPaused 呼叫。那裡刻意不撤銷剛換到的授權
	// （撤銷對整個 athlete 生效，會弄壞「重新授權的既有使用者」的既有連線），所以也不得呼叫 deauthorize。
	{
		ex := firstCall(t, "strava.go", "StravaHandler", "Callback", "exchangeCode")
		sv := firstCall(t, "strava.go", "StravaHandler", "Callback", "Save")
		recheck := false
		for _, off := range callOffsets(t, "strava.go", "StravaHandler", "Callback", "newConnectionsPaused") {
			if off > ex && off < sv {
				recheck = true
			}
		}
		if ex < 0 || sv < 0 || !recheck {
			t.Error("StravaHandler.Callback must re-check newConnectionsPaused between exchangeCode and Save")
		}
		callsNothingFrom(t, "strava.go", "StravaHandler", "Callback", "deauthorize")
	}
	if firstCall(t, "strava.go", "StravaHandler", "Status", "sunsetInfo") < 0 {
		t.Error("StravaHandler.Status must expose the sunset state")
	}
	// 既有連線必須照常運作（同步、webhook、背景回填、中斷、已同步活動清單）。
	for _, fn := range []string{"Status", "Sync", "Activities", "Disconnect", "WebhookEvent", "handleActivityEvent", "handleDeauthorizeEvent", "syncRecent", "backfill"} {
		callsNothingFrom(t, "strava.go", "StravaHandler", fn, "refuseNewConnection", "newConnectionsPaused")
	}
}

// persistTerraConn 是唯一的 Terra 落地入口：announce 只能走更新專用 SQL，off 才走 upsert。
func TestSunsetGuard_PersistTerraConnIsTheSingleChokePoint(t *testing.T) {
	files := parseNonTestFiles(t)
	only := func(sel string, want ...string) {
		t.Helper()
		got := callSitesOf(files, sel)
		sort.Strings(want)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("call sites of %s:\n  got  %v\n  want %v\n— every new Terra row-creating path must go through persistTerraConn (gated by the sunset announce)", sel, got, want)
		}
	}
	only("SaveTerraUnlessDirect", "wearable_sunset.go:TerraHandler.persistTerraConn")
	only("UpdateTerraConnIfExists", "wearable_sunset.go:TerraHandler.persistTerraConn")
	// 不經守衛的舊版 SaveTerra 在非測試碼不得有任何呼叫點。
	only("SaveTerra")

	for _, name := range []string{"newConnectionsPaused", "connStore", "UpdateTerraConnIfExists", "SaveTerraUnlessDirect"} {
		if firstCall(t, "wearable_sunset.go", "TerraHandler", "persistTerraConn", name) < 0 {
			t.Errorf("persistTerraConn must call %s", name)
		}
	}
	before(t, "wearable_sunset.go", "TerraHandler", "persistTerraConn", "newConnectionsPaused", "SaveTerraUnlessDirect")
}

// Strava（以及同一個 Repository.Save 的其他使用者）的建列呼叫點：只能是 StravaHandler.Callback（有守門）與
// COROS Partner 的 Callback（不屬於 Terra／Strava，白名單）。
func TestSunsetGuard_StravaSaveOnlyInGatedCallback(t *testing.T) {
	files := parseNonTestFiles(t)
	got := callSitesOf(files, "Save")
	want := []string{
		"coros.go:CorosHandler.Callback",   // COROS Partner OAuth：與 Terra／Strava 無關，刻意不納入公告守門
		"strava.go:StravaHandler.Callback", // 唯一的 Strava 建列點，上面的測試確認它在守門之後
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Repository.Save call sites:\n  got  %v\n  want %v\nA new caller must be reviewed against the Terra/Strava sunset announce rule (no new connections).", got, want)
	}
	// Strava 的 Save 在守門之後（接收者型別與檔案都限定在 strava.go／StravaHandler，不會誤抓 COROS Partner）
	before(t, "strava.go", "StravaHandler", "Callback", "newConnectionsPaused", "Save")
}

// 任何會 INSERT 進 user_integrations 的新程式碼（包含別的套件）都要讓這裡失敗，逼人檢視它是否碰到
// Terra／Strava（目前只有 corosmcp／garmin／repository 三個檔，且 Terra／Strava 的建列只有上面兩個入口）。
func TestSunsetGuard_UserIntegrationsInsertInventory(t *testing.T) {
	re := regexp.MustCompile(`(?is)INSERT\s+INTO\s+user_integrations\b`)
	got := map[string]int{}
	root := filepath.FromSlash("..") // internal/
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if n := len(re.FindAll(b, -1)); n > 0 {
			got[filepath.ToSlash(path)] = n
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"../integration/corosmcp.go":    1, // COROS MCP 直連（provider=coros_mcp）
		"../integration/garmin_repo.go": 2, // SaveGarmin（Garmin 直連）＋ SaveTerraUnlessDirect（唯一的 Terra upsert，只被 persistTerraConn 呼叫）
		"../integration/repository.go":  2, // Save（Strava／COROS Partner，見上）＋ SaveTerra（僅測試用，非測試碼無呼叫點）
	}
	if len(got) != len(want) {
		t.Fatalf("INSERT INTO user_integrations inventory changed:\n  got  %v\n  want %v", got, want)
	}
	for f, n := range want {
		if got[f] != n {
			t.Fatalf("INSERT INTO user_integrations inventory changed:\n  got  %v\n  want %v", got, want)
		}
	}
}

// 兩個建構子必須預設接上 app_settings（否則「忘了在 main.go 接線、守門永遠不生效」）。
func TestSunsetGuard_ConstructorsWireTheSettingsSource(t *testing.T) {
	if firstCall(t, "terra.go", "", "NewTerraHandler", "defaultSunsetSource") < 0 {
		t.Error("NewTerraHandler must default its sunset source via defaultSunsetSource(repo)")
	}
	if firstCall(t, "strava.go", "", "NewStravaHandler", "defaultSunsetSource") < 0 {
		t.Error("NewStravaHandler must default its sunset source via defaultSunsetSource(repo)")
	}
	// 預設來源在 repo 帶資料庫時讀 app_settings（FromSettings），repo=nil 才是恆 off。
	if firstCall(t, "wearable_sunset.go", "", "defaultSunsetSource", "FromSettings") < 0 {
		t.Error("defaultSunsetSource must read app_settings through wearablesunset.FromSettings")
	}
}

// 整合測試不得再用固定的 time.Sleep 等背景 goroutine（機器慢時會空過、快時白等）：唯一允許的 time.Sleep 在輪詢 helper
// sunsetITEventually 裡（每 20ms 檢查一次條件、有期限）。那個檔案帶 integration build tag、預設不編譯，所以這裡直接用
// go/parser 讀原始碼（parser 不看 build tag）。
func TestSunsetGuard_IntegrationTestsPollInsteadOfSleeping(t *testing.T) {
	const file = "wearable_sunset_integration_test.go"
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	sawHelper := false
	for _, d := range af.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		if fd.Name.Name == "sunsetITEventually" {
			sawHelper = true
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Sleep" {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "time" {
					t.Errorf("%s: %s calls time.Sleep at %s — wait with the bounded polling helper sunsetITEventually instead of a fixed sleep",
						file, fd.Name.Name, fset.Position(call.Pos()))
				}
			}
			return true
		})
	}
	if !sawHelper {
		t.Fatalf("%s: the polling helper sunsetITEventually is missing", file)
	}
}

// 過期的註解會誤導下一個改這裡的人：被擋下的回應是 409（wearablesunset.RefusalStatus），本套件任何一行註解或字串
// 都不得再宣稱它回的是 Forbidden 那一個狀態碼。（刻意只抓「提到 refuseNewConnection 的同一行」：規則說明如
// 「不得是 401／403」是對的，不能誤抓。）
func TestSunsetGuard_NoCommentClaimsTheRefusalAnswers403(t *testing.T) {
	claim := regexp.MustCompile(`(?i)(回|返回|respond(s|ing)?( with)?|returns?|answers?( with)?)\s*403`)
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources: %v", err)
	}
	checked := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if !strings.Contains(line, "refuseNewConnection") {
				continue
			}
			checked++
			if claim.MatchString(line) {
				t.Errorf("%s:%d: stale comment — the refusal status is 409 (wearablesunset.RefusalStatus), not the one it claims: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no line mentions refuseNewConnection: the guard scanned nothing")
	}
}

// 回應契約：被擋下的 API 一律由 refuseNewConnection 產生，且狀態碼必須是 wearablesunset.RefusalStatus——
// 不得是 401／403（middleware/ipdaily.go 會計成登入失敗，每日資安報告可能對共用 NAT 的 IP 誤報）、404，也不得是 5xx
// （會進「API 5xx 激增」告警）。直接寫 http.StatusXxx 字面值也不行：狀態碼的唯一來源是 wearablesunset.RefusalStatus。
func TestSunsetGuard_RefusalUsesTheSharedStatusConstant(t *testing.T) {
	src, err := os.ReadFile("wearable_sunset.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "func refuseNewConnection")
	if i < 0 {
		t.Fatal("refuseNewConnection missing")
	}
	body := s[i:]
	if j := strings.Index(body, "\n}\n"); j > 0 {
		body = body[:j]
	}
	if !strings.Contains(body, "wearablesunset.RefusalStatus") {
		t.Fatalf("refuseNewConnection must respond with wearablesunset.RefusalStatus:\n%s", body)
	}
	if regexp.MustCompile(`http\.Status[A-Za-z]+`).MatchString(body) {
		t.Fatalf("refuseNewConnection must not hard-code an http.StatusXxx (single source: wearablesunset.RefusalStatus):\n%s", body)
	}
}
