'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { activitiesApi, profileApi, type GpsRunHistory, type Profile } from '@/lib/api'
import { getUserToken, withUserAuth, useUser } from '@/lib/userAuth'
import { decodePolylineSegments } from '@/lib/polyline'
import { useDashboard } from '@/lib/useDashboard'
import { captureRunProofFromDom, deliverRunProof, generateRunProofImage } from '@/lib/runProof'
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
  const proofAreaRef = useRef<HTMLDivElement | null>(null) // 證明圖擷取範圍（詳情容器；關閉鈕/活動卡以 data-proof-ignore 排除）
  const coordsRef = useRef<[number, number][]>([]) // 目前選中紀錄的軌跡座標（snapshotMap 重投影用）
  const profileRef = useRef<Profile | null>(null)  // 個資快取（真實姓名）
  const [askRealName, setAskRealName] = useState(false)
  const [realNameInput, setRealNameInput] = useState('')
  const [realNameSaving, setRealNameSaving] = useState(false)
  const [gov500Msg, setGov500Msg] = useState('') // 下載完成提示（幾秒後自動清除）
  const proofCacheRef = useRef<{ key: string; blob: Blob } | null>(null) // 兩段式的成品快取（第一段建立、第二段即時分享）
  const [proofReadyKey, setProofReadyKey] = useState('') // 已建立完成的 cacheKey（控制按鈕名：建立證明圖→儲存證明圖）
  useEffect(() => { setProofReadyKey(''); setGov500Msg('') }, [sel]) // 換選別筆紀錄 → 兩段式狀態重置 // 政府「揮汗有禮」證明圖產生中（gov500_entry，見 lib/runProof.ts）

  // 把「畫面上的地圖」重繪成一張乾淨 canvas：磚用實際渲染位置（getBoundingClientRect 已含
  // Leaflet 所有 transform）、軌跡/起終點用 latLngToContainerPoint 重投影——與畫面同一套座標。
  // 動機：html2canvas 對 Leaflet 多層 translate3d 的疊層計算不準，成品的軌跡會相對磚面偏移
  //（使用者實測回報）；預繪快照在擷取時替換活地圖即可完全繞開。磚需 CORS 載入
  //（tileLayer crossOrigin:true），否則畫入即汙染 canvas、toBlob 會失敗（回 null 走合成卡退路）。
  async function snapshotMap(): Promise<HTMLCanvasElement | null> {
    const map = mapRef.current
    const cont = document.getElementById('hist-map')
    const coords = coordsRef.current
    if (!map || !cont || coords.length < 2) return null
    const rect = cont.getBoundingClientRect()
    if (rect.width < 10 || rect.height < 10) return null
    const cv = document.createElement('canvas')
    cv.width = Math.round(rect.width * 2); cv.height = Math.round(rect.height * 2)
    const ctx = cv.getContext('2d')
    if (!ctx) return null
    ctx.scale(2, 2)
    ctx.fillStyle = '#e8e6e1'; ctx.fillRect(0, 0, rect.width, rect.height) // 磚縫底色
    // 磚不直接 drawImage(DOM img)：瀏覽器可能命中「無 ACAO 標頭的舊快取」，畫入即汙染 canvas、
    // toDataURL 丟 SecurityError（v720 靜默退回合成卡=沒有地圖的原因）。改 fetch(mode:'cors')
    // 重取（OSM 磚有 ACAO:*），逐磚失敗只留底色缺口、不影響整體。
    const tiles = Array.from(cont.querySelectorAll<HTMLImageElement>('img.leaflet-tile'))
      .filter((t) => t.complete && t.naturalWidth > 0)
    await Promise.all(tiles.map(async (t) => {
      try {
        const res = await fetch(t.src, { mode: 'cors' })
        if (!res.ok) return
        const bmp = await createImageBitmap(await res.blob())
        const r = t.getBoundingClientRect()
        ctx.drawImage(bmp, r.left - rect.left, r.top - rect.top, r.width, r.height)
      } catch { /* 單磚失敗：留底色 */ }
    }))
    const pts = coords.map((c) => map.latLngToContainerPoint(c))
    ctx.lineJoin = 'round'; ctx.lineCap = 'round'
    ctx.strokeStyle = sel?.flagged ? '#ff5a5a' : '#46E3A0'; ctx.lineWidth = 5
    ctx.beginPath()
    pts.forEach((pt, i) => (i ? ctx.lineTo(pt.x, pt.y) : ctx.moveTo(pt.x, pt.y)))
    ctx.stroke()
    const dot = (pt: { x: number; y: number }, fill: string) => {
      ctx.beginPath(); ctx.arc(pt.x, pt.y, 7, 0, Math.PI * 2)
      ctx.fillStyle = fill; ctx.fill(); ctx.lineWidth = 2; ctx.strokeStyle = '#fff'; ctx.stroke()
    }
    dot(pts[0], '#46E3A0'); dot(pts[pts.length - 1], '#ff5a5a')
    // OSM 授權標示（ODbL 要求，重繪後仍須保留）
    ctx.font = '11px -apple-system, sans-serif'
    const attr = '© OpenStreetMap'
    const tw = ctx.measureText(attr).width
    ctx.fillStyle = 'rgba(255,255,255,.78)'; ctx.fillRect(rect.width - tw - 12, rect.height - 20, tw + 12, 20)
    ctx.fillStyle = '#333'; ctx.fillText(attr, rect.width - tw - 6, rect.height - 6)
    ctx.getImageData(0, 0, 1, 1) // 汙染自檢：被汙染會在這裡丟 SecurityError，交外層以明確訊息退回合成卡
    return cv
  }

  // 取「真實姓名」：政府網站上傳需與身分相符的姓名，不能用暱稱（使用者明確要求）。
  // 個資已填→直接用；沒填→開彈窗補填，儲存回個人資料（之後不用再填）並自動接續產圖。
  async function ensureRealName(): Promise<string | null> {
    const cached = profileRef.current?.real_name?.trim()
    if (cached) return cached
    const { profile } = await withUserAuth((t) => profileApi.getMe(t))
    profileRef.current = profile
    const rn = (profile.real_name || '').trim()
    if (rn) return rn
    setRealNameInput('')
    setAskRealName(true)
    return null
  }

  async function saveRealNameAndContinue() {
    const rn = realNameInput.trim()
    if (!rn || realNameSaving) return
    setRealNameSaving(true); setErr('')
    try {
      const prev = profileRef.current
      // 讀改寫全欄位：後端 PUT /profile 的空字串有「清空該欄」語意，不能只送 real_name
      const { profile } = await withUserAuth((t) => profileApi.updateMe(t, {
        name: prev?.name, avatar_url: prev?.avatar_url, real_name: rn, nickname: prev?.nickname,
        phone: prev?.phone, address: prev?.address, birthday: prev?.birthday, gender: prev?.gender,
      }))
      profileRef.current = profile
      setAskRealName(false)
      await runProofFlow(rn)
    } catch (e: any) {
      setErr(e?.message || '儲存失敗，請再試一次')
    } finally {
      setRealNameSaving(false)
    }
  }

  // 政府「揮汗有禮」活動證明圖：優先「畫面實況擷取」（含 GPS 軌跡圖/統計/分段計量表，與畫面
  // 一致才不會有作假疑慮）；地圖以 snapshotMap 預繪快照替換（見上）。擷取鏈任一環失敗
  //（磚汙染/記憶體不足等）退回合成卡片（仍含官方要求的日期/時間/距離）。數據同源（校正後優先）。
  // 確定性兩段式（使用者定案：兩次點擊要有不同按鈕名，否則像 bug）——
  // 第一段「建立證明圖」：產圖耗時數秒、iOS 手勢必過期，所以這段**不**嘗試開分享面板，
  // 完成後快取成品、按鈕切成「儲存證明圖」並給綠色提示；
  // 第二段「儲存證明圖」：用快取成品在手勢當下即時開分享面板（毫秒級不會過期）→ 儲存影像 → 相簿。
  async function runProofFlow(realName: string) {
    if (!sel) return
    const cacheKey = `${sel.started_at}|${realName}`

    // 第二段
    if (proofCacheRef.current?.key === cacheKey) {
      const fname = `DOR跑步證明_${new Date(sel.started_at).toISOString().slice(0, 10).replace(/-/g, '')}.png`
      const how = await deliverRunProof(proofCacheRef.current.blob, fname)
      if (how === 'retry') {
        setGov500Msg('請再點一次「儲存證明圖」') // 理論上不會發生（即時開啟），保險提示
      } else if (how === 'downloaded') {
        setGov500Msg('已下載證明圖：在「檔案」App 的「下載項目」可找到，直接到 500.gov.tw 上傳即可')
        setTimeout(() => setGov500Msg(''), 8000)
      } else {
        setGov500Msg('')
      }
      return
    }

    // 第一段
    setGov500Busy(true)
    try {
      let blob: Blob
      try {
        const mapCv = await snapshotMap()
        const mapEl = document.getElementById('hist-map')
        if (!proofAreaRef.current || !mapCv || !mapEl) throw new Error('地圖快照未就緒')
        blob = await captureRunProofFromDom(proofAreaRef.current, realName, { mapOverlay: { el: mapEl, canvas: mapCv, selector: '#hist-map' } })
      } catch (capErr: any) {
        // 退回合成卡仍可交差（含官方要求欄位），但把原因顯示出來——靜默退回會讓「圖裡沒地圖」
        // 變成無從追查的謎（v720 教訓）。
        setErr(`實況擷取失敗（${capErr?.message || capErr?.name || '未知'}），已改用合成卡片`)
        const avgPaceS = sel.calib_avg_pace_s ?? sel.avg_pace_s
        blob = await generateRunProofImage({
          startedAt: new Date(sel.started_at),
          durationS: sel.duration_s,
          distanceKm: sel.calib_distance_km ?? sel.distance_km,
          avgPaceS: avgPaceS > 0 ? avgPaceS : null,
          displayName: realName,
        })
      }
      proofCacheRef.current = { key: cacheKey, blob }
      setProofReadyKey(cacheKey)
      setGov500Msg('證明圖已建立完成——點「儲存證明圖」存入相簿')
    } catch (e: any) {
      setErr(e?.message || '證明圖建立失敗，請再試一次')
    } finally {
      setGov500Busy(false)
    }
  }

  async function handleGov500Proof() {
    if (!sel || gov500Busy) return
    try {
      const rn = await ensureRealName()
      if (!rn) return // 彈窗接手：儲存真實姓名後自動接續產圖
      await runProofFlow(rn)
    } catch (e: any) {
      setErr(e?.message || '讀取個人資料失敗，請再試一次')
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
    // 一趟軌跡可能存成多段 '|' 相接的 encoded polyline（斷訊/跳點期間排除，見 lib/polyline.ts 註解）；
    // 每段各畫一條 Leaflet polyline、段落間不連線——舊資料無 '|' 時就是單一段，行為與過去相同。
    const segments = decodePolylineSegments(sel.polyline || '')
    const coords = segments.flat()
    coordsRef.current = coords // snapshotMap 重投影仍用扁平座標：只取起訖點畫圓點，中間排除段不連線不影響
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
          {/* 政府「揮汗有禮」活動證明圖：先只給超管看（gov500_entry，見系統設定 gov500_entry_state），
              之後備妥後由系統設定開放給一般玩家。 */}
          {dash?.gov500_entry === 'shown' && (
            <div data-proof-ignore="1" style={{ marginTop: 12, background: 'var(--bg-2)', borderRadius: 'var(--radius-md, 10px)', padding: 14 }}>
              <div style={{ fontWeight: 800, fontSize: 14, marginBottom: 4 }}>運動部「揮汗有禮・全民動起來」活動</div>
              <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 10, lineHeight: 1.5 }}>
                產生含日期/時間/距離的證明圖並存入相簿（點分享面板的「儲存影像」），再到 500.gov.tw 上傳。
              </div>
              <div style={{ display: 'flex', gap: 8 }}>
                <button onClick={handleGov500Proof} disabled={gov500Busy}
                  style={{ flex: 1, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '10px', fontSize: 13, cursor: gov500Busy ? 'default' : 'pointer', opacity: gov500Busy ? 0.6 : 1 }}>
                  {gov500Busy ? '建立中…' : proofReadyKey.startsWith(`${sel.started_at}|`) ? '儲存證明圖' : '建立證明圖'}
                </button>
                <button onClick={() => window.open('https://500.gov.tw/registrant/', '_blank', 'noopener')}
                  style={{ flex: 1, background: 'var(--bg-1)', color: 'var(--tx)', fontWeight: 700, border: '1px solid var(--line-2)', borderRadius: 9, padding: '10px', fontSize: 13, cursor: 'pointer' }}>
                  <span style={{ display: 'block', lineHeight: 1.35 }}>上傳證明圖</span>
                  <span style={{ display: 'block', fontSize: 11, opacity: 0.8, lineHeight: 1.35 }}>500.gov.tw</span>
                </button>
              </div>
              {gov500Msg && <div style={{ fontSize: 12, color: 'var(--fug)', marginTop: 8, lineHeight: 1.5 }}>✓ {gov500Msg}</div>}
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
                </div>
                <span style={{ color: 'var(--fug)', fontSize: 13, alignSelf: 'center' }}>回放 →</span>
              </div>
            </button>
          ))}
        </div>
      </div>
      </ScrollArea>

      {/* 真實姓名補填彈窗：證明圖要上傳政府網站，署名需真實姓名（暱稱無法核對身分）；
          填一次即回存個人資料。此頁為手機全螢幕路由（PhoneFrame），fixed 覆蓋可視區即可。 */}
      {askRealName && (
        <div style={{ position: 'fixed', inset: 0, zIndex: 1600, background: 'rgba(0,0,0,.55)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 24 }}>
          <div style={{ width: '100%', maxWidth: 340, background: 'var(--bg-1)', border: '1px solid var(--line-2)', borderRadius: 14, padding: 18 }}>
            <div style={{ fontWeight: 800, fontSize: 15, marginBottom: 6 }}>請輸入真實姓名</div>
            <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', lineHeight: 1.6, marginBottom: 12 }}>
              證明圖要上傳到政府活動網站，署名需使用真實姓名（暱稱無法核對身分）。填寫後會存入你的個人資料，之後不用再填。
            </div>
            <input value={realNameInput} onChange={(e) => setRealNameInput(e.target.value)} placeholder="與身分證件相同的姓名"
              style={{ width: '100%', boxSizing: 'border-box', background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '10px 12px', fontSize: 14, color: 'var(--tx)', marginBottom: 12 }} />
            <div style={{ display: 'flex', gap: 8 }}>
              <button onClick={saveRealNameAndContinue} disabled={!realNameInput.trim() || realNameSaving}
                style={{ flex: 1, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '10px', fontSize: 13.5, cursor: 'pointer', opacity: !realNameInput.trim() || realNameSaving ? 0.5 : 1 }}>
                {realNameSaving ? '儲存中…' : '確認並產生證明圖'}
              </button>
              <button onClick={() => setAskRealName(false)} style={{ background: 'var(--bg-2)', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '10px 14px', fontSize: 13.5, cursor: 'pointer' }}>取消</button>
            </div>
          </div>
        </div>
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
