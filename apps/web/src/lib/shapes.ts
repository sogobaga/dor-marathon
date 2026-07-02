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

// SVG 底圖用：把圖形頂點縮放到 size 方框
export function shapeSvgPoints(shape: number, size: number, pad = 24): string {
  const r = size / 2 - pad, c = size / 2
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

export const SHAPE_MATCH_THRESHOLD = 0.34 // 平均距離低於此視為畫對（可調）
