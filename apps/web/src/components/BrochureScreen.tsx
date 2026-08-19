'use client'

import useSWR from 'swr'
import { useState } from 'react'
import { useRouter } from 'next/navigation'
import { racesApi, type Race, type RaceDetail, type BrochureBlock, type BrochureImageItem, normalizeBrochureImage } from '@/lib/api'
import { getUserToken } from '@/lib/userAuth'
import { navigateLink } from '@/lib/links'
import { MediaCarousel, Lightbox, YouTubeEmbed, ytId } from '@/components/shared/MediaCarousel'

// 圖片區塊 content：新版存「陣列」JSON（元素可為純網址字串，或 {url,caption?,link?} 物件，
// 每張圖各自可選填說明文字＋點擊連結）；相容舊的單一網址字串。一律正規化成 BrochureImageItem。
function imagesOf(content: string): BrochureImageItem[] {
  const c = (content ?? '').trim()
  if (!c) return []
  if (c.startsWith('[')) {
    try {
      const a = JSON.parse(c)
      return Array.isArray(a) ? a.filter(Boolean).map(normalizeBrochureImage) : []
    } catch {
      return []
    }
  }
  return [normalizeBrochureImage(c)]
}

export default function BrochureScreen({
  race,
  onBack,
  onRegister,
}: {
  race: Race
  onBack: () => void
  onRegister?: (race: Race) => void
}) {
  const token = getUserToken() || undefined
  const { data, error, isLoading } = useSWR(['brochure', race.id], () => racesApi.detail(race.id, token))
  const [lightbox, setLightbox] = useState<{ images: string[]; index: number } | null>(null)
  const zoom = (images: string[], index: number) => setLightbox({ images, index })

  const detail = data?.race
  const blocks = detail?.brochure ?? []

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 12px', flexShrink: 0 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
      </header>

      <div style={{ flex: 1, overflowY: 'auto', overscrollBehavior: 'contain', padding: '4px 20px 32px' }}>
        {isLoading && <Hint>載入中…</Hint>}
        {error && <Hint color="var(--hunt)">無法載入簡章</Hint>}

        {detail && (
          <>
            <h1 style={{ fontSize: 24, fontWeight: 800, color: 'var(--tx)', margin: '0 0 4px' }}>
              {detail.brochure_title || detail.title}
            </h1>
            <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginBottom: 18 }}>{detail.subtitle}</div>

            {blocks.length === 0 && <Hint>此賽事尚未提供簡章內容</Hint>}

            <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
              {blocks.map((b: BrochureBlock, i) => (
                <Block key={b.id ?? i} block={b} onZoom={zoom} />
              ))}
            </div>

            {detail.can_register && onRegister && (
              <button onClick={() => onRegister(race)} style={registerBtn}>立即報名</button>
            )}
          </>
        )}
      </div>

      {/* 圖片燈箱（支援左右切換同組圖） */}
      {lightbox && (
        <Lightbox images={lightbox.images} index={lightbox.index} onClose={() => setLightbox(null)} />
      )}
    </div>
  )
}

// BrochureBody 簡章內容（標題+區塊+燈箱），供賽事資訊頁「簡章」頁籤重用
export function BrochureBody({ detail }: { detail: RaceDetail }) {
  const [lightbox, setLightbox] = useState<{ images: string[]; index: number } | null>(null)
  const zoom = (images: string[], index: number) => setLightbox({ images, index })
  const blocks = detail.brochure ?? []
  return (
    <div>
      <h1 style={{ fontSize: 22, fontWeight: 800, color: 'var(--tx)', margin: '0 0 4px' }}>
        {detail.brochure_title || detail.title}
      </h1>
      {detail.subtitle && <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginBottom: 18 }}>{detail.subtitle}</div>}
      {blocks.length === 0 && <Hint>此賽事尚未提供簡章內容</Hint>}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
        {blocks.map((b: BrochureBlock, i) => (
          <Block key={b.id ?? i} block={b} onZoom={zoom} />
        ))}
      </div>
      {lightbox && <Lightbox images={lightbox.images} index={lightbox.index} onClose={() => setLightbox(null)} />}
    </div>
  )
}

function Block({ block, onZoom }: { block: BrochureBlock; onZoom: (images: string[], index: number) => void }) {
  if (block.block_type === 'text') {
    return <div className="brochure-html" style={{ fontSize: 14.5, lineHeight: 1.7, color: 'var(--tx)' }} dangerouslySetInnerHTML={{ __html: block.content }} />
  }
  if (block.block_type === 'image') {
    const items = imagesOf(block.content)
    if (items.length === 0) return null
    return (
      <figure style={{ margin: 0 }}>
        {items.length === 1 ? (
          <SingleImage item={items[0]} onZoom={() => onZoom([items[0].url], 0)} />
        ) : (
          <MediaCarousel images={items} onZoom={onZoom} />
        )}
        {block.caption && <figcaption style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 6, textAlign: 'center' }}>{block.caption}</figcaption>}
      </figure>
    )
  }
  if (block.block_type === 'video') {
    const id = ytId(block.content)
    return (
      <div>
        {id ? (
          <YouTubeEmbed url={block.content} title={block.caption ?? 'video'} />
        ) : (
          <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>無效的影片連結</div>
        )}
        {block.caption && <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 6, textAlign: 'center' }}>{block.caption}</div>}
      </div>
    )
  }
  return null
}

// 單張圖片（非輪播）：有 link 時點擊導向連結（站內 push／站外開新分頁），無 link 則維持原本
// 放大(Lightbox)行為；caption 顯示在圖片正下方一行小字。
function SingleImage({ item, onZoom }: { item: BrochureImageItem; onZoom: () => void }) {
  const router = useRouter()
  return (
    <>
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img
        src={item.url} alt={item.caption ?? ''}
        onClick={() => (item.link ? navigateLink(item.link, router) : onZoom())}
        style={{ width: '100%', borderRadius: 12, cursor: item.link ? 'pointer' : 'zoom-in', display: 'block' }}
      />
      {item.caption && <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 6, textAlign: 'center' }}>{item.caption}</div>}
    </>
  )
}

function Hint({ children, color = 'var(--tx-dim)' }: { children: React.ReactNode; color?: string }) {
  return <div style={{ textAlign: 'center', padding: '50px 20px', fontSize: 13.5, color }}>{children}</div>
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0 }
const registerBtn: React.CSSProperties = {
  background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none',
  borderRadius: 12, padding: '13px 20px', cursor: 'pointer', fontSize: 15, width: '100%', marginTop: 24,
}
