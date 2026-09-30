# COROS Partner API 直連 規格草案 v0.1 — 2026-09-30

狀態：**申請階段**（與 Garmin 同步送出 API 申請；核發 Client ID/Secret 前無法真機測試）。本檔與
`docs/integration/GARMIN_DIRECT_SPEC.md` 並列，兩案共用大量架構決策；閱讀本檔前建議先讀過 Garmin
規格書。標「⚠️待確認」者需在官方核發帳號／文件後逐條核對再定案。

## 0. 目標與範圍、與 Garmin 案的關鍵差異

- 用 DOR 自己的 COROS Partner API 授權，讓 COROS 手錶使用者的跑步活動直接同步到 DOR（不經 Terra）。
- **⚠️重要澄清（避免對外溝通時跟 Garmin 案混為一談）**：COROS 官方文件**沒有找到**任何類似 Garmin
  「資料只能給授權方自己的端點、禁止轉交第三方／聚合器」的政策條款；相反，COROS 官方「Supported
  3rd Party Apps」頁面本身就列了 FitnessSyncer、Sport Heroes、RunGap 等聚合器，且申請頁明講審核是
  「標準化、非人工挑選」（"a standardized, objective developer onboarding process...to any
  platform that satisfies our standard security and operational requirements"）。也就是說，**COROS
  直連的動機是「穩定性」，不是「合規／政策」**。
- **⚠️修正（2026-09-30 覆核）：「Terra→COROS 已於 9/23 起無預警完全停止送資料」這個說法查無實據，
  且與本檔自己的數據、正式庫都矛盾，對外溝通（尤其 COROS 申請信）前務必改口／重新確認**：
  - 本檔 §1 表格同一格就自相矛盾：既寫「近 7 天無新活動」又寫「最後一筆 2026-09-28」——本檔撰寫日
    2026-09-30 往前推 7 天是 9/23，往前推 2 天才是 9/28，兩句話對不起來。
  - 覆核時重新查證正式庫（唯讀）：`activities(source='coros')` 最新一筆 `recorded_at=2026-09-28
    21:16`、`created_at=2026-09-29 20:46`（UTC）——資料直到覆核前一天都還在進來，且這筆是在
    `dailyreport.go`（v1.2.862，commit 2026-09-29 20:35 UTC）那則「COROS 實際到 9/23」的 comment
    寫下**僅 11 分鐘後**才落地，代表下那個結論時看到的還不是最終狀態。
  - 「近 30 天僅 1 筆」只算了未被標記重複的活動；把另外 12 筆 `flagged`（`multi_device_duplicate`／
    `cross_source_duplicate`，代表這位使用者同時有其他來源在記錄同一趟跑步）一起算，近 60 天其實有
    13 筆、且持續有新資料進來，只是每筆入庫時間跟活動發生時間常差 2–9 天——這比較像「COROS 手錶/App
    本身不常同步」或「Terra 輪詢、要等使用者手動同步錶況才抓得到」，不像管道整個掛掉。
  - **建議**：COROS 直連仍然值得做（Terra 自述用輪詢、非官方文件講的 webhook push，加上入庫常有
    數天落差，本身就是資料新鮮度隱憂），但申請信／表單／對老闆報告都**不要斷言「COROS 資料已於
    9/23 完全停止」**，改用「延遲不穩定、入庫常有數天落差」這種站得住腳的說法；上線前請工程或老闆
    直接在 Terra 後台／COROS App 上再核一次現況，不要只依賴這次覆核當下的查證。
  - 「Terra 用輪詢、COROS 官方文件講 webhook」只是機制不同的觀察，**不足以推論 Terra 走的是非官方
    ／逆向工程管道**——Terra 頁面自己也寫「Terra manages the Coros API credentials」，較可能是 Terra
    本身就是 COROS 官方 Partner、只是自己統一用輪詢架構處理所有品牌。原本「合理推論非官方管道」的
    講法證據不足，本次已弱化，不建議當作決策依據或對外說法。
  - 向老闆報告時仍建議把「Garmin＝政策逼迫」和「COROS＝穩定性/成本考量」分開講，但 COROS 這邊的
    「穩定性」證據要換成上面「延遲不穩定」的說法，不要用「已停止」。
- 上線後：Terra 的 COROS 通道停用（現有唯一 1 位 Terra-COROS 使用者引導重新走直連授權）；Terra 僅
  保留給 Polar／Suunto／Wahoo（目前 0 使用者，見 `WEARABLE_CUTOVER_PLAN.md` §5，是否保留待老闆決定）。
- 範圍：跑步類活動的自動同步（webhook push）＋歷史回填（pull，本案新增能力，見 §4）。
- 不在範圍（Phase 1）：FIT 檔下載解析（爬升／心率）、訓練計畫推播（`tp/push`）、GPX 路線推播
  （`route/push`）、每日健康資料（`getDailyData`）、COROS MCP（單用戶自助路徑，見 §2，不適合多用戶
  平台場景）。

## 1. 現況總結（已存在的程式碼，唯讀盤點）

**結論先講：COROS 直連不是從零開發，是把既有半成品「打開開關＋補前端＋補回填」。**

| 項目 | 位置 | 現況 |
|---|---|---|
| OAuth2 connect/callback/status/disconnect | `services/api/internal/integration/coros.go`（516 行） | 完整實作，仿 `strava.go` 授權流程＋`terra.go` 落地流程 |
| Router 掛載 | `main.go:328-339,504`：`r.Mount("/integrations/coros", corosHandler.Router())` | 端點已是活的（`/api/v1/integrations/coros/*`），只是 `enabled()`（`coros.go:90`）判斷 `COROS_CLIENT_ID`/`SECRET` 是否有值 |
| Webhook | `coros.go:281-387` | 先回 200 ack → 背景 goroutine 處理；**沒有落地表**，失敗只 log＋`notify.Alert`，無重放能力 |
| Token 加密 | `repository.go`，沿用 `STRAVA_TOKEN_KEY`（跨 provider 共用金鑰） | 已就緒 |
| 資料模型 | `user_integrations.via`（migration 165，已套用），provider/via 皆為自由 VARCHAR，**無 CHECK 約束**，`provider='coros'` 本來就合法 | 不需為了「認識 coros 這個值」新增 migration |
| Env 變數 | `config.go:77-80,136-138`：`COROS_CLIENT_ID`／`COROS_CLIENT_SECRET`／`COROS_REDIRECT_URI`（預設 `https://www.dor.tw/api/v1/integrations/coros/callback`，已核對程式碼與 config 預設值一致） | 三個變數目前應為空（正式 DB 查證 `coros/direct=0` 筆，代表從未真的啟用過） |
| 正式 DB 現況（唯讀查證，2026-09-30 覆核更新） | — | `user_integrations`: coros/terra=1（唯一一位）、coros/direct=0；`activities(source='coros')` 近 60 天 1 筆（未 flagged 口徑，即「未被判為重複」）／13 筆（含 flagged 全量口徑）、**最後一筆 `recorded_at`=2026-09-28、`created_at`=2026-09-29**——⚠️這與「近 7 天無新活動」／「9/23 起已停止」的敘述矛盾，見 §0 修正說明，不要對外沿用「已停止」的說法 |
| 前端 | 全庫搜尋 `corosApi`／`integrations/coros` | **零匹配**，使用者目前唯一入口是 `ProfileScreen.tsx` 的 Terra 卡（品牌清單提到 COROS 字樣，L52/704/710/1023），沒有獨立「COROS 直連」卡 |
| 測試 | `coros_test.go`（3 個純函式單元測試） | 遠少於 `terra_test.go`；OAuth／webhook／token 刷新／去重整合路徑無測試覆蓋 |
| 運動類型白名單 | `coros.go:46-58`：`mode*10+subMode`，只收 81/82/151/201（戶外跑/室內跑/越野跑/田徑場跑） | **沒有走路類**，比 Terra 既有行為（v771 跑走都收）窄——⚠️修正：原稿誤稱這個白名單與 `coros_test.go:11-17` 的測試案例（8/1、8/2、15/1、20/1）「對不上」，實際重新驗算 `mode*10+subMode`＝81/82/151/201，與測試案例完全一致（8×10+1=81 等），**兩處並無矛盾**，此前的說法是覆核時的誤判，已移除；唯一要補的真缺口是走路類代碼未列入白名單 |

## 2. COROS 官方申請（申請方式、資格、材料）

來源：COROS Help Center《Submit an API Application》《Partner API Access》《Connect Your Data》
（2026-09-30 抓取存檔於 `scratchpad/coros_research/*.html|.txt`）。

### 2.1 COROS 現在的三條開發者路徑（2026-09 改版）

| 路徑 | 對象 | 需申請？ | 能力 |
|---|---|---|---|
| Connect Your COROS to AI | 一般使用者接 ChatGPT/Claude | 不用 | 消費者教學，非平台整合 |
| Build on COROS MCP | 開發者接 COROS 資料 | **不用，自助** | OAuth2、22 個欄位、可讀寫訓練計畫；**單用戶、無 webhook、無雙向活動同步**，FIT 請求上限 50 筆/日 |
| **Partner API Access** | 有既有用戶規模的平台 | **要申請** | 多用戶 OAuth、webhook push（~5 分鐘）、雙向同步、GPX、FIT 下載、1,000 calls/分鐘 |

DOR 要申請的是 **Partner API**（既有近千位 Terra-COROS／潛在 COROS 使用者，非單用戶場景）。

### 2.2 申請流程

1. 打開申請表（Feishu 線上表單）：`https://coros-teams.feishu.cn/share/base/form/shrcnLqSduZsaNhbvDJTO2x0Vlf`
   （⚠️此表單為 JS 動態渲染，curl 無法讀出完整欄位，需要用真的瀏覽器打開填寫；已知官方明文要求的
   三項是公司資料、技術聯絡人、OAuth 2.0 redirect URI，表單本身**可能還有**app 說明／Logo／隱私政策
   連結／預估用戶數等欄位，⚠️待確認）。
2. 同步 email `api@coros.com`，附上：公司資料、技術聯絡人、OAuth 2.0 redirect URI。
3. 官方審核並簽「標準條款」（COROS API Terms of Use，全文未公開，只有在這一步才會看到，⚠️待確認
   費用／品牌歸屬規則是否在此文件中）。
4. 核發 `Client ID` / `Client Secret`。

### 2.3 資格要求（官方原文）

> "Established platform with demonstrated user base / Registered company with authorized
> technical representative / Standard security and data privacy compliance / Agreement to
> COROS API Terms of Use"

DOR（聚澤數位整合有限公司，已有正式公司登記、已有真實 COROS 使用者群、已有 `/privacy` 頁面）字面上
符合全部三項。官方明講審核是規則式（非人工競爭挑選），字面上通過機率高，但**實際審核時長／是否會
追問更多商業細節，完全無法從公開頁面查證**，⚠️待確認。

### 2.4 申請材料清單（DOR 這邊要準備）

| 項目 | 現況 |
|---|---|
| 公司名稱（英文） | ⚠️待確認：聚澤數位整合有限公司的正式英文登記名稱，申請草稿先留 placeholder |
| 技術聯絡人 | ⚠️待老闆／工程負責人決定填誰的姓名/email |
| OAuth 2.0 redirect URI | `https://www.dor.tw/api/v1/integrations/coros/callback`（已核對 `config.go:138` 程式碼預設值與 `coros.go` 路由一致，可直接使用） |
| 平台說明 | DOR（www.dor.tw）：雲端馬拉松／GPS 跑步追蹤／訓練課表／獎勵遊戲化平台，見 `scratchpad/coros_research/COROS_APPLICATION_DRAFT.md` |
| 隱私政策連結 | `https://www.dor.tw/privacy`（現成，上線前需在此頁新增 COROS 直連條目、見 §9） |
| 預估用戶數 | 目前 COROS 直連 0、Terra-COROS 歷史連線 1 位；若表單問「目標用戶數」可誠實回覆「現有 Garmin/COROS 合計約 12 位連線裝置的活躍用戶，平台總會員數更大」（實際數字由老闆核實，避免我方誤報） |

### 2.5 費用／審核時程／品牌規則

**三者皆⚠️查無公開資訊**：官方兩篇頁面（Submit an API Application、Partner API Access）都沒有出現
費用／定價字樣；沒有任何天數／週數的審核時程承諾；品牌／歸屬顯示規則（如是否要顯示「Powered by
COROS」）只可能在未公開的 API Terms of Use 全文中，需要送出申請、進到「Review and accept standard
terms」步驟才會知道。**建議申請信與 Garmin 案並行送出，越早排隊越早知道這些答案。**

## 3. 授權流程（OAuth2）

現有 `coros.go` 已實作的流程（沿用 `strava.go` 的 state 簽章模式）：

| 步驟 | 端點（現況，寫死正式站） | 說明 |
|---|---|---|
| 導向 COROS 授權頁 | `GET open.coros.com/oauth2/authorize` | `client_id`／`redirect_uri`／`response_type=code`／`state`（HMAC 簽章，含發起使用者與登入後導回頁） |
| 換 token | `POST open.coros.com/oauth2/accesstoken` | 回應含 `access_token`／`refresh_token`／`openId`／`expires_in`（⚠️目前程式碼是靠 5 個開源實作交叉驗證出的欄位名，非官方文件） |
| 刷新 token | `POST open.coros.com/oauth2/refresh-token` | ⚠️待確認：官方回應是否真的帶新 `access_token`；現況 `coros.go:441-444` 採保守 fallback（拿不到就沿用舊 token、本地延展 25 天） |
| 撤權 | `POST open.coros.com/oauth2/deauthorize` | ⚠️待確認 HTTP method（開源實作有用 GET 的，現況先用 POST） |
| Scope | `workout` | ⚠️待確認官方 scope 名稱是否即為此值 |

**⚠️待確認（申請核准並拿到官方文件後第一件事就是逐條核對）**：
1. 上述四個端點網址是否等於 `open.coros.com` 系列（curl 直連測試：`open.coros.com` 根路徑回
   404——不代表網域本身錯誤，只代表根目錄無內容；`opentest.coros.com` 沙盒連線失敗，DNS/連線層級，
   ⚠️無法單靠公開頁面證實，需等核准後由官方後台/文件確認）。
2. token 有效期（現況假設 30 天，`coros.go:234` 保守預設）。
3. `refresh_token` 是否會輪替（若輪替但程式沒存新值，會導致下一次刷新失敗）。
4. 沙盒環境 `opentest.coros.com` 是否仍可用、申請/測試期間是否需要先在沙盒跑一輪。

**架構決策（可挑戰）**：state HMAC 簽章、`(user_id, provider)` 唯一鍵覆蓋策略、token 加密金鑰，三者
與 Garmin 案完全共用同一套既有機制（`coros.go`/`repository.go`），不重新設計。

## 4. 資料交付（Push + Pull，本案的主要新增能力）

官方文件證實 Partner API 是 **push（webhook，~5 分鐘內）+ pull（REST）混合**，比現況程式碼（只做
push）多一塊：

| 能力 | 官方規格（來源：Partner API Access 頁「API reference」表） | 現況程式碼 |
|---|---|---|
| Webhook push | 活動資料 ~5 分鐘內送達我方 `POST /webhook`，header 帶 `client`/`secret`（明文，非 HMAC）——**官方文件證實現有 `verifyWebhookHeaders` 的猜測完全正確** | 已實作（`coros.go:315-321`） |
| `getWorkoutRecords` | **可拉取歷史活動，單次最長 30 天、可回溯 3 個月** | **未實作**——現況 `coros.go:207-208` 的註解「沒有可拉取歷史活動列表的端點」是舊假設，官方文件推翻了這個假設，**這次直連上線時應該新增**（§7 回填設計） |
| `getDailyData` | 每日健康資料 | 不在本案 Phase 1 範圍 |
| `getWorkoutDetailFit` | FIT 檔下載 | 不在本案 Phase 1 範圍（Phase 2 可選，見 §5 備註） |
| 速率限制 | 1,000 calls/分鐘 | 回填批次處理需遵守，見 §7 |

**這個新發現有實際價值**：即使 Terra→COROS 通道實際上不是「完全停止」而是延遲/不穩定（見 §0
修正），COROS 官方回填（可回溯 3 個月）仍然有用——申請核准後可以把「申請等待期間」這段沒有直連的
空窗活動資料**拉回來**，不必依賴 Terra 這段時間是否有正常送資料。

## 5. 資料對照（`NormalizedActivity`）

| 欄位 | 來源 | 現況／待辦 |
|---|---|---|
| Source | `coros` | 已定 |
| ExternalID | `labelId` | 已定 |
| DistanceKm | `distance`（公尺）/1000 | 已定 |
| DurationS | `duration`（秒） | 已定 |
| AvgPaceS | 自算 `corosAvgPaceS`（COROS JSON 無配速欄，三來源口徑一致，不用官方配速欄） | 已定 |
| RecordedAt | `startTime` | ⚠️Garmin 案採「結束時刻」為全站慣例（`RecordedAt=start+duration`），現況 COROS 用 `startTime`（開始時刻）——`worker/main.go:1105` 註解也承認「其餘來源(strava/garmin/coros)的 recorded_at 是開始時間」，**這是既有的跨來源不一致，本次不強行改動（會動到既有活動排序/去重時間窗），只記錄待未來一併檢討** |
| AscentM／AvgHR | 留空（需解析 `fitUrl` 才拿得到，Phase 1 不做） | 沿用現況；Phase 2 可選（呼叫 `getWorkoutDetailFit`） |
| 運動類型白名單 | `mode*10+subMode`：81/82/151/201 | **待補走路類代碼**——官方核准後第一件事應確認走路的 mode/subMode（比照 Garmin WALKING/HIKING、Terra 既有跑走都收的口徑），否則上線後「COROS 走路資料進不來」會是第一個被使用者回報的症狀 |
| 運動類型代碼表本身 | `corosRunningModes`（5 個開源實作交叉驗證，非官方文件） | ⚠️修正：原稿誤稱 `coros_test.go:11-17` 測試案例（8/1、8/2、15/1、20/1）與正式代碼表（81/82/151/201）「對不上」，重新驗算後兩者一致（無矛盾，見 §1）；真正待辦是核准後拿官方文件核對這 4 個代碼是否正確、並補走路類代碼（見下一列） |

## 6. DB 變更（migration 編號協調）

**結論：COROS 本身不需要新 migration 才能「被認識」**——`provider`/`via` 都是自由 VARCHAR、無 CHECK
約束（`014_integrations.sql`、`165_user_integrations_via.sql` 已核對），`provider='coros'`、
`via='direct'` 現在就合法。真正需要的 DB 變更，是**可靠度層**和**沙盒/開關設定**，且要跟 Garmin
案協調編號：

| 項目 | 編號建議 | 說明 |
|---|---|---|
| `integration_events`（webhook 落地表，`id, provider, event_type, provider_user_id, payload JSONB, received_at, status pending\|done\|error\|dead, attempts, last_error, processed_at`） | **與 Garmin 共用同一張表**，由**先動工的那一案領走 migration 196**，此表設計時 `provider` 欄位就要涵蓋 `'garmin'`／`'coros'` 兩種值（provider-agnostic），另一案照描述在文件裡沿用該表、不重建 | 這是 COROS 現況完全沒有的可靠度層（現在 webhook 失敗只 log+alert，無重放能力）；跟 Garmin 一起做可以一次把兩個 provider 的 webhook 都掛上重試/dead-letter/後台重放 |
| 若 COROS 這邊先動工、Garmin 尚未排入 196 | **改用 migration 197** 建立同一張 `integration_events` 表（欄位設計仍保持 provider-agnostic），Garmin 動工時檢查 `schema_migrations` 若已有 197 建好此表，Garmin 規格書的「migration 196」段落改為「沿用既有 `integration_events` 表，不重建」——**兩邊工程負責人開工前務必先看一次 `schema_migrations` 目前最大號＋是否已存在此表，避免撞號或重複建表**（比照 memory「migration-apply-audit」慣例） | 誰先開工誰建表，後開工者只加自己需要的欄位／索引（若有） |
| `app_settings` 新增鍵 `coros_direct_enabled`（預設 `'false'`，後台總開關，方便先部署程式碼、後開流量） | 不需 migration（`app_settings` 是既有通用 KV 表，`INSERT ... ON CONFLICT DO NOTHING` 即可，比照 `042_app_settings.sql` 慣例） | 與 Garmin 案 `garmin_direct_enabled` 同一種做法 |
| `COROS_SANDBOX`（環境變數，非 DB）：允許把 `corosBaseURL` 從寫死的 `open.coros.com` 切到 `opentest.coros.com` | 屬於 §8 環境變數，非 migration | 申請/測試期間若沙盒可用會需要 |

**本文件對 migration 編號的立場**：不在此預先鎖死 196 或 197 給 COROS，因為實際順序取決於哪一案先
排入開發排程（老闆／編排者決定）；本節把「協調規則」寫清楚，交由開工當下的工程負責人核對
`schema_migrations` 現況後定案，避免像 memory 記載的「137 曾漏套」「187 撞號」重演。

## 7. 可靠度（事件落地、重試、回填）

1. **Webhook 落地**（見 §6）：驗證 header → 逐筆 INSERT `integration_events`（`provider='coros'`）→
   立即回 200 ack → 背景處理器處理，失敗指數退避重試 5 次後標 `dead`，後台可重放。取代現況「先 ack
   →背景處理失敗只 log+alert」的無重放設計。
2. **回填（新增能力，§4）**：
   - 連接當下（`Callback` 成功後）：呼叫一次 `getWorkoutRecords`，起始時間取「該使用者最後一筆
     `coros` 活動時間」或「30 天前」取較晚者（比照 Garmin 案的 floor 邏輯，`created_at` 不變）。
   - 手動匯入端點（新增 `POST /coros/import?days=1..90`，比照 Garmin `/import` 與 Terra 既有
     `/terra/import` 的使用者手動補抓體驗）：COROS webhook 已知現象是「常常只送 daily 不送
     activity」（沿用既有 COROS-via-Terra 的觀察），使用者需要一個手動按鈕補抓——**這是 COROS 直連
     現況完全沒有、必須新增的功能缺口**。
   - 速率限制 1,000 calls/分鐘：回填批次以使用者為單位序列化，不並行對同一使用者發多筆
     `getWorkoutRecords`。
3. **Cutover 空窗回補**：核准後若距送出申請日未滿 3 個月，用回填把「申請等待期間」這段直連空窗的
   活動拉回來（見 §4）；不需預設 Terra 在這段期間完全沒有資料（見 §0 修正），回填只是不論 Terra
   狀況如何都能保底補齊。

## 8. 去重、GPS 校正、來源命名（與現有系統的整合點）

這些都是**現有系統已經支援 `source='coros'`**，COROS 走直連或走 Terra 對它們是透明的，唯一要注意
的是兩處已知的「優先序表漂移」，建議跟 Garmin 案一起順手修掉（不是本案新增的問題）：

| 項目 | 位置 | 現況 |
|---|---|---|
| 跨來源去重優先序（正確版本） | `services/api/internal/profile/dedup.go:34,55-60` | `garmin > coros > polar > suunto > wahoo > strava`，已含 coros，無需改 |
| 跨來源去重優先序（**已漂移**） | `services/worker/main.go:415-417` | 只有 `garmin > coros > strava`，**缺 polar/suunto/wahoo**——Garmin 規格書已點名要修，COROS 案不需要重複改，但兩處要保持同步 |
| GPS 距離校正參考來源 | `services/api/internal/gpscalib/service.go`，`ref_source` 白名單 | 已含 `coros`；DB 目前 `user_gps_calib.ref_source` 有 1 筆 `coros`，直連上線不影響既有校正結果 |
| 後台資料來源分布儀表板 | `services/api/internal/profile/metrics.go:6-35` | 全時間統計 `source='coros'` 活動數，**不分 via**，COROS 切直連不用改這支統計，可直接拿來當替換前後對照指標 |
| 每日報告「穿戴靜默中斷」告警 | `services/api/internal/ops/dailyreport.go:471-507` | 查詢**不分 via**（direct/terra 皆涵蓋，已是這次任務context 中 v1.2.862 剛上線的邏輯），COROS 切直連後這段邏輯**不用改** |

## 9. 安全

- Webhook 驗證：明文 header `client`（=我方 `COROS_CLIENT_ID`）／`secret`（=我方
  `COROS_CLIENT_SECRET`），`subtle.ConstantTimeCompare` 比對（`coros.go:317-321`）——**官方文件已證實
  這個設計正確**（"client_id/secret in header"），不需改動。
- Token 加密：沿用 `STRAVA_TOKEN_KEY`（跨 provider 共用一把 AES-256-GCM 金鑰），與 Garmin 案共用。
- State 簽章：HMAC，callback 無登入時綁定發起者（防 CSRF／跨帳號綁定），與 Strava 同構。
- 撤權：使用者在 DOR 斷開 → 呼叫 `deauthorize`（⚠️method 待確認）→ 刪 token →
  `ResetPreferredSource` → `DeleteProviderActivities`（`coros.go:165-186`，與 Strava/Terra/Garmin
  案一致）。
- 隱私政策更新（§ 見 `WEARABLE_CUTOVER_PLAN.md`）：`/privacy` 目前把 COROS 併在 Terra 條目裡講（跨境
  傳輸經 Terra），直連上線後需要改成獨立列出「COROS（直接串接）」，資料項目、是否跨境（COROS 是否
  將資料存放於境外伺服器，⚠️待確認）、撤銷方式。

## 10. 測試計畫

- **Go 單元**（白名單本身與測試案例並無矛盾，見 §1/§5 修正；優先補的是走路類代碼案例）：state 往返、webhook header 驗證、運動類型
  白名單（含走路類補齊後的案例）、`corosAvgPaceS`／欄位對照、`Save` 覆蓋既有 Terra-COROS 列時
  `created_at` 不變、事件落地／重試／dead（若採用 `integration_events`）、去重優先序表兩處一致。
  目標至少比照 `terra_test.go` 的規模（目前 `coros_test.go` 僅 3 個純函式測試）。
- **Neon 臨時分支**（禁止對正式庫操作）：
  1. 建臨時分支後套用相關 migration（`integration_events`／`app_settings` 新鍵）並確認冪等。
  2. 用假資料模擬「既有 1 位 Terra-COROS 使用者改走直連」：確認 `via` 從 `terra`→`direct`、
     `created_at`（floor）不變、舊活動不受影響。
  3. 模擬 webhook 送入一筆活動（含走路類、含缺欄位的邊界案例）→ 確認落地→處理→去重→EXP/SP 帳本
     全鏈路。
  4. 模擬回填（`getWorkoutRecords` 假資料）與速率限制序列化。
- **E2E**：COROS 卡三態（未連接/已連接/斷開）、手動匯入回饋、後台事件重放（若做）。
- **COROS 沙盒／正式環境真機**（核准後）：至少 1 支錶跑一趟戶外跑＋一趟走路（若代碼表補齊）、驗證
  webhook 落地時間（是否真的 ~5 分鐘）、驗證回填 API 實際回應 schema、驗證 refresh-token 是否真的
  帶新 access_token、驗證 deauthorize 實際 HTTP method——把 §3/§4/§5 全部「⚠️待確認」逐條核對更新
  回本檔。

## 11. 上線（Rollout）

1. 送出申請（§2）——申請信／表單措辭請先套用 §0 修正（不要寫「COROS 資料已於 9/23 完全停止」）。
2. 等待核准期間：視 Garmin 進度決定 `integration_events` 歸屬哪個 migration 編號（§6）、補前端 COROS
   卡＋手動匯入端點（§7）、補走路類代碼（若能從其他公開資料源交叉驗證；否則等核准後拿官方文件確認）。
3. 拿到 `COROS_CLIENT_ID`/`SECRET` → 填 Railway env → `coros.go` 現成的 `enabled()` 自動轉真
   （不需重新部署程式碼，只需重啟服務讀新 env，或視 Railway 設定是否自動重啟）。
4. `coros_direct_enabled`（app_settings）開關保持關閉，先用老闆／內部測試帳號走一次真實連接
   （§10 真機測試）。
5. 驗證通過 → 開 `coros_direct_enabled` → 前端 COROS 卡上線 → 站內信通知既有 1 位 Terra-COROS
   使用者「請重新連接一次（歷史紀錄不受影響）」。
6. 觀察 3 天日報（穿戴串接段落＋靜默中斷告警）確認資料正常流入。
7. 確認穩定後，併入 `WEARABLE_CUTOVER_PLAN.md` 的整體 cutover 時程，停用 Terra COROS 通道。

## 12. 工作拆解與估時（Sonnet 工人平行，供編排者參考）

| 子任務 | 內容 | 估時 |
|---|---|---|
| BACKEND-1 | webhook 落地表（若與 Garmin 共用則協調編號）＋處理器＋重試/dead＋admin 重放 | 1.5 天（若表已由 Garmin 案建好則減半） |
| BACKEND-2 | `getWorkoutRecords` 回填（連接當下＋手動 `/import` 端點＋序列化速率限制） | 1.5 天 |
| BACKEND-3 | `coros_direct_enabled` 開關、走路類代碼補齊（核准後）、隱私政策文案 | 1 天 |
| FRONTEND | `corosApi`、COROS 卡（比照 Garmin 卡設計）、既有使用者重新授權提示 | 1 天 |
| VERIFY | 單元／Neon 分支／E2E／沙盒或正式環境真機 | 1.5 天 |

合計約 5.5–6.5 個工作天（不含 COROS 核發等待時間；比 Garmin 案少，因為 OAuth／webhook 骨架已現成；
原稿另列的「FIX-1 修白名單/測試矛盾」0.5 天已刪除——覆核後確認白名單與測試案例並無矛盾，不需要
這個工作項，見 §1/§5 修正）。

## 13. 要向 COROS 確認的問題（申請信或核准後往返信可附上）

1. Partner API 是否收費？如何收費（按用戶數／按呼叫量／固定月費）？
2. 審核通常需要多久？
3. 正式 OAuth 端點是否確實是 `open.coros.com/oauth2/*`？沙盒 `opentest.coros.com` 是否可用？
4. `refresh-token` 端點回應是否包含新的 `access_token`／`refresh_token`？
5. `deauthorize` 端點正確的 HTTP method？
6. `getWorkoutRecords`／webhook payload 完整欄位 schema（尤其走路類的 `mode`/`subMode` 代碼）？
7. 品牌／歸屬顯示是否有強制要求（如需顯示「Powered by COROS」）？
8. API Terms of Use 全文能否在簽約前先索取審閱？

---

**關鍵檔案路徑**：`services/api/internal/integration/coros.go`、`coros_test.go`、`repository.go`、
`services/api/internal/config/config.go:77-80,136-138`、`services/api/cmd/api/main.go:328-339,504`、
`services/api/internal/profile/dedup.go`、`services/worker/main.go:415-417`、
`services/api/internal/gpscalib/service.go`、`services/api/internal/ops/dailyreport.go:471-507`、
`services/api/migrations/014_integrations.sql`、`165_user_integrations_via.sql`、
`042_app_settings.sql`（`app_settings` 表結構參考）、`docs/integration/GARMIN_DIRECT_SPEC.md`
（migration 196／`integration_events` 設計協調對象）。

**研究存檔**：`scratchpad/coros_research/*.html|.txt`（COROS 官方頁面／Terra 官方頁面／同業比較，
2026-09-30 抓取）。
