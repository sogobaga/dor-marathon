// 純函式驗證：src/lib/activeRun.ts（見 CONTRACT.md §3）。
// 執行：node --experimental-strip-types scripts/verify-active-run.mjs
// 模擬 localStorage（activeRun.ts 全部 try/catch，這裡順便驗證「localStorage 不可用」的降級路徑）。

let store = new Map()
let throwing = false
globalThis.localStorage = {
  getItem: (k) => { if (throwing) throw new Error('boom'); return store.has(k) ? store.get(k) : null },
  setItem: (k, v) => { if (throwing) throw new Error('boom'); store.set(k, String(v)) },
  removeItem: (k) => { if (throwing) throw new Error('boom'); store.delete(k) },
}

const {
  ACTIVE_RUN_KEY, ACTIVE_RUN_STALE_MS,
  readActiveRun, writeActiveRun, touchActiveRun, clearActiveRun,
  activeRunAgeMs, isActiveRunFresh,
} = await import('../src/lib/activeRun.ts')

let failed = 0
function check(name, cond) {
  if (!cond) { failed++; console.error('FAIL:', name) } else { console.log('ok  :', name) }
}

// 1. 空狀態
check('讀空值回 null', readActiveRun() === null)

// 2. 首次寫入需帶 startedAt/href
const t0 = Date.now() - 1000
writeActiveRun({ startedAt: t0, href: '/track?strategy=abc', lastSeenAt: t0, distanceM: 120.5, calibK: 0.98 })
let s = readActiveRun()
check('首次寫入可讀回', !!s && s.startedAt === t0 && s.href === '/track?strategy=abc')
check('未帶欄位有預設值', s.movingAccumS === 0 && s.excludedSegs === 0 && Array.isArray(s.splits))
check('calibK 寫入正確', s.calibK === 0.98)

// 3. 合併寫入（不覆蓋未帶的欄位）
writeActiveRun({ distanceM: 200 })
s = readActiveRun()
check('合併寫入保留 href/startedAt', s.href === '/track?strategy=abc' && s.startedAt === t0)
check('合併寫入更新 distanceM', s.distanceM === 200)

// 4. touchActiveRun 只動 lastSeenAt/movingAccumS
const before = readActiveRun().distanceM
touchActiveRun(42)
s = readActiveRun()
check('touchActiveRun 更新 movingAccumS', s.movingAccumS === 42)
check('touchActiveRun 不動其他欄位', s.distanceM === before)

// 5. undefined 不清掉既有值（raceStrategyId 等可選欄位的合併語意）
writeActiveRun({ raceStrategyId: 'race-1' })
writeActiveRun({ distanceM: 300 }) // 不帶 raceStrategyId
s = readActiveRun()
check('可選欄位在後續合併寫入未被清掉', s.raceStrategyId === 'race-1')
writeActiveRun({ raceStrategyId: undefined === undefined ? undefined : 'x' }) // no-op guard
// 顯式清空（傳 undefined 走的是「!== undefined ? patch : prev」分支，故傳 undefined 視為「沒帶」→ 維持既有值）
check('傳 undefined 視為未帶，維持既有值', readActiveRun().raceStrategyId === 'race-1')

// 6. 新鮮度判斷
const fresh = { v: 1, startedAt: t0, href: '/track', lastSeenAt: Date.now() - 1000, movingAccumS: 0, distanceM: 0, rawDistanceM: 0, excludedSegs: 0, excludedKm: 0, splits: [], splitMarks: [], movingSplitMarks: [], calibK: 1 }
const stale = { ...fresh, lastSeenAt: Date.now() - (ACTIVE_RUN_STALE_MS + 1000) }
check('剛更新視為新鮮', isActiveRunFresh(fresh))
check('超過 2 小時視為過期', !isActiveRunFresh(stale))
check('activeRunAgeMs 非負且約等於間隔', Math.abs(activeRunAgeMs(fresh) - 1000) < 200)

// 7. 損毀資料（非法 JSON / 缺必要欄位）一律回 null，不丟例外
store.set(ACTIVE_RUN_KEY, '{not json')
check('非法 JSON 回 null', readActiveRun() === null)
store.set(ACTIVE_RUN_KEY, JSON.stringify({ v: 1 })) // 缺 startedAt/href/lastSeenAt
check('缺必要欄位回 null', readActiveRun() === null)

// 8. clearActiveRun
store.set(ACTIVE_RUN_KEY, JSON.stringify(fresh))
clearActiveRun()
check('clearActiveRun 後讀回 null', readActiveRun() === null)

// 9. localStorage 拋錯時全部降級為 no-op / null，不拋出例外
throwing = true
let threw = false
try {
  writeActiveRun({ startedAt: 1, href: '/track' })
  touchActiveRun(1)
  clearActiveRun()
  check('localStorage 拋錯時 readActiveRun 仍回 null（不拋例外）', readActiveRun() === null)
} catch (e) { threw = true }
check('localStorage 全面拋錯不讓呼叫端跟著炸', !threw)
throwing = false

// 10. focusOpen 往返（口袋模式併入專注模式，見 CONTRACT.md track_focus_merge）
writeActiveRun({ focusOpen: true })
check('focusOpen 寫入 true 可讀回', readActiveRun().focusOpen === true)
writeActiveRun({ distanceM: 400 }) // 不帶 focusOpen，合併寫入語意應維持既有值（同 raceStrategyId 規則）
check('focusOpen 在後續合併寫入未被清掉', readActiveRun().focusOpen === true)

console.log(failed === 0 ? `\n全部通過（0 失敗）` : `\n${failed} 項失敗`)
process.exit(failed === 0 ? 0 : 1)
