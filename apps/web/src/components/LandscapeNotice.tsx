'use client'

import { usePathname } from 'next/navigation'
import { useIsPhone } from '@/lib/useIsMobile'
import { useIsLandscape } from '@/lib/useIsLandscape'

// 手機橫向時，全站統一跳出「請轉回直立」提示（前台專用，後台不顯示）。
export default function LandscapeNotice() {
  const pathname = usePathname()
  const phone = useIsPhone()
  const landscape = useIsLandscape()
  if (pathname?.startsWith('/admin') || !phone || !landscape) return null
  return (
    <div style={overlay}>
      <div style={{ fontSize: 46 }}>📱↻</div>
      <div style={{ fontSize: 18, fontWeight: 800, color: 'var(--tx)' }}>請將手機轉回直立</div>
      <div style={{ fontSize: 13.5, color: 'var(--tx-dim)', lineHeight: 1.7, maxWidth: 300 }}>
        本站僅支援直式畫面，轉回直立即可繼續。跑步中你的移動仍在背景持續記錄。<br />
        建議把手機「自動旋轉」關閉，或將本站「加入主畫面」以固定直屏。
      </div>
    </div>
  )
}

const overlay: React.CSSProperties = { position: 'fixed', inset: 0, zIndex: 4000, background: 'var(--bg-1)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 14, padding: 24, textAlign: 'center' }
