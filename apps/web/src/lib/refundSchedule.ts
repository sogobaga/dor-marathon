// 取消退費規則的「天數→日期」換算與「表格列」拆分，鏡射後端 race.ComputeCancellation／
// race.ResolveCancellationPolicy（services/api/internal/race/cancel_policy.go）的天數比對規則，
// 確保後台設定畫面、前台簡章頁的顯示，永遠跟實際退費計算一致。純函式，不呼叫任何 API，
// 供 apps/web/src/app/admin/CancelPolicyEditor.tsx（後台即時換算）與
// apps/web/src/components/BrochureScreen.tsx（前台簡章頁尾表格）共用。
import type { CancellationPolicy, CancellationTier } from './api'

/** 依 days_before 由大到小排序（跟後端排序規則一致：取第一個 daysBefore >= tier.days_before 的比例）。 */
export function sortTiersDesc(tiers: CancellationTier[]): CancellationTier[] {
  return [...tiers].sort((a, b) => b.days_before - a.days_before)
}

/** 距賽事開始日往前推 N 天的確切時刻：startDate - N*24h，跟後端 startDate.Add(-N*24h) 完全一致
 *  （非僅日曆日期相減——賽事開賽時間若非 00:00，換算出的截止時刻也會帶著同樣的時分）。 */
export function cutoffDateForDaysBefore(startDate: Date, daysBefore: number): Date {
  return new Date(startDate.getTime() - daysBefore * 24 * 60 * 60 * 1000)
}

function pad2(n: number): string {
  return String(n).padStart(2, '0')
}

/** YYYY/MM/DD（前台簡章頁尾表格用，比照使用者習慣的「取消時間」欄位格式，不特別標時分）。 */
export function formatCutoffDate(d: Date): string {
  return `${d.getFullYear()}/${pad2(d.getMonth() + 1)}/${pad2(d.getDate())}`
}

/** YYYY/MM/DD HH:mm（後台即時換算提示用，需要精確到時分方便對照）。 */
export function formatCutoffDateTime(d: Date): string {
  return `${formatCutoffDate(d)} ${pad2(d.getHours())}:${pad2(d.getMinutes())}`
}

export interface RefundScheduleRow {
  daysBefore: number
  ratio: number
  cutoff: Date
}

/** 把政策拆成前台可直接渲染的表格列：只保留「可能真正生效」的級距——deadline_days 是硬性下限
 *  （daysBefore < deadline_days 一律不可申請取消，等同不予退費，見 ComputeCancellation），若某級距
 *  的 days_before < deadline_days，代表天數掉到那麼低之前就已經被 deadline_days 擋掉，該級距永遠不
 *  會生效，顯示出來只會誤導使用者，故濾除；ratio===0 的級距效果等同「不予退費」，併入表格外最後一列
 *  （之後取消：不予退費），不重複列出。回傳依 days_before 由大到小排序。 */
export function buildRefundScheduleRows(policy: CancellationPolicy, startDate: Date): RefundScheduleRow[] {
  const reachable = sortTiersDesc((policy.tiers ?? []).filter((t) => t.days_before >= policy.deadline_days && t.ratio > 0))
  return reachable.map((t) => ({
    daysBefore: t.days_before,
    ratio: t.ratio,
    cutoff: cutoffDateForDaysBefore(startDate, t.days_before),
  }))
}
