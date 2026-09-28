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
import dynamic from 'next/dynamic'
import { FUEL_KIND_LABEL, type RaceStrategy, type StrategySegment } from '@/lib/api'
import { fmtKm, goalProgressRatio, type RunGoal } from '@/lib/runGoal'

// 未來科幻世界（scifi）變體專用（CONTRACT.md §1／§4.4）：大字數字改 Orbitron＋青色光暈，其餘 skin
// 不受影響。2026-09-27 review 修正：本檔是 track/page.tsx 靜態 import、對所有 /track 訪客無條件掛載，
// 若在這裡頂層直接呼叫 next/font/google 的 Orbitron()，會讓它產生的 @font-face CSS chunk 進入 /track
// 首屏 app-build-manifest——非白名單使用者（或白名單但偏好關閉）也會多一份先前不存在的 CSS 請求，
// 牴觸 CONTRACT.md §1「所有 scifi 程式碼在 data-skin="scifi" 或動態 import 之後」的硬性隔離規則
// （原本的實作已用像素/網路請求量測證實：即使字型檔本體因瀏覽器 lazy font loading 不會真的被下載，
// 這個 CSS chunk 本身已經是一個對所有人都改變的可觀測事實）。改法：真正持有 Orbitron() 呼叫的程式碼
// 搬到 track/scifi/font.ts（與 SciFiMap.tsx 共用同一實例），本檔改用 next/dynamic 動態載入
// track/scifi/OrbitronText.tsx，且只在下方 Metric 元件 `scifi` 為真時才把它放進 JSX 樹——非白名單／
// 偏好關閉時這個節點根本不會建立，dynamic import 的 import() 不會觸發，本檔本身不再有任何頂層
// next/font 呼叫，對照 SciFiMap.tsx 的隔離手法一致。
const OrbitronText = dynamic(() => import('./scifi/OrbitronText'), { ssr: false })

const HOLD_MS = 1500 // 底部鎖頭長按離開專注模式所需時長（與舊 FocusLockScreen 解鎖時長一致）

// 復古 RPG（retro）專注模式配色（CONTRACT_R3.md §3「專注模式狀態視窗＝深色皮革＋金框，數字米白」）：
// 本疊層背景在所有 skin 下都維持接近純黑的漸層（上 45% 透地圖、下方漸黑，見 overlayRef 那個
// <div> 的 background），這與 globals.css `[data-skin="retro"]` 把 --tx 系列改成「深褐」給羊皮紙
// 淺底用完全衝突──若這裡繼續讀 var(--tx)/var(--tx-dim)/var(--gold) 等 token，深褐字疊在這層近黑
// 背景上會讀不出來。故本檔案的 retro 分支一律改用下面這組寫死的「深色底可讀」色票，不吃全站
// token 級聯；其餘 skin（scifi/default）完全不受影響，仍讀 var(--tx) 等既有邏輯。
const RETRO_CREAM = '#fff3d6'   // 主要文字／大字數字（米白，契約逐字）
const RETRO_DIM = '#c9a878'     // 次要／說明文字（暖褐，暗底可讀）
const RETRO_GOLD = '#d99a1a'    // 強調金（契約 §3 新色號，取代 v854 #f8b800）
const RETRO_HUNT = '#a8321e'    // 警示磚紅（契約 §3 新色號）
const RETRO_LEATHER = '#2a1a10' // 狀態視窗深色皮革底
const RETRO_LEATHER_LINE = '#d9a441' // 皮革面板細金框
const RETRO_BRONZE = '#c97b2e'  // 進度條「進行中」填色（--fug 深咖啡在近黑底幾乎不可見，改用可視的青銅色）

// 溫馨可愛（cute）專注模式配色 第二輪「草莓牛奶」（docs/skins/CUTE_CONTRACT_R2.md §3
// 「底部漸層從透明到 blush；貼紙卡白底＋lineSoft 框＋粉色柔影＋紙膠帶（改 lavender）；標題膠囊
// 『一起加油！』sakura 底 berry 字；大數字 rose 色；配速等小字 berry／berryDim；鎖頭環 candy」）。
// 與 retro 同一個理由：本疊層背景是自訂的「上 45% 透出地圖、下方轉為粉色調」漸層（不是
// scifi/retro 的近黑），但同樣不吃全站 --tx/--fug 等 token 級聯——這個疊層的視覺是契約明訂的固定配色
// 組合，與 globals.css 那套「隨 skin 換色」的一般頁面 token 屬於不同的設計意圖，直接寫死色票最不
// 容易因為 THEME 工人日後調整全站 token 而跟著跑掉。
// 對比規則（契約 §2）：粉色實心底一律配 berry 深字，不用白字；一般文字 ≥4.5:1、大字（≥24px）
// ≥3:1。CUTE_ROSE 只用在 Metric 的 xl/lg 兩種大字（clamp 最小值都 ≥26px，穩過 24px 門檻），md
// 級數（clamp 最小 20px，小螢幕可能跌破 24px）與其餘所有小字一律用 CUTE_BERRY／CUTE_BERRY_DIM，
// 兩者對白卡/blush 都 ≥9:1，任何字級皆安全。
const CUTE_CARD = '#ffffff'        // 貼紙卡底色（第一輪 #fff8ec 奶油 → 白）
const CUTE_BLUSH = '#ffe3ee'       // 疊層下半部漸層終點（第一輪 #ffe7a3 奶油黃 → blush）
const CUTE_BERRY = '#5b2a3c'       // 主要文字／深色描邊（第一輪 #3d2b2b 深可可墨 → berry）
const CUTE_BERRY_DIM = '#8a4a64'   // 次要／說明文字（第一輪 #7a5c52 → berryDim）
const CUTE_SAKURA = '#ffc4dc'      // 標題膠囊底色
const CUTE_CANDY = '#ff9fc8'       // 鎖頭環／警示強調／訊號點／進行中進度條
const CUTE_ROSE = '#d6457f'        // 大數字主色（僅限 ≥24px，見上方對比規則；第一輪 #ec6fae）
const CUTE_GOLD = '#f6b73c'        // 強調金（進度條已達標／補給提醒，契約未要求換色）
const CUTE_LAVENDER = '#d9c8ff'    // 紙膠帶色（第一輪 #a9dcf5 天空藍 → lavender）
const CUTE_LINE_SOFT = '#f6c6d8'   // 貼紙卡描邊（第一輪 2px 深可可墨線 → 契約「lineSoft 框」變柔）

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
  initialOpen, openSignal, onOpenChange, scifi, retro, cute,
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
  scifi?: boolean // 未來科幻世界（scifi）變體開關（CONTRACT.md §4.4）：只加樣式（背景改半透明露出下方
  // 仍在運作的 SciFiMap、數字改 Orbitron＋青色光暈、鎖頭改霓虹圓環），長按 1.5 秒解除等行為完全不變。
  // 省略/false＝其他 skin，維持 v850 純黑不動。
  retro?: boolean // 復古 RPG（retro）變體開關（CONTRACT.md §5）：背景改上 45% 透出下方仍在運作的
  // RetroMap 與勇者、數字區改黑底白雙框的 RPG 狀態視窗、標題「冒險中／比賽專注模式・名稱」、鎖頭改
  // 像素鎖頭圖示；scifi 與 retro 互斥（由父層依 activeSkin 分別傳入），長按 1.5 秒解除等行為完全不變。
  // 文字顏色／字型走 `[data-skin="retro"]` 的 CSS token 級聯（globals.css，另一工人負責），本檔不用
  // 額外寫死顏色。
  cute?: boolean // 溫馨可愛（cute）變體開關（docs/skins/CUTE_CONTRACT_R2.md §3；第二輪起地圖
  // 上不再有角色造型，改用靈魂光點呈現，見 app/track/cute/ 說明）：背景改上 45% 透出下方仍在
  // 運作的 CuteMap 與光點、數字區改白色貼紙卡（圓角 24px＋lineSoft 軟框＋白邊＋紙膠帶）、標題膠囊
  // 「一起加油！」、鎖頭改原創圓潤鎖頭圖示；與 scifi／retro 三者互斥（由父層依 activeSkin 分別傳
  // 入），長按 1.5 秒解除等行為完全不變。
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
        data-skin={retro ? 'retro' : scifi ? 'scifi' : cute ? 'cute' : 'default'}
        onClick={() => setHidden(false)}
        style={{
          // bottom（2026-09-28 review 抓到）：原本固定 100px，用 Playwright 在 390×844 running 態
          // 實測 getBoundingClientRect 發現這顆浮動鈕的底邊本來就已經蓋到下方「■ 結束並上傳」鈕的
          // 上緣——不是 cute 專屬新 bug：default 按鈕(無邊框)量到重疊 16.25px，cute 按鈕因
          // .skin-btn-end 多了 2px+2px 墨線框讓自身變高（footer 由下往上撐），重疊擴大到
          // 19.5px；scifi 的漸層框技巧同樣是 2px+2px 邊框，footer 高度與 cute 同一量級。
          // 改成 136px（四個 skin 共用同一顆按鈕、同一組 bottom，不特別分 skin）：以 cute 最壞情況
          // 反推——cute 按鈕頂端 y≈724.5、footer 距視窗底≈119.5px 為視窗高度無關的固定值（footer
          // 高度全由 padding/border 這些像素常數決定，不隨 100dvh 撐大），136 可讓本鈕（含 cute
          // 3px 白色貼紙外框 box-shadow 的視覺外緣）與按鈕上緣仍保有 ≥8px 淨空、default/scifi/retro
          // 更寬鬆（footer 較矮或無硬邊光暈）。不動「螢幕常亮」膠囊（page.tsx 同一列左側、
          // paddingRight:140 已避開本鈕，維持不重疊）。
          position: 'fixed', right: 16, bottom: 'calc(136px + env(safe-area-inset-bottom))', zIndex: 600,
          background: retro ? RETRO_LEATHER : cute ? CUTE_CARD : 'rgba(11,14,19,.9)', color: retro ? RETRO_CREAM : cute ? CUTE_BERRY : 'var(--tx)',
          border: scifi ? '1px solid rgba(53,230,255,.6)' : retro ? `1px solid ${RETRO_LEATHER_LINE}` : cute ? `2px solid ${CUTE_BERRY}` : '1px solid rgba(255,194,75,.6)',
          borderRadius: retro ? 6 : 999, padding: '10px 16px', fontSize: 13, fontWeight: 800, cursor: 'pointer',
          boxShadow: scifi ? '0 4px 16px rgba(53,230,255,.25)' : retro ? `inset 0 0 0 1px rgba(217,164,65,.3), 0 4px 12px rgba(0,0,0,.45)` : cute ? `0 0 0 3px #fff, 0 4px 12px rgba(91,42,60,.2)` : '0 4px 16px rgba(0,0,0,.4)', fontFamily: 'inherit',
        }}
      >🏁 {cute ? '一起加油！' : '專注模式'}</button>
    )
  }

  const holdRingSize = 88, holdStroke = 5, holdR = (holdRingSize - holdStroke) / 2, holdC = 2 * Math.PI * holdR

  return (
    <div
      ref={overlayRef}
      data-skin={retro ? 'retro' : scifi ? 'scifi' : cute ? 'cute' : 'default'}
      className="app-min-h"
      style={{
        position: 'fixed', inset: 0, zIndex: 3900,
        // scifi／retro／cute（CONTRACT.md §4／§5；cute 第二輪見 docs/skins/CUTE_CONTRACT_R2.md
        // §3）：由上而下漸層——頂部 45% 較透明（看得到下方仍在運作的 SciFiMap／RetroMap／CuteMap 與
        // 靈魂／勇者／光點），55% 以下轉為該風格的實色，大字數字靠 justifyContent:'flex-end' 整組推到
        // 下半部（見下方三個子區塊改用 gap 佈局，不再 space-between 把進度條釘在最頂端）。retro 用純黑
        // 漸層、cute 第二輪改 blush 粉色漸層（不再是第一輪的奶油色，契約「下方漸層從透明到 blush」——
        // 卡片本身另外用白底＋lineSoft 軟框呈現，見下方數字區容器），其餘 skin 維持 v850 純黑不變。
        background: scifi
          ? 'linear-gradient(to bottom, rgba(2,4,10,.15) 0%, rgba(2,4,10,.35) 45%, rgba(2,4,10,.92) 55%, rgba(2,4,10,.92) 100%)'
          : retro
          ? 'linear-gradient(to bottom, rgba(0,0,0,.12) 0%, rgba(0,0,0,.32) 45%, rgba(0,0,0,.94) 55%, rgba(0,0,0,.94) 100%)'
          : cute
          // 8 位十六進位色（含 alpha）直接複用 CUTE_BLUSH 常數，不必另外拆 rgba 三通道
          // （1a/52/eb ≈ 10%/32%/92% alpha，對應第一輪原本的漸層停駐點）。
          ? `linear-gradient(to bottom, ${CUTE_BLUSH}1a 0%, ${CUTE_BLUSH}52 45%, ${CUTE_BLUSH}eb 55%, ${CUTE_BLUSH}eb 100%)`
          : '#000',
        color: retro ? RETRO_CREAM : cute ? CUTE_BERRY : 'var(--tx)', display: 'flex', flexDirection: 'column', alignItems: 'center',
        justifyContent: (scifi || retro || cute) ? 'flex-end' : 'space-between', gap: (scifi || retro || cute) ? '2.4vh' : undefined,
        padding: '24px 20px calc(20px + env(safe-area-inset-bottom))',
        textAlign: 'center', touchAction: 'none', userSelect: 'none', WebkitUserSelect: 'none',
      }}
      // 整層攔截所有輸入：唯一可操作的是下方鎖頭（其自身 onPointerDown 已 stopPropagation）。
      // touchAction:none 已擋掉捲動/縮放手勢；桌機滑鼠滾輪改由上方 overlayRef 的原生
      // {passive:false} 監聽器攔截（JSX onWheel/onTouchMove 對這兩個事件是 no-op，見上方宣告處說明）。
      onContextMenu={(e) => e.preventDefault()}
    >
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '1.6vh', width: '100%' }}>
        <GoalProgressBar goal={goal} distanceM={distanceM} elapsed={elapsed} retro={retro} cute={cute} />

        {/* retro／cute 標題文案：retro 是緞帶（CONTRACT_R3.md §3），cute 是膠囊「一起加油！」
            （契約 §3「標題膠囊 sakura 底 berry 字」——第一輪這裡是桃紅底白字，不符合新規則
            「粉色實心底一律 berry 深字，不用白字」，第二輪改過來），本疊層背景不吃全站 --tx 系列
            （見上方常數說明），故直接寫死顏色。scifi/default 維持原本純文字＋var(--tx-dim)。 */}
        <div style={retro ? {
          fontSize: 12, letterSpacing: '.15em', fontWeight: 800, color: RETRO_CREAM,
          background: 'linear-gradient(180deg,#6b3a1c,#5a2f16)', padding: '6px 20px',
          borderTop: `1px solid ${RETRO_LEATHER_LINE}`, borderBottom: `1px solid ${RETRO_LEATHER_LINE}`,
          boxShadow: 'inset 0 1px 0 rgba(255,224,160,.18), inset 0 -1px 0 rgba(0,0,0,.35)',
          textShadow: '0 1px 0 rgba(0,0,0,.5)',
        } : cute ? {
          fontSize: 13, letterSpacing: '.05em', fontWeight: 800, color: CUTE_BERRY,
          background: CUTE_SAKURA, padding: '7px 22px', borderRadius: 999,
          border: `2px solid ${CUTE_BERRY}`, boxShadow: '0 0 0 2px #fff, 0 3px 8px rgba(91,42,60,.2)',
        } : { fontSize: 12, letterSpacing: '.15em', color: 'var(--tx-dim)', fontWeight: 700 }}>
          {retro ? (strategy ? `比賽專注模式・${strategy.name}` : '冒險中') : cute ? (strategy ? `一起加油・${strategy.name}` : '一起加油！') : (strategy ? `比賽專注模式 · ${strategy.name}` : '專注模式')}
        </div>
      </div>

      {/* retro 的數字區改成 RPG 狀態視窗（深色皮革＋細金框），cute 改成白色貼紙卡（圓角 24px、
          lineSoft 軟框、白邊、左上角一小段紙膠帶裝飾，契約 §3「貼紙卡白底＋lineSoft 框＋粉色
          柔影」——第一輪是 2px 深可可墨線，第二輪改軟框＋粉色系陰影）；其餘 skin 維持原本無邊框的
          置中欄位。 */}
      <div style={{ position: 'relative', ...(retro ? {
        background: RETRO_LEATHER, borderRadius: 6,
        border: `1px solid ${RETRO_LEATHER_LINE}`,
        boxShadow: 'inset 0 0 0 1px rgba(217,164,65,.28), inset 0 0 14px rgba(0,0,0,.55), 0 6px 18px rgba(0,0,0,.5)',
        padding: '18px 22px', display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '2vh',
        maxWidth: '92vw',
      } : cute ? {
        background: CUTE_CARD, borderRadius: 24, border: `1.5px solid ${CUTE_LINE_SOFT}`,
        boxShadow: '0 0 0 3px #fff, 0 8px 20px rgba(214,69,127,.22)',
        padding: '22px 24px', display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '2vh',
        maxWidth: '92vw',
      } : { display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '2vh' }) }}>
        {/* 紙膠帶裝飾（純 CSS 小斜貼矩形，原創，非任何吉伊卡哇商品排版元素）：只在 cute 顯示。
            第二輪改 lavender 半透明（契約「紙膠帶改 lavender 或 sky」，第一輪是天空藍）。 */}
        {cute && (
          <div aria-hidden style={{
            position: 'absolute', top: -12, left: 28, width: 58, height: 22,
            background: CUTE_LAVENDER, opacity: 0.85, border: `1px solid rgba(91,42,60,.28)`,
            borderRadius: 3, transform: 'rotate(-7deg)', boxShadow: '0 2px 4px rgba(91,42,60,.15)',
          }} />
        )}
        <Metric label="移動距離" value={distKm.toFixed(2)} unit="km" size="xl" scifi={scifi} retro={retro} cute={cute} />
        <Metric label="時間" value={fmtTime(elapsed)} unit="" size="lg" scifi={scifi} retro={retro} cute={cute} />
        <div style={{ display: 'flex', gap: '6vw', justifyContent: 'center', flexWrap: 'wrap' }}>
          <Metric label="平均配速" value={fmtPace(avgPace)} unit="/km" size="md" scifi={scifi} retro={retro} cute={cute} />
          <Metric label="分段即時配速" value={fmtPace(segLivePace)} unit="/km" size="md" scifi={scifi} retro={retro} cute={cute} />
        </div>
        {/* 以下皆為賽事策略專屬區塊：無 strategy（一般跑步/課表/個人任務等）整組不渲染 */}
        {strategy && (
          <div style={{ display: 'flex', gap: '6vw', justifyContent: 'center', flexWrap: 'wrap' }}>
            <Metric label="目前段目標配速" value={curSeg ? fmtPace(curSeg.pace_s) : '--:--'} unit="/km" size="md" scifi={scifi} retro={retro} cute={cute} />
            <Metric label="預計完成時間" value={etaLabel} unit="" size="md" scifi={scifi} retro={retro} cute={cute} />
          </div>
        )}

        {strategy && paceAlert && (
          <div style={{
            background: retro ? (paceAlert === 'fast' ? 'rgba(217,154,26,.18)' : 'rgba(168,50,30,.22)') : cute ? (paceAlert === 'fast' ? 'rgba(246,183,60,.18)' : 'rgba(255,159,200,.16)') : (paceAlert === 'fast' ? 'rgba(255,194,75,.16)' : 'rgba(255,75,92,.16)'),
            border: `1px solid ${retro ? (paceAlert === 'fast' ? RETRO_GOLD : RETRO_HUNT) : cute ? (paceAlert === 'fast' ? CUTE_GOLD : CUTE_CANDY) : (paceAlert === 'fast' ? 'var(--gold)' : 'var(--hunt)')}`,
            borderRadius: retro ? 6 : cute ? 14 : 14, padding: '10px 18px', fontSize: 16, fontWeight: 900,
            color: retro ? (paceAlert === 'fast' ? RETRO_GOLD : RETRO_HUNT) : cute ? CUTE_BERRY : (paceAlert === 'fast' ? 'var(--gold)' : 'var(--hunt)'),
          }}>
            {paceAlert === 'fast' ? '⚡ 配速過快' : '🐢 配速過慢'}，目標 {fmtPace(targetPaceS)}/km
          </div>
        )}

        {strategy && fuelLine && (
          <div style={{
            fontSize: 15, fontWeight: 800, color: retro ? RETRO_GOLD : cute ? CUTE_BERRY : 'var(--gold)',
            background: retro ? 'rgba(217,154,26,.14)' : cute ? 'rgba(246,183,60,.18)' : 'rgba(255,194,75,.12)', border: `1px solid ${retro ? 'rgba(217,154,26,.4)' : cute ? CUTE_GOLD : 'rgba(255,194,75,.4)'}`,
            borderRadius: retro ? 6 : 12, padding: '8px 16px',
          }}>🍫 {fuelLine}</div>
        )}
        {strategy && hasFuel && due && dueActive && (
          <div
            className="track-blink"
            style={{
              background: retro ? 'rgba(168,50,30,.28)' : cute ? 'rgba(255,159,200,.2)' : 'rgba(255,75,92,.2)', border: `2px solid ${retro ? RETRO_HUNT : cute ? CUTE_CANDY : 'var(--hunt)'}`, borderRadius: retro ? 8 : 16,
              padding: '14px 22px', fontSize: 19, fontWeight: 900, color: retro ? RETRO_CREAM : cute ? CUTE_BERRY : 'var(--tx)',
            }}
          >
            🍫 請進行補給：{FUEL_KIND_LABEL[fp.kind]}
            {/* 專注模式中無法點擊關閉（整層攔截輸入）：到期 60 秒後自動視為完成、換下一個補給點 */}
            <div style={{ fontSize: 11, fontWeight: 600, color: retro ? RETRO_DIM : cute ? CUTE_BERRY_DIM : 'var(--tx-dim)', marginTop: 4 }}>（60 秒後自動跳下一個）</div>
          </div>
        )}

        <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 11, color: retro ? RETRO_DIM : cute ? CUTE_BERRY_DIM : 'var(--tx-dim)', fontWeight: 700 }}>
          <span style={{ width: 7, height: 7, borderRadius: '50%', background: retro ? (hasSignal ? RETRO_GOLD : RETRO_DIM) : cute ? (hasSignal ? CUTE_CANDY : CUTE_BERRY_DIM) : (hasSignal ? 'var(--fug)' : 'var(--tx-dim)') }} />
          {hasSignal ? 'GPS 訊號中' : 'GPS 訊號弱／無'}
        </div>
      </div>

      {/* 底部鎖頭：長按 1.5 秒離開專注模式回到完整介面（原 FocusLockScreen 的長按環，見檔頭說明）。
          retro 用像素風鎖頭圖示（CONTRACT.md §5「鎖頭改像素鎖頭圖示」）、cute 用原創圓潤鎖頭圖示
          （契約「鎖頭環 candy」）取代 emoji 🔒，其餘行為不變。 */}
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
            <circle cx={holdRingSize / 2} cy={holdRingSize / 2} r={holdR} fill="none" stroke={cute ? 'rgba(91,42,60,.18)' : 'rgba(255,255,255,.18)'} strokeWidth={holdStroke} />
            <circle
              cx={holdRingSize / 2} cy={holdRingSize / 2} r={holdR} fill="none" stroke={retro ? RETRO_GOLD : scifi ? 'var(--fug)' : cute ? CUTE_CANDY : 'var(--gold)'} strokeWidth={holdStroke}
              strokeDasharray={holdC} strokeDashoffset={holdC * (1 - holdProgress)} strokeLinecap="round"
              style={{ transition: holdProgress === 0 ? 'stroke-dashoffset .15s linear' : 'none', filter: scifi ? 'drop-shadow(0 0 6px rgba(53,230,255,.7))' : undefined }}
            />
          </svg>
          {retro ? <PixelLock /> : cute ? <CuteLock /> : <span style={{ fontSize: 30 }}>🔒</span>}
        </div>
        <div style={{ fontSize: 12, color: retro ? RETRO_DIM : cute ? CUTE_BERRY_DIM : 'var(--tx-dim)', fontWeight: 700 }}>長按 1.5 秒解除專注模式</div>
      </div>
    </div>
  )
}

// 像素風鎖頭圖示（retro 變體，CONTRACT.md §5）：原創、逐像素以 SVG rect 繪製的極簡鎖頭，不使用 emoji
// 字型，維持像素塊的觀感，色彩取自復古 RPG 調色盤（CONTRACT_R3 新金色 #d99a1a／白／黑）。
function PixelLock() {
  const s = 3 // 每個邏輯像素放大成多少 px
  return (
    <svg width={12 * s} height={12 * s} viewBox={`0 0 ${12 * s} ${12 * s}`} shapeRendering="crispEdges">
      <rect x={3 * s} y={5 * s} width={6 * s} height={6 * s} fill={RETRO_GOLD} />
      <rect x={4 * s} y={2 * s} width={4 * s} height={3 * s} fill="none" stroke="#fff" strokeWidth={s} />
      <rect x={5 * s} y={7 * s} width={2 * s} height={2 * s} fill="#000" />
    </svg>
  )
}

// 原創圓潤鎖頭圖示（cute 變體）：與 PixelLock 同一個理由（不使用 emoji 字型），但改走「手繪可愛」
// 的圓角語彙——鎖環用圓角描邊弧線、鎖身用圓角矩形＋berry 描邊＋白邊，配色取自第二輪「草莓牛奶」
// 色盤（candy／berry／白，契約「鎖頭環 candy」——第一輪是桃紅＋深可可墨），與吉伊卡哇無任何造型
// 關聯（純幾何鎖頭圖示）。
function CuteLock() {
  return (
    <svg width={40} height={40} viewBox="0 0 40 40">
      <rect x={10} y={18} width={20} height={16} rx={5} fill={CUTE_CANDY} stroke={CUTE_BERRY} strokeWidth={2} />
      <path d="M14 18 V13 a6 6 0 0 1 12 0 V18" fill="none" stroke={CUTE_BERRY} strokeWidth={2.4} strokeLinecap="round" />
      <circle cx={20} cy={25} r={2.6} fill="#fff" stroke={CUTE_BERRY} strokeWidth={1.2} />
      <rect x={19} y={26.5} width={2} height={4} rx={1} fill={CUTE_BERRY} />
    </svg>
  )
}

function Metric({ label, value, unit, size, scifi, retro, cute }: { label: string; value: string; unit: string; size: 'xl' | 'lg' | 'md'; scifi?: boolean; retro?: boolean; cute?: boolean }) {
  const fs = size === 'xl' ? 'clamp(40px, 13vw, 76px)' : size === 'lg' ? 'clamp(26px, 8vw, 44px)' : 'clamp(20px, 6vw, 30px)'
  // cute 大字數字用 rose（契約「大數字 rose 色」，僅限 ≥24px 大字對比門檻 ≥3:1，見檔頭常數
  // 說明）：xl/lg 兩種字級的 clamp 最小值都 ≥26px，穩過門檻；md 字級 clamp 最小 20px，小螢幕
  // 可能跌破 24px，改用永遠安全的 berry（對白卡/blush ≥9:1，不受字級大小限制）。
  const cuteBigNum = size !== 'md'
  const valueStyle = {
    fontSize: fs, fontWeight: 900, fontVariantNumeric: 'tabular-nums' as const, lineHeight: 1.05,
    color: scifi ? 'var(--fug)' : retro ? RETRO_CREAM : cute ? (cuteBigNum ? CUTE_ROSE : CUTE_BERRY) : 'var(--tx)',
    // retro／cute 大字數字：retro 用金黃描邊字（CONTRACT_R3.md §5），cute 的 xl/lg 用「rose＋berry
    // 描邊」（rose 本色＋berry text-shadow 描邊，呼應契約「兩色組合」的描述，第一輪是桃紅＋深可可）；
    // md 字級本身已是 berry 實色，同色描邊沒有意義，略過。其餘沿用原本無陰影。
    textShadow: scifi ? '0 0 12px rgba(53,230,255,.55)' : retro ? '0 1px 0 rgba(0,0,0,.6), 0 -1px 0 rgba(0,0,0,.3), 1px 0 0 rgba(0,0,0,.3), -1px 0 0 rgba(0,0,0,.3)' : (cute && cuteBigNum) ? `0 1.5px 0 ${CUTE_BERRY}` : undefined,
  }
  const valueNode = (
    <>{value}{unit && <span style={{ fontSize: '0.35em', marginLeft: 4, color: retro ? RETRO_DIM : cute ? CUTE_BERRY_DIM : 'var(--tx-dim)' }}>{unit}</span>}</>
  )
  return (
    <div>
      <div style={{ fontSize: 12, color: retro ? RETRO_DIM : cute ? CUTE_BERRY_DIM : 'var(--tx-dim)', fontWeight: 700, marginBottom: 2 }}>{label}</div>
      {/* scifi 為真時才建立 OrbitronText 節點（見上方 import 處說明）——非白名單／偏好關閉時走一般
          <div>，本檔完全不觸發 next/font 的動態 import。 */}
      {scifi ? <OrbitronText style={valueStyle}>{valueNode}</OrbitronText> : <div style={valueStyle}>{valueNode}</div>}
    </div>
  )
}

// 本次跑步目標進度條（疊層最上面第一個節點，見掛載處）。三種目標型態的左/右標籤、填充比例、目前值
// 全部走 goal（見 lib/runGoal.ts resolveRunGoal）；none（無單一目標）改成「每 1 km 一段」自然歸零的
// 分段進度，讓一般跑步/混合課表也有個持續推進的視覺回饋。
// retro：本疊層背景近黑，不能吃全站 --tx/--fug 系列（羊皮紙下已改深褐，見檔頭常數說明），
// 進行中填色改用可視的青銅色 RETRO_BRONZE、已達標改 RETRO_GOLD。
function GoalProgressBar({ goal, distanceM, elapsed, retro, cute }: { goal: RunGoal; distanceM: number; elapsed: number; retro?: boolean; cute?: boolean }) {
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
  const fillColor = retro ? (reached ? RETRO_GOLD : RETRO_BRONZE) : cute ? (reached ? CUTE_GOLD : CUTE_CANDY) : (reached ? 'var(--gold)' : 'var(--fug)')
  return (
    <div style={{
      width: '100%', maxWidth: 520,
      // cute 專屬小貼紙底卡（2026-09-28 review 抓到；第二輪再調色）：本疊層最上緣露出的地圖只透
      // 10~32% 底色（見上方 overlayRef 那個 <div> 的 background 漸層說明），這排「0 km / 1 km /
      // 第 1 km 一段」字直接疊在還在動的地圖上幾乎讀不出來。比照契約「貼紙卡白底＋lineSoft 框」
      // 同一套語彙縮小版包住整組（labels＋進度條），不動 retro/scifi/default（維持無底色、只吃
      // 漸層透出）。
      ...(cute ? {
        background: CUTE_CARD, border: `1.5px solid ${CUTE_LINE_SOFT}`, borderRadius: 14,
        boxShadow: '0 0 0 3px #fff, 0 4px 10px rgba(214,69,127,.18)',
        padding: '8px 12px', boxSizing: 'border-box' as const,
      } : {}),
    }}>
      {hint && <div style={{ textAlign: 'right', fontSize: 10.5, color: retro ? RETRO_DIM : cute ? CUTE_BERRY_DIM : 'var(--tx-dim)', fontWeight: 700, marginBottom: 2 }}>{hint}</div>}
      {curLabel && (
        // cute 對比修正（第二輪自我審查抓到）：fillColor 在 cute 下是 candy／gold，兩者都是淺色，
        // 當「文字色」直接疊在白色貼紙卡上實測只有 ≈1.8–1.9:1，遠低於 WCAG AA 4.5:1（這顆數字
        // 13px 粗體不算大字，門檻仍是 4.5:1）——這其實是第一輪就存在的既有缺陷（第一輪 CUTE_PINK
        // #ec6fae 同樣只有 ≈2.8:1），第二輪順手修正：fillColor 只用在下面的進度條「填色」（一段
        // 色塊，非文字），這裡文字改固定用 berry（對白卡 ≥11:1，不受達標/進行中兩種狀態影響）。
        <div style={{ textAlign: 'center', fontSize: 13, fontWeight: 800, fontVariantNumeric: 'tabular-nums', marginBottom: 3, color: cute ? CUTE_BERRY : fillColor }}>
          {curLabel}
        </div>
      )}
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11, color: retro ? RETRO_DIM : cute ? CUTE_BERRY_DIM : 'var(--tx-dim)', fontWeight: 700, marginBottom: 4 }}>
        <span>{leftLabel}</span><span>{rightLabel}</span>
      </div>
      <div style={{ height: 10, borderRadius: 999, background: retro ? 'rgba(0,0,0,.45)' : cute ? 'rgba(255,255,255,.55)' : 'rgba(255,255,255,.18)', overflow: 'hidden', border: retro ? `1px solid ${RETRO_LEATHER_LINE}` : cute ? `1.5px solid ${CUTE_LINE_SOFT}` : undefined }}>
        <div style={{ height: '100%', width: `${pct}%`, background: fillColor, borderRadius: 999, transition: 'width .4s linear' }} />
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
