'use client'

// 帳號風格覆寫（未來科幻 scifi／復古 RPG retro／溫馨可愛 cute）跑步軌跡歷史地圖
// （HISTORY_MAP_CONTRACT.md）。只在 track/history/page.tsx 判斷 getActiveSkin() 命中三者之一時，以
// next/dynamic(ssr:false) 動態載入本檔——非白名單使用者的 /track/history 首屏 bundle 完全不含這裡的
// 程式碼（比照 track/page.tsx 三個即時地圖的既有隔離原則）。
//
// 與即時追蹤地圖（track/scifi|retro|cute/*Map.tsx）的差異：這裡是「靜態俯視、單趟回放」，不需要跟隨
// 鏡頭／GPS 位置更新／專注模式／粒子動畫，pitch/bearing 恆為 0，掛載時 fitBounds 一次之後就只剩使用者
// 手動拖曳/縮放；軌跡／公里號碼／起終點一律用 MapLibre 原生圖層（GeoJSON source ＋ line/circle/symbol
// layer，圖示用 map.addImage() 產生的圖），不疊 2D canvas（避免即時地圖那套 DPR 疊層問題，契約逐字
// 「不要再疊 2D canvas」）。
//
// 資料流：segments／kmMarks 由呼叫端（history/page.tsx）用既有 decodePolylineSegments／
// kmMarkerPositions(segments, 1000/calibK) 算好傳入，本檔不重算、不碰那兩個純函式。每次選一筆新的
// 歷史紀錄，呼叫端會用 key={sel.id} 讓本元件整個重新掛載（見 history/page.tsx），因此本檔的建圖 effect
// 依賴陣列刻意留空（只在掛載時建一次），props 在同一個 instance 生命週期內視為不變。

import { useEffect, useRef } from 'react'
import {
  Map as MapLibreMap,
  AttributionControl,
  LngLatBounds,
  config as maplibreConfig,
  type MapOptions,
  type MissingStyleImageResolver,
  type LayerSpecification,
} from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { buildScifiStyle } from '../scifi/style'
import { buildRetroStyle } from '../retro/style'
import { buildCuteStyle } from '../cute/style'
import { tileImageData as retroTileImageData } from '../retro/sprites'
import { tileImageData as cuteTileImageData, type TileKind as CuteTileKind } from '../cute/tiles'
import type { TileKind as RetroTileKind } from '@/components/retro/tiles'
import { retroKmIcon, retroStartIcon, retroEndIcon, cuteKmIcon, cuteStartIcon, cuteEndIcon, type RouteIcon } from './skinRouteIcons'
import type { KmMarkerPoint } from '@/lib/kmMarkers'
import type { OverrideSkin } from '@/lib/skinOverride'

/* eslint-disable @typescript-eslint/no-explicit-any -- MapLibre style/paint 表達式型別在動態組 spec 時摩擦大，比照全站既有慣例（history/page.tsx 檔頭同一行） */

// maplibre-gl worker 自架修正：與 track/scifi|retro|cute/*Map.tsx 同一個根因/同一套修法（見
// scifi/SciFiMap.tsx 檔頭詳細註解）——三套風格＋本檔共寫同一個 module-level flag
// （maplibreConfig.WORKER_URL），同一個 maplibre-gl 套件實例的同一份設定，誰先執行都會設好、不會互相
// 覆蓋成不同值，不需要 import 對方檔案。
const MAPLIBRE_WORKER_URL = '/vendor/maplibre-gl-6.11.2/maplibre-gl-worker.mjs'
if (typeof window !== 'undefined' && !maplibreConfig.WORKER_URL) {
  maplibreConfig.WORKER_URL = MAPLIBRE_WORKER_URL
}

const LOAD_TIMEOUT_MS = 10000 // 契約：載入逾時 10 秒（document.hidden 期間暫停計時）
const CONTEXT_LOST_GRACE_MS = 5000 // 契約：webglcontextlost → 等 MapLibre 自行 restore 5 秒
const MAX_RECREATE_ATTEMPTS = 1 // 契約：未恢復則重建一次，再失敗才 Leaflet
const FIT_PADDING = 28
// retro 專用：km/起/終點圖示是 icon-anchor:'bottom' 的高瘦像素旗（skinRouteIcons.ts retroKmIcon 等），
// 若 GPS 點剛好落在 fitBounds 邊界附近，一般 28px padding 不夠讓整支旗子都留在畫布內，會被上緣/右緣
// 裁切（E2E 對照 diag_retro_km4_before.png 目視/像素證實：旗面被裁掉一截，非取樣方法問題）。retro 的
// fitBounds 因此加大上/右 padding 涵蓋旗子實際佔用的範圍；scifi/cute 的圖示都是置中對稱（circle 圖層
// ／icon-anchor:'center'），不需要這個加大。
// 2026-09-30 owner 檢視 history_preview 截圖後回饋「旗子太大、快蓋住整條路線」，skinRouteIcons.ts 把
// 旗子縮到約 53%（HISTORY_RETRO_ICON_SCALE，見該檔常數註解）；旗子實際佔用範圍（旗面頂端/右緣到
// 錨點的距離）同比例縮小，這裡的額外 padding 跟著等比縮（40 → 22，40×0.53≈21 再留一點安全邊）——
// 之後再調旗子尺寸，這裡要一起等比調整，不是各自獨立的數字。
const RETRO_FIT_PADDING = { top: FIT_PADDING + 22, right: FIT_PADDING + 22, bottom: FIT_PADDING, left: FIT_PADDING }

// 2026-09-30 owner 檢視 C:\Users\paris\Downloads\history_preview\ 截圖後回饋定案：scifi 底圖本身的
// 道路網也是青色，原本青色軌跡疊在上面很難跟道路分開；改用熱洋紅發光（外層寬光暈 #ff3fd0 系＋主線
// #ff5fe0＋白色細芯，見下方 addRouteSourceAndLayers() 的 scifi 分支）；flagged（異常）路線因此不能
// 再跟正常路線同色系，改琥珀橘 #ffb020（SCIFI_FLAG_COLOR）；起點光環維持青色（SCIFI_FUG，跟新路線
// 色系區隔、一眼辨識「青＝起」）、終點光環沿用洋紅（同路線主線色 SCIFI_ROUTE_CORE），並各自在旁邊
// 加「起」「終」文字標籤（見 addPointSourceAndLayers() 的 scifi 分支）。
const SCIFI_FUG = '#35e6ff' // 青：起點光環／km 號碼牌描邊
const SCIFI_ROUTE_GLOW = '#ff3fd0' // 洋紅光暈：路線外層 line-blur 色
const SCIFI_ROUTE_CORE = '#ff5fe0' // 洋紅主線：路線核心色，同時也是終點光環描邊色
const SCIFI_FLAG_COLOR = '#ffb020' // 琥珀橘：flagged（異常）時的路線色，取代原本跟路線同色系的洋紅
const RETRO_GOLD = '#f8b800'
const RETRO_RED = '#e4462c' // RETRO_PALETTE 最後一色（components/retro/tiles.ts 原註解「圍巾/劍柄護手紅」）
const CUTE_LAVENDER = '#d9c8ff' // CUTE_PALETTE.lavender（薰衣草）
const CUTE_CANDY = '#ff9fc8' // CUTE_PALETTE.candy（糖果粉）
const CUTE_CORAL = '#ff9aa8' // CUTE_PALETTE.peachCoral（警示色：珊瑚紅）

const RETRO_TILE_IDS: readonly RetroTileKind[] = ['grass', 'forest', 'water', 'wall', 'cobble', 'path', 'sand', 'rail']
const CUTE_TILE_IDS: readonly CuteTileKind[] = ['tree', 'wave']
const CUTE_STYLE_IMAGE_IDS: Record<CuteTileKind, string> = { tree: 'cute-tree', wave: 'cute-wave' }
// 公里號碼超過 42 個（等於軌跡超過 42 公里的超馬等級）時只畫 5 的倍數，比照 lib/kmMarkers.ts
// addKmMarkers() 對 Leaflet 版本的既有規則，讓風格地圖與非白名單使用者看到的密度一致。
function kmMarksToRender(kmMarks: KmMarkerPoint[]): KmMarkerPoint[] {
  return kmMarks.length > 42 ? kmMarks.filter((m) => m.km % 5 === 0) : kmMarks
}

function isWebglSupported(): boolean {
  try {
    const canvas = document.createElement('canvas')
    return !!(canvas.getContext('webgl2') || canvas.getContext('webgl') || canvas.getContext('experimental-webgl'))
  } catch {
    return false
  }
}

function firstCoord(segs: [number, number][][]): [number, number] | null {
  for (const seg of segs) if (seg.length) return seg[0]
  return null
}
function lastCoord(segs: [number, number][][]): [number, number] | null {
  for (let i = segs.length - 1; i >= 0; i--) {
    const seg = segs[i]
    if (seg.length) return seg[seg.length - 1]
  }
  return null
}

export interface SkinRouteMapProps {
  skin: OverrideSkin
  segments: [number, number][][] // decodePolylineSegments 產出的 [lat,lng][][]，段落間不連線
  kmMarks: KmMarkerPoint[] // kmMarkerPositions(segments, 1000/calibK) 產出，呼叫端算好傳入，不可改算法
  flagged: boolean
  onFallback: (reason: string) => void // WebGL 不支援／逾時／context lost／例外 → 父層卸載本元件、恢復 Leaflet
}

export default function SkinRouteMap({ skin, segments, kmMarks, flagged, onFallback }: SkinRouteMapProps) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const mapRef = useRef<MapLibreMap | null>(null)
  const onFallbackRef = useRef(onFallback)
  onFallbackRef.current = onFallback

  useEffect(() => {
    if (!containerRef.current) return
    if (!isWebglSupported()) { onFallbackRef.current('webgl-unsupported'); return }
    const totalPts = segments.reduce((n, seg) => n + seg.length, 0)
    // 沒有任何可畫的座標：與其建一張空地圖，不如直接退回 Leaflet（那邊一樣什麼都畫不出來，行為一致）。
    if (totalPts < 2) { onFallbackRef.current('no-route-data'); return }

    const kmPts = kmMarksToRender(kmMarks)
    const bounds = new LngLatBounds()
    for (const seg of segments) for (const [lat, lng] of seg) bounds.extend([lng, lat])
    const first = firstCoord(segments)
    const last = lastCoord(segments)

    let cancelled = false
    let timeoutId: ReturnType<typeof setTimeout> | null = null
    let contextLostGraceTimer: ReturnType<typeof setTimeout> | null = null
    let ro: ResizeObserver | null = null
    let map: MapLibreMap | null = null
    let loaded = false
    let contextLost = false
    let recreateAttempts = 0
    let failed = false
    let routeHidden = false
    const layerIds: string[] = [] // setRouteHidden() 要一起切換可見度的所有圖層（見 updateDebug）

    const isCurrent = () => !cancelled && mapRef.current === map

    function detachListeners() {
      try {
        map?.off('load', onLoad)
        map?.off('error', onError)
        map?.off('moveend', onMoveEnd)
        map?.off('zoomend', onMoveEnd)
        map?.off('webglcontextlost', onContextLost)
        map?.off('webglcontextrestored', onContextRestored)
        map?.setMissingStyleImageResolver(null as unknown as MissingStyleImageResolver)
      } catch { /* ignore */ }
    }

    const failInstance = (reason: string) => {
      if (failed || !isCurrent()) return
      failed = true
      // eslint-disable-next-line no-console
      console.warn('[history-skin-map] fallback', reason)
      if (contextLostGraceTimer) { clearTimeout(contextLostGraceTimer); contextLostGraceTimer = null }
      if (timeoutId) { clearTimeout(timeoutId); timeoutId = null }
      ro?.disconnect()
      detachListeners()
      try { map?.remove() } catch { /* ignore */ }
      if (mapRef.current === map) mapRef.current = null
      try { delete (window as unknown as { __historyMapDebug?: unknown }).__historyMapDebug } catch { /* ignore */ }
      onFallbackRef.current(reason)
    }

    const armLoadTimeout = () => {
      if (!isCurrent() || loaded || document.hidden) return
      if (timeoutId) clearTimeout(timeoutId)
      timeoutId = setTimeout(() => {
        timeoutId = null
        if (isCurrent() && !loaded) failInstance('load-timeout-10s')
      }, LOAD_TIMEOUT_MS)
    }
    const armContextLostGrace = () => {
      if (!isCurrent() || !contextLost) return
      if (document.hidden) return
      if (contextLostGraceTimer) clearTimeout(contextLostGraceTimer)
      contextLostGraceTimer = setTimeout(() => {
        contextLostGraceTimer = null
        if (!isCurrent() || !contextLost) return
        if (recreateAttempts >= MAX_RECREATE_ATTEMPTS) { failInstance('webglcontextlost-unrecovered'); return }
        recreateAttempts += 1
        recreateInstance()
      }, CONTEXT_LOST_GRACE_MS)
    }
    const onVisibilityChange = () => {
      if (document.hidden) {
        if (timeoutId) { clearTimeout(timeoutId); timeoutId = null }
        if (contextLostGraceTimer) { clearTimeout(contextLostGraceTimer); contextLostGraceTimer = null }
        return
      }
      armLoadTimeout()
      armContextLostGrace()
    }
    document.addEventListener('visibilitychange', onVisibilityChange)

    const onContextLost = () => {
      if (!isCurrent()) return
      contextLost = true
      // eslint-disable-next-line no-console
      console.warn('[history-skin-map] webglcontextlost, waiting for auto-recovery')
      armContextLostGrace()
    }
    const onContextRestored = () => {
      if (!isCurrent() || !contextLost) return
      contextLost = false
      if (contextLostGraceTimer) { clearTimeout(contextLostGraceTimer); contextLostGraceTimer = null }
      recreateAttempts = 0
      // eslint-disable-next-line no-console
      console.warn('[history-skin-map] webglcontextrestored, recovered')
      if (skin === 'retro') { try { map!.getCanvas().style.imageRendering = 'pixelated' } catch { /* ignore */ } }
      // 既有 sources/layers 由 MapLibre 自己在 context 復原後重新繪製，不需要重新 addSource/addLayer
      // （那是給「整個 instance 被砍掉重建」的 recreateInstance() 用的，見下方）。
    }
    const onError = (e: unknown) => {
      if (!isCurrent()) return
      if (contextLost && recreateAttempts < MAX_RECREATE_ATTEMPTS) {
        if (contextLostGraceTimer) { clearTimeout(contextLostGraceTimer); contextLostGraceTimer = null }
        recreateAttempts += 1
        recreateInstance()
        return
      }
      failInstance('map-error:' + ((e as { error?: { message?: string } })?.error?.message || 'unknown'))
    }

    // retro/cute 重用既有 style builder 的地面圖塊（fill-pattern/line-pattern）：圖片懶載入一律走
    // map.setMissingStyleImageResolver()（不可用 'styleimagemissing' 事件——見 track/cute/CuteMap.tsx
    // resolveMissingImage 上方的根因註解，MapLibre ImageManager 時序陷阱會讓「第一個要求該圖片的圖磚」
    // 永遠拿不到圖，契約逐字要求比照辦理）。scifi 的 buildScifiStyle() 沒有任何 fill-pattern，不需要
    // 註冊任何 id。
    const onImageMissing: MissingStyleImageResolver = (id) => {
      if (!isCurrent() || !map) return
      if (map.hasImage(id)) return
      if (skin === 'retro' && (RETRO_TILE_IDS as readonly string[]).includes(id)) {
        try { map.addImage(id, id === 'water' ? retroTileImageData('water', 0) : retroTileImageData(id as RetroTileKind)) } catch { /* ignore：單一圖塊掛不上不影響其他圖層 */ }
        return
      }
      if (skin === 'cute') {
        const kind = (Object.keys(CUTE_STYLE_IMAGE_IDS) as CuteTileKind[]).find((k) => CUTE_STYLE_IMAGE_IDS[k] === id)
        if (kind && CUTE_TILE_IDS.includes(kind)) {
          try { map.addImage(id, cuteTileImageData(kind)) } catch { /* ignore */ }
        }
      }
    }

    function buildStyle() {
      if (skin === 'scifi') return buildScifiStyle()
      if (skin === 'retro') return buildRetroStyle()
      return buildCuteStyle()
    }

    function addLayer(spec: LayerSpecification) {
      map!.addLayer(spec)
      layerIds.push(spec.id)
    }

    function routeColor(): string {
      if (skin === 'scifi') return flagged ? SCIFI_FLAG_COLOR : SCIFI_ROUTE_CORE
      if (skin === 'retro') return flagged ? RETRO_RED : RETRO_GOLD
      return flagged ? CUTE_CORAL : CUTE_CANDY
    }

    // scifi 專用：路線外層光暈色與主線色故意分開（見檔頭常數註解），主線／終點光環色沿用 routeColor()，
    // 這裡另外算光暈色。
    function scifiGlowColor(): string {
      return flagged ? SCIFI_FLAG_COLOR : SCIFI_ROUTE_GLOW
    }

    // 圖示：retro/cute 一律事先 addImage 掛好（見 registerIcons）——km 號碼＋起點＋終點的圖片 id 在
    // 掛載當下就完全已知（不像地面圖塊「哪塊圖磚先請求哪個 id」無法預先得知），不需要
    // setMissingStyleImageResolver 那套懶載入機制。
    function registerIcons() {
      if (skin === 'scifi') return
      const dpr = window.devicePixelRatio || 1
      const icons: RouteIcon[] = []
      if (skin === 'retro') {
        for (const m of kmPts) icons.push(retroKmIcon(m.km, dpr))
        icons.push(retroStartIcon(dpr), retroEndIcon(dpr))
      } else {
        for (const m of kmPts) icons.push(cuteKmIcon(m.km, dpr))
        icons.push(cuteStartIcon(dpr), cuteEndIcon(dpr))
      }
      for (const icon of icons) {
        try { if (!map!.hasImage(icon.id)) map!.addImage(icon.id, icon.data, { pixelRatio: icon.pixelRatio }) } catch { /* ignore：單一圖示掛不上不影響其他圖層 */ }
      }
    }

    function addRouteSourceAndLayers() {
      const features = segments.filter((seg) => seg.length > 1).map((seg) => ({
        type: 'Feature' as const,
        properties: {},
        geometry: { type: 'LineString' as const, coordinates: seg.map(([lat, lng]) => [lng, lat]) },
      }))
      // lineMetrics:true：cute 的漸層緞帶靠 ['line-progress'] 需要（見下方），scifi/retro 不使用
      // line-gradient，宣告了也無害，三套風格共用同一個 source 定義比較簡單。
      map!.addSource('history-route', { type: 'geojson', lineMetrics: true, data: { type: 'FeatureCollection', features } })

      const color = routeColor()
      if (skin === 'scifi') {
        // 熱洋紅發光軌跡（2026-09-30 owner 定案，見檔頭常數註解）：外層寬光暈(line-blur，色號跟主線
        // 故意不同做出層次感)＋主線＋白色細芯。
        addLayer({ id: 'history-route-glow', type: 'line', source: 'history-route', layout: { 'line-cap': 'round', 'line-join': 'round' }, paint: { 'line-color': scifiGlowColor(), 'line-width': 14, 'line-blur': 14, 'line-opacity': 0.35 } } as LayerSpecification)
        addLayer({ id: 'history-route-core', type: 'line', source: 'history-route', layout: { 'line-cap': 'round', 'line-join': 'round' }, paint: { 'line-color': color, 'line-width': 5, 'line-opacity': 0.95 } } as LayerSpecification)
        addLayer({ id: 'history-route-white', type: 'line', source: 'history-route', layout: { 'line-cap': 'round', 'line-join': 'round' }, paint: { 'line-color': '#ffffff', 'line-width': 1.6, 'line-opacity': 0.9 } } as LayerSpecification)
      } else if (skin === 'retro') {
        // 黑框金色像素光路：黑色外框線＋金黃主線，方頭方角（line-cap butt / line-join miter，契約逐字）。
        addLayer({ id: 'history-route-outline', type: 'line', source: 'history-route', layout: { 'line-cap': 'butt', 'line-join': 'miter' }, paint: { 'line-color': '#000000', 'line-width': 8 } } as LayerSpecification)
        addLayer({ id: 'history-route-core', type: 'line', source: 'history-route', layout: { 'line-cap': 'butt', 'line-join': 'miter' }, paint: { 'line-color': color, 'line-width': 4 } } as LayerSpecification)
      } else {
        // 薰衣草→糖果粉漸層發光緞帶（line-gradient）＋白芯；flagged 時改用該 skin 警示色的平面色
        // （不套漸層，契約「軌跡改該 skin 的警示色」）。
        if (flagged) {
          addLayer({ id: 'history-route-glow', type: 'line', source: 'history-route', layout: { 'line-cap': 'round', 'line-join': 'round' }, paint: { 'line-color': color, 'line-width': 14, 'line-blur': 10, 'line-opacity': 0.35 } } as LayerSpecification)
          addLayer({ id: 'history-route-core', type: 'line', source: 'history-route', layout: { 'line-cap': 'round', 'line-join': 'round' }, paint: { 'line-color': color, 'line-width': 5, 'line-opacity': 0.95 } } as LayerSpecification)
        } else {
          const gradient = ['interpolate', ['linear'], ['line-progress'], 0, CUTE_LAVENDER, 1, CUTE_CANDY]
          addLayer({ id: 'history-route-glow', type: 'line', source: 'history-route', layout: { 'line-cap': 'round', 'line-join': 'round' }, paint: { 'line-gradient': gradient, 'line-width': 14, 'line-blur': 10, 'line-opacity': 0.35 } } as unknown as LayerSpecification)
          addLayer({ id: 'history-route-core', type: 'line', source: 'history-route', layout: { 'line-cap': 'round', 'line-join': 'round' }, paint: { 'line-gradient': gradient, 'line-width': 5, 'line-opacity': 0.95 } } as unknown as LayerSpecification)
        }
        addLayer({ id: 'history-route-white', type: 'line', source: 'history-route', layout: { 'line-cap': 'round', 'line-join': 'round' }, paint: { 'line-color': '#ffffff', 'line-width': 1.6, 'line-opacity': 0.85 } } as LayerSpecification)
      }
    }

    function addPointSourceAndLayers() {
      const kmFeatures = kmPts.map((m) => ({ type: 'Feature' as const, properties: { km: m.km }, geometry: { type: 'Point' as const, coordinates: [m.lng, m.lat] } }))
      map!.addSource('history-km', { type: 'geojson', data: { type: 'FeatureCollection', features: kmFeatures } })
      const endFeatures: any[] = []
      if (first) endFeatures.push({ type: 'Feature' as const, properties: { kind: 'start' }, geometry: { type: 'Point' as const, coordinates: [first[1], first[0]] } })
      if (last) endFeatures.push({ type: 'Feature' as const, properties: { kind: 'end' }, geometry: { type: 'Point' as const, coordinates: [last[1], last[0]] } })
      map!.addSource('history-ends', { type: 'geojson', data: { type: 'FeatureCollection', features: endFeatures } })

      if (skin === 'scifi') {
        // 公里號碼＝青框深底圓牌＋白字：circle 圖層當底牌＋symbol 文字圖層疊字（契約逐字），不需要
        // addImage（buildScifiStyle() 已宣告 glyphs，Noto Sans Bold 已核對存在，見 scifi/style.ts 檔頭）。
        addLayer({ id: 'history-km-badge', type: 'circle', source: 'history-km', paint: { 'circle-radius': 11, 'circle-color': '#02040a', 'circle-opacity': 0.92, 'circle-stroke-color': SCIFI_FUG, 'circle-stroke-width': 2 } } as LayerSpecification)
        addLayer({ id: 'history-km-text', type: 'symbol', source: 'history-km', layout: { 'text-field': ['to-string', ['get', 'km']], 'text-font': ['Noto Sans Bold'], 'text-size': 11, 'text-allow-overlap': true, 'text-ignore-placement': true } as any, paint: { 'text-color': '#ffffff' } } as LayerSpecification)
        // 起／終點（2026-09-30 owner 定案）：原本只用 circle-blur 做光暈，owner 截圖回饋「起點標記太
        // 淡」——改成白底圓心＋粗描邊做出實心環（直徑 = radius9×2 + stroke-width3 ≈ 21 CSS px，落在
        // owner 要求的 18–22px 區間），外側再疊一圈更淡的光暈保留科幻發光感；起＝青（SCIFI_FUG，跟新
        // 路線的洋紅區隔）、終＝洋紅（SCIFI_ROUTE_CORE，同路線主線色）。旁邊各加「起」「終」文字標籤
        // （白字＋深底 halo，text-anchor:'left' 從環外側往右延伸，不會被環本體蓋住；CJK 字元靠
        // mountInstance() 已設的 localIdeographFontFamily 本地渲染，不需要額外 glyphs 請求）。
        addLayer({ id: 'history-start-glow', type: 'circle', source: 'history-ends', filter: ['==', ['get', 'kind'], 'start'], paint: { 'circle-radius': 13, 'circle-color': 'rgba(53,230,255,.28)', 'circle-blur': 0.7 } } as LayerSpecification)
        addLayer({ id: 'history-start-ring', type: 'circle', source: 'history-ends', filter: ['==', ['get', 'kind'], 'start'], paint: { 'circle-radius': 9, 'circle-color': '#ffffff', 'circle-opacity': 0.95, 'circle-stroke-color': SCIFI_FUG, 'circle-stroke-width': 3 } } as LayerSpecification)
        addLayer({ id: 'history-start-label', type: 'symbol', source: 'history-ends', filter: ['==', ['get', 'kind'], 'start'], layout: { 'text-field': '起', 'text-font': ['Noto Sans Bold'], 'text-size': 13, 'text-offset': [1.4, 0], 'text-anchor': 'left', 'text-allow-overlap': true, 'text-ignore-placement': true } as any, paint: { 'text-color': '#ffffff', 'text-halo-color': '#02040a', 'text-halo-width': 1.6 } } as LayerSpecification)
        addLayer({ id: 'history-end-glow', type: 'circle', source: 'history-ends', filter: ['==', ['get', 'kind'], 'end'], paint: { 'circle-radius': 13, 'circle-color': 'rgba(255,95,224,.28)', 'circle-blur': 0.7 } } as LayerSpecification)
        addLayer({ id: 'history-end-ring', type: 'circle', source: 'history-ends', filter: ['==', ['get', 'kind'], 'end'], paint: { 'circle-radius': 9, 'circle-color': '#ffffff', 'circle-opacity': 0.95, 'circle-stroke-color': SCIFI_ROUTE_CORE, 'circle-stroke-width': 3 } } as LayerSpecification)
        addLayer({ id: 'history-end-label', type: 'symbol', source: 'history-ends', filter: ['==', ['get', 'kind'], 'end'], layout: { 'text-field': '終', 'text-font': ['Noto Sans Bold'], 'text-size': 13, 'text-offset': [1.4, 0], 'text-anchor': 'left', 'text-allow-overlap': true, 'text-ignore-placement': true } as any, paint: { 'text-color': '#ffffff', 'text-halo-color': '#02040a', 'text-halo-width': 1.6 } } as LayerSpecification)
      } else {
        // retro：像素公里旗／起點綠旗／終點格子旗，旗杆底部＝GPS 點，icon-anchor 'bottom'。
        // cute：愛心徽章／起點小圓點／終點小愛心，幾何中心＝GPS 點，icon-anchor 'center'。
        const anchor = skin === 'cute' ? 'center' : 'bottom'
        addLayer({ id: 'history-km-icon', type: 'symbol', source: 'history-km', layout: { 'icon-image': ['concat', `${skin}-km-`, ['to-string', ['get', 'km']]], 'icon-anchor': anchor, 'icon-allow-overlap': true, 'icon-ignore-placement': true } as any } as LayerSpecification)
        addLayer({ id: 'history-start-icon', type: 'symbol', source: 'history-ends', filter: ['==', ['get', 'kind'], 'start'], layout: { 'icon-image': `${skin}-start`, 'icon-anchor': anchor, 'icon-allow-overlap': true, 'icon-ignore-placement': true } as any } as LayerSpecification)
        addLayer({ id: 'history-end-icon', type: 'symbol', source: 'history-ends', filter: ['==', ['get', 'kind'], 'end'], layout: { 'icon-image': `${skin}-end`, 'icon-anchor': anchor, 'icon-allow-overlap': true, 'icon-ignore-placement': true } as any } as LayerSpecification)
      }
    }

    function applyLayers() {
      registerIcons()
      addRouteSourceAndLayers()
      addPointSourceAndLayers()
    }

    // ── 偵錯把手（契約逐字：skin／loaded／routeBounds／kmCount／kmScreens／startScreen／endScreen／
    // fallbackReason／setRouteHidden(bool)）：不用 setInterval 快照（本頁是靜態地圖，事件驅動即可，
    // 不需要像即時追蹤地圖那樣每秒輪詢）——load／moveend／zoomend 時各更新一次。
    function updateDebug() {
      if (!map) return
      const sw = bounds.getSouthWest(), ne = bounds.getNorthEast()
      const proj = (lng: number, lat: number) => { try { const p = map!.project([lng, lat]); return { x: Math.round(p.x), y: Math.round(p.y) } } catch { return null } }
      ;(window as unknown as Record<string, unknown>).__historyMapDebug = {
        skin,
        loaded,
        routeBounds: { sw: { lat: sw.lat, lng: sw.lng }, ne: { lat: ne.lat, lng: ne.lng } },
        kmCount: kmPts.length,
        kmScreens: kmPts.map((m) => ({ km: m.km, ...(proj(m.lng, m.lat) || { x: -1, y: -1 }) })),
        startScreen: first ? proj(first[1], first[0]) : null,
        endScreen: last ? proj(last[1], last[0]) : null,
        fallbackReason: null as string | null,
        routeHidden,
        projectLngLat: (lng: number, lat: number) => { try { const p = map!.project([lng, lat]); return { x: p.x, y: p.y } } catch { return null } },
        setRouteHidden: (hidden: boolean) => {
          routeHidden = hidden
          const vis = hidden ? 'none' : 'visible'
          for (const id of layerIds) { try { map!.setLayoutProperty(id, 'visibility', vis) } catch { /* ignore */ } }
          const dbg = (window as unknown as Record<string, any>).__historyMapDebug
          if (dbg) dbg.routeHidden = hidden
        },
      }
    }

    const onLoad = () => {
      if (!isCurrent()) return
      loaded = true
      try { containerRef.current?.querySelector('.maplibregl-ctrl-attrib')?.classList.remove('maplibregl-compact-show') } catch { /* ignore */ }
      if (skin === 'retro') { try { map!.getCanvas().style.imageRendering = 'pixelated' } catch { /* ignore */ } }
      applyLayers()
      updateDebug()
    }
    const onMoveEnd = () => { if (isCurrent() && loaded) updateDebug() }

    function mountInstance() {
      if (!containerRef.current) { failInstance('recreate-no-container'); return }
      try {
        const dpr = window.devicePixelRatio || 1
        const opts: Record<string, unknown> = {
          container: containerRef.current,
          style: buildStyle() as unknown as MapOptions['style'],
          bounds: bounds as unknown as MapOptions['bounds'],
          fitBoundsOptions: { padding: skin === 'retro' ? RETRO_FIT_PADDING : FIT_PADDING, animate: false },
          pitch: 0,
          bearing: 0,
          maxPitch: 0,
          dragRotate: false,
          pitchWithRotate: false,
          touchPitch: false,
          attributionControl: false,
        }
        if (skin === 'retro') {
          // 像素化（比照 track/retro/RetroMap.tsx）：低 pixelRatio＋canvas image-rendering:pixelated，
          // 讓歷史地圖的底圖磚跟即時追蹤地圖視覺一致。
          opts.antialias = false
          opts.pixelRatio = Math.max(0.28, dpr / 3)
        } else if (skin === 'scifi') {
          opts.antialias = true
          opts.localIdeographFontFamily = "'Noto Sans TC','Microsoft JhengHei',sans-serif"
        } else {
          opts.localIdeographFontFamily = "'DORCute','Noto Sans TC','Microsoft JhengHei',sans-serif"
        }
        map = new MapLibreMap(opts as MapOptions)
        try { map.addControl(new AttributionControl({ compact: true }), 'bottom-right') } catch { /* ignore */ }
        try { map.touchZoomRotate.disableRotation() } catch { /* ignore */ }
        mapRef.current = map
        loaded = false
        contextLost = false
        // 建構後立刻設好（任何圖磚被請求之前），見上方 onImageMissing 說明。
        map.setMissingStyleImageResolver(onImageMissing)

        armLoadTimeout()
        map.on('load', onLoad)
        map.on('error', onError)
        map.on('moveend', onMoveEnd)
        map.on('zoomend', onMoveEnd)
        map.on('webglcontextlost', onContextLost)
        map.on('webglcontextrestored', onContextRestored)

        ro?.disconnect()
        ro = new ResizeObserver(() => { if (isCurrent()) { try { map!.resize() } catch { /* ignore */ } } })
        ro.observe(containerRef.current)
      } catch (e) {
        failInstance('init-exception:' + ((e as Error)?.message || String(e)))
      }
    }

    // 寬限期逾時仍未 webglcontextrestored：整個 instance 就地重建一次（契約「重建一次，再失敗才
    // Leaflet」）；沿用同一組 bounds/fitBoundsOptions 重新 fitBounds，靜態地圖不需要保留使用者當下的
    // 鏡頭位置（跟即時追蹤地圖保留跟隨鏡頭的理由不同，這裡直接回到「看得到全程」的預設視角更合理）。
    function recreateInstance() {
      if (!isCurrent()) return
      // eslint-disable-next-line no-console
      console.warn('[history-skin-map] recreating instance after unrecovered context loss, attempt', recreateAttempts)
      detachListeners()
      ro?.disconnect()
      try { map?.remove() } catch { /* ignore */ }
      mapRef.current = null
      map = null
      layerIds.length = 0
      mountInstance()
    }

    mountInstance()

    return () => {
      cancelled = true
      document.removeEventListener('visibilitychange', onVisibilityChange)
      if (timeoutId) clearTimeout(timeoutId)
      if (contextLostGraceTimer) clearTimeout(contextLostGraceTimer)
      ro?.disconnect()
      detachListeners() // 先移除監聽，再 map.remove()（比照三個即時地圖元件的既有慣例）
      try { map?.remove() } catch { /* ignore */ }
      if (mapRef.current === map) mapRef.current = null
      try { delete (window as unknown as { __historyMapDebug?: unknown }).__historyMapDebug } catch { /* ignore */ }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 只在掛載時建圖一次；呼叫端用 key={sel.id} 保證每筆歷史紀錄各自重新掛載（見檔頭說明），props 在同一 instance 生命週期內視為不變
  }, [])

  return (
    <div style={{ position: 'absolute', inset: 0 }}>
      <div ref={containerRef} className={`${skin}-history-map`} style={{ position: 'absolute', inset: 0 }} />
      {/* 左上 +／− 按鈕：比照 track/page.tsx 即時地圖同一組按鈕視覺（var(--bg-1)/var(--fug)/var(--line-2)），
          契約「用 skin 樣式」——這三個 CSS 變數本來就會依目前生效的 data-skin 換膚，不需要另外分色。 */}
      <div style={{ position: 'absolute', top: 6, left: 6, zIndex: 5, display: 'flex', flexDirection: 'column', gap: 4 }}>
        <button
          type="button"
          aria-label="放大"
          onClick={() => { try { mapRef.current?.zoomIn() } catch { /* ignore */ } }}
          style={{ width: 26, height: 26, borderRadius: 8, background: 'var(--bg-1)', color: 'var(--fug)', border: '1px solid var(--line-2)', fontSize: 14, fontWeight: 800, cursor: 'pointer', lineHeight: 1 }}
        >＋</button>
        <button
          type="button"
          aria-label="縮小"
          onClick={() => { try { mapRef.current?.zoomOut() } catch { /* ignore */ } }}
          style={{ width: 26, height: 26, borderRadius: 8, background: 'var(--bg-1)', color: 'var(--fug)', border: '1px solid var(--line-2)', fontSize: 14, fontWeight: 800, cursor: 'pointer', lineHeight: 1 }}
        >－</button>
      </div>
    </div>
  )
}
