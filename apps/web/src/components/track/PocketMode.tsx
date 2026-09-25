'use client'

import { useCallback, useEffect, useRef, useState } from 'react'

// 口袋模式覆蓋層（CONTRACT.md §2.5）：全螢幕純黑，攔截所有觸控／點擊（防手機收進口袋時誤觸下方按鈕），
// 只以暗灰顯示計時／距離／GPS 訊號點；中央鎖頭長按 1.5 秒解鎖。
// 直接掛在 track 頁自己的 render 樹裡（PhoneFrame 內的 .phone-shell 本身就有 transform，對
// position:fixed 後代形成新的 containing block，桌機會自動框在模擬框內，不需要另外 portal，
// 與同頁既有的 recover/confirmStravaHold 等覆蓋層是同一套慣例）。z-index 高於可拖曳面板(500)
// 與其餘既有覆蓋層(最高 3300)，確保口袋模式一開就蓋住所有東西。

const HOLD_MS = 1500

export interface PocketModeProps {
  open: boolean
  onUnlock: () => void
  elapsedS: number
  distanceKm: number
  hasSignal: boolean
}

function fmtClock(s: number) {
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = Math.floor(s % 60)
  const p = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${p(m)}:${p(sec)}` : `${p(m)}:${p(sec)}`
}

export default function PocketMode({ open, onUnlock, elapsedS, distanceKm, hasSignal }: PocketModeProps) {
  const [progress, setProgress] = useState(0) // 0..1，長按進度環
  const rafRef = useRef<number | null>(null)
  const startRef = useRef(0)
  const holdingRef = useRef(false)

  const stopHold = useCallback(() => {
    holdingRef.current = false
    if (rafRef.current != null) { cancelAnimationFrame(rafRef.current); rafRef.current = null }
    setProgress(0)
  }, [])

  const tick = useCallback(() => {
    if (!holdingRef.current) return
    const p = Math.min(1, (Date.now() - startRef.current) / HOLD_MS)
    setProgress(p)
    if (p >= 1) { holdingRef.current = false; onUnlock(); return }
    rafRef.current = requestAnimationFrame(tick)
  }, [onUnlock])

  const startHold = useCallback((e: React.PointerEvent) => {
    e.preventDefault(); e.stopPropagation()
    holdingRef.current = true
    startRef.current = Date.now()
    rafRef.current = requestAnimationFrame(tick)
  }, [tick])

  // 開啟時鎖住背景捲動；關閉/卸載時還原＋清計時
  useEffect(() => {
    if (!open) { stopHold(); return }
    const prevOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => { document.body.style.overflow = prevOverflow; stopHold() }
  }, [open, stopHold])

  if (!open) return null

  const ringSize = 84, stroke = 5, r = (ringSize - stroke) / 2, c = 2 * Math.PI * r

  return (
    <div
      // 全螢幕純黑、攔截所有觸控／點擊；left/right:0 在已含 transform 的 .phone-shell 內會自動框住模擬框寬度
      // （見上方註解）。高度改用既有的 .app-min-h class（globals.css :262-272，min-height:
      // max(100dvh, var(--app-h,0px))，關在 @supports 裡）而不是裸 inset:0：原本 top:0+bottom:0 讓高度單純
      // 等於 containing block 當下高度，來源與其餘全螢幕層（.app-h 手機分支）的 dvh+app-h 安全網管線不同步；
      // 改成只釘 top:0（不設 bottom），height 依 CSS 絕對定位規則落回 auto→由內容決定（此處只有一個
      // position:absolute 的置中內容層、不撐高度)，min-height 才是實際決定高度的來源，等同 CONTRACT.md §2.5
      // 要求的公式，動態工具列展開/收合時不會與其他層的高度來源脫鉤而露出縫隙。
      className="app-min-h"
      style={{ position: 'fixed', top: 0, left: 0, right: 0, zIndex: 4000, background: '#000', touchAction: 'none', userSelect: 'none', WebkitUserSelect: 'none' }}
      onClick={(e) => e.preventDefault()}
      onContextMenu={(e) => e.preventDefault()}
    >
      <div style={{ position: 'absolute', inset: 0, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 28, color: '#3a3a3a' }}>
        <div style={{ fontSize: 13, fontWeight: 700, letterSpacing: '.08em' }}>口袋模式・螢幕已鎖定</div>
        <div style={{ fontSize: 44, fontWeight: 900, fontVariantNumeric: 'tabular-nums' }}>{fmtClock(elapsedS)}</div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, fontSize: 18, fontWeight: 800 }}>
          <span>{distanceKm.toFixed(2)} km</span>
          <span style={{ width: 8, height: 8, borderRadius: '50%', background: hasSignal ? '#3a3a3a' : '#232323', border: '1px solid #3a3a3a' }} title={hasSignal ? 'GPS 訊號中' : 'GPS 訊號弱／無'} />
        </div>
        <div
          onPointerDown={startHold}
          onPointerUp={stopHold}
          onPointerLeave={stopHold}
          onPointerCancel={stopHold}
          role="button"
          aria-label="長按 1.5 秒解鎖口袋模式"
          style={{ marginTop: 12, position: 'relative', width: ringSize, height: ringSize, display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'pointer' }}
        >
          <svg width={ringSize} height={ringSize} style={{ position: 'absolute', inset: 0, transform: 'rotate(-90deg)' }}>
            <circle cx={ringSize / 2} cy={ringSize / 2} r={r} fill="none" stroke="#232323" strokeWidth={stroke} />
            <circle
              cx={ringSize / 2} cy={ringSize / 2} r={r} fill="none" stroke="#3a3a3a" strokeWidth={stroke}
              strokeDasharray={c} strokeDashoffset={c * (1 - progress)} strokeLinecap="round"
              style={{ transition: progress === 0 ? 'stroke-dashoffset .15s linear' : 'none' }}
            />
          </svg>
          <span style={{ fontSize: 28 }}>🔒</span>
        </div>
        <div style={{ fontSize: 12, color: '#2c2c2c' }}>長按鎖頭 1.5 秒解鎖</div>
      </div>
    </div>
  )
}
