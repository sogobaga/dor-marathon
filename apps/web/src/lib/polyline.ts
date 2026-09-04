// 解碼 Google encoded polyline（精度 1e5）→ [[lat,lng],...]
export function decodePolyline(str: string): [number, number][] {
  if (!str) return []
  let index = 0, lat = 0, lng = 0
  const coords: [number, number][] = []
  while (index < str.length) {
    let b: number, shift = 0, result = 0
    do { b = str.charCodeAt(index++) - 63; result |= (b & 0x1f) << shift; shift += 5 } while (b >= 0x20)
    lat += (result & 1) ? ~(result >> 1) : (result >> 1)
    shift = 0; result = 0
    do { b = str.charCodeAt(index++) - 63; result |= (b & 0x1f) << shift; shift += 5 } while (b >= 0x20)
    lng += (result & 1) ? ~(result >> 1) : (result >> 1)
    coords.push([lat / 1e5, lng / 1e5])
  }
  return coords
}

// 後端斷訊/跳點排除上線後（見 apps/web/src/app/track/page.tsx 的 GAP_MAX_S/GAP_MAX_M），一趟軌跡可能
// 存成多段 encoded polyline 以 ';' 相接（每段各自獨立編碼、座標從 0,0 重新累加）；舊資料沒有 ';'，
// 整串就是單一段，行為與 decodePolyline 完全相同。呼叫端應每段各畫一條 Leaflet polyline，段落之間
// 不連線——那段被排除的訊號中斷/跳點距離本來就不該畫成一條直線。
export function decodePolylineSegments(str: string): [number, number][][] {
  if (!str) return []
  // ⚠️ 分隔符是 ';'（ASCII 59，不在 polyline 字元集 63~126 內）。v772 曾用 '|'（124，在字元集內）→ 舊軌跡裡合法的 '|'
  // 被錯切、解成 (0,0)（2026-09-04 回報）。絕不可再用字元集內的字元分段。
  return str.split(';').map(decodePolyline).filter((seg) => seg.length > 0)
}
