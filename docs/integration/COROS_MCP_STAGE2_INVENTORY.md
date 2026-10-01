# COROS MCP 第二階段：現有匯入流程盤點＋初步設計筆記（2026-10-01）

擁有者決定（2026-10-01）：現在開始第二階段（匯入、比對重複、自動同步、型號標示、安全措施），仍只開放擁有者帳號；
同步方式＝**自動（使用者打開 DOR 時，每人最多每 20–30 分鐘一次）＋手動「匯入數據」按鈕**。
第一階段契約與實測：docs/integration/COROS_MCP_STAGE1_CONTRACT.md（v868–v870）。

## A. COROS MCP 真實資料格式（v870 讀取測試，擁有者帳號）
- querySportRecords（參數見 STAGE1 契約）回傳「JSON 字串字面值」包人看的文字，每筆：
  `N. Outdoor Run — 2026-09-29` / `Location:` / `Start Coordinates: lat, lon` /
  `Time Window: startTimestamp=1790630201 | endTimestamp=1790632634` / `Duration: 40:33 | Distance: 5.31 km` /
  `Average Pace: 7:38 /km | Avg HR: 133 bpm | Calories: 334 kcal` / `LabelId: 480669290658299907 | SportType: 100`
  （labelId 17–18 位數字字串；Distance 兩位小數 km；Duration 可能是 mm:ss 或 h:mm:ss）。14 天 7 筆。
- getActivityDetail：文字，含 Workout Time、Total Time、Moving Average Pace、Elevation Gain / Loss、Average Heart Rate、
  Average Cadence…（沒有裝置型號）。
- queryActivityLapData：JSON，`{"source","labelId","sportType","mode","subMode","columns":[{name,label}...],...}`（每公里分段）。
- queryDevices：文字「Bound Devices (1) … 1. COROS PACE 4 / Model Name: COROS R4 …」→ 型號標示來源（活動本身不含型號）。
- **隱私**：回應含 Location 與起點座標——第二階段**不保存**這兩項（用不到）。

## B. 現有流程盤點（Explore 代理，file:line；A=services/api、N=A/internal、I=N/integration、M=A/migrations、W=services/worker/main.go、F=apps/web/src）

**1. activities 表**（source 自由文字無 CHECK；唯一鍵只有 (source, external_id)）
- M/001:63-76 distance_km DECIMAL(6,3)、duration_s INT（移動秒）、avg_pace_s、recorded_at、processed、created_at（只是寫入時間，worker 掃描用，不是連線 floor）
- M/014:22-26 ascent_m、avg_hr、source VARCHAR(20)、external_id VARCHAR(64)；M/016:7 UNIQUE (source, external_id)
- M/015:8-12 fingerprint、flagged、flag_reason、dup_of；M/047:3 km_paces INT[]（GPS worker 寫、ImportActivity 不寫；空時退回平均配速 N/race/progress.go:267）
- M/108:11 exp_awarded；M/154:3-11 raw_distance_km、calib_factor（外部為 NULL）、ext_manual、elapsed_s（只 Strava）
- 時間：只有 recorded_at＋duration_s；**外部來源 recorded_at 存開始時間**、App GPS 存結束時間（I/repository.go:540、mileage_exp.go:211-217）
- 沒有：裝置型號欄、外部 polyline、raw payload → 「COROS PACE 4」需要新欄位（migration）

**2. NormalizedActivity / ImportActivity**（I/repository.go）
- :372-392 {UserID, Source, ExternalID, Fingerprint, DistanceKm, DurationS, AvgPaceS, AscentM*, AvgHR*, RecordedAt(=開始), Manual, ElapsedS*}；沒有 KmPaces／裝置／結束時間
- :455-491 detectDuplicate → FindRegisteredRace → `INSERT … ON CONFLICT (source, external_id) DO NOTHING RETURNING id`
- 回傳 ImportResult{Status: exists|duplicate|inserted, Reason, ID}；duplicate 會寫入但 flagged
- detectDuplicate :423-452：(a) fingerprint（sha256 of startUnix|meters|sec）同指紋 → cross_account_duplicate／duplicate；(b) 同使用者時間重疊 → multi_device_duplicate（先到先贏、不看來源）
- **floor 不在 ImportActivity**：各 importer 自己略過 start < 連線 created_at（strava.go:641-644、terra.go:1266-1270、coros.go:346-351）；Save() 重新授權會重設 created_at

**3. 匯入後連鎖效果**——沒有共用 hook，三段尾巴各自複製：strava.go:680-697、coros.go:371-385、terra.go:1309-1322
- `gpscalib.RecomputeAsync`（inserted 或 duplicate）→ `stamina.ChargeSP`（只 inserted）→ `repo.AwardMileageExp(res.ID,uid)`（inserted 或 multi_device_duplicate，km>0）
- AwardMileageExp（mileage_exp.go:147-346）：advisory lock、exp_awarded 冪等、external_award_ledger（sha256(source:external_id)）、與已發獎重疊只補正差額、更新 users total_km/exp/dp、referral.Reward、mileage_exp_events
- 其餘都是讀取時 SQL 現算（race progress/leaderboard、挑戰、稱號），條件 NOT flagged＋外部資料閘門
- 個人任務只算 source NULL 或 'strava'（personaltask.go:653-660）；worker 的 recomputeStandings 只在啟動／GPS 事件時跑（外部來源使用者的分組排名會延遲）

**4. Terra 的 COROS 怎麼存＋來源字串清單**
- Terra：source='coros'，external_id＝Terra summary_id（退回 "coros:"+unix），連線列 provider='coros', via='terra'；Partner adapter 也是 source='coros'、external_id=labelId
- 要一起動的清單：N/profile/dedup.go:19-22 validSources（偏好來源驗證要求 user_integrations.provider==source，:98-110）；排名表 dedup.go:52-61 與 W:412-418（已漂移）；N/gpscalib/service.go:269 來源清單；外部資料閘門 `a.source IS NULL OR (rc.external_data AND a.source <> 'strava')`（~20 處，非 strava 自動算）；dailyreport.go:530-535（已排除 coros_mcp）；前台 F/lib/api.ts:1893-1899、ProfileScreen.tsx:51-53/:83-87/:166/:775-779、AchievementScreen.tsx:22-25、RaceDetailScreen.tsx:608-616、admin/gps-calib/page.tsx:81

**5. 跨來源去重**：匯入當下＝規則 2（先到先贏）；worker W:371-445 resolveCrossSourceDups（45 天內；GPS 0 > 偏好來源 1 > garmin 2 > coros 3 > strava 4 > 其他 5）；N/profile/dedup.go reResolveUser 立即重算（較完整排名表）

**6. DeleteProviderActivities**（I/repository.go:349-369）：`DELETE FROM activities WHERE user_id=$1 AND source=$2`——**只看來源字串**，刪 'coros' 會連 Terra／Partner 的 COROS 紀錄一起刪；不回收 EXP/km（ledger 防重發）；目前 MCP 中斷連線只刪連線列

**7. Terra 手動匯入**：POST /integrations/terra/import?provider=&days=（預設 30、上限 30——worker 去重只看 45 天）；記憶體 60 秒節流；回應 TerraImportResult；前台按鈕 ProfileScreen.tsx:1114 → importTerra :716-746；Redis allowRate 範例 strava.go:61-73、coros.go:94-106；**沒有現成的「打開 App 時同步」**（最近的模式：membership.go Dashboard lazy work、Terra /status 10 分鐘快取＋3 秒逾時）；注意 Neon 睡眠

**8. 前台活動列表**：GET /integrations/strava/activities（名稱是歷史遺留，其實回全部來源）→ ListActivities → ActivityRow（無裝置欄）；ProfileScreen「已同步活動」:1256-1316（徽章 :1284、配速行 :1296、「Powered by Strava」:1415-1419）→ 型號標示放每列徽章旁＋條件式頁尾

**9. 必須遵守**：一律走 ImportActivity＋相同的三段尾巴；略過 start < ConnectedAt；只收跑步／走路／健行；只硬擋 distance<=0 或 duration<=0；AvgPaceS＝round(duration/distance)（不用 provider 配速）；fingerprintOf(startUnix, meters, sec)；recorded_at 存 UTC 開始時間；回補天數 ≤30；labelId 要穩定（ledger 鍵＝source+external_id）；MCP 兩位小數 km 與 Terra 精確公尺重疊時可能補正幾公尺差額；個人任務不算非 strava/GPS 來源；/track 自動上傳暫停邏輯不認得 MCP（WEARABLE_CUTOVER_PLAN.md:61）

## C. 初步設計傾向（下次開工先定案，再寫第二階段契約）
1. **來源字串**：傾向 `source='coros'`＋`external_id='mcp:<labelId>'`（沿用所有 'coros' 清單：排名、gpscalib、COROS 標籤、外部資料閘門），
   中斷連線改成只刪 `source='coros' AND external_id LIKE 'mcp:%'`（新 repo 函式，不用既有 DeleteProviderActivities）；偏好來源驗證放寬
   「provider 為 coros 或 coros_mcp」。替代方案 `source='coros_mcp'` 要改的清單太多（見 B4）。
2. **型號**：migration 197 新增 `activities.device_name VARCHAR(40)`（擁有者手動套用，DB 先於程式）；匯入時用 queryDevices 的第一支裝置
   （或活動資料若有型號優先），ListActivities／前台列顯示「Data provided by COROS · COROS PACE 4」。
3. **同步**：POST /coros-mcp/import?days=1..30（記憶體 60 秒節流，比照 Terra）＋ Dashboard 載入時非同步觸發（Redis 每人 25 分鐘一次、
   只對有 coros_mcp 連線的人、goroutine＋逾時，不擋 Dashboard 回應；**不新增排程**）；增量以 last_synced_at 起算、最多回補 30 天、
   floor＝連線 created_at。
4. **匯入**：解析 querySportRecords 文字（labelId、sportType、start/end timestamp、Duration、Distance、Avg HR）→ NormalizedActivity
   （DurationS 用 Duration、ElapsedS＝end−start、AscentM 視需要再呼叫 getActivityDetail）→ ImportActivity → 三段尾巴；
   km_paces 由 queryActivityLapData（第二步可做：擴充 NormalizedActivity.KmPaces，其他 importer 傳 nil）。
5. **安全**（開放前必做）：/connect 種短效 httpOnly cookie（隨機 nonce 簽進 state），/callback 比對；state HMAC 金鑰用途隔離。
6. **不做**：保存 Location／座標、FIT／軌跡、排行榜政策變更（等 COROS 書面回覆）、切斷 Terra（另行決定）。
