'use client'

import { useEffect, useState } from 'react'
import { authApi } from '@/lib/api'

// 暫時診斷用：顯示本機 session 狀態與 /auth/me、/auth/refresh 的實際結果。
// 確認登入問題後即移除。
export default function DebugSession() {
  const [info, setInfo] = useState<string>('檢查中…')

  useEffect(() => {
    const t = localStorage.getItem('dor_user_token')
    const r = localStorage.getItem('dor_user_refresh')
    const u = localStorage.getItem('dor_user')
    const out: Record<string, unknown> = {
      hasToken: !!t, tokenLen: t?.length ?? 0,
      hasRefresh: !!r, refreshLen: r?.length ?? 0,
      hasUser: !!u,
    }
    // access token 的 exp 與現在比較
    if (t) {
      try {
        const payload = JSON.parse(atob(t.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')))
        out.accExp = payload.exp
        out.now = Math.floor(Date.now() / 1000)
        out.accExpired = payload.exp < Math.floor(Date.now() / 1000)
      } catch { out.accDecode = 'fail' }
    }
    ;(async () => {
      // 只做唯讀的 me 探測；refresh 改由 validateSession 走（麵包屑會記錄），
      // 避免一次性輪替下兩邊互搶 refresh token。
      if (t) {
        try { await authApi.me(t); out.me = 200 } catch (e: any) { out.me = e?.status ?? `ERR:${e?.message}` }
      }
      let log: string[] = []
      try { log = JSON.parse(localStorage.getItem('dor_diag') || '[]') } catch { /* ignore */ }
      setInfo(JSON.stringify(out, null, 1) + '\n\nLOG:\n' + log.join('\n'))
    })()
  }, [])

  return (
    <pre style={{
      fontSize: 11, lineHeight: 1.4, color: '#9fffea', background: '#04231c',
      border: '1px solid #0a6', borderRadius: 10, padding: 10, margin: '0 0 12px',
      whiteSpace: 'pre-wrap', wordBreak: 'break-all',
    }}>SESSION DEBUG{'\n'}{info}</pre>
  )
}
