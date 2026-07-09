'use client'

import { useEffect, useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import type { ExpBreakdown, ExpLevelRow } from '@/lib/api'
import * as sfx from '@/lib/sfx'
import DpCoin from './DpCoin'

type Step = {
  level: number
  title: string
  nextLevel: number
  nextTitle: string
  fromPct: number
  toPct: number
  willLevelUp: boolean
  maxed: boolean
}

// 依累積門檻把 expBefore→expAfter 拆成逐級的 bar 演出段
function buildSteps(before: number, after: number, levelsIn: ExpLevelRow[]): Step[] {
  const levels = [...levelsIn].sort((a, b) => a.exp_required - b.exp_required)
  if (levels.length === 0) return []
  const idxAt = (exp: number) => {
    let i = 0
    for (let k = 0; k < levels.length; k++) if (exp >= levels[k].exp_required) i = k
    return i
  }
  const steps: Step[] = []
  let cur = before
  let guard = 0
  while (cur < after && guard++ < 100) {
    const i = idxAt(cur)
    const floor = levels[i].exp_required
    const hasNext = i + 1 < levels.length
    if (!hasNext) {
      steps.push({ level: levels[i].level, title: levels[i].title, nextLevel: levels[i].level, nextTitle: '', fromPct: 1, toPct: 1, willLevelUp: false, maxed: true })
      break
    }
    const next = levels[i + 1].exp_required
    const span = Math.max(1, next - floor)
    const segEnd = Math.min(after, next)
    steps.push({
      level: levels[i].level,
      title: levels[i].title,
      nextLevel: levels[i + 1].level,
      nextTitle: levels[i + 1].title,
      fromPct: (cur - floor) / span,
      toPct: (segEnd - floor) / span,
      willLevelUp: segEnd >= next,
      maxed: false,
    })
    cur = segEnd
  }
  return steps
}

const easeOut = (t: number) => 1 - Math.pow(1 - t, 3)

export default function ExpSettlementModal({ breakdown, title = '成績結算', tagline = 'RESULT', subtitle, onClose }: { breakdown: ExpBreakdown; title?: string; tagline?: string; subtitle: string; onClose: () => void }) {
  const { gained, exp_before, exp_after, items, levels, dp_gained = 0, completion_pct } = breakdown
  const steps = useMemo(() => buildSteps(exp_before, exp_after, levels), [exp_before, exp_after, levels])
  // 完成度 → 星等（85~100→3、60~85→2、60 以下→1）。無 completion_pct（如里程彈窗）不顯示星星。
  const showStars = completion_pct != null
  const earnedStars = completion_pct == null ? 0 : completion_pct >= 85 ? 3 : completion_pct >= 60 ? 2 : 1

  const [phase, setPhase] = useState<'intro' | 'items' | 'total' | 'levels' | 'stars' | 'done'>('intro')
  const [revealed, setRevealed] = useState(0)
  const [total, setTotal] = useState(0)
  const [totalDp, setTotalDp] = useState(0)
  const [stepIdx, setStepIdx] = useState(0)
  const [barPct, setBarPct] = useState(steps[0]?.fromPct ?? 0)
  const [flash, setFlash] = useState(false)
  const [litStars, setLitStars] = useState(0)
  const [canClose, setCanClose] = useState(false)
  const [muted, setMuted] = useState(false)

  useEffect(() => {
    let cancelled = false
    const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms))
    const animate = (from: number, to: number, dur: number, cb: (v: number) => void) =>
      new Promise<void>((resolve) => {
        const start = performance.now()
        const tick = (now: number) => {
          if (cancelled) return resolve()
          const t = Math.min(1, (now - start) / dur)
          cb(from + (to - from) * easeOut(t))
          if (t < 1) requestAnimationFrame(tick)
          else resolve()
        }
        requestAnimationFrame(tick)
      })

    ;(async () => {
      setPhase('intro'); await sleep(750); if (cancelled) return
      setPhase('items')
      for (let i = 0; i < items.length; i++) { if (cancelled) return; setRevealed(i + 1); await sleep(300) }
      await sleep(280); if (cancelled) return
      setPhase('total')
      await animate(0, gained, Math.min(1700, 650 + gained * 4), (v) => setTotal(Math.round(v)))
      setTotal(gained); await sleep(250); if (cancelled) return
      if (dp_gained > 0) {
        await animate(0, dp_gained, Math.min(1400, 500 + dp_gained * 4), (v) => setTotalDp(Math.round(v)))
        setTotalDp(dp_gained); await sleep(300); if (cancelled) return
      }
      setPhase('levels')
      for (let si = 0; si < steps.length; si++) {
        if (cancelled) return
        setStepIdx(si); setBarPct(steps[si].fromPct); await sleep(60)
        const delta = steps[si].toPct - steps[si].fromPct
        // 連續升級時略加速，維持速度感；最後一段稍慢留住結果
        const accel = steps[si].willLevelUp ? Math.max(0.55, 1 - si * 0.06) : 1
        sfx.startFill() // bar 開始增加 → 上升音效
        await animate(steps[si].fromPct, steps[si].toPct, (230 + delta * 340) * accel, (v) => { setBarPct(v); sfx.updateFill(v) })
        setBarPct(steps[si].toPct)
        sfx.stopFill()
        if (steps[si].willLevelUp) { sfx.playDing(); setFlash(true); await sleep(Math.max(300, 400 - si * 12)); setFlash(false) } // 到 100% → 噹
      }
      if (cancelled) return
      // 星等演出：earned 顆星依序點亮（pop + 噹）
      if (showStars) {
        setPhase('stars'); await sleep(350)
        for (let s = 1; s <= earnedStars; s++) { if (cancelled) return; setLitStars(s); sfx.playDing(); await sleep(520) }
        await sleep(300); if (cancelled) return
      }
      setPhase('done'); setCanClose(true)
    })()
    return () => { cancelled = true; sfx.stopFill() }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => { sfx.unlockAudio() }, [])
  useEffect(() => { sfx.setMuted(muted) }, [muted])

  const step = steps[stepIdx]
  const finalLevel = levels.length ? [...levels].sort((a, b) => a.exp_required - b.exp_required).filter((l) => exp_after >= l.exp_required).pop() : undefined

  const content = (
    <div style={overlay} onPointerDown={() => sfx.unlockAudio()}>
      <style>{KEYFRAMES}</style>
      <div style={glow} />
      <button
        onClick={() => setMuted((m) => !m)}
        title={muted ? '開啟音效' : '靜音'}
        style={muteBtn}
      >{muted ? '🔇' : '🔊'}</button>

      <div style={panel}>
        {/* 副本完成橫幅 */}
        <div style={{ textAlign: 'center', animation: 'slamIn .6s cubic-bezier(.2,1.4,.5,1) both' }}>
          <div style={{ fontSize: 13, letterSpacing: '.4em', color: 'var(--gold)', fontWeight: 700 }}>{tagline}</div>
          <div style={{ fontSize: 34, fontWeight: 900, color: '#fff', textShadow: '0 0 24px rgba(229,196,107,.6)', margin: '2px 0 2px' }}>{title}</div>
          <div style={{ fontSize: 12, color: 'var(--tx-dim)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{subtitle}</div>
        </div>

        {/* 星等（依完成度）：預設三顆未亮，達成後依序點亮 */}
        {showStars && (
          <div style={{ textAlign: 'center', marginTop: 14 }}>
            <div style={{ display: 'flex', justifyContent: 'center', gap: 10 }}>
              {[0, 1, 2].map((i) => {
                const lit = i < litStars
                return (
                  <span key={i} style={{
                    fontSize: 44, lineHeight: 1, display: 'inline-block',
                    color: lit ? '#FFD24D' : 'rgba(255,255,255,.14)',
                    textShadow: lit ? '0 0 18px rgba(255,210,77,.8)' : 'none',
                    animation: lit ? 'starPop .5s cubic-bezier(.2,1.7,.4,1) both' : 'none',
                  }}>★</span>
                )
              })}
            </div>
            {(phase === 'stars' || phase === 'done') && (
              <div style={{ fontSize: 11, letterSpacing: '.15em', color: 'var(--tx-faint)', marginTop: 4 }}>完成度 {Math.round(completion_pct ?? 0)}%</div>
            )}
          </div>
        )}

        {/* 明細 */}
        <div style={{ marginTop: 18, display: 'flex', flexDirection: 'column', gap: 7, minHeight: 8 }}>
          {items.slice(0, revealed).map((it, i) => (
            <div key={i} style={{ ...rowItem, animation: 'itemIn .4s ease both' }}>
              <span style={{ fontSize: 13, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{it.label}</span>
              <span style={{ display: 'flex', alignItems: 'center', gap: 8, flexShrink: 0, fontVariantNumeric: 'tabular-nums' }}>
                <span style={{ fontSize: 14, fontWeight: 800, color: 'var(--fug)' }}>+{it.amount}</span>
                {it.dp ? <span style={{ display: 'inline-flex', alignItems: 'center', gap: 3, fontSize: 13, fontWeight: 800, color: 'var(--gold)' }}><DpCoin size={13} />+{it.dp}</span> : null}
              </span>
            </div>
          ))}
        </div>

        {/* 總 EXP 跳碼 */}
        {(phase === 'total' || phase === 'levels' || phase === 'done') && (
          <div style={{ textAlign: 'center', margin: '16px 0 4px' }}>
            <div style={{ fontSize: 11, letterSpacing: '.2em', color: 'var(--tx-faint)' }}>本場獲得經驗值</div>
            <div style={{ fontSize: 40, fontWeight: 900, color: 'var(--gold)', fontVariantNumeric: 'tabular-nums', textShadow: '0 0 20px rgba(229,196,107,.5)', lineHeight: 1.15 }}>
              +{total}<span style={{ fontSize: 18, marginLeft: 4 }}>EXP</span>
            </div>
            {dp_gained > 0 && (
              <div style={{ display: 'inline-flex', alignItems: 'center', gap: 6, marginTop: 6, fontSize: 22, fontWeight: 900, color: '#FFD24D', fontVariantNumeric: 'tabular-nums' }}>
                <DpCoin size={22} /> +{totalDp}<span style={{ fontSize: 13, marginLeft: 2 }}>DP</span>
              </div>
            )}
          </div>
        )}

        {/* 等級 + EXP bar */}
        {(phase === 'levels' || phase === 'done') && step && (
          <div style={{ marginTop: 10, position: 'relative' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', marginBottom: 6 }}>
              <span style={{ fontSize: 17, fontWeight: 900, color: 'var(--fug)', transition: 'transform .2s', display: 'inline-block', transform: flash ? 'scale(1.25)' : 'scale(1)' }}>
                Lv.{phase === 'done' ? (finalLevel?.level ?? step.level) : step.level}
                {phase === 'done' && finalLevel?.title ? <span style={{ fontSize: 12, color: 'var(--tx-dim)', marginLeft: 6 }}>{finalLevel.title}</span> : null}
              </span>
              <span style={{ fontSize: 12, color: 'var(--tx-dim)', fontVariantNumeric: 'tabular-nums' }}>
                {step.maxed ? 'MAX' : `${Math.round(barPct * 100)}%`}
              </span>
            </div>
            <div style={barOuter}>
              <div style={{ ...barInner, width: `${Math.max(0, Math.min(100, barPct * 100))}%` }}>
                <div style={shine} />
              </div>
            </div>
            {!step.maxed && (
              <div style={{ textAlign: 'right', fontSize: 11, color: 'var(--tx-faint)', marginTop: 4 }}>下一級 Lv.{step.nextLevel}</div>
            )}

            {/* LEVEL UP 爆點 */}
            {flash && (
              <div style={levelUpBurst}>
                <div style={{ fontSize: 24, fontWeight: 900, color: 'var(--gold)', textShadow: '0 0 24px rgba(229,196,107,.9)', animation: 'burst .56s ease both' }}>LEVEL&nbsp;UP!</div>
              </div>
            )}
          </div>
        )}

        {/* 關閉 */}
        <button
          onClick={canClose ? onClose : undefined}
          disabled={!canClose}
          style={{ ...closeBtn, opacity: canClose ? 1 : 0.35, cursor: canClose ? 'pointer' : 'default', animation: canClose ? 'pulse 1.6s ease-in-out infinite' : 'none' }}
        >
          {canClose ? '完成 ✓' : '結算中…'}
        </button>
      </div>
    </div>
  )
  // portal 到 body：跳出可拖曳面板等捲動容器/堆疊環境，確保結算演出永遠在最上層（不被面板蓋住）
  return typeof document === 'undefined' ? content : createPortal(content, document.body)
}

const overlay: React.CSSProperties = { position: 'fixed', inset: 0, zIndex: 3300, background: 'radial-gradient(120% 90% at 50% 30%, #11201b 0%, #070a09 70%)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 18, overflow: 'hidden' }
const glow: React.CSSProperties = { position: 'absolute', top: '18%', left: '50%', width: 420, height: 420, transform: 'translateX(-50%)', background: 'radial-gradient(circle, rgba(229,196,107,.16), transparent 60%)', pointerEvents: 'none', animation: 'glowPulse 3s ease-in-out infinite' }
const panel: React.CSSProperties = { position: 'relative', width: '100%', maxWidth: 380, background: 'rgba(10,14,12,.82)', border: '1px solid rgba(229,196,107,.35)', borderRadius: 18, padding: '24px 22px 20px', boxShadow: '0 20px 80px rgba(0,0,0,.6)', backdropFilter: 'blur(4px)' }
const rowItem: React.CSSProperties = { display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10, background: 'rgba(255,255,255,.04)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '8px 12px' }
const barOuter: React.CSSProperties = { height: 16, borderRadius: 999, background: 'rgba(255,255,255,.07)', border: '1px solid var(--line-2)', overflow: 'hidden', position: 'relative' }
const barInner: React.CSSProperties = { height: '100%', borderRadius: 999, background: 'linear-gradient(90deg,#2ad18f,#46E3A0,#9bffd2)', boxShadow: '0 0 16px rgba(70,227,160,.7)', position: 'relative', overflow: 'hidden' }
const shine: React.CSSProperties = { position: 'absolute', inset: 0, background: 'linear-gradient(90deg,transparent,rgba(255,255,255,.55),transparent)', animation: 'shine 1.1s linear infinite' }
const levelUpBurst: React.CSSProperties = { position: 'absolute', left: 0, right: 0, top: -2, display: 'flex', justifyContent: 'center', pointerEvents: 'none' }
const closeBtn: React.CSSProperties = { marginTop: 22, width: '100%', background: 'linear-gradient(135deg,#E5C46B,#caa64e)', color: '#fff', fontWeight: 800, border: 'none', borderRadius: 12, padding: '13px 20px', fontSize: 15 }
const muteBtn: React.CSSProperties = { position: 'absolute', top: 16, right: 18, zIndex: 2, background: 'rgba(255,255,255,.08)', border: '1px solid var(--line-2)', borderRadius: 999, width: 38, height: 38, fontSize: 16, cursor: 'pointer', color: '#fff' }

const KEYFRAMES = `
@keyframes slamIn { 0% { transform: scale(2.2); opacity: 0; filter: blur(6px) } 60% { opacity: 1 } 100% { transform: scale(1); opacity: 1; filter: blur(0) } }
@keyframes itemIn { 0% { transform: translateX(-14px); opacity: 0 } 100% { transform: translateX(0); opacity: 1 } }
@keyframes shine { 0% { transform: translateX(-120%) } 100% { transform: translateX(220%) } }
@keyframes glowPulse { 0%,100% { opacity: .65 } 50% { opacity: 1 } }
@keyframes burst { 0% { transform: scale(.4) translateY(6px); opacity: 0 } 40% { transform: scale(1.15) translateY(0); opacity: 1 } 100% { transform: scale(1) translateY(-10px); opacity: 0 } }
@keyframes pulse { 0%,100% { box-shadow: 0 0 0 0 rgba(229,196,107,.5) } 50% { box-shadow: 0 0 0 8px rgba(229,196,107,0) } }
@keyframes starPop { 0% { transform: scale(.2) rotate(-30deg); opacity: 0 } 55% { transform: scale(1.4) rotate(10deg); opacity: 1 } 100% { transform: scale(1) rotate(0); opacity: 1 } }
`
