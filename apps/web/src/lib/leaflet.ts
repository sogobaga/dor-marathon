// 共用 Leaflet（CDN）載入器：/track、後台打卡點地圖選點共用
export function loadLeaflet(): Promise<any> {
  return new Promise((resolve, reject) => {
    if ((window as any).L) return resolve((window as any).L)
    if (!document.getElementById('leaflet-css')) {
      const l = document.createElement('link')
      l.id = 'leaflet-css'
      l.rel = 'stylesheet'
      l.href = 'https://unpkg.com/leaflet@1.9.4/dist/leaflet.css'
      document.head.appendChild(l)
    }
    const existing = document.getElementById('leaflet-js') as HTMLScriptElement | null
    if (existing) {
      if ((window as any).L) return resolve((window as any).L)
      existing.addEventListener('load', () => resolve((window as any).L))
      existing.addEventListener('error', () => reject(new Error('地圖載入失敗')))
      return
    }
    const s = document.createElement('script')
    s.id = 'leaflet-js'
    s.src = 'https://unpkg.com/leaflet@1.9.4/dist/leaflet.js'
    s.onload = () => resolve((window as any).L)
    s.onerror = () => reject(new Error('地圖載入失敗'))
    document.head.appendChild(s)
  })
}
