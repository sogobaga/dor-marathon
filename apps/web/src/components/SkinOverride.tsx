'use client'

// SkinOverride：帳號層級「風格設定」的權威切換器（第 22 套未來科技＋第 23 套復古 RPG＋第 24 套
// 溫馨可愛，見契約
// scratchpad/retro_skin/CONTRACT.md §2）。掛在 root layout 的 client providers 內，本身不渲染畫面
// 以外的「風格背景層」（scifi 的 ParticleField／retro 的 RetroBackground），純粹依「登入 uid +
// dashboard.skin_select_entry + dashboard.ui_skin」三個條件同步 <html data-skin>／meta
// theme-color／localStorage 覆寫記錄。
//
// 三個條件缺一律恢復 originalSkin/originalThemeColor（layout.tsx 依 SSR 讀到的 active_skin 算好傳入）：
//   - 未登入 / 登出 / 換帳號 → uid 為 null 或改變
//   - dashboard.skin_select_entry !== 'shown'（非白名單帳號、或後台把入口關掉）
//   - dashboard.ui_skin 是 'default' 或 null（使用者在 ProfileScreen「風格設定」選了預設風格）
//
// 非白名單使用者完全不受影響的關鍵：useDashboard() 本來就是所有頁面共用的同一份請求（不會因為多了這個
// 元件而多打一次 API），且當條件不滿足時這裡只會呼叫 restoreOriginalSkin（等同 no-op，因為
// dataset.skin 本來就等於 originalSkin，从未被改成 scifi/retro 過）。
import { useEffect, useState } from 'react'
import dynamic from 'next/dynamic'
import { useUser } from '@/lib/userAuth'
import { useDashboard } from '@/lib/useDashboard'
import { applySkinOverride, restoreOriginalSkin, SKIN_CHANGE_EVENT, type OverrideSkin } from '@/lib/skinOverride'

// 全站背景粒子層（未來科技，契約 scifi_skin/CONTRACT.md §3）：next/dynamic(ssr:false) 動態載入，
// 只在下方 active==='scifi' 時才會實際掛載 render——非白名單使用者（active 恆 null）從未 import 這支
// 元件，不進首屏 bundle，也不進任何獨立 chunk 的網路請求（dynamic import 的 chunk 只在被 render
// 時才會被瀏覽器抓取）。
const ParticleField = dynamic(() => import('@/components/scifi/ParticleField'), { ssr: false })
// 全站像素背景層（復古 RPG，契約 retro_skin/CONTRACT.md §3）：同上道理，只在 active==='retro' 時才
// 掛載。RetroBackground 由 THEME 工人另外建立於 components/retro/RetroBackground.tsx（default
// export）；在它落地前這行 import 找不到模組是預期中的單一 tsc 錯誤（契約 §6 已註明可接受）。
const RetroBackground = dynamic(() => import('@/components/retro/RetroBackground'), { ssr: false })
// 全站彩色紙屑/星星/雲朵背景層（溫馨可愛，第 24 套，契約 docs/skins/CUTE_CONTRACT.md §3）：同上
// 道理，只在 active==='cute' 時才掛載。CuteBackground 由 THEME 工人另外建立於
// components/cute/CuteBackground.tsx（default export）；在它落地前這行 import 找不到模組是預期中的
// 單一 tsc 錯誤（契約 §6 已註明可接受，同 RetroBackground 前例）。
const CuteBackground = dynamic(() => import('@/components/cute/CuteBackground'), { ssr: false })

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
  const entryShown = dash?.skin_select_entry === 'shown'
  const uiSkin = dash?.ui_skin ?? null
  const [active, setActive] = useState<OverrideSkin | null>(null)

  useEffect(() => {
    function sync() {
      const isOverride = !!uid && entryShown && (uiSkin === 'scifi' || uiSkin === 'retro' || uiSkin === 'cute')
      const next: OverrideSkin | null = isOverride ? (uiSkin as OverrideSkin) : null
      setActive(next)
      if (next && uid) {
        applySkinOverride(uid, next)
      } else {
        restoreOriginalSkin(originalSkin, originalThemeColor)
      }
    }
    sync()
    // 舊版(第22套)偏好開關已移除，但 ProfileScreen 選擇風格後仍會廣播這個事件（配合 dashboard 重抓），
    // 讓畫面不必整頁重整就能立即套用／收回。
    window.addEventListener(SKIN_CHANGE_EVENT, sync)
    return () => window.removeEventListener(SKIN_CHANGE_EVENT, sync)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [uid, entryShown, uiSkin, originalSkin, originalThemeColor])

  if (active === 'scifi') return <ParticleField />
  if (active === 'retro') return <RetroBackground />
  if (active === 'cute') return <CuteBackground />
  return null
}
