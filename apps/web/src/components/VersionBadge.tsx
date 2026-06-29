'use client'

import { useEffect, useState } from 'react'
import { APP_VERSION } from '@/lib/version'
import { metaApi } from '@/lib/api'

// 置底置中版號。showApi=true 時同時顯示後端 API 版號（後台用，方便比對前後台是否都更新）。
export default function VersionBadge({ showApi = false, absolute = false }: { showApi?: boolean; absolute?: boolean }) {
  const [api, setApi] = useState<string | null>(null)
  useEffect(() => {
    if (!showApi) return
    metaApi.version().then((r) => setApi(r.version)).catch(() => setApi('—'))
  }, [showApi])

  const wrap: React.CSSProperties = absolute
    ? { position: 'absolute', bottom: 5, left: 0, right: 0, pointerEvents: 'none', zIndex: 5 }
    : { padding: '8px 0 12px' }

  return (
    <div style={{ ...wrap, textAlign: 'center', fontSize: 10, color: 'var(--tx-faint)', letterSpacing: '.04em' }}>
      {showApi ? `前台 ${APP_VERSION} · 後端 ${api ?? '…'}` : APP_VERSION}
    </div>
  )
}
