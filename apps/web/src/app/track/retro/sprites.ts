// 復古 RPG（retro）原創像素美術（docs/skins/RETRO_CONTRACT.md §1／§4）——硬性規則：全部圖塊皆為原創
// 逐像素設計，零外部素材、零商業遊戲參照，只借用「8-bit JRPG 俯視大地圖」的類型語彙（點描草地、樹林、
// 水波、石牆、石板路）。調色盤限定 NES 風 16 色（CONTRACT.md §4 色票，見 RETRO_PALETTE）。
//
// 地面圖塊（草地/森林/水波/石牆/石板路/小徑/沙地/鐵軌）改成重用 components/retro/tiles.ts 的
// drawTile()（THEME 工人為 RetroBackground.tsx 與本目錄共同建立的原創圖塊資料表，見該檔檔頭
// 「給 RetroBackground.tsx 與 MAP 工人共用」）——同一份像素資料，全站背景與 GPS 地圖圖塊視覺一致，
// 不維護兩份可能失焦的圖塊定義。本檔只負責把它轉成 maplibre-gl addImage() 需要的 ImageData，以及
// 地圖專屬的圖示（公里旗、城堡/寶箱）——這些不是「地面圖塊」，tiles.ts 不涵蓋。
//
// docs/skins/RETRO_CONTRACT_R4.md §3：原本畫在這裡的四方向角色 sprite（方向/站立/走路格線資料與
// 對應繪製函式）與身後的腳印機制已整段移除，改用 ./orb.ts 的 MagicOrb（像素魔法光點）取代——理由
// 沿用 v857 溫馨可愛拿掉角色造型的同一個決策：避免未來地圖角色需要性別顯示、或擴充角色種類造成的
// 美術/維護負擔。RetroMap.tsx 已改呼叫 MagicOrb，本檔不再有任何角色相關程式碼（死碼不留）。
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

// 每公里旗子（黑色描邊旗杆＋方形旗面＋白色公里數字）。
//
// ORBPOS_CONTRACT.md 補充（編排者檢視 R4 自檢截圖，2026-09-29）：舊版旗子固定用 scale=1（呼叫端
// RetroMap.tsx 過去沒把 orbScale 傳進來），無論裝置寬度、orbScale 實際是多少都畫成幾個 CSS px 大的
// 三角旗，草地／道路／新的金色光路上幾乎看不見。修法兩層：①呼叫端改傳 orbScale（本身已 clamp 在
// 2–4，光是這樣就比舊版固定的 1 放大至少 2 倍）；②這裡的網格尺寸本身也放大（旗杆變高、旗面從
// 三角形改成方形旗面＋外加 1 格黑色描邊，比原本的三角旗面積大得多），兩者相乘後在任何裝置上都
// 遠超過「至少放大 2 倍」的下限。旗面改方形（而非原本平滑的三角形路徑）是為了在任意背景上都有
// 完整的黑色矩形外框頂住可讀性，也更貼近本檔其餘像素圖示（drawTargetIcon 等）的方塊風格；公里
// 數字改白色（原本黑字在暗色地圖圖塊——如森林／石牆——上容易融入背景），並加一圈黑色描邊
// （strokeText）確保在金黃旗面本身、以及旗面外緣任何背景色上都讀得出數字。
export function drawKmFlag(ctx: CanvasRenderingContext2D, x: number, y: number, km: number, scale: number) {
  ctx.save()
  ctx.translate(x, y)
  const s = scale
  // 旗杆：黑色描邊＋灰色主體（描邊外框比主體寬 1 格，四邊都露出描邊）。
  ctx.fillStyle = '#000000'
  ctx.fillRect(-2 * s, -18 * s, 4 * s, 18 * s)
  ctx.fillStyle = '#7c7c7c'
  ctx.fillRect(-1 * s, -17 * s, 2 * s, 16 * s)
  // 旗面：方形（取代舊版三角形），兩位數公里數（10–42）多留寬度；黑色外框 1 格＋金黃底。
  const flagW = (String(km).length >= 2 ? 20 : 14) * s
  const flagH = 12 * s
  const flagX = 1 * s
  const flagY = -18 * s
  ctx.fillStyle = '#000000'
  ctx.fillRect(flagX - s, flagY - s, flagW + 2 * s, flagH + 2 * s)
  ctx.fillStyle = '#f8b800'
  ctx.fillRect(flagX, flagY, flagW, flagH)
  // 公里數字：白色主體＋黑色描邊（雙重對比，任何背景都讀得出來）。
  ctx.font = `bold ${Math.round(10 * s)}px monospace`
  ctx.textAlign = 'center'
  ctx.textBaseline = 'middle'
  const tx = flagX + flagW / 2
  const ty = flagY + flagH / 2 + 0.5 * s
  ctx.lineWidth = Math.max(1, 0.6 * s)
  ctx.strokeStyle = '#000000'
  ctx.strokeText(String(km), tx, ty)
  ctx.fillStyle = '#ffffff'
  ctx.fillText(String(km), tx, ty)
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
