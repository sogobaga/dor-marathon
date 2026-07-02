'use client'

import { useEffect, useRef, useState } from 'react'
import { pickTimeImage, type ActiveEvent } from './EventTaskModal'
import { playTapHit, playDefend, vibrate } from '@/lib/sfx'

// 互動小遊戲全屏層：點擊攻擊 / 按住防禦 / 滑動蓄力(魔法) / 滑動閃避。
// 依完成度計分，時間到（或按「放棄」）回傳 evidence 給 /track 送後端分級發獎。
type Ev = { taps: number; held_ms: number; swipe_px: number; swipes: number }

export function EventInteraction({ active, onDone, paused, assets }: { active: ActiveEvent; onDone: (ev: Ev) => void; paused?: boolean; assets?: Record<string, string> }) {
  const def = active.def
  const p = def.completion_params
  const ct = def.completion_type
  const isTap = ct === 'tap_burst'
  const isHold = ct === 'hold_press'
  const isCharge = ct === 'swipe_charge'
  const isDodge = ct === 'dodge_swipe'
  const isSwipe = isCharge || isDodge
  const targetTaps = Math.max(1, Math.round(p.target_taps ?? 20))
  const needMs = Math.max(1, (p.hold_s ?? 5) * 1000)
  const targetPx = Math.max(1, Math.round(p.target_px ?? 4000))
  const targetSwipes = Math.max(1, Math.round(p.target_swipes ?? 5))

  const [now, setNow] = useState(Date.now())
  const [taps, setTaps] = useState(0)
  const [heldMs, setHeldMs] = useState(0)
  const [holding, setHolding] = useState(false)
  const [travel, setTravel] = useState(0)
  const [swipes, setSwipes] = useState(0)
  const [fx, setFx] = useState<{ id: number; x: number; y: number; e: string }[]>([])
  const [trail, setTrail] = useState<{ id: number; x: number; y: number }[]>([])

  const tapsRef = useRef(0)
  const heldRef = useRef(0)
  const holdStartRef = useRef<number | null>(null)
  const pointersRef = useRef<Set<number>>(new Set())
  const travelRef = useRef(0)
  const swipesRef = useRef(0)
  const strokeDistRef = useRef(0)
  const lastPtRef = useRef<{ x: number; y: number } | null>(null)
  const lastTrailRef = useRef(0)
  const fxId = useRef(0)
  const doneRef = useRef(false)
  const onDoneRef = useRef(onDone); onDoneRef.current = onDone
  const pausedRef = useRef(false); pausedRef.current = !!paused
  const pausedMsRef = useRef(0)
  const skipMsRef = useRef(0)
  const lastTickRef = useRef(Date.now())

  function finish() {
    if (doneRef.current) return
    doneRef.current = true
    if (holdStartRef.current != null) { heldRef.current += Date.now() - holdStartRef.current; holdStartRef.current = null }
    onDoneRef.current({ taps: tapsRef.current, held_ms: heldRef.current, swipe_px: travelRef.current, swipes: swipesRef.current })
  }
  function skipPrep() {
    const n = Date.now()
    const curEffReady = active.readyUntil + pausedMsRef.current - skipMsRef.current
    if (n < curEffReady) { skipMsRef.current += curEffReady - n; setNow(Date.now()) }
  }

  useEffect(() => {
    const t = setInterval(() => {
      const n = Date.now()
      const dt = n - lastTickRef.current
      lastTickRef.current = n
      if (pausedRef.current) {
        pausedMsRef.current += dt
        if (holdStartRef.current != null) holdStartRef.current = n
        setNow(n)
        return
      }
      if (holdStartRef.current != null) { heldRef.current += n - holdStartRef.current; holdStartRef.current = n; setHeldMs(heldRef.current) }
      setNow(n)
      if (n >= active.deadline + pausedMsRef.current - skipMsRef.current) finish()
    }, 100)
    return () => clearInterval(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active.deadline])

  const effReady = active.readyUntil + pausedMsRef.current - skipMsRef.current
  const effDeadline = active.deadline + pausedMsRef.current - skipMsRef.current
  const ready = now < effReady
  const readyRemain = Math.max(0, Math.ceil((effReady - now) / 1000))
  const remain = Math.max(0, Math.ceil((effDeadline - now) / 1000))
  const progress = Math.min(1, isTap ? taps / targetTaps : isHold ? heldMs / needMs : isCharge ? travel / targetPx : swipes / targetSwipes)
  const pct = Math.round(progress * 100)
  const active2 = !ready && !paused && Date.now() < effDeadline

  function ptOf(e: React.PointerEvent) {
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect()
    return { x: e.clientX - rect.left, y: e.clientY - rect.top }
  }
  function onDown(e: React.PointerEvent) {
    e.preventDefault()
    try { (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId) } catch { /* 略過 */ }
    if (!active2) return
    if (isTap) {
      tapsRef.current += 1; setTaps(tapsRef.current)
      playTapHit(); vibrate(25)
      const { x, y } = ptOf(e)
      const id = fxId.current++
      setFx((f) => [...f.slice(-14), { id, x, y, e: ['💥', '💧', '⭐', '🪨'][id % 4] }])
      setTimeout(() => setFx((f) => f.filter((z) => z.id !== id)), 520)
    } else if (isSwipe) {
      lastPtRef.current = ptOf(e); strokeDistRef.current = 0
    } else {
      pointersRef.current.add(e.pointerId)
      if (holdStartRef.current == null) { holdStartRef.current = Date.now(); setHolding(true); playDefend(); vibrate(35) }
    }
  }
  function onMove(e: React.PointerEvent) {
    if (!isSwipe || !active2) return
    const { x, y } = ptOf(e)
    const last = lastPtRef.current
    if (last) {
      const d = Math.hypot(x - last.x, y - last.y)
      travelRef.current += d; strokeDistRef.current += d; setTravel(travelRef.current)
      const t = Date.now()
      if (t - lastTrailRef.current > 24) { // 節流拋出拖尾
        lastTrailRef.current = t
        const id = fxId.current++
        setTrail((tr) => [...tr.slice(-15), { id, x, y }])
        setTimeout(() => setTrail((tr) => tr.filter((z) => z.id !== id)), 340)
      }
    }
    lastPtRef.current = { x, y }
  }
  function onUpHold(e: React.PointerEvent) {
    pointersRef.current.delete(e.pointerId)
    if (pointersRef.current.size === 0 && holdStartRef.current != null) {
      heldRef.current += Date.now() - holdStartRef.current; holdStartRef.current = null; setHeldMs(heldRef.current); setHolding(false)
    }
  }
  function onUpSwipe() {
    if (isDodge && strokeDistRef.current > 45) { swipesRef.current += 1; setSwipes(swipesRef.current); playDefend(); vibrate(30) }
    strokeDistRef.current = 0; lastPtRef.current = null
  }
  const onUp = isHold ? onUpHold : isSwipe ? onUpSwipe : undefined

  const accent = isTap ? '#FFC24B' : isHold ? '#46E3A0' : isCharge ? '#a78bfa' : '#22d3ee'
  const img = pickTimeImage(def)
  const iconUrl = (isTap ? assets?.['interaction.tap.icon'] : isHold ? (holding ? assets?.['interaction.defend.icon'] : assets?.['interaction.idle.icon']) : isCharge ? assets?.['interaction.swipe.icon'] : assets?.['interaction.dodge.icon']) || ''
  const fxUrl = assets?.['interaction.tap.fx'] || ''
  const trailUrl = assets?.['interaction.swipe.trail'] || ''
  const emoji = isTap ? '👊' : isHold ? (holding ? '🛡️' : '✋') : isCharge ? '🌀' : '💨'
  const readout = isTap ? `${taps} / ${targetTaps} 次` : isHold ? `${(heldMs / 1000).toFixed(1)} / ${(needMs / 1000).toFixed(0)} 秒` : isCharge ? `${Math.round(travel)} / ${targetPx}` : `${swipes} / ${targetSwipes} 次`
  const readyMsg = isTap ? '準備連續點擊！' : isHold ? '準備按住防禦！' : isCharge ? '準備滑動蓄力！' : '準備滑動閃避！'
  const actionHint = isTap ? '連續點擊！💥' : isHold ? (holding ? '穩住！繼續按住 🛡️' : '按住螢幕不放！') : isCharge ? '快速來回滑動！🌀' : '用力滑動閃避！💨'

  return (
    <div
      onPointerDown={onDown}
      onPointerMove={isSwipe ? onMove : undefined}
      onPointerUp={onUp}
      onPointerCancel={onUp}
      style={{ position: 'fixed', inset: 0, zIndex: 2000, background: 'radial-gradient(circle at 50% 40%, rgba(20,26,34,.96), rgba(6,8,11,.98))', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'flex-start', padding: '20px 18px', touchAction: 'none', userSelect: 'none', WebkitUserSelect: 'none', WebkitTouchCallout: 'none', overflow: 'hidden' }}
    >
      <div style={{ width: '100%', maxWidth: 420, display: 'flex', justifyContent: 'flex-end' }}>
        <button
          onClick={ready ? skipPrep : finish}
          onPointerDown={(e) => e.stopPropagation()}
          style={{ background: 'rgba(255,255,255,.1)', border: '1px solid var(--line-2)', color: 'var(--tx-dim)', borderRadius: 999, padding: '6px 14px', fontSize: 12.5, cursor: 'pointer' }}
        >{ready ? '⏭ 略過準備' : '✕ 放棄'}</button>
      </div>

      <div style={{ width: '100%', maxWidth: 420, display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', marginTop: 10 }}>
        <span style={{ fontSize: 11, letterSpacing: '.2em', color: 'var(--gold)', fontWeight: 800 }}>⚡ 事件任務</span>
        <span style={{ fontSize: 22, fontWeight: 900, color: ready ? 'var(--fug)' : remain <= 3 ? 'var(--hunt)' : accent, fontVariantNumeric: 'tabular-nums' }}>{ready ? `準備 ${readyRemain}` : `${remain}s`}</span>
      </div>
      {img && <img src={img} alt="" style={{ width: '100%', maxWidth: 420, height: 120, objectFit: 'cover', borderRadius: 10, marginTop: 8 }} />}
      <div style={{ maxWidth: 420, textAlign: 'center', fontSize: 15, fontWeight: 700, color: 'var(--tx)', marginTop: 10, lineHeight: 1.5 }}>{def.message || def.name}</div>

      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 14, width: '100%' }}>
        {ready ? (
          <div style={{ fontSize: 26, fontWeight: 900, color: accent }}>{readyMsg}</div>
        ) : (
          <>
            <div style={{ position: 'relative', width: 190, height: 190, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              <svg width="190" height="190" style={{ position: 'absolute', transform: 'rotate(-90deg)' }}>
                <circle cx="95" cy="95" r="84" fill="none" stroke="rgba(255,255,255,.1)" strokeWidth="10" />
                <circle cx="95" cy="95" r="84" fill="none" stroke={accent} strokeWidth="10" strokeLinecap="round"
                  strokeDasharray={2 * Math.PI * 84} strokeDashoffset={2 * Math.PI * 84 * (1 - progress)} style={{ transition: 'stroke-dashoffset .12s' }} />
              </svg>
              <div style={{ transform: holding ? 'scale(1.12)' : 'scale(1)', transition: 'transform .1s', display: 'flex' }}>
                {iconUrl
                  ? <img src={iconUrl} alt="" draggable={false} style={{ width: 88, height: 88, objectFit: 'contain', pointerEvents: 'none' }} />
                  : <span style={{ fontSize: 60 }}>{emoji}</span>}
              </div>
            </div>
            <div style={{ fontSize: 34, fontWeight: 900, color: accent, fontVariantNumeric: 'tabular-nums' }}>{readout}</div>
            <div style={{ fontSize: 17, fontWeight: 800, color: 'var(--tx)' }}>{actionHint}</div>
            <div style={{ fontSize: 12.5, color: 'var(--tx-faint)' }}>完成度 {pct}%（星等依完成度計）</div>
          </>
        )}
      </div>

      {/* 滑動拖尾 */}
      {trail.map((z, i) => (
        trailUrl
          ? <img key={z.id} src={trailUrl} alt="" draggable={false} style={{ position: 'absolute', left: z.x, top: z.y, width: 26, height: 26, objectFit: 'contain', pointerEvents: 'none', transform: 'translate(-50%,-50%)', opacity: (i + 1) / trail.length }} />
          : <span key={z.id} style={{ position: 'absolute', left: z.x, top: z.y, width: 16, height: 16, borderRadius: '50%', background: accent, pointerEvents: 'none', transform: 'translate(-50%,-50%)', opacity: ((i + 1) / trail.length) * 0.8, boxShadow: `0 0 10px ${accent}` }} />
      ))}
      {/* 點擊特效 */}
      {fx.map((z) => (
        fxUrl
          ? <img key={z.id} src={fxUrl} alt="" draggable={false} style={{ position: 'absolute', left: z.x, top: z.y, width: 44, height: 44, objectFit: 'contain', pointerEvents: 'none', transform: 'translate(-50%,-50%)', animation: 'fxPop .5s ease-out forwards' }} />
          : <span key={z.id} style={{ position: 'absolute', left: z.x, top: z.y, fontSize: 34, pointerEvents: 'none', transform: 'translate(-50%,-50%)', animation: 'fxPop .5s ease-out forwards' }}>{z.e}</span>
      ))}
      <style>{`@keyframes fxPop{0%{opacity:1;transform:translate(-50%,-50%) scale(.5)}100%{opacity:0;transform:translate(-50%,-140%) scale(1.6)}}`}</style>
    </div>
  )
}
