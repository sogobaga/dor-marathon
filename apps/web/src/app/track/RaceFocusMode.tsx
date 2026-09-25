'use client'

// 專注模式＝鎖定模式（2026-09-25 CONTRACT.md track_autolock，取代同日稍早「口袋模式併入專注模式」版本）：
// 開啟即整個疊層攔截所有輸入，沒有「可互動／已鎖定」兩層子狀態——唯一可操作的是底部鎖頭，長按 1.5 秒
// 離開專注模式回到完整介面。取代舊版「顯示完整介面／🔒 鎖定／📣 測試應援」按鈕與閒置 10 秒自動上鎖、
// 獨立的 FocusLockScreen.tsx 元件（其長按解鎖進度環邏輯已直接搬進本檔，見下方 startHold/holdTick）。
// 任何 tracking 中的跑步都能切入的全螢幕大字資訊疊層，套在 track 頁上。
// 純顯示/提醒，不寫入任何 GPS/里程/課表/事件任務狀態——所有數據皆由父層（track/page.tsx）算好傳入，
// 這裡只讀不算第二套，也完全不碰 WorkoutHud/課表引擎/事件任務引擎的邏輯（它們在底下照常運作，
// 專注模式只是蓋在上面的顯示層，見 track/page.tsx 掛載處的 zIndex 說明）。
//
// strategy 可為 null（一般跑步/課表/個人任務等沒有賽事策略的情境）：此時只顯示大字 移動距離/時間/
// 平均配速/當下分段配速，策略專屬的「目標配速/預計完成/補給引擎/配速偏差提醒」整組不渲染（下面每個
// strategy 專屬區塊都用 `strategy &&` 或 `if (!strategy) return` 短路，讀者可以直接搜 `strategy` 找全部）。
// 開跑一律自動進入專注模式（見 track/page.tsx start()／resumeActiveRun() 對 initialOpen 的寫入），
// 首次提示尚未看過時例外：由父層先把 initialOpen 帶 false，等使用者按下提示「知道了」再透過 openSignal
// （遞增序號）命令本元件開啟，見下方該 prop 的 effect。
//
// 引擎狀態機（僅在有 strategy 時運作）：pace 提醒＝「差值判斷 + 60 秒同方向去重」的邊緣觸發器；
// fuel 提醒＝「單一游標依序消化」的有限狀態機（等待 → 倒數顯示 → due（醒目 60 秒後自動前進）→
// 游標前進取下一點），兩者互不干擾、各自用 ref 存計時器，不依賴 React effect 的自動 cleanup 時機
//（避免 GPS 高頻重繪把倒數計時器提前清掉的競態）。專注模式中無法手動按「補給完成」（整層攔截輸入，
// 只有底部鎖頭可操作）——到期提醒改為純顯示，60 秒後自動視為完成並前進到下一個補給點。
//
// 口徑決策（v0.1.5xx 時間口徑修正）：比賽情境的時鐘＝大會時間，不因站著不動而停錶，站定不動看到
// 「時間」歸零／不走會被誤以為故障。因此主要顯示指標（時間／平均配速／預計完成 ETA）一律改用
// elapsed（page.tsx 由 250ms interval 驅動、開跑起算的總牆鐘秒數）與其對應的總時間平均配速 avgPace
// （elapsed/distance），三者同一把尺、同步前進，不會出現「時間在走、平均配速或 ETA 卻凍結」的矛盾。
// 例外只有一處，刻意維持「移動中表現」口徑：
//   - 配速偏差提醒（引擎內部）：比較對象是 movingSegLivePace vs 目標配速——扣掉停等時間才有教練
//     意義，等紅燈不該被提醒「太慢」。
// 顯示層的「分段即時配速」大字（2026-08-27 使用者拍板）：改吃與主面板四格完全相同的 segLivePace
// （全程口徑）——本疊層定位是背景面板的放大鏡，四個大字必須跟四格數字一模一樣，否則「當下分段配速
// 10:21 vs 分段即時配速 12:52」同名不同數會被當成 bug（使用者實測回報）。移動口徑的分段配速在主
// 畫面次要列「分段」仍看得到；偏差提醒引擎與顯示脫鉤、各用各的口徑。
// 補給提醒引擎 time 模式門檻同樣用 elapsed（FuelPoint.at 契約＝「開跑後秒數」，比賽時鐘不停錶），
// 本疊層已完全不吃移動時間（movingS），移動時間僅存在於一般介面的量測列。
// 一般跑步/課表/個人任務（無 strategy）的「移動距離／時間／平均配速／當下分段配速」4 大字指標同一套
// 邏輯，時間口徑統一採 elapsed；至於 page.tsx 主畫面（非本疊層）「移動時間/移動配速/分段」那排維持
// 原樣不動——那是給一般訓練情境參考用的移動口徑，與本疊層各自獨立。

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { FUEL_KIND_LABEL, type RaceStrategy, type StrategySegment } from '@/lib/api'
import { fmtKm, goalProgressRatio, type RunGoal } from '@/lib/runGoal'

const HOLD_MS = 1500 // 底部鎖頭長按離開專注模式所需時長（與舊 FocusLockScreen 解鎖時長一致）

// 取整口徑必須與 track/page.tsx 的 fmtTime 完全一致（一律 Math.floor）：elapsed 是帶小數的秒數，
// 若這裡先 Math.round、主面板 Math.floor，同一個值會顯示成差 1 秒的兩個數字（使用者實測回報過）。
function fmtTime(s: number) {
  const v = Math.max(0, Math.floor(s))
  const h = Math.floor(v / 3600), m = Math.floor((v % 3600) / 60), sec = v % 60
  const p = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${p(m)}:${p(sec)}` : `${p(m)}:${p(sec)}`
}
function fmtPace(s: number) {
  if (!s || !isFinite(s) || s <= 0) return '--:--'
  // 先整體四捨五入再拆分秒，避免 479.6 → 「7:60」（秒位獨立 round 到 60 的邊界錯誤）
  const v = Math.round(s)
  return `${Math.floor(v / 60)}:${String(v % 60).padStart(2, '0')}`
}

// 依分段目標配速，推算「跑到某公里數」預計耗費的移動秒數（假設全程照分段目標配速跑）。
// 用途：把 distance 模式補給點的 at(公尺) 換算成與 time 模式 at(秒) 同一基準，兩種模式才能混在一起排序、依序消化。
function predictedTimeAtKm(km: number, segments: StrategySegment[]): number {
  let t = 0
  for (const seg of segments) {
    if (km <= seg.from_km) break
    const covered = Math.min(km, seg.to_km) - seg.from_km
    if (covered > 0) t += covered * seg.pace_s
    if (km <= seg.to_km) break
  }
  return t
}

type PaceDir = 'fast' | 'slow'

export default function RaceFocusMode({
  strategy, distanceM, elapsed, avgPace, segLivePace, movingSegLivePace, hasSignal, goal,
  initialOpen, openSignal, onOpenChange,
}: {
  strategy: RaceStrategy | null // null＝一般跑步/課表/個人任務等沒有賽事策略的情境，只顯示基本 4 大字指標
  distanceM: number // 目前有效距離（公尺）——與頁面主面板「距離」同一份數據（distRef）
  elapsed: number // 開跑後總牆鐘秒數，不因靜止而停錶——頁面既有 elapsed（250ms tick 驅動，天然平滑）；
  // 比賽情境＝大會時間口徑，本疊層「時間」大字、ETA 推估、補給 time 模式門檻都吃這個
  avgPace: number // 總時間平均配速（秒/公里，elapsed/distance；未達門檻為 0）——頁面既有 avgPace；
  // 與 elapsed 同一把尺，供「平均配速」大字與 ETA 推估共用，避免跟時間指標不同步
  segLivePace: number // 目前這 1km 的全程口徑即時配速（秒/公里；未達門檻為 0）——與四格「分段即時配速」
  // 同一個值，供「分段即時配速」大字顯示（放大鏡原則，見上方口徑決策說明）
  movingSegLivePace: number // 目前這 1km 的移動時間即時配速（秒/公里；未達門檻為 0）——只供配速偏差
  // 提醒引擎內部比較用，不再上畫面（見上方口徑決策說明）
  hasSignal: boolean // 目前是否有 GPS 訊號（頁面既有 !!curPos）——供疊層底部訊號點顯示
  goal: RunGoal // 本次跑步目標（distance/time/none，見 lib/runGoal.ts resolveRunGoal）——驅動進度條
  initialOpen?: boolean // 由父層決定掛載當下是否直接開啟（省略時視同 false）：一般為 true（開跑/接續
  // 一律進入專注模式）；首次提示尚未看過時父層帶 false，讓完整介面先可見，見下方 openSignal 說明
  openSignal?: number // 父層命令「現在進入專注模式」的遞增序號（首次提示按下「知道了」時 bump）——
  // 序號變動（而非布林本身變 true）才觸發，避免同一個 true 值連續下達時因值未變化被 effect 忽略
  onOpenChange?: (open: boolean) => void // hidden 狀態改變（含掛載當下）時通知父層——父層藉此把
  // open 狀態寫進 activeRun.focusOpen（供重開頁面判斷），也藉此得知是否要抑制新事件任務觸發、
  // CheerShow 是否要提高 z-index（見 track/page.tsx 呼叫處）
}) {
  const [hidden, setHidden] = useState(() => !initialOpen)
  useEffect(() => { onOpenChange?.(!hidden) }, [hidden]) // eslint-disable-line react-hooks/exhaustive-deps -- 只在 hidden 變動（含掛載當下）通知父層，onOpenChange 允許每次 render 傳新的閉包
  const distKm = distanceM / 1000

  // 整層攔截桌機滑鼠滾輪（見下方疊層 ref）：React 17+ 對 wheel/touchmove 一律以 passive:true 掛在
  // document 根節點，JSX 上的 onWheel={preventDefault} 是 no-op（瀏覽器直接忽略、DevTools 會警告
  // 「Unable to preventDefault inside passive event listener invocation」），2026-09-25 review 抓到
  // 桌機滾輪理論上仍可捲動底下頁面。改用原生 addEventListener 顯式帶 {passive:false} 才擋得住；
  // touch 手勢（滑動/縮放）已由 CSS touchAction:'none' 擋掉，不需要也不再用 JS 攔截 touchmove。
  const overlayRef = useRef<HTMLDivElement | null>(null)
  useEffect(() => {
    if (hidden) return
    const el = overlayRef.current
    if (!el) return
    const stopWheel = (e: Event) => e.preventDefault()
    el.addEventListener('wheel', stopWheel, { passive: false })
    return () => el.removeEventListener('wheel', stopWheel)
  }, [hidden])

  // 首次提示「知道了」命令開啟（見上方 openSignal prop 說明）：序號變動才觸發，掛載當下若父層剛好帶入
  // 與上次相同的初始值不會誤觸發。
  const openSignalSeenRef = useRef(openSignal)
  useEffect(() => {
    if (openSignal !== undefined && openSignal !== openSignalSeenRef.current) {
      openSignalSeenRef.current = openSignal
      setHidden(false)
    }
  }, [openSignal])

  // ── 底部鎖頭長按 1.5 秒離開專注模式（原 FocusLockScreen 的長按進度環邏輯，併入本檔）──
  const [holdProgress, setHoldProgress] = useState(0) // 0..1，長按進度環
  const holdRafRef = useRef<number | null>(null)
  const holdStartRef = useRef(0)
  const holdingRef = useRef(false)

  const stopHold = useCallback(() => {
    holdingRef.current = false
    if (holdRafRef.current != null) { cancelAnimationFrame(holdRafRef.current); holdRafRef.current = null }
    setHoldProgress(0)
  }, [])

  const holdTick = useCallback(() => {
    if (!holdingRef.current) return
    const p = Math.min(1, (Date.now() - holdStartRef.current) / HOLD_MS)
    setHoldProgress(p)
    if (p >= 1) { holdingRef.current = false; setHidden(true); return }
    holdRafRef.current = requestAnimationFrame(holdTick)
  }, [])

  const startHold = useCallback((e: React.PointerEvent) => {
    e.preventDefault(); e.stopPropagation()
    holdingRef.current = true
    holdStartRef.current = Date.now()
    holdRafRef.current = requestAnimationFrame(holdTick)
  }, [holdTick])

  useEffect(() => () => { if (holdRafRef.current != null) cancelAnimationFrame(holdRafRef.current) }, [])

  // 目前所在分段：落在 [from_km, to_km) 的那一段；已超過總距離則沿用最後一段的目標配速繼續顯示
  // （以下 strategy 專屬邏輯全部短路：無 strategy 時維持安全的空/零值，不渲染對應區塊）
  const curSeg: StrategySegment | null = strategy
    ? (strategy.segments.find((s) => distKm >= s.from_km && distKm < s.to_km) ?? strategy.segments[strategy.segments.length - 1] ?? null)
    : null
  const targetPaceS = curSeg?.pace_s ?? 0

  // ── 配速提醒：目前分段即時配速 vs 目前段目標配速，差超過 ±10s/km → 提示；同方向 60 秒內不重複跳 ──
  const [paceAlert, setPaceAlert] = useState<PaceDir | null>(null)
  const lastDirRef = useRef<PaceDir | null>(null)
  const lastAtRef = useRef(0)
  const alertTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => {
    if (!strategy || !targetPaceS || !movingSegLivePace) return
    const diff = movingSegLivePace - targetPaceS // 正值＝比目標慢，負值＝比目標快
    const dir: PaceDir | null = diff > 10 ? 'slow' : diff < -10 ? 'fast' : null
    if (!dir) return
    const now = Date.now()
    if (lastDirRef.current === dir && now - lastAtRef.current < 60000) return // 同方向 60 秒內不重複
    lastDirRef.current = dir; lastAtRef.current = now
    setPaceAlert(dir)
    try { navigator.vibrate?.(200) } catch { /* 無此 API 就略過 */ }
    if (alertTimerRef.current) clearTimeout(alertTimerRef.current)
    alertTimerRef.current = setTimeout(() => setPaceAlert(null), 4000)
    // 注意：不用 effect 的 return cleanup 清這顆計時器——GPS 高頻重繪會讓本 effect 頻繁重跑，
    // 若靠 cleanup 清時器，會在 4 秒未到前就被下一次（早退出的）重跑提前清掉，導致提示卡住不消失。
  }, [strategy, movingSegLivePace, targetPaceS])
  useEffect(() => () => { if (alertTimerRef.current) clearTimeout(alertTimerRef.current) }, [])

  // ── 補給提醒引擎：time/distance 兩種模式依「預計耗時」換算到同一時間軸排序，單一游標依序消化 ──
  const fuelSorted = useMemo(
    () => strategy
      ? strategy.fuel
          .map((f) => ({ ...f, _predS: f.mode === 'time' ? f.at : predictedTimeAtKm(f.at / 1000, strategy.segments) }))
          .sort((a, b) => a._predS - b._predS)
      : [],
    [strategy],
  )
  const [fuelIdx, setFuelIdx] = useState(0)
  const [dueActive, setDueActive] = useState(false)
  const dueTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const hasFuel = fuelIdx < fuelSorted.length
  const fp = fuelSorted[fuelIdx]
  // time 模式門檻用 elapsed：FuelPoint.at 契約即「開跑後秒數」（見 api.ts），且比賽時鐘不停錶——
  // 若用移動時間，站著休息時補給倒數會凍結，與「時間」大字口徑矛盾（使用者回報過同款觀感問題）。
  const remaining = hasFuel ? (fp.mode === 'time' ? fp.at - elapsed : fp.at - distanceM) : 0
  const due = hasFuel && remaining <= 0

  function advanceFuel() {
    if (dueTimerRef.current) clearTimeout(dueTimerRef.current)
    setDueActive(false)
    setFuelIdx((i) => i + 1)
  }
  useEffect(() => {
    if (hasFuel && due && !dueActive) {
      setDueActive(true)
      // 專注模式中無法手動按「補給完成」（整層攔截輸入）：到期提示顯示 60 秒後自動視為完成、換下一點。
      dueTimerRef.current = setTimeout(() => advanceFuel(), 60000)
    }
    // 同上：不用 return cleanup，避免高頻重繪把這顆計時器提前清掉
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasFuel, due, dueActive])
  useEffect(() => () => { if (dueTimerRef.current) clearTimeout(dueTimerRef.current) }, [])

  // 補給提示文字：due 期間顯示大字醒目提示（見下方）；之外依模式顯示「倒數中」或「尚未進入窗口」
  let fuelLine: string | null = null
  if (hasFuel && !due) {
    const label = FUEL_KIND_LABEL[fp.kind]
    if (fp.mode === 'time') {
      if (remaining <= 60) fuelLine = `${Math.max(0, Math.ceil(remaining))} 秒後補給：${label}`
      else if (remaining <= 180) fuelLine = `${Math.ceil(remaining / 60)} 分鐘後補給：${label}`
    } else if (remaining <= 100) {
      fuelLine = `距離補給點 ${Math.max(0, Math.ceil(remaining))}m：${label}`
    }
  }

  // ── 預計完成時間：口徑＝大會時間 elapsed（已耗牆鐘秒數，不停錶）+ 剩餘公里 × 總時間平均配速 avgPace；
  // 與上面「時間」「平均配速」大字同一把尺，避免時間持續前進、ETA 卻凍結在移動口徑上的矛盾。
  // 超過策略總距離則顯示「已達策略距離」。
  let etaLabel = '--:--'
  if (strategy) {
    if (distKm >= strategy.total_km) etaLabel = '已達策略距離'
    else if (avgPace > 0) etaLabel = fmtTime(elapsed + (strategy.total_km - distKm) * avgPace)
  }

  if (hidden) {
    return (
      <button
        data-skin="default"
        onClick={() => setHidden(false)}
        style={{
          position: 'fixed', right: 16, bottom: 'calc(100px + env(safe-area-inset-bottom))', zIndex: 600,
          background: 'rgba(11,14,19,.9)', color: 'var(--tx)', border: '1px solid rgba(255,194,75,.6)',
          borderRadius: 999, padding: '10px 16px', fontSize: 13, fontWeight: 800, cursor: 'pointer',
          boxShadow: '0 4px 16px rgba(0,0,0,.4)', fontFamily: 'inherit',
        }}
      >🏁 專注模式</button>
    )
  }

  const holdRingSize = 88, holdStroke = 5, holdR = (holdRingSize - holdStroke) / 2, holdC = 2 * Math.PI * holdR

  return (
    <div
      ref={overlayRef}
      data-skin="default"
      className="app-min-h"
      style={{
        position: 'fixed', inset: 0, zIndex: 3900, background: '#000',
        color: 'var(--tx)', display: 'flex', flexDirection: 'column', alignItems: 'center',
        justifyContent: 'space-between', padding: '24px 20px calc(20px + env(safe-area-inset-bottom))',
        textAlign: 'center', touchAction: 'none', userSelect: 'none', WebkitUserSelect: 'none',
      }}
      // 整層攔截所有輸入：唯一可操作的是下方鎖頭（其自身 onPointerDown 已 stopPropagation）。
      // touchAction:none 已擋掉捲動/縮放手勢；桌機滑鼠滾輪改由上方 overlayRef 的原生
      // {passive:false} 監聽器攔截（JSX onWheel/onTouchMove 對這兩個事件是 no-op，見上方宣告處說明）。
      onContextMenu={(e) => e.preventDefault()}
    >
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '1.6vh', width: '100%' }}>
        <GoalProgressBar goal={goal} distanceM={distanceM} elapsed={elapsed} />

        <div style={{ fontSize: 12, letterSpacing: '.15em', color: 'var(--tx-dim)', fontWeight: 700 }}>
          {strategy ? `比賽專注模式 · ${strategy.name}` : '專注模式'}
        </div>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '2vh' }}>
        <Metric label="移動距離" value={distKm.toFixed(2)} unit="km" size="xl" />
        <Metric label="時間" value={fmtTime(elapsed)} unit="" size="lg" />
        <div style={{ display: 'flex', gap: '6vw', justifyContent: 'center', flexWrap: 'wrap' }}>
          <Metric label="平均配速" value={fmtPace(avgPace)} unit="/km" size="md" />
          <Metric label="分段即時配速" value={fmtPace(segLivePace)} unit="/km" size="md" />
        </div>
        {/* 以下皆為賽事策略專屬區塊：無 strategy（一般跑步/課表/個人任務等）整組不渲染 */}
        {strategy && (
          <div style={{ display: 'flex', gap: '6vw', justifyContent: 'center', flexWrap: 'wrap' }}>
            <Metric label="目前段目標配速" value={curSeg ? fmtPace(curSeg.pace_s) : '--:--'} unit="/km" size="md" />
            <Metric label="預計完成時間" value={etaLabel} unit="" size="md" />
          </div>
        )}

        {strategy && paceAlert && (
          <div style={{
            background: paceAlert === 'fast' ? 'rgba(255,194,75,.16)' : 'rgba(255,75,92,.16)',
            border: `1px solid ${paceAlert === 'fast' ? 'var(--gold)' : 'var(--hunt)'}`,
            borderRadius: 14, padding: '10px 18px', fontSize: 16, fontWeight: 900,
            color: paceAlert === 'fast' ? 'var(--gold)' : 'var(--hunt)',
          }}>
            {paceAlert === 'fast' ? '⚡ 配速過快' : '🐢 配速過慢'}，目標 {fmtPace(targetPaceS)}/km
          </div>
        )}

        {strategy && fuelLine && (
          <div style={{
            fontSize: 15, fontWeight: 800, color: 'var(--gold)',
            background: 'rgba(255,194,75,.12)', border: '1px solid rgba(255,194,75,.4)',
            borderRadius: 12, padding: '8px 16px',
          }}>🍫 {fuelLine}</div>
        )}
        {strategy && hasFuel && due && dueActive && (
          <div
            className="track-blink"
            style={{
              background: 'rgba(255,75,92,.2)', border: '2px solid var(--hunt)', borderRadius: 16,
              padding: '14px 22px', fontSize: 19, fontWeight: 900, color: 'var(--tx)',
            }}
          >
            🍫 請進行補給：{FUEL_KIND_LABEL[fp.kind]}
            {/* 專注模式中無法點擊關閉（整層攔截輸入）：到期 60 秒後自動視為完成、換下一個補給點 */}
            <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--tx-dim)', marginTop: 4 }}>（60 秒後自動跳下一個）</div>
          </div>
        )}

        <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 11, color: 'var(--tx-dim)', fontWeight: 700 }}>
          <span style={{ width: 7, height: 7, borderRadius: '50%', background: hasSignal ? 'var(--fug)' : 'var(--tx-dim)' }} />
          {hasSignal ? 'GPS 訊號中' : 'GPS 訊號弱／無'}
        </div>
      </div>

      {/* 底部鎖頭：長按 1.5 秒離開專注模式回到完整介面（原 FocusLockScreen 的長按環，見檔頭說明） */}
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8 }}>
        <div
          onPointerDown={startHold}
          onPointerUp={stopHold}
          onPointerLeave={stopHold}
          onPointerCancel={stopHold}
          role="button"
          aria-label="長按 1.5 秒解除專注模式"
          style={{ position: 'relative', width: holdRingSize, height: holdRingSize, minWidth: 72, minHeight: 72, display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'pointer', touchAction: 'none' }}
        >
          <svg width={holdRingSize} height={holdRingSize} style={{ position: 'absolute', inset: 0, transform: 'rotate(-90deg)' }}>
            <circle cx={holdRingSize / 2} cy={holdRingSize / 2} r={holdR} fill="none" stroke="rgba(255,255,255,.18)" strokeWidth={holdStroke} />
            <circle
              cx={holdRingSize / 2} cy={holdRingSize / 2} r={holdR} fill="none" stroke="var(--gold)" strokeWidth={holdStroke}
              strokeDasharray={holdC} strokeDashoffset={holdC * (1 - holdProgress)} strokeLinecap="round"
              style={{ transition: holdProgress === 0 ? 'stroke-dashoffset .15s linear' : 'none' }}
            />
          </svg>
          <span style={{ fontSize: 30 }}>🔒</span>
        </div>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)', fontWeight: 700 }}>長按 1.5 秒解除專注模式</div>
      </div>
    </div>
  )
}

function Metric({ label, value, unit, size }: { label: string; value: string; unit: string; size: 'xl' | 'lg' | 'md' }) {
  const fs = size === 'xl' ? 'clamp(40px, 13vw, 76px)' : size === 'lg' ? 'clamp(26px, 8vw, 44px)' : 'clamp(20px, 6vw, 30px)'
  return (
    <div>
      <div style={{ fontSize: 12, color: 'var(--tx-dim)', fontWeight: 700, marginBottom: 2 }}>{label}</div>
      <div style={{ fontSize: fs, fontWeight: 900, fontVariantNumeric: 'tabular-nums', lineHeight: 1.05, color: 'var(--tx)' }}>
        {value}{unit && <span style={{ fontSize: '0.35em', marginLeft: 4, color: 'var(--tx-dim)' }}>{unit}</span>}
      </div>
    </div>
  )
}

// 本次跑步目標進度條（疊層最上面第一個節點，見掛載處）。三種目標型態的左/右標籤、填充比例、目前值
// 全部走 goal（見 lib/runGoal.ts resolveRunGoal）；none（無單一目標）改成「每 1 km 一段」自然歸零的
// 分段進度，讓一般跑步/混合課表也有個持續推進的視覺回饋。
function GoalProgressBar({ goal, distanceM, elapsed }: { goal: RunGoal; distanceM: number; elapsed: number }) {
  let leftLabel: string, rightLabel: string, curLabel: string | null = null, hint: string | null = null
  const ratio = goalProgressRatio(goal, distanceM, elapsed)
  if (goal.type === 'distance') {
    leftLabel = '0 km'
    rightLabel = `${fmtKm(goal.totalM / 1000)} km`
    curLabel = `${fmtKm(distanceM / 1000)} km`
  } else if (goal.type === 'time') {
    leftLabel = '00:00:00'
    rightLabel = fmtTime(goal.totalS) // 沿用檔內既有 fmtTime（floor，非 round）——與「時間」大字同一把尺
    curLabel = fmtTime(elapsed)
  } else {
    leftLabel = '0 km'
    rightLabel = '1 km'
    hint = '每 1 km 一段'
  }
  const pct = Math.min(1, Math.max(0, ratio)) * 100
  const reached = ratio >= 1
  return (
    <div style={{ width: '100%', maxWidth: 520 }}>
      {hint && <div style={{ textAlign: 'right', fontSize: 10.5, color: 'var(--tx-dim)', fontWeight: 700, marginBottom: 2 }}>{hint}</div>}
      {curLabel && (
        <div style={{ textAlign: 'center', fontSize: 13, fontWeight: 800, fontVariantNumeric: 'tabular-nums', marginBottom: 3, color: reached ? 'var(--gold)' : 'var(--fug)' }}>
          {curLabel}
        </div>
      )}
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11, color: 'var(--tx-dim)', fontWeight: 700, marginBottom: 4 }}>
        <span>{leftLabel}</span><span>{rightLabel}</span>
      </div>
      <div style={{ height: 10, borderRadius: 999, background: 'rgba(255,255,255,.18)', overflow: 'hidden' }}>
        <div style={{ height: '100%', width: `${pct}%`, background: reached ? 'var(--gold)' : 'var(--fug)', borderRadius: 999, transition: 'width .4s linear' }} />
      </div>
    </div>
  )
}

// 舊「每公里鼓勵語橫幅」元件已移除（v1.1.664 升級成泡泡對話框+啦啦隊角色演出，改由
// page.tsx 頂層的 track/CheerShow.tsx 獨立負責顯示；專注模式開啟時 CheerShow 提高 z-index 到 3950
// 蓋過本疊層（3900，2026-09-25 review 修正：原本兩者皆與全站 .landscape-lock「請轉回直立」蓋板
// 同為 4000，橫向小尺寸手機 useIsMobile()=true 時 PhoneFrame 不套 .phone-shell、與 body 層級的
// LandscapeNotice 同一個 stacking context，DOM 順序讓本疊層蓋過轉向警告——使用者卡在全黑鎖定層
// 連轉向提示都看不到。改成 3900/3950，維持互相的蓋過關係不變，但兩者都讓出 4000 給轉向警告），
// 仍維持 pointerEvents:none，見 track/page.tsx 呼叫處與 CheerShow.tsx 檔頭說明）。
