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
  type MovingState, type MovePoint,
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
  // 5 秒走 5m = 1 m/s ≥ 0.6，且 5m > max(3, 0.5*5=2.5)=3
  assert.equal(classifyMoveSignal(prev, cur, 5), 'moving')
})

check('classifyMoveSignal: 原地小幅飄移(精度差時的常見漂移) → still', () => {
  const prev: MovePoint = { lat: 0, lng: 0, t: 0, acc: 20 }
  const cur: MovePoint = { lat: 0, lng: 0, t: 5000, acc: 20 } // 5 秒
  // 5 秒飄 8m：速度 1.6 m/s ≥ 0.6，但位移下限 max(3, 0.5*20=10)=10 > 8 → 未過門檻 → still
  assert.equal(classifyMoveSignal(prev, cur, 8), 'still')
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

console.log(`\n${passed} assertions passed`)
