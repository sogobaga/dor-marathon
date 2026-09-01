'use client'

import { useEffect, useRef, useState } from 'react'
import { usePathname } from 'next/navigation'

// 版本更新通知：偵測到伺服器已部署新版 → 卡片通知＋倒數自動重新整理。
//
// 背景：PWA（standalone）沒有網址列可手動重整，iOS 又常把主畫面 App 直接從記憶體恢復
//（不重新載入），使用者可能停在舊版很久；本站的 service worker 只管推播、不攔資源，
// 所以「重新載入」本身就能拿到新版——缺的只是知道「該重載了」的時機與介面。
//
// 檢查時機：啟動後 10s、回前景（PWA 從記憶體恢復的關鍵時刻）、每 15 分鐘輪詢；
// 比對 /app-version（no-store，由 Next 伺服器回答）與 build 時內聯的 NEXT_PUBLIC_APP_VERSION。
//
// 自動更新的安全界線：
// ・/track 跑步中絕不打擾——不檢查、不顯示、倒數中途進入 /track 立即取消且不視同「稍後」
//   （被動打斷≠拒絕；跑完離開後重新提醒補上更新）。跑步紀錄不可被 reload 打斷。
// ・焦點在輸入框（打字中）只顯示卡片、不倒數，避免 reload 吃掉打到一半的留言；倒數途中開始打字則暫停。
// ・其餘情況倒數 5 秒自動 reload；「稍後」＝本工作階段對這個版本不再提醒（下個版本照常通知）。
// z-index 1450：> 面板(500)/安裝引導(1400)、< 蓋板廣告(2500)。

const CHECK_MIN_GAP_MS = 60 * 1000
const POLL_MS = 5 * 60 * 1000   // 15 分鐘實測太鈍（本站一天推十餘版，部署 5-8 分鐘完成，使用者等不到）
const FIRST_DELAY_MS = 10 * 1000
const SECOND_DELAY_MS = 90 * 1000 // 首查常落在「推送後、部署完成前」→ 90 秒補一查接住剛部署完的版本
const COUNTDOWN_S = 5
const DISMISS_KEY = 'dor.updDismissed'

const CUR = process.env.NEXT_PUBLIC_APP_VERSION || ''

function typingNow(): boolean {
  const el = document.activeElement as HTMLElement | null
  if (!el) return false
  return el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT' || el.isContentEditable
}

export default function UpdateNotice() {
  const pathname = usePathname()
  const [newVer, setNewVer] = useState<string | null>(null)
  const [count, setCount] = useState<number | null>(null)
  const lastCheckRef = useRef(0)

  const onTrack = !!pathname?.startsWith('/track')

  useEffect(() => {
    if (onTrack) return
    let alive = true
    const check = async () => {
      if (document.hidden || !CUR) return
      const now = Date.now()
      if (now - lastCheckRef.current < CHECK_MIN_GAP_MS) return // 回前景+輪詢可能重疊，節流
      lastCheckRef.current = now
      try {
        const res = await fetch('/app-version', { cache: 'no-store' })
        if (!res.ok) return
        const data = (await res.json()) as { version?: string }
        if (!alive || !data.version || data.version === 'dev' || data.version === CUR) return
        try { if (sessionStorage.getItem(DISMISS_KEY) === data.version) return } catch { /* ignore */ }
        setNewVer(data.version)
      } catch { /* 離線／部署切換瞬間：靜默略過，下次再查 */ }
    }
    const t = setTimeout(check, FIRST_DELAY_MS)
    const t2 = setTimeout(check, SECOND_DELAY_MS)
    const iv = setInterval(check, POLL_MS)
    const onVis = () => { if (!document.hidden) check() }
    document.addEventListener('visibilitychange', onVis)
    window.addEventListener('pageshow', onVis) // Safari bfcache 還原不一定發 visibilitychange，補一手
    return () => { alive = false; clearTimeout(t); clearTimeout(t2); clearInterval(iv); document.removeEventListener('visibilitychange', onVis); window.removeEventListener('pageshow', onVis) }
  }, [onTrack])

  // 倒數自動更新。打字中不啟動；倒數途中打字＝暫停在當前秒數；途中進 /track＝取消（不視同稍後，跑完再提醒）。
  useEffect(() => {
    if (!newVer) return
    const iv = setInterval(() => {
      if (window.location.pathname.startsWith('/track')) {
        // 取消但**不記**「稍後」：進跑步頁是被動打斷、不是使用者拒絕，
        // 跑完離開 /track 後偵測器重啟，這個版本要再次提醒並補上更新。
        clearInterval(iv)
        setNewVer(null); setCount(null)
        return
      }
      if (typingNow()) return
      setCount((c) => {
        const next = (c == null ? COUNTDOWN_S : c) - 1
        if (next <= 0) { clearInterval(iv); window.location.reload(); return 0 }
        return next
      })
    }, 1000)
    if (!typingNow()) setCount(COUNTDOWN_S)
    return () => clearInterval(iv)
  }, [newVer])

  if (!newVer || onTrack) return null

  const later = () => {
    try { sessionStorage.setItem(DISMISS_KEY, newVer) } catch { /* ignore */ }
    setNewVer(null); setCount(null)
  }

  return (
    // data-skin="default"：固定暗底亮字，不隨前台 skin 變色（比照事件面板慣例）
    <div data-skin="default" style={wrap} role="alert" aria-label="版本更新">
      <div style={{ minWidth: 0, flex: 1 }}>
        <div style={{ fontWeight: 800, fontSize: 13.5 }}>新版本已推出</div>
        <div style={{ fontSize: 11.5, color: 'rgba(255,255,255,.6)', marginTop: 1 }}>
          {newVer}{count != null ? `・${count} 秒後自動更新` : ''}
        </div>
      </div>
      <button onClick={() => window.location.reload()} style={nowBtn}>立即更新</button>
      <button onClick={later} style={laterBtn}>稍後</button>
    </div>
  )
}

const wrap: React.CSSProperties = {
  position: 'fixed', left: 12, right: 12, margin: '0 auto', maxWidth: 400,
  bottom: 'calc(env(safe-area-inset-bottom, 0px) + 84px)',
  zIndex: 1450,
  display: 'flex', alignItems: 'center', gap: 10,
  background: '#101218', color: '#fff',
  border: '1px solid rgba(255,255,255,.14)', borderRadius: 14,
  padding: '12px 14px', boxShadow: '0 16px 40px rgba(0,0,0,.5)',
}
const nowBtn: React.CSSProperties = {
  flexShrink: 0, border: 'none', borderRadius: 9, padding: '9px 14px',
  fontSize: 13, fontWeight: 800, background: '#1fae66', color: '#fff', cursor: 'pointer', fontFamily: 'inherit',
}
const laterBtn: React.CSSProperties = {
  flexShrink: 0, border: '1px solid rgba(255,255,255,.2)', borderRadius: 9, padding: '9px 12px',
  fontSize: 13, background: 'rgba(255,255,255,.06)', color: 'rgba(255,255,255,.75)', cursor: 'pointer', fontFamily: 'inherit',
}
