'use client'

import { type ReactNode } from 'react'
import { useIsMobile } from '@/lib/useIsMobile'

// 前台獨立頁面共用的「手機模擬框」：PC 上以手機寬度置中呈現，手機上全屏。
// 內部為直向 flex 欄；頁面自行放 header + <ScrollArea>。與首頁 PhoneShell 一致。
export default function PhoneFrame({ children }: { children: ReactNode }) {
  const isMobile = useIsMobile()

  return (
    <main className="phone-frame">
      <div className={isMobile ? 'w-full h-dvh' : 'phone-shell'} style={{ position: 'relative' }}>
        <div style={{
          position: 'absolute', inset: 0,
          paddingTop: isMobile ? 'env(safe-area-inset-top)' : 0,
          paddingBottom: isMobile ? 'env(safe-area-inset-bottom)' : 0,
          display: 'flex', flexDirection: 'column',
          background: 'var(--bg)', color: 'var(--tx)',
        }}>
          {children}
        </div>
      </div>
    </main>
  )
}
