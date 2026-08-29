'use client'

// 每公里鼓勵語「泡泡對話框 + 啦啦隊角色」彈出表演（v1.1.665）。取代舊版純文字橫幅元件
// （原本活在 RaceFocusMode.tsx，已隨本次改版移除）。獨立掛在 track/page.tsx 頂層、不受 status 或
// RaceFocusMode 的 hidden/顯示切換影響——任何畫面狀態下每公里觸發都要看得到。
//
// z-index 650：蓋過 RaceFocusMode 專注模式疊層（600）與浮動按鈕（500/560），但仍低於事件觸發演出
// （2100+）、確認結束（2500）、Strava 三選一與登入（3300）等更高優先的互動彈窗。純顯示、
// pointerEvents:none（不擋底下地圖/按鈕操作）；校正模式（edit）例外，見下方。
//
// 動畫：cheer 變化（key 遞增）→ 泡泡＋角色各自套用進場 keyframes（角色延遲 60ms 有層次）；cheer 變
// null 時不立即卸載——保留最後一筆內容切成 out 動畫，onAnimationEnd 後才真正清空（沿用舊版橫幅元件
// 「淡出前維持顯示」慣例）。keyframes 定義於 globals.css（cheerBubbleIn/cheerCharIn/cheerOut），
// reduced-motion 使用者改走同名但只做 opacity 淡入淡出的簡化版（見 globals.css media query）。
//
// 啦啦隊位置校正（2026-08-29）：白名單帳號可拖曳/縮放三張角色各自的位置，套用到所有跑者。契約定義在
// lib/api.ts 檔尾「啦啦隊位置校正」區塊（CheerCharLayout/CheerCharId/DEFAULT_CHEER_CHAR_LAYOUT/
// parseCheerCharLayout/cheerLayoutApi.save）。dx/dy 是相對角色容器自身寬高的位移百分比（正右正下）。
//
// ⚠️ 兩層 transform 是關鍵：cheerCharIn 的 keyframes 用 animation-fill-mode:both，動畫結束後最後一格
// 的 transform 會蓋掉任何寫在同一個節點上的 inline transform——校正位移/縮放不能跟進場動畫放在同一層。
// 結構：外層 div（絕對定位 top:CHAR_TOP，套 translate(dx%,dy%) scale(scale)，不套動畫）
//     → 內層 div（套 cheerCharIn/cheerOut 進場/退場動畫，只做 translateY，不含 X 位移）→ img。
import { useEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import type { CheerCharId, CheerCharLayout, CheerCharLayoutItem } from '@/lib/api'
import { DEFAULT_CHEER_CHAR_LAYOUT } from '@/lib/api'

// 素材版本參數：檔名不變但內容更新時（例如換更高解析度原圖）改這個值，強制瀏覽器與 Cloudflare 快取失效
const ASSET_VER = 'v3'
const BUBBLE_SRC = `/ui/cheer/chatbox.webp?${ASSET_VER}`
const CHAR_IDS: CheerCharId[] = ['01', '02', '03']
const CHAR_SRC_BY_ID: Record<CheerCharId, string> = {
  '01': `/ui/cheer/cheerleader-01.webp?${ASSET_VER}`,
  '02': `/ui/cheer/cheerleader-02.webp?${ASSET_VER}`,
  '03': `/ui/cheer/cheerleader-03.webp?${ASSET_VER}`,
}
const EDIT_BUBBLE_TEXT = '校正模式：拖曳啦啦隊調整位置'
const SCALE_MIN = 0.3
const SCALE_MAX = 3
const SCALE_STEP = 0.05
const XY_STEP = 1 // %

// 版面常數（2026-08-29 使用者定案）：泡泡框在上；啦啦隊角色「頭頂對齊泡泡框底緣 + 20px」往下放，
// 腳超出螢幕就被根容器 overflow:hidden 切掉（不再貼底部對齊）。角色寬 min(90vw, 460px)（≈原 58vw 的 1.55 倍）。
// ⚠️ 角色原圖只有 400×800，放大到這個尺寸在 Retina 上會糊，根治需換 ≥1200×2400 的原圖重壓（見 public/ui/cheer）。
const BUBBLE_TOP = 'calc(var(--app-top, 24px) + 10px)'
const BUBBLE_WIDTH = 'min(92vw, 520px)'
const BUBBLE_RATIO = 1523 / 697 // 泡泡框圖寬高比
const CHAR_WIDTH = 'min(90vw, 460px)'
const CHAR_GAP_PX = 20 // 角色頭頂與泡泡框底緣的距離
// 角色頂端 = 泡泡框 top + 泡泡框高度（寬 / 比例）+ 間距
const CHAR_TOP = `calc(${BUBBLE_TOP} + ${BUBBLE_WIDTH} / ${BUBBLE_RATIO.toFixed(4)} + ${CHAR_GAP_PX}px)`

const toolPillStyle = (active: boolean): CSSProperties => ({
  flex: 1, padding: '4px 0', borderRadius: 6, fontSize: 11, fontWeight: 800, cursor: 'pointer',
  border: active ? '1px solid #ffc24b' : '1px solid rgba(255,255,255,.28)',
  background: active ? 'rgba(255,194,75,.28)' : 'transparent',
  color: '#fff', fontFamily: 'inherit',
})
const toolBtnStyle: CSSProperties = {
  flex: 1, padding: '4px 0', borderRadius: 6, fontSize: 12, fontWeight: 800, cursor: 'pointer',
  border: '1px solid rgba(255,255,255,.3)', background: 'rgba(255,255,255,.08)', color: '#fff', fontFamily: 'inherit',
}
const toolActionBtnStyle: CSSProperties = {
  padding: '5px 0', borderRadius: 6, fontSize: 11.5, fontWeight: 700, cursor: 'pointer',
  border: '1px solid rgba(255,255,255,.3)', background: 'rgba(255,255,255,.08)', color: '#fff', fontFamily: 'inherit',
}
const toolSaveBtnStyle: CSSProperties = {
  padding: '6px 0', borderRadius: 6, fontSize: 12, fontWeight: 800, cursor: 'pointer',
  border: '1px solid #ffc24b', background: 'rgba(255,194,75,.25)', color: '#fff', fontFamily: 'inherit',
}

export interface CheerEditProps {
  layout: CheerCharLayout
  onChange: (layout: CheerCharLayout) => void
  onSave: () => Promise<void>
  onExit: () => void
  saving: boolean
}

export default function CheerShow({ cheer, layout, edit }: {
  cheer: { text: string; key: number } | null
  layout: CheerCharLayout
  edit?: CheerEditProps
}) {
  const [shown, setShown] = useState<{ text: string; key: number } | null>(null)
  const [phase, setPhase] = useState<'in' | 'out'>('in')
  const lastCharIdRef = useRef<CheerCharId | null>(null)
  const [selected, setSelected] = useState<CheerCharId>('01') // 校正模式：目前選中要編輯的那張
  const [savedFlash, setSavedFlash] = useState(false) // 儲存成功後短暫顯示「✓ 已儲存」
  const [isDragging, setIsDragging] = useState(false)
  const dragOuterRef = useRef<HTMLDivElement | null>(null)
  const dragStateRef = useRef<{ pointerId: number; startX: number; startY: number; startDx: number; startDy: number } | null>(null)

  useEffect(() => {
    if (edit) return // 校正模式永遠顯示、不受 cheer 觸發/自動消失影響
    if (cheer) { setShown(cheer); setPhase('in') } else { setPhase('out') }
  }, [cheer, edit])

  // 角色隨機：每次新一輪演出（shown.key 變更）才重抽，同一輪演出中途不換臉。校正模式不使用（改由 selected 決定）。
  const randomCharId = useMemo(() => {
    let idx = Math.floor(Math.random() * CHAR_IDS.length)
    let id = CHAR_IDS[idx]
    if (CHAR_IDS.length > 1 && id === lastCharIdRef.current) id = CHAR_IDS[(idx + 1) % CHAR_IDS.length]
    lastCharIdRef.current = id
    return id
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shown?.key])

  // 預先載入四張圖，避免第一次觸發時才開始下載造成的閃爍/延遲。
  useEffect(() => {
    [BUBBLE_SRC, ...Object.values(CHAR_SRC_BY_ID)].forEach((src) => { const img = new Image(); img.src = src })
  }, [])

  if (!edit && !shown) return null

  const charId: CheerCharId = edit ? selected : randomCharId
  const charSrc = CHAR_SRC_BY_ID[charId]
  const activeLayout = edit?.layout ?? layout
  const item: CheerCharLayoutItem = activeLayout[charId] ?? DEFAULT_CHEER_CHAR_LAYOUT[charId]
  const bubbleText = edit ? EDIT_BUBBLE_TEXT : (shown?.text ?? '')

  // 只改目前選中的那張。
  function updateSelected(patch: Partial<CheerCharLayoutItem>) {
    if (!edit) return
    const cur = edit.layout[selected] ?? DEFAULT_CHEER_CHAR_LAYOUT[selected]
    edit.onChange({ ...edit.layout, [selected]: { ...cur, ...patch } })
  }
  function stepXY(field: 'dx' | 'dy', delta: number) {
    if (!edit) return
    const cur = edit.layout[selected] ?? DEFAULT_CHEER_CHAR_LAYOUT[selected]
    updateSelected({ [field]: Math.round((cur[field] + delta) * 10) / 10 } as Partial<CheerCharLayoutItem>)
  }
  function stepScale(delta: number) {
    if (!edit) return
    const cur = edit.layout[selected] ?? DEFAULT_CHEER_CHAR_LAYOUT[selected]
    const v = Math.min(SCALE_MAX, Math.max(SCALE_MIN, Math.round((cur.scale + delta) * 100) / 100))
    updateSelected({ scale: v })
  }
  function resetSelected() { updateSelected({ dx: 0, dy: 0, scale: 1 }) }
  async function handleSaveClick() {
    if (!edit) return
    try {
      await edit.onSave()
      setSavedFlash(true)
      setTimeout(() => setSavedFlash(false), 1500)
    } catch { /* 錯誤由 onSave 呼叫端（page.tsx）處理提示，這裡不重複顯示 */ }
  }

  // 拖曳角色：手感照抄 MonopolyScreen 校準模式（Pointer Events + setPointerCapture）。
  // rect 用 offsetWidth/offsetHeight（外層 div 在 scale=1 時的版面尺寸，不受 scale transform 影響）而非
  // getBoundingClientRect（那會被 scale 放大/縮小，換算出來的 % 位移會失真）。
  function handlePointerDown(e: React.PointerEvent<HTMLDivElement>) {
    if (!edit) return
    e.preventDefault()
    e.currentTarget.setPointerCapture(e.pointerId)
    const cur = edit.layout[selected] ?? DEFAULT_CHEER_CHAR_LAYOUT[selected]
    dragStateRef.current = { pointerId: e.pointerId, startX: e.clientX, startY: e.clientY, startDx: cur.dx, startDy: cur.dy }
    setIsDragging(true)
  }
  function handlePointerMove(e: React.PointerEvent<HTMLDivElement>) {
    if (!edit) return
    const st = dragStateRef.current
    if (!st || st.pointerId !== e.pointerId) return
    const el = dragOuterRef.current
    const w = el?.offsetWidth || 1, h = el?.offsetHeight || 1
    const rawDx = st.startDx + ((e.clientX - st.startX) / w) * 100
    const rawDy = st.startDy + ((e.clientY - st.startY) / h) * 100
    updateSelected({ dx: Math.round(rawDx * 10) / 10, dy: Math.round(rawDy * 10) / 10 })
  }
  function handlePointerUp(e: React.PointerEvent<HTMLDivElement>) {
    if (!edit) return
    const st = dragStateRef.current
    if (st && st.pointerId === e.pointerId) dragStateRef.current = null
    setIsDragging(false)
  }

  return (
    <div
      data-skin="default"
      // inset:0 而非 height:var(--app-h)：桌機的 .phone-shell（transform）是 fixed 的定位基準，--app-h 是整個瀏覽器視窗高度、
      // 會比模擬框高，角色 bottom:0 會貼到框外被裁掉；inset:0 在手機（視窗）與桌機（模擬框）兩種基準下都貼齊底部。
      style={{ position: 'fixed', inset: 0, zIndex: 650, pointerEvents: edit ? 'auto' : 'none', overflow: 'hidden' }}
    >
      {/* 泡泡對話框：文字容器扣掉底部 27%（尾巴留白）與左右各 5%。校正模式：文字固定、不套動畫。 */}
      <div
        key={edit ? 'edit-bubble' : `bubble-${shown!.key}-${phase}`}
        style={{
          position: 'absolute', top: BUBBLE_TOP, left: '50%',
          width: BUBBLE_WIDTH, aspectRatio: '1523 / 697',
          background: `url(${BUBBLE_SRC}) center / 100% 100% no-repeat`,
          transform: 'translateX(-50%)',
          animation: edit ? undefined : (phase === 'in' ? 'cheerBubbleIn .36s cubic-bezier(.34,1.56,.64,1) both' : 'cheerOut .24s ease both'),
        }}
        onAnimationEnd={edit ? undefined : () => { if (phase === 'out') setShown(null) }}
      >
        <div
          style={{
            position: 'absolute', left: '5%', right: '5%', top: '9%', bottom: '27%',
            display: 'flex', alignItems: 'center', justifyContent: 'center', textAlign: 'center',
            color: '#1c2748', fontWeight: 800, fontSize: 'clamp(16px, 4.6vw, 23px)', lineHeight: 1.3, wordBreak: 'break-word',
          }}
        >{bubbleText}</div>
      </div>

      {/* 啦啦隊角色：外層＝定位＋校正 transform（不套動畫，避免 fill-mode:both 蓋掉）；內層＝進場/退場動畫；img。 */}
      <div
        ref={dragOuterRef}
        key={edit ? `edit-char-${charId}` : undefined}
        onPointerDown={edit ? handlePointerDown : undefined}
        onPointerMove={edit ? handlePointerMove : undefined}
        onPointerUp={edit ? handlePointerUp : undefined}
        onPointerCancel={edit ? handlePointerUp : undefined}
        style={{
          position: 'absolute', top: CHAR_TOP, left: '50%', width: CHAR_WIDTH,
          transform: `translateX(-50%) translate(${item.dx}%, ${item.dy}%) scale(${item.scale})`,
          transformOrigin: 'top center',
          touchAction: edit ? 'none' : undefined,
          cursor: edit ? (isDragging ? 'grabbing' : 'grab') : undefined,
        }}
      >
        <div
          key={edit ? undefined : `char-in-${shown!.key}-${phase}`}
          style={{
            animation: edit ? undefined : (phase === 'in' ? 'cheerCharIn .42s cubic-bezier(.34,1.56,.64,1) .06s both' : 'cheerOut .24s ease both'),
            outline: edit ? `2px dashed ${isDragging ? 'rgba(255,194,75,.95)' : 'rgba(255,255,255,.5)'}` : undefined,
            outlineOffset: edit ? 2 : undefined,
            borderRadius: edit ? 10 : undefined,
          }}
        >
          <img src={charSrc} alt="" draggable={false} style={{ width: '100%', height: 'auto', display: 'block' }} />
        </div>
      </div>

      {/* 校正模式工具列：右側直欄（118px，390px 手機寬綽有餘）。只影響目前選中(selected)那張。 */}
      {edit && (
        <div
          style={{
            position: 'absolute', top: 'calc(var(--app-top, 24px) + 54px)', right: 8, width: 118,
            background: 'rgba(11,14,19,.92)', border: '1px solid rgba(255,194,75,.5)', borderRadius: 12,
            padding: '10px 8px', display: 'flex', flexDirection: 'column', gap: 6,
            boxShadow: '0 6px 20px rgba(0,0,0,.35)', fontFamily: 'inherit',
          }}
        >
          <div style={{ display: 'flex', gap: 4, justifyContent: 'center' }}>
            {CHAR_IDS.map((id) => (
              <button key={id} onClick={() => setSelected(id)} style={toolPillStyle(id === selected)}>{id}</button>
            ))}
          </div>
          <div style={{ fontSize: 11, color: '#fff', textAlign: 'center', lineHeight: 1.5 }}>
            X {item.dx >= 0 ? '+' : ''}{item.dx.toFixed(1)}%<br />
            Y {item.dy >= 0 ? '+' : ''}{item.dy.toFixed(1)}%<br />
            ×{item.scale.toFixed(2)}
          </div>
          <div style={{ display: 'flex', gap: 4 }}>
            <button onClick={() => stepXY('dx', -XY_STEP)} style={toolBtnStyle}>X－</button>
            <button onClick={() => stepXY('dx', XY_STEP)} style={toolBtnStyle}>X＋</button>
          </div>
          <div style={{ display: 'flex', gap: 4 }}>
            <button onClick={() => stepXY('dy', -XY_STEP)} style={toolBtnStyle}>Y－</button>
            <button onClick={() => stepXY('dy', XY_STEP)} style={toolBtnStyle}>Y＋</button>
          </div>
          <div style={{ display: 'flex', gap: 4 }}>
            <button onClick={() => stepScale(-SCALE_STEP)} style={toolBtnStyle}>－</button>
            <button onClick={() => stepScale(SCALE_STEP)} style={toolBtnStyle}>＋</button>
          </div>
          <input
            type="range" min={SCALE_MIN} max={SCALE_MAX} step={SCALE_STEP} value={item.scale}
            onChange={(e) => updateSelected({ scale: Number(e.target.value) })}
            style={{ width: '100%' }}
          />
          <button onClick={resetSelected} style={toolActionBtnStyle}>重設此張</button>
          <button onClick={handleSaveClick} disabled={edit.saving} style={{ ...toolSaveBtnStyle, opacity: edit.saving ? .6 : 1 }}>
            {edit.saving ? '儲存中…' : savedFlash ? '✓ 已儲存' : '💾 儲存'}
          </button>
          <button onClick={edit.onExit} style={toolActionBtnStyle}>離開</button>
        </div>
      )}
    </div>
  )
}
