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

export function metersPerPixel(lat: number, zoom: number): number {
  return (156543.03392 * Math.cos((lat * Math.PI) / 180)) / Math.pow(2, zoom)
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

// 沿一串已投影的螢幕座標點，每隔 stepPx 取一個內插點（供每 100m 小愛心／每公里旗子等等距標記使用，
// 呼叫端先把「每隔幾公尺」換算成沿線比例交給這裡，這裡只管沿螢幕折線等距插值，不觸碰地理計算）。
export function pointsAtInterval(pts: { x: number; y: number }[], stepPx: number): { x: number; y: number }[] {
  if (pts.length < 2 || stepPx <= 0) return []
  const out: { x: number; y: number }[] = []
  let carry = 0
  for (let i = 1; i < pts.length; i++) {
    const a = pts[i - 1], b = pts[i]
    let segLen = Math.hypot(b.x - a.x, b.y - a.y)
    if (segLen <= 0) continue
    let pos = carry
    while (pos < segLen) {
      const t = pos / segLen
      out.push({ x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t })
      pos += stepPx
    }
    carry = pos - segLen
  }
  return out
}
