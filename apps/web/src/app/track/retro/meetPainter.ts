// 團練同步跑（P3）——retro（復古 RPG／像素風）風格的「他人亮點／泡泡／群聚徽章／自己綠環」長相。
// 邏輯在 ../meetCanvasPeers.ts；這裡全部用整數倍率（orbScale＝env.scale）的 fillRect，不用抗鋸齒圓弧、不用漸層（呼叫端 renderFrame
// 已設 imageSmoothingEnabled=false）：
//   · 他人＝像素方塊 #f8a000＋黑框（7×7 邏輯像素，放大 orbScale 倍），與自己的 13×13 圓形光球（橘紅 #e45c10 外圈）形狀、尺寸、色相都不同；
//     陳舊 gray＝灰色空心方環。
//   · 泡泡＝黑框＋橘色內框＋白底的缺角像素方塊，三階像素小三角指向亮點；文字黑色（對比 21:1）。
//   · 自己＝既有光球不動，外加 #46E3A0 像素環（半徑＝光球半徑＋4、粗＝1 邏輯像素≥2 px），外圍黑色描邊（草地底圖上才看得見）。
// 字型：Cubic 11（DORPixel，retro 已載入的像素中文字型）→ Noto Sans TC → sans-serif；DORPixel 缺字時由系統字型逐字補上，不會出豆腐塊
// （E2E 以「不同 CJK 字的像素不同」驗證）。
//
// 透明（fade 60% 透明）：像素圖由黑框／橘框／白底多層疊成，直接設 globalAlpha 會讓重疊處的不透明度累加（三層疊起來 ≈78%），
// 所以亮點先畫進 sprite、泡泡先畫進共用的離屏 scratch，再以單一 globalAlpha 貼回——整顆一致地只有 40% 不透明。

import { MEET_GRAY } from '../meetLiveUtil'
import { SELF_RING_GREEN, getSprite, type BubbleDraw, type BubbleMetrics, type DotPaint, type PeerEnv, type PeerPainter } from '../meetCanvasPeers'

export const RETRO_PEER_ORANGE = '#f8a000'
const BLACK = '#000000'
const WHITE = '#fcfcfc'
const PALE = '#fcd8a8' // RETRO_PALETTE 淡黃（高光）
const SHADE = '#e45c10' // RETRO_PALETTE 橘（底部陰影一列）
const FONT = '12px "DORPixel","Noto Sans TC","Microsoft JhengHei",sans-serif'

/** n×n 邏輯像素的方形邊框（厚 t）——四條 fillRect。 */
function ring(ctx: CanvasRenderingContext2D, ox: number, oy: number, n: number, t: number) {
  ctx.fillRect(ox, oy, n * t, t)
  ctx.fillRect(ox, oy + (n - 1) * t, n * t, t)
  ctx.fillRect(ox, oy + t, t, (n - 2) * t)
  ctx.fillRect(ox + (n - 1) * t, oy + t, t, (n - 2) * t)
}

/** 缺角像素方塊（四個角各少一格），w／h 皆為 t 的整數倍。 */
function notchedRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, t: number) {
  ctx.fillRect(x + t, y, w - 2 * t, h)
  ctx.fillRect(x, y + t, w, h - 2 * t)
}

/**
 * 三階像素小三角（尖端朝本地 +y）。本地原點＝箱邊中心；呼叫端以 translate／rotate(±90°)（座標皆整數、旋轉 90° 的倍數，像素不會糊）擺到四個方向。
 * 先把箱邊的兩層框（黑＋橘）在三角寬度內蓋成白色，讓箱內與三角相通。
 */
function stairTail(ctx: CanvasRenderingContext2D, t: number) {
  ctx.fillStyle = BLACK
  ctx.fillRect(-2.5 * t, 0, 5 * t, t)
  ctx.fillRect(-1.5 * t, t, 3 * t, t)
  ctx.fillRect(-0.5 * t, 2 * t, t, t)
  ctx.fillStyle = WHITE
  ctx.fillRect(-1.5 * t, 0, 3 * t, t) // 第 0 列內部
  ctx.fillRect(-0.5 * t, t, t, t) // 第 1 列內部
  ctx.fillRect(-1.5 * t, -2 * t, 3 * t, 2 * t) // 蓋掉箱邊的黑框＋橘框（讓箱內與三角相通）
}

/** 7×7 亮點（ok／fade）或空心方環（gray），畫在 (ox,oy) 起的 7s×7s 範圍；全部不透明，透明度交給呼叫端一次套用。 */
function paintDot(g: CanvasRenderingContext2D, ox: number, oy: number, s: number, gray: boolean) {
  if (gray) {
    g.fillStyle = BLACK; ring(g, ox, oy, 7, s) // 外黑框
    g.fillStyle = MEET_GRAY; ring(g, ox + s, oy + s, 5, s) // 灰色空心環
    g.fillStyle = BLACK; ring(g, ox + 2 * s, oy + 2 * s, 3, s) // 內黑框（中心 1 格透出地圖）
    return
  }
  g.fillStyle = BLACK; ring(g, ox, oy, 7, s) // 黑框（四條邊，不與內部重疊）
  g.fillStyle = RETRO_PEER_ORANGE; g.fillRect(ox + s, oy + s, 5 * s, 4 * s) // 實心 #f8a000（上 4 列）
  g.fillStyle = SHADE; g.fillRect(ox + s, oy + 5 * s, 5 * s, s) // 底部一列陰影
  g.fillStyle = PALE; g.fillRect(ox + 2 * s, oy + 2 * s, s, s) // 左上高光（遠離圓心，中心像素維持 #f8a000）
}

/** 泡泡本體＋三角＋（疊一層時的）引線＋文字，全部不透明；座標為絕對 CSS px（呼叫端決定 ctx 的 transform）。 */
function paintBubble(ctx: CanvasRenderingContext2D, t: number, b: BubbleDraw) {
  const { x, y, w, h } = b
  const gray = b.level === 'gray'
  // 箱體：黑框＋橘色內框（gray＝灰色內框）＋白底，三層皆缺角（階梯狀圓角）
  ctx.fillStyle = BLACK; notchedRect(ctx, x, y, w, h, t)
  ctx.fillStyle = gray ? MEET_GRAY : RETRO_PEER_ORANGE; notchedRect(ctx, x + t, y + t, w - 2 * t, h - 2 * t, t)
  ctx.fillStyle = WHITE; notchedRect(ctx, x + 2 * t, y + 2 * t, w - 4 * t, h - 4 * t, t)
  // 三階像素小三角（箱體之後畫，才蓋得住接縫處的框線）：above＝底邊朝下；right＝左邊緣朝左；left＝右邊緣朝右
  const c = Math.round((b.base - 0.5 * t) / t) * t + 0.5 * t // 三角中心落在邏輯像素正中央
  ctx.save()
  if (b.side === 'above') ctx.translate(c, y + h)
  else if (b.side === 'right') { ctx.translate(x, c); ctx.rotate(Math.PI / 2) }
  else { ctx.translate(x + w, c); ctx.rotate(-Math.PI / 2) }
  stairTail(ctx, t)
  ctx.restore()
  if (b.leader) { // 疊一層：三角尖端到亮點上緣的點狀引線
    ctx.fillStyle = BLACK
    const lx = Math.round(c - 0.5 * t)
    for (let yy = y + h + 3 * t; yy < b.leadY - t; yy += 2 * t) ctx.fillRect(lx, Math.round(yy), t, t)
  }
  ctx.fillStyle = BLACK
  ctx.font = FONT
  ctx.textAlign = 'left'
  ctx.textBaseline = 'middle'
  ctx.fillText(b.text, Math.round(x + 4 * t), Math.round(y + h / 2) + 1)
}

// 半透明泡泡共用的離屏畫布（只有 fade／gray 的泡泡會用到，每幀至多幾個）
let scratch: HTMLCanvasElement | null = null
function getScratch(cssW: number, cssH: number, dpr: number): HTMLCanvasElement | null {
  try {
    if (typeof document === 'undefined') return null
    const W = Math.ceil(cssW * dpr), H = Math.ceil(cssH * dpr)
    if (!scratch) scratch = document.createElement('canvas')
    if (scratch.width < W) scratch.width = W
    if (scratch.height < H) scratch.height = H
    return scratch
  } catch { return null }
}

export const retroPeerPainter: PeerPainter = {
  id: 'retro',
  color: RETRO_PEER_ORANGE,
  font: FONT,
  snapToScale: true,
  dotRadius: (s) => 3.5 * s,
  clusterRadius: (s) => 4.5 * s,
  // 缺角方塊徽章實際寬 ceil((文字+6s)/s)*s、高 9s；保守估計：避開保留區用
  clusterBox: (s, n) => ({ w: s * (6 + 4 * (String(n).length + 1)), h: 9 * s + 2 }),
  selfOrbRadius: (s) => 6.5 * s, // 13×13 光球
  bubbleMetrics: (s): BubbleMetrics => ({ padX: 4 * s, padY: 3 * s, textH: 12, tail: 3 * s, gap: s }),

  drawDot(ctx: CanvasRenderingContext2D, env: PeerEnv, x: number, y: number, p: DotPaint) {
    const s = env.scale
    const gray = p.level === 'gray'
    const ox = Math.round(x - 3.5 * s), oy = Math.round(y - 3.5 * s)
    const size = 7 * s
    const sp = getSprite(`retro-peer-${gray ? 'gray' : 'ok'}|${s}`, size, size, env.dpr, (g) => paintDot(g, 0, 0, s, gray))
    ctx.globalAlpha = gray ? 0.95 : p.alpha
    if (sp) ctx.drawImage(sp, ox, oy, size, size)
    else paintDot(ctx, ox, oy, s, gray)
    ctx.globalAlpha = 1
  },

  drawBubble(ctx: CanvasRenderingContext2D, env: PeerEnv, b: BubbleDraw) {
    const t = env.scale
    if (b.alpha >= 1) { paintBubble(ctx, t, b); return }
    // 半透明：先以不透明畫進離屏（含文字），再以單一 globalAlpha 整顆貼回
    const rx = b.x - 4 * t, ry = b.y - 4 * t, rw = b.w + 8 * t
    const rh = Math.max(b.h + 8 * t, b.leader ? b.leadY + t - ry : 0)
    const scr = getScratch(rw, rh, env.dpr)
    const g = scr?.getContext('2d')
    if (!scr || !g) { ctx.globalAlpha = b.alpha; paintBubble(ctx, t, b); ctx.globalAlpha = 1; return }
    g.setTransform(1, 0, 0, 1, 0, 0)
    g.clearRect(0, 0, scr.width, scr.height)
    g.setTransform(env.dpr, 0, 0, env.dpr, -rx * env.dpr, -ry * env.dpr)
    paintBubble(g, t, b)
    ctx.globalAlpha = b.alpha
    ctx.drawImage(scr, 0, 0, Math.ceil(rw * env.dpr), Math.ceil(rh * env.dpr), rx, ry, rw, rh)
    ctx.globalAlpha = 1
  },

  drawCluster(ctx: CanvasRenderingContext2D, env: PeerEnv, x: number, y: number, count: number) {
    const t = env.scale
    const label = `+${count}`
    ctx.font = FONT
    const tw = ctx.measureText(label).width
    const w = Math.ceil((tw + 6 * t) / t) * t, h = 9 * t
    const bx = Math.round(x - w / 2), by = Math.round(y - h / 2)
    ctx.fillStyle = BLACK; notchedRect(ctx, bx, by, w, h, t)
    ctx.fillStyle = RETRO_PEER_ORANGE; notchedRect(ctx, bx + t, by + t, w - 2 * t, h - 2 * t, t)
    ctx.fillStyle = BLACK
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    ctx.fillText(label, Math.round(x), Math.round(y) + 1)
  },

  drawSelfRing(ctx: CanvasRenderingContext2D, env: PeerEnv, x: number, y: number) {
    const s = env.scale
    const R = 6.5 * s + 4 // 光球半徑＋4（契約 §7）
    const size = Math.ceil((R + 2.5 * s) * 2)
    const sp = getSprite(`retro-selfring|${s}`, size, size, env.dpr, (g) => {
      const c = size / 2
      const n = Math.ceil((2 * Math.PI * R) / (s * 0.5))
      const put = (rad: number, color: string) => {
        g.fillStyle = color
        for (let i = 0; i < n; i++) {
          const a = (i / n) * Math.PI * 2
          g.fillRect(Math.round(c + Math.cos(a) * rad - s / 2), Math.round(c + Math.sin(a) * rad - s / 2), s, s)
        }
      }
      put(R + s, BLACK) // 外描邊
      put(R - s, BLACK) // 內描邊
      put(R, SELF_RING_GREEN) // 本體
    })
    if (sp) ctx.drawImage(sp, Math.round(x - size / 2), Math.round(y - size / 2), size, size)
  },
}
