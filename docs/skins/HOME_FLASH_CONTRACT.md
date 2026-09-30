# 回首頁先閃預設畫面：修正契約（v866）

使用者（2026-09-30，v1.2.865 之後）：「回到首頁時，仍然會先顯示"預設畫面"才切換成"指定的風格畫面"」。

## 根因（三路調查一致，scratchpad homeflash/）
所有回首頁路徑都是 `<a href="/">` 整頁載入（track/page.tsx「← 返回」、history「首頁」…），每次都從頭開機。
開機腳本 `skinOverrideBootJs` 在第一次繪製前已把 `<html data-skin>` 設對（CSS 色票第一格就對，已量測），但：

1. **風格背景層晚到**：`components/SkinOverride.tsx` 的 `active` 初值 null，要等 useUser effect＋dashboard
   回應（Gate 2a）才掛 ParticleField／RetroBackground／CuteBackground（各自 dynamic chunk）。retro／cute 首頁
   `--bg: transparent` 就是為了透出這層（像素草原／彩色紙屑），背景沒掛前只剩平面底色。Neon 睡著時 dashboard
   可能數秒。實測（CPU 4x＋3G）：retro ≈ 2.6–3.0 秒、cute ≈ 3.9 秒才完整。
2. **風格字型晚到**：DORPixel（retro）／DORCute（cute）只在背景層掛載時才開始下載（components/{retro,cute}/fonts.ts），
   之前是系統字型；`/fonts/*` 沒有快取標頭，每次整頁載入都要重新驗證。
3. **瀏覽器狀態列色（meta theme-color）**：開機腳本沒設，SSR 恆為預設 `#09090f`，要等 dashboard 才改。
4. **iPhone 返回手勢的「DOR 載入中…」白幕用預設色**：`layout.tsx` bootJs 在 `back_forward` 一律判 restore
   → 用 SSR skin（預設）的顏色蓋全螢幕再 replace；middleware 到站彈跳頁同樣只知道 SSR skin。

## 修法（範圍）
1. **背景層跟著 `<html data-skin>` 走**：SkinOverride 以
   `useSyncExternalStore(subscribeSkinChange, getActiveSkin, getSkinServerSnapshot)` 決定掛哪個背景層（不再用只在
   dashboard 回來後才設的 `active` state）。套用／收回邏輯（Gate 0/1/2a/2b、applySkinOverride／restoreOriginalSkin）
   **完全不變**——收回時 data-skin 被移除，背景層自然卸載。站內 active_skin 只允許 default/warm/warm2
   （layout.tsx skinOf），所以 data-skin 為 scifi/retro/cute 只可能來自帳號覆寫。
2. **背景 chunk 提早抓**：SkinOverride 模組載入時（hydration 前，只在瀏覽器）若 data-skin 已是覆寫風格，就先
   `import()` 對應背景元件（與 next/dynamic 同一個模組，webpack 去重）。
3. **開機腳本補齊**（僅在覆寫記錄 uid 與 dor_user 相符、已設 data-skin 的同一條件下）：
   - 設 `meta[name=theme-color]` 為該風格主題色（色表單一來源移到 lib/skinColors.ts，lib/skinOverride.ts 改引用）。
   - retro／cute：立即以 FontFace 註冊並開始下載該風格字型（family／URL 與 fonts.ts 同一份常數；fonts.ts 的
     already 檢查會自動略過重複註冊）。
   - 維持 `dor_skin_ov` cookie（見 4）。
4. **白幕／彈跳頁顏色跟著帳號風格**：
   - layout.tsx：`skinOverrideBootJs` 移到 `bootJs` 之前；bootJs 讀已設好的 data-skin，是覆寫風格就用該風格的白幕色
     （色表在 lib/skinColors.ts，與頁面第一格底色一致）。
   - applySkinOverride 設 cookie `dor_skin_ov=<skin>`（Path=/、SameSite=Lax、Secure、一年），restoreOriginalSkin
     清除；middleware 彈跳頁若 cookie 值為 scifi/retro/cute 就用該色（純外觀，彈跳頁 ≤600ms）。
5. **字型快取**：next.config.mjs 對 `/fonts/:path*` 加 `Cache-Control: public, max-age=2592000, stale-while-revalidate=86400`
   （字型檔日後若要換內容須改檔名）。

## 不在範圍
- 觸發條件與治療流程（arrival／restore／2 分鐘節流、middleware 判斷）一律不改，只改顏色。
- 預設／warm 帳號：開機腳本不觸發 → 無背景層、無字型、無 cookie、theme-color 與今天相同。
- scifi 的 `orbitron.variable` 從未掛到根節點（`--font-scifi-orbitron` 恆空）是既有問題，另案處理。

## 驗收
- tsc、next build 通過；0 hydration 警告／console error。
- 量測（CPU 4x＋網路節流、dashboard 延遲 1500ms、DPR3 390×844，scifi/retro/cute × 「← 返回」與重新整理）：
  第一次繪製時 theme-color 已是風格色；背景層在 dashboard 回應**之前**掛上；retro/cute 字型在第一次繪製時已在
  document.fonts（loading 或 loaded）。
- iPhone（CriOS UA、停用 bfcache、清掉 2 分鐘節流）返回手勢觸發 restore 時，白幕為該風格色；帶 `dor_skin_ov`
  cookie 的外部到站彈跳頁為該風格色。
- 迴歸：預設帳號零差異（無背景、無字型請求、無 cookie、theme-color 預設）；登出／入口被收回／ui_skin 改預設 →
  背景卸載、data-skin 移除、cookie 清除、theme-color 還原；別人的覆寫記錄 → 開機腳本不套用；即時切換
  scifi→cute 背景同步更換；/track、/track/history 地圖閃爍修正不退步。
