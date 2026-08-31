// 驗證 apps/web/src/lib/imageCompress.ts 的純邏輯（直接 import 實際檔案，非重寫邏輯；Node 24 原生 TS type-stripping）
// 執行位置：apps/web 下 `node --experimental-strip-types scripts/verify-image-compress.mjs`（路徑以本檔為基準，不寫死）
//
// ⚠️ 範圍限制：本檔只驗證「不碰瀏覽器 API」的三個純函式——
//   computeTargetSize（長邊縮放換算）、toJpegFileName（副檔名處理）、shouldUseCompressed（是否採用壓縮結果）、
//   needsDownscale（是否需要縮放，純輔助判斷）。
//   compressImageFile() 本體依賴 createImageBitmap / <img> / <canvas> / URL.createObjectURL 等瀏覽器 API，
//   Node 環境沒有這些（也不該用 jsdom 之類的東西假造——canvas 繪圖與 toBlob 編碼結果假造了也驗不出真的壓縮
//   行為對不對），這部分的正確性只能在瀏覽器裡人工測試：選一張 iPhone 拍的原圖（通常 3–12MB）上傳，
//   確認 Network 面板送出的檔案已經是縮小後的 image/jpeg、且成功送達後端。
const modUrl = new URL('../src/lib/imageCompress.ts', import.meta.url).href
const {
  computeTargetSize, toJpegFileName, shouldUseCompressed, needsDownscale, DEFAULT_MAX_DIM, DEFAULT_QUALITY,
} = await import(modUrl)

let pass = 0, fail = 0
function eq(actual, expected, label) {
  const a = JSON.stringify(actual), e = JSON.stringify(expected)
  if (a === e) { pass++; console.log(`PASS ${label}`) }
  else { fail++; console.log(`FAIL ${label}\n  actual:   ${a}\n  expected: ${e}`) }
}

// ── 預設值 ──────────────────────────────────────────────────────
eq(DEFAULT_MAX_DIM, 1600, '預設長邊上限 1600px')
eq(DEFAULT_QUALITY, 0.82, '預設 JPEG 品質 0.82')

// ── computeTargetSize：長邊縮放換算 ───────────────────────────────
eq(computeTargetSize(4032, 3024), { width: 1600, height: 1200 }, '橫圖 4032x3024（iPhone 常見尺寸）→ 長邊縮到 1600，等比縮小')
eq(computeTargetSize(3024, 4032), { width: 1200, height: 1600 }, '直圖 3024x4032 → 長邊(高)縮到 1600，寬等比縮小')
eq(computeTargetSize(1600, 1200), { width: 1600, height: 1200 }, '長邊剛好等於門檻 → 不縮放')
eq(computeTargetSize(800, 600), { width: 800, height: 600 }, '長邊小於門檻（小圖）→ 原樣回傳，不放大')
eq(computeTargetSize(1601, 900), { width: 1600, height: 899 }, '長邊剛超過門檻 1px → 仍觸發縮放')
eq(computeTargetSize(5000, 5000), { width: 1600, height: 1600 }, '正方形圖 → 長寬同比縮放')
eq(computeTargetSize(4032, 3024, 800), { width: 800, height: 600 }, '自訂 maxDim=800 → 依自訂門檻縮放')
eq(computeTargetSize(0, 0), { width: 0, height: 0 }, '寬高為 0（非法輸入）→ 不炸、回傳 0')
eq(computeTargetSize(NaN, 100), { width: 0, height: 100 }, '寬為 NaN（非法輸入）→ 不炸，回傳可用的高度')

// ── needsDownscale：是否會觸發縮放 ─────────────────────────────────
eq(needsDownscale(4032, 3024), true, '長邊超過門檻 → 需要縮放')
eq(needsDownscale(800, 600), false, '長邊小於門檻 → 不需要縮放')
eq(needsDownscale(1600, 1200), false, '長邊剛好等於門檻 → 不需要縮放')

// ── toJpegFileName：副檔名處理 ─────────────────────────────────────
eq(toJpegFileName('IMG_1234.HEIC'), 'IMG_1234.jpg', 'HEIC 副檔名 → 換成 .jpg（大小寫副檔名皆處理）')
eq(toJpegFileName('photo.png'), 'photo.jpg', 'PNG 副檔名 → 換成 .jpg')
eq(toJpegFileName('照片.jpeg'), '照片.jpg', '中文檔名保留，副檔名正規化為 .jpg')
eq(toJpegFileName('no-extension'), 'no-extension.jpg', '沒有副檔名 → 直接補上 .jpg')
eq(toJpegFileName('a.b.c.jpg'), 'a.b.c.jpg', '檔名本身含多個點 → 只去掉最後一段副檔名')
eq(toJpegFileName(''), 'image.jpg', '空字串檔名 → 用預設檔名 image.jpg（不炸）')
eq(toJpegFileName('   '), 'image.jpg', '純空白檔名 → 用預設檔名 image.jpg')

// ── shouldUseCompressed：是否採用壓縮結果 ───────────────────────────
eq(shouldUseCompressed(8_000_000, 1_200_000), true, '壓縮後明顯變小 → 採用壓縮結果')
eq(shouldUseCompressed(500_000, 500_000), false, '壓縮後大小相同（沒縮到）→ 不採用，回退原檔')
eq(shouldUseCompressed(300_000, 450_000), false, '壓縮後反而變大（已是高度壓縮的小圖）→ 不採用，回退原檔')
eq(shouldUseCompressed(1_000_000, 0), false, 'toBlob 回傳大小 0（等同失敗）→ 不採用，回退原檔')
eq(shouldUseCompressed(1_000_000, -1), false, '非法負值 → 不採用（防呆）')

console.log(`\n${pass} passed, ${fail} failed`)
if (fail > 0) process.exit(1)
