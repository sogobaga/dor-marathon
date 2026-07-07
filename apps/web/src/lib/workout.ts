import type { WorkoutSegment } from './api'

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

// 分段課表摘要：「暖身 2K → 間歇 400m ×6 → 緩和 2K」
export function segSummary(segs?: WorkoutSegment[] | null): string {
  if (!segs || !segs.length) return ''
  return segs.map((s) => {
    const d = s.target_type === 'distance' ? (s.target >= 1000 ? `${s.target / 1000}K` : `${s.target}m`) : `${Math.round(s.target / 60) || Math.round(s.target)}${s.target >= 60 ? '分' : '秒'}`
    const reps = s.reps && s.reps > 1 ? ` ×${s.reps}` : ''
    return `${s.label || kindLabel(s.kind)} ${d}${reps}`
  }).join(' → ')
}

// 總距離（公里）：所有距離型分段 × 組數加總
export function totalKm(segs?: WorkoutSegment[] | null): number {
  if (!segs) return 0
  let m = 0
  for (const s of segs) { const reps = s.reps && s.reps > 1 ? s.reps : 1; if (s.target_type === 'distance') m += s.target * reps }
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
