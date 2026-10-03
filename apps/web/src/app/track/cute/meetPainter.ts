// 團練同步跑（P3）——cute（溫馨可愛）風格的「他人亮點／泡泡／群聚徽章／自己綠環」長相。
// 邏輯在 ../meetCanvasPeers.ts；這裡只決定怎麼畫（契約 docs/runmeet/GROUP_RUN_LIVE_CONTRACT.md §7）：
//   · 他人＝柔和實心橘點 #FF8A3D＋白色 2 px 外框＋淡淡投影；形狀（實心點）與自己（粉色光球外加綠細環）不同。
//   · 泡泡＝奶油白圓角矩形＋橘色 2 px 邊＋向下小三角＋柔和投影；文字深棕（對比 ≈10:1）。fade 60% 透明、gray 灰邊「訊號中斷」。
//   · 自己＝既有粉色光球不動，外加 #46E3A0 細環（r+4、2 px），底襯白色細圈讓它在淺色底圖上也看得見。
// 字型：DORCute（cute 已載入）→ Noto Sans TC → sans-serif（與 cute/icons.ts 同一串）。

import { MEET_GRAY } from '../meetLiveUtil'
import { SELF_RING_GREEN, getSprite, snapDev, traceBubblePath, type BubbleDraw, type BubbleMetrics, type DotPaint, type PeerEnv, type PeerPainter } from '../meetCanvasPeers'

export const CUTE_PEER_ORANGE = '#FF8A3D'
const BUBBLE_FONT = '700 12px "DORCute","Noto Sans TC","Microsoft JhengHei",sans-serif'
const CLUSTER_FONT = '700 11px "DORCute","Noto Sans TC","Microsoft JhengHei",sans-serif'
const CREAM = '#FFFDF8'
const INK = '#5A3418' // 深棕文字（在奶油白上對比 ≈10:1）
const INK_ON_ORANGE = '#4A2610' // 橘底上的深棕（對比 ≈5.7:1；白字在 #FF8A3D 只有 ≈2.3:1，所以不用白字）
const SP = 40
const C = SP / 2

function dotSprite(dpr: number) {
  return getSprite('cute-peer-dot', SP, SP, dpr, (g) => {
    g.shadowColor = 'rgba(140,70,20,0.28)'; g.shadowBlur = 4; g.shadowOffsetY = 1.5
    g.beginPath(); g.arc(C, C, 8, 0, Math.PI * 2); g.fillStyle = '#ffffff'; g.fill() // 白色 2 px 外框（外徑 8、內徑 6）
    g.shadowColor = 'transparent'; g.shadowBlur = 0; g.shadowOffsetY = 0
    g.beginPath(); g.arc(C, C, 6, 0, Math.PI * 2); g.fillStyle = CUTE_PEER_ORANGE; g.fill()
    const hl = g.createRadialGradient(C - 2, C - 2, 0, C - 2, C - 2, 3.2) // 左上柔和高光（遠離圓心）
    hl.addColorStop(0, 'rgba(255,255,255,0.55)'); hl.addColorStop(1, 'rgba(255,255,255,0)')
    g.fillStyle = hl; g.beginPath(); g.arc(C - 2, C - 2, 3.2, 0, Math.PI * 2); g.fill()
  })
}
function grayRingSprite(dpr: number) {
  return getSprite('cute-peer-gray', SP, SP, dpr, (g) => {
    g.beginPath(); g.arc(C, C, 8.6, 0, Math.PI * 2); g.lineWidth = 1.2; g.strokeStyle = 'rgba(255,255,255,0.85)'; g.stroke() // 外側白邊：淺色底圖上也看得見
    g.beginPath(); g.arc(C, C, 6.5, 0, Math.PI * 2); g.lineWidth = 2.5; g.strokeStyle = MEET_GRAY; g.stroke() // 灰色空心環
  })
}

// 每顆亮點每幀都要取 sprite：同一個 dpr 下直接用快取的引用，不必每次組字串查 Map。
let spDpr = -1
let spDot: HTMLCanvasElement | null = null, spGray: HTMLCanvasElement | null = null
function sprites(dpr: number) {
  if (dpr !== spDpr || (!spDot && !spGray)) { spDot = dotSprite(dpr); spGray = grayRingSprite(dpr); spDpr = dpr }
  return { dot: spDot, ring: spGray }
}

export const cutePeerPainter: PeerPainter = {
  id: 'cute',
  color: CUTE_PEER_ORANGE,
  font: BUBBLE_FONT,
  dotRadius: () => 9,
  clusterRadius: () => 13,
  // 膠囊徽章實際寬 max(26, 文字+14)、高 24；保守估計：避開保留區用
  clusterBox: (_s, n) => ({ w: 16 + 8 * (String(n).length + 1), h: 26 }),
  selfOrbRadius: () => 9, // 光球本體半徑 9（orb.ts ballR）
  bubbleMetrics: (): BubbleMetrics => ({ padX: 8, padY: 4, textH: 14, tail: 6, gap: 2 }),

  drawDot(ctx: CanvasRenderingContext2D, env: PeerEnv, x: number, y: number, p: DotPaint) {
    const dpr = env.dpr
    const ox = snapDev(x - C, dpr), oy = snapDev(y - C, dpr)
    const sp = sprites(dpr)
    if (p.level === 'gray') {
      const ring = sp.ring
      ctx.globalAlpha = 0.95
      if (ring) ctx.drawImage(ring, ox, oy, SP, SP)
      else { ctx.beginPath(); ctx.arc(x, y, 6.5, 0, Math.PI * 2); ctx.lineWidth = 2.5; ctx.strokeStyle = MEET_GRAY; ctx.stroke() }
      ctx.globalAlpha = 1
      return
    }
    ctx.globalAlpha = p.alpha
    const dot = sp.dot
    if (dot) ctx.drawImage(dot, ox, oy, SP, SP)
    else {
      ctx.beginPath(); ctx.arc(x, y, 8, 0, Math.PI * 2); ctx.fillStyle = '#ffffff'; ctx.fill()
      ctx.beginPath(); ctx.arc(x, y, 6, 0, Math.PI * 2); ctx.fillStyle = CUTE_PEER_ORANGE; ctx.fill()
    }
    ctx.globalAlpha = 1
  },

  drawBubble(ctx: CanvasRenderingContext2D, _env: PeerEnv, b: BubbleDraw) {
    const gray = b.level === 'gray'
    const edge = gray ? MEET_GRAY : CUTE_PEER_ORANGE
    ctx.globalAlpha = b.alpha
    traceBubblePath(ctx, b, 8, 4.5)
    ctx.fillStyle = CREAM
    ctx.shadowColor = gray ? 'rgba(80,90,100,0.25)' : 'rgba(255,138,61,0.35)'
    ctx.shadowBlur = 6
    ctx.shadowOffsetY = 2
    ctx.fill()
    ctx.shadowBlur = 0
    ctx.shadowOffsetY = 0
    ctx.shadowColor = 'transparent'
    ctx.lineWidth = 2
    ctx.lineJoin = 'round'
    ctx.strokeStyle = edge
    ctx.stroke()
    if (b.leader) {
      ctx.beginPath(); ctx.moveTo(b.tipX, b.tipY); ctx.lineTo(b.leadX, b.leadY)
      ctx.lineCap = 'round'; ctx.stroke(); ctx.lineCap = 'butt'
    }
    ctx.fillStyle = gray ? '#4a4f55' : INK
    ctx.font = BUBBLE_FONT
    ctx.textAlign = 'left'
    ctx.textBaseline = 'middle'
    ctx.fillText(b.text, b.x + 8, b.y + b.h / 2 + 0.5)
    ctx.globalAlpha = 1
  },

  drawCluster(ctx: CanvasRenderingContext2D, _env: PeerEnv, x: number, y: number, count: number) {
    const label = `+${count}`
    ctx.font = CLUSTER_FONT
    const tw = ctx.measureText(label).width
    const w = Math.max(26, Math.ceil(tw) + 14), h = 24
    const bx = x - w / 2, by = y - h / 2, r = h / 2
    ctx.beginPath()
    ctx.moveTo(bx + r, by); ctx.lineTo(bx + w - r, by); ctx.arc(bx + w - r, by + r, r, -Math.PI / 2, Math.PI / 2)
    ctx.lineTo(bx + r, by + h); ctx.arc(bx + r, by + r, r, Math.PI / 2, -Math.PI / 2)
    ctx.closePath()
    ctx.fillStyle = CUTE_PEER_ORANGE
    ctx.shadowColor = 'rgba(140,70,20,0.28)'
    ctx.shadowBlur = 4
    ctx.shadowOffsetY = 1.5
    ctx.fill()
    ctx.shadowBlur = 0
    ctx.shadowOffsetY = 0
    ctx.shadowColor = 'transparent'
    ctx.lineWidth = 2
    ctx.strokeStyle = '#ffffff'
    ctx.stroke()
    ctx.fillStyle = INK_ON_ORANGE
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    ctx.fillText(label, x, y + 0.5)
  },

  drawSelfRing(ctx: CanvasRenderingContext2D, _env: PeerEnv, x: number, y: number) {
    const r = 9 + 4 // 光球半徑＋4（契約 §7）
    ctx.beginPath(); ctx.arc(x, y, r, 0, Math.PI * 2)
    ctx.lineWidth = 4; ctx.strokeStyle = 'rgba(255,255,255,0.85)'; ctx.stroke() // 白色底襯：淺色／綠色底圖上也看得見
    ctx.beginPath(); ctx.arc(x, y, r, 0, Math.PI * 2)
    ctx.lineWidth = 2; ctx.strokeStyle = SELF_RING_GREEN; ctx.stroke()
  },
}
