'use client'

// 每公里鼓勵語「泡泡對話框 + 啦啦隊角色」彈出表演（v1.1.665）。取代舊版純文字橫幅元件
// （原本活在 RaceFocusMode.tsx，已隨本次改版移除）。獨立掛在 track/page.tsx 頂層、不受 status 或
// RaceFocusMode 的 hidden/顯示切換影響——任何畫面狀態下每公里觸發都要看得到。
//
// z-index 650：蓋過 RaceFocusMode 專注模式疊層（600）與浮動按鈕（500/560），但仍低於事件觸發演出
// （2100+）、確認結束（2500）、Strava 三選一與登入（3300）等更高優先的互動彈窗。純顯示、
// pointerEvents:none（不擋底下地圖/按鈕操作）。
//
// 素材安全區（相對整張泡泡框圖 1523×697）：左右各 5%、上 9%、下 27%（下方留給尾巴，尾巴朝下、
// 底部正中央）。啦啦隊角色圖 400×800 全身、人物置中；容器 aspect-ratio 2/3 只露出頭頂往下
// 400×600 的部分（等比縮放後裁掉膝蓋以下），底部與螢幕底部切齊，像角色「站」在畫面下緣。
//
// 動畫：cheer 變化（key 遞增）→ 泡泡＋角色各自套用進場 keyframes（角色延遲 60ms 有層次）；cheer 變
// null 時不立即卸載——保留最後一筆內容切成 out 動畫，onAnimationEnd 後才真正清空（沿用舊版橫幅元件
// 「淡出前維持顯示」慣例）。keyframes 定義於 globals.css（cheerBubbleIn/cheerCharIn/cheerOut），
// reduced-motion 使用者改走同名但只做 opacity 淡入淡出的簡化版（見 globals.css media query）。
import { useEffect, useMemo, useRef, useState } from 'react'

const BUBBLE_SRC = '/ui/cheer/chatbox.webp'
const CHAR_SRCS = ['/ui/cheer/cheerleader-01.webp', '/ui/cheer/cheerleader-02.webp', '/ui/cheer/cheerleader-03.webp']

export default function CheerShow({ cheer }: { cheer: { text: string; key: number } | null }) {
  const [shown, setShown] = useState<{ text: string; key: number } | null>(null)
  const [phase, setPhase] = useState<'in' | 'out'>('in')
  const lastCharIdxRef = useRef(-1) // 避免連續兩輪抽到同一張角色圖

  useEffect(() => {
    if (cheer) { setShown(cheer); setPhase('in') } else { setPhase('out') }
  }, [cheer])

  // 角色隨機：每次新一輪演出（shown.key 變更）才重抽，同一輪演出中途不換臉。
  const charSrc = useMemo(() => {
    let idx = Math.floor(Math.random() * CHAR_SRCS.length)
    if (CHAR_SRCS.length > 1 && idx === lastCharIdxRef.current) idx = (idx + 1) % CHAR_SRCS.length
    lastCharIdxRef.current = idx
    return CHAR_SRCS[idx]
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shown?.key])

  // 預先載入四張圖，避免第一次觸發時才開始下載造成的閃爍/延遲。
  useEffect(() => {
    [BUBBLE_SRC, ...CHAR_SRCS].forEach((src) => { const img = new Image(); img.src = src })
  }, [])

  if (!shown) return null

  return (
    <div
      data-skin="default"
      // inset:0 而非 height:var(--app-h)：桌機的 .phone-shell（transform）是 fixed 的定位基準，--app-h 是整個瀏覽器視窗高度、
      // 會比模擬框高，角色 bottom:0 會貼到框外被裁掉；inset:0 在手機（視窗）與桌機（模擬框）兩種基準下都貼齊底部。
      style={{ position: 'fixed', inset: 0, zIndex: 650, pointerEvents: 'none' }}
    >
      {/* 泡泡對話框：文字容器扣掉底部 27%（尾巴留白）與左右各 5% */}
      <div
        key={`bubble-${shown.key}-${phase}`}
        style={{
          position: 'absolute', top: 'calc(var(--app-top, 24px) + 10px)', left: '50%',
          width: 'min(92vw, 520px)', aspectRatio: '1523 / 697',
          background: `url(${BUBBLE_SRC}) center / 100% 100% no-repeat`,
          transform: 'translateX(-50%)',
          animation: phase === 'in' ? 'cheerBubbleIn .36s cubic-bezier(.34,1.56,.64,1) both' : 'cheerOut .24s ease both',
        }}
        onAnimationEnd={() => { if (phase === 'out') setShown(null) }}
      >
        <div
          style={{
            position: 'absolute', left: '5%', right: '5%', top: '9%', bottom: '27%',
            display: 'flex', alignItems: 'center', justifyContent: 'center', textAlign: 'center',
            color: '#1c2748', fontWeight: 800, fontSize: 'clamp(16px, 4.6vw, 23px)', lineHeight: 1.3, wordBreak: 'break-word',
          }}
        >{shown.text}</div>
      </div>

      {/* 啦啦隊角色：容器只露出頭頂往下 400×600（overflow:hidden 裁掉膝蓋以下），底部貼齊螢幕底部 */}
      <div
        key={`char-${shown.key}-${phase}`}
        style={{
          position: 'absolute', bottom: 0, left: '50%', width: 'min(58vw, 300px)', aspectRatio: '2 / 3', overflow: 'hidden',
          transform: 'translateX(-50%)',
          animation: phase === 'in' ? 'cheerCharIn .42s cubic-bezier(.34,1.56,.64,1) .06s both' : 'cheerOut .24s ease both',
        }}
      >
        <img src={charSrc} alt="" draggable={false} style={{ width: '100%', height: 'auto', display: 'block' }} />
      </div>
    </div>
  )
}
