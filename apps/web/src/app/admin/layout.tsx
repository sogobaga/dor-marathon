'use client'

import { usePathname } from 'next/navigation'
import AdminShell from '@/components/AdminShell'

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname()

  // 登入頁維持獨立（無側欄 / 無切換鈕）
  if (pathname === '/admin/login') {
    return (
      <div style={{ minHeight: '100vh', background: 'var(--bg)', color: 'var(--tx)' }}>{children}</div>
    )
  }

  return <AdminShell>{children}</AdminShell>
}
