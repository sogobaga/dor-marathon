# 「溫馨可愛」風格 契約 v1 — 2026-09-28

## 0. 使用者需求
「請再設計一版『溫馨可愛』風格。」附圖為《吉伊卡哇 Chiikawa》（© nagano / chiikawa committee）官方插畫與商品。
**版權硬性規定**：不得使用、描摹、仿製任何吉伊卡哇角色（白色圓滾倉鼠、兔兔、小八貓等造型、臉部五官組合、招牌嘴型）、CHIIKAWA 字樣、商品排版。只借用通用的「手繪可愛」風格語彙：粉彩平塗、圓潤造型、深色手繪描邊、腮紅斜線、紙膠帶／貼紙白邊、彩色紙屑與星星雲朵點綴、圓體手寫感字。角色一律用 DOR 自有的**小井**（Q 版化）與原創小圖示。

## 1. 最高原則（沿用 docs/skins/SCIFI_CONTRACT.md、RETRO_CONTRACT*.md）
非授權帳號零影響（HTML／生效 CSS／JS bundle／網路請求／Leaflet 行為不變）、跑步邏輯零改動、地圖壞掉退回 Leaflet、z-index 與 .phone-shell／.app-min-h 慣例、事件任務面板維持 data-skin="default"、硬寫死的 skin 規則必須加 `:not([data-skin="default"] *)`、`--bg-1` 若為多層背景就不可被當純色用。使用者偏好：邊框不要粗、要細緻；卡片內動作鈕寬版或置右。

## 2. 風格設定擴充（後端＋migration）
- migration **194** `users_ui_skin_check` 改為允許 `('default','scifi','retro','cute')`（DROP CONSTRAINT IF EXISTS 再 ADD，可重複執行；schema_migrations '194'）。docs/integration/GARMIN_DIRECT_SPEC.md 內 194 字樣改 195。
- 後端 `isValidUiSkin` 加 cute、`skin_options` 回 `['default','scifi','retro','cute']`；Go 測試同步。
- 前端：`OverrideSkin` 加 'cute'、SkinOverride 掛 `<CuteBackground/>`（default export，'@/components/cute/CuteBackground'）、開機腳本允許 cute、theme-color `#fff3d6`；StyleSettingsModal 加第四個純文字選項「溫馨可愛」＋說明「粉彩手繪風、圓體字，小井陪你散步跑」。

## 3. 溫馨可愛主題（只在 `[data-skin="cute"]`）
- 色盤：奶油底 `#fff8ec`、奶油黃 `#ffe7a3`、糖果粉 `#ff9fc6`、桃紅 `#ec6fae`、天空藍 `#a9dcf5`、薄荷 `#bfeede`、深可可墨線 `#3d2b2b`、腮紅 `#ffb3c7`。token：`--bg`（頁面底：奶油黃或淡粉＋散落的原創彩色紙屑／小星星／小雲朵，純 CSS 或 CuteBackground canvas 產生）、`--bg-1: #fffdf8`（白色卡片）、`--bg-2: #fff1e0`、`--line: #3d2b2b`、`--tx: #3d2b2b`、`--tx-dim: #7a5c52`、`--tx-faint: #9b7d72`、`--fug: #ec6fae`（主色桃紅）／`--fug-ink: #fff`、`--hunt: #e8575a`、`--gold: #f6b73c`。文字對比 ≥ 7:1。
- 卡片：圓角 20px、**2px 深可可墨線**（細而清楚，不要更粗）、外側 3px 白色「貼紙白邊」（box-shadow 疊層）、下方 3px 柔和偏移陰影 `rgba(61,43,43,.18)`；重要卡片左上可貼一小段原創紙膠帶（CSS 斜紋小矩形，`::before`）。
- 按鈕：膠囊形（圓角 999px）、2px 墨線、主按鈕桃紅底白字、次要白底墨字；按下 `translateY(1px)`＋陰影縮小；focus 2px 天空藍外框。`.skin-btn-start`＝桃紅膠囊＋左側原創小愛心；`.skin-btn-end`＝珊瑚紅膠囊。
- 標題：圓體字＋底部一條手繪感波浪底線（SVG data URI）；分頁標籤＝膠囊，選中桃紅底白字。
- 字體：圓體中文 **jf open 粉圓（jf open huninn 2.1，SIL OFL 1.1，https://github.com/justfont/open-huninn-font）**。依 OFL 規定：子集化屬修改，**不得沿用保留字型名稱 "open huninn"／"huninn"**——以 fonttools 子集化（ASCII＋常用繁體中文約 5000 字＋ apps/web/src 內出現的所有中文字）、轉 woff2、把 name table 的 family 改為 `DORCute`，連同原 LICENSE（OFL＋Kosugi Maru Apache-2.0 聲明）與一份說明來源的 README 放 `apps/web/public/fonts/dorcute/`；**只在 cute 生效時以 FontFace API 載入**（同 retro 做法）；子集外字元退回系統字。回報檔案大小（目標 ≤ 2.5MB）。
- 背景 `components/cute/CuteBackground.tsx`：原創彩色紙屑（小方塊、彎曲線條）、五角小星星、蓬鬆小雲朵，緩慢飄動（≤ 60 個、30fps 上限、隱藏停止、reduced-motion 靜態），首頁透出、其他全頁畫面用 `--bg` 素色奶油底＋極淡點點圖樣（沿用 retro 的 `[data-screen="home"]` 機制）；跑步頁面板不透明。

## 4. 溫馨可愛地圖（`app/track/cute/`，架構比照 RetroMap：next/dynamic、實例隔離、fallback、public/vendor worker、OpenFreeMap、`window.__cuteDebug`）
- 鏡頭：pitch 0、bearing 0、zoom 16.5、跟隨跑者、`setPadding(bottomInset)`。
- 樣式（程式產生 style JSON）：陸地奶油 `#fff4df`、公園／綠地薄荷 `#cdeed9`（fill-pattern：原創小圓樹點點圖案）、水域天空藍 `#bfe3f5`（fill-pattern：原創小波浪「〜」圖案）、建築淡桃 `#ffe3d8`＋1.5px 墨線外框（`line` 圖層 opacity .55）、道路白色＋1px 墨線邊（casing），主幹道較寬；標籤：公園／地標名稱以圓體（localIdeographFontFamily 'DORCute'）墨色字＋白色光暈，只顯示 z ≥ 15 的 POI／公園名。
- **跑者＝Q 版小井（原創向量）**：SVG 在載入時繪製成離屏 canvas 圖（2 倍解析度）後每幀 drawImage；尺寸約 40×46 CSS px；造型：大頭短身 2.5 頭身、粉紅鮑伯髮（`#fc7aa4`／陰影 `#e0608c`）、**圓角黑框眼鏡**內圓點眼（含白色高光）、腮紅橢圓＋三條斜線、小小微笑（簡單弧線，**不可用吉伊卡哇招牌嘴型**）、DOR 白色球衣配粉紅袖（呼應應援圖）、深藍小裙、粉紅跑鞋；2px 墨線＋3px 白色貼紙外框。動畫：移動時上下彈跳（squash & stretch，週期 500ms），往西移動時水平翻轉；靜止時輕微呼吸。
- 軌跡：桃紅虛線（線寬 4、白色 casing 7），每 100 m 一顆原創小愛心；每公里一面圓角小旗「1K」；目標點＝原創小禮物盒／小旗子圖示，完成後打勾貼紙。
- 效能：30fps 上限、專注模式 8fps、隱藏停止。

## 5. 專注模式（cute 變體）
上 45% 透出地圖與小井；下方白色貼紙卡（圓角 24px、2px 墨線、白邊、紙膠帶），標題膠囊「一起加油！」，大數字圓體桃紅＋深可可；底部鎖頭改原創圓潤鎖頭圖示；攔截觸控與長按 1.5 秒不變。

## 6. 驗證
- Go：isValidUiSkin／skin_options 測試；Neon 臨時分支：194 套用＋再套冪等、CHECK 允許 cute 擋 xyz、本機 api PUT cute 200。
- Web：tsc 0、verify-active-run 全過、next build 54 頁、bundle 隔離（/track 首屏不含 maplibre／cute；全站 CSS 不含 DORCute @font-face）。
- Playwright（prod＋swiftshader，大安森林公園真實道路每秒一點 60 秒；dashboard／ui-skin route 攔截）：
  1. 風格選單出現四個純文字選項，選「溫馨可愛」套用＋PUT {skin:'cute'}＋關閉；reload 持續；切回其他三種正常。
  2. 首頁／會員管理／跑步 idle／跑步中／專注模式／1280 截圖存 `C:\Users\paris\Downloads\cute_preview\`（1_home、2_profile、3_track_idle、4_track_running、5_focus、6_track_1280），另 `7_runner_sheet.png`（Q 版小井靜止＋彈跳兩幀＋翻轉，4 倍）、`8_style_picker.png`（cute 外觀下打開選單）。
  3. 量測：`__cuteDebug` zoom∈[15.5,17.5]、pitch 0、tilesLoaded ≥ 4、center 距最後一點 ≤150 m、跑者在可見區中心 ≤2px、跑者包圍盒約 40×46（±4）；地圖像素分類（奶油陸地／薄荷綠地／天空藍水／淡桃建築／白色道路）至少 4 類 >1%；卡片外框 computed 2px、主文字對比 ≥ 7:1、`document.fonts.check('16px DORCute')`。
  4. 迴歸：scifi／retro／default 外觀不變；非白名單 0 個 cute／openfreemap／maplibre／DORCute 請求且 data-skin 不變；WebGL 不可用退回；四視窗 0 console error；跑步面板透出 ≤ 3%；next dev StrictMode 不退回。
- 唯讀審查：版權（無吉伊卡哇元素、跑者為小井、圖示原創）、字型 OFL 合規（改名、授權檔隨附）、隔離、效能、清理。
