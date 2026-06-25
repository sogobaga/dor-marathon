'use client'

import { useEffect, useState } from 'react'

export default function PhoneShell() {
  const [isMobile, setIsMobile] = useState(false)

  useEffect(() => {
    setIsMobile(window.innerWidth <= 430)
  }, [])

  return (
    <div className={isMobile ? 'w-full h-dvh' : 'phone-shell'}>
      {/* iOS notch */}
      {!isMobile && (
        <div style={{
          position: 'absolute', top: 12, left: '50%', transform: 'translateX(-50%)',
          width: 120, height: 34, background: '#000', borderRadius: 999, zIndex: 50,
        }} />
      )}

      {/* App content 區域 */}
      <div style={{
        position: 'absolute',
        inset: 0,
        paddingTop: isMobile ? 'env(safe-area-inset-top)' : 0,
        overflow: 'hidden',
      }}>
        {/* TODO: 接入 React App Router — Phase 2 */}
        <AppPlaceholder />
      </div>
    </div>
  )
}

function AppPlaceholder() {
  return (
    <div style={{
      height: '100%', display: 'flex', flexDirection: 'column',
      alignItems: 'center', justifyContent: 'center', gap: 16,
      color: 'rgba(255,255,255,.4)', fontFamily: 'system-ui',
    }}>
      <div style={{ fontSize: 48 }}>⚡</div>
      <div style={{ fontSize: 20, fontWeight: 700, color: '#2DE59A' }}>DOR</div>
      <div style={{ fontSize: 13 }}>雲端馬拉松 · 系統初始化中</div>
      <div style={{
        marginTop: 24, padding: '8px 16px', borderRadius: 999,
        border: '1px solid rgba(45,229,154,.3)', fontSize: 12, color: '#2DE59A',
      }}>
        Phase 1 — 後端服務建立中
      </div>
    </div>
  )
}
