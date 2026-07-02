'use client'

import { useEffect, useRef, useState } from 'react'
import { pickTimeImage, type ActiveEvent } from './EventTaskModal'
import { playTapHit, playDefend, vibrate } from '@/lib/sfx'

// 互動小遊戲全屏層：tap_burst（連點攻擊）/ hold_press（按住防禦）。
// 依完成度計分，時間到（或按「略過」）回傳 { taps, held_ms } 給 /track 送後端分級發獎。
export function EventInteraction({ active, onDone, paused }: { active: ActiveEvent; onDone: (ev: { taps: number; held_ms: number }) => void; paused?: boolean }) {
  const def = active.def
  const p = def.completion_params
  const isTap = def.completion_type === 'tap_burst'
  const targetTaps = Math.max(1, Math.round(p.target_taps ?? 20))
  const needMs = Math.max(1, (p.hold_s ?? 5) * 1000)

  const [now, setNow] = useState(Date.now())
  const [taps, setTaps] = useState(0)
  const [heldMs, setHeldMs] = useState(0)
  const [holding, setHolding] = useState(false)
  const [fx, setFx] = useState<{ id: number; x: number; y: number; e: string }[]>([])

  const tapsRef = useRef(0)
  const heldRef = useRef(0)
  const holdStartRef = useRef<number | null>(null)
  const pointersRef = useRef<Set<number>>(new Set()) // 目前壓著的手指（多點觸控用）
  const fxId = useRef(0)
  const doneRef = useRef(false)
  const onDoneRef = useRef(onDone); onDoneRef.current = onDone
  const pausedRef = useRef(false); pausedRef.current = !!paused
  const pausedMsRef = useRef(0) // 橫向遮罩等暫停累計，據以延後時限
  const lastTickRef = useRef(Date.now())

  function finish() {
    if (doneRef.current) return
    doneRef.current = true
    if (holdStartRef.current != null) { heldRef.current += Date.now() - holdStartRef.current; holdStartRef.current = null }
    onDoneRef.current({ taps: tapsRef.current, held_ms: heldRef.current })
  }

  // 計時器只建一次（onDone 用 ref、時限用 active.deadline）
  useEffect(() => {
    const t = setInterval(() => {
      const n = Date.now()
      const dt = n - lastTickRef.current
      lastTickRef.current = n
      if (pausedRef.current) {
        pausedMsRef.current += dt
        if (holdStartRef.current != null) holdStartRef.current = n // 暫停時不累積按住
        setNow(n)
        return
      }
      if (holdStartRef.current != null) { heldRef.current += n - holdStartRef.current; holdStartRef.current = n; setHeldMs(heldRef.current) }
      setNow(n)
      if (n >= active.deadline + pausedMsRef.current) finish()
    }, 100)
    return () => clearInterval(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active.deadline])

  const effReady = active.readyUntil + pausedMsRef.current
  const effDeadline = active.deadline + pausedMsRef.current
  const ready = now < effReady
  const readyRemain = Math.max(0, Math.ceil((effReady - now) / 1000))
  const remain = Math.max(0, Math.ceil((effDeadline - now) / 1000))
  const progress = isTap ? Math.min(1, taps / targetTaps) : Math.min(1, heldMs / needMs)
  const pct = Math.round(progress * 100)
  const active2 = !ready && !paused && Date.now() < effDeadline // 可輸入中

  function onDown(e: React.PointerEvent) {
    e.preventDefault()
    try { (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId) } catch { /* 不支援就略過 */ }
    if (!active2) return
    if (isTap) {
      tapsRef.current += 1; setTaps(tapsRef.current)
      playTapHit(); vibrate(25)
      const rect = (e.currentTarget as HTMLElement).getBoundingClientRect()
      const x = e.clientX - rect.left, y = e.clientY - rect.top
      const id = fxId.current++
      const e2 = ['💥', '💧', '⭐', '🪨'][id % 4]
      setFx((f) => [...f.slice(-14), { id, x, y, e: e2 }])
      setTimeout(() => setFx((f) => f.filter((z) => z.id !== id)), 520)
    } else {
      pointersRef.current.add(e.pointerId)
      if (holdStartRef.current == null) { holdStartRef.current = Date.now(); setHolding(true); playDefend(); vibrate(35) }
    }
  }
  // 只有「最後一根手指」放開才停止累積（多點觸控不會誤停）
  function onUpHold(e: React.PointerEvent) {
    pointersRef.current.delete(e.pointerId)
    if (pointersRef.current.size === 0 && holdStartRef.current != null) {
      heldRef.current += Date.now() - holdStartRef.current; holdStartRef.current = null; setHeldMs(heldRef.current); setHolding(false)
    }
  }

  const accent = isTap ? '#FFC24B' : '#46E3A0'
  const img = pickTimeImage(def)

  return (
    <div
      onPointerDown={onDown}
      onPointerUp={isTap ? undefined : onUpHold}
      onPointerCancel={isTap ? undefined : onUpHold}
      style={{ position: 'fixed', inset: 0, zIndex: 2000, background: 'radial-gradient(circle at 50% 40%, rgba(20,26,34,.96), rgba(6,8,11,.98))', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'flex-start', padding: '20px 18px', touchAction: 'none', userSelect: 'none', WebkitUserSelect: 'none', WebkitTouchCallout: 'none', overflow: 'hidden' }}
    >
      {/* 略過（放棄任務、以目前完成度計）→ 讓跑者隨時可退出、回去按結束 */}
      <button
        onClick={finish}
        onPointerDown={(e) => e.stopPropagation()}
        style={{ position: 'absolute', top: 14, right: 14, zIndex: 2001, background: 'rgba(255,255,255,.1)', border: '1px solid var(--line-2)', color: 'var(--tx-dim)', borderRadius: 999, padding: '5px 12px', fontSize: 12.5, cursor: 'pointer' }}
      >✕ 略過</button>

      <div style={{ width: '100%', maxWidth: 420, display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', paddingRight: 70 }}>
        <span style={{ fontSize: 11, letterSpacing: '.2em', color: 'var(--gold)', fontWeight: 800 }}>⚡ 事件任務</span>
        <span style={{ fontSize: 22, fontWeight: 900, color: ready ? 'var(--fug)' : remain <= 3 ? 'var(--hunt)' : accent, fontVariantNumeric: 'tabular-nums' }}>{ready ? `準備 ${readyRemain}` : `${remain}s`}</span>
      </div>
      {img && <img src={img} alt="" style={{ width: '100%', maxWidth: 420, height: 120, objectFit: 'cover', borderRadius: 10, marginTop: 8 }} />}
      <div style={{ maxWidth: 420, textAlign: 'center', fontSize: 15, fontWeight: 700, color: 'var(--tx)', marginTop: 10, lineHeight: 1.5 }}>{def.message || def.name}</div>

      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 14, width: '100%' }}>
        {ready ? (
          <div style={{ fontSize: 26, fontWeight: 900, color: accent }}>{isTap ? '準備連續點擊！' : '準備按住防禦！'}</div>
        ) : (
          <>
            {/* 圈內只放圖示（手指按住時本來就會蓋住），數字資訊移到圈外 */}
            <div style={{ position: 'relative', width: 190, height: 190, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              <svg width="190" height="190" style={{ position: 'absolute', transform: 'rotate(-90deg)' }}>
                <circle cx="95" cy="95" r="84" fill="none" stroke="rgba(255,255,255,.1)" strokeWidth="10" />
                <circle cx="95" cy="95" r="84" fill="none" stroke={accent} strokeWidth="10" strokeLinecap="round"
                  strokeDasharray={2 * Math.PI * 84} strokeDashoffset={2 * Math.PI * 84 * (1 - progress)} style={{ transition: 'stroke-dashoffset .12s' }} />
              </svg>
              <div style={{ fontSize: 60, transform: holding ? 'scale(1.12)' : 'scale(1)', transition: 'transform .1s' }}>{isTap ? '👊' : holding ? '🛡️' : '✋'}</div>
            </div>
            {/* 圈外：大數字進度（不被手指遮住、也不會被選取） */}
            <div style={{ fontSize: 34, fontWeight: 900, color: accent, fontVariantNumeric: 'tabular-nums' }}>
              {isTap ? `${taps} / ${targetTaps} 次` : `${(heldMs / 1000).toFixed(1)} / ${(needMs / 1000).toFixed(0)} 秒`}
            </div>
            <div style={{ fontSize: 17, fontWeight: 800, color: 'var(--tx)' }}>{isTap ? '連續點擊！💥' : (holding ? '穩住！繼續按住 🛡️' : '按住螢幕不放！')}</div>
            <div style={{ fontSize: 12.5, color: 'var(--tx-faint)' }}>完成度 {pct}%（星等依完成度計）</div>
          </>
        )}
      </div>

      {fx.map((z) => (
        <span key={z.id} style={{ position: 'absolute', left: z.x, top: z.y, fontSize: 34, pointerEvents: 'none', transform: 'translate(-50%,-50%)', animation: 'fxPop .5s ease-out forwards' }}>{z.e}</span>
      ))}
      <style>{`@keyframes fxPop{0%{opacity:1;transform:translate(-50%,-50%) scale(.5)}100%{opacity:0;transform:translate(-50%,-140%) scale(1.6)}}`}</style>
    </div>
  )
}
