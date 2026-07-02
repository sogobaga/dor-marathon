// 蓋板廣告 CTA 常用預設（後台可直接套用；未來可再擴充，也能自訂）。
export const CTA_PRESETS: { label: string; url: string }[] = [
  { label: '立即報名', url: '/' },
  { label: '查看賽事', url: '/' },
  { label: '了解更多', url: '' },
  { label: '開始跑步', url: '/track' },
]

// 「本日不再顯示」與「本次工作階段已顯示」用的鍵；跨本地 00:00 自動重置（比對本地日期字串）。
export const INTERSTITIAL_OFF_KEY = 'dor_interstitial_off'
export const INTERSTITIAL_SEEN_KEY = 'dor_interstitial_seen'

// 本地日期 YYYY-M-D（用來判斷是否已跨過 00:00 而該重置）
export function localDayKey(): string {
  const d = new Date()
  return `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`
}
