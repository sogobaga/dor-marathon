// 團練邀請（run meets）前端純函式：時間（台北時區）、距離分級文字、配額文案、CTA 狀態矩陣。
//
// 這裡刻意只放「無 React / 無 DOM / 無網路」的純函式，元件只負責渲染 —— 因為這幾組規則
// （尤其 CTA 矩陣與台北時區換算）錯了不會有畫面報錯，只會默默顯示錯的按鈕/時間，
// 必須能用 scripts/verify-run-meet.mjs 逐格驗證。
//
// ⚠️ 中文顯示文案一律用「團練」，不得出現「跑團」二字（賽事已有「跑團分組」，撞名會混淆）。

import type {
  RunMeetCard, RunMeetComment, RunMeetDistanceBand, RunMeetMyState, RunMeetPhase, RunMeetQuota, RunMeetReactionKind, RunMeetStatus,
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

/** 24 小時制 hh（0–23）→「上午/下午 h:mm」（12 小時制，時 1–12 不補零、分補零）。
 *  ⚠️ 兩個最容易寫錯的邊界：0 時（凌晨）是「上午 12:xx」，12 時（中午）是「下午 12:xx」——
 *  不是「上午 0 點」或「下午 0 點」。fmtMeetAt 是全站顯示預計時間的唯一函式（2026-08-31
 *  使用者回報「分不清楚是上午 4:30 還是下午 4:30」），這兩個邊界在 verify-run-meet.mjs 有專門測資釘住。 */
// 時段用語（2026-08-31 使用者定案）：左閉右開，一律以台北時間判定。
//   [0,4) 凌晨　[4,6) 清晨　[6,12) 上午　[12,17) 下午　[17,19) 傍晚　[19,24) 晚上
// ⚠️ 小時採 12 小時制但 0 點保留 0（「凌晨 0:30」，不是「上午 12:30」——中文會把後者讀成中午），
//    中午 12:xx 也保留 12（「下午 12:30」）。所以換算是「>12 才減 12」，不是常見的 hh%12。
const DAY_PERIODS: ReadonlyArray<{ from: number; label: string }> = [
  { from: 19, label: '晚上' },
  { from: 17, label: '傍晚' },
  { from: 12, label: '下午' },
  { from: 6, label: '上午' },
  { from: 4, label: '清晨' },
  { from: 0, label: '凌晨' },
]
function fmtHour12(hh: number, mm: number): string {
  const period = DAY_PERIODS.find((p) => hh >= p.from)?.label ?? '凌晨'
  const h12 = hh > 12 ? hh - 12 : hh
  return `${period} ${h12}:${pad2(mm)}`
}

/** 「8/31（日）上午 6:00」／「8/31（日）下午 4:30」；同一天（台北）則為「今天 上午 6:00」。
 *  一律以台北時間判定日期與上午/下午（沿用 taipeiParts，不吃裝置時區）。 */
export function fmtMeetAt(iso: string, now: Date = new Date()): string {
  const t = taipeiParts(iso)
  const n = taipeiParts(now)
  const time = fmtHour12(t.hh, t.mm)
  if (t.y === n.y && t.m === n.m && t.d === n.d) return `今天 ${time}`
  return `${t.m}/${t.d}（${t.wd}）${time}`
}

/** 表單下方的反算確認行：「將於 8/31（日）06:00（台北時間）開跑」。 */
export function fmtMeetAtConfirm(iso: string): string {
  if (!iso) return ''
  const t = taipeiParts(iso)
  return `將於 ${t.m}/${t.d}（${t.wd}）${pad2(t.hh)}:${pad2(t.mm)}（台北時間）開跑`
}

/** 「9/12（五）09:00–10:30」／「今天 09:00–10:30」：有結束時間（migration 168）時的區間顯示，
 *  是全站顯示「時間」欄位的唯一入口——元件不應該再自己組字串，一律呼叫這支。
 *  ends_at 為 null（migration 168 上線前建立的舊資料）時退回 fmtMeetAt，只顯示開始時間，
 *  維持既有樣式不變。
 *  ⚠️ 時間部分刻意改用 24 小時制 HH:mm，不沿用 fmtMeetAt 的「上午/下午」時段語意——
 *  範圍一旦跨越時段邊界會變成「上午 11:00–下午 12:30」這種不好讀的組合，24 小時制的
 *  區間寫法（09:00–10:30）才是慣用寫法。日期前綴（今天／M/D（週））仍沿用 fmtMeetAt 的判斷。
 *  ⚠️ 後端已強制 ends_at 與 meet_at 同一台北日曆日，這裡不需要、也不應該再顯示第二個日期。 */
export function fmtMeetRange(meetAtIso: string, endsAtIso: string | null | undefined, now: Date = new Date()): string {
  if (!endsAtIso) return fmtMeetAt(meetAtIso, now)
  const t = taipeiParts(meetAtIso)
  const n = taipeiParts(now)
  const e = taipeiParts(endsAtIso)
  const range = `${pad2(t.hh)}:${pad2(t.mm)}–${pad2(e.hh)}:${pad2(e.mm)}`
  // ⚠️ 空格規則跟 fmtMeetAt 一致，不是「前綴 + 空格 + 時間」無腦套用：「今天」後面才加空格，
  // 「M/D（週）」的全形括號後面直接接時間、不留空格（比對 fmtMeetAt 的 `今天 ${time}` 與
  // `${t.m}/${t.d}（${t.wd}）${time}` 這兩支寫法）——之前這裡兩種前綴都無條件補一個空格，
  // 會讓「M/D（週）」那支多出一格看起來像排版錯誤，被 verify-run-meet.mjs 的新測資抓到。
  return t.y === n.y && t.m === n.m && t.d === n.d ? `今天 ${range}` : `${t.m}/${t.d}（${t.wd}）${range}`
}

// ─────────────────────────────────────────────────────────────
// 生命週期三態（phase-2，2026-09-04 使用者定案）
//
// 「結束後該團練就會自動關閉」：anchor 是 effectiveEnd = COALESCE(ends_at, meet_at)，不是
// meet_at 本身——舊資料（migration 168 上線前建立、ends_at=null）effectiveEnd 退回 meet_at，
// 維持既有行為不變。now >= effectiveEnd 才是「已結束」；meet_at 到 effectiveEnd 之間是
// 「進行中」（可加入、可留言，跟 upcoming 一樣不受限——只有 ended 才關閉加入/編輯/新留言）。
// ⚠️ 這支純函式跟後端 is_ended／CardView.phase 必須算出同一個答案——後端已直接回 phase
// 欄位（見 api.ts RunMeetCard.phase），大多數畫面應該直接讀那個欄位，不必再自己重算一次；
// 只有 share 頁（/m/[id]，SSR 專用的精簡 DTO，沒有 phase 欄位）需要在前端呼叫這支自己算。
// ─────────────────────────────────────────────────────────────

/** now 落在 meet_at／effectiveEnd 的哪一段。只有沒有 phase 欄位可用的地方（目前只有分享頁）
 *  才需要呼叫這支——其餘畫面一律直接讀後端算好的 RunMeetCard.phase，避免前後端各算一次
 *  卻因為時鐘/邊界條件不同而兜不起來。 */
export function meetPhase(meetAtIso: string, endsAtIso: string | null | undefined, now: Date = new Date()): RunMeetPhase {
  const t = now.getTime()
  const start = new Date(meetAtIso).getTime()
  const effectiveEnd = endsAtIso ? new Date(endsAtIso).getTime() : start
  if (t >= effectiveEnd) return 'ended'
  if (t >= start) return 'ongoing'
  return 'upcoming'
}

/** 三態顯示文字：即將開始／進行中／已結束。全站狀態徽章的唯一文案來源，不要在元件裡各自寫死。 */
export const MEET_PHASE_LABEL: Record<RunMeetPhase, string> = {
  upcoming: '即將開始',
  ongoing: '進行中',
  ended: '已結束',
}

export interface MeetCountdown {
  text: string      // 「還有 2 天」／「還有 3 小時」／「還有 25 分鐘」／「已結束」
  urgent: boolean   // 6 小時內 → 橘色強調
  ended: boolean
}

/** 相對時間。分鐘/小時/天三段，不做「x 週」（團練最多只能設到 90 天後，天數夠用）。
 *  ⚠️ 這支只認 meet_at，一旦開始時間過了就直接判「已結束」——有 ends_at（migration 168）之後
 *  這句話對「進行中」的團練是錯的，元件不要再直接拿這支的文字顯示，改用下面的 phaseCountdown。 */
export function meetCountdown(iso: string, now: Date = new Date()): MeetCountdown {
  const ms = new Date(iso).getTime() - now.getTime()
  if (!Number.isFinite(ms) || ms <= 0) return { text: '已結束', urgent: false, ended: true }
  const min = Math.floor(ms / 60000)
  if (min < 60) return { text: `還有 ${Math.max(1, min)} 分鐘`, urgent: true, ended: false }
  const hr = Math.floor(min / 60)
  if (hr < 24) return { text: `還有 ${hr} 小時`, urgent: hr < 6, ended: false }
  return { text: `還有 ${Math.floor(hr / 24)} 天`, urgent: false, ended: false }
}

/** 卡片／詳情倒數文字：upcoming 沿用 meetCountdown 倒數到開始（含 urgent 6 小時內強調）；
 *  ongoing（已開始、還沒到 effectiveEnd）改顯示「進行中」，不能再用 meetCountdown 對 meet_at
 *  單獨算出的「已結束」——那句話會跟旁邊的 PhaseBadge／狀態文字互相矛盾；ended 統一顯示
 *  「已結束」，一律以 phase 為準，不再各自對 meet_at/ends_at 重算一次。 */
export function phaseCountdown(phase: RunMeetPhase, meetAtIso: string, now: Date = new Date()): MeetCountdown {
  if (phase === 'ongoing') return { text: '進行中', urgent: false, ended: false }
  if (phase === 'ended') return { text: '已結束', urgent: false, ended: true }
  return meetCountdown(meetAtIso, now)
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
// 集合地點顯示（migration 161「不限地點」）
//
// ⚠️ no_location=true 的團，後端 region/place_label 固定是「不限」佔位文字（見 service.go
//    normalizeNoLocation）。顯示邏輯必須先判斷 no_location 這個旗標本身，不能直接把
//    region/place_label 兩欄拼接——那樣會顯示成「不限・不限」這種沒有意義的字面組合。
// ─────────────────────────────────────────────────────────────

export const NO_LOCATION_TEXT = '不限'

export interface RunMeetLocationFields { no_location: boolean; region: string; place_label: string }

/** 集合地點的圖示：一般定點 📍；不限地點 🌏。 */
export function runMeetLocationIcon(noLocation: boolean): string {
  return noLocation ? '🌏' : '📍'
}

/** 集合地點顯示文字。no_location 一律回「不限地點」固定文字；一般定點回
 *  「region · place_label」（沒有 place_label 時只顯示 region，理論上不會發生，因為
 *  兩欄都是必填，這裡只是防禦性處理）。 */
export function runMeetLocationText(m: RunMeetLocationFields): string {
  if (m.no_location) return '不限地點'
  return m.place_label ? `${m.region} · ${m.place_label}` : m.region
}

// ─────────────────────────────────────────────────────────────
// 卡片封面佔位（migration 162：cover_url 為 null 時要顯示什麼）
//
// ⚠️ cover_url==null 可能是「私密團未解鎖」「show_cover=false」或單純「沒圖」，
//    三種原因都用同一個 null 表示（見後端 model.go resolveCoverURL）——前端不需要、
//    也拿不到區分它們的資訊，佔位規則只需要 is_private 這一個輸入。
// 使用者原話：「如果是需要密碼的，改成顯示『鎖頭』，不要顯示團練名稱的第一個字。」
// 非私密團沒有變動：維持既有的「標題首字」佔位。
// ─────────────────────────────────────────────────────────────

export const COVER_FALLBACK_LOCK = '🔒'

/** 無封面時的佔位字：私密團回鎖頭，非私密團回標題首字（無標題時回「團」）。 */
export function coverFallbackGlyph(isPrivate: boolean, title: string): string {
  return isPrivate ? COVER_FALLBACK_LOCK : (title || '團').slice(0, 1)
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
// 「發起團練」表單開啟前的配額/VIP 閘門（使用者定案的出口矩陣）
//
// ⚠️「再辦一次」（複製既有團練設定建立新團練）走的是同一套 POST /run-meets 建立流程，
//    會正常消耗一次發起次數，因此開啟表單前必須套用與「＋ 發起團練」完全相同的判定——
//    不可讓使用者填完整張表單才在送出時被 403/409 擋下。RunMeetScreen 的 openCreate 與
//    openDuplicate 都呼叫這支，任何一邊改判定邏輯不會漏改另一邊。
// ─────────────────────────────────────────────────────────────

export type CreateGate = 'ok' | 'vip' | 'wait'

/** 'ok'＝可直接開表單；'vip'＝非 VIP（政策開關要求 VIP，或次數用完但升級有解）→ 跳 VIP 引導；
 *  'wait'＝VIP 本月次數也用完 → 無解法，只能提示等下個月（不可再跳 VIP 引導，那是死路）。 */
export function createGate(quota: { requires_vip: boolean; is_vip: boolean; remaining: number }): CreateGate {
  if (quota.requires_vip && !quota.is_vip) return 'vip'
  if (quota.remaining <= 0) return quota.is_vip ? 'wait' : 'vip'
  return 'ok'
}

/** 'wait' 時的固定提示文案（VIP 本月次數用完，唯一出口是等下個月）。 */
export const CREATE_GATE_WAIT_TEXT = '本月團練發起次數為 0，請等待下個月更新次數。'

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

// ─────────────────────────────────────────────────────────────
// 留言討論串（migration 159）：純函式部分——表情彙總、@提及前綴組裝、cursor 分頁、
// 樂觀更新／回捲、軟刪遮蔽、頂層留言插入／回覆插入。元件只呼叫這些函式改本地陣列，
// 錯了不會有畫面報錯（只會默默少一則留言或表情數字兜不起來），所以一定要能被
// scripts/verify-run-meet.mjs 逐條驗證。
// ─────────────────────────────────────────────────────────────

export type ReactionCount = { kind: RunMeetReactionKind; count: number }
export interface ReactionPill extends ReactionCount { emoji: string; label: string; mine: boolean }

/** 把後端已排序好的 reactions（count desc, kind asc）套上 emoji/label，並標出「我按過的那顆」。 */
export function reactionPills(reactions: ReactionCount[], myReaction: RunMeetReactionKind | null): ReactionPill[] {
  return reactions.map((r) => {
    const meta = REACTION_META.find((m) => m.kind === r.kind)
    return { kind: r.kind, count: r.count, emoji: meta?.emoji ?? '', label: meta?.label ?? '', mine: r.kind === myReaction }
  })
}

/** 依 count desc、kind asc 排序（比照後端 thread.go sortReactions，樂觀更新期間也要同順序，
 *  否則伺服器回應蓋回來時 pill 順序會跳動）。就地排序＋回傳同一個陣列，方便鏈式使用。 */
export function sortReactionCounts(rs: ReactionCount[]): ReactionCount[] {
  rs.sort((a, b) => (b.count !== a.count ? b.count - a.count : a.kind.localeCompare(b.kind)))
  return rs
}

/** 點下表情鈕當下先算出的本地狀態（樂觀更新）；targetKind=null 代表取消。
 *  一人一則只能一種：換新的會先扣掉舊的那顆再加上新的那顆。失敗時呼叫端用回傳值之前的
 *  reactions/myReaction 快照回捲即可，這裡不處理回捲（純函式不留副作用）。 */
export function optimisticReactionUpdate(
  reactions: ReactionCount[], myReaction: RunMeetReactionKind | null, targetKind: RunMeetReactionKind | null,
): { reactions: ReactionCount[]; myReaction: RunMeetReactionKind | null } {
  const counts = new Map(reactions.map((r) => [r.kind, r.count]))
  if (myReaction) {
    const c = (counts.get(myReaction) ?? 1) - 1
    if (c <= 0) counts.delete(myReaction); else counts.set(myReaction, c)
  }
  if (targetKind) counts.set(targetKind, (counts.get(targetKind) ?? 0) + 1)
  const next = Array.from(counts.entries()).map(([kind, count]) => ({ kind, count }))
  return { reactions: sortReactionCounts(next), myReaction: targetKind }
}

/** 「@對方顯示名稱 」——回覆輸入框預填前綴；空白/未帶名稱時不強加 @。 */
export function mentionPrefix(name: string): string {
  const trimmed = (name || '').trim()
  return trimmed ? `@${trimmed} ` : ''
}

/** 對留言按「回覆」時，POST 要帶的 parent_id：對頂層留言＝它自己的 id；
 *  對回覆再按回覆＝該回覆所屬的頂層留言 id（後端只允許一層，前端不該送出違規請求）。 */
export function replyTargetId(comment: { id: string; parent_id: string | null }): string {
  return comment.parent_id ?? comment.id
}

/** next_cursor 是否代表「還有下一頁」（後端無效 cursor 一律當第一頁處理、不報錯，
 *  前端這裡只需要判斷有沒有下一頁游標）。 */
export function hasMoreCursor(nextCursor: string | null | undefined): boolean {
  return typeof nextCursor === 'string' && nextCursor.length > 0
}

/** 「查看全部留言(N)」：預設只載入前 10 筆，N<=10 時不必再開完整討論區。 */
export function showViewAllComments(total: number): boolean {
  return total > 10
}

/** 「查看全部 N 則回覆」／收起回覆：reply_count 超過預覽則數（預設 2）才顯示這顆按鈕。 */
export function showReplyToggle(replyCount: number, previewCount = 2): boolean {
  return replyCount > previewCount
}

/** 展開／收起回覆的按鈕文案。 */
export function replyToggleLabel(replyCount: number, expanded: boolean): string {
  return expanded ? '收起回覆' : `查看全部 ${replyCount} 則回覆`
}

/** 分頁合併：把新載入的一頁接到既有陣列後面，用 id 去重（游標分頁理論上不會重疊，
 *  但同一筆留言若因為併發新增而落在邊界上，這裡兜底不重複渲染）。 */
export function mergeCommentPages(existing: RunMeetComment[], incoming: RunMeetComment[]): RunMeetComment[] {
  const seen = new Set(existing.map((c) => c.id))
  const merged = existing.slice()
  for (const c of incoming) {
    if (!seen.has(c.id)) { merged.push(c); seen.add(c.id) }
  }
  return merged
}

/** 新頂層留言送出成功後插入陣列最前面（新的在前，呼應 GET 列表的排序）。 */
export function insertTopComment(items: RunMeetComment[], comment: RunMeetComment): RunMeetComment[] {
  return [comment, ...items]
}

/** 新回覆送出成功後接到所屬頂層留言的 replies 陣列尾端（回覆正序，對話由舊到新）；
 *  reply_count 同步 +1（後端下一次整批重新整理時仍會被權威值覆蓋，這裡只是本地即時反映）。
 *  parentId 對不到任何一則頂層留言時（理論上不會發生，因為送出前已用 replyTargetId 算過）原樣返回。 */
export function insertReply(items: RunMeetComment[], parentId: string, reply: RunMeetComment): RunMeetComment[] {
  return items.map((c) => (c.id === parentId ? { ...c, reply_count: c.reply_count + 1, replies: [...c.replies, reply] } : c))
}

/** 把某則留言（頂層或其回覆）的表情狀態換成伺服器回傳的權威值——PUT/DELETE 表情端點都回
 *  「該留言更新後的完整狀態」，直接覆蓋本地即可，不需要再自己兜計算。 */
export function updateCommentReaction(
  items: RunMeetComment[], commentId: string, reactions: ReactionCount[], myReaction: RunMeetReactionKind | null,
): RunMeetComment[] {
  return items.map((c) => {
    if (c.id === commentId) return { ...c, reactions, my_reaction: myReaction }
    if (c.replies.some((r) => r.id === commentId)) {
      return { ...c, replies: c.replies.map((r) => (r.id === commentId ? { ...r, reactions, my_reaction: myReaction } : r)) }
    }
    return c
  })
}

/** 軟刪某則留言（頂層或回覆）的本地遮蔽——比照後端 thread.go maskDeleted：deleted=true、
 *  body=''、can_delete=false、reactions=[]、my_reaction=null；佔位仍留在陣列裡，回覆照常顯示。 */
export function markCommentDeleted(items: RunMeetComment[], commentId: string): RunMeetComment[] {
  const mask = (c: RunMeetComment): RunMeetComment => ({ ...c, deleted: true, body: '', can_delete: false, reactions: [], my_reaction: null })
  return items.map((c) => {
    if (c.id === commentId) return mask(c)
    if (c.replies.some((r) => r.id === commentId)) {
      return { ...c, replies: c.replies.map((r) => (r.id === commentId ? mask(r) : r)) }
    }
    return c
  })
}

/** 「查看全部留言（N）」按鈕文案。 */
export function viewAllCommentsLabel(total: number): string {
  return `查看全部留言（${total}）`
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

// ─────────────────────────────────────────────────────────────
// 載入態判定（SWR）
//
// ⚠️ 不可只用 SWR 的 isLoading 當「載入中」：切換查詢金鑰的空窗期（例如 useUser 解析完把
// uid 從 'guest' 換成真實 id）會出現「isLoading=false、data 與 error 都還是 undefined」的一幀，
// 只判 isLoading 的話那一幀會掉進錯誤分支，使用者一進頁面就先看到「載入失敗」，
// 以為系統壞了（2026-08-31 使用者回報）。
// 判定順序固定：pending（還在等） → error（真的失敗且沒有可顯示的資料） → 空狀態 → 內容。
export function isFetchPending(isLoading: boolean, data: unknown, error: unknown): boolean {
  return isLoading || (data === undefined && error === undefined)
}
/** 真的該顯示錯誤：有 error、不在 pending、且沒有既有資料可續用（重新驗證失敗時不該把畫面清空）。 */
export function shouldShowError(isLoading: boolean, data: unknown, error: unknown, hasItems: boolean): boolean {
  return Boolean(error) && !isFetchPending(isLoading, data, error) && !hasItems
}
export const LOADING_TEXT = '資料載入中，請稍後。'

/**
 * isAbsorbing 「資料已到手，但畫面用的累積清單還沒同步」的那一幀。
 *
 * ⚠️ 列表用 useEffect 把 SWR 的 data 併進累積 state（分頁要保留前幾頁），
 * 而 effect 在 render 之後才跑——於是有一幀是「data 有 10 筆、items 還是 0 筆」。
 * 那一幀 isFetchPending 已經是 false，會直接掉進空狀態分支，使用者一進頁面
 * 就先閃過「找不到符合條件的團練」（2026-08-31 使用者回報）。
 * 這個判定把那一幀也算成載入中；若後端回的本來就是空陣列（真的沒有團練），
 * fetchedCount 為 0 → 回 false → 正常顯示空狀態。
 */
export function isAbsorbing(itemCount: number, fetchedCount: number | undefined): boolean {
  return itemCount === 0 && (fetchedCount ?? 0) > 0
}
