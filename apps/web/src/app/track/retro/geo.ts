// 復古 RPG（retro）GPS 地圖 — 幾何/相容性小工具（CONTRACT.md §4）。
// 刻意不 import track/scifi/SciFiMap.tsx（那支檔案有 `maplibreConfig.WORKER_URL = ...` 等頂層 side
// effect，import 它會把 scifi 的 runtime 一起拖進 retro 動態 chunk，違反 bundle 隔離）——這裡把 retro
// 用得到的幾個純函式各自複製一份，兩份風格各自獨立、互不依賴。

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

// 依地理座標＋半徑(公尺)算出地面圓的多邊形頂點（pitch/bearing 恆為 0，純俯視，比 scifi 版本簡單——
// 不需要考慮透視橢圓，單純一個正圓的地理座標環）。
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
