// 揮汗有禮 500.gov.tw 直接截圖需求（2026-09-06 owner 定案，見 lib/gov500.ts 頂部說明）：
// GPS 軌跡地圖上要看得到 1、2、3…號碼標記，代表跑到第 N 公里的分段完成點，讓截圖本身就能佐證
// 「這是真的跑出來的距離」。本檔只放「無 React／無 DOM」的純函式（kmMarkerPositions）＋一個依賴
// 呼叫端傳入 Leaflet 實例(L)/地圖(map) 的輕量渲染 helper（addKmMarkers）——純函式那半可直接用
// Node 原生 TS type-stripping 匯入驗證（見 scripts/verify-km-markers.mjs，比照 lib/runMeet.ts +
// scripts/verify-run-meet.mjs 的既有慣例）。

export interface KmMarkerPoint {
  km: number
  lat: number
  lng: number
}

function haversineM(a: [number, number], b: [number, number]): number {
  const R = 6371000, rad = Math.PI / 180
  const dLat = (b[0] - a[0]) * rad, dLng = (b[1] - a[1]) * rad
  const x = Math.sin(dLat / 2) ** 2 + Math.cos(a[0] * rad) * Math.cos(b[0] * rad) * Math.sin(dLng / 2) ** 2
  return R * 2 * Math.atan2(Math.sqrt(x), Math.sqrt(1 - x))
}

/**
 * 逐段（segment）累積 haversine 距離，每跨過一個整數 km（預設 everyM=1000）就在該段落內插一個座標。
 * 距離累計「跨段落延續」（例如第一段跑了 0.7km、第二段再跑 0.7km，累積是 1.4km、會在第二段內插
 * 出 km1 的點），但兩個段落的「交接處」本身不算一條邊、不會被拿來算距離或內插——那正是訊號中斷/
 * 跳點期間被後端排除的缺口（見 lib/polyline.ts decodePolylineSegments 註解），這段直線距離不可信。
 */
export function kmMarkerPositions(track: [number, number][][], everyM = 1000): KmMarkerPoint[] {
  const out: KmMarkerPoint[] = []
  if (!track || !track.length || everyM <= 0) return out
  let cum = 0 // 累積距離：跨段落延續，只是段落交接處那條「邊」本身不計入（見上方註解）
  let nextKm = 1
  for (const seg of track) {
    if (!seg || seg.length < 2) continue // 空/單點段沒有邊可算距離，略過
    for (let i = 1; i < seg.length; i++) {
      const a = seg[i - 1], b = seg[i]
      const d = haversineM(a, b)
      if (d <= 0) continue
      const start = cum
      const end = cum + d
      while (nextKm * everyM <= end) {
        // 同一條邊可能跨過不只一個整公里（例如單一段落一路直線 3.2km），while 迴圈依序內插、km 遞增
        const target = nextKm * everyM
        const frac = (target - start) / d
        out.push({ km: nextKm, lat: a[0] + (b[0] - a[0]) * frac, lng: a[1] + (b[1] - a[1]) * frac })
        nextKm++
      }
      cum = end
    }
  }
  return out
}

let stylesInjected = false
function ensureKmMarkerStyles(): void {
  if (stylesInjected || typeof document === 'undefined') return
  stylesInjected = true
  const style = document.createElement('style')
  // 蓋掉 Leaflet 預設 .leaflet-div-icon 的白底/灰框（leaflet.css 內建樣式），實際外觀全部改用
  // html 裡內嵌的 inline style（見 addKmMarkers）——這樣同一頁多次呼叫可以各自帶不同 color 而不必
  // 動態產生多個 class。
  style.textContent = '.dor-km-mark{background:transparent!important;border:none!important}'
  document.head.appendChild(style)
}

/**
 * 把 kmMarkerPositions() 算出的座標畫成地圖上的號碼標記（18×18 圓形色塊，白底數字＋白色描邊）。
 * L/map 由呼叫端傳入（各頁各自的 Leaflet 實例/地圖，見 track/page.tsx、track/history/page.tsx）。
 * 標記數超過 42（等於軌跡超過 42 公里的超馬等級）時只畫 5 的倍數，避免地圖被號碼塞滿看不清。
 */
export function addKmMarkers(L: any, map: any, points: KmMarkerPoint[], color: string): any[] { // eslint-disable-line @typescript-eslint/no-explicit-any -- Leaflet 無型別套件，比照全站慣例用 any
  if (!points || !points.length) return []
  ensureKmMarkerStyles()
  const toRender = points.length > 42 ? points.filter((p) => p.km % 5 === 0) : points
  return toRender.map((p) => {
    const icon = L.divIcon({
      className: 'dor-km-mark',
      html: `<div style="width:18px;height:18px;border-radius:50%;background:${color};color:#fff;font:800 10px/1 sans-serif;display:flex;align-items:center;justify-content:center;border:1.5px solid #fff;box-shadow:0 0 0 1px rgba(0,0,0,.25);box-sizing:border-box">${p.km}</div>`,
      iconSize: [18, 18],
      iconAnchor: [9, 9],
    })
    return L.marker([p.lat, p.lng], { icon }).addTo(map)
  })
}
