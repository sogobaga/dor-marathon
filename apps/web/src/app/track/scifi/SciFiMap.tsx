'use client'

// 未來科幻世界（scifi）GPS 地圖主元件（CONTRACT.md §4）。
// 只在 track/page.tsx 判斷 `document.documentElement.dataset.skin === 'scifi'` 為真時，以
// next/dynamic(ssr:false) 動態載入本檔（見該檔掛載處）——本檔（含 maplibre-gl 與其 CSS）因此只會
// 進入一個獨立的動態 chunk，非白名單使用者的 /track 首屏 bundle 完全不含這裡的程式碼。
//
// 隔離原則（CONTRACT.md §1）：既有 Leaflet 地圖照舊建立與運作（本檔完全不碰 track/page.tsx 的
// Leaflet refs／狀態），本元件只是疊在同一位置的另一層視覺呈現；任何初始化失敗（WebGL 不支援、
// 8 秒未 load、context lost、渲染例外）一律呼叫 onFallback() 通知父層卸載本元件、恢復顯示 Leaflet
// ——跑步紀錄（GPS 取點/距離/上傳）完全不依賴本檔是否成功渲染。

import { forwardRef, useEffect, useImperativeHandle, useRef } from 'react'
import { Map as MapLibreMap, AttributionControl, config as maplibreConfig, type MapOptions, type LngLatLike, type MapMouseEvent } from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { buildScifiStyle } from './style'
import { orbitron } from './font'
import { Soul, TrailPool } from './particles'
import { LightRain } from './rain'
import type { SciFiMapHandle, SciFiMapProps, SciFiTarget } from './types'

// Orbitron 字型 className 現由 ./font.ts 統一持有（與 OrbitronText.tsx 共用同一實例，避免重複
// @font-face），本檔不再自行呼叫 next/font/google（2026-09-27 review：與 RaceFocusMode.tsx 隔離修正
// 同一輪，見該檔與 font.ts 檔頭說明）。canvas 文字（每公里標記）與 className 掛在容器上，皆侷限在
// 本元件範圍——本檔本身只在 track/page.tsx 以 next/dynamic(ssr:false) 且 scifi 生效時才載入。

// maplibre-gl worker 自架修正（2026-09-27 VERIFY-E2E FAIL #1/#6 根因修復）：maplibre-gl 6.x 內部用
// `import.meta.url` 推算 worker 腳本網址（見 node_modules/maplibre-gl/dist/maplibre-gl.mjs 的 Qi()），
// 但該值先被指派給一個變數再傳入 `new URL(tpl, e)`，不是 webpack 靜態辨識的 `new URL('...', import.meta.url)`
// 直接字面樣式，導致 webpack 對這個第三方預先打包好的 .mjs 檔沒有把 import.meta.url 正確改寫成 public
// asset URL，實測（見契約 E2E #1 報告）建置後被展開成建置機器的絕對路徑
// file:///C:/Project%20Dev/.../maplibre-gl.mjs，maplibre 內部 regex 只認 `^https?:` 視為不合法→退化成
// 空字串 worker URL→`new Worker('', {type:'module'})` 100% 拋錯→100% 觸發 fallback，GPS 地圖的科幻粒子
// 呈現從未真正渲染過。修法：完全不依賴 maplibre 對 import.meta.url 的自動偵測，改用 maplibre-gl 官方
// 支援的 `config.WORKER_URL` 覆寫（見同一支 .mjs 匯出的 setWorkerUrl/getWorkerUrl，config.WORKER_URL 是
// 底層同一個值），指到我們自己複製進 public/ 的同版本 worker 靜態檔（same-origin 相對路徑，符合
// CONTRACT.md §5 `worker-src 'self' blob:`，不需再放寬 CSP）。檔案來源與版本同步方式見
// public/vendor/maplibre-gl-6.11.2/README.txt；package.json 的 maplibre-gl 版本一旦升級，必須同步更新
// 那個資料夾與這裡的路徑常數，否則 worker 協定版本不符会在 worker 內部丟錯（仍會被 onFallback 接住，
// 不影響跑步邏輯，但科幻地圖會退回 Leaflet）。
const MAPLIBRE_WORKER_URL = '/vendor/maplibre-gl-6.11.2/maplibre-gl-worker.mjs'
if (typeof window !== 'undefined' && !maplibreConfig.WORKER_URL) {
  maplibreConfig.WORKER_URL = MAPLIBRE_WORKER_URL
}

const FALLBACK_TIMEOUT_MS = 8000
const FUG = '#35e6ff'
const HUNT = '#ff3fa0'
const LAST_POS_KEY = 'dor_scifi_last_pos' // CONTRACT_R2.md §2：SciFiMap 自行在每次 pos 更新時寫入，供下次
// 開頁在拿到真實定位前，初始中心就能落在「最後已知位置」而非假的城市中心
const RESUME_FOLLOW_MS = 8000 // 使用者拖曳／縮放後暫停跟隨，8 秒後自動恢復（CONTRACT_R2.md §2）

function writeLastPos(lat: number, lng: number) {
  try { localStorage.setItem(LAST_POS_KEY, JSON.stringify({ lat, lng })) } catch { /* 私密瀏覽/storage 被封鎖：純錦上添花，略過即可 */ }
}

function isWebglSupported(): boolean {
  try {
    const canvas = document.createElement('canvas')
    return !!(canvas.getContext('webgl2') || canvas.getContext('webgl') || canvas.getContext('experimental-webgl'))
  } catch { return false }
}

function metersPerPixel(lat: number, zoom: number): number {
  return (156543.03392 * Math.cos((lat * Math.PI) / 180)) / Math.pow(2, zoom)
}

// 依地理座標＋半徑(公尺)算出地面圓的多邊形頂點（逐點交給 map.project 投影，pitch 55° 下自然呈現
// 透視橢圓，比「螢幕像素半徑畫正圓」更貼近真實地面圈的視覺）。
function geoCircle(lat: number, lng: number, radiusM: number, n = 28): [number, number][] {
  const latRad = (lat * Math.PI) / 180
  const dLat = radiusM / 111320
  const dLng = radiusM / (111320 * Math.cos(latRad) || 1)
  const out: [number, number][] = []
  for (let i = 0; i < n; i++) {
    const t = (i / n) * Math.PI * 2
    out.push([lat + dLat * Math.sin(t), lng + dLng * Math.cos(t)])
  }
  return out
}

function bearingBetween(a: { lat: number; lng: number }, b: { lat: number; lng: number }): number {
  const toRad = Math.PI / 180
  const y = Math.sin((b.lng - a.lng) * toRad) * Math.cos(b.lat * toRad)
  const x = Math.cos(a.lat * toRad) * Math.sin(b.lat * toRad) - Math.sin(a.lat * toRad) * Math.cos(b.lat * toRad) * Math.cos((b.lng - a.lng) * toRad)
  return (Math.atan2(y, x) * 180) / Math.PI
}

function haversineM(a: [number, number], b: [number, number]): number {
  const R = 6371000, rad = Math.PI / 180
  const dLat = (b[0] - a[0]) * rad, dLng = (b[1] - a[1]) * rad
  const x = Math.sin(dLat / 2) ** 2 + Math.cos(a[0] * rad) * Math.cos(b[0] * rad) * Math.sin(dLng / 2) ** 2
  return R * 2 * Math.atan2(Math.sqrt(x), Math.sqrt(1 - x))
}

function decimate<T>(arr: T[], n: number): T[] {
  if (arr.length <= n) return arr
  const step = arr.length / n
  const out: T[] = []
  for (let i = 0; i < n; i++) out.push(arr[Math.floor(i * step)])
  out.push(arr[arr.length - 1])
  return out
}

const SciFiMap = forwardRef<SciFiMapHandle, SciFiMapProps>(function SciFiMap(props, ref) {
  const { pos, status, segments, kmMarks, targets, focusMode, initialCenter, initialZoom, onFallback, onTargetClick } = props

  const wrapRef = useRef<HTMLDivElement | null>(null)
  const containerRef = useRef<HTMLDivElement | null>(null)
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const mapRef = useRef<MapLibreMap | null>(null)
  const rafRef = useRef<number | null>(null)
  // 2026-09-27 R2-fix2（minor）：render loop 的例外路徑（onFallbackFromRender，元件層級函式）原本直接
  // map.remove() 沒有先呼叫建圖 effect 內的 detachListeners()，與 failInstance() 的清理順序不一致
  // （契約要求「先移除監聽再 map.remove()」）。用這個 ref 讓建圖 effect 把它的 detachListeners 交出來，
  // 兩條退回路徑統一走同一套清理順序。
  const detachListenersRef = useRef<() => void>(() => {})
  const failedRef = useRef(false)
  const loadedRef = useRef(false)
  const followingRef = useRef(true)
  const soulRef = useRef<Soul | null>(null)
  const trailRef = useRef<TrailPool | null>(null)
  const rainRef = useRef<LightRain | null>(null)
  const lastPosRef = useRef<{ lat: number; lng: number; t: number } | null>(null)
  const dirRef = useRef<{ x: number; y: number }>({ x: 0, y: 0 })
  const movingTRef = useRef(0)
  const lastFrameTRef = useRef(0)
  const reducedMotionRef = useRef(false)
  const initialCenterRef = useRef(initialCenter) // 定位前的「靈魂定位中」暗淡狀態要畫在這個點上
  const resumeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null) // §2 拖曳/縮放暫停跟隨 8 秒後自動恢復
  const tilesRequestedRef = useRef(0) // __scifiDebug：openfreemap 圖磚請求/完成數（source 'data'/'dataloading' 事件，dataType==='tile'）
  const tilesLoadedRef = useRef(0)
  const fpsRef = useRef(0)
  const frameCountRef = useRef(0)
  const fpsWindowStartRef = useRef(0)

  // 高頻資料走 ref（避免 rAF 迴圈依賴 useEffect 重新掛載），由 props 變動時同步寫入。
  const posRef = useRef(pos); posRef.current = pos
  const statusRef = useRef(status); statusRef.current = status
  const segmentsRef = useRef(segments); segmentsRef.current = segments
  const kmMarksRef = useRef(kmMarks); kmMarksRef.current = kmMarks
  const targetsRef = useRef(targets); targetsRef.current = targets
  const focusModeRef = useRef(focusMode); focusModeRef.current = focusMode
  const onFallbackRef = useRef(onFallback); onFallbackRef.current = onFallback
  const onTargetClickRef = useRef(onTargetClick); onTargetClickRef.current = onTargetClick
  initialCenterRef.current = initialCenter // 只在尚無真實定位時的「定位中」暗淡靈魂位置用，可安全每次 render 同步

  // fail()：component 層級的「無條件」退回入口，只在建圖之前（webgl 完全不支援，這時還沒有任何
  // map instance 可比對）使用；一旦 map 建立，一律改用下方效果內的 instance-scoped failInstance()——
  // 2026-09-27 R2 review 根因：舊版 fail() 不分 instance 一律操作 mapRef.current，React StrictMode
  // 雙掛載或快速 OFF→ON 切換時，舊 instance 的非同步事件（tile 載入失敗、webglcontextlost…）若在新
  // instance 已建立、mapRef.current 已指向新 map 之後才觸發，會誤删新 instance（`mapRef.current?.remove()`
  // 砍掉的其實是新地圖），使用者因此看到「地圖忽然退回 Leaflet」——實際上新地圖本身毫無問題，只是被
  // 舊 instance 的遲到事件錯殺。
  const fail = (reason: string) => {
    if (failedRef.current) return
    failedRef.current = true
    // eslint-disable-next-line no-console
    console.warn('[scifi-map] fallback', reason)
    if (rafRef.current != null) { cancelAnimationFrame(rafRef.current); rafRef.current = null }
    try { delete (window as unknown as { __scifiDebug?: unknown }).__scifiDebug } catch { /* ignore */ }
    onFallbackRef.current(reason)
  }

  useImperativeHandle(ref, () => ({
    recenter(p) {
      const map = mapRef.current
      if (!map) return
      followingRef.current = true
      if (resumeTimerRef.current) { clearTimeout(resumeTimerRef.current); resumeTimerRef.current = null }
      const target = p || posRef.current
      if (target) { try { map.easeTo({ center: [target.lng, target.lat], zoom: 16.5, pitch: 55, duration: 500, essential: true }) } catch { /* ignore */ } }
    },
    zoomBy(delta: number) {
      const map = mapRef.current
      if (!map) return
      try { map.easeTo({ zoom: map.getZoom() + delta, duration: 250, essential: true }) } catch { /* ignore */ }
    },
  }), [])

  // ── 建圖（僅掛載時一次）：任何例外／不支援／逾時／context lost 一律退回 Leaflet ──────────────────
  // 實例隔離（CONTRACT_R2.md §1）：`map` 是本次 effect 呼叫的區域變數（instance token）；每個監聽
  // callback 一律先比對 `mapRef.current === map` 才動作，確保「這個 instance 已經不是目前使用中的那
  // 一個」時，其任何遲到事件都是 no-op（既不會誤觸發 fail、也不會誤呼叫 map.resize()/render 等 API）。
  // cleanup 依契約要求「先移除監聽再 map.remove()」——用具名 handler 變數以便準確 .off()／removeEventListener。
  useEffect(() => {
    if (!containerRef.current) return
    if (!isWebglSupported()) { fail('webgl-unsupported'); return }
    let cancelled = false
    let timeoutId: ReturnType<typeof setTimeout> | null = null
    let ro: ResizeObserver | null = null
    let localFailed = false
    let map: MapLibreMap | null = null

    // 2026-09-27 R2-fix2 根因：`new MapLibreMap(...)` 建構子本身同步丟例外時（例如 WebGL context 數量
    // 已達瀏覽器上限，快速 OFF→ON 切換後常見），`map =` 這行賦值從未完成，區域變數 `map` 仍是初始值
    // null；下方 catch 呼叫 failInstance() 時，舊版 `map != null` 這個條件恆為 false，isCurrent() 因此
    // 永遠 return false，failInstance 提前 return，不會 console.warn 也不會呼叫 onFallback()——使用者
    // 看到永久空白地圖、Leaflet 容器仍 visibility:hidden，且沒有任何診斷訊息。
    // 修法：不再要求 `map != null`，改比較「這個 instance 的 map 值」是否等於 mapRef.current——當
    // map 為 null（建構失敗，mapRef.current 從未被這次 effect 賦值過）時，只要 mapRef.current 也還是
    // null（沒有更新的 instance 搶先接管），就視為仍是目前有效的呼叫，讓 failInstance 得以執行到底。
    const isCurrent = () => !cancelled && mapRef.current === map

    const failInstance = (reason: string) => {
      if (localFailed || failedRef.current) return
      if (!isCurrent()) return // 舊 instance 的遲到事件：目前使用中的已經是別的 map（或已被 cleanup），不動它
      localFailed = true
      failedRef.current = true
      // eslint-disable-next-line no-console
      console.warn('[scifi-map] fallback', reason)
      if (rafRef.current != null) { cancelAnimationFrame(rafRef.current); rafRef.current = null }
      detachListeners()
      try { map?.remove() } catch { /* ignore */ }
      if (mapRef.current === map) mapRef.current = null
      try { delete (window as unknown as { __scifiDebug?: unknown }).__scifiDebug } catch { /* ignore */ }
      onFallbackRef.current(reason)
    }

    const onLoad = () => {
      if (!isCurrent()) return
      loadedRef.current = true
      // 精簡授權鈕預設是展開狀態（整條文字），會壓到「回到目前位置」；載入後先收成 ⓘ，點擊才展開。
      try { containerRef.current?.querySelector('.maplibregl-ctrl-attrib')?.classList.remove('maplibregl-compact-show') } catch { /* ignore */ }
      try {
        map!.setSky({
          'sky-color': '#02040a',
          'horizon-color': '#0a2540',
          'sky-horizon-blend': 0.9,
          'horizon-fog-blend': 0.6,
          'atmosphere-blend': 0.35,
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
        } as any)
      } catch { /* 版本不支援 setSky 就略過（CONTRACT.md §4.2「若版本支援」），不影響地圖本體 */ }
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
      let best: SciFiTarget | null = null
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
    // maplibre-gl 的 tile 事件是 dataType==='source'（不是 'tile'）＋帶 `tile` 欄位，見
    // maplibre-gl.d.ts MapSourceDataEvent（2026-09-27 smoke test 實測抓到：舊版誤判 dataType==='tile'，
    // 導致 tilesRequested/tilesLoaded 恆為 0）。
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
        map?.getCanvas().removeEventListener('webglcontextlost', onContextLost)
      } catch { /* ignore */ }
    }
    detachListenersRef.current = detachListeners

    try {
      reducedMotionRef.current = typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
      const isMobile = window.innerWidth < 768
      soulRef.current = new Soul(focusMode ? 28 : (isMobile ? 55 : 68))
      trailRef.current = new TrailPool(focusMode ? 200 : 400)
      rainRef.current = new LightRain(reducedMotionRef.current || focusMode ? 0 : (isMobile ? 24 : 36))

      map = new MapLibreMap({
        container: containerRef.current,
        style: buildScifiStyle() as unknown as MapOptions['style'],
        center: [initialCenter[1], initialCenter[0]] as LngLatLike,
        zoom: initialZoom ?? 16,
        pitch: 55,
        bearing: 0,
        antialias: true,
        maxPitch: 70,
        localIdeographFontFamily: "'Noto Sans TC','Microsoft JhengHei',sans-serif",
        // 授權聲明用精簡「ⓘ」按鈕放右上（預設是展開的整條放左/右下，專注模式時會橫在下半部數字上；2026-09-27 編排者檢視截圖）。
        attributionControl: false,
      } as MapOptions)
      try { map.addControl(new AttributionControl({ compact: true }), 'top-right') } catch { /* ignore */ }
      mapRef.current = map

      timeoutId = setTimeout(() => { if (!loadedRef.current) failInstance('load-timeout-8s') }, FALLBACK_TIMEOUT_MS)

      map.on('load', onLoad)
      map.on('error', onError)
      map.on('dragstart', onDragStart)
      map.on('zoomstart', onZoomStart)
      map.on('click', onClick)
      map.on('dataloading', onDataLoading as never)
      map.on('data', onData as never)
      try { map.getCanvas().addEventListener('webglcontextlost', onContextLost) } catch { /* ignore */ }

      try { document.fonts?.load?.(`700 14px ${orbitron.style.fontFamily}`) } catch { /* 預載失敗不影響地圖，canvas 文字退回預設字型 */ }

      ro = new ResizeObserver(() => { if (isCurrent()) { try { map!.resize() } catch { /* ignore */ } } })
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
      detachListeners() // 契約要求：先移除監聽，再 map.remove()
      try { map?.remove() } catch { /* ignore */ }
      if (mapRef.current === map) mapRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 只在掛載時建圖一次；initialCenter/initialZoom 僅供首次定位，focusMode 初始粒子數見下方獨立 effect 動態調整
  }, [])

  // __scifiDebug 偵錯把手（CONTRACT_R2.md §2）：每秒更新一次，只在本 instance 仍是目前使用中的地圖時
  // 才寫；unmount／fallback 時清除（fail()/failInstance() 亦各自清一次，涵蓋「render loop 已停但元件
  // 尚未真正 unmount」的空窗期）。
  useEffect(() => {
    const id = setInterval(() => {
      const map = mapRef.current
      if (!map) return
      let soulScreen = { x: 0, y: 0 }
      try {
        const p = posRef.current
        const proj = p ? map.project([p.lng, p.lat]) : map.project([initialCenterRef.current[1], initialCenterRef.current[0]])
        soulScreen = { x: Math.round(proj.x), y: Math.round(proj.y) }
      } catch { /* ignore */ }
      const c = map.getCenter()
      ;(window as unknown as { __scifiDebug?: unknown }).__scifiDebug = {
        center: { lat: c.lat, lng: c.lng },
        zoom: map.getZoom(),
        pitch: map.getPitch(),
        bearing: map.getBearing(),
        lastPos: lastPosRef.current ? { lat: lastPosRef.current.lat, lng: lastPosRef.current.lng } : null,
        tilesRequested: tilesRequestedRef.current,
        tilesLoaded: tilesLoadedRef.current,
        soulScreen,
        fps: fpsRef.current,
      }
    }, 1000)
    return () => {
      clearInterval(id)
      try { delete (window as unknown as { __scifiDebug?: unknown }).__scifiDebug } catch { /* ignore */ }
    }
  }, [])

  // 專注模式切換：動態調整粒子數／光雨（不需重建地圖）。非專注模式的數值對齊 CONTRACT_R2.md §3：
  // 環繞粒子 50–70 顆（55/68）、光雨 ≤40 條（24/36，已含「保留但降低到不干擾」的餘裕）。
  useEffect(() => {
    const isMobile = typeof window !== 'undefined' && window.innerWidth < 768
    soulRef.current?.setCount(focusMode ? 28 : (isMobile ? 55 : 68))
    trailRef.current?.setCap(focusMode ? 200 : 400)
    rainRef.current?.setCap(reducedMotionRef.current || focusMode ? 0 : (isMobile ? 24 : 36))
  }, [focusMode])

  // 跑步狀態離開 tracking：鏡頭 bearing 緩慢歸零、停止拖彗星尾巴（CONTRACT.md §4.2「idle 時 bearing 0」）
  useEffect(() => {
    if (status !== 'tracking') {
      movingTRef.current = 0
      dirRef.current = { x: 0, y: 0 }
      try { mapRef.current?.easeTo({ bearing: 0, duration: 1200 }) } catch { /* ignore */ }
    }
  }, [status])

  // GPS 位置更新：算移動速度/方向（供彗星拉長與粒子噴發）＋跟隨鏡頭（CONTRACT_R2.md §2：每次 pos
  // 更新 easeTo({center,zoom:16.5,pitch:55,bearing,duration:900})；bearing 只在速度 >1 m/s 才依行進
  // 方向更新，否則維持目前鏡頭 bearing 不變）。followingRef 由使用者拖曳/縮放暫停 8 秒（見上方
  // pauseFollow），「回到目前位置」透過 imperative handle 的 recenter() 立即恢復。
  useEffect(() => {
    if (!pos) return
    writeLastPos(pos.lat, pos.lng) // §2：SciFiMap 自行維護「最後已知位置」，供下次開頁定位前顯示城市用
    const now = performance.now()
    const prev = lastPosRef.current
    const map = mapRef.current
    const mapBearing = map ? map.getBearing() : 0
    let bearingToUse = mapBearing
    if (prev) {
      const dtS = Math.max(0.05, (now - prev.t) / 1000)
      const dM = haversineM([prev.lat, prev.lng], [pos.lat, pos.lng])
      const speed = dM / dtS
      movingTRef.current = Math.max(0, Math.min(1, speed / 3.2))
      if (speed > 1 && dM > 0.5) {
        const brg = bearingBetween(prev, pos)
        const rel = ((brg - mapBearing) * Math.PI) / 180
        dirRef.current = { x: Math.sin(rel), y: -Math.cos(rel) }
        bearingToUse = brg
      }
      // 速度 ≤1 m/s：維持目前鏡頭 bearing（bearingToUse 已預設為 mapBearing），dirRef 也不更新
      // （彗星尾巴方向沿用上一個有效方向，靜止時不會忽然轉向）。
    }
    lastPosRef.current = { lat: pos.lat, lng: pos.lng, t: now }
    if (map && followingRef.current) {
      try { map.easeTo({ center: [pos.lng, pos.lat], zoom: 16.5, pitch: 55, bearing: bearingToUse, duration: 900, essential: true }) } catch { /* ignore */ }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pos?.lat, pos?.lng])

  function startLoop() {
    const frame = (t: number) => {
      rafRef.current = requestAnimationFrame(frame)
      // __scifiDebug.fps：以實際 rAF 呼叫頻率量測（獨立於下方 fps 上限節流），每秒回填一次 fpsRef。
      frameCountRef.current += 1
      if (t - fpsWindowStartRef.current >= 1000) {
        fpsRef.current = frameCountRef.current
        frameCountRef.current = 0
        fpsWindowStartRef.current = t
      }
      const map = mapRef.current
      if (!map || !loadedRef.current || document.hidden) return
      const fps = focusModeRef.current ? 10 : 30
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

  // render loop 的例外走這裡，而不是直接呼叫效果內的 failInstance——render loop 由 startLoop() 在
  // map-creation effect 內啟動，但 frame() 閉包活得比單次 effect 呼叫更久沒有意義（cleanup 會
  // cancelAnimationFrame），此處只是型別上讓 renderFrame 的呼叫點不必依賴外層 effect 的區域函式。
  // 直接操作 mapRef/failedRef，語意與 failInstance 一致（cleanup 已把 rafRef 取消，這裡不會有殘留幀）。
  function onFallbackFromRender(reason: string) {
    if (failedRef.current) return
    failedRef.current = true
    // eslint-disable-next-line no-console
    console.warn('[scifi-map] fallback', reason)
    if (rafRef.current != null) { cancelAnimationFrame(rafRef.current); rafRef.current = null }
    try { detachListenersRef.current() } catch { /* ignore */ } // 契約要求：先移除監聽，再 map.remove()
    try { mapRef.current?.remove() } catch { /* ignore */ }
    mapRef.current = null
    try { delete (window as unknown as { __scifiDebug?: unknown }).__scifiDebug } catch { /* ignore */ }
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
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, w, h)

    if (!reducedMotionRef.current) {
      rainRef.current?.update(dt, w, h)
      rainRef.current?.draw(ctx)
    }

    drawRoute(ctx, map, segmentsRef.current)
    drawKmMarks(ctx, map, kmMarksRef.current)
    drawTargets(ctx, map, targetsRef.current)

    const p = posRef.current
    // §2「定位前就顯示城市」：還沒有真實定位時，靈魂以「定位中」暗淡狀態畫在 initialCenter（最後已知
    // 位置或台北大安森林公園），而不是整個不畫——讓使用者一進頁就看到城市與（暗淡的）自己，而非空地圖。
    const soulLat = p ? p.lat : initialCenterRef.current[0]
    const soulLng = p ? p.lng : initialCenterRef.current[1]
    const pt = map.project([soulLng, soulLat])
    const soul = soulRef.current, trail = trailRef.current
    if (!reducedMotionRef.current) {
      soul?.update(dt)
      if (p && statusRef.current === 'tracking' && movingTRef.current > 0.12 && trail) {
        const rate = 1 + movingTRef.current * 3
        for (let i = 0; i < rate; i++) if (Math.random() < 0.85) trail.spawn(pt.x, pt.y, dirRef.current.x, dirRef.current.y, 40 + movingTRef.current * 60)
      }
      trail?.update(dt)
    }
    trail?.draw(ctx)
    soul?.draw(ctx, pt.x, pt.y, p && !reducedMotionRef.current ? movingTRef.current : 0, dirRef.current.x, dirRef.current.y, !p)
  }

  function drawRoute(ctx: CanvasRenderingContext2D, map: MapLibreMap, segs: [number, number][][]) {
    if (!segs?.length) return
    for (const seg of segs) {
      if (!seg || seg.length < 2) continue
      const pts = seg.length > 600 ? decimate(seg, 600) : seg
      let total = 0
      const cum: number[] = [0]
      for (let i = 1; i < pts.length; i++) { total += haversineM(pts[i - 1], pts[i]); cum.push(total) }
      if (total <= 0) continue
      const screen = pts.map((p) => map.project([p[1], p[0]]))
      ctx.save()
      ctx.globalCompositeOperation = 'lighter'
      ctx.lineJoin = 'round'; ctx.lineCap = 'round'
      ctx.shadowColor = 'rgba(53,230,255,.55)'
      ctx.shadowBlur = 14
      ctx.strokeStyle = 'rgba(53,230,255,.35)'
      ctx.lineWidth = 14 // 外光暈 14（CONTRACT_R2.md §3）
      ctx.beginPath()
      screen.forEach((pt, i) => (i === 0 ? ctx.moveTo(pt.x, pt.y) : ctx.lineTo(pt.x, pt.y)))
      ctx.stroke()
      ctx.shadowBlur = 0
      for (let i = 1; i < screen.length; i++) {
        const t = total > 0 ? cum[i] / total : 0
        const r = 53 + (255 - 53) * t, g = 230 + (63 - 230) * t, b = 255 + (160 - 255) * t
        ctx.strokeStyle = `rgb(${r | 0},${g | 0},${b | 0})`
        ctx.lineWidth = 5 // 漸層線寬 5（CONTRACT_R2.md §3）
        ctx.beginPath()
        ctx.moveTo(screen[i - 1].x, screen[i - 1].y)
        ctx.lineTo(screen[i].x, screen[i].y)
        ctx.stroke()
      }
      ctx.restore()
    }
  }

  function drawKmMarks(ctx: CanvasRenderingContext2D, map: MapLibreMap, marks: { km: number; lat: number; lng: number }[]) {
    if (!marks?.length) return
    for (const m of marks) {
      const pt = map.project([m.lng, m.lat])
      ctx.save()
      ctx.translate(pt.x, pt.y)
      ctx.rotate(Math.PI / 4)
      ctx.fillStyle = 'rgba(53,230,255,.85)'
      ctx.shadowColor = 'rgba(53,230,255,.8)'
      ctx.shadowBlur = 8
      ctx.fillRect(-6, -6, 12, 12)
      ctx.restore()
      ctx.save()
      ctx.translate(pt.x, pt.y + 0.5)
      ctx.fillStyle = '#02040a'
      ctx.font = `700 11px ${orbitron.style.fontFamily}, sans-serif`
      ctx.textAlign = 'center'
      ctx.textBaseline = 'middle'
      ctx.fillText(`${m.km}K`, 0, 0)
      ctx.restore()
    }
  }

  function drawTargets(ctx: CanvasRenderingContext2D, map: MapLibreMap, tgts: SciFiTarget[]) {
    if (!tgts?.length) return
    for (const tgt of tgts) {
      const ring = geoCircle(tgt.lat, tgt.lng, Math.max(4, tgt.radius)).map((p) => map.project([p[1], p[0]]))
      const center = map.project([tgt.lng, tgt.lat])
      const color = tgt.done ? HUNT : FUG
      const rgb = tgt.done ? '255,63,160' : '53,230,255'
      ctx.save()
      ctx.globalCompositeOperation = 'lighter'
      ctx.beginPath()
      ring.forEach((p, i) => (i === 0 ? ctx.moveTo(p.x, p.y) : ctx.lineTo(p.x, p.y)))
      ctx.closePath()
      ctx.strokeStyle = color
      ctx.lineWidth = tgt.kind === 'focus' ? 3.5 : 2.5
      ctx.shadowColor = color
      ctx.shadowBlur = 12
      ctx.stroke()
      ctx.fillStyle = `rgba(${rgb},.12)`
      ctx.shadowBlur = 0
      ctx.fill()
      const topY = Math.min(...ring.map((p) => p.y))
      const pillarH = Math.max(24, center.y - topY) * 1.6
      const grad = ctx.createLinearGradient(center.x, center.y, center.x, center.y - pillarH)
      grad.addColorStop(0, `rgba(${rgb},.32)`)
      grad.addColorStop(1, `rgba(${rgb},0)`)
      ctx.fillStyle = grad
      ctx.fillRect(center.x - 3, center.y - pillarH, 6, pillarH)
      ctx.restore()
      if (tgt.done) {
        ctx.save()
        ctx.translate(center.x, center.y)
        ctx.strokeStyle = HUNT
        ctx.lineWidth = 3
        ctx.lineCap = 'round'
        ctx.lineJoin = 'round'
        ctx.beginPath(); ctx.moveTo(-6, 0); ctx.lineTo(-1, 5); ctx.lineTo(7, -7); ctx.stroke()
        ctx.restore()
      }
    }
  }

  return (
    <div ref={wrapRef} style={{ position: 'absolute', inset: 0 }}>
      <div ref={containerRef} className="scifi-map" style={{ position: 'absolute', inset: 0 }} />
      <canvas ref={canvasRef} className={orbitron.className} style={{ position: 'absolute', inset: 0, pointerEvents: 'none', display: 'block' }} />
    </div>
  )
})

export default SciFiMap
