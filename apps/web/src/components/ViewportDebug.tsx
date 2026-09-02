'use client'

import { useEffect, useState } from 'react'
import { APP_VERSION } from '@/lib/version'

/**
 * 視窗診斷條：網址帶 ?vpdebug=1 啟用（並記進 localStorage，之後每次載入都顯示——QR 掃碼的網址無法手動加參數，
 * 需要「先開一次、再走 QR 路徑」才看得到）；?vpdebug=0 關閉並清除記憶。未啟用時 effect 立刻 return，對一般使用者零成本。
 * 開發端在 Windows 無法重現 iOS，唯一可靠的資訊來源是請使用者在「症狀發生的那一刻」截圖回傳。
 * 判讀：
 *  ・回前景後 dvh/lvh/icb/vv 全等於畫面的 ~83% → 瀏覽器 viewport 整組過期，CSS 與 JS 都救不了（症狀 A 本質）
 *  ・lvh 正確而 dvh 偏小 → 有純 CSS 解（根容器改 100lvh），但需先有此數據才可下賭注
 *  ・鍵盤彈出時 dvh 不變、只有 vv 變小 → 症狀 B 的前提成立，本方案已根治
 *  ・2026-09-02 新判讀：各數值全部「正確」但畫面底部露帶 → root 圖層被合成器上移 lvh−svh
 *    （症狀 A 最終定性）；使用者實測「轉橫轉回」「切分頁再回」皆可當場復原 → 圖層重新提交即治。
 *  ・load 列（layout.tsx 的 LOAD_SNAPSHOT_JS）：vis=hidden→visible@Xms 代表首次繪製發生在分頁尚未可見時；
 *    ih0/ch0 與後來的 inner 不同、或 resize 很晚才到 → 視窗尺寸在載入中途變過（QR 路徑的嫌疑成因）。
 *  ・tap 列：症狀發生時點一下「帶子」本身——Δ>0 ＝ 觸點落在文件之外（圖層上移鐵證）；Δ≤0 且有 tgt ＝ 帶子是
 *    真實 DOM 空間（預留高度假說成立）；一直是 '-' ＝ 觸控根本沒送進網頁。
 */
declare global {
  interface Window {
    /** layout.tsx 開機腳本寫入的載入瞬間快照；ar＝到站自動重載的決策（arrival@ms／tap@ms／skip:原因） */
    __dorVis?: { l: string; t: number; pr: boolean; vt: number; n: string; ih: number; ch: number; sh: number; rt: number; rih: number; ar: string }
    /** 白幕重載（layout.tsx 開機腳本提供）：症狀 A 的治療手段，ViewportHeightFix 觸點越界偵測與診斷面板共用 */
    __dorVeilReload?: (why: string) => void
  }
}

const LS_KEY = 'dor.vpdebug'

export default function ViewportDebug() {
  const [rows, setRows] = useState<string[] | null>(null)

  useEffect(() => {
    let on = false
    try {
      const q = new URLSearchParams(window.location.search).get('vpdebug')
      if (q === '1') { on = true; localStorage.setItem(LS_KEY, '1') }
      else if (q === '0') { on = false; localStorage.removeItem(LS_KEY) }
      else on = localStorage.getItem(LS_KEY) === '1'
    } catch { /* noop */ }
    if (!on) return

    const probe = document.createElement('div')
    probe.style.cssText = 'position:absolute;top:0;left:-9999px;width:1px;pointer-events:none'
    document.body.appendChild(probe)
    const unit = (v: string) => { probe.style.height = ''; probe.style.height = v; return Math.round(probe.getBoundingClientRect().height) || -1 }

    let lastTap = '-'
    const onTap = (e: PointerEvent) => {
      const icb = document.documentElement.clientHeight
      const tgt = (e.target as Element | null)?.tagName || '?'
      const d = Math.round(e.clientY - icb)
      lastTap = `y${Math.round(e.clientY)} icb${icb} Δ${d > 0 ? '+' : ''}${d} ${tgt} @${new Date().toTimeString().slice(0, 8)}`
    }
    window.addEventListener('pointerdown', onTap, { capture: true, passive: true })

    let ref = '-'
    try { ref = document.referrer ? new URL(document.referrer).host : '-' } catch { /* noop */ }

    const tick = () => {
      const vv = window.visualViewport
      const root = document.documentElement
      const s = window.__dorVis
      const loadRow = s
        ? `load vis=${s.l}${s.vt >= 0 ? `→vis@${s.vt}ms` : '→never'} nav=${s.n || '?'}${s.pr ? ' prerender' : ''} · ih0 ${s.ih} ch0 ${s.ch} · rs ${s.rt >= 0 ? `@${s.rt}ms→${s.rih}` : '-'} · ref ${ref} · ar ${s.ar || '-'}`
        : `load (no snapshot) · ref ${ref}`
      setRows([
        `inner ${window.innerHeight} · icb ${root.clientHeight} · screen ${window.screen.height}`,
        `vv ${vv ? Math.round(vv.height) : '-'} · off ${vv ? Math.round(vv.offsetTop) : '-'} · scale ${vv ? vv.scale.toFixed(2) : '-'}`,
        `vh ${unit('100vh')} · dvh ${unit('100dvh')} · svh ${unit('100svh')} · lvh ${unit('100lvh')}`,
        `--app-h ${root.style.getPropertyValue('--app-h') || '(none)'} · ae ${document.activeElement?.tagName || '-'}`,
        loadRow,
        `heal ${root.dataset.vpheal || '-'}`, // 自癒儀表：觸點越界偵測觸發過的 tabflip 紀錄
        `tap ${lastTap}`, // 最後一次觸點：y vs icb 的差值 Δ 直接判別「圖層位移」或「DOM 預留空間」
        `ua ${navigator.userAgent}`, // UA 全文：iOS 26 Safari 本體會把 OS 凍結成 18_x；回報真實版本＋無 Safari/ 的是裸 WebKit 情境（v758 閘門依據）
        APP_VERSION, // 版號直接進面板：面板會蓋住頁尾版號，截圖回傳時才能確認當下版本（使用者要求）
      ])
    }
    tick()
    const id = window.setInterval(tick, 400)
    return () => { window.clearInterval(id); probe.remove(); window.removeEventListener('pointerdown', onTap, { capture: true }) }
  }, [])

  // 修復槓桿實驗 v2（2026-09-02）：v1 的 initial-scale 微擾實測無效——viewport meta 帶
  // user-scalable=no + maximum-scale=1，Safari 疑直接無視 scale 變更，槓桿沒扳動。
  // 使用者實測「轉橫轉回」「切分頁再回」皆可當場復原症狀 A → 補上兩支更接近該手勢的槓桿：
  // 🅰 重建＝<html> display:none 一幀再還原（實測無效，已移除）
  // 🅱 切頁＝window.open 空白分頁 0.4s 後自動關閉（逐字重演使用者實測有效的「切分頁再回」；實測有效）
  // 🔄 重載＝location.reload()：驗證使用者提議的「白幕＋自動刷新」方案的引擎是否真的能治
  //    （必須用按鈕而非下拉更新：下拉手勢本身會動到捲動視圖，會混淆判讀）
  const healTabFlip = () => {
    try {
      const w = window.open('about:blank', '_blank')
      setTimeout(() => { try { w?.close() } catch { /* noop */ } }, 400)
    } catch { /* noop */ }
  }
  const reload = () => { try { window.location.reload() } catch { /* noop */ } }

  if (!rows) return null
  const btn: React.CSSProperties = { flexShrink: 0, background: '#ff0', color: '#000', border: 'none', borderRadius: 6, padding: '6px 8px', font: 'bold 11px ui-monospace,monospace', cursor: 'pointer' }
  return (
    <div style={{ position: 'fixed', left: 0, right: 0, bottom: 0, zIndex: 9999, background: 'rgba(0,0,0,.82)', color: '#0f0', font: '11px/1.45 ui-monospace,monospace', padding: '6px 8px', whiteSpace: 'pre-wrap', wordBreak: 'break-all', display: 'flex', alignItems: 'flex-end', gap: 8 }}>
      <div style={{ flex: 1, pointerEvents: 'none', minWidth: 0 }}>{rows.join('\n')}</div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        <button onClick={healTabFlip} style={btn}>🅱 修復</button>
        <button onClick={reload} style={btn}>🔄 重載</button>
      </div>
    </div>
  )
}
