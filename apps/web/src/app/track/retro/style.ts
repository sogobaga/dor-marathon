// 復古 RPG（retro）GPS 地圖 — MapLibre style JSON 程式產生（CONTRACT.md §4）。
// 沿用 scifi 同一份 OpenFreeMap 免金鑰向量圖磚資料源（source-layer 名稱已由 scifi/style.ts 核對過），
// 但完全不接圖磚字型/標籤（大地圖沒有字，見 §4「不顯示任何文字標籤」）——本檔沒有 glyphs/symbol 圖層。
// 草地/森林/水域/建築/道路全部走 fill-pattern／line-pattern，圖片由 RetroMap.tsx 於
// 'styleimagemissing' 事件時呼叫 components/retro/tiles.ts 的 drawTile() 產生並 map.addImage() 掛上
// （本檔只放圖片 id，id 與 tiles.ts 的 TileKind 同名，見該檔／sprites.ts tileImageData()）。
import type { StyleSpecification } from 'maplibre-gl'

const GRASS_BG = '#3cbc3c' // 圖片尚未載入前的 fallback 底色（與 grass 圖塊主色一致，不會露餡）
const WATERWAY_COLOR = '#0078f8'

// z16 目標寬度（CSS px）展開成完整 zoom interpolate stops，比照 scifi/style.ts widthAtZ16 的作法。
function widthAtZ16(z16: number): unknown[] {
  return ['interpolate', ['linear'], ['zoom'], 10, z16 * 0.3, 14, z16 * 0.7, 16, z16, 18, z16 * 1.4, 20, z16 * 2]
}

export function buildRetroStyle(): StyleSpecification {
  return {
    version: 8,
    name: 'dor-retro',
    // 不設 glyphs：本風格沒有任何 symbol/文字圖層，MapLibre 不會因此嘗試抓字型圖磚。
    sources: {
      openmaptiles: { type: 'vector', url: 'https://tiles.openfreemap.org/planet' },
    },
    layers: [
      { id: 'background', type: 'background', paint: { 'background-color': GRASS_BG } },
      // R2 FIX（findings #5）：retro-landuse 原本排在 retro-landcover-wood（森林修復）之後，對「全部」
      // landuse class 不分青紅皂白疊 0.6 透明度沙地圖塊——只要驗收地點的 landuse 面資料剛好蓋在
      // wood/park 區之上，半透明沙色會疊在森林貼圖上面，稀釋本輪要驗收的像素占比。landuse 在
      // OpenMapTiles 裡代表 residential/commercial/cemetery 這類「沒有更具體圖層可畫」的兜底地面，
      // 移到最前面（background 之後、所有具體地物之前）才符合它「兜底」的定位：grass/wood/park/
      // water/building/road 這些更具體的圖層排在後面、天然疊在它上面蓋掉，沙地只在真的沒有其他
      // 地物覆蓋的地方透出來，不會再蓋過已修好的森林／草地分類。
      { id: 'retro-landuse', type: 'fill', source: 'openmaptiles', 'source-layer': 'landuse', paint: { 'fill-pattern': 'sand', 'fill-opacity': 0.6 } },
      { id: 'retro-landcover', type: 'fill', source: 'openmaptiles', 'source-layer': 'landcover', paint: { 'fill-pattern': 'grass' } },
      // 森林／樹林：CONTRACT_R2 §1 根因調查（querySourceFeatures 逐一比對 park／landuse／landcover
      // 等 source-layer 實際內容，證據見交付回報）證實 OpenFreeMap 這份 OpenMapTiles 資料裡，
      // 「森林」實際上是 `landcover` source-layer 底下 `class==='wood'` 的多邊形（例如大安森林公園
      // 有樹的區塊），不在 `park` source-layer 裡（那裡只有史蹟/野雁保護區之類的點狀record，沒有
      // 公園本體的面資料）；上面 retro-landcover 沒有 class 篩選，把 wood 也塗成了純草地 grass，
      // 導致森林紋理永遠不會被要求（styleimagemissing 永遠不會替 'forest' 觸發，hasImage('forest')
      // 全程 false）。加這層在 retro-landcover 之後（疊在草地之上）依 class 篩出 wood 改塗 forest
      // 圖塊；retro-park 保留（部分城市/更低 zoom 可能真的有面資料，留著無害，不影響本層）。
      { id: 'retro-landcover-wood', type: 'fill', source: 'openmaptiles', 'source-layer': 'landcover', filter: ['==', ['get', 'class'], 'wood'], paint: { 'fill-pattern': 'forest' } },
      { id: 'retro-park', type: 'fill', source: 'openmaptiles', 'source-layer': 'park', paint: { 'fill-pattern': 'forest' } },
      { id: 'retro-water', type: 'fill', source: 'openmaptiles', 'source-layer': 'water', paint: { 'fill-pattern': 'water' } },
      { id: 'retro-waterway', type: 'line', source: 'openmaptiles', 'source-layer': 'waterway', paint: { 'line-color': WATERWAY_COLOR, 'line-width': widthAtZ16(2) } },

      // 一般道路／步道（土黃小徑圖塊 line-pattern）：細到中等寬度，由窄到寬疊圖讓交叉口正確蓋過。
      {
        id: 'retro-road-path', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation',
        filter: ['match', ['get', 'class'], ['path', 'footway', 'cycleway', 'steps', 'pedestrian', 'track'], true, false],
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-pattern': 'path', 'line-width': widthAtZ16(3) },
      },
      {
        id: 'retro-road-minor', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation',
        filter: ['match', ['get', 'class'], ['minor', 'service'], true, false],
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-pattern': 'path', 'line-width': widthAtZ16(4) },
      },
      {
        id: 'retro-road-mid', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation',
        filter: ['match', ['get', 'class'], ['tertiary'], true, false],
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-pattern': 'cobble', 'line-width': widthAtZ16(4) },
      },
      // 主幹道（紅色石板路 line-pattern）：先畫黑色外框寬線再疊石板貼圖線，模擬石板路的黑縫勾邊。
      {
        id: 'retro-road-main-outline', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation',
        filter: ['match', ['get', 'class'], ['primary', 'secondary', 'trunk', 'motorway'], true, false],
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-color': '#000000', 'line-width': widthAtZ16(8) },
      },
      {
        id: 'retro-road-main', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation',
        filter: ['match', ['get', 'class'], ['primary', 'secondary', 'trunk', 'motorway'], true, false],
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-pattern': 'cobble', 'line-width': widthAtZ16(6) },
      },
      // 鐵道：灰黑枕木圖塊（tiles.ts 'rail'）。
      {
        id: 'retro-rail', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation',
        filter: ['match', ['get', 'class'], ['rail'], true, false],
        paint: { 'line-pattern': 'rail', 'line-width': widthAtZ16(3) },
      },

      // 建築（石牆城鎮）：fill-pattern＋黑色勾邊，minzoom 13 起才畫（比照 scifi 3D 建築的起始 zoom）。
      {
        id: 'retro-building', type: 'fill', source: 'openmaptiles', 'source-layer': 'building', minzoom: 13,
        paint: { 'fill-pattern': 'wall', 'fill-outline-color': '#000000' },
      },
    ],
  } as unknown as StyleSpecification
}
