package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/cache"
)

// store.go：團練同步跑的 Redis 資料層（Lua 腳本 + Store）。資料模型見契約 §4。
//
// Key（前綴 rml:{<meet-uuid>}: ——hash tag 讓同一團練的多 key Lua 可用；Lua 內所有 key 一律走 KEYS[]，
// 腳本內不拼字串）：
//
//	grants  hash  uid → "n|sid|expMs"          24h（每次寫入刷新）。expMs = now+15min（有界過期 M1）
//	idx     hash  uid → n（團練內短編號，遞增、永不重用；n = HLEN idx + 1）   24h
//	names   hash  n → 顯示名稱                                              24h
//	              名冊版本 rv = meta.rv（單調遞增計數器）：新編號或既有編號改名時 +1。舊版用 HLEN names，
//	              但既有成員改名時 HLEN 不變，其他 client 的 rv 比對相同、永遠看不到新名字。
//	pos     hash  n → "laE5|lnE5|rxMs|acc"（只留最新一點，無歷史）          4min（每次成功寫入刷新）
//	hb      hash  n → rxMs（心跳；任何 /pos 都寫，不論有無 p）              4min
//	meta    hash  po=1 presence-only、dead=1 團練已死                       24h
//	f:<uid> str   頻率地板 SET NX PX 1500
//	rv:<uid> str  撤銷墓碑 = revokedAtMs                                     15min
//	rml:kill str  全域緊急關閉旗標（無 TTL；不在任何 hash tag 內——單機 Redis 才可與其他 key 同腳本，
//	              Railway Redis 為單節點；若日後改 Redis Cluster，這個 key 會 CROSSSLOT，需改成每團複本）
//
// ⚠️ 時間一律 Lua 內 redis.call('TIME')（多副本 API 的時鐘偏差不會讓年齡不一致；不信任 client 時鐘）。
// 只用 Redis ≥ 6.0 指令（PEXPIRE／SET … PX，不用 EXAT）。

// KillKey 全域緊急關閉旗標。
const KillKey = "rml:kill"

// ErrUnavailable Redis 不可用（含 rdb 為 nil）：live 端點 fail-closed 回 503，client 顯示 🔴、跑步不受影響。
var ErrUnavailable = errors.New("live: redis unavailable")

// ErrBadID meetID／uid 不是合法 UUID（不允許任意字串進 Redis key——M9：避免 `}`、`:`、`{` 造成
// hash tag 與 key 空間混淆）。
var ErrBadID = errors.New("live: invalid id")

// Outcome Lua 腳本的業務結果（基礎設施錯誤走 error，不在這裡）。
type Outcome string

const (
	OutcomeOK            Outcome = "ok"             // start／pos 成功
	OutcomeNeedOwnFix    Outcome = "need_own_fix"   // pos：自己沒有 ≤60 s 的位置 → 看不到他人（互惠 C1）
	OutcomePresenceOnly  Outcome = "presence_only"  // pos：不限地點團，只回 live，不存也不回座標
	OutcomeLiveFull      Outcome = "live_full"      // start：同步名額已滿
	OutcomeRevokedRecent Outcome = "revoked_recent" // start：撤銷墓碑時間 ≥ checkedAtMs（M1 競態）
	OutcomeDead          Outcome = "dead"           // 團練取消／刪除／隱藏
	OutcomeKilled        Outcome = "killed"         // 全域緊急關閉
	OutcomeGrantMissing  Outcome = "grant_missing"  // pos：無 grant（過期／Redis 清空）
	OutcomeSIDMismatch   Outcome = "sid_mismatch"   // pos：另一分頁／裝置持有 grant
	OutcomeRevoked       Outcome = "revoked"        // pos：被踢／拒絕／退出
	OutcomeTooFast       Outcome = "too_fast"       // pos：距上次 < 1.5 s
)

// RosterEntry 名冊項。JSON 序列化為 [n,"名稱"]。
type RosterEntry struct {
	N    int
	Name string
}

func (r RosterEntry) MarshalJSON() ([]byte, error) { return json.Marshal([2]any{r.N, r.Name}) }

// Peer 快照中的他人亮點。JSON 序列化為 [n, la, ln, age_s, acc]。
//
// ⚠️ 結構上只有「短編號 n + 最新一點」：沒有 user_id、account_code、email，也沒有任何歷史欄位
// （契約驗收 P1-5）。新增欄位前先想清楚這點。
type Peer struct {
	N     int
	LaE5  int64 // 緯度 ×1e5（約 1 m）
	LnE5  int64
	AgeMs int64 // 伺服器收到位置至今（已扣 client 回報的定位年齡 fa）
	Acc   int   // 精度（m）
}

func (p Peer) MarshalJSON() ([]byte, error) {
	age := p.AgeMs / 1000
	if age < 0 {
		age = 0
	}
	return json.Marshal([5]any{p.N, float64(p.LaE5) / 1e5, float64(p.LnE5) / 1e5, age, p.Acc})
}

// StartParams /live/start 傳給 Lua 的參數（名額上限、presence-only 由 runmeet 讀 DB 後帶入）。
type StartParams struct {
	UID          string
	SID          string
	Name         string // 已消毒
	MaxLive      int
	PresenceOnly bool
	// CheckedAtMs 讀 DB「之前」用 Redis TIME 取得的時間（見 Store.NowMs）。Lua 以它對照撤銷墓碑，
	// 關掉「start 讀到 joined → Kick commit 並 Revoke → start 才寫 grant」的 TOCTOU（M1）。
	CheckedAtMs int64
}

// StartResult Lua start 的結果。Outcome 非 ok 時其餘欄位無意義。
type StartResult struct {
	Outcome Outcome
	N       int
	RV      int // 名冊版本（meta.rv：新編號或改名時 +1；與 /pos 請求帶的 rv 比對決定要不要附完整名冊）
	Live    int // 60 s 內有心跳的人數
	ExpMs   int64
	NowMs   int64
	Roster  []RosterEntry
}

// PosParams /pos 傳給 Lua 的參數（座標已由 handler 驗證並轉成 E5 整數）。
type PosParams struct {
	UID      string
	SID      string
	HasP     bool
	LaE5     int64
	LnE5     int64
	Acc      int
	FaS      int // client 回報的定位年齡（秒）：伺服器時間戳 = TIME − fa
	ClientRV int
}

// PosResult Lua pos 的結果。
type PosResult struct {
	Outcome      Outcome
	RetryAfterMs int64 // too_fast：地板剩餘毫秒
	NowMs        int64
	Live         int
	RV           int
	ExpMs        int64
	Roster       []RosterEntry // 僅當請求帶的 rv ≠ 伺服器 rv 時非 nil（完整名冊）
	RosterSent   bool
	Peers        []Peer
}

// Store 團練同步跑的 Redis 存取層。nil-safe：rdb 為 nil 時所有方法回 ErrUnavailable。
//
// ⚠️ 每一次 Redis 往返都有 RedisTimeout（2 秒）的硬期限，逾時一律回 ErrUnavailable（handler 立刻答 503
// redis_unavailable）。期限是**真的**：go-redis 預設不會因為 context 期限而中斷 socket 讀取（Redis 卡死時
// 一個指令實測卡 5～10 秒），所以走 cache.Bounded（與原 client 共用連線池、讀寫逾時 = 2 秒，
// 再加 context 期限讓重試迴圈在第一次失敗後就停）——機制與取捨見 cache/bounded.go 的檔頭。
// 逾時只代表 client 不等了，指令可能已在 Redis 端執行：start／pos／leave／revoke 都是冪等的。
type Store struct{ b *cache.Bounded }

// NewStore 建立 Store。rdb 可為 nil（測試／未接線時 live 功能整個 fail-closed）。
func NewStore(rdb *redis.Client) *Store { return &Store{b: cache.NewBounded(rdb, RedisTimeout)} }

// --- key 組裝 ---

type keySet struct{ p string }

func (k keySet) grants() string          { return k.p + "grants" }
func (k keySet) idx() string             { return k.p + "idx" }
func (k keySet) names() string           { return k.p + "names" }
func (k keySet) pos() string             { return k.p + "pos" }
func (k keySet) hb() string              { return k.p + "hb" }
func (k keySet) meta() string            { return k.p + "meta" }
func (k keySet) floor(uid string) string { return k.p + "f:" + uid }
func (k keySet) tomb(uid string) string  { return k.p + "rv:" + uid }

// canonUUID 解析並回規範小寫字串；非法回 ErrBadID。所有進 Redis key 的 id 都先過這裡
// （路徑參數可能是大寫、花括號等 uuid.Parse 也接受的寫法，必須收斂成同一個 key）。
func canonUUID(s string) (string, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return "", ErrBadID
	}
	return u.String(), nil
}

func newKeySet(meetID string) (keySet, error) {
	m, err := canonUUID(meetID)
	if err != nil {
		return keySet{}, err
	}
	return keySet{p: "rml:{" + m + "}:"}, nil
}

// --- Lua ---

// luaConsts 把 Go 端的常數寫進腳本文字（單一資料來源；腳本沒有額外 ARGV 常數）。
var luaConsts = strings.NewReplacer(
	"{{GRANT_MS}}", strconv.FormatInt(GrantTTL.Milliseconds(), 10),
	"{{KEY_TTL_MS}}", strconv.FormatInt(KeyTTL.Milliseconds(), 10),
	"{{POS_TTL_MS}}", strconv.FormatInt(PosTTL.Milliseconds(), 10),
	"{{FLOOR_MS}}", strconv.Itoa(FloorMs),
	"{{FRESH_MS}}", strconv.Itoa(FreshMs),
)

func mkScript(src string) *redis.Script { return redis.NewScript(luaConsts.Replace(src)) }

// luaNow 取 Redis 時間（毫秒）。TIME 之後才寫入：Redis ≥ 5 預設 effects replication，允許非確定性指令
// 之後寫入；契約已限定 Redis ≥ 6.0。
const luaNow = `
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
`

// startScript 契約 §4「Lua start」。
//
//	KEYS: 1 grants, 2 idx, 3 names, 4 meta, 5 rv:<uid>, 6 hb, 7 kill
//	ARGV: 1 uid, 2 sid, 3 name, 4 maxLive, 5 presenceOnly(0|1), 6 checkedAtMs
//	回傳：{'killed'|'dead'|'revoked_recent'|'live_full'} 或
//	      {'ok', n, rv, live, expMs, nowMs, n1, name1, n2, name2, …}（名冊攤平）
//
// 與契約文字的兩個刻意差異（外部行為等價或更嚴謹）：
//  1. 名額檢查排在「分配 n／寫 idx／names」之前：被拒絕（live_full）的人不會在名冊裡占一個 n。
//  2. 撤銷墓碑用 ≥ 比較（契約寫 >）：墓碑與 checkedAtMs 落在同一毫秒時無法判斷先後，保守拒絕；
//     合法的「被踢 → 重新加入 → 再 start」序列 checkedAtMs 必然晚於墓碑，不會被誤擋。
//
// 成功時一併 DEL 墓碑：通過檢查代表 DB 在撤銷「之後」讀到 joined（= 已重新加入），墓碑任務已完成；
// 留著會讓 /pos 在重新加入後的 15 分鐘內誤判 revoked。
var startScript = mkScript(luaNow + `
local uid, sid, name = ARGV[1], ARGV[2], ARGV[3]
local maxLive = tonumber(ARGV[4])
local checkedAt = tonumber(ARGV[6])

if redis.call('EXISTS', KEYS[7]) == 1 then return {'killed'} end
if redis.call('HGET', KEYS[4], 'dead') == '1' then return {'dead'} end

local tomb = redis.call('GET', KEYS[5])
if tomb and tonumber(tomb) and tonumber(tomb) >= checkedAt then
  return {'revoked_recent'}
end

-- 名額：遍歷 grants，刪掉過期項後計數（過期的 grant 不佔名額）
local grants = redis.call('HGETALL', KEYS[1])
local count, mine = 0, false
for i = 1, #grants, 2 do
  local exp = tonumber(string.match(grants[i + 1], '|(%d+)$'))
  if (not exp) or exp <= now then
    redis.call('HDEL', KEYS[1], grants[i])
  else
    count = count + 1
    if grants[i] == uid then mine = true end
  end
end
if (not mine) and count >= maxLive then return {'live_full'} end

-- 團練內短編號（遞增、永不重用）
local n = redis.call('HGET', KEYS[2], uid)
if n then
  n = tonumber(n)
else
  n = redis.call('HLEN', KEYS[2]) + 1
  redis.call('HSET', KEYS[2], uid, string.format('%d', n))
end
local ns = string.format('%d', n)

-- 名冊版本 rv（meta.rv，單調遞增）：新編號或既有編號改名才 +1（名字沒變的重驗不動它）。
-- 相容舊資料（舊版 rv = HLEN names、沒有 meta.rv）：第一次寫入時從 HLEN names 接續，rv 不會倒退。
if redis.call('HEXISTS', KEYS[4], 'rv') == 0 then
  redis.call('HSET', KEYS[4], 'rv', redis.call('HLEN', KEYS[3]))
end
local oldName = redis.call('HGET', KEYS[3], ns)
if (not oldName) or oldName ~= name then
  redis.call('HINCRBY', KEYS[4], 'rv', 1)
end
redis.call('HSET', KEYS[3], ns, name)

local exp = now + {{GRANT_MS}}
redis.call('HSET', KEYS[1], uid, ns .. '|' .. sid .. '|' .. string.format('%d', exp))
if ARGV[5] == '1' then
  redis.call('HSET', KEYS[4], 'po', '1')
else
  redis.call('HDEL', KEYS[4], 'po')
end
if tomb then redis.call('DEL', KEYS[5]) end
for i = 1, 4 do redis.call('PEXPIRE', KEYS[i], {{KEY_TTL_MS}}) end

local live = 0
local hbs = redis.call('HGETALL', KEYS[6])
for i = 1, #hbs, 2 do
  local ts = tonumber(hbs[i + 1])
  if ts and (now - ts) <= {{FRESH_MS}} then live = live + 1 end
end

local out = {'ok', n, tonumber(redis.call('HGET', KEYS[4], 'rv')), live, exp, now}
local names = redis.call('HGETALL', KEYS[3])
for i = 1, #names, 2 do
  out[#out + 1] = tonumber(names[i])
  out[#out + 1] = names[i + 1]
end
return out
`)

// posScript 契約 §4「Lua pos」。
//
//	KEYS: 1 grants, 2 pos, 3 hb, 4 names, 5 meta, 6 f:<uid>, 7 kill, 8 rv:<uid>
//	ARGV: 1 uid, 2 sid, 3 hasP(0|1), 4 laE5, 5 lnE5, 6 acc, 7 fa(秒), 8 clientRv, 9 dropMs
//	回傳：{'revoked'|'grant_missing'|'sid_mismatch'|'killed'|'dead'} 或 {'too_fast', pttlMs} 或
//	      {own, nowMs, live, rv, expMs, roster|false, peers}
//	      own ∈ ok | need_own_fix | presence_only；roster 為攤平 {n, name, …}（rv 相同時為 false）；
//	      peers 為 {{n, laE5, lnE5, ageMs, acc}, …}
//
// 檢查順序（先 grant、後 kill／dead，與契約一致；也符合審查 m4「無 grant 一律同一個回應」，
// 非成員無法用 409／410 差異探測某 meetId 是否存在或已取消）：
//
//	grant 不存在 →（有撤銷墓碑 ? revoked : grant_missing）→ grant 過期 → sid 不符 → kill → dead → 頻率地板
//
// ⚠️ 與契約文字的差異：契約寫「/pos 先查 rv:<uid> 存在 → revoked」。若墓碑優先於 grant，被踢後
// 重新加入並成功 start 的人，在墓碑 15 分鐘內會被 /pos 誤判 revoked（grant 明明是合法新發的）。
// 改成「grant 存在就信 grant、grant 不在才看墓碑」：被撤銷者（Revoke 已 HDEL grant）一樣得到 403
// revoked；重新加入者得到正常回應。start 成功時另外會 DEL 墓碑。
//
// 寫入全部排在所有拒絕檢查之後（拒絕的請求不會改動任何狀態，頻率地板除外——它本來就是「消耗一次額度」）。
var posScript = mkScript(luaNow + `
local g = redis.call('HGET', KEYS[1], ARGV[1])
if not g then
  if redis.call('EXISTS', KEYS[8]) == 1 then return {'revoked'} end
  return {'grant_missing'}
end
local n, sid, exp = string.match(g, '^(%d+)|([^|]*)|(%d+)$')
if not n then
  redis.call('HDEL', KEYS[1], ARGV[1])
  return {'grant_missing'}
end
exp = tonumber(exp)
if exp <= now then
  redis.call('HDEL', KEYS[1], ARGV[1])
  redis.call('HDEL', KEYS[2], n)
  redis.call('HDEL', KEYS[3], n)
  return {'grant_missing'}
end
if sid ~= ARGV[2] then return {'sid_mismatch'} end
if redis.call('EXISTS', KEYS[7]) == 1 then return {'killed'} end
if redis.call('HGET', KEYS[5], 'dead') == '1' then return {'dead'} end
if not redis.call('SET', KEYS[6], '1', 'NX', 'PX', {{FLOOR_MS}}) then
  return {'too_fast', redis.call('PTTL', KEYS[6])}
end

local po = (redis.call('HGET', KEYS[5], 'po') == '1')

-- 心跳（任何 /pos 都寫）；presence-only 團永不寫 pos（座標連進 Redis 都不會）
redis.call('HSET', KEYS[3], n, string.format('%d', now))
if (not po) and ARGV[3] == '1' then
  local rx = now - tonumber(ARGV[7]) * 1000
  redis.call('HSET', KEYS[2], n, ARGV[4] .. '|' .. ARGV[5] .. '|' .. string.format('%d', rx) .. '|' .. ARGV[6])
end
redis.call('PEXPIRE', KEYS[2], {{POS_TTL_MS}})
redis.call('PEXPIRE', KEYS[3], {{POS_TTL_MS}})

local live = 0
local hbs = redis.call('HGETALL', KEYS[3])
for i = 1, #hbs, 2 do
  local ts = tonumber(hbs[i + 1])
  if ts and (now - ts) <= {{FRESH_MS}} then live = live + 1 end
end

local own = 'ok'
local peers = {}
if po then
  own = 'presence_only'
else
  -- 互惠規則 C1：自己沒有 ≤60 s 的位置就看不到他人（沒分享就沒得看）
  local fresh = false
  local mine = redis.call('HGET', KEYS[2], n)
  if mine then
    local rx = string.match(mine, '^%-?%d+|%-?%d+|(%d+)|%d+$')
    if rx and (now - tonumber(rx)) <= {{FRESH_MS}} then fresh = true end
  end
  if not fresh then
    own = 'need_own_fix'
  else
    local all = redis.call('HGETALL', KEYS[2])
    local dropMs = tonumber(ARGV[9])
    for i = 1, #all, 2 do
      local k = all[i]
      if k ~= n then
        local la, ln, rx, ac = string.match(all[i + 1], '^(%-?%d+)|(%-?%d+)|(%d+)|(%d+)$')
        if not la then
          redis.call('HDEL', KEYS[2], k)
        else
          local age = now - tonumber(rx)
          if age > dropMs then
            redis.call('HDEL', KEYS[2], k)
          else
            peers[#peers + 1] = {tonumber(k), tonumber(la), tonumber(ln), age, tonumber(ac)}
          end
        end
      end
    end
  end
end

local rv = tonumber(redis.call('HGET', KEYS[5], 'rv') or '')
if not rv then rv = redis.call('HLEN', KEYS[4]) end -- 舊資料沒有 meta.rv：退回 HLEN names（下一次 start 會接續）
local roster = false
if tonumber(ARGV[8]) ~= rv then
  roster = {}
  local names = redis.call('HGETALL', KEYS[4])
  for i = 1, #names, 2 do
    roster[#roster + 1] = tonumber(names[i])
    roster[#roster + 1] = names[i + 1]
  end
end
return {own, now, live, rv, exp, roster, peers}
`)

// leaveScript 契約 §3.3：僅當 grant 內的 sid 相符才刪 grant＋該 n 的 pos／hb；**不寫墓碑**（M5：
// 跑完立刻開下一趟時，晚到的 leave 不能讓新 grant 被判 revoked）。
//
//	KEYS: 1 grants, 2 pos, 3 hb；ARGV: 1 uid, 2 sid；回傳 1＝已刪、0＝無事發生
var leaveScript = mkScript(`
local g = redis.call('HGET', KEYS[1], ARGV[1])
if not g then return 0 end
local n, sid = string.match(g, '^(%d+)|([^|]*)|%d+$')
if (not n) or sid ~= ARGV[2] then return 0 end
redis.call('HDEL', KEYS[1], ARGV[1])
redis.call('HDEL', KEYS[2], n)
redis.call('HDEL', KEYS[3], n)
return 1
`)

// revokeScript 契約 §4「Go Revoke」：HGET idx → HDEL grants／pos／hb、SET rv:<uid> <nowMs> PX。
// 契約允許非 Lua，但要求單一 MULTI；「先 HGET 取 n 再 HDEL pos／hb」在 MULTI 裡做不到
// （交易內拿不到前一步的結果），所以用 Lua（天然原子）。時間戳取 Redis TIME，與 start 的
// checkedAtMs 同一個時鐘。
//
//	KEYS: 1 grants, 2 idx, 3 pos, 4 hb, 5 rv:<uid>；ARGV: 1 uid, 2 tombstoneTtlMs
var revokeScript = mkScript(luaNow + `
local n = redis.call('HGET', KEYS[2], ARGV[1])
redis.call('HDEL', KEYS[1], ARGV[1])
if n then
  redis.call('HDEL', KEYS[3], n)
  redis.call('HDEL', KEYS[4], n)
end
redis.call('SET', KEYS[5], string.format('%d', now), 'PX', ARGV[2])
return 1
`)

// --- Store 方法 ---

func (s *Store) ready() error {
	if s == nil || s.b == nil {
		return ErrUnavailable
	}
	return nil
}

func unavailable(err error) error { return fmt.Errorf("%w: %v", ErrUnavailable, err) }

// NowMs 回 Redis TIME（毫秒）。/live/start 必須在「讀 DB 之前」呼叫，結果當 StartParams.CheckedAtMs。
func (s *Store) NowMs(ctx context.Context) (int64, error) {
	if err := s.ready(); err != nil {
		return 0, err
	}
	ctx, cancel := s.b.Ctx(ctx)
	defer cancel()
	t, err := s.b.C.Time(ctx).Result()
	if err != nil {
		return 0, unavailable(err)
	}
	return t.UnixMilli(), nil
}

// Start 建立／續期 grant（契約 §4 Lua start）。
func (s *Store) Start(ctx context.Context, meetID string, p StartParams) (StartResult, error) {
	if err := s.ready(); err != nil {
		return StartResult{}, err
	}
	ctx, cancel := s.b.Ctx(ctx)
	defer cancel()
	k, err := newKeySet(meetID)
	if err != nil {
		return StartResult{}, err
	}
	uid, err := canonUUID(p.UID)
	if err != nil {
		return StartResult{}, err
	}
	po := "0"
	if p.PresenceOnly {
		po = "1"
	}
	raw, err := startScript.Run(ctx, s.b.C,
		[]string{k.grants(), k.idx(), k.names(), k.meta(), k.tomb(uid), k.hb(), KillKey},
		uid, p.SID, p.Name, p.MaxLive, po, p.CheckedAtMs).Result()
	if err != nil {
		return StartResult{}, unavailable(err)
	}
	arr, ok := raw.([]interface{})
	if !ok || len(arr) == 0 {
		return StartResult{}, unavailable(errors.New("start: unexpected reply"))
	}
	res := StartResult{Outcome: Outcome(asString(arr[0]))}
	if res.Outcome != OutcomeOK {
		return res, nil
	}
	if len(arr) < 6 {
		return StartResult{}, unavailable(errors.New("start: short reply"))
	}
	res.N = int(asInt(arr[1]))
	res.RV = int(asInt(arr[2]))
	res.Live = int(asInt(arr[3]))
	res.ExpMs = asInt(arr[4])
	res.NowMs = asInt(arr[5])
	res.Roster = parseRoster(arr[6:])
	return res, nil
}

// Pos 寫入心跳／最新位置並回快照（契約 §4 Lua pos）。
func (s *Store) Pos(ctx context.Context, meetID string, p PosParams) (PosResult, error) {
	if err := s.ready(); err != nil {
		return PosResult{}, err
	}
	ctx, cancel := s.b.Ctx(ctx)
	defer cancel()
	k, err := newKeySet(meetID)
	if err != nil {
		return PosResult{}, err
	}
	uid, err := canonUUID(p.UID)
	if err != nil {
		return PosResult{}, err
	}
	hasP := "0"
	if p.HasP {
		hasP = "1"
	}
	raw, err := posScript.Run(ctx, s.b.C,
		[]string{k.grants(), k.pos(), k.hb(), k.names(), k.meta(), k.floor(uid), KillKey, k.tomb(uid)},
		uid, p.SID, hasP, p.LaE5, p.LnE5, p.Acc, p.FaS, p.ClientRV, int64(DropS)*1000).Result()
	if err != nil {
		return PosResult{}, unavailable(err)
	}
	arr, ok := raw.([]interface{})
	if !ok || len(arr) == 0 {
		return PosResult{}, unavailable(errors.New("pos: unexpected reply"))
	}
	res := PosResult{Outcome: Outcome(asString(arr[0]))}
	switch res.Outcome {
	case OutcomeOK, OutcomeNeedOwnFix, OutcomePresenceOnly:
		if len(arr) < 7 {
			return PosResult{}, unavailable(errors.New("pos: short reply"))
		}
		res.NowMs = asInt(arr[1])
		res.Live = int(asInt(arr[2]))
		res.RV = int(asInt(arr[3]))
		res.ExpMs = asInt(arr[4])
		if flat, ok := arr[5].([]interface{}); ok {
			res.RosterSent = true
			res.Roster = parseRoster(flat)
		}
		res.Peers = parsePeers(arr[6])
	case OutcomeTooFast:
		if len(arr) > 1 {
			res.RetryAfterMs = asInt(arr[1])
		}
	}
	return res, nil
}

// Leave 結束跑步／離開同步：僅 sid 相符才刪 grant 與該 n 的 pos／hb；不寫墓碑。回傳是否真的刪了。
func (s *Store) Leave(ctx context.Context, meetID, uid, sid string) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	ctx, cancel := s.b.Ctx(ctx)
	defer cancel()
	k, err := newKeySet(meetID)
	if err != nil {
		return false, err
	}
	u, err := canonUUID(uid)
	if err != nil {
		return false, err
	}
	n, err := leaveScript.Run(ctx, s.b.C, []string{k.grants(), k.pos(), k.hb()}, u, sid).Int()
	if err != nil {
		return false, unavailable(err)
	}
	return n == 1, nil
}

// Revoke 撤銷某人的同步（被踢／拒絕／退出／封鎖）：HDEL grant／pos／hb、寫墓碑 rv:<uid>（15 分鐘）。
// ⚠️ 必須在 DB commit 之「後」呼叫——commit 之前呼叫會被後到的 start（還讀得到舊的 joined）覆蓋。
func (s *Store) Revoke(ctx context.Context, meetID, uid string) error {
	if err := s.ready(); err != nil {
		return err
	}
	ctx, cancel := s.b.Ctx(ctx)
	defer cancel()
	k, err := newKeySet(meetID)
	if err != nil {
		return err
	}
	u, err := canonUUID(uid)
	if err != nil {
		return err
	}
	if err := revokeScript.Run(ctx, s.b.C,
		[]string{k.grants(), k.idx(), k.pos(), k.hb(), k.tomb(u)},
		u, TombstoneTTL.Milliseconds()).Err(); err != nil {
		return unavailable(err)
	}
	return nil
}

// MarkDead 團練取消／刪除／後台下架：所有人下一次 /pos 得 410 meet_over，start 也被拒。
func (s *Store) MarkDead(ctx context.Context, meetID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	ctx, cancel := s.b.Ctx(ctx)
	defer cancel()
	k, err := newKeySet(meetID)
	if err != nil {
		return err
	}
	pipe := s.b.C.TxPipeline()
	pipe.HSet(ctx, k.meta(), "dead", "1")
	pipe.PExpire(ctx, k.meta(), KeyTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return unavailable(err)
	}
	return nil
}

// ClearDead 團練恢復（cancelled→open、後台取消下架）：清掉 dead 旗標。
//
// ⚠️ 契約只列了 MarkDead；沒有對應的清除會讓「中止後恢復」或「後台下架後取消下架」的團練在
// meta TTL（24 h）內一直回 meet_over。呼叫端必須確認團練目前「完全可用」（未取消、未刪除、未被下架）。
func (s *Store) ClearDead(ctx context.Context, meetID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	ctx, cancel := s.b.Ctx(ctx)
	defer cancel()
	k, err := newKeySet(meetID)
	if err != nil {
		return err
	}
	if err := s.b.C.HDel(ctx, k.meta(), "dead").Err(); err != nil {
		return unavailable(err)
	}
	return nil
}

// SetKill 全域緊急關閉（契約 §4 M2）：on → SET rml:kill 1；off → DEL。
func (s *Store) SetKill(ctx context.Context, on bool) error {
	if err := s.ready(); err != nil {
		return err
	}
	ctx, cancel := s.b.Ctx(ctx)
	defer cancel()
	var err error
	if on {
		err = s.b.C.Set(ctx, KillKey, "1", 0).Err()
	} else {
		err = s.b.C.Del(ctx, KillKey).Err()
	}
	if err != nil {
		return unavailable(err)
	}
	return nil
}

// --- 回覆解析 ---

func asInt(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

func asString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	}
	return ""
}

// parseRoster 攤平的 {n, name, n, name, …} → 依 n 排序的名冊。
func parseRoster(flat []interface{}) []RosterEntry {
	out := make([]RosterEntry, 0, len(flat)/2)
	for i := 0; i+1 < len(flat); i += 2 {
		out = append(out, RosterEntry{N: int(asInt(flat[i])), Name: asString(flat[i+1])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].N < out[j].N })
	return out
}

// parsePeers {{n, laE5, lnE5, ageMs, acc}, …} → 依 n 排序的亮點（輸出順序穩定，方便測試）。
func parsePeers(v interface{}) []Peer {
	arr, _ := v.([]interface{})
	out := make([]Peer, 0, len(arr))
	for _, e := range arr {
		t, ok := e.([]interface{})
		if !ok || len(t) < 5 {
			continue
		}
		out = append(out, Peer{
			N: int(asInt(t[0])), LaE5: asInt(t[1]), LnE5: asInt(t[2]),
			AgeMs: asInt(t[3]), Acc: int(asInt(t[4])),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].N < out[j].N })
	return out
}
