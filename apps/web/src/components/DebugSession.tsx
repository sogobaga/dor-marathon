'use client'

import { useEffect, useState } from 'react'

// 暫時診斷用：顯示 token 解碼、/auth/me 的狀態與 body、伺服器時間 vs token 效期。
// 確認登入問題後即移除。
function b64urlJson(seg: string): any {
  try { return JSON.parse(atob(seg.replace(/-/g, '+').replace(/_/g, '/'))) } catch { return null }
}

export default function DebugSession() {
  const [info, setInfo] = useState<string>('檢查中…')

  useEffect(() => {
    const t = localStorage.getItem('dor_user_token')
    const r = localStorage.getItem('dor_user_refresh')
    const u = localStorage.getItem('dor_user')
    const out: Record<string, unknown> = {
      hasToken: !!t, tokenLen: t?.length ?? 0, hasRefresh: !!r, hasUser: !!u,
    }
    if (t) {
      const parts = t.split('.')
      out.parts = parts.length
      out.sigLen = parts[2]?.length ?? 0 // HS256 正常 43
      out.tokHead = t.slice(0, 16)
      out.tokTail = t.slice(-16)
      out.header = b64urlJson(parts[0] || '')
      const p = b64urlJson(parts[1] || '')
      if (p) { out.uid = p.uid; out.role = p.role; out.iat = p.iat; out.exp = p.exp }
      out.clientNow = Math.floor(Date.now() / 1000)
    }
    ;(async () => {
      if (t) {
        try {
          const res = await fetch('/api/v1/auth/me', { headers: { Authorization: `Bearer ${t}` } })
          out.me = res.status
          out.meBody = (await res.text()).slice(0, 120)
          const d = res.headers.get('date')
          if (d) {
            const serverNow = Math.floor(new Date(d).getTime() / 1000)
            out.serverNow = serverNow
            if (out.exp) out.serverVsExp = (out.exp as number) - serverNow // 負=伺服器認為已過期
          }
        } catch (e: any) { out.me = `ERR:${e?.message}` }
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
