import { useEffect } from 'react'

// 開 modal 時鎖住「背景」捲動（iOS Safari 尤其需要——fixed 覆蓋層仍會讓底下頁面/捲動容器被帶動）。
// 作法：攔截 document 的 touchmove，只有帶 data-scroll-lock-pass 的容器（＝modal 自己的捲動區）內
// 才放行捲動，其餘一律 preventDefault → 背景完全不動；桌面則鎖 body overflow。
// 用法：在 modal 元件內呼叫 useScrollLock()，並在該 modal 的捲動區加 data-scroll-lock-pass（ScrollArea 用 lockPass）。
export function useScrollLock() {
  useEffect(() => {
    const onMove = (e: TouchEvent) => {
      const t = e.target as Element | null
      // 放行：觸控落在 modal 的捲動區內（其捲動由 overscroll-behavior:contain 收邊、不外溢）
      if (t && typeof t.closest === 'function' && t.closest('[data-scroll-lock-pass]')) return
      e.preventDefault() // 其餘（背景頁、backdrop、modal 標題列）→ 禁止捲動
    }
    document.addEventListener('touchmove', onMove, { passive: false })
    const prevOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden' // 桌面滑鼠捲動一併鎖
    return () => {
      document.removeEventListener('touchmove', onMove)
      document.body.style.overflow = prevOverflow
    }
  }, [])
}
