// DORPG P7（CONTRACT §1）：Go↔TS 鏡像測試——BACKEND 產生的 element_cases.json 列出一組
// (attack_element, monster_attr, weak_elements[, chart_override]) → expected 倍率，Go 端與這支
// 腳本各自對同一份案例跑一次，兩邊都要算出同一個數字，才能證明 formulas.ts elementMultiplier()
// 真的是 CONTRACT §1 五行相剋表的正確鏡像，不是「看起來像」而已。
//
// 執行方式（apps/web 目錄下）：
//   node --experimental-strip-types scripts/verify-dorpg-elements.mjs
//
// 案例檔路徑：BACKEND 產生於 scratchpad/dorpg_p7/element_cases.json（INTEGRATOR 之後會把它搬進
// repo 固定路徑共用給 Go 測試，見任務回報）。本輪撰寫時該檔案已存在，直接讀取；萬一路徑改變或
// 檔案暫時不存在（例如未來重構），退回一份依契約手寫的最小案例組，不讓這支腳本直接掛掉——這樣
// `npx tsc --noEmit && node ... verify-dorpg-elements.mjs` 這條驗證鏈永遠有東西可以跑。
//
// loader hook 與 verify-dorpg-engine.mjs 同款（相對匯入補 .ts 副檔名），只是這支腳本只需要
// elementMultiplier/DEFAULT_BATTLE_CONFIG 兩個匯出，不需要目錄 index 解析的候選（沒有透過
// `from './engine'` 這種目錄匯入）。
import { register } from 'node:module'
import { readFileSync } from 'node:fs'

const loaderSrc = `
export async function resolve(specifier, context, nextResolve) {
  try {
    return await nextResolve(specifier, context)
  } catch (err) {
    if (err && err.code === 'ERR_MODULE_NOT_FOUND' && specifier.startsWith('.')) {
      for (const suffix of ['.ts', '.tsx']) {
        try { return await nextResolve(specifier + suffix, context) } catch {}
      }
    }
    throw err
  }
}
`
register('data:text/javascript,' + encodeURIComponent(loaderSrc), import.meta.url)

const modUrl = new URL('../src/lib/dorpg/engine/index.ts', import.meta.url).href
const { elementMultiplier, DEFAULT_BATTLE_CONFIG } = await import(modUrl)

let pass = 0, fail = 0
function eq(actual, expected, label) {
  const a = JSON.stringify(actual), e = JSON.stringify(expected)
  if (a === e) { pass++; console.log(`PASS ${label}`) }
  else { fail++; console.log(`FAIL ${label}\n  actual:   ${a}\n  expected: ${e}`) }
}

// scratchpad 的絕對路徑（BACKEND 這一輪產出的位置，見任務指示）；找不到就退回內建案例。
const SCRATCHPAD_PATH =
  'C:/Users/paris/AppData/Local/Temp/claude/c--Project-Dev-04-Online-Marathon-Project/6f259465-0f7d-431c-a122-bf21a4e9a8eb/scratchpad/dorpg_p7/element_cases.json'

/**
 * 內建最小案例組（契約 §1 手寫，20 條）：五行相剋方向全 5 對＋反向被剋 1 組＋光闇互剋雙向＋
 * 同屬性 1 組＋不相干組合＋neutral 短路（雙邊各一）＋weakElements 與五行表取大（兩種組合）＋
 * chart 覆寫最優先。只在 element_cases.json 讀不到時才會用到這組。
 */
const FALLBACK_CASES = {
  config: { advantage_pct: 25, disadvantage_pct: -25, same_pct: -25, weakness_pct: 25 },
  cases: [
    { label: 'metal_beats_wood', attack_element: 'metal', monster_attr: 'wood', weak_elements: [], expected: 1.25 },
    { label: 'wood_beats_earth', attack_element: 'wood', monster_attr: 'earth', weak_elements: [], expected: 1.25 },
    { label: 'earth_beats_water', attack_element: 'earth', monster_attr: 'water', weak_elements: [], expected: 1.25 },
    { label: 'water_beats_fire', attack_element: 'water', monster_attr: 'fire', weak_elements: [], expected: 1.25 },
    { label: 'fire_beats_metal', attack_element: 'fire', monster_attr: 'metal', weak_elements: [], expected: 1.25 },
    { label: 'wood_beaten_by_metal', attack_element: 'wood', monster_attr: 'metal', weak_elements: [], expected: 0.75 },
    { label: 'light_beats_dark', attack_element: 'light', monster_attr: 'dark', weak_elements: [], expected: 1.25 },
    { label: 'dark_beats_light', attack_element: 'dark', monster_attr: 'light', weak_elements: [], expected: 1.25 },
    { label: 'same_fire', attack_element: 'fire', monster_attr: 'fire', weak_elements: [], expected: 0.75 },
    { label: 'same_light', attack_element: 'light', monster_attr: 'light', weak_elements: [], expected: 0.75 },
    { label: 'unrelated_metal_water', attack_element: 'metal', monster_attr: 'water', weak_elements: [], expected: 1 },
    { label: 'attack_neutral', attack_element: 'neutral', monster_attr: 'fire', weak_elements: [], expected: 1 },
    { label: 'monster_neutral', attack_element: 'fire', monster_attr: 'neutral', weak_elements: [], expected: 1 },
    { label: 'monster_attribute_missing', attack_element: 'fire', monster_attr: undefined, weak_elements: [], expected: 1 },
    { label: 'weak_over_neutral_relation', attack_element: 'fire', monster_attr: 'wood', weak_elements: ['fire'], expected: 1.25 },
    { label: 'weak_take_max_over_disadvantage', attack_element: 'wood', monster_attr: 'metal', weak_elements: ['wood'], expected: 1.25 },
    { label: 'weak_take_max_no_stack_with_advantage', attack_element: 'metal', monster_attr: 'wood', weak_elements: ['metal'], expected: 1.25 },
    { label: 'weak_not_matching', attack_element: 'fire', monster_attr: 'wood', weak_elements: ['water'], expected: 1 },
    { label: 'chinese_alias_metal', attack_element: 'metal', monster_attr: '金', weak_elements: [], expected: 0.75 },
    { label: 'chart_override_wins', attack_element: 'light', monster_attr: 'dark', weak_elements: ['light'], chart_override: { light: 3 }, expected: 3 },
  ],
}

function loadCases() {
  try {
    const raw = readFileSync(SCRATCHPAD_PATH, 'utf8')
    const parsed = JSON.parse(raw)
    if (parsed && Array.isArray(parsed.cases) && parsed.cases.length > 0) {
      console.log(`（讀取 BACKEND 產出的 element_cases.json，共 ${parsed.cases.length} 條案例）`)
      return parsed
    }
  } catch {
    // 檔案不存在或格式不對，落到下面的內建案例。
  }
  console.log('（element_cases.json 讀取失敗，退回內建的 20 條手寫案例）')
  return FALLBACK_CASES
}

const data = loadCases()
const { advantage_pct, disadvantage_pct, same_pct, weakness_pct } = data.config

for (const c of data.cases) {
  const cfg = {
    ...DEFAULT_BATTLE_CONFIG,
    elementAdvantagePct: advantage_pct,
    elementDisadvantagePct: disadvantage_pct,
    elementSamePct: same_pct,
    weaknessBonusPct: weakness_pct,
    elementChart: c.chart_override ? { [c.monster_attr]: c.chart_override } : {},
  }
  const enemy = { attribute: c.monster_attr, weakElements: c.weak_elements ?? [] }
  const actual = elementMultiplier(cfg, c.attack_element, enemy)
  eq(actual, c.expected, `[${c.label}] attack=${c.attack_element} monster=${c.monster_attr} weak=${JSON.stringify(c.weak_elements ?? [])} → ${c.expected}`)
}

console.log(`\n${pass} passed, ${fail} failed`)
if (fail > 0) process.exit(1)
