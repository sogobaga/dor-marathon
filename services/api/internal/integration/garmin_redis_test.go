package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newMiniRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return mr, rdb
}

// KV 的 Redis 路徑（miniredis）：SETNX／GET／GETDEL（一次性）／INCRBY＋首次 TTL／集合上限。
func TestKV_RedisPath(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	kv := newGarminKV(rdb)
	ctx := context.Background()
	if !kv.hasRedis() {
		t.Fatal("must use redis")
	}
	if ok, err := kv.SetNX(ctx, "k:nx", "1", time.Minute); err != nil || !ok {
		t.Fatalf("SetNX first: %v %v", ok, err)
	}
	if ok, _ := kv.SetNX(ctx, "k:nx", "2", time.Minute); ok {
		t.Fatal("SetNX second must not overwrite")
	}
	if v, ok, _ := kv.Get(ctx, "k:nx"); !ok || v != "1" {
		t.Fatalf("Get: %q %v", v, ok)
	}
	if err := kv.Set(ctx, "k:once", "secret-verifier", 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if d := mr.TTL("k:once"); d <= 0 || d > 10*time.Minute {
		t.Fatalf("TTL = %s", d)
	}
	if v, ok, err := kv.GetDel(ctx, "k:once"); err != nil || !ok || v != "secret-verifier" {
		t.Fatalf("GetDel: %q %v %v", v, ok, err)
	}
	if _, ok, _ := kv.GetDel(ctx, "k:once"); ok {
		t.Fatal("GetDel must be one-time")
	}
	if mr.Exists("k:once") {
		t.Fatal("key must be gone after GetDel")
	}
	if n, err := kv.Incr(ctx, "k:cnt", time.Hour); err != nil || n != 1 {
		t.Fatalf("Incr: %d %v", n, err)
	}
	if n, _ := kv.IncrBy(ctx, "k:cnt", 4, time.Hour); n != 5 {
		t.Fatalf("IncrBy: %d", n)
	}
	if d := mr.TTL("k:cnt"); d <= 0 || d > time.Hour {
		t.Fatalf("counter must get a TTL on creation: %s", d)
	}
	for i := 0; i < 5; i++ {
		kv.SAddCap(ctx, "k:set", string(rune('a'+i)), 3, time.Hour)
	}
	if n, _ := rdb.SCard(ctx, "k:set").Result(); n != 3 {
		t.Fatalf("set must be capped at 3, got %d", n)
	}
	kv.Del(ctx, "k:nx")
	if _, ok, _ := kv.Get(ctx, "k:nx"); ok {
		t.Fatal("Del")
	}
}

// 連接流程走 Redis：verifier 存在 garmin:pkce:<sid>（TTL ≤10 分鐘）、callback 以 GETDEL 一次性取走；重放 → invalid_state。
func TestConnectFlow_WithRedis_VerifierOneTimeGetDel(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	e := newConnectEnvRedis(t, nil, rdb)
	cr := e.connect(t, tUserA, "v1")
	st, ok := garminVerifyState("jwt-secret-test", cr.State, time.Now())
	if !ok {
		t.Fatal("state")
	}
	key := garminPKCEKeyPrefix + st.SID
	if !mr.Exists(key) {
		t.Fatalf("verifier must be stored in redis under %s", key)
	}
	if d := mr.TTL(key); d <= 0 || d > 10*time.Minute {
		t.Fatalf("verifier TTL = %s, want (0,10m]", d)
	}
	v, _ := mr.Get(key)
	if len(v) != 43 || garminChallenge(v) != cr.Challenge {
		t.Fatalf("stored verifier does not match the challenge in the authorization url")
	}
	if strings.Contains(cr.URL, v) {
		t.Fatal("verifier leaked into the authorization url")
	}
	code := e.api.newCode(cr.Challenge)
	rec := e.callback(t, cr, code, "", cr.Cookie)
	if s, reason := redirectReason(rec); s != "connected" {
		t.Fatalf("callback: %q %q", s, reason)
	}
	if mr.Exists(key) {
		t.Fatal("verifier must be consumed (GETDEL) by the callback")
	}
	rec = e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)
	if _, reason := redirectReason(rec); reason != "invalid_state" {
		t.Fatalf("replay: %q", reason)
	}
	if tk, _, _, _ := e.api.calls(); tk != 1 {
		t.Fatalf("token endpoint calls = %d, want exactly 1", tk)
	}
}

// Redis 清空（F-14）：進行中的連接失敗（invalid_state），使用者重試即可；不會 panic、不會呼叫 token 端點。
func TestConnectFlow_RedisFlushedMidFlow(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	e := newConnectEnvRedis(t, nil, rdb)
	cr := e.connect(t, tUserA, "v1")
	mr.FlushAll()
	rec := e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)
	if _, reason := redirectReason(rec); reason != "invalid_state" {
		t.Fatalf("after FLUSHALL: %q", reason)
	}
	if tk, _, _, _ := e.api.calls(); tk != 0 {
		t.Fatal("no token call without a verifier")
	}
	// 重試：全新的 connect 可以成功
	cr = e.connect(t, tUserA, "v1")
	if s, reason := redirectReason(e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)); s != "connected" {
		t.Fatalf("retry: %q %q", s, reason)
	}
}

// Redis 不可用：/connect 回 503 try_again（不產生一個之後必定失敗的授權連結）；callback 導回 server_config。
func TestConnectFlow_RedisDown(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	e := newConnectEnvRedis(t, nil, rdb)
	cr := e.connect(t, tUserA, "v1")
	mr.Close()
	rec := e.req(http.MethodPost, "/connect", tUserA, `{"consent":true,"consent_v":"v1"}`)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "try_again") {
		t.Fatalf("connect with redis down: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.callback(t, cr, e.api.newCode(cr.Challenge), "", cr.Cookie)
	if _, reason := redirectReason(rec); reason != "server_config" {
		t.Fatalf("callback with redis down: %q", reason)
	}
	if tk, _, _, _ := e.api.calls(); tk != 0 {
		t.Fatal("no token call when the verifier cannot be read")
	}
}

// 推送觸發的順手掃描：Redis SET NX EX 300 搶到才掃（5 分鐘節流），沒有 ticker。
func TestSweepThrottle_Redis(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	tg := newTestGarmin(t, nil)
	tg.h.kv = newGarminKV(rdb)
	sweeps := 0
	countSweep := func() {
		// 放一筆已到期的事件，掃描會領走它；用處理器計數
		k := "sweep-" + time.Now().Format("150405.000000000")
		tg.store.InsertGarminEvents(context.Background(), []garminEventIn{{EventType: garminEvActivity, ProviderUserID: tUser1, DedupeKey: &k, Payload: []byte(`{}`)}})
		tg.store.mu.Lock()
		for _, r := range tg.store.events {
			if r.Status == garminStPending {
				r.Next = time.Now().Add(-time.Second)
			}
		}
		tg.store.mu.Unlock()
	}
	tg.h.handlers.activity = func(ctx context.Context, ev garminEvent) (string, error) { sweeps++; return garminResInserted, nil }
	countSweep()
	tg.h.maybeSweep(context.Background())
	if sweeps != 1 {
		t.Fatalf("first push-triggered sweep should run, sweeps=%d", sweeps)
	}
	if d := mr.TTL("garmin:sweep"); d <= 0 || d > 5*time.Minute {
		t.Fatalf("sweep throttle TTL = %s, want (0,5m]", d)
	}
	countSweep()
	tg.h.maybeSweep(context.Background()) // 節流中：不掃
	if sweeps != 1 {
		t.Fatalf("second sweep within 5 minutes must be throttled, sweeps=%d", sweeps)
	}
	mr.FastForward(6 * time.Minute)
	tg.h.maybeSweep(context.Background())
	if sweeps != 2 {
		t.Fatalf("after the throttle expires the next push sweeps again, sweeps=%d", sweeps)
	}
}

// 計數器走 Redis（小時桶＋TTL），DirectStats 能讀回。
func TestCountersAndStats_Redis(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	e := newConnectEnvRedis(t, nil, rdb)
	e.h.count(context.Background(), "garmin:cnt:unknown:"+e.h.hourBucket(), 7, 2*time.Hour)
	if d := mr.TTL("garmin:cnt:unknown:" + e.h.hourBucket()); d <= 0 || d > 2*time.Hour {
		t.Fatalf("counter TTL = %s", d)
	}
	st, err := e.h.DirectStats(context.Background())
	if err != nil || st.UnknownUser24h != 7 {
		t.Fatalf("stats: %+v %v", st, err)
	}
}

// Redis 不可用時 webhook 照常運作：計數／觀察／順手掃描都是盡力而為（fail-open），不拖慢也不擋住 200；事件仍落地並處理。
func TestWebhook_RedisDownStillAcksAndProcesses(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	tg := newTestGarmin(t, nil)
	tg.h.kv = newGarminKV(rdb)
	tg.store.addUser(tUser1, tDor1)
	mr.Close()
	start := time.Now()
	rec := tg.postJSON("activities", synthPush(synthActivity(tUser1, "RD-1", time.Now().Add(-time.Hour).Unix())))
	if rec.Code != http.StatusOK {
		t.Fatalf("redis down: webhook must still ack 200, got %d", rec.Code)
	}
	if el := time.Since(start); el > 900*time.Millisecond {
		t.Fatalf("the 200 must not wait for redis timeouts, took %s", el)
	}
	tg.store.waitSettled(t, 8*time.Second)
	if r := tg.store.rows(); len(r) != 1 || r[0].Status != garminStDone {
		t.Fatalf("event must still be processed: %+v", r)
	}
}
