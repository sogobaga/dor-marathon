// 團練同步跑（P3）——scifi（未來科幻）風格的「他人亮點／泡泡／群聚徽章／自己綠環」長相。
// 邏輯（內插／避碰／群聚／debug）全在 ../meetCanvasPeers.ts；這裡只決定怎麼畫（契約 docs/runmeet/GROUP_RUN_LIVE_CONTRACT.md §7）：
//   · 他人＝實心霓虹橘點＋白色 2 px 外框，外加 `lighter`（加法混合）發光；形狀（實心點）與自己（雙環）不同，色弱可分。
//     霓虹橘 #FF6A20（偏紅橘）與已規劃路線的琥珀虛線（#ffb020）、青色軌跡明確區隔；虛線 vs 實心點、粗細也不同。
//   · 泡泡＝深色玻璃圓角矩形＋橘色細框＋向下小三角；文字白色（對比 ≥ 15:1）。fade 60% 透明、gray 灰框「訊號中斷」。
//   · 自己＝既有光球不動，外加 #46E3A0 細環（r+4、2 px）。

import { MEET_GRAY } from '../meetLiveUtil'
import { SELF_RING_GREEN, getSprite, snapDev, traceBubblePath, type BubbleDraw, type BubbleMetrics, type DotPaint, type PeerEnv, type PeerPainter } from '../meetCanvasPeers'
import { orbitron } from './font'

export const SCIFI_PEER_NEON = '#FF6A20'
const BUBBLE_FONT = "700 12px 'Noto Sans TC','Microsoft JhengHei','PingFang TC',sans-serif"
const BUBBLE_FILL = 'rgba(6,14,30,0.92)'
const SP = 48 // sprite 邊長（CSS px），亮點置中
const C = SP / 2

function glowSprite(dpr: number) {
  return getSprite('scifi-peer-glow', SP, SP, dpr, (g) => {
    const grd = g.createRadialGradient(C, C, 2, C, C, 22)
    grd.addColorStop(0, 'rgba(255,106,32,0.62)')
    grd.addColorStop(0.45, 'rgba(255,106,32,0.22)')
    grd.addColorStop(1, 'rgba(255,106,32,0)')
    g.fillStyle = grd
    g.fillRect(0, 0, SP, SP)
  })
}
function coreSprite(dpr: number) {
  return getSprite('scifi-peer-core', SP, SP, dpr, (g) => {
    g.beginPath(); g.arc(C, C, 8, 0, Math.PI * 2); g.fillStyle = '#ffffff'; g.fill() // 白色 2 px 外框（外徑 8、內徑 6）
    g.beginPath(); g.arc(C, C, 6, 0, Math.PI * 2); g.fillStyle = SCIFI_PEER_NEON; g.fill()
    const hl = g.createRadialGradient(C - 2, C - 2, 0, C - 2, C - 2, 3) // 左上小高光（遠離圓心，圓心像素維持純橘）
    hl.addColorStop(0, 'rgba(255,255,255,0.5)'); hl.addColorStop(1, 'rgba(255,255,255,0)')
    g.fillStyle = hl; g.beginPath(); g.arc(C - 2, C - 2, 3, 0, Math.PI * 2); g.fill()
  })
}
function grayRingSprite(dpr: number) {
  return getSprite('scifi-peer-gray', SP, SP, dpr, (g) => {
    g.beginPath(); g.arc(C, C, 8.4, 0, Math.PI * 2); g.lineWidth = 1; g.strokeStyle = 'rgba(255,255,255,0.55)'; g.stroke() // 外側淡白邊：深淺底圖都看得見
    g.beginPath(); g.arc(C, C, 6.5, 0, Math.PI * 2); g.lineWidth = 2.5; g.strokeStyle = MEET_GRAY; g.stroke() // 灰色空心環
  })
}

// 每顆亮點每幀都要取 sprite：同一個 dpr 下直接用快取的引用，不必每次組字串查 Map。
let spDpr = -1
let spGlow: HTMLCanvasElement | null = null, spCore: HTMLCanvasElement | null = null, spGray: HTMLCanvasElement | null = null
function sprites(dpr: number) {
  if (dpr !== spDpr || (!spGlow && !spCore && !spGray)) { spGlow = glowSprite(dpr); spCore = coreSprite(dpr); spGray = grayRingSprite(dpr); spDpr = dpr }
  return { glow: spGlow, core: spCore, ring: spGray }
}

export const scifiPeerPainter: PeerPainter = {
  id: 'scifi',
  color: SCIFI_PEER_NEON,
  font: BUBBLE_FONT,
  dotRadius: () => 9,
  clusterRadius: () => 14,
  // 膠囊徽章實際寬 max(26, 文字+12)、高 22；保守估計（Orbitron 粗體 11px 每字約 8 px）：避開保留區用
  clusterBox: (_s, n) => ({ w: 14 + 8 * (String(n).length + 1), h: 24 }),
  selfOrbRadius: () => 10,
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
    const glow = sp.glow, core = sp.core
    if (glow) {
      ctx.globalCompositeOperation = 'lighter' // 霓虹發光＝加法混合
      ctx.drawImage(glow, ox, oy, SP, SP)
      ctx.globalCompositeOperation = 'source-over'
    }
    if (core) ctx.drawImage(core, ox, oy, SP, SP)
    else {
      ctx.beginPath(); ctx.arc(x, y, 8, 0, Math.PI * 2); ctx.fillStyle = '#ffffff'; ctx.fill()
      ctx.beginPath(); ctx.arc(x, y, 6, 0, Math.PI * 2); ctx.fillStyle = SCIFI_PEER_NEON; ctx.fill()
    }
    ctx.globalAlpha = 1
  },

  drawBubble(ctx: CanvasRenderingContext2D, _env: PeerEnv, b: BubbleDraw) {
    const gray = b.level === 'gray'
    ctx.globalAlpha = b.alpha
    traceBubblePath(ctx, b, 7, 4.5)
    ctx.fillStyle = BUBBLE_FILL
    ctx.shadowColor = gray ? 'rgba(154,160,166,0.55)' : 'rgba(255,106,32,0.7)'
    ctx.shadowBlur = 6
    ctx.fill()
    ctx.shadowBlur = 0
    ctx.shadowColor = 'transparent'
    ctx.lineWidth = 1.5
    ctx.strokeStyle = gray ? MEET_GRAY : SCIFI_PEER_NEON
    ctx.stroke()
    if (b.leader) {
      ctx.beginPath(); ctx.moveTo(b.tipX, b.tipY); ctx.lineTo(b.leadX, b.leadY); ctx.stroke()
    }
    ctx.fillStyle = '#ffffff'
    ctx.font = BUBBLE_FONT
    ctx.textAlign = 'left'
    ctx.textBaseline = 'middle'
    ctx.fillText(b.text, b.x + 8, b.y + b.h / 2 + 0.5)
    ctx.globalAlpha = 1
  },

  drawCluster(ctx: CanvasRenderingContext2D, _env: PeerEnv, x: number, y: number, count: number) {
    const label = `+${count}`
    ctx.font = `700 11px ${orbitron.style.fontFamily}, sans-serif`
    const tw = ctx.measureText(label).width
    const w = Math.max(26, Math.ceil(tw) + 12), h = 22
    const bx = x - w / 2, by = y - h / 2, r = h / 2
    ctx.beginPath()
    ctx.moveTo(bx + r, by); ctx.lineTo(bx + w - r, by); ctx.arc(bx + w - r, by + r, r, -Math.PI / 2, Math.PI / 2)
    ctx.lineTo(bx + r, by + h); ctx.arc(bx + r, by + r, r, Math.PI / 2, -Math.PI / 2)
    ctx.closePath()
    ctx.fillStyle = BUBBLE_FILL
    ctx.shadowColor = 'rgba(255,106,32,0.75)'
    ctx.shadowBlur = 8
    ctx.fill()
    ctx.shadowBlur = 0
    ctx.shadowColor = 'transparent'
    ctx.lineWidth = 2
    ctx.strokeStyle = SCIFI_PEER_NEON
    ctx.stroke()
    ctx.fillStyle = '#ffffff'
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    ctx.fillText(label, x, y + 0.5)
  },

  drawSelfRing(ctx: CanvasRenderingContext2D, _env: PeerEnv, x: number, y: number) {
    const r = 10 + 4 // 光球半徑＋4（契約 §7）
    ctx.beginPath(); ctx.arc(x, y, r, 0, Math.PI * 2)
    ctx.lineWidth = 4; ctx.strokeStyle = 'rgba(2,4,10,0.45)'; ctx.stroke() // 深色底襯：壓在青色光暈上仍看得清楚
    ctx.beginPath(); ctx.arc(x, y, r, 0, Math.PI * 2)
    ctx.lineWidth = 2
    ctx.strokeStyle = SELF_RING_GREEN
    ctx.shadowColor = 'rgba(70,227,160,0.85)'
    ctx.shadowBlur = 6
    ctx.stroke()
    ctx.shadowBlur = 0
    ctx.shadowColor = 'transparent'
  },
}
