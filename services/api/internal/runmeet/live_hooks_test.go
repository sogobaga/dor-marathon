package runmeet

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/runmeet/live"
)

// live_hooks_test.go：撤銷掛鉤（契約 §4）——寫入點清單、掛鉤外殼、緊急關閉回呼。
//
// ═══════════════════════════════════════════════════════════════════════════════
// 寫入點對照清單（P1 驗收：grep `run_meet_members`／`run_meets` 所有寫入點）
// ═══════════════════════════════════════════════════════════════════════════════
//
// grep 範圍：services/api 下所有 *.go 中的 INSERT INTO／UPDATE／DELETE FROM run_meet_members|run_meets。
// internal/runmeet 內的清單由下面 TestEveryRunMeetWritePointIsClassifiedForLiveHooks 以 go/parser
// **強制**：新增寫入點卻沒分類（或掛鉤被拿掉）→ 測試紅燈。
//
// 已掛（一律在 DB commit 之後；失敗只記 log、絕不影響主流程；Redis 為 nil 時略過）：
//
//	members.go     Repository.LeaveOrWithdraw  joined→left（自行退出）         → liveRevoke
//	               （「撤回申請」分支 pending 直接刪列：pending 不可能持有 grant，不掛）
//	members.go     Repository.Reject           pending→rejected（契約點名）     → liveRevoke（防禦性）
//	members.go     Repository.Kick             joined→kicked（＝封鎖；KickOne handler 經 memberAction→Kick）→ liveRevoke
//	repository.go  Repository.SetStatus        →cancelled                       → liveMarkDead
//	                                           cancelled→open（恢復）           → liveClearDead（契約未列，見下）
//	repository.go  Repository.SoftDelete       deleted_at                       → liveMarkDead
//	admin.go       Handler.AdminTakedown       hidden_by_admin=TRUE（強制下架） → liveMarkDead
//	admin.go       Handler.AdminRestore        hidden_by_admin=FALSE            → liveClearDead（契約未列，見下）
//
// 契約外的兩處補充（liveClearDead）：契約只列 MarkDead，但「中止後恢復」與「後台取消下架」若不清 dead 旗標，
// 恢復後的團練在 meta TTL（24h）內一直回 410 meet_over。AdminRestore 只在團練「完全可用」
// （未取消、未刪除）時才清，避免把仍是 cancelled 的團練誤清。
//
// 刻意不掛（附理由）：
//
//	Repository.Join / Approve        新增 joined／pending 成員，不會讓任何既有 grant 失效
//	Repository.Unban                 刪除 kicked 列；被踢者在 Kick 當下已撤銷，且 kicked 狀態不可能持有 grant
//	Repository.CreateMeet            新團練＋發起人成員列
//	Repository.UpdateMeet            可改 ends_at（時窗終點）與 no_location（presence-only）。影響以 grant 有界過期
//	                                 （15 分鐘）為上限：下一次 start 重驗時才會套用新時窗／新的 meta.po（start 每次依
//	                                 DB 現況改寫 meta.po）。meet_at 建立後鎖死，不會移動時窗起點。
//	Repository.SetVisibility         hidden_by_owner 不影響 joined 成員（契約 §2／§4：不撤銷）
//	Repository.CreateComment / DeleteComment / SetReaction / RemoveReaction   只更新 comment_count／reaction_count 計數
//	Handler.markReminderSent         只更新 reminder_sent_at
//
// internal/runmeet 以外的寫入（grep 全 repo）：
//
//	internal/adminacct   DELETE FROM users WHERE role='admin'：CASCADE 會連帶刪掉該帳號的成員列與其發起的團練。
//	                     被刪的帳號之 session 立即失效（RequireAuth 擋）；團練被 CASCADE 刪除後其他人的 grant
//	                     最多再存活 15 分鐘，下一次 start 重驗時團練已不存在 → 404。不掛（管理員帳號刪除是罕見操作）。
//	internal/virtualrunner  DELETE FROM users WHERE is_virtual=TRUE：虛擬選手不會是團練成員（ListMembers 排除 is_virtual）。
//
// 單一登入／停權的使用者：RequireAuth 在 /pos 之前就擋下（401），不需要撤銷。
// ═══════════════════════════════════════════════════════════════════════════════

type liveWritePoint struct {
	hooks []string // 函式內必須出現的掛鉤呼叫（空＝刻意不掛）
	why   string
}

var liveWriteChecklist = map[string]liveWritePoint{
	// --- 已掛 ---
	"Repository.LeaveOrWithdraw": {[]string{"liveRevoke"}, "joined→left"},
	"Repository.Reject":          {[]string{"liveRevoke"}, "pending→rejected（契約點名，防禦性）"},
	"Repository.Kick":            {[]string{"liveRevoke"}, "joined→kicked（封鎖）"},
	"Repository.SetStatus":       {[]string{"liveMarkDead", "liveClearDead"}, "→cancelled 終止；cancelled→open 恢復"},
	"Repository.SoftDelete":      {[]string{"liveMarkDead"}, "deleted_at"},
	"Handler.AdminTakedown":      {[]string{"liveMarkDead"}, "hidden_by_admin=TRUE"},
	"Handler.AdminRestore":       {[]string{"liveClearDead"}, "hidden_by_admin=FALSE（團練完全可用時才清 dead）"},

	// --- 刻意不掛 ---
	"Repository.Join":           {nil, "新增成員，不使任何 grant 失效"},
	"Repository.Approve":        {nil, "pending→joined（新增成員）"},
	"Repository.Unban":          {nil, "刪除 kicked 列；Kick 當下已撤銷"},
	"Repository.CreateMeet":     {nil, "新團練＋發起人成員列"},
	"Repository.UpdateMeet":     {nil, "ends_at／no_location 的影響以 grant 15 分鐘有界過期為上限，start 重驗時套用"},
	"Repository.SetVisibility":  {nil, "hidden_by_owner 不影響 joined 成員"},
	"Repository.CreateComment":  {nil, "只更新 comment_count"},
	"Repository.DeleteComment":  {nil, "只更新 comment_count"},
	"Repository.SetReaction":    {nil, "只更新 reaction_count"},
	"Repository.RemoveReaction": {nil, "只更新 reaction_count"},
	"Handler.markReminderSent":  {nil, "只更新 reminder_sent_at"},
}

var runMeetWriteRE = regexp.MustCompile(`(?is)\b(INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+(run_meet_members|run_meets)\b`)

var liveHookNames = map[string]bool{"liveRevoke": true, "liveMarkDead": true, "liveClearDead": true}

// funcName 回 "Repository.Kick"／"Handler.AdminTakedown"／"plainFunc"。
func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	t := fd.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

func TestEveryRunMeetWritePointIsClassifiedForLiveHooks(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	writers := map[string]*ast.FuncDecl{}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", n, err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			writes := false
			ast.Inspect(fd.Body, func(node ast.Node) bool {
				if lit, ok := node.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, err := strconv.Unquote(lit.Value); err == nil && runMeetWriteRE.MatchString(s) {
						writes = true
					}
				}
				return true
			})
			if writes {
				writers[funcName(fd)] = fd
			}
		}
	}
	if len(writers) < 10 {
		t.Fatalf("found only %d writers (%v) — the AST scan is not exercising the package", len(writers), keys(writers))
	}

	for name, fd := range writers {
		cp, ok := liveWriteChecklist[name]
		if !ok {
			t.Errorf("%s writes run_meet_members/run_meets but is not in liveWriteChecklist: classify it as hooked (revoke / mark-dead) "+
				"or intentionally not hooked WITH a reason (see the checklist comment at the top of this file)", name)
			continue
		}
		calls := hookCalls(fd)
		for _, want := range cp.hooks {
			if _, has := calls[want]; !has {
				t.Errorf("%s must call %s after its DB commit (%s) — the hook was removed", name, want, cp.why)
			}
		}
		if len(cp.hooks) == 0 && len(calls) > 0 {
			t.Errorf("%s is classified as not hooked but calls %v — update the checklist", name, keys2(calls))
		}
		// 掛鉤必須在 DB commit「之後」（M1）：函式若有 Commit 呼叫，每個掛鉤的位置都要晚於某個 Commit
		commits := commitPositions(fd)
		for hook, positions := range calls {
			for _, p := range positions {
				if len(commits) > 0 && !anyBefore(commits, p) {
					t.Errorf("%s calls %s before any Commit — hooks must run AFTER the DB commit (M1)", name, hook)
				}
			}
		}
	}
	// 清單裡的項目必須仍然是寫入點（避免清單腐爛）
	for name := range liveWriteChecklist {
		if _, ok := writers[name]; !ok {
			t.Errorf("liveWriteChecklist lists %s but it no longer writes run_meet_members/run_meets — remove the stale entry", name)
		}
	}
}

func hookCalls(fd *ast.FuncDecl) map[string][]token.Pos {
	out := map[string][]token.Pos{}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && liveHookNames[sel.Sel.Name] {
				out[sel.Sel.Name] = append(out[sel.Sel.Name], call.Pos())
			}
		}
		return true
	})
	return out
}

func commitPositions(fd *ast.FuncDecl) []token.Pos {
	var out []token.Pos
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Commit" {
				out = append(out, call.Pos())
			}
		}
		return true
	})
	return out
}

func anyBefore(ps []token.Pos, p token.Pos) bool {
	for _, x := range ps {
		if x < p {
			return true
		}
	}
	return false
}

func keys(m map[string]*ast.FuncDecl) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func keys2(m map[string][]token.Pos) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// 路由註冊：/live/start 掛在 Router() 內（requireEntry 照走），限流 runmeet_live_start 6/分/人。
func TestLiveStartRouteIsRegisteredWithItsRateLimit(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "handler.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var router *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && funcName(fd) == "Handler.Router" {
			router = fd
		}
	}
	if router == nil {
		t.Fatal("Handler.Router not found")
	}
	var usesEntry, foundPost, limited bool
	ast.Inspect(router.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "Use" && len(call.Args) == 1 {
			if a, ok := call.Args[0].(*ast.SelectorExpr); ok && a.Sel.Name == "requireEntry" {
				usesEntry = true
			}
		}
		if sel.Sel.Name == "Post" && len(call.Args) == 2 {
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Value == `"/{id}/live/start"` {
				foundPost = true
				// 接收者必須是 r.With(h.limit("runmeet_live_start", 6, time.Minute))
				if with, ok := sel.X.(*ast.CallExpr); ok {
					if ws, ok := with.Fun.(*ast.SelectorExpr); ok && ws.Sel.Name == "With" && len(with.Args) == 1 {
						if lim, ok := with.Args[0].(*ast.CallExpr); ok && len(lim.Args) == 3 {
							a0, _ := lim.Args[0].(*ast.BasicLit)
							a1, _ := lim.Args[1].(*ast.BasicLit)
							if a0 != nil && a1 != nil && a0.Value == `"runmeet_live_start"` && a1.Value == "6" {
								limited = true
							}
						}
					}
				}
			}
		}
		return true
	})
	if !usesEntry {
		t.Error("Router() must keep r.Use(h.requireEntry) — /live/start sits behind the runmeet entry gate")
	}
	if !foundPost {
		t.Fatal(`Router() has no Post("/{id}/live/start", …)`)
	}
	if !limited {
		t.Error(`/live/start must be wrapped in r.With(h.limit("runmeet_live_start", 6, time.Minute))`)
	}
}

// --- 掛鉤外殼 ---

type fakeHooks struct {
	revoked     [][2]string
	dead        []string
	cleared     []string
	err         error
	panicOn     string
	ctxCanceled bool // 掛鉤執行時 ctx 是否已被取消
	hadDeadline bool
}

func (f *fakeHooks) observe(ctx context.Context) {
	if ctx.Err() != nil {
		f.ctxCanceled = true
	}
	if _, ok := ctx.Deadline(); ok {
		f.hadDeadline = true
	}
}

func (f *fakeHooks) Revoke(ctx context.Context, meetID, uid string) error {
	f.observe(ctx)
	if f.panicOn == "revoke" {
		panic("boom")
	}
	f.revoked = append(f.revoked, [2]string{meetID, uid})
	return f.err
}

func (f *fakeHooks) MarkDead(ctx context.Context, meetID string) error {
	f.observe(ctx)
	if f.panicOn == "mark_dead" {
		panic("boom")
	}
	f.dead = append(f.dead, meetID)
	return f.err
}

func (f *fakeHooks) ClearDead(ctx context.Context, meetID string) error {
	f.observe(ctx)
	f.cleared = append(f.cleared, meetID)
	return f.err
}

func TestHooksNeverFailTheMainOperation(t *testing.T) {
	ctx := context.Background()

	// nil Repository／未接線：整個略過
	var nilRepo *Repository
	nilRepo.liveRevoke(ctx, "m", "u")
	nilRepo.liveMarkDead(ctx, "m")
	nilRepo.liveClearDead(ctx, "m")
	unwired := &Repository{}
	unwired.liveRevoke(ctx, "m", "u")
	unwired.liveMarkDead(ctx, "m")
	unwired.liveClearDead(ctx, "m")

	// 回傳錯誤：只記 log，呼叫端看不到
	fh := &fakeHooks{err: errors.New("redis down")}
	repo := &Repository{live: fh}
	repo.liveRevoke(ctx, "m1", "u1")
	repo.liveMarkDead(ctx, "m2")
	repo.liveClearDead(ctx, "m3")
	if len(fh.revoked) != 1 || fh.revoked[0] != [2]string{"m1", "u1"} || len(fh.dead) != 1 || len(fh.cleared) != 1 {
		t.Fatalf("hooks not invoked: %+v", fh)
	}

	// panic：攔下（不得讓 commit 已完成的請求變成 500）
	fh = &fakeHooks{panicOn: "revoke"}
	repo = &Repository{live: fh}
	repo.liveRevoke(ctx, "m", "u")
	fh.panicOn = "mark_dead"
	repo.liveMarkDead(ctx, "m")
}

// commit 已完成後才呼叫：client 此時斷線（ctx 已取消）不得讓撤銷落空；且要有逾時保護。
func TestHooksRunEvenWhenRequestContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fh := &fakeHooks{}
	repo := &Repository{live: fh}
	repo.liveRevoke(ctx, "m", "u")
	repo.liveMarkDead(ctx, "m")
	if fh.ctxCanceled {
		t.Fatal("hooks must run on a context detached from the (already canceled) request")
	}
	if !fh.hadDeadline {
		t.Fatal("hooks must run with a timeout")
	}
	if len(fh.revoked) != 1 || len(fh.dead) != 1 {
		t.Fatalf("hooks skipped: %+v", fh)
	}
}

// 真實 Store：Repository 掛鉤 → Redis（miniredis）。
func TestRepositoryHooksAgainstRealStore(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	store := live.NewStore(rdb)
	repo := NewRepository(nil)
	repo.SetLiveHooks(store)

	const meet, uid = "6f259465-0f7d-431c-a122-bf21a4e9a8eb", "11111111-2222-3333-4444-555555555555"
	repo.liveMarkDead(context.Background(), meet)
	if mr.HGet("rml:{"+meet+"}:meta", "dead") != "1" {
		t.Fatal("liveMarkDead must set meta.dead")
	}
	repo.liveClearDead(context.Background(), meet)
	if mr.HGet("rml:{"+meet+"}:meta", "dead") != "" {
		t.Fatal("liveClearDead must clear meta.dead")
	}
	// 大寫的路徑參數（isValidUUID 接受）也收斂成同一組 key
	repo.liveRevoke(context.Background(), strings.ToUpper(meet), strings.ToUpper(uid))
	if !mr.Exists("rml:{" + meet + "}:rv:" + uid) {
		t.Fatalf("liveRevoke must write the tombstone under the canonical key; keys = %v", mr.Keys())
	}
}

// --- 緊急關閉（契約 §4 M2）---

// 契約 §9-10：設定回呼寫入／清除 kill。
func TestKillSwitchCallbackSetsAndClearsKillFlag(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	store := live.NewStore(rdb)
	off := registerLiveKillSwitch(store)
	t.Cleanup(off)
	ctx := context.Background()

	appsettings.NotifyChange(ctx, live.EntryStateKey, "hidden")
	if v, _ := mr.Get(live.KillKey); v != "1" {
		t.Fatalf("hidden must SET rml:kill 1, got %q", v)
	}
	for _, v := range []string{"whitelist", "open", "locked", ""} {
		appsettings.NotifyChange(ctx, live.EntryStateKey, "hidden")
		appsettings.NotifyChange(ctx, live.EntryStateKey, v)
		if mr.Exists(live.KillKey) {
			t.Fatalf("value %q must DEL rml:kill", v)
		}
	}
	appsettings.NotifyChange(ctx, live.EntryStateKey, " hidden ")
	if !mr.Exists(live.KillKey) {
		t.Fatal("surrounding whitespace must not defeat the kill switch")
	}

	// 其他設定鍵不得影響 kill 旗標
	appsettings.NotifyChange(ctx, live.EntryStateKey, "whitelist")
	appsettings.NotifyChange(ctx, live.EntryWhitelistKey, "hidden")
	appsettings.NotifyChange(ctx, EntryStateKey, "hidden") // runmeet_entry_state（另一道閘門）
	if mr.Exists(live.KillKey) {
		t.Fatal("only runmeet_live_entry_state may drive the kill flag")
	}

	// 取消註冊後不再動作
	off()
	appsettings.NotifyChange(ctx, live.EntryStateKey, "hidden")
	if mr.Exists(live.KillKey) {
		t.Fatal("unregistered callback must not fire")
	}
}

// NewHandler 在 Redis 可用時接線：回呼已註冊、撤銷掛鉤已注入；rdb 為 nil 時兩者都不接。
func TestNewHandlerWiresKillSwitchAndHooksOnlyWithRedis(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	h := NewHandler(nil, rdb, nil)
	t.Cleanup(h.killSwitchOff)
	if h.repo.live == nil || h.liveStart == nil || h.liveStart.store == nil {
		t.Fatal("NewHandler with Redis must wire the hooks and the /live/start store")
	}
	appsettings.NotifyChange(context.Background(), live.EntryStateKey, "hidden")
	if !mr.Exists(live.KillKey) {
		t.Fatal("NewHandler must register the kill-switch callback")
	}
	appsettings.NotifyChange(context.Background(), live.EntryStateKey, "open")

	nilHandler := NewHandler(nil, nil, nil)
	if nilHandler.repo.live != nil {
		t.Fatal("no Redis → no hooks (must not be a typed-nil interface)")
	}
	if nilHandler.killSwitchOff != nil {
		t.Fatal("no Redis → no kill-switch registration")
	}
	if nilHandler.liveStart == nil || nilHandler.liveStart.store != nil {
		t.Fatal("no Redis → /live/start exists but its store is nil (fails closed with 503)")
	}
}

// 後台設定值的驗證與 live 套件的常數一致（appsettings 不能 import live，所以上限寫了兩次，這裡對照）。
func TestAppSettingsValidatorsMatchLiveConstants(t *testing.T) {
	check := func(key, value string, wantValid bool) {
		t.Helper()
		known, valid := appsettings.ValidateValue(key, value)
		if !known {
			t.Errorf("%s is not registered in appsettings specs (admin Set would return 400 unknown setting)", key)
			return
		}
		if valid != wantValid {
			t.Errorf("%s=%q valid=%v, want %v", key, value, valid, wantValid)
		}
	}
	for _, v := range []string{"", "hidden", "locked", "whitelist", "open"} {
		check(live.EntryStateKey, v, true)
	}
	for _, v := range []string{"off", "shown", "HIDDEN", "yes"} {
		check(live.EntryStateKey, v, false)
	}
	check(live.EntryWhitelistKey, "a@x.com\n#abc123, b@x.com", true)
	check(live.EntryWhitelistKey, strings.Repeat("x", 20001), false)

	check(live.MaxKey, strconv.Itoa(live.MaxLiveCeiling), true)
	check(live.MaxKey, strconv.Itoa(live.MaxLiveCeiling+1), false)
	check(live.MaxKey, "1", true)
	check(live.MaxKey, "0", false)
	check(live.MaxKey, "-5", false)
	check(live.MaxKey, "abc", false)
	check(live.MaxKey, "", true)

	check(live.PreMinutesKey, "0", true)
	check(live.PreMinutesKey, "1440", true)
	check(live.PreMinutesKey, "1441", false)
	check(live.PreMinutesKey, "-1", false)
	check(live.DefaultHoursKey, "1", true)
	check(live.DefaultHoursKey, "24", true)
	check(live.DefaultHoursKey, "0", false)
	check(live.DefaultHoursKey, "25", false)
	check(live.GraceMinutesKey, "0", true)
	check(live.GraceMinutesKey, "1440", true)
	check(live.GraceMinutesKey, "1441", false)

	// 預設值本身必須通過自己的驗證器
	d := live.DefaultSettings()
	check(live.EntryStateKey, d.EntryState, true)
	check(live.MaxKey, strconv.Itoa(d.Max), true)
	check(live.PreMinutesKey, strconv.Itoa(d.PreMinutes), true)
	check(live.DefaultHoursKey, strconv.Itoa(d.DefaultHours), true)
	check(live.GraceMinutesKey, strconv.Itoa(d.GraceMinutes), true)
}

// 入口判定：hidden 對所有人關閉（含超管，與 kill 旗標一致）；其餘狀態超管恆 shown。
func TestLiveEntryFrom(t *testing.T) {
	cases := []struct {
		state, whitelist, email, code string
		super                         bool
		want                          string
	}{
		{"hidden", "", "", "", true, "hidden"},
		{" hidden ", "a@x.com", "a@x.com", "", false, "hidden"},
		{"locked", "", "", "", true, "shown"},
		{"locked", "a@x.com", "a@x.com", "", false, "locked"},
		{"whitelist", "a@x.com", "A@X.COM", "", false, "shown"},
		{"whitelist", "#abc", "", "ABC", false, "shown"},
		{"whitelist", "a@x.com", "z@x.com", "zz", false, "hidden"},
		{"whitelist", "", "", "", true, "shown"},
		{"open", "", "z@x.com", "", false, "shown"},
		{"", "", "z@x.com", "", false, "hidden"},
		{"nonsense", "z@x.com", "z@x.com", "", false, "hidden"},
	}
	for _, c := range cases {
		if got := liveEntryFrom(c.state, c.whitelist, c.email, c.code, c.super); got != c.want {
			t.Errorf("liveEntryFrom(%q,%q,%q,%q,super=%v) = %q, want %q", c.state, c.whitelist, c.email, c.code, c.super, got, c.want)
		}
	}
}

// /live/start 處理檔不得記 log：request body（含 sid、consent_v）與座標都不得出現在任何 log。
// （live_hooks.go 的掛鉤只記操作名稱與 meet／user id，沒有 body、沒有座標。）
func TestLiveStartHandlerFileImportsNoLogger(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "handler_live.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p == "log" || p == "log/slog" || strings.Contains(p, "zerolog") {
			t.Errorf("handler_live.go imports %q — the /live/start handler must never log (request bodies and coordinates must not reach any log)", p)
		}
	}
}

// 確保 fake 滿足介面（編譯期）。
var (
	_ liveHooks     = (*fakeHooks)(nil)
	_ liveHooks     = (*live.Store)(nil)
	_ liveStartRepo = (*fakeLiveRepo)(nil)
	_ liveStartRepo = (*pgLiveRepo)(nil)
)
