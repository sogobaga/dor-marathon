'use client'

import { useEffect, useState } from 'react'
import { usePathname } from 'next/navigation'

// 已知會擋 Google 登入（disallowed_useragent）的「App 內建瀏覽器」UA 特徵（跨 iOS/Android）。
const IN_APP: { re: RegExp; name: string }[] = [
  { re: /\bLine\//i, name: 'LINE' },
  { re: /MicroMessenger/i, name: 'WeChat' },       // 須在 Messenger 之前（MicroMessenger 含 "Messenger"）
  { re: /\bMessenger/i, name: 'Messenger' },        // \b 才不會誤中 MicroMessenger
  { re: /FBAN|FBAV|FB_IAB|FBIOS/i, name: 'Facebook' },
  { re: /Instagram/i, name: 'Instagram' },
  { re: /Threads|Barcelona/i, name: 'Threads' },
  { re: /TikTok|musical_ly|Bytedance/i, name: 'TikTok' },
  { re: /\bTwitter\b/i, name: 'X／Twitter' },
  { re: /LinkedInApp/i, name: 'LinkedIn' },
]

// 供其他元件共用（PwaInstallPrompt）：這類環境裝不了 PWA，安裝引導必須跳過
export function isInAppBrowser(ua: string): boolean { return detect(ua) != null }
// 回傳 App 名稱（LINE／Facebook…）或 null；報名頁的「用 Safari／Chrome 開啟」引導卡也用這個判定
export function detectInAppBrowser(ua: string): string | null { return detect(ua) }

function detect(ua: string): string | null {
  for (const p of IN_APP) if (p.re.test(ua)) return p.name
  if (/Android/i.test(ua) && /;\s*wv\)/i.test(ua)) return 'App 內建瀏覽器' // 泛用 Android WebView
  return null
}

// 能「一鍵」跳出內建瀏覽器的手段（做得到才回連結，做不到回 null → 由「複製網址＋⋯選單」引導接手）：
// ・LINE（iOS/Android）：網址加 openExternalBrowser=1，LINE 會改交給系統瀏覽器開啟（LINE 官方支援的參數）。
// ・Android 其他 App 的 WebView：intent:// 指定 Chrome 套件，FB/IG/Messenger 多數放行。
// ・iOS 的 FB/IG/Messenger/Threads：WKWebView 內沒有任何 API 能叫出 Safari，只能引導 ⋯／分享 選單或複製網址。
export function externalOpenHref(ua: string, href: string): string | null {
  const app = detect(ua)
  if (!app) return null
  try {
    const u = new URL(href)
    if (app === 'LINE') { u.searchParams.set('openExternalBrowser', '1'); return u.toString() }
    if (/Android/i.test(ua)) return `intent://${u.host}${u.pathname}${u.search}#Intent;scheme=https;package=com.android.chrome;end`
  } catch { /* 非法網址：退回複製引導 */ }
  return null
}

// 在 App 內建瀏覽器時，提示改用系統瀏覽器（否則 Google 登入會被 Google 擋）。前台專用，不在後台顯示。
export default function InAppBrowserNotice() {
  const pathname = usePathname()
  const [app, setApp] = useState<string | null>(null)
  const [dismissed, setDismissed] = useState(false)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (pathname?.startsWith('/admin')) { setApp(null); return }
    try { setApp(detect(navigator.userAgent || '')) } catch { /* ignore */ }
  }, [pathname])

  if (!app || dismissed || pathname?.startsWith('/admin')) return null

  const isAndroid = typeof navigator !== 'undefined' && /Android/i.test(navigator.userAgent)
  const host = typeof window !== 'undefined' ? window.location.host : 'www.dor.tw'
  const extHref = typeof window !== 'undefined' ? externalOpenHref(navigator.userAgent || '', window.location.href) : null

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(window.location.href)
      setCopied(true); setTimeout(() => setCopied(false), 2000)
    } catch { /* 舊瀏覽器不支援：使用者可自行輸入下方網址 */ }
  }

  return (
    <div style={overlay}>
      <div style={card}>
        <div style={{ fontSize: 15, fontWeight: 900 }}>⚠️ 請用瀏覽器開啟才能登入</div>
        <div style={{ fontSize: 13, lineHeight: 1.7, marginTop: 6 }}>
          你正在 <b>{app}</b> 的內建瀏覽器，<b>Google 登入會被擋</b>（Google 安全政策）。
          請點右上角的 <b>⋯ ／ 分享</b> 圖示，選「<b>用預設瀏覽器開啟</b>」{isAndroid ? '（Chrome）' : '（Safari）'}，再登入。
        </div>
        <div style={{ display: 'flex', gap: 8, marginTop: 10, flexWrap: 'wrap', alignItems: 'center' }}>
          {extHref && <a href={extHref} style={{ ...btnPrimary, textDecoration: 'none', display: 'inline-block' }}>🧭 用 Safari／Chrome 開啟</a>}
          <button onClick={copy} style={extHref ? btnGhost : btnPrimary}>{copied ? '✓ 已複製網址' : '📋 複製網址'}</button>
          <button onClick={() => setDismissed(true)} style={btnGhost}>先關閉</button>
        </div>
        <div style={{ fontSize: 11.5, marginTop: 8, opacity: 0.85 }}>
          複製後貼到 Chrome／Safari 開啟；或直接輸入：<b style={{ userSelect: 'all', fontFamily: 'monospace' }}>{host}</b>
        </div>
      </div>
    </div>
  )
}

const overlay: React.CSSProperties = { position: 'fixed', top: 0, left: 0, right: 0, zIndex: 3000, display: 'flex', justifyContent: 'center', padding: '10px 12px', pointerEvents: 'none' }
const card: React.CSSProperties = { pointerEvents: 'auto', maxWidth: 460, width: '100%', background: 'linear-gradient(180deg,#FFE39A,#F7B733)', color: '#2a1e05', borderRadius: 14, padding: '12px 14px', boxShadow: '0 12px 32px rgba(0,0,0,.4)', border: '1px solid rgba(0,0,0,.12)' }
const btnPrimary: React.CSSProperties = { background: '#2a1e05', color: '#FFE39A', fontWeight: 800, border: 'none', borderRadius: 9, padding: '8px 14px', fontSize: 13, cursor: 'pointer' }
const btnGhost: React.CSSProperties = { background: 'rgba(0,0,0,.08)', color: '#2a1e05', border: '1px solid rgba(0,0,0,.2)', borderRadius: 9, padding: '8px 14px', fontSize: 13, cursor: 'pointer' }
