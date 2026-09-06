'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { activitiesApi, profileApi, type GpsRunHistory } from '@/lib/api'
import { getUserToken, withUserAuth, useUser } from '@/lib/userAuth'
import { decodePolylineSegments } from '@/lib/polyline'
import { kmMarkerPositions, addKmMarkers } from '@/lib/kmMarkers'
import { useDashboard } from '@/lib/useDashboard'
import { qualifiesGov500, gov500RunKey, markGov500Shot, hasGov500ShotThisWeek } from '@/lib/gov500'
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
// 揮汗有禮直接截圖需求（2026-09-06）：第一畫面要看得到日期/時段/跑者姓名，比照 RunProofScreen（已刪除）的格式
const WEEKDAY_ZH = ['日', '一', '二', '三', '四', '五', '六']
const p2 = (n: number) => String(n).padStart(2, '0')
const fmtDateBig = (d: Date) => `${d.getFullYear()}/${p2(d.getMonth() + 1)}/${p2(d.getDate())}（${WEEKDAY_ZH[d.getDay()]}）`
const fmtHm = (d: Date) => `${p2(d.getHours())}:${p2(d.getMinutes())}`

export default function TrackHistoryPage() {
  const user = useUser()
  const [runs, setRuns] = useState<GpsRunHistory[] | null>(null)
  const [sel, setSel] = useState<GpsRunHistory | null>(null)
  const detailRef = useRef<HTMLDivElement>(null) // 點列表後把詳情區捲到最上面（2026-09-07 使用者需求）
  const [err, setErr] = useState('')
  const mapRef = useRef<any>(null)
  const { dash } = useDashboard() // 共用會員儀表板快取（見 lib/useDashboard.ts）；這裡只用來讀 gov500_entry
  // 揮汗有禮直接截圖需求（2026-09-06 owner 定案，見 lib/gov500.ts 頂部說明）：紀錄畫面本身要顯示
  // 真實姓名，只在掛載時抓一次（不必每次選別筆紀錄重抓，同一使用者姓名不會變）。
  const [realName, setRealName] = useState('')
  useEffect(() => {
    const t = getUserToken(); if (!t) return
    withUserAuth((tk) => profileApi.getMe(tk)).then((r) => setRealName(r.profile?.real_name || '')).catch(() => {})
  }, [])

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
    // 點下方列表的紀錄後，詳情區渲染在列表上方、但視窗還停在剛才點的位置——自動捲到詳情區頂端，
    // 讓日期/時間/姓名/分段第一時間入眼（也方便直接截圖）。scrollIntoView 會捲最近的可捲動祖先（ScrollArea）。
    detailRef.current?.scrollIntoView({ block: 'start', behavior: 'smooth' })
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
      // 揮汗有禮直接截圖需求（2026-09-06）：軌跡上疊 1/2/3…號碼標記代表跑到第 N 公里的分段完成點，
      // 讓截圖本身就能佐證距離——即使整趟被標異常（紅線），標記仍用一般品牌色，不需要跟著變色。
      // 對抗式審查修正：kmMarkerPositions 只算「原始」haversine 距離，不吃 GPS 距離校正係數，但這個
      // 畫面的「距離」統計（見下方 selQual/Stat）一律優先顯示 calib_distance_km（校正後）——若不換算，
      // 套用過校正的那趟，標記位置／顆數會跟畫面上的校正後距離、每公里分段對不上。校正後距離每滿
      // 1000m ⟺ 原始距離每滿 1000/k m，把 everyM 除以 k 即可，不必更動純函式本身。
      const calibK = sel.calib_factor && sel.calib_factor > 0 ? sel.calib_factor : 1
      const kmMarks = addKmMarkers(L, map, kmMarkerPositions(segments, 1000 / calibK), '#46E3A0')
      if (coords.length > 1 && lines.length > 0) {
        L.circleMarker(coords[0], { radius: 7, color: '#fff', fillColor: '#46E3A0', fillOpacity: 1 }).addTo(map).bindTooltip('起')
        L.circleMarker(coords[coords.length - 1], { radius: 7, color: '#fff', fillColor: '#ff5a5a', fillOpacity: 1 }).addTo(map).bindTooltip('終')
        const group = L.featureGroup([...lines, ...kmMarks])
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
        <div ref={detailRef} style={{ padding: 16 }}>
          {/* 揮汗有禮直接截圖需求（2026-09-06 owner 定案）：第一畫面就要看得到日期、開始/結束時間、
              真實姓名——不再靠另一個「截圖模式」畫面湊，這個畫面本身就是要截的畫面。 */}
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', marginBottom: 4 }}>
            <strong style={{ fontSize: 18, fontVariantNumeric: 'tabular-nums' }}>{fmtDateBig(new Date(sel.started_at))}</strong>
            <button onClick={() => { setSel(null); if (mapRef.current) { mapRef.current.remove(); mapRef.current = null } }} style={ghost}>關閉</button>
          </div>
          <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginBottom: 2 }}>
            開始 {fmtHm(new Date(sel.started_at))} ～ 結束 {fmtHm(sel.ended_at ? new Date(sel.ended_at) : new Date(new Date(sel.started_at).getTime() + sel.duration_s * 1000))}
          </div>
          <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginBottom: 12 }}>
            跑者　{realName || user?.name || user?.handle || 'DOR 跑者'}
            {!realName && <span style={{ color: 'var(--tx-faint)', fontSize: 11 }}>（請至個人資料填寫真實姓名）</span>}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 10 }}>
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
          {/* 每公里分段配速：移到地圖上方（揮汗有禮截圖優先看得到分段，2026-09-06），壓縮列高
              （fontSize 12.5、gap 4）讓前 5 段連同上面日期/統計仍能一起塞進第一畫面。 */}
          {sel.km_paces && sel.km_paces.length > 0 ? (
            <div style={{ marginTop: 12 }}>
              <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 5 }}>每公里分段配速</div>
              {(() => {
                const paces = sel.km_paces!
                const mx = Math.max(...paces), mn = Math.min(...paces)
                // 超過 10 段改多欄並排（2026-09-07 使用者：同一塊高度要放得下 20km）：≤10 段一欄、11–20 段兩欄、
                // 更多三欄；多欄模式把「第」「/km」拿掉、字級縮小，讓 375px 寬每欄仍放得下「12 ▮▮▮ 6:39」。
                // 順序採「先直後橫」（column-major）：第 1–10 段在左欄、11–20 在右欄，跟閱讀習慣一致。
                const cols = paces.length <= 10 ? 1 : paces.length <= 20 ? 2 : 3
                const rows = Math.ceil(paces.length / cols)
                const compact = cols > 1
                return (
                  <div style={{ display: 'grid', gridTemplateColumns: `repeat(${cols}, minmax(0,1fr))`, columnGap: 10, rowGap: 4 }}>
                    {paces.map((p, i) => {
                      const pct = mx > mn ? 100 - ((p - mn) / (mx - mn)) * 62 : 100 // 越快(秒少)條越長
                      const col = Math.floor(i / rows), row = i % rows
                      return (
                        <div key={i} style={{ gridColumn: col + 1, gridRow: row + 1, display: 'flex', alignItems: 'center', gap: compact ? 5 : 8, fontSize: compact ? 11.5 : 12.5, minWidth: 0 }}>
                          <span style={{ minWidth: compact ? 18 : 42, whiteSpace: 'nowrap', color: 'var(--tx-dim)', flexShrink: 0, textAlign: compact ? 'right' : 'left', fontVariantNumeric: 'tabular-nums' }}>{compact ? i + 1 : `第${i + 1}km`}</span>
                          <div style={{ flex: 1, height: compact ? 6 : 7, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden' }}>
                            <div style={{ height: '100%', width: `${pct}%`, background: 'linear-gradient(90deg,#FFD24D,#46E3A0)', borderRadius: 999 }} />
                          </div>
                          <span style={{ minWidth: compact ? 32 : 54, whiteSpace: 'nowrap', textAlign: 'right', fontWeight: 700, fontVariantNumeric: 'tabular-nums', flexShrink: 0 }}>{compact ? fmtPace(p) : `${fmtPace(p)}/km`}</span>
                        </div>
                      )
                    })}
                  </div>
                )
              })()}
            </div>
          ) : (
            <div style={{ marginTop: 12, fontSize: 11.5, color: 'var(--tx-faint)' }}>（此筆沒有每公里分段資料；v0.1.205 之後的新 GPS 跑步才會記錄）</div>
          )}
          <div id="hist-map" style={{ width: '100%', height: 220, borderRadius: 10, overflow: 'hidden', background: 'var(--bg-2)', marginTop: 12 }} />
          {/* 運動部「揮汗有禮」活動：先只給超管看（gov500_entry，見系統設定 gov500_entry_state），
              之後備妥後由系統設定開放給一般玩家。2026-09-06 規則變動（owner 定案，見 lib/gov500.ts
              頂部說明）：500.gov.tw 只收手機系統截圖鍵截出的 App 原始紀錄畫面——這個畫面現在「就是」
              那個原始畫面（日期/時間/姓名/距離/時間/分段配速/軌跡號碼標記全都在上面），不再需要另開
              一個「截圖模式」畫面，使用者直接對這裡按截圖鍵即可。 */}
          {dash?.gov500_entry === 'shown' && (
            <div style={{ marginTop: 12, background: 'var(--bg-2)', borderRadius: 'var(--radius-md, 10px)', padding: 14 }}>
              <div style={{ fontWeight: 800, fontSize: 14, marginBottom: 4 }}>運動部「揮汗有禮・全民動起來」活動</div>
              <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 8, lineHeight: 1.5 }}>
                單次跑滿 5 公里或 30 分鐘即可完成本週任務。達標後直接用手機截圖鍵擷取這個畫面（整個畫面、不要裁切，需看得到日期與達標數值），再到 500.gov.tw 上傳。
              </div>
              {selQual?.ok ? (
                <div style={{ fontSize: 12.5, color: 'var(--fug)', fontWeight: 700, marginBottom: 10 }}>✓ 本趟已達標，直接截圖此畫面即可</div>
              ) : (
                <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 10 }}>{selQual?.shortfallText}</div>
              )}
              <button onClick={() => { markGov500Shot(selRunKey); window.open('https://500.gov.tw/registrant/', '_blank', 'noopener') }}
                style={{ width: '100%', background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '10px', fontSize: 13, cursor: 'pointer' }}>
                前往運動部活動網頁，上傳截圖
              </button>
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
