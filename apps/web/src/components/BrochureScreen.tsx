'use client'

import useSWR from 'swr'
import { useState } from 'react'
import { racesApi, type Race, type RaceDetail, type BrochureBlock } from '@/lib/api'
import { getUserToken } from '@/lib/userAuth'
import { MediaCarousel, Lightbox, YouTubeEmbed, ytId } from '@/components/shared/MediaCarousel'

// 圖片區塊 content：新版存「網址陣列」JSON；相容舊的單一網址字串
function imagesOf(content: string): string[] {
  const c = (content ?? '').trim()
  if (!c) return []
  if (c.startsWith('[')) {
    try {
      const a = JSON.parse(c)
      return Array.isArray(a) ? a.filter(Boolean) : []
    } catch {
      return []
    }
  }
  return [c]
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

      <div style={{ flex: 1, overflowY: 'auto', padding: '4px 20px 32px' }}>
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
    return <div style={{ fontSize: 14.5, lineHeight: 1.7, color: 'var(--tx)' }} dangerouslySetInnerHTML={{ __html: block.content }} />
  }
  if (block.block_type === 'image') {
    const imgs = imagesOf(block.content)
    if (imgs.length === 0) return null
    return (
      <figure style={{ margin: 0 }}>
        {imgs.length === 1 ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={imgs[0]} alt={block.caption ?? ''}
            onClick={() => onZoom(imgs, 0)}
            style={{ width: '100%', borderRadius: 12, cursor: 'zoom-in', display: 'block' }}
          />
        ) : (
          <MediaCarousel images={imgs} onZoom={onZoom} />
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

function Hint({ children, color = 'var(--tx-dim)' }: { children: React.ReactNode; color?: string }) {
  return <div style={{ textAlign: 'center', padding: '50px 20px', fontSize: 13.5, color }}>{children}</div>
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0 }
const registerBtn: React.CSSProperties = {
  background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none',
  borderRadius: 12, padding: '13px 20px', cursor: 'pointer', fontSize: 15, width: '100%', marginTop: 24,
}
