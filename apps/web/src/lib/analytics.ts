// GA4（Google Analytics）串接。Measurement ID 非機密（本就出現在網頁），直接常數即可。
export const GA_ID = 'G-8FCCLZ6K88'

// 只在「正式站」送資料：www.dor.tw / dor.tw。UAT(dor.hero-mi.com) 與 localhost 不送 → 統計乾淨。
const PROD_HOSTS = ['www.dor.tw', 'dor.tw']

declare global {
  interface Window {
    dataLayer?: unknown[]
    gtag?: (...args: unknown[]) => void
  }
}

export function gaEnabled(): boolean {
  return typeof window !== 'undefined' && !!GA_ID && PROD_HOSTS.includes(window.location.hostname)
}

// 載入 gtag.js 並初始化（送出初始 page_view）。只在正式站生效；重複呼叫有防護。
export function initGA(): void {
  if (!gaEnabled() || window.gtag) return
  const s = document.createElement('script')
  s.async = true
  s.src = `https://www.googletagmanager.com/gtag/js?id=${GA_ID}`
  document.head.appendChild(s)
  const dl: unknown[] = window.dataLayer || (window.dataLayer = [])
  const g: (...args: unknown[]) => void = function gtag() {
    dl.push(arguments) // gtag 慣例：把 arguments 物件原樣 push 進 dataLayer
  }
  window.gtag = g
  g('js', new Date())
  g('config', GA_ID) // 預設送出初始 page_view（首頁）
}

// SPA 換畫面時手動送一次瀏覽（本 app 導覽是狀態切換、非 URL 變動）。
export function pageview(path: string, title?: string): void {
  if (!gaEnabled() || !window.gtag) return
  window.gtag('event', 'page_view', {
    page_path: path,
    page_title: title,
    page_location: window.location.origin + path,
  })
}

// 自訂事件（如報名完成、前往付款）。
export function track(event: string, params?: Record<string, unknown>): void {
  if (!gaEnabled() || !window.gtag) return
  window.gtag('event', event, params || {})
}
