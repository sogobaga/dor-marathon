'use client'

import { useEffect, useLayoutEffect } from 'react'
import { usePathname } from 'next/navigation'
import { publicSettingsApi } from '@/lib/api'

// 前台 skin（主題外觀）套用：讀取系統設定 active_skin，設到 <html data-skin>。
// 後台（/admin）一律維持預設暗色，不受 skin 影響。用 localStorage 快取避免載入時閃一下預設色。
const SKINS = ['default', 'warm'] as const
const THEME_COLOR: Record<string, string> = { default: '#09090f', warm: '#FBF4E9' }
// 於 client 用 layout effect（paint 前生效），server 退回 useEffect（no-op、不觸發 SSR 警告）
const useIso = typeof window !== 'undefined' ? useLayoutEffect : useEffect

function apply(skin: string) {
  const s = SKINS.includes(skin as any) ? skin : 'default'
  const el = document.documentElement
  if (s === 'default') el.removeAttribute('data-skin')
  else el.setAttribute('data-skin', s)
  const meta = document.querySelector('meta[name="theme-color"]')
  if (meta) meta.setAttribute('content', THEME_COLOR[s] || THEME_COLOR.default)
}

export default function SkinProvider() {
  const pathname = usePathname()
  const isAdmin = pathname?.startsWith('/admin')

  // paint 前先把外觀套對：後台→預設；前台→快取值。避免 client 導航進後台時先閃到暖色（後台須恆暗）。
  useIso(() => {
    if (isAdmin) { apply('default'); return }
    try { apply(localStorage.getItem('dor_skin') || 'default') } catch { /* ignore */ }
  }, [isAdmin])

  // 前台：向伺服器取最新 skin（非同步），更新快取與外觀。
  useEffect(() => {
    if (isAdmin) return
    let alive = true
    publicSettingsApi.get().then((r) => {
      if (!alive) return
      const skin = r.settings?.active_skin || 'default'
      try { localStorage.setItem('dor_skin', skin) } catch { /* ignore */ }
      apply(skin)
    }).catch(() => { /* 取不到就沿用快取/預設 */ })
    return () => { alive = false }
  }, [isAdmin])

  return null
}
