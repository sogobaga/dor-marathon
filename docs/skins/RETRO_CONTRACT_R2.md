# 復古 RPG 第二輪（地圖圖塊、勇者、背景、字體）— 2026-09-27

前提：第一輪成果在工作區未提交（風格設定、migration 193、retro 主題、RetroMap、小井勇者 v1 均已存在）。CONTRACT.md 全部原則沿用。編排者檢視交付截圖後判定以下不合格：

## 1. 地圖圖塊沒有畫出來（最嚴重）
- 現象：跑步畫面地圖幾乎是**整片純綠**＋兩條很粗的黑色斜線（鐵道），看不到點描草地、森林（大安森林公園應是一大片森林）、石牆城鎮（周邊建築）、紅色石板路／土黃小徑（新生南路、信義路）、水。
- 要求：先**找根因**（pattern 圖片是否 addImage 成功、`styleimagemissing` 時機、低 pixelRatio 對 pattern 的影響、source-layer／filter 是否命中 OpenMapTiles 的 `building`／`landcover`／`park`／`landuse`／`transportation`／`water`、圖層順序／opacity），在回報裡寫出根因與證據（例如 `map.getStyle().layers` 與 `map.queryRenderedFeatures` 在大安森林公園中心的結果、`map.hasImage(...)`）。
- 鐵道改成細（1–2 地圖像素）的灰色枕木線，不可比道路搶眼。
- 驗收（像素分類，**看多樣性不看占比**）：以大安森林公園西北角（新生南路／信義路口附近）為中心跑步 60 秒後，對地圖可見區（面板以上）逐像素依調色盤歸類成 grass／forest／wall(建築)／road(cobble+path)／water／other：forest ≥ 8%、wall ≥ 5%、road ≥ 3%、grass ≥ 10%、且 ≥ 4 類同時 >1%；草地區域內至少 2 種調色盤色（證明有點描紋理而非平塗）。回報各類百分比數值。

## 2. 勇者位置與造型
- 位置：勇者要在**「面板以上可見地圖區」的中心**，不是整個畫面中心（目前一半被底部面板蓋住）。track 頁把底部面板頂端到畫面底的高度（`bottomInset` px，隨面板拖曳更新，節流）傳給 RetroMap；RetroMap 以 `map.setPadding({bottom: bottomInset})` 讓跟隨中心落在可見區，勇者畫在同一點。專注模式開啟時可見區＝上 45%（RaceFocusMode retro 漸層透出區），勇者畫在該區中心。
- 造型：以下 16×24 正面站立圖為**權威底稿**（編排者繪製），其餘方向與步行幀據此衍生、保持同一比例與配色。圖例：`.`透明 `K`#000 `P`#fc7aa4 `p`#c8406c `S`#fcd8a8 `E`#ac7c00(眼) `F`#fc7aa4(腮紅/跑鞋) `R`#e4462c `G`#f8b800 `W`#fcfcfc `A`#bcbcbc `B`#ac7c00(背帶) `N`#24188c `w`#fcfcfc(襪)
```
r00 ....KKKKKKKK....
r01 ...KPPPPPPPPK...
r02 ..KPPPPPPPPPPK..
r03 ..KPPPPPPPPPPK..
r04 .KPPPPPPPPPPPPK.
r05 .KPpPPPPPPPPpPK.
r06 .KPpSSSSSSSSpPK.
r07 .KPKKKKSSKKKKPK.
r08 .KPKSEKKKKESKPK.
r09 .KPKKKKSSKKKKPK.
r10 .KPSFSSSSSSFSPK.
r11 ..KPSSSKKSSSPK..
r12 ...KKSSSSSSKK...
r13 ..KRRRRGRRRRRK..
r14 .KAWRRRRRRRRWAK.
r15 .KAWWBRRWWWWWAK.
r16 .KSWWWGBWWWWWSK.
r17 ..KWWWGWBWWWWK..
r18 ..KNNNNNNNNNNK..
r19 .KNNNNNNNNNNNNK.
r20 .KNNKNNNNNNKNNK.
r21 ....KwK..KwK....
r22 ...KFFK..KFFK...
r23 ...KKKK..KKKK...
```
  - 步行兩幀：左右腳交替抬起（抬起那隻少一列、鞋子上移 1px），手臂 `S` 前後各移 1px，圍巾尾端（r14–r15 的 R）往行進反方向擺 1px。
  - 背面（up）：頭全為髮（P/p，無臉），r13 圍巾結在後頸、左肩後斜出劍柄（A 柄＋G 護手），其餘同比例。
  - 側面（left；right 為鏡像）：單片鏡框（K 框＋E 眼）＋鏡腳 K 延伸到耳側、鼻尖 1px S 突出、瀏海在前、後腦髮量較多、圍巾尾端飄在身後、劍柄露在背側。
  - 交付：以程式直接呼叫 `drawHero()` 在離屏 canvas 畫出 4 方向 × 3 幀（站立＋兩步行）、每格 8 倍放大、格間 8px 灰底間隔，輸出 `C:\Users\paris\Downloads\retro_preview\7_hero_sheet.png`（不可用畫面截圖裁切）。

## 3. 首頁與各頁的像素草原背景
- 現象：首頁背景全黑，RetroBackground 沒透出來。
- 要求：retro 下讓頁面底層容器背景透明（只在 `[data-skin="retro"]` 選擇器下），視窗卡片維持黑底；RetroBackground 置於最底層可見（暗化 .45 維持）。驗收：首頁卡片之間與上下空白區域取樣，非黑像素 ≥ 25% 且以綠色系為主。

## 4. 像素字體確認
- 驗收：`document.fonts.check('16px DORPixel') === true`、`getComputedStyle(document.body).fontFamily` 以 DORPixel 開頭、網路請求中 Cubic_11.woff2 200；截圖可見像素字形。非 retro 帳號 0 次請求該字體。

## 5. 門檻修正（編排者裁決）
- retro 為 pitch 0 俯視，`tilesLoaded ≥ 4` 即可（取代原 ≥10）。

## 6. 其餘驗收沿用 CONTRACT.md §6（風格設定、非白名單零影響、scifi 迴歸、WebGL 退回、StrictMode、四視窗 0 console error），截圖覆蓋 `C:\Users\paris\Downloads\retro_preview\` 1–7。

## 7. 編排者追加（檢視 21:46 的 4_track_running.png 後）— FIX／複驗必須處理
- **track 頁底部面板（可拖曳資訊面板、統計卡、「結束並上傳」CTA 區、提示文字）在 retro 下變成半透明**，地圖圖塊與 RetroBackground 從面板底下透出，文字難讀。§3 的「底層容器透明」只適用首頁／列表等外層頁面容器；**track 頁的面板與 CTA 區必須是不透明黑底（#000）視窗**，地圖只在面板以上的區域顯示。驗收：跑步中截圖在面板區（面板頂端以下）取樣，非黑且非白（視窗框）像素比例 ≤ 3%。
- 勇者 sprite sheet 與首頁像素草原、字體已通過編排者目視（保留，不要改壞）。
