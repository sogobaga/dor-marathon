package integration

// COROS MCP 節流與保護（corosmcp_throttle.go，GA 契約 §3.1）單元測試：
//   - 手動 5 分鐘／自動 25 分鐘名額（Redis，跨副本）；Redis 清空或故障時退記憶體、不 fail-open；
//   - in-flight 鎖（只有持有者能釋放、併發只有一個贏）；
//   - COROS 429／5xx 後 30 分鐘冷卻；
//   - 自動同步全域並發上限（滿了略過、不扣名額）、隨機延遲可被取消；
//   - 錯誤分類（前台只看穩定代碼，不顯示 COROS 原始字串）。
// 全程 miniredis（記憶體內），不連真實 Redis／不打 COROS。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRedisHandler(t *testing.T) (*CorosMcpHandler, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	h := newTestCorosMcpHandler()
	h.rdb = rdb
	h.autoJitterMax = 0
	return h, mr
}

func TestClaimManual_FiveMinuteWindow_Redis(t *testing.T) {
	h, mr := newRedisHandler(t)
	ctx := context.Background()
	if ok, _ := h.claimManual(ctx, "u1"); !ok {
		t.Fatal("first manual import must pass")
	}
	ok, retry := h.claimManual(ctx, "u1")
	if ok || retry < 290 || retry > 301 {
		t.Fatalf("second manual import within 5 minutes must be throttled with ~300s hint, ok=%v retry=%d", ok, retry)
	}
	if ok, _ := h.claimManual(ctx, "u2"); !ok {
		t.Fatal("throttle is per user")
	}
	mr.FastForward(corosMcpManualWindow + time.Second)
	if ok, _ := h.claimManual(ctx, "u1"); !ok {
		t.Fatal("after 5 minutes the user may import again")
	}
	if corosMcpManualWindow != 5*time.Minute {
		t.Fatalf("manual window must be 5 minutes (contract §3.1), got %v", corosMcpManualWindow)
	}
}

func TestClaimAutoSync_TwentyFiveMinuteWindow_Redis(t *testing.T) {
	h, mr := newRedisHandler(t)
	ctx := context.Background()
	if !h.claimAutoSync(ctx, "u1") {
		t.Fatal("first claim wins")
	}
	if h.claimAutoSync(ctx, "u1") {
		t.Fatal("second claim within 25 minutes must lose")
	}
	if !h.claimAutoSync(ctx, "u2") {
		t.Fatal("claims are per user")
	}
	if got := mr.TTL(corosMcpKeyAutoSync + "u1"); got != 1500*time.Second {
		t.Fatalf("auto-sync key must be SET NX EX 1500, TTL=%v", got)
	}
	mr.FastForward(corosMcpAutoSyncWindow + time.Second)
	if !h.claimAutoSync(ctx, "u1") {
		t.Fatal("after 25 minutes the user may be synced again")
	}
}

// 跨副本：兩個 handler（兩個 API 副本）共用同一個 Redis，名額只算一次。
func TestClaims_AreSharedAcrossReplicas(t *testing.T) {
	a, mr := newRedisHandler(t)
	b := newTestCorosMcpHandler()
	b.rdb = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = b.rdb.Close() })
	ctx := context.Background()
	if !a.claimAutoSync(ctx, "u1") {
		t.Fatal("replica A claims")
	}
	if b.claimAutoSync(ctx, "u1") {
		t.Fatal("replica B must see A's claim (Redis is the cross-replica store)")
	}
	if ok, _ := a.claimManual(ctx, "u1"); !ok {
		t.Fatal("manual claim A")
	}
	if ok, _ := b.claimManual(ctx, "u1"); ok {
		t.Fatal("manual claim must be shared across replicas")
	}
}

// Redis 故障：退記憶體節流，而且「不 fail-open」（第二次仍被擋）。
func TestClaims_RedisDown_FallsBackToMemoryNotFailOpen(t *testing.T) {
	h, mr := newRedisHandler(t)
	mr.Close() // 之後所有 Redis 呼叫都出錯
	ctx := context.Background()
	if !h.claimAutoSync(ctx, "u1") {
		t.Fatal("with Redis down the first claim still works (memory)")
	}
	if h.claimAutoSync(ctx, "u1") {
		t.Fatal("with Redis down a second claim must STILL be refused (no fail-open)")
	}
	if ok, _ := h.claimManual(ctx, "u1"); !ok {
		t.Fatal("first manual claim via memory")
	}
	if ok, retry := h.claimManual(ctx, "u1"); ok || retry < 1 {
		t.Fatalf("manual throttle must hold without Redis, ok=%v retry=%d", ok, retry)
	}
	if ok, _ := h.allowProbe(ctx, "u1"); !ok {
		t.Fatal("first probe passes")
	}
	if ok, _ := h.allowProbe(ctx, "u1"); ok {
		t.Fatal("probe throttle must not fail open when Redis is down (old behaviour was fail-open)")
	}
}

// 沒有 Redis（rdb=nil）：記憶體節流，時間用可注入的時鐘推進。
func TestClaims_MemoryFallback_WithInjectedClock(t *testing.T) {
	h := newTestCorosMcpHandler()
	now := time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return now }
	ctx := context.Background()
	if !h.claimAutoSync(ctx, "u1") || h.claimAutoSync(ctx, "u1") {
		t.Fatal("once per window")
	}
	now = now.Add(corosMcpAutoSyncWindow - time.Second)
	if h.claimAutoSync(ctx, "u1") {
		t.Fatal("still inside the 25-minute window")
	}
	now = now.Add(2 * time.Second)
	if !h.claimAutoSync(ctx, "u1") {
		t.Fatal("after 25 minutes the user may be synced again")
	}
	if corosMcpAutoSyncWindow != 1500*time.Second {
		t.Fatalf("window must be 1500s, got %v", corosMcpAutoSyncWindow)
	}
}

func TestSyncLock_ExclusiveAndOwnerOnlyRelease_Redis(t *testing.T) {
	h, mr := newRedisHandler(t)
	ctx := context.Background()
	rel1, ok := h.acquireSyncLock(ctx, "u1")
	if !ok {
		t.Fatal("first acquire")
	}
	if _, ok := h.acquireSyncLock(ctx, "u1"); ok {
		t.Fatal("same user: second acquire must fail while the first sync is in flight")
	}
	if rel2, ok := h.acquireSyncLock(ctx, "u2"); !ok {
		t.Fatal("locks are per user")
	} else {
		rel2()
	}
	if got := mr.TTL(corosMcpKeySyncLock + "u1"); got != corosMcpSyncLockTTL {
		t.Fatalf("lock must be SET NX EX 120, TTL=%v", got)
	}
	rel1()
	rel3, ok := h.acquireSyncLock(ctx, "u1")
	if !ok {
		t.Fatal("after release the lock is free")
	}
	// 持有者 A 的鎖逾時被 B 拿走後，A 遲來的 release 不得把 B 的鎖刪掉。
	mr.FastForward(corosMcpSyncLockTTL + time.Second)
	relB, ok := h.acquireSyncLock(ctx, "u1")
	if !ok {
		t.Fatal("expired lock can be re-acquired")
	}
	rel3() // A 遲來的釋放
	if _, ok := h.acquireSyncLock(ctx, "u1"); ok {
		t.Fatal("a stale holder's release must not delete the new holder's lock")
	}
	relB()
	if _, ok := h.acquireSyncLock(ctx, "u1"); !ok {
		t.Fatal("new holder released → free")
	}
}

// 併發：同一位使用者同時 50 個同步請求，只有一個拿到鎖。
func TestSyncLock_ConcurrentOnlyOneWins(t *testing.T) {
	for name, h := range map[string]*CorosMcpHandler{
		"redis":  func() *CorosMcpHandler { h, _ := newRedisHandler(t); return h }(),
		"memory": newTestCorosMcpHandler(),
	} {
		var wins atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, ok := h.acquireSyncLock(context.Background(), "u1"); ok {
					wins.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("%s: exactly one concurrent sync may hold the lock, got %d", name, wins.Load())
		}
	}
}

func TestSyncLock_MemoryFallbackWhenRedisDown(t *testing.T) {
	h, mr := newRedisHandler(t)
	mr.Close()
	rel, ok := h.acquireSyncLock(context.Background(), "u1")
	if !ok {
		t.Fatal("memory lock acquired")
	}
	if _, ok := h.acquireSyncLock(context.Background(), "u1"); ok {
		t.Fatal("memory lock must still exclude (no fail-open)")
	}
	rel()
	if _, ok := h.acquireSyncLock(context.Background(), "u1"); !ok {
		t.Fatal("released")
	}
}

func TestCooldown_ThirtyMinutes_RedisAndMemory(t *testing.T) {
	h, mr := newRedisHandler(t)
	ctx := context.Background()
	if left := h.cooldownLeft(ctx, "u1"); left != 0 {
		t.Fatalf("no cooldown initially, got %v", left)
	}
	h.setCooldown(ctx, "u1")
	if left := h.cooldownLeft(ctx, "u1"); left < 29*time.Minute || left > 30*time.Minute {
		t.Fatalf("cooldown must be ~30 minutes, got %v", left)
	}
	if left := h.cooldownLeft(ctx, "u2"); left != 0 {
		t.Fatal("cooldown is per user")
	}
	mr.FastForward(corosMcpCooldown + time.Second)
	// Redis 鍵到期；記憶體表是用真實時間記的（剛設定），所以 Redis 到期後仍可能殘留記憶體冷卻——
	// 這是刻意的雙寫兜底，這裡只確認 Redis 那一份確實到期。
	if mr.Exists(corosMcpKeyCooldown + "u1") {
		t.Fatal("redis cooldown key must expire after 30 minutes")
	}
	if corosMcpCooldown != 30*time.Minute {
		t.Fatalf("cooldown must be 30 minutes (contract §3.1), got %v", corosMcpCooldown)
	}

	// 純記憶體：時鐘推進 30 分鐘後解除
	m := newTestCorosMcpHandler()
	now := time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	m.setCooldown(ctx, "u1")
	if m.cooldownLeft(ctx, "u1") <= 0 {
		t.Fatal("memory cooldown active")
	}
	now = now.Add(corosMcpCooldown + time.Second)
	if m.cooldownLeft(ctx, "u1") != 0 {
		t.Fatal("memory cooldown must end after 30 minutes")
	}
}

func TestIsCorosThrottleErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"MCP 429", &corosMcpHTTPError{Method: "tools/call", Status: http.StatusTooManyRequests}, true},
		{"MCP 500", &corosMcpHTTPError{Method: "initialize", Status: 500}, true},
		{"MCP 503", &corosMcpHTTPError{Method: "initialize", Status: 503}, true},
		{"MCP 400 is not throttling", &corosMcpHTTPError{Method: "tools/call", Status: 400}, false},
		{"MCP 403", &corosMcpHTTPError{Method: "tools/call", Status: 403}, false},
		{"token endpoint 429", &corosMcpTokenError{Status: 429}, true},
		{"token endpoint 502", &corosMcpTokenError{Status: 502}, true},
		{"token invalid_grant 400", &corosMcpTokenError{Status: 400, Code: "invalid_grant"}, false},
		{"wrapped", fmt.Errorf("sync: %w", &corosMcpHTTPError{Method: "x", Status: 500}), true},
		{"reconnect is not throttling", errCorosMcpReconnect, false},
		{"plain error", errors.New("boom"), false},
	}
	for _, c := range cases {
		if got := isCorosThrottleErr(c.err); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestCorosMcpErrorCode_StableCodesOnly(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{errCorosMcpReconnect, "reauth_required"},
		{fmt.Errorf("%w: %v", errCorosMcpReconnect, "oauth http 400 invalid_grant"), "reauth_required"},
		{errMCPUnauthorized, "reauth_required"},
		{errCorosMcpAnomaly, "anomaly"},
		{errCorosMcpNotConnected, "not_connected"},
		{context.DeadlineExceeded, "timeout"},
		{&corosMcpHTTPError{Status: 429}, "rate_limited"},
		{&corosMcpHTTPError{Status: 502}, "upstream_5xx"},
		{&corosMcpHTTPError{Status: 404}, "http_error"},
		{&corosMcpTokenError{Status: 429}, "rate_limited"},
		{&corosMcpTokenError{Status: 400, Code: "invalid_request", Description: "secret detail"}, "token_error"},
		{errors.New("tool getActivityDetail returned isError: Lab 123 private text"), "tool_error"},
		{errors.New("something else entirely with PII 王小明"), "error"},
	}
	for _, c := range cases {
		got := corosMcpErrorCode(c.err)
		if got != c.want {
			t.Errorf("code(%v) = %q, want %q", c.err, got, c.want)
		}
		// 代碼本身只含小寫字母、數字與底線（絕不夾帶 COROS 的原始字串）
		for _, r := range got {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' {
				t.Errorf("code %q contains unexpected character %q", got, r)
			}
		}
	}
}

// 自動同步全域並發上限：滿了略過、**不扣使用者名額**。
func TestAutoSyncOnce_GlobalConcurrencyCapSkipsWithoutConsumingQuota(t *testing.T) {
	h := newTestCorosMcpHandler() // repo=nil：總開關／緊急關閉視為「開／未關閉」，不碰 DB
	ctx := context.Background()
	sem := h.sem()
	if cap(sem) != corosMcpAutoConcurrency || corosMcpAutoConcurrency != 5 {
		t.Fatalf("global cap must be 5, got cap=%d const=%d", cap(sem), corosMcpAutoConcurrency)
	}
	for i := 0; i < corosMcpAutoConcurrency; i++ {
		sem <- struct{}{}
	}
	ran, res, err := h.autoSyncOnce(ctx, "u1")
	if ran || err != nil || res.Fetched != 0 {
		t.Fatalf("cap full must be a no-op, got ran=%v err=%v res=%+v", ran, err, res)
	}
	for i := 0; i < corosMcpAutoConcurrency; i++ {
		<-sem
	}
	if !h.claimAutoSync(ctx, "u1") {
		t.Fatal("skipped because of the global cap → the user's 25-minute quota must NOT have been consumed")
	}
}

func TestAutoSyncOnce_LostClaimIsCompleteNoOp(t *testing.T) {
	h := newTestCorosMcpHandler()
	if !h.claimAutoSync(context.Background(), "u1") {
		t.Fatal("setup claim")
	}
	ran, res, err := h.autoSyncOnce(context.Background(), "u1")
	if ran || err != nil || res.Fetched != 0 {
		t.Fatalf("lost claim must be a complete no-op, got ran=%v err=%v res=%+v", ran, err, res)
	}
}

// 隨機延遲可被取消：搶到名額後在延遲期間 ctx 被取消 → 立刻返回、不打 COROS、釋放並發名額。
func TestAutoSyncOnce_JitterIsCancelableAndReleasesSlot(t *testing.T) {
	h := newTestCorosMcpHandler()
	h.autoJitterMax = time.Hour // 一定會排到很長的延遲
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ran, _, err := h.autoSyncOnce(ctx, "u1")
		if ran || !errors.Is(err, context.Canceled) {
			t.Errorf("canceled during jitter: ran=%v err=%v", ran, err)
		}
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("autoSyncOnce did not return after cancel (jitter must be cancelable)")
	}
	if len(h.sem()) != 0 {
		t.Fatal("concurrency slot must be released on early return")
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	if got := retryAfterSeconds(&corosMcpTooSoonError{RetryAfter: 90 * time.Second}, time.Minute); got != 91 {
		t.Fatalf("too-soon retry hint = %d", got)
	}
	if got := retryAfterSeconds(&corosMcpCooldownError{RetryAfter: 10 * time.Minute}, time.Minute); got != 601 {
		t.Fatalf("cooldown retry hint = %d", got)
	}
	if got := retryAfterSeconds(errors.New("x"), 5*time.Minute); got != 301 {
		t.Fatalf("fallback = %d", got)
	}
	if got := retryAfterSeconds(nil, 0); got != 1 {
		t.Fatalf("minimum 1 second, got %d", got)
	}
	if !errors.Is(&corosMcpTooSoonError{}, errCorosMcpTooSoon) || !errors.Is(&corosMcpCooldownError{}, errCorosMcpCooldown) {
		t.Fatal("typed errors must unwrap to their sentinels")
	}
}

// --- 起點公式、手動回看天數 ---

func TestCorosMcpCatchUpFrom(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	ptr := func(t time.Time) *time.Time { return &t }
	floor := now.AddDate(0, 0, -90) // 90 天前連接
	cases := []struct {
		name  string
		floor time.Time
		last  *time.Time
		want  time.Time
	}{
		{"never synced → 30 days back (floor older)", floor, nil, now.AddDate(0, 0, -30)},
		{"synced 5 days ago → last−1 day", floor, ptr(now.AddDate(0, 0, -5)), now.AddDate(0, 0, -6)},
		{"synced 40 days ago → capped at 30 days", floor, ptr(now.AddDate(0, 0, -40)), now.AddDate(0, 0, -30)},
		{"connected 3 days ago (floor newer) clamps up to floor", now.AddDate(0, 0, -3), ptr(now.AddDate(0, 0, -10)), now.AddDate(0, 0, -3)},
		{"never synced, floor 10 days ago", now.AddDate(0, 0, -10), nil, now.AddDate(0, 0, -10)},
		{"zero floor = no clamp", time.Time{}, ptr(now.AddDate(0, 0, -2)), now.AddDate(0, 0, -3)},
	}
	for _, c := range cases {
		if got := corosMcpCatchUpFrom(now, c.floor, c.last); !got.Equal(c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestCorosMcpManualDaysFor(t *testing.T) {
	synced := time.Now()
	cases := []struct {
		name      string
		last      *time.Time
		requested int
		has       bool
		want      int
	}{
		{"default after first sync = 3", &synced, 0, false, 3},
		{"default on first connect = 30", nil, 0, false, 30},
		{"30 requested after first sync is clamped to 3", &synced, 30, true, 3},
		{"30 requested on first connect = 30", nil, 30, true, 30},
		{"2 requested", &synced, 2, true, 2},
		{"0 / negative requested → 1", &synced, -5, true, 1},
		{"999 requested on first connect → 30", nil, 999, true, 30},
	}
	for _, c := range cases {
		if got := corosMcpManualDaysFor(c.last, c.requested, c.has); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

// /status 的 expires_at／needs_reauth 規則（協調者追加需求）。
func TestStatusExpiresAtAndNeedsReauth(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	exp := now.Add(29 * 24 * time.Hour)
	reauthAt := now.Add(-time.Hour)
	cases := []struct {
		name          string
		c             *corosMcpConnection
		wantExpiresAt any
		wantReauth    bool
	}{
		{"usable refresh token → expires_at null, no reauth", &corosMcpConnection{RefreshToken: "enc:xyz", ExpiresAt: exp}, nil, false},
		{"usable refresh token and access token already expired → still null/no reauth (auto refresh)", &corosMcpConnection{RefreshToken: "enc:xyz", ExpiresAt: now.Add(-time.Hour)}, nil, false},
		{"no refresh token, not expired → expires_at given, no reauth yet", &corosMcpConnection{RefreshToken: "", ExpiresAt: exp}, exp.Format(time.RFC3339), false},
		{"no refresh token and expired → needs reauth", &corosMcpConnection{RefreshToken: "", ExpiresAt: now.Add(-time.Minute)}, now.Add(-time.Minute).Format(time.RFC3339), true},
		{"refresh failed (flagged) → reauth, refresh token no longer counts as usable", &corosMcpConnection{RefreshToken: "enc:xyz", ExpiresAt: exp, ReauthRequiredAt: &reauthAt}, exp.Format(time.RFC3339), true},
		{"no refresh token and flagged", &corosMcpConnection{RefreshToken: "", ExpiresAt: exp, ReauthRequiredAt: &reauthAt}, exp.Format(time.RFC3339), true},
	}
	for _, c := range cases {
		if got := corosMcpStatusExpiresAt(c.c); got != c.wantExpiresAt {
			t.Errorf("%s: expires_at = %v, want %v", c.name, got, c.wantExpiresAt)
		}
		if got := c.c.NeedsReauth(now); got != c.wantReauth {
			t.Errorf("%s: needs_reauth = %v, want %v", c.name, got, c.wantReauth)
		}
	}
}
