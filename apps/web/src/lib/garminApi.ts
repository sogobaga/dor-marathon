// Garmin 官方直連（Activity API，推送模式）— 前台 API client＋純函式（導回結果文案、卡片狀態、時間顯示）。
//
// 後端契約：services/api/internal/integration/garmin_connect.go（路由掛在 /api/v1/integrations/garmin）：
//   POST /connect     body {consent:true, consent_v}  → {url}（再整頁導去 Garmin 授權）；失敗 400 consent_required／
//                     invalid_consent_version、403 forbidden、503 garmin_disabled／try_again、429 限流
//   GET  /status      → GarminStatus（不受入口閘限制；不呼叫 Garmin）
//   POST /disconnect  → {ok, deleted_activities, garmin_notified, was_connected}（不受入口閘限制；冪等）
//   GET  /callback    （瀏覽器導向，不是 fetch）固定 302 回 /?garmin=connected 或 /?garmin=error&reason=<固定詞彙>
// Garmin 只有「推送」、沒有「拉取」：所以這裡沒有 import／probe 之類的 API，卡片也沒有「匯入數據」按鈕。
//
// ⚠️ 本檔刻意沒有任何 import（也不用 lib/api.ts 的 request／withAuth——它們沒有 export）：
//   1. scripts/verify-garmin-card.mjs 以 Node 原生 type-stripping 直接載入本檔，只能用可剝除型別的語法
//      （不用 enum／namespace／建構子參數屬性）、且不能有沒帶副檔名的相對 import。
//   2. 自帶的 garminRequest 行為與 lib/api.ts request() 對齊：錯誤丟出帶 status／message／retryAfterS／body 的物件，
//      與 lib/userAuth.ts withUserAuth（以 e.status===401 判斷換發 token 後重試）相容——呼叫端一律包 withUserAuth。

const BASE = '/api/v1' // 與 lib/api.ts 的 BASE 同值

/** 同意文字版本。後端只收 GARMIN_CONSENT_VERSIONS（預設 "v1"）內的值；同意說明有實質改動時一起遞增，並同步後端環境變數。 */
export const GARMIN_CONSENT_VERSION = 'v1'

// ── 型別 ─────────────────────────────────────────────────────────────

export interface GarminStatus {
  connected: boolean              // 有「官方直連」連線（舊 Terra 連線不算，見 legacy_terra）
  connected_at?: string | null    // 授權完成時間（RFC3339）
  import_from?: string | null     // 匯入起算點：只匯入這個時間之後開始的活動
  last_data_at?: string | null    // 最後一次收到 Garmin 活動資料的時間
  device_name?: string | null     // 最近一筆活動的手錶型號（不明＝缺席／null）
  needs_reauth?: boolean          // 授權需要更新（refresh 失效、App 世代變更…）→ 顯示「重新授權」
  paused?: boolean                // 使用者在 Garmin Connect 關閉了「活動」資料分享
  legacy_terra?: boolean          // 還留著舊的 Terra-Garmin 連線 → 請重新授權一次改走官方直連
  entry?: 'shown' | 'hidden'      // 入口狀態（與 Dashboard garmin_entry 同一個後端判斷）
}

export interface GarminDisconnectResult {
  ok: boolean
  deleted_activities?: number     // 這次一併刪除的已匯入 Garmin 活動筆數
  garmin_notified?: boolean       // DOR 是否成功通知 Garmin 撤銷授權（false＝要請使用者到 Garmin Connect 自行移除）
  was_connected?: boolean         // false＝原本就沒有直連連線（冪等）
}

export type GarminNoticeKind = 'success' | 'info' | 'warn' | 'error'
export interface GarminNotice { kind: GarminNoticeKind; text: string }

export class GarminApiError extends Error {
  status: number
  retryAfterS?: number
  body: Record<string, unknown> | null
  constructor(status: number, message: string, retryAfterS?: number, body?: Record<string, unknown> | null) {
    super(message)
    this.name = 'GarminApiError'
    this.status = status
    this.retryAfterS = retryAfterS
    this.body = body ?? null
  }
}

// ── API ──────────────────────────────────────────────────────────────

async function garminRequest<T>(path: string, token: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, {
    cache: 'no-store',
    ...init,
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
  })
  // 204／空 body 不解析 JSON
  const text = await res.text()
  let data: any = null
  if (text) {
    try { data = JSON.parse(text) } catch { data = null }
  }
  if (!res.ok) {
    const ra = Number(res.headers.get('Retry-After'))
    throw new GarminApiError(
      res.status,
      typeof data?.error === 'string' ? data.error : 'request failed',
      Number.isFinite(ra) && ra > 0 ? ra : undefined,
      data && typeof data === 'object' ? data : null,
    )
  }
  return data as T
}

export const garminApi = {
  // 不受入口閘限制（只要有連線列就能看）；後端不會呼叫 Garmin。
  status: (token: string) => garminRequest<GarminStatus>('/integrations/garmin/status', token),
  // 回授權網址；呼叫端先用 isGarminAuthUrl 檢查再 window.location.assign（授權在 Garmin 自己的頁面完成）。
  // consent 一律由呼叫端確認使用者已勾選（首次連接）或已同意過（重新授權）才呼叫。
  connect: (token: string) =>
    garminRequest<{ url: string }>('/integrations/garmin/connect', token, {
      method: 'POST',
      body: JSON.stringify({ consent: true, consent_v: GARMIN_CONSENT_VERSION }),
    }),
  disconnect: (token: string) =>
    garminRequest<GarminDisconnectResult>('/integrations/garmin/disconnect', token, { method: 'POST' }),
}

// ── 純函式 ───────────────────────────────────────────────────────────

/** 授權網址防呆：只放行 https 的 garmin.com（含子網域）、不含帳密。後端本來就只會回官方網址，這是前台的第二道保險。 */
export function isGarminAuthUrl(raw: unknown): raw is string {
  if (typeof raw !== 'string' || raw.length === 0 || raw.length > 4096) return false
  let u: URL
  try { u = new URL(raw) } catch { return false }
  if (u.protocol !== 'https:' || u.username || u.password) return false
  const h = u.hostname.toLowerCase()
  return h === 'garmin.com' || h.endsWith('.garmin.com')
}

// ?garmin=error&reason=<固定詞彙> → 中文說明。⚠️ 未知的 reason 一律只顯示通用句、絕不把參數原樣顯示（避免內容注入）。
const GENERIC_FAIL = '連接未完成，請再試一次。'
const REASON_TEXT: Record<string, string> = {
  invalid_state: '連接逾時，或這個連接連結已失效（超過 10 分鐘，或已使用過）。請重新按「連接 Garmin Connect」。',
  state_mismatch: '連接沒有完成：瀏覽器的安全驗證不符（常見於 App 內建瀏覽器或主畫面捷徑）。請改用 Safari／Chrome 重新連接。',
  denied: '連接未完成：你在 Garmin 頁面取消了授權。DOR 沒有取得任何資料，可以再試一次。',
  missing_code: '連接未完成：Garmin 沒有回傳授權結果，DOR 沒有取得任何資料。請再試一次。',
  token_exchange_failed: '連接未完成：Garmin 暫時無法完成授權，DOR 沒有取得任何資料。請稍後再試一次。',
  api_failed: '連接未完成：和 Garmin 連線時發生問題，DOR 沒有取得任何資料。請稍後再試一次。',
  no_activity_permission: '連接未完成：你在 Garmin 授權頁面沒有允許分享「活動」資料，DOR 無法收到你的跑步紀錄。請重新連接，並在 Garmin 頁面保留「活動」的勾選。',
  already_linked: '此 Garmin 帳號已連結到另一個 DOR 帳號，無法重複連接。如要改用目前這個 DOR 帳號，請先到原本連結的 DOR 帳號按「中斷連線」。',
  account_changed: '你這次授權的 Garmin 帳號和先前連接的不同。請先按「中斷連線」，再連接新的 Garmin 帳號。',
  entry_closed: 'Garmin 直連目前未對你的帳號開放。如有疑問請聯絡我們。', // 入口未開放與「被後台限制」在後端是同一個詞彙（刻意不洩漏是哪一種），所以不承諾「稍後再試」
  server_config: 'Garmin 直連目前尚未開通或維護中，請稍後再試。',
  save_failed: '連接未完成：DOR 暫時無法儲存連接結果。請稍後再試一次。',
  disabled: 'Garmin 直連目前尚未開通或維護中，請稍後再試。',
}
export const GARMIN_RESULT_REASONS: readonly string[] = Object.keys(REASON_TEXT)

const CONNECTED_TEXT =
  '✓ Garmin 授權完成。之後你的手錶同步到 Garmin Connect 時，Garmin 會自動把新的跑步／走路／健行紀錄送到 DOR（只匯入連接之後開始的活動，可能需要幾分鐘到數小時才會出現）。'

/** 導回網址參數 → 要顯示的訊息；不是 connected／error 的值回 null（不處理）。 */
export function garminResultNotice(result?: string | null, reason?: string | null): GarminNotice | null {
  if (result === 'connected') return { kind: 'success', text: CONNECTED_TEXT }
  if (result !== 'error') return null
  const r = typeof reason === 'string' ? reason.trim() : ''
  const known = r !== '' && Object.prototype.hasOwnProperty.call(REASON_TEXT, r)
  return { kind: known && r === 'denied' ? 'info' : 'error', text: known ? REASON_TEXT[r] : GENERIC_FAIL }
}

type ErrLike = { name?: unknown; status?: unknown; message?: unknown } | null | undefined

/** API 呼叫失敗 → 給使用者看的一句話。不原樣顯示伺服器訊息（那是給程式判斷用的代碼）。 */
export function garminErrorText(op: 'status' | 'connect' | 'disconnect', e: unknown): string {
  const err = (e && typeof e === 'object' ? e : null) as ErrLike
  if (err?.name === 'SessionExpiredError') return '登入已過期，請重新登入。'
  const status = typeof err?.status === 'number' ? err.status : 0
  const code = typeof err?.message === 'string' ? err.message : ''
  if (status === 429) return '操作太頻繁，請稍候一分鐘再試。'
  if (op === 'connect') {
    if (status === 400 && code === 'consent_required') return '請先勾選同意後再連接。'
    if (status === 400 && code === 'invalid_consent_version') return '說明內容已更新，請重新整理頁面後再試。'
    if (status === 403) return 'Garmin 直連目前未對你的帳號開放。如有疑問請聯絡我們。'
    if (status === 503 && code === 'garmin_disabled') return 'Garmin 直連目前尚未開通，請稍後再試。'
    if (status === 503) return '系統忙碌中，請稍後再試。'
    return '無法連接，請再試一次。'
  }
  if (op === 'disconnect') return '中斷失敗，請稍後再試。'
  return '無法取得 Garmin 連線狀態，請稍後再試。'
}

/** 中斷連線完成後的訊息：帶刪除筆數；Garmin 端撤銷沒成功時，要請使用者自己到 Garmin Connect 移除 DOR。 */
export function garminDisconnectNotice(r?: GarminDisconnectResult | null): GarminNotice {
  if (r && r.was_connected === false) return { kind: 'info', text: '目前沒有連接中的 Garmin。' }
  const n = typeof r?.deleted_activities === 'number' && r.deleted_activities >= 0 ? Math.floor(r.deleted_activities) : null
  let text = n != null ? `已中斷 Garmin 連線，已刪除 ${n} 筆已匯入的 Garmin 紀錄。` : '已中斷 Garmin 連線，已匯入的 Garmin 紀錄已刪除。'
  text += '已獲得的 EXP／DP 等獎勵不受影響。'
  if (r?.garmin_notified === false) {
    return {
      kind: 'warn',
      text: text + 'DOR 這次沒能通知 Garmin 撤銷授權；為確保 Garmin 不再傳送資料，請到 Garmin Connect 的「連結的應用程式（Connected Apps）」設定中移除 DOR。',
    }
  }
  return { kind: 'success', text: text + (r?.garmin_notified === true ? '已通知 Garmin 撤銷授權。' : '') }
}

// ── 卡片狀態 ─────────────────────────────────────────────────────────

export type GarminView =
  | { kind: 'none' }                                                    // 什麼都不顯示
  | { kind: 'loading' }                                                 // 狀態還沒回來（避免已連線者先閃一下「連接」表單）
  | { kind: 'error' }                                                   // 狀態載入失敗（可重試）
  | { kind: 'closed' }                                                  // 入口關閉且沒有連線：只顯示訊息（例如剛中斷／導回錯誤）
  | { kind: 'connect'; legacy: boolean }                                // 尚未連接，入口開放（legacy＝舊 Terra 連線要重新授權）
  | { kind: 'connected'; entryShown: boolean; needsReauth: boolean; paused: boolean }

export interface GarminViewInput {
  entryHint?: string | null       // Dashboard 的 garmin_entry
  hasConnectionHint: boolean      // Dashboard 的 connected_sources 含 garmin
  status: GarminStatus | null
  loaded: boolean                 // status 請求已結束（成功或失敗）
  failed: boolean                 // 最近一次 status 請求失敗
  hasNotice: boolean              // 有訊息要顯示（導回結果、剛中斷…）
}

/** 卡片要不要打 /status：入口開放，或 Dashboard 說已有 Garmin 連線；其餘（入口關閉且沒連線）零請求。 */
export function garminShouldFetch(entryHint: string | null | undefined, hasConnectionHint: boolean): boolean {
  return entryHint === 'shown' || hasConnectionHint
}

export function garminView(i: GarminViewInput): GarminView {
  const fetching = garminShouldFetch(i.entryHint, i.hasConnectionHint)
  if (fetching && !i.loaded) return { kind: 'loading' }
  if (fetching && i.failed && !i.status) return { kind: 'error' }
  const entryShown = (i.status?.entry ?? i.entryHint) === 'shown'
  if (i.status?.connected === true) {
    return { kind: 'connected', entryShown, needsReauth: i.status.needs_reauth === true, paused: i.status.paused === true }
  }
  if (entryShown) return { kind: 'connect', legacy: i.status?.legacy_terra === true }
  return i.hasNotice ? { kind: 'closed' } : { kind: 'none' }
}

// ── 時間顯示（台北時區，與賽事頁一致；不依賴瀏覽器所在時區）────────────────

function taipeiParts(iso?: string | null): Record<string, string> | null {
  if (!iso) return null
  const t = new Date(iso).getTime()
  if (isNaN(t)) return null
  const f = new Intl.DateTimeFormat('en-US', {
    timeZone: 'Asia/Taipei', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  })
  const out: Record<string, string> = {}
  for (const p of f.formatToParts(t)) out[p.type] = p.value
  return out
}

/** 2026/10/03（台北日期）；無效回空字串。 */
export function garminDay(iso?: string | null): string {
  const p = taipeiParts(iso)
  return p ? `${p.year}/${p.month}/${p.day}` : ''
}

/** 10/03 09:12（台北時間）；無效回空字串。 */
export function garminShortTime(iso?: string | null): string {
  const p = taipeiParts(iso)
  return p ? `${p.month}/${p.day} ${p.hour}:${p.minute}` : ''
}

/** 剛剛／N 分鐘前／N 小時前／N 天前；無效回空字串；時鐘偏差造成的未來時間視為「剛剛」。 */
export function garminRelativeAgo(iso: string | null | undefined, nowMs: number): string {
  if (!iso) return ''
  const t = new Date(iso).getTime()
  if (isNaN(t)) return ''
  const min = Math.floor((nowMs - t) / 60000)
  if (min < 1) return '剛剛'
  if (min < 60) return `${min} 分鐘前`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr} 小時前`
  return `${Math.floor(hr / 24)} 天前`
}

export const GARMIN_STALE_AFTER_MS = 48 * 60 * 60 * 1000

/** 超過 48 小時沒收到資料（沒收過就從連接時間起算）。任一時間無效 → false（不亂提示）。 */
export function garminDataStale(lastDataAt: string | null | undefined, connectedAt: string | null | undefined, nowMs: number): boolean {
  const ref = lastDataAt || connectedAt
  if (!ref) return false
  const t = new Date(ref).getTime()
  return !isNaN(t) && nowMs - t > GARMIN_STALE_AFTER_MS
}
