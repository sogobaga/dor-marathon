# 未來科幻世界（scifi）風格 契約 v1 — 2026-09-27

## 0. 使用者需求（原話）
「全新設計具備粒子科幻感的介面風格體驗，新增一種 UI/UX 風格－未來科幻世界。目前只有 sogobaga@gmail.com 的帳號可以感受，其餘帳號維持不變，不受到影響。尤其是在 GPS 跑步追蹤時的 GPS 地圖：以真實世界的地圖資訊為基礎，設計成一個充滿科技感粒子風格的呈現，就像一個人在一個未知領域探索的感覺一樣；而跑者就是一個用粒子來呈現的靈魂感，移動時會有粒子的移動軌跡，十足的未來感與科幻感。」
參考圖：(1) 深藍夜色 3D 城市、建築塊體、青色發光道路網、天空垂直光雨；(2) 霓虹青／洋紅雙色、霧化遠景、玻璃高樓、光暈。

## 1. 最高原則
- **其他帳號零影響（硬性）**：非白名單使用者的 HTML、CSS 生效規則、JS bundle（track 頁不得多載 maplibre）、網路請求、Leaflet 地圖行為全部不變。所有 scifi 程式碼在 `data-skin="scifi"` 或動態 import 之後。
- **跑步紀錄永不依賴地圖渲染**：GPS 取點、距離、上傳、只有手動才結束、專注模式等邏輯一律不動；科幻地圖壞掉（WebGL 不支援、tiles 失敗、例外）→ 自動退回既有 Leaflet 地圖，跑步照常。
- 既有規則沿用：fixed 覆蓋層在桌機由 `.phone-shell` 框住；全螢幕高度 `.app-min-h`；z-index 面板 500 < 一般彈窗 2000–3300 < 倒數 3800 < 專注 3900 < Cheer 3950 < landscape-lock 4000；禁用 scrollIntoView；事件任務面板維持 `data-skin="default"`。

## 2. 開關（誰看得到）
- 後端：`GET /me/dashboard` 新增 `scifi_entry: 'hidden'|'shown'`。解析：讀 app_settings `scifi_entry_state`（hidden|whitelist|open|off；**缺鍵預設 whitelist**）與 `scifi_entry_whitelist`（逗號分隔 email／帳號編碼；**缺鍵預設 `sogobaga@gmail.com`**）；**不給 super_admin 旁路**（使用者要求只有這個帳號）。兩鍵註冊到 `internal/appsettings` specs 與前台 `lib/appSettings.ts`（後台「系統設定」可改）。不需 migration。
- 前端：`lib/skinOverride.ts`＋`components/SkinOverride.tsx`（掛在 root layout 的 client providers 內）：登入且 `scifi_entry==='shown'` 且使用者偏好不是 off → `<html data-skin="scifi">`、`meta[name=theme-color]`=#02040a，並寫 localStorage `dor_skin_override={uid, skin:'scifi'}`；登出、換帳號、entry 變 hidden、偏好關閉 → 移除覆寫並恢復 SSR 原值。
- 防閃爍：`app/layout.tsx` 既有 head 內聯開機腳本加一段：若 localStorage 覆寫的 uid 與目前登入 uid（從既有 localStorage 使用者資訊或 token uid claim 讀，實作者查 `lib/userAuth.ts`）一致 → 在 paint 前設 `data-skin="scifi"`；`<html>` 加 `suppressHydrationWarning`（只抑制這個屬性差異）。
- 會員管理（ProfileScreen）對白名單者顯示開關列「🌌 未來科幻世界」ON/OFF（偏好存 localStorage `dor_skin_pref`，預設 ON）。

## 3. 全站視覺（`[data-skin="scifi"]`，只寫在 globals.css 與 components/scifi/）
- 色盤 token：`--bg:#02040a` `--bg-1:rgba(10,22,44,.62)`（玻璃卡）`--bg-2:rgba(20,40,72,.55)` `--line:rgba(80,210,255,.18)` `--line-2:rgba(80,210,255,.32)` `--tx:#e8f7ff` `--tx-dim:#9dbbd0` `--tx-faint:#5f7f96` `--fug:#35e6ff`（主色青）`--fug-ink:#01121a` `--hunt:#ff3fa0`（結束／警示洋紅）`--gold:#ffd166`；結構 token：卡片 `backdrop-filter: blur(12px)`＋1px 青色描邊＋外光 `0 0 24px rgba(53,230,255,.12)`，按鈕主色＝青→洋紅漸層描邊的霓虹鈕（`.skin-btn-start`／`.skin-btn-end` 在 scifi 下改純 CSS，不用 warm2 圖片），數字字體用 next/font 自託管的 **Orbitron**（僅數字／英文標題，中文維持系統字）。
- 背景：`components/scifi/ParticleField.tsx`（canvas 2D，全螢幕固定於內容之下 z 0、pointer-events none）：深空漸層＋緩慢漂移的星塵粒子（手機 ≤90 顆、桌機 ≤160）＋極淡透視網格；頁面隱藏時停止、`prefers-reduced-motion` 只畫靜態；幀率上限 30fps。由 SkinOverride 在 scifi 生效時掛載。
- 逐頁檢查（主要前台頁）：首頁（PhoneShell 主卡／入口格／開始跑步橫幅）、會員管理、活動探索、track 頁面板、專注模式、倒數層、常見彈窗——任何寫死淺色背景或 `rgba(255,255,255,…)` 導致不可讀者改用 token（只在 scifi 生效的選擇器下覆寫，不改其他 skin 的外觀）。對比：主字 ≥ 7:1、點綴色只用於粗體大字。

## 4. GPS 地圖（核心體驗）
### 4.1 架構（隔離優先，不重構既有 Leaflet 程式）
- 新元件 `src/app/track/scifi/SciFiMap.tsx`（`next/dynamic`、`ssr:false`，只在 scifi 生效時載入），使用 npm `maplibre-gl`（BSD-3）＋ OpenFreeMap 向量圖磚（`https://tiles.openfreemap.org/planet`，OpenMapTiles schema；glyphs `https://tiles.openfreemap.org/fonts/{fontstack}/{range}.pbf`；免金鑰；attribution「© OpenFreeMap © OpenMapTiles © OpenStreetMap contributors」右下角小字）。
- **既有 Leaflet 地圖照舊建立與運作**（所有邏輯、打卡、每公里標記、km 圖層照跑），scifi 時只把 Leaflet 容器視覺隱藏（`visibility:hidden`，保留尺寸避免 invalidateSize 異常），SciFiMap 疊在同一位置、同尺寸。
- track/page.tsx 只做「加法」：(a) 判斷 `document.documentElement.dataset.skin==='scifi'`（加 MutationObserver 監聽切換）；(b) 提供資料快照 props 給 SciFiMap（節流 ≤ 4 次／秒）：`pos{lat,lng,acc,heading?}`、`status`、`segments: [lat,lng][][]`（依既有 ';' 分段／排除段拆開，排除段不畫）、`kmMarks`、`targets`（城市探索關主／賽事打卡點／focus 關主：`{id,lat,lng,radius,label,kind,done}`，從既有 state/ref 取）、`eventMarkers`（若有）；(c) 「回到目前位置」「＋／－」在 scifi 下改呼叫 SciFiMap 的 imperative handle（`recenter()`、`zoomBy()`）。實作者先盤點 page.tsx 所有 Leaflet 圖層（約 40 處：circleMarker×3、circle×2、polyline×2、layerGroup×2、tooltip×2、popup×1、fitBounds/panTo/setView），逐一在 SciFiMap 找到對應呈現，盤點表寫進回報。
- 失敗退回：`maplibregl.supported()` 為否、`webglcontextlost`、style/tiles 8 秒內未 `load`、任何 render 例外 → 卸載 SciFiMap、恢復 Leaflet 顯示，並記一次 `console.warn('[scifi-map] fallback', reason)`（不打擾使用者）。

### 4.2 地圖風格（程式產生 style JSON，放 `scifi/style.ts`）
- 背景 `#02040a`；水域 `#031428`＋外緣 1px `rgba(53,230,255,.25)`；綠地／公園 `#04181c`；
- 道路：每一級兩層——寬模糊光暈（line-blur 4–8、`rgba(53,230,255,.18~.35)`）＋細亮芯（`#35e6ff`／主幹道 `#7ff3ff`，寬度依 zoom 內插）；次要道路亮度遞減，營造參考圖 1 的發光路網；
- 建築：`fill-extrusion`，高度 `coalesce(render_height, 10)`、底 `render_min_height`，顏色依高度 `interpolate`：低樓 `#0a1a3a` → 中 `#11306a` → 高 `#1b5bb5`，頂緣以另一層低透明 `fill-extrusion` 或 `line` 描出青色輪廓；`fill-extrusion-vertical-gradient: true`；
- 標籤：僅道路名與地標，小字 `#7fdfff` 半透明、光暈 `#02040a`；中文用 `localIdeographFontFamily`（系統中文字型），不依賴 CJK glyph；
- 相機：pitch 55°、跑步中跟隨跑者並依移動方向緩慢旋轉 bearing（可關：idle 時 bearing 0）；zoom 16.5；地平線霧化 `setFog`／`sky`（若版本支援）呈現參考圖 2 的遠景霧光；
- 光雨：canvas 疊層（在地圖之上、UI 之下），細長青色光絲自上而下（數量 ≤ 60，速度隨機），`prefers-reduced-motion` 關閉。

### 4.3 跑者「粒子靈魂」與軌跡
- 自訂 canvas 2D 疊層（或 MapLibre CustomLayerInterface WebGL，實作者擇一；優先 2D canvas 以 `map.project` 每幀換算，易於維護），與地圖同尺寸、`pointer-events:none`：
  - 靈魂本體：中心白青核心＋多層加法混合光暈，40–70 顆粒子以不同半徑／速度環繞並呼吸縮放；靜止時緩慢漂浮，移動時沿行進反方向拉長成彗星狀；
  - 移動軌跡粒子：每幀依速度在身後噴出粒子（青→洋紅漸層、隨時間減速、擴散、淡出 1.5–3 秒），上限 ≤ 400 顆（手機），物件池重用避免 GC；
  - 已跑路線：MapLibre `line` 圖層（`lineMetrics:true`＋`line-gradient` 青→洋紅，寬 4＋外光暈 12），排除段不畫；每公里標記：小型發光菱形＋「1K」「2K」Orbitron 字；
  - 目標點（關主／打卡點）：地面發光圓環（依 radius 以 `circle`／fill 圖層呈現）＋上方淡淡光柱；完成者轉洋紅勾；點擊顯示既有等效資訊（對應 Leaflet tooltip/popup 內容）。
- 效能：渲染迴圈 `requestAnimationFrame`，30fps 上限；頁面隱藏停止；**專注模式開啟時**地圖在背景降為 10fps、關閉光雨、粒子數減半。

### 4.4 專注模式（scifi 變體，只在 scifi 生效）
- RaceFocusMode 背景由純黑改為 `rgba(2,4,10,.72)`，露出下方仍在運作的科幻地圖（使用者要的「在未知領域探索」在鎖定時也看得到）；數字改 Orbitron＋青色光暈；底部鎖頭改霓虹圓環；攔截觸控、長按 1.5 秒解除等行為完全不變。其他 skin 維持 v850 純黑。

## 5. CSP 與相依
- `next.config.mjs` CSP：`connect-src` 加 `https://tiles.openfreemap.org`；`worker-src 'self' blob:`（MapLibre web worker）；`img-src` 已含 https:。只加，不移除既有。
- `apps/web/package.json` 加 `maplibre-gl`（固定版本）；MapLibre CSS 由 SciFiMap 動態 import（不進全站 CSS）。
- **Bundle 隔離驗證**：`next build` 後確認 `/track` 首屏 JS 不含 maplibre（只在動態 chunk）。

## 6. 驗證
- Go：`go build ./... && go vet && go test`（scifi entry 解析：缺鍵預設、whitelist 命中／未命中、off、super_admin 不旁路）。
- Web：`tsc --noEmit`（0）、`verify-active-run.mjs`（全過）、`next build`（54 頁）＋ bundle 檢查。
- Playwright（Chromium 開 WebGL：`--use-gl=swiftshader --enable-unsafe-swiftshader`；dashboard 以 route 攔截回 `scifi_entry`；geolocation 模擬台北市一段真實路線，例如大安森林公園周邊）：
  1. **白名單帳號**：首頁、會員管理（含開關）、track idle、track 跑步中（餵 60 點、看到路線漸層與粒子軌跡）、專注模式（半透明看得到地圖）截圖；`data-skin="scifi"`；maplibre canvas 存在且非空白（取像素採樣確認有非背景色像素）。
  2. **非白名單帳號**：`data-skin` 維持原值；網路請求中**沒有** openfreemap／maplibre chunk；Leaflet 地圖可見；與改動前行為一致（打卡點、路線、回到目前位置）。
  3. 開關 OFF → 立即恢復原風格且重整後不閃回 scifi；登出 → 覆寫清除。
  4. 退回機制：以 addInitScript 讓 `WebGLRenderingContext` 不可用 → 自動顯示 Leaflet、跑步正常。
  5. 效能粗測：跑步中 10 秒內 long task（>50ms）次數、rAF 平均幀間隔（headless 僅供參考，回報數值）。
  6. 四視窗（390/430/768/1280）無橫向溢出、1280 在 phone-shell 內、0 新 console error。
- 唯讀審查：隔離（非白名單零影響的每條路徑）、跑步邏輯零改動、記憶體（粒子池、事件監聽、map.remove()）、fallback 完整、CSP 最小化、授權聲明、無殘留 console.log。
- 交付：把白名單帳號的 5 張截圖複製到 `C:\Users\paris\Downloads\scifi_preview\`。
