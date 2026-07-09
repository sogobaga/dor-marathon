'use client'

import { useState } from 'react'
import useSWR from 'swr'
import { exploreApi, type ExploreBoss } from '@/lib/api'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'

// 卡片圖鑑：收集到的關主卡片。9 張/頁，未收集顯示灰底「？」，右上顯示已收集數（不給總數，卡片持續擴充）。
const PER_PAGE = 9

export default function CardGalleryScreen({ onBack }: { onBack: () => void }) {
  const user = useUser()
  const uid = user?.id ?? null
  const { data } = useSWR(
    uid && getUserToken() ? ['explore-gallery', uid] : null,
    () => withUserAuth((t) => exploreApi.list(t)).then((r) => r.bosses),
  )
  const bosses = (data ?? null) as ExploreBoss[] | null
  const [page, setPage] = useState(0)
  const [zoom, setZoom] = useState<ExploreBoss | null>(null)

  const collected = bosses ? bosses.filter((b) => b.card_obtained).length : 0
  const pages = bosses ? Math.max(1, Math.ceil(bosses.length / PER_PAGE)) : 1
  const cur = page >= pages ? 0 : page
  const slots: (ExploreBoss | null)[] = bosses ? bosses.slice(cur * PER_PAGE, cur * PER_PAGE + PER_PAGE) : []
  while (slots.length < PER_PAGE) slots.push(null) // 補滿 3×3

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>卡片圖鑑</span>
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
              {slots.map((b, i) => (
                <div key={b?.id ?? `empty-${i}`} style={{ aspectRatio: '3 / 4', borderRadius: 10, overflow: 'hidden', position: 'relative', border: '1px solid var(--line)', background: 'var(--bg-2)' }}>
                  {b && b.card_obtained ? (
                    <button onClick={() => setZoom(b)} style={{ display: 'block', width: '100%', height: '100%', padding: 0, border: 'none', background: 'none', cursor: 'pointer' }}>
                      <img src={b.card_image_url || undefined} alt={b.name} style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
                    </button>
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
              ))}
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

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
const pageBtn: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 999, width: 34, height: 34, cursor: 'pointer', fontSize: 18, fontFamily: 'inherit' }
