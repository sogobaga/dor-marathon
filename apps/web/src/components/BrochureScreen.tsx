'use client'

import useSWR from 'swr'
import { useState } from 'react'
import { racesApi, type Race, type BrochureBlock } from '@/lib/api'
import { getUserToken } from '@/lib/userAuth'

// 從各種 YouTube 連結取出 video id
function ytId(url: string): string | null {
  const u = url.trim()
  const patterns = [
    /[?&]v=([A-Za-z0-9_-]{11})/,
    /youtu\.be\/([A-Za-z0-9_-]{11})/,
    /youtube\.com\/embed\/([A-Za-z0-9_-]{11})/,
    /youtube\.com\/shorts\/([A-Za-z0-9_-]{11})/,
  ]
  for (const p of patterns) {
    const m = u.match(p)
    if (m) return m[1]
  }
  if (/^[A-Za-z0-9_-]{11}$/.test(u)) return u
  return null
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
  const [lightbox, setLightbox] = useState<string | null>(null)

  const detail = data?.race
  const blocks = detail?.brochure ?? []

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: '52px 22px 12px', flexShrink: 0 }}>
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
                <Block key={b.id ?? i} block={b} onZoom={setLightbox} />
              ))}
            </div>

            {detail.can_register && onRegister && (
              <button onClick={() => onRegister(race)} style={registerBtn}>立即報名</button>
            )}
          </>
        )}
      </div>

      {/* 圖片燈箱 */}
      {lightbox && (
        <div style={lightboxStyle} onClick={() => setLightbox(null)}>
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={lightbox} alt="" style={{ maxWidth: '100%', maxHeight: '100%', objectFit: 'contain' }} />
        </div>
      )}
    </div>
  )
}

function Block({ block, onZoom }: { block: BrochureBlock; onZoom: (url: string) => void }) {
  if (block.block_type === 'text') {
    return <div style={{ fontSize: 14.5, lineHeight: 1.7, color: 'var(--tx)' }} dangerouslySetInnerHTML={{ __html: block.content }} />
  }
  if (block.block_type === 'image') {
    return (
      <figure style={{ margin: 0 }}>
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img
          src={block.content} alt={block.caption ?? ''}
          onClick={() => onZoom(block.content)}
          style={{ width: '100%', borderRadius: 12, cursor: 'zoom-in', display: 'block' }}
        />
        {block.caption && <figcaption style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 6, textAlign: 'center' }}>{block.caption}</figcaption>}
      </figure>
    )
  }
  if (block.block_type === 'video') {
    const id = ytId(block.content)
    return (
      <div>
        {id ? (
          <div style={{ position: 'relative', paddingBottom: '56.25%', height: 0, borderRadius: 12, overflow: 'hidden' }}>
            <iframe
              src={`https://www.youtube.com/embed/${id}`}
              title={block.caption ?? 'video'}
              allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture"
              allowFullScreen
              style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', border: 0 }}
            />
          </div>
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
  background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none',
  borderRadius: 12, padding: '13px 20px', cursor: 'pointer', fontSize: 15, width: '100%', marginTop: 24,
}
const lightboxStyle: React.CSSProperties = {
  position: 'fixed', inset: 0, background: 'rgba(0,0,0,.92)', zIndex: 90,
  display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 16, cursor: 'zoom-out',
}
