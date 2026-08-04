'use client'

import dynamic from 'next/dynamic'

// 比照 app/event/[slug]/EventLanding.tsx：PhoneShell 是純 client SPA，SSR 會造成 hydration 不一致，改 client-only 掛載。
const PhoneShell = dynamic(() => import('@/components/PhoneShell'), {
  ssr: false,
  loading: () => <div style={{ minHeight: '100dvh' }} />,
})

// 合作商家專屬連結 /shop/{id} 的 client 進入點：帶 openShopId 讓 PhoneShell 開頁即直接顯示該商家詳細頁。
export default function ShopLanding({ id }: { id: string }) {
  return (
    <main className="phone-frame">
      <PhoneShell openShopId={id} />
    </main>
  )
}
