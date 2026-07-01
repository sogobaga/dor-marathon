'use client'

import { useEffect, useState, type ReactNode } from 'react'

// 前台獨立頁面共用的「手機模擬框」：PC 上以手機寬度置中呈現（含瀏海），手機上全屏。
// 內部為直向 flex 欄；頁面自行放 header + <ScrollArea>。與首頁 PhoneShell 一致。
export default function PhoneFrame({ children }: { children: ReactNode }) {
  const [isMobile, setIsMobile] = useState(false)
  useEffect(() => {
    setIsMobile(window.innerWidth <= 430)
  }, [])

  return (
    <main className="phone-frame">
      <div className={isMobile ? 'w-full h-dvh' : 'phone-shell'} style={{ position: 'relative' }}>
        <div style={{
          position: 'absolute', inset: 0,
          paddingTop: isMobile ? 'env(safe-area-inset-top)' : 0,
          display: 'flex', flexDirection: 'column',
          background: 'var(--bg)', color: 'var(--tx)',
        }}>
          {children}
        </div>
      </div>
    </main>
  )
}
