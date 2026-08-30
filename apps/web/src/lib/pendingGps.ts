import type { GpsPoint } from './api'

// 與 track 頁 LS_KEY 同一把('dor_gps_run')，存 { start:number(ms), points:GpsPoint[] }
export const PENDING_GPS_KEY = 'dor_gps_run'

function haversineM(a: GpsPoint, b: GpsPoint): number {
  const R = 6371000, rad = Math.PI / 180
  const dLat = (b.lat - a.lat) * rad, dLon = (b.lng - a.lng) * rad
  const s = Math.sin(dLat / 2) ** 2 + Math.cos(a.lat * rad) * Math.cos(b.lat * rad) * Math.sin(dLon / 2) ** 2
  return R * 2 * Math.atan2(Math.sqrt(s), Math.sqrt(1 - s))
}

export interface PendingGpsRun { start: number; endedAt: number; points: GpsPoint[]; km: number; mins: number }

// 讀本機未上傳的 GPS 跑步(≥2 點、未逾 24h);無效/過期回 null(不主動清除,交由呼叫端決定)
export function readPendingGps(): PendingGpsRun | null {
  try {
    const raw = localStorage.getItem(PENDING_GPS_KEY)
    if (!raw) return null
    const data = JSON.parse(raw)
    const points: GpsPoint[] = data?.points
    if (!Array.isArray(points) || points.length < 2 || !data.start) return null
    const endedAt = points[points.length - 1].t
    if (Date.now() - endedAt > 24 * 3600 * 1000) return null
    let m = 0
    for (let i = 1; i < points.length; i++) m += haversineM(points[i - 1], points[i])
    const durS = Math.max(1, Math.round((endedAt - data.start) / 1000))
    // GPS 距離校正（見 internal/gpscalib，2026-08-30）：該趟記錄當下固定的係數快照（track/page.tsx
    // 開跑時存進同一把 LS_KEY，見 calibKRef 宣告處）；舊資料無此欄位 → 1（未套校正）。對抗式審查
    // 修正：track 頁自己的 recover 彈窗已經套這個係數，這裡（ProfileScreen「本機尚未上傳」卡片
    // 讀的同一份資料）先前完全沒讀 data.k，顯示值比實際上傳入帳的距離多約 1/k−1（例如係數 0.9781
    // 時多約 2.2%），同一筆資料在兩處顯示不同公里數。
    const k = typeof data?.k === 'number' && data.k > 0 ? data.k : 1
    return { start: data.start, endedAt, points, km: Math.round(m * k / 10) / 100, mins: Math.round(durS / 60) }
  } catch { return null }
}

export function clearPendingGps() { try { localStorage.removeItem(PENDING_GPS_KEY) } catch { /* ignore */ } }
