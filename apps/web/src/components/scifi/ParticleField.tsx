'use client'

// scifi skin 全站背景（契約 §3）：深空漸層 + 緩慢漂移星塵粒子 + 極淡透視網格。
// 由 SkinOverride（另一位工人負責）在 [data-skin="scifi"] 生效時才掛載本元件；本檔案本身
// 不判斷/不讀取目前 skin 是否為 scifi——它只負責「被掛上後怎麼畫」，掛不掛由呼叫端決定，
// 這樣非白名單使用者的 bundle 只要沒人 import 這支檔案（或走 dynamic import），就完全不受影響。
//
// 效能／無障礙原則（契約 §3 逐字要求）：
//   - 手機（寬 ≤600px）粒子數上限 90，桌機上限 160。
//   - 分頁隱藏（document.visibilitychange）時停止動畫迴圈，不佔背景 CPU。
//   - prefers-reduced-motion 只畫一張靜態畫面（漸層+網格+粒子初始定位），不跑 requestAnimationFrame。
//   - 幀率上限 30fps：rAF 迴圈內用時間戳節流，未到間隔就直接排下一幀、不重繪。
//   - pointer-events:none、z-index:0、position:fixed 鋪滿視窗，蓋在內容之下，不擋任何互動。
//
// 無 props：呼叫端只需要 <ParticleField />，不必配置任何參數（契約：ParticleField.tsx default export 無 props）。
import { useEffect, useRef } from 'react'

const FRAME_INTERVAL_MS = 1000 / 30 // 30fps 上限
const MOBILE_MAX_PARTICLES = 120 // CONTRACT_R2.md §5
const DESKTOP_MAX_PARTICLES = 180
const MOBILE_WIDTH_BREAKPOINT = 600 // 與全站 useIsMobile 的判斷方向一致，但本元件只是抓個粒子預算，故用單純寬度判斷即可，不需要跟全站定義逐位元同步

type Particle = {
  x: number // 0..1，相對畫布寬度的比例座標（resize 時不必重算位置）
  y: number // 0..1
  r: number // 半徑（px）
  baseAlpha: number
  vx: number // 每毫秒漂移速度（比例座標）
  vy: number
  phase: number // 呼吸縮放用的相位
  speed: number // 呼吸速度
}

function makeParticles(count: number): Particle[] {
  const list: Particle[] = []
  for (let i = 0; i < count; i++) {
    list.push({
      x: Math.random(),
      y: Math.random(),
      r: 1 + Math.random() * 1.4, // 大小 1–2.4px（CONTRACT_R2.md §5）
      baseAlpha: 0.25 + Math.random() * 0.55,
      // 緩慢漂移：整個畫面跑完一輪要數分鐘，才叫「星塵」而非「下雨」
      vx: (Math.random() - 0.5) * 0.000012,
      vy: (Math.random() - 0.5) * 0.000012 - 0.000004, // 略偏向上飄，呼應「探索深空」的感覺
      phase: Math.random() * Math.PI * 2,
      speed: 0.0006 + Math.random() * 0.0009,
    })
  }
  return list
}

export default function ParticleField() {
  const canvasRef = useRef<HTMLCanvasElement | null>(null)

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return // 極舊瀏覽器沒有 2D context：靜默不畫，不影響底下內容（本層本來就 pointer-events:none）

    let particles: Particle[] = []
    let width = 0
    let height = 0
    let dpr = 1
    let rafId: number | null = null
    let lastFrameAt = 0
    let visible = typeof document === 'undefined' ? true : document.visibilityState !== 'hidden'

    const reduceMotionMq = typeof window !== 'undefined' ? window.matchMedia('(prefers-reduced-motion: reduce)') : null
    const mobileMq = typeof window !== 'undefined' ? window.matchMedia(`(max-width: ${MOBILE_WIDTH_BREAKPOINT}px)`) : null

    function particleBudget(): number {
      return mobileMq?.matches ? MOBILE_MAX_PARTICLES : DESKTOP_MAX_PARTICLES
    }

    function resize() {
      if (!canvas) return
      width = window.innerWidth
      height = window.innerHeight
      dpr = Math.min(window.devicePixelRatio || 1, 2) // 上限 2x，避免高解析度螢幕把 canvas 畫布撐到過大拖累效能
      canvas.width = Math.round(width * dpr)
      canvas.height = Math.round(height * dpr)
      canvas.style.width = width + 'px'
      canvas.style.height = height + 'px'
    }

    function ensureParticleCount() {
      const target = particleBudget()
      if (particles.length === target) return
      particles = makeParticles(target)
    }

    // 深空漸層：左上偏冷藍紫、中心偏深藍、四周墜入接近純黑，呼應參考圖「深藍夜色」氛圍。
    function drawBackground() {
      if (!ctx) return
      const g = ctx.createRadialGradient(width * dpr * 0.5, height * dpr * 0.32, 0, width * dpr * 0.5, height * dpr * 0.32, Math.max(width, height) * dpr * 0.85)
      g.addColorStop(0, '#0a1830')
      g.addColorStop(0.45, '#050b18')
      g.addColorStop(1, '#02040a')
      ctx.fillStyle = g
      ctx.fillRect(0, 0, canvas!.width, canvas!.height)
    }

    // 極淡透視網格：地平線在畫面中段偏下，橫線隨距離變密、直線向消失點收斂——營造「未知領域」的縱深感。
    function drawGrid() {
      if (!ctx) return
      const horizonY = height * dpr * 0.62
      const vanishX = width * dpr * 0.5
      ctx.save()
      ctx.strokeStyle = 'rgba(53,230,255,.06)'
      ctx.lineWidth = 1
      // 橫線：越靠近地平線越密（模擬透視），數量固定 7 條，肉眼幾乎只留下氛圍感
      const rows = 7
      for (let i = 1; i <= rows; i++) {
        const t = i / rows
        const y = horizonY + (height * dpr - horizonY) * (t * t) // 二次曲線分佈，靠近底部才密
        ctx.globalAlpha = 0.5 * (1 - t) + 0.08
        ctx.beginPath()
        ctx.moveTo(0, y)
        ctx.lineTo(width * dpr, y)
        ctx.stroke()
      }
      // 直線：從底部等距分佈的多個點收斂到 vanishX
      const cols = 9
      ctx.globalAlpha = 0.14
      for (let i = 0; i <= cols; i++) {
        const bottomX = (width * dpr * i) / cols
        ctx.beginPath()
        ctx.moveTo(bottomX, height * dpr)
        ctx.lineTo(vanishX, horizonY)
        ctx.stroke()
      }
      ctx.restore()
    }

    function drawParticles(tMs: number, animate: boolean) {
      if (!ctx) return
      for (const p of particles) {
        if (animate) {
          p.x += p.vx * FRAME_INTERVAL_MS
          p.y += p.vy * FRAME_INTERVAL_MS
          // 環繞包邊：飄出畫面就從另一側回來，星塵永遠均勻分佈，不會愈飄愈少
          if (p.x < -0.02) p.x = 1.02
          if (p.x > 1.02) p.x = -0.02
          if (p.y < -0.02) p.y = 1.02
          if (p.y > 1.02) p.y = -0.02
        }
        const breathe = animate ? 0.75 + 0.25 * Math.sin(p.phase + tMs * p.speed) : 1
        const alpha = p.baseAlpha * breathe
        const px = p.x * width * dpr
        const py = p.y * height * dpr
        const r = p.r * dpr * breathe
        ctx.beginPath()
        ctx.fillStyle = `rgba(180,236,255,${alpha.toFixed(3)})`
        ctx.arc(px, py, Math.max(0.4, r), 0, Math.PI * 2)
        ctx.fill()
      }
    }

    function renderFrame(tMs: number, animate: boolean) {
      drawBackground()
      drawGrid()
      drawParticles(tMs, animate)
    }

    function loop(t: number) {
      rafId = requestAnimationFrame(loop)
      if (!visible) return // 分頁隱藏時完全不繪製，省電
      if (t - lastFrameAt < FRAME_INTERVAL_MS) return // 30fps 節流：未到間隔就跳過這幀
      lastFrameAt = t
      renderFrame(t, true)
    }

    function start() {
      ensureParticleCount()
      resize()
      if (reduceMotionMq?.matches) {
        renderFrame(0, false) // 只畫一次靜態畫面，不進 rAF 迴圈
        return
      }
      lastFrameAt = 0
      rafId = requestAnimationFrame(loop)
    }

    function stop() {
      if (rafId != null) {
        cancelAnimationFrame(rafId)
        rafId = null
      }
    }

    function handleResize() {
      resize()
      // 靜態模式下 resize 後要重畫一次，否則畫布被 reset 成透明
      if (reduceMotionMq?.matches) renderFrame(0, false)
    }

    function handleVisibility() {
      visible = document.visibilityState !== 'hidden'
    }

    function handleReduceMotionChange() {
      // 執行期切換 OS 減少動態設定：重啟一次迴圈，切到對應模式
      stop()
      start()
    }

    function handleMobileChange() {
      ensureParticleCount()
    }

    resize()
    start()
    window.addEventListener('resize', handleResize)
    document.addEventListener('visibilitychange', handleVisibility)
    reduceMotionMq?.addEventListener('change', handleReduceMotionChange)
    mobileMq?.addEventListener('change', handleMobileChange)

    return () => {
      stop()
      window.removeEventListener('resize', handleResize)
      document.removeEventListener('visibilitychange', handleVisibility)
      reduceMotionMq?.removeEventListener('change', handleReduceMotionChange)
      mobileMq?.removeEventListener('change', handleMobileChange)
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
