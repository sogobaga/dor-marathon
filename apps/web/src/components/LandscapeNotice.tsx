'use client'

import { usePathname } from 'next/navigation'

// 手機橫向時全站統一「請轉回直立」。顯示完全由 CSS media query 控制（.landscape-lock，見 globals.css）：
// 旋轉當下即蓋上、幾乎看不到橫式畫面，且只回應真實裝置方向、不會因晃動誤觸。後台不顯示。
export default function LandscapeNotice() {
  const pathname = usePathname()
  if (pathname?.startsWith('/admin')) return null
  return (
    <div className="landscape-lock">
      <div style={{ fontSize: 46 }}>📱↻</div>
      <div style={{ fontSize: 18, fontWeight: 800, color: 'var(--tx)' }}>請將手機轉回直立</div>
      <div style={{ fontSize: 13.5, color: 'var(--tx-dim)', lineHeight: 1.7, maxWidth: 300 }}>
        本站僅支援直式畫面，轉回直立即可繼續。跑步中你的移動仍在背景持續記錄。<br />
        建議把手機「自動旋轉」關閉，或將本站「加入主畫面」以固定直屏。
      </div>
    </div>
  )
}
