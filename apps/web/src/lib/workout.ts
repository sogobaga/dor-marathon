import type { WorkoutSegment, TemplateSegment, PaceLevel, WorkoutTemplate } from './api'

// 結構化課表：把 segments（含 reps）展開成「一步一步」的執行序列，供 GPS 追蹤逐段驅動。
export type WoStep = {
  kind: string            // warmup | work | rest | recovery | cooldown | steady
  label: string           // 顯示名（含「第 3/6 趟」）
  targetType: 'distance' | 'time'
  target: number          // 公尺 或 秒
  paceFast?: number       // 較快界（秒/公里，較小）
  paceSlow?: number       // 較慢界
  graded: boolean         // 是否列入配速達成度評星（work 段）
}

const KIND_LABEL: Record<string, string> = {
  warmup: '暖身', work: '主課', rest: '組間休息', recovery: '恢復', cooldown: '緩和', steady: '穩定跑', surge: '加速',
}
// 列入配速評星的分段類型（有配速區間才評）：主課/穩定跑/加速；暖身/緩和/組間休/恢復不評。
const GRADED_KINDS = new Set(['work', 'steady', 'surge'])

export function kindLabel(kind: string): string { return KIND_LABEL[kind] || kind }

// 展開：work 400m ×6（間休 60s）→ [work,休,work,休,...,work]（reps-1 個組間休）
export function expandSegments(segs: WorkoutSegment[] | null | undefined): WoStep[] {
  const out: WoStep[] = []
  for (const s of segs || []) {
    const reps = s.reps && s.reps > 1 ? s.reps : 1
    for (let i = 1; i <= reps; i++) {
      const base = s.label || kindLabel(s.kind)
      out.push({
        kind: s.kind,
        label: reps > 1 ? `${base} 第 ${i}/${reps} 趟` : base,
        targetType: s.target_type,
        target: s.target,
        paceFast: s.pace_fast_s,
        paceSlow: s.pace_slow_s,
        graded: GRADED_KINDS.has(s.kind) && !!s.pace_slow_s,
      })
      if (s.rest_s && s.rest_s > 0 && i < reps) {
        out.push({ kind: 'rest', label: '組間休息', targetType: 'time', target: s.rest_s, graded: false })
      }
    }
  }
  return out
}

export function fmtPaceS(s?: number): string {
  if (!s || !isFinite(s) || s <= 0) return '--:--'
  return `${Math.floor(s / 60)}:${String(Math.round(s % 60)).padStart(2, '0')}`
}

// 配速區間文字：「5:30–6:00 /km」（fast 較快=較小在前）
export function paceBand(fast?: number, slow?: number): string {
  if (!fast && !slow) return ''
  return `${fmtPaceS(fast)}–${fmtPaceS(slow)} /km`
}

// 目標文字：距離「400m / 2.0K」或時間「60秒」
export function targetText(step: WoStep): string {
  if (step.targetType === 'time') return `${step.target}秒`
  return step.target >= 1000 ? `${(step.target / 1000).toFixed(step.target % 1000 ? 1 : 0)}K` : `${step.target}m`
}

// 判定一段 work 是否落在配速區間：達到「較慢界」的速度即算達成（跑更快也算），給 5% GPS 容差。
export function paceInBand(avgPaceS: number, step: WoStep): boolean {
  if (!step.paceSlow) return true
  return avgPaceS > 0 && avgPaceS <= step.paceSlow * 1.05
}

// 目標配速區間：該課表的「目標效率配速」——優先取評分段(work/steady/surge)的配速範圍；
// 若無評分段(純輕鬆/長跑/恢復)則取所有有配速的段。回「5:30–6:00 /km」，無配速回 ''。
export function targetPaceBand(segs?: WorkoutSegment[] | null): string {
  if (!segs || !segs.length) return ''
  const paced = segs.filter((s) => s.pace_slow_s && s.pace_slow_s > 0)
  const graded = paced.filter((s) => GRADED_KINDS.has(s.kind))
  const pool = graded.length ? graded : paced
  if (!pool.length) return ''
  let fast = Infinity
  let slow = 0
  for (const s of pool) {
    if (s.pace_fast_s && s.pace_fast_s < fast) fast = s.pace_fast_s
    if (s.pace_slow_s && s.pace_slow_s > slow) slow = s.pace_slow_s
  }
  if (!isFinite(fast)) fast = slow
  return paceBand(fast, slow)
}

// 分段課表摘要：「暖身 2K → 間歇 400m ×6 → 緩和 2K」
// 若 label 本身已等於或以距離字串 d 開頭（如金字塔 work 段 label 直接就是 '400m'/'800m'…），不再另接 d，
// 避免「400m 400m」重複；label 為一般描述（如「輕鬆跑」）時仍照舊接上 d。
export function segSummary(segs?: WorkoutSegment[] | null): string {
  if (!segs || !segs.length) return ''
  return segs.map((s) => {
    const isKm = s.target_type === 'distance' && s.target >= 1000
    const d = s.target_type === 'distance' ? (isKm ? `${s.target / 1000}K` : `${s.target}m`) : `${Math.round(s.target / 60) || Math.round(s.target)}${s.target >= 60 ? '分' : '秒'}`
    const label = s.label || kindLabel(s.kind)
    const reps = s.reps && s.reps > 1 ? ` ×${s.reps}` : ''
    const dupD = label === d || label.startsWith(d)
    if (dupD) return `${label}${reps}`
    // 距離 ≥1000m 時 d 換成 K 制（如 target=2000 → d="2K"），但部份課表 label 仍用 m 制自帶距離前綴
    // （如關主課表 label="2000m 間歇"），字面對不上上面的 K 制比對、才會漏接成「2000m 間歇 2K」這種
    // 同一距離講兩次。這裡另外比對 m 制寫法，命中就剝掉重複的距離前綴，只留描述文字再接 d（K 制）。
    const mForm = isKm ? `${s.target}m` : null
    if (mForm && (label === mForm || label.startsWith(mForm))) {
      const core = label.slice(mForm.length).trim()
      return core ? `${core} ${d}${reps}` : `${d}${reps}`
    }
    return `${label} ${d}${reps}`
  }).join(' → ')
}

// 總距離（公里）：距離型分段 × 組數直接加總；時間型「主課」分段（節奏跑/乳酸閾值跑/法特萊克等「跑 X 秒」）
// 換算成距離再加總：公里 = 秒數 ÷ 配速(秒/公里) → 配速取該段已解析的中位數 (pace_fast_s+pace_slow_s)/2
// （與後端 training.go pm.mid(effort) 同語意）；段上還沒解析配速（僅有 effort，尚未套配速等級）則落回
// 420 秒/km，比照後端 pm.mid 的 fallback、也與本檔 estMinutes 既有的距離型 fallback 一致。
// 組間休息不計入距離：reps 展開的組間休息走 rest_s 欄位（本函式本就不加總），但金字塔這類把組間恢復
// 拆成獨立 recovery/rest 段（time 型）的課表，也要排除，否則恢復段會被誤當主課換算成距離、隨配速等級
// 膨脹總距離——與後端 segTotalKm 的加總公式一致，避免課表庫（前端算）與月曆（後端存 planned_km）顯示
// 不同總距離。
const NON_TRAINING_KINDS = new Set(['recovery', 'rest'])
export function totalKm(segs?: WorkoutSegment[] | null): number {
  if (!segs) return 0
  let m = 0
  for (const s of segs) {
    const reps = s.reps && s.reps > 1 ? s.reps : 1
    if (s.target_type === 'distance') {
      m += s.target * reps
    } else if (!NON_TRAINING_KINDS.has(s.kind)) {
      const pace = s.pace_fast_s && s.pace_slow_s ? (s.pace_fast_s + s.pace_slow_s) / 2 : 420
      m += (s.target / pace) * 1000 * reps
    }
  }
  return Math.round(m / 100) / 10
}
// 預估完成時間（分）：距離段用配速中位數估、時間段直接計，加組間休。
export function estMinutes(segs?: WorkoutSegment[] | null): number {
  if (!segs) return 0
  let total = 0
  for (const s of segs) {
    const reps = s.reps && s.reps > 1 ? s.reps : 1
    if (s.target_type === 'distance') {
      const p = s.pace_fast_s && s.pace_slow_s ? (s.pace_fast_s + s.pace_slow_s) / 2 : 420
      total += (s.target / 1000) * p * reps
    } else {
      total += s.target * reps
    }
    if (reps > 1 && s.rest_s) total += s.rest_s * (reps - 1)
  }
  return Math.round(total / 60)
}
export function fmtDuration(min: number): string {
  if (min <= 0) return '—'
  if (min < 60) return `${min} 分`
  return `${Math.floor(min / 60)} 時 ${min % 60} 分`
}

// ── 自主訓練（P1）：課表庫「效度分段」→ 既有 WorkoutSegment ──
const EASY_DEFAULT_KINDS = new Set(['warmup', 'cooldown', 'recovery']) // 沒標 effort 的暖身/緩和/恢復段一律用輕鬆配速

function clamp(v: number, lo: number, hi: number): number { return Math.min(hi, Math.max(lo, v)) }

// 課表可微調（migration 085）：金字塔 work 段的距離級距。
const PYRAMID_STEP_M = 400

// 金字塔峰值(m)：該 pyramid 課表 work 段目前最大距離（預設 1600）+ adjust 階（每階 400m），夾 [800,2400]。
// 課表庫卡片的精簡摘要與微調 bounds 檢查共用。
export function pyramidPeak(segments?: TemplateSegment[] | null, adjust = 0): number {
  const work = (segments || []).filter((s) => s.kind === 'work')
  const basePeak = work.length ? Math.max(...work.map((s) => s.target)) : 1600
  return clamp(basePeak + (adjust || 0) * PYRAMID_STEP_M, 800, 2400)
}

// 微調（migration 085）：解析配速「之前」先依 adjust_type 調整 TemplateSegment[]。
// adjust=0（或未帶 adjustType）原樣不動 → P1/P2/P3 既有行為不回歸。
// distance：delta(km) 平均分攤到所有 work 距離段(kind==='work' && target_type==='distance')，各段夾 ≥1000m
//           （單一 work 段就整份加；progression 多段均分）。
// reps：主間歇 work 段（reps>1 者優先，否則第一個 work 段）reps += delta，夾 [1,20]。
// pyramid：以新峰值（pyramidPeak）重建中段 400m 級距 work+recovery（work 之間插組間恢復，沿用原 rest_s
//          或預設 150s），保留原 warmup/cooldown 段。
export function applyAdjust(segments?: TemplateSegment[] | null, adjustType?: string, adjust?: number): TemplateSegment[] {
  const segs = segments || []
  const delta = adjust || 0
  if (!delta || !adjustType || adjustType === 'none') return segs

  if (adjustType === 'distance') {
    const idx = segs.reduce<number[]>((acc, s, i) => { if (s.kind === 'work' && s.target_type === 'distance') acc.push(i); return acc }, [])
    if (!idx.length) return segs
    const share = (delta * 1000) / idx.length
    return segs.map((s, i) => (idx.includes(i) ? { ...s, target: Math.max(1000, s.target + share) } : s))
  }

  if (adjustType === 'reps') {
    let mainIdx = segs.findIndex((s) => s.kind === 'work' && (s.reps || 1) > 1)
    if (mainIdx < 0) mainIdx = segs.findIndex((s) => s.kind === 'work')
    if (mainIdx < 0) return segs
    return segs.map((s, i) => (i === mainIdx ? { ...s, reps: clamp((s.reps || 1) + delta, 1, 20) } : s))
  }

  if (adjustType === 'pyramid') {
    const warmup = segs.filter((s) => s.kind === 'warmup')
    const cooldown = segs.filter((s) => s.kind === 'cooldown')
    const work = segs.filter((s) => s.kind === 'work')
    const restS = work.find((s) => s.rest_s)?.rest_s || 150
    const peak = pyramidPeak(segs, delta)
    const up: number[] = []
    for (let d = PYRAMID_STEP_M; d <= peak; d += PYRAMID_STEP_M) up.push(d)
    const ladder = [...up, ...up.slice(0, -1).reverse()]
    const middle: TemplateSegment[] = []
    ladder.forEach((d, i) => {
      middle.push({ kind: 'work', label: `${d}m`, effort: 'interval', target_type: 'distance', target: d })
      if (i < ladder.length - 1) middle.push({ kind: 'recovery', label: '組間恢復', effort: 'easy', target_type: 'time', target: restS })
    })
    return [...warmup, ...middle, ...cooldown]
  }

  return segs
}

// 依玩家選定的配速等級，把課表庫的 TemplateSegment[]（effort 表達強度）解析成既有 WorkoutSegment[]（實際配速秒/公里）。
// adjustType/adjust（migration 085）：解析配速前先套用微調（見 applyAdjust）；未帶或 adjust=0 時行為與現況相同。
export function resolveTemplate(segments?: TemplateSegment[] | null, level?: PaceLevel | null, adjustType?: string, adjust?: number): WorkoutSegment[] {
  if (!segments || !level) return []
  const adjusted = applyAdjust(segments, adjustType, adjust)
  return adjusted.map((s) => {
    const effort = s.effort || (EASY_DEFAULT_KINDS.has(s.kind) ? 'easy' : undefined)
    const p = effort ? level.paces[effort] : undefined
    return {
      kind: s.kind,
      label: s.label,
      target_type: s.target_type,
      target: s.target,
      reps: s.reps,
      rest_s: s.rest_s,
      pace_fast_s: p?.fast,
      pace_slow_s: p?.slow,
    }
  })
}

// 微調 UI 中繼資料：依 template.adjust_type 決定單位/步階/上下界（課表庫卡片、選課表 modal 的 −/＋ 共用）。
// level：distance 型 min 需要換算時間型分段的實際距離（見 totalKm），沒帶（理論上只在資料尚未載入時發生）
// 則落回未解析的 TemplateSegment[]，totalKm 內部會用 420 秒/km 的 fallback，行為與過去一致。
export interface AdjustMeta { type: string; unit: string; step: number; min: number; max: number }
export function adjustMeta(template: Pick<WorkoutTemplate, 'adjust_type' | 'segments'>, level?: PaceLevel | null): AdjustMeta {
  switch (template.adjust_type) {
    case 'distance': return { type: 'distance', unit: 'km', step: 1, min: totalKm(level ? resolveTemplate(template.segments, level, 'distance', -999) : applyAdjust(template.segments, 'distance', -999)), max: 40 }
    case 'reps': return { type: 'reps', unit: '趟', step: 1, min: 1, max: 20 }
    case 'pyramid': return { type: 'pyramid', unit: 'm', step: 400, min: 800, max: 2400 }
    default: return { type: 'none', unit: '', step: 0, min: 0, max: 0 }
  }
}
// 微調後的目前數值（距離型＝套用 adjust 後的總距離 km；間歇型＝主課 reps；金字塔＝峰值 m）；
// 供顯示文字（currentValue）與 UI bounds 檢查（disable −/＋）共用，避免各自重算一次。
export function adjustedValue(template: Pick<WorkoutTemplate, 'adjust_type' | 'segments'>, adjust: number, level?: PaceLevel | null): number {
  const meta = adjustMeta(template, level)
  if (meta.type === 'distance') return totalKm(level ? resolveTemplate(template.segments, level, 'distance', adjust) : applyAdjust(template.segments, 'distance', adjust))
  if (meta.type === 'reps') {
    const segs = applyAdjust(template.segments, 'reps', adjust)
    let idx = segs.findIndex((s) => s.kind === 'work' && (s.reps || 1) > 1)
    if (idx < 0) idx = segs.findIndex((s) => s.kind === 'work')
    return idx >= 0 ? (segs[idx].reps || 1) : 1
  }
  if (meta.type === 'pyramid') return pyramidPeak(template.segments, adjust)
  return 0
}
// 微調目前值顯示文字：距離型「總距離 6 K」、間歇型「8 趟」、金字塔「峰值 1600m」。
export function currentValue(template: Pick<WorkoutTemplate, 'adjust_type' | 'segments'>, adjust: number, level?: PaceLevel | null): string {
  const meta = adjustMeta(template, level)
  const n = adjustedValue(template, adjust, level)
  if (meta.type === 'distance') return `總距離 ${n} K`
  if (meta.type === 'reps') return `${n} 趟`
  if (meta.type === 'pyramid') return `峰值 ${n}m`
  return ''
}

// ── 自主訓練：TrainingScreen → /track 的橋接（無伺服器端「進行中挑戰」狀態，靠 sessionStorage 帶一次）──
const FREETRAIN_LS_KEY = 'dor_freetrain_wo'
export function saveFreetrainWorkout(code: string, name: string, segments: WorkoutSegment[]) {
  try { sessionStorage.setItem(FREETRAIN_LS_KEY, JSON.stringify({ code, name, segments })) } catch { /* ignore */ }
}
// 取出並清除（只消費一次，避免重新整理 /track 又重新帶入一份舊課表）
export function takeFreetrainWorkout(): { code: string; name: string; segments: WorkoutSegment[] } | null {
  try {
    const raw = sessionStorage.getItem(FREETRAIN_LS_KEY)
    if (!raw) return null
    sessionStorage.removeItem(FREETRAIN_LS_KEY)
    const data = JSON.parse(raw)
    if (!data?.segments?.length) return null
    return data
  } catch { return null }
}
