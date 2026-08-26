// 針對 lib/movingTime.ts 狀態機的可獨立驗算單元測試。
//
// apps/web 目前沒有安裝任何測試框架（package.json 無 jest/vitest/testing-library），
// 因此狀態機刻意寫成不依賴任何框架的純函式（見 movingTime.ts），這個檔案改用 Node 內建
// assert 模組直接驗算，不依賴 describe/it/expect 等測試框架 API。
//
// 執行方式（本機已驗證可行，Node 24 內建 TypeScript type-stripping，無需額外安裝 ts-node/tsx）：
//   node --experimental-strip-types src/lib/movingTime.test.ts
// 或先用專案既有的 tsc 編譯再跑純 JS：
//   npx tsc src/lib/movingTime.ts src/lib/movingTime.test.ts --module commonjs --target es2020 \
//     --outDir <tmp> --skipLibCheck && node <tmp>/movingTime.test.js

import assert from 'node:assert/strict'
import {
  initMovingState, advanceMovingState, classifyMoveSignal, currentMovingS, flushMovingState, isAccurateEnough,
  MOVE_JUDGE_WINDOW_S, type MovingState, type MovePoint,
} from './movingTime'

let passed = 0
function check(name: string, fn: () => void) {
  fn()
  passed++
  console.log(`ok - ${name}`)
}

// ---- classifyMoveSignal ----

check('classifyMoveSignal: 明顯位移(足夠速度+足夠距離) → moving', () => {
  const prev: MovePoint = { lat: 0, lng: 0, t: 0, acc: 5 }
  const cur: MovePoint = { lat: 0, lng: 0, t: 5000, acc: 5 } // 5 秒
  // 5 秒走 5m = 1 m/s ≥ 0.6，且 5m > max(2.5, 0.3*5=1.5)=2.5
  assert.equal(classifyMoveSignal(prev, cur, 5), 'moving')
})

check('classifyMoveSignal: 原地小幅飄移(精度差時的常見漂移) → still', () => {
  const prev: MovePoint = { lat: 0, lng: 0, t: 0, acc: 20 }
  const cur: MovePoint = { lat: 0, lng: 0, t: 5000, acc: 20 } // 5 秒
  // 5 秒飄 5m：速度 1 m/s ≥ 0.6，但位移下限 max(2.5, 0.3*20=6)=6 > 5 → 未過門檻 → still
  assert.equal(classifyMoveSignal(prev, cur, 5), 'still')
})

// ---- classifyMoveSignal: v0.1.587 之後「移動時間卡住不動」根因回歸測試 ----
// 根因：手機 GPS 約 1Hz 回報，跑步 3m/s 時單次回呼間位移只有約 3m；舊門檻 max(3m,0.5×acc) 在精度 20m 時
// ＝10m，遠大於單筆 3m 位移 → 每一筆訊號都被判定為 still，movingSince 永遠是 null，移動時間卡在 0。
// 新設計要求呼叫端把判定基準點的視窗拉長到 MOVE_JUDGE_WINDOW_S(2.5s) 以上再判定，這裡直接驗證「用規格
// 給的邊界範例」(3m/s × 2.5s = 7.5m、精度 20m) 確實能穿過新門檻，証實候選①已解決。
check('classifyMoveSignal: 根因回歸——1Hz 單筆 3m 位移仍判 still（舊門檻的症狀在新門檻下依然如此，凸顯必須拉長視窗才行，不能只改門檻數字）', () => {
  const prev: MovePoint = { lat: 0, lng: 0, t: 0, acc: 20 }
  const cur: MovePoint = { lat: 0, lng: 0, t: 1000, acc: 20 } // 1 秒（1Hz 單次回呼間隔）走 3m（3 m/s 跑步）
  // 位移下限 max(2.5, 0.3*20=6)=6 > 3m → 單筆 1 秒判定仍然是 still：新門檻本身不夠，必須靠呼叫端拉長視窗
  assert.equal(classifyMoveSignal(prev, cur, 3), 'still')
})
check('classifyMoveSignal: 根因回歸——把同樣的 3m/s 位移累積到 2.5 秒視窗(規格邊界範例 7.5m) → moving', () => {
  const prev: MovePoint = { lat: 0, lng: 0, t: 0, acc: 20 }
  const cur: MovePoint = { lat: 0, lng: 0, t: 2500, acc: 20 } // 2.5 秒（MOVE_JUDGE_WINDOW_S）累積走 7.5m（3 m/s）
  assert.equal(MOVE_JUDGE_WINDOW_S, 2.5)
  // 位移下限 max(2.5, 0.3*20=6)=6，7.5m > 6 → moving：同樣的移動速度，視窗拉長後就能正確判定
  assert.equal(classifyMoveSignal(prev, cur, 7.5), 'moving')
})

check('classifyMoveSignal: 速度不足(位移夠但時間拉很長) → still', () => {
  const prev: MovePoint = { lat: 0, lng: 0, t: 0, acc: 5 }
  const cur: MovePoint = { lat: 0, lng: 0, t: 60000, acc: 5 } // 60 秒走 10m = 0.167 m/s
  assert.equal(classifyMoveSignal(prev, cur, 10), 'still')
})

check('classifyMoveSignal: dt<=0（亂序/重複時間戳）→ null，呼叫端應略過', () => {
  const prev: MovePoint = { lat: 0, lng: 0, t: 5000, acc: 5 }
  const cur: MovePoint = { lat: 0, lng: 0, t: 5000, acc: 5 }
  assert.equal(classifyMoveSignal(prev, cur, 10), null)
  const curEarlier: MovePoint = { lat: 0, lng: 0, t: 4000, acc: 5 }
  assert.equal(classifyMoveSignal(prev, curEarlier, 10), null)
})

// ---- isAccurateEnough ----

check('isAccurateEnough: 沿用頁內既有慣例（呼叫端傳 MAX_ACC=65）', () => {
  assert.equal(isAccurateEnough(0, 65), true) // acc===0 視為特例：良好
  assert.equal(isAccurateEnough(65, 65), true)
  assert.equal(isAccurateEnough(66, 65), false)
})

check('isAccurateEnough: 未帶入 maxAcc 時預設 30m', () => {
  assert.equal(isAccurateEnough(30), true)
  assert.equal(isAccurateEnough(31), false)
})

// ---- advanceMovingState: 遲滯（hysteresis） ----

check('advanceMovingState: 單筆移動訊號不足以啟動移動段（需連續 2 筆）', () => {
  let s = initMovingState()
  s = advanceMovingState(s, 'moving', 1000)
  assert.equal(s.movingSince, null, '單筆訊號不應啟動')
  assert.equal(s.moveStreak, 1)
})

check('advanceMovingState: 連續 2 筆移動訊號才開新 movingSince（起點=確認當下，不回溯）', () => {
  let s = initMovingState()
  s = advanceMovingState(s, 'moving', 1000)
  s = advanceMovingState(s, 'moving', 2000)
  assert.equal(s.movingSince, 2000, 'movingSince 應是第 2 筆確認訊號當下的時間，而非第 1 筆')
  assert.equal(s.movingAccumS, 0)
})

check('advanceMovingState: 單筆飄移尖峰不會誤觸恢復（移動中插入 1 筆 still 不會暫停）', () => {
  let s: MovingState = initMovingState()
  s = advanceMovingState(s, 'moving', 1000)
  s = advanceMovingState(s, 'moving', 2000) // 進入移動，since=2000
  s = advanceMovingState(s, 'still', 3000) // 單筆飄移尖峰
  assert.notEqual(s.movingSince, null, '單筆 still 不應中斷移動段')
  assert.equal(s.movingSince, 2000)
  assert.equal(s.stillStreak, 1)
  s = advanceMovingState(s, 'moving', 4000) // 訊號轉回移動 → 對向計數歸零
  assert.equal(s.stillStreak, 0)
  assert.equal(s.movingSince, 2000, '仍是同一段移動，未被中間那筆飄移打斷')
})

check('advanceMovingState: 連續 2 筆靜止訊號才切成暫停，並把該段時長結清進 accum', () => {
  let s: MovingState = initMovingState()
  s = advanceMovingState(s, 'moving', 0)
  s = advanceMovingState(s, 'moving', 1000) // 進入移動，since=1000
  s = advanceMovingState(s, 'still', 5000) // 第 1 筆靜止：還在移動段
  assert.notEqual(s.movingSince, null)
  s = advanceMovingState(s, 'still', 6000) // 第 2 筆靜止：確認暫停
  assert.equal(s.movingSince, null)
  assert.equal(s.movingAccumS, 5, '結清 since(1000) 到第 2 筆確認靜止時間(6000) = 5 秒')
})

check('advanceMovingState: 恢復移動不回溯補秒——暫停期間永遠不計入', () => {
  let s: MovingState = initMovingState()
  s = advanceMovingState(s, 'moving', 0)
  s = advanceMovingState(s, 'moving', 1000) // since=1000
  s = advanceMovingState(s, 'still', 2000)
  s = advanceMovingState(s, 'still', 3000) // 暫停確認，accum=2(1000→3000)
  assert.equal(s.movingAccumS, 2)
  // 暫停很久之後才又開始動（例如休息 100 秒）
  s = advanceMovingState(s, 'moving', 103000)
  s = advanceMovingState(s, 'moving', 104000) // 第 2 筆移動確認，新段從 104000 起算
  assert.equal(s.movingSince, 104000, '新段起點是確認當下，暫停的 100 秒不會被補進 since')
  assert.equal(s.movingAccumS, 2, '暫停期間不併入 accum，只有結清時的那一段才算')
})

// ---- flushMovingState: 主動結清（切背景等外部事件用） ----

check('flushMovingState: 移動中被強制結清 → 立即凍結，不需遲滯', () => {
  let s: MovingState = initMovingState()
  s = advanceMovingState(s, 'moving', 0)
  s = advanceMovingState(s, 'moving', 1000) // since=1000
  const flushed = flushMovingState(s, 4000)
  assert.equal(flushed.movingSince, null)
  assert.equal(flushed.movingAccumS, 3)
})

check('flushMovingState: 已是靜止狀態時為 no-op', () => {
  const s = initMovingState()
  const flushed = flushMovingState(s, 9999)
  assert.deepEqual(flushed, s)
})

check('flushMovingState 模擬「切背景」情境：斷流期間不計入、重接後由新訊號重新判定', () => {
  let s: MovingState = initMovingState()
  s = advanceMovingState(s, 'moving', 0)
  s = advanceMovingState(s, 'moving', 1000) // 移動中，since=1000
  // t=2000：切背景，主動結清（模擬 visibilitychange → hidden）
  s = flushMovingState(s, 2000)
  assert.equal(currentMovingS(s, 2000), 1, '結清當下 accum=1')
  // 斷流 300 秒（背景中沒有任何 onPos 呼叫，狀態機完全不變）
  // 重接後，第一筆新訊號距離很近（真的沒動）→ still，不影響已凍結的 accum
  s = advanceMovingState(s, 'still', 302000)
  assert.equal(currentMovingS(s, 302000), 1, '斷流的 300 秒不會被算成移動時間')
  // 接著使用者真的開始跑 → 連續 2 筆 moving 才恢復
  s = advanceMovingState(s, 'moving', 303000)
  s = advanceMovingState(s, 'moving', 304000)
  assert.equal(s.movingSince, 304000)
  assert.equal(currentMovingS(s, 304000), 1, '恢復瞬間 accum 仍是 1，不含斷流期間')
})

// ---- currentMovingS ----

check('currentMovingS: 靜止時=accum；移動中=accum+距今差值', () => {
  const stillS: MovingState = { movingAccumS: 42, movingSince: null, stillStreak: 0, moveStreak: 0 }
  assert.equal(currentMovingS(stillS, 999999), 42)
  const movingS: MovingState = { movingAccumS: 10, movingSince: 5000, stillStreak: 0, moveStreak: 0 }
  assert.equal(currentMovingS(movingS, 8000), 13) // 10 + (8000-5000)/1000
})

// ---- 實戰情境模擬：驗證任務A修復（判定視窗拉長）在端到端場景下的行為 ----
// 這裡的 simulateTrack() 是 track/page.tsx onPos 內「lastMoveRef 判定基準點」邏輯的最小重現（只重現移動
// 狀態機這條路徑本身，不含 page.tsx 另外疊加的「距離累積閘門補強訊號」——那條路徑耦合了 page.tsx 的
// JITTER_MIN/MAX_SPEED，不屬於這個純函式模組，留給實機/整合驗證），用來證實 classifyMoveSignal +
// advanceMovingState + MOVE_JUDGE_WINDOW_S 這三者組合起來，在真實的 GPS 取樣節奏下確實解掉「移動時間卡
// 住不動」的根因。座標點用 haversine 產生（與 page.tsx 的 haversineM 等價公式），固定種子 PRNG 確保可重現。

function haversineM(a: MovePoint, b: MovePoint): number {
  const R = 6371000, toRad = (d: number) => (d * Math.PI) / 180
  const dLat = toRad(b.lat - a.lat), dLng = toRad(b.lng - a.lng)
  const la1 = toRad(a.lat), la2 = toRad(b.lat)
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(la1) * Math.cos(la2) * Math.sin(dLng / 2) ** 2
  return 2 * R * Math.asin(Math.sqrt(h))
}
// mulberry32：小型固定種子 PRNG，只用於產生可重現的測試座標點，非密碼學用途。
function mulberry32(seed: number): () => number {
  return function () {
    seed |= 0; seed = (seed + 0x6d2b79f5) | 0
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}
const LAT0 = 25.03
const COS_LAT0 = Math.cos((LAT0 * Math.PI) / 180)
// 用「沿東西向、位移量(公尺)」直接產生座標點，避免測試還要重算等效速度——cumM 是沿路徑的累積公尺數。
function ptAt(cumM: number, t: number, acc: number): MovePoint {
  return { lat: LAT0, lng: 121.5 + cumM / (111320 * COS_LAT0), t, acc }
}
// 重現 page.tsx onPos 內「lastMoveRef 判定基準點」的視窗邏輯：距上一個判定基準點 dt < MOVE_JUDGE_WINDOW_S
// 的點不判定、也不推進基準點；只有真的做了判定才推進基準點。回傳最終狀態＋每次判定的軌跡（供除錯用）。
function simulateTrack(points: MovePoint[]): { state: MovingState; log: Array<{ t: number; signal: 'moving' | 'still' }> } {
  let state = initMovingState()
  let basis: MovePoint | null = null
  const log: Array<{ t: number; signal: 'moving' | 'still' }> = []
  for (const p of points) {
    if (!basis) { basis = p; continue }
    if ((p.t - basis.t) / 1000 >= MOVE_JUDGE_WINDOW_S) {
      const signal = classifyMoveSignal(basis, p, haversineM(basis, p))
      if (signal) { state = advanceMovingState(state, signal, p.t); log.push({ t: p.t, signal }) }
      basis = p
    }
  }
  return { state, log }
}

check('實戰情境①：1Hz、精度輪流5/15/25m、速度2.5-3.5m/s 連續60筆真實跑步 → 移動時間應累積掉「絕大部分」時間（修復前是卡在 0）', () => {
  const rng = mulberry32(42)
  const accCycle = [5, 15, 25]
  let cum = 0
  const points: MovePoint[] = []
  for (let i = 0; i < 60; i++) {
    if (i > 0) cum += 2.5 + rng() * 1.0 // 每秒前進 2.5-3.5m（隨機速度，固定種子可重現）
    points.push(ptAt(cum, i * 1000, accCycle[i % 3]))
  }
  const { state, log } = simulateTrack(points)
  const finalS = currentMovingS(state, points[points.length - 1].t)
  // 精確可重現值（種子42）＝53s：60 筆跨 59 秒，只在「一開始」損耗約 6 秒（MOVE_JUDGE_WINDOW_S=2.5s 在
  // 1Hz 取樣下量化成 3 秒一次判定 × 遲滯需連續 2 次才確認開始移動 ≈ 3x2=6 秒）；此後每個 2.5s+ 視窗都正確
  // 判定為 moving（見 log 全為 'moving'，無誤判），相較修復前「永遠 0」是質變。這一次性的暖機成本已在
  // movingTime.ts 開頭的根因說明中記錄，屬於已知、有界的設計取捨（非漏洞）。
  assert.ok(finalS >= 50, `修復後 60 筆(1Hz)應累積至少 50 秒移動時間，實得 ${finalS}s`)
  assert.ok(log.every((l) => l.signal === 'moving'), '穩定跑步中，暖機後每個判定視窗都應正確判為 moving，不應有誤判 still')
})

check('實戰情境②：靜止飄移（同點附近有界隨機漫步 ±8m、精度15m）連續60筆 → 移動時間應累積 < 5 秒', () => {
  const rng = mulberry32(7)
  let pos = 0
  const points: MovePoint[] = []
  for (let i = 0; i < 60; i++) {
    if (i > 0) {
      pos += (rng() - 0.5) * 3 // 每秒隨機游走 ±1.5m（貼近真實靜止 GPS 的自相關雜訊，而非逐筆重新取樣的大跳動）
      if (pos > 8) pos = 8 - (pos - 8) // 反射夾在 ±8m 內，模擬「同一點附近飄移」
      if (pos < -8) pos = -8 - (pos + 8)
    }
    points.push(ptAt(pos, i * 1000, 15))
  }
  const { state } = simulateTrack(points)
  const finalS = currentMovingS(state, points[points.length - 1].t)
  assert.ok(finalS < 5, `原地飄移 60 筆應累積 < 5 秒移動時間，實得 ${finalS}s（判定視窗+新門檻應能濾掉這類雜訊）`)
})

check('實戰情境③：跑30秒 → 停15秒（有界飄移）→ 跑30秒，階段性驗證移動時間正確增長/持平/再增長', () => {
  const rng = mulberry32(99)
  const points: MovePoint[] = []
  let cum = 0
  for (let i = 0; i <= 30; i++) { if (i > 0) cum += 3; points.push(ptAt(cum, i * 1000, 10)) } // 跑 30 秒，3 m/s
  let stopPos = cum
  for (let i = 1; i <= 15; i++) { // 停 15 秒：原地有界飄移（不是真的移動）
    stopPos += (rng() - 0.5) * 3
    if (stopPos > cum + 8) stopPos = cum + 8 - (stopPos - (cum + 8))
    if (stopPos < cum - 8) stopPos = cum - 8 - (stopPos - (cum - 8))
    points.push(ptAt(stopPos, 30000 + i * 1000, 10))
  }
  let cum2 = stopPos
  for (let i = 1; i <= 30; i++) { cum2 += 3; points.push(ptAt(cum2, 45000 + i * 1000, 10)) } // 再跑 30 秒

  // 逐點餵狀態機，在關鍵時間點拍下 currentMovingS 快照（複用 simulateTrack 的判定邏輯，但要中途取值，
  // 所以這裡展開迴圈而非直接呼叫 simulateTrack）。
  let state = initMovingState()
  let basis: MovePoint | null = null
  const snap: Record<number, number> = {}
  for (const p of points) {
    if (!basis) { basis = p } else if ((p.t - basis.t) / 1000 >= MOVE_JUDGE_WINDOW_S) {
      const signal = classifyMoveSignal(basis, p, haversineM(basis, p))
      if (signal) state = advanceMovingState(state, signal, p.t)
      basis = p
    }
    if ([30000, 36000, 44000, 51000, 75000].includes(p.t)) snap[p.t] = currentMovingS(state, p.t)
  }
  // 精確可重現值（種子99）：第一階段跑步結束(t=30s)已累積 24s（起跑暖機損耗~6s，同情境①）。
  assert.equal(snap[30000], 24, '第一階段跑步結束時應已累積約 24 秒移動時間')
  // 停止後兩點(36s→44s，皆已進入停止確認之後)應該持平，證明「停下來」被正確判定、沒有繼續累加。
  assert.equal(snap[44000], snap[36000], '停止期間(36s→44s)移動時間應持平，不應繼續累加')
  // 全程結束時應遠高於停止時的水位，證明第二段跑步有被正確恢復累加。
  assert.ok(snap[75000] > snap[44000] + 15, `恢復跑步後應明顯繼續累加，實得 停止水位=${snap[44000]}s 結束=${snap[75000]}s`)
  // 上限保底：不可能超過全程真實時間 75 秒。
  assert.ok(snap[75000] <= 75, '移動時間不可能超過全程總時間')
})

console.log(`\n${passed} assertions passed`)
