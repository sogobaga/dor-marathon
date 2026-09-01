'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { activitiesApi, type GpsRunHistory } from '@/lib/api'
import { getUserToken, withUserAuth, useUser, getUser } from '@/lib/userAuth'
import { decodePolyline } from '@/lib/polyline'
import { useDashboard } from '@/lib/useDashboard'
import { captureRunProofFromDom, generateRunProofImage, shareRunProof } from '@/lib/runProof'
import PhoneFrame from '@/components/PhoneFrame'
import ScrollArea from '@/components/ScrollArea'

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
const fmtDt = (iso: string) => { const d = new Date(iso); return `${d.getFullYear()}/${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}` }

export default function TrackHistoryPage() {
  const user = useUser()
  const [runs, setRuns] = useState<GpsRunHistory[] | null>(null)
  const [sel, setSel] = useState<GpsRunHistory | null>(null)
  const [err, setErr] = useState('')
  const mapRef = useRef<any>(null)
  const { dash } = useDashboard() // 共用會員儀表板快取（見 lib/useDashboard.ts）；這裡只用來讀 gov500_entry
  const [gov500Busy, setGov500Busy] = useState(false)
  const proofAreaRef = useRef<HTMLDivElement | null>(null) // 證明圖擷取範圍（詳情容器；關閉鈕/活動卡以 data-proof-ignore 排除） // 政府「揮汗有禮」證明圖產生中（gov500_entry，見 lib/runProof.ts）

  // 政府「揮汗有禮」活動證明圖：優先「畫面實況擷取」（含 GPS 軌跡圖/統計/分段計量表，與畫面
  // 一致才不會有作假疑慮——使用者明確要求）；擷取失敗（地圖磚 CORS/記憶體不足等）才退回合成卡片
  //（至少含官方要求的日期/時間/距離欄位）。兩者數據同源（校正後優先）。
  async function handleGov500Proof() {
    if (!sel || gov500Busy) return
    setGov500Busy(true)
    try {
      const name = getUser()?.name || ''
      let blob: Blob
      try {
        if (!proofAreaRef.current) throw new Error('no capture area')
        blob = await captureRunProofFromDom(proofAreaRef.current, name)
      } catch {
        const avgPaceS = sel.calib_avg_pace_s ?? sel.avg_pace_s
        blob = await generateRunProofImage({
          startedAt: new Date(sel.started_at),
          durationS: sel.duration_s,
          distanceKm: sel.calib_distance_km ?? sel.distance_km,
          avgPaceS: avgPaceS > 0 ? avgPaceS : null,
          displayName: name,
        })
      }
      await shareRunProof(blob)
    } catch (e: any) {
      setErr(e?.message || '證明圖產生失敗，請再試一次')
    } finally {
      setGov500Busy(false)
    }
  }

  const load = useCallback(() => {
    const t = getUserToken(); if (!t) return
    withUserAuth((tk) => activitiesApi.gpsHistory(tk)).then((r) => setRuns(r.runs)).catch((e) => setErr(e?.message || '載入失敗'))
  }, [])
  useEffect(() => { if (user) load() }, [user, load])

  async function openRun(id: string) {
    setErr('')
    try {
      const { run } = await withUserAuth((t) => activitiesApi.gpsDetail(t, id))
      setSel(run)
    } catch (e: any) { setErr(e?.message || '載入軌跡失敗') }
  }

  useEffect(() => {
    if (!sel) return
    const coords = decodePolyline(sel.polyline || '')
    let cancelled = false
    ;(async () => {
      const L = await loadLeaflet()
      if (cancelled) return
      if (mapRef.current) { mapRef.current.remove(); mapRef.current = null }
      const center = coords[0] || [25.04, 121.56]
      const map = L.map('hist-map').setView(center, 15)
      L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', { maxZoom: 19, attribution: '© OpenStreetMap' }).addTo(map)
      if (coords.length > 1) {
        const line = L.polyline(coords, { color: sel.flagged ? '#ff5a5a' : '#46E3A0', weight: 5 }).addTo(map)
        L.circleMarker(coords[0], { radius: 7, color: '#fff', fillColor: '#46E3A0', fillOpacity: 1 }).addTo(map).bindTooltip('起')
        L.circleMarker(coords[coords.length - 1], { radius: 7, color: '#fff', fillColor: '#ff5a5a', fillOpacity: 1 }).addTo(map).bindTooltip('終')
        map.fitBounds(line.getBounds(), { padding: [22, 22] })
      }
      mapRef.current = map
    })()
    return () => { cancelled = true }
  }, [sel])

  return (
    <PhoneFrame>
      <header style={{ padding: '14px 18px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid var(--line)' }}>
        <a href="/track" style={{ color: 'var(--tx-dim)', fontSize: 14, textDecoration: 'none' }}>← 追蹤</a>
        <strong style={{ fontSize: 16 }}>跑步軌跡歷史</strong>
        <a href="/" style={{ color: 'var(--tx-faint)', fontSize: 13, textDecoration: 'none' }}>首頁</a>
      </header>

      <ScrollArea>
      {sel && (
        <div ref={proofAreaRef} style={{ padding: 16 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
            <strong>{fmtDt(sel.started_at)}</strong>
            <button data-proof-ignore="1" onClick={() => { setSel(null); if (mapRef.current) { mapRef.current.remove(); mapRef.current = null } }} style={ghost}>關閉</button>
          </div>
          <div id="hist-map" style={{ width: '100%', height: 300, borderRadius: 10, overflow: 'hidden', background: 'var(--bg-2)' }} />
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 10, marginTop: 12 }}>
            <Stat label="距離" v={`${(sel.calib_distance_km ?? sel.distance_km).toFixed(2)} km`} />
            <Stat label="時間" v={fmtTime(sel.duration_s)} />
            <Stat label="平均配速" v={`${fmtPace(sel.calib_avg_pace_s ?? sel.avg_pace_s)}/km`} />
          </div>
          {/* GPS 距離校正（見 internal/gpscalib，2026-08-30）：這裡與「已同步活動」列表/總里程用同一套
              校正後數字；calib_factor<1 才代表真的套用過，額外標示原始值供對照（medium-3 finding 修正
              前，這裡顯示的是未校正原始距離，跟其他頁面對不上）。 */}
          {sel.calib_factor != null && sel.calib_factor < 1 && (
            <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4, textAlign: 'center' }}>
              已依手錶紀錄校正 · 原始 {sel.distance_km.toFixed(2)} km ×{sel.calib_factor.toFixed(4)}
            </div>
          )}
          {sel.km_paces && sel.km_paces.length > 0 ? (
            <div style={{ marginTop: 14 }}>
              <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 6 }}>每公里分段配速</div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
                {(() => {
                  const paces = sel.km_paces!
                  const mx = Math.max(...paces), mn = Math.min(...paces)
                  return paces.map((p, i) => {
                    const pct = mx > mn ? 100 - ((p - mn) / (mx - mn)) * 62 : 100 // 越快(秒少)條越長
                    return (
                      <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13 }}>
                        {/* 不換行＋自然寬度（minWidth 對齊常見 1-2 位數）：第1km～第100km 皆單行，
                            量條 flex:1 自動讓位——原本硬限 46px 使「第 2 km」就折行、一列變兩列高 */}
                        <span style={{ minWidth: 46, whiteSpace: 'nowrap', color: 'var(--tx-dim)', flexShrink: 0 }}>第{i + 1}km</span>
                        <div style={{ flex: 1, height: 8, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden' }}>
                          <div style={{ height: '100%', width: `${pct}%`, background: 'linear-gradient(90deg,#FFD24D,#46E3A0)', borderRadius: 999 }} />
                        </div>
                        <span style={{ minWidth: 58, whiteSpace: 'nowrap', textAlign: 'right', fontWeight: 700, fontVariantNumeric: 'tabular-nums', flexShrink: 0 }}>{fmtPace(p)}/km</span>
                      </div>
                    )
                  })
                })()}
              </div>
            </div>
          ) : (
            <div style={{ marginTop: 12, fontSize: 11.5, color: 'var(--tx-faint)' }}>（此筆沒有每公里分段資料；v0.1.205 之後的新 GPS 跑步才會記錄）</div>
          )}
          {/* 政府「揮汗有禮」活動證明圖：先只給超管看（gov500_entry，見系統設定 gov500_entry_state），
              之後備妥後由系統設定開放給一般玩家。 */}
          {dash?.gov500_entry === 'shown' && (
            <div data-proof-ignore="1" style={{ marginTop: 12, background: 'var(--bg-2)', borderRadius: 'var(--radius-md, 10px)', padding: 14 }}>
              <div style={{ fontWeight: 800, fontSize: 14, marginBottom: 4 }}>政府「揮汗有禮」活動</div>
              <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 10, lineHeight: 1.5 }}>
                產生含日期/時間/距離的證明圖，上傳到 500.gov.tw 完成當週任務。
              </div>
              <div style={{ display: 'flex', gap: 8 }}>
                <button onClick={handleGov500Proof} disabled={gov500Busy}
                  style={{ flex: 1, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '10px', fontSize: 13, cursor: gov500Busy ? 'default' : 'pointer', opacity: gov500Busy ? 0.6 : 1 }}>
                  {gov500Busy ? '產生中…' : '產生證明圖'}
                </button>
                <button onClick={() => window.open('https://500.gov.tw/registrant/', '_blank', 'noopener')}
                  style={{ flex: 1, background: 'var(--bg-1)', color: 'var(--tx)', fontWeight: 700, border: '1px solid var(--line-2)', borderRadius: 9, padding: '10px', fontSize: 13, cursor: 'pointer' }}>
                  前往 500.gov.tw
                </button>
              </div>
            </div>
          )}
          {sel.flagged && <div style={{ marginTop: 10, fontSize: 12, color: '#ff8a8a' }}>⚠️ 此筆標記{sel.review_action === 'rejected' ? '（已駁回，不計）' : sel.review_action === 'approved' ? '（已核准計入）' : '待審'}：{sel.flag_reason}</div>}
        </div>
      )}

      <div style={{ padding: 16 }}>
        {err && <div style={{ color: 'var(--hunt)', fontSize: 13, marginBottom: 10 }}>{err}</div>}
        {!user && <div style={{ color: 'var(--tx-dim)' }}>請先登入</div>}
        {user && !runs && <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
        {runs && runs.length === 0 && <div style={{ color: 'var(--tx-faint)', padding: '16px 0' }}>還沒有跑步紀錄，去 /track 開始第一筆吧 🏃</div>}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          {runs?.map((r) => (
            <button key={r.id} onClick={() => openRun(r.id)} style={{ ...card, textAlign: 'left', cursor: 'pointer' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', gap: 10 }}>
                <div>
                  <div style={{ fontSize: 15, fontWeight: 800 }}>{(r.calib_distance_km ?? r.distance_km).toFixed(2)} km {r.flagged && <span style={{ fontSize: 11, color: '#ff8a8a' }}>⚠️</span>}</div>
                  <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 2 }}>{fmtDt(r.started_at)} · {fmtTime(r.duration_s)} · {fmtPace(r.calib_avg_pace_s ?? r.avg_pace_s)}/km</div>
                </div>
                <span style={{ color: 'var(--fug)', fontSize: 13, alignSelf: 'center' }}>回放 →</span>
              </div>
            </button>
          ))}
        </div>
      </div>
      </ScrollArea>
    </PhoneFrame>
  )
}

function Stat({ label, v }: { label: string; v: string }) {
  return (
    <div style={{ background: 'var(--bg-2)', borderRadius: 10, padding: '8px 12px', textAlign: 'center' }}>
      <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</div>
      <div style={{ fontSize: 16, fontWeight: 800 }}>{v}</div>
    </div>
  )
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: 14, width: '100%' }
const ghost: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 13 }
