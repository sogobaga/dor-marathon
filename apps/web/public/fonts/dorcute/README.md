# DORCute 字型（cute 溫馨可愛風格專用）

## 來源

- 原始字型：**jf open huninn 2.1**（俗稱「粉圓體」），由 justfont Co., LTD. 釋出。
  下載頁：https://github.com/justfont/open-huninn-font/releases/tag/v2.1
  漢字部分衍生自 **Kosugi Maru**（MOTOYA CO.,LTD.，Apache License 2.0）。
- 授權：SIL Open Font License, Version 1.1（原文見同目錄 `LICENSE.txt`，逐字保留，
  含 Kosugi Maru／Varela Round／jf open huninn 三段版權聲明）。

## 本檔案（`DORCute.woff2`）是什麼

依 OFL 1.1 第 3 條規定——修改版（含子集化）**不得沿用原始的 Reserved Font Name
「open huninn」／「huninn」**——本檔案是對原始字型做的**修改版**：

1. **字符子集化**（glyph subsetting，使用 fonttools `pyftsubset`）：只保留
   - ASCII 可印字元（0x20–0x7E）
   - Big5 Level 1 常用字（俗稱「常用字」，以 Python `big5` codec 解出 0xA440–0xC67E
     範圍所有合法漢字，共 5401 字，這是台灣網頁排版最通行的「常用繁體中文字集」定義）
   - `apps/web/src` 原始碼內實際出現的所有中文字（避免子集化漏字）
   - 常見全形標點符號
   - 合計 5588 個字元，最終字型含 5703 個 glyph。
2. **輸出格式轉為 WOFF2**（原始 ttf 檔 4.68MB → 子集化後 1.10MB）。
3. **改名**：`name` table 的 family（nameID 1/16）、full name（nameID 4）、
   PostScript name（nameID 6）、subfamily（nameID 2/17）一律改為 `DORCute` /
   `DORCute-Regular`，不再使用 `jf-openhuninn` 或任何含 "huninn" 的字串；
   unique identifier（nameID 3）與 trademark（nameID 7）也一併改寫，註明這是
   jf open huninn 的修改衍生版、與 justfont Co., Ltd. 無關聯／非其背書。
   copyright（nameID 0）**保留原始三段聲明全文**，並附加一段說明本次修改內容
   （子集化＋改名）的附註。

以上重新命名符合 OFL 1.1 條款 3（修改版不得使用保留字型名稱）與條款 2（散布時
須隨附原始版權聲明與授權全文）。

## 產生方式（可重現）

```bash
pip install fonttools brotli
# 1) 下載原始字型 jf-openhuninn-2.1.ttf 與 LICENSE（見上方連結）
# 2) 產生字元子集清單：ASCII + Big5 Level 1(5401字) + grep apps/web/src 內所有中文字
# 3) 子集化並轉 woff2：
python -m fontTools.subset jf-openhuninn-2.1.ttf \
  --text-file=subset_chars.txt \
  --output-file=DORCute-subset.ttf \
  --flavor=woff2 --glyph-names --symbol-cmap --legacy-cmap \
  --notdef-glyph --notdef-outline --recommended-glyphs \
  --name-IDs='*' --name-legacy --name-languages='*' --no-hinting
# 4) 用 fontTools TTFont 改寫 name table（family/full/postscript → DORCute），
#    另存為 DORCute.woff2（見 CuteBackground 同批工人的 rename 腳本）
```

## 使用方式

**只在 `data-skin="cute"` 生效時**才會被瀏覽器抓取——見
`apps/web/src/components/cute/fonts.ts` 的 `loadCuteFont()`，用 FontFace API
在 cute 背景元件掛載時載入一次並註冊到 `document.fonts`，非 cute 使用者的頁面
完全不會發出這個字型檔的網路請求，`globals.css` 也不宣告任何
`@font-face { font-family: DORCute; ... }` 規則（避免全站使用者被迫下載）。

字型家族名稱：`DORCute`（CSS `font-family: 'DORCute', ...` fallback 鏈）。
