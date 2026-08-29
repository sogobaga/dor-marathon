'use client'

import { useEffect, useMemo, useRef, useState, useCallback } from 'react'
import useSWR from 'swr'
import { activitiesApi, checkpointApi, routeApi, eventApi, eventRaceApi, mileageExpApi, personalTasksApi, exploreApi, profileApi, integrationsApi, racesApi, strategiesApi, runCheersApi, createRaceSocket, formatChallengeRule, formatChallengeProgress, type GpsPoint, type GpsRunResult, type ActiveCheckpoint, type EventDef, type RaceEventInvite, type GroupGoalProgressMsg, type GroupGoalReachedMsg, type CompleteEvidence, type MileageConfig, type PanelCard, type ExploreBoss, type MyActiveRace, type RaceStrategy } from '@/lib/api'
import { getUserToken, withUserAuth, useUser } from '@/lib/userAuth'
import WorkoutHud from '@/components/WorkoutHud'
import BossChallengePanel from '@/components/BossChallengePanel'
import BossRankingPanel from '@/components/BossRankingPanel'
import CardUnlockCelebration from '@/components/CardUnlockCelebration'
import TrackTaskPanel from '@/components/TrackTaskPanel'
import FreetrainIntroPanel from '@/components/FreetrainIntroPanel'
import { expandSegments, paceInBand, takeFreetrainWorkout, type WoStep } from '@/lib/workout'
import { resolveRunGoal, cheerPhaseAndRemain, fmtKm, type RunGoal } from '@/lib/runGoal'
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
import { initMovingState, advanceMovingState, classifyMoveSignal, currentMovingS, flushMovingState, classifyDistSignal, shouldCommitDist, MOVE_JUDGE_WINDOW_S, RETRO_WINDOW_S, type MovingState } from '@/lib/movingTime'
import { useDashboard } from '@/lib/useDashboard'
import RaceFocusMode from './RaceFocusMode'
import CheerShow from './CheerShow'

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

// 每公里鼓勵語內建 fallback（後台文案 API 失敗/池為空時使用）：{done}=已完成量、{remain}=剩餘量，見 lib/runGoal.ts
const CHEER_FALLBACK: { before: string[]; after: string[] } = {
  before: [
    '加油！你已經完成 {done} 了，保持這個節奏！',
    '很棒的表現，{done} 已經穩穩入袋，繼續前進！',
    '{done} 達成，身體正在熱起來，享受這股勁道！',
  ],
  after: [
    '撐住！只剩 {remain}，終點就在不遠處！',
    '進入最後階段，還剩 {remain}，你可以的！',
    '最後衝刺，剩下 {remain}，堅持就是勝利！',
  ],
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
  const [stravaPriority, setStravaPriority] = useState(false) // 里程優先來源＝Strava 且已連接：GPS 結束不自動上傳，先讓使用者選
  const [confirmStravaHold, setConfirmStravaHold] = useState<null | { km: number; mins: number; paceS: number }>(null)
  const [showLogin, setShowLogin] = useState(false)
  const [showActiveRaces, setShowActiveRaces] = useState(false) // 「進行中活動/賽事」面板開關
  const [showStartTip, setShowStartTip] = useState(false) // 從賽事詳情頁「前往挑戰」進入（?from=race）→ idle 時顯示一次性新手提醒，可點擊/X關閉
  const [uploading, setUploading] = useState(false)
  const [checkpoints, setCheckpoints] = useState<ActiveCheckpoint[]>([])
  const [curPos, setCurPos] = useState<{ lat: number; lng: number; acc: number } | null>(null)
  const curPosRef = useRef<{ lat: number; lng: number; acc: number } | null>(null); curPosRef.current = curPos // 供 marker click 等閉包讀最新位置（避免 stale）
  const [routePlan, setRoutePlan] = useState<{ toName: string; km: number; etaMin: number } | null>(null) // 建議跑步路線資訊條
  const [routeBusy, setRouteBusy] = useState(false)
  const routeLineRef = useRef<any>(null) // 建議路線 polyline（虛線橘）
  const [cpBusy, setCpBusy] = useState('') // 正在打卡的 checkpoint id
  const [cpMsg, setCpMsg] = useState('')
  const [exChecked, setExChecked] = useState<Set<string>>(new Set()) // 本 session 已成功打卡的城市探索點 id（重進頁/重抓列表後回復，冷卻後仍能再打）
  // 事件任務（日常隨機）
  const [eventDefs, setEventDefs] = useState<EventDef[]>([])
  const [activeEvent, setActiveEvent] = useState<ActiveEvent | null>(null)
  const [eventMoved, setEventMoved] = useState(0)
  const [eventResult, setEventResult] = useState<EventResult | null>(null)
  const [mapReady, setMapReady] = useState(false)
  // 賽事多人連動事件（Phase B）
  const [raceInvite, setRaceInvite] = useState<RaceEventInvite | null>(null)
  const [inviteNow, setInviteNow] = useState(0) // 驅動邀請倒數重繪
  // Phase B2：共享累積目標（collective）進行中時的即時進度（供 EventBanner 渲染群體進度條）
  const [raceGroupProgress, setRaceGroupProgress] = useState<{ instanceId: string; current: number; target: number; participants: number } | null>(null)
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
  // 面板從 top:curY 蓋到底 → 可視地圖區＝上方 [0, curY]；用 ref 讓 onPos/recenter 等閉包讀最新值（避免 stale）
  const sheetHRef = useRef(0); sheetHRef.current = sheet.H     // 地圖容器總高
  const sheetYRef = useRef(0); sheetYRef.current = sheet.curY  // 面板頂端 y（＝可視地圖區高度）
  const followRef = useRef(true) // 地圖是否自動跟隨目前位置；使用者拖曳/縮放地圖後暫停，按「回到目前位置」恢復
  const zoomedToFixRef = useRef(false) // 第一次取得定位時要放大到本地 zoom（初始是全台俯視 zoom 7）；之後跟隨只平移、保留使用者縮放
  const [following, setFollowing] = useState(true) // 驅動「回到目前位置」按鈕顯示
  const [autoLocating, setAutoLocating] = useState(false) // 進頁面「預熱」正在自動嘗試定位中（尚未拿到第一個座標/尚未逾時失敗）：期間顯示「定位中…」遮罩、隱藏「定位到我」CTA，避免使用者誤以為要手動按
  const [mileageCfg, setMileageCfg] = useState<MileageConfig | null>(null) // 里程獎勵設定（進度條/預覽）
  // 個人任務「結構化課表」執行（挑戰後帶到本頁跑）
  const [workout, setWorkout] = useState<{ taskId: string; title: string; steps: WoStep[]; kind: 'personal' | 'explore' | 'freetrain'; cardUrl?: string; freerunSec?: number } | null>(null)
  const freetrainRef = useRef(false) // 自主訓練：workout 已由 TrainingScreen 橋接載入、尚未開跑/結束——保護不被 loadPanel() 的「無進行中挑戰→清空」誤清掉
  const [exploreCps, setExploreCps] = useState<ExploreBoss[]>([]) // 城市探索打卡點（含座標）
  const [exDailyRemaining, setExDailyRemaining] = useState<number | null>(null) // 今日打卡剩餘次數（跨所有點）
  const [bossPanel, setBossPanel] = useState<{ boss: ExploreBoss; phase: 'intro' | 'start'; dpCost: number } | null>(null) // 打卡後跳出的關主挑戰面板
  const [rankingBoss, setRankingBoss] = useState<{ id: string; name: string } | null>(null) // 挑戰者成績排行覆蓋層
  const [celebrateCard, setCelebrateCard] = useState<{ bossId: string; name: string; cardUrl?: string } | null>(null) // 3★取卡恭喜彈窗
  const [exploreBusy, setExploreBusy] = useState(false)
  const [focusBoss, setFocusBoss] = useState<string | null>(null) // 「前往打卡」帶來的目標關主 id → 地圖定位到該打卡點
  const focusDoneRef = useRef(false)
  const [woPhase, setWoPhase] = useState<'idle' | 'countdown' | 'running' | 'done'>('idle')
  const [woStepIdx, setWoStepIdx] = useState(0)
  const [woHits, setWoHits] = useState<Record<number, boolean>>({}) // work 段 index → 是否達配速
  const [woResult, setWoResult] = useState<{ stars: number; reward_exp: number; reward_dp: number; flagged?: boolean; card_obtained?: boolean; time_s?: number } | null>(null)
  const [, setWoNow] = useState(0) // 驅動 HUD 每 0.5s 重繪
  const woStepIdxRef = useRef(0)
  const woStepStartRef = useRef<{ dist: number; time: number }>({ dist: 0, time: 0 }) // 目前分段起點（距離 m / 時間 ms）
  const woResultsRef = useRef<{ inBand: number; total: number; detail: any[] }>({ inBand: 0, total: 0, detail: [] })
  const woActiveRef = useRef(false) // 課表執行中：跑步引擎暫停隨機事件
  const vehicleLikeRef = useRef(false) // 即時偵測：近 45 秒配速快於人體極限（疑似搭車）
  const [panel, setPanel] = useState<{ cards: PanelCard[]; active_card: PanelCard | null } | null>(null) // 任務面板（各階段可挑戰課表）
  const [panelBusy, setPanelBusy] = useState('') // 面板挑戰處理中的 task_id
  // 比賽專注模式：?strategy=<id> 帶入的賽事策略（配速分段＋補給計劃）。載入成功→開跑前顯示小標示（可取消）；
  // 開跑後（status==='tracking'）交給 RaceFocusMode 疊層顯示大字資訊＋配速/補給提醒。載入失敗只提示、不擋跑步。
  const [raceStrategy, setRaceStrategy] = useState<RaceStrategy | null>(null)
  const [stratErr, setStratErr] = useState('')

  // 專注模式進度條的目標：strategy > 自主訓練 Free Run > 結構化課表全距離/全時間（純函式見 lib/runGoal.ts）。
  // runGoalRef：commitSeg 活在 onPos 的 useCallback 裡（deps=[ensureMap]，closure 凍結在首次 render），
  // 直接讀 runGoal 會拿到舊值，所以照全檔既有慣例（distRef 等）另存一份 ref 供 commitSeg 讀最新值。
  const runGoal: RunGoal = useMemo(() => resolveRunGoal(raceStrategy, workout), [raceStrategy, workout])
  const runGoalRef = useRef<RunGoal>(runGoal); runGoalRef.current = runGoal
  // 每公里鼓勵語（v1.1.663）：免登入文案池，失敗/為空時用內建 fallback（CHEER_FALLBACK）。
  const { data: cheerPoolRaw } = useSWR('run-cheers', () => runCheersApi.get(), { revalidateOnFocus: false, shouldRetryOnError: false })
  const cheerPoolRef = useRef<{ before: string[]; after: string[] }>(CHEER_FALLBACK)
  cheerPoolRef.current = {
    before: cheerPoolRaw?.before?.length ? cheerPoolRaw.before : CHEER_FALLBACK.before,
    after: cheerPoolRaw?.after?.length ? cheerPoolRaw.after : CHEER_FALLBACK.after,
  }
  // 應援表演升級（泡泡對話框+啦啦隊角色，v1.1.664）：顯示秒數吃系統設定 cheer_display_ms（DashboardInfo，
  // api.ts:1533），未設定/非正數 fallback 3000ms；存 ref 供 fireCheer 讀最新值（fireCheer 是具名函式宣告，
  // 只讀 ref，不受呼叫端 closure 是否為舊版影響，同檔既有慣例）。cheer_test_entry==='shown' 才顯示白名單
  // 測試按鈕（後台系統設定白名單開關，本頁不做任何權限判斷、只吃這顆旗標）。
  const { dash } = useDashboard()
  const cheerDurationRef = useRef(3000)
  cheerDurationRef.current = dash?.cheer_display_ms && dash.cheer_display_ms > 0 ? dash.cheer_display_ms : 3000
  const canTestCheer = dash?.cheer_test_entry === 'shown'
  const [cheer, setCheer] = useState<{ text: string; key: number } | null>(null) // 目前顯示的鼓勵語（cheerDurationRef 毫秒後自動清空）
  const cheerTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const lastCheerTextRef = useRef<string | null>(null) // 避免連續兩次抽到同一句
  const cheerKeyRef = useRef(0) // 遞增 key，讓連續兩句也能重新觸發淡入動畫
  const cheerTestCountRef = useRef(0) // 測試按鈕：遞增當假 km，讓有目標時能連續看到 50% 前後兩種文案

  const pointsRef = useRef<GpsPoint[]>([])
  const distRef = useRef(0)      // 有效距離（排除超速段）：顯示/里程/課表進度用
  const rawDistRef = useRef(0)   // 原始距離（含超速夾限）：僅供疑似搭車偵測，避免排除有效距離後偵測失效
  const splitMarkRef = useRef<number[]>([]) // 每跨整公里時的 elapsed 秒
  const startRef = useRef(0)
  // 配速基準：第一個 GPS 點的時間戳（GPS 鎖定後才開始計時），若尚無點則退回按鈕按下時間。
  // 修正「第一公里分段被 GPS 鎖定等待時間虛胖」的問題：前端 elapsed 從按鈕算，後端從第一個 GPS 點算，
  // 用 paceBaseMs() 統一前端所有配速計算的基準，使前後端保持一致。
  const paceBaseMs = () => pointsRef.current[0]?.t ?? startRef.current
  const watchRef = useRef<number | null>(null)
  const warmWatchRef = useRef<number | null>(null) // 進頁面時的 GPS 預熱偵測（顯示精度/定位地圖，不記錄）
  const wakeRef = useRef<any>(null)
  const timerRef = useRef<any>(null)
  const pingTimerRef = useRef<any>(null) // 跑步中心跳（後台「目前在跑名單」）
  const mapRef = useRef<any>(null)
  const lineRef = useRef<any>(null)
  const markRef = useRef<any>(null)
  const markShownRef = useRef(false) // 目前位置綠點是否已「加到地圖上」——只在拿到真實 GPS 定位後才顯示（避免預設中心=信義區冒出假定位點）
  const cpLayerRef = useRef<any>(null) // 地圖上的打卡點圖層
  const warnTimer = useRef<any>(null)
  const errTimerRef = useRef<any>(null) // 「軌跡太短」等暫時訊息的自動淡出計時
  const statusRef = useRef(status)
  statusRef.current = status
  const lastAccRef = useRef<GpsPoint | null>(null) // 上一個「採納」的點（過濾原地抖動用）
  const pendingRef = useRef<{ p: GpsPoint; d: number; at: number }[]>([]) // 距離防漂移：已採納但未 commit 的暫存段（狀態機靜止期間、有速度讀值的位移）；「靜止→移動」翻轉時回補最近 RETRO_WINDOW_S 秒內的，其餘老化丟棄
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
  // Phase B2：collective 貢獻節流——lastContributedDistRef 是「上次成功回報」時的 distRef 快照，
  // 每次只送出兩者差值（delta）；僅在請求成功時前移，失敗則保留待下次連同新位移一起重送（不遺漏、不重複）。
  const lastContributedDistRef = useRef(0)
  const lastContributeAtRef = useRef(0) // 節流：至少間隔 6 秒才送一次
  const contributeBusyRef = useRef(false) // 避免同一時間疊加送出多個請求

  const ensureMap = useCallback(async (lat: number, lng: number, zoom = 16) => {
    const L = await loadLeaflet()
    if (mapRef.current) return
    const map = L.map('gps-map', { zoomControl: true }).setView([lat, lng], zoom)
    L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', { maxZoom: 19, attribution: '© OpenStreetMap' }).addTo(map)
    lineRef.current = L.polyline([], { color: '#46E3A0', weight: 5 }).addTo(map)
    routeLineRef.current = L.polyline([], { color: '#FF8A3D', weight: 4, dashArray: '6 8', opacity: 0.9 }).addTo(map) // 建議路線（虛線橘）
    cpLayerRef.current = L.layerGroup().addTo(map)
    // 目前位置綠點：先建立但「不」加到地圖——在真正拿到 GPS 定位（onPos）時才 addTo，避免預設中心(信義區)冒出假定位點
    markRef.current = L.circleMarker([lat, lng], { radius: 7, color: '#fff', fillColor: '#46E3A0', fillOpacity: 1, weight: 2 })
    // 使用者手動拖曳/縮放地圖 → 暫停自動跟隨（否則每次 GPS 更新都會把畫面拉回目前位置，無法看前方路線）
    map.on('dragstart zoomstart', () => { if (followRef.current) { followRef.current = false; setFollowing(false) } })
    mapRef.current = map
    setMapReady(true)
  }, [])

  // 把某座標置中到「可視地圖區」的中心（＝扣掉底部資訊面板遮住的部分後、實際看得到的區域中心），
  // 而非整個地圖容器的幾何中心（那會被面板遮住、看起來偏下）。作法：面板頂端在 y=curY，可視區為 [0,curY]，
  // 其中心在 y=curY/2＝比容器中心(H/2)高 (H-curY)/2 px → 把投影點往下推 (H-curY)/2 再置中，定位點就落在可視中心。
  // 只讀 ref → 即使是舊閉包也拿到最新面板高度；面板幾乎蓋滿(可視區<80px)時退回一般置中避免把點推出畫面。
  function centerMap(latlng: [number, number], zoom?: number) {
    const map = mapRef.current; if (!map) return
    const H = sheetHRef.current, visH = sheetYRef.current, z = zoom ?? map.getZoom()
    if (H <= 0 || visH < 80) { if (zoom != null) map.setView(latlng, z); else map.panTo(latlng); return }
    const pt = map.project(latlng, z); pt.y += (H - visH) / 2
    const c = map.unproject(pt, z)
    if (zoom != null) map.setView(c, z); else map.panTo(c)
  }

  // 每公里鼓勵語觸發：呼叫端＝commitSeg 的 while 迴圈（每跨一整公里呼叫一次）。km＝剛跨過的整公里數
  // （1-based），elapsedS＝commitSeg 內同一基準算好的 el（GPS 第一點起算秒數，與 splitMarkRef 同源）。
  // 用 function 宣告（非 const）→ 具名函式宣告會 hoist，onPos 的 useCallback（deps=[ensureMap]，closure
  // 凍結在首次 render）引用到的一定是這份、不受宣告先後順序影響；內部只讀 ref／呼叫穩定的 setState，
  // 所以即使 onPos 閉包本身是舊的，fireCheer 執行時讀到的仍是最新 runGoal/文案池。
  function fireCheer(km: number, elapsedS: number) {
    const { phase, remainText } = cheerPhaseAndRemain(runGoalRef.current, km, elapsedS)
    const pool = cheerPoolRef.current[phase]
    const list = pool.length ? pool : CHEER_FALLBACK[phase]
    if (!list.length) return
    let text = list[Math.floor(Math.random() * list.length)]
    if (list.length > 1 && text === lastCheerTextRef.current) {
      const others = list.filter((t) => t !== lastCheerTextRef.current)
      text = others[Math.floor(Math.random() * others.length)] ?? text
    }
    lastCheerTextRef.current = text
    const finalText = text.replace('{done}', `${fmtKm(km)} km`).replace('{remain}', remainText)
    cheerKeyRef.current += 1
    setCheer({ text: finalText, key: cheerKeyRef.current })
    try { navigator.vibrate?.([80, 60, 80]) } catch { /* 無此 API 就略過 */ }
    if (cheerTimerRef.current) clearTimeout(cheerTimerRef.current)
    cheerTimerRef.current = setTimeout(() => setCheer(null), cheerDurationRef.current) // 顯示秒數吃系統設定（cheerDurationRef，見上方宣告處）
  }
  useEffect(() => () => { if (cheerTimerRef.current) clearTimeout(cheerTimerRef.current) }, [])

  // 白名單測試按鈕（canTestCheer，見上方 cheer_test_entry 宣告處）：手動觸發一次應援演出，不需要真的
  // 跑到整公里。km 用遞增計數器模擬「第 1、2、3…公里」（第 1 次=1、第 2 次=2…），有目標時能連續看到
  // 50% 前後兩種文案分支；elapsedS 直接讀目前 elapsed，與正式觸發同一套 fireCheer 邏輯。
  function testCheer() {
    cheerTestCountRef.current += 1
    fireCheer(cheerTestCountRef.current, elapsed)
  }

  // #4 移動時間（排除靜止/抖動的「實際移動」時間）＋依它算的移動配速
  // 狀態機（movingAccumS 已結清秒數 + movingSince 目前移動段起點或 null）獨立於下面 onPos 的距離累積
  // 邏輯，判定門檻也各自獨立（見 lib/movingTime.ts 頂部說明）；lastMoveRef 是專供這個狀態機用的
  // 「上一個精度合格的點」，與距離累積用的 lastAccRef 分開更新節奏（不受距離 JITTER_MIN=6m 門檻限制）。
  const movingStateRef = useRef<MovingState>(initMovingState())
  const lastMoveRef = useRef<GpsPoint | null>(null)
  const [movingS, setMovingS] = useState(0) // 顯示值：由下方 250ms 本地 tick 與 onPos 共同用 currentMovingS() 重算，見兩處呼叫點
  const movingSplitMarkRef = useRef<number[]>([]) // 跨每公里當下的移動時間（與 splitMarkRef 同步，供移動分段配速）
  const onPos = useCallback((pos: GeolocationPosition) => {
    // speed：都卜勒速度（裝置直接量測，非位置差分——靜止時漂移「假位移」不會推高它，是防漂移的關鍵訊號）。
    // 缺失/非有限值一律正規化為 null，下游（classifyDistSignal/shouldCommitDist）以 null 走「速度缺失裝置」fallback。
    const spdRaw = pos.coords.speed
    const p: GpsPoint = { lat: pos.coords.latitude, lng: pos.coords.longitude, t: pos.timestamp, acc: pos.coords.accuracy ?? 0, speed: typeof spdRaw === 'number' && isFinite(spdRaw) ? spdRaw : null }
    setCurPos({ lat: p.lat, lng: p.lng, acc: p.acc })
    try { localStorage.setItem('dor:gps-authorized', '1') } catch { /* ignore */ } // 曾成功定位＝已授權；供 Safari(無 permissions.query) 回訪時判斷可否預熱定位
    ensureMap(p.lat, p.lng)
    // 標記與地圖永遠跟著「目前」位置（即時感），即使該點未被採納為距離
    if (markRef.current) { if (!markShownRef.current && mapRef.current) { try { markRef.current.addTo(mapRef.current); markShownRef.current = true } catch { /* ignore */ } } markRef.current.setLatLng([p.lat, p.lng]) }
    // 僅在「跟隨中」才回中（置中到可視地圖區、避開面板遮蔽）；使用者手動看地圖時不打斷
    if (mapRef.current && followRef.current) {
      // 第一次取得定位：放大到本地 zoom（初始是全台俯視 zoom 7，只平移會停在 7、看起來像「全台灣」）；之後跟隨只平移、保留使用者縮放
      if (!zoomedToFixRef.current) { zoomedToFixRef.current = true; centerMap([p.lat, p.lng], 16) }
      else centerMap([p.lat, p.lng])
    }
    if (statusRef.current !== 'tracking') return // 預熱階段（未開始跑步）：只顯示 GPS 精度＋地圖位置，不累積距離、不警告
    const goodAcc = p.acc === 0 || p.acc <= MAX_ACC
    if (!goodAcc) {
      setWarn(`GPS 訊號較弱（±${Math.round(p.acc)}m），移動可能未被記錄，請到較空曠處`)
      clearTimeout(warnTimer.current)
      warnTimer.current = setTimeout(() => setWarn(''), 4000)
    }

    // ---- 距離防漂移 helpers（設計說明見 lib/movingTime.ts「距離防漂移」段落）----
    // commitSeg：把一段已採納的位移正式計入——distRef、每公里分段、軌跡點/軌跡線。
    // pointsRef 只收 committed 點：上傳給後端的就是這批點，後端以同批點重算距離/分段，口徑自然一致
    // （後端零改動）；漂移暫存段未被回補就不會出現在軌跡與上傳資料裡。
    const commitSeg = (cp: GpsPoint, dSeg: number) => {
      distRef.current += dSeg
      setDistance(distRef.current)
      // 每公里分段（只在有效距離上前進）；el 用該點自己的 GPS 時間戳（回補時=暫存當下的點，非現在）
      const km = Math.floor(distRef.current / 1000)
      const el = (cp.t - paceBaseMs()) / 1000 // 從第一個 GPS 點算（非按鈕時間），與後端一致
      while (splitMarkRef.current.length < km) {
        const prevEl = splitMarkRef.current.length ? splitMarkRef.current[splitMarkRef.current.length - 1] : 0
        splitMarkRef.current.push(el)
        setSplits((s) => [...s, el - prevEl])
        // 整公里分段當下拍移動時間快照，供「移動分段配速」用（與 splitMarkRef 同步）
        movingSplitMarkRef.current.push(currentMovingS(movingStateRef.current, Date.now()))
        fireCheer(splitMarkRef.current.length, el) // 剛跨過的整公里數 + 同基準已耗秒數 → 每公里鼓勵語
      }
      pointsRef.current.push(cp)
      if (lineRef.current) lineRef.current.addLatLng([cp.lat, cp.lng])
    }
    // feedMoveSignal：所有「餵訊號進移動狀態機」的路徑共用（距離採納分流＋下方 lastMoveRef 判定鏈）。
    // 在「靜止→移動」翻轉那一刻（無論由哪條路徑觸發翻轉），把暫存段中最近 RETRO_WINDOW_S 秒內的
    // 按時間序回補 commit——解紅綠燈重啟/起跑暖機（遲滯未滿前的真位移）漏計；更早的即為漂移，直接丟棄。
    const feedMoveSignal = (signal: 'moving' | 'still', nowMs: number) => {
      const prev = movingStateRef.current
      movingStateRef.current = advanceMovingState(prev, signal, nowMs)
      setMovingS(currentMovingS(movingStateRef.current, nowMs)) // GPS 觸發當下先更新一次；兩次 GPS 之間由下方 250ms tick 補平滑
      if (prev.movingSince == null && movingStateRef.current.movingSince != null && pendingRef.current.length) {
        const cutoff = nowMs - RETRO_WINDOW_S * 1000
        const retro = pendingRef.current.filter((it) => it.at >= cutoff)
        pendingRef.current = []
        for (const it of retro) commitSeg(it.p, it.d) // push 順序即時間序
      }
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
            lastAccRef.current = p // 仍前進採納點（避免搭車結束後算出巨大跳段）
            pointsRef.current.push(p)
            if (lineRef.current) lineRef.current.addLatLng([p.lat, p.lng])
          } else {
            const nowMsD = Date.now()
            // 平時老化：剔除超過回補窗口的暫存段（防膨脹；它們已確定是漂移，不會再被回補）
            if (pendingRef.current.length) pendingRef.current = pendingRef.current.filter((it) => it.at >= nowMsD - RETRO_WINDOW_S * 1000)
            // ① 訊號分流（classifyDistSignal）：不再無條件餵 moving——speed<0.5 的採納段（漂移假位移）
            //    改餵 still，修「靜止 85 秒還累出 21 秒移動時間」的根因；死區/速度缺失維持現行餵 moving。
            feedMoveSignal(classifyDistSignal(p.speed), nowMsD)
            // ② commit 閘門（shouldCommitDist）：狀態機移動中、或該點速度缺失（fallback 現行行為）才立即
            //    commit；否則暫存，等「靜止→移動」翻轉時由 feedMoveSignal 回補最近 RETRO_WINDOW_S 秒內的段。
            if (shouldCommitDist(movingStateRef.current, p.speed)) commitSeg(p, seg)
            else pendingRef.current.push({ p, d: seg, at: nowMsD })
            lastAccRef.current = p // 永遠前進採納點（未 commit 也前進：維持既有「仍前進採納點」語意，避免漂移結束後算出巨大跳段）
          }
        }
      }
    }
    // #4 移動/靜止 狀態機：與上面的距離累積刻意各自獨立——距離用 JITTER_MIN=6m 抖動門檻＋MAX_ACC=65m
    // 精度門檻決定「這個點算不算數」；這裡改用速度(≥0.6m/s)＋動態位移(>max(2.5m,0.3×accuracy))雙門檻＋
    // 連續2筆遲滯才切換，避免「移動時間」沿用距離的寬鬆門檻而在原地休息/等紅燈時仍持續累加。
    // ⚠️ 判定視窗（修「移動時間卡住不動」的根因，見 lib/movingTime.ts 開頭的根因說明）：手機 GPS 常態
    // 以約 1Hz 回報，跑步單次回呼間位移只有 2-4m，套用上面的位移門檻幾乎必被判定為 still。改成「距上一個
    // 判定基準點(lastMoveRef) dt<MOVE_JUDGE_WINDOW_S(2.5秒) 的點先不判定、也不推進基準點」，讓真正拿去
    // classifyMoveSignal 的位移是 ≥2.5 秒的累積量（跑步 3m/s×2.5s=7.5m 能穿過門檻，靜止飄移在長視窗下
    // 等效速度被攤薄）；只有真的做了判定，才把 lastMoveRef 推進到本點，否則留給下一個點繼續累積位移。
    if (goodAcc) {
      const prevMove = lastMoveRef.current
      if (!prevMove) {
        lastMoveRef.current = p // 第一個精度合格的點：設為判定基準，不判定
      } else if ((p.t - prevMove.t) / 1000 >= MOVE_JUDGE_WINDOW_S) {
        const dMove = haversineM(prevMove, p)
        const signal = classifyMoveSignal(prevMove, p, dMove)
        if (signal) {
          const nowMs = Date.now() // 與 elapsed（Date.now()-startRef）同一牆鐘時間基準，兩個顯示數字才同步
          feedMoveSignal(signal, nowMs) // 共用餵訊號入口：這條路徑翻轉「靜止→移動」時同樣觸發暫存段回補
        }
        lastMoveRef.current = p // 只在真的做了判定後才推進基準點；dt<視窗的點留給下一筆繼續累積位移
      }
    }
    // 事件正式進行中（非演出階段）才更新即時位移（進度條）
    if (activeEventRef.current?.phase === 'active') setEventMoved(distRef.current - activeEventRef.current.triggerD)
    // 防當掉：暫存採納後的軌跡
    localStorage.setItem(LS_KEY, JSON.stringify({ start: startRef.current, points: pointsRef.current.slice(-2000) }))
  }, [ensureMap])
  const onPosRef = useRef(onPos); onPosRef.current = onPos
  // 進頁面即初始化地圖（不等 GPS）：GPS 權限未授權時「預熱定位」不會啟動（見下方 permissions.query 判斷），
  // 而地圖過去只由 onPos→ensureMap 觸發 → 未授權/未定位時「地圖整個不顯示」（v0.1.423 後的回歸）。
  // 這裡在掛載時先用預設中心把地圖畫出來（不請求 GPS，保留不主動跳權限的行為）；GPS 一有位置
  //（onPos→ensureMap 為 no-op）會自動把畫面/標記移到實際位置。
  useEffect(() => { ensureMap(23.8, 121.0, 7) }, [ensureMap]) // 中性視圖（台灣全島俯視、低 zoom）：避免無 GPS 時空白，也避免顯示看起來像真實定位的假地點（原本市政府座標會讓使用者誤以為已定位）；有 GPS 後 onPos→ensureMap 為 no-op，實際置中靠 onPos 內的 centerMap

  // 里程獎勵設定（進度條/預覽用）：進頁抓一次
  useEffect(() => {
    if (!getUserToken()) return
    withUserAuth((t) => mileageExpApi.config(t)).then(setMileageCfg).catch(() => {})
  }, [])

  // 里程優先來源＝Strava 且已連接 → GPS 結束不自動上傳，改為讓使用者選擇（避免與 Strava 同步重複而輸掉去重）
  useEffect(() => {
    const token = getUserToken(); if (!token) return
    Promise.all([
      profileApi.getMe(token).catch(() => null),
      integrationsApi.stravaStatus(token).catch(() => null),
    ]).then(([me, st]) => {
      setStravaPriority(me?.profile?.preferred_data_source === 'strava' && !!st?.connected)
    })
  }, [user?.id])

  // 進入頁面（idle）的 GPS「預熱」定位：立即把地圖/綠點移到目前位置，但不記錄距離。
  // ⚠️ iOS 上「所有」瀏覽器(含 Chrome/Edge/Firefox)底層都是 WebKit，permissions.query 對 geolocation
  //   「不可靠」——常回傳 state:'prompt'(不是拋錯、也不是 null)即使已設「允許」。所以不能單靠 perm.state 判斷。
  // 策略：① 明確 granted 或「曾成功定位過」旗標 → watch 持續預熱(不跳提示)；② 明確 denied 或「曾拒絕」旗標 → 不打擾；
  //   ③ 其餘未確定(perm=null 或 perm.state==='prompt')：非 iOS 且 perm 可靠偵測到「未授權」→ 尊重 #5 不主動跳(等按鈕/開始跑步)；
  //   iOS(perm 不可靠) → 進頁面做「一次」getCurrentPosition：設「允許」者靜默成功自動定位、設「詢問」者最多跳一次、
  //   明確拒絕(code1)記 'dor:gps-declined' 不再自動問(維持 #5 不重複騷擾)。成功一次後靠旗標升級為 watch 預熱。
  // 開始跑步時 start() 會同步關掉預熱、換成正式追蹤；離開 idle / 卸載時自動關閉（用 onPosRef 避免每次 render 重訂閱）。
  useEffect(() => {
    if (status !== 'idle') return
    if (typeof navigator === 'undefined' || !navigator.geolocation) return
    let cancelled = false
    let watchId: number | null = null
    const geoOpts = { enableHighAccuracy: true, maximumAge: 3000, timeout: 15000 }
    // 快取優先：接受最多 60 秒內的快取位置，能瞬間把地圖移到大致位置；隨後高精度 watch/一次性定位再收斂到精確點。
    // 錯誤一律忽略（不彈錯、不影響後續 watch/正式定位）。
    const quickOpts = { enableHighAccuracy: false, maximumAge: 60000, timeout: 8000 }
    const startWatch = () => {
      setAutoLocating(true)
      navigator.geolocation.getCurrentPosition((pos) => onPosRef.current(pos), () => { /* ignore：靠後面的 watch 收斂 */ }, quickOpts)
      watchId = navigator.geolocation.watchPosition(
        (pos) => { onPosRef.current(pos); setAutoLocating(false) },
        () => { setAutoLocating(false) /* 預熱失敗忽略；開始跑步時會在使用者手勢內再要求 */ },
        geoOpts,
      )
      warmWatchRef.current = watchId
    }
    ;(async () => {
      let perm: { state: string } | null = null
      try { perm = (await (navigator.permissions as any)?.query?.({ name: 'geolocation' })) ?? null } catch { perm = null }
      if (cancelled || statusRef.current !== 'idle') return
      let grantedBefore = false, declinedBefore = false
      try { grantedBefore = localStorage.getItem('dor:gps-authorized') === '1' } catch { /* ignore */ }
      try { declinedBefore = localStorage.getItem('dor:gps-declined') === '1' } catch { /* ignore */ }
      const ua = navigator.userAgent || ''
      const isIOS = /iP(hone|od|ad)/.test(ua) || ((navigator as any).platform === 'MacIntel' && (navigator as any).maxTouchPoints > 1) // iPad iOS13+ 會偽裝成 Mac
      if ((perm && perm.state === 'granted') || grantedBefore) { startWatch(); return } // ① 明確已授權 / 曾成功定位 → 持續預熱
      if ((perm && perm.state === 'denied') || declinedBefore) return                    // ② 明確拒絕 / 曾拒絕 → 不打擾
      // ③ 未確定（perm=null 或 perm.state==='prompt'）：
      if (!isIOS && perm) return // 非 iOS 且 perm 可靠偵測到「未授權」→ 維持 #5：進頁面不主動跳，等使用者按「定位到我」/「開始跑步」
      // iOS（permissions.query 不可靠）或完全偵測不到 → 進頁面做「一次」定位嘗試：允許者靜默成功自動定位、詢問者最多跳一次
      // geoOpts 換成快取優先參數，讓它盡快回、靜默成功（成功一樣經 onPos 設 'dor:gps-authorized'）
      setAutoLocating(true)
      navigator.geolocation.getCurrentPosition(
        (pos) => { if (!cancelled) { onPosRef.current(pos); setAutoLocating(false) } },
        (e) => { if (e?.code === 1) { try { localStorage.setItem('dor:gps-declined', '1') } catch { /* ignore */ } } setAutoLocating(false) },
        quickOpts,
      )
    })()
    return () => { cancelled = true; if (watchId != null) { try { navigator.geolocation.clearWatch(watchId) } catch { /* ignore */ } } warmWatchRef.current = null; setAutoLocating(false) }
  }, [status])

  // 建立/重建正式追蹤的 geolocation watch（start() 與「回前景重接」共用）。先清舊 watch 再建新，
  // 避免切背景被系統暫停後留下已死的 watch（#6：切背景/切 App 不強制結束、回前景自動重新接上 GPS）。
  function acquireWatch() {
    if (typeof navigator === 'undefined' || !navigator.geolocation) return
    if (watchRef.current != null) { try { navigator.geolocation.clearWatch(watchRef.current) } catch { /* ignore */ } }
    watchRef.current = navigator.geolocation.watchPosition(
      (pos) => onPosRef.current(pos),
      (e) => {
        if (e.code === 1) setErr('定位權限被拒：Safari 跳出詢問時請按「允許」；若沒跳出，到 設定 → Apps → Safari → 位置 設為「詢問」或「允許」後重試。')
        else if (e.code === 3) setErr('定位逾時，請到較空曠處再試（室內 GPS 收訊較差）。')
        else setErr('定位失敗：' + (e.message || '請確認已開啟定位'))
      },
      { enableHighAccuracy: true, maximumAge: 0, timeout: 20000 },
    )
  }

  async function acquireWake() {
    try { wakeRef.current = await (navigator as any).wakeLock?.request('screen') } catch { /* ignore */ }
  }

  // 「回到目前位置」：恢復自動跟隨並置中。若尚無定位（idle 未預熱／Safari／權限未知）→ 在使用者手勢內
  // 請求一次定位（此時可跳原生權限提示，屬使用者主動點擊、不違反「不主動跳權限」）。onPos 會 setCurPos＋置中＋記旗標。
  function recenterMap() {
    followRef.current = true; setFollowing(true)
    const cp = curPosRef.current
    if (cp && mapRef.current) { centerMap([cp.lat, cp.lng]); return }
    if (typeof navigator === 'undefined' || !navigator.geolocation) return
    navigator.geolocation.getCurrentPosition(
      (pos) => onPosRef.current(pos),
      (e) => { setErr(e?.code === 1 ? '需要定位權限才能定位到你的位置，請允許後再試。' : '無法取得目前位置，請到較空曠處再試。') },
      { enableHighAccuracy: true, timeout: 15000, maximumAge: 3000 },
    )
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
    setActiveEvent(null); setEventMoved(0); setShowFlash(false); setRaceGroupProgress(null)
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
  // 收到多人事件邀請 / 共享進度更新 / 達標結算（任一綁定賽事的 WS 都走這裡）
  function onRaceMsg(ev: MessageEvent) {
    try {
      const msg = JSON.parse(ev.data)
      if (msg.type === 'event_race_invite') {
        const p = msg.payload as RaceEventInvite
        if (!user?.id || !p.target_user_ids?.includes(user.id)) return
        if (activeEventRef.current || raceInviteRef.current || armingRef.current || woActiveRef.current) return // 一次一任務（課表進行中也不插隊）
        if (Date.now() > p.join_deadline) return
        setRaceInvite(p)
        setShowFlash(true); playEventAlarm(); vibrate([120, 80, 120, 80, 120]) // 多人邀請：同樣全螢幕紅閃 + 噹噹噹（引注意），仍走加入/略過流程
        return
      }
      // Phase B2：以下兩則只在「我目前正參加的就是這個共享事件」時才處理（依 instance_id 對應 activeEvent，避免處理到無關賽局的廣播）
      if (msg.type === 'group_goal_progress') {
        const p = msg.payload as GroupGoalProgressMsg
        const ae = activeEventRef.current
        if (!ae || ae.raceInstanceId !== p.instance_id) return
        setRaceGroupProgress({ instanceId: p.instance_id, current: p.current, target: p.target, participants: p.participants })
        return
      }
      if (msg.type === 'group_goal_reached') {
        const p = msg.payload as GroupGoalReachedMsg
        const ae = activeEventRef.current
        if (!ae || ae.raceInstanceId !== p.instance_id) return // 我沒加入這個共享事件（或已離開/逾時）：與我無關，忽略
        clearEvent(); lastEventEndRef.current = Date.now(); rollNextEvent() // 清掉貢獻迴圈（下個 evalTick 見 ae=null 即不再回報）
        playEventComplete(); vibrate([90, 50, 90])
        setEventResult({ status: 'completed', def: ae.def, reward_exp: p.reward_exp, reward_dp: p.reward_dp })
      }
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
      // mode 以 join 回應為準（伺服器可能因 goal_target 未設定而把 collective 退化為 individual）；inv.mode 僅供回應缺欄位時保底
      const raceMode: 'individual' | 'collective' = (res.mode ?? inv.mode) === 'collective' ? 'collective' : 'individual'
      // Phase B：加入即開始（保留多人同步節奏，不插 321）→ 直接 active 交既有引擎
      armIdRef.current = ++armSeqRef.current
      const ae: ActiveEvent = { def, occId: '', raceInstanceId: inv.instance_id, raceMode, triggerD: distRef.current, triggerT: now, readyUntil: now, deadline, baseSpk, phase: 'active' }
      activeEventRef.current = ae; setActiveEvent(ae); setEventMoved(0); setEventResult(null); setShowFlash(false)
      // collective：貢獻節流基準點對齊加入當下的距離，避免把「加入前」的移動也算進貢獻
      lastContributedDistRef.current = distRef.current
      lastContributeAtRef.current = 0
      // collective：加入當下就把共享進度條顯示出來(current/target)，讓玩家立刻看到目標與現況，
      // 不必等第一次成功貢獻（GPS 需累積≥1m）才出現；之後由 contribute 回應/WS 廣播即時更新人數與進度。
      if (raceMode === 'collective') {
        setRaceGroupProgress({ instanceId: inv.instance_id, current: res.current ?? 0, target: res.goal_target ?? 0, participants: 1 })
      } else {
        setRaceGroupProgress(null)
      }
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
    // 測試：跑步中、無進行中事件時輪詢認領後台手動觸發（每 30 秒；結算中不認領）
    // 效能優化：跑步中 95% 時間空轉；1000 人同時跑步時此輪詢是全站最大宗請求流量（5s 間隔約 200 req/s）
    // 拉長到 30 秒後降到約 33 req/s；管理員手動推送事件最晚 30 秒內會被領取，可接受。
    // 賽事模式（raceStrategy 已載入）：與下方隨機事件觸發同理抑制——手動認領也是「事件任務」引擎的另一個
    // 觸發入口（同一套 ActiveEvent/EventInteraction 全螢幕演出），賽事進行需要專注，不應被任何來源的新
    // 事件打斷（使用者規格，見任務規格 C）。
    if (!raceStrategy && !activeEventRef.current && !completingRef.current && now - lastClaimRef.current > 30000) {
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
      if (ae.raceMode === 'collective' && ae.raceInstanceId) {
        // Phase B2：collective 完成完全由後端 contribute+settle 驅動（RaceComplete 會拒絕 collective 實例，
        // 見 event_race.go "use_collective_contribute"），這裡只節流回報位移量，不跑下方 individual 的判定分支。
        if (now >= ae.deadline) { failEvent(ae); return } // 共享視窗已過仍未達標：比照 individual 逾時失敗（達標則已由 WS group_goal_reached 先行收尾）
        const delta = distRef.current - lastContributedDistRef.current
        if (delta >= 1 && !contributeBusyRef.current && now - lastContributeAtRef.current >= 6000) {
          lastContributeAtRef.current = now
          contributeBusyRef.current = true
          const token = getUserToken()
          const instId = ae.raceInstanceId
          const sendDelta = delta
          if (token) {
            eventRaceApi.contribute(token, instId, sendDelta)
              .then((r) => {
                lastContributedDistRef.current += sendDelta // 只在成功時前移基準點，失敗保留待下次連同新位移一起重送
                setRaceGroupProgress({ instanceId: instId, current: r.current, target: r.target, participants: r.participants })
              })
              .catch(() => { /* 忽略：下次 tick 用累積中的更大 delta 重試；already_reached/window_closed 等終態由 WS 收尾 */ })
              .finally(() => { contributeBusyRef.current = false })
          } else contributeBusyRef.current = false
        }
        return
      }
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
    // 賽事模式（已載入賽事策略 raceStrategy，見 RaceStrategyTab「啟動賽事模式」帶 ?strategy=<id> 進來）
    // 抑制「事件任務」的新觸發：賽事進行需要專注在配速/補給，GPS 即時觸發的隨機任務全螢幕紅閃演出會打斷
    // 比賽節奏，這是使用者明確規格（見任務規格 C）。只抑制「新事件的觸發」（這裡 activeEventRef 必為
    // null，不存在「事件已在進行中」的情況）；claimManualEvent（後台手動測試認領）與 Phase B 多人賽事
    // 邀請走各自獨立的機制，不受此處影響。raceStrategy 在追蹤期間不會變動（見 setRaceStrategy 呼叫點：
    // 一次為 idle 時載入、一次為 idle 時取消策略，皆早於 start()），這裡讀取一般 state 而非 ref 是安全的。
    if (raceStrategy) return
    // 事件間距＝「隨機等待 nextEventAtRef([最短,最長])」＋伺服器防濫用地板(taskGateOpen)決定。
    // 舊的 per-def cooldown_sec 是「寫死 15 分鐘冷卻」的殘留：第一個事件時 lastEventEndRef=0 剛好不擋，
    // 但事件結束後 lastEventEndRef 變成真時間，會把「所有」def 擋掉整趟（cooldown 越大擋越久）→ 第二個事件永遠不觸發。移除之。
    const eligible = eventDefsRef.current.filter((d) => triggerEligible(d))
    if (eligible.length > 0) armEvent(pickWeighted(eligible))
    else rollNextEvent(false, 0.5) // 計時到但此刻無事件符合條件 → 用一半等待較快重試（不整趟卡死、也不每秒狂試）
  }

  function start() {
    setShowStartTip(false) // 開跑後新手提醒的任務已完成，收起（idle 狀態本就已用 status 條件隱藏，這裡一併重置狀態避免「再跑一次」時殘留）
    setErr(''); clearTimeout(errTimerRef.current); setErrFade(false)
    if (!navigator.geolocation) { setErr('此裝置/瀏覽器不支援定位'); return }
    // 關掉進頁面的 GPS 預熱偵測，避免與正式追蹤重複回報
    if (warmWatchRef.current != null) { try { navigator.geolocation.clearWatch(warmWatchRef.current) } catch { /* ignore */ } warmWatchRef.current = null }
    pointsRef.current = []; distRef.current = 0; rawDistRef.current = 0; splitMarkRef.current = []; lastAccRef.current = null; movingSplitMarkRef.current = []
    movingStateRef.current = initMovingState(); lastMoveRef.current = null // #4 移動時間狀態機重置（見 lib/movingTime.ts）
    pendingRef.current = [] // 距離防漂移：清掉上一趟未回補的暫存段
    setDistance(0); setElapsed(0); setSplits([]); setAnomalies(0); setResult(null); setMovingS(0)
    // 每公里鼓勵語重置：避免上一趟結束前顯示中的句子/去重記憶殘留到這一趟
    setCheer(null); lastCheerTextRef.current = null; if (cheerTimerRef.current) clearTimeout(cheerTimerRef.current)
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
    // Phase B2 重置（collective 貢獻節流）
    setRaceGroupProgress(null); lastContributedDistRef.current = 0; lastContributeAtRef.current = 0; contributeBusyRef.current = false
    startRef.current = Date.now()
    setStatus('tracking')
    unlockAudio() // 在使用者手勢內解鎖音訊（iOS 必須）
    connectRaceWS() // 連 WS 監聽多人事件（不 await；失敗不影響跑步）
    // ⚠️ iOS：定位權限提示必須在使用者手勢「同步」流程內直接請求，不能先 await 任何東西
    //（否則會失去使用者手勢 → Safari 直接判定拒絕，code 1）。acquireWatch() 為同步呼叫，符合此要求。
    acquireWatch()
    timerRef.current = setInterval(() => {
      const now = Date.now()
      setElapsed((now - startRef.current) / 1000)
      // #4 移動時間顯示改由本地節拍驅動（原本只在 GPS onPos 回呼時前進，因 watchPosition 觸發頻率不定而
      // 一段一段跳）；GPS 只負責透過 movingStateRef 判定/切換移動/靜止狀態，這裡每 250ms 用目前狀態重算
      // 顯示值（見 currentMovingS），暫停中 movingSince=null 值會維持不動，移動中則隨牆鐘時間平滑前進，
      // 與「全程時間」同等平滑；RaceFocusMode 的補給倒數/移動配速吃同一個 movingS，同步受益。
      setMovingS(currentMovingS(movingStateRef.current, now))
    }, 250)
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

  // 單一登入：被踢下線／refresh 失敗／手動登出都會經 clearUserSession() emit 這個事件。
  // 若當下正在跑步且已無 token（已登出）→ 停跑但「保留」軌跡待上傳：只 cleanup + 收掉可能殘留的覆蓋層，
  // 絕不呼叫 finish()/doUploadGps()/清 LS_KEY——每個 GPS 點已即時寫入 localStorage(:264)，
  // 下次登入回來由 ProfileScreen/本頁的「上次未上傳的跑步」偵測（:912-925）撿回上傳。
  useEffect(() => {
    const onAuthChanged = () => {
      if (statusRef.current !== 'tracking') return // 只處理「跑步中」被登出的情況
      if (getUserToken()) return // 還有 token：不是登出（例如剛登入/refresh 成功），略過
      cleanup()
      setStatus('done')
      setConfirmStravaHold(null) // 收掉可能殘留的 Strava 三選一彈窗
      clearEvent() // 收掉進行中的事件演出/旗幟（含 setShowFlash(false)）
      setConfirmEnd(false) // 收掉「結束前確認」彈窗
      if (woActiveRef.current) { woActiveRef.current = false; freetrainRef.current = false; setWoPhase('idle') } // 收掉課表 HUD，避免殘留蓋在「已結束」畫面上
      setErr('已在其他裝置登入，本次跑步已保留，請重新登入後於運動數據頁上傳')
    }
    window.addEventListener('dor-auth-changed', onAuthChanged)
    return () => window.removeEventListener('dor-auth-changed', onAuthChanged)
  }, [cleanup]) // eslint-disable-line react-hooks/exhaustive-deps

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
    if (woActiveRef.current) { woActiveRef.current = false; freetrainRef.current = false; setWoPhase('idle') } // 課表中途結束：停止逐段驅動（挑戰仍保留，可再進來續挑）
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
  // 面板停靠點改變（半展/全展/收合）→ 可視地圖區高度變了 → 若正在跟隨且已定位，立即把定位點重新置中到新的可視中心
  useEffect(() => { if (mapReady && followRef.current) { const cp = curPosRef.current; if (cp) centerMap([cp.lat, cp.lng]) } }, [sheet.snap, mapReady]) // eslint-disable-line react-hooks/exhaustive-deps
  // 載入效果覆寫（正式圖片/音檔）：圖片給互動層、音效交給 sfx 解碼
  useEffect(() => { const t = getUserToken(); if (t) loadEffectAssets(t).then(setFxAssets) }, [user?.id])
  function toggleMute() { const next = !isMuted(); sfxSetMuted(next); setMuted(next); if (!next) unlockAudio() }

  async function doUploadGps(pts: GpsPoint[]): Promise<GpsRunResult | null> {
    setUploading(true)
    try {
      const { result } = await withUserAuth((t) => activitiesApi.uploadGps(t, {
        started_at: new Date(startRef.current).toISOString(),
        ended_at: new Date(pts[pts.length - 1].t).toISOString(),
        points: pts,
      }))
      setResult(result)
      // 結束後以後端分段為單一真相：後端由軌跡重算、可信，且與 avg_pace_s 同源。
      // 覆寫本地即時分段（可能因 paceBaseMs 時間差而略有誤差），讓結束畫面「分段」與「均配速」一致。
      if (result.km_paces?.length) setSplits(result.km_paces)
      localStorage.removeItem(LS_KEY)
      return result
    } catch (e: any) {
      setErr(e?.message || '上傳失敗')
      return null
    } finally { setUploading(false) }
  }

  // allowStravaHold：僅「自由跑結束」允許偏好 Strava 時暫緩上傳讓使用者選；
  // 課表挑戰（finishWorkout）必須直接上傳——課表成績要靠 GPS 防弊/best_time，且不可讓 Strava 彈窗蓋掉 WorkoutHud 收服演出。
  async function finish(allowStravaHold = true): Promise<GpsRunResult | null> {
    const pts = pointsRef.current
    // 偏好 Strava 的自由跑：先「不」cleanup、「不」setStatus('done')——GPS 保持追蹤，只跳彈窗讓使用者三選一
    // （直接使用本次數據／前往確認數據／繼續進行跑步）。這樣「繼續進行跑步」才能真的接續，不會被結束掉；
    // 收尾（cleanup+setStatus+上傳/導頁）由彈窗各按鈕決定。軌跡太短/未登入則落到下面一般流程。
    if (allowStravaHold && stravaPriority && pts.length >= 2 && getUserToken()) {
      let m = 0; for (let i = 1; i < pts.length; i++) m += haversineM(pts[i - 1], pts[i])
      const km = Math.round(m / 10) / 100
      const durS = Math.max(1, Math.round((pts[pts.length - 1].t - startRef.current) / 1000))
      setConfirmStravaHold({ km, mins: Math.round(durS / 60), paceS: km > 0 ? Math.round(durS / km) : 0 })
      return null
    }
    cleanup()
    setStatus('done')
    if (pts.length < 2) { flashErr('軌跡太短，未上傳'); localStorage.removeItem(LS_KEY); return null }
    const token = getUserToken()
    if (!token) { setErr('未登入，無法上傳'); return null }
    return doUploadGps(pts)
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

  // 使用者「主動」導頁（如彈窗按「前往確認數據」，此時 GPS 仍在 tracking）時抑制 beforeunload 警告；
  // 只有跑步中的「意外」離開/關窗才攔截。
  const leavingRef = useRef(false)
  // 跑步進行中若嘗試離開/關閉視窗 → native 攔截提示，避免誤觸中斷整趟
  useEffect(() => {
    if (status !== 'tracking') return
    const h = (e: BeforeUnloadEvent) => { if (leavingRef.current) return; e.preventDefault(); e.returnValue = '' }
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
      } else if (!woActiveRef.current && !freetrainRef.current) {
        setWorkout(null)
      }
    } catch { /* ignore */ }
  }, [])
  useEffect(() => { loadPanel() }, [loadPanel])

  // 自主訓練：進頁偵測 TrainingScreen 橋接帶來的一次性課表（sessionStorage）→ 進就緒，等使用者按「開始」
  useEffect(() => {
    const wo = takeFreetrainWorkout()
    if (!wo) return
    freetrainRef.current = true
    // Free Run（freerun 旗標）：steps 仍由 expandSegments 產生單一 time 段，沿用既有逐段驅動引擎；
    // freerunSec 只多帶給 HUD/介紹面板做「時:分」倒數顯示用，不影響狀態機。
    setWorkout({ taskId: wo.code || 'freetrain', title: wo.name || '自主訓練', steps: expandSegments(wo.segments), kind: 'freetrain', freerunSec: wo.freerun ? wo.freerunSec : undefined })
  }, [])

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
  // 啟動一份課表（個人任務／關主挑戰／自主訓練共用）。須在使用者手勢內呼叫（start() 會請求定位權限）
  function beginWorkout(wo: { taskId: string; title: string; steps: WoStep[]; kind: 'personal' | 'explore' | 'freetrain'; cardUrl?: string; freerunSec?: number }) {
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
    freetrainRef.current = false
    setWoPhase('done')
    // 課表達標即結算成績、顯示收服演出——但「不結束整趟 GPS」：不呼叫 finish()（不 cleanup、不 setStatus('done')、
    // 不上傳），status 保持 'tracking'、GPS 繼續累積里程，讓使用者可續跑；要結束整趟(上傳)按底部「結束並上傳」即可。
    // 防弊改由「配速達標(work_in_band)」本身把關（載具配速不對拿不到星）；原本綁在 GPS upload 的 flagged 閘門與
    // 關主 best_time 在此脫鉤（使用者拍板 A 方案：達標即結算、可續跑）。
    // 自主訓練：不發任何額外獎勵/星數（里程 EXP 由整趟上傳後的 activity_queue 自動發）。
    if (workout?.kind === 'freetrain') {
      // Free Run 歸零回饋：輕震動 + 完成音，只對 Free Run（有 freerunSec）觸發，不影響一般結構化課表。
      if (workout.freerunSec) { vibrate([90, 50, 90]); playEventComplete() }
      setWoResult({ stars: 0, reward_exp: 0, reward_dp: 0 }); return
    }
    const res = woResultsRef.current
    const token = getUserToken()
    try {
      if (token && workout) {
        if (workout.kind === 'explore') {
          // 關主挑戰：回報 → 得星、3★ 取得卡片；刷新探索列表（收服狀態）
          const r = await withUserAuth((t) => exploreApi.complete(t, workout.taskId, { finished: true, work_in_band: res.inBand, work_total: res.total }))
          setWoResult({ stars: r.stars, reward_exp: r.reward_exp, reward_dp: r.reward_dp, card_obtained: r.card_obtained, time_s: r.time_s })
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
    const onVis = () => {
      // 回前景且仍在跑：重取 wake lock + 重新接上 GPS watch（背景時可能已被系統暫停）。
      // 切背景不做任何「結束跑步」的動作（沿用既有設計，不影響 GPS watch/計時器/事件引擎）。
      if (document.visibilityState === 'visible' && statusRef.current === 'tracking') { acquireWake(); acquireWatch() }
      // #4 切背景：GPS 回呼隨時可能被系統整段暫停，若此刻正判定為「移動中」，主動把目前這段立即結清進
      // movingAccumS（繞過遲滯，見 flushMovingState）；避免斷流期間的牆鐘時間被回前景後某一筆遲來的訊號
      // 整段誤算成移動時間。回前景後由新的 GPS 訊號（onPos → advanceMovingState）重新判定要不要恢復移動，
      // 不回溯補秒。這只新增「凍結移動時間」這一件事，不改動既有的 GPS 重連機制本身。
      else if (document.visibilityState === 'hidden' && statusRef.current === 'tracking') {
        const now = Date.now()
        movingStateRef.current = flushMovingState(movingStateRef.current, now)
        setMovingS(currentMovingS(movingStateRef.current, now))
      }
    }
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
        bindRoutePopup(L, L.circleMarker([cp.lat, cp.lng], { radius: 6, color: '#fff', weight: 2, fillColor: color, fillOpacity: 1 }).addTo(layer).bindTooltip(cp.title || '打卡點'), cp.lat, cp.lng, cp.title || '打卡點') // 點打卡點 → 彈出「路線規劃」按鈕
      })
      // 城市探索打卡點：畫在最上層、較醒目（紫=未探索神秘/金=已揭露/綠=已收服）；目標關主放大+常駐標籤
      exploreCps.forEach((b) => {
        const color = b.card_obtained ? '#46E3A0' : b.discovered ? '#E7B84B' : '#C77DFF'
        const isFocus = b.id === focusBoss
        L.circle([b.lat, b.lng], { radius: b.radius_m || 40, color, weight: isFocus ? 3 : 1.5, fillOpacity: isFocus ? 0.25 : 0.12, dashArray: '4 4' }).addTo(layer)
        bindRoutePopup(L, L.circleMarker([b.lat, b.lng], { radius: isFocus ? 13 : 9, color: '#fff', weight: 2.5, fillColor: color, fillOpacity: 1 }).addTo(layer)
          .bindTooltip((b.discovered ? b.name : (b.place || '神秘打卡點')) + ' ⚔', { permanent: isFocus }), b.lat, b.lng, b.discovered ? b.name : (b.place || '神秘打卡點')) // 點關主打卡點 → 彈出「路線規劃」按鈕
      })
    })
  }, [checkpoints, exploreCps, mapReady, focusBoss])

  // 建議跑步路線：從「目前位置」規劃一條跑者友善（避開車道/高速）的 foot-walking 路線到該打卡點。
  // 讀 curPosRef（非 curPos 閉包）避免 marker click 的 stale 位置。
  async function planRoute(toLat: number, toLng: number, toName: string) {
    const from = curPosRef.current
    if (!from) { setCpMsg('尚未取得目前位置，請到較空曠處或稍候再試'); return }
    const token = getUserToken()
    if (!token) { setShowLogin(true); return }
    setRouteBusy(true)
    try {
      const { distance_m, duration_s, coords } = await withUserAuth((t) => routeApi.plan(t, from.lat, from.lng, toLat, toLng))
      const latlngs: [number, number][] = [[from.lat, from.lng], ...coords] // 從目前位置接到 ORS 路線
      routeLineRef.current?.setLatLngs(latlngs)
      // 看整條路線：暫停自動跟隨並縮放到路線範圍
      if (mapRef.current && latlngs.length > 1) {
        followRef.current = false; setFollowing(false)
        try { mapRef.current.fitBounds(latlngs, { padding: [40, 40] }) } catch { /* ignore */ }
      }
      setRoutePlan({ toName, km: Math.round(distance_m / 10) / 100, etaMin: Math.max(1, Math.round(duration_s / 60)) })
    } catch (e: any) {
      setCpMsg(e?.message || '無法規劃路線，請稍後再試')
    } finally { setRouteBusy(false) }
  }
  function clearRoute() { routeLineRef.current?.setLatLngs([]); setRoutePlan(null) }
  // 點地圖打卡點 → 彈出小卡（名稱 + 「路線規劃」按鈕）；按了按鈕「才」真的規劃、畫線。
  // 改用「彈窗按鈕」而非「點 marker 直接畫線」→ 降低誤觸、動作更明確。
  function bindRoutePopup(L: any, marker: any, lat: number, lng: number, name: string) {
    const box = L.DomUtil.create('div')
    box.style.cssText = 'min-width:134px;font-family:inherit'
    const title = L.DomUtil.create('div', '', box)
    title.textContent = name
    title.style.cssText = 'font-weight:800;font-size:13px;margin-bottom:7px;color:#1a1a1a;word-break:break-word;line-height:1.35'
    const btn = L.DomUtil.create('button', '', box)
    btn.textContent = '🧭 路線規劃'
    btn.style.cssText = 'width:100%;background:#FF8A3D;color:#fff;border:none;border-radius:8px;padding:8px 10px;font-size:12.5px;font-weight:800;cursor:pointer;font-family:inherit'
    L.DomEvent.on(btn, 'click', (e: any) => { L.DomEvent.stop(e); mapRef.current?.closePopup(); planRoute(lat, lng, name) })
    marker.bindPopup(box, { autoPan: true })
  }

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
      const { bosses, checkin_daily_remaining } = await exploreApi.list(token)
      setExploreCps(bosses.filter((b) => b.lat && b.lng))
      setExDailyRemaining(checkin_daily_remaining)
    } catch { /* ignore */ }
  }, [])
  useEffect(() => { fetchExplore() }, [fetchExplore, user?.id])

  const exDist = (b: ExploreBoss): number | null =>
    curPos ? haversineM({ lat: curPos.lat, lng: curPos.lng, t: 0, acc: 0 }, { lat: b.lat, lng: b.lng, t: 0, acc: 0 }) : null
  // 是否在該關主打卡範圍內（curPos 到關主中心 ≤ radius_m）；curPos 是 state，移動時即時重算。挑戰面板「接受挑戰」需在範圍內才可點。
  const exInRange = (b: ExploreBoss): boolean => { const d = exDist(b); return d != null && d <= (b.radius_m || 40) }
  const exDpCost = (b: ExploreBoss): number =>
    (b.attempts && b.attempts > 0 && b.retry_dp_cost > 0) ? b.retry_dp_cost : Math.max(0, b.difficulty_stars) * 10
  // 城市探索清單：依距離排序（最近在最上，未定位則維持原順序）
  const exSorted = exploreCps
    .slice()
    .sort((a, b) => (exDist(a) ?? Infinity) - (exDist(b) ?? Infinity))
  // 清單列：關主挑戰＝一種打卡點，只顯示「走進打卡範圍內（d ≤ 半徑）」的城市探索點；未定位（無 GPS）時不顯示。
  // 例外：從城市探索「前往打卡」聚焦帶來的目標點無條件保留（使用者正要前往，尚未到範圍也要能看到/追蹤）。
  const exList = (() => {
    const inCheckinRange = (b: ExploreBoss) => { const d = exDist(b); return d != null && d <= (b.radius_m || 40) }
    const base = [
      ...exSorted.filter((b) => b.discovered && !b.card_obtained),
      ...exSorted.filter((b) => !b.discovered).slice(0, 10),
    ].filter(inCheckinRange)
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
  // 賽事詳情頁「前往挑戰」帶 ?from=race 進來 → 顯示一次性新手提醒（見下方 showStartTip 渲染處）
  useEffect(() => {
    const from = typeof window !== 'undefined' ? new URLSearchParams(window.location.search).get('from') : null
    if (from === 'race') setShowStartTip(true)
  }, [])
  // 比賽專注模式：?strategy=<id> 帶入賽事策略 → 載入後開跑前顯示小標示；403/404/網路失敗只提示一行、回一般模式
  useEffect(() => {
    const id = typeof window !== 'undefined' ? new URLSearchParams(window.location.search).get('strategy') : null
    if (!id) return
    withUserAuth((t) => strategiesApi.get(t, id))
      .then(({ strategy }) => setRaceStrategy(strategy))
      .catch(() => setStratErr('賽事策略載入失敗，已切換一般模式'))
  }, [])
  useEffect(() => {
    if (!stratErr) return
    const t = setTimeout(() => setStratErr(''), 4000)
    return () => clearTimeout(t)
  }, [stratErr])
  useEffect(() => {
    if (focusDoneRef.current || !mapReady || !mapRef.current || !focusBoss) return
    const b = exploreCps.find((x) => x.id === focusBoss)
    if (!b || (!b.lat && !b.lng)) return
    focusDoneRef.current = true
    followRef.current = false; setFollowing(false)
    centerMap([b.lat, b.lng], 16) // 置中到可視地圖區（避開面板遮蔽）
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
      if (!r.ok) {
        // 冷卻中 / 今日已達上限 / 未到範圍 / 精度不足：不發獎，顯示伺服器訊息
        setCpMsg(r.message || (r.status === 'out_of_range' ? '尚未到達打卡點' : r.status === 'low_accuracy' ? '定位精準度不足，請到空曠處再試' : '打卡失敗'))
        if (typeof r.daily_remaining === 'number') setExDailyRemaining(r.daily_remaining)
        return
      }
      await fetchExplore()
      setExChecked(prev => { const n = new Set(prev); n.add(b.id); return n }) // 走到這裡代表 r.ok===true（早退的失敗已在上面 return），本 session 標記已打卡
      const rewardMsg = (r.dp_awarded || r.gp_awarded) ? `　獲得 DP+${r.dp_awarded ?? 0} GP+${r.gp_awarded ?? 0}（今日打卡剩餘 ${r.daily_remaining ?? 0} 次）` : ''
      if (r.checkin_only) {
        // 純打卡點：不揭露關主，僅行內訊息提示（可重複打卡刷 DP/GP，過冷卻即可再打）
        setCpMsg((r.already ? '✓ 打卡完成：' : '✓ 打卡完成！') + r.place + rewardMsg)
      } else if (r.boss) {
        if (!r.already) {
          // 第一次揭露此關主 → 自動跳出挑戰面板（既有行為不變）
          setBossPanel({ boss: r.boss, phase: 'intro', dpCost: exDpCost(r.boss) })
        } else {
          // 已揭露過的重複打卡（含 active=true 的點）：只拿獎勵，不再自動彈出挑戰面板——
          // 要挑戰請按旁邊的「⚔ 挑戰／繼續挑戰」按鈕。
          setCpMsg('✓ 打卡完成！' + rewardMsg)
        }
      }
    } catch (e: any) {
      setCpMsg(e?.code === 1 ? '需要定位權限才能打卡' : (e?.message || '打卡失敗，請重試'))
    } finally { setCpBusy('') }
  }

  // 開啟關主挑戰面板（清單上按「⚔ 挑戰／繼續挑戰／自由挑戰」）：exList 的 boss 物件來自精簡版 /explore
  // 列表（不含 segments/對話/金句/技能/card_image_url/master_image_url，見 api.ts ExploreBoss 註解），
  // 面板要顯示的立繪/對話/課表都在這些欄位裡 → 開面板前先打 exploreApi.detail() 抓單一關主完整資料，
  // 抓到什麼就用什麼渲染，不會有「面板打開時欄位是 undefined」的破圖情況。
  // （剛打卡揭露、由 doExploreCheckin 直接開面板的路徑不會走這裡——那裡的 r.boss 已是 Checkin 回應的完整資料。）
  async function openBossPanel(b: ExploreBoss) {
    setCpMsg('')
    const token = getUserToken()
    if (!token) { setShowLogin(true); return }
    setCpBusy('exd:' + b.id)
    try {
      const { boss } = await withUserAuth((t) => exploreApi.detail(t, b.id))
      setBossPanel({ boss, phase: boss.active ? 'start' : 'intro', dpCost: exDpCost(boss) })
    } catch (e: any) {
      setCpMsg(e?.message || '讀取關主資料失敗，請重試')
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
  // paceElapsed：從第一個 GPS 點到現在的秒數（與後端 duration_s 口徑一致）；
  // 跑步開始前（无 GPS 點）退回按鈕時間，此時兩者相同，行為不變。
  const paceElapsed = (Date.now() - paceBaseMs()) / 1000
  const avgPace = distKm >= PACE_MIN_KM ? paceElapsed / distKm : 0 // 未達門檻先顯示 --:--，避免爆數字
  // 分段即時配速：當下（進行中）這一公里的即時配速（秒/公里）。跨過整公里即歸零重算；不足 30m 先顯示 --:--
  // segStartT 與 paceElapsed 同一基準（皆從第一個 GPS 點算），相減才正確。
  const segKmDone = splitMarkRef.current.length
  const segStartT = segKmDone > 0 ? splitMarkRef.current[segKmDone - 1] : 0
  const segDistM = Math.max(0, distance - segKmDone * 1000)
  const segLivePace = segDistM >= 30 ? (paceElapsed - segStartT) / (segDistM / 1000) : 0
  // #4 依「移動時間」（排除靜止/停等）計的平均配速與分段即時配速
  const movingAvgPace = distKm >= PACE_MIN_KM ? movingS / distKm : 0
  const movingSegStartT = segKmDone > 0 && movingSplitMarkRef.current.length >= segKmDone ? movingSplitMarkRef.current[segKmDone - 1] : 0
  const movingSegLivePace = segDistM >= 30 ? (movingS - movingSegStartT) / (segDistM / 1000) : 0
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
      {/* 里程優先來源＝Strava：GPS 結束不自動上傳，先讓使用者選 */}
      {confirmStravaHold && (
        <div style={{ position: 'fixed', inset: 0, zIndex: 3300, background: 'rgba(0,0,0,.66)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 24 }}>
          <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line-2)', borderRadius: 16, padding: '20px 18px', maxWidth: 340, width: '100%', boxShadow: '0 12px 40px rgba(0,0,0,.6)' }}>
            <div style={{ fontSize: 17, fontWeight: 900, color: 'var(--tx)', marginBottom: 8 }}>您已開啟 STRAVA 數據同步</div>
            <div style={{ fontSize: 13.5, color: 'var(--tx-dim)', lineHeight: 1.7 }}>
              是否優先確認 STRAVA 數據，或是直接使用本次數據？
            </div>
            <div style={{ fontSize: 13.5, color: 'var(--tx)', marginTop: 10 }}>
              本次跑步 · {confirmStravaHold.km} km · {confirmStravaHold.mins} 分
              {confirmStravaHold.paceS > 0 ? ` · 配速 ${Math.floor(confirmStravaHold.paceS / 60)}:${String(confirmStravaHold.paceS % 60).padStart(2, '0')}/km` : ''}
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 16 }}>
              <button onClick={() => { setConfirmStravaHold(null); cleanup(); setStatus('done'); doUploadGps(pointsRef.current) }} disabled={uploading} style={{ ...btn, opacity: uploading ? 0.6 : 1 }}>
                {uploading ? '上傳中…' : '直接使用本次數據'}
              </button>
              <button onClick={() => { leavingRef.current = true; cleanup(); setStatus('done'); window.location.href = '/?profile=sports' }} disabled={uploading} style={{ background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '10px', fontSize: 13.5, cursor: 'pointer', fontFamily: 'inherit' }}>
                前往確認數據
              </button>
              {/* 反悔/暫不決定：關彈窗、GPS 繼續追蹤續跑（此路徑從未 cleanup/未 setStatus，故直接接續）。 */}
              <button onClick={() => setConfirmStravaHold(null)} disabled={uploading} style={{ background: 'transparent', color: 'var(--fug)', border: 'none', padding: '8px', fontSize: 13.5, fontWeight: 800, cursor: 'pointer', fontFamily: 'inherit' }}>
                ▶ 繼續進行跑步
              </button>
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
      {/* 專注模式：任何 tracking 中的跑步都能切入的全螢幕大字資訊疊層（有賽事策略時＋配速/補給提醒完整版，
          維持開跑自動進入；無策略的一般跑步/課表/個人任務只顯示基本 4 指標，預設不自動進入，靠元件內建的
          切換鈕手動開關，見 RaceFocusMode 內 hidden 初始值）。z-index 600（>面板 500，但低於事件演出
          2100+/確認結束 2500/Strava 三選一與登入 3300），讓既有的警示/事件系統仍蓋在它之上；純顯示層，
          不影響 WorkoutHud/課表引擎/事件任務引擎下方繼續運作的任何邏輯。
          時間口徑：疊層內「時間/平均配速/預計完成」吃 elapsed/avgPace（大會時間，不因靜止停錶）；
          「分段即時配速」大字吃 segLivePace＝與四格完全同一個值（2026-08-27 拍板「放大鏡原則」：疊層
          四大字必須跟背景四格一模一樣，同名不同數會被當 bug）；movingSegLivePace 只供偏差提醒引擎內部
          比較、不再上畫面（見 RaceFocusMode 內口徑決策註解）。下方一般面板「移動時間/移動配速/分段」
          那排不受影響。 */}
      {status === 'tracking' && (
        <RaceFocusMode strategy={raceStrategy} distanceM={distance} elapsed={elapsed} avgPace={avgPace} segLivePace={segLivePace} movingSegLivePace={movingSegLivePace} goal={runGoal} canTestCheer={canTestCheer} onTestCheer={testCheer} />
      )}
      {/* 每公里鼓勵語「泡泡對話框+啦啦隊角色」演出（v1.1.664）：獨立掛在本頁頂層、不論 status，
          z-index 650 蓋過上面的 RaceFocusMode（600）——hidden 分支切回一般畫面時本節點仍在，不需要
          RaceFocusMode 內再各自渲染一份。 */}
      <CheerShow cheer={cheer} />
      {/* 白名單測試按鈕：cheer_test_entry==='shown' 才顯示（系統設定白名單，見上方 canTestCheer 宣告處）。
          一般畫面固定在右上角；RaceFocusMode 完整專注模式另有一顆同款按鈕（見該檔「顯示完整介面」旁）。 */}
      {canTestCheer && (
        <button
          data-skin="default"
          onClick={testCheer}
          style={{
            position: 'fixed', top: 'calc(var(--app-top, 24px) + 8px)', right: 12, zIndex: 560,
            background: 'rgba(11,14,19,.9)', color: 'var(--tx)', border: '1px solid rgba(255,194,75,.6)',
            borderRadius: 999, padding: '8px 13px', fontSize: 12.5, fontWeight: 800, cursor: 'pointer',
            boxShadow: '0 3px 12px rgba(0,0,0,.28)', fontFamily: 'inherit',
          }}
        >📣 測試應援</button>
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
          {/* 進行中活動/賽事：GPS 累積里程會計入的賽事清單，非導航連結，跑步中也可安全開啟查看 */}
          {user && <button onClick={() => setShowActiveRaces(true)} style={{ background: 'none', border: 'none', cursor: 'pointer', fontSize: 12.5, fontWeight: 700, color: 'var(--fug)', padding: 0, whiteSpace: 'nowrap' }}>進行中活動/賽事</button>}
          {status !== 'tracking' && <a href="/track/history" style={{ color: 'var(--fug)', fontSize: 13, textDecoration: 'none' }}>歷史</a>}
        </div>
      </header>

      {/* 地圖 + COROS 式可拖曳資訊面板：地圖佔滿容器、資訊面板可上下拖曳露出更多/更少（配色與顯示資訊都不變，只改操作體驗） */}
      <div ref={sheet.wrapRef} style={{ position: 'relative', flex: 1, minHeight: 0, overflow: 'hidden' }}>
        <div id="gps-map" style={{ position: 'absolute', inset: 0, zIndex: 0, background: 'var(--bg-2)' }} />
        {/* 「定位中…」遮罩：進頁自動預熱定位期間（還沒拿到第一個座標）顯示，取代看起來像真實地點的假中心；
            拿到 curPos 或逾時/失敗（autoLocating 轉 false）即消失。不擋操作。 */}
        {status === 'idle' && !curPos && autoLocating && (
          <div style={{ position: 'absolute', top: '50%', left: '50%', transform: 'translate(-50%, -50%)', zIndex: 400, background: 'rgba(0,0,0,.55)', color: '#fff', padding: '8px 16px', borderRadius: 999, fontSize: 13, fontWeight: 700, pointerEvents: 'none', display: 'flex', alignItems: 'center', gap: 6, boxShadow: '0 3px 12px rgba(0,0,0,.28)' }}>
            定位中…
          </div>
        )}
        {/* 回到目前位置：手動看地圖後（暫停跟隨）→ 恢復置中；idle 尚未定位 → 點了在使用者手勢內請求一次定位。
            顯示時機：非 done 且（已暫停跟隨 或 尚無定位）；正在自動跟隨且已有定位時地圖本就置中、不需此鈕故隱藏。
            另外：正在自動預熱定位中（autoLocating，上方遮罩已表達狀態）時不顯示這顆 CTA，避免使用者誤以為要手動按才會定位；
            自動定位失敗/逾時/被拒/非 iOS 未授權需手勢等情況 autoLocating 為 false，仍會顯示讓使用者手動觸發。 */}
        {status !== 'done' && (!following || !curPos) && !(status === 'idle' && !curPos && autoLocating) && (
          <button
            onClick={recenterMap}
            style={{ position: 'absolute', top: 12, right: 12, zIndex: 550, background: 'var(--bg-1)', color: 'var(--fug)', border: '1px solid var(--line-2)', borderRadius: 999, padding: '8px 13px', fontSize: 12.5, fontWeight: 700, cursor: 'pointer', boxShadow: '0 3px 12px rgba(0,0,0,.28)' }}
          >◎ {curPos ? '回到目前位置' : '定位到我'}</button>
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
        {/* 建議跑步路線資訊條（點地圖打卡點規劃後顯示） */}
        {(routeBusy || routePlan) && (
          <div style={{ position: 'absolute', left: 0, right: 0, top: 0, zIndex: 920, padding: '10px 12px 0', pointerEvents: 'none' }}>
            <div style={{ background: 'var(--bg-1)', color: 'var(--tx)', border: '1px solid #FF8A3D', borderRadius: 10, padding: '9px 10px 9px 12px', fontSize: 12.5, boxShadow: '0 4px 16px rgba(0,0,0,.35)', pointerEvents: 'auto', display: 'flex', alignItems: 'center', gap: 8 }}>
              {routeBusy ? (
                <span style={{ flex: 1 }}>🧭 規劃建議路線中…</span>
              ) : routePlan ? (
                <span style={{ flex: 1, minWidth: 0, lineHeight: 1.5 }}>
                  🧭 建議路線 · 約 <strong style={{ color: '#FF8A3D' }}>{routePlan.km} km</strong> · 預估 {routePlan.etaMin} 分 → {routePlan.toName}
                  <span style={{ display: 'block', fontSize: 10.5, color: 'var(--tx-faint)' }}>跑者友善（已避開車道/高速）· 僅供參考</span>
                </span>
              ) : null}
              {routePlan && <button onClick={clearRoute} style={{ flexShrink: 0, background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '5px 9px', fontSize: 12, cursor: 'pointer', fontFamily: 'inherit' }}>清除</button>}
            </div>
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
            <EventBanner
              active={activeEvent}
              moved={eventMoved}
              groupProgress={raceGroupProgress && raceGroupProgress.instanceId === activeEvent.raceInstanceId ? raceGroupProgress : undefined}
            />
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
            {/* #4 依「GPS 有移動時的實際時間」（排除靜止/停等）計的移動時間與配速 */}
            <div style={{ marginTop: 6, fontSize: 11.5, color: 'var(--tx-dim)', textAlign: 'center', letterSpacing: 0.2 }}>
              移動時間 {fmtTime(movingS)} · 移動配速 {fmtPace(movingAvgPace)}/km · 分段 {fmtPace(movingSegLivePace)}/km
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
          <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', touchAction: 'pan-y', padding: '2px 16px calc(20px + var(--cta-safe, 0px))' }}>
            {/* 個人任務課表（在滑動面板內，不蓋地圖）：閒置＝選課表(左右滑動輪播+●○○)；進行中/完成＝分段執行 HUD */}
            {status === 'idle' && woPhase === 'idle' && workout?.kind === 'freetrain' && (
              <FreetrainIntroPanel title={workout.title} steps={workout.steps} freerunMin={workout.freerunSec ? Math.round(workout.freerunSec / 60) : undefined} />
            )}
            {status === 'idle' && woPhase === 'idle' && workout?.kind !== 'freetrain' && panel && (() => {
              const ac = panel.active_card
              const list = ac && !panel.cards.some((c) => c.task_id === ac.task_id) ? [ac, ...panel.cards] : panel.cards
              return <TrackTaskPanel cards={list} activeTaskId={workout?.taskId ?? ac?.task_id ?? null} busy={panelBusy} onChallenge={challengeCard} onAbandon={abandonActive} />
            })()}
            {(woPhase === 'running' || woPhase === 'done') && workout && (() => {
              const stepDist = Math.max(0, distRef.current - woStepStartRef.current.dist)
              const stepTime = Math.max(0, (Date.now() - woStepStartRef.current.time) / 1000)
              const livePace = stepDist > 5 ? stepTime / (stepDist / 1000) : 0
              return (
                <WorkoutHud title={workout.title} kind={workout.kind} steps={workout.steps} stepIdx={woStepIdx}
                  stepDist={stepDist} stepTime={stepTime} livePaceS={livePace} hits={woHits}
                  phase={woPhase === 'done' ? 'done' : 'running'} result={woResult}
                  freerun={!!workout.freerunSec}
                  onRanking={workout.kind === 'explore' && !woResult?.flagged ? () => setRankingBoss({ id: workout.taskId, name: workout.title }) : undefined}
                  continuing={status === 'tracking'}
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
            <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 6 }}><span className="skin-ico" data-ico="pin" aria-hidden>📍</span> 打卡點任務{exploreCps.length > 0 && exDailyRemaining != null && <span style={{ color: 'var(--tx-dim)' }}>（今日打卡剩餘 {exDailyRemaining} 次）</span>}</div>
            {cpMsg && <div style={{ fontSize: 12.5, color: 'var(--fug)', marginBottom: 8, wordBreak: 'break-word' }}>{cpMsg}</div>}
            {status !== 'tracking' && <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginBottom: 8 }}>走到打卡點附近，在範圍內按「打卡」即可（不需邊跑邊打卡）。打卡點過 24h 冷卻可再次打卡拿獎勵。</div>}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {/* 城市探索打卡點：清單只列 已揭露待挑戰 + 最近 10 筆未打卡（依縣市篩選＋距離排序） */}
              {exList.map((b) => {
                const d = exDist(b)
                const inRange = d != null && d <= (b.radius_m || 40)
                const busy = cpBusy === 'ex:' + b.id
                const checked = exChecked.has(b.id) // 本 session 已成功打卡（反灰＋顯示「已打卡」；冷卻後重進頁可再打）
                const title = b.discovered ? ([b.name, b.place].filter(Boolean).join(' ｜ ') || '關主挑戰') : (b.place || '神秘打卡點')
                return (
                  <div key={'ex:' + b.id} style={{ background: 'var(--bg-2)', borderRadius: 'var(--radius-md, 10px)', padding: '10px 12px', display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 10, border: b.discovered ? '1px solid rgba(231,184,75,.45)' : '1px solid transparent' }}>
                    <div style={{ minWidth: 0 }}>
                      <div style={{ fontSize: 13.5, fontWeight: 700, color: 'var(--tx)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                        {b.discovered ? '⚔ ' : '📍 '}{title}
                        {b.discovered && b.difficulty_stars > 0 && <span style={{ color: 'var(--gold)', fontSize: 11, marginLeft: 6 }}>{'★'.repeat(b.difficulty_stars)}</span>}
                      </div>
                      <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                        城市探索{b.region ? ` · ${b.region}` : ''}
                        {b.card_obtained && <> · ✓已收服{b.best_time_s ? ` · 最佳 ${fmtTime(b.best_time_s)}` : ''}</>}
                        {d != null && !b.card_obtained && <> · {d < 1000 ? `還有 ${Math.round(d)}m` : `${(d / 1000).toFixed(1)}km`}</>}
                      </div>
                    </div>
                    <div style={{ display: 'flex', gap: 6, flexShrink: 0 }}>
                      {/* 打卡：純打卡點固定顯示；一般關主點在已揭露後仍可重複按（拿 DP/GP，24h 冷卻+每日上限），
                          不會再次觸發挑戰面板（active 短路，只擋自動彈窗，不擋打卡本身）*/}
                      {(b.checkin_only || b.discovered) && (
                        <button onClick={() => doExploreCheckin(b)} disabled={busy || checked || (curPos != null && !inRange)}
                          style={{ background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '8px 12px', fontSize: 13, cursor: (checked || busy || (curPos != null && !inRange)) ? 'default' : 'pointer', opacity: (checked || busy || (curPos != null && !inRange)) ? 0.45 : 1 }}>
                          {checked ? '已打卡' : busy ? '打卡中…' : curPos != null && !inRange ? '未到範圍' : '打卡'}
                        </button>
                      )}
                      {!b.checkin_only && b.discovered && (() => {
                        const loadingDetail = cpBusy === 'exd:' + b.id
                        return (
                          <button onClick={() => openBossPanel(b)} disabled={loadingDetail}
                            style={{ background: 'var(--gold)', color: '#fff', fontWeight: 800, border: 'none', borderRadius: 9, padding: '8px 12px', fontSize: 13, cursor: loadingDetail ? 'default' : 'pointer', opacity: loadingDetail ? 0.6 : 1 }}>
                            {loadingDetail ? '載入中…' : b.active ? '▶ 繼續挑戰' : b.card_obtained ? '自由挑戰' : '⚔ 挑戰'}
                          </button>
                        )
                      })()}
                      {!b.checkin_only && !b.discovered && (
                        <button onClick={() => doExploreCheckin(b)} disabled={busy || checked || (curPos != null && !inRange)}
                          style={{ background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '8px 14px', fontSize: 13, cursor: (checked || busy || (curPos != null && !inRange)) ? 'default' : 'pointer', opacity: (checked || busy || (curPos != null && !inRange)) ? 0.45 : 1 }}>
                          {checked ? '已打卡' : busy ? '打卡中…' : curPos != null && !inRange ? '未到範圍' : '打卡'}
                        </button>
                      )}
                    </div>
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

      {/* 新手提醒（從賽事詳情頁「前往挑戰」進入，?from=race）：只在 idle、尚未進入結構化課表時顯示；
          非 fixed 疊層，跟著版面正常排列在「開始跑步」按鈕正上方——不需 overlayMount（見該檔頂部註解），
          也不受桌機 .phone-shell 模擬框的 fixed 疊層規則影響。點擊本身或 X 皆可關閉，不會再自動顯示。 */}
      {showStartTip && status === 'idle' && !workout && (
        <div
          onClick={() => setShowStartTip(false)}
          style={{ margin: '0 16px 10px', flexShrink: 0, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 8, background: 'rgba(70,227,160,.1)', border: '1px solid var(--fug)', borderRadius: 12, padding: '10px 12px', fontSize: 13, color: 'var(--tx)', lineHeight: 1.5 }}
        >
          <span style={{ flex: 1, minWidth: 0 }}>👉 點擊下方「開始跑步」按鈕，立即進行挑戰。</span>
          <button
            onClick={(e) => { e.stopPropagation(); setShowStartTip(false) }}
            aria-label="關閉"
            style={{ flexShrink: 0, background: 'transparent', border: 'none', color: 'var(--tx-dim)', fontSize: 15, lineHeight: 1, cursor: 'pointer', padding: '4px 6px', fontWeight: 700 }}
          >✕</button>
        </div>
      )}

      {/* 已載入賽事策略（?strategy=<id>）：開跑前顯示，可取消（卸下策略回一般模式）；開跑後交給 RaceFocusMode 疊層 */}
      {raceStrategy && status === 'idle' && (
        <div style={{ margin: '0 16px 10px', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 8, background: 'rgba(255,194,75,.1)', border: '1px solid var(--gold)', borderRadius: 12, padding: '10px 12px', fontSize: 13, color: 'var(--tx)', lineHeight: 1.5 }}>
          <span style={{ flex: 1, minWidth: 0 }}>🏁 已載入賽事策略：<strong>{raceStrategy.name}</strong>（開跑後進入專注模式）</span>
          <button onClick={() => setRaceStrategy(null)} aria-label="取消策略" style={{ flexShrink: 0, background: 'transparent', border: 'none', color: 'var(--tx-dim)', fontSize: 15, lineHeight: 1, cursor: 'pointer', padding: '4px 6px', fontWeight: 700 }}>✕</button>
        </div>
      )}
      {stratErr && (
        <div style={{ margin: '0 16px 10px', flexShrink: 0, fontSize: 12.5, color: 'var(--hunt)' }}>{stratErr}</div>
      )}

      {/* 操作 */}
      <div style={{ padding: '16px 16px calc(16px + var(--cta-safe, 0px))', flexShrink: 0, borderTop: '1px solid var(--line)', background: 'var(--bg)' }}>
        {status === 'idle' && (
          user
            ? (workout
                ? <button onClick={startWorkout} className="skin-btn-start" style={btn}>{workout.kind === 'freetrain' ? '▶ 開始訓練' : '▶ 開始課表挑戰'}</button>
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
          user={user}
          note={status === 'tracking' ? '⚠ 請先結束目前的跑步，再開始關主挑戰（挑戰為獨立的追蹤紀錄）' : undefined}
          outOfRange={bossPanel.phase === 'intro' && !exInRange(bossPanel.boss)}
          onAccept={acceptBoss}
          onDecline={() => setBossPanel(null)}
          onStart={startBossWorkout}
        />
      )}

      {/* 挑戰者成績排行覆蓋層 */}
      {rankingBoss && (
        <BossRankingPanel bossId={rankingBoss.id} bossName={rankingBoss.name} onClose={() => setRankingBoss(null)} />
      )}

      {/* 進行中活動/賽事：GPS 跑步追蹤累積的里程會計入的賽事清單 + 各自進度 */}
      {showActiveRaces && <ActiveRacesPanel onClose={() => setShowActiveRaces(false)} />}

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

// 「進行中活動/賽事」面板：目前登入者「這筆 GPS 跑步、現在跑會被計入」的賽事/挑戰清單 + 各自進度。
// 開啟時才 fetch（本元件只在 showActiveRaces=true 時才會被掛載）。不放任何導航連結——跑步中開啟本面板
// 查看進度是安全的，但點連結跳頁會中斷正在進行的 GPS 追蹤。
function ActiveRacesPanel({ onClose }: { onClose: () => void }) {
  const [races, setRaces] = useState<MyActiveRace[] | null>(null)
  const [err, setErr] = useState(false)

  useEffect(() => {
    let alive = true
    if (!getUserToken()) { setErr(true); return }
    withUserAuth((t) => racesApi.myActive(t))
      .then((r) => { if (alive) setRaces(r.races) })
      .catch(() => { if (alive) setErr(true) })
    return () => { alive = false }
  }, [])

  function periodLabel(endDate: string) {
    const end = new Date(endDate)
    if (isNaN(end.getTime())) return ''
    const daysLeft = Math.ceil((end.getTime() - Date.now()) / 86400000)
    const md = `${end.getMonth() + 1}/${end.getDate()}`
    return daysLeft <= 0 ? `今天截止（${md}）` : `剩 ${daysLeft} 天（至 ${md}）`
  }

  return (
    <div style={{ position: 'fixed', inset: 0, zIndex: 3300, background: 'rgba(0,0,0,.66)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 20 }}>
      <div style={{ width: '100%', maxWidth: 400, maxHeight: '85dvh', display: 'flex', flexDirection: 'column', background: 'var(--bg-1)', border: '1px solid var(--line-2)', borderRadius: 16, boxShadow: '0 12px 40px rgba(0,0,0,.6)', overflow: 'hidden' }}>
        <div style={{ padding: '18px 18px 12px', borderBottom: '1px solid var(--line)', flexShrink: 0 }}>
          <div style={{ display: 'flex', alignItems: 'flex-start', gap: 8 }}>
            <div style={{ fontSize: 17, fontWeight: 900, color: 'var(--tx)', flex: 1, minWidth: 0 }}>進行中活動/賽事</div>
            <button onClick={onClose} aria-label="關閉" style={{ background: 'transparent', border: 'none', color: 'var(--tx-dim)', fontSize: 22, lineHeight: 1, cursor: 'pointer', padding: 0, flexShrink: 0 }}>×</button>
          </div>
          <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 6, lineHeight: 1.6 }}>
            GPS 跑步追蹤累積的里程，將會計入在以下活動/賽事：
          </div>
        </div>

        <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', padding: '12px 16px 18px' }}>
          {err ? (
            <div style={{ textAlign: 'center', padding: '36px 10px', fontSize: 13.5, color: 'var(--tx-dim)' }}>無法載入，請稍後再試</div>
          ) : races === null ? (
            <div style={{ textAlign: 'center', padding: '36px 10px', fontSize: 13.5, color: 'var(--tx-dim)' }}>載入中…</div>
          ) : races.length === 0 ? (
            <div style={{ textAlign: 'center', padding: '36px 10px', fontSize: 13.5, color: 'var(--tx-dim)' }}>目前沒有進行中的已報名活動/賽事</div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {races.map((r) => (
                <div key={r.id} style={{ background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 12, padding: '12px 14px' }}>
                  <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 8 }}>
                    <div style={{ fontSize: 14.5, fontWeight: 800, color: 'var(--tx)', minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{r.title}</div>
                    <span style={{ flexShrink: 0, fontSize: 11, fontWeight: 700, color: 'var(--fug)', background: 'rgba(45,229,154,.14)', borderRadius: 999, padding: '2px 9px' }}>
                      {r.event_mode === 'personal' ? '個人挑戰' : '賽事'}
                    </span>
                  </div>
                  <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 4 }}>{periodLabel(r.end_date)}</div>

                  <div style={{ display: 'flex', gap: 14, marginTop: 8 }}>
                    <div>
                      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>我的里程</div>
                      <div style={{ fontSize: 15, fontWeight: 900, color: 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>{r.my_total_km.toFixed(1)} <span style={{ fontSize: 11, color: 'var(--tx-dim)' }}>K</span></div>
                    </div>
                    <div>
                      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>活動筆數</div>
                      <div style={{ fontSize: 15, fontWeight: 900, color: 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>{r.my_activities} <span style={{ fontSize: 11, color: 'var(--tx-dim)' }}>筆</span></div>
                    </div>
                  </div>

                  {r.event_mode === 'personal' ? (
                    <div style={{ marginTop: 8, fontSize: 12, color: 'var(--tx-dim)', lineHeight: 1.6 }}>
                      <div>條件：{formatChallengeRule(r.challenge_rule)}</div>
                      {r.challenge_progress && <div style={{ color: 'var(--gold)', fontWeight: 700, marginTop: 2 }}>{formatChallengeProgress(r.challenge_progress)}</div>}
                    </div>
                  ) : (
                    <div style={{ marginTop: 8, fontSize: 12, color: 'var(--tx-dim)' }}>
                      任務 <span style={{ color: 'var(--gold)', fontWeight: 700 }}>{r.tasks_done}/{r.tasks_total}</span>
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
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
