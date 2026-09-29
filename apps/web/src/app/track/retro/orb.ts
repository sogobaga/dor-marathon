// 復古 RPG（retro）GPS 地圖「像素魔法光點」（docs/skins/RETRO_CONTRACT_R4.md §3-4，取代 v854/R3
// 原本的四方向角色造型——沿用 v857 溫馨可愛（cute）拿掉角色造型的同一個理由：避免未來地圖角色
// 需要性別顯示、或擴充角色種類造成的美術/維護負擔，見 track/cute/orb.ts 檔頭同一段說明）。
//
// 與 cute/orb.ts（SoulOrb）的差異：cute 是一般抗鋸齒的漸層光球，retro 走純像素風——逐格
// fillRect（不用 canvas 漸層／不用半透明模糊），只用 RETRO_PALETTE 既有色碼、整數格子、
// imageSmoothingEnabled=false（由呼叫端 RetroMap.tsx renderFrame 統一設定），移動時噴出的火花
// 也是離散色階（白→淡黃→金黃→橘），不像 cute 粒子那樣用連續 alpha 淡出——契約§3 逐字「不用半透明
// 漸層，用離散色階維持像素風」。
//
// 純 canvas 2D 繪圖類別，不碰 DOM／不 import maplibre-gl，由 RetroMap.tsx 的單一
// requestAnimationFrame 迴圈驅動（update(dt,...) 再 draw(ctx,...)，兩者都由呼叫端每幀各呼叫一次）。
//
// docs/skins/ORBPOS_CONTRACT.md §B「定位中」外觀：draw() 新增 opts.dim（本體半透明，見下方 draw()
// 內段落）；精度圈（依 pos.acc 換算半徑的像素點狀圈）不屬於光點本體造型，畫在光點之外、由
// RetroMap.tsx 自己算（它才有 map/metersPerPixel 可用），本檔不處理。

// 色票：本檔逐字複製 components/retro/tiles.ts RETRO_PALETTE 內既有的幾個色碼（不 import 該陣列，
// 理由同 cute/orb.ts 檔頭——兩份檔案各自獨立宣告同一組色碼數字，不會因為對方檔案改到一半而讀到
// 中途狀態）。ORB_WHITE／ORB_PALE 是原本角色造型追加色票裡的 #fcfcfc／#fcd8a8（角色造型已移除，
// 這兩個色碼改由光點沿用，見 tiles.ts 對應色碼旁的更新註解）；ORB_GOLD／ORB_BLACK 是圖塊表本來
// 就有的 16 色基本盤。
const ORB_WHITE = '#fcfcfc' // 核心（RETRO_PALETTE 既有色，契約「白色核心」）
const ORB_PALE = '#fcd8a8' // 淡黃（RETRO_PALETTE 既有色，契約「淡黃」；沿用原本角色造型的膚色色碼）
const ORB_GOLD = '#f8b800' // 金黃（RETRO_PALETTE 16 色基本盤，契約「金黃」，與公里旗/軌跡主線同色）
const ORB_ORANGE = '#e45c10' // 橘色外圈（RETRO_PALETTE 16 色基本盤裡最偏橘的一格，契約「橘色外圈」）
const ORB_BLACK = '#000000' // 描邊／地面投影

// 逐格對照表：'.'＝透明、其餘字元見上方色票常數。
const ORB_COLORS: Record<string, string> = { W: ORB_WHITE, Y: ORB_PALE, G: ORB_GOLD, O: ORB_ORANGE, K: ORB_BLACK }

// 13×13（≤ 14×14，契約 §3）同心圓魔法光球底稿，以中心 (6,6) 的歐氏距離分層算出（白核心→淡黃→
// 金黃→橘色外圈→1 格黑色描邊，各層之間刻意保留「距離門檻」而非畫等比例的圓，才會是規整的像素環，
// 不是抗鋸齒的真圓）。幀 A＝標準幀；幀 B 在黑色描邊外再多一層淡黃光暈像素（契約「幀B外圈多一層
// 淡黃光暈」），兩幀交替＝脈動。
const ORB_FRAME_A: readonly string[] = [
  '....KKKKK....',
  '..KKOOOOOKK..',
  '.KOOGGGGGOOK.',
  '.KOGGYYYGGOK.',
  'KOGGYYYYYGGOK',
  'KOGYYWWWYYGOK',
  'KOGYYWWWYYGOK',
  'KOGYYWWWYYGOK',
  'KOGGYYYYYGGOK',
  '.KOGGYYYGGOK.',
  '.KOOGGGGGOOK.',
  '..KKOOOOOKK..',
  '....KKKKK....',
]
// ORBPOS_CONTRACT.md 補充（optional）：幀 B 外圈的淡黃光暈加大一圈，讓「脈動」更明顯——規則＝
// 幀 A 裡每一個原本透明（'.')的格子，只要跟任一黑色描邊格（'K'）8 方向相鄰，就補成淡黃（'Y'），
// 等於把原本只有 2 格厚的光暈再往外擴一圈（仍是同一個 13×13 網格，色票沒有新增，只是既有的 Y／K
// 用得更多），最外圈的正角落（(0,0) 這種與任何 K 都距離 2 格以上的格子）維持透明，圓角觀感不變。
const ORB_FRAME_B: readonly string[] = [
  '.YYYKKKKKYYY.',
  'YYKKOOOOOKKYY',
  'YKOOGGGGGOOKY',
  'YKOGGYYYGGOKY',
  'KOGGYYYYYGGOK',
  'KOGYYWWWYYGOK',
  'KOGYYWWWYYGOK',
  'KOGYYWWWYYGOK',
  'KOGGYYYYYGGOK',
  'YKOGGYYYGGOKY',
  'YKOOGGGGGOOKY',
  'YYKKOOOOOKKYY',
  '.YYYKKKKKYYY.',
]
const ORB_GRID_SIZE = 13
const ORB_FRAMES: readonly [readonly string[], readonly string[]] = [ORB_FRAME_A, ORB_FRAME_B]

function assertOrbGrid(rows: readonly string[]): void {
  if (rows.length !== ORB_GRID_SIZE) throw new Error(`orb grid must have ${ORB_GRID_SIZE} rows, got ${rows.length}`)
  for (const r of rows) if (r.length !== ORB_GRID_SIZE) throw new Error(`orb grid row must be ${ORB_GRID_SIZE} chars, got ${r.length}: ${r}`)
}
assertOrbGrid(ORB_FRAME_A)
assertOrbGrid(ORB_FRAME_B)

/** 逐格畫魔法光球本體（不含地面投影）。cx/cy＝畫面中心點（CSS px，已含浮動位移）；
 * frame＝0 標準、1 脈動外圈；px＝1 個邏輯像素要放大成多少 CSS px（沿用 RetroMap.tsx 的 orbScale，
 * 整數倍率＋fillRect 才有像素風的銳利邊緣，理由同舊版角色繪製函式）。 */
function drawOrbBody(ctx: CanvasRenderingContext2D, cx: number, cy: number, frame: 0 | 1, px: number) {
  const ox = cx - (ORB_GRID_SIZE * px) / 2
  const oy = cy - (ORB_GRID_SIZE * px) / 2
  const grid = ORB_FRAMES[frame]
  for (let y = 0; y < ORB_GRID_SIZE; y++) {
    const row = grid[y]
    for (let x = 0; x < ORB_GRID_SIZE; x++) {
      const ch = row[x]
      if (ch === '.') continue
      const color = ORB_COLORS[ch]
      if (!color) continue // 理論上不會發生（字元表窮舉），防呆略過而非丟例外
      ctx.fillStyle = color
      ctx.fillRect(Math.round(ox + x * px), Math.round(oy + y * px), Math.ceil(px), Math.ceil(px))
    }
  }
}

/** 地面投影：光球正下方 1–2 格高的黑色扁橢圓（不隨浮動位移），契約「營造漂浮感」。用純色
 * fillRect＋橢圓近似（scale 壓扁一顆圓，不用半透明漸層，維持像素風離散色階原則）而非
 * ctx.filter/shadowBlur（效能/相容性理由同 cute/orb.ts 投影段落）。 */
function drawGroundShadow(ctx: CanvasRenderingContext2D, cx: number, cy: number, px: number) {
  const rx = 4 * px, ry = Math.max(1, Math.round(1.4 * px))
  ctx.save()
  ctx.fillStyle = ORB_BLACK
  ctx.beginPath()
  ctx.ellipse(cx, cy + 5 * px, rx, ry, 0, 0, Math.PI * 2)
  ctx.fill()
  ctx.restore()
}

// 閃爍十字星：四個角落（NE/NW/SE/SW）輪流亮滅，每次只有一個角落亮著（契約「每 ~250ms 換一個」，
// 讀作依序輪替而非四顆各自獨立閃爍）。單顆是 3 個像素組成的「+」（中心＋上下左右各 1 格），比
// drawSparkle 一類的填色多邊形更貼近本檔案逐格 fillRect 的像素風格一致性。
const FLICKER_INTERVAL_S = 0.25
const FLICKER_OFFSET = 9 // 距光球中心的邏輯像素偏移（落在黑色描邊之外，契約「四個角落」）
function drawFlickerCross(ctx: CanvasRenderingContext2D, cx: number, cy: number, px: number, cornerIdx: number) {
  const dirX = cornerIdx === 0 || cornerIdx === 2 ? -1 : 1 // 0=NW 1=NE 2=SW 3=SE
  const dirY = cornerIdx < 2 ? -1 : 1
  const ox = cx + dirX * FLICKER_OFFSET * px
  const oy = cy + dirY * FLICKER_OFFSET * px
  ctx.save()
  ctx.fillStyle = ORB_WHITE
  const cells: [number, number][] = [[0, 0], [0, -1], [0, 1], [-1, 0], [1, 0]]
  for (const [dx, dy] of cells) ctx.fillRect(Math.round(ox + dx * px), Math.round(oy + dy * px), Math.ceil(px), Math.ceil(px))
  ctx.restore()
}

// 移動火花：離散色階（白→淡黃→金黃→橘）依剩餘壽命分四階，不用 globalAlpha 淡出（契約「不用半透明
// 漸層，用離散色階維持像素風」）——階段之間直接跳色，消失前最後一階是橘色，滿壽命時是白色，跟光球
// 核心↔外圈的顏色順序相反（火花「從核心噴出、飛遠變暗淡」，光球本體「核心亮、外圈暗」，兩者概念
// 一致：離核心/噴出時間越久＝越接近外圈色）。
const SPARK_CAP = 30 // 契約逐字上限
const SPARK_COLORS: readonly string[] = [ORB_WHITE, ORB_PALE, ORB_GOLD, ORB_ORANGE]
interface Spark { active: boolean; x: number; y: number; vx: number; vy: number; life: number; maxLife: number }

/** MagicOrb：跑者「像素魔法光點」＋地面投影＋四角閃爍十字星＋移動時噴出的像素火花。RetroMap.tsx
 * 掛載時建立一次，每幀呼叫 update() 再 draw()。 */
export class MagicOrb {
  private sparks: Spark[] = []
  private cycleT = 0 // 驅動「浮動＋脈動」的共用時脈：同一個相位切換同時觸發兩者，讓光球浮起的
  // 瞬間也正好是外圈光暈變大的瞬間，視覺上「浮起→發光→落下→收斂」一氣呵成，不需要兩條各自獨立又
  // 恰好對不上拍子的計時器。
  private flickerT = 0
  private flickerIdx = 0

  /** 目前活躍火花數（供 window.__retroDebug.particles 使用，契約 §6）。 */
  get particleCount(): number {
    let n = 0
    for (const s of this.sparks) if (s.active) n++
    return n
  }

  /** 每幀呼叫一次。dt＝秒；moving＝是否視為移動中（決定要不要噴火花）；reducedMotion 為真時
   * 立刻清空既有火花並跳過浮動/脈動/閃爍的所有時脈推進（契約「只畫靜態幀A」）。
   * cx/cy＝光點目前螢幕座標（CSS px），火花從這裡噴出。 */
  update(dt: number, moving: boolean, reducedMotion: boolean, cx: number, cy: number): void {
    if (reducedMotion) {
      if (this.sparks.length) this.sparks.length = 0
      return
    }
    this.cycleT += dt
    this.flickerT += dt
    if (this.flickerT >= FLICKER_INTERVAL_S) {
      this.flickerT -= FLICKER_INTERVAL_S
      this.flickerIdx = (this.flickerIdx + 1) % 4
    }
    if (moving) {
      const spawnPerSec = 15
      const n = Math.max(1, Math.round(spawnPerSec * dt))
      for (let i = 0; i < n; i++) this.spawn(cx, cy)
    }
    for (const s of this.sparks) {
      if (!s.active) continue
      s.life -= dt
      if (s.life <= 0) { s.active = false; continue }
      s.x += s.vx * dt; s.y += s.vy * dt
      s.vx *= 0.92; s.vy *= 0.92
    }
  }

  private spawn(cx: number, cy: number): void {
    let s = this.sparks.find((q) => !q.active)
    if (!s) {
      if (this.sparks.length >= SPARK_CAP) return // 硬上限 30（契約逐字），滿了就不再新增
      s = { active: false, x: 0, y: 0, vx: 0, vy: 0, life: 0, maxLife: 1 }
      this.sparks.push(s)
    }
    const ang = Math.random() * Math.PI * 2
    const spd = 14 + Math.random() * 22
    s.active = true
    s.x = cx; s.y = cy
    s.vx = Math.cos(ang) * spd; s.vy = Math.sin(ang) * spd
    s.maxLife = 0.7 + Math.random() * 0.4 // 契約「壽命約 0.9 秒」，取平均值附近的隨機區間
    s.life = s.maxLife
  }

  /** 畫在 (x, y)（螢幕 CSS px，通常是 map.project() 出來的座標）。px＝邏輯像素放大倍率
   * （RetroMap.tsx 的 orbScale，與地圖圖塊／軌跡線寬共用同一個基準值）。opts.dim＝
   * ORBPOS_CONTRACT.md §B「定位中」外觀：本體半透明（只調整光球本體，地面投影／火花／閃爍十字星
   * 維持原本可見度，讓玩家仍看得出「東西還在這附近、只是定位還沒確定」，而不是整個光點一起變淡到
   * 幾乎看不見）。 */
  draw(ctx: CanvasRenderingContext2D, x: number, y: number, px: number, opts: { reducedMotion?: boolean; dim?: boolean }): void {
    const reducedMotion = !!opts.reducedMotion

    // 浮動：離散兩態（0 格／上移 1 格）每 ~400ms 切換一次，跟脈動共用同一個 cycleT（見上方欄位
    // 註解）——契約要的是「每 ~400ms 上下浮動 1 格」的階梯感，不是平滑的正弦補間（那會失去像素
    // 風格特有的「定格動畫」質感，比照舊版角色走路幀的整數格切換手法）。
    const phase = reducedMotion ? 0 : Math.floor(this.cycleT / 0.4) % 2
    const floatOffset = phase === 1 ? -1 * px : 0
    const frame: 0 | 1 = phase === 1 ? 1 : 0

    // 地面投影固定在原點（不隨浮動位移），先畫最底層。
    drawGroundShadow(ctx, x, y, px)

    // 移動火花：畫在光球本體之前（噴出、飄散開才看得見邊緣，不會整團被光球蓋住，理由同
    // cute/orb.ts 粒子的畫層順序）。1 格方塊、離散色階、不透明（契約「不用半透明漸層」）。
    if (!reducedMotion && this.sparks.length) {
      ctx.save()
      for (const s of this.sparks) {
        if (!s.active) continue
        const lived = 1 - s.life / s.maxLife // 0→1：0=剛噴出，1=即將消失
        const stage = Math.min(3, Math.floor(lived * 4))
        ctx.fillStyle = SPARK_COLORS[stage]
        ctx.fillRect(Math.round(s.x - px / 2), Math.round(s.y - px / 2), Math.ceil(px), Math.ceil(px))
      }
      ctx.restore()
    }

    if (opts.dim) {
      ctx.save()
      ctx.globalAlpha = 0.5
      drawOrbBody(ctx, x, y + floatOffset, frame, px)
      ctx.restore()
    } else {
      drawOrbBody(ctx, x, y + floatOffset, frame, px)
    }

    // 四角閃爍十字星：只在非 reduced-motion 時輪替顯示一個角落。
    if (!reducedMotion) drawFlickerCross(ctx, x, y + floatOffset, px, this.flickerIdx)
  }
}
