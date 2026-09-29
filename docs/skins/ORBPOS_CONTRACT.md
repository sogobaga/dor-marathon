# 光點位置修正契約（cute／retro／scifi＋track/page.tsx）

使用者回報（2026-09-28，真機，v1.2.858，溫馨可愛）：「目前光點的位置並不是在 GPS 目前的定位座標上」。
唯讀調查（三路追查＋懷疑式綜合，wf_553df313-994）結論如下；本契約把**所有已證實會讓光點不在（或看起來不在）目前定位上的缺陷**一次修掉。
使用者的要求是字面上的：**光點要畫在最新的 GPS 定位座標上**。

## 已證實的缺陷（附證據）
1. **跟隨鏡頭被自己的程式縮放打斷**（cute `CuteMap.tsx:196` `onZoomStart` 與 `:309`；retro、scifi 需檢查同型）：
   `zoomstart`／`dragstart` 不分「使用者手勢」或「程式 easeTo」，一律 `pauseFollow()` 8 秒。跟隨更新
   `easeTo({center, zoom:16.5})`（`:441`）與「回到目前位置」`recenter()` 的 `easeTo({zoom:16.5})`（`:148`）只要 zoom
   與目前不同就會觸發 `zoomstart` → 跟隨被暫停 8 秒 → 光點（座標正確）漂離可見區中心、甚至出畫面；使用者縮放過
   一次後，之後每次跟隨都會把縮放彈回 16.5 又再暫停 8 秒，形成「每 8 秒才跳一下」。
2. **閒置時可能只拿到一筆粗略、最多 60 秒前的定位就不再更新**（`page.tsx:810-852` 預熱）：權限查詢回 `prompt`
   （iOS/WebKit 常見，即使其實已允許）且本機沒有 `dor:gps-authorized` 旗標時，只做一次
   `getCurrentPosition({enableHighAccuracy:false, maximumAge:60000})`，**沒有持續 watch**——光點與預設地圖標記都
   停在那筆粗略／過時的位置。
3. **冷啟動第一筆是低精度（Wi-Fi／基地台）定位，畫得跟精準定位一模一樣**（`page.tsx:818,821,845-849`）：誤差可達數十公尺到公里，
   沒有任何「還在定位中」的視覺提示，看起來就是「光點不在我這」。
4. **背景回前景**（`page.tsx:2097-2124`）：鎖屏期間位置凍結，回來後到新定位之前光點停在舊點，也沒有提示。
5. **疊層畫布尺寸與 MapLibre 內部尺寸短暫不同步**（`CuteMap.tsx:487-495` 每幀用 container 尺寸，MapLibre 的 resize 有節流）：
   面板拖動／網址列收合時，光點會短暫偏移約 Δ高度/2（常見 25–45px）。
6. **scifi 在沒有定位時把靈魂畫在 localStorage 舊位置或大安森林公園預設點**（`SciFiMap.tsx:513-514`）：可能是另一個城市。

已排除（不要改）：經緯度順序、DPR 換算架構、setPadding 與 project() 不一致、250ms 快照節流（≤0.75 m）、moveAnchor/lastPos 被誤當光點座標。

## 修正要求
- **A. 跟隨鏡頭**（cute、retro、scifi 三個地圖元件，凡有同型寫法）：只有使用者手勢（事件帶 `originalEvent`）才暫停跟隨；
  跟隨更新只移動 center，**保留使用者目前的縮放**（不再每次強制 16.5）；`recenter()` 把縮放回到 16.5（或該 skin 預設）並恢復跟隨，
  不可因自身的 easeTo 又被暫停。+／− 按鈕屬使用者操作，照舊暫停跟隨 8 秒。
- **B. 光點一律畫在最新定位**（字面要求）：`pos` 就是最新定位，光點畫在 `project(pos)`；不做「凍結在上一個好點」。
  但要誠實表達精度：`acc > 65 m`（沿用 MAX_ACC）或最新定位已超過 15 秒沒有更新（背景回前景、訊號中斷）時，光點改為
  「定位中」外觀：本體半透明＋一圈依 `acc` 換算半徑的淡色精度圈（用已修正的 metersPerPixel，圈的半徑上限 120px）＋緩慢脈動
  （reduced-motion 下不脈動）；精度恢復 ≤65 m 且為新定位時回到正常外觀。各 skin 用自己的風格畫（cute 粉色淡圈、retro 像素點狀圈、
  scifi 青色細圈）。軌跡接點規則不變（只接到已記錄點，精度差不接）。
- **C. 閒置預熱**（`page.tsx`，影響所有 skin 與預設地圖，屬正確性修正）：預熱的單次粗略定位**成功**後（證明權限已允許），
  立即啟動與已授權分支相同的持續高精度 watch；不可因此多跳任何權限提示；卸載／開始跑步時清理方式比照既有 `startWatch`。
  其餘 GPS 追蹤、距離、防弊邏輯**一律不動**。
- **D. 畫布與地圖同步**：疊層畫布的 CSS 尺寸與繪製座標系以 MapLibre 自己的 canvas／transform 尺寸為準（例如 `map.getCanvas()` 的
  clientWidth/clientHeight 或 transform.width/height），確保 `project()` 的座標系與畫布完全一致；三個 skin 都改。
- **E. scifi 沒有定位時不畫靈魂**（比照 cute／retro R4）。
- 偵錯把手（`__cuteDebug`／`__retroDebug`／`__scifiDebug`）新增：`orbLatLng`（實際拿來畫光點的座標）、`lastFixAgeMs`、`orbState`
  （'normal'|'searching'）、`following`（bool）、`zoom`，以及測試用 `projectLngLat(lng,lat)`（回傳同一個 map.project 的 CSS px）。

## 驗收（E2E，三個 skin 都跑，含 DPR 3）
Playwright 以 init script 模擬 `navigator.geolocation`（watchPosition／getCurrentPosition），`deviceScaleFactor` 分別 1 與 3：
1. 每餵一筆定位後 ≤300ms：`orbScreen` 與 `projectLngLat(最新定位)` 距離 ≤ 3 CSS px（含冷啟動粗略點、精度差點、正常點）。
2. 冷啟動：先給 `accuracy:800` 偏 550 m 的粗略點 → `orbState='searching'`；接著給精準點 → `orbState='normal'` 且位置到精準點。
3. 權限查詢回 `prompt` 且無 `dor:gps-authorized` 旗標的閒置情境：粗略點成功後，watch 確實啟動（之後餵的點會更新光點）。
4. 使用者縮放：點「−」一次 → `following=false`；8 秒後恢復跟隨時 `zoom` 保持使用者設定（不彈回 16.5）、`following` 維持 true 並持續把光點置中
   （光點與可見區中心距離 ≤ 20 px）；按「回到目前位置」→ `zoom=16.5`、`following=true`，且之後 3 次定位更新都維持跟隨。
5. 停止餵點 16 秒 → `orbState='searching'`；再餵新點 → 'normal'。
6. 面板拖動／視窗高度改變的過程中，每幀 `orbScreen` 與 `projectLngLat(pos)` 距離 ≤ 3 px（連續取樣 1 秒）。
7. scifi 無定位時 `orbVisible=false`。
8. 迴歸：各 skin 既有 E2E（cute verify_cute_r2、retro R4 驗收、scifi 迴歸）、非白名單零影響、預設 skin 的 Leaflet 標記照常、0 console error、
   `npx tsc --noEmit`、`npx next build`。截圖存 `C:\Users\paris\Downloads\orbpos_preview\`（每個 skin：正常／定位中／縮放後跟隨）。

## 補充（編排者檢視 retro R4 自檢截圖）
- retro 公里旗在新金色光路上太小、幾乎看不到（自檢截圖 1 km 處只有一個小橘三角）。本輪一併修：公里旗放大（至少 2 倍）、
  加黑色描邊與白色公里數字，讓它在草地／道路／光路上都一眼可見；驗收比照 cute：kmFlagScreens 可見區內 12px ΔE ≥ 30，
  並由驗收者親自看放大截圖確認。

## ★ 最新確認的主因（2026-09-29，retro R4 worker 以 deviceScaleFactor=4 自檢時發現並已在 RetroMap 修正）
**疊層 `<canvas>` 沒有 CSS width/height**：canvas 是置換元素（replaced element），`position:absolute; inset:0` **不會**把它撐滿，
CSS 盒子會退回「固有尺寸」＝ `width/height` 屬性（= clientWidth×dpr）。所以在 DPR>1 的手機上，整層光點／軌跡／徽章被
**放大 dpr 倍、位置也等比偏移**（iPhone DPR 3：光點畫在 (x·3, y·3)，常常跑出畫面或落在錯的地方）；桌機與 DPR 1 的 E2E 完全看不出來。
這幾乎可以確定就是使用者看到「光點不在目前定位上」的直接原因；也解釋了 v854 使用者覺得「小井太大」。
- 修法：疊層 canvas 一律加 `width:'100%', height:'100%'`（retro 已加）；cute 必修；scifi 必查（有同型寫法就修）。
- 已知 E2E 盲點：之前所有 E2E 都跑 DPR 1。本輪起**所有 skin 的 E2E 一律加跑 deviceScaleFactor 3**，並斷言：
  `canvas.getBoundingClientRect()` 與地圖容器完全相同；`orbScreen` 與 `projectLngLat(最新定位)` ≤ 3 CSS px；
  光點實際像素（有畫/沒畫對照的差異中心）與 `orbScreen` ≤ 4 CSS px。

## 使用者真機實跑回報（2026-09-29，線上 v1.2.858）
> 「實際跑步測試後，發現我的 GPS 定位點並不會顯示在地圖畫面的中央，於是我看不到我移動時的軌跡路徑」
與 ★ 主因（DPR 3 疊層放大 3 倍偏移）及跟隨鏡頭缺陷一致。**追加驗收（E2E 必做，三個 skin、deviceScaleFactor 3、390×844）**：
9. 跑步中（status=tracking、跟隨中、未手動操作）連續餵 60 筆移動定位：每一筆之後 ≤1 秒內，`orbScreen` 與「面板上緣以上可見地圖區的中心點」
   距離 ≤ 20 CSS px（專注模式時為上 45% 區的中心）；光點實際像素（有畫/沒畫差異重心）也在同一範圍。
10. 同一段中，光點後方 30–80 m 的軌跡在畫面可見區內，且在 DPR 3 的截圖上有畫/沒畫 ΔE ≥ 20（軌跡確實看得到）。
11. 截圖（DPR 3）：每個 skin 跑步中一張全畫面，驗收者必須親自看圖並描述「光點是否在可見區中央、軌跡是否從光點往後延伸可見」。

## 第二輪（2026-09-29，使用者實跑用的是 scifi＝未來科技）
使用者帳號目前 ui_skin=scifi。追加確認的缺陷：
- **S1 scifi 沒有面板避讓**：SciFiMap 沒有 `setPadding(bottomInset)`（cute/retro 有），跟隨把靈魂放在「整個容器」正中央＝面板上緣附近；
  scifi 又是 pitch 55＋速度 >1 m/s 時依行進方向轉 bearing（車頭朝上），身後的軌跡整段往畫面下方延伸，**完全被面板蓋住**。
  修法：比照 CuteMap `applyCutePadding`，scifi 也依 `bottomInset`（專注模式＝容器高度 55% 的底部 padding）設 padding，讓靈魂在可見區中心、
  身後軌跡在靈魂與面板之間可見；保留 pitch 55 與車頭朝上。
- **S2「回到目前位置」按鈕在三個 skin 都叫不出來**：按鈕條件 `(!following || !curPos)`（page.tsx ~2775），`following` 只由 Leaflet 的
  dragstart/zoomstart 改變；skin 地圖被拖動時 page 不知道 → 有定位後按鈕永遠隱藏。修法：三個地圖元件新增 prop
  `onFollowChange?: (following: boolean) => void`，在使用者手勢暫停跟隨、8 秒自動恢復、recenter 恢復時呼叫；page.tsx 接上
  `followRef/setFollowing`，按鈕因此出現；按下時照舊呼叫 page recenter＋skin recenter。
- 追加驗收：
  12. scifi（DPR 3、390×844、面板在預設高度）跑步中車頭朝上：靈魂與可見區中心 ≤ 20 px；靈魂身後 30–80 m 的軌跡在面板上緣以上且有畫/沒畫 ΔE ≥ 20。
  13. 三個 skin：使用者手勢拖動地圖 → 「回到目前位置」按鈕出現；按下 → 按鈕消失、跟隨恢復、光點回到可見區中心 ≤ 20 px；8 秒自動恢復時按鈕也消失。
  14. 光點「核心像素」定位（取代之前被判定為雜訊的差異重心法）：cute 白色核心、retro 白色核心、scifi 靈魂最亮核心——在有畫/沒畫差異中，
      只取亮度最高（R,G,B 皆 >235 或該 skin 核心色 ±12）的像素群，其重心與 orbScreen ≤ 4 CSS px（DPR 1 與 3 都要）。
  15. 上一輪 E2E 的偶發項（scifi dpr3 '.fresh' 狀態未回 normal）要連跑 10 次確認：10/10 通過才算，否則找根因。

## 第三輪（2026-09-29，收尾）
- **P1 預設地圖（Leaflet，所有使用者）第一筆定位後就停止跟隨**：page.tsx `ensureMap` 以全台俯視 zoom 7 建圖；第一筆定位
  `centerMap(p,16)` → Leaflet `setView` 改變 zoom 會同步觸發 `zoomstart` → `map.on('dragstart zoomstart')`（~page.tsx:555）
  把 `followRef/following` 設 false → 之後定位不再置中、「回到目前位置」按鈕跳出。修法：centerMap 內的程式移動用
  `programmaticMoveRef` 包起來（setView/panTo 期間為 true，Leaflet 的 zoomstart 在 setView 內同步觸發），handler 遇到程式移動就忽略；
  使用者拖曳（dragstart 只由手勢觸發）與手勢縮放照舊暫停跟隨。boss 置中（~page.tsx:2330-2338）原本就明確設 following=false，保持不變。
- **P2 scifi「定位中」本體半透明**（審查發現：cute/retro 有、scifi 缺）。
- **P3 使用者要求（2026-09-29）：scifi 粒子光點再小一點、更精緻**：在 DPR 修正（原本 iPhone 上被放大 3 倍≈250px）之外，再把靈魂整體縮到約 60%：
  光暈半徑 (10+10i)→約 (6+6i)、核心 7→約 4.5、環繞粒子半徑 14–42→約 8–25、粒子大小 1–3.2→約 0.6–1.9、拖尾粒子大小同比例縮小；
  核心亮度不減（仍要一眼看到）。SCIFI_CONTRACT_R2 §3「核心光暈直徑 ≥36px」由本條取代。
- 驗收追加：
  16. 預設 skin（Leaflet）：idle 首筆定位後 `following` 仍為 true、連續 10 筆定位地圖中心都跟著走（標記在可見區中心 ≤ 20 px）、「回到目前位置」按鈕不出現；
      使用者真的拖曳 → 暫停＋按鈕出現；按下 → 恢復跟隨；使用者手勢縮放 → 暫停。
  17. scifi 靈魂：DPR 3 截圖中靈魂（核心＋光暈＋環繞粒子）外接寬度約 45–60 CSS px（原本設計約 84px），核心在可見區中心 ≤ 20 px，仍清楚可見；
      搜尋中外觀本體半透明＋青色細圈。
  18. cute item 14：核心像素偵測改以光球本體（排除偏左上的高光點）為準，≤ 4 px。
