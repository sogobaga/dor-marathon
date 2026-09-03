// 驗證 iOS Safari 視窗高度安全網（ViewportHeightFix.tsx + globals.css + layout.tsx）。
// 執行位置：apps/web 下 `node --experimental-strip-types scripts/verify-viewport-height.mjs`（路徑以本檔為基準，不寫死）
//
// 為什麼要有這支：開發端在 Windows，無法實機重現 iOS。但本次修正的三條不變式其實**不需要瀏覽器**就能驗——
// 它們全都是「給定一組量測值，程式該不該寫入 --app-h」的純決策問題。這支腳本用假 DOM 餵入
// 「症狀 A（ICB 過期偏小）」「症狀 B（鍵盤把 vv 壓小）」等情境，直接對真實的 ViewportHeightFix 原始碼取證。
//
// Part 1：靜態不變式（掃真實檔案，防止未來有人把 max() 改回 var(--app-h, 100dvh)）
// Part 2：行為驗證（載入真實 ViewportHeightFix.tsx，只把兩行 import 換成 stub，其餘一字不改）
import { readFileSync, writeFileSync, mkdtempSync, rmSync } from 'node:fs'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

let pass = 0, fail = 0
function ok(cond, label, extra) {
  if (cond) { pass++; console.log(`PASS ${label}`) }
  else { fail++; console.log(`FAIL ${label}${extra ? `\n  ${extra}` : ''}`) }
}
function eq(actual, expected, label) {
  ok(actual === expected, label, `actual: ${JSON.stringify(actual)}  expected: ${JSON.stringify(expected)}`)
}

const src = (rel) => readFileSync(fileURLToPath(new URL(`../${rel}`, import.meta.url)), 'utf8')

// ── Part 1：靜態不變式 ─────────────────────────────────────────
console.log('── Part 1：靜態不變式（真實檔案）──')

const css = src('src/app/globals.css')
const vhfSrc = src('src/components/ViewportHeightFix.tsx')
const adSrc = src('src/components/InterstitialAd.tsx')
const layoutSrc = src('src/app/layout.tsx')
const mobileSrc = src('src/lib/useIsMobile.ts')

// 只看「真的會生效的宣告」：排除 CSS 註解區塊與 JS 註解行（那裡刻意留了反例警語）。
const cssDecls = css.replace(/\/\*[\s\S]*?\*\//g, '')
const adDecls = adSrc.split('\n').filter((l) => !l.trim().startsWith('//')).join('\n')

const consumers = [...cssDecls.matchAll(/var\(--app-h[^)]*\)/g), ...adDecls.matchAll(/var\(--app-h[^)]*\)/g)]
eq(consumers.length, 6, '--app-h 消費點恰好 6 處（html/body、.app-h、.app-min-h、.phone-frame、.phone-shell 死碼、InterstitialAd）')
ok(consumers.every((m) => m[0] === 'var(--app-h, 0px)'), '每個 var(--app-h) 的後備值都是 0px（不得是 100dvh/100%：舊瀏覽器解析失敗會蓋掉上一行的 100vh → height:auto 塌陷）')

// 不變式 I：每個消費點都被 max() 包住 → JS 無權讓版面比純 CSS 版矮。
for (const [name, re] of [
  ['html/body', /height:\s*max\(100%,\s*var\(--app-h, 0px\)\)/],
  ['.app-h', /\.app-h\s*\{[^}]*height:\s*max\(100dvh,\s*var\(--app-h, 0px\)\)/],
  ['手機 .phone-frame', /\.phone-frame\s*\{[^}]*height:\s*max\(100dvh,\s*var\(--app-h, 0px\)\)/],
  ['手機 .phone-shell（死碼備援）', /\.phone-shell\s*\{[^}]*height:\s*max\(100dvh,\s*var\(--app-h, 0px\)\)\s*!important/],
]) ok(re.test(cssDecls), `不變式 I：${name} 用 max() 夾住`)
ok(/height:\s*'max\(100dvh,\s*var\(--app-h, 0px\)\)'/.test(adDecls), '不變式 I：InterstitialAd 蓋板用 max() 夾住')

// ── 後備鏈是不是「真的」有效：含 var() 的宣告必須關在 @supports 裡 ──
// 為什麼這組檢查比「檔案裡看得到 height:100vh」重要（v1.1.675 正是敗在這裡，而舊版腳本回報 PASS）：
// CSS Variables 規範明定「宣告只要含語法合法的 var()，parse 階段一律視為有效」，於是它會贏得 cascade
// 並丟棄同屬性的前幾條；要到 computed-value 階段代換完才發現舊引擎不認得 dvh → invalid at
// computed-value time → 非繼承屬性取 initial 值 height:auto，**不會**回退到 100vh。而 .app-h /
// .phone-frame 的子節點全是 position:absolute ⇒ auto 收斂成 0 ⇒ 整頁空白（不是「底部少一截」）。
// DevTools 的「停用宣告」等同 parse 階段失效、一定會正常回退，那種手動驗法在原理上測不出本問題；
// 純字串比對同樣測不出。只能像下面這樣做結構檢查。
function supportsRegions(text) {
  const out = []
  const re = /@supports\s*([^{]*)\{/g
  let m
  while ((m = re.exec(text))) {
    let depth = 1, i = re.lastIndex
    while (i < text.length && depth > 0) { const c = text[i]; if (c === '{') depth++; else if (c === '}') depth--; i++ }
    out.push({ start: m.index, end: i, cond: m[1].trim() })
  }
  return out
}
const regions = supportsRegions(cssDecls)
const regionOf = (idx) => regions.find((r) => idx > r.start && idx < r.end)

const unguarded = []
for (const m of cssDecls.matchAll(/var\(--app-h[^)]*\)/g)) {
  if (!regionOf(m.index)) unguarded.push((cssDecls.slice(Math.max(0, m.index - 120), m.index + 24).split('\n').pop() || '').trim())
}
ok(unguarded.length === 0, '後備鏈有效：所有含 var(--app-h) 的 CSS 宣告都關在 @supports 內（否則它會蓋掉裸露的 100vh/100% 後備，舊引擎 IACVT → height:auto → 整頁空白）', unguarded.join('\n  '))

ok(regions.length >= 3, `@supports 區塊存在（實得 ${regions.length} 個）`)
for (const r of regions) ok(/height:\s*(100dvh|max\()/.test(r.cond), `@supports 條件真的測得出舊引擎缺的能力：@supports ${r.cond}`)

for (const [name, re] of [
  ['html/body', /html,\s*body\s*\{[^}]*height:\s*100%;/],
  ['.app-h', /\.app-h\s*\{\s*height:\s*100vh;\s*\}/],
  ['手機 .phone-frame', /\.phone-frame\s*\{[^}]*overflow:\s*hidden;\s*height:\s*100vh;\s*\}/],
  ['手機 .phone-shell', /\.phone-shell\s*\{[^}]*height:\s*100vh\s*!important;/],
]) {
  const m = re.exec(cssDecls)
  ok(!!m && !regionOf(m.index), `${name} 在 @supports 之外留有裸露、到處合法的後備宣告（舊引擎唯一會生效的一條）`)
}

// 症狀 A 的降級保護：蓋板暗色遮罩獨立成超掃層，就算 ICB 過期偏小也不會被腰斬。
ok(/top:\s*'-30vh',\s*height:\s*'160vh'/.test(adDecls), 'InterstitialAd 有 160vh 超掃遮罩層（症狀 A 的純 CSS 降級保護）')
ok(/zIndex:\s*-1/.test(adDecls), '超掃遮罩 z-index:-1（否則會蓋住 in-flow 的卡片/文字/checkbox）')

// 不變式 II：innerHeight 絕不可當高度來源（v1.1.664 的致命點）。
const vhfCode = vhfSrc.split('\n').filter((l) => !/^\s*(\/\/|\*|\/\*)/.test(l)).join('\n')
ok(!/Math\.max\(\s*window\.innerHeight/.test(vhfCode), '不變式 II：不再用 Math.max(innerHeight, vv.height) 當高度（iOS 17/18 兩者會一起被鍵盤壓小）')
ok(!/setVar\(\s*[^)]*innerHeight/.test(vhfCode), '不變式 II：setVar 的引數不含 innerHeight')
ok(/setVar\(cand\)/.test(vhfCode) && /const cand = Math\.round\(vv\.height \|\| 0\)/.test(vhfCode), '不變式 II：唯一寫入值來自 visualViewport.height')
ok(!/addEventListener\('scroll'/.test(vhfCode), "不再綁 visualViewport 'scroll'（iOS 捲到焦點輸入框時的過渡值來源）")
ok(!/scrollTo\(/.test(vhfCode), '移除 scrollTo(0,0) nudge（body 是 overflow:hidden，該行本來就是 no-op）')

// 不變式 III：鍵盤沉默。
ok(/if \(kbUp\(\)\) \{[\s\S]*?\n\s*return\n\s*\}/.test(vhfCode), '不變式 III：kbUp() 為真時 commit 不寫入')
ok(/Date\.now\(\) - kbSince >= KB_LATCH_MAX_MS[^\n]*release\(\)/.test(vhfCode), '鍵盤態有 latch 上限：連續判定超過 KB_LATCH_MAX_MS 就強制 release（--app-h 不可能被永久鎖在偏大的值 → 底部導覽列不會被推出可見區）')
ok(/vv\.offsetTop > 1 && lastFocusAt > 0 && Date\.now\(\) - lastFocusAt < KB_HINT_MS/.test(vhfCode), 'vv.offsetTop 旁證有時效（iOS 收鍵盤後常不歸零，且本站 overflow:hidden 使用者捲不回 0，無時效會變成永久 latch）')
ok(/tag === 'IFRAME' && compressed/.test(vhfCode), 'IFRAME 焦點需搭配「可見區真的被壓縮」才算鍵盤態（Google One-Tap 的焦點可能長駐）')
ok(/try \{ window\.localStorage\.setItem\(FLAG_KEY, urlFlag\) \} catch/.test(vhfCode), '?vpfix 逃生門：URL 參數優先且獨立 try，localStorage 不可寫時本次仍生效')
ok(/document\.addEventListener\('focusout'/.test(vhfCode), '綁 focusout 記錄 lastBlurAt，涵蓋鍵盤收合動畫殘留期')

// meta：把「鍵盤只縮 visual viewport」釘死（對 Android/舊 WebView 有實效，iOS 為 no-op）。
ok(/interactiveWidget: 'resizes-visual'/.test(layoutSrc), "layout.tsx 宣告 interactiveWidget:'resizes-visual'")
ok(!/resizes-content|overlays-content/.test(layoutSrc.replace(/\/\/.*$/gm, '')), '未使用 resizes-content（＝症狀 B 的規格化版本）/overlays-content')

// ── Part 2：行為驗證（載入真實 ViewportHeightFix.tsx）────────────
console.log('\n── Part 2：行為驗證（真實 ViewportHeightFix.tsx，僅換掉 2 行 import）──')

const MOBILE_MQ = /export const MOBILE_MQ = '([^']+)'/.exec(mobileSrc)?.[1]
ok(!!MOBILE_MQ, 'MOBILE_MQ 可自 src/lib/useIsMobile.ts 取得（harness 用真實常數，不自己編一個）')

// 只置換 import 行；若置換數不是 2，代表原始碼 import 結構變了 → 直接失敗，不會靜默測到空氣。
let replaced = 0
const harnessTs = vhfSrc
  .replace(/^import \{ useEffect \} from 'react'$/m, () => { replaced++; return "import { useEffect } from './stub.ts'" })
  .replace(/^import \{ MOBILE_MQ \} from '@\/lib\/useIsMobile'$/m, () => { replaced++; return "import { MOBILE_MQ } from './stub.ts'" })
eq(replaced, 2, 'harness 置換到 2 行 import（其餘原始碼一字未改）')

const dir = mkdtempSync(join(tmpdir(), 'vhf-'))
writeFileSync(join(dir, 'stub.ts'), [
  `export const MOBILE_MQ = ${JSON.stringify(MOBILE_MQ)}`,
  'let cleanup: any = null',
  'export function useEffect(cb: any) { cleanup = cb() ?? null }',
  'export function __takeCleanup() { const c = cleanup; cleanup = null; return c }',
].join('\n'))
writeFileSync(join(dir, 'vhf.ts'), harnessTs.replace(/^'use client'$/m, ''))

const stub = await import(pathToFileURL(join(dir, 'stub.ts')).href)
const { default: ViewportHeightFix } = await import(pathToFileURL(join(dir, 'vhf.ts')).href)

// ── 假 DOM + 假時鐘（在 import 之後才覆蓋全域，避免影響模組載入）──
const realTimers = { setTimeout, clearTimeout, setInterval, clearInterval, now: Date.now }
let now = 1_000_000
let tid = 1
const timers = new Map()
globalThis.setTimeout = (fn, ms = 0) => { const id = tid++; timers.set(id, { fn, at: now + ms, every: 0 }); return id }
globalThis.clearTimeout = (id) => timers.delete(id)
globalThis.setInterval = (fn, ms) => { const id = tid++; timers.set(id, { fn, at: now + ms, every: ms }); return id }
globalThis.clearInterval = (id) => timers.delete(id)
Date.now = () => now
function advance(ms) {
  const end = now + ms
  for (let guard = 0; guard < 1000; guard++) {
    let next = null
    for (const [id, t] of timers) if (t.at <= end && (!next || t.at < next[1].at)) next = [id, t]
    if (!next) break
    const [id, t] = next
    now = t.at
    if (t.every) t.at = now + t.every; else timers.delete(id)
    t.fn()
  }
  now = end
}

let rafQ = []
globalThis.requestAnimationFrame = (cb) => { rafQ.push(cb); return rafQ.length }
globalThis.cancelAnimationFrame = () => {}
const flushRaf = () => { for (let i = 0; i < 5 && rafQ.length; i++) { const q = rafQ; rafQ = []; q.forEach((cb) => cb(0)) } }

function mount({ icb = 800, vvH = 800, innerWidth = 390, screenW = 390, screenH = 844, mobile = true, pathname = '/', search = '', storage = 'ok' } = {}) {
  const props = new Map()
  const listeners = new Map() // `${target}:${type}` -> fn[]
  const add = (target) => (type, fn) => { const k = `${target}:${type}`; listeners.set(k, [...(listeners.get(k) || []), fn]) }
  const rm = (target) => (type, fn) => { const k = `${target}:${type}`; listeners.set(k, (listeners.get(k) || []).filter((f) => f !== fn)) }

  const style = {
    height: '',
    setProperty: (k, v) => props.set(k, v),
    removeProperty: (k) => props.delete(k),
    getPropertyValue: (k) => props.get(k) || '',
  }
  const body = { tagName: 'BODY' }
  const docEl = { style, clientHeight: icb, clientWidth: innerWidth, offsetHeight: 1 }
  const vv = { height: vvH, offsetTop: 0, scale: 1, addEventListener: add('vv'), removeEventListener: rm('vv') }
  const mql = { matches: mobile, addEventListener: add('mql'), removeEventListener: rm('mql') }

  globalThis.document = {
    documentElement: docEl, body, activeElement: null, hidden: false,
    addEventListener: add('doc'), removeEventListener: rm('doc'),
    getElementById: () => null, querySelector: () => null,
  }
  globalThis.window = {
    visualViewport: vv, innerWidth, innerHeight: icb,
    screen: { width: screenW, height: screenH },
    location: { pathname, search },
    // storage:'throw' 模擬 iOS 無痕分頁（setItem 丟 QuotaExceededError）、Safari「封鎖所有 Cookie」
    // 與停用 DOM storage 的 in-app WebView（存取即丟 SecurityError）——逃生門最需要生效的族群。
    localStorage: storage === 'throw'
      ? { getItem: () => { throw new Error('blocked') }, setItem: () => { throw new Error('blocked') } }
      : { getItem: () => null, setItem: () => {} },
    matchMedia: () => mql,
    addEventListener: add('win'), removeEventListener: rm('win'),
  }

  ViewportHeightFix()
  const cleanup = stub.__takeCleanup()
  const fire = (target, type) => { (listeners.get(`${target}:${type}`) || []).forEach((f) => f()); flushRaf() }
  return { appH: () => props.get('--app-h'), docEl, vv, mql, fire, cleanup, listeners, style }
}

// S1 桌機：完全不介入（避免把整個瀏覽器視窗高寫進模擬框）
{
  const e = mount({ mobile: false, icb: 700, vvH: 900 })
  e.fire('win', 'resize')
  eq(e.appH(), undefined, 'S1 桌機（mql 不匹配）：即使 vv 遠大於 ICB 也不寫入 --app-h')
}

// S2 一切正常：vv == ICB → 不需要加高，維持純 CSS
{
  const e = mount({ icb: 800, vvH: 800 })
  eq(e.appH(), undefined, 'S2 正常態（vv == ICB）：不寫入 --app-h，交給 CSS 的 100dvh')
}

// S3 症狀 A 模擬：ICB 卡在過期偏小值、實際可見區較大 → 唯一該加高的情況
{
  const e = mount({ icb: 700, vvH: 844 })
  eq(e.appH(), '844px', 'S3 症狀 A（ICB 過期 700、vv 844）：加高到 844px（不變式 II 的紅利）')
}

// S4 症狀 B 核心：鍵盤把 vv 壓小 → 既不得寫入更小值，也不得釋放
{
  const e = mount({ icb: 700, vvH: 844 })
  eq(e.appH(), '844px', 'S4 前置：已加高到 844px')
  globalThis.document.activeElement = { tagName: 'INPUT', type: 'number', isContentEditable: false }
  e.docEl.clientHeight = 800
  e.vv.height = 400 // 鍵盤 + 輸入輔助列
  e.fire('vv', 'resize')
  advance(5000) // 讓 1.5s 保險輪詢跑好幾輪
  eq(e.appH(), '844px', 'S4 鍵盤彈出（vv 844→400、activeElement=input[number]）：--app-h 完全不動（不變式 III）')
}

// S4b 鍵盤但焦點不在輸入框（iOS 有時 activeElement 不變）：靠數值旁證仍判定鍵盤態
{
  const e = mount({ icb: 800, vvH: 800 })
  e.vv.height = 500
  e.fire('vv', 'resize')
  advance(5000)
  eq(e.appH(), undefined, 'S4b vv 比 ICB 小 60px 以上：判定鍵盤態，不寫入任何值（更不會寫入 500px）')
}

// S5 vv 永遠不會讓版面變矮：即使 vv < ICB 也不寫
{
  const e = mount({ icb: 900, vvH: 600 })
  advance(5000)
  eq(e.appH(), undefined, 'S5 vv(600) < ICB(900)：不寫入（--app-h 只有「加高」與「不存在」兩種狀態）')
}

// S6 自我恢復：ICB 恢復正常後，連續 3 次 ≥900ms 判定不需加高 → 整個釋放
{
  const e = mount({ icb: 700, vvH: 844 })
  eq(e.appH(), '844px', 'S6 前置：已加高')
  e.docEl.clientHeight = 844 // ICB 自己恢復了
  advance(1500); eq(e.appH(), '844px', 'S6 第 1 次 calm：尚未釋放')
  advance(1500); eq(e.appH(), '844px', 'S6 第 2 次 calm：尚未釋放')
  advance(1500); eq(e.appH(), undefined, 'S6 第 3 次 calm（≥900ms）：釋放 --app-h，回到純 CSS 100dvh')
}

// S7 荒謬值護欄：vv 大於實體螢幕長邊 → 拒寫（否則會把底部導覽列推到工具列底下）
{
  const e = mount({ icb: 700, vvH: 2000, screenW: 390, screenH: 844 })
  eq(e.appH(), undefined, 'S7 vv(2000) > 螢幕長邊(844)：拒絕寫入')
}

// S8 量測失敗護欄
{
  const e = mount({ icb: 700, vvH: 100 })
  eq(e.appH(), undefined, 'S8 vv(100) < MIN_SANE_PX(240)：視為量測失敗，拒絕寫入')
}

// S9 轉向/改寬：基準整組作廢並釋放
{
  const e = mount({ icb: 700, vvH: 844 })
  eq(e.appH(), '844px', 'S9 前置：已加高')
  globalThis.window.innerWidth = 844 // 轉橫
  e.docEl.clientHeight = 390
  e.vv.height = 390
  e.fire('win', 'orientationchange')
  advance(1200)
  eq(e.appH(), undefined, 'S9 轉向（innerWidth 變）：立即釋放，不把直立值帶到橫向')
}

// S10 /admin 與 vv 缺席：完全不介入
{
  const e = mount({ icb: 700, vvH: 844, pathname: '/admin/overview' })
  eq(e.appH(), undefined, 'S10 後台路徑：不介入（後台走 100vh，不吃 --app-h）')
}

// S11 綁定面：確認沒有偷偷綁回 vv 的 scroll
{
  const e = mount({ icb: 800, vvH: 800 })
  const vvTypes = [...e.listeners.keys()].filter((k) => k.startsWith('vv:') && e.listeners.get(k).length)
  eq(vvTypes.join(','), 'vv:resize', "S11 visualViewport 只綁 resize（不綁 scroll）")
  e.cleanup()
  eq(e.appH(), undefined, 'S12 卸載：清除 --app-h')
  eq(e.style.height, '', 'S12 卸載：清除 <html> 的 inline height（nudge 殘留保險）')
}

// ── S13–S16：kbUp() 不得變成永久 latch（v1.1.676 修正） ──
// 舊版 commit() 的 `if (kbUp()) return` 同時擋住寫入與釋放，而 kbUp() 有兩個沒有時效的訊號
// （vv.offsetTop 不歸零、iframe/select 長駐焦點）→ --app-h 會被鎖在偏大的值，底部導覽列被推出
// 可見區、又因 body{overflow:hidden} 捲不到。以下四組把每條逃生路徑都釘住。

// S13 旁證卡住：iOS 收鍵盤後 visualViewport.offsetTop 常不歸零（本站 overflow:hidden，使用者也捲不回 0）
{
  const e = mount({ icb: 700, vvH: 844 })
  eq(e.appH(), '844px', 'S13 前置：已加高到 844px')
  globalThis.document.activeElement = { tagName: 'INPUT', type: 'text', isContentEditable: false }
  e.fire('doc', 'focusin')
  e.docEl.clientHeight = 800; e.vv.height = 420; e.vv.offsetTop = 40
  e.fire('vv', 'resize')
  globalThis.document.activeElement = null
  e.fire('doc', 'focusout')
  e.vv.height = 800 // 鍵盤收了，但 offsetTop 仍停在 40
  e.fire('vv', 'resize')
  advance(30000)
  eq(e.appH(), undefined, 'S13 offsetTop 卡在 40 但已無焦點：旁證過期 → calm 正常釋放（舊版永久鎖在 844px）')
}

// S14 iframe 長駐焦點（Google One-Tap／YouTube）：可見區沒被壓縮就不算鍵盤態
{
  const e = mount({ icb: 700, vvH: 844 })
  eq(e.appH(), '844px', 'S14 前置：已加高')
  globalThis.document.activeElement = { tagName: 'IFRAME', isContentEditable: false }
  e.docEl.clientHeight = 844
  advance(8000)
  eq(e.appH(), undefined, 'S14 IFRAME 取得焦點但 vv 未被壓縮：不判鍵盤態，calm 釋放照常運作')
}

// S15 真的在長時間打字：先沉默（不變式 III），超過 KB_LATCH_MAX_MS 才強制釋放
{
  const e = mount({ icb: 700, vvH: 844 })
  eq(e.appH(), '844px', 'S15 前置：已加高')
  globalThis.document.activeElement = { tagName: 'INPUT', type: 'text', isContentEditable: false }
  e.docEl.clientHeight = 800; e.vv.height = 400
  e.fire('vv', 'resize')
  advance(9000)
  eq(e.appH(), '844px', 'S15 打字中 9 秒：--app-h 完全不動（鍵盤期間不得改變版面）')
  advance(6000)
  eq(e.appH(), undefined, 'S15 連續鍵盤態 >12s：強制釋放回純 CSS 100dvh（釋放只會變回 dvh，不可能造成症狀 B）')
}

// S16 逃生門：localStorage 不可寫時 ?vpfix=off 仍必須當場生效
{
  const e = mount({ icb: 700, vvH: 844, search: '?vpfix=off', storage: 'throw' })
  eq(e.appH(), undefined, 'S16 ?vpfix=off + localStorage 丟例外：仍然完全停用')
  const bound = [...e.listeners.keys()].filter((k) => e.listeners.get(k).length)
  eq(bound.length, 0, 'S16 停用時不註冊任何監聽器（真的關掉，不是關一半）')
}
{
  const e = mount({ icb: 700, vvH: 844, storage: 'throw' })
  eq(e.appH(), '844px', 'S16b localStorage 丟例外且無 URL 參數：維持預設 on，加高照常運作')
}

Object.assign(globalThis, realTimers)
Date.now = realTimers.now
rmSync(dir, { recursive: true, force: true })

console.log(`\n${fail === 0 ? 'ALL PASS' : 'FAILED'}  pass=${pass} fail=${fail}`)
process.exit(fail === 0 ? 0 : 1)
