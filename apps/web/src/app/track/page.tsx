'use client'

import { useEffect, useRef, useState, useCallback } from 'react'
import { activitiesApi, checkpointApi, eventApi, type GpsPoint, type GpsRunResult, type ActiveCheckpoint, type EventDef } from '@/lib/api'
import { getUserToken, withUserAuth, useUser } from '@/lib/userAuth'
import { loadLeaflet } from '@/lib/leaflet'
import GoogleAuthProvider from '@/components/GoogleAuthProvider'
import { LoginModal } from '@/components/UserAuthBar'
import PhoneFrame from '@/components/PhoneFrame'
import ScrollArea from '@/components/ScrollArea'
import { EventBanner, EventResultBanner, type ActiveEvent, type EventResult } from '@/components/EventTaskModal'

/* eslint-disable @typescript-eslint/no-explicit-any */

const LS_KEY = 'dor_gps_run'
const MAX_ACC = 65 // 精度差於此（公尺）的點不採計距離（城市/大樓旁訊號較差，放寬以免整趟記不到）
const MAX_SPEED = 1000 / 120 // 8.33 m/s（2:00/km）人類極限上限
const JITTER_MIN = 6 // 公尺：距上一個採納點移動不足此值視為原地抖動，不計距離
const EVENT_GRACE_MS = 3000 // 事件觸發後的「準備期」：吸收偵測+反應+延遲，倒數結束才開始計算
const PACE_MIN_KM = 0.005 // 累積達此距離（5m，約顯示 0.01km 時）即顯示平均配速

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
  const [showLogin, setShowLogin] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [checkpoints, setCheckpoints] = useState<ActiveCheckpoint[]>([])
  const [curPos, setCurPos] = useState<{ lat: number; lng: number; acc: number } | null>(null)
  const [cpBusy, setCpBusy] = useState('') // 正在打卡的 checkpoint id
  const [cpMsg, setCpMsg] = useState('')
  // 事件任務（日常隨機）
  const [eventDefs, setEventDefs] = useState<EventDef[]>([])
  const [activeEvent, setActiveEvent] = useState<ActiveEvent | null>(null)
  const [eventMoved, setEventMoved] = useState(0)
  const [eventResult, setEventResult] = useState<EventResult | null>(null)
  const [mapReady, setMapReady] = useState(false)

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
  const cpLayerRef = useRef<any>(null) // 地圖上的打卡點圖層
  const warnTimer = useRef<any>(null)
  const statusRef = useRef(status)
  statusRef.current = status
  const lastAccRef = useRef<GpsPoint | null>(null) // 上一個「採納」的點（過濾原地抖動用）
  // 事件引擎用
  const distSamplesRef = useRef<{ t: number; d: number }[]>([]) // {時間ms, 累積距離m}
  const eventDefsRef = useRef<EventDef[]>([])
  const activeEventRef = useRef<ActiveEvent | null>(null)
  const lastEventEndRef = useRef(0) // 上次事件結束時間（冷卻用）
  const evalTimerRef = useRef<any>(null)
  eventDefsRef.current = eventDefs

  const ensureMap = useCallback(async (lat: number, lng: number) => {
    const L = await loadLeaflet()
    if (mapRef.current) return
    const map = L.map('gps-map', { zoomControl: true }).setView([lat, lng], 16)
    L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', { maxZoom: 19, attribution: '© OpenStreetMap' }).addTo(map)
    lineRef.current = L.polyline([], { color: '#46E3A0', weight: 5 }).addTo(map)
    cpLayerRef.current = L.layerGroup().addTo(map)
    markRef.current = L.circleMarker([lat, lng], { radius: 7, color: '#fff', fillColor: '#46E3A0', fillOpacity: 1, weight: 2 }).addTo(map)
    mapRef.current = map
    setMapReady(true)
  }, [])

  const onPos = useCallback((pos: GeolocationPosition) => {
    const p: GpsPoint = { lat: pos.coords.latitude, lng: pos.coords.longitude, t: pos.timestamp, acc: pos.coords.accuracy ?? 0 }
    setCurPos({ lat: p.lat, lng: p.lng, acc: p.acc })
    ensureMap(p.lat, p.lng)
    const goodAcc = p.acc === 0 || p.acc <= MAX_ACC
    if (!goodAcc) {
      setWarn(`GPS 訊號較弱（±${Math.round(p.acc)}m），移動可能未被記錄，請到較空曠處`)
      clearTimeout(warnTimer.current)
      warnTimer.current = setTimeout(() => setWarn(''), 4000)
    }

    // 距離以「上一個採納點」為基準計算；移動不足門檻 → 視為原地抖動，不採納、不累積
    if (goodAcc) {
      const lastAcc = lastAccRef.current
      if (!lastAcc) {
        // 第一個有效點：當作起點
        lastAccRef.current = p
        pointsRef.current.push(p)
        if (lineRef.current) lineRef.current.addLatLng([p.lat, p.lng])
      } else {
        const d = haversineM(lastAcc, p)
        const dt = (p.t - lastAcc.t) / 1000
        if (d >= JITTER_MIN && dt > 0) {
          // 超過人體極限的段落 → 視為 GPS 跳點（城市多路徑常見）：距離只計到「極限值」上限
          //（不灌爆里程/害配速失真），但仍前進採納點、仍累積距離 → 移動不會整段消失。
          let seg = d
          if (d / dt > MAX_SPEED) {
            seg = MAX_SPEED * dt
            setAnomalies((n) => n + 1)
          }
          distRef.current += seg
          setDistance(distRef.current)
          // 每公里分段
          const km = Math.floor(distRef.current / 1000)
          const el = (p.t - startRef.current) / 1000
          while (splitMarkRef.current.length < km) {
            const prevEl = splitMarkRef.current.length ? splitMarkRef.current[splitMarkRef.current.length - 1] : 0
            splitMarkRef.current.push(el)
            setSplits((s) => [...s, el - prevEl])
          }
          lastAccRef.current = p
          pointsRef.current.push(p)
          if (lineRef.current) lineRef.current.addLatLng([p.lat, p.lng])
        }
      }
    }
    // 標記點與地圖永遠跟著「目前」位置（即時感），即使該點未被採納為距離
    if (markRef.current) markRef.current.setLatLng([p.lat, p.lng])
    if (mapRef.current) mapRef.current.panTo([p.lat, p.lng])
    // 事件進行中：更新即時位移（進度條）
    if (activeEventRef.current) setEventMoved(distRef.current - activeEventRef.current.triggerD)
    // 防當掉：暫存採納後的軌跡
    localStorage.setItem(LS_KEY, JSON.stringify({ start: startRef.current, points: pointsRef.current.slice(-2000) }))
  }, [ensureMap])

  async function acquireWake() {
    try { wakeRef.current = await (navigator as any).wakeLock?.request('screen') } catch { /* ignore */ }
  }

  // --- 事件任務引擎 ---
  // 近 windowMs 的位移（公尺）；歷史不足回 null
  function movedInWindow(windowMs: number): number | null {
    const s = distSamplesRef.current
    if (s.length < 2) return null
    const target = Date.now() - windowMs
    let past: { t: number; d: number } | null = null
    for (let i = s.length - 1; i >= 0; i--) { if (s[i].t <= target) { past = s[i]; break } }
    if (!past) return null // 尚無足夠歷史（跑步時間短於觀察視窗）
    return distRef.current - past.d
  }
  function triggerEligible(def: EventDef): boolean {
    const p = def.trigger_params
    const moved = movedInWindow((p.window_s ?? 0) * 1000)
    if (moved == null) return false
    if (def.trigger_type === 'distance_below') return moved < (p.max_move_m ?? 0)
    if (def.trigger_type === 'distance_above') return moved > (p.min_move_m ?? 0)
    return false
  }
  function pickWeighted(list: EventDef[]): EventDef {
    const total = list.reduce((s, d) => s + Math.max(1, d.weight), 0)
    let r = Math.random() * total
    for (const d of list) { r -= Math.max(1, d.weight); if (r <= 0) return d }
    return list[list.length - 1]
  }
  async function armEvent(def: EventDef) {
    const token = getUserToken(); if (!token || !def.id) return
    const triggerD = distRef.current, triggerT = Date.now()
    const limitS = def.completion_params.limit_s || 60
    const readyUntil = triggerT + EVENT_GRACE_MS // 準備期結束才開始計算完成
    const ae: ActiveEvent = { def, occId: '', triggerD, triggerT, readyUntil, deadline: readyUntil + limitS * 1000 }
    activeEventRef.current = ae; setActiveEvent(ae); setEventMoved(0); setEventResult(null)
    try {
      const occ = await eventApi.createOccurrence(token, { def_id: def.id, trigger_dist_m: triggerD, trigger_elapsed_s: Math.floor((triggerT - startRef.current) / 1000) })
      const armed = { ...ae, occId: occ.id }
      activeEventRef.current = armed; setActiveEvent(armed)
    } catch { activeEventRef.current = null; setActiveEvent(null) }
  }
  async function completeEvent(ae: ActiveEvent, moved: number, windowS: number) {
    activeEventRef.current = null; setActiveEvent(null); lastEventEndRef.current = Date.now()
    const token = getUserToken()
    try {
      const res = token ? await eventApi.complete(token, ae.occId, { moved_m: moved, window_s: windowS }) : { completed: false }
      setEventResult(res.completed
        ? { status: 'completed', def: ae.def, reward_exp: res.reward_exp ?? 0, reward_dp: res.reward_dp ?? 0 }
        : { status: 'failed', def: ae.def, reward_exp: 0, reward_dp: 0 })
    } catch { setEventResult({ status: 'failed', def: ae.def, reward_exp: 0, reward_dp: 0 }) }
  }
  function failEvent(ae: ActiveEvent) {
    activeEventRef.current = null; setActiveEvent(null); lastEventEndRef.current = Date.now()
    const token = getUserToken()
    if (token && ae.occId) eventApi.fail(token, ae.occId).catch(() => {})
    setEventResult({ status: 'failed', def: ae.def, reward_exp: 0, reward_dp: 0 })
  }
  function evalTick() {
    if (statusRef.current !== 'tracking') return
    const now = Date.now()
    distSamplesRef.current.push({ t: now, d: distRef.current })
    if (distSamplesRef.current.length > 1600) distSamplesRef.current.splice(0, 400) // 上限 ~25 分鐘
    const ae = activeEventRef.current
    if (ae) {
      // 準備期：基準線持續對齊「目前位置」，完成計算尚未起算（吸收偵測/反應/延遲誤差）
      if (now < ae.readyUntil) { ae.triggerD = distRef.current; setEventMoved(0); return }
      const moved = distRef.current - ae.triggerD
      setEventMoved(moved)
      const cp = ae.def.completion_params
      const windowS = (now - ae.readyUntil) / 1000 // 計時從準備結束起算
      if (ae.def.completion_type === 'move_more') {
        if (moved >= (cp.target_m ?? 0)) completeEvent(ae, moved, windowS)
        else if (now > ae.deadline) failEvent(ae)
      } else if (ae.def.completion_type === 'move_less') {
        if (moved > (cp.max_m ?? 0)) failEvent(ae)
        else if (now >= ae.deadline) completeEvent(ae, moved, windowS)
      }
      return
    }
    // 無進行中事件 → 依冷卻 + 觸發條件挑選
    const eligible = eventDefsRef.current.filter((d) =>
      now - lastEventEndRef.current >= (d.cooldown_sec || 0) * 1000 && triggerEligible(d))
    if (eligible.length > 0) armEvent(pickWeighted(eligible))
  }

  function start() {
    setErr('')
    if (!navigator.geolocation) { setErr('此裝置/瀏覽器不支援定位'); return }
    pointsRef.current = []; distRef.current = 0; splitMarkRef.current = []; lastAccRef.current = null
    setDistance(0); setElapsed(0); setSplits([]); setAnomalies(0); setResult(null)
    if (lineRef.current) lineRef.current.setLatLngs([]) // 清掉上一趟的軌跡線（避免地圖殘留）
    // 事件引擎重置
    distSamplesRef.current = []; activeEventRef.current = null; lastEventEndRef.current = 0
    setActiveEvent(null); setEventResult(null); setEventMoved(0)
    startRef.current = Date.now()
    setStatus('tracking')
    // ⚠️ iOS：定位權限提示必須在使用者手勢「同步」流程內直接請求，不能先 await 任何東西
    //（否則會失去使用者手勢 → Safari 直接判定拒絕，code 1）。故先請求定位，wake lock 之後再背景取得。
    watchRef.current = navigator.geolocation.watchPosition(
      onPos,
      (e) => {
        if (e.code === 1) setErr('定位權限被拒：Safari 跳出詢問時請按「允許」；若沒跳出，到 設定 → Apps → Safari → 位置 設為「詢問」或「允許」後重試。')
        else if (e.code === 3) setErr('定位逾時，請到較空曠處再試（室內 GPS 收訊較差）。')
        else setErr('定位失敗：' + (e.message || '請確認已開啟定位'))
      },
      { enableHighAccuracy: true, maximumAge: 0, timeout: 20000 },
    )
    timerRef.current = setInterval(() => setElapsed((Date.now() - startRef.current) / 1000), 250)
    evalTimerRef.current = setInterval(evalTick, 1000) // 事件引擎每秒評估
    acquireWake() // 不 await：wake lock 失敗或延遲都不影響定位
  }

  const cleanup = useCallback(() => {
    if (watchRef.current != null) navigator.geolocation.clearWatch(watchRef.current)
    watchRef.current = null
    clearInterval(timerRef.current)
    clearInterval(evalTimerRef.current)
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
  // 只在掛載/卸載執行：status 變動不可觸發 cleanup（否則會清掉 start 剛建立的計時器與定位）
  useEffect(() => {
    const onVis = () => { if (document.visibilityState === 'visible' && statusRef.current === 'tracking') acquireWake() }
    document.addEventListener('visibilitychange', onVis)
    return () => { document.removeEventListener('visibilitychange', onVis); cleanup() }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 打卡點：載入「進行中賽事 + 已報名」的可打卡點
  const fetchCheckpoints = useCallback(async () => {
    const token = getUserToken()
    if (!token) { setCheckpoints([]); return }
    try {
      const { checkpoints } = await checkpointApi.active(token)
      setCheckpoints(checkpoints)
    } catch { /* ignore */ }
  }, [])
  useEffect(() => { fetchCheckpoints() }, [fetchCheckpoints, user?.id])

  // 載入啟用中的事件定義（供跑步引擎）
  const fetchEventDefs = useCallback(async () => {
    const token = getUserToken()
    if (!token) { setEventDefs([]); return }
    try { const { defs } = await eventApi.active(token); setEventDefs(defs) } catch { /* ignore */ }
  }, [])
  useEffect(() => { fetchEventDefs() }, [fetchEventDefs, user?.id])

  // 在跑步地圖上標出打卡點（已打卡綠/待審金/未打卡灰）→ 邊跑邊探索、就近打卡
  useEffect(() => {
    if (!mapReady || !mapRef.current || !cpLayerRef.current) return
    loadLeaflet().then((L) => {
      const layer = cpLayerRef.current
      layer.clearLayers()
      checkpoints.forEach((cp) => {
        const color = cp.checked ? '#46E3A0' : cp.pending ? '#FFC24B' : '#9aa0a6'
        L.circle([cp.lat, cp.lng], { radius: cp.radius_m || 20, color, weight: 1.5, fillOpacity: 0.1 }).addTo(layer)
        L.circleMarker([cp.lat, cp.lng], { radius: 6, color: '#fff', weight: 2, fillColor: color, fillOpacity: 1 }).addTo(layer).bindTooltip(cp.title || '打卡點')
      })
    })
  }, [checkpoints, mapReady])

  async function doCheckin(cp: ActiveCheckpoint) {
    setCpMsg('')
    const token = getUserToken()
    if (!token) { setShowLogin(true); return }
    setCpBusy(cp.id)
    try {
      let lat = curPos?.lat, lng = curPos?.lng, acc = curPos?.acc ?? 0
      // 非追蹤中（無 live 位置）→ 一次性定位
      if (status !== 'tracking' || lat == null) {
        const pos = await new Promise<GeolocationPosition>((res, rej) =>
          navigator.geolocation.getCurrentPosition(res, rej, { enableHighAccuracy: true, timeout: 20000, maximumAge: 0 }))
        lat = pos.coords.latitude; lng = pos.coords.longitude; acc = pos.coords.accuracy ?? 0
      }
      // 追蹤中附上近期軌跡作為佐證
      const points = status === 'tracking'
        ? pointsRef.current.slice(-40).map((p) => ({ lat: p.lat, lng: p.lng, t: p.t, acc: p.acc }))
        : []
      const { result } = await checkpointApi.checkin(token, cp.id, { lat: lat!, lng: lng!, acc, points })
      setCpMsg(result.message)
      await fetchCheckpoints()
    } catch (e: any) {
      setCpMsg(e?.code === 1 ? '需要定位權限才能打卡' : (e?.message || '打卡失敗，請重試'))
    } finally { setCpBusy('') }
  }

  const cpDist = (cp: ActiveCheckpoint): number | null =>
    curPos ? haversineM({ lat: curPos.lat, lng: curPos.lng, t: 0, acc: 0 }, { lat: cp.lat, lng: cp.lng, t: 0, acc: 0 }) : null

  const distKm = distance / 1000
  const avgPace = distKm >= PACE_MIN_KM ? elapsed / distKm : 0 // 未達門檻先顯示 --:--，避免爆數字

  return (
   <GoogleAuthProvider>
    <PhoneFrame>
      {showLogin && <LoginModal onClose={() => setShowLogin(false)} />}
      <header style={{ padding: '14px 18px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid var(--line)' }}>
        {/* 跑步期間隱藏「返回/歷史」，避免誤離開而中斷；只能按「結束並上傳」正常結束 */}
        {status === 'tracking'
          ? <span style={{ color: 'var(--hunt)', fontSize: 13, fontWeight: 700 }}>● 跑步中</span>
          : <a href="/" style={{ color: 'var(--tx-dim)', fontSize: 14, textDecoration: 'none' }}>← 返回</a>}
        <strong style={{ fontSize: 16 }}>GPS 跑步追蹤</strong>
        {status === 'tracking'
          ? <span style={{ width: 32 }} />
          : <a href="/track/history" style={{ color: 'var(--fug)', fontSize: 13, textDecoration: 'none' }}>歷史</a>}
      </header>

      {/* 地圖（事件橫幅直接疊在地圖上方，任務結束才收起；地圖與數據不位移） */}
      <div style={{ position: 'relative' }}>
        <div id="gps-map" style={{ width: '100%', height: 280, background: 'var(--bg-2)' }} />
        {activeEvent && (
          <div style={{ position: 'absolute', left: 0, right: 0, top: 0, zIndex: 1000, pointerEvents: 'none' }}>
            <EventBanner active={activeEvent} moved={eventMoved} />
          </div>
        )}
        {!activeEvent && eventResult && (
          <div style={{ position: 'absolute', left: 0, right: 0, top: 0, zIndex: 1000 }}>
            <EventResultBanner result={eventResult} onClose={() => { setEventResult(null); fetchEventDefs() }} />
          </div>
        )}
      </div>

      <ScrollArea padding="16">
        {status === 'tracking' && curPos && (
          <div style={{ fontSize: 11.5, marginBottom: 10, color: curPos.acc > MAX_ACC ? 'var(--hunt)' : 'var(--tx-faint)' }}>
            📶 GPS 精度 ±{Math.round(curPos.acc)}m{curPos.acc > MAX_ACC ? '（訊號弱，移動可能未計入 → 請到空曠處）' : '（正常）'}
          </div>
        )}
        {warn && <div style={{ background: 'rgba(255,90,90,.12)', border: '1px solid rgba(255,90,90,.4)', color: '#ff8a8a', borderRadius: 10, padding: '10px 12px', fontSize: 13, marginBottom: 12, wordBreak: 'break-word' }}>⚠️ {warn}</div>}
        {err && <div style={{ color: 'var(--hunt)', fontSize: 13, marginBottom: 12 }}>{err}</div>}

        {/* 即時數據 */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
          <Big label="距離" value={distKm.toFixed(2)} unit="km" />
          <Big label="時間" value={fmtTime(elapsed)} unit="" />
          <Big label="平均配速" value={fmtPace(avgPace)} unit="/km" />
          <Big label="濾除跳點" value={String(anomalies)} unit="個" />
        </div>

        {/* 打卡點任務 */}
        {checkpoints.length > 0 && (
          <div style={{ marginTop: 16 }}>
            <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 6 }}>📍 打卡點任務</div>
            {cpMsg && <div style={{ fontSize: 12.5, color: 'var(--fug)', marginBottom: 8, wordBreak: 'break-word' }}>{cpMsg}</div>}
            {status !== 'tracking' && <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginBottom: 8 }}>建議按「開始跑步」邊跑邊打卡（有軌跡佐證，免審核）。</div>}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {checkpoints.map((cp) => {
                const d = cpDist(cp)
                const inRange = d != null && d <= cp.radius_m
                const busy = cpBusy === cp.id
                const blocked = busy || (curPos != null && !inRange)
                return (
                  <div key={cp.id} style={{ background: 'var(--bg-2)', borderRadius: 10, padding: '10px 12px', display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 10 }}>
                    <div style={{ minWidth: 0 }}>
                      <div style={{ fontSize: 13.5, fontWeight: 700, color: 'var(--tx)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{cp.title || '打卡點'}</div>
                      <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                        {cp.race_title}{cp.task_title ? ` · ${cp.task_title}` : ''}
                        {d != null && !cp.checked && <> · {d < 1000 ? `還有 ${Math.round(d)}m` : `${(d / 1000).toFixed(1)}km`}</>}
                      </div>
                    </div>
                    {cp.checked ? (
                      <span style={{ color: 'var(--fug)', fontWeight: 700, fontSize: 13, flexShrink: 0 }}>✓ 已打卡</span>
                    ) : cp.pending ? (
                      <span style={{ color: 'var(--gold)', fontSize: 12.5, flexShrink: 0 }}>審核中</span>
                    ) : (
                      <button onClick={() => doCheckin(cp)} disabled={blocked}
                        style={{ flexShrink: 0, background: 'var(--fug)', color: '#05140e', fontWeight: 800, border: 'none', borderRadius: 9, padding: '8px 14px', fontSize: 13, cursor: blocked ? 'default' : 'pointer', opacity: blocked ? 0.45 : 1 }}>
                        {busy ? '打卡中…' : curPos != null && !inRange ? '未到範圍' : '打卡'}
                      </button>
                    )}
                  </div>
                )
              })}
            </div>
          </div>
        )}

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
          <div style={{ marginTop: 16, background: 'var(--bg-1)', border: `1px solid ${result.flagged ? 'rgba(255,90,90,.4)' : 'var(--line-2)'}`, borderRadius: 12, padding: 14, wordBreak: 'break-word' }}>
            <div style={{ fontWeight: 800, marginBottom: 8 }}>
              {result.too_short ? 'ℹ️ 移動距離不足，無法計算' : result.flagged ? '⚠️ 數據異常，已標記待審' : '✓ 已記錄'}
            </div>
            <div style={{ fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.8 }}>
              {result.too_short ? (
                <span>本次移動距離過短（{result.distance_km.toFixed(2)} km），未達可計算配速的最小距離，故不記錄。請實際移動一段距離後再試。</span>
              ) : (
                <>
                  距離 {result.distance_km.toFixed(2)} km · 時間 {fmtTime(result.duration_s)} · 平均配速 {fmtPace(result.avg_pace_s)}/km<br />
                  {result.flagged
                    ? <span style={{ color: '#ff8a8a' }}>原因：{result.flag_reason}（不發 EXP，待後台審核）</span>
                    : <span style={{ color: 'var(--fug)' }}>已進活動記錄{result.exp_awarded ? '，里程 EXP 將於數秒後發放' : ''}</span>}
                </>
              )}
            </div>
          </div>
        )}
      </ScrollArea>

      {/* 操作 */}
      <div style={{ padding: 16, borderTop: '1px solid var(--line)', position: 'sticky', bottom: 0, background: 'var(--bg)' }}>
        {status === 'idle' && (
          user
            ? <button onClick={start} style={btn}>▶ 開始跑步</button>
            : <button onClick={() => setShowLogin(true)} style={btn}>請先登入</button>
        )}
        {status === 'tracking' && <button onClick={finish} style={{ ...btn, background: 'var(--hunt)', color: '#fff' }}>■ 結束並上傳</button>}
        {status === 'done' && <button onClick={() => { setStatus('idle'); setElapsed(0); setDistance(0); setSplits([]); setAnomalies(0) }} style={{ ...btn, background: 'var(--bg-2)', color: 'var(--tx)' }}>再跑一次</button>}
        {status === 'tracking' && <div style={{ textAlign: 'center', fontSize: 11, color: 'var(--tx-faint)', marginTop: 8 }}>追蹤中請保持本頁在前景、螢幕勿關（背景追蹤瀏覽器不支援）{uploading ? ' · 上傳中…' : ''}</div>}
      </div>
    </PhoneFrame>
   </GoogleAuthProvider>
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
