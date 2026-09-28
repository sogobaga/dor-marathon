// 溫馨可愛（cute）GPS 地圖 — MapLibre style JSON 程式產生（docs/skins/CUTE_CONTRACT.md§4）。
// 資料源沿用 scifi/retro 同一份 OpenFreeMap 免金鑰向量圖磚（source-layer 名稱已由 scifi/style.ts、
// retro/style.ts 兩邊實測核對過，這裡不再重新 curl 驗證，直接沿用同一組 source-layer/class 篩選）。
// 與 retro 的差異：cute 要顯示公園／地標名稱標籤（retro 刻意不顯示任何文字），因此本檔仍宣告 glyphs
// （只用於非中文字元的 Latin fallback）＋兩個 symbol 圖層；中文標籤走 CuteMap.tsx 建圖時設定的
// localIdeographFontFamily（'DORCute' 開頭，見該檔），不依賴 CJK glyph 圖磚，比照 scifi/SciFiMap.tsx
// 的既有作法。公園/水域圖案（cute-tree／cute-wave）由 CuteMap.tsx 在 'styleimagemissing' 時呼叫
// tiles.ts 的 tileImageData() 產生並 addImage()，本檔只放圖片 id。
import type { StyleSpecification } from 'maplibre-gl'

const LAND = '#fff4df'
const BUILDING = '#ffe3d8'
const BUILDING_LINE = '#3d2b2b'
const ROAD_CASING = '#3d2b2b'
const ROAD_FILL = '#ffffff'
const WATERWAY = '#8ecbe8'
const LABEL_INK = '#3d2b2b'
const LABEL_HALO = '#ffffff'

// z16 目標寬度（CSS px）展開成完整 zoom interpolate stops，比照 scifi/retro 的 widthAtZ16 作法。
function widthAtZ16(z16: number): unknown[] {
  return ['interpolate', ['linear'], ['zoom'], 10, z16 * 0.3, 14, z16 * 0.7, 16, z16, 18, z16 * 1.4, 20, z16 * 2]
}

export function buildCuteStyle(): StyleSpecification {
  return {
    version: 8,
    name: 'dor-cute',
    glyphs: 'https://tiles.openfreemap.org/fonts/{fontstack}/{range}.pbf',
    sources: {
      openmaptiles: { type: 'vector', url: 'https://tiles.openfreemap.org/planet' },
    },
    layers: [
      { id: 'background', type: 'background', paint: { 'background-color': LAND } },

      // 公園／綠地（薄荷＋小圓樹點點）：與 retro-landcover-wood 同一個根因教訓——OpenFreeMap 這份
      // OpenMapTiles 資料裡「森林/樹林」實際落在 landcover source-layer 的 class==='wood'，公園面積
      // 也可能落在 landcover class==='grass' 或獨立的 park source-layer，三者都算進「公園／綠地」
      // 這個單一視覺類別（契約沒有像 retro 那樣要求分開森林/草地兩種質感）。
      { id: 'cute-park', type: 'fill', source: 'openmaptiles', 'source-layer': 'park', paint: { 'fill-pattern': 'cute-tree' } },
      { id: 'cute-landcover-green', type: 'fill', source: 'openmaptiles', 'source-layer': 'landcover', filter: ['match', ['get', 'class'], ['wood', 'grass', 'forest'], true, false], paint: { 'fill-pattern': 'cute-tree' } },

      // 水域（天空藍＋小波浪）。
      { id: 'cute-water', type: 'fill', source: 'openmaptiles', 'source-layer': 'water', paint: { 'fill-pattern': 'cute-wave' } },
      { id: 'cute-waterway', type: 'line', source: 'openmaptiles', 'source-layer': 'waterway', paint: { 'line-color': WATERWAY, 'line-width': widthAtZ16(2) } },

      // 道路：白色主體＋墨線 casing（先畫較寬的墨線在下面當邊框，再疊白色主線在上面），由窄到寬依序
      // 疊（path→minor→mid→主幹道較寬），交叉口讓主幹道蓋在最上面。
      {
        id: 'cute-road-path-casing', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation',
        filter: ['match', ['get', 'class'], ['path', 'footway', 'cycleway', 'steps', 'pedestrian', 'track'], true, false],
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-color': ROAD_CASING, 'line-width': widthAtZ16(3), 'line-opacity': 0.55 },
      },
      {
        id: 'cute-road-path', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation',
        filter: ['match', ['get', 'class'], ['path', 'footway', 'cycleway', 'steps', 'pedestrian', 'track'], true, false],
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-color': ROAD_FILL, 'line-width': widthAtZ16(2) },
      },
      {
        id: 'cute-road-minor-casing', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation',
        filter: ['match', ['get', 'class'], ['minor', 'service'], true, false],
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-color': ROAD_CASING, 'line-width': widthAtZ16(5), 'line-opacity': 0.55 },
      },
      {
        id: 'cute-road-minor', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation',
        filter: ['match', ['get', 'class'], ['minor', 'service'], true, false],
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-color': ROAD_FILL, 'line-width': widthAtZ16(3.5) },
      },
      {
        id: 'cute-road-mid-casing', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation',
        filter: ['match', ['get', 'class'], ['tertiary'], true, false],
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-color': ROAD_CASING, 'line-width': widthAtZ16(6), 'line-opacity': 0.55 },
      },
      {
        id: 'cute-road-mid', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation',
        filter: ['match', ['get', 'class'], ['tertiary'], true, false],
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-color': ROAD_FILL, 'line-width': widthAtZ16(4.5) },
      },
      // 主幹道（較寬）。
      {
        id: 'cute-road-main-casing', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation',
        filter: ['match', ['get', 'class'], ['primary', 'secondary', 'trunk', 'motorway'], true, false],
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-color': ROAD_CASING, 'line-width': widthAtZ16(9), 'line-opacity': 0.55 },
      },
      {
        id: 'cute-road-main', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation',
        filter: ['match', ['get', 'class'], ['primary', 'secondary', 'trunk', 'motorway'], true, false],
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-color': ROAD_FILL, 'line-width': widthAtZ16(7) },
      },

      // 建築（淡桃＋1.5px 墨線外框，opacity .55——契約逐字）。
      { id: 'cute-building', type: 'fill', source: 'openmaptiles', 'source-layer': 'building', minzoom: 13, paint: { 'fill-color': BUILDING } },
      { id: 'cute-building-outline', type: 'line', source: 'openmaptiles', 'source-layer': 'building', minzoom: 13, paint: { 'line-color': BUILDING_LINE, 'line-width': 1.5, 'line-opacity': 0.55 } },

      // 標籤：只顯示 z≥15 的公園／地標名稱（契約「只顯示 z ≥ 15 的 POI／公園名」），中文字型走
      // localIdeographFontFamily='DORCute'（CuteMap.tsx 建圖時設定），這裡的 text-font 只服務
      // 非中文字元（標點/英數）走 Noto Sans Regular（比照 scifi/style.ts）。
      {
        id: 'cute-park-label', type: 'symbol', source: 'openmaptiles', 'source-layer': 'park', minzoom: 15,
        filter: ['has', 'name'],
        layout: { 'text-field': ['coalesce', ['get', 'name'], ['get', 'name:latin']], 'text-font': ['Noto Sans Regular'], 'text-size': 12 },
        paint: { 'text-color': LABEL_INK, 'text-halo-color': LABEL_HALO, 'text-halo-width': 1.6 },
      },
      {
        id: 'cute-poi-label', type: 'symbol', source: 'openmaptiles', 'source-layer': 'poi', minzoom: 15,
        filter: ['all', ['has', 'name'], ['<=', ['coalesce', ['get', 'rank'], 10], 15]],
        layout: { 'text-field': ['coalesce', ['get', 'name'], ['get', 'name:latin']], 'text-font': ['Noto Sans Regular'], 'text-size': 11, 'text-max-width': 6 },
        paint: { 'text-color': LABEL_INK, 'text-halo-color': LABEL_HALO, 'text-halo-width': 1.4 },
      },
    ],
  } as unknown as StyleSpecification
}
