'use client'

import { useEffect, useRef, useState, useCallback } from 'react'
import { activitiesApi, checkpointApi, eventApi, eventRaceApi, mileageExpApi, personalTasksApi, exploreApi, createRaceSocket, type GpsPoint, type GpsRunResult, type ActiveCheckpoint, type EventDef, type RaceEventInvite, type CompleteEvidence, type MileageConfig, type PanelCard, type ExploreBoss } from '@/lib/api'
import { getUserToken, withUserAuth, useUser } from '@/lib/userAuth'
import WorkoutHud from '@/components/WorkoutHud'
import BossChallengePanel from '@/components/BossChallengePanel'
import BossRankingPanel from '@/components/BossRankingPanel'
import CardUnlockCelebration from '@/components/CardUnlockCelebration'
import TrackTaskPanel from '@/components/TrackTaskPanel'
import { expandSegments, paceInBand, type WoStep } from '@/lib/workout'
import { loadLeaflet } from '@/lib/leaflet'
import { unlockAudio, playEventAlarm, playEventComplete, vibrate, setMuted as sfxSetMuted, isMuted } from '@/lib/sfx'
import { loadEffectAssets } from '@/lib/effects'
import GoogleAuthProvider from '@/components/GoogleAuthProvider'
import { LoginModal } from '@/components/UserAuthBar'
import PhoneFrame from '@/components/PhoneFrame'
import { EventBanner, EventResultBanner, EventTriggerFlash, Countdown321, EventOfferPanel, pickTimeImage, isInteractionType, type ActiveEvent, type EventResult } from '@/components/EventTaskModal'
import { EventInteraction } from '@/components/EventInteraction'
import { useIsPhone } from '@/lib/useIsMobile'
import { useIsLandscape } from '@/lib/useIsLandscape'
import { useDraggableSheet } from '@/lib/useDraggableSheet'

/* eslint-disable @typescript-eslint/no-explicit-any */

const LS_KEY = 'dor_gps_run'
const MAX_ACC = 65 // 精度差於此（公尺）的點不採計距離（城市/大樓旁訊號較差，放寬以免整趟記不到）
const MAX_SPEED = 1000 / 120 // 8.33 m/s（2:00/km）人類極限上限
const JITTER_MIN = 6 // 公尺：距上一個採納點移動不足此值視為原地抖動，不計距離
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
  const [errFade, setErrFade] = useState(false) // 提示訊息淡出中
  const [vehicleWarn, setVehicleWarn] = useState(false) // 即時偵測到疑似搭車速度
  const [recover, setRecover] = useState<{ start: number; points: GpsPoint[]; km: number; mins: number } | null>(null) // 上次未上傳的跑步（可恢復上傳）
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
  // 兩個 hook 必須各自「無條件」呼叫再合併——不可寫成 useIsPhone() && useIsLandscape()（&& 短路會讓某些 render 少呼叫一個 hook → 崩潰）
  const isPhone = useIsPhone()
  const inLandscape = useIsLandscape()
  const isLandscape = isPhone && inLandscape // 手機橫向：暫停互動小遊戲（「請轉直」提示由全域 LandscapeNotice 顯示）
  const [fxAssets, setFxAssets] = useState<Record<string, string>>({}) // 效果覆寫（正式圖片/音檔）
  const [confirmEnd, setConfirmEnd] = useState(false) // 事件進行中按「結束」→ 先跳確認（損失規避）
  const [muted, setMuted] = useState(false) // 事件音效靜音（震動不受影響）
  const [showFlash, setShowFlash] = useState(false) // Step1：全螢幕「事件觸發」紅閃警報（Phase A/B 共用）
  // COROS 式 UX：可上下拖曳的資訊面板疊在放大的地圖上（配色與顯示的資訊都不變，只改操作體驗）
  const sheet = useDraggableSheet('half')
  const followRef = useRef(true) // 地圖是否自動跟隨目前位置；使用者拖曳/縮放地圖後暫停，按「回到目前位置」恢復
  const [following, setFollowing] = useState(true) // 驅動「回到目前位置」按鈕顯示
  const [mileageCfg, setMileageCfg] = useState<MileageConfig | null>(null) // 里程獎勵設定（進度條/預覽）
  // 個人任務「結構化課表」執行（挑戰後帶到本頁跑）
  const [workout, setWorkout] = useState<{ taskId: string; title: string; steps: WoStep[]; kind: 'personal' | 'explore'; cardUrl?: string } | null>(null)
  const [exploreCps, setExploreCps] = useState<ExploreBoss[]>([]) // 城市探索打卡點（含座標）
  const [bossPanel, setBossPanel] = useState<{ boss: ExploreBoss; phase: 'intro' | 'start'; dpCost: number } | null>(null) // 打卡後跳出的關主挑戰面板
  const [rankingBoss, setRankingBoss] = useState<{ id: string; name: string } | null>(null) // 挑戰者成績排行覆蓋層
  const [celebrateCard, setCelebrateCard] = useState<{ bossId: string; name: string; cardUrl?: string } | null>(null) // 3★取卡恭喜彈窗
  const [exploreBusy, setExploreBusy] = useState(false)
  const [focusBoss, setFocusBoss] = useState<string | null>(null) // 「前往打卡」帶來的目標關主 id → 地圖定位到該打卡點
  const focusDoneRef = useRef(false)
  const [woPhase, setWoPhase] = useState<'idle' | 'countdown' | 'running' | 'done'>('idle')
  const [woStepIdx, setWoStepIdx] = useState(0)
  const [woHits, setWoHits] = useState<Record<number, boolean>>({}) // work 段 index → 是否達配速
  const [woResult, setWoResult] = useState<{ stars: number; reward_exp: number; reward_dp: number; flagged?: boolean; card_obtained?: boolean } | null>(null)
  const [, setWoNow] = useState(0) // 驅動 HUD 每 0.5s 重繪
  const woStepIdxRef = useRef(0)
  const woStepStartRef = useRef<{ dist: number; time: number }>({ dist: 0, time: 0 }) // 目前分段起點（距離 m / 時間 ms）
  const woResultsRef = useRef<{ inBand: number; total: number; detail: any[] }>({ inBand: 0, total: 0, detail: [] })
  const woActiveRef = useRef(false) // 課表執行中：跑步引擎暫停隨機事件
  const vehicleLikeRef = useRef(false) // 即時偵測：近 45 秒配速快於人體極限（疑似搭車）
  const [panel, setPanel] = useState<{ cards: PanelCard[]; active_card: PanelCard | null } | null>(null) // 任務面板（各階段可挑戰課表）
  const [panelBusy, setPanelBusy] = useState('') // 面板挑戰處理中的 task_id

  const pointsRef = useRef<GpsPoint[]>([])
  const distRef = useRef(0)      // 有效距離（排除超速段）：顯示/里程/課表進度用
  const rawDistRef = useRef(0)   // 原始距離（含超速夾限）：僅供疑似搭車偵測，避免排除有效距離後偵測失效
  const splitMarkRef = useRef<number[]>([]) // 每跨整公里時的 elapsed 秒
  const startRef = useRef(0)
  const watchRef = useRef<number | null>(null)
  const warmWatchRef = useRef<number | null>(null) // 進頁面時的 GPS 預熱偵測（顯示精度/定位地圖，不記錄）
  const wakeRef = useRef<any>(null)
  const timerRef = useRef<any>(null)
  const pingTimerRef = useRef<any>(null) // 跑步中心跳（後台「目前在跑名單」）
  const mapRef = useRef<any>(null)
  const lineRef = useRef<any>(null)
  const markRef = useRef<any>(null)
  const cpLayerRef = useRef<any>(null) // 地圖上的打卡點圖層
  const warnTimer = useRef<any>(null)
  const errTimerRef = useRef<any>(null) // 「軌跡太短」等暫時訊息的自動淡出計時
  const statusRef = useRef(status)
  statusRef.current = status
  const lastAccRef = useRef<GpsPoint | null>(null) // 上一個「採納」的點（過濾原地抖動用）
  // 事件引擎用
  const distSamplesRef = useRef<{ t: number; d: number }[]>([]) // {時間ms, 累積距離m}
  const eventDefsRef = useRef<EventDef[]>([])
  const activeEventRef = useRef<ActiveEvent | null>(null)
  const lastEventEndRef = useRef(0) // 上次事件結束時間（per-def cooldown 用）
  const waitMinRef = useRef(300) // 事件隨機等待區間（秒），由系統設定帶入
  const waitMaxRef = useRef(900)
  const firstWaitRef = useRef(0) // 本趟「第一個事件」的等待秒數（前幾趟較短，伺服器依帳號帶入）；0=用正常區間
  const nextEventAtRef = useRef(0) // 下一個事件最早可觸發的時間（開始跑步/每次事件結束後隨機重取）
  const armSeqRef = useRef(0) // 事件流水序號：辨識非同步 createOccurrence 回來時該事件是否仍是「當前這一個」
  const armIdRef = useRef(0) // 當前進行中事件的流水序號（0=無）
  const armingRef = useRef(false) // armEvent 建立 occurrence 進行中（尚未提交事件）：擋 evalTick 重複 arm / 邀請插隊
  const evalTimerRef = useRef<any>(null)
  const completingRef = useRef(false) // 完成/失敗結算中：避免 evalTick 在空窗期又 arm 新事件蓋掉結果
  eventDefsRef.current = eventDefs
  // Phase B 用
  const wssRef = useRef<WebSocket[]>([]) // 綁定所有進行中賽事的 WS（多人事件邀請）
  const raceIdsRef = useRef<string[]>([]) // 進行中且已報名的賽事 id（供回報里程/接收邀請）
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
    // 使用者手動拖曳/縮放地圖 → 暫停自動跟隨（否則每次 GPS 更新都會把畫面拉回目前位置，無法看前方路線）
    map.on('dragstart zoomstart', () => { if (followRef.current) { followRef.current = false; setFollowing(false) } })
    mapRef.current = map
    setMapReady(true)
  }, [])

  const onPos = useCallback((pos: GeolocationPosition) => {
    const p: GpsPoint = { lat: pos.coords.latitude, lng: pos.coords.longitude, t: pos.timestamp, acc: pos.coords.accuracy ?? 0 }
    setCurPos({ lat: p.lat, lng: p.lng, acc: p.acc })
    ensureMap(p.lat, p.lng)
    // 標記與地圖永遠跟著「目前」位置（即時感），即使該點未被採納為距離
    if (markRef.current) markRef.current.setLatLng([p.lat, p.lng])
    if (mapRef.current && followRef.current) mapRef.current.panTo([p.lat, p.lng]) // 僅在「跟隨中」才回中；使用者手動看地圖時不打斷
    if (statusRef.current !== 'tracking') return // 預熱階段（未開始跑步）：只顯示 GPS 精度＋地圖位置，不累積距離、不警告
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
          const over = d / dt > MAX_SPEED // 超過人體極限（疑似載具/GPS 跳點）
          const seg = over ? MAX_SPEED * dt : d
          rawDistRef.current += seg // 原始距離（含超速夾限）→ 供疑似搭車偵測（不因排除有效距離而失效）
          if (over) {
            setAnomalies((n) => n + 1)
            // 超速段完全不計入有效距離：不刷里程、不推進課表分段（與伺服器一致）
          } else {
            distRef.current += seg
            setDistance(distRef.current)
            // 每公里分段（只在有效距離上前進）
            const km = Math.floor(distRef.current / 1000)
            const el = (p.t - startRef.current) / 1000
            while (splitMarkRef.current.length < km) {
              const prevEl = splitMarkRef.current.length ? splitMarkRef.current[splitMarkRef.current.length - 1] : 0
              splitMarkRef.current.push(el)
              setSplits((s) => [...s, el - prevEl])
            }
          }
          lastAccRef.current = p // 仍前進採納點（避免搭車結束後算出巨大跳段）
          pointsRef.current.push(p)
          if (lineRef.current) lineRef.current.addLatLng([p.lat, p.lng])
        }
      }
    }
    // 事件正式進行中（非演出階段）才更新即時位移（進度條）
    if (activeEventRef.current?.phase === 'active') setEventMoved(distRef.current - activeEventRef.current.triggerD)
    // 防當掉：暫存採納後的軌跡
    localStorage.setItem(LS_KEY, JSON.stringify({ start: startRef.current, points: pointsRef.current.slice(-2000) }))
  }, [ensureMap])
  const onPosRef = useRef(onPos); onPosRef.current = onPos

  // 里程獎勵設定（進度條/預覽用）：進頁抓一次
  useEffect(() => {
    if (!getUserToken()) return
    withUserAuth((t) => mileageExpApi.config(t)).then(setMileageCfg).catch(() => {})
  }, [])

  // 進入頁面（idle）就先啟動 GPS「預熱」偵測：立即顯示精度、定位地圖，但不記錄距離。
  // 開始跑步時 start() 會同步關掉它、換成正式追蹤；離開 idle / 卸載時自動關閉（用 onPosRef 避免每次 render 重訂閱）。
  useEffect(() => {
    if (status !== 'idle') return
    if (typeof navigator === 'undefined' || !navigator.geolocation) return
    const id = navigator.geolocation.watchPosition(
      (pos) => onPosRef.current(pos),
      () => { /* 預熱失敗忽略（權限未給/逾時）；開始跑步時會在使用者手勢內再要求 */ },
      { enableHighAccuracy: true, maximumAge: 3000, timeout: 15000 },
    )
    warmWatchRef.current = id
    return () => { try { navigator.geolocation.clearWatch(id) } catch { /* ignore */ } warmWatchRef.current = null }
  }, [status])

  async function acquireWake() {
    try { wakeRef.current = await (navigator as any).wakeLock?.request('screen') } catch { /* ignore */ }
  }

  // --- 事件任務引擎 ---
  // 事件隨機等待：開始跑步 / 每次事件結束後，重取一個落在 [min,max] 的等待時間，
  // 決定「下一個事件最早何時可觸發」（時間到、且符合觸發條件時才真的出現）。取代原本寫死的 15 分鐘冷卻。
  // firstOfRun：本趟第一個事件——若伺服器帶來「前幾趟較短等待」(firstWaitRef>0) 則用它（±10% 抖動），讓新玩家更快遇到。
  // factor：本次等待倍率。計時到但當下沒有事件符合條件時，用 0.5（一半等待）較快重試，
  // 避免「先短暫達標、計時卻落在達標窗口之外」而整趟錯過。
  function rollNextEvent(firstOfRun = false, factor = 1) {
    const now = Date.now()
    if (firstOfRun && firstWaitRef.current > 0) {
      nextEventAtRef.current = now + firstWaitRef.current * (0.9 + Math.random() * 0.2) * 1000
      return
    }
    const min = Math.max(1, waitMinRef.current)
    const max = Math.max(min, waitMaxRef.current)
    nextEventAtRef.current = now + (min + Math.random() * (max - min)) * 1000 * factor
  }
  // 清空目前進行中事件（含演出階段）：統一收尾，讓所有清除路徑一致（放棄/完成/失敗/結束跑步）。
  function clearEvent() {
    activeEventRef.current = null
    armIdRef.current = 0
    setActiveEvent(null); setEventMoved(0); setShowFlash(false)
  }
  // 近 windowMs 的位移（公尺）；歷史不足回 null
  function movedInWindow(windowMs: number): number | null {
    const s = distSamplesRef.current
    if (s.length < 2) return null
    const target = Date.now() - windowMs
    let past: { t: number; d: number } | null = null
    for (let i = s.length - 1; i >= 0; i--) { if (s[i].t <= target) { past = s[i]; break } }
    if (!past) return null // 尚無足夠歷史（跑步時間短於觀察視窗）
    return rawDistRef.current - past.d
  }
  function triggerEligible(def: EventDef): boolean {
    const p = def.trigger_params
    const windowSec = p.window_s ?? 0
    const moved = movedInWindow(windowSec * 1000)
    if (moved == null) return false // 歷史不足（跑步時間短於觀察視窗）
    switch (def.trigger_type) {
      case 'distance_below': return moved < (p.max_move_m ?? 0)
      case 'distance_above': return moved > (p.min_move_m ?? 0)
      case 'pace_slow': // 跑太慢：這段有實際移動、且配速（秒/公里）慢於門檻
        if (moved < (p.min_move_m ?? 0) || moved <= 0) return false
        return (windowSec * 1000) / moved > (p.slower_than_spk ?? 0)
      case 'pace_fast': // 跑很快：配速快於門檻（需有移動，否則配速為無限大不會成立）
        if (moved <= 0) return false
        return (windowSec * 1000) / moved < (p.faster_than_spk ?? 0)
      case 'pace_drop': { // 越跑越慢：後半配速比前半慢 drop 以上（兩段都要有移動）
        if (moved < (p.min_move_m ?? 0)) return false
        const now = Date.now()
        const first = distAt(now - windowSec * 500) - distAt(now - windowSec * 1000)
        const second = distAt(now) - distAt(now - windowSec * 500)
        if (first <= 0 || second <= 0) return false
        return (windowSec * 500) / second - (windowSec * 500) / first > (p.drop_spk ?? 0)
      }
    }
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
    const elapsedS = Math.floor((triggerT - startRef.current) / 1000)
    const baseSpk = baselineSpk(triggerD, elapsedS)
    if (def.completion_type === 'pace_shift' && baseSpk <= 0) return // 變速跑需配速基準；里程/時間不足時不觸發（避免必敗任務）
    // 先建立 occurrence（伺服器閘門確認）→ 確認後才提交事件 + 放煙火。與舊版「確認後才響」一致：
    // 被閘門擋下時完全靜默（不誤放警報/紅閃），且晚回來的失敗永遠不會拆掉使用者已進行中的任務。
    // 前 3 趟的「本趟第一個事件」(lastEventEndRef=0 且有加速等待) 帶 first_of_run，讓後端放寬間隔地板。
    const firstOfRun = lastEventEndRef.current === 0 && firstWaitRef.current > 0
    const myArm = ++armSeqRef.current; armIdRef.current = myArm; armingRef.current = true
    try {
      const occ = await eventApi.createOccurrence(token, { def_id: def.id, trigger_dist_m: triggerD, trigger_elapsed_s: elapsedS, first_of_run: firstOfRun })
      if (!occ.id) { if (armIdRef.current === myArm) armIdRef.current = 0; lastEventEndRef.current = Date.now(); rollNextEvent(); return } // 閘門擋下：靜默退回 + reroll
      if (armIdRef.current !== myArm) { eventApi.fail(token, occ.id).catch(() => {}); return } // 飛行期間已 reset/start（序號變動）→ 收掉孤兒
      // Step1 宣告：deadline/readyUntil 先不算（等接受 → 321 結束才起算）。基準 baseSpk 取觸發當下。
      const ae: ActiveEvent = { def, occId: occ.id, triggerD, triggerT, readyUntil: 0, deadline: 0, baseSpk, phase: 'announce' }
      activeEventRef.current = ae; setActiveEvent(ae); setEventMoved(0); setEventResult(null)
      setShowFlash(true); playEventAlarm(); vibrate([120, 80, 120, 80, 120]) // 事件觸發：全螢幕紅閃 + 噹噹噹 + 震動
    } catch { if (armIdRef.current === myArm) armIdRef.current = 0 }
    finally { armingRef.current = false }
  }
  // Step1 紅閃結束（約 1.6s）→ 進入 Step2 任務目標面板（等接受/放棄）。Phase B 邀請的閃光則無事件可推進。
  function onFlashDone() {
    setShowFlash(false)
    const ae = activeEventRef.current
    if (ae && ae.phase === 'announce') { const next = { ...ae, phase: 'offer' as const }; activeEventRef.current = next; setActiveEvent(next) }
  }
  // Step2 接受 → Step3 置中 321 倒數
  function acceptEvent() {
    const ae = activeEventRef.current; if (!ae) return
    const next = { ...ae, phase: 'countdown' as const }; activeEventRef.current = next; setActiveEvent(next)
  }
  // Step2 放棄 → 靜默收掉（伺服器標 failed 釋放閘門），不顯示失敗結果橫幅
  function declineEvent() {
    const ae = activeEventRef.current
    clearEvent(); lastEventEndRef.current = Date.now(); rollNextEvent()
    if (ae) {
      const token = getUserToken()
      if (token) { if (ae.raceInstanceId) eventRaceApi.fail(token, ae.raceInstanceId).catch(() => {}); else if (ae.occId) eventApi.fail(token, ae.occId).catch(() => {}) }
    }
  }
  // Step3 321 數完 → Step4 事件正式開始：此刻才捕捉完成基準與 deadline（跑者讀面板站著不吃虧、更公平）
  function startActivePhase() {
    const ae = activeEventRef.current; if (!ae) return
    const now = Date.now()
    const limitS = ae.def.completion_params.limit_s || 60
    const next: ActiveEvent = { ...ae, phase: 'active', triggerD: distRef.current, triggerT: now, readyUntil: now, deadline: now + limitS * 1000 }
    activeEventRef.current = next; setActiveEvent(next); setEventMoved(0)
  }
  async function completeEvent(ae: ActiveEvent, moved: number, windowS: number, extra: Partial<CompleteEvidence> = {}) {
    clearEvent(); lastEventEndRef.current = Date.now(); rollNextEvent()
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
        ? { status: 'completed', def: ae.def, reward_exp: res.reward_exp ?? 0, reward_dp: res.reward_dp ?? 0, stars: (res as any).stars, bonus_exp: (res as any).bonus_exp, bonus_dp: (res as any).bonus_dp }
        : { status: 'failed', def: ae.def, reward_exp: 0, reward_dp: 0 })
    } catch { setEventResult({ status: 'failed', def: ae.def, reward_exp: 0, reward_dp: 0 }) }
    finally { completingRef.current = false }
  }
  function failEvent(ae: ActiveEvent) {
    clearEvent(); lastEventEndRef.current = Date.now(); rollNextEvent()
    const token = getUserToken()
    if (token) {
      if (ae.raceInstanceId) eventRaceApi.fail(token, ae.raceInstanceId).catch(() => {})
      else if (ae.occId) eventApi.fail(token, ae.occId).catch(() => {})
    }
    setEventResult({ status: 'failed', def: ae.def, reward_exp: 0, reward_dp: 0 })
  }
  // 互動小遊戲時間到 → 用收集到的 evidence 送後端分級發獎
  function handleInteractionDone(ev: { taps: number; held_ms: number; swipe_px: number; swipes: number; shape_pts: [number, number][]; shape: number }) {
    const ae = activeEventRef.current
    if (!ae) return
    const windowS = Math.max(0, (ae.deadline - ae.readyUntil) / 1000)
    completeEvent(ae, 0, windowS, { taps: ev.taps, held_ms: ev.held_ms, swipe_px: ev.swipe_px, swipes: ev.swipes, shape_pts: ev.shape_pts, shape: ev.shape })
  }
  // 收到多人事件邀請（任一綁定賽事的 WS 都走這裡）
  function onRaceMsg(ev: MessageEvent) {
    try {
      const msg = JSON.parse(ev.data)
      if (msg.type !== 'event_race_invite') return
      const p = msg.payload as RaceEventInvite
      if (!user?.id || !p.target_user_ids?.includes(user.id)) return
      if (activeEventRef.current || raceInviteRef.current || armingRef.current || woActiveRef.current) return // 一次一任務（課表進行中也不插隊）
      if (Date.now() > p.join_deadline) return
      setRaceInvite(p)
      setShowFlash(true); playEventAlarm(); vibrate([120, 80, 120, 80, 120]) // 多人邀請：同樣全螢幕紅閃 + 噹噹噹（引注意），仍走加入/略過流程
    } catch { /* ignore */ }
  }
  // Phase B：對「所有進行中且已報名」的賽事各連一條 WS，任一場來邀請都收得到
  async function connectRaceWS() {
    const token = getUserToken()
    if (!token || wssRef.current.length) return
    try {
      const { races } = await eventRaceApi.context(token)
      if (!races.length) return
      raceIdsRef.current = races.map((r) => r.id)
      for (const rid of raceIdsRef.current) {
        const ws = createRaceSocket(rid, token)
        ws.onmessage = onRaceMsg
        ws.onclose = () => { wssRef.current = wssRef.current.filter((w) => w !== ws) }
        wssRef.current.push(ws)
      }
    } catch { /* ignore */ }
  }
  // 加入多人事件 → 轉為一般 activeEvent，交給既有引擎評估完成
  async function joinRace(inv: RaceEventInvite) {
    const token = getUserToken(); if (!token) return
    setRaceInvite(null)
    const baseSpk = baselineSpk(distRef.current, Math.floor((Date.now() - startRef.current) / 1000))
    if (inv.completion_type === 'pace_shift' && baseSpk <= 0) { setCpMsg('需先跑一小段建立配速基準，才能加入此變速跑任務'); return }
    try {
      const res = await eventRaceApi.join(token, inv.instance_id)
      if (!res.joined) { setCpMsg(res.message || '無法加入此事件'); return }
      const now = Date.now()
      const def: EventDef = {
        name: res.name || inv.name, description: '', enabled: true, weight: 100,
        trigger_type: '', trigger_params: {}, completion_type: res.completion_type || inv.completion_type,
        completion_params: res.completion_params || inv.completion_params, message: res.message || inv.message,
        image_url: inv.image_url, image_day_url: inv.image_day_url, image_dusk_url: inv.image_dusk_url, image_night_url: inv.image_night_url,
        reward_exp: res.reward_exp ?? inv.reward_exp, reward_dp: res.reward_dp ?? inv.reward_dp,
      }
      const deadline = res.deadline || (now + (def.completion_params.limit_s || 180) * 1000)
      // Phase B：加入即開始（保留多人同步節奏，不插 321）→ 直接 active 交既有引擎
      armIdRef.current = ++armSeqRef.current
      const ae: ActiveEvent = { def, occId: '', raceInstanceId: inv.instance_id, triggerD: distRef.current, triggerT: now, readyUntil: now, deadline, baseSpk, phase: 'active' }
      activeEventRef.current = ae; setActiveEvent(ae); setEventMoved(0); setEventResult(null); setShowFlash(false)
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
      // 手動觸發（測試）也走完整四步驟演出：宣告 → 任務目標 → 321 → 開始
      armIdRef.current = ++armSeqRef.current
      const ae: ActiveEvent = { def, occId: res.occ_id || '', triggerD: distRef.current, triggerT, readyUntil: 0, deadline: 0, baseSpk: baselineSpk(distRef.current, Math.floor((triggerT - startRef.current) / 1000)), phase: 'announce' }
      activeEventRef.current = ae; setActiveEvent(ae); setEventMoved(0); setEventResult(null)
      setShowFlash(true); playEventAlarm(); vibrate([120, 80, 120, 80, 120]) // 事件觸發：全螢幕紅閃 + 噹噹噹 + 震動
    } catch { /* ignore */ }
  }

  // pace_shift 基準：觸發時的平均配速（秒/公里），夾在 [180,1200]（與伺服器 clampBaselineSpk 一致）。
  // 0 = 無有效資料（距離/時間不足），該任務將無法達成。
  function baselineSpk(distM: number, elapsedS: number): number {
    if (distM <= 0 || elapsedS <= 0) return 0
    return Math.min(1200, Math.max(180, elapsedS / (distM / 1000)))
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
    distSamplesRef.current.push({ t: now, d: rawDistRef.current })
    if (distSamplesRef.current.length > 1600) distSamplesRef.current.splice(0, 400) // 上限 ~25 分鐘
    // 疑似搭車（即時偵測）：近 45 秒配速快於 2:20/km（遠超人體極限）→ 即時提醒＋暫停事件（避免搭車刷任務）
    const veh45 = movedInWindow(45000)
    const vehLike = veh45 != null && veh45 > 150 && 45000 / veh45 < 140
    vehicleLikeRef.current = vehLike
    setVehicleWarn(vehLike)
    if (woActiveRef.current || vehLike) return // 課表挑戰中 / 疑似搭車：暫停隨機事件/多人邀請
    // 測試：跑步中、無進行中事件時輪詢認領後台手動觸發（每 5 秒；結算中不認領）
    if (!activeEventRef.current && !completingRef.current && now - lastClaimRef.current > 5000) {
      lastClaimRef.current = now
      claimManualEvent()
    }
    // Phase B：節流回報里程給後端（由後端依定義門檻/冷卻決定是否觸發多人事件）
    if (raceIdsRef.current.length && distRef.current > 0 && now - lastTriggerRef.current > 20000) {
      lastTriggerRef.current = now
      const token = getUserToken()
      if (token) {
        const moved = distRef.current, elapsed = Math.floor((now - startRef.current) / 1000)
        for (const rid of raceIdsRef.current) eventRaceApi.trigger(token, { race_id: rid, moved_m: moved, elapsed_s: elapsed }).catch(() => {})
      }
    }
    // 邀請倒數重繪＋逾時自動關閉
    if (raceInviteRef.current) {
      if (now > raceInviteRef.current.join_deadline) setRaceInvite(null)
      else setInviteNow(now)
    }
    const ae = activeEventRef.current
    if (ae) {
      if (ae.phase !== 'active') return // 演出中（announce/offer/countdown）：完成計算尚未起算（基準於 321 結束時捕捉）
      if (!ae.occId && !ae.raceInstanceId) return // 無 occurrence/賽事實例 → 不送完成（避免打空 id）
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
        // 後段加速（舊型）：後半移動 ≥ 前半 × 比例（到時間才判定）
        setEventMoved(moved)
        if (now >= ae.deadline) {
          const mid = ae.readyUntil + (ae.deadline - ae.readyUntil) / 2
          const firstHalf = distAt(mid) - distAt(ae.readyUntil)
          const secondHalf = distAt(ae.deadline) - distAt(mid)
          if (firstHalf > 5 && secondHalf >= firstHalf * ((cp.ratio_pct ?? 100) / 100)) completeEvent(ae, moved, windowS, { first_half_m: firstHalf, second_half_m: secondHalf })
          else failEvent(ae)
        }
      } else if (ae.def.completion_type === 'pace_shift') {
        // 變速跑：整段維持比平均配速快/慢 delta（到時間才判定；伺服器以觸發快照重算基準 + 分段防瞬移）
        setEventMoved(moved)
        if (now >= ae.deadline) {
          const winDist = distAt(ae.deadline) - distAt(ae.readyUntil)
          const winSec = (ae.deadline - ae.readyUntil) / 1000
          const winPace = winDist > 0 ? winSec / (winDist / 1000) : Infinity
          const base = ae.baseSpk ?? 0
          const delta = Math.abs(cp.delta_spk ?? 0) // 與伺服器一致：距離差取絕對值
          const faster = (cp.faster ?? 0) >= 0.5
          const maxSeg = bestBurst(ae.readyUntil, ae.deadline, 5000) // 任一 5 秒最大位移（防瞬移，與伺服器同門檻）
          const ok = base > 0 && maxSeg <= (1000 / 120) * 6 * 1.2 &&
            (faster ? (base - delta > 0 && winPace <= base - delta) : (winPace >= base + delta && winDist >= winSec * 0.5))
          if (ok) completeEvent(ae, winDist, winSec, { baseline_spk: base, max_seg_m: maxSeg })
          else failEvent(ae)
        }
      }
      return
    }
    // 無進行中事件 → 等隨機等待時間到 + 符合觸發條件才挑選（結算中 / 建立 occurrence 中不 arm，避免重複觸發或蓋掉剛完成的結果）
    if (completingRef.current || armingRef.current || now < nextEventAtRef.current) return
    // 事件間距＝「隨機等待 nextEventAtRef([最短,最長])」＋伺服器防濫用地板(taskGateOpen)決定。
    // 舊的 per-def cooldown_sec 是「寫死 15 分鐘冷卻」的殘留：第一個事件時 lastEventEndRef=0 剛好不擋，
    // 但事件結束後 lastEventEndRef 變成真時間，會把「所有」def 擋掉整趟（cooldown 越大擋越久）→ 第二個事件永遠不觸發。移除之。
    const eligible = eventDefsRef.current.filter((d) => triggerEligible(d))
    if (eligible.length > 0) armEvent(pickWeighted(eligible))
    else rollNextEvent(false, 0.5) // 計時到但此刻無事件符合條件 → 用一半等待較快重試（不整趟卡死、也不每秒狂試）
  }

  function start() {
    setErr(''); clearTimeout(errTimerRef.current); setErrFade(false)
    if (!navigator.geolocation) { setErr('此裝置/瀏覽器不支援定位'); return }
    // 關掉進頁面的 GPS 預熱偵測，避免與正式追蹤重複回報
    if (warmWatchRef.current != null) { try { navigator.geolocation.clearWatch(warmWatchRef.current) } catch { /* ignore */ } warmWatchRef.current = null }
    pointsRef.current = []; distRef.current = 0; rawDistRef.current = 0; splitMarkRef.current = []; lastAccRef.current = null
    setDistance(0); setElapsed(0); setSplits([]); setAnomalies(0); setResult(null)
    vehicleLikeRef.current = false; setVehicleWarn(false)
    followRef.current = true; setFollowing(true) // 每趟開始都恢復自動跟隨（即使 idle 時曾手動看地圖）
    if (lineRef.current) lineRef.current.setLatLngs([]) // 清掉上一趟的軌跡線（避免地圖殘留）
    // 事件引擎重置
    distSamplesRef.current = []; activeEventRef.current = null; armIdRef.current = 0; lastEventEndRef.current = 0
    rollNextEvent(true) // 開始跑步：第一個事件用「前幾趟較短」的等待（若伺服器有帶）
    setActiveEvent(null); setEventResult(null); setEventMoved(0); setShowFlash(false)
    // 重新抓一次（含最新 run_count 對應的 first_event_wait_sec）：同一 session 連跑時，mount 時的值可能已過期
    fetchEventDefs().then(() => { if (statusRef.current === 'tracking' && !activeEventRef.current && lastEventEndRef.current === 0) rollNextEvent(true) })
    // Phase B 重置
    setRaceInvite(null); raceIdsRef.current = []; lastTriggerRef.current = 0; lastClaimRef.current = 0
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
    // 心跳：立即 + 每 30 秒回報「目前在跑」（供後台總覽名單；失敗忽略）
    const ping = () => { const t = getUserToken(); if (t) activitiesApi.trackPing(t).catch(() => {}) }
    ping()
    pingTimerRef.current = setInterval(ping, 30000)
    acquireWake() // 不 await：wake lock 失敗或延遲都不影響定位
    // 盡力鎖直屏（Android/PWA 全螢幕有效；iOS Safari 不支援 → 靠下方「轉回直立」提示保底）
    try { (screen.orientation as any)?.lock?.('portrait').catch(() => {}) } catch { /* 不支援就忽略 */ }
  }

  const cleanup = useCallback(() => {
    if (watchRef.current != null) navigator.geolocation.clearWatch(watchRef.current)
    watchRef.current = null
    clearInterval(timerRef.current)
    clearInterval(evalTimerRef.current)
    clearInterval(pingTimerRef.current)
    clearTimeout(errTimerRef.current)
    for (const w of wssRef.current) { try { w.close() } catch { /* ignore */ } }
    wssRef.current = []; raceIdsRef.current = []
    try { wakeRef.current?.release() } catch { /* ignore */ }
    wakeRef.current = null
    try { (screen.orientation as any)?.unlock?.() } catch { /* ignore */ }
  }, [])

  // 按「結束並上傳」：只有「正式進行中」事件才跳確認（損失規避）；演出中（未接受）則靜默放棄後直接結束
  // 提示訊息：X 手動關閉；「軌跡太短」等暫時訊息顯示約 1 秒後自動淡出（避免擋住下方面板操作）
  function dismissWarn() { clearTimeout(warnTimer.current); setWarn('') }
  function dismissErr() { clearTimeout(errTimerRef.current); setErrFade(false); setErr('') }
  function flashErr(msg: string) {
    clearTimeout(errTimerRef.current)
    setErrFade(false); setErr(msg)
    errTimerRef.current = setTimeout(() => {
      setErrFade(true) // 開始淡出
      errTimerRef.current = setTimeout(() => { setErr(''); setErrFade(false) }, 550)
    }, 1000)
  }

  function requestFinish() {
    if (woActiveRef.current) { woActiveRef.current = false; setWoPhase('idle') } // 課表中途結束：停止逐段驅動（挑戰仍保留，可再進來續挑）
    const ae = activeEventRef.current
    if (ae && ae.phase === 'active') { setConfirmEnd(true); return }
    if (ae) declineEvent()
    finish()
  }
  // 確認放棄事件並結束：伺服器端標記事件失敗（釋放 occurrence/閘門），再結束上傳
  function endWithForfeit() {
    setConfirmEnd(false)
    const ae = activeEventRef.current
    if (ae) {
      clearEvent(); lastEventEndRef.current = Date.now()
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
  // 依狀態預設面板停靠：完成全展（看結果）、其餘半展（同時看得到地圖與數據/警告/打卡，可再上拉看更多或下拉看更多地圖）
  useEffect(() => { sheet.setSnap(status === 'done' || woPhase === 'running' || woPhase === 'done' ? 'full' : 'half') }, [status, woPhase]) // eslint-disable-line react-hooks/exhaustive-deps
  // 面板高度變動 → 讓 Leaflet 重算尺寸（避免地圖灰塊/破圖）
  useEffect(() => { if (mapReady && mapRef.current) { try { mapRef.current.invalidateSize() } catch { /* ignore */ } } }, [mapReady, sheet.H])
  // 載入效果覆寫（正式圖片/音檔）：圖片給互動層、音效交給 sfx 解碼
  useEffect(() => { const t = getUserToken(); if (t) loadEffectAssets(t).then(setFxAssets) }, [user?.id])
  function toggleMute() { const next = !isMuted(); sfxSetMuted(next); setMuted(next); if (!next) unlockAudio() }

  async function finish(): Promise<GpsRunResult | null> {
    cleanup()
    setStatus('done')
    const pts = pointsRef.current
    if (pts.length < 2) { flashErr('軌跡太短，未上傳'); localStorage.removeItem(LS_KEY); return null }
    const token = getUserToken()
    if (!token) { setErr('未登入，無法上傳'); return null }
    setUploading(true)
    try {
      const { result } = await withUserAuth((t) => activitiesApi.uploadGps(t, {
        started_at: new Date(startRef.current).toISOString(),
        ended_at: new Date(pts[pts.length - 1].t).toISOString(),
        points: pts,
      }))
      setResult(result)
      localStorage.removeItem(LS_KEY)
      return result
    } catch (e: any) {
      setErr(e?.message || '上傳失敗')
      return null
    } finally { setUploading(false) }
  }

  // 進頁偵測「上次未上傳的跑步」（LS_KEY 備份）→ 提示可恢復上傳，避免忘記上傳整趟白跑
  useEffect(() => {
    try {
      const raw = localStorage.getItem(LS_KEY)
      if (!raw) return
      const data = JSON.parse(raw)
      const pts: GpsPoint[] = data?.points
      if (!Array.isArray(pts) || pts.length < 2 || !data.start) { localStorage.removeItem(LS_KEY); return }
      const lastT = pts[pts.length - 1].t
      if (Date.now() - lastT > 24 * 3600 * 1000) { localStorage.removeItem(LS_KEY); return } // 太舊(>24h)不提示
      let m = 0
      for (let i = 1; i < pts.length; i++) m += haversineM(pts[i - 1], pts[i])
      setRecover({ start: data.start, points: pts, km: Math.round(m / 10) / 100, mins: Math.round((lastT - data.start) / 60000) })
    } catch { localStorage.removeItem(LS_KEY) }
  }, [])

  async function uploadRecovered() {
    if (!recover) return
    const token = getUserToken()
    if (!token) { setShowLogin(true); return }
    const pts = recover.points
    setUploading(true)
    try {
      const { result } = await withUserAuth((t) => activitiesApi.uploadGps(t, {
        started_at: new Date(recover.start).toISOString(),
        ended_at: new Date(pts[pts.length - 1].t).toISOString(),
        points: pts,
      }))
      setResult(result); setStatus('done'); localStorage.removeItem(LS_KEY); setRecover(null)
    } catch (e: any) { setErr(e?.message || '上傳失敗') }
    finally { setUploading(false) }
  }
  function discardRecovered() { localStorage.removeItem(LS_KEY); setRecover(null) }

  // 跑步進行中若嘗試離開/關閉視窗 → native 攔截提示，避免誤觸中斷整趟
  useEffect(() => {
    if (status !== 'tracking') return
    const h = (e: BeforeUnloadEvent) => { e.preventDefault(); e.returnValue = '' }
    window.addEventListener('beforeunload', h)
    return () => window.removeEventListener('beforeunload', h)
  }, [status])

  // ── 個人任務「結構化課表」：面板載入 / 挑戰 / 開始 / 逐段驅動 / 完成 ──
  // 載入任務面板（各階段前沿課表卡 + 進行中挑戰卡）。有進行中挑戰 → 載入分段序列進入就緒。
  const loadPanel = useCallback(async () => {
    if (!getUserToken()) return
    try {
      const r = await withUserAuth((t) => personalTasksApi.trackPanel(t))
      setPanel(r)
      const ac = r.active_card
      if (ac && ac.segments && ac.segments.length) {
        setWorkout({ taskId: ac.task_id, title: ac.title || '課表挑戰', steps: expandSegments(ac.segments), kind: 'personal' })
      } else if (!woActiveRef.current) {
        setWorkout(null)
      }
    } catch { /* ignore */ }
  }, [])
  useEffect(() => { loadPanel() }, [loadPanel])

  // 面板上挑戰某課表卡 → 挑戰(第一次免費/重挑扣DP) → 直接用卡片 segments 進就緒(鎖定該卡)
  async function challengeCard(c: PanelCard) {
    setPanelBusy(c.task_id); setErr('')
    try {
      await withUserAuth((t) => personalTasksApi.challenge(t, c.task_id))
      setWorkout({ taskId: c.task_id, title: c.title, steps: expandSegments(c.segments), kind: 'personal' })
      await loadPanel()
    } catch (e: any) { setErr(e?.message || '挑戰失敗') }
    finally { setPanelBusy('') }
  }
  // 放棄承接中的課表（已花的 DP 不退還）→ 解鎖面板、可改挑其他課表
  async function abandonActive(taskId: string) {
    setPanelBusy(taskId)
    try { await withUserAuth((t) => personalTasksApi.abandon(t, taskId)) } catch (e: any) { setErr(e?.message || '放棄失敗') }
    setWorkout(null); setWoPhase('idle')
    await loadPanel()
    setPanelBusy('')
  }

  function startWorkout() {
    if (!workout) return
    beginWorkout(workout)
  }
  // 啟動一份課表（個人任務或關主挑戰共用）。須在使用者手勢內呼叫（start() 會請求定位權限）
  function beginWorkout(wo: { taskId: string; title: string; steps: WoStep[]; kind: 'personal' | 'explore'; cardUrl?: string }) {
    setWorkout(wo)
    woResultsRef.current = { inBand: 0, total: 0, detail: [] }
    woStepIdxRef.current = 0
    setWoStepIdx(0); setWoHits({}); setWoResult(null)
    woActiveRef.current = true // 從一開始（含 321 倒數）就暫停隨機事件，整趟課表都不被打擾
    start() // 既有 GPS 追蹤啟動（含定位權限請求，須在使用者手勢內）
    setWoPhase('countdown')
  }
  function woCountdownDone() {
    woStepStartRef.current = { dist: distRef.current, time: Date.now() }
    woActiveRef.current = true
    setWoPhase('running')
  }
  async function finishWorkout() {
    woActiveRef.current = false
    setWoPhase('done')
    // 先上傳 GPS（伺服器重算+防弊）→ 拿到是否標記；被標記疑似載具就不回報課表完成（成績不計）
    const upload = await finish()
    if (upload?.flagged) { setWoResult({ stars: 0, reward_exp: 0, reward_dp: 0, flagged: true }); return }
    const res = woResultsRef.current
    const token = getUserToken()
    try {
      if (token && workout) {
        if (workout.kind === 'explore') {
          // 關主挑戰：回報 → 得星、3★ 取得卡片；刷新探索列表（收服狀態）
          const r = await withUserAuth((t) => exploreApi.complete(t, workout.taskId, { finished: true, work_in_band: res.inBand, work_total: res.total }))
          setWoResult({ stars: r.stars, reward_exp: r.reward_exp, reward_dp: r.reward_dp, card_obtained: r.card_obtained })
          if (r.card_obtained) setCelebrateCard({ bossId: workout.taskId, name: workout.title, cardUrl: workout.cardUrl }) // 3★ 取卡 → 恭喜彈窗
          fetchExplore()
        } else {
          const r = await withUserAuth((t) => personalTasksApi.complete(t, workout.taskId, { finished: true, work_in_band: res.inBand, work_total: res.total, evidence: res.detail }))
          setWoResult({ stars: r.stars, reward_exp: r.reward_exp, reward_dp: r.reward_dp })
        }
      }
    } catch { /* 結算失敗不擋畫面 */ }
  }
  // 逐段驅動：每 0.5s 讀 distRef/時間，分段達標即（對 work 段）評配速並前進；跑完整份課表 → 完成
  useEffect(() => {
    if (woPhase !== 'running' || !workout) return
    const id = setInterval(() => {
      if (statusRef.current !== 'tracking') return
      const idx = woStepIdxRef.current
      const step = workout.steps[idx]
      if (!step) return
      const stepDist = distRef.current - woStepStartRef.current.dist
      const stepTime = (Date.now() - woStepStartRef.current.time) / 1000
      setWoNow(Date.now())
      const done = step.targetType === 'distance' ? stepDist >= step.target : stepTime >= step.target
      if (!done) return
      if (step.graded) {
        const avgPace = stepDist > 5 ? stepTime / (stepDist / 1000) : 9999
        const inBand = paceInBand(avgPace, step)
        woResultsRef.current.total += 1
        if (inBand) woResultsRef.current.inBand += 1
        woResultsRef.current.detail.push({ label: step.label, avg_pace_s: Math.round(avgPace), in_band: inBand, dist_m: Math.round(stepDist), time_s: Math.round(stepTime) })
        setWoHits((h) => ({ ...h, [idx]: inBand }))
      }
      const next = idx + 1
      woStepIdxRef.current = next
      if (next >= workout.steps.length) finishWorkout()
      else { woStepStartRef.current = { dist: distRef.current, time: Date.now() }; setWoStepIdx(next) }
    }, 500)
    return () => clearInterval(id)
  }, [woPhase, workout]) // eslint-disable-line react-hooks/exhaustive-deps

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
    try {
      const r = await eventApi.active(token)
      setEventDefs(r.defs)
      if (r.wait_min_sec && r.wait_min_sec > 0) waitMinRef.current = r.wait_min_sec
      if (r.wait_max_sec && r.wait_max_sec > 0) waitMaxRef.current = Math.max(waitMinRef.current, r.wait_max_sec)
      firstWaitRef.current = r.first_event_wait_sec && r.first_event_wait_sec > 0 ? r.first_event_wait_sec : 0 // 前幾趟較短等待；0=用正常區間
    } catch { /* ignore */ }
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
      // 城市探索打卡點：畫在最上層、較醒目（紫=未探索神秘/金=已揭露/綠=已收服）；目標關主放大+常駐標籤
      exploreCps.forEach((b) => {
        const color = b.card_obtained ? '#46E3A0' : b.discovered ? '#E7B84B' : '#C77DFF'
        const isFocus = b.id === focusBoss
        L.circle([b.lat, b.lng], { radius: b.radius_m || 40, color, weight: isFocus ? 3 : 1.5, fillOpacity: isFocus ? 0.25 : 0.12, dashArray: '4 4' }).addTo(layer)
        L.circleMarker([b.lat, b.lng], { radius: isFocus ? 13 : 9, color: '#fff', weight: 2.5, fillColor: color, fillOpacity: 1 }).addTo(layer)
          .bindTooltip((b.discovered ? b.name : (b.place || '神秘打卡點')) + ' ⚔', { permanent: isFocus })
      })
    })
  }, [checkpoints, exploreCps, mapReady, focusBoss])

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

  // ── 城市探索打卡點：表面上是打卡任務，打卡後才揭露關主挑戰事件 ──
  const fetchExplore = useCallback(async () => {
    const token = getUserToken()
    if (!token) { setExploreCps([]); return }
    try {
      const { bosses } = await exploreApi.list(token)
      setExploreCps(bosses.filter((b) => b.lat && b.lng))
    } catch { /* ignore */ }
  }, [])
  useEffect(() => { fetchExplore() }, [fetchExplore, user?.id])

  const exDist = (b: ExploreBoss): number | null =>
    curPos ? haversineM({ lat: curPos.lat, lng: curPos.lng, t: 0, acc: 0 }, { lat: b.lat, lng: b.lng, t: 0, acc: 0 }) : null
  const exDpCost = (b: ExploreBoss): number =>
    (b.attempts && b.attempts > 0 && b.retry_dp_cost > 0) ? b.retry_dp_cost : Math.max(0, b.difficulty_stars) * 10
  // 城市探索清單：依距離排序（最近在最上，未定位則維持原順序）
  const exSorted = exploreCps
    .slice()
    .sort((a, b) => (exDist(a) ?? Infinity) - (exDist(b) ?? Infinity))
  // 清單列：已揭露待挑戰 ＋ 最近 10 筆未打卡（避免 572 點全列）＋ 從城市探索「前往打卡」聚焦帶來的目標點（確保清單裡有它可打卡）
  const exList = (() => {
    const base = [
      ...exSorted.filter((b) => b.discovered && !b.card_obtained),
      ...exSorted.filter((b) => !b.discovered).slice(0, 10),
    ]
    if (focusBoss && !base.some((b) => b.id === focusBoss)) {
      const fb = exSorted.find((b) => b.id === focusBoss)
      if (fb) return [fb, ...base]
    }
    return base
  })()

  // 「前往打卡」：讀取目標關主 id，地圖定位到該打卡點並放大（只做一次；停止 GPS 自動跟隨、篩到該縣市讓清單也顯示）
  useEffect(() => {
    const p = typeof window !== 'undefined' ? new URLSearchParams(window.location.search).get('focus') : null
    if (p) setFocusBoss(p)
  }, [])
  useEffect(() => {
    if (focusDoneRef.current || !mapReady || !mapRef.current || !focusBoss) return
    const b = exploreCps.find((x) => x.id === focusBoss)
    if (!b || (!b.lat && !b.lng)) return
    focusDoneRef.current = true
    followRef.current = false; setFollowing(false)
    mapRef.current.setView([b.lat, b.lng], 16)
  }, [mapReady, exploreCps, focusBoss])

  // 打卡 → 地理驗證通過即揭露關主 → 跳出關主挑戰面板（表面打卡，實為事件觸發）
  async function doExploreCheckin(b: ExploreBoss) {
    setCpMsg('')
    const token = getUserToken()
    if (!token) { setShowLogin(true); return }
    setCpBusy('ex:' + b.id)
    try {
      let lat = curPos?.lat, lng = curPos?.lng, acc = curPos?.acc ?? 0
      if (status !== 'tracking' || lat == null) {
        const pos = await new Promise<GeolocationPosition>((res, rej) =>
          navigator.geolocation.getCurrentPosition(res, rej, { enableHighAccuracy: true, timeout: 20000, maximumAge: 0 }))
        lat = pos.coords.latitude; lng = pos.coords.longitude; acc = pos.coords.accuracy ?? 0
      }
      const r = await exploreApi.checkin(token, b.id, { lat: lat!, lng: lng!, acc })
      if (r.ok && r.boss) {
        await fetchExplore()
        setBossPanel({ boss: r.boss, phase: 'intro', dpCost: exDpCost(r.boss) })
      } else {
        setCpMsg(r.message || (r.status === 'out_of_range' ? '尚未到達打卡點' : r.status === 'low_accuracy' ? '定位精準度不足，請到空曠處再試' : '打卡失敗'))
      }
    } catch (e: any) {
      setCpMsg(e?.code === 1 ? '需要定位權限才能打卡' : (e?.message || '打卡失敗，請重試'))
    } finally { setCpBusy('') }
  }

  // 接受關主挑戰（扣 DP）→ 面板切到「開始」階段（關主開場對話）
  async function acceptBoss() {
    if (!bossPanel) return
    const token = getUserToken()
    if (!token) { setShowLogin(true); return }
    setExploreBusy(true); setCpMsg('')
    try {
      await withUserAuth((t) => exploreApi.accept(t, bossPanel.boss.id))
      setBossPanel({ ...bossPanel, phase: 'start' })
    } catch (e: any) { setCpMsg(e?.message || '接受挑戰失敗') }
    finally { setExploreBusy(false) }
  }
  // 「開始挑戰」（使用者手勢）→ 關閉面板 + 用關主 segments 啟動課表引擎（kind=explore）
  function startBossWorkout() {
    if (!bossPanel) return
    const b = bossPanel.boss
    const steps = expandSegments(b.segments || [])
    if (!steps.length) { setCpMsg('此關主尚未設定挑戰課表'); setBossPanel(null); return }
    setBossPanel(null)
    beginWorkout({ taskId: b.id, title: b.name, steps, kind: 'explore', cardUrl: b.card_image_url })
  }

  const distKm = distance / 1000
  const avgPace = distKm >= PACE_MIN_KM ? elapsed / distKm : 0 // 未達門檻先顯示 --:--，避免爆數字
  // 分段即時配速：當下（進行中）這一公里的即時配速（秒/公里）。跨過整公里即歸零重算；不足 30m 先顯示 --:--
  const segKmDone = splitMarkRef.current.length
  const segStartT = segKmDone > 0 ? splitMarkRef.current[segKmDone - 1] : 0
  const segDistM = Math.max(0, distance - segKmDone * 1000)
  const segLivePace = segDistM >= 30 ? (elapsed - segStartT) / (segDistM / 1000) : 0
  // 里程獎勵進度（本趟）：每滿 1km 一份、受單趟上限
  const mCap = mileageCfg?.cap_km ?? 0
  const mEarned = mCap > 0 ? Math.min(Math.floor(distKm), mCap) : Math.floor(distKm)
  const mAtCap = mCap > 0 && mEarned >= mCap
  const mFrac = mAtCap ? 1 : distKm - Math.floor(distKm) // 距下一份的進度 0..1

  return (
   <GoogleAuthProvider>
    <PhoneFrame>
      {showLogin && <LoginModal onClose={() => setShowLogin(false)} />}
      {/* 上次未上傳的跑步 → 可恢復上傳 */}
      {recover && status !== 'tracking' && (
        <div style={{ position: 'fixed', inset: 0, zIndex: 3300, background: 'rgba(0,0,0,.66)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 24 }}>
          <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line-2)', borderRadius: 16, padding: '20px 18px', maxWidth: 340, width: '100%', boxShadow: '0 12px 40px rgba(0,0,0,.6)' }}>
            <div style={{ fontSize: 17, fontWeight: 900, color: 'var(--tx)', marginBottom: 8 }}>🏃 有一趟未上傳的跑步</div>
            <div style={{ fontSize: 13.5, color: 'var(--tx-dim)', lineHeight: 1.7 }}>
              偵測到上次離開時尚未上傳的跑步紀錄（約 <strong style={{ color: 'var(--fug)' }}>{recover.km} km</strong>、<strong style={{ color: 'var(--tx)' }}>{recover.mins} 分鐘</strong>）。要現在上傳嗎？
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 16 }}>
              <button onClick={uploadRecovered} disabled={uploading} style={{ ...btn, opacity: uploading ? 0.6 : 1 }}>{uploading ? '上傳中…' : '上傳這趟'}</button>
              <button onClick={discardRecovered} disabled={uploading} style={{ background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '10px', fontSize: 13.5, cursor: 'pointer', fontFamily: 'inherit' }}>捨棄</button>
            </div>
          </div>
        </div>
      )}
      {/* 觸發演出：Step1 全螢幕紅閃警報（Phase A/B 共用） */}
      {showFlash && <EventTriggerFlash onDone={onFlashDone} />}
      {/* Step2 任務目標面板（等接受/放棄，不自動消失） */}
      {status === 'tracking' && activeEvent?.phase === 'offer' && (
        <EventOfferPanel active={activeEvent} onAccept={acceptEvent} onDecline={declineEvent} />
      )}
      {/* Step3 置中 321，數完進 Step4 正式開始 */}
      {status === 'tracking' && activeEvent?.phase === 'countdown' && <Countdown321 onDone={startActivePhase} />}
      {/* 課表挑戰：321 倒數後開始逐段驅動 */}
      {status === 'tracking' && woPhase === 'countdown' && <Countdown321 onDone={woCountdownDone} />}
      {status === 'tracking' && activeEvent?.phase === 'active' && isInteractionType(activeEvent.def.completion_type) && (
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
                <button onClick={() => setConfirmEnd(false)} style={{ background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 10, padding: '11px', fontSize: 14.5, cursor: 'pointer' }}>再撐一下、完成任務</button>
                <button onClick={endWithForfeit} style={{ background: 'transparent', color: 'var(--hunt)', fontWeight: 700, border: '1px solid rgba(255,75,92,.5)', borderRadius: 10, padding: '10px', fontSize: 13.5, cursor: 'pointer' }}>放棄獎勵、仍要結束</button>
              </div>
            </div>
          </div>
        )
      })()}
      <header style={{ padding: 'var(--app-top, 16px) 18px 12px', flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid var(--line)' }}>
        {/* 跑步期間隱藏「返回/歷史」，避免誤離開而中斷；只能按「結束並上傳」正常結束 */}
        {status === 'tracking'
          ? <span className="track-blink" style={{ color: 'var(--hunt)', fontSize: 13, fontWeight: 800 }}>● 數據偵測中</span>
          : <a href="/" style={{ color: 'var(--tx-dim)', fontSize: 14, textDecoration: 'none' }}>← 返回</a>}
        <strong style={{ fontSize: 16 }}>GPS 跑步追蹤</strong>
        <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
          <button onClick={toggleMute} title={muted ? '事件音效：關' : '事件音效：開'} aria-label="事件音效開關" style={{ background: 'none', border: 'none', cursor: 'pointer', fontSize: 16, lineHeight: 1, padding: 0, color: 'var(--tx-dim)' }}>{muted ? '🔇' : '🔊'}</button>
          {status !== 'tracking' && <a href="/track/history" style={{ color: 'var(--fug)', fontSize: 13, textDecoration: 'none' }}>歷史</a>}
        </div>
      </header>

      {/* 地圖 + COROS 式可拖曳資訊面板：地圖佔滿容器、資訊面板可上下拖曳露出更多/更少（配色與顯示資訊都不變，只改操作體驗） */}
      <div ref={sheet.wrapRef} style={{ position: 'relative', flex: 1, minHeight: 0, overflow: 'hidden' }}>
        <div id="gps-map" style={{ position: 'absolute', inset: 0, zIndex: 0, background: 'var(--bg-2)' }} />
        {/* 回到目前位置：使用者手動看地圖後（暫停跟隨）才出現，點了恢復自動置中 */}
        {!following && curPos && status !== 'done' && (
          <button
            onClick={() => { followRef.current = true; setFollowing(true); if (mapRef.current && curPos) mapRef.current.panTo([curPos.lat, curPos.lng]) }}
            style={{ position: 'absolute', top: 12, right: 12, zIndex: 550, background: 'var(--bg-1)', color: 'var(--fug)', border: '1px solid var(--line-2)', borderRadius: 999, padding: '8px 13px', fontSize: 12.5, fontWeight: 700, cursor: 'pointer', boxShadow: '0 3px 12px rgba(0,0,0,.28)' }}
          >◎ 回到目前位置</button>
        )}
        {/* GPS 弱訊號警告 / 錯誤：浮在面板之上，任何停靠狀態都看得到（不隨面板收合而被藏起來） */}
        {(warn || err) && (
          <div style={{ position: 'absolute', left: 0, right: 0, top: 0, zIndex: 900, padding: '10px 12px 0', pointerEvents: 'none' }}>
            {warn && (
              <div style={{ background: '#b42020', color: '#fff', borderRadius: 10, padding: '9px 8px 9px 12px', fontSize: 13, marginBottom: 8, boxShadow: '0 4px 16px rgba(0,0,0,.4)', pointerEvents: 'auto', display: 'flex', alignItems: 'flex-start', gap: 8 }}>
                <span style={{ flex: 1, minWidth: 0, wordBreak: 'break-word' }}>⚠️ {warn}</span>
                <button onClick={dismissWarn} aria-label="關閉" style={dismissBtn}>✕</button>
              </div>
            )}
            {err && (
              <div style={{ background: '#b42020', color: '#fff', borderRadius: 10, padding: '9px 8px 9px 12px', fontSize: 13, boxShadow: '0 4px 16px rgba(0,0,0,.4)', pointerEvents: 'auto', display: 'flex', alignItems: 'flex-start', gap: 8, opacity: errFade ? 0 : 1, transition: 'opacity .5s ease' }}>
                <span style={{ flex: 1, minWidth: 0, wordBreak: 'break-word' }}>{err}</span>
                <button onClick={dismissErr} aria-label="關閉" style={dismissBtn}>✕</button>
              </div>
            )}
          </div>
        )}
        {/* 疑似搭車即時提醒 */}
        {vehicleWarn && status === 'tracking' && (
          <div style={{ position: 'absolute', left: 0, right: 0, top: 0, zIndex: 950, padding: '10px 12px 0', pointerEvents: 'none' }}>
            <div style={{ background: '#b46a00', color: '#fff', borderRadius: 10, padding: '10px 12px', fontSize: 13, boxShadow: '0 4px 16px rgba(0,0,0,.4)', lineHeight: 1.5 }}>
              🚗 偵測到疑似搭乘車輛的速度（超過人體極限）——這段不列入有效里程與課表進度、也不觸發事件；整趟過快將標記待審、不發獎勵
            </div>
          </div>
        )}
        {activeEvent?.phase === 'active' && (
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
            <div data-skin="default" style={{ position: 'absolute', left: 0, right: 0, top: 0, zIndex: 1001, margin: '10px 12px 0', background: '#0b0e13', border: '1px solid rgba(255,194,75,.6)', borderRadius: 12, padding: '12px 14px', boxShadow: '0 6px 24px rgba(0,0,0,.55)' }}>
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
                <button onClick={() => joinRace(raceInvite)} style={{ flex: 1, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '9px', fontSize: 14, cursor: 'pointer' }}>加入一起跑</button>
                <button onClick={() => setRaceInvite(null)} style={{ background: 'transparent', color: 'var(--tx-faint)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '9px 14px', fontSize: 13, cursor: 'pointer' }}>略過</button>
              </div>
            </div>
          )
        })()}

        {/* 資訊面板（可拖曳）：收合只露出把手＋四格數據，上拉展開看更多（打卡/分段/結果），下拉看更多地圖 */}
        <div style={{
          position: 'absolute', left: 0, right: 0, top: sheet.curY, bottom: 0,
          transition: !sheet.dragging && sheet.ready ? 'top .28s cubic-bezier(.22,.61,.36,1)' : 'none',
          opacity: sheet.ready ? 1 : 0,
          display: 'flex', flexDirection: 'column',
          background: 'var(--bg)', color: 'var(--tx)',
          borderTopLeftRadius: 18, borderTopRightRadius: 18,
          borderTop: '1px solid var(--line)', boxShadow: '0 -10px 30px rgba(0,0,0,.22)',
          zIndex: 500, userSelect: 'none', WebkitUserSelect: 'none',
        }}>
          {/* 把手 + 四格數據（收合時可見；此整區皆可拖曳） */}
          <div ref={sheet.peekRef} {...sheet.handlers}
               style={{ flexShrink: 0, padding: '8px 16px 12px', cursor: 'grab', touchAction: 'none' }}>
            <div style={{ width: 40, height: 5, borderRadius: 3, background: 'var(--line-2)', margin: '0 auto 12px' }} />
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 8 }}>
              <Big compact label="距離" value={distKm.toFixed(2)} unit="km" />
              <Big compact label="時間" value={fmtTime(elapsed)} unit="" />
              <Big compact label="平均配速" value={fmtPace(avgPace)} unit="/km" />
              <Big compact label="分段即時配速" value={fmtPace(segLivePace)} unit="/km" />
            </div>
            {/* 里程獎勵進度：每滿 1km 一份（本趟上限），即時看到距下一份還差多少 → 誘因持續跑 */}
            {mileageCfg && mileageCfg.per_km > 0 && (
              <div style={{ marginTop: 10, background: 'var(--bg-2)', borderRadius: 10, padding: '8px 11px' }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', fontSize: 11.5, marginBottom: 5 }}>
                  <span style={{ color: 'var(--tx-dim)' }}>里程獎勵 · 每滿 1km +{mileageCfg.per_km} EXP{mileageCfg.dp_per_km > 0 ? ` +${mileageCfg.dp_per_km} DP` : ''}</span>
                  <span style={{ fontWeight: 800, color: 'var(--fug)', fontVariantNumeric: 'tabular-nums' }}>本趟 {mEarned} 份</span>
                </div>
                <div style={{ height: 8, background: 'var(--line-2)', borderRadius: 999, overflow: 'hidden' }}>
                  <div style={{ height: '100%', width: `${Math.round(mFrac * 100)}%`, background: mAtCap ? 'var(--gold)' : 'var(--fug)', borderRadius: 999, transition: 'width .3s' }} />
                </div>
                <div style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 4 }}>
                  {mAtCap ? `已達本趟上限 ${mCap} km` : `距下一份還要 ${(1 - mFrac).toFixed(2)} km${mCap > 0 ? `（本趟上限 ${mCap} km）` : ''}`}
                </div>
              </div>
            )}
          </div>
          {/* 可捲動內容（展開時顯示） */}
          <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', touchAction: 'pan-y', padding: '2px 16px calc(20px + var(--cta-safe, 0px))' }}>
            {/* 個人任務課表（在滑動面板內，不蓋地圖）：閒置＝選課表(左右滑動輪播+●○○)；進行中/完成＝分段執行 HUD */}
            {status === 'idle' && woPhase === 'idle' && panel && (() => {
              const ac = panel.active_card
              const list = ac && !panel.cards.some((c) => c.task_id === ac.task_id) ? [ac, ...panel.cards] : panel.cards
              return <TrackTaskPanel cards={list} activeTaskId={workout?.taskId ?? ac?.task_id ?? null} busy={panelBusy} onChallenge={challengeCard} onAbandon={abandonActive} />
            })()}
            {(woPhase === 'running' || woPhase === 'done') && workout && (() => {
              const stepDist = Math.max(0, distRef.current - woStepStartRef.current.dist)
              const stepTime = Math.max(0, (Date.now() - woStepStartRef.current.time) / 1000)
              const livePace = stepDist > 5 ? stepTime / (stepDist / 1000) : 0
              return (
                <WorkoutHud title={workout.title} steps={workout.steps} stepIdx={woStepIdx}
                  stepDist={stepDist} stepTime={stepTime} livePaceS={livePace} hits={woHits}
                  phase={woPhase === 'done' ? 'done' : 'running'} result={woResult}
                  onRanking={workout.kind === 'explore' && !woResult?.flagged ? () => setRankingBoss({ id: workout.taskId, name: workout.title }) : undefined}
                  onClose={() => { setWoPhase('idle'); loadPanel() }} />
              )
            })()}
            {(status === 'idle' || status === 'tracking') && (
              curPos ? (
                <div style={{ fontSize: 11.5, marginBottom: 10, color: curPos.acc > MAX_ACC ? 'var(--hunt)' : 'var(--tx-faint)' }}>
                  <span className="skin-ico" data-ico="gps" aria-hidden>📶</span> GPS 精度 ±{Math.round(curPos.acc)}m{curPos.acc > MAX_ACC
                    ? (status === 'tracking' ? '（訊號弱，移動可能未計入 → 請到空曠處）' : '（訊號弱，建議到空曠處再開始）')
                    : '（正常）'}
                </div>
              ) : status === 'idle' ? (
                <div style={{ fontSize: 11.5, marginBottom: 10, color: 'var(--tx-faint)' }}><span className="skin-ico" data-ico="gps" aria-hidden>📶</span> GPS 偵測中…（首次進入請允許定位權限）</div>
              ) : null
            )}
            {anomalies > 0 && (
              <div style={{ fontSize: 11.5, marginBottom: 10, color: 'var(--tx-faint)' }}>⚠ 已濾除 {anomalies} 個 GPS 跳點（未計入距離）</div>
            )}
            {/* warn / err 已改為浮在面板上方的常駐提示（見地圖區），此處不再重複顯示 */}

        {/* 打卡點任務 */}
        {(checkpoints.length > 0 || exploreCps.length > 0) && (
          <div style={{ marginTop: 16 }}>
            <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 6 }}><span className="skin-ico" data-ico="pin" aria-hidden>📍</span> 打卡點任務</div>
            {cpMsg && <div style={{ fontSize: 12.5, color: 'var(--fug)', marginBottom: 8, wordBreak: 'break-word' }}>{cpMsg}</div>}
            {status !== 'tracking' && <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginBottom: 8 }}>走到打卡點附近，在範圍內按「打卡」即可（不需邊跑邊打卡）。{checkpoints.length > 0 && '（賽事打卡點若邊跑邊打卡有 GPS 軌跡佐證可免審核）'}</div>}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {/* 城市探索打卡點：清單只列 已揭露待挑戰 + 最近 10 筆未打卡（依縣市篩選＋距離排序） */}
              {exList.map((b) => {
                const d = exDist(b)
                const inRange = d != null && d <= (b.radius_m || 40)
                const busy = cpBusy === 'ex:' + b.id
                const title = b.discovered ? b.name : (b.place || '神秘打卡點')
                return (
                  <div key={'ex:' + b.id} style={{ background: 'var(--bg-2)', borderRadius: 'var(--radius-md, 10px)', padding: '10px 12px', display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 10, border: b.discovered ? '1px solid rgba(231,184,75,.45)' : '1px solid transparent' }}>
                    <div style={{ minWidth: 0 }}>
                      <div style={{ fontSize: 13.5, fontWeight: 700, color: 'var(--tx)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                        {b.discovered ? '⚔ ' : '📍 '}{title}
                        {b.discovered && b.difficulty_stars > 0 && <span style={{ color: 'var(--gold)', fontSize: 11, marginLeft: 6 }}>{'★'.repeat(b.difficulty_stars)}</span>}
                      </div>
                      <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                        城市探索{b.region ? ` · ${b.region}` : ''}
                        {d != null && !b.card_obtained && <> · {d < 1000 ? `還有 ${Math.round(d)}m` : `${(d / 1000).toFixed(1)}km`}</>}
                      </div>
                    </div>
                    {b.card_obtained ? (
                      <span style={{ color: 'var(--fug)', fontWeight: 700, fontSize: 13, flexShrink: 0 }}>✓ 已收服</span>
                    ) : b.discovered ? (
                      <button onClick={() => setBossPanel({ boss: b, phase: b.active ? 'start' : 'intro', dpCost: exDpCost(b) })}
                        style={{ flexShrink: 0, background: 'var(--gold)', color: '#fff', fontWeight: 800, border: 'none', borderRadius: 9, padding: '8px 14px', fontSize: 13, cursor: 'pointer' }}>
                        {b.active ? '▶ 繼續挑戰' : '⚔ 挑戰'}
                      </button>
                    ) : (
                      <button onClick={() => doExploreCheckin(b)} disabled={busy || (curPos != null && !inRange)}
                        style={{ flexShrink: 0, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '8px 14px', fontSize: 13, cursor: (busy || (curPos != null && !inRange)) ? 'default' : 'pointer', opacity: (busy || (curPos != null && !inRange)) ? 0.45 : 1 }}>
                        {busy ? '打卡中…' : curPos != null && !inRange ? '未到範圍' : '打卡'}
                      </button>
                    )}
                  </div>
                )
              })}
              {checkpoints.map((cp) => {
                const d = cpDist(cp)
                const inRange = d != null && d <= cp.radius_m
                const busy = cpBusy === cp.id
                const blocked = busy || (curPos != null && !inRange)
                return (
                  <div key={cp.id} style={{ background: 'var(--bg-2)', borderRadius: 'var(--radius-md, 10px)', padding: '10px 12px', display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 10 }}>
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
                        style={{ flexShrink: 0, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '8px 14px', fontSize: 13, cursor: blocked ? 'default' : 'pointer', opacity: blocked ? 0.45 : 1 }}>
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
          <div style={{ marginTop: 16, background: 'var(--bg-1)', border: `1px solid ${result.flagged ? 'rgba(255,90,90,.4)' : 'var(--line-2)'}`, borderRadius: 'var(--radius-lg, 12px)', padding: 14, boxShadow: 'var(--card-shadow, none)', wordBreak: 'break-word' }}>
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
          </div>
        </div>
      </div>

      {/* 操作 */}
      <div style={{ padding: '16px 16px calc(16px + var(--cta-safe, 0px))', flexShrink: 0, borderTop: '1px solid var(--line)', background: 'var(--bg)' }}>
        {status === 'idle' && (
          user
            ? (workout
                ? <button onClick={startWorkout} className="skin-btn-start" style={btn}>▶ 開始課表挑戰</button>
                : <button onClick={start} className="skin-btn-start" style={btn}>▶ 開始跑步</button>)
            : <button onClick={() => setShowLogin(true)} style={btn}>請先登入</button>
        )}
        {status === 'tracking' && <button onClick={requestFinish} className="skin-btn-end" style={{ ...btn, background: 'var(--hunt)', color: '#fff' }}>■ 結束並上傳</button>}
        {status === 'done' && <button onClick={() => { setStatus('idle'); setElapsed(0); setDistance(0); setSplits([]); setAnomalies(0) }} style={{ ...btn, background: 'var(--bg-2)', color: 'var(--tx)' }}>再跑一次</button>}
        {status === 'tracking' && <div className="track-blink" style={{ textAlign: 'center', fontSize: 12.5, fontWeight: 800, color: 'var(--hunt)', marginTop: 8, lineHeight: 1.5 }}>⚠️ 數據偵測中，請勿離開或關閉視窗！跑完請按「結束並上傳」{uploading ? '（上傳中…）' : ''}</div>}
      </div>

      {/* 關主挑戰面板（打卡揭露後跳出）*/}
      {bossPanel && (
        <BossChallengePanel
          boss={bossPanel.boss}
          phase={bossPanel.phase}
          busy={exploreBusy}
          dpCost={bossPanel.dpCost}
          note={status === 'tracking' ? '⚠ 請先結束目前的跑步，再開始關主挑戰（挑戰為獨立的追蹤紀錄）' : undefined}
          onAccept={acceptBoss}
          onDecline={() => setBossPanel(null)}
          onStart={startBossWorkout}
        />
      )}

      {/* 挑戰者成績排行覆蓋層 */}
      {rankingBoss && (
        <BossRankingPanel bossId={rankingBoss.id} bossName={rankingBoss.name} onClose={() => setRankingBoss(null)} />
      )}

      {/* 3★ 取卡恭喜彈窗 → 前往卡片圖鑑（帶 ?unlock 播翻轉解鎖特效）*/}
      {celebrateCard && (
        <CardUnlockCelebration
          name={celebrateCard.name}
          cardUrl={celebrateCard.cardUrl}
          onGallery={() => { window.location.href = '/?unlock=' + encodeURIComponent(celebrateCard.bossId) }}
          onClose={() => setCelebrateCard(null)}
        />
      )}
    </PhoneFrame>
   </GoogleAuthProvider>
  )
}

function Big({ label, value, unit, warn, compact }: { label: string; value: string; unit: string; warn?: boolean; compact?: boolean }) {
  return (
    <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 'var(--radius-md, 12px)', padding: compact ? '9px 6px' : '12px 14px', boxShadow: 'var(--card-shadow, none)', minWidth: 0 }}>
      <div style={{ fontSize: compact ? 10 : 11, color: 'var(--tx-faint)', whiteSpace: 'nowrap' }}>{label}</div>
      <div style={{ fontSize: compact ? 16 : 26, fontWeight: 900, color: warn ? 'var(--hunt)' : 'var(--tx)', fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
        {value}<span style={{ fontSize: compact ? 10 : 13, marginLeft: compact ? 2 : 3, color: 'var(--tx-dim)' }}>{unit}</span>
      </div>
    </div>
  )
}

const btn: React.CSSProperties = { width: '100%', background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 'var(--radius-btn, 12px)', padding: '15px 20px', fontSize: 16, cursor: 'pointer' }
const dismissBtn: React.CSSProperties = { background: 'rgba(255,255,255,.18)', border: 'none', color: '#fff', fontSize: 14, lineHeight: 1, cursor: 'pointer', padding: '4px 9px', borderRadius: 8, flexShrink: 0, fontWeight: 700 }
