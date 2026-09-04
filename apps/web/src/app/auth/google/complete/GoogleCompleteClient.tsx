'use client'

import { useEffect, useRef, useState } from 'react'
import { authApi } from '@/lib/api'
import { setUserSession } from '@/lib/userAuth'
import { buildAcqPayload } from '@/lib/acquisition'
import { REF_CODE_KEY, LOGIN_RETURN_KEY } from '@/components/UserAuthBar'

// Google 登入整頁導轉（google_login_ux_mode='redirect'）完成頁：由 app/auth/google/callback/route.ts
// 303 redirect 過來，網址帶 #credential=<Google ID token>（fragment，伺服器端讀不到、不進任何 log）。
// 純客端流程：解析 hash → 立刻清掉 hash → 呼叫後端換發本站 token → 寫入 session → 導回登入前的頁面。
//
// 安全：credential 只存在於這支元件的區域變數與一次性的 fetch body 裡，絕不 console.log、絕不寫入任何
// 持久化儲存；hash 在讀取後立刻用 history.replaceState 清掉（在任何非同步 await 之前），避免使用者
// 重新整理、上一頁/下一頁、或不慎分享網址時把一次性憑證重放出去。

// 只接受同源相對路徑（'/' 開頭、非 '//'），避免萬一 sessionStorage 內容被污染成為開放重導向。
// 只允許同源的相對路徑：用瀏覽器同一套 WHATWG URL 解析器解析後比對 origin，'/\evil.com' 這類反斜線
// 或 protocol-relative 寫法字串前綴檢查擋不住（審查以 new URL 實測會解析成 https://evil.com）。
function safeReturnPath(p: string | null): string {
  if (!p) return '/'
  try {
    const u = new URL(p, window.location.origin)
    if (u.origin !== window.location.origin) return '/'
    return u.pathname + u.search
  } catch { return '/' }
}

export default function GoogleCompleteClient() {
  const [err, setErr] = useState('')
  const ran = useRef(false) // React 18 dev StrictMode 會把 effect 跑兩次；一次性憑證只能兌換一次，加個門閂

  useEffect(() => {
    if (ran.current) return
    ran.current = true

    ;(async () => {
      // 從 URL fragment 取出 credential，並立刻清掉 hash（在任何 await 之前）——不留一次性憑證在網址上。
      const hash = window.location.hash || ''
      const m = /(?:^|[&#])credential=([^&]+)/.exec(hash)
      const credential = m ? decodeURIComponent(m[1]) : ''
      history.replaceState(null, '', window.location.pathname + window.location.search)

      if (!credential) {
        setErr('未取得 Google 憑證，請重新登入一次')
        return
      }

      try {
        // 若之前透過推廣連結進站，帶上暫存的推薦碼（後端只在「新帳號」分支綁定，舊帳號會忽略）——與彈出視窗
        // 模式（UserAuthBar.tsx LoginModal 的 onSuccess）同一套邏輯，只是流程從彈窗換成整頁導轉。
        const refCode = localStorage.getItem(REF_CODE_KEY) || undefined
        const res = await authApi.google(credential, refCode, buildAcqPayload())
        setUserSession(res.tokens.access_token, res.tokens.refresh_token, res.user, res.tokens.session_epoch)
        localStorage.removeItem(REF_CODE_KEY)

        let back = '/'
        try { back = safeReturnPath(sessionStorage.getItem(LOGIN_RETURN_KEY)) } catch {}
        window.location.replace(back)
      } catch (e: any) {
        setErr(e?.message || '登入失敗，請重新嘗試')
      }
    })()
  }, [])

  return (
    <div style={wrap}>
      <style>{'@keyframes dorGSpin{to{transform:rotate(360deg)}}'}</style>
      {err ? (
        <div style={box}>
          <p style={{ margin: '0 0 16px', fontSize: 14, lineHeight: 1.6 }}>{err}</p>
          <a href="/" style={link}>回首頁</a>
        </div>
      ) : (
        <div style={box}>
          <div style={spinner} />
          <p style={{ margin: '16px 0 0', fontSize: 13, letterSpacing: '.08em', opacity: 0.7 }}>登入中…</p>
        </div>
      )}
    </div>
  )
}

// 沿用全站既有的 skin CSS variable（RootLayout 已在 <html data-skin> 套上，見 app/layout.tsx），
// 而非另外重算一份「品牌色」——這樣切換 skin 時這頁跟著變、不會有第二處色表要同步維護。
const wrap: React.CSSProperties = {
  position: 'fixed', inset: 0, background: 'var(--bg-1)', color: 'var(--tx)',
  display: 'flex', alignItems: 'center', justifyContent: 'center',
}
const box: React.CSSProperties = { display: 'flex', flexDirection: 'column', alignItems: 'center', textAlign: 'center', padding: 24 }
const spinner: React.CSSProperties = {
  width: 30, height: 30, borderRadius: '50%',
  border: '3px solid var(--tx-faint)', borderTopColor: 'var(--tx)',
  animation: 'dorGSpin .8s linear infinite',
}
const link: React.CSSProperties = { color: 'var(--fug)', fontWeight: 600, textDecoration: 'none', fontSize: 14 }
