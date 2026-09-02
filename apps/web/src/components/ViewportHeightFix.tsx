'use client'

import { useEffect } from 'react'
import { MOBILE_MQ } from '@/lib/useIsMobile'

/**
 * iOS Safari 視窗高度「安全網」v3。
 *
 * 【定位】全站高度的唯一真相是 CSS（globals.css 的 .app-h / .phone-frame ＝ 100vh → 100dvh）。
 * 本檔只做一件事：在偵測到「瀏覽器回報的 layout viewport 明顯小於實際可見區」時，寫入 --app-h 把版面**加高**。
 * CSS 端一律 max(100dvh, var(--app-h, 0px)) ⇒ 本檔不可能讓版面變矮，最壞情況＝完全沒有這支程式。
 *
 * 【不變式（I–IV＋S），改動時不得違反】
 *  I  下限：CSS 的 max() 保證 ≥ 100dvh。JS 無權讓版面比純 CSS 版矮。
 *  II 只准加高，且只信 visualViewport.height。
 *     ⚠️ 禁止用 innerHeight 加高：部分 iOS 版本它等同「工具列收合態的大 viewport」，
 *        拿它加高會把底部導覽列推到 Safari 工具列底下 —— 那是比症狀 A 更嚴重的功能缺陷。
 *  III 鍵盤沉默：可能有鍵盤/原生選單時**不寫入**。「釋放」不在此限——release() 只會讓高度回到
 *     CSS 的 100dvh（對鍵盤免疫且 ≥ 現值），數學上不可能造成症狀 B；而 kbUp() 的旁證訊號在 iOS
 *     上可能永久為真（收鍵盤後 vv.offsetTop 不歸零、iframe/select 長駐焦點），若讓它連釋放一起
 *     擋掉，--app-h 會被永久鎖在偏大的值 → 底部導覽列被推出可見區、又因 body overflow:hidden
 *     捲不到，那是比症狀 A 更嚴重的功能缺陷。故所有旁證都有時效，並額外設 KB_LATCH_MAX_MS 上限。
 *  IV 100dvh 也可能過期：CSS 的 100dvh 本身可能比可見區矮（實測差一個「狀態列＋網址列」的高度），
 *     此時 vv.height 與 root.clientHeight 都是對的，不變式 II 那條規則不會成立。用 dvhPx() 探針
 *     直接量「純 100dvh 解析出多少」，比 vv.height 矮才加高。加高值仍只取 vv.height（見不變式 II）。
 *
 * 【v1.1.703 的教訓（勿重蹈）】「跨工作階段記憶健康高度、重載後用記憶補高」上線當天即翻車：
 *  病態不只會「一致地偏小」（露出底色米白帶），也會「一致地偏大」（載入瞬間短暫回報工具列收合的
 *  大 viewport）——偏大樣本一旦被記住，之後每次健康瀏覽都被多墊 70px，首頁「開始跑步」面板整個
 *  被推出可見區。變矮只是難看、變高會藏掉功能；在「無法分辨誰說謊」的前提下，任何基於歷史記憶的
 *  自動加高都不安全，故整組撤除（v1.1.704），並主動清掉舊鍵 dor.vpmem。
 *  S  standalone 硬基準（v1.1.709）：iOS 主畫面 App（navigator.standalone===true）＋viewport-fit=cover
 *     下 viewport ≡ 實體螢幕；首啟少算狀態列（852→793）時以 screen 邊長加高。僅 iOS standalone，
 *     Android 旗標不存在天然排除；轉向自癒後規則停火、走 calm 釋放。
 *
 * 【v1.1.706-707 的教訓（勿重蹈）】三個「猜測性去黏」hack——真實捲動 nudge（+2px→scrollTo(0,1)）、
 *  meta viewport 等價改寫、root translateZ(0) 層重建——上線期間使用者回報「一般瀏覽也開始出現底部
 *  空帶」（先前僅『分頁被釋放後還原』偶發），時間點吻合、又修不好原病（有截圖為證），v1.1.728
 *  全數移除、nudge 回到 +1px 重排版；v1.1.744 連 +1px nudge 也移除（每次載入/回前景讓文件短暫
 *  可捲動，是最後一個無佐證的干預、也是病態的可疑誘因之一）——本檔自此為**純量測**：只讀數值、
 *  只在規則 II/IV/S 成立時寫 --app-h。合成器行為本機無法驗證，主動干預沒有 A/B 證據不得再上；
 *  量測性規則（II/IV/S，只准加高、只信可驗證來源）不在此限。
 *
 * 【v1.1.664 的教訓（勿重蹈）】
 *  ・Math.max(innerHeight, visualViewport.height) 的前提是「innerHeight 不受鍵盤影響」，
 *    iOS 12 起已被推翻、iOS 17/18 更有 innerHeight/vv/100dvh 三者一起掉的實測 → max() 兩邊同時被壓 →
 *    版面被寫成鍵盤態高度並鎖死（症狀 B）。
 *  ・不綁 visualViewport 的 'scroll'：那是 iOS 把焦點輸入框 pan 進可視區時的過渡值來源。
 *  ・不做 window.scrollTo(0,0) nudge：body 是 overflow:hidden，那行從頭到尾都是 no-op。
 *
 * 【使用者端逃生門／診斷（不需重新部署）】
 *  ?vpfix=off  完全停用並清除 --app-h（＝純 CSS 版），設定寫入 localStorage 持久生效
 *  ?vpfix=on   恢復預設
 *  ?vpfix=heal 額外啟用「回前景 hard heal」（display:none 一幀強制重排；/track 永久排除）
 *  ?vpdebug=1  顯示診斷條（見 ViewportDebug.tsx）
 */

const FLAG_KEY = 'dor.vpfix'   // 'on' | 'off' | 'heal'
const MIN_SANE_PX = 240        // 低於此值一律視為量測失敗
const GROW_MIN_PX = 24         // 需比 layout viewport 大這麼多才算「真的該加高」（iOS 26 已知 10~24px 常態落差）
const RELEASE_N = 3            // 自我恢復：連續幾次讀到「不需要加高」
const RELEASE_MS = 900         // 且需持續這麼久，才釋放 --app-h
const BLUR_GRACE_MS = 700      // 失焦後鍵盤收合動畫殘留期
const KB_HINT_MS = 2000        // 數值旁證（被 pan 過的 visual viewport）的信任期，超過即不再採信
const KB_LATCH_MAX_MS = 12000  // 鍵盤態連續判定上限：超過此值仍在「鍵盤中」就強制釋放 --app-h（防永久 latch）
const POLL_MS = 1500
const RESUME_STEPS = [0, 120, 400, 1000]

const NO_KB_INPUT = new Set(['button', 'checkbox', 'radio', 'submit', 'reset', 'file', 'image', 'range', 'color'])

export default function ViewportHeightFix() {
  useEffect(() => {
    const root = document.documentElement
    const clearVar = () => { try { root.style.removeProperty('--app-h') } catch { /* noop */ } }
    try { localStorage.removeItem('dor.vpmem') } catch { /* noop */ } // v1.1.703 記憶規則已撤除，清掉可能被污染的舊鍵

    // URL 參數是最高優先來源，且與 localStorage 的存取分開 try：無痕分頁（setItem 丟
    // QuotaExceededError）、Safari「封鎖所有 Cookie」或 in-app WebView 停用 DOM storage
    // （存取即丟 SecurityError）時，使用者手打的 ?vpfix=off 逃生門仍然必須當場生效——
    // 而那正是最需要逃生門的族群（本站有大量 LINE/FB in-app 瀏覽器流量）。寫不進去只是不持久。
    let mode: string = 'on'
    let urlFlag: string | null = null
    try { urlFlag = new URLSearchParams(window.location.search).get('vpfix') } catch { /* noop */ }
    if (urlFlag === 'on' || urlFlag === 'off' || urlFlag === 'heal') {
      mode = urlFlag
      try { window.localStorage.setItem(FLAG_KEY, urlFlag) } catch { /* noop：本次仍生效，只是不持久 */ }
    } else {
      try { mode = window.localStorage.getItem(FLAG_KEY) || 'on' } catch { /* noop：維持預設 'on' */ }
    }

    const vv = window.visualViewport
    // 沒有 visualViewport（iOS 12 以前）＝沒有可信來源；後台不吃 --app-h 也不需要介入。
    if (mode === 'off' || !vv || window.location.pathname.startsWith('/admin')) { clearVar(); return }

    const mql = window.matchMedia(MOBILE_MQ)
    let disposed = false
    let applied = 0
    let widthKey = -1
    let calmCount = 0
    let calmSince = 0
    let lastBlurAt = 0
    let lastFocusAt = 0   // 最後一次 focusin/focusout 的時刻（數值旁證的信任期基準）
    let kbSince = 0       // 目前這段「連續判定為鍵盤態」的起點；kbUp() 轉 false 即歸零
    let wasHidden = false
    let rafId = 0
    let timers: ReturnType<typeof setTimeout>[] = []

    const clearTimers = () => { timers.forEach(clearTimeout); timers = [] }
    const later = (ms: number, fn: () => void) => { timers.push(setTimeout(() => { if (!disposed) fn() }, ms)) }

    // 鍵盤態判定：主訊號是「有無可輸入元素取得焦點」——完全不依賴任何會失真的數值，
    // 所以 iOS 17/18「innerHeight 也被壓縮」打不倒它。數值只當旁證，且旁證一律要有時效或佐證：
    // 本站 body{overflow:hidden}，使用者無法自己捲回頂端，而 iOS 收鍵盤後 visualViewport.offsetTop
    // 常態不歸零（Apple Forums 800125 / claus gist），無時效的話這條會變成永久 latch。
    const kbUp = (): boolean => {
      const compressed = vv.height > 0 && root.clientHeight - vv.height > 60 // 可見區被實際壓縮＝有東西蓋著
      const el = document.activeElement as HTMLElement | null
      if (el && el !== document.body) {
        if (el.isContentEditable) return true
        const tag = el.tagName
        if (tag === 'TEXTAREA' || tag === 'SELECT') return true // select 在 iOS 會叫出原生 picker
        if (tag === 'INPUT') {
          const t = ((el as HTMLInputElement).type || 'text').toLowerCase()
          if (!NO_KB_INPUT.has(t)) return true
        }
        // iframe（Google One-Tap 等第三方輸入）：焦點可能長駐（例如影片播完仍停在 iframe 上），
        // 不足以單獨當鍵盤證據 → 必須搭配「可見區真的被壓縮」的數值佐證。
        if (tag === 'IFRAME' && compressed) return true
      }
      if (lastBlurAt > 0 && Date.now() - lastBlurAt < BLUR_GRACE_MS) return true
      if (compressed) return true
      // 被 pan 過的 visual viewport 只在「剛剛真的有焦點事件」時才算鍵盤旁證（見上方時效說明）。
      if (vv.offsetTop > 1 && lastFocusAt > 0 && Date.now() - lastFocusAt < KB_HINT_MS) return true
      return false
    }

    // 上限＝實體螢幕邊長（screen 不隨工具列/鍵盤變動）。用「寬度」判方向：寬度是穩定指紋。
    const screenCeil = (): number => {
      const s = window.screen
      const a = (s && s.width) || 0
      const b = (s && s.height) || 0
      if (!a || !b) return Number.POSITIVE_INFINITY
      const long = Math.max(a, b)
      const short = Math.min(a, b)
      const w = window.innerWidth || root.clientWidth || 0
      const portrait = Math.abs(w - short) <= Math.abs(w - long)
      return (portrait ? long : short) + 2
    }

    // 【探針】量出消費端拿到的「純 100dvh」實際是多少（不含 var(--app-h)，故非循環：
    // --app-h 不影響 100dvh，也不影響 vv.height，不會自我震盪）。
    // visibility:hidden 仍會參與版面計算，讀 rect 拿得到高度；不進無障礙樹、不吃點擊。
    // 舊引擎（無 dvh）→ height 宣告無效 → auto → 固定定位空元素高度 0 → 回傳 0 讓規則整條略過
    //（那些引擎走的是 CSS 的 100vh 分支，本來就用不到 --app-h）。
    const supportsDvh = typeof CSS !== 'undefined' && typeof CSS.supports === 'function' && CSS.supports('height', '100dvh')
    let probe: HTMLDivElement | null = null
    const dvhPx = (): number => {
      if (!supportsDvh) return 0
      try {
        if (!probe) {
          probe = document.createElement('div')
          probe.setAttribute('aria-hidden', 'true')
          probe.style.cssText = 'position:fixed;top:0;left:0;width:1px;height:100dvh;visibility:hidden;pointer-events:none;z-index:-2147483647'
          document.body.appendChild(probe)
        }
        return Math.round(probe.getBoundingClientRect().height)
      } catch { return 0 }
    }
    const dropProbe = () => { try { probe?.remove() } catch { /* noop */ } finally { probe = null } }

    const setVar = (px: number) => {
      if (px === applied) return
      applied = px
      try { root.style.setProperty('--app-h', `${px}px`) } catch { /* noop */ }
    }
    const release = () => { if (applied !== 0) { applied = 0; clearVar() } }

    const commit = () => {
      if (disposed) return
      if (!mql.matches) { release(); return }   // 桌機／平板：完全不介入

      const w = Math.round(window.innerWidth || root.clientWidth || 0)
      if (w !== widthKey) { widthKey = w; calmCount = 0; kbSince = 0; release() } // 轉向或改寬 → 基準整組作廢

      // 不變式 III：鍵盤期間**不寫入**（避免打字中版面跳動）。但「釋放」有硬上限——
      // 連續判定鍵盤態超過 KB_LATCH_MAX_MS 就強制 release()，確保任何一個 kbUp() 訊號卡住
      // （offsetTop 不歸零、iframe/select 長駐焦點）都不可能把 --app-h 永久鎖在偏大的值。
      // 釋放只會回到 CSS 的 100dvh，對鍵盤免疫，因此這條逃生路徑不會重現症狀 B。
      if (kbUp()) {
        if (kbSince === 0) kbSince = Date.now()
        if (applied !== 0 && Date.now() - kbSince >= KB_LATCH_MAX_MS) { calmCount = 0; release() }
        return
      }
      kbSince = 0

      if (vv.scale && vv.scale > 1.01) return   // 捏合縮放中，vv.height 不代表版面

      // root.clientHeight ＝ CSSOM 規定的「viewport 高度」，不受我們寫在 html 上的 height 影響（非循環）
      const icb = root.clientHeight || 0
      const cand = Math.round(vv.height || 0)
      if (cand < MIN_SANE_PX || icb < MIN_SANE_PX) return
      if (cand > screenCeil()) return           // 荒謬值護欄

      if (cand > icb + GROW_MIN_PX) {           // 不變式 II：可見區 > layout viewport ⇒ layout viewport 過期 ⇒ 加高
        calmCount = 0
        setVar(cand)
        return
      }

      // 不變式 IV（v1.1.701 新增，依實測數據）：**100dvh 自己也可能過期偏小**。
      // 2026-08-31 使用者回報「Safari 把分頁資源釋放掉、回來時重新讀取」後跑版，兩張同機截圖量出：
      // 可見內容區 553px，但蓋板／根容器只有 483px，差 70px ＝ 狀態列＋網址列高度；
      // 而該狀態下 vv.height 與 root.clientHeight 都是正確的 553 → 上面那條「cand > icb」永遠不成立，
      // 安全網從頭到尾沒啟動過。也就是說壞掉的不是 layout viewport，而是 100dvh 本身——
      // 這是前三次修正都沒有量測過的維度（都在假設 layout viewport 過期）。
      // 寫入值一律用 cand（visualViewport.height）＝不變式 II 唯一信任的來源；
      // 刻意不用 root.clientHeight，避免踩到「大 viewport 把底部導覽列推出可見區」那個更嚴重的坑。
      const dvh = dvhPx()
      if (dvh >= MIN_SANE_PX && cand > dvh + GROW_MIN_PX) {
        calmCount = 0
        setVar(cand)
        return
      }

      // 不變式 S（v1.1.709，standalone 專屬）：iOS 主畫面 App（navigator.standalone===true）＋
      // meta viewport-fit=cover 下，「viewport ≡ 實體螢幕」是硬事實——沒有工具列、沒有動態 chrome。
      // 已知 WebKit 病態：standalone 首次啟動 viewport 少算一條狀態列高（實例：852 回報 793，
      // 底部露出一條 canvas 帶），且各 API 一致地錯、轉向後才自癒。瀏覽器模式沒有任何絕對基準
      // 可信（見上方 v1.1.703 教訓），但 standalone 有：window.screen 不受 webview 狀態影響。
      // 只在 iOS standalone 生效——navigator.standalone 是 iOS 專屬旗標；Android standalone 的
      // viewport 本來就不含狀態列，套 screen 會過高（把底部導覽推出畫面），靠旗標天然排除。
      if ((navigator as { standalone?: boolean }).standalone === true) {
        const full = screenCeil() - 2 // screenCeil 的 +2 是護欄餘裕；這裡要的是依方向選出的實際螢幕邊長
        if (Number.isFinite(full) && full >= MIN_SANE_PX && full > cand + GROW_MIN_PX) {
          calmCount = 0
          setVar(full)
          return
        }
      }

      if (applied === 0) return
      // 自我恢復：連續 N 次、持續 T 毫秒都不需要加高 → 整個釋放，回到純 CSS 的 100dvh。
      // 刻意「釋放」而非「寫入較小值」：--app-h 永遠只有「有加高」與「不存在」兩種狀態，沒有中間錯值可鎖死。
      if (calmCount === 0) calmSince = Date.now()
      calmCount += 1
      if (calmCount >= RELEASE_N && Date.now() - calmSince >= RELEASE_MS) { calmCount = 0; release() }
    }

    const schedule = () => {
      if (rafId || disposed) return
      rafId = requestAnimationFrame(() => { rafId = 0; commit() })
    }

    // cederhook healViewport：強迫整棵子樹重排。已知副作用：閃一下、子樹捲動位置/CSS 動畫/媒體狀態重置。
    // 預設關閉（需 ?vpfix=heal），且 /track 永久排除（跑步中 Leaflet/計時器不可重排）。
    const hardHeal = () => {
      if (mode !== 'heal' || kbUp()) return
      if (window.location.pathname.startsWith('/track')) return
      const el = document.getElementById('app-shell') || document.querySelector<HTMLElement>('main.phone-frame')
      if (!el) return
      const prev = el.style.display
      el.style.display = 'none'
      void el.offsetHeight
      el.style.display = prev
    }

    const onResume = () => {
      if (disposed || document.hidden || !mql.matches) return
      clearTimers()
      RESUME_STEPS.forEach((ms) => later(ms, commit))
      if (mode === 'heal') later(500, () => { hardHeal(); commit() })
    }

    const onResize = () => schedule()
    const onOrientation = () => { widthKey = -1; onResume() }
    const onPageShow = () => onResume()
    const onPageHide = () => { wasHidden = true }
    const onWinFocus = () => { if (wasHidden) { wasHidden = false; onResume() } } // 只有真的離開過前景才算 resume
    const onVis = () => { if (document.hidden) wasHidden = true; else { wasHidden = false; onResume() } }
    const onFocusIn = () => { lastFocusAt = Date.now() }
    const onFocusOut = () => { lastBlurAt = Date.now(); lastFocusAt = Date.now(); later(BLUR_GRACE_MS + 60, commit); later(KB_LATCH_MAX_MS + 60, commit) }
    const onMqChange = () => { widthKey = -1; commit() }

    commit()

    window.addEventListener('resize', onResize)
    window.addEventListener('orientationchange', onOrientation)
    window.addEventListener('pageshow', onPageShow)
    window.addEventListener('pagehide', onPageHide)
    window.addEventListener('focus', onWinFocus)
    document.addEventListener('visibilitychange', onVis)
    document.addEventListener('focusin', onFocusIn)
    document.addEventListener('focusout', onFocusOut)
    vv.addEventListener('resize', onResize)
    // ⚠️ 刻意不綁 vv 的 'scroll'：那是 iOS 捲到焦點輸入框時的過渡值來源（v1.1.664 的惡性迴圈觸發點之一）。
    mql.addEventListener('change', onMqChange)

    // ── 症狀 A 自癒（2026-09-02，證據鏈完備後上線）──
    // 病態＝root 圖層被合成器上移 Δ=lvh−svh（兩張 vpdebug 定量），所有可讀指標卻全部正常
    // → 量測式偵測在數學上不可能。唯一可靠訊號：**命中測試跟著位移走**——實體觸點落在畫面
    // 下緣 Δ 區（如底部導覽列，它也被上移了）時，事件 clientY 會超過 layout viewport 高度，
    // 健康狀態幾何上不可能（捏合縮放/視窗被 pan 為僅有例外，已排除）。
    // 治癒（v752 改）＝白幕重載 window.__dorVeilReload（layout.tsx 開機腳本）：v749 原用程式化切分頁
    // （about:blank 開 0.4s 即關），使用者在首頁實測有效、但在賽事落地頁實測無效；程式化 location.reload()
    // 則實測有效（vpdebug 🔄 按鈕，nav=reload 後版面正常）。這條現在是「到站自動重載」之後的第二道備援
    // （重載後仍壞、或到站重載被閘門跳過時），節流：每次載入最多 1 次——幾何誤判理論上不存在，這是縱深防禦。
    let healCount = 0
    const onPointerHeal = (e: PointerEvent) => {
      if (!mql.matches) return
      if (healCount >= 1) return
      const vvp = window.visualViewport
      if (vvp && (vvp.scale > 1.01 || vvp.offsetTop > 1)) return
      const icb = root.clientHeight || 0
      if (icb < MIN_SANE_PX || e.clientY <= icb + 8) return
      if (typeof window.__dorVeilReload !== 'function') return
      healCount++
      try {
        root.dataset.vpheal = `tap-reload@${new Date().toTimeString().slice(0, 8)}`
        window.__dorVeilReload('tap')
      } catch { /* noop */ }
    }
    window.addEventListener('pointerdown', onPointerHeal, { capture: true, passive: true })

    const poll = setInterval(() => { if (!document.hidden) commit() }, POLL_MS)

    return () => {
      disposed = true
      window.removeEventListener('pointerdown', onPointerHeal, { capture: true } as EventListenerOptions)
      dropProbe()
      clearInterval(poll)
      clearTimers()
      if (rafId) cancelAnimationFrame(rafId)
      window.removeEventListener('resize', onResize)
      window.removeEventListener('orientationchange', onOrientation)
      window.removeEventListener('pageshow', onPageShow)
      window.removeEventListener('pagehide', onPageHide)
      window.removeEventListener('focus', onWinFocus)
      document.removeEventListener('visibilitychange', onVis)
      document.removeEventListener('focusin', onFocusIn)
      document.removeEventListener('focusout', onFocusOut)
      vv.removeEventListener('resize', onResize)
      mql.removeEventListener('change', onMqChange)
      root.style.height = ''
      clearVar()
    }
  }, [])

  return null
}
