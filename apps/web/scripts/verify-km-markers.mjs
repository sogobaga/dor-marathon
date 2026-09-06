// 驗證 apps/web/src/lib/kmMarkers.ts（直接 import 實際檔案，非重寫邏輯；Node 24 原生 TS type-stripping）
// 執行位置：apps/web 下 `node --experimental-strip-types scripts/verify-km-markers.mjs`（路徑以本檔為基準，不寫死）
//
// 涵蓋（見任務規格）：單一直線邊的整公里內插分數位置、跨段落（含斷訊缺口）距離延續但缺口本身
// 不算一條邊、同一條邊一次跨過多個整公里、空/單點段的防呆。
const modUrl = new URL('../src/lib/kmMarkers.ts', import.meta.url).href
const { kmMarkerPositions } = await import(modUrl)

let pass = 0, fail = 0
function eq(actual, expected, label) {
  const a = JSON.stringify(actual), e = JSON.stringify(expected)
  if (a === e) { pass++; console.log(`PASS ${label}`) }
  else { fail++; console.log(`FAIL ${label}\n  actual:   ${a}\n  expected: ${e}`) }
}
function close(actual, expected, eps, label) {
  if (Math.abs(actual - expected) <= eps) { pass++; console.log(`PASS ${label} (${actual})`) }
  else { fail++; console.log(`FAIL ${label}\n  actual:   ${actual}\n  expected ~${expected} (±${eps})`) }
}

// ── 1) 直線 2.5km（沿同一緯度，經度均勻位移）→ km1 在 40%、km2 在 80% 處 ──
{
  const lat = 25.0
  const totalM = 2500
  // 1 度經度在此緯度的公尺數（近似）：用來反推「移動 totalM 公尺」要位移多少經度，構造精確測試軌跡
  const mPerDegLng = (Math.PI / 180) * 6371000 * Math.cos(lat * Math.PI / 180)
  const dLng = totalM / mPerDegLng
  const track = [[[lat, 121.0], [lat, 121.0 + dLng]]]
  const marks = kmMarkerPositions(track)
  eq(marks.map((m) => m.km), [1, 2], '2.5km 直線 → 只有 km1、km2 兩個標記')
  if (marks.length === 2) {
    close(marks[0].lng, 121.0 + dLng * 0.4, 1e-9, 'km1 在 40% 處（經度內插）')
    close(marks[1].lng, 121.0 + dLng * 0.8, 1e-9, 'km2 在 80% 處（經度內插）')
    eq(marks[0].lat, lat, 'km1 緯度不變（同緯度直線）')
  }
}

// ── 2) 0.7km + 缺口 + 0.7km：累積距離跨段落延續(1.4km 在第二段內插出 km1)，但缺口本身不算一條邊 ──
{
  const lat = 25.0
  const mPerDegLng = (Math.PI / 180) * 6371000 * Math.cos(lat * Math.PI / 180)
  const seg1M = 700, seg2M = 700
  const dLng1 = seg1M / mPerDegLng
  const dLng2 = seg2M / mPerDegLng
  // 第二段刻意從完全不同的經度起跳（模擬斷訊/跳點期間被排除、位置不連續的缺口）
  const seg1 = [[lat, 121.0], [lat, 121.0 + dLng1]]
  const seg2 = [[lat, 130.0], [lat, 130.0 + dLng2]]
  const marks = kmMarkerPositions([seg1, seg2])
  eq(marks.map((m) => m.km), [1], '0.7km+缺口+0.7km → 只有 1 個 km1 標記（缺口本身不產生標記）')
  if (marks.length === 1) {
    // km1 落在第二段：累積 700(段一) + x = 1000 → x=300，即第二段的 300/700 處
    close(marks[0].lng, 130.0 + dLng2 * (300 / 700), 1e-9, 'km1 落在第二段內 300/700 處（累積距離延續）')
  }
}

// ── 3) 單一 3.2km 邊 → 一次跨過 km1/km2/km3，依序排列 ──
{
  const lat = 25.0
  const mPerDegLng = (Math.PI / 180) * 6371000 * Math.cos(lat * Math.PI / 180)
  const dLng = 3200 / mPerDegLng
  const track = [[[lat, 121.0], [lat, 121.0 + dLng]]]
  const marks = kmMarkerPositions(track)
  eq(marks.map((m) => m.km), [1, 2, 3], '單一 3.2km 邊 → km1,2,3 依序（同一條邊可跨多個整公里）')
}

// ── 4) 防呆：空陣列／單點段／完全沒有段落 ──
eq(kmMarkerPositions([]), [], '空 track → []')
eq(kmMarkerPositions([[]]), [], '單一空段 → []')
eq(kmMarkerPositions([[[25.0, 121.0]]]), [], '單點段（只有 1 個點，沒有邊）→ []')
eq(kmMarkerPositions([[[25.0, 121.0]], [[25.0, 121.001]]]), [], '兩個各自單點的段 → 仍是 []（沒有任何一段有邊）')

// ── 5) 距離不足 1km → 沒有任何標記 ──
{
  const lat = 25.0
  const mPerDegLng = (Math.PI / 180) * 6371000 * Math.cos(lat * Math.PI / 180)
  const dLng = 400 / mPerDegLng
  eq(kmMarkerPositions([[[lat, 121.0], [lat, 121.0 + dLng]]]), [], '400m（不足 1km）→ 沒有標記')
}

console.log(`\n${pass} passed, ${fail} failed`)
if (fail > 0) process.exit(1)
