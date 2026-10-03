// 團練同步跑（Group Run Live）P3——三套 skin（scifi／retro／cute：MapLibre ＋ pointerEvents:none 的 2D 疊層 canvas）
// 共用的「他人亮點／名字泡泡／群聚徽章／自己綠環」繪製核心。契約 docs/runmeet/GROUP_RUN_LIVE_CONTRACT.md §7「skin（P3）」。
//
// 分工：
//   · 本檔＝所有「與風格無關」的邏輯：每位夥伴的時間內插狀態（以 n 為鍵）、陳舊分級（meetLiveUtil）、螢幕投影（呼叫端給的
//     project 回呼）、螢幕外剔除、依與自己距離排序、貪婪泡泡避碰（上→右→左→再上疊一層）、完整泡泡上限 15、同一個 24 px 格內
//     ≥3 點合併成「+N」徽章（外框相交的徽章再反覆合併成一枚：任兩枚徽章永不重疊、N 恆等於它代表的點數）、measureText 寬度快取、
//     debug 快照（peers／叢集／peerFrameMs）。
//   · 保留區（reserved）：呼叫端（meetReserved.ts 從真實 DOM 量出的縮放鈕／回到目前位置鈕／橫幅卡片等，CSS px、canvas 座標）在泡泡避碰前就先
//     佔位——泡泡與「+N」徽章不得與之相交（四個方向都放不下就只畫點；徽章先就近挪開、挪不開就拆回單點）。亮點本身不動。
//   · 各風格只提供 PeerPainter（scifi/meetPainter.ts、retro/meetPainter.ts、cute/meetPainter.ts）：亮點／泡泡／徽章／綠環「長什麼樣」。
//   · 三張地圖（SciFiMap／RetroMap／CuteMap）只在 renderFrame 內呼叫 begin → drawDots →（自己光球）→ drawSelfRing → drawTop，
//     並讀 meetPeersRef.current（ref，不碰 React state、不重繪）。
//
// ⚠️ 最重要的不變量：這裡「絕不 throw 進呼叫端」。skin 地圖的 render loop 連續 6 幀丟例外就會把整張地圖退回 Leaflet
// （見 SciFiMap／RetroMap／CuteMap 的 consecutiveRenderFailures），所以 begin／drawDots／drawSelfRing／drawTop／clear／debug* 全部
// 在內部 try/catch，單一夥伴（座標非有限數、名稱怪異、painter 例外）只會被略過；失敗次數記在 stats().fails 供除錯。
// 全部座標都是 CSS px（呼叫端既有的 ctx.setTransform(dpr,…) 不變、本檔不再縮放）。

import type { MeetLivePeers, StaleThresholds } from './meetLiveTypes'
import { MEET_GRAY, haversineM, staleLevel, staleSeconds, truncName } from './meetLiveUtil'

export type PeerLevel = 'ok' | 'fade' | 'gray' // 'drop' 不畫、狀態直接移除
export type BubbleSide = 'above' | 'right' | 'left'

/** 完整泡泡（名字）上限；超過的只畫點（契約 §7）。 */
export const MAX_FULL_BUBBLES = 15
/** 群聚：同一個 24 px 格內 ≥3 個點合併成一枚「+N」徽章（契約 §7）。 */
export const CLUSTER_CELL_PX = 24
export const CLUSTER_MIN = 3
/** 「fadeS 後 60% 透明」＝不透明度 0.4（與 Leaflet 圖層 meetLeafletPeers.ts 同值）。 */
export const FADE_ALPHA = 0.4
/** 自己光球外環（契約 §7：#46E3A0、r+4、2 px）。 */
export const SELF_RING_GREEN = '#46E3A0'
/** 灰階（≥grayS）泡泡文字（契約 §7）。 */
export const NO_SIGNAL_TEXT = '訊號中斷'
/** 名稱為空（或清掉控制字元後為空）時的退路——與伺服器端名稱消毒（契約 §5）的最後一道退路相同。 */
export const FALLBACK_NAME = '跑者'

const DEFAULT_STALE: StaleThresholds = { fadeS: 30, grayS: 75, dropS: 180 }
/** 保留區最多取幾個（避碰迴圈的上限）。 */
const MAX_RESERVED = 32
/** 兩枚「+N」徽章外框之間至少留的空隙（CSS px）；小於它就合併成一枚。 */
const BADGE_GAP = 4
/** 徽章合併迴圈的圈數上限（每圈至少合併一對才會繼續，實際遠小於此；只是保險）。 */
const MAX_MERGE_PASSES = 64

// ───────────────────────── 型別 ─────────────────────────

export interface Rect { x: number; y: number; w: number; h: number }

export interface PeerEnv {
  /** devicePixelRatio（只給 sprite 快取用；繪圖座標一律 CSS px）。 */
  dpr: number
  /** 像素風倍率（retro 的 orbScale＝每個「邏輯像素」佔幾個 CSS px）；其他風格恆 1。 */
  scale: number
}

export interface DotPaint { level: PeerLevel; alpha: number }

export interface BubbleDraw {
  /** 圓角矩形本體（不含小三角），CSS px。 */
  x: number; y: number; w: number; h: number
  side: BubbleSide
  /** 小三角底邊中心：above＝箱底邊上的 x；left／right＝箱側邊上的 y。 */
  base: number
  /** 小三角尖端（螢幕座標，指向亮點邊緣）。 */
  tipX: number; tipY: number
  /** 「再上疊一層」時：尖端之下再畫一條細引線連到 (leadX, leadY)＝亮點上緣。 */
  leader: boolean
  leadX: number; leadY: number
  dotX: number; dotY: number
  text: string
  level: PeerLevel
  alpha: number
  n: number
}

export interface BubbleMetrics {
  /** 箱內左右／上下留白（CSS px）。 */
  padX: number; padY: number
  /** 文字行高（CSS px）。 */
  textH: number
  /** 小三角長度、亮點外緣到尖端的間距。 */
  tail: number; gap: number
}

export interface PeerPainter {
  readonly id: string
  /** 亮點主色（debug.color；ok／fade）。 */
  readonly color: string
  /** 泡泡字型（CSS font shorthand，含 CJK fallback）；measureText 與 fillText 共用同一串。 */
  readonly font: string
  /** 像素風：泡泡位置／尺寸吸附到 scale 的整數倍（retro）。 */
  readonly snapToScale?: boolean
  /** 亮點含外框的半徑（CSS px；避碰／剔除用）。 */
  dotRadius(scale: number): number
  bubbleMetrics(scale: number): BubbleMetrics
  clusterRadius(scale: number): number
  /** 「+N」徽章實際佔用的外框尺寸（CSS px，保守估計；避開保留區用）；缺省＝(2R+12)×2R。 */
  clusterBox?(scale: number, count: number): { w: number; h: number }
  /** 自己光球的視覺半徑（綠環半徑＝此值＋4）。 */
  selfOrbRadius(scale: number): number
  drawDot(ctx: CanvasRenderingContext2D, env: PeerEnv, x: number, y: number, p: DotPaint): void
  drawBubble(ctx: CanvasRenderingContext2D, env: PeerEnv, b: BubbleDraw): void
  drawCluster(ctx: CanvasRenderingContext2D, env: PeerEnv, x: number, y: number, count: number): void
  drawSelfRing(ctx: CanvasRenderingContext2D, env: PeerEnv, x: number, y: number): void
}

export interface PeerFrameInput {
  peers: MeetLivePeers | null | undefined
  /** map.project 的包裝；回傳 CSS px（相對疊層 canvas 左上角）。 */
  project: (lng: number, lat: number) => { x: number; y: number } | null | undefined
  /** 疊層 canvas 的 CSS 寬高。 */
  w: number
  h: number
  dpr: number
  /** retro 的 orbScale；其餘風格不傳。 */
  scale?: number
  /** prefers-reduced-motion：直接跳位置、不內插。 */
  reduced?: boolean
  /** 自己的經緯度（依距離排序用）；null＝退回以畫面中心排序。 */
  self?: { lat: number; lng: number } | null
  /** 自己光球的螢幕座標（綠環、避碰）；null＝沒有定位、不畫綠環。 */
  selfXY?: { x: number; y: number } | null
  /**
   * 保留區（CSS px、疊層 canvas 座標；已含呼叫端加的內距）：地圖上的控制鈕／橫幅卡片。泡泡與群聚徽章的外框不得與之相交
   * （泡泡四個方向都放不下 → 只畫點；徽章就近挪開，挪不開 → 拆回單點）。亮點本身不動。壞掉的項目（非有限數、寬高≤0）略過，最多取 32 個。
   */
  reserved?: readonly Rect[] | null
  nowMs?: number
}

export interface PeerDebugEntry {
  n: number; x: number; y: number; name: string; color: string; level: PeerLevel
  /** 這位夥伴最後一幀是否有完整泡泡。 */
  bubble: boolean
  /** 泡泡文字（沒有泡泡的點也會算出來，方便驗證 fade 尾綴／「訊號中斷」）。 */
  text: string
  alpha: number
  /** 被合併進「+N」徽章（不單獨畫點）。 */
  clustered: boolean
  /** 完整泡泡的外框（CSS px，不含小三角）；沒有泡泡＝null。E2E 用來量泡泡像素。 */
  box: Rect | null
}
export interface PeerClusterDebug {
  /** 成員重心（螢幕）。 */
  x: number; y: number
  count: number; ns: number[]
  /** 徽章實際畫的位置（為了避開保留區可能偏離重心）與避碰外框（保守估計）。 */
  drawX: number; drawY: number
  box: Rect
  /** 這枚徽章由幾個 24 px 格的群聚合併而成（1＝沒有合併）。 */
  merged: number
}
export interface PeerFrameStats {
  p95: number; median: number
  /** 滾動視窗內的樣本數（上限 120）。 */
  samples: number
  /** 累計畫過的幀數（有亮點的幀；單調遞增，E2E 用來確認視窗內全是新幀）。 */
  frames: number
  fails: number; lastError: string
  /** 累計：因為外框相交而被合併掉的徽章數／因為放不進保留區而拆回單點的徽章數／帳目自檢失敗而全部拆回單點的次數（應恆為 0）。 */
  badgeMerges: number; badgeDissolved: number; acctRepairs: number
  /** 單一幀的徽章合併最多跑了幾圈「有合併」的迴圈（1＝一圈就穩；≥2＝合併後的新外框又撞到別枚、連鎖合併）。 */
  mergePassesMax: number
}

export interface PeerLayer {
  /** 每幀第一步：同步狀態／內插／投影／剔除／群聚／避碰。回傳「這幀有沒有東西可能要畫」。永不 throw。 */
  begin(inp: PeerFrameInput): boolean
  /** 第一輪：所有亮點（畫在自己光球「之前」；遠到近）。 */
  drawDots(ctx: CanvasRenderingContext2D): void
  /** 自己光球之後：selfRing 為 true 且有 selfXY 才畫綠環。 */
  drawSelfRing(ctx: CanvasRenderingContext2D): void
  /** 第二輪：所有泡泡與群聚徽章（最上層）；同時記錄這一幀的 peer 繪製耗時。 */
  drawTop(ctx: CanvasRenderingContext2D): void
  /** overlayHidden／離開團練模式：清空狀態與 debug 快照。 */
  clear(): void
  debugPeers(): PeerDebugEntry[]
  debugClusters(): PeerClusterDebug[]
  /** 最後一幀實際採用的保留區（已過濾壞項目）。 */
  debugReserved(): Rect[]
  stats(): PeerFrameStats
  destroy(): void
}

export interface PeerLayerOptions {
  /** 測試用：不依賴 DOM 的文字寬度（預設用離屏 canvas 的 measureText）。 */
  measureText?: (font: string, text: string) => number
  /** 測試用：計時來源（預設 performance.now）。 */
  now?: () => number
}

// ───────────────────────── 小工具（painter 也會用） ─────────────────────────

export const clamp = (v: number, lo: number, hi: number): number => (v < lo ? lo : v > hi ? hi : v)
const isNum = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)
/** 吸附到裝置像素格（sprite／細線的邊緣才銳利）；座標仍是 CSS px。 */
export const snapDev = (v: number, dpr: number): number => Math.round(v * dpr) / dpr

function hit(a: Rect, b: Rect): boolean {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h
}
/** 兩個矩形相距小於 gap 也算「撞到」（徽章之間要留空隙）。 */
function hitGap(a: Rect, b: Rect, gap: number): boolean {
  return a.x < b.x + b.w + gap && b.x < a.x + a.w + gap && a.y < b.y + b.h + gap && b.y < a.y + a.h + gap
}

// 控制字元／零寬／bidi 控制碼：伺服器端消毒（契約 §5）已移除，這裡再保險一次——canvas 不會把名稱當 HTML，但 RLO（U+202E）
// 之類會把整段文字視覺上倒轉；全部清掉後文字照字面畫。
// eslint-disable-next-line no-control-regex
const NAME_JUNK = /[\u0000-\u001f\u007f-\u009f​-‏‪-‮⁠-⁤⁦-⁩﻿]/g
export function cleanPeerName(raw: unknown): string {
  const s = typeof raw === 'string' ? raw : raw == null ? '' : String(raw)
  const t = s.replace(NAME_JUNK, '').trim()
  return t === '' ? FALLBACK_NAME : t
}

export function sanitizeStale(s: unknown): StaleThresholds {
  const o = (s && typeof s === 'object' ? s : {}) as Partial<StaleThresholds>
  const fade = isNum(o.fadeS) && o.fadeS > 0 ? o.fadeS : DEFAULT_STALE.fadeS
  const gray = isNum(o.grayS) && o.grayS >= fade ? o.grayS : Math.max(DEFAULT_STALE.grayS, fade)
  const drop = isNum(o.dropS) && o.dropS >= gray ? o.dropS : Math.max(DEFAULT_STALE.dropS, gray)
  return { fadeS: fade, grayS: gray, dropS: drop }
}

/**
 * 畫「圓角矩形＋小三角」的單一封閉路徑（小三角整合進對應那一邊，描邊不會在三角底邊多出一條線）。
 * scifi／cute 共用；retro 走自己的像素階梯畫法。
 */
export function traceBubblePath(ctx: CanvasRenderingContext2D, b: BubbleDraw, radius: number, tailHalf: number): void {
  const { x, y, w, h } = b
  const r = Math.max(0, Math.min(radius, w / 2, h / 2))
  const x2 = x + w, y2 = y + h
  ctx.beginPath()
  ctx.moveTo(x + r, y)
  ctx.lineTo(x2 - r, y)
  ctx.arcTo(x2, y, x2, y + r, r)
  if (b.side === 'left') { // 箱在亮點左邊：小三角長在右邊緣、尖端朝右
    const c = clamp(b.base, y + tailHalf + 3, Math.max(y + tailHalf + 3, y2 - tailHalf - 3)) // 箱不高：底邊可略入圓角，尾巴才不會被擠歪
    ctx.lineTo(x2, c - tailHalf); ctx.lineTo(b.tipX, b.tipY); ctx.lineTo(x2, c + tailHalf)
  }
  ctx.lineTo(x2, y2 - r)
  ctx.arcTo(x2, y2, x2 - r, y2, r)
  if (b.side === 'above') { // 箱在亮點上方：小三角長在底邊、尖端朝下（契約 §7）
    const c = clamp(b.base, x + r + tailHalf, Math.max(x + r + tailHalf, x2 - r - tailHalf))
    ctx.lineTo(c + tailHalf, y2); ctx.lineTo(b.tipX, b.tipY); ctx.lineTo(c - tailHalf, y2)
  }
  ctx.lineTo(x + r, y2)
  ctx.arcTo(x, y2, x, y2 - r, r)
  if (b.side === 'right') { // 箱在亮點右邊：小三角長在左邊緣、尖端朝左
    const c = clamp(b.base, y + tailHalf + 3, Math.max(y + tailHalf + 3, y2 - tailHalf - 3))
    ctx.lineTo(x, c + tailHalf); ctx.lineTo(b.tipX, b.tipY); ctx.lineTo(x, c - tailHalf)
  }
  ctx.lineTo(x, y + r)
  ctx.arcTo(x, y, x + r, y, r)
  ctx.closePath()
}

// ───────────────────────── sprite 快取（painter 用） ─────────────────────────
// 亮點長得一模一樣、每幀要畫數十顆：預先畫進離屏 canvas（以 devicePixelRatio 倍解析度），之後每顆只是一次 drawImage。
const spriteCache = new Map<string, HTMLCanvasElement>()
export function getSprite(key: string, cssW: number, cssH: number, dpr: number, draw: (g: CanvasRenderingContext2D) => void): HTMLCanvasElement | null {
  const k = `${key}|${dpr}`
  const hitC = spriteCache.get(k)
  if (hitC) return hitC
  try {
    if (typeof document === 'undefined') return null
    const c = document.createElement('canvas')
    c.width = Math.max(1, Math.ceil(cssW * dpr))
    c.height = Math.max(1, Math.ceil(cssH * dpr))
    const g = c.getContext('2d')
    if (!g) return null
    g.scale(dpr, dpr)
    draw(g)
    if (spriteCache.size > 48) spriteCache.clear()
    spriteCache.set(k, c)
    return c
  } catch { return null }
}

// ───────────────────────── 圖層 ─────────────────────────

interface PeerState {
  n: number
  raw: unknown // 最近一次收到的原始 name（只在它改變時才重新清理／截斷）
  name: string // 已清理的顯示名稱
  short: string // truncName(name)
  rxAt: number
  from: [number, number]
  to: [number, number]
  cur: [number, number]
  t0: number
  dur: number
  seen: number
}
interface Item {
  n: number
  st: PeerState
  x: number; y: number
  level: PeerLevel
  alpha: number
  s: number
  dist: number
  text: string
  clustered: boolean
  bubble: boolean
  box: Rect | null
}
interface Cluster { x: number; y: number; count: number; items: Item[]; ox: number; oy: number; w: number; h: number; merged: number }

const EMPTY: readonly unknown[] = []
const SAMPLE_CAP = 120

export function createPeerLayer(painter: PeerPainter, opts: PeerLayerOptions = {}): PeerLayer {
  const perfNow = opts.now ?? (() => (typeof performance !== 'undefined' ? performance.now() : Date.now()))
  const states = new Map<number, PeerState>()
  const widthCache = new Map<string, number>()
  const env: PeerEnv = { dpr: 1, scale: 1 }
  let frameNo = 0
  let items: Item[] = []
  let clusters: Cluster[] = []
  let bubbles: BubbleDraw[] = []
  let reservedUsed: Rect[] = []
  let selfXY: { x: number; y: number } | null = null
  let selfRingOn = false
  let tFrame = 0
  const samples = new Float32Array(SAMPLE_CAP)
  let sampleN = 0, sampleI = 0, frames = 0
  let fails = 0
  let lastError = ''
  let mctx: CanvasRenderingContext2D | null | undefined
  let destroyed = false
  let badgeMerges = 0, badgeDissolved = 0, acctRepairs = 0, mergePassesMax = 0

  // 字型載入完成後文字寬度會變（retro／cute 的自訂字型）：清掉寬度快取，下一幀以新字型重新量測。
  const onFonts = () => { widthCache.clear() }
  try { if (typeof document !== 'undefined') (document as Document & { fonts?: FontFaceSet }).fonts?.addEventListener?.('loadingdone', onFonts) } catch { /* 舊瀏覽器沒有 FontFaceSet 事件：略過 */ }

  function noteFail(e: unknown) {
    fails++
    if (!lastError) lastError = String((e as Error)?.message ?? e).slice(0, 160)
  }

  function measure(text: string): number {
    let w = widthCache.get(text)
    if (w !== undefined) return w
    try {
      if (opts.measureText) w = opts.measureText(painter.font, text)
      else {
        if (mctx === undefined) mctx = typeof document !== 'undefined' ? document.createElement('canvas').getContext('2d') : null
        if (mctx) { mctx.font = painter.font; w = mctx.measureText(text).width }
      }
    } catch { w = undefined }
    if (w === undefined || !Number.isFinite(w) || w < 0) w = Array.from(text).length * 12
    if (widthCache.size > 600) widthCache.clear() // 「· 35s」秒數會一直變、字串種類有限但保險封頂
    widthCache.set(text, w)
    return w
  }

  function pushSample(ms: number) {
    samples[sampleI] = ms
    sampleI = (sampleI + 1) % SAMPLE_CAP
    if (sampleN < SAMPLE_CAP) sampleN++
    frames++
  }

  function resetFrame() {
    items = []; clusters = []; bubbles = []; reservedUsed = []
  }

  function bubbleText(st: PeerState, level: PeerLevel, s: number): string {
    if (level === 'gray') return NO_SIGNAL_TEXT
    if (level === 'fade') return `${st.short} · ${Math.round(s)}s`
    return st.short
  }

  // 內插：新夥伴直接出現在目標；之後每次 rxAt（或座標）改變，從目前位置線性滑到新位置，時間長度＝兩次更新的間隔
  // （≈1.0×iv，夾在 0.6–12 s），以 wall-clock 為準、不依賴幀率；reduced-motion 直接跳。
  function retarget(st: PeerState, lat: number, lng: number, rxAt: number, now: number, reduced: boolean) {
    const gap = rxAt - st.rxAt
    st.rxAt = rxAt
    if (reduced) {
      st.from = [lat, lng]; st.to = [lat, lng]; st.cur = [lat, lng]; st.t0 = now; st.dur = 1
      return
    }
    st.from = [st.cur[0], st.cur[1]]
    st.to = [lat, lng]
    st.t0 = now
    st.dur = clamp(Number.isFinite(gap) ? gap : 0, 600, 12000)
  }

  // 「+N」徽章的外框尺寸（painter.clusterBox；沒給或壞掉的那一維退回由 clusterRadius 推的預設值）。N 的位數會影響寬度。
  function sizeBadge(c: Cluster) {
    let R = 13
    try { const r = painter.clusterRadius(env.scale); if (isNum(r) && r > 0) R = r } catch { /* 用預設 */ }
    let bw = 2 * R + 12, bh = 2 * R
    try {
      const b = painter.clusterBox ? painter.clusterBox(env.scale, c.count) : null
      if (b && isNum(b.w) && b.w > 0) bw = b.w
      if (b && isNum(b.h) && b.h > 0) bh = b.h
    } catch { /* 用預設 */ }
    c.w = bw; c.h = bh
  }

  // 最近的成員離自己多遠（items 已依距離排序，第一個就是最近的）；平手以 n 排序，輸出才穩定。
  const byNearest = (a: Cluster, b: Cluster) => a.items[0].dist - b.items[0].dist || a.items[0].n - b.items[0].n

  // 徽章互相不得重疊：外框（含 BADGE_GAP 空隙）相交的兩枚合併成一枚——N＝成員數相加、位置＝成員數加權重心（＝全部成員的平均位置）。
  // 合併後外框會變（位數、重心），可能又撞上第三枚，所以反覆直到任兩枚都不相交；每一圈至少合併一對才會繼續（最多 k−1 圈，另設
  // MAX_MERGE_PASSES 保險；超過上限而仍相交的，下一步（定位）會把後來的那枚拆回單點，所以「徽章不重疊」恆成立）。k 很小，O(k²) 即可。
  function mergeBadges(list: Cluster[]): Cluster[] {
    let arr = list
    let merging = 0
    for (let pass = 0; pass < MAX_MERGE_PASSES && arr.length > 1; pass++) {
      let did = false
      const dead: boolean[] = new Array(arr.length).fill(false)
      for (let i = 0; i < arr.length; i++) {
        if (dead[i]) continue
        const a = arr[i]
        for (let j = i + 1; j < arr.length; j++) {
          if (dead[j]) continue
          const b = arr[j]
          if (Math.abs(a.x - b.x) < (a.w + b.w) / 2 + BADGE_GAP && Math.abs(a.y - b.y) < (a.h + b.h) / 2 + BADGE_GAP) {
            const na = a.items.length, nb = b.items.length, n = na + nb
            a.x = (a.x * na + b.x * nb) / n
            a.y = (a.y * na + b.y * nb) / n
            for (const it of b.items) a.items.push(it)
            a.count = n
            a.merged += b.merged
            sizeBadge(a)
            dead[j] = true
            did = true
            badgeMerges++
          }
        }
      }
      if (!did) break
      merging++
      arr = arr.filter((_, i) => !dead[i])
    }
    if (merging > mergePassesMax) mergePassesMax = merging
    for (const c of arr) if (c.merged > 1) c.items.sort((p, q) => p.dist - q.dist || p.n - q.n) // 成員維持「近→遠」，byNearest 才讀得到最近的
    return arr
  }

  function begin(inp: PeerFrameInput): boolean {
    const t0 = perfNow()
    tFrame = 0
    try {
      if (destroyed) { resetFrame(); return false }
      const peers = inp.peers
      selfRingOn = !!peers && peers.selfRing === true
      const sx = inp.selfXY
      selfXY = sx && isNum(sx.x) && isNum(sx.y) ? { x: sx.x, y: sx.y } : null
      env.dpr = isNum(inp.dpr) && inp.dpr > 0 ? inp.dpr : 1
      env.scale = isNum(inp.scale) && (inp.scale as number) >= 1 ? (inp.scale as number) : 1
      const active = !!peers && peers.active === true && Array.isArray(peers.dots)
      const dots: readonly unknown[] = active ? (peers!.dots as readonly unknown[]) : EMPTY
      if (dots.length === 0) {
        if (states.size) states.clear()
        resetFrame()
        return selfRingOn && !!selfXY // 只有綠環可能要畫
      }
      const W = inp.w, H = inp.h
      if (!isNum(W) || !isNum(H) || W <= 0 || H <= 0 || typeof inp.project !== 'function') { resetFrame(); return false }

      const now = isNum(inp.nowMs) ? (inp.nowMs as number) : Date.now()
      const stale = sanitizeStale(peers!.stale)
      const reduced = inp.reduced === true
      const selfLL = inp.self && isNum(inp.self.lat) && isNum(inp.self.lng) ? inp.self : null
      const dotR = painter.dotRadius(env.scale)
      const cullM = dotR + 8
      frameNo++
      // 保留區：壞項目（非物件／非有限數／寬高≤0）略過，最多 MAX_RESERVED 個
      const rsv: Rect[] = []
      if (inp.reserved && Array.isArray(inp.reserved)) {
        for (const r of inp.reserved as readonly unknown[]) {
          const o = r as Partial<Rect> | null
          if (!o || typeof o !== 'object' || !isNum(o.x) || !isNum(o.y) || !isNum(o.w) || !isNum(o.h) || o.w <= 0 || o.h <= 0) continue
          rsv.push({ x: o.x, y: o.y, w: o.w, h: o.h })
          if (rsv.length >= MAX_RESERVED) break
        }
      }

      // 1) 同步狀態＋內插＋投影＋剔除
      const list: Item[] = []
      for (let i = 0; i < dots.length; i++) {
        try {
          const d = dots[i] as { n?: unknown; name?: unknown; lat?: unknown; lng?: unknown; rxAt?: unknown; ageS?: unknown } | null
          if (!d || typeof d !== 'object') continue
          if (!isNum(d.n) || !isNum(d.lat) || !isNum(d.lng)) continue
          if (Math.abs(d.lat) > 90 || Math.abs(d.lng) > 180) continue
          const rxAt = isNum(d.rxAt) ? d.rxAt : now
          const ageS = isNum(d.ageS) && d.ageS > 0 ? d.ageS : 0
          const s = staleSeconds({ rxAt, ageS }, now)
          const level = staleLevel(s, stale)
          if (level === 'drop') continue // 不畫；沒被標記 seen 的狀態會在迴圈後移除
          let st = states.get(d.n)
          if (st && st.seen === frameNo) continue // 同一幀重複的 n：只取第一筆
          if (!st) {
            const nm = cleanPeerName(d.name)
            st = { n: d.n, raw: d.name, name: nm, short: truncName(nm), rxAt, from: [d.lat, d.lng], to: [d.lat, d.lng], cur: [d.lat, d.lng], t0: now, dur: 1, seen: 0 }
            states.set(d.n, st)
          } else {
            if (st.rxAt !== rxAt || st.to[0] !== d.lat || st.to[1] !== d.lng) retarget(st, d.lat, d.lng, rxAt, now, reduced)
            if (d.name !== st.raw) { st.raw = d.name; st.name = cleanPeerName(d.name); st.short = truncName(st.name) }
          }
          st.seen = frameNo
          if (reduced) { st.cur[0] = st.to[0]; st.cur[1] = st.to[1] }
          else {
            const t = clamp((now - st.t0) / st.dur, 0, 1)
            st.cur[0] = st.from[0] + (st.to[0] - st.from[0]) * t
            st.cur[1] = st.from[1] + (st.to[1] - st.from[1]) * t
          }
          const pt = inp.project(st.cur[1], st.cur[0])
          if (!pt || !isNum(pt.x) || !isNum(pt.y)) continue
          if (pt.x < -cullM || pt.x > W + cullM || pt.y < -cullM || pt.y > H + cullM) continue // 螢幕外不畫
          list.push({
            n: st.n, st, x: pt.x, y: pt.y, level, alpha: level === 'ok' ? 1 : FADE_ALPHA, s,
            dist: selfLL ? haversineM(selfLL.lat, selfLL.lng, st.cur[0], st.cur[1]) : Math.hypot(pt.x - W / 2, pt.y - H / 2),
            text: bubbleText(st, level, s), clustered: false, bubble: false, box: null,
          })
        } catch (e) { noteFail(e) }
      }
      states.forEach((st, n) => { if (st.seen !== frameNo) states.delete(n) })
      list.sort((a, b) => a.dist - b.dist || a.n - b.n)

      // 2) 群聚：同一個 24 px 格內 ≥3 點 → 一枚「+N」徽章（成員不單獨畫點、沒有泡泡）
      const cells = new Map<number, Item[]>()
      for (const it of list) {
        const key = Math.floor(it.x / CLUSTER_CELL_PX) * 8192 + (Math.floor(it.y / CLUSTER_CELL_PX) + 8)
        const arr = cells.get(key)
        if (arr) arr.push(it); else cells.set(key, [it])
      }
      let cl: Cluster[] = []
      cells.forEach((arr) => {
        if (arr.length < CLUSTER_MIN) return
        let cx = 0, cy = 0
        for (const it of arr) { it.clustered = true; cx += it.x; cy += it.y }
        const c: Cluster = { x: cx / arr.length, y: cy / arr.length, count: arr.length, items: arr, ox: 0, oy: 0, w: 0, h: 0, merged: 1 }
        sizeBadge(c)
        cl.push(c)
      })
      cl.sort(byNearest)

      // 2b) 徽章互相不得重疊：外框相交（含 BADGE_GAP 空隙）的就合併成一枚（N＝成員數相加、位置＝成員數加權重心），反覆直到沒有任兩枚相交
      if (cl.length > 1) { cl = mergeBadges(cl); cl.sort(byNearest) }

      // 2c) 定位：預設位置（成員重心）沒被保留區（含自己的光球）擋住、也沒壓到別枚徽章的先固定（它們彼此已不相交）；被擋住的再就近挪到障礙旁邊
      //     （單軸位移、取位移最小且完全合法者：在畫面內、不碰保留區、不碰任何已定位的徽章）；挪不開 → 拆回單點（亮點本身不動）
      if (cl.length) {
        const keptRects: Rect[] = []
        const kept: Cluster[] = []
        const blocked: Cluster[] = []
        const EDGE_C = 2
        // 徽章不能蓋住的地方＝保留區＋自己的光球（與泡泡避開光球同一個半徑；光球只對徽章當保留區，不進 debugReserved）
        const blockers: Rect[] = rsv.slice()
        if (selfXY) { const sr = painter.selfOrbRadius(env.scale) + 6; if (isNum(sr) && sr > 0) blockers.push({ x: selfXY.x - sr, y: selfXY.y - sr, w: 2 * sr, h: 2 * sr }) }
        const rectAt = (c: Cluster, ox: number, oy: number): Rect => ({ x: c.x + ox - c.w / 2, y: c.y + oy - c.h / 2, w: c.w, h: c.h })
        const hitReserved = (r: Rect) => { for (const q of blockers) if (hit(r, q)) return true; return false }
        const hitBadges = (r: Rect) => { for (const k of keptRects) if (hitGap(r, k, BADGE_GAP)) return true; return false }
        for (const c of cl) {
          const r0 = rectAt(c, 0, 0)
          if (!hitReserved(r0) && !hitBadges(r0)) { kept.push(c); keptRects.push(r0) } else blocked.push(c)
        }
        for (const c of blocked) {
          const r0 = rectAt(c, 0, 0)
          let spot: [number, number] | null = null
          let bestD = Infinity
          for (const q of blockers) {
            if (!hit(r0, q)) continue
            const cands: [number, number][] = [
              [q.x - 1 - c.w / 2 - c.x, 0], [q.x + q.w + 1 + c.w / 2 - c.x, 0],
              [0, q.y - 1 - c.h / 2 - c.y], [0, q.y + q.h + 1 + c.h / 2 - c.y],
            ]
            for (const [ox, oy] of cands) {
              const d = Math.abs(ox) + Math.abs(oy)
              if (d >= bestD) continue
              const r = rectAt(c, ox, oy)
              if (r.x < EDGE_C || r.y < EDGE_C || r.x + r.w > W - EDGE_C || r.y + r.h > H - EDGE_C) continue
              if (hitReserved(r) || hitBadges(r)) continue
              bestD = d; spot = [ox, oy]
            }
          }
          if (spot) { c.ox = spot[0]; c.oy = spot[1]; kept.push(c); keptRects.push(rectAt(c, spot[0], spot[1])) }
          else { for (const it of c.items) it.clustered = false; badgeDissolved++ } // 徽章放不下：成員改畫單點
        }
        cl = kept
        cl.sort(byNearest)
      }

      // 2d) 帳目自檢：每枚徽章的 N＝它的成員數、成員都標了 clustered，而且「徽章 N 的總和＋沒被群聚的點＝可見的點」。
      //     不符（程式不該走到）→ 全部拆回單點並計數（stats().acctRepairs）：寧可多畫點，也不能讓徽章上的數字說謊。
      {
        let inKept = 0, flagged = 0, bad = false
        for (const c of cl) {
          if (c.count !== c.items.length) bad = true
          inKept += c.items.length
          for (const it of c.items) if (!it.clustered) bad = true
        }
        for (const it of list) if (it.clustered) flagged++
        if (bad || inKept !== flagged) { for (const it of list) it.clustered = false; cl = []; acctRepairs++ }
      }

      // 3) 泡泡：依與自己距離（近→遠）貪婪放置——上→右→左→再上疊一層；最多 15 個，其餘只畫點
      const singles = list.filter((it) => !it.clustered)
      const m = painter.bubbleMetrics(env.scale)
      const q = painter.snapToScale ? env.scale : 1
      const snap = (v: number) => Math.round(v / q) * q
      const obstacles: Rect[] = singles.map((it) => ({ x: it.x - dotR - 1, y: it.y - dotR - 1, w: 2 * dotR + 2, h: 2 * dotR + 2 }))
      for (const c of cl) obstacles.push({ x: c.x + c.ox - c.w / 2 - 1, y: c.y + c.oy - c.h / 2 - 1, w: c.w + 2, h: c.h + 2 })
      if (selfXY) {
        const r = painter.selfOrbRadius(env.scale) + 6
        obstacles.push({ x: selfXY.x - r, y: selfXY.y - r, w: 2 * r, h: 2 * r })
      }
      const placed: Rect[] = []
      const out: BubbleDraw[] = []
      const EDGE = 2
      for (let si = 0; si < singles.length && out.length < MAX_FULL_BUBBLES; si++) {
        const it = singles[si]
        try {
          const tw = measure(it.text)
          const bw = Math.min(220, Math.ceil((tw + 2 * m.padX) / q) * q)
          const bh = Math.ceil((m.textH + 2 * m.padY) / q) * q
          const x = it.x, y = it.y
          const tipGap = dotR + m.gap
          const cands: { side: BubbleSide; leader: boolean; r: Rect; base: number; tipX: number; tipY: number }[] = []
          const bxA = snap(clamp(x - bw / 2, EDGE, Math.max(EDGE, W - EDGE - bw)))
          const byA = snap(y - tipGap - m.tail - bh)
          cands.push({ side: 'above', leader: false, r: { x: bxA, y: byA, w: bw, h: bh }, base: x, tipX: x, tipY: y - tipGap })
          cands.push({ side: 'right', leader: false, r: { x: snap(x + tipGap + m.tail), y: snap(y - bh / 2), w: bw, h: bh }, base: y, tipX: x + tipGap, tipY: y })
          cands.push({ side: 'left', leader: false, r: { x: snap(x - tipGap - m.tail - bw), y: snap(y - bh / 2), w: bw, h: bh }, base: y, tipX: x - tipGap, tipY: y })
          const byB = snap(byA - bh - 3)
          cands.push({ side: 'above', leader: true, r: { x: bxA, y: byB, w: bw, h: bh }, base: x, tipX: clamp(x, bxA + 8, Math.max(bxA + 8, bxA + bw - 8)), tipY: byB + bh + m.tail })
          for (const c of cands) {
            const r = c.r
            if (r.x < EDGE || r.y < EDGE || r.x + r.w > W - EDGE || r.y + r.h > H - EDGE) continue
            let bad = false
            if (rsv.length) { // 保留區（控制鈕／橫幅）：連小三角一起算，不得相交
              const e: Rect = c.side === 'above' ? { x: r.x, y: r.y, w: r.w, h: r.h + m.tail }
                : c.side === 'right' ? { x: r.x - m.tail, y: r.y, w: r.w + m.tail, h: r.h }
                : { x: r.x, y: r.y, w: r.w + m.tail, h: r.h }
              for (let k = 0; k < rsv.length && !bad; k++) if (hit(e, rsv[k])) bad = true
            }
            for (let k = 0; k < placed.length && !bad; k++) if (hit(r, placed[k])) bad = true
            for (let k = 0; k < obstacles.length && !bad; k++) if (k !== si && hit(r, obstacles[k])) bad = true
            if (bad) continue
            placed.push({ x: r.x - 2, y: r.y - 2, w: r.w + 4, h: r.h + 4 }) // 泡泡之間至少留 2 px
            out.push({ x: r.x, y: r.y, w: r.w, h: r.h, side: c.side, base: c.base, tipX: c.tipX, tipY: c.tipY, leader: c.leader, leadX: x, leadY: y - tipGap, dotX: x, dotY: y, text: it.text, level: it.level, alpha: it.alpha, n: it.n })
            it.bubble = true
            it.box = { x: r.x, y: r.y, w: r.w, h: r.h }
            break
          }
        } catch (e) { noteFail(e) }
      }
      items = list; clusters = cl; bubbles = out; reservedUsed = rsv
      return true
    } catch (e) {
      noteFail(e)
      resetFrame()
      return false
    } finally {
      tFrame += perfNow() - t0
    }
  }

  function drawDots(ctx: CanvasRenderingContext2D) {
    const t0 = perfNow()
    try {
      if (items.length) {
        ctx.save()
        for (let i = items.length - 1; i >= 0; i--) { // 遠到近：近的疊在上面
          const it = items[i]
          if (it.clustered) continue
          try { painter.drawDot(ctx, env, it.x, it.y, { level: it.level, alpha: it.alpha }) } catch (e) { noteFail(e) }
        }
        ctx.restore()
      }
    } catch (e) { noteFail(e) } finally { tFrame += perfNow() - t0 }
  }

  function drawSelfRing(ctx: CanvasRenderingContext2D) {
    const t0 = perfNow()
    try {
      if (selfRingOn && selfXY) {
        ctx.save()
        try { painter.drawSelfRing(ctx, env, selfXY.x, selfXY.y) } finally { ctx.restore() }
      }
    } catch (e) { noteFail(e) } finally { tFrame += perfNow() - t0 }
  }

  function drawTop(ctx: CanvasRenderingContext2D) {
    const t0 = perfNow()
    try {
      if (bubbles.length || clusters.length) {
        ctx.save()
        for (let i = bubbles.length - 1; i >= 0; i--) {
          try { painter.drawBubble(ctx, env, bubbles[i]) } catch (e) { noteFail(e) }
        }
        for (const c of clusters) {
          try { painter.drawCluster(ctx, env, c.x + c.ox, c.y + c.oy, c.count) } catch (e) { noteFail(e) }
        }
        ctx.restore()
      }
    } catch (e) { noteFail(e) } finally {
      tFrame += perfNow() - t0
      if (items.length) pushSample(tFrame) // 有亮點的幀才計入耗時分佈（閒置幀不稀釋 p95）
    }
  }

  function clear() {
    try { states.clear(); resetFrame(); selfXY = null; selfRingOn = false } catch { /* ignore */ }
  }

  function debugPeers(): PeerDebugEntry[] {
    try {
      return items.map((it) => ({
        n: it.n, x: it.x, y: it.y, name: it.st.name, color: it.level === 'gray' ? MEET_GRAY : painter.color,
        level: it.level, bubble: it.bubble, text: it.text, alpha: it.alpha, clustered: it.clustered, box: it.box,
      }))
    } catch { return [] }
  }
  function debugClusters(): PeerClusterDebug[] {
    try { return clusters.map((c) => ({ x: c.x, y: c.y, count: c.count, ns: c.items.map((i) => i.n), drawX: c.x + c.ox, drawY: c.y + c.oy, box: { x: c.x + c.ox - c.w / 2, y: c.y + c.oy - c.h / 2, w: c.w, h: c.h }, merged: c.merged })) } catch { return [] }
  }
  function debugReserved(): Rect[] {
    try { return reservedUsed.map((r) => ({ x: r.x, y: r.y, w: r.w, h: r.h })) } catch { return [] }
  }
  function stats(): PeerFrameStats {
    try {
      const n = sampleN
      if (n === 0) return { p95: 0, median: 0, samples: 0, frames, fails, lastError, badgeMerges, badgeDissolved, acctRepairs, mergePassesMax }
      const arr = Array.from(samples.subarray(0, n)).sort((a, b) => a - b)
      return { p95: arr[Math.min(n - 1, Math.floor(0.95 * n))], median: arr[Math.floor(n / 2)], samples: n, frames, fails, lastError, badgeMerges, badgeDissolved, acctRepairs, mergePassesMax }
    } catch { return { p95: 0, median: 0, samples: 0, frames, fails, lastError, badgeMerges, badgeDissolved, acctRepairs, mergePassesMax } }
  }
  function destroy() {
    destroyed = true
    try { (document as Document & { fonts?: FontFaceSet }).fonts?.removeEventListener?.('loadingdone', onFonts) } catch { /* ignore */ }
    clear()
    widthCache.clear()
  }

  return { begin, drawDots, drawSelfRing, drawTop, clear, debugPeers, debugClusters, debugReserved, stats, destroy }
}
