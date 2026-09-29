# 復古 RPG（retro）第四輪契約 — GPS 地圖小井勇者改為「像素魔法光點」

前提：v1.2.858 已上線。溫馨可愛已在 v857 移除小井改靈魂光點（docs/skins/CUTE_CONTRACT_R2.md／R2b.md）。本輪把同一決策套到復古 RPG 地圖。

## 1. 使用者原話（2026-09-28）
> 「復古 RPG」地圖上的小井勇者 → 也想改成光點
（沿用 v857 的理由：避免未來地圖角色的性別顯示、避免擴充角色造成負擔。）

## 2. 範圍與不變原則
- **只改 retro 的 GPS 地圖跑者表現**：app/track/retro/（RetroMap.tsx、sprites.ts、types.ts 如需要），外加
  StyleSettingsModal.tsx 的 retro 說明文字、與 retro 相關的過時註解。
- **不動**：羊皮紙 UI、RetroBackground 首頁像素草原、地圖圖塊與配色、字型、專注模式皮革視窗的版面、DORPG
  遊戲系統裡的「小井」角色（lib/dorpg/*，那是另一套戰鬥系統的角色，不在本輪範圍）、其他 skin。
- 沿用：非授權帳號零影響、跑步邏輯零改動、原創美術、z-index 慣例、MapLibre 架構（vendor worker、實例隔離
  `mapRef.current===map`、Leaflet fallback、`setPadding(bottomInset)` 讓跑者在可見區中心、單一 rAF 迴圈卸載即停、
  `setMissingStyleImageResolver` 圖片註冊、水波 remove+addImage 動畫）。
- 無 migration、無後端改動。

## 3. 像素魔法光點（取代勇者）
- 逐像素繪製的「魔法光球」，像素格邏輯尺寸 ≤ 14×14，放大倍率沿用現有勇者倍率 `heroScale`（目前 2，整數倍、
  `imageSmoothingEnabled=false`、crispEdges 感）。
- 只用 NES 風調色盤（RETRO_PALETTE 內的顏色）：白色核心 → 淡黃 → 金黃 → 橘色外圈，外面一圈 1 格黑色描邊
  （在草地／森林／水面／石板上都要清楚）。
- 下方 1–2 格高的深色橢圓「地面投影」，光球本體每 ~400ms 上下浮動 1 格（投影不動），營造漂浮感。
- 兩幀脈動：幀 A 標準、幀 B 外圈多一層淡黃光暈像素；四個角落的「閃爍十字星」像素輪流亮滅（每 ~250ms 換一個）。
- 移動中：從光球位置冒出像素火花（1 格方塊），顏色依序 白→淡黃→金黃→橘 階梯變化後消失（**不用半透明漸層**，
  用離散色階維持像素風），壽命約 0.9 秒，**上限 30 顆**；靜止 3 秒後歸零。
- `prefers-reduced-motion`：不浮動、不脈動、不閃爍、無火花，只畫靜態幀 A。
- 跑步前（idle）也在目前位置顯示（靜態呼吸即可）；沒有定位時不畫。
- 移除：勇者四方向 sprite、走路幀、面向判斷（heroDir）、**腳印（FootprintPool）**——光點不會留腳印。
  sprites.ts 內勇者模板與 drawHero 刪除（死碼不留），RETRO_PALETTE 中「勇者追加色」若已無人使用可保留並改註解
  （避免影響其他依調色盤做分類的程式），在回報中說明處理方式。

## 4. 軌跡＝光點走過的路
- 已走路徑改成「像素金色光路」：黑色外框（寬約 4×倍率/2 CSS px）＋金黃主線（寬約 2×倍率/2）、方頭方角
  （lineCap butt、lineJoin miter）；每隔約 8 個邏輯像素在主線上點一顆白色像素當「光粒」，強化「光點拖出的軌跡」。
- 軌跡末端要接到光點目前位置（接點要用已記錄進路線的點，不可用未過濾的即時 GPS 畫出假線——比照 cute v857 審查修正）。
- 訊號中斷分段（polyline 以 `;` 分段）照舊，分段之間不相連。
- 軌跡要一眼能跟道路（石板路／小徑圖塊）區分。
- 長距離效能：全馬 42 km 數千點時每幀不可 O(n) 重算全部投影與樣式（沿用或比照 cute 的快取／抽稀做法）。

## 5. 每公里標誌（保留）
- 沿用現有像素公里旗（drawKmFlag），確認在新軌跡上清楚可見（黑色描邊），位置在該公里數的軌跡點上。

## 6. 偵錯把手 `window.__retroDebug`
移除 heroDir 等勇者欄位；新增 `orbVisible`、`orbScreen {x,y}`、`trailPoints`、`kmFlags`（數量）、
`kmFlagScreens [{km,x,y}]`、`trailScreenSample`（5 點）、`particles`、`setOverlayHidden(bool)`（只供 E2E 有畫/沒畫對照，
預設 false，開始新的一趟時自動重設為 false）；保留 patternImages、tilesLoaded、water frame 等既有欄位。

## 7. 文案
- StyleSettingsModal retro 說明改為：「復古 RPG 大地圖：羊皮紙選單、像素大地圖與魔法光點。」
- 專注模式、page.tsx、globals.css、RaceFocusMode 等註解中「勇者」改為「光點」（只改註解與使用者可見文案，不改邏輯）。

## 8. 驗收（截圖存 `C:\Users\paris\Downloads\retro_preview_r4\`）
1. 截圖：1_track_idle、2_track_running（已跑 ≥1.2 km，看得到光點＋金色光路＋至少一面公里旗）、2b_running_zoom
   （光點＋光路＋公里旗 3 倍放大）、3_focus、4_orb_sheet（4 倍：幀A、幀B、移動含火花、公里旗樣本）、5_track_1280、
   6_style_picker。
2. 有畫/沒畫對照（setOverlayHidden）：orbScreen 12px 內 ΔE ≥ 25；trailScreenSample 可見區內各點 4px 內 ΔE ≥ 20；
   kmFlagScreens 可見區內各點 12px 內 ΔE ≥ 30（安排路線／縮放讓 1 km 點落在面板上緣以上）。
3. 光點像素顏色全部落在 RETRO_PALETTE（含黑）內；地圖上不存在勇者特徵色塊（小井髮色 `#fc7aa4` 與裙色
   `#24188c` 同時出現）。
4. 移動中 particles 1–30；靜止 3 秒後 0；reduced-motion 全程 0。
5. 大安森林公園 idle 3 次：森林紋理完整無圖磚缺塊（左右差 ≤ 25pp、各次一致）；水波動畫仍在前進。
6. 迴歸：cute／scifi／default／非白名單零影響（非白名單零 openfreemap/maplibre/字型請求）；WebGL 不支援 fallback；
   390／430／768／1280 四視窗 0 console error、無橫向捲動；`npx tsc --noEmit`、`npx next build` 通過。
7. 原始碼：app/track/retro/ 內 grep「小井」「勇者」「drawHero」「Footprint」為 0（允許一句移除說明）；
   StyleSettingsModal 文案正確。
