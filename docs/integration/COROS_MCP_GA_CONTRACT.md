# COROS MCP 全員開放（GA）修正契約 v1 — 2026-10-03

依據：COROS 書面核准（2026-10-03，見 memory coros-mcp-written-approval）＋ 開放前合規稽核（scratchpad garmin/COROS_AUDIT_FINDINGS.txt，5 個稽核員＋對抗式驗證）。
已核對：COROS 會發 refresh token（access token 約 30 天），憑證以既有機制加密保存。

## 0. 結論與原則
- COROS 條件 2（不向他人揭露原始資料）：**目前無違規**——他人只看得到衍生結果（總里程／完賽／名次）。條件 3：自動同步每人 25 分鐘 ✅；手動匯入／讀取測試 60 秒節流過密 ⚠️（本契約修）。
- 本契約的「共用模組」（§2）設計成與供應商無關，Garmin 直連直接沿用，不得各寫一份。
- 需要 migration **198**（§6），擁有者先套用才可推送讀新欄位的程式。

## 1. 擁有者決策（2026-10-03，採建議預設；擁有者可推翻）
| 題 | 決定 |
|---|---|
| 公開排行榜（含未登入訪客）顯示含 COROS 的總里程 | 維持現狀；由擁有者寄信請 COROS 確認（草稿：Downloads\COROS_application\COROS_followup_email.md） |
| COROS 數據用於 GPS 距離校正 | COROS 回覆前**先排除** `external_id LIKE 'mcp:%'` |
| 同一趟 Strava 先到、COROS 後到 | **直連手錶優先**：Strava 那筆改標重複，COROS 那筆計入 |
| 讀取測試（probe） | 只留超管、不存原始回應 |
| 開放方式 | 修完後由擁有者在後台改「全部開放」；保留「緊急關閉」 |

## 2. 共用模組（供應商無關；Garmin 沿用）
1. **入口狀態**：新套件 `internal/integration/entrygate`（或 integration 內共用函式）`Resolve(state, whitelist, email, code, isSuper) string`，狀態 `hidden|whitelist|open`（缺鍵＝whitelist；`hidden`＝緊急關閉，**含超管**；其他狀態超管恆可）。白名單格式同既有（換行／逗號、email 或帳號編碼）。COROS 用 `coros_mcp_entry_state` ＋既有 `coros_mcp_whitelist`；Garmin 之後用 `garmin_entry_state`＋`garmin_whitelist`。後端 appsettings specs 登記（含驗證），前端 `lib/appSettings.ts` 新增群組「COROS 直連」（白名單務必 `type:'text'`）。**Dashboard 入口與 API 閘門必須呼叫同一個判斷函式**。
2. **閘門範圍**：connect／callback／import／自動同步受入口限制；**status 與 disconnect 不受限制**（只要有連線列就能看、能中斷）。
3. **需要重新授權（reauth）**：`user_integrations.reauth_required_at TIMESTAMPTZ NULL`（migration 198）。refresh 失敗（invalid_grant／401 後 refresh 失敗／無 refresh token 且已過期）時寫入；成功換 token 時清空。`/status` 回 `expires_at`、`needs_reauth`。
4. **非破壞性重新授權**：已連接時也可走 `/connect`；callback 對既有列只更新 token／expires_at／scope／issuer／清 reauth_required_at，**保留 created_at（匯入 floor）**；重新授權成功後立即補同步，起點 `max(floor, last_synced_at − 1 天)`，上限 30 天。
5. **外部活動合理性檢查**（ImportActivity 前、所有外部來源共用）：開始時間晚於現在＋10 分鐘→略過；距離 < 0.1 km 或時間 < 60 s→略過；跑步類平均配速快於 2:30/km（或步行類快於 4:00/km）→寫入但 `flagged=TRUE, flag_reason='implausible_pace'`（不計賽事、不發獎勵、不加 total_km）；單筆 > 120 km→同樣 flagged。數值放常數並有單元測試。
6. **供應商帳號綁定**：`provider_user_id` 非空時，`UNIQUE (provider, provider_user_id) WHERE provider_user_id <> ''`（migration 198 部分唯一索引）。COROS：callback 取 OIDC `id_token` 的 `sub`（驗簽或至少驗 iss／aud／exp；若 COROS 不發 id_token，改用 MCP `queryUserInfo` 取穩定 id，擇一並寫明）。已被另一個 DOR 帳號綁定→拒絕連接（`/?coros_mcp=already_linked`，前台顯示「此 COROS 帳號已連結到另一個 DOR 帳號」）。
7. **去重偏好「直連手錶優先於 Strava」**（profile/dedup.go 與 import 的 detectDuplicate 兩處一致）：新外部列與既有 Strava 列重疊時，Strava 列改標 `cross_source_duplicate`（dup_of＝新列），新列保持計入；reResolveUser 與 worker 的裁決規則同步。附單元測試。
8. **detectDuplicate 的 App GPS 時間基準修正**：`source IS NULL` 候選列的 recorded_at 是**結束時間**，重疊判斷改以 `recorded_at − duration` 為起點（比照 mileage_exp.go 的 CASE）；補「GPS 晚停」與「GPS 後 N 分鐘的獨立跑步」兩個測試。
9. **每日報告「直連手錶」段**（純 DB、不逐人呼叫外部 API）：每供應商的連線數、24 h 內有成功同步數、需要重新授權數、`last_synced_at` 超過 3 天者數；COROS 改以 `source='coros' AND external_id LIKE 'mcp:%'` 參與靜默中斷偵測。

## 3. COROS 專屬修正
1. **節流統一**（Redis，跨副本）：手動匯入每人 ≥ 5 分鐘、自動同步 25 分鐘（既有）、兩者共用 in-flight 鎖 `coros_mcp:sync:<uid>`（SET NX EX 120）；`last_synced_at` 做第二道（Redis 清空也不會連打）；手動匯入回看 3 天（首次連接 30 天）；Redis 失敗退記憶體且**不 fail-open**。自動同步加全域並發上限 5（滿了略過、不扣名額）＋觸發前隨機延遲 0–30 s。COROS 回 429／5xx：該使用者冷卻 30 分鐘。設定鍵 `coros_mcp_autosync_enabled`（預設 1；0＝停自動同步，手動仍可）。
2. **讀取測試**：路由與按鈕只給超管；`coros_mcp_probe_logs` 只存摘要（step／ok／count／error 代碼），**不存原始回應**；Disconnect 同交易刪除該使用者 probe logs；前台不顯示 COROS 原始錯誤字串。
3. **Disconnect**：不受入口狀態限制；刪 `mcp:` 活動＋連線列＋probe logs＋重設偏好來源與該使用者 GPS 校正（若有）；確認視窗寫明「會刪除已匯入的 COROS 紀錄（賽事成績會重算；已發獎勵不收回）」並在完成後顯示刪除筆數。
4. **GPS 校正**：gpscalib 候選排除 `external_id LIKE 'mcp:%'`。
5. **競賽分組成績**：MCP 匯入有新增且未標記的列時，對該使用者報名的 competition 賽事執行既有的分組聚合重算（比照 virtualrunner 的 recompute），不新增排程。
6. **Terra 並存**：`TERRA_PROVIDERS` 預設移除 coros（停止新的 Terra-COROS；既有連線保留）；前台 connectedSources 認得 COROS 直連（`coros_mcp` 連線＝coros 來源），偏好來源可選 COROS；/track 的暫緩上傳判斷也認得。
7. **文案**：卡片拿掉「測試版」；刪除「測試期間不會寫入跑步紀錄」錯誤訊息；同意文案補：自動同步（開啟 DOR 時、最短約 25 分鐘）、距離／完賽／名次會顯示在你參加的賽事排行榜、不會把心率與路線給其他人、需要重新授權時的說明；隱私權政策新增 #coros 段（與 Garmin 一起，文字見 scratchpad garmin/POLICY_DRAFT.md，擁有者確認後上線）。

## 4. 每日報告可靠度（擁有者 2026-10-03 項目 4）
調查結論：10/02 08:09:33 有認領（`ops_daily_report_last_date`＝2026-10-02），之後無錯誤或發送失敗紀錄＝應已送出；**標題日期是「統計的昨日」**（10/02 送的是 2026-10-01）。仍修以下脆弱點：
1. **成功才標記**：先佔 advisory lock 與 in-memory 標記，Telegram 回 2xx 後才寫持久標記；產生或發送失敗→不寫持久標記、釋放 in-memory，窗口內下一輪重試。
2. **窗口內密集 tick**：排程每分鐘做「只看時間、不碰 DB」的檢查；只有在 08:00–08:59 且今天尚未成功時才碰 DB（每天最多幾次）→ 08:00 準時、失敗 10 分鐘後重試（最多 5 次）。
3. **成功留 log**：`ops dailyreport: sent`（報告日、字元數、耗時）；失敗訊息帶階段（build／send）。
4. 標題改「DOR 每日營運報告｜2026-10-02（昨日統計）」之類的明確字樣（擁有者不再誤解）。
5. 自檢（selfcheck）同樣套用 1–3。

## 5. 驗收
- Go：`go build ./...`、`go vet ./...`、相關套件 `go test` 全綠；新增測試涵蓋 §2 1–8、§3 1–5、§4 1–3。
- Neon 臨時分支：migration 198 可重複執行；部分唯一索引對既有資料（provider_user_id 多為空字串）不衝突；去重與 gpscalib SQL 實跑。
- 前端：tsc、next build；Playwright（DPR 3）：入口狀態四種組合、重新授權鈕、需要重新授權的橫幅、中斷確認與筆數、已綁定他帳號的錯誤訊息、文案無「測試版」。
- 不改 track/**、runmeet/**、團練同步跑相關檔案。

## 6. Migration 198（擁有者先套用）
```sql
ALTER TABLE user_integrations ADD COLUMN IF NOT EXISTS reauth_required_at TIMESTAMPTZ NULL;
CREATE UNIQUE INDEX IF NOT EXISTS user_integrations_provider_account_uniq
  ON user_integrations (provider, provider_user_id) WHERE provider_user_id <> '';
-- 預設設定（缺鍵時程式也有預設，這裡只為後台可見）
INSERT INTO app_settings (key, value, updated_at) VALUES
  ('coros_mcp_entry_state','whitelist',NOW()), ('coros_mcp_autosync_enabled','1',NOW())
  ON CONFLICT (key) DO NOTHING;
INSERT INTO schema_migrations (version) VALUES ('198') ON CONFLICT DO NOTHING;
```
⚠️ 套用前先唯讀確認：`SELECT provider, provider_user_id, count(*) FROM user_integrations WHERE provider_user_id <> '' GROUP BY 1,2 HAVING count(*) > 1` 為空（否則唯一索引建立失敗）。Garmin 需要的欄位若有，併入 198 一次套用。
