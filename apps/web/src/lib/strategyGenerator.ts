// 自動建立賽事策略——純函式產生配速分段＋補給計畫（規格＝評審合成版，2026-08）。
// 只 import api.ts 既有型別（不改動 api.ts 的型別契約）；無 React/DOM/Date/Math.random 依賴，
// 同輸入同輸出，可在 RaceStrategyTab.tsx 與 verify-strategy-generator.mjs 兩處共用。
//
// 設計摘要（完整規則見規格文件，不重複貼在這裡）：
// 1) 距離快照：42.195/21.0975 附近 ±0.1km 視為標準全馬/半馬；其餘四捨五入到 0.01km。
// 2) 硬性拒絕 → 分級(F/M/S/C/W，依平均配速 P̄=T/D) → 固定段界模板 + 配速係數 δ（各級 Σδ×段長=0，
//    末段吸收捨入殘差，僅在殘差 >1 秒時對倒數第二段做 ±1 秒修正）。
// 3) 補給：能量膠/鹽錠/咖啡因走 time 觸發，運動飲料走 distance 觸發；咖啡因不新增點、改寫既有
//    膠點的 kind；超過 30 筆依序稀疏化（運動飲料→10km／鹽錠→90分／能量膠→60分／再刪最晚點）。

import type { StrategySegment, FuelPoint, FuelKind } from './api'

export interface GenerateInput { distanceKm: number; targetSeconds: number }

export interface GenerateOutput {
  segments: StrategySegment[] // 拒絕時為 []
  fuel: FuelPoint[]
  warnings: string[] // 已代入 {m:ss}/{h:mm} 的最終文案，依 §5 表順序附加；拒絕時只有一條
  avgPaceSecPerKm: number // P̄=T/D，浮點不取整；拒絕時 D,T>0 仍回傳，否則 0
}

export const DISTANCE_PRESETS = { full: 42.195, half: 21.0975 } as const

export type PaceClass = 'F' | 'M' | 'S' | 'C' | 'W'
export type WarnCode =
  | 'E_DIST' | 'E_TIME' | 'E_PACE_FAST' | 'E_PACE_SLOW'
  | 'W_ELITE' | 'W_WALK' | 'W_CUTOFF_FULL' | 'W_CUTOFF_HALF' | 'W_CUTOFF_CUSTOM' | 'W_CUTOFF_WALK'
  | 'W_SHORT' | 'W_NOFUEL' | 'W_ULTRA' | 'W_CLAMP' | 'W_FUEL_THINNED'

// 文案模板：{m:ss} 由 P̄ 格式化代入、{h:mm} 由 T 格式化代入（見 fillWarn）。此常數保留原始模板
// （含未代入的 placeholder 字樣），供測試比對；generateRaceStrategy 回傳的 warnings 是代入後的成品字串。
export const WARN_TEXT: Record<WarnCode, string> = {
  E_DIST: '自動建立僅支援 1–100 公里，請調整距離或手動建立。',
  E_TIME: '目標時間需介於 1 秒至 24 小時之間。',
  E_PACE_FAST: '目標配速 {m:ss}/km 快於 2:10/km，超出可設定範圍，請確認距離與時間是否輸入錯誤。',
  E_PACE_SLOW: '目標配速 {m:ss}/km 慢於 28:20/km，超出可設定範圍，請縮短目標時間或距離。',
  W_ELITE: '目標配速 {m:ss}/km 已達菁英水準，請確認目標時間是否輸入正確。',
  W_WALK: '目標配速 {m:ss}/km 慢於 12:00/km，接近走跑／健走，已改為等速分段；多數賽事會在此之前關門，請以簡章關門時間為準。',
  W_CUTOFF_FULL: '目標 {h:mm} 接近多數全馬 6.5–7 小時的關門時間，且沿途設有分站關門；本策略起步僅放慢 3%，請對照簡章逐站確認。',
  W_CUTOFF_HALF: '目標 {h:mm} 接近多數半馬 3–3.5 小時的關門時間，請對照簡章確認分站關門。',
  W_CUTOFF_CUSTOM: '目標配速 {m:ss}/km 在多數長距離賽事屬關門邊緣，請對照簡章確認分站關門。',
  W_CUTOFF_WALK: '目標時間接近多數賽事關門時間，請留意各分站關門時刻並保持穩定節奏。',
  W_SHORT: '距離過短，僅提供單一目標配速，不做分段。',
  W_NOFUEL: '1 小時內的賽事不安排賽中補給；請於賽前 2 小時補水 500 ml、起跑前 15 分鐘 150–200 ml。',
  W_ULTRA: '超馬距離：分段依比例切分、能量補給以時間觸發；固定食物、走路段與補給站停留請自行加入。',
  W_CLAMP: '目標配速接近系統邊界，已改為等速分段。',
  W_FUEL_THINNED: '補給點超過 30 筆上限，已自動稀疏化（運動飲料每 10 km／鹽錠每 90 分／能量膠每 60 分）。',
}

// {m:ss} 代入方向：文案含「慢於 X」時若四捨五入到剛好等於 X 秒會自相矛盾（例如 P̄=720.3 四捨五入
// 顯示 12:00 卻又說「慢於 12:00」），故對「慢於」類文案的配速一律 Math.ceil（顯示值只會更慢，
// 與「慢於」語意一致）；對「快於」類文案一律 Math.floor（顯示值只會更快）。其餘無方向詞的文案
// （W_ELITE／W_CUTOFF_CUSTOM）維持一般四捨五入。
const PACE_CEIL_CODES = new Set<WarnCode>(['E_PACE_SLOW', 'W_WALK'])
const PACE_FLOOR_CODES = new Set<WarnCode>(['E_PACE_FAST'])

// ── 小工具 ──────────────────────────────────────────────────────
function round1(x: number): number { return Math.round(x * 10) / 10 }

function fmtPaceStr(paceS: number, mode: 'round' | 'ceil' | 'floor' = 'round'): string {
  const t = mode === 'ceil' ? Math.ceil(paceS) : mode === 'floor' ? Math.floor(paceS) : Math.round(paceS)
  const m = Math.floor(t / 60)
  const s = t % 60
  return `${m}:${String(s).padStart(2, '0')}`
}
function fmtTimeStr(sec: number): string {
  const t = Math.round(sec)
  const h = Math.floor(t / 3600)
  const m = Math.floor((t % 3600) / 60)
  return `${h}:${String(m).padStart(2, '0')}`
}
function fillWarn(code: WarnCode, ctx: { pace?: number; time?: number }): string {
  let s = WARN_TEXT[code]
  if (ctx.pace !== undefined) {
    const mode = PACE_CEIL_CODES.has(code) ? 'ceil' : PACE_FLOOR_CODES.has(code) ? 'floor' : 'round'
    s = s.split('{m:ss}').join(fmtPaceStr(ctx.pace, mode))
  }
  if (ctx.time !== undefined) s = s.split('{h:mm}').join(fmtTimeStr(ctx.time))
  return s
}

// D<1 或 D>100 → 拒絕；42.195/21.0975 附近 ±0.1 視為標準賽道；其餘四捨五入到 0.01km
// （對應 DB total_km NUMERIC(6,2) 精度，讓自訂距離也能穩定對回同一組分段模板）。
function snapshotDistance(D: number): number {
  if (Math.abs(D - DISTANCE_PRESETS.full) <= 0.1) return DISTANCE_PRESETS.full
  if (Math.abs(D - DISTANCE_PRESETS.half) <= 0.1) return DISTANCE_PRESETS.half
  return Math.round(D * 100) / 100
}

function classify(P: number): PaceClass {
  if (P < 300) return 'F'
  if (P < 384) return 'M'
  if (P < 512) return 'S'
  if (P <= 720) return 'C'
  return 'W'
}

// ── 分段模板：段界 km × 配速係數 δ（各級非末段 Σ(段長×δ)=0，末段吸收捨入殘差）──
const FULL_DELTAS: Record<PaceClass, number[]> = {
  F: [0.02, -0.004, 0, 0],
  M: [0.04, -0.012, 0.005, 0.015],
  S: [0.05, -0.015, 0.005, 0.02],
  C: [0.03, -0.01, 0, 0.02],
  W: [0, 0, 0, 0],
}
const HALF_DELTAS: Record<PaceClass, number[]> = {
  F: [0.02, -0.005, 0],
  M: [0.04, -0.0125, 0.0075],
  S: [0.05, -0.015, 0.0075],
  C: [0.03, -0.01, 0.0075],
  W: [0, 0, 0],
}

// 自訂距離段界偶爾因四捨五入到 0.1km 與前一界重疊（僅發生在極窄的邊界帶，如 2<D<2.05）；
// 強制往後推 0.1km 避免產生零長度分段（後端 invalid_segment_range）。首尾兩端（0 與 D）不動。
function ensureIncreasing(bs: number[]): number[] {
  const out = bs.slice()
  for (let i = 1; i < out.length - 1; i++) {
    if (out[i] <= out[i - 1]) out[i] = round1(out[i - 1] + 0.1)
  }
  return out
}

function template(D: number, cls: PaceClass): { boundaries: number[]; deltas: number[] } {
  if (D === DISTANCE_PRESETS.full) return { boundaries: [0, 5, 30, 35, 40, DISTANCE_PRESETS.full], deltas: FULL_DELTAS[cls] }
  if (D === DISTANCE_PRESETS.half) return { boundaries: [0, 3, 15, 19, DISTANCE_PRESETS.half], deltas: HALF_DELTAS[cls] }
  if (D <= 2) return { boundaries: [0, D], deltas: [] }
  if (D < 10) return { boundaries: ensureIncreasing([0, 1, round1(D - 1), D]), deltas: cls === 'W' ? [0, 0] : [0.03, 0] }
  if (D < 30) return { boundaries: ensureIncreasing([0, round1(0.15 * D), round1(0.75 * D), round1(Math.min(0.95 * D, D - 1)), D]), deltas: HALF_DELTAS[cls] }
  return { boundaries: ensureIncreasing([0, round1(0.12 * D), round1(0.72 * D), round1(0.84 * D), round1(Math.max(0.96 * D, D - 2)), D]), deltas: FULL_DELTAS[cls] }
}

// 逐段配速：非末段 round(P̄×(1+δᵢ))；末段吸收捨入殘差；|殘差|>1 秒時對倒數第二段做 ±1 秒修正一次；
// 修正後任一段仍落在 [120,1800] 之外就全段 δ:=0 重算一次（W_CLAMP）——防禦路徑，但非純理論：
// P̄≈130–132 的自訂距離（δ 使某段配速逼近 120 下限）會實際觸發，非僅防禦性程式碼。
// absorbInMiddle：僅「自訂且 D>=30」啟用（呼叫端傳入）。末段吸收捨入殘差後，若末段配速相對 P̄
// 偏差 >3%，改由「最長的中段」（排除首末兩段）吸收——對其 pace 逐步 ±1 秒/km、每步重算末段，
// 直到末段偏差 <=3% 或中段累積調整量超過其原始配速的 ±3%（達到上限則停止並保留當下狀態）。
// 只對自訂 D>=30 開啟，確保全馬/半馬與 <30km 自訂距離的六組規格向量數值不變。
function computeSegments(boundaries: number[], deltas: number[], P: number, T: number, absorbInMiddle = false): { segments: StrategySegment[]; clamped: boolean } {
  const n = boundaries.length - 1
  const lens = (i: number) => boundaries[i + 1] - boundaries[i]

  function computeLast(paceS: number[]): number {
    let sumOther = 0
    for (let i = 0; i < n - 1; i++) sumOther += paceS[i] * lens(i)
    return Math.round((T - sumOther) / lens(n - 1))
  }
  function residual(paceS: number[]): number {
    let sum = 0
    for (let i = 0; i < n; i++) sum += paceS[i] * lens(i)
    return sum - T
  }
  function build(useDeltas: number[] | null): number[] {
    const paceS: number[] = new Array(n)
    for (let i = 0; i < n - 1; i++) paceS[i] = Math.round(P * (1 + (useDeltas ? useDeltas[i] : 0)))
    paceS[n - 1] = computeLast(paceS)
    if (n >= 2) {
      const r = residual(paceS)
      if (Math.abs(r) > 1) {
        paceS[n - 2] -= Math.sign(r)
        paceS[n - 1] = computeLast(paceS)
      }
    }
    return paceS
  }

  let paceS = build(deltas)
  let clamped = false
  if (paceS.some((p) => p < 120 || p > 1800)) {
    clamped = true
    paceS = build(null)
  }

  if (absorbInMiddle && !clamped && n >= 3) {
    const lastIdx = n - 1
    const baseline = paceS.slice()
    const initDev = Math.abs(baseline[lastIdx] - P) / P
    if (initDev > 0.03) {
      // 候選中段（排除首末兩段）依長度由長到短嘗試，優先用「最長的中段」；但中段長度可能遠大於
      // 末段（例如自訂超馬模板的巡航段 60km vs 末段 2km），單步 ±1 秒/km 換算到末段會是數十秒的
      // 跳動，可能整段跳過原本只有幾秒寬的 3% 容許帶、怎麼調都無法收斂——此時退而嘗試次長的中段
      // （其換算跳動較小，較可能落入容許帶）。方向在每個候選的迴圈開始前只決定一次（不逐步依當下
      // 正負號重新取號，否則會來回震盪、永遠出不了迴圈）；每步若無法讓末段更接近 P̄ 就停止並還原。
      const candidates: number[] = []
      for (let i = 1; i <= n - 2; i++) candidates.push(i)
      candidates.sort((a, b) => lens(b) - lens(a))

      let bestPaceS = baseline
      let bestDev = initDev

      for (const midIdx of candidates) {
        const trial = baseline.slice()
        let dev = initDev
        const dir = Math.sign(trial[lastIdx] - P) || 1
        const cap = Math.abs(trial[midIdx]) * 0.03
        let adjustedBy = 0
        while (dev > 0.03 && Math.abs(adjustedBy) + 1 <= cap) {
          const candidateMid = trial[midIdx] + dir
          if (candidateMid < 120 || candidateMid > 1800) break
          const savedLast = trial[lastIdx]
          trial[midIdx] = candidateMid
          const candidateLast = computeLast(trial)
          const candidateDev = Math.abs(candidateLast - P) / P
          if (candidateDev >= dev) {
            trial[midIdx] = candidateMid - dir // 還原：這步無法讓末段更接近 P̄
            trial[lastIdx] = savedLast
            break
          }
          trial[lastIdx] = candidateLast
          adjustedBy += dir
          dev = candidateDev
        }
        if (dev < bestDev) { bestDev = dev; bestPaceS = trial }
        if (dev <= 0.03) break // 已有候選達標，優先採用較長的中段，不再嘗試更短的段
      }
      paceS = bestPaceS
    }
  }

  const segments: StrategySegment[] = []
  for (let i = 0; i < n; i++) segments.push({ from_km: boundaries[i], to_km: boundaries[i + 1], pace_s: paceS[i] })
  return { segments, clamped }
}

// ── 補給 ─────────────────────────────────────────────────────────
const KIND_ORDER: Record<FuelKind, number> = { gel: 0, caffeine: 1, salt: 2, electrolyte: 3 }

interface FuelConfig { gelIntervalMin: number; saltIntervalMin: number; electrolyteIntervalKm: number }
function defaultFuelConfig(cls: PaceClass): FuelConfig {
  const gelIntervalMin = cls === 'S' ? 40 : cls === 'C' || cls === 'W' ? 45 : 30 // F/M:30
  return { gelIntervalMin, saltIntervalMin: 60, electrolyteIntervalKm: 5 }
}

// 咖啡因不新增點、改寫既有膠點的 kind（就地 mutate points 陣列內物件）：
// 主劑＝最晚一個 t ≤ T_min-60 的膠點；第一劑（僅 T≥5h）＝最接近 T_min/2 的膠點（不得是主劑，
// 且與主劑須間隔 ≥60 分，否則捨棄）。
function applyCaffeine(points: FuelPoint[], T: number) {
  if (T < 7200) return
  const gelPoints = points.filter((p) => p.kind === 'gel' && p.mode === 'time')
  const cutoffMain = T - 3600
  const eligibleMain = gelPoints.filter((p) => p.at <= cutoffMain)
  if (eligibleMain.length === 0) return
  const mainDose = eligibleMain.reduce((a, b) => (b.at > a.at ? b : a))

  let firstDose: FuelPoint | null = null
  if (T >= 18000) {
    const half = T / 2
    const candidates = gelPoints.filter((p) => p !== mainDose)
    if (candidates.length > 0) {
      firstDose = candidates.reduce((best, cur) => {
        const db = Math.abs(best.at - half)
        const dc = Math.abs(cur.at - half)
        if (dc < db) return cur
        if (dc === db) return cur.at < best.at ? cur : best
        return best
      })
      if (mainDose.at - firstDose.at < 3600) firstDose = null
    }
  }
  mainDose.kind = 'caffeine'
  if (firstDose) firstDose.kind = 'caffeine'
}

// salt 與 gel/caffeine 落在同一個 time 觸發點時，track 專注模式的補給引擎是單游標依序消化，
// 第二個點會晚 30 秒才出現——在最終排序前把 salt 位移化解：先試著往後移 60 秒（仍為 60 倍數）；
// 若移動後超過原本的生成截止秒數（T-1800，即 30 分鐘前）就改往前移 60 秒；若仍碰撞則同方向
// 再位移一次，最多嘗試 3 次，仍碰撞則保留原值（極端情況下寧可保留碰撞也不移出合理範圍）。
function resolveSaltCollisions(points: FuelPoint[], T: number): void {
  const cutoff = T - 1800
  for (const p of points) {
    if (p.kind !== 'salt' || p.mode !== 'time') continue
    const occupied = () => points.some((q) => q !== p && q.mode === 'time' && (q.kind === 'gel' || q.kind === 'caffeine') && q.at === p.at)
    if (!occupied()) continue
    const original = p.at
    const dir: 1 | -1 = original + 60 <= cutoff ? 1 : -1
    for (let attempt = 1; attempt <= 3; attempt++) {
      p.at = original + dir * 60 * attempt
      if (!occupied()) break
      if (attempt === 3) p.at = original // 三次仍碰撞，保留原值
    }
  }
}

// distance 補給點換算成「預估開跑後秒數」（供排序用）：走過幾個完整段＋在當前段內的比例時間。
function elapsedAtKm(km: number, segments: StrategySegment[]): number {
  let t = 0
  for (const seg of segments) {
    if (km >= seg.to_km) t += (seg.to_km - seg.from_km) * seg.pace_s
    else if (km > seg.from_km) { t += (km - seg.from_km) * seg.pace_s; break }
    else break
  }
  return t
}
function sortFuel(points: FuelPoint[], segments: StrategySegment[]) {
  points.sort((a, b) => {
    const ka = a.mode === 'time' ? a.at : elapsedAtKm(a.at / 1000, segments)
    const kb = b.mode === 'time' ? b.at : elapsedAtKm(b.at / 1000, segments)
    if (ka !== kb) return ka - kb
    return KIND_ORDER[a.kind] - KIND_ORDER[b.kind]
  })
}

function generateFuelWithConfig(D: number, T: number, segments: StrategySegment[], cfg: FuelConfig): FuelPoint[] {
  const points: FuelPoint[] = []
  if (T >= 4500) {
    const deltaS = cfg.gelIntervalMin * 60, cutoff = T - 1500
    for (let k = 1; ; k++) { const t = k * deltaS; if (t > cutoff) break; points.push({ kind: 'gel', mode: 'time', at: t }) }
  }
  if (T >= 5400) {
    const deltaS = cfg.saltIntervalMin * 60, cutoff = T - 1800
    for (let k = 1; ; k++) { const t = k * deltaS; if (t > cutoff) break; points.push({ kind: 'salt', mode: 'time', at: t }) }
  }
  if (D >= 10) {
    const deltaM = cfg.electrolyteIntervalKm * 1000, cutoffM = (D - 2) * 1000
    for (let k = 1; ; k++) { const at = k * deltaM; if (at > cutoffM) break; points.push({ kind: 'electrolyte', mode: 'distance', at }) }
  }
  applyCaffeine(points, T)
  resolveSaltCollisions(points, T)
  sortFuel(points, segments)
  return points
}

// 30 筆上限稀疏化階梯：①電解質→10km ②鹽錠→90分 ③能量膠→60分 ④仍超過則從最晚的點刪起。
// 每步後含咖啡因重新放置一併重算（generateFuelWithConfig 內已含 applyCaffeine）。
function buildFuel(D: number, T: number, cls: PaceClass, segments: StrategySegment[]): { fuel: FuelPoint[]; thinned: boolean } {
  if (T < 3600) return { fuel: [], thinned: false }
  let cfg = defaultFuelConfig(cls)
  let fuel = generateFuelWithConfig(D, T, segments, cfg)
  if (fuel.length <= 30) return { fuel, thinned: false }

  cfg = { ...cfg, electrolyteIntervalKm: 10 }
  fuel = generateFuelWithConfig(D, T, segments, cfg)
  if (fuel.length <= 30) return { fuel, thinned: true }

  cfg = { ...cfg, saltIntervalMin: 90 }
  fuel = generateFuelWithConfig(D, T, segments, cfg)
  if (fuel.length <= 30) return { fuel, thinned: true }

  cfg = { ...cfg, gelIntervalMin: 60 }
  fuel = generateFuelWithConfig(D, T, segments, cfg)
  if (fuel.length <= 30) return { fuel, thinned: true }

  while (fuel.length > 30) fuel.pop() // 已依「預估開跑後秒數」排序，pop 掉的即最晚的點
  return { fuel, thinned: true }
}

// ── 主函式 ───────────────────────────────────────────────────────
export function generateRaceStrategy(input: GenerateInput): GenerateOutput {
  const D = snapshotDistance(input.distanceKm)
  const T = input.targetSeconds
  const avgPreview = D > 0 && T > 0 ? T / D : 0

  if (!(D >= 1) || D > 100) return { segments: [], fuel: [], warnings: [fillWarn('E_DIST', {})], avgPaceSecPerKm: avgPreview }
  if (!(T > 0) || T > 86400) return { segments: [], fuel: [], warnings: [fillWarn('E_TIME', {})], avgPaceSecPerKm: avgPreview }

  const P = T / D
  if (P < 130) return { segments: [], fuel: [], warnings: [fillWarn('E_PACE_FAST', { pace: P })], avgPaceSecPerKm: P }
  if (P > 1700) return { segments: [], fuel: [], warnings: [fillWarn('E_PACE_SLOW', { pace: P })], avgPaceSecPerKm: P }

  const cls = classify(P)
  const isFull = D === DISTANCE_PRESETS.full
  const isHalf = D === DISTANCE_PRESETS.half
  const isCustom = !isFull && !isHalf

  const warnings: string[] = []
  if (P >= 130 && P < 180) warnings.push(fillWarn('W_ELITE', { pace: P }))
  if (cls === 'W') warnings.push(fillWarn('W_WALK', { pace: P }))
  // 關門提醒僅在 C 級（尚未整段等速）附「起步僅放慢 3%」類文案；W 級（全段等速，已無「起步放慢」
  // 這回事）改附不含「3%」的通用關門提醒 W_CUTOFF_WALK，避免自相矛盾。
  const cutoffEligible = (isFull && T > 23400) || (isHalf && T > 10800) || (isCustom && D >= 30 && P >= 512)
  if (cls === 'C') {
    if (isFull && T > 23400) warnings.push(fillWarn('W_CUTOFF_FULL', { time: T }))
    if (isHalf && T > 10800) warnings.push(fillWarn('W_CUTOFF_HALF', { time: T }))
    if (isCustom && D >= 30 && P >= 512) warnings.push(fillWarn('W_CUTOFF_CUSTOM', { pace: P }))
  } else if (cls === 'W' && cutoffEligible) {
    warnings.push(fillWarn('W_CUTOFF_WALK', {}))
  }
  if (isCustom && D <= 2) warnings.push(fillWarn('W_SHORT', {}))
  if (T < 3600) warnings.push(fillWarn('W_NOFUEL', {}))
  if (isCustom && D > DISTANCE_PRESETS.full) warnings.push(fillWarn('W_ULTRA', {}))

  const { boundaries, deltas } = template(D, cls)
  const { segments, clamped } = computeSegments(boundaries, deltas, P, T, isCustom && D >= 30)
  if (clamped) warnings.push(fillWarn('W_CLAMP', {}))

  const { fuel, thinned } = buildFuel(D, T, cls, segments)
  if (thinned) warnings.push(fillWarn('W_FUEL_THINNED', {}))

  return { segments, fuel, warnings, avgPaceSecPerKm: P }
}

// ── 呼叫端 helper（非 generateRaceStrategy 本體，供 RaceStrategyTab 命名用）──
// 「{全馬｜半馬｜{D}K} {h:mm} 自動策略」；{D}K 去尾零（30K、12.5K）。
export function autoStrategyName(distanceKm: number, targetSeconds: number): string {
  const D = snapshotDistance(distanceKm)
  const distLabel = D === DISTANCE_PRESETS.full ? '全馬' : D === DISTANCE_PRESETS.half ? '半馬' : `${D}K`
  const h = Math.floor(targetSeconds / 3600)
  const m = Math.floor((targetSeconds % 3600) / 60)
  return `${distLabel} ${h}:${String(m).padStart(2, '0')} 自動策略`
}

// ── 預覽明細格式化（純函式，供 RaceStrategyTab「產生建議」後的可捲動明細呼叫；不依賴 api.ts
// 執行期匯出，僅在此複寫與 FUEL_KIND_LABEL 同值的中文標籤，維持本檔「只 import 型別」慣例）──
const FUEL_KIND_TEXT: Record<FuelKind, string> = { caffeine: '咖啡因錠', gel: '能量膠', salt: '鹽錠', electrolyte: '電解質' }
// 明細內容合併時的種類順序（咖啡因 → 能量膠 → 鹽錠 → 電解質/運動飲料）
const FUEL_DISPLAY_ORDER: FuelKind[] = ['caffeine', 'gel', 'salt', 'electrolyte']

function fmtKmDisp(km: number): string {
  return String(Math.round(km * 100) / 100)
}
function fmtElapsedHM(sec: number): string {
  const t = Math.round(sec)
  const h = Math.floor(t / 3600)
  const m = Math.floor((t % 3600) / 60)
  return h > 0 ? `${h} 小時 ${m} 分` : `${m} 分`
}
// time 觸發補給點的「開跑後秒數」換算成對應的預估公里數（elapsedAtKm 的反函式）
function kmAtElapsedTime(t: number, segments: StrategySegment[]): number {
  let elapsed = 0
  for (const seg of segments) {
    const dur = (seg.to_km - seg.from_km) * seg.pace_s
    if (t <= elapsed + dur) return seg.from_km + (seg.pace_s > 0 ? (t - elapsed) / seg.pace_s : 0)
    elapsed += dur
  }
  return segments.length ? segments[segments.length - 1].to_km : 0
}

// 分段明細：「{from}–{to} km｜{m:ss}/km（{±x%}）」，±% = round(pace_s÷avgPace − 1) 的整數百分比。
export function formatSegmentPreviewLines(segments: StrategySegment[], avgPaceSecPerKm: number): string[] {
  if (!(avgPaceSecPerKm > 0)) return []
  return segments.map((seg) => {
    const pct = Math.round((seg.pace_s / avgPaceSecPerKm - 1) * 100)
    const pctStr = pct > 0 ? `+${pct}%` : `${pct}%`
    return `${fmtKmDisp(seg.from_km)}–${fmtKmDisp(seg.to_km)} km｜${fmtPaceStr(seg.pace_s)}/km（${pctStr}）`
  })
}

// 補給明細：同一 (mode, at) 合併一行，內容依咖啡因→能量膠→鹽錠→電解質排序以「＋」串接；
// time 點格式「第 n 次補給｜開跑後 {h 小時 }{m 分}（約 {km} km）：{內容}」，
// distance 點格式「第 n 次補給｜第 {km} km 水站：{內容}」。
export function formatFuelPreviewLines(fuel: FuelPoint[], segments: StrategySegment[]): string[] {
  const groups: { mode: 'time' | 'distance'; at: number; kinds: Set<FuelKind> }[] = []
  const index = new Map<string, number>()
  for (const f of fuel) {
    const key = `${f.mode}:${f.at}`
    let idx = index.get(key)
    if (idx === undefined) {
      idx = groups.length
      index.set(key, idx)
      groups.push({ mode: f.mode, at: f.at, kinds: new Set() })
    }
    groups[idx].kinds.add(f.kind)
  }
  return groups.map((g, i) => {
    const content = FUEL_DISPLAY_ORDER.filter((k) => g.kinds.has(k)).map((k) => FUEL_KIND_TEXT[k]).join('＋')
    if (g.mode === 'time') {
      const km = fmtKmDisp(kmAtElapsedTime(g.at, segments))
      return `第 ${i + 1} 次補給｜開跑後 ${fmtElapsedHM(g.at)}（約 ${km} km）：${content}`
    }
    const km = fmtKmDisp(g.at / 1000)
    return `第 ${i + 1} 次補給｜第 ${km} km 水站：${content}`
  })
}
