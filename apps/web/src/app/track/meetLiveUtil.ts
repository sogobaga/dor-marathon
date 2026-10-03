// 團練同步跑（Group Run Live）純函式共用模組——契約 docs/runmeet/GROUP_RUN_LIVE_CONTRACT.md §5／§6／§7。
// useMeetLive（P2）、Leaflet 亮點圖層（P2）、專注模式資訊行（P2）與三套 skin 的泡泡渲染（P3）共用同一份，
// 名稱截斷／陳舊度分級／文案不各寫一份，避免分岔。全部純函式、無 React、無 DOM。

import type { MeetLiveStats, PeerDot, StaleThresholds } from './meetLiveTypes'

/**
 * 沒有「使用者在團練頁確認過同意視窗」的證據時，同步引擎不啟動（零請求）、狀態 'stopped'＋這句話（owner 要求：
 * 深連結 /track?meet=<id> 不得略過團練頁的同意流程；consent_v 是稽核紀錄、必須為真）。膠囊／專注模式第一行（meetStateParts）、
 * 開跑前橫幅（page.tsx）與引擎（useMeetLive.ts）共用同一句——比對與顯示不各寫一份。
 */
export const NO_CONSENT_MESSAGE = '尚未同意位置分享：請從團練頁的『開始跑步』進入'

/** 他人亮點主色（契約 §7：Leaflet／cute＝#FF8A3D）。 */
export const MEET_ORANGE = '#FF8A3D'
/** 陳舊（gray）亮點的灰色空心環。 */
export const MEET_GRAY = '#9aa0a6'

const EARTH_R = 6371000
const RAD = Math.PI / 180

export function haversineM(aLat: number, aLng: number, bLat: number, bLng: number): number {
  const dLat = (bLat - aLat) * RAD, dLng = (bLng - aLng) * RAD
  const x = Math.sin(dLat / 2) ** 2 + Math.cos(aLat * RAD) * Math.cos(bLat * RAD) * Math.sin(dLng / 2) ** 2
  return EARTH_R * 2 * Math.atan2(Math.sqrt(x), Math.sqrt(1 - x))
}

/** 從 a 看向 b 的方位角（度，0＝正北、順時針，範圍 [0,360)）。 */
export function bearingDeg(aLat: number, aLng: number, bLat: number, bLng: number): number {
  const p1 = aLat * RAD, p2 = bLat * RAD, dl = (bLng - aLng) * RAD
  const y = Math.sin(dl) * Math.cos(p2)
  const x = Math.cos(p1) * Math.sin(p2) - Math.sin(p1) * Math.cos(p2) * Math.cos(dl)
  return (Math.atan2(y, x) / RAD + 360) % 360
}

const COMPASS = ['↑', '↗', '→', '↘', '↓', '↙', '←', '↖'] as const
/** 8 方位羅盤箭頭（契約 §7 專注模式最近 3 位：「小明 約120m ↗」）。 */
export function compassArrow(deg: number): string {
  const d = ((deg % 360) + 360) % 360
  return COMPASS[Math.round(d / 45) % 8]
}

/** 「約120m」／「約1.2km」。 */
export function fmtDistApprox(m: number): string {
  if (!Number.isFinite(m) || m < 0) return ''
  if (m < 1000) return `約${Math.max(10, Math.round(m / 10) * 10)}m`
  return `約${(m / 1000).toFixed(1)}km`
}

// 名稱截斷（契約 §7：≤ 6 中文字／10 英文字，超過「…」）。寬度單位：CJK／全形／emoji 算 1，其餘（半形）算 0.6
// ——10 個半形字＝6.0＝6 個中文字，兩者同一個上限。以 code point 計（Array.from），不會把 emoji 代理對切半。
const NAME_MAX_W = 6
function charW(cp: number): number {
  return cp >= 0x2e80 || (cp >= 0x1100 && cp <= 0x11ff) ? 1 : 0.6
}
export function truncName(name: string): string {
  const chars = Array.from(String(name ?? ''))
  let w = 0
  for (let i = 0; i < chars.length; i++) {
    w += charW(chars[i].codePointAt(0) ?? 0)
    if (w > NAME_MAX_W + 1e-6) return chars.slice(0, i).join('') + '…'
  }
  return chars.join('')
}

export type StaleLevel = 'ok' | 'fade' | 'gray' | 'drop'
/** 陳舊秒數（契約 §6）：(Date.now() − rxAt)/1000 + ageS。 */
export function staleSeconds(d: Pick<PeerDot, 'rxAt' | 'ageS'>, nowMs: number): number {
  return Math.max(0, (nowMs - d.rxAt) / 1000) + Math.max(0, d.ageS)
}
/** 陳舊分級：< fadeS 正常；≥ fadeS 淡出；≥ grayS 灰色空心環／「訊號中斷」；≥ dropS 不畫。 */
export function staleLevel(s: number, t: StaleThresholds): StaleLevel {
  if (s >= t.dropS) return 'drop'
  if (s >= t.grayS) return 'gray'
  if (s >= t.fadeS) return 'fade'
  return 'ok'
}

/**
 * `409 outside_window` 的停止訊息：後端回應 body 另帶 opens_at／closes_at（RFC3339）→ 告訴使用者什麼時候開放／
 * 何時已結束（裝置在地時區；非今天加 M/D）。body 缺欄位或解析失敗就退回不帶時間的通用句。
 */
export function outsideWindowMessage(body: unknown, nowMs: number): string {
  const base = '目前不在團練同步時段'
  const b = (body && typeof body === 'object' ? body : {}) as { opens_at?: unknown; closes_at?: unknown }
  const opens = Date.parse(String(b.opens_at ?? '')), closes = Date.parse(String(b.closes_at ?? ''))
  const p2 = (n: number) => String(n).padStart(2, '0')
  const fmt = (ms: number) => {
    const d = new Date(ms), now = new Date(nowMs)
    const day = d.toDateString() === now.toDateString() ? '' : `${d.getMonth() + 1}/${d.getDate()} `
    return `${day}${p2(d.getHours())}:${p2(d.getMinutes())}`
  }
  if (Number.isFinite(opens) && nowMs < opens) return `${base}（${fmt(opens)} 開放），仍可自由跑步`
  if (Number.isFinite(closes) && nowMs >= closes) return `${base}（已於 ${fmt(closes)} 結束），仍可自由跑步`
  return `${base}，仍可自由跑步`
}

/**
 * 專注模式第二行：最近 3 位「小明 約120m ↗ · 阿華 約340m ↙ · 另 5 人」（契約 §7）。
 * 「另 N 人」＝同步中的其他人（live 含自己）扣掉已列出的；沒有可列的位置時依原因給一句說明。
 */
export function meetNearestText(s: MeetLiveStats): string {
  if (s.presenceOnly || s.state === 'stopped' || s.state === 'idle' || s.state === 'starting') return ''
  if (s.nearest.length === 0) {
    if (s.state === 'paused') return '暫停分享期間看不到夥伴'
    if (!s.sharing) return '開始分享位置後才看得到夥伴'
    return s.live > 1 ? '夥伴位置更新中…' : '目前只有你在同步'
  }
  // 每一位的「名字 距離 箭頭」之間用不換行空白（NBSP）——寬字型（retro 像素字）下折行只會發生在「 · 」分隔處，
  // 不會把箭頭單獨甩到下一行。
  const parts = s.nearest.map((x) => `${truncName(x.name)} ${fmtDistApprox(x.distM)} ${compassArrow(x.bearingDeg)}`)
  const others = Math.max(0, s.live - 1 - s.nearest.length)
  if (others > 0) parts.push(`另 ${others} 人`)
  return parts.join(' · ')
}

/**
 * 同步狀態文案（膠囊與專注模式第一行共用單一真相）。base 是契約 §7 指定的固定字串；note 是補充說明
 * （例如「暫停時你也看不到夥伴」互惠規則，契約 §5 要求 UI 必須寫明）。
 */
export function meetStateParts(s: MeetLiveStats): { base: string; note?: string } {
  switch (s.state) {
    case 'idle':
    case 'starting':
      return { base: '🟡 同步連線中' }
    case 'live':
      return s.sharing
        ? { base: `🟢 同步中 · ${s.live} 人` }
        : { base: `🟢 同步中 · ${s.live} 人`, note: '起點附近暫不分享位置（夥伴看不到你，你也看不到夥伴）' }
    case 'need_fix':
      return { base: '⚪ 搜尋 GPS 中（夥伴看不到你）' }
    case 'presence_only':
      return { base: `🟢 ${s.live} 人在跑`, note: '不限地點團練只顯示人數，不分享位置' }
    case 'paused':
      return { base: '⏸ 已暫停分享', note: '夥伴看不到你，你也看不到夥伴' }
    case 'reconnecting':
      return { base: '🟡 重連中' }
    case 'error':
      return { base: '🔴 同步中斷', note: '重試中，跑步記錄不受影響' }
    case 'stopped':
      // 從沒開始同步（沒有同意證據）不是「中斷」：用中性圖示，不嚇人；提示句照樣放在補充說明。
      if (s.message === NO_CONSENT_MESSAGE) return { base: '⚪ 未同步', note: NO_CONSENT_MESSAGE }
      return { base: '🔴 同步中斷', note: s.message || undefined }
  }
}
