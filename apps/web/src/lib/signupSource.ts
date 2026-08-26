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
  other: '其他',
  direct: '直接進入',
}

// 列表/詳情共用：組成顯示用字串。referral 附上推薦人暱稱；歷史會員（無資料）顯示「—」。
export function signupSourceText(source?: SignupSource | null, refName?: string | null): string {
  if (!source) return '—'
  const label = SIGNUP_SOURCE_LABEL[source] || source
  if (source === 'referral' && refName) return `${label}(${refName})`
  return label
}

// 篩選下拉選單選項（依契約列出的 9 種來源）
export const SIGNUP_SOURCE_OPTIONS: { value: SignupSource; label: string }[] = (
  ['referral', 'facebook', 'instagram', 'line', 'google', 'threads', 'tiktok', 'other', 'direct'] as SignupSource[]
).map((value) => ({ value, label: SIGNUP_SOURCE_LABEL[value] }))
