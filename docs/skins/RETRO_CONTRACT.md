# 復古 RPG 風格＋帳號層級「風格設定」 契約 v1 — 2026-09-27

## 0. 使用者需求（原話）
「持續新增風格版本：傳統 RPG 版本。請把風格切換的功能讓帳號在『會員管理→個人資料』新增『風格設定』，未來這個功能只提供給 VIP 帳戶可以切換，現在只提供給 sogobaga@gmail.com 這個帳號可以切換。除了之前的版本（預設風格），加上『未來科技』風格，現在多一個『復古 RPG』。」參考圖：8-bit／16-bit 日式 RPG 的俯視大地圖（草地點描、森林、山脈、河流、城堡、石牆城鎮、紅色石板路、黑底白框對話視窗）。

## 1. 最高原則（沿用 docs/skins/SCIFI_CONTRACT.md §1，全部適用）
- 非白名單／非授權帳號零影響（HTML、CSS 生效規則、JS bundle、網路請求、Leaflet 行為不變）；跑步邏輯零改動；地圖壞掉一律退回 Leaflet。
- **原創美術（硬性）**：參考圖是 SQUARE ENIX《勇者鬥惡龍》，**不得使用、描摹或仿製任何其角色、怪物（含史萊姆造型）、圖塊、LOGO、字體或 UI 素材**。全部圖塊與角色以程式逐像素繪製（原創設計），只借用「8-bit JRPG 俯視大地圖」這個類型語彙（點描草地、樹林、山、水波、石牆、石板路、黑底白框視窗）。
- 既有 z-index／`.phone-shell`／`.app-min-h`／禁用 scrollIntoView 規則沿用。

## 2. 帳號層級風格設定（取代 v853 的 scifi 開關）
### 2.1 後端
- migration **193** `users.ui_skin TEXT NULL`，CHECK (ui_skin IS NULL OR ui_skin IN ('default','scifi','retro'))（DO $$ 判斷約束存在才加，可重複執行）；資料延續：`UPDATE users SET ui_skin='scifi' WHERE lower(email)='sogobaga@gmail.com' AND ui_skin IS NULL`；`INSERT INTO schema_migrations ('193') ON CONFLICT DO NOTHING`。（Garmin 直連規格草案原本寫的 193 改為 194，順手更新 docs/integration/GARMIN_DIRECT_SPEC.md 相關字樣。）
- 權限解析 `resolveSkinSelectEntry`（internal/profile，純函式＋包裝）：app_settings `skin_select_entry_state` ∈ hidden|whitelist|vip|open（**缺鍵預設 whitelist**；vip＝VIP 有效期內或白名單命中；open＝所有登入者）、`skin_select_entry_whitelist`（缺鍵預設 `sogobaga@gmail.com`）；**無 super_admin 旁路**。兩鍵註冊到 appsettings specs 與前台 lib/appSettings.ts。
- `GET /me/dashboard` 新增：`skin_select_entry: 'hidden'|'shown'`、`skin_options: string[]`（shown 時 `['default','scifi','retro']`，否則 `[]`）、`ui_skin: 'default'|'scifi'|'retro'|null`（**未授權一律回 null**，即使 DB 有值）。保留 `scifi_entry`（＝ `skin_select_entry` 且 ui_skin==='scifi' 時才 'shown'，供舊 bundle 相容）。
- `PUT /me/ui-skin {skin}`：未授權 403 `skin_not_allowed`；值不在 skin_options → 400 `invalid_skin`；成功 200 回 `{ui_skin}`；只寫本人。
- 舊鍵 `scifi_entry_state`／`scifi_entry_whitelist`：程式不再讀（保留在 specs 但標註 deprecated，或移除——實作者擇一並說明）。

### 2.2 前端
- `lib/skinOverride.ts` 一般化：`type OverrideSkin = 'scifi'|'retro'`、`getActiveSkin(): OverrideSkin|null`（讀 `document.documentElement.dataset.skin`）、`SKIN_CHANGE_EVENT` 不變；保留 `isSciFiActive()`（＝getActiveSkin()==='scifi'）。localStorage 覆寫記錄改 `dor_skin_override={uid, skin}`（skin 可為 scifi/retro），開機腳本一般化；舊 `dor_skin_pref` 不再使用（讀到就刪）。
- `SkinOverride.tsx`：依 `dash.ui_skin`（且 `skin_select_entry==='shown'`）套用：scifi → data-skin=scifi＋ParticleField；retro → data-skin=retro＋`<RetroBackground/>`（default export，from '@/components/retro/RetroBackground'）；default／null → 恢復 SSR 原值。theme-color：scifi #02040a、retro #000000。
- **會員管理 → 個人資料（tab 'info'）新增「風格設定」區塊**（只在 `skin_select_entry==='shown'` 顯示）：三張可選卡片（預設風格／未來科技／復古 RPG），每張有小色塊預覽與一句說明；目前選中者高亮；點選 → 樂觀更新立即套用 → `PUT /me/ui-skin` → 失敗回滾並提示。**移除 v853 放在運動數據分頁的「🌌 未來科幻世界」開關**。區塊下方小字：「此功能目前開放測試帳號，未來將提供 VIP 會員使用」。

## 3. 復古 RPG 全站主題（`[data-skin="retro"]`，只寫在 globals.css 與 components/retro/）
- 視窗語彙：主要卡片／彈窗＝黑底 `#000` ＋ 白色 3px 實線框（`box-shadow: 0 0 0 3px #fff, 0 0 0 6px #000` 形成雙框）＋ 小圓角 4px；文字白 `#fff`；次要文字 `#c8c8c8`；強調金 `#f8b800`；HP 綠 `#3cbc3c`；警示紅 `#e4462c`；按鈕＝同視窗樣式，hover/focus 左側出現 `▶` 游標（`::before`）。
- token：`--bg:#000` `--bg-1:#000` `--bg-2:#1b1b1b` `--line:#fff` `--line-2:#fff` `--tx:#fff` `--tx-dim:#c8c8c8` `--tx-faint:#8a8a8a` `--fug:#3cbc3c`（主色綠）`--fug-ink:#000` `--hunt:#e4462c` `--gold:#f8b800` `--card-shadow: 0 0 0 3px #fff`；結構 token 圓角縮小（4px）。`.skin-btn-start`／`.skin-btn-end` 在 retro 下改黑框白字＋左側 ▶（純 CSS，不用 warm2 圖片）。
- 字體：像素中文字 **俐方體 11 號 Cubic 11**（SIL OFL 1.1，https://github.com/ACh-K/Cubic-11 `fonts/web/Cubic_11.woff2` 約 400KB）放 `apps/web/public/fonts/cubic11/Cubic_11.woff2`＋`OFL.txt`；**只在 retro 生效時以 FontFace API 載入**（components/retro/fonts.ts，由 RetroBackground 掛載時呼叫），不在全站 CSS 宣告 @font-face；`[data-skin="retro"]` 的 font-family 以 `'DORPixel', monospace` 為首；數字 `font-variant-numeric: tabular-nums`；整體 `-webkit-font-smoothing: none`。
- 背景 `components/retro/RetroBackground.tsx`：以 canvas 在低解析度（每格 16px 原創圖塊：點描草地為主，零星樹叢與小河，河面兩格動畫 500ms 切換）鋪滿，`image-rendering: pixelated` 放大，置於內容下方 z 0、pointer-events none、隱藏時停止、reduced-motion 靜態；整體亮度壓低（上蓋 rgba(0,0,0,.45)）以免搶走視窗文字。
- 事件任務面板維持 `data-skin="default"`。專注時隱藏頁面 chrome：`[data-skin="retro"] [data-scifi-focus-hide="true"] { visibility:hidden }`。

## 4. 復古 RPG GPS 地圖（`app/track/retro/`，next/dynamic 只在 retro 載入）
- **架構比照 SciFiMap**（實例隔離、fallback、`types.ts` 同一組 props、imperative handle recenter/zoomBy、同源 `public/vendor/maplibre-gl-6.11.2` + `config.WORKER_URL`、OpenFreeMap 向量圖磚、CSP 已涵蓋）；`window.__retroDebug = {center, zoom, pitch, bearing, lastPos, tilesRequested, tilesLoaded, heroScreen:{x,y}, heroDir, fps}`。
- **像素化**：`pitch 0`、`bearing 0`（經典俯視）、zoom 16.5；MapLibre `pixelRatio` 設低（目標：1 個地圖像素 ≈ 3 CSS px；以 `devicePixelRatio` 換算）＋ canvas `image-rendering: pixelated`；**不顯示任何文字標籤**（大地圖沒有字）。
- **原創圖塊（程式繪製 16×16，`map.addImage` 後以 `fill-pattern`／`line-pattern` 使用）**：草地點描（背景＋綠地）、森林（公園／樹林 landcover／wood）、水波（水域，兩幀動畫：定時 `updateImage` 切換）、石牆城鎮（建築：灰色磚牆＋黑色描邊 `fill-outline-color`）、紅色石板路（主幹道 line-pattern 或寬 line＋石板色）、土黃小徑（一般道路／步道）、沙地（沙灘／操場）、鐵道（灰黑枕木）。調色盤限定 NES 風約 16 色：#000 #fff #3cbc3c #00a800 #005800 #80d010 #0078f8 #3cbcfc #a4e4fc #b8b8f8 #f8b800 #ac7c00 #c84c0c #e45c10 #a81000 #7c7c7c。
- **勇者（原創）**：16×16 像素小冒險者（原創造型：短披風＋頭帶＋背包，配色以 DOR 綠／白為主，**不可近似任何既有遊戲角色**），四方向 × 兩幀步行動畫（依行進方向決定面向；速度 >0.8 m/s 才走路動畫，否則站立）；畫在 canvas 疊層、固定於畫面中心（地圖跟隨勇者，RPG 鏡頭）；身後保留最近 12 個位置的像素腳印（每 6 m 一個，漸隱）。
- 路線：已跑路線以 2 格寬虛線「足跡道路」呈現（金色 #f8b800 點列），排除段不畫；每公里標記＝小旗子像素圖示＋「1」「2」像素數字。
- 目標點（關主／打卡點）：原創像素城堡／塔／寶箱圖示（未完成＝關閉寶箱或城堡、完成＝打開寶箱或旗子），半徑以點狀虛線圓圈表示。
- 光效：無粒子；只有水波兩幀動畫與勇者步行動畫；30fps 上限，專注模式 8fps，隱藏停止。
- 失敗退回 Leaflet 與跑步零影響同 scifi。

## 5. 專注模式（retro 變體）
- RaceFocusMode 在 retro 下：背景上→下漸層（上 45% 透出大地圖與勇者，下半 `#000`）；數字區改為 RPG 狀態視窗（黑底白雙框）列出「距離／時間／配速／分段配速」，標題列寫「冒險中」或「比賽專注模式・名稱」；鎖頭改像素鎖頭圖示＋「長按 1.5 秒解除」；攔截觸控與長按行為不變。

## 6. 驗證
- Go：go build/vet；`resolveSkinSelectEntry` 表格測試（缺鍵預設、whitelist、vip（VIP 有效／過期）、open、hidden、大小寫、無 super_admin 旁路）；PUT /me/ui-skin handler 測試（403／400／200）；dashboard 未授權時 ui_skin 回 null。
- Neon 臨時分支：193 套用＋再套冪等、sogobaga 列 ui_skin=scifi、CHECK 擋非法值；本機 api：白名單帳號 PUT retro→dashboard ui_skin=retro、非白名單 PUT → 403、非法值 400。
- Web：tsc 0、verify-active-run 全過、next build 54 頁；bundle 隔離（/track 首屏不含 maplibre／retro 模組；root layout CSS 不含 Cubic @font-face）。
- Playwright（production build＋swiftshader；dashboard／ui-skin API 以 route 攔截，GPS 沿大安森林公園周邊真實道路每秒一點 3.2 m/s 餵 60 秒）：
  1. 個人資料分頁「風格設定」三選一：選 retro → 立即變 retro、PUT 帶 {skin:'retro'}；reload 後維持（開機腳本無閃）；選 default → 恢復 SSR 原值；運動數據分頁已無 🌌 開關。
  2. retro 首頁、個人資料、track idle、track 跑步中、專注模式截圖（存 `C:\Users\paris\Downloads\retro_preview\` 1_home…5_focus，另 6_track_1280）。
  3. `__retroDebug`：zoom∈[15.5,17.5]、pitch==0、bearing==0、tilesLoaded≥10、center 與最後一點 ≤150 m、heroDir 與行進方向一致；地圖區像素檢驗：調色盤內顏色占比 ≥ 90%（證明是限色像素風）、綠色系占比 ≥ 25%、勇者位置 16×16×3 範圍內非背景像素 ≥ 60。
  4. 非白名單帳號：無風格設定區塊、data-skin 不變、0 openfreemap／maplibre／Cubic 請求、Leaflet 正常。
  5. scifi 迴歸：選未來科技 → v853 行為照舊（__scifiDebug 正常）。
  6. WebGL 不支援 → retro 退回 Leaflet 且跑步正常；四視窗無溢出、0 新 console error；next dev（StrictMode）不退回。
- 唯讀審查：隔離、原創美術（程式繪製、無外部遊戲素材）、字體授權檔隨附、PUT 權限、實例隔離與清理、效能、無殘留 console.log。

## 7. 補充（2026-09-27 使用者追加）：勇者＝小井（DOR 自有角色）
- 使用者原話：「勇者的圖像，請參考小井 PM 的造型來製作。」小井是 DOR 自有角色（DORPG `char_xiaojing`，輕騎士、is_player_portrait），參考圖：`apps/web/public/ui/dorpg/char/char_xiaojing_512.webp`（頭像：粉紅鮑伯短髮、黑色方框眼鏡、紅色圍巾＋金色圓形胸針、銀白鎧甲金邊、咖啡色皮帶）與 `source/data/pictures/cheerleading/DOR-Runner-Cheer-01-XiaoJing-2048x4096-Clean-v2.png`（全身：白底粉袖 DOR 球衣、深藍百褶裙、白襪、粉紅跑鞋）。§1 的「原創、不得近似既有遊戲角色」改為：**勇者＝小井的像素版**（自有 IP，可直接參考）。
- 像素規格：**16 寬 × 24 高**（比 16×16 多出頭身比，才放得下眼鏡與圍巾），以 3 倍放大顯示；四方向（下／上／左／右）× 兩幀步行＋一幀站立。
- 造型要點（每一方向都要辨識得出）：粉紅鮑伯頭（亮 #fc7aa4、暗 #c8406c，瀏海蓋額、兩側到下巴）；**黑色方框眼鏡**（正面＝眼睛位置一條 1px 黑線橫跨＋兩個鏡框角；側面＝1px 黑線＋鏡腳）；膚色 #fcd8a8；**紅色圍巾**（#e4462c，胸前一格 **金色胸針** #f8b800；移動時圍巾末端往行進反方向飄 1–2px 兩幀擺動）；上身銀白鎧甲（#fcfcfc／#bcbcbc，陰影 #7c7c7c，肩甲與胸線金邊 #f8b800）；咖啡色斜背帶 #ac7c00；下身深藍裙 #24188c；粉紅跑鞋 #fc7aa4＋白襪；背上露出劍柄（灰 #bcbcbc＋金護手 #f8b800）；背面看得到後腦粉紅髮與圍巾結。
- 調色盤 `RETRO_PALETTE` 追加：#fc7aa4、#c8406c、#fcd8a8、#24188c、#fcfcfc、#bcbcbc（像素檢驗「調色盤內顏色占比」一併納入）。
- 驗收追加：以 8 倍放大另存一張勇者四方向站立＋步行幀的 sprite sheet 截圖 `C:\Users\paris\Downloads\retro_preview\7_hero_sheet.png`，編排者會逐格檢視粉紅髮、眼鏡、紅圍巾＋金胸針、銀甲是否可辨識。
