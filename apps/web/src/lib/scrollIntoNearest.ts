// 把元素捲進「最近的可捲動祖先」（overflowY 為 auto/scroll 的那個容器），刻意不用 element.scrollIntoView()。
//
// 為什麼不能用 scrollIntoView（2026-09-13，Playwright 探針實證）：
// 手機視窗高度規則（globals.css / ViewportHeightFix）讓全螢幕容器一律取 max(100dvh, var(--app-h, 0px))、
// html/body 取 max(100%, …) 且 overflow:hidden——iOS 上 html/body/.phone-frame 因此可能比實際視窗高 10–24px。
// 而 scrollIntoView 會把「每一個」可捲動祖先都捲到位，包含這些 overflow:hidden 的根容器（overflow:hidden 只是
// 藏掉捲軸與手勢，程式化捲動照樣生效），於是頁首被往上推 10–24px 出視窗；body 又是 overflow:hidden，使用者
// 根本捲不回來。症狀：/track/history 點一筆紀錄後標題文字被裁掉（v801 加的 detailRef.scrollIntoView 觸發）。
//
// 這裡只算一個目標 scrollTop、只捲那「一個」overflow:auto/scroll 的容器（ScrollArea / AdminShell 主內容區）；
// documentElement / body 永遠不視為容器、也永遠不碰。找不到容器就什麼都不做——絕不退回 window.scrollTo
// 或 scrollIntoView，寧可不捲也不要把版面捲壞。

export type ScrollIntoNearestOpts = {
  /** 'start'＝元素頂對齊容器頂；'center'＝元素置中（預設 'start'） */
  block?: 'start' | 'center'
  behavior?: ScrollBehavior
  /** 額外往上留的空間（px），例如容器內有 sticky 標題時避免被蓋住 */
  offset?: number
}

/** 往上找第一個「真的捲得動」的祖先（computed overflowY 為 auto/scroll 且 scrollHeight > clientHeight）；
 *  html/body/.phone-frame 一律不算（見檔頭），找不到回 null。
 *  對抗式審查修正（2026-09-13）：只設 overflow-x:hidden/auto 的包裝層（TrainingScreen 月曆殼、後台表格殼）
 *  依 CSS Overflow 規範會把 overflow-y 的 visible「計算成 auto」，但它高度隨內容長、根本捲不動——若把它當
 *  容器，scrollTo 變 no-op、真正的捲動層永遠不會被捲（月曆「自動捲到今天」整個失效）。所以捲不動的候選要
 *  跳過、繼續往上找。 */
export function nearestScrollable(el: HTMLElement | null): HTMLElement | null {
  if (!el || typeof window === 'undefined') return null
  let cur: HTMLElement | null = el.parentElement
  while (cur) {
    // 根容器（html/body/.phone-frame）正是 iOS 上「比視窗高又 overflow:hidden」的元兇，捲了就回不來——直接略過、
    // 不當候選（桌機 .phone-frame 為 overflow-y:auto 的極矮視窗退路，也不該由這裡去捲）
    if (cur !== document.documentElement && cur !== document.body && !cur.classList.contains('phone-frame')) {
      const oy = window.getComputedStyle(cur).overflowY
      if ((oy === 'auto' || oy === 'scroll') && cur.scrollHeight > cur.clientHeight + 1) return cur
    }
    cur = cur.parentElement
  }
  return null
}

/** 只捲最近的可捲動容器讓 el 進入可視範圍；沒有容器（或 SSR）就不動作。 */
export function scrollIntoNearest(el: HTMLElement | null, opts?: ScrollIntoNearestOpts): void {
  if (!el) return
  const container = nearestScrollable(el)
  if (!container) return
  // 目標 scrollTop＝目前 scrollTop＋（元素相對容器頂的位移）；兩者都用 getBoundingClientRect 量，
  // 不受中間 position:relative 祖先影響（offsetTop 會被它們攔截）
  // 減 clientTop：getBoundingClientRect 量到的是容器 border-box 頂，捲動視口（scrollport）從 border 內側起算
  let target = container.scrollTop + (el.getBoundingClientRect().top - container.getBoundingClientRect().top) - container.clientTop - (opts?.offset ?? 0)
  if (opts?.block === 'center') target -= (container.clientHeight - el.offsetHeight) / 2
  target = Math.max(0, target)
  container.scrollTo({ top: target, behavior: opts?.behavior ?? 'auto' })
}
