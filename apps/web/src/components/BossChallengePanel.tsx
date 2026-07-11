'use client'

import { type ExploreBoss } from '@/lib/api'
import { segSummary, totalKm, estMinutes, fmtDuration } from '@/lib/workout'

// 關主挑戰面板（打卡後跳出）：比事件任務面板更精緻。兩階段——
// intro：Scene 圖 + 關主對話(dialogue_intro) + 挑戰資訊 + 接受(扣DP)/放棄；
// start：接受後關主對話(dialogue_start) + 「開始挑戰」→ 帶到 GPS 追蹤課表。
export default function BossChallengePanel({ boss, phase, busy, dpCost, note, onAccept, onDecline, onStart }: {
  boss: ExploreBoss
  phase: 'intro' | 'start'
  busy: boolean
  dpCost: number
  note?: string
  onAccept: () => void
  onDecline: () => void
  onStart: () => void
}) {
  const dialogue = phase === 'intro' ? boss.dialogue_intro : boss.dialogue_start
  return (
    <div data-skin="default" style={{ position: 'fixed', inset: 0, zIndex: 3200, background: 'rgba(4,8,6,.82)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 18 }}>
      <div style={{ width: '100%', maxWidth: 380, maxHeight: '92dvh', overflowY: 'auto', WebkitOverflowScrolling: 'touch', background: '#0b0e13', border: '1px solid var(--gold)', borderRadius: 18, boxShadow: '0 16px 50px rgba(0,0,0,.7)' }}>
        {/* Scene 圖 */}
        {boss.scene_image_url && (
          <div style={{ position: 'relative' }}>
            <img src={boss.scene_image_url} alt={boss.name} style={{ width: '100%', aspectRatio: '16 / 9', objectFit: 'cover', display: 'block', borderRadius: '18px 18px 0 0' }} />
            <div style={{ position: 'absolute', left: 0, right: 0, bottom: 0, padding: '24px 16px 10px', background: 'linear-gradient(transparent, rgba(4,8,6,.92))' }}>
              <div style={{ fontSize: 11, letterSpacing: '.25em', color: 'var(--gold)', fontWeight: 800 }}>⚔️ 關主挑戰</div>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginTop: 2 }}>
                <span style={{ fontSize: 20, fontWeight: 900, color: '#fff' }}>{boss.name}</span>
                <span style={{ fontSize: 12, color: 'var(--gold)', letterSpacing: 1 }}>{'★'.repeat(Math.max(0, boss.difficulty_stars))}</span>
              </div>
              {boss.title && <div style={{ fontSize: 12, color: 'rgba(255,255,255,.75)', marginTop: 1 }}>{boss.title}</div>}
            </div>
          </div>
        )}

        <div style={{ padding: '14px 16px 16px' }}>
          {/* 關主對話 */}
          {dialogue && (
            <div style={{ background: 'rgba(255,255,255,.05)', border: '1px solid var(--line-2)', borderRadius: 12, padding: '11px 13px', fontSize: 14, lineHeight: 1.7, color: 'var(--tx)' }}>
              <span style={{ color: 'var(--gold)', fontWeight: 800 }}>{boss.name}：</span>
              「{dialogue.split(/<br\s*\/?>/i).map((ln, i) => <span key={i}>{i > 0 && <br />}{ln}</span>)}」
            </div>
          )}

          {phase === 'intro' ? (
            <>
              {/* 挑戰資訊 */}
              <div style={{ marginTop: 12 }}>
                <div style={{ fontSize: 10.5, letterSpacing: '.2em', color: 'var(--tx-faint)', fontWeight: 800 }}>挑戰目標</div>
                {segSummary(boss.segments) && <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 5, lineHeight: 1.6 }}>📋 {segSummary(boss.segments)}</div>}
                <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 5 }}>總距離 {totalKm(boss.segments)} K · 預估 {fmtDuration(estMinutes(boss.segments))} · 3★ 完成即可收服，取得關主卡片</div>
              </div>
              {boss.access_note && (
                <div style={{ marginTop: 10, fontSize: 12, color: 'var(--tx-dim)', background: 'rgba(255,255,255,.04)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '8px 11px', lineHeight: 1.6 }}>
                  📍 開放資訊：{boss.access_note}
                </div>
              )}
              {note && <div style={{ marginTop: 12, fontSize: 12.5, color: '#ffcf6b', background: 'rgba(231,184,75,.12)', border: '1px solid rgba(231,184,75,.35)', borderRadius: 10, padding: '8px 11px', lineHeight: 1.5 }}>{note}</div>}
              <div style={{ display: 'flex', gap: 10, marginTop: 16 }}>
                <button onClick={onDecline} disabled={busy} style={declineBtn}>放棄</button>
                <button onClick={onAccept} disabled={busy || !!note} style={{ ...acceptBtn, opacity: (busy || note) ? 0.5 : 1, cursor: (busy || note) ? 'default' : 'pointer' }}>{busy ? '處理中…' : `接受挑戰 · 扣 ${dpCost} DP`}</button>
              </div>
            </>
          ) : (
            <>
              {note && <div style={{ marginTop: 12, fontSize: 12.5, color: '#ffcf6b', background: 'rgba(231,184,75,.12)', border: '1px solid rgba(231,184,75,.35)', borderRadius: 10, padding: '8px 11px', lineHeight: 1.5 }}>{note}</div>}
              <div style={{ display: 'flex', gap: 10, marginTop: 16 }}>
                <button onClick={onDecline} disabled={busy} style={declineBtn}>稍後</button>
                <button onClick={onStart} disabled={busy || !!note} style={{ ...acceptBtn, opacity: (busy || note) ? 0.5 : 1, cursor: (busy || note) ? 'default' : 'pointer' }}>▶ 開始挑戰</button>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}

const acceptBtn: React.CSSProperties = { flex: 1, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 900, border: 'none', borderRadius: 12, padding: '12px', fontSize: 14.5, cursor: 'pointer', fontFamily: 'inherit' }
const declineBtn: React.CSSProperties = { background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 12, padding: '12px 18px', fontSize: 13.5, cursor: 'pointer', fontFamily: 'inherit', flexShrink: 0 }
