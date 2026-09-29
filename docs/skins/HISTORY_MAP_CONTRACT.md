# 跑步軌跡歷史地圖風格化契約（/track/history）

使用者（2026-09-29，附截圖：未來科技風格帳號，歷史頁地圖仍是 Leaflet＋OSM 淺色圖）：「歷史軌跡的部分，是否有辦法也改成對應的風格」。

## 範圍
- 只改 `apps/web/src/app/track/history/page.tsx` 的**單趟詳細地圖**（目前 Leaflet：軌跡綠線／異常紅線、每公里號碼標記 `addKmMarkers`、起點綠圈／終點紅圈、fitBounds padding 22）。
- 帳號目前生效的風格是 scifi／retro／cute 且支援 WebGL → 改用 MapLibre 風格地圖；其他（default/warm/warm2、非白名單、WebGL 不支援、載入失敗）→ 維持原本 Leaflet，**行為與畫面完全不變**。
- 頁面其他部分（標題、數據卡、每公里分段、揮汗有禮區塊）已經吃全站 skin CSS，不動。

## 地圖元件
- 新增 `apps/web/src/app/track/history/SkinRouteMap.tsx`（`next/dynamic` ssr:false，只有在 skin 生效時才 import，非白名單零下載），props：`skin`、`segments`（decodePolylineSegments 的 `[lat,lng][][]`）、`kmMarks`（沿用 `kmMarkerPositions(segments, 1000/calibK)`，**不可改算法**）、`flagged`、`onFallback(reason)`。
- 地圖樣式直接重用各 skin 既有的 style builder 與圖塊（`track/scifi/style.ts`、`track/retro/style.ts`＋`components/retro/tiles.ts`／`retro/sprites.ts` 的 tileImageData、`track/cute/style.ts`＋`cute/tiles.ts`），圖片一律用 `map.setMissingStyleImageResolver()`（不可用 styleimagemissing 事件，見 CuteMap 註解）；MapLibre worker 用 `config.WORKER_URL='/vendor/maplibre-gl-6.11.2/maplibre-gl-worker.mjs'`（比照三個 *Map.tsx）。
- **靜態俯視**：pitch 0、bearing 0（scifi 也用 0，歷史頁要看全貌）；`fitBounds(route, padding ~28px)`；可拖曳縮放；左上 +／− 按鈕比照現有 Leaflet 視覺（用 skin 樣式）；精簡 attribution。
- 軌跡、公里號碼、起終點用 **MapLibre 原生圖層**（GeoJSON source＋line/circle/symbol layer，icon 用 addImage 產生的圖），不要再疊 2D canvas（避免 DPR 疊層問題；若真的要疊 canvas，必須 CSS width/height 100%）。
  - scifi：青色發光軌跡（外層寬光暈 line-blur＋主線 #35e6ff＋白色細芯）；公里號碼＝青框深底圓牌＋白字；起點青色光環、終點洋紅光環。
  - retro：黑框金色像素光路（黑色外框線＋金黃主線，方頭方角 line-cap butt / line-join miter）；公里號碼＝沿用 R4 放大後的像素公里旗樣式（黑框、數字清楚）；起點像素綠旗、終點像素格子旗（原創繪製）。
  - cute：薰衣草→糖果粉漸層發光緞帶（line-gradient，source lineMetrics:true）＋白芯；公里號碼＝cute 的粉紅愛心數字徽章（沿用 icons.ts 的繪法產生 image）；起點白底粉框小圓點、終點小愛心。
  - `flagged`（異常）時軌跡改該 skin 的警示色（scifi 洋紅、retro 紅、cute 珊瑚紅），並保留原本的文字說明。
- **揮汗有禮截圖需求不可退步**（lib/gov500.ts）：整條路線在畫面內、每公里號碼清楚可讀（號碼字級 ≥ 11 CSS px、與底圖對比足夠）、起終點可辨識。
- 容錯（比照 v860）：WebGL 不支援 → 直接 Leaflet；載入逾時 10 秒（document.hidden 期間暫停計時）→ Leaflet；webglcontextlost → 等 MapLibre 自行 restore 5 秒，未恢復則重建一次，再失敗才 Leaflet；render 例外不會發生（沒有 rAF 疊層）。
- 偵錯把手 `window.__historyMapDebug`：`skin`、`loaded`、`routeBounds`、`kmCount`、`kmScreens [{km,x,y}]`、`startScreen`、`endScreen`、`fallbackReason`、`setRouteHidden(bool)`（E2E 有畫/沒畫對照用）。

## 驗收
- E2E（Playwright，DPR 3，390×844 與 1280；mock 歷史 API 回傳一筆含多段 polyline、km_paces 的跑步）：四種情況（default、scifi、retro、cute）各一：
  1. default／非白名單：仍是 Leaflet，零 maplibre／openfreemap 請求，畫面與改動前一致。
  2. 三個 skin：地圖為 MapLibre 且套用該 skin 樣式；`routeBounds` 全在可見區；`kmCount` = 預期公里數，每個號碼中心在畫面內且有畫/沒畫 ΔE ≥ 30；起終點可見；多段軌跡段間不相連；0 console error。
  3. 截圖存 `C:\Users\paris\Downloads\history_preview\`（每 skin 一張全頁＋一張地圖放大），驗收者親自看圖描述「路線、公里號碼、起終點是否一眼可辨」。
  4. WebGL 關閉 → Leaflet；context lost→restore → 仍是 skin 地圖。
- `npx tsc --noEmit`、`npx next build` 通過；非白名單零影響。

## 定案調整（2026-09-30，編排者看截圖後）
- scifi：路線改洋紅發光（#ff3fd0 光暈／#ff5fe0 主線＋白芯），因為 scifi 底圖道路本身就是青色發光、青色路線會融進道路；異常改琥珀 #ffb020；起點白底青框環、終點白底洋紅框環（約 21px），旁加「起」「終」白字深色描邊。
- retro：像素公里旗／起終點旗縮到約 55%（HISTORY_RETRO_ICON_SCALE=1.6，數字約 16 CSS px），fitBounds 非對稱 padding 同步調小。
