// Terra／Strava 串接結束公告（announce）— 前台純函式：狀態正規化、卡片視圖、公告與提示文案、API 錯誤→固定訊息。
//
// 後端契約（services/api/internal/integration/wearablesunset，Terra／Strava 兩支 /status 回應都帶 sunset 欄位）：
//   GET /integrations/strava/status、/integrations/terra/status → { …, sunset: { state: 'off'|'announce', date?: 'YYYY-MM-DD' } }
//   announce 期間所有「連接」入口一律擋下：API 回 409 { error, code:'wearable_sunset', state, date }
//   （刻意不是 401／403／404：那三個會被每日資安報告算成「登入失敗／不存在」；也不能是 5xx，會進告警）；
//   瀏覽器導回前台帶 ?strava=sunset／?terra=sunset（前台對應成固定訊息）。既有連線照常同步到結束日。
//   off（或欄位缺席＝舊後端）＝完全維持現狀，畫面不得有任何差異。
//
// ⚠️ 本檔刻意沒有任何 import、enum、建構子參數屬性：scripts/verify-wearable-sunset.mjs 以 Node 原生 type-stripping
//    直接載入本檔做純函式驗證（做法比照其他可被 verify 腳本直接載入的純函式 lib）。
// ⚠️ 對外文案守則（擁有者規定，verify 腳本會掃本檔與每個文案函式的輸出）：繁體中文；不提任何第三方裝置品牌的名字、
//    不使用擁有者明令禁用的詞（清單在 verify 腳本）、不暗示任何品牌背書；「COROS 直連」只作為功能名稱出現，且只在
//    該入口對這位會員開放（corosShown）時才提。狀態與日期一律當純文字顯示（不經 HTML 注入路徑）。
// ⚠️ 文案只陳述已確定的事實，不做未經擁有者核准的承諾（例如「之後會再通知你」）；對「同步說不準」的連線列
//    （見 SunsetBannerContext.syncUncertain）不承諾「照常同步」，也不引導改用其他品牌。

/** 後端 wearablesunset.ErrorCode：被擋下的 API 回應體 code 欄位。 */
export const SUNSET_ERROR_CODE = 'wearable_sunset'
/** 後端 wearablesunset.RefusalStatus：被擋下的 API 回應狀態碼（409）。 */
export const SUNSET_ERROR_STATUS = 409
/** 後端 wearablesunset.ResultValue：瀏覽器導回 ?strava=／?terra= 帶的結果值。 */
export const SUNSET_RESULT = 'sunset'

export type SunsetState = 'off' | 'announce'
export interface SunsetInfo {
  state: SunsetState
  date?: string // YYYY-MM-DD（台北曆日、含當天）；只在 announce 帶出
}

export type SunsetBrand = 'Strava' | 'Terra'
/** normal＝與改版前完全相同；connected＝公告橫幅＋保留同步／中斷；paused＝沒有連接鈕，只有「暫停新連接」短提示。 */
export type SunsetCardView = 'normal' | 'connected' | 'paused'

const OFF: SunsetInfo = { state: 'off' }

/** 合法的 YYYY-MM-DD 曆日（含閏年、月底天數）才算；其他一律視為沒有日期。 */
function validDate(d: unknown): d is string {
  if (typeof d !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(d)) return false
  const y = Number(d.slice(0, 4)), m = Number(d.slice(5, 7)), day = Number(d.slice(8, 10))
  if (m < 1 || m > 12 || day < 1) return false
  const dim = [31, (y % 4 === 0 && y % 100 !== 0) || y % 400 === 0 ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]
  return day <= dim[m - 1]
}

/**
 * 把後端 /status 的 sunset 欄位正規化：缺席、null、非物件、未知 state 一律是 off（舊後端或欄位壞掉時畫面不變）；
 * announce 才保留日期（日期不合法就省略，文案改用不含日期的句子）。
 */
export function normalizeSunset(raw: unknown): SunsetInfo {
  if (!raw || typeof raw !== 'object') return OFF
  const r = raw as { state?: unknown; date?: unknown }
  if (typeof r.state !== 'string' || r.state.trim().toLowerCase() !== 'announce') return OFF
  return validDate(r.date) ? { state: 'announce', date: r.date } : { state: 'announce' }
}

/** "2026-10-31" → "10 月 31 日"（月日不補零）；不合法回空字串。 */
export function sunsetDateLabel(date?: string): string {
  if (!validDate(date)) return ''
  return `${Number(date.slice(5, 7))} 月 ${Number(date.slice(8, 10))} 日`
}

/** 卡片視圖：off 一律 normal；announce 時依「這張卡對應的服務是否有連線」分成 connected／paused。 */
export function sunsetCardView(state: SunsetState, hasConnection: boolean): SunsetCardView {
  if (state !== 'announce') return 'normal'
  return hasConnection ? 'connected' : 'paused'
}

/** 已連接者的公告橫幅標題。 */
export function sunsetBannerTitle(brand: SunsetBrand, date?: string): string {
  const label = sunsetDateLabel(date)
  return label ? `${brand} 串接將於 ${label}結束` : `${brand} 串接即將結束`
}

/**
 * 一條 Terra 連線列「會照常同步到結束日」說不準嗎？品牌在保守清單內（cautiousBrands：該品牌經 Terra 的推送已知中斷，
 * 由呼叫端提供），或最後資料已逾期（isStale 由呼叫端提供，與列內「同步中斷」警示同一個判斷；last_data_at 缺席視為不逾期）。
 * 品牌比對不分大小寫。
 */
export function sunsetRowSyncUncertain(
  row: { provider: string; last_data_at?: string },
  cautiousBrands: readonly string[],
  isStale: (iso?: string) => boolean,
): boolean {
  return cautiousBrands.includes(String(row.provider).toLowerCase()) || isStale(row.last_data_at)
}

/**
 * 公告文案可以指名「COROS 直連」嗎？入口對這位會員開放（corosEntryShown），而且這位會員已連接的來源（connectedSources，
 * 小寫品牌代碼，Dashboard 彙整的 Terra／直連來源）裡沒有保守品牌——不引導這些會員改用其他品牌。
 */
export function sunsetCanNameCoros(
  corosEntryShown: boolean,
  connectedSources: readonly string[],
  cautiousBrands: readonly string[],
): boolean {
  return corosEntryShown && !cautiousBrands.some((b) => connectedSources.includes(b))
}

/** 公告橫幅內文的情境（由呼叫端依這位會員的連線狀態算出，本檔只負責依情境挑句子）。 */
export interface SunsetBannerContext {
  /** 「COROS 直連」入口對這位會員開放，而且文案可以指名它（呼叫端已排除不宜引導的會員）。 */
  corosShown: boolean
  /**
   * 這張卡上有「會照常同步」說不準的連線列（該品牌經 Terra 的推送已知中斷，或最後資料已超過 48 小時）：
   * 橫幅不承諾「照常同步到結束日」，也不引導改用其他品牌的直連（即使 corosShown 為真）。省略＝false（例如 Strava 卡）。
   */
  syncUncertain?: boolean
}

/**
 * 已連接者的公告橫幅內文（每個元素一段）。只陳述事實：不再開放新連接、（連線正常時）既有連線照常同步到結束日
 * （當天仍可使用）、之後中斷連線不再同步新紀錄；不承諾（也不否認）已匯入資料的處置方式，那是另案決定，
 * 也不承諾任何「之後會通知」之類未經核准的事。
 * ctx.corosShown＝可以指名「COROS 直連」，否則只引導用 DOR 的 GPS 跑步追蹤；ctx.syncUncertain 見上。
 */
export function sunsetBannerLines(date: string | undefined, ctx: SunsetBannerContext): string[] {
  if (ctx.syncUncertain) {
    return [
      '目前不再開放新的連接；串接結束之後會中斷連線，不再同步新的紀錄。',
      '想記錄成績，可以隨時使用 DOR 的 GPS 跑步追蹤。',
    ]
  }
  const until = sunsetDateLabel(date) || '結束日'
  return [
    `目前不再開放新的連接。你已經連接的，會照常同步到 ${until}（當天仍可使用）；之後會中斷連線，不再同步新的紀錄。`,
    ctx.corosShown
      ? '想繼續同步手錶紀錄，請改用下方的「COROS 直連」；也可以隨時用 DOR 的 GPS 跑步追蹤記錄成績。'
      : '想記錄成績，可以隨時使用 DOR 的 GPS 跑步追蹤。',
  ]
}

/** 沒有連線者的短提示（取代連接鈕）。 */
export function sunsetPausedNotice(brand: SunsetBrand, date: string | undefined, corosShown: boolean): string {
  const label = sunsetDateLabel(date)
  const what = brand === 'Terra' ? '新的裝置連接' : '新的 Strava 連接'
  const tail = label ? `（${brand} 串接將於 ${label}結束）` : `（${brand} 串接即將結束）`
  return `${what}目前暫停開放${tail}。${corosShown ? '想同步手錶紀錄，請改用下方的「COROS 直連」。' : ''}`
}

/** ?strava=sunset／?terra=sunset 導回後顯示的固定訊息（沒有日期：導回網址不帶日期，橫幅上才有）。 */
export const SUNSET_RESULT_TEXT = 'Terra 與 Strava 串接即將結束，目前不再開放新的連接。'

/**
 * API 被擋下（409 且 body.code === 'wearable_sunset'）時的固定訊息；其他任何錯誤回 null（呼叫端走原本的錯誤處理）。
 * 絕不回顯伺服器的 error 字串，日期只取通過驗證的 YYYY-MM-DD。
 */
export function sunsetErrorText(status: unknown, body: unknown): string | null {
  if (status !== SUNSET_ERROR_STATUS || !body || typeof body !== 'object') return null
  const b = body as { code?: unknown; date?: unknown }
  if (b.code !== SUNSET_ERROR_CODE) return null
  const label = sunsetDateLabel(typeof b.date === 'string' ? b.date : undefined)
  return label ? `Terra 與 Strava 串接將於 ${label}結束，目前不再開放新的連接。` : SUNSET_RESULT_TEXT
}

/**
 * 公告期間「中斷／斷開」確認視窗追加的句子（中斷後無法再連接，所以要讓使用者知道）。off 時呼叫端不得附加。
 * Terra：斷開會把這個品牌經 Terra 匯入的活動一併刪除（後端 Disconnect → DeleteProviderActivities），賽事排名是依剩餘活動
 * 即時計算，所以要明講；off 時的確認文字原本沒有這句，維持逐字不動（見 verify 腳本），只在公告期間補上。
 * Strava：原本的確認文字（兩個狀態都有）已經講了活動會刪除，這裡只補「無法再重新連接」。
 */
export function sunsetDisconnectNote(brand: SunsetBrand): string {
  if (brand === 'Terra') {
    return '這個品牌經 Terra 匯入的活動將一併刪除（賽事成績會重新計算）。Terra 串接即將結束，斷開後無法再重新連接。'
  }
  return 'Strava 串接即將結束，中斷後無法再重新連接。'
}

/**
 * 個人頁底部「支援與隱私」裡原本那句「到上方運動數據分頁點官方連接按鈕」的 announce 版：連接鈕已經不存在，
 * 不能繼續叫人去點。只在 announce 時使用；off 時呼叫端維持原句不變。
 */
export function sunsetSupportLine(date?: string): string {
  const label = sunsetDateLabel(date)
  return `Strava 串接${label ? `將於 ${label}結束` : '即將結束'}，目前不再開放新的連接；已連接者可隨時到上方「運動數據」分頁按「中斷」。`
}
