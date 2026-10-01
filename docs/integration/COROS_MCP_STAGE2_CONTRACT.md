# COROS MCP 串接 第二階段契約：匯入、去重、自動同步、型號標示、安全措施（v871，仍只開放白名單）

擁有者決定（2026-10-01）：現在做第二階段，仍只開放擁有者；同步＝**自動（使用者打開 DOR 時，每人最多每 25 分鐘一次）＋手動「匯入數據」按鈕**。
依據：docs/integration/COROS_MCP_STAGE2_INVENTORY.md（真實資料格式 A 節、現有流程 file:line B 節、設計傾向 C 節）、
COROS_MCP_STAGE1_CONTRACT.md（v868–v870 實測）。

## 0. 不變的原則
- 仍只開放 `coros_mcp_whitelist`；**不新增任何排程／背景迴圈**（Neon 要能睡），同步只由使用者動作觸發。
- 一律走既有 `Repository.ImportActivity`＋與 terra.go:1308-1322 **完全相同條件**的三段尾巴（gpscalib.RecomputeAsync／
  stamina.ChargeSP／AwardMileageExp）；不 raw INSERT、不改既有 importer 行為。
- **不保存** COROS 回傳的 Location 與起點座標、不下載 FIT、不改排行榜政策、不動 Terra 連線。

## 1. 資料怎麼存（migration 197，擁有者手動套用，DB 先於程式）
- `activities.device_name VARCHAR(60)`（NULL＝未知；只有 COROS MCP 會寫）。
- `user_integrations.last_synced_at TIMESTAMPTZ`（MCP 連線最後一次成功同步時間）。
- 皆 `ADD COLUMN IF NOT EXISTS`、可重複套用；`schema_migrations` 記 '197'。
- MCP 匯入的活動：`source='coros'`、`external_id='mcp:'+labelId`（labelId 原樣字串，17–18 位數）——沿用所有 'coros' 清單
  （去重排名、gpscalib、COROS 標籤、外部資料閘門）；`mcp:` 前綴與 Terra（summary_id）、Partner（純 labelId）不撞鍵。
- NormalizedActivity 新增 `DeviceName *string`，ImportActivity 寫入 device_name（其他 importer 傳 nil，行為不變）。

## 2. 解析 querySportRecords（純函式 + 單元測試，用 A 節真實格式）
每筆（以編號「N. 」分段）取：`startTimestamp`、`endTimestamp`（unix 秒）、`Duration`（mm:ss 或 h:mm:ss）、`Distance`（km，可能
是 `m`；非距離型如 sets 一律略過）、`Avg HR`（bpm，可缺）、`LabelId`、`SportType`。標題行的運動名稱／日期只做除錯。
- 只收 sportType ∈ {100,101,102,103,104,900}；其他記 skipped_non_running。
- distance<=0 或 duration<=0 → skipped_invalid；start < 連線 floor（user_integrations.created_at，coros_mcp 那一列）→ skipped_before_connect。
- 對應：RecordedAt=start（UTC）、DurationS=Duration、ElapsedS=end−start（>0 才給）、DistanceKm、AvgPaceS=round(DurationS/DistanceKm)、
  AvgHR、Fingerprint=既有 fingerprintOf(startUnix, meters, sec)、Source='coros'、ExternalID='mcp:'+labelId、DeviceName。
- 文字外層引號、anomaly 判斷沿用 v870 的 corosMcpUnwrapText／corosMcpIsAnomaly（anomaly → 該次同步失敗、不寫任何東西）。

## 3. 型號
- 每次同步先呼叫 queryDevices 一次，解析清單第一支裝置名稱（例「COROS PACE 4」，即「1. 」後那行文字，去頭尾空白、最長 60 字）；
  解析不到就 NULL。多支裝置時取第一支（活動本身不含型號，記於註解）。

## 4. 同步
- 共用核心 `syncCorosMcp(ctx, userID, from, to) (CorosMcpSyncResult, error)`：取連線（無→not_connected）、必要時換 token
  （errCorosMcpReconnect 照舊）、queryDevices、querySportRecords（日期 yyyyMMdd；以 ≤10 天為一段分段查，limit 50；回傳筆數
  ＝50 時記 warn）、逐筆對應→ImportActivity→三段尾巴、最後更新 last_synced_at。結果
  `{fetched, imported, duplicate, exists, skipped_before_connect, skipped_non_running, skipped_invalid, errors, device_name}`。
- **手動**：`POST /api/v1/integrations/coros-mcp/import?days=N`（1–30，預設 30；from＝max(now−N天, floor)）；白名單＋已連接；
  每人 60 秒節流（記憶體，比照 terra allowImport）；409 not_connected／reconnect_required、429 rate_limited、502 COROS 失敗。
- **自動**：`profile` 的 Dashboard 處理中，若 `coros_mcp_entry==='shown'` 才呼叫 `CorosMcpAutoSync(userID)`：Redis `SET NX EX 1500`
  鍵 `coros_mcp:autosync:<uid>`（Redis nil 時退回記憶體 map）搶到才在 goroutine 背景跑 `syncCorosMcp(now−3天, now)`（context
  逾時 60 秒、panic recover、失敗只記 log），**不阻塞、不改 Dashboard 回應**；未連接直接結束（查一次連線列）。
- Dashboard 的其他使用者（非白名單）零額外查詢。

## 5. 中斷連線
- 新 repo 函式 `DeleteCorosMcpActivities(user)`：與 DeleteProviderActivities 相同交易內清 dup_of／flag 的處理，但只刪
  `source='coros' AND external_id LIKE 'mcp:%'`（**不得**動 Terra／Partner 的 coros 列）；不回收 EXP（與既有一致，ledger 防重發）。
- 刪連線列；若使用者已沒有任何 provider='coros' 連線、且偏好來源是 'coros' → 重設 'gps'（沿用 ResetPreferredSource）。
- 偏好來源驗證（profile/dedup.go:98-110）：source='coros' 時，provider 為 'coros' **或 'coros_mcp'** 都算有連線。

## 6. 安全（開放前必做，本版完成）
- OAuth 登入 CSRF：/connect 回應同時種 cookie `dor_cmcp_n=<32 bytes 隨機 base64url>`（Path=`/api/v1/integrations/coros-mcp/callback`、
  HttpOnly、Secure、SameSite=Lax、Max-Age=600），nonce 一併簽進 state；/callback 必須帶同一 cookie 且常數時間比對相符才換 token，
  比對後清 cookie（Max-Age=0）。不符 → `/?coros_mcp=error&reason=state_mismatch`。前台同源呼叫（BASE='/api/v1'），cookie 自動帶。
- state HMAC 金鑰用途隔離：key＝HMAC-SHA256(JWT_SECRET, "dor/coros-mcp/state/v1")。

## 7. 前台（ProfileScreen「COROS 直連 · 測試版」卡＋「已同步活動」）
- 已連接時：顯示「上次同步：<時間或『尚未同步』>」（/status 新增 `last_synced_at`、`device_name`）、主按鈕「匯入數據」（POST /import，
  顯示結果：匯入 N 筆／重複 M 筆／略過 K 筆，或 409／429／502 友善訊息；匯入>0 時重新載入活動列表）；「讀取測試」改為次要按鈕保留。
- 同意說明改成：會匯入你連接之後的跑步／健行／走路紀錄（距離、時間、心率、手錶型號）計入 DOR 里程、賽事與獎勵；不保存地點與座標；
  中斷連線會刪除從 COROS 直連匯入的紀錄。
- 已同步活動列：ListActivities 回傳 `device_name`；有值時在該列加一行「Data provided by COROS · <device_name>」。
- 風格：沿用 CSS 變數；金底白字規則；五種 skin 可讀。

## 8. 驗收
- Go：parser 單元測試（真實格式範本去除地點座標、mm:ss／h:mm:ss、m／km、缺 HR、非跑步、anomaly、引號包裝）、對應（fingerprint、
  ElapsedS、floor、external_id）、型號解析、CSRF（缺 cookie／不符／過期／正確）、金鑰隔離、手動節流、自動同步 SET NX 只跑一次。
- Neon 暫時分支整合測試（假 COROS 一律 in-process transport）：connect（含 cookie）→callback；手動匯入寫入 source='coros'、
  external_id='mcp:…'、device_name；floor 前略過；預先種一筆重疊的 Terra coros 列 → MCP 那筆 multi_device_duplicate；
  AwardMileageExp 生效（exp_awarded、users.total_km 增加、重疊只補差額）；自動同步 25 分鐘內第二次不打 COROS；中斷只刪 mcp: 列、
  Terra 列保留、偏好來源處理正確。跑完刪分支並再列確認。
- 前台 Playwright（DPR3 390×844、mock API）：匯入按鈕與結果文字、409／429、上次同步、活動列型號標示、非白名單零差異、五種風格。
- go build／vet、tsc、next build 通過；0 console error。

## 實作與驗收記錄（2026-10-01）
- Neon 暫時分支（先套 197 兩次驗證可重複）整合測試 11/11 PASS；單元 102 PASS；前台 Playwright 18/18 PASS（DPR3、五種風格、
  錯誤訊息、型號標示、非白名單零請求）；後端對抗式審查 NO BLOCKING。
- 整合測試抓到 worker 留下的 2 個**共用程式**漏洞（已修）：ImportActivity INSERT 加了 device_name 參數卻沒加欄位／$17、
  ListActivities 掃描 device_name 卻沒 SELECT——會讓所有 Strava／Terra 匯入與每個人的活動列表壞掉。
- 審查 minor 中已修：Terra／Partner 中斷 COROS（DeleteProviderActivities 'coros'）不再連帶刪 MCP 的 'mcp:' 列；同步途中若連線被
  中斷即停手不寫孤兒列；自動同步起點改依 last_synced_at 回補（未同步過 30 天、同步過取 3 天與上次−1 天較早者，最多 30 天）；
  讀取測試紀錄存檔前遮掉 Location／座標行；state 無效時也清 nonce cookie。
- 未修（記錄）：自動同步與手動匯入同時換 token 的競態（窄、下次同步自癒）；mi 單位未支援（記 skipped_invalid）；
  名額先占才查連線（未連接者 25 分鐘查一次連線列）；Redis SET NX 路徑無單元測試（記憶體 fallback 有）。
