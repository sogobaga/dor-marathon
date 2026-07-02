'use client'

import { useEffect, useRef, useState, useCallback } from 'react'
import { activitiesApi, checkpointApi, eventApi, eventRaceApi, createRaceSocket, type GpsPoint, type GpsRunResult, type ActiveCheckpoint, type EventDef, type RaceEventInvite, type CompleteEvidence } from '@/lib/api'
import { getUserToken, withUserAuth, useUser } from '@/lib/userAuth'
import { loadLeaflet } from '@/lib/leaflet'
import { unlockAudio, playEventAlert, playEventComplete, vibrate, setMuted as sfxSetMuted, isMuted } from '@/lib/sfx'
import { loadEffectAssets } from '@/lib/effects'
import GoogleAuthProvider from '@/components/GoogleAuthProvider'
import { LoginModal } from '@/components/UserAuthBar'
import PhoneFrame from '@/components/PhoneFrame'
import ScrollArea from '@/components/ScrollArea'
import { EventBanner, EventResultBanner, pickTimeImage, isInteractionType, type ActiveEvent, type EventResult } from '@/components/EventTaskModal'
import { EventInteraction } from '@/components/EventInteraction'

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
  // 賽事多人連動事件（Phase B）
  const [raceInvite, setRaceInvite] = useState<RaceEventInvite | null>(null)
  const [inviteNow, setInviteNow] = useState(0) // 驅動邀請倒數重繪
  const [isLandscape, setIsLandscape] = useState(false) // 橫向時顯示「轉回直立」提示
  const [fxAssets, setFxAssets] = useState<Record<string, string>>({}) // 效果覆寫（正式圖片/音檔）
  const [confirmEnd, setConfirmEnd] = useState(false) // 事件進行中按「結束」→ 先跳確認（損失規避）
  const [muted, setMuted] = useState(false) // 事件音效靜音（震動不受影響）

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
  const completingRef = useRef(false) // 完成/失敗結算中：避免 evalTick 在空窗期又 arm 新事件蓋掉結果
  eventDefsRef.current = eventDefs
  // Phase B 用
  const wsRef = useRef<WebSocket | null>(null)
  const raceIdRef = useRef('') // 綁定的賽事（供回報里程/接收邀請）
  const lastTriggerRef = useRef(0) // 里程回報節流
  const lastClaimRef = useRef(0) // 認領後台手動觸發事件的節流
  const raceInviteRef = useRef<RaceEventInvite | null>(null)
  raceInviteRef.current = raceInvite

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
      // 被全域閘門擋下（冷卻中）：退回並設冷卻，避免每秒重試又閃橫幅/重複提示
      if (!occ.id) { if (activeEventRef.current === ae) { activeEventRef.current = null; setActiveEvent(null) }; lastEventEndRef.current = Date.now(); return }
      // 若在 createOccurrence 飛行期間使用者已放棄/結束（ref 被清）→ 補送 fail，避免留下 active 孤兒列
      if (activeEventRef.current !== ae) { eventApi.fail(token, occ.id).catch(() => {}); return }
      const armed = { ...ae, occId: occ.id }
      activeEventRef.current = armed; setActiveEvent(armed)
      playEventAlert(); vibrate([200, 100, 200]) // 事件來了：音效 + 震動
    } catch { if (activeEventRef.current === ae) { activeEventRef.current = null; setActiveEvent(null) } }
  }
  async function completeEvent(ae: ActiveEvent, moved: number, windowS: number, extra: Partial<CompleteEvidence> = {}) {
    activeEventRef.current = null; setActiveEvent(null); lastEventEndRef.current = Date.now()
    completingRef.current = true
    const inter = isInteractionType(ae.def.completion_type)
    // 樂觀顯示：非互動直接「完成」；互動先「結算中」（星等要等後端算完成度）
    setEventResult({ status: 'completed', def: ae.def, reward_exp: 0, reward_dp: 0, pending: inter })
    playEventComplete(); vibrate([90, 50, 90]) // 事件完成：成功音 + 短震動
    const token = getUserToken()
    const body: CompleteEvidence = { moved_m: moved, window_s: windowS, ...extra }
    try {
      const res = token
        ? (ae.raceInstanceId
          ? await eventRaceApi.complete(token, ae.raceInstanceId, body)
          : await eventApi.complete(token, ae.occId, body))
        : { completed: false }
      setEventResult(res.completed
        ? { status: 'completed', def: ae.def, reward_exp: res.reward_exp ?? 0, reward_dp: res.reward_dp ?? 0, stars: (res as any).stars }
        : { status: 'failed', def: ae.def, reward_exp: 0, reward_dp: 0 })
    } catch { setEventResult({ status: 'failed', def: ae.def, reward_exp: 0, reward_dp: 0 }) }
    finally { completingRef.current = false }
  }
  function failEvent(ae: ActiveEvent) {
    activeEventRef.current = null; setActiveEvent(null); lastEventEndRef.current = Date.now()
    const token = getUserToken()
    if (token) {
      if (ae.raceInstanceId) eventRaceApi.fail(token, ae.raceInstanceId).catch(() => {})
      else if (ae.occId) eventApi.fail(token, ae.occId).catch(() => {})
    }
    setEventResult({ status: 'failed', def: ae.def, reward_exp: 0, reward_dp: 0 })
  }
  // 互動小遊戲時間到 → 用收集到的 evidence 送後端分級發獎
  function handleInteractionDone(ev: { taps: number; held_ms: number; swipe_px: number; swipes: number }) {
    const ae = activeEventRef.current
    if (!ae) return
    const windowS = Math.max(0, (ae.deadline - ae.readyUntil) / 1000)
    completeEvent(ae, 0, windowS, { taps: ev.taps, held_ms: ev.held_ms, swipe_px: ev.swipe_px, swipes: ev.swipes })
  }
  // Phase B：連 WS（綁第一場「進行中且已報名」賽事）＋ 監聽多人事件邀請
  async function connectRaceWS() {
    const token = getUserToken()
    if (!token || wsRef.current) return
    try {
      const { races } = await eventRaceApi.context(token)
      if (!races.length) return
      raceIdRef.current = races[0].id
      const ws = createRaceSocket(raceIdRef.current, token)
      wsRef.current = ws
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data)
          if (msg.type !== 'event_race_invite') return
          const p = msg.payload as RaceEventInvite
          if (!user?.id || !p.target_user_ids?.includes(user.id)) return
          if (activeEventRef.current || raceInviteRef.current) return // 一次一任務
          if (Date.now() > p.join_deadline) return
          setRaceInvite(p)
          playEventAlert(); vibrate([200, 100, 200]) // 多人事件邀請來了：音效 + 震動
        } catch { /* ignore */ }
      }
      ws.onclose = () => { if (wsRef.current === ws) wsRef.current = null }
    } catch { /* ignore */ }
  }
  // 加入多人事件 → 轉為一般 activeEvent，交給既有引擎評估完成
  async function joinRace(inv: RaceEventInvite) {
    const token = getUserToken(); if (!token) return
    setRaceInvite(null)
    try {
      const res = await eventRaceApi.join(token, inv.instance_id)
      if (!res.joined) { setCpMsg(res.message || '無法加入此事件'); return }
      const now = Date.now()
      const def: EventDef = {
        name: res.name || inv.name, description: '', enabled: true, weight: 100, cooldown_sec: 0,
        trigger_type: '', trigger_params: {}, completion_type: res.completion_type || inv.completion_type,
        completion_params: res.completion_params || inv.completion_params, message: res.message || inv.message,
        image_url: inv.image_url, image_day_url: inv.image_day_url, image_dusk_url: inv.image_dusk_url, image_night_url: inv.image_night_url,
        reward_exp: res.reward_exp ?? inv.reward_exp, reward_dp: res.reward_dp ?? inv.reward_dp,
      }
      const deadline = res.deadline || (now + (def.completion_params.limit_s || 180) * 1000)
      const ae: ActiveEvent = { def, occId: '', raceInstanceId: inv.instance_id, triggerD: distRef.current, triggerT: now, readyUntil: now, deadline }
      activeEventRef.current = ae; setActiveEvent(ae); setEventMoved(0); setEventResult(null)
    } catch { setCpMsg('加入失敗，請重試') }
  }

  // 測試：認領後台手動觸發的事件，直接 arm（沿用一般 activeEvent 引擎）
  async function claimManualEvent() {
    const token = getUserToken(); if (!token) return
    try {
      const res = await eventApi.claimManual(token)
      if (!res.armed || !res.def || activeEventRef.current) return
      const def = res.def
      const triggerT = Date.now()
      const limitS = def.completion_params.limit_s || 60
      const readyUntil = triggerT + EVENT_GRACE_MS
      const ae: ActiveEvent = { def, occId: res.occ_id || '', triggerD: distRef.current, triggerT, readyUntil, deadline: readyUntil + limitS * 1000 }
      activeEventRef.current = ae; setActiveEvent(ae); setEventMoved(0); setEventResult(null)
      playEventAlert(); vibrate([200, 100, 200]) // 事件來了：音效 + 震動
    } catch { /* ignore */ }
  }

  // 配速類完成用：由每秒累積距離樣本推算指標
  function distAt(t: number): number {
    const s = distSamplesRef.current
    if (!s.length) return distRef.current
    let d = s[0].d
    for (const p of s) { if (p.t <= t) d = p.d; else break }
    return d
  }
  function bestBurst(fromT: number, toT: number, burstMs: number): number {
    const s = distSamplesRef.current.filter((p) => p.t >= fromT && p.t <= toT)
    let best = 0
    for (let i = 0; i < s.length; i++) {
      const endT = s[i].t + burstMs
      let endD = s[i].d
      for (let j = i; j < s.length && s[j].t <= endT; j++) endD = s[j].d
      best = Math.max(best, endD - s[i].d)
    }
    return best
  }
  function minInterval(fromT: number, toT: number, checkMs: number): number {
    if (toT - fromT < checkMs) return distAt(toT) - distAt(fromT)
    let min = Infinity
    for (let a = fromT; a + checkMs <= toT + 1; a += checkMs) min = Math.min(min, distAt(a + checkMs) - distAt(a))
    return min === Infinity ? 0 : min
  }

  function evalTick() {
    if (statusRef.current !== 'tracking') return
    const now = Date.now()
    distSamplesRef.current.push({ t: now, d: distRef.current })
    if (distSamplesRef.current.length > 1600) distSamplesRef.current.splice(0, 400) // 上限 ~25 分鐘
    // 測試：跑步中、無進行中事件時輪詢認領後台手動觸發（每 5 秒；結算中不認領）
    if (!activeEventRef.current && !completingRef.current && now - lastClaimRef.current > 5000) {
      lastClaimRef.current = now
      claimManualEvent()
    }
    // Phase B：節流回報里程給後端（由後端依定義門檻/冷卻決定是否觸發多人事件）
    if (raceIdRef.current && distRef.current > 0 && now - lastTriggerRef.current > 20000) {
      lastTriggerRef.current = now
      const token = getUserToken()
      if (token) eventRaceApi.trigger(token, { race_id: raceIdRef.current, moved_m: distRef.current, elapsed_s: Math.floor((now - startRef.current) / 1000) }).catch(() => {})
    }
    // 邀請倒數重繪＋逾時自動關閉
    if (raceInviteRef.current) {
      if (now > raceInviteRef.current.join_deadline) setRaceInvite(null)
      else setInviteNow(now)
    }
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
      } else if (ae.def.completion_type === 'sprint') {
        // 衝刺：任一 burst_s 區間移動達標即完成
        const maxSeg = bestBurst(ae.readyUntil, now, (cp.burst_s ?? 5) * 1000)
        setEventMoved(maxSeg)
        if (maxSeg >= (cp.burst_m ?? 0)) completeEvent(ae, moved, windowS, { max_seg_m: maxSeg })
        else if (now > ae.deadline) failEvent(ae)
      } else if (ae.def.completion_type === 'hold_pace') {
        // 維持配速：撐滿時間 + 每個 check_s 區間都達標（到時間才判定）
        const checkMs = (cp.check_s ?? 10) * 1000
        setEventMoved(distAt(now) - distAt(Math.max(ae.readyUntil, now - checkMs)))
        if (now >= ae.deadline) {
          const minSeg = minInterval(ae.readyUntil, ae.deadline, checkMs)
          if (minSeg >= (cp.min_m ?? 0)) completeEvent(ae, moved, windowS, { min_seg_m: minSeg })
          else failEvent(ae)
        }
      } else if (ae.def.completion_type === 'negative_split') {
        // 後段加速：後半移動 ≥ 前半 × 比例（到時間才判定）
        setEventMoved(moved)
        if (now >= ae.deadline) {
          const mid = ae.readyUntil + (ae.deadline - ae.readyUntil) / 2
          const firstHalf = distAt(mid) - distAt(ae.readyUntil)
          const secondHalf = distAt(ae.deadline) - distAt(mid)
          if (firstHalf > 5 && secondHalf >= firstHalf * ((cp.ratio_pct ?? 100) / 100)) completeEvent(ae, moved, windowS, { first_half_m: firstHalf, second_half_m: secondHalf })
          else failEvent(ae)
        }
      }
      return
    }
    // 無進行中事件 → 依冷卻 + 觸發條件挑選（結算中不 arm，避免蓋掉剛完成的結果通知）
    if (completingRef.current) return
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
    // Phase B 重置
    setRaceInvite(null); raceIdRef.current = ''; lastTriggerRef.current = 0; lastClaimRef.current = 0
    startRef.current = Date.now()
    setStatus('tracking')
    unlockAudio() // 在使用者手勢內解鎖音訊（iOS 必須）
    connectRaceWS() // 連 WS 監聽多人事件（不 await；失敗不影響跑步）
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
    // 盡力鎖直屏（Android/PWA 全螢幕有效；iOS Safari 不支援 → 靠下方「轉回直立」提示保底）
    try { (screen.orientation as any)?.lock?.('portrait').catch(() => {}) } catch { /* 不支援就忽略 */ }
  }

  const cleanup = useCallback(() => {
    if (watchRef.current != null) navigator.geolocation.clearWatch(watchRef.current)
    watchRef.current = null
    clearInterval(timerRef.current)
    clearInterval(evalTimerRef.current)
    try { wsRef.current?.close() } catch { /* ignore */ }
    wsRef.current = null; raceIdRef.current = ''
    try { wakeRef.current?.release() } catch { /* ignore */ }
    wakeRef.current = null
    try { (screen.orientation as any)?.unlock?.() } catch { /* ignore */ }
  }, [])

  // 按「結束並上傳」：事件任務進行中 → 先跳確認（損失規避）；否則直接結束
  function requestFinish() {
    if (activeEventRef.current) { setConfirmEnd(true); return }
    finish()
  }
  // 確認放棄事件並結束：伺服器端標記事件失敗（釋放 occurrence/閘門），再結束上傳
  function endWithForfeit() {
    setConfirmEnd(false)
    const ae = activeEventRef.current
    if (ae) {
      activeEventRef.current = null; setActiveEvent(null); setEventMoved(0)
      lastEventEndRef.current = Date.now()
      const token = getUserToken()
      if (token) {
        if (ae.raceInstanceId) eventRaceApi.fail(token, ae.raceInstanceId).catch(() => {})
        else if (ae.occId) eventApi.fail(token, ae.occId).catch(() => {})
      }
    }
    finish()
  }

  // 確認視窗開啟時若事件已結束（完成/失敗）→ 強制關閉，讓「事件完成/結果」通知顯示
  useEffect(() => { if (confirmEnd && !activeEvent) setConfirmEnd(false) }, [confirmEnd, activeEvent])

  useEffect(() => { setMuted(isMuted()) }, [])
  // 載入效果覆寫（正式圖片/音檔）：圖片給互動層、音效交給 sfx 解碼
  useEffect(() => { const t = getUserToken(); if (t) loadEffectAssets(t).then(setFxAssets) }, [user?.id])
  function toggleMute() { const next = !isMuted(); sfxSetMuted(next); setMuted(next); if (!next) unlockAudio() }

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

  // 追蹤螢幕方向：橫向時顯示「轉回直立」提示（跨平台保底；真正鎖定見 start() 的 orientation.lock）
  // 用 resize + orientationchange（iOS Safari 對 matchMedia 的 change 事件不可靠），並以視窗長寬保底判斷。
  useEffect(() => {
    if (typeof window === 'undefined') return
    let t: any
    const isLand = () => (window.matchMedia?.('(orientation: landscape)').matches) ?? (window.innerWidth > window.innerHeight)
    const check = () => {
      clearTimeout(t)
      // 橫向需持續 ~0.5 秒才顯示提示（避免晃動瞬間翻轉狂閃）；轉回直立立即收起。
      // timeout 內再判一次，閃避 iOS 剛旋轉時回報舊尺寸的問題。
      if (isLand()) t = setTimeout(() => { if (isLand()) setIsLandscape(true) }, 500)
      else setIsLandscape(false)
    }
    check()
    const mq = window.matchMedia?.('(orientation: landscape)')
    window.addEventListener('resize', check)
    window.addEventListener('orientationchange', check)
    mq?.addEventListener?.('change', check)
    return () => {
      clearTimeout(t)
      window.removeEventListener('resize', check)
      window.removeEventListener('orientationchange', check)
      mq?.removeEventListener?.('change', check)
    }
  }, [])

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
      {isLandscape && (
        <div style={{ position: 'fixed', inset: 0, zIndex: 3000, background: 'var(--bg-1, #0b0e13)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 14, padding: 24, textAlign: 'center' }}>
          <div style={{ fontSize: 46 }}>📱↻</div>
          <div style={{ fontSize: 18, fontWeight: 800, color: 'var(--tx)' }}>請將手機轉回直立</div>
          <div style={{ fontSize: 13.5, color: 'var(--tx-dim)', lineHeight: 1.7, maxWidth: 300 }}>
            此頁僅支援直式畫面。{status === 'tracking' && <>跑步中你的移動<strong style={{ color: 'var(--fug)' }}>仍在背景持續記錄</strong>，</>}轉回直立即可繼續。<br />
            建議把手機「自動旋轉」關閉，或將本站「加入主畫面」以固定直屏。
          </div>
        </div>
      )}
      {status === 'tracking' && activeEvent && isInteractionType(activeEvent.def.completion_type) && (
        <EventInteraction active={activeEvent} onDone={handleInteractionDone} paused={isLandscape} assets={fxAssets} />
      )}
      {confirmEnd && activeEvent && (() => {
        const ev = activeEvent
        const remain = Math.max(0, Math.ceil((ev.deadline - Date.now()) / 1000))
        const rExp = ev.def.reward_exp ?? 0
        const rDp = ev.def.reward_dp ?? 0
        return (
          <div style={{ position: 'fixed', inset: 0, zIndex: 2500, background: 'rgba(0,0,0,.66)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 24 }}>
            <div style={{ background: 'var(--bg-1, #0b0e13)', border: '1px solid var(--line-2)', borderRadius: 16, padding: '20px 18px', maxWidth: 340, width: '100%', boxShadow: '0 12px 40px rgba(0,0,0,.6)' }}>
              <div style={{ fontSize: 17, fontWeight: 900, color: 'var(--tx)', marginBottom: 8 }}>⚠️ 事件任務進行中</div>
              <div style={{ fontSize: 13.5, color: 'var(--tx-dim)', lineHeight: 1.7 }}>
                你有一個事件任務還在進行（剩 <strong style={{ color: 'var(--gold)' }}>{remain}s</strong>）。<br />
                現在結束跑步的話，<strong style={{ color: 'var(--hunt)' }}>事件任務也會一起結束，無法取得任務獎勵</strong>
                {(rExp > 0 || rDp > 0) && <>（<span style={{ color: 'var(--gold)', fontWeight: 700 }}>{rExp > 0 ? `+${rExp} EXP` : ''}{rExp > 0 && rDp > 0 ? '、' : ''}{rDp > 0 ? `🪙+${rDp}` : ''}</span>）</>}
                。確定要結束嗎？
              </div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 16 }}>
                <button onClick={() => setConfirmEnd(false)} style={{ background: 'var(--fug)', color: '#05140e', fontWeight: 800, border: 'none', borderRadius: 10, padding: '11px', fontSize: 14.5, cursor: 'pointer' }}>再撐一下、完成任務</button>
                <button onClick={endWithForfeit} style={{ background: 'transparent', color: 'var(--hunt)', fontWeight: 700, border: '1px solid rgba(255,75,92,.5)', borderRadius: 10, padding: '10px', fontSize: 13.5, cursor: 'pointer' }}>放棄獎勵、仍要結束</button>
              </div>
            </div>
          </div>
        )
      })()}
      <header style={{ padding: '14px 18px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid var(--line)' }}>
        {/* 跑步期間隱藏「返回/歷史」，避免誤離開而中斷；只能按「結束並上傳」正常結束 */}
        {status === 'tracking'
          ? <span style={{ color: 'var(--hunt)', fontSize: 13, fontWeight: 700 }}>● 跑步中</span>
          : <a href="/" style={{ color: 'var(--tx-dim)', fontSize: 14, textDecoration: 'none' }}>← 返回</a>}
        <strong style={{ fontSize: 16 }}>GPS 跑步追蹤</strong>
        <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
          <button onClick={toggleMute} title={muted ? '事件音效：關' : '事件音效：開'} aria-label="事件音效開關" style={{ background: 'none', border: 'none', cursor: 'pointer', fontSize: 16, lineHeight: 1, padding: 0, color: 'var(--tx-dim)' }}>{muted ? '🔇' : '🔊'}</button>
          {status !== 'tracking' && <a href="/track/history" style={{ color: 'var(--fug)', fontSize: 13, textDecoration: 'none' }}>歷史</a>}
        </div>
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
        {!activeEvent && raceInvite && (() => {
          const remain = Math.max(0, Math.ceil((raceInvite.join_deadline - (inviteNow || Date.now())) / 1000))
          return (
            <div style={{ position: 'absolute', left: 0, right: 0, top: 0, zIndex: 1001, margin: '10px 12px 0', background: 'rgba(9,12,16,.96)', border: '1px solid rgba(255,194,75,.6)', borderRadius: 12, padding: '12px 14px', boxShadow: '0 6px 24px rgba(0,0,0,.55)' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 10 }}>
                <span style={{ fontSize: 11, letterSpacing: '.2em', color: 'var(--gold)', fontWeight: 800 }}>⚡ 多人事件邀請</span>
                <span style={{ fontSize: 18, fontWeight: 900, color: remain <= 10 ? 'var(--hunt)' : 'var(--gold)', fontVariantNumeric: 'tabular-nums' }}>{remain}s</span>
              </div>
              {pickTimeImage(raceInvite) && <img src={pickTimeImage(raceInvite)} alt="" style={{ width: '100%', height: 110, objectFit: 'cover', borderRadius: 8, margin: '8px 0 2px', display: 'block' }} />}
              <div style={{ fontSize: 13.5, fontWeight: 700, color: 'var(--tx)', marginTop: 4, lineHeight: 1.5 }}>
                <span style={{ color: 'var(--fug)' }}>{raceInvite.initiator_name}</span> 發起：{raceInvite.message || raceInvite.name}
              </div>
              <div style={{ display: 'flex', gap: 12, alignItems: 'center', marginTop: 6 }}>
                {raceInvite.reward_exp > 0 && <span style={{ fontSize: 13, fontWeight: 900, color: 'var(--gold)' }}>+{raceInvite.reward_exp} EXP</span>}
                {raceInvite.reward_dp > 0 && <span style={{ fontSize: 13, fontWeight: 900, color: '#FFD24D' }}>🪙 +{raceInvite.reward_dp}</span>}
              </div>
              <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
                <button onClick={() => joinRace(raceInvite)} style={{ flex: 1, background: 'var(--fug)', color: '#05140e', fontWeight: 800, border: 'none', borderRadius: 9, padding: '9px', fontSize: 14, cursor: 'pointer' }}>加入一起跑</button>
                <button onClick={() => setRaceInvite(null)} style={{ background: 'transparent', color: 'var(--tx-faint)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '9px 14px', fontSize: 13, cursor: 'pointer' }}>略過</button>
              </div>
            </div>
          )
        })()}
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
        {status === 'tracking' && <button onClick={requestFinish} style={{ ...btn, background: 'var(--hunt)', color: '#fff' }}>■ 結束並上傳</button>}
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
