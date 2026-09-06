'use client'

import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useUser, getUserToken, clearUserSession, getSessionEpoch, refreshUserToken, userTokenExpiresInSec } from '@/lib/userAuth'
import { createSiteSocket } from '@/lib/api'
import { useSiteRealtimeStore, DATA_TOPICS, type DataTopic } from '@/lib/siteRealtimeStore'
import { overlayMount } from '@/lib/overlayMount'

const INITIAL_BACKOFF_MS = 1000
const MAX_BACKOFF_MS = 30000
// 連續握手失敗（從未 onopen）達此次數後改用長退避：多半是 token 已失效（後端 /ws/site 回 401 不 upgrade），
// 瀏覽器只給 onclose 1006、分不出 401 或斷網——2026-09-05 日報「登入失敗異常 IP 1663 次」就是一台裝置每 30 秒重連一次
// 打出來的。策略：連線前 token 快過期先續期；握手連續失敗 2 次就試著續期，續期明確失敗（session 已死）→ 登出停止；
// 之後退避拉長到 5 分鐘（推播不是關鍵功能，慢慢重連就好）。
const FAIL_STREAK_REFRESH = 2
const LONG_BACKOFF_MS = 5 * 60_000

interface DataUpdatedMsg {
  type: string
  topic?: string
  target_user_ids?: string[] | null
  payload?: { epoch?: number } // session_revoked 專用：新登入的 session_epoch
}

// 全站掛載的 /ws/site 連線（登入才連）：收到 data_updated 就把 topic 記進待更新集合，
// 並在短暫去抖動後「靜默」失效對應 SWR 快取（背景重抓、資料原地換新）。
// v0.1.600 起移除原本的 RefreshBadge「有新內容，點我更新」藥丸（使用者拍板：點了沒有可感知的
// 變化、還讓玩家覺得系統一直在更新怪怪的）——改為自動靜默刷新，SWR 背景 revalidate 不打斷閱讀。
export default function SiteRealtime() {
  const user = useUser()
  const userId = user?.id ?? null
  const addTopic = useSiteRealtimeStore((s) => s.addTopic)
  const refreshAndClear = useSiteRealtimeStore((s) => s.refreshAndClear)
  const bumpMail = useSiteRealtimeStore((s) => s.bumpMail)
  const [revoked, setRevoked] = useState(false) // 單一登入：本裝置被踢下線時顯示提示彈窗
  // 靜默刷新去抖動：後台批次操作常在一兩秒內連發多個 data_updated，收斂成一次失效
  const refreshTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const backoffRef = useRef(INITIAL_BACKOFF_MS)
  const failStreakRef = useRef(0) // 連續「從未 onopen 就 onclose」的次數（握手失敗＝多半 401）
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

    const connect = async () => {
      if (closingRef.current) return
      let token = getUserToken()
      if (!token) return
      clearReconnectTimer()
      // token 已過期／30 秒內過期 → 先續期再連（避免用一把必定 401 的 token 去握手）
      const left = userTokenExpiresInSec(token)
      if (left != null && left < 30) {
        try {
          const fresh = await refreshUserToken()
          if (!fresh) { clearUserSession(); return } // refresh 明確失敗＝session 已死 → 登出、不再重連
          token = fresh
        } catch { /* 暫時性錯誤：照用舊 token 試一次，失敗走下面的退避 */ }
        if (closingRef.current) return
      }

      let opened = false
      const ws = createSiteSocket(token)
      wsRef.current = ws

      ws.onopen = () => {
        opened = true
        failStreakRef.current = 0
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
        if (msg.topic === 'mail') { bumpMail(); return } // 站內信：自動即時更新未讀紅點（獨立通道）
        if ((DATA_TOPICS as readonly string[]).includes(msg.topic)) {
          addTopic(msg.topic as DataTopic)
          // 靜默自動失效（600ms 去抖動）：取代舊 RefreshBadge 的手動點擊觸發
          if (refreshTimerRef.current) clearTimeout(refreshTimerRef.current)
          refreshTimerRef.current = setTimeout(() => { refreshTimerRef.current = null; refreshAndClear() }, 600)
        }
      }
      ws.onclose = () => {
        if (wsRef.current === ws) wsRef.current = null
        if (closingRef.current) return
        if (!opened) failStreakRef.current += 1
        const streak = failStreakRef.current
        if (streak > 0 && streak % FAIL_STREAK_REFRESH === 0) {
          // 連續握手失敗：最可能是 token 失效（後端 401 不 upgrade）。試著續期，明確失敗就登出停止。
          reconnectTimerRef.current = setTimeout(async () => {
            reconnectTimerRef.current = null
            try {
              const fresh = await refreshUserToken()
              if (!fresh) { clearUserSession(); return }
            } catch { /* 暫時性錯誤：繼續退避重連 */ }
            if (!closingRef.current) connect()
          }, backoffRef.current)
          backoffRef.current = LONG_BACKOFF_MS
          return
        }
        const delay = backoffRef.current
        backoffRef.current = Math.min(backoffRef.current * 2, streak >= FAIL_STREAK_REFRESH ? LONG_BACKOFF_MS : MAX_BACKOFF_MS)
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
      if (refreshTimerRef.current) { clearTimeout(refreshTimerRef.current); refreshTimerRef.current = null }
      wsRef.current?.close()
      wsRef.current = null
    }
  }, [userId, addTopic, refreshAndClear, bumpMail])

  return revoked ? <SessionRevokedModal onClose={() => setRevoked(false)} /> : null
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
