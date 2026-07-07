'use client'

import { useEffect, useRef, useState } from 'react'
import { type PanelCard } from '@/lib/api'
import { segSummary } from '@/lib/workout'

const stars3 = (n: number) => '★'.repeat(Math.max(0, n)) + '☆'.repeat(Math.max(0, 3 - n))

// GPS 追蹤頁的「個人任務資訊面板」：各階段(計畫)目前可挑戰的課表，左右滑動切換，下方 ●○○ 指示。
// 已承接(active)的課表卡顯示「已選」+「放棄」；同時只能一個進行中挑戰 → 其他卡挑戰鈕鎖住(須先放棄)，
// 但仍可左右滑動瀏覽。放棄若已花 DP 會提示「不退還」。重挑的過去任務卡由父層置頂於 cards[0]。
export default function TrackTaskPanel({ cards, activeTaskId, busy, onChallenge, onAbandon }: {
  cards: PanelCard[]
  activeTaskId: string | null   // 進行中挑戰的 task
  busy: string                  // 處理中的 task_id
  onChallenge: (c: PanelCard) => void
  onAbandon: (taskId: string) => void
}) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const [idx, setIdx] = useState(0)
  const [confirmAbandon, setConfirmAbandon] = useState(false)
  const scrolledRef = useRef<string | null>(null)
  const locked = !!activeTaskId

  // active 卡首次出現時捲到它一次（之後仍可自由左右滑動瀏覽其他階段）
  useEffect(() => {
    const el = scrollRef.current
    if (!el || !activeTaskId) { scrolledRef.current = null; return }
    if (scrolledRef.current === activeTaskId) return
    const i = cards.findIndex((c) => c.task_id === activeTaskId)
    if (i >= 0) { el.scrollTo({ left: i * el.clientWidth, behavior: 'smooth' }); setIdx(i); scrolledRef.current = activeTaskId }
  }, [activeTaskId, cards])
  useEffect(() => { setConfirmAbandon(false) }, [activeTaskId])

  const onScroll = () => { const el = scrollRef.current; if (el && el.clientWidth) setIdx(Math.round(el.scrollLeft / el.clientWidth)) }
  if (!cards.length) return null

  return (
    <div style={{ margin: '4px 0 12px' }}>
      <div style={{ fontSize: 11, color: 'var(--tx-faint)', fontWeight: 700, marginBottom: 7 }}>個人任務 · 課表挑戰{cards.length > 1 ? '（左右滑動切換階段）' : ''}</div>
      <div ref={scrollRef} onScroll={onScroll}
        style={{ display: 'flex', overflowX: 'auto', scrollSnapType: 'x mandatory', WebkitOverflowScrolling: 'touch', touchAction: 'pan-x', scrollbarWidth: 'none' }}>
        {cards.map((c) => {
          const isActive = c.task_id === activeTaskId
          const dpSpent = c.attempts >= 2 && c.retry_dp_cost > 0 // 這次承接是「付費重挑」
          return (
            <div key={c.task_id} style={{ flex: '0 0 100%', scrollSnapAlign: 'center', boxSizing: 'border-box', padding: '0 2px' }}>
              <div style={{ background: 'var(--bg-2)', border: `1px solid ${isActive ? 'var(--fug)' : 'var(--line-2)'}`, borderRadius: 14, padding: '12px 14px' }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 8 }}>
                  <span style={{ fontSize: 10.5, letterSpacing: '.12em', color: 'var(--fug)', fontWeight: 800, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>階段 {c.stage_order} · {c.plan_name}</span>
                  <span style={{ fontSize: 11, color: c.stars > 0 ? 'var(--gold)' : 'var(--tx-faint)', letterSpacing: 1, flexShrink: 0 }}>{stars3(c.stars)}</span>
                </div>
                <div style={{ fontSize: 15, fontWeight: 900, color: 'var(--tx)', marginTop: 3 }}>Day {c.day} · {c.title}</div>
                {segSummary(c.segments) && <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginTop: 5, lineHeight: 1.6 }}>📋 {segSummary(c.segments)}</div>}

                {isActive ? (
                  confirmAbandon ? (
                    <div style={{ marginTop: 10 }}>
                      <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', lineHeight: 1.6, marginBottom: 8 }}>
                        放棄將結束此挑戰、可改挑其他課表。
                        {dpSpent && <div style={{ color: 'var(--hunt)', fontWeight: 800, marginTop: 4 }}>已花費的 {c.retry_dp_cost} DP 不退還。</div>}
                      </div>
                      <div style={{ display: 'flex', gap: 8 }}>
                        <button onClick={() => onAbandon(c.task_id)} disabled={busy === c.task_id} style={{ ...solidBtn, background: 'var(--hunt)', color: '#fff', flex: 1, opacity: busy === c.task_id ? 0.6 : 1 }}>{busy === c.task_id ? '放棄中…' : '確定放棄'}</button>
                        <button onClick={() => setConfirmAbandon(false)} disabled={busy === c.task_id} style={ghostBtn}>取消</button>
                      </div>
                    </div>
                  ) : (
                    <div style={{ marginTop: 10 }}>
                      <div style={{ fontSize: 12.5, color: 'var(--fug)', fontWeight: 800, marginBottom: 8 }}>✓ 已選 · 按下方「開始課表挑戰」→ 321 倒數開始</div>
                      <button onClick={() => setConfirmAbandon(true)} style={{ ...outlineBtn }}>放棄挑戰</button>
                    </div>
                  )
                ) : (
                  <button onClick={() => onChallenge(c)} disabled={!!busy || locked}
                    style={{ ...solidBtn, marginTop: 10, width: '100%', opacity: (busy === c.task_id || locked) ? 0.5 : 1, cursor: locked ? 'not-allowed' : 'pointer' }}>
                    {busy === c.task_id ? '準備中…' : locked ? '挑戰中 · 請先放棄目前課表' : c.stars > 0 ? `再挑戰課表　·　DP ${c.retry_dp_cost}` : c.attempts > 0 ? `重新挑戰　·　DP ${c.retry_dp_cost}` : '▶ 挑戰此課表'}
                  </button>
                )}
              </div>
            </div>
          )
        })}
      </div>
      {/* ●○○ 指示 */}
      {cards.length > 1 && (
        <div style={{ display: 'flex', justifyContent: 'center', gap: 6, marginTop: 8 }}>
          {cards.map((c, i) => (
            <span key={c.task_id} style={{ width: i === idx ? 18 : 7, height: 7, borderRadius: 999, background: i === idx ? 'var(--fug)' : 'var(--line-2)', transition: 'width .2s, background .2s' }} />
          ))}
        </div>
      )}
    </div>
  )
}

const solidBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 10, padding: '10px', fontSize: 14, cursor: 'pointer', fontFamily: 'inherit' }
const outlineBtn: React.CSSProperties = { width: '100%', background: 'transparent', color: 'var(--hunt)', border: '1px solid var(--hunt)', borderRadius: 10, padding: '9px', fontSize: 13.5, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit' }
const ghostBtn: React.CSSProperties = { background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '9px 16px', fontSize: 13, cursor: 'pointer', fontFamily: 'inherit', flexShrink: 0 }
