// 溫馨可愛（cute）原創地面圖塊 — docs/skins/CUTE_CONTRACT.md§4：公園／綠地用「原創小圓樹點點圖案」，
// 水域用「原創小波浪『〜』圖案」，皆為程式逐筆繪製的 16×16 可平鋪 fill-pattern，零外部素材、零任何
// 吉伊卡哇造型語彙（純幾何圓點／波浪線，不含任何動物/角色輪廓）。
// 用法：CuteMap.tsx 於 'styleimagemissing' 事件時呼叫 tileImageData() 產生 ImageData 並 map.addImage()
// 掛上（id 與這裡的 TileKind 同名），比照 track/retro/sprites.ts 的 tileImageData() 做法。

export type TileKind = 'tree' | 'wave'

const TILE = 16

// 色票（CONTRACT.md §3／§4 色盤，只在本檔重複宣告避免跨目錄互相 import runtime 依賴）。
const MINT_BG = '#cdeed9'
const MINT_DOT = '#8fd3ae' // 小圓樹主體（比背景深一階的綠）
const MINT_DOT_CORE = '#5fae82' // 樹心（更深一階，做出「一顆小圓樹」的層次感，不是純平塗色點）
const SKY_BG = '#bfe3f5'
const WAVE_LINE = '#8ecbe8' // 水波線（比背景深一階的藍）

function newTileCanvas(): { ctx: CanvasRenderingContext2D; toImageData: () => ImageData } {
  const canvas = document.createElement('canvas')
  canvas.width = TILE
  canvas.height = TILE
  const ctx = canvas.getContext('2d')!
  ctx.imageSmoothingEnabled = true // 手繪可愛風要平滑（有別於 retro 的像素風 imageSmoothingEnabled=false）
  return { ctx, toImageData: () => ctx.getImageData(0, 0, TILE, TILE) }
}

// 小圓樹點點：在 16×16 磚內畫兩顆大小交錯的小圓樹（一大一小、對角錯開），讓磚與磚平鋪時看起來像
// 自然散落的樹叢點描而非死板方格；每顆樹＝外圈淺綠圓＋內圈深綠圓心，模擬「樹冠＋樹心」的層次。
function drawTreeDot(ctx: CanvasRenderingContext2D, cx: number, cy: number, r: number) {
  ctx.fillStyle = MINT_DOT
  ctx.beginPath(); ctx.arc(cx, cy, r, 0, Math.PI * 2); ctx.fill()
  ctx.fillStyle = MINT_DOT_CORE
  ctx.beginPath(); ctx.arc(cx, cy, r * 0.45, 0, Math.PI * 2); ctx.fill()
}

function drawTreeTile(ctx: CanvasRenderingContext2D) {
  ctx.fillStyle = MINT_BG
  ctx.fillRect(0, 0, TILE, TILE)
  drawTreeDot(ctx, 4, 4, 2.6)
  drawTreeDot(ctx, 12, 11, 1.9)
  // 邊角各留一點點半顆，讓磚與磚拼接處仍有點狀延續感（避免明顯的磚縫空白）。
  drawTreeDot(ctx, 15, 2, 1.3)
}

// 小波浪「〜」：每 8px 一行畫一條正弦曲線，行與行左右錯開半個週期，模擬水面粼粼波紋；用線段近似
// （非真正三次貝茲）已足夠在 16×16 小圖上呈現波浪感，且平鋪時銜接自然（振幅與週期整除磚寬）。
function drawWaveRow(ctx: CanvasRenderingContext2D, y: number, phase: number) {
  ctx.strokeStyle = WAVE_LINE
  ctx.lineWidth = 1.1
  ctx.lineCap = 'round'
  ctx.beginPath()
  const amp = 1.1
  const steps = 16
  for (let i = 0; i <= steps; i++) {
    const x = (i / steps) * TILE
    const yy = y + Math.sin((i / steps) * Math.PI * 2 + phase) * amp
    if (i === 0) ctx.moveTo(x, yy); else ctx.lineTo(x, yy)
  }
  ctx.stroke()
}

function drawWaveTile(ctx: CanvasRenderingContext2D) {
  ctx.fillStyle = SKY_BG
  ctx.fillRect(0, 0, TILE, TILE)
  drawWaveRow(ctx, 4, 0)
  drawWaveRow(ctx, 12, Math.PI) // 錯半個週期，避免整磚看起來像死板橫條紋
}

export function drawTile(ctx: CanvasRenderingContext2D, kind: TileKind) {
  ctx.clearRect(0, 0, TILE, TILE)
  if (kind === 'tree') drawTreeTile(ctx)
  else drawWaveTile(ctx)
}

export function tileImageData(kind: TileKind): ImageData {
  const { ctx, toImageData } = newTileCanvas()
  drawTile(ctx, kind)
  return toImageData()
}
