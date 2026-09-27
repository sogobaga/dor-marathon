# 未來科幻世界 第二輪（地圖體驗與視覺品質）— 2026-09-27

前提：第一輪（CONTRACT.md）成果保留在工作區未提交。編排者檢視交付截圖後判定地圖體驗未達標，本輪只處理以下項目；CONTRACT.md 的隔離與安全原則全部沿用（非白名單零影響、跑步邏輯零改動、fallback 永遠可用）。

## 1. 必修 bug
- **實例隔離**：SciFiMap 每次建圖產生新 instance；所有監聽（load、error、webglcontextlost、styledata…）先比對 `map === mapRef.current`（或 instance token）才動作；cleanup 先移除這些監聽再 `map.remove()`；`fail()` 只能作用在目前 instance。驗收：`next dev`（React StrictMode 雙掛載）與快速 OFF→ON 切換下，地圖不退回 Leaflet。

## 2. 鏡頭與初始畫面
- **定位前就顯示城市**：初始中心＝最後已知位置（localStorage；若 page 沒有就由 SciFiMap 自行在每次 pos 更新時寫 `dor_scifi_last_pos`），沒有則台北大安森林公園（25.0296, 121.5357）；zoom 16、pitch 55、bearing 0；靈魂以「定位中」暗淡狀態顯示在該處。
- **跟隨**：每次 pos 更新 `easeTo({center, zoom:16.5, pitch:55, bearing:平滑後的行進方向（速度 >1 m/s 才更新，否則維持）, duration:900})`；使用者拖曳／縮放後暫停跟隨 8 秒，「回到目前位置」立即恢復。
- **偵錯把手**：`window.__scifiDebug = { center, zoom, pitch, bearing, lastPos, tilesRequested, tilesLoaded, soulScreen:{x,y}, fps }`（每秒更新；只在 scifi 生效時存在）。

## 3. 視覺品質（目標＝參考圖 1 的近景 3D 發光城市）
- 標籤：只顯示 zoom ≥ 14 的道路名與地標；國家／省市／遠方城市等 place 標籤一律隱藏（避免出現「溫州市」這類遠方地名）。
- 建築：顏色對比拉高（低樓 #0d2350 → 高樓 #2a7fff，頂面較亮），`fill-extrusion-opacity` .92，另加 zoom ≥ 15 的建築輪廓發光線（`line` 圖層取 building 外框，#35e6ff、opacity .35、blur 1）；
- 道路：z16 主幹道光暈寬 ≥ 14px、亮芯 2.5px；一般道路光暈 8px、亮芯 1.2px；
- 靈魂：核心光暈直徑 ≥ 36px（CSS px）、環繞粒子 50–70 顆半徑 14–42px，加法混合；移動時身後軌跡粒子肉眼明顯（每秒新增 ≥ 40 顆、壽命 2 秒）；
- 路線：漸層線寬 5＋外光暈 14，排除段不畫；
- 光雨：保留但降低到不干擾（≤ 40 條、opacity ≤ .35）。

## 4. 專注模式（scifi）
- 開啟時 track 頁自身的標題列、底部面板、橫幅（每公里分段條、GPS 異常提示等）在 scifi 下全部 `visibility:hidden`（page.tsx 以 `data-scifi-focus` 之類屬性＋globals.css `[data-skin="scifi"]` 選擇器實作，只在 scifi 生效），畫面只剩地圖＋專注層。
- 專注層背景改由上而下漸層：頂部 0–45% `rgba(2,4,10,.15→.35)`（看得到城市與靈魂），55% 以下 `rgba(2,4,10,.92)`；大字數字放下半部；底部鎖頭不變；攔截觸控與長按 1.5 秒不變。

## 5. 首頁星塵
- ParticleField 手機 120 顆、桌機 180 顆，大小 1–2.4px、亮度閃爍，底部淡青色透視網格地平線；截圖中肉眼可見（像素檢驗：非背景亮點比例 > 0.3%）。

## 6. 驗收（看值與畫面，不看檔案大小）
- Playwright（`next build && next start`，另跑一次 `next dev` 確認第 1 節）；Chromium `--use-gl=swiftshader --enable-unsafe-swiftshader --ignore-gpu-blocklist`；viewport 390×844（另補 1280）。
- GPS 餵點：沿大安森林公園周邊真實道路（新生南路→信義路→建國南路→和平東路）手作 ≥ 40 個座標，**合成時鐘每 1 秒一點、速度約 3.2 m/s**，跑 60 秒；不得觸發跳點排除（檢查頁面沒有「已排除 N 段」）。
- 量測（全部回報數值）：`__scifiDebug.zoom` ∈ [15.5, 17.5]、`pitch ≥ 45`、`center` 與最後一點距離 ≤ 150 m、`tilesLoaded ≥ 10`（且網路請求 openfreemap 200 ≥ 10）、定位前截圖地圖區域非背景像素比例 > 20%、跑步 60 秒後在 `soulScreen` 周圍 40px 內青色高亮像素 > 30、路線漸層沿線取樣命中 ≥ 5 點。
- 截圖（覆蓋到 `C:\Users\paris\Downloads\scifi_preview\`）：1_home、2_profile、3_track_idle（定位前）、4_track_running（跑 60 秒後、專注模式先關閉）、5_focus（專注模式開啟）、6_track_running_1280。
- 迴歸：非白名單帳號零影響（data-skin 不變、無 openfreemap/maplibre 請求、Leaflet 正常）、WebGL 不支援退回、0 新 console error、四視窗無溢出。
