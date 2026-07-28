'use client'

import { useEffect, useRef, useState } from 'react'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'
import { monopolyApi, type MonopolyRollResult, type DrawResult } from '@/lib/api'
import GpCoin from './GpCoin'
import DpCoin from './DpCoin'

// 環台大富翁 Phase 1：盤面遊戲。
// 盤面固定 46 格（position 0=START，1..45 為格子，繞一圈=46 格），座標由底圖量出、寫死成常數
// （之後美術要微調座標，直接改這個陣列即可，不需要動任何邏輯）。
// 機會格(6,15,25,35)／命運格(10,18,30,39)：停格後由後端在 Roll 交易內抽卡（Phase 3），
// 前端只依 landed_on + draw_result 演出翻卡揭曉，判定完全交給後端、前端不重複判斷。
const BOARD_SIZE = 46
const BOARD_COORDS: [number, number][] = [
  [28, 82], [34, 81], [39.7, 80.2], [45.5, 78.4], [50.6, 75.9], [54.2, 73.1], [56.4, 69.3], [58.3, 65], [60.9, 61.9], [64.1, 58.5],
  [66.8, 55.7], [68.3, 52.1], [69.8, 48.5], [71.2, 44.4], [71.9, 41], [74.2, 37.2], [76.8, 33.9], [80, 30.9], [82.2, 28], [83.3, 23.6],
  [83.3, 20.2], [81.9, 16.4], [77.5, 13.8], [72.2, 11.8], [66, 11.1], [59.6, 11.2], [52.8, 12.3], [47.5, 14.3], [42.7, 16.9], [38.8, 20.4],
  [36, 23.7], [34, 27.7], [32, 31.1], [29.3, 34.4], [24.9, 37.7], [21.7, 40.8], [19.4, 44.6], [18.4, 47.9], [18, 51.8], [17.1, 55.9],
  [16.9, 59.8], [17.8, 63.3], [20, 66.6], [22.3, 69.6], [24.9, 73], [27.6, 75.9],
]
const BOARD_IMG = '/source/ui/02_BG/DOR_TAW_RUNNER_START_1to45.png'
const RUNNER_IMG = '/source/ui/02_BG/DOR_RUNNER.png'
// 角色圖 DOR_RUNNER.png 站立點（前腳鞋底/身體重心）在圖片上的 [xPct, yPct]，
// 用來把「站立點」對齊格子中心，而非用圖片幾何中心對齊（否則棋子會比格子低半個身體）。
// 之後美術微調角色圖，只需調整這兩個數字。
const RUNNER_ANCHOR: [number, number] = [50, 80]
// 機會/命運格位置（僅用於校準模式標記上色，玩法判定仍完全由後端決定）
const CHANCE_POS = [6, 15, 25, 35]
const DESTINY_POS = [10, 18, 30, 39]

function sleep(ms: number) {
  return new Promise<void>((resolve) => window.setTimeout(resolve, ms))
}

// 校準模式用：把目前 46 格座標序列化成與 BOARD_COORDS 同格式的字串，方便直接複製取代
function formatCoords(coords: [number, number][]): string {
  const rows: string[] = []
  for (let i = 0; i < coords.length; i += 10) {
    const chunk = coords.slice(i, i + 10).map(([px, py]) => `[${px}, ${py}]`).join(', ')
    rows.push('  ' + chunk + ',')
  }
  return '[\n' + rows.join('\n') + '\n]'
}

type Phase = 'idle' | 'dice' | 'moving'

export default function MonopolyScreen({ onBack }: { onBack: () => void }) {
  const user = useUser()
  const [position, setPosition] = useState(0)
  const [laps, setLaps] = useState(0)
  const [gpBalance, setGpBalance] = useState<number | null>(null)
  const [diceCost, setDiceCost] = useState(3)
  const [loading, setLoading] = useState(true)
  const [loadErr, setLoadErr] = useState(false)

  const [phase, setPhase] = useState<Phase>('idle')
  const [dieFace, setDieFace] = useState(1)
  const [rollErr, setRollErr] = useState('')
  const [drawModal, setDrawModal] = useState<{ landedOn: 'chance' | 'destiny'; result: DrawResult } | null>(null)
  const [codeCopied, setCodeCopied] = useState(false)
  const [lapCelebration, setLapCelebration] = useState<{ laps: number; gp: number } | null>(null)
  const [startFlash, setStartFlash] = useState(false)
  const [showGpInfo, setShowGpInfo] = useState(false)

  // 校準模式（?cal=1）：開發用，拖曳 46 格標記校正座標；一般玩家完全不受影響
  const [calMode] = useState<boolean>(
    () => typeof window !== 'undefined' && new URLSearchParams(window.location.search).get('cal') === '1'
  )
  const [calCoords, setCalCoords] = useState<[number, number][]>(
    () => BOARD_COORDS.map(([px, py]) => [px, py] as [number, number])
  )
  const [draggingIndex, setDraggingIndex] = useState<number | null>(null)
  const [copied, setCopied] = useState(false)
  const boardWrapRef = useRef<HTMLDivElement | null>(null)

  const dieIntervalRef = useRef<number | null>(null)

  useEffect(() => {
    let cancelled = false
    async function load() {
      if (!getUserToken()) { setLoading(false); return }
      try {
        const st = await withUserAuth((t) => monopolyApi.state(t))
        if (cancelled) return
        setPosition(st.position)
        setLaps(st.laps_completed)
        setGpBalance(st.gp_balance)
        setDiceCost(st.dice_gp_cost)
      } catch {
        if (!cancelled) setLoadErr(true)
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => { cancelled = true }
  }, [])

  useEffect(() => () => {
    if (dieIntervalRef.current != null) window.clearInterval(dieIntervalRef.current)
  }, [])

  // 預載 6 張骰面圖，避免第一次滾動動畫時才載入造成閃爍
  useEffect(() => {
    for (let i = 1; i <= 6; i++) {
      const img = new Image()
      img.src = `/ui/bg/DOR-Dice-${i}.png`
    }
  }, [])

  const busy = phase !== 'idle'
  const canAfford = gpBalance != null && gpBalance >= diceCost

  async function movePiece(res: MonopolyRollResult) {
    setPhase('moving')
    const steps: number[] = []
    for (let i = 1; i <= res.roll; i++) steps.push((res.from + i) % BOARD_SIZE)
    for (const p of steps) {
      setPosition(p)
      if (p === 0 && p !== res.to) {
        // 中途繞經 START（尚未走完，仍是繞圈路徑上的一步）→ 閃一下讓玩家有「經過感」
        setStartFlash(true)
        window.setTimeout(() => setStartFlash(false), 260)
      }
      // eslint-disable-next-line no-await-in-loop
      await sleep(250)
    }
    setGpBalance(res.gp_balance)
    if (res.draw_pending && res.draw_result) {
      setDrawModal({ landedOn: res.landed_on as 'chance' | 'destiny', result: res.draw_result })
    } else if (res.laps_gained > 0) {
      setLapCelebration({ laps: laps + res.laps_gained, gp: res.lap_reward_gp })
    }
    setLaps((l) => l + res.laps_gained)
    setPhase('idle')
  }

  async function handleRoll() {
    if (busy || !canAfford) return
    setRollErr('')
    setPhase('dice')
    setDieFace(1 + Math.floor(Math.random() * 6))
    dieIntervalRef.current = window.setInterval(() => {
      setDieFace(1 + Math.floor(Math.random() * 6))
    }, 90)

    const minAnimMs = 900
    const startedAt = Date.now()
    let res: MonopolyRollResult
    try {
      res = await withUserAuth((t) => monopolyApi.roll(t))
    } catch (e: any) {
      if (dieIntervalRef.current != null) { window.clearInterval(dieIntervalRef.current); dieIntervalRef.current = null }
      setPhase('idle')
      setRollErr(e?.status === 409 ? 'GP 不足，無法擲骰' : '擲骰失敗，請稍後再試')
      return
    }
    const wait = Math.max(0, minAnimMs - (Date.now() - startedAt))
    await sleep(wait)
    if (dieIntervalRef.current != null) { window.clearInterval(dieIntervalRef.current); dieIntervalRef.current = null }
    setDieFace(res.roll) // 動畫收尾＝後端回傳的點數，前端絕不自行決定結果
    await sleep(280) // 停頓看清骰面，再開始走格子
    await movePiece(res)
  }

  // 兌換碼複製（機會/命運 · redemption_code 結果用）
  async function handleCopyCode(code: string) {
    try {
      await navigator.clipboard.writeText(code)
      setCodeCopied(true)
      window.setTimeout(() => setCodeCopied(false), 1600)
    } catch {
      // 剪貼簿權限被拒或不可用時忽略，玩家仍可長按手動選取複製
    }
  }

  // ---- 校準模式：拖曳標記、複製座標（不影響一般玩家流程） ----
  function handleMarkerDown(idx: number, e: React.PointerEvent<HTMLDivElement>) {
    e.preventDefault()
    e.currentTarget.setPointerCapture(e.pointerId)
    setDraggingIndex(idx)
  }
  function handleMarkerMove(idx: number, e: React.PointerEvent<HTMLDivElement>) {
    if (draggingIndex !== idx || !boardWrapRef.current) return
    const rect = boardWrapRef.current.getBoundingClientRect()
    const rawX = ((e.clientX - rect.left) / rect.width) * 100
    const rawY = ((e.clientY - rect.top) / rect.height) * 100
    const px = Math.max(0, Math.min(100, Math.round(rawX * 10) / 10))
    const py = Math.max(0, Math.min(100, Math.round(rawY * 10) / 10))
    setCalCoords((prev) => {
      const next = prev.slice() as [number, number][]
      next[idx] = [px, py]
      return next
    })
  }
  function handleMarkerUp(idx: number) {
    if (draggingIndex === idx) setDraggingIndex(null)
  }
  async function handleCopyCoords() {
    const text = formatCoords(calCoords)
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    } catch {
      // 剪貼簿權限被拒或不可用時，使用者仍可從下方文字區手動選取複製
    }
  }

  if (calMode) {
    return (
      <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)', position: 'relative' }}>
        <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
          <button onClick={onBack} style={backBtn}>← 返回</button>
          <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>🛠️ 座標校準模式</span>
        </header>
        <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '10px 18px 28px' }}>
          <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginBottom: 10, lineHeight: 1.6 }}>
            拖曳每個編號標記到底圖對應格子中心（藍=機會、紫=命運、金=START），完成後按「複製座標」貼回給開發者。
          </div>
          <div
            ref={boardWrapRef}
            style={{ position: 'relative', width: '100%', maxWidth: 480, margin: '0 auto', touchAction: 'none' }}
          >
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src={BOARD_IMG} alt="環台盤面" draggable={false}
              style={{ width: '100%', display: 'block', borderRadius: 12, userSelect: 'none', pointerEvents: 'none' }}
            />
            {calCoords.map(([px, py], idx) => {
              const isChance = CHANCE_POS.includes(idx)
              const isDestiny = DESTINY_POS.includes(idx)
              const bg = idx === 0 ? '#e7b84b' : isChance ? '#3f8fe0' : isDestiny ? '#9a5be0' : 'rgba(20,20,20,.78)'
              return (
                <div
                  key={idx}
                  onPointerDown={(e) => handleMarkerDown(idx, e)}
                  onPointerMove={(e) => handleMarkerMove(idx, e)}
                  onPointerUp={() => handleMarkerUp(idx)}
                  onPointerCancel={() => handleMarkerUp(idx)}
                  style={{
                    position: 'absolute', left: `${px}%`, top: `${py}%`, transform: 'translate(-50%,-50%)',
                    width: 24, height: 24, borderRadius: '50%', background: bg, color: '#fff',
                    display: 'flex', alignItems: 'center', justifyContent: 'center',
                    fontSize: 10, fontWeight: 900, border: '2px solid #fff', boxShadow: '0 2px 6px rgba(0,0,0,.4)',
                    cursor: 'grab', touchAction: 'none', userSelect: 'none',
                    zIndex: draggingIndex === idx ? 20 : 10,
                  }}
                >
                  {idx}
                </div>
              )
            })}
          </div>

          <div style={{ marginTop: 18, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8 }}>
            <button onClick={handleCopyCoords} style={rollBtn}>複製座標</button>
            {copied && <div style={{ fontSize: 12.5, color: 'var(--violet)', fontWeight: 700 }}>已複製</div>}
            <textarea
              readOnly
              value={formatCoords(calCoords)}
              onFocus={(e) => e.currentTarget.select()}
              style={{
                width: '100%', maxWidth: 480, height: 180, fontSize: 11, fontFamily: 'monospace',
                color: 'var(--tx-dim)', background: 'var(--bg-1)', border: '1px solid var(--line)',
                borderRadius: 8, padding: 8, boxSizing: 'border-box',
              }}
            />
          </div>
        </div>
      </div>
    )
  }

  const [x, y] = BOARD_COORDS[position]
  const [startX, startY] = BOARD_COORDS[0]

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)', position: 'relative' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>🎲 環台大富翁</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '10px 18px 28px' }}>
        {!user ? (
          <div style={{ color: 'var(--tx-dim)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>請先登入才能遊玩</div>
        ) : loading ? (
          <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>載入中…</div>
        ) : loadErr ? (
          <div style={{ color: 'var(--hunt)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>載入失敗，請稍後再試</div>
        ) : (
          <>
            {/* 狀態列：目前圈數 */}
            <div style={{ display: 'flex', alignItems: 'center', marginBottom: 14 }}>
              <span style={{ fontSize: 13, fontWeight: 700, color: 'var(--tx-dim)' }}>第 {laps} 圈</span>
            </div>

            {/* 盤面 */}
            <div style={{ position: 'relative', width: '100%', maxWidth: 480, margin: '0 auto' }}>
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={BOARD_IMG} alt="環台盤面" style={{ width: '100%', display: 'block', borderRadius: 12 }} />

              {/* START 經過閃光 */}
              {startFlash && (
                <div style={{
                  position: 'absolute', left: `${startX}%`, top: `${startY}%`, width: '13%', aspectRatio: '1/1',
                  transform: 'translate(-50%,-50%)', borderRadius: '50%',
                  background: 'radial-gradient(circle, rgba(231,184,75,.55), rgba(231,184,75,0) 70%)',
                  animation: 'monoStartFlash .26s ease-out', pointerEvents: 'none',
                }} />
              )}

              {/* 棋子：外層負責「定位」（站立點對齊格子中心）+ 移動 transition；
                  內層負責「走路跳動」動畫。兩層 transform 分開，避免 hop 動畫覆蓋定位位移。 */}
              <div style={{
                position: 'absolute', left: `${x}%`, top: `${y}%`, width: '19%', aspectRatio: '1/1',
                transform: `translate(-${RUNNER_ANCHOR[0]}%, -${RUNNER_ANCHOR[1]}%)`,
                transition: 'left .22s linear, top .22s linear',
                pointerEvents: 'none', zIndex: 5,
              }}>
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img src={RUNNER_IMG} alt="" style={{
                  width: '100%', height: '100%', display: 'block',
                  animation: phase === 'moving' ? 'monoRunnerHop .25s ease-in-out infinite' : 'none',
                  filter: phase === 'moving' ? 'drop-shadow(0 4px 6px rgba(0,0,0,.35))' : 'none',
                }} />
              </div>

              {/* 中央小骰盤：只在擲骰/移動時顯示（待機時中央地圖乾淨），絕對 overlay 淡入淡出不佔版面、不造成位移 */}
              <div style={{
                position: 'absolute', left: '50%', top: '48%', transform: 'translate(-50%,-50%)',
                width: '40%', zIndex: 8,
                background: 'url(/ui/bg/bg_roll_the_dice_tray.png) center / 100% 100% no-repeat',
                borderRadius: 14, boxShadow: '0 6px 20px rgba(0,0,0,.28)',
                display: 'flex', alignItems: 'center', justifyContent: 'center',
                padding: '9%', boxSizing: 'border-box',
                opacity: phase === 'idle' ? 0 : 1, pointerEvents: 'none', transition: 'opacity .2s ease',
              }}>
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img
                  src={`/ui/bg/DOR-Dice-${dieFace}.png`} alt=""
                  style={{
                    width: '62%', height: 'auto', objectFit: 'contain', display: 'block', flexShrink: 0, margin: '0 auto',
                    animation: phase === 'dice' ? 'monoDiceShake .09s linear infinite' : 'none',
                  }}
                />
              </div>

              {/* 底部擲骰按鈕列：常駐，位於盤面南端一帶，GP 整合進按鈕內文；「！」說明鈕獨立於按鈕右側避免點擊衝突 */}
              <div style={{
                position: 'absolute', left: '50%', bottom: '4%', transform: 'translateX(-50%)',
                zIndex: 9, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 4,
              }}>
                {/* GP不足／rollErr 訊息：固定高度，避免有無訊息造成位移 */}
                <div style={{ height: 13, fontSize: 10.5, color: 'var(--hunt)', fontWeight: 700, textAlign: 'center', lineHeight: 1 }}>
                  {!canAfford && phase === 'idle' ? 'GP 不足，無法擲骰' : rollErr}
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <button
                    onClick={handleRoll}
                    disabled={busy || !canAfford}
                    style={{
                      ...rollBtn,
                      display: 'inline-flex', alignItems: 'center', gap: 6, whiteSpace: 'nowrap',
                      padding: '10px 22px', fontSize: 13.5,
                      opacity: busy || !canAfford ? 0.55 : 1,
                      cursor: busy || !canAfford ? 'default' : 'pointer',
                    }}
                  >
                    <GpCoin size={16} />{(gpBalance ?? 0).toLocaleString()} · {phase === 'dice' ? '擲骰中…' : phase === 'moving' ? '前進中…' : `擲骰 ${diceCost} GP`}
                  </button>
                  <button
                    onClick={(e) => { e.stopPropagation(); setShowGpInfo((v) => !v) }}
                    aria-label="如何取得 GP"
                    style={{
                      width: 22, height: 22, borderRadius: '50%', border: '1px solid var(--line)',
                      background: 'var(--bg-1)', color: 'var(--tx-dim)', fontSize: 12, fontWeight: 900,
                      lineHeight: 1, display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
                      padding: 0, cursor: 'pointer', fontFamily: 'inherit', flexShrink: 0,
                      boxShadow: '0 2px 6px rgba(0,0,0,.25)',
                    }}
                  >！</button>
                </div>
              </div>
            </div>
          </>
        )}
      </div>

      {/* GP 取得說明 */}
      {showGpInfo && (
        <div style={overlayStyle} onClick={() => setShowGpInfo(false)}>
          <div style={cardStyle} onClick={(e) => e.stopPropagation()}>
            <div style={{ fontSize: 16, fontWeight: 900, color: 'var(--tx)' }}>如何取得 GP</div>
            <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 8, lineHeight: 1.7 }}>
              進行城市探索打卡、以及參與活動（賽事）都可以隨機獲得 GP。
            </div>
            <button onClick={() => setShowGpInfo(false)} style={{ ...rollBtn, marginTop: 16, padding: '9px 28px', fontSize: 13.5 }}>知道了</button>
          </div>
        </div>
      )}

      {/* 機會/命運 揭曉彈窗（擲骰→移動動畫結束後、停在機會/命運格且實際抽到獎勵才顯示） */}
      {drawModal && (
        <div style={overlayStyle} onClick={() => { setDrawModal(null); setCodeCopied(false) }}>
          <div
            style={{ ...cardStyle, padding: 0, overflow: 'hidden', animation: 'monoDrawReveal .38s cubic-bezier(.22,.9,.32,1.18)' }}
            onClick={(e) => e.stopPropagation()}
          >
            <div style={{
              padding: '20px 22px 16px',
              // 主題色與知識探索圖鑑一致：機會（跑者訓練）＝綠 --fug，命運（跑者照護）＝金 --gold（金底一律白字）。
              background: drawModal.landedOn === 'chance'
                ? 'linear-gradient(135deg, rgba(255,255,255,.2), rgba(255,255,255,0) 55%), var(--fug)'
                : 'linear-gradient(135deg, rgba(255,255,255,.2), rgba(255,255,255,0) 55%), var(--gold)',
              color: drawModal.landedOn === 'chance' ? 'var(--fug-ink)' : '#fff',
            }}>
              <div style={{ fontSize: 30, lineHeight: 1 }}>{drawModal.landedOn === 'chance' ? '🎁' : '📜'}</div>
              <div style={{ fontSize: 17, fontWeight: 900, marginTop: 4 }}>{drawModal.landedOn === 'chance' ? '機會' : '命運'}</div>
            </div>
            <div style={{ padding: '18px 22px 22px', textAlign: 'center' }}>
              <DrawResultBody result={drawModal.result} codeCopied={codeCopied} onCopyCode={handleCopyCode} />
              <button
                onClick={() => { setDrawModal(null); setCodeCopied(false) }}
                style={{ ...rollBtn, marginTop: 18, padding: '9px 28px', fontSize: 13.5, width: '100%' }}
              >知道了</button>
            </div>
          </div>
        </div>
      )}

      {/* 繞圈慶祝 */}
      {lapCelebration && (
        <div style={overlayStyle} onClick={() => setLapCelebration(null)}>
          <div style={cardStyle} onClick={(e) => e.stopPropagation()}>
            <div style={{ fontSize: 32, marginBottom: 8 }}>🏁</div>
            <div style={{ fontSize: 16, fontWeight: 900, color: 'var(--tx)' }}>繞完第 {lapCelebration.laps} 圈！</div>
            {lapCelebration.gp > 0 && (
              <div style={{
                display: 'inline-flex', alignItems: 'center', gap: 6, marginTop: 10,
                background: 'var(--gold)', color: '#fff', fontWeight: 900, fontSize: 14,
                borderRadius: 999, padding: '6px 16px',
              }}>
                <GpCoin size={16} /> +{lapCelebration.gp} GP
              </div>
            )}
            <button onClick={() => setLapCelebration(null)} style={{ ...rollBtn, marginTop: 16, padding: '9px 28px', fontSize: 13.5 }}>太棒了</button>
          </div>
        </div>
      )}

      <style>{`
        @keyframes monoDiceShake { 0%{transform:rotate(-6deg) scale(1)} 50%{transform:rotate(6deg) scale(1.06)} 100%{transform:rotate(-6deg) scale(1)} }
        @keyframes monoRunnerHop { 0%,100%{transform:translateY(0) scale(1)} 50%{transform:translateY(-14%) scale(1.06)} }
        @keyframes monoStartFlash { 0%{opacity:0;transform:translate(-50%,-50%) scale(.6)} 40%{opacity:1} 100%{opacity:0;transform:translate(-50%,-50%) scale(1.3)} }
        @keyframes monoDrawReveal { 0%{opacity:0;transform:scale(.8)} 60%{opacity:1;transform:scale(1.04)} 100%{opacity:1;transform:scale(1)} }
      `}</style>
    </div>
  )
}

// 機會/命運揭曉彈窗內容：依 draw_result.type 分派展示樣式。
function DrawResultBody({ result, codeCopied, onCopyCode }: { result: DrawResult; codeCopied: boolean; onCopyCode: (code: string) => void }) {
  switch (result.type) {
    case 'knowledge_card':
      return (
        <div style={{ textAlign: 'left' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', marginBottom: 10 }}>
            {result.main_category && <span style={tagStyle}>{result.main_category}</span>}
            {result.rarity === 'rare' && <span style={rareBadge}>★ 稀有</span>}
          </div>
          {result.image_url && (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={result.image_url} alt="" style={{ width: '100%', maxHeight: 160, objectFit: 'cover', borderRadius: 10, marginBottom: 10 }} />
          )}
          <div style={{ fontSize: 16, fontWeight: 900, color: 'var(--tx)' }}>{result.title}</div>
          {result.body && <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 8, lineHeight: 1.7 }}>{result.body}</div>}
          {result.player_action && (
            <div style={{ marginTop: 10, background: 'var(--bg-2)', borderRadius: 10, padding: '8px 12px' }}>
              <div style={{ fontSize: 11, fontWeight: 800, color: 'var(--fug)' }}>玩家行動</div>
              <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 3, lineHeight: 1.6 }}>{result.player_action}</div>
            </div>
          )}
          {result.is_duplicate && (
            <div style={{ marginTop: 10, display: 'inline-block', background: 'var(--gold)', color: '#fff', fontWeight: 800, fontSize: 12, borderRadius: 999, padding: '6px 14px' }}>
              重複卡，已轉 {result.converted_gp ?? 0} GP
            </div>
          )}
          {result.risk_note && (
            <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 10, lineHeight: 1.6 }}>{result.risk_note}</div>
          )}
        </div>
      )
    case 'gp':
    case 'dp':
      return (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 10 }}>
          <div style={{ fontSize: 15, fontWeight: 900, color: 'var(--tx)' }}>獲得 {result.type.toUpperCase()}</div>
          <div style={{ display: 'inline-flex', alignItems: 'center', gap: 8, fontSize: 26, fontWeight: 900, color: result.type === 'gp' ? 'var(--violet)' : 'var(--gold)' }}>
            {result.type === 'gp' ? <GpCoin size={28} /> : <DpCoin size={28} />}
            +{result.amount ?? 0}
          </div>
        </div>
      )
    case 'vip_days':
      return (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 6 }}>
          <div style={{ fontSize: 15, fontWeight: 900, color: 'var(--tx)' }}>獲得 VIP 天數</div>
          <div style={{ fontSize: 26, fontWeight: 900, color: 'var(--gold)' }}>+{result.amount ?? 0} 天</div>
        </div>
      )
    case 'redemption_code': {
      const code = result.code
      return (
        <div>
          <div style={{ fontSize: 15, fontWeight: 900, color: 'var(--tx)' }}>{result.title || '兌換碼'}</div>
          {result.kind && (
            <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 4 }}>{result.kind === 'line_point' ? 'LINE POINT' : '優惠券'}</div>
          )}
          {result.is_fallback ? (
            <div style={{ marginTop: 12, display: 'inline-block', background: 'var(--gold)', color: '#fff', fontWeight: 800, fontSize: 12, borderRadius: 999, padding: '6px 14px' }}>
              庫存已滿，改發 {result.converted_gp ?? 0} GP
            </div>
          ) : code ? (
            <button
              onClick={() => onCopyCode(code)}
              style={{
                marginTop: 12, width: '100%', border: '1px dashed var(--line-2)', borderRadius: 10,
                background: 'var(--bg-2)', color: 'var(--tx)', fontFamily: 'monospace', fontSize: 15,
                fontWeight: 800, padding: '10px 12px', cursor: 'pointer', letterSpacing: 1,
              }}
            >
              {code}{codeCopied ? '（已複製）' : '（點擊複製）'}
            </button>
          ) : null}
          {result.body && <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 10, lineHeight: 1.6 }}>{result.body}</div>}
        </div>
      )
    }
    default:
      // sticker（貼紙）等其餘類型：通用展示 fallback
      return (
        <div>
          {result.image_url && (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={result.image_url} alt="" style={{ width: '100%', maxHeight: 160, objectFit: 'cover', borderRadius: 10, marginBottom: 10 }} />
          )}
          <div style={{ fontSize: 15, fontWeight: 900, color: 'var(--tx)' }}>{result.title || '獲得獎勵'}</div>
          {result.body && <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 8, lineHeight: 1.7 }}>{result.body}</div>}
        </div>
      )
  }
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
const rollBtn: React.CSSProperties = {
  background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', borderRadius: 999,
  padding: '13px 34px', fontSize: 15.5, fontWeight: 900, fontFamily: 'inherit',
}
const overlayStyle: React.CSSProperties = {
  position: 'absolute', inset: 0, background: 'rgba(10,13,12,.62)', display: 'flex',
  alignItems: 'center', justifyContent: 'center', zIndex: 700, padding: 24,
}
const cardStyle: React.CSSProperties = {
  background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 16, padding: '24px 22px',
  maxWidth: 320, width: '100%', textAlign: 'center', boxShadow: '0 12px 40px rgba(0,0,0,.35)',
}
const tagStyle: React.CSSProperties = { fontSize: 10.5, fontWeight: 700, color: 'var(--tx-dim)', background: 'var(--bg-2)', borderRadius: 999, padding: '3px 10px' }
const rareBadge: React.CSSProperties = { fontSize: 10.5, fontWeight: 800, color: '#fff', background: 'var(--gold)', borderRadius: 999, padding: '3px 10px' }
