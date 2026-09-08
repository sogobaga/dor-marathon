'use client'

import useSWR from 'swr'
import { useEffect, useState } from 'react'
import { mileageExpApi } from '@/lib/api'
import { useUser, getUserToken, withUserAuth } from '@/lib/userAuth'
import { refreshDashboard } from '@/lib/useDashboard'
import ExpSettlementModal from './ExpSettlementModal'

// 全域：偵測未顯示的日常里程 EXP，彈出結算演出（與完賽結算同風格）
export default function MileageExpGate() {
  const user = useUser()
  const token = getUserToken() || undefined
  // 2026-09-08 第二次稽核修法：key 加 user id——這是登入者自己的未顯示里程 EXP，原本用不分
  // 使用者的裸字串 'mileage-exp'，且這個 Gate 元件是全域常駐掛載，同裝置換帳號時特別容易撞到
  // 上一位使用者留在 SWR 快取裡的資料（見 lib/swrCache.ts／RaceDetailScreen.tsx 同一批修法）。
  const { data, mutate } = useSWR(
    user && token ? ['mileage-exp', user.id] : null,
    () => withUserAuth((t) => mileageExpApi.get(t)),
    { refreshInterval: 60000, revalidateOnFocus: true },
  )
  const bd = data?.breakdown
  const [open, setOpen] = useState(false)
  // 有里程獎勵 → 彈窗 + 讓首頁會員卡的 EXP/里程 一起更新
  useEffect(() => { if (bd && bd.gained > 0) { setOpen(true); refreshDashboard() } }, [bd])

  if (!open || !bd || bd.gained <= 0) return null

  return (
    <ExpSettlementModal
      breakdown={bd}
      title="里程達成"
      tagline="DAILY MILEAGE"
      subtitle={`日常里程獎勵 +${bd.gained} EXP`}
      onClose={async () => {
        setOpen(false)
        try { if (token) await withUserAuth((t) => mileageExpApi.markSeen(t)) } catch { /* ignore */ }
        mutate({ breakdown: { ...bd, gained: 0, items: [] } }, { revalidate: false })
      }}
    />
  )
}
