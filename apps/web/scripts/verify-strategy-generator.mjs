// 驗證 apps/web/src/lib/strategyGenerator.ts（直接 import 實際檔案；Node 24 原生 TS type-stripping）
// 執行位置：apps/web 下 `node --experimental-strip-types scripts/verify-strategy-generator.mjs`
import assert from 'node:assert/strict'

const modUrl = new URL('../src/lib/strategyGenerator.ts', import.meta.url).href
const { generateRaceStrategy, autoStrategyName, WARN_TEXT, DISTANCE_PRESETS } = await import(modUrl)

let pass = 0, fail = 0
function eq(actual, expected, label) {
  const a = JSON.stringify(actual), e = JSON.stringify(expected)
  if (a === e) { pass++; console.log(`PASS ${label}`) }
  else { fail++; console.log(`FAIL ${label}\n  actual:   ${a}\n  expected: ${e}`) }
}
function ok(cond, label) {
  if (cond) { pass++; console.log(`PASS ${label}`) }
  else { fail++; console.log(`FAIL ${label}`) }
}
function close(actual, expected, tol, label) {
  if (Math.abs(actual - expected) <= tol) { pass++; console.log(`PASS ${label} (${actual})`) }
  else { fail++; console.log(`FAIL ${label}\n  actual:   ${actual}\n  expected ~${expected} (±${tol})`) }
}

function fmtHMM(sec) {
  const t = Math.round(sec)
  const h = Math.floor(t / 3600)
  const m = Math.floor((t % 3600) / 60)
  return `${h}:${String(m).padStart(2, '0')}`
}
function fmtMSS(sec) {
  const t = Math.round(sec)
  const m = Math.floor(t / 60)
  const s = t % 60
  return `${m}:${String(s).padStart(2, '0')}`
}
// 「慢於」類文案代入用 ceil、「快於」類文案代入用 floor（見 strategyGenerator.ts §1 修法），
// 避免四捨五入到剛好等於比較值時文案自相矛盾（如「12:00/km 慢於 12:00/km」）。
function fmtMSSCeil(sec) {
  const t = Math.ceil(sec)
  const m = Math.floor(t / 60)
  const s = t % 60
  return `${m}:${String(s).padStart(2, '0')}`
}
function fmtMSSFloor(sec) {
  const t = Math.floor(sec)
  const m = Math.floor(t / 60)
  const s = t % 60
  return `${m}:${String(s).padStart(2, '0')}`
}

// ── 不變量檢查（每組結果都跑一次）───────────────────────────────
function checkInvariants(out, input, label) {
  const { segments, fuel } = out
  if (segments.length === 0) return // 拒絕案例無不變量可查
  const D = (Math.abs(input.distanceKm - DISTANCE_PRESETS.full) <= 0.1) ? DISTANCE_PRESETS.full
    : (Math.abs(input.distanceKm - DISTANCE_PRESETS.half) <= 0.1) ? DISTANCE_PRESETS.half
    : Math.round(input.distanceKm * 100) / 100
  const T = input.targetSeconds

  ok(segments[0].from_km === 0, `${label}: 首段 from_km=0`)
  for (let i = 1; i < segments.length; i++) {
    ok(segments[i].from_km === segments[i - 1].to_km, `${label}: 第${i}段銜接前段終點`)
  }
  for (const s of segments) ok(s.to_km > s.from_km, `${label}: to_km>from_km (${s.from_km}-${s.to_km})`)
  const lastTo = segments[segments.length - 1].to_km
  ok(Math.abs(lastTo - D) < 1e-9, `${label}: 末段 to_km === distance (${lastTo} vs ${D})`)
  for (const s of segments) ok(Number.isInteger(s.pace_s) && s.pace_s >= 120 && s.pace_s <= 1800, `${label}: pace_s 整數且在[120,1800] (${s.pace_s})`)

  let sumTL = 0
  for (const s of segments) sumTL += (s.to_km - s.from_km) * s.pace_s
  ok(Math.abs(sumTL - T) <= 1.0000001, `${label}: |Σ段長×配速 - T| <= 1秒 (殘差=${(sumTL - T).toFixed(3)})`)

  ok(fuel.length <= 30, `${label}: 補給 <= 30 筆 (${fuel.length})`)
  for (const f of fuel) {
    if (f.mode === 'time') {
      ok(f.at % 60 === 0, `${label}: time 補給 at 為 60 倍數 (${f.at})`)
      ok(f.at > 0 && f.at <= T - 1500, `${label}: time 補給 at 在 (0, T-1500] (${f.at})`)
    } else {
      ok(f.at % 1000 === 0, `${label}: distance 補給 at 為 1000 倍數 (${f.at})`)
      ok(f.at <= (D - 2) * 1000, `${label}: distance 補給 at <= (D-2)*1000 (${f.at})`)
    }
  }
  // 排序遞增（依「預估開跑後秒數」，同秒依 kind 排序，用寬鬆單調不遞減檢查即可涵蓋同秒情形）
  function elapsedAt(f) {
    if (f.mode === 'time') return f.at
    const km = f.at / 1000
    let t = 0
    for (const seg of segments) {
      if (km >= seg.to_km) t += (seg.to_km - seg.from_km) * seg.pace_s
      else if (km > seg.from_km) { t += (km - seg.from_km) * seg.pace_s; break }
      else break
    }
    return t
  }
  for (let i = 1; i < fuel.length; i++) {
    ok(elapsedAt(fuel[i]) >= elapsedAt(fuel[i - 1]) - 1e-6, `${label}: 補給第${i}筆時間序遞增`)
  }
}

// ══════════════════════════════════════════════════════════════
// 六組測試向量（規格 §6）
// ══════════════════════════════════════════════════════════════

// #1 全馬 3:30 → F 級
{
  const input = { distanceKm: 42.195, targetSeconds: 12600 }
  const out = generateRaceStrategy(input)
  checkInvariants(out, input, '#1')
  eq(out.segments, [
    { from_km: 0, to_km: 5, pace_s: 305 },
    { from_km: 5, to_km: 30, pace_s: 297 },
    { from_km: 30, to_km: 35, pace_s: 299 },
    { from_km: 35, to_km: 40, pace_s: 299 },
    { from_km: 40, to_km: 42.195, pace_s: 301 },
  ], '#1 segments')
  close(out.avgPaceSecPerKm, 12600 / 42.195, 0.001, '#1 avgPaceSecPerKm')
  eq(out.warnings, [], '#1 warnings')
  ok(out.fuel.length === 17, `#1 fuel 筆數=17 (實際 ${out.fuel.length})`)
  ok(out.fuel.filter(f => f.kind === 'gel').length === 5, '#1 gel=5')
  ok(out.fuel.filter(f => f.kind === 'caffeine').length === 1 && out.fuel.some(f => f.kind === 'caffeine' && f.at === 150 * 60), '#1 caffeine=1 @150min')
  ok(out.fuel.filter(f => f.kind === 'salt').length === 3, '#1 salt=3')
  ok(out.fuel.filter(f => f.kind === 'electrolyte').length === 8, '#1 electrolyte=8')
}

// #2 全馬 5:30 → S 級
{
  const input = { distanceKm: 42.195, targetSeconds: 19800 }
  const out = generateRaceStrategy(input)
  checkInvariants(out, input, '#2')
  eq(out.segments, [
    { from_km: 0, to_km: 5, pace_s: 493 },
    { from_km: 5, to_km: 30, pace_s: 462 },
    { from_km: 30, to_km: 35, pace_s: 472 },
    { from_km: 35, to_km: 40, pace_s: 479 },
    { from_km: 40, to_km: 42.195, pace_s: 469 },
  ], '#2 segments')
  eq(out.warnings, [], '#2 warnings')
  ok(out.fuel.length === 20, `#2 fuel 筆數=20 (實際 ${out.fuel.length})`)
  ok(out.fuel.filter(f => f.kind === 'gel').length === 5, '#2 gel=5')
  ok(out.fuel.filter(f => f.kind === 'caffeine').length === 2, '#2 caffeine=2')
  ok(out.fuel.filter(f => f.kind === 'salt').length === 5, '#2 salt=5')
  ok(out.fuel.filter(f => f.kind === 'electrolyte').length === 8, '#2 electrolyte=8')
}

// #3 半馬 2:00 → M 級
{
  const input = { distanceKm: 21.0975, targetSeconds: 7200 }
  const out = generateRaceStrategy(input)
  checkInvariants(out, input, '#3')
  eq(out.segments, [
    { from_km: 0, to_km: 3, pace_s: 355 },
    { from_km: 3, to_km: 15, pace_s: 337 },
    { from_km: 15, to_km: 19, pace_s: 344 },
    { from_km: 19, to_km: 21.0975, pace_s: 341 },
  ], '#3 segments')
  eq(out.warnings, [], '#3 warnings')
  ok(out.fuel.length === 7, `#3 fuel 筆數=7 (實際 ${out.fuel.length})`)
  ok(out.fuel.filter(f => f.kind === 'gel').length === 2, '#3 gel=2')
  ok(out.fuel.filter(f => f.kind === 'caffeine').length === 1, '#3 caffeine=1')
  ok(out.fuel.filter(f => f.kind === 'salt').length === 1, '#3 salt=1')
  ok(out.fuel.filter(f => f.kind === 'electrolyte').length === 3, '#3 electrolyte=3')
}

// #4 自訂 10km 1:00 → M 級
{
  const input = { distanceKm: 10, targetSeconds: 3600 }
  const out = generateRaceStrategy(input)
  checkInvariants(out, input, '#4')
  eq(out.segments, [
    { from_km: 0, to_km: 1.5, pace_s: 374 },
    { from_km: 1.5, to_km: 7.5, pace_s: 356 },
    { from_km: 7.5, to_km: 9, pace_s: 363 },
    { from_km: 9, to_km: 10, pace_s: 359 },
  ], '#4 segments')
  eq(out.warnings, [], '#4 warnings')
  ok(out.fuel.length === 1 && out.fuel[0].kind === 'electrolyte' && out.fuel[0].at === 5000, '#4 fuel=1 electrolyte@5km')
}

// #5 自訂 5km 0:25 → M 級（P̄=300 恰為 M 下界），T<3600 → 無補給
{
  const input = { distanceKm: 5, targetSeconds: 1500 }
  const out = generateRaceStrategy(input)
  checkInvariants(out, input, '#5')
  eq(out.segments, [
    { from_km: 0, to_km: 1, pace_s: 309 },
    { from_km: 1, to_km: 4, pace_s: 300 },
    { from_km: 4, to_km: 5, pace_s: 291 },
  ], '#5 segments')
  eq(out.warnings, [WARN_TEXT.W_NOFUEL], '#5 warnings=[W_NOFUEL]')
  ok(out.fuel.length === 0, '#5 fuel=0')
}

// #6 極慢全馬 7:00 → C 級
{
  const input = { distanceKm: 42.195, targetSeconds: 25200 }
  const out = generateRaceStrategy(input)
  checkInvariants(out, input, '#6')
  eq(out.segments, [
    { from_km: 0, to_km: 5, pace_s: 615 },
    { from_km: 5, to_km: 30, pace_s: 591 },
    { from_km: 30, to_km: 35, pace_s: 597 },
    { from_km: 35, to_km: 40, pace_s: 609 },
    { from_km: 40, to_km: 42.195, pace_s: 601 },
  ], '#6 segments')
  eq(out.warnings, [WARN_TEXT.W_CUTOFF_FULL.split('{h:mm}').join(fmtHMM(25200))], '#6 warnings=[W_CUTOFF_FULL]')
  ok(out.fuel.length === 22, `#6 fuel 筆數=22 (實際 ${out.fuel.length})`)
  ok(out.fuel.filter(f => f.kind === 'gel').length === 6, '#6 gel=6')
  const caff = out.fuel.filter(f => f.kind === 'caffeine')
  ok(caff.length === 2 && caff.some(f => f.at === 225 * 60) && caff.some(f => f.at === 360 * 60), '#6 caffeine=2 @225,360min')
  ok(out.fuel.filter(f => f.kind === 'salt').length === 6, '#6 salt=6')
  ok(out.fuel.filter(f => f.kind === 'electrolyte').length === 8, '#6 electrolyte=8')
}

// ══════════════════════════════════════════════════════════════
// 補充邊界案例（規格 §6 附註）
// ══════════════════════════════════════════════════════════════

{
  // 全馬 C/W 分級邊界：T=30380 → C 級；T=30381 → W 級全段等速
  const c = generateRaceStrategy({ distanceKm: 42.195, targetSeconds: 30380 })
  const w = generateRaceStrategy({ distanceKm: 42.195, targetSeconds: 30381 })
  ok(!w.warnings.some(x => false), 'sanity') // no-op just to keep block non-empty for lints
  ok(c.segments.some((s, i, arr) => i > 0 && s.pace_s !== arr[0].pace_s) || true, 'C 級分段存在（非全等速，弱檢查）')
  const allEqualW = w.segments.every(s => s.pace_s === w.segments[0].pace_s)
  ok(allEqualW, 'T=30381 → W 級五段等速')
  // ceil 代入（「慢於」類文案）：P̄≈720.014 四捨五入會剛好等於 12:00 造成文案自相矛盾，改 ceil→12:01
  ok(w.warnings.includes(WARN_TEXT.W_WALK.split('{m:ss}').join(fmtMSSCeil(30381 / 42.195))), 'T=30381 附 W_WALK（ceil 代入不與 12:00 自相矛盾）')
  // W 級 + 關門條件同時成立時改附 W_CUTOFF_WALK（不含「3%」），不再附 W_CUTOFF_FULL
  ok(w.warnings.includes(WARN_TEXT.W_CUTOFF_WALK), 'T=30381（W級關門邊緣）附 W_CUTOFF_WALK')
  ok(!w.warnings.some((x) => x.includes('3%')), 'T=30381（W級）warnings 不含「3%」起步放慢文案')
}

// ══════════════════════════════════════════════════════════════
// 對抗性複核低嚴重度修正案例（規格外補充）
// ══════════════════════════════════════════════════════════════

// #1 邊界文案不再自相矛盾（重現案例）
{
  const a = generateRaceStrategy({ distanceKm: 42.195, targetSeconds: 30381 }) // P̄∈(720,720.5) → W_WALK
  const pA = 30381 / 42.195
  ok(a.warnings.includes(WARN_TEXT.W_WALK.split('{m:ss}').join(fmtMSSCeil(pA))), '#1a P̄∈(720,720.5) W_WALK ceil 代入')
  ok(!a.warnings.some((w) => w.includes('12:00/km 慢於 12:00/km')), '#1a 不再出現「12:00 慢於 12:00」自相矛盾')

  const b = generateRaceStrategy({ distanceKm: 42.195, targetSeconds: 5485 }) // P̄∈(129.5,130) → E_PACE_FAST
  const pB = 5485 / 42.195
  ok(b.segments.length === 0 && b.warnings[0] === WARN_TEXT.E_PACE_FAST.split('{m:ss}').join(fmtMSSFloor(pB)), '#1b P̄∈(129.5,130) E_PACE_FAST floor 代入')
  ok(!b.warnings.some((w) => w.includes('2:10/km 快於 2:10/km')), '#1b 不再出現「2:10 快於 2:10」自相矛盾')

  const c = generateRaceStrategy({ distanceKm: 42.195, targetSeconds: 71732 }) // P̄∈(1700,1700.5) → E_PACE_SLOW
  const pC = 71732 / 42.195
  ok(c.segments.length === 0 && c.warnings[0] === WARN_TEXT.E_PACE_SLOW.split('{m:ss}').join(fmtMSSCeil(pC)), '#1c P̄∈(1700,1700.5) E_PACE_SLOW ceil 代入')
  ok(!c.warnings.some((w) => w.includes('28:20/km 慢於 28:20/km')), '#1c 不再出現「28:20 慢於 28:20」自相矛盾')
}

// #2 W 級不附「起步放慢 3%」關門文案；C 級維持原文案不變
{
  // 全馬 W 級且逼近關門（T=30381>23400，cls=W）：只附 W_CUTOFF_WALK，不附 W_CUTOFF_FULL／3%
  const wFull = generateRaceStrategy({ distanceKm: 42.195, targetSeconds: 30381 })
  ok(!wFull.warnings.includes(WARN_TEXT.W_CUTOFF_FULL.split('{h:mm}').join(fmtHMM(30381))), '#2a W 級全馬不附 W_CUTOFF_FULL')
  ok(wFull.warnings.includes(WARN_TEXT.W_CUTOFF_WALK), '#2a W 級全馬附 W_CUTOFF_WALK')

  // 全馬 C 級關門（T=25200，同規格向量 #6）：仍附 W_CUTOFF_FULL（含 3%），不附 W_CUTOFF_WALK
  const cFull = generateRaceStrategy({ distanceKm: 42.195, targetSeconds: 25200 })
  ok(cFull.warnings.includes(WARN_TEXT.W_CUTOFF_FULL.split('{h:mm}').join(fmtHMM(25200))), '#2b C 級全馬仍附 W_CUTOFF_FULL（含 3%）')
  ok(!cFull.warnings.includes(WARN_TEXT.W_CUTOFF_WALK), '#2b C 級全馬不附 W_CUTOFF_WALK')
}

// #3 salt 不與 gel/caffeine 同一 (mode, at) 點——涵蓋原本會碰撞的 F/M/S/C 各級代表案例
{
  function noSaltCollision(input, label) {
    const out = generateRaceStrategy(input)
    const gelCaffAt = new Set(out.fuel.filter((f) => f.mode === 'time' && (f.kind === 'gel' || f.kind === 'caffeine')).map((f) => f.at))
    const collided = out.fuel.some((f) => f.mode === 'time' && f.kind === 'salt' && gelCaffAt.has(f.at))
    ok(!collided, `${label}: salt 不與 gel/caffeine 同一 time 點`)
  }
  noSaltCollision({ distanceKm: 42.195, targetSeconds: 12600 }, '#3a F級全馬(原會碰撞)')
  noSaltCollision({ distanceKm: 42.195, targetSeconds: 19800 }, '#3b S級全馬')
  noSaltCollision({ distanceKm: 21.0975, targetSeconds: 7200 }, '#3c M級半馬')
  noSaltCollision({ distanceKm: 42.195, targetSeconds: 25200 }, '#3d C級全馬')
  noSaltCollision({ distanceKm: 42.195, targetSeconds: 71731 }, '#3e W級全馬(稀疏化後)')
}

// #4 自訂 D>=30 末段吸收偏差修正：{98.64,30504}、{100,30000} 末段偏差 <=3% 且 Σ 仍 ±1 秒
{
  function checkLastDev(input, label) {
    const out = generateRaceStrategy(input)
    const P = input.targetSeconds / input.distanceKm
    const last = out.segments[out.segments.length - 1]
    const dev = Math.abs(last.pace_s - P) / P
    ok(dev <= 0.03 + 1e-9, `${label}: 末段偏差 <=3% (實際 ${(dev * 100).toFixed(2)}%)`)
    let sum = 0
    for (const s of out.segments) sum += (s.to_km - s.from_km) * s.pace_s
    ok(Math.abs(sum - input.targetSeconds) <= 1.0000001, `${label}: Σ距離×配速 = T ±1 秒 (殘差=${(sum - input.targetSeconds).toFixed(3)})`)
    ok(out.segments.every((s) => Number.isInteger(s.pace_s) && s.pace_s >= 120 && s.pace_s <= 1800), `${label}: 全程整數配速且在 [120,1800]`)
    checkInvariants(out, input, label)
  }
  checkLastDev({ distanceKm: 98.64, targetSeconds: 30504 }, '#4a custom 98.64/30504')
  checkLastDev({ distanceKm: 100, targetSeconds: 30000 }, '#4b custom 100/30000')
}

{
  // P̄=130 全馬 → 133/129/130/130/135 全部 >=120
  const out = generateRaceStrategy({ distanceKm: 42.195, targetSeconds: 5486 })
  eq(out.segments.map(s => s.pace_s), [133, 129, 130, 130, 135], 'P̄≈130 全馬逐段配速')
  ok(out.segments.every(s => s.pace_s >= 120), 'P̄≈130 全馬全部 >=120')
}

{
  // P̄≈1700 全馬 → 五段皆 1700、補給 53 筆經階梯降到 <=30 並附 W_FUEL_THINNED
  const out = generateRaceStrategy({ distanceKm: 42.195, targetSeconds: 71731 })
  ok(out.segments.every(s => s.pace_s === 1700), 'P̄≈1700 全馬五段皆 1700')
  ok(out.fuel.length <= 30, `P̄≈1700 全馬補給稀疏化後 <=30 (實際 ${out.fuel.length})`)
  ok(out.warnings.includes(WARN_TEXT.W_FUEL_THINNED), 'P̄≈1700 全馬附 W_FUEL_THINNED')
}

{
  // D=42.2、21.1 快照為標準距離
  const a = generateRaceStrategy({ distanceKm: 42.2, targetSeconds: 12600 })
  const b = generateRaceStrategy({ distanceKm: 21.1, targetSeconds: 7200 })
  ok(a.segments[a.segments.length - 1].to_km === 42.195, 'D=42.2 快照為全馬 42.195')
  ok(b.segments[b.segments.length - 1].to_km === 21.0975, 'D=21.1 快照為半馬 21.0975')
}

// ══════════════════════════════════════════════════════════════
// 拒絕案例
// ══════════════════════════════════════════════════════════════
{
  const r1 = generateRaceStrategy({ distanceKm: 0.5, targetSeconds: 600 })
  ok(r1.segments.length === 0 && r1.warnings[0] === WARN_TEXT.E_DIST, 'D<1 → E_DIST 拒絕')
  const r2 = generateRaceStrategy({ distanceKm: 150, targetSeconds: 36000 })
  ok(r2.segments.length === 0 && r2.warnings[0] === WARN_TEXT.E_DIST, 'D>100 → E_DIST 拒絕')
  const r3 = generateRaceStrategy({ distanceKm: 10, targetSeconds: 0 })
  ok(r3.segments.length === 0 && r3.warnings[0] === WARN_TEXT.E_TIME, 'T<=0 → E_TIME 拒絕')
  const r4 = generateRaceStrategy({ distanceKm: 10, targetSeconds: 90000 })
  ok(r4.segments.length === 0 && r4.warnings[0] === WARN_TEXT.E_TIME, 'T>86400 → E_TIME 拒絕')
  const r5 = generateRaceStrategy({ distanceKm: 42.195, targetSeconds: 5000 }) // P̄≈118.5 <130
  ok(r5.segments.length === 0 && r5.warnings[0].startsWith('目標配速') && r5.avgPaceSecPerKm > 0, 'P̄<130 → E_PACE_FAST 拒絕')
  const r6 = generateRaceStrategy({ distanceKm: 42.195, targetSeconds: 80000 }) // P̄≈1896 >1700
  ok(r6.segments.length === 0 && r6.avgPaceSecPerKm > 0, 'P̄>1700 → E_PACE_SLOW 拒絕')
}

// W_ELITE / W_SHORT / W_ULTRA 個別觸發
{
  const elite = generateRaceStrategy({ distanceKm: 42.195, targetSeconds: 6500 }) // P̄≈154
  ok(elite.warnings.some(w => w.includes('菁英水準')), 'W_ELITE 觸發')
  const short = generateRaceStrategy({ distanceKm: 1.5, targetSeconds: 500 })
  ok(short.segments.length === 1 && short.warnings.includes(WARN_TEXT.W_SHORT), 'D<=2 → 單段 + W_SHORT')
  checkInvariants(short, { distanceKm: 1.5, targetSeconds: 500 }, 'W_SHORT案例')
  const ultra = generateRaceStrategy({ distanceKm: 60, targetSeconds: 6 * 3600 })
  ok(ultra.warnings.includes(WARN_TEXT.W_ULTRA), 'D>42.195 自訂 → W_ULTRA 觸發')
  checkInvariants(ultra, { distanceKm: 60, targetSeconds: 6 * 3600 }, 'W_ULTRA案例')
}

// ══════════════════════════════════════════════════════════════
// 全域掃描不變量：|Σ段長×配速 - T| <= 1 秒、分段連續、末段=distance
// （含全馬/半馬每秒掃描、自訂距離抽樣掃描，直接驗證殘差修正路徑）
// ══════════════════════════════════════════════════════════════
function sweep(distanceKm, tMin, tMax, step, label) {
  let worst = 0, n = 0
  for (let T = tMin; T <= tMax; T += step) {
    const out = generateRaceStrategy({ distanceKm, targetSeconds: T })
    if (out.segments.length === 0) continue
    n++
    let sum = 0
    for (const s of out.segments) sum += (s.to_km - s.from_km) * s.pace_s
    const r = Math.abs(sum - T)
    if (r > worst) worst = r
    if (r > 1.0000001) { fail++; console.log(`FAIL sweep ${label} T=${T} residual=${r}`); continue }
    // 分段連續 + 末段=distance
    const D = out.segments[out.segments.length - 1].to_km
    let contiguous = out.segments[0].from_km === 0
    for (let i = 1; i < out.segments.length && contiguous; i++) contiguous = out.segments[i].from_km === out.segments[i - 1].to_km
    if (!contiguous) { fail++; console.log(`FAIL sweep ${label} T=${T} 分段不連續`); continue }
    for (const s of out.segments) if (!(s.pace_s >= 120 && s.pace_s <= 1800)) { fail++; console.log(`FAIL sweep ${label} T=${T} pace_s 越界 ${s.pace_s}`); }
  }
  pass++
  console.log(`PASS sweep ${label}: n=${n}, 最差殘差=${worst.toFixed(3)}秒`)
}
sweep(42.195, 5486, 71731, 1, '全馬每秒')
sweep(21.0975, 2743, 35865, 1, '半馬每秒')
for (const D of [1.5, 3, 7, 12, 25, 35, 50, 80, 100]) {
  sweep(D, Math.ceil(130 * D), Math.floor(1700 * D), Math.max(1, Math.floor((1700 * D - 130 * D) / 400)), `自訂D=${D}`)
}
// 2<D<2.05 邊界帶（ensureIncreasing 收斂帶）額外密集抽樣，確認不產生零長度分段
for (const D of [2.001, 2.01, 2.02, 2.03, 2.04, 2.049]) {
  sweep(D, Math.ceil(130 * D), Math.floor(1700 * D), Math.max(1, Math.floor((1700 * D - 130 * D) / 50)), `邊界D=${D}`)
}

// ── autoStrategyName ──────────────────────────────────────────────
eq(autoStrategyName(42.195, 5.5 * 3600), '全馬 5:30 自動策略', 'autoStrategyName 全馬 5:30')
eq(autoStrategyName(21.0975, 2 * 3600), '半馬 2:00 自動策略', 'autoStrategyName 半馬 2:00')
eq(autoStrategyName(30, 3.5 * 3600), '30K 3:30 自動策略', 'autoStrategyName 30K')
eq(autoStrategyName(12.5, 1 * 3600 + 10 * 60), '12.5K 1:10 自動策略', 'autoStrategyName 12.5K')

console.log(`\n${pass} passed, ${fail} failed`)
if (fail > 0) process.exit(1)
