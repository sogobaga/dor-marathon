'use client'

import { useEffect, useRef, useState } from 'react'
import { usePathname, useRouter } from 'next/navigation'
import { interstitialApi, type InterstitialAd as Ad } from '@/lib/api'
import { INTERSTITIAL_OFF_KEY, INTERSTITIAL_SEEN_KEY, localDayKey } from '@/lib/interstitial'
import { navigateLink } from '@/lib/links'

// 蓋板廣告：拍立得卡片堆疊。前台開啟時彈一次；左右滑動換下一張、滑完自動關閉；右上 X；dots；本日不再顯示。
export default function InterstitialAd() {
  const pathname = usePathname()
  const router = useRouter()
  const [ads, setAds] = useState<Ad[]>([])
  const [open, setOpen] = useState(false)
  const [index, setIndex] = useState(0)
  const [dontShow, setDontShow] = useState(false)
  const [dragX, setDragX] = useState(0)
  const [fly, setFly] = useState<0 | 1 | -1>(0)
  const startXRef = useRef<number | null>(null)
  const draggingRef = useRef(false)
  const flyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const preloadTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const indexRef = useRef(0); indexRef.current = index
  const dontShowRef = useRef(false); dontShowRef.current = dontShow

  // 蓋板開啟期間把 canvas 底色（html background）暫時押成遮罩同系深色。
  // 背景：iOS Safari 分頁還原後有「合成器把 root 圖層放錯位置」的病態（見 ViewportHeightFix 註解），
  // 視窗底部會露出一條 canvas 底色；warm skin 的奶油色配深色遮罩非常刺眼。此舉治不了錨定，
  // 但讓露出的帶子與遮罩同色＝隱形。關閉/卸載即還原原 inline 值（正常畫面完全無感——canvas
  // 被 .phone-frame/遮罩整面蓋住，只有病態露出的那條看得到 canvas）。
  useEffect(() => {
    if (!open) return
    const el = document.documentElement
    const bd = document.body
    const prev = el.style.background
    const prevB = bd.style.background
    el.style.background = '#0c0e12'
    bd.style.background = '#0c0e12' // body 一起押：露出的帶子有時取的是 body 背景（standalone 實測）
    return () => { el.style.background = prev; bd.style.background = prevB }
  }, [open])

  useEffect(() => {
    if (pathname?.startsWith('/admin')) return
    // 深連結落地頁不彈蓋板（2026-09-02 使用者實測：海報 QR 掃進賽事頁被自家廣告整個蓋住，
    // 行銷動線被攔截）。這裡 return 時「不」標記 SEEN——使用者之後導回首頁，effect 依 pathname
    // 重跑會照常查詢顯示，廣告沒有損失、只是讓出第一眼給掃碼目標內容。
    // 範圍：賽事/活動/商家/團練分享/track 這些「有明確目的地」的路徑。
    if (/^\/(race|event|shop|m|track)(\/|$)/.test(pathname || '')) return
    try {
      if (localStorage.getItem(INTERSTITIAL_OFF_KEY) === localDayKey()) return // 本日已勾不再顯示
      if (sessionStorage.getItem(INTERSTITIAL_SEEN_KEY) === '1') return          // 本次工作階段已查過
    } catch { /* ignore */ }
    let alive = true
    interstitialApi.get().then((r) => {
      if (!alive) return
      try { sessionStorage.setItem(INTERSTITIAL_SEEN_KEY, '1') } catch { /* ignore */ } // 查過就標記，避免每次導航重打（含無廣告時）
      const list = (r.ads || []).filter((a) => a.image_url)
      if (!list.length) return
      setAds(list)
      // 先預載第一張圖再開遮罩，避免「遮罩先出現、圖片還在下載」時攤在使用者眼前的全黑空等。
      // 保底：圖太慢或載入失敗最多等 1.5s 還是要開，不讓廣告卡住不出現。
      const reveal = () => {
        if (!alive) return
        if (preloadTimerRef.current) { clearTimeout(preloadTimerRef.current); preloadTimerRef.current = null }
        setOpen(true)
      }
      const pre = new Image()
      pre.onload = reveal
      pre.onerror = reveal
      pre.src = list[0].image_url
      preloadTimerRef.current = setTimeout(reveal, 1500)
    }).catch(() => { /* 取不到就不彈；下次導航再試 */ })
    return () => {
      alive = false
      if (preloadTimerRef.current) { clearTimeout(preloadTimerRef.current); preloadTimerRef.current = null }
    }
  }, [pathname])

  useEffect(() => () => { if (flyTimerRef.current) clearTimeout(flyTimerRef.current) }, [])

  if (!open || !ads.length) return null

  const close = () => {
    if (flyTimerRef.current) clearTimeout(flyTimerRef.current)
    if (dontShowRef.current) { try { localStorage.setItem(INTERSTITIAL_OFF_KEY, localDayKey()) } catch { /* ignore */ } }
    setOpen(false)
  }
  const jump = (i: number) => { if (flyTimerRef.current) clearTimeout(flyTimerRef.current); setFly(0); setDragX(0); setIndex(i) }
  const advance = (dir: 1 | -1) => {
    if (fly) return
    setFly(dir)
    if (flyTimerRef.current) clearTimeout(flyTimerRef.current)
    flyTimerRef.current = setTimeout(() => {
      setFly(0); setDragX(0)
      const cur = indexRef.current // 讀最新 index，避免 stale closure / dots 跳頁後溢位
      if (cur >= ads.length - 1) close()
      else setIndex(cur + 1)
    }, 260)
  }
  const onDown = (e: React.PointerEvent) => { if (fly) return; startXRef.current = e.clientX; draggingRef.current = true }
  const onMove = (e: React.PointerEvent) => { if (!draggingRef.current || startXRef.current == null) return; setDragX(e.clientX - startXRef.current) }
  const onUp = () => {
    if (!draggingRef.current) return
    draggingRef.current = false; startXRef.current = null
    if (Math.abs(dragX) > 72) advance(dragX > 0 ? 1 : -1)
    else setDragX(0)
  }
  const handleCTA = (url: string) => {
    // 點了 CTA ＝ 使用者已互動：本次工作階段就別再彈。尤其站內跳轉後，使用者會預期廣告已關閉，
    // 再冒出來很煩躁。明確寫入 SEEN（雖然顯示當下已寫過，這裡再保險一次、並自我說明此需求）。
    try { sessionStorage.setItem(INTERSTITIAL_SEEN_KEY, '1') } catch { /* ignore */ }
    close()
    navigateLink(url, router)
  }

  const stack = ads.slice(index, index + 3) // 顯示 index..index+2（最多 3 張）

  return (
    <div style={overlay} onPointerMove={onMove} onPointerUp={onUp} onPointerCancel={onUp}>
      <div style={overlayBackdrop} aria-hidden />
      <button onClick={close} aria-label="關閉" style={closeBtn}>✕</button>

      <div style={{ position: 'relative', width: 'min(78vw, 300px)', aspectRatio: '3 / 4.5' }}>
        {stack.map((a, pos) => {
          const isTop = pos === 0
          const back = [{ r: 0, tx: 0, ty: 0, s: 1 }, { r: 3.5, tx: 13, ty: 10, s: 0.965 }, { r: -3.5, tx: -13, ty: 18, s: 0.93 }][pos]
          let transform: string, transition = 'transform .28s cubic-bezier(.2,.7,.2,1)'
          if (isTop && fly !== 0) transform = `translateX(${fly * 130}%) rotate(${fly * 16}deg)`
          else if (isTop) { transform = `translateX(${dragX}px) rotate(${dragX * 0.04}deg)`; if (draggingRef.current) transition = 'none' }
          else transform = `translate(${back.tx}px, ${back.ty}px) rotate(${back.r}deg) scale(${back.s})`
          return (
            <div key={a.id || pos} style={{ ...cardWrap, zIndex: 10 - pos, transform, transition }} onPointerDown={isTop ? onDown : undefined}>
              <div style={polaroid}>
                <img
                  src={a.image_url}
                  alt={a.headline || ''}
                  draggable={false}
                  fetchPriority={isTop ? 'high' : 'auto'}
                  decoding="async"
                  style={img}
                  onLoad={(e) => { e.currentTarget.style.opacity = '1' }}
                />
                <div style={caption}>
                  {a.headline && <div style={{ fontSize: 16, fontWeight: 900, color: '#2b2b2b', lineHeight: 1.35 }}>{a.headline}</div>}
                  {a.description && <div style={{ fontSize: 12, color: '#7a7a7a', marginTop: 3 }}>{a.description}</div>}
                  {a.cta_label && isTop && (
                    <button onClick={() => handleCTA(a.cta_url)} onPointerDown={(e) => e.stopPropagation()} style={ctaBtn}>{a.cta_label} →</button>
                  )}
                </div>
              </div>
            </div>
          )
        })}
      </div>

      {ads.length > 1 && (
        <div style={{ display: 'flex', gap: 7, marginTop: 18 }}>
          {ads.map((_, i) => (
            <button key={i} onClick={() => jump(i)} aria-label={`第 ${i + 1} 張`}
              style={{ width: i === index ? 22 : 8, height: 8, borderRadius: 999, border: 'none', cursor: 'pointer', background: i === index ? 'rgba(255,255,255,.92)' : 'rgba(255,255,255,.4)', transition: 'all .2s', padding: 0 }} />
          ))}
        </div>
      )}

      <div style={{ marginTop: 14, fontSize: 12, color: 'rgba(255,255,255,.6)' }}>
        {ads.length > 1 ? '左右滑動看下一張 · 滑完自動關閉' : '點右上角 ✕ 關閉'}
      </div>
      <label style={{ marginTop: 10, display: 'flex', alignItems: 'center', gap: 7, fontSize: 12.5, color: 'rgba(255,255,255,.78)', cursor: 'pointer' }}>
        <input type="checkbox" checked={dontShow} onChange={(e) => setDontShow(e.target.checked)} style={{ width: 16, height: 16 }} />
        本日不再顯示
      </label>
    </div>
  )
}

// 高度與根容器同一條公式（單一真相）：inset:0 已錨在 layout viewport，height 再用 max() 夾一次，
// 保證蓋板永遠 ≥ 100dvh，不可能比根容器矮。v1.1.664 曾寫成 var(--app-h, 100dvh)，JS 量到偏小值時
// 蓋板被截在同一條線 → 改 max() 後在數學上不可能發生。背景色移到下方 overlayBackdrop。
// 註（與 globals.css 的 @supports 對照）：這裡刻意**不需要** @supports 隔離。無 dvh 的舊引擎上這條
// inline height 一樣會 invalid at computed-value time → height:auto，但本元素同時有 inset:0
// （top 與 bottom 皆為 0），auto 高度會自動撐滿視窗，結果與預期一致；globals.css 那三處之所以必須
// 隔離，是因為它們有「要被保護的 100vh 前置宣告」而且子節點全是 absolute（auto ⇒ 0）。
// ⚠️ 2026-09-02 移除 backdrop-filter: blur(2px)（單變因實驗）：使用者觀察「每次跑版都在蓋板
// 出現時」。全螢幕 fixed ＋ backdrop-filter＝要求合成器即時快照背後內容的重量級圖層，且掛載
// 時機正落在 Safari 還原/載入的敏感窗（症狀 A＝root 圖層被上移 lvh−svh）。注意混淆因子：蓋板
// 與病態都只在「工作階段第一次載入」出現，相關未必因果——故拆掉觀察發生率，底色 .72→.78 補償
// 失去的模糊分離感。若發生率歸零＝因果確認；若不變＝排除此嫌疑、把 blur 加回來也無妨。
const overlay: React.CSSProperties = { position: 'fixed', inset: 0, height: 'max(100dvh, var(--app-h, 0px))', zIndex: 2500, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', padding: 24, touchAction: 'none', userSelect: 'none', WebkitUserSelect: 'none' }

// 遮罩底色獨立成一層，上下各超掃 30vh（共 160vh）。
// ⚠️ 這是「有機會、失敗即無害」的降級保護，**不是保證**。它只在「合成器的裁切矩形大於過期的 layout
//    viewport」時才有效；若 Safari 連裁切矩形都用同一個過期偏小值，overlay 是 position:fixed，
//    它與所有後代都會被裁在同一條線上，超掃 160vh 也照樣被腰斬、畫面完全不變。
//    （下方 17% 看到的米色不能當反證：root 背景色會傳播到 canvas，不論 ICB 多大都塗滿整個視窗。）
//    驗收時請一併回報「遮罩被裁的線是否與 app 內容被裁的線同高」——同高即代表裁切線假設成立，
//    症狀 A 的所有「換 CSS 長度單位」方案（含 100lvh）都可直接排除，只剩 ?vpfix=heal 一條路。
// ⚠️ 這招只能用在「純裝飾、底部沒有互動元件」的層。app 根容器不可比照辦理——超掃會把底部導覽列推出
//    可見區，等於把外觀缺陷換成功能缺陷。
// z-index:-1 是必要的：overlay 是 fixed（自成 stacking context），absolute 子節點若 z-index:auto 會
// 排在未定位的 in-flow 內容（卡片/說明文字/checkbox）之後，把它們蓋掉。
const overlayBackdrop: React.CSSProperties = { position: 'absolute', left: 0, right: 0, top: '-30vh', height: '160vh', background: 'rgba(10,12,16,.78)', pointerEvents: 'none', zIndex: -1 }
const closeBtn: React.CSSProperties = { position: 'absolute', top: 'calc(env(safe-area-inset-top, 0px) + 14px)', right: 16, width: 38, height: 38, borderRadius: 999, border: '1px solid rgba(255,255,255,.28)', background: 'rgba(0,0,0,.35)', color: '#fff', fontSize: 16, cursor: 'pointer', zIndex: 20 }
const cardWrap: React.CSSProperties = { position: 'absolute', inset: 0, willChange: 'transform' }
const polaroid: React.CSSProperties = { width: '100%', height: '100%', background: '#fff', borderRadius: 14, padding: '12px 12px 0', boxShadow: '0 18px 50px rgba(0,0,0,.45)', display: 'flex', flexDirection: 'column' }
const img: React.CSSProperties = { display: 'block', width: '100%', flex: 1, borderRadius: 7, backgroundColor: 'var(--bg-2)', objectFit: 'cover', objectPosition: 'center', minHeight: 0, opacity: 0, transition: 'opacity .25s ease' }
const caption: React.CSSProperties = { padding: '14px 8px 18px', textAlign: 'center', flexShrink: 0 }
const ctaBtn: React.CSSProperties = { marginTop: 8, background: 'none', border: 'none', color: '#3f6fb0', fontWeight: 800, fontSize: 13.5, cursor: 'pointer', fontFamily: 'inherit' }
