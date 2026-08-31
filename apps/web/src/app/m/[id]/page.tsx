import type { Metadata } from 'next'
import { cache } from 'react'
import { taipeiParts } from '@/lib/runMeet'

// 團練分享短網址 /m/{id}：給社群分享用（RunMeetDetailView.tsx 的分享按鈕會產生這個網址取代
// 原本的 /?runmeet={id}）。後端公開端點免登入、不區分「不存在／已刪／已下架／未開放」，一律回
// {available:false}（防列舉），私密團的 region/place_label 為空字串、cover_url 為 null（不洩漏地點）。
//
// 完全比照 app/event/[slug]/page.tsx（getRaceBySlug）與 app/shop/[id]/page.tsx（getShop）的既有
// OG 慣例：React cache 同請求只查一次、fetch 快取 30 秒、逾時/失敗回 null。
//
// ⚠️ 與 Event/Shop 兩頁不同：那兩頁會掛載完整的 PhoneShell（client SPA）當落地頁本身。
//    這裡刻意不掛 PhoneShell——社群 App 內建瀏覽器（LINE/IG/FB 等）常對「載入一個完整 SPA 再自動跳轉」
//    不穩定，改用一個極簡的純 SSR 靜態畫面 + 一顆手動導向按鈕（見需求：「建議顯示落地畫面再導向，
//    因為社群 App 內建瀏覽器直接 redirect 有時會失敗」），最大化在各種內建瀏覽器下的相容性。

interface ShareInfo {
  available: boolean
  title?: string
  meet_at?: string
  region?: string
  place_label?: string
  cover_url?: string | null
  is_private?: boolean
  member_count?: number
  capacity?: number
}

const SITE_DEFAULT_OG_IMAGE = 'https://www.dor.tw/brand-hero-og-v3.jpg' // 同 app/layout.tsx 的站台預設主視覺

const getShare = cache(async (id: string): Promise<ShareInfo | null> => {
  try {
    const base = process.env.API_URL || 'http://localhost:8080'
    const res = await fetch(`${base}/api/v1/run-meets/${encodeURIComponent(id)}/share`, {
      next: { revalidate: 30 },
      signal: AbortSignal.timeout(2500),
    })
    if (!res.ok) return null
    const j = await res.json()
    return (j as ShareInfo) ?? null
  } catch {
    return null
  }
})

function siteUrl(): string {
  return process.env.NEXT_PUBLIC_SITE_URL || 'https://www.dor.tw'
}

function absoluteUrl(u: string | null | undefined, site: string): string | null {
  if (!u) return null
  return u.startsWith('http') ? u : site + u
}

const pad2 = (n: number) => String(n).padStart(2, '0')

/** 「9/6（六）06:00」——與 lib/runMeet.ts fmtMeetAtConfirm 同規則，但不含「將於...開跑」包裝文字。 */
function fmtDateShort(iso: string): string {
  const t = taipeiParts(iso)
  return `${t.m}/${t.d}（${t.wd}）${pad2(t.hh)}:${pad2(t.mm)}`
}

export async function generateMetadata({ params }: { params: { id: string } }): Promise<Metadata> {
  const share = await getShare(params.id)
  const site = siteUrl()

  // 不存在／已刪／已下架／未開放 → 通用標題，不透露任何團練資訊（後端同一句話防列舉，前端 OG 也一致）。
  if (!share || !share.available) {
    return { title: '團練邀請｜DOR' }
  }

  const title = `${share.title || '團練邀請'}｜DOR 團練邀請`
  const description = share.is_private
    ? '私密團練・需要密碼加入'
    : [
        share.meet_at ? fmtDateShort(share.meet_at) : '',
        share.region || '',
        share.place_label || '',
        share.capacity ? `${share.member_count ?? 0}/${share.capacity} 人` : '',
      ].filter(Boolean).join('・')

  const img = absoluteUrl(share.cover_url, site) || SITE_DEFAULT_OG_IMAGE

  return {
    title,
    description,
    openGraph: {
      title,
      description,
      url: `${site}/m/${params.id}`,
      type: 'website',
      images: [img],
    },
    twitter: {
      card: 'summary_large_image',
      title,
      description,
      images: [img],
    },
  }
}

export default async function RunMeetSharePage({ params }: { params: { id: string } }) {
  const share = await getShare(params.id)
  const openHref = `/?runmeet=${encodeURIComponent(params.id)}` // PhoneShell 既有 ?runmeet= 深連結處理（保留不動）

  if (!share || !share.available) {
    return (
      <Frame>
        <div style={{ fontSize: 20, fontWeight: 700 }}>這個團練目前無法查看</div>
        <div style={{ fontSize: 13, color: 'rgba(255,255,255,0.6)', lineHeight: 1.7 }}>
          可能已經結束、被刪除、被下架，或尚未開放查看。請確認連結是否正確。
        </div>
        <a href="/" style={homeLinkStyle}>回首頁</a>
      </Frame>
    )
  }

  const memberText = share.capacity ? `${share.member_count ?? 0} / ${share.capacity} 人` : ''

  return (
    <Frame>
      {share.cover_url && (
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={share.cover_url}
          alt=""
          style={{ width: '100%', maxWidth: 420, aspectRatio: '16 / 9', objectFit: 'cover', borderRadius: 16, border: '1px solid rgba(255,255,255,0.12)' }}
        />
      )}

      <div style={{ fontSize: 21, fontWeight: 800, lineHeight: 1.4, wordBreak: 'break-word' }}>{share.title}</div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 6, fontSize: 13.5, color: 'rgba(255,255,255,0.82)', lineHeight: 1.8 }}>
        {share.meet_at && <div>🕕 {fmtDateShort(share.meet_at)}</div>}
        {share.is_private ? (
          <div>🔒 私密團練・需要密碼加入</div>
        ) : (
          (share.region || share.place_label) && (
            <div>📍 {share.region}{share.place_label ? ` · ${share.place_label}` : ''}</div>
          )
        )}
        {memberText && <div>👥 {memberText}</div>}
      </div>

      <a href={openHref} style={ctaStyle}>開啟 DOR 查看團練</a>
    </Frame>
  )
}

function Frame({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        minHeight: '100dvh',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 14,
        background: '#09090f',
        color: '#fff',
        textAlign: 'center',
        padding: '24px 20px',
      }}
    >
      {children}
    </div>
  )
}

const homeLinkStyle: React.CSSProperties = { marginTop: 12, color: '#fff', textDecoration: 'underline' }

const ctaStyle: React.CSSProperties = {
  marginTop: 8,
  display: 'inline-block',
  background: '#2DE59A',
  color: '#04140d',
  fontWeight: 800,
  fontSize: 15,
  padding: '13px 28px',
  borderRadius: 999,
  textDecoration: 'none',
}
