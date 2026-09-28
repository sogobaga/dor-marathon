'use client'

// retro (復古 RPG) skin 全站背景（契約 §3）：低解析度 16×16 原創像素圖塊鋪滿整個視窗
// （點描草地為主、零星樹叢與小河，河面兩幀動畫每 500ms 切換一次），`image-rendering: pixelated`
// 放大顯示，疊在內容下方（z-index 0、pointer-events none）；整體再蓋一層 rgba(0,0,0,.45)
// 壓低亮度，避免搶走前景視窗文字的可讀性。
//
// 由 SkinOverride（另一位工人負責）在 [data-skin="retro"] 生效時才掛載本元件；本檔案本身不判斷
// 目前 skin 是否為 retro——只負責「被掛上後怎麼畫」。沒人 import 這支檔案（或走 dynamic import）
// 的話，非白名單使用者的 bundle 完全不受影響。
//
// 圖塊繪製邏輯全部委派給 tiles.ts 的 drawTile()／RETRO_PALETTE（與 GPS 地圖的 MAP 工人共用同一份
// 原創美術資料，避免兩處各刻一套、風格不一致）。
//
// 效能／無障礙原則：
//   - 背景 canvas 是「低解析度」的：backing store 只有 CSS 尺寸的 1/DISPLAY_SCALE，
//     靠 CSS 放大＋image-rendering:pixelated 呈現粗顆粒像素感，同時大幅降低繪製像素數。
//   - 動畫只有「水波兩幀切換」，用 500ms 的 setInterval 觸發整格重繪一次，不需要 rAF 迴圈
//     （沒有連續位移動畫），對背景分頁 CPU 幾乎零負擔。
//   - 分頁隱藏（visibilitychange）時停止 interval。
//   - prefers-reduced-motion 只畫一次靜態（frame 0），不啟動 interval。
//   - resize 時重新計算網格並重繪一次。
//
// 無 props：呼叫端只需要 <RetroBackground />（比照 ParticleField 的呼叫慣例）。
import { useEffect, useRef } from 'react'
import { drawTile, type TileKind } from './tiles'
import { loadRetroFont } from './fonts'

const TILE_PX = 16 // tiles.ts 圖塊原生尺寸（世界像素，非 CSS px）
const DISPLAY_SCALE = 2 // 1 個世界像素 = 2 個 CSS px（backing canvas 解析度 = CSS 尺寸 / 2）
const WATER_FRAME_INTERVAL_MS = 500 // 契約 §3：河面兩幀動畫 500ms 切換
const OVERLAY_ALPHA = 0.5 // 契約 §3（第三輪）：整體壓暗改 .5，避免搶走前景羊皮紙文字

type Cell = { kind: TileKind }

// 決定性偽隨機（同一座標永遠得到同一個值），不需要额外的 seed 状态、resize 后网格也不会「重洗」。
function hash2(x: number, y: number): number {
  const v = Math.sin(x * 12.9898 + y * 78.233) * 43758.5453
  return v - Math.floor(v)
}

// 小河：沿 row 用正弦波左右蜿蜒，寬度 2 格；不在河道上的格子才有機會長樹叢。
function buildGrid(cols: number, rows: number): Cell[][] {
  const grid: Cell[][] = []
  const riverBaseCol = Math.floor(cols * 0.62)
  for (let row = 0; row < rows; row++) {
    const line: Cell[] = []
    const riverCol = riverBaseCol + Math.round(Math.sin(row * 0.22) * (cols * 0.12))
    for (let col = 0; col < cols; col++) {
      const isRiver = col === riverCol || col === riverCol + 1
      if (isRiver) {
        line.push({ kind: 'water' })
        continue
      }
      const forestChance = hash2(col, row)
      // 森林零星分布（約 6%），且避免緊貼河道（视觉上更像「疏落樹叢」而非整片森林）。
      const nearRiver = Math.abs(col - riverCol) <= 1
      if (!nearRiver && forestChance > 0.94) {
        line.push({ kind: 'forest' })
      } else {
        line.push({ kind: 'grass' })
      }
    }
    grid.push(line)
  }
  return grid
}

export default function RetroBackground() {
  const canvasRef = useRef<HTMLCanvasElement | null>(null)

  useEffect(() => {
    loadRetroFont() // 契約 §3：只在 retro 生效（本元件掛載）時才載入 Cubic 11

    const canvas = canvasRef.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return // 極舊瀏覽器沒有 2D context：靜默不畫，本層本來就 pointer-events:none

    let grid: Cell[][] = []
    let cols = 0
    let rows = 0
    let backingW = 0
    let backingH = 0
    let frame: 0 | 1 = 0
    let intervalId: ReturnType<typeof setInterval> | null = null
    let visible = typeof document === 'undefined' ? true : document.visibilityState !== 'hidden'

    const reduceMotionMq = typeof window !== 'undefined' ? window.matchMedia('(prefers-reduced-motion: reduce)') : null

    function render() {
      if (!ctx || !canvas) return
      for (let row = 0; row < rows; row++) {
        for (let col = 0; col < cols; col++) {
          ctx.save()
          ctx.translate(col * TILE_PX, row * TILE_PX)
          drawTile(ctx, grid[row][col].kind, grid[row][col].kind === 'water' ? frame : 0)
          ctx.restore()
        }
      }
      // 壓暗整體亮度，避免像素背景搶走前景卡片／文字的可讀性。
      ctx.fillStyle = `rgba(0,0,0,${OVERLAY_ALPHA})`
      ctx.fillRect(0, 0, backingW, backingH)
    }

    function resize() {
      if (!canvas) return
      const cssW = window.innerWidth
      const cssH = window.innerHeight
      backingW = Math.ceil(cssW / DISPLAY_SCALE)
      backingH = Math.ceil(cssH / DISPLAY_SCALE)
      canvas.width = backingW
      canvas.height = backingH
      canvas.style.width = cssW + 'px'
      canvas.style.height = cssH + 'px'
      cols = Math.ceil(backingW / TILE_PX) + 1
      rows = Math.ceil(backingH / TILE_PX) + 1
      grid = buildGrid(cols, rows)
      render()
    }

    function tick() {
      if (!visible) return
      frame = frame === 0 ? 1 : 0
      render()
    }

    function start() {
      resize()
      if (reduceMotionMq?.matches) return // 靜態：只畫一次（resize() 內已呼叫 render()），不啟動 interval
      intervalId = setInterval(tick, WATER_FRAME_INTERVAL_MS)
    }

    function stop() {
      if (intervalId != null) {
        clearInterval(intervalId)
        intervalId = null
      }
    }

    function handleVisibility() {
      visible = document.visibilityState !== 'hidden'
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
        imageRendering: 'pixelated',
      }}
    />
  )
}
