// GPS 移動時間狀態機（純函式，無副作用）——供 track/page.tsx 的「移動時間」計算使用。
//
// 背景／要修的兩個症狀（詳見交付前的唯讀探查）：
// ① 顯示不平滑：「移動時間」原本只在 GPS onPos 回呼命中距離累積門檻時才前進（watchPosition 觸發
//    頻率不定，可能好幾秒才一次），畫面因此一段一段跳，不像「全程時間」用固定 250ms interval 那樣平滑。
// ② 判定過寬鬆：「移動」判定原本與距離累積共用同一個門檻（JITTER_MIN=6m，且無最小速度下限），
//    GPS 精度稍差時原地不動也常被判定為「移動」，休息/等紅燈時移動時間仍持續增加。
//
// 這個檔案只解決「移動/靜止狀態判定」本身，是一個獨立於 page.tsx 距離累積邏輯的純函式狀態機：
//   - 完全不觸碰 page.tsx 既有的 distance 累積（JITTER_MIN/MAX_SPEED/MAX_ACC 用於距離的邏輯不變、
//     不共用同一組門檻——這正是本次要修掉的耦合）。
//   - 顯示平滑改由呼叫端（page.tsx）額外用本地 250ms tick（比 1Hz 更細）疊加 movingSince 差值達成，
//     這個檔案只負責「現在算不算在移動」以及「已經確定累積了多少移動秒數」，不管畫面多久刷新一次。
//
// 狀態模型（MovingState）：
//   - movingAccumS：已經「結清」的移動秒數（一段移動結束、確定切回靜止時，把該段時長併入這裡）。
//   - movingSince：目前「移動中」這一段的起始時間戳（ms, epoch）；null 表示目前判定為靜止/暫停。
//   - 顯示值 = movingAccumS + (movingSince != null ? (nowMs - movingSince) / 1000 : 0)
//     由呼叫端在任意時機（GPS 回呼、或本地 tick）重算，見 currentMovingS()。
//
// 遲滯（hysteresis，HYSTERESIS_N=2）：
//   - 單筆 GPS 訊號常有飄移雜訊，若「移動⇄靜止」逐點切換，短暫停下時仍會忽快忽慢地累加/停止。
//   - 連續 2 筆「靜止訊號」才真正切成暫停（movingSince 結清進 movingAccumS）；
//     連續 2 筆「移動訊號」才真正切回移動（開新 movingSince）。
//   - 單筆飄移尖峰只會讓對向的連續計數中斷歸零，不會誤觸切換；短暫等紅燈能正確被判定為暫停。
//
// 邊界：
//   - 恢復移動時「不回溯補秒」：新 movingSince 就是「確認移動的當下」，暫停期間的秒數永遠不計入。
//   - GPS 斷流（例如切背景，watchPosition 回呼可能被系統整段暫停）：這個純函式狀態機本身「不呼叫
//     就不變」，呼叫端在偵測到即將斷流時應呼叫 flushMovingState() 主動把目前這段結清並清空
//     movingSince，讓「斷流期間」不被計入移動時間、也不需要恢復時特別處理；重接後由新 GPS 訊號
//     （再次呼叫 advanceMovingState）重新判定要不要開新的移動段。

export interface MovePoint {
  lat: number
  lng: number
  t: number // epoch ms（GPS 定位時間戳，pos.timestamp）
  acc: number // 定位精度（公尺）
}

export interface MovingState {
  movingAccumS: number // 已結清的移動秒數
  movingSince: number | null // 目前移動段起點時間戳（ms），null=目前判定為靜止/暫停
  stillStreak: number // 連續「靜止訊號」筆數（未達遲滯門檻前的中間計數）
  moveStreak: number // 連續「移動訊號」筆數（未達遲滯門檻前的中間計數）
}

export function initMovingState(): MovingState {
  return { movingAccumS: 0, movingSince: null, stillStreak: 0, moveStreak: 0 }
}

const MOVE_SPEED_MIN = 0.6 // m/s：連續兩點瞬時速度達此值才算「移動訊號」的必要條件之一
const MOVE_DIST_MIN_BASE = 2.5 // 公尺：移動訊號的最小位移下限（另有 0.3×accuracy 動態下限，取較大者）
export const HYSTERESIS_N = 2 // 連續 N 筆同類訊號才切換確定狀態，過濾單筆飄移尖峰

// v0.1.587 上線後回報「移動時間不會前進」的根因（已用單元測試驗證，見 movingTime.test.ts 開頭的根因情境）：
// 手機 watchPosition 在戶外常態以「約 1Hz」回報，跑步 3m/s 時單次回呼之間的位移只有約 3m；舊門檻
// max(3m, 0.5×acc) 在精度 20m 時＝10m，遠大於單筆 3m 位移，導致「正常跑步」的每一筆訊號都被判定為
// still、movingSince 永遠是 null、移動時間卡在 0。（另一個候選根因——精度 >30m 的點被完全排除在判定
// 之外——查證後不成立：page.tsx 實際餵給這個狀態機的精度門檻是 goodAcc=acc<=65，與距離累積共用，
// 30m 只是這裡曾經想像的預設值、從未被 page.tsx 呼叫，是死碼，已移除。）
// 修法：呼叫端（page.tsx）改成「距上一個判定基準點 dt<2.5 秒的點不判定、也不推進基準點」（見
// MOVE_JUDGE_WINDOW_S），讓進來的位移是 ≥2.5 秒的累積量，飄移在長視窗下的等效速度會被攤薄、真實移動
// 的位移量則足以穿過門檻；同時把位移門檻放寬到 max(2.5m, 0.3×accuracy)（原 3m/0.5×accuracy 對長視窗
// 位移仍偏嚴）。
export const MOVE_JUDGE_WINDOW_S = 2.5 // 秒：lastMoveRef 判定基準點的最小視窗，見上方說明

/** 精度過濾：沿用頁內既有精度門檻慣例（呼叫端傳入頁面既有的 MAX_ACC）；未帶入時用 30m 保守值。 */
export function isAccurateEnough(acc: number, maxAcc = 30): boolean {
  return acc === 0 || acc <= maxAcc
}

/**
 * 判定「上一個判定基準點→本點」是移動訊號、靜止訊號、或不可判定（dt<=0，例如亂序/重複時間戳，
 * 呼叫端應略過）。與距離累積門檻（page.tsx 的 JITTER_MIN=6m）完全獨立：這裡的位移下限取
 * max(2.5m, 0.3×accuracy)，accuracy 越差，需要越大的位移才敢判定為「真的在移動」（否則可能只是定位
 * 漂移，不是真的移動）。呼叫端應先套用 MOVE_JUDGE_WINDOW_S 篩選過 prev/cur 的時間間距（見上方常數
 * 說明），讓 distM 是足夠長視窗下的累積位移，而不是單次 GPS 回呼之間的瞬時位移——這是本檔案修掉
 * 「移動時間卡住不動」的關鍵前提，這個函式本身不重複檢查視窗長度。
 * distM 由呼叫端算好傳入（沿用頁面既有的 haversineM，避免這個檔案重複依賴地理計算的實作）。
 */
export function classifyMoveSignal(prev: MovePoint, cur: MovePoint, distM: number): 'moving' | 'still' | null {
  const dt = (cur.t - prev.t) / 1000
  if (!(dt > 0)) return null
  const speed = distM / dt
  const distMin = Math.max(MOVE_DIST_MIN_BASE, 0.3 * cur.acc)
  return speed >= MOVE_SPEED_MIN && distM > distMin ? 'moving' : 'still'
}

/**
 * 推進狀態機一步（收到一筆已分類的訊號）。不修改傳入的 state，回傳新狀態，供純函式呼叫/單元測試使用。
 * nowMs：本次訊號發生的時間戳。呼叫端一律傳 Date.now()（而非 GPS 點的 t），
 *        與 page.tsx「全程時間」(elapsed = Date.now()-startRef) 用同一個牆鐘時間基準，
 *        兩個顯示數字才會在同一把尺上前進，避免時鐘來源不一致造成的細微落差。
 */
export function advanceMovingState(state: MovingState, signal: 'moving' | 'still', nowMs: number): MovingState {
  if (signal === 'moving') {
    const moveStreak = state.moveStreak + 1
    if (state.movingSince == null && moveStreak >= HYSTERESIS_N) {
      // 連續達門檻筆數的移動訊號 → 確認切回移動：開新一段，起點就是「確認的當下」，不回溯補秒
      return { movingAccumS: state.movingAccumS, movingSince: nowMs, stillStreak: 0, moveStreak }
    }
    return { ...state, stillStreak: 0, moveStreak } // 訊號類別改變 → 中斷對向（靜止）的連續計數
  } else {
    const stillStreak = state.stillStreak + 1
    if (state.movingSince != null && stillStreak >= HYSTERESIS_N) {
      // 連續達門檻筆數的靜止訊號 → 確認切成暫停：把目前這段結清進 accum
      const seg = Math.max(0, (nowMs - state.movingSince) / 1000)
      return { movingAccumS: state.movingAccumS + seg, movingSince: null, stillStreak, moveStreak: 0 }
    }
    return { ...state, stillStreak, moveStreak: 0 }
  }
}

/**
 * 強制把「目前這段（若正在移動）」立即結清進 movingAccumS，並重置遲滯計數，繞過 advanceMovingState
 * 的連續 2 筆遲滯門檻。用途：呼叫端已知即將沒有 GPS 訊號了（例如切到背景），要主動凍結進度，
 * 避免斷流期間的牆鐘時間被之後某一筆遲來的訊號整段誤算成移動時間。
 * 與 advanceMovingState 是兩條不同路徑：這裡是「外部事件通知即將斷流」，不是「收到一筆判定訊號」。
 */
export function flushMovingState(state: MovingState, nowMs: number): MovingState {
  if (state.movingSince == null) return state
  const seg = Math.max(0, (nowMs - state.movingSince) / 1000)
  return { movingAccumS: state.movingAccumS + seg, movingSince: null, stillStreak: 0, moveStreak: 0 }
}

/** 顯示值：呼叫端在任意時機（GPS 回呼、或本地 tick）用目前時間重算；movingSince 存在時才疊加差值。 */
export function currentMovingS(state: MovingState, nowMs: number): number {
  return state.movingAccumS + (state.movingSince != null ? Math.max(0, (nowMs - state.movingSince) / 1000) : 0)
}
