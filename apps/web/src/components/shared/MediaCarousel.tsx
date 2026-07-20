'use client'

// 共用媒體元件：多圖輪播（MediaCarousel）、圖片燈箱（Lightbox）、YouTube 連結解析（ytId）+ 16:9 響應式嵌入（YouTubeEmbed）。
// 抽出自 BrochureScreen.tsx，行為保持不變（僅搬移，未重寫邏輯）。

import { useEffect, useRef, useState } from 'react'

// 從各種 YouTube 連結取出 video id
export function ytId(url: string): string | null {
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

// 16:9 響應式 YouTube 嵌入；一律用 youtube-nocookie.com 組 iframe。
// url 解析不出 id 時不渲染任何東西（不可把使用者輸入直接塞進 iframe src）。
export function YouTubeEmbed({ url, title }: { url: string; title?: string }) {
  const id = ytId(url)
  if (!id) return null
  return (
    <div style={{ position: 'relative', paddingBottom: '56.25%', height: 0, borderRadius: 12, overflow: 'hidden' }}>
      <iframe
        src={`https://www.youtube-nocookie.com/embed/${id}`}
        title={title ?? 'video'}
        allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture"
        allowFullScreen
        style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', border: 0 }}
      />
    </div>
  )
}

export function Lightbox({ images, index, onClose }: { images: string[]; index: number; onClose: () => void }) {
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

export function MediaCarousel({ images, onZoom }: { images: string[]; onZoom: (images: string[], index: number) => void }) {
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

const lightboxStyle: React.CSSProperties = {
  position: 'fixed', inset: 0, background: 'rgba(0,0,0,.92)', zIndex: 90,
  display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'zoom-out',
}
const closeBtn: React.CSSProperties = {
  position: 'absolute', top: 14, right: 14, zIndex: 2, width: 38, height: 38, borderRadius: 999,
  border: 'none', cursor: 'pointer', background: 'rgba(0,0,0,.5)', color: '#fff', fontSize: 18,
  display: 'flex', alignItems: 'center', justifyContent: 'center',
}
