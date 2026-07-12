'use client'

import { useEffect, useMemo, useRef, useState } from 'react'
import useSWR from 'swr'
import { exploreApi, type ExploreBoss } from '@/lib/api'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'

// 卡片圖鑑：收集到的關主卡片。9 張/頁，未收集顯示灰底「？」，右上顯示已收集數（不給總數，卡片持續擴充）。
// focusCardId：從關主挑戰完成導入（?unlock）→ 跳到該卡所在頁 + 播放翻轉解鎖 + 星星粒子特效。
const PER_PAGE = 9

export default function CardGalleryScreen({ onBack, focusCardId }: { onBack: () => void; focusCardId?: string }) {
  const user = useUser()
  const uid = user?.id ?? null
  const { data } = useSWR(
    uid && getUserToken() ? ['explore-gallery', uid] : null,
    () => withUserAuth((t) => exploreApi.list(t)).then((r) => r.bosses),
  )
  const bosses = (data ?? null) as ExploreBoss[] | null
  const [page, setPage] = useState(0)
  const [zoom, setZoom] = useState<ExploreBoss | null>(null)
  const [unlockingId, setUnlockingId] = useState<string | null>(null)
  const didFocus = useRef(false)

  // 導入時跳到該卡所在頁 + 觸發解鎖特效（僅一次）
  useEffect(() => {
    if (didFocus.current || !bosses || !focusCardId) return
    const idx = bosses.findIndex((b) => b.id === focusCardId && b.card_obtained)
    if (idx < 0) return
    didFocus.current = true
    setPage(Math.floor(idx / PER_PAGE))
    setUnlockingId(focusCardId)
    const t = setTimeout(() => setUnlockingId(null), 1700)
    return () => clearTimeout(t)
  }, [bosses, focusCardId])

  const collected = bosses ? bosses.filter((b) => b.card_obtained).length : 0
  const pages = bosses ? Math.max(1, Math.ceil(bosses.length / PER_PAGE)) : 1
  const cur = page >= pages ? 0 : page
  const slots: (ExploreBoss | null)[] = bosses ? bosses.slice(cur * PER_PAGE, cur * PER_PAGE + PER_PAGE) : []
  while (slots.length < PER_PAGE) slots.push(null) // 補滿 3×3

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <style>{`
        @keyframes cardReveal { 0%{transform:rotateY(180deg) scale(.82)} 55%{transform:rotateY(0deg) scale(1.12)} 100%{transform:rotateY(0deg) scale(1)} }
        @keyframes starFly { 0%{transform:translate(-50%,-50%) scale(.3);opacity:1} 100%{transform:translate(calc(-50% + var(--dx)),calc(-50% + var(--dy))) scale(1) rotate(var(--r));opacity:0} }
        @keyframes cardGlow { 0%{box-shadow:0 0 0 rgba(231,184,75,0)} 45%{box-shadow:0 0 24px 5px rgba(231,184,75,.75)} 100%{box-shadow:0 0 0 rgba(231,184,75,0)} }
      `}</style>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>卡片探索</span>
        <span style={{ marginLeft: 'auto', fontSize: 13, fontWeight: 800, color: 'var(--gold)' }}>已收集 {collected} 張</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '10px 18px 28px' }}>
        {bosses === null ? (
          <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>載入中…</div>
        ) : (
          <>
            <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '2px 2px 12px', lineHeight: 1.7 }}>
              到城市探索中進行打卡任務，可以收集意想不到的卡片唷～。
            </p>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 10 }}>
              {slots.map((b, i) => {
                const unlocking = !!b && b.id === unlockingId
                return (
                  <div
                    key={b?.id ?? `empty-${i}`}
                    ref={unlocking ? (el) => el?.scrollIntoView({ block: 'center', behavior: 'smooth' }) : undefined}
                    style={{ aspectRatio: '3 / 4', borderRadius: 10, position: 'relative', overflow: unlocking ? 'visible' : 'hidden', zIndex: unlocking ? 5 : undefined, border: '1px solid var(--line)', background: 'var(--bg-2)', animation: unlocking ? 'cardGlow 1.5s ease-out' : undefined }}
                  >
                    {b && b.card_obtained ? (
                      unlocking ? (
                        // 解鎖演出：翻牌（？→卡片）+ 星星粒子噴發
                        <>
                          <div style={{ perspective: 700, width: '100%', height: '100%', borderRadius: 10, overflow: 'hidden' }}>
                            <div style={{ position: 'relative', width: '100%', height: '100%', transformStyle: 'preserve-3d', animation: 'cardReveal .85s ease-out' }}>
                              <div style={{ position: 'absolute', inset: 0, backfaceVisibility: 'hidden', transform: 'rotateY(180deg)', display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'var(--bg-2)', fontSize: 34, fontWeight: 900, color: 'var(--tx-faint)' }}>？</div>
                              <div style={{ position: 'absolute', inset: 0, backfaceVisibility: 'hidden' }}>
                                <img src={b.card_image_url || undefined} alt={b.name} style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
                              </div>
                            </div>
                          </div>
                          <StarBurst />
                        </>
                      ) : (
                        <button onClick={() => setZoom(b)} style={{ display: 'block', width: '100%', height: '100%', padding: 0, border: 'none', background: 'none', cursor: 'pointer' }}>
                          <img src={b.card_image_url || undefined} alt={b.name} style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
                        </button>
                      )
                    ) : b ? (
                      // 未收集（但存在此關主）→ 灰底 ？
                      <div style={{ width: '100%', height: '100%', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 6, background: 'var(--bg-2)' }}>
                        <span style={{ fontSize: 34, fontWeight: 900, color: 'var(--tx-faint)' }}>？</span>
                        <span style={{ fontSize: 9.5, color: 'var(--tx-faint)', letterSpacing: 1 }}>{'★'.repeat(Math.max(0, b.difficulty_stars))}</span>
                      </div>
                    ) : (
                      // 空位（補滿 3×3）
                      <div style={{ width: '100%', height: '100%', background: 'repeating-linear-gradient(45deg, transparent, transparent 6px, rgba(127,127,127,.05) 6px, rgba(127,127,127,.05) 12px)' }} />
                    )}
                  </div>
                )
              })}
            </div>
            {pages > 1 && (
              <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', gap: 14, marginTop: 16 }}>
                <button onClick={() => setPage((p) => Math.max(0, p - 1))} disabled={cur === 0} style={{ ...pageBtn, opacity: cur === 0 ? 0.4 : 1 }}>‹</button>
                <span style={{ fontSize: 12.5, color: 'var(--tx-dim)', fontVariantNumeric: 'tabular-nums' }}>{cur + 1} / {pages}</span>
                <button onClick={() => setPage((p) => Math.min(pages - 1, p + 1))} disabled={cur >= pages - 1} style={{ ...pageBtn, opacity: cur >= pages - 1 ? 0.4 : 1 }}>›</button>
              </div>
            )}
          </>
        )}
      </div>

      {/* 卡片放大檢視 */}
      {zoom && (
        <div onClick={() => setZoom(null)} style={{ position: 'fixed', inset: 0, zIndex: 3300, background: 'rgba(0,0,0,.85)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', padding: 24, gap: 14 }}>
          <img src={zoom.card_image_url || undefined} alt={zoom.name} style={{ maxWidth: '100%', maxHeight: '82%', objectFit: 'contain', borderRadius: 12, boxShadow: '0 10px 40px rgba(0,0,0,.6)' }} />
          <div style={{ color: '#fff', fontSize: 14, fontWeight: 700 }}>{zoom.name} · {zoom.place}</div>
        </div>
      )}
    </div>
  )
}

// 星星粒子噴發（放射狀 20 顆，向外飛散淡出）。方向用 index 決定（無隨機，避免 SSR 不一致）。
function StarBurst() {
  const stars = useMemo(
    () => Array.from({ length: 20 }, (_, i) => {
      const ang = (Math.PI * 2 * i) / 20 + (i % 3) * 0.28
      const dist = 58 + (i % 5) * 15
      return { dx: Math.cos(ang) * dist, dy: Math.sin(ang) * dist, delay: (i % 4) * 0.05, size: 9 + (i % 3) * 5, rot: (i * 47) % 360 }
    }),
    [],
  )
  return (
    <div style={{ position: 'absolute', inset: 0, pointerEvents: 'none', zIndex: 3, overflow: 'visible' }}>
      {stars.map((s, i) => (
        <span
          key={i}
          style={{
            position: 'absolute', left: '50%', top: '50%', fontSize: s.size, lineHeight: 1,
            color: i % 2 ? 'var(--gold)' : '#fff', textShadow: '0 0 6px rgba(231,184,75,.9)',
            opacity: 0, animation: `starFly .95s ease-out ${s.delay}s forwards`,
            ['--dx' as string]: `${s.dx.toFixed(1)}px`, ['--dy' as string]: `${s.dy.toFixed(1)}px`, ['--r' as string]: `${s.rot}deg`,
          } as React.CSSProperties}
        >★</span>
      ))}
    </div>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
const pageBtn: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 999, width: 34, height: 34, cursor: 'pointer', fontSize: 18, fontFamily: 'inherit' }
