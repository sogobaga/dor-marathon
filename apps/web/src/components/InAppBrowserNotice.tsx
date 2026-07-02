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

function detect(ua: string): string | null {
  for (const p of IN_APP) if (p.re.test(ua)) return p.name
  if (/Android/i.test(ua) && /;\s*wv\)/i.test(ua)) return 'App 內建瀏覽器' // 泛用 Android WebView
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
  const host = typeof window !== 'undefined' ? window.location.host : 'dor.hero-mi.com'

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
          <button onClick={copy} style={btnPrimary}>{copied ? '✓ 已複製網址' : '📋 複製網址'}</button>
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
