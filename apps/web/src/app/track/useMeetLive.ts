'use client'

// 團練同步跑（Group Run Live）client 端同步引擎——契約 docs/runmeet/GROUP_RUN_LIVE_CONTRACT.md §3／§5／§6。
//
// 疊在 /track 自由跑上的「盡力而為」功能：這裡任何失敗都只影響同步狀態（膠囊／專注模式狀態行），
// 絕不碰跑步記錄（距離／軌跡／上傳／Wake Lock 一律零依賴本檔）。重點規則：
//   · 只在 status==='tracking' 且 meetId 合法時運作；首次 start 延遲 random(0,2000)ms（整點風暴）。
//   · 位置只從 timer 讀 curPosRef／curPosAtRef 送出，絕不在 onPos 內送請求（onPos 是高頻熱路徑）。
//   · 排程＝iv×random(0.9,1.1)（saveData ×2）；請求逾時 8 s；同時只有一個請求（不重疊）；
//     AbortController 在 cleanup 取消；頁面 hidden 清 timer、visible／pageshow／focus 冷卻 ≥1.5 s 後補送。
//   · 帶 p 的條件：距上次已送位置 ≥8 m 或距上次帶 p ≥15 s（靜止 keepalive）；精度 >65 m 或定位年齡 >10 s、
//     隱私區（§5）、暫停分享、presence-only 一律不帶 p（心跳照送）。
//   · 退避 5→10→20→30 s（±30%）；grant_missing → start(reauth)，自限 6/min；撤銷類錯誤 → 'stopped'＋訊息。
//   · 輸出：meetPeersRef（可變 ref，更新不觸發 page 重繪）＋節流 state liveStats（≤1 Hz）。
//
// 隱私起點錨點（契約 §5）：起點＝本趟開跑後第一個可用定位（精度 ≤150 m、年齡 ≤60 s）。頁面被系統砍掉後自動接續時，錨點若只存記憶體
// 會變成「接續當下的位置」——使用者跑回家時反而不在「起點」200 m 內、把家分享出去——所以錨點（連同它的精度、暫停分享旗標）
// 以「本趟開跑時間（startRef）」為鍵寫在 localStorage（dor_meet_live_run），同一趟接續時讀回。
//   · 錨點一取得（begin／第一個可用定位／30 秒內精度改善時的校正）就立刻落地，不等 /live/start 成功——
//     start 一直失敗（離線／503）時頁面被系統砍掉，接續才不會把錨點重設成「當下位置」。
//   · 隱私半徑＝200＋錨點精度（公尺）；「起點在集合點附近」要「確定」才算（距離＋錨點精度 ≤300 m）；
//     隱私區外 → 可分享要「連續 2 個不同定位」都在半徑外才放行（單點飄移不外洩），回到半徑內則立即停止分享。
//   · 本趟紀錄只在「跑步真的結束」才清（dispose 時 dor_gps_active 已不是這趟）；離開頁面（返回鍵／被導走）時跑步仍在進行，
//     全站 ActiveRunGuard 會把使用者導回，這時紀錄必須還在，否則暫停旗標與錨點會被重設、同意證據也沒了。
//
// 同意證據（owner 要求）：深連結 /track?meet=<id> 不得略過團練頁的同意流程——沒有「使用者在團練頁確認過同意視窗」的
// 證據（hasLiveConsentEvidence＋本趟紀錄 consent）時，引擎完全不啟動：零請求（沒有 /live/start、沒有 /pos）、不寫 storage、
// 不抓錨點，狀態 'stopped'＋NO_CONSENT_MESSAGE，跑步本身照常（一般自由跑）。consent_v 只會在證據成立時送出（稽核紀錄必須為真）。
// 證據成立後立即把 consent:true 寫進本趟紀錄（見 begin），頁面被系統砍掉重載（sessionStorage 已清空）時由它當證據。
// 本分頁標記是「一次性」的：引擎因它才通過時，寫入 consent:true 後立刻移除——每次開跑都要從團練頁按鈕進入（除非勾了「不再提醒」）；
// 同一趟的重載／被導回由本趟紀錄接續。

import { useCallback, useEffect, useRef, useState, type MutableRefObject } from 'react'
import { meetLiveApi, type MeetLivePosResp, type MeetLiveStartResp } from '@/lib/api'
import { readActiveRun } from '@/lib/activeRun'
import { readLiveConsent, readLiveConsentSession, runMeetLiveConsentSessionKey } from '@/lib/runMeet'
import { withUserAuth, SessionExpiredError } from '@/lib/userAuth'
import type { MeetLivePeers, MeetLiveState, MeetLiveStats, PeerDot, StaleThresholds } from './meetLiveTypes'
import { NO_CONSENT_MESSAGE, bearingDeg, haversineM, outsideWindowMessage, staleSeconds } from './meetLiveUtil'

const PV = 1 as const
const CONSENT_V = 1
const REQ_TIMEOUT_MS = 8000 // 請求逾時
const MIN_GAP_MS = 1500 // 伺服器 1.5 s 頻率地板：visible 補送的冷卻
const START_DELAY_MAX_MS = 2000 // 首次 start 隨機延遲上限（整點風暴 M10）
const MIN_MOVE_M = 8 // 距上次已送位置 ≥ 此值才再帶 p
const KEEPALIVE_P_MS = 15000 // 靜止也要每 15 s 帶一次 p（M3）
const MAX_ACC_M = 65 // 精度差於此不帶 p（同 page.tsx MAX_ACC）
const MAX_FIX_AGE_MS = 10000 // 定位年齡超過此不帶 p
const NEAR_MEET_M = 300 // 起點「確定」在團練地點附近（距離＋錨點精度 ≤ 此值）→ 不設隱私區（§5）
const PRIVACY_AWAY_M = 200 // 隱私半徑基數：實際半徑＝200＋錨點精度；離起點 ≥ 半徑才分享、回到半徑內即停止分享
const ANCHOR_MAX_ACC_M = 150 // 錨點可接受的定位精度上限（分享仍要 ≤65 m，見 MAX_ACC_M）
const ANCHOR_MAX_AGE_MS = 60000 // 錨點用的定位年齡上限
const ANCHOR_REFINE_MS = 30000 // 錨點設定後 30 秒內、且還沒分享過位置，遇到「精度嚴格更好」的新定位就校正
const ANCHOR_ACC_CLAMP_M = 300 // 存下來的錨點精度上限
const AWAY_CONFIRM_FIXES = 2 // 隱私區外 → 可分享：需連續 2 個「不同」定位都在半徑外（單點飄移不外洩）
const BACKOFF_S = [5, 10, 20, 30] // 退避階梯（秒）
const GRANT_MISSING_LIMIT = 6 // grant_missing 重建自限（每 60 s）
// 後端 /live/start 限流是 6 次／分／人（runmeet_live_start，契約 §3）：client 把「所有」start 請求（首次、重試、
// grant_missing 重建、靜默 reauth）算進同一個 60 s 滑動視窗，滿額就等到最舊的一筆滑出視窗再送——
// 只數 grant_missing 的話「首次＋6 次重建」會多出 1 次、被伺服器 429（Retry-After≈60）。
const START_LIMIT_PER_MIN = 6
const STATS_MIN_INTERVAL_MS = 1000 // liveStats 更新 ≤ 1 Hz
const DEFAULT_IV_MS = 5000
const RUN_KEY = 'dor_meet_live_run'
const RUN_TTL_MS = 12 * 3600 * 1000

type Fix = { lat: number; lng: number; acc: number }

const IDLE_STATS: MeetLiveStats = { state: 'idle', live: 0, iv: DEFAULT_IV_MS, sharing: false, presenceOnly: false, nearest: [] }

const rand = (a: number, b: number) => a + Math.random() * (b - a)
const round5 = (v: number) => Math.round(v * 1e5) / 1e5
const clampInt = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, Math.round(Number.isFinite(v) ? v : 0)))

/** 契約 §3.1：sid＝client 隨機 16–32 字元 [A-Za-z0-9]（每趟一組）。 */
function randomSid(): string {
  const ALPHA = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789'
  let out = ''
  try {
    const buf = new Uint8Array(48)
    while (out.length < 24) {
      crypto.getRandomValues(buf)
      for (let i = 0; i < buf.length && out.length < 24; i++) if (buf[i] < 248) out += ALPHA[buf[i] % 62] // 248＝62×4，拒絕取樣避免偏差
    }
  } catch {
    out = ''
    for (let i = 0; i < 24; i++) out += ALPHA[Math.floor(Math.random() * 62)]
  }
  return out
}

/** 陳舊度門檻（契約 §3.1）：fade=max(3·iv,30s)、gray=max(8·iv,75s)、drop=180s。start 回應有明確值就用它。 */
function staleFor(ivMs: number, fromServer?: { fade_s?: number; gray_s?: number; drop_s?: number } | null): StaleThresholds {
  const ivS = ivMs / 1000
  const pick = (v: unknown, dflt: number) => { const n = Number(v); return Number.isFinite(n) && n > 0 ? n : dflt }
  return {
    fadeS: pick(fromServer?.fade_s, Math.max(3 * ivS, 30)),
    grayS: pick(fromServer?.gray_s, Math.max(8 * ivS, 75)),
    dropS: pick(fromServer?.drop_s, 180),
  }
}
const clampIv = (v: unknown) => { const n = Number(v); return Number.isFinite(n) && n > 0 ? Math.min(30000, Math.max(2000, n)) : DEFAULT_IV_MS }

/**
 * 同意證據（a）＋（b）——（c）「本趟紀錄 consent:true」由引擎讀本趟紀錄後另外判斷（createEngine.begin）。
 *   (a) localStorage「這個團練不再提醒」旗標；
 *   (b) 本分頁 sessionStorage 的「剛在團練頁確認過」標記（≤12 h；團練頁 RunMeetDetailView.navigateLive 在使用者確認同意視窗
 *       或因 (a) 略過視窗的當下寫入）。
 * 兩把 key 的格式與 12 h 規則都在 lib/runMeet.ts（單一真相，這裡不重複）。storage 取不到／丟例外 → 沒有證據（fail-closed）。
 * page.tsx 的開跑前橫幅也用它決定要不要顯示「請從團練頁進入」。
 */
export function hasLiveConsentEvidence(meetId: string): boolean {
  const s = liveConsentSources(meetId)
  return s.flag || s.marker
}
/** 分開回報兩種證據來源（引擎要知道「是不是只靠本分頁標記」才決定要不要把一次性標記用掉）。 */
function liveConsentSources(meetId: string): { flag: boolean; marker: boolean } {
  if (typeof window === 'undefined') return { flag: false, marker: false }
  let ls: Storage | null = null, ss: Storage | null = null
  try { ls = window.localStorage } catch { ls = null }
  try { ss = window.sessionStorage } catch { ss = null }
  return { flag: readLiveConsent(ls, meetId), marker: readLiveConsentSession(ss, meetId, Date.now()) }
}
/** 一次性標記：引擎用它通過同意把關後移除（每次開跑都要從團練頁按鈕進入）。 */
function consumeConsentMarker(meetId: string) {
  try { window.sessionStorage.removeItem(runMeetLiveConsentSessionKey(meetId)) } catch { /* ignore */ }
}

// ── 本趟隱私錨點／暫停旗標／同意的持久化（見檔頭說明）──
// consent:true＝本趟已有同意證據（begin 把關後才會寫）；anchorAcc＝錨點定位精度（公尺，0–300）；
// presence-only 的紀錄不帶 anchor／anchorAcc／paused（不分享位置、沒有隱私區可言）。
interface RunRec { meetId: string; runStartedAt: number; at: number; consent?: boolean; anchor?: { lat: number; lng: number }; anchorAcc?: number; paused?: boolean }
function loadRunRec(meetId: string, runStartedAt: number): RunRec | null {
  try {
    const raw = localStorage.getItem(RUN_KEY)
    if (!raw) return null
    const r = JSON.parse(raw) as RunRec
    if (!r || r.meetId !== meetId || r.runStartedAt !== runStartedAt || !(Date.now() - r.at < RUN_TTL_MS)) return null
    return r
  } catch { return null }
}
/** 這趟跑步是否還在進行：全站 dor_gps_active 還在、而且就是這一趟（startedAt 與本趟開跑時間相同）。 */
function runStillActive(runStartedAt: number): boolean {
  try { const a = readActiveRun(); return !!a && a.startedAt === runStartedAt } catch { return false }
}
function saveRunRec(rec: RunRec) { try { localStorage.setItem(RUN_KEY, JSON.stringify(rec)) } catch { /* localStorage 不可用就算了 */ } }
function clearRunRec() { try { localStorage.removeItem(RUN_KEY) } catch { /* ignore */ } }

interface EngineDeps {
  meetId: string
  runStartedAt: number
  curPosRef: MutableRefObject<Fix | null>
  curPosAtRef: MutableRefObject<number | null>
  peersRef: MutableRefObject<MeetLivePeers>
  pausedRef: MutableRefObject<boolean>
  setPaused: (p: boolean) => void
  setTitle: (t: string | null) => void
  publish: (s: MeetLiveStats, immediate?: boolean) => void
}
interface Engine {
  begin(): void
  dispose(): void
  /** 重算並發布 stats（debug inject 之後；受節流）。 */
  refresh(): void
  onPauseChanged(paused: boolean): void
  /** 最近排程的延遲（毫秒，最多 60 筆）——只給 debug API／E2E 驗「iv×random(0.9–1.1)、saveData ×2」用，與 CPU 負載無關。 */
  scheduleLog(): number[]
}

function createEngine(d: EngineDeps): Engine {
  const sid = randomSid()
  let disposed = false
  let stopped = false
  let phase: 'starting' | 'running' | 'reconnecting' | 'error' = 'starting'
  let startSent = false // 是否曾送出 start（決定結束時要不要送 leave）
  let leaveSent = false
  let grantOk = false
  let consentSent = false // 已有一次 start 成功 → 之後的 start 一律 reauth=true（不再寫同意稽核）
  let consentOk = false // begin 判定：有同意證據（localStorage 旗標／本分頁標記／本趟紀錄 consent）；false → 引擎從頭到尾零請求
  let ownsRec = false // 本引擎寫過本趟紀錄 → dispose 才清（沒同意的引擎不碰別趟／別分頁的紀錄）
  let iv = DEFAULT_IV_MS
  let stale: StaleThresholds = staleFor(DEFAULT_IV_MS)
  let presenceOnly = false
  let meetPt: { lat: number; lng: number } | null = null
  let reauthAt = Infinity
  let rv = 0
  const roster = new Map<number, string>()
  let live = 0
  let failCount = 0
  let timer: ReturnType<typeof setTimeout> | null = null
  let timerDue = 0
  let statsTimer: ReturnType<typeof setInterval> | null = null
  let inflight = false
  let abort: AbortController | null = null
  let lastSendAt = 0
  let lastPSentAt = 0
  let lastPSentPos: { lat: number; lng: number } | null = null
  let anchor: { lat: number; lng: number } | null = null
  let anchorAcc = 0 // 錨點的定位精度（公尺，0–300）：隱私半徑＝200＋anchorAcc；「起點在集合點附近」要 距離＋anchorAcc ≤ 300 才算確定
  let anchorSetAt = 0 // 本引擎設定錨點的時間（0＝從本機紀錄還原：不再校正）；校正只在設定後 30 秒內、且還沒分享過位置時發生
  let anchorFixAt = 0 // 錨點所用定位的時間戳（只接受「更新」的定位來校正）
  let everShared = false // 本趟是否已送出過帶 p 的 /pos（送出就算分享過；校正窗口關閉）
  let privacyOk = false // 目前是否已出隱私區（有遲滯：false→true 要連續 2 個不同定位都在半徑外；true→false 立即）
  let awayStreak = 0
  let lastAwayFixAt: number | null = null
  let backoffActive = false // 有一段退避等待尚未結束（hardBackoff／長 Retry-After）→ 前景事件不可提早打斷（F6）
  let hiddenSinceBackoff = false // 退避開始後頁面曾被隱藏（計時器已被清掉）→ 回前景才需要主動補排
  let fixOk = false // 目前定位可用（精度≤65 m 且年齡≤10 s）
  let shareAllowed = false // 目前允許分享位置（未暫停／非 presence-only／定位可用／不在隱私區）
  let message: string | undefined
  let messageAt: number | undefined
  const grantMissingAt: number[] = []
  const startAts: number[] = [] // 所有 /live/start 請求的時間戳（60 s 滑動視窗，見 START_LIMIT_PER_MIN）
  let hidden = typeof document !== 'undefined' && document.visibilityState === 'hidden'

  // ── 小工具 ──
  const setMessage = (m: string | undefined) => { if (m !== message) { message = m; messageAt = m ? Date.now() : undefined } }
  const clearTimer = () => { if (timer) { clearTimeout(timer); timer = null } }
  const schedLog: number[] = []
  function schedule(ms: number) {
    clearTimer()
    if (disposed || stopped || hidden) return // hidden：回前景由 resume() 補排
    schedLog.push(Math.round(ms))
    if (schedLog.length > 60) schedLog.shift()
    timerDue = Date.now() + ms
    timer = setTimeout(tick, ms)
  }
  function nextDelay(): number {
    let sd = 1
    try { if ((navigator as unknown as { connection?: { saveData?: boolean } }).connection?.saveData) sd = 2 } catch { /* ignore */ }
    return iv * sd * rand(0.9, 1.1)
  }
  function persistRec() {
    if (!consentOk) return // 沒有同意證據的引擎不寫任何東西（begin 已 stop，這裡是防呆）
    ownsRec = true
    saveRunRec({
      meetId: d.meetId, runStartedAt: d.runStartedAt, at: Date.now(), consent: true,
      // presence-only（不限地點團練）：不存錨點／暫停旗標（不分享位置）；只留 consent 讓接續時仍有同意證據。
      ...(presenceOnly ? {} : { anchor: anchor ?? undefined, anchorAcc: anchor ? anchorAcc : undefined, paused: d.pausedRef.current }),
    })
  }
  function writePeers(dots: PeerDot[], active: boolean) {
    d.peersRef.current = { active, dots, stale, selfRing: false }
  }

  // ── 隱私錨點（見檔頭）：取得／校正即落地 ──
  /** 目前定位可否當錨點：座標有效、年齡 ≤60 s、精度 ≤150 m（acc=0＝未知，視為可用，與 page.tsx 的 goodAcc 口徑一致）。 */
  function anchorCandidate(now: number): { fix: Fix; at: number } | null {
    const fix = d.curPosRef.current, at = d.curPosAtRef.current
    if (!fix || at == null || !Number.isFinite(fix.lat) || !Number.isFinite(fix.lng)) return null
    if (now - at > ANCHOR_MAX_AGE_MS || fix.acc > ANCHOR_MAX_ACC_M) return null
    return { fix, at }
  }
  function setAnchor(c: { fix: Fix; at: number }, now: number) {
    anchor = { lat: c.fix.lat, lng: c.fix.lng }
    anchorAcc = clampInt(c.fix.acc, 0, ANCHOR_ACC_CLAMP_M)
    anchorSetAt = now
    anchorFixAt = c.at
    privacyOk = false; awayStreak = 0; lastAwayFixAt = null // 錨點變了 → 隱私區判定重來
    persistRec() // 不等 /live/start 成功：start 一直失敗時頁面被系統砍掉，接續才不會把錨點重設成「當下位置」
  }

  // ── 分享判定（精度／年齡／隱私區／暫停）──
  function evalSharing(now: number) {
    const fix = d.curPosRef.current
    const fixAt = d.curPosAtRef.current
    fixOk = false
    if (fix && fixAt != null && Number.isFinite(fix.lat) && Number.isFinite(fix.lng)) {
      fixOk = now - fixAt <= MAX_FIX_AGE_MS && !(fix.acc > MAX_ACC_M) // acc=0＝未知，沿用 page.tsx 的 goodAcc 口徑視為可用
    }
    if (consentOk && !presenceOnly) {
      const cand = anchorCandidate(now)
      if (cand) {
        if (!anchor) setAnchor(cand, now) // 起點＝第一個可用定位（精度 ≤150 m；不必等到可分享的 ≤65 m）
        else if (anchorSetAt > 0 && !everShared && now - anchorSetAt <= ANCHOR_REFINE_MS && cand.at > anchorFixAt && cand.fix.acc > 0 && cand.fix.acc < anchorAcc) {
          setAnchor(cand, now) // 校正：設定後 30 秒內、尚未分享過、遇到精度嚴格更好的新定位（錨點定位其實偏很多的情況）
        }
      }
    }
    if (fixOk && fix && fixAt != null && anchor) {
      // 起點「確定」在集合點附近（距離＋錨點精度 ≤300 m）→ 不設隱私區；否則半徑＝200＋錨點精度
      const nearMeet = !!meetPt && haversineM(anchor.lat, anchor.lng, meetPt.lat, meetPt.lng) + anchorAcc <= NEAR_MEET_M
      if (nearMeet) { privacyOk = true; awayStreak = 0; lastAwayFixAt = null }
      else {
        const away = haversineM(anchor.lat, anchor.lng, fix.lat, fix.lng) >= PRIVACY_AWAY_M + anchorAcc
        if (!away) { privacyOk = false; awayStreak = 0; lastAwayFixAt = null } // 回到半徑內：立即停止分享
        else if (!privacyOk) { // 遲滯：連續 2 個「不同」定位（定位時間戳不同）都在半徑外才放行，單點飄移不外洩
          if (fixAt !== lastAwayFixAt) { lastAwayFixAt = fixAt; awayStreak++ }
          if (awayStreak >= AWAY_CONFIRM_FIXES) privacyOk = true
        }
      }
    } else { privacyOk = false; awayStreak = 0; lastAwayFixAt = null } // 定位不可用：保守起見重來（恢復後需再連續 2 個定位）
    shareAllowed = !presenceOnly && !d.pausedRef.current && fixOk && privacyOk
  }

  // ── stats ──
  function currentState(): MeetLiveState {
    if (stopped) return 'stopped'
    if (phase === 'starting') return 'starting'
    if (phase === 'error') return 'error'
    if (phase === 'reconnecting') return 'reconnecting'
    if (presenceOnly) return 'presence_only'
    if (d.pausedRef.current) return 'paused'
    if (!fixOk) return 'need_fix'
    return 'live'
  }
  function buildStats(now: number): MeetLiveStats {
    evalSharing(now)
    const state = currentState()
    const sharing = !stopped && grantOk && phase === 'running' && shareAllowed
    const nearest: MeetLiveStats['nearest'] = []
    const fix = d.curPosRef.current
    if (!stopped && !presenceOnly && !d.pausedRef.current && fix) {
      const arr: MeetLiveStats['nearest'] = []
      for (const dot of d.peersRef.current.dots) {
        if (staleSeconds(dot, now) >= stale.grayS) continue // 訊號中斷的點不列入「最近 3 位」
        arr.push({ n: dot.n, name: dot.name, distM: haversineM(fix.lat, fix.lng, dot.lat, dot.lng), bearingDeg: bearingDeg(fix.lat, fix.lng, dot.lat, dot.lng) })
      }
      arr.sort((a, b) => a.distM - b.distM)
      for (const x of arr.slice(0, 3)) nearest.push({ n: x.n, name: x.name, distM: Math.round(x.distM / 10) * 10, bearingDeg: Math.round(x.bearingDeg) % 360 })
    }
    return { state, live, iv, sharing, presenceOnly, message, messageAt, nearest }
  }
  function publish(immediate = false) {
    const s = buildStats(Date.now())
    d.peersRef.current.selfRing = s.sharing // 直接改欄位（ref 不觸發重繪；P3 每幀讀取）
    d.publish(s, immediate)
  }

  // ── 終止／退避 ──
  function abortInflight() { try { abort?.abort() } catch { /* ignore */ } abort = null }
  function stop(msg: string) {
    stopped = true
    grantOk = false
    clearTimer()
    abortInflight()
    setMessage(msg)
    writePeers([], false)
    publish(true)
  }
  /** 開始一段「不可被前景事件提早打斷」的等待（F6）：desktop 的 focus／visible 連發不能把 20–30 s 的退避階梯打回立即重試。 */
  function beginBackoffWait() { backoffActive = true; hiddenSinceBackoff = hidden }
  function endBackoffWait() { backoffActive = false; hiddenSinceBackoff = false }
  function hardBackoff(level: 'reconnecting' | 'error') {
    failCount++
    const delay = BACKOFF_S[Math.min(failCount, BACKOFF_S.length) - 1] * 1000 * rand(0.7, 1.3)
    phase = level === 'error' || failCount >= 3 ? 'error' : 'reconnecting'
    setMessage(phase === 'error' ? '同步暫時中斷，重試中（跑步記錄不受影響）' : undefined)
    beginBackoffWait()
    publish()
    schedule(delay)
  }
  function handleGrantMissing() {
    const now = Date.now()
    while (grantMissingAt.length && now - grantMissingAt[0] > 60000) grantMissingAt.shift()
    if (grantMissingAt.length >= GRANT_MISSING_LIMIT) { hardBackoff('reconnecting'); return } // 自限：每 60 s 最多重建 6 次
    grantMissingAt.push(now)
    grantOk = false
    phase = 'reconnecting'
    publish()
    schedule(rand(100, 1000)) // tick 看到 !grantOk → start(reauth=true)；帶抖動避免同時重建
  }

  /**
   * 失敗分類（契約 §3.1／§3.2 錯誤碼表＋後端定案 handler.go／handler_live.go）：
   *   · 終止（stopped＋訊息，跑步照常）：403／404／409 sid_mismatch／409 outside_window／410／426／429 live_full／
   *     400（consent_required／bad_request／bad_sid／bad_position——client 請求本身有問題，重試也不會好，不進退避迴圈）／413。
   *   · 409 grant_missing → 靜默 start(reauth)（自限 6/min）。
   *   · 429 too_fast → 依 Retry-After 退避、不算錯誤。
   *   · 401 → 一般會員 token 續期（withUserAuth／request()）；續期失敗＝SessionExpiredError → 終止；暫時性失敗＝一般退避。
   *   · 500／503／其他 5xx／網路錯誤／逾時 → 退避 5→10→20→30 s（503＝膠囊 🔴）。
   */
  function onFail(err: unknown) {
    const e = err as { status?: number; message?: string; retryAfterS?: number; body?: unknown } | null
    if (err instanceof SessionExpiredError) { stop('登入已過期，同步已停止'); return }
    const status = typeof e?.status === 'number' ? e.status : 0
    const code = typeof e?.message === 'string' ? e.message : ''
    if (status === 403) {
      if (code === 'revoked') stop('你已不在此團練，同步已停止')
      else if (code === 'not_member') stop('你不是此團練成員，無法同步位置')
      else if (code === 'entry_closed') stop('團練同步功能目前未開放')
      else stop('沒有同步位置的權限')
      return
    }
    if (status === 404) { stop('找不到此團練，同步已停止'); return }
    if (status === 409) {
      if (code === 'grant_missing') { handleGrantMissing(); return }
      if (code === 'sid_mismatch') { stop('另一個視窗正在同步，本頁已停止同步'); return }
      if (code === 'outside_window') { stop(outsideWindowMessage(e?.body, Date.now())); return } // body 另帶 opens_at／closes_at
      hardBackoff('reconnecting'); return
    }
    if (status === 410) { stop(code === 'killed' ? '團練同步功能暫時關閉，仍可自由跑步' : '團練已結束或取消，同步已停止'); return }
    if (status === 426) { stop('請重新整理頁面'); return }
    if (status === 429) {
      if (code === 'live_full') { stop('同步名額已滿，仍可自由跑步'); return }
      // too_fast（伺服器 1.5 s 地板，Retry-After 1–2 s）：退避、不算錯誤（優先 Retry-After；沒帶就 2 s）。
      // 一般限流（middleware RateLimit：error 是句子、Retry-After＝視窗秒數≈60）：一樣等 Retry-After，但要等很久
      // 就不能繼續宣稱 🟢——期間膠囊改「重連中」（下一個成功回應會回到 running）。
      // ⚠️ 不是 too_fast／live_full、也沒有 Retry-After 的 429（例如 proxy／WAF 的限流）：不知道要等多久，
      // 不能每 ~2 s 重試一次打爆它——走一般退避階梯（5→10→20→30 s）。
      const raHdr = e?.retryAfterS
      if (code !== 'too_fast' && !(typeof raHdr === 'number' && raHdr > 0)) { hardBackoff('reconnecting'); return }
      const ra = typeof raHdr === 'number' && raHdr > 0 ? raHdr : 2
      if (ra >= 10) { beginBackoffWait(); if (phase === 'running') { phase = 'reconnecting'; publish() } }
      schedule(Math.max(MIN_GAP_MS, ra * 1000) * rand(1, 1.3))
      return
    }
    if (status === 400) { stop(code === 'consent_required' ? '需要同意分享位置才能同步' : '同步資料異常，已停止同步（跑步記錄不受影響）'); return } // bad_request／bad_sid／bad_position：終止，不重試
    if (status === 413) { stop('同步資料異常，已停止同步（跑步記錄不受影響）'); return } // payload_too_large
    if (status === 503) { hardBackoff('error'); return } // redis_unavailable（fail-closed）：退避、膠囊 🔴
    hardBackoff('reconnecting') // 網路錯誤／逾時／500 server_error／其他 5xx／401 續期暫時失敗：退避重試
  }

  // ── 回應套用 ──
  function applyRoster(list: unknown, newRv: unknown) {
    if (Array.isArray(list)) {
      roster.clear()
      for (const it of list) if (Array.isArray(it) && Number.isFinite(Number(it[0]))) roster.set(Number(it[0]), String(it[1] ?? ''))
    }
    const v = Number(newRv)
    if (Number.isFinite(v)) rv = v
  }
  function onStartOk(r: MeetLiveStartResp) {
    consentSent = true
    grantOk = true
    leaveSent = false
    failCount = 0
    endBackoffWait()
    phase = 'running'
    setMessage(undefined)
    iv = clampIv(r.iv)
    stale = staleFor(iv, r.stale)
    presenceOnly = !!r.presence_only
    const la = Number(r.meet?.lat), ln = Number(r.meet?.lng)
    meetPt = !presenceOnly && Number.isFinite(la) && Number.isFinite(ln) ? { lat: la, lng: ln } : null
    if (presenceOnly) { anchor = null; anchorAcc = 0 } // 不限地點團練不分享位置：記憶體與紀錄都不留錨點（persistRec 對 presence-only 只寫 consent）
    persistRec()
    const reauthIn = Number(r.reauth_in_s)
    reauthAt = Date.now() + Math.max(0, Number.isFinite(reauthIn) ? reauthIn : 600) * 1000
    applyRoster(r.roster, r.rv)
    live = Math.max(1, Math.round(Number(r.live) || 0)) // 至少 1：自己已在同步中（start 回應的 live 還沒算到自己的第一個心跳，否則會閃一下「0 人」）
    d.setTitle(typeof r.meet?.title === 'string' ? r.meet.title : null)
    writePeers(d.peersRef.current.dots, true)
    publish()
    schedule(rand(150, 600)) // start 成功後很快補第一次 pos（取得名冊內他人位置）
  }
  function onPosOk(r: MeetLivePosResp, sentPos: { lat: number; lng: number } | null, sentAt: number) {
    failCount = 0
    endBackoffWait()
    phase = 'running'
    setMessage(undefined)
    iv = clampIv(r.iv)
    stale = staleFor(iv)
    live = Math.max(0, Math.round(Number(r.live) || 0))
    if (Array.isArray(r.roster)) applyRoster(r.roster, r.rv)
    const reauthIn = Number(r.reauth_in_s)
    reauthAt = Date.now() + Math.max(0, Number.isFinite(reauthIn) ? reauthIn : 600) * 1000 // ≤0 → 下一 tick 先 start(reauth=true)
    if (sentPos) { lastPSentAt = sentAt; lastPSentPos = sentPos }
    if (r.own === 'presence_only' && !presenceOnly) { presenceOnly = true; anchor = null; anchorAcc = 0; persistRec() } // 覆寫掉先前存的錨點，只留 consent
    const dots: PeerDot[] = []
    // 互惠規則一致化：暫停分享時伺服器 ≤60 s 內仍可能回他人位置，但 UI 已寫明「你也看不到夥伴」→ 一律不畫。
    if (r.own === 'ok' && !presenceOnly && !d.pausedRef.current && Array.isArray(r.p)) {
      const rx = Date.now()
      for (const it of r.p.slice(0, 100)) {
        if (!Array.isArray(it) || it.length < 5) continue
        const n = Number(it[0]), lat = Number(it[1]), lng = Number(it[2]), age = Number(it[3]), acc = Number(it[4])
        if (!Number.isFinite(n) || !Number.isFinite(lat) || !Number.isFinite(lng) || Math.abs(lat) > 90 || Math.abs(lng) > 180) continue
        dots.push({ n, name: roster.get(n) ?? '跑者', lat, lng, acc: Number.isFinite(acc) ? acc : 0, rxAt: rx, ageS: Number.isFinite(age) && age > 0 ? age : 0 })
      }
    }
    writePeers(dots, true)
    publish()
    schedule(nextDelay())
  }

  // ── 請求 ──
  /** start 配額：視窗內已滿 START_LIMIT_PER_MIN 次 → 回「還要等幾毫秒」（最舊一筆滑出視窗＋小抖動），否則 0。 */
  function startBudgetWaitMs(now: number): number {
    while (startAts.length && now - startAts[0] >= 60000) startAts.shift()
    return startAts.length >= START_LIMIT_PER_MIN ? startAts[0] + 60000 - now + rand(200, 1500) : 0
  }
  async function doStart(reauth: boolean) {
    if (!consentOk) { stop(NO_CONSENT_MESSAGE); return } // 防呆：begin 已把關，沒有同意證據絕不送 start（consent_v 只在證據成立時才送）
    inflight = true
    startSent = true
    lastSendAt = Date.now()
    startAts.push(lastSendAt)
    const ac = new AbortController()
    abort = ac
    let timedOut = false
    const to = setTimeout(() => { timedOut = true; ac.abort() }, REQ_TIMEOUT_MS)
    let r: MeetLiveStartResp | null = null
    let err: unknown = null
    try {
      r = await withUserAuth((t) => meetLiveApi.start(t, d.meetId, reauth ? { pv: PV, sid, reauth: true } : { pv: PV, sid, consent_v: CONSENT_V, reauth: false }, ac.signal))
    } catch (e) { err = e } finally { clearTimeout(to); inflight = false; if (abort === ac) abort = null }
    if (disposed || stopped) return
    if (r) onStartOk(r)
    else onFail(timedOut ? new Error('timeout') : err)
  }
  async function sendPos(now: number) {
    const fix = d.curPosRef.current
    const fixAt = d.curPosAtRef.current
    const body: { pv: 1; sid: string; rv?: number; p?: { la: number; ln: number; ac: number; fa: number } } = { pv: PV, sid }
    if (rv > 0) body.rv = rv
    let sentPos: { lat: number; lng: number } | null = null
    if (shareAllowed && fix && fixAt != null) {
      const moved = !lastPSentPos || haversineM(lastPSentPos.lat, lastPSentPos.lng, fix.lat, fix.lng) >= MIN_MOVE_M
      const keepalive = now - lastPSentAt >= KEEPALIVE_P_MS
      if (moved || keepalive) {
        body.p = { la: round5(fix.lat), ln: round5(fix.lng), ac: clampInt(fix.acc, 0, 10000), fa: clampInt((now - fixAt) / 1000, 0, 600) }
        sentPos = { lat: fix.lat, lng: fix.lng }
        everShared = true // 位置已經離開裝置：錨點校正窗口關閉（見 evalSharing）
      }
    }
    inflight = true
    lastSendAt = now
    const ac = new AbortController()
    abort = ac
    let timedOut = false
    const to = setTimeout(() => { timedOut = true; ac.abort() }, REQ_TIMEOUT_MS)
    let r: MeetLivePosResp | null = null
    let err: unknown = null
    try {
      r = await withUserAuth((t) => meetLiveApi.pos(t, d.meetId, body, ac.signal))
    } catch (e) { err = e } finally { clearTimeout(to); inflight = false; if (abort === ac) abort = null }
    if (disposed || stopped) return
    if (r) onPosOk(r, sentPos, now)
    else onFail(timedOut ? new Error('timeout') : err)
  }

  function tick() {
    timer = null
    if (disposed || stopped || hidden) return
    if (inflight) return // 不重疊：前一個未回就跳過（它完成時會重新排程）
    const now = Date.now()
    evalSharing(now)
    if (!grantOk || now >= reauthAt) { // reauth_in_s ≤ 0／grant_missing／首次 start 重試
      const wait = startBudgetWaitMs(now)
      if (wait > 0) { // 一分鐘內 start 已達伺服器上限：不送、等視窗滑開（期間膠囊維持「重連中」）
        if (phase === 'running') { phase = 'reconnecting'; publish() }
        schedule(wait)
        return
      }
      void doStart(consentSent)
      return
    }
    void sendPos(now)
  }

  // ── 頁面生命週期 ──
  function sendLeave() {
    if (!startSent || leaveSent) return
    leaveSent = true
    try { void withUserAuth((t) => meetLiveApi.leave(t, d.meetId, { pv: PV, sid })).catch(() => { /* 失敗無妨（契約 §3.3） */ }) } catch { /* ignore */ }
  }
  function resume() {
    if (disposed || stopped || inflight) return
    // 退避等待中（20–30 s 的階梯）：桌機 focus／pageshow／visible 連發不能每次都提早重試，否則階梯形同虛設。
    // 例外：退避開始後頁面真的被隱藏過（計時器已被清掉）→ 回前景要補排一次，之後的連發再被忽略。
    if (backoffActive && !hiddenSinceBackoff) return
    hiddenSinceBackoff = false
    const wait = Math.max(0, MIN_GAP_MS - (Date.now() - lastSendAt)) // 冷卻 ≥1.5 s 後立即補一次
    if (timer && timerDue <= Date.now() + wait + 50) return // 已有更早的排程
    schedule(wait)
  }
  const onVis = () => {
    if (document.visibilityState === 'hidden') { hidden = true; if (backoffActive) hiddenSinceBackoff = true; clearTimer() } // 切背景：不送
    else { hidden = false; resume() }
  }
  const onShowOrFocus = () => { if (document.visibilityState === 'hidden') return; hidden = false; resume() }
  const onPageHide = () => { sendLeave() } // 關頁／重整：伺服器立即釋放名額與亮點（舊 sid 的 leave 不會誤刪新頁面的 grant）

  return {
    begin() {
      const rec = loadRunRec(d.meetId, d.runStartedAt)
      // 同意閘門（owner 要求）：沒有證據 → 引擎不啟動——不註冊監聽、不排程、不送任何請求、不寫 storage、不抓錨點；
      // 狀態 'stopped'＋提示句（膠囊／專注模式第一行／toast），跑步本身是一般自由跑。
      const src = liveConsentSources(d.meetId)
      const viaRec = rec?.consent === true
      consentOk = viaRec || src.flag || src.marker
      if (!consentOk) { stop(NO_CONSENT_MESSAGE); return }
      // 隱私錨點先算好：接續同一趟 → 沿用本機紀錄的錨點與精度（不再校正）；否則用開跑當下的定位（精度 ≤150 m、年齡 ≤60 s）。
      const now = Date.now()
      if (rec?.anchor && Number.isFinite(rec.anchor.lat) && Number.isFinite(rec.anchor.lng)) {
        anchor = { lat: rec.anchor.lat, lng: rec.anchor.lng }
        anchorAcc = clampInt(Number(rec.anchorAcc), 0, ANCHOR_ACC_CLAMP_M)
        anchorSetAt = 0
      } else {
        const cand = anchorCandidate(now)
        if (cand) { anchor = { lat: cand.fix.lat, lng: cand.fix.lng }; anchorAcc = clampInt(cand.fix.acc, 0, ANCHOR_ACC_CLAMP_M); anchorSetAt = now; anchorFixAt = cand.at }
      }
      d.setPaused(!!rec?.paused) // 接續同一趟沿用暫停；新的一趟一律重置
      // 同意＋錨點＋暫停旗標一次落地（不等 /live/start 成功：start 一直失敗時頁面被系統砍掉，接續才不會把錨點重設、同意證據也還在）。
      // 若這是不限地點（presence-only）團練，/live/start 回應確認後 onStartOk 會把錨點從紀錄抹掉。
      persistRec()
      // 一次性標記：只靠本分頁標記通過時，寫入 consent:true 後立刻移除——「再跑一次」（新的一趟）要重新從團練頁進入（除非勾了「不再提醒」）。
      if (src.marker && !viaRec && !src.flag) consumeConsentMarker(d.meetId)
      document.addEventListener('visibilitychange', onVis)
      window.addEventListener('pageshow', onShowOrFocus)
      window.addEventListener('focus', onShowOrFocus)
      window.addEventListener('pagehide', onPageHide)
      statsTimer = setInterval(() => { if (!hidden && !disposed) publish() }, 1000) // 讓最近 3 位的距離／方位隨移動更新（state 仍只在變動時才更新）
      publish(true)
      schedule(rand(0, START_DELAY_MAX_MS))
    },
    dispose() {
      if (disposed) return
      disposed = true
      clearTimer()
      if (statsTimer) { clearInterval(statsTimer); statsTimer = null }
      document.removeEventListener('visibilitychange', onVis)
      window.removeEventListener('pageshow', onShowOrFocus)
      window.removeEventListener('focus', onShowOrFocus)
      window.removeEventListener('pagehide', onPageHide)
      abortInflight()
      if (!stopped) sendLeave()
      // 紀錄只在「跑步真的結束」才清：離開頁面（返回鍵／被導走）時這趟還在進行（dor_gps_active 仍是這一趟），
      // 全站 ActiveRunGuard 會把使用者導回 /track，這時暫停旗標、錨點與同意證據都要還在。
      // 沒有同意證據的引擎從沒寫過紀錄（ownsRec=false），也不該清掉別分頁／別趟的。
      if (ownsRec && !runStillActive(d.runStartedAt)) clearRunRec()
      d.peersRef.current = { active: false, dots: [], stale, selfRing: false }
    },
    refresh() { publish() }, // 只給 debug inject 用；一樣受 1 Hz 節流（stats() 讀的是未節流的最新快照，永遠是即時的）
    scheduleLog() { return schedLog.slice() },
    onPauseChanged() {
      persistRec()
      // 暫停：UI 已寫明「你也看不到夥伴」→ 立即清掉畫面上的他人亮點（互惠規則）；恢復：盡快補送一次。
      if (d.pausedRef.current) writePeers([], d.peersRef.current.active)
      else resume()
      publish(true)
    },
  }
}

function sameStats(a: MeetLiveStats, b: MeetLiveStats): boolean {
  if (a === b) return true
  if (a.state !== b.state || a.live !== b.live || a.iv !== b.iv || a.sharing !== b.sharing || a.presenceOnly !== b.presenceOnly || a.message !== b.message || a.messageAt !== b.messageAt) return false
  if (a.nearest.length !== b.nearest.length) return false
  for (let i = 0; i < a.nearest.length; i++) {
    const x = a.nearest[i], y = b.nearest[i]
    if (x.n !== y.n || x.name !== y.name || x.distM !== y.distM || x.bearingDeg !== y.bearingDeg) return false
  }
  return true
}

export interface UseMeetLiveOpts {
  /** 已驗證的團練 UUID（嚴格 regex，page.tsx 解析 ?meet=）；null＝非團練模式。 */
  meetId: string | null
  status: 'idle' | 'tracking' | 'done'
  curPosRef: MutableRefObject<Fix | null>
  curPosAtRef: MutableRefObject<number | null>
  /** 本趟開跑時間（page.tsx 的 startRef）：隱私錨點以它為鍵，接續同一趟才沿用。 */
  startedAtRef: MutableRefObject<number>
}

export function useMeetLive({ meetId, status, curPosRef, curPosAtRef, startedAtRef }: UseMeetLiveOpts) {
  const tracking = status === 'tracking'
  // 可變 ref：他人亮點／陳舊門檻／自己環。更新「不」觸發 page 重繪；Leaflet 圖層與（P3）三套 skin 每幀讀取。
  const meetPeersRef = useRef<MeetLivePeers>({ active: false, dots: [], stale: staleFor(DEFAULT_IV_MS), selfRing: false })
  const [liveStats, setLiveStats] = useState<MeetLiveStats>(IDLE_STATS)
  const [sharePaused, setSharePaused] = useState(false)
  const [meetTitle, setMeetTitle] = useState<string | null>(null)
  const pausedRef = useRef(false)
  const statsRef = useRef<MeetLiveStats>(IDLE_STATS) // 最新（未節流）快照
  const shownRef = useRef<MeetLiveStats>(IDLE_STATS) // 已 setLiveStats 出去的快照（比對用）
  const pubLogRef = useRef<number[]>([]) // 實際觸發重繪的時間戳（僅 debug API 讀；上限 500 筆）
  const engineRef = useRef<Engine | null>(null)
  const pubRef = useRef<{ last: number; timer: ReturnType<typeof setTimeout> | null }>({ last: 0, timer: null })
  const mountedRef = useRef(true)
  useEffect(() => {
    mountedRef.current = true
    return () => { mountedRef.current = false; if (pubRef.current.timer) { clearTimeout(pubRef.current.timer); pubRef.current.timer = null } }
  }, [])

  // liveStats 節流 ≤ 1 Hz（契約 §6）：immediate 只給離散事件（開始／暫停切換／結束），常態更新一律受節流。
  const publish = useCallback((s: MeetLiveStats, immediate?: boolean) => {
    statsRef.current = s
    const p = pubRef.current
    const flush = () => {
      p.timer = null
      p.last = Date.now()
      if (!mountedRef.current) return
      if (sameStats(shownRef.current, statsRef.current)) return // 沒有實質變化就不重繪
      shownRef.current = statsRef.current
      const log = pubLogRef.current
      log.push(p.last)
      if (log.length > 500) log.splice(0, log.length - 500)
      setLiveStats(statsRef.current)
    }
    const since = Date.now() - p.last
    if (immediate || since >= STATS_MIN_INTERVAL_MS) {
      if (p.timer) { clearTimeout(p.timer); p.timer = null }
      flush()
    } else if (!p.timer) {
      p.timer = setTimeout(flush, STATS_MIN_INTERVAL_MS - since)
    }
  }, [])

  const setPaused = useCallback((p: boolean) => { pausedRef.current = p; setSharePaused(p) }, [])

  useEffect(() => {
    if (!tracking || !meetId) return
    const eng = createEngine({
      meetId, runStartedAt: startedAtRef.current, curPosRef, curPosAtRef,
      peersRef: meetPeersRef, pausedRef, setPaused, setTitle: setMeetTitle, publish,
    })
    engineRef.current = eng
    eng.begin()
    return () => {
      eng.dispose()
      if (engineRef.current === eng) engineRef.current = null
      setMeetTitle(null)
      publish(IDLE_STATS, true) // 本趟結束／離開團練模式：膠囊與專注模式狀態行歸零
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 只由 tracking／meetId 驅動；其餘皆為穩定 ref／callback
  }, [tracking, meetId])

  /** 暫停／恢復分享位置（契約 §5「暫停分享位置」）：暫停時只送心跳；因互惠規則也看不到他人。 */
  const toggleShare = useCallback(() => {
    const next = !pausedRef.current
    setPaused(next)
    engineRef.current?.onPauseChanged(next)
  }, [setPaused])

  // window.__meetLiveDebug：僅非 production 或 ?dev=1 掛載，供 P3／E2E（契約 §6）。
  useEffect(() => {
    if (!meetId || typeof window === 'undefined') return
    let dev = process.env.NODE_ENV !== 'production'
    if (!dev) { try { dev = new URLSearchParams(window.location.search).get('dev') === '1' } catch { dev = false } }
    if (!dev) return
    const w = window as unknown as { __meetLiveDebug?: unknown }
    const api = {
      inject(dots: PeerDot[]) {
        const now = Date.now()
        const list: PeerDot[] = []
        for (const x of Array.isArray(dots) ? dots : []) {
          const lat = Number(x?.lat), lng = Number(x?.lng)
          if (!Number.isFinite(lat) || !Number.isFinite(lng)) continue
          list.push({ n: Number(x.n) || 0, name: String(x.name ?? ''), lat, lng, acc: Number(x.acc) || 0, rxAt: Number.isFinite(Number(x.rxAt)) && Number(x.rxAt) > 0 ? Number(x.rxAt) : now, ageS: Number(x.ageS) > 0 ? Number(x.ageS) : 0 })
        }
        meetPeersRef.current = { ...meetPeersRef.current, active: true, dots: list }
        engineRef.current?.refresh()
      },
      stats(): MeetLiveStats { return statsRef.current },
      /** 實際觸發 liveStats 重繪的時間戳（E2E 驗「≤1 Hz」用）。 */
      publishLog(): number[] { return pubLogRef.current.slice() },
      /** 引擎實際選的排程延遲（毫秒；E2E 驗 jitter／saveData ×2，不受 CPU 負載影響）。 */
      scheduleLog(): number[] { return engineRef.current?.scheduleLog() ?? [] },
    }
    w.__meetLiveDebug = api
    return () => { if (w.__meetLiveDebug === api) delete w.__meetLiveDebug }
  }, [meetId])

  return { meetPeersRef, liveStats, sharePaused, toggleShare, meetTitle }
}
