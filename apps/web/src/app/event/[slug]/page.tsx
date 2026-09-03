import type { Metadata } from 'next'
import { cache } from 'react'
import EventLanding from './EventLanding'

// 活動廣告落地頁 OG/SEO 用的輕量公開資料型別（GET /races/{slug}/meta 的回應，非完整 Race——
// 完整 Race 含報名狀態/名額等即時資料，故意不走這支只給爬蟲/OG 用的輕量端點）。
// ⚠️ 欄位名必須與 Go race.RaceMeta（internal/race/meta_cache.go）的 json tag 完全一致：fetch 結果只是型別斷言、
// tsc 抓不到打錯的欄位名（審查曾抓到 banner_url/og_image_url 這種賽事根本沒有的欄位，導致分享卡片永遠沒圖）。
interface PublicRaceMeta {
  id: string
  slug: string
  title: string
  brochure_title?: string
  subtitle?: string
  hero_image_url?: string
  start_date?: string
  end_date?: string
  status?: string
  event_mode?: string
}

// 伺服器端讀取活動公開詳情（供 SEO/OG metadata 與「查無此活動」判斷用）。
// 2026-09-03 改走 /races/{slug}/meta 輕量端點（Go 端有 in-process TTL 快取，不像 /races/{slug} 那樣
// 每次都直接查 DB）——這支只給匿名 SSR/爬蟲用，不含名額/報名狀態等即時欄位，所以可以放心拉長快取。
// fetch 快取拉到 600 秒（原 30 秒）：爬蟲夜間掃頁不該把 Neon 撐醒；活動標題/副標/封面圖幾乎不會
// 分秒必爭地更新，後台改了最多等 10 分鐘生效可接受。逾時/失敗一律回 null → 退回通用標題。
const getRaceMeta = cache(async (slug: string): Promise<PublicRaceMeta | null> => {
  try {
    const base = process.env.API_URL || 'http://localhost:8080'
    const res = await fetch(`${base}/api/v1/races/${encodeURIComponent(slug)}/meta`, {
      next: { revalidate: 600 },
      signal: AbortSignal.timeout(2500),
    })
    if (!res.ok) return null
    const j = await res.json()
    return (j?.race as PublicRaceMeta) || null
  } catch {
    return null
  }
})

export async function generateMetadata({ params }: { params: { slug: string } }): Promise<Metadata> {
  const race = await getRaceMeta(params.slug)
  if (!race) {
    return { title: '活動｜DOR' }
  }
  // 分享標題優先用主辦可編輯的簡章標題（brochure_title），與站內簡章頁同口徑
  const title = `${race.brochure_title || race.title}｜DOR`
  const description = race.subtitle || '點擊查看活動簡章與報名資訊'
  const site = process.env.NEXT_PUBLIC_SITE_URL || 'https://www.dor.tw'
  // OG 圖＝賽事主視覺 hero_image_url（後端 meta 端點唯一的圖片欄位）
  const rawImg = race.hero_image_url
  const img = rawImg
    ? (rawImg.startsWith('http') ? rawImg : site + rawImg)
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

// ⚠️ 不在 SSR 端擋「查無活動」：SSR 讀不到使用者 token（token 存 localStorage、伺服器端拿不到），
// 對「測試(testing)」控制狀態的活動，後端會因 userID/email 為空而回 404 → 連白名單使用者都被誤擋。
// 因此一律渲染 EventLanding，交給帶著 token 的前端（PhoneShell openEventBrochure）判定可見性；
// 公開活動的 OG 由 generateMetadata 盡力抓取，測試/查無者退回通用標題（可接受）。
export default function EventPage({ params }: { params: { slug: string } }) {
  return <EventLanding slug={params.slug} />
}
