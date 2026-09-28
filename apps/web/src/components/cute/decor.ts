// cute（溫馨可愛）skin 原創裝飾繪圖函式（契約 §3／§4 共用）。
//
// 只提供「通用手繪風裝飾圖案」的 canvas 2D 繪製函式——彩色紙屑（小方塊／彎曲線條）、
// 五角小星星、蓬鬆小雲朵、愛心、紙膠帶——不含任何角色／人物造型（跑者 Q 版小井是
// app/track/cute/ 的 MAP 工人負責，屬於契約 §4，不在本檔案範圍）。
//
// 全部圖案均為原創手繪語彙（粉彩平塗＋深色手繪描邊），刻意避免任何具體角色臉部
// 五官組合、招牌嘴型或商品排版，只借用「圓潤造型／腮紅斜線／貼紙白邊」等通用可愛
// 風格語彙（見契約 §0 版權硬性規定）。
//
// 設計原則：
//   - 每個 draw* 函式都是純函式（不持有任何模組級狀態），只在呼叫當下對傳入的
//     CanvasRenderingContext2D 畫一次，並在內部自行 save()/restore()，呼叫端不需要
//     自己管理 transform（多個裝飾疊在同一個 ctx 上時互不干擾旋轉/位移狀態）。
//   - 座標系統：所有函式都以「圖案中心點 (x, y)」為錨點（不是左上角），旋轉也繞中心點，
//     方便呼叫端做飄動/擺動動畫時直接更新中心座標與旋轉角。
//   - CuteBackground.tsx（全站背景，只用 confetti／star／cloud）與 app/track/cute/
//     的 MAP 工人（軌跡愛心、地圖裝飾）共用同一份函式，避免兩處各刻一套、風格不一致。

export const CUTE_PALETTE = {
  cream: '#fff8ec',
  creamYellow: '#ffe7a3',
  candyPink: '#ff9fc6',
  peach: '#ec6fae',
  skyBlue: '#a9dcf5',
  mint: '#bfeede',
  ink: '#3d2b2b',
  blush: '#ffb3c7',
  white: '#ffffff',
  gold: '#f6b73c',
} as const

export type CuteColor = string

/** 背景飄浮裝飾可挑選的粉彩色盤（不含 ink／white，避免跟描邊/貼紙白邊混淆）。 */
export const CUTE_CONFETTI_COLORS: readonly CuteColor[] = [
  CUTE_PALETTE.creamYellow,
  CUTE_PALETTE.candyPink,
  CUTE_PALETTE.peach,
  CUTE_PALETTE.skyBlue,
  CUTE_PALETTE.mint,
  CUTE_PALETTE.gold,
]

export type ConfettiKind = 'square' | 'curl'

/**
 * 畫一片原創彩色紙屑，中心在 (x, y)。
 * - 'square'：小圓角方塊，填色＋細墨線描邊（手繪感）。
 * - 'curl'：一段彎曲線條（二次貝茲曲線的粗描邊），像飄落的緞帶碎片。
 * @param size 概略邊長／線段長度（CSS px）
 * @param rotation 弧度
 */
export function drawConfettiPiece(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  size: number,
  color: CuteColor,
  rotation = 0,
  kind: ConfettiKind = 'square',
  opacity = 1,
): void {
  ctx.save()
  ctx.globalAlpha *= opacity
  ctx.translate(x, y)
  ctx.rotate(rotation)
  ctx.lineJoin = 'round'
  ctx.lineCap = 'round'
  if (kind === 'square') {
    const half = size / 2
    const r = Math.min(3, half * 0.4)
    ctx.beginPath()
    ctx.moveTo(-half + r, -half)
    ctx.arcTo(half, -half, half, half, r)
    ctx.arcTo(half, half, -half, half, r)
    ctx.arcTo(-half, half, -half, -half, r)
    ctx.arcTo(-half, -half, half, -half, r)
    ctx.closePath()
    ctx.fillStyle = color
    ctx.fill()
    ctx.strokeStyle = CUTE_PALETTE.ink
    ctx.globalAlpha *= 0.55
    ctx.lineWidth = Math.max(1, size * 0.08)
    ctx.stroke()
  } else {
    const half = size / 2
    ctx.beginPath()
    ctx.moveTo(-half, 0)
    ctx.quadraticCurveTo(0, -half * 0.9, half, 0)
    ctx.strokeStyle = color
    ctx.lineWidth = Math.max(2, size * 0.22)
    ctx.stroke()
  }
  ctx.restore()
}

/**
 * 畫一顆原創五角小星星，中心在 (cx, cy)，半徑 r。可選淡墨線描邊（預設開，柔化邊緣）。
 */
export function drawStar(
  ctx: CanvasRenderingContext2D,
  cx: number,
  cy: number,
  r: number,
  color: CuteColor,
  rotation = 0,
  opacity = 1,
  outline = true,
): void {
  const inner = r * 0.45
  ctx.save()
  ctx.globalAlpha *= opacity
  ctx.translate(cx, cy)
  ctx.rotate(rotation)
  ctx.beginPath()
  for (let i = 0; i < 10; i++) {
    const radius = i % 2 === 0 ? r : inner
    const angle = (Math.PI / 5) * i - Math.PI / 2
    const px = Math.cos(angle) * radius
    const py = Math.sin(angle) * radius
    if (i === 0) ctx.moveTo(px, py)
    else ctx.lineTo(px, py)
  }
  ctx.closePath()
  ctx.fillStyle = color
  ctx.fill()
  if (outline) {
    ctx.strokeStyle = CUTE_PALETTE.ink
    ctx.globalAlpha *= 0.45
    ctx.lineWidth = Math.max(1, r * 0.12)
    ctx.lineJoin = 'round'
    ctx.stroke()
  }
  ctx.restore()
}

/**
 * 畫一朵原創蓬鬆小雲朵（多顆疊圓組成），中心在 (cx, cy)，寬度約 w。
 * 預設填色刻意不是純白（背景奶油底 #fff8ec 跟純白幾乎無法區分，會讓雲朵看起來只剩
 * 描邊、沒有實體），改用極淡天空藍白，跟背景有足夠的明度/色相差異、仍讀作「白雲」。
 * 描邊做法：疊圓的「聯集輪廓」不能直接對每個子圓分別 stroke（那樣每顆圓自己的圓周都會
 * 畫出來，變成一堆互相穿插的線圈、不像雲的外輪廓）——改成「先在同一組路徑上放大
 * ~6% 畫一層純墨色當底，再用原尺寸疊上填色」，兩層都用同一個 nonzero-fill 聯集規則，
 * 底層只在外緣露出一圈，效果等同「幫聯集外輪廓描邊」且沒有內部穿插線條。
 */
export function drawCloud(
  ctx: CanvasRenderingContext2D,
  cx: number,
  cy: number,
  w: number,
  color: CuteColor = '#f3faff',
  opacity = 0.92,
  outline = true,
): void {
  const h = w * 0.52
  const lumps: Array<[number, number, number]> = [
    [-w * 0.32, h * 0.12, h * 0.42],
    [-w * 0.08, -h * 0.18, h * 0.52],
    [w * 0.18, h * 0.05, h * 0.46],
    [w * 0.36, h * 0.18, h * 0.32],
  ]
  const bodyCy = h * 0.22
  const bodyRx = w * 0.5
  const bodyRy = h * 0.34

  function unionPath(scale: number) {
    ctx.beginPath()
    ctx.ellipse(0, bodyCy * scale, bodyRx * scale, bodyRy * scale, 0, 0, Math.PI * 2)
    for (const [lx, ly, lr] of lumps) {
      const sx = lx * scale
      const sy = ly * scale
      const sr = lr * scale
      ctx.moveTo(sx + sr, sy)
      ctx.arc(sx, sy, sr, 0, Math.PI * 2)
    }
  }

  ctx.save()
  ctx.translate(cx, cy)
  const baseAlpha = ctx.globalAlpha
  if (outline) {
    ctx.globalAlpha = baseAlpha * opacity * 0.5
    unionPath(1.06)
    ctx.fillStyle = CUTE_PALETTE.ink
    ctx.fill()
  }
  ctx.globalAlpha = baseAlpha * opacity
  unionPath(1)
  ctx.fillStyle = color
  ctx.fill()
  ctx.restore()
}

/**
 * 畫一顆原創愛心，中心在 (cx, cy)（幾何中心，非頂點凹陷處），寬度約 size。
 * 供軌跡標記（每 100m 一顆）或裝飾使用；預設帶墨線描邊＋可選細白邊（貼紙感）。
 */
export function drawHeart(
  ctx: CanvasRenderingContext2D,
  cx: number,
  cy: number,
  size: number,
  color: CuteColor = CUTE_PALETTE.peach,
  opacity = 1,
  stickerOutline = true,
): void {
  const s = size / 2
  ctx.save()
  ctx.globalAlpha *= opacity
  ctx.translate(cx, cy - s * 0.15)
  ctx.beginPath()
  ctx.moveTo(0, s * 0.65)
  ctx.bezierCurveTo(-s * 1.15, -s * 0.15, -s * 0.55, -s * 1.05, 0, -s * 0.35)
  ctx.bezierCurveTo(s * 0.55, -s * 1.05, s * 1.15, -s * 0.15, 0, s * 0.65)
  ctx.closePath()
  if (stickerOutline) {
    ctx.save()
    ctx.strokeStyle = CUTE_PALETTE.white
    ctx.lineWidth = Math.max(2, size * 0.28)
    ctx.lineJoin = 'round'
    ctx.stroke()
    ctx.restore()
  }
  ctx.fillStyle = color
  ctx.fill()
  ctx.strokeStyle = CUTE_PALETTE.ink
  ctx.lineWidth = Math.max(1, size * 0.09)
  ctx.lineJoin = 'round'
  ctx.stroke()
  ctx.restore()
}

/**
 * 畫一段原創紙膠帶（washi tape）：半透明斜紋小矩形＋淡白色纖維紋＋鋸齒狀邊緣感。
 * 中心在 (cx, cy)，尺寸 w×h，angle 為旋轉弧度（通常給小角度製造「隨手貼上」的手感）。
 */
export function drawWashiTape(
  ctx: CanvasRenderingContext2D,
  cx: number,
  cy: number,
  w: number,
  h: number,
  color: CuteColor = CUTE_PALETTE.candyPink,
  angle = -0.12,
  opacity = 0.8,
): void {
  ctx.save()
  ctx.globalAlpha *= opacity
  ctx.translate(cx, cy)
  ctx.rotate(angle)
  // 主體：半透明色底
  ctx.fillStyle = color
  ctx.fillRect(-w / 2, -h / 2, w, h)
  // 斜紋（repeating diagonal stripes）：用細白線條疊出紙膠帶常見的斜紋壓花感。
  ctx.save()
  ctx.beginPath()
  ctx.rect(-w / 2, -h / 2, w, h)
  ctx.clip()
  ctx.strokeStyle = 'rgba(255,255,255,.55)'
  ctx.lineWidth = Math.max(1, h * 0.12)
  const step = h * 0.32
  for (let off = -w; off < w * 2; off += step) {
    ctx.beginPath()
    ctx.moveTo(-w / 2 + off, -h / 2)
    ctx.lineTo(-w / 2 + off - h, h / 2)
    ctx.stroke()
  }
  ctx.restore()
  // 邊框：極淡墨線，暗示膠帶邊緣。
  ctx.strokeStyle = 'rgba(61,43,43,.25)'
  ctx.lineWidth = 1
  ctx.strokeRect(-w / 2, -h / 2, w, h)
  ctx.restore()
}
