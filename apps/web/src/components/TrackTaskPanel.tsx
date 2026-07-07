'use client'

import { useEffect, useRef, useState } from 'react'
import { type PanelCard } from '@/lib/api'
import { segSummary } from '@/lib/workout'

const stars3 = (n: number) => '★'.repeat(Math.max(0, n)) + '☆'.repeat(Math.max(0, 3 - n))

// GPS 追蹤頁的「個人任務資訊面板」：各階段(計畫)目前可挑戰的課表，左右滑動切換，下方 ●○○ 指示。
// 進行中挑戰時鎖定在該卡（不可切換），完成/放棄後才恢復滑動。重挑的過去任務卡由父層置頂於 cards[0]。
export default function TrackTaskPanel({ cards, activeTaskId, busy, onChallenge }: {
  cards: PanelCard[]
  activeTaskId: string | null   // 進行中挑戰的 task（鎖定、不可切換）
  busy: string                  // 處理中的 task_id
  onChallenge: (c: PanelCard) => void
}) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const [idx, setIdx] = useState(0)
  const locked = !!activeTaskId

  // 鎖定時捲到 active 卡並停在那
  useEffect(() => {
    const el = scrollRef.current
    if (!el || !locked) return
    const i = cards.findIndex((c) => c.task_id === activeTaskId)
    if (i >= 0) { el.scrollTo({ left: i * el.clientWidth, behavior: 'smooth' }); setIdx(i) }
  }, [locked, activeTaskId, cards])

  const onScroll = () => { const el = scrollRef.current; if (el && el.clientWidth) setIdx(Math.round(el.scrollLeft / el.clientWidth)) }
  if (!cards.length) return null

  return (
    <div style={{ margin: '4px 0 12px' }}>
      <div style={{ fontSize: 11, color: 'var(--tx-faint)', fontWeight: 700, marginBottom: 7 }}>個人任務 · 課表挑戰{cards.length > 1 ? '（左右滑動切換階段）' : ''}</div>
      <div ref={scrollRef} onScroll={onScroll}
        style={{ display: 'flex', overflowX: locked ? 'hidden' : 'auto', scrollSnapType: 'x mandatory', WebkitOverflowScrolling: 'touch', touchAction: locked ? 'none' : 'pan-x', scrollbarWidth: 'none' }}>
        {cards.map((c) => {
          const isActive = c.task_id === activeTaskId
          const dim = locked && !isActive
          return (
            <div key={c.task_id} style={{ flex: '0 0 100%', scrollSnapAlign: 'center', boxSizing: 'border-box', padding: '0 2px' }}>
              <div style={{ background: 'var(--bg-2)', border: `1px solid ${isActive ? 'var(--fug)' : 'var(--line-2)'}`, borderRadius: 14, padding: '12px 14px', opacity: dim ? 0.45 : 1, transition: 'opacity .2s' }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 8 }}>
                  <span style={{ fontSize: 10.5, letterSpacing: '.12em', color: 'var(--fug)', fontWeight: 800, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>階段 {c.stage_order} · {c.plan_name}</span>
                  <span style={{ fontSize: 11, color: c.stars > 0 ? 'var(--gold)' : 'var(--tx-faint)', letterSpacing: 1, flexShrink: 0 }}>{stars3(c.stars)}</span>
                </div>
                <div style={{ fontSize: 15, fontWeight: 900, color: 'var(--tx)', marginTop: 3 }}>Day {c.day} · {c.title}</div>
                {segSummary(c.segments) && <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginTop: 5, lineHeight: 1.6 }}>📋 {segSummary(c.segments)}</div>}
                {isActive ? (
                  <div style={{ marginTop: 10, fontSize: 12.5, color: 'var(--fug)', fontWeight: 800 }}>✓ 已選 · 按下方「開始課表挑戰」→ 321 倒數開始</div>
                ) : (
                  <button onClick={() => onChallenge(c)} disabled={!!busy || locked}
                    style={{ marginTop: 10, width: '100%', background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 10, padding: '10px', fontSize: 14, cursor: locked ? 'not-allowed' : 'pointer', opacity: (busy === c.task_id || locked) ? 0.55 : 1 }}>
                    {busy === c.task_id ? '準備中…' : locked ? '挑戰中無法切換' : c.stars > 0 ? `再挑戰課表　·　DP ${c.retry_dp_cost}` : c.attempts > 0 ? `重新挑戰　·　DP ${c.retry_dp_cost}` : '▶ 挑戰此課表'}
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
