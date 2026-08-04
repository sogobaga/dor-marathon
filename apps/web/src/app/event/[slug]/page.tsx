import type { Metadata } from 'next'
import { cache } from 'react'
import EventLanding from './EventLanding'

// 活動廣告落地頁的公開資料型別（僅取用得到的欄位，非完整 Race）。
interface PublicRace {
  title: string
  brochure_title?: string
  subtitle?: string
  hero_image_url?: string
}

// 伺服器端讀取活動公開詳情（供 SEO/OG metadata 與「查無此活動」判斷用）。
// 照抄 app/layout.tsx 的 getPublicSettings pattern：React cache 同請求只查一次、fetch 快取 30 秒、逾時/失敗回 null。
const getRaceBySlug = cache(async (slug: string): Promise<PublicRace | null> => {
  try {
    const base = process.env.API_URL || 'http://localhost:8080'
    const res = await fetch(`${base}/api/v1/races/${encodeURIComponent(slug)}`, {
      next: { revalidate: 30 },
      signal: AbortSignal.timeout(2500),
    })
    if (!res.ok) return null
    const j = await res.json()
    return (j?.race as PublicRace) || null
  } catch {
    return null
  }
})

export async function generateMetadata({ params }: { params: { slug: string } }): Promise<Metadata> {
  const race = await getRaceBySlug(params.slug)
  if (!race) {
    return { title: '活動｜DOR' }
  }
  const title = `${race.brochure_title || race.title}｜DOR`
  const description = race.subtitle || '點擊查看活動簡章與報名資訊'
  const site = process.env.NEXT_PUBLIC_SITE_URL || 'https://www.dor.tw'
  const img = race.hero_image_url
    ? (race.hero_image_url.startsWith('http') ? race.hero_image_url : site + race.hero_image_url)
    : undefined
  return {
    title,
    description,
    openGraph: {
      title,
      description,
      url: `${site}/event/${params.slug}`,
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

export default async function EventPage({ params }: { params: { slug: string } }) {
  const race = await getRaceBySlug(params.slug)

  // 查無此活動（不存在／已下架）→ 簡單置中訊息頁，不依賴其他元件，避免 SSR 出錯。
  if (!race) {
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
        <div style={{ fontSize: 20, fontWeight: 700 }}>找不到這場活動</div>
        <div style={{ fontSize: 13, color: 'rgba(255,255,255,0.6)' }}>
          可能已結束或尚未開放，請確認連結是否正確。
        </div>
        <a href="/" style={{ marginTop: 12, color: '#fff', textDecoration: 'underline' }}>
          回首頁
        </a>
      </div>
    )
  }

  return <EventLanding slug={params.slug} />
}
