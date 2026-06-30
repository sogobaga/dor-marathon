'use client'

import { useEffect, useRef, useState, useCallback } from 'react'
import { activitiesApi, type GpsPoint, type GpsRunResult } from '@/lib/api'
import { getUserToken, withUserAuth, useUser } from '@/lib/userAuth'

/* eslint-disable @typescript-eslint/no-explicit-any */

const LS_KEY = 'dor_gps_run'
const MAX_ACC = 40 // 精度差於此（公尺）的點不採計距離
const MAX_SPEED = 1000 / 120 // 8.33 m/s（2:00/km）人類極限上限

function haversineM(a: GpsPoint, b: GpsPoint) {
  const R = 6371000, rad = Math.PI / 180
  const dLat = (b.lat - a.lat) * rad, dLng = (b.lng - a.lng) * rad
  const x = Math.sin(dLat / 2) ** 2 + Math.cos(a.lat * rad) * Math.cos(b.lat * rad) * Math.sin(dLng / 2) ** 2
  return R * 2 * Math.atan2(Math.sqrt(x), Math.sqrt(1 - x))
}
function fmtTime(s: number) {
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = Math.floor(s % 60)
  const p = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${p(m)}:${p(sec)}` : `${p(m)}:${p(sec)}`
}
function fmtPace(s: number) {
  if (!s || !isFinite(s) || s <= 0) return '--:--'
  return `${Math.floor(s / 60)}:${String(Math.round(s % 60)).padStart(2, '0')}`
}

function loadLeaflet(): Promise<any> {
  return new Promise((resolve, reject) => {
    if ((window as any).L) return resolve((window as any).L)
    if (!document.getElementById('leaflet-css')) {
      const l = document.createElement('link')
      l.id = 'leaflet-css'; l.rel = 'stylesheet'; l.href = 'https://unpkg.com/leaflet@1.9.4/dist/leaflet.css'
      document.head.appendChild(l)
    }
    const s = document.createElement('script')
    s.src = 'https://unpkg.com/leaflet@1.9.4/dist/leaflet.js'
    s.onload = () => resolve((window as any).L)
    s.onerror = () => reject(new Error('地圖載入失敗'))
    document.head.appendChild(s)
  })
}

export default function TrackPage() {
  const user = useUser()
  const [status, setStatus] = useState<'idle' | 'tracking' | 'done'>('idle')
  const [distance, setDistance] = useState(0) // 公尺
  const [elapsed, setElapsed] = useState(0)
  const [splits, setSplits] = useState<number[]>([]) // 每公里配速（秒）
  const [anomalies, setAnomalies] = useState(0)
  const [warn, setWarn] = useState('')
  const [err, setErr] = useState('')
  const [result, setResult] = useState<GpsRunResult | null>(null)
  const [uploading, setUploading] = useState(false)

  const pointsRef = useRef<GpsPoint[]>([])
  const distRef = useRef(0)
  const splitMarkRef = useRef<number[]>([]) // 每跨整公里時的 elapsed 秒
  const startRef = useRef(0)
  const watchRef = useRef<number | null>(null)
  const wakeRef = useRef<any>(null)
  const timerRef = useRef<any>(null)
  const mapRef = useRef<any>(null)
  const lineRef = useRef<any>(null)
  const markRef = useRef<any>(null)
  const warnTimer = useRef<any>(null)

  const ensureMap = useCallback(async (lat: number, lng: number) => {
    const L = await loadLeaflet()
    if (mapRef.current) return
    const map = L.map('gps-map', { zoomControl: true }).setView([lat, lng], 16)
    L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', { maxZoom: 19, attribution: '© OpenStreetMap' }).addTo(map)
    lineRef.current = L.polyline([], { color: '#46E3A0', weight: 5 }).addTo(map)
    markRef.current = L.circleMarker([lat, lng], { radius: 7, color: '#fff', fillColor: '#46E3A0', fillOpacity: 1, weight: 2 }).addTo(map)
    mapRef.current = map
  }, [])

  const onPos = useCallback((pos: GeolocationPosition) => {
    const p: GpsPoint = { lat: pos.coords.latitude, lng: pos.coords.longitude, t: pos.timestamp, acc: pos.coords.accuracy ?? 0 }
    ensureMap(p.lat, p.lng)
    const pts = pointsRef.current
    const prev = pts.length ? pts[pts.length - 1] : null
    pts.push(p)

    if (prev && (p.acc === 0 || p.acc <= MAX_ACC)) {
      const d = haversineM(prev, p)
      const dt = (p.t - prev.t) / 1000
      if (dt > 0) {
        if (d > 5 && d / dt > MAX_SPEED) {
          setAnomalies((n) => n + 1)
          setWarn(`偵測到異常速度區段（${(d / dt).toFixed(1)} m/s），此筆將標記待審`)
          clearTimeout(warnTimer.current)
          warnTimer.current = setTimeout(() => setWarn(''), 4000)
        }
        distRef.current += d
        setDistance(distRef.current)
        // 每公里分段
        const km = Math.floor(distRef.current / 1000)
        const el = (p.t - startRef.current) / 1000
        while (splitMarkRef.current.length < km) {
          const prevEl = splitMarkRef.current.length ? splitMarkRef.current[splitMarkRef.current.length - 1] : 0
          splitMarkRef.current.push(el)
          setSplits((s) => [...s, el - prevEl])
        }
      }
    }
    // 地圖更新
    if (lineRef.current) lineRef.current.addLatLng([p.lat, p.lng])
    if (markRef.current) markRef.current.setLatLng([p.lat, p.lng])
    if (mapRef.current) mapRef.current.panTo([p.lat, p.lng])
    // 防當掉：暫存
    localStorage.setItem(LS_KEY, JSON.stringify({ start: startRef.current, points: pts.slice(-2000) }))
  }, [ensureMap])

  async function acquireWake() {
    try { wakeRef.current = await (navigator as any).wakeLock?.request('screen') } catch { /* ignore */ }
  }

  async function start() {
    setErr('')
    if (!navigator.geolocation) { setErr('此裝置/瀏覽器不支援定位'); return }
    pointsRef.current = []; distRef.current = 0; splitMarkRef.current = []
    setDistance(0); setSplits([]); setAnomalies(0); setResult(null)
    startRef.current = Date.now()
    setStatus('tracking')
    await acquireWake()
    timerRef.current = setInterval(() => setElapsed((Date.now() - startRef.current) / 1000), 250)
    watchRef.current = navigator.geolocation.watchPosition(onPos, (e) => {
      setErr(e.code === 1 ? '需要定位權限才能追蹤跑步' : '定位失敗：' + e.message)
    }, { enableHighAccuracy: true, maximumAge: 0, timeout: 15000 })
  }

  const cleanup = useCallback(() => {
    if (watchRef.current != null) navigator.geolocation.clearWatch(watchRef.current)
    watchRef.current = null
    clearInterval(timerRef.current)
    try { wakeRef.current?.release() } catch { /* ignore */ }
    wakeRef.current = null
  }, [])

  async function finish() {
    cleanup()
    setStatus('done')
    const pts = pointsRef.current
    if (pts.length < 2) { setErr('軌跡太短，未上傳'); localStorage.removeItem(LS_KEY); return }
    const token = getUserToken()
    if (!token) { setErr('未登入，無法上傳'); return }
    setUploading(true)
    try {
      const { result } = await withUserAuth((t) => activitiesApi.uploadGps(t, {
        started_at: new Date(startRef.current).toISOString(),
        ended_at: new Date(pts[pts.length - 1].t).toISOString(),
        points: pts,
      }))
      setResult(result)
      localStorage.removeItem(LS_KEY)
    } catch (e: any) {
      setErr(e?.message || '上傳失敗')
    } finally { setUploading(false) }
  }

  // 螢幕回到前景時重新取得 wake lock
  useEffect(() => {
    const onVis = () => { if (document.visibilityState === 'visible' && status === 'tracking') acquireWake() }
    document.addEventListener('visibilitychange', onVis)
    return () => { document.removeEventListener('visibilitychange', onVis); cleanup() }
  }, [status, cleanup])

  const distKm = distance / 1000
  const avgPace = distKm > 0 ? elapsed / distKm : 0

  return (
    <div style={{ minHeight: '100dvh', background: 'var(--bg)', color: 'var(--tx)', display: 'flex', flexDirection: 'column' }}>
      <header style={{ padding: '14px 18px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid var(--line)' }}>
        <a href="/" style={{ color: 'var(--tx-dim)', fontSize: 14, textDecoration: 'none' }}>← 返回</a>
        <strong style={{ fontSize: 16 }}>GPS 跑步追蹤</strong>
        <a href="/track/history" style={{ color: 'var(--fug)', fontSize: 13, textDecoration: 'none' }}>歷史</a>
      </header>

      {/* 地圖 */}
      <div id="gps-map" style={{ width: '100%', height: 280, background: 'var(--bg-2)' }} />

      <div style={{ padding: 16, flex: 1 }}>
        {warn && <div style={{ background: 'rgba(255,90,90,.12)', border: '1px solid rgba(255,90,90,.4)', color: '#ff8a8a', borderRadius: 10, padding: '10px 12px', fontSize: 13, marginBottom: 12 }}>⚠️ {warn}</div>}
        {err && <div style={{ color: 'var(--hunt)', fontSize: 13, marginBottom: 12 }}>{err}</div>}

        {/* 即時數據 */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
          <Big label="距離" value={distKm.toFixed(2)} unit="km" />
          <Big label="時間" value={fmtTime(elapsed)} unit="" />
          <Big label="平均配速" value={fmtPace(avgPace)} unit="/km" />
          <Big label="異常區段" value={String(anomalies)} unit="段" warn={anomalies > 0} />
        </div>

        {/* 分段 */}
        {splits.length > 0 && (
          <div style={{ marginTop: 16 }}>
            <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 6 }}>每公里分段</div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              {splits.map((s, i) => (
                <div key={i} style={{ display: 'flex', justifyContent: 'space-between', background: 'var(--bg-2)', borderRadius: 8, padding: '7px 12px', fontSize: 13 }}>
                  <span style={{ color: 'var(--tx-dim)' }}>第 {i + 1} km</span>
                  <span style={{ fontWeight: 700 }}>{fmtPace(s)} /km</span>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* 結果 */}
        {status === 'done' && result && (
          <div style={{ marginTop: 16, background: 'var(--bg-1)', border: `1px solid ${result.flagged ? 'rgba(255,90,90,.4)' : 'var(--line-2)'}`, borderRadius: 12, padding: 14 }}>
            <div style={{ fontWeight: 800, marginBottom: 8 }}>{result.flagged ? '⚠️ 數據異常，已標記待審' : '✓ 已記錄'}</div>
            <div style={{ fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.8 }}>
              距離 {result.distance_km.toFixed(2)} km · 時間 {fmtTime(result.duration_s)} · 平均配速 {fmtPace(result.avg_pace_s)}/km<br />
              {result.flagged
                ? <span style={{ color: '#ff8a8a' }}>原因：{result.flag_reason}（不發 EXP，待後台審核）</span>
                : <span style={{ color: 'var(--fug)' }}>已進活動記錄{result.exp_awarded ? '，里程 EXP 將於數秒後發放' : ''}</span>}
            </div>
          </div>
        )}
      </div>

      {/* 操作 */}
      <div style={{ padding: 16, borderTop: '1px solid var(--line)', position: 'sticky', bottom: 0, background: 'var(--bg)' }}>
        {status === 'idle' && <button onClick={start} disabled={!user} style={btn}>{user ? '▶ 開始跑步' : '請先登入'}</button>}
        {status === 'tracking' && <button onClick={finish} style={{ ...btn, background: 'var(--hunt)', color: '#fff' }}>■ 結束並上傳</button>}
        {status === 'done' && <button onClick={() => { setStatus('idle'); setElapsed(0); setDistance(0); setSplits([]); setAnomalies(0) }} style={{ ...btn, background: 'var(--bg-2)', color: 'var(--tx)' }}>再跑一次</button>}
        {status === 'tracking' && <div style={{ textAlign: 'center', fontSize: 11, color: 'var(--tx-faint)', marginTop: 8 }}>追蹤中請保持本頁在前景、螢幕勿關（背景追蹤瀏覽器不支援）{uploading ? ' · 上傳中…' : ''}</div>}
      </div>
    </div>
  )
}

function Big({ label, value, unit, warn }: { label: string; value: string; unit: string; warn?: boolean }) {
  return (
    <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '12px 14px' }}>
      <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</div>
      <div style={{ fontSize: 26, fontWeight: 900, color: warn ? 'var(--hunt)' : 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>
        {value}<span style={{ fontSize: 13, marginLeft: 3, color: 'var(--tx-dim)' }}>{unit}</span>
      </div>
    </div>
  )
}

const btn: React.CSSProperties = { width: '100%', background: 'var(--fug)', color: '#05140e', fontWeight: 800, border: 'none', borderRadius: 12, padding: '15px 20px', fontSize: 16, cursor: 'pointer' }
