'use client'

// 共用媒體元件：多圖輪播（MediaCarousel）、圖片燈箱（Lightbox）、YouTube 連結解析（ytId）+ 16:9 響應式嵌入（YouTubeEmbed）、
// Facebook Reel 連結解析（fbReelHref）+ 9:16 直式響應式嵌入（FBReelEmbed）。
// 抽出自 BrochureScreen.tsx，行為保持不變（僅搬移，未重寫邏輯）。

import { useEffect, useRef, useState } from 'react'
import { useRouter } from 'next/navigation'
import { normalizeBrochureImage, type BrochureImageItem } from '@/lib/api'
import { navigateLink } from '@/lib/links'

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

// 從 Facebook Reel／短連結判斷是否為合法格式，回傳「標準化後的原始 https 連結」（供組 embed iframe
// href 參數用）；非白名單網域或路徑格式不符一律回傳 null。網域比對一律用 URL 解析後的 hostname 做
// 「結尾比對」（=== 或 .endsWith('.facebook.com')），不可用 includes/substring 字串比對（避免
// evil-facebook.com.attacker.tld 這類字串包含繞過白名單）。
// 支援格式（可帶 www./m. 子網域、結尾斜線、任意 query string）：
//   - https://www.facebook.com/reel/<id>
//   - https://www.facebook.com/share/r/<token>
//   - https://fb.watch/<token>
// 驗算範例：
//   fbReelHref('https://www.facebook.com/reel/1234567890')                        → 'https://www.facebook.com/reel/1234567890'
//   fbReelHref('https://m.facebook.com/share/r/AbC12_-/?mibextid=xyz')            → 'https://m.facebook.com/share/r/AbC12_-/?mibextid=xyz'
//   fbReelHref('https://fb.watch/AbC123-_/')                                      → 'https://fb.watch/AbC123-_/'
//   fbReelHref('fb.watch/AbC123')                                                 → 'https://fb.watch/AbC123'（無 scheme 自動補 https://）
//   fbReelHref('https://evil-facebook.com.attacker.tld/reel/1')                   → null（網域非結尾比對通過）
//   fbReelHref('https://www.facebook.com/watch/?v=123')                          → null（非 reel/share-r 路徑格式）
//   fbReelHref('not a url')                                                       → null
export function fbReelHref(url: string): string | null {
  const raw = url.trim()
  if (!raw) return null
  let u: URL
  try {
    // 容許使用者貼上時漏打 scheme（例如 www.facebook.com/reel/123）
    u = new URL(/^https?:\/\//i.test(raw) ? raw : `https://${raw}`)
  } catch {
    return null
  }
  const host = u.hostname.toLowerCase()
  const isFacebook = host === 'facebook.com' || host.endsWith('.facebook.com')
  const isFbWatch = host === 'fb.watch' || host.endsWith('.fb.watch')
  if (!isFacebook && !isFbWatch) return null
  if (isFacebook && !/^\/(reel\/[A-Za-z0-9._-]+|share\/r\/[A-Za-z0-9._-]+)\/?$/.test(u.pathname)) return null
  if (isFbWatch && !/^\/[A-Za-z0-9._-]+\/?$/.test(u.pathname)) return null
  return u.toString()
}

// 9:16 直式響應式 Facebook Reel 嵌入（官方免 SDK plugins/video.php），比照手機畫面置中、
// max-width 340px（不佔滿整頁）。url 解析不出合法 FB 連結時不渲染任何東西（不可把使用者輸入
// 直接塞進 iframe src；href query 一律放 fbReelHref() 解析後的標準化網址，不是原始輸入）。
export function FBReelEmbed({ url, title }: { url: string; title?: string }) {
  const href = fbReelHref(url)
  if (!href) return null
  return (
    <div style={{ maxWidth: 340, margin: '0 auto' }}>
      <div style={{ position: 'relative', aspectRatio: '9 / 16', borderRadius: 12, overflow: 'hidden' }}>
        <iframe
          src={`https://www.facebook.com/plugins/video.php?href=${encodeURIComponent(href)}&show_text=false`}
          title={title ?? 'video'}
          allow="autoplay; clipboard-write; encrypted-media; picture-in-picture; web-share"
          allowFullScreen
          loading="lazy"
          style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', border: 0 }}
        />
      </div>
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

// images：可為純網址字串陣列（舊呼叫端，如跑者充電站商家照片），或 BrochureImageItem
// 物件陣列（簡章圖片，各自可帶 caption/link）。點擊行為：有 link 的圖點擊導向連結
// （站內 push／站外開新分頁，見 lib/links）；無 link 則維持原本的放大(Lightbox)行為。
// caption 顯示在輪播下方，隨目前滑到第幾張切換。
export function MediaCarousel({
  images, onZoom,
}: {
  images: (string | BrochureImageItem)[]
  onZoom: (images: string[], index: number) => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [idx, setIdx] = useState(0)
  const router = useRouter()
  const items = images.map(normalizeBrochureImage)
  const urls = items.map((it) => it.url)

  function onScroll() {
    const el = ref.current
    if (!el) return
    const i = Math.round(el.scrollLeft / el.clientWidth)
    if (i !== idx) setIdx(i)
  }
  function go(i: number) {
    const el = ref.current
    if (!el) return
    const next = Math.max(0, Math.min(items.length - 1, i))
    el.scrollTo({ left: next * el.clientWidth, behavior: 'smooth' })
  }

  return (
    <div style={{ position: 'relative' }}>
      <div
        ref={ref}
        onScroll={onScroll}
        style={{
          display: 'flex', overflowX: 'auto', scrollSnapType: 'x mandatory', alignItems: 'center',
          borderRadius: 12, WebkitOverflowScrolling: 'touch', scrollbarWidth: 'none',
        }}
      >
        {items.map((item, i) => (
          // eslint-disable-next-line @next/next/no-img-element
          // 整張照片依顯示寬度縮放、不裁切不變形：height:auto + maxHeight 上限，objectFit 改 contain。
          // alignItems:'center'（見外層容器）避免 flex 預設 stretch 把矮的圖片撐成最高那張的高度。
          <img
            key={i} src={item.url} alt={item.caption ?? ''}
            onClick={() => (item.link ? navigateLink(item.link, router) : onZoom(urls, i))}
            style={{ flex: '0 0 100%', width: '100%', height: 'auto', maxHeight: '65vh', scrollSnapAlign: 'center', cursor: item.link ? 'pointer' : 'zoom-in', display: 'block', objectFit: 'contain' }}
          />
        ))}
      </div>

      {/* 左右箭頭引導 */}
      {idx > 0 && <button onClick={() => go(idx - 1)} style={{ ...arrowBtn, left: 8 }} aria-label="上一張">‹</button>}
      {idx < items.length - 1 && <button onClick={() => go(idx + 1)} style={{ ...arrowBtn, right: 8 }} aria-label="下一張">›</button>}

      {/* 計數徽章 */}
      <div style={{ position: 'absolute', top: 8, right: 10, background: 'rgba(0,0,0,.55)', color: '#fff', fontSize: 11, padding: '2px 8px', borderRadius: 999 }}>
        {idx + 1} / {items.length}
      </div>

      {/* 頁碼點 ○●○○○ */}
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', gap: 6, marginTop: 8 }}>
        {items.map((_, i) => (
          <button
            key={i} onClick={() => go(i)} aria-label={`第 ${i + 1} 張`}
            style={{
              width: i === idx ? 8 : 7, height: i === idx ? 8 : 7, borderRadius: 999, border: 'none', padding: 0, cursor: 'pointer',
              background: i === idx ? 'var(--tx)' : 'var(--line-2)', transition: 'background .2s',
            }}
          />
        ))}
      </div>
      {/* 當前圖片說明（隨滑動切換）*/}
      {items[idx]?.caption && (
        <div style={{ fontSize: 12, color: 'var(--tx-dim)', textAlign: 'center', marginTop: 6 }}>{items[idx].caption}</div>
      )}
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
