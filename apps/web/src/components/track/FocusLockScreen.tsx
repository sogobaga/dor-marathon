'use client'

import { useCallback, useEffect, useRef, useState } from 'react'

// 專注模式的鎖定畫面（口袋模式併入專注模式，2026-09-25 CONTRACT.md §2；原 PocketMode.tsx 改名）：
// 全螢幕純黑，攔截所有觸控／點擊（防手機收進口袋時誤觸下方按鈕），只以暗灰顯示計時／距離／移動配速／
// 目標進度／GPS 訊號點；有補給提醒時額外顯示暗黃字；中央鎖頭長按 1.5 秒解鎖。
// 不再是獨立模式——完全由 RaceFocusMode 內部的 locked 狀態驅動（自動上鎖 10 秒無觸控／手動「🔒 鎖定」鈕），
// 本檔不管「要不要顯示」，只管「顯示時長什麼樣子」：父層只在 locked===true 時才掛載這個元件，
// 卸載即代表解鎖，不再需要 open prop。
// 直接掛在 track 頁自己的 render 樹裡（PhoneFrame 內的 .phone-shell 本身就有 transform，對
// position:fixed 後代形成新的 containing block，桌機會自動框在模擬框內，不需要另外 portal，
// 與同頁既有的 recover/confirmStravaHold 等覆蓋層是同一套慣例）。z-index 4000，高於可拖曳面板(500)、
// RaceFocusMode 本體(600)、CheerShow(650) 與其餘既有覆蓋層(最高 3300)，確保一鎖就蓋住所有東西
// （包含事件任務演出，避免口袋裡誤觸事件按鈕）。
// 鎖定畫面顯示的每個數字都是父層（RaceFocusMode，往上追到 track/page.tsx）已經算好的值，這裡只讀不算——
// 進度百分比與主畫面的 GoalProgressBar 共用同一套算法（見 lib/runGoal.ts goalProgressRatio），移動配速
// 與主面板「移動配速」同一個值，避免鎖定畫面與其餘畫面的數字對不上。

const HOLD_MS = 1500

export interface FocusLockScreenProps {
  onUnlock: () => void
  elapsedS: number
  distanceKm: number
  hasSignal: boolean
  movingPaceS: number // 秒/公里；未達門檻為 0（顯示 --:--），與主面板「移動配速」同一個值
  progressPct: number // 0..100（呼叫端已 clamp），與 RaceFocusMode 內 GoalProgressBar 同一套算法
  fuelLabel: string | null // 補給到期提醒（僅賽事策略且補給到期時有值，暗黃字顯示，不需可點）
}

function fmtClock(s: number) {
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = Math.floor(s % 60)
  const p = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${p(m)}:${p(sec)}` : `${p(m)}:${p(sec)}`
}
function fmtPace(s: number) {
  if (!s || !isFinite(s) || s <= 0) return '--:--'
  const v = Math.round(s)
  return `${Math.floor(v / 60)}:${String(v % 60).padStart(2, '0')}`
}

export default function FocusLockScreen({ onUnlock, elapsedS, distanceKm, hasSignal, movingPaceS, progressPct, fuelLabel }: FocusLockScreenProps) {
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

  // 掛載期間鎖住背景捲動；卸載（＝解鎖／退出專注模式）時還原＋清計時
  useEffect(() => {
    const prevOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => { document.body.style.overflow = prevOverflow; stopHold() }
  }, [stopHold])

  const ringSize = 84, stroke = 5, r = (ringSize - stroke) / 2, c = 2 * Math.PI * r
  const pct = Math.min(100, Math.max(0, progressPct))

  return (
    <div
      // 高度公式與掛載慣例見檔頭註解；left/right:0 在已含 transform 的 .phone-shell 內會自動框住模擬框寬度。
      className="app-min-h"
      style={{ position: 'fixed', top: 0, left: 0, right: 0, zIndex: 4000, background: '#000', touchAction: 'none', userSelect: 'none', WebkitUserSelect: 'none' }}
      onClick={(e) => e.preventDefault()}
      onContextMenu={(e) => e.preventDefault()}
    >
      <div style={{ position: 'absolute', inset: 0, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 20, color: '#3a3a3a', padding: '24px 20px' }}>
        <div style={{ fontSize: 13, fontWeight: 700, letterSpacing: '.08em' }}>🔒 專注模式・螢幕已鎖定</div>
        <div style={{ fontSize: 44, fontWeight: 900, fontVariantNumeric: 'tabular-nums' }}>{fmtClock(elapsedS)}</div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, fontSize: 18, fontWeight: 800 }}>
          <span>{distanceKm.toFixed(2)} km</span>
          <span style={{ width: 8, height: 8, borderRadius: '50%', background: hasSignal ? '#3a3a3a' : '#232323', border: '1px solid #3a3a3a' }} title={hasSignal ? 'GPS 訊號中' : 'GPS 訊號弱／無'} />
        </div>
        <div style={{ fontSize: 15, fontWeight: 700 }}>移動配速 {fmtPace(movingPaceS)}/km</div>
        <div style={{ width: '100%', maxWidth: 260 }}>
          <div style={{ textAlign: 'center', fontSize: 12, fontWeight: 700, marginBottom: 4 }}>{Math.round(pct)}%</div>
          <div style={{ height: 8, borderRadius: 999, background: '#1a1a1a', overflow: 'hidden' }}>
            <div style={{ height: '100%', width: `${pct}%`, background: '#3a3a3a', borderRadius: 999 }} />
          </div>
        </div>
        {fuelLabel && (
          <div style={{ fontSize: 14, fontWeight: 800, color: '#8a7020' }}>🍫 {fuelLabel}</div>
        )}
        <div
          onPointerDown={startHold}
          onPointerUp={stopHold}
          onPointerLeave={stopHold}
          onPointerCancel={stopHold}
          role="button"
          aria-label="長按 1.5 秒解鎖專注模式"
          style={{ marginTop: 8, position: 'relative', width: ringSize, height: ringSize, display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'pointer' }}
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
