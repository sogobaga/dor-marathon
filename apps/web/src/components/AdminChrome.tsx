'use client'

import { useEffect, useState } from 'react'
import { usePathname } from 'next/navigation'
import AdminShell from '@/components/AdminShell'
import { ensureValidToken, refreshSession, getRefresh } from '@/lib/adminAuth'

// 後台外殼（登入頁無側欄；其餘用 AdminShell）。skin 由外層 admin/layout 以 data-skin="default" 固定為暗色。
// 進後台時先確保 access token 新鮮（「保持登入」有 refresh 就自動續期），再渲染頁面，避免頁面用過期 token 打 API 被登出。
export default function AdminChrome({ children }: { children: React.ReactNode }) {
  const pathname = usePathname()
  const isLogin = pathname === '/admin/login'
  const [ready, setReady] = useState(false)

  useEffect(() => {
    if (isLogin) { setReady(true); return }
    let alive = true
    ensureValidToken().finally(() => { if (alive) setReady(true) })
    // 使用中每 45 分保活（access TTL 60 分）→ 期間不會突然被登出
    const id = setInterval(() => { if (getRefresh()) refreshSession() }, 45 * 60 * 1000)
    // 分頁被背景後喚回（背景計時器可能被節流）→ 立即檢查/續期，避免醒來第一個動作就 401
    const onVis = () => { if (document.visibilityState === 'visible') ensureValidToken() }
    document.addEventListener('visibilitychange', onVis)
    return () => { alive = false; clearInterval(id); document.removeEventListener('visibilitychange', onVis) }
  }, [isLogin])

  if (isLogin) return <div style={{ minHeight: '100vh', background: 'var(--bg)', color: 'var(--tx)' }}>{children}</div>
  if (!ready) return <div style={{ minHeight: '100vh', display: 'grid', placeItems: 'center', background: 'var(--bg)', color: 'var(--tx-faint)', fontSize: 13 }}>載入中…</div>
  return <AdminShell>{children}</AdminShell>
}
