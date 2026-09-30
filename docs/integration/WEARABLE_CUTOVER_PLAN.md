# Garmin＋COROS 直連切換計畫（Terra 退場）v0.1 — 2026-09-30

狀態：**規劃階段**（Garmin、COROS 兩案皆在申請中，尚未核發 API key）。本檔是 Garmin／COROS 兩份
直連規格書（`GARMIN_DIRECT_SPEC.md`、`COROS_DIRECT_SPEC.md`）之上的**整體切換與退場計畫**，回答
「什麼時候做、依什麼順序、怎麼知道可以安全取消 Terra」。

## 0. 目的

把 Garmin、COROS 兩個品牌的資料串接，從「透過 Terra 聚合器」換成「DOR 自己申請的官方直連」，並在
確認安全後取消 Terra 訂閱。兩案動機不同（見 §2），但因為程式碼共用大量基礎設施（`user_integrations`
`via` 欄位、token 加密、去重優先序、每日報告穿戴段落），適合合併規劃、分階段一起切換。

## 1. 現況與數字（唯讀查證，Neon 正式庫，2026-09-30，僅聚合計數，無個資）

| provider | via | 連線數 | 近期活躍 |
|---|---|---|---|
| garmin | terra | 11 | 8（近 30 天有活動） |
| coros | terra | 1 | ⚠️修正（見下方說明）：最後一筆活動 `recorded_at`=2026-09-28、入庫 `created_at`=2026-09-29，即覆核前一天，並非「7–30 天前」／「9/23 起無資料」 |
| strava | direct | 11 | 5 |
| garmin | direct | **0** | — |
| coros | direct | **0** | — |
| polar / suunto / wahoo | terra | **0（三個品牌合計 0）** | — |

近 60 天活動筆數（未 flagged 口徑，即「未被判為重複」的活動）：App GPS 3159 筆（187 人）／Strava
103 筆（6 人）／Garmin 57 筆（8 人）／COROS 1 筆（1 人）——⚠️COROS 這個「1 筆」只是未被判重複的
數量，若含 flagged（被判跨來源/跨裝置重複）的活動，近 60 天其實有 13 筆、最新一筆 `recorded_at`
2026-09-28，並非「無新資料」，見 §1 下方修正說明。

**關鍵結論**：
- **取消 Terra 對 Polar／Suunto／Wahoo 使用者衝擊為零**——這三個品牌在 `user_integrations` 裡完全
  沒有連線紀錄（0 筆）。
- 真正受影響的只有 **11 位 Garmin 使用者**（8 位近期活躍）與 **1 位 COROS 使用者**。
- **⚠️修正（2026-09-30 覆核）**：原稿在這裡寫「其 Terra 同步本來就已經斷了一週以上」，覆核唯讀 DB
  發現不成立——這位使用者最新一筆 `coros` 活動 `recorded_at=2026-09-28`、`created_at=2026-09-29`
  （覆核前一天），且與 `COROS_DIRECT_SPEC.md §0` 的修正說明一致：Terra→COROS 通道並未「已於 9/23
  完全停止」，比較像是延遲數天、且常被判為跨來源重複。**這個修正不影響本檔的整體建議**（COROS 直連
  仍值得跟 Garmin 一起做），但代表「COROS 使用者換直連是在修復一個已死的連線」這個說法不成立，對外
  溝通與老闆報告請改用「COROS 直連是為了取代一個不穩定/延遲的聚合器管道」，細節見
  `COROS_DIRECT_SPEC.md §0`。
- `schema_migrations` 目前最大號 195；196 已由 Garmin 規格書預留給 `integration_events`（若 COROS
  案先動工，依 `COROS_DIRECT_SPEC.md` §6 的協調規則改用 197 或沿用先建好的表，**兩邊工程負責人開工
  前務必核對 `schema_migrations` 實際現況**，避免撞號）。

## 2. 為什麼要換（兩個品牌的動機不同，對外溝通時分開講）

| 品牌 | 動機 | 性質 |
|---|---|---|
| **Garmin** | Garmin 開發者政策明文要求資料只能送到授權方自己的端點、不得轉交第三方／聚合器 | **合規／政策問題**——用 Terra 本身就不符合 Garmin 政策，直連是唯一合法路徑 |
| **COROS** | COROS 官方文件**沒有**類似禁令（官方甚至歡迎聚合器）；現有 Terra→COROS 通道**不是**「已於 9/23 完全停止」（⚠️見 §1 修正，覆核 DB 顯示資料到覆核前一天都還有進來），而是入庫常有數天延遲、且大量被判為跨來源重複，資料新鮮度不佳；Terra 自述用「輪詢」拿 COROS 資料，與官方文件講的「webhook push」機制不同，但證據不足以推論 Terra 走的是非官方管道 | **穩定性／成本問題**——COROS 直連不是政策逼迫，是「既有官方管道可能更即時可靠、且既然要重寫 Garmin 骨架，COROS 順手一起做成本很低」的工程判斷 |

兩者共通的附加理由：Terra 是按月付費的第三方訂閱，若 Garmin/COROS 都改直連，且 Polar/Suunto/Wahoo
本來就 0 使用者，Terra 訂閱可能已無存在必要（最終取消與否是商業決策，見 §5）。

## 3. 依賴盤點（cutover 時需要一併處理的程式碼路徑）

| 功能 | 檔案:行 | 需要的處理 |
|---|---|---|
| 跨來源去重優先序（正確版本） | `services/api/internal/profile/dedup.go:34,55-60` | 已含 garmin/coros，直連上線不用改邏輯本身 |
| 跨來源去重優先序（**已漂移，需修**） | `services/worker/main.go:415-417` | 缺 polar/suunto/wahoo，與上面不同步——cutover 前順手修掉，兩處保持一致 |
| GPS 距離校正參考來源 | `services/api/internal/gpscalib/service.go` | `ref_source` 白名單已含 garmin/coros，不用改 |
| GPS 追蹤頁「外部來源持有配速」判斷 | `apps/web/src/app/track/page.tsx:925-935`（`isTerraHold` 只檢查 `integrationsApi.terraStatus()` 的 `connections`） | **cutover 後必改**：使用者改走直連後，`terraStatus()` 不會再列出他，這段邏輯會誤判「沒有外部來源持有配速」，導致直連使用者的 GPS 結束後重複上傳／跟外部來源打架。需同時檢查 Garmin/COROS 各自的 `/status` 端點 |
| 每日報告「穿戴串接」段落 | `services/api/internal/ops/dailyreport.go:117-118` + `main.go:324`（`opsHandler.SetWearableReporter(terraHandler)`） | 直連上線後，統計來源要從「打 Terra」改成「讀 `integration_events`／各 provider 自己的 handler」，依 `GARMIN_DIRECT_SPEC.md` §3.4 設計 |
| 「疑似靜默中斷」告警 | `services/api/internal/ops/dailyreport.go:471-507` | 查詢本身**不分 via**（direct/terra 皆涵蓋），架構上已跟 Terra 解耦，**cutover 不用改邏輯**，只需把告警文案裡提到 Terra 的措辭順手改掉 |
| 手動匯入 | `services/api/internal/integration/terra.go`（`/terra/import`） | COROS/Garmin webhook 常有「只送 daily 不送 activity」的已知現象，使用者靠這個按鈕補抓；**直連版本目前都沒有對應端點**，是兩案規格書都列的新功能缺口，cutover 前必須補上 |
| 前台隱私權政策 | `apps/web/src/app/privacy/page.tsx:38`（提到「Terra（跨境傳輸）」，把 Garmin/COROS 併在一起講） | cutover 時改成分別列 Garmin／COROS 直連條款，拿掉 Terra 段落（或改為只提 Polar/Suunto/Wahoo，視 §5 決定） |
| 後台資料來源分布儀表板 | `services/api/internal/profile/metrics.go:6-35`、`apps/web/src/app/admin/overview/page.tsx` | 全時間統計、不分 via，**不用改**，可直接當 cutover 前後的對照指標 |
| 前端連接卡 | `apps/web/src/app/.../ProfileScreen.tsx`（Terra 卡品牌清單含 Garmin/COROS 字樣，L52/704/710/1023） | 新增獨立 Garmin 卡＋COROS 卡（各自指向各自 direct handler），Terra 卡品牌清單收斂到只剩 Polar/Suunto/Wahoo |
| Env vars | `main.go:311-317`（Terra）／`config.go:77-80,136-138`（COROS）／`GARMIN_DIRECT_SPEC.md §5`（Garmin） | 新增 `GARMIN_*`／確認 `COROS_*`／`TERRA_PROVIDERS` 改窄 |
| DB schema | `migrations/165_user_integrations_via.sql`（已套用） | `via` 欄位是本次 cutover 的核心：同一 `(user_id, provider)` 唯一鍵，改直連時覆蓋同一列、保留 `created_at`（里程 floor）——這代表 Garmin/COROS 使用者重新走直連 OAuth 時，歷史資料與 floor 不受影響，**不需要資料遷移腳本** |

## 4. 分階段時程

> ⚠️ 所有「N 天／N 週」皆為工程估時，**不含官方審核等待時間**（Garmin/COROS 審核時程官方皆未公開，
> 見兩份規格書 §13/§2.5）。實際時程由核准時間點決定，以下是核准後的相對順序。

### Phase 0（現在，2026-09-30）— 同步送件
- 送出 Garmin 開發者申請（已在進行中，依使用者說明「確認信已回覆」）。
- 送出 COROS Partner API 申請（`COROS_DIRECT_SPEC.md` §2；郵件草稿見
  `scratchpad/coros_research/COROS_APPLICATION_DRAFT.md`，需老闆補公司英文名稱／技術聯絡人後寄出）。
- 兩案並行不互相阻塞：COROS 骨架已現成，可以在等 Garmin 審核的同時先做 COROS 的加固與前端。

### Phase 1 — 等待核准期間，先把能做的都做完（不需 key）
- 決定 `integration_events` 表由哪一案領走 migration 196（依實際開工順序，見 §1 提醒）。
- 補齊兩案前端連接卡、手動匯入端點（§3）、去重優先序表同步（§3）。
- 依 `GARMIN_DIRECT_SPEC.md §7` 的工作拆解，Sonnet 工人平行做 BACKEND-1/2/3、FRONTEND、VERIFY。
- **不觸碰**：`app_settings.garmin_direct_enabled`／`coros_direct_enabled` 皆保持 `false`，現有 Terra
  流量完全不受影響。

### Phase 2 — 核准後，先用內部帳號在 Neon 臨時分支＋正式環境小規模驗證
- 兩份規格書 §10（Garmin）/§10（COROS）的完整驗證計畫（單元→Neon 分支→E2E→真機）。
- **用擁有真實 Garmin／COROS 手錶的帳號**（老闆本人或指定測試帳號）先手動連接一次，確認 webhook 落
  地、回填、去重、EXP/SP 帳本全鏈路正確，且看到「穿戴串接」日報段落數字正常。

### Phase 3 — 雙軌並行期（dual-run）
⚠️**重要限制，寫清楚避免誤解**：因為 `(user_id, provider)` 是唯一鍵、`via` 欄位只能存一個值
（`repository.go` `Save`/`SaveTerra` 都是覆蓋同一列），**單一使用者不可能同時「經 Terra」又「直連」
同一個品牌**。所以這裡的「雙軌並行」是**艦隊層級**，不是個人層級：
- 已經重新授權直連的使用者 → 走新管道。
- 尚未重新授權的既有 Terra 使用者 → 繼續走 Terra（`TERRA_PROVIDERS` 暫不移除 garmin/coros）。
- 兩邊的活動一律落在 `activities.source='garmin'`／`'coros'`，靠既有 `UNIQUE(source, external_id)`
  天然冪等；不會有「同一位使用者同一趟活動被兩邊各記一筆」的問題，因為同一時間只有一條連線在跑。
- 這個階段的長度＝「使用者重新連接活動」（Phase 4）進行的時間，不是固定天數。

### Phase 4 — 使用者重新連接活動（in-app + email）
- 對象：11 位 Terra-Garmin 使用者、1 位 Terra-COROS 使用者。
- 站內信 + 連接卡片文案：「Garmin／COROS 已改為官方直接連接，請重新授權一次（歷史紀錄與里程不受
  影響）」，附一鍵連接按鈕。
- 建議觀察期：至少 1–2 週，讓使用者有時間點擊重新連接；可搭配一次 email 提醒。
- 追蹤指標：`admin/data-source-metrics`（`profile/metrics.go`）＋ `user_integrations` 的
  `via='direct'` 筆數逐日增加。

### Phase 5 — 切換預設連接按鈕
- 前端「連接裝置」入口的預設按鈕改為直連 Garmin／COROS 卡；Terra 卡（若保留給 Polar/Suunto/Wahoo）
  移到次要位置或按 §5 決定整張拿掉。

### Phase 6 — 停用 Terra 的 Garmin／COROS webhook
- `TERRA_PROVIDERS` 環境變數收斂（依 §5 決定是否保留 polar/suunto/wahoo）。
- Terra webhook 收到 `provider=garmin`／`provider=coros` 的事件一律略過並 log（避免極少數還沒重新
  連接的使用者資料突然消失；比照 `GARMIN_DIRECT_SPEC.md §2-6`）。

### Phase 7 — 取消 Terra 訂閱
- 確認條件：Phase 4 的重新連接率已達可接受水準（例如 11 位 Garmin + 1 位 COROS 中，除少數失聯/流失
  帳號外都已重新連接），且 §5 對 Polar/Suunto/Wahoo 的處置已定案。
- 建議在取消前保留至少 1–2 週的「Terra 帳號仍在、但 webhook 已停用」觀察期作為安全網（見 §6
  rollback），再正式去電/去信 Terra 取消訂閱。

## 5. Polar／Suunto／Wahoo 使用者的選項（目前 0 使用者）

現況：這三個品牌透過 Terra 連接的使用者數為 **0**。這是純粹的商業決策，列出選項交老闆拍板：

| 選項 | 說明 |
|---|---|
| A. 直接砍掉，不再支援 | 既然 0 使用者，Terra 取消後這三個品牌就從連接卡選單移除；未來若有使用者詢問，再視需求評估是否個別申請（Polar/Suunto/Wahoo 是否也有官方直連 API，本次未研究，⚠️待確認） |
| B. 保留 Terra 最小訂閱只為這三品牌 | 若 Terra 訂閱費用低、且老闆希望保留品牌廣度作為行銷宣傳（「支援 Garmin/COROS/Polar/Suunto/Wahoo」），可以只保留 Terra 給這三牌，但這樣 Terra 訂閱不會真的取消，跟本次「Terra 可以取消」的目標衝突，需要老闆明確決定是否接受 |
| C. 之後比照 COROS 模式個別評估 | 若未來有使用者需求，重新走「研究該品牌官方直連 API 可行性」流程 |

**本檔不代老闆決定**，建議選項是 A（0 使用者、直連使用者又已涵蓋兩大品牌，繼續付 Terra 訂閱費用
的理由不足），但費用/品牌廣度的取捨屬於商業判斷。

## 6. Rollback 計畫

**⚠️Garmin／COROS 不對稱，兩者的 rollback 選項不一樣，這點原稿沒有區分，必須明確寫清楚**：Garmin
開發者政策明文禁止把資料轉交第三方／聚合器（見 §2），這正是本次要把 Garmin 從 Terra 換成直連的理由
本身；如果 Garmin 直連上線後出問題就讓使用者「改回連 Terra」，等於重新製造出一開始要解決的那個政策
違規。COROS 沒有這條限制，可以安全地把 Terra 當作 Garmin 沒有的一種安全網。

- **DB 層面天然安全**：`via` 欄位覆蓋設計代表「改直連」不會刪除或搬動歷史活動資料，這一點對兩個品牌
  都成立，不受下面的差異影響。
- **COROS**：若直連連線出問題，使用者可以在 Terra 尚未停用（Phase 6 之前）期間改回連 Terra（重新走
  Terra widget 授權，`SaveTerra` 會覆蓋回 `via='terra'`，`created_at` 邏輯不同——⚠️需在 Phase 6 前
  確認 `SaveTerra` 的 floor 規則是否會因「先 direct 後 terra」的順序產生非預期結果，建議 Neon 分支
  測試涵蓋此案例）。
- **Garmin（不可回退到 Terra）**：若直連連線出問題，**不能**引導使用者改連 Terra——這樣做會讓 Garmin
  資料重新經過第三方，違反 Garmin 政策本身。正確做法是「停止對外開放 Garmin 直連新連線
  （`garmin_direct_enabled` 關掉）、既有已連接使用者的資料短暫中斷但不轉走其他管道、工程優先修直連
  本身的問題」；只有在使用者主動要求、且清楚知情的情況下，才考慮暫時退回 Terra 作為最後手段，且應
  視為需要老闆核准的例外，不是本檔預設的 rollback 路徑。
- **監控觸發 rollback 的訊號**：日報「穿戴串接」段落連續 3 天顯示某 provider 事件數異常下降、
  webhook 5xx 比例升高、`integration_events` dead 事件數 >0（若採用）、TG 告警。
- **Rollback 動作**：`coros_direct_enabled`（app_settings）關掉 → 前端改回顯示 Terra 卡 →
  `TERRA_PROVIDERS` 加回 coros → 已重新連接 COROS 直連的使用者需要再手動改回 Terra 授權一次（無法
  自動切回，因為 Terra widget 連接是使用者主動行為）。**Garmin 不執行這個流程**（見上）；
  `garmin_direct_enabled` 只有「關閉」一個方向，關閉後是「沒有新資料進來」而不是「改走 Terra」。
- **Terra 訂閱不立刻取消**：至少保留到 Phase 4 重新連接率確認穩定、且觀察期（§4 Phase 7）過後，避免
  「取消訂閱後才發現 COROS 直連有問題、卻已經回不去 Terra」的最壞情況（此處風險僅適用於 COROS，
  Garmin 依上述政策不應該依賴 Terra 作為安全網）。

## 7. Owner 檢查清單 vs 工程檢查清單

### Owner（老闆）
- [ ] 確認 Garmin 申請進度（已在進行中）。
- [ ] 補齊 COROS 申請所需資訊（公司英文名稱、技術聯絡人姓名/email）並寄出申請
      （`scratchpad/coros_research/COROS_APPLICATION_DRAFT.md`）；寄出前先在 Terra 後台或 COROS
      App 上親自核一次目前 COROS 資料是否真的中斷（本檔 §1／`COROS_DIRECT_SPEC.md §0` 覆核發現
      「9/23 起已完全停止」查無實據，不要把這句話寫進申請信或跟 COROS 的往來信件）。
- [ ] 核准後審閱 Garmin／COROS 的 API Terms of Use（費用、品牌歸屬規則），必要時找法務看過。
- [ ] 決定 §5 Polar/Suunto/Wahoo 的處置方案（A/B/C）。
- [ ] 核准 Phase 4 使用者重新連接的站內信/email 文案發送時間。
- [ ] 核准 Phase 7 正式取消 Terra 訂閱的時間點（去電/去信 Terra 客服）。
- [ ] 拍板 `/privacy` 頁面新條款文字（§3 隱私政策更新）。

### 工程（編排者委派 Sonnet 工人）
- [ ] 完成 `GARMIN_DIRECT_SPEC.md`／`COROS_DIRECT_SPEC.md` 的 BACKEND/FRONTEND/VERIFY 工作拆解。
- [ ] 修去重優先序表漂移（`worker/main.go`）。
- [ ] 修 `track/page.tsx` 的 `isTerraHold` 邏輯，涵蓋直連使用者。
- [ ] 補 Garmin／COROS 手動匯入端點。
- [ ] 每日報告「穿戴串接」段落改讀新資料源。
- [ ] Neon 臨時分支完整驗證（含 rollback 情境：先 direct 後改回 terra）。
- [ ] 監控 Phase 4 重新連接率，達標後回報老闆進入 Phase 5/6/7。

## 8. 風險

| 風險 | 影響 | 緩解 |
|---|---|---|
| Garmin／COROS 審核時程未知、可能延遲數週至數月 | 兩案都卡在等待，Terra 訂閱持續付費 | Phase 0/1 期間先把不需 key 的工作做完，核准後能立刻進 Phase 2，不浪費等待時間 |
| COROS refresh-token 回應 schema 假設錯誤（⚠️待確認） | 使用者連線在約 25–30 天後（現行保守 fallback 上限）可能悄悄失效而不自知 | Phase 2 真機測試必須驗證至少一次完整 refresh 週期；上線後靠日報「穿戴串接」段落監控 `last_data_at` |
| COROS 運動類型白名單缺走路類 | 使用者走路活動進不來，比 Terra 既有行為（跑走都收）倒退 | Phase 1 期間補齊，核准後用官方文件核對代碼表 |
| Garmin/COROS 官方 webhook 失敗無重放（若不採用 `integration_events`） | 少量活動漏抓，只能靠使用者手動匯入補救 | 兩案都規劃了落地表＋重試＋dead letter，建議不要為了趕時程而跳過 |
| 公司英文登記名稱、技術聯絡人等申請材料未備妥 | COROS/Garmin 申請被卡在起跑點 | Owner 檢查清單第一項，儘早提供 |
| `track/page.tsx` `isTerraHold` 邏輯未同步更新 | 直連使用者的 GPS 結束後可能與外部來源資料打架、重複上傳 | 列入 §3 依賴盤點與 §7 工程檢查清單，cutover 前必須修 |
| migration 編號協調失誤（Garmin/COROS 兩案平行開發撞號） | 可能重複建表或 migration 套用失敗 | §1／`COROS_DIRECT_SPEC.md §6` 已寫協調規則，開工前核對 `schema_migrations` 現況 |
| Polar/Suunto/Wahoo 決策延宕 | Terra 訂閱無法取消（若選 B），或取消後才發現有隱藏使用者（⚠️即使目前查證 0 筆，仍建議 Phase 7 前再查一次最新數字） | Owner 儘早拍板 §5；Phase 7 前重新查證一次 `user_integrations` 計數 |
| 「Terra→COROS 已於 9/23 完全停止」這個說法查無實據（見 §1 修正）卻被寫進 COROS 申請信/對老闆報告 | 對 COROS 說錯話可能損及申請信任度；對老闆的急迫性判斷可能失真（實際是延遲不穩定，不是斷線） | 送出申請前 owner 或工程在 Terra 後台/COROS App 上親自核一次現況；對外一律用「延遲不穩定」而非「已停止」 |

---

**依據文件**：`docs/integration/GARMIN_DIRECT_SPEC.md`、`docs/integration/COROS_DIRECT_SPEC.md`。
**查證方式**：Neon 正式庫唯讀聚合 SELECT（無個資），透過
`scratchpad/q_serial.py` 模式執行，未印出連線字串。
