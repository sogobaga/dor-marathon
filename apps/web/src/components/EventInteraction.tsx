'use client'

import { useEffect, useRef, useState } from 'react'
import { pickTimeImage, type ActiveEvent } from './EventTaskModal'
import { playTapHit, playDefend, vibrate } from '@/lib/sfx'
import { shapeAccepts, recognizeShape, shapeSvgPoints, shapeName, type Pt } from '@/lib/shapes'

// 互動小遊戲全屏層：點擊攻擊 / 按住防禦 / 滑動蓄力(魔法) / 滑動閃避 / 畫圖形(魔法陣)。
// 依完成度計分，時間到（或按「放棄」）回傳 evidence 給 /track 送後端分級發獎。
type Ev = { taps: number; held_ms: number; swipe_px: number; swipes: number; shape_pts: [number, number][]; shape: number }

// 依權重抽出本次要畫的圖形（w3/w4/w5；都未設則平均隨機）
function pickShape(p: Record<string, number>): number {
  const w: Record<number, number> = { 3: p.w3 || 0, 4: p.w4 || 0, 5: p.w5 || 0 }
  const total = w[3] + w[4] + w[5]
  if (total <= 0) return [3, 4, 5][Math.floor(Math.random() * 3)]
  let r = Math.random() * total
  for (const s of [3, 4, 5]) { r -= w[s]; if (r < 0) return s }
  return 5
}

export function EventInteraction({ active, onDone, paused, assets }: { active: ActiveEvent; onDone: (ev: Ev) => void; paused?: boolean; assets?: Record<string, string> }) {
  const def = active.def
  const p = def.completion_params
  const ct = def.completion_type
  const isTap = ct === 'tap_burst'
  const isHold = ct === 'hold_press'
  const isCharge = ct === 'swipe_charge'
  const isDodge = ct === 'dodge_swipe'
  const isSwipe = isCharge || isDodge
  const isShape = ct === 'draw_shape'
  const targetTaps = Math.max(1, Math.round(p.target_taps ?? 20))
  const needMs = Math.max(1, (p.hold_s ?? 5) * 1000)
  const targetPx = Math.max(1, Math.round(p.target_px ?? 4000))
  const targetSwipes = Math.max(1, Math.round(p.target_swipes ?? 5))
  const attempts = Math.max(1, Math.round(p.attempts ?? 3))
  const [shape] = useState(() => isShape ? pickShape(p) : 0) // 本次抽到的圖形（整場固定）

  const [now, setNow] = useState(Date.now())
  const [taps, setTaps] = useState(0)
  const [heldMs, setHeldMs] = useState(0)
  const [holding, setHolding] = useState(false)
  const [travel, setTravel] = useState(0)
  const [swipes, setSwipes] = useState(0)
  const [fx, setFx] = useState<{ id: number; x: number; y: number; e: string }[]>([])
  const [trail, setTrail] = useState<{ id: number; x: number; y: number }[]>([])
  const [strokePts, setStrokePts] = useState<Pt[]>([]) // 畫圖形：目前這筆的螢幕座標
  const [shapeOk, setShapeOk] = useState(false)
  const [attemptsLeft, setAttemptsLeft] = useState(attempts)
  const [shapeMsg, setShapeMsg] = useState('')
  const [burst, setBurst] = useState<{ id: number; dx: number; dy: number; e: string }[]>([]) // 成功噴發粒子
  const [bgLoaded, setBgLoaded] = useState(false) // 底圖載入完成
  // 導引虛線階段：hidden→(底圖先出現)→blink(閃兩下)→gone(有底圖時消失)/shown(無底圖時保留)
  const [guidePhase, setGuidePhase] = useState<'hidden' | 'blink' | 'shown' | 'gone'>('hidden')

  const tapsRef = useRef(0)
  const heldRef = useRef(0)
  const holdStartRef = useRef<number | null>(null)
  const pointersRef = useRef<Set<number>>(new Set())
  const travelRef = useRef(0)
  const swipesRef = useRef(0)
  const strokeDistRef = useRef(0)
  const lastPtRef = useRef<{ x: number; y: number } | null>(null)
  const activeSwipeIdRef = useRef<number | null>(null) // 只認「主滑動指」，其餘手指不影響累積（多點觸控防暴衝/防弊）
  const drawnRef = useRef<Pt[]>([]) // 畫圖形：目前這筆的點
  const bestPtsRef = useRef<Pt[]>([]) // 最接近的一次筆跡（結算送後端重算）
  const bestDistRef = useRef(Infinity)
  const shapeOkRef = useRef(false)
  const attemptsLeftRef = useRef(attempts)
  const finishTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const clearStrokeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const lastTrailRef = useRef(0)
  const lastTravelUiRef = useRef(0)
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
    onDoneRef.current({ taps: tapsRef.current, held_ms: heldRef.current, swipe_px: travelRef.current, swipes: swipesRef.current, shape_pts: bestPtsRef.current.map((p) => [p.x, p.y]), shape })
  }
  function spawnBurst() {
    const parts = Array.from({ length: 16 }, (_, i) => {
      const a = (i / 16) * Math.PI * 2 + Math.random() * 0.5
      const dist = 120 + Math.random() * 120
      return { id: fxId.current++, dx: Math.cos(a) * dist, dy: Math.sin(a) * dist, e: ['✨', '⭐', '💫', '🌟'][i % 4] }
    })
    setBurst(parts)
    setTimeout(() => setBurst([]), 1000)
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

  useEffect(() => () => { // 卸載清掉待觸發的計時器
    if (finishTimerRef.current) clearTimeout(finishTimerRef.current)
    if (clearStrokeTimerRef.current) clearTimeout(clearStrokeTimerRef.current)
  }, [])

  // 進入互動、底圖載入後 → 顯示導引虛線
  const shapeBg = isShape ? (assets?.[`interaction.shape.bg${shape}`] || '') : ''
  const nowMs = now
  const inDraw = isShape && nowMs >= active.readyUntil + pausedMsRef.current - skipMsRef.current
  useEffect(() => {
    if (guidePhase !== 'hidden' || !inDraw) return
    if (shapeBg && !bgLoaded) return // 先等底圖出現
    const t = setTimeout(() => setGuidePhase('blink'), 200)
    return () => clearTimeout(t)
  }, [guidePhase, inDraw, shapeBg, bgLoaded])
  useEffect(() => {
    if (guidePhase !== 'blink') return
    const t = setTimeout(() => setGuidePhase(shapeBg ? 'gone' : 'shown'), 1150) // 閃兩下後：有底圖→消失、無底圖→保留
    return () => clearTimeout(t)
  }, [guidePhase, shapeBg])

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
    } else if (isShape) {
      if (shapeOkRef.current) return
      if (clearStrokeTimerRef.current) clearTimeout(clearStrokeTimerRef.current) // 別讓上一筆的清除蓋掉新筆
      if (activeSwipeIdRef.current == null) { activeSwipeIdRef.current = e.pointerId; drawnRef.current = [ptOf(e)]; setStrokePts([ptOf(e)]) }
    } else if (isSwipe) {
      // 只讓第一根按下的指驅動滑動；第二指落下不重置進行中的段落
      if (activeSwipeIdRef.current == null) { activeSwipeIdRef.current = e.pointerId; lastPtRef.current = ptOf(e); strokeDistRef.current = 0 }
    } else {
      pointersRef.current.add(e.pointerId)
      if (holdStartRef.current == null) { holdStartRef.current = Date.now(); setHolding(true); playDefend(); vibrate(35) }
    }
  }
  function onMove(e: React.PointerEvent) {
    if (!(isSwipe || isShape) || !active2) return
    if (e.pointerId !== activeSwipeIdRef.current) return // 只認主指
    if (isShape) {
      if (shapeOkRef.current) return
      const pt = ptOf(e)
      drawnRef.current.push(pt)
      const t = Date.now()
      if (t - lastTravelUiRef.current > 24) { lastTravelUiRef.current = t; setStrokePts(drawnRef.current.slice()) }
      return
    }
    const { x, y } = ptOf(e)
    const last = lastPtRef.current
    if (last) {
      const d = Math.hypot(x - last.x, y - last.y)
      travelRef.current += d; strokeDistRef.current += d
      const t = Date.now()
      if (t - lastTravelUiRef.current > 40) { lastTravelUiRef.current = t; setTravel(travelRef.current) } // UI 節流；計數仍即時
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
  function onUpSwipe(e: React.PointerEvent) {
    if (e.pointerId !== activeSwipeIdRef.current) return // 非主指放開不結算
    if (isDodge && strokeDistRef.current > 45) { swipesRef.current += 1; setSwipes(swipesRef.current); playDefend(); vibrate(30) }
    strokeDistRef.current = 0; lastPtRef.current = null; activeSwipeIdRef.current = null; setTravel(travelRef.current)
  }
  function onUpShape(e: React.PointerEvent) {
    if (e.pointerId !== activeSwipeIdRef.current) return
    activeSwipeIdRef.current = null
    // 暫停(橫向)/時間到時放開 → 不判定、不消耗次數
    if (pausedRef.current || Date.now() >= active.deadline + pausedMsRef.current - skipMsRef.current) { drawnRef.current = []; return }
    if (shapeOkRef.current) return
    const pts = drawnRef.current.slice()
    setStrokePts(pts)
    if (pts.length < 8) { drawnRef.current = []; return } // 太短：不算一次嘗試
    const r = recognizeShape(pts)
    if (r.dist < bestDistRef.current) { bestDistRef.current = r.dist; bestPtsRef.current = pts } // 保留最接近的一次，供結算送出
    if (shapeAccepts(pts, shape)) {
      shapeOkRef.current = true; setShapeOk(true); setShapeMsg('')
      bestPtsRef.current = pts
      playDefend(); vibrate([50, 40, 160]); spawnBurst()
      if (finishTimerRef.current) clearTimeout(finishTimerRef.current)
      finishTimerRef.current = setTimeout(() => finish(), 1300) // 演出（發光+噴發）後才結束
    } else {
      attemptsLeftRef.current -= 1; setAttemptsLeft(attemptsLeftRef.current); vibrate(60)
      if (attemptsLeftRef.current <= 0) { finish() } // 用完 → 送最接近的一次交伺服器判（多半 0 分）
      else {
        setShapeMsg('形狀不太對，再試一次'); drawnRef.current = []
        if (clearStrokeTimerRef.current) clearTimeout(clearStrokeTimerRef.current)
        clearStrokeTimerRef.current = setTimeout(() => setStrokePts([]), 300)
      }
    }
  }
  const onUp = isHold ? onUpHold : isShape ? onUpShape : isSwipe ? onUpSwipe : undefined

  const accent = isTap ? '#FFC24B' : isHold ? '#46E3A0' : isCharge ? '#a78bfa' : isDodge ? '#22d3ee' : '#c084fc'
  const img = pickTimeImage(def)
  const iconUrl = (isTap ? assets?.['interaction.tap.icon'] : isHold ? (holding ? assets?.['interaction.defend.icon'] : assets?.['interaction.idle.icon']) : isCharge ? assets?.['interaction.swipe.icon'] : assets?.['interaction.dodge.icon']) || ''
  const fxUrl = assets?.['interaction.tap.fx'] || ''
  const trailUrl = assets?.['interaction.swipe.trail'] || ''
  const emoji = isTap ? '👊' : isHold ? (holding ? '🛡️' : '✋') : isCharge ? '🌀' : '💨'
  const readout = isTap ? `${taps} / ${targetTaps} 次` : isHold ? `${(heldMs / 1000).toFixed(1)} / ${(needMs / 1000).toFixed(0)} 秒` : isCharge ? `${Math.round(travel)} / ${targetPx}` : `${swipes} / ${targetSwipes} 次`
  const readyMsg = isTap ? '準備連續點擊！' : isHold ? '準備按住防禦！' : isCharge ? '準備滑動蓄力！' : isDodge ? '準備滑動閃避！' : '請跟隨身體的能量流動，畫出圖形。'
  const actionHint = isTap ? '連續點擊！💥' : isHold ? (holding ? '穩住！繼續按住 🛡️' : '按住螢幕不放！') : isCharge ? '快速來回滑動！🌀' : '用力滑動閃避！💨'

  return (
    <div
      onPointerDown={onDown}
      onPointerMove={(isSwipe || isShape) ? onMove : undefined}
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
        ) : isShape ? (
          <>
            {/* 底圖先出現 → 導引虛線閃兩下（有底圖則閃完消失，無底圖則保留）；成功時發光＋脈動 */}
            <div style={{ position: 'relative', width: 264, height: 264, animation: shapeOk ? 'shapePulse .8s ease-in-out infinite' : undefined }}>
              {shapeBg && <img src={shapeBg} alt="" draggable={false} onLoad={() => setBgLoaded(true)} style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', objectFit: 'contain', opacity: shapeOk ? 1 : 0.55, filter: shapeOk ? `drop-shadow(0 0 22px ${accent}) drop-shadow(0 0 10px ${accent})` : undefined, transition: 'all .2s', pointerEvents: 'none' }} />}
              {(guidePhase === 'blink' || guidePhase === 'shown' || (shapeOk && !shapeBg)) && (
                <svg width="264" height="264" style={{ position: 'absolute', inset: 0 }}>
                  <polyline points={shapeSvgPoints(shape, 264)} fill="none"
                    stroke={shapeOk ? accent : shapeBg ? 'rgba(255,255,255,.22)' : 'rgba(255,255,255,.26)'} strokeWidth={shapeOk ? 8 : 3}
                    strokeLinecap="round" strokeLinejoin="round" strokeDasharray={shapeOk ? undefined : '9 9'}
                    style={{ filter: shapeOk ? `drop-shadow(0 0 22px ${accent}) drop-shadow(0 0 10px ${accent})` : undefined, transition: 'all .2s', animation: guidePhase === 'blink' && !shapeOk ? 'guideBlink 1.15s ease-in-out forwards' : undefined }} />
                </svg>
              )}
            </div>
            <div style={{ fontSize: 20, fontWeight: 900, color: accent }}>{shapeOk ? '✦ 施法成功！' : `描出 ${shapeName(shape)}`}</div>
            {!shapeOk && <div style={{ fontSize: 13.5, color: 'var(--tx)' }}>一筆畫完成・剩 {attemptsLeft} 次機會</div>}
            {shapeMsg && <div style={{ fontSize: 12.5, color: 'var(--hunt)' }}>{shapeMsg}</div>}
          </>
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

      {/* 施法成功：中央噴發粒子 */}
      {burst.map((b) => (
        <span key={b.id} style={{ position: 'absolute', left: '50%', top: '48%', fontSize: 26, pointerEvents: 'none', ['--dx' as string]: `${b.dx}px`, ['--dy' as string]: `${b.dy}px`, animation: 'burstFly .95s ease-out forwards' } as React.CSSProperties}>{b.e}</span>
      ))}
      {/* 畫圖形：玩家這一筆的軌跡（螢幕座標，發光） */}
      {isShape && strokePts.length > 1 && (
        <svg style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', pointerEvents: 'none' }}>
          <polyline points={strokePts.map((p) => `${p.x},${p.y}`).join(' ')} fill="none" stroke={accent} strokeWidth={6} strokeLinecap="round" strokeLinejoin="round" style={{ filter: `drop-shadow(0 0 6px ${accent})` }} />
        </svg>
      )}
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
      <style>{`
        @keyframes fxPop{0%{opacity:1;transform:translate(-50%,-50%) scale(.5)}100%{opacity:0;transform:translate(-50%,-140%) scale(1.6)}}
        @keyframes shapePulse{0%,100%{transform:scale(1)}50%{transform:scale(1.06)}}
        @keyframes guideBlink{0%{opacity:1}20%{opacity:.08}40%{opacity:1}60%{opacity:.08}80%{opacity:1}100%{opacity:1}}
        @keyframes burstFly{0%{opacity:1;transform:translate(-50%,-50%) scale(.4)}70%{opacity:1}100%{opacity:0;transform:translate(calc(-50% + var(--dx)),calc(-50% + var(--dy))) scale(1.25)}}
      `}</style>
    </div>
  )
}
