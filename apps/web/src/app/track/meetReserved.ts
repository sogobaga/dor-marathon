// 團練同步跑（Group Run Live）P3 第二輪——量出「疊在 skin 地圖疊層 canvas 上方的控制鈕／橫幅卡片」的矩形，
// 讓 meetCanvasPeers 的泡泡與「+N」徽章避開它們（縮放「＋」「−」鈕、回到目前位置鈕、路線規劃條上的清除鈕、警告橫幅的 ✕、目標小卡的按鈕…）。
//
// 不寫死任何尺寸／位置：每次都從真實 DOM 量（getBoundingClientRect），所以 track/page.tsx 之後怎麼搬控制鈕（例如路線規劃條出現時
// 以 --dor-map-ctrl-top 把縮放鈕往下推）都自動跟上。量到的矩形換算成「疊層 canvas 的 CSS px 座標」（扣掉 canvas 左上角；有 CSS scale
// 時以 rect／clientWidth 還原比例），並外擴 RESERVED_PAD px。
//
// 量什麼（只收「與疊層 canvas 有交集、看得見」的）：
//   1) 風格地圖自己的 MapLibre 控制（.maplibregl-ctrl：例如 scifi 右上角的「ⓘ」授權鈕）。
//   2) 文件裡所有互動元件（button／a[href]／input／select／textarea／summary／[role=button]／[data-meet-reserve]），
//      排除風格地圖外層本身與被它隱藏的 Leaflet 背景地圖（#gps-map）。
//   3) 橫幅／小卡：#track-map-area 直屬、position:absolute|fixed 且 pointer-events:none 的外層（警告／錯誤／路線規劃條／目標小卡），
//      其底下 pointer-events:auto 的那張卡片整張算（卡片本身不是 button，但泡泡畫在它上面會蓋住字）。
// 看不見的（display:none／visibility:hidden／opacity:0／寬高<1）一律略過——例如專注模式（data-scifi-focus-hide）把縮放鈕藏起來時就不再保留。
//
// 用法：createReservedTracker(() => canvasRef.current)；每幀呼叫 .get(Date.now())——內部每 ≥1 秒才真的重量一次（或 invalidate() 之後，
// 例如 ResizeObserver 觸發時）。任何錯誤（元素不在、瀏覽器不支援）都只回上一次的結果，永不 throw。

import type { Rect } from './meetCanvasPeers'

/** 外擴內距（CSS px）。 */
export const RESERVED_PAD = 4
const MAX_RECTS = 24
const INTERACTIVE = 'button, a[href], input, select, textarea, summary, [role="button"], [data-meet-reserve]'

export function collectReservedRects(canvas: HTMLCanvasElement | null | undefined, pad: number = RESERVED_PAD): Rect[] {
  try {
    if (!canvas || typeof document === 'undefined') return []
    const cr = canvas.getBoundingClientRect()
    if (!(cr.width > 1 && cr.height > 1)) return []
    const sx = canvas.clientWidth > 0 ? cr.width / canvas.clientWidth : 1
    const sy = canvas.clientHeight > 0 ? cr.height / canvas.clientHeight : 1
    const wrap = canvas.parentElement // 風格地圖外層：MapLibre 容器＋疊層 canvas
    const area = canvas.closest('#track-map-area')
    const out: Rect[] = []
    const seen = new Set<Element>()
    const take = (el: Element) => {
      if (out.length >= MAX_RECTS || seen.has(el)) return
      seen.add(el)
      const r = el.getBoundingClientRect()
      if (!(r.width >= 1 && r.height >= 1)) return
      if (r.right <= cr.left || r.left >= cr.right || r.bottom <= cr.top || r.top >= cr.bottom) return // 與疊層 canvas 沒有交集
      const cs = getComputedStyle(el)
      if (cs.display === 'none' || cs.visibility === 'hidden' || Number(cs.opacity) === 0) return
      out.push({ x: (r.left - cr.left) / sx - pad, y: (r.top - cr.top) / sy - pad, w: r.width / sx + 2 * pad, h: r.height / sy + 2 * pad })
    }
    // 1) 風格地圖自己的 MapLibre 控制
    wrap?.querySelectorAll('.maplibregl-ctrl').forEach(take)
    // 2) 互動元件（縮放鈕、回到目前位置、橫幅上的按鈕…）
    document.querySelectorAll(INTERACTIVE).forEach((el) => {
      if (wrap && wrap.contains(el)) return
      if (el.closest('#gps-map')) return
      take(el)
    })
    // 3) 橫幅／小卡（pointer-events:none 外層底下 pointer-events:auto 的卡片）
    if (area) {
      for (const w of Array.from(area.children)) {
        if (w === wrap || (wrap && w.contains(wrap))) continue
        const ws = getComputedStyle(w)
        if (ws.pointerEvents !== 'none' || (ws.position !== 'absolute' && ws.position !== 'fixed')) continue
        for (const c of Array.from(w.children)) if (getComputedStyle(c).pointerEvents === 'auto') take(c)
      }
    }
    return out
  } catch {
    return []
  }
}

export interface ReservedTracker {
  /** 取目前的保留區；內部每 ≥ everyMs 才重量一次（或 invalidate 之後）。永不 throw。 */
  get(nowMs: number): Rect[]
  /** 下一次 get 一定重量（ResizeObserver／版面變動時呼叫）。 */
  invalidate(): void
  /** 最近一次量到的結果（複本，debug 用）。 */
  last(): Rect[]
  /** 量測次數與耗時（毫秒；debug／效能驗證用——每秒至多一次）。 */
  stats(): { count: number; lastMs: number; maxMs: number }
}

export function createReservedTracker(getCanvas: () => HTMLCanvasElement | null | undefined, everyMs = 1000): ReservedTracker {
  let at = Number.NEGATIVE_INFINITY
  let dirty = true
  let cache: Rect[] = []
  let count = 0, lastMs = 0, maxMs = 0
  const now = () => (typeof performance !== 'undefined' ? performance.now() : Date.now())
  return {
    get(nowMs: number): Rect[] {
      try {
        if (dirty || !Number.isFinite(nowMs) || Math.abs(nowMs - at) >= everyMs) {
          const t0 = now()
          cache = collectReservedRects(getCanvas())
          lastMs = now() - t0
          if (lastMs > maxMs) maxMs = lastMs
          count++
          at = Number.isFinite(nowMs) ? nowMs : at
          dirty = false
        }
      } catch { /* 保留上一次的結果 */ }
      return cache
    },
    invalidate() { dirty = true },
    last() { return cache.map((r) => ({ x: r.x, y: r.y, w: r.w, h: r.h })) },
    stats() { return { count, lastMs, maxMs } },
  }
}
