// 到站彈跳頁（症狀 A 主治療，v757）：純函式庫，無 Next/React import——src/middleware.ts 跑在 edge runtime，
// 且本檔要能被 scripts/verify-arrival-bounce.mjs 用純 Node 單元測試（不開瀏覽器、不跑 edge runtime）直接驗證。
//
// 背景（真機二分法已確定，勿重查）：iOS Safari 從站外到站（QR 掃碼／訊息連結／輸入網址）的第一份文件，
// 合成器把 root 圖層上移 lvh−svh（~70pt），所有 JS 量測值卻正確；站內任何處置（zoom/scroll/viewport-fit/
// resize/theme-color…）都治不了，唯有「同分頁的第二次導覽」（reload，或一次極短的彈跳頁 location.replace）
// 能治好。過去做法是整個 app 先載入、蓋白幕、再 reload——等於載入兩次完整 app。這裡改成：符合到站特徵的
// 請求由 middleware 攔截，直接回一份 ~1KB 的品牌色彈跳頁（不含 app 的任何程式碼/資料），600ms 後
// location.replace() 同網址；那次 replace 帶著 Sec-Fetch-Site: same-origin，decideBounce 判定不再彈跳、
// middleware 放行給真正的 app——使用者只看到「彈跳頁 → app」，而非「白幕 app → reload → app」。
//
// 三道獨立防迴圈保險（缺一都可能彈成無窮迴圈）：
//   ① Sec-Fetch-Site 必須是 'none'（真到站才有；同源 replace 是 'same-origin'，跨站連結導覽的一般情況
//      也不是 'none'——'none' 專指「使用者主動輸網址／書籤／OS 喚起」這類無發起頁的導覽）
//   ② Referer 必須不存在（Sec-Fetch-Site:none 的導覽依規範不帶 Referer；只要出現 Referer，代表這不是
//      真到站，這條和①在大多數瀏覽器等價，但②在不送 Sec-Fetch-*的舊環境仍可能單獨擋下一些情況，故保留
//      當第二道而非合併——寧可多一道判斷、不可只靠一道）
//   ③ 彈跳頁自己種的 cookie dor_b=1（Max-Age=5s）：即使①②因某些代理/隱私模式失真，這道是最後防線。
//      刻意只留 5 秒：cookie 跨分頁共享，若同一支手機 5 秒內再掃一張 QR，第二次到站會被這道擋掉不彈跳——
//      那份文件會退回 layout.tsx 開機腳本的舊路徑（到站 → 白幕 → reload）仍能治好，只是慢一點；窗口越短越好。
//
// PWA standalone 的排除靠 cookie dor_pwa=1（一年）而非 UA：iOS 主畫面 web app 有獨立 cookie jar，這顆 cookie
// 只會在 standalone 情境被種下（layout.tsx 開機腳本、以及彈跳頁自己在 navigator.standalone===true 時）——
// 首次啟動會被彈一次（多一趟 1KB 請求），之後都放行。為什麼不能用 UA 排除，見 decideBounce 內 v758 註解。

export const BOUNCE_DELAY_MS = 600

export type BounceDecision = { bounce: true } | { bounce: false; reason: string }

const BOT_RE = /bot|crawl|spider|slurp|facebookexternalhit|preview|headless|lighthouse|pingdom|hetrix|uptime/i
const NOT_SAFARI_UA_RE = /CriOS|FxiOS|EdgiOS|OPiOS|Line[/]|FBAN|FBAV|Instagram|MicroMessenger|DuckDuckGo/

export function decideBounce(
  h: { get(name: string): string | null },
  url: { pathname: string; search: string },
  method: string
): BounceDecision {
  if (method !== 'GET') return { bounce: false, reason: 'method' }

  const site = h.get('sec-fetch-site')
  const mode = h.get('sec-fetch-mode')
  const dest = h.get('sec-fetch-dest')
  if (site == null || mode == null || dest == null) return { bounce: false, reason: 'no-sec-fetch' }
  if (site !== 'none') return { bounce: false, reason: 'site' }
  if (mode !== 'navigate') return { bounce: false, reason: 'mode' }
  if (dest !== 'document') return { bounce: false, reason: 'dest' }

  const accept = h.get('accept')
  if (!accept || accept.indexOf('text/html') === -1) return { bounce: false, reason: 'accept' }

  const ua = h.get('user-agent') || ''
  if (!/iPhone|iPod/.test(ua)) return { bounce: false, reason: 'not-iphone' }
  // 這段刻意跟 layout.tsx 開機腳本的判定式一致（同一份「這是不是會發病的 iPhone WebKit」邏輯兩處各留一份，
  // 一份跑 edge runtime、一份跑瀏覽器，無法共用同一個檔案——但語意必須對齊，改一邊記得改另一邊）。
  //
  // ⚠️ v758 真機證據（第三輪掃碼，iOS 26.6.1，勿再要求 Safari/ 或 Version/）：從「條碼掃描器」喚起的 Safari
  // 分頁，navigator.userAgent 回報真實 OS 版本 26_6_1、且沒有 Safari/ 也沒有 Version/——Safari 26 本體的 UA
  // 依 Apple 新規把 OS token 凍結在 18_x（nielsleenheer.com「The User-Agent string of Safari on iOS 26」），
  // 會回報真實版本的只有非 Safari 的 WebKit 情境。也就是說，會發病的那份文件，UA 長得跟「裸 WKWebView／
  // PWA standalone」一模一樣；v757 要求 Safari/+Version/ 的閘門把真正的病人全擋在外面（面板 ar 顯示
  // skip:not-safari、A/B 卡全跑版；靜態 C/D 卡 0ms/150ms 彈跳皆正常＝療法本身沒錯，是閘門錯）。
  // 因此只排除「明確標示自己是別家瀏覽器／in-app 瀏覽器」的 UA；PWA standalone 改由 cookie dor_pwa=1 排除。
  // 其他裸 UA 的 in-app WebView（Gmail/Twitter 等）會被彈一次——只多一趟 1KB 請求，無害。
  if (NOT_SAFARI_UA_RE.test(ua)) return { bounce: false, reason: 'not-safari' }
  if (BOT_RE.test(ua)) return { bounce: false, reason: 'bot' }

  // Sec-Fetch-Site:none 的導覽依規範不帶 Referer；出現 Referer 就代表不是「真到站」（見上方保險②）。
  if (h.get('referer') != null) return { bounce: false, reason: 'referer' }

  const cookie = h.get('cookie') || ''
  if (/(^|;\s*)dor_b=1(;|$)/.test(cookie)) return { bounce: false, reason: 'cookie' }
  if (/(^|;\s*)dor_pwa=1(;|$)/.test(cookie)) return { bounce: false, reason: 'pwa' }

  if (/vpfix=off|noreload=1/.test(url.search)) return { bounce: false, reason: 'opt-out' }
  if (/[?&](code|state|token|access_token|id_token)=/i.test(url.search)) return { bounce: false, reason: 'auth-url' }

  return { bounce: true }
}

export function parseBounceDelay(search: string): number {
  const m = /[?&]bdelay=(\d+)(?:&|$)/.exec(search)
  if (!m) return BOUNCE_DELAY_MS
  const n = parseInt(m[1], 10)
  if (!Number.isFinite(n) || n < 0 || n > 3000) return BOUNCE_DELAY_MS
  return n
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

function safeTarget(target: string): string {
  if (typeof target !== 'string') return '/'
  if (!target.startsWith('/') || target.startsWith('//')) return '/'
  return target
}

export function bounceHtml(o: { bg: string; fg: string; target: string; delayMs: number }): string {
  const bg = o.bg
  const fg = o.fg
  const target = safeTarget(o.target)
  const targetEsc = escapeHtml(target)
  const delayMs = Number.isFinite(o.delayMs) && o.delayMs >= 0 && o.delayMs <= 3000 ? Math.round(o.delayMs) : BOUNCE_DELAY_MS

  // ES5、包 try/catch：中途任何一行丟例外都不能讓整支腳本停擺（否則連 meta refresh 都可能因為使用者已經
  // 手動點過而顯得多餘，但那是唯一的 no-JS/JS-fail 後備，寧可留著）。
  const scriptBody =
    'try{' +
    'document.cookie="dor_b=1;Max-Age=5;Path=/;SameSite=Lax";' +
    'if(navigator.standalone===true){document.cookie="dor_pwa=1;Max-Age=31536000;Path=/;SameSite=Lax"}' +
    'var u=location.href;' +
    'if(location.hash){u=location.pathname+location.search+(location.search?"&":"?")+"_b=1"+location.hash}' +
    (delayMs === 0
      ? 'location.replace(u);'
      : 'setTimeout(function(){location.replace(u)},' + delayMs + ');')
    + '}catch(e){}'

  const headScript = delayMs === 0 ? `<script>${scriptBody}</script>` : ''
  const bodyScript = delayMs === 0 ? '' : `<script>${scriptBody}</script>`

  return `<!doctype html>
<html lang="zh-TW">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no, viewport-fit=cover">
<meta name="theme-color" content="${bg}">
<meta name="robots" content="noindex">
<meta http-equiv="refresh" content="4;url=${targetEsc}">
<title>DOR｜城市探索</title>
<style>
html,body{margin:0;height:100%;background:${bg};color:${fg};font-family:-apple-system,BlinkMacSystemFont,"PingFang TC",sans-serif}
.b{position:fixed;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center}
.s{width:28px;height:28px;border:3px solid ${fg};border-top-color:transparent;border-radius:50%}
@media (prefers-reduced-motion: no-preference){.s{animation:r .8s linear infinite}}
@keyframes r{to{transform:rotate(360deg)}}
.w{margin-top:14px;font-size:13px;letter-spacing:.12em;opacity:.7}
</style>
${headScript}
</head>
<body>
<div class="b"><div class="s"></div><div class="w">DOR</div></div>
${bodyScript}
</body>
</html>`
}
