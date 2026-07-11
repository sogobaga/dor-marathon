'use client'

import { useEffect, useRef } from 'react'
import { useUser, getUserToken } from '@/lib/userAuth'
import { createSiteSocket } from '@/lib/api'
import { useSiteRealtimeStore, DATA_TOPICS, type DataTopic } from '@/lib/siteRealtimeStore'
import RefreshBadge from './RefreshBadge'

const INITIAL_BACKOFF_MS = 1000
const MAX_BACKOFF_MS = 30000

interface DataUpdatedMsg {
  type: string
  topic?: string
  target_user_ids?: string[] | null
}

// 全站掛載的 /ws/site 連線（登入才連）：收到 data_updated 就把 topic 記進待更新集合，
// 交給 RefreshBadge 顯示非阻斷式提示，使用者點了才真的失效 SWR 快取。絕不自動刷新畫面。
export default function SiteRealtime() {
  const user = useUser()
  const userId = user?.id ?? null
  const addTopic = useSiteRealtimeStore((s) => s.addTopic)
  const bumpMail = useSiteRealtimeStore((s) => s.bumpMail)

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

  return <RefreshBadge />
}
