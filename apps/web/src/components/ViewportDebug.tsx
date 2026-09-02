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

  // 修復槓桿實驗 v2（2026-09-02）：v1 的 initial-scale 微擾實測無效——viewport meta 帶
  // user-scalable=no + maximum-scale=1，Safari 疑直接無視 scale 變更，槓桿沒扳動。
  // 使用者實測「轉橫轉回」「切分頁再回」皆可當場復原症狀 A → 補上兩支更接近該手勢的槓桿：
  // 🅰 重建＝<html> display:none 一幀再還原（摧毀重建整棵圖層樹，「重新掛載」的程式化近似；
  //    副作用：畫面閃一下、容器捲動位置可能重置——手動修復情境可接受）
  // 🅱 切頁＝window.open 空白分頁 0.4s 後自動關閉（逐字重演使用者實測有效的「切分頁再回」）
  const healRebuild = () => {
    try {
      const root = document.documentElement
      const prev = root.style.display
      root.style.display = 'none'
      void root.offsetHeight
      setTimeout(() => { root.style.display = prev }, 50)
    } catch { /* noop */ }
  }
  const healTabFlip = () => {
    try {
      const w = window.open('about:blank', '_blank')
      setTimeout(() => { try { w?.close() } catch { /* noop */ } }, 400)
    } catch { /* noop */ }
  }

  if (!rows) return null
  return (
    <div style={{ position: 'fixed', left: 0, right: 0, bottom: 0, zIndex: 9999, background: 'rgba(0,0,0,.82)', color: '#0f0', font: '11px/1.45 ui-monospace,monospace', padding: '6px 8px', whiteSpace: 'pre', display: 'flex', alignItems: 'flex-end', gap: 8 }}>
      <div style={{ flex: 1, pointerEvents: 'none', minWidth: 0 }}>{rows.join('\n')}</div>
      <button onClick={healRebuild}
        style={{ flexShrink: 0, background: '#0f0', color: '#000', border: 'none', borderRadius: 6, padding: '6px 8px', font: 'bold 11px ui-monospace,monospace', cursor: 'pointer' }}>
        🅰 重建
      </button>
      <button onClick={healTabFlip}
        style={{ flexShrink: 0, background: '#ff0', color: '#000', border: 'none', borderRadius: 6, padding: '6px 8px', font: 'bold 11px ui-monospace,monospace', cursor: 'pointer' }}>
        🅱 切頁
      </button>
    </div>
  )
}
