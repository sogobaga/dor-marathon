'use client'

import { useEffect, useRef } from 'react'

// 修 iOS Safari「切到背景再回到分頁」根容器塌陷的問題：
// 全站高度純靠 CSS 100dvh，iOS Safari 回前景時內部 viewport 矩形可能停在舊值且不補發 resize，
// 導致 100dvh（與 InterstitialAd 的 position:fixed;inset:0）都讀到過期、偏矮的值。
// 這裡改用 JS 主動量測寫入 --app-h CSS 變數，各處以 var(--app-h, 100dvh) 取代直接寫死 100dvh。
export default function ViewportHeightFix() {
  const lastRef = useRef(0)
  const timersRef = useRef<ReturnType<typeof setTimeout>[]>([])
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)

  useEffect(() => {
    const measure = () => {
      // 取 max(innerHeight, visualViewport.height) 而非其一：
      // - iOS 鍵盤彈出時 visualViewport 變矮但 innerHeight 不變 → 取 max 避免版面因鍵盤跳動。
      // - 回前景時 innerHeight 常停在過期偏矮的舊值，visualViewport 則多半已更新為正確值 → 取 max 拿到對的高度。
      const h = Math.max(window.innerHeight, window.visualViewport?.height ?? 0)
      if (h > 0 && h !== lastRef.current) {
        lastRef.current = h
        document.documentElement.style.setProperty('--app-h', `${Math.round(h)}px`)
      }
    }

    const clearTimers = () => {
      timersRef.current.forEach(clearTimeout)
      timersRef.current = []
    }

    const onResume = () => {
      // 推 Safari 重新計算 viewport；html/body 本來就 overflow:hidden 鎖版，scrollTo 不影響版面。
      // 只在 scrollY 已是 0 時做（此時 scrollTo 是純 nudge）：若某頁允許文件捲動，回前景不該把人彈回頂端。
      try { if (window.scrollY === 0) window.scrollTo(0, 0) } catch {}
      measure()
      clearTimers()
      // iOS 回前景後 innerHeight/visualViewport 數值可能延遲才更新，多次補量測。
      timersRef.current = [100, 400, 1000].map((ms) => setTimeout(measure, ms))
    }

    const onVisibilityChange = () => {
      if (!document.hidden) onResume()
    }

    measure() // 首次 mount 立即量測一次

    window.addEventListener('resize', measure)
    window.addEventListener('orientationchange', measure)
    window.addEventListener('pageshow', onResume)
    window.addEventListener('focus', onResume)
    document.addEventListener('visibilitychange', onVisibilityChange)
    window.visualViewport?.addEventListener('resize', measure)
    window.visualViewport?.addEventListener('scroll', measure)

    // 最後保險：對「Safari 完全不補發任何事件」的情況，可見時每 2 秒核對一次。
    // 只讀 innerHeight、值沒變就不寫 DOM，成本可忽略。
    intervalRef.current = setInterval(() => {
      if (!document.hidden) measure()
    }, 2000)

    return () => {
      window.removeEventListener('resize', measure)
      window.removeEventListener('orientationchange', measure)
      window.removeEventListener('pageshow', onResume)
      window.removeEventListener('focus', onResume)
      document.removeEventListener('visibilitychange', onVisibilityChange)
      window.visualViewport?.removeEventListener('resize', measure)
      window.visualViewport?.removeEventListener('scroll', measure)
      if (intervalRef.current) clearInterval(intervalRef.current)
      clearTimers()
    }
  }, [])

  return null
}
