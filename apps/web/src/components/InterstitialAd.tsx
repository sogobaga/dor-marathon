'use client'

import { useEffect, useRef, useState } from 'react'
import { usePathname, useRouter } from 'next/navigation'
import { interstitialApi, type InterstitialAd as Ad } from '@/lib/api'
import { INTERSTITIAL_OFF_KEY, INTERSTITIAL_SEEN_KEY, localDayKey } from '@/lib/interstitial'

// 蓋板廣告：拍立得卡片堆疊。前台開啟時彈一次；左右滑動換下一張、滑完自動關閉；右上 X；dots；本日不再顯示。
export default function InterstitialAd() {
  const pathname = usePathname()
  const router = useRouter()
  const [ads, setAds] = useState<Ad[]>([])
  const [open, setOpen] = useState(false)
  const [index, setIndex] = useState(0)
  const [dontShow, setDontShow] = useState(false)
  const [dragX, setDragX] = useState(0)
  const [fly, setFly] = useState<0 | 1 | -1>(0)
  const startXRef = useRef<number | null>(null)
  const draggingRef = useRef(false)
  const flyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const indexRef = useRef(0); indexRef.current = index
  const dontShowRef = useRef(false); dontShowRef.current = dontShow

  useEffect(() => {
    if (pathname?.startsWith('/admin')) return
    try {
      if (localStorage.getItem(INTERSTITIAL_OFF_KEY) === localDayKey()) return // 本日已勾不再顯示
      if (sessionStorage.getItem(INTERSTITIAL_SEEN_KEY) === '1') return          // 本次工作階段已查過
    } catch { /* ignore */ }
    let alive = true
    interstitialApi.get().then((r) => {
      if (!alive) return
      try { sessionStorage.setItem(INTERSTITIAL_SEEN_KEY, '1') } catch { /* ignore */ } // 查過就標記，避免每次導航重打（含無廣告時）
      const list = (r.ads || []).filter((a) => a.image_url)
      if (!list.length) return
      setAds(list); setOpen(true)
    }).catch(() => { /* 取不到就不彈；下次導航再試 */ })
    return () => { alive = false }
  }, [pathname])

  useEffect(() => () => { if (flyTimerRef.current) clearTimeout(flyTimerRef.current) }, [])

  if (!open || !ads.length) return null

  const close = () => {
    if (flyTimerRef.current) clearTimeout(flyTimerRef.current)
    if (dontShowRef.current) { try { localStorage.setItem(INTERSTITIAL_OFF_KEY, localDayKey()) } catch { /* ignore */ } }
    setOpen(false)
  }
  const jump = (i: number) => { if (flyTimerRef.current) clearTimeout(flyTimerRef.current); setFly(0); setDragX(0); setIndex(i) }
  const advance = (dir: 1 | -1) => {
    if (fly) return
    setFly(dir)
    if (flyTimerRef.current) clearTimeout(flyTimerRef.current)
    flyTimerRef.current = setTimeout(() => {
      setFly(0); setDragX(0)
      const cur = indexRef.current // 讀最新 index，避免 stale closure / dots 跳頁後溢位
      if (cur >= ads.length - 1) close()
      else setIndex(cur + 1)
    }, 260)
  }
  const onDown = (e: React.PointerEvent) => { if (fly) return; startXRef.current = e.clientX; draggingRef.current = true }
  const onMove = (e: React.PointerEvent) => { if (!draggingRef.current || startXRef.current == null) return; setDragX(e.clientX - startXRef.current) }
  const onUp = () => {
    if (!draggingRef.current) return
    draggingRef.current = false; startXRef.current = null
    if (Math.abs(dragX) > 72) advance(dragX > 0 ? 1 : -1)
    else setDragX(0)
  }
  const handleCTA = (url: string) => {
    close()
    if (!url) return
    if (/^https?:\/\//i.test(url)) window.open(url, '_blank', 'noopener')
    else router.push(url)
  }

  const stack = ads.slice(index, index + 3) // 顯示 index..index+2（最多 3 張）

  return (
    <div style={overlay} onPointerMove={onMove} onPointerUp={onUp} onPointerCancel={onUp}>
      <button onClick={close} aria-label="關閉" style={closeBtn}>✕</button>

      <div style={{ position: 'relative', width: 'min(78vw, 300px)', aspectRatio: '3 / 4.5' }}>
        {stack.map((a, pos) => {
          const isTop = pos === 0
          const back = [{ r: 0, tx: 0, ty: 0, s: 1 }, { r: 3.5, tx: 13, ty: 10, s: 0.965 }, { r: -3.5, tx: -13, ty: 18, s: 0.93 }][pos]
          let transform: string, transition = 'transform .28s cubic-bezier(.2,.7,.2,1)'
          if (isTop && fly !== 0) transform = `translateX(${fly * 130}%) rotate(${fly * 16}deg)`
          else if (isTop) { transform = `translateX(${dragX}px) rotate(${dragX * 0.04}deg)`; if (draggingRef.current) transition = 'none' }
          else transform = `translate(${back.tx}px, ${back.ty}px) rotate(${back.r}deg) scale(${back.s})`
          return (
            <div key={a.id || pos} style={{ ...cardWrap, zIndex: 10 - pos, transform, transition }} onPointerDown={isTop ? onDown : undefined}>
              <div style={polaroid}>
                <div style={{ ...img, backgroundImage: `url("${a.image_url}")` }} draggable={false} />
                <div style={caption}>
                  {a.headline && <div style={{ fontSize: 16, fontWeight: 900, color: '#2b2b2b', lineHeight: 1.35 }}>{a.headline}</div>}
                  {a.description && <div style={{ fontSize: 12, color: '#7a7a7a', marginTop: 3 }}>{a.description}</div>}
                  {a.cta_label && isTop && (
                    <button onClick={() => handleCTA(a.cta_url)} onPointerDown={(e) => e.stopPropagation()} style={ctaBtn}>{a.cta_label} →</button>
                  )}
                </div>
              </div>
            </div>
          )
        })}
      </div>

      {ads.length > 1 && (
        <div style={{ display: 'flex', gap: 7, marginTop: 18 }}>
          {ads.map((_, i) => (
            <button key={i} onClick={() => jump(i)} aria-label={`第 ${i + 1} 張`}
              style={{ width: i === index ? 22 : 8, height: 8, borderRadius: 999, border: 'none', cursor: 'pointer', background: i === index ? 'rgba(255,255,255,.92)' : 'rgba(255,255,255,.4)', transition: 'all .2s', padding: 0 }} />
          ))}
        </div>
      )}

      <div style={{ marginTop: 14, fontSize: 12, color: 'rgba(255,255,255,.6)' }}>
        {ads.length > 1 ? '左右滑動看下一張 · 滑完自動關閉' : '點右上角 ✕ 關閉'}
      </div>
      <label style={{ marginTop: 10, display: 'flex', alignItems: 'center', gap: 7, fontSize: 12.5, color: 'rgba(255,255,255,.78)', cursor: 'pointer' }}>
        <input type="checkbox" checked={dontShow} onChange={(e) => setDontShow(e.target.checked)} style={{ width: 16, height: 16 }} />
        本日不再顯示
      </label>
    </div>
  )
}

const overlay: React.CSSProperties = { position: 'fixed', inset: 0, zIndex: 2500, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', background: 'rgba(10,12,16,.72)', backdropFilter: 'blur(2px)', padding: 24, touchAction: 'none', userSelect: 'none', WebkitUserSelect: 'none' }
const closeBtn: React.CSSProperties = { position: 'absolute', top: 'calc(env(safe-area-inset-top, 0px) + 14px)', right: 16, width: 38, height: 38, borderRadius: 999, border: '1px solid rgba(255,255,255,.28)', background: 'rgba(0,0,0,.35)', color: '#fff', fontSize: 16, cursor: 'pointer', zIndex: 20 }
const cardWrap: React.CSSProperties = { position: 'absolute', inset: 0, willChange: 'transform' }
const polaroid: React.CSSProperties = { width: '100%', height: '100%', background: '#fff', borderRadius: 14, padding: '12px 12px 0', boxShadow: '0 18px 50px rgba(0,0,0,.45)', display: 'flex', flexDirection: 'column' }
const img: React.CSSProperties = { width: '100%', flex: 1, borderRadius: 7, backgroundColor: '#000', backgroundSize: 'cover', backgroundPosition: 'center', minHeight: 0 }
const caption: React.CSSProperties = { padding: '14px 8px 18px', textAlign: 'center', flexShrink: 0 }
const ctaBtn: React.CSSProperties = { marginTop: 8, background: 'none', border: 'none', color: '#3f6fb0', fontWeight: 800, fontSize: 13.5, cursor: 'pointer', fontFamily: 'inherit' }
