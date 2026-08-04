'use client'

import dynamic from 'next/dynamic'

// 比照 app/page.tsx：PhoneShell 是純 client SPA，SSR 會造成 hydration 不一致，改 client-only 掛載。
const PhoneShell = dynamic(() => import('@/components/PhoneShell'), {
  ssr: false,
  loading: () => <div style={{ minHeight: '100dvh' }} />,
})

// 廣告落地頁 /event/{slug} 的 client 進入點：帶 openEventSlug 讓 PhoneShell 開頁即直接顯示該活動簡章。
export default function EventLanding({ slug }: { slug: string }) {
  return (
    <main className="phone-frame">
      <PhoneShell openEventSlug={slug} />
    </main>
  )
}
