// cute（溫馨可愛）skin 原創裝飾繪圖函式（契約 §3／§4 共用）。
//
// 只提供「通用手繪風裝飾圖案」的 canvas 2D 繪製函式——彩色紙屑（小方塊／彎曲線條）、
// 五角小星星、蓬鬆小雲朵、愛心、紙膠帶、半透明泡泡——不含任何角色／人物造型（第二輪起
// GPS 地圖上不再有角色，改為「靈魂光點」，由 app/track/cute/ 的 MAP 工人負責繪製，屬於
// 契約 §4，不在本檔案範圍）。
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

// 第二輪「草莓牛奶」色票（docs/skins/CUTE_CONTRACT_R2.md §2 逐字色號）。THEME 工人與
// MAP 工人（app/track/cute/）並行開發、共用同一份色票；為了不讓對方手上還在跑的程式因為
// key 改名而破版，這裡「只換值、不刪舊 key」——所有第一輪就存在的 key 名稱原封不動保留，
// 只更新成新色號（見旁註對應關係），並在下半段新增契約 §2 的逐字 key 名稱（milk/card/
// sakura/candy/rose/berry/berryDim/line/lineSoft/lavender/sky/peachCoral），雙方引用
// 舊名或新名都會拿到同一組粉嫩色值。
export const CUTE_PALETTE = {
  // ── 契約 §2 逐字 key（新）──
  milk: '#fff5f8',
  card: '#ffffff',
  sakura: '#ffc4dc',
  candy: '#ff9fc8',
  rose: '#d6457f', // 僅限 ≥24px 大字／裝飾使用，見契約對比門檻
  berry: '#5b2a3c',
  berryDim: '#8a4a64',
  line: '#b5708a',
  lineSoft: '#f6c6d8',
  lavender: '#d9c8ff',
  sky: '#cfe8ff',
  peachCoral: '#ff9aa8',

  // ── 第一輪舊 key（相容別名，值已改指向新色票，不再是奶油／米黃系）──
  cream: '#fff5f8',       // ≈ milk
  creamYellow: '#fff0b8', // ≈ butter：不再當大面積底色，只保留給極少量點綴（如小星星）
  candyPink: '#ff9fc8',   // ≈ candy
  peach: '#ff9aa8',       // ≈ peachCoral
  skyBlue: '#cfe8ff',     // ≈ sky
  mint: '#c8f2e1',        // 契約新 mint 值
  ink: '#5b2a3c',         // ≈ berry：取代舊深可可墨線／墨字
  blush: '#ffe3ee',       // 契約新 blush 值（次要底色，非舊版腮紅點綴義）
  white: '#ffffff',
  gold: '#f6b73c',        // 契約未要求換色，僅小星星等點綴沿用
} as const

export type CuteColor = string

/** 背景飄浮裝飾可挑選的粉彩色盤（不含 ink／white，避免跟描邊/貼紙白邊混淆）。第二輪拿掉
 * creamYellow／gold 兩個偏黃色號，改成 sakura／candy／lavender／mint／rose 五色，讓紙屑/
 * 愛心/泡泡整體讀作「粉嫩」而非「奶油黃」（契約 §3「大面積不得再是奶油黃／米黃」，驗收要求
 * 粉／薰衣草色相數量 ≥ 黃／橘色相的 3 倍）；小星星另外保留 gold 當點綴色（見 makeParticle
 * 'star' 分支），不受這個共用色盤限制。 */
export const CUTE_CONFETTI_COLORS: readonly CuteColor[] = [
  CUTE_PALETTE.sakura,
  CUTE_PALETTE.candy,
  CUTE_PALETTE.lavender,
  CUTE_PALETTE.mint,
  CUTE_PALETTE.rose,
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
 * 第二輪改「帶粉色描邊的粉白雲朵」（契約 §3「不要灰色雲」）：預設填色從偏藍白 #f3faff
 * 改成偏粉白 #fff6fa（跟草莓牛奶頁面底 #fff5f8 仍有足夠明度/色相差異，讀作「白雲」，
 * 不會糊在背景裡），描邊也從深可可墨改成柔和的 candy 粉（見 outlineColor 參數，預設
 * `CUTE_PALETTE.candy`，不再是 ink／灰黑）。
 * 描邊做法：疊圓的「聯集輪廓」不能直接對每個子圓分別 stroke（那樣每顆圓自己的圓周都會
 * 畫出來，變成一堆互相穿插的線圈、不像雲的外輪廓）——改成「先在同一組路徑上放大
 * ~6% 畫一層粉色當底，再用原尺寸疊上填色」，兩層都用同一個 nonzero-fill 聯集規則，
 * 底層只在外緣露出一圈，效果等同「幫聯集外輪廓描邊」且沒有內部穿插線條。
 */
export function drawCloud(
  ctx: CanvasRenderingContext2D,
  cx: number,
  cy: number,
  w: number,
  color: CuteColor = '#fff6fa',
  opacity = 0.92,
  outline = true,
  outlineColor: CuteColor = CUTE_PALETTE.candy,
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
    ctx.fillStyle = outlineColor
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
  color: CuteColor = CUTE_PALETTE.lavender,
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
  // 邊框：極淡莓色線，暗示膠帶邊緣（第二輪改 berry 系，取代舊深可可墨 rgba(61,43,43,.25)）。
  ctx.strokeStyle = 'rgba(91,42,60,.25)'
  ctx.lineWidth = 1
  ctx.strokeRect(-w / 2, -h / 2, w, h)
  ctx.restore()
}

/**
 * 畫一顆原創半透明小泡泡，中心在 (cx, cy)，半徑 r（第二輪新增，契約 §3「半透明泡泡」點綴）。
 * 純裝飾、不承載文字：柔和色底＋外緣稍深一圈細邊＋左上角一小撮白色高光橢圓，營造「肥皂泡」
 * 的立體感，不描摹任何角色商品素材。
 */
export function drawBubble(
  ctx: CanvasRenderingContext2D,
  cx: number,
  cy: number,
  r: number,
  color: CuteColor = CUTE_PALETTE.sky,
  opacity = 0.4,
): void {
  ctx.save()
  ctx.globalAlpha *= opacity
  ctx.translate(cx, cy)
  ctx.beginPath()
  ctx.arc(0, 0, r, 0, Math.PI * 2)
  ctx.fillStyle = color
  ctx.fill()
  ctx.lineWidth = Math.max(1, r * 0.08)
  ctx.strokeStyle = color
  ctx.globalAlpha *= 1.4 // 邊緣稍微更飽和，跟填色拉出一點層次
  ctx.stroke()
  // 左上角小高光（純白半透明橢圓），暗示光線反射，加強「泡泡」而非「圓點」的辨識度。
  ctx.globalAlpha = ctx.globalAlpha / 1.4 * 0.7
  ctx.beginPath()
  ctx.ellipse(-r * 0.32, -r * 0.34, r * 0.28, r * 0.16, -0.6, 0, Math.PI * 2)
  ctx.fillStyle = '#ffffff'
  ctx.fill()
  ctx.restore()
}
