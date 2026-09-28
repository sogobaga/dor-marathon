// 溫馨可愛（cute）原創地面圖塊 — docs/skins/CUTE_CONTRACT.md§4：公園／綠地用「原創小圓樹點點圖案」，
// 水域用「原創小波浪『〜』圖案」，皆為程式逐筆繪製的 16×16 可平鋪 fill-pattern，零外部素材、零任何
// 吉伊卡哇造型語彙（純幾何圓點／波浪線，不含任何動物/角色輪廓）。
// 用法：CuteMap.tsx 透過 map.setMissingStyleImageResolver() 於圖片首次被要求時呼叫 tileImageData()
// 產生 ImageData 並 map.addImage() 掛上（id 與這裡的 TileKind 同名）——改用 resolver 而非監聽
// 'styleimagemissing' 事件是刻意的根因修法，見 CuteMap.tsx 該行上方註解（MapLibre ImageManager
// 時序陷阱：event 版會讓「第一個要求該圖片的圖磚」永遠拿不到圖，resolver 版會等 addImage() 完成
// 才組 response，從根本上不會有這個競態）。

export type TileKind = 'tree' | 'wave'

const TILE = 16

// 色票：docs/skins/CUTE_CONTRACT_R2b.md §A「地圖底色退一步」逐字色號（公園 mint 底、水域 sky 底，比 CONTRACT_R2.md
// §4.5 第一版再退淡一階），只在本檔重複宣告避免跨目錄互相 import runtime 依賴（理由同 style.ts／
// icons.ts／orb.ts 頂端註解）。
const MINT_BG = '#e3f6ec' // docs/skins/CUTE_CONTRACT_R2b.md §A 逐字公園底色
const TREE_GREEN = '#a6e2c3' // 小圓樹主體維持「淡綠」（R2b §A 只retone 底色，樹點顏色不變）
const TREE_GREEN_CORE = '#7cc79f' // 樹心（更深一階，做出「一顆小圓樹」的層次感，不是純平塗色點）
const TREE_OUTLINE = '#f6c6d8' // = 契約 lineSoft，樹的「淡粉描邊」
const SKY_BG = '#dcefff' // docs/skins/CUTE_CONTRACT_R2b.md §A 逐字水域底色
const WAVE_LINE = 'rgba(255,255,255,0.85)' // 契約逐字：水域「sky 底＋白色小波浪」

function newTileCanvas(): { ctx: CanvasRenderingContext2D; toImageData: () => ImageData } {
  const canvas = document.createElement('canvas')
  canvas.width = TILE
  canvas.height = TILE
  const ctx = canvas.getContext('2d')!
  ctx.imageSmoothingEnabled = true // 手繪可愛風要平滑（有別於 retro 的像素風 imageSmoothingEnabled=false）
  return { ctx, toImageData: () => ctx.getImageData(0, 0, TILE, TILE) }
}

// 小圓樹點點：在 16×16 磚內畫兩顆大小交錯的小圓樹（一大一小、對角錯開），讓磚與磚平鋪時看起來像
// 自然散落的樹叢點描而非死板方格；每顆樹＝淡綠圓（細淡粉描邊）＋內圈深綠圓心，模擬「樹冠＋樹心」
// 的層次（CONTRACT_R2.md §4.5：樹改淡綠＋淡粉描邊）。
function drawTreeDot(ctx: CanvasRenderingContext2D, cx: number, cy: number, r: number) {
  ctx.fillStyle = TREE_GREEN
  ctx.strokeStyle = TREE_OUTLINE
  ctx.lineWidth = Math.max(0.5, r * 0.22)
  ctx.beginPath(); ctx.arc(cx, cy, r, 0, Math.PI * 2); ctx.fill(); ctx.stroke()
  ctx.fillStyle = TREE_GREEN_CORE
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
