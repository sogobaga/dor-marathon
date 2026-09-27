'use client'

// SkinOverride：未來科幻世界風格（scifi skin）的權威切換器（第 22 套，見契約 §2）。掛在 root layout
// 的 client providers 內，本身不渲染任何畫面（return null），純粹依「登入 uid + dashboard.scifi_entry
// + 裝置偏好」三個條件同步 <html data-skin>／meta theme-color／localStorage 覆寫記錄。
//
// 三個條件缺一律恢復 originalSkin/originalThemeColor（layout.tsx 依 SSR 讀到的 active_skin 算好傳入）：
//   - 未登入 / 登出 / 換帳號 → uid 為 null 或改變
//   - dashboard.scifi_entry !== 'shown'（非白名單帳號、或後台把入口關掉）
//   - 使用者自己在 ProfileScreen 把偏好切成 off
//
// 非白名單使用者完全不受影響的關鍵：useDashboard() 本來就是所有頁面共用的同一份請求（不會因為多了這個
// 元件而多打一次 API），且當 scifi_entry 不是 'shown' 時這裡只會呼叫 restoreOriginalSkin（等同 no-op，
// 因為 dataset.skin 本來就等於 originalSkin，从未被改成 'scifi' 過）。
import { useEffect, useState } from 'react'
import dynamic from 'next/dynamic'
import { useUser } from '@/lib/userAuth'
import { useDashboard } from '@/lib/useDashboard'
import { applyScifiSkin, restoreOriginalSkin, getSkinPref, SKIN_CHANGE_EVENT } from '@/lib/skinOverride'

// 全站背景粒子層（契約 §3）：next/dynamic(ssr:false) 動態載入，只在下方 active 為真時才會實際掛載
// render——非白名單使用者（active 恆 false）從未 import 這支元件，不進首屏 bundle，也不進任何獨立
// chunk 的網路請求（dynamic import 的 chunk 只在被 render 時才會被瀏覽器抓取）。
const ParticleField = dynamic(() => import('@/components/scifi/ParticleField'), { ssr: false })

export default function SkinOverride({
  originalSkin,
  originalThemeColor,
}: {
  originalSkin: string
  originalThemeColor: string
}) {
  const user = useUser()
  const { dash } = useDashboard()
  const uid = user?.id ?? null
  const shown = dash?.scifi_entry === 'shown'
  const [active, setActive] = useState(false)

  useEffect(() => {
    function sync() {
      const isActive = !!uid && shown && getSkinPref() !== 'off'
      setActive(isActive)
      if (isActive && uid) {
        applyScifiSkin(uid)
      } else {
        restoreOriginalSkin(originalSkin, originalThemeColor)
      }
    }
    sync()
    // 偏好開關（ProfileScreen）改動時透過這個事件通知，不需要整頁重整就能立即生效／立即恢復。
    window.addEventListener(SKIN_CHANGE_EVENT, sync)
    return () => window.removeEventListener(SKIN_CHANGE_EVENT, sync)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [uid, shown, originalSkin, originalThemeColor])

  return active ? <ParticleField /> : null
}
