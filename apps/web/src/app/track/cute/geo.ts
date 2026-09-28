// 溫馨可愛（cute）GPS 地圖 — 幾何/相容性小工具。逐字複製 track/retro/geo.ts 的純函式（同一個理由：
// 不 import track/scifi 或 track/retro 的檔案，避免把對方的頂層 side effect／runtime 一起拖進 cute
// 動態 chunk，三套風格各自獨立、互不依賴）。

export function isWebglSupported(): boolean {
  try {
    const canvas = document.createElement('canvas')
    return !!(canvas.getContext('webgl2') || canvas.getContext('webgl') || canvas.getContext('experimental-webgl'))
  } catch {
    return false
  }
}

export function haversineM(a: [number, number], b: [number, number]): number {
  const R = 6371000, rad = Math.PI / 180
  const dLat = (b[0] - a[0]) * rad, dLng = (b[1] - a[1]) * rad
  const x = Math.sin(dLat / 2) ** 2 + Math.cos(a[0] * rad) * Math.cos(b[0] * rad) * Math.sin(dLng / 2) ** 2
  return R * 2 * Math.atan2(Math.sqrt(x), Math.sqrt(1 - x))
}

// metersPerPixel：地面 1 公尺對應多少「CSS px」，只用來把公尺半徑的目標圈換算成螢幕點擊命中半徑
// （見 CuteMap.tsx onClick 的 pxR = tgt.radius/metersPerPixel(...)）。FIX（根因調查，見 scratchpad
// cute_skin/park_repro/mpp_check.html：用 MapLibre 6.11.2 實例本身的 map.unproject() 在螢幕上取
// 100px 距離、haversine 換算回公尺，跟這條公式的結果對照）——這條公式原本抄自 Google Maps／Leaflet
// 那種「256px 圖磚」慣例（156543.03392 = 赤道周長 40075016.6856m ÷ 256），但 MapLibre／Mapbox GL 的
// zoom 是建立在 512px 圖磚上（node_modules/maplibre-gl/src/geo/transform_helper.ts
// `_tileSize = 512`、`worldSize = tileSize * 2^zoom`），同一個 zoom 數字下世界實際攤開的像素寬度是
// 256px 慣例的兩倍，換算下來每個 CSS px 代表的實際公尺數只有這條公式算出來的一半——大安森林公園
// zoom16.5 實測：舊公式算出 1.5304 m/px，map.unproject() 實測 0.7644 m/px（比值 2.0022），改半後的
// 0.7652 m/px 只差 0.11%（殘差是小範圍球面近似的正常誤差）。這個公式只有這裡（換算點擊命中半徑）
// 用到，繪製虛線圈的 geoCircle() 是直接用公尺→經緯度差再交給 map.project()，不受這個誤差影響——
// 換句話說畫面上的虛線圈本來就是正確的公尺半徑，只有「點下去判不判定有點到」的半徑一直只有畫面看
// 起來的一半，玩家得點得比視覺圈精準兩倍才點得到；改成正確係數（原常數除以 2）修正。
export function metersPerPixel(lat: number, zoom: number): number {
  return (78271.51696 * Math.cos((lat * Math.PI) / 180)) / Math.pow(2, zoom)
}

// 依地理座標＋半徑(公尺)算出地面圓的多邊形頂點（pitch/bearing 恆為 0，純俯視）。
export function geoCircle(lat: number, lng: number, radiusM: number, n = 28): [number, number][] {
  const latRad = (lat * Math.PI) / 180
  const dLat = radiusM / 111320
  const dLng = radiusM / (111320 * Math.cos(latRad) || 1)
  const out: [number, number][] = []
  for (let i = 0; i < n; i++) {
    const t = (i / n) * Math.PI * 2
    out.push([lat + dLat * Math.sin(t), lng + dLng * Math.cos(t)])
  }
  return out
}

export function decimate<T>(arr: T[], n: number): T[] {
  if (arr.length <= n) return arr
  const step = arr.length / n
  const out: T[] = []
  for (let i = 0; i < n; i++) out.push(arr[Math.floor(i * step)])
  out.push(arr[arr.length - 1])
  return out
}
