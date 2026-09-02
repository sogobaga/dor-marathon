'use client'

import { useEffect, useState } from 'react'
import { APP_VERSION } from '@/lib/version'

/**
 * 視窗診斷條：只有網址帶 ?vpdebug=1 才啟用（未帶參數時 effect 立刻 return，對一般使用者零成本）。
 * 開發端在 Windows 無法重現 iOS，唯一可靠的資訊來源是請使用者在「症狀發生的那一刻」截圖回傳。
 * 判讀：
 *  ・回前景後 dvh/lvh/icb/vv 全等於畫面的 ~83% → 瀏覽器 viewport 整組過期，CSS 與 JS 都救不了（症狀 A 本質）
 *  ・lvh 正確而 dvh 偏小 → 有純 CSS 解（根容器改 100lvh），但需先有此數據才可下賭注
 *  ・鍵盤彈出時 dvh 不變、只有 vv 變小 → 症狀 B 的前提成立，本方案已根治
 *  ・2026-09-02 新判讀：各數值全部「正確」但畫面底部露帶 → root 圖層被合成器上移 lvh−svh
 *    （症狀 A 最終定性）；使用者實測「轉橫轉回」「切分頁再回」皆可當場復原 → 圖層重新提交即治。
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
        APP_VERSION, // 版號直接進面板：面板會蓋住頁尾版號，截圖回傳時才能確認當下版本（使用者要求）
      ])
    }
    tick()
    const id = window.setInterval(tick, 400)
    return () => { window.clearInterval(id); probe.remove() }
  }, [])

  // 「🔧 修復」＝程式化的等效轉向：真實改變 initial-scale（非 v707 那種解析值等價的改寫）逼 WebKit
  // 重算 viewport 與圖層樹，150ms 後還原。依 2026-09-02 使用者實驗：轉橫轉回／切分頁再回都能讓
  // 症狀 A（root 圖層被上移 lvh−svh）當場復原 → 「圖層重新提交」即治；這顆按鈕就是在驗證
  // 程式化觸發是否等效——若實測有效，下一步才把它自動接到「分頁還原載入」時機（tombstone 規則：
  // 合成器干預需先有 A/B 證據才可自動化）。
  // ⚠️ generateViewport 設了 maximumScale=1 會把放大夾回去，blip 字串必須同步抬高 maximum-scale。
  const blip = () => {
    try {
      const m = document.querySelector('meta[name="viewport"]')
      if (!m) return
      const orig = m.getAttribute('content') || ''
      if (!orig) return
      const blipped = orig
        .replace(/initial-scale=[0-9.]+/, 'initial-scale=1.001')
        .replace(/maximum-scale=[0-9.]+/, 'maximum-scale=1.001')
      m.setAttribute('content', blipped)
      setTimeout(() => { try { m.setAttribute('content', orig) } catch { /* noop */ } }, 150)
    } catch { /* noop */ }
  }

  if (!rows) return null
  return (
    <div style={{ position: 'fixed', left: 0, right: 0, bottom: 0, zIndex: 9999, background: 'rgba(0,0,0,.82)', color: '#0f0', font: '11px/1.45 ui-monospace,monospace', padding: '6px 8px', whiteSpace: 'pre', display: 'flex', alignItems: 'flex-end', gap: 8 }}>
      <div style={{ flex: 1, pointerEvents: 'none', minWidth: 0 }}>{rows.join('\n')}</div>
      <button onClick={blip}
        style={{ flexShrink: 0, background: '#0f0', color: '#000', border: 'none', borderRadius: 6, padding: '6px 10px', font: 'bold 11px ui-monospace,monospace', cursor: 'pointer' }}>
        🔧 修復
      </button>
    </div>
  )
}
