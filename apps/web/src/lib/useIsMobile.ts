'use client'

import { useEffect, useState } from 'react'

// 判定「真手機」：窄視窗，或觸控裝置（primary pointer 粗）且非平板寬。與 globals.css 的 media query 一致，
// 兩者務必同步（一個決定 JS 版面/內距，一個決定假動態島/外框顯示）。含 change 監聽，旋轉/縮放即時更新。
export const MOBILE_MQ = '(max-width: 500px), (pointer: coarse) and (max-width: 600px)'

export function useIsMobile(): boolean {
  const [mobile, setMobile] = useState(false)
  useEffect(() => {
    const mql = window.matchMedia(MOBILE_MQ)
    const update = () => setMobile(mql.matches)
    update()
    mql.addEventListener('change', update)
    return () => mql.removeEventListener('change', update)
  }, [])
  return mobile
}

// 是否為「手機」但與方向無關（橫向時寬度會 > 600 讓 useIsMobile 失準）：觸控裝置 + 短邊 ≤ 500（排除平板）。
// 用於「橫向請轉直」這類需在任一方向都正確判斷是不是手機的情境。
export function useIsPhone(): boolean {
  const [phone, setPhone] = useState(false)
  useEffect(() => {
    if (typeof window === 'undefined') return
    const check = () => {
      const coarse = window.matchMedia?.('(pointer: coarse)').matches ?? false
      setPhone(coarse && Math.min(window.innerWidth, window.innerHeight) <= 500)
    }
    check()
    window.addEventListener('resize', check)
    window.addEventListener('orientationchange', check)
    return () => {
      window.removeEventListener('resize', check)
      window.removeEventListener('orientationchange', check)
    }
  }, [])
  return phone
}
