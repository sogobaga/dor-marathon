'use client'

// 溫馨可愛（cute）GPS 地圖主元件（docs/skins/CUTE_CONTRACT.md§4，第二輪見 CONTRACT_R2.md§4，
// 第二輪補正見 docs/skins/CUTE_CONTRACT_R2b.md）。架構比照
// track/retro/RetroMap.tsx（只在 track/page.tsx 判斷 getActiveSkin()==='cute' 為真時，以
// next/dynamic(ssr:false) 動態載入本檔，maplibre-gl 與本目錄程式碼只進獨立 chunk，非白名單使用者的
// /track 首屏 bundle 完全不含這裡的程式碼）。
//
// 隔離原則（CONTRACT.md §1，沿用 retro/scifi 同一段規則）：既有 Leaflet 地圖照舊建立與運作（本檔完全
// 不碰 track/page.tsx 的 Leaflet refs／狀態），本元件只是疊在同一位置的另一層視覺呈現；任何初始化失敗
// （WebGL 不支援、8 秒未 load、context lost、渲染例外）一律呼叫 onFallback() 通知父層卸載本元件、恢復
// 顯示 Leaflet——跑步紀錄（GPS 取點/距離/上傳）完全不依賴本檔是否成功渲染。
//
// v857 起不再使用角色造型：CONTRACT_R2.md §4.1 決定移除原本代表跑者的 Q 版「小井」角色（原 SVG 離屏
// canvas 圖＋彈跳/翻轉動畫，見已刪除的 runner.ts），避免未來需要角色性別顯示或擴充角色種類造成的
// 維護負擔。取而代之的是一顆不具性別/外觀意涵的「靈魂光點」（見 ./orb.ts 的 SoulOrb），移動路徑改由
// 光點拖出的發光軌跡呈現（見下方 drawRoute），每公里徽章維持（見 ./icons.ts drawKmHeartBadge）。
//
// 與 retro 的差異：cute 不是像素風，維持一般抗鋸齒平滑渲染（無 pixelRatio 降級／無
// image-rendering:pixelated）；不需要水域兩幀動畫（靜態小波浪 fill-pattern 已足夠，契約沒有要求
// 動畫水波）；顯示公園／地標文字標籤（retro 刻意不顯示任何文字）。

import { forwardRef, useEffect, useImperativeHandle, useRef } from 'react'
import { Map as MapLibreMap, config as maplibreConfig, type MapOptions, type LngLatLike, type MapMouseEvent, type MapMovementEvent, type MissingStyleImageResolver } from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { buildCuteStyle } from './style'
import { tileImageData, type TileKind } from './tiles'
import { SoulOrb } from './orb'
import { drawKmHeartBadge, drawTargetIcon, drawStartDot } from './icons'
import { isWebglSupported, haversineM, metersPerPixel, geoCircle, decimate } from './geo'
import type { CuteMapHandle, CuteMapProps, CuteTarget, CutePos } from './types'

// maplibre-gl worker 自架修正：與 scifi/retro 同一個根因/同一套修法（見 RetroMap.tsx 詳細註解）——
// 三套風格共寫同一個 module-level flag（maplibreConfig.WORKER_URL），同一個 maplibre-gl 套件實例的
// 同一份設定，誰先執行都會設好、不會互相覆蓋成不同值，因此不需要 import 對方檔案。
const MAPLIBRE_WORKER_URL = '/vendor/maplibre-gl-6.11.2/maplibre-gl-worker.mjs'
if (typeof window !== 'undefined' && !maplibreConfig.WORKER_URL) {
  maplibreConfig.WORKER_URL = MAPLIBRE_WORKER_URL
}

const FALLBACK_TIMEOUT_MS = 8000
const LAST_POS_KEY = 'dor_cute_last_pos' // 與 track/page.tsx 的 CUTE_LAST_POS_KEY 同一把 key
const RESUME_FOLLOW_MS = 8000
// FIX（review 抓到的 major 根因，docs/skins/ORBPOS_CONTRACT.md 驗收項目 4「回到目前位置→zoom=16.5、
// 之後 3 次定位更新都維持跟隨」）：recenter() 的 zoom:16.5 easeTo 若在動畫尚未跑完（500ms）前被下面
// 「GPS 位置更新」effect 的跟隨 easeTo 打斷，會被 MapLibre 永久凍結在打斷當下的中間值，且再也不會自己
// 恢復——追出根因見 node_modules/maplibre-gl/src/geo/projection/mercator_camera_helper.ts
// handleEaseTo()：新一次 easeTo 的 startZoom 讀的是 tr.zoom，這是「目前正在跑的動畫已經套用到 map
// 實際 transform 上」的即時值（不是動畫開始前的舊值、也不是動畫目標值）；跟隨 effect 的 easeTo 依
// ORBPOS_CONTRACT.md §A（保留使用者縮放）刻意不帶 zoom，一旦在這種情況下打斷 recenter，
// endZoom=zoom=startZoom、isZooming 判定為 false，新動畫完全不會再移動 zoom，就此卡在那個中間值。
// recenterZoomPendingRef（見下方使用處）讓「recenter() 剛按下、zoom 還沒真的收斂到 16.5」這段期間，
// 跟隨 effect 的 easeTo 繼續一起帶 zoom:16.5——即使又被下一次跟隨更新打斷，新動畫的終點依然是
// 16.5，只是要多花一點時間收斂，不會卡在半路；量到目前 zoom 已經夠接近 16.5 就視為收斂完成，恢復
// 「跟隨只搬 center」的原設計，不會一直霸占使用者之後自行縮放的操作。
const RECENTER_ZOOM = 16.5
const RECENTER_ZOOM_EPS = 0.05
const TILE_IDS: readonly TileKind[] = ['tree', 'wave']
const STYLE_IMAGE_IDS: Record<TileKind, string> = { tree: 'cute-tree', wave: 'cute-wave' }

// 軌跡「發光緞帶」色票（docs/skins/CUTE_CONTRACT_R2b.md §C 逐字色號，取代 CONTRACT_R2.md §4.3 第一版較淡的
// candy/lavender token）：本檔獨立宣告，不 import components/cute/decor.ts 的 CUTE_PALETTE
// （理由同 orb.ts／icons.ts 頂端註解）。
const TRAIL_END = '#ff6fae' // 主線漸層終點（→光點端）／外層光暈色號，與光點本體深色同一色號，強化「同一顆光點拖出來」的視覺連結
const TRAIL_START = '#b58cff' // 主線漸層起點（軌跡起點端）
const TRAIL_CORE = '#ffffff' // 內芯（docs/skins/CUTE_CONTRACT_R2b.md §C 逐字「2px 白色」）

// FIX round2（review 抓到的根因修正，見 CuteMap 內對應呼叫處的詳細註解）：
// - MOVE_NOISE_FLOOR_M／STILL_TIMEOUT_MS：移動/靜止判定改用「位移門檻＋逾時衰減」而非瞬時速度。
// - TRAIL_CONNECT_MAX_ACC：軌跡末端連到光點的門檻，避免精度差的瞬時定位拉出橡皮筋線。
// 三者都沿用 track/page.tsx 既有同類常數的數值慣例（JITTER_MIN=6／MAX_ACC=65），本檔獨立宣告
// 一份數字（不 import page.tsx），理由同檔頭其他常數（cute 動態 chunk 隔離）。
const MOVE_NOISE_FLOOR_M = 6
const STILL_TIMEOUT_MS = 3000
const TRAIL_CONNECT_MAX_ACC = 65

// docs/skins/ORBPOS_CONTRACT.md §B：「搜尋中」外觀的判定門檻——GPS acc 精度差於 65m（沿用
// TRAIL_CONNECT_MAX_ACC／page.tsx MAX_ACC 同一數字慣例）或最新定位已超過 15 秒沒有更新（背景回前景、
// 訊號中斷）時，光點改用半透明＋精度圈的「搜尋中」外觀（見下方 isOrbSearching／renderFrame），但仍然
// 一律畫在 posRef.current 這個最新值上，不凍結在舊點。
const MAX_ORB_ACC = 65
const STALE_FIX_MS = 15000

function writeLastPos(lat: number, lng: number) {
  try { localStorage.setItem(LAST_POS_KEY, JSON.stringify({ lat, lng })) } catch { /* 私密瀏覽/storage 被封鎖：純錦上添花，略過即可 */ }
}

const CuteMap = forwardRef<CuteMapHandle, CuteMapProps>(function CuteMap(props, ref) {
  const { pos, status, segments, kmMarks, targets, focusMode, initialCenter, initialZoom, onFallback, onTargetClick, bottomInset, onFollowChange } = props

  const containerRef = useRef<HTMLDivElement | null>(null)
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const mapRef = useRef<MapLibreMap | null>(null)
  const rafRef = useRef<number | null>(null)
  const detachListenersRef = useRef<() => void>(() => {})
  const failedRef = useRef(false)
  const loadedRef = useRef(false)
  const followingRef = useRef(true)
  const recenterZoomPendingRef = useRef(false) // 見上方 RECENTER_ZOOM 宣告處：recenter() 剛按下、zoom 還沒收斂到 16.5 期間為 true
  const lastPosRef = useRef<{ lat: number; lng: number; t: number } | null>(null)
  // 移動判定（供光點呼吸快慢／冒粒子用）——FIX round2 review 抓到根因：原本用「上一點到這一點」的
  // 瞬時位移/時間差算速度，GPS 靜止時仍會持續回報幾公尺內的抖動座標，短時間差除出來的瞬時速度
  // 常態性超過門檻，導致「停止移動後 particles 回不到 0」（CONTRACT_R2.md §5.2 逐字要求靜止 3 秒後
  // 歸零；且原本只在 pos 真的改變時才重算，座標完全沒變/被去重時也不會自然衰減）。改成：
  // moveAnchorRef 記錄「上一個真實移動基準點」，位移 ≥ MOVE_NOISE_FLOOR_M 才算真實移動並推進
  // lastMovingAtRef；renderFrame 每幀用當下 performance.now() 重新判斷「距上次真實移動是否已超過
  // STILL_TIMEOUT_MS」，即使之後 GPS 回報完全相同或抖動範圍內的座標（不會再推進這兩者），時間流逝
  // 本身也會讓「移動中」自然衰減回靜止。
  const moveAnchorRef = useRef<{ lat: number; lng: number } | null>(null)
  const lastMovingAtRef = useRef(0) // performance.now()：最後一次判定為「真實移動」的時間戳，0＝從未偵測到移動
  const reducedMotionRef = useRef(false)
  const initialCenterRef = useRef(initialCenter)
  const resumeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const tilesRequestedRef = useRef(0)
  const tilesLoadedRef = useRef(0)
  // 圖磚公園/水域 fill-pattern 競態根因修復（見下方 resolveMissingImage 詳細註解）驗證用：記錄
  // resolveMissingImage 實際呼叫 map.addImage() 成功的次數，正常情況下 'cute-tree'／'cute-wave' 各自
  // 全地圖只會需要成功呼叫一次（之後 hasImage() 恆為 true，resolver 會提早 return），透過 __cuteDebug
  // 曝光供 E2E／人工重現時確認「兩個圖案都真的各自掛上去過」，不是量畫面像素以外的另一種佐證方式。
  const patternAddedRef = useRef<Record<TileKind, number>>({ tree: 0, wave: 0 })
  const fpsRef = useRef(0)
  const frameCountRef = useRef(0)
  const fpsWindowStartRef = useRef(0)
  const lastFrameTRef = useRef(0)
  const orbRef = useRef<SoulOrb | null>(null)
  // docs/skins/CUTE_CONTRACT_R2b.md §E「setOverlayHidden(bool)：暫時不畫光點／軌跡／徽章」，只給 E2E 做「有畫 vs
  // 沒畫」前後對照截圖用，預設 false（renderFrame 內 gating，見下方）。這是目前 __cuteDebug 裡唯一
  // 會「改變畫面」的把手（其餘欄位都是唯讀量測值，比照 retro/scifi 的 __retroDebug／__scifiDebug）；
  // FIX（review 抓到的 minor 根因）：純 console 呼叫、沒有任何 gate，理論上任何使用者都能在自己的
  // 瀏覽器主控台把它叫成 true——但這裡刻意不加 NODE_ENV／查詢字串之類的門檻，因為 E2E 驗收腳本本身
  // 就是對 production build（`next start`）呼叫這個把手做「有畫 vs 沒畫」對照（見 docs/skins/CUTE_CONTRACT_R2b.md
  // §5 全文），加上這類 gate 反而會讓正式環境的驗收腳本本身叫不動；真正對應到 review 提出的疑慮
  // 「沒有辦法復原、只能重新整理頁面」則有實質修正：見下方「新一輪開跑自動重置」。
  const overlayHiddenRef = useRef(false)
  const prevStatusRef = useRef<string | undefined>(undefined) // 供下方判斷「是否剛從非 tracking 轉進 tracking」（新一輪開跑）

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
  const appliedPaddingBottomRef = useRef<number | null>(null)
  initialCenterRef.current = initialCenter

  // 統一改變 followingRef 的唯一入口：只在狀態真的翻轉（true↔false）時才呼叫 onFollowChange，避免
  // 呼叫端（8 秒自動恢復計時器、recenter()、使用者手勢 pauseFollow、+/− 按鈕）各自重複觸發
  // page.tsx 不必要的 re-render。只讀寫 followingRef／onFollowChangeRef 兩個穩定 ref，元件生命週期內
  // 可以安全跨 render 重新宣告（跟 recenter/zoomBy 本身一樣）。
  function setFollowing(v: boolean) {
    if (followingRef.current === v) return
    followingRef.current = v
    onFollowChangeRef.current?.(v)
  }

  // 底部可拖曳資訊面板頂端到畫面底的高度 → map.setPadding({bottom})，讓跟隨中心落在面板以上的可見
  // 地圖區正中央（比照 RetroMap.tsx CONTRACT_R2 §2 同一段邏輯）；專注模式改用容器高度的上 45%。
  const applyCutePadding = () => {
    const map = mapRef.current
    if (!map) return
    const container = containerRef.current
    const h = container?.clientHeight || 0
    const bottom = Math.max(0, Math.round(focusModeRef.current ? h * 0.55 : bottomInsetRef.current))
    if (appliedPaddingBottomRef.current === bottom) return
    appliedPaddingBottomRef.current = bottom
    try { map.setPadding({ top: 0, bottom, left: 0, right: 0 }) } catch { /* ignore */ }
  }
  useEffect(() => { applyCutePadding() }) // eslint-disable-line react-hooks/exhaustive-deps -- 每次 render 檢查一次，內部已用 appliedPaddingBottomRef 擋掉沒變化時的重呼叫

  useImperativeHandle(ref, () => ({
    recenter(p) {
      const map = mapRef.current
      if (!map) return
      setFollowing(true)
      if (resumeTimerRef.current) { clearTimeout(resumeTimerRef.current); resumeTimerRef.current = null }
      const target = p || posRef.current
      if (target) {
        recenterZoomPendingRef.current = true // 見上方 RECENTER_ZOOM 宣告處：標記「zoom 還沒收斂」，讓跟隨 effect 之後一起帶 zoom
        try { map.easeTo({ center: [target.lng, target.lat], zoom: RECENTER_ZOOM, pitch: 0, bearing: 0, duration: 500, essential: true }) } catch { /* ignore */ }
      }
    },
    zoomBy(delta: number) {
      const map = mapRef.current
      if (!map) return
      // docs/skins/ORBPOS_CONTRACT.md §A「＋／− 按鈕屬使用者操作，照舊暫停跟隨 8 秒」：這裡呼叫的是
      // 程式 easeTo，觸發的 'zoomstart' 不會帶 originalEvent（見下方 onZoomStart 改法只認使用者手勢），
      // 不會再被事件自動暫停，所以改成直接暫停——邏輯與 mount effect 內的 pauseFollow() 相同，這裡另外
      // 寫一份是因為 zoomBy 定義在 useImperativeHandle，摸不到 mount effect 閉包內的那個函式；
      // followingRef／resumeTimerRef 都是元件層級的 ref，兩處各自操作同一份 ref 沒有問題。
      recenterZoomPendingRef.current = false // 使用者主動縮放：不再幫他把 zoom 拉回 16.5，尊重這次操作（見上方 RECENTER_ZOOM 說明）
      setFollowing(false)
      if (resumeTimerRef.current) clearTimeout(resumeTimerRef.current)
      resumeTimerRef.current = setTimeout(() => { setFollowing(true); resumeTimerRef.current = null }, RESUME_FOLLOW_MS)
      try { map.easeTo({ zoom: map.getZoom() + delta, duration: 250, essential: true }) } catch { /* ignore */ }
    },
  }), [])

  // ── 建圖（僅掛載時一次）：任何例外／不支援／逾時／context lost 一律退回 Leaflet ──────────────────
  // 實例隔離／清理順序比照 RetroMap.tsx／SciFiMap.tsx 同一段的詳細註解。
  useEffect(() => {
    if (!containerRef.current) return
    if (!isWebglSupported()) { onFallbackRef.current('webgl-unsupported'); return }
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
      console.warn('[cute-map] fallback', reason)
      if (rafRef.current != null) { cancelAnimationFrame(rafRef.current); rafRef.current = null }
      detachListeners()
      try { map?.remove() } catch { /* ignore */ }
      if (mapRef.current === map) mapRef.current = null
      try { delete (window as unknown as { __cuteDebug?: unknown }).__cuteDebug } catch { /* ignore */ }
      onFallbackRef.current(reason)
    }

    const onLoad = () => {
      if (!isCurrent()) return
      loadedRef.current = true
      try { containerRef.current?.querySelector('.maplibregl-ctrl-attrib')?.classList.remove('maplibregl-compact-show') } catch { /* ignore */ }
      applyCutePadding()
    }
    const onError = (e: unknown) => {
      if (!isCurrent()) return
      failInstance('map-error:' + ((e as { error?: { message?: string } })?.error?.message || 'unknown'))
    }
    // docs/skins/ORBPOS_CONTRACT.md §A：只有「使用者手勢」才暫停跟隨——dragstart/zoomstart 不分使用者
    // 操作或程式 easeTo 觸發，只有前者的事件物件帶 originalEvent（真實 DOM
    // MouseEvent/TouchEvent/WheelEvent）；程式呼叫 easeTo()（跟隨鏡頭更新、recenter()）觸發的同名事件
    // originalEvent 恆為 undefined，藉此分辨兩種來源，不再無差別暫停跟隨（舊版每次跟隨更新 zoom 回
    // 16.5 都會自己觸發一次 zoomstart，把自己剛恢復的跟隨又暫停掉）。
    const onDragStart = (e: MapMovementEvent) => { if (isCurrent() && e.originalEvent) pauseFollow() }
    const onZoomStart = (e: MapMovementEvent) => { if (isCurrent() && e.originalEvent) pauseFollow() }
    const onClick = (e: MapMouseEvent) => {
      if (!isCurrent()) return
      const list = targetsRef.current
      if (!list?.length) return
      let best: CuteTarget | null = null
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
    // 原創圖塊懶載入：style.ts 只放圖片 id（'cute-tree'/'cute-wave'），實際 ImageData 由 tiles.ts 的
    // tileImageData() 在第一次被要求時產生（map.addImage）；找不到對應 id 就安靜略過。
    //
    // FIX（根因調查，見 scratchpad cute_skin/park_repro/：獨立 harness 用 node_modules/maplibre-gl
    // 原始碼＋真實 openfreemap 圖磚重現，old 寫法連續 15 次每次都有 635/1422 個公園取樣點量到底色
    // 而非圖案色，改用下面這支 resolver 後只剩 1/1422 個點——且那 1 個點在 old／fix 兩版都一樣，是
    // 多邊形邊緣反鋸齒的既有像素級誤差，與這裡要修的競態無關）：舊版監聽 'styleimagemissing' 事件、
    // 在 handler 裡呼叫 map.addImage()，這剛好撞上 MapLibre ImageManager 的一個時序陷阱
    // （node_modules/maplibre-gl/src/render/image_manager.ts _getImagesForIds()）——當
    // missingImageResolver 未設定時，該函式「先」把目前已存在的圖片組成要回傳給圖磚 worker 的
    // response，「最後」才逐一 fire 'styleimagemissing' 通知使用者；即使 handler 在事件觸發當下
    // 同步呼叫 addImage() 把圖片補上，也已經來不及塞進「觸發這次請求的那個圖磚」自己的 response——
    // 而 style.ts 那段「圖片變更→重新請求依賴此圖片的圖磚」的補救機制
    // （Style._updateTilesForChangedImages()）此時也還沒生效，因為這個圖磚的依賴清單要等這次
    // getImages() 呼叫回傳「之後」才登記（Style.getImages() 原始碼：先
    // _updateTilesForChangedImages() 才 tileManager.setDependencies()）。結果：全地圖第一個要求
    // 'cute-tree'／'cute-wave' 的那塊圖磚永遠拿不到該圖片，公園/水域那塊圖磚整片只剩底色（露出下面
    // 的 land background）——而「哪塊圖磚是第一個」取決於瀏覽器實際處理各圖磚 worker 訊息的先後
    // 順序（真實 app 裡跟 React hydration／其他 useEffect／字型與其他資源載入都在搶同一條主執行緒，
    // 時間點會抖動），每次整頁重新整理都可能不同——這正是兩輪重現「破圖的是不同一塊圖磚」的成因
    // （大安森林公園跨兩塊 z14 圖磚，兩塊都會各自第一次要求 'cute-tree'，誰先送出誰就中獎）。
    // 改用 MapLibre 官方文件明確指出「專為執行期動態產生圖片設計、不會有這個時序問題」的
    // map.setMissingStyleImageResolver()（見 maplibre-gl.d.ts 該方法註解：「MapLibre awaits the
    // returned promise before treating the image as missing」）：ImageManager 這條路徑會先 await
    // resolver 執行完（同步呼叫 addImage() 也算數，Promise.allSettled 對非 Promise 值一樣會等一輪
    // microtask），再組 response，同一次請求就能拿到剛補上的圖片，從根本上不會有任何一塊圖磚是
    // 「來不及」的——不是在事件 handler 裡加時機判斷這種治標寫法。
    const resolveMissingImage: MissingStyleImageResolver = (id) => {
      if (!isCurrent()) return
      if (map!.hasImage(id)) return
      const kind = (Object.keys(STYLE_IMAGE_IDS) as TileKind[]).find((k) => STYLE_IMAGE_IDS[k] === id)
      if (!kind || !TILE_IDS.includes(kind)) return
      try { map!.addImage(id, tileImageData(kind)); patternAddedRef.current[kind] += 1 } catch { /* ignore：單一圖塊掛不上不影響其他圖層 */ }
    }

    function pauseFollow() {
      setFollowing(false)
      if (resumeTimerRef.current) clearTimeout(resumeTimerRef.current)
      resumeTimerRef.current = setTimeout(() => { setFollowing(true); resumeTimerRef.current = null }, RESUME_FOLLOW_MS)
    }

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

      // 靈魂光點：純幾何/漸層繪製，無需非同步載入任何素材，掛載時直接建立一個實例即可（比舊版角色
      // SVG 離屏 canvas 的非同步載入簡單很多，不再需要「載入完成前安靜略過幾幀」的處理）。
      orbRef.current = new SoulOrb()

      map = new MapLibreMap({
        container: containerRef.current,
        style: buildCuteStyle() as unknown as MapOptions['style'],
        center: [initialCenter[1], initialCenter[0]] as LngLatLike,
        zoom: initialZoom ?? 16.5,
        pitch: 0,
        bearing: 0,
        maxPitch: 0,
        attributionControl: false,
        // 中文地標／公園名稱走本地字型繪製（不需要 CJK glyph 圖磚）：契約要求 localIdeographFontFamily
        // 以 'DORCute' 開頭，字型尚未載入或子集外字元時瀏覽器自然 fallback 到後面的通用中文字體。
        localIdeographFontFamily: "'DORCute','Noto Sans TC','Microsoft JhengHei',sans-serif",
      } as MapOptions)
      mapRef.current = map
      // 必須在任何圖磚有機會被請求之前（=建構後立刻）就設好，見上方 resolveMissingImage 根因說明——
      // 同步呼叫、JS 單執行緒，這行執行完成前不可能有 tile worker 訊息插進來搶跑。
      map.setMissingStyleImageResolver(resolveMissingImage)

      timeoutId = setTimeout(() => { if (!loadedRef.current) failInstance('load-timeout-8s') }, FALLBACK_TIMEOUT_MS)

      map.on('load', onLoad)
      map.on('error', onError)
      map.on('dragstart', onDragStart)
      map.on('zoomstart', onZoomStart)
      map.on('click', onClick)
      map.on('dataloading', onDataLoading as never)
      map.on('data', onData as never)
      try { map.getCanvas().addEventListener('webglcontextlost', onContextLost) } catch { /* ignore */ }

      ro = new ResizeObserver(() => { if (isCurrent()) { try { map!.resize() } catch { /* ignore */ }; applyCutePadding() } })
      ro.observe(containerRef.current)

      startLoop()
    } catch (e) {
      failInstance('init-exception:' + ((e as Error)?.message || String(e)))
    }
    return () => {
      cancelled = true
      if (timeoutId) clearTimeout(timeoutId)
      if (resumeTimerRef.current) { clearTimeout(resumeTimerRef.current); resumeTimerRef.current = null }
      if (rafRef.current != null) cancelAnimationFrame(rafRef.current)
      ro?.disconnect()
      detachListeners()
      try { map?.remove() } catch { /* ignore */ }
      if (mapRef.current === map) mapRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 只在掛載時建圖一次
  }, [])

  // __cuteDebug 偵錯把手（CONTRACT_R2.md §4.6）：每秒更新一次。
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
      const canvas = map.getCanvas()
      let trailPoints = 0
      for (const seg of segmentsRef.current) trailPoints += seg?.length || 0
      // docs/skins/CUTE_CONTRACT_R2b.md §E「kmBadgeScreens：目前畫出的每個公里徽章在地圖容器內的 CSS px 位置」——
      // 與 drawKmMarks() 用同一個 map.project()，量到的就是實際畫上去的那個位置（不是另外重算一套）。
      const kmBadgeScreens = (kmMarksRef.current || []).map((m) => {
        try {
          const pt = map.project([m.lng, m.lat])
          return { km: m.km, x: Math.round(pt.x), y: Math.round(pt.y) }
        } catch { return { km: m.km, x: 0, y: 0 } }
      })
      // docs/skins/CUTE_CONTRACT_R2b.md §E「trailScreenSample：軌跡上 5 個取樣點的 CSS px 位置」——攤平所有分段後
      // 等距抽 5 個點（含頭尾），與 drawRoute() 同一個 map.project()。
      const flatTrail: [number, number][] = []
      for (const seg of segmentsRef.current) for (const pt of seg || []) flatTrail.push(pt)
      const trailScreenSample: { x: number; y: number }[] = []
      if (flatTrail.length) {
        const nSample = Math.min(5, flatTrail.length)
        for (let i = 0; i < nSample; i++) {
          const idx = nSample === 1 ? 0 : Math.round((i * (flatTrail.length - 1)) / (nSample - 1))
          try {
            const pt = map.project([flatTrail[idx][1], flatTrail[idx][0]])
            trailScreenSample.push({ x: Math.round(pt.x), y: Math.round(pt.y) })
          } catch { /* ignore */ }
        }
      }
      ;(window as unknown as { __cuteDebug?: unknown }).__cuteDebug = {
        center: { lat: c.lat, lng: c.lng },
        zoom: map.getZoom(),
        pitch: map.getPitch(),
        bearing: map.getBearing(),
        // ORBPOS_CONTRACT.md 第二輪驗收12：直接讀 MapLibre 自己的 getPadding()（applyCutePadding()
        // 呼叫 setPadding 的權威回讀），讓 E2E 能算出「可見區（面板以上那塊）中心」的 CSS px，不必自己
        // 另外猜測 bottomInset 換算後的實際生效值。
        padding: map.getPadding(),
        lastPos: lastPosRef.current ? { lat: lastPosRef.current.lat, lng: lastPosRef.current.lng } : null,
        tilesRequested: tilesRequestedRef.current,
        tilesLoaded: tilesLoadedRef.current,
        // 沒有真實 GPS 定位時 renderFrame 完全不畫光點（CONTRACT_R2.md §4.2「沒有定位時不畫」，見
        // renderFrame）；orbVisible 如實反映這件事，不是恆為 true（FIX round2 修正第一版的退回
        // initialCenter 恆顯示行為）。
        orbVisible: !!p,
        orbScreen,
        // docs/skins/ORBPOS_CONTRACT.md 偵錯把手：orbLatLng＝實際拿來畫光點的座標（一律最新定位，不
        // 凍結）；lastFixAgeMs／orbState 用 renderFrame 同一組 getFixAgeMs／isOrbSearching 判定；
        // following＝目前是否處於跟隨狀態；projectLngLat 讓 E2E 直接驗證任意座標的 CSS px 投影是否與
        // 光點實際畫的位置一致。
        orbLatLng: p ? { lat: p.lat, lng: p.lng } : null,
        lastFixAgeMs: p ? getFixAgeMs(p) : null,
        orbState: p ? (isOrbSearching(p) ? 'searching' : 'normal') : null,
        following: followingRef.current,
        projectLngLat: (lng: number, lat: number) => {
          try { const pt = map.project([lng, lat]); return { x: pt.x, y: pt.y } } catch { return null }
        },
        trailPoints,
        kmBadges: kmMarksRef.current?.length || 0,
        kmBadgeScreens,
        trailScreenSample,
        particles: orbRef.current?.particleCount ?? 0,
        fps: fpsRef.current,
        patternImages: { tree: map.hasImage('cute-tree'), wave: map.hasImage('cute-wave') },
        // 見上方 patternAddedRef 宣告處：正常應恆為 {tree:0或1, wave:0或1}（該圖案根本沒被任何圖磚
        // 用到就是 0；用到了但只成功掛一次是 1）；若某次重現又看到 >1，代表 hasImage() 提早 return
        // 的防重掛判斷失效，是另一個問題的訊號。
        patternAddCount: { ...patternAddedRef.current },
        canvasSize: { w: canvas.width, h: canvas.height, cssW: canvas.clientWidth, cssH: canvas.clientHeight },
        fontReady: (typeof document !== 'undefined' && document.fonts) ? document.fonts.check('16px DORCute') : null,
        // docs/skins/CUTE_CONTRACT_R2b.md §E：只給 E2E 做「有畫 vs 沒畫」前後對照用，預設 false。
        setOverlayHidden: (v: boolean) => { overlayHiddenRef.current = !!v },
      }
    }, 1000)
    return () => {
      clearInterval(id)
      try { delete (window as unknown as { __cuteDebug?: unknown }).__cuteDebug } catch { /* ignore */ }
    }
  }, [])

  useEffect(() => {
    if (status !== 'tracking') lastMovingAtRef.current = 0
    // FIX（review 抓到的 minor 根因：setOverlayHidden(true) 沒有 gate、被叫了之後「只能重新整理
    // 頁面」才能復原）：每次從非 tracking 狀態「新」轉進 tracking（=開始新的一趟跑步）時，把
    // overlayHiddenRef 強制歸回 false——確保就算上一輪（或任何來源）曾經把它叫成 true 卡住，新開始
    // 的一趟跑步一定看得到光點／軌跡／徽章，不需要整頁重新整理。只在「轉進」那一刻重置（比對
    // prevStatusRef），同一次 tracking 期間中途的呼叫（E2E 對照用）不受影響，不會打斷驗收腳本
    // 在同一次追蹤過程中連續呼叫 setOverlayHidden(true)/(false) 做前後對照。
    if (status === 'tracking' && prevStatusRef.current !== 'tracking') {
      overlayHiddenRef.current = false
    }
    prevStatusRef.current = status
  }, [status])

  // GPS 位置更新：跟隨鏡頭（center 平移，pitch/bearing 恆 0）＋判斷是否為真實移動（供光點呼吸快慢/
  // 粒子冒出判斷用，見上方 moveAnchorRef／lastMovingAtRef 宣告處的根因說明）。
  useEffect(() => {
    if (!pos) return
    writeLastPos(pos.lat, pos.lng)
    const now = performance.now()
    const anchor = moveAnchorRef.current
    // 位移 ≥ MOVE_NOISE_FLOOR_M 才算「真實移動」（見上方 moveAnchorRef／lastMovingAtRef 宣告處的根因
    // 說明）；第一個基準點只是起點，不算移動。抖動範圍內的座標忽略、基準點與 lastMovingAtRef 都不
    // 動——由 renderFrame 每幀重新評估「距上次真實移動是否已超過 STILL_TIMEOUT_MS」來決定要不要
    // 繼續冒粒子／加快脈動，時間本身會讓它自然衰減，不需要新的 pos 進來才能歸零。
    if (!anchor || haversineM([anchor.lat, anchor.lng], [pos.lat, pos.lng]) >= MOVE_NOISE_FLOOR_M) {
      if (anchor) lastMovingAtRef.current = now
      moveAnchorRef.current = { lat: pos.lat, lng: pos.lng }
    }
    lastPosRef.current = { lat: pos.lat, lng: pos.lng, t: now }
    const map = mapRef.current
    if (map && followingRef.current) {
      // docs/skins/ORBPOS_CONTRACT.md §A：跟隨更新只搬 center，不再強制 zoom:16.5——使用者縮放過一次
      // 之後，跟隨鏡頭若仍寫死 zoom 會持續把剛設定好的縮放層級彈回去（與「保留使用者目前的縮放」牴觸），
      // 縮放要恢復預設值只透過 recenter()（下方「回到目前位置」）。pitch/bearing 對 cute 恆為 0
      // （maxPitch:0，本檔從不改動 bearing），繼續帶著寫死不影響行為，維持原樣。
      const followOpts: { center: [number, number]; pitch: number; bearing: number; duration: number; essential: true; zoom?: number } =
        { center: [pos.lng, pos.lat], pitch: 0, bearing: 0, duration: 900, essential: true }
      // 見上方 RECENTER_ZOOM／recenterZoomPendingRef 宣告處的根因說明：recenter() 剛按下、zoom 還沒
      // 收斂到 16.5 前，這裡也一起帶 zoom，即使被打斷也不會凍結在半路；量到已經夠接近目標就清旗標，
      // 之後恢復「只搬 center」。
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
    console.warn('[cute-map] fallback', reason)
    if (rafRef.current != null) { cancelAnimationFrame(rafRef.current); rafRef.current = null }
    try { detachListenersRef.current() } catch { /* ignore */ }
    try { mapRef.current?.remove() } catch { /* ignore */ }
    mapRef.current = null
    try { delete (window as unknown as { __cuteDebug?: unknown }).__cuteDebug } catch { /* ignore */ }
    onFallbackRef.current(reason)
  }

  function renderFrame(map: MapLibreMap, dt: number) {
    const canvas = canvasRef.current
    if (!canvas) return
    const dpr = window.devicePixelRatio || 1
    // docs/skins/ORBPOS_CONTRACT.md §D：疊層畫布的座標系以 MapLibre 自己的 canvas 尺寸為準（不是
    // container 的 clientWidth/clientHeight）——兩者理論上同步，但 resize 節流／版面剛切換的瞬間可能
    // 短暫不同步，project() 用的是 map 自己的 transform，這裡跟著它才能保證每一幀都對得上。
    const mapCanvas = map.getCanvas()
    const w = mapCanvas.clientWidth, h = mapCanvas.clientHeight
    if (w <= 0 || h <= 0) return
    const wantW = Math.round(w * dpr), wantH = Math.round(h * dpr)
    if (canvas.width !== wantW || canvas.height !== wantH) { canvas.width = wantW; canvas.height = wantH }
    const ctx = canvas.getContext('2d')
    if (!ctx) return
    ctx.imageSmoothingEnabled = true // 手繪可愛風要平滑，不像 retro 走像素風
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, w, h)

    // docs/skins/CUTE_CONTRACT_R2b.md §E「setOverlayHidden(bool)：暫時不畫光點／軌跡／徽章」：只給 E2E 做「有畫 vs
    // 沒畫」前後對照截圖用（同一畫面各截一張比對 ΔE），target 圖示（打卡點/賽事目標）不在契約列舉
    // 範圍內，維持照常繪製。
    const overlayHidden = overlayHiddenRef.current
    if (!overlayHidden) {
      drawRoute(ctx, map, segmentsRef.current)
      drawKmMarks(ctx, map, kmMarksRef.current)
    }
    drawTargets(ctx, map, targetsRef.current)

    // 光點目前位置：跑步前（idle）也顯示於目前位置（靜態呼吸）；沒有真實 GPS 定位時完全不畫
    // （CONTRACT_R2.md §4.2 逐字「沒有定位時不畫」——FIX round2 review 抓到前一版仍退回 initialCenter
    // 畫一顆假光點，與契約字面牴觸，這裡改成沒有 p 就整段跳過，不畫任何東西）。update() 仍要照常
    // 呼叫（讓粒子計時/星星角度持續推進，overlayHidden 只影響「畫不畫出來」，不影響狀態本身，恢復
    // 顯示時才不會看到粒子瞬間跳一大段）。
    const p = posRef.current
    if (p) {
      const pt = map.project([p.lng, p.lat])
      // 移動判定：距上次真實移動（見 moveAnchorRef／lastMovingAtRef 根因說明）是否還在
      // STILL_TIMEOUT_MS 之內；用 render 迴圈當下的 performance.now() 重新評估，不是只在 pos effect
      // 觸發當下算一次，這樣時間流逝本身就會讓「移動中」自然衰減回靜止。
      const moving = statusRef.current === 'tracking' && lastMovingAtRef.current > 0 && (performance.now() - lastMovingAtRef.current) < STILL_TIMEOUT_MS
      // docs/skins/ORBPOS_CONTRACT.md §B：一律用最新定位畫光點（p 就是 posRef.current 這個最新值，不
      // 凍結在舊點）；精度差／久未更新時改成「搜尋中」外觀，半透明本體＋依 acc 換算的精度圈（見
      // orb.ts SoulOrbDrawOpts.searching／accuracyPx，換算沿用 onClick 已在用的同一個 metersPerPixel）。
      const searching = isOrbSearching(p)
      const mpp = metersPerPixel(p.lat, map.getZoom())
      const accuracyPx = typeof p.acc === 'number' && mpp > 0 ? Math.min(120, p.acc / mpp) : 40
      const orb = orbRef.current
      if (orb) {
        orb.update(dt, moving, reducedMotionRef.current, pt.x, pt.y)
        if (!overlayHidden) orb.draw(ctx, pt.x, pt.y, { moving, reducedMotion: reducedMotionRef.current, searching, accuracyPx })
      }
    }
  }

  // docs/skins/ORBPOS_CONTRACT.md §B：最新定位距今幾毫秒沒有更新——有 pos.ts（PAGE 工人新增於
  // scifi/types.ts SciFiPos，epoch ms，本檔不擁有那份型別檔，用安全的執行期存取避免在對方欄位進版前
  // 就編譯失敗）就用它；還沒有這個欄位時退回「pos 最後一次真的改變的時間」（lastPosRef.current.t，
  // performance.now() 時鐘）。兩種時鐘各自在自己的分支內使用，不互相比較。
  function getFixAgeMs(p: CutePos | null): number {
    if (!p) return Infinity
    const ts = (p as unknown as { ts?: number }).ts
    if (typeof ts === 'number' && Number.isFinite(ts)) return Math.max(0, Date.now() - ts)
    const last = lastPosRef.current
    return last ? Math.max(0, performance.now() - last.t) : 0
  }

  // 「搜尋中」＝精度差於 MAX_ORB_ACC 或最新定位已超過 STALE_FIX_MS 沒更新（契約 §B 逐字）。
  function isOrbSearching(p: CutePos | null): boolean {
    if (!p) return false
    if (typeof p.acc === 'number' && p.acc > MAX_ORB_ACC) return true
    return getFixAgeMs(p) > STALE_FIX_MS
  }

  // 軌跡＝光點走過的路（CONTRACT_R2.md §4.3）：發光緞帶＝外層柔光（寬 14px、candy 30% 透明、
  // shadowBlur 模糊）＋主線（寬 6px，由 lavender 漸變到 candy，強化「光點拖出來的軌跡」感）
  // ＋內芯（寬 2px，純白提亮）。訊號中斷分段（segs 陣列本身已依 `;` 分段）照舊不相連，只換樣式。
  //
  // FIX（review 抓到的 major/minor 兩項根因，一併修正）：
  // 1) major／效能：舊版主線是對每個相鄰點各自 beginPath()/moveTo()/lineTo()/stroke()——
  //    decimate 上限 600 點時，一個分段每一幀最多 600 次個別 canvas draw call，長路線/多次訊號
  //    中斷分段時會線性暴增（rAF 每秒最多呼叫 30 次，長時間持續消耗）。改成跟外層柔光／內芯同一種
  //    寫法：一個分段一次 beginPath + 多次 lineTo + 一次 stroke()，draw call 數從 O(n) 降到
  //    O(分段數)（多數時間只有 1 段）。
  // 2) minor／視覺一致性：舊版 total/cum（累積弧長）在 segs.forEach 內逐段分開算，訊號中斷後第 2
  //    段以後的漸層會從 lavender 重新開始，不是契約§C「起點→光點」一路連續的漸層。改用 canvas
  //    CanvasGradient（依螢幕座標位置決定顏色，不是依單一分段的弧長比例）：起訖端點固定為「整條
  //    路線最早一點」與「目前光點」，同一個漸層物件給所有分段共用的主線 stroke 使用，訊號中斷處
  //    不會重新起算。
  function drawRoute(ctx: CanvasRenderingContext2D, map: MapLibreMap, segs: [number, number][][]) {
    if (!segs?.length) return
    const p = posRef.current
    // 軌跡末端只在目前定位精度可信時才延伸連到光點——FIX round2 review 抓到根因：p 是完全未經精度
    // 過濾的即時定位（供地圖跟隨用，見 track/page.tsx mapSnapshot.pos），訊號變差時（建物旁/室內）
    // 會跳到一個誤差可能達數十甚至數百公尺的瞬時座標；若無條件把軌跡末端接過去，會在畫面上拉出一條
    // 與實際路徑無關、之後又彈回來的橡皮筋線。門檻沿用 track/page.tsx MAX_ACC=65m 同一慣例（本檔
    // 獨立宣告一份數字，不 import page.tsx，理由同檔頭其他常數）；acc 缺省/為 0 時視為可信，維持
    // 原本「一律連接」的行為。
    const pAccOk = !!p && (p.acc == null || p.acc === 0 || p.acc <= TRAIL_CONNECT_MAX_ACC)

    // 主線漸層端點（螢幕座標，見上方 FIX 2 說明）：起點＝整條路線最早一點（與下方起點小圓點同一個
    // 點）；終點＝目前光點（精度可信時，跟 drawOrb 同一個 map.project）或退而求其次用最後一段最後
    // 一點。project() 理論上不會丟例外（純數學投影），但仍包一層 try/catch 保守以對——任何失敗就
    // 讓 trailGradient 安靜退回純色 TRAIL_END，不影響其餘畫面。
    let globalStartPt: { x: number; y: number } | null = null
    let globalEndPt: { x: number; y: number } | null = null
    try {
      const firstRaw = segs[0]?.[0]
      if (firstRaw) globalStartPt = map.project([firstRaw[1], firstRaw[0]])
      if (p && pAccOk) {
        globalEndPt = map.project([p.lng, p.lat])
      } else {
        for (let i = segs.length - 1; i >= 0 && !globalEndPt; i--) {
          const lastRaw = segs[i]?.[segs[i].length - 1]
          if (lastRaw) globalEndPt = map.project([lastRaw[1], lastRaw[0]])
        }
      }
    } catch { /* ignore：退回下面純色 fallback */ }
    const trailGradient: string | CanvasGradient = (globalStartPt && globalEndPt)
      ? (() => {
          const g = ctx.createLinearGradient(globalStartPt!.x, globalStartPt!.y, globalEndPt!.x, globalEndPt!.y)
          g.addColorStop(0, TRAIL_START)
          g.addColorStop(1, TRAIL_END)
          return g
        })()
      : TRAIL_END

    segs.forEach((seg, segIdx) => {
      if (!seg || seg.length < 2) return
      const pts = seg.length > 600 ? decimate(seg, 600) : seg
      const screen = pts.map((pp) => map.project([pp[1], pp[0]]))
      // 軌跡末端要接到光點目前位置，不能與光點脫節：只在「最後一段」且精度可信時補上光點的螢幕座標
      // （其餘段落是訊號中斷前的舊分段，不該接到目前位置）。
      if (segIdx === segs.length - 1 && p && pAccOk) {
        const orbPt = map.project([p.lng, p.lat])
        const last = screen[screen.length - 1]
        if (Math.hypot(orbPt.x - last.x, orbPt.y - last.y) > 0.5) {
          screen.push(orbPt)
        }
      }
      ctx.save()
      ctx.lineJoin = 'round'; ctx.lineCap = 'round'
      // 外層柔光（docs/skins/CUTE_CONTRACT_R2b.md §C：寬約 14px、`#ff6fae` 30%——比 R2 第一版的 11px/candy 30%
      // 更寬更深，退淡後的地圖底色下才夠顯眼）。
      ctx.shadowColor = 'rgba(255,111,174,.35)'
      ctx.shadowBlur = 10
      ctx.strokeStyle = 'rgba(255,111,174,.3)'
      ctx.lineWidth = 14
      ctx.beginPath()
      screen.forEach((pt, i) => (i === 0 ? ctx.moveTo(pt.x, pt.y) : ctx.lineTo(pt.x, pt.y)))
      ctx.stroke()
      ctx.shadowBlur = 0
      // 主線：寬 6px，單一漸層（見上方 trailGradient 計算處）＋單一 stroke（FIX：取代舊版逐點
      // beginPath/stroke 迴圈）。
      ctx.strokeStyle = trailGradient
      ctx.lineWidth = 6
      ctx.beginPath()
      screen.forEach((pt, i) => (i === 0 ? ctx.moveTo(pt.x, pt.y) : ctx.lineTo(pt.x, pt.y)))
      ctx.stroke()
      // 內芯提亮：2px 純白（docs/skins/CUTE_CONTRACT_R2b.md §C 逐字「內芯 2px 白色」，取代 R2 第一版偏粉的 #fff0f6）。
      ctx.strokeStyle = TRAIL_CORE
      ctx.lineWidth = 2
      ctx.beginPath()
      screen.forEach((pt, i) => (i === 0 ? ctx.moveTo(pt.x, pt.y) : ctx.lineTo(pt.x, pt.y)))
      ctx.stroke()
      ctx.restore()
    })
    // 起點小圓點（原創、非必要裝飾）：畫在第一段的第一個點。
    const first = segs[0]?.[0]
    if (first) {
      const startPt = map.project([first[1], first[0]])
      drawStartDot(ctx, startPt.x, startPt.y)
    }
  }

  // FIX round2b §D 根因調查（編排者放大 4_track_running.png 抓到「kmBadges=1 但畫面上完全看不到」）：
  // 這裡的 map.project() + drawKmHeartBadge() 呼叫本身沒有錯——用 scratchpad cute_skin/
  // mapfix_repro.mjs 實測同一個 1.23km 直線測試路線／zoom16.5，__cuteDebug.kmBadgeScreens[0] 量到
  // 的螢幕座標（相對地圖容器 y≈455／換算成整個頁面的絕對座標 y≈514）確實對應地圖上正確的公里1
  // 位置，尺寸也是 30px（非 0）；把該座標裁切放大直接看到的卻是「距離／時間／配速」資訊面板的文字
  // ——真正的根因是這個座標落在底部可拖曳資訊面板（z-index 500，DOM 順序疊在本檔 canvas 之上）的
  // 「面板頂端 y」（同一次量測 y≈365）以下 149px，也就是徽章被蓋住，不是沒畫出來。成因：
  // applyCutePadding()（上方，約 121-130 行）用 map.setPadding({bottom: bottomInset}) 讓跟隨中心
  // （目前位置）落在面板以上那塊可見區的正中央；可見區在這個視窗高度下只有約 306px 高，目前位置
  // 因此固定落在可見區「上緣以下 153px」處——任何比目前位置早超過「153px 螢幕距離」的軌跡點（這條
  // 測試路線的公里1恰好落在目前位置往回約 303px 處）幾何上就一定落到可見區以外、被面板蓋住，跟
  // 徽章本身的顏色/尺寸無關（實測：把鏡頭縮小兩級，換算後公里1回到可見區內，同一顆 30px 愛心立刻
  // 清楚可見，見 mapfix_zoom.png）。這個「目前位置置中、可見區只有容器一半高」的 padding／
  // 跟隨邏輯是三套風格（scifi/retro/cute）共用且契約明訂沿用不動的既有設計（CONTRACT_R2.md
  // §4.2「沿用現有 follow／padding 邏輯」），因此這裡不改動相機/padding 行為，只把徽章本體做得更大
  // 更醒目（見 icons.ts drawKmHeartBadge 30px＋#ff6fae 描邊＋陰影）；真正保證「公里徽章在測試截圖
  // 裡看得到」要靠驗收腳本挑選會讓公里點落在可見區內的路線/鏡頭（docs/skins/CUTE_CONTRACT_R2b.md §5-4 原文「必要時
  // 把鏡頭縮小一級或路線轉彎」），不是地圖繪製程式碼能單方面保證的事（公里標記是固定地理位置，跟隨
  // 相機一直在動，兩者關係本來就會隨著跑者跑多遠而改變）。
  function drawKmMarks(ctx: CanvasRenderingContext2D, map: MapLibreMap, marks: { km: number; lat: number; lng: number }[]) {
    if (!marks?.length) return
    for (const m of marks) {
      const pt = map.project([m.lng, m.lat])
      drawKmHeartBadge(ctx, pt.x, pt.y, m.km, 1)
    }
  }

  function drawTargets(ctx: CanvasRenderingContext2D, map: MapLibreMap, tgts: CuteTarget[]) {
    if (!tgts?.length) return
    for (const tgt of tgts) {
      const ring = geoCircle(tgt.lat, tgt.lng, Math.max(4, tgt.radius)).map((p) => map.project([p[1], p[0]]))
      const center = map.project([tgt.lng, tgt.lat])
      ctx.save()
      ctx.strokeStyle = tgt.done ? '#f6b73c' : TRAIL_END
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
    <div style={{ position: 'absolute', inset: 0 }}>
      <div ref={containerRef} className="cute-map" style={{ position: 'absolute', inset: 0 }} />
      {/* docs/skins/ORBPOS_CONTRACT.md ★：<canvas> 是可替換元素（replaced element，跟 <img> 同一類），
          position:absolute+inset:0 不會撐滿容器，沒有明確 width/height 時退回內在尺寸（=canvas.width/
          height 這兩個 HTML attribute，即 renderFrame() 設的 backing pixel 尺寸）；DPR>1 手機上整層
          疊層因此放大偏移（根因與詳細說明見 track/retro/RetroMap.tsx 同一處註解，retro 已修）。 */}
      <canvas ref={canvasRef} style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', pointerEvents: 'none', display: 'block' }} />
    </div>
  )
})

export default CuteMap
