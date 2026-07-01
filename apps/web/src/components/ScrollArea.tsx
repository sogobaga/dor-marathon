import type { CSSProperties, ReactNode } from 'react'

// 捲動內容區。
// - 桌機/desktop：內層捲動 + iOS 回彈手感（容器永遠可捲 + 內層 minHeight 比容器高 1px）。
// - 手機/mobile（globals.css .app-scroll 覆寫）：改為「文件流」，交給 body 捲動 →
//   Chrome/Safari 底部工具列可隨捲動自動隱藏/顯示。
// - fixed：加 .is-fixed，手機也維持內層捲動（給 /track 等需固定畫面的頁面）。
export default function ScrollArea({
  children,
  padding,
  style,
  innerStyle,
  fixed,
}: {
  children: ReactNode
  padding?: string
  style?: CSSProperties
  innerStyle?: CSSProperties
  fixed?: boolean
}) {
  return (
    <div
      className={'app-scroll' + (fixed ? ' is-fixed' : '')}
      style={{
        flex: 1,
        minHeight: 0,
        overflowY: 'scroll',
        WebkitOverflowScrolling: 'touch',
        overscrollBehaviorY: 'auto',
        ...style,
      }}
    >
      <div className="app-scroll-inner" style={{ minHeight: 'calc(100% + 1px)', ...(padding ? { padding } : {}), ...innerStyle }}>
        {children}
      </div>
    </div>
  )
}
