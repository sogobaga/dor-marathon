'use client'

import { useEffect, useState } from 'react'

// 橫向偵測（含防抖）：橫向需持續 ~0.5 秒才回 true（避免旋轉/晃動瞬間狂閃）；轉回直立立即 false。
// 用 resize + orientationchange（iOS Safari 對 matchMedia change 不可靠），並以視窗長寬保底判斷。
export function useIsLandscape(): boolean {
  const [land, setLand] = useState(false)
  useEffect(() => {
    if (typeof window === 'undefined') return
    let t: ReturnType<typeof setTimeout> | undefined
    const isLand = () => (window.matchMedia?.('(orientation: landscape)').matches) ?? (window.innerWidth > window.innerHeight)
    const check = () => {
      clearTimeout(t)
      if (isLand()) t = setTimeout(() => { if (isLand()) setLand(true) }, 500)
      else setLand(false)
    }
    check()
    const mq = window.matchMedia?.('(orientation: landscape)')
    window.addEventListener('resize', check)
    window.addEventListener('orientationchange', check)
    mq?.addEventListener?.('change', check)
    return () => {
      clearTimeout(t)
      window.removeEventListener('resize', check)
      window.removeEventListener('orientationchange', check)
      mq?.removeEventListener?.('change', check)
    }
  }, [])
  return land
}
