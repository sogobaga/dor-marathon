// 復古 RPG（retro）原創像素美術（CONTRACT.md §1／§4）——硬性規則：全部圖塊與角色皆為原創逐像素
// 設計，零外部素材、零商業遊戲參照，只借用「8-bit JRPG 俯視大地圖」的類型語彙（點描草地、樹林、
// 水波、石牆、石板路、勇者）。調色盤限定 NES 風 16 色（CONTRACT.md §4 色票，見 RETRO_PALETTE）。
//
// 地面圖塊（草地/森林/水波/石牆/石板路/小徑/沙地/鐵軌）改成重用 components/retro/tiles.ts 的
// drawTile()（THEME 工人為 RetroBackground.tsx 與本目錄共同建立的原創圖塊資料表，見該檔檔頭
// 「給 RetroBackground.tsx 與 MAP 工人共用」）——同一份像素資料，全站背景與 GPS 地圖圖塊視覺一致，
// 不維護兩份可能失焦的圖塊定義。本檔只負責把它轉成 maplibre-gl addImage() 需要的 ImageData，以及
// 地圖專屬的角色/圖示（勇者、腳印、公里旗、城堡/寶箱）——這些不是「地面圖塊」，tiles.ts 不涵蓋。
import { drawTile, RETRO_PALETTE, type TileKind } from '@/components/retro/tiles'

export const PALETTE = RETRO_PALETTE

const TILE = 16

function newTileCanvas(): { ctx: CanvasRenderingContext2D; toImageData: () => ImageData } {
  const canvas = document.createElement('canvas')
  canvas.width = TILE
  canvas.height = TILE
  const ctx = canvas.getContext('2d')!
  ctx.imageSmoothingEnabled = false
  return { ctx, toImageData: () => ctx.getImageData(0, 0, TILE, TILE) }
}

// 回傳 ImageData（而非 canvas 本身）——maplibre-gl 的 map.addImage()/updateImage() 型別是
// StyleImageSource（HTMLImageElement | ImageBitmap | ImageData | {width,height,data}），不含
// HTMLCanvasElement，因此轉出來的是 ctx.getImageData() 而非 canvas 元素本身。frame 只有 'water'
// 有意義（見 tiles.ts drawTile 說明），其餘 kind 忽略。
export function tileImageData(kind: TileKind, frame: 0 | 1 = 0): ImageData {
  const { ctx, toImageData } = newTileCanvas()
  drawTile(ctx, kind, frame)
  return toImageData()
}

// ── 勇者（DOR 自有原創角色）：CONTRACT_R2.md §2 權威底稿（2026-09-27 第二輪，取代第一輪
// CONTRACT.md §7 手刻矩形版）──16 寬 × 24 高，逐字解碼契約給的字元表（不手動轉譯成矩形，確保
// 像素跟契約底稿逐格一致），四方向 × 一幀站立 + 兩幀走路（frame 0 兼作站立幀，沿用既有
// RetroMap.tsx 呼叫慣例：靜止時固定傳 frame 0）。down 以外的方向與走路幀，依契約文字規則
// （背面全髮無臉＋圍巾結＋劍柄；側面單片鏡框＋鏡腳＋鼻尖凸出＋後腦髮量較多；走路兩幀＝左右腳
// 交替抬起一列＋鞋子上移 1px）對底稿做局部改寫衍生，而非另外手刻四份，確保比例/配色不會走鐘。
export type HeroDir = 'down' | 'up' | 'left' | 'right'

// 契約 §2 圖例字元 → 色碼（逐字對照，E/B 與 F/P 契約本就指定同一色，不是筆誤）。
const HERO_PALETTE: Record<string, string> = {
  K: '#000000', P: '#fc7aa4', p: '#c8406c', S: '#fcd8a8',
  E: '#ac7c00', F: '#fc7aa4', R: '#e4462c', G: '#f8b800',
  W: '#fcfcfc', A: '#bcbcbc', B: '#ac7c00', N: '#24188c', w: '#fcfcfc',
}

// 正面（down）站立：CONTRACT_R2.md §2 逐字底稿，24 列 × 16 欄，一字不改；其餘全部由這份資料衍生。
const HERO_DOWN_STAND: readonly string[] = [
  '....KKKKKKKK....',
  '...KPPPPPPPPK...',
  '..KPPPPPPPPPPK..',
  '..KPPPPPPPPPPK..',
  '.KPPPPPPPPPPPPK.',
  '.KPpPPPPPPPPpPK.',
  '.KPpSSSSSSSSpPK.',
  '.KPKKKKSSKKKKPK.',
  '.KPKSEKKKKESKPK.',
  '.KPKKKKSSKKKKPK.',
  '.KPSFSSSSSSFSPK.',
  '..KPSSSKKSSSPK..',
  '...KKSSSSSSKK...',
  '..KRRRRGRRRRRK..',
  '.KAWRRRRRRRRWAK.',
  '.KAWWBRRWWWWWAK.',
  '.KSWWWGBWWWWWSK.',
  '..KWWWGWBWWWWK..',
  '..KNNNNNNNNNNK..',
  '.KNNNNNNNNNNNNK.',
  '.KNNKNNNNNNKNNK.',
  '....KwK..KwK....',
  '...KFFK..KFFK...',
  '...KKKK..KKKK...',
]

function assertHeroGrid(rows: readonly string[]): void {
  if (rows.length !== 24) throw new Error(`hero grid must have 24 rows, got ${rows.length}`)
  for (const r of rows) if (r.length !== 16) throw new Error(`hero grid row must be 16 chars, got ${r.length}: ${r}`)
}
assertHeroGrid(HERO_DOWN_STAND)

function cloneGrid(rows: readonly string[]): string[] { return rows.slice() }
function setChar(row: string, x: number, ch: string): string { return row.slice(0, x) + ch + row.slice(x + 1) }
// 逐字鏡射每一列（模擬「right 為 left 的鏡像」），是最不容易出錯的鏡像實作方式。
function mirrorGrid(rows: readonly string[]): string[] { return rows.map((r) => r.split('').reverse().join('')) }

// 走路：左右腳交替抬起一隻（對照 r21 "....KwK..KwK...."：左腳 col4-6、右腳 col9-11）。抬起那隻
// 的 r21-23（襪/鞋/鞋底輪廓）整體上移一列、捨棄原本最底那列（鞋子上移 1px、腳尖離地少一列），
// 另一隻腳維持站立格不動。frame 0＝原始底稿（雙腳站好、站立幀）；frame 1＝抬左腳；frame 2＝抬
// 右腳——三幀彼此都不同（CONTRACT_R2 §2「一幀站立＋兩幀走路」），不再讓 frame 0 身兼站立與走路。
const FOOT_TOP_ROW = 21
function liftFoot(rows: string[], colFrom: number, colTo: number): void {
  for (let x = colFrom; x <= colTo; x++) {
    const mid = HERO_DOWN_STAND[FOOT_TOP_ROW + 1][x]
    const bot = HERO_DOWN_STAND[FOOT_TOP_ROW + 2][x]
    rows[FOOT_TOP_ROW] = setChar(rows[FOOT_TOP_ROW], x, mid)
    rows[FOOT_TOP_ROW + 1] = setChar(rows[FOOT_TOP_ROW + 1], x, bot)
    rows[FOOT_TOP_ROW + 2] = setChar(rows[FOOT_TOP_ROW + 2], x, '.')
  }
}
// R2 FIX（findings #3）：走路次要動態——手臂前後擺＋圍巾尾端反向擺，原版 walkVariant 只做了
// liftFoot。guardedSet 只在「目標像素仍是預期的原始字元」時才替換：up/left/right 這幾個方向會把
// row10 的 F（袖口）整段覆寫成髮色（buildUpGrid）或部分覆寫成髮色（buildLeftGrid 的後腦髮量），
// 若不檢查就硬改，會在那些方向上把別的造型元素改壞；檢查失敗就整段跳過，保證對其他方向零風險。
function guardedSet(rows: string[], y: number, x: number, expect: string, next: string): void {
  if (rows[y][x] !== expect) return
  rows[y] = setChar(rows[y], x, next)
}
function swingArmsAndScarf(rows: string[], frame: 0 | 1 | 2): void {
  if (frame === 0) return
  if (frame === 1) {
    // 左袖口（col3-4）外擺 1px：F 從 col4 移到 col3。
    guardedSet(rows, 10, 4, 'F', 'S')
    guardedSet(rows, 10, 3, 'S', 'F')
    // 圍巾尾端（row14 的 R 塊）往左擺 1px：左緣多 1 格、右緣收 1 格。
    guardedSet(rows, 14, 3, 'W', 'R')
    guardedSet(rows, 14, 11, 'R', 'W')
  } else {
    // 右袖口（col11-12）外擺 1px：F 從 col11 移到 col12。
    guardedSet(rows, 10, 11, 'F', 'S')
    guardedSet(rows, 10, 12, 'S', 'F')
    // 圍巾尾端往右擺 1px（與左擺相反方向），對稱做法：右緣多 1 格、左緣收 1 格。
    guardedSet(rows, 14, 12, 'W', 'R')
    guardedSet(rows, 14, 4, 'R', 'W')
  }
}
function walkVariant(base: readonly string[], frame: 0 | 1 | 2): string[] {
  const rows = cloneGrid(base)
  if (frame === 1) liftFoot(rows, 4, 6) // 左腳 col4-6
  else if (frame === 2) liftFoot(rows, 9, 11) // 右腳 col9-11
  swingArmsAndScarf(rows, frame)
  return rows
}

// 背面（up）：看不到臉，頭部（原本臉/眼罩那幾列）整片改頭髮色＋中央髮分線陰影；圍巾結（後頸中央
// 一小塊凸起）；左肩後斜出劍柄（灰柄＋金護手，畫在頭側外緣的透明邊界上，不覆蓋既有輪廓）。
function buildUpGrid(base: readonly string[]): string[] {
  const rows = cloneGrid(base)
  for (let y = 6; y <= 12; y++) {
    let row = rows[y]
    let out = ''
    for (let x = 0; x < 16; x++) {
      const ch = row[x]
      // r07/r08/r09 是黑框眼鏡的「內部」像素（S 皮膚／E 眼／K 鏡框全部在頭部輪廓內側，
      // 只有 col1／col14 那兩個 K 才是頭部外緣輪廓，必須保留）；一律覆成髮色才會真的
      // 「無臉」，只覆蓋 S/E/F 會留下鏡框的黑色網格形狀（первый版本的 bug：背面看起來
      // 仍像戴著眼鏡）。r06/r10-12 維持只覆蓋 S/E/F（那幾列的 K 是頭型輪廓本身）。
      const isInnerMaskRow = y >= 7 && y <= 9
      const isOutlineCol = x === 1 || x === 14
      if (ch === 'S' || ch === 'E' || ch === 'F') out += 'P'
      else if (isInnerMaskRow && ch === 'K' && !isOutlineCol) out += 'P'
      else out += ch
    }
    row = out
    // R2 FIX（findings #2）：原本只到 row10，但 row11 中央（col7-8，原始底稿是 'KK'，不是頭型輪廓
    // ——輪廓在 col2/13——而是臉部細節）沒被前面的迴圈覆蓋（isInnerMaskRow 只到 y<=9），殘留兩個
    // 黑點，背面看起來像後腦有嘴巴/陰影。延伸到 y<=11，讓髮分線陰影一路蓋過去，兩個黑點跟著變成
    // 髮色的深色分線，符合契約「頭全為髮、無臉」且不影響 row11 真正的輪廓（col2/13，不在 col7/8）。
    if (y >= 6 && y <= 11) row = setChar(setChar(row, 7, 'p'), 8, 'p') // 髮分線陰影
    rows[y] = row
  }
  rows[12] = setChar(setChar(rows[12], 7, 'R'), 8, 'R') // 圍巾結：後頸中央凸起一小塊（沿用圍巾色）
  // 左肩後斜出劍柄：畫在頭側外緣本來是透明的邊界格，不覆蓋既有輪廓像素。
  rows[11] = setChar(rows[11], 0, 'A')
  rows[12] = setChar(rows[12], 0, 'A')
  rows[13] = setChar(rows[13], 0, 'G')
  return rows
}

// 側面（left；right 由 mirrorGrid(left) 取得，見契約「right 為鏡像」）：正面對稱的雙眼/口罩改成
// 前方（低 x＝面朝方向）單片鏡框＋鏡腳延伸到後腦；鼻尖 1px 突出在輪廓外緣；後腦（高 x）髮量較多；
// 圍巾尾端在後方（高 x）多飄 1px；劍柄露在背側（高 x，覆蓋在原本的邊框格上，比照第一輪同一手法）。
function buildLeftGrid(base: readonly string[]): string[] {
  const rows = cloneGrid(base)
  // r07（口罩上緣 KKKKSSKKKKPK）／r08（雙眼 KSEKKKKESKPK）／r09（口罩下緣，同 r07 形狀）：
  // 前方（col3-7）保留單邊鏡框＋眼睛＋鼻樑，後方（col8-12）填成頭髮（後腦看不到另一邊鏡片）。
  for (const y of [7, 8, 9]) {
    let row = rows[y]
    for (let x = 8; x <= 12; x++) row = setChar(row, x, 'P')
    rows[y] = row
  }
  rows[8] = setChar(rows[8], 6, 'K') // 鏡腳：從鏡框往後腦延伸一小段黑線
  rows[9] = setChar(rows[9], 2, 'S') // 鼻尖：在原本輪廓（col3=K）外多凸出 1px
  // 後腦髮量較多：把口罩列之外、頭部下緣（r10-12）後方也覆成髮色，前方保留原本臉頰/下巴膚色。
  for (const y of [10, 11, 12]) {
    let row = rows[y]
    for (let x = 9; x <= 12; x++) if (row[x] !== '.' && row[x] !== 'K') row = setChar(row, x, 'P')
    rows[y] = row
  }
  // 圍巾尾端在背後（高 x）多飄 1px：col14 原本是輪廓 A，col15 原本透明，補一格 R 讓尾端露出來。
  rows[14] = setChar(rows[14], 15, 'R')
  rows[15] = setChar(rows[15], 15, 'R')
  // 劍柄露在背側：覆蓋在既有輪廓格上（同第一輪手法，肩後緊鄰頭側、不重疊臉）。
  rows[11] = setChar(rows[11], 13, 'A')
  rows[12] = setChar(setChar(rows[12], 12, 'A'), 13, 'G')
  return rows
}

// 各方向／幀的衍生格線只需算一次（純函式、輸入固定），快取起來避免每個動畫格都重新字串運算。
const HERO_UP_STAND = buildUpGrid(HERO_DOWN_STAND)
// buildLeftGrid 只改頭部／圍巾／劍柄那幾列（r7-15），完全沒碰腿部/腳部（r18-23）——所以
// HERO_LEFT_STAND 的腳部欄位意義跟 HERO_DOWN_STAND 相同，liftFoot() 可以直接套用，不必另外
// 為側面重寫一份「抬腳」邏輯。right 一律「鏡射 left 的同一幀」取得（見下方 HERO_GRIDS），
// 這樣兩個方向的走路幀永遠是彼此的鏡像，不會有一邊抬錯腳的風險。
const HERO_LEFT_STAND = buildLeftGrid(HERO_DOWN_STAND)
const HERO_LEFT_WALK1 = walkVariant(HERO_LEFT_STAND, 1)
const HERO_LEFT_WALK2 = walkVariant(HERO_LEFT_STAND, 2)
const HERO_GRIDS: Record<HeroDir, readonly [string[], string[], string[]]> = {
  down: [walkVariant(HERO_DOWN_STAND, 0), walkVariant(HERO_DOWN_STAND, 1), walkVariant(HERO_DOWN_STAND, 2)],
  up: [walkVariant(HERO_UP_STAND, 0), walkVariant(HERO_UP_STAND, 1), walkVariant(HERO_UP_STAND, 2)],
  left: [cloneGrid(HERO_LEFT_STAND), HERO_LEFT_WALK1, HERO_LEFT_WALK2],
  right: [mirrorGrid(HERO_LEFT_STAND), mirrorGrid(HERO_LEFT_WALK1), mirrorGrid(HERO_LEFT_WALK2)],
}

// px＝1 個邏輯像素要放大成多少 CSS px（維持像素化觀感）；cx/cy＝畫面上的中心點（CSS px）；
// frame＝0 站立、1/2 兩幀走路（CONTRACT_R2 §2「一幀站立＋兩幀走路」，三幀彼此皆不同）。
export function drawHero(ctx: CanvasRenderingContext2D, cx: number, cy: number, dir: HeroDir, frame: 0 | 1 | 2, px: number) {
  const ox = cx - (16 * px) / 2
  const oy = cy - (24 * px) / 2
  const grid = HERO_GRIDS[dir][frame]
  for (let y = 0; y < 24; y++) {
    const row = grid[y]
    for (let x = 0; x < 16; x++) {
      const ch = row[x]
      if (ch === '.') continue
      const color = HERO_PALETTE[ch]
      if (!color) continue // 理論上不會發生（字元表窮舉），防呆略過而非丟例外
      ctx.fillStyle = color
      ctx.fillRect(Math.round(ox + x * px), Math.round(oy + y * px), Math.ceil(px), Math.ceil(px))
    }
  }
}

// 腳印（漸隱）：留在勇者身後最近 12 個位置，每 6m 一個。
export interface Footprint { x: number; y: number; born: number }
export class FootprintPool {
  private list: Footprint[] = []
  private cap: number
  constructor(cap = 12) { this.cap = cap }
  push(x: number, y: number) {
    this.list.push({ x, y, born: performance.now() })
    if (this.list.length > this.cap) this.list.shift()
  }
  draw(ctx: CanvasRenderingContext2D, px: number) {
    const now = performance.now()
    for (const f of this.list) {
      const age = (now - f.born) / 6000 // 6 秒內漸隱完畢
      const alpha = Math.max(0, 1 - age)
      if (alpha <= 0) continue
      ctx.save()
      ctx.globalAlpha = alpha * 0.55
      ctx.fillStyle = '#3c2415'
      ctx.fillRect(Math.round(f.x - px), Math.round(f.y - px * 0.5), px * 2, px)
      ctx.restore()
    }
  }
}

// 每公里旗子（小三角旗＋像素數字）。
export function drawKmFlag(ctx: CanvasRenderingContext2D, x: number, y: number, km: number, scale: number) {
  ctx.save()
  ctx.translate(x, y)
  ctx.fillStyle = '#7c7c7c'
  ctx.fillRect(-1 * scale, -10 * scale, 2 * scale, 10 * scale) // 旗杆
  ctx.fillStyle = '#f8b800'
  ctx.beginPath()
  ctx.moveTo(1 * scale, -10 * scale)
  ctx.lineTo(7 * scale, -8 * scale)
  ctx.lineTo(1 * scale, -6 * scale)
  ctx.closePath()
  ctx.fill()
  ctx.fillStyle = '#000000'
  ctx.font = `${Math.round(9 * scale)}px monospace`
  ctx.textAlign = 'left'
  ctx.textBaseline = 'middle'
  ctx.fillText(String(km), 2 * scale, -8 * scale)
  ctx.restore()
}

// 目標點：未完成＝關閉的城堡（灰石牆＋深色門），完成＝打開的寶箱（金色）。
export function drawTargetIcon(ctx: CanvasRenderingContext2D, x: number, y: number, kind: 'checkpoint' | 'boss' | 'focus', done: boolean, scale: number) {
  ctx.save()
  ctx.translate(x, y)
  const s = scale
  if (done) {
    // 打開的寶箱
    ctx.fillStyle = '#ac7c00'
    ctx.fillRect(-8 * s, -4 * s, 16 * s, 8 * s)
    ctx.fillStyle = '#f8b800'
    ctx.fillRect(-8 * s, -10 * s, 16 * s, 4 * s)
    ctx.fillStyle = '#000000'
    ctx.fillRect(-1.5 * s, -4 * s, 3 * s, 3 * s) // 鎖扣
  } else if (kind === 'boss' || kind === 'focus') {
    // 關閉的城堡（塔樓＋雉堞）
    ctx.fillStyle = '#7c7c7c'
    ctx.fillRect(-9 * s, -6 * s, 18 * s, 14 * s)
    ctx.fillStyle = '#000000'
    for (let i = -9; i < 9; i += 3) ctx.fillRect(i * s, -10 * s, 2 * s, 4 * s) // 雉堞
    ctx.fillStyle = '#a81000'
    ctx.fillRect(-2 * s, 0, 4 * s, 8 * s) // 城門
  } else {
    // 一般打卡點：小型石柱（未完成）
    ctx.fillStyle = '#b8b8f8'
    ctx.fillRect(-4 * s, -10 * s, 8 * s, 16 * s)
    ctx.fillStyle = '#000000'
    ctx.fillRect(-4 * s, -10 * s, 8 * s, 2 * s)
  }
  ctx.restore()
}
