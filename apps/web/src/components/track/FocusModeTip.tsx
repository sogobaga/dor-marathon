'use client'

import { useEffect, useState } from 'react'

// 首次開跑提示（口袋模式併入專注模式，2026-09-25 CONTRACT.md §2；原 PocketModeTip.tsx 改名，key 也改新的，
// 舊 dor_track_pocket_tip_seen 使用者會再看到一次新文案的提示——一次性設計本就允許改版後重新提示）。
// 每裝置只顯示一次（localStorage 記旗標），內容固定：「跑步時請讓螢幕開著。要放口袋請先切入『專注模式』：
// 10 秒沒碰螢幕會自動上鎖防誤觸，長按 1.5 秒解鎖。按側邊鍵鎖屏會讓 GPS 暫停，解鎖後會自動接續。」
// active＝true（開跑）時檢查旗標；已看過就永遠不再顯示，不受這次追蹤結束/重開影響。
const SEEN_KEY = 'dor_track_focus_tip_seen'

export default function FocusModeTip({ active }: { active: boolean }) {
  const [show, setShow] = useState(false)

  useEffect(() => {
    if (!active) return
    try {
      if (localStorage.getItem(SEEN_KEY) === '1') return
    } catch { /* localStorage 不可用：不阻擋追蹤本身，這次就不顯示提示 */ return }
    setShow(true)
  }, [active])

  function dismiss() {
    setShow(false)
    try { localStorage.setItem(SEEN_KEY, '1') } catch { /* ignore */ }
  }

  if (!show) return null

  return (
    <div style={{ position: 'absolute', left: 12, right: 12, top: 56, zIndex: 1000, pointerEvents: 'none' }}>
      <div style={{ pointerEvents: 'auto', background: 'var(--bg-1)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 12, padding: '12px 14px', fontSize: 13, lineHeight: 1.7, boxShadow: '0 6px 24px rgba(0,0,0,.4)' }}>
        <div>
          跑步時請讓螢幕開著。要放口袋請先切入「專注模式」：10 秒沒碰螢幕會自動上鎖防誤觸，長按 1.5 秒解鎖。按側邊鍵鎖屏會讓 GPS 暫停，解鎖後會自動接續。
        </div>
        {/* 「知道了」＝綠底主按鈕、寬版占滿（2026-09-25 使用者要求：不用灰色小膠囊；與 track 頁主按鈕同色系 --fug/--fug-ink） */}
        <button
          onClick={dismiss}
          aria-label="知道了"
          style={{ display: 'block', width: '100%', marginTop: 10, background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', fontSize: 14, fontWeight: 800, cursor: 'pointer', padding: '10px 12px', borderRadius: 10, fontFamily: 'inherit' }}
        >知道了</button>
      </div>
    </div>
  )
}
