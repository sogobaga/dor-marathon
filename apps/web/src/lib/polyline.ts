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
