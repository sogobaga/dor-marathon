'use client'

import { usePathname } from 'next/navigation'
import AdminShell from '@/components/AdminShell'

// 後台外殼（登入頁無側欄；其餘用 AdminShell）。skin 由外層 admin/layout 以 data-skin="default" 固定為暗色。
export default function AdminChrome({ children }: { children: React.ReactNode }) {
  const pathname = usePathname()
  if (pathname === '/admin/login') {
    return <div style={{ minHeight: '100vh', background: 'var(--bg)', color: 'var(--tx)' }}>{children}</div>
  }
  return <AdminShell>{children}</AdminShell>
}
