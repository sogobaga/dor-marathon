// 溫馨可愛（cute）GPS 地圖 — MapLibre style JSON 程式產生（docs/skins/CUTE_CONTRACT.md§4）。
// 資料源沿用 scifi/retro 同一份 OpenFreeMap 免金鑰向量圖磚（source-layer 名稱已由 scifi/style.ts、
// retro/style.ts 兩邊實測核對過，這裡不再重新 curl 驗證，直接沿用同一組 source-layer/class 篩選）。
// 與 retro 的差異：cute 要顯示公園／地標名稱標籤（retro 刻意不顯示任何文字），因此本檔仍宣告 glyphs
// （只用於非中文字元的 Latin fallback）＋兩個 symbol 圖層；中文標籤走 CuteMap.tsx 建圖時設定的
// localIdeographFontFamily（'DORCute' 開頭，見該檔），不依賴 CJK glyph 圖磚，比照 scifi/SciFiMap.tsx
// 的既有作法。公園/水域圖案（cute-tree／cute-wave）由 CuteMap.tsx 透過
// map.setMissingStyleImageResolver()（非 'styleimagemissing' 事件，後者有 ImageManager 時序競態，
// 見 CuteMap.tsx 根因註解）呼叫 tiles.ts 的 tileImageData() 產生並 addImage()，本檔只放圖片 id。
import type { StyleSpecification } from 'maplibre-gl'

// 色票：docs/skins/CUTE_CONTRACT_R2b.md §A「地圖底色退一步，讓光點當主角」逐字色號（取代 CONTRACT_R2.md §4.5 第一版
// ——編排者實測 4_track_running.png 抓到第一版陸地/建築/道路 casing 都還太飽和的粉色，跟光點/軌跡
// /公里徽章搶顏色，這裡整組再退淡一階）。本檔獨立宣告（不 import components/cute/decor.ts 的
// CUTE_PALETTE），理由同 orb.ts／icons.ts 頂端註解。
const LAND = '#fffafc'
const BUILDING = '#f8eef3'
const BUILDING_LINE = '#ecd6e0' // 1px 細框
const ROAD_CASING = '#eedfe6' // R2b「不再用粉色外框」：改成近乎無彩度的極淡藕色
const ROAD_FILL = '#ffffff'
const WATERWAY = '#b9d9ef' // 水域線（河流）同步退淡，避免比 #dcefff 的水域面還搶眼
const LABEL_INK = '#9a6a7e' // docs/skins/CUTE_CONTRACT_R2b.md §A 逐字地名色號
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

      // 道路：白色主體＋極淡藕色 casing（先畫較寬的 casing 在下面當邊框，再疊白色主線在上面），由窄到
      // 寬依序疊（path→minor→mid→主幹道較寬），交叉口讓主幹道蓋在最上面。docs/skins/CUTE_CONTRACT_R2b.md §A「不再用
      // 粉色外框」：casing 改 #eedfe6（比 R2 第一版的 #f3b6cc 更淡、更低彩度），本身已經夠淺，不需要
      // 再疊 opacity 洗淡。
      {
        id: 'cute-road-path-casing', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation',
        filter: ['match', ['get', 'class'], ['path', 'footway', 'cycleway', 'steps', 'pedestrian', 'track'], true, false],
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-color': ROAD_CASING, 'line-width': widthAtZ16(3) },
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
        paint: { 'line-color': ROAD_CASING, 'line-width': widthAtZ16(5) },
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
        paint: { 'line-color': ROAD_CASING, 'line-width': widthAtZ16(6) },
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
        paint: { 'line-color': ROAD_CASING, 'line-width': widthAtZ16(9) },
      },
      {
        id: 'cute-road-main', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation',
        filter: ['match', ['get', 'class'], ['primary', 'secondary', 'trunk', 'motorway'], true, false],
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-color': ROAD_FILL, 'line-width': widthAtZ16(7) },
      },

      // 建築（近白淡粉＋1px 細框——docs/skins/CUTE_CONTRACT_R2b.md §A 再退淡一階，不再是 R2 第一版還偏飽和的
      // #ffe0ec／#e3a9c0，1px 就足夠、不需要再洗淡透明度）。
      { id: 'cute-building', type: 'fill', source: 'openmaptiles', 'source-layer': 'building', minzoom: 13, paint: { 'fill-color': BUILDING } },
      { id: 'cute-building-outline', type: 'line', source: 'openmaptiles', 'source-layer': 'building', minzoom: 13, paint: { 'line-color': BUILDING_LINE, 'line-width': 1 } },

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
