// scifi skin 專屬字體（契約 §3）：Orbitron，僅用於數字／英文標題，中文維持系統字。
// 用 next/font/google 在建置時自託管（不在執行期打 fonts.googleapis.com，符合「只加不改」CSP 原則
// ——不需要在 next.config.mjs 的 CSP 加 Google Fonts 網域）。
//
// 用法（給整合者）：本檔只匯出 font 物件，不會自己套用到任何頁面（THEME 工人不得改 layout.tsx）。
// 若要讓 --font-scifi-orbitron 這個 CSS 變數在頁面上真的有值，需要有人把 `orbitron.variable`
// 這個 className 掛到一個夠高層的祖先節點（例如 <html>）上，掛上後其所有子孫元素都能讀到這個
// CSS 變數。掛不掛都不影響其他 skin：本檔案未被匯入到任何全站入口前，不會產生任何效果；
// [data-skin="scifi"] 下的 var(--font-scifi-orbitron) 有 fallback 字型鏈，缺這個 class 時
// 只是退回到 fallback 字型，不會壞版面。
//
// 建議加到 app/layout.tsx 的程式片段（本檔不自己做）：
//   import { orbitron } from '@/components/scifi/fonts'
//   ...
//   <html lang="zh-Hant" className={orbitron.variable} ...>
// v857：改用 next/font/local（同 app/track/scifi/font.ts 的自帶字型檔），不再於建置時連 Google Fonts，
// 原因見該檔說明。
import localFont from 'next/font/local'

export const orbitron = localFont({
  src: '../../app/track/scifi/fonts/Orbitron-latin-var.woff2',
  weight: '400 900',
  variable: '--font-scifi-orbitron',
  display: 'swap',
  preload: false, // 只有 scifi 會用到，不要在任何頁面預載（非 scifi 使用者零下載）
  // Orbitron 只含拉丁字符集，中文一律走系統字體 fallback（契約要求「中文維持系統字」）。
  fallback: ['system-ui', 'sans-serif'],
})
