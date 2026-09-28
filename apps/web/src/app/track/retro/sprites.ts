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
// RetroMap.tsx 呼叫慣例：靜止時固定傳 frame 0）。down 與 up 依契約文字規則對 down 底稿做局部改寫
// 衍生（背面全髮無臉＋圍巾結＋劍柄；走路兩幀＝左右腳交替抬起一列＋鞋子上移 1px），不再另外手刻，
// 確保比例/配色不會走鐘；left／right 側面圖則自 CONTRACT_R3.md §2 起改用編排者逐字繪製的權威底稿
// （見下方 HERO_LEFT_STAND），不再用「正面衍生規則」推出側面（第二輪的 buildLeftGrid 衍生法使用者
// 反映「看起來像頭髮遮住眼睛，不像側臉」，已整段移除，改成跟 HERO_DOWN_STAND 同等地位的逐字底稿）。
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
// liftFoot。guardedSet 只在「目標像素仍是預期的原始字元」時才替換：up 方向會把 row10 的 F（袖口）
// 整段覆寫成髮色（buildUpGrid），若不檢查就硬改，會把 up 造型改壞；檢查失敗就整段跳過，保證零風險。
// swingArmsAndScarf／walkVariant 現在只服務 down／up（CONTRACT_R3 §2：left／right 側面走路改用
// 下面專屬的 swingSideHandAndScarf／sideWalkVariant，座標系不同、不能共用同一組函式）。
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

// 側面（left；right 由 mirrorGrid(left) 取得，見契約「right 為鏡像」）：CONTRACT_R3.md §2 權威底稿，
// 編排者逐字繪製、逐字解碼（不再像第二輪 buildLeftGrid 那樣從正面衍生出「頭髮遮眼」的誤讀）。重點
// 對照契約說明：臉在左（低 x）、瀏海在前，r09 的 E（col3）＝單片鏡框內看得到的眼睛、col2 的 K＝
// 鏡腳延伸到耳；鼻尖在 r10 col0（S，突出於輪廓外緣）；r11 的 F（col4）＝腮紅；胸前 r13 的 G（col4）
// ＝金色胸針；圍巾尾端與劍護手在背側（高 x：r13-14 的 P/p＝後腦髮量延伸到肩、r15-18 的 A/G＝劍柄
// 護手與圍巾尾端）。
const HERO_LEFT_STAND: readonly string[] = [
  '....KKKKKKK.....',
  '..KKPPPPPPPKK...',
  '.KPPPPPPPPPPPK..',
  '.KPPPPPPPPPPPPK.',
  'KPPPPPPPPPPPPPK.',
  'KPPPPPPPPPPPPpK.',
  'KPPPPPPPPPPPPpK.',
  'KPPPPpPPPPPPPpK.',
  '.KKKKKKKKPPPPpK.',
  'KSKEKSSSSKPPPpK.',
  'SSKKKSSSSSKPPpK.',
  'KSSSFSSSSSKPPpK.',
  '.KKSSSSSSKPPPpK.',
  '..KRGRRRRRKPpK..',
  '..KRRRRRRRRRRK..',
  '...KAAWWWWRRRK..',
  '...KAWWBWWWAGK..',
  '...KSWWWBWWWAK..',
  '...KSWWGGGWWWK..',
  '...KNNNNNNNNNK..',
  '..KNNNNNNNNNNNK.',
  '....KwK..KwK....',
  '...KFFK..KFFK...',
  '...KKKK..KKKK...',
]
assertHeroGrid(HERO_LEFT_STAND)

// 側面走路：CONTRACT_R3 §2 明訂側面走路不同於 down/up 的「單純抬腳」——前腳／後腳要交替「前後移
// 1px」（模擬跨步）、抬腳者鞋子再上移 1px；前手與圍巾尾端各自前後／上下擺動 1px。down／up 依契約
// 「維持 v854 不變」，繼續共用上面的 walkVariant／liftFoot／swingArmsAndScarf，不動一行；側面走路
// 因此另外寫一組專用函式，兩邊互不影響。

// 把一列裡「目前有值的欄位」整批平移 dx（±1）：依 dx 方向決定搬移順序（dx>0 先搬右邊，dx<0
// 先搬左邊），避免同一列內把還沒讀到的來源格覆寫掉。跳過空白格（沒有腳的地方不必搬）。
function shiftRowCols(rows: string[], y: number, cols: readonly number[], dx: 1 | -1): void {
  const ordered = dx > 0 ? [...cols].sort((a, b) => b - a) : [...cols].sort((a, b) => a - b)
  for (const x of ordered) {
    const ch = rows[y][x]
    if (ch === '.') continue
    rows[y] = setChar(rows[y], x, '.')
    rows[y] = setChar(rows[y], x + dx, ch)
  }
}
// 鞋子上移 1px：跟 liftFoot() 同一手法（中段搬到上段、下段清空），差別是這裡直接讀「目前 rows 陣列
// 本身」而非固定的 HERO_DOWN_STAND 來源——側面走路先做完 shiftRowCols() 水平跨步之後，腳已經不在
// 原始欄位，必須用移動後的當下內容做上移，固定來源表無法對應新位置。
function liftFootInPlace(rows: string[], colFrom: number, colTo: number): void {
  for (let x = colFrom; x <= colTo; x++) {
    const mid = rows[FOOT_TOP_ROW + 1][x]
    const bot = rows[FOOT_TOP_ROW + 2][x]
    rows[FOOT_TOP_ROW] = setChar(rows[FOOT_TOP_ROW], x, mid)
    rows[FOOT_TOP_ROW + 1] = setChar(rows[FOOT_TOP_ROW + 1], x, bot)
    rows[FOOT_TOP_ROW + 2] = setChar(rows[FOOT_TOP_ROW + 2], x, '.')
  }
}
// 側面底稿 r21-23 的腳跟 down 底稿一模一樣（同一份「兩腳並排」造型，見契約 r21 "....KwK..KwK...."），
// 前腳＝畫面上的左側欄位（col4-6／col3-6），後腳＝右側欄位（col9-11／col9-12），沿用跟 down 版
// liftFoot() 呼叫一致的「只取中間 3 欄」慣例（略過最外側輪廓那 1 欄，同一個簡化前例）。
const FRONT_FOOT_21 = [4, 5, 6] as const
const FRONT_FOOT_2223 = [3, 4, 5, 6] as const
const BACK_FOOT_21 = [9, 10, 11] as const
const BACK_FOOT_2223 = [9, 10, 11, 12] as const

// 前手（實際像素在 r17-r18 col4 的 S，契約文字「r16–r18」為概略範圍）與圍巾尾端（r14-15 右側
// col10-12 的 R）的次要擺動。guardedSet 只在目標格仍是預期原始字元時才替換：col3（r17/r18）是
// 身體正面輪廓的黑線，往那個方向擺會咬掉輪廓，所以只做「往身體方向收 1px」（col4→col5）這個安全
// 方向，guardedSet 天生會讓不安全的另一個方向直接不生效，不需要額外判斷。
function swingSideHandAndScarf(rows: string[], frame: 1 | 2): void {
  if (frame === 1) {
    guardedSet(rows, 17, 4, 'S', 'W')
    guardedSet(rows, 17, 5, 'W', 'S')
    guardedSet(rows, 18, 4, 'S', 'W')
    guardedSet(rows, 18, 5, 'W', 'S')
    // 圍巾尾端上擺 1px：收起 r15 這一段（改成緊鄰的白色袖口色，guarded 只在原本是 R 時才替換）。
    guardedSet(rows, 15, 10, 'R', 'W')
    guardedSet(rows, 15, 11, 'R', 'W')
    guardedSet(rows, 15, 12, 'R', 'W')
  } else {
    // 圍巾尾端下擺 1px：往下多露 1 列（把手套/袖口那一列對應的 3 格改成圍巾色）。
    guardedSet(rows, 16, 10, 'W', 'R')
    guardedSet(rows, 16, 11, 'A', 'R')
    guardedSet(rows, 16, 12, 'G', 'R')
  }
}

// frame 0＝站立（跟契約底稿逐字一致，不做任何改寫）；frame 1＝前腳跨步向前＋抬腳，後腳退後半步；
// frame 2＝相反（後腳跨步向前＋抬腳，前腳退後半步）——標準的兩幀交叉步態，frame1/frame2 互為對稱。
function sideWalkVariant(base: readonly string[], frame: 0 | 1 | 2): string[] {
  const rows = cloneGrid(base)
  if (frame === 0) return rows
  if (frame === 1) {
    shiftRowCols(rows, 21, FRONT_FOOT_21, -1)
    shiftRowCols(rows, 22, FRONT_FOOT_2223, -1)
    shiftRowCols(rows, 23, FRONT_FOOT_2223, -1)
    // FIX2（2026-09-28 審查修補）：這裡原本只傳 3-5（3 欄寬，抄自腳踝 FRONT_FOOT_21 的欄寬），
    // 但實際鞋身 FRONT_FOOT_2223 是 4 欄寬（3-6 整體 -1 之後落在 2-5），只提 3-5 會漏抬/漏清 col2，
    // 留下一顆沒被抬起也沒被清空的孤立黑點（row23 col2）。改成 2-5，涵蓋位移後完整的鞋身寬度。
    liftFootInPlace(rows, 2, 5) // 前腳跨步後新位置（3-6 整體 -1）
    shiftRowCols(rows, 21, BACK_FOOT_21, 1)
    shiftRowCols(rows, 22, BACK_FOOT_2223, 1)
    shiftRowCols(rows, 23, BACK_FOOT_2223, 1)
  } else {
    shiftRowCols(rows, 21, BACK_FOOT_21, -1)
    shiftRowCols(rows, 22, BACK_FOOT_2223, -1)
    shiftRowCols(rows, 23, BACK_FOOT_2223, -1)
    // FIX2（2026-09-28）：同上，後腳鞋身 BACK_FOOT_2223 是 4 欄寬（9-12 整體 -1 之後落在 8-11），
    // 原本只傳 8-10 會漏 col11，改成 8-11。
    liftFootInPlace(rows, 8, 11) // 後腳跨步後新位置（9-12 整體 -1）
    shiftRowCols(rows, 21, FRONT_FOOT_21, 1)
    shiftRowCols(rows, 22, FRONT_FOOT_2223, 1)
    shiftRowCols(rows, 23, FRONT_FOOT_2223, 1)
  }
  swingSideHandAndScarf(rows, frame)
  return rows
}

// 各方向／幀的衍生格線只需算一次（純函式、輸入固定），快取起來避免每個動畫格都重新字串運算。
const HERO_UP_STAND = buildUpGrid(HERO_DOWN_STAND)
const HERO_LEFT_WALK1 = sideWalkVariant(HERO_LEFT_STAND, 1)
const HERO_LEFT_WALK2 = sideWalkVariant(HERO_LEFT_STAND, 2)
// right 一律「鏡射 left 的同一幀」取得（而非另外手刻），這樣兩個方向的走路幀永遠是彼此的鏡像，
// 不會有一邊抬錯腳的風險（見契約「right 為鏡像」）。
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
