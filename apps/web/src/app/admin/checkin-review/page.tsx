'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminRacesApi, adminCheckinReviewApi, type Race, type PendingCheckin } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import { loadLeaflet } from '@/lib/leaflet'

/* eslint-disable @typescript-eslint/no-explicit-any */

const fmtDt = (iso: string) => { const d = new Date(iso); return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}` }
const fmtM = (m: number) => (m < 1000 ? `${Math.round(m)}m` : `${(m / 1000).toFixed(2)}km`)

export default function AdminCheckinReviewPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [races, setRaces] = useState<Race[]>([])
  const [raceID, setRaceID] = useState('')
  const [rows, setRows] = useState<PendingCheckin[] | null>(null)
  const [sel, setSel] = useState<PendingCheckin | null>(null)
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')
  const mapRef = useRef<any>(null)
  const layerRef = useRef<any>(null)

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminRacesApi.list(t).then((r) => {
      setRaces(r.races)
      if (r.races.length) setRaceID(r.races[0].id)
    }).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入失敗')
    })
  }, [router])

  const load = useCallback((rid: string) => {
    const t = getToken()
    if (!t || !rid) return
    setRows(null); setSel(null); setErr('')
    adminCheckinReviewApi.list(t, rid)
      .then((r) => { setRows(r.checkins); if (r.checkins.length) setSel(r.checkins[0]) })
      .catch((e) => setErr(e?.message || '載入失敗'))
  }, [])

  useEffect(() => { if (raceID) load(raceID) }, [raceID, load])

  // 地圖：顯示選取的待審打卡（打卡點 + 允許半徑 + 會員打卡位置）
  useEffect(() => {
    if (!sel) return
    let cancelled = false
    loadLeaflet().then((L) => {
      if (cancelled) return
      // 容器可能因清單清空/切換賽事而被卸載重掛，偵測到就重建地圖
      if (mapRef.current && !document.body.contains(mapRef.current.getContainer())) {
        mapRef.current.remove(); mapRef.current = null; layerRef.current = null
      }
      if (!document.getElementById('checkin-review-map')) return
      if (!mapRef.current) {
        const map = L.map('checkin-review-map').setView([sel.cp_lat, sel.cp_lng], 16)
        L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', { maxZoom: 19, attribution: '© OpenStreetMap' }).addTo(map)
        layerRef.current = L.layerGroup().addTo(map)
        mapRef.current = map
      }
      const layer = layerRef.current
      layer.clearLayers()
      // 打卡點 + 允許半徑
      L.circle([sel.cp_lat, sel.cp_lng], { radius: sel.radius_m || 20, color: '#FFC24B', weight: 1.5, fillOpacity: 0.1 }).addTo(layer)
      L.circleMarker([sel.cp_lat, sel.cp_lng], { radius: 7, color: '#fff', weight: 2, fillColor: '#FFC24B', fillOpacity: 1 }).addTo(layer).bindTooltip('打卡點')
      // 會員打卡位置
      L.circleMarker([sel.lat, sel.lng], { radius: 7, color: '#fff', weight: 2, fillColor: '#3b82f6', fillOpacity: 1 }).addTo(layer).bindTooltip(sel.user_name)
      L.polyline([[sel.cp_lat, sel.cp_lng], [sel.lat, sel.lng]], { color: '#8892a0', weight: 1.5, dashArray: '4 4' }).addTo(layer)
      mapRef.current.fitBounds([[sel.cp_lat, sel.cp_lng], [sel.lat, sel.lng]], { padding: [40, 40], maxZoom: 17 })
    })
    return () => { cancelled = true }
  }, [sel])

  async function review(c: PendingCheckin, approve: boolean) {
    if (!token) return
    setBusy(c.id); setErr(''); setMsg('')
    try {
      if (approve) await adminCheckinReviewApi.approve(token, c.id)
      else await adminCheckinReviewApi.reject(token, c.id)
      setRows((rs) => {
        const next = (rs ?? []).filter((x) => x.id !== c.id)
        setSel((cur) => (cur?.id === c.id ? (next[0] ?? null) : cur))
        return next
      })
      setMsg(approve ? `已核准「${c.user_name}」在「${c.checkpoint_name}」的打卡` : `已退回「${c.user_name}」的打卡`)
    } catch (e: any) { setErr(e?.message || '操作失敗') } finally { setBusy('') }
  }

  return (
    <div>
      <h1 style={{ margin: '0 0 6px', fontSize: 24, fontWeight: 800 }}>打卡審核</h1>
      <p style={{ margin: '0 0 18px', color: 'var(--tx-dim)', fontSize: 13.5 }}>
        缺 GPS 軌跡佐證的打卡會列在這裡等待人工審核。核准後計入該任務集章；退回後該筆刪除，會員可重新打卡。
      </p>

      <div style={{ marginBottom: 16 }}>
        <select value={raceID} onChange={(e) => setRaceID(e.target.value)} style={{ ...inp, maxWidth: 320 }}>
          {races.map((r) => <option key={r.id} value={r.id}>{r.title}</option>)}
        </select>
      </div>

      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0' }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '10px 0', fontSize: 13.5 }}>{msg}</div>}
      {!rows && !err && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>載入中…</div>}
      {rows && rows.length === 0 && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>此賽事目前沒有待審核的打卡 ✅</div>}

      {rows && rows.length > 0 && (
        <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,1.1fr)', gap: 16, alignItems: 'start' }}>
          <div style={{ border: '1px solid var(--line)', borderRadius: 14, overflow: 'hidden' }}>
            {rows.map((c) => {
              const active = sel?.id === c.id
              return (
                <div key={c.id} onClick={() => setSel(c)} style={{
                  padding: '12px 14px', borderBottom: '1px solid var(--line)', cursor: 'pointer',
                  background: active ? 'rgba(70,227,160,.06)' : 'transparent',
                  borderLeft: active ? '3px solid var(--fug)' : '3px solid transparent',
                }}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', gap: 10 }}>
                    <div style={{ minWidth: 0 }}>
                      <div style={{ fontWeight: 700 }}>{c.user_name}</div>
                      <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{c.user_email}</div>
                    </div>
                    <div style={{ fontSize: 11, color: 'var(--tx-faint)', whiteSpace: 'nowrap' }}>{fmtDt(c.checked_at)}</div>
                  </div>
                  <div style={{ fontSize: 13, marginTop: 6 }}>📍 {c.checkpoint_name || '打卡點'} <span style={{ color: 'var(--tx-faint)' }}>· {c.task_title}</span></div>
                  <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 3 }}>
                    距打卡點 {fmtM(c.distance_m)}（允許 {c.radius_m}m）· 定位精度 ±{Math.round(c.accuracy)}m
                  </div>
                  <div style={{ fontSize: 12, color: 'var(--gold)', marginTop: 3 }}>⚠ {c.flag_reason || '待審核'}</div>
                  <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
                    <button onClick={(e) => { e.stopPropagation(); review(c, true) }} disabled={busy === c.id} style={approveBtn}>✓ 核准</button>
                    <button onClick={(e) => { e.stopPropagation(); review(c, false) }} disabled={busy === c.id} style={rejectBtn}>✕ 退回</button>
                  </div>
                </div>
              )
            })}
          </div>

          <div style={{ position: 'sticky', top: 16 }}>
            <div id="checkin-review-map" style={{ width: '100%', height: 380, borderRadius: 12, overflow: 'hidden', background: 'var(--bg-2)', border: '1px solid var(--line)' }} />
            <div style={{ display: 'flex', gap: 16, marginTop: 8, fontSize: 12, color: 'var(--tx-dim)' }}>
              <span><span style={{ color: '#FFC24B' }}>●</span> 打卡點／允許半徑</span>
              <span><span style={{ color: '#3b82f6' }}>●</span> 會員打卡位置</span>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

const inp: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10,
  padding: '10px 12px', color: 'var(--tx)', fontSize: 14, width: '100%', fontFamily: 'inherit',
}
const approveBtn: React.CSSProperties = {
  background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none',
  borderRadius: 9, padding: '7px 16px', cursor: 'pointer', fontSize: 13,
}
const rejectBtn: React.CSSProperties = {
  background: 'transparent', color: 'var(--hunt)', fontWeight: 700, border: '1px solid rgba(255,75,92,.5)',
  borderRadius: 9, padding: '7px 16px', cursor: 'pointer', fontSize: 13,
}
