import { useEffect } from 'react'

// 開 modal 期間鎖住 document 捲動（belt-and-suspenders）。
// 主要防漏靠「把 modal portal 到 body（脫離背景捲動容器）＋ 其捲動區 overscroll-behavior:contain」；
// 此處只再把 body 鎖住，避免拖曳遮罩區時帶動整頁。不攔 modal 自己的捲動 → 短清單仍可回彈、長清單可捲，體驗一致。
export function useScrollLock() {
  useEffect(() => {
    const prevOverflow = document.body.style.overflow
    const prevOverscroll = document.body.style.overscrollBehavior
    document.body.style.overflow = 'hidden'
    document.body.style.overscrollBehavior = 'contain'
    return () => {
      document.body.style.overflow = prevOverflow
      document.body.style.overscrollBehavior = prevOverscroll
    }
  }, [])
}
