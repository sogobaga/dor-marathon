'use client'

import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useUser, getUserToken, clearUserSession, getSessionEpoch } from '@/lib/userAuth'
import { createSiteSocket } from '@/lib/api'
import { useSiteRealtimeStore, DATA_TOPICS, type DataTopic } from '@/lib/siteRealtimeStore'
import { overlayMount } from '@/lib/overlayMount'
import RefreshBadge from './RefreshBadge'

const INITIAL_BACKOFF_MS = 1000
const MAX_BACKOFF_MS = 30000

interface DataUpdatedMsg {
  type: string
  topic?: string
  target_user_ids?: string[] | null
  payload?: { epoch?: number } // session_revoked 專用：新登入的 session_epoch
}

// 全站掛載的 /ws/site 連線（登入才連）：收到 data_updated 就把 topic 記進待更新集合，
// 交給 RefreshBadge 顯示非阻斷式提示，使用者點了才真的失效 SWR 快取。絕不自動刷新畫面。
export default function SiteRealtime() {
  const user = useUser()
  const userId = user?.id ?? null
  const addTopic = useSiteRealtimeStore((s) => s.addTopic)
  const bumpMail = useSiteRealtimeStore((s) => s.bumpMail)
  const [revoked, setRevoked] = useState(false) // 單一登入：本裝置被踢下線時顯示提示彈窗

  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const backoffRef = useRef(INITIAL_BACKOFF_MS)
  const closingRef = useRef(false) // true = 主動關閉（登出/卸載），不再重連
  const userIdRef = useRef<string | null>(userId)
  userIdRef.current = userId

  useEffect(() => {
    if (!userId) return // 未登入：不連線（若前一個 effect 已開連線，其 cleanup 會先關閉）

    closingRef.current = false
    backoffRef.current = INITIAL_BACKOFF_MS

    const clearReconnectTimer = () => {
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current)
        reconnectTimerRef.current = null
      }
    }

    const connect = () => {
      if (closingRef.current) return
      const token = getUserToken()
      if (!token) return
      clearReconnectTimer()

      const ws = createSiteSocket(token)
      wsRef.current = ws

      ws.onopen = () => {
        backoffRef.current = INITIAL_BACKOFF_MS // 連上後重置退避
      }
      ws.onmessage = (ev) => {
        let msg: DataUpdatedMsg
        try {
          msg = JSON.parse(ev.data)
        } catch {
          return
        }
        if (msg.type === 'session_revoked') {
          // 單一登入：後端每次登入都會推播，帶「新登入」的 session_epoch。
          const targets = msg.target_user_ids
          const relevant = !targets || targets.length === 0 || (userIdRef.current != null && targets.includes(userIdRef.current))
          if (!relevant) return
          const epoch = Number(msg.payload?.epoch)
          if (!(epoch > getSessionEpoch())) return // epoch 不大於自己 → 是自己這次登入或更舊的訊息，忽略
          clearUserSession() // 我是被取代的舊 session → 登出（emit dor-auth-changed，track 頁若正在跑步會自行停跑保留待上傳）
          setRevoked(true)
          return
        }
        if (msg.type !== 'data_updated' || !msg.topic) return
        const targets = msg.target_user_ids
        const relevant = !targets || targets.length === 0 || (userIdRef.current != null && targets.includes(userIdRef.current))
        if (!relevant) return
        if (msg.topic === 'mail') { bumpMail(); return } // 站內信：自動即時更新未讀紅點（不進 refresh badge）
        if ((DATA_TOPICS as readonly string[]).includes(msg.topic)) addTopic(msg.topic as DataTopic)
      }
      ws.onclose = () => {
        if (wsRef.current === ws) wsRef.current = null
        if (closingRef.current) return
        const delay = backoffRef.current
        backoffRef.current = Math.min(backoffRef.current * 2, MAX_BACKOFF_MS)
        reconnectTimerRef.current = setTimeout(connect, delay)
      }
      // onerror 不重複處理：瀏覽器會接著觸發 onclose，重連邏輯統一交給 onclose
    }

    connect()

    // 分頁重新變為可見時，若連線已斷（非主動關閉）→ 立刻重連，不等退避跑完
    const onVisible = () => {
      if (document.visibilityState !== 'visible' || closingRef.current) return
      const ws = wsRef.current
      if (!ws || ws.readyState === WebSocket.CLOSED) {
        clearReconnectTimer()
        backoffRef.current = INITIAL_BACKOFF_MS
        connect()
      }
    }
    document.addEventListener('visibilitychange', onVisible)

    return () => {
      closingRef.current = true
      document.removeEventListener('visibilitychange', onVisible)
      clearReconnectTimer()
      wsRef.current?.close()
      wsRef.current = null
    }
  }, [userId, addTopic, bumpMail])

  return (
    <>
      <RefreshBadge />
      {revoked && <SessionRevokedModal onClose={() => setRevoked(false)} />}
    </>
  )
}

// 單一登入被踢下線的提示彈窗：沿用 overlayMount()（PC 版框在手機模擬框內，獨立路由退回視窗 fixed）
function SessionRevokedModal({ onClose }: { onClose: () => void }) {
  const om = overlayMount()
  const content = (
    <div style={{ position: om.position, inset: 0, background: 'rgba(0,0,0,.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 5000, padding: 20 }} onClick={onClose}>
      <div style={{ background: 'var(--bg-1)', border: '1px solid var(--fug)', borderRadius: 16, padding: 22, width: '100%', maxWidth: 340 }} onClick={(e) => e.stopPropagation()}>
        <strong style={{ fontSize: 17, color: 'var(--fug)' }}>此帳號已在其他裝置登入</strong>
        <p style={{ fontSize: 13, color: 'var(--tx-dim)', margin: '10px 0 18px', lineHeight: 1.6 }}>
          本裝置已登出。若剛才正在跑步，資料已保留待上傳，請重新登入後於運動數據頁上傳。
        </p>
        <button
          onClick={onClose}
          style={{ width: '100%', background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none', borderRadius: 9, padding: '10px 0', cursor: 'pointer', fontSize: 14 }}
        >
          知道了
        </button>
      </div>
    </div>
  )
  // SSR / om.node 未就緒時不 portal，直接 render（掛載後 effect 會重繪一次修正）
  return om.node ? createPortal(content, om.node) : content
}
