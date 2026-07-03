import type { Metadata, Viewport } from 'next'
import { cache } from 'react'
import './globals.css'
import InAppBrowserNotice from '@/components/InAppBrowserNotice'
import InterstitialAd from '@/components/InterstitialAd'
import LandscapeNotice from '@/components/LandscapeNotice'

export const metadata: Metadata = {
  title: 'DOR 城市探索',
  description: '一場把城市變成賽道的跑步挑戰——不用站上起跑線，也能用每一次出門，跑出屬於自己的完賽故事。',
  manifest: '/manifest.json',
  icons: {
    icon: [
      { url: '/icon-192.png', sizes: '192x192', type: 'image/png' },
      { url: '/icon-512.png', sizes: '512x512', type: 'image/png' },
    ],
    apple: '/apple-touch-icon.png', // iOS 加入主畫面的圖示
  },
  appleWebApp: {
    capable: true, // iOS 從主畫面啟動時全螢幕（無瀏覽器介面）
    statusBarStyle: 'black-translucent',
    title: 'DOR',
  },
}

// 各 skin 的瀏覽器 chrome（狀態列）色；新增 skin 時在此與 globals.css/appSettings/後端 specs 一併加。
const SKIN_THEME_COLOR: Record<string, string> = { default: '#09090f', warm: '#FBF4E9' }

// 伺服器端讀取目前前台 skin：直接把 data-skin 寫進 SSR 的 <html>，第一次繪製就正確，
// 完全不靠 localStorage、也不會有暗→亮/亮→暗的閃爍。用 React cache 讓同一請求只查一次；
// fetch 快取 30 秒（改設定約 30 秒內於「下次載入」生效，且始終不閃畫面）。
const getActiveSkin = cache(async (): Promise<string> => {
  try {
    const base = process.env.API_URL || 'http://localhost:8080'
    // 逾時保護：API 慢/不可達時不拖住 SSR/build，退回預設（不影響前台其餘功能）
    const res = await fetch(`${base}/api/v1/app-settings/public`, { next: { revalidate: 30 }, signal: AbortSignal.timeout(2500) })
    if (!res.ok) return 'default'
    const j = await res.json()
    const s = j?.settings?.active_skin
    return typeof s === 'string' && SKIN_THEME_COLOR[s] && s !== 'default' ? s : 'default'
  } catch {
    return 'default'
  }
})

export async function generateViewport(): Promise<Viewport> {
  const skin = await getActiveSkin()
  return {
    width: 'device-width',
    initialScale: 1,
    maximumScale: 1,
    userScalable: false,
    viewportFit: 'cover',
    themeColor: SKIN_THEME_COLOR[skin] || SKIN_THEME_COLOR.default,
  }
}

export default async function RootLayout({ children }: { children: React.ReactNode }) {
  const skin = await getActiveSkin()
  return (
    <html lang="zh-TW" data-skin={skin !== 'default' ? skin : undefined}>
      <body><InAppBrowserNotice /><InterstitialAd /><LandscapeNotice />{children}</body>
    </html>
  )
}
