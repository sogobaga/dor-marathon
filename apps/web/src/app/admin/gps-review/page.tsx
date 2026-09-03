'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminGpsApi, type GpsRunSummary, type AdminRecentGpsRun, type GpsRecallResult } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import { decodePolyline } from '@/lib/polyline'

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
// 回收異常數據清單用：明講「台北時間」，不依裝置時區（管理員可能不在台灣時區操作）
const fmtDtTaipei = (iso: string) => new Date(iso).toLocaleString('zh-TW', { timeZone: 'Asia/Taipei', year: 'numeric', month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })

export default function AdminGpsReviewPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [runs, setRuns] = useState<GpsRunSummary[] | null>(null)
  const [sel, setSel] = useState<GpsRunSummary | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const mapRef = useRef<any>(null)

  // 回收異常數據（2026-09-03）：已入帳（可能早於偵測規則或漏網）的跑步，事後標異常＋反沖 EXP/DP/里程
  const [recentRuns, setRecentRuns] = useState<AdminRecentGpsRun[] | null>(null)
  const [recentErr, setRecentErr] = useState('')
  const [recentLoading, setRecentLoading] = useState(false)
  const [recentDays, setRecentDays] = useState(7)
  const [recentQInput, setRecentQInput] = useState('') // 輸入框即時值
  const [recentQ, setRecentQ] = useState('') // 按「查詢」才套用的值
  const [recallOpenId, setRecallOpenId] = useState<string | null>(null) // 目前展開確認面板的 run id
  const [recallReason, setRecallReason] = useState('admin_anomaly')
  const [recallValidKm, setRecallValidKm] = useState('') // 選填「合法距離」文字輸入
  const [recallBusy, setRecallBusy] = useState(false)
  const [recallErr, setRecallErr] = useState('')
  const [recallResults, setRecallResults] = useState<Record<string, GpsRecallResult>>({}) // 各筆最近一次回收結果

  const load = useCallback((t: string) => {
    adminGpsApi.list(t).then((r) => setRuns(r.runs)).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入失敗')
    })
  }, [router])

  const loadRecent = useCallback((t: string, days: number, q: string) => {
    setRecentLoading(true); setRecentErr('')
    adminGpsApi.recent(t, { days, limit: 200, q: q || undefined }).then((r) => setRecentRuns(r.runs)).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setRecentErr(e?.message || '載入失敗')
    }).finally(() => setRecentLoading(false))
  }, [router])

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t); load(t); loadRecent(t, 7, '')
  }, [router, load, loadRecent])

  function runRecentQuery() {
    if (!token) return
    setRecentQ(recentQInput)
    loadRecent(token, recentDays, recentQInput)
  }

  function changeRecentDays(days: number) {
    setRecentDays(days)
    if (token) loadRecent(token, days, recentQ)
  }

  function openRecall(id: string) {
    setRecallOpenId(id); setRecallReason('admin_anomaly'); setRecallValidKm(''); setRecallErr('')
  }

  async function confirmRecall(r: AdminRecentGpsRun) {
    if (!token) return
    setRecallBusy(true); setRecallErr('')
    try {
      const trimmedKm = recallValidKm.trim()
      const parsedKm = trimmedKm === '' ? NaN : Number(trimmedKm)
      const res = await adminGpsApi.recall(token, r.id, {
        reason: recallReason.trim() || undefined,
        valid_distance_km: Number.isNaN(parsedKm) ? undefined : parsedKm,
        activity_id: r.activity_id,
      })
      setRecallResults((m) => ({ ...m, [r.id]: res }))
      setRecallOpenId(null)
      loadRecent(token, recentDays, recentQ)
    } catch (e: any) { setRecallErr(e?.message || '回收失敗') } finally { setRecallBusy(false) }
  }

  async function openDetail(id: string) {
    if (!token) return
    setErr('')
    try { const { run } = await adminGpsApi.get(token, id); setSel(run) } catch (e: any) { setErr(e?.message || '載入軌跡失敗') }
  }

  // 畫軌跡
  useEffect(() => {
    if (!sel) return
    let cancelled = false
    ;(async () => {
      const L = await loadLeaflet()
      if (cancelled) return
      if (mapRef.current) { mapRef.current.remove(); mapRef.current = null }
      const latlngs = decodePolyline(sel.polyline || '')
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

      {/* 回收異常數據：已入帳但事後才發現異常的跑步（早於偵測規則或漏網），人工標異常＋反沖已發出的
          EXP／DP／里程；紀錄保留不刪，僅排除計算。與上面「待審清單」不同——這裡列的是已經算過帳的資料。 */}
      <h2 style={{ fontSize: 20, fontWeight: 800, margin: '32px 0 6px' }}>回收異常數據</h2>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, marginTop: 0 }}>列出近期已入帳的 GPS 跑步（含未被系統旗標者）。發現異常（如訊號跳點、載具代跑）可事後標記並反沖已發出的 EXP／DP／里程；紀錄保留不刪，僅排除計算。</p>

      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center', margin: '10px 0 14px' }}>
        <select value={recentDays} onChange={(e) => changeRecentDays(Number(e.target.value))} style={select}>
          <option value={7}>近 7 天</option>
          <option value={30}>近 30 天</option>
          <option value={90}>近 90 天</option>
        </select>
        <input
          value={recentQInput}
          onChange={(e) => setRecentQInput(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') runRecentQuery() }}
          placeholder="使用者 ID／Email／名稱"
          style={{ ...input, flex: 1, minWidth: 180 }}
        />
        <button onClick={runRecentQuery} style={ghost}>查詢</button>
      </div>

      {recentErr && <div style={{ color: 'var(--hunt)', padding: '8px 0' }}>{recentErr}</div>}
      {recentLoading && <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
      {!recentLoading && recentRuns && recentRuns.length === 0 && <div style={{ color: 'var(--tx-faint)', padding: '20px 0' }}>查無符合條件的跑步紀錄</div>}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        {recentRuns?.map((r) => {
          // 已回收：後端把回收後的 flag_reason 存成 'admin_anomaly'；review_action==='rejected' 是舊管道
          // （例如原本就在待審清單被駁回）也算已排除，一併視為已回收避免重複操作。
          const recalled = r.flag_reason === 'admin_anomaly' || (r.review_action === 'rejected' && r.flagged)
          const badge = recalled
            ? { text: '已回收', color: '#ff5a5a' }
            : r.flagged
              ? { text: `已旗標：${r.flag_reason}`, color: '#ff8a8a' }
              : { text: '未旗標', color: 'var(--tx-faint)' }
          const dist = r.calib_distance_km ?? r.distance_km
          const showOrig = r.calib_distance_km != null && Math.abs(r.calib_distance_km - r.distance_km) > 0.005
          const expText = r.exp_awarded === true ? 'EXP 已發' : r.exp_awarded === false ? 'EXP 未發' : 'EXP 狀態未知'
          const result = recallResults[r.id]
          return (
            <div key={r.id} style={card}>
              <div style={{ display: 'flex', justifyContent: 'space-between', gap: 12, flexWrap: 'wrap' }}>
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>{fmtDtTaipei(r.started_at)}</div>
                  <div style={{ fontSize: 15, fontWeight: 700, marginTop: 2 }}>{r.user_name} <span style={{ fontWeight: 400, color: 'var(--tx-dim)', fontSize: 12 }}>{r.user_email}</span></div>
                  <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4 }}>
                    {dist.toFixed(2)} km{showOrig && <span style={{ color: 'var(--tx-faint)' }}>（原始 {r.distance_km.toFixed(2)} km）</span>} · {fmtTime(r.duration_s)} · {fmtPace(r.avg_pace_s)}/km
                  </div>
                  {r.excluded_segments > 0 && <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginTop: 2 }}>⚠️ 已排除 {r.excluded_km.toFixed(1)} km</div>}
                  <div style={{ display: 'flex', gap: 8, marginTop: 6, flexWrap: 'wrap' }}>
                    <span style={{ fontSize: 11, fontWeight: 700, color: badge.color }}>{badge.text}</span>
                    <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{expText}</span>
                  </div>
                </div>
                <button onClick={() => openRecall(r.id)} disabled={recalled} style={{ ...danger, flexShrink: 0, alignSelf: 'center', opacity: recalled ? 0.4 : 1, cursor: recalled ? 'default' : 'pointer' }}>標為異常並回收</button>
              </div>

              {recallOpenId === r.id && (
                <div style={{ marginTop: 12, background: 'var(--bg-2)', borderRadius: 10, padding: 12 }}>
                  <label style={{ display: 'block', fontSize: 12, color: 'var(--tx-dim)', marginBottom: 4 }}>回收原因</label>
                  <input value={recallReason} onChange={(e) => setRecallReason(e.target.value)} style={{ ...input, width: '100%' }} />
                  <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4 }}>不可用 multi_device_duplicate／cross_source_duplicate／duplicate</div>
                  <label style={{ display: 'block', fontSize: 12, color: 'var(--tx-dim)', margin: '10px 0 4px' }}>合法距離 (km)（選填）</label>
                  <input value={recallValidKm} onChange={(e) => setRecallValidKm(e.target.value)} placeholder="留空＝全數不計" inputMode="decimal" style={{ ...input, width: '100%' }} />
                  {recallErr && <div style={{ color: 'var(--hunt)', fontSize: 12, marginTop: 8 }}>{recallErr}</div>}
                  <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
                    <button onClick={() => confirmRecall(r)} disabled={recallBusy} style={{ ...danger, flex: 1 }}>確認回收</button>
                    <button onClick={() => setRecallOpenId(null)} style={ghost}>取消</button>
                  </div>
                </div>
              )}

              {result && (
                <div style={{ marginTop: 10, background: 'rgba(70,227,160,.08)', border: '1px solid rgba(70,227,160,.3)', borderRadius: 8, padding: '10px 12px', fontSize: 12.5 }}>
                  {result.already_recalled
                    ? <div>此筆先前已回收，未重複扣除</div>
                    : <div>已回收：里程 −{result.reversed.total_km.toFixed(2)} km、EXP −{result.reversed.exp}、DP −{result.reversed.dp}</div>}
                  {result.followups && result.followups.length > 0 && (
                    <div style={{ marginTop: 6 }}>
                      <div style={{ color: 'var(--tx-faint)' }}>需人工追查：</div>
                      <ul style={{ margin: '4px 0 0', paddingLeft: 18 }}>
                        {result.followups.map((f, i) => <li key={i}>{f}</li>)}
                      </ul>
                    </div>
                  )}
                </div>
              )}
            </div>
          )
        })}
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
const primary: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 10, padding: '11px 16px', cursor: 'pointer', fontSize: 14 }
const danger: React.CSSProperties = { background: 'rgba(255,80,80,.1)', color: 'var(--hunt)', fontWeight: 800, border: '1px solid rgba(255,80,80,.3)', borderRadius: 10, padding: '11px 16px', cursor: 'pointer', fontSize: 14 }
const ghost: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 13 }
const select: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', fontSize: 13 }
const input: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', fontSize: 13 }
