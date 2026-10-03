package live

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// testEnv miniredis（記憶體內的 Redis，Lua 由 gopher-lua 執行；本機沙盒沒有 Docker）。
//
// 時間：Lua 內的 redis.call('TIME') 取 miniredis 的 SetTime；TTL 只在 FastForward 時遞減——
// 所以「時間前進」一律走 advance()（兩者一起動），否則 grant 過期／TTL 過期會與 TIME 脫鉤。
type testEnv struct {
	t     *testing.T
	mr    *miniredis.Miniredis
	rdb   *redis.Client
	store *Store
	now   time.Time
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	e := &testEnv{
		t: t, mr: mr, rdb: rdb, store: NewStore(rdb),
		now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
	}
	mr.SetTime(e.now)
	return e
}

func (e *testEnv) advance(d time.Duration) {
	e.t.Helper()
	e.now = e.now.Add(d)
	e.mr.SetTime(e.now)
	e.mr.FastForward(d)
}

func (e *testEnv) nowMs() int64 { return e.now.UnixMilli() }

// player 一個測試玩家（固定 uid + 固定 sid）。
type player struct {
	uid, sid, name string
}

var sidCounter int

func newPlayer(name string) player {
	sidCounter++
	return player{
		uid:  uuid.NewString(),
		sid:  fmt.Sprintf("sid%013d", sidCounter), // 16 字元 [A-Za-z0-9]
		name: name,
	}
}

const testMeet = "6f259465-0f7d-431c-a122-bf21a4e9a8eb"

// start 以「現在」的 Redis 時間當 checkedAtMs 呼叫 Store.Start。
func (e *testEnv) start(p player, maxLive int, po bool) StartResult {
	e.t.Helper()
	res, err := e.store.Start(context.Background(), testMeet, StartParams{
		UID: p.uid, SID: p.sid, Name: p.name, MaxLive: maxLive, PresenceOnly: po, CheckedAtMs: e.nowMs(),
	})
	if err != nil {
		e.t.Fatalf("Start(%s): %v", p.name, err)
	}
	return res
}

// pos 送一次 /pos；fix=nil 代表只心跳。送完自動前進 2 秒（避開 1.5 s 頻率地板）。
func (e *testEnv) pos(p player, fix *[2]float64, clientRV int) PosResult {
	e.t.Helper()
	res := e.posNoAdvance(p, fix, clientRV)
	e.advance(2 * time.Second)
	return res
}

func (e *testEnv) posNoAdvance(p player, fix *[2]float64, clientRV int) PosResult {
	e.t.Helper()
	pp := PosParams{UID: p.uid, SID: p.sid, ClientRV: clientRV}
	if fix != nil {
		pp.HasP, pp.LaE5, pp.LnE5, pp.Acc, pp.FaS = true, int64(math.Round(fix[0]*1e5)), int64(math.Round(fix[1]*1e5)), 8, 0
	}
	res, err := e.store.Pos(context.Background(), testMeet, pp)
	if err != nil {
		e.t.Fatalf("Pos(%s): %v", p.name, err)
	}
	return res
}

// hkeys／get：miniredis 直接 API 的簡化版（找不到時回空，不回 error）。
func (e *testEnv) hkeys(key string) []string {
	ks, _ := e.mr.HKeys(key)
	return ks
}

func (e *testEnv) get(key string) string {
	v, _ := e.mr.Get(key)
	return v
}
