import type { CSSProperties, ReactNode } from 'react'

// 可捲動內容區：即使內容沒滿版，也保留 iOS 上下滑動的回彈手感。
// 作法：容器永遠可捲動（overflow:scroll）+ 內層 minHeight 比容器高 1px。
export default function ScrollArea({
  children,
  padding,
  style,
  innerStyle,
}: {
  children: ReactNode
  padding?: string
  style?: CSSProperties
  innerStyle?: CSSProperties
}) {
  return (
    <div
      style={{
        flex: 1,
        minHeight: 0,
        overflowY: 'scroll',
        WebkitOverflowScrolling: 'touch',
        overscrollBehaviorY: 'auto',
        ...style,
      }}
    >
      <div style={{ minHeight: 'calc(100% + 1px)', ...(padding ? { padding } : {}), ...innerStyle }}>
        {children}
      </div>
    </div>
  )
}
