'use client'

// 復古 RPG（retro）GPS 地圖主元件（CONTRACT.md §4）。
// 只在 track/page.tsx 判斷 `document.documentElement.dataset.skin === 'retro'` 為真時，以
// next/dynamic(ssr:false) 動態載入本檔——本檔（含 maplibre-gl 與其 CSS）因此只會進入一個獨立的動態
// chunk，非白名單使用者的 /track 首屏 bundle 完全不含這裡的程式碼（比照 track/scifi/SciFiMap.tsx）。
//
// 隔離原則（CONTRACT.md §1）：既有 Leaflet 地圖照舊建立與運作（本檔完全不碰 track/page.tsx 的
// Leaflet refs／狀態），本元件只是疊在同一位置的另一層視覺呈現；任何初始化失敗（WebGL 不支援、8 秒
// 未 load、context lost、渲染例外）一律呼叫 onFallback() 通知父層卸載本元件、恢復顯示 Leaflet——
// 跑步紀錄（GPS 取點/距離/上傳）完全不依賴本檔是否成功渲染。
//
// 與 SciFiMap.tsx 的差異：pitch/bearing 恆為 0（經典俯視，鏡頭不隨行進方向旋轉），改用低 pixelRatio
// ＋canvas image-rendering:pixelated 做出「地圖本體」的像素化效果；docs/skins/RETRO_CONTRACT_R4.md
// §3 起，跑者不再是四方向小人，改成 ./orb.ts 的 MagicOrb（逐像素繪製的魔法光點，不留腳印），已跑
// 路線畫成「像素金色光路」（黑色外框＋金黃主線＋白色光粒，見下方 drawRoute）。

import { forwardRef, useEffect, useImperativeHandle, useRef } from 'react'
import { Map as MapLibreMap, config as maplibreConfig, type MapOptions, type LngLatLike, type MapMouseEvent, type MapMovementEvent } from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { buildRetroStyle } from './style'
import { tileImageData, drawKmFlag, drawTargetIcon } from './sprites'
import { MagicOrb } from './orb'
import type { TileKind } from '@/components/retro/tiles'
import { isWebglSupported, haversineM, metersPerPixel, geoCircle, decimate } from './geo'
import type { RetroMapHandle, RetroMapProps, RetroPos, RetroTarget } from './types'

// ORBPOS_CONTRACT.md：`pos` 新增可選欄位 `ts?: number`（PAGE 工人在 track/page.tsx／
// scifi/types.ts 加，本檔只讀）。這裡用交集型別本機加這個欄位，不直接改 scifi/types.ts 或依賴它
// 何時被改到——不論 PAGE 工人的修改是否已落地，本檔的型別都自洽（PAGE 工人補上同名欄位後，這個
// 交集型別只是變成多餘但無害；順序不影響本檔能不能過 tsc）。缺席時退回「這個元件自己偵測到
// pos.lat/lng 最後改變的時間」（見下方 lastFixReceivedAtRef／fixAgeMs）。
type RetroPosWithTs = RetroPos & { ts?: number }

// maplibre-gl worker 自架修正：與 scifi/SciFiMap.tsx 同一個根因/同一套修法（見該檔詳細註解）——
// maplibre-gl 6.x 對 import.meta.url 的靜態改寫在這個第三方預先打包檔上不會發生，改用官方支援的
// config.WORKER_URL 指到 public/ 內同版本的 worker 靜態檔（same-origin，符合 CSP worker-src）。
// 與 SciFiMap.tsx 共寫同一個 module-level flag（`maplibreConfig.WORKER_URL`）是同一個 maplibre-gl
// 套件實例的同一份設定，兩邊誰先執行都會設好、不會互相覆蓋成不同值，因此這裡不需要重複 import 對方
// 檔案，只要照抄同一個常數字串即可。
const MAPLIBRE_WORKER_URL = '/vendor/maplibre-gl-6.11.2/maplibre-gl-worker.mjs'
if (typeof window !== 'undefined' && !maplibreConfig.WORKER_URL) {
  maplibreConfig.WORKER_URL = MAPLIBRE_WORKER_URL
}

const FALLBACK_TIMEOUT_MS = 8000
const GOLD = '#f8b800'
const LAST_POS_KEY = 'dor_retro_last_pos' // 與 track/page.tsx 的 RETRO_LAST_POS_KEY 同一把 key
const RESUME_FOLLOW_MS = 8000
// 移動判定（供光點噴火花用）——比照 track/cute/CuteMap.tsx FIX round2 的根因修正：不用「上一點到
// 這一點」的瞬時速度（GPS 靜止時仍會持續回報幾公尺內的抖動座標，瞬時速度常態性超過門檻，導致
// 「停止移動後 particles 回不到 0」）。改成 moveAnchorRef 記錄「上一個真實移動基準點」，位移 ≥
// MOVE_NOISE_FLOOR_M 才算真實移動並推進 lastMovingAtRef；renderFrame 每幀用當下 performance.now()
// 重新判斷「距上次真實移動是否已超過 STILL_TIMEOUT_MS」，時間流逝本身就會讓「移動中」自然衰減回
// 靜止（docs/skins/RETRO_CONTRACT_R4.md §3「靜止 3 秒後歸零」）。
const MOVE_NOISE_FLOOR_M = 6
const STILL_TIMEOUT_MS = 3000
// 軌跡末端連到光點目前位置的精度門檻（docs/skins/RETRO_CONTRACT_R4.md §4「不可用未過濾的即時 GPS
// 畫出假線」）：沿用 track/page.tsx MAX_ACC=65m 同一慣例，理由與數值比照 cute/CuteMap.tsx
// TRAIL_CONNECT_MAX_ACC。
const TRAIL_CONNECT_MAX_ACC = 65
// FIX（review 抓到的 major 根因，docs/skins/ORBPOS_CONTRACT.md 驗收項目 4「回到目前位置→zoom=16.5、
// 之後 3 次定位更新都維持跟隨」）：recenter() 的 zoom:16.5 easeTo 若在動畫尚未跑完（500ms）前被下面
// 「GPS 位置更新」effect 的跟隨 easeTo 打斷，會被 MapLibre 永久凍結在打斷當下的中間值，且再也不會自己
// 恢復——根因見 node_modules/maplibre-gl/src/geo/projection/mercator_camera_helper.ts handleEaseTo()：
// 新一次 easeTo 的 startZoom 讀的是 tr.zoom，這是「目前正在跑的動畫已經套用到 map 實際 transform 上」
// 的即時值；跟隨 effect 的 easeTo 依 ORBPOS_CONTRACT.md §A（保留使用者縮放）刻意不帶 zoom，一旦在這
// 種情況下打斷 recenter，endZoom=zoom=startZoom、isZooming 判定為 false，新動畫完全不會再移動
// zoom，就此卡在那個中間值。recenterZoomPendingRef（見下方使用處）讓「recenter() 剛按下、zoom 還沒
// 真的收斂到 16.5」這段期間，跟隨 effect 的 easeTo 繼續一起帶 zoom:16.5——即使又被下一次跟隨更新
// 打斷，新動畫的終點依然是 16.5，只是要多花一點時間收斂，不會卡在半路；量到目前 zoom 已經夠接近
// 16.5 就視為收斂完成，恢復「跟隨只搬 center」的原設計。
const RECENTER_ZOOM = 16.5
const RECENTER_ZOOM_EPS = 0.05
// ORBPOS_CONTRACT.md §B「定位中」外觀判定：acc 超過這個門檻（沿用 page.tsx 同名 MAX_ACC 慣例、
// 與上面 TRAIL_CONNECT_MAX_ACC 同一個數字，理由相同）或距上次收到定位已超過 15 秒，光點改畫
// 「定位中」外觀（本體半透明＋像素點狀精度圈）。RING_MAX_PX＝精度圈半徑上限（契約逐字「上限 120
// px」），避免 acc 很差（數百公尺）時圈大到佔滿整個畫面。
const SEARCHING_MAX_ACC = 65
const SEARCHING_STALE_MS = 15000
const SEARCH_RING_MAX_PX = 120

function writeLastPos(lat: number, lng: number) {
  try { localStorage.setItem(LAST_POS_KEY, JSON.stringify({ lat, lng })) } catch { /* 私密瀏覽/storage 被封鎖：純錦上添花，略過即可 */ }
}

const RetroMap = forwardRef<RetroMapHandle, RetroMapProps>(function RetroMap(props, ref) {
  const { pos, status, segments, kmMarks, targets, focusMode, initialCenter, initialZoom, onFallback, onTargetClick, bottomInset, onFollowChange } = props

  const wrapRef = useRef<HTMLDivElement | null>(null)
  const containerRef = useRef<HTMLDivElement | null>(null)
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const mapRef = useRef<MapLibreMap | null>(null)
  const rafRef = useRef<number | null>(null)
  const detachListenersRef = useRef<() => void>(() => {})
  const failedRef = useRef(false)
  const loadedRef = useRef(false)
  const followingRef = useRef(true)
  const recenterZoomPendingRef = useRef(false) // 見上方 RECENTER_ZOOM 宣告處：recenter() 剛按下、zoom 還沒收斂到 16.5 期間為 true
  const orbRef = useRef<MagicOrb | null>(null)
  const lastPosRef = useRef<{ lat: number; lng: number; t: number } | null>(null)
  // 見上方 MOVE_NOISE_FLOOR_M／STILL_TIMEOUT_MS 宣告處的根因說明。
  const moveAnchorRef = useRef<{ lat: number; lng: number } | null>(null)
  const lastMovingAtRef = useRef(0) // performance.now()：最後一次判定為「真實移動」的時間戳，0＝從未偵測到移動
  // ORBPOS_CONTRACT.md §B：pos.ts（PAGE 工人加的欄位）缺席時的 fallback——「這個元件自己偵測到
  // pos.lat/lng 最後改變」的時間，performance.now() 定義域，見下方 GPS 位置更新 effect 與 fixAgeMs()。
  const lastFixReceivedAtRef = useRef(0)
  const searchPulseRef = useRef(0) // 「定位中」精度圈緩慢脈動的相位時脈；reduced-motion 時不推進（畫面靜止）
  const orbStateRef = useRef<'normal' | 'searching'>('normal') // 供 __retroDebug 讀，renderFrame 每幀更新
  const lastFixAgeMsRef = useRef(0) // 同上，供 __retroDebug.lastFixAgeMs
  const waterFrameRef = useRef<0 | 1>(0)
  const waterTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const lastFrameTRef = useRef(0)
  const reducedMotionRef = useRef(false)
  const initialCenterRef = useRef(initialCenter)
  const resumeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const tilesRequestedRef = useRef(0)
  const tilesLoadedRef = useRef(0)
  const fpsRef = useRef(0)
  const frameCountRef = useRef(0)
  const fpsWindowStartRef = useRef(0)
  // docs/skins/RETRO_CONTRACT_R4.md §6「setOverlayHidden(bool)：暫時不畫光點／軌跡／徽章」，只給
  // E2E 做「有畫 vs 沒畫」前後對照截圖用（比照 cute/CuteMap.tsx 同名欄位），預設 false。
  const overlayHiddenRef = useRef(false)
  const prevStatusRef = useRef<string | undefined>(undefined) // 供下方判斷「是否剛從非 tracking 轉進 tracking」（新一輪開跑，重置 overlayHiddenRef）

  const posRef = useRef(pos); posRef.current = pos
  const statusRef = useRef(status); statusRef.current = status
  const segmentsRef = useRef(segments); segmentsRef.current = segments
  const kmMarksRef = useRef(kmMarks); kmMarksRef.current = kmMarks
  const targetsRef = useRef(targets); targetsRef.current = targets
  const focusModeRef = useRef(focusMode); focusModeRef.current = focusMode
  const onFallbackRef = useRef(onFallback); onFallbackRef.current = onFallback
  const onTargetClickRef = useRef(onTargetClick); onTargetClickRef.current = onTargetClick
  // docs/skins/ORBPOS_CONTRACT.md §S2：「回到目前位置」按鈕在三個 skin 都叫不出來——page.tsx 讀不到
  // skin 地圖被使用者手勢拖動/縮放後的跟隨狀態。新增這個回呼，用 ref 存最新的函式（比照上面
  // onFallbackRef／onTargetClickRef 同一慣例），下方 setFollowing() 只在真的翻轉時才呼叫一次。
  const onFollowChangeRef = useRef(onFollowChange); onFollowChangeRef.current = onFollowChange
  const bottomInsetRef = useRef(bottomInset ?? 0); bottomInsetRef.current = bottomInset ?? 0
  const appliedPaddingBottomRef = useRef<number | null>(null) // 上次實際 setPadding 的值，避免每次 render 都重呼叫
  initialCenterRef.current = initialCenter

  const fail = (reason: string) => {
    if (failedRef.current) return
    failedRef.current = true
    // eslint-disable-next-line no-console
    console.warn('[retro-map] fallback', reason)
    if (rafRef.current != null) { cancelAnimationFrame(rafRef.current); rafRef.current = null }
    try { delete (window as unknown as { __retroDebug?: unknown }).__retroDebug } catch { /* ignore */ }
    onFallbackRef.current(reason)
  }

  // CONTRACT_R2 §2：讓「跟隨鏡頭」的中心落在面板以上的可見地圖區正中央（而非整個容器含被面板
  // 蓋住那一半的正中央），呼叫 map.setPadding({bottom}) 即可——padding 會直接影響 map 的
  // transform，之後 easeTo({center}) 與既有的 map.project() 畫光點都自動反映新的可見區中心，
  // 不需要另外調整光點的畫法。專注模式開啟時可見區＝容器高度的上 45%（RaceFocusMode retro
  // 漸層透出區，見該檔），改用容器高度換算、忽略 bottomInset（此時面板本身已整層攔截觸控，
  // bottomInset 量到的面板高度沒有意義）。同一個值沒變就不重呼叫（setPadding 本身會觸發一次
  // repaint，250ms 節流的 tick 若每次都呼叫會浪費）。
  const applyRetroPadding = () => {
    const map = mapRef.current
    if (!map) return
    const container = containerRef.current
    const h = container?.clientHeight || 0
    const bottom = Math.max(0, Math.round(focusModeRef.current ? h * 0.55 : bottomInsetRef.current))
    if (appliedPaddingBottomRef.current === bottom) return
    appliedPaddingBottomRef.current = bottom
    try { map.setPadding({ top: 0, bottom, left: 0, right: 0 }) } catch { /* ignore */ }
  }

  // bottomInset／focusMode 任一改變都重算一次（track/page.tsx 已對 bottomInset 做 250ms 節流，
  // 這裡不用再自己節流；focusMode 切換是即時的使用者操作，本就該立刻反應）。
  useEffect(() => { applyRetroPadding() }) // eslint-disable-line react-hooks/exhaustive-deps -- 每次 render 都檢查一次即可，內部已用 appliedPaddingBottomRef 擋掉沒變化時的重呼叫

  // 統一改變 followingRef 的唯一入口（見上方 onFollowChangeRef 宣告處）：只在狀態真的翻轉
  // （true↔false）時才呼叫 onFollowChange，避免呼叫端（8 秒自動恢復計時器、recenter()、使用者手勢
  // pauseFollow、+/− 按鈕）各自重複觸發 page.tsx 不必要的 re-render。
  function setFollowing(v: boolean) {
    if (followingRef.current === v) return
    followingRef.current = v
    onFollowChangeRef.current?.(v)
  }

  // ORBPOS_CONTRACT.md §A：暫停跟隨鏡頭 8 秒。移到元件頂層（不再是建圖 useEffect 內的區域函式），
  // 讓下面 useImperativeHandle 的 zoomBy（+/− 按鈕，契約「屬使用者操作，照舊暫停跟隨 8 秒」）也能
  // 直接呼叫——按鈕本身不是地圖上的手勢，觸發的 map.easeTo() 不會帶 originalEvent，不能靠下面建圖
  // effect 裡 onZoomStart 的「只有使用者手勢才暫停」判斷順便觸發，必須這裡主動呼叫一次。只讀寫
  // followingRef／resumeTimerRef 兩個穩定 ref，不依賴 map 實例，元件生命週期內可以安全跨 render
  // 重新宣告（跟 recenter/zoomBy 本身一樣，每次 render 產生新的函式物件但邏輯等價）。
  function pauseFollow() {
    setFollowing(false)
    if (resumeTimerRef.current) clearTimeout(resumeTimerRef.current)
    resumeTimerRef.current = setTimeout(() => { setFollowing(true); resumeTimerRef.current = null }, RESUME_FOLLOW_MS)
  }

  // ORBPOS_CONTRACT.md §B：這筆定位是多久以前收到的。優先用 PAGE 工人加在 pos 上的 `ts`
  // （epoch ms，每次收到新定位就填一次）；缺席（欄位不存在／值不是正數，例如 PAGE 工人的修改還沒
  // 落地、或呼叫端本來就沒有這個欄位）時退回 lastFixReceivedAtRef（見上方宣告處）。
  function fixAgeMs(p: RetroPosWithTs | null): number {
    if (!p) return Infinity
    if (typeof p.ts === 'number' && p.ts > 0) return Date.now() - p.ts
    if (!lastFixReceivedAtRef.current) return 0
    return performance.now() - lastFixReceivedAtRef.current
  }

  useImperativeHandle(ref, () => ({
    recenter(p) {
      const map = mapRef.current
      if (!map) return
      setFollowing(true)
      if (resumeTimerRef.current) { clearTimeout(resumeTimerRef.current); resumeTimerRef.current = null }
      const target = p || posRef.current
      // ORBPOS_CONTRACT.md §A「回到目前位置」：縮放回到 retro 預設 16.5、恢復跟隨；這裡的 easeTo
      // 會觸發 zoomstart，但下面建圖 effect 的 onZoomStart 已改成只在事件帶 originalEvent（使用者
      // 手勢）才 pauseFollow()——這個 easeTo 是程式呼叫、沒有 originalEvent，不會把上面剛設的
      // followingRef=true 又立刻蓋掉暫停，不需要另外做旗標防重入。
      if (target) {
        recenterZoomPendingRef.current = true // 見上方 RECENTER_ZOOM 宣告處：標記「zoom 還沒收斂」，讓跟隨 effect 之後一起帶 zoom
        try { map.easeTo({ center: [target.lng, target.lat], zoom: RECENTER_ZOOM, pitch: 0, bearing: 0, duration: 500, essential: true }) } catch { /* ignore */ }
      }
    },
    zoomBy(delta: number) {
      const map = mapRef.current
      if (!map) return
      // ORBPOS_CONTRACT.md §A「+/− 按鈕屬使用者操作，照舊暫停跟隨 8 秒」：按鈕觸發的 easeTo 不是
      // 地圖手勢，不會帶 originalEvent，靠下面 onZoomStart 的手勢判斷等不到；這裡主動呼叫一次。
      recenterZoomPendingRef.current = false // 使用者主動縮放：不再幫他把 zoom 拉回 16.5，尊重這次操作（見上方 RECENTER_ZOOM 說明）
      pauseFollow()
      try { map.easeTo({ zoom: map.getZoom() + delta, duration: 250, essential: true }) } catch { /* ignore */ }
    },
  }), [])

  // ── 建圖（僅掛載時一次）：任何例外／不支援／逾時／context lost 一律退回 Leaflet ──────────────────
  // 實例隔離／清理順序，比照 scifi/SciFiMap.tsx 同一段的詳細註解（isCurrent()／failInstance() 兩層
  // 防護、cleanup 先 detachListeners() 再 map.remove()）。
  useEffect(() => {
    if (!containerRef.current) return
    if (!isWebglSupported()) { fail('webgl-unsupported'); return }
    let cancelled = false
    let timeoutId: ReturnType<typeof setTimeout> | null = null
    let ro: ResizeObserver | null = null
    let localFailed = false
    let map: MapLibreMap | null = null

    const isCurrent = () => !cancelled && mapRef.current === map

    const failInstance = (reason: string) => {
      if (localFailed || failedRef.current) return
      if (!isCurrent()) return
      localFailed = true
      failedRef.current = true
      // eslint-disable-next-line no-console
      console.warn('[retro-map] fallback', reason)
      if (rafRef.current != null) { cancelAnimationFrame(rafRef.current); rafRef.current = null }
      if (waterTimerRef.current) { clearInterval(waterTimerRef.current); waterTimerRef.current = null }
      detachListeners()
      try { map?.remove() } catch { /* ignore */ }
      if (mapRef.current === map) mapRef.current = null
      try { delete (window as unknown as { __retroDebug?: unknown }).__retroDebug } catch { /* ignore */ }
      onFallbackRef.current(reason)
    }

    const onLoad = () => {
      if (!isCurrent()) return
      loadedRef.current = true
      try { map!.getCanvas().style.imageRendering = 'pixelated' } catch { /* ignore */ }
      try { containerRef.current?.querySelector('.maplibregl-ctrl-attrib')?.classList.remove('maplibregl-compact-show') } catch { /* ignore */ }
      applyRetroPadding() // CONTRACT_R2 §2：map 剛就緒就套用一次，不必等下一次 React re-render
    }
    const onError = (e: unknown) => {
      if (!isCurrent()) return
      failInstance('map-error:' + ((e as { error?: { message?: string } })?.error?.message || 'unknown'))
    }
    // ORBPOS_CONTRACT.md §A（已證實缺陷 1 的根因修正）：只有使用者手勢（事件帶 originalEvent，
    // 即滑鼠/觸控/滾輪實際操作地圖）才暫停跟隨；`recenter()`／GPS 跟隨更新／`zoomBy()` 自己呼叫的
    // `map.easeTo()` 都是程式觸發，MapLibre 對這類程式化相機移動一律不帶 originalEvent，用這個
    // 判斷就能把「跟隨鏡頭被自己的程式縮放打斷」的根因徹底排除，不需要額外的「正在跟隨中」旗標
    // 防重入。
    const onDragStart = (e: MapMovementEvent) => { if (isCurrent() && e.originalEvent) pauseFollow() }
    const onZoomStart = (e: MapMovementEvent) => { if (isCurrent() && e.originalEvent) pauseFollow() }
    const onClick = (e: MapMouseEvent) => {
      if (!isCurrent()) return
      const list = targetsRef.current
      if (!list?.length) return
      let best: RetroTarget | null = null
      let bestD = Infinity
      for (const tgt of list) {
        const c = map!.project([tgt.lng, tgt.lat])
        const d = Math.hypot(c.x - e.point.x, c.y - e.point.y)
        const mppx = metersPerPixel(tgt.lat, map!.getZoom())
        const pxR = Math.max(16, tgt.radius / Math.max(1e-6, mppx))
        if (d <= pxR && d < bestD) { bestD = d; best = tgt }
      }
      if (best) onTargetClickRef.current?.(best)
    }
    const onContextLost = () => failInstance('webglcontextlost')
    const onDataLoading = (e: unknown) => {
      if (!isCurrent()) return
      const ev = e as { dataType?: string; tile?: unknown }
      if (ev?.dataType === 'source' && ev.tile) tilesRequestedRef.current += 1
    }
    const onData = (e: unknown) => {
      if (!isCurrent()) return
      const ev = e as { dataType?: string; tile?: unknown }
      if (ev?.dataType === 'source' && ev.tile) tilesLoadedRef.current += 1
    }
    // 原創圖塊懶載入：style.ts 只放圖片 id（與 components/retro/tiles.ts 的 TileKind 同名），實際
    // ImageData 由 sprites.ts 的 tileImageData() 呼叫該共用圖塊表產生、在第一次被要求時掛上
    // （map.addImage）；找不到對應 id 就安靜略過（不影響其餘圖層照常渲染）。
    // v857：改掛 map.setMissingStyleImageResolver()，不再用 'styleimagemissing' 事件——MapLibre 6.11.2
    // 在事件觸發前就已組好該圖磚的圖片回應，事件裡同步 addImage 對「第一塊要求該圖片的圖磚」永遠慢一步，
    // 那塊圖磚會整塊少掉紋理（根因與重現見 track/cute/CuteMap.tsx resolveMissingImage 註解）；resolver
    // 會被 await 完才組回應，從根本上沒有這個時序問題。
    const TILE_IDS: readonly TileKind[] = ['grass', 'forest', 'water', 'wall', 'cobble', 'path', 'sand', 'rail']
    const onImageMissing = (imageId: string) => {
      if (!isCurrent()) return
      const id = imageId as TileKind
      if (map!.hasImage(id)) return
      if (!TILE_IDS.includes(id)) return
      try {
        if (id === 'water') { map!.addImage(id, tileImageData('water', 0)); waterFrameRef.current = 0 }
        else map!.addImage(id, tileImageData(id))
      } catch { /* ignore：單一圖塊掛不上不影響其他圖層 */ }
    }

    // pauseFollow() 已移到元件頂層（見上方宣告處的 ORBPOS_CONTRACT.md §A 說明），這裡直接沿用外層
    // closure 抓到的那一份，不再本地重宣告一次。

    function detachListeners() {
      try {
        map?.off('load', onLoad)
        map?.off('error', onError)
        map?.off('dragstart', onDragStart)
        map?.off('zoomstart', onZoomStart)
        map?.off('click', onClick)
        map?.off('dataloading', onDataLoading as never)
        map?.off('data', onData as never)
        map?.setMissingStyleImageResolver(null)
        map?.getCanvas().removeEventListener('webglcontextlost', onContextLost)
      } catch { /* ignore */ }
    }
    detachListenersRef.current = detachListeners

    try {
      reducedMotionRef.current = typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches

      // 像素魔法光點：純幾何/fillRect 繪製，無需非同步載入任何素材，掛載時直接建立一個實例即可
      // （比舊版角色 sprite 簡單很多，不再需要方向/走路幀狀態）。
      orbRef.current = new MagicOrb()

      // 像素化（CONTRACT.md §4）：目標「1 個地圖像素 ≈ 3 CSS px」，pixelRatio 設成 devicePixelRatio/3
      // （MapOptions.pixelRatio：canvas 實際解析度＝container 尺寸 × pixelRatio，配合 CSS
      // image-rendering:pixelated 放大時自然呈現方塊感，不需要另外縮放 DOM）。
      const dpr = window.devicePixelRatio || 1
      const lowPixelRatio = Math.max(0.28, dpr / 3)

      map = new MapLibreMap({
        container: containerRef.current,
        style: buildRetroStyle() as unknown as MapOptions['style'],
        center: [initialCenter[1], initialCenter[0]] as LngLatLike,
        zoom: initialZoom ?? 16.5,
        pitch: 0,
        bearing: 0,
        antialias: false,
        pixelRatio: lowPixelRatio,
        maxPitch: 0,
        attributionControl: false,
      } as MapOptions)
      mapRef.current = map
      // 建構後立刻設好（任何圖磚被請求之前），見上方 onImageMissing 說明。
      map.setMissingStyleImageResolver(onImageMissing)

      timeoutId = setTimeout(() => { if (!loadedRef.current) failInstance('load-timeout-8s') }, FALLBACK_TIMEOUT_MS)

      map.on('load', onLoad)
      map.on('error', onError)
      map.on('dragstart', onDragStart)
      map.on('zoomstart', onZoomStart)
      map.on('click', onClick)
      map.on('dataloading', onDataLoading as never)
      map.on('data', onData as never)
      try { map.getCanvas().addEventListener('webglcontextlost', onContextLost) } catch { /* ignore */ }

      // 水波兩幀動畫：每 500ms 切換一次。CONTRACT_R2 §1 根因調查發現：MapLibre 的
      // `Style.updateImage()`（map.updateImage() 底層）刻意不呼叫 `_afterImageUpdated()`
      // （對照同檔案 addImage()/removeImage() 都會呼叫），因此不會設 `_imagesListDirty`／
      // `_changedImages`／不會 fire 'data' 事件——也就不會觸發 `_updateTilesForChangedImages()`
      // 把該圖片廣播給 worker（"SI" 訊息）。經隔離重現（同一份 style.ts／tiles.ts，模擬 GPS 跟隨
      // 鏡頭每秒 easeTo()）證實：只要跑起這個 500ms updateImage() 迴圈，之後任何新載入/重新載入的
      // 圖磚，其 fill-pattern／line-pattern 全部失敗只剩底色（背景綠＋道路黑色外框線，也就是
      // CONTRACT_R2 §1 回報的「整片純綠＋兩條黑色斜線」），即使 map.hasImage(id) 全部回 true
      // （CPU 端圖片登記表沒問題，問題在 worker 端沒被正確通知重新烤入圖磚）；改成
      // removeImage()+addImage()（會完整走 `_afterImageUpdated` 那條路徑）後，同樣的 60 秒
      // GPS 跟隨壓力測試下所有圖塊持續正確渲染。addImage() 前必須先 removeImage()，否則
      // "Image id already exist" 會拋錯（map.on('error') 才會退回 Leaflet，這裡就地 try/catch
      // 吞掉單次失敗即可，不影響其餘圖層）。
      waterTimerRef.current = setInterval(() => {
        if (!isCurrent() || reducedMotionRef.current) return
        const m = mapRef.current
        if (!m || !m.hasImage('water')) return
        waterFrameRef.current = waterFrameRef.current ? 0 : 1
        try {
          m.removeImage('water')
          m.addImage('water', tileImageData('water', waterFrameRef.current))
        } catch { /* ignore：單次幀沒換成不影響其他圖層，下一輪 500ms 再試 */ }
      }, 500)

      ro = new ResizeObserver(() => { if (isCurrent()) { try { map!.resize() } catch { /* ignore */ }; applyRetroPadding() } })
      ro.observe(containerRef.current)

      startLoop()
    } catch (e) {
      failInstance('init-exception:' + ((e as Error)?.message || String(e)))
    }
    return () => {
      cancelled = true
      if (timeoutId) clearTimeout(timeoutId)
      if (resumeTimerRef.current) { clearTimeout(resumeTimerRef.current); resumeTimerRef.current = null }
      if (waterTimerRef.current) { clearInterval(waterTimerRef.current); waterTimerRef.current = null }
      if (rafRef.current != null) cancelAnimationFrame(rafRef.current)
      ro?.disconnect()
      detachListeners()
      try { map?.remove() } catch { /* ignore */ }
      if (mapRef.current === map) mapRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 只在掛載時建圖一次
  }, [])

  // __retroDebug 偵錯把手（docs/skins/RETRO_CONTRACT_R4.md §6／ORBPOS_CONTRACT.md）：每秒更新一次。
  // heroScreen/heroDir 兩個舊版角色專屬欄位已移除，改成 orbVisible/orbScreen（光點）；R4 新增
  // trailPoints/kmFlags/kmFlagScreens/trailScreenSample/particles/setOverlayHidden，供 E2E 做
  // 「有畫 vs 沒畫」像素對照與軌跡/公里旗位置驗證（做法比照 track/cute/CuteMap.tsx 同名欄位，兩邊
  // 各自獨立算，不共用程式碼）。ORBPOS 再新增 orbLatLng/lastFixAgeMs/orbState/following/
  // projectLngLat：orbLatLng＝實際拿來畫光點的座標（＝posRef.current，契約「光點一律畫在最新定位」
  // 沒有另外凍結，這裡如實回報用的就是同一份）；lastFixAgeMs/orbState 直接讀 renderFrame 每幀算好
  // 存起來的 ref（避免這裡的 1 秒輪詢自己重算一次跟 renderFrame 的判斷分岔）；projectLngLat 給 E2E
  // 直接呼叫 map.project() 拿同一套座標系的 CSS px，跟 orbScreen 對照用。
  useEffect(() => {
    const id = setInterval(() => {
      const map = mapRef.current
      if (!map) return
      const p = posRef.current
      let orbScreen = { x: 0, y: 0 }
      try {
        const proj = p ? map.project([p.lng, p.lat]) : map.project([initialCenterRef.current[1], initialCenterRef.current[0]])
        orbScreen = { x: Math.round(proj.x), y: Math.round(proj.y) }
      } catch { /* ignore */ }
      const c = map.getCenter()
      // 根因調查暫時欄位（CONTRACT_R2 §1）：patternImages/canvasSize，抓 addImage 是否真的掛上、
      // 容器尺寸是否為 0——調查完成後視情況精簡。
      const TILE_IDS = ['grass', 'forest', 'water', 'wall', 'cobble', 'path', 'sand', 'rail'] as const
      const patternImages = Object.fromEntries(TILE_IDS.map((k) => [k, map.hasImage(k)]))
      const canvas = map.getCanvas()
      let trailPoints = 0
      for (const seg of segmentsRef.current) trailPoints += seg?.length || 0
      const kmFlagScreens = (kmMarksRef.current || []).map((m) => {
        try { const pt = map.project([m.lng, m.lat]); return { km: m.km, x: Math.round(pt.x), y: Math.round(pt.y) } }
        catch { return { km: m.km, x: 0, y: 0 } }
      })
      const flatTrail: [number, number][] = []
      for (const seg of segmentsRef.current) for (const pt of seg || []) flatTrail.push(pt)
      const trailScreenSample: { x: number; y: number }[] = []
      if (flatTrail.length) {
        const nSample = Math.min(5, flatTrail.length)
        for (let i = 0; i < nSample; i++) {
          const idx = nSample === 1 ? 0 : Math.round((i * (flatTrail.length - 1)) / (nSample - 1))
          try { const pt = map.project([flatTrail[idx][1], flatTrail[idx][0]]); trailScreenSample.push({ x: Math.round(pt.x), y: Math.round(pt.y) }) } catch { /* ignore */ }
        }
      }
      ;(window as unknown as { __retroDebug?: unknown }).__retroDebug = {
        center: { lat: c.lat, lng: c.lng },
        zoom: map.getZoom(),
        pitch: map.getPitch(),
        bearing: map.getBearing(),
        // ORBPOS_CONTRACT.md 第二輪驗收12：比照 CuteMap，直接讀 MapLibre 自己的 getPadding()。
        padding: map.getPadding(),
        lastPos: lastPosRef.current ? { lat: lastPosRef.current.lat, lng: lastPosRef.current.lng } : null,
        tilesRequested: tilesRequestedRef.current,
        tilesLoaded: tilesLoadedRef.current,
        // 沒有真實 GPS 定位時 renderFrame 完全不畫光點（契約「沒有定位時不畫」，見 renderFrame）；
        // orbVisible 如實反映這件事。
        orbVisible: !!p,
        orbScreen,
        // ORBPOS_CONTRACT.md：實際拿來畫光點的座標——契約「光點一律畫在最新定位」，本檔沒有任何
        // 「凍結在上一個好點」的邏輯，這裡就是 posRef.current 本身，如實回報。
        orbLatLng: p ? { lat: p.lat, lng: p.lng } : null,
        lastFixAgeMs: lastFixAgeMsRef.current,
        orbState: orbStateRef.current,
        following: followingRef.current,
        trailPoints,
        kmFlags: kmMarksRef.current?.length || 0,
        kmFlagScreens,
        trailScreenSample,
        particles: orbRef.current?.particleCount ?? 0,
        fps: fpsRef.current,
        patternImages,
        waterFrame: waterFrameRef.current,
        canvasSize: { w: canvas.width, h: canvas.height, cssW: canvas.clientWidth, cssH: canvas.clientHeight },
        // ORBPOS_CONTRACT.md：回傳同一個 map.project() 的 CSS px，供 E2E 跟 orbScreen／kmFlagScreens
        // 對照用（不額外四捨五入，保留原始精度）。
        projectLngLat: (lng: number, lat: number) => {
          try { const pt = map.project([lng, lat]); return { x: pt.x, y: pt.y } } catch { return null }
        },
        // 契約 §6：只給 E2E 做「有畫 vs 沒畫」前後對照用，預設 false。
        setOverlayHidden: (v: boolean) => { overlayHiddenRef.current = !!v },
      }
    }, 1000)
    return () => {
      clearInterval(id)
      try { delete (window as unknown as { __retroDebug?: unknown }).__retroDebug } catch { /* ignore */ }
    }
  }, [])

  // 跑步狀態離開 tracking：清移動時脈鎖存（不影響既有 pointsRef 等跑步狀態）；新一輪開跑（從非
  // tracking 轉進 tracking）強制把 overlayHiddenRef 歸回 false——理由與做法比照
  // track/cute/CuteMap.tsx 同一段：保證就算上一輪（或任何來源）曾把它叫成 true 卡住，新開始的一趟
  // 跑步一定看得到光點／軌跡／徽章，不需要整頁重新整理。
  useEffect(() => {
    if (status !== 'tracking') lastMovingAtRef.current = 0
    if (status === 'tracking' && prevStatusRef.current !== 'tracking') overlayHiddenRef.current = false
    prevStatusRef.current = status
  }, [status])

  // GPS 位置更新：跟隨鏡頭（center 平移，pitch/bearing 恆 0）＋判斷是否為真實移動（供光點噴火花用，
  // 見上方 moveAnchorRef／lastMovingAtRef 宣告處的根因說明；不再需要算面向，光點沒有方向性）。
  useEffect(() => {
    if (!pos) return
    writeLastPos(pos.lat, pos.lng)
    const now = performance.now()
    lastFixReceivedAtRef.current = now // ORBPOS_CONTRACT.md §B fixAgeMs() fallback，見上方宣告處
    const anchor = moveAnchorRef.current
    // 位移 ≥ MOVE_NOISE_FLOOR_M 才算「真實移動」；第一個基準點只是起點，不算移動。抖動範圍內的
    // 座標忽略、基準點與 lastMovingAtRef 都不動——由 renderFrame 每幀重新評估「距上次真實移動是否
    // 已超過 STILL_TIMEOUT_MS」來決定要不要繼續噴火花，時間本身會讓它自然衰減，不需要新的 pos
    // 進來才能歸零。
    if (!anchor || haversineM([anchor.lat, anchor.lng], [pos.lat, pos.lng]) >= MOVE_NOISE_FLOOR_M) {
      if (anchor) lastMovingAtRef.current = now
      moveAnchorRef.current = { lat: pos.lat, lng: pos.lng }
    }
    lastPosRef.current = { lat: pos.lat, lng: pos.lng, t: now }
    const map = mapRef.current
    // ORBPOS_CONTRACT.md §A（已證實缺陷 1）：只移動 center，不再每次都強制 zoom:16.5——保留使用者
    // 目前的縮放，跟隨更新才不會把使用者剛縮放過的畫面彈回預設值。
    if (map && followingRef.current) {
      const followOpts: { center: [number, number]; pitch: number; bearing: number; duration: number; essential: true; zoom?: number } =
        { center: [pos.lng, pos.lat], pitch: 0, bearing: 0, duration: 900, essential: true }
      // 見上方 RECENTER_ZOOM／recenterZoomPendingRef 宣告處的根因說明：recenter() 剛按下、zoom 還沒
      // 收斂到 16.5 前，這裡也一起帶 zoom，即使被打斷也不會凍結在半路；量到已經夠接近目標就清旗標。
      if (recenterZoomPendingRef.current) {
        if (Math.abs(map.getZoom() - RECENTER_ZOOM) < RECENTER_ZOOM_EPS) recenterZoomPendingRef.current = false
        else followOpts.zoom = RECENTER_ZOOM
      }
      try { map.easeTo(followOpts) } catch { /* ignore */ }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pos?.lat, pos?.lng])

  function startLoop() {
    const frame = (t: number) => {
      rafRef.current = requestAnimationFrame(frame)
      frameCountRef.current += 1
      if (t - fpsWindowStartRef.current >= 1000) {
        fpsRef.current = frameCountRef.current
        frameCountRef.current = 0
        fpsWindowStartRef.current = t
      }
      const map = mapRef.current
      if (!map || !loadedRef.current || document.hidden) return
      const fps = focusModeRef.current ? 8 : 30 // CONTRACT.md §4：30fps 上限，專注模式 8fps
      if (t - lastFrameTRef.current < 1000 / fps) return
      const dt = lastFrameTRef.current ? Math.min(0.25, (t - lastFrameTRef.current) / 1000) : 0.016
      lastFrameTRef.current = t
      try {
        renderFrame(map, dt)
      } catch (e) {
        onFallbackFromRender('render-exception:' + ((e as Error)?.message || String(e)))
      }
    }
    rafRef.current = requestAnimationFrame(frame)
  }

  function onFallbackFromRender(reason: string) {
    if (failedRef.current) return
    failedRef.current = true
    // eslint-disable-next-line no-console
    console.warn('[retro-map] fallback', reason)
    if (rafRef.current != null) { cancelAnimationFrame(rafRef.current); rafRef.current = null }
    if (waterTimerRef.current) { clearInterval(waterTimerRef.current); waterTimerRef.current = null }
    try { detachListenersRef.current() } catch { /* ignore */ }
    try { mapRef.current?.remove() } catch { /* ignore */ }
    mapRef.current = null
    try { delete (window as unknown as { __retroDebug?: unknown }).__retroDebug } catch { /* ignore */ }
    onFallbackRef.current(reason)
  }

  function renderFrame(map: MapLibreMap, dt: number) {
    const canvas = canvasRef.current
    if (!canvas) return
    const dpr = window.devicePixelRatio || 1
    // ORBPOS_CONTRACT.md §D：疊層畫布的座標系尺寸以 MapLibre 自己目前實際在用的尺寸為準
    // （map.project() 內部用的就是這個尺寸），不用 wrapping container 的 clientWidth/Height——
    // container 的 DOM 尺寸跟著版面每一幀都同步（clientWidth 是即時算出來的 live 值），但 MapLibre
    // 自己的 resize()（進而更新它內部真正拿去投影的尺寸）是透過 ResizeObserver 觸發、天生比
    // clientWidth 的即時讀值慢一拍（ResizeObserver callback 排在下一輪 layout 之後才跑），兩者短暫
    // 不同步的那幾幀，若疊層畫布用 container 的即時尺寸算座標系，project() 給的點會對不上疊層的
    // clip space、光點/軌跡/公里旗看起來偏移（ORBPOS_CONTRACT.md 已證實缺陷 5：面板拖動／網址列
    // 收合時常見 Δ高度/2）。`map.transform` 是 MapLibre 內部欄位、TypeScript 公開型別故意不匯出
    // （tsc 會報 Property 'transform' does not exist），改用一定會與它同步更新的公開替代：
    // map.getCanvas().width／height（backing store 像素數，MapLibre 只在自己真正 resize() 時才會
    // 改這兩個值，跟它內部尺寸同一次寫入、天生同步）除以 map.getPixelRatio()（本檔建圖時設的
    // lowPixelRatio，見上方 mount effect），換算回 CSS px 邏輯尺寸——等於間接讀到跟 project() 同一把
    // 尺，且完全是公開 API，不需要碰內部欄位。canvas 元素本身仍維持 CSS width/height:100%（JSX 內
    // 既有的 R4 DPR 修正，見下方 return），backing store 解析度與 CSS 顯示尺寸各自獨立，這裡的 w/h
    // 只決定 backing store 要開多大、以及 ctx 座標系的邏輯寬高。
    const mapCanvas = map.getCanvas()
    const mapRatio = map.getPixelRatio() || dpr
    const w = mapCanvas.width / mapRatio, h = mapCanvas.height / mapRatio
    if (w <= 0 || h <= 0) return
    const wantW = Math.round(w * dpr), wantH = Math.round(h * dpr)
    if (canvas.width !== wantW || canvas.height !== wantH) { canvas.width = wantW; canvas.height = wantH }
    const ctx = canvas.getContext('2d')
    if (!ctx) return
    ctx.imageSmoothingEnabled = false
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, w, h)

    // 整數倍率（CONTRACT.md §4「1 個地圖像素≈3 CSS px」的像素化精神一併套用在光點與軌跡上）：非整數
    // 倍率會讓 fillRect 落在次像素邊界，被瀏覽器抗鋸齒糊成灰邊，破壞限色像素風的銳利感（2026-09-27
    // 冒煙截圖比對像素值時發現的舊根因，沿用同一個 clamp(2,4) 公式，見 docs/skins/RETRO_CONTRACT_R4.md
    // §3「放大倍率沿用現有 heroScale」）。orbScale 同時是光點格子放大倍率、軌跡線寬、火花尺寸、
    // 以及公里旗（ORBPOS_CONTRACT.md 補充）的共用基準值，比例才不會跑掉（比照舊版 heroScale 同時
    // 服務角色圖案/腳印機制/足跡線寬的慣例）。
    const orbScale = Math.round(Math.max(2, Math.min(4, (w / 360) * 2)))

    // ORBPOS_CONTRACT.md §B：這筆定位多久以前收到（見 fixAgeMs() 說明），供下面「定位中」外觀判斷、
    // 也存進 ref 供 __retroDebug 讀（避免 debug 的 1 秒輪詢自己重算一次跟這裡邏輯分岔）。
    const p = posRef.current
    const ageMs = fixAgeMs(p as RetroPosWithTs | null)
    lastFixAgeMsRef.current = Number.isFinite(ageMs) ? ageMs : 0
    const searching = !!p && (ageMs > SEARCHING_STALE_MS || (typeof p.acc === 'number' && p.acc > SEARCHING_MAX_ACC))
    orbStateRef.current = searching ? 'searching' : 'normal'
    if (!reducedMotionRef.current) searchPulseRef.current += dt

    // docs/skins/RETRO_CONTRACT_R4.md §6「setOverlayHidden(bool)：暫時不畫光點／軌跡／徽章」：只給
    // E2E 做「有畫 vs 沒畫」前後對照截圖用（target 圖示不在契約列舉範圍內，維持照常繪製，比照
    // track/cute/CuteMap.tsx 同一段）。
    const overlayHidden = overlayHiddenRef.current
    if (!overlayHidden) {
      drawRoute(ctx, map, segmentsRef.current, orbScale)
      drawKmMarks(ctx, map, kmMarksRef.current, orbScale)
    }
    drawTargets(ctx, map, targetsRef.current)

    // 光點目前位置：沒有真實 GPS 定位時完全不畫（契約「沒有定位時不畫」，取代舊版退回 initialCenter
    // 畫一個假角色的行為，比照 cute/CuteMap.tsx 同一段）。update() 仍要照常呼叫（讓火花計時/浮動
    // 相位持續推進，overlayHidden 只影響「畫不畫出來」，不影響狀態本身，恢復顯示時才不會瞬間跳一
    // 大段）。ORBPOS_CONTRACT.md §B「光點一律畫在最新定位」：這裡永遠用 p（=posRef.current＝最新
    // 一筆 pos）投影畫點，不做「凍結在上一個好點」；精度差／過期只改外觀（半透明＋精度圈），不改
    // 位置本身。
    if (p) {
      const pt = map.project([p.lng, p.lat])
      // 移動判定：距上次真實移動（見 moveAnchorRef／lastMovingAtRef 根因說明）是否還在
      // STILL_TIMEOUT_MS 之內；用 render 迴圈當下的 performance.now() 重新評估，時間流逝本身就會讓
      // 「移動中」自然衰減回靜止（契約「靜止 3 秒後歸零」）。
      const moving = statusRef.current === 'tracking' && lastMovingAtRef.current > 0 && (performance.now() - lastMovingAtRef.current) < STILL_TIMEOUT_MS
      const orb = orbRef.current
      if (orb) {
        orb.update(dt, moving, reducedMotionRef.current, pt.x, pt.y)
        if (!overlayHidden) {
          // ORBPOS_CONTRACT.md §B：「定位中」外觀＝精度圈（依 acc 換算半徑，上限 120px）＋光點本體
          // 半透明。acc 缺席（例如尚未拿到任何精度資訊）就不畫圈，只讓光點變淡（age 過期那一種
          // searching 仍然成立）。
          if (searching && typeof p.acc === 'number' && p.acc > 0) {
            const mpp = metersPerPixel(p.lat, map.getZoom())
            const ringPx = Math.min(SEARCH_RING_MAX_PX, p.acc / Math.max(1e-6, mpp))
            drawSearchRing(ctx, pt.x, pt.y, ringPx, orbScale, searchPulseRef.current, reducedMotionRef.current)
          }
          orb.draw(ctx, pt.x, pt.y, orbScale, { reducedMotion: reducedMotionRef.current, dim: searching })
        }
      }
    }
  }

  // ORBPOS_CONTRACT.md §B「定位中」精度圈：像素點狀圈（沿用契約用字「像素點狀圈」，不用平滑的
  // 抗鋸齒圓弧虛線——用離散的正方形色塊沿圓周排列，跟本檔其餘像素繪製手法一致）。radiusPx 由呼叫端
  // 算好、已 clamp 到上限（這裡不重算）；pulsePhase 由呼叫端的 searchPulseRef 累加傳入，
  // reduced-motion 時呼叫端已停止推進該 ref，這裡另外用固定透明度（不吃 pulsePhase）雙重保險，確保
  // 「reduced-motion 下靜態」不會因為呼叫順序意外漏接。
  function drawSearchRing(ctx: CanvasRenderingContext2D, cx: number, cy: number, radiusPx: number, orbScale: number, pulsePhase: number, reducedMotion: boolean) {
    if (radiusPx < 4) return
    const dotSize = Math.max(1, orbScale)
    const n = Math.max(10, Math.round((radiusPx * Math.PI * 2) / (dotSize * 3)))
    const alpha = reducedMotion ? 0.55 : 0.4 + 0.25 * Math.sin(pulsePhase * 1.4)
    ctx.save()
    ctx.globalAlpha = Math.max(0.15, Math.min(0.9, alpha))
    ctx.fillStyle = '#3cbcfc' // RETRO_PALETTE 水藍（亮）：「定位中」搜尋圈專用色，跟金色軌跡/公里旗區隔
    for (let i = 0; i < n; i++) {
      const t = (i / n) * Math.PI * 2
      const x = cx + Math.cos(t) * radiusPx
      const y = cy + Math.sin(t) * radiusPx
      ctx.fillRect(Math.round(x - dotSize / 2), Math.round(y - dotSize / 2), dotSize, dotSize)
    }
    ctx.restore()
  }

  // 軌跡＝光點走過的路（docs/skins/RETRO_CONTRACT_R4.md §4）：黑色外框（方頭方角）＋金黃主線（同）
  // ＋每隔約 8 個邏輯像素在主線上點一顆白色像素光粒，強化「光點拖出來的軌跡」感。訊號中斷分段
  // （segs 陣列本身已依 `;` 分段）照舊不相連，只換樣式；不再是舊版的金色虛線。
  //
  // 效能（契約「長距離效能」）：跟舊版一樣先 decimate 到最多 600 點才計算投影/樣式（全馬數千點時
  // 不會每幀 O(n) 重算全部原始點），比照 cute/CuteMap.tsx drawRoute 的抽稀做法；光粒取樣沿著這
  // 已抽稀後的 ≤600 點序列累加距離計算，同樣是 O(抽稀後點數) 而非 O(原始點數)。
  //
  // 軌跡末端接到光點：只在「最後一段」且目前定位精度可信時，把光點目前螢幕座標補進去畫——不可用
  // 未過濾的即時 GPS 話出假線（契約逐字，根因與做法比照 cute/CuteMap.tsx drawRoute 的 pAccOk 判斷：
  // p 是完全未經精度過濾的即時定位，訊號變差時可能是誤差達數十甚至數百公尺的瞬時座標，若無條件
  // 連過去會在畫面上拉出一條與實際路徑無關的橡皮筋線）。
  function drawRoute(ctx: CanvasRenderingContext2D, map: MapLibreMap, segs: [number, number][][], orbScale: number) {
    if (!segs?.length) return
    const p = posRef.current
    const pAccOk = !!p && (p.acc == null || p.acc === 0 || p.acc <= TRAIL_CONNECT_MAX_ACC)
    const borderW = Math.max(1, 2 * orbScale) // 契約「黑色外框寬約 4×倍率/2」＝ 2×倍率
    const mainW = Math.max(1, 1 * orbScale) // 契約「金黃主線寬約 2×倍率/2」＝ 1×倍率
    const dotSpacing = 8 * orbScale // 契約「每隔約 8 個邏輯像素」＝ 8×倍率 CSS px
    const dotSize = Math.max(1, orbScale) // 「一顆白色像素」＝ 1 個邏輯像素

    segs.forEach((seg, segIdx) => {
      if (!seg || seg.length < 2) return
      const pts = seg.length > 600 ? decimate(seg, 600) : seg
      const screen = pts.map((pp) => map.project([pp[1], pp[0]]))
      if (segIdx === segs.length - 1 && p && pAccOk) {
        const orbPt = map.project([p.lng, p.lat])
        const last = screen[screen.length - 1]
        if (Math.hypot(orbPt.x - last.x, orbPt.y - last.y) > 0.5) screen.push(orbPt)
      }
      ctx.save()
      ctx.lineCap = 'butt' // 方頭方角（契約逐字，取代舊版 round，維持像素風的硬邊）
      ctx.lineJoin = 'miter'
      ctx.strokeStyle = '#000000'
      ctx.lineWidth = borderW
      ctx.beginPath()
      screen.forEach((pt, i) => (i === 0 ? ctx.moveTo(pt.x, pt.y) : ctx.lineTo(pt.x, pt.y)))
      ctx.stroke()
      ctx.strokeStyle = GOLD
      ctx.lineWidth = mainW
      ctx.beginPath()
      screen.forEach((pt, i) => (i === 0 ? ctx.moveTo(pt.x, pt.y) : ctx.lineTo(pt.x, pt.y)))
      ctx.stroke()
      ctx.restore()
      drawTrailLightDots(ctx, screen, dotSpacing, dotSize)
    })
  }

  // 沿主線累積距離取樣，每滿一個 spacing 點一顆白色像素方塊——用累積距離而非逐點都點，才不會因為
  // GPS 點密度不均（時快時慢）讓光粒間距忽疏忽密。acc 在段落內延續（同一 seg 的相鄰兩點之間累加），
  // 不同 seg 之間各自重新起算（訊號中斷分段本就不相連，光粒沒有必要跨段連續）。
  function drawTrailLightDots(ctx: CanvasRenderingContext2D, screen: { x: number; y: number }[], spacing: number, size: number) {
    if (screen.length < 2) return
    ctx.save()
    ctx.fillStyle = '#ffffff'
    let acc = 0
    for (let i = 1; i < screen.length; i++) {
      const a = screen[i - 1], b = screen[i]
      const segLen = Math.hypot(b.x - a.x, b.y - a.y)
      if (segLen <= 0.0001) continue
      const dirX = (b.x - a.x) / segLen, dirY = (b.y - a.y) / segLen
      let dist = spacing - acc
      while (dist <= segLen) {
        const x = a.x + dirX * dist, y = a.y + dirY * dist
        ctx.fillRect(Math.round(x - size / 2), Math.round(y - size / 2), Math.ceil(size), Math.ceil(size))
        dist += spacing
      }
      acc = (acc + segLen) % spacing
    }
    ctx.restore()
  }

  // ORBPOS_CONTRACT.md 補充：旗子改吃 orbScale（原本固定傳 1，是舊版「小到幾乎看不見」的主因之一，
  // 詳見 sprites.ts drawKmFlag 檔內註解）。
  function drawKmMarks(ctx: CanvasRenderingContext2D, map: MapLibreMap, marks: { km: number; lat: number; lng: number }[], orbScale: number) {
    if (!marks?.length) return
    for (const m of marks) {
      const pt = map.project([m.lng, m.lat])
      drawKmFlag(ctx, pt.x, pt.y, m.km, orbScale)
    }
  }

  function drawTargets(ctx: CanvasRenderingContext2D, map: MapLibreMap, tgts: RetroTarget[]) {
    if (!tgts?.length) return
    for (const tgt of tgts) {
      const ring = geoCircle(tgt.lat, tgt.lng, Math.max(4, tgt.radius)).map((p) => map.project([p[1], p[0]]))
      const center = map.project([tgt.lng, tgt.lat])
      ctx.save()
      ctx.strokeStyle = tgt.done ? GOLD : '#ffffff'
      ctx.lineWidth = 2
      ctx.setLineDash([3, 4]) // 半徑以點狀虛線圓圈表示
      ctx.beginPath()
      ring.forEach((p, i) => (i === 0 ? ctx.moveTo(p.x, p.y) : ctx.lineTo(p.x, p.y)))
      ctx.closePath()
      ctx.stroke()
      ctx.restore()
      drawTargetIcon(ctx, center.x, center.y, tgt.kind, tgt.done, 1)
    }
  }

  return (
    <div ref={wrapRef} style={{ position: 'absolute', inset: 0 }}>
      <div ref={containerRef} className="retro-map" style={{ position: 'absolute', inset: 0 }} />
      {/* R4 自我檢查根因修補：<canvas> 是「可替換元素」（replaced element，跟 <img> 同一類），
          position:absolute+inset:0 對它不像對一般 <div> 那樣會撐滿容器——CSS 規格規定可替換元素
          在 width/height 未另外指定時，用「內在尺寸」（canvas.width/height 這兩個 HTML attribute，
          即 renderFrame() 設的 backing pixel 尺寸）當版面尺寸，inset:0 只決定定位、不會把它縮回容器
          大小。devicePixelRatio===1 時 backing pixel 尺寸恰好等於容器的 CSS px 尺寸，兩者數字一樣，
          這個問題被完全遮蓋；一旦 devicePixelRatio>1（幾乎所有真實手機），canvas.width/height 是
          容器 CSS 尺寸的 dpr 倍，畫面上這個 <canvas> 元素本身也跟著變成 dpr 倍大並溢出容器，疊加
          ctx.setTransform(dpr,...) 内部座標系統的 dpr 倍縮放，兩個 dpr 相乘，畫面呈現「整個疊層放大
          dpr 倍、大部分內容跑到可視範圍外」——光點/軌跡/公里旗因此在 dpr>1 的裝置上實際上全部跑位。
          明確補上 width/height:100%（可替換元素有這兩個屬性時才會照容器撐滿，跟 <img> 的修法一致）
          即可讓 canvas 的 CSS 版面尺寸鎖定為容器大小，backing pixel 解析度與 CSS 顯示尺寸各自獨立，
          dpr 倍的細節只留在 backing pixel 那一層（真正達成「更高解析度」而非「整層放大位移」）。
          （2026-09-28 R4 devicesScaleFactor=4 自我檢查截圖時發現：dpr=1 的既有 E2E 從未測出這個根因，
          光點/軌跡在自我檢查截圖中完全對不上預期座標，才回頭挖出這個一直存在、影響所有 dpr>1 真實
          裝置的既有問題——cute/scifi 的同類疊層 canvas 依契約範圍本輪不得碰，已在回報中列為待辦。） */}
      <canvas ref={canvasRef} style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', pointerEvents: 'none', display: 'block', imageRendering: 'pixelated' }} />
    </div>
  )
})

export default RetroMap
