'use client'

import useSWR from 'swr'
import { useEffect, useRef, useState } from 'react'
import { racesApi, type Race, type BrochureBlock } from '@/lib/api'
import { getUserToken } from '@/lib/userAuth'

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
  const [lightbox, setLightbox] = useState<{ images: string[]; index: number } | null>(null)
  const zoom = (images: string[], index: number) => setLightbox({ images, index })

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
          <Carousel images={imgs} onZoom={onZoom} />
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

function Lightbox({ images, index, onClose }: { images: string[]; index: number; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null)
  const [idx, setIdx] = useState(index)

  useEffect(() => {
    const el = ref.current
    if (el) el.scrollLeft = index * el.clientWidth
  }, [index])

  function onScroll() {
    const el = ref.current
    if (!el) return
    const i = Math.round(el.scrollLeft / el.clientWidth)
    if (i !== idx) setIdx(i)
  }
  function go(e: React.MouseEvent, i: number) {
    e.stopPropagation()
    const el = ref.current
    if (!el) return
    const next = Math.max(0, Math.min(images.length - 1, i))
    el.scrollTo({ left: next * el.clientWidth, behavior: 'smooth' })
  }

  return (
    <div style={lightboxStyle} onClick={onClose}>
      <button onClick={onClose} style={closeBtn} aria-label="關閉">✕</button>

      <div
        ref={ref} onScroll={onScroll}
        style={{ display: 'flex', width: '100%', height: '100%', overflowX: 'auto', scrollSnapType: 'x mandatory', scrollbarWidth: 'none' }}
      >
        {images.map((src, i) => (
          <div key={i} style={{ flex: '0 0 100%', height: '100%', scrollSnapAlign: 'center', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img src={src} alt="" style={{ maxWidth: '100%', maxHeight: '100%', objectFit: 'contain' }} />
          </div>
        ))}
      </div>

      {images.length > 1 && (
        <>
          {idx > 0 && <button onClick={(e) => go(e, idx - 1)} style={{ ...arrowBtn, left: 12, width: 42, height: 42, fontSize: 26 }} aria-label="上一張">‹</button>}
          {idx < images.length - 1 && <button onClick={(e) => go(e, idx + 1)} style={{ ...arrowBtn, right: 12, width: 42, height: 42, fontSize: 26 }} aria-label="下一張">›</button>}
          <div style={{ position: 'absolute', top: 16, left: 16, color: '#fff', fontSize: 13, background: 'rgba(0,0,0,.5)', padding: '3px 10px', borderRadius: 999 }}>
            {idx + 1} / {images.length}
          </div>
          <div style={{ position: 'absolute', bottom: 22, left: 0, right: 0, display: 'flex', justifyContent: 'center', gap: 8 }}>
            {images.map((_, i) => (
              <span key={i} style={{ width: 8, height: 8, borderRadius: 999, background: i === idx ? '#fff' : 'rgba(255,255,255,.4)' }} />
            ))}
          </div>
        </>
      )}
    </div>
  )
}

function Carousel({ images, onZoom }: { images: string[]; onZoom: (images: string[], index: number) => void }) {
  const ref = useRef<HTMLDivElement>(null)
  const [idx, setIdx] = useState(0)

  function onScroll() {
    const el = ref.current
    if (!el) return
    const i = Math.round(el.scrollLeft / el.clientWidth)
    if (i !== idx) setIdx(i)
  }
  function go(i: number) {
    const el = ref.current
    if (!el) return
    const next = Math.max(0, Math.min(images.length - 1, i))
    el.scrollTo({ left: next * el.clientWidth, behavior: 'smooth' })
  }

  return (
    <div style={{ position: 'relative' }}>
      <div
        ref={ref}
        onScroll={onScroll}
        style={{
          display: 'flex', overflowX: 'auto', scrollSnapType: 'x mandatory',
          borderRadius: 12, WebkitOverflowScrolling: 'touch', scrollbarWidth: 'none',
        }}
      >
        {images.map((src, i) => (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            key={i} src={src} alt=""
            onClick={() => onZoom(images, i)}
            style={{ flex: '0 0 100%', width: '100%', scrollSnapAlign: 'center', cursor: 'zoom-in', display: 'block', objectFit: 'cover' }}
          />
        ))}
      </div>

      {/* 左右箭頭引導 */}
      {idx > 0 && <button onClick={() => go(idx - 1)} style={{ ...arrowBtn, left: 8 }} aria-label="上一張">‹</button>}
      {idx < images.length - 1 && <button onClick={() => go(idx + 1)} style={{ ...arrowBtn, right: 8 }} aria-label="下一張">›</button>}

      {/* 計數徽章 */}
      <div style={{ position: 'absolute', top: 8, right: 10, background: 'rgba(0,0,0,.55)', color: '#fff', fontSize: 11, padding: '2px 8px', borderRadius: 999 }}>
        {idx + 1} / {images.length}
      </div>

      {/* 頁碼點 ○●○○○ */}
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', gap: 6, marginTop: 8 }}>
        {images.map((_, i) => (
          <button
            key={i} onClick={() => go(i)} aria-label={`第 ${i + 1} 張`}
            style={{
              width: i === idx ? 8 : 7, height: i === idx ? 8 : 7, borderRadius: 999, border: 'none', padding: 0, cursor: 'pointer',
              background: i === idx ? 'var(--tx)' : 'var(--line-2)', transition: 'background .2s',
            }}
          />
        ))}
      </div>
      <div style={{ textAlign: 'center', fontSize: 11, color: 'var(--tx-faint)', marginTop: 4 }}>← 左右滑動瀏覽 →</div>
    </div>
  )
}

const arrowBtn: React.CSSProperties = {
  position: 'absolute', top: '50%', transform: 'translateY(-50%)',
  width: 32, height: 32, borderRadius: 999, border: 'none', cursor: 'pointer',
  background: 'rgba(0,0,0,.45)', color: '#fff', fontSize: 20, lineHeight: '30px',
  display: 'flex', alignItems: 'center', justifyContent: 'center',
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
  display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'zoom-out',
}
const closeBtn: React.CSSProperties = {
  position: 'absolute', top: 14, right: 14, zIndex: 2, width: 38, height: 38, borderRadius: 999,
  border: 'none', cursor: 'pointer', background: 'rgba(0,0,0,.5)', color: '#fff', fontSize: 18,
  display: 'flex', alignItems: 'center', justifyContent: 'center',
}
