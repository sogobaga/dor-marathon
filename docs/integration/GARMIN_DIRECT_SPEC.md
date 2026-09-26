# Garmin Health API 直連 規格草案 v0.1 — 2026-09-26

狀態：**等 Garmin Enterprise access（Consumer Key／Secret）核發後開工**。本檔是實作契約的前身；標「⚠️待核對」者需在拿到 Garmin 開發者入口文件後逐條確認再定案。

## 0. 目標與範圍
- 用 DOR 自己的 Garmin Health API 授權，讓會員把 Garmin 手錶的跑步／走路活動直接同步到 DOR（不經 Terra）。
- 滿足 Garmin 政策：資料只交付給 DOR 自己的網域與伺服器；不代第三方整合；不分享給其他平台或 AI。
- 上線後：Terra 的 Garmin 通道停用（Terra 僅保留給 Polar／Suunto／Wahoo，之後視使用者數決定是否整體取消）；既有 11 位 Terra-Garmin 使用者引導重新授權。
- 不在範圍：Activity API（FIT 檔）、睡眠／壓力等健康資料、Apple Watch（另案原生 App）。

## 1. Garmin Health API 事實（依公開資訊整理，⚠️待核對）
| 項目 | 目前認知 | 待核對 |
|---|---|---|
| 授權 | OAuth 2.0 + PKCE（authorize `https://connect.garmin.com/oauth2Confirm`、token `https://diauth.garmin.com/di-oauth2-service/oauth/token`）；access token 短效、refresh token 長效（約 3 個月） | 端點、PKCE 是否必要、token 壽命、scope 名稱 |
| 使用者識別 | `GET https://apis.garmin.com/wellness-api/rest/user/id` → Garmin API user id（穩定、非 Garmin Connect 帳號 ID） | 路徑 |
| 資料交付 | **Push**（Garmin 主動 POST JSON 到我們在入口設定的 endpoint，每種 summary 各一個）或 Ping（通知後自取）。採 **Push** | Push payload 結構、是否含 `userId`、重送規則 |
| Summary 類型 | `activities`（活動摘要）、`activityDetails`（含逐點樣本：lat/lng/elevation/hr/speed）、`dailies`、`deregistrations`、`userPermissions` | 各欄位名稱；activityDetails 是否含在 Health API 授權內 |
| 活動摘要欄位 | `summaryId`、`activityId`、`activityType`（RUNNING／TRAIL_RUNNING／TREADMILL_RUNNING／WALKING／HIKING…）、`startTimeInSeconds`、`startTimeOffsetInSeconds`、`durationInSeconds`、`distanceInMeters`、`averageSpeedInMetersPerSecond`、`averageHeartRateInBeatsPerMinute`、`totalElevationGainInMeters`、`manual`、`deviceName` | 是否另有 `activeTimeInSeconds`（移動時間）；type 枚舉完整清單 |
| 回填 | `GET /wellness-api/rest/backfill/activities?summaryStartTimeInSeconds=&summaryEndTimeInSeconds=`，非同步、結果經 Push 送回；單次範圍上限（傳聞 30 天） | 上限、速率限制 |
| 解除授權 | 使用者在 Garmin 端撤銷 → Garmin 推 `deregistrations`；我們主動 `DELETE /wellness-api/rest/user/registration` | 路徑 |
| Webhook 安全 | Garmin Push **不簽章**；靠不可猜的 endpoint 路徑 token＋（可選）Garmin 來源 IP 清單 | 是否提供 IP 清單或簽章 |
| 環境 | 核准後先有 Evaluation 環境（少量使用者測試），簽約後 Production | 使用者數上限、切換方式 |

## 2. 架構決策（編排者定案，可挑戰）
1. **資料模型沿用 `user_integrations`**：`provider='garmin'`、`via='direct'`。與既有 Terra-Garmin 列（`via='terra'`）共用 `(user_id, provider)` 唯一鍵——**直連連上時覆蓋同一列**（`via`→`direct`、token 換成 Garmin token、`provider_user_id` 換成 Garmin API user id），**但保留 `created_at`（里程 floor）**，避免中間活動被 floor 擋掉；同時觸發一次回填（自該使用者最後一筆 garmin 活動時間或 30 天前，取較晚者）。
2. **Push 事件先落地再處理**（新表 `integration_events`，migration 193）：`id, provider, event_type, provider_user_id, payload JSONB, received_at, status pending|done|error|dead, attempts, last_error, processed_at`。Webhook handler：驗證路徑 token → 逐筆 INSERT → 立即 200；背景處理器（api 內佇列或 services/worker 排程，≤5 秒延遲）處理，失敗指數退避重試 5 次後 `dead`；後台可重放。**這是 COROS／Terra 都沒有的可靠度層**，Garmin 直連一起補上。
3. **PKCE 必做**：`code_verifier` 以 state 為鍵存 Redis（TTL 10 分鐘），state 仍用既有 HMAC 簽章模式（coros.go）。
4. **活動類型白名單**：跑步全類（RUNNING／STREET／TRACK／TRAIL／TREADMILL／VIRTUAL／INDOOR_RUNNING）＋走路類（WALKING／HIKING／INDOOR_WALKING），與 Terra 白名單語意一致；其餘（騎車／游泳／重訓）略過並計入 `skipped_non_running`。
5. **軌跡（Phase 2，可選）**：訂閱 `activityDetails`，把樣本編成 polyline 存入 `activities.polyline`（新欄位，`km_paces` 亦可由樣本算），讓 Garmin 活動也有「跑步紀錄畫面」（揮汗有禮截圖需求）。Phase 1 先不做，欄位預留在 migration 193 內一次加好。
6. **停用 Terra 的 Garmin 通道**：`TERRA_PROVIDERS` 環境變數（預設 `polar,suunto,wahoo`）控制 Terra 連接 UI 顯示的品牌；Terra webhook 收到 provider=garmin 的事件一律略過並 log（避免雙來源）。
7. **跨來源去重優先序**：直連 garmin 仍是外部來源第一順位；順手修 `services/worker/main.go` 的優先序表（缺 polar／suunto／wahoo，與 `profile/dedup.go` 漂移）。
8. **解除授權政策**：使用者在 DOR 斷開 → 呼叫 Garmin deregistration＋刪 token＋`ResetPreferredSource`＋刪除該來源活動（與 COROS 相同）；Garmin 端撤銷 → 收到 `deregistrations` 事件後做同樣處理並發站內信。⚠️待核對 Garmin 條款是否要求刪除已匯入資料。
9. **隱私政策**：上線前在 `/privacy` 第 3 節新增「Garmin」條目（直接串接、資料項目、跨境、撤銷方式），並移除 Terra 條目中的 Garmin 字樣。

## 3. 端點與流程
### 3.1 路由（`/api/v1/integrations/garmin`，掛法比照 coros.go）
| 方法 | 路徑 | 認證 | 說明 |
|---|---|---|---|
| GET | `/connect` | 會員 | 產生 state＋PKCE，302 到 Garmin 授權頁 |
| GET | `/callback` | 公開 | 驗 state → 換 token → 取 user id → `Save`（保留 floor）→ 觸發回填 → 302 回 `/?garmin=connected` |
| GET | `/status` | 會員 | `{connected, via, athlete_name, connected_at, last_data_at}`（`last_data_at`＝`integration_events` 該使用者最近 `received_at`，**不需打 Garmin**） |
| POST | `/disconnect` | 會員 | 見 §2-8 |
| POST | `/import?days=1..30` | 會員 | 手動回填（呼叫 Garmin backfill；每人 10 分鐘限一次） |
| POST | `/webhook/{token}/activities` | 路徑 token | 落地 `activities` 事件 |
| POST | `/webhook/{token}/activity-details` | 路徑 token | Phase 2 |
| POST | `/webhook/{token}/deregistrations` | 路徑 token | 解除授權 |
| POST | `/webhook/{token}/permissions` | 路徑 token | 權限變更（記錄＋若失去 activity 權限 → 提示使用者） |
| GET | `/health` | 公開 | 入口設定用 |
| GET/POST | `/admin/integrations/garmin/events…` | admin（scope integrations） | 事件列表、重放、dead 清單 |

### 3.2 匯入對照（`NormalizedActivity`）
| 欄位 | 來源 |
|---|---|
| Source | `garmin` |
| ExternalID | `summaryId`（字串） |
| DistanceKm | `distanceInMeters/1000`（3 位小數） |
| DurationS | `durationInSeconds`（⚠️若有 activeTime 則 DurationS=activeTime、ElapsedS=duration） |
| AvgPaceS | 自算 `DurationS / DistanceKm`（三來源口徑一致，不用 Garmin 的配速欄） |
| RecordedAt | `startTimeInSeconds + DurationS`（＝結束時刻，全站慣例） |
| AscentM | `totalElevationGainInMeters`（>0 才填） |
| AvgHR | `averageHeartRateInBeatsPerMinute` |
| Manual | `manual` |
| Fingerprint | 既有算法（跨帳號去重） |

接著：`ImportActivity` → `gpscalib.RecomputeAsync` → `stamina.ChargeSP`（inserted）→ `AwardMileageExp`（inserted 或 multi_device_duplicate）。floor＝`conn.ConnectedAt`（保留舊 floor）。

### 3.3 前端
- `lib/api.ts` 新增 `garminApi`（status／connectUrl／disconnect／import）；`ProfileScreen` 穿戴區新增「Garmin」卡（比照 Terra 卡：連接／已連接 athlete／最後同步時間 >48h ⚠️／手動匯入／中斷）；Terra 卡品牌清單改讀 `TERRA_PROVIDERS`。
- 既有 Terra-Garmin 使用者：卡片顯示「Garmin 已改為直接連接，請重新授權一次（歷史紀錄不受影響）」＋按鈕；上線當天發站內信給這 11 位。

### 3.4 營運
- 每日 08:00 營運報告「⌚ 穿戴串接」段落：garmin 直連改由 `integration_events` 統計（連結數、24h 事件數、最後事件時間），不再打 Terra。
- TG 告警：webhook 連續 5xx／dead 事件 >0／Garmin token refresh 連續失敗。

## 4. 資料庫（migration 193，可重複執行）
- `integration_events`（§2-2）＋索引 `(provider, status, received_at)`、`(provider, provider_user_id, received_at DESC)`。
- `activities` 加 `polyline TEXT NULL`、`km_paces INT[]`（若已存在則略過；Phase 2 用）。
- `app_settings`：`garmin_direct_enabled`（後台總開關，預設 false，方便先部署後開）。

## 5. 環境變數
`GARMIN_CLIENT_ID`、`GARMIN_CLIENT_SECRET`、`GARMIN_REDIRECT_URI`（預設 `https://www.dor.tw/api/v1/integrations/garmin/callback`）、`GARMIN_WEBHOOK_TOKEN`（≥32 字亂數；入口設定 endpoint 時嵌在路徑）、`GARMIN_API_BASE`（預設 `https://apis.garmin.com`；Evaluation 環境若不同則覆寫）、`TERRA_PROVIDERS`。token 加密沿用既有金鑰機制。

## 6. 驗證計畫
- Go 單元：state／PKCE 往返、webhook token 驗證、活動類型白名單、欄位對照（含 RecordedAt＝結束時刻、pace 自算）、回填窗口、`Save` 保留 floor、事件落地／重試／dead、Terra 對 garmin 事件略過、去重優先序表兩處一致。用 httptest 假 Garmin。
- Neon 臨時分支：193 冪等；連接→事件→活動→EXP/DP 帳本全鏈路；Terra-Garmin 列被直連覆蓋且 floor 不變；斷開清理。
- E2E：連接卡三態、手動匯入回饋、>48h ⚠️、後台事件重放。
- **Garmin Evaluation 環境真機**：至少 2 支錶（跑步／走路各一趟）、一次 30 天回填、一次撤銷授權；核對 §1 全部「待核對」項並更新本檔。
- 正式切換：`garmin_direct_enabled` 開 → 站內信 → 觀察 3 天日報 → 停用 Terra Garmin 通道。

## 7. 工作拆解與估時（Sonnet 工人平行）
| 子任務 | 內容 | 估時 |
|---|---|---|
| BACKEND-1 | OAuth2+PKCE、connect/callback/status/disconnect、token 刷新、Save 保留 floor | 2 天 |
| BACKEND-2 | webhook 落地表＋處理器＋重試／dead＋admin 重放；匯入對照；回填 | 2 天 |
| BACKEND-3 | Terra garmin 通道停用、去重優先序修正、日報改讀事件表、TG 告警 | 1 天 |
| FRONTEND | garminApi、Garmin 卡、Terra 卡品牌清單、重新授權提示、後台事件頁 | 1.5 天 |
| VERIFY | 單元／Neon／E2E／Evaluation 真機 | 2 天 |

合計約 8–9 個工作天（不含 Garmin 核發與 Evaluation 排程等待）。

## 8. 要向 Garmin 確認的問題（可附在簽約往返信中）
1. Health API 是否含 Activity Details（GPS 樣本）？
2. Push endpoint 是否有簽章或來源 IP 清單？
3. Backfill 單次最大範圍與速率限制？
4. Evaluation 環境使用者數上限與切到 Production 的方式？
5. 使用者撤銷授權後，已匯入資料的保留政策？
6. OAuth 是否強制 PKCE、token 壽命？
