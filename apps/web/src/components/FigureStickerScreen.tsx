'use client'

import useSWR from 'swr'
import { createPortal } from 'react-dom'
import { monopolyApi, type StickerPiece } from '@/lib/api'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'

// 完賽公仔九宮格貼紙：擲骰停在機會/命運格有機會抽到公仔碎片，收集滿 9 片可拼出完整彩色公仔。
// 全螢幕覆蓋層作法比照 KnowledgeGalleryScreen（createPortal 到 body，不受呼叫端目前頁面版位影響）。
// 貼紙不做防劇透：未擁有的格子直接顯示灰階切片 gray_url，讓玩家知道缺哪片；已擁有的格子改用完整彩圖
// figure_url 裁出對應的 1/9（background-size 300% 300% + 依 position 換算 background-position），
// 9 格全部收集時剛好拼成一張完整公仔圖。
export default function FigureStickerScreen({ onClose }: { onClose: () => void }) {
  const user = useUser()
  const uid = user?.id ?? null
  const { data } = useSWR(
    uid && getUserToken() ? ['monopoly-stickers', uid] : null,
    () => withUserAuth((t) => monopolyApi.stickers(t)),
  )

  const complete = !!data && data.total > 0 && data.owned >= data.total

  const body = (
    <div style={{ position: 'fixed', inset: 0, zIndex: 2000, background: 'var(--bg)', display: 'flex', flexDirection: 'column' }}>
      <header style={{ padding: 'var(--app-top) 22px 10px', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onClose} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>🧩 {data?.title ?? '公仔收集'}</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', padding: '10px 18px 32px' }}>
        {!user ? (
          <div style={{ color: 'var(--tx-dim)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>請先登入才能查看公仔收集</div>
        ) : data === undefined ? (
          <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>載入中…</div>
        ) : (
          <>
            <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '2px 2px 12px', lineHeight: 1.7 }}>
              擲骰停在機會/命運格有機會抽到公仔碎片，集滿九宮格拼出完整公仔！
            </p>
            <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginBottom: 14 }}>
              已收集 <b style={{ color: 'var(--gold)', fontWeight: 800 }}>{data.owned}</b> / {data.total} 片
            </div>

            {complete && (
              <div style={{
                textAlign: 'center', marginBottom: 16, padding: '12px 10px', borderRadius: 12,
                background: 'linear-gradient(135deg, rgba(231,184,75,.2), rgba(231,184,75,0) 65%)',
                border: '1px solid var(--gold)',
              }}>
                <div style={{ fontSize: 14, fontWeight: 900, color: 'var(--gold)' }}>🎉 公仔收集完成！</div>
              </div>
            )}

            {/* 九宮格：3×3、gap 1px（用底色當格線），已收集的格子彼此相鄰時會拼成連續的彩圖 */}
            <div style={{
              width: '100%', maxWidth: 360, margin: '0 auto', aspectRatio: '1 / 1',
              display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gridTemplateRows: 'repeat(3, 1fr)', gap: 1,
              background: 'var(--line)', borderRadius: 12, overflow: 'hidden',
            }}>
              {data.pieces.map((p) => (
                <StickerCell key={p.position} piece={p} figureUrl={data.figure_url} />
              ))}
            </div>

            {complete && (
              <div style={{ marginTop: 20, textAlign: 'center' }}>
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img
                  src={data.figure_url}
                  alt={data.title}
                  style={{ width: '100%', maxWidth: 260, borderRadius: 16, display: 'inline-block', boxShadow: '0 8px 24px rgba(0,0,0,.35)' }}
                />
              </div>
            )}
          </>
        )}
      </div>
    </div>
  )

  if (typeof document === 'undefined') return null
  return createPortal(body, document.body)
}

// 單一九宮格格子。position 1..9 依 3×3 row-major 換算成 col/row(0..2)：
// col=(position-1)%3、row=Math.floor((position-1)/3)。已收集時用完整彩圖 figure_url 搭配
// backgroundSize:300% 300% + backgroundPosition:`${col*50}% ${row*50}%` 裁出對應的 1/9。
function StickerCell({ piece, figureUrl }: { piece: StickerPiece; figureUrl: string }) {
  const col = (piece.position - 1) % 3
  const row = Math.floor((piece.position - 1) / 3)

  return (
    <div style={{ position: 'relative', aspectRatio: '1 / 1', overflow: 'hidden', background: 'var(--bg-2)' }}>
      {piece.owned ? (
        <div
          style={{
            width: '100%', height: '100%',
            backgroundImage: `url(${figureUrl})`,
            backgroundSize: '300% 300%',
            backgroundPosition: `${col * 50}% ${row * 50}%`,
          }}
        />
      ) : (
        // 未收集：灰階切片 + 低透明度，暗示尚未收集（不遮蓋圖案本身，玩家仍看得出缺哪片）
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={piece.gray_url}
          alt=""
          style={{ width: '100%', height: '100%', objectFit: 'cover', display: 'block', opacity: .55 }}
        />
      )}
      {piece.owned && piece.rarity === 'rare' && (
        <span style={{ position: 'absolute', top: 3, left: 3, fontSize: 8.5, fontWeight: 900, color: '#fff', background: 'var(--gold)', borderRadius: 999, padding: '1px 5px' }}>★</span>
      )}
      {piece.owned && piece.obtained_count > 1 && (
        <span style={{ position: 'absolute', bottom: 3, right: 3, fontSize: 9, fontWeight: 900, color: '#fff', background: 'rgba(0,0,0,.55)', borderRadius: 999, padding: '1px 5px' }}>×{piece.obtained_count}</span>
      )}
    </div>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
