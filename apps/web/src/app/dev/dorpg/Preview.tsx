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
      {/* 預覽頁沒有「上一頁」可回；逃跑鈕在這裡是 no-op */}
      <BattleScreen onBack={() => {}} />
    </div>
  )
}
