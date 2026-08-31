import type { Viewport } from 'next'
import AdminChrome from '@/components/AdminChrome'

// 後台狀態列/瀏覽器 chrome 也維持暗色（覆寫前台 skin 的 theme-color；巢狀 viewport 深者優先）。
export function generateViewport(): Viewport {
  return { themeColor: '#09090f' }
}

// 後台一律維持預設暗色，不受前台 skin 影響：
// - data-skin="default" 把此子樹的 CSS 變數覆寫回暗色（即使 <html> 被 SSR 設成 warm）。
// - wrapper 自帶暗底 + .app-min-h（≥一屏、含 --app-h 安全網），避免 <html data-skin="warm"> 時
//   body 底色（奶油）透出——inline minHeight:100vh 在 viewport 單位過期時會矮一截，故掛 class。
export default function AdminLayout({ children }: { children: React.ReactNode }) {
  return (
    <div data-skin="default" className="app-min-h" style={{ background: 'var(--bg)' }}>
      <AdminChrome>{children}</AdminChrome>
    </div>
  )
}
