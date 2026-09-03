import type { Metadata } from 'next'
import { cache } from 'react'
import ShopLanding from './ShopLanding'

// 合作商家專屬公開連結頁的公開資料型別（僅取用得到的欄位，非完整 PartnerShopDetail）。
interface PublicShop {
  name: string
  summary?: string
  banner_url?: string
}

// 伺服器端讀取商家公開詳情（供 SEO/OG metadata 與「查無此商家」判斷用）。
// 完全比照 app/event/[slug]/page.tsx 的 getRaceMeta pattern：React cache 同請求只查一次、逾時/失敗回 null。
// fetch 快取 2026-09-03 由 30 秒拉到 300 秒（降低 Neon 夜間空轉喚醒；name/summary/banner_url 非個人化、
// 收藏狀態等即時資料仍由 ShopLanding 掛載後的 client-side 認證 fetch 取得，不受影響）。
// GET /api/v1/partner-shops/{id} 回傳形狀為 {shop: {...}}（見 services/api/internal/partner/handler.go Detail）。
const getShop = cache(async (id: string): Promise<PublicShop | null> => {
  try {
    const base = process.env.API_URL || 'http://localhost:8080'
    const res = await fetch(`${base}/api/v1/partner-shops/${encodeURIComponent(id)}`, {
      next: { revalidate: 300 },
      signal: AbortSignal.timeout(2500),
    })
    if (!res.ok) return null
    const j = await res.json()
    return (j?.shop as PublicShop) || null
  } catch {
    return null
  }
})

export async function generateMetadata({ params }: { params: { id: string } }): Promise<Metadata> {
  const shop = await getShop(params.id)
  if (!shop) {
    return { title: '合作商家｜DOR' }
  }
  const title = `${shop.name}｜DOR`
  const description = shop.summary || '查看合作商家資訊'
  const site = process.env.NEXT_PUBLIC_SITE_URL || 'https://www.dor.tw'
  const img = shop.banner_url
    ? (shop.banner_url.startsWith('http') ? shop.banner_url : site + shop.banner_url)
    : undefined
  return {
    title,
    description,
    openGraph: {
      title,
      description,
      url: `${site}/shop/${params.id}`,
      type: 'website',
      images: img ? [img] : [],
    },
    twitter: {
      card: 'summary_large_image',
      title,
      description,
      images: img ? [img] : [],
    },
  }
}

export default async function ShopPage({ params }: { params: { id: string } }) {
  const shop = await getShop(params.id)

  // 查無此商家（不存在／已下架）→ 簡單置中訊息頁，不依賴其他元件，避免 SSR 出錯。
  if (!shop) {
    return (
      <div
        style={{
          minHeight: '100dvh',
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          gap: 12,
          background: '#09090f',
          color: '#fff',
          textAlign: 'center',
          padding: 24,
        }}
      >
        <div style={{ fontSize: 20, fontWeight: 700 }}>找不到這個商家</div>
        <div style={{ fontSize: 13, color: 'rgba(255,255,255,0.6)' }}>
          可能已下架或尚未開放，請確認連結是否正確。
        </div>
        <a href="/" style={{ marginTop: 12, color: '#fff', textDecoration: 'underline' }}>
          回首頁
        </a>
      </div>
    )
  }

  return <ShopLanding id={params.id} />
}
