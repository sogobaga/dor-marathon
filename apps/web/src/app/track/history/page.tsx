'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { activitiesApi, type GpsRunHistory } from '@/lib/api'
import { getUserToken, withUserAuth, useUser } from '@/lib/userAuth'
import { decodePolylineSegments } from '@/lib/polyline'
import { useDashboard } from '@/lib/useDashboard'
import { qualifiesGov500, gov500RunKey, hasGov500ShotThisWeek } from '@/lib/gov500'
import RunProofScreen from '@/components/RunProofScreen'
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
  // 截圖模式（RunProofScreen，見 lib/gov500.ts 頂部說明 2026-09-06 規則變動）：不再產生任何圖片，
  // 純粹開/關一個全螢幕畫面，選中的紀錄（sel）本身就是要顯示的資料來源。
  const [gov500ScreenOpen, setGov500ScreenOpen] = useState(false)
  useEffect(() => { setGov500ScreenOpen(false) }, [sel]) // 換選別筆紀錄 → 關掉還開著的截圖模式畫面

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
    // 一趟軌跡可能存成多段 ';' 相接的 encoded polyline（斷訊/跳點期間排除，見 lib/polyline.ts 註解）；
    // 每段各畫一條 Leaflet polyline、段落間不連線——舊資料無 ';' 時就是單一段，行為與過去相同。
    const segments = decodePolylineSegments(sel.polyline || '')
    const coords = segments.flat() // 只取起訖點畫圓點；中間排除段不連線不影響
    let cancelled = false
    ;(async () => {
      const L = await loadLeaflet()
      if (cancelled) return
      if (mapRef.current) { mapRef.current.remove(); mapRef.current = null }
      const center = coords[0] || [25.04, 121.56]
      const map = L.map('hist-map').setView(center, 15)
      L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', { maxZoom: 19, attribution: '© OpenStreetMap', crossOrigin: true }).addTo(map) // crossOrigin：磚要能畫進證明圖 canvas 而不汙染（OSM 有 ACAO:*）
      const lines = segments.filter((seg) => seg.length > 1).map((seg) => L.polyline(seg, { color: sel.flagged ? '#ff5a5a' : '#46E3A0', weight: 5 }).addTo(map))
      if (coords.length > 1 && lines.length > 0) {
        L.circleMarker(coords[0], { radius: 7, color: '#fff', fillColor: '#46E3A0', fillOpacity: 1 }).addTo(map).bindTooltip('起')
        L.circleMarker(coords[coords.length - 1], { radius: 7, color: '#fff', fillColor: '#ff5a5a', fillOpacity: 1 }).addTo(map).bindTooltip('終')
        const group = L.featureGroup(lines)
        map.fitBounds(group.getBounds(), { padding: [22, 22] })
      }
      mapRef.current = map
    })()
    return () => { cancelled = true }
  }, [sel])

  // 運動部「揮汗有禮」達標判定（見 lib/gov500.ts，單次 5 公里或 30 分鐘擇一）：distance/avg_pace
  // 一律優先用校正後數值（calib_*），與這個畫面其他統計數字同源。
  const selQual = sel ? qualifiesGov500({ distanceKm: sel.calib_distance_km ?? sel.distance_km, totalS: sel.duration_s }) : null
  const selRunKey = sel ? gov500RunKey(sel.started_at) : ''

  return (
    <PhoneFrame>
      <header style={{ padding: '14px 18px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid var(--line)' }}>
        <a href="/track" style={{ color: 'var(--tx-dim)', fontSize: 14, textDecoration: 'none' }}>← 追蹤</a>
        <strong style={{ fontSize: 16 }}>跑步軌跡歷史</strong>
        <a href="/" style={{ color: 'var(--tx-faint)', fontSize: 13, textDecoration: 'none' }}>首頁</a>
      </header>

      <ScrollArea>
      {sel && (
        <div style={{ padding: 16 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
            <strong>{fmtDt(sel.started_at)}</strong>
            <button onClick={() => { setSel(null); if (mapRef.current) { mapRef.current.remove(); mapRef.current = null } }} style={ghost}>關閉</button>
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
          {/* 已排除異常段（超速／訊號中斷跳點，見 apps/web/src/app/track/page.tsx 的 GAP_MAX_S/GAP_MAX_M）：
              optional 欄位，此功能上線前的舊紀錄沒有這兩欄，undefined/0 都不顯示。 */}
          {(sel.excluded_segments ?? 0) > 0 && (
            <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4, textAlign: 'center' }}>
              ⚠️ 本次有 {sel.excluded_segments} 段異常數據已排除（{(sel.excluded_km ?? 0).toFixed(1)} km 不計入）
            </div>
          )}
          {/* 後台「回收異常數據」（2026-09-03）：整趟被標異常時，讓跑者看得懂為什麼這筆不算——
              軌跡已依 sel.flagged 畫成紅線（見上面 decodePolylineSegments 那段），這裡補文字說明。 */}
          {sel.flagged && (
            <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4, textAlign: 'center' }}>
              ⚠️ 此筆已列為異常，不列入里程與統計
              {sel.flag_reason === 'admin_anomaly'
                ? '（後台判定：跳點／載具軌跡）'
                : /speed|pace/i.test(sel.flag_reason || '')
                  ? '（速度超過人體極限）'
                  : sel.flag_reason
                    ? `（${sel.flag_reason}）`
                    : ''}
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
          {/* 運動部「揮汗有禮」活動：先只給超管看（gov500_entry，見系統設定 gov500_entry_state），
              之後備妥後由系統設定開放給一般玩家。2026-09-06 規則變動（見 lib/gov500.ts 頂部說明）：
              500.gov.tw 只收手機截圖鍵截出的 App 原始畫面，改開「截圖模式」全螢幕畫面讓使用者自己截。 */}
          {dash?.gov500_entry === 'shown' && (
            <div style={{ marginTop: 12, background: 'var(--bg-2)', borderRadius: 'var(--radius-md, 10px)', padding: 14 }}>
              <div style={{ fontWeight: 800, fontSize: 14, marginBottom: 4 }}>運動部「揮汗有禮・全民動起來」活動</div>
              <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 10, lineHeight: 1.5 }}>
                單次跑滿 5 公里或 30 分鐘即可完成本週任務：開啟截圖模式後，用手機截圖鍵擷取整個畫面，再到 500.gov.tw 上傳。
              </div>
              <div style={{ display: 'flex', gap: 8 }}>
                {selQual?.ok ? (
                  <button onClick={() => setGov500ScreenOpen(true)}
                    style={{ flex: 1, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '10px', fontSize: 13, cursor: 'pointer' }}>
                    開啟截圖模式
                  </button>
                ) : (
                  <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', textAlign: 'center', fontSize: 12, color: 'var(--tx-faint)', padding: '0 4px' }}>
                    {selQual?.shortfallText}
                  </div>
                )}
                <button onClick={() => window.open('https://500.gov.tw/registrant/', '_blank', 'noopener')}
                  style={{ flex: 1, background: 'var(--bg-1)', color: 'var(--tx)', fontWeight: 700, border: '1px solid var(--line-2)', borderRadius: 9, padding: '10px', fontSize: 13, cursor: 'pointer' }}>
                  <span style={{ display: 'block', lineHeight: 1.35 }}>上傳證明圖</span>
                  <span style={{ display: 'block', fontSize: 11, opacity: 0.8, lineHeight: 1.35 }}>500.gov.tw</span>
                </button>
              </div>
              {hasGov500ShotThisWeek(selRunKey) && (
                <div style={{ fontSize: 11.5, color: 'var(--fug)', marginTop: 8, lineHeight: 1.5 }}>✓ 本週已截圖</div>
              )}
              <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 8, lineHeight: 1.5 }}>
                小提示：政府網站第一次登入時，讓 Safari「儲存密碼」——之後回訪只要 Face ID 自動填入，免重打帳密。
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
                  {/* 本週已截圖（純前端 localStorage 提醒，見 lib/gov500.ts）：只是提醒「這筆這週截過了」，不是審核依據 */}
                  {hasGov500ShotThisWeek(gov500RunKey(r.started_at)) && (
                    <div style={{ fontSize: 10.5, color: 'var(--fug)', marginTop: 2 }}>✓ 本週已截圖</div>
                  )}
                </div>
                <span style={{ color: 'var(--fug)', fontSize: 13, alignSelf: 'center' }}>回放 →</span>
              </div>
            </button>
          ))}
        </div>
      </div>
      </ScrollArea>

      {/* 截圖模式全螢幕畫面（RunProofScreen，見 lib/gov500.ts 頂部說明）：sel 是這筆歷史紀錄的完整
          detail（含 started_at/ended_at），比 /track 剛跑完當下更精確、不必猜結束時間。 */}
      {gov500ScreenOpen && sel && (
        <RunProofScreen
          startedAt={new Date(sel.started_at)}
          endedAt={sel.ended_at ? new Date(sel.ended_at) : null}
          durationS={sel.duration_s}
          distanceKm={sel.calib_distance_km ?? sel.distance_km}
          avgPaceS={(sel.calib_avg_pace_s ?? sel.avg_pace_s) > 0 ? (sel.calib_avg_pace_s ?? sel.avg_pace_s) : null}
          displayName={user?.name || user?.handle || 'DOR 跑者'}
          recordId={sel.id}
          runKey={selRunKey}
          track={decodePolylineSegments(sel.polyline || '')}
          onClose={() => setGov500ScreenOpen(false)}
        />
      )}
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
