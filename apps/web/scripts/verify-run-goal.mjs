// 驗證 apps/web/src/lib/runGoal.ts（直接 import 實際檔案，非重寫邏輯；Node 24 原生 TS type-stripping）
// 執行位置：apps/web 下 `node --experimental-strip-types scripts/verify-run-goal.mjs`（路徑以本檔為基準，不寫死）
const modUrl = new URL('../src/lib/runGoal.ts', import.meta.url).href
const { resolveRunGoal, fmtKm, cheerPhaseAndRemain } = await import(modUrl)

let pass = 0, fail = 0
function eq(actual, expected, label) {
  const a = JSON.stringify(actual), e = JSON.stringify(expected)
  if (a === e) { pass++; console.log(`PASS ${label}`) }
  else { fail++; console.log(`FAIL ${label}\n  actual:   ${a}\n  expected: ${e}`) }
}

// ── resolveRunGoal ──────────────────────────────────────────────
eq(
  resolveRunGoal({ total_km: 21.1 }, null),
  { type: 'distance', totalM: 21100 },
  'strategy total_km>0 → distance',
)
eq(
  resolveRunGoal(null, { kind: 'freetrain', steps: [], freerunSec: 1800 }),
  { type: 'time', totalS: 1800 },
  'freetrain + freerunSec>0 → time',
)
eq(
  resolveRunGoal(null, { kind: 'freetrain', steps: [], freerunSec: 0 }),
  { type: 'none' },
  'freetrain freerunSec=0 → none（非硬性目標）',
)
eq(
  resolveRunGoal(null, {
    kind: 'personal',
    steps: [
      { kind: 'warmup', label: '暖身', targetType: 'distance', target: 1000, graded: false },
      { kind: 'work', label: '主課', targetType: 'distance', target: 5000, graded: true },
    ],
  }),
  { type: 'distance', totalM: 6000 },
  '全 distance 分段 → 加總 distance',
)
eq(
  resolveRunGoal(null, {
    kind: 'personal',
    steps: [
      { kind: 'work', label: 'A', targetType: 'time', target: 600, graded: true },
      { kind: 'rest', label: 'B', targetType: 'time', target: 60, graded: false },
    ],
  }),
  { type: 'time', totalS: 660 },
  '全 time 分段 → 加總 time',
)
eq(
  resolveRunGoal(null, {
    kind: 'personal',
    steps: [
      { kind: 'warmup', label: '暖身', targetType: 'distance', target: 1000, graded: false },
      { kind: 'work', label: '主課', targetType: 'time', target: 600, graded: true },
    ],
  }),
  { type: 'none' },
  '混合 distance/time 分段 → none',
)
eq(resolveRunGoal(null, null), { type: 'none' }, '無 strategy 無 workout → none')
eq(resolveRunGoal({ total_km: 0 }, null), { type: 'none' }, 'strategy total_km=0 → 落回其他判定 → none')

// ── fmtKm ───────────────────────────────────────────────────────
eq(fmtKm(10), '10', '整數 10 → "10"（不帶小數）')
eq(fmtKm(21.1), '21.1', '21.1 → "21.1"')
eq(fmtKm(21.05), '21.1', '21.05 四捨五入到 1 位 → "21.1"')
eq(fmtKm(3.04), '3', '3.04 四捨五入到 1 位得整數 3 → "3"（不帶 .0）')
eq(fmtKm(-2), '0', '負值夾在 0（remain 超過目標時的防呆）')

// ── cheerPhaseAndRemain ─────────────────────────────────────────
// distance 目標，總量 10km=10000m
eq(
  cheerPhaseAndRemain({ type: 'distance', totalM: 10000 }, 3, 0),
  { phase: 'before', remainText: '7 km' },
  'distance 3/10km（30%）→ before，remain 7 km',
)
eq(
  cheerPhaseAndRemain({ type: 'distance', totalM: 10000 }, 5, 0),
  { phase: 'after', remainText: '5 km' },
  'distance 5/10km（剛好 50%，remain>0）→ after',
)
eq(
  cheerPhaseAndRemain({ type: 'distance', totalM: 10000 }, 8, 0),
  { phase: 'after', remainText: '2 km' },
  'distance 8/10km（80%）→ after，remain 2 km',
)
eq(
  cheerPhaseAndRemain({ type: 'distance', totalM: 10000 }, 10, 0),
  { phase: 'before', remainText: '0 km' },
  'distance 10/10km（達標，remain=0 不 >0）→ 落回 before（累積式）',
)
eq(
  cheerPhaseAndRemain({ type: 'distance', totalM: 10000 }, 12, 0),
  { phase: 'before', remainText: '0 km' },
  'distance 超過目標（12km/10km）→ before，remain 夾 0',
)
// time 目標，總量 3600s（60 分鐘）
eq(
  cheerPhaseAndRemain({ type: 'time', totalS: 3600 }, 1, 900),
  { phase: 'before', remainText: '45 分鐘' },
  'time 900/3600s（25%）→ before，remain ceil(2700/60)=45 分鐘',
)
eq(
  cheerPhaseAndRemain({ type: 'time', totalS: 3600 }, 1, 1801),
  { phase: 'after', remainText: '30 分鐘' },
  'time 1801/3600s(>50%,remain>0) → after，remain ceil(1799/60)=30 分鐘',
)
eq(
  cheerPhaseAndRemain({ type: 'time', totalS: 3600 }, 1, 3700),
  { phase: 'before', remainText: '0 分鐘' },
  'time 超過目標 → before，remain 夾 0',
)
// none 目標
eq(
  cheerPhaseAndRemain({ type: 'none' }, 3, 500),
  { phase: 'before', remainText: '' },
  'none 目標一律 before，remainText 空字串',
)

console.log(`\n${pass} passed, ${fail} failed`)
if (fail > 0) process.exit(1)
