// 團練邀請（run meets）前端純函式：時間（台北時區）、距離分級文字、配額文案、CTA 狀態矩陣。
//
// 這裡刻意只放「無 React / 無 DOM / 無網路」的純函式，元件只負責渲染 —— 因為這幾組規則
// （尤其 CTA 矩陣與台北時區換算）錯了不會有畫面報錯，只會默默顯示錯的按鈕/時間，
// 必須能用 scripts/verify-run-meet.mjs 逐格驗證。
//
// ⚠️ 中文顯示文案一律用「團練」，不得出現「跑團」二字（賽事已有「跑團分組」，撞名會混淆）。

import type {
  RunMeetCard, RunMeetDistanceBand, RunMeetMyState, RunMeetQuota, RunMeetStatus,
} from './api'

// ─────────────────────────────────────────────────────────────
// 台北時區
//
// ⚠️ 團練一定是台灣的實體集合，所以「使用者在 datetime-local 打的牆上時間」一律當台北時間解讀，
//    不吃裝置時區（既有 5 處 datetime-local 全在後台且吃裝置時區，這是前台第一個刻意分歧）。
// ─────────────────────────────────────────────────────────────

export const TAIPEI_TZ = 'Asia/Taipei'
const TAIPEI_OFFSET_MIN = -480 // getTimezoneOffset() 的台北值

/** 裝置時區是否為台北（不是就要在表單顯示「會以台北時間儲存」的灰字提示）。 */
export function isDeviceTaipei(now: Date = new Date()): boolean {
  return now.getTimezoneOffset() === TAIPEI_OFFSET_MIN
}

type TaipeiParts = { y: number; m: number; d: number; hh: number; mm: number; wd: string }

/** 取某個時間點在台北時區的年月日時分與星期（單一字元：日一二三四五六）。 */
export function taipeiParts(iso: string | Date): TaipeiParts {
  const d = typeof iso === 'string' ? new Date(iso) : iso
  const fmt = new Intl.DateTimeFormat('zh-TW', {
    timeZone: TAIPEI_TZ, year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', hour12: false, weekday: 'narrow',
  })
  const p: Record<string, string> = {}
  for (const part of fmt.formatToParts(d)) p[part.type] = part.value
  return {
    y: Number(p.year), m: Number(p.month), d: Number(p.day),
    hh: Number(p.hour === '24' ? '00' : p.hour), mm: Number(p.minute),
    wd: p.weekday || '',
  }
}

const pad2 = (n: number) => String(n).padStart(2, '0')

/** "2026-08-31T06:00"（使用者打的牆上時間，當台北時間解讀）→ ISO 字串。 */
export function taipeiLocalToISO(v: string): string {
  if (!v) return ''
  const d = new Date(`${v}:00+08:00`)
  return isNaN(d.getTime()) ? '' : d.toISOString()
}

/** ISO → datetime-local 的 value（台北牆上時間，供編輯表單回填）。 */
export function isoToTaipeiLocalInput(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const t = taipeiParts(d)
  return `${t.y}-${pad2(t.m)}-${pad2(t.d)}T${pad2(t.hh)}:${pad2(t.mm)}`
}

/** 「8/31（日）06:00」；同一天（台北）則為「今天 06:00」。 */
export function fmtMeetAt(iso: string, now: Date = new Date()): string {
  const t = taipeiParts(iso)
  const n = taipeiParts(now)
  const time = `${pad2(t.hh)}:${pad2(t.mm)}`
  if (t.y === n.y && t.m === n.m && t.d === n.d) return `今天 ${time}`
  return `${t.m}/${t.d}（${t.wd}）${time}`
}

/** 表單下方的反算確認行：「將於 8/31（日）06:00（台北時間）開跑」。 */
export function fmtMeetAtConfirm(iso: string): string {
  if (!iso) return ''
  const t = taipeiParts(iso)
  return `將於 ${t.m}/${t.d}（${t.wd}）${pad2(t.hh)}:${pad2(t.mm)}（台北時間）開跑`
}

export interface MeetCountdown {
  text: string      // 「還有 2 天」／「還有 3 小時」／「還有 25 分鐘」／「已結束」
  urgent: boolean   // 6 小時內 → 橘色強調
  ended: boolean
}

/** 相對時間。分鐘/小時/天三段，不做「x 週」（團練最多只能設到 90 天後，天數夠用）。 */
export function meetCountdown(iso: string, now: Date = new Date()): MeetCountdown {
  const ms = new Date(iso).getTime() - now.getTime()
  if (!Number.isFinite(ms) || ms <= 0) return { text: '已結束', urgent: false, ended: true }
  const min = Math.floor(ms / 60000)
  if (min < 60) return { text: `還有 ${Math.max(1, min)} 分鐘`, urgent: true, ended: false }
  const hr = Math.floor(min / 60)
  if (hr < 24) return { text: `還有 ${hr} 小時`, urgent: hr < 6, ended: false }
  return { text: `還有 ${Math.floor(hr / 24)} 天`, urgent: false, ended: false }
}

// ─────────────────────────────────────────────────────────────
// 距離分級
//
// ⚠️ 後端刻意不回精確距離（回 0.23 km 這種值可讓攻擊者換多組座標查詢、三角定位反推出精確
//    地點，等於繞過整套地點分層設計）。前端只會拿到 band 字串，這裡就只負責翻成中文。
// ─────────────────────────────────────────────────────────────

const BAND_LABEL: Record<RunMeetDistanceBand, string> = {
  lt1: '1 公里內',
  '1to3': '1–3 公里',
  '3to5': '3–5 公里',
  '5to10': '5–10 公里',
  gt10: '10 公里以上',
}

/** 距離分級 → 中文；未帶（非附近搜尋）或未知值回空字串。 */
export function distanceBandLabel(band?: string | null): string {
  if (!band) return ''
  return BAND_LABEL[band as RunMeetDistanceBand] ?? ''
}

// ─────────────────────────────────────────────────────────────
// 配額文案
// ─────────────────────────────────────────────────────────────

// 「＋ 發起團練（尚可發起 3 次）」——配額資訊一律整合進發起按鈕本身。
//
// ⚠️ 不要再把配額做成獨立徽章（例如「本月 0/10 · VIP」）或放在功能入口旁：
// 使用者實測回饋是「看不出那是剩餘還是已用」，而放在入口旁的「本月還有 N 次」
// 更會被誤讀成「我還能加入 N 個團練」。配額只跟「發起」這個動作有關，
// 就只寫在發起按鈕上，並用「尚可發起」明確指向剩餘數。
export function createBtnText(remaining: number, short = false): string {
  const head = short ? '＋ 發起' : '＋ 發起團練'
  return remaining > 0 ? `${head}（尚可發起 ${remaining} 次）` : head
}

/** 「9 月 1 日」——配額重置日（台北）。 */
export function resetDayText(resetsAt: string): string {
  if (!resetsAt) return ''
  const t = taipeiParts(resetsAt)
  return `${t.m} 月 ${t.d} 日`
}

/** 「本月剩餘 1 次（9 月 1 日重置）」；用完時「本月發起次數已用完（9 月 1 日重置）」。 */
export function remainingText(remaining: number, resetsAt: string): string {
  const day = resetDayText(resetsAt)
  const tail = day ? `（${day}重置）` : ''
  return remaining > 0 ? `本月剩餘 ${remaining} 次${tail}` : `本月發起次數已用完${tail}`
}

// ─────────────────────────────────────────────────────────────
// CTA 狀態矩陣（規格 5.7）
//
// 判斷順序即優先序，改動時務必連同 scripts/verify-run-meet.mjs 一起更新。
// ⚠️ 「已解鎖密碼」不等於「已加入」：private 且 has_access=false 時主 CTA 是「輸入密碼」，
//    通過密碼後才會變成加入／申請；精確地點仍需正式加入才看得到（見 api.ts 的分層說明）。
// ─────────────────────────────────────────────────────────────

export type RunMeetCtaAction =
  | 'none'        // 不可點
  | 'join'        // 直接加入
  | 'apply'       // 送出申請（審核制）
  | 'unlock'      // 先開密碼彈窗（解鎖後才會變成 join/apply）
  | 'manage'      // 發起人管理面板
export type RunMeetSecondaryAction = 'share' | 'withdraw' | 'leave'

export interface RunMeetCta {
  label: string
  action: RunMeetCtaAction
  disabled: boolean
  variant: 'primary' | 'outline' | 'muted'
  secondary?: { label: string; action: RunMeetSecondaryAction }
}

export interface CtaInput {
  my_state: RunMeetMyState
  status: RunMeetStatus
  is_ended: boolean
  is_private: boolean
  approval_required: boolean
  has_access: boolean
  member_count: number
  capacity: number
}

const SHARE = { label: '📤 分享', action: 'share' as const }

export function runMeetCta(c: CtaInput): RunMeetCta {
  const full = c.member_count >= c.capacity
  const joinable = c.status === 'open' && !c.is_ended

  // 1) 發起人：任何狀態都是「管理團練」
  if (c.my_state === 'owner') {
    return { label: '管理團練', action: 'manage', disabled: false, variant: 'primary', secondary: SHARE }
  }
  // 2) 被剔除：永久擋（發起人可在成員管理解除）
  if (c.my_state === 'kicked') {
    return { label: '你已被移出這個團練', action: 'none', disabled: true, variant: 'muted' }
  }
  // 3) 已加入
  if (c.my_state === 'joined') {
    const canLeave = !c.is_ended && c.status !== 'cancelled'
    return {
      label: '已加入 ✓', action: 'none', disabled: true, variant: 'outline',
      secondary: canLeave ? { label: '退出團練', action: 'leave' } : undefined,
    }
  }
  // 4) 申請中（審核制）。已結束的團未決申請視為失效，不發婉拒信、也不再能撤回。
  if (c.my_state === 'pending') {
    if (c.is_ended) return { label: '申請已失效', action: 'none', disabled: true, variant: 'muted' }
    return {
      label: '⏳ 審核中…', action: 'none', disabled: true, variant: 'muted',
      secondary: { label: '撤回申請', action: 'withdraw' },
    }
  }
  // 5) 生命週期擋下（順序：已結束 → 已關閉 → 已中止）
  //    ⚠️ 規格 5.7 第三格寫「已下架」，這裡改用「已中止」：本專案「下架」＝後台強制下架
  //    （hidden_by_admin，前台任何端點一律 404，根本不會渲染到 CTA），沿用會誤導玩家。
  //    ⚠️ 「已取消」已改名「已中止」（後端狀態模型重整）：closed＝暫停收人、其他都照舊，
  //    cancelled＝停止一切加入動作、可再重新開啟；兩者都不是終局狀態，措辭不能再暗示「結束了」。
  if (c.is_ended) return { label: '已結束', action: 'none', disabled: true, variant: 'muted' }
  if (c.status === 'closed') return { label: '已關閉，不再收新成員', action: 'none', disabled: true, variant: 'muted' }
  if (c.status === 'cancelled') return { label: '已中止', action: 'none', disabled: true, variant: 'muted' }
  // 6) 婉拒冷卻中（24 小時後恢復可再申請；後端仍會再擋一次）
  if (c.my_state === 'rejected') {
    return { label: '申請未通過', action: 'none', disabled: true, variant: 'muted' }
  }
  // 7) 額滿（pending 不占名額，所以這條只影響尚未加入者）
  if (full) {
    return { label: '已額滿', action: 'none', disabled: true, variant: 'muted', secondary: SHARE }
  }
  // 8) 可加入：left（曾退出）顯示「重新加入」，其餘依「公開/私密 × 自由/審核」四格
  const rejoin = c.my_state === 'left'
  if (c.is_private && !c.has_access) {
    return {
      label: c.approval_required ? '🔒 輸入密碼並申請' : '🔒 輸入密碼加入',
      action: 'unlock', disabled: false, variant: 'primary', secondary: SHARE,
    }
  }
  if (c.approval_required) {
    return { label: rejoin ? '重新申請加入' : '申請加入', action: 'apply', disabled: false, variant: 'primary', secondary: SHARE }
  }
  return { label: rejoin ? '重新加入' : '加入團練', action: 'join', disabled: false, variant: 'primary', secondary: SHARE }
}

/** 卡片／詳情共用：把 API 物件轉成 CTA 判定所需的最小輸入（欄位名一致，只是收斂型別）。 */
export function ctaInputOf(m: RunMeetCard): CtaInput {
  return {
    my_state: m.my_state, status: m.status, is_ended: m.is_ended,
    is_private: m.is_private, approval_required: m.approval_required,
    has_access: m.has_access, member_count: m.member_count, capacity: m.capacity,
  }
}

// ─────────────────────────────────────────────────────────────
// 其他小工具
// ─────────────────────────────────────────────────────────────

export const REACTION_META: { kind: 'like' | 'fire' | 'muscle' | 'pray' | 'heart'; emoji: string; label: string }[] = [
  { kind: 'like', emoji: '👍', label: '讚' },
  { kind: 'fire', emoji: '🔥', label: '熱血' },
  { kind: 'muscle', emoji: '💪', label: '一起跑' },
  { kind: 'pray', emoji: '🙏', label: '加油' },
  { kind: 'heart', emoji: '❤️', label: '喜歡' },
]

/** 「6 / 12 人」（含發起人）。 */
export function memberCountText(memberCount: number, capacity: number): string {
  return `${memberCount} / ${capacity} 人`
}

/** 人數進度百分比（0–100，供進度條）。 */
export function memberPct(memberCount: number, capacity: number): number {
  if (capacity <= 0) return 0
  return Math.max(0, Math.min(100, Math.round((memberCount / capacity) * 100)))
}

/** 建立前確認彈窗的次級說明（非 VIP 多一行 VIP 權益）。主文另外寫死在元件內、一字不改。
 *  ⚠️ vipCap / vipImages 必須來自 GET /run-meets/quota 的 vip_cap / vip_image_limit
 *  （後台可調的 runmeet_quota_vip / runmeet_images_vip），不得寫死 10 / 4。 */
export function confirmSubText(
  remaining: number, resetsAt: string, isVip: boolean, vipCap: number, vipImages: number,
): string[] {
  const lines = [
    `${remainingText(remaining, resetsAt)}。`,
    '團練建立後即使關閉或刪除，次數也不會返還。',
  ]
  if (!isVip) lines.push(`VIP 會員每月可發起 ${vipCap} 次，並可上傳最多 ${vipImages} 張圖片。`)
  return lines
}
