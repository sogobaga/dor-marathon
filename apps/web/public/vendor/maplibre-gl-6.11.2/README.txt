未來科幻世界（scifi）GPS 地圖用的 maplibre-gl web worker 靜態檔（2026-09-27 新增）。

由 node_modules/maplibre-gl/dist/ 原樣複製（只移除結尾的 //# sourceMappingURL 註解，其餘位元組不變）：
  - maplibre-gl-worker.mjs
  - maplibre-gl-shared.mjs（worker 的相對 import，缺這個檔案 worker 會直接載入失敗）

為什麼需要這份複本：maplibre-gl 6.x 內部靠 import.meta.url 自動推算 worker 網址，但 webpack 對它
（第三方預先打包好的 .mjs、透過變數傳遞、非字面 `new URL('...', import.meta.url)` 樣式）沒有正確
改寫成 public asset URL，build 後會被展開成建置機器的絕對檔案路徑，導致 worker 100% 建立失敗、
scifi 地圖 100% 退回 Leaflet（詳見 track/scifi/SciFiMap.tsx 對應註解）。改為在程式碼裡把
maplibre-gl 的 config.WORKER_URL 明確指到這個 same-origin 靜態檔，完全繞開該推算邏輯。

⚠️ 升級 apps/web/package.json 的 maplibre-gl 版本時，必須同步：
  1. 重新從新版 node_modules/maplibre-gl/dist/ 複製這兩個檔案到一個新版號的資料夾（例如
     public/vendor/maplibre-gl-6.12.0/），
  2. 更新 track/scifi/SciFiMap.tsx 裡 MAPLIBRE_WORKER_URL 常數的路徑，
  3. 可以刪除舊版號資料夾。
版本不同步不會讓建置失敗，但 worker 執行期通訊協定版本不符時可能拋錯——會被既有 fail()/onFallback
機制接住退回 Leaflet（跑步邏輯不受影響），只是科幻地圖又會失效，記得跑一次 Playwright #1 覆核。
