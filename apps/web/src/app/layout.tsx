import type { Metadata, Viewport } from 'next'
import { cache } from 'react'
import './globals.css'
import InAppBrowserNotice from '@/components/InAppBrowserNotice'
import InterstitialAd from '@/components/InterstitialAd'
import LandscapeNotice from '@/components/LandscapeNotice'
import Analytics from '@/components/Analytics'
import AppProviders from '@/components/AppProviders'
import ViewportHeightFix from '@/components/ViewportHeightFix'
import ViewportDebug from '@/components/ViewportDebug'
import PwaInstallPrompt from '@/components/PwaInstallPrompt'
import UpdateNotice from '@/components/UpdateNotice'

// 各 skin 的瀏覽器 chrome（狀態列）色；新增 skin 時在此與 globals.css/appSettings/後端 specs 一併加。
const SKIN_THEME_COLOR: Record<string, string> = { default: '#09090f', warm: '#FBF4E9', warm2: '#FBF5EA' }

// 伺服器端讀取前台公開系統設定（skin、favicon…）：直接寫進 SSR，第一次繪製就正確、不靠 localStorage。
// React cache：同一請求只查一次；fetch 快取 30 秒（改設定約 30 秒內於「下次載入」生效）。逾時/失敗回空 → 用預設。
const getPublicSettings = cache(async (): Promise<Record<string, string>> => {
  try {
    const base = process.env.API_URL || 'http://localhost:8080'
    const res = await fetch(`${base}/api/v1/app-settings/public`, { next: { revalidate: 30 }, signal: AbortSignal.timeout(2500) })
    if (!res.ok) return {}
    const j = await res.json()
    return (j?.settings as Record<string, string>) || {}
  } catch {
    return {}
  }
})

function skinOf(s: Record<string, string>): string {
  const v = s.active_skin
  return typeof v === 'string' && SKIN_THEME_COLOR[v] && v !== 'default' ? v : 'default'
}

export async function generateMetadata(): Promise<Metadata> {
  const s = await getPublicSettings()
  const fav = s.favicon_url // 後台可自訂的瀏覽器分頁 favicon（未設 → 用內建 icon）
  return {
    title: 'DOR 城市探索',
    description: '一場把城市變成賽道的跑步挑戰——不用站上起跑線，也能用每一次出門，跑出屬於自己的完賽故事。',
    manifest: '/manifest.json',
    icons: {
      icon: fav ? [{ url: fav }] : [
        { url: '/icon-192.png', sizes: '192x192', type: 'image/png' },
        { url: '/icon-512.png', sizes: '512x512', type: 'image/png' },
      ],
      // iOS「加入主畫面」的圖示：**固定用 /apple-touch-icon.png，刻意不吃後台的 favicon_url**。
      // favicon 通常是 16~48px 的分頁小圖，拿去當主畫面 App 圖示會糊掉/變形（曾實際發生）；
      // 兩者用途不同，不該共用同一個設定。要換 App 圖示請替換 public/apple-touch-icon.png。
      apple: '/apple-touch-icon.png',
    },
    appleWebApp: { capable: true, statusBarStyle: 'black-translucent', title: 'DOR' },
    openGraph: {
      title: 'DOR｜城市探索',
      description: '一場把城市變成賽道的跑步挑戰——不用站上起跑線，也能用每一次出門，跑出屬於自己的完賽故事。',
      url: 'https://www.dor.tw',
      siteName: 'DOR｜城市探索',
      locale: 'zh_TW',
      type: 'website',
      images: [{ url: 'https://www.dor.tw/brand-hero-og-v3.jpg', width: 1200, height: 628, alt: 'DOR｜城市探索——把城市，變成你的遊戲場' }],
    },
    twitter: {
      card: 'summary_large_image', // 有 1200×630 主視覺後改大圖卡（原 summary 小方圖）
    },
  }
}

export async function generateViewport(): Promise<Viewport> {
  const skin = skinOf(await getPublicSettings())
  return {
    width: 'device-width',
    initialScale: 1,
    maximumScale: 1,
    userScalable: false,
    viewportFit: 'cover',
    // 明文宣告「虛擬鍵盤只縮 visual viewport、不縮版面」——這正是本站要的行為。
    // ・iOS：WebKit 尚未實作（bug 259770，2023 開單至今 NEW）→ 這行是 no-op，不會壞任何事；
    //   而 iOS 現行預設本來就等同 resizes-visual。
    // ・Android Chrome 108+ 預設同樣是 resizes-visual → 明寫等於把行為釘死，不怕未來預設值變動，
    //   並可救回舊 Chrome/WebView 那種「鍵盤壓縮 layout viewport」的預設。
    // ⚠️ 永遠不要改成 resizes-content：那就是症狀 B 的規格化版本。
    // ⚠️ 也不要用 overlays-content：會奪走瀏覽器「自動捲到焦點輸入框」的能力。
    interactiveWidget: 'resizes-visual',
    themeColor: SKIN_THEME_COLOR[skin] || SKIN_THEME_COLOR.default,
  }
}

export default async function RootLayout({ children }: { children: React.ReactNode }) {
  const skin = skinOf(await getPublicSettings())
  return (
    <html lang="zh-TW" data-skin={skin !== 'default' ? skin : undefined}>
      <body><AppProviders><ViewportHeightFix /><ViewportDebug /><Analytics /><InAppBrowserNotice /><InterstitialAd /><PwaInstallPrompt /><UpdateNotice /><LandscapeNotice />{children}</AppProviders></body>
    </html>
  )
}
