'use client'

import { useSiteRealtimeStore } from '@/lib/siteRealtimeStore'

// 非阻斷式小藥丸：靠底部置中、有待更新內容才顯示，點擊才失效對應 SWR keys（絕不自動刷新）。
// zIndex 600：比可拖曳資訊面板(500)高、比一般全螢幕彈窗(1200+)低——不擋互動，也不會被面板蓋住。
export default function RefreshBadge() {
  const pendingCount = useSiteRealtimeStore((s) => s.pendingTopics.size)
  const refreshAndClear = useSiteRealtimeStore((s) => s.refreshAndClear)

  if (pendingCount === 0) return null

  return (
    <div style={wrap}>
      <button onClick={refreshAndClear} style={pill}>
        🔄 有新內容，點我更新
      </button>
    </div>
  )
}

const wrap: React.CSSProperties = {
  position: 'fixed',
  left: 0,
  right: 0,
  bottom: 'calc(env(safe-area-inset-bottom, 0px) + 18px)',
  display: 'flex',
  justifyContent: 'center',
  zIndex: 600,
  pointerEvents: 'none',
}

const pill: React.CSSProperties = {
  pointerEvents: 'auto',
  padding: '9px 18px',
  borderRadius: 999,
  border: '1px solid rgba(255,255,255,.18)',
  background: 'rgba(20,20,28,.94)',
  color: '#fff',
  fontSize: 13,
  fontWeight: 700,
  cursor: 'pointer',
  boxShadow: '0 6px 20px rgba(0,0,0,.35)',
  letterSpacing: '.02em',
}
