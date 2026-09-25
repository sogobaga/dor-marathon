// 「進行中跑步」狀態持久化（見 CONTRACT.md §2.1）——純函式，全部 try/catch（localStorage 可能不可用：
// 隱私模式、儲存空間已滿、SSR）。與 track 頁的 LS_KEY('dor_gps_run'，存 GPS 點本身) 是兩把不同的 key，
// 各自獨立讀寫；dor_gps_run 的格式/寫入時機維持既有（見 pendingGps.ts），本檔只管「這趟還在跑」這件事
// 以及重開頁面後要拿回哪些數字才能無縫接續（不重算距離：距離本身在 commitSeg 當下已經算好，直接存快照，
// 避免另外維護一份重算邏輯與 track/page.tsx 的即時累積邏輯分岔、產生兩套數字對不上的風險）。

export const ACTIVE_RUN_KEY = 'dor_gps_active'
export const ACTIVE_RUN_STALE_MS = 2 * 60 * 60 * 1000 // 2 小時：超過才彈三選一，見契約 §2.2

// 進行中課表/挑戰快照（個人任務／城市探索關主／自主訓練共用；型別特意用 unknown 承接 steps，
// 避免這個檔案依賴 lib/workout.ts 造成不必要的耦合，track 頁還原時自行 cast 回 WoStep[]）。
export interface ActiveRunWorkoutSnapshot {
  taskId: string
  title: string
  steps: unknown
  kind: 'personal' | 'explore' | 'freetrain'
  cardUrl?: string
  freerunSec?: number
}

export interface ActiveRunState {
  v: 1
  startedAt: number // ms（=track 頁 startRef，等同 dor_gps_run 的 start）
  href: string // 開跑當下完整 /track 網址（含 query，如 ?strategy=… /?taskId=…），供全站導回與三選一彈窗使用
  lastSeenAt: number // ms：最後一次頁面確定還活著的時間（每採納點/心跳/hidden 都會更新）
  movingAccumS: number // 移動時間累積秒數（lib/movingTime.ts MovingState.movingAccumS 快照）
  distanceM: number // 有效距離快照（已套用 GPS 校正係數，等同 distRef.current）
  rawDistanceM: number // 原始距離快照（供疑似搭車偵測 rawDistRef.current 還原）
  excludedSegs: number
  excludedKm: number
  splits: number[] // 每公里配速（秒），鏡射 splits state
  splitMarks: number[] // 每跨公里的 elapsed 秒，鏡射 splitMarkRef
  movingSplitMarks: number[] // 每跨公里的移動時間快照，鏡射 movingSplitMarkRef
  calibK: number // GPS 距離校正係數快照（=calibKRef.current）
  // ⚠️ 以下兩欄刻意保留在型別但目前無人寫入（track/page.tsx 全檔搜尋不到任何 writeActiveRun 呼叫
  // 帶入這兩個欄位）：這兩個值改由 href 的 query string（?strategy=…／?focus=…）攜帶還原——重開頁面時
  // 既有的對應 effect 會自行依 URL 重新載入，不需要在這裡另存一份、也不會有兩套來源對不上的風險（見
  // track/page.tsx resumeActiveRun() 內對應註解）。讀到 undefined 是正常狀態，不代表遺漏寫入；若之後
  // 真的需要在別處單獨讀取這兩個值（不透過 href），才需要補上實際的 writeActiveRun 呼叫。
  raceStrategyId?: string // ?strategy=<id> 帶入的比賽策略 id（見上方說明：目前不由此欄位還原）
  focusBoss?: string | null // 「前往打卡」帶來的目標關主 id（見上方說明：目前不由此欄位還原）
  workout?: ActiveRunWorkoutSnapshot | null
  woPhase?: 'idle' | 'countdown' | 'running' | 'done'
  woStepIdx?: number
  // 口袋模式併入專注模式（2026-09-25）：專注模式目前是否開啟（RaceFocusMode 的 hidden 取反），切入/退出
  // 時由該元件的 onOpenChange 寫入。重開頁面自動接續（resumeActiveRun）時若讀到 true → 專注模式直接
  // 開啟（未鎖定，開啟後一樣走 10 秒無觸控自動上鎖），維持接續前的使用情境；讀到顯式 false → 尊重使用者
  // 上次手動退出的選擇，同樣直接還原為關閉（不會被 strategy 蓋回自動開啟）；只有 undefined（從未寫入，
  // 例如舊資料或尚未進出過專注模式）才交回 RaceFocusMode 既有預設規則（有 strategy 才自動開啟）。見
  // RaceFocusMode.tsx `initialOpen !== undefined ? !initialOpen : !strategy`。
  focusOpen?: boolean
}

function isValid(data: unknown): data is ActiveRunState {
  if (!data || typeof data !== 'object') return false
  const d = data as Record<string, unknown>
  return d.v === 1 && typeof d.startedAt === 'number' && typeof d.href === 'string' && typeof d.lastSeenAt === 'number'
}

export function readActiveRun(): ActiveRunState | null {
  try {
    const raw = localStorage.getItem(ACTIVE_RUN_KEY)
    if (!raw) return null
    const data = JSON.parse(raw)
    return isValid(data) ? data : null
  } catch {
    return null
  }
}

// 與既有值合併寫入（呼叫端只需傳有變動的欄位）；第一次寫入（尚無既有值）時 startedAt/href 必須由呼叫端帶入。
export function writeActiveRun(patch: Partial<ActiveRunState>): void {
  try {
    const prev = readActiveRun()
    const next: ActiveRunState = {
      v: 1,
      startedAt: patch.startedAt ?? prev?.startedAt ?? Date.now(),
      href: patch.href ?? prev?.href ?? '',
      lastSeenAt: patch.lastSeenAt ?? Date.now(),
      movingAccumS: patch.movingAccumS ?? prev?.movingAccumS ?? 0,
      distanceM: patch.distanceM ?? prev?.distanceM ?? 0,
      rawDistanceM: patch.rawDistanceM ?? prev?.rawDistanceM ?? 0,
      excludedSegs: patch.excludedSegs ?? prev?.excludedSegs ?? 0,
      excludedKm: patch.excludedKm ?? prev?.excludedKm ?? 0,
      splits: patch.splits ?? prev?.splits ?? [],
      splitMarks: patch.splitMarks ?? prev?.splitMarks ?? [],
      movingSplitMarks: patch.movingSplitMarks ?? prev?.movingSplitMarks ?? [],
      calibK: patch.calibK ?? prev?.calibK ?? 1,
      raceStrategyId: patch.raceStrategyId !== undefined ? patch.raceStrategyId : prev?.raceStrategyId,
      focusBoss: patch.focusBoss !== undefined ? patch.focusBoss : prev?.focusBoss,
      workout: patch.workout !== undefined ? patch.workout : prev?.workout,
      woPhase: patch.woPhase !== undefined ? patch.woPhase : prev?.woPhase,
      woStepIdx: patch.woStepIdx !== undefined ? patch.woStepIdx : prev?.woStepIdx,
      focusOpen: patch.focusOpen !== undefined ? patch.focusOpen : prev?.focusOpen,
    }
    localStorage.setItem(ACTIVE_RUN_KEY, JSON.stringify(next))
  } catch {
    /* ignore：localStorage 可能不可用（隱私模式／容量已滿） */
  }
}

// 心跳／hidden／每個採納點共用：只更新「還活著」時間戳＋目前累積移動秒數，其餘欄位維持既有值。
export function touchActiveRun(movingAccumS: number): void {
  writeActiveRun({ lastSeenAt: Date.now(), movingAccumS })
}

export function clearActiveRun(): void {
  try {
    localStorage.removeItem(ACTIVE_RUN_KEY)
  } catch {
    /* ignore */
  }
}

export function activeRunAgeMs(state: ActiveRunState, now: number = Date.now()): number {
  return Math.max(0, now - state.lastSeenAt)
}

export function isActiveRunFresh(state: ActiveRunState, now: number = Date.now()): boolean {
  return activeRunAgeMs(state, now) <= ACTIVE_RUN_STALE_MS
}
