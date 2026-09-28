// cute（溫馨可愛）skin 專屬字體（契約 §3）：圓體手寫感中文字型「DORCute」
// （jf open huninn 2.1 的子集化＋改名衍生版，SIL OFL 1.1）。
// 字型檔＋授權原文＋來源說明放在 apps/web/public/fonts/dorcute/
// （DORCute.woff2 ＋ LICENSE.txt ＋ README.md，逐字保留原始版權聲明）。
//
// 比照 components/retro/fonts.ts 的做法——契約要求「只在 cute 生效時以 FontFace API
// 載入」「不在全站 CSS 宣告 @font-face」：因此本檔案*不*是 next/font（那會在建置期把
// @font-face 塞進全站 CSS，違反契約），而是提供一個 loadCuteFont() 函式，由
// CuteBackground.tsx 掛載時（即 cute 生效時）呼叫一次。非 cute 使用者的頁面完全不會
// 執行到這支函式，globals.css 也不含任何 DORCute 的 @font-face 規則，零 bundle／零網路
// 請求影響（字型檔只在呼叫時才會被瀏覽器抓取）。
//
// 用法：
//   import { loadCuteFont, CUTE_FONT_FAMILY } from '@/components/cute/fonts'
//   useEffect(() => { loadCuteFont() }, [])
//
// CSS 端 [data-skin="cute"] 的 font-family 已把 'DORCute' 排第一順位（見 globals.css），
// 在字型還沒載入完成前，瀏覽器會先用 fallback 鏈顯示，載入完成後 document.fonts
// 會觸發重繪換成圓體字，不會造成版面跳動以外的問題（純文字換字體，無 FOIT 阻塞）。
// 子集外字元（罕見字／emoji）一律退回 fallback 鏈，不會消失或變成缺字方塊。

export const CUTE_FONT_FAMILY = 'DORCute'

const FONT_URL = '/fonts/dorcute/DORCute.woff2'

let loadPromise: Promise<void> | null = null

/**
 * 以 FontFace API 載入 DORCute 並註冊到 document.fonts（僅在 cute skin 生效時呼叫）。
 * 重複呼叫會回傳同一個 in-flight/已完成的 Promise，不會重複下載或重複註冊。
 * 任何環境不支援 FontFace（極舊瀏覽器）或載入失敗，一律靜默失敗——CSS fallback 鏈已經
 * 保證版面不會壞，這裡不需要，也不應該讓例外往外拋出去打斷 cute 背景的掛載。
 */
export function loadCuteFont(): Promise<void> {
  if (loadPromise) return loadPromise
  if (typeof window === 'undefined' || typeof (window as any).FontFace !== 'function' || !('fonts' in document)) {
    loadPromise = Promise.resolve()
    return loadPromise
  }
  // 已經載入過（例如 cute -> default -> cute 來回切換）就不用重複註冊
  const already = Array.from(document.fonts.values()).some((f) => f.family === `"${CUTE_FONT_FAMILY}"` || f.family === CUTE_FONT_FAMILY)
  if (already) {
    loadPromise = Promise.resolve()
    return loadPromise
  }
  loadPromise = (async () => {
    try {
      const face = new FontFace(CUTE_FONT_FAMILY, `url(${FONT_URL}) format('woff2')`, {
        display: 'swap',
      })
      const loaded = await face.load()
      document.fonts.add(loaded)
    } catch {
      // 靜默失敗：CSS 的 fallback 字型鏈確保畫面永遠可讀。
    }
  })()
  return loadPromise
}
