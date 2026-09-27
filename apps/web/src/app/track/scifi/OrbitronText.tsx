'use client'

// 未來科幻世界（scifi）變體專用：持有 Orbitron 字型 className 的最小 client 元件（CONTRACT.md §4.4）。
// 由 RaceFocusMode.tsx 以 `next/dynamic(() => import('./scifi/OrbitronText'), { ssr:false })` 動態載入，
// 且只在該檔 Metric 元件 `scifi` 為真時才把本元件放進 JSX 樹——非白名單使用者／偏好關閉時這個節點
// 根本不會被建立，dynamic import 的 import() 就不會觸發，本檔（與它靜態 import 的 ./font 內的
// next/font/google 呼叫）不會被下載，不會多一個 CSS chunk（2026-09-27 review 修正，見 RaceFocusMode.tsx
// 與 font.ts 檔頭說明）。純顯示用途，不含任何邏輯。
import type { CSSProperties, ReactNode } from 'react'
import { orbitron } from './font'

export default function OrbitronText({
  children, style, className,
}: {
  children: ReactNode
  style?: CSSProperties
  className?: string
}) {
  return (
    <div className={className ? `${orbitron.className} ${className}` : orbitron.className} style={style}>
      {children}
    </div>
  )
}
