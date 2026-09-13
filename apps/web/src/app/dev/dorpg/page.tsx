import type { Metadata } from 'next'
import { notFound } from 'next/navigation'
import Preview from './Preview'

// DORPG 戰鬥畫面的開發預覽頁（截圖／像素比對用）：繞過 PhoneShell 直接全螢幕掛 BattleScreen。
// 只有以 DORPG_DEV=1 啟動時才存在，其餘一律 404——這頁沒有登入／資格閘門，不能讓正式站看得到。
export const metadata: Metadata = {
  title: 'DORPG 戰鬥畫面預覽',
  robots: { index: false, follow: false },
}

// 每個請求都重新讀 env：不要在 build 時把 notFound 烤死，開發者在本機或測試環境設好變數即可開啟。
export const dynamic = 'force-dynamic'

export default function DorpgDevPage() {
  if (process.env.DORPG_DEV !== '1') notFound()
  return <Preview />
}
