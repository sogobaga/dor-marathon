'use client'

import { useEffect, useState, type ReactNode } from 'react'

// 前台獨立頁面共用的「手機模擬框」：PC 以手機寬度置中呈現，手機上全屏。
// 手機預設走「文件流」（body 捲動 → 瀏覽器工具列可自動隱藏）；
// fixed=true 則維持 app-shell（畫面固定、內層捲動），給有現場地圖等需固定的頁面。
export default function PhoneFrame({ children, fixed }: { children: ReactNode; fixed?: boolean }) {
  const [isMobile, setIsMobile] = useState(false)
  useEffect(() => {
    setIsMobile(window.innerWidth <= 430)
  }, [])

  const fx = fixed ? ' is-fixed' : ''
  return (
    <main className={'phone-frame' + fx}>
      <div className={'phone-shell' + fx} style={{ position: 'relative' }}>
        <div
          className={'app-shell-body' + fx}
          style={{
            position: 'absolute', inset: 0,
            paddingTop: isMobile ? 'env(safe-area-inset-top)' : 0,
            paddingBottom: isMobile ? 'env(safe-area-inset-bottom)' : 0,
            display: 'flex', flexDirection: 'column',
            background: 'var(--bg)', color: 'var(--tx)',
          }}
        >
          {children}
        </div>
      </div>
    </main>
  )
}
