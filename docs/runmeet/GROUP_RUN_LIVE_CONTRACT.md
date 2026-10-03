# 團練同步跑（Group Run Live）實作契約 v1

日期：2026-10-02。來源：scratchpad grouprun/GROUP_RUN_DESIGN.md（設計＋對抗式審查）＋擁有者拍板。
本文件是 P1–P4 四個工作包的**唯一真相**；設計文件與審查只是背景。衝突時以本文件為準。

## 0. 擁有者決策（2026-10-02）

| 題 | 決定 |
|---|---|
| 更新頻率 | **依同步人數自動調整**：≤20 人 5 s、21–35 人 7 s、36–50 人 10 s（伺服器下發 `iv`，client 遵守） |
| 「開始跑步」開放時段 | **集合前 30 分 ～ 結束後 30 分**；沒有 `ends_at` 的團練以 `meet_at + 3 h` 當結束 |
| 不限地點（`no_location`）團練 | **可開跑、只顯示在跑人數**，伺服器端永不收/存座標（presence-only） |
| 上線範圍 | **白名單**：超管恆可用＋`runmeet_live_whitelist` 指定帳號；驗收後再改 open |

其餘採設計建議：不標記 gps_run（不需 migration）、只有「正在分享位置」的成員互相看得到（互惠）、v1 不做觀戰、泡泡只放名字、名額上限 50（可調）、進入 tracking 才開始同步、每次開跑一頁式確認（可勾「這個團練不再提醒」）。

## 1. 範圍與不變量

- 團練詳情頁（發起人＋`joined` 成員）新增「🏃 開始跑步」→ `/track?meet=<uuid>`：**自由跑**（目標 `none`、不計時、無里程目標），所有 HUD／Wake Lock／專注鎖定（長按 1.5 s 離開）／gps_run 上傳／反作弊／獎勵**零改動**。同步只是疊在自由跑上的「盡力而為」功能：**同步任何失敗都不得影響跑步記錄**。
- 他人的位置**只存在 Redis 記憶體、只保留最新一點**（無歷史、無軌跡、不寫 DB、不寫 log）；自己的軌跡仍只在本機與既有 gps 上傳。
- 熱路徑（`/pos`、`/leave`）**零 PostgreSQL 查詢**（RequireAuth 既有 session 快取除外）。不新增任何背景 goroutine／排程（Neon 要能睡）。
- 不回傳 account_code／email／user_id；玩家可見名字 = `COALESCE(NULLIF(u.name,''), u.handle)`（display-name-convention），禁讀個資暱稱。
- 不改 `/ws/site`、`/track/ping`、realtime hub。不用 WebSocket（理由見設計 §2.1：`maxPerIPConn=60` 同出口會撞、hub 是全站失效匯流排）。
- 所有新全螢幕層 z < 3900；新 DOM 不放進可拖曳面板捲動容器；PC 手機框規則照舊。
- 中文一律「團練」，不得寫「跑團」。金底白字規則適用於按鈕。

## 2. 入口與時窗（P1 後端、P4 前端）

**設定鍵（app_settings，無列時用程式預設；不需 migration）**

| key | 預設 | 說明 |
|---|---|---|
| `runmeet_live_entry_state` | `whitelist` | hidden／locked／whitelist／open，語意同 `runmeet_entry_state`，判斷沿用 `entryFrom(state, whitelist, email, code)`；超管恆 shown |
| `runmeet_live_whitelist` | `""` | 同 `runmeet_entry_whitelist` 格式 |
| `runmeet_live_max` | `50` | 同一團練同時持有 grant 的人數上限 |
| `runmeet_live_pre_minutes` | `30` | 開放起點 = meet_at − pre |
| `runmeet_live_default_hours` | `3` | `ends_at` 為 NULL 時的結束 = meet_at + 此值 |
| `runmeet_live_grace_minutes` | `30` | 開放終點 = 結束 + grace |

- 誰可用：團練 `deleted_at IS NULL AND hidden_by_admin=FALSE AND status IN ('open','closed')`；請求者是 owner 或 `run_meet_members.status='joined'`；且 live 入口 shown。`hidden_by_owner` 不影響 joined 成員（與既有可見性一致）。
- 時窗：`opens_at = meet_at − pre`，`closes_at = COALESCE(ends_at, meet_at + default_hours) + grace`；`/live/start` 在 `now ∉ [opens_at, closes_at)` → 409 `outside_window`。
- 詳情 DTO：**只在 `MemberDetailView`** 新增 `live`（`PublicDetailView` 不得出現）：
  `{"enabled":bool, "opens_at":RFC3339, "closes_at":RFC3339, "server_now":RFC3339, "presence_only":bool}`；`enabled=false` 代表該使用者的 live 入口非 shown（前端不顯示按鈕）。前端用 `server_now − Date.now()` 修正手機時鐘偏差，最終以 `/live/start` 回應為準。

## 3. 端點與協定（`pv = 1`）

| 端點 | 掛載 | DB | 限流 |
|---|---|---|---|
| `POST /api/v1/run-meets/{id}/live/start` | 既有 runmeet `Router()`（requireEntry 照走） | 是（每次 start） | `runmeet_live_start` 6/min/user |
| `POST /api/v1/run-meet-live/{id}/pos` | **新的輕量子路由**（main.go 已登入群組下 `Mount("/run-meet-live", live.Router())`） | 否 | `runmeet_live_pos` 60/min/user |
| `POST /api/v1/run-meet-live/{id}/leave` | 同上 | 否 | 同上桶 |

- 新子路由**不**經 runmeet `requireEntry`（它每請求查 DB）。RequireAuth 已由上層群組提供；子路由內 RateLimit 的 dim 用 `UserOrIP`——**必須確認 RequireAuth 在 RateLimit 之前執行**（否則 50 人同出口 IP 共用一桶）；加一個路由掛載順序測試。
- handler 第一行 `uuid.Parse(chi.URLParam(r,"id"))`，非法 → 404；Redis key 一律用解析後的規範字串。
- Body 上限 512 B（`http.MaxBytesReader`）；JSON 未知欄位忽略；`pv` 缺或 ≠ 1 → 426 `{"error":"upgrade_required"}`（client 顯示「請重新整理頁面」）。

### 3.1 `POST …/live/start`
請求：`{"pv":1,"sid":"<client 隨機 16–32 字元 [A-Za-z0-9]>","consent_v":1,"reauth":false}`
- `reauth=false`（使用者按下開始後的第一次）：`consent_v` 必填（整數 ≥ 1，缺 → 400 `consent_required`），並寫一列 `audit_logs`（`user_id`, `action='runmeet_live_consent'`, `resource='run_meet'`, `resource_id=<meetId>`, `meta={"consent_v":1,"presence_only":bool}`, `ip`）。**meta 不得含座標**。
- `reauth=true`（grant 快到期的靜默重驗）：不寫 audit，其餘檢查相同。
- 伺服器順序：uuid → 全域 live 入口（`ResolveLiveEntry`，非 shown → 403 `entry_closed`）→ 讀團練（不存在/刪除/後台隱藏 → 404；status cancelled → 410 `meet_over`）→ 成員資格（非 owner/joined → 403 `not_member`）→ 時窗（→ 409 `outside_window`）→ 取顯示名稱並消毒（§5）→ 取 `checkedAtMs`（讀 DB **之前**用 Redis `TIME` 取得）→ Lua `start`。
- Lua `start` 可能回：`live_full` → 429 `{"error":"live_full"}`（client 提示「同步名額已滿，仍可自由跑步」）、`revoked_recent`（墓碑時間 > checkedAtMs，M1 競態）→ 403 `revoked`、`dead` → 410 `meet_over`、`killed` → 410 `killed`。
- 成功 200：
```json
{"pv":1,"n":3,"sid":"…","iv":5000,"presence_only":false,"grant_ttl_s":900,"reauth_in_s":600,
 "meet":{"id":"…","title":"週六河濱團練","lat":25.03,"lng":121.56},
 "rv":7,"roster":[[1,"小明"],[2,"阿華"],[3,"我"]],"live":4,"max_live":50,
 "stale":{"fade_s":30,"gray_s":75,"drop_s":180},"server_now_ms":1759999999000}
```
`meet.lat/lng` 僅成員層可見（本端點已是成員）；`presence_only` 團不帶 lat/lng。`stale` 由 iv 計算：`fade=max(3·iv,30s)`、`gray=max(8·iv,75s)`、`drop=180s`。

### 3.2 `POST …/run-meet-live/{id}/pos`
請求：`{"pv":1,"sid":"…","p":{"la":25.03321,"ln":121.56543,"ac":8,"fa":2}}`（`p` 可省略＝只心跳／只輪詢）。驗證：`la∈[-90,90]`、`ln∈[-180,180]`、`ac∈[0,10000]`、`fa∈[0,600]`，不合 → 400。伺服器以 Redis `TIME` 為時間戳（扣掉 `fa`），**不信任 client 時鐘**。
成功 200：
```json
{"pv":1,"t":1759999999000,"iv":5000,"live":12,"rv":8,"own":"ok","reauth_in_s":540,
 "roster":[[1,"小明"],…],            // 僅當請求帶的 rv ≠ 伺服器 rv 時附上（完整名冊）
 "p":[[1,25.03401,121.56612,3,6],[2,25.03288,121.56477,41,12]]}  // [n, la, ln, age_s, acc]
```
- `own`：`ok`＝有自己 ≤60 s 的位置、回傳他人；`need_own_fix`＝自己沒有新位置 → **`p` 一律空陣列**（互惠規則 C1：沒分享就看不到）；`presence_only`＝不限地點團 → 永不含 `p`、不存任何座標，只回 `live`。
- `p` 不含請求者自己、不含 age > `drop_s` 者（Lua 讀取時順便 HDEL）。
- `live` = 60 s 內有心跳（任何 `/pos`，不論有無 `p`）的 grant 持有人數。
- `reauth_in_s` = grant 剩餘秒數 − 300（最小 0）；client 在 ≤ 0 時靜默 `start(reauth=true)`。
- 請求帶 `rv`（client 目前名冊版本，可省略＝0）。

錯誤：

| 碼 | error | 意義 | client |
|---|---|---|---|
| 409 | `grant_missing` | 無 grant（過期／Redis 清空） | 走 start 重建（自限 6/min、帶抖動） |
| 409 | `sid_mismatch` | 另一分頁／裝置的同一帳號持有 grant | 本分頁停止同步、提示「另一個視窗正在同步」 |
| 403 | `revoked` | 被踢／拒絕／退出／封鎖 | 停止同步、toast「你已不在此團練，同步已停止」、跑步繼續 |
| 410 | `meet_over` / `killed` | 團練取消/刪除/隱藏；或全域緊急關閉 | 停止同步 |
| 429 | `too_fast` | 距上次 < 1.5 s（`Retry-After` 秒） | 退避，不算錯誤 |
| 426 | `upgrade_required` | 協定版本不符 | 停止同步、顯示「請重新整理」 |
| 503 | `redis_unavailable` | Redis 不可用（fail-closed） | 退避、膠囊 🔴 |

### 3.3 `POST …/run-meet-live/{id}/leave`
請求 `{"pv":1,"sid":"…"}` → 204。Lua：僅當 grant 內 sid 相符才 DEL grant＋HDEL pos/hb；**不寫墓碑**（M5）。client 用 `fetch(..., {keepalive:true})`，失敗無妨。

## 4. Redis 資料與 Lua（P1）

前綴 `rml:{<uuid>}:`（hash tag 讓多 key Lua 可用；Lua 內所有 key 走 `KEYS[]`，不得在腳本內拼字串）。**只用 Redis ≥ 6.0 指令**（PEXPIRE／PX，不用 EXAT；Railway Redis 版本未驗證）。時間一律 `redis.call('TIME')`。

| Key | 型別 | 內容 | TTL |
|---|---|---|---|
| `grants` | hash | `uid → "n|sid|expMs"` | 24 h（每次寫入刷新） |
| `idx` | hash | `uid → n`（團練內短編號，遞增、永不重用） | 24 h |
| `names` | hash | `n → 顯示名稱` | 24 h |
| `pos` | hash | `n → "laE5|lnE5|rxMs|acc"` | 4 min（每次成功寫入刷新） |
| `hb` | hash | `n → rxMs`（心跳） | 4 min |
| `meta` | hash | `po`=1 presence-only、`dead`=1 | 24 h |
| `f:<uid>` | string | 頻率地板 `SET NX PX 1500` | 1.5 s |
| `rv:<uid>` | string | 撤銷墓碑 = revokedAtMs | 15 min |
| 全域 `rml:kill` | string | 緊急關閉旗標 | 無 |

- grant 有界過期（M1）：`expMs = now + 900 000`；`pos`/`leave` 以 hash 內 `expMs` 判定有效，過期視同 `grant_missing`（Lua 順手 HDEL）。
- **Lua `start`**（KEYS: grants, idx, names, meta, rv:<uid>, hb, kill；ARGV: uid, sid, name, maxLive, presenceOnly, checkedAtMs）：`kill` 存在 → `killed`；`meta.dead` → `dead`；`rv:<uid>` > checkedAtMs → `revoked_recent`；若 uid 無 `n` → `n = HLEN idx + 1`、HSET idx/names；名額：遍歷 `grants` 刪掉過期項後計數，若 uid 不在其中且 count ≥ maxLive → `live_full`；HSET grants；設 TTL；回 `n, rv(=HLEN names), live(=hb 內 ≤60 s 的數量), roster`。
- **Lua `pos`**（KEYS: grants, pos, hb, names, meta, f:<uid>, kill；ARGV: uid, sid, hasP, laE5, lnE5, acc, fa, clientRv, dropMs）：先查 grant（無/過期 → `grant_missing`；sid 不符 → `sid_mismatch`；值為 `-` 墓碑語意不使用——撤銷直接 HDEL grant 並寫 `rv:<uid>`，所以 `/pos` 先查 `rv:<uid>` 存在 → `revoked`）；`kill` → `killed`；`meta.dead` → `dead`；`SET f NX PX 1500` 失敗 → `too_fast`；HSET hb；若 `po≠1` 且 hasP → HSET pos；PEXPIRE pos/hb 240 000；計算 `live`；若 `po=1` → 回 `presence_only` 不帶 p；若自己在 pos 且 age ≤ 60 s → 收集他人（過濾自己與 age > dropMs，順手 HDEL）→ `ok`；否則 `need_own_fix`、p 空；`rv ≠ clientRv` 時附名冊。
- **Lua `leave`**：sid 相符才 HDEL grants/pos/hb。
- **Go `Revoke(meetId, uid)`**（非 Lua 亦可，但須單一 MULTI）：HGET idx → HDEL grants/pos/hb、`SET rv:<uid> <nowMs> PX 900000`。**在 DB commit 之後呼叫**。
- **`MarkDead(meetId)`**：HSET meta dead 1、PEXPIRE 24 h。
- 名額上限數字由 `/live/start` 讀設定後傳入 ARGV。
- Redis 清空：下一次 `/pos` → 409 `grant_missing` → client 重新 start（DB 一次），正確性仍以 DB 為真。

**撤銷掛鉤（P1 必列清單逐一掛上）**：`members.go` 的 LeaveOrWithdraw（status→left）、Reject（→rejected）、Kick／KickOne（→kicked）、任何 ban 路徑；`repository.go` `SetStatus`→`cancelled` 時 `MarkDead`；`SoftDelete`（deleted_at）→ `MarkDead`；`admin.go` 強制下架（hidden_by_admin=TRUE）→ `MarkDead`。`repository.go:704`（取消時婉拒 pending）不需撤銷。`hidden_by_owner` 不撤銷。驗收：grep `run_meet_members`／`run_meets` 所有寫入點對照清單，寫進測試檔註解。

**緊急關閉（M2）**：`appsettings` 後台寫入 `runmeet_live_entry_state` 時（找出 admin 寫 app_settings 的 handler，加「key 變更回呼」註冊機制，不要在 appsettings 內 import runmeet），值為 `hidden`→`SET rml:kill 1`，其他值→`DEL rml:kill`。≤ 1 個 tick 生效列為驗收。

## 5. 隱私與消毒（P1／P2／P3 共同）

- 顯示名稱消毒（`/live/start` 取名後，Go 端）：NFC 正規化；移除控制字元（Cc）、零寬／bidi 字元（U+200B–U+200F、U+202A–U+202E、U+2060–U+2064、U+2066–U+2069、U+FEFF）；trim；超過 20 個 rune 截斷加「…」；空字串退回 `handle`，再空 → 「跑者」。單元測試含 `<script>`、RTL、超長、emoji。
- **Leaflet tooltip 一律傳 DOM 元素（`textContent`）**，禁字串（C2）。
- 隱私區（C3，client 端）：記錄開跑第一個有效定位為「起點」；若起點與 `meet.lat/lng` 距離 ≤ 300 m → 立即分享；否則離起點 ≥ 200 m 才開始帶 `p`；之後若再回到起點 200 m 內（回家）→ 停止帶 `p`（心跳照送）。`presence_only` 團永不帶 `p`。同意視窗明講。
- 「暫停分享位置」切換（P2 必做）：暫停時只送心跳；因互惠規則也看不到他人，UI 必須寫明。
- logger／告警／httpLogs 不得記錄 request body（請用 `POST` body，禁改成 query string）。`gps_raw_log` 不得被本功能寫入。
- 同意：每次開跑一頁式確認（可勾「這個團練不再提醒」＝仍送 `consent_v`，只是不彈窗；存 localStorage `dor_meet_consent_v1:<meetId>`）。隱私權政策（/privacy）新增一段（P4）。

## 6. Client 同步行為（P2：`app/track/useMeetLive.ts`）

- 啟動：`useEffect([status==='tracking', meetId])`；首次 start 延遲 `random(0,2000) ms`（整點風暴 M10）；跑步不等它。
- 排程：下一次 = `iv × random(0.9,1.1)`；`navigator.connection?.saveData` → ×2；請求逾時 8 s；不重疊（前一個未回就跳過）；`AbortController` 在 cleanup 取消。
- 帶 `p` 的條件：距上次已送位置 ≥ 8 m **或** 距上次送出 ≥ 15 s（靜止也要 keepalive，M3）；精度 > 65 m 或定位年齡 > 10 s → 不帶 `p`；隱私區／暫停分享 → 不帶 `p`。
- 退避：5→10→20→30 s（±30 % 抖動），適用於網路錯誤、5xx、503、429（優先 `Retry-After`）、start 失敗；`grant_missing` → start(reauth=true)（自限 6/min）；`revoked`/`meet_over`/`killed`/`sid_mismatch`/426 → 停止（狀態 `stopped`，附訊息）。
- `reauth_in_s ≤ 0` → 下一 tick 先 start(reauth=true)。
- 頁面 hidden：清 timer（不送）；visible/pageshow/focus → ≥ 1.5 s 冷卻後立即補一次。
- 結束／status 離開 tracking → cleanup：abort、`leave`（keepalive）、清 peers。
- 不在 `onPos` 內送請求；由 timer 讀 `curPosRef`/`curPosAtRef`。
- 輸出：`meetPeersRef: MutableRefObject<MeetLivePeers>`（可變 ref，**不觸發 page 重繪**）＋節流 state `liveStats: MeetLiveStats`（每次快照更新 ≤ 1 Hz）。
- 共用型別（新檔 `app/track/meetLiveTypes.ts`，P3 直接 import）：
```ts
export type PeerDot = { n: number; name: string; lat: number; lng: number; acc: number; rxAt: number /*client ms*/; ageS: number /*server age at receipt*/ }
export type StaleThresholds = { fadeS: number; grayS: number; dropS: number }
export type MeetLivePeers = { active: boolean; dots: PeerDot[]; stale: StaleThresholds; selfRing: boolean }
export type MeetLiveState = 'idle'|'starting'|'live'|'need_fix'|'presence_only'|'paused'|'reconnecting'|'stopped'|'error'
export type MeetLiveStats = { state: MeetLiveState; live: number; iv: number; sharing: boolean; presenceOnly: boolean; message?: string; messageAt?: number; nearest: { n: number; name: string; distM: number; bearingDeg: number }[] }
```
陳舊度（渲染端計算）= `(Date.now() − rxAt)/1000 + ageS`。
- `window.__meetLiveDebug = { inject(dots: PeerDot[]), stats() }` 僅非 production 或 `?dev=1` 掛載，供 P3／E2E。

## 7. 渲染規格

- 自己：Leaflet 既有綠點 `#46E3A0`＋軌跡不變；三套 skin 的自己光球外加綠色細環（r+4、2 px），不改光球；自己永不畫泡泡。
- 他人：橘點＋白色 2 px 外框；主色 `#FF8A3D`（Leaflet／cute）、scifi 霓虹橘＋`lighter` 發光、retro 像素方塊 `#f8a000`＋黑框；形狀與自己不同（自己＝雙環，他人＝實心點，色弱可分）。
- 泡泡：圓角矩形＋向下小三角指向亮點；名稱 ≤ 6 中文字／10 英文字，超過「…」；`fadeS` 後 60 % 透明＋尾綴「· 35s」；`grayS` 後點改灰色空心環、泡泡改「訊號中斷」；`dropS` 後不畫。字型：CJK fallback `'Noto Sans TC', sans-serif`（retro 需截圖驗證無豆腐塊）。
- 避碰：依與自己距離排序，貪婪放置（上→右→左→再上疊一層）；完整泡泡上限 15，其餘只畫點；同一 24 px 格內 ≥3 點合併「+N」徽章；螢幕外不畫。
- 內插：由上一點線性滑到新點（約 1.0×iv，以 dt 為準不依賴幀率）；`prefers-reduced-motion` → 直接跳。
- Leaflet（P2）：`L.layerGroup` 緊鄰 `kmMarkersRef`；`Map<n, CircleMarker>`＋`setLatLng`；`bindTooltip(DOM 元素, {permanent:true, direction:'top'})`；永久 tooltip 只開最近 15 位；自己 `bringToFront()`。
- skin（P3）：三套 `renderFrame` 在 `drawTargets` 之後、自己光球之前畫橘點；所有點畫完後第二輪畫泡泡；只用 CSS 像素（沿用既有 `ctx.setTransform(dpr…)`，canvas 維持 CSS `width/height:100%`）；每個亮點包 try/catch＋`Number.isFinite`（**peer 繪製不得 throw**，連續幀失敗會退回 Leaflet）；`window.__scifiDebug/__retroDebug/__cuteDebug.peers = [{n, x, y, name, color}]`。
- 專注／鎖定模式（P2，`RaceFocusMode`）：props 新增 `meetLive?: MeetLiveStats`；資訊區加兩行（`role="status" aria-live="polite"`）：同步狀態「🟢 同步中 · 7 人／🟡 重連中／🔴 同步中斷／⚪ 搜尋 GPS 中（夥伴看不到你）／⏸ 已暫停分享」與最近 3 位「小明 約120m ↗ · 阿華 約340m ↙ · 另 5 人」（8 方位羅盤箭頭）；錯誤訊息停留 ≥ 10 s 並 `navigator.vibrate?.(200)`。**預設地圖在團練模式下不再純黑**：背景改成與 skin 相同形狀的漸層（上 45 % 透出地圖），其他模式維持 v850 純黑。同層 z 3900，不新增 fixed 層。暫停分享的切換放在專注模式資訊區（長按 1.5 s 同一手勢規格）與非專注的膠囊上。
- 同步膠囊（非專注時）：`#track-map-area` 兄弟節點、帶 `scifiFocusHideAttr`、z ≤ 960。

## 8. 詳情頁入口（P4）

- 位置：`RunMeetDetailView.tsx` 底部 CTA 上方獨立一列。純函式 `runMeetLiveCta(detail, nowMs)`（`lib/runMeet.ts`，與 `runMeetCta` 分開；`scripts/verify-run-meet.mjs` 加案例）：`live` 缺或 `enabled=false`／非成員／cancelled → 不顯示；`now < opens_at` → 灰色「🏃 開始跑步（HH:mm 開放）」disabled；時窗內 → 金底白字「🏃 開始跑步」；`now ≥ closes_at` → 不顯示；本機 `dor_gps_active.href` 含 `meet=<id>` → 「▶ 回到團練跑」。
- 小字：「團練同步需用手機 GPS 全程開啟；只用手錶記錄，夥伴看不到你的位置。」`presence_only` 團改：「不限地點團練：只顯示在跑人數，不分享位置。」
- 同意視窗（overlayMount portal、z < 3900）三點：分享對象與結束即停、不保存；需手機 GPS 全程開啟＋螢幕常亮（鎖定模式）、按側邊鍵關螢幕會暫停；起點 200 m 內與回到起點後不分享。勾選「這個團練不再提醒」存 localStorage。確認 → `router.push('/track?meet=<id>')`（不用 sessionStorage 交接，標題由 start 回傳）。
- 時窗以 `server_now` 校正；`ends_at` 為 NULL 時 `meetPhase` 在 meet_at 即 `ended`：確認詳情頁不會因 `is_ended` 隱藏 `MemberDetailView`／按鈕；同步時窗內顯示 badge「團練進行中」。
- 後台：`apps/web/src/lib/appSettings.ts` 新 group「團練同步跑」登記 §2 六個 key（白名單 `type:'text'`）。
- /privacy 新增一段。每日 08:00 營運報告加一行「團練同步跑場次／峰值人數」僅在能以 Redis 計數取得時才做（不得掃 DB）；否則留待後續。

## 9. 分階段與驗收

**P1 後端**（Go only，不碰 web）
1. 非成員／pending／kicked／left 的 `/live/start` → 403；成員時窗外 → 409；cancelled → 410；入口非 shown → 403。
2. `/pos`、`/leave` 熱路徑零 PostgreSQL 查詢（handler 不持有 pool；以型別保證＋測試）。
3. Kick／退出／拒絕後下一次 `/pos` → 403 `revoked`，且該亮點已從他人快照消失；`SetStatus(cancelled)`／`SoftDelete`／後台下架後 `/pos` → 410。
4. 互惠：請求者無 ≤60 s 位置 → `p=[]`、`own=need_own_fix`；presence-only 團永不存 `pos`、永不回 `p`。
5. 快照不含自己、不含 > drop 者、不含 account_code／email／user_id；回應 struct 無任何歷史欄位。
6. 1.5 s 地板→429；body 512 B→413；座標範圍→400；`pv` 錯→426；`consent_v` 缺→400；非法 uuid→404。
7. 名額：51 個 uid 併發 start 只有 50 個成功（Lua 原子）；過期 grant 不佔名額。
8. `sid_mismatch`；`leave` 帶錯 sid 不刪；`leave` 後再 start 正常（無墓碑）。
9. `revoked_recent`：Revoke 的時間 > checkedAtMs → start 被拒。
10. kill key：SET 後下一次 `/pos` → 410 `killed`；設定回呼寫入/清除 kill。
11. 名稱消毒單元測試。Redis 清空 → 409 → 重新 start 成功。
12. 路由順序測試：RequireAuth 先於 RateLimit（dim 得到 `u<uid>`）。
13. `go vet` ＋ `go test ./internal/runmeet/... ./internal/middleware/...` 全綠（Lua 用 miniredis v2，加入 go.mod 僅測試依賴；本機無 Docker）。被 App Control 擋時 `-c` 編到 scratchpad 再跑（換 -ldflags 重編直到可執行）。

**P2 /track 團練模式**（web；page.tsx＋新 hook＋Leaflet＋api.ts＋RaceFocusMode＋FocusModeTip）
1. `?meet=` 嚴格 UUID；目標 `none`；所有 HUD 與無 `?meet` 時逐項一致。
2. 同步中斷（離線、503、被踢）時跑步與 gps 上傳不受影響；`finish()` 後 `leave` 送出。
3. reload 自動接續後同步恢復（`?meet=` 在 `dor_gps_active.href` 內保留）；>2 h 三選一不受影響。
4. 離開 tracking 後無殘留 timer／請求（Playwright 斷言請求數停止）。
5. hidden→visible ≤ 1.5 s 補送；start 首次延遲 0–2 s；退避抖動。
6. 隱私區與暫停分享：起點 200 m 內不帶 `p`；暫停時不帶 `p`、UI 說明看不到他人。
7. 專注模式：狀態行與最近 3 位可見；預設地圖團練模式透出上方地圖；鎖頭長按 1.5 s 行為不變；錯誤訊息 ≥ 10 s。
8. Leaflet 橘點＋DOM tooltip（XSS 測試：名稱 `<img src=x onerror=…>` 只顯示文字）。
9. 不引入每秒以上 re-render（`meetPeersRef` 為 ref）。
10. 三套 skin 的 Props 新增 `meetPeersRef?`（型別宣告＋傳入，P3 才實作繪製）。
11. `tsc` ＋ `next build` 通過；Playwright（DPR 3，`page.route` 模擬 `/live/start`、`/pos` 各種回應與多位 peers、`context.setGeolocation` 移動）。

**P3 三套 skin 渲染**：§7 全部；DPR 3 下亮點座標與 `map.project` 誤差 ≤ 2 px；50 個注入亮點每幀 < 3 ms（p95）、無連續幀失敗、不退回 Leaflet；截圖量像素驗證自己綠／他人橘與路線橘可區分；retro CJK 無豆腐塊；圖塊失敗退回 Leaflet 後亮點仍在。

**P4 入口與收尾**：按鈕矩陣（owner/joined/pending/kicked/left/非成員 × 時窗前/中/後 × cancelled × presence_only）`verify-run-meet.mjs` 通過；同意視窗在 PC 手機框內位置正確；開關 hidden 時完全看不到入口；設定登記；/privacy 文案；Playwright 完整流程（建團→加入→時窗內開跑→兩個 context 互見→結束上傳 gps_run 正常）需本機 Docker 後端（請擁有者啟動 Docker Desktop）。

**最終真機驗收（擁有者）**：兩支手機、兩個白名單帳號（同帳號只能一台登入）；iPhone Chrome/Safari＋Android Chrome：互見、關螢幕→陳舊→回前景恢復、被踢→停止、結束→亮點消失時間。

## 10. 實作後定案（2026-10-02；與上文不同處以本節為準）

**後端**
- `runmeet_live_entry_state=hidden` 對**所有人含超管**生效（kill 旗標在 Lua 對任何人一視同仁）；`locked`＝不顯示按鈕、超管仍可用；`whitelist`＝白名單＋超管；`open`＝全部成員。
- `/pos` 檢查順序：先查 grant（存在即信任）→ grant 不在才看墓碑 `rv:<uid>`（→ 403 revoked，否則 409 grant_missing）→ sid → kill → dead → 1.5 s 地板。`start` 成功會 DEL 墓碑（被踢後重新加入者不被誤判）；墓碑時間 ≥ checkedAtMs 即拒絕。名額檢查先於分配 `n`。Revoke 為 Lua。
- 另有 `ClearDead`：cancelled→open、後台取消下架（且未中止／未刪除）時清 `meta.dead`。
- 名冊版本 `rv`＝`meta.rv` 計數器（新成員或改名時 `HINCRBY`；舊 key 無 `meta.rv` 時退回 `HLEN names`）。
- 額外錯誤碼：400 `bad_request`／`bad_sid`／`bad_position`／`consent_required`、413 `payload_too_large`、404 `not_found`、401 `unauthorized`、500 `server_error`；409 `outside_window` 另帶 `opens_at`／`closes_at`。`p` 出現時 `la/ln/ac/fa` 四欄必齊。`runmeet_live_max` 上限 200。presence-only 回應仍帶名冊（只有名字）。
- 同意稽核：`audit_logs` 同一 user＋meet 15 分鐘內只寫一列（`INSERT … WHERE NOT EXISTS`）；寫在 Lua start 之前（沒有同意證據就不分享）。
- Redis 快速失敗（`internal/cache/bounded.go`：`rdb.WithTimeout(d)`＋context 期限，兩者缺一不可——go-redis v9 的 socket 讀寫不看 context 期限）：`middleware.RateLimit` 1 s 後 fail-open（下游拿到原始 context）、`live.Store` 全部方法 2 s → 503 `redis_unavailable`、`auth.checkDenylist` 真 300 ms（政策不變：會員 fail-open、admin fail-closed）。實測 Redis 停止／暫停：`/pos` 3.3 s、`/live/start` 4.3 s 回 503（修正前 30 s／500）。
- 撤銷掛鉤清單與「commit 之後才呼叫」由 `live_hooks_test.go` 的 AST 稽核強制；新增 `run_meet_members`／`run_meets` 寫入點必須在該表分類。

**前端**
- 同意證據（缺一即不啟動同步、不送任何請求，跑步當一般自由跑，畫面提示＋「前往團練頁」`/?runmeet=<id>`）：(a) localStorage `dor_meet_consent_v1:<id>`（勾「這個團練不再提醒」）；(b) sessionStorage `dor_meet_consent_ok:<id>`（團練頁確認當下寫入，≤12 h，**一次性**：引擎採用後即移除，「再跑一次」需重新從團練頁進入）；(c) 本趟紀錄 `dor_meet_live_run` 的 `consent:true`（重載／被系統回收／ActiveRunGuard 導回同一趟時沿用）。
- 本趟紀錄（裝置本機 localStorage `dor_meet_live_run`：meetId、開跑時間、consent、錨點＋錨點精度、暫停旗標）**只在跑步真正結束時清除**（離開頁面時若 `dor_gps_active` 仍是這一趟則保留）；presence-only 不存錨點。
- 隱私區定案：錨點＝開跑當下定位（年齡 ≤60 s、精度 ≤150 m；否則第一個精度 ≤150 m 的定位），設定後 30 秒內且尚未分享過可被精度更好的定位校正；半徑＝`200 + anchorAcc`（anchorAcc 0–300）；「起點在集合點附近」需確定：`dist(anchor, 集合點) + anchorAcc ≤ 300` 才免隱私區；離開半徑需連續 2 個不同定位才開始分享、回到半徑內立即停止。
- 錯誤處理：400／413／404／403／410／426／409 `sid_mismatch`／`outside_window`／429 `live_full` 為終止（停止同步、跑步繼續）；409 `grant_missing` → 靜默 start(reauth)；429 `too_fast`／一般限流依 `Retry-After`（無標頭走退避階梯）；5xx／網路走 5→10→20→30 s（±30 %）；所有 start 共用「60 秒 6 次」預算；回前景不打斷進行中的錯誤退避（除非真的切過背景）。
- 陳舊顯示 fade＝透明度 0.4（「60 % 透明」），Leaflet 與三套 skin 一致。scifi 夥伴色 `#FF6A20`（與規劃路線琥珀色區隔）、retro `#f8a000` 方塊黑框、cute／預設 `#FF8A3D`。
- 同步膠囊固定高度 48 px、單行省略（地圖區不因狀態變動而重新佈局）。

**已知限制（接受）**
- Redis 故障當下的踢人／取消：撤銷掛鉤 2 s 逾時失敗，Redis 恢復後被踢者的 grant 最多仍有效 15 分鐘（有界過期兜底；DB 為真，重新 start 會被擋）。同期間這些端點因 `realtime.Hub.Publish` 尚未加逾時而偏慢——同類未加逾時的 Redis 呼叫（realtime publish、auth Logout／Refresh、其他模組節流）列為後續。
- `/join` 既有規則：`ends_at` 為 NULL 且已過 `meet_at` 視為已結束，退出／被踢者在同步時窗內無法重新加入。
- RequireAuth 的 session 狀態每位使用者每分鐘查一次 DB：同步進行期間 Neon 不會睡（`/track/ping` 本來就每 30 秒寫一次）。

**實測（拋棄式堆疊：真 Redis 7.4.9＋PostgreSQL 16＋完整路由鏈，2026-10-02）**
- 功能 189 項全過；integration tag 測試 19 項全過；競態（踢人／退出 vs start）250 次 0 洩漏。
- 50 人穩態 3 分鐘：`/pos` 全 200，p50／p95／p99＝1.9／2.8／4.6 ms；回應 10／30／50 人＝337／903／1,475 B（gzip 216／457／661 B）；每請求 18 個 Redis 指令；`/pos`、`/leave` 零 SQL。
- 人數階梯 5／7／10 s 在 20→21、35→36 人切換；51 個併發 start＝50 成功＋1 `live_full`。

## 11. 尚未驗證（真機驗收時確認）
Cloudflare 對本網域的 per-IP 速率規則（50 人同出口 ≈ 600 req/min/IP）；Railway Redis 版本（Lua 僅用 ≥6.0 指令規避）；Next 代理對連續 POST 的延遲；真機耗電；retro CJK 字型。
