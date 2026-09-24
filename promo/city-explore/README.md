# DOR｜城市探索 產品宣傳影片

成品：`dor-city-explore-promo.mp4`（1920×1080、30fps、44 秒、H.264 Main + AAC，約 9.8MB，含原創合成配樂）

## 分鏡

| 秒數 | 段落 | 內容 |
|---|---|---|
| 0–4 | 開場 | 夜晚信義街景：「每天經過的街角，都藏著你還沒發現的冒險。」 |
| 4–9 | 品牌 | DOR・城市探索 Logo、「把城市，變成你的遊戲場」、三大主軸 |
| 9–15 | STEP 01 發現 | 地圖路線動畫、神秘打卡點（紫/金/綠）、全台 1,058 個打卡點計數 |
| 15–22 | STEP 02 打卡揭曉 | 手機 GPS 範圍打卡、+DP/+GP、大安森林公園場景揭曉＋CHECK IN 章 |
| 22–29 | STEP 03 挑戰關主 | 大安小鹿卡片、台詞、3★ 收服、全台 572 位關主 |
| 29–35 | 收集 | 六張場景卡牆＋打卡點分類 |
| 35–39 | 主視覺 | brand-hero-v3 |
| 39–44 | CTA | 「出發吧！讓城市因你的腳步而精彩」、www.dor.tw |

## 修改與重新算圖

- `index.html`：整支影片（CSS 動畫，`animation-delay` = 影片秒數），素材直接引用 `apps/web/public/`。瀏覽器開 `index.html?t=20` 可預覽第 20 秒。
- `music.py`：純 Python 合成配樂（120 BPM，C–G–Am–F）。
- `render.mjs`：Playwright 逐格截圖 → ffmpeg 合成。

```bash
cd promo/city-explore
node render.mjs --still 3,13,25      # 輸出單張預覽 still-*.png
node render.mjs                      # 算完整影片（約 4 分鐘）
```

需求：Node + playwright、ffmpeg（PATH 或 `FFMPEG=` 指定）、python3。字型（Noto Sans TC / Space Grotesk）首次執行時自動從 Google Fonts 下載到 `fonts/`（不進 git）。
