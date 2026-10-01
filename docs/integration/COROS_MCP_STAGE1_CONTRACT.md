# COROS MCP 串接 第一階段契約：連接＋讀取測試（v868 起，僅白名單）

使用者決定（2026-10-01）：「現在開始，先只開放我測試」；同意 DOR 向 COROS 登記成應用程式（DCR）。
背景與研究：scratchpad coros_mcp/（COROS_MCP_SPEC_DRAFT.md、A_protocol_spec.md、C_dor_design.md、B_terms_risk.md）、
Downloads\COROS_application\COROS_MCP評估.md。官方：https://github.com/coroslab/COROS-MCP 、support 文章「Build on COROS MCP」。

## 為什麼分兩階段
MCP 工具的參數與回傳欄位（運動類型代碼、距離／時間欄位名、deviceName 在哪）**只有用真實帳號 `tools/list`／實際呼叫才拿得到**。
第一階段只做「連得上、拿得到原始資料」，**不寫入任何活動**；擁有者連線並按「讀取測試」後，依真實資料寫第二階段（對應、匯入、同步、去重）。

## 範圍（第一階段）
### 後端（services/api，新檔 internal/integration/corosmcp*.go，路由 /api/v1/integrations/coros-mcp）
1. **入口閘門**：app_settings `coros_mcp_whitelist`（比照 `gps_raw_log_whitelist` 的格式與驗證，預設只有擁有者帳號；超管不自動放行）。
   dashboard 回 `coros_mcp_entry: 'shown' | 'hidden'`；所有 coros-mcp 端點（callback 除外）不在白名單一律 403。
2. **Discovery**：`GET https://mcp.coros.com/.well-known/openid-configuration`（gateway 可由設定覆寫，預設此值）→ 取 issuer 與各端點。
   **issuer 與所有端點必須是 `https://` 且主機為 `coros.com` 或其子網域**，否則拒絕（防 SSRF／被導去他處）。記憶體快取 6 小時。
3. **DCR（動態註冊，每個 issuer 一次）**：第一次有人連接時，對 `{issuer}/connect/register` 註冊；先要求
   `token_endpoint_auth_method: client_secret_basic`（才能呼叫 revoke），被拒才退回 `none`（公開＋PKCE）。
   body：client_name「DOR」、redirect_uris [`https://www.dor.tw/api/v1/integrations/coros-mcp/callback`（由設定組出，與正式路由一致）]、
   grant_types [authorization_code, refresh_token]、response_types [code]、scope「openid offline_access mcp.tools」。
   結果存 DB `coros_mcp_clients`（issuer 為主鍵；client_secret 以既有 token 加密管線加密；保存回應原文但**移除 secret**）；
   併發以 pg advisory lock 保證只註冊一次。任何 log 不得出現 secret／token。
4. **連接**：`POST /connect`（需登入＋白名單）→ 回 authorize URL：`{authorization_endpoint}?response_type=code&client_id&redirect_uri
   &scope=openid offline_access mcp.tools&code_challenge(S256)&code_challenge_method=S256&resource={issuer}/mcp&state`。
   state＝沿用 coros.go 的 HMAC 簽章 state（userID、到期、issuer、PKCE verifier），10 分鐘到期；不需 DB／Redis。
5. **回呼**：`GET /callback`（公開）→ 驗 state → 換 token（confidential 用 HTTP Basic）→ 存連線（見 6）→ 302 導回前台
   `/?coros_mcp=connected|error`（只導回自家固定路徑，不接受任意 return URL）。
6. **連線儲存**：沿用既有穿戴連線表與 token 加密（C_dor_design.md 指出的 repository），provider 以**獨立值 `coros_mcp`**
   存（不可覆寫或影響現有 provider='coros' 的 Terra／直連列）；另存 issuer、scope、expires_at、connected_at、last_probe_at。
   若既有表結構放不下，新增欄位或新表，寫在同一支 migration。
7. **MCP 用戶端**：無狀態 JSON-RPC over HTTP（POST `{issuer}/mcp`、Authorization Bearer、Accept `application/json, text/event-stream`），
   每次邏輯操作先 `initialize` 再 `tools/list`／`tools/call`；回應支援純 JSON 與 SSE（取最後一個 data 事件）；`isError` 與
   JSON-RPC error 都要處理；401 → 用 refresh token 換新一次再重試（不無限重試）；token 到期前 60 秒主動換新；refresh 回傳的新
   refresh token 一律存回。逾時 20 秒。
8. **讀取測試（probe）**：`POST /probe`（需登入＋白名單，每人每分鐘最多 1 次，Redis 或記憶體限流）→ 依序：`tools/list`（存完整清單含
   inputSchema）、`queryDevices`、`querySportRecords`（最近 14 天；參數依 tools/list 的 schema 組，帶不出就只帶日期）、對最新一筆跑步
   `getActivityDetail` 與 `queryActivityLapData`。**不呼叫 queryUserInfo、不下載 FIT、不呼叫任何寫入類工具**。
   每一步的請求參數與回應原文存 DB `coros_mcp_probe_logs`（user_id、tool、request jsonb、response jsonb、status、created_at），
   保留 30 天（併入每日報告既有的清理流程，不新增排程）。回應給前台：每步成功／失敗＋筆數摘要（不回傳原文）。
9. **狀態**：`GET /status` → connected、issuer、connected_at、last_probe_at、最後一次 probe 摘要。
10. **中斷連線**：`POST /disconnect` → 若為 confidential 先呼叫 revoke（失敗不阻擋）→ 刪除 coros_mcp 連線列（token）。
    **第一階段不寫入活動，所以也不刪任何活動**（絕不可觸碰 provider='coros' 的活動與連線）。
11. **Neon 睡眠**：全部由使用者動作觸發，**不新增任何排程或背景迴圈**。

### Migration（一支，編號接續 repo 最新號；擁有者手動套用，DB 先於程式）
`coros_mcp_clients`、`coros_mcp_probe_logs`、連線所需欄位（若需要）；app_settings 預設 `coros_mcp_whitelist`（擁有者）。
全部 `IF NOT EXISTS`／可重複套用；無破壞性變更。

### 前台（apps/web）
- 只在 dashboard `coros_mcp_entry === 'shown'` 時，於既有穿戴裝置連線區（與 Terra／Strava 卡同處）顯示「COROS 直連（測試版）」卡；
  其他人完全看不到、零請求。
- 連接前顯示同意說明：會讀取哪些資料（跑步／走路活動紀錄、分段、手錶型號）、用途（計算你自己的賽事里程、挑戰與獎勵）、
  保存與刪除（中斷連線即刪除授權；測試期間不會寫入跑步紀錄）、可隨時中斷；勾選同意後才可按「連接 COROS」。
- 卡片狀態：未連接／已連接（連接時間、區域主機）＋「讀取測試」按鈕（顯示每步結果與筆數）＋「中斷連線」。
- 回呼帶 `?coros_mcp=connected|error` 時顯示提示並清掉網址參數。
- 標示「Data provided by COROS」。風格（default/warm/scifi/retro/cute）用既有 CSS 變數，金底白字規則照舊。

## 不在範圍（第二階段再做）
活動對應與寫入、手動「匯入數據」、使用者活動觸發的節流同步、去重／與 Terra 切換、FIT／軌跡、排行榜顯示、型號標示到活動列。

## 驗收
- Go：單元測試（PKCE／state 簽章與到期／issuer 主機驗證／DCR 請求內容與 fallback／JSON 與 SSE 解析／isError／401→refresh 重試一次）；
  以 httptest 假 COROS（discovery＋register＋authorize 導回＋token＋mcp）跑完整「connect→callback→probe→status→disconnect」；
  `go build ./...`、`go vet`、相關 `go test` 通過（本機 App Control 擋時依 memory windows-appcontrol-go-test 處理）。
- Migration：在 Neon **暫時分支**套用兩次（可重複）成功，查表結構正確，之後刪除分支並再列一次確認已刪；不得動正式分支。
- 前台：Playwright（mock API、DPR3 390×844）—白名單才看得到卡、同意前不能連接、各狀態與按鈕正確、預設帳號零差異、五種風格可讀；
  tsc、next build 通過；0 console error。
- 安全審查：state CSRF／重放、PKCE、redirect URI 固定、不接受任意導回、issuer 主機限制、secret／token 不入 log 與回應、白名單 403、
  不影響既有 provider='coros' 資料。
- **任何代理都不得連線到真正的 COROS**（含 discovery、註冊）；真實註冊只在正式站擁有者第一次按「連接」時發生。

## 第二階段開放給所有人之前必做（第一階段審查記錄，2026-10-01）
- **OAuth 登入 CSRF**：/callback 是公開路由、只靠簽章 state 綁定 DOR 使用者（與既有 Strava／COROS 直連相同）。若開放給所有人，
  攻擊者可用自己的 DOR 帳號產生授權網址，誘騙受害者在 COROS 登入授權，把受害者的 COROS 資料綁到攻擊者帳號。第一階段只有白名單
  （擁有者）能產生授權網址，不受影響；開放前要在 /connect 回應時種一個短效 httpOnly cookie（隨機 nonce，同時簽進 state），
  /callback 比對一致才接受。
- state 目前以 JWT_SECRET 做 HMAC；開放前改用衍生金鑰（例如 HMAC(JWT_SECRET, "coros-mcp-state")）做用途隔離。

## 正式站第一次連線實測修正（v869，2026-10-01）
- 症狀：擁有者按「連接 COROS」→ COROS 登入授權成功 → 導回顯示「連接未完成（token_exchange_failed）」；Railway log 兩次
  `coros mcp token http 400`。
- 根因（唯讀查 coros_mcp_clients＋log 證實）：DCR 要求 `client_secret_basic`，COROS 仍回 200 並登記成
  `token_endpoint_auth_method: "none"`、不發 client_secret（與官方 skill 的 public＋PKCE 一致）。v868 存的是「要求的」方法，
  換 token 時送出「空密碼的 Basic 標頭」，COROS（Spring Authorization Server）以 400 invalid_request 拒絕。
- 修正：一律以 COROS 實際登記的方法為準（`corosMcpEffectiveAuthMethod`：沒有 secret 就是 public）；註冊直接要求 none；
  只有真的有 secret 才送 Basic；舊列不需改資料庫就地校正。token／DCR／revoke 錯誤改記 OAuth `error`／`error_description`
  （不含 token），callback 錯誤帶出代碼（例 `token_exchange_failed:invalid_grant`）。
- 一併處理：Spring AS 預設**不發 refresh token 給 public client** → 不再因缺 refresh token 判連線失敗；access token 到期且無
  refresh token → 讀取測試回 409 `reconnect_required`，前台提示重新連接；refresh 沒輪替就沿用舊值。public client 的 revoke
  多半被拒（metadata 不含 none）→ 照樣刪除本機授權，前台文案改成「DOR 已刪除保存的授權、不再讀取」，不再宣稱「撤銷授權」。
- 測試：假 COROS 改成照正式站實際行為（public-only DCR、Basic 一律 400、缺 client_id 401、revoke 拒 public），新增「從正式站
  那筆錯誤列出發」與「不發 refresh token」兩支資料庫整合測試；整合測試首次在 Neon 暫時分支實跑 4/4 通過（分支已刪除）。
  本機注意：這台開發機的執行沙盒會改寫 127.0.0.1 的 HTTP 回應（伺服器寫 Content-Length，客戶端收到變 chunked 且未分段 →
  讀 body 逾時），整合測試的假 COROS 改走 in-process transport（scratchpad keepalive_repro 以伺服器端 tee 對照證實）。
