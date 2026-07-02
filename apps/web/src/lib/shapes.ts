// 圖形辨識（畫圓已用滑動；此為三角/四角/五芒星）。概念類似手機滑動解鎖：
// 顯示半透明底圖提示，玩家一筆畫；辨識用「重取樣 + 正規化 + 循環位移比對」，位置/大小/起點/方向皆容忍。
export type Pt = { x: number; y: number }

// 各圖形「描繪路徑」的頂點（正規化到 [-1,1]，含收尾回到起點）
export function shapePath(shape: number): Pt[] {
  const polygon = (n: number, startDeg: number): Pt[] => {
    const v: Pt[] = []
    for (let k = 0; k < n; k++) {
      const a = ((startDeg + (k * 360) / n) * Math.PI) / 180
      v.push({ x: Math.cos(a), y: Math.sin(a) })
    }
    return v
  }
  if (shape === 5) {
    const o = polygon(5, -90) // 5 個外點（尖端朝上）
    const order = [0, 2, 4, 1, 3] // 一筆畫五芒星：跳一點連線
    const path = order.map((i) => o[i])
    path.push(path[0])
    return path
  }
  const n = shape === 4 ? 4 : 3
  const start = shape === 4 ? -135 : -90 // 四角＝軸對齊正方；三角＝尖端朝上
  const v = polygon(n, start)
  v.push(v[0])
  return v
}

// 導引虛線的「預設」外頂點半徑（占框半徑比例）。量測自後台三張魔法陣底圖的實際線條位置，
// 讓虛線貼齊底圖（否則虛線畫得比底圖大，會誤導成「畫在外圈才對」但其實要畫在裡面）。
// 後台可用「效果管理 → 導引縮放」逐一覆寫（見 guideScaleFor）。
export const SHAPE_GUIDE_SCALE: Record<number, number> = { 3: 0.56, 4: 0.58, 5: 0.59 }

// 取本次圖形的導引縮放：優先讀後台覆寫（effect_assets slug `interaction.shape.scale{3|4|5}`，
// 值存百分比字串如 "59"），否則回退預設。解析：>1.5 視為百分比（/100）、否則視為比例；夾在 [0.2, 1.0]。
export function guideScaleFor(shape: number, assets?: Record<string, string>): number {
  const raw = assets?.[`interaction.shape.scale${shape}`]
  if (raw != null && String(raw).trim() !== '') {
    let n = parseFloat(String(raw))
    if (isFinite(n) && n > 0) {
      if (n > 1.5) n /= 100
      return Math.min(1, Math.max(0.2, n))
    }
  }
  return SHAPE_GUIDE_SCALE[shape] ?? 0.59
}

// SVG 底圖用：把圖形頂點縮放到 size 方框。scale＝外頂點半徑占框半徑比例（未給則用 SHAPE_GUIDE_SCALE 預設）。
export function shapeSvgPoints(shape: number, size: number, scale?: number): string {
  const c = size / 2
  const r = c * (scale ?? SHAPE_GUIDE_SCALE[shape] ?? 0.59)
  return shapePath(shape).map((p) => `${(c + p.x * r).toFixed(1)},${(c + p.y * r).toFixed(1)}`).join(' ')
}

export function shapeName(shape: number): string {
  return shape === 5 ? '五芒星' : shape === 4 ? '四角形' : '三角形'
}

function pathLen(pts: Pt[]): number {
  let L = 0
  for (let i = 1; i < pts.length; i++) L += Math.hypot(pts[i].x - pts[i - 1].x, pts[i].y - pts[i - 1].y)
  return L
}

// 等弧長重取樣為 n 點
function resample(pts: Pt[], n: number): Pt[] {
  if (pts.length < 2) return Array.from({ length: n }, () => pts[0] ?? { x: 0, y: 0 })
  const I = pathLen(pts) / (n - 1)
  const out: Pt[] = [pts[0]]
  let D = 0
  const src = pts.map((p) => ({ ...p }))
  for (let i = 1; i < src.length; i++) {
    const d = Math.hypot(src[i].x - src[i - 1].x, src[i].y - src[i - 1].y)
    if (D + d >= I && d > 0) {
      const t = (I - D) / d
      const q = { x: src[i - 1].x + t * (src[i].x - src[i - 1].x), y: src[i - 1].y + t * (src[i].y - src[i - 1].y) }
      out.push(q)
      src.splice(i, 0, q)
      D = 0
    } else D += d
  }
  while (out.length < n) out.push(src[src.length - 1])
  return out.slice(0, n)
}

// 置中 + 以 RMS 半徑縮放（大小/位置無關）
function normalize(pts: Pt[]): Pt[] {
  const c = pts.reduce((a, p) => ({ x: a.x + p.x, y: a.y + p.y }), { x: 0, y: 0 })
  c.x /= pts.length; c.y /= pts.length
  const cen = pts.map((p) => ({ x: p.x - c.x, y: p.y - c.y }))
  let s = Math.sqrt(cen.reduce((a, p) => a + p.x * p.x + p.y * p.y, 0) / pts.length)
  if (s < 1e-6) s = 1e-6
  return cen.map((p) => ({ x: p.x / s, y: p.y / s }))
}

const N = 48

// 回傳「平均對應點距離」（越小越像）；容忍起點不同（循環位移）與方向（正/反）
export function shapeMatchDistance(drawn: Pt[], shape: number): number {
  const a = normalize(resample(drawn, N))
  const b = normalize(resample(shapePath(shape), N))
  let best = Infinity
  for (const rev of [false, true]) {
    const bb = rev ? b.slice().reverse() : b
    for (let sft = 0; sft < N; sft++) {
      let sum = 0
      for (let i = 0; i < N; i++) {
        const p = a[i], q = bb[(i + sft) % N]
        sum += Math.hypot(p.x - q.x, p.y - q.y)
      }
      const avg = sum / N
      if (avg < best) best = avg
    }
  }
  return best
}

export const SHAPE_MATCH_THRESHOLD = 0.24 // 絕對距離上限（與伺服器一致；三角↔四角跨類約 0.28，故壓在其下）

// 找最接近的圖形 + 次佳（供 margin 判別，避免三角/四角互相誤判）
export function recognizeShape(drawn: Pt[]): { shape: number; dist: number; second: number } {
  let best = Infinity, second = Infinity, bestShape = 0
  for (const s of [3, 4, 5]) {
    const d = shapeMatchDistance(drawn, s)
    if (d < best) { second = best; best = d; bestShape = s }
    else if (d < second) second = d
  }
  return { shape: bestShape, dist: best, second }
}

// 前端本地判定「畫對目標圖形」：目標須為最接近、領先次佳 >=0.05、且絕對距離 <= 上限（與伺服器同規則）
export function shapeAccepts(drawn: Pt[], target: number): boolean {
  const r = recognizeShape(drawn)
  return r.shape === target && r.second - r.dist >= 0.05 && r.dist <= SHAPE_MATCH_THRESHOLD
}
