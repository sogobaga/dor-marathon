'use client'

import { useEffect, useState } from 'react'

/**
 * 視窗診斷條：只有網址帶 ?vpdebug=1 才啟用（未帶參數時 effect 立刻 return，對一般使用者零成本）。
 * 開發端在 Windows 無法重現 iOS，唯一可靠的資訊來源是請使用者在「症狀發生的那一刻」截圖回傳。
 * 判讀：
 *  ・回前景後 dvh/lvh/icb/vv 全等於畫面的 ~83% → 瀏覽器 viewport 整組過期，CSS 與 JS 都救不了（症狀 A 本質）
 *  ・lvh 正確而 dvh 偏小 → 有純 CSS 解（根容器改 100lvh），但需先有此數據才可下賭注
 *  ・鍵盤彈出時 dvh 不變、只有 vv 變小 → 症狀 B 的前提成立，本方案已根治
 */
export default function ViewportDebug() {
  const [rows, setRows] = useState<string[] | null>(null)

  useEffect(() => {
    let on = false
    try { on = new URLSearchParams(window.location.search).get('vpdebug') === '1' } catch { /* noop */ }
    if (!on) return

    const probe = document.createElement('div')
    probe.style.cssText = 'position:absolute;top:0;left:-9999px;width:1px;pointer-events:none'
    document.body.appendChild(probe)
    const unit = (v: string) => { probe.style.height = ''; probe.style.height = v; return Math.round(probe.getBoundingClientRect().height) || -1 }

    const tick = () => {
      const vv = window.visualViewport
      const root = document.documentElement
      setRows([
        `inner ${window.innerHeight} · icb ${root.clientHeight} · screen ${window.screen.height}`,
        `vv ${vv ? Math.round(vv.height) : '-'} · off ${vv ? Math.round(vv.offsetTop) : '-'} · scale ${vv ? vv.scale.toFixed(2) : '-'}`,
        `vh ${unit('100vh')} · dvh ${unit('100dvh')} · svh ${unit('100svh')} · lvh ${unit('100lvh')}`,
        `--app-h ${root.style.getPropertyValue('--app-h') || '(none)'} · ae ${document.activeElement?.tagName || '-'}`,
      ])
    }
    tick()
    const id = window.setInterval(tick, 400)
    return () => { window.clearInterval(id); probe.remove() }
  }, [])

  if (!rows) return null
  return (
    <div style={{ position: 'fixed', left: 0, right: 0, bottom: 0, zIndex: 9999, background: 'rgba(0,0,0,.82)', color: '#0f0', font: '11px/1.45 ui-monospace,monospace', padding: '6px 8px', pointerEvents: 'none', whiteSpace: 'pre' }}>
      {rows.join('\n')}
    </div>
  )
}
