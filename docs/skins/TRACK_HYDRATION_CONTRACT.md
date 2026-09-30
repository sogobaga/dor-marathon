# 進跑步頁整頁先閃預設：修正契約（v867）

使用者（2026-10-01，v1.2.866）：「在進入GPS跑步追蹤頁時，仍然會先跳"預設"再跳"風格"的介面」、「整個都是預設的風格，然後才切換成指定的風格」。

## 根因（實測，scratchpad trackflash/H_*）
- `/track` 是**建置時預先產生的靜態頁**（prerender），面板常駐那行 `status==='idle' ? fmtDateBig(new Date())`
  （track/page.tsx 約 3313 行）把「建置當下的日期」（Railway 為 UTC）凍結在 HTML 裡。瀏覽器 hydration 時算出的
  是使用者當下的台灣日期 → 文字不一致 → React 19 **#418 hydration mismatch** → 從根節點改為 client render。
- React 19 client render 根節點時，`acquireSingletonInstance` 會**移除 `<html>` 上所有屬性**（node_modules/next/dist/
  compiled/react-dom …acquireSingletonInstance：`removeAttributeNode` 迴圈）→ 開機腳本設好的 `data-skin` 被清掉 →
  整頁回到預設，直到 SkinOverride 等 dashboard 回來才重新套用（實測 cute：100ms 有 → 250ms 被 React 移除 →
  ~700–1000ms 才回來）。
- 正式站匿名開 /track 3/3 次都丟 #418（2026-10-01）。只影響**整頁載入** /track（網址直接開、`<a href>`、重新整理、
  分頁還原、部署後第一次導覽等）；首頁「▶ 開始跑步」站內切頁實測不觸發。

## 修法（範圍）
1. **消除 /track 的 hydration mismatch**：idle 那行日期改為「掛載後才顯示」（例如 useSyncExternalStore／mounted
   旗標：伺服器與 hydration 那一輪輸出相同的占位，掛載後才算 `new Date()`；掛載後的行為與今天相同，每次重繪取當下日期）。
   並全面檢查 /track 首次 render 樹（含 WorkoutHud、BossChallengePanel、RaceFocusMode、CheerShow 等）與 root layout
   各元件，還有沒有其他「SSR 與瀏覽器第一輪輸出不同」的地方（時間、亂數、localStorage／window 讀取影響輸出），一併修正。
2. **防線：data-skin 釘選**（任何未來的 hydration mismatch、或其他意外移除都不再讓整頁閃預設）：
   - 開機腳本套用覆寫時設 `window.__dorSkinPin = <skin>` 並在 `<html>` 裝 MutationObserver：只要 `data-skin`
     與 pin 不同就立刻設回（MutationObserver 是 microtask，在瀏覽器繪製前執行）；同時把 meta theme-color 設回該風格色。
   - `applySkinOverride` 先更新 pin 再設屬性（沒有 observer 就補裝）；`restoreOriginalSkin` 先把 pin 設為 null 再移除
     屬性——**刻意的收回一律照舊生效**。
3. **其他整頁載入頁面**：把正式站可匿名開啟的 SSR 頁（/track/history、/event/<slug>、/privacy、/terms、/support 等）
   也掃一次 #418/#423/#425，有則同法修正（本契約範圍內只修「SSR 與第一輪 client 輸出不同」這類問題）。

4. **（拆到 v868，v867 不含）風格地圖不因單一圖塊失敗就整張退回預設地圖**——三輪審查顯示「忽略圖塊錯誤」會打開
   「全部圖塊失敗但 MapLibre 仍觸發 load（errored 也算 loaded）→ 永遠空白、無 fallback」的洞，需另加 tile-stall 看門狗，
   牽涉 visibility 暫停與 recreateInstance 重置，獨立驗收後再上線（進度：git stash「v868-map-tile-error-wip」）。原描述：（WebKit 重現時發現的潛在問題，trackflash/C_*）：SciFiMap／RetroMap／
   CuteMap 目前 `map.on('error', …)` 收到**任何** MapLibre error 事件都當致命錯誤 → 退回 Leaflet。行動網路下單一圖塊
   逾時／404 就會讓整張風格地圖變回預設地圖。改為：帶 `sourceId`／`tile` 的圖塊／來源錯誤只記錄不退回；只有樣式本身
   載入失敗（地圖 'load' 前、非圖塊錯誤）、WebGL 不支援、context lost 未復原、載入逾時才退回（既有 v860 規則不變）。

## 不在範圍
- 站內切頁（Link／router.push）進 /track 的其他延遲（另一路調查結果若有，另列）。
- 開機腳本判斷條件、SkinOverride 閘門、背景層邏輯（v866）不變。

## 驗收
- 本機以 **UTC 伺服器**（`TZ=UTC node .next/standalone/server.js`；`npx next start` 在本機 git-bash 不會帶 TZ）＋瀏覽器
  timezoneId Asia/Taipei，且建置日期與測試日期不同（或以 Date 覆寫模擬隔天）：整頁載入 /track（idle、有未上傳紀錄、
  帶 ?focus=／?strategy=／?from=race、登入／未登入）0 個 #418/#423/#425；data-skin 全程不被移除（instrument
  removeAttributeNode）；三種風格第一次繪製到穩定全程為風格外觀。
- 防線單測：人為在頁面上移除 data-skin（模擬 React 清屬性）→ 下一次繪製前已恢復；登出／入口收回／改預設 → 照常收回、
  不被釘回。
- 圖塊錯誤：攔截單一圖塊回 500／逾時 → 三種風格地圖都**不**退回；樣式檔回 500 → 照常退回 Leaflet（data-map-fallback）。
- 首頁站內切頁與 v866 驗收項目不退步；tsc、next build 通過；0 console error。
- 另案（不在本次）：WebKit 下復古地圖畫出像素草原約需 4 秒（其餘約 1.2–1.8 秒）、站內切頁進 /track 前 1.6–1.8 秒下載程式。
