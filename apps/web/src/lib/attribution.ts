// 資料來源歸屬字串工具（Garmin API 品牌規範要求的「Garmin ＋ 裝置型號」歸屬標示）。
// 純函式、沒有任何 import：scripts/verify-garmin-card.mjs 以 Node 原生 type-stripping 直接載入本檔，
// 所以這裡只能用「可被剝除型別」的語法（不用 enum／namespace／建構子參數屬性）。
//
// 規則（改寫自品牌規範的要求，不是原文）：
// - 凡顯示 Garmin 裝置資料之處，一律標示「Garmin ＋ 型號」，例如「Garmin Forerunner 265」。
// - 型號不明（API 沒給／空字串／"unknown"）→ 只寫「Garmin」。
// - 型號字串本身已以 Garmin 開頭 → 不重複，且品牌字樣統一成「Garmin」大小寫。
// - 歸屬必須放在資料旁、首屏可見；不可藏在 tooltip、頁尾或可展開區塊裡（呼叫端負責擺放）。
// - 不使用、也不仿製任何 Garmin 標誌圖形；官方 tile 由擁有者日後提供（見 GarminCard 的 data-garmin-tile-slot）。
//
// 目前只有 Garmin 需要這套歸屬；COROS 仍沿用 ProfileScreen 既有的「Data provided by COROS · 型號」一行。

export const GARMIN_BRAND = 'Garmin'

// 型號字串長度上限（正常型號如 "Forerunner 265"、"fenix 7X Pro" 遠短於此）；超過只是防版面被異常資料撐爆。
const MODEL_MAX_CODEPOINTS = 64

// API 在「不知道型號」時可能回的占位字樣（例如手動編輯過的活動摘要 deviceName 恆為 unknown）。
const UNKNOWN_MODELS = new Set(['unknown', 'unkown', 'n/a', 'na', 'none', 'null', 'undefined', '-', '--', '?'])

// 去掉開頭的 "garmin"（不分大小寫；後面必須是分隔字元或字串結尾，避免誤砍 "Garminfoo" 這類字）以及緊接的分隔符。
const LEADING_BRAND_RE = /^garmin(?![a-z0-9])[\s\-_:·]*/i

// 零寬字元與雙向控制字元（可被拿來改變顯示順序、偽造字樣）一律直接移除。
// 用「碼位範圍」在執行時組出正規式——原始碼裡不放任何不可見字元（避免被編輯器／審查工具當成隱藏字元警告）。
const INVISIBLE_RANGES: ReadonlyArray<readonly [number, number]> = [
  [0x200b, 0x200f], // 零寬空白、零寬（不）連接符、左右方向標記
  [0x202a, 0x202e], // 雙向嵌入／覆寫（含 RLO）
  [0x2060, 0x2064], // word joiner 與不可見運算符
  [0x2066, 0x2069], // 雙向隔離
  [0xfeff, 0xfeff], // BOM／零寬不換行空白
]
const INVISIBLE_RE = new RegExp(
  '[' + INVISIBLE_RANGES.map(([a, b]) => String.fromCharCode(a) + (a === b ? '' : '-' + String.fromCharCode(b))).join('') + ']',
  'g',
)

/** 清理後的「純型號」（不含 Garmin 字樣）；不明回空字串。 */
export function cleanDeviceModel(deviceName?: string | null): string {
  if (typeof deviceName !== 'string') return ''
  // 控制字元一律當空白，零寬／雙向控制字元移除，再壓縮空白。
  // eslint-disable-next-line no-control-regex
  let s = deviceName.replace(/[\u0000-\u001f\u007f]/g, ' ').replace(INVISIBLE_RE, '').replace(/\s+/g, ' ').trim()
  if (!s) return ''
  const cps = Array.from(s)
  if (cps.length > MODEL_MAX_CODEPOINTS) s = cps.slice(0, MODEL_MAX_CODEPOINTS).join('').trim()
  if (UNKNOWN_MODELS.has(s.toLowerCase())) return ''
  s = s.replace(LEADING_BRAND_RE, '').trim()
  if (!s) return '' // 型號就只有 "Garmin" 這個字
  if (UNKNOWN_MODELS.has(s.toLowerCase())) return ''
  return s
}

/** 「Garmin ＋ 型號」；型號不明回「Garmin」。 */
export function garminAttribution(deviceName?: string | null): string {
  const model = cleanDeviceModel(deviceName)
  return model ? `${GARMIN_BRAND} ${model}` : GARMIN_BRAND
}

/** activities.source 是否為 Garmin（大小寫不拘；其餘來源一律 false）。 */
export function isGarminSource(source?: string | null): boolean {
  return typeof source === 'string' && source.trim().toLowerCase() === 'garmin'
}

/**
 * 單筆活動的來源歸屬字串：Garmin 列回「Garmin ＋ 型號」，其他來源回 null（由呼叫端自行處理，例如 COROS 沿用既有那一行）。
 * 不會因為別的來源帶了 device_name 就誤標成 Garmin。
 */
export function sourceAttribution(source?: string | null, deviceName?: string | null): string | null {
  return isGarminSource(source) ? garminAttribution(deviceName) : null
}

/**
 * 多筆活動集中標示（表頭／全域歸屬）：只看 Garmin 列。沒有 Garmin 列回 null；
 * 有的話依出現順序列出不重複的「Garmin ＋ 型號」，以「、」連接；只要有任一已知型號，就不另外附一個單獨的「Garmin」
 * （型號已知的那幾項本身就已列出 Garmin 為來源）。全都不知道型號 → 「Garmin」。
 */
export function garminListAttribution(rows: ReadonlyArray<{ source?: string | null; device_name?: string | null }>): string | null {
  const models: string[] = []
  const seen = new Set<string>()
  let any = false
  for (const r of rows) {
    if (!r || !isGarminSource(r.source)) continue
    any = true
    const m = cleanDeviceModel(r.device_name)
    if (m && !seen.has(m.toLowerCase())) { seen.add(m.toLowerCase()); models.push(m) }
  }
  if (!any) return null
  return models.length === 0 ? GARMIN_BRAND : models.map((m) => `${GARMIN_BRAND} ${m}`).join('、')
}
