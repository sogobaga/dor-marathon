# cute 第二輪補正（R2b）— 光點／軌跡／公里徽章「看得見」

R2（CONTRACT_R2.md）其餘部分已驗收，只修地圖。編排者親自放大檢視 `Downloads\cute_preview_r2\4_track_running.png` 與 `7_orb_sheet.png` 的結論：

1. **光點幾乎看不見**：粉色光暈疊在粉色地圖上，只剩一顆小白點＋一圈極淡薰衣草線，不是視覺焦點。
2. **軌跡與道路混在一起**：粉色緞帶沿著粉色外框道路，看不出是「走過的路」。
3. **公里徽章完全沒出現**：跑了 1.23 km，但畫面上找不到任何愛心徽章，但 `__cuteDebug.kmBadges=1`。E2E 的「sakura 像素 3707 顆」是量到粉色地圖本身的假陽性。

## 修正方向（MAP，僅 app/track/cute/）

### A. 地圖底色退一步，讓光點當主角
地圖改成「柔和、低彩度」的粉白畫布，**地圖上最飽和的粉色只能是光點與軌跡**：
- 陸地 `#fffafc`；建築 `#f8eef3` 填色＋`#ecd6e0` 1px 細框；道路白色主線＋`#eedfe6` 外框（**不再用粉色外框**）；公園 mint `#e3f6ec`＋淡綠小圓樹；水域 `#dcefff`＋白色小波浪；地名 `#9a6a7e` 字＋白色光暈。
- 整體仍是粉嫩系（UI 已經很粉），但地圖不可以跟光點搶顏色。

### B. 光點要是畫面焦點
- 直徑約 18px 的實心光球：中心白色高光、主體 sakura→`#ff6fae` 放射漸層、2px 白色外緣；外圍光暈半徑約 30px（`#ff6fae` 55% → 0）；再外一圈淡薰衣草光環。
- 光球下方加一個柔和投影（`rgba(214,69,127,.35)`，模糊 6px），讓它在淺色地圖上「浮起來」。
- 呼吸脈動、閃亮小星、移動粒子維持 R2 規格（粒子上限 60、靜止 3 秒歸零、reduced-motion 全停）；粒子顏色要比地圖飽和（`#ff6fae`、`#b58cff`、`#ffcf5c`）。

### C. 軌跡要一眼看出
- 外層光暈寬約 14px、`#ff6fae` 30%；主線寬 6px，由 lavender `#b58cff` 漸變到 `#ff6fae`（起點→光點）；內芯 2px 白色。
- 軌跡要畫在道路之上、地名之下或之上皆可，但不能被任何圖層遮住。

### D. 公里徽章要真的畫出來
- 先找出根因：為什麼 `kmBadges=1` 但畫面上看不到（位置投影錯？畫在畫面外？被別的圖層蓋住？尺寸 0？顏色跟地圖一樣？）。在回報中寫明根因與 file:line。
- 規格：約 30px 寬 sakura 愛心、2px `#ff6fae` 描邊、3px 白色貼紙外框、柔和投影；愛心內 berry `#5b2a3c` 粗體數字；固定大小不隨縮放；位置在軌跡上該公里數的點。

### E. 偵錯把手補充（驗證用）
`window.__cuteDebug` 新增：
- `kmBadgeScreens`：目前畫出的每個公里徽章在地圖容器內的 CSS px 位置 `[{km,x,y}]`
- `trailScreenSample`：軌跡上 5 個取樣點的 CSS px 位置
- `setOverlayHidden(bool)`：暫時不畫光點／軌跡／徽章（只給 E2E 做前後對照用，預設 false）

## 驗收（E2E 必須做「有畫 vs 沒畫」對照，不可再只數粉色像素）
1. 同一畫面各截一張：正常畫、`setOverlayHidden(true)`。
2. 光點：在 `orbScreen` 周圍 12px 內，兩張圖平均色差 ΔE（CIE76）≥ 25；光點中心像素飽和度要明顯高於周圍 40px 環狀區的地圖平均。
3. 軌跡：`trailScreenSample` 每個點周圍 4px 內 ΔE ≥ 20。
4. 公里徽章：`kmBadgeScreens` 每個點周圍 12px 內 ΔE ≥ 30，而且必須在地圖可見區內（面板上緣以上、不在畫面外）；E2E 要確保餵點路線讓 1 km 點落在可見區（必要時把鏡頭縮小一級或路線轉彎）。
5. 地圖彩度：`setOverlayHidden(true)` 的截圖中，地圖區平均飽和度（HSV S）≤ 0.12。
6. 其餘 R2 驗收項（粒子、reduced-motion、迴歸、非白名單零請求、0 console error、tsc、next build）全部重跑。
7. 截圖覆蓋到 `Downloads\cute_preview_r2\`（3_track_idle、4_track_running、5_focus、6_track_1280、7_orb_sheet），另存 `4b_running_zoom.png`（光點＋軌跡＋公里徽章區域放大 3 倍的裁切）。
