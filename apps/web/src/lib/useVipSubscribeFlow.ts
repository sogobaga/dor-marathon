'use client'

import { useCallback, useState } from 'react'
import { profileApi, type VipSubscribeResponse } from './api'
import { getUserToken, withUserAuth } from './userAuth'
import { refreshDashboard } from './useDashboard'

// VIP 訂閱 Phase E：UpgradeVipModal 選定方案 → 呼叫 /profile/vip/subscribe → 成功即交出 BindCardModal
// 所需的全部參數（token/server_type/order_id…）。ProfileScreen（手動「升級VIP」入口）與 PhoneShell
// （試用到期自動彈出的 UpgradeVipModal）共用同一套 subscribe→綁卡 orchestration，避免兩處各自維護
// 一份重複邏輯（尤其是 409「已有進行中訂閱」等錯誤文案，後端已經很講究，前端不該各自兜一份）。
export interface VipBindCardState extends VipSubscribeResponse {
  plan: 'monthly' | 'annual'
}

export function useVipSubscribeFlow() {
  const [bindCard, setBindCard] = useState<VipBindCardState | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const subscribe = useCallback(async (plan: 'monthly' | 'annual') => {
    if (!getUserToken()) return
    setBusy(true)
    setError('')
    try {
      const res = await withUserAuth((t) => profileApi.vipSubscribe(t, plan))
      setBindCard({ ...res, plan })
    } catch (e: any) {
      setError(e?.message || '訂閱發起失敗，請稍後再試')
    } finally {
      setBusy(false)
    }
  }, [])

  const closeBindCard = useCallback(() => setBindCard(null), [])

  // 綁卡付款成功：關閉綁卡視窗 + 讓全站用到會員儀表板的畫面（會員卡 VIP 徽章/到期日等）重抓一次。
  const handleBindSuccess = useCallback(() => {
    setBindCard(null)
    refreshDashboard()
  }, [])

  return { bindCard, busy, error, subscribe, closeBindCard, handleBindSuccess }
}
