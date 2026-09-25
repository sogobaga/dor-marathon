'use client'

import { useEffect, useState } from 'react'

// 首次開跑提示（口袋模式併入專注模式，2026-09-25 CONTRACT.md §2；原 PocketModeTip.tsx 改名，key 也改新的，
// 舊 dor_track_pocket_tip_seen 使用者會再看到一次新文案的提示——一次性設計本就允許改版後重新提示）。
// 每裝置只顯示一次（localStorage 記旗標），內容固定：「跑步時請讓螢幕開著。要放口袋請先切入『專注模式』：
// 10 秒沒碰螢幕會自動上鎖防誤觸，長按 1.5 秒解鎖。按側邊鍵鎖屏會讓 GPS 暫停，解鎖後會自動接續。」
// active＝true（開跑）時檢查旗標；已看過就永遠不再顯示，不受這次追蹤結束/重開影響。
// 專注模式＝鎖定模式（2026-09-25 CONTRACT.md track_autolock）：開跑後一律直接進入專注模式；唯一例外是
// 「本裝置尚未看過這則提示」——此時 track/page.tsx 的 start() 先讓完整介面可見、不自動進入專注模式，
// 等使用者按下「知道了」（本檔 onDismiss）的瞬間才進入。SEEN_KEY 匯出供 page.tsx 在 start() 當下同步判斷
// 「這次開跑要不要先擋著、等提示關閉」，兩處必須讀同一把 key，否則會出現「提示明明顯示但已經進了專注模式看不到」
// 或反過來的不一致。
export const FOCUS_TIP_SEEN_KEY = 'dor_track_focus_tip_seen'

export default function FocusModeTip({ active, onDismiss }: {
  active: boolean
  onDismiss?: () => void // 「知道了」按下的瞬間呼叫（page.tsx 藉此命令 RaceFocusMode 進入專注模式，見該檔 openSignal prop）
}) {
  const [show, setShow] = useState(false)

  useEffect(() => {
    if (!active) return
    try {
      if (localStorage.getItem(FOCUS_TIP_SEEN_KEY) === '1') return
    } catch { /* localStorage 不可用：不阻擋追蹤本身，這次就不顯示提示 */ return }
    setShow(true)
  }, [active])

  function dismiss() {
    setShow(false)
    try { localStorage.setItem(FOCUS_TIP_SEEN_KEY, '1') } catch { /* ignore */ }
    onDismiss?.()
  }

  if (!show) return null

  return (
    <div style={{ position: 'absolute', left: 12, right: 12, top: 56, zIndex: 1000, pointerEvents: 'none' }}>
      <div style={{ pointerEvents: 'auto', background: 'var(--bg-1)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 12, padding: '12px 14px', fontSize: 13, lineHeight: 1.7, boxShadow: '0 6px 24px rgba(0,0,0,.4)' }}>
        <div>
          開跑後會自動進入「專注模式」並鎖定螢幕，可以直接放口袋；要操作請長按畫面下方的鎖頭 1.5 秒解除。按側邊鍵鎖屏會讓 GPS 暫停，解鎖後會自動接續。
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
