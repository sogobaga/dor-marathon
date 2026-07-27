'use client'

import { useEffect, useRef, useState } from 'react'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'
import { monopolyApi, type MonopolyRollResult } from '@/lib/api'
import GpCoin from './GpCoin'

// 環台大富翁 Phase 1：盤面遊戲。
// 盤面固定 46 格（position 0=START，1..45 為格子，繞一圈=46 格），座標由底圖量出、寫死成常數
// （之後美術要微調座標，直接改這個陣列即可，不需要動任何邏輯）。
// 機會格(6,15,25,35)／命運格(10,18,30,39)：Phase 1 只顯示「抽卡功能即將開放」placeholder，
// 真正抽卡是 Phase 3；判定完全交給後端回傳的 landed_on + draw_pending，前端不重複判斷。
const BOARD_SIZE = 46
const BOARD_COORDS: [number, number][] = [
  [28, 82], [34, 82], [40, 82], [46, 81], [51, 80], [54, 77], [56, 73], [57, 69], [58, 65], [61, 61],
  [66, 58], [68, 55], [70, 51], [71, 47], [72, 43], [74, 39], [73, 35], [75, 32], [81, 28], [79, 25],
  [80, 21], [79, 17], [77, 14], [72, 12], [66, 11], [60, 11], [54, 12], [49, 14], [45, 17], [41, 21],
  [36, 23], [35, 27], [33, 31], [33, 35], [31, 38], [20, 41], [22, 45], [22, 48], [22, 52], [17, 56],
  [20, 60], [22, 64], [24, 67], [25, 70], [25, 73], [24, 76],
]
const BOARD_IMG = '/source/ui/02_BG/DOR_TAW_RUNNER_START_1to45.png'
const RUNNER_IMG = '/source/ui/02_BG/DOR_RUNNER.png'
// 六面骰字符（Unicode 骰子），滾動動畫期間快速切換，最終停在後端回傳的點數
const DIE_GLYPH = ['', '⚀', '⚁', '⚂', '⚃', '⚄', '⚅']

function sleep(ms: number) {
  return new Promise<void>((resolve) => window.setTimeout(resolve, ms))
}

type Phase = 'idle' | 'dice' | 'moving'

export default function MonopolyScreen({ onBack }: { onBack: () => void }) {
  const user = useUser()
  const [position, setPosition] = useState(0)
  const [laps, setLaps] = useState(0)
  const [gpBalance, setGpBalance] = useState<number | null>(null)
  const [diceCost, setDiceCost] = useState(3)
  const [loading, setLoading] = useState(true)
  const [loadErr, setLoadErr] = useState(false)

  const [phase, setPhase] = useState<Phase>('idle')
  const [dieFace, setDieFace] = useState(1)
  const [rollErr, setRollErr] = useState('')
  const [drawModal, setDrawModal] = useState<'chance' | 'destiny' | null>(null)
  const [lapCelebration, setLapCelebration] = useState<{ laps: number; gp: number } | null>(null)
  const [startFlash, setStartFlash] = useState(false)

  const dieIntervalRef = useRef<number | null>(null)

  useEffect(() => {
    let cancelled = false
    async function load() {
      if (!getUserToken()) { setLoading(false); return }
      try {
        const st = await withUserAuth((t) => monopolyApi.state(t))
        if (cancelled) return
        setPosition(st.position)
        setLaps(st.laps_completed)
        setGpBalance(st.gp_balance)
        setDiceCost(st.dice_gp_cost)
      } catch {
        if (!cancelled) setLoadErr(true)
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => { cancelled = true }
  }, [])

  useEffect(() => () => {
    if (dieIntervalRef.current != null) window.clearInterval(dieIntervalRef.current)
  }, [])

  const busy = phase !== 'idle'
  const canAfford = gpBalance != null && gpBalance >= diceCost

  async function movePiece(res: MonopolyRollResult) {
    setPhase('moving')
    const steps: number[] = []
    for (let i = 1; i <= res.roll; i++) steps.push((res.from + i) % BOARD_SIZE)
    for (const p of steps) {
      setPosition(p)
      if (p === 0 && p !== res.to) {
        // 中途繞經 START（尚未走完，仍是繞圈路徑上的一步）→ 閃一下讓玩家有「經過感」
        setStartFlash(true)
        window.setTimeout(() => setStartFlash(false), 260)
      }
      // eslint-disable-next-line no-await-in-loop
      await sleep(250)
    }
    setGpBalance(res.gp_balance)
    if (res.draw_pending) {
      setDrawModal(res.landed_on as 'chance' | 'destiny')
    } else if (res.laps_gained > 0) {
      setLapCelebration({ laps: laps + res.laps_gained, gp: res.lap_reward_gp })
    }
    setLaps((l) => l + res.laps_gained)
    setPhase('idle')
  }

  async function handleRoll() {
    if (busy || !canAfford) return
    setRollErr('')
    setPhase('dice')
    setDieFace(1 + Math.floor(Math.random() * 6))
    dieIntervalRef.current = window.setInterval(() => {
      setDieFace(1 + Math.floor(Math.random() * 6))
    }, 90)

    const minAnimMs = 900
    const startedAt = Date.now()
    let res: MonopolyRollResult
    try {
      res = await withUserAuth((t) => monopolyApi.roll(t))
    } catch (e: any) {
      if (dieIntervalRef.current != null) { window.clearInterval(dieIntervalRef.current); dieIntervalRef.current = null }
      setPhase('idle')
      setRollErr(e?.status === 409 ? 'GP 不足，無法擲骰' : '擲骰失敗，請稍後再試')
      return
    }
    const wait = Math.max(0, minAnimMs - (Date.now() - startedAt))
    await sleep(wait)
    if (dieIntervalRef.current != null) { window.clearInterval(dieIntervalRef.current); dieIntervalRef.current = null }
    setDieFace(res.roll) // 動畫收尾＝後端回傳的點數，前端絕不自行決定結果
    await sleep(280) // 停頓看清骰面，再開始走格子
    await movePiece(res)
  }

  const [x, y] = BOARD_COORDS[position]
  const [startX, startY] = BOARD_COORDS[0]

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)', position: 'relative' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>🎲 環台大富翁</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '10px 18px 28px' }}>
        {!user ? (
          <div style={{ color: 'var(--tx-dim)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>請先登入才能遊玩</div>
        ) : loading ? (
          <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>載入中…</div>
        ) : loadErr ? (
          <div style={{ color: 'var(--hunt)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>載入失敗，請稍後再試</div>
        ) : (
          <>
            {/* 狀態列：GP 餘額 + 目前圈數 */}
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 14 }}>
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 15, fontWeight: 900, color: 'var(--violet)' }}>
                <GpCoin size={18} />{(gpBalance ?? 0).toLocaleString()}
              </span>
              <span style={{ fontSize: 13, fontWeight: 700, color: 'var(--tx-dim)' }}>第 {laps} 圈</span>
            </div>

            {/* 盤面 */}
            <div style={{ position: 'relative', width: '100%', maxWidth: 480, margin: '0 auto' }}>
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={BOARD_IMG} alt="環台盤面" style={{ width: '100%', display: 'block', borderRadius: 12 }} />

              {/* START 經過閃光 */}
              {startFlash && (
                <div style={{
                  position: 'absolute', left: `${startX}%`, top: `${startY}%`, width: '13%', aspectRatio: '1/1',
                  transform: 'translate(-50%,-50%)', borderRadius: '50%',
                  background: 'radial-gradient(circle, rgba(231,184,75,.55), rgba(231,184,75,0) 70%)',
                  animation: 'monoStartFlash .26s ease-out', pointerEvents: 'none',
                }} />
              )}

              {/* 棋子 */}
              <div style={{
                position: 'absolute', left: `${x}%`, top: `${y}%`, width: '10%',
                transform: 'translate(-50%,-50%)', transition: 'left .22s linear, top .22s linear',
                pointerEvents: 'none', zIndex: 5,
              }}>
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img src={RUNNER_IMG} alt="" style={{
                  width: '100%', display: 'block',
                  animation: phase === 'moving' ? 'monoRunnerHop .25s ease-in-out infinite' : 'none',
                  filter: phase === 'moving' ? 'drop-shadow(0 4px 6px rgba(0,0,0,.35))' : 'none',
                }} />
              </div>
            </div>

            {/* 擲骰區 */}
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 10, marginTop: 22 }}>
              {phase === 'dice' && (
                <div style={{ fontSize: 56, lineHeight: 1, animation: 'monoDiceShake .09s linear infinite' }} aria-hidden="true">
                  {DIE_GLYPH[dieFace]}
                </div>
              )}
              <button
                onClick={handleRoll}
                disabled={busy || !canAfford}
                style={{
                  ...rollBtn,
                  opacity: busy || !canAfford ? 0.55 : 1,
                  cursor: busy || !canAfford ? 'default' : 'pointer',
                }}
              >
                {phase === 'dice' ? '擲骰中…' : phase === 'moving' ? '前進中…' : `擲骰（−${diceCost} GP）`}
              </button>
              {!canAfford && phase === 'idle' && (
                <div style={{ fontSize: 12, color: 'var(--hunt)', fontWeight: 700 }}>GP 不足，無法擲骰</div>
              )}
              {rollErr && (
                <div style={{ fontSize: 12, color: 'var(--hunt)', fontWeight: 700 }}>{rollErr}</div>
              )}
            </div>
          </>
        )}
      </div>

      {/* 機會/命運 placeholder */}
      {drawModal && (
        <div style={overlayStyle} onClick={() => setDrawModal(null)}>
          <div style={cardStyle} onClick={(e) => e.stopPropagation()}>
            <div style={{ fontSize: 32, marginBottom: 8 }}>{drawModal === 'chance' ? '🎁' : '📜'}</div>
            <div style={{ fontSize: 16, fontWeight: 900, color: 'var(--tx)' }}>
              {drawModal === 'chance' ? '機會' : '命運'} · 抽卡功能即將開放
            </div>
            <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 8, lineHeight: 1.7 }}>
              停在了{drawModal === 'chance' ? '機會' : '命運'}格！這裡的抽卡玩法還在準備中，敬請期待。
            </div>
            <button onClick={() => setDrawModal(null)} style={{ ...rollBtn, marginTop: 16, padding: '9px 28px', fontSize: 13.5 }}>知道了</button>
          </div>
        </div>
      )}

      {/* 繞圈慶祝 */}
      {lapCelebration && (
        <div style={overlayStyle} onClick={() => setLapCelebration(null)}>
          <div style={cardStyle} onClick={(e) => e.stopPropagation()}>
            <div style={{ fontSize: 32, marginBottom: 8 }}>🏁</div>
            <div style={{ fontSize: 16, fontWeight: 900, color: 'var(--tx)' }}>繞完第 {lapCelebration.laps} 圈！</div>
            {lapCelebration.gp > 0 && (
              <div style={{
                display: 'inline-flex', alignItems: 'center', gap: 6, marginTop: 10,
                background: 'var(--gold)', color: '#fff', fontWeight: 900, fontSize: 14,
                borderRadius: 999, padding: '6px 16px',
              }}>
                <GpCoin size={16} /> +{lapCelebration.gp} GP
              </div>
            )}
            <button onClick={() => setLapCelebration(null)} style={{ ...rollBtn, marginTop: 16, padding: '9px 28px', fontSize: 13.5 }}>太棒了</button>
          </div>
        </div>
      )}

      <style>{`
        @keyframes monoDiceShake { 0%{transform:rotate(-6deg) scale(1)} 50%{transform:rotate(6deg) scale(1.06)} 100%{transform:rotate(-6deg) scale(1)} }
        @keyframes monoRunnerHop { 0%,100%{transform:translateY(0) scale(1)} 50%{transform:translateY(-14%) scale(1.06)} }
        @keyframes monoStartFlash { 0%{opacity:0;transform:translate(-50%,-50%) scale(.6)} 40%{opacity:1} 100%{opacity:0;transform:translate(-50%,-50%) scale(1.3)} }
      `}</style>
    </div>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
const rollBtn: React.CSSProperties = {
  background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', borderRadius: 999,
  padding: '13px 34px', fontSize: 15.5, fontWeight: 900, fontFamily: 'inherit',
}
const overlayStyle: React.CSSProperties = {
  position: 'absolute', inset: 0, background: 'rgba(10,13,12,.62)', display: 'flex',
  alignItems: 'center', justifyContent: 'center', zIndex: 700, padding: 24,
}
const cardStyle: React.CSSProperties = {
  background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 16, padding: '24px 22px',
  maxWidth: 320, width: '100%', textAlign: 'center', boxShadow: '0 12px 40px rgba(0,0,0,.35)',
}
