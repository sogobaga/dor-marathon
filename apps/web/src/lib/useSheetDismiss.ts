import { useEffect, useRef, useState } from 'react'

// 底部彈窗「下滑關閉」手勢：讓拖曳一定有反應（短清單→往下拖關閉；長清單→捲動，捲到頂再往下拖才關閉）。
// 用法：把回傳的 panelRef 掛到「彈窗面板」<div>，並用 dy 做 translateY；面板內的可捲區需標 data-scroll-lock-pass。
export function useSheetDismiss(onClose: () => void) {
  const panelRef = useRef<HTMLDivElement | null>(null)
  const [dy, setDy] = useState(0)
  const dyRef = useRef(0)
  const set = (v: number) => { dyRef.current = v; setDy(v) }

  useEffect(() => {
    const panel = panelRef.current
    if (!panel) return
    let startY = 0
    let tracking = false // 這一觸控是否還在判定中
    let dismiss = false // 已判定為「下滑關閉」手勢

    const scrollTop = () => {
      const sc = panel.querySelector('[data-scroll-lock-pass]') as HTMLElement | null
      return sc ? sc.scrollTop : 0
    }
    const onStart = (e: TouchEvent) => {
      if (e.touches.length !== 1) return
      startY = e.touches[0].clientY
      tracking = true
      dismiss = false
    }
    const onMove = (e: TouchEvent) => {
      if (!tracking) return
      const delta = e.touches[0].clientY - startY
      if (!dismiss) {
        // 僅在「往下拖 + 可捲區已在頂端」時，這一手勢才升級為下滑關閉；否則交還給原生捲動
        if (delta > 6 && scrollTop() <= 0) dismiss = true
        else if (delta < -6 || delta > 6) { tracking = false; return }
        else return
      }
      e.preventDefault() // 接管手勢：面板跟著手指走，別讓清單同時捲/回彈
      set(Math.max(0, delta))
    }
    const onEnd = () => {
      if (dismiss && dyRef.current > 110) onClose()
      else set(0)
      tracking = false
      dismiss = false
    }
    panel.addEventListener('touchstart', onStart, { passive: true })
    panel.addEventListener('touchmove', onMove, { passive: false })
    panel.addEventListener('touchend', onEnd)
    panel.addEventListener('touchcancel', onEnd)
    return () => {
      panel.removeEventListener('touchstart', onStart)
      panel.removeEventListener('touchmove', onMove)
      panel.removeEventListener('touchend', onEnd)
      panel.removeEventListener('touchcancel', onEnd)
    }
  }, [onClose])

  return { panelRef, dy }
}
