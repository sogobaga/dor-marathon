# DOR 產品宣傳影片（城市探索／GPS 跑步追蹤）

成品：
- `dor-city-explore-promo.mp4`：1920×1080、30fps、47 秒，H.264 Main + AAC，含原創合成配樂
- `dor-city-explore-promo-720p.mp4`：720p 版，適合手機傳送
- `dor-city-explore-promo-vertical.mp4`：直式 1080×1920（9:16），給 Instagram Reels / YouTube Shorts；同一條時間軸與配樂，重要文字避開上方約 220px、下方約 360px 的平台 UI 區

主題只圍繞「城市探索」：打卡 → 關主挑戰 → 卡片收集。只用正式資料，不使用 DORPG 測試素材。

## 分鏡

| 秒數 | 段落 | 內容 |
|---|---|---|
| 0–6 | 開場 | 以 1,057 個正式點位座標畫出全台地圖：1000+ 打卡點、570+ 在地關主 |
| 6–11 | 品牌 | DOR・城市探索、「把城市，變成你的遊戲場」、打卡揭曉／挑戰關主／收集卡片 |
| 11–18 | STEP 01 打卡 | 手機 GPS：走進範圍 → 前往打卡 → 揭曉，隨機獲得 DP＋GP、24h 冷卻可再打卡 |
| 18–25 | 揭曉 | 大安森林公園場景＋大安小鹿立繪，戰鬥台詞、「要接受大安小鹿的叫陣嗎？」 |
| 25–31 | STEP 02 挑戰關主 | 大安小鹿資料、必殺技能「森林節奏接管」、關主課表（暖身／節奏跑／緩和）→ 3★ |
| 31–37 | STEP 03 收集卡片 | 卡片翻面揭曉正式卡面：「已收服此關主・卡片已收藏」 |
| 37–42 | 卡片圖鑑 | 12 張正式關主卡（已收服 3、未收服 9）、3 / 572 |
| 42–47 | 結尾 | 「出發吧！讓城市因你的腳步而精彩」、www.dor.tw |

## 資料來源

- 點位地圖 `points.js`：`services/api/migrations` 063（關主）+ 079（純打卡點）+ 064–068 座標校正，扣除 070 停用點
- 關主卡面／場景／立繪 `assets/`：正式圖床 `img.dor.tw`（`./fetch_assets.sh` 重新下載）；卡面網址取自 `scripts/master_cards.manifest.json`
- 關主文字（名稱、稱號、必殺技、戰鬥台詞）：以正式卡面上印的內容為準（migration 裡的舊文字已在後台更新過）
- 介面文案：`apps/web/src/components/ExploreScreen.tsx`、`CardGalleryScreen.tsx`

## 修改與重新算圖

- `index.html`：橫式影片（CSS 動畫，`animation-delay` = 影片秒數），瀏覽器開 `index.html?t=20` 可預覽第 20 秒
- `index-vertical.html`：直式影片，時間軸與橫式相同；改時間點時兩個檔案要一起改（`music.py` 的 `TRANSITIONS` 也要對齊）
- `music.py`：純 Python 合成配樂（120 BPM，C–G–Am–F）
- `render.mjs`：Playwright 逐格截圖 → ffmpeg 合成

```bash
cd promo/city-explore
./fetch_assets.sh                    # 更新正式圖片（需可連 img.dor.tw）
node render.mjs --still 3,20,34      # 輸出單張預覽 still-*.png
node render.mjs                      # 橫式完整影片（約 4 分鐘）
node render.mjs --page index-vertical.html --out dor-city-explore-promo-vertical.mp4   # 直式版
```

需求：Node + playwright、ffmpeg（PATH 或 `FFMPEG=` 指定）、python3。字型首次執行時自動從 Google Fonts 下載到 `fonts/`（不進 git）。

## GPS 跑步追蹤 功能介紹（直式）

成品：`dor-gps-tracking-promo-vertical.mp4`（1080×1920、47 秒，Reels / Shorts），原始檔 `gps-vertical.html`。
與城市探索直式版共用時間軸節點與配樂。

| 秒數 | 段落 | 內容 |
|---|---|---|
| 0–6 | 開場 | 夜色中畫出跑步軌跡與公里標記：「不用站上起跑線，每一次出門，都算數。」 |
| 6–11 | 標題 | GPS 跑步追蹤・打開手機，城市就是你的跑道 |
| 11–18 | STEP 01 一鍵開跑 | 「▶ 開始跑步」→「■ 結束並上傳」、GPS 精度、螢幕保持喚醒、未上傳跑步可復原 |
| 18–25 | STEP 02 即時數據 | 距離／時間／平均配速／分段即時配速、綠色軌跡與每公里標記、移動時間 |
| 25–31 | STEP 03 每公里應援 | 啦啦隊＋內建鼓勵語、里程獎勵進度條（每滿 1km +EXP +DP） |
| 31–37 | STEP 04 課表挑戰 | 第 N/M 段、目標配速、「再加速 ↑／稍放慢 ↓／配速剛好 ✓」、星數規則、城市探索關主挑戰 |
| 37–42 | STEP 05 結束並上傳 | 「✓ 已記錄」結果卡、每公里分段、計入活動／賽事、疑似搭車自動排除、Strava／手錶同步 |
| 42–47 | 結尾 | 今天的路線，由你來畫・▶ 開始跑步・www.dor.tw |

介面文字取自 `apps/web/src/app/track/page.tsx`、`components/WorkoutHud.tsx`、`lib/runGoal.ts`；距離／時間／配速與課表目標為示意（畫面有標註）。

```bash
node render.mjs --page gps-vertical.html --out dor-gps-tracking-promo-vertical.mp4
```
