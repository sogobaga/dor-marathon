'use client'

import { useEffect, useRef, useState } from 'react'
import { usePathname } from 'next/navigation'
import { MOBILE_MQ } from '@/lib/useIsMobile'
import { useUser } from '@/lib/userAuth'
import { isInAppBrowser } from './InAppBrowserNotice'

// PWA 安裝引導（前台）：溫和提示手機瀏覽器使用者「加入主畫面」。
// 動機除了體驗（全螢幕、開啟快），還有工程面：standalone 模式沒有 Safari 動態工具列，
// ViewportHeightFix 記載的那整類「合成器圖層錯位／視窗高度過期」病態直接消失——這是根本解。
//
// 顯示條件（全部成立才排程顯示，延遲 4s 避免與蓋板廣告搶第一眼）：
//   已登入（訪客還在試水溫，別急著推安裝）＋手機（MOBILE_MQ）＋非 standalone（已安裝就永遠不吵）
//   ＋非 in-app 瀏覽器（LINE/FB 裝不了 PWA，那是 InAppBrowserNotice 的戰場）
//   ＋未被拒絕：X／「暫時不用」＝14 天後再提醒；「不再提醒」＝永久關閉；安裝成功（appinstalled）＝永久關閉。
// 路由排除：/admin（後台自用）、/m/（分享落地頁，訪客導向）、/track（跑步中絕不打擾）。
//
// 平台分支：iOS 沒有可程式觸發的安裝 API → 給「分享→加入主畫面」兩步教學；
// Android Chrome 攔 beforeinstallprompt → 一鍵喚起原生安裝框；攔不到（條件未達/其他瀏覽器）→ 選單教學。
// z-index 1400：> 可拖曳面板(500)、< 蓋板廣告(2500)。僅手機顯示，桌機不渲染 → 毋須 phone-shell overlayMount portal。

const SNOOZE_KEY = 'dor.pwa.snoozeUntil'
const OFF_KEY = 'dor.pwa.off'
const SNOOZE_MS = 14 * 24 * 3600 * 1000
const SHOW_DELAY_MS = 4000

interface BeforeInstallPromptEvent extends Event {
  prompt: () => Promise<void>
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>
}

function isStandalone(): boolean {
  try {
    if (window.matchMedia('(display-mode: standalone)').matches) return true
    return (navigator as { standalone?: boolean }).standalone === true // iOS Safari 專屬旗標
  } catch { return false }
}

function isIOS(): boolean {
  const ua = navigator.userAgent || ''
  if (/iPhone|iPad|iPod/.test(ua)) return true
  return /Mac/.test(ua) && (navigator.maxTouchPoints || 0) > 1 // iPadOS 13+ 偽裝桌面版 UA
}

export default function PwaInstallPrompt() {
  const pathname = usePathname()
  const user = useUser()
  const [show, setShow] = useState(false)
  const [ios, setIos] = useState(false)
  const [canNative, setCanNative] = useState(false)
  const deferredRef = useRef<BeforeInstallPromptEvent | null>(null)

  // Android Chrome 的原生安裝事件只發一次：不論當下要不要顯示卡片都先攔下存好
  useEffect(() => {
    const onBip = (e: Event) => { e.preventDefault(); deferredRef.current = e as BeforeInstallPromptEvent; setCanNative(true) }
    const onInstalled = () => { try { localStorage.setItem(OFF_KEY, '1') } catch { /* ignore */ } setShow(false) }
    window.addEventListener('beforeinstallprompt', onBip)
    window.addEventListener('appinstalled', onInstalled)
    return () => { window.removeEventListener('beforeinstallprompt', onBip); window.removeEventListener('appinstalled', onInstalled) }
  }, [])

  useEffect(() => {
    if (!user) { setShow(false); return }
    if (pathname?.startsWith('/admin') || pathname?.startsWith('/m/') || pathname?.startsWith('/track')) { setShow(false); return }
    try {
      if (!window.matchMedia(MOBILE_MQ).matches) return
      if (isStandalone()) return
      if (isInAppBrowser(navigator.userAgent || '')) return
      if (localStorage.getItem(OFF_KEY) === '1') return
      if (Number(localStorage.getItem(SNOOZE_KEY) || 0) > Date.now()) return
    } catch { return }
    setIos(isIOS())
    const t = setTimeout(() => setShow(true), SHOW_DELAY_MS)
    return () => clearTimeout(t)
  }, [user, pathname])

  if (!show) return null

  const snooze = () => { try { localStorage.setItem(SNOOZE_KEY, String(Date.now() + SNOOZE_MS)) } catch { /* ignore */ } setShow(false) }
  const off = () => { try { localStorage.setItem(OFF_KEY, '1') } catch { /* ignore */ } setShow(false) }
  const install = async () => {
    const ev = deferredRef.current
    if (!ev) return
    deferredRef.current = null; setCanNative(false)
    try {
      await ev.prompt()
      const { outcome } = await ev.userChoice
      if (outcome === 'accepted') off()
      else snooze()
    } catch { snooze() }
  }

  return (
    // data-skin="default"：卡片固定暗底亮字，不隨前台 skin 變色（比照事件面板慣例）
    <div data-skin="default" style={wrap} role="dialog" aria-label="安裝 DOR App">
      <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img src="/icon-192.png" alt="" width={44} height={44} style={{ borderRadius: 10, flexShrink: 0 }} />
        <div style={{ minWidth: 0, flex: 1 }}>
          <div style={{ fontWeight: 800, fontSize: 14.5 }}>把 DOR 下載到手機</div>
          <div style={{ fontSize: 12, color: 'rgba(255,255,255,.66)', marginTop: 2 }}>全螢幕體驗、開啟更快，跑步一鍵開始</div>
        </div>
        <button onClick={snooze} aria-label="關閉" style={xBtn}>✕</button>
      </div>

      {ios ? (
        // 兩個步驟各自一行、靠左對齊。第一行用 flex 把文字與分享鈕圖示鎖在同一行——
        // 先前用 inline svg + nowrap 仍被 WebKit 排成獨立一行（實測兩版皆如此），
        // flex 不換行是規格保證、與子元素 display 無關，一勞永逸。
        <div style={stepsBox}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
            <span>① 點 Safari 下方的分享鈕</span>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ display: 'block', flexShrink: 0 }} aria-label="分享">
              <path d="M12 3v12" /><path d="m8 7 4-4 4 4" /><path d="M5 12v7a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-7" />
            </svg>
          </div>
          <div>② 選「加入主畫面」</div>
        </div>
      ) : canNative ? (
        <button onClick={install} style={installBtn}>立即安裝</button>
      ) : (
        <div style={stepsBox}>瀏覽器選單（⋮）→「安裝應用程式」或「加入主畫面」</div>
      )}

      <button onClick={off} style={neverBtn}>不再提醒</button>
    </div>
  )
}

const wrap: React.CSSProperties = {
  position: 'fixed', left: 12, right: 12, margin: '0 auto', maxWidth: 400,
  bottom: 'calc(env(safe-area-inset-bottom, 0px) + 84px)', // 懸在底部導覽列上方
  zIndex: 1400,
  display: 'flex', flexDirection: 'column', gap: 10,
  background: '#101218', color: '#fff',
  border: '1px solid rgba(255,255,255,.14)', borderRadius: 16,
  padding: '14px 14px 10px', boxShadow: '0 16px 40px rgba(0,0,0,.5)',
}
const xBtn: React.CSSProperties = {
  flexShrink: 0, width: 30, height: 30, borderRadius: 999, border: '1px solid rgba(255,255,255,.2)',
  background: 'rgba(255,255,255,.06)', color: 'rgba(255,255,255,.8)', fontSize: 13, cursor: 'pointer',
}
const stepsBox: React.CSSProperties = {
  fontSize: 12.5, lineHeight: 1.8, color: 'rgba(255,255,255,.86)',
  background: 'rgba(255,255,255,.06)', border: '1px solid rgba(255,255,255,.1)',
  borderRadius: 10, padding: '8px 12px',
}
const installBtn: React.CSSProperties = {
  border: 'none', borderRadius: 10, padding: '10px 0', fontSize: 14, fontWeight: 800,
  background: '#1fae66', color: '#fff', cursor: 'pointer', fontFamily: 'inherit',
}
const neverBtn: React.CSSProperties = {
  alignSelf: 'flex-end', border: 'none', background: 'none', color: 'rgba(255,255,255,.4)',
  fontSize: 11.5, cursor: 'pointer', padding: '0 2px 2px', fontFamily: 'inherit',
}
