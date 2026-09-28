'use client'

// 溫馨可愛（cute）GPS 地圖主元件（docs/skins/CUTE_CONTRACT.md§4）。架構比照 track/retro/RetroMap.tsx
// （只在 track/page.tsx 判斷 getActiveSkin()==='cute' 為真時，以 next/dynamic(ssr:false) 動態載入本檔，
// maplibre-gl 與本目錄程式碼只進獨立 chunk，非白名單使用者的 /track 首屏 bundle 完全不含這裡的程式碼）。
//
// 隔離原則（CONTRACT.md §1，沿用 retro/scifi 同一段規則）：既有 Leaflet 地圖照舊建立與運作（本檔完全
// 不碰 track/page.tsx 的 Leaflet refs／狀態），本元件只是疊在同一位置的另一層視覺呈現；任何初始化失敗
// （WebGL 不支援、8 秒未 load、context lost、渲染例外）一律呼叫 onFallback() 通知父層卸載本元件、恢復
// 顯示 Leaflet——跑步紀錄（GPS 取點/距離/上傳）完全不依賴本檔是否成功渲染。
//
// 與 retro 的差異：cute 不是像素風，維持一般抗鋸齒平滑渲染（無 pixelRatio 降級／無
// image-rendering:pixelated）；不需要水域兩幀動畫（靜態小波浪 fill-pattern 已足夠，契約沒有要求
// 動畫水波）；顯示公園／地標文字標籤（retro 刻意不顯示任何文字）；跑者是「SVG 離屏 canvas 圖」而非
// 逐像素繪製的方向性 sprite（見 runner.ts），只需要左右翻轉＋彈跳/呼吸動畫，不需要四方向切幀。

import { forwardRef, useEffect, useImperativeHandle, useRef } from 'react'
import { Map as MapLibreMap, config as maplibreConfig, type MapOptions, type LngLatLike, type MapMouseEvent } from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { buildCuteStyle } from './style'
import { tileImageData, type TileKind } from './tiles'
import { loadRunnerSprite, drawRunner } from './runner'
import { drawKmFlag, drawTargetIcon } from './icons'
import { drawHeart, CUTE_PALETTE } from '@/components/cute/decor'
import { isWebglSupported, haversineM, metersPerPixel, geoCircle, decimate, pointsAtInterval } from './geo'
import type { CuteMapHandle, CuteMapProps, CuteTarget } from './types'

// maplibre-gl worker 自架修正：與 scifi/retro 同一個根因/同一套修法（見 RetroMap.tsx 詳細註解）——
// 三套風格共寫同一個 module-level flag（maplibreConfig.WORKER_URL），同一個 maplibre-gl 套件實例的
// 同一份設定，誰先執行都會設好、不會互相覆蓋成不同值，因此不需要 import 對方檔案。
const MAPLIBRE_WORKER_URL = '/vendor/maplibre-gl-6.11.2/maplibre-gl-worker.mjs'
if (typeof window !== 'undefined' && !maplibreConfig.WORKER_URL) {
  maplibreConfig.WORKER_URL = MAPLIBRE_WORKER_URL
}

const FALLBACK_TIMEOUT_MS = 8000
const PINK = '#ec6fae'
const LAST_POS_KEY = 'dor_cute_last_pos' // 與 track/page.tsx 的 CUTE_LAST_POS_KEY 同一把 key
const RESUME_FOLLOW_MS = 8000
const HEART_STEP_M = 100 // 每 100 公尺一顆小愛心（契約逐字）
const WALK_SPEED_MPS = 0.8 // 速度 >0.8 m/s 才播彈跳動畫，否則呼吸待機
const TILE_IDS: readonly TileKind[] = ['tree', 'wave']
const STYLE_IMAGE_IDS: Record<TileKind, string> = { tree: 'cute-tree', wave: 'cute-wave' }

function writeLastPos(lat: number, lng: number) {
  try { localStorage.setItem(LAST_POS_KEY, JSON.stringify({ lat, lng })) } catch { /* 私密瀏覽/storage 被封鎖：純錦上添花，略過即可 */ }
}

const CuteMap = forwardRef<CuteMapHandle, CuteMapProps>(function CuteMap(props, ref) {
  const { pos, status, segments, kmMarks, targets, focusMode, initialCenter, initialZoom, onFallback, onTargetClick, bottomInset } = props

  const containerRef = useRef<HTMLDivElement | null>(null)
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const mapRef = useRef<MapLibreMap | null>(null)
  const rafRef = useRef<number | null>(null)
  const detachListenersRef = useRef<() => void>(() => {})
  const failedRef = useRef(false)
  const loadedRef = useRef(false)
  const followingRef = useRef(true)
  const lastPosRef = useRef<{ lat: number; lng: number; t: number } | null>(null)
  const heroFlipRef = useRef(false) // 往西移動時翻轉（true＝面向左）
  const movingTRef = useRef(0) // 0..1：目前速度換算出的「移動強度」，>WALK_SPEED_MPS 才播彈跳動畫
  const reducedMotionRef = useRef(false)
  const initialCenterRef = useRef(initialCenter)
  const resumeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const tilesRequestedRef = useRef(0)
  const tilesLoadedRef = useRef(0)
  const fpsRef = useRef(0)
  const frameCountRef = useRef(0)
  const fpsWindowStartRef = useRef(0)
  const lastFrameTRef = useRef(0)
  const runnerReadyRef = useRef(false)

  const posRef = useRef(pos); posRef.current = pos
  const statusRef = useRef(status); statusRef.current = status
  const segmentsRef = useRef(segments); segmentsRef.current = segments
  const kmMarksRef = useRef(kmMarks); kmMarksRef.current = kmMarks
  const targetsRef = useRef(targets); targetsRef.current = targets
  const focusModeRef = useRef(focusMode); focusModeRef.current = focusMode
  const onFallbackRef = useRef(onFallback); onFallbackRef.current = onFallback
  const onTargetClickRef = useRef(onTargetClick); onTargetClickRef.current = onTargetClick
  const bottomInsetRef = useRef(bottomInset ?? 0); bottomInsetRef.current = bottomInset ?? 0
  const appliedPaddingBottomRef = useRef<number | null>(null)
  initialCenterRef.current = initialCenter

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
    const onDragStart = () => { if (isCurrent()) pauseFollow() }
    const onZoomStart = () => { if (isCurrent()) pauseFollow() }
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
    const onImageMissing = (e: { id: string }) => {
      if (!isCurrent()) return
      const id = e.id
      if (map!.hasImage(id)) return
      const kind = (Object.keys(STYLE_IMAGE_IDS) as TileKind[]).find((k) => STYLE_IMAGE_IDS[k] === id)
      if (!kind || !TILE_IDS.includes(kind)) return
      try { map!.addImage(id, tileImageData(kind)) } catch { /* ignore：單一圖塊掛不上不影響其他圖層 */ }
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

      // 角色 SVG 離屏 canvas 只需載入一次（data URI，無網路請求）；不阻塞地圖建立，載入完成前
      // renderFrame() 對應那幾幀安靜略過（見 runner.ts drawRunner 說明）。
      loadRunnerSprite().then(() => { runnerReadyRef.current = true }).catch(() => { /* 理論上不會發生，data URI 沒有網路請求；即使失敗也只是角色晚一點出現 */ })

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

  // __cuteDebug 偵錯把手（CONTRACT.md §6）：每秒更新一次。
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
      const canvas = map.getCanvas()
      ;(window as unknown as { __cuteDebug?: unknown }).__cuteDebug = {
        center: { lat: c.lat, lng: c.lng },
        zoom: map.getZoom(),
        pitch: map.getPitch(),
        bearing: map.getBearing(),
        lastPos: lastPosRef.current ? { lat: lastPosRef.current.lat, lng: lastPosRef.current.lng } : null,
        tilesRequested: tilesRequestedRef.current,
        tilesLoaded: tilesLoadedRef.current,
        heroScreen,
        heroFlip: heroFlipRef.current,
        runnerReady: runnerReadyRef.current,
        fps: fpsRef.current,
        patternImages: { tree: map.hasImage('cute-tree'), wave: map.hasImage('cute-wave') },
        canvasSize: { w: canvas.width, h: canvas.height, cssW: canvas.clientWidth, cssH: canvas.clientHeight },
        fontReady: (typeof document !== 'undefined' && document.fonts) ? document.fonts.check('16px DORCute') : null,
      }
    }, 1000)
    return () => {
      clearInterval(id)
      try { delete (window as unknown as { __cuteDebug?: unknown }).__cuteDebug } catch { /* ignore */ }
    }
  }, [])

  useEffect(() => {
    if (status !== 'tracking') movingTRef.current = 0
  }, [status])

  // GPS 位置更新：跟隨鏡頭（center 平移，pitch/bearing 恆 0）＋算移動速度（供彈跳動畫）＋依經緯度
  // 位移的主軸方向決定是否翻轉（往西移動才翻轉，其餘方向維持面向右／預設）。
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
        const dLng = pos.lng - prev.lng
        if (Math.abs(dLng) > 1e-9) heroFlipRef.current = dLng < 0
      }
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
      lastFrameTRef.current = t
      try {
        renderFrame(map, t)
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

  function renderFrame(map: MapLibreMap, t: number) {
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
    ctx.imageSmoothingEnabled = true // 手繪可愛風要平滑，不像 retro 走像素風
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, w, h)

    drawRoute(ctx, map, segmentsRef.current)
    drawKmMarks(ctx, map, kmMarksRef.current)
    drawTargets(ctx, map, targetsRef.current)

    const p = posRef.current
    const heroLat = p ? p.lat : initialCenterRef.current[0]
    const heroLng = p ? p.lng : initialCenterRef.current[1]
    const pt = map.project([heroLng, heroLat])
    const moving = !!p && statusRef.current === 'tracking' && movingTRef.current > 0.1
    drawRunner(ctx, pt.x, pt.y, { flipX: heroFlipRef.current, moving, t, reducedMotion: reducedMotionRef.current })
  }

  function drawRoute(ctx: CanvasRenderingContext2D, map: MapLibreMap, segs: [number, number][][]) {
    if (!segs?.length) return
    const zoom = map.getZoom()
    for (const seg of segs) {
      if (!seg || seg.length < 2) continue
      const pts = seg.length > 600 ? decimate(seg, 600) : seg
      const screen = pts.map((p) => map.project([p[1], p[0]]))
      // 白色 casing（線寬 7）在下，桃紅虛線（線寬 4）在上——契約逐字。
      ctx.save()
      ctx.strokeStyle = '#ffffff'
      ctx.lineWidth = 7
      ctx.lineCap = 'round'
      ctx.lineJoin = 'round'
      ctx.beginPath()
      screen.forEach((pt, i) => (i === 0 ? ctx.moveTo(pt.x, pt.y) : ctx.lineTo(pt.x, pt.y)))
      ctx.stroke()
      ctx.restore()
      ctx.save()
      ctx.strokeStyle = PINK
      ctx.lineWidth = 4
      ctx.lineCap = 'round'
      ctx.lineJoin = 'round'
      ctx.setLineDash([8, 6])
      ctx.beginPath()
      screen.forEach((pt, i) => (i === 0 ? ctx.moveTo(pt.x, pt.y) : ctx.lineTo(pt.x, pt.y)))
      ctx.stroke()
      ctx.restore()
      // 每 100m 一顆小愛心：把「100 公尺」換算成目前 zoom 下的螢幕像素間距，沿投影後的折線等距取點。
      const midLat = pts[Math.floor(pts.length / 2)][0]
      const mppx = metersPerPixel(midLat, zoom) || 1
      const stepPx = Math.max(10, HEART_STEP_M / mppx)
      for (const hp of pointsAtInterval(screen, stepPx)) drawHeart(ctx, hp.x, hp.y, 8, CUTE_PALETTE.peach)
    }
  }

  function drawKmMarks(ctx: CanvasRenderingContext2D, map: MapLibreMap, marks: { km: number; lat: number; lng: number }[]) {
    if (!marks?.length) return
    for (const m of marks) {
      const pt = map.project([m.lng, m.lat])
      drawKmFlag(ctx, pt.x, pt.y, m.km, 1)
    }
  }

  function drawTargets(ctx: CanvasRenderingContext2D, map: MapLibreMap, tgts: CuteTarget[]) {
    if (!tgts?.length) return
    for (const tgt of tgts) {
      const ring = geoCircle(tgt.lat, tgt.lng, Math.max(4, tgt.radius)).map((p) => map.project([p[1], p[0]]))
      const center = map.project([tgt.lng, tgt.lat])
      ctx.save()
      ctx.strokeStyle = tgt.done ? '#f6b73c' : PINK
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
      <canvas ref={canvasRef} style={{ position: 'absolute', inset: 0, pointerEvents: 'none', display: 'block' }} />
    </div>
  )
})

export default CuteMap
