import type { RaceStrategy } from './api'
import type { WoStep } from './workout'

// 專注模式進度條＋每公里鼓勵語（v1.1.663 前台）共用的目標判定與格式化，全部純函式、不碰 React state，
// 方便單元測試（見 apps/web/scripts/verify-run-goal.mjs）。呼叫端（track/page.tsx）算好 strategy/workout
// 傳進來，這裡只讀不算 GPS/課表本身的邏輯。

export type RunGoal =
  | { type: 'distance'; totalM: number }
  | { type: 'time'; totalS: number }
  | { type: 'none' }

// resolveRunGoal 第二參數：只取用得到的欄位子集（避免依賴 track/page.tsx 完整 workout state 型別）。
export interface RunGoalWorkout {
  kind: 'personal' | 'explore' | 'freetrain'
  steps: WoStep[]
  freerunSec?: number
}

// 本次跑步的目標判定，優先序：
//   1) 有賽事策略（total_km>0）→ distance，總量＝策略總距離
//   2) 自主訓練 Free Run（只設時間、不控配速/距離）→ time，總量＝freerunSec
//   3) 結構化課表：全部分段同為 distance → distance（Σtarget）；全部同為 time → time（Σtarget）；
//      混合（暖身距離+主課時間等）→ 沒有單一可換算的目標，回 none
//   4) 其餘（一般跑步、無 workout）→ none
export function resolveRunGoal(
  strategy: RaceStrategy | null | undefined,
  workout: RunGoalWorkout | null | undefined,
): RunGoal {
  if (strategy && strategy.total_km > 0) return { type: 'distance', totalM: strategy.total_km * 1000 }
  if (workout?.kind === 'freetrain' && workout.freerunSec && workout.freerunSec > 0) {
    return { type: 'time', totalS: workout.freerunSec }
  }
  if (workout?.steps?.length) {
    const allDistance = workout.steps.every((s) => s.targetType === 'distance')
    const allTime = workout.steps.every((s) => s.targetType === 'time')
    if (allDistance) return { type: 'distance', totalM: workout.steps.reduce((sum, s) => sum + s.target, 0) }
    if (allTime) return { type: 'time', totalS: workout.steps.reduce((sum, s) => sum + s.target, 0) }
  }
  return { type: 'none' }
}

// 公里數文字：整數不帶小數（"10"），否則四捨五入到小數第 1 位（"21.1"）——呼叫端自行接上單位（"km"）。
export function fmtKm(km: number): string {
  const v = Math.round(Math.max(0, km) * 10) / 10
  return Number.isInteger(v) ? String(v) : v.toFixed(1)
}

export type CheerPhase = 'before' | 'after'
export interface CheerInfo {
  phase: CheerPhase
  remainText: string // distance→"7 km"／"21.1 km"；time→"38 分鐘"；none→''
}

// 每公里鼓勵語的 phase/remain 判定：km＝剛跨過的整公里數（1-based），elapsedS＝同一時刻的已耗秒數
// （呼叫端須與 totalS 同一把尺，見 track/page.tsx commitSeg 內的 el 變數）。
//   phase：達成度 ratio ≥ 50% 且尚有剩餘（remain>0）→ 'after'（剩餘式文案）；否則 'before'（累積式文案）。
//   none 目標（無單一目標）一律 before，也沒有 remain 可算；超過目標後 remain≤0 同樣落回 before（累積式）。
export function cheerPhaseAndRemain(goal: RunGoal, km: number, elapsedS: number): CheerInfo {
  if (goal.type === 'none') return { phase: 'before', remainText: '' }

  let ratio: number
  let remain: number
  let remainText: string
  if (goal.type === 'distance') {
    ratio = goal.totalM > 0 ? (km * 1000) / goal.totalM : 0
    remain = goal.totalM - km * 1000
    remainText = `${fmtKm(remain / 1000)} km`
  } else {
    ratio = goal.totalS > 0 ? elapsedS / goal.totalS : 0
    remain = goal.totalS - elapsedS
    remainText = `${Math.ceil(Math.max(0, remain) / 60)} 分鐘`
  }
  const phase: CheerPhase = ratio >= 0.5 && remain > 0 ? 'after' : 'before'
  return { phase, remainText }
}
