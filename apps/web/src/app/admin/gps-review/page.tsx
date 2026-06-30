'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminGpsApi, type GpsRunSummary } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

/* eslint-disable @typescript-eslint/no-explicit-any */

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
    s.onload = () => resolve((window as any).L); s.onerror = () => reject(new Error('地圖載入失敗'))
    document.head.appendChild(s)
  })
}
const fmtPace = (s: number) => (!s || s <= 0 ? '--:--' : `${Math.floor(s / 60)}:${String(Math.round(s % 60)).padStart(2, '0')}`)
const fmtTime = (s: number) => { const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), x = Math.floor(s % 60); const p = (n: number) => String(n).padStart(2, '0'); return h > 0 ? `${h}:${p(m)}:${p(x)}` : `${p(m)}:${p(x)}` }
const fmtDt = (iso: string) => { const d = new Date(iso); return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}` }

export default function AdminGpsReviewPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [runs, setRuns] = useState<GpsRunSummary[] | null>(null)
  const [sel, setSel] = useState<GpsRunSummary | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const mapRef = useRef<any>(null)

  const load = useCallback((t: string) => {
    adminGpsApi.list(t).then((r) => setRuns(r.runs)).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入失敗')
    })
  }, [router])

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t); load(t)
  }, [router, load])

  async function openDetail(id: string) {
    if (!token) return
    setErr('')
    try { const { run } = await adminGpsApi.get(token, id); setSel(run) } catch (e: any) { setErr(e?.message || '載入軌跡失敗') }
  }

  // 畫軌跡
  useEffect(() => {
    if (!sel) return
    const pts = (sel.points as any[]) || []
    let cancelled = false
    ;(async () => {
      const L = await loadLeaflet()
      if (cancelled) return
      if (mapRef.current) { mapRef.current.remove(); mapRef.current = null }
      const latlngs = pts.map((p) => [p.lat, p.lng] as [number, number]).filter((x) => x[0] && x[1])
      const center = latlngs[0] || [25.04, 121.56]
      const map = L.map('gps-review-map').setView(center, 15)
      L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', { maxZoom: 19, attribution: '© OpenStreetMap' }).addTo(map)
      if (latlngs.length > 1) {
        const line = L.polyline(latlngs, { color: '#ff5a5a', weight: 5 }).addTo(map)
        L.circleMarker(latlngs[0], { radius: 7, color: '#fff', fillColor: '#46E3A0', fillOpacity: 1 }).addTo(map).bindTooltip('起')
        L.circleMarker(latlngs[latlngs.length - 1], { radius: 7, color: '#fff', fillColor: '#ff5a5a', fillOpacity: 1 }).addTo(map).bindTooltip('終')
        map.fitBounds(line.getBounds(), { padding: [24, 24] })
      }
      mapRef.current = map
    })()
    return () => { cancelled = true }
  }, [sel])

  async function decide(id: string, action: 'approve' | 'reject') {
    if (!token) return
    setBusy(true); setErr('')
    try {
      await (action === 'approve' ? adminGpsApi.approve(token, id) : adminGpsApi.reject(token, id))
      setRuns((rs) => (rs ? rs.filter((r) => r.id !== id) : rs))
      setSel(null)
      if (mapRef.current) { mapRef.current.remove(); mapRef.current = null }
    } catch (e: any) { setErr(e?.message || '操作失敗') } finally { setBusy(false) }
  }

  return (
    <div style={{ maxWidth: 820, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 6px' }}>GPS 跑步審核</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, marginTop: 0 }}>系統偵測為「數據異常」而標記待審的網頁 GPS 跑步。核准＝計入活動並發里程 EXP；駁回＝不計。</p>
      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0' }}>{err}</div>}

      {/* 詳情（含軌跡圖） */}
      {sel && (
        <div style={{ background: 'var(--bg-1)', border: '1px solid rgba(255,90,90,.4)', borderRadius: 14, padding: 16, marginBottom: 18 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 10 }}>
            <strong style={{ fontSize: 16 }}>{sel.user_name} 的跑步軌跡</strong>
            <button onClick={() => { setSel(null); if (mapRef.current) { mapRef.current.remove(); mapRef.current = null } }} style={ghost}>關閉</button>
          </div>
          <div id="gps-review-map" style={{ width: '100%', height: 320, borderRadius: 10, overflow: 'hidden', background: 'var(--bg-2)' }} />
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(120px,1fr))', gap: 10, margin: '12px 0' }}>
            <Stat label="距離" v={`${sel.distance_km.toFixed(2)} km`} />
            <Stat label="時間" v={fmtTime(sel.duration_s)} />
            <Stat label="平均配速" v={`${fmtPace(sel.avg_pace_s)}/km`} />
            <Stat label="軌跡點" v={`${sel.point_count}`} />
          </div>
          <div style={{ background: 'rgba(255,90,90,.1)', border: '1px solid rgba(255,90,90,.3)', borderRadius: 8, padding: '8px 12px', fontSize: 13, color: '#ff8a8a' }}>⚠️ 異常原因：{sel.flag_reason}</div>
          <div style={{ display: 'flex', gap: 10, marginTop: 14 }}>
            <button onClick={() => decide(sel.id, 'approve')} disabled={busy} style={{ ...primary, flex: 1 }}>✓ 核准（計入＋發 EXP）</button>
            <button onClick={() => decide(sel.id, 'reject')} disabled={busy} style={{ ...danger, flex: 1 }}>✕ 駁回</button>
          </div>
        </div>
      )}

      {/* 待審清單 */}
      {!runs && <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
      {runs && runs.length === 0 && <div style={{ color: 'var(--tx-faint)', padding: '20px 0' }}>目前沒有待審的 GPS 跑步 🎉</div>}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        {runs?.map((r) => (
          <button key={r.id} onClick={() => openDetail(r.id)} style={{ ...card, textAlign: 'left', cursor: 'pointer' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', gap: 12 }}>
              <div style={{ minWidth: 0 }}>
                <div style={{ fontSize: 15, fontWeight: 700 }}>{r.user_name}</div>
                <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 2 }}>{r.distance_km.toFixed(2)} km · {fmtTime(r.duration_s)} · {fmtPace(r.avg_pace_s)}/km · {fmtDt(r.started_at)}</div>
                <div style={{ fontSize: 12, color: '#ff8a8a', marginTop: 4 }}>⚠️ {r.flag_reason}</div>
              </div>
              <span style={{ color: 'var(--fug)', fontSize: 13, flexShrink: 0, alignSelf: 'center' }}>查看軌跡 →</span>
            </div>
          </button>
        ))}
      </div>
    </div>
  )
}

function Stat({ label, v }: { label: string; v: string }) {
  return (
    <div style={{ background: 'var(--bg-2)', borderRadius: 10, padding: '8px 12px' }}>
      <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</div>
      <div style={{ fontSize: 16, fontWeight: 800 }}>{v}</div>
    </div>
  )
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: 14, width: '100%' }
const primary: React.CSSProperties = { background: 'var(--fug)', color: '#05140e', fontWeight: 800, border: 'none', borderRadius: 10, padding: '11px 16px', cursor: 'pointer', fontSize: 14 }
const danger: React.CSSProperties = { background: 'rgba(255,80,80,.1)', color: 'var(--hunt)', fontWeight: 800, border: '1px solid rgba(255,80,80,.3)', borderRadius: 10, padding: '11px 16px', cursor: 'pointer', fontSize: 14 }
const ghost: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 13 }
