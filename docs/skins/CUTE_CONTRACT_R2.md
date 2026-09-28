# 溫馨可愛（cute）第二輪契約 — 更粉嫩＋小井改為「光點」

前提：v1.2.856 已上線（docs/skins/CUTE_CONTRACT.md 第一輪）。第一輪原則全部沿用：非授權帳號零影響（零請求、零視覺變化）、跑步邏輯零改動、原創美術、不使用任何吉伊卡哇元素、z-index 與 .phone-shell 慣例、硬寫死規則排除 `[data-skin="default"]` 子樹、不動其他 skin（scifi／retro／default）。**不需要 migration、不改後端。**

## 1. 使用者原話（2026-09-28）
> 「溫馨可愛」需要再更粉嫩可愛一點
> 在 GPS 地圖上面，我決定把小井的圖像移除，避免未來會有角色性別顯示，或是需要擴充角色導致負擔加重
> 先把這個角色移除，但是在跑步進行時，還是需要有一個類似靈魂光點的呈現
> 移動的路徑由光點移動的軌跡來呈現，這點還是需要保留，每一公里一樣要有一個標誌來呈現

範圍：只改 cute。復古 RPG 的小井勇者**不動**。

## 2. 新色票「草莓牛奶」（兩位工人共用，逐字使用這些色號）

| token | 色號 | 用途 |
|---|---|---|
| milk | `#fff5f8` | 頁面底（草莓牛奶） |
| card | `#ffffff` | 卡片底 |
| blush | `#ffe3ee` | 次要底（--bg-2、輸入框、專注模式下半部） |
| sakura | `#ffc4dc` | 主按鈕／選中分頁填色（淡粉） |
| candy | `#ff9fc8` | 主按鈕漸層深端、軌跡主色 |
| rose | `#d6457f` | 大字數字（≥24px 才可用，對比 ≥3:1） |
| berry | `#5b2a3c` | 主要文字／粉色底上的文字（墨色，取代舊 #3d2b2b） |
| berryDim | `#8a4a64` | 次要文字 |
| line | `#b5708a` | 按鈕／輸入框／分頁的描邊（對 milk ≥3:1） |
| lineSoft | `#f6c6d8` | 卡片的裝飾性細框、分隔線 |
| lavender | `#d9c8ff` | 點綴 |
| mint | `#c8f2e1` | 點綴、公園 |
| sky | `#cfe8ff` | 點綴、水域 |
| butter | `#fff0b8` | 點綴（少量，不可再當大面積底色） |
| peachCoral | `#ff9aa8` | 「結束並上傳」等收尾按鈕（配 berry 字） |

規則：
- **粉色實心底上一律用 berry 深色字，不用白字**（白字在粉色上對比不足，上一輪會員管理標題列就是這樣壞的）。金色實心底仍依全站「金底白字」規則。
- 對比：一般文字 ≥4.5:1、大字（≥24px，或 ≥18.66px 粗體）≥3:1、按鈕／輸入框邊界 ≥3:1。
- 描邊整體變柔：上一輪 2px 深可可墨線改成 `line` 玫瑰藕色；卡片改 1.5–2px `lineSoft`＋3px 白色貼紙白邊＋粉色柔影 `0 4px 12px rgba(214,69,127,.14)`。**不要比上一輪更粗。**
- 大面積不得再是奶油黃／米黃：頁面、專注模式下半部、首頁背景都改粉色系。
- 字型 DORCute 不變。

## 3. 全站 UI（THEME 工人）
- `globals.css` 的 `[data-skin="cute"]` 區塊整套換成第 2 節色票（--bg 草莓牛奶底＋極淡小愛心／圓點圖樣、--bg-1 白、--bg-2 blush、--line line、--tx berry、--tx-dim berryDim、--fug 改成可配 berry 字的粉色並檢查所有用 `--fug-ink` 的地方改 berry、--hunt 改 peachCoral 配 berry 字）。逐一檢查 cute 區塊內每條規則在新色票下的對比。
- 主按鈕（開始跑步等）：sakura→candy 漸層、berry 字、line 描邊、白色貼紙白邊；按下有輕微 Q 彈（尊重 prefers-reduced-motion）。
- 收尾按鈕（結束並上傳等 `.skin-btn-end`）：peachCoral 系、berry 字。
- 會員管理標題列 `.profile-header`：blush→sakura 淡粉緞帶、berry 字。分頁 `.profile-tab`：未選白底 line 框 berry 字；選中 sakura 底 berry 字。
- `components/cute/CuteBackground.tsx`／`decor.ts`：背景改成粉嫩系——粉／薰衣草／薄荷小愛心、四角閃亮星、半透明泡泡、帶粉色描邊的粉白雲朵（不要灰色雲），紙屑減量；`CUTE_PALETTE` 換成第 2 節色票。飄動慢而輕，尊重 reduced-motion。
- `RaceFocusMode.tsx` cute 變體：底部漸層從透明到 blush（不再是奶油黃）；貼紙卡白底＋lineSoft 框＋粉色柔影＋紙膠帶（紙膠帶改 lavender 或 sky 半透明）；標題膠囊「一起加油！」sakura 底 berry 字（上一輪白字配粉是錯的）；大數字 rose 色；配速等小字 berry／berryDim；鎖頭環 candy；隱藏時的浮動鈕同樣改色。**浮動鈕位置 `bottom:136px`（v856 修的重疊）不可改回。**
- `StyleSettingsModal.tsx` cute 說明改為：「粉嫩手繪風、圓體字，跑步時化身閃亮小光點。」
- `lib/skinOverride.ts` cute 的 theme-color 改 `#fff5f8`（註解同步）。
- `.phone-shell` 桌機外框：粉色系（可保留現有粉框，改成 candy／lineSoft 雙層）。

## 4. GPS 地圖（MAP 工人，app/track/cute/）
### 4.1 移除小井
- 刪除 `runner.ts` 與所有引用（loadRunnerSprite／drawRunner／getRunnerSvgSource、heroFlip、彈跳、翻轉等狀態）。cute 相關程式與註解中不得再出現「小井」或 runner 角色造型描述（可留一句「v857 起不再使用角色」）。

### 4.2 光點（取代角色）
- 目前位置畫一顆「靈魂光點」：白色核心（r≈5px，白→淡粉放射漸層）＋粉色光暈（r≈20–24px，candy 半透明→透明，放射漸層）＋外圈極淡薰衣草光環；呼吸脈動（約 1.6 秒週期、±10% 大小），移動中脈動略快。
- 光點旁 1–2 顆小四角閃亮星緩慢繞轉或忽明忽滅（可愛感點綴，小、低調）。
- 移動時從光點位置持續冒出少量閃亮粒子（粉／薰衣草／butter 小光點），1–1.5 秒內飄散淡出；**粒子上限 60**；靜止時不冒粒子。
- `prefers-reduced-motion`：沒有脈動、沒有粒子，只畫靜態光點。
- 光點尺寸固定（不隨 zoom 放大），在 `setPadding(bottomInset)` 的可見區中心，不被面板蓋住（沿用現有 follow／padding 邏輯）。
- 跑步前（idle）也顯示光點於目前位置（靜態呼吸），沒有定位時不畫。

### 4.3 軌跡＝光點走過的路
- 已走路徑畫成「發光緞帶」：外層柔光（寬約 10–12px、candy 25–35% 透明、可模糊）＋主線（寬約 5px、candy）＋內芯（寬約 2px、`#fff0f6`）。可用 MapLibre line-gradient（需 geojson `lineMetrics:true`）讓起點→光點由 lavender 漸變到 candy，強化「光點拖出來的軌跡」感。
- 軌跡末端要接到光點目前位置（不能與光點脫節）。
- **移除每 100m 小愛心**（改由發光軌跡本身呈現路徑）。
- 訊號中斷分段（polyline 以 `;` 分段、跳點不相連）照舊，只換樣式。

### 4.4 每公里標誌（保留）
- 每滿 1 km 在軌跡上的對應位置放一個「愛心徽章」：sakura 粉色愛心（約 26px 寬）＋白色貼紙白邊＋line 細描邊，愛心內 berry 粗體數字（1、2、3…），下方或側邊小字「km」可省略。大小固定不隨 zoom 變。
- 起點可以有一個小小白底粉框圓點（原創），非必要。
- 賽事目標圖示（禮物盒、打卡點旗等 icons.ts 既有者）保留，改用第 2 節色票（描邊 line、berry 取代舊墨色）。

### 4.5 地圖本身更粉嫩（style.ts／tiles.ts）
- 陸地 `#fff3f7`；建築 `#ffe0ec` 填色＋`#e3a9c0` 1px 細框（不要深色粗框，上一輪建築墨線太重太雜）；道路白色主線＋`#f3b6cc` 外框；公園 mint 底＋原創小圓樹點點（樹改淡綠＋淡粉描邊）；水域 sky 底＋白色小波浪；地名 berryDim 字＋白色光暈。
- 整體要一眼看出「粉色系地圖」，但道路／公園／水域仍分得出來。

### 4.6 偵錯把手 `window.__cuteDebug`
移除 runner 欄位，新增：`orbVisible`（bool）、`orbScreen`（{x,y}，CSS px，相對地圖容器）、`trailPoints`（number）、`kmBadges`（number，目前畫出的公里徽章數）、`particles`（目前粒子數）、`fps`；保留原有 center／zoom／tilesLoaded 等。

## 5. 驗收（E2E，截圖存 `C:\Users\paris\Downloads\cute_preview_r2\`）
1. 截圖：1_home、2_profile、3_track_idle、4_track_running（已跑 ≥1.2 km，看得到發光軌跡＋至少 1 個公里徽章＋光點）、5_focus、6_track_1280、7_orb_sheet（4 倍：光點靜止／移動含粒子／公里徽章／一段軌跡樣本）、8_style_picker。
2. 數值：`orbVisible=true` 且 `orbScreen` 在地圖可見區（面板上緣以上）；`trailPoints` 隨餵點增加；餵滿 1.2 km 後 `kmBadges ≥ 1`；移動中 `particles` 介於 1–60、靜止 3 秒後回到 0；reduced-motion 下 `particles` 恆為 0。
3. 像素：光點中心附近有高亮粉白像素；軌跡上取樣點為粉色系；公里徽章位置有粉色愛心像素；**地圖區與頁面上不存在上一輪小井的特徵色塊**（例如 `#fc7aa4` 髮色＋深藍裙 `#2b3a8c` 附近色同時出現）。
4. 粉嫩度：1_home、2_profile、5_focus 三張，取亮度 >80% 且飽和度 >8% 的像素，粉／薰衣草色相（280–360°、0–15°）數量 ≥ 黃／橘色相（20–65°）的 3 倍。
5. 對比：主要文字、按鈕文字、標題列、分頁、專注模式膠囊與數字，量實際 computed 色計算對比，符合第 2 節門檻。
6. 迴歸：scifi／retro／default／非白名單帳號完全不變（非白名單零 cute 請求）；WebGL 不支援時 fallback 到 Leaflet；四種視窗 0 console error；`npx tsc --noEmit`、`npx next build` 通過。
7. 原始碼：`app/track/cute/`、`components/cute/` 內 grep「小井」「runner」為 0（允許一句移除說明）；runner.ts 已刪。
