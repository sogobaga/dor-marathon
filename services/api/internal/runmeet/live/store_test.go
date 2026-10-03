package live

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// store_test.go：Lua／Store 行為（契約 §9 P1 驗收 3–5、7–11）。用 miniredis（本機沒有 Docker）。

func mk(name string) string { return "rml:{" + testMeet + "}:" + name }

// --- start ---

func TestStartAssignsStableNumbersAndRoster(t *testing.T) {
	e := newEnv(t)
	a, b := newPlayer("小明"), newPlayer("阿華")

	ra := e.start(a, 50, false)
	if ra.Outcome != OutcomeOK || ra.N != 1 || ra.RV != 1 || ra.Live != 0 {
		t.Fatalf("first start: %+v", ra)
	}
	if len(ra.Roster) != 1 || ra.Roster[0] != (RosterEntry{N: 1, Name: "小明"}) {
		t.Fatalf("roster: %+v", ra.Roster)
	}
	if ra.ExpMs-ra.NowMs != GrantTTL.Milliseconds() {
		t.Fatalf("grant ttl = %d ms, want %d", ra.ExpMs-ra.NowMs, GrantTTL.Milliseconds())
	}
	if ra.NowMs != e.nowMs() {
		t.Fatalf("nowMs = %d, want redis TIME %d", ra.NowMs, e.nowMs())
	}

	rb := e.start(b, 50, false)
	if rb.N != 2 || rb.RV != 2 || len(rb.Roster) != 2 {
		t.Fatalf("second start: %+v", rb)
	}
	if rb.Roster[0].N != 1 || rb.Roster[1].N != 2 {
		t.Fatalf("roster must be sorted by n: %+v", rb.Roster)
	}

	// 同一個人再 start（靜默重驗／新一趟）：n 不變、名冊版本不變（永不重用）
	ra2 := e.start(a, 50, false)
	if ra2.N != 1 || ra2.RV != 2 {
		t.Fatalf("restart must keep n and rv: %+v", ra2)
	}
}

// 契約 §9-7：51 個 uid 併發 start 只有 50 個成功（Lua 原子）。
func TestCapacityRace51ConcurrentStartsExactly50Succeed(t *testing.T) {
	e := newEnv(t)
	const total, max = 51, 50
	players := make([]player, total)
	for i := range players {
		players[i] = newPlayer(fmt.Sprintf("跑者%02d", i))
	}
	results := make([]StartResult, total)
	errs := make([]error, total)
	var wg sync.WaitGroup
	gate := make(chan struct{})
	for i := range players {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			results[i], errs[i] = e.store.Start(context.Background(), testMeet, StartParams{
				UID: players[i].uid, SID: players[i].sid, Name: players[i].name,
				MaxLive: max, CheckedAtMs: e.nowMs(),
			})
		}(i)
	}
	close(gate)
	wg.Wait()

	ok, full := 0, 0
	nums := map[int]bool{}
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("player %d: %v", i, errs[i])
		}
		switch results[i].Outcome {
		case OutcomeOK:
			ok++
			if nums[results[i].N] {
				t.Fatalf("duplicate n %d", results[i].N)
			}
			nums[results[i].N] = true
		case OutcomeLiveFull:
			full++
		default:
			t.Fatalf("unexpected outcome %v", results[i].Outcome)
		}
	}
	if ok != max || full != total-max {
		t.Fatalf("ok=%d full=%d, want %d/%d", ok, full, max, total-max)
	}
	for n := 1; n <= max; n++ {
		if !nums[n] {
			t.Fatalf("n=%d missing: numbers must be 1..%d without gaps (rejected start must not consume a number)", n, max)
		}
	}
	if got := len(e.hkeys(mk("names"))); got != max {
		t.Fatalf("names has %d entries, want %d (rejected player must not appear in the roster)", got, max)
	}
	if got := len(e.hkeys(mk("grants"))); got != max {
		t.Fatalf("grants has %d entries, want %d", got, max)
	}
}

// 契約 §9-7：過期 grant 不佔名額；已持有 grant 者在滿額時仍可重驗。
func TestExpiredGrantsDoNotCountTowardCapacity(t *testing.T) {
	e := newEnv(t)
	a, b, c := newPlayer("A"), newPlayer("B"), newPlayer("C")
	if e.start(a, 2, false).Outcome != OutcomeOK || e.start(b, 2, false).Outcome != OutcomeOK {
		t.Fatal("first two must succeed")
	}
	if got := e.start(c, 2, false); got.Outcome != OutcomeLiveFull {
		t.Fatalf("third start at max=2: %v, want live_full", got.Outcome)
	}
	// 滿額時，已持有 grant 的人重驗不算新增（reauth 不能被名額擋掉）
	if got := e.start(a, 2, false); got.Outcome != OutcomeOK || got.N != 1 {
		t.Fatalf("holder restart at full: %+v", got)
	}

	e.advance(GrantTTL + time.Second) // a、b 的 grant 都過期
	got := e.start(c, 2, false)
	if got.Outcome != OutcomeOK {
		t.Fatalf("after expiry c must fit: %v", got.Outcome)
	}
	if keys := e.hkeys(mk("grants")); len(keys) != 1 || keys[0] != c.uid {
		t.Fatalf("expired grants must be swept; grants = %v", keys)
	}
	// c 之後 n 仍接續（a=1,b=2,c 為第三個被分配編號者；c 先前被拒絕時沒占號）
	if got.N != 3 {
		t.Fatalf("c.n = %d, want 3", got.N)
	}
}

func TestGrantIsBoundedAndPosAfterExpiryIsGrantMissing(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	e.advance(GrantTTL - time.Second)
	if got := e.pos(a, &[2]float64{25.0, 121.5}, 0); got.Outcome != OutcomeOK && got.Outcome != OutcomeNeedOwnFix {
		t.Fatalf("pos just before expiry: %v", got.Outcome)
	}
	e.advance(GrantTTL) // 遠超過期限（不重驗）
	got := e.posNoAdvance(a, nil, 0)
	if got.Outcome != OutcomeGrantMissing {
		t.Fatalf("pos after expiry: %v, want grant_missing", got.Outcome)
	}
	if e.mr.Exists(mk("rv:" + a.uid)) {
		t.Fatal("grant expiry must not write a tombstone")
	}
	if e.mr.HGet(mk("grants"), a.uid) != "" {
		t.Fatal("expired grant must be removed by the script")
	}
}

// --- pos：sid ---

func TestSIDMismatchAndTabTakeover(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	other := a
	other.sid = "OTHERTABSID0000001" // 另一個分頁／裝置

	if got := e.posNoAdvance(other, nil, 0); got.Outcome != OutcomeSIDMismatch {
		t.Fatalf("pos with other sid: %v, want sid_mismatch", got.Outcome)
	}
	// 另一個分頁 start 接手 → 舊 sid 之後一律 sid_mismatch
	e.start(other, 50, false)
	e.advance(2 * time.Second)
	if got := e.posNoAdvance(a, nil, 0); got.Outcome != OutcomeSIDMismatch {
		t.Fatalf("old tab after takeover: %v, want sid_mismatch", got.Outcome)
	}
	e.advance(2 * time.Second)
	if got := e.posNoAdvance(other, nil, 0); got.Outcome != OutcomeNeedOwnFix {
		t.Fatalf("new tab: %v", got.Outcome)
	}
}

// --- leave ---

// 契約 §9-8：leave 帶錯 sid 不刪；leave 後再 start 正常（無墓碑）。
func TestLeaveWrongSIDKeepsGrantAndRightSIDClearsWithoutTombstone(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	e.pos(a, &[2]float64{25.03321, 121.56543}, 0)
	ctx := context.Background()

	wrong, err := e.store.Leave(ctx, testMeet, a.uid, "WRONGSID000000001")
	if err != nil || wrong {
		t.Fatalf("leave with wrong sid: deleted=%v err=%v", wrong, err)
	}
	if e.mr.HGet(mk("grants"), a.uid) == "" || !e.mr.Exists(mk("pos")) || !e.mr.Exists(mk("hb")) {
		t.Fatal("leave with wrong sid must not delete anything")
	}
	if got := e.posNoAdvance(a, nil, 0); got.Outcome == OutcomeGrantMissing || got.Outcome == OutcomeRevoked {
		t.Fatalf("grant must survive a wrong-sid leave: %v", got.Outcome)
	}
	e.advance(2 * time.Second)

	right, err := e.store.Leave(ctx, testMeet, a.uid, a.sid)
	if err != nil || !right {
		t.Fatalf("leave with right sid: deleted=%v err=%v", right, err)
	}
	if e.mr.HGet(mk("grants"), a.uid) != "" {
		t.Fatal("grant must be deleted")
	}
	if e.mr.Exists(mk("pos")) || e.mr.Exists(mk("hb")) {
		t.Fatalf("pos/hb entries of the leaver must be deleted (pos exists=%v hb exists=%v)", e.mr.Exists(mk("pos")), e.mr.Exists(mk("hb")))
	}
	if e.mr.Exists(mk("rv:" + a.uid)) {
		t.Fatal("leave must NOT write a tombstone (M5)")
	}
	// leave 之後 /pos 是 grant_missing（不是 revoked），再 start 正常
	if got := e.posNoAdvance(a, nil, 0); got.Outcome != OutcomeGrantMissing {
		t.Fatalf("pos after leave: %v, want grant_missing", got.Outcome)
	}
	if got := e.start(a, 50, false); got.Outcome != OutcomeOK || got.N != 1 {
		t.Fatalf("start after leave: %+v", got)
	}
	e.advance(2 * time.Second)
	if got := e.posNoAdvance(a, &[2]float64{25.0, 121.5}, 0); got.Outcome != OutcomeOK {
		t.Fatalf("pos after restart: %v", got.Outcome)
	}
}

// M5：跑完立刻開下一趟，晚到的舊 sid leave 不能砍掉新 grant。
func TestLateLeaveOfOldSessionDoesNotKillNewGrant(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	newRun := a
	newRun.sid = "NEWRUNSID00000001"
	e.start(newRun, 50, false) // 新一趟先到
	if deleted, err := e.store.Leave(context.Background(), testMeet, a.uid, a.sid); err != nil || deleted {
		t.Fatalf("late leave(old sid): deleted=%v err=%v", deleted, err)
	}
	e.advance(2 * time.Second)
	if got := e.posNoAdvance(newRun, nil, 0); got.Outcome != OutcomeNeedOwnFix {
		t.Fatalf("new run must stay valid: %v", got.Outcome)
	}
}

// --- 撤銷（Revoke）---

// 契約 §9-3：Kick／退出／拒絕後下一次 /pos → revoked，且該亮點已從他人快照消失。
func TestRevokeThenPosIsRevokedAndDotVanishesFromSnapshots(t *testing.T) {
	e := newEnv(t)
	a, b := newPlayer("A"), newPlayer("B")
	e.start(a, 50, false)
	e.start(b, 50, false)
	e.pos(a, &[2]float64{25.03321, 121.56543}, 0)
	e.pos(b, &[2]float64{25.03401, 121.56612}, 0)
	if got := e.pos(a, &[2]float64{25.03321, 121.56543}, 0); len(got.Peers) != 1 || got.Peers[0].N != 2 {
		t.Fatalf("precondition: a sees b: %+v", got.Peers)
	}

	if err := e.store.Revoke(context.Background(), testMeet, b.uid); err != nil {
		t.Fatal(err)
	}
	// 被撤銷者：grant／pos／hb 都沒了
	if e.mr.HGet(mk("grants"), b.uid) != "" || e.mr.HGet(mk("pos"), "2") != "" || e.mr.HGet(mk("hb"), "2") != "" {
		t.Fatal("revoke must clear grant, pos and hb of the revoked user")
	}
	if got := e.posNoAdvance(b, &[2]float64{25.03401, 121.56612}, 0); got.Outcome != OutcomeRevoked {
		t.Fatalf("revoked user's pos: %v, want revoked", got.Outcome)
	}
	// 別人的快照：亮點已消失
	got := e.posNoAdvance(a, &[2]float64{25.03321, 121.56543}, 0)
	if got.Outcome != OutcomeOK || len(got.Peers) != 0 {
		t.Fatalf("a's snapshot after revoking b: %+v", got)
	}
	// 被撤銷者的 start 也被擋（M1 競態：checkedAt 早於墓碑）
	late := e.store
	res, err := late.Start(context.Background(), testMeet, StartParams{
		UID: b.uid, SID: b.sid, Name: b.name, MaxLive: 50, CheckedAtMs: e.nowMs() - 1000,
	})
	if err != nil || res.Outcome != OutcomeRevokedRecent {
		t.Fatalf("start with stale checkedAt: %+v err=%v", res, err)
	}
}

// 契約 §9-9：Revoke 的時間 ≥ checkedAtMs → start 被拒；重新加入後（checkedAt 晚於墓碑）可 start，
// 且墓碑被清掉、/pos 不會再把新 grant 判成 revoked。
func TestRevokedRecentBoundaryAndRejoinFlow(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	ctx := context.Background()
	revokedAt := e.nowMs()
	if err := e.store.Revoke(ctx, testMeet, a.uid); err != nil { // 沒 start 過也要寫墓碑（start 競態中的新人）
		t.Fatal(err)
	}
	if got := e.get(mk("rv:" + a.uid)); got != fmt.Sprint(revokedAt) {
		t.Fatalf("tombstone = %q, want revokedAtMs %d (Redis TIME)", got, revokedAt)
	}

	try := func(checkedAt int64) Outcome {
		res, err := e.store.Start(ctx, testMeet, StartParams{
			UID: a.uid, SID: a.sid, Name: a.name, MaxLive: 50, CheckedAtMs: checkedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		return res.Outcome
	}
	if got := try(revokedAt - 1); got != OutcomeRevokedRecent {
		t.Fatalf("checkedAt < revokedAt: %v, want revoked_recent", got)
	}
	// 同一毫秒無法判斷先後 → 保守拒絕（契約寫 >，這裡用 ≥，見 startScript 註解）
	if got := try(revokedAt); got != OutcomeRevokedRecent {
		t.Fatalf("checkedAt == revokedAt: %v, want revoked_recent (conservative tie)", got)
	}
	if e.mr.HGet(mk("grants"), a.uid) != "" {
		t.Fatal("rejected start must not write a grant")
	}

	// 重新加入：DB 在撤銷之後才讀 → checkedAt 晚於墓碑
	e.advance(5 * time.Second)
	if got := try(e.nowMs()); got != OutcomeOK {
		t.Fatalf("checkedAt after revoke: %v, want ok", got)
	}
	if e.mr.Exists(mk("rv:" + a.uid)) {
		t.Fatal("successful start must delete the tombstone (it already did its job)")
	}
	e.advance(2 * time.Second)
	if got := e.posNoAdvance(a, &[2]float64{25.0, 121.5}, 0); got.Outcome != OutcomeOK {
		t.Fatalf("pos after rejoin+start: %v, want ok (must not be treated as revoked)", got.Outcome)
	}
}

// 墓碑存活期內，被撤銷者且沒有 grant → revoked（不是 grant_missing）；墓碑過期後 → grant_missing。
func TestTombstoneTTLThenGrantMissing(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	if err := e.store.Revoke(context.Background(), testMeet, a.uid); err != nil {
		t.Fatal(err)
	}
	if ttl := e.mr.TTL(mk("rv:" + a.uid)); ttl != TombstoneTTL {
		t.Fatalf("tombstone ttl = %v, want %v", ttl, TombstoneTTL)
	}
	e.advance(TombstoneTTL - time.Second)
	if got := e.posNoAdvance(a, nil, 0); got.Outcome != OutcomeRevoked {
		t.Fatalf("within 15 min: %v, want revoked", got.Outcome)
	}
	e.advance(2 * time.Second)
	if got := e.posNoAdvance(a, nil, 0); got.Outcome != OutcomeGrantMissing {
		t.Fatalf("after tombstone expiry: %v, want grant_missing", got.Outcome)
	}
}

// --- kill／dead ---

// 契約 §9-10：kill key SET 後下一次 /pos → killed。
func TestKillKey(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	e.pos(a, &[2]float64{25.0, 121.5}, 0)

	e.mr.Set(KillKey, "1") // 直接寫 Redis（模擬別的副本／維運手動 SET）
	if got := e.posNoAdvance(a, nil, 0); got.Outcome != OutcomeKilled {
		t.Fatalf("pos with kill key: %v, want killed", got.Outcome)
	}
	if res, _ := e.store.Start(context.Background(), testMeet, StartParams{
		UID: a.uid, SID: a.sid, Name: "A", MaxLive: 50, CheckedAtMs: e.nowMs(),
	}); res.Outcome != OutcomeKilled {
		t.Fatalf("start with kill key: %v, want killed", res.Outcome)
	}

	// 無 grant 的陌生人看不出 kill（先看 grant：一律 grant_missing，不洩漏狀態）
	stranger := newPlayer("S")
	if got := e.posNoAdvance(stranger, nil, 0); got.Outcome != OutcomeGrantMissing {
		t.Fatalf("stranger pos with kill key: %v, want grant_missing", got.Outcome)
	}

	// SetKill(false) 解除；SetKill(true) 再關
	if err := e.store.SetKill(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if e.mr.Exists(KillKey) {
		t.Fatal("SetKill(false) must DEL the key")
	}
	e.advance(2 * time.Second)
	if got := e.posNoAdvance(a, nil, 0); got.Outcome != OutcomeOK {
		t.Fatalf("pos after kill cleared: %v", got.Outcome)
	}
	if err := e.store.SetKill(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if e.get(KillKey) != "1" {
		t.Fatal("SetKill(true) must SET rml:kill 1")
	}
	if ttl := e.mr.TTL(KillKey); ttl != 0 {
		t.Fatalf("kill key must have no TTL, got %v", ttl)
	}
}

// 契約 §9-3：SetStatus(cancelled)／SoftDelete／後台下架後 /pos → 410（dead）。
func TestMarkDeadAndClearDead(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	ctx := context.Background()
	e.start(a, 50, false)
	e.pos(a, &[2]float64{25.0, 121.5}, 0)

	if err := e.store.MarkDead(ctx, testMeet); err != nil {
		t.Fatal(err)
	}
	if e.mr.HGet(mk("meta"), "dead") != "1" || e.mr.TTL(mk("meta")) != KeyTTL {
		t.Fatalf("meta.dead=%q ttl=%v", e.mr.HGet(mk("meta"), "dead"), e.mr.TTL(mk("meta")))
	}
	if got := e.posNoAdvance(a, nil, 0); got.Outcome != OutcomeDead {
		t.Fatalf("pos on dead meet: %v, want dead", got.Outcome)
	}
	if res, _ := e.store.Start(ctx, testMeet, StartParams{
		UID: a.uid, SID: a.sid, Name: "A", MaxLive: 50, CheckedAtMs: e.nowMs(),
	}); res.Outcome != OutcomeDead {
		t.Fatalf("start on dead meet: %v, want dead", res.Outcome)
	}

	// 恢復（cancelled→open／後台取消下架）
	if err := e.store.ClearDead(ctx, testMeet); err != nil {
		t.Fatal(err)
	}
	e.advance(2 * time.Second)
	if got := e.posNoAdvance(a, nil, 0); got.Outcome != OutcomeOK && got.Outcome != OutcomeNeedOwnFix {
		t.Fatalf("pos after ClearDead: %v", got.Outcome)
	}
}

// --- presence-only ---

// 契約 §9-4：presence-only 團永不存 pos、永不回 p。
func TestPresenceOnlyNeverStoresPositionNorReturnsP(t *testing.T) {
	e := newEnv(t)
	a, b := newPlayer("A"), newPlayer("B")
	e.start(a, 50, true)
	e.start(b, 50, true)
	if e.mr.HGet(mk("meta"), "po") != "1" {
		t.Fatal("start(po=true) must set meta.po=1")
	}

	for i := 0; i < 3; i++ {
		ra := e.pos(a, &[2]float64{25.03321, 121.56543}, 0)
		rb := e.pos(b, &[2]float64{25.03401, 121.56612}, 0)
		for _, r := range []PosResult{ra, rb} {
			if r.Outcome != OutcomePresenceOnly {
				t.Fatalf("outcome = %v, want presence_only", r.Outcome)
			}
			if len(r.Peers) != 0 {
				t.Fatalf("presence-only must never return p: %+v", r.Peers)
			}
		}
		if rb.Live != 2 {
			t.Fatalf("live = %d, want 2 (heartbeats still counted)", rb.Live)
		}
	}
	if e.mr.Exists(mk("pos")) {
		t.Fatal("presence-only: the pos hash must never exist")
	}
	// Redis 任何地方都沒有座標（5 位小數換算後的整數與原始小數都不出現）
	dump := e.dumpRedis()
	for _, needle := range []string{"2503321", "12156543", "2503401", "12156612", "25.033", "121.565"} {
		if strings.Contains(dump, needle) {
			t.Fatalf("coordinate fragment %q found in Redis:\n%s", needle, dump)
		}
	}
}

// start 每次依 DB 現況改寫 meta.po（no_location 被編輯後，下一次 start 就會翻轉）。
func TestStartRewritesPresenceOnlyFlag(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	e.pos(a, &[2]float64{25.0, 121.5}, 0)
	if got := e.pos(a, nil, 0); got.Outcome != OutcomeOK {
		t.Fatalf("normal meet: %v", got.Outcome)
	}
	e.start(a, 50, true) // 團練改成不限地點，下一次重驗翻轉
	if got := e.pos(a, nil, 0); got.Outcome != OutcomePresenceOnly || len(got.Peers) != 0 {
		t.Fatalf("after flip to presence-only: %+v", got)
	}
	e.start(a, 50, false)
	e.pos(a, &[2]float64{25.0, 121.5}, 0)
	if got := e.pos(a, nil, 0); got.Outcome != OutcomeOK {
		t.Fatalf("after flip back: %v", got.Outcome)
	}
}

// --- 互惠規則 C1 ---

// 契約 §9-4：請求者無 ≤60 s 位置 → p=[]、own=need_own_fix。
func TestReciprocity(t *testing.T) {
	e := newEnv(t)
	a, b := newPlayer("A"), newPlayer("B")
	e.start(a, 50, false)
	e.start(b, 50, false)

	// A 分享位置；B 只送心跳（沒分享就沒得看）
	ra := e.pos(a, &[2]float64{25.03321, 121.56543}, 0)
	if ra.Outcome != OutcomeOK || len(ra.Peers) != 0 {
		t.Fatalf("a alone: %+v", ra)
	}
	rb := e.pos(b, nil, 0)
	if rb.Outcome != OutcomeNeedOwnFix || len(rb.Peers) != 0 {
		t.Fatalf("b heartbeat-only must not see a: %+v", rb)
	}
	// B 的心跳不會讓 A 看到什麼（B 沒有 pos）
	if ra2 := e.pos(a, &[2]float64{25.03321, 121.56543}, 0); len(ra2.Peers) != 0 {
		t.Fatalf("a sees heartbeat-only b: %+v", ra2.Peers)
	}
	// B 開始分享 → 互相看得到
	rb2 := e.pos(b, &[2]float64{25.03401, 121.56612}, 0)
	if rb2.Outcome != OutcomeOK || len(rb2.Peers) != 1 || rb2.Peers[0].N != 1 {
		t.Fatalf("b after sharing: %+v", rb2)
	}
	// A 只送心跳，但自己 ≤60 s 內有位置 → 仍看得到 B
	ra3 := e.pos(a, nil, 0)
	if ra3.Outcome != OutcomeOK || len(ra3.Peers) != 1 || ra3.Peers[0].N != 2 {
		t.Fatalf("a heartbeat within 60s of own fix: %+v", ra3)
	}

	// 自己的位置超過 60 s → 立刻變 need_own_fix（即使他人的 pos 還沒到 180 s 被丟棄）
	e.advance(61 * time.Second)
	ra4 := e.pos(a, nil, 0)
	if ra4.Outcome != OutcomeNeedOwnFix || len(ra4.Peers) != 0 {
		t.Fatalf("a with a >60s-old own fix: %+v", ra4)
	}
	// B 重新分享 → B 看得到 A（A 的位置已 >60 s 但 ≤180 s，仍在快照內，age 如實回報）
	rb3 := e.pos(b, &[2]float64{25.03401, 121.56612}, 0)
	if rb3.Outcome != OutcomeOK || len(rb3.Peers) != 1 {
		t.Fatalf("b after re-share: %+v", rb3)
	}
	if age := rb3.Peers[0].AgeMs; age < 60_000 || age > 70_000 {
		t.Fatalf("a's dot age = %d ms, want ~63000", age)
	}
}

// --- 快照內容 ---

// 契約 §9-5：快照不含自己、不含 > drop 者（並順手 HDEL）。
func TestSnapshotExcludesSelfAndDropsStale(t *testing.T) {
	e := newEnv(t)
	a, b, c := newPlayer("A"), newPlayer("B"), newPlayer("C")
	for _, p := range []player{a, b, c} {
		e.start(p, 50, false)
	}
	e.pos(a, &[2]float64{25.1, 121.1}, 0)
	e.pos(b, &[2]float64{25.2, 121.2}, 0)
	e.pos(c, &[2]float64{25.3, 121.3}, 0)

	// 全員仍新鮮：每人只看到另外兩位，永遠不含自己
	snap := e.pos(a, &[2]float64{25.1, 121.1}, 0)
	if snap.Outcome != OutcomeOK || len(snap.Peers) != 2 {
		t.Fatalf("a snapshot: %+v", snap)
	}
	for _, p := range snap.Peers {
		if p.N == 1 {
			t.Fatalf("snapshot contains the requester's own dot: %+v", snap.Peers)
		}
	}

	// C 之後不再回報；過 DropS 後 A、B 持續回報 → C 被丟棄（且從 Redis 刪除）
	e.advance(time.Duration(DropS)*time.Second - 3*time.Second)
	e.pos(a, &[2]float64{25.1, 121.1}, 0)
	e.pos(b, &[2]float64{25.2, 121.2}, 0)
	e.advance(5 * time.Second) // C 的位置年齡 > 180 s
	got := e.pos(a, &[2]float64{25.1, 121.1}, 0)
	if got.Outcome != OutcomeOK {
		t.Fatalf("outcome %v", got.Outcome)
	}
	for _, p := range got.Peers {
		if p.N == 3 {
			t.Fatalf("stale dot (age %d ms) still in snapshot", p.AgeMs)
		}
		if p.AgeMs > int64(DropS)*1000 {
			t.Fatalf("peer older than drop: %+v", p)
		}
	}
	if len(got.Peers) != 1 || got.Peers[0].N != 2 {
		t.Fatalf("snapshot = %+v, want only b", got.Peers)
	}
	if e.mr.HGet(mk("pos"), "3") != "" {
		t.Fatal("stale dot must be HDEL'd when read")
	}
}

// 伺服器時間戳 = Redis TIME − fa（不信任 client 時鐘）。
func TestPositionAgeUsesServerTimeMinusFA(t *testing.T) {
	e := newEnv(t)
	a, b := newPlayer("A"), newPlayer("B")
	e.start(a, 50, false)
	e.start(b, 50, false)
	if _, err := e.store.Pos(context.Background(), testMeet, PosParams{
		UID: a.uid, SID: a.sid, HasP: true, LaE5: 2503321, LnE5: 12156543, Acc: 8, FaS: 30,
	}); err != nil {
		t.Fatal(err)
	}
	e.advance(2 * time.Second)
	got, err := e.store.Pos(context.Background(), testMeet, PosParams{
		UID: b.uid, SID: b.sid, HasP: true, LaE5: 2503401, LnE5: 12156612, Acc: 6, FaS: 0,
	})
	if err != nil || len(got.Peers) != 1 {
		t.Fatalf("b snapshot: %+v err=%v", got, err)
	}
	if got.Peers[0].AgeMs != 32_000 || got.Peers[0].Acc != 8 {
		t.Fatalf("peer = %+v, want age 32000 ms (30 s fix age + 2 s) and acc 8", got.Peers[0])
	}
}

// 回歸：Lua pattern 的 `-` 是 lazy 量詞，`|-?` 會悄悄壞掉；南半球／西半球（負座標）必須來回一致。
func TestNegativeCoordinatesRoundTrip(t *testing.T) {
	e := newEnv(t)
	a, b := newPlayer("A"), newPlayer("B")
	e.start(a, 50, false)
	e.start(b, 50, false)
	e.pos(a, &[2]float64{-34.60372, -58.38159}, 0) // 布宜諾斯艾利斯
	got := e.pos(b, &[2]float64{-33.86882, 151.20929}, 0)
	if got.Outcome != OutcomeOK || len(got.Peers) != 1 {
		t.Fatalf("b must see a: %+v", got)
	}
	if p := got.Peers[0]; p.LaE5 != -3460372 || p.LnE5 != -5838159 {
		t.Fatalf("negative coordinates corrupted: %+v", p)
	}
	// 回頭：A 看 B（B 的緯度負、經度正）
	back := e.pos(a, &[2]float64{-34.60372, -58.38159}, 0)
	if len(back.Peers) != 1 || back.Peers[0].LaE5 != -3386882 || back.Peers[0].LnE5 != 15120929 {
		t.Fatalf("a's view of b: %+v", back.Peers)
	}
}

// --- 頻率地板、live 計數、名冊版本 ---

func TestFrequencyFloor(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	if got := e.posNoAdvance(a, &[2]float64{25.0, 121.5}, 0); got.Outcome != OutcomeOK {
		t.Fatalf("first pos: %v", got.Outcome)
	}
	got := e.posNoAdvance(a, &[2]float64{25.0, 121.5}, 0) // 同一毫秒再來一次
	if got.Outcome != OutcomeTooFast || got.RetryAfterMs <= 0 || got.RetryAfterMs > FloorMs {
		t.Fatalf("second pos within the floor: %+v", got)
	}
	e.advance(1499 * time.Millisecond)
	if got := e.posNoAdvance(a, nil, 0); got.Outcome != OutcomeTooFast {
		t.Fatalf("1499 ms later: %v, want too_fast", got.Outcome)
	}
	e.advance(1 * time.Millisecond) // 滿 1.5 s
	if got := e.posNoAdvance(a, nil, 0); got.Outcome == OutcomeTooFast {
		t.Fatalf("1500 ms later: still too_fast")
	}
}

// 被地板擋下的請求不得改動任何狀態（不寫心跳、不寫位置）。
func TestTooFastDoesNotWriteState(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	e.posNoAdvance(a, nil, 0) // 只心跳
	if e.mr.Exists(mk("pos")) {
		t.Fatal("heartbeat-only must not create pos")
	}
	if got := e.posNoAdvance(a, &[2]float64{25.0, 121.5}, 0); got.Outcome != OutcomeTooFast {
		t.Fatalf("%v", got.Outcome)
	}
	if e.mr.Exists(mk("pos")) {
		t.Fatal("a too_fast request must not write the position")
	}
}

func TestLiveCountsHeartbeatsWithin60s(t *testing.T) {
	e := newEnv(t)
	a, b, c := newPlayer("A"), newPlayer("B"), newPlayer("C")
	for _, p := range []player{a, b, c} {
		e.start(p, 50, false)
	}
	e.pos(a, nil, 0) // 只心跳也算 live（不論有無 p）
	e.pos(b, &[2]float64{25.0, 121.5}, 0)
	if got := e.pos(c, nil, 0); got.Live != 3 {
		t.Fatalf("live = %d, want 3", got.Live)
	}
	e.advance(55 * time.Second)
	e.pos(b, nil, 0) // 只有 b 持續
	e.advance(10 * time.Second)
	// a、c 的心跳已 65 s 前 → 不算；b 剛剛才送
	if got := e.pos(b, nil, 0); got.Live != 1 {
		t.Fatalf("live = %d, want 1 (a and c are >60 s old)", got.Live)
	}
	// start 回應的 live 同樣數 hb ≤60 s
	if res := e.start(a, 50, false); res.Live != 1 {
		t.Fatalf("start.live = %d, want 1", res.Live)
	}
}

func TestRosterOnlyWhenRVDiffers(t *testing.T) {
	e := newEnv(t)
	a, b := newPlayer("A"), newPlayer("B")
	e.start(a, 50, false)
	e.start(b, 50, false)

	same := e.pos(a, nil, 2)
	if same.RosterSent || len(same.Roster) != 0 || same.RV != 2 {
		t.Fatalf("matching rv must not resend the roster: %+v", same)
	}
	diff := e.pos(a, nil, 1)
	if !diff.RosterSent || len(diff.Roster) != 2 || diff.Roster[0].N != 1 || diff.Roster[1].N != 2 {
		t.Fatalf("stale rv must resend the full roster sorted by n: %+v", diff)
	}
	zero := e.pos(a, nil, 0)
	if !zero.RosterSent {
		t.Fatal("rv=0 (omitted) must receive the roster")
	}
	ahead := e.pos(a, nil, 99) // Redis 被清空後 rv 變小，client 的 rv 反而比較大
	if !ahead.RosterSent {
		t.Fatal("any rv difference (including client ahead of server) must resend the roster")
	}
}

// 名冊版本 rv 是單調遞增計數器（meta.rv）：新編號或既有成員改名時 +1。
// 舊實作用 HLEN names——既有成員改名時 HLEN 不變，其他 client 帶的 rv 與伺服器相同，永遠收不到新名字。
func TestRosterVersionBumpsWhenAMemberRenames(t *testing.T) {
	e := newEnv(t)
	a, b := newPlayer("小明"), newPlayer("阿華")
	if r := e.start(a, 50, false); r.RV != 1 {
		t.Fatalf("first member: rv = %d, want 1", r.RV)
	}
	if r := e.start(b, 50, false); r.RV != 2 {
		t.Fatalf("second member: rv = %d, want 2", r.RV)
	}
	if r := e.start(a, 50, false); r.RV != 2 {
		t.Fatalf("re-start with the SAME name must not bump rv: rv = %d, want 2", r.RV)
	}
	if got := e.pos(b, nil, 2); got.RosterSent || got.RV != 2 {
		t.Fatalf("up-to-date client must not receive the roster: %+v", got)
	}

	// a 改名後重新 start（例如個資改名、或下一趟重驗）：rv 前進，b 下一次輪詢就會收到完整名冊與新名字
	a.name = "新名字"
	if r := e.start(a, 50, false); r.RV != 3 {
		t.Fatalf("rename must bump rv: rv = %d, want 3", r.RV)
	}
	if e.mr.HGet(mk("names"), "1") != "新名字" {
		t.Fatalf("names[1] = %q, want the new name", e.mr.HGet(mk("names"), "1"))
	}
	got := e.pos(b, nil, 2) // b 手上還是舊版本 2
	if !got.RosterSent || got.RV != 3 {
		t.Fatalf("a client holding rv=2 must receive the roster after the rename: %+v", got)
	}
	if len(got.Roster) != 2 || got.Roster[0] != (RosterEntry{N: 1, Name: "新名字"}) || got.Roster[1] != (RosterEntry{N: 2, Name: "阿華"}) {
		t.Fatalf("roster after rename: %+v", got.Roster)
	}
	if got := e.pos(b, nil, 3); got.RosterSent {
		t.Fatalf("a client that already holds rv=3 must not receive it again: %+v", got)
	}
	// 改名後再用同一個新名字重驗：不再前進（rv 只在名冊真的變動時才動）
	if r := e.start(a, 50, false); r.RV != 3 {
		t.Fatalf("re-start with the unchanged new name: rv = %d, want 3", r.RV)
	}
	// 被名額擋下的人不會讓 rv 前進（也不占編號）
	c := newPlayer("路人")
	if r := e.start(c, 2, false); r.Outcome != OutcomeLiveFull {
		t.Fatalf("setup: %v", r.Outcome)
	}
	if got := e.pos(b, nil, 3); got.RosterSent || got.RV != 3 {
		t.Fatalf("a rejected start must not bump rv: %+v", got)
	}
}

// 相容舊資料：升級前寫入的 key 沒有 meta.rv（舊版 rv = HLEN names）。/pos 退回 HLEN names；
// 第一次 start 從 HLEN names 接續，rv 不會倒退。
func TestRosterVersionContinuesFromLegacyKeysWithoutMetaRV(t *testing.T) {
	e := newEnv(t)
	legacy := newPlayer("舊成員")
	e.mr.HSet(mk("names"), "1", "甲", "2", "乙", "3", "丙")
	e.mr.HSet(mk("idx"), legacy.uid, "1", uuidFor(2), "2", uuidFor(3), "3")
	e.mr.HSet(mk("grants"), legacy.uid, fmt.Sprintf("1|%s|%d", legacy.sid, e.nowMs()+GrantTTL.Milliseconds()))
	if e.mr.Exists(mk("meta")) {
		t.Fatal("setup: legacy data must not have a meta key")
	}

	got := e.posNoAdvance(legacy, nil, 0)
	if got.Outcome != OutcomeNeedOwnFix || got.RV != 3 {
		t.Fatalf("legacy pos: %+v, want rv = HLEN names = 3", got)
	}

	// 新成員 start：編號 4、rv 從 3 接續到 4（不是從 0 重來——重來會與升級前的 client 手上的舊 rv 撞號）
	d := newPlayer("新成員")
	r := e.start(d, 50, false)
	if r.N != 4 || r.RV != 4 {
		t.Fatalf("new member on legacy data: n=%d rv=%d, want 4/4", r.N, r.RV)
	}
	if e.mr.HGet(mk("meta"), "rv") != "4" {
		t.Fatalf("meta.rv = %q, want 4", e.mr.HGet(mk("meta"), "rv"))
	}
}

// --- Redis 被清空 / 不可用 / nil ---

// 契約 §9-11：Redis 清空 → 409 grant_missing → 重新 start 成功。
func TestRedisFlushThenGrantMissingThenRestart(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	e.pos(a, &[2]float64{25.0, 121.5}, 0)

	e.mr.FlushAll()
	if got := e.posNoAdvance(a, nil, 0); got.Outcome != OutcomeGrantMissing {
		t.Fatalf("after flush: %v, want grant_missing", got.Outcome)
	}
	if got := e.start(a, 50, false); got.Outcome != OutcomeOK || got.N != 1 {
		t.Fatalf("restart after flush: %+v", got)
	}
	e.advance(2 * time.Second)
	if got := e.posNoAdvance(a, &[2]float64{25.0, 121.5}, 0); got.Outcome != OutcomeOK {
		t.Fatalf("pos after restart: %v", got.Outcome)
	}
}

func TestRedisUnavailableFailsClosed(t *testing.T) {
	e := newEnv(t)
	rdb := redis.NewClient(&redis.Options{
		Addr: e.mr.Addr(), DialTimeout: 200 * time.Millisecond, ReadTimeout: 200 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond, MaxRetries: -1,
	})
	defer rdb.Close()
	s := NewStore(rdb)
	e.mr.Close()
	ctx := context.Background()
	if _, err := s.NowMs(ctx); !errors.Is(err, ErrUnavailable) {
		t.Errorf("NowMs: %v", err)
	}
	if _, err := s.Start(ctx, testMeet, StartParams{UID: newPlayer("A").uid, SID: "abcdefghijklmnop", Name: "A", MaxLive: 50}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Start: %v", err)
	}
	if _, err := s.Pos(ctx, testMeet, PosParams{UID: newPlayer("A").uid, SID: "abcdefghijklmnop"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Pos: %v", err)
	}
	if _, err := s.Leave(ctx, testMeet, newPlayer("A").uid, "abcdefghijklmnop"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Leave: %v", err)
	}
	if err := s.Revoke(ctx, testMeet, newPlayer("A").uid); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Revoke: %v", err)
	}
	if err := s.MarkDead(ctx, testMeet); !errors.Is(err, ErrUnavailable) {
		t.Errorf("MarkDead: %v", err)
	}
	if err := s.SetKill(ctx, true); !errors.Is(err, ErrUnavailable) {
		t.Errorf("SetKill: %v", err)
	}
}

func TestNilStoreIsSafe(t *testing.T) {
	ctx := context.Background()
	for name, s := range map[string]*Store{"nil store": nil, "nil client": NewStore(nil)} {
		if _, err := s.NowMs(ctx); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s NowMs: %v", name, err)
		}
		if _, err := s.Start(ctx, testMeet, StartParams{UID: newPlayer("A").uid}); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s Start: %v", name, err)
		}
		if err := s.Revoke(ctx, testMeet, newPlayer("A").uid); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s Revoke: %v", name, err)
		}
		if err := s.MarkDead(ctx, testMeet); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s MarkDead: %v", name, err)
		}
		if err := s.ClearDead(ctx, testMeet); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s ClearDead: %v", name, err)
		}
		if err := s.SetKill(ctx, false); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s SetKill: %v", name, err)
		}
	}
}

// --- key 衛生（審查 M9）---

func TestKeysAreCanonicalAndMalformedIDsNeverReachRedis(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	ctx := context.Background()

	// 大寫／花括號／urn 寫法都收斂成同一組規範（小寫）key
	for _, form := range []string{
		strings.ToUpper(testMeet), "{" + testMeet + "}", "urn:uuid:" + testMeet,
	} {
		res, err := e.store.Start(ctx, form, StartParams{
			UID: strings.ToUpper(a.uid), SID: a.sid, Name: "A", MaxLive: 50, CheckedAtMs: e.nowMs(),
		})
		if err != nil || res.Outcome != OutcomeOK || res.N != 1 {
			t.Fatalf("Start(%q): %+v err=%v", form, res, err)
		}
	}
	for _, k := range e.mr.Keys() {
		if k != strings.ToLower(k) {
			t.Fatalf("non-canonical key %q", k)
		}
		if !strings.HasPrefix(k, "rml:{"+testMeet+"}:") {
			t.Fatalf("unexpected key %q", k)
		}
	}

	// 非法 id：不得進 Redis（`}`、`:`、`{` 會造成 hash tag 與 key 空間混淆）
	e.mr.FlushAll()
	for _, bad := range []string{"a}:g:x{", "", "not-a-uuid", testMeet + "}:x", "../" + testMeet} {
		if _, err := e.store.Start(ctx, bad, StartParams{UID: a.uid, SID: a.sid, Name: "A", MaxLive: 50}); !errors.Is(err, ErrBadID) {
			t.Errorf("Start(%q) err = %v, want ErrBadID", bad, err)
		}
		if _, err := e.store.Pos(ctx, bad, PosParams{UID: a.uid, SID: a.sid}); !errors.Is(err, ErrBadID) {
			t.Errorf("Pos(%q) err = %v, want ErrBadID", bad, err)
		}
		if err := e.store.Revoke(ctx, bad, a.uid); !errors.Is(err, ErrBadID) {
			t.Errorf("Revoke(%q) err = %v, want ErrBadID", bad, err)
		}
		if err := e.store.MarkDead(ctx, bad); !errors.Is(err, ErrBadID) {
			t.Errorf("MarkDead(%q) err = %v, want ErrBadID", bad, err)
		}
	}
	if _, err := e.store.Start(ctx, testMeet, StartParams{UID: "u}:x{", SID: a.sid, Name: "A", MaxLive: 50}); !errors.Is(err, ErrBadID) {
		t.Errorf("bad uid err = %v, want ErrBadID", err)
	}
	if keys := e.mr.Keys(); len(keys) != 0 {
		t.Fatalf("malformed ids leaked keys into Redis: %v", keys)
	}
}

// --- TTL（契約 §4 表）---

func TestKeyTTLs(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, true) // po=true 才會有 meta
	for _, k := range []string{"grants", "idx", "names", "meta"} {
		if ttl := e.mr.TTL(mk(k)); ttl != KeyTTL {
			t.Errorf("%s ttl = %v, want %v", k, ttl, KeyTTL)
		}
	}
	e.start(a, 50, false)
	e.posNoAdvance(a, &[2]float64{25.0, 121.5}, 0)
	for _, k := range []string{"pos", "hb"} {
		if ttl := e.mr.TTL(mk(k)); ttl != PosTTL {
			t.Errorf("%s ttl = %v, want %v", k, ttl, PosTTL)
		}
	}
	if ttl := e.mr.TTL(mk("f:" + a.uid)); ttl != time.Duration(FloorMs)*time.Millisecond {
		t.Errorf("floor ttl = %v, want %dms", ttl, FloorMs)
	}
}

// --- 小工具 ---

// dumpRedis 把 miniredis 內所有 key／值攤平成一個字串（供「某個字串不得出現在 Redis」的斷言）。
func (e *testEnv) dumpRedis() string {
	var sb strings.Builder
	keys := e.mr.Keys()
	sort.Strings(keys)
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString(" = ")
		switch e.mr.Type(k) {
		case "hash":
			fields := e.hkeys(k)
			sort.Strings(fields)
			for _, f := range fields {
				sb.WriteString(f + ":" + e.mr.HGet(k, f) + ";")
			}
		default:
			sb.WriteString(e.get(k))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// uuidFor 產生固定格式的合法 UUID 字串（測試用，不會與 newPlayer 的隨機 uid 撞）。
func uuidFor(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
