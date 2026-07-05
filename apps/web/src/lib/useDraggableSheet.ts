'use client'

import { useEffect, useLayoutEffect, useRef, useState, type PointerEvent } from 'react'

export type SheetSnap = 'peek' | 'half' | 'full'

// SSR 安全的 layout effect：客戶端用 useLayoutEffect（量測在繪製前完成，面板一開始就落在正確位置、
// 不會載入時從「蓋住背景」滑下來），伺服器退回 useEffect（避免警告）。
const useIsoLayoutEffect = typeof window !== 'undefined' ? useLayoutEffect : useEffect

/**
 * COROS 式可拖曳資訊面板的共用邏輯：量測、三段吸附（收合/半展/全展）、拖曳位移。
 * 只負責「位置與手勢」；把手/內容的呈現由各頁自行放。
 *
 * 用法：
 *   const sheet = useDraggableSheet('half')
 *   <div ref={sheet.wrapRef} style={{ position:'relative', flex:1, minHeight:0, overflow:'hidden' }}>
 *     ...背景層（地圖 / 會員面板）...
 *     <div style={{ position:'absolute', left:0, right:0, top:0, height:'100%',
 *                   transform:`translateY(${sheet.curY}px)`,
 *                   transition: !sheet.dragging && sheet.ready ? 'transform .28s ...' : 'none',
 *                   opacity: sheet.ready ? 1 : 0, ... }}>
 *       <div ref={sheet.peekRef} {...sheet.handlers} style={{ touchAction:'none', ... }}>把手＋標題</div>
 *       <div style={{ flex:1, minHeight:0, overflowY:'auto', touchAction:'pan-y' }}>可捲動內容</div>
 *     </div>
 *   </div>
 */
export function useDraggableSheet(initial: SheetSnap = 'half') {
  const [snap, setSnap] = useState<SheetSnap>(initial)
  const [dragY, setDragY] = useState<number | null>(null) // 拖曳中的即時位移(px)；null=非拖曳（吸附到停靠點）
  const [H, setH] = useState(0)          // 容器（背景區）高度
  const [peekH, setPeekH] = useState(120) // 收合露出高度（把手＋頂部標題，量測而得）
  const wrapRef = useRef<HTMLDivElement>(null)
  const peekRef = useRef<HTMLDivElement>(null)
  const dragRef = useRef<{ startY: number; off: number; pointerId: number; active: boolean; cur: number; el: HTMLElement } | null>(null)

  // 量測容器與收合露出區高度；旋轉/尺寸變動即時更新
  useIsoLayoutEffect(() => {
    const wrap = wrapRef.current, peek = peekRef.current
    const update = () => { if (wrap) setH(wrap.clientHeight); if (peek) setPeekH(peek.offsetHeight) }
    update()
    let ro: ResizeObserver | null = null
    try { ro = new ResizeObserver(update); if (wrap) ro.observe(wrap); if (peek) ro.observe(peek) } catch { /* ignore */ }
    return () => { try { ro?.disconnect() } catch { /* ignore */ } }
  }, [])

  // 停靠點對應的下移量(px)：full=不下移(蓋住背景)、half=下移一半、peek=只露出把手＋標題
  const offsetFor = (s: SheetSnap) => {
    if (H <= 0) return 0
    if (s === 'full') return 0
    if (s === 'half') return Math.round(H * 0.5)
    return Math.max(0, H - Math.max(90, peekH))
  }
  const curY = dragY ?? offsetFor(snap)

  // 移動超過門檻才視為「拖曳」，否則當成「點擊」——這樣整個面板頂部（含分頁/按鈕）都能當拖曳把手，
  // 但點分頁時不會被拖曳吃掉（不 setPointerCapture、不吸附、放行 onClick）。
  const DRAG_THRESHOLD = 6
  function onPointerDown(e: PointerEvent<HTMLElement>) {
    // 先不 capture、不進拖曳；等移動超過門檻才開始
    dragRef.current = { startY: e.clientY, off: offsetFor(snap), pointerId: e.pointerId, active: false, cur: offsetFor(snap), el: e.currentTarget as HTMLElement }
  }
  function onPointerMove(e: PointerEvent<HTMLElement>) {
    const st = dragRef.current; if (!st) return
    const dy = e.clientY - st.startY
    if (!st.active) {
      if (Math.abs(dy) < DRAG_THRESHOLD) return // 仍可能是點擊
      st.active = true
      try { st.el.setPointerCapture(st.pointerId) } catch { /* ignore */ }
    }
    const y = Math.min(Math.max(st.off + dy, 0), offsetFor('peek')) // 夾在 [全展, 收合] 之間
    st.cur = y
    setDragY(y)
  }
  function onPointerUp() {
    const st = dragRef.current; dragRef.current = null
    if (!st || !st.active) { setDragY(null); return } // 沒真的拖曳（點擊）→ 不吸附、放行點擊
    const y = st.cur
    let best: SheetSnap = 'half', bd = Infinity
    for (const s of ['full', 'half', 'peek'] as const) { const d = Math.abs(offsetFor(s) - y); if (d < bd) { bd = d; best = s } }
    setSnap(best); setDragY(null) // 吸附到最近的停靠點
  }

  return {
    wrapRef,
    peekRef,
    handlers: { onPointerDown, onPointerMove, onPointerUp, onPointerCancel: onPointerUp },
    curY,
    dragging: dragY != null,
    ready: H > 0,
    H,
    snap,
    setSnap,
  }
}
