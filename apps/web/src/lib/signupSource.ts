// 註冊來源歸因：中文標籤 + 篩選選項（後台會員管理列表/詳情共用）。見 migration 147_signup_attribution。

import type { SignupSource } from './api'

export const SIGNUP_SOURCE_LABEL: Record<SignupSource, string> = {
  referral: '推廣連結',
  facebook: 'Facebook',
  instagram: 'Instagram',
  line: 'LINE',
  google: 'Google',
  threads: 'Threads',
  tiktok: 'TikTok',
  x: 'X',
  youtube: 'YouTube',
  dcard: 'Dcard',
  ptt: 'PTT',
  other: '其他',
  direct: '直接進入',
}

// 列表/詳情共用：組成顯示用字串。referral 附上推薦人暱稱；other 且有 utm_source 原值時附上原值；
// 歷史會員（無資料）顯示「—」。
export function signupSourceText(source?: SignupSource | null, refName?: string | null, utmSource?: string | null): string {
  if (!source) return '—'
  const label = SIGNUP_SOURCE_LABEL[source] || source
  if (source === 'referral' && refName) return `${label}(${refName})`
  if (source === 'other' && utmSource) return `${label}(${utmSource})`
  return label
}

// 篩選下拉選單選項（依契約列出的 13 種來源）
export const SIGNUP_SOURCE_OPTIONS: { value: SignupSource; label: string }[] = (
  ['referral', 'facebook', 'instagram', 'line', 'google', 'threads', 'tiktok', 'x', 'youtube', 'dcard', 'ptt', 'other', 'direct'] as SignupSource[]
).map((value) => ({ value, label: SIGNUP_SOURCE_LABEL[value] }))

// 各來源固定色（後台成效統計堆疊長條圖 + 彙總表色點共用，admin 後台固定暗色 data-skin="default"，
// 不隨前台淺色 skin 變動，見 app/admin/layout.tsx）。前 8 色取自 dataviz skill 已驗證的類別色階
// （dark 步階，CVD-safe），指派給預期較常見的 8 個通路；其餘 5 個通路（含 other/direct）用視覺上
// 仍可清楚區分、但未跑全量 ΔE 驗證的延伸色——每個色塊/圖例都固定搭配中文文字（hover title / legend
// label），身分辨識不單獨依賴顏色，符合 skill 對超過 8 類別時的緩解原則。
export const SIGNUP_SOURCE_COLOR: Record<SignupSource, string> = {
  facebook: '#3987e5',
  instagram: '#d55181',
  line: '#199e70',
  google: '#c98500',
  dcard: '#9085e9',
  ptt: '#008300',
  referral: '#d95926',
  tiktok: '#e66767',
  threads: '#2bb3c0',
  x: '#8a94a6',
  youtube: '#6366a8',
  other: '#6b7280',
  direct: '#a8b0bc',
}
