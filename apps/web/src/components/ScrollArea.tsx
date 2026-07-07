import type { CSSProperties, ReactNode } from 'react'

// 可捲動內容區：即使內容沒滿版，也保留 iOS 上下滑動的回彈手感。
// 作法：容器永遠可捲動（overflow:scroll）+ 內層 minHeight 比容器高 1px。
export default function ScrollArea({
  children,
  padding,
  style,
  innerStyle,
  lockPass,
}: {
  children: ReactNode
  padding?: string
  style?: CSSProperties
  innerStyle?: CSSProperties
  lockPass?: boolean // 搭配 useScrollLock：標記此區為「modal 內允許捲動」的容器
}) {
  return (
    <div
      data-scroll-lock-pass={lockPass ? '' : undefined}
      style={{
        flex: 1,
        minHeight: 0,
        overflowY: 'scroll',
        WebkitOverflowScrolling: 'touch',
        overscrollBehavior: 'contain', // 防捲動外溢：在 modal 內滑動不會帶到背景頁（元素內回彈仍保留）
        touchAction: 'pan-y',
        ...style,
      }}
    >
      <div style={{ minHeight: 'calc(100% + 1px)', ...(padding ? { padding } : {}), ...innerStyle }}>
        {children}
      </div>
    </div>
  )
}
