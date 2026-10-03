// 團練同步跑——Leaflet 版他人亮點圖層（契約 docs/runmeet/GROUP_RUN_LIVE_CONTRACT.md §7「Leaflet（P2）」）。
//
// 預設風格（無 skin）的地圖就是 Leaflet；三套 skin 生效時 #gps-map 只是 visibility:hidden 的背景地圖，
// 但 skin 圖塊失敗退回 Leaflet 時亮點必須還在（契約 §9 P3），所以這個圖層在團練模式下一律運作
// （地圖被 skin 蓋住時降頻到 2 Hz，見 isMapHidden）。
//
// 規格重點：
//   · L.layerGroup（page.tsx 緊鄰 kmMarkersRef 建立，這裡只管裡面的 marker）＋ Map<n, CircleMarker>＋setLatLng。
//   · 他人＝橘點 #FF8A3D＋白色 2px 外框；沒有軌跡線；自己（綠點）每次加入新亮點後 bringToFront。
//   · ⚠️ tooltip 一律傳 DOM 元素並用 textContent 設文字（C2）——名稱雖已由伺服器消毒，仍不信任任何字串進 innerHTML。
//   · 陳舊度（契約 §6／§7）：(now−rxAt)/1000+ageS；≥fadeS → 60% 透明＋尾綴「· 35s」；≥grayS → 灰色空心環、
//     泡泡改「訊號中斷」；≥dropS → 移除。門檻來自伺服器（peers.stale）。
//   · 內插：由上一點線性滑到新點（時間長度＝該亮點兩次更新的間隔≈1.0×iv，以時間為準、不依賴幀率）；
//     prefers-reduced-motion → 直接跳。
//   · 整個圖層不碰 React state（讀 meetPeersRef），不引入任何重繪。
//
// 名字泡泡（永久 tooltip）擺放 v2（2026-10-03，E2E 50 人實測：預設風格沒有 skin 那套避碰，15 個泡泡會整疊蓋在一起）：
//   · 方向候選 top（預設）→ right → left → bottom，用 Leaflet tooltip 的 direction＋對應 offset（箭頭永遠指向亮點）；第一個「空位」勝出。
//     「空位」＝與已放置的泡泡重疊不超過 2 px（雙軸皆 >2 才算衝突）、且不蓋住自己（綠點）圓心。
//   · 泡泡尺寸：tooltip 元素存在就讀真實大小（getBoundingClientRect），否則估算 h=30、w=14+CJK 每字 12＋其他每字 7。
//   · 只有「目前在地圖可視範圍內」的亮點競爭名額，離螢幕的亮點不佔名額。
//   · 螢幕內 ≤5 人（小團體並肩跑，主要情境）：每個人都有永久名字——第一個空位方向，真的都沒空位就退回 top 並接受重疊。
//   · 螢幕內 >5 人：由近到遠（與自己的距離）貪婪放置、最多 15 個永久泡泡；沒有空位的亮點只留點（點一下／hover 仍顯示名字）。
//   · 穩定性：泡泡目前的方向若仍是空位就繼續用（黏著，避免閃動）；DOM 尺寸只在重排（rank）內一次讀完、絕不逐幀讀，
//     而且依文字快取 30 秒（泡泡大小只取決於文字；文字變了／網頁字型載入完成就作廢）——穩態重排完全不碰 DOM、不會強制 layout。
//   · 投影不逐點呼叫 Leaflet（冷程式碼每點要配置好幾個物件）：一次 Leaflet 校準＋直接 Web Mercator 算式，並照 Leaflet 的整數取整規則，
//     算出來的容器座標與 latLngToContainerPoint 逐像素一致（否則 ±1 px 的誤差疊上 2 px 容忍度，DOM 上會量到 >2 px 的重疊）。
//   · 重排時機：地圖 zoomend／moveend（合併到下一幀）、亮點新增／移除，加上原本每 ≥1 秒一次。
//     縮放動畫期間（zoomstart→zoomend）一律不綁／不改泡泡、不動亮點位置——動畫中新建的泡泡會從 pane 原點滑進來（疊在一起 ≤0.5 s）。
//   · 小地方偏好「整個框在可視範圍內」的方向（靠邊的亮點不把名字甩出畫面）；沒有這種方向才接受被邊緣裁掉的候選。

import type { MutableRefObject } from 'react'
import type { MeetLivePeers, PeerDot } from './meetLiveTypes'
import { MEET_GRAY, MEET_ORANGE, haversineM, staleLevel, staleSeconds, truncName, type StaleLevel } from './meetLiveUtil'

const MAX_PERMANENT_TIPS = 15
const SMALL_GROUP_MAX = 5 // 螢幕內人數 ≤ 此值：每個人都保證有永久名字
const FRAME_MS = 45 // ≈20 fps 上限（50 個 SVG circle 逐幀 setLatLng 沒必要 60 fps）
const IDLE_MS = 500 // 沒有亮點／地圖被 skin 蓋住時的檢查間隔
const RERANK_MS = 1000 // 週期性重排間隔（另有 zoomend／moveend／亮點增減觸發）
const ZOOM_WATCHDOG_MS = 1500 // zoomstart 之後超過這個時間還沒 zoomend（動畫被中斷）→ 不再卡住重排
const SIZE_TTL_MS = 30000 // 從 DOM 讀到的泡泡尺寸的快取時間（另外：文字變了、網頁字型載入完成（document.fonts loadingdone）就立刻作廢）

// ── 名字泡泡的幾何（Leaflet 預設 tooltip 樣式：padding 6＋border 1，12px 粗體；CSS 另有 ±6 的方向 margin）──
type Dir = 'top' | 'right' | 'left' | 'bottom'
const DIRS: readonly Dir[] = ['top', 'right', 'left', 'bottom'] // 候選順序
// 每個方向的 offset：讓箭頭尖端落在亮點邊緣（亮點半徑 8）——top 沿用原本的 [0,-8]；其餘依對稱。
const DIR_OFFSET: Record<Dir, [number, number]> = { top: [0, -8], right: [8, 0], left: [-8, 0], bottom: [0, 8] }
const BOX_GAP = 14 // 亮點圓心到泡泡框近邊的距離＝|offset| 8＋CSS 方向 margin 6
const OVERLAP_TOL = 2 // 兩個框重疊「超過」2 px（兩軸皆是）才算衝突——剛好貼著不算
const SELF_PAD = 4 // 泡泡框（外擴這麼多）不可蓋住自己綠點的圓心
const EST_H = 30
const EST_W_BASE = 14
const EST_W_CJK = 12
const EST_W_OTHER = 7

interface Box { x0: number; y0: number; x1: number; y1: number }
interface Size { w: number; h: number }

interface Entry {
  marker: any // eslint-disable-line @typescript-eslint/no-explicit-any -- Leaflet 以 CDN 載入（window.L），沒有型別
  el: HTMLElement // tooltip 內容（DOM 元素；契約 §5 C2）
  text: string
  level: StaleLevel
  permanent: boolean
  dir: Dir // 目前綁定的 tooltip 方向（非永久時固定 top）
  bound: boolean // 是否已 bindTooltip（縮放動畫期間建立的亮點延到 zoomend 後才綁）
  needsUpdate: boolean // 縮放動畫期間文字變了：tooltip.update() 延到動畫結束
  sz: Size | null // 最近一次從 DOM 讀到的泡泡真實尺寸（依文字快取，見 rank 步驟 2）
  szText: string
  szAt: number
  rxAt: number
  from: [number, number]
  to: [number, number]
  cur: [number, number]
  t0: number
  dur: number
}

interface Cand { n: number; e: Entry; x: number; y: number; d: number; size: Size }

export interface MeetPeerRenderer {
  start(): void
  destroy(): void
}

/** 泡泡尺寸估算（tooltip 還不存在時用）：h=30、w=14＋CJK 每字 12＋其他每字 7。 */
export function estimateTipSize(text: string): Size {
  let w = EST_W_BASE
  for (const ch of text) w += (ch.codePointAt(0) ?? 0) > 0x2e7f ? EST_W_CJK : EST_W_OTHER
  return { w, h: EST_H }
}

/** 某方向的泡泡框（容器座標；p＝亮點圓心）。 */
function boxAt(p: { x: number; y: number }, s: Size, dir: Dir): Box {
  switch (dir) {
    case 'top': return { x0: p.x - s.w / 2, x1: p.x + s.w / 2, y0: p.y - BOX_GAP - s.h, y1: p.y - BOX_GAP }
    case 'bottom': return { x0: p.x - s.w / 2, x1: p.x + s.w / 2, y0: p.y + BOX_GAP, y1: p.y + BOX_GAP + s.h }
    case 'right': return { x0: p.x + BOX_GAP, x1: p.x + BOX_GAP + s.w, y0: p.y - s.h / 2, y1: p.y + s.h / 2 }
    default: return { x0: p.x - BOX_GAP - s.w, x1: p.x - BOX_GAP, y0: p.y - s.h / 2, y1: p.y + s.h / 2 }
  }
}
/** 與任何已放置的框重疊超過 OVERLAP_TOL（兩軸皆是）。 */
function blocked(b: Box, placed: Box[]): boolean {
  for (let i = 0; i < placed.length; i++) {
    const p = placed[i]
    if (Math.min(b.x1, p.x1) - Math.max(b.x0, p.x0) > OVERLAP_TOL && Math.min(b.y1, p.y1) - Math.max(b.y0, p.y0) > OVERLAP_TOL) return true
  }
  return false
}
// Web Mercator（Leaflet EPSG:3857）的世界像素：x=(lng+180)/360·S、y=(0.5−ln((1+sinφ)/(1−sinφ))/4π)·S，S=256·2^zoom；緯度夾在 ±85.0511287798（同 Leaflet）。
function lngPx(lng: number, scale: number): number { return (lng + 180) / 360 * scale }
function latPy(lat: number, scale: number): number {
  const s = Math.sin(Math.max(-85.0511287798, Math.min(85.0511287798, lat)) * Math.PI / 180)
  return (0.5 - Math.log((1 + s) / (1 - s)) / (4 * Math.PI)) * scale
}
const ZERO_SIZE: Size = { w: 0, h: 0 }
function coversPoint(b: Box, p: { x: number; y: number } | null): boolean {
  return !!p && p.x >= b.x0 - SELF_PAD && p.x <= b.x1 + SELF_PAD && p.y >= b.y0 - SELF_PAD && p.y <= b.y1 + SELF_PAD
}

export function createMeetPeerRenderer(opts: {
  L: any // eslint-disable-line @typescript-eslint/no-explicit-any
  layer: any // eslint-disable-line @typescript-eslint/no-explicit-any -- L.layerGroup（已 addTo(map)）
  peersRef: MutableRefObject<MeetLivePeers>
  getSelf: () => { lat: number; lng: number } | null
  bringSelfToFront: () => void
  isMapHidden: () => boolean
}): MeetPeerRenderer {
  const { L, layer, peersRef } = opts
  const entries = new Map<number, Entry>()
  let raf: number | null = null
  let timeout: ReturnType<typeof setTimeout> | null = null
  let stopped = false
  let lastFrame = 0
  let lastRank = 0
  let rankDirty = true
  let reduced = false
  let zooming = false // zoomstart → zoomend：動畫期間不綁／不改泡泡、不動亮點位置
  let zoomStartAt = 0
  let boundMap: any = null // eslint-disable-line @typescript-eslint/no-explicit-any -- 已掛上事件監聽的 Leaflet map
  let mql: MediaQueryList | null = null
  const onMql = () => { reduced = !!mql?.matches }
  try {
    mql = window.matchMedia('(prefers-reduced-motion: reduce)')
    reduced = mql.matches
    mql.addEventListener?.('change', onMql)
  } catch { /* 舊瀏覽器：視為不減少動態 */ }

  // 網頁字型（Noto Sans TC）晚載入會改變泡泡寬度：作廢所有尺寸快取並重排
  const onFontsDone = () => { entries.forEach((e) => { e.sz = null }); rankDirty = true }
  let fontsTarget: EventTarget | null = null
  try {
    fontsTarget = (document as unknown as { fonts?: EventTarget }).fonts ?? null
    fontsTarget?.addEventListener('loadingdone', onFontsDone)
  } catch { fontsTarget = null }

  // ── 地圖事件：zoomstart／zoomend／moveend／resize（只設旗標，實際重排合併到下一幀）──
  const onZoomStart = () => { zooming = true; zoomStartAt = Date.now() }
  const onZoomEnd = () => { zooming = false; rankDirty = true }
  const onMapMoved = () => { rankDirty = true }
  function bindMapEvents() {
    const map = layer._map // layerGroup 加到地圖後才有
    if (!map || map === boundMap) return
    try {
      map.on('zoomstart', onZoomStart)
      map.on('zoomend', onZoomEnd)
      map.on('moveend', onMapMoved)
      map.on('resize', onMapMoved)
      boundMap = map
      if (map._animatingZoom) onZoomStart() // 掛上監聽時剛好在縮放動畫中：等 zoomend
    } catch { /* ignore */ }
  }
  function unbindMapEvents() {
    const map = boundMap
    boundMap = null
    if (!map) return
    try { map.off('zoomstart', onZoomStart); map.off('zoomend', onZoomEnd); map.off('moveend', onMapMoved); map.off('resize', onMapMoved) } catch { /* ignore */ }
  }

  // ── dev-only 診斷（非 production 或 ?dev=1）：重排耗時與 DOM 讀取次數，只給 E2E 驗「重排 <3 ms、不逐幀讀 DOM」用 ──
  type RankLog = { t: number; ms: number; peers: number; onScreen: number; permanent: number; reads: number; changed: number }
  let dbg: { log: RankLog[] } | null = null
  const dbgApi = {
    rankLog: () => (dbg ? dbg.log.slice() : []),
    /** 立刻重排一次並回傳耗時（ms）；縮放動畫中不會做事。 */
    rankNow: () => { const t = performance.now(); if (!zooming) rank(); return performance.now() - t },
    state: () => Array.from(entries.entries()).map(([n, e]) => ({ n, text: e.text, permanent: e.permanent, dir: e.dir, bound: e.bound })),
  }
  try {
    let dev = process.env.NODE_ENV !== 'production'
    if (!dev) dev = new URLSearchParams(window.location.search).get('dev') === '1'
    if (dev) { dbg = { log: [] }; (window as unknown as { __meetPeerDebug?: unknown }).__meetPeerDebug = dbgApi }
  } catch { dbg = null }

  /** 綁 tooltip：permanent＝永久名字；dir＝方向（offset 依方向配對，箭頭指向亮點）。 */
  function bindTip(e: Entry, permanent: boolean, dir: Dir = 'top') {
    // 舊的永久 tooltip：Leaflet 移除時會先淡出 200 ms 才從 DOM 拿掉——期間舊名字還掛在原位、可能蓋到剛重排出來的新名字，
    // 也讓「縮放／平移後名額換成螢幕內的人」晚了 200 ms 才在 DOM 上成立。這裡直接把舊容器拿掉（Leaflet 之後的延遲移除是 no-op）。
    let oldEl: HTMLElement | null = null
    if (e.permanent) { try { oldEl = e.marker.getTooltip()?.getElement?.() ?? null } catch { oldEl = null } }
    try { e.marker.unbindTooltip() } catch { /* ignore */ }
    if (oldEl) { try { oldEl.remove() } catch { /* ignore */ } }
    // ⚠️ 傳 DOM 元素（不是字串）——Leaflet 對字串會走 innerHTML。
    e.marker.bindTooltip(e.el, { permanent, direction: dir, offset: DIR_OFFSET[dir].slice(), className: 'dor-meet-tip' })
    e.permanent = permanent
    e.dir = dir
    e.bound = true
    e.needsUpdate = false
    try { e.marker.getTooltip()?.setOpacity(e.level === 'ok' ? 0.95 : 0.4) } catch { /* ignore */ }
  }

  /** 已是永久名字、只換方向：就地改 tooltip 的 direction／offset 再 update()（不重建 DOM）；失敗才重綁。 */
  function setDir(e: Entry, dir: Dir) {
    if (e.permanent && e.bound) {
      if (e.dir === dir) return
      try {
        const tt = e.marker.getTooltip()
        if (tt?.options) {
          tt.options.direction = dir
          tt.options.offset = DIR_OFFSET[dir].slice()
          tt.update()
          e.dir = dir
          e.needsUpdate = false
          return
        }
      } catch { /* 退回重綁 */ }
    }
    bindTip(e, true, dir)
  }

  function create(dot: PeerDot, now: number): Entry {
    const marker = L.circleMarker([dot.lat, dot.lng], {
      radius: 8, color: '#fff', weight: 2, opacity: 1, fillColor: MEET_ORANGE, fillOpacity: 1,
      interactive: true, bubblingMouseEvents: false,
    })
    const el = document.createElement('div')
    el.style.cssText = 'font:700 12px/1.3 "Noto Sans TC",sans-serif;white-space:nowrap;color:#1a1a1a'
    const text = truncName(dot.name)
    el.textContent = text // textContent：名稱當純文字（C2）
    marker.addTo(layer)
    const e: Entry = {
      marker, el, text, level: 'ok', permanent: false, dir: 'top', bound: false, needsUpdate: false, sz: null, szText: '', szAt: 0, rxAt: dot.rxAt,
      from: [dot.lat, dot.lng], to: [dot.lat, dot.lng], cur: [dot.lat, dot.lng], t0: now, dur: 1,
    }
    if (!zooming) bindTip(e, false) // 縮放動畫期間不綁——zoomend 後第一次重排補綁
    return e
  }

  function retarget(e: Entry, dot: PeerDot, now: number) {
    const prevRx = e.rxAt
    e.rxAt = dot.rxAt
    const target: [number, number] = [dot.lat, dot.lng]
    if (reduced) { e.from = target; e.to = target; e.t0 = now; e.dur = 1; return }
    e.from = e.cur
    e.to = target
    e.t0 = now
    e.dur = Math.min(12000, Math.max(600, dot.rxAt - prevRx)) // ≈1.0×iv（兩次更新的間隔）
  }

  function refreshTip(e: Entry) {
    if (!e.bound) return
    if (zooming) { e.needsUpdate = true; return } // 動畫中重算位置會把泡泡拉回舊投影座標——延到 zoomend 後
    e.needsUpdate = false
    try { e.marker.getTooltip()?.update() } catch { /* ignore */ }
  }

  function applyLevel(e: Entry, dot: PeerDot, s: number, level: StaleLevel) {
    const name = truncName(dot.name)
    const text = level === 'gray' ? '訊號中斷' : level === 'fade' ? `${name} · ${Math.round(s)}s` : name
    if (text !== e.text) {
      e.text = text
      e.el.textContent = text
      refreshTip(e)
    }
    if (level !== e.level) {
      e.level = level
      if (level === 'gray') e.marker.setStyle({ fillColor: MEET_GRAY, fillOpacity: 0, color: MEET_GRAY, opacity: 0.95, weight: 2 }) // 灰色空心環
      else if (level === 'fade') e.marker.setStyle({ fillColor: MEET_ORANGE, fillOpacity: 0.4, color: '#fff', opacity: 0.4, weight: 2 }) // 60% 透明
      else e.marker.setStyle({ fillColor: MEET_ORANGE, fillOpacity: 1, color: '#fff', opacity: 1, weight: 2 })
      try { e.marker.getTooltip()?.setOpacity(level === 'ok' ? 0.95 : 0.4) } catch { /* ignore */ }
    }
  }

  /** 讀 tooltip 真實大小（元素存在且有尺寸才算）；否則回 null 由呼叫端估算。 */
  function readTipSize(e: Entry): Size | null {
    try {
      const el: HTMLElement | undefined = e.marker.getTooltip?.()?.getElement?.()
      if (!el) return null
      const r = el.getBoundingClientRect()
      return r.width > 0 && r.height > 0 ? { w: r.width, h: r.height } : null
    } catch { return null }
  }

  /** 沒有 map 時（layerGroup 還沒加到地圖）的退回行為：只限數量，全部 top。 */
  function rankLegacy(self: { lat: number; lng: number } | null) {
    const arr: { n: number; e: Entry; d: number }[] = []
    entries.forEach((e, n) => arr.push({ n, e, d: self ? haversineM(self.lat, self.lng, e.cur[0], e.cur[1]) : n }))
    arr.sort((a, b) => a.d - b.d)
    arr.forEach((x, i) => { const want = i < MAX_PERMANENT_TIPS; if (want !== x.e.permanent || !x.e.bound) bindTip(x.e, want) })
  }

  /**
   * 重排永久名字泡泡（規格見檔頭）。流程：投影所有亮點 → 篩出可視範圍內的 → 一次讀完真實尺寸 → 由近到遠貪婪放置 → 最後才寫 DOM。
   * 讀寫分離（先讀完再寫），所以一次重排最多觸發一次強制 layout；逐幀的 step() 完全不讀 DOM。
   */
  function rank() {
    const t0 = dbg ? performance.now() : 0
    let reads = 0, changed = 0, onScreenCount = 0, permanentCount = 0
    try {
      const self = opts.getSelf()
      const map = layer._map
      if (!map) { rankLegacy(self); return }
      const sz = map.getSize()
      const view = { x: Number(sz?.x), y: Number(sz?.y) }
      if (!(view.x > 0) || !(view.y > 0)) { rankLegacy(self); return }
      // 投影：Leaflet 的 latLngToContainerPoint 每次呼叫都會配置好幾個 Point／LatLng，一秒才跑一次的冷程式碼 50 個點就要 ~1 ms——
      // 預設 CRS（EPSG:3857）改成「一次 Leaflet 校準（地圖中心）＋直接 Web Mercator 算式」：容器座標 = 中心的容器座標 + (世界像素差)。
      // 非 3857 或校準失敗就退回逐點呼叫 Leaflet。
      const zoomScale = 256 * Math.pow(2, Number(map.getZoom()))
      // ⚠️ Leaflet 的圖層座標是「先把世界像素四捨五入成整數、再減去整數的 pixelOrigin」，容器座標再加整數的 pane 位移——
      //    所以 容器座標 = round(世界像素) + K，K = 校準點的容器座標 − round(校準點的世界像素)（整數）。照這個做才會與 Leaflet 實際擺放 tooltip 的位置逐像素一致
      //    （不四捨五入會有 ±1 px 的落差，再疊上 2 px 重疊容忍度就會在 DOM 上量到 >2 px 的重疊）。
      let kx = NaN, ky = NaN, direct = false
      try {
        const c = map.getCenter(), rc = map.latLngToContainerPoint(c)
        kx = rc.x - Math.round(lngPx(c.lng, zoomScale)); ky = rc.y - Math.round(latPy(c.lat, zoomScale))
        direct = map.options?.crs?.code === 'EPSG:3857' && Number.isFinite(kx + ky + zoomScale)
      } catch { direct = false }
      let selfPt: { x: number; y: number } | null = null
      if (self && Number.isFinite(self.lat) && Number.isFinite(self.lng)) {
        try {
          if (direct) selfPt = { x: Math.round(lngPx(self.lng, zoomScale)) + kx, y: Math.round(latPy(self.lat, zoomScale)) + ky }
          else { const p = map.latLngToContainerPoint([self.lat, self.lng]); if (Number.isFinite(p.x) && Number.isFinite(p.y)) selfPt = { x: p.x, y: p.y } }
        } catch { selfPt = null }
      }

      // 1) 投影＋篩可視範圍（只有螢幕內的亮點競爭名額）；「與自己的距離」用螢幕像素平方（同一視野內與地面距離單調一致，省掉 haversine 的三角函數）
      const visible: Cand[] = []
      for (const [n, e] of entries) {
        let x = NaN, y = NaN
        if (direct) { x = Math.round(lngPx(e.cur[1], zoomScale)) + kx; y = Math.round(latPy(e.cur[0], zoomScale)) + ky }
        else { try { const p = map.latLngToContainerPoint(e.cur); x = p.x; y = p.y } catch { /* 投影失敗：視為不在螢幕 */ } }
        if (!(x >= 0 && x <= view.x && y >= 0 && y <= view.y)) continue // NaN 也在這裡被擋掉
        const dx = selfPt ? x - selfPt.x : n, dy = selfPt ? y - selfPt.y : 0
        visible.push({ n, e, x, y, d: dx * dx + dy * dy, size: ZERO_SIZE })
      }
      onScreenCount = visible.length
      if (visible.length > 1) visible.sort((a, b) => a.d - b.d || a.n - b.n)

      // 2) 尺寸：泡泡大小只取決於文字，所以讀到的真實尺寸依文字快取（文字變了／過期／網頁字型載入完成才重讀）——穩態重排完全不碰 DOM。
      //    要讀的時候一次讀完（讀在所有寫入之前＝最多一次強制 layout）；沒有 tooltip 元素的用估算（不快取）。
      const nowMs = Date.now()
      for (const c of visible) {
        const e = c.e
        if (e.sz && e.szText === e.text && nowMs - e.szAt < SIZE_TTL_MS) { c.size = e.sz; continue }
        const real = readTipSize(e)
        if (real) { reads++; e.sz = real; e.szText = e.text; e.szAt = nowMs; c.size = real } else c.size = estimateTipSize(e.text)
      }

      // 3) 由近到遠貪婪放置
      const small = visible.length <= SMALL_GROUP_MAX
      const placed: Box[] = []
      const want = new Map<Entry, Dir>()
      for (const c of visible) {
        if (!small && want.size >= MAX_PERMANENT_TIPS) break
        // 候選順序：目前方向（黏著）→ top → right → left → bottom；先要求「整個框在可視範圍內」，沒有再接受被邊緣裁掉的
        const cur = c.e.permanent && c.e.bound ? c.e.dir : null
        let pickDir: Dir | null = null, pickBox: Box | null = null
        for (let pass = 0; pass < 2 && !pickDir; pass++) {
          for (let k = cur ? -1 : 0; k < DIRS.length; k++) { // k=-1：先試目前方向（黏著）
            const d: Dir = k < 0 ? (cur as Dir) : DIRS[k]
            if (k >= 0 && d === cur) continue
            const b = boxAt(c, c.size, d)
            if (pass === 0 && (b.x0 < 0 || b.y0 < 0 || b.x1 > view.x || b.y1 > view.y)) continue
            if (coversPoint(b, selfPt) || blocked(b, placed)) continue
            pickDir = d; pickBox = b
            break
          }
        }
        if (!pickDir && small) { pickDir = 'top'; pickBox = boxAt(c, c.size, 'top') } // 小團體：名字比整齊重要——都沒空位就退回 top、接受重疊
        if (pickDir && pickBox) { want.set(c.e, pickDir); placed.push(pickBox) }
      }
      permanentCount = want.size

      // 4) 寫入 DOM（只動有變化的）
      for (const e of entries.values()) {
        const dir = want.get(e)
        if (dir) {
          if (!e.permanent || !e.bound || e.dir !== dir) { setDir(e, dir); changed++ }
        } else if (e.permanent || !e.bound) {
          bindTip(e, false)
          changed++
        }
        if (e.needsUpdate && !zooming) refreshTip(e) // 縮放動畫期間文字變了、延後的 tooltip.update()
      }
    } catch {
      /* 重排絕不丟例外：下一次再來 */
    } finally {
      if (dbg) {
        const log = dbg.log
        log.push({ t: Date.now(), ms: performance.now() - t0, peers: entries.size, onScreen: onScreenCount, permanent: permanentCount, reads, changed })
        if (log.length > 500) log.splice(0, log.length - 500)
      }
    }
  }

  function frame() {
    raf = null; timeout = null
    if (stopped) return
    const nowPerf = performance.now()
    if (nowPerf - lastFrame < FRAME_MS) { schedule(); return }
    lastFrame = nowPerf
    try { step() } catch { /* 單一幀失敗不得讓迴圈停止（否則亮點永久凍結）；下一幀重來 */ }
    schedule()
  }

  function step() {
    const now = Date.now()
    if (!boundMap) bindMapEvents()
    if (zooming && now - zoomStartAt > ZOOM_WATCHDOG_MS) { zooming = false; rankDirty = true } // zoomend 沒來（動畫被打斷）：不要永遠卡住
    const peers = peersRef.current
    const dots = peers.active && Array.isArray(peers.dots) ? peers.dots : []

    // 1) 同步 entries 與 dots：新增／更新目標／陳舊分級／移除
    const alive = new Set<number>()
    let added = false
    for (const dot of dots) {
      // 防呆：引擎與 debug inject 都會先過濾，但壞資料（null／非有限座標）直接進到 ref 時，不得讓 Leaflet 建出半殘 marker
      if (!dot || !Number.isFinite(dot.n) || !Number.isFinite(dot.lat) || !Number.isFinite(dot.lng)) continue
      const s = staleSeconds(dot, now)
      const level = staleLevel(s, peers.stale)
      if (level === 'drop') continue
      alive.add(dot.n)
      let e = entries.get(dot.n)
      if (!e) {
        try { e = create(dot, now) } catch { continue } // 單一亮點建立失敗不影響其他
        entries.set(dot.n, e)
        added = true
      } else if (e.rxAt !== dot.rxAt) retarget(e, dot, now)
      applyLevel(e, dot, s, level)
    }
    entries.forEach((e, n) => {
      if (alive.has(n)) return
      let oldEl: HTMLElement | null = null // 離線的人：名字泡泡同樣立刻拿掉（不留 200 ms 淡出殘影蓋到重排後的新名字）
      if (e.permanent) { try { oldEl = e.marker.getTooltip()?.getElement?.() ?? null } catch { oldEl = null } }
      try { layer.removeLayer(e.marker) } catch { /* ignore */ }
      if (oldEl) { try { oldEl.remove() } catch { /* ignore */ } }
      entries.delete(n)
      rankDirty = true
    })
    if (added) { rankDirty = true; try { opts.bringSelfToFront() } catch { /* ignore */ } } // 自己的綠點永遠在最上層

    // 2) 位置內插（時間為準；reduced-motion 時 from===to 直接就位）。縮放動畫期間不動：setLatLng 會連帶把 tooltip 擺回舊投影座標
    if (!zooming) {
      entries.forEach((e) => {
        const t = Math.min(1, Math.max(0, (now - e.t0) / e.dur))
        const lat = e.from[0] + (e.to[0] - e.from[0]) * t
        const lng = e.from[1] + (e.to[1] - e.from[1]) * t
        if (lat !== e.cur[0] || lng !== e.cur[1]) {
          e.cur = [lat, lng]
          try { e.marker.setLatLng(e.cur) } catch { /* ignore */ }
        }
      })
    }

    // 3) 永久名字：亮點新增／移除、地圖 zoomend／moveend／resize（rankDirty），或每 ≥1 秒重排一次；縮放動畫中不重排
    if (!zooming && (rankDirty || now - lastRank >= RERANK_MS)) { rankDirty = false; lastRank = now; rank() }
  }

  function schedule() {
    if (stopped) return
    const busy = entries.size > 0 || (peersRef.current.active && peersRef.current.dots.length > 0)
    if (!busy || opts.isMapHidden()) { timeout = setTimeout(frame, IDLE_MS); return }
    raf = requestAnimationFrame(frame)
  }

  return {
    start() { bindMapEvents(); if (!stopped && raf == null && timeout == null) schedule() },
    destroy() {
      stopped = true
      if (raf != null) cancelAnimationFrame(raf)
      if (timeout != null) clearTimeout(timeout)
      raf = null; timeout = null
      unbindMapEvents()
      try { fontsTarget?.removeEventListener('loadingdone', onFontsDone) } catch { /* ignore */ }
      try { mql?.removeEventListener?.('change', onMql) } catch { /* ignore */ }
      try { layer.clearLayers() } catch { /* ignore */ }
      entries.clear()
      try { const w = window as unknown as { __meetPeerDebug?: unknown }; if (w.__meetPeerDebug === dbgApi) delete w.__meetPeerDebug } catch { /* ignore */ }
    },
  }
}
