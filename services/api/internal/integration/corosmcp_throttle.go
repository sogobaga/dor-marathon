package integration

// COROS MCP 呼叫節流與保護（GA 契約 §3.1）。COROS 書面條件：每位活躍使用者約 20–30 分鐘輪詢一次——
// 自動同步 25 分鐘、手動匯入 ≥5 分鐘，兩者共用 in-flight 鎖；全部走 Redis（跨副本），Redis 出錯退回
// 「記憶體」節流而不是放行（fail-closed：寧可在單機上嚴格節流，也不要在 Redis 故障時放大對 COROS 的呼叫）。
//
// 鍵（Redis）：
//
//	coros_mcp:autosync:<uid>  SET NX EX 1500  自動同步名額（25 分鐘）
//	coros_mcp:manual:<uid>    SET NX EX 300   手動匯入名額（5 分鐘）
//	coros_mcp:sync:<uid>      SET NX EX 120   in-flight 鎖（自動／手動／重新授權補同步共用，值＝持有者 token）
//	coros_mcp:cool:<uid>      SET    EX 1800  COROS 回 429／5xx 後的使用者冷卻（30 分鐘）
//	coros_mcp:probe:<uid>     INCR  EX 60     讀取測試（僅超管）
//
// 第二道防線（不依賴 Redis）：user_integrations.last_synced_at——Redis 被清空後所有名額鍵同時消失，
// 但 last_synced_at 還在，距今不足門檻的使用者仍然不會打 COROS（見 syncCorosMcp 的 MinInterval）。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// corosMcpUnlockScript 比對持有者 token 再刪鎖（Lua，原子）。
var corosMcpUnlockScript = redis.NewScript(`if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`)

const (
	// corosMcpManualWindow 手動匯入每人最短間隔（契約 §3.1：≥ 5 分鐘）。
	corosMcpManualWindow = 5 * time.Minute
	// corosMcpSyncLockTTL in-flight 鎖壽命：大於單次同步逾時（60 秒），崩潰時 2 分鐘內自動釋放。
	corosMcpSyncLockTTL = 120 * time.Second
	// corosMcpCooldown COROS 回 429／5xx 後，該使用者的同步冷卻（契約 §3.1：30 分鐘）。
	corosMcpCooldown = 30 * time.Minute
	// corosMcpAutoConcurrency 全域（本進程）同時進行的自動同步上限；滿了就略過、不扣使用者名額。
	corosMcpAutoConcurrency = 5
	// corosMcpAutoJitterMax 自動同步觸發前的隨機延遲上限（打散「廣播 dashboard 失效」造成的同時湧入）。
	corosMcpAutoJitterMax = 30 * time.Second

	corosMcpKeyAutoSync = "coros_mcp:autosync:"
	corosMcpKeyManual   = "coros_mcp:manual:"
	corosMcpKeySyncLock = "coros_mcp:sync:"
	corosMcpKeyCooldown = "coros_mcp:cool:"
	corosMcpKeyProbe    = "coros_mcp:probe:"
)

var (
	errCorosMcpBusy     = errors.New("coros mcp: another sync is in progress")
	errCorosMcpTooSoon  = errors.New("coros mcp: synced too recently")
	errCorosMcpCooldown = errors.New("coros mcp: user is in cooldown after COROS throttling")
)

// corosMcpMem 記憶體節流表（Redis 為 nil 或出錯時的 fallback）：key → 到期時間。
type corosMcpMem struct {
	mu sync.Mutex
	m  map[string]time.Time
}

// claim 若 key 尚未到期就回 false（並給剩餘時間）；否則登記新的到期時間回 true。
func (c *corosMcpMem) claim(key string, ttl time.Duration, now time.Time) (bool, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]time.Time{}
	}
	if exp, ok := c.m[key]; ok && now.Before(exp) {
		return false, exp.Sub(now)
	}
	c.m[key] = now.Add(ttl)
	c.pruneLocked(now)
	return true, 0
}

// left 回 key 剩餘時間（已到期／不存在＝0）。
func (c *corosMcpMem) left(key string, now time.Time) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if exp, ok := c.m[key]; ok && now.Before(exp) {
		return exp.Sub(now)
	}
	return 0
}

func (c *corosMcpMem) set(key string, ttl time.Duration, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]time.Time{}
	}
	c.m[key] = now.Add(ttl)
	c.pruneLocked(now)
}

func (c *corosMcpMem) del(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, key)
}

// pruneLocked 表太大時清掉已到期的項目（避免長期運行的進程無限累積每位使用者的鍵）。
func (c *corosMcpMem) pruneLocked(now time.Time) {
	if len(c.m) < 2048 {
		return
	}
	for k, exp := range c.m {
		if !now.Before(exp) {
			delete(c.m, k)
		}
	}
}

func (h *CorosMcpHandler) timeNow() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// claimKey 搶一個「窗口名額」：Redis SET NX EX；Redis 為 nil 或出錯時退回記憶體（不放行）。
// 回傳 (搶到, 還要等多久)。
func (h *CorosMcpHandler) claimKey(ctx context.Context, key string, ttl time.Duration) (bool, time.Duration) {
	if h.rdb != nil {
		ok, err := h.rdb.SetNX(ctx, key, "1", ttl).Result()
		if err == nil {
			if ok {
				return true, 0
			}
			left, terr := h.rdb.TTL(ctx, key).Result()
			if terr != nil || left <= 0 {
				left = ttl
			}
			return false, left
		}
		log.Warn().Err(err).Str("key", key).Msg("coros mcp: redis claim failed, falling back to in-memory throttle (not fail-open)")
	}
	return h.mem.claim(key, ttl, h.timeNow())
}

// cooldownLeft 該使用者冷卻剩餘時間（0＝沒有冷卻）。Redis 出錯時看記憶體表。
func (h *CorosMcpHandler) cooldownLeft(ctx context.Context, userID string) time.Duration {
	key := corosMcpKeyCooldown + userID
	if h.rdb != nil {
		left, err := h.rdb.TTL(ctx, key).Result()
		if err == nil {
			if left > 0 {
				return left
			}
			// -2＝不存在；也看一下記憶體表（先前 Redis 出錯時設的冷卻）
			return h.mem.left(key, h.timeNow())
		}
		log.Warn().Err(err).Msg("coros mcp: redis cooldown check failed, using in-memory table")
	}
	return h.mem.left(key, h.timeNow())
}

// setCooldown COROS 回 429／5xx 後設使用者冷卻（Redis＋記憶體雙寫：Redis 之後出錯時仍有記憶體兜底）。
func (h *CorosMcpHandler) setCooldown(ctx context.Context, userID string) {
	key := corosMcpKeyCooldown + userID
	h.mem.set(key, corosMcpCooldown, h.timeNow())
	if h.rdb != nil {
		if err := h.rdb.Set(ctx, key, "1", corosMcpCooldown).Err(); err != nil {
			log.Warn().Err(err).Msg("coros mcp: redis cooldown set failed (in-memory cooldown still applies)")
		}
	}
	log.Warn().Str("user", userID).Dur("cooldown", corosMcpCooldown).Msg("coros mcp: COROS throttled/failed (429/5xx), user sync cooldown started")
}

// acquireSyncLock in-flight 鎖：同一位使用者同一時間只能有一個同步在跑（自動／手動／重新授權補同步共用）。
// 回傳 release（務必 defer 呼叫；只有持有者 token 相符才會刪鎖）。
func (h *CorosMcpHandler) acquireSyncLock(ctx context.Context, userID string) (release func(), ok bool) {
	key := corosMcpKeySyncLock + userID
	var tokenBytes [8]byte
	_, _ = rand.Read(tokenBytes[:])
	token := hex.EncodeToString(tokenBytes[:])
	if h.rdb != nil {
		got, err := h.rdb.SetNX(ctx, key, token, corosMcpSyncLockTTL).Result()
		if err == nil {
			if !got {
				return func() {}, false
			}
			return func() {
				// 比對持有者再刪（Lua，原子）：鎖逾時被別人拿走後，不能把別人的鎖刪掉。
				rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				if err := corosMcpUnlockScript.Run(rctx, h.rdb, []string{key}, token).Err(); err != nil {
					log.Warn().Err(err).Msg("coros mcp: release sync lock failed (it expires by TTL)")
				}
			}, true
		}
		log.Warn().Err(err).Msg("coros mcp: redis sync lock failed, falling back to in-memory lock (not fail-open)")
	}
	got, _ := h.mem.claim(key, corosMcpSyncLockTTL, h.timeNow())
	if !got {
		return func() {}, false
	}
	return func() { h.mem.del(key) }, true
}

// allowProbe 讀取測試（僅超管）每人每分鐘最多 1 次；Redis 出錯退記憶體（不再 fail-open）。
func (h *CorosMcpHandler) allowProbe(ctx context.Context, userID string) (bool, int) {
	ok, left := h.claimKey(ctx, corosMcpKeyProbe+userID, corosMcpProbeWindow)
	if ok {
		return true, 0
	}
	return false, int(left.Seconds()) + 1
}

// claimManual 手動匯入名額（5 分鐘）。回傳 (放行, 還需等待秒數)。
func (h *CorosMcpHandler) claimManual(ctx context.Context, userID string) (bool, int) {
	ok, left := h.claimKey(ctx, corosMcpKeyManual+userID, corosMcpManualWindow)
	if ok {
		return true, 0
	}
	return false, int(left.Seconds()) + 1
}

// claimAutoSync 自動同步名額（25 分鐘）。
func (h *CorosMcpHandler) claimAutoSync(ctx context.Context, userID string) bool {
	ok, _ := h.claimKey(ctx, corosMcpKeyAutoSync+userID, corosMcpAutoSyncWindow)
	return ok
}

// isCorosThrottleErr COROS 以 429 或 5xx 回應（MCP 呼叫或 token 端點）→ 該使用者進入冷卻。
func isCorosThrottleErr(err error) bool {
	if err == nil {
		return false
	}
	var he *corosMcpHTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusTooManyRequests || he.Status >= 500
	}
	var te *corosMcpTokenError
	if errors.As(err, &te) {
		return te.Status == http.StatusTooManyRequests || te.Status >= 500
	}
	return false
}

// corosMcpErrorCode 把內部錯誤對應成「給前台／probe 紀錄看的穩定代碼」——前台不顯示 COROS 的原始錯誤字串
// （契約 §3.2），probe 紀錄也只存代碼。
func corosMcpErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errCorosMcpReconnect), errors.Is(err, errMCPUnauthorized):
		return "reauth_required"
	case errors.Is(err, errCorosMcpAnomaly):
		return "anomaly"
	case errors.Is(err, errCorosMcpNotConnected):
		return "not_connected"
	case errors.Is(err, errCorosMcpCooldown):
		return "cooldown"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	var he *corosMcpHTTPError
	if errors.As(err, &he) {
		switch {
		case he.Status == http.StatusTooManyRequests:
			return "rate_limited"
		case he.Status >= 500:
			return "upstream_5xx"
		default:
			return "http_error"
		}
	}
	var te *corosMcpTokenError
	if errors.As(err, &te) {
		if te.Status == http.StatusTooManyRequests {
			return "rate_limited"
		}
		return "token_error"
	}
	if strings.Contains(err.Error(), "returned isError") {
		return "tool_error"
	}
	return "error"
}
