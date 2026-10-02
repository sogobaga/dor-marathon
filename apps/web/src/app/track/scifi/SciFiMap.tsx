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
import { Map as MapLibreMap, AttributionControl, config as maplibreConfig, type MapOptions, type LngLatLike, type MapMouseEvent, type MapMovementEvent } from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { buildScifiStyle } from './style'
import { orbitron } from './font'
import { Soul, TrailPool } from './particles'
import { LightRain } from './rain'
import type { SciFiMapHandle, SciFiMapProps, SciFiTarget, SciFiPos } from './types'

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
// review round 2 抓到的 MAJOR 缺口（docs/skins/TRACK_HYDRATION_CONTRACT.md 修法 4）：上面
// FALLBACK_TIMEOUT_MS 只保護「map 'load' 事件之前」的逾時，但 MapLibre 把「已嘗試過、即使失敗」的
// tile 也算進 loaded() 判斷（node_modules/maplibre-gl/src/tile/tile_manager.ts loaded()：tile.state
// 為 'loaded' 或 'errored' 都算數）——也就是說即使初始視野的圖塊 100% 失敗，'load' 事件通常仍會正常
// fire，loadedRef.current 變 true，上面的 8 秒逾時從此完全失效；加上修法 4 把帶 sourceId/tile 的
// error 一律吞掉不退回，兩者疊加會出現「地圖已判定為『載入完成』但畫面其實整片空白、之後也再也不會
// 有任何機會退回 Leaflet」的情況（單一圖塊失敗不該退回，但『全部圖塊持續失敗』不該永遠空白）。
// 修法：獨立追蹤「持續多久沒有任何一顆圖塊真正成功」，逾時才視同載入失敗退回——見下方 onError 內
// 圖塊/來源錯誤分支（起算）與 onData 內圖塊成功分支（清除／視為已復原）。20 秒明顯長於既有 pct30
// 測試情境的觀察窗（13 秒，33% 圖塊持續失敗但仍有 2/3 成功、會不斷清掉這顆計時器），不會誤退。
const TILE_STALL_TIMEOUT_MS = 20000
// 底圖向量來源 id（三種風格 style.ts 都叫 'openmaptiles'）：stall 偵測與失敗圖塊重試只看底圖，不讓我們自己
// 加的 GeoJSON 來源（歷史頁路線／公里標記）發出的成功事件把偵測關掉（2026-10-02 審查 minor 3）。
const BASE_TILE_SOURCE = 'openmaptiles'
// 失敗圖塊重試（2026-10-02 審查 minor 4）：MapLibre 不會自動重抓狀態為 errored 的圖塊（tile_manager 的
// _addTile 直接回傳舊的 errored tile），單一失敗會留下一塊空白直到移出視野。只重抓失敗的那幾顆
// （map.refreshTiles），依退避間隔最多 3 輪；背景中不重試；網路恢復（online）或回到前景時重新給 3 輪
// （第二輪審查：斷網超過約一分鐘時，舊版 3 輪早已用完，網路好了也不再重抓）。
const TILE_RETRY_DELAYS_MS = [8000, 20000, 45000]
const FUG = '#35e6ff'
const HUNT = '#ff3fa0'
const LAST_POS_KEY = 'dor_scifi_last_pos' // CONTRACT_R2.md §2：SciFiMap 自行在每次 pos 更新時寫入，供下次
// 開頁在拿到真實定位前，初始中心就能落在「最後已知位置」而非假的城市中心
const RESUME_FOLLOW_MS = 8000 // 使用者拖曳／縮放後暫停跟隨，8 秒後自動恢復（CONTRACT_R2.md §2）
// docs/skins/ORBPOS_CONTRACT.md §B：「搜尋中」外觀判定門檻，數值與 cute/retro 同一慣例（各檔獨立
// 宣告，理由同檔頭 MAPLIBRE_WORKER_URL 之後那段隔離說明）。
const MAX_ORB_ACC = 65
const STALE_FIX_MS = 15000
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
// GAP #1 修復：fitRoute() 縮放到看得見整條建議路線時，四周留白（CSS px）＋最多放大到多少 zoom（路線
// 很短時避免貼到幾乎看不出地圖，比照一般地圖 App「導航路線總覽」慣例）。
const ROUTE_FIT_EXTRA_PADDING = 48
const ROUTE_FIT_MAX_ZOOM = 17

function writeLastPos(lat: number, lng: number) {
  try { localStorage.setItem(LAST_POS_KEY, JSON.stringify({ lat, lng })) } catch { /* 私密瀏覽/storage 被封鎖：純錦上添花，略過即可 */ }
}

function isWebglSupported(): boolean {
  try {
    const canvas = document.createElement('canvas')
    return !!(canvas.getContext('webgl2') || canvas.getContext('webgl') || canvas.getContext('experimental-webgl'))
  } catch { return false }
}

// metersPerPixel：地面 1 公尺對應多少「CSS px」，只用來把公尺半徑的目標圈換算成螢幕點擊命中半徑
// （見下方 onClick 的 pxR = tgt.radius/metersPerPixel(...)）。FIX（根因調查，見 scratchpad
// cute_skin/park_repro/mpp_check.html：用 MapLibre 6.11.2 實例本身的 map.unproject() 在螢幕上取
// 100px 距離、haversine 換算回公尺，跟這條公式的結果對照，與 cute/retro 的 geo.ts 同一份調查一次查
// 三份同源程式碼）——這條公式原本抄自 Google Maps／Leaflet 那種「256px 圖磚」慣例（156543.03392 =
// 赤道周長 40075016.6856m ÷ 256），但 MapLibre／Mapbox GL 的 zoom 是建立在 512px 圖磚上
// （node_modules/maplibre-gl/src/geo/transform_helper.ts `_tileSize = 512`、
// `worldSize = tileSize * 2^zoom`），同一個 zoom 數字下世界實際攤開的像素寬度是 256px 慣例的兩倍，
// 換算下來每個 CSS px 代表的實際公尺數只有這條公式算出來的一半——大安森林公園 zoom16.5 實測：舊公式
// 算出 1.5304 m/px，map.unproject() 實測 0.7644 m/px（比值 2.0022），改半後的 0.7652 m/px 只差
// 0.11%（殘差是小範圍球面近似的正常誤差）。這個公式只有這裡（換算點擊命中半徑）用到，繪製地面圈的
// geoCircle() 是直接用公尺→經緯度差逐點交給 map.project()，不受這個誤差影響——換句話說畫面上的圈
// 本來就是正確的公尺半徑，只有「點下去判不判定有點到」的半徑一直只有畫面看起來的一半，玩家得點得比
// 視覺圈精準兩倍才點得到；改成正確係數（原常數除以 2）修正。
function metersPerPixel(lat: number, zoom: number): number {
  return (78271.51696 * Math.cos((lat * Math.PI) / 180)) / Math.pow(2, zoom)
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

// __scifiDebug.plannedRouteScreenSample 專用：均勻取「最多 n 點」（含頭尾），不像 decimate() 那樣會
// 額外多塞一個尾端點（decimate 用途是畫圖不在意剛好幾點，這裡契約明確要求「up to 5」，多一點就超標）。
function sampleUpTo<T>(arr: T[], n: number): T[] {
  if (arr.length <= n) return arr
  const out: T[] = []
  const step = (arr.length - 1) / (n - 1)
  for (let i = 0; i < n; i++) out.push(arr[Math.round(i * step)])
  return out
}

const SciFiMap = forwardRef<SciFiMapHandle, SciFiMapProps>(function SciFiMap(props, ref) {
  const { pos, status, segments, kmMarks, targets, focusMode, initialCenter, initialZoom, onFallback, onTargetClick, bottomInset, onFollowChange } = props
  // 2026-09-30「切換風格後路線規劃壞掉」根因修復（PAGE 工人稽核 GAP #1）：plannedRoute 是新增欄位，
  // 型別（SciFiMapProps）由 PAGE 工人同步補進 ./types.ts（本檔依契約不擁有 types.ts，見檔頭）——用
  // intersection 轉型讓本檔可以不依賴對方進度先行開發／獨立跑 tsc；等 types.ts 真的補上同名同型別欄位
  // 後，這裡只是多餘但無害的重複宣告。[lat,lng] 陣列，從目前位置到使用者點選的打卡點/賽事目標；
  // null/空陣列＝目前沒有規劃中的路線。
  const { plannedRoute } = props as SciFiMapProps & { plannedRoute?: [number, number][] | null }

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
  // 契約 TRACK_HYDRATION_CONTRACT.md 修法 4：帶 sourceId/tile 的圖塊／來源錯誤只記錄、不退回
  // （見下方 onError），但同一個來源逾時/斷線時可能連續噴發大量 'error' 事件——這個 ref 記「這個
  // sourceId 最近一次 warn 的時間」，同一來源 10 秒內只印一次，避免 console 被洗版（不影響任何
  // 判斷邏輯，純粹節流輸出）。
  const tileErrWarnedRef = useRef<Map<string, number>>(new Map())
  const followingRef = useRef(true)
  const recenterZoomPendingRef = useRef(false) // 見上方 RECENTER_ZOOM 宣告處：recenter() 剛按下、zoom 還沒收斂到 16.5 期間為 true
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
  // docs/skins/ORBPOS_CONTRACT.md 第二輪驗收14「核心像素」量測：只給 E2E 做「有畫 vs 沒畫」前後對照用
  // （比照 CuteMap.tsx／RetroMap.tsx 的 setOverlayHidden，scifi 原本沒有這個把手，E2E 無法乾淨量測靈魂
  // 實際畫出來的像素位置），預設 false，不影響一般使用者。
  const overlayHiddenRef = useRef(false)
  const prevStatusRef = useRef<string | undefined>(undefined) // 比照 cute：新一趟 tracking 開始時強制歸回 false，避免卡在上一輪 E2E 呼叫的 true
  // A2 FALLBACK FIX（2026-09-29）：webglcontextlost／render-exception 這類「其實常常救得回來」的失敗，
  // 舊版一律立即永久退回 Leaflet（根因調查：iOS 背景分頁的 WebGL context 遺失絕大多數可自動復原，
  // MapLibre 自己就支援，舊版卻搶在它復原之前就把 instance 砍了）。這兩個 ref 是輕量診斷欄位，供
  // __scifiDebug 曝光「最近一次觸發過的失敗原因」與「這個 session 內自動復原成功幾次」，純觀測用，
  // 不影響任何判斷邏輯本身；沒有前端可上報的 client-log 端點（已查證），因此只曝光在這裡＋console。
  const lastFallbackReasonRef = useRef<string | null>(null)
  const recoveriesRef = useRef(0)

  // 高頻資料走 ref（避免 rAF 迴圈依賴 useEffect 重新掛載），由 props 變動時同步寫入。
  const posRef = useRef(pos); posRef.current = pos
  const statusRef = useRef(status); statusRef.current = status
  const segmentsRef = useRef(segments); segmentsRef.current = segments
  const kmMarksRef = useRef(kmMarks); kmMarksRef.current = kmMarks
  const targetsRef = useRef(targets); targetsRef.current = targets
  // GAP #1 修復：規劃路線資料走 ref（同高頻資料慣例，即使目前更新頻率不高——每次按「🧭 路線規劃」
  // 才變一次），供 renderFrame／__scifiDebug 讀取，不用重新掛載 render loop。plannedRouteHiddenRef
  // 是獨立於既有 overlayHiddenRef 的另一個把手（驗收14 overlayHidden 管的是「光點／軌跡／徽章」，不含
  // 這條新的建議路線），讓 E2E 能單獨對這個新功能做「有畫 vs 沒畫」前後對照，不用連光點/軌跡一起關掉。
  const plannedRouteRef = useRef<[number, number][] | null>(plannedRoute ?? null); plannedRouteRef.current = plannedRoute ?? null
  const plannedRouteHiddenRef = useRef(false)
  const focusModeRef = useRef(focusMode); focusModeRef.current = focusMode
  const onFallbackRef = useRef(onFallback); onFallbackRef.current = onFallback
  const onTargetClickRef = useRef(onTargetClick); onTargetClickRef.current = onTargetClick
  const onFollowChangeRef = useRef(onFollowChange); onFollowChangeRef.current = onFollowChange
  // docs/skins/ORBPOS_CONTRACT.md 第二輪 S1：底部可拖曳資訊面板頂端到畫面底的高度（CSS px，PAGE 工人
  // 用既有 sheet.H／sheet.curY 算出，透過 mapSnapshot.bottomInset 帶入，同 cute/retro）→
  // map.setPadding({bottom})，讓跟隨中心落在面板以上的可見地圖區正中央——做法比照
  // CuteMap.tsx applyCutePadding／RetroMap.tsx applyRetroPadding 同一段（見下方 applyScifiPadding）。
  const bottomInsetRef = useRef(bottomInset ?? 0); bottomInsetRef.current = bottomInset ?? 0
  const appliedPaddingBottomRef = useRef<number | null>(null) // 上次實際 setPadding 的值，避免每次 render 都重呼叫
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
    lastFallbackReasonRef.current = reason
    // eslint-disable-next-line no-console
    console.warn('[scifi-map] fallback', reason)
    if (rafRef.current != null) { cancelAnimationFrame(rafRef.current); rafRef.current = null }
    try { delete (window as unknown as { __scifiDebug?: unknown }).__scifiDebug } catch { /* ignore */ }
    onFallbackRef.current(reason)
  }

  // docs/skins/ORBPOS_CONTRACT.md 第二輪 S1：scifi 原本完全沒有這段——跟隨把靈魂放在「整個容器」正
  // 中央（＝面板上緣附近），scifi 又是 pitch 55＋速度 >1 m/s 時依行進方向轉 bearing（車頭朝上），
  // 身後的軌跡整段往畫面下方延伸，完全被面板蓋住（使用者實跑用的正是 scifi，這是他真機回報「看不到
  // 軌跡」的直接成因之一）。比照 CuteMap.tsx applyCutePadding／RetroMap.tsx applyRetroPadding 同一段：
  // map.setPadding({bottom}) 讓可見區（面板以上那塊）的中心＝跟隨中心，MapLibre 算「center 要投影到
  // 螢幕上哪一點」時只看 padding 設定本身（centerPoint=((padding.left-padding.right+width)/2,
  // (padding.top-padding.bottom+height)/2)），不受 pitch/bearing 影響——pitch 55／車頭朝上 bearing
  // 因此不受影響，project() 自動反映套用 padding 後的座標系，靈魂／軌跡/公里標記照舊呼叫同一個
  // map.project() 畫，不需要另外調整任何繪製座標。專注模式（RaceFocusMode 全螢幕面板）用容器高度
  // 上 45%（比照 cute/retro），其餘情況用 bottomInset（面板頂端到畫面底的高度）。
  const applyScifiPadding = () => {
    const map = mapRef.current
    if (!map) return
    const container = containerRef.current
    const h = container?.clientHeight || 0
    const bottom = Math.max(0, Math.round(focusModeRef.current ? h * 0.55 : bottomInsetRef.current))
    if (appliedPaddingBottomRef.current === bottom) return
    appliedPaddingBottomRef.current = bottom
    try { map.setPadding({ top: 0, bottom, left: 0, right: 0 }) } catch { /* ignore */ }
  }
  // bottomInset／focusMode 任一改變都重算一次；每次 render 都檢查一次即可，內部已用
  // appliedPaddingBottomRef 擋掉沒變化時的重呼叫（比照 cute/retro 同一段）。
  useEffect(() => { applyScifiPadding() }) // eslint-disable-line react-hooks/exhaustive-deps

  // 驗收14 setOverlayHidden 的歸位（比照 CuteMap.tsx 同一段 FIX）：每次「新」轉進 tracking（=開始新
  // 一趟跑步）時把 overlayHiddenRef 強制歸回 false，避免上一輪 E2E（或任何來源）呼叫成 true 卡住，
  // 新一趟跑步一定看得到靈魂／軌跡／公里標記，不需要整頁重新整理。
  useEffect(() => {
    if (status === 'tracking' && prevStatusRef.current !== 'tracking') overlayHiddenRef.current = false
    prevStatusRef.current = status
  }, [status])

  // docs/skins/ORBPOS_CONTRACT.md §A：暫停跟隨鏡頭 8 秒。移到元件頂層（不再是建圖 useEffect 內的
  // 區域函式，比照 RetroMap.tsx 同一輪的改法），讓下面 useImperativeHandle 的 zoomBy（+/− 按鈕，
  // 契約「屬使用者操作，照舊暫停跟隨 8 秒」）與建圖 effect 內的 onDragStart/onZoomStart（真實使用者
  // 手勢）共用同一份邏輯。第二輪 S2 新增：只在真的從「跟隨中」轉為「暫停」的那一刻通知
  // onFollowChange(false)，8 秒後自動恢復時通知 onFollowChange(true)——讓 page.tsx 接得到 skin 地圖
  // 的跟隨狀態變化，「回到目前位置」按鈕才會在使用者拖動/縮放 scifi 地圖後正確出現（S2 根因：舊版
  // followingRef 只有本檔自己讀，page.tsx 完全不知道，按鈕永遠隱藏）。
  function pauseFollow() {
    if (followingRef.current) onFollowChangeRef.current?.(false)
    followingRef.current = false
    if (resumeTimerRef.current) clearTimeout(resumeTimerRef.current)
    resumeTimerRef.current = setTimeout(() => {
      followingRef.current = true
      resumeTimerRef.current = null
      onFollowChangeRef.current?.(true)
    }, RESUME_FOLLOW_MS)
  }

  // 2026-09-30 owner 拍板「路線規劃／前往打卡後鏡頭要一直停在那，不能自動跳回跟隨」修復：fitRoute()／
  // centerOn() 是「使用者主動要求看某個畫面」（按了🧭路線規劃、或點了城市探索關主的深連結），跟拖曳／
  // 縮放地圖／按＋－那種「臨時看一眼」的手勢語意不同——比照預設 Leaflet 地圖同一套行為（page.tsx
  // planRoute() 只設 followRef.current=false，focusBoss centerMap() 也只設 false，兩處都完全沒有計時
  // 器），鏡頭應該一路保持在使用者要求的畫面，直到使用者自己按「回到目前位置」（recenter()）才恢復
  // 跟隨。因此另外開這個「只暫停、不安排自動恢復」的函式，跟上面 pauseFollow()（拖曳/縮放/＋－按鈕
  // 用，8 秒後自動恢復）分開，讓兩種語意的呼叫端各自對應正確的行為。
  //
  // 邊界情況（owner 確認「兩種都可接受」，這裡選擇不特別處理）：若使用者在這個「保持」期間自己動手
  // 拖曳/縮放地圖，下方建圖 effect 的 onDragStart/onZoomStart 仍會呼叫 pauseFollow()（會排一個新的 8
  // 秒自動恢復）——也就是說使用者自己的手勢會讓保持提前依 8 秒規則結束，而不是永遠停留到使用者按
  // 「回到目前位置」。這被視為合理：使用者一旦自己動手操作地圖，就代表他接手了鏡頭控制權，之後的
  // 行為理應比照一般手勢操作，不需要再特別維護「這是不是路線規劃後的保持」這種額外狀態。
  function holdFollow() {
    if (followingRef.current) onFollowChangeRef.current?.(false)
    followingRef.current = false
    if (resumeTimerRef.current) { clearTimeout(resumeTimerRef.current); resumeTimerRef.current = null }
  }

  useImperativeHandle(ref, () => ({
    recenter(p) {
      const map = mapRef.current
      if (!map) return
      followingRef.current = true
      if (resumeTimerRef.current) { clearTimeout(resumeTimerRef.current); resumeTimerRef.current = null }
      // docs/skins/ORBPOS_CONTRACT.md 第二輪 S2：「回到目前位置」恢復跟隨，通知 page.tsx（按鈕消失、
      // followRef/setFollowing 同步）——不論呼叫前 followingRef 是不是已經是 true，都無條件通知一次，
      // page.tsx 那端是 React state，同值不會多觸發 re-render，這裡不需要另外判斷是否為「真的變化」。
      onFollowChangeRef.current?.(true)
      const target = p || posRef.current
      if (target) {
        recenterZoomPendingRef.current = true // 見上方 RECENTER_ZOOM 宣告處：標記「zoom 還沒收斂」，讓跟隨 effect 之後一起帶 zoom
        try { map.easeTo({ center: [target.lng, target.lat], zoom: RECENTER_ZOOM, pitch: 55, duration: 500, essential: true }) } catch { /* ignore */ }
      }
    },
    zoomBy(delta: number) {
      const map = mapRef.current
      if (!map) return
      // docs/skins/ORBPOS_CONTRACT.md §A「＋／− 按鈕屬使用者操作，照舊暫停跟隨 8 秒」＋第二輪 S2：
      // 這裡呼叫的是程式 easeTo，觸發的 'zoomstart' 不會帶 originalEvent（見下方 onZoomStart 只認
      // 使用者手勢），不會再被事件自動暫停，改成呼叫上面元件頂層的 pauseFollow()（比照
      // RetroMap.tsx 同一段）——順便帶出 onFollowChange(false)／8 秒後 (true) 通知，不用像舊版
      // 重複一份跳過通知的 inline 邏輯。
      recenterZoomPendingRef.current = false // 使用者主動縮放：不再幫他把 zoom 拉回 16.5，尊重這次操作（見上方 RECENTER_ZOOM 說明）
      pauseFollow()
      try { map.easeTo({ zoom: map.getZoom() + delta, duration: 250, essential: true }) } catch { /* ignore */ }
    },
    // 2026-09-30 GAP #1（路線規劃跨風格壞掉）修復：PAGE 工人按下「🧭 路線規劃」後，除了既有 Leaflet
    // 路線（預設風格）之外，skin 生效時改呼叫這裡——把整條建議路線（含目前位置與目的地）縮放進可視
    // 範圍，等同 Leaflet 版 planRoute() 的 `mapRef.current.fitBounds(latlngs,...)`。
    // 2026-09-30 owner 拍板修正：改呼叫 holdFollow()（不是 pauseFollow()）——鏡頭要一直停在路線總覽，
    // 不會 8 秒後自動跳回跟隨，直到使用者自己按「回到目前位置」（比照 Leaflet 版 planRoute() 的
    // followRef.current=false、完全不設計時器，見上方 holdFollow() 宣告處的完整說明）。padding 沿用
    // applyScifiPadding() 同一套 bottomInset／focusMode 換算，避免建議路線的下半段被底部面板蓋住。
    fitRoute(points: [number, number][]) {
      const map = mapRef.current
      if (!map || !points?.length) return
      holdFollow()
      // FIX（review 抓到的 minor 根因）：centerOn() 與 cute/retro 的 fitRoute() 都會重置這個旗標，這裡
      // 原本漏了——若使用者先按「回到目前位置」（recenter() 設 recenterZoomPendingRef=true，zoom 動畫
      // 還沒收斂到 16.5）、緊接著就按「🧭 路線規劃」，旗標會一路存活到跟隨恢復的那一刻，下方 GPS 位置
      // 更新 effect 見旗標仍是 true，會把鏡頭 zoom 強制拉回 16.5，蓋掉 fitBounds 特地算好、能看到整條
      // 路線的縮放層級。這裡是使用者明確要看路線總覽，不套用 recenter()「補足到 16.5」那套邏輯（比照
      // centerOn() 同一行）。
      recenterZoomPendingRef.current = false
      try {
        const container = containerRef.current
        const h = container?.clientHeight || 0
        const bottomPad = Math.max(0, Math.round(focusModeRef.current ? h * 0.55 : bottomInsetRef.current)) + ROUTE_FIT_EXTRA_PADDING
        if (points.length === 1) {
          map.easeTo({ center: [points[0][1], points[0][0]], zoom: RECENTER_ZOOM, pitch: 55, duration: 500, essential: true })
          return
        }
        let west = Infinity, south = Infinity, east = -Infinity, north = -Infinity
        for (const [lat, lng] of points) {
          if (lng < west) west = lng
          if (lng > east) east = lng
          if (lat < south) south = lat
          if (lat > north) north = lat
        }
        // 建議路線總覽刻意壓平成俯視（pitch/bearing 都歸 0，比照 Leaflet fitBounds 一律北朝上、無傾角
        // 的慣例）——不然 pitch 55 的透視角會讓 fitBounds 算出的邊界跟畫面上實際看到的範圍對不齊，使用者
        // 反而看不到路線全貌；8 秒後自動恢復跟隨時，跟隨 effect 本來就會把 pitch 帶回 55，這裡不用另外處理。
        map.fitBounds([[west, south], [east, north]], {
          padding: { top: ROUTE_FIT_EXTRA_PADDING, bottom: bottomPad, left: ROUTE_FIT_EXTRA_PADDING, right: ROUTE_FIT_EXTRA_PADDING },
          pitch: 0,
          bearing: 0,
          duration: 600,
          essential: true,
          maxZoom: ROUTE_FIT_MAX_ZOOM,
        })
      } catch { /* ignore */ }
    },
    // GAP #2（前往打卡 focus 深連結／未來任何「程式指定中心但不想立刻被跟隨拉回去」的呼叫）修復：舊版
    // 只有 recenter()，一律無條件把 followingRef 設回 true＋onFollowChange(true)（見上方 recenter()
    // 註解），下一次 pos 更新就會把鏡頭拉回使用者目前實際位置，蓋掉剛剛的程式指定中心——這正是稽核
    // GAP #2 指出「即使呼叫了也會被下一個 GPS tick 立刻復原」的根因。
    // 2026-09-30 owner 拍板修正：centerOn() 改用 holdFollow()（不是 pauseFollow()）——呼叫端可以把
    // 鏡頭釘在任意座標，且會一直停留到使用者自己按「回到目前位置」，不會 8 秒後被跟隨拉走（見上方
    // holdFollow() 宣告處的完整說明）。
    centerOn(lat: number, lng: number, zoom?: number) {
      const map = mapRef.current
      if (!map) return
      holdFollow()
      recenterZoomPendingRef.current = false // 呼叫端明確指定（或刻意不指定）zoom，不套用 recenter() 的「補足到 16.5」邏輯
      const opts: { center: [number, number]; pitch: number; duration: number; essential: true; zoom?: number } =
        { center: [lng, lat], pitch: 55, duration: 500, essential: true }
      if (typeof zoom === 'number') opts.zoom = zoom
      try { map.easeTo(opts) } catch { /* ignore */ }
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
    let tileStallTimer: ReturnType<typeof setTimeout> | null = null // 見上方 TILE_STALL_TIMEOUT_MS 說明
    // review round 3 修補（finding 1）：這顆計時器要跟 timeoutId/contextLostGraceTimer 一樣受
    // document.hidden 暫停/恢復管控——tileStallPending 記錄「是否仍在等待首次任一圖塊成功」，在
    // onVisibilityChange 隱藏時清掉 tileStallTimer 但保留這個旗標，回到前景由 armTileStallTimer()
    // 重新起算一輪；否則背景中到期時 callback 看到 document.hidden 只會 return、不會重新排程，回到
    // 前景後就再也沒有任何 fallback／重試機會，地圖永久空白（契約 §4 明文禁止的情境）。
    let tileStallPending = false
    // review round 3 修補（finding 2）：只在「從未有任何圖塊成功過」時才會被 onError 設成 true；一旦
    // onData 收到過一次成功的圖塊就永久設 true 且不會再被清掉，往後任何單一/孤立圖塊失敗都不會再起算
    // 這顆計時器——避免對一張已經成功繪製、運作中的地圖，只因為之後偶發一顆圖塊逾時就在 20 秒後被
    // failInstance() 整張丟棄，那等於用延遲版本重新引入契約明文禁止的「單一圖塊失敗就整張退回」。
    let everTileLoaded = false
    // 失敗圖塊重試狀態（見 TILE_RETRY_DELAYS_MS）
    let tileRetryTimer: ReturnType<typeof setTimeout> | null = null
    let tileRetryRounds = 0
    const failedTileIds = new Map<string, { x: number; y: number; z: number }>()
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

    // A2 FALLBACK FIX（2026-09-29）：webglcontextlost／8 秒 load timeout 這兩種失敗，根因調查證實絕大
    // 多數是「暫時性、MapLibre／瀏覽器自己就會恢復」的情況（iOS 背景分頁 GPU 資源回收、背景分頁計時器
    // 節流），舊版卻一律立即永久退回 Leaflet，完全沒有給復原機會。以下狀態只影響「要不要現在就判
    // 死」，不動 isCurrent()／instance 隔離／DPR canvas／padding／follow 等既有邏輯。
    let contextLost = false // MapLibre 已回報 webglcontextlost、尚未 restore／尚未放棄
    let contextLostGraceTimer: ReturnType<typeof setTimeout> | null = null
    let recreateAttempts = 0
    const CONTEXT_LOST_GRACE_MS = 5000 // 回到前景後給 MapLibre 自己 restore 的寬限期（node_modules/maplibre-gl/src/ui/map.ts _contextRestored）
    const MAX_RECREATE_ATTEMPTS = 2 // 寬限期逾時仍未 restore：就地重建 instance 最多幾次，超過才真的退回 Leaflet
    const clearContextLostGraceTimer = () => { if (contextLostGraceTimer) { clearTimeout(contextLostGraceTimer); contextLostGraceTimer = null } }
    const clearTileStallTimer = () => { if (tileStallTimer) { clearTimeout(tileStallTimer); tileStallTimer = null } }
    // 與 armLoadTimeout()/armContextLostGrace() 同一套規則：只在頁面可見且仍在等待（tileStallPending）
    // 時才會（重新）起算，已經在跑就不重複起算（維持原本「只要還沒在跑，就起算一次」語意）；背景/前景
    // 切換交給 onVisibilityChange 呼叫這個函式，不再是裸 setTimeout。
    const armTileStallTimer = () => {
      if (!isCurrent() || !tileStallPending || document.hidden || tileStallTimer) return
      tileStallTimer = setTimeout(() => {
        tileStallTimer = null
        if (!isCurrent() || document.hidden) return
        tileStallPending = false
        failInstance('tile-stall-timeout-20s')
      }, TILE_STALL_TIMEOUT_MS)
    }
    const clearTileRetryTimer = () => { if (tileRetryTimer) { clearTimeout(tileRetryTimer); tileRetryTimer = null } }
    const scheduleTileRetry = () => {
      if (!isCurrent() || tileRetryTimer || failedTileIds.size === 0 || tileRetryRounds >= TILE_RETRY_DELAYS_MS.length || document.hidden) return
      tileRetryTimer = setTimeout(() => {
        tileRetryTimer = null
        // WebGL context 復原中（style 已被 MapLibre 拆掉）不重試、不扣輪數；復原後 resetTileWatch 會重新偵測
        if (!isCurrent() || document.hidden || contextLost || failedTileIds.size === 0) return
        const ids = Array.from(failedTileIds.values())
        failedTileIds.clear()
        tileRetryRounds += 1
        try { map?.refreshTiles(BASE_TILE_SOURCE, ids) } catch { /* ignore */ }
      }, TILE_RETRY_DELAYS_MS[tileRetryRounds])
    }
    // 重置「圖塊全數失敗」偵測與重試（新 instance、WebGL context 復原後都要從頭偵測）
    const resetTileWatch = () => {
      everTileLoaded = false
      tileStallPending = false
      if (tileStallTimer) { clearTimeout(tileStallTimer); tileStallTimer = null }
      failedTileIds.clear()
      clearTileRetryTimer()
      tileRetryRounds = 0
    }

    const failInstance = (reason: string) => {
      if (localFailed || failedRef.current) return
      if (!isCurrent()) return // 舊 instance 的遲到事件：目前使用中的已經是別的 map（或已被 cleanup），不動它
      localFailed = true
      failedRef.current = true
      lastFallbackReasonRef.current = reason
      // eslint-disable-next-line no-console
      console.warn('[scifi-map] fallback', reason)
      clearContextLostGraceTimer()
      clearTileStallTimer()
      clearTileRetryTimer()
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
      applyScifiPadding() // S1：map 剛就緒就套用一次，不必等下一次 React re-render（比照 cute/retro）
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
      // 契約 TRACK_HYDRATION_CONTRACT.md 修法 4：MapLibre 'error' 事件若帶 sourceId 或 tile
      // （來源/圖塊層級的載入失敗——單一圖塊逾時/404/來源連線問題），只記錄、絕不觸發 fallback。
      // 行動網路下偶爾一顆圖塊失敗很正常，地圖本身（樣式／WebGL context）沒事，退回 Leaflet
      // 反而是過度反應（契約 §驗收：攔截單一圖塊回 500／逾時 → 三種風格地圖都不退回）。真正的
      // 樣式檔載入失敗（'load' 之前、不帶 sourceId/tile 的錯誤）與下面既有的 context-lost/逾時
      // 邏輯完全不變，繼續往下走原本的 failInstance() 路徑。
      const ee = e as { sourceId?: string; tile?: unknown }
      if (ee && (ee.sourceId != null || ee.tile != null)) {
        const key = ee.sourceId != null ? String(ee.sourceId) : 'tile'
        const now = Date.now()
        const last = tileErrWarnedRef.current.get(key) || 0
        if (now - last > 10000) {
          tileErrWarnedRef.current.set(key, now)
          // eslint-disable-next-line no-console
          console.warn('[scifi-map] tile/source error (ignored, no fallback)', key, e)
        }
        // review round 2 缺口修補：只要「持續沒有任何圖塊成功」的計時器還沒在跑，就起算一次；
        // 只要 onData 收到任一顆圖塊成功就會清掉它（視為復原），不會因為單一/部分圖塊失敗誤退回。
        // review round 3 修補（finding 2）：一旦有任何圖塊成功過（everTileLoaded）就永久不再起算——
        // 這個計時器只用來偵測「初次連一顆圖塊都沒成功過」的持續空白，不是用來監控已經在跑的地圖。
        // 審查 major 2（2026-10-02）：'error' 不會排程重繪；若最後一顆收尾的圖塊剛好失敗，'load'（只在 _render
        // 內發出）永遠不會觸發，load timeout 會把其他圖塊都正常的地圖整張丟掉。主動要一次重繪。
        try { map?.triggerRepaint() } catch { /* ignore */ }
        if (ee.sourceId == null || ee.sourceId === BASE_TILE_SOURCE) {
          const c = (ee.tile as { tileID?: { canonical?: { x: number; y: number; z: number } } } | null | undefined)?.tileID?.canonical
          if (c) {
            failedTileIds.set(`${c.z}/${c.x}/${c.y}`, { x: c.x, y: c.y, z: c.z })
            scheduleTileRetry()
          }
          if (!everTileLoaded) {
            tileStallPending = true
            armTileStallTimer()
          }
        }
        return
      }
      // review 抓到的 minor 根因：MapLibre `_contextRestored()`（node_modules/maplibre-gl/src/ui/map.ts）
      // 重新 `_setupPainter()`（取得 WebGL context）失敗時不會 fire 'webglcontextrestored'，而是直接 fire
      // 這個 'error' 事件然後 return——此時 contextLost 仍是 true（webglcontextrestored 從沒發生過）。舊
      // 寫法不分青紅皂白一律 failInstance() 永久退回 Leaflet，等於讓這個情境完全繞過上面剛加的「寬限期＋
      // 就地重建最多兩次」安全網。改成：只要還在 context-lost 復原流程中且重建次數沒用完，就走跟
      // armContextLostGrace() 逾時分支一樣的路（recreateInstance()）；真的用完才 failInstance()。
      if (contextLost && recreateAttempts < MAX_RECREATE_ATTEMPTS) {
        clearContextLostGraceTimer()
        recreateAttempts += 1
        recreateInstance()
        return
      }
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
    // A2 FALLBACK FIX：webglcontextlost 改成「等 MapLibre 自己救」——不再收到就立即判死。改聽 Map
    // 自己 fire 的 'webglcontextlost'／'webglcontextrestored'（不是 DOM canvas 事件；MapLibre 在
    // map.ts _contextLost 內已經呼叫 event.preventDefault() 並完成 painter/style 的拆卸，等它自己的
    // Map-level 事件更能確保時序正確）。寬限期只在「頁面可見」時起算（onVisibilityChange 負責在背景/
    // 前景切換時暫停/重新起算），逾期仍未 restore 才嘗試就地重建 instance（保留鏡頭位置），重建次數
    // 用盡才真的退回 Leaflet。
    const armContextLostGrace = () => {
      if (!isCurrent() || !contextLost) return
      if (document.hidden) return // 背景中不起算，回到前景由 onVisibilityChange 重新呼叫
      clearContextLostGraceTimer()
      contextLostGraceTimer = setTimeout(() => {
        contextLostGraceTimer = null
        if (!isCurrent() || !contextLost) return
        if (recreateAttempts >= MAX_RECREATE_ATTEMPTS) { failInstance('webglcontextlost-unrecovered'); return }
        recreateAttempts += 1
        recreateInstance()
      }, CONTEXT_LOST_GRACE_MS)
    }
    const onContextLost = () => {
      if (!isCurrent()) return
      contextLost = true
      lastFallbackReasonRef.current = 'webglcontextlost'
      // eslint-disable-next-line no-console
      console.warn('[scifi-map] webglcontextlost, waiting for auto-recovery')
      armContextLostGrace()
    }
    const onContextRestored = () => {
      if (!isCurrent() || !contextLost) return
      contextLost = false
      // 審查 major 1（2026-10-02）：MapLibre _contextRestored 會重新 setStyle、重抓 TileJSON 與全部圖塊；之前成功過
      // 不代表這次會成功（例：iPhone 從背景回來網路還沒恢復）。不重置的話全失敗時 stall 永不起算、地圖永久空白。
      resetTileWatch()
      clearContextLostGraceTimer()
      recreateAttempts = 0
      recoveriesRef.current += 1
      // eslint-disable-next-line no-console
      console.warn('[scifi-map] webglcontextrestored, recovered')
      applyScifiPadding() // 保險：transform／padding 理論上不受影響，重套用一次不會有副作用
      try {
        map!.setSky({
          'sky-color': '#02040a',
          'horizon-color': '#0a2540',
          'sky-horizon-blend': 0.9,
          'horizon-fog-blend': 0.6,
          'atmosphere-blend': 0.35,
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
        } as any)
      } catch { /* ignore */ }
    }
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
      const ev = e as { dataType?: string; tile?: unknown; sourceId?: string }
      if (ev?.dataType === 'source' && ev.tile) {
        tilesLoadedRef.current += 1
        // 有圖塊真正成功了：視為從「持續空白」復原，清掉上面 onError 起的計時器；並永久關閉
        // stall 偵測（everTileLoaded），往後任何孤立的單一圖塊失敗都不會再誤判成持續空白。
        // 只有底圖圖塊成功才算「地圖有東西」（審查 minor 3：自己加的 GeoJSON 來源也會發 'data'{tile}）
        if (ev.sourceId == null || ev.sourceId === BASE_TILE_SOURCE) {
          // 已成功的圖塊移出重試清單（第二輪審查 minor 3：否則之後重試可能把好的圖塊重抓成失敗）
          const cc = (ev.tile as { tileID?: { canonical?: { x: number; y: number; z: number } } } | null | undefined)?.tileID?.canonical
          if (cc) failedTileIds.delete(`${cc.z}/${cc.x}/${cc.y}`)
          everTileLoaded = true
          tileStallPending = false
          clearTileStallTimer()
        }
      }
    }

    // pauseFollow() 已移到元件頂層（見上方宣告處的 ORBPOS_CONTRACT.md §A／S2 說明），這裡直接沿用
    // 外層 closure 抓到的那一份，不再本地重宣告一次（比照 RetroMap.tsx 同一輪的改法）。

    // A2 FALLBACK FIX：8 秒 load timeout 改成「背景分頁不消耗額度」——原本掛載當下就無條件起算 8 秒，
    // 若使用者在地圖 tiles/worker 尚未 load 完前就切背景/鎖屏，背景計時器只會被節流、不會整個停止，
    // 回到前景時往往已經被誤判逾時。改成：只在頁面可見時才起算／繼續倒數，背景時清掉計時器，回到前景
    // 給滿一輪新的 FALLBACK_TIMEOUT_MS（比恢復剩餘額度更寬裕，避免「背景時已經快到期，一回前景立刻
    // 又被判定逾時」，對慢網路更寬容）。
    const armLoadTimeout = () => {
      if (!isCurrent() || loadedRef.current || document.hidden) return
      if (timeoutId) clearTimeout(timeoutId)
      timeoutId = setTimeout(() => {
        timeoutId = null
        if (isCurrent() && !loadedRef.current) failInstance('load-timeout-8s')
      }, FALLBACK_TIMEOUT_MS)
    }
    const onVisibilityChange = () => {
      if (document.hidden) {
        if (timeoutId) { clearTimeout(timeoutId); timeoutId = null }
        clearContextLostGraceTimer()
        clearTileStallTimer() // review round 3 修補（finding 1）：背景中不消耗額度，tileStallPending 保留給回前景重新起算
        clearTileRetryTimer()
        return
      }
      armLoadTimeout()
      armContextLostGrace()
      armTileStallTimer()
      tileRetryRounds = 0 // 回到前景：重新給重試輪數
      scheduleTileRetry()
    }
    document.addEventListener('visibilitychange', onVisibilityChange)
    // 網路恢復：重新給重試輪數並排程（斷網期間失敗的圖塊不必等使用者移動地圖才補）
    const onOnline = () => {
      if (!isCurrent()) return
      tileRetryRounds = 0
      scheduleTileRetry()
    }
    window.addEventListener('online', onOnline)

    function detachListeners() {
      try {
        map?.off('load', onLoad)
        map?.off('error', onError)
        map?.off('dragstart', onDragStart)
        map?.off('zoomstart', onZoomStart)
        map?.off('click', onClick)
        map?.off('dataloading', onDataLoading as never)
        map?.off('data', onData as never)
        map?.off('webglcontextlost', onContextLost)
        map?.off('webglcontextrestored', onContextRestored)
      } catch { /* ignore */ }
    }
    detachListenersRef.current = detachListeners

    // A2 FALLBACK FIX：把原本「只在掛載時跑一次」的建圖邏輯抽成可重入的 mountInstance()——寬限期逾時
    // 仍未 restore 時，recreateInstance() 會呼叫它就地重建一顆新 instance（保留鏡頭位置），不需要整個
    // React 元件重新掛載。camera 有值＝重建（沿用既有鏡頭，且不重建 Soul/Trail/Rain／不重啟 render
    // loop，這些跟地圖 instance 本身無關）；沒有值＝初次掛載（沿用 initialCenter/initialZoom）。
    function mountInstance(camera?: { center: [number, number]; zoom: number; pitch: number; bearing: number }) {
      if (!containerRef.current) { failInstance('recreate-no-container'); return }
      try {
        if (!camera) {
          reducedMotionRef.current = typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
          const isMobile = window.innerWidth < 768
          soulRef.current = new Soul(focusMode ? 28 : (isMobile ? 55 : 68))
          trailRef.current = new TrailPool(focusMode ? 200 : 400)
          rainRef.current = new LightRain(reducedMotionRef.current || focusMode ? 0 : (isMobile ? 24 : 36))
        }

        map = new MapLibreMap({
          container: containerRef.current,
          style: buildScifiStyle() as unknown as MapOptions['style'],
          center: (camera?.center ?? [initialCenter[1], initialCenter[0]]) as LngLatLike,
          zoom: camera?.zoom ?? (initialZoom ?? 16),
          pitch: camera?.pitch ?? 55,
          bearing: camera?.bearing ?? 0,
          antialias: true,
          maxPitch: 70,
          localIdeographFontFamily: "'Noto Sans TC','Microsoft JhengHei',sans-serif",
          // 授權聲明用精簡「ⓘ」按鈕放右上（預設是展開的整條放左/右下，專注模式時會橫在下半部數字上；2026-09-27 編排者檢視截圖）。
          attributionControl: false,
        } as MapOptions)
        try { map.addControl(new AttributionControl({ compact: true }), 'top-right') } catch { /* ignore */ }
        mapRef.current = map
        loadedRef.current = false // 重建時要等新 instance 自己 load 過（render loop 靠這個旗標暫停到新地圖就緒）
        contextLost = false
        // 每顆新 instance 都重新偵測「圖塊全數失敗」（審查 round 3 MAJOR）：上一顆曾載入成功（everTileLoaded）
        // 不代表重建後這顆也會成功（例：WebGL context lost → recreateInstance 時剛好斷網）；不重置的話新 instance
        // 全部圖塊失敗時 tile-stall 永遠不會起算，地圖就一直空白、不會退回預設地圖。
        resetTileWatch()

        armLoadTimeout()

        map.on('load', onLoad)
        map.on('error', onError)
        map.on('dragstart', onDragStart)
        map.on('zoomstart', onZoomStart)
        map.on('click', onClick)
        map.on('dataloading', onDataLoading as never)
        map.on('data', onData as never)
        map.on('webglcontextlost', onContextLost)
        map.on('webglcontextrestored', onContextRestored)

        try { document.fonts?.load?.(`700 14px ${orbitron.style.fontFamily}`) } catch { /* 預載失敗不影響地圖，canvas 文字退回預設字型 */ }

        ro?.disconnect()
        ro = new ResizeObserver(() => { if (isCurrent()) { try { map!.resize() } catch { /* ignore */ }; applyScifiPadding() } })
        ro.observe(containerRef.current)

        if (!camera) startLoop() // render loop 只需要啟動一次：frame() 每幀都重讀 mapRef.current，重建後自動接上新 instance
      } catch (e) {
        failInstance('init-exception:' + ((e as Error)?.message || String(e)))
      }
    }

    // 寬限期逾時仍未 webglcontextrestored：就地重建一顆新 instance，盡量沿用目前鏡頭位置（讀不到就退回
    // 最新定位或 initialCenter）；先徹底清掉舊 instance（它已經是壞的，context 不會回來）再建新的。
    function recreateInstance() {
      if (!isCurrent()) return
      // eslint-disable-next-line no-console
      console.warn('[scifi-map] recreating instance after unrecovered context loss, attempt', recreateAttempts)
      let camera: { center: [number, number]; zoom: number; pitch: number; bearing: number } | null = null
      try {
        if (map) camera = { center: [map.getCenter().lng, map.getCenter().lat], zoom: map.getZoom(), pitch: map.getPitch(), bearing: map.getBearing() }
      } catch { /* ignore：退回下面 fallback */ }
      if (!camera) {
        const p = posRef.current
        camera = { center: p ? [p.lng, p.lat] : [initialCenterRef.current[1], initialCenterRef.current[0]], zoom: RECENTER_ZOOM, pitch: 55, bearing: 0 }
      }
      detachListeners()
      try { map?.remove() } catch { /* ignore */ }
      mapRef.current = null
      map = null
      recoveriesRef.current += 1 // 就地重建成功也算一次「救回來」（未必等於新 instance 一定會 load 成功，屬輕量診斷、非精確計數）
      mountInstance(camera)
    }

    mountInstance()

    return () => {
      cancelled = true
      document.removeEventListener('visibilitychange', onVisibilityChange)
      window.removeEventListener('online', onOnline)
      if (timeoutId) clearTimeout(timeoutId)
      if (tileStallTimer) clearTimeout(tileStallTimer)
      clearTileRetryTimer()
      clearContextLostGraceTimer()
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
      const p = posRef.current
      let soulScreen = { x: 0, y: 0 }
      try {
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
        // ORBPOS_CONTRACT.md 第二輪驗收12：直接讀 MapLibre 自己的 getPadding()（applyScifiPadding()
        // 呼叫 setPadding 的權威回讀），讓 E2E 能算出「可見區（面板以上那塊）中心」的 CSS px。
        padding: map.getPadding(),
        soulScreen,
        // docs/skins/ORBPOS_CONTRACT.md §E＋偵錯把手：沒有真實定位時完全不畫靈魂（見 renderFrame），
        // orbVisible 如實反映這件事；orbLatLng／lastFixAgeMs／orbState 用 renderFrame 同一組
        // getFixAgeMs／isOrbSearching 判定；following＝目前是否處於跟隨狀態；projectLngLat 讓 E2E
        // 直接驗證任意座標的 CSS px 投影是否與靈魂實際畫的位置一致。
        orbVisible: !!p,
        orbLatLng: p ? { lat: p.lat, lng: p.lng } : null,
        lastFixAgeMs: p ? getFixAgeMs(p) : null,
        orbState: p ? (isOrbSearching(p) ? 'searching' : 'normal') : null,
        following: followingRef.current,
        projectLngLat: (lng: number, lat: number) => {
          try { const pt = map.project([lng, lat]); return { x: pt.x, y: pt.y } } catch { return null }
        },
        fps: fpsRef.current,
        // 驗收14：只給 E2E 做「有畫 vs 沒畫」前後對照用，預設 false（見上方 overlayHiddenRef 宣告處）。
        setOverlayHidden: (v: boolean) => { overlayHiddenRef.current = !!v },
        // 2026-09-30 GAP #1 修復：建議路線目前有幾個點／均勻取樣最多 5 個點換算成畫面 CSS px（比照
        // 上面 projectLngLat 同一套 map.project()），讓 E2E 能驗證「plannedRoute 有沒有真的畫在螢幕
        // 上」而不用截圖比對像素；setPlannedRouteHidden 是獨立於 setOverlayHidden 的另一個開關（見上方
        // plannedRouteHiddenRef 宣告處），只管這條新路線的顯示/隱藏，方便 E2E 單獨對照。
        plannedRoutePoints: plannedRouteRef.current?.length || 0,
        plannedRouteScreenSample: (() => {
          const route = plannedRouteRef.current
          if (!route?.length) return []
          return sampleUpTo(route, 5).map((pt) => {
            try { const s = map.project([pt[1], pt[0]]); return { x: Math.round(s.x), y: Math.round(s.y) } } catch { return { x: 0, y: 0 } }
          })
        })(),
        setPlannedRouteHidden: (v: boolean) => { plannedRouteHiddenRef.current = !!v },
        // A2 FALLBACK FIX 輕量診斷（見上方 lastFallbackReasonRef／recoveriesRef 宣告處）：最近一次觸發過
        // 的失敗原因（webglcontextlost／render-exception:*／load-timeout-8s…，即使最後有救回來也會留在
        // 這裡）、以及這個 session 內自動復原成功幾次（webglcontextrestored 或就地重建各算一次）。
        lastFallbackReason: lastFallbackReasonRef.current,
        recoveries: recoveriesRef.current,
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
      // docs/skins/ORBPOS_CONTRACT.md §A：跟隨更新只搬 center（＋沿用既有的方向性 bearing 更新），
      // 不再強制 zoom:16.5——理由與 cute/CuteMap.tsx 同一段完全相同（使用者縮放後跟隨若仍寫死 zoom，
      // 會持續把剛設定好的縮放層級彈回去，與「保留使用者目前的縮放」牴觸；縮放要恢復預設值只透過
      // recenter()）。pitch 對 scifi 有意義（3D 傾角，非 cute/retro 的恆為 0），契約只點名 zoom，這裡
      // 維持原本每次跟隨都套用固定 pitch:55 的既有行為不動。
      const followOpts: { center: [number, number]; pitch: number; bearing: number; duration: number; essential: true; zoom?: number } =
        { center: [pos.lng, pos.lat], pitch: 55, bearing: bearingToUse, duration: 900, essential: true }
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
    // A2 FALLBACK FIX：單一 render-frame 例外不再立刻拆地圖——大多是暫時性的（例如某一幀座標算出
    // NaN、canvas 尺寸剛切版面瞬間為 0），記一筆、跳過這一幀即可，rAF 迴圈繼續跑；只有連續很多幀都
    // 失敗（真的壞掉，不是單一瞬間的偶發狀況）才判定不可用、真正退回 Leaflet。任何一幀成功就歸零計數。
    let consecutiveRenderFailures = 0
    const MAX_CONSECUTIVE_RENDER_FAILURES = 6
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
        consecutiveRenderFailures = 0
      } catch (e) {
        consecutiveRenderFailures += 1
        lastFallbackReasonRef.current = 'render-exception:' + ((e as Error)?.message || String(e))
        // eslint-disable-next-line no-console
        console.warn('[scifi-map] render-frame error, skip frame', consecutiveRenderFailures, e)
        if (consecutiveRenderFailures >= MAX_CONSECUTIVE_RENDER_FAILURES) {
          onFallbackFromRender('render-exception:' + ((e as Error)?.message || String(e)))
        }
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
    lastFallbackReasonRef.current = reason
    // eslint-disable-next-line no-console
    console.warn('[scifi-map] fallback', reason)
    if (rafRef.current != null) { cancelAnimationFrame(rafRef.current); rafRef.current = null }
    try { detachListenersRef.current() } catch { /* ignore */ } // 契約要求：先移除監聽，再 map.remove()
    try { mapRef.current?.remove() } catch { /* ignore */ }
    mapRef.current = null
    try { delete (window as unknown as { __scifiDebug?: unknown }).__scifiDebug } catch { /* ignore */ }
    onFallbackRef.current(reason)
  }

  // docs/skins/ORBPOS_CONTRACT.md §B：最新定位距今幾毫秒沒有更新——有 pos.ts（PAGE 工人新增於
  // scifi/types.ts SciFiPos，epoch ms，本檔不擁有那份型別檔，用安全的執行期存取避免在對方欄位進版前
  // 就編譯失敗）就用它；還沒有這個欄位時退回「pos 最後一次真的改變的時間」（lastPosRef.current.t，
  // performance.now() 時鐘）。兩種時鐘各自在自己的分支內使用，不互相比較。
  function getFixAgeMs(p: SciFiPos | null): number {
    if (!p) return Infinity
    const ts = (p as unknown as { ts?: number }).ts
    if (typeof ts === 'number' && Number.isFinite(ts)) return Math.max(0, Date.now() - ts)
    const last = lastPosRef.current
    return last ? Math.max(0, performance.now() - last.t) : 0
  }

  // 「搜尋中」＝精度差於 MAX_ORB_ACC 或最新定位已超過 STALE_FIX_MS 沒更新（契約 §B 逐字）。
  function isOrbSearching(p: SciFiPos | null): boolean {
    if (!p) return false
    if (typeof p.acc === 'number' && p.acc > MAX_ORB_ACC) return true
    return getFixAgeMs(p) > STALE_FIX_MS
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
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, w, h)

    if (!reducedMotionRef.current) {
      rainRef.current?.update(dt, w, h)
      rainRef.current?.draw(ctx)
    }

    // docs/skins/ORBPOS_CONTRACT.md 第二輪驗收14「setOverlayHidden(bool)：暫時不畫光點／軌跡／徽章」：
    // 只給 E2E 做「有畫 vs 沒畫」前後對照截圖用（同一畫面各截一張比對差異像素），target 圖示（打卡點/
    // 賽事目標）不在契約列舉範圍內，維持照常繪製（比照 CuteMap.tsx 同一段）。
    const overlayHidden = overlayHiddenRef.current
    if (!overlayHidden) {
      drawRoute(ctx, map, segmentsRef.current)
      drawKmMarks(ctx, map, kmMarksRef.current)
    }
    // 2026-09-30 GAP #1 修復：建議路線用獨立的 plannedRouteHiddenRef（不是 overlayHidden，見上方宣告
    // 處），畫在「光點／目標圈」之下、圖磚之上——所以擺在 drawTargets() 之前、soul/trail 之前。
    if (!plannedRouteHiddenRef.current) drawPlannedRoute(ctx, map, plannedRouteRef.current)
    drawTargets(ctx, map, targetsRef.current)

    const p = posRef.current
    // docs/skins/ORBPOS_CONTRACT.md §E：沒有真實定位時完全不畫靈魂——移除舊版「退回 initialCenter／
    // 最後已知位置，用暗淡狀態畫一顆假靈魂」的 fallback（比照 cute/retro 已經是的做法：沒有 p 就不畫
    // 任何角色）。soul.update() 仍照常呼叫，讓呼吸/環繞角度持續推進，下次真的有定位時不會卡格重播。
    const pt = p ? map.project([p.lng, p.lat]) : null
    const soul = soulRef.current, trail = trailRef.current
    if (!reducedMotionRef.current) {
      soul?.update(dt)
      if (p && pt && statusRef.current === 'tracking' && movingTRef.current > 0.12 && trail) {
        const rate = 1 + movingTRef.current * 3
        for (let i = 0; i < rate; i++) if (Math.random() < 0.85) trail.spawn(pt.x, pt.y, dirRef.current.x, dirRef.current.y, 40 + movingTRef.current * 60)
      }
      trail?.update(dt)
    }
    if (!overlayHidden) trail?.draw(ctx)
    if (p && pt) {
      // docs/skins/ORBPOS_CONTRACT.md §B：一律用最新定位畫靈魂（p 就是 posRef.current 這個最新值，不
      // 凍結在舊點）；精度差／久未更新時改成「搜尋中」外觀（青色細圈，見 particles.ts Soul.draw）。
      const searching = isOrbSearching(p)
      const mpp = metersPerPixel(p.lat, map.getZoom())
      const accuracyPx = typeof p.acc === 'number' && mpp > 0 ? Math.min(120, p.acc / mpp) : 40
      if (!overlayHidden) soul?.draw(ctx, pt.x, pt.y, reducedMotionRef.current ? 0 : movingTRef.current, dirRef.current.x, dirRef.current.y, { searching, accuracyPx, reducedMotion: reducedMotionRef.current })
    }
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

  // 2026-09-30「切換風格後路線規劃壞掉」根因修復（GAP #1）：畫出使用者點選打卡點/賽事目標後、由
  // page.tsx routeApi.plan() 算出的建議路線（[lat,lng] 陣列，從目前位置到目的地）。刻意與 drawRoute()
  // 畫的「已跑過的 GPS 軌跡」（青色系 FUG）用完全不同色系＋線型區分——琥珀/橙色（#ffb020）＋虛線發光，
  // 對照 CONTRACT 既有配色只有青色（軌跡/公里標）與洋紅（已完成目標）兩組，橙色目前唯一用途就是「建議
  // 路線」，一眼就能分辨「這是導航建議，不是我已經跑過的路」。終點另外畫一個小標記，比照 drawTargets()
  // 的「光柱+圈」語彙但用同一組琥珀色，代表路線終點＝使用者剛剛點的那個打卡點/賽事目標。
  function drawPlannedRoute(ctx: CanvasRenderingContext2D, map: MapLibreMap, route: [number, number][] | null) {
    if (!route || route.length < 2) return
    // FIX（review 抓到的 minor 根因）：後端 /route（services/api/internal/routing/routing.go Plan()）
    // 直接透傳 ORS 未簡化的完整 geometry，路線較長/較繞（人行步道常見）時點數可能不少於逐秒 GPS 取樣的
    // 軌跡；這裡原本沒有像下面 drawRoute() 那樣 decimate，每一幀都要重新 project() 全部點，與本檔既有
    // 的效能守則（drawRoute() 上限 600 點的註解）不一致。比照 drawRoute() 同一套上限，只影響繪製取樣，
    // plannedRouteRef／__scifiDebug.plannedRoutePoints 等其餘讀取仍是完整原始路線，不受影響。
    const pts = route.length > 600 ? decimate(route, 600) : route
    const screen = pts.map((p) => map.project([p[1], p[0]]))
    ctx.save()
    ctx.globalCompositeOperation = 'lighter'
    ctx.lineJoin = 'round'
    ctx.lineCap = 'round'
    ctx.setLineDash([10, 8])
    ctx.shadowColor = 'rgba(255,176,32,.65)'
    ctx.shadowBlur = 12
    ctx.strokeStyle = 'rgba(255,176,32,.9)'
    ctx.lineWidth = 4
    ctx.beginPath()
    screen.forEach((pt, i) => (i === 0 ? ctx.moveTo(pt.x, pt.y) : ctx.lineTo(pt.x, pt.y)))
    ctx.stroke()
    ctx.setLineDash([])
    ctx.shadowBlur = 0
    ctx.restore()

    // 終點標記：琥珀色雙層圈（外圈發光框線＋內圈實心點），與 drawTargets() 的目標圈同一套視覺語彙但
    // 換色，讓使用者一眼認出「這是我剛規劃的路線終點」。
    const dest = screen[screen.length - 1]
    ctx.save()
    ctx.globalCompositeOperation = 'lighter'
    ctx.beginPath()
    ctx.arc(dest.x, dest.y, 9, 0, Math.PI * 2)
    ctx.strokeStyle = '#ffb020'
    ctx.lineWidth = 2.5
    ctx.shadowColor = '#ffb020'
    ctx.shadowBlur = 10
    ctx.stroke()
    ctx.fillStyle = 'rgba(255,176,32,.25)'
    ctx.shadowBlur = 0
    ctx.fill()
    ctx.beginPath()
    ctx.arc(dest.x, dest.y, 3, 0, Math.PI * 2)
    ctx.fillStyle = '#ffb020'
    ctx.fill()
    ctx.restore()
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
      {/* docs/skins/ORBPOS_CONTRACT.md ★：<canvas> 是可替換元素（replaced element，跟 <img> 同一類），
          position:absolute+inset:0 不會撐滿容器，沒有明確 width/height 時退回內在尺寸（=canvas.width/
          height 這兩個 HTML attribute，即 renderFrame() 設的 backing pixel 尺寸）；DPR>1 手機上整層
          疊層因此放大偏移（根因與詳細說明見 track/retro/RetroMap.tsx 同一處註解，retro 已修，scifi
          同型寫法一併修正）。 */}
      <canvas ref={canvasRef} className={orbitron.className} style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', pointerEvents: 'none', display: 'block' }} />
    </div>
  )
})

export default SciFiMap
