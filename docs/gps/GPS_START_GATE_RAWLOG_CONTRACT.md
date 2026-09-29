# GPS 開跑精度門檻＋原始定位點記錄（除錯用）契約

2026-09-29 使用者拍板（背景：當天實跑 App 5.44 km vs COROS 5.307 km，分析見對話紀錄；GPS 距離校正卡在 warming）：
1. **開跑前等 GPS 精度夠好**：精度 ≤ 20 m 才讓倒數結束；最多等 15 秒，超過就照常開始，不會卡住。**所有使用者**。
2. **保存原始定位點**：只對使用者本人帳號（白名單，預設 sogobaga@gmail.com），**30 天自動刪除**，只有後台超管看得到。
3. **不使用 Strava 資料做校正**（維持 Strava 一律排除的合規原則），改為優先查修 COROS 經 Terra 直連中斷。
4. 抖動門檻自適應**暫不改**：等收集幾趟原始資料、離線重播驗證後再議（避免影響其他會員成績與防弊）。

## A. 開跑精度門檻（apps/web/src/app/track/page.tsx，前端）
- 現行：按「開始跑步」→ `requestStart()` 在點擊當下同步做手勢相依動作（watchPosition、Wake Lock、音訊），3 秒倒數 → `start()`。**這些都不可改動**（iOS 手勢堆疊，見 memory gps-run-track-ux v851）。
- 新規則：倒數 3 秒照舊；倒數結束時，若「按下開始後收到的定位」中已有 `acc ≤ 20 m`（acc 0 視為未知→不算達標）→ 立即 `start()`；否則進入「GPS 定位穩定中…」等待狀態：
  - 顯示於同一個倒數疊層（z 3800，不可 ≥ 4000）：大字「GPS 定位穩定中」＋目前精度「±X m」（無定位時「搜尋訊號中」）＋細進度（剩餘秒數）＋兩個按鈕「直接開始」（立即 start）與「取消」（沿用既有取消行為）。
  - 任何一筆 `acc ≤ 20 m` 的定位到達 → 立即 `start()`。
  - 從按下開始起算滿 15 秒 → 照常 `start()`（不論精度）。
- 不影響：自動接續（≤2h）與三選一「繼續追蹤」流程（不經過門檻）；`start()` 內重置距離、專注模式自動進入、倒數取消等既有行為；距離／防弊演算法一律不動。
- 以 skin 樣式呈現：沿用倒數疊層既有的 skin 化（若倒數疊層已有 skin 分支就比照；沒有就用中性深色）。
- 常數：`START_GATE_ACC_M = 20`、`START_GATE_MAX_S = 15`，集中宣告並註解本契約。

## B. 原始定位點記錄（前後端＋migration 195）
### 資料表（migration `195_gps_run_raw_points.sql`，可重複執行，使用者手動套用）
```
CREATE TABLE IF NOT EXISTS gps_run_raw_points (
  run_id      UUID PRIMARY KEY REFERENCES gps_runs(id) ON DELETE CASCADE,
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  point_count INT  NOT NULL,
  points      JSONB NOT NULL,          -- {"v":1,"fields":[...],"rows":[[...],...]}
  client_version TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_gps_run_raw_points_created ON gps_run_raw_points(created_at);
INSERT INTO schema_migrations (version) VALUES ('195') ON CONFLICT DO NOTHING;
```
（先確認 gps_runs 主鍵型別與 users 表名，照實際 schema 寫。）docs/integration/GARMIN_DIRECT_SPEC.md 內原本預留的 migration 195 改為 196。

### 白名單／開關
- app_settings `gps_raw_log_whitelist`（逗號分隔 email，缺鍵＝`sogobaga@gmail.com`）；比照既有 whitelist 解析（小寫、trim）。無 super_admin 旁路（超管要看資料走後台端點，不代表自己的跑步要被記錄）。
- 會員 dashboard（profile/membership）回傳 `gps_raw_log: boolean`，前端據此決定是否收集與上傳。

### 前端收集
- 只在 `gps_raw_log=true` 時收集；`status==='tracking'` 期間每一筆 onPos 原始定位都記一列：
  `[t_ms(pos.timestamp), lat(6位小數), lng(6位小數), acc(1位), speed|null(2位), heading|null(0位), code]`，
  `code` 單字元：`a` 採納計入、`j` 未達 JITTER_MIN 略過、`p` 精度差（>MAX_ACC）、`x` 超速／斷訊排除、`h` 靜止暫存（尚未計入）、`d` 暫存後丟棄、`f` 起點。依 onPos 實際分支標記（讀碼確定每個分支對應的碼；做不到精準的分支標 `?` 並註解）。
- 緩衝只放記憶體（上限 30,000 列，超過就停止記錄並標記截斷）；**不可放進 GPS outbox／localStorage**（避免佔滿配額影響主要跑步資料上傳）。頁面重整／自動接續時緩衝遺失可接受（除錯用途），在接續後的上傳帶 `truncated/partial` 旗標。
- 跑步資料上傳成功、拿到 run id 後，**另外** fire-and-forget `POST /api/v1/me/gps-runs/{runId}/raw-points`（body `{"v":1,"fields":[...],"rows":[...],"truncated":bool,"client_version":...}`）；失敗不重試、不影響主流程、不顯示錯誤給使用者。離線排隊稍後才上傳的跑步：若緩衝仍在記憶體就一起送，否則放棄。

### 後端
- `POST /api/v1/me/gps-runs/{runId}/raw-points`：需登入；run 必須屬於本人；本人必須在白名單（否則 403 `raw_log_not_allowed`）；body 上限 3 MB、rows ≤ 30,000、欄位型別／範圍驗證（lat/lng 範圍、acc ≥ 0…）；同一 run 重送＝覆寫（ON CONFLICT (run_id) DO UPDATE）。
- `GET /api/v1/admin/gps-runs/{runId}/raw-points`：`RequireSuper`；回傳原始 JSON（附 run 基本資訊：distance_km、duration_s、km_paces、client_version）。後台既有 GPS 紀錄頁（/admin/gps-runs…）若方便，加一個「下載原始定位點（JSON）」連結，只有超管看得到。
- 保存期限：每日報告排程（ops RunDailyReportLoop，Neon-safe，不另開週期性 DB 查詢）順帶執行 `DELETE FROM gps_run_raw_points WHERE created_at < now() - interval '30 days'`，把刪除筆數附在日報的自檢段落（有刪才顯示）。
- 不回傳他人資料；log 不印座標。

## C. 驗收
- Go：白名單解析、403、ownership、欄位驗證、重送覆寫、admin RequireSuper、retention 刪除（單元＋Neon 暫存分支上套 195 後的整合測試，測完刪分支並二次確認）；`go build ./...`、`go vet`、`go test` 相關套件。
- 前端 E2E（DPR 3，390×844，模擬 geolocation）：
  1. 精度好（acc 8 m）→ 倒數 3 秒後立即開始；2. 精度差（acc 45 m）→ 顯示「GPS 定位穩定中 ±45 m」，第 7 秒來一筆 12 m → 立即開始；3. 一直 45 m → 第 15 秒自動開始；4. 等待中按「直接開始」→ 立即開始；5. 等待中按「取消」→ 回到開始前；6. 自動接續流程不經過門檻；7. 疊層 z < 4000、四種 skin（default/scifi/retro/cute）外觀可讀、0 console error。
  8. 白名單帳號：跑完上傳後送出一次 raw-points POST，列數與 onPos 呼叫數一致、code 分佈合理；非白名單：完全不送、dashboard `gps_raw_log=false`；raw 上傳失敗不影響主流程。
- `npx tsc --noEmit`、`npx next build`；非白名單使用者除「開跑精度門檻」外零行為改變。
