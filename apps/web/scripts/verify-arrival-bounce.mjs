// 驗證到站彈跳頁純函式庫（src/lib/arrivalBounce.ts）：decideBounce 的判定順序／防迴圈三道保險、
// parseBounceDelay 的護欄、bounceHtml 的跳脫與結構。純 Node 執行，不需瀏覽器、不需 edge runtime。
// 執行位置：apps/web 下 `node scripts/verify-arrival-bounce.mjs`
//
// 原始碼是 TS（含型別標註），用 typescript 套件（devDependency）的 ts.transpileModule 轉成純 JS、
// 寫進 os.tmpdir() 的暫存檔再 import()——lib 檔本身無任何 import（見檔頭註解：middleware 跑 edge
// runtime，且要能被本腳本這樣直接單元測試），transpileModule 逐檔轉譯不需要型別檢查也能正確運作。
import { readFileSync, writeFileSync, mkdtempSync, rmSync } from 'node:fs'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import ts from 'typescript'

let pass = 0, fail = 0
function ok(cond, label, extra) {
  if (cond) { pass++; console.log(`PASS ${label}`) }
  else { fail++; console.log(`FAIL ${label}${extra ? `\n  ${extra}` : ''}`) }
}
function eq(actual, expected, label) {
  const a = JSON.stringify(actual), e = JSON.stringify(expected)
  ok(a === e, label, `actual: ${a}  expected: ${e}`)
}

const srcPath = fileURLToPath(new URL('../src/lib/arrivalBounce.ts', import.meta.url))
const src = readFileSync(srcPath, 'utf8')
const transpiled = ts.transpileModule(src, {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2020 },
})

const dir = mkdtempSync(join(tmpdir(), 'arrival-bounce-'))
const outFile = join(dir, 'arrivalBounce.mjs')
writeFileSync(outFile, transpiled.outputText)
const { decideBounce, parseBounceDelay, bounceHtml, BOUNCE_DELAY_MS } = await import(pathToFileURL(outFile).href)

// ── 假 Headers（大小寫不敏感，模擬 Next 的 req.headers.get）──
function headers(map) {
  const norm = new Map(Object.entries(map).filter(([, v]) => v != null).map(([k, v]) => [k.toLowerCase(), v]))
  return { get: (name) => (norm.has(name.toLowerCase()) ? norm.get(name.toLowerCase()) : null) }
}
function url(pathname, search = '') { return { pathname, search } }

const IPHONE_SAFARI = 'Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Mobile/15E148 Safari/604.1'
const IPHONE_BARE_REAL_OS = 'Mozilla/5.0 (iPhone; CPU iPhone OS 26_6_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148' // 第三輪真機（條碼掃描器→Safari）
const IPHONE_STANDALONE = 'Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148' // PWA standalone：無 Safari/、無 Version/
const IPHONE_REAL_CRIOS = 'Mozilla/5.0 (iPhone; CPU iPhone OS 26_6_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/152.0.7977.64 Mobile/15E148 Safari/604.1' // 第四輪真機面板 ua 全文
const IPHONE_CRIOS = 'Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/125.0.6422.80 Mobile/15E148 Safari/604.1'
const IPHONE_LINE = IPHONE_SAFARI + ' Line/14.0.0'
const IPHONE_FBAN = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1 [FBAN/FBIOS;FBAV/456.0.0.36.108;]'
const ANDROID_CHROME = 'Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Mobile Safari/537.36'
const MAC_SAFARI = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15'
const IPHONE_GOOGLEBOT = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)'
const IPHONE_FBEXT = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1 (compatible; facebookexternalhit/1.1; +http://www.facebook.com/externalhit_uatext.php)'

const BASE_HEADERS = {
  'sec-fetch-site': 'none',
  'sec-fetch-mode': 'navigate',
  'sec-fetch-dest': 'document',
  accept: 'text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8',
  'user-agent': IPHONE_SAFARI,
}

function decide(overrideHeaders = {}, u = url('/', ''), method = 'GET') {
  return decideBounce(headers({ ...BASE_HEADERS, ...overrideHeaders }), u, method)
}

console.log('── decideBounce ──')

eq(decide(), { bounce: true }, 'QR 到站基準情境（iPhone Safari, sec-fetch-site:none, 無 referer/cookie）→ bounce')

// v758：裸 WebKit 形狀的 UA（無 Safari/、無 Version/、OS 未凍結）正是真機會發病的「條碼掃描器→Safari」到站文件，必須彈跳
eq(decide({ 'user-agent': IPHONE_STANDALONE }), { bounce: true }, '裸 WebKit UA（無 Safari/、無 Version/）→ bounce（v758：這才是真正的病人）')
eq(decide({ 'user-agent': IPHONE_BARE_REAL_OS }), { bounce: true }, '真機 UA 形狀（iPhone OS 26_6_1、無 Safari/）→ bounce')
eq(decide({ 'user-agent': IPHONE_STANDALONE, cookie: 'dor_pwa=1' }), { bounce: false, reason: 'pwa' }, 'PWA standalone（cookie dor_pwa=1）→ pwa')
eq(decide({ cookie: 'a=1; dor_pwa=1; b=2' }), { bounce: false, reason: 'pwa' }, 'cookie dor_pwa=1 夾在中間 → pwa')
eq(decide({ cookie: 'dor_pwa=10' }), { bounce: true }, 'cookie dor_pwa=10（值不是 1）不算 → bounce')
// v759：真機病人就是 Chrome iOS；iPhone 上所有瀏覽器都是 WebKit，一律彈跳（只排 bot／PWA）
eq(decide({ 'user-agent': IPHONE_CRIOS }), { bounce: true }, 'CriOS（iOS Chrome）UA → bounce（v759：真機病人）')
eq(decide({ 'user-agent': IPHONE_REAL_CRIOS }), { bounce: true }, '第四輪真機 UA 全文（CriOS/152、OS 26_6_1）→ bounce')
eq(decide({ 'user-agent': IPHONE_LINE }), { bounce: true }, 'LINE 內建瀏覽器 UA → bounce（多一趟 1KB，無害）')
eq(decide({ 'user-agent': IPHONE_FBAN }), { bounce: true }, 'FBAN（FB 內建瀏覽器）UA → bounce')
eq(decide({ 'user-agent': ANDROID_CHROME }), { bounce: false, reason: 'not-iphone' }, 'Android Chrome UA → not-iphone')
eq(decide({ 'user-agent': MAC_SAFARI }), { bounce: false, reason: 'not-iphone' }, 'Mac Safari UA → not-iphone')

{
  const h = headers({ 'user-agent': IPHONE_SAFARI, accept: BASE_HEADERS.accept }) // 完全缺三個 Sec-Fetch-*
  eq(decideBounce(h, url('/'), 'GET'), { bounce: false, reason: 'no-sec-fetch' }, '完全缺 Sec-Fetch-* 標頭 → no-sec-fetch（舊 Safari <16.4／bot／monitor，交給頁內 boot script 備援）')
}
{
  const h = headers({ ...BASE_HEADERS, 'sec-fetch-dest': null }) // 只缺 sec-fetch-dest
  eq(decideBounce(h, url('/'), 'GET'), { bounce: false, reason: 'no-sec-fetch' }, '只缺 Sec-Fetch-Dest 一個 → 仍算 no-sec-fetch')
}
eq(decide({ 'sec-fetch-site': 'same-origin' }), { bounce: false, reason: 'site' }, 'Sec-Fetch-Site: same-origin（我方彈跳頁 replace 後的第二次請求）→ site')
eq(decide({ 'sec-fetch-site': 'cross-site' }), { bounce: false, reason: 'site' }, 'Sec-Fetch-Site: cross-site → site')
eq(decide({ 'sec-fetch-mode': 'cors' }), { bounce: false, reason: 'mode' }, 'Sec-Fetch-Mode: cors → mode')
eq(decide({ 'sec-fetch-dest': 'iframe' }), { bounce: false, reason: 'dest' }, 'Sec-Fetch-Dest: iframe → dest')
eq(decide({}, url('/'), 'POST'), { bounce: false, reason: 'method' }, 'POST → method')
eq(decide({}, url('/'), 'HEAD'), { bounce: false, reason: 'method' }, 'HEAD → method')
eq(decide({ accept: null }), { bounce: false, reason: 'accept' }, 'Accept 缺失 → accept')
eq(decide({ accept: '*/*' }), { bounce: false, reason: 'accept' }, "Accept: */*（不含 text/html）→ accept")
eq(decide({ referer: 'https://www.dor.tw/' }), { bounce: false, reason: 'referer' }, 'Referer 存在（即使同站）→ referer（防迴圈保險②：Sec-Fetch-Site:none 規範上不帶 Referer）')
eq(decide({ cookie: 'dor_b=1' }), { bounce: false, reason: 'cookie' }, 'cookie dor_b=1（單獨）→ cookie（防迴圈保險③）')
eq(decide({ cookie: 'theme=dark; dor_b=1; sid=abc' }), { bounce: false, reason: 'cookie' }, 'cookie dor_b=1（夾在其他 cookie 中間）→ cookie')
eq(decide({}, url('/', '?vpfix=off')), { bounce: false, reason: 'opt-out' }, '?vpfix=off → opt-out')
eq(decide({}, url('/', '?noreload=1')), { bounce: false, reason: 'opt-out' }, '?noreload=1 → opt-out')
eq(decide({}, url('/race/x', '?code=abc123&state=xyz')), { bounce: false, reason: 'auth-url' }, '?code= 授權回跳網址（/auth/ 以外的路徑）→ auth-url（一次性授權碼禁不起被彈跳頁多消費一次網址）')
// Google 登入整頁導轉（google_login_ux_mode='redirect'，2026-09-04）：/auth/ 路徑一律不彈跳，這道判斷插在
// auth-url 的 query-string 檢查之前（見 arrivalBounce.ts），所以 /auth/ 底下即使也帶著 ?code= 也會先命中
// auth-path——這正是要的：credential 可能藏在 URL fragment（伺服器端本來就看不到，query-string 判斷不到），
// 路徑前綴是唯一保證涵蓋得到的判斷。
eq(decide({}, url('/auth/callback', '?code=abc123&state=xyz')), { bounce: false, reason: 'auth-path' }, '/auth/callback（/auth/ 前綴，即使帶 ?code=）→ auth-path 優先於 auth-url')
eq(decide({}, url('/auth/google/complete', '')), { bounce: false, reason: 'auth-path' }, '/auth/google/complete（無 query，credential 在 fragment）→ auth-path')
eq(decide({}, url('/auth/google/callback', '')), { bounce: false, reason: 'auth-path' }, '/auth/google/callback → auth-path')
// 完成頁是 Google 跨站 POST → 303 而來的導覽：Sec-Fetch-Site 不會是 none。auth-path 必須排在 site 檢查之前，
// 否則永遠輪不到（審查以 decideBounce 實測抓到）。
eq(decide({ 'sec-fetch-site': 'same-origin' }, url('/auth/google/complete', '')), { bounce: false, reason: 'auth-path' }, '/auth/google/complete 帶 Sec-Fetch-Site: same-origin → 仍是 auth-path（不是 site）')
eq(decide({ 'sec-fetch-site': 'cross-site', referer: 'https://accounts.google.com/' }, url('/auth/google/complete', '')), { bounce: false, reason: 'auth-path' }, '/auth/google/complete 跨站導覽（referer google）→ auth-path')
eq(decide({}, url('/authorize', '')), { bounce: true }, '/authorize（非 /auth/ 前綴、只是字首相似）→ 仍照常彈跳')
eq(decide({ 'user-agent': IPHONE_GOOGLEBOT }), { bounce: false, reason: 'bot' }, 'Googlebot UA（即使宣稱 iPhone Safari）→ bot')
eq(decide({ 'user-agent': IPHONE_FBEXT }), { bounce: false, reason: 'bot' }, 'facebookexternalhit UA → bot')

console.log('\n── parseBounceDelay ──')
eq(BOUNCE_DELAY_MS, 0, 'BOUNCE_DELAY_MS 預設 0（v760：真機 0ms 已證實）')
eq(parseBounceDelay(''), BOUNCE_DELAY_MS, "'' → BOUNCE_DELAY_MS")
eq(parseBounceDelay('?bdelay=0'), 0, '?bdelay=0 → 0')
eq(parseBounceDelay('?bdelay=150'), 150, '?bdelay=150 → 150')
eq(parseBounceDelay('?bdelay=9999'), BOUNCE_DELAY_MS, '?bdelay=9999（超出 0..3000）→ 回退預設')
eq(parseBounceDelay('?bdelay=abc'), BOUNCE_DELAY_MS, '?bdelay=abc（非數字）→ 回退預設')

console.log('\n── bounceHtml ──')

function scriptBodies(html) {
  return [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map((m) => m[1])
}

{
  const html = bounceHtml({ bg: '#FBF4E9', fg: '#6b5a3e', target: '/', delayMs: 600 })
  ok(html.includes('document.cookie="dor_b=1'), 'bounceHtml：含種 cookie dor_b=1 的那行')
  ok(html.includes('if(navigator.standalone===true){document.cookie="dor_pwa=1;Max-Age=31536000'), 'bounceHtml：standalone 時種 cookie dor_pwa=1（一年）')
  ok(html.includes('location.replace('), 'bounceHtml：含 location.replace(')
}

{
  const html = bounceHtml({ bg: '#FBF4E9', fg: '#6b5a3e', target: '/race/x?a=1&b=2', delayMs: 600 })
  ok(html.includes('content="4;url=/race/x?a=1&amp;b=2"'), 'bounceHtml：meta refresh 的 target 正確 HTML escape（& → &amp;）', html)
}

for (const bad of ['//evil.com/x', 'javascript:alert(1)']) {
  const html = bounceHtml({ bg: '#FBF4E9', fg: '#6b5a3e', target: bad, delayMs: 600 })
  ok(html.includes('content="4;url=/"'), `bounceHtml：target=${JSON.stringify(bad)} 不合法（非 "/" 開頭或是 "//"）→ 回退 "/"`, html)
}

{
  const html = bounceHtml({ bg: '#FBF4E9', fg: '#6b5a3e', target: '/', delayMs: 0 })
  const headSection = html.slice(0, html.indexOf('</head>'))
  const bodySection = html.slice(html.indexOf('<body>'))
  ok(headSection.includes('location.replace(u)') && !headSection.includes('setTimeout'), 'delayMs=0：<head> 內同步呼叫 location.replace，沒有 setTimeout')
  ok(!bodySection.includes('<script>'), 'delayMs=0：<body> 內沒有第二個 <script>（避免重複執行）')
}

{
  const html = bounceHtml({ bg: '#FBF4E9', fg: '#6b5a3e', target: '/', delayMs: 600 })
  const headSection = html.slice(0, html.indexOf('</head>'))
  const bodySection = html.slice(html.indexOf('<body>'))
  ok(!headSection.includes('<script>'), 'delayMs=600：<head> 內沒有 <script>（延遲版本改放 body 尾端）')
  ok(bodySection.includes('setTimeout(function(){location.replace(u)},600)'), 'delayMs=600：body 用 setTimeout(...,600) 延遲導覽')
}

{
  const html = bounceHtml({ bg: '#FBF4E9', fg: '#6b5a3e', target: '/', delayMs: 600 })
  ok(html.includes('location.hash') && html.includes('"_b=1"'), 'bounceHtml：含 hash 差異化處理（純 fragment 差異不構成新文件導覽，需靠查詢字串製造差異）')
}

{
  const html = bounceHtml({ bg: '#FBF4E9', fg: '#6b5a3e', target: '/x</script><script>alert(1)</script>', delayMs: 600 })
  const bodies = scriptBodies(html)
  ok(bodies.length > 0 && bodies.every((b) => !b.includes('</script')), 'bounceHtml：即使 target 含 "</script"，<script> 內容也不含 </script（target 從未塞進 JS，只在 runtime 用 location 取值）')
}

{
  const html = bounceHtml({ bg: '#FBF4E9', fg: '#6b5a3e', target: '/', delayMs: 600 })
  const bytes = Buffer.byteLength(html, 'utf8')
  ok(bytes < 2000, `bounceHtml：大小 < 2000 bytes（實得 ${bytes}）`)
}

{
  const html = bounceHtml({ bg: '#FBF4E9', fg: '#6b5a3e', target: '/', delayMs: 600 })
  const titleMatch = /<title>([\s\S]*?)<\/title>/.exec(html)
  ok(!!titleMatch, 'bounceHtml：有 <title>')
  const withoutTitle = titleMatch ? html.replace(titleMatch[0], '') : html
  const nonAscii = [...withoutTitle].filter((ch) => ch.charCodeAt(0) > 127)
  ok(nonAscii.length === 0, `bounceHtml：<title> 以外全部 ASCII（實得非 ASCII 字元：${JSON.stringify(nonAscii.join(''))}）`)
}

rmSync(dir, { recursive: true, force: true })

console.log(`\n${fail === 0 ? 'ALL PASS' : 'FAILED'}  pass=${pass} fail=${fail}`)
process.exit(fail === 0 ? 0 : 1)
