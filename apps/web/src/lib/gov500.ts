// 運動部「揮汗有禮・全民動起來」500.gov.tw 活動：截圖模式（screenshot mode）純函式。
//
// 2026-09-06 規則變動（owner 定案）：500.gov.tw 只收「手機截圖鍵」截出的運動 App 原始紀錄畫面
// （未裁切、看得到日期與達標數值），四種情形一律退件——
//   ① 裁切／拼貼過的圖片　② 非 App 畫面的照片（例如翻拍手錶螢幕）
//   ③ 文字編輯或另外產生的圖　④ 手動輸入的數據
// 我們原本的 canvas 產「證明圖」（lib/runProof.ts，已刪除）正是③——不管畫得多像，本質仍是一張
// 「生成圖」，必被退件。全面改用 components/RunProofScreen.tsx 這個「原始紀錄」全螢幕畫面，讓使用者
// 自己用手機系統截圖鍵擷取——那才是①②③④都不成立的合格證明。
//
// 本檔只放「無 React／無 DOM」的純函式，元件只負責渲染：達標判定（單次 5 公里或 30 分鐘擇一即可，
// owner 2026-09-06 定案）與「本週已截圖」提醒標記（純前端 localStorage——沒有伺服器可信來源，
// 純粹提醒使用者「這筆這週截過了」，不是任何審核依據，也不代表真的已上傳）。
// 可用 apps/web/scripts（或本次驗證用的 scratchpad gov500_check.mjs）以 Node 原生 TS type-stripping
// 直接 import 這個檔驗證，比照 lib/runMeet.ts + scripts/verify-run-meet.mjs 的既有慣例。

export const GOV500_DISTANCE_KM = 5
export const GOV500_TIME_S = 30 * 60 // 30 分鐘
export const GOV500_QUALIFY_COPY = '單次 5 公里或 30 分鐘皆可'

export interface Gov500Qualify {
  ok: boolean
  byDistance: boolean
  byTime: boolean
  shortfallText: string // ok 時為空字串；否則例如「還差 1.2 公里或 6 分鐘」
}

/** 單次跑步是否達成本週任務（距離、時間擇一達標即可，兩者互不相斥）。 */
export function qualifiesGov500(input: { distanceKm: number; totalS: number }): Gov500Qualify {
  const distanceKm = Math.max(0, Number.isFinite(input.distanceKm) ? input.distanceKm : 0)
  const totalS = Math.max(0, Number.isFinite(input.totalS) ? input.totalS : 0)
  const byDistance = distanceKm >= GOV500_DISTANCE_KM
  const byTime = totalS >= GOV500_TIME_S
  const ok = byDistance || byTime
  if (ok) return { ok, byDistance, byTime, shortfallText: '' }
  const kmShort = Math.max(0, GOV500_DISTANCE_KM - distanceKm)
  const minShort = Math.max(0, Math.ceil((GOV500_TIME_S - totalS) / 60))
  return { ok, byDistance, byTime, shortfallText: `還差 ${kmShort.toFixed(1)} 公里或 ${minShort} 分鐘` }
}

// ── 紀錄鍵（runKey）───────────────────────────────────────────────────
// 同一趟跑步在「剛跑完（/track，只有 startRef 開跑時間戳）」與「歷史列表（/track/history，
// GpsRunHistory.started_at 是伺服器存回的同一個時間戳）」兩處都要能標記到同一支鑰匙，但兩邊送出/
// 存回的 ISO 字串可能因為資料庫來回而在秒以下的精度或格式（Z vs +00:00）有落差——floor 到分鐘
// 可以避開這個落差，同一趟跑步「開始的那一分鐘」不會變。純提醒用途，不需要更高精度。
export function gov500RunKey(startedAtIso: string): string {
  const d = new Date(startedAtIso)
  if (isNaN(d.getTime())) return startedAtIso
  return d.toISOString().slice(0, 16) // "2026-09-06T02:15"
}

// ── 本週已截圖標記（純前端 localStorage）───────────────────────────────
const SHOTS_KEY = 'dor:gov500_shots'
const TAIPEI_TZ = 'Asia/Taipei'

type ShotsMap = Record<string, string> // runKey -> 標記當下的 ISO 時間

function readShots(): ShotsMap {
  if (typeof localStorage === 'undefined') return {}
  try {
    const raw = localStorage.getItem(SHOTS_KEY)
    if (!raw) return {}
    const parsed = JSON.parse(raw)
    return parsed && typeof parsed === 'object' ? (parsed as ShotsMap) : {}
  } catch {
    return {}
  }
}

/** 使用者按下「我截好了 → 前往上傳」時呼叫：記下這個 runKey 這次被標記的時間。 */
export function markGov500Shot(runKey: string, at: Date = new Date()): void {
  if (!runKey || typeof localStorage === 'undefined') return
  try {
    const shots = readShots()
    shots[runKey] = at.toISOString()
    localStorage.setItem(SHOTS_KEY, JSON.stringify(shots))
  } catch {
    /* 純前端小提醒，寫入失敗（無痕視窗/容量滿）不影響主流程 */
  }
}

/** 這個 runKey 是否「本週」（台北時區、週一為週首的 ISO 週）已標記過截圖。 */
export function hasGov500ShotThisWeek(runKey: string, now: Date = new Date()): boolean {
  if (!runKey) return false
  const iso = readShots()[runKey]
  if (!iso) return false
  const shotDate = new Date(iso)
  if (isNaN(shotDate.getTime())) return false
  return isoWeekKeyTaipei(shotDate) === isoWeekKeyTaipei(now)
}

/**
 * 台北時區的 ISO 8601 週鍵，例如 "2026-W37"（週一為週首、跨年以週四所在年份為準）。
 * 標準演算法：https://en.wikipedia.org/wiki/ISO_week_date —— 只是先用 Intl 把時間點轉成
 * 台北當地的年/月/日，再套用這個公式（避免直接對 UTC 值套用時區位移造成跨日誤差）。
 */
export function isoWeekKeyTaipei(d: Date): string {
  const fmt = new Intl.DateTimeFormat('en-US', { timeZone: TAIPEI_TZ, year: 'numeric', month: '2-digit', day: '2-digit' })
  const p: Record<string, string> = {}
  for (const part of fmt.formatToParts(d)) p[part.type] = part.value
  const date = new Date(Date.UTC(Number(p.year), Number(p.month) - 1, Number(p.day)))
  const dayNum = date.getUTCDay() || 7 // 週一=1…週日=7
  date.setUTCDate(date.getUTCDate() + 4 - dayNum) // 移到本週四（ISO 週以週四所在的年份/週數為準）
  const yearStart = new Date(Date.UTC(date.getUTCFullYear(), 0, 1))
  const week = Math.ceil(((date.getTime() - yearStart.getTime()) / 86400000 + 1) / 7)
  return `${date.getUTCFullYear()}-W${String(week).padStart(2, '0')}`
}
