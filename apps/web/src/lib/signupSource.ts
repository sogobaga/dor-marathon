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
