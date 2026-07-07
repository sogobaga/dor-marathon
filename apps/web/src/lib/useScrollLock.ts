import { useEffect } from 'react'

// 開 modal 時鎖住「背景」捲動（iOS/Chrome 皆適用）。
// 關鍵：只有當觸控/滾輪落在「真的有可捲動內容」的 modal 捲動區（data-scroll-lock-pass）內才放行；
// 若該區內容很短、根本捲不動，就一律 preventDefault —— 否則瀏覽器會把這次捲動「外溢」到背景頁
// （這正是「短版面彈窗滑到背景、全版面彈窗正常」的原因）。桌面滾輪與行動觸控都處理。
export function useScrollLock() {
  useEffect(() => {
    // 觸控/滾輪的目標是否落在一個「真的可捲動」的放行區內
    const scrollableAncestor = (el: Element | null): boolean => {
      const pass = el && typeof el.closest === 'function'
        ? (el.closest('[data-scroll-lock-pass]') as HTMLElement | null)
        : null
      // scrollHeight 需明顯大於 clientHeight（ScrollArea 有 +1px 的回彈技巧，故用 +1 當門檻）→ 才算真的能捲
      return !!pass && pass.scrollHeight > pass.clientHeight + 1
    }
    const onTouch = (e: TouchEvent) => { if (!scrollableAncestor(e.target as Element)) e.preventDefault() }
    const onWheel = (e: WheelEvent) => { if (!scrollableAncestor(e.target as Element)) e.preventDefault() }

    document.addEventListener('touchmove', onTouch, { passive: false })
    document.addEventListener('wheel', onWheel, { passive: false })
    const prevOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.removeEventListener('touchmove', onTouch)
      document.removeEventListener('wheel', onWheel)
      document.body.style.overflow = prevOverflow
    }
  }, [])
}
