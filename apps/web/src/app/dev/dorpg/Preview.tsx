'use client'

// 開發預覽的 client 外殼：BattleScreen 用 ResizeObserver／useLayoutEffect 量自己，只能在瀏覽器渲染，
// 故 ssr:false；position:fixed 鋪滿視窗，模擬 PhoneShell 內容區「填滿父層」的條件。
import dynamic from 'next/dynamic'
import { PALETTE } from '@/lib/dorpg/assets'

const BattleScreen = dynamic(() => import('@/components/dorpg/BattleScreen'), {
  ssr: false,
  loading: () => <div style={{ position: 'fixed', inset: 0, background: PALETTE.surfaceBase }} />,
})

export default function Preview() {
  return (
    <div style={{ position: 'fixed', inset: 0, background: PALETTE.surfaceBase }}>
      {/* 預覽頁沒有「上一頁」可回；逃跑鈕在這裡是 no-op。
          debugAllowed=true：這頁本來就只在 DORPG_DEV=1 才存在（見 app/dev/dorpg/page.tsx 的 notFound()
          守門），是唯一允許 ?dorpgDebug=1 生效的地方——2026-09-14 審查修正：正式戰鬥入口不會傳這個
          prop，就算網址加了 query 也不會有任何效果。 */}
      <BattleScreen onBack={() => {}} debugAllowed />
    </div>
  )
}
