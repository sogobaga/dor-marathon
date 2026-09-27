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
// ＋canvas image-rendering:pixelated 做出「地圖本體」的像素化效果；勇者不是粒子而是逐像素繪製的
// 四方向小人（sprites.ts），身後留腳印、已跑路線畫成金色虛線「足跡道路」。

import { forwardRef, useEffect, useImperativeHandle, useRef } from 'react'
import { Map as MapLibreMap, config as maplibreConfig, type MapOptions, type LngLatLike, type MapMouseEvent } from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { buildRetroStyle } from './style'
import { tileImageData, drawHero, FootprintPool, drawKmFlag, drawTargetIcon, type HeroDir } from './sprites'
import type { TileKind } from '@/components/retro/tiles'
import { isWebglSupported, haversineM, metersPerPixel, geoCircle, decimate } from './geo'
import type { RetroMapHandle, RetroMapProps, RetroTarget } from './types'

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
const FOOTPRINT_STEP_M = 6 // 每 6 公尺留一個腳印
const WALK_SPEED_MPS = 0.8 // 速度 >0.8 m/s 才播走路動畫，否則站立

function writeLastPos(lat: number, lng: number) {
  try { localStorage.setItem(LAST_POS_KEY, JSON.stringify({ lat, lng })) } catch { /* 私密瀏覽/storage 被封鎖：純錦上添花，略過即可 */ }
}

const RetroMap = forwardRef<RetroMapHandle, RetroMapProps>(function RetroMap(props, ref) {
  const { pos, status, segments, kmMarks, targets, focusMode, initialCenter, initialZoom, onFallback, onTargetClick, bottomInset } = props

  const wrapRef = useRef<HTMLDivElement | null>(null)
  const containerRef = useRef<HTMLDivElement | null>(null)
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const mapRef = useRef<MapLibreMap | null>(null)
  const rafRef = useRef<number | null>(null)
  const detachListenersRef = useRef<() => void>(() => {})
  const failedRef = useRef(false)
  const loadedRef = useRef(false)
  const followingRef = useRef(true)
  const footprintsRef = useRef<FootprintPool>(new FootprintPool(12))
  const lastFootprintPosRef = useRef<{ lat: number; lng: number } | null>(null)
  const lastPosRef = useRef<{ lat: number; lng: number; t: number } | null>(null)
  const heroDirRef = useRef<HeroDir>('down')
  const walkFrameRef = useRef<0 | 1 | 2>(0) // CONTRACT_R2 §2：0=站立、1/2=兩幀走路（左/右腳），三幀皆相異
  const walkFrameTRef = useRef(0)
  const movingTRef = useRef(0)
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

  const posRef = useRef(pos); posRef.current = pos
  const statusRef = useRef(status); statusRef.current = status
  const segmentsRef = useRef(segments); segmentsRef.current = segments
  const kmMarksRef = useRef(kmMarks); kmMarksRef.current = kmMarks
  const targetsRef = useRef(targets); targetsRef.current = targets
  const focusModeRef = useRef(focusMode); focusModeRef.current = focusMode
  const onFallbackRef = useRef(onFallback); onFallbackRef.current = onFallback
  const onTargetClickRef = useRef(onTargetClick); onTargetClickRef.current = onTargetClick
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
  // transform，之後 easeTo({center}) 與既有的 map.project() 畫勇者都自動反映新的可見區中心，
  // 不需要另外調整勇者的畫法。專注模式開啟時可見區＝容器高度的上 45%（RaceFocusMode retro
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

  useImperativeHandle(ref, () => ({
    recenter(p) {
      const map = mapRef.current
      if (!map) return
      followingRef.current = true
      if (resumeTimerRef.current) { clearTimeout(resumeTimerRef.current); resumeTimerRef.current = null }
      const target = p || posRef.current
      if (target) { try { map.easeTo({ center: [target.lng, target.lat], zoom: 16.5, pitch: 0, bearing: 0, duration: 500, essential: true }) } catch { /* ignore */ } }
    },
    zoomBy(delta: number) {
      const map = mapRef.current
      if (!map) return
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
    const onDragStart = () => { if (isCurrent()) pauseFollow() }
    const onZoomStart = () => { if (isCurrent()) pauseFollow() }
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
    const TILE_IDS: readonly TileKind[] = ['grass', 'forest', 'water', 'wall', 'cobble', 'path', 'sand', 'rail']
    const onImageMissing = (e: { id: string }) => {
      if (!isCurrent()) return
      const id = e.id as TileKind
      if (map!.hasImage(id)) return
      if (!TILE_IDS.includes(id)) return
      try {
        if (id === 'water') { map!.addImage(id, tileImageData('water', 0)); waterFrameRef.current = 0 }
        else map!.addImage(id, tileImageData(id))
      } catch { /* ignore：單一圖塊掛不上不影響其他圖層 */ }
    }

    function pauseFollow() {
      followingRef.current = false
      if (resumeTimerRef.current) clearTimeout(resumeTimerRef.current)
      resumeTimerRef.current = setTimeout(() => { followingRef.current = true; resumeTimerRef.current = null }, RESUME_FOLLOW_MS)
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
        map?.off('styleimagemissing', onImageMissing as never)
        map?.getCanvas().removeEventListener('webglcontextlost', onContextLost)
      } catch { /* ignore */ }
    }
    detachListenersRef.current = detachListeners

    try {
      reducedMotionRef.current = typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
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

      timeoutId = setTimeout(() => { if (!loadedRef.current) failInstance('load-timeout-8s') }, FALLBACK_TIMEOUT_MS)

      map.on('load', onLoad)
      map.on('error', onError)
      map.on('dragstart', onDragStart)
      map.on('zoomstart', onZoomStart)
      map.on('click', onClick)
      map.on('dataloading', onDataLoading as never)
      map.on('data', onData as never)
      map.on('styleimagemissing', onImageMissing as never)
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

  // __retroDebug 偵錯把手（CONTRACT.md §4）：每秒更新一次。
  useEffect(() => {
    const id = setInterval(() => {
      const map = mapRef.current
      if (!map) return
      let heroScreen = { x: 0, y: 0 }
      try {
        const p = posRef.current
        const proj = p ? map.project([p.lng, p.lat]) : map.project([initialCenterRef.current[1], initialCenterRef.current[0]])
        heroScreen = { x: Math.round(proj.x), y: Math.round(proj.y) }
      } catch { /* ignore */ }
      const c = map.getCenter()
      // 根因調查暫時欄位（CONTRACT_R2 §1）：patternImages/canvasSize，抓 addImage 是否真的掛上、
      // 容器尺寸是否為 0——調查完成後視情況精簡。
      const TILE_IDS = ['grass', 'forest', 'water', 'wall', 'cobble', 'path', 'sand', 'rail'] as const
      const patternImages = Object.fromEntries(TILE_IDS.map((k) => [k, map.hasImage(k)]))
      const canvas = map.getCanvas()
      ;(window as unknown as { __retroDebug?: unknown }).__retroDebug = {
        center: { lat: c.lat, lng: c.lng },
        zoom: map.getZoom(),
        pitch: map.getPitch(),
        bearing: map.getBearing(),
        lastPos: lastPosRef.current ? { lat: lastPosRef.current.lat, lng: lastPosRef.current.lng } : null,
        tilesRequested: tilesRequestedRef.current,
        tilesLoaded: tilesLoadedRef.current,
        heroScreen,
        heroDir: heroDirRef.current,
        fps: fpsRef.current,
        patternImages,
        canvasSize: { w: canvas.width, h: canvas.height, cssW: canvas.clientWidth, cssH: canvas.clientHeight },
      }
    }, 1000)
    return () => {
      clearInterval(id)
      try { delete (window as unknown as { __retroDebug?: unknown }).__retroDebug } catch { /* ignore */ }
    }
  }, [])

  // 跑步狀態離開 tracking：重置走路動畫/方向到站立、清腳印起點鎖存（不影響既有 pointsRef 等跑步狀態）。
  useEffect(() => {
    if (status !== 'tracking') {
      movingTRef.current = 0
      lastFootprintPosRef.current = null
    }
  }, [status])

  // GPS 位置更新：跟隨鏡頭（center 平移，pitch/bearing 恆 0）＋算移動速度（供走路動畫/腳印間距）＋
  // 依經緯度位移的主軸方向決定勇者面向（不依賴地圖 bearing，因為本風格鏡頭永遠正北朝上）。
  useEffect(() => {
    if (!pos) return
    writeLastPos(pos.lat, pos.lng)
    const now = performance.now()
    const prev = lastPosRef.current
    const map = mapRef.current
    if (prev) {
      const dtS = Math.max(0.05, (now - prev.t) / 1000)
      const dM = haversineM([prev.lat, prev.lng], [pos.lat, pos.lng])
      const speed = dM / dtS
      movingTRef.current = Math.max(0, Math.min(1, speed / 2))
      if (speed > WALK_SPEED_MPS && dM > 0.3) {
        const dLat = (pos.lat - prev.lat) * 111320
        const dLng = (pos.lng - prev.lng) * 111320 * Math.cos((pos.lat * Math.PI) / 180)
        heroDirRef.current = Math.abs(dLat) >= Math.abs(dLng) ? (dLat >= 0 ? 'up' : 'down') : (dLng >= 0 ? 'right' : 'left')
      }
    }
    // 腳印：每 6 公尺留一個（走路中才留，用目前投影座標當下記錄，之後純靠 FootprintPool 內建的
    // 時間漸隱，不需要跟著地圖重新投影）。
    const lastFp = lastFootprintPosRef.current
    if (map && movingTRef.current > 0.1 && (!lastFp || haversineM([lastFp.lat, lastFp.lng], [pos.lat, pos.lng]) >= FOOTPRINT_STEP_M)) {
      lastFootprintPosRef.current = { lat: pos.lat, lng: pos.lng }
      try { const pt = map.project([pos.lng, pos.lat]); footprintsRef.current.push(pt.x, pt.y) } catch { /* ignore */ }
    }
    lastPosRef.current = { lat: pos.lat, lng: pos.lng, t: now }
    if (map && followingRef.current) {
      try { map.easeTo({ center: [pos.lng, pos.lat], zoom: 16.5, pitch: 0, bearing: 0, duration: 900, essential: true }) } catch { /* ignore */ }
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
    const container = containerRef.current
    if (!canvas || !container) return
    const dpr = window.devicePixelRatio || 1
    const w = container.clientWidth, h = container.clientHeight
    if (w <= 0 || h <= 0) return
    const wantW = Math.round(w * dpr), wantH = Math.round(h * dpr)
    if (canvas.width !== wantW || canvas.height !== wantH) { canvas.width = wantW; canvas.height = wantH }
    const ctx = canvas.getContext('2d')
    if (!ctx) return
    ctx.imageSmoothingEnabled = false
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, w, h)

    drawRoute(ctx, map, segmentsRef.current)
    drawFootprints(ctx)
    drawKmMarks(ctx, map, kmMarksRef.current)
    drawTargets(ctx, map, targetsRef.current)

    const p = posRef.current
    const heroLat = p ? p.lat : initialCenterRef.current[0]
    const heroLng = p ? p.lng : initialCenterRef.current[1]
    const pt = map.project([heroLng, heroLat])

    // 走路動畫：速度 >0.8 m/s 才切幀（每 180ms 換一次腳，在 1/2 兩幀之間交替），否則固定站立幀 0
    // （CONTRACT_R2 §2：站立＋兩幀走路共 3 個相異畫面，見 sprites.ts drawHero() 的 frame 參數）。
    if (!reducedMotionRef.current && p && statusRef.current === 'tracking' && movingTRef.current > 0.1) {
      walkFrameTRef.current += dt
      if (walkFrameTRef.current >= 0.18) { walkFrameTRef.current = 0; walkFrameRef.current = walkFrameRef.current === 1 ? 2 : 1 }
    } else {
      walkFrameRef.current = 0
    }
    // 整數倍率（CONTRACT.md §4「1 個地圖像素≈3 CSS px」的像素化精神一併套用在勇者身上）：非整數倍率
    // 會讓 fillRect 落在次像素邊界，被瀏覽器抗鋸齒糊成灰邊，破壞限色像素風的銳利感（2026-09-27 冒煙
    // 截圖比對像素值時發現：headband 白色在螢幕上量到接近純白，但相鄰邊緣糊成灰階，即此問題）。
    const heroScale = Math.round(Math.max(2, Math.min(4, (w / 360) * 3)))
    drawHero(ctx, pt.x, pt.y, heroDirRef.current, walkFrameRef.current, heroScale)
  }

  function drawFootprints(ctx: CanvasRenderingContext2D) {
    footprintsRef.current.draw(ctx, 3)
  }

  function drawRoute(ctx: CanvasRenderingContext2D, map: MapLibreMap, segs: [number, number][][]) {
    if (!segs?.length) return
    for (const seg of segs) {
      if (!seg || seg.length < 2) continue
      const pts = seg.length > 600 ? decimate(seg, 600) : seg
      const screen = pts.map((p) => map.project([p[1], p[0]]))
      ctx.save()
      ctx.strokeStyle = GOLD
      ctx.lineWidth = 4
      ctx.lineCap = 'round'
      ctx.lineJoin = 'round'
      ctx.setLineDash([6, 5]) // 「足跡道路」：金色點列虛線
      ctx.beginPath()
      screen.forEach((pt, i) => (i === 0 ? ctx.moveTo(pt.x, pt.y) : ctx.lineTo(pt.x, pt.y)))
      ctx.stroke()
      ctx.restore()
    }
  }

  function drawKmMarks(ctx: CanvasRenderingContext2D, map: MapLibreMap, marks: { km: number; lat: number; lng: number }[]) {
    if (!marks?.length) return
    for (const m of marks) {
      const pt = map.project([m.lng, m.lat])
      drawKmFlag(ctx, pt.x, pt.y, m.km, 1)
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
      <canvas ref={canvasRef} style={{ position: 'absolute', inset: 0, pointerEvents: 'none', display: 'block', imageRendering: 'pixelated' }} />
    </div>
  )
})

export default RetroMap
