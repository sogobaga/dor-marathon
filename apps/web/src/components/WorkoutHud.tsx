'use client'

import { type WoStep, fmtPaceS, paceBand, targetText } from '@/lib/workout'

// GPS 追蹤頁的「結構化課表」執行面板：目前分段目標 + 進度 + 即時配速回饋 + 全段進度點。
// 純顯示：即時數據由 /track 從 GPS 計算後傳入。
export default function WorkoutHud({ title, steps, stepIdx, stepDist, stepTime, livePaceS, hits, phase, result, onClose }: {
  title: string
  steps: WoStep[]
  stepIdx: number
  stepDist: number     // 目前分段已跑公尺
  stepTime: number     // 目前分段已過秒數
  livePaceS: number    // 即時配速（秒/公里）
  hits: Record<number, boolean> // 已評分的 work 段：index → 是否達配速
  phase: 'running' | 'done'
  result: { stars: number; reward_exp: number; reward_dp: number } | null
  onClose: () => void
}) {
  const step = steps[stepIdx]
  const workTotal = steps.filter((s) => s.graded).length
  const workDone = Object.keys(hits).length
  const workHit = Object.values(hits).filter(Boolean).length

  if (phase === 'done') {
    return (
      <div data-skin="default" style={wrap}>
        <div style={{ ...card, borderColor: 'var(--fug)' }}>
          <div style={{ fontSize: 11, letterSpacing: '.2em', color: 'var(--fug)', fontWeight: 800 }}>課表完成</div>
          <div style={{ fontSize: 16, fontWeight: 900, color: 'var(--tx)', margin: '4px 0 8px' }}>{title}</div>
          {result ? (
            <>
              <div style={{ fontSize: 30, letterSpacing: 3, color: 'var(--gold)', textAlign: 'center' }}>{'★'.repeat(result.stars)}{'☆'.repeat(3 - result.stars)}</div>
              <div style={{ textAlign: 'center', fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 4 }}>
                400m 配速達成 {workHit}/{workTotal}
                {(result.reward_exp > 0 || result.reward_dp > 0) && <>　·　<span style={{ color: 'var(--gold)', fontWeight: 800 }}>{result.reward_exp > 0 ? `+${result.reward_exp} EXP` : ''}{result.reward_exp > 0 && result.reward_dp > 0 ? ' ' : ''}{result.reward_dp > 0 ? `+${result.reward_dp} DP` : ''}</span></>}
              </div>
            </>
          ) : (
            <div style={{ textAlign: 'center', fontSize: 13, color: 'var(--tx-dim)' }}>結算中…</div>
          )}
          <button onClick={onClose} style={{ marginTop: 12, width: '100%', background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 10, padding: '10px', fontSize: 14, cursor: 'pointer' }}>看跑步結果</button>
        </div>
      </div>
    )
  }

  if (!step) return null
  const isTime = step.targetType === 'time'
  const cur = isTime ? stepTime : stepDist
  const pct = step.target > 0 ? Math.min(100, Math.round((cur / step.target) * 100)) : 0
  const remain = Math.max(0, step.target - cur)
  // 即時配速回饋（僅對有配速區間的段）
  let paceHint = '', paceColor = 'var(--tx-dim)'
  if (step.paceSlow && livePaceS > 0) {
    if (livePaceS > step.paceSlow) { paceHint = '再加速 ↑'; paceColor = 'var(--gold)' }
    else if (step.paceFast && livePaceS < step.paceFast) { paceHint = '稍放慢 ↓'; paceColor = '#5aa0ff' }
    else { paceHint = '配速剛好 ✓'; paceColor = 'var(--fug)' }
  }
  const accent = step.kind === 'work' ? 'var(--fug)' : step.kind === 'rest' ? 'var(--gold)' : 'var(--tx-dim)'

  return (
    <div data-skin="default" style={wrap}>
      <div style={card}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 8 }}>
          <span style={{ fontSize: 10.5, letterSpacing: '.15em', color: 'var(--tx-faint)', fontWeight: 800 }}>課表挑戰 · {title}</span>
          <span style={{ fontSize: 11, color: 'var(--tx-faint)', fontVariantNumeric: 'tabular-nums' }}>第 {stepIdx + 1}/{steps.length} 段</span>
        </div>
        {/* 目前分段 */}
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginTop: 6 }}>
          <span style={{ fontSize: 18, fontWeight: 900, color: accent }}>{step.label}</span>
          <span style={{ fontSize: 13, color: 'var(--tx-dim)', fontWeight: 700 }}>{targetText(step)}</span>
          {step.paceSlow ? <span style={{ fontSize: 12, color: 'var(--tx-faint)', marginLeft: 'auto' }}>目標 {paceBand(step.paceFast, step.paceSlow)}</span> : null}
        </div>
        {/* 分段進度 */}
        <div style={{ height: 9, background: 'rgba(255,255,255,.12)', borderRadius: 999, overflow: 'hidden', marginTop: 8 }}>
          <div style={{ height: '100%', width: `${pct}%`, background: accent, borderRadius: 999, transition: 'width .35s' }} />
        </div>
        <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11.5, marginTop: 5, fontVariantNumeric: 'tabular-nums' }}>
          <span style={{ color: 'var(--tx-dim)' }}>
            {isTime ? `剩 ${Math.ceil(remain)} 秒` : `${Math.round(cur)} / ${step.target} m`}
          </span>
          {step.kind !== 'rest' && <span style={{ color: paceColor, fontWeight: 800 }}>{fmtPaceS(livePaceS)}/km{paceHint ? ` · ${paceHint}` : ''}</span>}
        </div>
        {/* 全段進度點：work 已評分顯示達標(綠)/未達(紅)，其餘灰；目前段亮 */}
        <div style={{ display: 'flex', gap: 4, marginTop: 10, flexWrap: 'wrap' }}>
          {steps.map((s, i) => {
            let bg = 'rgba(255,255,255,.16)'
            if (i < stepIdx) bg = s.graded ? (hits[i] ? 'var(--fug)' : 'var(--hunt)') : 'rgba(255,255,255,.35)'
            else if (i === stepIdx) bg = accent
            return <span key={i} title={s.label} style={{ flex: s.kind === 'rest' ? '0 0 8px' : 1, height: 5, minWidth: 8, borderRadius: 999, background: bg, opacity: i === stepIdx ? 1 : 0.9 }} />
          })}
        </div>
        <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 6 }}>完成整份課表即結算；400m 段配速達成度決定星數（全達 3★／部分 2★／完成 1★）。</div>
      </div>
    </div>
  )
}

const wrap: React.CSSProperties = { position: 'absolute', left: 0, right: 0, top: 0, zIndex: 1100, padding: '10px 12px 0' }
const card: React.CSSProperties = { background: '#0b0e13', border: '1px solid var(--line-2)', borderRadius: 14, padding: '12px 14px', boxShadow: '0 8px 28px rgba(0,0,0,.5)', color: 'var(--tx)' }
