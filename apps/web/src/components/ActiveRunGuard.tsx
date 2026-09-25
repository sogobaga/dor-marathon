'use client'

import { useEffect } from 'react'
import { usePathname } from 'next/navigation'
import { readActiveRun } from '@/lib/activeRun'

// 全站導回（CONTRACT.md §2.3）：任何前台頁（非 /track、非 /admin）偵測到 dor_gps_active 存在
// → location.replace(開跑當下的完整 /track 網址，含 query)。主畫面 App（standalone）被系統砍掉後
// 從圖示重開會落在首頁（manifest start_url:"/"）——這裡接手把使用者送回跑步畫面，由 /track 頁掛載時
// 的自動接續邏輯（resumeActiveRun，見 track/page.tsx）接手還原。只讀 localStorage，不打 API、不寫入。
export default function ActiveRunGuard() {
  const pathname = usePathname()
  useEffect(() => {
    if (!pathname) return
    if (pathname === '/track' || pathname.startsWith('/track/')) return
    if (pathname.startsWith('/admin')) return
    let active: ReturnType<typeof readActiveRun> = null
    try { active = readActiveRun() } catch { active = null }
    if (active?.href) {
      try { window.location.replace(active.href) } catch { /* ignore */ }
    }
  }, [pathname])
  return null
}
