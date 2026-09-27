// retro skin 專屬字體（契約 §3）：像素中文字型「Cubic 11」（俐方體 11 號，SIL OFL 1.1）。
// https://github.com/ACh-K/Cubic-11 ，字型檔＋授權原文放在
// apps/web/public/fonts/cubic11/Cubic_11.woff2 ＋ OFL.txt（一併下載，逐字保留）。
//
// 契約要求「只在 retro 生效時以 FontFace API 載入」「不在全站 CSS 宣告 @font-face」：
// 因此本檔案*不*是 next/font（那會在建置期把 @font-face 塞進全站 CSS，違反契約），
// 而是提供一個 loadRetroFont() 函式，由 RetroBackground.tsx 掛載時（即 retro 生效時）呼叫一次。
// 非 retro 使用者的頁面完全不會執行到這支函式，globals.css 也不含任何 @font-face 規則，
// 零 bundle／零網路請求影響（字型檔只在呼叫時才會被瀏覽器抓取）。
//
// 用法：
//   import { loadRetroFont, RETRO_FONT_FAMILY } from '@/components/retro/fonts'
//   useEffect(() => { loadRetroFont() }, [])
//
// CSS 端 [data-skin="retro"] 的 font-family 已把 'DORPixel' 排第一順位（見 globals.css），
// 在字型還沒載入完成前，瀏覽器會先用 fallback 鏈（monospace）顯示，載入完成後 document.fonts
// 會觸發重繪換成像素字，不會造成版面跳動以外的問題（純文字換字體，無 FOIT 阻塞）。

export const RETRO_FONT_FAMILY = 'DORPixel'

const FONT_URL = '/fonts/cubic11/Cubic_11.woff2'

let loadPromise: Promise<void> | null = null

/**
 * 以 FontFace API 載入 Cubic 11 並註冊到 document.fonts（僅在 retro skin 生效時呼叫）。
 * 重複呼叫會回傳同一個 in-flight/已完成的 Promise，不會重複下載或重複註冊。
 * 任何環境不支援 FontFace（極舊瀏覽器）或載入失敗，一律靜默失敗——CSS fallback 鏈已經
 * 保證版面不會壞，這裡不需要，也不應該讓例外往外拋出去打斷 retro 背景的掛載。
 */
export function loadRetroFont(): Promise<void> {
  if (loadPromise) return loadPromise
  if (typeof window === 'undefined' || typeof (window as any).FontFace !== 'function' || !('fonts' in document)) {
    loadPromise = Promise.resolve()
    return loadPromise
  }
  // 已经载入过（例如 retro -> default -> retro 来回切换）就不用重复注册
  const already = Array.from(document.fonts.values()).some((f) => f.family === `"${RETRO_FONT_FAMILY}"` || f.family === RETRO_FONT_FAMILY)
  if (already) {
    loadPromise = Promise.resolve()
    return loadPromise
  }
  loadPromise = (async () => {
    try {
      const face = new FontFace(RETRO_FONT_FAMILY, `url(${FONT_URL}) format('woff2')`, {
        display: 'swap',
      })
      const loaded = await face.load()
      document.fonts.add(loaded)
    } catch {
      // 靜默失敗：CSS 的 fallback 字型鏈（monospace）確保畫面永遠可讀。
    }
  })()
  return loadPromise
}
