import type { Metadata } from 'next'
import { cache } from 'react'
import { fmtMeetAt, memberCountText, runMeetLocationIcon, runMeetLocationText } from '@/lib/runMeet'
import DeletedRedirect from './DeletedRedirect'

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
  // available:false 時才可能出現：已被發起人刪除。其餘不可用（不存在／已下架／未開放）一律不帶這個欄位，
  // 沿用「這個團練目前無法查看」的通用文案（防列舉：不透露是哪一種不可用）。
  deleted?: boolean
  title?: string
  meet_at?: string
  region?: string
  place_label?: string
  // 「不限地點」（migration 161）：true 時 region/place_label 是「不限」佔位文字——一律先判斷
  // 這個旗標，不要直接拼接兩欄，否則會顯示成「不限・不限」這種沒有意義的字面組合。
  no_location?: boolean
  cover_url?: string | null
  is_private?: boolean
  // 團練說明摘要（後端已截斷到 200 字元）。私密團一律空字串（後端遮蔽，見 share.go），
  // 前端不需要、也拿不到區分「沒填」與「私密被遮蔽」的資訊，兩者都當「不顯示」處理即可。
  description?: string
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
        share.meet_at ? fmtMeetAt(share.meet_at) : '',
        // 「不限地點」一律顯示固定文字，不把 region/place_label 兩欄拼進來——no_location=true
        // 時兩欄後端固定回「不限」，字面拼接會變成「不限・不限」。
        ...(share.no_location ? ['🌏 不限地點'] : [share.region || '', share.place_label || '']),
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
  const listHref = '/?runmeet=list'

  if (!share || !share.available) {
    // 已刪除：與 RunMeetDetailView.tsx 詳情頁 410 時同一句文案 + 3 秒倒數導頁（DeletedRedirect 是
    // client component，這頁本身維持 SSR）。其餘不可用（不存在／下架／未開放）維持原本靜態提示，不倒數。
    if (share?.deleted) {
      return (
        <Frame>
          <DeletedRedirect />
          {/* 手動連結與倒數導向一致（團練邀請頁），不要一個回首頁一個回列表 */}
          <a href={listHref} style={homeLinkStyle}>看看其他團練</a>
        </Frame>
      )
    }
    return (
      <Frame>
        <div style={{ fontSize: 20, fontWeight: 700 }}>這個團練目前無法查看</div>
        <div style={{ fontSize: 13, color: 'rgba(255,255,255,0.6)', lineHeight: 1.7 }}>
          可能已經結束、被刪除、被下架，或尚未開放查看。請確認連結是否正確。
        </div>
        <a href={listHref} style={homeLinkStyle}>看看其他團練</a>
      </Frame>
    )
  }

  // 「已結束」前端自己用 meet_at 跟現在的時間比較即可——share 端點沒有、也不需要多回一個
  // is_ended 欄位（那是詳情/卡片層的計算欄位，見 lib/api.ts），這裡已經有 meet_at 了。
  const ended = !!share.meet_at && new Date(share.meet_at).getTime() <= Date.now()
  const isFull = !!share.capacity && (share.member_count ?? 0) >= share.capacity
  const memberText = share.capacity ? memberCountText(share.member_count ?? 0, share.capacity) : ''

  return (
    <Frame>
      {/* 產品介紹：這頁常是完全沒聽過 DOR 的人第一次看到這個 App（分享連結的宣傳重點），
          放在卡片外面、不搶團練本身的版面。 */}
      <div style={introStyle}>
        DOR 是一款結合 GPS 跑步紀錄與遊戲化任務的訓練 App。「團練邀請」是其中一個功能——想約人一起跑步，直接在 App 內發起就好，大家看得到集合時間地點、按個讚表示要來，不用再另外開群組喬細節。
      </div>

      <div style={cardStyle}>
        {share.cover_url && (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={share.cover_url}
            alt=""
            style={{ width: '100%', aspectRatio: '16 / 9', objectFit: 'cover', borderRadius: 12, border: '1px solid rgba(255,255,255,0.12)' }}
          />
        )}

        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 8, flexWrap: 'wrap' }}>
          <div style={{ fontSize: 21, fontWeight: 800, lineHeight: 1.4, wordBreak: 'break-word' }}>{share.title}</div>
          {ended && <span style={endedPillStyle}>已結束</span>}
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 6, fontSize: 13.5, color: 'rgba(255,255,255,0.82)', lineHeight: 1.8 }}>
          {share.meet_at && <div>🕕 {fmtMeetAt(share.meet_at)}</div>}
          {share.is_private ? (
            <div>🔒 私密團練・需要密碼加入</div>
          ) : (
            (share.no_location || share.region || share.place_label) && (
              <div>
                {runMeetLocationIcon(!!share.no_location)}{' '}
                {runMeetLocationText({ no_location: !!share.no_location, region: share.region || '', place_label: share.place_label || '' })}
              </div>
            )
          )}
          {memberText && <div>👥 {memberText}{isFull ? '・已額滿' : ''}</div>}
        </div>

        {/* 說明摘要：私密團 description 後端一律回空字串（見 share.go），這裡再判一次 is_private
            是防禦性寫法，不是唯一防線。 */}
        {!share.is_private && share.description && (
          <div style={descStyle}>{share.description}</div>
        )}

        {ended ? (
          // 已結束：不引導去加入一個過去的團練，CTA 直接換成看其他團練。
          <a href={listHref} style={ctaStyle}>看看其他團練</a>
        ) : (
          <>
            <a href={openHref} style={ctaStyle}>登入並加入這場團練</a>
            <a href={listHref} style={homeLinkStyle}>看看其他團練</a>
          </>
        )}
      </div>
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

// 產品介紹（第一印象文案）：比卡片本身淡一點、字級小一點，不搶團練資訊的視覺重量。
const introStyle: React.CSSProperties = {
  width: '100%',
  maxWidth: 380,
  fontSize: 12.5,
  lineHeight: 1.8,
  color: 'rgba(255,255,255,0.55)',
  textAlign: 'left',
}

const cardStyle: React.CSSProperties = {
  width: '100%',
  maxWidth: 420,
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  gap: 14,
  background: 'rgba(255,255,255,0.04)',
  border: '1px solid rgba(255,255,255,0.10)',
  borderRadius: 20,
  padding: '20px 18px',
}

const descStyle: React.CSSProperties = {
  width: '100%',
  fontSize: 13,
  lineHeight: 1.8,
  color: 'rgba(255,255,255,0.7)',
  textAlign: 'left',
  wordBreak: 'break-word',
}

const endedPillStyle: React.CSSProperties = {
  display: 'inline-block',
  fontSize: 11,
  fontWeight: 800,
  color: 'rgba(255,255,255,0.75)',
  background: 'rgba(255,255,255,0.12)',
  padding: '3px 9px',
  borderRadius: 999,
  flexShrink: 0,
}
