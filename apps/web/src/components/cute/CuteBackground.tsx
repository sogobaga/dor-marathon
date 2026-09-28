'use client'

// cute（溫馨可愛）skin 全站背景（契約 §3）：原創彩色紙屑（小方塊／彎曲線條）、五角小
// 星星、蓬鬆小雲朵，緩慢飄動，數量 ≤60、更新率上限 30fps、分頁隱藏時停止、
// prefers-reduced-motion 只畫一次靜態畫面。
//
// 由 SkinOverride（另一位工人負責）在 [data-skin="cute"] 生效時才掛載本元件；本檔案本身
// 不判斷目前 skin 是否為 cute——只負責「被掛上後怎麼畫」。沒人 import 這支檔案（或走
// dynamic import）的話，非白名單使用者的 bundle 完全不受影響（比照 RetroBackground 慣例）。
//
// 圖案繪製邏輯全部委派給 decor.ts 的 drawConfettiPiece()／drawStar()／drawCloud()
// （與 app/track/cute/ 的 MAP 工人共用同一份原創美術資料，愛心／紙膠帶等圖案供軌跡標記
// 重用，避免兩處各刻一套、風格不一致）。
//
// 效能／無障礙原則：
//   - backing canvas 依 devicePixelRatio（上限 2）繪製，維持向量圖案在高解析度螢幕上清晰，
//     不像 retro 像素背景需要刻意降解析度。
//   - 更新率上限 30fps：用 requestAnimationFrame + 上次繪製時間戳記門檻（~33ms）節流，
//     不是每個 rAF tick 都重繪。
//   - 分頁隱藏（visibilitychange）時完全停止排程 rAF，不消耗任何 CPU。
//   - prefers-reduced-motion 只畫一次靜態畫面（不啟動動畫迴圈）。
//   - resize 時依可視面積重新配置粒子（不超過 MAX_PARTICLES），並重繪一次。
//
// 無 props：呼叫端只需要 <CuteBackground />（比照 RetroBackground 的呼叫慣例）。
import { useEffect, useRef } from 'react'
import { drawConfettiPiece, drawStar, drawCloud, CUTE_CONFETTI_COLORS, CUTE_PALETTE, type ConfettiKind } from './decor'
import { loadCuteFont } from './fonts'

const FRAME_INTERVAL_MS = 1000 / 30 // 契約 §3：更新率上限 30fps
const MAX_PARTICLES = 56 // 契約 §3：≤60 個（留一點餘裕給不同螢幕尺寸的密度換算）
const MAX_DPR = 2

type ParticleKind = 'confetti-square' | 'confetti-curl' | 'star' | 'cloud'

interface Particle {
  kind: ParticleKind
  x: number
  y: number
  vx: number
  vy: number
  size: number
  color: string
  rotation: number
  rotSpeed: number
  phase: number
}

function rand(min: number, max: number): number {
  return min + Math.random() * (max - min)
}

function pickColor(excludeWhiteLike = false): string {
  const pool = excludeWhiteLike
    ? CUTE_CONFETTI_COLORS.filter((c) => c !== CUTE_PALETTE.creamYellow)
    : CUTE_CONFETTI_COLORS
  return pool[Math.floor(Math.random() * pool.length)]
}

function makeParticle(kind: ParticleKind, w: number, h: number, initial: boolean): Particle {
  const x = rand(0, w)
  const y = initial ? rand(0, h) : -rand(20, 60)
  switch (kind) {
    case 'confetti-square':
      return {
        kind,
        x,
        y,
        vx: rand(-6, 6),
        vy: rand(10, 20),
        size: rand(7, 14),
        color: pickColor(),
        rotation: rand(0, Math.PI * 2),
        rotSpeed: rand(-0.8, 0.8),
        phase: rand(0, Math.PI * 2),
      }
    case 'confetti-curl':
      return {
        kind,
        x,
        y,
        vx: rand(-5, 5),
        vy: rand(8, 16),
        size: rand(10, 18),
        color: pickColor(),
        rotation: rand(0, Math.PI * 2),
        rotSpeed: rand(-0.6, 0.6),
        phase: rand(0, Math.PI * 2),
      }
    case 'star':
      return {
        kind,
        x,
        y: initial ? rand(0, h) : rand(0, h),
        vx: 0,
        vy: rand(2, 5),
        size: rand(5, 9),
        color: CUTE_PALETTE.gold,
        rotation: rand(0, Math.PI * 2),
        rotSpeed: rand(-0.2, 0.2),
        phase: rand(0, Math.PI * 2),
      }
    case 'cloud':
    default:
      return {
        kind,
        x,
        y: initial ? rand(0, h) : rand(0, h),
        vx: rand(-8, 8) || 6,
        vy: 0,
        size: rand(60, 110),
        color: '#f3faff', // 見 decor.ts drawCloud() 說明：刻意不用純白，跟奶油背景才有區隔
        rotation: 0,
        rotSpeed: 0,
        phase: rand(0, Math.PI * 2),
      }
  }
}

function buildParticles(w: number, h: number): Particle[] {
  // 依可視面積換算密度，但整體數量絕不超過 MAX_PARTICLES（契約 §3「≤60 個」）。
  const area = Math.max(1, w * h)
  const density = Math.min(1, area / (390 * 700)) // 以手機畫面尺寸為基準 1.0
  const counts = {
    'confetti-square': Math.round(16 * density),
    'confetti-curl': Math.round(8 * density),
    star: Math.round(14 * density),
    cloud: Math.round(6 * density),
  } as const
  const particles: Particle[] = []
  ;(Object.keys(counts) as ParticleKind[]).forEach((kind) => {
    const n = counts[kind]
    for (let i = 0; i < n; i++) {
      particles.push(makeParticle(kind, w, h, true))
    }
  })
  return particles.slice(0, MAX_PARTICLES)
}

export default function CuteBackground() {
  const canvasRef = useRef<HTMLCanvasElement | null>(null)

  useEffect(() => {
    loadCuteFont() // 契約 §3：只在 cute 生效（本元件掛載）時才載入 DORCute

    const canvas = canvasRef.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return // 極舊瀏覽器沒有 2D context：靜默不畫，本層本來就 pointer-events:none

    let particles: Particle[] = []
    let cssW = 0
    let cssH = 0
    let rafId: number | null = null
    let lastFrameTs = 0
    let lastTickTs = 0
    let visible = typeof document === 'undefined' ? true : document.visibilityState !== 'hidden'

    const reduceMotionMq = typeof window !== 'undefined' ? window.matchMedia('(prefers-reduced-motion: reduce)') : null

    function render(nowSec: number) {
      if (!ctx) return
      ctx.clearRect(0, 0, cssW, cssH)
      for (const p of particles) {
        if (p.kind === 'cloud') {
          const bob = Math.sin(nowSec * 0.6 + p.phase) * 4
          drawCloud(ctx, p.x, p.y + bob, p.size, p.color, 0.8)
        } else if (p.kind === 'star') {
          const twinkle = 0.55 + 0.45 * Math.sin(nowSec * 1.4 + p.phase)
          drawStar(ctx, p.x, p.y, p.size, p.color, p.rotation, twinkle)
        } else {
          const kind: ConfettiKind = p.kind === 'confetti-square' ? 'square' : 'curl'
          drawConfettiPiece(ctx, p.x, p.y, p.size, p.color, p.rotation, kind)
        }
      }
    }

    function step(dt: number) {
      const marginTop = -80
      for (const p of particles) {
        if (p.kind === 'cloud') {
          p.x += p.vx * dt
          if (p.x < -p.size) p.x = cssW + p.size
          if (p.x > cssW + p.size) p.x = -p.size
          continue
        }
        p.x += p.vx * dt
        p.y += p.vy * dt
        p.rotation += p.rotSpeed * dt
        if (p.kind !== 'star') {
          // confetti 左右微幅擺動，飄落感比純直線自然。
          p.x += Math.sin(p.y * 0.03 + p.phase) * 0.4
        }
        if (p.x < -30) p.x = cssW + 30
        if (p.x > cssW + 30) p.x = -30
        if (p.y > cssH + 30) {
          p.y = marginTop
          p.x = rand(0, cssW)
        }
      }
    }

    function resize() {
      if (!canvas) return
      cssW = window.innerWidth
      cssH = window.innerHeight
      const dpr = Math.min(MAX_DPR, window.devicePixelRatio || 1)
      canvas.width = Math.ceil(cssW * dpr)
      canvas.height = Math.ceil(cssH * dpr)
      canvas.style.width = cssW + 'px'
      canvas.style.height = cssH + 'px'
      if (ctx) ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
      particles = buildParticles(cssW, cssH)
      render(0)
    }

    function loop(ts: number) {
      if (!visible) return
      rafId = requestAnimationFrame(loop)
      if (ts - lastFrameTs < FRAME_INTERVAL_MS) return
      const dtMs = lastTickTs === 0 ? 0 : Math.min(100, ts - lastTickTs)
      lastTickTs = ts
      lastFrameTs = ts
      step(dtMs / 1000)
      render(ts / 1000)
    }

    function start() {
      resize()
      if (reduceMotionMq?.matches) return // 靜態：只畫一次（resize() 內已呼叫 render()），不啟動迴圈
      lastFrameTs = 0
      lastTickTs = 0
      rafId = requestAnimationFrame(loop)
    }

    function stop() {
      if (rafId != null) {
        cancelAnimationFrame(rafId)
        rafId = null
      }
    }

    function handleVisibility() {
      visible = document.visibilityState !== 'hidden'
      if (visible) {
        if (!reduceMotionMq?.matches && rafId == null) {
          lastFrameTs = 0
          lastTickTs = 0
          rafId = requestAnimationFrame(loop)
        }
      } else {
        stop()
      }
    }

    function handleReduceMotionChange() {
      stop()
      start()
    }

    start()
    window.addEventListener('resize', resize)
    document.addEventListener('visibilitychange', handleVisibility)
    reduceMotionMq?.addEventListener('change', handleReduceMotionChange)

    return () => {
      stop()
      window.removeEventListener('resize', resize)
      document.removeEventListener('visibilitychange', handleVisibility)
      reduceMotionMq?.removeEventListener('change', handleReduceMotionChange)
    }
  }, [])

  return (
    <canvas
      ref={canvasRef}
      aria-hidden="true"
      style={{
        position: 'fixed',
        inset: 0,
        zIndex: 0,
        pointerEvents: 'none',
        display: 'block',
      }}
    />
  )
}
