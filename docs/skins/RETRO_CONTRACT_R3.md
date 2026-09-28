# 復古 RPG 第三輪（使用者回饋）— 2026-09-28

前提：v1.2.854 已上線（風格設定、retro 主題、RetroMap、小井勇者）。CONTRACT.md／R2 原則沿用（非授權帳號零影響、跑步邏輯零改動、原創美術不用商業遊戲素材、z-index 與 .phone-shell 慣例）。

## 0. 使用者原話
1.「『預設風格、未來科技、復古 RPG』前方不需要有一個小 ICON，另外這三個選項請收斂在『風格設定』的按鈕中，點擊後才會出現風格選單讓使用者設定，後台開關這個『風格設定』的按鈕顯示。」
2.「在大地圖中的小井，圖像有點過大，會遮蓋住很大部分的地圖，請進行優化。然後小井的 left、right 圖，看起來只是比較像是頭髮遮住眼睛，不像 left、right 角度的圖。」
3.「整體 UI 的邊框都太粗了，我想要的 RPG 像素風在目前的 GPS 地圖上很不錯，其他的部分可以參考附圖進行調整。」附圖（《勇者鬥惡龍》系列選單畫面，**只取風格語彙、不得複製任何素材**）：羊皮紙米黃底選單、深咖啡色緞帶標題（兩端箭頭形、細金邊）、細褐色橫線分隔的清單、深褐文字、重要名稱用金黃字加深色描邊、清單左側小短劍游標、底部「◀ 1/3 ▶」金色翻頁、另一張為深色皮革／石板面板配細金框。

## 1. 風格設定改為按鈕＋選單（ProfileScreen 個人資料分頁）
- 個人資料分頁只顯示一列「風格設定」按鈕（右側顯示目前風格名稱＋›），**只在 `dash.skin_select_entry==='shown'` 時出現**（後台「系統設定」的 `skin_select_entry_state`：hidden＝隱藏按鈕、whitelist／vip／open＝顯示給對應對象；把 lib/appSettings.ts 這兩個鍵的中文 label 改清楚：「風格設定按鈕顯示對象（會員管理→個人資料）」與「風格設定白名單」）。
- 點按鈕開啟選單（彈窗，走既有 overlayMount／.phone-shell 慣例，z 2000–3300 區間）：標題「風格設定」、三個純文字選項「預設風格」「未來科技」「復古 RPG」，**不放任何圖示／emoji**；每列一行簡短說明（小字）；目前選中者以右側勾選「✓」或左側游標標示；點選即套用（沿用既有樂觀更新＋PUT /me/ui-skin＋失敗回滾）並關閉選單；有「關閉」按鈕（寬版）。選單在三種 skin 下都要可讀（用 token）。
- 移除個人資料分頁上原本的三張卡片。

## 2. 小井在大地圖的尺寸與側面圖（app/track/retro/）
- 尺寸：勇者繪製倍率由 3 改 **2**（16×24 → 32×48 CSS px），腳印與足跡線寬同比例縮小；`heroScreen` 仍在可見區中心。
- 左側面圖以下 16×24 為**權威底稿**（編排者繪製；right＝水平鏡像）。圖例同 R2：`.`透明 `K`#000 `P`#fc7aa4 `p`#c8406c `S`#fcd8a8 `E`#ac7c00(眼) `F`#fc7aa4(腮紅/鞋) `R`#e4462c `G`#f8b800 `W`#fcfcfc `A`#bcbcbc `B`#ac7c00(背帶) `N`#24188c `w`#fcfcfc(襪)。重點：臉在左、瀏海在前、**單片鏡框內看得到眼睛（E）**、鏡腳延伸到耳、鼻尖（r10 c0）、腮紅、小嘴、胸前金色胸針、圍巾尾端與劍護手在背側（右）。
```
r00 ....KKKKKKK.....
r01 ..KKPPPPPPPKK...
r02 .KPPPPPPPPPPPK..
r03 .KPPPPPPPPPPPPK.
r04 KPPPPPPPPPPPPPK.
r05 KPPPPPPPPPPPPpK.
r06 KPPPPPPPPPPPPpK.
r07 KPPPPpPPPPPPPpK.
r08 .KKKKKKKKPPPPpK.
r09 KSKEKSSSSKPPPpK.
r10 SSKKKSSSSSKPPpK.
r11 KSSSFSSSSSKPPpK.
r12 .KKSSSSSSKPPPpK.
r13 ..KRGRRRRRKPpK..
r14 ..KRRRRRRRRRRK..
r15 ...KAAWWWWRRRK..
r16 ...KAWWBWWWAGK..
r17 ...KSWWWBWWWAK..
r18 ...KSWWGGGWWWK..
r19 ...KNNNNNNNNNK..
r20 ..KNNNNNNNNNNNK.
r21 ....KwK..KwK....
r22 ...KFFK..KFFK...
r23 ...KKKK..KKKK...
```
  - 步行兩幀：前腳（左）與後腳（右）交替前後移 1px、抬腳者鞋子上移 1px；前手（r16–r18 的 S）前後擺 1px；圍巾尾端（r14–r15 右側 R）上下擺 1px。
  - 正面（down）與背面（up）維持 v854（R2 底稿）不變。
- 重新產出 `C:\Users\paris\Downloads\retro_preview\7_hero_sheet.png`（程式呼叫 drawHero 產生，4 方向 × 3 幀，8 倍）。

## 3. 其他 UI 改為「羊皮紙＋皮革」RPG 選單風（GPS 地圖本身不改）
- **邊框全部變細**：取消 v854 的白色 3px 雙框。原則：外框 1px 深褐 `#7a4a22`＋內側 1px 淺色描線（inset）形成細雙線；圓角 6px；外陰影 `0 2px 0 rgba(40,20,5,.35)`。
- 色盤（只在 `[data-skin="retro"]`）：羊皮紙 `--bg-1: #ecd9ae`（面板以 `linear-gradient(180deg,#f3e5c3,#e3c995)` 呈現，可加極淡 radial 斑點質感，純 CSS）、`--bg-2: #dcc28f`、`--line: #b98a4f`、`--line-2: #8f5f2e`、文字 `--tx: #3b2412`、`--tx-dim: #6b4a2a`、`--tx-faint: #8f6f48`、主色 `--fug: #6e3a1a`（深咖啡）／`--fug-ink: #fff3d6`、`--hunt: #a8321e`、`--gold: #d99a1a`；頁面底層維持 RetroBackground 像素草原（暗化 .5）透出（跑步頁仍以 :has(#gps-map) 不透明）。
- **標題緞帶**：卡片／彈窗／頁面標題用深咖啡 `#5a2f16` 緞帶、1px `#d9a441` 金邊、兩端箭頭形（clip-path），字色 `#fff3d6`；只在 retro 下以既有 className 或 `[data-skin="retro"] h2` 等選擇器套用，不改元件結構為原則（真的需要時在元件加 `className` 屬性，不改其他 skin 的外觀）。
- **清單**：列與列之間 1px `#c9a36b` 細橫線；重要名稱／數值可用金黃 `#f0c040` 加深色 1px 描邊（`text-shadow`）；hover／focus 列左側顯示原創小短劍游標（CSS 內嵌 SVG data URI，簡單劍形，**不得描摹參考圖**），取代 v854 的 ▶。
- **按鈕**：主按鈕（含 `.skin-btn-start`）＝深咖啡緞帶底 `#5a2f16`、1px 金邊、米白字；`.skin-btn-end`＝深紅 `#7a1e12`＋1px 金邊；次要按鈕＝羊皮紙底＋1px 深褐框。
- **跑步頁面板與統計卡**：面板＝羊皮紙；統計卡＝淺羊皮紙內框（1px 細線）；數字深褐。**專注模式狀態視窗**＝深色皮革面板 `#2a1a10`（對應附圖二右側）＋1px `#d9a441` 細金框＋內側 1px 暗金線，數字米白，標題緞帶「冒險中」；上 45% 透出地圖不變。
- 字體：維持 Cubic 11 像素字（復古識別）；羊皮紙上字色用深褐確保對比 ≥ 7:1。
- 事件任務面板維持 `data-skin="default"`；非 retro skin 完全不受影響。

## 4. 驗收
- `tsc` 0、`verify-active-run` 全過、`next build` 54 頁。
- Playwright（prod＋swiftshader，沿用 ${DIR}\r2\verifye2e\verify_r2.mjs 的 stub 與餵點）：
  1. 個人資料只有一顆「風格設定」按鈕（無卡片、無圖示），點開選單三個純文字選項、選 retro／scifi／default 各自套用並關閉選單、PUT 參數正確；`skin_select_entry` 為 hidden 時按鈕不存在。
  2. 勇者在地圖上的包圍盒 ≈ 32×48 CSS px（±2），仍在可見區中心（誤差 ≤ 2px）。
  3. 側面圖逐像素符合 §2 底稿（從 drawHero 直接取 left 站立幀比對 384 個像素，100% 一致；right 為鏡像）。
  4. 邊框量測：首頁主卡與入口格的外框寬度（computed border／box-shadow 解析）≤ 2px；羊皮紙背景色取樣落在 #e0c38f–#f5e8c8 範圍；主要文字對比 ≥ 7:1。
  5. 截圖覆蓋 `C:\Users\paris\Downloads\retro_preview\`：1_home、2_profile（顯示風格設定按鈕）、8_style_picker（選單打開）、3_track_idle、4_track_running、5_focus、6_track_1280、7_hero_sheet。
  6. 迴歸：scifi 與預設風格外觀不變（scifi __scifiDebug 正常、預設風格 data-skin 為 SSR 值）；非白名單 0 個 retro 資源請求；WebGL 不可用退回；四視窗 0 console error；跑步面板不透明（面板區透出像素 ≤ 3%）。

## 5. 使用者追加（2026-09-28）：「整體來說就是復古 RPG 風格的 UI 可以更細緻」
在 §3 的基礎上提高細節密度（全部純 CSS／內嵌原創 SVG data URI，只在 `[data-skin="retro"]` 下）：
- **紙張質感**：面板底色以 2–3 層極淡 radial-gradient 斑點＋細微纖維紋（repeating-linear-gradient 0.5px、透明度 ≤ .05）呈現羊皮紙，不用圖片檔；面板邊緣內側 6–10px 較深的焦邊暈（inset box-shadow 大半徑低透明）。
- **撕邊**：彈窗與主要大面板的上下緣用 clip-path 或 mask 做不規則細碎撕邊（原創多邊形，起伏 2–4px），一般小卡片維持圓角細框即可。
- **四角飾件**：主要面板四角各一個 10–12px 的細金色 L 形角飾（SVG data URI，1px 線＋一個小菱形），以 background 多層定位實作，不增加 DOM。
- **分隔線**：區塊分隔用「細線＋中央小菱形」裝飾線（`::before/::after` 或 background），清單列用 1px 淡褐細線、奇偶列極淡底色差。
- **緞帶標題**：兩端燕尾或箭頭缺口、上下各 1px 金邊＋內側 1px 暗色描線、字距 .08em；緞帶下方極淡投影。
- **按鈕**：上緣 1px 亮色、下緣 1px 暗色的細斜面（bevel）、按下時下沉 1px 並互換亮暗；focus 用 1px 金色外框；小短劍游標在 hover／focus 時出現並有 2px 左右來回的輕微動畫（reduced-motion 關閉）。
- **數字與重點字**：金黃字＋1px 深色描邊（多向 text-shadow）僅用於標題／重點數值；內文維持深褐。
- **間距與層次**：面板內距 ≥ 14px、區塊間距一致（8／12／16 系統）、標題與內文層級清楚。
- 驗收追加：編排者目視截圖時以「接近附圖的精緻 RPG 選單」為標準；工人自評需列出上述每一項的實作位置（選擇器）與截圖佐證。

## 6. 編排者追加（檢視 r3/theme 截圖後）— 必修
- **全頁畫面的底色**：`--bg: transparent` 讓所有全頁畫面（會員管理 ProfileScreen、活動探索、賽事詳細、訓練、充電站、各種 *Screen）的內容直接疊在暗綠像素草原上，**會員管理個人資料分頁的分頁標籤、欄位標題、帳號／會員身分等文字幾乎不可讀**。改為：retro 下 `--bg` ＝ 羊皮紙頁面底（比卡片 `--bg-1` 略深一階的 `#e2c996` 系，含同樣的淡紋理，純 CSS），**只有首頁（PhoneShell 主畫面，入口格那一頁）維持透明透出像素草原**——以該首頁容器的既有 className／屬性或 `:has()` 精準選取（實作者先找出首頁根容器的可辨識點；若沒有，允許在 PhoneShell 首頁根容器加一個 `data-screen="home"` 屬性，不改其他行為）。跑步頁 `:has(#gps-map)` 與專注模式黑底規則照舊。
- 全頁畫面標題列（← 返回＋標題）在 retro 下改為深咖啡緞帶底條（1px 金邊、米白字），分頁標籤（個人資料／運動數據…）改為羊皮紙頁籤：選中者深咖啡底米白字、未選中者淺羊皮紙底深褐字、1px 細框。
- 驗收：會員管理個人資料分頁截圖中，欄位標題／分頁標籤文字與其背景對比 ≥ 7:1（取樣計算）；首頁卡片間仍透出像素草原（非黑綠色比例 ≥ 25%）。
