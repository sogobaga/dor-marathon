'use client'

import { useState } from 'react'
import useSWR from 'swr'
import { createPortal } from 'react-dom'
import { monopolyApi, type StickerPiece } from '@/lib/api'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'
import { overlayMount } from '@/lib/overlayMount'

// 完賽公仔九宮格貼紙：擲骰停在機會/命運格有機會抽到公仔碎片，收集滿 9 片可拼出完整彩色公仔。
// 全螢幕覆蓋層作法比照 KnowledgeGalleryScreen（createPortal 到 body，不受呼叫端目前頁面版位影響）。
// 貼紙不做防劇透：未擁有的格子直接顯示灰階切片 gray_url，讓玩家知道缺哪片；已擁有的格子改用完整彩圖
// figure_url 裁出對應的 1/9（background-size 300% 300% + 依 position 換算 background-position），
// 9 格全部收集時剛好拼成一張完整公仔圖。
export default function FigureStickerScreen({ onClose }: { onClose: () => void }) {
  const user = useUser()
  const uid = user?.id ?? null
  const { data, error, mutate } = useSWR(
    uid && getUserToken() ? ['monopoly-stickers', uid] : null,
    () => withUserAuth((t) => monopolyApi.stickers(t)),
  )
  const [showRedeem, setShowRedeem] = useState(false)

  const complete = !!data && data.total > 0 && data.owned >= data.total

  async function handleRedeemFigure() {
    const res = await withUserAuth((t) => monopolyApi.redeemFigure(t))
    // 直接把新狀態寫回 SWR 快取（不重打 API），彈窗會因 data 變動同步更新按鈕文字
    mutate((prev) => (prev ? { ...prev, redemption_status: res.status as typeof prev.redemption_status } : prev), { revalidate: false })
  }

  // 掛載點：手機模擬框內→portal 進框(桌機不鋪滿視窗)；獨立路由(無手機框)→退回 document.body(視窗)
  const om = overlayMount()

  const body = (
    <div style={{ position: om.position, inset: 0, zIndex: 2000, background: 'var(--bg)', display: 'flex', flexDirection: 'column' }}>
      <header style={{ padding: 'var(--app-top) 22px 10px', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onClose} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>🧩 {data?.title ?? '公仔收集'}</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', padding: '10px 18px 32px' }}>
        {!user ? (
          <div style={{ color: 'var(--tx-dim)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>請先登入才能查看公仔收集</div>
        ) : error ? (
          <div style={{ color: 'var(--hunt)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>載入失敗，請稍後再試</div>
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

            <p style={{ fontSize: 11, color: 'var(--tx-faint)', margin: '12px 4px 0', lineHeight: 1.6, textAlign: 'center' }}>
              ※完賽公仔圖片顯示僅為產品範例參考，實際製作將以兌換時提供時的照片進行設計。
            </p>

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

            {/* 兌換公仔入口：未集滿時顯示暗色提示按鈕（不可點），集滿才亮起可點開兌換彈窗 */}
            <div style={{ marginTop: 20, textAlign: 'center' }}>
              <button
                onClick={() => complete && setShowRedeem(true)}
                disabled={!complete}
                style={complete ? redeemBtnActive : redeemBtnDisabled}
              >
                {complete ? '🎁 兌換公仔' : '集滿九宮格即可兌換'}
              </button>
            </div>
          </>
        )}
      </div>

      {showRedeem && data && (
        <FigureRedeemModal
          lineOA={data.line_oa}
          landingUrl={data.landing_url}
          redemptionStatus={data.redemption_status}
          onClose={() => setShowRedeem(false)}
          onRedeem={handleRedeemFigure}
        />
      )}
    </div>
  )

  return om.node ? createPortal(body, om.node) : body
}

// 兌換公仔彈窗：三顆直向按鈕——我要兌換／聯繫官方 LINE OA／完賽公仔。點外（overlay）關閉。
// redemptionStatus 由父層 SWR data 傳入，送出兌換申請後父層會更新快取，此處隨 props 同步刷新文字，
// 不另外持有一份狀態，避免兩處狀態不同步。
function FigureRedeemModal({
  lineOA, landingUrl, redemptionStatus, onClose, onRedeem,
}: {
  lineOA: string
  landingUrl: string
  redemptionStatus: string
  onClose: () => void
  onRedeem: () => Promise<void>
}) {
  const [busy, setBusy] = useState(false)

  async function handleRedeem() {
    if (redemptionStatus !== '' || busy) return
    setBusy(true)
    try {
      await onRedeem()
    } catch {
      // 送出失敗：保留原狀態（''），按鈕維持可點，玩家可重試
    }
    setBusy(false)
  }

  const label =
    redemptionStatus === 'pending' ? '兌換公仔申請中' :
    redemptionStatus === 'fulfilled' ? '已兌換' :
    redemptionStatus === 'rejected' ? '申請未通過，請聯繫官方' :
    busy ? '送出中…' : '我要兌換'
  const disabled = redemptionStatus !== '' || busy

  return (
    <div data-skin="default" onClick={onClose} style={modalOverlay}>
      <div onClick={(e) => e.stopPropagation()} style={modalCard}>
        <div style={{ fontSize: 18, fontWeight: 900, color: '#fff', textAlign: 'center' }}>兌換公仔</div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginTop: 16 }}>
          <button onClick={handleRedeem} disabled={disabled} style={{ ...modalBtnPrimary, opacity: disabled ? 0.6 : 1 }}>
            {label}
          </button>
          <button
            onClick={() => window.open(`https://line.me/R/ti/p/${encodeURIComponent(lineOA)}`, '_blank', 'noopener')}
            style={modalBtnGhost}
          >
            聯繫官方 LINE OA（{lineOA}）
          </button>
          <button onClick={() => window.open(landingUrl, '_blank', 'noopener')} style={modalBtnGhost}>
            完賽公仔
          </button>
        </div>
      </div>
    </div>
  )
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

// 兌換入口按鈕：集滿(complete)才亮起可點；未集滿顯示暗色提示，讓玩家知道目標。金底一律白字。
const redeemBtnActive: React.CSSProperties = {
  width: '100%', maxWidth: 300, background: 'var(--gold)', color: '#fff', fontWeight: 900,
  border: 'none', borderRadius: 12, padding: '13px 16px', fontSize: 14.5, cursor: 'pointer', fontFamily: 'inherit',
  boxShadow: '0 6px 18px rgba(231,184,75,.35)',
}
const redeemBtnDisabled: React.CSSProperties = {
  width: '100%', maxWidth: 300, background: 'var(--bg-2)', color: 'var(--tx-faint)', fontWeight: 700,
  border: '1px solid var(--line-2)', borderRadius: 12, padding: '13px 16px', fontSize: 13.5, cursor: 'not-allowed', fontFamily: 'inherit',
}

// 兌換彈窗樣式（比照 CancelSubscriptionModal 的 overlay/卡片慣例）；zIndex 須高於本頁 portal 的 2000
const modalOverlay: React.CSSProperties = { position: 'fixed', inset: 0, zIndex: 2600, background: 'rgba(4,8,6,.88)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 18 }
const modalCard: React.CSSProperties = { width: '100%', maxWidth: 340, background: '#0b0e13', border: '1px solid var(--line-2)', borderRadius: 16, padding: '18px 16px', boxShadow: '0 16px 50px rgba(0,0,0,.7)' }
const modalBtnPrimary: React.CSSProperties = { width: '100%', background: 'var(--gold)', color: '#fff', fontWeight: 900, border: 'none', borderRadius: 11, padding: '12px', fontSize: 14, cursor: 'pointer', fontFamily: 'inherit' }
const modalBtnGhost: React.CSSProperties = { width: '100%', background: 'transparent', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 11, padding: '12px', fontSize: 13.5, cursor: 'pointer', fontFamily: 'inherit' }
