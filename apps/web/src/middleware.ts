import { NextResponse, type NextFetchEvent, type NextRequest } from 'next/server'
import { decideBounce, parseBounceDelay, bounceHtml } from '@/lib/arrivalBounce'
import { veilColorsOf } from '@/lib/skinColors'

// 到站彈跳頁中介層（症狀 A 主治療，v757）。
//
// 【症狀 A 是什麼】iOS Safari（真機）從站外到站（QR 掃碼／訊息連結／輸入網址）時，第一份文件的 root 圖層被
// 合成器上移 lvh−svh（約 70pt 的狀態列＋網址列高度）——所有 JS 量測值（innerHeight/visualViewport/
// documentElement.clientHeight）都回報正確值，只有實際繪製位置錯，量測式偵測在數學上不可能成立。
//
// 【為什麼「第二次導覽」是唯一的治法】第一輪排查（10 種 CSS 模型：dvh/svh/lvh、fixed/absolute、
// transform 重建、視覺視口 API…）全部重現了病態、沒有一種能靠 CSS 單邊避開；第二輪排查（頁內所有 JS 槓桿：
// zoom/scroll/theme-color/resize/viewport-fit 切換）全部治不好——已經跑起來的合成器圖層錯位，站內任何
// 處置都救不回。唯一實測有效的是「同一分頁內再導覽一次」：無論是整份 reload，或極短的 location.replace，
// 新文件的合成器會重新對齊，第二次繪製必定正確。
//
// 【舊做法 vs 這裡】v752–v756：完整載入 app → 用 CSS 偽元素蓋白幕 → 0.4s 後 location.reload()（見
// src/app/layout.tsx 的 bootJs）。代價是使用者的裝置實際下載/解析/執行了兩次完整 app（bundle、hydration、
// 首屏資料 fetch 都跑兩次，只是第一次被蓋住看不到）。這裡把「觸發那次額外導覽」的判斷提前到 middleware：
// 符合到站特徵的請求，直接回一份不含任何 app 程式碼的 ~1KB 彈跳頁（品牌色底＋轉圈），delayMs 後
// location.replace() 同一個網址；那次 replace 帶 Sec-Fetch-Site: same-origin，decideBounce() 判定不再
// 彈跳、直接放行給真正的 app —— 使用者只多等 delayMs，且完全省下第一次「被蓋住」的完整 app 載入。
//
// 【三道獨立防迴圈保險】（decideBounce 的判定順序，任何一道失真都還有下一道兜底）
//   ① Sec-Fetch-Site 必須是 'none'：真正的到站（使用者輸網址/QR/書籤/OS 喚起）才會是 none；
//      我方彈跳頁的 location.replace() 是同源導覽 → same-origin，天然被這道擋下、不會遞迴彈跳。
//   ② Referer 不得存在：Sec-Fetch-Site:none 的規範定義就是不帶 Referer；若①因故失真（極舊瀏覽器沒有
//      Sec-Fetch-* 標頭時①根本量不到，直接走 no-sec-fetch 放行），這道仍能單獨擋下大多數重放。
//   ③ cookie dor_b=1：彈跳頁自己在導覽前種下（Max-Age=10s），是最後一道防線——即使①②都因為某些
//      隱私代理/預抓取工具而失真，這道仍能認出「這份文件是我方剛發的」。
//
// 【?bdelay= 調參】測試/除錯用；0 代表把 location.replace 移到 <head> 同步執行（沒有轉圈可看，最快）、
// 非 0 則在 <body> 尾端 setTimeout（可看到轉圈，避免使用者覺得「白畫面沒反應」）。正式環境不需要帶這個參數，
// 預設 BOUNCE_DELAY_MS 自 v760 起為 0（真機 0ms 已證實可治、使用者無感）；600 是舊預設，留作 ?bdelay=600 對照。
//
// 【逃生口】?vpfix=off 或 ?noreload=1：跳過彈跳（沿用舊的頁內判斷，那邊也認得同一組參數）；
// auth-url（?code=/?state=/?token=…）：一次性授權碼經不起被彈跳頁「消費」一次網址再導覽一次的風險，直接放行。

const SKIN_CACHE_MS = 60_000
const SKIN_FETCH_TIMEOUT_MS = 800
let skinCache: { skin: string; at: number } = { skin: 'default', at: 0 }
let refreshing = false

async function fetchSkin(): Promise<string> {
  const base = process.env.API_URL || 'http://localhost:8080'
  const ac = new AbortController()
  const timer = setTimeout(() => ac.abort(), SKIN_FETCH_TIMEOUT_MS)
  try {
    const res = await fetch(`${base}/api/v1/app-settings/public`, { signal: ac.signal })
    if (!res.ok) return skinCache.skin
    const j = await res.json()
    const v = j?.settings?.active_skin
    return typeof v === 'string' && v ? v : 'default'
  } catch {
    return skinCache.skin // 失敗一律沿用上一個已知值（初始 fallback 'default'）
  } finally {
    clearTimeout(timer)
  }
}

// 回傳目前可用的 skin：新鮮就直接用；過期先回舊值（不擋這次請求），背景用 event.waitUntil 刷新。
// 伺服器剛啟動、從未取得過（at===0）時例外：等這一次 fetch（最多 SKIN_FETCH_TIMEOUT_MS），否則每次部署後的
// 第一位到站者會拿到深色預設底、再切到 warm 皮膚的 app，正好閃一下——這是本機制最該避免的畫面。
async function getSkin(event: NextFetchEvent): Promise<string> {
  const fresh = Date.now() - skinCache.at < SKIN_CACHE_MS
  if (fresh) return skinCache.skin
  if (skinCache.at === 0 && !refreshing) {
    refreshing = true
    try {
      const skin = await fetchSkin()
      skinCache = { skin, at: Date.now() }
    } finally {
      refreshing = false
    }
    return skinCache.skin
  }
  if (!refreshing) {
    refreshing = true
    event.waitUntil(
      fetchSkin()
        .then((skin) => { skinCache = { skin, at: Date.now() } })
        .finally(() => { refreshing = false })
    )
  }
  return skinCache.skin
}

export async function middleware(req: NextRequest, event: NextFetchEvent) {
  const decision = decideBounce(req.headers, req.nextUrl, req.method)

  // 診斷（只在 ?vpdebug=1）：真機的 UA／Sec-Fetch／Referer／cookie 只有在這裡看得到，寫進 Railway log
  // 讓開發端能對照面板截圖（第三輪掃碼就是靠面板 ar=skip:not-safari 才抓到 UA 閘門判錯，但看不到 UA 全文）。
  if (req.nextUrl.searchParams.get('vpdebug') === '1') {
    const h = req.headers
    const ck = h.get('cookie') || ''
    console.log(
      `[arrival-bounce] ${decision.bounce ? 'BOUNCE' : 'skip:' + decision.reason}` +
        ` path=${req.nextUrl.pathname}${req.nextUrl.search} site=${h.get('sec-fetch-site')} mode=${h.get('sec-fetch-mode')}` +
        ` dest=${h.get('sec-fetch-dest')} referer=${h.get('referer') ? 'yes' : 'no'}` +
        ` cookie=${/dor_b=1/.test(ck) ? 'dor_b' : '-'}/${/dor_pwa=1/.test(ck) ? 'dor_pwa' : '-'}` +
        ` ua=${JSON.stringify(h.get('user-agent') || '')}`
    )
  }

  if (!decision.bounce) {
    if (req.nextUrl.searchParams.get('vpdebug') === '1') {
      const headers = new Headers()
      headers.set('x-dor-bounce', `skip:${decision.reason}`)
      return NextResponse.next({ headers })
    }
    return NextResponse.next()
  }

  const skin = await getSkin(event)
  const [bg, fg] = veilColorsOf(skin)
  const target = req.nextUrl.pathname + req.nextUrl.search
  const delayMs = parseBounceDelay(req.nextUrl.search)
  const html = bounceHtml({ bg, fg, target, delayMs })

  return new NextResponse(html, {
    status: 200,
    headers: {
      'content-type': 'text/html; charset=utf-8',
      'cache-control': 'private, no-store, max-age=0',
      'cdn-cache-control': 'no-store',
      vary: 'User-Agent, Sec-Fetch-Site, Cookie',
      'x-dor-bounce': '1',
    },
  })
}

// 只攔頁面請求：靜態檔（副檔名結尾）、/api、/_next、/ws、/vptest 一律排除。
// /vptest 是症狀 A 的診斷用靜態二分頁（見 MEMORY 的 mobile-viewport-height 條目），彈跳邏輯不該碰它。
export const config = {
  matcher: ['/((?!api/|_next/|ws/|vptest/|.*\\.[A-Za-z0-9]+$).*)'],
}
