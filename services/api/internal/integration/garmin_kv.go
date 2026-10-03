package integration

// Garmin 用的短命鍵值存取（PKCE verifier、計數器、節流旗標）：Redis（每次往返有硬期限）＋記憶體備援。
//
// 設計原則：Redis 只放「短命、可遺失」的資料——被清空頂多讓進行中的連接失敗（使用者重試）、計數遺失無害；
// 需要正確性的互斥（同使用者處理、token 刷新）改走 Postgres advisory lock，不依賴這裡。
// 沒有 Redis（本機／測試）時退回行程內記憶體（單機語意）。

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/cache"
)

// garminRedisTimeout：每次 Redis 往返的硬期限（見 internal/cache.Bounded 的說明）。
const garminRedisTimeout = time.Second

type garminMemEntry struct {
	val string
	exp time.Time
}

type garminKV struct {
	b   *cache.Bounded // nil＝沒有 Redis，只用記憶體
	mu  sync.Mutex
	mem map[string]garminMemEntry
	now func() time.Time
}

func newGarminKV(rdb *redis.Client) *garminKV {
	return &garminKV{b: cache.NewBounded(rdb, garminRedisTimeout), mem: map[string]garminMemEntry{}, now: time.Now}
}

func (k *garminKV) hasRedis() bool { return k.b != nil }

func (k *garminKV) memGetLocked(key string) (garminMemEntry, bool) {
	e, ok := k.mem[key]
	if !ok {
		return e, false
	}
	if !e.exp.IsZero() && !k.now().Before(e.exp) {
		delete(k.mem, key)
		return e, false
	}
	return e, true
}

// memSweepLocked：順手清掉過期項目（只在寫入時，不另開 goroutine）。
func (k *garminKV) memSweepLocked() {
	if len(k.mem) < 256 {
		return
	}
	now := k.now()
	for key, e := range k.mem {
		if !e.exp.IsZero() && !now.Before(e.exp) {
			delete(k.mem, key)
		}
	}
}

// Set 寫入（覆蓋）。
func (k *garminKV) Set(ctx context.Context, key, val string, ttl time.Duration) error {
	if k.b != nil {
		rctx, cancel := k.b.Ctx(ctx)
		defer cancel()
		return k.b.C.Set(rctx, key, val, ttl).Err()
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.memSweepLocked()
	k.mem[key] = garminMemEntry{val: val, exp: k.now().Add(ttl)}
	return nil
}

// SetNX 不存在才寫入；回傳是否寫入成功。
func (k *garminKV) SetNX(ctx context.Context, key, val string, ttl time.Duration) (bool, error) {
	if k.b != nil {
		rctx, cancel := k.b.Ctx(ctx)
		defer cancel()
		return k.b.C.SetNX(rctx, key, val, ttl).Result()
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.memGetLocked(key); ok {
		return false, nil
	}
	k.memSweepLocked()
	k.mem[key] = garminMemEntry{val: val, exp: k.now().Add(ttl)}
	return true, nil
}

// Get 讀取；找不到回 ("", false, nil)。
func (k *garminKV) Get(ctx context.Context, key string) (string, bool, error) {
	if k.b != nil {
		rctx, cancel := k.b.Ctx(ctx)
		defer cancel()
		v, err := k.b.C.Get(rctx, key).Result()
		if errors.Is(err, redis.Nil) {
			return "", false, nil
		}
		return v, err == nil, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	e, ok := k.memGetLocked(key)
	return e.val, ok, nil
}

// GetDel 讀取並刪除（一次性：PKCE verifier 用）；找不到回 ("", false, nil)。
func (k *garminKV) GetDel(ctx context.Context, key string) (string, bool, error) {
	if k.b != nil {
		rctx, cancel := k.b.Ctx(ctx)
		defer cancel()
		v, err := k.b.C.GetDel(rctx, key).Result()
		if errors.Is(err, redis.Nil) {
			return "", false, nil
		}
		return v, err == nil, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	e, ok := k.memGetLocked(key)
	if ok {
		delete(k.mem, key)
	}
	return e.val, ok, nil
}

// Del 刪除（盡力）。
func (k *garminKV) Del(ctx context.Context, key string) {
	if k.b != nil {
		rctx, cancel := k.b.Ctx(ctx)
		defer cancel()
		_ = k.b.C.Del(rctx, key).Err()
		return
	}
	k.mu.Lock()
	delete(k.mem, key)
	k.mu.Unlock()
}

// Incr 計數＋1；首次建立時設 TTL。回傳新值。
func (k *garminKV) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	return k.IncrBy(ctx, key, 1, ttl)
}

// IncrBy 計數＋n；首次建立時設 TTL。
func (k *garminKV) IncrBy(ctx context.Context, key string, n int64, ttl time.Duration) (int64, error) {
	if k.b != nil {
		rctx, cancel := k.b.Ctx(ctx)
		defer cancel()
		v, err := k.b.C.IncrBy(rctx, key, n).Result()
		if err != nil {
			return 0, err
		}
		if v == n { // 首次建立才設 TTL；失敗只影響這個鍵的壽命，下一次不再補（計數器遺失無害）
			_ = k.b.C.Expire(rctx, key, ttl).Err()
		}
		return v, nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.memSweepLocked()
	e, ok := k.memGetLocked(key)
	var cur int64
	if ok {
		cur, _ = strconv.ParseInt(e.val, 10, 64)
	}
	cur += n
	exp := e.exp
	if !ok {
		exp = k.now().Add(ttl)
	}
	k.mem[key] = garminMemEntry{val: strconv.FormatInt(cur, 10), exp: exp}
	return cur, nil
}

// SAddCap 把 member 加進集合，集合滿 max 個就不再加；首次建立時設 TTL。回傳集合目前大小。
func (k *garminKV) SAddCap(ctx context.Context, key, member string, max int64, ttl time.Duration) (int64, error) {
	if k.b != nil {
		rctx, cancel := k.b.Ctx(ctx)
		defer cancel()
		n, err := k.b.C.SCard(rctx, key).Result()
		if err != nil {
			return 0, err
		}
		if n >= max {
			return n, nil
		}
		if _, err := k.b.C.SAdd(rctx, key, member).Result(); err != nil {
			return n, err
		}
		if n == 0 {
			_ = k.b.C.Expire(rctx, key, ttl).Err()
		}
		return n + 1, nil
	}
	return 0, nil // 記憶體備援不記錄來源 IP（只是觀察用）
}

// garminKeyedMutex：依 key 的行程內互斥鎖（用完自動回收，不會無限長大）。
type garminKeyedMutex struct {
	mu sync.Mutex
	m  map[string]*garminKM
}

type garminKM struct {
	sync.Mutex
	refs int
}

// Lock 取得 key 的鎖，回傳解鎖函式。
func (k *garminKeyedMutex) Lock(key string) func() {
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]*garminKM{}
	}
	e := k.m[key]
	if e == nil {
		e = &garminKM{}
		k.m[key] = e
	}
	e.refs++
	k.mu.Unlock()
	e.Lock()
	return func() {
		e.Unlock()
		k.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(k.m, key)
		}
		k.mu.Unlock()
	}
}
